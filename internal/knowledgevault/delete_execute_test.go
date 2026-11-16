package knowledgevault

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type deleteTestFixture struct {
	dataDir      string
	databasePath string
	vaultPath    string
	prepDir      string
	planPath     string
	planSHA      string
	skillsRoot   string
	skills       []DeleteSkillRecord
	scopeRows    int
	skillsRows   int
}

type deleteTestTransport func(*http.Request) (*http.Response, error)

func (transport deleteTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func deleteTestHealthClient(status int) *http.Client {
	return &http.Client{Transport: deleteTestTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     make(http.Header),
		}, nil
	})}
}

func newDeleteTestFixture(t *testing.T) *deleteTestFixture {
	t.Helper()
	database, databasePath := openTestStore(t)
	ctx := context.Background()
	dataDir := filepath.Dir(databasePath)
	for _, table := range []string{
		"channel_knowledge_grants", "channel_knowledge_policies",
		"project_knowledge_bindings", "knowledge_libraries", "knowledge_entries",
		"knowledge_proposals", "skills", "tasks", "projects",
	} {
		execTestSQL(t, databasePath, `DELETE FROM `+table)
	}
	createTestProject(t, database, "project-bound", "Bound Project", "folder")
	createTestProject(t, database, "project-unbound", "Unbound Project", "folder")
	// CreateProject also seeds a project knowledge root; remove those so this
	// fixture only contains the rows each test inserts.
	execTestSQL(t, databasePath, `DELETE FROM knowledge_entries`)

	now := "2026-11-16T00:00:00Z"
	for index := 1; index <= 3; index++ {
		execTestSQL(t, databasePath, `INSERT INTO knowledge_entries(id,scope,project_id,slug,type,title,body,status,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?)`,
			fmt.Sprintf("knowledge_test_%d", index), "project", "project-bound",
			fmt.Sprintf("test-%d", index), "practice", fmt.Sprintf("知识 %d", index), "正文", "verified", now, now)
	}
	for index := 1; index <= 2; index++ {
		execTestSQL(t, databasePath, `INSERT INTO knowledge_proposals(id,entry_id,base_revision,proposed_json,status,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?)`,
			fmt.Sprintf("proposal_test_%d", index), fmt.Sprintf("knowledge_test_%d", index), 1, `{"title":"提案"}`, "pending", now, now)
	}
	// The container project is unique per library, so use the two test projects.
	for _, projectID := range []string{"project-bound", "project-unbound"} {
		execTestSQL(t, databasePath, `INSERT INTO knowledge_libraries(id,container_project_id,name,description,source_identity,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?)`, "library_"+projectID, projectID, "知识库 "+projectID, "", "", now, now)
		execTestSQL(t, databasePath, `INSERT INTO project_knowledge_bindings(library_id,project_id,created_at,updated_at)
			VALUES(?,?,?,?)`, "library_"+projectID, projectID, now, now)
	}
	for index := 1; index <= 4; index++ {
		execTestSQL(t, databasePath, `INSERT INTO channel_knowledge_policies(id,instance_id,endpoint_id,fixed_index_entry_id,default_visibility,revision,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?)`,
			fmt.Sprintf("policy_test_%d", index), fmt.Sprintf("channel_instance_test_%d", index),
			fmt.Sprintf("channel_endpoint_test_%d", index), "knowledge_test_1", "conversation_only", 1, now, now)
	}

	vaultPath := filepath.Join(dataDir, "knowledge-vault")
	if err := os.MkdirAll(vaultPath, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{
		"version": 1, "vault_id": "vault-test",
		"projects":       map[string]string{"project-bound": "bound-project"},
		"archives":       map[string]string{},
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

	skillsRoot := filepath.Join(dataDir, "knowledge", "skills")
	fixture := &deleteTestFixture{
		dataDir: dataDir, databasePath: databasePath, vaultPath: vaultPath, skillsRoot: skillsRoot,
	}
	for index := 1; index <= 15; index++ {
		scope, projectID, prefix := "global", "", "global"
		if index > 9 {
			scope, projectID, prefix = "project", "project-bound", "project"
		}
		slug := fmt.Sprintf("skill-%s-%02d", prefix, index)
		item := DeleteSkillRecord{
			ID: fmt.Sprintf("skill_test_%02d", index), PackageSlug: slug,
			Scope: scope, ProjectID: projectID, Name: slug,
		}
		execTestSQL(t, databasePath, `INSERT INTO skills(id,package_slug,scope,project_id,name,description,instructions,created_at,updated_at,auto_attach)
			VALUES(?,?,?,?,?,?,?,?,?,?)`,
			item.ID, item.PackageSlug, item.Scope, item.ProjectID, item.Name, "", "指令", now, now, 0)
		legacyDir := filepath.Join(skillsRoot, slug)
		if err := os.MkdirAll(legacyDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(legacyDir, "SKILL.md"), []byte("# "+slug), 0o644); err != nil {
			t.Fatal(err)
		}
		vaultDir := filepath.Join(vaultPath, "global", "skills", slug)
		if scope == "project" {
			vaultDir = filepath.Join(vaultPath, "projects", "bound-project", "skills", slug)
		}
		if err := os.MkdirAll(vaultDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(vaultDir, "SKILL.md"), []byte("---\nname: "+slug+"\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		fixture.skills = append(fixture.skills, item)
	}
	unrelated := filepath.Join(skillsRoot, "unrelated-package")
	if err := os.MkdirAll(unrelated, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unrelated, "SKILL.md"), []byte("# unrelated"), 0o644); err != nil {
		t.Fatal(err)
	}

	backupDir := filepath.Join(t.TempDir(), "backups")
	fixed := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	prep, err := PrepareKnowledgeDeletion(ctx, DeletePrepOptions{
		DataDir: dataDir, DatabasePath: databasePath, VaultPath: vaultPath,
		BackupDir: backupDir, Now: func() time.Time { return fixed },
	})
	if err != nil {
		t.Fatalf("prepare deletion fixture: %v", err)
	}
	fixture.prepDir = prep.BackupPath
	fixture.planPath = prep.PlanJSON
	fixture.scopeRows = prep.ScopeRows
	fixture.skillsRows = prep.Skills.Rows
	planData, err := os.ReadFile(fixture.planPath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(planData)
	fixture.planSHA = hex.EncodeToString(sum[:])
	return fixture
}

func (fixture *deleteTestFixture) options(execute bool) DeleteExecuteOptions {
	return DeleteExecuteOptions{
		DataDir: fixture.dataDir, DatabasePath: fixture.databasePath,
		VaultPath: fixture.vaultPath, PrepDir: fixture.prepDir,
		Execute: execute, Confirm: fixture.planSHA,
		VaultEnabled: true, IndexReady: true,
		HealthURL:  "http://127.0.0.1:1/healthz",
		HTTPClient: deleteTestHealthClient(http.StatusServiceUnavailable),
		Now:        func() time.Time { return time.Date(2026, 11, 16, 1, 0, 0, 0, time.UTC) },
	}
}

func deleteTestTableCount(t *testing.T, databasePath, table string) int {
	t.Helper()
	database, err := openReadOnlySQLite(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	count, err := deleteRowCount(context.Background(), database, table)
	if err != nil {
		t.Fatal(err)
	}
	return count
}

func (fixture *deleteTestFixture) assertKnowledgeCounts(t *testing.T, proposals, entries, bindings, libraries, skills, policies int) {
	t.Helper()
	want := map[string]int{
		"knowledge_proposals": proposals, "knowledge_entries": entries,
		"project_knowledge_bindings": bindings, "knowledge_libraries": libraries,
		"skills": skills, "channel_knowledge_policies": policies,
	}
	for table, count := range want {
		if got := deleteTestTableCount(t, fixture.databasePath, table); got != count {
			t.Fatalf("%s rows = %d, want %d", table, got, count)
		}
	}
}

func deleteTestRewritePlan(t *testing.T, fixture *deleteTestFixture, mutate func(*DeletePrepReport)) {
	t.Helper()
	data, err := os.ReadFile(fixture.planPath)
	if err != nil {
		t.Fatal(err)
	}
	var plan DeletePrepReport
	if err := json.Unmarshal(data, &plan); err != nil {
		t.Fatal(err)
	}
	mutate(&plan)
	encoded, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(fixture.planPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(encoded)
	fixture.planSHA = hex.EncodeToString(sum[:])
}

func deleteTestRewriteBackup(t *testing.T, fixture *deleteTestFixture, mutate func(*deleteBackupMetadata)) {
	t.Helper()
	path := filepath.Join(fixture.prepDir, deleteBackupFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var backup deleteBackupMetadata
	if err := json.Unmarshal(data, &backup); err != nil {
		t.Fatal(err)
	}
	mutate(&backup)
	encoded, err := json.MarshalIndent(backup, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteKnowledgeDeletionDryRunDoesNotWrite(t *testing.T) {
	fixture := newDeleteTestFixture(t)
	report, err := ExecuteKnowledgeDeletion(context.Background(), fixture.options(false))
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if report.Mode != DeleteDryRunMode || report.DeleteExecuted {
		t.Fatalf("dry-run report = %+v", report)
	}
	if len(report.Policies) != requiredDeletePolicyCount {
		t.Fatalf("policies = %d, want %d", len(report.Policies), requiredDeletePolicyCount)
	}
	if len(report.Skills) != fixture.skillsRows || report.SkillsDeleted != 0 {
		t.Fatalf("skills report = %d deleted=%d", len(report.Skills), report.SkillsDeleted)
	}
	if report.Before.Tables["knowledge_entries"] != 3 {
		t.Fatalf("before entries = %d", report.Before.Tables["knowledge_entries"])
	}
	if report.PlanScopeRows != fixture.scopeRows || report.PlanSkillsRows != fixture.skillsRows {
		t.Fatalf("plan rows = %+v", report)
	}
	if report.TotalRows != fixture.scopeRows+fixture.skillsRows {
		t.Fatalf("total rows = %d, want %d", report.TotalRows, fixture.scopeRows+fixture.skillsRows)
	}
	if report.DeleteRows != fixture.scopeRows {
		t.Fatalf("delete rows without -delete-skills = %d, want %d", report.DeleteRows, fixture.scopeRows)
	}
	if report.ReadyToExecute != true {
		t.Fatalf("dry-run should be ready: %+v", report.Warnings)
	}
	if _, err := os.Stat(filepath.Join(fixture.prepDir, deleteResultFileName)); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote %s: %v", deleteResultFileName, err)
	}
	fixture.assertKnowledgeCounts(t, 2, 3, 2, 2, 15, 4)
}

func TestExecuteKnowledgeDeletionRequiresConfirmAndVaultGate(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*DeleteExecuteOptions)
		want   string
	}{
		{name: "missing confirm", mutate: func(options *DeleteExecuteOptions) { options.Confirm = "" }, want: "requires -confirm"},
		{name: "wrong confirm", mutate: func(options *DeleteExecuteOptions) { options.Confirm = "deadbeef" }, want: "confirm hash mismatch"},
		{name: "vault disabled", mutate: func(options *DeleteExecuteOptions) { options.VaultEnabled = false }, want: "not enabled"},
		{name: "index not ready", mutate: func(options *DeleteExecuteOptions) { options.IndexReady = false }, want: "index is not ready"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newDeleteTestFixture(t)
			options := fixture.options(true)
			testCase.mutate(&options)
			report, err := ExecuteKnowledgeDeletion(context.Background(), options)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want %q", err, testCase.want)
			}
			if report.DeleteExecuted {
				t.Fatal("refused execute reported delete_executed=true")
			}
			fixture.assertKnowledgeCounts(t, 2, 3, 2, 2, 15, 4)
			if _, err := os.Stat(filepath.Join(fixture.prepDir, deleteResultFileName)); !os.IsNotExist(err) {
				t.Fatalf("refused execute wrote %s", deleteResultFileName)
			}
		})
	}
}

func TestExecuteKnowledgeDeletionRejectsRowCountAndPolicyDrift(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*testing.T, *deleteTestFixture)
		want   string
	}{
		{
			name: "knowledge row drift",
			mutate: func(t *testing.T, fixture *deleteTestFixture) {
				execTestSQL(t, fixture.databasePath, `INSERT INTO knowledge_entries(id,scope,project_id,slug,type,title,body,status,created_at,updated_at)
					VALUES(?,?,?,?,?,?,?,?,?,?)`, "knowledge_extra", "global", "", "extra", "practice", "额外", "正文", "verified", "2026-11-16T00:00:00Z", "2026-11-16T00:00:00Z")
			},
			want: "knowledge_entries has 4 rows, delete-plan expects 3",
		},
		{
			name: "skills row drift",
			mutate: func(t *testing.T, fixture *deleteTestFixture) {
				execTestSQL(t, fixture.databasePath, `DELETE FROM skills WHERE id='skill_test_15'`)
			},
			want: "skills has 14 rows, delete-plan expects 15",
		},
		{
			name: "policy count",
			mutate: func(t *testing.T, fixture *deleteTestFixture) {
				execTestSQL(t, fixture.databasePath, `DELETE FROM channel_knowledge_policies WHERE id='policy_test_4'`)
			},
			want: "want exactly 4",
		},
		{
			name: "active grants",
			mutate: func(t *testing.T, fixture *deleteTestFixture) {
				execTestSQL(t, fixture.databasePath, `INSERT INTO channel_knowledge_grants(id,policy_id,knowledge_entry_id,grant_scope,granted_by_owner_id,created_at)
					VALUES(?,?,?,?,?,?)`, "grant_test", "policy_test_1", "knowledge_test_1", "node", "owner_test", "2026-11-16T00:00:00Z")
			},
			want: "channel_knowledge_grants has 1 rows, want 0",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newDeleteTestFixture(t)
			testCase.mutate(t, fixture)
			before := map[string]int{}
			for _, table := range []string{"knowledge_entries", "knowledge_proposals", "skills", "channel_knowledge_policies", "channel_knowledge_grants"} {
				before[table] = deleteTestTableCount(t, fixture.databasePath, table)
			}
			_, err := ExecuteKnowledgeDeletion(context.Background(), fixture.options(true))
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want %q", err, testCase.want)
			}
			for table, count := range before {
				if got := deleteTestTableCount(t, fixture.databasePath, table); got != count {
					t.Fatalf("%s changed after refusal: %d → %d", table, count, got)
				}
			}
		})
	}
}

