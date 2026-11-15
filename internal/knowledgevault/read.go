package knowledgevault

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// ErrReaderNotReady is returned when a read API is called before the vault or
// its rebuildable index exists. The Web UI turns it into an explicit empty
// state instead of a blank page.
var ErrReaderNotReady = errors.New("knowledge vault index is not ready")

// ErrDocumentNotFound reports an id or path that is not present in the index.
var ErrDocumentNotFound = errors.New("knowledge vault document not found")

// maxSkillPackageFileSize mirrors the legacy loader cap so switching sources
// never changes which files are materialized.
const maxSkillPackageFileSize = 2 << 20

// Reader is the single read-only entry point for the Phase 2 Web API. It never
// writes the vault and only writes the index to apply schema migrations before
// the read-only connection is opened.
type Reader struct {
	config     Config
	manifest   Manifest
	index      *Index
	vaultReady bool
	indexReady bool
}

// OpenReader opens the existing vault and index if both are present. A missing
// vault or index is not an error: the reader still reports the feature switch
// and the UI shows the "not exported yet" state.
func OpenReader(ctx context.Context, config Config) (*Reader, error) {
	normalized, err := config.Normalized()
	if err != nil {
		return nil, err
	}
	reader := &Reader{config: normalized}
	if info, statErr := os.Stat(normalized.VaultPath); statErr == nil && info.IsDir() {
		reader.vaultReady = true
		manifest, manifestErr := ReadManifest(normalized.VaultPath)
		if manifestErr == nil {
			reader.manifest = manifest
		} else if !os.IsNotExist(manifestErr) {
			return nil, fmt.Errorf("read knowledge vault manifest: %w", manifestErr)
		}
	}
	if info, statErr := os.Stat(normalized.IndexPath); statErr == nil && !info.IsDir() {
		// A deployed index may predate columns such as summary/revision. The
		// read-only handle cannot migrate, so upgrade the schema first; if that
		// fails we still try to open and let the caller report the read error
		// instead of silently falling back to legacy knowledge.
		if migrateErr := migrateIndexSchemaFile(ctx, normalized.IndexPath); migrateErr != nil {
			log.Printf("knowledge vault index schema migration failed for %s; reads may fail and fall back to legacy knowledge: %v", normalized.IndexPath, migrateErr)
		}
		index, openErr := openReadOnlyIndex(ctx, normalized.IndexPath)
		if openErr != nil {
			return nil, fmt.Errorf("open knowledge vault index: %w", openErr)
		}
		reader.index = index
		reader.indexReady = true
	}
	return reader, nil
}

func openReadOnlyIndex(ctx context.Context, indexPath string) (*Index, error) {
	absolute, err := filepath.Abs(indexPath)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", SQLiteFileDSN(absolute, "mode=ro&_pragma=busy_timeout(5000)"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return &Index{db: db, path: absolute}, nil
}

func (r *Reader) Close() error {
	if r == nil || r.index == nil {
		return nil
	}
	return r.index.Close()
}

func (r *Reader) Config() Config     { return r.config }
func (r *Reader) VaultReady() bool   { return r.vaultReady }
func (r *Reader) IndexReady() bool   { return r.indexReady }
func (r *Reader) Manifest() Manifest { return r.manifest }

// Guide returns the canonical knowledge-base guide. It reads the file directly
// so the guide remains available even when the index is missing.
func (r *Reader) Guide() (Guide, error) {
	if !r.vaultReady {
		return Guide{}, ErrGuideNotFound
	}
	return ReadGuide(r.config.VaultPath)
}

// Entries returns raw index rows for one scope/project. Callers are responsible
// for filtering status, archives and reserved paths for their own surface.
func (r *Reader) Entries(ctx context.Context, scope, projectID string) ([]IndexEntry, error) {
	if !r.indexReady {
		return nil, ErrReaderNotReady
	}
	return r.index.Entries(ctx, scope, projectID)
}

// SkillPackageFile is one text file inside a vault skill package.
type SkillPackageFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// ErrSkillPackageNotFound reports a missing SKILL.md so the caller can fall
// back to the legacy flat skill directory.
var ErrSkillPackageNotFound = errors.New("knowledge vault skill package not found")

// SkillFiles reads a vault skill package relative to the vault root. Binary
// and oversized files are skipped; the legacy loader applies the same limits.
func (r *Reader) SkillFiles(relative string) ([]SkillPackageFile, error) {
	if !r.vaultReady {
		return nil, ErrSkillPackageNotFound
	}
	relative = filepath.ToSlash(filepath.Clean(strings.TrimSpace(relative)))
	if relative == "" || relative == "." || strings.HasPrefix(relative, "../") {
		return nil, ErrSkillPackageNotFound
	}
	directory := filepath.Join(r.config.VaultPath, filepath.FromSlash(relative))
	if !IsInsideRoot(r.config.VaultPath, directory) {
		return nil, ErrSkillPackageNotFound
	}
	if _, err := os.Stat(filepath.Join(directory, "SKILL.md")); err != nil {
		return nil, ErrSkillPackageNotFound
	}
	files := []SkillPackageFile{}
	err := filepath.WalkDir(directory, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil || info.Size() > maxSkillPackageFileSize {
			return nil
		}
		relativeFile, relErr := filepath.Rel(directory, filePath)
		if relErr != nil || strings.HasPrefix(relativeFile, "..") {
			return nil
		}
		data, readErr := os.ReadFile(filePath)
		if readErr != nil {
			return nil
		}
		if !utf8.Valid(data) || strings.ContainsRune(string(data), '\x00') {
			return nil
		}
		files = append(files, SkillPackageFile{Path: filepath.ToSlash(relativeFile), Content: string(data)})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(left, right int) bool { return files[left].Path < files[right].Path })
	return files, nil
}

