package knowledgevault

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	indexBlockStart = "<!-- aha-vault:index:start -->"
	indexBlockEnd   = "<!-- aha-vault:index:end -->"
)

type sourceProject struct {
	ID          string
	Name        string
	Description string
	ProjectType string
	CreatedAt   string
	UpdatedAt   string
}

type sourceEntry struct {
	ID           string
	Scope        string
	ProjectID    string
	ParentID     string
	Slug         string
	SortOrder    int
	IsIndex      bool
	Type         string
	Title        string
	Body         string
	Status       string
	Confidence   float64
	SourceTaskID string
	SourceTurnID string
	CreatedAt    string
	UpdatedAt    string
}

type sourceData struct {
	Projects []sourceProject
	Entries  []sourceEntry
}

// ExportOptions controls one staged export.
type ExportOptions struct {
	Commit bool
	Now    func() time.Time
}

// ExportReport is the operator-facing result of one export.
type ExportReport struct {
	VaultPath    string   `json:"vault_path"`
	DatabasePath string   `json:"database_path"`
	Total        int      `json:"total"`
	Global       int      `json:"global"`
	Projects     int      `json:"projects"`
	Archives     int      `json:"archives"`
	ProjectInbox int      `json:"project_inbox"`
	Groups       int      `json:"groups"`
	Written      int      `json:"written"`
	Unchanged    int      `json:"unchanged"`
	Removed      int      `json:"removed"`
	Warnings     []string `json:"warnings,omitempty"`
	GitWarning   string   `json:"git_warning,omitempty"`
	Committed    bool     `json:"committed"`
}

func openReadOnlySQLite(path string) (*sql.DB, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	location := &url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}
	dsn := location.String() + "?mode=ro&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open source database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return db, nil
}

func loadSource(ctx context.Context, databasePath string) (sourceData, error) {
	if info, err := os.Stat(databasePath); err != nil || info.IsDir() {
		return sourceData{}, fmt.Errorf("AHA2 database does not exist at %s", databasePath)
	}
	db, err := openReadOnlySQLite(databasePath)
	if err != nil {
		return sourceData{}, err
	}
	defer db.Close()

	projects, err := db.QueryContext(ctx, `SELECT id,name,description,project_type,created_at,updated_at FROM projects ORDER BY id`)
	if err != nil {
		return sourceData{}, fmt.Errorf("read projects: %w", err)
	}
	result := sourceData{}
	for projects.Next() {
		var item sourceProject
		if err := projects.Scan(&item.ID, &item.Name, &item.Description, &item.ProjectType, &item.CreatedAt, &item.UpdatedAt); err != nil {
			projects.Close()
			return sourceData{}, err
		}
		result.Projects = append(result.Projects, item)
	}
	if err := projects.Close(); err != nil {
		return sourceData{}, err
	}

	rows, err := db.QueryContext(ctx, `SELECT id,scope,project_id,parent_id,slug,sort_order,is_index,type,title,body,status,confidence,source_task_id,source_turn_id,created_at,updated_at
		FROM knowledge_entries ORDER BY scope,project_id,is_index DESC,parent_id,sort_order,slug,id`)
	if err != nil {
		return sourceData{}, fmt.Errorf("read knowledge entries: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item sourceEntry
		if err := rows.Scan(&item.ID, &item.Scope, &item.ProjectID, &item.ParentID, &item.Slug, &item.SortOrder,
			&item.IsIndex, &item.Type, &item.Title, &item.Body, &item.Status, &item.Confidence,
			&item.SourceTaskID, &item.SourceTurnID, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return sourceData{}, err
		}
		result.Entries = append(result.Entries, item)
	}
	return result, rows.Err()
}

type projectSlugSet struct {
	byID  map[string]string
	names map[string]string
}

func allocateProjectSlugs(projects []sourceProject, existing map[string]string, existingNames map[string]string, reserved map[string]bool, warnings *[]string) projectSlugSet {
	result := projectSlugSet{byID: map[string]string{}, names: map[string]string{}}
	used := map[string]string{}
	for id, slug := range existing {
		key := strings.ToLower(slug)
		if key == "" {
			continue
		}
		if other, ok := used[key]; ok && other != id {
			*warnings = append(*warnings, fmt.Sprintf("manifest project slug %q is assigned to both %s and %s; the later project was reassigned", slug, other, id))
			continue
		}
		result.byID[id] = slug
		used[key] = id
	}
	ordered := append([]sourceProject(nil), projects...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].ID < ordered[right].ID })
	for _, project := range ordered {
		if _, ok := result.byID[project.ID]; ok {
			result.names[project.ID] = project.Name
			continue
		}
		base := Slugify(project.Name)
		// A non-ASCII display name hashes to an opaque doc-* slug. Prefer the
		// project ID in that case so the directory remains stable and readable.
		if containsNonASCII(project.Name) && project.ID != "" {
			base = Slugify(project.ID)
		}
		if base == "" {
			base = Slugify(project.ID)
		}
		candidate := base
		for attempt := 0; ; attempt++ {
			key := strings.ToLower(candidate)
			_, takenByProject := used[key]
			if !takenByProject && !reserved[key] {
				break
			}
			sum := sha256.Sum256([]byte(project.ID))
			suffix := hex.EncodeToString(sum[:])[:8]
			candidate = base + "-" + suffix
			if attempt > 0 {
				candidate = fmt.Sprintf("%s-%s-%d", base, suffix, attempt+1)
			}
		}
		result.byID[project.ID] = candidate
		result.names[project.ID] = project.Name
		used[strings.ToLower(candidate)] = project.ID
	}
	for id, name := range existingNames {
		if _, ok := result.names[id]; !ok && name != "" {
			result.names[id] = name
		}
	}
	return result
}

