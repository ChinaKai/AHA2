package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
	"github.com/ChinaKai/AHA2/internal/store"
)

// Errors surfaced by the knowledge write router. The HTTP layer maps them to
// conflict responses so a caller learns that the read source, not the request
// shape, is why the write was refused.
var (
	ErrKnowledgeVaultDeleteUnsupported = errors.New("文件知识库中的知识条目暂不支持删除")
	ErrKnowledgeReadSourceMismatch     = errors.New("知识条目不在当前读取源中，已拒绝写入旧库")
)

// vaultGlobalReadEnabled reports whether global knowledge is read from the
// vault. Global entries have no project binding, so the project manifest check
// does not apply; the manager switch, runtime read gate, rollback allowlist and
// index readiness do.
func (s *Service) vaultGlobalReadEnabled() bool {
	if s == nil || s.knowledgeVault == nil || !s.knowledgeVaultRuntimeRead {
		return false
	}
	if raw, configured := os.LookupEnv(knowledgeVaultReadProjectsEnv); configured && strings.TrimSpace(raw) == "" {
		return false
	}
	ready := false
	err := s.knowledgeVault.WithReader(func(reader *knowledgevault.Reader) error {
		ready = reader.IndexReady()
		return nil
	})
	return err == nil && ready
}

func (s *Service) vaultGlobalKnowledgeWriteEnabled() bool {
	return s.vaultGlobalReadEnabled() && s.knowledgeVaultRuntimeWrite && s.knowledgeVault.RuntimeWriteEnabled()
}

func (s *Service) vaultGlobalFallbackWarning() string {
	if s == nil || s.knowledgeVault == nil || !s.knowledgeVault.Enabled() {
		return ""
	}
	if !s.knowledgeVaultRuntimeRead {
		return "知识库已启用，但支持诊断开关关闭了读取；全局知识使用旧知识库。"
	}
	if raw, configured := os.LookupEnv(knowledgeVaultReadProjectsEnv); configured && strings.TrimSpace(raw) == "" {
		return "知识库已启用，但支持诊断回退名单已清空；全局知识使用旧知识库。"
	}
	ready := false
	_ = s.knowledgeVault.WithReader(func(reader *knowledgevault.Reader) error {
		ready = reader.IndexReady()
		return nil
	})
	if !ready {
		return "知识库已启用，但索引未就绪；全局知识使用旧知识库。"
	}
	return ""
}

// knowledgeWriteRoute chooses the write source from the read source. It returns
// (true, nil) for the vault, (false, nil) for the legacy tables and an error
// when the source reads the vault but the write gate is off. That last case
// must never fall back silently.
func (s *Service) knowledgeWriteRoute(scope, projectID string) (bool, error) {
	if strings.TrimSpace(scope) == "global" {
		if s.vaultGlobalKnowledgeWriteEnabled() {
			return true, nil
		}
		if s.vaultGlobalReadEnabled() {
			return false, s.vaultWriteDisabledError()
		}
		return false, nil
	}
	if s.vaultKnowledgeWriteEnabled(projectID) {
		return true, nil
	}
	if s.vaultReadEnabled(projectID) {
		return false, s.vaultWriteDisabledError()
	}
	return false, nil
}

// vaultEntryByID reports whether an ID belongs to the vault. It is used before a
// Pet/Web operation touches either store, so an ID that exists in the vault is
// never revised or deleted through the legacy tables.
func (s *Service) vaultGlobalKnowledge(ctx context.Context) ([]domain.KnowledgeEntry, bool) {
	if !s.vaultGlobalReadEnabled() {
		return nil, false
	}
	var result []domain.KnowledgeEntry
	ok := false
	err := s.knowledgeVault.WithReader(func(reader *knowledgevault.Reader) error {
		if !reader.IndexReady() {
			return nil
		}
		entries, err := reader.Entries(ctx, "global", "")
		if err != nil {
			return err
		}
		active, stale := splitVaultKnowledge(mapVaultKnowledge(entries))
		result = append(active, stale...)
		ok = true
		return nil
	})
	if err != nil {
		return nil, false
	}
	return result, ok
}

func (s *Service) readSourceIsVault(scope, projectID string) bool {
	if strings.TrimSpace(scope) == "global" {
		return s.vaultGlobalReadEnabled()
	}
	return s.vaultReadEnabled(projectID)
}

