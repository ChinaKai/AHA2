package knowledgevault

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Vault skills are ordinary file packages (SKILL.md plus text assets) under
// global/skills/<slug>/ or projects/<manifest-slug>/skills/<slug>/. The legacy
// skills table is no longer a write target: every create, update and delete
// goes through this file-first service, which serializes with knowledge writes
// on the same Manager lock.
const (
	SkillScopeGlobal  = "global"
	SkillScopeProject = "project"

	maxSkillNameRunes         = 200
	maxSkillDescriptionRunes  = 2000
	maxSkillInstructionsRunes = 200000
	maxSkillPackageFiles      = 200
	maxSkillPackageTotalSize  = 16 << 20
	maxSkillSlugRunes         = 100

	skillWriteErrorInvalid     = "skill_write_invalid"
	skillWriteErrorForbidden   = "skill_write_forbidden"
	skillWriteErrorDuplicate   = "skill_write_duplicate"
	skillWriteErrorConflict    = "skill_write_conflict"
	skillWriteErrorNotFound    = "skill_write_not_found"
	skillWriteErrorDisabled    = "skill_write_disabled"
	skillWriteErrorUnavailable = "skill_write_unavailable"

	skillTrashDirectory = ".trash/skills"
)

// SkillWriteError carries a stable machine code so the HTTP layer can map a
// rejected write to a status without parsing messages.
type SkillWriteError struct {
	Code           string
	Message        string
	CurrentVersion int
}

func (e *SkillWriteError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Message) == "" {
		return e.Code
	}
	return e.Code + ": " + e.Message
}

