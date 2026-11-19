package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/domain"
)

// ErrAgentCollaborationRequiresAuto reports a direct collaboration call from a
// Task that is not in auto collaboration mode. Single-agent mode has no
// dispatchable sub-agent surface; the error keeps the HTTP endpoint and the MCP
// facade refusing instead of returning a successful empty batch.
var ErrAgentCollaborationRequiresAuto = errors.New("collaboration requires auto mode")

// ErrAgentCollabMCPNothingQueued reports a syntactically valid submission that
// dispatched no sub-agent, for example because the Task already reached its
// Max Agents limit. The server-side cap is the authority; the tool must not
// report success for a batch it never created.
var ErrAgentCollabMCPNothingQueued = errors.New("no agent assignment was queued")

// ErrAgentCollabMCPBatchInvalid reports an empty batch identifier. Other Task,
// other Project and unknown batches all stay behind ErrAgentCallForbidden so
// the caller cannot probe for their existence.
var ErrAgentCollabMCPBatchInvalid = errors.New("collaboration batch id is invalid")

// ErrAgentCollabMCPInvalidInput reports an action list outside the existing
// HTTP contract (1 to 8 entries) or a missing required field. The MCP schema
// marks the same fields required, but the server rejects them itself rather
// than trusting the client to honor the schema.
var ErrAgentCollabMCPInvalidInput = errors.New("collaboration input is invalid")

// AgentCollabMCPAction is the reduced action shape this domain accepts. The
// HTTP batches endpoint also accepts backend/model/effort/filesystem/approval
// overrides; this facade deliberately keeps the first slice to the four fields
// the Owner asked for, so a caller cannot widen a sub-agent's runtime ceiling
// through the MCP surface. Pointer fields distinguish an omitted key from an
// empty value, so a missing agent_id/title/assignment is rejected instead of
// silently becoming an empty string.
type AgentCollabMCPAction struct {
	AgentID    *string `json:"agent_id"`
	Title      *string `json:"title"`
	Assignment *string `json:"assignment"`
	Required   *bool   `json:"required,omitempty"`
}

type AgentCollabMCPSubmitInput struct {
	Actions                 []AgentCollabMCPAction `json:"actions"`
	IntegrationInstructions *string                `json:"integration_instructions"`
}

type AgentCollabMCPSubmitResult struct {
	BatchID       string `json:"batch_id"`
	CreatedAgents int    `json:"created_agents"`
}

// AgentCollabMCPAllowed is the single gate for the collab domain MCP. The
// runtime injects the server only when this returns true, and every tool call
// re-checks it server-side. The domain is Main-only, requires auto
// collaboration mode, and is never exposed to a channel-hosted Turn; the pet
// Task is outside it for the same reason as the other project-scoped domains.
func (s *Service) AgentCollabMCPAllowed(ctx context.Context, call AgentCallContext) bool {
	if call.Task.ID == petTaskID || call.Task.ProjectID == petProjectID {
		return false
	}
	if strings.TrimSpace(call.Turn.AgentID) != "main" {
		return false
	}
	if call.Task.CollaborationMode != "auto" {
		return false
	}
	return !s.agentMCPChannelBound(ctx, call.Task.ID, call.Turn.InboxBatchID)
}

// collabMCPInjected mirrors AgentCollabMCPAllowed for the runtime path, which
// has no AgentCallContext yet. agentID is the Agent the Turn will run as.
func (s *Service) collabMCPInjected(ctx context.Context, task domain.Task, agentID, inboxBatchID string) bool {
	if task.ID == petTaskID || task.ProjectID == petProjectID {
		return false
	}
	if strings.TrimSpace(agentID) != "main" || task.CollaborationMode != "auto" {
		return false
	}
	return !s.agentMCPChannelBound(ctx, task.ID, inboxBatchID)
}

func (s *Service) agentCollabMCPContext(ctx context.Context, claims agentapi.Claims) (AgentCallContext, error) {
	call, err := s.AgentCallContext(ctx, claims, true)
	if err != nil {
		return AgentCallContext{}, err
	}
	if call.Task.ID == petTaskID || call.Task.ProjectID == petProjectID || strings.TrimSpace(call.Turn.AgentID) != "main" {
		return AgentCallContext{}, ErrAgentCallForbidden
	}
	if call.Task.CollaborationMode != "auto" {
		return AgentCallContext{}, ErrAgentCollaborationRequiresAuto
	}
	if s.agentMCPChannelBound(ctx, call.Task.ID, call.Turn.InboxBatchID) {
		return AgentCallContext{}, ErrAgentCallForbidden
	}
	return call, nil
}

