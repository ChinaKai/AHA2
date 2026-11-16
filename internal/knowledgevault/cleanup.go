package knowledgevault

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Infrastructure projects (the pet's system project and channel host
// projects) do not own a vault space. These errors distinguish the two reasons
// a leftover space cannot be removed automatically: it still has knowledge
// files, or its content cannot be confirmed as an empty generated space.
var (
	ErrProjectSpaceNotEmpty    = errors.New("project knowledge space is not empty")
	ErrProjectSpaceUnconfirmed = errors.New("project knowledge space cannot be confirmed empty")
)

// projectSpaceTrashPrefix marks the .trash entries written by the empty-space
// cleanup. Only entries with this prefix are ever swept at startup; anything
// else in .trash belongs to another feature or to the owner.
const projectSpaceTrashPrefix = "infra-space-"

// ProjectSpaceCleanupResult reports one cleanup attempt. Removed is true when
// the manifest mapping (and directory, when it existed) is gone. BlockingPaths
// lists the files that prevented deletion so the owner can inspect them.
// Warning reports a directory kept in .trash after the mapping was already
// removed: the cleanup succeeded, but unconfirmed content appeared in the last
// window before deletion and was preserved instead of deleted.
type ProjectSpaceCleanupResult struct {
	ProjectID     string   `json:"project_id"`
	Slug          string   `json:"slug,omitempty"`
	Removed       bool     `json:"removed"`
	BlockingPaths []string `json:"blocking_paths,omitempty"`
	Warning       string   `json:"warning,omitempty"`
}

// cleanupHooks are test seams around the two filesystem windows of a space
// removal. Production passes the zero value.
type cleanupHooks struct {
	// afterRename runs after the directory was moved into .trash and before
	// the space is re-confirmed there.
	afterRename func(trashPath string)
	// beforeRemove runs after the manifest update and before the final
	// emptiness confirmation and deletion.
	beforeRemove func(trashPath string)
}

// RemoveEmptyProjectSpace removes the vault space of an infrastructure project
// when it only contains generated scaffolding: the root index.md, empty
// directories and .gitkeep markers. The vault package has no project-type
// knowledge, so callers must restrict this to channel/system projects. Knowledge
// files are never deleted: a space with any other file is refused with
// ErrProjectSpaceNotEmpty, and a directory whose root index does not declare
// the project ID is refused with ErrProjectSpaceUnconfirmed.
//
// The operation is idempotent. It renames the directory into the vault's
// .trash area before touching the manifest, so a failed manifest write can be
// rolled back by renaming the directory back. The directory is removed only
// after the manifest no longer maps the project. The file tree is the source of
// truth: the rebuild that follows a removal discards any stale index rows, so
// the index cannot retain documents that no longer have files.
func RemoveEmptyProjectSpace(cfg Config, projectID string, now time.Time) (ProjectSpaceCleanupResult, error) {
	result, _, err := removeEmptyProjectSpace(cfg, projectID, now)
	return result, err
}

// removeEmptyProjectSpace is the implementation; needsRebuild is true whenever
// the manifest changed and the index projection has to be refreshed.
func removeEmptyProjectSpace(cfg Config, projectID string, now time.Time) (result ProjectSpaceCleanupResult, needsRebuild bool, err error) {
	return removeEmptyProjectSpaceWithHooks(cfg, projectID, now, cleanupHooks{})
}

// removeEmptyProjectSpaceWithHooks is removeEmptyProjectSpace with the test
// seams described by cleanupHooks. The hooks only widen the two windows where
// an external writer can race the removal; production never sets them.
func removeEmptyProjectSpaceWithHooks(cfg Config, projectID string, now time.Time, hooks cleanupHooks) (result ProjectSpaceCleanupResult, needsRebuild bool, err error) {
	cfg, err = cfg.Normalized()
	if err != nil {
		return ProjectSpaceCleanupResult{}, false, err
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return ProjectSpaceCleanupResult{}, false, fmt.Errorf("project id is required")
	}
	result = ProjectSpaceCleanupResult{ProjectID: projectID}
	if !VaultInitialized(cfg.VaultPath) {
		return result, false, nil
	}
	manifest, err := ReadManifest(cfg.VaultPath)
	if err != nil {
		if os.IsNotExist(err) {
			return result, false, nil
		}
		return result, false, fmt.Errorf("read vault manifest: %w", err)
	}
	now = now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	slug := strings.TrimSpace(manifest.Projects[projectID])
	if slug == "" {
		// Repair a crash between directory creation and the manifest write.
		if existing, ok := projectSlugFromRootIndex(cfg.VaultPath, projectID); ok {
			slug = existing
		}
	}
	if slug == "" {
		return result, false, nil
	}
	relative := path.Join("projects", slug)
	if err := ValidateRelativePath(relative); err != nil {
		return result, false, fmt.Errorf("project space path is invalid: %w", err)
	}
	if path.Dir(relative) != "projects" {
		return result, false, fmt.Errorf("project space slug %q escapes the projects directory", slug)
	}
	result.Slug = slug
	directory := filepath.Join(cfg.VaultPath, filepath.FromSlash(relative))
	info, statErr := os.Lstat(directory)
	if statErr != nil {
		if !os.IsNotExist(statErr) {
			return result, false, fmt.Errorf("inspect project space %s: %w", relative, statErr)
		}
		// The mapping is stale and there is nothing on disk to delete. Drop it
		// so reconciliation stops treating the project as bound.
		if _, mapped := manifest.Projects[projectID]; !mapped {
			return result, false, nil
		}
		delete(manifest.Projects, projectID)
		delete(manifest.ProjectNames, projectID)
		manifest.UpdatedAt = now.Format(time.RFC3339)
		if err := writeManifest(cfg.VaultPath, manifest); err != nil {
			return result, false, fmt.Errorf("write vault manifest: %w", err)
		}
		result.Removed = true
		return result, true, nil
	}
	if !info.IsDir() {
		return result, false, fmt.Errorf("project space %s is not a directory", relative)
	}
	blocking, confirmErr := emptyInfrastructureSpaceFiles(directory, projectID)
	if confirmErr != nil {
		return result, false, confirmErr
	}
	if len(blocking) > 0 {
		result.BlockingPaths = blocking
		return result, false, fmt.Errorf("%w: %s contains %d knowledge file(s)", ErrProjectSpaceNotEmpty, relative, len(blocking))
	}

	trashPath, err := moveToVaultTrash(cfg.VaultPath, slug, now)
	if err != nil {
		return result, false, err
	}
	if hooks.afterRename != nil {
		hooks.afterRename(trashPath)
	}
	// Re-confirm the tree at its new location. A writer can add a file to the
	// source directory after the check above and before the rename; the rename
	// carries it into .trash. The manifest still maps the project here, so a
	// mismatch is rolled back by renaming the directory back and nothing is
	// deleted.
	lateBlocking, lateErr := emptyInfrastructureSpaceFiles(trashPath, projectID)
	if lateErr != nil || len(lateBlocking) > 0 {
		result.BlockingPaths = lateBlocking
		if restoreErr := os.Rename(trashPath, directory); restoreErr != nil {
			return result, false, fmt.Errorf("project space %s changed while it was moved to .trash and %s could not be restored: %w", relative, trashPath, restoreErr)
		}
		if lateErr != nil {
			return result, false, lateErr
		}
		return result, false, fmt.Errorf("%w: %s gained %d file(s) while it was being removed", ErrProjectSpaceNotEmpty, relative, len(lateBlocking))
	}
	delete(manifest.Projects, projectID)
	delete(manifest.ProjectNames, projectID)
	manifest.UpdatedAt = now.Format(time.RFC3339)
	if err := writeManifest(cfg.VaultPath, manifest); err != nil {
		// Restore the directory so the failed cleanup leaves no half-deleted
		// state. The manifest was not written, so the mapping is still valid.
		restoreErr := os.Rename(trashPath, directory)
		if restoreErr != nil {
			return result, false, fmt.Errorf("write vault manifest: %w (restoring %s also failed: %v)", err, relative, restoreErr)
		}
		return result, false, fmt.Errorf("write vault manifest: %w", err)
	}
	result.Removed = true
	if hooks.beforeRemove != nil {
		hooks.beforeRemove(trashPath)
	}
	// One more confirmation immediately before deletion. Content that appears
	// after the post-rename check is kept in .trash and reported; it is never
	// deleted. The manifest mapping is already gone, so the cleanup itself
	// counts as done and the warning is not an error.
	lateBlocking, lateErr = emptyInfrastructureSpaceFiles(trashPath, projectID)
	if lateErr != nil || len(lateBlocking) > 0 {
		result.BlockingPaths = lateBlocking
		reason := strings.Join(lateBlocking, ", ")
		if lateErr != nil {
			reason = lateErr.Error()
		}
		result.Warning = fmt.Sprintf("project space %s gained unconfirmed content after the manifest update; kept %s for inspection (%s)", relative, trashPath, reason)
		return result, true, nil
	}
	// The manifest no longer references the space, so a leftover .trash
	// directory is invisible to the index. Report it only if the platform
	// refused the final removal.
	if err := os.RemoveAll(trashPath); err != nil {
		return result, true, fmt.Errorf("project space %s was removed from the manifest but %s could not be deleted: %w", relative, trashPath, err)
	}
	return result, true, nil
}

// spaceRootProjectID reads the project attribution from a space's root index.
// An unreadable or unattributable root means the directory cannot be treated
// as generated scaffolding.
func spaceRootProjectID(directory string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(directory, "index.md"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%w: root index.md is missing", ErrProjectSpaceUnconfirmed)
		}
		return "", fmt.Errorf("%w: read root index.md: %v", ErrProjectSpaceUnconfirmed, err)
	}
	return strings.TrimSpace(ParseDocument(string(raw)).Frontmatter.ProjectID), nil
}