func skillWriteError(code, format string, args ...any) *SkillWriteError {
	return &SkillWriteError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// SkillWriteRequest is the server-side form of a vault skill create/update.
// Files is optional: when empty the service renders SKILL.md from
// Name/Description/Instructions and preserves every other existing file on an
// update.
type SkillWriteRequest struct {
	Key          string
	Scope        string
	ProjectID    string
	Slug         string
	Name         string
	Description  string
	Instructions string
	Files        []SkillPackageFile
	BaseVersion  int
	Enabled      *bool
	Status       string
}

// SkillWriteResult describes the package after a successful write.
type SkillWriteResult struct {
	Key          string   `json:"key"`
	ID           string   `json:"id"`
	Slug         string   `json:"slug"`
	Scope        string   `json:"scope"`
	ProjectID    string   `json:"project_id,omitempty"`
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	Version      int      `json:"version"`
	RelativePath string   `json:"relative_path"`
	Files        []string `json:"files,omitempty"`
}

// CreateSkillPackage writes a new package. An existing slug is rejected: the
// caller should update the skill it already sees instead of forking identity.
func (m *Manager) CreateSkillPackage(ctx context.Context, request SkillWriteRequest) (SkillWriteResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.skillWritePreconditionsLocked(); err != nil {
		return SkillWriteResult{}, err
	}
	request = normalizeSkillWriteRequest(request)
	if request.Scope == "" {
		request.Scope = SkillScopeProject
	}
	if err := m.validateSkillScopeLocked(request.Scope, request.ProjectID); err != nil {
		return SkillWriteResult{}, err
	}
	if err := validateSkillFields(request.Name, request.Description, request.Instructions); err != nil {
		return SkillWriteResult{}, err
	}
	slug := normalizeSkillSlug(request.Slug, request.Name)
	if err := validateSkillSlug(slug); err != nil {
		return SkillWriteResult{}, err
	}
	files, err := m.skillFilesForCreateLocked(request, slug)
	if err != nil {
		return SkillWriteResult{}, err
	}
	directory, err := m.skillLibraryDirectoryLocked(request.Scope, request.ProjectID)
	if err != nil {
		return SkillWriteResult{}, err
	}
	target := filepath.Join(directory, slug)
	if _, statErr := os.Lstat(target); statErr == nil {
		return SkillWriteResult{}, skillWriteError(skillWriteErrorDuplicate, "skill %q already exists in this library", slug)
	} else if !os.IsNotExist(statErr) {
		return SkillWriteResult{}, statErr
	}
	if request.Scope == SkillScopeGlobal {
		// Reserve slugs from the retired global library so an owner can move
		// a retained package into the canonical location without a collision.
		flat := filepath.Join(m.config.VaultPath, "skills", slug)
		if info, flatErr := os.Lstat(flat); flatErr == nil && info.IsDir() {
			return SkillWriteResult{}, skillWriteError(skillWriteErrorDuplicate, "skill %q already exists in the flat skills library", slug)
		}
	}
	if err := m.writeSkillPackageLocked(target, files); err != nil {
		return SkillWriteResult{}, err
	}
	result := m.skillWriteResultLocked(request.Scope, request.ProjectID, slug, target, files)
	m.requestAutoCommit()
	return result, nil
}

// UpdateSkillPackage replaces a package in place. A positive BaseVersion is a
// compare-and-swap guard; zero means "the caller already resolved the current
// package and accepts it" (the Web editor does not track revisions).
func (m *Manager) UpdateSkillPackage(ctx context.Context, request SkillWriteRequest) (SkillWriteResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.skillWritePreconditionsLocked(); err != nil {
		return SkillWriteResult{}, err
	}
	request = normalizeSkillWriteRequest(request)
	existing, err := m.resolveSkillLocked(ctx, request.Key)
	if err != nil {
		return SkillWriteResult{}, err
	}
	if existing.Scope != SkillScopeGlobal && existing.Scope != SkillScopeProject {
		return SkillWriteResult{}, skillWriteError(skillWriteErrorForbidden, "archive skills are read-only")
	}
	if request.Scope != "" && request.Scope != existing.Scope {
		return SkillWriteResult{}, skillWriteError(skillWriteErrorForbidden, "skill scope does not match the requested scope")
	}
	if request.ProjectID != "" && existing.Scope == SkillScopeProject && request.ProjectID != existing.ProjectID {
		return SkillWriteResult{}, skillWriteError(skillWriteErrorForbidden, "skill belongs to another project")
	}
	currentVersion := existing.Version
	if currentVersion <= 0 {
		currentVersion = 1
	}
	if request.BaseVersion > 0 && request.BaseVersion != currentVersion {
		conflict := skillWriteError(skillWriteErrorConflict, "base_version %d does not match current version %d; re-read the skill and retry", request.BaseVersion, currentVersion)
		conflict.CurrentVersion = currentVersion
		return SkillWriteResult{}, conflict
	}
	name, description, instructions := request.Name, request.Description, request.Instructions
	if name == "" {
		name = existing.Name
	}
	if description == "" {
		description = existing.Description
	}
	if instructions == "" {
		instructions = strings.TrimSpace(existing.Instructions)
	}
	if err := validateSkillFields(name, description, instructions); err != nil {
		return SkillWriteResult{}, err
	}
	enabled := existing.Enabled
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	status := strings.TrimSpace(request.Status)
	if status == "" {
		status = existing.Status
	}
	files, err := m.skillFilesForUpdateLocked(existing, name, description, instructions, currentVersion+1, enabled, status, request.Files)
	if err != nil {
		return SkillWriteResult{}, err
	}
	target := filepath.Join(m.config.VaultPath, filepath.FromSlash(existing.RelativePath))
	if len(request.Files) == 0 {
		// A field-only update rewrites SKILL.md in place and leaves every other
		// asset untouched, including binaries that the text-only package
		// contract would otherwise drop.
		content := ""
		for _, file := range files {
			if file.Path == "SKILL.md" {
				content = file.Content
				break
			}
		}
		if content == "" {
			return SkillWriteResult{}, skillWriteError(skillWriteErrorInvalid, "SKILL.md is required")
		}
		entry := filepath.Join(target, "SKILL.md")
		if err := writeVaultFileIfChanged(m.config.VaultPath, entry, []byte(content), 0o600); err != nil {
			return SkillWriteResult{}, fmt.Errorf("write skill entry: %w", err)
		}
		refreshed, err := m.resolveSkillLocked(ctx, existing.Key)
		if err != nil {
			return SkillWriteResult{}, err
		}
		m.requestAutoCommit()
		return skillResultFromPackage(refreshed), nil
	}
	if err := m.writeSkillPackageLocked(target, files); err != nil {
		return SkillWriteResult{}, err
	}
	refreshed, err := m.resolveSkillLocked(ctx, existing.Key)
	if err != nil {
		return SkillWriteResult{}, err
	}
	m.requestAutoCommit()
	return skillResultFromPackage(refreshed), nil
}

// DeleteSkillPackage moves one package into the vault trash. It never removes
// files physically, so the owner can recover a package that was deleted by
// mistake. Archive libraries are read-only and refused.
func (m *Manager) DeleteSkillPackage(ctx context.Context, key string) (SkillWriteResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.skillWritePreconditionsLocked(); err != nil {
		return SkillWriteResult{}, err
	}
	existing, err := m.resolveSkillLocked(ctx, key)
	if err != nil {
		return SkillWriteResult{}, err
	}
	if existing.Scope != SkillScopeGlobal && existing.Scope != SkillScopeProject {
		return SkillWriteResult{}, skillWriteError(skillWriteErrorForbidden, "archive skills are read-only")
	}
	target := filepath.Join(m.config.VaultPath, filepath.FromSlash(existing.RelativePath))
	info, err := os.Lstat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return SkillWriteResult{}, skillWriteError(skillWriteErrorNotFound, "skill package is missing from the vault")
		}
		return SkillWriteResult{}, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return SkillWriteResult{}, skillWriteError(skillWriteErrorForbidden, "skill package is not a regular directory")
	}
	trashRoot := filepath.Join(m.config.VaultPath, filepath.FromSlash(skillTrashDirectory))
	if err := os.MkdirAll(trashRoot, 0o700); err != nil {
		return SkillWriteResult{}, fmt.Errorf("create skill trash directory: %w", err)
	}
	if !IsInsideRoot(m.config.VaultPath, trashRoot) {
		return SkillWriteResult{}, skillWriteError(skillWriteErrorForbidden, "skill trash directory resolves outside the vault")
	}
	scopeDir := existing.Scope
	if existing.Scope == SkillScopeProject {
		scopeDir = "project-" + SafeSegment(existing.ProjectID, "project")
	}
	destinationDir := filepath.Join(trashRoot, scopeDir)
	if err := os.MkdirAll(destinationDir, 0o700); err != nil {
		return SkillWriteResult{}, fmt.Errorf("create skill trash scope directory: %w", err)
	}
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	destination := filepath.Join(destinationDir, existing.Slug+"-"+stamp)
	if err := os.Rename(target, destination); err != nil {
		return SkillWriteResult{}, fmt.Errorf("move skill package to trash: %w", err)
	}
	result := SkillWriteResult{
		Key: existing.Key, ID: existing.ID, Slug: existing.Slug, Scope: existing.Scope,
		ProjectID: existing.ProjectID, Name: existing.Name, Description: existing.Description,
		Version: existing.Version, RelativePath: existing.RelativePath, Files: existing.Files,
	}
	m.requestAutoCommit()
	return result, nil
}

