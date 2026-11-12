package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Service) petManageableTask(ctx context.Context, taskID string) (domain.Task, domain.OwnerAssistant, error) {
	assistant, err := s.petAssistant(ctx)
	if err != nil {
		return domain.Task{}, domain.OwnerAssistant{}, err
	}
	if !assistant.PetAllowCreateTasks {
		return domain.Task{}, domain.OwnerAssistant{}, fmt.Errorf("主人未开启主动创建/管理任务权限")
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return domain.Task{}, domain.OwnerAssistant{}, fmt.Errorf("task_id 不能为空")
	}
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		return domain.Task{}, domain.OwnerAssistant{}, fmt.Errorf("Task 不存在")
	}
	if task.ReadOnly {
		return domain.Task{}, domain.OwnerAssistant{}, fmt.Errorf("该 Task 只读")
	}
	return task, assistant, nil
}

func (s *Service) PetCompleteTask(ctx context.Context, taskID string) (domain.Task, error) {
	return s.PetCompleteTaskWithDecision(ctx, taskID, "", "", "")
}

// PetCompleteTaskWithReview explicitly requests an independent review.
func (s *Service) PetCompleteTaskWithReview(ctx context.Context, taskID, reviewTaskID string) (domain.Task, error) {
	return s.PetCompleteTaskWithDecision(ctx, taskID, "required", "", reviewTaskID)
}