// emptyInfrastructureSpaceFiles confirms the directory is generated
// scaffolding and returns the files that are not. The root index must exist
// and declare this project: without it the directory cannot be attributed to
// the project and is treated as unconfirmed instead of deleted.
func emptyInfrastructureSpaceFiles(directory, projectID string) ([]string, error) {
	declared, err := spaceRootProjectID(directory)
	if err != nil {
		return nil, err
	}
	if declared != projectID {
		return nil, fmt.Errorf("%w: root index.md does not declare project %s", ErrProjectSpaceUnconfirmed, projectID)
	}
	blocking := []string{}
	walkErr := filepath.WalkDir(directory, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		name := entry.Name()
		if filePath == filepath.Join(directory, "index.md") || name == ".gitkeep" {
			return nil
		}
		relative, relErr := filepath.Rel(directory, filePath)
		if relErr != nil {
			relative = filePath
		}
		blocking = append(blocking, filepath.ToSlash(relative))
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("%w: walk project space: %v", ErrProjectSpaceUnconfirmed, walkErr)
	}
	sort.Strings(blocking)
	return blocking, nil
}

// moveToVaultTrash renames one project directory below the vault's reserved
// .trash area. The dot-prefixed directory is skipped by the manifest derivation
// and the index rebuild, so the rename is invisible to readers even before the
// manifest is rewritten.
func moveToVaultTrash(vaultPath, slug string, now time.Time) (string, error) {
	trashDir := filepath.Join(vaultPath, ".trash")
	if err := os.MkdirAll(trashDir, 0o755); err != nil {
		return "", fmt.Errorf("create vault trash directory: %w", err)
	}
	base := filepath.Join(trashDir, projectSpaceTrashPrefix+SafeSegment(slug, "project")+"-"+now.Format("20060102T150405Z"))
	target := base
	for attempt := 2; attempt <= 100; attempt++ {
		if _, err := os.Lstat(target); os.IsNotExist(err) {
			break
		} else if err != nil {
			return "", fmt.Errorf("inspect trash target %s: %w", target, err)
		}
		target = fmt.Sprintf("%s-%d", base, attempt)
	}
	if _, err := os.Lstat(target); err == nil {
		return "", fmt.Errorf("no free trash directory for project space %s", slug)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect trash target %s: %w", target, err)
	}
	source := filepath.Join(vaultPath, "projects", slug)
	if err := os.Rename(source, target); err != nil {
		return "", fmt.Errorf("move project space %s to trash: %w", slug, err)
	}
	return target, nil
}