// Library is one selectable source in the top-level library picker.
type Library struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	ProjectID string `json:"project_id,omitempty"`
	Count     int    `json:"count"`
}

type LibraryList struct {
	Enabled       bool      `json:"enabled"`
	VaultReady    bool      `json:"vault_ready"`
	IndexReady    bool      `json:"index_ready"`
	VaultName     string    `json:"vault_name,omitempty"`
	ObsidianVault string    `json:"obsidian_vault,omitempty"`
	ObsidianReady bool      `json:"obsidian_ready"`
	VaultPath     string    `json:"vault_path,omitempty"`
	Libraries     []Library `json:"libraries"`
	Projects      []Library `json:"projects"`
	Archived      []Library `json:"archives,omitempty"`
	Orphans       int       `json:"orphans,omitempty"`
	RebuiltAt     string    `json:"rebuilt_at,omitempty"`
	Warning       string    `json:"warning,omitempty"`
}

// Libraries lists the global library, every exported project, archive
// libraries, and an isolated orphan library when one exists. Counts exclude
// inbox documents because the knowledge tree never shows them.
func (r *Reader) Libraries(ctx context.Context) LibraryList {
	result := LibraryList{
		Enabled: true, VaultReady: r.vaultReady, IndexReady: r.indexReady,
		VaultName: r.manifest.VaultName, ObsidianVault: r.manifest.ObsidianVault,
		ObsidianReady: r.obsidianRegistered(),
		VaultPath:     r.config.VaultPath, Libraries: []Library{}, Projects: []Library{},
	}
	if !r.vaultReady {
		result.Warning = "vault_missing"
		return result
	}
	if !r.indexReady {
		result.Warning = "index_missing"
		// The manifest still gives the picker its project names, but every
		// count stays zero until the index is rebuilt.
		result.Libraries = append(result.Libraries, Library{ID: "global", Kind: "global", Name: "全局知识"})
		result.Projects = r.manifestProjects()
		result.Libraries = append(result.Libraries, result.Projects...)
		return result
	}

	entries, err := r.index.Entries(ctx, "", "")
	if err != nil {
		result.Warning = "index_read_failed"
		return result
	}
	counts := map[string]int{}
	orphans := 0
	for _, entry := range entries {
		if isInboxPath(entry.Path) || isReservedVaultPath(entry.Path) {
			continue
		}
		switch entry.Scope {
		case "global":
			counts["global"]++
		case "project":
			counts["project:"+entry.ProjectID]++
		case "archive":
			counts["archive:"+entry.ProjectID]++
		case "orphan":
			orphans++
		}
	}
	result.Libraries = append(result.Libraries, Library{ID: "global", Kind: "global", Name: "全局知识", Count: counts["global"]})
	result.Projects = r.manifestProjects()
	for index := range result.Projects {
		result.Projects[index].Count = counts[result.Projects[index].ID]
		result.Libraries = append(result.Libraries, result.Projects[index])
	}
	result.Archived = r.manifestArchives()
	for index := range result.Archived {
		result.Archived[index].Count = counts[result.Archived[index].ID]
		result.Libraries = append(result.Libraries, result.Archived[index])
	}
	if orphans > 0 {
		result.Orphans = orphans
		result.Libraries = append(result.Libraries, Library{ID: "orphan", Kind: "orphan", Name: "未归类", Count: orphans})
	}
	if status, statusErr := r.index.Status(ctx); statusErr == nil {
		result.RebuiltAt = status.RebuiltAt
	}
	return result
}

func (r *Reader) manifestProjects() []Library {
	ids := make([]string, 0, len(r.manifest.Projects))
	for id := range r.manifest.Projects {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(left, right int) bool {
		leftName := r.projectName(ids[left])
		rightName := r.projectName(ids[right])
		if leftName != rightName {
			return leftName < rightName
		}
		return ids[left] < ids[right]
	})
	result := make([]Library, 0, len(ids))
	for _, id := range ids {
		result = append(result, Library{ID: "project:" + id, Kind: "project", Name: r.projectName(id), ProjectID: id})
	}
	return result
}