func (s *Service) PetCompleteTaskWithDecision(ctx context.Context, taskID, decision, reason, reviewTaskID string) (domain.Task, error) {
	task, _, err := s.petManageableTask(ctx, taskID)
	if err != nil {
		return domain.Task{}, err
	}
	decision, reason, reviewTaskID = strings.TrimSpace(decision), strings.TrimSpace(reason), strings.TrimSpace(reviewTaskID)
	if decision != "required" && decision != "skipped" {
		return task, fmt.Errorf("review_decision 必填，必须是 required 或 skipped")
	}
	if decision == "skipped" && reason == "" {
		return task, fmt.Errorf("skipped 时 review_reason 必填且不能为空（建议至少 8 字符）")
	}
	if decision == "skipped" && reviewTaskID != "" {
		return task, fmt.Errorf("skipped 时不能提供 review_task_id")
	}
	// Linked review Tasks still need a final verdict even when review is skipped.
	if _, err := s.store.PetCodeReviewSource(ctx, task.ID); err == nil {
		if decision != "skipped" {
			return task, fmt.Errorf("Review Task 自身只能申报 skipped 并提供理由，不能提供 review_task_id")
		}
		if err := s.requirePetCodeReview(ctx, task, reviewTaskID); err != nil {
			return task, err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return task, fmt.Errorf("无法读取 Review Task 关联：%w", err)
	} else if decision == "required" {
		if err := s.requirePetCodeReview(ctx, task, reviewTaskID); err != nil {
			return task, err
		}
	} else {
		reviews, err := s.store.PetCodeReviewsForSource(ctx, task.ID)
		if err != nil {
			return task, fmt.Errorf("无法读取源 Task 的评审关联：%w", err)
		}
		// No review is required when none exists. Otherwise, as with required,
		// only the latest associated review determines whether completion is safe.
		if len(reviews) > 0 {
			reviewID := reviews[len(reviews)-1]
			if err := s.requirePetCodeReview(ctx, task, reviewID); err != nil {
				return task, fmt.Errorf("不能 skipped 绕过最新 Review Task %s：%w", reviewID, err)
			}
		}
	}
	audit := &domain.TaskReviewDecision{
		ReviewDecision: decision, ReviewReason: reason, ReviewTaskID: reviewTaskID,
		ReviewDecidedAt: s.now().UTC(), ReviewDecidedBy: "pet",
	}
	if err := s.completeTask(ctx, task.ID, audit); err != nil {
		return task, err
	}
	return s.store.Task(ctx, task.ID)
}

func (s *Service) PetReopenTask(ctx context.Context, taskID string) (domain.Task, error) {
	task, _, err := s.petManageableTask(ctx, taskID)
	if err != nil {
		return domain.Task{}, err
	}
	if err := s.ReopenTask(ctx, task.ID); err != nil {
		return task, err
	}
	return s.store.Task(ctx, task.ID)
}

// PetInterruptTask interrupts whichever execution is active for the task.
func (s *Service) PetInterruptTask(ctx context.Context, taskID string) (domain.Task, error) {
	task, _, err := s.petManageableTask(ctx, taskID)
	if err != nil {
		return domain.Task{}, err
	}
	if turn, activeErr := s.store.ActiveTurn(ctx, task.ID); activeErr == nil {
		if err := s.InterruptTurn(ctx, turn.ID); err != nil {
			return task, err
		}
		return s.store.Task(ctx, task.ID)
	} else if !errors.Is(activeErr, sql.ErrNoRows) {
		return task, activeErr
	}
	if round, activeErr := s.store.ActiveRound(ctx, task.ID); activeErr == nil {
		if err := s.InterruptRound(ctx, round.ID); err != nil {
			return task, err
		}
		return s.store.Task(ctx, task.ID)
	} else if !errors.Is(activeErr, sql.ErrNoRows) {
		return task, activeErr
	}
	return task, fmt.Errorf("Task 当前没有执行中的 Turn 或 Round")
}

type PetUpdateTaskInput struct {
	TaskID            string  `json:"task_id"`
	Title             *string `json:"title"`
	CurrentGoal       *string `json:"current_goal"`
	CollaborationMode *string `json:"collaboration_mode"`
	MaxAgents         *int    `json:"max_agents"`
	// SkillIDs replaces the Task's whole Skill selection when present, like the
	// Web PATCH; an empty list clears it. Omitted or null leaves it unchanged.
	SkillIDs *[]string `json:"skill_ids"`
}

func (s *Service) PetUpdateTask(ctx context.Context, input PetUpdateTaskInput) (domain.Task, error) {
	task, _, err := s.petManageableTask(ctx, input.TaskID)
	if err != nil {
		return domain.Task{}, err
	}
	if task.Status == domain.TaskCompleted || task.Status == domain.TaskCancelled {
		return task, fmt.Errorf("Task 已处于 %s，不能修改", task.Status)
	}
	changed := false
	// Skills go first: UpdateTaskSkills validates every ID before writing, so an
	// unavailable Skill rejects the call before any other field is touched.
	if input.SkillIDs != nil {
		if _, err := s.UpdateTaskSkills(ctx, task.ID, *input.SkillIDs); err != nil {
			return task, err
		}
		changed = true
	}
	if input.Title != nil {
		title := strings.TrimSpace(*input.Title)
		if title == "" || len([]rune(title)) > 120 {
			return task, fmt.Errorf("标题不能为空且不能超过 120 个字符")
		}
		if err := s.store.UpdateTaskTitle(ctx, task.ID, title, timeString(s.now().UTC())); err != nil {
			return task, err
		}
		changed = true
	}
	if input.CurrentGoal != nil {
		goal := strings.TrimSpace(*input.CurrentGoal)
		if goal == "" || len([]rune(goal)) > 2000 {
			return task, fmt.Errorf("Current Goal 不能为空且不能超过 2000 个字符")
		}
		if err := s.store.UpdateTaskGoalWithEvent(ctx, domain.TaskGoalChange{
			TaskID: task.ID, GoalAfter: goal, ChangeType: "revise",
			Reason: "宠物代主人修改目标", CreatedAt: s.now().UTC(),
		}); err != nil {
			return task, err
		}
		changed = true
	}
	if input.CollaborationMode != nil || input.MaxAgents != nil {
		mode := task.CollaborationMode
		if input.CollaborationMode != nil {
			mode = strings.TrimSpace(*input.CollaborationMode)
		}
		maxAgents := task.MaxAgents
		if input.MaxAgents != nil {
			maxAgents = *input.MaxAgents
		}
		if _, err := s.UpdateTaskCollaboration(ctx, task.ID, mode, maxAgents); err != nil {
			return task, err
		}
		changed = true
	}
	if !changed {
		return task, fmt.Errorf("没有需要修改的字段")
	}
	return s.store.Task(ctx, task.ID)
}

func (s *Service) PetDeleteTask(ctx context.Context, taskID string, reason ...string) (domain.PetApproval, error) {
	reasonText, reasonErr := requiredPetApprovalReason(reason...)
	if reasonErr != nil {
		return domain.PetApproval{}, reasonErr
	}
	task, _, err := s.petManageableTask(ctx, taskID)
	if err != nil {
		return domain.PetApproval{}, err
	}
	if task.ID == petTaskID {
		return domain.PetApproval{}, fmt.Errorf("不能删除宠物自己的 Task")
	}
	if s.store.IsManagedChannelTask(ctx, task.ID) {
		return domain.PetApproval{}, fmt.Errorf("渠道宿主 Task 不能在宠物流程中删除")
	}
	if _, activeErr := s.store.ActiveTurn(ctx, task.ID); activeErr == nil {
		return domain.PetApproval{}, fmt.Errorf("Task 正在执行，请先中断或等待完成")
	} else if !errors.Is(activeErr, sql.ErrNoRows) {
		return domain.PetApproval{}, activeErr
	}
	return s.CreatePetApproval(ctx, PetApprovalInput{
		Kind: "delete_task", Title: "删除 Task「" + task.Title + "」",
		Summary: "宠物请求删除这个 Task，需要主人确认。",
		Reason:  reasonText, Risk: "high", Payload: mustJSON(map[string]string{"task_id": task.ID}),
	})
}

func (s *Service) deleteTaskNow(ctx context.Context, taskID string) error {
	task, _, err := s.petManageableTask(ctx, taskID)
	if err != nil {
		return err
	}
	if task.ID == petTaskID {
		return fmt.Errorf("不能删除宠物自己的 Task")
	}
	if s.store.IsManagedChannelTask(ctx, task.ID) {
		return fmt.Errorf("渠道宿主 Task 不能在宠物流程中删除")
	}
	if _, activeErr := s.store.ActiveTurn(ctx, task.ID); activeErr == nil {
		return fmt.Errorf("Task 正在执行，请先中断或等待完成")
	} else if !errors.Is(activeErr, sql.ErrNoRows) {
		return activeErr
	}
	if cleanupErr := s.CleanupTaskScratch(ctx, task); cleanupErr != nil {
		log.Printf("cleanup task scratch %s: %v", task.ID, cleanupErr)
	}
	if err := s.store.DeleteTaskWithSyncTombstone(ctx, task.ID); err != nil {
		return err
	}
	if task.RuntimeConfigSnapshotID != "" {
		_ = s.store.DeleteRuntimeSnapshot(ctx, task.RuntimeConfigSnapshotID)
	}
	_ = s.store.DeletePetTaskWatch(ctx, task.ID)
	_ = s.store.AppendPetEvent(ctx, domain.PetEvent{
		ID: domain.NewID("pet_event"), At: s.now().UTC(), Actor: "pet", Kind: "task_deleted",
		Detail: task.ID, Status: task.Title,
	})
	return nil
}

type PetUpdateTaskAgentInput struct {
	TaskID          string `json:"task_id"`
	ModelID         string `json:"model_id"`
	ReasoningEffort string `json:"reasoning_effort"`
	Filesystem      string `json:"filesystem"`
	Approval        string `json:"approval"`
	FastMode        *bool  `json:"fast_mode"`
}

func (s *Service) PetUpdateTaskAgent(ctx context.Context, input PetUpdateTaskAgentInput) (domain.TaskAgent, error) {
	task, _, err := s.petManageableTask(ctx, input.TaskID)
	if err != nil {
		return domain.TaskAgent{}, err
	}
	if task.Status == domain.TaskCompleted || task.Status == domain.TaskCancelled {
		return domain.TaskAgent{}, fmt.Errorf("Task 已处于 %s，不能修改 Agent", task.Status)
	}
	return s.UpdateAgentConfig(ctx, task.ID, "main", UpdateAgentConfigInput{
		ModelID: strings.TrimSpace(input.ModelID), ReasoningEffort: strings.TrimSpace(input.ReasoningEffort),
		Filesystem: strings.TrimSpace(input.Filesystem), Approval: strings.TrimSpace(input.Approval),
		FastMode: input.FastMode,
	})
}

func (s *Service) PetCompactTaskSession(ctx context.Context, taskID string) (domain.BackendSession, error) {
	task, _, err := s.petManageableTask(ctx, taskID)
	if err != nil {
		return domain.BackendSession{}, err
	}
	return s.CompactAgentSession(ctx, task.ID, "main")
}

func (s *Service) PetResetTaskSession(ctx context.Context, taskID string) (domain.BackendSession, error) {
	task, _, err := s.petManageableTask(ctx, taskID)
	if err != nil {
		return domain.BackendSession{}, err
	}
	return s.ResetAgentSession(ctx, task.ID, "main")
}

func (s *Service) petViewableTask(ctx context.Context, taskID string) (domain.Task, error) {
	assistant, err := s.petAssistant(ctx)
	if err != nil {
		return domain.Task{}, err
	}
	if !assistant.PetAllowViewTasks {
		return domain.Task{}, fmt.Errorf("主人未开启查看其他任务信息权限")
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return domain.Task{}, fmt.Errorf("task_id 不能为空")
	}
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		return domain.Task{}, fmt.Errorf("Task 不存在")
	}
	return task, nil
}

