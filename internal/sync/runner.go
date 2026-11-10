package sync

import (
	"context"
	"errors"
	"fmt"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

const DefaultTokenRef = "sync/default/token"
const DefaultPassphraseRef = "sync/default/passphrase"

type SecretReader interface{ Get(string) (string, bool) }
type SecretStore interface {
	SecretReader
	PutMany(map[string]string) error
	DeleteMany([]string) error
}

type Runner struct {
	Store           *store.Store
	Secrets         SecretStore
	Scope           string
	TokenRef        string
	PassphraseRef   string
	SecretSelection *SecretSelection
	Progress        func(phase, detail string, completed int)
	// OnPreview is called by RunOnce after it has computed the same summary
	// Preview would report. Scheduled runs set it so the authoritative export
	// is not repeated just to fill the progress summary.
	OnPreview func(Preview)
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
	objects, err := ExportPendingBusinessObjectsForDevice(ctx, r.Store, settings.DeviceID)
	if err != nil {
		return Preview{}, err
	}
	result := Preview{}
	for _, object := range objects {
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
	if err := r.previewRemote(ctx, settings, &result); err != nil {
		return Preview{}, err
	}
	return result, nil
}

// previewRemote fills the count of remote objects that are not yet applied.
// It never writes sync state; the real pull happens later in RunOnce.
func (r Runner) previewRemote(ctx context.Context, settings domain.SyncSettings, result *Preview) error {
	if r.Secrets == nil || settings.Endpoint == "" || settings.DeviceID == "" {
		return nil
	}
	tokenRef := r.TokenRef
	if tokenRef == "" {
		tokenRef = DefaultTokenRef
	}
	token, ok := r.Secrets.Get(tokenRef)
	if !ok || token == "" {
		return nil
	}
	state, err := r.Store.SyncState(ctx, r.scope())
	if err != nil {
		return err
	}
	client := &Client{BaseURL: settings.Endpoint, DeviceID: settings.DeviceID, Credential: func(context.Context) (string, error) { return token, nil }}
	cursor := state.Cursor
	for page := 0; page < 40; page++ {
		response, pullErr := client.Pull(ctx, r.scope(), cursor, settings.DeviceID, 500)
		if pullErr != nil {
			return pullErr
		}
		deliveryKeys := make([]string, len(response.Objects))
		for index, object := range response.Objects {
			deliveryKey := object.EventID
			if deliveryKey == "" && object.RemoteVersion != "" {
				deliveryKey = fmt.Sprintf("center-object:%s:%s:%s", object.Type, object.ID, object.RemoteVersion)
			}
			if deliveryKey == "" {
				deliveryKey = object.IdempotencyKey
			}
			deliveryKeys[index] = deliveryKey
		}
		deliveredKeys, checkErr := r.Store.SyncAppliedKeys(ctx, deliveryKeys)
		if checkErr != nil {
			return checkErr
		}
		for index, object := range response.Objects {
			if _, delivered := deliveredKeys[deliveryKeys[index]]; delivered {
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
	return nil
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
	var preview Preview
	if r.OnPreview != nil {
		r.report("preview", "分析差异", 0)
		if err := r.previewRemote(ctx, settings, &preview); err != nil {
			return err
		}
	}
	client := &Client{BaseURL: settings.Endpoint, DeviceID: settings.DeviceID, Credential: func(context.Context) (string, error) { return token, nil }}
	engine := &Engine{Store: r.Store, Remote: client, Scope: r.scope(), DeviceID: settings.DeviceID, Report: r.Progress}
	if err := r.Store.ClaimLocalWorkspaces(ctx, settings.DeviceID); err != nil {
		return err
	}
	if err := r.Store.PurgeOwnRemoteMirrors(ctx, settings.DeviceID); err != nil {
		return err
	}
	RegisterBusinessHandlersForDevice(engine, r.Store, settings.DeviceID)
	RegisterCodexAccountTombstoneHandler(engine, r.Store, r.Secrets)
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
		r.report("replaying", "回放远端历史", 0)
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
	// The remote preview scan (when scheduled) is the only work left in the
	// preview phase; the authoritative export belongs to preparing.
	r.report("preparing", "分析差异", 0)
	// The export already excludes identities recorded as applied. EnqueueSync
	// still deduplicates by idempotency key, so an object applied by a
	// concurrent writer between the export and this loop is harmless.
	objects, err := ExportPendingBusinessObjectsForDevice(ctx, r.Store, settings.DeviceID)
	if err != nil {
		return err
	}
	if r.OnPreview != nil {
		for _, object := range objects {
			if object.Operation == "delete" {
				preview.Deletes++
			} else {
				preview.Upserts++
			}
		}
		preview.Pending, err = r.Store.SyncOutboxCount(ctx, r.scope())
		if err != nil {
			return err
		}
		conflicts, conflictErr := r.Store.SyncConflicts(ctx, r.scope())
		if conflictErr != nil {
			return conflictErr
		}
		preview.Conflicts = len(conflicts)
		r.OnPreview(preview)
		r.report("preparing", "写入待上传变更", 0)
	}
	for _, object := range objects {
		if err := r.Store.EnqueueSync(ctx, domain.SyncOutboxItem{Scope: r.scope(), Object: object}); err != nil {
			return err
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
	r.report("pushing", "上传本机变更", 0)
	pending, err := r.Store.SyncOutboxCount(ctx, r.scope())
	if err != nil {
		return err
	}
	if pending == 0 {
		r.report("pushing", "本机无待上传变更，跳过上传", 0)
	} else if err := r.drainPending(ctx, engine); err != nil {
		return err
	}
	r.report("pulling", "拉取远端变化", 0)
	if err := engine.Pull(ctx); err != nil {
		return err
	}
	r.report("finalizing", "确认最终状态", 0)
	r.report("complete", "同步完成", 0)
	return nil
}

func (r Runner) report(phase, detail string, completed int) {
	if r.Progress != nil {
		r.Progress(phase, detail, completed)
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

func (r Runner) scope() string {
	if r.Scope != "" {
		return r.Scope
	}
	return "default"
}
