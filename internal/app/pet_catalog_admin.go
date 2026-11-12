package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
	workspacepkg "github.com/ChinaKai/AHA2/internal/workspace"
)

func (s *Service) petManageableProject(ctx context.Context, projectID string) (domain.Project, domain.OwnerAssistant, error) {
	assistant, err := s.petAssistant(ctx)
	if err != nil {
		return domain.Project{}, domain.OwnerAssistant{}, err
	}
	if !assistant.PetAllowCreateWorkspaces {
		return domain.Project{}, domain.OwnerAssistant{}, fmt.Errorf("主人未开启主动管理项目/Workspace 权限")
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return domain.Project{}, domain.OwnerAssistant{}, fmt.Errorf("project_id 不能为空")
	}
	if projectID == petProjectID {
		return domain.Project{}, domain.OwnerAssistant{}, fmt.Errorf("不能修改宠物系统项目")
	}
	if s.store.IsManagedChannelProject(ctx, projectID) {
		return domain.Project{}, domain.OwnerAssistant{}, fmt.Errorf("渠道宿主项目只能在渠道管理流程中修改")
	}
	project, err := s.store.Project(ctx, projectID)
	if err != nil {
		return domain.Project{}, domain.OwnerAssistant{}, fmt.Errorf("项目不存在")
	}
	if project.ProjectType == "knowledge" {
		return domain.Project{}, domain.OwnerAssistant{}, fmt.Errorf("知识项目不能通过宠物管理")
	}
	return project, assistant, nil
}

type PetUpdateProjectInput struct {
	ProjectID          string  `json:"project_id"`
	Name               *string `json:"name"`
	Description        *string `json:"description"`
	ProjectType        *string `json:"project_type"`
	RepositoryIdentity *string `json:"repository_identity"`
	DefaultBranch      *string `json:"default_branch"`
	KnowledgePolicy    *string `json:"knowledge_policy"`
}

func (s *Service) PetUpdateProject(ctx context.Context, input PetUpdateProjectInput) (domain.Project, error) {
	project, _, err := s.petManageableProject(ctx, input.ProjectID)
	if err != nil {
		return domain.Project{}, err
	}
	if input.Name != nil {
		name := strings.TrimSpace(*input.Name)
		if name == "" || len([]rune(name)) > 120 {
			return project, fmt.Errorf("项目名称不能为空且不能超过 120 个字符")
		}
		project.Name = name
	}
	if input.Description != nil {
		project.Description = strings.TrimSpace(*input.Description)
	}
	if input.ProjectType != nil {
		projectType := strings.TrimSpace(*input.ProjectType)
		if projectType != "folder" && projectType != "git" {
			return project, fmt.Errorf("project_type 必须是 folder 或 git")
		}
		project.ProjectType = projectType
	}
	if input.RepositoryIdentity != nil {
		project.RepositoryIdentity = strings.TrimSpace(*input.RepositoryIdentity)
	}
	if input.DefaultBranch != nil {
		project.DefaultBranch = strings.TrimSpace(*input.DefaultBranch)
	}
	if input.KnowledgePolicy != nil {
		policy := strings.TrimSpace(*input.KnowledgePolicy)
		if policy != "disabled" {
			policy = "enabled"
		}
		project.KnowledgePolicy = policy
	}
	project.UpdatedAt = s.now().UTC()
	if err := s.store.UpdateProject(ctx, project); err != nil {
		return project, err
	}
	return project, nil
}

func (s *Service) PetDeleteProject(ctx context.Context, projectID string, reason ...string) (domain.PetApproval, error) {
	reasonText, reasonErr := requiredPetApprovalReason(reason...)
	if reasonErr != nil {
		return domain.PetApproval{}, reasonErr
	}
	project, _, err := s.petManageableProject(ctx, projectID)
	if err != nil {
		return domain.PetApproval{}, err
	}
	return s.CreatePetApproval(ctx, PetApprovalInput{
		Kind: "delete_project", Title: "删除项目「" + project.Name + "」",
		Summary: "宠物请求删除项目及其本地 Task/Workspace，需要主人确认。",
		Reason:  reasonText, Risk: "high", Payload: mustJSON(map[string]string{"project_id": project.ID}),
	})
}

