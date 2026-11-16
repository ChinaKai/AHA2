package knowledgevault

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// deriveManifest rebuilds the project/archive/skill maps from the vault layout.
// The vault is the source of truth: a directory without an index.md binding is
// reported as unbound instead of silently inheriting stale manifest data. The
// function only reads knowledge and skill files.
func deriveManifest(vaultPath string, manifest Manifest, now time.Time) (Manifest, []string, error) {
	updated := manifest
	projects, projectNames, archives, archiveNames, warnings, err := deriveLibraries(vaultPath, manifest)
	if err != nil {
		return manifest, warnings, err
	}
	skills, skillWarnings, err := deriveSkills(vaultPath, projects, archives)
	if err != nil {
		return manifest, append(warnings, skillWarnings...), err
	}
	warnings = append(warnings, skillWarnings...)

	updated.Projects = projects
	updated.ProjectNames = projectNames
	updated.Archives = archives
	updated.ArchiveNames = archiveNames
	updated.Skills = skills
	if manifestsEquivalent(manifest, updated) {
		return manifest, warnings, nil
	}
	updated.UpdatedAt = now.UTC().Format(time.RFC3339)
	if err := writeManifest(vaultPath, updated); err != nil {
		return manifest, warnings, fmt.Errorf("write vault manifest: %w", err)
	}
	return updated, warnings, nil
}

func deriveLibraries(vaultPath string, manifest Manifest) (map[string]string, map[string]string, map[string]string, map[string]string, []string, error) {
	projects := map[string]string{}
	projectNames := map[string]string{}
	archives := map[string]string{}
	archiveNames := map[string]string{}
	warnings := []string{}
	for _, root := range []struct {
		directory string
		byID      map[string]string
		names     map[string]string
		existing  map[string]string
		kind      string
	}{
		{directory: "projects", byID: projects, names: projectNames, existing: manifest.ProjectNames, kind: "project"},
		{directory: "archive", byID: archives, names: archiveNames, existing: manifest.ArchiveNames, kind: "archive"},
	} {
		children, err := os.ReadDir(filepath.Join(vaultPath, filepath.FromSlash(root.directory)))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, nil, nil, nil, warnings, fmt.Errorf("read %s: %w", root.directory, err)
		}
		sort.Slice(children, func(left, right int) bool { return children[left].Name() < children[right].Name() })
		usedSlugs := map[string]string{}
		bind := func(relative, slug string) {
			indexPath := filepath.Join(vaultPath, filepath.FromSlash(path.Join(relative, "index.md")))
			raw, readErr := os.ReadFile(indexPath)
			if readErr != nil {
				if os.IsNotExist(readErr) {
					warnings = append(warnings, fmt.Sprintf("%s: unbound (%s/index.md is missing); no %s mapping was generated", relative, relative, root.kind))
				} else {
					warnings = append(warnings, fmt.Sprintf("%s: unbound (read index.md: %v); no %s mapping was generated", relative, readErr, root.kind))
				}
				return
			}
			document := ParseDocument(string(raw))
			projectID := strings.TrimSpace(document.Frontmatter.ProjectID)
			if projectID == "" {
				warnings = append(warnings, fmt.Sprintf("%s: unbound (index.md frontmatter project_id is missing); no %s mapping was generated", relative, root.kind))
				return
			}
			if other, exists := usedSlugs[projectID]; exists {
				warnings = append(warnings, fmt.Sprintf("%s: duplicate %s binding %q already used by %s; mapping skipped", relative, root.kind, projectID, other))
				return
			}
			usedSlugs[projectID] = relative
			root.byID[projectID] = slug
			title := strings.TrimSpace(document.Frontmatter.Title)
			if title == "" {
				title = FirstHeading(document.Body)
			}
			root.names[projectID] = resolveLibraryName(root.existing[projectID], title, slug, projectID)
		}
		for _, child := range children {
			slug := child.Name()
			if !child.IsDir() || strings.HasPrefix(slug, ".") {
				continue
			}
			if root.directory == "archive" && slug == "projects" {
				// Archived project spaces: archive/projects/<slug-id8-ts>/.
				// The container directory has no index.md of its own, so it
				// must not be reported as an unbound archive library. A
				// hand-authored flat archive that happens to use this slug
				// keeps its mapping when it has its own root page.
				if _, statErr := os.Stat(filepath.Join(vaultPath, "archive", "projects", "index.md")); statErr == nil {
					bind(path.Join("archive", "projects"), "projects")
				}
				nested, nestedErr := os.ReadDir(filepath.Join(vaultPath, "archive", "projects"))
				if nestedErr != nil {
					if !os.IsNotExist(nestedErr) {
						warnings = append(warnings, fmt.Sprintf("archive/projects: read archived spaces: %v", nestedErr))
					}
					continue
				}
				sort.Slice(nested, func(left, right int) bool { return nested[left].Name() < nested[right].Name() })
				for _, item := range nested {
					if !item.IsDir() || strings.HasPrefix(item.Name(), ".") {
						continue
					}
					bind(path.Join("archive", "projects", item.Name()), path.Join("projects", item.Name()))
				}
				continue
			}
			bind(path.Join(root.directory, slug), slug)
		}
	}
	return projects, projectNames, archives, archiveNames, warnings, nil
}