type knowledgeNode struct {
	entry    sourceEntry
	parent   *knowledgeNode
	children []*knowledgeNode
	rel      string
	dirRel   string
	isDir    bool
}

type knowledgeTree struct {
	base string
	root *knowledgeNode
}

func buildTree(entries []sourceEntry, base string, warnings *[]string) *knowledgeTree {
	tree := &knowledgeTree{base: base}
	byID := map[string]*knowledgeNode{}
	for _, entry := range entries {
		node := &knowledgeNode{entry: entry}
		byID[entry.ID] = node
	}
	var roots []*knowledgeNode
	for _, entry := range entries {
		if entry.IsIndex {
			roots = append(roots, byID[entry.ID])
		}
	}
	sort.Slice(roots, func(left, right int) bool {
		return roots[left].entry.ID < roots[right].entry.ID
	})
	if len(roots) == 0 {
		rootEntry := sourceEntry{
			ID: syntheticRootID(entries), Scope: firstScope(entries), IsIndex: true,
			Slug: "index", Type: TypeNavigation, Title: "知识首页", Status: "verified",
		}
		if len(entries) > 0 {
			rootEntry.ProjectID = entries[0].ProjectID
		}
		root := &knowledgeNode{entry: rootEntry}
		byID[rootEntry.ID] = root
		roots = []*knowledgeNode{root}
		*warnings = append(*warnings, fmt.Sprintf("%s has no root index; created %s", base, rootEntry.ID))
	}
	if len(roots) > 1 {
		for _, extra := range roots[1:] {
			*warnings = append(*warnings, fmt.Sprintf("%s has multiple root indexes; %s was treated as a child", base, extra.entry.ID))
			extra.entry.IsIndex = false
		}
	}
	tree.root = roots[0]
	// Detect cycles before attaching children. A node whose parent chain loops
	// back to itself is moved to the root instead of being dropped.
	for _, entry := range entries {
		node := byID[entry.ID]
		if node == tree.root {
			continue
		}
		seen := map[string]bool{node.entry.ID: true}
		parent := byID[node.entry.ParentID]
		cyclic := false
		for parent != nil {
			if seen[parent.entry.ID] {
				cyclic = true
				break
			}
			seen[parent.entry.ID] = true
			parent = byID[parent.entry.ParentID]
		}
		if cyclic {
			*warnings = append(*warnings, fmt.Sprintf("%s has a parent cycle; %s was attached to the root", base, node.entry.ID))
			node.entry.ParentID = ""
		}
	}
	for _, entry := range entries {
		node := byID[entry.ID]
		if node == tree.root {
			continue
		}
		parent := byID[node.entry.ParentID]
		if parent == nil || parent == node {
			if node.entry.ParentID != "" {
				*warnings = append(*warnings, fmt.Sprintf("%s references missing parent %q; %s was attached to the root", base, node.entry.ParentID, node.entry.ID))
			}
			parent = tree.root
		}
		node.parent = parent
		parent.children = append(parent.children, node)
	}
	sortChildren(tree.root)
	return tree
}

