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
	Progress        func(phase string)
}

type Preview struct {
	Upserts         int  `json:"upserts"`
	Deletes         int  `json:"deletes"`
	RemoteUpserts   int  `json:"remote_upserts"`
	RemoteDeletes   int  `json:"remote_deletes"`
	RemoteTruncated bool `json:"remote_truncated,omitempty"`
	Pending         int  `json:"pending"`
	Conflicts       int  `json:"conflicts"`
}

func (r Runner) Preview(ctx context.Context) (Preview, error) {
	settings, err := r.Store.SyncSettings(ctx, r.scope())
	if err != nil {
		return Preview{}, err
	}
	objects, err := ExportBusinessObjectsForDevice(ctx, r.Store, settings.DeviceID)
	if err != nil {
		return Preview{}, err
	}
	result := Preview{}
	for _, object := range objects {
		applied, err := r.Store.SyncWasApplied(ctx, object.IdempotencyKey)
		if err != nil {
			return Preview{}, err
		}
		if applied {
			continue
		}
		if object.Operation == "delete" {
			result.Deletes++
		} else {
			result.Upserts++
		}
	}
	result.Pending, err = r.Store.SyncOutboxCount(ctx, r.scope())
	if err != nil {
		return Preview{}, err
	}
	conflicts, err := r.Store.SyncConflicts(ctx, r.scope())
	if err != nil {
		return Preview{}, err
	}
	result.Conflicts = len(conflicts)
	if r.Secrets != nil && settings.Endpoint != "" && settings.DeviceID != "" {
		tokenRef := r.TokenRef
		if tokenRef == "" {
			tokenRef = DefaultTokenRef
		}
		if token, ok := r.Secrets.Get(tokenRef); ok && token != "" {
			state, stateErr := r.Store.SyncState(ctx, r.scope())
			if stateErr != nil {
				return Preview{}, stateErr
			}
			client := &Client{BaseURL: settings.Endpoint, DeviceID: settings.DeviceID, Credential: func(context.Context) (string, error) { return token, nil }}
			cursor := state.Cursor
			for page := 0; page < 40; page++ {
				response, pullErr := client.Pull(ctx, r.scope(), cursor, settings.DeviceID, 500)
				if pullErr != nil {
					return Preview{}, pullErr
				}
				for _, object := range response.Objects {
					deliveryKey := object.EventID
					if deliveryKey == "" && object.RemoteVersion != "" {
						deliveryKey = fmt.Sprintf("center-object:%s:%s:%s", object.Type, object.ID, object.RemoteVersion)
					}
					if deliveryKey == "" {
						deliveryKey = object.IdempotencyKey
					}
					delivered, checkErr := r.Store.SyncWasApplied(ctx, deliveryKey)
					if checkErr != nil {
						return Preview{}, checkErr
					}
					if delivered {
						continue
					}
					if object.Operation == "delete" {
						result.RemoteDeletes++
					} else {
						result.RemoteUpserts++
					}
				}
				cursor = response.Cursor
				if !response.HasMore {
					break
				}
				if page == 39 {
					result.RemoteTruncated = true
				}
			}
		}
	}
	return result, nil
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
	if err := r.Store.PurgeOwnRemoteMirrors(ctx, settings.DeviceID); err != nil {
		return err
	}
	RegisterBusinessHandlersForDevice(engine, r.Store, settings.DeviceID)
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
		r.report("replaying")
		// Preserve changes that were already queued before the upgrade, then replay
		// the center's ordered history before exporting the current local snapshot.
		if err := r.drainPending(ctx, engine); err != nil {
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
	r.report("preparing")
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
	if selection == nil && passphraseConfigured && passphrase != "" {
		discovered, selectionErr := PortableSecretSelection(ctx, r.Store)
		if selectionErr != nil {
			return selectionErr
		}
		selection = &discovered
	}
	if selection != nil && passphraseConfigured && passphrase != "" {
		bundle, bundleErr := ExportSecretBundle(ctx, r.Store, r.Secrets, *selection, passphrase)
		if err := r.enqueueSecretBundle(ctx, bundle, bundleErr); err != nil {
			return err
		}
		infrastructure, infrastructureErr := ExportInfrastructureSecretBundle(ctx, r.Store, r.Secrets, settings.DeviceID, passphrase)
		if err := r.enqueueSecretBundle(ctx, infrastructure, infrastructureErr); err != nil {
			return err
		}
	}
	r.report("pushing")
	if err := r.drainPending(ctx, engine); err != nil {
		return err
	}
	r.report("pulling")
	if err := engine.Pull(ctx); err != nil {
		return err
	}
	r.report("finalizing")
	r.report("complete")
	return nil
}

func (r Runner) report(phase string) {
	if r.Progress != nil {
		r.Progress(phase)
	}
}

func (r Runner) enqueueSecretBundle(ctx context.Context, bundle domain.SyncObject, bundleErr error) error {
	if bundleErr != nil {
		if errors.Is(bundleErr, ErrNoPortableSecrets) {
			return nil
		}
		return bundleErr
	}
	applied, err := r.Store.SyncWasApplied(ctx, bundle.IdempotencyKey)
	if err != nil || applied {
		return err
	}
	return r.Store.EnqueueSync(ctx, domain.SyncOutboxItem{Scope: r.scope(), Object: bundle})
}

func (r Runner) drainPending(ctx context.Context, engine *Engine) error {
	for {
		before, err := r.Store.SyncOutboxCount(ctx, r.scope())
		if err != nil || before == 0 {
			return err
		}
		if err := engine.Push(ctx); err != nil {
			return err
		}
		after, err := r.Store.SyncOutboxCount(ctx, r.scope())
		if err != nil {
			return err
		}
		if after >= before {
			// Conflicted or backoff-delayed items remain pending for a later run.
			return nil
		}
	}
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
