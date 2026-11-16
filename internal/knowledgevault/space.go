package knowledgevault

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ProjectSpaceResult reports the vault directory that backs one project. In
// model A the project ID is the space ID, so no separate space record exists.
type ProjectSpaceResult struct {
	ProjectID string `json:"project_id"`
	Slug      string `json:"slug"`
	RootPath  string `json:"root_path"`
	Created   bool   `json:"created"`
}

// ErrSpaceSlugConflict reports that even the stable suffix for a project ID is
// already used by another vault space. It is an explicit error rather than a
// silent second directory for the same project.
var ErrSpaceSlugConflict = fmt.Errorf("knowledge vault project slug conflict")

// EnsureProjectSpace creates or repairs the manifest mapping, project
// directory and root index.md for one project. It is idempotent: an existing
// mapping keeps its slug and an existing index page is never overwritten.
func EnsureProjectSpace(cfg Config, projectID, projectName string, now time.Time) (ProjectSpaceResult, error) {
	cfg, err := cfg.Normalized()
	if err != nil {
		return ProjectSpaceResult{}, err
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return ProjectSpaceResult{}, fmt.Errorf("project id is required")
	}
	if !VaultInitialized(cfg.VaultPath) {
		return ProjectSpaceResult{}, ErrVaultNotInitialized
	}
	manifest, err := ReadManifest(cfg.VaultPath)
	if err != nil {
		return ProjectSpaceResult{}, err
	}
	now = now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	result := ProjectSpaceResult{ProjectID: projectID}
	slug := strings.TrimSpace(manifest.Projects[projectID])
	if slug == "" {
		slug, err = allocateProjectSpaceSlug(cfg.VaultPath, manifest, projectID, projectName)
		if err != nil {
			return result, err
		}
		manifest.Projects[projectID] = slug
		result.Created = true
	}
	result.Slug = slug
	result.RootPath = path.Join("projects", slug)
	displayName := strings.TrimSpace(projectName)
	if displayName != "" && manifest.ProjectNames[projectID] != displayName {
		manifest.ProjectNames[projectID] = displayName
	}
	directory := filepath.Join(cfg.VaultPath, "projects", slug)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return result, fmt.Errorf("create project space directory: %w", err)
	}
	rootIndex := filepath.Join(directory, "index.md")
	if _, statErr := os.Lstat(rootIndex); os.IsNotExist(statErr) {
		title := displayName
		if title == "" {
			title = manifest.ProjectNames[projectID]
		}
		if title == "" {
			title = humanizeSegment(slug)
		}
		frontmatter := Frontmatter{
			Title: title, Slug: "index", Scope: "project", ProjectID: projectID,
			Type: TypeNavigation, Status: "verified", IsIndex: boolPointer(true),
			Created: now.Format(time.RFC3339), Updated: now.Format(time.RFC3339),
		}
		if err := createVaultIndexPage(cfg.VaultPath, path.Join("projects", slug, "index.md"), frontmatter); err != nil {
			return result, fmt.Errorf("create project space root index: %w", err)
		}
		result.Created = true
	}
	manifest.UpdatedAt = now.Format(time.RFC3339)
	if err := writeManifest(cfg.VaultPath, manifest); err != nil {
		return result, fmt.Errorf("write vault manifest: %w", err)
	}
	return result, nil
}

// allocateProjectSpaceSlug derives a stable slug from the display name and
// adds a deterministic suffix when the base is taken. Manual directories that
// exist without a manifest mapping are treated as taken so a new space can
// never silently adopt somebody else's folder.
func allocateProjectSpaceSlug(vaultPath string, manifest Manifest, projectID, projectName string) (string, error) {
	used := map[string]bool{
		"global": true, "projects": true, "archive": true, "skills": true,
		"orphans": true, "inbox": true, "knowledge-base-guide": true,
	}
	for _, slug := range manifest.Projects {
		used[slug] = true
	}
	for _, slug := range manifest.Archives {
		used[slug] = true
	}
	if entries, err := os.ReadDir(filepath.Join(vaultPath, "projects")); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				used[entry.Name()] = true
			}
		}
	}
	base := Slugify(projectName)
	if base == "" {
		base = Slugify(projectID)
	}
	if !used[base] {
		return base, nil
	}
	for index := 2; index <= 20; index++ {
		candidate := fmt.Sprintf("%s-%d", base, index)
		if !used[candidate] {
			return candidate, nil
		}
	}
	sum := sha256.Sum256([]byte(projectID))
	candidate := base + "-" + hex.EncodeToString(sum[:])[:8]
	if used[candidate] {
		return "", fmt.Errorf("%w: %s already maps to another project", ErrSpaceSlugConflict, candidate)
	}
	return candidate, nil
}

// ProjectSpaceDir returns the absolute directory for a project's vault space.
func ProjectSpaceDir(cfg Config, projectID string) (string, bool) {
	cfg, err := cfg.Normalized()
	if err != nil {
		return "", false
	}
	manifest, err := ReadManifest(cfg.VaultPath)
	if err != nil {
		return "", false
	}
	slug := strings.TrimSpace(manifest.Projects[strings.TrimSpace(projectID)])
	if slug == "" {
		return "", false
	}
	return filepath.Join(cfg.VaultPath, "projects", slug), true
}

// SpaceRootPath returns the vault-relative root index path for a project.
func SpaceRootPath(cfg Config, projectID string) (string, bool) {
	cfg, err := cfg.Normalized()
	if err != nil {
		return "", false
	}
	manifest, err := ReadManifest(cfg.VaultPath)
	if err != nil {
		return "", false
	}
	slug := strings.TrimSpace(manifest.Projects[strings.TrimSpace(projectID)])
	if slug == "" {
		return "", false
	}
	return path.Join("projects", slug, "index.md"), true
}

// CheckProjectSpacePrerequisites verifies the vault and index locations can
// accept a new space before a project row is created. The probe writes a
// temporary file and removes it; it never mutates knowledge content.
func CheckProjectSpacePrerequisites(cfg Config) error {
	cfg, err := cfg.Normalized()
	if err != nil {
		return err
	}
	if !VaultInitialized(cfg.VaultPath) {
		return ErrVaultNotInitialized
	}
	if err := probeWritableDirectory(cfg.VaultPath); err != nil {
		return fmt.Errorf("knowledge vault is not writable: %w", err)
	}
	if err := probeWritableDirectory(filepath.Dir(cfg.IndexPath)); err != nil {
		return fmt.Errorf("knowledge index is not writable: %w", err)
	}
	return nil
}

func probeWritableDirectory(directory string) error {
	file, err := os.CreateTemp(directory, ".aha-write-probe-*")
	if err != nil {
		return err
	}
	name := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Remove(name)
}

// SortedProjectIDs is used by reconciliation reports so repeated runs produce
// deterministic output.
func SortedProjectIDs(values map[string]string) []string {
	ids := make([]string, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
