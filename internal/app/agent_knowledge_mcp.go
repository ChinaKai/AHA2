package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
)

// ErrKnowledgeMCPEntryNotFound reports a knowledge ID that is not in this
// Turn's readable project/global range. The MCP layer deliberately treats a
// cross-project ID as not found so it cannot be used to probe another
// Project's library.
var ErrKnowledgeMCPEntryNotFound = errors.New("knowledge entry not found")

// ErrKnowledgeMCPSkillNotFound reports a skill ID that is not selected on this
// Task. Skills are never resolved by a client-supplied vault key or path.
var ErrKnowledgeMCPSkillNotFound = errors.New("skill not found")

// AgentKnowledgeMCPAllowed is the single gate for the knowledge domain MCP.
// The runtime injects the server only when this returns true, and every tool
// call re-checks it server-side. A Task with knowledge switched off, the pet
// Task (whose root libraries are outside a Project), and channel-routed Turns
// (which resolve other Projects' knowledge) do not receive this domain.
func (s *Service) AgentKnowledgeMCPAllowed(ctx context.Context, call AgentCallContext) bool {
	if call.Task.ID == petTaskID || call.Task.ProjectID == petProjectID || !knowledgeEnabled(call.Project, call.Task) {
		return false
	}
	routed, err := s.store.HasActiveChannelTaskRoute(ctx, call.Task.ID)
	if err != nil {
		return false
	}
	return !routed
}

func (s *Service) agentKnowledgeMCPReadContext(ctx context.Context, claims agentapi.Claims) (AgentCallContext, error) {
	call, err := s.AgentCallContext(ctx, claims, false)
	if err != nil {
		return AgentCallContext{}, err
	}
	if !s.AgentKnowledgeMCPAllowed(ctx, call) {
		return AgentCallContext{}, ErrAgentCallForbidden
	}
	return call, nil
}

// agentKnowledgeMCPWriteContext enforces the full write boundary in the
// application layer, not just in the MCP HTTP handler: Main only, an explicit
// writable permission snapshot, and a domain that is actually injected.
func (s *Service) agentKnowledgeMCPWriteContext(ctx context.Context, claims agentapi.Claims) (AgentCallContext, error) {
	call, err := s.AgentCallContext(ctx, claims, true)
	if err != nil {
		return AgentCallContext{}, err
	}
	if !s.AgentKnowledgeMCPAllowed(ctx, call) {
		return AgentCallContext{}, ErrAgentCallForbidden
	}
	if !s.AgentWriteAllowed(ctx, call) {
		return AgentCallContext{}, ErrAgentReadOnly
	}
	return call, nil
}

// AgentKnowledgeMCPList returns bounded metadata for the current Project and
// global knowledge only. It never returns another Project's entry, the root
// pet libraries, or a body: the model must call knowledge_get for content.
type AgentKnowledgeMCPListItem struct {
	ID        string    `json:"id"`
	Scope     string    `json:"scope"`
	ProjectID string    `json:"project_id,omitempty"`
	Type      string    `json:"type"`
	Title     string    `json:"title"`
	Revision  int       `json:"revision"`
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updated_at"`
}

type AgentKnowledgeMCPListResult struct {
	Knowledge []AgentKnowledgeMCPListItem `json:"knowledge"`
	Total     int                         `json:"total"`
	Limit     int                         `json:"limit"`
	Offset    int                         `json:"offset"`
}

func (s *Service) AgentKnowledgeMCPList(ctx context.Context, claims agentapi.Claims, query, entryType string, limit, offset int) (AgentKnowledgeMCPListResult, error) {
	if _, err := s.agentKnowledgeMCPReadContext(ctx, claims); err != nil {
		return AgentKnowledgeMCPListResult{}, err
	}
	items, err := s.ApplicableAgentKnowledge(ctx, claims)
	if err != nil {
		return AgentKnowledgeMCPListResult{}, err
	}
	query = strings.ToLower(strings.TrimSpace(query))
	entryType = strings.TrimSpace(entryType)
	filtered := make([]AgentKnowledgeMCPListItem, 0, len(items))
	for _, item := range items {
		if entryType != "" && item.Type != entryType {
			continue
		}
		if query != "" &&
			!strings.Contains(strings.ToLower(item.Title), query) &&
			!strings.Contains(strings.ToLower(item.Body), query) {
			continue
		}
		filtered = append(filtered, AgentKnowledgeMCPListItem{
			ID: item.ID, Scope: item.Scope, ProjectID: item.ProjectID, Type: item.Type,
			Title: item.Title, Revision: item.Revision, Status: string(item.Status),
			UpdatedAt: item.UpdatedAt,
		})
	}
	total := len(filtered)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return AgentKnowledgeMCPListResult{
		Knowledge: filtered[offset:end], Total: total, Limit: limit, Offset: offset,
	}, nil
}

