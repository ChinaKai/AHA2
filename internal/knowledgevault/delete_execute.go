package knowledgevault

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// The delete command is the only writer of legacy knowledge tables. delete-prep
// remains the read-only planner; this file executes a reviewed plan behind a
// fixed set of protections. Every protection must pass before a single row is
// deleted, and all row deletions share one SQLite transaction.
const (
	DeleteDryRunMode  = "dry-run"
	DeleteExecuteMode = "execute"
	DeleteScope       = "unlink_and_delete"

	deletePlanFileName   = "delete-plan.json"
	deleteBackupFileName = "backup.json"
	deleteResultFileName = "delete-result.json"

	deletePolicyTable  = "channel_knowledge_policies"
	deleteGrantTable   = "channel_knowledge_grants"
	deleteRecordsTable = "channel_knowledge_records"
	deleteSkillsTable  = "skills"

	defaultDeleteHealthURL = "http://127.0.0.1:8766/healthz"
	// The owner decision covers the four channel policies that point at the two
	// legacy knowledge roots. Any other count means the plan no longer matches
	// the reviewed scope.
	requiredDeletePolicyCount = 4
)

// deleteKnowledgeTableOrder is both the reviewed set and the delete order. A
// plan that names any other table, misses one, or reorders them is rejected
// instead of trusted.
var deleteKnowledgeTableOrder = []string{
	"knowledge_proposals",
	"knowledge_entries",
	"project_knowledge_bindings",
	"knowledge_libraries",
}

type DeleteExecuteOptions struct {
	DataDir      string
	DatabasePath string
	VaultPath    string
	// PrepDir is the delete-prep directory that contains backup.json,
	// delete-plan.json and the SQLite backup.
	PrepDir string
	Execute bool
	// Confirm must equal the SHA256 of delete-plan.json when Execute is set.
	Confirm          string
	DeleteSkills     bool
	DeleteSkillFiles bool
	HealthURL        string
	// VaultEnabled and IndexReady are evaluated by the caller with the same
	// readiness code the service uses. They are still re-checked here so the
	// destructive path cannot be entered by a caller that skips the gate.
	VaultEnabled bool
	IndexReady   bool
	Now          func() time.Time
	HTTPClient   *http.Client
}

type DeletePolicyRecord struct {
	ID                string `json:"id"`
	InstanceID        string `json:"instance_id"`
	EndpointID        string `json:"endpoint_id"`
	FixedIndexEntryID string `json:"fixed_index_entry_id,omitempty"`
}

type DeleteSkillRecord struct {
	ID          string `json:"id"`
	PackageSlug string `json:"package_slug"`
	Scope       string `json:"scope"`
	ProjectID   string `json:"project_id,omitempty"`
	Name        string `json:"name"`
}

type BlockingTaskRef struct {
	TaskID   string   `json:"task_id"`
	Title    string   `json:"title"`
	Status   string   `json:"status,omitempty"`
	SkillIDs []string `json:"skill_ids,omitempty"`
	Error    string   `json:"error,omitempty"`
}

// DeleteCounts keeps the per-table numbers so the result file proves what was
// deleted rather than only how many rows the plan claimed.
type DeleteCounts struct {
	Tables          map[string]int `json:"tables"`
	ChannelPolicies int            `json:"channel_knowledge_policies"`
	ChannelGrants   int            `json:"channel_knowledge_grants"`
	ChannelRecords  int            `json:"channel_knowledge_records"`
	Skills          int            `json:"skills"`
}

