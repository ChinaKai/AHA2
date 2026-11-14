package knowledgevault

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Index is the rebuildable SQLite projection of the vault files. It is a
// separate database; the existing knowledge_entries table is never touched.
type Index struct {
	db   *sql.DB
	path string
}

const indexSchema = `
PRAGMA journal_mode = WAL;
CREATE TABLE IF NOT EXISTS meta (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS entries (
    id TEXT PRIMARY KEY,
    path TEXT NOT NULL UNIQUE,
    scope TEXT NOT NULL,
    project_id TEXT NOT NULL DEFAULT '',
    slug TEXT NOT NULL DEFAULT '',
    parent_id TEXT NOT NULL DEFAULT '',
    type TEXT NOT NULL,
    legacy_type TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    status TEXT NOT NULL,
    summary TEXT NOT NULL DEFAULT '',
    revision INTEGER NOT NULL DEFAULT 0,
    tags TEXT NOT NULL DEFAULT '',
    sort_order INTEGER NOT NULL DEFAULT 0,
    is_index INTEGER NOT NULL DEFAULT 0,
    archive INTEGER NOT NULL DEFAULT 0,
    created TEXT NOT NULL DEFAULT '',
    updated TEXT NOT NULL DEFAULT '',
    source_task TEXT NOT NULL DEFAULT '',
    source_turn TEXT NOT NULL DEFAULT '',
    confidence REAL NOT NULL DEFAULT 0,
    trust TEXT NOT NULL DEFAULT '',
    prefix TEXT NOT NULL DEFAULT '',
    content_hash TEXT NOT NULL DEFAULT '',
    warning TEXT NOT NULL DEFAULT '',
    frontmatter_json TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_entries_scope ON entries(scope, project_id, parent_id, sort_order);
CREATE INDEX IF NOT EXISTS idx_entries_updated ON entries(updated DESC, id DESC);
CREATE TABLE IF NOT EXISTS tags (
    entry_id TEXT NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
    tag TEXT NOT NULL,
    PRIMARY KEY(entry_id, tag)
);
CREATE INDEX IF NOT EXISTS idx_tags_tag ON tags(tag);
CREATE TABLE IF NOT EXISTS links (
    source_id TEXT NOT NULL,
    target TEXT NOT NULL,
    target_path TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL DEFAULT 'wikilink'
);
CREATE INDEX IF NOT EXISTS idx_links_source ON links(source_id);
CREATE INDEX IF NOT EXISTS idx_links_target ON links(target_path);
CREATE TABLE IF NOT EXISTS warnings (
    path TEXT PRIMARY KEY,
    message TEXT NOT NULL
);
`

func openIndexDatabase(path string) (*sql.DB, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return nil, fmt.Errorf("create index directory: %w", err)
	}
	dsn := SQLiteFileDSN(absolute, "_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_txlock=immediate")
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open knowledge index: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return db, nil
}

// OpenIndex opens (and creates if needed) the index database.
func OpenIndex(ctx context.Context, indexPath string) (*Index, error) {
	db, err := openIndexDatabase(indexPath)
	if err != nil {
		return nil, err
	}
	index := &Index{db: db, path: indexPath}
	if _, err := db.ExecContext(ctx, indexSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create knowledge index schema: %w", err)
	}
	if err := migrateIndexSchema(ctx, db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate knowledge index schema: %w", err)
	}
	return index, nil
}