func TestExecuteKnowledgeDeletionRejectsLiveHealthEndpoint(t *testing.T) {
	fixture := newDeleteTestFixture(t)
	options := fixture.options(true)
	options.HTTPClient = deleteTestHealthClient(http.StatusOK)
	_, err := ExecuteKnowledgeDeletion(context.Background(), options)
	if err == nil || !strings.Contains(err.Error(), "HTTP 200") {
		t.Fatalf("error = %v, want health refusal", err)
	}
	fixture.assertKnowledgeCounts(t, 2, 3, 2, 2, 15, 4)
}

func TestExecuteKnowledgeDeletionRejectsTamperedPrep(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*testing.T, *deleteTestFixture)
		want   string
	}{
		{
			name: "plan backup sha",
			mutate: func(t *testing.T, fixture *deleteTestFixture) {
				deleteTestRewritePlan(t, fixture, func(plan *DeletePrepReport) { plan.BackupSHA256 = "deadbeef" })
			},
			want: "backup_sha256 mismatch",
		},
		{
			name: "plan delete executed",
			mutate: func(t *testing.T, fixture *deleteTestFixture) {
				deleteTestRewritePlan(t, fixture, func(plan *DeletePrepReport) { plan.DeleteExecuted = true })
			},
			want: "delete_executed=true",
		},
		{
			name: "plan scope sum",
			mutate: func(t *testing.T, fixture *deleteTestFixture) {
				deleteTestRewritePlan(t, fixture, func(plan *DeletePrepReport) { plan.ScopeRows++ })
			},
			want: "scope_rows",
		},
		{
			name: "backup integrity",
			mutate: func(t *testing.T, fixture *deleteTestFixture) {
				deleteTestRewriteBackup(t, fixture, func(backup *deleteBackupMetadata) { backup.IntegrityCheck = "corrupt" })
			},
			want: "integrity_check",
		},
		{
			name: "backup size",
			mutate: func(t *testing.T, fixture *deleteTestFixture) {
				path := filepath.Join(fixture.prepDir, "aha2.db")
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Truncate(path, info.Size()-1); err != nil {
					t.Fatal(err)
				}
			},
			want: "size is",
		},
		{
			name: "backup metadata missing",
			mutate: func(t *testing.T, fixture *deleteTestFixture) {
				if err := os.Remove(filepath.Join(fixture.prepDir, deleteBackupFileName)); err != nil {
					t.Fatal(err)
				}
			},
			want: "backup.json",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newDeleteTestFixture(t)
			testCase.mutate(t, fixture)
			_, err := ExecuteKnowledgeDeletion(context.Background(), fixture.options(true))
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want %q", err, testCase.want)
			}
			fixture.assertKnowledgeCounts(t, 2, 3, 2, 2, 15, 4)
		})
	}
}

