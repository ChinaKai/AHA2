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
				// archive/projects is the container for those spaces; its
				// generated index.md is directory navigation, never a flat
				// archive library binding. A hand-authored page that declares
				// its own project_id keeps the historical flat-library binding.
				if indexPageProjectID(vaultPath, path.Join("archive", "projects", "index.md")) != "" {
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

// Container index pages for the projects and archive trees. They are vault
// navigation, not project documents: the indexer skips them (they have no
// single scope/project), and their dedicated maintain functions keep the
// generated block in sync with the library directories.
const (
	projectsRootIndexPath    = "projects/index.md"
	archiveRootIndexPath     = "archive/index.md"
	archiveProjectsIndexPath = "archive/projects/index.md"
)

// vaultIndexContainers are directories whose index.md is a container
// navigation page maintained by a dedicated function instead of the generic
// directory backfill.
var vaultIndexContainers = map[string]bool{
	"projects":         true,
	"archive":          true,
	"archive/projects": true,
}

// isContainerVaultIndexPath reports whether a vault-relative path is one of
// the generated container navigation pages. archive/projects/index.md is a
// container only when it has no project binding: a hand-authored flat archive
// named "projects" keeps its page and stays indexed as an archive library.
func isContainerVaultIndexPath(vaultPath, relative string) bool {
	switch filepath.ToSlash(relative) {
	case projectsRootIndexPath, archiveRootIndexPath:
		return true
	case archiveProjectsIndexPath:
		return indexPageProjectID(vaultPath, relative) == ""
	}
	// A root library's own navigation page is generated on disk but stays out
	// of the index and search; Tree synthesizes a tree-only node for it.
	segments := strings.Split(filepath.ToSlash(relative), "/")
	if len(segments) == 2 && segments[1] == "index.md" {
		return isRootLibraryDirectory(vaultPath, segments[0])
	}
	return false
}

// missingIndexSkipNames are directory names that never receive an automatic
// index.md. They are reserved vault areas or directories the knowledge tree
// deliberately keeps out of the generated navigation. "archive" is reserved
// inside a project or global subtree; the top-level archive tree is recognized
// by path and exempted by the walker.
var missingIndexSkipNames = map[string]bool{
	"inbox": true, "skills": true, "index": true,
	".trash": true, ".aha": true, ".git": true, ".obsidian": true,
	"archive": true, "orphans": true,
}

// createMissingIndexPages creates a canonical index.md for every directory in
// the knowledge tree (global/, projects/<slug>/ and archive/...), including
// empty directories with no documents. Existing pages are never touched,
// reserved directories are skipped, and any failure is reported as a warning
// so a broken page cannot abort the rebuild. Container pages (projects/,
// archive/ and archive/projects/) are maintained by their own functions. The
// generated navigation block stays empty here; maintainVaultIndexes fills it
// from the records scanned by this rebuild.
func createMissingIndexPages(vaultPath string, manifest Manifest) []string {
	warnings := []string{}
	reverseProjects := reverseSlugMap(manifest.Projects)
	reverseArchives := reverseSlugMap(manifest.Archives)
	candidates := map[string]bool{}
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
		if !entry.IsDir() {
			return nil
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			return fs.SkipDir
		}
		if missingIndexSkipNames[strings.ToLower(name)] && relative != "archive" {
			return fs.SkipDir
		}
		if !autoIndexDirectoryAllowed(relative) {
			return fs.SkipDir
		}
		if vaultIndexContainers[relative] {
			return nil
		}
		candidates[relative] = true
		return nil
	})
	if walkErr != nil {
		return append(warnings, fmt.Sprintf("scan for missing index pages: %v", walkErr))
	}

	paths := make([]string, 0, len(candidates))
	for directory := range candidates {
		paths = append(paths, directory)
	}
	sort.Strings(paths)

	// A directory tree with no attributable library root cannot be bound to a
	// project; report it once at the root instead of once per subdirectory.
	failedRoots := map[string]bool{}
	for _, directory := range paths {
		relative := path.Join(directory, "index.md")
		target := filepath.Join(vaultPath, filepath.FromSlash(relative))
		if _, err := os.Lstat(target); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			warnings = append(warnings, fmt.Sprintf("%s: inspect missing index page: %v", relative, err))
			continue
		}
		frontmatter, warning, ok := missingIndexFrontmatter(vaultPath, manifest, reverseProjects, reverseArchives, directory)
		root, _, _, hasRoot := autoIndexLibraryRoot(directory)
		if !ok {
			if hasRoot && !failedRoots[root] {
				failedRoots[root] = true
				if warning != "" {
					warnings = append(warnings, warning)
				}
			}
			continue
		}
		// An unattributable library root warns once; its subdirectories inherit
		// the same missing binding and would only repeat the message.
		if warning != "" && (!hasRoot || directory == root) {
			warnings = append(warnings, warning)
		}
		if err := createVaultIndexPage(vaultPath, relative, frontmatter); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: create missing index page: %v", relative, err))
		}
	}
	return warnings
}

