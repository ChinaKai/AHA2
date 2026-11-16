package knowledgevault

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// PrepareKnowledgeDeletion backs up the legacy knowledge tables and writes a
// deletion plan that can be reviewed before anything is removed. It never
// runs DELETE: the legacy database is opened read-only, the backup is created
// with VACUUM INTO, and every count and export is produced from that backup so
// the plan cannot drift from the data it protects.
const (
	DeletePrepMode          = "backup-and-dry-run"
	deletePrepBackupsFolder = "backups"
	deletePrepNamePrefix    = "knowledge-delete-"
)

type DeletePrepOptions struct {
	DataDir      string
	DatabasePath string
	VaultPath    string
	BackupDir    string
	// Now is injectable for tests; the directory name uses UTC.
	Now func() time.Time
}

// DeleteTablePlan is one legacy table covered by the deletion plan.
type DeleteTablePlan struct {
	Table       string `json:"table"`
	SourceRows  int    `json:"source_rows"`
	Rows        int    `json:"rows"`
	InScope     bool   `json:"in_scope"`
	ExportJSONL string `json:"export_jsonl,omitempty"`
	ExportMD    string `json:"export_markdown,omitempty"`
	DeleteOrder int    `json:"delete_order,omitempty"`
	Note        string `json:"note,omitempty"`
}

// UnboundProjectPlan describes a project that the vault manifest does not
// bind. Deleting the legacy knowledge tables removes its legacy entries even
// though the vault does not carry them.
type UnboundProjectPlan struct {
	ProjectID   string `json:"project_id"`
	Name        string `json:"name"`
	ProjectType string `json:"project_type,omitempty"`
	Entries     int    `json:"entries"`
	LibraryID   string `json:"library_id,omitempty"`
	LibraryName string `json:"library_name,omitempty"`
}

// SkillDeletionPlan is reported separately because the owner has not decided
// whether the legacy skills table is part of the deletion scope.
type SkillDeletionPlan struct {
	Rows          int    `json:"rows"`
	Global        int    `json:"global"`
	Project       int    `json:"project"`
	AutoAttach    int    `json:"auto_attach"`
	PackageFiles  int    `json:"package_skill_files"`
	BackupPath    string `json:"backup_path,omitempty"`
	InDeleteScope bool   `json:"in_delete_scope"`
	Decision      string `json:"decision"`
}

type DeletePrepReport struct {
	Mode             string               `json:"mode"`
	GeneratedAt      string               `json:"generated_at"`
	DataDir          string               `json:"data_dir"`
	DatabasePath     string               `json:"database_path"`
	DatabaseBytes    int64                `json:"database_bytes"`
	DatabaseSHA256   string               `json:"database_sha256"`
	DatabaseHasWAL   bool                 `json:"database_has_wal"`
	VaultPath        string               `json:"vault_path"`
	VaultManifest    bool                 `json:"vault_manifest_read"`
	BackupPath       string               `json:"backup_path"`
	BackupDatabase   string               `json:"backup_database"`
	BackupBytes      int64                `json:"backup_bytes"`
	BackupSHA256     string               `json:"backup_sha256"`
	BackupIntegrity  string               `json:"backup_integrity"`
	BackupMethod     string               `json:"backup_method"`
	ExportDir        string               `json:"export_dir"`
	PlanJSON         string               `json:"plan_json"`
	PlanMarkdown     string               `json:"plan_markdown"`
	Tables           []DeleteTablePlan    `json:"tables"`
	TotalRows        int                  `json:"total_rows"`
	ScopeRows        int                  `json:"scope_rows"`
	BoundProjects    []string             `json:"bound_projects"`
	UnboundProjects  []UnboundProjectPlan `json:"unbound_projects"`
	UnboundEntries   int                  `json:"unbound_entries"`
	OrphanProjectIDs []string             `json:"orphan_project_ids"`
	OrphanEntries    int                  `json:"orphan_entries"`
	UnboundLibraries int                  `json:"unbound_libraries"`
	Skills           SkillDeletionPlan    `json:"skills"`
	Blockers         []string             `json:"blockers,omitempty"`
	Warnings         []string             `json:"warnings,omitempty"`
	WaitingFor       []string             `json:"waiting_for"`
	DeleteExecuted   bool                 `json:"delete_executed"`
}