func TestExecuteKnowledgeDeletionDeletesKnowledgeAndSkills(t *testing.T) {
	fixture := newDeleteTestFixture(t)
	options := fixture.options(true)
	options.DeleteSkills = true
	options.DeleteSkillFiles = true
	report, err := ExecuteKnowledgeDeletion(context.Background(), options)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !report.DeleteExecuted || report.Mode != DeleteExecuteMode {
		t.Fatalf("report = %+v", report)
	}
	if report.After == nil {
		t.Fatal("missing after counts")
	}
	for _, table := range []string{"knowledge_proposals", "knowledge_entries", "project_knowledge_bindings", "knowledge_libraries"} {
		if report.After.Tables[table] != 0 {
			t.Fatalf("after %s = %d", table, report.After.Tables[table])
		}
	}
	if report.After.ChannelPolicies != 0 || report.After.ChannelGrants != 0 || report.After.Skills != 0 {
		t.Fatalf("after counts = %+v", report.After)
	}
	if len(report.DeletedPolicyIDs) != requiredDeletePolicyCount {
		t.Fatalf("deleted policy ids = %v", report.DeletedPolicyIDs)
	}
	if report.SkillsDeleted != 15 || report.SkillFilesDeleted != 15 {
		t.Fatalf("skills deleted = %d files = %d", report.SkillsDeleted, report.SkillFilesDeleted)
	}
	if report.TotalRows != fixture.scopeRows+fixture.skillsRows || report.DeleteRows != fixture.scopeRows+fixture.skillsRows {
		t.Fatalf("total/delete rows = %d/%d", report.TotalRows, report.DeleteRows)
	}
	if len(report.SkillFileSamples) != 15 {
		t.Fatalf("skill file samples = %d", len(report.SkillFileSamples))
	}
	if len(report.BlockingTaskRefs) != 0 {
		t.Fatalf("blocking refs = %+v", report.BlockingTaskRefs)
	}
	fixture.assertKnowledgeCounts(t, 0, 0, 0, 0, 0, 0)
	if got := deleteTestTableCount(t, fixture.databasePath, "projects"); got != 2 {
		t.Fatalf("projects = %d, want 2 (unrelated tables must stay)", got)
	}
	for _, skill := range fixture.skills {
		if _, err := os.Stat(filepath.Join(fixture.skillsRoot, skill.PackageSlug)); !os.IsNotExist(err) {
			t.Fatalf("legacy skill directory %s still exists: %v", skill.PackageSlug, err)
		}
	}
	if _, err := os.Stat(filepath.Join(fixture.skillsRoot, "unrelated-package", "SKILL.md")); err != nil {
		t.Fatalf("unrelated skill directory was removed: %v", err)
	}

	resultData, err := os.ReadFile(filepath.Join(fixture.prepDir, deleteResultFileName))
	if err != nil {
		t.Fatal(err)
	}
	var result DeleteExecuteReport
	if err := json.Unmarshal(resultData, &result); err != nil {
		t.Fatal(err)
	}
	if !result.DeleteExecuted || result.ConfirmSHA256 != fixture.planSHA {
		t.Fatalf("result = %+v", result)
	}
	if result.SkillsDeleted != 15 || result.SkillFilesDeleted != 15 || len(result.BlockingTaskRefs) != 0 {
		t.Fatalf("result skills = %+v", result)
	}
	if result.After == nil || result.After.Tables["knowledge_entries"] != 0 {
		t.Fatalf("result after = %+v", result.After)
	}
}

