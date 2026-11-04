package app

import (
	"context"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

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

type PetWorkState struct {
	PendingApprovals int                  `json:"pending_approvals"`
	Approvals        []domain.PetApproval `json:"approvals"`
	Work             []PetWorkItem        `json:"work"`
	OpenLoops        string               `json:"open_loops,omitempty"`
	OpenLoopsPath    string               `json:"open_loops_path,omitempty"`
	OpenLoopsNote    string               `json:"open_loops_note,omitempty"`
	Agenda           string               `json:"agenda,omitempty"`
	AgendaPath       string               `json:"agenda_path,omitempty"`
	AgendaNote       string               `json:"agenda_note,omitempty"`
	LabPath          string               `json:"lab_path,omitempty"`
	Note             string               `json:"note,omitempty"`
}

// PetWorkState is the pet's own wake-up briefing: approval results plus the
// tasks/projects/workspaces it touched recently and their current progress.
// It only reads state; the pet decides whether to continue, remind the owner,
// or record the outcome in worklog.
func (s *Service) PetWorkState(ctx context.Context) (PetWorkState, error) {
	approvals, err := s.store.ListPetApprovals(ctx, 20)
	if err != nil {
		return PetWorkState{}, err
	}
	loops := s.PetOpenLoops(ctx)
	agenda := s.PetAgenda(ctx)
	state := PetWorkState{
		Approvals: approvals, Work: make([]PetWorkItem, 0),
		OpenLoops: loops, OpenLoopsPath: s.petOpenLoopsPath(ctx), OpenLoopsNote: petLoopsNote(loops),
		Agenda: agenda, AgendaPath: s.petAgendaPath(ctx), AgendaNote: petAgendaNote(agenda),
		LabPath: s.petLabPath(ctx),
	}
	for _, approval := range approvals {
		if approval.Status == petApprovalPending {
			state.PendingApprovals++
		}
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
	if len(state.Work) == 0 && state.PendingApprovals == 0 {
		state.Note = "没有待处理审批或近期工作记录。"
	}
	return state, nil
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