// AgentKnowledgeMCPGet returns one entry from the same readable set as
// knowledge_list. A cross-Project ID is indistinguishable from an unknown ID.
type AgentKnowledgeMCPGetResult struct {
	Source    string                `json:"source"`
	Knowledge domain.KnowledgeEntry `json:"knowledge"`
}

func (s *Service) AgentKnowledgeMCPGet(ctx context.Context, claims agentapi.Claims, entryID string) (AgentKnowledgeMCPGetResult, error) {
	if _, err := s.agentKnowledgeMCPReadContext(ctx, claims); err != nil {
		return AgentKnowledgeMCPGetResult{}, err
	}
	entryID = strings.TrimSpace(entryID)
	if entryID == "" {
		return AgentKnowledgeMCPGetResult{}, ErrKnowledgeMCPEntryNotFound
	}
	items, err := s.ApplicableAgentKnowledge(ctx, claims)
	if err != nil {
		return AgentKnowledgeMCPGetResult{}, err
	}
	for _, item := range items {
		if item.ID != entryID {
			continue
		}
		source := "legacy"
		if vaultEntry, ok, vaultErr := s.KnowledgeVaultEntry(ctx, item.ID); vaultErr != nil {
			return AgentKnowledgeMCPGetResult{}, vaultErr
		} else if ok {
			source = "vault"
			item = vaultEntry
		}
		return AgentKnowledgeMCPGetResult{Source: source, Knowledge: item}, nil
	}
	return AgentKnowledgeMCPGetResult{}, ErrKnowledgeMCPEntryNotFound
}

// AgentKnowledgeMCPGuide is the static guide view. It intentionally omits the
// guide's filesystem path, which is server-internal.
type AgentKnowledgeMCPGuide struct {
	Available bool   `json:"available"`
	Title     string `json:"title,omitempty"`
	Content   string `json:"content,omitempty"`
	Prompt    string `json:"prompt,omitempty"`
}

func (s *Service) AgentKnowledgeMCPGuide(ctx context.Context, claims agentapi.Claims) (AgentKnowledgeMCPGuide, error) {
	if _, err := s.agentKnowledgeMCPReadContext(ctx, claims); err != nil {
		return AgentKnowledgeMCPGuide{}, err
	}
	if s.knowledgeVault == nil {
		return AgentKnowledgeMCPGuide{Available: false}, nil
	}
	guide, err := s.knowledgeVault.Guide()
	if errors.Is(err, knowledgevault.ErrGuideNotFound) {
		return AgentKnowledgeMCPGuide{Available: false}, nil
	}
	if err != nil {
		return AgentKnowledgeMCPGuide{}, err
	}
	return AgentKnowledgeMCPGuide{
		Available: true, Title: guide.Title, Content: guide.Content, Prompt: guide.Prompt,
	}, nil
}