func TestExecuteKnowledgeDeletionKeepsSkillsWithoutDeleteSkills(t *testing.T) {
	fixture := newDeleteTestFixture(t)
	report, err := ExecuteKnowledgeDeletion(context.Background(), fixture.options(true))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !report.DeleteExecuted || report.SkillsDeleted != 0 || report.SkillFilesDeleted != 0 {
		t.Fatalf("report = %+v", report)
	}
	fixture.assertKnowledgeCounts(t, 0, 0, 0, 0, 15, 0)
	for _, skill := range fixture.skills {
		if _, err := os.Stat(filepath.Join(fixture.skillsRoot, skill.PackageSlug, "SKILL.md")); err != nil {
			t.Fatalf("skill files should stay: %v", err)
		}
	}
}

func TestExecuteKnowledgeDeletionRollsBackOnInjectedError(t *testing.T) {
	fixture := newDeleteTestFixture(t)
	execTestSQL(t, fixture.databasePath, `CREATE TRIGGER delete_test_fail_entries
		BEFORE DELETE ON knowledge_entries BEGIN SELECT RAISE(ABORT,'injected entry delete failure'); END`)
	options := fixture.options(true)
	options.DeleteSkills = true
	_, err := ExecuteKnowledgeDeletion(context.Background(), options)
	if err == nil || !strings.Contains(err.Error(), "injected entry delete failure") {
		t.Fatalf("error = %v, want injected failure", err)
	}
	fixture.assertKnowledgeCounts(t, 2, 3, 2, 2, 15, 4)
	if _, err := os.Stat(filepath.Join(fixture.prepDir, deleteResultFileName)); !os.IsNotExist(err) {
		t.Fatalf("rollback wrote %s", deleteResultFileName)
	}
}