// migrateIndexSchema adds columns introduced after the first index release.
// The index is a rebuildable projection, but deployed databases must keep
// opening until the next rebuild replaces them.
func migrateIndexSchema(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(entries)`)
	if err != nil {
		return err
	}
	columns := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull int
		var defaultValue any
		var primaryKey int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, item := range []struct {
		name string
		sql  string
	}{
		{"summary", `ALTER TABLE entries ADD COLUMN summary TEXT NOT NULL DEFAULT ''`},
		{"revision", `ALTER TABLE entries ADD COLUMN revision INTEGER NOT NULL DEFAULT 0`},
	} {
		if columns[item.name] {
			continue
		}
		if _, err := db.ExecContext(ctx, item.sql); err != nil {
			return err
		}
	}
	return nil
}

func (i *Index) Close() error {
	if i == nil || i.db == nil {
		return nil
	}
	return i.db.Close()
}

func (i *Index) Path() string { return i.path }

type IndexEntry struct {
	ID          string   `json:"id"`
	Path        string   `json:"path"`
	Scope       string   `json:"scope"`
	ProjectID   string   `json:"project_id,omitempty"`
	Slug        string   `json:"slug,omitempty"`
	ParentID    string   `json:"parent_id,omitempty"`
	Type        string   `json:"type"`
	LegacyType  string   `json:"legacy_type,omitempty"`
	Title       string   `json:"title"`
	Body        string   `json:"body"`
	Status      string   `json:"status"`
	Summary     string   `json:"summary,omitempty"`
	Revision    int      `json:"revision"`
	Tags        []string `json:"tags,omitempty"`
	SortOrder   int      `json:"sort_order"`
	IsIndex     bool     `json:"is_index"`
	Archive     bool     `json:"archive"`
	Created     string   `json:"created,omitempty"`
	Updated     string   `json:"updated,omitempty"`
	SourceTask  string   `json:"source_task,omitempty"`
	SourceTurn  string   `json:"source_turn,omitempty"`
	Confidence  float64  `json:"confidence,omitempty"`
	Trust       string   `json:"trust,omitempty"`
	Prefix      string   `json:"prefix,omitempty"`
	ContentHash string   `json:"content_hash,omitempty"`
	Warning     string   `json:"warning,omitempty"`
	Frontmatter string   `json:"frontmatter_json,omitempty"`
}

type Link struct {
	SourceID   string `json:"source_id"`
	Target     string `json:"target"`
	TargetPath string `json:"target_path,omitempty"`
	Kind       string `json:"kind"`
}

type RebuildReport struct {
	VaultPath string   `json:"vault_path"`
	IndexPath string   `json:"index_path"`
	Entries   int      `json:"entries"`
	Global    int      `json:"global"`
	Projects  int      `json:"projects"`
	Archives  int      `json:"archives"`
	Orphans   int      `json:"orphans"`
	Tags      int      `json:"tags"`
	Links     int      `json:"links"`
	Warnings  []string `json:"warnings,omitempty"`
	Skipped   int      `json:"skipped"`
	RebuiltAt string   `json:"rebuilt_at"`
}

type scannedRecord struct {
	entry    IndexEntry
	warnings []string
	links    []string
}

var wikilinkPattern = regexp.MustCompile(`\[\[([^\]\n]+)\]\]`)

var skippedDirectoryNames = map[string]bool{
	".trash": true, ".aha": true, ".git": true, ".obsidian": true, "skills": true,
}

// Rebuild drops the projection and reconstructs it from vault files. The
// operation is atomic from a reader's perspective: the delete and inserts run
// in one transaction.
func (i *Index) Rebuild(ctx context.Context, vaultPath string) (RebuildReport, error) {
	absoluteVault, err := filepath.Abs(vaultPath)
	if err != nil {
		return RebuildReport{}, err
	}
	info, err := os.Stat(absoluteVault)
	if err != nil || !info.IsDir() {
		return RebuildReport{}, fmt.Errorf("knowledge vault does not exist at %s", absoluteVault)
	}
	manifest, manifestErr := ReadManifest(absoluteVault)
	report := RebuildReport{VaultPath: absoluteVault, IndexPath: i.path, RebuiltAt: time.Now().UTC().Format(time.RFC3339)}
	if manifestErr != nil {
		if os.IsNotExist(manifestErr) {
			manifest = defaultManifest(absoluteVault, "", time.Now().UTC())
			if err := writeManifest(absoluteVault, manifest); err != nil {
				return report, fmt.Errorf("create vault manifest: %w", err)
			}
		} else {
			report.Warnings = append(report.Warnings, "manifest could not be read: "+firstLine(manifestErr.Error()))
		}
	}
	if manifestErr == nil || os.IsNotExist(manifestErr) {
		derived, deriveWarnings, deriveErr := deriveManifest(absoluteVault, manifest, time.Now().UTC())
		report.Warnings = append(report.Warnings, deriveWarnings...)
		if deriveErr != nil {
			return report, deriveErr
		}
		manifest = derived
	}
	reverseProjects := reverseSlugMap(manifest.Projects)
	reverseArchives := reverseSlugMap(manifest.Archives)

	records := []*scannedRecord{}
	byPath := map[string]*scannedRecord{}
	seenIDs := map[string]string{}
	vaultWarnings := []string{}
	walkErr := filepath.WalkDir(absoluteVault, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			vaultWarnings = append(vaultWarnings, fmt.Sprintf("%s: %v", filePath, walkErr))
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		relative, err := filepath.Rel(absoluteVault, filePath)
		if err != nil || relative == "." {
			return nil
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			if skippedDirectoryNames[strings.ToLower(entry.Name())] {
				return fs.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			report.Skipped++
			vaultWarnings = append(vaultWarnings, relative+": symbolic link skipped")
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
			return nil
		}
		if relative == readmeFileName {
			return nil
		}
		if !IsInsideRoot(absoluteVault, filePath) {
			report.Skipped++
			vaultWarnings = append(vaultWarnings, relative+": path escapes the vault root; skipped")
			return nil
		}
		raw, err := os.ReadFile(filePath)
		if err != nil {
			report.Skipped++
			vaultWarnings = append(vaultWarnings, relative+": "+firstLine(err.Error()))
			return nil
		}
		document := ParseDocument(string(raw))
		record := buildScannedRecord(relative, string(raw), document, manifest, reverseProjects, reverseArchives)
		for _, warning := range record.warnings {
			report.Warnings = append(report.Warnings, relative+": "+warning)
		}
		if existing, ok := seenIDs[record.entry.ID]; ok {
			message := fmt.Sprintf("duplicate id %q also used by %s; deterministic suffix applied", record.entry.ID, existing)
			record.entry.ID = record.entry.ID + "-" + shortID(relative)
			record.warnings = append(record.warnings, message)
			vaultWarnings = append(vaultWarnings, relative+": "+message)
		}
		seenIDs[record.entry.ID] = relative
		records = append(records, record)
		byPath[relative] = record
		return nil
	})
	if walkErr != nil {
		return report, walkErr
	}
	sort.Slice(records, func(left, right int) bool { return records[left].entry.Path < records[right].entry.Path })

	resolveParents(records, byPath, reverseProjects, reverseArchives, &report.Warnings)

	tx, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		return report, err
	}
	defer tx.Rollback()
	for _, table := range []string{"links", "tags", "entries", "warnings"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table); err != nil {
			return report, err
		}
	}
	for _, record := range records {
		entry := record.entry
		frontmatterJSON := record.entry.Frontmatter
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO entries(id,path,scope,project_id,slug,parent_id,type,legacy_type,title,body,status,summary,revision,tags,
				sort_order,is_index,archive,created,updated,source_task,source_turn,confidence,trust,prefix,content_hash,warning,frontmatter_json)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			entry.ID, entry.Path, entry.Scope, entry.ProjectID, entry.Slug, entry.ParentID,
			entry.Type, entry.LegacyType, entry.Title, entry.Body, entry.Status, entry.Summary, entry.Revision, strings.Join(entry.Tags, ","),
			entry.SortOrder, boolInt(entry.IsIndex), boolInt(entry.Archive), entry.Created, entry.Updated,
			entry.SourceTask, entry.SourceTurn, entry.Confidence, entry.Trust, entry.Prefix, entry.ContentHash,
			strings.Join(record.warnings, "; "), frontmatterJSON,
		); err != nil {
			return report, err
		}
		for _, tag := range entry.Tags {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO tags(entry_id,tag) VALUES(?,?)`, entry.ID, tag); err != nil {
				return report, err
			}
			report.Tags++
		}
		for _, target := range record.links {
			targetPath := resolveLinkTarget(target, byPath)
			if _, err := tx.ExecContext(ctx, `INSERT INTO links(source_id,target,target_path,kind) VALUES(?,?,?,?)`, entry.ID, target, targetPath, "wikilink"); err != nil {
				return report, err
			}
			report.Links++
		}
		if len(record.warnings) > 0 {
			if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO warnings(path,message) VALUES(?,?)`, entry.Path, strings.Join(record.warnings, "; ")); err != nil {
				return report, err
			}
		}
	}
	for index, warning := range vaultWarnings {
		pathKey := fmt.Sprintf(".vault-warning-%d", index)
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO warnings(path,message) VALUES(?,?)`, pathKey, warning); err != nil {
			return report, err
		}
	}
	metadata := map[string]string{
		"vault_path": absoluteVault,
		"rebuilt_at": report.RebuiltAt,
		"entries":    fmt.Sprint(len(records)),
	}
	for key, value := range metadata {
		if _, err := tx.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value); err != nil {
			return report, err
		}
	}
	if err := tx.Commit(); err != nil {
		return report, err
	}
	report.Entries = len(records)
	report.Warnings = append(report.Warnings, vaultWarnings...)
	report.Warnings = append(report.Warnings, maintainVaultIndexes(absoluteVault, records)...)
	report.Warnings = capWarnings(report.Warnings, 200)
	for _, record := range records {
		switch record.entry.Scope {
		case "global":
			report.Global++
		case "project":
			report.Projects++
		case "archive":
			report.Archives++
		case "orphan":
			report.Orphans++
		}
	}
	return report, nil
}