func (s *Service) deleteProjectNow(ctx context.Context, projectID string) error {
	project, _, err := s.petManageableProject(ctx, projectID)
	if err != nil {
		return err
	}
	// Snapshot the Tasks and Workspaces before the transaction commits so their
	// scratch directories and SSH credentials can be removed afterwards. The
	// store deletes the whole graph in one transaction, so a refusal leaves the
	// project, its Tasks and its Workspaces intact.
	tasks, err := s.store.ListTasks(ctx, "")
	if err != nil {
		return err
	}
	workspaces, _ := s.store.ListWorkspaces(ctx, "")
	if err := s.store.DeleteProjectWithSyncTombstone(ctx, project.ID); err != nil {
		switch {
		case errors.Is(err, store.ErrProjectHasActiveTurn):
			return fmt.Errorf("项目下有执行中的任务，请先中断或等待完成")
		case errors.Is(err, store.ErrWorkspaceInUse):
			return fmt.Errorf("项目的 Workspace 仍被其他任务或运行记录引用")
		}
		return err
	}
	for _, task := range tasks {
		if task.ProjectID != project.ID {
			continue
		}
		if cleanupErr := s.CleanupTaskScratch(ctx, task); cleanupErr != nil {
			log.Printf("cleanup task scratch %s: %v", task.ID, cleanupErr)
		}
	}
	for _, workspace := range workspaces {
		if workspace.ProjectID == project.ID {
			s.deleteWorkspaceSecret(workspace)
		}
	}
	_ = s.store.AppendPetEvent(ctx, domain.PetEvent{
		ID: domain.NewID("pet_event"), At: s.now().UTC(), Actor: "pet", Kind: "project_deleted",
		Detail: project.ID, Status: project.Name,
	})
	return nil
}

func (s *Service) petManageableWorkspace(ctx context.Context, workspaceID string) (domain.Workspace, domain.OwnerAssistant, error) {
	assistant, err := s.petAssistant(ctx)
	if err != nil {
		return domain.Workspace{}, domain.OwnerAssistant{}, err
	}
	if !assistant.PetAllowCreateWorkspaces {
		return domain.Workspace{}, domain.OwnerAssistant{}, fmt.Errorf("主人未开启主动管理项目/Workspace 权限")
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return domain.Workspace{}, domain.OwnerAssistant{}, fmt.Errorf("workspace_id 不能为空")
	}
	if workspaceID == petWorkspaceID {
		return domain.Workspace{}, domain.OwnerAssistant{}, fmt.Errorf("不能修改宠物系统 Workspace")
	}
	if s.store.IsManagedChannelWorkspace(ctx, workspaceID) {
		return domain.Workspace{}, domain.OwnerAssistant{}, fmt.Errorf("渠道宿主 Workspace 只能在渠道管理流程中修改")
	}
	workspace, err := s.store.Workspace(ctx, workspaceID)
	if err != nil {
		return domain.Workspace{}, domain.OwnerAssistant{}, fmt.Errorf("Workspace 不存在")
	}
	if workspace.ReadOnly {
		return domain.Workspace{}, domain.OwnerAssistant{}, fmt.Errorf("该 Workspace 只读")
	}
	return workspace, assistant, nil
}

type PetUpdateWorkspaceInput struct {
	WorkspaceID      string  `json:"workspace_id"`
	Name             *string `json:"name"`
	RootPath         *string `json:"root_path"`
	Transport        *string `json:"transport"`
	Distro           *string `json:"distro"`
	SSHHost          *string `json:"ssh_host"`
	SSHUser          *string `json:"ssh_user"`
	SSHPort          *int    `json:"ssh_port"`
	SSHAuth          *string `json:"ssh_auth"`
	SSHPassword      *string `json:"ssh_password"`
	ClearSSHPassword bool    `json:"clear_ssh_password"`
}