func TestExecuteKnowledgeDeletionRejectsOpenTaskSkillRefs(t *testing.T) {
	fixture := newDeleteTestFixture(t)
	now := "2026-11-16T00:00:00Z"
	legacyID := fixture.skills[0].ID
	insertTask := func(id, status, skillIDs string) {
		execTestSQL(t, fixture.databasePath, `INSERT INTO tasks(id,project_id,workspace_id,title,original_request,current_goal,status,runtime_config_snapshot_id,skill_ids_json,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?)`, id, "project-bound", "workspace-test", "任务 "+id, "请求", "目标", status, "snapshot-test", skillIDs, now, now)
	}
	insertTask("task_open", "waiting_user", fmt.Sprintf(`["%s"]`, legacyID))
	insertTask("task_completed", "completed", fmt.Sprintf(`["%s"]`, legacyID))
	insertTask("task_vault", "active", `["vault-skill:global:embedded-change-review"]`)

	dryReport, err := ExecuteKnowledgeDeletion(context.Background(), func() DeleteExecuteOptions {
		options := fixture.options(false)
		options.DeleteSkills = true
		return options
	}())
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if len(dryReport.BlockingTaskRefs) != 1 || dryReport.BlockingTaskRefs[0].TaskID != "task_open" {
		t.Fatalf("blocking refs = %+v", dryReport.BlockingTaskRefs)
	}
	if dryReport.ReadyToExecute {
		t.Fatal("dry-run should not be ready while an open task references a legacy skill")
	}

	options := fixture.options(true)
	options.DeleteSkills = true
	_, err = ExecuteKnowledgeDeletion(context.Background(), options)
	if err == nil || !strings.Contains(err.Error(), "open task") {
		t.Fatalf("error = %v, want open-task refusal", err)
	}
	fixture.assertKnowledgeCounts(t, 2, 3, 2, 2, 15, 4)

	execTestSQL(t, fixture.databasePath, `UPDATE tasks SET status='completed' WHERE id='task_open'`)
	report, err := ExecuteKnowledgeDeletion(context.Background(), options)
	if err != nil {
		t.Fatalf("execute after clearing refs: %v", err)
	}
	if len(report.BlockingTaskRefs) != 0 {
		t.Fatalf("blocking refs after clearing = %+v", report.BlockingTaskRefs)
	}
	fixture.assertKnowledgeCounts(t, 0, 0, 0, 0, 0, 0)
}