// knowledgeHomeTitles are the generated navigation titles for the global and
// project knowledge roots. They name the generated index page, not the owning
// project, so they must never be stored as a project or archive display name.
var knowledgeHomeTitles = map[string]bool{
	"知识首页":   true,
	"全局知识首页": true,
}

// resolveLibraryName picks the display name for one project/archive mapping.
// A previously exported name (from the AHA2 projects table) wins because the
// vault itself only stores a slug. The root index title is a last-resort
// fallback for hand-authored vaults; the generated "knowledge home" titles are
// rejected so the old bug cannot write them back. Without any usable name the
// stable slug is the honest fallback.
func resolveLibraryName(existing, title, slug, id string) string {
	existing = strings.TrimSpace(existing)
	title = strings.TrimSpace(title)
	if existing != "" && !isKnowledgeHomeTitle(existing) && existing != title {
		return existing
	}
	if title != "" && !isKnowledgeHomeTitle(title) {
		return title
	}
	if slug = strings.TrimSpace(slug); slug != "" {
		return slug
	}
	return id
}

func isKnowledgeHomeTitle(value string) bool {
	return knowledgeHomeTitles[strings.TrimSpace(value)]
}

func deriveSkills(vaultPath string, projects, archives map[string]string) (map[string]SkillLocation, []string, error) {
	type candidate struct {
		slug     string
		relative string
		location SkillLocation
	}
	candidates := []candidate{}
	addDirectory := func(relative, slug string, location SkillLocation) {
		if strings.HasPrefix(slug, ".") {
			return
		}
		if info, err := os.Stat(filepath.Join(vaultPath, filepath.FromSlash(relative), "SKILL.md")); err != nil || info.IsDir() {
			return
		}
		candidates = append(candidates, candidate{slug: slug, relative: relative, location: location})
	}
	scanSkillRoot := func(relative, scope, projectID string) {
		children, err := os.ReadDir(filepath.Join(vaultPath, filepath.FromSlash(relative)))
		if err != nil {
			return
		}
		for _, child := range children {
			if !child.IsDir() {
				continue
			}
			addDirectory(path.Join(relative, child.Name()), child.Name(), SkillLocation{Scope: scope, ProjectID: projectID})
		}
	}
	scanSkillRoot("skills", "global", "")
	scanSkillRoot("global/skills", "global", "")
	for projectID, slug := range projects {
		scanSkillRoot(path.Join("projects", slug, "skills"), "project", projectID)
	}
	for projectID, slug := range archives {
		scanSkillRoot(path.Join("archive", slug, "skills"), "archive", projectID)
	}
	sort.Slice(candidates, func(left, right int) bool {
		if candidates[left].slug != candidates[right].slug {
			return candidates[left].slug < candidates[right].slug
		}
		return candidates[left].relative < candidates[right].relative
	})
	result := map[string]SkillLocation{}
	warnings := []string{}
	for _, item := range candidates {
		if _, exists := result[item.slug]; exists {
			warnings = append(warnings, fmt.Sprintf("%s: duplicate skill slug %q; keeping the first sorted location", item.relative, item.slug))
			continue
		}
		result[item.slug] = item.location
	}
	return result, warnings, nil
}