func (s *Service) vaultEntryByID(ctx context.Context, id string) (knowledgevault.IndexEntry, bool, error) {
	id = strings.TrimSpace(id)
	if id == "" || s == nil || s.knowledgeVault == nil {
		return knowledgevault.IndexEntry{}, false, nil
	}
	var entry knowledgevault.IndexEntry
	found := false
	err := s.knowledgeVault.WithReader(func(reader *knowledgevault.Reader) error {
		item, lookupErr := reader.EntryByID(ctx, id)
		switch {
		case lookupErr == nil:
			entry, found = item, true
		case errors.Is(lookupErr, sql.ErrNoRows):
		default:
			return lookupErr
		}
		return nil
	})
	if errors.Is(err, knowledgevault.ErrDisabled) {
		return knowledgevault.IndexEntry{}, false, nil
	}
	if err != nil {
		return knowledgevault.IndexEntry{}, false, err
	}
	return entry, found, nil
}

func (s *Service) vaultDomainEntry(ctx context.Context, id string) (domain.KnowledgeEntry, bool, error) {
	entry, found, err := s.vaultEntryByID(ctx, id)
	if err != nil || !found {
		return domain.KnowledgeEntry{}, false, err
	}
	item, ok := mapVaultKnowledgeEntry(entry)
	return item, ok, nil
}

func knowledgeCanonicalType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return knowledgevault.TypePractice
	}
	mapped, _ := knowledgevault.NormalizeType(value, "")
	return mapped
}

// knowledgeSummaryFor derives the single-line summary the vault contract
// requires from the candidate body. Pet and Web payloads never had a summary
// field, so requiring one would otherwise make every submission invalid.
func knowledgeSummaryFor(title, body string) string {
	value := strings.Join(strings.Fields(body), " ")
	runes := []rune(value)
	if len(runes) > 600 {
		value = string(runes[:599]) + "…"
		runes = []rune(value)
	}
	if len(runes) >= 20 {
		return value
	}
	for _, separator := range []string{"：", " "} {
		candidate := strings.TrimSpace(title + separator + value)
		if len([]rune(candidate)) < 20 {
			continue
		}
		if len([]rune(candidate)) > 600 {
			candidate = string([]rune(candidate)[:599]) + "…"
		}
		return candidate
	}
	return value
}

func knowledgeEntryContentHash(title, body string) string {
	value := sha256.Sum256([]byte(strings.TrimSpace(title) + "\n" + strings.TrimSpace(body)))
	return fmt.Sprintf("%x", value[:])
}

func knowledgeProjectIDForScope(scope, projectID string) (string, string) {
	scope = strings.ToLower(strings.TrimSpace(scope))
	if scope == "global" {
		return "global", ""
	}
	return "project", strings.TrimSpace(projectID)
}

type vaultCandidatePlan struct {
	request   knowledgevault.WriteRequest
	proposed  domain.KnowledgeEntry
	baseEntry *domain.KnowledgeEntry
}

