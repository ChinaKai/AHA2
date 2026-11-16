package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func cutoverTestStore(t *testing.T) (*Store, domain.Project) {
	t.Helper()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	now := time.Now().UTC()
	project := domain.Project{ID: "project-cutover", Name: "Cutover", ProjectType: "knowledge", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	return database, project
}

func TestCutoverLegacyKnowledgeBacksUpVerifiesAndDrops(t *testing.T) {
	ctx := context.Background()
	database, _ := cutoverTestStore(t)
	rows, err := database.LegacyKnowledgeRows(ctx)
	if err != nil || rows == 0 {
		t.Fatalf("legacy rows = %d err=%v", rows, err)
	}
	report, err := database.CutoverLegacyKnowledge(ctx, LegacyCutoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Applied || !report.BackupTableOK || report.BackedUpRows != rows {
		t.Fatalf("report = %#v", report)
	}
	if report.BackupPath == "" {
		t.Fatal("cutover did not record a backup path")
	}
	ok, err := verifyLegacyBackup(ctx, report.BackupPath, rows)
	if err != nil || !ok {
		t.Fatalf("backup verification = %v err=%v", ok, err)
	}
	for _, table := range legacyCutoverTables {
		exists, err := database.tableExists(ctx, table)
		if err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Fatalf("legacy table %s still exists after cutover", table)
		}
	}
	applied, err := database.LegacyCutoverApplied(ctx)
	if err != nil || !applied {
		t.Fatalf("v143 applied=%t err=%v", applied, err)
	}
	if rows, err := database.LegacyKnowledgeRows(ctx); err != nil || rows != 0 {
		t.Fatalf("legacy rows after cutover = %d err=%v", rows, err)
	}
	inventory, err := database.LegacyKnowledgeInventory(ctx)
	if err != nil || inventory.Entries != 0 {
		t.Fatalf("legacy inventory after cutover = %#v err=%v", inventory, err)
	}
}

func TestCutoverLegacyKnowledgeAbortsWhenExportVerificationFails(t *testing.T) {
	ctx := context.Background()
	database, _ := cutoverTestStore(t)
	wantErr := errors.New("export missing")
	_, err := database.CutoverLegacyKnowledge(ctx, LegacyCutoverOptions{
		VerifyExport: func(context.Context) error { return wantErr },
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("cutover error = %v", err)
	}
	for _, table := range []string{"knowledge_entries", "knowledge_libraries", "channel_knowledge_policies"} {
		exists, tableErr := database.tableExists(ctx, table)
		if tableErr != nil || !exists {
			t.Fatalf("table %s was dropped despite failed verification: exists=%t err=%v", table, exists, tableErr)
		}
	}
	applied, err := database.LegacyCutoverApplied(ctx)
	if err != nil || applied {
		t.Fatalf("v143 applied after aborted cutover: applied=%t err=%v", applied, err)
	}
}