func sortChildren(node *knowledgeNode) {
	sort.SliceStable(node.children, func(left, right int) bool {
		a, b := node.children[left].entry, node.children[right].entry
		if a.SortOrder != b.SortOrder {
			return a.SortOrder < b.SortOrder
		}
		if a.Slug != b.Slug {
			return a.Slug < b.Slug
		}
		return a.ID < b.ID
	})
	for _, child := range node.children {
		sortChildren(child)
	}
}

func firstScope(entries []sourceEntry) string {
	if len(entries) == 0 {
		return "global"
	}
	return entries[0].Scope
}

func syntheticRootID(entries []sourceEntry) string {
	scope, projectID := firstScope(entries), ""
	if len(entries) > 0 {
		projectID = entries[0].ProjectID
	}
	if scope == "global" {
		return "knowledge_root_global"
	}
	if projectID != "" {
		return "knowledge_root_project_" + projectID
	}
	return "knowledge_root_unknown"
}

func documentSlug(entry sourceEntry) string {
	if entry.IsIndex {
		return "index"
	}
	slug := strings.TrimSpace(entry.Slug)
	if slug != "" && slug != "index" {
		return slug
	}
	if slug == "index" && !entry.IsIndex {
		return "index-" + shortID(entry.ID)
	}
	title := strings.TrimSpace(entry.Title)
	if title != "" {
		return Slugify(title)
	}
	return "doc-" + shortID(entry.ID)
}

func shortID(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])[:8]
}

func containsNonASCII(value string) bool {
	for _, r := range value {
		if r > 0x7f {
			return true
		}
	}
	return false
}

func (tree *knowledgeTree) assignPaths(guard *PathGuard, warnings *[]string) error {
	rootIdentity := "root:" + tree.root.entry.ID
	rel, _, err := guard.Allocate(path.Join(tree.base, "index.md"), rootIdentity)
	if err != nil {
		return err
	}
	tree.root.rel = rel
	tree.root.dirRel = tree.base
	tree.root.isDir = true
	if err := tree.assignChildren(tree.root, guard, warnings); err != nil {
		return err
	}
	return nil
}

// managedGlobalCategories are the durable AHA2 global category nodes. They
// always become folders, even when they currently have no children, so the
// existing three-level global tree survives an export.
var managedGlobalCategories = map[string]bool{
	"knowledge_global_general":                 true,
	"knowledge_global_agent_lessons":           true,
	"knowledge_global_agent_lessons_technical": true,
	"knowledge_global_agent_lessons_behavior":  true,
}

func (tree *knowledgeTree) assignChildren(node *knowledgeNode, guard *PathGuard, warnings *[]string) error {
	for _, child := range node.children {
		segment := SafeSegment(documentSlug(child.entry), "doc-"+shortID(child.entry.ID))
		forceDirectory := child.entry.Scope == "global" && managedGlobalCategories[child.entry.ID]
		child.isDir = len(child.children) > 0 || forceDirectory
		if child.isDir {
			dirRel, changed, err := guard.Allocate(path.Join(node.dirRel, segment, "index.md"), "dir:"+child.entry.ID)
			if err != nil {
				return err
			}
			if changed {
				*warnings = append(*warnings, fmt.Sprintf("case-insensitive path collision for %s; published as %s", child.entry.ID, dirRel))
			}
			child.rel = dirRel
			child.dirRel = path.Dir(dirRel)
		} else {
			directory := node.dirRel
			if node == tree.root {
				directory = path.Join(tree.base, "inbox")
			}
			fileRel, changed, err := guard.Allocate(path.Join(directory, segment+".md"), "file:"+child.entry.ID)
			if err != nil {
				return err
			}
			if changed {
				*warnings = append(*warnings, fmt.Sprintf("case-insensitive path collision for %s; published as %s", child.entry.ID, fileRel))
			}
			child.rel = fileRel
		}
		if err := tree.assignChildren(child, guard, warnings); err != nil {
			return err
		}
	}
	return nil
}

func (tree *knowledgeTree) walk(visitor func(*knowledgeNode)) {
	var visit func(*knowledgeNode)
	visit = func(node *knowledgeNode) {
		visitor(node)
		for _, child := range node.children {
			visit(child)
		}
	}
	visit(tree.root)
}