// planVaultCandidate converts one legacy-shaped candidate into a vault write
// request plus the proposed entry the review page shows. Create requests carry
// the generated ID through ForceEntryID; revise requests are validated against
// the vault's current revision.
func (s *Service) planVaultCandidate(ctx context.Context, projectID, taskID, turnID string, candidate KnowledgeCandidate) (vaultCandidatePlan, error) {
	now := s.now().UTC()
	scope, projectID := knowledgeProjectIDForScope(candidate.Scope, projectID)
	candidate.Scope = scope
	if candidate.IsIndex != nil && *candidate.IsIndex {
		return vaultCandidatePlan{}, fmt.Errorf("文件知识库不支持通过知识提交创建索引页")
	}
	typ := knowledgeCanonicalType(candidate.Type)
	confidence := candidate.Confidence
	request := knowledgevault.WriteRequest{
		Scope: scope, ProjectID: projectID, AllowGlobal: true,
		Type: typ, Title: strings.TrimSpace(candidate.Title), Body: strings.TrimSpace(candidate.Body),
		Summary: knowledgeSummaryFor(candidate.Title, candidate.Body), Confidence: &confidence,
		DuplicateAck: true, SourceTask: taskID, SourceTurn: turnID, SkipBudget: true,
	}

	if strings.TrimSpace(candidate.EntryID) == "" {
		entryID := domain.NewID("knowledge")
		if scope == "global" && (candidate.ParentID == nil || strings.TrimSpace(*candidate.ParentID) == "") {
			parentID := "knowledge_global_agent_lessons_behavior"
			if strings.ToLower(strings.TrimSpace(candidate.Type)) == "diagnostic" {
				parentID = "knowledge_global_agent_lessons_technical"
			}
			candidate.ParentID = &parentID
		}
		slug := knowledgevault.Slugify(request.Title)
		if slug == "" {
			slug = "document"
		}
		request.Mode = knowledgevault.WriteModeCreate
		request.ForceEntryID = entryID
		request.Slug = &slug
		request.ParentID = candidate.ParentID
		request.SortOrder = candidate.SortOrder
		proposed := domain.KnowledgeEntry{
			ID: entryID, Scope: scope, ProjectID: projectID, ParentID: strings.TrimSpace(valueOrEmpty(candidate.ParentID)),
			Slug: slug, Type: typ, Title: request.Title, Body: request.Body,
			Status: domain.KnowledgeCandidate, Confidence: confidence, Revision: 1,
			SourceTaskID: taskID, SourceTurnID: turnID, CreatedAt: now, UpdatedAt: now,
		}
		if candidate.SortOrder != nil {
			proposed.SortOrder = *candidate.SortOrder
		}
		if candidate.Slug != nil {
			request.Slug = candidate.Slug
			proposed.Slug = strings.TrimSpace(*candidate.Slug)
		}
		return vaultCandidatePlan{request: request, proposed: proposed}, nil
	}

	existing, found, err := s.vaultDomainEntry(ctx, candidate.EntryID)
	if err != nil {
		return vaultCandidatePlan{}, err
	}
	if !found {
		return vaultCandidatePlan{}, fmt.Errorf("知识条目 %s 不在文件知识库中", candidate.EntryID)
	}
	if existing.Scope != scope || (scope == "project" && existing.ProjectID != projectID) {
		return vaultCandidatePlan{}, fmt.Errorf("只能修订当前 scope 的知识条目")
	}
	if candidate.BaseRevision <= 0 || candidate.BaseRevision != existing.Revision {
		return vaultCandidatePlan{}, fmt.Errorf("知识条目 %s 的 base_revision 已过期", candidate.EntryID)
	}
	request.Mode = knowledgevault.WriteModeRevise
	request.EntryID = existing.ID
	request.BaseRevision = existing.Revision
	request.ProjectID = existing.ProjectID
	request.ParentID = candidate.ParentID
	request.Slug = candidate.Slug
	request.SortOrder = candidate.SortOrder
	proposed := existing
	proposed.Scope, proposed.ProjectID = existing.Scope, existing.ProjectID
	proposed.Type, proposed.Title, proposed.Body = typ, request.Title, request.Body
	proposed.Confidence = confidence
	proposed.Status = domain.KnowledgeCandidate
	proposed.Revision = existing.Revision + 1
	proposed.SourceTaskID, proposed.SourceTurnID = taskID, turnID
	proposed.UpdatedAt = now
	if candidate.ParentID != nil {
		proposed.ParentID = strings.TrimSpace(*candidate.ParentID)
	}
	if candidate.Slug != nil {
		proposed.Slug = strings.TrimSpace(*candidate.Slug)
	}
	if candidate.SortOrder != nil {
		proposed.SortOrder = *candidate.SortOrder
	}
	return vaultCandidatePlan{request: request, proposed: proposed, baseEntry: &existing}, nil
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// createVaultKnowledgeCandidates is the vault counterpart of
// createKnowledgeCandidates. Manual review leaves a pending proposal without
// touching the vault; auto review publishes the document and records the
// approved proposal so both review modes stay auditable.
func (s *Service) createVaultKnowledgeCandidates(ctx context.Context, projectID, taskID, turnID, branch, productLineID string, candidates []KnowledgeCandidate) ([]domain.KnowledgeEntry, []domain.KnowledgeProposal, error) {
	now := s.now().UTC()
	reviewSettings, _ := s.store.KnowledgeReviewSettings(ctx)
	reviewMode := "manual"
	if reviewSettings.AutoApprove {
		reviewMode = "auto"
	}
	result := make([]domain.KnowledgeEntry, 0, len(candidates))
	proposals := make([]domain.KnowledgeProposal, 0, len(candidates))
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.Title) == "" || strings.TrimSpace(candidate.Body) == "" {
			continue
		}
		plan, err := s.planVaultCandidate(ctx, projectID, taskID, turnID, candidate)
		if err != nil {
			return nil, nil, err
		}
		if err := knowledgevault.ValidateWriteRequest(plan.request); err != nil {
			return nil, nil, err
		}
		plan.proposed.BranchScope = branch
		if candidate.ProductLineID != "" {
			plan.proposed.ProductLineID = candidate.ProductLineID
		} else if plan.request.Scope == "project" {
			plan.proposed.ProductLineID = productLineID
		}
		proposal := domain.KnowledgeProposal{
			ID: domain.NewID("knowledge_proposal"), EntryID: plan.proposed.ID,
			BaseRevision: plan.request.BaseRevision, Proposed: plan.proposed, BaseEntry: plan.baseEntry,
			SourceTaskID: taskID, SourceTurnID: turnID, Status: domain.KnowledgeProposalPending,
			ReviewMode: reviewMode, Reason: strings.TrimSpace(candidate.Reason),
			TargetSource: domain.KnowledgeProposalTargetVault, CreatedAt: now, UpdatedAt: now,
		}
		created, err := s.store.CreateKnowledgeProposal(ctx, proposal)
		if err != nil {
			return nil, nil, err
		}
		s.emit(ctx, taskID, "knowledge_proposal", created.ID, "knowledge_proposal_created", map[string]any{
			"entry_id": created.EntryID, "title": created.Proposed.Title, "base_revision": created.BaseRevision, "target": "vault",
		})
		if reviewMode != "auto" {
			result = append(result, created.Proposed)
			proposals = append(proposals, created)
			continue
		}
		plan.request.IdempotencyKey = "pet-auto:" + created.ID
		written, writeErr := s.knowledgeVault.WriteKnowledge(ctx, plan.request)
		if writeErr != nil {
			_, _ = s.store.RejectKnowledgeProposal(ctx, created.ID, s.now().UTC(), "自动发布到文件知识库失败："+writeErr.Error())
			return nil, nil, fmt.Errorf("auto-approve knowledge proposal %s: %w", created.ID, writeErr)
		}
		approved, markErr := s.store.MarkKnowledgeProposalApproved(ctx, created.ID, s.now().UTC(), "auto")
		if markErr != nil {
			return nil, nil, fmt.Errorf("record auto-approved knowledge proposal %s: %w", created.ID, markErr)
		}
		published := knowledgeEntryFromVaultWrite(plan.proposed, written)
		s.emit(ctx, taskID, "knowledge_proposal", approved.ID, "knowledge_proposal_approved", map[string]any{
			"entry_id": published.ID, "revision": published.Revision, "review_mode": "auto", "target": "vault",
		})
		result = append(result, published)
		proposals = append(proposals, approved)
	}
	return result, proposals, nil
}