// projectsRootIndexPath is the container index for the whole projects tree.
// It is a vault navigation file, not a project document: the indexer skips it
// (its path has no project slug), and maintainProjectsRootIndex keeps its
// generated block in sync with the project directories.
const projectsRootIndexPath = "projects/index.md"

// missingIndexSkipNames are directory names that never receive an automatic
// index.md. They are reserved vault areas or directories the knowledge tree
// deliberately keeps out of the generated navigation.
var missingIndexSkipNames = map[string]bool{
	"inbox": true, "skills": true, "index": true,
	".trash": true, ".aha": true, ".git": true, ".obsidian": true,
	"archive": true, "orphans": true,
}

// createMissingIndexPages creates a canonical index.md for every directory
// under global/ and projects/<slug>/ that holds knowledge documents but has no
// index page yet. Existing pages are never touched, reserved directories are
// skipped, and any failure is reported as a warning so a broken page cannot
// abort the rebuild. The generated navigation block stays empty here;
// maintainVaultIndexes fills it from the records scanned by this rebuild.
func createMissingIndexPages(vaultPath string, manifest Manifest) []string {
	warnings := []string{}
	directoriesWithDocuments := map[string]bool{}
	walkErr := filepath.WalkDir(vaultPath, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			// The main scan reports walk errors; page creation is best-effort.
			return nil
		}
		relative, err := filepath.Rel(vaultPath, filePath)
		if err != nil || relative == "." {
			return nil
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			name := entry.Name()
			if strings.HasPrefix(name, ".") || missingIndexSkipNames[strings.ToLower(name)] {
				return fs.SkipDir
			}
			if !autoIndexDirectoryAllowed(relative) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(entry.Name()), ".md") || relative == readmeFileName {
			return nil
		}
		directory := path.Dir(relative)
		// Documents directly under global/ count too; projects/ itself is only
		// the container for project directories, not a knowledge directory.
		if directory != "projects" && autoIndexDirectoryAllowed(directory) {
			directoriesWithDocuments[directory] = true
		}
		return nil
	})
	if walkErr != nil {
		return append(warnings, fmt.Sprintf("scan for missing index pages: %v", walkErr))
	}

	// A directory that only contains subdirectories needs an index page too so
	// the parent can link to it; walk the ancestors of each document directory.
	candidates := map[string]bool{}
	for directory := range directoriesWithDocuments {
		for directory != "" && directory != "." && directory != "projects" {
			if !autoIndexDirectoryAllowed(directory) {
				break
			}
			candidates[directory] = true
			parent := path.Dir(directory)
			if parent == directory {
				break
			}
			directory = parent
		}
	}
	paths := make([]string, 0, len(candidates))
	for directory := range candidates {
		paths = append(paths, directory)
	}
	sort.Strings(paths)

	reverseProjects := reverseSlugMap(manifest.Projects)
	for _, directory := range paths {
		relative := path.Join(directory, "index.md")
		target := filepath.Join(vaultPath, filepath.FromSlash(relative))
		if _, err := os.Lstat(target); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			warnings = append(warnings, fmt.Sprintf("%s: inspect missing index page: %v", relative, err))
			continue
		}
		frontmatter, warning, ok := missingIndexFrontmatter(vaultPath, manifest, reverseProjects, directory)
		if warning != "" {
			warnings = append(warnings, warning)
		}
		if !ok {
			continue
		}
		if err := createVaultIndexPage(vaultPath, relative, frontmatter); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: create missing index page: %v", relative, err))
		}
	}
	return warnings
}