// AgentCollabMCPSubmit dispatches through the existing collaboration service,
// so duplicate detection, the agent cap, runtime ceiling checks and the
// single-agent refusal are the same server-side rules the HTTP endpoint uses.
// It never starts an agent itself and exposes no cancellation, retry, list or
// agent-lifecycle operation.
func (s *Service) AgentCollabMCPSubmit(ctx context.Context, claims agentapi.Claims, input AgentCollabMCPSubmitInput) (AgentCollabMCPSubmitResult, error) {
	call, err := s.agentCollabMCPContext(ctx, claims)
	if err != nil {
		return AgentCollabMCPSubmitResult{}, err
	}
	if len(input.Actions) == 0 || len(input.Actions) > 8 || input.IntegrationInstructions == nil {
		return AgentCollabMCPSubmitResult{}, ErrAgentCollabMCPInvalidInput
	}
	actions := make([]AgentAction, 0, len(input.Actions))
	for _, action := range input.Actions {
		if action.AgentID == nil || action.Title == nil || action.Assignment == nil ||
			strings.TrimSpace(*action.Assignment) == "" {
			return AgentCollabMCPSubmitResult{}, ErrAgentCollabMCPInvalidInput
		}
		// The existing HTTP handler normalizes every action to required=true.
		// Keep that behavior instead of introducing a second default.
		actions = append(actions, AgentAction{
			AgentID: *action.AgentID, Title: *action.Title,
			Assignment: *action.Assignment, Required: true,
		})
	}
	batchID, created, err := s.spawnAgentTurns(
		ctx, call.Task, call.Turn, actions, strings.TrimSpace(*input.IntegrationInstructions),
	)
	if err != nil {
		var rejected *CollaborationAssignmentError
		if errors.As(err, &rejected) {
			return AgentCollabMCPSubmitResult{BatchID: rejected.BatchID, CreatedAgents: rejected.Created}, err
		}
		return AgentCollabMCPSubmitResult{}, err
	}
	if batchID == "" || created == 0 {
		// No batch row exists, so reporting success would make the model wait on
		// an identifier that can never resolve. The cap and the assignment
		// filters have already recorded an orchestration error.
		return AgentCollabMCPSubmitResult{}, ErrAgentCollabMCPNothingQueued
	}
	return AgentCollabMCPSubmitResult{BatchID: batchID, CreatedAgents: created}, nil
}

// AgentCollabMCPWait polls an existing batch through the HTTP wait service. The
// batch must belong to this Task and this Round; any other identifier is
// rejected as forbidden, so the tool cannot distinguish "another Task's batch"
// from "does not exist". A timeout is a normal result (Complete=false).
func (s *Service) AgentCollabMCPWait(ctx context.Context, claims agentapi.Claims, batchID string, after int64, timeout time.Duration) (domain.CollaborationProgress, error) {
	if _, err := s.agentCollabMCPContext(ctx, claims); err != nil {
		return domain.CollaborationProgress{}, err
	}
	if strings.TrimSpace(batchID) == "" {
		return domain.CollaborationProgress{}, ErrAgentCollabMCPBatchInvalid
	}
	return s.WaitAgentCollaboration(ctx, claims, batchID, after, timeout)
}

// CollabMCPRejection is the safe per-action rejection shape. The Chinese
// message from CollaborationAssignmentRejection is omitted: it can embed Turn
// identifiers, and the code/reason pair already tells the model what to do.
type CollabMCPRejection struct {
	AgentID string `json:"agent_id"`
	Code    string `json:"code"`
	Reason  string `json:"reason"`
}

func CollabMCPRejectionViews(items []CollaborationAssignmentRejection) []CollabMCPRejection {
	result := make([]CollabMCPRejection, 0, len(items))
	for _, item := range items {
		result = append(result, CollabMCPRejection{AgentID: item.AgentID, Code: item.Code, Reason: item.Reason})
	}
	return result
}
