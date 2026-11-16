package knowledgevault

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPrepareKnowledgeDeletionBacksUpExportsAndPlans(t *testing.T) {
	database, databasePath := openTestStore(t)
	ctx := context.Background()
	// store.Open seeds navigation roots and categories; clear them so the
	// fixture counts describe only the rows this test inserts.
	for _, table := range []string{"knowledge_entries", "knowledge_proposals", "knowledge_libraries", "project_knowledge_bindings", "skills", "projects"} {
		execTestSQL(t, databasePath, `DELETE FROM `+table)
	}
	createTestProject(t, database, "project-bound", "Bound Project", "folder")
	createTestProject(t, database, "project-archived", "Archived Project", "archive")
	createTestProject(t, database, "project-unbound-a", "未绑定甲", "folder")
	createTestProject(t, database, "project-unbound-b", "未绑定乙", "folder")
	createTestProject(t, database, "project-infra", "渠道宿主", "channel")

	now := "2026-11-15T12:00:00Z"
	insertKnowledge := func(id, scope, projectID, title, body string) {
		execTestSQL(t, databasePath, `INSERT INTO knowledge_entries(id,scope,project_id,slug,type,title,body,status,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?)`, id, scope, projectID, id, "practice", title, body, "verified", now, now)
	}
	insertKnowledge("knowledge_global", "global", "", "全局知识", "全局正文")
	insertKnowledge("knowledge_bound", "project", "project-bound", "已绑定项目知识", "已绑定正文")
	insertKnowledge("knowledge_unbound_a1", "project", "project-unbound-a", "未绑定甲一", "甲正文一")
	insertKnowledge("knowledge_unbound_a2", "project", "project-unbound-a", "未绑定甲二", "甲正文二")
	insertKnowledge("knowledge_unbound_b1", "project", "project-unbound-b", "未绑定乙一", "乙正文")
	insertKnowledge("knowledge_infra", "project", "project-infra", "渠道宿主知识", "基础设施正文")
	insertKnowledge("knowledge_ghost", "project", "ghost-project", "孤儿知识", "孤儿正文")

	execTestSQL(t, databasePath, `INSERT INTO knowledge_libraries(id,container_project_id,name,description,source_identity,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?)`, "library_unbound_a", "project-unbound-a", "未绑定甲知识库", "", "", now, now)
	execTestSQL(t, databasePath, `INSERT INTO knowledge_proposals(id,entry_id,base_revision,proposed_json,status,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?)`, "proposal_pending", "knowledge_bound", 1, `{"title":"提案"}`, "pending", now, now)
	execTestSQL(t, databasePath, `INSERT INTO knowledge_proposals(id,entry_id,base_revision,proposed_json,status,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?)`, "proposal_done", "knowledge_global", 1, `{"title":"已完成"}`, "approved", now, now)
	execTestSQL(t, databasePath, `INSERT INTO skills(id,package_slug,scope,project_id,name,description,instructions,created_at,updated_at,auto_attach)
		VALUES(?,?,?,?,?,?,?,?,?,?)`, "skill_global", "skill-global", "global", "", "全局技能", "", "指令", now, now, 1)
	execTestSQL(t, databasePath, `INSERT INTO skills(id,package_slug,scope,project_id,name,description,instructions,created_at,updated_at,auto_attach)
		VALUES(?,?,?,?,?,?,?,?,?,?)`, "skill_project", "skill-project", "project", "project-bound", "项目技能", "", "指令", now, now, 0)

	vaultPath := filepath.Join(t.TempDir(), "knowledge-vault")
	if err := os.MkdirAll(vaultPath, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{
		"version": 1, "vault_id": "vault-test",
		"projects":       map[string]string{"project-bound": "bound-project"},
		"archives":       map[string]string{"project-archived": "archived-project"},
		"skills":         map[string]any{},
		"reserved":       []string{},
		"reserved_files": []string{},
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vaultPath, ".aha-vault.json"), encoded, 0o644); err != nil {
		t.Fatal(err)
	}

	sourceHashBefore, err := fileSHA256(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	backupDir := filepath.Join(t.TempDir(), "backups")
	fixed := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	report, err := PrepareKnowledgeDeletion(ctx, DeletePrepOptions{
		DataDir: filepath.Dir(databasePath), DatabasePath: databasePath,
		VaultPath: vaultPath, BackupDir: backupDir, Now: func() time.Time { return fixed },
	})
	if err != nil {
		t.Fatalf("prepare deletion: %v", err)
	}
	if report.DeleteExecuted {
		t.Fatal("delete-prep must never execute DELETE")
	}
	if report.Mode != DeletePrepMode {
		t.Fatalf("mode = %q", report.Mode)
	}
	if report.BackupIntegrity != "ok" {
		t.Fatalf("backup integrity = %q", report.BackupIntegrity)
	}
	if report.BackupSHA256 == "" || report.BackupBytes == 0 {
		t.Fatalf("backup metadata missing: %+v", report)
	}
	if _, err := os.Stat(report.BackupDatabase); err != nil {
		t.Fatalf("backup database missing: %v", err)
	}
	if report.BackupPath != filepath.Join(backupDir, "knowledge-delete-20261115-120000") {
		t.Fatalf("backup path = %s", report.BackupPath)
	}

	rowsByTable := map[string]int{}
	for _, table := range report.Tables {
		rowsByTable[table.Table] = table.Rows
		if table.InScope && table.ExportJSONL == "" {
			t.Fatalf("in-scope table %s has no export", table.Table)
		}
	}
	want := map[string]int{
		"knowledge_entries": 12, "knowledge_proposals": 2, "knowledge_libraries": 1,
		"project_knowledge_bindings": 0, "skills": 2, "projects": 5,
	}
	for table, count := range want {
		if rowsByTable[table] != count {
			t.Fatalf("%s rows = %d, want %d", table, rowsByTable[table], count)
		}
	}
	if report.ScopeRows != 15 {
		t.Fatalf("scope rows = %d, want 15", report.ScopeRows)
	}
	if len(report.UnboundProjects) != 2 {
		t.Fatalf("unbound projects = %+v", report.UnboundProjects)
	}
	for _, project := range report.UnboundProjects {
		if project.ProjectType == "channel" || project.ProjectType == "system" {
			t.Fatalf("infrastructure project counted as unbound: %+v", project)
		}
	}
	if report.UnboundEntries != 5 {
		t.Fatalf("unbound entries = %d", report.UnboundEntries)
	}
	infraWarning := false
	for _, warning := range report.Warnings {
		if strings.Contains(warning, "project-infra") && strings.Contains(warning, "未计入未绑定项目") {
			infraWarning = true
		}
	}
	if !infraWarning {
		t.Fatalf("infrastructure legacy rows were not reported: %+v", report.Warnings)
	}
	if len(report.OrphanProjectIDs) != 1 || report.OrphanProjectIDs[0] != "ghost-project" || report.OrphanEntries != 1 {
		t.Fatalf("orphans = %+v entries=%d", report.OrphanProjectIDs, report.OrphanEntries)
	}
	if report.UnboundLibraries != 1 {
		t.Fatalf("unbound libraries = %d", report.UnboundLibraries)
	}
	if report.Skills.Rows != 2 || report.Skills.Global != 1 || report.Skills.Project != 1 || report.Skills.AutoAttach != 1 {
		t.Fatalf("skills plan = %+v", report.Skills)
	}
	if report.Skills.InDeleteScope || report.Skills.Decision != "pending-owner-confirmation" {
		t.Fatalf("skills must stay out of scope: %+v", report.Skills)
	}

	for _, table := range []string{"knowledge_entries", "knowledge_proposals", "knowledge_libraries", "skills"} {
		path := filepath.Join(report.ExportDir, table+".jsonl")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read export %s: %v", path, err)
		}
		lines := 0
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if strings.TrimSpace(line) != "" {
				lines++
			}
		}
		if lines != want[table] {
			t.Fatalf("%s export lines = %d, want %d", table, lines, want[table])
		}
	}
	planMarkdown, err := os.ReadFile(report.PlanMarkdown)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"没有执行任何 DELETE", "未绑定甲", "未绑定乙", "skills", "delete_executed"} {
		if !strings.Contains(string(planMarkdown), expected) {
			t.Fatalf("plan markdown missing %q", expected)
		}
	}
	var planJSON DeletePrepReport
	planData, err := os.ReadFile(report.PlanJSON)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(planData, &planJSON); err != nil {
		t.Fatal(err)
	}
	if planJSON.BackupSHA256 != report.BackupSHA256 || planJSON.ScopeRows != 15 {
		t.Fatalf("plan json mismatch: %+v", planJSON)
	}

	sourceHashAfter, err := fileSHA256(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if sourceHashBefore != sourceHashAfter {
		t.Fatal("source database changed during delete-prep")
	}
	source, err := openReadOnlySQLite(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	sourceCounts, err := deletePrepTableCounts(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	for table, count := range want {
		if sourceCounts[table] != count {
			t.Fatalf("source %s rows = %d, want unchanged %d", table, sourceCounts[table], count)
		}
	}

	second, err := PrepareKnowledgeDeletion(ctx, DeletePrepOptions{
		DataDir: filepath.Dir(databasePath), DatabasePath: databasePath,
		VaultPath: vaultPath, BackupDir: backupDir, Now: func() time.Time { return fixed },
	})
	if err != nil {
		t.Fatalf("second prepare: %v", err)
	}
	if second.BackupPath == report.BackupPath {
		t.Fatal("second prepare overwrote the first backup directory")
	}
	if second.BackupPath != report.BackupPath+"-2" {
		t.Fatalf("second backup path = %s", second.BackupPath)
	}
}

func TestPrepareKnowledgeDeletionMissingDatabase(t *testing.T) {
	_, err := PrepareKnowledgeDeletion(context.Background(), DeletePrepOptions{
		DataDir: t.TempDir(), DatabasePath: filepath.Join(t.TempDir(), "missing.db"),
	})
	if err == nil {
		t.Fatal("expected missing database error")
	}
}

// prepareDeletionWithin guards against the backup-directory path regressing to
// an unbounded retry loop: invalid locations must fail instead of hanging.
func prepareDeletionWithin(t *testing.T, options DeletePrepOptions) (DeletePrepReport, error) {
	t.Helper()
	type outcome struct {
		report DeletePrepReport
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		report, err := PrepareKnowledgeDeletion(context.Background(), options)
		done <- outcome{report: report, err: err}
	}()
	select {
	case result := <-done:
		return result.report, result.err
	case <-time.After(5 * time.Second):
		t.Fatal("delete-prep did not return within 5s")
		return DeletePrepReport{}, nil
	}
}

func TestPrepareKnowledgeDeletionRejectsBackupDirThatIsAFile(t *testing.T) {
	_, databasePath := openTestStore(t)
	backupFile := filepath.Join(t.TempDir(), "backups")
	if err := os.WriteFile(backupFile, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := prepareDeletionWithin(t, DeletePrepOptions{
		DataDir: filepath.Dir(databasePath), DatabasePath: databasePath,
		VaultPath: filepath.Join(t.TempDir(), "missing-vault"), BackupDir: backupFile,
	})
	if err == nil {
		t.Fatal("expected a fast error when the backup directory is a regular file")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("error = %v, want a not-a-directory error", err)
	}
}

func TestPrepareKnowledgeDeletionRejectsUnreadableBackupDir(t *testing.T) {
	_, databasePath := openTestStore(t)
	backupDir := filepath.Join(t.TempDir(), "locked-backups")
	if err := os.Mkdir(backupDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(backupDir, 0o000); err != nil {
		t.Skipf("cannot chmod the backup directory: %v", err)
	}
	defer os.Chmod(backupDir, 0o700)
	probe, probeErr := os.Create(filepath.Join(backupDir, "probe"))
	if probeErr == nil {
		probe.Close()
		// Root (or a platform where 0o000 does not restrict traversal, such as
		// Windows) can still reach the directory; the scenario is not testable.
		t.Skip("process can still write inside a 0o000 directory")
	}
	_, err := prepareDeletionWithin(t, DeletePrepOptions{
		DataDir: filepath.Dir(databasePath), DatabasePath: databasePath,
		VaultPath: filepath.Join(t.TempDir(), "missing-vault"), BackupDir: backupDir,
	})
	if err == nil {
		t.Fatal("expected a fast error when the backup directory is unreadable")
	}
}

func TestPrepareKnowledgeDeletionWarnsWithoutManifest(t *testing.T) {
	_, databasePath := openTestStore(t)
	now := "2026-11-15T12:00:00Z"
	execTestSQL(t, databasePath, `INSERT INTO knowledge_entries(id,scope,project_id,slug,type,title,body,status,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)`, "knowledge_one", "global", "", "one", "practice", "标题", "正文", "verified", now, now)
	report, err := PrepareKnowledgeDeletion(context.Background(), DeletePrepOptions{
		DataDir: filepath.Dir(databasePath), DatabasePath: databasePath,
		VaultPath: filepath.Join(t.TempDir(), "missing-vault"), BackupDir: filepath.Join(t.TempDir(), "backups"),
	})
	if err != nil {
		t.Fatalf("prepare deletion: %v", err)
	}
	if report.VaultManifest {
		t.Fatal("manifest should not be reported as read")
	}
	found := false
	for _, blocker := range report.Blockers {
		if strings.Contains(blocker, "vault manifest") {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing manifest blocker: %+v", report.Blockers)
	}
}
