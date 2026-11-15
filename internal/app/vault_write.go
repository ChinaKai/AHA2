package app

import (
	"context"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
)

// AgentKnowledgeEntryInput is the payload of POST
// /api/v1/agent/knowledge/entries. ProjectID is accepted only so the server can
// prove it matches the Task token; it is never used to route the write.
type AgentKnowledgeEntryInput struct {
	Mode           string   `json:"mode"`
	Scope          string   `json:"scope"`
	ProjectID      string   `json:"project_id"`
	EntryID        string   `json:"entry_id"`
	BaseRevision   int      `json:"base_revision"`
	BaseHash       string   `json:"base_hash"`
	Type           string   `json:"type"`
	Title          string   `json:"title"`
	Summary        string   `json:"summary"`
	Body           string   `json:"body"`
	Tags           []string `json:"tags"`
	Confidence     *float64 `json:"confidence"`
	SearchQuery    string   `json:"search_query"`
	DuplicateAck   bool     `json:"duplicate_ack"`
	Group          string   `json:"group"`
	IdempotencyKey string   `json:"idempotency_key"`
}

// AgentKnowledgeEntryResult reports which source served the write. Vault writes
// always carry Entry; the legacy fallback returns the proposal pipeline output.
// Warning is set when the owner-visible vault switch is on but this Turn had to
// fall back to legacy (unbound project, index unavailable, support override).
type AgentKnowledgeEntryResult struct {
	Source    string                      `json:"source"`
	Warning   string                      `json:"warning,omitempty"`
	Entry     *knowledgevault.WriteResult `json:"entry,omitempty"`
	Knowledge []domain.KnowledgeEntry     `json:"knowledge,omitempty"`
	Proposals []domain.KnowledgeProposal  `json:"proposals,omitempty"`
}