func (r *Reader) manifestArchives() []Library {
	ids := make([]string, 0, len(r.manifest.Archives))
	for id := range r.manifest.Archives {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(left, right int) bool {
		leftName, rightName := r.archiveName(ids[left]), r.archiveName(ids[right])
		if leftName != rightName {
			return leftName < rightName
		}
		return ids[left] < ids[right]
	})
	result := make([]Library, 0, len(ids))
	for _, id := range ids {
		result = append(result, Library{ID: "archive:" + id, Kind: "archive", Name: r.archiveName(id), ProjectID: id})
	}
	return result
}

func (r *Reader) projectName(id string) string {
	if name := strings.TrimSpace(r.manifest.ProjectNames[id]); name != "" {
		return name
	}
	if slug := strings.TrimSpace(r.manifest.Projects[id]); slug != "" {
		return slug
	}
	return id
}

func (r *Reader) archiveName(id string) string {
	if name := strings.TrimSpace(r.manifest.ArchiveNames[id]); name != "" {
		return name
	}
	if slug := strings.TrimSpace(r.manifest.Archives[id]); slug != "" {
		return slug
	}
	return id
}

func (r *Reader) obsidianRegistered() bool {
	if !r.vaultReady {
		return false
	}
	info, err := os.Stat(filepath.Join(r.config.VaultPath, ".obsidian"))
	return err == nil && info.IsDir()
}

// TreeNode is a nested knowledge tree node. Folder nodes are rendered as real
// disclosure buttons by the Web UI; no synthetic ARIA tree is involved.
type TreeNode struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Path      string     `json:"path"`
	Type      string     `json:"type"`
	Status    string     `json:"status"`
	Scope     string     `json:"scope"`
	ProjectID string     `json:"project_id,omitempty"`
	Updated   string     `json:"updated,omitempty"`
	IsIndex   bool       `json:"is_index"`
	System    bool       `json:"system"`
	Children  []TreeNode `json:"children,omitempty"`
}

type TreeResult struct {
	Enabled    bool       `json:"enabled"`
	IndexReady bool       `json:"index_ready"`
	Library    string     `json:"library"`
	ProjectID  string     `json:"project_id,omitempty"`
	Count      int        `json:"count"`
	Nodes      []TreeNode `json:"nodes"`
	Warning    string     `json:"warning,omitempty"`
}

// Tree returns the folder/theme tree for one library. Skills and trash
// documents are excluded by design because they have their own read-only pages.
// Inbox documents are kept: they live in the vault's real inbox/ directory and
// the Web tree shows that directory alongside the theme folders.
func (r *Reader) Tree(ctx context.Context, library, projectID string) (TreeResult, error) {
	result := TreeResult{Enabled: true, IndexReady: r.indexReady, Library: library, ProjectID: projectID, Nodes: []TreeNode{}}
	scope, scopedProjectID, libraryErr := r.libraryScope(library, projectID)
	if libraryErr != nil {
		return result, libraryErr
	}
	result.ProjectID = scopedProjectID
	if !r.indexReady {
		result.Warning = "index_missing"
		return result, nil
	}
	entries, err := r.index.Entries(ctx, scope, scopedProjectID)
	if err != nil {
		return result, err
	}
	filtered := make([]IndexEntry, 0, len(entries))
	for _, entry := range entries {
		if isReservedVaultPath(entry.Path) {
			continue
		}
		filtered = append(filtered, entry)
	}
	result.Count = len(filtered)
	result.Nodes = buildReaderTree(filtered)
	return result, nil
}

// Doc is the full read-only document payload, including the links needed to
// open the same file in VSCode or Obsidian.
type Doc struct {
	ID            string        `json:"id"`
	Title         string        `json:"title"`
	Path          string        `json:"path"`
	RelativePath  string        `json:"relative_path"`
	AbsolutePath  string        `json:"absolute_path"`
	Scope         string        `json:"scope"`
	ProjectID     string        `json:"project_id,omitempty"`
	LibraryID     string        `json:"library_id"`
	LibraryName   string        `json:"library_name"`
	Type          string        `json:"type"`
	LegacyType    string        `json:"legacy_type,omitempty"`
	Status        string        `json:"status"`
	Body          string        `json:"body"`
	Tags          []string      `json:"tags,omitempty"`
	Updated       string        `json:"updated,omitempty"`
	Created       string        `json:"created,omitempty"`
	ModifiedAt    string        `json:"modified_at,omitempty"`
	IsIndex       bool          `json:"is_index"`
	System        bool          `json:"system"`
	Warning       string        `json:"warning,omitempty"`
	Outline       []OutlineItem `json:"outline"`
	Backlinks     []Backlink    `json:"backlinks"`
	ObsidianVault string        `json:"obsidian_vault,omitempty"`
	ObsidianReady bool          `json:"obsidian_ready"`
	ObsidianFile  string        `json:"obsidian_file,omitempty"`
	VaultPath     string        `json:"vault_path,omitempty"`
}

type OutlineItem struct {
	Level int    `json:"level"`
	Text  string `json:"text"`
	Slug  string `json:"slug"`
}

type Backlink struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Path  string `json:"path"`
}