type DeleteExecuteReport struct {
	Scope          string `json:"scope"`
	Mode           string `json:"mode"`
	DeleteExecuted bool   `json:"delete_executed"`
	GeneratedAt    string `json:"generated_at"`

	DataDir      string `json:"data_dir"`
	DatabasePath string `json:"database_path"`
	VaultPath    string `json:"vault_path"`
	PrepDir      string `json:"prep_dir"`

	PlanPath      string `json:"plan_path"`
	PlanSHA256    string `json:"plan_sha256"`
	ConfirmSHA256 string `json:"confirm_sha256,omitempty"`

	BackupDatabase  string `json:"backup_database"`
	BackupBytes     int64  `json:"backup_bytes"`
	BackupSHA256    string `json:"backup_sha256"`
	BackupIntegrity string `json:"backup_integrity"`

	Before DeleteCounts  `json:"before"`
	After  *DeleteCounts `json:"after,omitempty"`

	Policies         []DeletePolicyRecord `json:"channel_knowledge_policies"`
	DeletedPolicyIDs []string             `json:"deleted_policy_ids"`
	Skills           []DeleteSkillRecord  `json:"skills"`

	SkillsDeleted     int      `json:"skills_deleted"`
	SkillFilesPlanned int      `json:"skill_files_planned"`
	SkillFilesDeleted int      `json:"skill_files_deleted"`
	SkillFileSamples  []string `json:"skill_file_samples,omitempty"`

	BlockingTaskRefs []BlockingTaskRef `json:"blocking_task_refs"`

	VaultEnabled     bool   `json:"vault_enabled"`
	IndexReady       bool   `json:"index_ready"`
	HealthURL        string `json:"health_url,omitempty"`
	HealthStatusCode int    `json:"health_status_code,omitempty"`

	PlanScopeRows  int `json:"plan_scope_rows"`
	PlanSkillsRows int `json:"plan_skills_rows"`
	// TotalRows is the owner-approved scope (knowledge + skills) and is only
	// reported. DeleteRows is what this invocation would actually remove; the
	// plan-row checks always use PlanScopeRows/PlanSkillsRows instead.
	TotalRows  int `json:"total_rows"`
	DeleteRows int `json:"delete_rows"`

	ReadyToExecute bool     `json:"ready_to_execute"`
	Warnings       []string `json:"warnings,omitempty"`
}

type deleteBackupMetadata struct {
	Mode           string `json:"mode"`
	GeneratedAt    string `json:"generated_at"`
	DatabasePath   string `json:"database_path"`
	BackupDatabase string `json:"backup_database"`
	BackupBytes    int64  `json:"backup_bytes"`
	BackupSHA256   string `json:"backup_sha256"`
	BackupMethod   string `json:"backup_method"`
	IntegrityCheck string `json:"integrity_check"`
	DeleteExecuted bool   `json:"delete_executed"`
}

type deleteExecutePaths struct {
	dataDir      string
	databasePath string
	vaultPath    string
	prepDir      string
}

type deleteQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func resolveDeleteExecutePaths(options DeleteExecuteOptions) (deleteExecutePaths, error) {
	paths := deleteExecutePaths{}
	dataDir := strings.TrimSpace(options.DataDir)
	if dataDir == "" {
		dataDir = ".data"
	}
	absolute, err := filepath.Abs(dataDir)
	if err != nil {
		return paths, err
	}
	paths.dataDir = absolute
	paths.databasePath = strings.TrimSpace(options.DatabasePath)
	if paths.databasePath == "" {
		paths.databasePath = filepath.Join(absolute, "aha2.db")
	}
	if paths.databasePath, err = filepath.Abs(paths.databasePath); err != nil {
		return paths, err
	}
	paths.vaultPath = strings.TrimSpace(options.VaultPath)
	if paths.vaultPath == "" {
		paths.vaultPath = filepath.Join(absolute, "knowledge-vault")
	}
	if paths.vaultPath, err = filepath.Abs(paths.vaultPath); err != nil {
		return paths, err
	}
	paths.prepDir = strings.TrimSpace(options.PrepDir)
	if paths.prepDir == "" {
		return paths, fmt.Errorf("-prep is required: point it at the delete-prep directory")
	}
	if paths.prepDir, err = filepath.Abs(paths.prepDir); err != nil {
		return paths, err
	}
	return paths, nil
}

