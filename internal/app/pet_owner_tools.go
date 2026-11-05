package app

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/ChinaKai/AHA2/internal/domain"
)

type PetWorkspaceBrief struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Locality      string `json:"locality,omitempty"`
	Transport     string `json:"transport,omitempty"`
	Health        string `json:"health,omitempty"`
	ReadOnly      bool   `json:"read_only,omitempty"`
	OwnerDeviceID string `json:"owner_device_id,omitempty"`
}

type PetProjectBrief struct {
	ID              string              `json:"id"`
	Name            string              `json:"name"`
	ProjectType     string              `json:"project_type,omitempty"`
	KnowledgePolicy string              `json:"knowledge_policy,omitempty"`
	Workspaces      []PetWorkspaceBrief `json:"workspaces,omitempty"`
}

// PetProjectCatalog is the read-only project/workspace directory the pet needs
// before it can create a task. It follows the same view-tasks permission as task
// details and excludes the pet's own system project.
func (s *Service) PetProjectCatalog(ctx context.Context) ([]PetProjectBrief, error) {
	assistant, err := s.petAssistant(ctx)
	if err != nil {
		return nil, err
	}
	if !assistant.PetAllowViewTasks && !assistant.PetAllowCreateTasks && !assistant.PetAllowCreateWorkspaces {
		return nil, fmt.Errorf("主人未开启查看项目/任务信息权限")
	}
	projects, err := s.store.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	workspaces, err := s.store.ListWorkspaces(ctx, "")
	if err != nil {
		return nil, err
	}
	byProject := map[string][]PetWorkspaceBrief{}
	for _, workspace := range workspaces {
		byProject[workspace.ProjectID] = append(byProject[workspace.ProjectID], PetWorkspaceBrief{
			ID: workspace.ID, Name: workspace.Name, Locality: workspace.Locality,
			Transport: workspace.Transport, Health: workspace.Health,
			ReadOnly: workspace.ReadOnly, OwnerDeviceID: workspace.OwnerDeviceID,
		})
	}
	result := make([]PetProjectBrief, 0, len(projects))
	for _, project := range projects {
		if project.ID == petProjectID || project.ProjectType == "knowledge" {
			continue
		}
		result = append(result, PetProjectBrief{
			ID: project.ID, Name: project.Name, ProjectType: project.ProjectType,
			KnowledgePolicy: project.KnowledgePolicy, Workspaces: byProject[project.ID],
		})
	}
	if len(result) > 40 {
		result = result[:40]
	}
	return result, nil
}

type PetCreateTaskInput struct {
	ProjectID       string `json:"project_id"`
	WorkspaceID     string `json:"workspace_id"`
	Title           string `json:"title"`
	Request         string `json:"request"`
	ModelID         string `json:"model_id"`
	ReasoningEffort string `json:"reasoning_effort"`
	Filesystem      string `json:"filesystem"`
	Approval        string `json:"approval"`
	Isolation       string `json:"isolation"`
	Start           *bool  `json:"start"`
	// SkillIDs are requested on top of the project's auto-attach Skills, the
	// same default CreateTask gives every internal caller. Each ID is validated
	// against the project/global scope; an unavailable one fails the request
	// instead of being dropped.
	SkillIDs []string `json:"skill_ids"`
}

// Pet-created code work defaults to the strongest runtime the owner asked for:
// high reasoning and full filesystem access. The pet may lower either per Task
// when the work is trivial, but must pass the values explicitly.
const (
	petCreatedTaskReasoningEffort = "high"
	petCreatedTaskFilesystem      = "danger-full-access"
	petCreatedTaskApproval        = "never"
)

