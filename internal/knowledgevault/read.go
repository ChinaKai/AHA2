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
	root, _, _ := strings.Cut(relative, "/")
	if strings.EqualFold(root, "skills") || strings.EqualFold(root, ".trash") {
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
	// RootID is the index entry ID of the library's own index document. It is
	// the value channel knowledge grants must store: the library ID is only a
	// picker key ("project:<id>"/"global"), not a knowledge entry ID.
	RootID string `json:"root_id,omitempty"`
	Count  int    `json:"count"`
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
		result.Libraries = append(result.Libraries, r.rootLibraries(nil)...)
		return result
	}

	entries, err := r.index.Entries(ctx, "", "")
	if err != nil {
		result.Warning = "index_read_failed"
		return result
	}
	counts := map[string]int{}
	rootIDs := map[string]string{}
	rootCounts := map[string]int{}
	orphans := 0
	for _, entry := range entries {
		if entry.IsIndex {
			switch entry.Scope {
			case "global":
				rootIDs["global"] = entry.ID
			case "project":
				rootIDs["project:"+entry.ProjectID] = entry.ID
			}
		}
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
		case "root":
			rootCounts[entry.Prefix]++
		case "orphan":
			orphans++
		}
	}
	result.Libraries = append(result.Libraries, Library{ID: "global", Kind: "global", Name: "全局知识", Count: counts["global"], RootID: rootIDs["global"]})
	result.Projects = r.manifestProjects()
	for index := range result.Projects {
		result.Projects[index].Count = counts[result.Projects[index].ID]
		result.Projects[index].RootID = rootIDs[result.Projects[index].ID]
		result.Libraries = append(result.Libraries, result.Projects[index])
	}
	result.Archived = r.manifestArchives()
	for index := range result.Archived {
		result.Archived[index].Count = counts[result.Archived[index].ID]
		result.Libraries = append(result.Libraries, result.Archived[index])
	}
	result.Libraries = append(result.Libraries, r.rootLibraries(rootCounts)...)
	if orphans > 0 {
		result.Orphans = orphans
		result.Libraries = append(result.Libraries, Library{ID: "orphan", Kind: "orphan", Name: "未归类", Count: orphans})
	}
	if status, statusErr := r.index.Status(ctx); statusErr == nil {
		result.RebuiltAt = status.RebuiltAt
	}
	return result
}