func buildScannedRecord(relative, raw string, document Document, manifest Manifest, reverseProjects, reverseArchives map[string]string) *scannedRecord {
	record := &scannedRecord{}
	frontmatter := document.Frontmatter
	scope, projectID, archive := scopeForPath(relative, manifest, reverseProjects, reverseArchives)
	if frontmatter.Scope == "archive" {
		scope, archive = "archive", true
	}
	if frontmatter.Scope == "global" {
		scope = "global"
	}
	if frontmatter.Scope == "orphan" {
		scope = "orphan"
		if projectID == "" {
			projectID = strings.TrimSpace(frontmatter.ProjectID)
		}
	}
	body := StripGeneratedIndex(document.Body)
	if document.Warning != "" {
		record.warnings = append(record.warnings, document.Warning)
		body = document.Raw
	}
	slug := strings.TrimSpace(frontmatter.Slug)
	if slug == "" {
		slug = inferredSlug(relative)
	}
	title := strings.TrimSpace(frontmatter.Title)
	if title == "" {
		title = FirstHeading(body)
	}
	if title == "" {
		title = humanizeSegment(slug)
	}
	canonical, legacy := NormalizeType(frontmatter.Type, relative)
	if strings.TrimSpace(frontmatter.LegacyType) != "" {
		legacy = strings.TrimSpace(frontmatter.LegacyType)
	}
	status := strings.ToLower(strings.TrimSpace(frontmatter.Status))
	if status == "" {
		status = "verified"
	}
	isIndex := isRootIndexPath(relative)
	if frontmatter.IsIndex != nil {
		isIndex = *frontmatter.IsIndex
	}
	sortOrder := 0
	if frontmatter.SortOrder != nil {
		sortOrder = *frontmatter.SortOrder
	}
	confidence := 0.0
	if frontmatter.Confidence != nil {
		confidence = *frontmatter.Confidence
	}
	id := strings.TrimSpace(frontmatter.ID)
	if id == "" {
		sum := sha256.Sum256([]byte(relative))
		id = "vault_" + hex.EncodeToString(sum[:])[:16]
	}
	if frontmatter.Archive != nil && *frontmatter.Archive {
		archive = true
	}
	if scope == "archive" {
		archive = true
	}
	if scopeForPathIsUnknown(relative) {
		scope = "unknown"
		record.warnings = append(record.warnings, "path is outside global/projects/archive; indexed as unknown scope")
	}
	if (scope == "project" || scope == "archive") && projectID == "" {
		record.warnings = append(record.warnings, "project slug is missing from the manifest")
	}
	revision := frontmatter.Revision
	if revision <= 0 {
		revision = 1
	}
	tags := append([]string{}, frontmatter.Tags...)
	sort.Strings(tags)
	record.entry = IndexEntry{
		ID: id, Path: relative, Scope: scope, ProjectID: projectID, Slug: slug,
		ParentID: strings.TrimSpace(frontmatter.Parent), Type: canonical, LegacyType: legacy,
		Title: title, Body: body, Status: status, Summary: strings.TrimSpace(frontmatter.Summary),
		Revision: revision, Tags: tags, SortOrder: sortOrder,
		IsIndex: isIndex, Archive: archive, Created: frontmatter.Created, Updated: frontmatter.Updated,
		SourceTask: frontmatter.SourceTask, SourceTurn: frontmatter.SourceTurn,
		Confidence: confidence, Trust: frontmatter.Trust,
		ContentHash: fmt.Sprintf("%x", sha256.Sum256([]byte(raw))),
		Prefix:      prefixFromPath(relative),
	}
	record.entry.Frontmatter = encodeExtra(frontmatter.Extra)
	record.links = extractLinks(body)
	return record
}