func (m *Manager) skillWritePreconditionsLocked() error {
	if !m.enabled || m.reader == nil {
		return skillWriteError(skillWriteErrorDisabled, "knowledge vault is disabled")
	}
	if !m.runtimeWrite {
		return skillWriteError(skillWriteErrorDisabled, "the knowledge vault write gate is off")
	}
	if !VaultInitialized(m.config.VaultPath) {
		return skillWriteError(skillWriteErrorUnavailable, "knowledge vault is not initialized")
	}
	return nil
}

func normalizeSkillWriteRequest(request SkillWriteRequest) SkillWriteRequest {
	request.Key = strings.TrimSpace(request.Key)
	request.Scope = strings.ToLower(strings.TrimSpace(request.Scope))
	request.ProjectID = strings.TrimSpace(request.ProjectID)
	request.Slug = strings.TrimSpace(request.Slug)
	request.Name = strings.TrimSpace(request.Name)
	request.Description = strings.TrimSpace(strings.ReplaceAll(request.Description, "\r\n", "\n"))
	request.Instructions = strings.TrimSpace(strings.ReplaceAll(request.Instructions, "\r\n", "\n"))
	request.Status = strings.ToLower(strings.TrimSpace(request.Status))
	return request
}

func (m *Manager) validateSkillScopeLocked(scope, projectID string) error {
	switch scope {
	case SkillScopeGlobal:
		return nil
	case SkillScopeProject:
		if projectID == "" {
			return skillWriteError(skillWriteErrorInvalid, "project scope requires a project id")
		}
		slug := strings.TrimSpace(m.reader.Manifest().Projects[projectID])
		if slug == "" {
			return skillWriteError(skillWriteErrorForbidden, "project %s is not bound to a vault library", projectID)
		}
		directory := filepath.Join(m.config.VaultPath, "projects", slug)
		info, err := os.Lstat(directory)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return skillWriteError(skillWriteErrorForbidden, "project %s has no writable vault folder", projectID)
		}
		return nil
	default:
		return skillWriteError(skillWriteErrorForbidden, "scope must be project or global")
	}
}