// Document resolves an id (or, when id is empty, an explicit vault-relative
// path) and returns the body plus outline and backlinks.
func (r *Reader) Document(ctx context.Context, id, relativePath string) (Doc, error) {
	if !r.indexReady {
		return Doc{}, ErrReaderNotReady
	}
	var entry IndexEntry
	var err error
	if strings.TrimSpace(id) != "" {
		entry, err = r.entryByID(ctx, strings.TrimSpace(id))
	} else {
		entry, err = r.entryByPath(ctx, strings.TrimSpace(relativePath))
	}
	if errors.Is(err, sql.ErrNoRows) {
		return Doc{}, ErrDocumentNotFound
	}
	if err != nil {
		return Doc{}, err
	}
	if err := ValidateRelativePath(entry.Path); err != nil {
		return Doc{}, ErrDocumentNotFound
	}
	absolute := filepath.Join(r.config.VaultPath, filepath.FromSlash(entry.Path))
	if !IsInsideRoot(r.config.VaultPath, absolute) {
		return Doc{}, ErrDocumentNotFound
	}
	libraryID, libraryName := r.libraryForEntry(entry)
	modified := ""
	if info, statErr := os.Stat(absolute); statErr == nil {
		modified = info.ModTime().UTC().Format(time.RFC3339Nano)
	}
	obsidianFile := strings.TrimSuffix(entry.Path, path.Ext(entry.Path))
	doc := Doc{
		ID: entry.ID, Title: entry.Title, Path: entry.Path,
		RelativePath: entry.Path, AbsolutePath: absolute,
		Scope: entry.Scope, ProjectID: entry.ProjectID,
		LibraryID: libraryID, LibraryName: libraryName,
		Type: entry.Type, LegacyType: entry.LegacyType, Status: entry.Status,
		Body: entry.Body, Tags: append([]string{}, entry.Tags...),
		Updated: entry.Updated, Created: entry.Created, ModifiedAt: modified,
		IsIndex: entry.IsIndex, System: isSystemEntry(entry), Warning: entry.Warning,
		Outline: outlineItems(entry.Body), Backlinks: []Backlink{},
		ObsidianVault: r.manifest.ObsidianVault, ObsidianReady: r.obsidianRegistered(),
		ObsidianFile: obsidianFile, VaultPath: r.config.VaultPath,
	}
	backlinks, backlinkErr := r.backlinks(ctx, entry)
	if backlinkErr != nil {
		return Doc{}, backlinkErr
	}
	doc.Backlinks = backlinks
	return doc, nil
}

// Diff resolves an indexed document id and returns its latest git change. The
// vault-relative path comes from the index row, never from the HTTP request,
// and is re-validated before git sees it.
func (r *Reader) Diff(ctx context.Context, id string) (DocDiff, error) {
	if !r.indexReady {
		return DocDiff{}, ErrReaderNotReady
	}
	entry, err := r.entryByID(ctx, strings.TrimSpace(id))
	if errors.Is(err, sql.ErrNoRows) {
		return DocDiff{}, ErrDocumentNotFound
	}
	if err != nil {
		return DocDiff{}, err
	}
	if err := ValidateRelativePath(entry.Path); err != nil {
		return DocDiff{}, ErrDocumentNotFound
	}
	absolute := filepath.Join(r.config.VaultPath, filepath.FromSlash(entry.Path))
	if !IsInsideRoot(r.config.VaultPath, absolute) {
		return DocDiff{}, ErrDocumentNotFound
	}
	git := &Git{vaultPath: r.config.VaultPath, timeout: gitCommandTimeout}
	return git.DocumentDiff(ctx, entry.Path)
}

func (r *Reader) entryByID(ctx context.Context, id string) (IndexEntry, error) {
	return scanIndexEntry(r.index.db.QueryRowContext(ctx, entrySelectSQL+` WHERE id=?`, id))
}

func (r *Reader) entryByPath(ctx context.Context, relative string) (IndexEntry, error) {
	relative = filepath.ToSlash(strings.TrimSpace(relative))
	return scanIndexEntry(r.index.db.QueryRowContext(ctx, entrySelectSQL+` WHERE path=?`, relative))
}

const entrySelectSQL = `SELECT id,path,scope,project_id,slug,parent_id,type,legacy_type,title,body,status,summary,revision,tags,
	sort_order,is_index,archive,created,updated,source_task,source_turn,confidence,trust,prefix,content_hash,warning,frontmatter_json
	FROM entries`

func scanIndexEntry(row *sql.Row) (IndexEntry, error) {
	var item IndexEntry
	var tags string
	err := row.Scan(&item.ID, &item.Path, &item.Scope, &item.ProjectID, &item.Slug, &item.ParentID,
		&item.Type, &item.LegacyType, &item.Title, &item.Body, &item.Status, &item.Summary,
		&item.Revision, &tags,
		&item.SortOrder, &item.IsIndex, &item.Archive, &item.Created, &item.Updated,
		&item.SourceTask, &item.SourceTurn, &item.Confidence, &item.Trust, &item.Prefix,
		&item.ContentHash, &item.Warning, &item.Frontmatter)
	if err != nil {
		return IndexEntry{}, err
	}
	if tags != "" {
		item.Tags = strings.Split(tags, ",")
	}
	return item, nil
}