func (r *Reader) rootLibraries(counts map[string]int) []Library {
	directories := RootLibraryDirectories(r.config.VaultPath)
	if len(directories) == 0 {
		return nil
	}
	result := make([]Library, 0, len(directories))
	for _, directory := range directories {
		result = append(result, Library{
			ID: "root:" + directory, Kind: "root", Name: rootLibraryTitle(r.config.VaultPath, directory),
			RootID: vaultNavigationID(RootLibraryIndexRelative(directory)), Count: counts[directory],
		})
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
	// Older rebuilds wrote the generated knowledge-home title into
	// project_names. Ignore it so an existing install stops showing every
	// project as "知识首页" even before the next rebuild repairs the manifest.
	if name := strings.TrimSpace(r.manifest.ProjectNames[id]); name != "" && !isKnowledgeHomeTitle(name) {
		return name
	}
	if slug := strings.TrimSpace(r.manifest.Projects[id]); slug != "" {
		return slug
	}
	return id
}

func (r *Reader) archiveName(id string) string {
	if name := strings.TrimSpace(r.manifest.ArchiveNames[id]); name != "" && !isKnowledgeHomeTitle(name) {
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
	rootDirectory := rootLibraryScopeDirectory(library)
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
		if rootDirectory != "" && entry.Prefix != rootDirectory {
			continue
		}
		filtered = append(filtered, entry)
	}
	// The all-libraries tree shows the projects/, archive/ and root library
	// container directories. Their own navigation pages stay out of the index
	// and search, so synthesize tree-only entries here: the folder becomes
	// clickable while Document() reads the page straight from the vault. An
	// index written by an older build may still contain a page; never add a
	// duplicate path in that case.
	if scope == "" || rootDirectory != "" {
		indexedPaths := make(map[string]bool, len(filtered))
		for _, entry := range filtered {
			indexedPaths[entry.Path] = true
		}
		for _, navigation := range r.containerNavigationEntries() {
			if rootDirectory != "" && navigation.Path != RootLibraryIndexRelative(rootDirectory) {
				continue
			}
			if !indexedPaths[navigation.Path] {
				filtered = append(filtered, navigation)
			}
		}
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
	if relative, ok := vaultNavigationRelativePath(strings.TrimSpace(id)); ok {
		return r.navigationDocument(relative)
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

// EntryByID resolves one indexed document. Callers outside the package use it
// to decide whether an ID belongs to the vault (and therefore whether a Web or
// Pet operation must write the vault) before touching either store. A vault
// opened without a ready index has no rows: report not-found rather than
// dereferencing the nil index. Callers that route by source keep their normal
// legacy path for that state.
func (r *Reader) EntryByID(ctx context.Context, id string) (IndexEntry, error) {
	if r == nil || !r.indexReady || r.index == nil {
		return IndexEntry{}, sql.ErrNoRows
	}
	return r.entryByID(ctx, strings.TrimSpace(id))
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
	case "root":
		directory := strings.SplitN(entry.Path, "/", 2)[0]
		return "root:" + directory, rootLibraryTitle(r.config.VaultPath, directory)
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
	// IncludeArchives controls whether archive/ entries are searchable. The
	// Web directory passes true so archived knowledge is findable with its
	// archive marker; API callers can explicitly exclude it.
	IncludeArchives bool
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
		Scope: scope, ProjectID: projectID, Prefix: rootLibraryScopeDirectory(options.Library),
		Type: options.Type, Status: options.Status, Tag: options.Tag, Limit: options.Limit,
		IncludeArchives: options.IncludeArchives || strings.HasPrefix(strings.TrimSpace(options.Library), "archive:"),
	})
	if err != nil {
		return result, err
	}
	result.Results = results
	return result, nil
}

// maxRecentDocuments bounds one change-feed response after content filtering.
const maxRecentDocuments = 120

// RecentDocument is one row of the content change feed.
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
	// ChangedAt is when the content actually changed: file mtime for
	// uncommitted rows, the last content-changing commit time for committed
	// rows. The feed sorts on it.
	ChangedAt      string `json:"changed_at,omitempty"`
	ContentChanged bool   `json:"content_changed"`
	ChangeKind     string `json:"change_kind"`
	LastCommitAt   string `json:"last_commit_at,omitempty"`
}

type RecentResponse struct {
	Enabled    bool             `json:"enabled"`
	IndexReady bool             `json:"index_ready"`
	Items      []RecentDocument `json:"items"`
}

// Recent lists documents whose content actually changed. It compares the
// worktree with HEAD (status plus one batched blob hash) and the last commit
// that touched each path, so touching, exporting or rebuilding an unchanged
// file no longer pushes an empty diff into the feed. includeUnchanged keeps
// those timestamp-only rows with content_changed=false so the Web UI can reveal
// them behind its explicit toggle.
func (r *Reader) Recent(ctx context.Context, projectID string, limit int, includeUnchanged bool) (RecentResponse, error) {
	result := RecentResponse{Enabled: true, IndexReady: r.indexReady, Items: []RecentDocument{}}
	if !r.indexReady {
		return result, nil
	}
	if limit <= 0 {
		limit = maxRecentDocuments
	}
	if limit > maxRecentDocuments {
		limit = maxRecentDocuments
	}
	entries, err := r.index.Entries(ctx, "", "")
	if err != nil {
		return result, err
	}
	type recentRow struct {
		doc       RecentDocument
		candidate recentCandidate
	}
	rows := make([]recentRow, 0, len(entries))
	candidates := make([]recentCandidate, 0, len(entries))
	for _, entry := range entries {
		if isReservedVaultPath(entry.Path) {
			continue
		}
		if projectID != "" && entry.ProjectID != projectID {
			continue
		}
		if err := ValidateRelativePath(entry.Path); err != nil {
			continue
		}
		absolute := filepath.Join(r.config.VaultPath, filepath.FromSlash(entry.Path))
		if !IsInsideRoot(r.config.VaultPath, absolute) {
			continue
		}
		libraryID, libraryName := r.libraryForEntry(entry)
		modified := entry.Updated
		candidate := recentCandidate{path: entry.Path, updatedAt: parseRecentTime(entry.Updated)}
		if info, statErr := os.Stat(absolute); statErr == nil {
			candidate.exists = true
			candidate.modifiedAt = info.ModTime()
			modified = info.ModTime().UTC().Format(time.RFC3339Nano)
		}
		if modified == "" {
			modified = entry.Updated
		}
		rows = append(rows, recentRow{
			doc: RecentDocument{
				ID: entry.ID, Title: entry.Title, Path: entry.Path, Type: entry.Type,
				Status: entry.Status, Scope: entry.Scope, ProjectID: entry.ProjectID,
				LibraryID: libraryID, Library: libraryName,
				Updated: entry.Updated, ModifiedAt: modified,
			},
			candidate: candidate,
		})
		candidates = append(candidates, candidate)
	}
	changes := r.recentContentChanges(ctx, candidates)
	type sortedItem struct {
		doc       RecentDocument
		effective time.Time
	}
	sorted := make([]sortedItem, 0, len(rows))
	for _, row := range rows {
		change, ok := changes[row.candidate.path]
		if !ok {
			change = recentContentChange{kind: RecentChangeUnknown, changedAt: row.candidate.fallbackTime()}
		}
		if !includeUnchanged && !change.contentChanged() {
			continue
		}
		effective := change.changedAt
		if effective.IsZero() {
			effective = row.candidate.fallbackTime()
		}
		doc := row.doc
		doc.ChangeKind = change.kind
		doc.ContentChanged = change.contentChanged()
		if !effective.IsZero() {
			doc.ChangedAt = effective.UTC().Format(time.RFC3339Nano)
		}
		if change.hasLastCommit {
			doc.LastCommitAt = change.lastCommit.UTC().Format(time.RFC3339Nano)
		}
		sorted = append(sorted, sortedItem{doc: doc, effective: effective})
	}
	sort.SliceStable(sorted, func(left, right int) bool {
		if !sorted[left].effective.Equal(sorted[right].effective) {
			return sorted[left].effective.After(sorted[right].effective)
		}
		return sorted[left].doc.ID < sorted[right].doc.ID
	})
	items := make([]RecentDocument, 0, len(sorted))
	for _, item := range sorted {
		items = append(items, item.doc)
	}
	if len(items) > limit {
		items = items[:limit]
	}
	result.Items = items
	return result, nil
}

func parseRecentTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed
	}
	return time.Time{}
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
	ID string `json:"id"`
	// Key is the stable identity used by task selection and auto-attach
	// preferences. Unlike ID it does not embed the project's vault slug, so a
	// project rename inside the vault does not orphan the preference.
	Key          string   `json:"key"`
	Slug         string   `json:"slug"`
	Name         string   `json:"name"`
	Scope        string   `json:"scope"`
	ProjectID    string   `json:"project_id,omitempty"`
	Library      string   `json:"library"`
	Description  string   `json:"description,omitempty"`
	Instructions string   `json:"instructions,omitempty"`
	Version      int      `json:"version"`
	Enabled      bool     `json:"enabled"`
	Status       string   `json:"status"`
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
	locations := []location{{relative: "global/skills", scope: "global", library: "全局技能"}}
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
	version := 1
	if value, ok := document.Frontmatter.Extra["version"]; ok {
		if parsed, ok := intFromAny(value); ok && parsed > 0 {
			version = parsed
		}
	}
	enabled := true
	if value, ok := document.Frontmatter.Extra["enabled"]; ok {
		if parsed, ok := boolFromAny(value); ok {
			enabled = parsed
		}
	}
	status := "active"
	if value, ok := document.Frontmatter.Extra["status"].(string); ok && strings.TrimSpace(value) != "" {
		status = strings.ToLower(strings.TrimSpace(value))
	}
	return SkillPackage{
		ID: "vault-skill:" + strings.ReplaceAll(relative, "/", ":"), Key: SkillKey(scope, projectID, slug, relative),
		Slug: slug, Name: name,
		Scope: scope, ProjectID: projectID, Library: library, Description: description,
		Instructions: strings.TrimSpace(document.Body),
		Version:      version, Enabled: enabled, Status: status,
		FileCount: fileCount, Files: files, RelativePath: relative,
		AbsolutePath: directory, OpenPath: skillPath,
	}, true
}