func entryFrontmatter(entry sourceEntry, scope string, isIndex bool, archive bool, pathHint string) Frontmatter {
	canonical, legacy := NormalizeType(entry.Type, pathHint)
	if entry.Scope == "project" && archive {
		scope = "archive"
	}
	frontmatter := Frontmatter{
		ID: entry.ID, Title: entry.Title, Slug: documentSlug(entry), Scope: scope,
		ProjectID: entry.ProjectID, Type: canonical, LegacyType: legacy, Status: entry.Status,
		Parent: entry.ParentID, Created: formatFrontmatterTime(entry.CreatedAt), Updated: formatFrontmatterTime(entry.UpdatedAt),
		SourceTask: entry.SourceTaskID, SourceTurn: entry.SourceTurnID,
		SortOrder: intPointer(entry.SortOrder), IsIndex: boolPointer(isIndex),
	}
	if entry.Confidence != 0 {
		frontmatter.Confidence = floatPointer(entry.Confidence)
	}
	if archive {
		frontmatter.Archive = boolPointer(true)
	}
	return frontmatter
}

func formatFrontmatterTime(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return value
	}
	return parsed.UTC().Format(time.RFC3339)
}

func generatedIndexBlock(node *knowledgeNode) string {
	lines := []string{indexBlockStart, "## 文档", ""}
	if len(node.children) == 0 {
		lines = append(lines, "_暂无文档。_")
	}
	for _, child := range node.children {
		target := strings.TrimSuffix(filepath.ToSlash(child.rel), ".md")
		target = strings.ReplaceAll(target, "|", "-")
		title := strings.ReplaceAll(strings.TrimSpace(child.entry.Title), "|", "-")
		title = strings.ReplaceAll(title, "]]", "]")
		summary := IndexSummary(child.entry.Body, 120)
		line := fmt.Sprintf("- [[%s|%s]]", target, title)
		if summary != "" {
			line += " — " + summary
		}
		lines = append(lines, line)
	}
	lines = append(lines, indexBlockEnd)
	return strings.Join(lines, "\n")
}

// StripGeneratedIndex removes the exporter-generated navigation block from a
// document body. Rebuilds use it so the stored body stays identical across
// repeated export/index cycles.
func StripGeneratedIndex(body string) string {
	for {
		start := strings.Index(body, indexBlockStart)
		if start < 0 {
			break
		}
		end := strings.Index(body[start:], indexBlockEnd)
		if end < 0 {
			break
		}
		end = start + end + len(indexBlockEnd)
		body = strings.TrimRight(body[:start], "\n \t") + "\n" + strings.TrimLeft(body[end:], "\n")
	}
	return strings.TrimRight(body, "\n \t")
}

func renderEntryDocument(node *knowledgeNode, scope string, isIndex bool, archive bool) (string, error) {
	frontmatter := entryFrontmatter(node.entry, scope, isIndex, archive, node.rel)
	body := StripGeneratedIndex(node.entry.Body)
	if node.isDir {
		block := generatedIndexBlock(node)
		if body == "" {
			body = block
		} else {
			body = body + "\n\n" + block
		}
	}
	return renderDocument(frontmatter, body)
}

func (t *knowledgeTree) allNodes() []*knowledgeNode {
	result := []*knowledgeNode{}
	t.walk(func(node *knowledgeNode) { result = append(result, node) })
	return result
}