type AgentKnowledgeFeedbackInput struct {
	EntryID        string `json:"entry_id"`
	Kind           string `json:"kind"`
	Scope          string `json:"scope"`
	ProjectID      string `json:"project_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

type AgentKnowledgeFeedbackResult struct {
	Source    string                         `json:"source"`
	Warning   string                         `json:"warning,omitempty"`
	Feedback  *knowledgevault.FeedbackResult `json:"feedback,omitempty"`
	Knowledge *domain.KnowledgeEntry         `json:"knowledge,omitempty"`
}

// SetKnowledgeVaultRuntimeWrite applies the support-only emergency Agent write
// cutoff. It never enables or disables the vault itself: the owner-visible
// manager switch decides whether reads, writes, skills and Web pages use the
// vault, and writes simply follow the source that reads use.
func (s *Service) SetKnowledgeVaultRuntimeWrite(enabled bool) {
	s.knowledgeVaultRuntimeWrite = enabled
	if s.knowledgeVault != nil {
		// Keep the engine gate and the routing gate in lockstep: the engine must
		// reject direct writes whenever the service would route to legacy.
		s.knowledgeVault.SetRuntimeWrite(enabled)
	}
}

// vaultKnowledgeWriteEnabled follows the read source: a project reads from the
// vault only when the manager switch matches, the support overrides allow it,
// the index is ready and the manifest binds the project; it may write the vault
// only when the support-only write cutoff is also on. There is no path where a
// Turn reads the vault and writes the legacy tables.
func (s *Service) vaultKnowledgeWriteEnabled(projectID string) bool {
	if !s.vaultReadEnabled(projectID) {
		return false
	}
	return s.knowledgeVaultRuntimeWrite && s.knowledgeVault.RuntimeWriteEnabled()
}

// vaultFallbackWarning explains a legacy fallback when the owner-visible switch
// is on. It only looks at the in-memory Reader (never probes the filesystem) so
// it is cheap enough for a write response; without it an Agent would silently
// see legacy knowledge and never learn that the vault is degraded.
func (s *Service) vaultFallbackWarning(projectID string) string {
	if s == nil || s.knowledgeVault == nil || !s.knowledgeVault.Enabled() {
		return ""
	}
	if !s.knowledgeVaultRuntimeRead {
		return "知识库已启用，但支持诊断开关关闭了读取；本次使用旧知识库。"
	}
	if !knowledgeVaultReadAllowed(projectID) {
		return "知识库已启用，但支持诊断的项目回退名单排除了当前项目；本次使用旧知识库。"
	}
	reason := ""
	err := s.knowledgeVault.WithReader(func(reader *knowledgevault.Reader) error {
		switch {
		case !reader.IndexReady():
			reason = "索引未就绪"
		case strings.TrimSpace(reader.Manifest().Projects[projectID]) == "":
			reason = "项目未绑定到知识库"
		}
		return nil
	})
	if err != nil {
		reason = err.Error()
	}
	if reason == "" {
		return ""
	}
	return "知识库已启用，但" + reason + "；本次使用旧知识库。"
}

func (s *Service) vaultWriteDisabledError() error {
	return &knowledgevault.WriteError{
		Code:    knowledgevault.WriteErrorDisabled,
		Message: "this project reads from the vault but the Agent vault write gate is off; refusing to fall back to the legacy knowledge tables",
	}
}

// legacyKnowledgeBudget mirrors knowledgevault.DefaultWriteBudget for the
// fallback path, which has no durable agent-writes state file of its own.
type legacyKnowledgeBudget struct {
	count int
	at    time.Time
}

const legacyKnowledgeWriteBudget = knowledgevault.DefaultWriteBudget

func legacyKnowledgeBudgetKey(taskID, turnID string) string {
	key := strings.TrimSpace(taskID) + "|" + strings.TrimSpace(turnID)
	if key == "|" {
		return ""
	}
	return key
}

func (s *Service) pruneLegacyKnowledgeBudgetLocked(now time.Time) {
	for existing, value := range s.agentKnowledgeBudget {
		if now.Sub(value.at) > 24*time.Hour {
			delete(s.agentKnowledgeBudget, existing)
		}
	}
}

func (s *Service) legacyKnowledgeBudgetCount(taskID, turnID string) int {
	key := legacyKnowledgeBudgetKey(taskID, turnID)
	if key == "" {
		return 0
	}
	s.agentKnowledgeBudgetMu.Lock()
	defer s.agentKnowledgeBudgetMu.Unlock()
	s.pruneLegacyKnowledgeBudgetLocked(time.Now().UTC())
	return s.agentKnowledgeBudget[key].count
}

func (s *Service) consumeLegacyKnowledgeBudget(taskID, turnID string) {
	key := legacyKnowledgeBudgetKey(taskID, turnID)
	if key == "" {
		return
	}
	now := time.Now().UTC()
	s.agentKnowledgeBudgetMu.Lock()
	defer s.agentKnowledgeBudgetMu.Unlock()
	if s.agentKnowledgeBudget == nil {
		s.agentKnowledgeBudget = map[string]legacyKnowledgeBudget{}
	}
	s.pruneLegacyKnowledgeBudgetLocked(now)
	value := s.agentKnowledgeBudget[key]
	value.count++
	value.at = now
	s.agentKnowledgeBudget[key] = value
}

func validateAgentKnowledgeProject(call AgentCallContext, projectID string) error {
	projectID = strings.TrimSpace(projectID)
	if projectID != "" && projectID != call.Project.ID {
		return ErrAgentCallForbidden
	}
	return nil
}

// SubmitAgentKnowledgeEntry is the single write entry point for the new Agent
// knowledge API. It resolves the write source from the read source so a Turn
// can never read one store and write the other.
func (s *Service) SubmitAgentKnowledgeEntry(ctx context.Context, claims agentapi.Claims, input AgentKnowledgeEntryInput) (AgentKnowledgeEntryResult, error) {
	call, err := s.AgentCallContext(ctx, claims, true)
	if err != nil {
		return AgentKnowledgeEntryResult{}, err
	}
	if err := validateAgentKnowledgeProject(call, input.ProjectID); err != nil {
		return AgentKnowledgeEntryResult{}, err
	}
	if err := s.agentKnowledgeRouteAllowed(ctx, call); err != nil {
		return AgentKnowledgeEntryResult{}, err
	}
	if !knowledgeEnabled(call.Project, call.Task) {
		return AgentKnowledgeEntryResult{}, ErrAgentCallForbidden
	}
	if strings.TrimSpace(input.Mode) == "" {
		input.Mode = knowledgevault.WriteModeCreate
	}
	mode := strings.ToLower(strings.TrimSpace(input.Mode))
	if mode != knowledgevault.WriteModeCreate && mode != knowledgevault.WriteModeRevise {
		return AgentKnowledgeEntryResult{}, &knowledgevault.WriteError{
			Code: knowledgevault.WriteErrorValidation, Message: "mode must be create or revise",
		}
	}
	scope := strings.ToLower(strings.TrimSpace(input.Scope))
	if scope == "" && mode == knowledgevault.WriteModeCreate {
		scope = knowledgevault.WriteScopeProject
	}
	if scope != "" && scope != knowledgevault.WriteScopeProject && scope != knowledgevault.WriteScopeGlobal {
		return AgentKnowledgeEntryResult{}, &knowledgevault.WriteError{
			Code: knowledgevault.WriteErrorForbiddenScope, Message: "scope must be project or global",
		}
	}
	allowGlobal := s.AgentKnowledgePublishAllowed(ctx, call)

	request := knowledgevault.WriteRequest{
		Mode: mode, Scope: scope, ProjectID: call.Project.ID,
		AllowGlobal: allowGlobal, EntryID: input.EntryID,
		BaseRevision: input.BaseRevision, BaseHash: input.BaseHash,
		Type: input.Type, Title: input.Title, Summary: input.Summary, Body: input.Body,
		Tags: input.Tags, Confidence: input.Confidence, SearchQuery: input.SearchQuery,
		DuplicateAck: input.DuplicateAck, Group: input.Group,
		IdempotencyKey: input.IdempotencyKey,
		SourceTask:     call.Task.ID, SourceTurn: call.Turn.ID, Writer: call.Turn.AgentID,
	}
	// The shared contract runs before source selection: the legacy fallback must
	// not become a way to smuggle in content the vault path rejects.
	if err := knowledgevault.ValidateWriteRequest(request); err != nil {
		return AgentKnowledgeEntryResult{}, err
	}

	if s.vaultReadEnabled(call.Project.ID) {
		if !s.vaultKnowledgeWriteEnabled(call.Project.ID) {
			return AgentKnowledgeEntryResult{}, s.vaultWriteDisabledError()
		}
		result, writeErr := s.knowledgeVault.WriteKnowledge(ctx, request)
		if writeErr != nil {
			return AgentKnowledgeEntryResult{}, writeErr
		}
		s.emit(ctx, call.Task.ID, "knowledge", result.ID, "knowledge_vault_entry_written", map[string]any{
			"mode": result.Mode, "path": result.Path, "revision": result.Revision, "writer": call.Turn.AgentID,
		})
		return AgentKnowledgeEntryResult{Source: "vault", Entry: &result}, nil
	}

	// Legacy source: reuse the existing proposal/validation pipeline instead of
	// writing the database directly.
	if strings.TrimSpace(input.Group) != "" {
		return AgentKnowledgeEntryResult{}, &knowledgevault.WriteError{
			Code:    knowledgevault.WriteErrorValidation,
			Message: "group is only supported when this project writes the vault",
		}
	}
	candidate := KnowledgeCandidate{
		EntryID: input.EntryID, BaseRevision: input.BaseRevision, Scope: scope,
		Type: input.Type, Title: input.Title, Body: input.Body,
	}
	if input.Confidence != nil {
		candidate.Confidence = *input.Confidence
	}
	if scope == knowledgevault.WriteScopeGlobal && !allowGlobal {
		return AgentKnowledgeEntryResult{}, ErrAgentCallForbidden
	}
	if mode == knowledgevault.WriteModeRevise && strings.TrimSpace(candidate.Scope) == "" {
		if existing, lookupErr := s.store.Knowledge(ctx, candidate.EntryID); lookupErr == nil {
			candidate.Scope = existing.Scope
		}
	}
	if value := s.legacyKnowledgeBudgetCount(call.Task.ID, call.Turn.ID); value >= legacyKnowledgeWriteBudget {
		return AgentKnowledgeEntryResult{}, &knowledgevault.WriteError{
			Code:    knowledgevault.WriteErrorBudget,
			Message: "this Task/Turn has reached the knowledge write budget",
		}
	}
	items, proposals, err := s.SubmitAgentKnowledgeProposals(ctx, claims, []KnowledgeCandidate{candidate})
	if err != nil {
		return AgentKnowledgeEntryResult{}, err
	}
	s.consumeLegacyKnowledgeBudget(call.Task.ID, call.Turn.ID)
	return AgentKnowledgeEntryResult{Source: "legacy", Warning: s.vaultFallbackWarning(call.Project.ID), Knowledge: items, Proposals: proposals}, nil
}

// SubmitAgentKnowledgeFeedbackEntry routes stale/wrong/helped to the same
// source the Turn reads. Feedback never rewrites a body in either source.
func (s *Service) SubmitAgentKnowledgeFeedbackEntry(ctx context.Context, claims agentapi.Claims, input AgentKnowledgeFeedbackInput) (AgentKnowledgeFeedbackResult, error) {
	call, err := s.AgentCallContext(ctx, claims, false)
	if err != nil {
		return AgentKnowledgeFeedbackResult{}, err
	}
	if err := validateAgentKnowledgeProject(call, input.ProjectID); err != nil {
		return AgentKnowledgeFeedbackResult{}, err
	}
	if err := s.agentKnowledgeRouteAllowed(ctx, call); err != nil {
		return AgentKnowledgeFeedbackResult{}, err
	}
	if !knowledgeEnabled(call.Project, call.Task) {
		return AgentKnowledgeFeedbackResult{}, ErrAgentCallForbidden
	}
	kind := strings.ToLower(strings.TrimSpace(input.Kind))
	switch kind {
	case knowledgevault.FeedbackHelped, knowledgevault.FeedbackStale, knowledgevault.FeedbackWrong:
	default:
		return AgentKnowledgeFeedbackResult{}, &knowledgevault.WriteError{
			Code: knowledgevault.WriteErrorValidation, Message: "kind must be helped, stale or wrong",
		}
	}
	if strings.TrimSpace(input.EntryID) == "" {
		return AgentKnowledgeFeedbackResult{}, &knowledgevault.WriteError{
			Code: knowledgevault.WriteErrorValidation, Message: "entry_id is required",
		}
	}

	if s.vaultReadEnabled(call.Project.ID) {
		if !s.vaultKnowledgeWriteEnabled(call.Project.ID) {
			return AgentKnowledgeFeedbackResult{}, s.vaultWriteDisabledError()
		}
		request := knowledgevault.FeedbackRequest{
			Scope: strings.ToLower(strings.TrimSpace(input.Scope)), ProjectID: call.Project.ID,
			AllowGlobal:    s.AgentKnowledgePublishAllowed(ctx, call),
			EntryID:        input.EntryID,
			Kind:           kind,
			IdempotencyKey: input.IdempotencyKey,
			SourceTask:     call.Task.ID, SourceTurn: call.Turn.ID, Writer: call.Turn.AgentID,
		}
		result, writeErr := s.knowledgeVault.KnowledgeFeedback(ctx, request)
		if writeErr != nil {
			return AgentKnowledgeFeedbackResult{}, writeErr
		}
		return AgentKnowledgeFeedbackResult{Source: "vault", Feedback: &result}, nil
	}

	item, err := s.SubmitAgentKnowledgeFeedback(ctx, claims, KnowledgeFeedback{EntryID: input.EntryID, Kind: kind})
	if err != nil {
		return AgentKnowledgeFeedbackResult{}, err
	}
	return AgentKnowledgeFeedbackResult{Source: "legacy", Warning: s.vaultFallbackWarning(call.Project.ID), Knowledge: &item}, nil
}

func (s *Service) agentKnowledgeRouteAllowed(ctx context.Context, call AgentCallContext) error {
	if channelContext, err := s.store.ChannelContextForInboxBatch(ctx, call.Turn.InboxBatchID); err == nil && channelRouteMode(channelContext) != "task_route" {
		return ErrAgentCallForbidden
	}
	return nil
}
