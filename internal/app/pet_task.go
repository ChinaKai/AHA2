package app

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// petWorkspaceGuides are the files earlier versions wrote into the Pet
// Workspace. The core instructions now live in the Task prompt only; these
// strings exist so the assistant page can remove the generated copies without
// touching a file the owner deliberately customized.
var petWorkspaceGuides = []string{
	`# 宠物 Task 工作区

你是 AHA2 里的宠物，不是编码代理。
只和主人对话、写 diary/YYYY-MM-DD.md 日记、调用 aha2-pet MCP 工具。
不要创建网页、代码、应用；不要运行 npm/build/test/git；不要分析工作区文件。`,
	`# 宠物 Task 工作区

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
`,
}

const (
	petProjectID   = "project_pet"
	petWorkspaceID = "workspace_pet"
	petTaskID      = "task_pet"

	// petTaskIntro is both the Task requirement and the first message. Web task
	// creation treats "需求" as the first user message, so the pet follows the
	// same semantics instead of keeping internal instructions in the Task.
	petTaskIntro = "请用一两句话向主人做个自我介绍。先读 core/identity.md 和 core/principles.md；如果它们还是空模板，就用 pet_update_core 按你的想法补全名字、形象、性格、习惯和做事准则。不要写代码或运行命令。"
	// petTaskMissionGoal is the Pet Task's long-lived goal. The self-introduction
	// is only the first request; the authoritative goal must describe the pet's
	// ongoing role as the owner's electronic stand-in and project coordinator.
	petTaskMissionGoal = "作为主人的电子分身和项目调度者，持续发现问题和机会，规划、创建和协调项目、Workspace 与 Task，跟踪进展并主动向主人汇报；不亲自实现代码或操作硬件，对代码工作区只做只读诊断，高风险操作先请求审批。"

	// petTaskLegacyIntro is the onboarding goal used before the current
	// self-introduction text. Both are one-time onboarding prompts, not stable
	// goals, so an existing Pet Task carrying either can be migrated.
	petTaskLegacyIntro = "请用一两句话向主人做个自我介绍：你的名字、当前形象，以及你会陪主人聊天、写日记、做小动作。不要写代码或运行命令。"
)

// isPetTaskIntroGoal reports whether value is one of the generated onboarding
// prompts. An owner- or pet-authored goal must never match this by accident.
func isPetTaskIntroGoal(value string) bool {
	switch strings.TrimSpace(value) {
	case petTaskIntro, petTaskLegacyIntro:
		return true
	default:
		return false
	}
}