func knowledgeEntryFromVaultWrite(proposed domain.KnowledgeEntry, result knowledgevault.WriteResult) domain.KnowledgeEntry {
	entry := proposed
	entry.ID = result.ID
	if result.Scope != "" {
		entry.Scope = result.Scope
	}
	entry.ProjectID = result.ProjectID
	if result.Type != "" {
		entry.Type = result.Type
	}
	entry.Title = result.Title
	entry.Status = domain.KnowledgeVerified
	entry.Revision = result.Revision
	entry.ContentHash = result.ContentHash
	entry.SourceTaskID, entry.SourceTurnID = result.SourceTask, result.SourceTurn
	entry.CreatedAt = parseVaultTime(result.Created)
	entry.UpdatedAt = parseVaultTime(result.Updated)
	entry.LastVerifiedAt = entry.UpdatedAt
	return entry
}

// createKnowledgeCandidates keeps its legacy signature but chooses the write
// source first. A vault-routed batch never touches knowledge_entries, and a
// vault read with the write gate off returns an error instead of falling back.
func (s *Service) createKnowledgeCandidates(ctx context.Context, projectID, taskID, turnID, branch, productLineID string, candidates []KnowledgeCandidate) ([]domain.KnowledgeEntry, []domain.KnowledgeProposal, error) {
	_, vault, err := s.vaultCandidateRoute(projectID, candidates)
	if err != nil {
		return nil, nil, err
	}
	if vault {
		return s.createVaultKnowledgeCandidates(ctx, projectID, taskID, turnID, branch, productLineID, candidates)
	}
	return s.createLegacyKnowledgeCandidates(ctx, projectID, taskID, turnID, branch, productLineID, candidates)
}