// maintainProjectsRootIndex creates or refreshes projects/index.md. Clicking
// projects in a file browser or Obsidian should land on a page listing every
// project entry, just like global/index.md. The page lives outside the indexed
// document set because it has no single project_id; only the marker block is
// machine-written, so a hand-written intro or frontmatter survives rebuilds.
func maintainProjectsRootIndex(vaultPath string, manifest Manifest) []string {
	warnings := []string{}
	projectsRoot := filepath.Join(vaultPath, "projects")
	info, err := os.Lstat(projectsRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return warnings
		}
		return append(warnings, fmt.Sprintf("projects: inspect root: %v", err))
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return warnings
	}
	children, err := os.ReadDir(projectsRoot)
	if err != nil {
		return append(warnings, fmt.Sprintf("projects: read root: %v", err))
	}
	nameBySlug := map[string]string{}
	for projectID, slug := range manifest.Projects {
		if slug != "" {
			nameBySlug[slug] = manifest.ProjectNames[projectID]
		}
	}
	slugs := make([]string, 0, len(children))
	for _, child := range children {
		slug := child.Name()
		if !child.IsDir() || strings.HasPrefix(slug, ".") || child.Type()&os.ModeSymlink != 0 {
			continue
		}
		if _, statErr := os.Lstat(filepath.Join(projectsRoot, slug, "index.md")); statErr != nil {
			continue
		}
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	lines := []string{indexBlockStart, "## 文档", ""}
	if len(slugs) == 0 {
		lines = append(lines, "_暂无文档。_")
	}
	for _, slug := range slugs {
		title := strings.TrimSpace(nameBySlug[slug])
		if title == "" || isKnowledgeHomeTitle(title) {
			title = humanizeSegment(slug)
		}
		lines = append(lines, indexListLine(path.Join("projects", slug, "index"), title, ""))
	}
	lines = append(lines, indexBlockEnd)
	block := strings.Join(lines, "\n")

	target := filepath.Join(projectsRoot, "index.md")
	raw, readErr := os.ReadFile(target)
	if readErr != nil {
		if !os.IsNotExist(readErr) {
			return append(warnings, fmt.Sprintf("%s: read index page: %v", projectsRootIndexPath, readErr))
		}
		frontmatter := Frontmatter{Title: "项目知识", Scope: "global", Type: TypeNavigation, Status: "verified", IsIndex: boolPointer(true)}
		content, renderErr := renderDocument(frontmatter, block)
		if renderErr != nil {
			return append(warnings, fmt.Sprintf("%s: render index page: %v", projectsRootIndexPath, renderErr))
		}
		if writeErr := writeVaultFileIfChanged(vaultPath, target, []byte(content), 0o644); writeErr != nil {
			return append(warnings, fmt.Sprintf("%s: create index page: %v", projectsRootIndexPath, writeErr))
		}
		return warnings
	}
	start := strings.Index(string(raw), indexBlockStart)
	if start < 0 {
		return append(warnings, fmt.Sprintf("%s: generated index markers are missing; page left unchanged", projectsRootIndexPath))
	}
	endRelative := strings.Index(string(raw)[start:], indexBlockEnd)
	if endRelative < 0 {
		return append(warnings, fmt.Sprintf("%s: generated index markers are incomplete; page left unchanged", projectsRootIndexPath))
	}
	end := start + endRelative + len(indexBlockEnd)
	next := string(raw[:start]) + block + string(raw[end:])
	if next == string(raw) {
		return warnings
	}
	if writeErr := writeVaultFileIfChanged(vaultPath, target, []byte(next), 0o644); writeErr != nil {
		warnings = append(warnings, fmt.Sprintf("%s: update generated index: %v", projectsRootIndexPath, writeErr))
	}
	return warnings
}

