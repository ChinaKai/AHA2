package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestDeleteCodexAccountCreatesTombstoneAndLocalUpsertClearsIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	account := domain.CodexAccount{
		ID: "codex-account-tombstone", Label: "Work", Status: "ready",
		CredentialRef: "codex-account/codex-account-tombstone/auth", CredentialConfigured: true,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertCodexAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteCodexAccount(ctx, account.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CodexAccount(ctx, account.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted Codex account remains visible: %v", err)
	}
	tombstone, err := database.SyncTombstone(ctx, "codex_account", account.ID)
	if err != nil || tombstone.Version == "" {
		t.Fatalf("Codex account tombstone=%#v err=%v", tombstone, err)
	}

	account.UpdatedAt = now.Add(time.Minute)
	if err := database.UpsertCodexAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SyncTombstone(ctx, "codex_account", account.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("local re-import did not clear tombstone: %v", err)
	}
}