func scopeForPath(relative string, manifest Manifest, reverseProjects, reverseArchives map[string]string) (string, string, bool) {
	segments := strings.Split(relative, "/")
	if len(segments) == 0 {
		return "unknown", "", false
	}
	switch segments[0] {
	case "global":
		return "global", "", false
	case "orphans":
		return "orphan", "", false
	case "projects", "archive":
		if len(segments) < 2 {
			return "unknown", "", false
		}
		slug := segments[1]
		if segments[0] == "archive" {
			return "archive", reverseArchives[slug], true
		}
		return "project", reverseProjects[slug], false
	default:
		return "unknown", "", false
	}
}

func scopeForPathIsUnknown(relative string) bool {
	first := strings.SplitN(relative, "/", 2)[0]
	return first != "global" && first != "projects" && first != "archive" && first != "orphans"
}

func reverseSlugMap(values map[string]string) map[string]string {
	result := map[string]string{}
	for id, slug := range values {
		if slug != "" {
			result[slug] = id
		}
	}
	return result
}

func inferredSlug(relative string) string {
	base := path.Base(relative)
	stem := strings.TrimSuffix(base, path.Ext(base))
	if stem == "index" {
		directory := path.Base(path.Dir(relative))
		if directory == "." || directory == "/" || directory == "global" || directory == "projects" || directory == "archive" {
			return "index"
		}
		return SafeSegment(directory, "index")
	}
	return SafeSegment(stem, "doc")
}