// autoIndexDirectoryAllowed reports whether a vault-relative directory belongs
// to the global or project knowledge trees. Excluded names are pruned while
// walking, so this only has to recognise the two roots.
func autoIndexDirectoryAllowed(relative string) bool {
	if relative == "global" || strings.HasPrefix(relative, "global/") {
		return true
	}
	if relative == "projects" {
		return true
	}
	if !strings.HasPrefix(relative, "projects/") {
		return false
	}
	slug := strings.SplitN(strings.TrimPrefix(relative, "projects/"), "/", 2)[0]
	return slug != ""
}

// missingIndexFrontmatter builds the canonical page metadata for a missing
// directory index. A project page without a trustworthy project id is skipped:
// guessing would bind documents to the wrong project.
func missingIndexFrontmatter(vaultPath string, manifest Manifest, reverseProjects map[string]string, directory string) (Frontmatter, string, bool) {
	frontmatter := Frontmatter{Type: TypeNavigation, Status: "verified", IsIndex: boolPointer(true)}
	if directory == "global" || strings.HasPrefix(directory, "global/") {
		frontmatter.Scope = "global"
		if directory == "global" {
			frontmatter.Title = "全局知识"
		} else {
			frontmatter.Title = humanizeSegment(path.Base(directory))
		}
		return frontmatter, "", true
	}
	projectSlug := strings.SplitN(strings.TrimPrefix(directory, "projects/"), "/", 2)[0]
	projectID := reverseProjects[projectSlug]
	if projectID == "" {
		projectID = inferProjectIDFromDocuments(vaultPath, path.Join("projects", projectSlug))
	}
	if projectID == "" {
		return Frontmatter{}, fmt.Sprintf("projects/%s: index.md is missing and the project id cannot be determined; page not created", projectSlug), false
	}
	frontmatter.Scope = "project"
	frontmatter.ProjectID = projectID
	if directory == path.Join("projects", projectSlug) {
		frontmatter.Title = strings.TrimSpace(manifest.ProjectNames[projectID])
		if frontmatter.Title == "" || isKnowledgeHomeTitle(frontmatter.Title) {
			frontmatter.Title = humanizeSegment(projectSlug)
		}
	} else {
		frontmatter.Title = humanizeSegment(path.Base(directory))
	}
	return frontmatter, "", true
}

// inferProjectIDFromDocuments recovers a project binding from the documents
// that already live in the project directory. Exported vaults always carry
// project_id in frontmatter, so an old vault whose index.md was lost can still
// be repaired without guessing.
func inferProjectIDFromDocuments(vaultPath, projectRoot string) string {
	counts := map[string]int{}
	_ = filepath.WalkDir(filepath.Join(vaultPath, filepath.FromSlash(projectRoot)), func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
			return nil
		}
		raw, err := os.ReadFile(filePath)
		if err != nil {
			return nil
		}
		projectID := strings.TrimSpace(ParseDocument(string(raw)).Frontmatter.ProjectID)
		if projectID != "" {
			counts[projectID]++
		}
		return nil
	})
	best := ""
	for projectID, count := range counts {
		if best == "" || count > counts[best] || (count == counts[best] && projectID < best) {
			best = projectID
		}
	}
	return best
}

// createVaultIndexPage writes one canonical index page. O_EXCL plus the safe
// target check make this a create-only operation: a hand-written page, a
// concurrent writer, or a symlink can never be overwritten.
func createVaultIndexPage(vaultPath, relative string, frontmatter Frontmatter) error {
	target := filepath.Join(vaultPath, filepath.FromSlash(relative))
	if _, err := os.Lstat(target); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := ensureSafeWriteTarget(vaultPath, target); err != nil {
		return err
	}
	content, err := renderDocument(frontmatter, indexBlockStart+"\n"+indexBlockEnd)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return nil
		}
		return err
	}
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		_ = os.Remove(target)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(target)
		return err
	}
	return nil
}