// ExecuteKnowledgeDeletion validates a reviewed delete-prep directory and, when
// Execute is set, removes the legacy knowledge in one transaction. Without
// Execute it only reports what would be deleted.
func ExecuteKnowledgeDeletion(ctx context.Context, options DeleteExecuteOptions) (DeleteExecuteReport, error) {
	now := time.Now().UTC()
	if options.Now != nil {
		now = options.Now().UTC()
	}
	paths, err := resolveDeleteExecutePaths(options)
	if err != nil {
		return DeleteExecuteReport{}, err
	}
	report := DeleteExecuteReport{
		Scope: DeleteScope, GeneratedAt: now.Format(time.RFC3339),
		DataDir: paths.dataDir, DatabasePath: paths.databasePath,
		VaultPath: paths.vaultPath, PrepDir: paths.prepDir,
		VaultEnabled: options.VaultEnabled, IndexReady: options.IndexReady,
		DeletedPolicyIDs: []string{},
	}
	if options.Execute {
		report.Mode = DeleteExecuteMode
	} else {
		report.Mode = DeleteDryRunMode
	}
	if options.DeleteSkillFiles && !options.DeleteSkills {
		return report, fmt.Errorf("-delete-skill-files requires -delete-skills: package files are never removed without their skill rows")
	}

	plan, backup, planPath, planSHA, err := loadDeletePrep(paths.prepDir)
	if err != nil {
		return report, err
	}
	report.PlanPath = planPath
	report.PlanSHA256 = planSHA
	report.ConfirmSHA256 = strings.ToLower(strings.TrimSpace(options.Confirm))
	report.BackupDatabase = backup.BackupDatabase
	report.BackupBytes = backup.BackupBytes
	report.BackupSHA256 = backup.BackupSHA256
	report.BackupIntegrity = backup.IntegrityCheck
	report.PlanScopeRows = plan.ScopeRows
	report.PlanSkillsRows = plan.Skills.Rows
	report.TotalRows = plan.ScopeRows + plan.Skills.Rows
	report.DeleteRows = plan.ScopeRows
	if options.DeleteSkills {
		report.DeleteRows += plan.Skills.Rows
	}
	report.HealthURL = strings.TrimSpace(options.HealthURL)
	if report.HealthURL == "" {
		report.HealthURL = defaultDeleteHealthURL
	}

	planTables, err := deletePlanTables(plan)
	if err != nil {
		return report, err
	}
	if !sameFilesystemPath(plan.DatabasePath, paths.databasePath) {
		if options.Execute {
			return report, fmt.Errorf("delete-plan was prepared for database %s, not %s", plan.DatabasePath, paths.databasePath)
		}
		report.Warnings = append(report.Warnings,
			fmt.Sprintf("delete-plan was prepared for database %s; dry-run is inspecting %s", plan.DatabasePath, paths.databasePath))
	}

	if options.Execute {
		if err := validateDeleteConfirmation(planSHA, options.Confirm); err != nil {
			return report, err
		}
		if !options.VaultEnabled {
			return report, fmt.Errorf("knowledge vault is not enabled; enable it and rebuild its index before deleting legacy knowledge")
		}
		if !options.IndexReady {
			return report, fmt.Errorf("knowledge vault index is not ready; rebuild it before deleting legacy knowledge")
		}
		client := options.HTTPClient
		if client == nil {
			client = &http.Client{Timeout: 5 * time.Second}
		}
		statusCode, healthErr := deleteHealthStatus(ctx, client, report.HealthURL)
		report.HealthStatusCode = statusCode
		if healthErr != nil {
			return report, healthErr
		}
		if err := ensureDeleteResultWritable(paths.prepDir); err != nil {
			return report, err
		}
		return executeKnowledgeDeletion(ctx, paths, options, plan, planTables, report)
	}
	return inspectKnowledgeDeletion(ctx, paths, options, plan, planTables, report)
}

