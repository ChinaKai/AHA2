package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/domain"
)

// ErrAgentReadOnly refuses a write made by a read-only Agent. It is kept
// separate from ErrAgentCallForbidden so the caller can explain the refusal
// without exposing the underlying permission check.
var ErrAgentReadOnly = errors.New("agent is read-only")

const (
	peerTaskMessageKind = "peer_task_message"
	// coreTaskMessageMaxRunes bounds a Task-to-Task message so one peer cannot
	// use another Task as an unbounded storage or prompt channel.
	coreTaskMessageMaxRunes = 8000
)

// AgentCoreTurnView is the bounded Turn shape exposed by the core MCP. It
// deliberately omits instructions, prompts, backend sessions and results:
// another Task in the same Project may see status, not execution internals.
type AgentCoreTurnView struct {
	ID            string            `json:"id"`
	AgentID       string            `json:"agent_id"`
	Sequence      int               `json:"sequence"`
	Status        domain.TurnStatus `json:"status"`
	Title         string            `json:"title,omitempty"`
	WaitingReason string            `json:"waiting_reason,omitempty"`
	QueuedAt      time.Time         `json:"queued_at,omitempty"`
	StartedAt     time.Time         `json:"started_at,omitempty"`
	FinishedAt    time.Time         `json:"finished_at,omitempty"`
}

// AgentCoreTaskView is the bounded Task shape exposed by the core MCP. It
// carries the coordination fields a peer Task needs, not credentials,
// workspace paths, model configuration or the original request.
type AgentCoreTaskView struct {
	ID          string              `json:"id"`
	Code        string              `json:"code,omitempty"`
	ProjectID   string              `json:"project_id"`
	Title       string              `json:"title"`
	CurrentGoal string              `json:"current_goal"`
	Status      domain.TaskStatus   `json:"status"`
	ReadOnly    bool                `json:"read_only,omitempty"`
	CreatedAt   time.Time           `json:"created_at"`
	UpdatedAt   time.Time           `json:"updated_at"`
	CompletedAt time.Time           `json:"completed_at,omitempty"`
	Turns       []AgentCoreTurnView `json:"turns,omitempty"`
}

// AgentWriteAllowed reports whether this Turn may use the core write tools.
// The read-only filesystem permission is the same switch that keeps the model
// from editing files; a review Task must not gain AHA state writes through MCP.
// Unavailable snapshot metadata fails closed.
func (s *Service) AgentWriteAllowed(ctx context.Context, call AgentCallContext) bool {
	return s.agentSnapshotWritable(ctx, call.Task, call.Turn.RuntimeConfigSnapshotID, call.Turn.AgentID)
}

func (s *Service) agentSnapshotWritable(ctx context.Context, task domain.Task, snapshotID, agentID string) bool {
	if agentID != "main" || task.ReadOnly || task.Status.Terminal() {
		return false
	}
	snapshot, err := s.store.RuntimeSnapshot(ctx, snapshotID)
	if err != nil {
		return false
	}
	return strictSnapshotWritable(snapshot.PermissionsJSON)
}

// strictSnapshotWritable accepts only an explicit, well-formed permission
// snapshot. The general runtime parser is deliberately tolerant for legacy
// snapshots, but a tolerant result must never turn an abnormal snapshot into a
// write grant: malformed JSON, null, wrong field types, missing fields and
// unknown or differently-cased filesystem values all fail closed.
func strictSnapshotWritable(permissionsJSON string) bool {
	var payload struct {
		Filesystem *string `json:"filesystem"`
		Approval   *string `json:"approval"`
	}
	if err := json.Unmarshal([]byte(permissionsJSON), &payload); err != nil {
		return false
	}
	if payload.Filesystem == nil || payload.Approval == nil {
		return false
	}
	switch *payload.Approval {
	case "never", "auto":
	default:
		return false
	}
	switch *payload.Filesystem {
	case "workspace-write", "danger-full-access":
		return true
	default:
		return false
	}
}

func (s *Service) agentWriteCallContext(ctx context.Context, claims agentapi.Claims) (AgentCallContext, error) {
	call, err := s.AgentCallContext(ctx, claims, true)
	if err != nil {
		return AgentCallContext{}, err
	}
	if !s.AgentWriteAllowed(ctx, call) {
		return AgentCallContext{}, ErrAgentReadOnly
	}
	return call, nil
}

func agentCoreTaskView(item domain.Task) AgentCoreTaskView {
	return AgentCoreTaskView{
		ID: item.ID, Code: item.Code, ProjectID: item.ProjectID, Title: item.Title,
		CurrentGoal: item.CurrentGoal, Status: item.Status, ReadOnly: item.ReadOnly,
		CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt, CompletedAt: item.CompletedAt,
	}
}

func agentCoreTurnView(item domain.Turn) AgentCoreTurnView {
	return AgentCoreTurnView{
		ID: item.ID, AgentID: item.AgentID, Sequence: item.Sequence, Status: item.Status,
		Title: item.Title, WaitingReason: item.WaitingReason,
		QueuedAt: item.QueuedAt, StartedAt: item.StartedAt, FinishedAt: item.FinishedAt,
	}
}