type deletePrepPaths struct {
	dataDir      string
	databasePath string
	vaultPath    string
	backupDir    string
}

func resolveDeletePrepPaths(options DeletePrepOptions) (deletePrepPaths, error) {
	paths := deletePrepPaths{}
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
	paths.databasePath, err = filepath.Abs(paths.databasePath)
	if err != nil {
		return paths, err
	}
	paths.vaultPath = strings.TrimSpace(options.VaultPath)
	if paths.vaultPath == "" {
		paths.vaultPath = filepath.Join(absolute, "knowledge-vault")
	}
	paths.vaultPath, err = filepath.Abs(paths.vaultPath)
	if err != nil {
		return paths, err
	}
	paths.backupDir = strings.TrimSpace(options.BackupDir)
	if paths.backupDir == "" {
		paths.backupDir = filepath.Join(absolute, deletePrepBackupsFolder)
	}
	paths.backupDir, err = filepath.Abs(paths.backupDir)
	if err != nil {
		return paths, err
	}
	return paths, nil
}

// PrepareKnowledgeDeletion creates a timestamped SQLite snapshot plus readable
// exports and a dry-run deletion plan. The legacy database is never modified.
func PrepareKnowledgeDeletion(ctx context.Context, options DeletePrepOptions) (DeletePrepReport, error) {
	now := time.Now().UTC()
	if options.Now != nil {
		now = options.Now().UTC()
	}
	paths, err := resolveDeletePrepPaths(options)
	if err != nil {
		return DeletePrepReport{}, err
	}
	report := DeletePrepReport{
		Mode: DeletePrepMode, GeneratedAt: now.Format(time.RFC3339),
		DataDir: paths.dataDir, DatabasePath: paths.databasePath, VaultPath: paths.vaultPath,
		DeleteExecuted: false,
	}
	if info, statErr := os.Stat(paths.databasePath); statErr != nil || info.IsDir() {
		if statErr == nil {
			statErr = fmt.Errorf("path is a directory")
		}
		return report, fmt.Errorf("AHA2 database does not exist at %s: %w", paths.databasePath, statErr)
	}
	report.DatabaseBytes = fileSize(paths.databasePath)
	report.DatabaseSHA256, _ = fileSHA256(paths.databasePath)
	if _, statErr := os.Stat(paths.databasePath + "-wal"); statErr == nil {
		report.DatabaseHasWAL = true
	}

	source, err := openReadOnlySQLite(paths.databasePath)
	if err != nil {
		return report, fmt.Errorf("open source database: %w", err)
	}
	sourceCounts, countErr := deletePrepTableCounts(ctx, source)
	source.Close()
	if countErr != nil {
		return report, countErr
	}

	backupPath, backupDatabase, backupMethod, err := backupDatabaseForDeletion(ctx, paths.databasePath, paths.backupDir, now)
	if err != nil {
		return report, err
	}
	report.BackupPath = backupPath
	report.BackupDatabase = backupDatabase
	report.BackupMethod = backupMethod
	report.BackupBytes = fileSize(backupDatabase)
	report.BackupSHA256, _ = fileSHA256(backupDatabase)

	backup, err := openReadOnlySQLite(backupDatabase)
	if err != nil {
		return report, fmt.Errorf("open backup database: %w", err)
	}
	defer backup.Close()
	report.BackupIntegrity = integrityCheck(ctx, backup)

	exportDir := filepath.Join(backupPath, "export")
	if err := os.MkdirAll(exportDir, 0o700); err != nil {
		return report, err
	}
	report.ExportDir = exportDir

	scope := []struct {
		table string
		order int
		note  string
	}{
		{table: "knowledge_proposals", order: 1, note: "无外键；先删提案，避免留下指向已删条目的孤儿提案"},
		{table: "knowledge_entries", order: 2, note: "知识正文、层级与状态；被渠道知识策略/授权/记录以 RESTRICT 引用时不能直接删"},
		{table: "project_knowledge_bindings", order: 3, note: "知识库到项目的绑定；随 knowledge_libraries 级联删除，单独计数便于核对"},
		{table: "knowledge_libraries", order: 4, note: "知识库容器；删除会级联清空 project_knowledge_bindings"},
	}
	report.Tables = make([]DeleteTablePlan, 0, len(scope))
	for _, item := range scope {
		plan := DeleteTablePlan{Table: item.table, InScope: true, DeleteOrder: item.order, Note: item.note}
		plan.SourceRows = sourceCounts[item.table]
		rows, jsonl, markdown, exportErr := exportDeletePrepTable(ctx, backup, item.table, exportDir)
		if exportErr != nil {
			return report, exportErr
		}
		plan.Rows = rows
		plan.ExportJSONL = jsonl
		plan.ExportMD = markdown
		report.Tables = append(report.Tables, plan)
		report.TotalRows += rows
		report.ScopeRows += rows
	}

	reference := []struct {
		table string
		note  string
	}{
		{table: "skills", note: "旧技能表；owner 尚未确认是否删除，本工具只备份与列出"},
		{table: "projects", note: "项目清单；用于判断哪些项目未绑定到 vault"},
	}
	for _, item := range reference {
		rows, jsonl, markdown, exportErr := exportDeletePrepTable(ctx, backup, item.table, exportDir)
		if exportErr != nil {
			return report, exportErr
		}
		report.Tables = append(report.Tables, DeleteTablePlan{
			Table: item.table, SourceRows: sourceCounts[item.table], Rows: rows,
			InScope: false, ExportJSONL: jsonl, ExportMD: markdown, Note: item.note,
		})
		report.TotalRows += rows
	}

	report.BoundProjects, report.UnboundProjects, report.OrphanProjectIDs, err =
		deletePrepProjectScope(ctx, backup, paths.vaultPath, &report)
	if err != nil {
		return report, err
	}
	for _, project := range report.UnboundProjects {
		report.UnboundEntries += project.Entries
	}
	report.OrphanEntries = countQuery(ctx, backup, `SELECT COUNT(*) FROM knowledge_entries WHERE scope='project' AND project_id<>'' AND project_id NOT IN (SELECT id FROM projects)`)
	report.UnboundLibraries = countQuery(ctx, backup, `SELECT COUNT(*) FROM knowledge_libraries WHERE id NOT IN (SELECT library_id FROM project_knowledge_bindings)`)

	skills, skillsErr := deletePrepSkills(ctx, backup, paths.dataDir, backupPath)
	if skillsErr != nil {
		return report, skillsErr
	}
	report.Skills = skills

	report.Blockers = append(report.Blockers, deletePrepBlockers(ctx, backup)...)
	if report.DatabaseHasWAL {
		report.Warnings = append(report.Warnings, "源库存在 WAL；备份通过 VACUUM INTO 生成一致快照，导出与计数都来自该快照")
	}
	for _, table := range report.Tables {
		if table.SourceRows != table.Rows {
			report.Warnings = append(report.Warnings,
				fmt.Sprintf("表 %s 在备份期间发生变化：源库 %d 行，备份 %d 行；以备份快照为准", table.Table, table.SourceRows, table.Rows))
		}
	}
	if !report.VaultManifest {
		report.Blockers = append(report.Blockers, "无法读取 vault manifest，未绑定项目清单不可信；删除前必须先确认 vault 绑定")
	}
	report.WaitingFor = []string{
		"task-467（vault 技能开关）与 task-470（写入跟随读取源）上线并验证",
		"Owner 最终确认删除范围（含 skills 是否随旧知识表一并删除）",
		"确认无渠道知识策略/授权/记录以 RESTRICT 引用待删知识条目",
	}

	report.PlanJSON = filepath.Join(backupPath, "delete-plan.json")
	report.PlanMarkdown = filepath.Join(backupPath, "delete-plan.md")
	if err := writeDeletePrepMetadata(report, backupPath); err != nil {
		return report, err
	}
	if err := writeDeletePrepPlanJSON(report.PlanJSON, report); err != nil {
		return report, err
	}
	if err := writeDeletePrepPlanMarkdown(report.PlanMarkdown, report); err != nil {
		return report, err
	}
	return report, nil
}

