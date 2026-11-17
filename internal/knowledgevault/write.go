package knowledgevault

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// The Agent write API is file-first: the Markdown file is the source of truth
// and the index is rebuilt after every successful write. All writes go through
// Manager.WriteKnowledge / Manager.KnowledgeFeedback so the manager write lock
// serializes them with the Web switch and manual rebuilds.
const (
	WriteModeCreate = "create"
	WriteModeRevise = "revise"

	WriteScopeProject = "project"
	WriteScopeGlobal  = "global"

	StatusVerified = "verified"
	StatusStale    = "stale"
	StatusWrong    = "wrong"

	FeedbackHelped = "helped"
	FeedbackStale  = "stale"
	FeedbackWrong  = "wrong"

	// DefaultWriteBudget is the per-Task/Turn limit for entry writes
	// (create + revise). Feedback is deliberately not counted.
	DefaultWriteBudget = 3

	minTitleRunes   = 3
	maxTitleRunes   = 300
	minSummaryRunes = 20
	maxSummaryRunes = 600
	minBodyRunes    = 20
	maxBodyRunes    = 20000
	maxTags         = 10
	maxTagRunes     = 40
	maxGroupRunes   = 100

	agentStateRelative = ".aha/agent-writes.json"
	agentStateVersion  = 1
	maxAgentRecords    = 500
	maxPendingUpdates  = 200
)

// Write request/result error codes. The HTTP layer maps them to status codes
// without parsing error strings.
const (
	WriteErrorDisabled         = "knowledge_vault_write_disabled"
	WriteErrorValidation       = "knowledge_write_invalid"
	WriteErrorForbiddenContent = "knowledge_content_forbidden"
	WriteErrorDuplicate        = "duplicate_knowledge"
	WriteErrorRevisionConflict = "knowledge_revision_conflict"
	WriteErrorNotFound         = "knowledge_entry_not_found"
	WriteErrorForbiddenScope   = "knowledge_scope_forbidden"
	WriteErrorBudget           = "knowledge_write_budget_exceeded"
	WriteErrorProjectUnbound   = "knowledge_project_unbound"
	WriteErrorIndexStale       = "knowledge_vault_index_stale"
	WriteErrorUnavailable      = "knowledge_vault_unavailable"
)

// WriteError carries a stable machine code and, for duplicate rejection, the
// candidates that triggered it.
type WriteError struct {
	Code       string
	Message    string
	Candidates []DuplicateCandidate
}

func (e *WriteError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Message) == "" {
		return e.Code
	}
	return e.Code + ": " + e.Message
}

func writeError(code, format string, args ...any) *WriteError {
	return &WriteError{Code: code, Message: fmt.Sprintf(format, args...)}
}

func errorCode(err error) string {
	var target *WriteError
	if errors.As(err, &target) {
		return target.Code
	}
	return ""
}

// WriteRequest is the server-side form of POST /api/v1/agent/knowledge/entries.
// Scope/ProjectID/SourceTask/SourceTurn/Writer are filled from the Task token;
// the HTTP payload cannot override them.
type WriteRequest struct {
	Mode           string
	Scope          string
	ProjectID      string
	AllowGlobal    bool
	EntryID        string
	BaseRevision   int
	BaseHash       string
	Type           string
	Title          string
	Summary        string
	Body           string
	Tags           []string
	Confidence     *float64
	SearchQuery    string
	DuplicateAck   bool
	Group          string
	IdempotencyKey string
	SourceTask     string
	SourceTurn     string
	Writer         string
	// ForceEntryID is server-only. It lets the Pet/Web submission paths preserve
	// the caller's entry ID so a pending proposal can name the document before
	// it exists. The Agent HTTP payload never fills it: Agent creates keep the
	// server-generated ID.
	ForceEntryID string
	// ParentID/Slug/SortOrder carry the hierarchy the legacy proposal pipeline
	// supported. Nil means "unchanged" on revise and "server default" on create.
	// The Agent HTTP payload never fills these.
	ParentID  *string
	Slug      *string
	SortOrder *int
	// SkipBudget is server-only. Pet and Web submissions are explicit owner
	// actions that may batch many candidates under one long-lived Task/Turn; they
	// keep every validation and idempotency guarantee but do not consume the
	// per-Agent-Turn write budget the Agent API uses to throttle speculation.
	SkipBudget bool
}

// DuplicateCandidate is one existing document that resembles a create request.
type DuplicateCandidate struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	Path       string  `json:"path"`
	Type       string  `json:"type"`
	Status     string  `json:"status"`
	Revision   int     `json:"revision"`
	Similarity float64 `json:"similarity"`
}

// WriteResult is returned for both create and revise. Path is vault-relative
// and is always chosen by the server.
type WriteResult struct {
	Mode             string `json:"mode"`
	ID               string `json:"id"`
	Path             string `json:"path"`
	Scope            string `json:"scope"`
	ProjectID        string `json:"project_id,omitempty"`
	Type             string `json:"type"`
	Title            string `json:"title"`
	Summary          string `json:"summary"`
	Status           string `json:"status"`
	Revision         int    `json:"revision"`
	ContentHash      string `json:"content_hash"`
	Created          string `json:"created,omitempty"`
	Updated          string `json:"updated"`
	SourceTask       string `json:"source_task,omitempty"`
	SourceTurn       string `json:"source_turn,omitempty"`
	Writer           string `json:"writer,omitempty"`
	IdempotentReplay bool   `json:"idempotent_replay,omitempty"`
}

// FeedbackRequest is the server-side form of
// POST /api/v1/agent/knowledge/feedback.
type FeedbackRequest struct {
	Scope          string
	ProjectID      string
	AllowGlobal    bool
	EntryID        string
	Kind           string
	IdempotencyKey string
	SourceTask     string
	SourceTurn     string
	Writer         string
}

// FeedbackResult reports the status a feedback call left the document in.
type FeedbackResult struct {
	Kind             string `json:"kind"`
	ID               string `json:"id"`
	Path             string `json:"path"`
	Scope            string `json:"scope"`
	ProjectID        string `json:"project_id,omitempty"`
	Status           string `json:"status"`
	Revision         int    `json:"revision"`
	ContentHash      string `json:"content_hash"`
	Updated          string `json:"updated"`
	IdempotentReplay bool   `json:"idempotent_replay,omitempty"`
	Queued           bool   `json:"queued"`
}