func humanizeSegment(slug string) string {
	if slug == "" {
		return "Untitled"
	}
	parts := strings.Split(slug, "-")
	for index, part := range parts {
		runes := []rune(part)
		if len(runes) == 0 {
			continue
		}
		parts[index] = strings.ToUpper(string(runes[0])) + string(runes[1:])
	}
	return strings.Join(parts, " ")
}

func isRootIndexPath(relative string) bool {
	segments := strings.Split(relative, "/")
	if len(segments) == 2 && segments[0] == "global" && segments[1] == "index.md" {
		return true
	}
	return len(segments) == 3 && (segments[0] == "projects" || segments[0] == "archive") && segments[2] == "index.md"
}

func prefixFromPath(relative string) string {
	segments := strings.Split(relative, "/")
	if len(segments) >= 2 && (segments[0] == "projects" || segments[0] == "archive") {
		return segments[0] + "/" + segments[1]
	}
	return segments[0]
}

func extractLinks(body string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, match := range wikilinkPattern.FindAllStringSubmatch(body, -1) {
		target := strings.TrimSpace(match[1])
		if index := strings.IndexByte(target, '|'); index >= 0 {
			target = strings.TrimSpace(target[:index])
		}
		if index := strings.IndexByte(target, '#'); index >= 0 {
			target = strings.TrimSpace(target[:index])
		}
		target = strings.TrimPrefix(target, "./")
		if target == "" || strings.Contains(target, "://") || seen[target] {
			continue
		}
		seen[target] = true
		result = append(result, target)
	}
	sort.Strings(result)
	return result
}

func resolveLinkTarget(target string, byPath map[string]*scannedRecord) string {
	candidate := target
	if !strings.HasSuffix(strings.ToLower(candidate), ".md") {
		candidate += ".md"
	}
	if _, ok := byPath[candidate]; ok {
		return candidate
	}
	lower := strings.ToLower(candidate)
	for relative := range byPath {
		if strings.ToLower(relative) == lower {
			return relative
		}
	}
	base := strings.ToLower(path.Base(candidate))
	var matches []string
	for relative := range byPath {
		if strings.ToLower(path.Base(relative)) == base {
			matches = append(matches, relative)
		}
	}
	if len(matches) == 1 {
		return matches[0]
	}
	return ""
}

func resolveParents(records []*scannedRecord, byPath map[string]*scannedRecord, reverseProjects, reverseArchives map[string]string, warnings *[]string) {
	directoryOwners := map[string]string{}
	for _, record := range records {
		if path.Base(record.entry.Path) != "index.md" {
			continue
		}
		directory := path.Dir(record.entry.Path)
		directoryOwners[directory] = record.entry.ID
	}
	ids := map[string]bool{}
	for _, record := range records {
		ids[record.entry.ID] = true
	}
	for _, record := range records {
		relative := record.entry.Path
		frontmatterParent := record.entry.ParentID
		if frontmatterParent != "" && ids[frontmatterParent] {
			continue
		}
		derived := ""
		if isRootIndexPath(relative) {
			derived = ""
		} else {
			directory := path.Dir(relative)
			base := path.Base(relative)
			if base == "index.md" {
				parentDirectory := path.Dir(directory)
				derived = directoryOwners[parentDirectory]
			} else if path.Base(directory) == "inbox" {
				derived = directoryOwners[path.Dir(directory)]
			} else {
				derived = directoryOwners[directory]
			}
			if derived == "" {
				derived = directoryOwners[prefixFromPath(relative)]
			}
		}
		if derived == "" && frontmatterParent != "" {
			*warnings = append(*warnings, fmt.Sprintf("%s references missing parent %q; stored without a parent", relative, frontmatterParent))
		}
		record.entry.ParentID = derived
	}
}