func deletePrepTableCounts(ctx context.Context, db *sql.DB) (map[string]int, error) {
	counts := map[string]int{}
	for _, table := range []string{"knowledge_entries", "knowledge_proposals", "knowledge_libraries", "project_knowledge_bindings", "skills", "projects"} {
		exists, err := tableExists(ctx, db, table)
		if err != nil {
			return nil, err
		}
		if !exists {
			continue
		}
		count, err := countRows(ctx, db, table)
		if err != nil {
			return nil, fmt.Errorf("count %s: %w", table, err)
		}
		counts[table] = count
	}
	if _, ok := counts["knowledge_entries"]; !ok {
		return nil, fmt.Errorf("knowledge_entries table is missing; this database has no legacy knowledge to prepare")
	}
	return counts, nil
}

func backupDatabaseForDeletion(ctx context.Context, databasePath, backupDir string, now time.Time) (string, string, string, error) {
	// Reject unusable backup locations up front. Without this a plain file (or
	// an unreadable directory) surfaces as a non-NotExist stat error inside the
	// uniqueness loop below, and treating it as "already exists" would spin
	// forever instead of failing fast.
	if info, err := os.Stat(backupDir); err == nil {
		if !info.IsDir() {
			return "", "", "", fmt.Errorf("backup directory %s is not a directory", backupDir)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", "", "", fmt.Errorf("inspect backup directory %s: %w", backupDir, err)
	}
	base := filepath.Join(backupDir, deletePrepNamePrefix+now.Format("20060102-150405"))
	target := base
	for attempt := 2; ; attempt++ {
		_, statErr := os.Stat(target)
		if errors.Is(statErr, os.ErrNotExist) {
			break
		}
		if statErr != nil {
			return "", "", "", fmt.Errorf("inspect backup target %s: %w", target, statErr)
		}
		target = fmt.Sprintf("%s-%d", base, attempt)
	}
	if err := os.MkdirAll(target, 0o700); err != nil {
		return "", "", "", err
	}
	backupDatabase := filepath.Join(target, "aha2.db")
	source, err := openReadOnlySQLite(databasePath)
	if err != nil {
		return "", "", "", fmt.Errorf("open source for backup: %w", err)
	}
	defer source.Close()
	if _, err := source.ExecContext(ctx, `VACUUM INTO ?`, backupDatabase); err != nil {
		return "", "", "", fmt.Errorf("SQLite backup failed: %w", err)
	}
	if err := os.Chmod(backupDatabase, 0o600); err != nil {
		return "", "", "", err
	}
	return target, backupDatabase, "vacuum-into", nil
}

func integrityCheck(ctx context.Context, db *sql.DB) string {
	var result string
	if err := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&result); err != nil {
		return "error: " + err.Error()
	}
	return result
}