type agentWriteRecord struct {
	Key          string      `json:"key,omitempty"`
	Mode         string      `json:"mode"`
	Result       WriteResult `json:"result"`
	TaskID       string      `json:"task_id,omitempty"`
	TurnID       string      `json:"turn_id,omitempty"`
	Writer       string      `json:"writer,omitempty"`
	OldRevision  int         `json:"old_revision,omitempty"`
	DiffSummary  string      `json:"diff_summary,omitempty"`
	At           string      `json:"at"`
	SourceTask   string      `json:"source_task,omitempty"`
	SourceTurn   string      `json:"source_turn,omitempty"`
	PreviousHash string      `json:"previous_hash,omitempty"`
}

type agentFeedbackRecord struct {
	Key        string         `json:"key,omitempty"`
	Result     FeedbackResult `json:"result"`
	TaskID     string         `json:"task_id,omitempty"`
	TurnID     string         `json:"turn_id,omitempty"`
	Writer     string         `json:"writer,omitempty"`
	At         string         `json:"at"`
	SourceTask string         `json:"source_task,omitempty"`
	SourceTurn string         `json:"source_turn,omitempty"`
}

type agentPendingUpdate struct {
	EntryID      string `json:"entry_id"`
	Path         string `json:"path"`
	Kind         string `json:"kind"`
	BaseRevision int    `json:"base_revision"`
	SourceTask   string `json:"source_task,omitempty"`
	SourceTurn   string `json:"source_turn,omitempty"`
	Writer       string `json:"writer,omitempty"`
	At           string `json:"at"`
}

type agentWriteState struct {
	Version  int                   `json:"version"`
	Entries  []agentWriteRecord    `json:"entries,omitempty"`
	Feedback []agentFeedbackRecord `json:"feedback,omitempty"`
	Turns    map[string]int        `json:"turns,omitempty"`
	Pending  []agentPendingUpdate  `json:"pending_updates,omitempty"`
}

var forbiddenKnowledgePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)BEGIN [A-Z0-9 ]*PRIVATE KEY`),
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}\b`),
	regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`),
	regexp.MustCompile(`\bsk-[A-Za-z0-9]{20,}\b`),
	regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}\b`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.`),
	regexp.MustCompile(`(?i)\b(password|passwd|secret|api[_-]?key|access[_-]?token)\s*[:=]\s*["']?[^\s"']{8,}`),
}

// WriteKnowledge creates or revises one vault document. It is intentionally
// strict: a request either applies completely (file + index + audit state) or
// leaves the previous file and index in place.
func (m *Manager) WriteKnowledge(ctx context.Context, request WriteRequest) (WriteResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.writePreconditionsLocked(); err != nil {
		return WriteResult{}, err
	}
	request = normalizeWriteRequest(request)
	if err := validateWriteRequest(request); err != nil {
		return WriteResult{}, err
	}
	// Idempotent replays must be answered before duplicate detection: the first
	// create is now itself a highly similar document.
	state, err := m.loadAgentStateLocked()
	if err != nil {
		return WriteResult{}, err
	}
	if request.IdempotencyKey != "" {
		if record, ok := findWriteRecord(state, request.SourceTask, request.IdempotencyKey); ok {
			record.Result.IdempotentReplay = true
			return record.Result, nil
		}
	}
	if err := checkWriteBudget(state, request, m.writeBudgetOrDefault()); err != nil {
		return WriteResult{}, err
	}

	now := time.Now().UTC()
	var result WriteResult
	var previous, current []byte
	var target string
	switch request.Mode {
	case WriteModeCreate:
		result, current, target, err = m.createLocked(ctx, request, now)
	case WriteModeRevise:
		result, previous, current, target, err = m.reviseLocked(ctx, request, now)
	default:
		err = writeError(WriteErrorValidation, "mode must be create or revise")
	}
	if err != nil {
		return WriteResult{}, err
	}

	rollback := func(cause error) (WriteResult, error) {
		restoreErr := m.restoreWriteLocked(target, previous, request.Mode == WriteModeCreate)
		if restoreErr != nil {
			return WriteResult{}, fmt.Errorf("%w (restore failed: %v)", cause, restoreErr)
		}
		return WriteResult{}, cause
	}
	if m.writeHook != nil {
		if hookErr := m.writeHook(); hookErr != nil {
			return rollback(fmt.Errorf("knowledge write hook failed: %w", hookErr))
		}
	}
	if _, err := m.rebuildLocked(ctx); err != nil {
		return rollback(fmt.Errorf("rebuild knowledge index after write: %w", err))
	}

	record := agentWriteRecord{
		Key: request.IdempotencyKey, Mode: request.Mode, Result: result,
		TaskID: request.SourceTask, TurnID: request.SourceTurn, Writer: request.Writer,
		At: now.Format(time.RFC3339), SourceTask: request.SourceTask, SourceTurn: request.SourceTurn,
		DiffSummary: diffSummary(previous, current),
	}
	if request.Mode == WriteModeRevise {
		record.OldRevision = request.BaseRevision
		record.PreviousHash = hashBytes(previous)
	}
	state.Entries = append(state.Entries, record)
	state.Entries = capWriteRecords(state.Entries, maxAgentRecords)
	if request.SourceTask != "" || request.SourceTurn != "" {
		if request.SkipBudget {
			// An owner-initiated write must not consume the Agent turn's budget.
		} else {
			state.Turns[turnBudgetKey(request)]++
		}
	}
	if err := m.saveAgentStateLocked(state); err != nil {
		// The document and index are already durable. Report the audit failure
		// instead of pretending the write did not happen.
		return result, fmt.Errorf("record agent knowledge write: %w", err)
	}
	m.requestAutoCommit()
	return result, nil
}