func (s *Service) PetUpdateWorkspace(ctx context.Context, input PetUpdateWorkspaceInput) (domain.Workspace, error) {
	workspace, _, err := s.petManageableWorkspace(ctx, input.WorkspaceID)
	if err != nil {
		return domain.Workspace{}, err
	}
	if input.Name != nil {
		name := strings.TrimSpace(*input.Name)
		if name == "" || len([]rune(name)) > 120 {
			return workspace, fmt.Errorf("Workspace 名称不能为空且不能超过 120 个字符")
		}
		workspace.Name = name
	}
	if input.Transport != nil {
		transport := strings.ToLower(strings.TrimSpace(*input.Transport))
		if transport != "native" && transport != "wsl" && transport != "ssh" {
			return workspace, fmt.Errorf("transport 必须是 native、wsl 或 ssh")
		}
		workspace.Transport = transport
	}
	if input.RootPath != nil {
		rootPath := strings.TrimSpace(*input.RootPath)
		if rootPath == "" {
			return workspace, fmt.Errorf("root_path 不能为空")
		}
		if workspace.Transport == "native" {
			workspace.RootPath = filepath.Clean(rootPath)
		} else {
			workspace.RootPath = path.Clean(rootPath)
		}
	}
	if input.Distro != nil {
		workspace.Distro = strings.TrimSpace(*input.Distro)
	}
	oldCredentialRef := workspace.SSHCredentialRef
	if workspace.Transport == "ssh" {
		workspace.Locality = "remote"
		if input.SSHHost != nil {
			workspace.SSHHost = strings.TrimSpace(*input.SSHHost)
		}
		if input.SSHUser != nil {
			workspace.SSHUser = strings.TrimSpace(*input.SSHUser)
		}
		if input.SSHPort != nil {
			workspace.SSHPort = *input.SSHPort
		}
		if workspace.SSHPort == 0 {
			workspace.SSHPort = 22
		}
		if input.SSHAuth != nil {
			workspace.SSHAuth = petNormalizeSSHAuth(*input.SSHAuth)
		} else if workspace.SSHAuth == "" {
			workspace.SSHAuth = "auto"
		}
		if workspace.SSHHost == "" || workspace.SSHUser == "" || workspace.SSHPort < 1 || workspace.SSHPort > 65535 {
			return workspace, fmt.Errorf("SSH Host、User 和有效 Port 不能为空")
		}
		if input.ClearSSHPassword {
			workspace.SSHCredentialRef = ""
			workspace.SSHPasswordConfigured = false
		} else if input.SSHPassword != nil && strings.TrimSpace(*input.SSHPassword) != "" {
			workspace.SSHCredentialRef = "workspace/" + workspace.ID + "/ssh/credential"
			workspace.SSHPasswordConfigured = true
			if s.secrets != nil {
				if err := s.secrets.PutMany(map[string]string{workspace.SSHCredentialRef: strings.TrimSpace(*input.SSHPassword)}); err != nil {
					return workspace, err
				}
			}
		}
		if workspace.SSHAuth == "password" && !workspace.SSHPasswordConfigured {
			return workspace, fmt.Errorf("密码认证需要提供 SSH 密码")
		}
	} else {
		workspace.Locality = "local"
		workspace.SSHHost = ""
		workspace.SSHUser = ""
		workspace.SSHPort = 0
		workspace.SSHAuth = ""
		workspace.SSHCredentialRef = ""
		workspace.SSHPasswordConfigured = false
	}
	if workspace.Transport == "native" {
		info, statErr := os.Stat(workspace.RootPath)
		if statErr != nil || !info.IsDir() {
			return workspace, fmt.Errorf("native Workspace 路径必须已经存在且是目录")
		}
	}
	workspace.Health = "unknown"
	workspace.UpdatedAt = s.now().UTC()
	if err := s.store.UpdateWorkspaceConfig(ctx, workspace); err != nil {
		if workspace.SSHCredentialRef != "" && workspace.SSHCredentialRef != oldCredentialRef && s.secrets != nil {
			_ = s.secrets.DeleteMany([]string{workspace.SSHCredentialRef})
		}
		return workspace, err
	}
	if oldCredentialRef != "" && oldCredentialRef != workspace.SSHCredentialRef && s.secrets != nil {
		_ = s.secrets.DeleteMany([]string{oldCredentialRef})
	}
	return s.store.Workspace(ctx, workspace.ID)
}