func tableExists(ctx context.Context, db *sql.DB, table string) (bool, error) {
	var count int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count)
	return count > 0, err
}

func countRows(ctx context.Context, db *sql.DB, table string) (int, error) {
	return countQueryErr(ctx, db, `SELECT COUNT(*) FROM `+quoteIdentifier(table))
}

func countQuery(ctx context.Context, db *sql.DB, query string, args ...any) int {
	count, err := countQueryErr(ctx, db, query, args...)
	if err != nil {
		return 0
	}
	return count
}

func countQueryErr(ctx context.Context, db *sql.DB, query string, args ...any) (int, error) {
	var count int
	if err := db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

// exportDeletePrepTable writes the full table as JSONL plus a Markdown digest.
// Both are produced from the backup, so the export always matches the snapshot.
func exportDeletePrepTable(ctx context.Context, db *sql.DB, table, exportDir string) (int, string, string, error) {
	if ok, err := tableExists(ctx, db, table); err != nil {
		return 0, "", "", err
	} else if !ok {
		return 0, "", "", nil
	}
	rows, err := db.QueryContext(ctx, `SELECT * FROM `+quoteIdentifier(table)+` ORDER BY rowid`)
	if err != nil {
		return 0, "", "", fmt.Errorf("read %s: %w", table, err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return 0, "", "", err
	}
	jsonlPath := filepath.Join(exportDir, table+".jsonl")
	markdownPath := filepath.Join(exportDir, table+".md")
	jsonl, err := os.OpenFile(jsonlPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, "", "", err
	}
	defer jsonl.Close()
	markdown, err := os.OpenFile(markdownPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, "", "", err
	}
	defer markdown.Close()
	jsonWriter := bufio.NewWriter(jsonl)
	markdownWriter := bufio.NewWriter(markdown)
	defer jsonWriter.Flush()
	defer markdownWriter.Flush()
	if _, err := fmt.Fprintf(markdownWriter, "# %s 可读导出\n\n来自数据库备份快照；每行一条记录。\n\n", table); err != nil {
		return 0, "", "", err
	}
	encoder := json.NewEncoder(jsonWriter)
	encoder.SetEscapeHTML(false)
	count := 0
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for index := range values {
			pointers[index] = &values[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			return 0, "", "", err
		}
		record := make(map[string]any, len(columns))
		ordered := make([][2]string, 0, len(columns))
		for index, column := range columns {
			value := normalizeSQLValue(values[index])
			record[column] = value
			ordered = append(ordered, [2]string{column, asText(value)})
		}
		if err := encoder.Encode(record); err != nil {
			return 0, "", "", err
		}
		if err := writeMarkdownRow(markdownWriter, count+1, ordered); err != nil {
			return 0, "", "", err
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, "", "", err
	}
	if err := jsonWriter.Flush(); err != nil {
		return 0, "", "", err
	}
	if err := markdownWriter.Flush(); err != nil {
		return 0, "", "", err
	}
	return count, jsonlPath, markdownPath, nil
}

func writeMarkdownRow(writer io.Writer, index int, ordered [][2]string) error {
	title := ""
	for _, pair := range ordered {
		switch pair[0] {
		case "title", "name":
			if strings.TrimSpace(pair[1]) != "" {
				title = strings.TrimSpace(pair[1])
			}
		}
		if title != "" {
			break
		}
	}
	if title == "" {
		for _, pair := range ordered {
			if pair[0] == "id" && pair[1] != "" {
				title = pair[1]
				break
			}
		}
	}
	if _, err := fmt.Fprintf(writer, "## %d. %s\n\n", index, title); err != nil {
		return err
	}
	longFields := map[string]bool{"body": true, "instructions": true, "proposed_json": true, "description": true, "evidence_json": true}
	for _, pair := range ordered {
		if longFields[pair[0]] && strings.TrimSpace(pair[1]) != "" {
			continue
		}
		if _, err := fmt.Fprintf(writer, "- %s: %s\n", pair[0], pair[1]); err != nil {
			return err
		}
	}
	for _, pair := range ordered {
		if !longFields[pair[0]] || strings.TrimSpace(pair[1]) == "" {
			continue
		}
		if _, err := fmt.Fprintf(writer, "\n### %s\n\n%s\n", pair[0], pair[1]); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(writer); err != nil {
		return err
	}
	return nil
}

func normalizeSQLValue(value any) any {
	switch typed := value.(type) {
	case []byte:
		return string(typed)
	case int64, float64, bool, string, nil:
		return typed
	default:
		return fmt.Sprintf("%v", typed)
	}
}

func asText(value any) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return fmt.Sprintf("%v", typed)
	}
}

func deletePrepProjectScope(ctx context.Context, db *sql.DB, vaultPath string, report *DeletePrepReport) ([]string, []UnboundProjectPlan, []string, error) {
	bound := map[string]bool{}
	manifest, err := ReadManifest(vaultPath)
	if err != nil {
		report.Warnings = append(report.Warnings, "无法读取 vault manifest："+err.Error())
	} else {
		report.VaultManifest = true
		for id := range manifest.Projects {
			bound[id] = true
		}
		for id := range manifest.Archives {
			bound[id] = true
		}
	}
	boundProjects := make([]string, 0, len(bound))
	for id := range bound {
		boundProjects = append(boundProjects, id)
	}
	sort.Strings(boundProjects)

	type projectRow struct {
		ID, Name, Type string
	}
	projects := []projectRow{}
	rows, err := db.QueryContext(ctx, `SELECT id,name,COALESCE(project_type,'') FROM projects ORDER BY id`)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read projects: %w", err)
	}
	for rows.Next() {
		var item projectRow
		if err := rows.Scan(&item.ID, &item.Name, &item.Type); err != nil {
			rows.Close()
			return nil, nil, nil, err
		}
		projects = append(projects, item)
	}
	if err := rows.Close(); err != nil {
		return nil, nil, nil, err
	}
	entryCounts := map[string]int{}
	entries, err := db.QueryContext(ctx, `SELECT project_id,COUNT(*) FROM knowledge_entries WHERE scope='project' AND project_id<>'' GROUP BY project_id`)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read project entry counts: %w", err)
	}
	for entries.Next() {
		var projectID string
		var count int
		if err := entries.Scan(&projectID, &count); err != nil {
			entries.Close()
			return nil, nil, nil, err
		}
		entryCounts[projectID] = count
	}
	if err := entries.Close(); err != nil {
		return nil, nil, nil, err
	}
	type libraryRow struct {
		ID, Name, Container string
	}
	libraries := map[string]libraryRow{}
	libraryRows, err := db.QueryContext(ctx, `SELECT id,name,container_project_id FROM knowledge_libraries`)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read knowledge libraries: %w", err)
	}
	for libraryRows.Next() {
		var item libraryRow
		if err := libraryRows.Scan(&item.ID, &item.Name, &item.Container); err != nil {
			libraryRows.Close()
			return nil, nil, nil, err
		}
		libraries[item.Container] = item
	}
	if err := libraryRows.Close(); err != nil {
		return nil, nil, nil, err
	}

	known := map[string]bool{}
	unbound := []UnboundProjectPlan{}
	for _, project := range projects {
		known[project.ID] = true
		if bound[project.ID] {
			continue
		}
		if project.Type == "channel" || project.Type == "system" {
			// Infrastructure projects have no vault space by design. They are
			// not unbound; if legacy rows still exist, report them as a warning
			// so deleting the old tables is not silent about them.
			if entryCounts[project.ID] > 0 {
				report.Warnings = append(report.Warnings, fmt.Sprintf(
					"基础设施项目 %s（%s）有 %d 条旧知识条目；它不拥有 vault 知识空间，未计入未绑定项目",
					project.ID, project.Type, entryCounts[project.ID]))
			}
			continue
		}
		item := UnboundProjectPlan{
			ProjectID: project.ID, Name: project.Name, ProjectType: project.Type,
			Entries: entryCounts[project.ID],
		}
		if library, ok := libraries[project.ID]; ok {
			item.LibraryID, item.LibraryName = library.ID, library.Name
		}
		unbound = append(unbound, item)
	}
	orphans := []string{}
	for projectID, count := range entryCounts {
		if !known[projectID] && count > 0 {
			orphans = append(orphans, projectID)
		}
	}
	sort.Strings(orphans)
	return boundProjects, unbound, orphans, nil
}

