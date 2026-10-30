package app

import (
	"context"
	"log"
	"os"
	"path/filepath"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const (
	petProjectID   = "project_pet"
	petWorkspaceID = "workspace_pet"
	petTaskID      = "task_pet"

	// petTaskIntro is both the Task requirement and the first message. Web task
	// creation treats "需求" as the first user message, so the pet follows the
	// same semantics instead of keeping internal instructions in the Task.
	petTaskIntro = "请用一两句话向主人做个自我介绍：你的名字、当前形象，以及你会陪主人聊天、写日记、做小动作。不要写代码或运行命令。"
)

// reopenPetTaskIfFailed returns the Pet Task to waiting_user after a backend
// failure. Without this an owner whose assistant already points at task_pet
// would keep seeing the failed Task: the PetTaskID branch used to return it
// unchanged, so the "reopen failed pet task" fallback below never ran.
func (s *Service) reopenPetTaskIfFailed(ctx context.Context, task domain.Task) domain.Task {
	if task.Status != domain.TaskFailed {
		return task
	}
	if err := s.ReopenTask(ctx, task.ID); err != nil {
		return task
	}
	if reloaded, err := s.store.Task(ctx, task.ID); err == nil {
		return reloaded
	}
	return task
}

// ensureOwnerPetTask creates the visible system Pet Task and its dedicated
// writable workspace on first use. The pet is a normal Task so it can use the
// normal backend conversation, while pet tools come from the MCP endpoint.
// A brand-new draft with no Turns is started immediately with the
// self-introduction as its first message.
func (s *Service) ensureOwnerPetTask(ctx context.Context, assistant *domain.OwnerAssistant) (string, error) {
	if assistant == nil {
		return "", nil
	}
	s.ensurePetWorkspaceGuide()
	// Serialize the lazy creation/start so concurrent assistant page loads
	// cannot start the same draft twice or submit two introductions.
	s.petTaskMu.Lock()
	defer s.petTaskMu.Unlock()

	recordPetTask := func(taskID string) {
		if taskID == "" || assistant.PetTaskID == taskID {
			return
		}
		assistant.PetTaskID = taskID
		assistant.UpdatedAt = s.now().UTC()
		_ = s.store.UpdateOwnerPetTask(ctx, assistant.ID, taskID, assistant.UpdatedAt)
	}
	if assistant.PetTaskID != "" {
		if existing, err := s.store.Task(ctx, assistant.PetTaskID); err == nil {
			existing = s.reopenPetTaskIfFailed(ctx, existing)
			s.ensurePetTaskStarted(ctx, existing)
			recordPetTask(existing.ID)
			return existing.ID, nil
		}
	}
	if existing, err := s.store.Task(ctx, petTaskID); err == nil {
		existing = s.reopenPetTaskIfFailed(ctx, existing)
		recordPetTask(petTaskID)
		s.ensurePetTaskStarted(ctx, existing)
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
		Title: "宠物", Request: petTaskIntro, ModelID: assistant.ModelID,
		Isolation: "inplace", Filesystem: "workspace-write", Approval: "never",
		CollaborationMode: "single", MaxAgents: 1, StartMode: "manual",
	})
	if err != nil {
		return "", err
	}
	recordPetTask(task.ID)
	s.ensurePetTaskStarted(ctx, task)
	return task.ID, nil
}

// ensurePetTaskStarted starts a pet Task that is still a draft. A draft with
// no Turns gets the self-introduction as its requirement and first message.
// A draft that already has Turns is only started, so a retry never duplicates
// the introduction. Failures roll the Task back to draft for the next retry.
func (s *Service) ensurePetTaskStarted(ctx context.Context, task domain.Task) {
	if task.Status != domain.TaskDraft {
		return
	}
	turns, err := s.store.TaskTurnCount(ctx, task.ID)
	if err != nil {
		return
	}
	if turns == 0 && task.OriginalRequest != petTaskIntro {
		now := timeString(s.now().UTC())
		if err := s.store.UpdateTaskOriginalRequest(ctx, task.ID, petTaskIntro, now); err != nil {
			return
		}
		task.OriginalRequest = petTaskIntro
		// The requirement is what the first message uses; a failed goal mirror
		// still leaves a startable Task, so do not block startup on it.
		_ = s.store.UpdateTaskGoal(ctx, task.ID, petTaskIntro, now)
	}
	started, err := s.StartTask(ctx, task.ID)
	if err != nil {
		s.resetPetTaskDraft(ctx, task.ID)
		return
	}
	if turns > 0 {
		return
	}
	if _, err := s.SubmitMessage(ctx, started.ID, petTaskIntro); err != nil {
		s.resetPetTaskDraft(ctx, started.ID)
	}
}

func (s *Service) resetPetTaskDraft(ctx context.Context, taskID string) {
	if err := s.store.ResetTaskToDraft(ctx, taskID, timeString(s.now().UTC())); err != nil {
		log.Printf("pet task %s start rollback failed: %v", taskID, err)
	}
}

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
