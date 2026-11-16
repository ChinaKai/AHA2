package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
)

// KnowledgeSpaceError is a project creation/provisioning failure with a stable
// code for the HTTP layer. It is deliberately not a bare error: the owner needs
// to know whether the vault is unavailable (retry later) or the space itself
// failed (retry the provisioning action).
type KnowledgeSpaceError struct {
	Code    string
	Message string
	Err     error
}

func (e *KnowledgeSpaceError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Code
}

func (e *KnowledgeSpaceError) Unwrap() error { return e.Err }

func knowledgeSpaceError(code, message string, err error) *KnowledgeSpaceError {
	return &KnowledgeSpaceError{Code: code, Message: message, Err: err}
}

func (s *Service) projectKnowledgeSpaceEnabled() bool {
	return s != nil && s.knowledgeVault != nil
}

// KnowledgeVaultAvailable reports whether the file vault is the active
// knowledge source. Callers that provision spaces opportunistically use it to
// stay quiet in legacy/test configurations.
func (s *Service) KnowledgeVaultAvailable() bool {
	return s != nil && s.knowledgeVault != nil && s.knowledgeVault.Enabled()
}

// CreateProject creates a project row and initializes its knowledge space in
// the same operation. Model A has no separate space entity: the project row
// itself is the space, and a project is only usable once its manifest mapping,
// root index and index entry are ready. A failure after the row insert leaves
// the project in error so the owner can retry; it never falls back to the
// legacy knowledge tables.
func (s *Service) CreateProject(ctx context.Context, project domain.Project) (domain.Project, error) {
	if !s.projectKnowledgeSpaceEnabled() {
		// Tests and support tools that construct the service without a vault
		// keep the pre-model-A behavior. Production always wires a manager.
		return project, s.store.CreateProject(ctx, project)
	}
	// A deleted project's knowledge lives on under its old project ID in
	// archive_names/archives. Reusing that ID would silently merge two
	// histories (and the manifest only has one archive slot per ID), so a
	// reused ID is refused with a stable code instead of overwriting.
	if archivedSlug, archived := s.archivedProjectSpace(project.ID); archived {
		return domain.Project{}, knowledgeSpaceError(
			"project_id_archived",
			fmt.Sprintf("项目 ID 已被归档知识空间 %q 使用，不能复用；请新建项目或使用其他 ID", archivedSlug), nil)
	}
	if !s.knowledgeVault.Enabled() {
		return domain.Project{}, knowledgeSpaceError(
			"knowledge_vault_disabled",
			"文件知识库尚未启用，无法创建项目；请先在高级设置中启用或初始化文件知识库", knowledgevault.ErrDisabled)
	}
	if err := s.knowledgeVault.CheckProjectSpacePrerequisites(); err != nil {
		return domain.Project{}, knowledgeSpaceError(
			"knowledge_space_not_ready",
			"文件知识库不可用，项目未创建："+err.Error(), err)
	}
	project.KnowledgeState = domain.KnowledgeStateInitializing
	if err := s.store.CreateProject(ctx, project); err != nil {
		return domain.Project{}, err
	}
	if err := s.provisionProjectKnowledgeSpace(ctx, project); err != nil {
		project.KnowledgeState = domain.KnowledgeStateError
		return project, err
	}
	project.KnowledgeState = domain.KnowledgeStateReady
	return project, nil
}

// archivedProjectSpace reports the archive slug already registered for one
// project ID. A missing vault or an unreadable manifest means "not archived":
// provisioning will surface a broken vault through its own prerequisite check.
func (s *Service) archivedProjectSpace(projectID string) (string, bool) {
	projectID = strings.TrimSpace(projectID)
	if s == nil || s.knowledgeVault == nil || projectID == "" {
		return "", false
	}
	return knowledgevault.ArchivedProjectSpace(s.knowledgeVault.Config(), projectID)
}