func deletePrepSkills(ctx context.Context, db *sql.DB, dataDir, backupPath string) (SkillDeletionPlan, error) {
	plan := SkillDeletionPlan{Decision: "pending-owner-confirmation", InDeleteScope: false}
	if ok, err := tableExists(ctx, db, "skills"); err != nil {
		return plan, err
	} else if !ok {
		return plan, nil
	}
	plan.Rows = countQuery(ctx, db, `SELECT COUNT(*) FROM skills`)
	plan.Global = countQuery(ctx, db, `SELECT COUNT(*) FROM skills WHERE scope='global'`)
	plan.Project = countQuery(ctx, db, `SELECT COUNT(*) FROM skills WHERE scope='project'`)
	plan.AutoAttach = countQuery(ctx, db, `SELECT COUNT(*) FROM skills WHERE auto_attach=1`)
	legacySkills := filepath.Join(dataDir, "knowledge", "skills")
	if info, err := os.Stat(legacySkills); err == nil && info.IsDir() {
		target := filepath.Join(backupPath, "knowledge", "skills")
		if err := copyDirectory(legacySkills, target); err != nil {
			return plan, fmt.Errorf("backup legacy skill packages: %w", err)
		}
		plan.BackupPath = target
	}
	plan.PackageFiles = countFilesNamed(legacySkills, "SKILL.md")
	return plan, nil
}

