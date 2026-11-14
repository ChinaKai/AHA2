package knowledgevault

import (
	"fmt"
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
	projects, projectNames, archives, archiveNames, warnings, err := deriveLibraries(vaultPath)
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

func deriveLibraries(vaultPath string) (map[string]string, map[string]string, map[string]string, map[string]string, []string, error) {
	projects := map[string]string{}
	projectNames := map[string]string{}
	archives := map[string]string{}
	archiveNames := map[string]string{}
	warnings := []string{}
	for _, root := range []struct {
		directory string
		byID      map[string]string
		names     map[string]string
		kind      string
	}{
		{directory: "projects", byID: projects, names: projectNames, kind: "project"},
		{directory: "archive", byID: archives, names: archiveNames, kind: "archive"},
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
		for _, child := range children {
			slug := child.Name()
			if !child.IsDir() || strings.HasPrefix(slug, ".") {
				continue
			}
			relative := path.Join(root.directory, slug)
			indexPath := filepath.Join(vaultPath, filepath.FromSlash(path.Join(relative, "index.md")))
			raw, readErr := os.ReadFile(indexPath)
			if readErr != nil {
				if os.IsNotExist(readErr) {
					warnings = append(warnings, fmt.Sprintf("%s: unbound (%s/index.md is missing); no %s mapping was generated", relative, relative, root.kind))
				} else {
					warnings = append(warnings, fmt.Sprintf("%s: unbound (read index.md: %v); no %s mapping was generated", relative, readErr, root.kind))
				}
				continue
			}
			document := ParseDocument(string(raw))
			projectID := strings.TrimSpace(document.Frontmatter.ProjectID)
			if projectID == "" {
				warnings = append(warnings, fmt.Sprintf("%s: unbound (index.md frontmatter project_id is missing); no %s mapping was generated", relative, root.kind))
				continue
			}
			if other, exists := usedSlugs[projectID]; exists {
				warnings = append(warnings, fmt.Sprintf("%s: duplicate %s binding %q already used by %s; mapping skipped", relative, root.kind, projectID, other))
				continue
			}
			usedSlugs[projectID] = relative
			root.byID[projectID] = slug
			name := strings.TrimSpace(document.Frontmatter.Title)
			if name == "" {
				name = FirstHeading(document.Body)
			}
			if name == "" {
				name = humanizeSegment(slug)
			}
			root.names[projectID] = name
		}
	}
	return projects, projectNames, archives, archiveNames, warnings, nil
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
		lines = append(lines, indexListLine(target, entry.entry.Title, IndexSummary(entry.entry.Body, 120)))
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