func TestExecuteKnowledgeDeletionRejectsPreparingTaskSkillRefs(t *testing.T) {
	fixture := newDeleteTestFixture(t)
	now := "2026-11-16T00:00:00Z"
	legacyID := fixture.skills[0].ID
	vaultKey := `["vault-skill:global:embedded-change-review"]`
	insertTask := func(id, status, skillIDs string) {
		execTestSQL(t, fixture.databasePath, `INSERT INTO tasks(id,project_id,workspace_id,title,original_request,current_goal,status,runtime_config_snapshot_id,skill_ids_json,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?)`, id, "project-bound", "workspace-test", "任务 "+id, "请求", "目标", status, "snapshot-test", skillIDs, now, now)
	}
	insertTask("task_preparing_legacy", "preparing", fmt.Sprintf(`["%s"]`, legacyID))
	insertTask("task_preparing_vault", "preparing", vaultKey)

	dryOptions := fixture.options(false)
	dryOptions.DeleteSkills = true
	dryReport, err := ExecuteKnowledgeDeletion(context.Background(), dryOptions)
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if len(dryReport.BlockingTaskRefs) != 1 || dryReport.BlockingTaskRefs[0].TaskID != "task_preparing_legacy" {
		t.Fatalf("blocking refs = %+v, want the preparing task with a legacy skill", dryReport.BlockingTaskRefs)
	}
	if dryReport.ReadyToExecute {
		t.Fatal("dry-run should not be ready while a preparing task references a legacy skill")
	}

	options := fixture.options(true)
	options.DeleteSkills = true
	_, err = ExecuteKnowledgeDeletion(context.Background(), options)
	if err == nil || !strings.Contains(err.Error(), "open task") {
		t.Fatalf("error = %v, want open-task refusal", err)
	}
	fixture.assertKnowledgeCounts(t, 2, 3, 2, 2, 15, 4)

	// A preparing task that only selects a vault skill key must not block.
	execTestSQL(t, fixture.databasePath, `UPDATE tasks SET skill_ids_json=? WHERE id='task_preparing_legacy'`, vaultKey)
	report, err := ExecuteKnowledgeDeletion(context.Background(), options)
	if err != nil {
		t.Fatalf("execute with only vault skill keys: %v", err)
	}
	if !report.DeleteExecuted || len(report.BlockingTaskRefs) != 0 {
		t.Fatalf("report = %+v", report)
	}
	fixture.assertKnowledgeCounts(t, 0, 0, 0, 0, 0, 0)
}