func (s *Service) PetCreateTask(ctx context.Context, input PetCreateTaskInput) (domain.Task, error) {
	assistant, err := s.petAssistant(ctx)
	if err != nil {
		return domain.Task{}, err
	}
	if !assistant.PetAllowCreateTasks {
		return domain.Task{}, fmt.Errorf("主人未开启主动创建任务权限")
	}
	projectID := strings.TrimSpace(input.ProjectID)
	workspaceID := strings.TrimSpace(input.WorkspaceID)
	title := strings.TrimSpace(input.Title)
	request := strings.TrimSpace(input.Request)
	if projectID == "" || workspaceID == "" {
		return domain.Task{}, fmt.Errorf("project_id 和 workspace_id 不能为空")
	}
	if title == "" || len([]rune(title)) > 120 {
		return domain.Task{}, fmt.Errorf("任务标题不能为空且不能超过 120 个字符")
	}
	if request == "" || len([]rune(request)) > 20000 {
		return domain.Task{}, fmt.Errorf("任务需求不能为空且不能超过 20000 个字符")
	}
	modelID := strings.TrimSpace(input.ModelID)
	if modelID == "" {
		modelID = assistant.ModelID
	}
	reasoningEffort := strings.ToLower(strings.TrimSpace(input.ReasoningEffort))
	if reasoningEffort == "" {
		reasoningEffort = petCreatedTaskReasoningEffort
	}
	switch reasoningEffort {
	case "low", "medium", "high", "xhigh", "max":
	default:
		return domain.Task{}, fmt.Errorf("reasoning_effort 必须是 low、medium、high、xhigh 或 max")
	}
	filesystem := strings.TrimSpace(input.Filesystem)
	if filesystem == "" {
		filesystem = petCreatedTaskFilesystem
	}
	switch filesystem {
	case "read-only", "workspace-write", "danger-full-access":
	default:
		return domain.Task{}, fmt.Errorf("filesystem 必须是 read-only、workspace-write 或 danger-full-access")
	}
	approval := strings.TrimSpace(input.Approval)
	if approval == "" {
		approval = petCreatedTaskApproval
	}
	switch approval {
	case "never", "auto":
	default:
		return domain.Task{}, fmt.Errorf("approval 必须是 never 或 auto")
	}
	isolation := strings.ToLower(strings.TrimSpace(input.Isolation))
	if isolation == "" {
		isolation = "worktree"
	}
	switch isolation {
	case "worktree", "inplace":
	default:
		return domain.Task{}, fmt.Errorf("isolation 必须是 worktree 或 inplace")
	}
	project, err := s.store.Project(ctx, projectID)
	if err != nil {
		return domain.Task{}, fmt.Errorf("项目不存在")
	}
	workspace, err := s.store.Workspace(ctx, workspaceID)
	if err != nil {
		return domain.Task{}, fmt.Errorf("Workspace 不存在")
	}
	if workspace.ReadOnly {
		return domain.Task{}, fmt.Errorf("该 Workspace 只读，不能创建任务")
	}
	task, err := s.CreateTask(ctx, CreateTaskInput{
		ProjectID: projectID, WorkspaceID: workspaceID, Title: title, Request: request,
		ModelID: modelID, ReasoningEffort: reasoningEffort, Filesystem: filesystem, Approval: approval,
		Isolation: isolation, CollaborationMode: "single", MaxAgents: 1, KnowledgePolicy: project.KnowledgePolicy,
		StartMode: "manual", SkillIDs: input.SkillIDs,
	})
	if err != nil {
		return domain.Task{}, err
	}
	s.watchPetTask(ctx, task.ID, "created")
	start := true
	if input.Start != nil {
		start = *input.Start
	}
	if start {
		started, startErr := s.StartTask(ctx, task.ID)
		if startErr != nil {
			return task, fmt.Errorf("任务 %s 已创建但启动失败：%w", task.ID, startErr)
		}
		task = started
		if _, err := s.SubmitPetDelegatedMessage(ctx, task.ID, request); err != nil {
			return task, fmt.Errorf("任务 %s 已启动但首条消息提交失败：%w", task.ID, err)
		}
	}
	s.recordPetWork(ctx, "task_created", task.ID, string(task.Status))
	return task, nil
}

func (s *Service) PetStartTask(ctx context.Context, taskID string) (domain.Task, error) {
	assistant, err := s.petAssistant(ctx)
	if err != nil {
		return domain.Task{}, err
	}
	if !assistant.PetAllowCreateTasks {
		return domain.Task{}, fmt.Errorf("主人未开启主动创建任务权限")
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return domain.Task{}, fmt.Errorf("task_id 不能为空")
	}
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		return domain.Task{}, fmt.Errorf("Task 不存在")
	}
	if task.ReadOnly {
		return task, fmt.Errorf("该 Task 只读，不能启动")
	}
	if task.Status != domain.TaskDraft {
		return task, fmt.Errorf("Task 状态为 %s，只有草稿可以启动", task.Status)
	}
	s.watchPetTask(ctx, task.ID, "started")
	started, err := s.StartTask(ctx, task.ID)
	if err != nil {
		return task, err
	}
	// The Web creation flow submits the requirement as the first message. A pet
	// starting an existing draft should produce the same runnable Task, not an
	// active Task with no Turn.
	if turns, turnErr := s.store.TaskTurnCount(ctx, started.ID); turnErr == nil && turns == 0 {
		if strings.TrimSpace(started.OriginalRequest) != "" {
			if _, err := s.SubmitPetDelegatedMessage(ctx, started.ID, started.OriginalRequest); err != nil {
				return started, fmt.Errorf("Task 已启动但首条消息提交失败：%w", err)
			}
		}
	}
	s.recordPetWork(ctx, "task_started", started.ID, string(started.Status))
	return started, nil
}