// KnowledgeFeedback records helped/stale/wrong for one document. stale and
// wrong change only the frontmatter status and enqueue a pending update; the
// body is never modified and the revision is not bumped.
func (m *Manager) KnowledgeFeedback(ctx context.Context, request FeedbackRequest) (FeedbackResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.writePreconditionsLocked(); err != nil {
		return FeedbackResult{}, err
	}
	request.Scope = strings.ToLower(strings.TrimSpace(request.Scope))
	if request.Scope != "" && request.Scope != WriteScopeProject && request.Scope != WriteScopeGlobal {
		return FeedbackResult{}, writeError(WriteErrorForbiddenScope, "scope must be project or global")
	}
	request.EntryID = strings.TrimSpace(request.EntryID)
	if request.EntryID == "" {
		return FeedbackResult{}, writeError(WriteErrorValidation, "entry_id is required")
	}
	kind := strings.ToLower(strings.TrimSpace(request.Kind))
	switch kind {
	case FeedbackHelped, FeedbackStale, FeedbackWrong:
	default:
		return FeedbackResult{}, writeError(WriteErrorValidation, "kind must be helped, stale or wrong")
	}

	state, err := m.loadAgentStateLocked()
	if err != nil {
		return FeedbackResult{}, err
	}
	if request.IdempotencyKey != "" {
		if record, ok := findFeedbackRecord(state, request.SourceTask, request.IdempotencyKey); ok {
			record.Result.IdempotentReplay = true
			return record.Result, nil
		}
	}

	entry, raw, currentRevision, err := m.lookupEntryForWriteLocked(ctx, request.EntryID, request.Scope, request.ProjectID, request.AllowGlobal)
	if err != nil {
		return FeedbackResult{}, err
	}
	now := time.Now().UTC()
	result := FeedbackResult{
		Kind: kind, ID: entry.ID, Path: entry.Path, Scope: entry.Scope, ProjectID: entry.ProjectID,
		Status: entry.Status, Revision: currentRevision, ContentHash: hashBytes(raw),
		Updated: entry.Updated,
	}

	if kind != FeedbackHelped {
		document := ParseDocument(string(raw))
		frontmatter := document.Frontmatter
		frontmatter.Status = kind
		frontmatter.Updated = now.Format(time.RFC3339)
		body := strings.TrimLeft(strings.ReplaceAll(StripGeneratedIndex(document.Body), "\r\n", "\n"), "\n")
		rendered, renderErr := renderDocument(frontmatter, body)
		if renderErr != nil {
			return FeedbackResult{}, fmt.Errorf("render knowledge feedback: %w", renderErr)
		}
		if err := writeVaultFileIfChanged(m.config.VaultPath, filepath.Join(m.config.VaultPath, filepath.FromSlash(entry.Path)), []byte(rendered), 0o644); err != nil {
			return FeedbackResult{}, fmt.Errorf("write knowledge feedback: %w", err)
		}
		rollback := func(cause error) (FeedbackResult, error) {
			if restoreErr := m.restoreWriteLocked(entry.Path, raw, false); restoreErr != nil {
				return FeedbackResult{}, fmt.Errorf("%w (restore failed: %v)", cause, restoreErr)
			}
			return FeedbackResult{}, cause
		}
		if m.writeHook != nil {
			if hookErr := m.writeHook(); hookErr != nil {
				return rollback(fmt.Errorf("knowledge feedback hook failed: %w", hookErr))
			}
		}
		if _, err := m.rebuildLocked(ctx); err != nil {
			return rollback(fmt.Errorf("rebuild knowledge index after feedback: %w", err))
		}
		result.Status = kind
		result.Updated = frontmatter.Updated
		result.ContentHash = hashBytes([]byte(rendered))
		result.Queued = true
		state.Pending = append(state.Pending, agentPendingUpdate{
			EntryID: entry.ID, Path: entry.Path, Kind: kind, BaseRevision: currentRevision,
			SourceTask: request.SourceTask, SourceTurn: request.SourceTurn, Writer: request.Writer,
			At: now.Format(time.RFC3339),
		})
		state.Pending = capPendingUpdates(state.Pending, maxPendingUpdates)
	}

	state.Feedback = append(state.Feedback, agentFeedbackRecord{
		Key: request.IdempotencyKey, Result: result,
		TaskID: request.SourceTask, TurnID: request.SourceTurn, Writer: request.Writer,
		At: now.Format(time.RFC3339), SourceTask: request.SourceTask, SourceTurn: request.SourceTurn,
	})
	state.Feedback = capFeedbackRecords(state.Feedback, maxAgentRecords)
	if err := m.saveAgentStateLocked(state); err != nil {
		return result, fmt.Errorf("record agent knowledge feedback: %w", err)
	}
	m.requestAutoCommit()
	return result, nil
}

func (m *Manager) writePreconditionsLocked() error {
	if !m.enabled || m.reader == nil {
		return ErrDisabled
	}
	if !m.runtimeWrite {
		return writeError(WriteErrorDisabled, "the Agent vault write gate is off; legacy knowledge remains the write target")
	}
	if !VaultInitialized(m.config.VaultPath) {
		return ErrVaultNotInitialized
	}
	if !m.reader.IndexReady() {
		return writeError(WriteErrorUnavailable, "the knowledge index is not ready; rebuild it before writing")
	}
	return nil
}

func normalizeWriteRequest(request WriteRequest) WriteRequest {
	request.Mode = strings.ToLower(strings.TrimSpace(request.Mode))
	if request.Mode == "" {
		request.Mode = WriteModeCreate
	}
	request.Scope = strings.ToLower(strings.TrimSpace(request.Scope))
	if request.Scope == "" && request.Mode == WriteModeCreate {
		request.Scope = WriteScopeProject
	}
	request.ProjectID = strings.TrimSpace(request.ProjectID)
	request.EntryID = strings.TrimSpace(request.EntryID)
	request.Type = strings.ToLower(strings.TrimSpace(request.Type))
	request.Title = strings.TrimSpace(request.Title)
	request.Summary = strings.TrimSpace(request.Summary)
	request.Body = strings.TrimSpace(strings.ReplaceAll(request.Body, "\r\n", "\n"))
	request.SearchQuery = strings.TrimSpace(request.SearchQuery)
	request.Group = strings.TrimSpace(request.Group)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	request.SourceTask = strings.TrimSpace(request.SourceTask)
	request.SourceTurn = strings.TrimSpace(request.SourceTurn)
	request.Writer = strings.TrimSpace(request.Writer)
	request.ForceEntryID = strings.TrimSpace(request.ForceEntryID)
	if request.ParentID != nil {
		value := strings.TrimSpace(*request.ParentID)
		request.ParentID = &value
	}
	if request.Slug != nil {
		value := strings.TrimSpace(*request.Slug)
		request.Slug = &value
	}
	request.Tags = normalizeTags(request.Tags)
	return request
}