func TestExecuteKnowledgeDeletionSkillFileValidation(t *testing.T) {
	t.Run("missing vault package", func(t *testing.T) {
		fixture := newDeleteTestFixture(t)
		slug := fixture.skills[0].PackageSlug
		if err := os.RemoveAll(filepath.Join(fixture.vaultPath, "global", "skills", slug)); err != nil {
			t.Fatal(err)
		}
		options := fixture.options(true)
		options.DeleteSkills = true
		options.DeleteSkillFiles = true
		_, err := ExecuteKnowledgeDeletion(context.Background(), options)
		if err == nil || !strings.Contains(err.Error(), "vault has no SKILL.md") {
			t.Fatalf("error = %v", err)
		}
		fixture.assertKnowledgeCounts(t, 2, 3, 2, 2, 15, 4)
	})

	t.Run("missing backup package", func(t *testing.T) {
		fixture := newDeleteTestFixture(t)
		slug := fixture.skills[1].PackageSlug
		if err := os.RemoveAll(filepath.Join(fixture.prepDir, "knowledge", "skills", slug)); err != nil {
			t.Fatal(err)
		}
		options := fixture.options(true)
		options.DeleteSkills = true
		options.DeleteSkillFiles = true
		_, err := ExecuteKnowledgeDeletion(context.Background(), options)
		if err == nil || !strings.Contains(err.Error(), "delete-prep backup") {
			t.Fatalf("error = %v", err)
		}
		fixture.assertKnowledgeCounts(t, 2, 3, 2, 2, 15, 4)
	})

	t.Run("path traversal slug", func(t *testing.T) {
		fixture := newDeleteTestFixture(t)
		execTestSQL(t, fixture.databasePath, `UPDATE skills SET package_slug='../escape' WHERE id=?`, fixture.skills[2].ID)
		options := fixture.options(true)
		options.DeleteSkills = true
		options.DeleteSkillFiles = true
		_, err := ExecuteKnowledgeDeletion(context.Background(), options)
		if err == nil || !strings.Contains(err.Error(), "single path segment") {
			t.Fatalf("error = %v", err)
		}
		fixture.assertKnowledgeCounts(t, 2, 3, 2, 2, 15, 4)
	})

	t.Run("symbolic link", func(t *testing.T) {
		fixture := newDeleteTestFixture(t)
		slug := fixture.skills[3].PackageSlug
		target := filepath.Join(fixture.skillsRoot, slug)
		if err := os.RemoveAll(target); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(fixture.skillsRoot, "unrelated-package"), target); err != nil {
			t.Skipf("cannot create a symlink: %v", err)
		}
		options := fixture.options(true)
		options.DeleteSkills = true
		options.DeleteSkillFiles = true
		_, err := ExecuteKnowledgeDeletion(context.Background(), options)
		if err == nil || !strings.Contains(err.Error(), "symbolic link") {
			t.Fatalf("error = %v", err)
		}
		fixture.assertKnowledgeCounts(t, 2, 3, 2, 2, 15, 4)
	})
}

func TestExecuteKnowledgeDeletionRejectsSkillFilesWithoutSkills(t *testing.T) {
	fixture := newDeleteTestFixture(t)
	options := fixture.options(false)
	options.DeleteSkillFiles = true
	_, err := ExecuteKnowledgeDeletion(context.Background(), options)
	if err == nil || !strings.Contains(err.Error(), "requires -delete-skills") {
		t.Fatalf("error = %v", err)
	}
}