func loadDeletePrep(prepDir string) (DeletePrepReport, deleteBackupMetadata, string, string, error) {
	var plan DeletePrepReport
	var backup deleteBackupMetadata
	planPath := filepath.Join(prepDir, deletePlanFileName)
	planData, err := os.ReadFile(planPath)
	if err != nil {
		return plan, backup, planPath, "", fmt.Errorf("read %s: %w", planPath, err)
	}
	if err := json.Unmarshal(planData, &plan); err != nil {
		return plan, backup, planPath, "", fmt.Errorf("parse %s: %w", planPath, err)
	}
	backupPath := filepath.Join(prepDir, deleteBackupFileName)
	backupData, err := os.ReadFile(backupPath)
	if err != nil {
		return plan, backup, planPath, "", fmt.Errorf("read %s: %w", backupPath, err)
	}
	if err := json.Unmarshal(backupData, &backup); err != nil {
		return plan, backup, planPath, "", fmt.Errorf("parse %s: %w", backupPath, err)
	}
	if plan.Mode != DeletePrepMode {
		return plan, backup, planPath, "", fmt.Errorf("delete-plan mode is %q, want %q", plan.Mode, DeletePrepMode)
	}
	if plan.DeleteExecuted {
		return plan, backup, planPath, "", fmt.Errorf("delete-plan already records delete_executed=true")
	}
	if strings.TrimSpace(plan.BackupIntegrity) != "ok" {
		return plan, backup, planPath, "", fmt.Errorf("delete-plan backup_integrity is %q, want ok", plan.BackupIntegrity)
	}
	if backup.Mode != DeletePrepMode {
		return plan, backup, planPath, "", fmt.Errorf("backup.json mode is %q, want %q", backup.Mode, DeletePrepMode)
	}
	if backup.DeleteExecuted {
		return plan, backup, planPath, "", fmt.Errorf("backup.json already records delete_executed=true")
	}
	if strings.TrimSpace(backup.IntegrityCheck) != "ok" {
		return plan, backup, planPath, "", fmt.Errorf("backup.json integrity_check is %q, want ok", backup.IntegrityCheck)
	}
	if backup.BackupSHA256 == "" || !strings.EqualFold(backup.BackupSHA256, plan.BackupSHA256) {
		return plan, backup, planPath, "", fmt.Errorf("backup_sha256 mismatch: backup.json=%q delete-plan.json=%q", backup.BackupSHA256, plan.BackupSHA256)
	}
	if backup.BackupBytes <= 0 || backup.BackupBytes != plan.BackupBytes {
		return plan, backup, planPath, "", fmt.Errorf("backup_bytes mismatch: backup.json=%d delete-plan.json=%d", backup.BackupBytes, plan.BackupBytes)
	}
	if backup.BackupDatabase == "" || !sameFilesystemPath(backup.BackupDatabase, plan.BackupDatabase) {
		return plan, backup, planPath, "", fmt.Errorf("backup_database mismatch: backup.json=%q delete-plan.json=%q", backup.BackupDatabase, plan.BackupDatabase)
	}
	backupAbs, err := filepath.Abs(backup.BackupDatabase)
	if err != nil {
		return plan, backup, planPath, "", err
	}
	if !pathWithin(prepDir, backupAbs) {
		return plan, backup, planPath, "", fmt.Errorf("backup database %s is outside the delete-prep directory %s", backupAbs, prepDir)
	}
	info, err := os.Stat(backupAbs)
	if err != nil {
		return plan, backup, planPath, "", fmt.Errorf("stat backup database: %w", err)
	}
	if !info.Mode().IsRegular() {
		return plan, backup, planPath, "", fmt.Errorf("backup database %s is not a regular file", backupAbs)
	}
	if info.Size() != backup.BackupBytes {
		return plan, backup, planPath, "", fmt.Errorf("backup database size is %d bytes, backup.json records %d", info.Size(), backup.BackupBytes)
	}
	if plan.ScopeRows <= 0 {
		return plan, backup, planPath, "", fmt.Errorf("delete-plan scope_rows is %d; nothing to delete", plan.ScopeRows)
	}
	if plan.Skills.Rows < 0 {
		return plan, backup, planPath, "", fmt.Errorf("delete-plan skills rows is %d", plan.Skills.Rows)
	}
	hash := sha256.Sum256(planData)
	return plan, backup, planPath, hex.EncodeToString(hash[:]), nil
}

func validateDeleteConfirmation(planSHA, confirm string) error {
	confirm = strings.ToLower(strings.TrimSpace(confirm))
	if confirm == "" {
		return fmt.Errorf("-execute requires -confirm with the SHA256 of %s", deletePlanFileName)
	}
	if confirm != planSHA {
		return fmt.Errorf("confirm hash mismatch: got %s, want %s (SHA256 of %s)", confirm, planSHA, deletePlanFileName)
	}
	return nil
}

func deletePlanTables(plan DeletePrepReport) ([]DeleteTablePlan, error) {
	byName := map[string]DeleteTablePlan{}
	for _, table := range plan.Tables {
		if !table.InScope {
			continue
		}
		byName[table.Table] = table
	}
	tables := make([]DeleteTablePlan, 0, len(deleteKnowledgeTableOrder))
	sum := 0
	for index, name := range deleteKnowledgeTableOrder {
		table, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("delete-plan does not include in-scope table %s", name)
		}
		if table.DeleteOrder != index+1 {
			return nil, fmt.Errorf("delete-plan table %s has delete_order %d, want %d", name, table.DeleteOrder, index+1)
		}
		tables = append(tables, table)
		sum += table.Rows
	}
	if len(byName) != len(deleteKnowledgeTableOrder) {
		return nil, fmt.Errorf("delete-plan includes unexpected in-scope tables")
	}
	if sum != plan.ScopeRows {
		return nil, fmt.Errorf("delete-plan in-scope rows sum to %d, scope_rows is %d", sum, plan.ScopeRows)
	}
	skillsRows := 0
	skillsFound := false
	for _, table := range plan.Tables {
		if table.Table == deleteSkillsTable && !table.InScope {
			skillsRows = table.Rows
			skillsFound = true
		}
	}
	if !skillsFound {
		return nil, fmt.Errorf("delete-plan does not include the %s reference row", deleteSkillsTable)
	}
	if plan.Skills.Rows != skillsRows {
		return nil, fmt.Errorf("delete-plan skills rows disagree: skills.rows=%d table.rows=%d", plan.Skills.Rows, skillsRows)
	}
	return tables, nil
}