// AgentCoreTaskGet returns one Task in the caller's Project. The current Task
// is the default so an Agent does not need to know its own ID.
func (s *Service) AgentCoreTaskGet(ctx context.Context, claims agentapi.Claims, taskID string) (AgentCoreTaskView, error) {
	call, err := s.AgentCallContext(ctx, claims, false)
	if err != nil {
		return AgentCoreTaskView{}, err
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		taskID = call.Task.ID
	}
	item, err := s.store.Task(ctx, taskID)
	if err != nil || item.ProjectID != call.Project.ID || (call.Task.ID == petTaskID && item.ID != call.Task.ID) {
		return AgentCoreTaskView{}, ErrAgentCallForbidden
	}
	turns, err := s.store.ListTurns(ctx, item.ID)
	if err != nil {
		return AgentCoreTaskView{}, err
	}
	view := agentCoreTaskView(item)
	view.Turns = make([]AgentCoreTurnView, 0, len(turns))
	for _, turn := range turns {
		view.Turns = append(view.Turns, agentCoreTurnView(turn))
	}
	return view, nil
}

// AgentCoreTaskList returns Task summaries from the caller's Project only.
// The store sorts newest first; limit is clamped before the read so a caller
// cannot request the whole Project by accident.
func (s *Service) AgentCoreTaskList(ctx context.Context, claims agentapi.Claims, limit int) ([]AgentCoreTaskView, error) {
	call, err := s.AgentCallContext(ctx, claims, false)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	items, err := s.store.ListTasks(ctx, call.Project.ID)
	if err != nil {
		return nil, err
	}
	result := make([]AgentCoreTaskView, 0, min(len(items), limit))
	for _, item := range items {
		if call.Task.ID == petTaskID && item.ID != call.Task.ID {
			continue
		}
		if len(result) == limit {
			break
		}
		result = append(result, agentCoreTaskView(item))
	}
	return result, nil
}

// SubmitPeerTaskMessage sends a Main Agent message to another Task in the same
// Project. The target must be writable and not the caller itself. The message
// is visibly sourced from the sending Task, never from the Owner, so the target
// cannot treat it as an owner instruction or permission grant.
func (s *Service) SubmitPeerTaskMessage(ctx context.Context, claims agentapi.Claims, targetTaskID, content string) (domain.Turn, error) {
	call, err := s.agentWriteCallContext(ctx, claims)
	if err != nil {
		return domain.Turn{}, err
	}
	targetTaskID = strings.TrimSpace(targetTaskID)
	if targetTaskID == "" || targetTaskID == call.Task.ID {
		return domain.Turn{}, ErrAgentCallForbidden
	}
	target, err := s.store.Task(ctx, targetTaskID)
	if err != nil || target.ProjectID != call.Project.ID || target.ID == petTaskID || target.ReadOnly {
		return domain.Turn{}, ErrAgentCallForbidden
	}
	content = strings.TrimSpace(content)
	if content == "" || len([]rune(content)) > coreTaskMessageMaxRunes {
		return domain.Turn{}, fmt.Errorf("message must be 1-%d characters", coreTaskMessageMaxRunes)
	}
	return s.submitMessageWithSource(
		ctx, target.ID, "main", content, nil,
		"", "", "", nil, "task:"+call.Task.ID, peerTaskMessageKind, false,
	)
}

// AddAgentCoreProgress records progress from the Main Agent only. The HTTP
// progress endpoint stays available to every Agent in a collaboration; the MCP
// facade narrows it because the core tool set is shared with read-only callers.
func (s *Service) AddAgentCoreProgress(ctx context.Context, claims agentapi.Claims, message string) error {
	if _, err := s.agentWriteCallContext(ctx, claims); err != nil {
		return err
	}
	return s.AddAgentProgress(ctx, claims, message, nil)
}

// BindAgentAttachments binds existing unsent attachment IDs to a new
// conversation item. Upload and download remain on the HTTP attachment API;
// this method only proves the IDs belong to the current Task and flips their
// message_id so they can no longer be bound twice.
func (s *Service) BindAgentAttachments(ctx context.Context, claims agentapi.Claims, message string, attachmentIDs []string) ([]domain.Attachment, error) {
	call, err := s.agentWriteCallContext(ctx, claims)
	if err != nil {
		return nil, err
	}
	message = strings.TrimSpace(message)
	if len([]rune(message)) > 4000 {
		return nil, fmt.Errorf("message exceeds 4000 characters")
	}
	if len(attachmentIDs) == 0 {
		return nil, fmt.Errorf("attachment_ids is required")
	}
	if len(attachmentIDs) > 8 {
		return nil, fmt.Errorf("at most 8 attachments are allowed")
	}
	seen := map[string]bool{}
	for _, id := range attachmentIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return nil, fmt.Errorf("invalid attachment selection")
		}
		seen[id] = true
		item, err := s.store.Attachment(ctx, call.Task.ID, id)
		if err != nil {
			return nil, fmt.Errorf("attachment %s is unavailable", id)
		}
		if item.MessageID != "" {
			return nil, fmt.Errorf("attachment %s is already sent", id)
		}
	}
	summary := message
	if summary == "" {
		summary = "附件"
	}
	item := domain.ConversationItem{
		ID: domain.NewID("conversation"), TaskID: call.Task.ID, RoundID: call.Turn.RoundID, TurnID: call.Turn.ID,
		AgentID: call.Turn.AgentID, StreamAgentID: call.Turn.AgentID, FromAgentID: call.Turn.AgentID,
		ToAgentID: "owner", RouteKind: "agent_attachment_binding", Category: "update",
		Kind: "agent_message_update", Summary: summary, Payload: map[string]any{"api": true},
		CreatedAt: s.now().UTC(),
	}
	created, err := s.store.AddConversationItemWithAttachments(ctx, item, attachmentIDs)
	if err != nil {
		return nil, err
	}
	attachments, _ := created.Payload["attachments"].([]domain.Attachment)
	s.emitTurn(ctx, call.Turn, "agent_attachment_binding", map[string]any{"attachment_count": len(attachments)})
	return attachments, nil
}