func (r *Reader) backlinks(ctx context.Context, entry IndexEntry) ([]Backlink, error) {
	rows, err := r.index.db.QueryContext(ctx, `
		SELECT DISTINCT e.id,e.title,e.path FROM links l JOIN entries e ON e.id=l.source_id
		WHERE l.target_path=? OR l.target=? ORDER BY e.title,e.id`, entry.Path, entry.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Backlink{}
	for rows.Next() {
		var item Backlink
		if err := rows.Scan(&item.ID, &item.Title, &item.Path); err != nil {
			return nil, err
		}
		if item.ID == entry.ID {
			continue
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *Reader) libraryForEntry(entry IndexEntry) (string, string) {
	switch entry.Scope {
	case "global":
		return "global", "全局知识"
	case "project":
		return "project:" + entry.ProjectID, r.projectName(entry.ProjectID)
	case "archive":
		return "archive:" + entry.ProjectID, r.archiveName(entry.ProjectID)
	case "orphan":
		return "orphan", "未归类"
	default:
		return "", "知识库"
	}
}

// DocumentSearchOptions is the Web-facing filter set. Type, status and tag are
// exact matches; library/project resolve to scope and project id.
type DocumentSearchOptions struct {
	Query     string
	Library   string
	ProjectID string
	Type      string
	Status    string
	Tag       string
	Limit     int
}

type SearchResponse struct {
	Enabled    bool           `json:"enabled"`
	IndexReady bool           `json:"index_ready"`
	Results    []SearchResult `json:"results"`
	Warning    string         `json:"warning,omitempty"`
}

func (r *Reader) Search(ctx context.Context, options DocumentSearchOptions) (SearchResponse, error) {
	result := SearchResponse{Enabled: true, IndexReady: r.indexReady, Results: []SearchResult{}}
	if !r.indexReady {
		result.Warning = "index_missing"
		return result, nil
	}
	scope, projectID, err := r.libraryScope(options.Library, options.ProjectID)
	if err != nil {
		return result, err
	}
	results, err := r.index.Search(ctx, options.Query, SearchOptions{
		Scope: scope, ProjectID: projectID, Type: options.Type,
		Status: options.Status, Tag: options.Tag, Limit: options.Limit,
	})
	if err != nil {
		return result, err
	}
	result.Results = results
	return result, nil
}

// RecentDocument is one row of the mtime change feed.
type RecentDocument struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Path       string `json:"path"`
	Type       string `json:"type"`
	Status     string `json:"status"`
	Scope      string `json:"scope"`
	ProjectID  string `json:"project_id,omitempty"`
	LibraryID  string `json:"library_id"`
	Library    string `json:"library"`
	Updated    string `json:"updated,omitempty"`
	ModifiedAt string `json:"modified_at"`
}

type RecentResponse struct {
	Enabled    bool             `json:"enabled"`
	IndexReady bool             `json:"index_ready"`
	Items      []RecentDocument `json:"items"`
}

// Recent orders documents by file mtime, not by frontmatter updated_at, so a
// freshly exported or hand-edited file shows up even if its header is older.
func (r *Reader) Recent(ctx context.Context, projectID string, limit int) (RecentResponse, error) {
	result := RecentResponse{Enabled: true, IndexReady: r.indexReady, Items: []RecentDocument{}}
	if !r.indexReady {
		return result, nil
	}
	if limit <= 0 {
		limit = 120
	}
	if limit > 500 {
		limit = 500
	}
	entries, err := r.index.Entries(ctx, "", "")
	if err != nil {
		return result, err
	}
	items := make([]RecentDocument, 0, len(entries))
	for _, entry := range entries {
		if isReservedVaultPath(entry.Path) {
			continue
		}
		if projectID != "" && entry.ProjectID != projectID {
			continue
		}
		libraryID, libraryName := r.libraryForEntry(entry)
		modified := entry.Updated
		absolute := filepath.Join(r.config.VaultPath, filepath.FromSlash(entry.Path))
		if info, statErr := os.Stat(absolute); statErr == nil {
			modified = info.ModTime().UTC().Format(time.RFC3339Nano)
		}
		if modified == "" {
			modified = entry.Updated
		}
		items = append(items, RecentDocument{
			ID: entry.ID, Title: entry.Title, Path: entry.Path, Type: entry.Type,
			Status: entry.Status, Scope: entry.Scope, ProjectID: entry.ProjectID,
			LibraryID: libraryID, Library: libraryName,
			Updated: entry.Updated, ModifiedAt: modified,
		})
	}
	sort.SliceStable(items, func(left, right int) bool {
		if items[left].ModifiedAt != items[right].ModifiedAt {
			return items[left].ModifiedAt > items[right].ModifiedAt
		}
		return items[left].ID < items[right].ID
	})
	if len(items) > limit {
		items = items[:limit]
	}
	result.Items = items
	return result, nil
}

type InboxCount struct {
	LibraryID string `json:"library_id"`
	Library   string `json:"library"`
	ProjectID string `json:"project_id,omitempty"`
	Count     int    `json:"count"`
}

type InboxResponse struct {
	Enabled    bool             `json:"enabled"`
	IndexReady bool             `json:"index_ready"`
	Count      int              `json:"count"`
	Counts     []InboxCount     `json:"counts"`
	Items      []RecentDocument `json:"items"`
}

// Inbox is the read-only "to file" list. It contains only files under an
// inbox/ directory; the knowledge tree deliberately never shows those paths.
func (r *Reader) Inbox(ctx context.Context, projectID string) (InboxResponse, error) {
	result := InboxResponse{Enabled: true, IndexReady: r.indexReady, Counts: []InboxCount{}, Items: []RecentDocument{}}
	if !r.indexReady {
		return result, nil
	}
	entries, err := r.index.Entries(ctx, "", "")
	if err != nil {
		return result, err
	}
	counts := map[string]*InboxCount{}
	for _, entry := range entries {
		if !isInboxPath(entry.Path) {
			continue
		}
		if projectID != "" && entry.ProjectID != projectID {
			continue
		}
		libraryID, libraryName := r.libraryForEntry(entry)
		item := RecentDocument{
			ID: entry.ID, Title: entry.Title, Path: entry.Path, Type: entry.Type,
			Status: entry.Status, Scope: entry.Scope, ProjectID: entry.ProjectID,
			LibraryID: libraryID, Library: libraryName, Updated: entry.Updated,
			ModifiedAt: entry.Updated,
		}
		result.Items = append(result.Items, item)
		key := libraryID
		counter := counts[key]
		if counter == nil {
			counter = &InboxCount{LibraryID: libraryID, Library: libraryName, ProjectID: entry.ProjectID}
			counts[key] = counter
		}
		counter.Count++
	}
	sort.SliceStable(result.Items, func(left, right int) bool {
		return result.Items[left].Updated > result.Items[right].Updated
	})
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		result.Counts = append(result.Counts, *counts[key])
	}
	result.Count = len(result.Items)
	return result, nil
}

// SkillPackage is a read-only skill folder under skills/ or a project
// skills/ directory. Phase 2 does not migrate skill contents.
type SkillPackage struct {
	ID           string   `json:"id"`
	Slug         string   `json:"slug"`
	Name         string   `json:"name"`
	Scope        string   `json:"scope"`
	ProjectID    string   `json:"project_id,omitempty"`
	Library      string   `json:"library"`
	Description  string   `json:"description,omitempty"`
	FileCount    int      `json:"file_count"`
	Files        []string `json:"files,omitempty"`
	RelativePath string   `json:"relative_path"`
	AbsolutePath string   `json:"absolute_path"`
	OpenPath     string   `json:"open_path"`
}

type SkillsResponse struct {
	Enabled bool           `json:"enabled"`
	Skills  []SkillPackage `json:"skills"`
}

// Skills scans the flat skills/ area plus per-project skills/ placeholders.
func (r *Reader) Skills(ctx context.Context) (SkillsResponse, error) {
	result := SkillsResponse{Enabled: true, Skills: []SkillPackage{}}
	if !r.vaultReady {
		return result, nil
	}
	type location struct {
		relative string
		scope    string
		project  string
		library  string
	}
	locations := []location{{relative: "skills", scope: "global", library: "全局技能"}}
	if info, err := os.Stat(filepath.Join(r.config.VaultPath, "global", "skills")); err == nil && info.IsDir() {
		locations = append(locations, location{relative: "global/skills", scope: "global", library: "全局技能"})
	}
	for id, slug := range r.manifest.Projects {
		relative := path.Join("projects", slug, "skills")
		locations = append(locations, location{relative: relative, scope: "project", project: id, library: r.projectName(id)})
	}
	for id, slug := range r.manifest.Archives {
		relative := path.Join("archive", slug, "skills")
		locations = append(locations, location{relative: relative, scope: "archive", project: id, library: r.archiveName(id)})
	}
	sort.Slice(locations, func(left, right int) bool { return locations[left].relative < locations[right].relative })
	for _, loc := range locations {
		directory := filepath.Join(r.config.VaultPath, filepath.FromSlash(loc.relative))
		if !IsInsideRoot(r.config.VaultPath, directory) {
			continue
		}
		children, err := os.ReadDir(directory)
		if err != nil {
			continue
		}
		for _, child := range children {
			if !child.IsDir() || strings.HasPrefix(child.Name(), ".") {
				continue
			}
			skill, ok := r.skillPackage(path.Join(loc.relative, child.Name()), loc.scope, loc.project, loc.library)
			if ok {
				result.Skills = append(result.Skills, skill)
			}
		}
	}
	sort.Slice(result.Skills, func(left, right int) bool {
		if result.Skills[left].Name != result.Skills[right].Name {
			return result.Skills[left].Name < result.Skills[right].Name
		}
		return result.Skills[left].RelativePath < result.Skills[right].RelativePath
	})
	return result, nil
}

func (r *Reader) skillPackage(relative, scope, projectID, library string) (SkillPackage, bool) {
	directory := filepath.Join(r.config.VaultPath, filepath.FromSlash(relative))
	if !IsInsideRoot(r.config.VaultPath, directory) {
		return SkillPackage{}, false
	}
	skillPath := filepath.Join(directory, "SKILL.md")
	raw, err := os.ReadFile(skillPath)
	if err != nil {
		return SkillPackage{}, false
	}
	document := ParseDocument(string(raw))
	name := strings.TrimSpace(document.Frontmatter.Title)
	if value, ok := document.Frontmatter.Extra["name"].(string); ok && strings.TrimSpace(value) != "" {
		name = strings.TrimSpace(value)
	}
	if name == "" {
		name = FirstHeading(document.Body)
	}
	if name == "" {
		name = childName(relative)
	}
	description := ""
	if value, ok := document.Frontmatter.Extra["description"].(string); ok {
		description = strings.TrimSpace(value)
	}
	if description == "" {
		description = IndexSummary(document.Body, 160)
	}
	files := []string{}
	fileCount := 0
	_ = filepath.WalkDir(directory, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		relativeFile, relErr := filepath.Rel(directory, filePath)
		if relErr != nil || strings.HasPrefix(relativeFile, "..") {
			return nil
		}
		fileCount++
		if len(files) < 40 {
			files = append(files, filepath.ToSlash(relativeFile))
		}
		return nil
	})
	slug := childName(relative)
	return SkillPackage{
		ID: "vault-skill:" + strings.ReplaceAll(relative, "/", ":"), Slug: slug, Name: name,
		Scope: scope, ProjectID: projectID, Library: library, Description: description,
		FileCount: fileCount, Files: files, RelativePath: relative,
		AbsolutePath: directory, OpenPath: skillPath,
	}, true
}

