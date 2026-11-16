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

func (s *Service) provisionProjectKnowledgeSpace(ctx context.Context, project domain.Project) error {
	if _, err := s.knowledgeVault.ProvisionProjectSpace(ctx, project.ID, project.Name); err != nil {
		_ = s.store.SetProjectKnowledgeState(ctx, project.ID, domain.KnowledgeStateError)
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
// spaces without a project row; they are reported, never deleted.
type KnowledgeSpaceReconciliation struct {
	Enabled     bool     `json:"enabled"`
	Checked     int      `json:"checked"`
	Ready       int      `json:"ready"`
	Provisioned []string `json:"provisioned,omitempty"`
	MarkedError []string `json:"marked_error,omitempty"`
	Orphans     []string `json:"orphans,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
}

// ReconcileKnowledgeSpaces validates every space at startup: a manifest
// project must have its directory and root index, a ready project must have an
// indexed root, and system/channel hosts get their empty space back if it is
// missing. Unbound normal projects stay uninitialized until the owner creates
// them through the normal path; AHA1 archive residue is not given a new space.
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
	indexedRoots := map[string]bool{}
	_ = s.knowledgeVault.WithReader(func(reader *knowledgevault.Reader) error {
		manifestProjects = reader.Manifest().Projects
		if !reader.IndexReady() {
			return nil
		}
		entries, err := reader.Entries(ctx, "project", "")
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.IsIndex && entry.ProjectID != "" {
				indexedRoots[entry.ProjectID] = true
			}
		}
		return nil
	})
	config := s.knowledgeVault.Config()
	rebuildNeeded := false
	ids := knowledgevault.SortedProjectIDs(manifestProjects)
	manifestKnown := map[string]bool{}
	for _, projectID := range ids {
		manifestKnown[projectID] = true
		project, exists := byID[projectID]
		if !exists {
			report.Orphans = append(report.Orphans, projectID)
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
				report.Warnings = append(report.Warnings, fmt.Sprintf("%s 空间初始化失败：%v", project.ID, err))
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
			if project.KnowledgeState == "" || project.KnowledgeState == domain.KnowledgeStateUninitialized {
				report.Warnings = append(report.Warnings, project.ID+" 尚未初始化知识空间")
			}
		}
	}
	if rebuildNeeded {
		if _, err := s.knowledgeVault.Rebuild(ctx); err != nil {
			return report, fmt.Errorf("reconcile knowledge index: %w", err)
		}
	}
	return report, nil
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