func countFilesNamed(root, name string) int {
	count := 0
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !entry.IsDir() && entry.Name() == name {
			count++
		}
		return nil
	})
	return count
}

func deletePrepBlockers(ctx context.Context, db *sql.DB) []string {
	blockers := []string{}
	for _, item := range []struct {
		table string
		label string
	}{
		{table: "channel_knowledge_policies", label: "渠道知识固定索引策略"},
		{table: "channel_knowledge_grants", label: "渠道知识授权"},
		{table: "channel_knowledge_records", label: "渠道知识问答记录"},
	} {
		if ok, err := tableExists(ctx, db, item.table); err != nil || !ok {
			continue
		}
		count := countQuery(ctx, db, `SELECT COUNT(*) FROM `+quoteIdentifier(item.table))
		if count > 0 {
			blockers = append(blockers,
				fmt.Sprintf("%d 条%s引用 knowledge_entries（ON DELETE RESTRICT）；需要 Owner 先决定保留、迁移或解除引用", count, item.label))
		}
	}
	return blockers
}

func writeDeletePrepMetadata(report DeletePrepReport, backupPath string) error {
	metadata := map[string]any{
		"mode":            report.Mode,
		"generated_at":    report.GeneratedAt,
		"database_path":   report.DatabasePath,
		"database_bytes":  report.DatabaseBytes,
		"database_sha256": report.DatabaseSHA256,
		"backup_database": report.BackupDatabase,
		"backup_bytes":    report.BackupBytes,
		"backup_sha256":   report.BackupSHA256,
		"backup_method":   report.BackupMethod,
		"integrity_check": report.BackupIntegrity,
		"delete_executed": false,
	}
	encoded, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	return os.WriteFile(filepath.Join(backupPath, "backup.json"), encoded, 0o600)
}