// maintainProjectsRootIndex creates or refreshes projects/index.md. Clicking
// projects in a file browser or Obsidian should land on a page listing every
// directory entry, just like global/index.md. The page lives outside the
// indexed document set because it has no single project_id; only the marker
// block is machine-written, so a hand-written intro or frontmatter survives
// rebuilds.
func maintainProjectsRootIndex(vaultPath string, manifest Manifest) []string {
	projectsRoot := filepath.Join(vaultPath, "projects")
	info, err := os.Lstat(projectsRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return []string{fmt.Sprintf("projects: inspect root: %v", err)}
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	children, err := os.ReadDir(projectsRoot)
	if err != nil {
		return []string{fmt.Sprintf("projects: read root: %v", err)}
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
	links := make([]string, 0, len(slugs))
	for _, slug := range slugs {
		title := strings.TrimSpace(nameBySlug[slug])
		if title == "" || isKnowledgeHomeTitle(title) {
			title = humanizeSegment(slug)
		}
		links = append(links, indexListLine(path.Join("projects", slug, "index"), title, ""))
	}
	return writeContainerIndexPage(vaultPath, projectsRootIndexPath, "项目知识", links)
}

// maintainArchiveRootIndex creates or refreshes archive/index.md and, when the
// archive/projects container exists, archive/projects/index.md. Both are
// directory navigation: they list the archived libraries (flat archive/<slug>
// and nested archive/projects/<dir>), only the marker block is machine-written,
// and neither page enters the index.
func maintainArchiveRootIndex(vaultPath string, manifest Manifest) []string {
	archiveRoot := filepath.Join(vaultPath, "archive")
	info, err := os.Lstat(archiveRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return []string{fmt.Sprintf("archive: inspect root: %v", err)}
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	children, err := os.ReadDir(archiveRoot)
	if err != nil {
		return []string{fmt.Sprintf("archive: read root: %v", err)}
	}
	nameBySlug := map[string]string{}
	for projectID, slug := range manifest.Archives {
		if slug != "" {
			nameBySlug[slug] = manifest.ArchiveNames[projectID]
		}
	}
	flat := []string{}
	nested := []string{}
	containerIsLibrary := false
	for _, child := range children {
		slug := child.Name()
		if !child.IsDir() || strings.HasPrefix(slug, ".") || child.Type()&os.ModeSymlink != 0 {
			continue
		}
		if missingIndexSkipNames[strings.ToLower(slug)] && slug != "projects" {
			continue
		}
		if slug == "projects" {
			nested = archiveProjectIndexLinks(vaultPath, nameBySlug)
			if indexPageProjectID(vaultPath, archiveProjectsIndexPath) != "" {
				// A hand-authored flat archive named "projects" keeps its root
				// page; it is listed as a library and its page is not rewritten
				// as the nested container.
				containerIsLibrary = true
				title := strings.TrimSpace(nameBySlug["projects"])
				if title == "" || isKnowledgeHomeTitle(title) {
					title = humanizeSegment("projects")
				}
				flat = append(flat, indexListLine(path.Join("archive", "projects", "index"), title, ""))
			}
			continue
		}
		if _, statErr := os.Lstat(filepath.Join(archiveRoot, slug, "index.md")); statErr != nil {
			continue
		}
		title := strings.TrimSpace(nameBySlug[slug])
		if title == "" || isKnowledgeHomeTitle(title) {
			title = humanizeSegment(slug)
		}
		flat = append(flat, indexListLine(path.Join("archive", slug, "index"), title, ""))
	}
	sort.Strings(flat)
	links := append(append([]string{}, flat...), nested...)
	warnings := writeContainerIndexPage(vaultPath, archiveRootIndexPath, "归档知识", links)
	if !containerIsLibrary {
		if info, statErr := os.Lstat(filepath.Join(archiveRoot, "projects")); statErr == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			warnings = append(warnings, writeContainerIndexPage(vaultPath, archiveProjectsIndexPath, "归档项目", nested)...)
		}
	}
	return warnings
}

// archiveProjectIndexLinks lists the archived project spaces below
// archive/projects. The manifest slug carries both segments ("projects/<dir>"),
// so the display-name cache is keyed the same way.
func archiveProjectIndexLinks(vaultPath string, nameBySlug map[string]string) []string {
	projectsRoot := filepath.Join(vaultPath, "archive", "projects")
	entries, err := os.ReadDir(projectsRoot)
	if err != nil {
		return nil
	}
	links := []string{}
	for _, item := range entries {
		name := item.Name()
		if !item.IsDir() || strings.HasPrefix(name, ".") || item.Type()&os.ModeSymlink != 0 {
			continue
		}
		if missingIndexSkipNames[strings.ToLower(name)] {
			continue
		}
		if _, statErr := os.Lstat(filepath.Join(projectsRoot, name, "index.md")); statErr != nil {
			continue
		}
		title := strings.TrimSpace(nameBySlug[path.Join("projects", name)])
		if title == "" || isKnowledgeHomeTitle(title) {
			title = humanizeSegment(name)
		}
		links = append(links, indexListLine(path.Join("archive", "projects", name, "index"), title, ""))
	}
	sort.Strings(links)
	return links
}

// writeContainerIndexPage creates or refreshes one container navigation page.
// Only the marker block is machine-written, so a hand-written intro or
// frontmatter survives rebuilds. A page without markers is reported and left
// unchanged; a missing parent directory means there is nothing to maintain.
func writeContainerIndexPage(vaultPath, relative, title string, links []string) []string {
	target := filepath.Join(vaultPath, filepath.FromSlash(relative))
	parent := filepath.Dir(target)
	info, err := os.Lstat(parent)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return []string{fmt.Sprintf("%s: inspect container root: %v", relative, err)}
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	lines := []string{indexBlockStart, "## 文档", ""}
	if len(links) == 0 {
		lines = append(lines, "_暂无文档。_")
	}
	lines = append(lines, links...)
	lines = append(lines, indexBlockEnd)
	block := strings.Join(lines, "\n")

	raw, readErr := os.ReadFile(target)
	if readErr != nil {
		if !os.IsNotExist(readErr) {
			return []string{fmt.Sprintf("%s: read index page: %v", relative, readErr)}
		}
		frontmatter := Frontmatter{Title: title, Scope: "global", Type: TypeNavigation, Status: "verified", IsIndex: boolPointer(true)}
		content, renderErr := renderDocument(frontmatter, block)
		if renderErr != nil {
			return []string{fmt.Sprintf("%s: render index page: %v", relative, renderErr)}
		}
		if writeErr := writeVaultFileIfChanged(vaultPath, target, []byte(content), 0o644); writeErr != nil {
			return []string{fmt.Sprintf("%s: create index page: %v", relative, writeErr)}
		}
		return nil
	}
	start := strings.Index(string(raw), indexBlockStart)
	if start < 0 {
		return []string{fmt.Sprintf("%s: generated index markers are missing; page left unchanged", relative)}
	}
	endRelative := strings.Index(string(raw)[start:], indexBlockEnd)
	if endRelative < 0 {
		return []string{fmt.Sprintf("%s: generated index markers are incomplete; page left unchanged", relative)}
	}
	end := start + endRelative + len(indexBlockEnd)
	next := string(raw[:start]) + block + string(raw[end:])
	if next == string(raw) {
		return nil
	}
	if writeErr := writeVaultFileIfChanged(vaultPath, target, []byte(next), 0o644); writeErr != nil {
		return []string{fmt.Sprintf("%s: update generated index: %v", relative, writeErr)}
	}
	return nil
}

// autoIndexDirectoryAllowed reports whether a vault-relative directory belongs
// to the global, project or archive knowledge trees. Excluded names are pruned
// while walking, so this only has to recognise the three roots.
func autoIndexDirectoryAllowed(relative string) bool {
	for _, root := range []string{"global", "projects", "archive"} {
		if relative == root || strings.HasPrefix(relative, root+"/") {
			return true
		}
	}
	// Top-level root libraries and their subdirectories also get generated
	// index pages, including empty ones.
	segments := strings.Split(filepath.ToSlash(relative), "/")
	if len(segments) >= 1 && rootLibraryNameAllowed(segments[0]) &&
		!skippedDirectoryNames[strings.ToLower(segments[0])] {
		return true
	}
	return false
}

// autoIndexLibraryRoot maps a directory in the knowledge tree to the library
// root that owns it. Subdirectories inherit the project/archive binding of
// their library root. A directory directly under projects/ or archive/ is its
// own root even when no manifest mapping exists yet; the caller then reports it
// as unbound instead of leaving it without a navigation page.
func autoIndexLibraryRoot(directory string) (root, slugKey, scope string, ok bool) {
	segments := strings.Split(filepath.ToSlash(directory), "/")
	switch segments[0] {
	case "projects":
		if len(segments) < 2 || segments[1] == "" {
			return "", "", "", false
		}
		slug := segments[1]
		return path.Join("projects", slug), slug, "project", true
	case "archive":
		if len(segments) >= 2 && segments[1] == "projects" {
			if len(segments) < 3 || segments[2] == "" {
				// archive/projects is the container, not a library.
				return "", "", "", false
			}
			name := segments[2]
			return path.Join("archive", "projects", name), path.Join("projects", name), "archive", true
		}
		if len(segments) < 2 || segments[1] == "" {
			return "", "", "", false
		}
		slug := segments[1]
		return path.Join("archive", slug), slug, "archive", true
	default:
		if len(segments) >= 1 && rootLibraryNameAllowed(segments[0]) &&
			!skippedDirectoryNames[strings.ToLower(segments[0])] {
			return segments[0], segments[0], "root", true
		}
		return "", "", "", false
	}
}

// missingIndexFrontmatter builds the canonical page metadata for a missing
// directory index. A directory without a trustworthy project id still gets a
// navigation page so the tree is browsable; the returned warning names the
// unbound root instead of silently skipping it.
func missingIndexFrontmatter(vaultPath string, manifest Manifest, reverseProjects, reverseArchives map[string]string, directory string) (Frontmatter, string, bool) {
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
	root, slugKey, scope, ok := autoIndexLibraryRoot(directory)
	if !ok {
		return Frontmatter{}, "", false
	}
	if scope == "root" {
		// Root libraries are filesystem-managed: no project binding and no
		// project/archive name cache. The directory name is the title source.
		frontmatter.Scope = "root"
		frontmatter.ProjectID = ""
		frontmatter.Title = humanizeSegment(path.Base(directory))
		return frontmatter, "", true
	}
	projectID := ""
	if scope == "project" {
		projectID = reverseProjects[slugKey]
	} else {
		projectID = reverseArchives[slugKey]
	}
	if projectID == "" {
		projectID = inferProjectIDFromDocuments(vaultPath, root)
	}
	frontmatter.Scope = scope
	frontmatter.ProjectID = projectID
	if directory == root {
		if scope == "project" {
			frontmatter.Title = strings.TrimSpace(manifest.ProjectNames[projectID])
		} else {
			frontmatter.Title = strings.TrimSpace(manifest.ArchiveNames[projectID])
		}
		if frontmatter.Title == "" || isKnowledgeHomeTitle(frontmatter.Title) {
			frontmatter.Title = humanizeSegment(path.Base(root))
		}
	} else {
		frontmatter.Title = humanizeSegment(path.Base(directory))
	}
	if projectID == "" {
		return frontmatter, fmt.Sprintf("%s: index.md is missing and no project id can be determined; page created as unbound navigation", root), true
	}
	return frontmatter, "", true
}

// indexPageProjectID reads the project binding declared by an existing index
// page. It returns "" for a missing page or a page without project_id, which
// is how a generated container page differs from a hand-authored library.
func indexPageProjectID(vaultPath, relative string) string {
	raw, err := os.ReadFile(filepath.Join(vaultPath, filepath.FromSlash(relative)))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(ParseDocument(string(raw)).Frontmatter.ProjectID)
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
// maintainRootLibraryIndexes fills the generated block of every root library's
// own navigation page. Those pages are deliberately excluded from the index, so
// maintainVaultIndexes never sees them; their subdirectory index pages are
// indexed normally and contribute folder nodes to the tree.
func maintainRootLibraryIndexes(vaultPath string, records []*scannedRecord) []string {
	warnings := []string{}
	byPath := make(map[string]*scannedRecord, len(records))
	for _, record := range records {
		byPath[record.entry.Path] = record
	}
	for _, root := range RootLibraryDirectories(vaultPath) {
		relativeIndex := RootLibraryIndexRelative(root)
		target := filepath.Join(vaultPath, filepath.FromSlash(relativeIndex))
		if info, err := os.Lstat(target); err != nil || info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		subdirectories := map[string]string{}
		docPaths := make([]string, 0)
		for record := range byPath {
			if !strings.HasPrefix(record, root+"/") {
				continue
			}
			if path.Dir(record) == root {
				if path.Base(record) != "index.md" && !isReservedVaultPath(record) {
					docPaths = append(docPaths, record)
				}
				continue
			}
			// Immediate subdirectory index pages become folder links.
			if path.Base(record) == "index.md" && path.Dir(path.Dir(record)) == root {
				subdirectories[path.Dir(record)] = byPath[record].entry.Title
			}
		}
		subdirPaths := make([]string, 0, len(subdirectories))
		for candidate := range subdirectories {
			subdirPaths = append(subdirPaths, candidate)
		}
		sort.Strings(subdirPaths)
		sort.Strings(docPaths)
		block := generatedIndexBlockForEntries(byPath, subdirPaths, docPaths)
		warnings = append(warnings, writeMaintainedIndexBlock(vaultPath, relativeIndex, block)...)
	}
	return warnings
}

// writeMaintainedIndexBlock replaces only the machine-generated marker block
// inside an existing page. A missing page or missing markers is left alone.
func writeMaintainedIndexBlock(vaultPath, relative, block string) []string {
	target := filepath.Join(vaultPath, filepath.FromSlash(relative))
	raw, err := os.ReadFile(target)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return []string{fmt.Sprintf("%s: read index page: %v", relative, err)}
	}
	start := strings.Index(string(raw), indexBlockStart)
	if start < 0 {
		return []string{fmt.Sprintf("%s: generated index markers are missing; page left unchanged", relative)}
	}
	endRelative := strings.Index(string(raw)[start:], indexBlockEnd)
	if endRelative < 0 {
		return []string{fmt.Sprintf("%s: generated index markers are incomplete; page left unchanged", relative)}
	}
	end := start + endRelative + len(indexBlockEnd)
	next := string(raw[:start]) + block + string(raw[end:])
	if next == string(raw) {
		return nil
	}
	if err := writeVaultFileIfChanged(vaultPath, target, []byte(next), 0o644); err != nil {
		return []string{fmt.Sprintf("%s: update generated index: %v", relative, err)}
	}
	return nil
}

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