func (s *Service) provisionProjectKnowledgeSpace(ctx context.Context, project domain.Project) error {
	if _, err := s.knowledgeVault.ProvisionProjectSpace(ctx, project.ID, project.Name); err != nil {
		_ = s.store.SetProjectKnowledgeState(ctx, project.ID, domain.KnowledgeStateError)
		if errors.Is(err, knowledgevault.ErrProjectIDArchived) {
			return knowledgeSpaceError(
				"project_id_archived",
				"项目 ID 已被归档知识空间占用，不能复用，也无法自动恢复；请删除这个项目行或改用新的项目 ID。归档内容仍可在知识库中浏览："+err.Error(), err)
		}
		return knowledgeSpaceError(
			"knowledge_space_init_failed",
			"项目已创建，但知识空间初始化失败，可重试："+err.Error(), err)
	}
	if err := s.store.SetProjectKnowledgeState(ctx, project.ID, domain.KnowledgeStateReady); err != nil {
		return knowledgeSpaceError("knowledge_space_state_failed", "知识空间已创建，但保存状态失败："+err.Error(), err)
	}
	return nil
}

// UpdateProject persists a project edit and, when the project already has a
// vault space, refreshes the manifest display name. The slug and files never
// move on rename.
func (s *Service) UpdateProject(ctx context.Context, project domain.Project) error {
	if err := s.store.UpdateProject(ctx, project); err != nil {
		return err
	}
	if s.projectKnowledgeSpaceEnabled() && s.knowledgeVault.Enabled() && project.KnowledgeState == domain.KnowledgeStateReady {
		if err := s.knowledgeVault.RenameProjectSpace(project.ID, project.Name); err != nil {
			return fmt.Errorf("项目已保存，但更新知识库显示名失败：%w", err)
		}
	}
	return nil
}

// RetryProjectKnowledgeSpace re-runs the whole initialization for a project
// whose first attempt failed. It is idempotent: an existing manifest mapping,
// directory and root index are reused.
func (s *Service) RetryProjectKnowledgeSpace(ctx context.Context, projectID string) (domain.Project, error) {
	if !s.projectKnowledgeSpaceEnabled() {
		return domain.Project{}, knowledgeSpaceError("knowledge_vault_disabled", "当前未配置文件知识库", knowledgevault.ErrDisabled)
	}
	if !s.knowledgeVault.Enabled() {
		return domain.Project{}, knowledgeSpaceError("knowledge_vault_disabled", "文件知识库尚未启用", knowledgevault.ErrDisabled)
	}
	project, err := s.store.Project(ctx, strings.TrimSpace(projectID))
	if err != nil {
		return domain.Project{}, err
	}
	if project.KnowledgeState == domain.KnowledgeStateReady {
		// A synced project can arrive marked ready while this device only has
		// an archive for its ID. Trusting the flag would report success and
		// defer the failure to the first knowledge write.
		if archivedPath, archived := s.archivedProjectSpace(project.ID); archived {
			_ = s.store.SetProjectKnowledgeState(ctx, project.ID, domain.KnowledgeStateError)
			project.KnowledgeState = domain.KnowledgeStateError
			return project, knowledgeSpaceError(
				"project_id_archived",
				fmt.Sprintf("项目标记为 ready，但本地 live 知识空间不在 manifest 中，且项目 ID 已被归档空间 %s 占用，无法自动恢复；请删除这个项目行或改用新的项目 ID。归档内容仍可在知识库中浏览。", archivedPath),
				knowledgevault.ErrProjectIDArchived)
		}
		return project, nil
	}
	if err := s.store.SetProjectKnowledgeState(ctx, project.ID, domain.KnowledgeStateInitializing); err != nil {
		return domain.Project{}, err
	}
	project.KnowledgeState = domain.KnowledgeStateInitializing
	if err := s.provisionProjectKnowledgeSpace(ctx, project); err != nil {
		project.KnowledgeState = domain.KnowledgeStateError
		return project, err
	}
	project.KnowledgeState = domain.KnowledgeStateReady
	return project, nil
}