func (s *Service) vaultCandidateRoute(projectID string, candidates []KnowledgeCandidate) (string, bool, error) {
	vault := false
	legacy := false
	for _, candidate := range candidates {
		scope, targetProject := knowledgeProjectIDForScope(candidate.Scope, projectID)
		useVault, err := s.knowledgeWriteRoute(scope, targetProject)
		if err != nil {
			return "", false, err
		}
		if useVault {
			vault = true
		} else {
			legacy = true
		}
	}
	if vault && legacy {
		return "", false, fmt.Errorf("同一批知识候选不能混合写入文件知识库和旧知识库")
	}
	return scopeName(vault), vault, nil
}

func scopeName(vault bool) string {
	if vault {
		return "vault"
	}
	return "legacy"
}

// ApproveKnowledgeProposal routes approval by the proposal's target source.
// Vault proposals publish the document first and only then become approved, so
// a failed write leaves the proposal pending and retryable.
func (s *Service) ApproveKnowledgeProposal(ctx context.Context, id string, decidedAt time.Time, decisionReason ...string) (domain.KnowledgeProposal, domain.KnowledgeEntry, error) {
	proposal, err := s.store.KnowledgeProposal(ctx, id)
	if err != nil {
		return domain.KnowledgeProposal{}, domain.KnowledgeEntry{}, err
	}
	if proposal.TargetSource != domain.KnowledgeProposalTargetVault {
		return s.store.ApproveKnowledgeProposal(ctx, id, decidedAt, decisionReason...)
	}
	return s.approveVaultKnowledgeProposal(ctx, proposal, decidedAt, decisionReason...)
}

func (s *Service) approveVaultKnowledgeProposal(ctx context.Context, proposal domain.KnowledgeProposal, decidedAt time.Time, decisionReason ...string) (domain.KnowledgeProposal, domain.KnowledgeEntry, error) {
	request, err := vaultWriteRequestFromProposal(proposal)
	if err != nil {
		return domain.KnowledgeProposal{}, domain.KnowledgeEntry{}, err
	}
	if err := knowledgevault.ValidateWriteRequest(request); err != nil {
		return domain.KnowledgeProposal{}, domain.KnowledgeEntry{}, err
	}
	written, writeErr := s.knowledgeVault.WriteKnowledge(ctx, request)
	if writeErr != nil {
		// A create retried after the file landed but the proposal update failed
		// must be idempotent. The entry ID is unique, so "already exists" is the
		// only duplicate that can mean this approval already published.
		var target *knowledgevault.WriteError
		if !errors.As(writeErr, &target) || target.Code != knowledgevault.WriteErrorDuplicate || request.Mode != knowledgevault.WriteModeCreate {
			return domain.KnowledgeProposal{}, domain.KnowledgeEntry{}, writeErr
		}
		existing, found, lookupErr := s.vaultDomainEntry(ctx, proposal.EntryID)
		if lookupErr != nil {
			return domain.KnowledgeProposal{}, domain.KnowledgeEntry{}, lookupErr
		}
		if !found {
			return domain.KnowledgeProposal{}, domain.KnowledgeEntry{}, writeErr
		}
		written = knowledgevault.WriteResult{
			Mode: request.Mode, ID: existing.ID, Scope: existing.Scope, ProjectID: existing.ProjectID,
			Type: existing.Type, Title: existing.Title, Status: string(existing.Status),
			Revision: existing.Revision, ContentHash: existing.ContentHash,
			Created: existing.CreatedAt.Format(time.RFC3339), Updated: existing.UpdatedAt.Format(time.RFC3339),
			SourceTask: request.SourceTask, SourceTurn: request.SourceTurn, Writer: request.Writer,
		}
	}
	reason := ""
	if len(decisionReason) > 0 {
		reason = strings.TrimSpace(decisionReason[0])
	}
	approved, err := s.store.MarkKnowledgeProposalApproved(ctx, proposal.ID, decidedAt, reason)
	if err != nil {
		return domain.KnowledgeProposal{}, domain.KnowledgeEntry{}, err
	}
	return approved, knowledgeEntryFromVaultWrite(proposal.Proposed, written), nil
}

