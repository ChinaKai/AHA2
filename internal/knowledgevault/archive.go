package knowledgevault

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ErrProjectIDArchived reports that a new project tried to reuse the ID of an
// existing archived vault space. The ID stays reserved so a second archive can
// never overwrite or silently merge with the first.
var ErrProjectIDArchived = errors.New("knowledge vault project id is already archived")

// archiveTimestampLayout is the UTC stamp in an archived project directory
// name. It is compact, sortable and safe on Windows.
const archiveTimestampLayout = "20060102T150405Z"

// ArchiveProjectSpace moves one project's vault directory below
// archive/projects/ and removes its manifest mapping. The move is a single
// rename, so a failure leaves the live project space untouched; knowledge
// files are never deleted. The index is not rebuilt here: Manager rebuilds it
// after the files and manifest are consistent.
//
// It is idempotent: when neither a manifest mapping nor a project directory
// exists the call succeeds with an empty path. A previous partial attempt that
// already moved the directory but failed to update the manifest is repaired on
// the next call (the stale mapping is dropped and the archived directory is
// rediscovered by the rebuild).
func ArchiveProjectSpace(cfg Config, projectID string, now time.Time) (string, error) {
	archivedPath, _, err := archiveProjectSpace(cfg, projectID, now)
	return archivedPath, err
}

// archiveProjectSpace is the implementation; needsRebuild is true whenever the
// files or manifest changed, including a retry that only has to refresh an
// already-moved archive.
func archiveProjectSpace(cfg Config, projectID string, now time.Time) (archivedPath string, needsRebuild bool, err error) {
	cfg, err = cfg.Normalized()
	if err != nil {
		return "", false, err
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return "", false, fmt.Errorf("project id is required")
	}
	manifest, err := ReadManifest(cfg.VaultPath)
	if err != nil {
		if os.IsNotExist(err) {
			// A legacy/test installation without a vault has no space to
			// archive. Deleting a project must not fail because of that.
			return "", false, nil
		}
		return "", false, fmt.Errorf("read vault manifest: %w", err)
	}
	now = now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	slug := strings.TrimSpace(manifest.Projects[projectID])
	if slug == "" {
		// A retry after the manifest update but before the index rebuild finds
		// the archive mapping instead of the project mapping.
		if archivedSlug := strings.TrimSpace(manifest.Archives[projectID]); archivedSlug != "" {
			if existingPath, ok := existingArchiveDir(cfg.VaultPath, archivedSlug); ok {
				return existingPath, true, nil
			}
		}
		// Repair a crash between directory creation and the manifest write:
		// the root page still declares the project id.
		if existing, ok := projectSlugFromRootIndex(cfg.VaultPath, projectID); ok {
			slug = existing
		} else {
			return "", false, nil
		}
	}
	sourceRelative := path.Join("projects", slug)
	if err := ValidateRelativePath(sourceRelative); err != nil {
		return "", false, fmt.Errorf("project space path is invalid: %w", err)
	}
	if path.Dir(sourceRelative) != "projects" {
		return "", false, fmt.Errorf("project space slug %q escapes the projects directory", slug)
	}
	sourceAbsolute := filepath.Join(cfg.VaultPath, filepath.FromSlash(sourceRelative))
	info, statErr := os.Stat(sourceAbsolute)
	if statErr != nil {
		if !os.IsNotExist(statErr) {
			return "", false, fmt.Errorf("inspect project space %s: %w", sourceRelative, statErr)
		}
		// The mapping is stale and there are no files to move. Drop it so the
		// index rebuild no longer reports a phantom project.
		delete(manifest.Projects, projectID)
		delete(manifest.ProjectNames, projectID)
		manifest.UpdatedAt = now.Format(time.RFC3339)
		if err := writeManifest(cfg.VaultPath, manifest); err != nil {
			return "", false, fmt.Errorf("write vault manifest: %w", err)
		}
		return "", true, nil
	}
	if !info.IsDir() {
		return "", false, fmt.Errorf("project space %s is not a directory", sourceRelative)
	}
	targetSlug, err := allocateProjectArchiveSlug(cfg.VaultPath, manifest, slug, projectID, now)
	if err != nil {
		return "", false, err
	}
	targetRelative := path.Join("archive", targetSlug)
	targetAbsolute := filepath.Join(cfg.VaultPath, filepath.FromSlash(targetRelative))
	if err := os.MkdirAll(filepath.Dir(targetAbsolute), 0o755); err != nil {
		return "", false, fmt.Errorf("create archive directory for %s: %w", projectID, err)
	}
	if err := os.Rename(sourceAbsolute, targetAbsolute); err != nil {
		return "", false, fmt.Errorf("move project space %s to %s: %w", sourceRelative, targetRelative, err)
	}
	displayName := strings.TrimSpace(manifest.ProjectNames[projectID])
	delete(manifest.Projects, projectID)
	delete(manifest.ProjectNames, projectID)
	manifest.Archives[projectID] = targetSlug
	if displayName != "" {
		manifest.ArchiveNames[projectID] = displayName
	}
	manifest.UpdatedAt = now.Format(time.RFC3339)
	if err := writeManifest(cfg.VaultPath, manifest); err != nil {
		return targetRelative, true, fmt.Errorf("project space moved to %s but the manifest update failed: %w", targetRelative, err)
	}
	return targetRelative, true, nil
}