func (m *Manager) skillLibraryDirectoryLocked(scope, projectID string) (string, error) {
	switch scope {
	case SkillScopeGlobal:
		return filepath.Join(m.config.VaultPath, "global", "skills"), nil
	case SkillScopeProject:
		slug := strings.TrimSpace(m.reader.Manifest().Projects[projectID])
		if slug == "" {
			return "", skillWriteError(skillWriteErrorForbidden, "project %s is not bound to a vault library", projectID)
		}
		return filepath.Join(m.config.VaultPath, "projects", slug, "skills"), nil
	default:
		return "", skillWriteError(skillWriteErrorForbidden, "scope must be project or global")
	}
}

// resolveSkillLocked finds one package by its stable key. The key embeds the
// project id, not the vault slug, so a project rename does not orphan a task.
func (m *Manager) resolveSkillLocked(ctx context.Context, key string) (SkillPackage, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return SkillPackage{}, skillWriteError(skillWriteErrorInvalid, "skill key is required")
	}
	response, err := m.reader.Skills(ctx)
	if err != nil {
		return SkillPackage{}, fmt.Errorf("list vault skills: %w", err)
	}
	for _, skill := range response.Skills {
		if skill.Key == key {
			return skill, nil
		}
	}
	return SkillPackage{}, skillWriteError(skillWriteErrorNotFound, "skill %s was not found in the vault", key)
}

func (m *Manager) skillFilesForCreateLocked(request SkillWriteRequest, slug string) ([]SkillPackageFile, error) {
	if len(request.Files) > 0 {
		files, err := validateSkillPackageFiles(request.Files)
		if err != nil {
			return nil, err
		}
		document, err := parseRenderedSkillFile(files)
		if err != nil {
			return nil, err
		}
		if request.Name != "" && !strings.EqualFold(document.Name, request.Name) {
			return nil, skillWriteError(skillWriteErrorInvalid, "SKILL.md name does not match the request name")
		}
		// Normalize the entry frontmatter so a caller-supplied SKILL.md cannot
		// skip the server-managed version of a new package.
		return normalizeSkillEntry(files, document.Name, document.Description, document.Instructions, 1, true, "active")
	}
	content, err := renderSkillDocument(request.Name, request.Description, request.Instructions, 1, true, "active")
	if err != nil {
		return nil, err
	}
	return []SkillPackageFile{{Path: "SKILL.md", Content: string(content)}}, nil
}

func (m *Manager) skillFilesForUpdateLocked(existing SkillPackage, name, description, instructions string, version int, enabled bool, status string, requested []SkillPackageFile) ([]SkillPackageFile, error) {
	if len(requested) > 0 {
		files, err := validateSkillPackageFiles(requested)
		if err != nil {
			return nil, err
		}
		document, err := parseRenderedSkillFile(files)
		if err != nil {
			return nil, err
		}
		// The server owns the revision counter: replace whatever version a
		// caller put in SKILL.md with the next one. Other files are kept as-is.
		return normalizeSkillEntry(files, document.Name, document.Description, document.Instructions, version, enabled, status)
	}
	// A field-only update rewrites SKILL.md and nothing else, so only SKILL.md
	// is validated. Checking the files already on disk against the rules for
	// incoming files rejected the whole update whenever a package carried
	// something those rules refuse as input (a dot-file such as .gitkeep or
	// .claude/notes.md, or more assets than a request may send), even though
	// those files were never going to be written.
	content, err := renderSkillDocument(name, description, instructions, version, enabled, status)
	if err != nil {
		return nil, err
	}
	return validateSkillPackageFiles([]SkillPackageFile{{Path: "SKILL.md", Content: string(content)}})
}

// normalizeSkillEntry rebuilds SKILL.md from parsed fields so the server-owned
// version/enabled/status frontmatter is authoritative, then validates the
// complete package again.
func normalizeSkillEntry(files []SkillPackageFile, name, description, instructions string, version int, enabled bool, status string) ([]SkillPackageFile, error) {
	content, err := renderSkillDocument(name, description, instructions, version, enabled, status)
	if err != nil {
		return nil, err
	}
	normalized := make([]SkillPackageFile, 0, len(files))
	for _, file := range files {
		if file.Path == "SKILL.md" {
			continue
		}
		normalized = append(normalized, file)
	}
	normalized = append(normalized, SkillPackageFile{Path: "SKILL.md", Content: string(content)})
	return validateSkillPackageFiles(normalized)
}