// AgentKnowledgeMCPSkill is the metadata-only skill view. It omits the vault
// package key, internal source path and package files; skill_get adds only the
// instructions the model needs to use the skill.
type AgentKnowledgeMCPSkill struct {
	ID           string    `json:"id"`
	Scope        string    `json:"scope"`
	ProjectID    string    `json:"project_id,omitempty"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	Version      int       `json:"version"`
	Status       string    `json:"status"`
	Enabled      bool      `json:"enabled"`
	AutoAttach   bool      `json:"auto_attach"`
	UpdatedAt    time.Time `json:"updated_at"`
	Instructions string    `json:"instructions,omitempty"`
}

func agentKnowledgeMCPSkillView(item domain.Skill, withInstructions bool) AgentKnowledgeMCPSkill {
	view := AgentKnowledgeMCPSkill{
		ID: item.ID, Scope: item.Scope, ProjectID: item.ProjectID, Name: item.Name,
		Description: item.Description, Version: item.Version, Status: item.Status,
		Enabled: item.Enabled, AutoAttach: item.AutoAttach, UpdatedAt: item.UpdatedAt,
	}
	if withInstructions {
		view.Instructions = item.Instructions
	}
	return view
}

func (s *Service) AgentKnowledgeMCPSkillList(ctx context.Context, claims agentapi.Claims) ([]AgentKnowledgeMCPSkill, error) {
	if _, err := s.agentKnowledgeMCPReadContext(ctx, claims); err != nil {
		return nil, err
	}
	items, _, err := s.SelectedAgentSkills(ctx, claims)
	if err != nil {
		return nil, err
	}
	result := make([]AgentKnowledgeMCPSkill, 0, len(items))
	for _, item := range items {
		result = append(result, agentKnowledgeMCPSkillView(item, false))
	}
	return result, nil
}

func (s *Service) AgentKnowledgeMCPSkillGet(ctx context.Context, claims agentapi.Claims, skillID string) (AgentKnowledgeMCPSkill, error) {
	if _, err := s.agentKnowledgeMCPReadContext(ctx, claims); err != nil {
		return AgentKnowledgeMCPSkill{}, err
	}
	skillID = strings.TrimSpace(skillID)
	items, _, err := s.SelectedAgentSkills(ctx, claims)
	if err != nil {
		return AgentKnowledgeMCPSkill{}, err
	}
	for _, item := range items {
		if item.ID == skillID {
			return agentKnowledgeMCPSkillView(item, true), nil
		}
	}
	return AgentKnowledgeMCPSkill{}, ErrKnowledgeMCPSkillNotFound
}

// AgentKnowledgeMCPSubmitInput is the project-only create/revise contract. It
// deliberately has no project_id: the write always targets the Project proven
// by the Turn capability. Global writes and review/delete actions are not
// exposed here.
type AgentKnowledgeMCPSubmitInput struct {
	Mode         string `json:"mode"`
	Scope        string `json:"scope"`
	EntryID      string `json:"entry_id"`
	BaseRevision int    `json:"base_revision"`
	Type         string `json:"type"`
	Title        string `json:"title"`
	Summary      string `json:"summary"`
	Body         string `json:"body"`
	SearchQuery  string `json:"search_query"`
	DuplicateAck bool   `json:"duplicate_ack"`
}

type AgentKnowledgeMCPWrittenEntry struct {
	Mode     string `json:"mode"`
	ID       string `json:"id"`
	Scope    string `json:"scope"`
	Type     string `json:"type"`
	Title    string `json:"title"`
	Revision int    `json:"revision"`
	Status   string `json:"status"`
}

// AgentKnowledgeMCPWriteResult is the safe MCP shape of a knowledge write. It
// omits the vault filesystem path. Source=legacy always means the returned
// rows/proposals are pending candidates, not verified knowledge.
type AgentKnowledgeMCPWriteResult struct {
	Source    string                         `json:"source"`
	Proposal  bool                           `json:"proposal,omitempty"`
	Warning   string                         `json:"warning,omitempty"`
	Entry     *AgentKnowledgeMCPWrittenEntry `json:"entry,omitempty"`
	Knowledge []domain.KnowledgeEntry        `json:"knowledge,omitempty"`
	Proposals []domain.KnowledgeProposal     `json:"proposals,omitempty"`
}

func (s *Service) SubmitAgentKnowledgeMCPEntry(ctx context.Context, claims agentapi.Claims, input AgentKnowledgeMCPSubmitInput) (AgentKnowledgeMCPWriteResult, error) {
	if _, err := s.agentKnowledgeMCPWriteContext(ctx, claims); err != nil {
		return AgentKnowledgeMCPWriteResult{}, err
	}
	mode := strings.ToLower(strings.TrimSpace(input.Mode))
	if mode == "" {
		mode = knowledgevault.WriteModeCreate
	}
	if mode != knowledgevault.WriteModeCreate && mode != knowledgevault.WriteModeRevise {
		return AgentKnowledgeMCPWriteResult{}, &knowledgevault.WriteError{
			Code: knowledgevault.WriteErrorValidation, Message: "mode must be create or revise",
		}
	}
	scope := strings.ToLower(strings.TrimSpace(input.Scope))
	if scope != "" && scope != knowledgevault.WriteScopeProject {
		return AgentKnowledgeMCPWriteResult{}, &knowledgevault.WriteError{
			Code: knowledgevault.WriteErrorForbiddenScope, Message: "scope must be project",
		}
	}
	if mode == knowledgevault.WriteModeCreate && scope != knowledgevault.WriteScopeProject {
		return AgentKnowledgeMCPWriteResult{}, &knowledgevault.WriteError{
			Code: knowledgevault.WriteErrorValidation, Message: "create requires scope=project",
		}
	}
	if mode == knowledgevault.WriteModeRevise {
		if strings.TrimSpace(input.EntryID) == "" || input.BaseRevision <= 0 {
			return AgentKnowledgeMCPWriteResult{}, &knowledgevault.WriteError{
				Code: knowledgevault.WriteErrorValidation, Message: "revise requires entry_id and a positive base_revision",
			}
		}
	}
	result, err := s.SubmitAgentKnowledgeEntry(ctx, claims, AgentKnowledgeEntryInput{
		Mode: mode, Scope: scope, EntryID: input.EntryID, BaseRevision: input.BaseRevision,
		Type: input.Type, Title: input.Title, Summary: input.Summary, Body: input.Body,
		SearchQuery: input.SearchQuery, DuplicateAck: input.DuplicateAck,
	})
	if err != nil {
		return AgentKnowledgeMCPWriteResult{}, err
	}
	view := AgentKnowledgeMCPWriteResult{
		Source: result.Source, Warning: result.Warning,
		Knowledge: result.Knowledge, Proposals: result.Proposals,
	}
	if result.Source == "legacy" {
		view.Proposal = true
	}
	if result.Entry != nil {
		view.Entry = &AgentKnowledgeMCPWrittenEntry{
			Mode: result.Entry.Mode, ID: result.Entry.ID, Scope: result.Entry.Scope,
			Type: result.Entry.Type, Title: result.Entry.Title,
			Revision: result.Entry.Revision, Status: result.Entry.Status,
		}
	}
	return view, nil
}

type AgentKnowledgeMCPFeedbackResult struct {
	Source    string                     `json:"source"`
	Warning   string                     `json:"warning,omitempty"`
	Feedback  *AgentKnowledgeMCPFeedback `json:"feedback,omitempty"`
	Knowledge *domain.KnowledgeEntry     `json:"knowledge,omitempty"`
}

type AgentKnowledgeMCPFeedback struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Scope    string `json:"scope"`
	Status   string `json:"status"`
	Revision int    `json:"revision"`
	Queued   bool   `json:"queued"`
}

func (s *Service) SubmitAgentKnowledgeMCPFeedback(ctx context.Context, claims agentapi.Claims, entryID, kind string) (AgentKnowledgeMCPFeedbackResult, error) {
	if _, err := s.agentKnowledgeMCPWriteContext(ctx, claims); err != nil {
		return AgentKnowledgeMCPFeedbackResult{}, err
	}
	entryID = strings.TrimSpace(entryID)
	items, err := s.ApplicableAgentKnowledge(ctx, claims)
	if err != nil {
		return AgentKnowledgeMCPFeedbackResult{}, err
	}
	readable := false
	for _, item := range items {
		if item.ID == entryID {
			readable = true
			break
		}
	}
	if !readable {
		// Feedback may only target something this Turn can actually read. This
		// also turns a cross-Project ID into the same not-found result as an
		// unknown ID instead of revealing that the entry exists elsewhere.
		return AgentKnowledgeMCPFeedbackResult{}, ErrKnowledgeMCPEntryNotFound
	}
	result, err := s.SubmitAgentKnowledgeFeedbackEntry(ctx, claims, AgentKnowledgeFeedbackInput{
		EntryID: entryID, Kind: kind,
	})
	if err != nil {
		return AgentKnowledgeMCPFeedbackResult{}, err
	}
	view := AgentKnowledgeMCPFeedbackResult{
		Source: result.Source, Warning: result.Warning, Knowledge: result.Knowledge,
	}
	if result.Feedback != nil {
		view.Feedback = &AgentKnowledgeMCPFeedback{
			Kind: result.Feedback.Kind, ID: result.Feedback.ID, Scope: result.Feedback.Scope,
			Status: result.Feedback.Status, Revision: result.Feedback.Revision, Queued: result.Feedback.Queued,
		}
	}
	return view, nil
}
