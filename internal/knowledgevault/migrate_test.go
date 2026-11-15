package knowledgevault

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateLegacyKnowledgeBacksUpVerifiesAndKeepsOldData(t *testing.T) {
	ctx := context.Background()
	database, databasePath := openTestStore(t)
	seedStandardVaultDatabase(t, database)
	dataDir := filepath.Dir(databasePath)
	config, err := DefaultConfig(dataDir).Normalized()
	if err != nil {
		t.Fatal(err)
	}
	inventoryBefore, err := database.LegacyKnowledgeInventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if inventoryBefore.Entries == 0 {
		t.Fatal("test fixture has no substantive legacy knowledge")
	}

	report, err := MigrateLegacyKnowledge(ctx, config, LegacyMigrationOptions{
		DataDir: dataDir, DatabasePath: databasePath, Commit: false,
	})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if report.SourceEntries < inventoryBefore.Entries || report.VerifiedEntries != report.SourceEntries {
		t.Fatalf("migration report = %+v (inventory %+v)", report, inventoryBefore)
	}
	if report.TitlesVerified == 0 || report.HashesVerified == 0 || len(report.SampledEntries) == 0 {
		t.Fatalf("verification counts = %+v", report)
	}
	if report.VerifiedAt.IsZero() || report.BackupPath == "" {
		t.Fatalf("missing verification/backup metadata: %+v", report)
	}
	if _, err := os.Stat(filepath.Join(report.BackupPath, "aha2.db")); err != nil {
		t.Fatalf("database backup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(config.VaultPath, manifestFileName)); err != nil {
		t.Fatalf("vault manifest: %v", err)
	}
	index, err := OpenIndex(ctx, config.IndexPath)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := index.Entries(ctx, "", "")
	_ = index.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < inventoryBefore.Entries {
		t.Fatalf("indexed %d entries, want at least %d", len(entries), inventoryBefore.Entries)
	}
	inventoryAfter, err := database.LegacyKnowledgeInventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if inventoryAfter != inventoryBefore {
		t.Fatalf("legacy data changed: before=%+v after=%+v", inventoryBefore, inventoryAfter)
	}
}

func TestMigrateLegacyKnowledgeRejectsFreshInstall(t *testing.T) {
	ctx := context.Background()
	_, databasePath := openTestStore(t)
	// A fresh store seeds only navigation roots/categories.
	dataDir := filepath.Dir(databasePath)
	config, err := DefaultConfig(dataDir).Normalized()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateLegacyKnowledge(ctx, config, LegacyMigrationOptions{
		DataDir: dataDir, DatabasePath: databasePath, Commit: false,
	}); !errors.Is(err, ErrNoLegacyKnowledge) {
		t.Fatalf("fresh install migration = %v, want ErrNoLegacyKnowledge", err)
	}
	if VaultInitialized(config.VaultPath) {
		t.Fatal("rejected migration must not create a vault")
	}
}