// writeSkillPackageLocked stages the package beside the live directory and
// swaps it in with a rename. A failed second rename restores the previous
// package; a failed write leaves the live directory untouched. Only the target
// package and the hidden staging directory are ever modified.
func (m *Manager) writeSkillPackageLocked(target string, files []SkillPackageFile) error {
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create skill library directory: %w", err)
	}
	if !IsInsideRoot(m.config.VaultPath, parent) {
		return skillWriteError(skillWriteErrorForbidden, "skill library resolves outside the vault")
	}
	if info, err := os.Lstat(parent); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return skillWriteError(skillWriteErrorForbidden, "skill library is not a regular directory")
	}
	if info, err := os.Lstat(target); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return skillWriteError(skillWriteErrorForbidden, "skill package target is not a regular directory")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	stagingRoot := filepath.Join(m.config.VaultPath, ".aha", "skill-staging")
	if err := os.MkdirAll(stagingRoot, 0o700); err != nil {
		return fmt.Errorf("create skill staging directory: %w", err)
	}
	stage, err := os.MkdirTemp(stagingRoot, "new-")
	if err != nil {
		return fmt.Errorf("stage skill package: %w", err)
	}
	defer os.RemoveAll(stage)
	for _, file := range files {
		destination := filepath.Join(stage, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return fmt.Errorf("create skill package directory: %w", err)
		}
		if err := os.WriteFile(destination, []byte(file.Content), 0o600); err != nil {
			return fmt.Errorf("write skill package file: %w", err)
		}
	}
	backup := ""
	if _, statErr := os.Lstat(target); statErr == nil {
		backup = stage + "-old"
		if err := os.Rename(target, backup); err != nil {
			return fmt.Errorf("move existing skill package aside: %w", err)
		}
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	if err := os.Rename(stage, target); err != nil {
		if backup != "" {
			_ = os.Rename(backup, target)
		}
		return fmt.Errorf("install skill package: %w", err)
	}
	if backup != "" {
		_ = os.RemoveAll(backup)
	}
	return nil
}

func skillResultFromPackage(skill SkillPackage) SkillWriteResult {
	return SkillWriteResult{
		Key: skill.Key, ID: skill.ID, Slug: skill.Slug, Scope: skill.Scope,
		ProjectID: skill.ProjectID, Name: skill.Name, Description: skill.Description,
		Version: skill.Version, RelativePath: skill.RelativePath,
		Files: append([]string{}, skill.Files...),
	}
}

func (m *Manager) skillWriteResultLocked(scope, projectID, slug, target string, files []SkillPackageFile) SkillWriteResult {
	result := SkillWriteResult{
		Scope: scope, ProjectID: projectID, Slug: slug,
		RelativePath: m.skillRelativePath(scope, projectID, slug),
	}
	if abs, err := filepath.Abs(target); err == nil {
		result.RelativePath = strings.TrimPrefix(filepath.ToSlash(abs), filepath.ToSlash(filepath.Clean(m.config.VaultPath))+"/")
	}
	switch scope {
	case SkillScopeGlobal:
		result.Key = VaultSkillIDPrefix + "global:" + slug
	case SkillScopeProject:
		result.Key = VaultSkillIDPrefix + "project:" + projectID + ":" + slug
	}
	result.ID = result.Key
	document, _ := skillDocumentFromFiles(files)
	result.Name = document.Name
	result.Description = document.Description
	result.Version = document.Version
	for _, file := range files {
		result.Files = append(result.Files, file.Path)
	}
	sortStrings(result.Files)
	return result
}

func (m *Manager) skillRelativePath(scope, projectID, slug string) string {
	switch scope {
	case SkillScopeGlobal:
		return path.Join("global", "skills", slug)
	case SkillScopeProject:
		projectSlug := strings.TrimSpace(m.reader.Manifest().Projects[projectID])
		return path.Join("projects", projectSlug, "skills", slug)
	default:
		return path.Join(scope, slug)
	}
}

func normalizeSkillSlug(value, name string) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}
	return Slugify(name)
}