// KnowledgeSpaceReconciliation is the startup report. Orphans are manifest
// spaces without a project row; they are archived (never deleted) so a failed
// project deletion is retried. Orphans lists only the spaces whose archive
// attempt failed and therefore still need attention.
type KnowledgeSpaceReconciliation struct {
	Enabled     bool     `json:"enabled"`
	Checked     int      `json:"checked"`
	Ready       int      `json:"ready"`
	Provisioned []string `json:"provisioned,omitempty"`
	Archived    []string `json:"archived,omitempty"`
	MarkedError []string `json:"marked_error,omitempty"`
	Orphans     []string `json:"orphans,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
}

// ReconcileKnowledgeSpaces validates every space at startup: a manifest
// project must have its directory and root index, a ready project must have an
// indexed root, and every non-knowledge project that predates model A gets its
// empty space created (a previous failure is retried). AHA1 archive residue is
// never given a new active space.
func (s *Service) ReconcileKnowledgeSpaces(ctx context.Context) (KnowledgeSpaceReconciliation, error) {
	report := KnowledgeSpaceReconciliation{}
	if !s.projectKnowledgeSpaceEnabled() || !s.knowledgeVault.Enabled() {
		return report, nil
	}
	report.Enabled = true
	projects, err := s.store.ListProjects(ctx)
	if err != nil {
		return report, err
	}
	report.Checked = len(projects)
	byID := make(map[string]domain.Project, len(projects))
	for _, project := range projects {
		byID[project.ID] = project
	}
	manifestProjects := map[string]string{}
	manifestArchives := map[string]string{}
	config := s.knowledgeVault.Config()
	// Reconcile from the manifest currently on disk. The live Reader may have
	// been opened before an archive/provision step, and a stale snapshot would
	// make reconciliation act on mappings that no longer exist (or miss ones
	// that do).
	if manifest, manifestErr := knowledgevault.ReadManifest(config.VaultPath); manifestErr == nil {
		manifestProjects = manifest.Projects
		manifestArchives = manifest.Archives
	}
	indexedRoots := map[string]bool{}
	indexedArchives := map[string]bool{}
	projectEntries := map[string]bool{}
	_ = s.knowledgeVault.WithReader(func(reader *knowledgevault.Reader) error {
		if !reader.IndexReady() {
			return nil
		}
		entries, err := reader.Entries(ctx, "project", "")
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.ProjectID != "" {
				projectEntries[entry.ProjectID] = true
			}
			if entry.IsIndex && entry.ProjectID != "" {
				indexedRoots[entry.ProjectID] = true
			}
		}
		archiveEntries, err := reader.Entries(ctx, "archive", "")
		if err != nil {
			return err
		}
		for _, entry := range archiveEntries {
			if entry.ProjectID != "" {
				indexedArchives[entry.ProjectID] = true
			}
		}
		return nil
	})
	rebuildNeeded := false
	ids := knowledgevault.SortedProjectIDs(manifestProjects)
	manifestKnown := map[string]bool{}
	for _, projectID := range ids {
		manifestKnown[projectID] = true
		project, exists := byID[projectID]
		if !exists {
			// The project row is gone but its space is still registered. This
			// is the retry path for a deletion whose archive step failed (or
			// for a pre-existing orphan). Archive it; never delete files.
			archivedPath, archiveErr := s.knowledgeVault.ArchiveProjectSpace(ctx, projectID)
			if archiveErr != nil {
				report.Orphans = append(report.Orphans, projectID)
				report.Warnings = append(report.Warnings, fmt.Sprintf("项目 %s 已不存在，但知识空间归档失败，将在下次启动重试：%v", projectID, archiveErr))
				continue
			}
			report.Archived = append(report.Archived, projectID)
			if archivedPath != "" {
				report.Warnings = append(report.Warnings, fmt.Sprintf("项目 %s 已不存在，知识空间已归档到 %s", projectID, archivedPath))
			}
			continue
		}
		rootRelative, _ := knowledgevault.SpaceRootPath(config, projectID)
		rootAbsolute := filepath.Join(config.VaultPath, filepath.FromSlash(rootRelative))
		if _, statErr := os.Stat(rootAbsolute); statErr != nil {
			if os.IsNotExist(statErr) {
				report.MarkedError = append(report.MarkedError, projectID)
				_ = s.store.SetProjectKnowledgeState(ctx, projectID, domain.KnowledgeStateError)
				report.Warnings = append(report.Warnings, fmt.Sprintf("projects/%s: 根 index.md 缺失，标记为 error", manifestProjects[projectID]))
			} else {
				report.MarkedError = append(report.MarkedError, projectID)
				report.Warnings = append(report.Warnings, fmt.Sprintf("项目 %s 的根 index.md 不可读：%v", projectID, statErr))
			}
			continue
		}
		// The project row is authoritative for the display name; refresh the
		// manifest cache so a rename survives even if no rebuild happens.
		if err := s.knowledgeVault.RenameProjectSpace(projectID, project.Name); err != nil {
			report.Warnings = append(report.Warnings, fmt.Sprintf("项目 %s 的 manifest 显示名更新失败：%v", projectID, err))
		}
		if project.KnowledgeState != domain.KnowledgeStateReady {
			_ = s.store.SetProjectKnowledgeState(ctx, projectID, domain.KnowledgeStateReady)
			rebuildNeeded = true
		} else if !indexedRoots[projectID] {
			// Ready in the project row but missing from the index: the file
			// exists, so one rebuild restores the root entry.
			rebuildNeeded = true
			report.Warnings = append(report.Warnings, fmt.Sprintf("项目 %s 的知识空间缺少索引根条目，已安排重建", projectID))
		}
		report.Ready++
	}
	// An archive can be registered while the index still shows the old
	// project entries: the archive rename and manifest update succeeded but
	// the rebuild failed. The project mapping is already gone, so this state
	// is invisible to the project loop above. One rebuild reconciles both the
	// missing archive root and the stale project rows.
	for _, archiveID := range knowledgevault.SortedProjectIDs(manifestArchives) {
		archiveSlug := strings.TrimSpace(manifestArchives[archiveID])
		archiveDir := filepath.Join(config.VaultPath, "archive", filepath.FromSlash(archiveSlug))
		if info, statErr := os.Stat(archiveDir); statErr != nil || !info.IsDir() {
			// The files were removed by hand; a rebuild drops the residual
			// manifest mapping instead of keeping a dead archive library.
			rebuildNeeded = true
			report.Warnings = append(report.Warnings, fmt.Sprintf("归档 %s 的目录不存在，重建会移除残留映射", archiveID))
			continue
		}
		hasRootIndex := true
		if _, rootErr := os.Stat(filepath.Join(archiveDir, "index.md")); os.IsNotExist(rootErr) {
			hasRootIndex = false
			// createMissingIndexPages deliberately skips archive/, so a
			// rebuild cannot create this page. Report it instead of silently
			// accepting an archive whose documents have no browsable root.
			report.Warnings = append(report.Warnings, fmt.Sprintf("归档 %s 缺少根 index.md；重建不会为 archive/ 自动补根页，这是不可自动修复状态，需要手工补", archiveID))
		}
		if !indexedArchives[archiveID] {
			// Without a root page a rebuild cannot make the archive fully
			// browsable. Do not trigger a full rebuild on every startup just
			// for that state; a rebuild is still needed when stale live rows
			// exist.
			if !hasRootIndex && !projectEntries[archiveID] {
				continue
			}
			rebuildNeeded = true
			report.Warnings = append(report.Warnings, fmt.Sprintf("归档 %s 缺少索引条目，已安排重建", archiveID))
			continue
		}
		if projectEntries[archiveID] {
			rebuildNeeded = true
			report.Warnings = append(report.Warnings, fmt.Sprintf("归档 %s 仍残留旧项目索引条目，已安排重建", archiveID))
		}
	}
	for _, project := range projects {
		if manifestKnown[project.ID] {
			continue
		}
		switch project.ProjectType {
		case "channel", "system":
			// The channel host is a default/identity space and the pet keeps
			// its own space. Both are infrastructure and are never selected as
			// ordinary projects, but their spaces must exist.
			_ = s.store.SetProjectKnowledgeState(ctx, project.ID, domain.KnowledgeStateInitializing)
			if _, err := s.knowledgeVault.ProvisionProjectSpace(ctx, project.ID, project.Name); err != nil {
				_ = s.store.SetProjectKnowledgeState(ctx, project.ID, domain.KnowledgeStateError)
				report.MarkedError = append(report.MarkedError, project.ID)
				report.Warnings = append(report.Warnings, knowledgeSpaceProvisionWarning(project.ID, err))
				continue
			}
			_ = s.store.SetProjectKnowledgeState(ctx, project.ID, domain.KnowledgeStateReady)
			report.Provisioned = append(report.Provisioned, project.ID)
			report.Ready++
		case "knowledge":
			// AHA1 archive residue has no workspace and no knowledge; the
			// dedicated channel library is provisioned by the cutover, not by
			// every startup. Only report here.
			report.Warnings = append(report.Warnings, project.ID+" 是知识项目但尚无 vault 空间，等待迁移/清理")
		default:
			// Model A gives every normal project an empty space. Projects that
			// predate the model are initialized here once; a previous failure is
			// retried on every startup until it reaches ready.
			if project.KnowledgeState == domain.KnowledgeStateReady {
				// Do not trust ready blindly: a sync replay can resurrect a
				// ready row whose ID only exists as an archive on this device.
				if archivedPath, archived := s.archivedProjectSpace(project.ID); archived {
					_ = s.store.SetProjectKnowledgeState(ctx, project.ID, domain.KnowledgeStateError)
					report.MarkedError = append(report.MarkedError, project.ID)
					report.Warnings = append(report.Warnings, fmt.Sprintf(
						"%s 状态为 ready，但 live 知识空间不在 manifest 中且项目 ID 已被归档空间 %s 占用（project_id_archived）；请删除这个项目行或改用新的项目 ID",
						project.ID, archivedPath))
				}
				continue
			}
			_ = s.store.SetProjectKnowledgeState(ctx, project.ID, domain.KnowledgeStateInitializing)
			if _, err := s.knowledgeVault.ProvisionProjectSpace(ctx, project.ID, project.Name); err != nil {
				_ = s.store.SetProjectKnowledgeState(ctx, project.ID, domain.KnowledgeStateError)
				report.MarkedError = append(report.MarkedError, project.ID)
				report.Warnings = append(report.Warnings, knowledgeSpaceProvisionWarning(project.ID, err))
				continue
			}
			_ = s.store.SetProjectKnowledgeState(ctx, project.ID, domain.KnowledgeStateReady)
			report.Provisioned = append(report.Provisioned, project.ID)
			report.Ready++
		}
	}
	if rebuildNeeded {
		if _, err := s.knowledgeVault.Rebuild(ctx); err != nil {
			return report, fmt.Errorf("reconcile knowledge index: %w", err)
		}
	}
	return report, nil
}

// knowledgeSpaceProvisionWarning turns a provisioning error into a startup
// warning. An archived ID is not retryable: the row must be removed or renamed
// to a new ID, so the warning carries that recovery path explicitly.
func knowledgeSpaceProvisionWarning(projectID string, err error) string {
	if errors.Is(err, knowledgevault.ErrProjectIDArchived) {
		return fmt.Sprintf("%s 的项目 ID 已被归档知识空间占用（project_id_archived），无法自动初始化；请删除这个项目行或改用新的项目 ID", projectID)
	}
	return fmt.Sprintf("%s 空间初始化失败：%v", projectID, err)
}

// KnowledgeSpaceStatus reports readiness for one project without mutating it.
func (s *Service) KnowledgeSpaceStatus(ctx context.Context, projectID string) (domain.Project, error) {
	return s.store.Project(ctx, strings.TrimSpace(projectID))
}

// IsKnowledgeSpaceNotReadyError lets HTTP handlers map an error without
// matching implementation details.
func IsKnowledgeSpaceNotReadyError(err error) bool {
	var spaceErr *KnowledgeSpaceError
	if !errors.As(err, &spaceErr) {
		return false
	}
	switch spaceErr.Code {
	case "knowledge_space_not_ready", "knowledge_vault_disabled":
		return true
	default:
		return false
	}
}

// IsKnowledgeSpaceConflictError reports a client-correctable project/space
// conflict: the vault is unavailable, or the requested project ID is already
// reserved by an archived space. HTTP handlers return 409 for these.
func IsKnowledgeSpaceConflictError(err error) bool {
	if IsKnowledgeSpaceNotReadyError(err) {
		return true
	}
	var spaceErr *KnowledgeSpaceError
	if !errors.As(err, &spaceErr) {
		return false
	}
	return spaceErr.Code == "project_id_archived"
}