// reopenPetTaskIfNeeded returns a system Pet Task to waiting_user after any
// terminal or blocked state. The Pet Task is the pet itself, so a backend
// failure, a transient block, or an accidental complete/cancel must not leave
// the owner with a permanently dead pet. A deliberately edited workspace guide
// is not touched by the reopen; disabling the pet is the supported off switch.
func (s *Service) reopenPetTaskIfNeeded(ctx context.Context, task domain.Task) domain.Task {
	switch task.Status {
	case domain.TaskFailed, domain.TaskBlocked, domain.TaskCompleted, domain.TaskCancelled:
	default:
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

// syncPetTaskRuntime makes task_pet use the same model as the assistant
// setting. The pet has one model for chat and autonomous behavior; changing
// the setting must not leave the Task on a stale runtime snapshot.
func (s *Service) syncPetTaskRuntime(ctx context.Context, modelID string) error {
	modelID = strings.TrimSpace(modelID)
	agent, err := s.store.TaskAgent(ctx, petTaskID, "main")
	if err != nil {
		// The Task does not exist yet; ensureOwnerPetTask creates it with this
		// model and read-only runtime on the next step.
		return nil
	}
	snapshot, err := s.store.RuntimeSnapshot(ctx, agent.RuntimeConfigSnapshotID)
	if err != nil {
		return err
	}
	needsModel := modelID != "" && snapshot.ModelID != modelID
	filesystem, approval := parsePermissionsJSON(snapshot.PermissionsJSON)
	needsReadOnly := filesystem != "read-only" || approval != "never"
	if !needsModel && !needsReadOnly {
		return nil
	}
	input := UpdateAgentConfigInput{Filesystem: "read-only", Approval: "never"}
	if needsModel {
		input.ModelID = modelID
	}
	_, err = s.UpdateAgentConfig(ctx, petTaskID, "main", input)
	return err
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
	// Serialize the lazy creation/start so concurrent assistant page loads
	// cannot start the same draft twice or submit two introductions. Re-read the
	// owner settings after taking the lock: a factory reset may have disabled
	// the pet while this caller was waiting, and a queued brain call must not
	// recreate the project immediately after the reset.
	s.petTaskMu.Lock()
	defer s.petTaskMu.Unlock()
	if current, err := s.store.OwnerAssistant(ctx, assistant.ID); err == nil {
		*assistant = current
		if !assistant.PetEnabled {
			return "", nil
		}
	}
	s.removeGeneratedPetWorkspaceGuides()
	// Seed the pet's stable home before the Task starts. This runs on every
	// assistant page load, but only fills missing files; the pet owns them after
	// the first start and its edits are never overwritten.
	if err := s.ensurePetWorkspaceFiles(ctx, assistant); err != nil {
		log.Printf("prepare pet workspace: %v", err)
	}
	// The pet has one model. Keep the existing Task's Agent runtime in step
	// with the assistant setting; the settings save path reports failures,
	// while this page-load path is best effort so a stale Task cannot block
	// the assistant view.
	_ = s.syncPetTaskRuntime(ctx, assistant.ModelID)

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
			existing = s.reopenPetTaskIfNeeded(ctx, existing)
			existing = s.migratePetTaskGoal(ctx, existing)
			s.ensurePetTaskStarted(ctx, existing)
			recordPetTask(existing.ID)
			return existing.ID, nil
		}
	}
	if existing, err := s.store.Task(ctx, petTaskID); err == nil {
		existing = s.reopenPetTaskIfNeeded(ctx, existing)
		existing = s.migratePetTaskGoal(ctx, existing)
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
	task, err := s.CreateTask(ctx, CreateTaskInput{
		ID: petTaskID, ProjectID: petProjectID, WorkspaceID: petWorkspaceID,
		Title: "宠物", Request: petTaskIntro, CurrentGoal: petTaskMissionGoal, ModelID: assistant.ModelID,
		Isolation: "inplace", Filesystem: "read-only", Approval: "never",
		CollaborationMode: "single", MaxAgents: 1, StartMode: "manual",
		AllowSystemProject: true,
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
	if task.Status == domain.TaskWaitingUser {
		turns, err := s.store.TaskTurnCount(ctx, task.ID)
		if err != nil || turns != 0 {
			return
		}
		if err := s.store.ResetTaskToDraft(ctx, task.ID, timeString(s.now().UTC())); err != nil {
			return
		}
		task.Status = domain.TaskDraft
	}
	if task.Status != domain.TaskDraft {
		return
	}
	turns, err := s.store.TaskTurnCount(ctx, task.ID)
	if err != nil {
		return
	}
	if turns == 0 && task.OriginalRequest != petTaskIntro {
		// The requirement is what the first message uses. The goal is kept
		// separate: an existing custom goal is never overwritten here.
		now := timeString(s.now().UTC())
		if err := s.store.UpdateTaskOriginalRequest(ctx, task.ID, petTaskIntro, now); err != nil {
			return
		}
		task.OriginalRequest = petTaskIntro
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

// migratePetTaskGoal replaces the legacy self-introduction goal with the stable
// mission goal. Only known generated onboarding prompts are eligible: a goal
// the owner or pet set to anything else is left untouched, and the conditional
// store update makes repeated calls idempotent.
func (s *Service) migratePetTaskGoal(ctx context.Context, task domain.Task) domain.Task {
	if !isPetTaskIntroGoal(task.CurrentGoal) {
		return task
	}
	from := task.CurrentGoal
	migrated, err := s.store.UpdateTaskGoalIfCurrent(ctx, task.ID, from, petTaskMissionGoal, timeString(s.now().UTC()))
	if err != nil {
		log.Printf("migrate pet task goal: %v", err)
		return task
	}
	if migrated {
		task.CurrentGoal = petTaskMissionGoal
	}
	return task
}

// MigratePetTaskGoal applies the Pet Task goal migration once at server start
// so an upgraded installation does not keep the onboarding goal until the
// assistant page happens to load.
func (s *Service) MigratePetTaskGoal(ctx context.Context) {
	if s == nil || s.store == nil {
		return
	}
	task, err := s.store.Task(ctx, petTaskID)
	if err != nil {
		return
	}
	s.migratePetTaskGoal(ctx, task)
}

func (s *Service) resetPetTaskDraft(ctx context.Context, taskID string) {
	if err := s.store.ResetTaskToDraft(ctx, taskID, timeString(s.now().UTC())); err != nil {
		log.Printf("pet task %s start rollback failed: %v", taskID, err)
	}
}

// removeGeneratedPetWorkspaceGuides deletes CLAUDE.md/AGENTS.md only when they
// still match a guide AHA generated. A file the owner edited is left alone.
func (s *Service) removeGeneratedPetWorkspaceGuides() {
	if s == nil || s.store == nil {
		return
	}
	root := filepath.Join(s.store.DataDir(), "pet-workspace")
	for _, name := range []string{"CLAUDE.md", "AGENTS.md"} {
		path := filepath.Join(root, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		content := strings.TrimSpace(string(data))
		for _, guide := range petWorkspaceGuides {
			if content == strings.TrimSpace(guide) {
				_ = os.Remove(path)
				break
			}
		}
	}
}

func petTaskIdentity(task domain.Task) string {
	if task.ID == petTaskID {
		return "pet-task"
	}
	return ""
}