type SearchOptions struct {
	Scope           string
	ProjectID       string
	Type            string
	Status          string
	Tag             string
	Limit           int
	IncludeArchives bool
}

type SearchResult struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Path      string   `json:"path"`
	Type      string   `json:"type"`
	Scope     string   `json:"scope"`
	ProjectID string   `json:"project_id,omitempty"`
	Status    string   `json:"status"`
	Summary   string   `json:"summary,omitempty"`
	Revision  int      `json:"revision,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	Snippet   string   `json:"snippet,omitempty"`
	Updated   string   `json:"updated,omitempty"`
	Archive   bool     `json:"archive,omitempty"`
}

// Search is the Phase 1 LIKE/instr baseline. It deliberately does not depend
// on FTS5.
func (i *Index) Search(ctx context.Context, query string, options SearchOptions) ([]SearchResult, error) {
	limit := options.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	query = strings.TrimSpace(query)
	sqlQuery := `SELECT id,title,path,type,scope,project_id,status,summary,revision,tags,updated,archive,body FROM entries WHERE 1=1`
	args := []any{}
	if options.Scope != "" {
		sqlQuery += ` AND scope=?`
		args = append(args, options.Scope)
	}
	if options.ProjectID != "" {
		sqlQuery += ` AND project_id=?`
		args = append(args, options.ProjectID)
	}
	if options.Type != "" {
		sqlQuery += ` AND type=?`
		args = append(args, options.Type)
	}
	if options.Status != "" {
		sqlQuery += ` AND status=?`
		args = append(args, options.Status)
	}
	if options.Tag != "" {
		// instr keeps the tag comparison literal: LIKE would treat % and _ in
		// a tag name as wildcards and make an "exact" filter fuzzy.
		sqlQuery += ` AND instr(','||tags||',', ?)>0`
		args = append(args, ","+options.Tag+",")
	}
	if !options.IncludeArchives {
		sqlQuery += ` AND archive=0`
	}
	if query != "" {
		lower := strings.ToLower(query)
		sqlQuery += ` AND (instr(lower(title),?)>0 OR instr(lower(body),?)>0 OR instr(lower(path),?)>0 OR instr(lower(tags),?)>0)`
		args = append(args, lower, lower, lower, lower)
		sqlQuery += ` ORDER BY CASE WHEN instr(lower(title),?)>0 THEN 0 ELSE 1 END, updated DESC, id LIMIT ?`
		args = append(args, lower, limit)
	} else {
		sqlQuery += ` ORDER BY updated DESC, id LIMIT ?`
		args = append(args, limit)
	}
	rows, err := i.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := []SearchResult{}
	for rows.Next() {
		var item SearchResult
		var tags string
		var body string
		var archive int
		if err := rows.Scan(&item.ID, &item.Title, &item.Path, &item.Type, &item.Scope, &item.ProjectID,
			&item.Status, &item.Summary, &item.Revision, &tags, &item.Updated, &archive, &body); err != nil {
			return nil, err
		}
		item.Archive = archive != 0
		if tags != "" {
			item.Tags = strings.Split(tags, ",")
		}
		item.Snippet = searchSnippet(body, query, 160)
		results = append(results, item)
	}
	return results, rows.Err()
}

func searchSnippet(body, query string, limit int) string {
	if strings.TrimSpace(body) == "" {
		return ""
	}
	lowerBody := strings.ToLower(body)
	lowerQuery := strings.ToLower(strings.TrimSpace(query))
	position := -1
	if lowerQuery != "" {
		position = strings.Index(lowerBody, lowerQuery)
	}
	if position < 0 {
		return IndexSummary(body, limit)
	}
	start := 0
	for index := range body {
		if index >= position {
			break
		}
		start++
	}
	begin := start - 50
	if begin < 0 {
		begin = 0
	}
	end := start + limit
	contentRunes := []rune(body)
	if end > len(contentRunes) {
		end = len(contentRunes)
	}
	if begin > len(contentRunes) {
		begin = len(contentRunes)
	}
	snippet := strings.TrimSpace(string(contentRunes[begin:end]))
	if begin > 0 {
		snippet = "…" + snippet
	}
	if end < len(contentRunes) {
		snippet += "…"
	}
	return snippet
}

type IndexStatus struct {
	Path      string `json:"path"`
	Entries   int    `json:"entries"`
	Global    int    `json:"global"`
	Projects  int    `json:"projects"`
	Archives  int    `json:"archives"`
	Orphans   int    `json:"orphans"`
	Tags      int    `json:"tags"`
	Links     int    `json:"links"`
	Warnings  int    `json:"warnings"`
	RebuiltAt string `json:"rebuilt_at,omitempty"`
}

// VisibleEntryCount counts documents a reader actually sees in the knowledge
// tree: inbox files and reserved top-level paths (skills, .trash, ...) are
// excluded, matching the reader's tree and library counts.
func (i *Index) VisibleEntryCount(ctx context.Context) (int, error) {
	rows, err := i.db.QueryContext(ctx, `SELECT path FROM entries`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var relative string
		if err := rows.Scan(&relative); err != nil {
			return 0, err
		}
		if isInboxPath(relative) || isReservedVaultPath(relative) {
			continue
		}
		count++
	}
	return count, rows.Err()
}

func (i *Index) Status(ctx context.Context) (IndexStatus, error) {
	status := IndexStatus{Path: i.path}
	counts := []struct {
		target *int
		query  string
	}{
		{&status.Entries, `SELECT COUNT(*) FROM entries`},
		{&status.Global, `SELECT COUNT(*) FROM entries WHERE scope='global'`},
		{&status.Projects, `SELECT COUNT(*) FROM entries WHERE scope='project'`},
		{&status.Archives, `SELECT COUNT(*) FROM entries WHERE scope='archive'`},
		{&status.Orphans, `SELECT COUNT(*) FROM entries WHERE scope='orphan'`},
		{&status.Tags, `SELECT COUNT(*) FROM tags`},
		{&status.Links, `SELECT COUNT(*) FROM links`},
		{&status.Warnings, `SELECT COUNT(*) FROM warnings`},
	}
	for _, item := range counts {
		if err := i.db.QueryRowContext(ctx, item.query).Scan(item.target); err != nil {
			return status, err
		}
	}
	_ = i.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key='rebuilt_at'`).Scan(&status.RebuiltAt)
	return status, nil
}

