package sync

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

const DefaultTokenRef = "sync/default/token"
const DefaultPassphraseRef = "sync/default/passphrase"

type SecretReader interface{ Get(string) (string, bool) }
type SecretStore interface {
	SecretReader
	PutMany(map[string]string) error
}

type Runner struct {
	Store           *store.Store
	Secrets         SecretStore
	Scope           string
	TokenRef        string
	PassphraseRef   string
	SecretSelection *SecretSelection
}

func (r Runner) RunOnce(ctx context.Context) error {
	settings, err := r.Store.SyncSettings(ctx, r.scope())
	if err != nil {
		return err
	}
	token, ok := r.Secrets.Get(r.TokenRef)
	if !ok || token == "" {
		return fmt.Errorf("sync token is not configured")
	}
	client := &Client{BaseURL: settings.Endpoint, DeviceID: settings.DeviceID, Credential: func(context.Context) (string, error) { return token, nil }}
	engine := &Engine{Store: r.Store, Remote: client, Scope: r.scope(), DeviceID: settings.DeviceID}
	if err := r.Store.ClaimLocalWorkspaces(ctx, settings.DeviceID); err != nil {
		return err
	}
	RegisterBusinessHandlers(engine, r.Store)
	passphraseRef := r.PassphraseRef
	if passphraseRef == "" {
		passphraseRef = DefaultPassphraseRef
	}
	passphrase, passphraseConfigured := r.Secrets.Get(passphraseRef)
	if passphraseConfigured && passphrase != "" {
		RegisterSecretBundleHandler(engine, r.Store, r.Secrets, passphrase)
	}
	state, err := r.Store.SyncState(ctx, r.scope())
	if err != nil {
		return err
	}
	if state.ReplayRequired {
		// Preserve changes that were already queued before the upgrade, then replay
		// the center's ordered history before exporting the current local snapshot.
		if err := engine.Push(ctx); err != nil {
			return err
		}
		pending, err := r.Store.SyncOutboxCount(ctx, r.scope())
		if err != nil {
			return err
		}
		if pending > 0 {
			return fmt.Errorf("sync replay is waiting for %d pending local changes", pending)
		}
		if err := engine.Pull(ctx); err != nil {
			return err
		}
		if err := r.Store.CompleteSyncReplay(ctx, r.scope()); err != nil {
			return err
		}
	}
	objects, err := ExportBusinessObjectsForDevice(ctx, r.Store, settings.DeviceID)
	if err != nil {
		return err
	}
	for _, object := range objects {
		applied, err := r.Store.SyncWasApplied(ctx, object.IdempotencyKey)
		if err != nil {
			return err
		}
		if !applied {
			if err := r.Store.EnqueueSync(ctx, domain.SyncOutboxItem{Scope: r.scope(), Object: object}); err != nil {
				return err
			}
		}
	}
	selection := r.SecretSelection
	if selection == nil && len(settings.ProviderIDs)+len(settings.EnvGroupIDs)+len(settings.CodexAccountIDs) > 0 {
		selection = &SecretSelection{ProviderIDs: settings.ProviderIDs, EnvGroupIDs: settings.EnvGroupIDs, CodexAccountIDs: settings.CodexAccountIDs}
	}
	if selection != nil && passphraseConfigured && passphrase != "" {
		bundle, bundleErr := ExportSecretBundle(ctx, r.Store, r.Secrets, *selection, passphrase)
		if bundleErr != nil && !errors.Is(bundleErr, ErrNoPortableSecrets) {
			return bundleErr
		}
		if bundleErr == nil {
			applied, applyErr := r.Store.SyncWasApplied(ctx, bundle.IdempotencyKey)
			if applyErr != nil {
				return applyErr
			}
			if applied {
				bundleErr = ErrNoPortableSecrets
			}
		}
		if bundleErr == nil {
			if enqueueErr := r.Store.EnqueueSync(ctx, domain.SyncOutboxItem{Scope: r.scope(), Object: bundle}); enqueueErr != nil {
				return enqueueErr
			}
		}
	}
	if err := engine.Push(ctx); err != nil {
		return err
	}
	return engine.Pull(ctx)
}

func (r Runner) Loop(ctx context.Context) {
	for {
		settings, err := r.Store.SyncSettings(ctx, r.scope())
		interval := 5 * time.Minute
		if err == nil && settings.IntervalSeconds >= 10 {
			interval = time.Duration(settings.IntervalSeconds) * time.Second
		}
		if err == nil && settings.Enabled {
			run, cancel := context.WithTimeout(ctx, 45*time.Second)
			err = r.RunOnce(run)
			cancel()
			state, _ := r.Store.SyncState(context.Background(), r.scope())
			if err != nil {
				state.LastError = err.Error()
			} else {
				state.LastError = ""
			}
			state.UpdatedAt = time.Now().UTC()
			_ = r.Store.UpdateSyncState(context.Background(), state)
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (r Runner) scope() string {
	if r.Scope != "" {
		return r.Scope
	}
	return "default"
}