func childName(relative string) string {
	segments := strings.Split(filepath.ToSlash(relative), "/")
	if len(segments) == 0 {
		return relative
	}
	return segments[len(segments)-1]
}

func (r *Reader) libraryScope(library, projectID string) (string, string, error) {
	library = strings.TrimSpace(library)
	projectID = strings.TrimSpace(projectID)
	switch {
	case library == "" && projectID != "":
		return "project", projectID, nil
	case library == "" || library == "all":
		return "", "", nil
	case library == "global":
		return "global", "", nil
	case library == "orphan":
		return "orphan", "", nil
	case strings.HasPrefix(library, "project:"):
		id := strings.TrimPrefix(library, "project:")
		if id == "" {
			return "", "", fmt.Errorf("invalid library %q", library)
		}
		return "project", id, nil
	case strings.HasPrefix(library, "archive:"):
		id := strings.TrimPrefix(library, "archive:")
		if id == "" {
			return "", "", fmt.Errorf("invalid library %q", library)
		}
		return "archive", id, nil
	default:
		return "", "", fmt.Errorf("unknown library %q", library)
	}
}

// isIndexDocumentPath reports whether a vault-relative path is a directory's
// index.md. Exported folder nodes always live at <dir>/index.md, but the index
// database cannot be trusted for the is_index flag: a document that gained
// children after export keeps is_index=false in its frontmatter, so the path is
// the only stable directory marker.
func isIndexDocumentPath(relative string) bool {
	return strings.EqualFold(path.Base(filepath.ToSlash(relative)), "index.md")
}