func vaultWriteRequestFromProposal(proposal domain.KnowledgeProposal) (knowledgevault.WriteRequest, error) {
	proposed := proposal.Proposed
	scope, projectID := knowledgeProjectIDForScope(proposed.Scope, proposed.ProjectID)
	request := knowledgevault.WriteRequest{
		Mode: knowledgevault.WriteModeRevise, Scope: scope, ProjectID: projectID,
		EntryID: proposal.EntryID, BaseRevision: proposal.BaseRevision, AllowGlobal: true,
		Type: knowledgeCanonicalType(proposed.Type), Title: proposed.Title, Body: proposed.Body,
		Summary: knowledgeSummaryFor(proposed.Title, proposed.Body), DuplicateAck: true,
		SourceTask: proposal.SourceTaskID, SourceTurn: proposal.SourceTurnID,
		SkipBudget: true, IdempotencyKey: "knowledge-proposal:" + proposal.ID,
	}
	confidence := proposed.Confidence
	request.Confidence = &confidence
	if proposal.BaseRevision == 0 {
		request.Mode = knowledgevault.WriteModeCreate
		request.EntryID = ""
		request.ForceEntryID = proposal.EntryID
	} else if request.EntryID == "" {
		return knowledgevault.WriteRequest{}, fmt.Errorf("知识提案缺少 entry_id")
	}
	if proposed.IsIndex {
		return knowledgevault.WriteRequest{}, fmt.Errorf("文件知识库不支持通过知识提案写入索引页")
	}
	if strings.TrimSpace(proposed.ParentID) != "" {
		parentID := strings.TrimSpace(proposed.ParentID)
		request.ParentID = &parentID
	}
	if strings.TrimSpace(proposed.Slug) != "" {
		slug := strings.TrimSpace(proposed.Slug)
		request.Slug = &slug
	}
	if proposal.BaseRevision == 0 || proposed.SortOrder != 0 {
		sortOrder := proposed.SortOrder
		request.SortOrder = &sortOrder
	}
	return request, nil
}

type KnowledgeWriteInput struct {
	ID             string
	Scope          string
	ProjectID      string
	Type           string
	Title          string
	Body           string
	ParentID       *string
	Slug           *string
	SortOrder      *int
	IsIndex        *bool
	Confidence     float64
	Status         string
	BranchScope    string
	ProductLineID  string
	VerifiedCommit string
}

type KnowledgeWriteResult struct {
	Entry   domain.KnowledgeEntry
	Source  string
	Warning string
}

