package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const petOpenTaskListLimit = 50

type PetWorkItem struct {
	Kind           string `json:"kind"`
	TargetType     string `json:"target_type"`
	TargetID       string `json:"target_id"`
	Title          string `json:"title,omitempty"`
	CreatedAt      string `json:"created_at,omitempty"`
	CurrentStatus  string `json:"current_status,omitempty"`
	LastTurnStatus string `json:"last_turn_status,omitempty"`
	LastTurnError  string `json:"last_turn_error,omitempty"`
	LastTurnResult string `json:"last_turn_result,omitempty"`
	Note           string `json:"note,omitempty"`
}

// PetOpenTask is a pet-created Task that has not reached a final closed state
// yet. It is intentionally task-level rather than event-level: an old waiting
// Task must stay visible even after it falls out of the recent-event window.
type PetOpenTask struct {
	ID                string `json:"id"`
	Code              string `json:"code,omitempty"`
	Title             string `json:"title"`
	Project           string `json:"project,omitempty"`
	Workspace         string `json:"workspace,omitempty"`
	Status            string `json:"status"`
	Goal              string `json:"goal,omitempty"`
	CreatedAt         string `json:"created_at,omitempty"`
	UpdatedAt         string `json:"updated_at,omitempty"`
	AgeMinutes        int    `json:"age_minutes"`
	LastTurnStatus    string `json:"last_turn_status,omitempty"`
	LastTurnError     string `json:"last_turn_error,omitempty"`
	LastTurnResult    string `json:"last_turn_result,omitempty"`
	NeedsAttention    bool   `json:"needs_attention"`
	AttentionReason   string `json:"attention_reason,omitempty"`
	PendingApprovalID string `json:"pending_approval_id,omitempty"`
}

// PetInspection is the compact, server-derived snapshot the pet quotes at the
// end of an inspection reply. It never replaces the detailed worklog evidence.
type PetInspection struct {
	CheckedAt        string   `json:"checked_at"`
	Version          string   `json:"version,omitempty"`
	WebVersion       string   `json:"web_version,omitempty"`
	StartedAt        string   `json:"started_at,omitempty"`
	UptimeMinutes    int      `json:"uptime_minutes,omitempty"`
	Health           string   `json:"health"`
	TasksTotal       int      `json:"tasks_total"`
	Active           int      `json:"active"`
	WaitingUser      int      `json:"waiting_user"`
	Failed           int      `json:"failed"`
	Blocked          int      `json:"blocked"`
	Completed        int      `json:"completed"`
	PendingApprovals int      `json:"pending_approvals"`
	DegradedChannels []string `json:"degraded_channels,omitempty"`
	OpenTasks        int      `json:"open_tasks"`
	AttentionTasks   int      `json:"attention_tasks"`
	Note             string   `json:"note,omitempty"`
}

type PetWorkState struct {
	PendingApprovals int                  `json:"pending_approvals"`
	Approvals        []domain.PetApproval `json:"approvals"`
	Work             []PetWorkItem        `json:"work"`
	OpenTasks        []PetOpenTask        `json:"open_tasks"`
	Inspection       PetInspection        `json:"inspection"`
	OpenLoops        string               `json:"open_loops,omitempty"`
	OpenLoopsPath    string               `json:"open_loops_path,omitempty"`
	OpenLoopsNote    string               `json:"open_loops_note,omitempty"`
	Agenda           string               `json:"agenda,omitempty"`
	AgendaPath       string               `json:"agenda_path,omitempty"`
	AgendaNote       string               `json:"agenda_note,omitempty"`
	LabPath          string               `json:"lab_path,omitempty"`
	Note             string               `json:"note,omitempty"`
}