// existingArchiveDir confirms a manifest archive slug still names a directory.
func existingArchiveDir(vaultPath, archivedSlug string) (string, bool) {
	if archivedSlug == "" {
		return "", false
	}
	relative := path.Join("archive", archivedSlug)
	if err := ValidateRelativePath(relative); err != nil {
		return "", false
	}
	info, err := os.Stat(filepath.Join(vaultPath, filepath.FromSlash(relative)))
	if err != nil || !info.IsDir() {
		return "", false
	}
	return relative, true
}

// ArchivedProjectSpace reports whether projectID already owns an archive
// directory. It is the read-only variant used before project creation; the
// provisioning path uses the same lookup and may additionally drop a stale
// manifest entry.
func ArchivedProjectSpace(cfg Config, projectID string) (string, bool) {
	cfg, err := cfg.Normalized()
	if err != nil {
		return "", false
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return "", false
	}
	manifest, err := ReadManifest(cfg.VaultPath)
	if err != nil {
		return "", false
	}
	return archivedProjectSpace(manifest, cfg.VaultPath, projectID)
}

// archivedProjectSpace combines the manifest mapping with a scan of archive
// root pages. The scan is what catches a crash between the directory rename
// and the manifest write, when manifest.projects still points at the moved (now
// missing) project directory and manifest.archives has no entry yet. A mapping
// whose directory no longer exists is ignored.
func archivedProjectSpace(manifest Manifest, vaultPath, projectID string) (string, bool) {
	if archivedSlug := strings.TrimSpace(manifest.Archives[projectID]); archivedSlug != "" {
		relative := path.Join("archive", archivedSlug)
		if info, err := os.Stat(filepath.Join(vaultPath, filepath.FromSlash(relative))); err == nil && info.IsDir() {
			return relative, true
		}
	}
	return archiveDirectoryClaimingProject(vaultPath, projectID)
}

// archiveDirectoryClaimingProject walks flat archive libraries and the nested
// archive/projects area for a root index.md whose frontmatter declares the
// project ID. Archive directories without a root page cannot claim an ID.
func archiveDirectoryClaimingProject(vaultPath, projectID string) (string, bool) {
	archiveRoot := filepath.Join(vaultPath, "archive")
	children, err := os.ReadDir(archiveRoot)
	if err != nil {
		return "", false
	}
	sort.Slice(children, func(left, right int) bool { return children[left].Name() < children[right].Name() })
	claims := func(relative string) (string, bool) {
		raw, err := os.ReadFile(filepath.Join(vaultPath, filepath.FromSlash(path.Join(relative, "index.md"))))
		if err != nil {
			return "", false
		}
		document := ParseDocument(string(raw))
		if strings.TrimSpace(document.Frontmatter.ProjectID) != projectID {
			return "", false
		}
		return relative, true
	}
	for _, child := range children {
		name := child.Name()
		if !child.IsDir() || strings.HasPrefix(name, ".") {
			continue
		}
		relative := path.Join("archive", name)
		if found, ok := claims(relative); ok {
			return found, true
		}
		if name != "projects" {
			continue
		}
		nested, nestedErr := os.ReadDir(filepath.Join(archiveRoot, "projects"))
		if nestedErr != nil {
			continue
		}
		sort.Slice(nested, func(left, right int) bool { return nested[left].Name() < nested[right].Name() })
		for _, item := range nested {
			if !item.IsDir() || strings.HasPrefix(item.Name(), ".") {
				continue
			}
			if found, ok := claims(path.Join(relative, item.Name())); ok {
				return found, true
			}
		}
	}
	return "", false
}

// allocateProjectArchiveSlug builds archive/projects/<slug>-<project8>-<UTC>
// and adds a numeric suffix when that path or manifest entry already exists.
// It never returns a target that exists on disk, so an archive can never
// overwrite older knowledge.
func allocateProjectArchiveSlug(vaultPath string, manifest Manifest, slug, projectID string, now time.Time) (string, error) {
	shortID := projectID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	base := slug + "-" + shortID + "-" + now.UTC().Format(archiveTimestampLayout)
	container := filepath.Join(vaultPath, "archive", "projects")
	if info, err := os.Lstat(container); err == nil {
		if !info.IsDir() {
			return "", fmt.Errorf("archive projects path %s is not a directory", container)
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect archive projects path %s: %w", container, err)
	}
	used := map[string]bool{}
	for _, archived := range manifest.Archives {
		used[strings.TrimSpace(archived)] = true
	}
	for attempt := 1; attempt <= 100; attempt++ {
		candidate := base
		if attempt > 1 {
			candidate = fmt.Sprintf("%s-%d", base, attempt)
		}
		relative := path.Join("projects", candidate)
		if used[relative] {
			continue
		}
		if _, err := os.Lstat(filepath.Join(vaultPath, "archive", filepath.FromSlash(relative))); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("inspect archive target %s: %w", relative, err)
		}
		return relative, nil
	}
	return "", fmt.Errorf("no free archive directory for project %s under archive/projects", projectID)
}