// Export writes the current database knowledge into the staging vault. The
// database is opened read-only; only vault files, its manifest, and the local
// Git repository are written.
func Export(ctx context.Context, cfg Config, databasePath string, options ExportOptions) (ExportReport, error) {
	cfg, err := cfg.Normalized()
	if err != nil {
		return ExportReport{}, err
	}
	report := ExportReport{VaultPath: cfg.VaultPath, DatabasePath: databasePath}
	source, err := loadSource(ctx, databasePath)
	if err != nil {
		return report, err
	}
	if _, err := Bootstrap(cfg, InitOptions{}); err != nil {
		return report, err
	}
	manifest, err := ReadManifest(cfg.VaultPath)
	if err != nil {
		return report, err
	}
	originalManifest := manifest
	now := time.Now().UTC()
	if options.Now != nil {
		now = options.Now().UTC()
	}

	mainProjects := []sourceProject{}
	archiveProjects := []sourceProject{}
	for _, project := range source.Projects {
		if project.ProjectType == "knowledge" {
			archiveProjects = append(archiveProjects, project)
		} else {
			mainProjects = append(mainProjects, project)
		}
	}
	reservedSlugs := map[string]bool{"index": true, "inbox": true, "skills": true}
	mainSlugs := allocateProjectSlugs(mainProjects, manifest.Projects, manifest.ProjectNames, reservedSlugs, &report.Warnings)
	archiveSlugs := allocateProjectSlugs(archiveProjects, manifest.Archives, manifest.ArchiveNames, reservedSlugs, &report.Warnings)
	manifest.Projects = mainSlugs.byID
	manifest.ProjectNames = mainSlugs.names
	manifest.Archives = archiveSlugs.byID
	manifest.ArchiveNames = archiveSlugs.names

	guard := NewPathGuard(cfg.VaultPath)
	for _, relative := range []string{manifestFileName, readmeFileName, ".gitignore", ".trash", ".aha", ".git", ".obsidian", "skills"} {
		guard.Reserved(relative, "reserved")
	}

	documents := map[string][]byte{}
	groupEntries := map[string][]sourceEntry{}
	for _, entry := range source.Entries {
		key := entry.Scope + "\x00" + entry.ProjectID
		groupEntries[key] = append(groupEntries[key], entry)
	}

	writeTree := func(scope, projectID, base string, archive bool) error {
		entries := groupEntries[scope+"\x00"+projectID]
		if len(entries) == 0 {
			return nil
		}
		if scope == "project" && archive {
			scope = "archive"
		}
		tree := buildTree(entries, base, &report.Warnings)
		if err := tree.assignPaths(guard, &report.Warnings); err != nil {
			return err
		}
		for _, node := range tree.allNodes() {
			isRoot := node == tree.root
			content, err := renderEntryDocument(node, node.entry.Scope, isRoot || node.entry.IsIndex, archive)
			if err != nil {
				return err
			}
			documents[filepath.ToSlash(node.rel)] = []byte(content)
			if node.isDir && node != tree.root {
				report.Groups++
			}
			if node.parent == tree.root && !node.isDir && node.entry.Scope == "project" && !archive {
				report.ProjectInbox++
			}
		}
		switch nodeScope := tree.root.entry.Scope; nodeScope {
		case "global":
			report.Global += len(entries)
		case "project":
			if archive {
				report.Archives += len(entries)
			} else {
				report.Projects += len(entries)
			}
		default:
			report.Warnings = append(report.Warnings, fmt.Sprintf("unknown scope %q in %s", nodeScope, base))
		}
		return nil
	}

	globalEntries := groupEntries["global\x00"]
	if len(globalEntries) == 0 {
		// Bootstrap already wrote a placeholder root. Leave it in place.
	} else if err := writeTree("global", "", "global", false); err != nil {
		return report, err
	}
	for _, project := range mainProjects {
		if len(groupEntries["project\x00"+project.ID]) == 0 {
			continue
		}
		base := filepath.ToSlash(filepath.Join("projects", mainSlugs.byID[project.ID]))
		for _, directory := range []string{path.Join(base, "inbox"), path.Join(base, "skills")} {
			guard.Reserved(path.Join(directory, ".gitkeep"), "reserved")
			documents[path.Join(directory, ".gitkeep")] = []byte{}
		}
		if err := writeTree("project", project.ID, base, false); err != nil {
			return report, err
		}
	}
	for _, project := range archiveProjects {
		if len(groupEntries["project\x00"+project.ID]) == 0 {
			continue
		}
		base := filepath.ToSlash(filepath.Join("archive", archiveSlugs.byID[project.ID]))
		for _, directory := range []string{path.Join(base, "inbox"), path.Join(base, "skills")} {
			guard.Reserved(path.Join(directory, ".gitkeep"), "reserved")
			documents[path.Join(directory, ".gitkeep")] = []byte{}
		}
		if err := writeTree("project", project.ID, base, true); err != nil {
			return report, err
		}
	}

	report.Total = report.Global + report.Projects + report.Archives
	report.Written, report.Unchanged, err = writeDocuments(cfg.VaultPath, documents)
	if err != nil {
		return report, err
	}
	report.Removed, err = pruneExportedPaths(cfg.VaultPath, documents)
	if err != nil {
		return report, err
	}
	pruneEmptyDirectories(cfg.VaultPath, map[string]bool{"global": true, "projects": true, "archive": true})

	summary := &ExportSummary{
		Total: report.Total, Global: report.Global, Projects: report.Projects,
		Archives: report.Archives, ProjectInbox: report.ProjectInbox, Groups: report.Groups,
		ExportedAt: now.Format(time.RFC3339),
	}
	if manifest.Export != nil && sameExportSummary(*manifest.Export, *summary) {
		summary.ExportedAt = manifest.Export.ExportedAt
	}
	manifest.Export = summary
	manifest.UpdatedAt = now.Format(time.RFC3339)
	if report.Written == 0 && report.Removed == 0 && manifestsEquivalent(originalManifest, manifest) {
		manifest = originalManifest
	}
	if err := writeManifest(cfg.VaultPath, manifest); err != nil {
		return report, err
	}
	if err := writeExportState(cfg.VaultPath, documents, now); err != nil {
		return report, err
	}
	if options.Commit {
		warning, committed := commitVault(cfg.VaultPath, "Export AHA2 knowledge into vault")
		report.GitWarning = warning
		report.Committed = committed
	}
	return report, nil
}