// PetWorkState is the pet's own wake-up briefing: approval results, the
// inspection snapshot, every still-open Task it created, and the
// tasks/projects/workspaces it touched recently. It only reads state; the pet
// decides whether to continue, remind the owner, or record the outcome in
// worklog.
func (s *Service) PetWorkState(ctx context.Context) (PetWorkState, error) {
	approvals, err := s.store.ListPetApprovals(ctx, 20)
	if err != nil {
		return PetWorkState{}, err
	}
	pendingApprovals, err := s.store.ListPendingPetApprovals(ctx, 200)
	if err != nil {
		return PetWorkState{}, err
	}
	tasks, err := s.store.ListTasks(ctx, "")
	if err != nil {
		return PetWorkState{}, err
	}
	openTasks, openTaskTotal, attentionTasks, err := s.petOpenTasks(ctx, tasks, pendingApprovals)
	if err != nil {
		return PetWorkState{}, err
	}
	loops := s.PetOpenLoops(ctx)
	agenda := s.PetAgenda(ctx)
	state := PetWorkState{
		Approvals: approvals, PendingApprovals: len(pendingApprovals),
		OpenTasks: openTasks, Inspection: s.petInspection(ctx, tasks, pendingApprovals, openTaskTotal, attentionTasks),
		Work:      make([]PetWorkItem, 0),
		OpenLoops: loops, OpenLoopsPath: s.petOpenLoopsPath(ctx), OpenLoopsNote: petLoopsNote(loops),
		Agenda: agenda, AgendaPath: s.petAgendaPath(ctx), AgendaNote: petAgendaNote(agenda),
		LabPath: s.petLabPath(ctx),
	}
	events, err := s.store.ListPetEvents(ctx, 100)
	if err != nil {
		return PetWorkState{}, err
	}
	workKinds := map[string]string{
		"task_created": "task", "task_started": "task", "task_message_sent": "task",
		"task_deleted": "task", "project_created": "project", "project_deleted": "project",
		"workspace_created": "workspace", "workspace_deleted": "workspace",
		"knowledge_submitted": "knowledge", "knowledge_deleted": "knowledge",
		"skill_created": "skill", "skill_updated": "skill", "skill_deleted": "skill",
		"prompt_updated": "prompt", "channel_message_sent": "channel",
		"channel_enabled": "channel", "channel_disabled": "channel",
		"channel_reset_binding": "channel", "channel_archive": "channel", "channel_purge": "channel",
		"security_settings_updated": "system", "permission_changed": "permission", "idea_submitted": "idea",
		"model_deleted": "model", "provider_deleted": "model", "env_group_deleted": "model",
		"hardware_resource_deleted": "hardware",
	}
	seen := map[string]bool{}
	for _, event := range events {
		targetType, ok := workKinds[event.Kind]
		if !ok {
			continue
		}
		targetID := strings.TrimSpace(event.Detail)
		if targetID == "" {
			continue
		}
		// The event table has no separate target type; the kind prefix is enough
		// to disambiguate and the ID is stable.
		key := event.Kind + ":" + targetID
		if seen[key] {
			continue
		}
		seen[key] = true
		item := PetWorkItem{
			Kind: event.Kind, TargetType: targetType, TargetID: targetID,
			CreatedAt: event.At.Format(time.RFC3339), Note: event.Status,
		}
		switch targetType {
		case "task":
			task, taskErr := s.store.Task(ctx, targetID)
			if taskErr != nil {
				item.CurrentStatus = "deleted"
			} else {
				item.Title = task.Title
				item.CurrentStatus = string(task.Status)
				turns, _ := s.store.ListTurns(ctx, task.ID)
				if len(turns) > 0 {
					last := turns[len(turns)-1]
					item.LastTurnStatus = string(last.Status)
					item.LastTurnError = truncateAssistantText(last.Error, 1200)
					item.LastTurnResult = truncateAssistantText(last.Result, 1200)
				}
			}
		case "project":
			if project, projectErr := s.store.Project(ctx, targetID); projectErr == nil {
				item.Title = project.Name
				item.CurrentStatus = "exists"
			} else {
				item.CurrentStatus = "deleted"
			}
		case "workspace":
			if workspace, workspaceErr := s.store.Workspace(ctx, targetID); workspaceErr == nil {
				item.Title = workspace.Name
				item.CurrentStatus = workspace.Health
			} else {
				item.CurrentStatus = "deleted"
			}
		}
		state.Work = append(state.Work, item)
		if len(state.Work) >= 20 {
			break
		}
	}
	if len(state.Work) == 0 && state.PendingApprovals == 0 && len(state.OpenTasks) == 0 {
		state.Note = "没有待处理审批、未闭环任务或近期工作记录。"
	}
	return state, nil
}