func inspectKnowledgeDeletion(ctx context.Context, paths deleteExecutePaths, options DeleteExecuteOptions, plan DeletePrepReport, planTables []DeleteTablePlan, report DeleteExecuteReport) (DeleteExecuteReport, error) {
	database, err := openReadOnlySQLite(plan.DatabasePath)
	if err != nil {
		return report, err
	}
	defer database.Close()
	counts, policies, skills, refs, err := readDeleteState(ctx, database, planTables)
	if err != nil {
		return report, err
	}
	report.Before = counts
	report.Policies = policies
	report.Skills = skills
	report.BlockingTaskRefs = refs
	for _, problem := range deleteStateProblems(plan, planTables, counts, len(policies)) {
		report.Warnings = append(report.Warnings, problem)
	}

	if options.DeleteSkills && len(refs) > 0 {
		report.Warnings = append(report.Warnings,
			fmt.Sprintf("%d open task(s) still reference legacy skill IDs; execute with -delete-skills will refuse until they are migrated or cleared", len(refs)))
	}
	if !options.VaultEnabled {
		report.Warnings = append(report.Warnings, "knowledge vault is not enabled; execute will refuse")
	}
	if !options.IndexReady {
		report.Warnings = append(report.Warnings, "knowledge vault index is not ready; execute will refuse")
	}
	if options.DeleteSkillFiles {
		planned, fileErr := validateDeleteSkillFiles(paths.dataDir, paths.prepDir, paths.vaultPath, skills)
		if fileErr != nil {
			report.Warnings = append(report.Warnings, fileErr.Error())
		} else {
			report.SkillFilesPlanned = len(planned)
			report.SkillFileSamples = planned
		}
	}
	report.ReadyToExecute = len(report.Warnings) == 0
	return report, nil
}