func sameExportSummary(left, right ExportSummary) bool {
	return left.Total == right.Total && left.Global == right.Global &&
		left.Projects == right.Projects && left.Archives == right.Archives &&
		left.ProjectInbox == right.ProjectInbox && left.Groups == right.Groups
}

func writeDocuments(vaultPath string, documents map[string][]byte) (int, int, error) {
	written, unchanged := 0, 0
	paths := make([]string, 0, len(documents))
	for relative := range documents {
		paths = append(paths, relative)
	}
	sort.Strings(paths)
	for _, relative := range paths {
		if err := ValidateRelativePath(relative); err != nil {
			return written, unchanged, err
		}
		absolute := filepath.Join(vaultPath, filepath.FromSlash(relative))
		if !IsInsideRoot(vaultPath, absolute) && fileExists(absolute) {
			return written, unchanged, fmt.Errorf("refusing to write outside vault root: %s", relative)
		}
		existing, err := os.ReadFile(absolute)
		if err == nil && string(existing) == string(documents[relative]) {
			unchanged++
			continue
		}
		if err != nil && !os.IsNotExist(err) {
			return written, unchanged, err
		}
		if err := writeVaultFileIfChanged(vaultPath, absolute, documents[relative], 0o644); err != nil {
			return written, unchanged, err
		}
		written++
	}
	return written, unchanged, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

type exportState struct {
	Version    int      `json:"version"`
	ExportedAt string   `json:"exported_at"`
	Paths      []string `json:"paths"`
}

func writeExportState(vaultPath string, documents map[string][]byte, now time.Time) error {
	paths := make([]string, 0, len(documents))
	for relative := range documents {
		paths = append(paths, relative)
	}
	sort.Strings(paths)
	state := exportState{Version: 1, ExportedAt: now.Format(time.RFC3339), Paths: paths}
	encoded, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	return writeVaultFileIfChanged(vaultPath, filepath.Join(vaultPath, stateDirectory, exportStateFile), encoded, 0o644)
}

func readExportState(vaultPath string) exportState {
	data, err := os.ReadFile(filepath.Join(vaultPath, stateDirectory, exportStateFile))
	if err != nil {
		return exportState{}
	}
	var state exportState
	if json.Unmarshal(data, &state) != nil {
		return exportState{}
	}
	return state
}

func pruneExportedPaths(vaultPath string, documents map[string][]byte) (int, error) {
	previous := readExportState(vaultPath)
	removed := 0
	for _, relative := range previous.Paths {
		if _, stillPresent := documents[relative]; stillPresent {
			continue
		}
		if err := ValidateRelativePath(relative); err != nil {
			continue
		}
		absolute := filepath.Join(vaultPath, filepath.FromSlash(relative))
		if !IsInsideRoot(vaultPath, absolute) {
			continue
		}
		if err := os.Remove(absolute); err == nil {
			removed++
		} else if !os.IsNotExist(err) {
			return removed, err
		}
	}
	return removed, nil
}

func pruneEmptyDirectories(vaultPath string, keep map[string]bool) {
	var visit func(string)
	visit = func(directory string) {
		entries, err := os.ReadDir(directory)
		if err != nil {
			return
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name := entry.Name()
			if reservedVaultNames[strings.ToLower(name)] {
				continue
			}
			visit(filepath.Join(directory, name))
		}
		relative, err := filepath.Rel(vaultPath, directory)
		if err != nil || relative == "." {
			return
		}
		relative = filepath.ToSlash(relative)
		if keep[relative] {
			return
		}
		remaining, err := os.ReadDir(directory)
		if err != nil || len(remaining) > 0 {
			return
		}
		_ = os.Remove(directory)
	}
	visit(vaultPath)
}