// Entries lists indexed entries for a scope. Empty scope returns all entries.
func (i *Index) Entries(ctx context.Context, scope, projectID string) ([]IndexEntry, error) {
	query := `SELECT id,path,scope,project_id,slug,parent_id,type,legacy_type,title,body,status,summary,revision,tags,
		sort_order,is_index,archive,created,updated,source_task,source_turn,confidence,trust,prefix,content_hash,warning,frontmatter_json
		FROM entries WHERE 1=1`
	args := []any{}
	if scope != "" {
		query += ` AND scope=?`
		args = append(args, scope)
	}
	if projectID != "" {
		query += ` AND project_id=?`
		args = append(args, projectID)
	}
	query += ` ORDER BY prefix,path`
	rows, err := i.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []IndexEntry{}
	for rows.Next() {
		var item IndexEntry
		var tags string
		if err := rows.Scan(&item.ID, &item.Path, &item.Scope, &item.ProjectID, &item.Slug, &item.ParentID,
			&item.Type, &item.LegacyType, &item.Title, &item.Body, &item.Status, &item.Summary,
			&item.Revision, &tags,
			&item.SortOrder, &item.IsIndex, &item.Archive, &item.Created, &item.Updated,
			&item.SourceTask, &item.SourceTurn, &item.Confidence, &item.Trust, &item.Prefix,
			&item.ContentHash, &item.Warning, &item.Frontmatter); err != nil {
			return nil, err
		}
		if tags != "" {
			item.Tags = strings.Split(tags, ",")
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func capWarnings(values []string, limit int) []string {
	if len(values) <= limit {
		return values
	}
	result := append([]string{}, values[:limit]...)
	result = append(result, fmt.Sprintf("… %d more warnings omitted", len(values)-limit))
	return result
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