func executeKnowledgeDeletion(ctx context.Context, paths deleteExecutePaths, options DeleteExecuteOptions, plan DeletePrepReport, planTables []DeleteTablePlan, report DeleteExecuteReport) (DeleteExecuteReport, error) {
	database, err := openReadWriteSQLite(paths.databasePath)
	if err != nil {
		return report, err
	}
	defer database.Close()

	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return report, fmt.Errorf("begin delete transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	counts, policies, skills, refs, err := readDeleteState(ctx, tx, planTables)
	if err != nil {
		return report, err
	}
	report.Before = counts
	report.Policies = policies
	report.Skills = skills
	report.BlockingTaskRefs = refs
	if problems := deleteStateProblems(plan, planTables, counts, len(policies)); len(problems) > 0 {
		return report, fmt.Errorf("refusing to delete: %s", strings.Join(problems, "; "))
	}
	if options.DeleteSkills && len(refs) > 0 {
		return report, fmt.Errorf("refusing to delete skills: %d open task(s) still reference legacy skill IDs", len(refs))
	}

	var filePlans []string
	if options.DeleteSkillFiles {
		filePlans, err = validateDeleteSkillFiles(paths.dataDir, paths.prepDir, paths.vaultPath, skills)
		if err != nil {
			return report, err
		}
		report.SkillFilesPlanned = len(filePlans)
	}

	for _, policy := range policies {
		report.DeletedPolicyIDs = append(report.DeletedPolicyIDs, policy.ID)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM `+quoteIdentifier(deletePolicyTable)); err != nil {
		return report, fmt.Errorf("delete channel knowledge policies: %w", err)
	}
	for _, table := range planTables {
		result, err := tx.ExecContext(ctx, `DELETE FROM `+quoteIdentifier(table.Table))
		if err != nil {
			return report, fmt.Errorf("delete %s: %w", table.Table, err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return report, fmt.Errorf("count deleted %s rows: %w", table.Table, err)
		}
		if int(affected) != table.Rows {
			return report, fmt.Errorf("delete %s removed %d rows, delete-plan expects %d", table.Table, affected, table.Rows)
		}
	}
	if options.DeleteSkills {
		result, err := tx.ExecContext(ctx, `DELETE FROM `+quoteIdentifier(deleteSkillsTable))
		if err != nil {
			return report, fmt.Errorf("delete skills: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return report, fmt.Errorf("count deleted skills rows: %w", err)
		}
		if int(affected) != plan.Skills.Rows {
			return report, fmt.Errorf("delete skills removed %d rows, delete-plan expects %d", affected, plan.Skills.Rows)
		}
	}
	if err := tx.Commit(); err != nil {
		return report, fmt.Errorf("commit delete transaction: %w", err)
	}

	report.DeleteExecuted = true
	if options.DeleteSkills {
		report.SkillsDeleted = len(skills)
	}
	after, err := readDeleteCounts(ctx, database, planTables)
	if err != nil {
		return report, fmt.Errorf("delete committed but post-delete counts failed: %w", err)
	}
	report.After = &after

	if options.DeleteSkillFiles {
		deleted, samples, removeErr := removeDeleteSkillDirs(filePlans)
		report.SkillFilesDeleted = deleted
		report.SkillFileSamples = samples
		if removeErr != nil {
			report.Warnings = append(report.Warnings, "database deletion committed, but skill package cleanup failed: "+removeErr.Error())
			if writeErr := writeDeleteResult(paths.prepDir, report); writeErr != nil {
				return report, fmt.Errorf("skill package cleanup failed (%v) and %s could not be written: %w", removeErr, deleteResultFileName, writeErr)
			}
			return report, fmt.Errorf("database deletion committed but skill package cleanup failed: %w", removeErr)
		}
	}
	if err := writeDeleteResult(paths.prepDir, report); err != nil {
		return report, fmt.Errorf("database deletion committed but %s could not be written: %w", deleteResultFileName, err)
	}
	return report, nil
}

func readDeleteState(ctx context.Context, queryer deleteQueryer, planTables []DeleteTablePlan) (DeleteCounts, []DeletePolicyRecord, []DeleteSkillRecord, []BlockingTaskRef, error) {
	counts, err := readDeleteCounts(ctx, queryer, planTables)
	if err != nil {
		return DeleteCounts{}, nil, nil, nil, err
	}
	policies, err := readDeletePolicies(ctx, queryer)
	if err != nil {
		return DeleteCounts{}, nil, nil, nil, err
	}
	skills, err := readDeleteSkills(ctx, queryer)
	if err != nil {
		return DeleteCounts{}, nil, nil, nil, err
	}
	refs, err := readBlockingTaskRefs(ctx, queryer, skills)
	if err != nil {
		return DeleteCounts{}, nil, nil, nil, err
	}
	return counts, policies, skills, refs, nil
}

func readDeleteCounts(ctx context.Context, queryer deleteQueryer, planTables []DeleteTablePlan) (DeleteCounts, error) {
	counts := DeleteCounts{Tables: make(map[string]int, len(planTables))}
	for _, table := range planTables {
		count, err := deleteRowCount(ctx, queryer, table.Table)
		if err != nil {
			return counts, err
		}
		counts.Tables[table.Table] = count
	}
	var err error
	if counts.ChannelPolicies, err = deleteRowCount(ctx, queryer, deletePolicyTable); err != nil {
		return counts, err
	}
	if counts.ChannelGrants, err = deleteRowCount(ctx, queryer, deleteGrantTable); err != nil {
		return counts, err
	}
	exists, err := deleteTableExists(ctx, queryer, deleteRecordsTable)
	if err != nil {
		return counts, err
	}
	if exists {
		if counts.ChannelRecords, err = deleteRowCount(ctx, queryer, deleteRecordsTable); err != nil {
			return counts, err
		}
	}
	if counts.Skills, err = deleteRowCount(ctx, queryer, deleteSkillsTable); err != nil {
		return counts, err
	}
	return counts, nil
}

func deleteStateProblems(plan DeletePrepReport, planTables []DeleteTablePlan, counts DeleteCounts, policyCount int) []string {
	problems := []string{}
	for _, table := range planTables {
		if counts.Tables[table.Table] != table.Rows {
			problems = append(problems, fmt.Sprintf("%s has %d rows, delete-plan expects %d", table.Table, counts.Tables[table.Table], table.Rows))
		}
	}
	if counts.Skills != plan.Skills.Rows {
		problems = append(problems, fmt.Sprintf("skills has %d rows, delete-plan expects %d", counts.Skills, plan.Skills.Rows))
	}
	if policyCount != requiredDeletePolicyCount {
		problems = append(problems, fmt.Sprintf("channel_knowledge_policies has %d rows, want exactly %d", policyCount, requiredDeletePolicyCount))
	}
	if counts.ChannelGrants != 0 {
		problems = append(problems, fmt.Sprintf("channel_knowledge_grants has %d rows, want 0", counts.ChannelGrants))
	}
	if counts.ChannelRecords != 0 {
		problems = append(problems, fmt.Sprintf("channel_knowledge_records has %d rows; resolve them before deleting knowledge entries", counts.ChannelRecords))
	}
	return problems
}

func readDeletePolicies(ctx context.Context, queryer deleteQueryer) ([]DeletePolicyRecord, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT id,instance_id,endpoint_id,COALESCE(fixed_index_entry_id,'') FROM `+quoteIdentifier(deletePolicyTable)+` ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("read channel knowledge policies: %w", err)
	}
	defer rows.Close()
	policies := []DeletePolicyRecord{}
	for rows.Next() {
		var item DeletePolicyRecord
		if err := rows.Scan(&item.ID, &item.InstanceID, &item.EndpointID, &item.FixedIndexEntryID); err != nil {
			return nil, err
		}
		policies = append(policies, item)
	}
	return policies, rows.Err()
}

func readDeleteSkills(ctx context.Context, queryer deleteQueryer) ([]DeleteSkillRecord, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT id,package_slug,scope,project_id,name FROM `+quoteIdentifier(deleteSkillsTable)+` ORDER BY scope,package_slug,id`)
	if err != nil {
		return nil, fmt.Errorf("read legacy skills: %w", err)
	}
	defer rows.Close()
	skills := []DeleteSkillRecord{}
	for rows.Next() {
		var item DeleteSkillRecord
		if err := rows.Scan(&item.ID, &item.PackageSlug, &item.Scope, &item.ProjectID, &item.Name); err != nil {
			return nil, err
		}
		skills = append(skills, item)
	}
	return skills, rows.Err()
}

// readBlockingTaskRefs reports open tasks whose skill selection still points at
// a legacy skill row. Vault keys ("vault-skill:...") do not match a legacy ID
// and therefore do not block.
func readBlockingTaskRefs(ctx context.Context, queryer deleteQueryer, skills []DeleteSkillRecord) ([]BlockingTaskRef, error) {
	skillIDs := make(map[string]bool, len(skills))
	for _, skill := range skills {
		skillIDs[skill.ID] = true
	}
	rows, err := queryer.QueryContext(ctx, `SELECT id,title,status,skill_ids_json FROM tasks
		WHERE status IN ('draft','preparing','active','waiting_user','blocked','failed') ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("scan open task skill references: %w", err)
	}
	defer rows.Close()
	refs := []BlockingTaskRef{}
	for rows.Next() {
		var taskID, title, status, raw string
		if err := rows.Scan(&taskID, &title, &status, &raw); err != nil {
			return nil, err
		}
		selected := []string{}
		if err := json.Unmarshal([]byte(raw), &selected); err != nil {
			refs = append(refs, BlockingTaskRef{TaskID: taskID, Title: title, Status: status,
				Error: "skill_ids_json is not a string array: " + err.Error()})
			continue
		}
		matches := []string{}
		for _, id := range selected {
			if skillIDs[strings.TrimSpace(id)] {
				matches = append(matches, id)
			}
		}
		if len(matches) > 0 {
			refs = append(refs, BlockingTaskRef{TaskID: taskID, Title: title, Status: status, SkillIDs: matches})
		}
	}
	return refs, rows.Err()
}

// validateDeleteSkillFiles validates every package directory before the
// database transaction starts. It returns the exact directories that may be
// removed; any failed check refuses the whole execute.
func validateDeleteSkillFiles(dataDir, prepDir, vaultPath string, skills []DeleteSkillRecord) ([]string, error) {
	skillsRoot := filepath.Join(dataDir, "knowledge", "skills")
	backupRoot := filepath.Join(prepDir, "knowledge", "skills")
	planned := make([]string, 0, len(skills))
	if len(skills) == 0 {
		return planned, nil
	}
	if err := requireRegularDirectory(skillsRoot); err != nil {
		return nil, fmt.Errorf("legacy skills root: %w", err)
	}
	if err := requireRegularDirectory(backupRoot); err != nil {
		return nil, fmt.Errorf("delete-prep skills backup: %w", err)
	}
	config := Config{DataDir: dataDir, VaultPath: vaultPath}
	for _, skill := range skills {
		slug := strings.TrimSpace(skill.PackageSlug)
		if slug == "" {
			slug = strings.TrimSpace(skill.ID)
		}
		if err := validDeleteSkillSlug(slug); err != nil {
			return nil, err
		}
		target := filepath.Join(skillsRoot, slug)
		if !pathWithin(skillsRoot, target) {
			return nil, fmt.Errorf("skill %s resolves outside %s", slug, skillsRoot)
		}
		if err := requireRegularDirectory(target); err != nil {
			return nil, fmt.Errorf("legacy skill package %s: %w", slug, err)
		}
		resolvedRoot, err := filepath.EvalSymlinks(skillsRoot)
		if err != nil {
			return nil, fmt.Errorf("resolve legacy skills root: %w", err)
		}
		resolvedTarget, err := filepath.EvalSymlinks(target)
		if err != nil {
			return nil, fmt.Errorf("resolve legacy skill package %s: %w", slug, err)
		}
		if !pathWithin(resolvedRoot, resolvedTarget) {
			return nil, fmt.Errorf("legacy skill package %s resolves outside %s", slug, skillsRoot)
		}
		backupTarget := filepath.Join(backupRoot, slug)
		if !pathWithin(backupRoot, backupTarget) {
			return nil, fmt.Errorf("skill %s backup resolves outside %s", slug, backupRoot)
		}
		if err := requireRegularDirectory(backupTarget); err != nil {
			return nil, fmt.Errorf("delete-prep backup for skill %s: %w", slug, err)
		}
		if !skillDirectoryExists(config, legacySkill{
			ID: skill.ID, PackageSlug: skill.PackageSlug, Scope: skill.Scope,
			ProjectID: skill.ProjectID, Name: skill.Name,
		}) {
			return nil, fmt.Errorf("vault has no SKILL.md for legacy skill %s (%s)", slug, skill.Scope)
		}
		planned = append(planned, target)
	}
	sort.Strings(planned)
	return planned, nil
}

func removeDeleteSkillDirs(planned []string) (int, []string, error) {
	deleted := 0
	samples := make([]string, 0, len(planned))
	for _, target := range planned {
		info, err := os.Lstat(target)
		if err != nil {
			return deleted, samples, fmt.Errorf("inspect %s: %w", target, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return deleted, samples, fmt.Errorf("refusing to delete %s: it is no longer a regular directory", target)
		}
		if err := os.RemoveAll(target); err != nil {
			return deleted, samples, fmt.Errorf("delete %s: %w", target, err)
		}
		deleted++
		samples = append(samples, target)
	}
	return deleted, samples, nil
}

func validDeleteSkillSlug(slug string) error {
	if slug == "" {
		return fmt.Errorf("legacy skill has an empty package slug")
	}
	if slug == "." || slug == ".." || filepath.Base(slug) != slug ||
		strings.ContainsAny(slug, `/\`) || strings.ContainsRune(slug, 0) {
		return fmt.Errorf("legacy skill package slug %q is not a single path segment", slug)
	}
	return nil
}

func requireRegularDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symbolic link", path)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", path)
	}
	return nil
}

func pathWithin(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return false
	}
	return true
}

func sameFilesystemPath(left, right string) bool {
	if strings.TrimSpace(left) == "" || strings.TrimSpace(right) == "" {
		return false
	}
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	leftAbs = filepath.Clean(leftAbs)
	rightAbs = filepath.Clean(rightAbs)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(leftAbs, rightAbs)
	}
	return leftAbs == rightAbs
}

func deleteHealthStatus(ctx context.Context, client *http.Client, rawURL string) (int, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return 0, fmt.Errorf("invalid health URL %q", rawURL)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, err
	}
	response, err := client.Do(request)
	if err != nil {
		// A stopped service refuses the connection; that is the expected state
		// for an execute. Only a live 200 blocks.
		return 0, nil
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode == http.StatusOK {
		return response.StatusCode, fmt.Errorf("health endpoint %s returned HTTP 200; stop the AHA2 service before executing deletion", rawURL)
	}
	return response.StatusCode, nil
}

func ensureDeleteResultWritable(prepDir string) error {
	file, err := os.CreateTemp(prepDir, ".delete-result-probe-*")
	if err != nil {
		return fmt.Errorf("delete-prep directory is not writable: %w", err)
	}
	name := file.Name()
	_ = file.Close()
	_ = os.Remove(name)
	return nil
}

func writeDeleteResult(prepDir string, report DeleteExecuteReport) error {
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	return os.WriteFile(filepath.Join(prepDir, deleteResultFileName), encoded, 0o600)
}

func openReadWriteSQLite(path string) (*sql.DB, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	dsn := SQLiteFileDSN(absolute, "_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_txlock=immediate")
	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database for deletion: %w", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	return database, nil
}

func deleteRowCount(ctx context.Context, queryer deleteQueryer, table string) (int, error) {
	var count int
	if err := queryer.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+quoteIdentifier(table)).Scan(&count); err != nil {
		return 0, fmt.Errorf("count %s: %w", table, err)
	}
	return count, nil
}

func deleteTableExists(ctx context.Context, queryer deleteQueryer, table string) (bool, error) {
	var count int
	err := queryer.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count)
	return count > 0, err
}