func validateSkillSlug(slug string) error {
	if slug == "" || slug == "." || slug == ".." {
		return skillWriteError(skillWriteErrorInvalid, "skill slug is required")
	}
	if len([]rune(slug)) > maxSkillSlugRunes {
		return skillWriteError(skillWriteErrorInvalid, "skill slug is too long")
	}
	if strings.ContainsAny(slug, `/\`) || strings.ContainsRune(slug, 0) {
		return skillWriteError(skillWriteErrorInvalid, "skill slug must be a single path segment")
	}
	if strings.HasPrefix(slug, ".") {
		return skillWriteError(skillWriteErrorInvalid, "skill slug may not start with a dot")
	}
	if err := ValidateRelativePath(slug); err != nil {
		return skillWriteError(skillWriteErrorInvalid, "invalid skill slug: %v", err)
	}
	return nil
}

func validateSkillFields(name, description, instructions string) error {
	if strings.TrimSpace(name) == "" || len([]rune(name)) > maxSkillNameRunes {
		return skillWriteError(skillWriteErrorInvalid, "skill name must be 1-%d characters", maxSkillNameRunes)
	}
	if strings.ContainsAny(name, "\r\n") {
		return skillWriteError(skillWriteErrorInvalid, "skill name must be a single line")
	}
	if strings.TrimSpace(description) == "" || len([]rune(description)) > maxSkillDescriptionRunes || strings.ContainsAny(description, "\r\n") {
		return skillWriteError(skillWriteErrorInvalid, "skill description must be a non-empty single line of at most %d characters", maxSkillDescriptionRunes)
	}
	trimmed := strings.TrimSpace(instructions)
	if trimmed == "" || len([]rune(trimmed)) > maxSkillInstructionsRunes {
		return skillWriteError(skillWriteErrorInvalid, "skill instructions must be 1-%d characters", maxSkillInstructionsRunes)
	}
	if !utf8.ValidString(trimmed) || strings.ContainsRune(trimmed, 0) {
		return skillWriteError(skillWriteErrorInvalid, "skill instructions must be valid UTF-8 without NUL bytes")
	}
	return nil
}

// validateSkillPackageFiles enforces the text-only package contract. It is
// deliberately stricter than the legacy loader: hidden segments, traversal,
// NUL bytes, duplicate paths and oversized files are rejected instead of being
// skipped, so a caller never believes content was stored when it was not.
func validateSkillPackageFiles(files []SkillPackageFile) ([]SkillPackageFile, error) {
	if len(files) == 0 {
		return nil, skillWriteError(skillWriteErrorInvalid, "skill package has no files")
	}
	if len(files) > maxSkillPackageFiles {
		return nil, skillWriteError(skillWriteErrorInvalid, "skill package has too many files (max %d)", maxSkillPackageFiles)
	}
	seen := map[string]bool{}
	total := 0
	hasEntry := false
	result := make([]SkillPackageFile, 0, len(files))
	for _, file := range files {
		rawPath := strings.ReplaceAll(strings.TrimSpace(file.Path), `\`, "/")
		if rawPath == "" || strings.HasPrefix(rawPath, "/") || strings.ContainsRune(rawPath, 0) {
			return nil, skillWriteError(skillWriteErrorInvalid, "invalid skill package path %q", file.Path)
		}
		clean := path.Clean(rawPath)
		if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
			return nil, skillWriteError(skillWriteErrorInvalid, "invalid skill package path %q", file.Path)
		}
		for _, segment := range strings.Split(clean, "/") {
			if segment == "" || segment == "." || segment == ".." || strings.HasPrefix(segment, ".") {
				return nil, skillWriteError(skillWriteErrorInvalid, "invalid skill package path %q", file.Path)
			}
		}
		if err := ValidateRelativePath(clean); err != nil {
			return nil, skillWriteError(skillWriteErrorInvalid, "invalid skill package path %q: %v", file.Path, err)
		}
		if seen[clean] {
			return nil, skillWriteError(skillWriteErrorInvalid, "duplicate skill package path %q", clean)
		}
		seen[clean] = true
		if len([]byte(file.Content)) > maxSkillPackageFileSize {
			return nil, skillWriteError(skillWriteErrorInvalid, "skill package file %q is too large", clean)
		}
		if !utf8.ValidString(file.Content) || strings.ContainsRune(file.Content, 0) {
			return nil, skillWriteError(skillWriteErrorInvalid, "skill package file %q is not UTF-8 text", clean)
		}
		total += len([]byte(file.Content))
		hasEntry = hasEntry || clean == "SKILL.md"
		result = append(result, SkillPackageFile{Path: clean, Content: file.Content})
	}
	if !hasEntry {
		return nil, skillWriteError(skillWriteErrorInvalid, "SKILL.md is required")
	}
	if total > maxSkillPackageTotalSize {
		return nil, skillWriteError(skillWriteErrorInvalid, "skill package is too large")
	}
	if _, err := parseRenderedSkillFile(result); err != nil {
		return nil, err
	}
	return result, nil
}

type skillDocument struct {
	Name         string
	Description  string
	Instructions string
	Version      int
	Enabled      bool
	Status       string
}

func parseRenderedSkillFile(files []SkillPackageFile) (skillDocument, error) {
	for _, file := range files {
		if file.Path != "SKILL.md" {
			continue
		}
		return parseSkillDocument(file.Content)
	}
	return skillDocument{}, skillWriteError(skillWriteErrorInvalid, "SKILL.md is required")
}

func parseSkillDocument(raw string) (skillDocument, error) {
	document := ParseDocument(raw)
	if !document.HasFrontmatter {
		return skillDocument{}, skillWriteError(skillWriteErrorInvalid, "SKILL.md requires YAML frontmatter")
	}
	name := strings.TrimSpace(document.Frontmatter.Title)
	if value, ok := document.Frontmatter.Extra["name"].(string); ok && strings.TrimSpace(value) != "" {
		name = strings.TrimSpace(value)
	}
	description := ""
	if value, ok := document.Frontmatter.Extra["description"].(string); ok {
		description = strings.TrimSpace(value)
	}
	instructions := strings.TrimSpace(document.Body)
	if name == "" || description == "" || instructions == "" {
		return skillDocument{}, skillWriteError(skillWriteErrorInvalid, "SKILL.md frontmatter requires name and description and a non-empty body")
	}
	if err := validateSkillFields(name, description, instructions); err != nil {
		return skillDocument{}, err
	}
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
	return skillDocument{Name: name, Description: description, Instructions: instructions, Version: version, Enabled: enabled, Status: status}, nil
}

func skillDocumentFromFiles(files []SkillPackageFile) (skillDocument, error) {
	document, err := parseRenderedSkillFile(files)
	if err != nil {
		return skillDocument{}, err
	}
	return document, nil
}

func renderSkillDocument(name, description, instructions string, version int, enabled bool, status string) ([]byte, error) {
	frontmatter := map[string]any{
		"name": name, "description": description, "version": version,
	}
	if !enabled {
		frontmatter["enabled"] = false
	}
	if status != "" && status != "active" {
		frontmatter["status"] = status
	}
	encoded, err := yaml.Marshal(frontmatter)
	if err != nil {
		return nil, fmt.Errorf("render skill frontmatter: %w", err)
	}
	return []byte("---\n" + string(encoded) + "---\n\n" + strings.TrimSpace(instructions) + "\n"), nil
}

func intFromAny(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case uint64:
		return int(typed), true
	case float64:
		return int(typed), true
	case string:
		var parsed int
		if _, err := fmt.Sscanf(strings.TrimSpace(typed), "%d", &parsed); err == nil {
			return parsed, true
		}
	}
	return 0, false
}

func boolFromAny(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case int:
		return typed != 0, true
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1", "yes", "on":
			return true, true
		case "false", "0", "no", "off":
			return false, true
		}
	}
	return false, false
}

func sortStrings(values []string) {
	for left := 0; left < len(values); left++ {
		for right := left + 1; right < len(values); right++ {
			if values[right] < values[left] {
				values[left], values[right] = values[right], values[left]
			}
		}
	}
}

// SkillWriteErrorCode returns the stable code of a skill write failure.
func SkillWriteErrorCode(err error) string {
	var target *SkillWriteError
	if errors.As(err, &target) {
		return target.Code
	}
	return ""
}

// Exported code accessors keep the HTTP layer free of duplicated strings.
func SkillWriteErrorInvalidCode() string     { return skillWriteErrorInvalid }
func SkillWriteErrorForbiddenCode() string   { return skillWriteErrorForbidden }
func SkillWriteErrorDuplicateCode() string   { return skillWriteErrorDuplicate }
func SkillWriteErrorConflictCode() string    { return skillWriteErrorConflict }
func SkillWriteErrorNotFoundCode() string    { return skillWriteErrorNotFound }
func SkillWriteErrorDisabledCode() string    { return skillWriteErrorDisabled }
func SkillWriteErrorUnavailableCode() string { return skillWriteErrorUnavailable }