type PetCreateProjectInput struct {
	Name               string `json:"name"`
	Description        string `json:"description"`
	ProjectType        string `json:"project_type"`
	RepositoryIdentity string `json:"repository_identity"`
	DefaultBranch      string `json:"default_branch"`
	KnowledgePolicy    string `json:"knowledge_policy"`
}

func (s *Service) PetCreateProject(ctx context.Context, input PetCreateProjectInput) (domain.Project, error) {
	assistant, err := s.petAssistant(ctx)
	if err != nil {
		return domain.Project{}, err
	}
	if !assistant.PetAllowCreateWorkspaces {
		return domain.Project{}, fmt.Errorf("主人未开启主动创建项目/Workspace 权限")
	}
	name := strings.TrimSpace(input.Name)
	if name == "" || len([]rune(name)) > 120 {
		return domain.Project{}, fmt.Errorf("项目名称不能为空且不能超过 120 个字符")
	}
	projectType := strings.TrimSpace(input.ProjectType)
	if projectType == "" {
		projectType = "folder"
	}
	if projectType != "folder" && projectType != "git" {
		return domain.Project{}, fmt.Errorf("project_type 必须是 folder 或 git")
	}
	policy := strings.TrimSpace(input.KnowledgePolicy)
	if policy != "disabled" {
		policy = "enabled"
	}
	now := s.now().UTC()
	project := domain.Project{
		ID: domain.NewID("project"), Name: name, Description: strings.TrimSpace(input.Description),
		ProjectType: projectType, RepositoryIdentity: strings.TrimSpace(input.RepositoryIdentity),
		DefaultBranch: strings.TrimSpace(input.DefaultBranch), KnowledgePolicy: policy,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.store.CreateProject(ctx, project); err != nil {
		return domain.Project{}, err
	}
	_ = s.store.AppendPetEvent(ctx, domain.PetEvent{
		ID: domain.NewID("pet_event"), At: now, Actor: "pet", Kind: "project_created",
		Detail: project.ID, Status: project.Name,
	})
	return project, nil
}

type PetCreateWorkspaceInput struct {
	ProjectID   string `json:"project_id"`
	Name        string `json:"name"`
	Transport   string `json:"transport"`
	RootPath    string `json:"root_path"`
	Distro      string `json:"distro"`
	SSHHost     string `json:"ssh_host"`
	SSHUser     string `json:"ssh_user"`
	SSHPort     int    `json:"ssh_port"`
	SSHAuth     string `json:"ssh_auth"`
	SSHPassword string `json:"ssh_password"`
}

func (s *Service) PetCreateWorkspace(ctx context.Context, input PetCreateWorkspaceInput) (domain.Workspace, error) {
	assistant, err := s.petAssistant(ctx)
	if err != nil {
		return domain.Workspace{}, err
	}
	if !assistant.PetAllowCreateWorkspaces {
		return domain.Workspace{}, fmt.Errorf("主人未开启主动创建项目/Workspace 权限")
	}
	projectID := strings.TrimSpace(input.ProjectID)
	name := strings.TrimSpace(input.Name)
	rootPath := strings.TrimSpace(input.RootPath)
	if projectID == "" || name == "" || rootPath == "" {
		return domain.Workspace{}, fmt.Errorf("project_id、名称和 root_path 不能为空")
	}
	if len([]rune(name)) > 120 {
		return domain.Workspace{}, fmt.Errorf("Workspace 名称不能超过 120 个字符")
	}
	if _, err := s.store.Project(ctx, projectID); err != nil {
		return domain.Workspace{}, fmt.Errorf("项目不存在")
	}
	transport := strings.ToLower(strings.TrimSpace(input.Transport))
	if transport == "" {
		transport = "native"
	}
	if transport != "native" && transport != "wsl" && transport != "ssh" {
		return domain.Workspace{}, fmt.Errorf("transport 必须是 native、wsl 或 ssh")
	}
	health := "unknown"
	locality := "local"
	if transport == "native" {
		info, statErr := os.Stat(rootPath)
		if statErr != nil || !info.IsDir() {
			return domain.Workspace{}, fmt.Errorf("native Workspace 路径必须已经存在且是目录")
		}
		health = "ready"
	}
	cleanRoot := filepath.Clean(rootPath)
	if transport == "wsl" {
		cleanRoot = path.Clean(rootPath)
	}
	now := s.now().UTC()
	workspace := domain.Workspace{
		ID: domain.NewID("workspace"), ProjectID: projectID, Name: name,
		Locality: locality, Transport: transport, RootPath: cleanRoot,
		Distro: strings.TrimSpace(input.Distro), Health: health,
		AgentAPIMode: "auto", CreatedAt: now, UpdatedAt: now,
	}
	if transport == "ssh" {
		workspace.Locality = "remote"
		workspace.SSHHost = strings.TrimSpace(input.SSHHost)
		workspace.SSHUser = strings.TrimSpace(input.SSHUser)
		workspace.SSHPort = input.SSHPort
		if workspace.SSHPort == 0 {
			workspace.SSHPort = 22
		}
		workspace.SSHAuth = petNormalizeSSHAuth(input.SSHAuth)
		if workspace.SSHHost == "" || workspace.SSHUser == "" || workspace.SSHPort < 1 || workspace.SSHPort > 65535 {
			return domain.Workspace{}, fmt.Errorf("SSH Host、User 和有效 Port 不能为空")
		}
		if password := strings.TrimSpace(input.SSHPassword); password != "" {
			workspace.SSHCredentialRef = "workspace/" + workspace.ID + "/ssh/credential"
			workspace.SSHPasswordConfigured = true
			if s.secrets != nil {
				if err := s.secrets.PutMany(map[string]string{workspace.SSHCredentialRef: password}); err != nil {
					return domain.Workspace{}, err
				}
			}
		}
		if workspace.SSHAuth == "password" && !workspace.SSHPasswordConfigured {
			return domain.Workspace{}, fmt.Errorf("密码认证需要提供 SSH 密码")
		}
	}
	if err := s.store.CreateWorkspace(ctx, workspace); err != nil {
		if workspace.SSHCredentialRef != "" && s.secrets != nil {
			_ = s.secrets.DeleteMany([]string{workspace.SSHCredentialRef})
		}
		return domain.Workspace{}, err
	}
	_ = s.store.AppendPetEvent(ctx, domain.PetEvent{
		ID: domain.NewID("pet_event"), At: now, Actor: "pet", Kind: "workspace_created",
		Detail: workspace.ID, Status: workspace.Name,
	})
	return workspace, nil
}

type PetSendTaskMessageInput struct {
	TaskID        string   `json:"task_id"`
	Content       string   `json:"content"`
	AttachmentIDs []string `json:"attachment_ids"`
}

func (s *Service) PetSendTaskMessage(ctx context.Context, input PetSendTaskMessageInput) (domain.Turn, error) {
	assistant, err := s.petAssistant(ctx)
	if err != nil {
		return domain.Turn{}, err
	}
	if !assistant.PetAllowMessageTasks {
		return domain.Turn{}, fmt.Errorf("主人未开启给其他任务发消息权限")
	}
	taskID := strings.TrimSpace(input.TaskID)
	content := strings.TrimSpace(input.Content)
	if taskID == "" || taskID == petTaskID {
		return domain.Turn{}, fmt.Errorf("不能给宠物自己的 Task 发消息")
	}
	if content == "" && len(input.AttachmentIDs) == 0 {
		return domain.Turn{}, fmt.Errorf("消息或附件不能为空")
	}
	if len([]rune(content)) > 8000 {
		return domain.Turn{}, fmt.Errorf("消息不能超过 8000 个字符")
	}
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		return domain.Turn{}, fmt.Errorf("Task 不存在")
	}
	if task.ReadOnly {
		return domain.Turn{}, fmt.Errorf("该 Task 只读，不能发消息")
	}
	if task.Status == domain.TaskDraft {
		if !assistant.PetAllowCreateTasks {
			return domain.Turn{}, fmt.Errorf("目标 Task 还是草稿；主人未开启启动任务权限")
		}
		if _, err := s.StartTask(ctx, task.ID); err != nil {
			return domain.Turn{}, fmt.Errorf("启动目标 Task 失败：%w", err)
		}
	}
	s.watchPetTask(ctx, task.ID, "messaged")
	turn, err := s.SubmitPetDelegatedMessageWithAttachments(ctx, task.ID, content, input.AttachmentIDs)
	if err != nil {
		return domain.Turn{}, err
	}
	s.recordPetWork(ctx, "task_message_sent", task.ID, turn.ID)
	return turn, nil
}

func (s *Service) petAssistant(ctx context.Context) (domain.OwnerAssistant, error) {
	return s.store.EnsureOwnerAssistant(ctx, s.now().UTC())
}

func petNormalizeSSHAuth(value string) string {
	switch strings.TrimSpace(value) {
	case "password":
		return "password"
	case "key":
		return "key"
	default:
		return "auto"
	}
}