func (s *Service) PetDeleteWorkspace(ctx context.Context, workspaceID string, reason ...string) (domain.PetApproval, error) {
	reasonText, reasonErr := requiredPetApprovalReason(reason...)
	if reasonErr != nil {
		return domain.PetApproval{}, reasonErr
	}
	workspace, _, err := s.petManageableWorkspace(ctx, workspaceID)
	if err != nil {
		return domain.PetApproval{}, err
	}
	return s.CreatePetApproval(ctx, PetApprovalInput{
		Kind: "delete_workspace", Title: "删除 Workspace「" + workspace.Name + "」",
		Summary: "宠物请求删除 Workspace 及其本地 Task，需要主人确认。",
		Reason:  reasonText, Risk: "high", Payload: mustJSON(map[string]string{"workspace_id": workspace.ID}),
	})
}

func (s *Service) deleteWorkspaceNow(ctx context.Context, workspaceID string) error {
	workspace, _, err := s.petManageableWorkspace(ctx, workspaceID)
	if err != nil {
		return err
	}
	// Snapshot the Tasks before the transaction so their scratch directories can
	// be removed after the rows are gone. The store deletes the Tasks, their sync
	// tombstones and the Workspace in one transaction, so a refusal leaves both
	// sides intact.
	tasks, _ := s.store.ListTasks(ctx, "")
	if err := s.store.DeleteWorkspaceWithSyncTombstone(ctx, workspace.ID); err != nil {
		switch {
		case errors.Is(err, store.ErrWorkspaceHasActiveTurn):
			return fmt.Errorf("Workspace 下有执行中的任务，请先中断或等待完成")
		case errors.Is(err, store.ErrWorkspaceInUse):
			return fmt.Errorf("Workspace 仍被其他 Workspace 的任务或运行记录引用")
		}
		return err
	}
	for _, task := range tasks {
		if task.WorkspaceID != workspace.ID {
			continue
		}
		if cleanupErr := s.CleanupTaskScratchIn(ctx, workspace, task); cleanupErr != nil {
			log.Printf("cleanup task scratch %s: %v", task.ID, cleanupErr)
		}
	}
	s.deleteWorkspaceSecret(workspace)
	_ = s.store.AppendPetEvent(ctx, domain.PetEvent{
		ID: domain.NewID("pet_event"), At: s.now().UTC(), Actor: "pet", Kind: "workspace_deleted",
		Detail: workspace.ID, Status: workspace.Name,
	})
	return nil
}

func (s *Service) PetDetectWorkspace(ctx context.Context, workspaceID string) (domain.Workspace, error) {
	workspace, _, err := s.petManageableWorkspace(ctx, workspaceID)
	if err != nil {
		return domain.Workspace{}, err
	}
	detected, detectErr := workspacepkg.Detect(ctx, workspace)
	if detected.ID == "" {
		detected = workspace
	}
	if detected.SSHCredentialRef != "" && s.secrets != nil {
		detected.SSHPassword, _ = s.secrets.Get(detected.SSHCredentialRef)
	}
	if err := s.store.UpdateWorkspaceDetection(ctx, detected); err != nil {
		return detected, err
	}
	if detectErr != nil {
		return s.store.Workspace(ctx, detected.ID)
	}
	return s.store.Workspace(ctx, detected.ID)
}

func (s *Service) deleteWorkspaceSecret(workspace domain.Workspace) {
	if s.secrets == nil || workspace.SSHCredentialRef == "" {
		return
	}
	_ = s.secrets.DeleteMany([]string{workspace.SSHCredentialRef})
}

func mustJSON(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}
