package sync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

// RegisterCodexAccountTombstoneHandler applies the account deletes exported by
// ExportBusinessObjectsForDevice. Codex accounts are not business objects: their
// metadata arrives in secret bundles, so deletion needs this dedicated handler
// to remove both the account row and its encrypted credential.
func RegisterCodexAccountTombstoneHandler(engine *Engine, database *store.Store, secrets SecretReaderWriter) {
	engine.Register(TypeCodexAccount, func(ctx context.Context, obj domain.SyncObject) error {
		if obj.Operation != "delete" {
			return fmt.Errorf("unsupported Codex account sync operation %q", obj.Operation)
		}
		account, err := database.CodexAccount(ctx, obj.ID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := database.ApplySyncTombstone(ctx, TypeCodexAccount, obj.ID, obj.IdempotencyKey, obj.SourceVersion, time.Now().UTC()); err != nil {
			return err
		}
		refs := []string{codexAccountSecretRef(obj.ID)}
		if account.CredentialRef != "" && account.CredentialRef != refs[0] {
			refs = append(refs, account.CredentialRef)
		}
		if secrets != nil {
			if err := secrets.DeleteMany(refs); err != nil {
				return err
			}
		}
		return nil
	})
}