// maintainVaultIndexes rewrites only the machine-generated block inside
// existing index.md files. It never creates a page and never touches content
// outside the markers, including frontmatter and hand-written intros.
func maintainVaultIndexes(vaultPath string, records []*scannedRecord) []string {
	warnings := []string{}
	byPath := make(map[string]*scannedRecord, len(records))
	directories := map[string]bool{}
	for _, record := range records {
		byPath[record.entry.Path] = record
		directory := path.Dir(record.entry.Path)
		for directory != "." && directory != "/" {
			directories[directory] = true
			parent := path.Dir(directory)
			if parent == directory {
				break
			}
			directory = parent
		}
	}
	indexPaths := make([]string, 0)
	for _, record := range records {
		if path.Base(record.entry.Path) == "index.md" && !isReservedVaultPath(record.entry.Path) {
			indexPaths = append(indexPaths, record.entry.Path)
		}
	}
	sort.Strings(indexPaths)
	for _, indexPath := range indexPaths {
		directory := path.Dir(indexPath)
		subdirectories := map[string]string{}
		for candidate := range directories {
			if path.Dir(candidate) != directory {
				continue
			}
			indexRelative := path.Join(candidate, "index.md")
			entry, ok := byPath[indexRelative]
			if !ok {
				continue
			}
			subdirectories[candidate] = entry.entry.Title
		}
		subdirPaths := make([]string, 0, len(subdirectories))
		for candidate := range subdirectories {
			subdirPaths = append(subdirPaths, candidate)
		}
		sort.Strings(subdirPaths)
		docPaths := make([]string, 0)
		for relative := range byPath {
			if path.Dir(relative) != directory || path.Base(relative) == "index.md" {
				continue
			}
			if isReservedVaultPath(relative) {
				continue
			}
			docPaths = append(docPaths, relative)
		}
		sort.Strings(docPaths)
		block := generatedIndexBlockForEntries(byPath, subdirPaths, docPaths)
		target := filepath.Join(vaultPath, filepath.FromSlash(indexPath))
		raw, err := os.ReadFile(target)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: read index page: %v", indexPath, err))
			continue
		}
		start := strings.Index(string(raw), indexBlockStart)
		if start < 0 {
			warnings = append(warnings, fmt.Sprintf("%s: generated index markers are missing; page left unchanged", indexPath))
			continue
		}
		endRelative := strings.Index(string(raw)[start:], indexBlockEnd)
		if endRelative < 0 {
			warnings = append(warnings, fmt.Sprintf("%s: generated index markers are incomplete; page left unchanged", indexPath))
			continue
		}
		end := start + endRelative + len(indexBlockEnd)
		next := string(raw[:start]) + block + string(raw[end:])
		if next == string(raw) {
			continue
		}
		if err := writeVaultFileIfChanged(vaultPath, target, []byte(next), 0o644); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: update generated index: %v", indexPath, err))
		}
	}
	return warnings
}

func generatedIndexBlockForEntries(byPath map[string]*scannedRecord, subdirectories, documents []string) string {
	lines := []string{indexBlockStart, "## 文档", ""}
	if len(subdirectories) == 0 && len(documents) == 0 {
		lines = append(lines, "_暂无文档。_")
	}
	for _, relative := range subdirectories {
		entry := byPath[path.Join(relative, "index.md")]
		target := strings.TrimSuffix(relative, ".md") + "/index"
		// Directory pages keep the generated block in their indexed body, but
		// the summary must ignore it or the parent block would change on every
		// rebuild.
		lines = append(lines, indexListLine(target, entry.entry.Title, IndexSummary(StripGeneratedIndex(entry.entry.Body), 120)))
	}
	for _, relative := range documents {
		entry := byPath[relative]
		lines = append(lines, indexListLine(strings.TrimSuffix(relative, ".md"), entry.entry.Title, IndexSummary(entry.entry.Body, 120)))
	}
	lines = append(lines, indexBlockEnd)
	return strings.Join(lines, "\n")
}

func indexListLine(target, title, summary string) string {
	target = strings.ReplaceAll(target, "|", "-")
	title = strings.ReplaceAll(strings.TrimSpace(title), "|", "-")
	title = strings.ReplaceAll(title, "]]", "]")
	line := fmt.Sprintf("- [[%s|%s]]", target, title)
	if summary != "" {
		line += " — " + summary
	}
	return line
}
