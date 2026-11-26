package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
	workspacepkg "github.com/ChinaKai/AHA2/internal/workspace"
)

var (
	ErrAgentCallForbidden = errors.New("agent control operation is forbidden")
	ErrAgentTurnInactive  = errors.New("agent turn is not active")
	// ErrAgentLimitReached reports a dispatch where no action could be queued
	// because the Task already held MaxAgents agents. It is returned only when
	// nothing was dispatched, so callers can distinguish a fully rejected
	// submission from a partial one that still created a batch.
	ErrAgentLimitReached = errors.New("agent limit reached")
	ErrRevisionConflict  = errors.New("resource revision conflict")
	ErrKnowledgeReadOnly = errors.New("knowledge entry is available through a read-only library binding")
	// A global skill applies to every Project on this machine, so creating one
	// is refused outside a Main turn that is acting for the Owner directly.
	ErrGlobalSkillForbidden = errors.New("global skill creation is not allowed in this Turn")
)

type AgentCallContext struct {
	Task    domain.Task
	Turn    domain.Turn
	Project domain.Project
}

type AgentTaskCreateInput struct {
	WorkspaceID     string `json:"workspace_id"`
	Title           string `json:"title"`
	Request         string `json:"request"`
	CloneHardware   bool   `json:"clone_hardware"`
	Backend         string `json:"backend,omitempty"`
	ModelSource     string `json:"model_source,omitempty"`
	ModelID         string `json:"model_id,omitempty"`
	WireModel       string `json:"wire_model,omitempty"`
	CodexAccountID  string `json:"codex_account_id,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

type AgentSkillCreateInput struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	Instructions string `json:"instructions"`
	// Scope is "project" (the default) or "global". A global skill applies to
	// every Project, so it is accepted only where agentGlobalSkillAllowed says so.
	Scope      string `json:"scope"`
	AutoAttach bool   `json:"auto_attach"`
}

type AgentRuntimeOption struct {
	Backend          string   `json:"backend"`
	ModelSource      string   `json:"model_source"`
	ModelID          string   `json:"model_id,omitempty"`
	WireModel        string   `json:"wire_model"`
	CodexAccountID   string   `json:"codex_account_id,omitempty"`
	DisplayName      string   `json:"display_name"`
	ProviderName     string   `json:"provider_name,omitempty"`
	DefaultEffort    string   `json:"default_reasoning_effort,omitempty"`
	ReasoningEfforts []string `json:"reasoning_efforts,omitempty"`
}

func (s *Service) AgentCallContext(ctx context.Context, claims agentapi.Claims, mainOnly bool) (AgentCallContext, error) {
	turn, err := s.store.Turn(ctx, claims.TurnID)
	if err != nil || turn.TaskID != claims.TaskID || turn.AgentID != claims.AgentID {
		return AgentCallContext{}, ErrAgentCallForbidden
	}
	if turn.Status != domain.TurnRunning && turn.Status != domain.TurnStarting {
		return AgentCallContext{}, ErrAgentTurnInactive
	}
	if mainOnly && turn.AgentID != "main" {
		return AgentCallContext{}, ErrAgentCallForbidden
	}
	task, err := s.store.Task(ctx, turn.TaskID)
	if err != nil {
		return AgentCallContext{}, err
	}
	project, err := s.store.Project(ctx, task.ProjectID)
	if err != nil {
		return AgentCallContext{}, err
	}
	return AgentCallContext{Task: task, Turn: turn, Project: project}, nil
}

func (s *Service) WaitAgentInbox(
	ctx context.Context,
	claims agentapi.Claims,
	after int64,
	timeout time.Duration,
) ([]domain.LiveInboxMessage, int64, error) {
	call, err := s.AgentCallContext(ctx, claims, false)
	if err != nil {
		return nil, after, err
	}
	if after < 0 {
		after = 0
	}
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	if timeout > 30*time.Second {
		timeout = 30 * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		items, claimErr := s.store.ClaimOwnerInboxForTurn(
			ctx, call.Task.ID, call.Turn.AgentID, call.Turn.RoundID, call.Turn.ID, after, 100,
		)
		if claimErr != nil {
			return nil, after, claimErr
		}
		if len(items) > 0 {
			messages := make([]domain.LiveInboxMessage, 0, len(items))
			for _, item := range items {
				messages = append(messages, liveInboxMessage(item))
				if item.Sequence > after {
					after = item.Sequence
				}
			}
			s.emit(ctx, call.Task.ID, "inbox", call.Turn.ID, "agent_inbox_delivered_in_turn", map[string]any{
				"round_id": call.Turn.RoundID, "turn_id": call.Turn.ID, "agent_id": call.Turn.AgentID,
				"message_count": len(messages), "next_cursor": after,
			})
			return messages, after, nil
		}
		select {
		case <-ctx.Done():
			return nil, after, ctx.Err()
		case <-timer.C:
			return []domain.LiveInboxMessage{}, after, nil
		case <-ticker.C:
		}
	}
}

func liveInboxMessage(item domain.AgentInboxItem) domain.LiveInboxMessage {
	result := domain.LiveInboxMessage{
		Sequence: item.Sequence, ID: item.ID, RoundID: item.RoundID,
		SourceKind: item.SourceKind, SourceAgentID: item.SourceAgentID,
		Content: item.Content, Payload: item.Payload, CreatedAt: item.CreatedAt,
		Source: domain.LiveInboxSource{Kind: "owner", Authority: "owner", AgentID: item.SourceAgentID},
	}
	if item.SourceKind == "pet_delegate_message" {
		result.Source = domain.LiveInboxSource{
			Kind: "pet", Authority: "owner_delegate", AgentID: "pet", DisplayName: "pet",
		}
	}
	if item.SourceKind == "peer_task_message" {
		// A peer Task is a collaborator, not the Owner. Keep the source Task ID
		// visible so the receiving Agent can judge the message on its provenance.
		result.Source = domain.LiveInboxSource{
			Kind: "task", Authority: "task_peer", AgentID: item.SourceAgentID,
			DisplayName: "task " + item.SourceAgentID,
		}
	}
	channelContext, _ := item.Payload["channel_context"].(map[string]any)
	if len(channelContext) == 0 {
		return result
	}
	actor, _ := channelContext["actor"].(map[string]any)
	route, _ := channelContext["route"].(map[string]any)
	result.Source = domain.LiveInboxSource{
		Kind: "channel", AgentID: item.SourceAgentID,
		DisplayName:    strings.TrimSpace(fmt.Sprint(actor["display_name"])),
		Role:           strings.TrimSpace(fmt.Sprint(actor["role"])),
		IdentityLinkID: strings.TrimSpace(fmt.Sprint(actor["identity_link_id"])),
		RouteMode:      strings.TrimSpace(fmt.Sprint(route["mode"])),
		IsBot:          func() bool { value, _ := actor["is_bot"].(bool); return value }(),
	}
	if result.Source.DisplayName == "<nil>" {
		result.Source.DisplayName = ""
	}
	if result.Source.Role == "<nil>" {
		result.Source.Role = ""
	}
	if result.Source.IdentityLinkID == "<nil>" {
		result.Source.IdentityLinkID = ""
	}
	if result.Source.RouteMode == "<nil>" {
		result.Source.RouteMode = ""
	}
	switch {
	case result.Source.Role == "owner":
		result.Source.Authority = "owner_channel"
	case result.Source.RouteMode == "task_route":
		result.Source.Authority = "task_channel_participant"
	default:
		result.Source.Authority = "channel_context"
	}
	return result
}

func (s *Service) AgentKnowledgePublishAllowed(ctx context.Context, call AgentCallContext) bool {
	if call.Turn.AgentID != "main" || !knowledgeEnabled(call.Project, call.Task) {
		return false
	}
	if channelContext, err := s.store.ChannelContextForInboxBatch(ctx, call.Turn.InboxBatchID); err == nil {
		return channelRouteMode(channelContext) == "task_route"
	}
	return true
}

// AgentGlobalSkillAllowed reports whether this Turn may create a skill that
// reaches beyond its own Project.
//
// Global scope applies to every Project on this machine, so it stays Main-only
// and is refused on a restricted group route, which is not acting for the Owner.
// It deliberately does not require Knowledge to be enabled: a skill is not
// knowledge, and gating one on the other would refuse for an unrelated reason.
func (s *Service) AgentGlobalSkillAllowed(ctx context.Context, call AgentCallContext) bool {
	if call.Turn.AgentID != "main" {
		return false
	}
	if channelContext, err := s.store.ChannelContextForInboxBatch(ctx, call.Turn.InboxBatchID); err == nil {
		return channelRouteMode(channelContext) == "task_route"
	}
	return true
}

// AgentChannelReceiptID resolves the routed channel receipt delivered to this
// Turn. It backs the narrow channel MCP: the tool answers the current routed
// message, so the model never has to reproduce an opaque receipt id.
func (s *Service) AgentChannelReceiptID(ctx context.Context, claims agentapi.Claims) (string, error) {
	call, err := s.AgentCallContext(ctx, claims, true)
	if err != nil {
		return "", err
	}
	channelContext, err := s.store.ChannelContextForInboxBatch(ctx, call.Turn.InboxBatchID)
	if err != nil {
		return "", ErrAgentCallForbidden
	}
	return channelMCPReceipt(channelContext)
}

// channelMCPReceipt accepts a group or private task_route context. Both modes
// have no automatic reply mirror and must answer explicitly. A
// conversation_host mirrors its answer itself, so it is deliberately excluded.
func channelMCPReceipt(channelContext map[string]any) (string, error) {
	if channelRouteMode(channelContext) != "task_route" {
		return "", ErrAgentCallForbidden
	}
	endpoint := strings.TrimSpace(fmt.Sprint(channelContext["endpoint"]))
	if endpoint != domain.ChannelEndpointGroupDigitalHuman && endpoint != domain.ChannelEndpointAssistantDM {
		return "", ErrAgentCallForbidden
	}
	receiptID, _ := channelContext["inbound_receipt_id"].(string)
	receiptID = strings.TrimSpace(receiptID)
	if receiptID == "" {
		return "", ErrAgentCallForbidden
	}
	return receiptID, nil
}

func (s *Service) SetAgentChannelReplyDecision(ctx context.Context, claims agentapi.Claims, decision, inboundReceiptID, reason string) error {
	if decision != "continue" && decision != "end" {
		return fmt.Errorf("channel reply decision must be continue or end")
	}
	inboundReceiptID = strings.TrimSpace(inboundReceiptID)
	reason = strings.TrimSpace(reason)
	if decision == "end" {
		if inboundReceiptID == "" {
			return fmt.Errorf("inbound_receipt_id is required when ending a channel reply")
		}
		if reason == "" || len([]rune(reason)) > 300 {
			return fmt.Errorf("reason must contain 1 to 300 characters when ending a channel reply")
		}
	}
	call, err := s.AgentCallContext(ctx, claims, true)
	if err != nil {
		return err
	}
	var channelContext map[string]any
	if inboundReceiptID != "" {
		channelContext, err = s.store.ChannelContextForReceipt(
			ctx, claims.TaskID, call.Turn.ID, inboundReceiptID,
		)
	} else {
		channelContext, err = s.store.ChannelContextForInboxBatch(ctx, call.Turn.InboxBatchID)
	}
	if err != nil {
		return ErrAgentCallForbidden
	}
	endpointKind := strings.TrimSpace(fmt.Sprint(channelContext["endpoint"]))
	if channelRouteMode(channelContext) != "task_route" ||
		(endpointKind != domain.ChannelEndpointGroupDigitalHuman && endpointKind != domain.ChannelEndpointAssistantDM) {
		return ErrAgentCallForbidden
	}
	if inboundReceiptID != "" {
		delivered, err := s.store.ChannelReceiptDeliveredToTurn(
			ctx, claims.TaskID, call.Turn.InboxBatchID, claims.TurnID, inboundReceiptID,
		)
		if err != nil {
			return err
		}
		if !delivered {
			return fmt.Errorf("inbound_receipt_id does not belong to this Turn")
		}
	}
	threadID, topicScoped := channelContext["thread_id"].(string)
	if endpointKind == domain.ChannelEndpointGroupDigitalHuman && topicScoped {
		conversationID, _ := channelContext["conversation_id"].(string)
		err = s.store.SetChannelTopicReplyDecision(ctx, claims.TurnID, conversationID, threadID, decision, reason)
	} else {
		err = s.store.SetTurnChannelReplyDecision(ctx, claims.TurnID, claims.TaskID, claims.AgentID, decision)
	}
	if err != nil {
		return err
	}
	terminalState := "decided_continue"
	reasonCode := "reply_decision_continue"
	reasonText := ""
	if decision == "end" {
		terminalState, reasonCode, reasonText = "decided_end", "reply_decision_end", reason
	}
	return s.SyncAgentChannelMessageStatuses(
		ctx, claims.TaskID, claims.TurnID, inboundReceiptID, terminalState, reasonCode, reasonText,
	)
}

type AgentFeishuTopicRevokeInput struct {
	ChatID           string `json:"chat_id"`
	ThreadID         string `json:"thread_id"`
	InboundReceiptID string `json:"inbound_receipt_id"`
	Reason           string `json:"reason"`
}

func (s *Service) RevokeAgentFeishuTopicWake(ctx context.Context, claims agentapi.Claims, input AgentFeishuTopicRevokeInput) error {
	call, err := s.AgentCallContext(ctx, claims, true)
	if err != nil {
		return err
	}
	if input.ChatID == "" || input.ThreadID == "" || len(input.ChatID) > 128 || len(input.ThreadID) > 128 ||
		strings.ContainsAny(input.ChatID+input.ThreadID, " \t\r\n") {
		return fmt.Errorf("valid chat_id and thread_id are required")
	}
	reason := strings.TrimSpace(input.Reason)
	if len([]rune(reason)) > 300 {
		return fmt.Errorf("reason must contain at most 300 characters")
	}
	err = s.store.RevokeFeishuTopicWake(ctx, call.Task.ID, call.Turn.AgentID, call.Turn.ID, input.ChatID,
		input.ThreadID, strings.TrimSpace(input.InboundReceiptID), reason, s.now().UTC())
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAgentCallForbidden
	}
	return err
}

// AgentFeishuTopicRoute describes the starting channel context for tool
// discovery. The action also verifies receipt ownership and the current route.
func (s *Service) AgentFeishuTopicRoute(ctx context.Context, call AgentCallContext) string {
	return s.feishuTopicRoute(ctx, call.Turn.AgentID, call.Turn.InboxBatchID)
}

func (s *Service) feishuTopicRoute(ctx context.Context, agentID, inboxBatchID string) string {
	if agentID != "main" {
		return ""
	}
	channelContext, err := s.store.ChannelContextForInboxBatch(ctx, inboxBatchID)
	threadID, _ := channelContext["thread_id"].(string)
	if err != nil || threadID == "" || channelContext["provider"] != "feishu" ||
		channelContext["endpoint"] != domain.ChannelEndpointGroupDigitalHuman {
		return ""
	}
	mode := channelRouteMode(channelContext)
	if mode == "task_route" || mode == "group_qa" {
		return mode
	}
	return ""
}

func (s *Service) UpdateAgentMemory(ctx context.Context, claims agentapi.Claims, update MemoryUpdate) (domain.TaskMemory, error) {
	call, err := s.agentWriteCallContext(ctx, claims)
	if err != nil {
		return domain.TaskMemory{}, err
	}
	if update.Empty() {
		return domain.TaskMemory{}, fmt.Errorf("memory update is empty")
	}
	now := s.now().UTC()
	if err := s.store.UpdateTaskMemory(ctx, call.Task.ID, call.Turn, now, update.Lines,
		func(memory *domain.TaskMemory) (*domain.TaskGoalChange, error) {
			return applyMemoryUpdate(memory, call.Turn, now, update)
		}); err != nil {
		return domain.TaskMemory{}, err
	}
	return s.store.TaskMemory(ctx, call.Task.ID)
}

func (s *Service) AddAgentProgress(ctx context.Context, claims agentapi.Claims, message string, attachmentIDs []string) error {
	call, err := s.AgentCallContext(ctx, claims, false)
	if err != nil {
		return err
	}
	message = strings.TrimSpace(message)
	if message == "" && len(attachmentIDs) == 0 {
		return fmt.Errorf("progress message or attachment is required")
	}
	if len([]rune(message)) > 4000 {
		return fmt.Errorf("progress message exceeds 4000 characters")
	}
	item := domain.ConversationItem{
		ID: domain.NewID("conversation"), TaskID: call.Task.ID, RoundID: call.Turn.RoundID, TurnID: call.Turn.ID,
		AgentID: call.Turn.AgentID, StreamAgentID: call.Turn.AgentID, FromAgentID: call.Turn.AgentID, ToAgentID: "owner",
		RouteKind: "agent_progress", Category: "update", Kind: "agent_message_update", Summary: message,
		Payload: map[string]any{"api": true}, CreatedAt: s.now().UTC(),
	}
	_, err = s.store.AddConversationItemWithAttachments(ctx, item, attachmentIDs)
	if err == nil {
		s.emitTurn(ctx, call.Turn, "agent_progress", map[string]any{"message": message})
	}
	return err
}

func (s *Service) PublishAgentChannelOutreach(ctx context.Context, taskID, turnID, conversationItemID string, recipientCount int) {
	s.emit(ctx, taskID, "conversation", conversationItemID, "agent_channel_outreach", map[string]any{
		"turn_id": turnID, "recipient_count": recipientCount,
	})
}

func (s *Service) PublishAgentChannelReply(ctx context.Context, taskID, turnID, conversationItemID string) {
	s.emit(ctx, taskID, "conversation", conversationItemID, "agent_channel_reply", map[string]any{
		"turn_id": turnID, "conversation_item_id": conversationItemID,
	})
}

func (s *Service) SyncAgentChannelMessageStatuses(ctx context.Context, taskID, turnID, receiptID, terminalState, reason, reasonText string) error {
	items, err := s.store.ReconcileChannelMessageStatuses(ctx, taskID, turnID, receiptID, terminalState, reason, reasonText, s.now().UTC())
	if err != nil {
		return err
	}
	for _, item := range items {
		s.emit(ctx, taskID, "conversation", item.ID, "agent_channel_status", map[string]any{
			"turn_id": turnID, "receipt_id": receiptID,
		})
	}
	return nil
}

func (s *Service) SubmitAgentKnowledge(ctx context.Context, claims agentapi.Claims, candidates []KnowledgeCandidate) ([]domain.KnowledgeEntry, error) {
	items, _, err := s.SubmitAgentKnowledgeProposals(ctx, claims, candidates)
	return items, err
}

func (s *Service) SubmitAgentKnowledgeProposals(ctx context.Context, claims agentapi.Claims, candidates []KnowledgeCandidate) ([]domain.KnowledgeEntry, []domain.KnowledgeProposal, error) {
	call, err := s.AgentCallContext(ctx, claims, true)
	if err != nil {
		return nil, nil, err
	}
	if channelContext, channelErr := s.store.ChannelContextForInboxBatch(ctx, call.Turn.InboxBatchID); channelErr == nil && channelRouteMode(channelContext) != "task_route" {
		return nil, nil, ErrAgentCallForbidden
	}
	if !knowledgeEnabled(call.Project, call.Task) {
		return nil, nil, ErrAgentCallForbidden
	}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.Title) == "" || strings.TrimSpace(candidate.Body) == "" || len([]rune(candidate.Title)) > 300 || len([]rune(candidate.Body)) > 20000 {
			return nil, nil, fmt.Errorf("knowledge title or body is invalid")
		}
		if candidate.Confidence < 0 || candidate.Confidence > 1 {
			return nil, nil, fmt.Errorf("knowledge confidence must be between 0 and 1")
		}
		if strings.TrimSpace(candidate.EntryID) == "" && candidate.Slug != nil && !store.ValidKnowledgeSlug(strings.TrimSpace(*candidate.Slug)) {
			return nil, nil, store.ErrKnowledgeInvalidSlug
		}
		if candidate.SortOrder != nil && *candidate.SortOrder < 0 {
			return nil, nil, fmt.Errorf("knowledge sort order must be non-negative")
		}
		if strings.TrimSpace(candidate.EntryID) == "" {
			if candidate.BaseRevision != 0 || (candidate.IsIndex != nil && *candidate.IsIndex) {
				return nil, nil, ErrRevisionConflict
			}
			continue
		}
		existing, err := s.store.Knowledge(ctx, candidate.EntryID)
		if err != nil {
			return nil, nil, ErrAgentCallForbidden
		}
		if existing.Scope == "project" && existing.ProjectID != call.Project.ID {
			allowed, bindingErr := s.store.CanContributeKnowledgeLibrary(ctx, existing.ProjectID, call.Project.ID)
			if bindingErr != nil {
				return nil, nil, bindingErr
			}
			if !allowed {
				return nil, nil, ErrKnowledgeReadOnly
			}
		}
		if candidate.BaseRevision <= 0 || candidate.BaseRevision != existing.Revision {
			return nil, nil, ErrRevisionConflict
		}
		requestedScope := candidate.Scope
		if requestedScope != "global" {
			requestedScope = "project"
		}
		if requestedScope != existing.Scope {
			return nil, nil, ErrAgentCallForbidden
		}
	}
	lines, _ := s.store.ListProductLines(ctx, call.Project.ID)
	line := resolveProductLine(lines, call.Task.TargetBranch, call.Project.DefaultBranch)
	return s.createKnowledgeCandidates(ctx, call.Project.ID, call.Task.ID, call.Turn.ID, call.Task.TaskBranch, line.ID, candidates)
}

func (s *Service) SubmitAgentKnowledgeFeedback(ctx context.Context, claims agentapi.Claims, feedback KnowledgeFeedback) (domain.KnowledgeEntry, error) {
	call, err := s.AgentCallContext(ctx, claims, false)
	if err != nil {
		return domain.KnowledgeEntry{}, err
	}
	if channelContext, channelErr := s.store.ChannelContextForInboxBatch(ctx, call.Turn.InboxBatchID); channelErr == nil && channelRouteMode(channelContext) != "task_route" {
		return domain.KnowledgeEntry{}, ErrAgentCallForbidden
	}
	kind := strings.TrimSpace(feedback.Kind)
	if kind != "helped" && kind != "stale" && kind != "wrong" {
		return domain.KnowledgeEntry{}, fmt.Errorf("invalid knowledge feedback")
	}
	// The legacy endpoint keeps its historical permission scope (any Agent in a
	// task route may mark global knowledge), but it must still write the source
	// the reader uses: an ID that resolves to the vault is written to the vault.
	outcome, err := s.submitAgentKnowledgeFeedbackForCall(ctx, call, feedback.EntryID, kind, true, "")
	if err != nil {
		return domain.KnowledgeEntry{}, err
	}
	if outcome.Knowledge == nil {
		return domain.KnowledgeEntry{}, fmt.Errorf("knowledge feedback did not return an entry")
	}
	return *outcome.Knowledge, nil
}

func (s *Service) SubmitAgentCollaboration(ctx context.Context, claims agentapi.Claims, actions []AgentAction, mainFollowup string) (string, int, error) {
	call, err := s.AgentCallContext(ctx, claims, true)
	if err != nil {
		return "", 0, err
	}
	if channelContext, channelErr := s.store.ChannelContextForInboxBatch(ctx, call.Turn.InboxBatchID); channelErr == nil && channelRouteMode(channelContext) != "task_route" {
		return "", 0, ErrAgentCallForbidden
	}
	singleMode := call.Task.CollaborationMode != "auto"
	batchID, created, err := s.spawnAgentTurns(ctx, call.Task, call.Turn, actions, strings.TrimSpace(mainFollowup))
	if err != nil {
		return batchID, created, err
	}
	if singleMode {
		// A single-agent Task has no dispatchable sub-agent surface. The enqueue
		// path has already recorded the visible per-action rejection; returning a
		// distinct error keeps the HTTP endpoint and the collab MCP facade from
		// reporting a successful empty batch on top of it.
		return "", 0, ErrAgentCollaborationRequiresAuto
	}
	return batchID, created, nil
}

func (s *Service) SelectedAgentSkills(ctx context.Context, claims agentapi.Claims) ([]domain.Skill, AgentCallContext, error) {
	call, err := s.AgentCallContext(ctx, claims, false)
	if err != nil {
		return nil, AgentCallContext{}, err
	}
	items := s.activeTaskSkills(ctx, call.Task)
	for index := range items {
		items[index].CanUpdate = items[index].Scope == "global" || items[index].ProjectID == call.Project.ID || items[index].BoundProjectID == call.Project.ID && items[index].BindingMode == "project"
	}
	return items, call, nil
}

func (s *Service) CreateAgentSkill(ctx context.Context, claims agentapi.Claims, input AgentSkillCreateInput) (domain.Skill, error) {
	call, err := s.AgentCallContext(ctx, claims, true)
	if err != nil {
		return domain.Skill{}, err
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.Instructions = strings.TrimSpace(input.Instructions)
	if input.Name == "" || input.Instructions == "" {
		return domain.Skill{}, fmt.Errorf("skill name and instructions are required")
	}
	scope := strings.ToLower(strings.TrimSpace(input.Scope))
	if scope == "" {
		scope = "project"
	}
	if scope != "project" && scope != "global" {
		return domain.Skill{}, fmt.Errorf(`skill scope must be "project" or "global"`)
	}
	allowGlobal := false
	projectID := call.Project.ID
	if scope == "global" {
		if !s.AgentGlobalSkillAllowed(ctx, call) {
			return domain.Skill{}, ErrGlobalSkillForbidden
		}
		allowGlobal = true
		projectID = ""
	}
	// Auto-attach is intentionally not applied here: a new skill starts with
	// no preference row. The owner can enable it from the Web vault Skills tab.
	item, err := s.CreateVaultSkill(ctx, VaultSkillWriteInput{
		Scope: scope, ProjectID: projectID, Name: input.Name,
		Description: input.Description, Instructions: input.Instructions,
		AllowGlobal: allowGlobal,
	})
	if err != nil {
		return domain.Skill{}, err
	}
	skillIDs := append(append([]string(nil), call.Task.SkillIDs...), item.ID)
	if _, err := s.UpdateTaskSkills(ctx, call.Task.ID, skillIDs); err != nil {
		// The package is already durable; report the task update failure instead
		// of deleting user content that the next Turn can still select.
		return domain.Skill{}, fmt.Errorf("skill created in the vault but attaching it to the task failed: %w", err)
	}
	return item, nil
}

func (s *Service) ApplicableAgentKnowledge(ctx context.Context, claims agentapi.Claims) ([]domain.KnowledgeEntry, error) {
	call, err := s.AgentCallContext(ctx, claims, false)
	if err != nil {
		return nil, err
	}
	if !knowledgeEnabled(call.Project, call.Task) {
		// A Task with knowledge switched off must not read knowledge through any
		// surface, including the Agent API.
		return []domain.KnowledgeEntry{}, nil
	}
	if channelContext, channelErr := s.store.ChannelContextForInboxBatch(ctx, call.Turn.InboxBatchID); channelErr == nil {
		if channelRouteMode(channelContext) != "task_route" {
			// Channel endpoints resolve each source project's current read
			// source; a vault read failure is returned instead of exposing
			// legacy rows.
			return s.channelAllowedKnowledge(
				ctx,
				strings.TrimSpace(fmt.Sprint(channelContext["instance_id"])),
				strings.TrimSpace(fmt.Sprint(channelContext["endpoint"])),
				strings.TrimSpace(fmt.Sprint(channelContext["conversation_id"])),
			)
		}
	}
	lines, _ := s.store.ListProductLines(ctx, call.Project.ID)
	line := resolveProductLine(lines, call.Task.TargetBranch, call.Project.DefaultBranch)
	// Project and global knowledge are read independently: a project still on
	// the legacy tables must not hide a ready vault global library, and a
	// vault-bound project must not see legacy global rows.
	project, projectStale, _, err := s.projectKnowledgeForTask(ctx, call.Project.ID, line, false)
	if err != nil {
		return nil, err
	}
	global, globalStale, _, err := s.globalKnowledgeForTask(ctx, false)
	if err != nil {
		return nil, err
	}
	items := make([]domain.KnowledgeEntry, 0, len(project)+len(projectStale)+len(global)+len(globalStale))
	items = append(items, project...)
	items = append(items, projectStale...)
	items = append(items, global...)
	items = append(items, globalStale...)
	// Root libraries are read-only and only visible to the pet's own Task.
	if s.isPetKnowledgeTask(ctx, call.Task) {
		if rootActive, rootStale, rootErr := s.vaultRootSnapshot(ctx); rootErr == nil {
			items = append(items, rootActive...)
			items = append(items, rootStale...)
		}
	}
	markAgentKnowledgeWritable(items, s.AgentKnowledgePublishAllowed(ctx, call))
	return items, nil
}

func markAgentKnowledgeWritable(items []domain.KnowledgeEntry, allowed bool) {
	for index := range items {
		if !allowed {
			items[index].CanProposeRevision = false
		} else if items[index].Scope == "global" {
			items[index].CanProposeRevision = true
		}
	}
}

func (s *Service) AgentProjectWorkspaces(ctx context.Context, claims agentapi.Claims) ([]domain.Workspace, error) {
	call, err := s.AgentCallContext(ctx, claims, true)
	if err != nil {
		return nil, err
	}
	return s.store.ListWorkspaces(ctx, call.Project.ID)
}

func (s *Service) AgentProjectRuntimes(ctx context.Context, claims agentapi.Claims) ([]AgentRuntimeOption, error) {
	call, err := s.AgentCallContext(ctx, claims, true)
	if err != nil {
		return nil, err
	}
	return s.agentProjectRuntimeOptions(ctx, call)
}

// agentProjectRuntimeOptions is the shared read behind the Main-only HTTP
// Agent API and the read-only workspace domain MCP. Both surfaces project the
// same options differently; neither can see a token, an environment value or
// an internal address because only display identity is read here.
func (s *Service) agentProjectRuntimeOptions(ctx context.Context, call AgentCallContext) ([]AgentRuntimeOption, error) {
	models, err := s.store.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]AgentRuntimeOption, 0, len(models))
	for _, model := range models {
		if model.Source == domain.ModelSourceOfficial || model.Source == domain.ModelSourceClaudeNative || model.DefaultEnvGroupID == "" {
			continue
		}
		efforts, _ := model.Capabilities["reasoning_efforts"].([]string)
		if len(efforts) == 0 {
			if values, ok := model.Capabilities["reasoning_efforts"].([]any); ok {
				for _, value := range values {
					if effort, ok := value.(string); ok {
						efforts = append(efforts, effort)
					}
				}
			}
		}
		result = append(result, AgentRuntimeOption{
			Backend: model.Backend, ModelSource: domain.ModelSourceProvider, ModelID: model.ID,
			WireModel: model.WireModel, DisplayName: model.DisplayName, ProviderName: model.ProviderName,
			DefaultEffort: model.DefaultEffort, ReasoningEfforts: efforts,
		})
	}
	accounts, err := s.store.ListCodexAccounts(ctx)
	if err != nil {
		return nil, err
	}
	for _, account := range accounts {
		if !account.CredentialConfigured || account.Status != "ready" {
			continue
		}
		for _, model := range account.AvailableModels {
			result = append(result, AgentRuntimeOption{
				Backend: "codex", ModelSource: domain.ModelSourceOfficial, WireModel: model.WireModel,
				CodexAccountID: account.ID, DisplayName: model.DisplayName, ProviderName: account.Label,
				DefaultEffort: model.DefaultEffort, ReasoningEfforts: model.ReasoningEfforts,
			})
		}
	}
	if workspace, workspaceErr := s.store.Workspace(ctx, call.Task.WorkspaceID); workspaceErr == nil {
		if probe, ok := workspace.Capabilities["claude"].(map[string]any); ok &&
			probe["status"] == "ready" && probe["auth_status"] == "logged_in" {
			for _, model := range workspacepkg.ClaudeModelsFromCapabilities(workspace.Capabilities) {
				result = append(result, AgentRuntimeOption{
					Backend: "claude", ModelSource: domain.ModelSourceClaudeNative, WireModel: model.WireModel,
					DisplayName: model.DisplayName, ProviderName: "Claude Code Official",
					ReasoningEfforts: model.SupportedEffortLevels,
				})
			}
		}
	}
	return result, nil
}

func (s *Service) CreateAgentTask(ctx context.Context, claims agentapi.Claims, input AgentTaskCreateInput) (domain.Task, domain.Turn, error) {
	call, err := s.AgentCallContext(ctx, claims, true)
	if err != nil {
		return domain.Task{}, domain.Turn{}, err
	}
	workspace, err := s.store.Workspace(ctx, strings.TrimSpace(input.WorkspaceID))
	if err != nil || workspace.ProjectID != call.Project.ID {
		return domain.Task{}, domain.Turn{}, ErrAgentCallForbidden
	}
	snapshot, err := s.store.RuntimeSnapshot(ctx, call.Turn.RuntimeConfigSnapshotID)
	if err != nil {
		return domain.Task{}, domain.Turn{}, err
	}
	modelSource := domain.ModelSourceProvider
	if snapshot.EnvGroupID == domain.ClaudeNativeEnvGroupID {
		modelSource = domain.ModelSourceClaudeNative
	} else if snapshot.CodexAccountID != "" {
		modelSource = domain.ModelSourceOfficial
	}
	backend, modelID, wireModel := snapshot.Backend, snapshot.ModelID, snapshot.WireModel
	codexAccountID, reasoningEffort := snapshot.CodexAccountID, snapshot.ReasoningEffort
	if strings.TrimSpace(input.Backend) != "" || strings.TrimSpace(input.ModelID) != "" || strings.TrimSpace(input.WireModel) != "" || strings.TrimSpace(input.CodexAccountID) != "" {
		backend, modelSource, modelID = strings.TrimSpace(input.Backend), strings.TrimSpace(input.ModelSource), strings.TrimSpace(input.ModelID)
		wireModel, codexAccountID = strings.TrimSpace(input.WireModel), strings.TrimSpace(input.CodexAccountID)
		if modelSource == "" {
			modelSource = domain.ModelSourceProvider
		}
	}
	if strings.TrimSpace(input.ReasoningEffort) != "" {
		reasoningEffort = strings.TrimSpace(input.ReasoningEffort)
	}
	filesystem, approval := parsePermissionsJSON(snapshot.PermissionsJSON)
	item, err := s.CreateTask(ctx, CreateTaskInput{
		ProjectID: call.Project.ID, WorkspaceID: workspace.ID, Title: input.Title, Request: input.Request,
		Isolation: "inplace", Backend: backend, ModelSource: modelSource,
		ModelID: modelID, WireModel: wireModel,
		CodexAccountID: codexAccountID, ReasoningEffort: reasoningEffort,
		StreamIdleTimeoutMS: intPointer(snapshot.StreamIdleTimeoutMS),
		StreamMaxRetries:    intPointer(snapshot.StreamMaxRetries),
		Filesystem:          filesystem, Approval: approval, ProxyEnabled: snapshot.ProxyEnabled,
		CollaborationMode: "single", MaxAgents: 1, KnowledgePolicy: call.Task.KnowledgePolicy,
	})
	if err != nil {
		return domain.Task{}, domain.Turn{}, err
	}
	if input.CloneHardware {
		if err := s.CloneTaskHardware(ctx, call.Task.ID, item.ID); err != nil {
			_ = s.store.UpdateTaskStatus(ctx, item.ID, item.Status, domain.TaskBlocked, timeString(s.now().UTC()), "")
			return item, domain.Turn{}, fmt.Errorf("clone hardware: %w", err)
		}
	}
	turn, err := s.SubmitMessage(ctx, item.ID, item.OriginalRequest)
	return item, turn, err
}

func intPointer(value int) *int {
	return &value
}

func (s *Service) AgentProjectTask(ctx context.Context, claims agentapi.Claims, taskID string) (domain.Task, []domain.Turn, error) {
	call, err := s.AgentCallContext(ctx, claims, true)
	if err != nil {
		return domain.Task{}, nil, err
	}
	item, err := s.store.Task(ctx, strings.TrimSpace(taskID))
	if err != nil || item.ProjectID != call.Project.ID || (call.Task.ID == petTaskID && item.ID != call.Task.ID) {
		return domain.Task{}, nil, ErrAgentCallForbidden
	}
	turns, err := s.store.ListTurns(ctx, item.ID)
	return item, turns, err
}

func (s *Service) CloneTaskHardware(ctx context.Context, sourceTaskID, targetTaskID string) error {
	groups, err := s.store.HardwareGroups(ctx, sourceTaskID)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	bindings := make([]domain.TaskHardwareBinding, 0, len(groups))
	for _, group := range groups {
		if group.ResourceID == "" {
			return fmt.Errorf("hardware %s has no system resource", group.ID)
		}
		bindings = append(bindings, domain.TaskHardwareBinding{
			TaskID: targetTaskID, ID: group.ID, ResourceID: group.ResourceID,
			CreatedAt: now, UpdatedAt: now,
		})
	}
	return s.store.ReplaceTaskHardwareBindings(ctx, targetTaskID, bindings)
}