type PetConversationItem struct {
	Sequence  int64  `json:"sequence"`
	ID        string `json:"id"`
	AgentID   string `json:"agent_id,omitempty"`
	Category  string `json:"category"`
	Kind      string `json:"kind"`
	Summary   string `json:"summary"`
	CreatedAt string `json:"created_at"`
}

func (s *Service) PetTaskConversation(ctx context.Context, taskID string, limit int) ([]PetConversationItem, error) {
	task, err := s.petViewableTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	page, err := s.store.ConversationPageForAgent(ctx, task.ID, "main", 0, 0, limit, nil)
	if err != nil {
		return nil, err
	}
	items := make([]PetConversationItem, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, PetConversationItem{
			Sequence: item.Sequence, ID: item.ID, AgentID: item.AgentID,
			Category: item.Category, Kind: item.Kind,
			Summary: truncateAssistantText(item.Summary, 2000), CreatedAt: item.CreatedAt.Format(time.RFC3339),
		})
	}
	return items, nil
}

type PetTurnBrief struct {
	ID        string `json:"id"`
	AgentID   string `json:"agent_id"`
	Sequence  int    `json:"sequence"`
	Status    string `json:"status"`
	Result    string `json:"result,omitempty"`
	Error     string `json:"error,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

func (s *Service) PetTaskTurns(ctx context.Context, taskID string, limit int) ([]PetTurnBrief, error) {
	task, err := s.petViewableTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	turns, err := s.store.ListTurns(ctx, task.ID)
	if err != nil {
		return nil, err
	}
	if len(turns) > limit {
		turns = turns[len(turns)-limit:]
	}
	items := make([]PetTurnBrief, 0, len(turns))
	for _, turn := range turns {
		items = append(items, PetTurnBrief{
			ID: turn.ID, AgentID: turn.AgentID, Sequence: turn.Sequence,
			Status: string(turn.Status), Result: truncateAssistantText(turn.Result, 3000),
			Error: truncateAssistantText(turn.Error, 2000), CreatedAt: turn.QueuedAt.Format(time.RFC3339),
		})
	}
	return items, nil
}