// VaultSkillIDPrefix marks a Task skill id owned by the file vault instead of
// the legacy skills table. The two share one selection list in the Web UI.
const VaultSkillIDPrefix = "vault-skill:"

// IsVaultSkillID reports whether a task skill id belongs to a vault package.
func IsVaultSkillID(id string) bool {
	return strings.HasPrefix(strings.TrimSpace(id), VaultSkillIDPrefix)
}

// SkillKey derives the stable key for a vault skill. Scope plus project plus
// slug survives project slug renames; the flat legacy skills/ area gets its
// own namespace so it cannot collide with global/skills/<same-slug>.
func SkillKey(scope, projectID, slug, relative string) string {
	switch scope {
	case "global":
		if strings.HasPrefix(filepath.ToSlash(relative), "skills/") {
			return VaultSkillIDPrefix + "global:flat:" + slug
		}
		return VaultSkillIDPrefix + "global:" + slug
	case "project":
		return VaultSkillIDPrefix + "project:" + projectID + ":" + slug
	case "archive":
		return VaultSkillIDPrefix + "archive:" + projectID + ":" + slug
	}
	return VaultSkillIDPrefix + strings.ReplaceAll(filepath.ToSlash(relative), "/", ":")
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
	case strings.HasPrefix(library, "root:"):
		directory := strings.TrimPrefix(library, "root:")
		if !rootLibraryNameAllowed(directory) || strings.Contains(directory, "/") {
			return "", "", fmt.Errorf("invalid library %q", library)
		}
		return "root", "", nil
	default:
		return "", "", fmt.Errorf("unknown library %q", library)
	}
}