// buildReaderTree renders the vault's on-disk directory hierarchy instead of
// the database parent links. Every index.md entry owns its directory and shows
// up as a folder node even when the database stored is_index=false. Documents,
// and folders under directories without an index.md, attach to their nearest
// indexed ancestor; directories without an index.md never become synthetic
// clickable nodes.
func buildReaderTree(entries []IndexEntry) []TreeNode {
	type treeEntry struct {
		node     TreeNode
		dir      string // non-empty only for directory nodes
		children []*treeEntry
	}
	newEntry := func(entry IndexEntry, folder bool) *treeEntry {
		item := &treeEntry{node: TreeNode{
			ID: entry.ID, Title: entry.Title, Path: entry.Path, Type: entry.Type,
			Status: entry.Status, Scope: entry.Scope, ProjectID: entry.ProjectID,
			Updated: entry.Updated, IsIndex: entry.IsIndex, System: isSystemEntry(entry),
		}}
		if folder {
			item.node.IsIndex = true
			item.node.System = true
			item.dir = path.Dir(filepath.ToSlash(entry.Path))
			if item.dir == "." {
				item.dir = ""
			}
		}
		return item
	}

	folders := map[string]*treeEntry{}
	folderIDs := map[string]bool{}
	folderOrder := make([]*treeEntry, 0, len(entries))
	for _, entry := range entries {
		if !isIndexDocumentPath(entry.Path) || folderIDs[entry.ID] {
			continue
		}
		item := newEntry(entry, true)
		if _, exists := folders[item.dir]; exists {
			continue
		}
		folders[item.dir] = item
		folderIDs[entry.ID] = true
		folderOrder = append(folderOrder, item)
	}

	rootChildren := make([]*treeEntry, 0, len(entries))
	nearestFolder := func(directory string) *treeEntry {
		for directory != "" && directory != "." && directory != "/" {
			if folder, ok := folders[directory]; ok {
				return folder
			}
			parent := path.Dir(directory)
			if parent == directory {
				break
			}
			directory = parent
		}
		return nil
	}
	attach := func(item *treeEntry, directory string) {
		if parent := nearestFolder(directory); parent != nil && parent != item {
			parent.children = append(parent.children, item)
			return
		}
		rootChildren = append(rootChildren, item)
	}
	for _, item := range folderOrder {
		attach(item, path.Dir(item.dir))
	}
	placed := make(map[string]bool, len(folderIDs)+len(entries))
	for id := range folderIDs {
		placed[id] = true
	}
	for _, entry := range entries {
		if placed[entry.ID] {
			continue
		}
		placed[entry.ID] = true
		attach(newEntry(entry, false), path.Dir(filepath.ToSlash(entry.Path)))
	}

	sortEntries := func(list []*treeEntry) {
		sort.SliceStable(list, func(left, right int) bool {
			a, b := list[left].node, list[right].node
			if a.IsIndex != b.IsIndex {
				return a.IsIndex
			}
			if a.Title != b.Title {
				return a.Title < b.Title
			}
			return a.ID < b.ID
		})
	}
	var render func(item *treeEntry) TreeNode
	render = func(item *treeEntry) TreeNode {
		sortEntries(item.children)
		node := item.node
		node.Children = nil
		for _, child := range item.children {
			node.Children = append(node.Children, render(child))
		}
		return node
	}
	sortEntries(rootChildren)
	result := make([]TreeNode, 0, len(rootChildren))
	for _, item := range rootChildren {
		result = append(result, render(item))
	}
	return result
}