// CreateWebKnowledge is the Web create entry point. It publishes to the same
// source the Web reads: a vault-routed project writes the file vault, an
// unbound/read-fallback project keeps the legacy tables and reports a warning.
func (s *Service) CreateWebKnowledge(ctx context.Context, input KnowledgeWriteInput) (KnowledgeWriteResult, error) {
	scope, projectID := knowledgeProjectIDForScope(input.Scope, input.ProjectID)
	input.Scope, input.ProjectID = scope, projectID
	if input.Scope == "project" && input.ProjectID == "" {
		return KnowledgeWriteResult{}, fmt.Errorf("project_id_required")
	}
	useVault, err := s.knowledgeWriteRoute(input.Scope, input.ProjectID)
	if err != nil {
		return KnowledgeWriteResult{}, err
	}
	title, body := strings.TrimSpace(input.Title), strings.TrimSpace(input.Body)
	typ := knowledgeCanonicalType(input.Type)
	if useVault {
		if input.IsIndex != nil && *input.IsIndex {
			return KnowledgeWriteResult{}, fmt.Errorf("文件知识库不支持通过 Web 创建索引页")
		}
		entryID := strings.TrimSpace(input.ID)
		if entryID == "" {
			entryID = domain.NewID("knowledge")
		}
		slug := knowledgevault.Slugify(title)
		if slug == "" {
			slug = "document"
		}
		if input.Slug != nil {
			slug = strings.TrimSpace(*input.Slug)
		}
		confidence := input.Confidence
		request := knowledgevault.WriteRequest{
			Mode: knowledgevault.WriteModeCreate, Scope: input.Scope, ProjectID: input.ProjectID,
			AllowGlobal: true, ForceEntryID: entryID, Slug: &slug, ParentID: input.ParentID,
			SortOrder: input.SortOrder, Type: typ, Title: title, Body: body,
			Summary: knowledgeSummaryFor(title, body), Confidence: &confidence,
			DuplicateAck: true, Writer: "web", SkipBudget: true,
		}
		if err := knowledgevault.ValidateWriteRequest(request); err != nil {
			return KnowledgeWriteResult{}, err
		}
		written, err := s.knowledgeVault.WriteKnowledge(ctx, request)
		if err != nil {
			return KnowledgeWriteResult{}, err
		}
		proposed := domain.KnowledgeEntry{
			ID: entryID, Scope: input.Scope, ProjectID: input.ProjectID, Type: typ, Title: title, Body: body,
			Slug: slug, Confidence: confidence, BranchScope: input.BranchScope, ProductLineID: input.ProductLineID,
		}
		if input.ParentID != nil {
			proposed.ParentID = strings.TrimSpace(*input.ParentID)
		}
		if input.SortOrder != nil {
			proposed.SortOrder = *input.SortOrder
		}
		return KnowledgeWriteResult{Entry: knowledgeEntryFromVaultWrite(proposed, written), Source: "vault"}, nil
	}

	now := s.now().UTC()
	status := domain.KnowledgeCandidate
	if strings.TrimSpace(input.Status) == string(domain.KnowledgeVerified) {
		status = domain.KnowledgeVerified
	}
	item := domain.KnowledgeEntry{
		ID: domain.NewID("knowledge"), Scope: input.Scope, ProjectID: input.ProjectID, Type: typ,
		Title: title, Body: body, Status: status, BranchScope: input.BranchScope,
		ProductLineID: input.ProductLineID, Confidence: input.Confidence, Revision: 1,
		ContentHash: knowledgeEntryContentHash(title, body), VerifiedCommit: input.VerifiedCommit,
		CreatedAt: now, UpdatedAt: now,
	}
	item.Slug = store.KnowledgeSlug(title, item.ID)
	if input.ParentID != nil {
		item.ParentID = strings.TrimSpace(*input.ParentID)
	}
	if input.Slug != nil {
		item.Slug = strings.TrimSpace(*input.Slug)
	}
	if input.SortOrder != nil {
		item.SortOrder = *input.SortOrder
	}
	if input.IsIndex != nil {
		item.IsIndex = *input.IsIndex
	}
	if status == domain.KnowledgeVerified {
		item.LastVerifiedAt = now
	}
	if err := s.store.CreateKnowledge(ctx, item); err != nil {
		return KnowledgeWriteResult{}, err
	}
	stored, err := s.store.Knowledge(ctx, item.ID)
	if err != nil {
		return KnowledgeWriteResult{}, err
	}
	item = stored
	warning := s.vaultFallbackWarning(input.ProjectID)
	if input.Scope == "global" {
		warning = s.vaultGlobalFallbackWarning()
	}
	return KnowledgeWriteResult{Entry: item, Source: "legacy", Warning: warning}, nil
}