// ValidateWriteRequest applies the shared write contract without touching the
// vault. The HTTP layer runs it before choosing a source so the legacy
// fallback cannot accept content the vault path would reject.
func ValidateWriteRequest(request WriteRequest) error {
	return validateWriteRequest(normalizeWriteRequest(request))
}

func validateWriteRequest(request WriteRequest) error {
	switch request.Scope {
	case "":
		// Revise/feedback may omit scope and let the server infer it from the
		// entry, which is safer than trusting a client-supplied scope.
		if request.Mode != WriteModeRevise {
			return writeError(WriteErrorForbiddenScope, "scope must be project or global")
		}
	case WriteScopeGlobal:
		if !request.AllowGlobal {
			return writeError(WriteErrorForbiddenScope, "global knowledge is not writable in this Turn")
		}
	case WriteScopeProject:
		if request.ProjectID == "" {
			return writeError(WriteErrorValidation, "project scope requires a project from the Task token")
		}
	default:
		return writeError(WriteErrorForbiddenScope, "scope must be project or global")
	}
	if request.LenTitle() < minTitleRunes || request.LenTitle() > maxTitleRunes {
		return writeError(WriteErrorValidation, "title must be %d-%d characters", minTitleRunes, maxTitleRunes)
	}
	if strings.ContainsAny(request.Title, "\r\n") {
		return writeError(WriteErrorValidation, "title must be a single line")
	}
	if request.LenSummary() < minSummaryRunes || request.LenSummary() > maxSummaryRunes {
		return writeError(WriteErrorValidation, "summary must be %d-%d characters", minSummaryRunes, maxSummaryRunes)
	}
	if strings.ContainsAny(request.Summary, "\r\n") {
		return writeError(WriteErrorValidation, "summary must be a single line")
	}
	if !utf8.ValidString(request.Body) || strings.ContainsRune(request.Body, 0) {
		return writeError(WriteErrorValidation, "body must be valid UTF-8 without NUL bytes")
	}
	bodyLength := len([]rune(request.Body))
	if bodyLength < minBodyRunes || bodyLength > maxBodyRunes {
		return writeError(WriteErrorValidation, "body must be %d-%d characters", minBodyRunes, maxBodyRunes)
	}
	if request.Type != "" && !canonicalTypes[request.Type] {
		return writeError(WriteErrorValidation, "type must be one of navigation, decision, practice, diagnostic, reference, architecture")
	}
	if request.Mode == WriteModeCreate && request.Type == "" {
		return writeError(WriteErrorValidation, "type is required when creating")
	}
	if request.Confidence != nil && (*request.Confidence < 0 || *request.Confidence > 1) {
		return writeError(WriteErrorValidation, "confidence must be between 0 and 1")
	}
	if len(request.Tags) > maxTags {
		return writeError(WriteErrorValidation, "at most %d tags are allowed", maxTags)
	}
	if len([]rune(request.Group)) > maxGroupRunes {
		return writeError(WriteErrorValidation, "group name is too long")
	}
	for _, tag := range request.Tags {
		if len([]rune(tag)) > maxTagRunes {
			return writeError(WriteErrorValidation, "tag %q is too long", tag)
		}
	}
	switch request.Mode {
	case WriteModeCreate:
		if request.EntryID != "" || request.BaseRevision != 0 {
			return writeError(WriteErrorValidation, "create does not accept entry_id or base_revision")
		}
		if request.ForceEntryID != "" && !validEntryID(request.ForceEntryID) {
			return writeError(WriteErrorValidation, "force_entry_id is not a valid knowledge ID")
		}
		if request.SearchQuery == "" && !request.DuplicateAck {
			return writeError(WriteErrorValidation, "create requires search_query or duplicate_ack")
		}
		if request.SearchQuery != "" && len([]rune(request.SearchQuery)) > 200 {
			return writeError(WriteErrorValidation, "search_query is too long")
		}
	case WriteModeRevise:
		if request.EntryID == "" {
			return writeError(WriteErrorValidation, "revise requires entry_id")
		}
		if request.BaseRevision <= 0 {
			return writeError(WriteErrorValidation, "revise requires a positive base_revision")
		}
		if request.ForceEntryID != "" && request.ForceEntryID != request.EntryID {
			return writeError(WriteErrorValidation, "force_entry_id only applies to create")
		}
	default:
		return writeError(WriteErrorValidation, "mode must be create or revise")
	}
	if request.ParentID != nil && !validParentID(*request.ParentID) {
		return writeError(WriteErrorValidation, "parent_id is not a valid knowledge ID")
	}
	if request.Slug != nil && !validExplicitSlug(*request.Slug) {
		return writeError(WriteErrorValidation, "slug is not a valid knowledge slug")
	}
	if request.Group != "" && !strings.EqualFold(request.Group, "inbox") && !validGroupName(request.Group) {
		return writeError(WriteErrorValidation, "group must be an existing topic group name without path separators")
	}
	for _, value := range []struct {
		name  string
		value string
	}{{"title", request.Title}, {"summary", request.Summary}, {"body", request.Body}} {
		if forbiddenContent(value.value) {
			return writeError(WriteErrorForbiddenContent, "%s contains credentials or secrets that must not be stored in knowledge", value.name)
		}
	}
	return nil
}

func (r WriteRequest) LenTitle() int   { return len([]rune(r.Title)) }
func (r WriteRequest) LenSummary() int { return len([]rune(r.Summary)) }