// ProjectSpaceTrashSweepResult reports the startup sweep of crash residue below
// .trash. Removed lists the .trash entries that were confirmed generated
// scaffolding and deleted; Retained lists the entries left alone and Warnings
// explains why.
type ProjectSpaceTrashSweepResult struct {
	Removed  []string `json:"removed,omitempty"`
	Retained []string `json:"retained,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// SweepProjectSpaceTrash deletes leftover .trash/infra-space-* directories from
// an interrupted empty-space cleanup. A previous process can die between the
// rename into .trash and the final deletion, leaving a directory that the
// manifest no longer references.
//
// Only directories that are positively confirmed as generated scaffolding are
// removed: a root index.md declaring a project, .gitkeep markers and empty
// directories. Anything unknown, unreadable or non-empty is kept and reported
// so the owner can inspect it. The sweep is idempotent; a repeated startup
// finds nothing left to remove.
func SweepProjectSpaceTrash(cfg Config) (ProjectSpaceTrashSweepResult, error) {
	var result ProjectSpaceTrashSweepResult
	cfg, err := cfg.Normalized()
	if err != nil {
		return result, err
	}
	if !VaultInitialized(cfg.VaultPath) {
		return result, nil
	}
	trashDir := filepath.Join(cfg.VaultPath, ".trash")
	entries, err := os.ReadDir(trashDir)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return result, fmt.Errorf("read vault trash directory: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, projectSpaceTrashPrefix) {
			continue
		}
		relative := path.Join(".trash", name)
		entryPath := filepath.Join(trashDir, name)
		info, statErr := os.Lstat(entryPath)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				// Another process finished the sweep between ReadDir and Lstat.
				continue
			}
			result.Retained = append(result.Retained, relative)
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s 无法读取，已保留：%v", relative, statErr))
			continue
		}
		if !info.IsDir() {
			result.Retained = append(result.Retained, relative)
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s 不是目录，已保留", relative))
			continue
		}
		projectID, confirmErr := spaceRootProjectID(entryPath)
		if confirmErr != nil {
			result.Retained = append(result.Retained, relative)
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s 无法确认是空脚手架，已保留：%v", relative, confirmErr))
			continue
		}
		if projectID == "" {
			result.Retained = append(result.Retained, relative)
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s 的根 index.md 未声明项目，已保留", relative))
			continue
		}
		blocking, confirmErr := emptyInfrastructureSpaceFiles(entryPath, projectID)
		if confirmErr != nil {
			result.Retained = append(result.Retained, relative)
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s 无法确认是空脚手架，已保留：%v", relative, confirmErr))
			continue
		}
		if len(blocking) > 0 {
			result.Retained = append(result.Retained, relative)
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s 包含未确认内容，已保留：%s", relative, strings.Join(blocking, ", ")))
			continue
		}
		if removeErr := os.RemoveAll(entryPath); removeErr != nil {
			result.Retained = append(result.Retained, relative)
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s 删除失败，已保留并将在下次启动重试：%v", relative, removeErr))
			continue
		}
		result.Removed = append(result.Removed, relative)
	}
	sort.Strings(result.Removed)
	sort.Strings(result.Retained)
	return result, nil
}

// ManagerSweepProjectSpaceTrash is the Manager entry point. The write lock
// keeps the sweep from racing a provision or removal on the same vault.
func (m *Manager) SweepProjectSpaceTrash() (ProjectSpaceTrashSweepResult, error) {
	if m == nil {
		return ProjectSpaceTrashSweepResult{}, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return SweepProjectSpaceTrash(m.config)
}

// ManagerRemoveEmptyProjectSpace is the Manager entry point. The manager holds
// the write lock across the removal and rebuild so no reader observes a
// half-updated manifest.
func (m *Manager) RemoveEmptyProjectSpace(ctx context.Context, projectID string) (ProjectSpaceCleanupResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !VaultInitialized(m.config.VaultPath) {
		return ProjectSpaceCleanupResult{ProjectID: strings.TrimSpace(projectID)}, nil
	}
	result, needsRebuild, err := removeEmptyProjectSpace(m.config, projectID, time.Now().UTC())
	if err != nil {
		return result, err
	}
	if needsRebuild {
		if _, err := m.rebuildLocked(ctx); err != nil {
			return result, fmt.Errorf("rebuild index after removing project space: %w", err)
		}
	}
	return result, nil
}
