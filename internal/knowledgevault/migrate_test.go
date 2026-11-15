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

func TestMigrateLegacyKnowledgeRefusesNonEmptyVaultWithoutOverwriting(t *testing.T) {
	ctx := context.Background()
	t.Run("indexed vault", func(t *testing.T) {
		database, databasePath := openTestStore(t)
		seedStandardVaultDatabase(t, database)
		dataDir := filepath.Dir(databasePath)
		config, err := DefaultConfig(dataDir).Normalized()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Bootstrap(config, InitOptions{Commit: false}); err != nil {
			t.Fatal(err)
		}
		pagePath := filepath.Join(config.VaultPath, "global", "general", "owner-edit.md")
		ownerContent := []byte("---\ntitle: owner edit\n---\n\n必须保留的 vault 内容。\n")
		if err := os.WriteFile(pagePath, ownerContent, 0o644); err != nil {
			t.Fatal(err)
		}
		index, err := OpenIndex(ctx, config.IndexPath)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
			_ = index.Close()
			t.Fatal(err)
		}
		_ = index.Close()
		inventoryBefore, err := database.LegacyKnowledgeInventory(ctx)
		if err != nil {
			t.Fatal(err)
		}

		report, err := MigrateLegacyKnowledge(ctx, config, LegacyMigrationOptions{
			DataDir: dataDir, DatabasePath: databasePath, Commit: false,
		})
		if !errors.Is(err, ErrTargetNotEmpty) {
			t.Fatalf("migration over a populated vault = %v, want ErrTargetNotEmpty", err)
		}
		if report.BackupPath != "" {
			t.Fatalf("refused migration must not create a backup: %+v", report)
		}
		after, err := os.ReadFile(pagePath)
		if err != nil {
			t.Fatalf("owner page disappeared: %v", err)
		}
		if string(after) != string(ownerContent) {
			t.Fatalf("refused migration changed the owner page:\n%s", after)
		}
		inventoryAfter, err := database.LegacyKnowledgeInventory(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if inventoryAfter != inventoryBefore {
			t.Fatalf("refused migration changed legacy data: before=%+v after=%+v", inventoryBefore, inventoryAfter)
		}
		if _, err := os.Stat(filepath.Join(dataDir, defaultMigrationBackups)); !os.IsNotExist(err) {
			t.Fatalf("refused migration created a backup directory: %v", err)
		}
	})
	t.Run("page without an index", func(t *testing.T) {
		database, databasePath := openTestStore(t)
		seedStandardVaultDatabase(t, database)
		dataDir := filepath.Dir(databasePath)
		config, err := DefaultConfig(dataDir).Normalized()
		if err != nil {
			t.Fatal(err)
		}
		pagePath := filepath.Join(config.VaultPath, "global", "general", "hand-written.md")
		if err := os.MkdirAll(filepath.Dir(pagePath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(pagePath, []byte("手写页面，必须保留。\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := MigrateLegacyKnowledge(ctx, config, LegacyMigrationOptions{
			DataDir: dataDir, DatabasePath: databasePath, Commit: false,
		}); !errors.Is(err, ErrTargetNotEmpty) {
			t.Fatalf("migration over a page without an index = %v, want ErrTargetNotEmpty", err)
		}
		after, err := os.ReadFile(pagePath)
		if err != nil || string(after) != "手写页面，必须保留。\n" {
			t.Fatalf("refused migration changed the page: %q err=%v", after, err)
		}
	})
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