func isInboxPath(relative string) bool {
	for _, segment := range strings.Split(filepath.ToSlash(relative), "/") {
		if strings.EqualFold(segment, "inbox") {
			return true
		}
	}
	return false
}

func isReservedVaultPath(relative string) bool {
	first := strings.SplitN(filepath.ToSlash(relative), "/", 2)[0]
	switch strings.ToLower(first) {
	case "skills", ".trash", ".aha", ".git", ".obsidian":
		return true
	default:
		return false
	}
}

func isSystemEntry(entry IndexEntry) bool {
	return entry.IsIndex || strings.EqualFold(entry.Type, TypeNavigation) ||
		strings.HasSuffix(strings.ToLower(entry.Path), "/index.md") ||
		strings.EqualFold(entry.Path, "global/index.md")
}

func outlineItems(body string) []OutlineItem {
	result := []OutlineItem{}
	seen := map[string]int{}
	fenceMarker := byte(0)
	fenceLength := 0
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimLeft(line, " ")
		if len(line)-len(trimmed) > 3 {
			continue
		}
		if fenceMarker != 0 {
			if closesCodeFence(trimmed, fenceMarker, fenceLength) {
				fenceMarker = 0
				fenceLength = 0
			}
			continue
		}
		if marker, length, ok := opensCodeFence(trimmed); ok {
			fenceMarker = marker
			fenceLength = length
			continue
		}
		level := 0
		for level < len(trimmed) && level < 6 && trimmed[level] == '#' {
			level++
		}
		if level == 0 || level >= len(trimmed) || trimmed[level] != ' ' {
			continue
		}
		text := strings.TrimSpace(strings.TrimRight(trimmed[level:], "# "))
		if text == "" {
			continue
		}
		slug := outlineSlug(text)
		if count := seen[slug]; count > 0 {
			seen[slug] = count + 1
			slug = fmt.Sprintf("%s-%d", slug, count+1)
		} else {
			seen[slug] = 1
		}
		result = append(result, OutlineItem{Level: level, Text: text, Slug: slug})
	}
	return result
}

func opensCodeFence(line string) (byte, int, bool) {
	if len(line) < 3 || (line[0] != '`' && line[0] != '~') {
		return 0, 0, false
	}
	marker := line[0]
	length := 0
	for length < len(line) && line[length] == marker {
		length++
	}
	if length < 3 {
		return 0, 0, false
	}
	info := strings.TrimSpace(line[length:])
	if marker == '`' && strings.ContainsRune(info, '`') {
		return 0, 0, false
	}
	return marker, length, true
}

func closesCodeFence(line string, marker byte, minimum int) bool {
	length := 0
	for length < len(line) && line[length] == marker {
		length++
	}
	return length >= minimum && strings.TrimSpace(line[length:]) == ""
}

func outlineSlug(text string) string {
	var builder strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(text)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastDash = false
		case r == ' ' || r == '-' || r == '_':
			if builder.Len() > 0 && !lastDash {
				builder.WriteByte('-')
				lastDash = true
			}
		}
	}
	slug := strings.Trim(builder.String(), "-")
	if slug == "" {
		slug = "section"
	}
	return "kv-" + slug
}