// rootLibraryScopeDirectory returns the directory name of a root:<dir>
// library selector. It is empty for every other selector.
func rootLibraryScopeDirectory(library string) string {
	library = strings.TrimSpace(library)
	if !strings.HasPrefix(library, "root:") {
		return ""
	}
	return strings.TrimPrefix(library, "root:")
}

// isIndexDocumentPath reports whether a vault-relative path is a directory's
// index.md. Exported folder nodes always live at <dir>/index.md, but the index
// database cannot be trusted for the is_index flag: a document that gained
// children after export keeps is_index=false in its frontmatter, so the path is
// the only stable directory marker.
func isIndexDocumentPath(relative string) bool {
	return strings.EqualFold(path.Base(filepath.ToSlash(relative)), "index.md")
}

// Container navigation pages (projects/index.md, archive/index.md and
// archive/projects/index.md) are maintained on disk but deliberately stay out
// of the index and search. The Web tree still needs a clickable folder for
// them, so Tree synthesizes a tree-only entry with this ID prefix and
// Document reads the file from the vault instead of the index. The ID avoids
// "/" so it survives a single HTTP path segment.
const vaultNavigationIDPrefix = "vault-navigation:"

var vaultNavigationPaths = []string{
	projectsRootIndexPath,
	archiveRootIndexPath,
	archiveProjectsIndexPath,
}

func vaultNavigationID(relative string) string {
	return vaultNavigationIDPrefix + strings.ReplaceAll(filepath.ToSlash(relative), "/", ":")
}

func vaultNavigationRelativePath(id string) (string, bool) {
	id = strings.TrimSpace(id)
	if !strings.HasPrefix(id, vaultNavigationIDPrefix) {
		return "", false
	}
	relative := strings.ReplaceAll(strings.TrimPrefix(id, vaultNavigationIDPrefix), ":", "/")
	for _, known := range vaultNavigationPaths {
		if relative == known {
			return relative, true
		}
	}
	// Root library navigation pages are discovered from the filesystem, so the
	// ID is accepted here and the concrete page is validated against the vault
	// by navigationDocument.
	segments := strings.Split(relative, "/")
	if len(segments) == 2 && segments[1] == "index.md" && rootLibraryNameAllowed(segments[0]) {
		return relative, true
	}
	return "", false
}