// UpdateWebKnowledge routes a Web edit to the source that owns the entry. A
// vault entry can never be edited through the legacy tables; a legacy-only
// entry stays hidden from a vault-routed project and its edit is refused.
func (s *Service) UpdateWebKnowledge(ctx context.Context, id string, input KnowledgeWriteInput) (KnowledgeWriteResult, error) {
	id = strings.TrimSpace(id)
	vaultEntry, vaultFound, err := s.vaultEntryByID(ctx, id)
	if err != nil {
		return KnowledgeWriteResult{}, err
	}
	if vaultFound {
		scope, projectID := knowledgeProjectIDForScope(vaultEntry.Scope, vaultEntry.ProjectID)
		payloadScope, payloadProject := knowledgeProjectIDForScope(input.Scope, input.ProjectID)
		if payloadScope != scope || payloadProject != projectID {
			return KnowledgeWriteResult{}, fmt.Errorf("文件知识库中的条目不能跨 scope/project 移动")
		}
		if input.IsIndex != nil && *input.IsIndex != vaultEntry.IsIndex {
			return KnowledgeWriteResult{}, fmt.Errorf("文件知识库不支持通过 Web 修改索引页类型")
		}
		status := strings.TrimSpace(input.Status)
		if status != "" && status != string(domain.KnowledgeVerified) {
			return KnowledgeWriteResult{}, fmt.Errorf("文件知识库写入只支持 verified 状态")
		}
		useVault, routeErr := s.knowledgeWriteRoute(scope, projectID)
		if routeErr != nil {
			return KnowledgeWriteResult{}, routeErr
		}
		if !useVault {
			return KnowledgeWriteResult{}, ErrKnowledgeReadSourceMismatch
		}
		title, body := strings.TrimSpace(input.Title), strings.TrimSpace(input.Body)
		confidence := input.Confidence
		request := knowledgevault.WriteRequest{
			Mode: knowledgevault.WriteModeRevise, Scope: scope, ProjectID: projectID,
			EntryID: vaultEntry.ID, BaseRevision: vaultEntry.Revision, AllowGlobal: true,
			Type: knowledgeCanonicalType(input.Type), Title: title, Body: body,
			Summary: knowledgeSummaryFor(title, body), Confidence: &confidence,
			DuplicateAck: true, Writer: "web", SkipBudget: true,
		}
		request.ParentID = input.ParentID
		request.Slug = input.Slug
		request.SortOrder = input.SortOrder
		if err := knowledgevault.ValidateWriteRequest(request); err != nil {
			return KnowledgeWriteResult{}, err
		}
		written, err := s.knowledgeVault.WriteKnowledge(ctx, request)
		if err != nil {
			return KnowledgeWriteResult{}, err
		}
		proposed, ok := mapVaultKnowledgeEntry(vaultEntry)
		if !ok {
			return KnowledgeWriteResult{}, fmt.Errorf("文件知识库条目 %s 不可编辑", vaultEntry.ID)
		}
		proposed.Type, proposed.Title, proposed.Body = request.Type, title, body
		proposed.Confidence = confidence
		return KnowledgeWriteResult{Entry: knowledgeEntryFromVaultWrite(proposed, written), Source: "vault"}, nil
	}

	item, err := s.store.Knowledge(ctx, id)
	if err != nil {
		return KnowledgeWriteResult{}, err
	}
	useVault, routeErr := s.knowledgeWriteRoute(item.Scope, item.ProjectID)
	if routeErr != nil {
		return KnowledgeWriteResult{}, routeErr
	}
	if useVault {
		return KnowledgeWriteResult{}, ErrKnowledgeReadSourceMismatch
	}
	scope, projectID := knowledgeProjectIDForScope(input.Scope, input.ProjectID)
	item.Scope, item.ProjectID = scope, projectID
	item.Type, item.Title, item.Body = knowledgeCanonicalType(input.Type), strings.TrimSpace(input.Title), strings.TrimSpace(input.Body)
	item.BranchScope, item.ProductLineID = input.BranchScope, input.ProductLineID
	if input.ParentID != nil {
		item.ParentID = strings.TrimSpace(*input.ParentID)
	}
	if input.Slug != nil {
		item.Slug = strings.TrimSpace(*input.Slug)
	}
	if input.SortOrder != nil {
		item.SortOrder = *input.SortOrder
	}
	if input.IsIndex != nil {
		item.IsIndex = *input.IsIndex
	}
	item.VerifiedCommit, item.Confidence = input.VerifiedCommit, input.Confidence
	item.ContentHash = knowledgeEntryContentHash(item.Title, item.Body)
	item.Revision++
	item.UpdatedAt = s.now().UTC()
	if input.Status != "" {
		item.Status = domain.KnowledgeStatus(input.Status)
	}
	if item.Status == domain.KnowledgeVerified {
		item.LastVerifiedAt = item.UpdatedAt
	}
	if err := s.store.UpdateKnowledge(ctx, item); err != nil {
		return KnowledgeWriteResult{}, err
	}
	warning := s.vaultFallbackWarning(item.ProjectID)
	if item.Scope == "global" {
		warning = s.vaultGlobalFallbackWarning()
	}
	return KnowledgeWriteResult{Entry: item, Source: "legacy", Warning: warning}, nil
}

// DeleteWebKnowledge refuses vault entries because the vault write contract has
// no delete operation. A legacy-only entry that is invisible under the current
// read source is also refused rather than silently deleted.
func (s *Service) DeleteWebKnowledge(ctx context.Context, id string) (string, error) {
	id = strings.TrimSpace(id)
	if _, found, err := s.vaultEntryByID(ctx, id); err != nil {
		return "", err
	} else if found {
		return "", ErrKnowledgeVaultDeleteUnsupported
	}
	item, err := s.store.Knowledge(ctx, id)
	if err != nil {
		return "", err
	}
	useVault, routeErr := s.knowledgeWriteRoute(item.Scope, item.ProjectID)
	if routeErr != nil {
		return "", routeErr
	}
	if useVault {
		return "", ErrKnowledgeReadSourceMismatch
	}
	if err := s.store.DeleteKnowledge(ctx, id); err != nil {
		return "", err
	}
	return "legacy", nil
}