func (s *Service) petOpenTasks(ctx context.Context, tasks []domain.Task, pendingApprovals []domain.PetApproval) ([]PetOpenTask, int, int, error) {
	pendingByTask := make(map[string]string, len(pendingApprovals))
	for _, approval := range pendingApprovals {
		if taskID := petApprovalTargetTaskID(approval); taskID != "" {
			if _, exists := pendingByTask[taskID]; !exists {
				pendingByTask[taskID] = approval.ID
			}
		}
	}
	tasksByID := make(map[string]domain.Task, len(tasks))
	for _, task := range tasks {
		tasksByID[task.ID] = task
	}
	ids, err := s.store.PetCreatedTaskIDs(ctx)
	if err != nil {
		return nil, 0, 0, err
	}
	now := s.now().UTC()
	open := make([]PetOpenTask, 0, len(ids))
	attention := 0
	for _, taskID := range ids {
		if taskID == petTaskID {
			continue
		}
		task, ok := tasksByID[taskID]
		if !ok || task.Status == domain.TaskCompleted || task.Status == domain.TaskCancelled {
			continue
		}
		item := PetOpenTask{
			ID: task.ID, Code: task.Code, Title: task.Title, Status: string(task.Status),
			Goal:      truncateAssistantText(task.CurrentGoal, 300),
			CreatedAt: task.CreatedAt.Format(time.RFC3339), UpdatedAt: task.UpdatedAt.Format(time.RFC3339),
			AgeMinutes: int(now.Sub(task.CreatedAt).Minutes()),
		}
		if item.AgeMinutes < 0 {
			item.AgeMinutes = 0
		}
		if project, projectErr := s.store.Project(ctx, task.ProjectID); projectErr == nil {
			item.Project = project.Name
		}
		if workspace, workspaceErr := s.store.Workspace(ctx, task.WorkspaceID); workspaceErr == nil {
			item.Workspace = workspace.Name
		}
		if turn, turnErr := s.store.LatestTurnForAgent(ctx, task.ID, "main"); turnErr == nil {
			item.LastTurnStatus = string(turn.Status)
			item.LastTurnError = truncateAssistantText(turn.Error, 800)
			item.LastTurnResult = truncateAssistantText(turn.Result, 800)
		}
		if approvalID := pendingByTask[task.ID]; approvalID != "" {
			item.PendingApprovalID = approvalID
		}
		switch task.Status {
		case domain.TaskWaitingUser:
			item.AttentionReason = "waiting_user：等待主人决定或补充信息"
		case domain.TaskBlocked:
			item.AttentionReason = "blocked：需要解除阻塞条件"
		case domain.TaskFailed:
			item.AttentionReason = "failed：需要重试或决定是否继续"
		}
		if item.AttentionReason != "" {
			if item.PendingApprovalID != "" {
				item.AttentionReason = "已提交待审批 " + item.PendingApprovalID
			} else {
				item.NeedsAttention = true
				attention++
			}
		}
		open = append(open, item)
	}
	total := len(open)
	sort.SliceStable(open, func(i, j int) bool {
		if open[i].CreatedAt == open[j].CreatedAt {
			return open[i].ID < open[j].ID
		}
		return open[i].CreatedAt < open[j].CreatedAt
	})
	if len(open) > petOpenTaskListLimit {
		open = open[:petOpenTaskListLimit]
	}
	return open, total, attention, nil
}

func petApprovalTargetTaskID(item domain.PetApproval) string {
	var payload struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal([]byte(item.Payload), &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.TaskID)
}

func (s *Service) petInspection(ctx context.Context, tasks []domain.Task, pendingApprovals []domain.PetApproval, openTasks int, attentionTasks int) PetInspection {
	now := s.now().UTC()
	inspection := PetInspection{
		CheckedAt: now.Format(time.RFC3339), Health: "ok",
		Version: s.buildVersion, WebVersion: s.webVersion,
		PendingApprovals: len(pendingApprovals),
		OpenTasks:        openTasks, AttentionTasks: attentionTasks,
	}
	if !s.startedAt.IsZero() {
		inspection.StartedAt = s.startedAt.UTC().Format(time.RFC3339)
		inspection.UptimeMinutes = int(now.Sub(s.startedAt).Minutes())
		if inspection.UptimeMinutes < 0 {
			inspection.UptimeMinutes = 0
		}
	}
	for _, task := range tasks {
		if task.ID == petTaskID {
			continue
		}
		inspection.TasksTotal++
		switch task.Status {
		case domain.TaskActive:
			inspection.Active++
		case domain.TaskWaitingUser:
			inspection.WaitingUser++
		case domain.TaskFailed:
			inspection.Failed++
		case domain.TaskBlocked:
			inspection.Blocked++
		case domain.TaskCompleted:
			inspection.Completed++
		}
	}
	if channels, err := s.store.ChannelInstances(ctx, ""); err == nil {
		for _, channel := range channels {
			if channel.Retired || channel.Status == "ready" || channel.Status == "disabled" {
				continue
			}
			inspection.DegradedChannels = append(inspection.DegradedChannels, fmt.Sprintf("%s（%s）", channel.Name, channel.Status))
		}
	}
	switch {
	case attentionTasks > 0:
		inspection.Note = fmt.Sprintf("有 %d 个未闭环任务需要处理；先处理最早的，真实阻塞提交一次 owner_decision 审批。", attentionTasks)
	case openTasks > 0:
		inspection.Note = fmt.Sprintf("有 %d 个未闭环任务；先处理最早的一个，再创建新任务。", openTasks)
	default:
		inspection.Note = "没有未闭环任务。"
	}
	return inspection
}

func (s *Service) recordPetWork(ctx context.Context, kind, targetID, status string) {
	if strings.TrimSpace(targetID) == "" {
		return
	}
	_ = s.store.AppendPetEvent(ctx, domain.PetEvent{
		ID: domain.NewID("pet_event"), At: s.now().UTC(), Actor: "pet", Kind: kind,
		Detail: targetID, Status: status,
	})
}