func navigationPageTitle(relative string, document Document) string {
	if title := strings.TrimSpace(document.Frontmatter.Title); title != "" {
		return title
	}
	return humanizeSegment(path.Base(path.Dir(filepath.ToSlash(relative))))
}

// containerNavigationEntries reports the container pages that exist on disk for
// the all-libraries tree. A flat archive actually named "projects" is a real
// library, not a container; isContainerVaultIndexPath keeps that distinction.
func (r *Reader) containerNavigationEntries() []IndexEntry {
	result := make([]IndexEntry, 0, len(vaultNavigationPaths))
	paths := append([]string{}, vaultNavigationPaths...)
	for _, directory := range RootLibraryDirectories(r.config.VaultPath) {
		paths = append(paths, RootLibraryIndexRelative(directory))
	}
	for _, relative := range paths {
		if !isContainerVaultIndexPath(r.config.VaultPath, relative) {
			continue
		}
		absolute := filepath.Join(r.config.VaultPath, filepath.FromSlash(relative))
		if !IsInsideRoot(r.config.VaultPath, absolute) {
			continue
		}
		info, err := os.Stat(absolute)
		if err != nil || info.IsDir() {
			continue
		}
		title := ""
		if raw, readErr := os.ReadFile(absolute); readErr == nil {
			title = strings.TrimSpace(ParseDocument(string(raw)).Frontmatter.Title)
		}
		if title == "" {
			title = humanizeSegment(path.Base(path.Dir(relative)))
		}
		result = append(result, IndexEntry{
			ID: vaultNavigationID(relative), Path: relative, Type: TypeNavigation,
			Title: title, IsIndex: true, Updated: info.ModTime().UTC().Format(time.RFC3339Nano),
		})
	}
	return result
}

// navigationDocument builds the reader payload for a container navigation page
// without an index row. Only the three known container paths are accepted, so
// a crafted ID cannot read arbitrary vault files.
func (r *Reader) navigationDocument(relative string) (Doc, error) {
	relative = filepath.ToSlash(strings.TrimSpace(relative))
	if !isContainerVaultIndexPath(r.config.VaultPath, relative) {
		return Doc{}, ErrDocumentNotFound
	}
	if err := ValidateRelativePath(relative); err != nil {
		return Doc{}, ErrDocumentNotFound
	}
	absolute := filepath.Join(r.config.VaultPath, filepath.FromSlash(relative))
	if !IsInsideRoot(r.config.VaultPath, absolute) {
		return Doc{}, ErrDocumentNotFound
	}
	raw, err := os.ReadFile(absolute)
	if os.IsNotExist(err) {
		return Doc{}, ErrDocumentNotFound
	}
	if err != nil {
		return Doc{}, err
	}
	document := ParseDocument(string(raw))
	modified := ""
	if info, statErr := os.Stat(absolute); statErr == nil {
		modified = info.ModTime().UTC().Format(time.RFC3339Nano)
	}
	updated := strings.TrimSpace(document.Frontmatter.Updated)
	if updated == "" {
		updated = modified
	}
	libraryID, libraryName := "", "知识库"
	segments := strings.Split(filepath.ToSlash(relative), "/")
	if len(segments) == 2 && segments[1] == "index.md" && isRootLibraryDirectory(r.config.VaultPath, segments[0]) {
		libraryID = "root:" + segments[0]
		libraryName = rootLibraryTitle(r.config.VaultPath, segments[0])
	}
	return Doc{
		ID: vaultNavigationID(relative), Title: navigationPageTitle(relative, document),
		Path: relative, RelativePath: relative, AbsolutePath: absolute,
		LibraryID: libraryID, LibraryName: libraryName,
		Type: TypeNavigation, Status: strings.TrimSpace(document.Frontmatter.Status),
		Body: document.Body, Updated: updated, Created: strings.TrimSpace(document.Frontmatter.Created),
		ModifiedAt: modified, IsIndex: true, System: true, Warning: document.Warning,
		Outline: outlineItems(document.Body), Backlinks: []Backlink{},
		ObsidianVault: r.manifest.ObsidianVault, ObsidianReady: r.obsidianRegistered(),
		ObsidianFile: strings.TrimSuffix(relative, path.Ext(relative)),
		VaultPath:    r.config.VaultPath,
	}, nil
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
