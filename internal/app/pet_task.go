package app

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const (
	petProjectID   = "project_pet"
	petWorkspaceID = "workspace_pet"
	petTaskID      = "task_pet"
)

// ensureOwnerPetTask creates the visible system Pet Task and its dedicated
// writable workspace on first use. The pet is a normal Task so it can use the
// normal backend conversation, while pet tools come from the MCP endpoint.
func (s *Service) ensureOwnerPetTask(ctx context.Context, assistant *domain.OwnerAssistant) (string, error) {
	if assistant == nil {
		return "", nil
	}
	s.ensurePetWorkspaceGuide()
	if assistant.PetTaskID != "" {
		if _, err := s.store.Task(ctx, assistant.PetTaskID); err == nil {
			return assistant.PetTaskID, nil
		}
	}
	if existing, err := s.store.Task(ctx, petTaskID); err == nil {
		if existing.Status == domain.TaskFailed {
			_ = s.ReopenTask(ctx, existing.ID)
		}
		assistant.PetTaskID = petTaskID
		return petTaskID, nil
	}
	now := s.now().UTC()
	project, projectErr := s.store.Project(ctx, petProjectID)
	if projectErr != nil {
		project = domain.Project{
			ID: petProjectID, Name: "宠物", Description: "系统管理的宠物 Task 项目",
			ProjectType: "system", KnowledgePolicy: "none", CreatedAt: now, UpdatedAt: now,
		}
		if err := s.store.CreateProject(ctx, project); err != nil {
			return "", err
		}
	}
	if _, err := s.store.Workspace(ctx, petWorkspaceID); err != nil {
		root := filepath.Join(s.store.DataDir(), "pet-workspace")
		if err := os.MkdirAll(root, 0o700); err != nil {
			return "", err
		}
		workspace := domain.Workspace{
			ID: petWorkspaceID, ProjectID: project.ID, Name: "pet-workspace",
			Locality: "local", Transport: "native", RootPath: root, Health: "ready",
			AgentAPIMode: "embedded", CreatedAt: now, UpdatedAt: now,
		}
		if err := s.store.CreateWorkspace(ctx, workspace); err != nil {
			return "", err
		}
	}
	_ = os.MkdirAll(filepath.Join(s.store.DataDir(), "pet-workspace", "diary"), 0o700)
	guide := `# 宠物 Task 工作区

你是 AHA2 里的宠物，不是编码代理。

你的职责只有：
- 和主人进行简短、自然的宠物对话。
- 需要写日记时，写到 diary/YYYY-MM-DD.md，用第一人称。
- 需要做动作时，调用 aha2-pet MCP 工具：pet_play、pet_move_to、pet_walk_path、pet_mood、pet_open_task、pet_sleep、pet_wake。

禁止：
- 不要创建网页、CSS、JS、应用或任何代码。
- 不要运行 npm、build、test、git 或开发服务器。
- 不要分析这个工作区的文件结构，不要把它当成软件项目。
- 除了 diary/ 下的日记，不要写其他文件。

主人说话时直接回应；不要主动开发任何东西。
`
	for _, name := range []string{"CLAUDE.md", "AGENTS.md"} {
		_ = os.WriteFile(filepath.Join(s.store.DataDir(), "pet-workspace", name), []byte(guide), 0o600)
	}
	task, err := s.CreateTask(ctx, CreateTaskInput{
		ID: petTaskID, ProjectID: petProjectID, WorkspaceID: petWorkspaceID,
		Title: "宠物", Request: "你是宠物，不是编码代理。只和主人对话、写 diary/ 日记、调用 aha2-pet MCP 工具；不要创建网页、代码或运行开发命令。", ModelID: assistant.ModelID,
		Isolation: "inplace", Filesystem: "workspace-write", Approval: "never",
		CollaborationMode: "single", MaxAgents: 1, StartMode: "manual",
	})
	if err != nil {
		return "", err
	}
	assistant.PetTaskID = task.ID
	assistant.UpdatedAt = s.now().UTC()
	_ = s.store.UpdateOwnerPetTask(ctx, assistant.ID, task.ID, assistant.UpdatedAt)
	return task.ID, nil
}

var _ = time.Time{}

func (s *Service) ensurePetWorkspaceGuide() {
	if s == nil || s.store == nil {
		return
	}
	root := filepath.Join(s.store.DataDir(), "pet-workspace")
	_ = os.MkdirAll(filepath.Join(root, "diary"), 0o700)
	guide := `# 宠物 Task 工作区

你是 AHA2 里的宠物，不是编码代理。
只和主人对话、写 diary/YYYY-MM-DD.md 日记、调用 aha2-pet MCP 工具。
不要创建网页、代码、应用；不要运行 npm/build/test/git；不要分析工作区文件。
`
	for _, name := range []string{"CLAUDE.md", "AGENTS.md"} {
		_ = os.WriteFile(filepath.Join(root, name), []byte(guide), 0o600)
	}
}

func petTaskIdentity(task domain.Task) string {
	if task.ID == petTaskID {
		return "pet-task"
	}
	return ""
}