func normalizeTags(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		value = strings.Join(strings.Fields(value), "-")
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func validEntryID(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) < 3 || len(value) > 120 {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

func validParentID(value string) bool {
	value = strings.TrimSpace(value)
	return value == "" || validEntryID(value)
}

func validExplicitSlug(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || value == "." || value == ".." || len([]rune(value)) > 80 {
		return false
	}
	if strings.ContainsAny(value, `/\`) || strings.ContainsRune(value, 0) {
		return false
	}
	if strings.ToLower(value) == "index" || isWindowsReserved(value) {
		return false
	}
	return value == strings.TrimSpace(value)
}

func validGroupName(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || value == "." || value == ".." {
		return false
	}
	if strings.ContainsAny(value, `/\`) || strings.ContainsRune(value, 0) {
		return false
	}
	lower := strings.ToLower(value)
	if reservedVaultNames[lower] || lower == "index" || isWindowsReserved(value) {
		return false
	}
	if strings.HasSuffix(value, ".") || strings.HasSuffix(value, " ") {
		return false
	}
	return true
}

func forbiddenContent(value string) bool {
	for _, pattern := range forbiddenKnowledgePatterns {
		if pattern.MatchString(value) {
			return true
		}
	}
	return false
}

func (m *Manager) writeBudgetOrDefault() int {
	if m.writeBudget > 0 {
		return m.writeBudget
	}
	return DefaultWriteBudget
}

func (m *Manager) createLocked(ctx context.Context, request WriteRequest, now time.Time) (WriteResult, []byte, string, error) {
	manifest := m.reader.Manifest()
	projectSlug := ""
	target := ""
	if request.Scope == WriteScopeProject {
		projectSlug = strings.TrimSpace(manifest.Projects[request.ProjectID])
		if projectSlug == "" {
			return WriteResult{}, nil, "", writeError(WriteErrorProjectUnbound, "project %s is not bound to a vault library", request.ProjectID)
		}
		group, err := m.chooseProjectGroupLocked(projectSlug, request.Group)
		if err != nil {
			return WriteResult{}, nil, "", err
		}
		target = path.Join("projects", projectSlug, group)
	} else {
		target = path.Join("global", "inbox")
	}
	if err := m.ensureWritableDirectoryLocked(target); err != nil {
		return WriteResult{}, nil, "", err
	}
	relative, err := m.allocateDocumentPathLocked(target, request.Title)
	if err != nil {
		return WriteResult{}, nil, "", err
	}
	if err := validateVaultWritePath(relative, request.Scope, projectSlug); err != nil {
		return WriteResult{}, nil, "", err
	}
	// Duplicate detection runs after the path policy: a request for a
	// nonexistent group must be rejected for the structural problem first.
	if !request.DuplicateAck {
		candidates, err := m.duplicateCandidatesLocked(ctx, request)
		if err != nil {
			return WriteResult{}, nil, "", err
		}
		if high := highSimilarityCandidates(candidates); len(high) > 0 {
			return WriteResult{}, nil, "", &WriteError{
				Code:       WriteErrorDuplicate,
				Message:    "a highly similar document already exists; revise it or acknowledge the duplicate explicitly",
				Candidates: high,
			}
		}
	}

	frontmatter := Frontmatter{
		ID: "knowledge_" + randomHex(8), Title: request.Title, Slug: Slugify(request.Title),
		Scope: request.Scope, ProjectID: request.ProjectID, Type: request.Type,
		Tags: append([]string{}, request.Tags...), Status: StatusVerified,
		Summary: request.Summary, Revision: 1,
		Created: now.Format(time.RFC3339), Updated: now.Format(time.RFC3339),
		SourceTask: request.SourceTask, SourceTurn: request.SourceTurn,
		Confidence: request.Confidence,
	}
	if request.ForceEntryID != "" {
		frontmatter.ID = request.ForceEntryID
	}
	if request.Slug != nil && *request.Slug != "" {
		frontmatter.Slug = *request.Slug
	}
	if request.ParentID != nil {
		frontmatter.Parent = *request.ParentID
	}
	if request.SortOrder != nil {
		sortOrder := *request.SortOrder
		frontmatter.SortOrder = &sortOrder
	}
	if request.ForceEntryID != "" {
		// A caller-supplied ID must name a new document. Rewriting an existing
		// ID through create would fork the index's identity, so refuse instead.
		if _, lookupErr := m.reader.entryByID(ctx, request.ForceEntryID); lookupErr == nil {
			return WriteResult{}, nil, "", &WriteError{
				Code:    WriteErrorDuplicate,
				Message: "knowledge entry " + request.ForceEntryID + " already exists in the vault",
			}
		} else if !errors.Is(lookupErr, sql.ErrNoRows) {
			return WriteResult{}, nil, "", fmt.Errorf("check knowledge entry id: %w", lookupErr)
		}
	}
	content, err := renderDocument(frontmatter, request.Body)
	if err != nil {
		return WriteResult{}, nil, "", fmt.Errorf("render knowledge document: %w", err)
	}
	full := filepath.Join(m.config.VaultPath, filepath.FromSlash(relative))
	if err := writeVaultFileIfChanged(m.config.VaultPath, full, []byte(content), 0o644); err != nil {
		return WriteResult{}, nil, "", fmt.Errorf("write knowledge document: %w", err)
	}
	result := WriteResult{
		Mode: request.Mode, ID: frontmatter.ID, Path: relative, Scope: request.Scope,
		ProjectID: request.ProjectID, Type: frontmatter.Type, Title: frontmatter.Title,
		Summary: frontmatter.Summary, Status: frontmatter.Status, Revision: frontmatter.Revision,
		ContentHash: hashBytes([]byte(content)), Created: frontmatter.Created, Updated: frontmatter.Updated,
		SourceTask: request.SourceTask, SourceTurn: request.SourceTurn, Writer: request.Writer,
	}
	return result, []byte(content), relative, nil
}

func (m *Manager) reviseLocked(ctx context.Context, request WriteRequest, now time.Time) (WriteResult, []byte, []byte, string, error) {
	entry, raw, currentRevision, err := m.lookupEntryForWriteLocked(ctx, request.EntryID, request.Scope, request.ProjectID, request.AllowGlobal)
	if err != nil {
		return WriteResult{}, nil, nil, "", err
	}
	if request.Type == "" {
		request.Type = entry.Type
	}
	if request.BaseRevision != currentRevision {
		return WriteResult{}, nil, nil, "", &WriteError{
			Code:    WriteErrorRevisionConflict,
			Message: fmt.Sprintf("base_revision %d does not match current revision %d; re-read the entry and retry", request.BaseRevision, currentRevision),
		}
	}
	currentHash := hashBytes(raw)
	if request.BaseHash != "" && request.BaseHash != currentHash {
		return WriteResult{}, nil, nil, "", &WriteError{
			Code:    WriteErrorRevisionConflict,
			Message: "base_hash does not match the current file; re-read the entry and retry",
		}
	}
	if entry.ContentHash != "" && entry.ContentHash != currentHash {
		return WriteResult{}, nil, nil, "", &WriteError{
			Code:    WriteErrorIndexStale,
			Message: "the index hash differs from the file on disk; rebuild the index before revising",
		}
	}
	if err := validateVaultWritePath(entry.Path, entry.Scope, strings.TrimSpace(m.reader.Manifest().Projects[entry.ProjectID])); err != nil {
		return WriteResult{}, nil, nil, "", err
	}
	document := ParseDocument(string(raw))
	frontmatter := document.Frontmatter
	if strings.TrimSpace(frontmatter.ID) == "" {
		frontmatter.ID = entry.ID
	}
	frontmatter.Title = request.Title
	frontmatter.Summary = request.Summary
	frontmatter.Type = request.Type
	frontmatter.Tags = append([]string{}, request.Tags...)
	frontmatter.Status = StatusVerified
	frontmatter.Revision = currentRevision + 1
	frontmatter.Updated = now.Format(time.RFC3339)
	frontmatter.SourceTask = request.SourceTask
	frontmatter.SourceTurn = request.SourceTurn
	if request.Confidence != nil {
		frontmatter.Confidence = request.Confidence
	}
	if request.ParentID != nil {
		frontmatter.Parent = *request.ParentID
	}
	if request.Slug != nil && *request.Slug != "" {
		frontmatter.Slug = *request.Slug
	}
	if request.SortOrder != nil {
		sortOrder := *request.SortOrder
		frontmatter.SortOrder = &sortOrder
	}
	if frontmatter.Scope == "" {
		frontmatter.Scope = entry.Scope
	}
	if frontmatter.ProjectID == "" {
		frontmatter.ProjectID = entry.ProjectID
	}
	content, err := renderDocument(frontmatter, request.Body)
	if err != nil {
		return WriteResult{}, nil, nil, "", fmt.Errorf("render knowledge revision: %w", err)
	}
	full := filepath.Join(m.config.VaultPath, filepath.FromSlash(entry.Path))
	if err := writeVaultFileIfChanged(m.config.VaultPath, full, []byte(content), 0o644); err != nil {
		return WriteResult{}, nil, nil, "", fmt.Errorf("write knowledge revision: %w", err)
	}
	result := WriteResult{
		Mode: request.Mode, ID: frontmatter.ID, Path: entry.Path, Scope: entry.Scope,
		ProjectID: entry.ProjectID, Type: frontmatter.Type, Title: frontmatter.Title,
		Summary: frontmatter.Summary, Status: frontmatter.Status, Revision: frontmatter.Revision,
		ContentHash: hashBytes([]byte(content)), Created: frontmatter.Created, Updated: frontmatter.Updated,
		SourceTask: request.SourceTask, SourceTurn: request.SourceTurn, Writer: request.Writer,
	}
	return result, raw, []byte(content), entry.Path, nil
}

// lookupEntryForWriteLocked resolves an indexed entry, validates that it
// belongs to the token-resolved scope/project, and returns the raw file bytes.
// An empty scope means "infer from the entry", which keeps revise/feedback
// working without trusting a client-supplied scope.
func (m *Manager) lookupEntryForWriteLocked(ctx context.Context, id, scope, projectID string, allowGlobal bool) (IndexEntry, []byte, int, error) {
	entry, err := m.reader.entryByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return IndexEntry{}, nil, 0, writeError(WriteErrorNotFound, "knowledge entry %s was not found in the vault", id)
	}
	if err != nil {
		return IndexEntry{}, nil, 0, fmt.Errorf("look up knowledge entry: %w", err)
	}
	if scope != "" && entry.Scope != scope {
		return IndexEntry{}, nil, 0, writeError(WriteErrorForbiddenScope, "entry scope does not match the requested scope")
	}
	if entry.Scope == WriteScopeProject && (projectID == "" || entry.ProjectID != projectID) {
		return IndexEntry{}, nil, 0, writeError(WriteErrorForbiddenScope, "entry belongs to another project")
	}
	if entry.Scope == WriteScopeGlobal && !allowGlobal {
		return IndexEntry{}, nil, 0, writeError(WriteErrorForbiddenScope, "global knowledge is not writable in this Turn")
	}
	if err := validateVaultWritePath(entry.Path, entry.Scope, strings.TrimSpace(m.reader.Manifest().Projects[entry.ProjectID])); err != nil {
		return IndexEntry{}, nil, 0, err
	}
	full := filepath.Join(m.config.VaultPath, filepath.FromSlash(entry.Path))
	if !IsInsideRoot(m.config.VaultPath, full) {
		return IndexEntry{}, nil, 0, writeError(WriteErrorForbiddenScope, "entry path resolves outside the vault")
	}
	raw, readErr := os.ReadFile(full)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return IndexEntry{}, nil, 0, writeError(WriteErrorNotFound, "knowledge entry file is missing")
		}
		return IndexEntry{}, nil, 0, fmt.Errorf("read knowledge entry: %w", readErr)
	}
	document := ParseDocument(string(raw))
	revision := document.Frontmatter.Revision
	if revision <= 0 {
		revision = 1
	}
	return entry, raw, revision, nil
}

func (m *Manager) chooseProjectGroupLocked(projectSlug, requested string) (string, error) {
	root := filepath.Join(m.config.VaultPath, "projects", projectSlug)
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", writeError(WriteErrorProjectUnbound, "project folder projects/%s is missing", projectSlug)
	}
	groups, err := existingTopicGroups(root)
	if err != nil {
		return "", err
	}
	if requested == "" || strings.EqualFold(requested, "inbox") {
		return "inbox", nil
	}
	for _, group := range groups {
		if group == requested {
			return requested, nil
		}
	}
	return "", writeError(WriteErrorValidation, "group %q is not an existing topic group in this project", requested)
}

func existingTopicGroups(projectRoot string) ([]string, error) {
	entries, err := os.ReadDir(projectRoot)
	if err != nil {
		return nil, err
	}
	groups := []string{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		switch strings.ToLower(name) {
		case "inbox", "skills", "archive":
			continue
		}
		indexPath := filepath.Join(projectRoot, name, "index.md")
		if info, statErr := os.Stat(indexPath); statErr == nil && !info.IsDir() {
			groups = append(groups, name)
		}
	}
	sort.Strings(groups)
	return groups, nil
}

func (m *Manager) ensureWritableDirectoryLocked(relative string) error {
	if err := ValidateRelativePath(relative); err != nil {
		return writeError(WriteErrorValidation, "invalid target directory: %v", err)
	}
	full := filepath.Join(m.config.VaultPath, filepath.FromSlash(relative))
	if info, err := os.Stat(full); err == nil {
		if !info.IsDir() {
			return writeError(WriteErrorValidation, "target %s is not a directory", relative)
		}
		if !IsInsideRoot(m.config.VaultPath, full) {
			return writeError(WriteErrorForbiddenScope, "target directory resolves outside the vault")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	// Agents may create the special inbox folder, but never a project root or a
	// topic group. Every other missing directory is rejected.
	if strings.ToLower(path.Base(relative)) != "inbox" {
		return writeError(WriteErrorValidation, "target group %s does not exist; agents may only use an existing topic group or inbox", relative)
	}
	if err := os.MkdirAll(full, 0o755); err != nil {
		return fmt.Errorf("create inbox directory: %w", err)
	}
	if !IsInsideRoot(m.config.VaultPath, full) {
		return writeError(WriteErrorForbiddenScope, "target directory resolves outside the vault")
	}
	return nil
}

func (m *Manager) allocateDocumentPathLocked(directory, title string) (string, error) {
	name := SafeSegment(title, "document")
	if name == "" {
		name = "document"
	}
	stem := strings.TrimSuffix(name, ".md")
	candidate := path.Join(directory, stem+".md")
	for attempt := 0; attempt < 100; attempt++ {
		if attempt == 1 {
			candidate = path.Join(directory, stem+"-"+shortID(title)+".md")
		} else if attempt > 1 {
			candidate = path.Join(directory, fmt.Sprintf("%s-%s-%d.md", stem, shortID(title), attempt))
		}
		full := filepath.Join(m.config.VaultPath, filepath.FromSlash(candidate))
		if _, err := os.Lstat(full); os.IsNotExist(err) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", writeError(WriteErrorValidation, "could not allocate a unique file name for %q", title)
}

func validateVaultWritePath(relative, scope, projectSlug string) error {
	if err := ValidateRelativePath(relative); err != nil {
		return writeError(WriteErrorValidation, "invalid vault path: %v", err)
	}
	segments := strings.Split(relative, "/")
	for index, segment := range segments {
		lower := strings.ToLower(segment)
		switch lower {
		case "skills", "archive", "orphans", ".trash", ".git", ".aha", ".obsidian":
			return writeError(WriteErrorValidation, "path segment %q is reserved", segment)
		}
		if index == len(segments)-1 && lower == "index.md" {
			return writeError(WriteErrorValidation, "agents may not write index.md")
		}
	}
	switch scope {
	case WriteScopeProject:
		if len(segments) < 3 || segments[0] != "projects" {
			return writeError(WriteErrorValidation, "project knowledge must live under projects/<slug>/")
		}
		if projectSlug != "" && segments[1] != projectSlug {
			return writeError(WriteErrorForbiddenScope, "project path belongs to a different project")
		}
	case WriteScopeGlobal:
		if len(segments) < 2 || segments[0] != "global" {
			return writeError(WriteErrorValidation, "global knowledge must live under global/")
		}
	default:
		return writeError(WriteErrorForbiddenScope, "scope must be project or global")
	}
	return nil
}

func (m *Manager) duplicateCandidatesLocked(ctx context.Context, request WriteRequest) ([]DuplicateCandidate, error) {
	// Global rows are indexed with an empty project_id, so filtering a global
	// create by the token project would return no rows and silently skip the
	// duplicate gate.
	projectID := request.ProjectID
	if request.Scope == WriteScopeGlobal {
		projectID = ""
	}
	entries, err := m.reader.index.Entries(ctx, request.Scope, projectID)
	if err != nil {
		return nil, err
	}
	query := request.SearchQuery
	if query == "" {
		query = request.Title
	}
	query = strings.ToLower(strings.TrimSpace(query))
	candidates := []DuplicateCandidate{}
	for _, entry := range entries {
		if entry.IsIndex || entry.Archive || isReservedVaultPath(entry.Path) {
			continue
		}
		if entry.Scope != request.Scope || (request.Scope == WriteScopeProject && entry.ProjectID != request.ProjectID) {
			continue
		}
		score := documentSimilarity(request.Title, request.Summary, request.Body, entry.Title, entry.Summary, entry.Body)
		if query != "" {
			haystack := strings.ToLower(entry.Title + "\n" + entry.Summary + "\n" + entry.Body)
			if strings.Contains(haystack, query) {
				score += 0.1
			}
		}
		if score < 0.35 {
			continue
		}
		if score > 1 {
			score = 1
		}
		candidates = append(candidates, DuplicateCandidate{
			ID: entry.ID, Title: entry.Title, Path: entry.Path, Type: entry.Type,
			Status: entry.Status, Revision: entry.Revision, Similarity: roundSimilarity(score),
		})
	}
	sort.Slice(candidates, func(left, right int) bool {
		if candidates[left].Similarity != candidates[right].Similarity {
			return candidates[left].Similarity > candidates[right].Similarity
		}
		return candidates[left].ID < candidates[right].ID
	})
	if len(candidates) > 5 {
		candidates = candidates[:5]
	}
	return candidates, nil
}

func highSimilarityCandidates(candidates []DuplicateCandidate) []DuplicateCandidate {
	result := []DuplicateCandidate{}
	for _, candidate := range candidates {
		if candidate.Similarity >= 0.8 {
			result = append(result, candidate)
		}
	}
	return result
}

func documentSimilarity(titleA, summaryA, bodyA, titleB, summaryB, bodyB string) float64 {
	titleScore := diceSimilarity(normalizeForSimilarity(titleA), normalizeForSimilarity(titleB))
	textA := normalizeForSimilarity(titleA + " " + summaryA + " " + firstRunes(bodyA, 1200))
	textB := normalizeForSimilarity(titleB + " " + summaryB + " " + firstRunes(bodyB, 1200))
	textScore := diceSimilarity(textA, textB)
	score := titleScore*0.5 + textScore*0.5
	if score < 0 || score > 1 {
		return 0
	}
	return score
}

func normalizeForSimilarity(value string) string {
	var builder strings.Builder
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(r)
			continue
		}
		builder.WriteRune(' ')
	}
	return strings.Join(strings.Fields(builder.String()), " ")
}

func diceSimilarity(left, right string) float64 {
	if left == "" || right == "" {
		return 0
	}
	if left == right {
		return 1
	}
	leftPairs := runePairs(left)
	rightPairs := runePairs(right)
	if len(leftPairs) == 0 || len(rightPairs) == 0 {
		return 0
	}
	common := 0
	for pair, count := range leftPairs {
		if other := rightPairs[pair]; other > 0 {
			if other < count {
				common += other
			} else {
				common += count
			}
		}
	}
	return float64(2*common) / float64(len(leftPairs)+len(rightPairs))
}

func runePairs(value string) map[string]int {
	runes := []rune(value)
	pairs := map[string]int{}
	for index := 0; index+1 < len(runes); index++ {
		pairs[string(runes[index:index+2])]++
	}
	return pairs
}

func roundSimilarity(value float64) float64 {
	return float64(int(value*1000+0.5)) / 1000
}

func firstRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func (m *Manager) restoreWriteLocked(target string, previous []byte, created bool) error {
	if target == "" {
		return nil
	}
	full := filepath.Join(m.config.VaultPath, filepath.FromSlash(target))
	if created {
		if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
			return err
		}
	} else if previous != nil {
		if err := writeVaultFileIfChanged(m.config.VaultPath, full, previous, 0o644); err != nil {
			return err
		}
	}
	_, err := m.rebuildLocked(context.Background())
	return err
}

func (m *Manager) agentStatePath() string {
	return filepath.Join(m.config.VaultPath, filepath.FromSlash(agentStateRelative))
}

func (m *Manager) loadAgentStateLocked() (agentWriteState, error) {
	state := agentWriteState{Version: agentStateVersion, Turns: map[string]int{}}
	raw, err := os.ReadFile(m.agentStatePath())
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return agentWriteState{}, fmt.Errorf("read agent write state: %w", err)
	}
	if len(raw) == 0 {
		return state, nil
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return agentWriteState{}, fmt.Errorf("parse agent write state: %w", err)
	}
	if state.Turns == nil {
		state.Turns = map[string]int{}
	}
	return state, nil
}

func (m *Manager) saveAgentStateLocked(state agentWriteState) error {
	state.Version = agentStateVersion
	encoded, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	return writeVaultFileIfChanged(m.config.VaultPath, m.agentStatePath(), encoded, 0o644)
}

func turnBudgetKey(request WriteRequest) string {
	return request.SourceTask + "|" + request.SourceTurn
}

func checkWriteBudget(state agentWriteState, request WriteRequest, budget int) error {
	if request.SkipBudget {
		return nil
	}
	if request.SourceTask == "" && request.SourceTurn == "" {
		return nil
	}
	if budget <= 0 {
		budget = DefaultWriteBudget
	}
	if state.Turns[turnBudgetKey(request)] >= budget {
		return writeError(WriteErrorBudget, "this Task/Turn has reached the %d knowledge write budget", budget)
	}
	return nil
}

// idempotencyKeyScope namespaces a client key by the Task that submitted it.
// The agent-writes state file is shared by the whole vault, so a key alone
// would let another Task replay (and silently swallow) a write it never made.
func idempotencyKeyScope(sourceTask, key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	return strings.TrimSpace(sourceTask) + "|" + key
}

func findWriteRecord(state agentWriteState, sourceTask, key string) (agentWriteRecord, bool) {
	scope := idempotencyKeyScope(sourceTask, key)
	if scope == "" {
		return agentWriteRecord{}, false
	}
	for index := len(state.Entries) - 1; index >= 0; index-- {
		record := state.Entries[index]
		if idempotencyKeyScope(record.SourceTask, record.Key) == scope {
			return record, true
		}
	}
	return agentWriteRecord{}, false
}

func findFeedbackRecord(state agentWriteState, sourceTask, key string) (agentFeedbackRecord, bool) {
	scope := idempotencyKeyScope(sourceTask, key)
	if scope == "" {
		return agentFeedbackRecord{}, false
	}
	for index := len(state.Feedback) - 1; index >= 0; index-- {
		record := state.Feedback[index]
		if idempotencyKeyScope(record.SourceTask, record.Key) == scope {
			return record, true
		}
	}
	return agentFeedbackRecord{}, false
}

func capWriteRecords(values []agentWriteRecord, limit int) []agentWriteRecord {
	if len(values) <= limit {
		return values
	}
	return append([]agentWriteRecord{}, values[len(values)-limit:]...)
}

func capFeedbackRecords(values []agentFeedbackRecord, limit int) []agentFeedbackRecord {
	if len(values) <= limit {
		return values
	}
	return append([]agentFeedbackRecord{}, values[len(values)-limit:]...)
}

func capPendingUpdates(values []agentPendingUpdate, limit int) []agentPendingUpdate {
	if len(values) <= limit {
		return values
	}
	return append([]agentPendingUpdate{}, values[len(values)-limit:]...)
}

func randomHex(bytesCount int) string {
	buffer := make([]byte, bytesCount)
	if _, err := rand.Read(buffer); err != nil {
		return strings.ReplaceAll(fmt.Sprintf("%d", time.Now().UTC().UnixNano()), ".", "")
	}
	return hex.EncodeToString(buffer)
}

func hashBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

// diffSummary produces the short audit string stored with each write. The full
// diff is not persisted: the vault is a Git repository, so the file history is
// the authoritative diff.
func diffSummary(previous, current []byte) string {
	if previous == nil {
		document := ParseDocument(string(current))
		return fmt.Sprintf("created %s (%d body chars)", document.Frontmatter.Title, len([]rune(document.Body)))
	}
	old := ParseDocument(string(previous))
	fresh := ParseDocument(string(current))
	return fmt.Sprintf("revised %s: summary and body replaced (%d -> %d body chars)",
		fresh.Frontmatter.Title, len([]rune(old.Body)), len([]rune(fresh.Body)))
}