func writeDeletePrepPlanJSON(path string, report DeletePrepReport) error {
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		return err
	}
	return nil
}

func writeDeletePrepPlanMarkdown(path string, report DeletePrepReport) error {
	var builder strings.Builder
	builder.WriteString("# 旧库知识删除准备（dry-run）\n\n")
	builder.WriteString("本报告只做备份、导出和删除清单；**没有执行任何 DELETE**。\n\n")
	builder.WriteString(fmt.Sprintf("- 生成时间：%s\n", report.GeneratedAt))
	builder.WriteString(fmt.Sprintf("- 源数据库：`%s`\n", report.DatabasePath))
	builder.WriteString(fmt.Sprintf("- 源库大小/哈希：%d 字节 / `%s`\n", report.DatabaseBytes, report.DatabaseSHA256))
	builder.WriteString(fmt.Sprintf("- SQLite 备份：`%s`\n", report.BackupDatabase))
	builder.WriteString(fmt.Sprintf("- 备份大小/哈希：%d 字节 / `%s`\n", report.BackupBytes, report.BackupSHA256))
	builder.WriteString(fmt.Sprintf("- 备份完整性：%s\n", report.BackupIntegrity))
	builder.WriteString(fmt.Sprintf("- 可读导出：`%s`\n\n", report.ExportDir))

	builder.WriteString("## 删除范围内的表\n\n")
	builder.WriteString("| 顺序 | 表 | 行数（备份） | 行数（源库核对） | 说明 |\n")
	builder.WriteString("| --- | --- | ---: | ---: | --- |\n")
	for _, table := range report.Tables {
		if !table.InScope {
			continue
		}
		builder.WriteString(fmt.Sprintf("| %d | `%s` | %d | %d | %s |\n", table.DeleteOrder, table.Table, table.Rows, table.SourceRows, table.Note))
	}
	builder.WriteString(fmt.Sprintf("\n删除范围合计：**%d 行**。\n\n", report.ScopeRows))

	builder.WriteString("## 未绑定到 vault 的项目\n\n")
	if len(report.UnboundProjects) == 0 {
		builder.WriteString("无。\n\n")
	} else {
		builder.WriteString("这些项目的旧知识不在当前 vault 中，删除旧表会一并清掉。\n\n")
		builder.WriteString("| 项目 | 名称 | 类型 | 旧知识条数 | 知识库 |\n")
		builder.WriteString("| --- | --- | --- | ---: | --- |\n")
		for _, project := range report.UnboundProjects {
			library := ""
			if project.LibraryID != "" {
				library = project.LibraryName + " (`" + project.LibraryID + "`)"
			}
			builder.WriteString(fmt.Sprintf("| `%s` | %s | %s | %d | %s |\n", project.ProjectID, project.Name, project.ProjectType, project.Entries, library))
		}
		builder.WriteString(fmt.Sprintf("\n未绑定项目旧知识合计：**%d 条**。\n\n", report.UnboundEntries))
	}
	builder.WriteString(fmt.Sprintf("孤儿 project_id（projects 表中不存在）：%d 条，涉及 %s。\n\n",
		report.OrphanEntries, inlineCodeList(report.OrphanProjectIDs)))
	builder.WriteString(fmt.Sprintf("未建立项目绑定的 knowledge_libraries：%d 个。\n\n", report.UnboundLibraries))

	builder.WriteString("## 技能（单独列出，待确认）\n\n")
	builder.WriteString(fmt.Sprintf("- `skills` 表：%d 行（global %d、project %d、AutoAttach %d）\n",
		report.Skills.Rows, report.Skills.Global, report.Skills.Project, report.Skills.AutoAttach))
	builder.WriteString(fmt.Sprintf("- 技能包：%d 个 `SKILL.md`", report.Skills.PackageFiles))
	if report.Skills.BackupPath != "" {
		builder.WriteString(fmt.Sprintf("，已复制到 `%s`", report.Skills.BackupPath))
	}
	builder.WriteString("\n")
	builder.WriteString("- 是否删除：**待 Owner 确认**；本工具没有把 skills 放进删除范围。\n\n")

	if len(report.Blockers) > 0 {
		builder.WriteString("## 阻断项\n\n")
		for _, blocker := range report.Blockers {
			builder.WriteString("- " + blocker + "\n")
		}
		builder.WriteString("\n")
	}
	if len(report.Warnings) > 0 {
		builder.WriteString("## 警告\n\n")
		for _, warning := range report.Warnings {
			builder.WriteString("- " + warning + "\n")
		}
		builder.WriteString("\n")
	}
	builder.WriteString("## 等待项\n\n")
	for _, item := range report.WaitingFor {
		builder.WriteString("- " + item + "\n")
	}
	builder.WriteString("\n## 当前状态\n\n")
	builder.WriteString("- `delete_executed`: **false**\n")
	builder.WriteString("- 真正执行 DELETE 必须等 Owner 在看到本清单和备份后二次确认。\n")
	return os.WriteFile(path, []byte(builder.String()), 0o600)
}

func inlineCodeList(values []string) string {
	if len(values) == 0 {
		return "无"
	}
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, "`"+value+"`")
	}
	return strings.Join(quoted, "、")
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
