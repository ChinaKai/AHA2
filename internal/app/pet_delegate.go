package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

const (
	petApprovalPending   = "pending"
	petApprovalExecuting = "executing"
	petApprovalApproved  = "approved"
	petApprovalRejected  = "rejected"
	petApprovalCancelled = "cancelled"
)

const (
	// Heavy deletes can take minutes on a multi-gigabyte database. They run in
	// the background with the service lifetime as parent so a dropped browser
	// connection cannot cancel a transaction that is already committing.
	petApprovalExecutionBudgetDefault = 15 * time.Minute
	// Finalizing and notifying get a fresh context: the execution context may
	// already be canceled by shutdown or by its own deadline when they run.
	petApprovalFinalizeTimeoutDefault = 30 * time.Second
)

// petApprovalRunsInBackground reports whether approving this kind performs a
// long delete transaction. Light approvals keep the synchronous resolve path.
func petApprovalRunsInBackground(kind string) bool {
	switch strings.TrimSpace(kind) {
	case "delete_task", "delete_project", "delete_workspace":
		return true
	}
	return false
}

type PetWorkbenchView struct {
	Core          []PetWorkspaceDocument `json:"core"`
	Worklog       []PetMarkdownEntry     `json:"worklog"`
	WorklogTotal  int                    `json:"worklog_total"`
	Approvals     []domain.PetApproval   `json:"approvals"`
	ApprovalTotal int                    `json:"approval_total"`
	// ApprovalCounts is the untruncated kind/status breakdown of the approval
	// table. The page returns only a pending-first slice, so the browser needs
	// these totals to page and label a client-side filter accurately.
	ApprovalCounts []domain.PetApprovalCount `json:"approval_counts,omitempty"`
	Watches        []PetTaskWatchView        `json:"watches"`
	Task           *domain.Task              `json:"task,omitempty"`
	Channels       []PetChannelHealth        `json:"channels"`
	ChannelTotal   int                       `json:"channel_total,omitempty"`
	ChannelNote    string                    `json:"channel_note,omitempty"`
	Inspection     *PetInspection            `json:"inspection,omitempty"`
	UpdatedAt      time.Time                 `json:"updated_at"`
}

type PetWorkbenchOptions struct {
	ApprovalLimit int
	WorklogLimit  int
}

// PetWorkbench is the data behind the pet's workbench page. It is owner-only
// and read-only; the pet writes core/worklog files through its Task.
func (s *Service) PetWorkbench(ctx context.Context) (PetWorkbenchView, error) {
	return s.PetWorkbenchWithOptions(ctx, PetWorkbenchOptions{})
}

func (s *Service) PetWorkbenchWithOptions(ctx context.Context, options PetWorkbenchOptions) (PetWorkbenchView, error) {
	core, err := s.PetCoreDocuments(ctx)
	if err != nil {
		return PetWorkbenchView{}, err
	}
	worklog, worklogTotal, err := s.PetWorklogPage(ctx, petWorkbenchLimit(options.WorklogLimit, 30))
	if err != nil {
		return PetWorkbenchView{}, err
	}
	approvals, approvalTotal, err := s.PetApprovalsWithTotal(ctx, petWorkbenchLimit(options.ApprovalLimit, 50))
	if err != nil {
		return PetWorkbenchView{}, err
	}
	approvalCounts, err := s.store.PetApprovalCounts(ctx)
	if err != nil {
		return PetWorkbenchView{}, err
	}
	// The watch list and the inspection snapshot share one task read; the
	// inspection also shares the channel snapshot with the channel cards so a
	// busy database is not queried twice for the same rows.
	tasks, err := s.store.ListTasks(ctx, "")
	if err != nil {
		return PetWorkbenchView{}, err
	}
	watches, err := s.petTaskWatchListWithTasks(ctx, tasks)
	if err != nil {
		return PetWorkbenchView{}, err
	}
	now := s.now().UTC()
	channels := s.petChannelSnapshotValues(ctx, now)
	view := PetWorkbenchView{
		Core: core, Worklog: worklog, WorklogTotal: worklogTotal,
		Approvals: approvals, ApprovalTotal: approvalTotal, ApprovalCounts: approvalCounts, Watches: watches,
		Channels: channels.Health, ChannelTotal: channels.Total, ChannelNote: channels.Note,
		UpdatedAt: now,
	}
	// The summary is additive: a failed inspection read must not take the rest
	// of the workbench down, and the client shows an explicit empty state.
	if inspection, inspectErr := s.petWorkbenchInspection(ctx, tasks, now, channels); inspectErr == nil {
		view.Inspection = &inspection
	}
	if task, taskErr := s.store.Task(ctx, petTaskID); taskErr == nil {
		view.Task = &task
	}
	return view, nil
}

// petWorkbenchInspection reuses the petInspection field derivation without the
// costly per-task enrichment PetWorkState performs for its open-task list.
func (s *Service) petWorkbenchInspection(ctx context.Context, tasks []domain.Task, now time.Time, channels petChannelSnapshotValues) (PetInspection, error) {
	pendingApprovals, err := s.store.ListPendingPetApprovals(ctx, 200)
	if err != nil {
		return PetInspection{}, err
	}
	ids, err := s.store.PetCreatedTaskIDs(ctx)
	if err != nil {
		return PetInspection{}, err
	}
	openTasks, attentionTasks := petOpenTaskCounts(ids, tasks, pendingApprovals)
	inspection := s.petInspectionWithChannelSnapshot(now, tasks, pendingApprovals, openTasks, attentionTasks, channels)
	// The workbench already renders the full channel cards, so keep the
	// embedded snapshot compact; callers that need the rows use PetWorkState.
	inspection.Channels = nil
	return inspection, nil
}

func petWorkbenchLimit(value, fallback int) int {
	if value <= 0 {
		value = fallback
	}
	if value > 200 {
		value = 200
	}
	return value
}

func (s *Service) PetApprovals(ctx context.Context, limit int) ([]domain.PetApproval, error) {
	approvals, _, err := s.PetApprovalsWithTotal(ctx, limit)
	return approvals, err
}

func (s *Service) PetApprovalsWithTotal(ctx context.Context, limit int) ([]domain.PetApproval, int, error) {
	return s.store.ListPetApprovalsWithTotal(ctx, limit)
}

type PetApprovalInput struct {
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Reason  string `json:"reason"`
	Risk    string `json:"risk"`
	Payload string `json:"payload"`
}

// CreatePetApproval records a pet request for an owner decision. It does not
// execute the requested action; phase-two owner-delegate write tools will check
// for an approved record before acting.
func (s *Service) CreatePetApproval(ctx context.Context, input PetApprovalInput) (domain.PetApproval, error) {
	item, err := s.buildPetApproval(input)
	if err != nil {
		return domain.PetApproval{}, err
	}
	if err := s.store.CreatePetApproval(ctx, item); err != nil {
		return domain.PetApproval{}, err
	}
	s.afterPetApprovalCreated(ctx, item)
	return item, nil
}

// CreatePetApprovalDeduped is CreatePetApproval with an atomic store-level
// dedupe key. Concurrent submissions of the same request return the winning
// pending approval instead of creating another card; only the winner publishes
// the realtime event.
func (s *Service) CreatePetApprovalDeduped(ctx context.Context, input PetApprovalInput, dedupeKey string) (domain.PetApproval, error) {
	item, err := s.buildPetApproval(input)
	if err != nil {
		return domain.PetApproval{}, err
	}
	stored, err := s.store.CreatePetApprovalDeduped(ctx, item, dedupeKey)
	if err != nil {
		return domain.PetApproval{}, err
	}
	if stored.ID == item.ID {
		s.afterPetApprovalCreated(ctx, stored)
	}
	return stored, nil
}

func (s *Service) buildPetApproval(input PetApprovalInput) (domain.PetApproval, error) {
	kind := strings.TrimSpace(input.Kind)
	title := strings.TrimSpace(input.Title)
	summary := strings.TrimSpace(input.Summary)
	reason := strings.TrimSpace(input.Reason)
	risk := strings.ToLower(strings.TrimSpace(input.Risk))
	if kind == "" || len([]rune(kind)) > 40 {
		return domain.PetApproval{}, fmt.Errorf("审批类型不能为空且不能超过 40 个字符")
	}
	if title == "" || len([]rune(title)) > 120 {
		return domain.PetApproval{}, fmt.Errorf("审批标题不能为空且不能超过 120 个字符")
	}
	if summary == "" || len([]rune(summary)) > 2000 {
		return domain.PetApproval{}, fmt.Errorf("审批说明不能为空且不能超过 2000 个字符")
	}
	switch risk {
	case "low", "medium", "high":
	default:
		risk = "medium"
	}
	payload := strings.TrimSpace(input.Payload)
	if payload == "" {
		payload = "{}"
	}
	if len(payload) > 30000 {
		return domain.PetApproval{}, fmt.Errorf("审批附加数据过大")
	}
	if !json.Valid([]byte(payload)) {
		return domain.PetApproval{}, fmt.Errorf("审批附加数据必须是合法 JSON")
	}
	if risk == "high" && reason == "" {
		reason = summary
	}
	return domain.PetApproval{
		ID: domain.NewID("pet_approval"), TaskID: petTaskID, Kind: kind, Title: title,
		Summary: summary, Reason: reason, Risk: risk, Payload: payload, Status: petApprovalPending, CreatedAt: s.now().UTC(),
	}, nil
}

func (s *Service) afterPetApprovalCreated(ctx context.Context, item domain.PetApproval) {
	s.publishPetApproval(item)
	_ = s.store.AppendPetEvent(ctx, domain.PetEvent{
		ID: domain.NewID("pet_event"), At: item.CreatedAt, Actor: "pet", Kind: "approval_request",
		Detail: item.Summary, Status: petApprovalPending,
	})
}

// ResolvePetApproval records the owner's decision and, when possible, wakes the
// pet Task with the result so the next Turn can continue the work. Approving a
// heavy delete claims the approval and returns immediately with status
// "executing"; the caller serves 202 while the delete runs in the background.
func (s *Service) ResolvePetApproval(ctx context.Context, id, decision, note string) (domain.PetApproval, error) {
	return s.resolvePetApproval(ctx, id, decision, note, nil)
}

// ResolvePetApprovalBeforeExecution is the HTTP path for a heavy delete: the
// callback runs after the claim commits but before the background transaction
// starts, so the handler's audit insert does not queue behind the long delete.
func (s *Service) ResolvePetApprovalBeforeExecution(ctx context.Context, id, decision, note string, beforeStart func(domain.PetApproval)) (domain.PetApproval, error) {
	return s.resolvePetApproval(ctx, id, decision, note, beforeStart)
}

func (s *Service) resolvePetApproval(ctx context.Context, id, decision, note string, beforeStart func(domain.PetApproval)) (domain.PetApproval, error) {
	id = strings.TrimSpace(id)
	decision = strings.ToLower(strings.TrimSpace(decision))
	var status string
	switch decision {
	case "approve", "approved":
		status = petApprovalApproved
	case "reject", "rejected":
		status = petApprovalRejected
	case "cancel", "cancelled":
		// Cancelling keeps the record for history but removes it from the
		// pending queue. It never executes the requested action.
		status = petApprovalCancelled
	default:
		return domain.PetApproval{}, fmt.Errorf("审批决定必须是 approve、reject 或 cancel")
	}
	item, err := s.store.PetApproval(ctx, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return domain.PetApproval{}, fmt.Errorf("审批不存在")
		}
		return domain.PetApproval{}, err
	}
	if item.Status != petApprovalPending {
		return domain.PetApproval{}, fmt.Errorf("审批已经处理过")
	}
	if status == petApprovalRejected && strings.TrimSpace(note) == "" {
		return domain.PetApproval{}, fmt.Errorf("decision_reason 不能为空")
	}
	if status == petApprovalApproved {
		if petApprovalRunsInBackground(item.Kind) {
			return s.claimPetApprovalExecution(ctx, item, strings.TrimSpace(note), beforeStart)
		}
		if err := s.executePetApproval(ctx, item); err != nil {
			return domain.PetApproval{}, fmt.Errorf("审批已批准但执行失败：%w", err)
		}
	}
	now := s.now().UTC()
	if err := s.store.ResolvePetApproval(ctx, id, status, strings.TrimSpace(note), now, strings.TrimSpace(note)); err != nil {
		if err == sql.ErrNoRows {
			return domain.PetApproval{}, fmt.Errorf("审批已经处理过")
		}
		return domain.PetApproval{}, err
	}
	item.Status = status
	item.Resolution = strings.TrimSpace(note)
	item.DecisionReason = strings.TrimSpace(note)
	item.ResolvedAt = now
	s.finishPetApprovalResolution(ctx, item)
	return item, nil
}

// claimPetApprovalExecution atomically takes ownership of a heavy approval and
// starts its background execution. Only the claim uses the request context;
// the execution itself is detached from it below.
func (s *Service) claimPetApprovalExecution(ctx context.Context, item domain.PetApproval, note string, beforeStart func(domain.PetApproval)) (domain.PetApproval, error) {
	started := time.Now()
	claimed, err := s.store.ClaimPetApprovalExecution(ctx, item.ID)
	if err != nil {
		return domain.PetApproval{}, err
	}
	claimDuration := time.Since(started)
	if !claimed {
		return domain.PetApproval{}, fmt.Errorf("审批已经处理过")
	}
	item.Status = petApprovalExecuting
	item.Resolution = ""
	item.DecisionReason = ""
	item.ResolvedAt = time.Time{}
	// Publish the claimed state before the worker starts so a fast execution
	// cannot publish "approved" first and then be overwritten by "executing".
	s.publishPetApproval(item)
	if beforeStart != nil {
		// Runs while no background transaction is open yet; handlers use it to
		// record their own writes without waiting on the long delete lock.
		beforeStart(item)
	}
	if err := s.startPetApprovalExecution(item, note, claimDuration); err != nil {
		// The claim must never outlive a service that is no longer willing to
		// run it: put the row back so the owner can retry after restart.
		_ = s.failPetApprovalExecution(item, "服务正在关闭，审批未执行，请重试")
		return domain.PetApproval{}, err
	}
	log.Printf("pet approval %s (%s): claimed in %s", item.ID, item.Kind, claimDuration)
	return item, nil
}

func (s *Service) startPetApprovalExecution(item domain.PetApproval, note string, claimDuration time.Duration) error {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return fmt.Errorf("服务正在关闭，请稍后重试")
	}
	parent := s.runContext
	// Registering under the same lock Shutdown uses keeps Add from racing a
	// Wait that already saw a zero counter.
	s.runWG.Add(1)
	s.mu.Unlock()
	if parent == nil {
		parent = context.Background()
	}
	budget := s.petApprovalExecutionTimeout
	if budget <= 0 {
		budget = petApprovalExecutionBudgetDefault
	}
	go func() {
		defer s.runWG.Done()
		// Rooted in the service lifetime, not the HTTP request, plus an
		// explicit budget: a client disconnect cannot cancel a claimed delete.
		ctx, cancel := context.WithTimeout(parent, budget)
		defer cancel()
		s.runPetApprovalExecution(ctx, item, note, claimDuration)
	}()
	return nil
}

func (s *Service) runPetApprovalExecution(ctx context.Context, item domain.PetApproval, note string, claimDuration time.Duration) {
	executionStarted := time.Now()
	var execErr error
	if hook := s.petApprovalExecutionHook; hook != nil {
		execErr = hook(ctx, item)
	} else {
		execErr = s.executePetApproval(ctx, item)
	}
	executionDuration := time.Since(executionStarted)
	if execErr != nil {
		failure := "执行失败：" + execErr.Error()
		commitStarted := time.Now()
		finalizeErr := s.failPetApprovalExecution(item, failure)
		commitDuration := time.Since(commitStarted)
		// The full error stays in the log; the owner sees the same text on the
		// approval card because the row went back to pending.
		log.Printf("pet approval %s (%s) execution failed: claim=%s execute=%s commit=%s error=%v finalize_error=%v",
			item.ID, item.Kind, claimDuration, executionDuration, commitDuration, execErr, finalizeErr)
		return
	}
	commitStarted := time.Now()
	now := s.now().UTC()
	finalizeCtx, cancel := context.WithTimeout(context.Background(), petApprovalFinalizeTimeoutDefault)
	err := s.store.CompletePetApprovalExecution(finalizeCtx, item.ID, note, now)
	commitDuration := time.Since(commitStarted)
	if err != nil {
		cancel()
		// The row stays executing and the next service start restores it to
		// pending, so the delete is never reported as approved without its
		// transaction having committed.
		log.Printf("pet approval %s (%s): execution succeeded but status update failed: claim=%s execute=%s commit=%s error=%v",
			item.ID, item.Kind, claimDuration, executionDuration, commitDuration, err)
		return
	}
	item.Status = petApprovalApproved
	item.Resolution = note
	item.DecisionReason = note
	item.ResolvedAt = now
	cleanupStarted := time.Now()
	s.finishPetApprovalResolution(finalizeCtx, item)
	cleanupDuration := time.Since(cleanupStarted)
	cancel()
	log.Printf("pet approval %s (%s): claim=%s execute=%s commit=%s cleanup=%s",
		item.ID, item.Kind, claimDuration, executionDuration, commitDuration, cleanupDuration)
}

// failPetApprovalExecution returns the approval to pending with the error text.
// It uses a fresh context because the execution context may already be
// canceled (shutdown or timeout) when the transaction rolled back.
func (s *Service) failPetApprovalExecution(item domain.PetApproval, failure string) error {
	ctx, cancel := context.WithTimeout(context.Background(), petApprovalFinalizeTimeoutDefault)
	defer cancel()
	if err := s.store.FailPetApprovalExecution(ctx, item.ID, failure); err != nil {
		return err
	}
	item.Status = petApprovalPending
	item.Resolution = failure
	item.DecisionReason = failure
	item.ResolvedAt = time.Time{}
	s.publishPetApproval(item)
	return nil
}

// finishPetApprovalResolution publishes the resolved approval and wakes the pet
// Task. Both the synchronous and the background path share it so the pet keeps
// receiving the same approval-result message.
func (s *Service) finishPetApprovalResolution(ctx context.Context, item domain.PetApproval) {
	s.publishPetApproval(item)
	_ = s.store.AppendPetEvent(ctx, domain.PetEvent{
		ID: domain.NewID("pet_event"), At: item.ResolvedAt, Actor: "owner", Kind: "approval_resolved",
		Detail: item.Title, Status: item.Status,
	})
	message := ""
	if s.petUsesEnglish(ctx) {
		decisionText := "approved"
		switch item.Status {
		case petApprovalRejected:
			decisionText = "rejected"
		case petApprovalCancelled:
			decisionText = "cancelled"
		}
		message = fmt.Sprintf("[Approval result] The owner %s the approval「%s」（%s）.", decisionText, item.Title, item.ID)
		if item.Resolution != "" {
			message += " Note: " + item.Resolution
		}
	} else {
		decisionText := "批准"
		switch item.Status {
		case petApprovalRejected:
			decisionText = "拒绝"
		case petApprovalCancelled:
			decisionText = "取消"
		}
		message = fmt.Sprintf("[审批结果] 主人%s了审批「%s」（%s）。", decisionText, item.Title, item.ID)
		if item.Resolution != "" {
			message += " 备注：" + item.Resolution
		}
	}
	if _, err := s.SubmitMessage(ctx, petTaskID, message); err != nil {
		log.Printf("notify pet task about approval %s: %v", item.ID, err)
	}
}

// petApprovalPayload is the union of fields used by the approval kinds the
// pet can request. Keep new fields here so execution stays a single decode.
type petApprovalPayload struct {
	TaskID                 string   `json:"task_id"`
	ProjectID              string   `json:"project_id"`
	WorkspaceID            string   `json:"workspace_id"`
	KnowledgeID            string   `json:"knowledge_id"`
	SkillID                string   `json:"skill_id"`
	ModelID                string   `json:"model_id"`
	ModelIDs               []string `json:"model_ids"`
	ProviderID             string   `json:"provider_id"`
	EnvGroupID             string   `json:"env_group_id"`
	ResourceID             string   `json:"resource_id"`
	Kind                   string   `json:"kind"`
	TemplateID             string   `json:"template_id"`
	Content                string   `json:"content"`
	InstanceID             string   `json:"instance_id"`
	Enabled                bool     `json:"enabled"`
	Revision               int      `json:"revision"`
	ValidateOrigin         bool     `json:"validate_origin"`
	AccessScope            string   `json:"access_scope"`
	Permission             string   `json:"permission"`
	RequestID              string   `json:"request_id"`
	TurnID                 string   `json:"turn_id"`
	Purpose                string   `json:"purpose"`
	Message                string   `json:"message"`
	MentionIdentityLinkIDs []string `json:"mention_identity_link_ids"`
	AttachmentIDs          []string `json:"attachment_ids"`
}

func (s *Service) executePetApproval(ctx context.Context, item domain.PetApproval) error {
	// MCP approvals carry their own payload shapes. Decoding them through the
	// generic union would make unrelated owner_decision payloads fail whenever
	// a same-named key has another type, so keep the paths separate.
	if isPetMCPApprovalKind(item.Kind) {
		return s.executePetMCPApproval(ctx, item)
	}
	var payload petApprovalPayload
	if strings.TrimSpace(item.Payload) != "" {
		if err := json.Unmarshal([]byte(item.Payload), &payload); err != nil {
			return fmt.Errorf("审批参数无效")
		}
	}
	switch item.Kind {
	case "delete_task":
		return s.deleteTaskNow(ctx, payload.TaskID)
	case "delete_project":
		return s.deleteProjectNow(ctx, payload.ProjectID)
	case "delete_workspace":
		return s.deleteWorkspaceNow(ctx, payload.WorkspaceID)
	case "delete_model":
		modelID := strings.TrimSpace(payload.ModelID)
		cleanup, err := s.deleteModelNow(ctx, modelID)
		if err != nil {
			return err
		}
		s.recordPetWork(ctx, "model_deleted", modelID, "approved")
		s.auditModelDeleteApproval(ctx, "model.delete", "model", modelID, item.ID, []string{modelID}, cleanup)
		return nil
	case "delete_models":
		modelIDs := normalizeModelIDs(payload.ModelIDs)
		cleanup, err := s.deleteModelsNow(ctx, modelIDs)
		if err != nil {
			return err
		}
		for _, modelID := range modelIDs {
			s.recordPetWork(ctx, "model_deleted", modelID, "approved")
		}
		s.auditModelDeleteApproval(ctx, "model.batch_delete", "model_batch", item.ID, item.ID, modelIDs, cleanup)
		return nil
	case "delete_provider":
		if err := s.deleteProviderNow(ctx, payload.ProviderID); err != nil {
			return err
		}
		s.recordPetWork(ctx, "provider_deleted", payload.ProviderID, "approved")
		return nil
	case "delete_env_group":
		if err := s.deleteEnvGroupNow(ctx, payload.EnvGroupID); err != nil {
			return err
		}
		s.recordPetWork(ctx, "env_group_deleted", payload.EnvGroupID, "approved")
		return nil
	case "delete_hardware_resource":
		if err := s.deleteHardwareResourceNow(ctx, payload.ResourceID); err != nil {
			return err
		}
		s.recordPetWork(ctx, "hardware_resource_deleted", payload.ResourceID, "approved")
		return nil
	case "delete_mail_resource":
		if err := s.deleteMailResourceNow(ctx, payload.Kind, payload.ResourceID); err != nil {
			return err
		}
		s.auditPetMailResource(ctx, "pet.mail_resource.delete", map[string]any{
			"kind": strings.ToLower(strings.TrimSpace(payload.Kind)), "resource_id": payload.ResourceID,
		})
		s.recordPetWork(ctx, "mail_resource_deleted", payload.ResourceID, "approved")
		return nil
	case "delete_knowledge":
		if err := s.store.DeleteKnowledgeAndPruneEmptyAHA1Parents(ctx, payload.KnowledgeID); err != nil {
			return err
		}
		s.recordPetWork(ctx, "knowledge_deleted", payload.KnowledgeID, "approved")
		return nil
	case "delete_skill":
		if err := s.store.DeleteSkill(ctx, payload.SkillID); err != nil {
			return err
		}
		s.recordPetWork(ctx, "skill_deleted", payload.SkillID, "approved")
		return nil
	case "update_prompt_template":
		if petPromptTemplateProtected(payload.TemplateID) {
			return fmt.Errorf("该 Prompt 模板受保护，宠物不能修改")
		}
		if err := s.UpdatePromptTemplate(ctx, payload.TemplateID, payload.Content); err != nil {
			return err
		}
		s.recordPetWork(ctx, "prompt_updated", payload.TemplateID, "approved")
		return nil
	case "external_channel_send":
		if s.petChannels == nil {
			return fmt.Errorf("渠道运行时不可用")
		}
		delivery, err := s.petChannels.SendPetTaskChannelMessage(
			ctx, payload.TaskID, payload.TurnID, payload.RequestID, payload.Purpose,
			payload.Message, payload.MentionIdentityLinkIDs, payload.AttachmentIDs, s.now().UTC(),
		)
		if err != nil {
			return err
		}
		s.recordPetWork(ctx, "channel_message_sent", payload.TaskID, delivery.ID)
		return nil
	case "channel_set_enabled":
		if s.petChannels == nil {
			return fmt.Errorf("渠道运行时不可用")
		}
		ownerID, err := s.store.PrimaryOwnerID(ctx)
		if err != nil {
			return fmt.Errorf("本机还没有 Owner 账号")
		}
		if _, err := s.petChannels.SetInstanceEnabled(ctx, ownerID, payload.InstanceID, payload.Enabled, payload.Revision); err != nil {
			return err
		}
		status := "disabled"
		if payload.Enabled {
			status = "enabled"
		}
		s.recordPetWork(ctx, "channel_"+status, payload.InstanceID, status)
		return nil
	case "channel_reset_binding", "channel_archive", "channel_purge":
		if s.petChannels == nil {
			return fmt.Errorf("渠道运行时不可用")
		}
		ownerID, err := s.store.PrimaryOwnerID(ctx)
		if err != nil {
			return fmt.Errorf("本机还没有 Owner 账号")
		}
		var operationErr error
		switch item.Kind {
		case "channel_reset_binding":
			_, operationErr = s.petChannels.ResetBinding(ctx, ownerID, payload.InstanceID, payload.Revision)
		case "channel_archive":
			_, operationErr = s.petChannels.ArchiveInstance(ctx, ownerID, payload.InstanceID, payload.Revision)
		case "channel_purge":
			_, operationErr = s.petChannels.PurgeInstance(ctx, ownerID, payload.InstanceID, payload.Revision)
		}
		if operationErr != nil {
			return operationErr
		}
		s.recordPetWork(ctx, item.Kind, payload.InstanceID, "approved")
		return nil
	case "security_settings_update":
		if s.petSecurity == nil {
			return fmt.Errorf("安全设置应用器不可用")
		}
		if _, err := s.petSecurity.ApplyPetSecuritySettings(ctx, payload.ValidateOrigin, payload.AccessScope); err != nil {
			return err
		}
		s.recordPetWork(ctx, "security_settings_updated", "security", payload.AccessScope)
		return nil
	case "permission_change":
		return s.applyPetPermissionChange(ctx, payload.Permission, payload.Enabled)
	default:
		return nil
	}
}

func (s *Service) publishPetApproval(item domain.PetApproval) {
	s.hub.Publish("", domain.Event{
		ID: domain.NewID("pet_event"), AggregateType: "pet", AggregateID: "pet_approval",
		Type: "pet_approval", OccurredAt: s.now().UTC(),
		Data: map[string]any{"approval": item},
	})
}

type PetTaskBrief struct {
	ID        string `json:"id"`
	Code      string `json:"code,omitempty"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Project   string `json:"project,omitempty"`
	Workspace string `json:"workspace,omitempty"`
	Goal      string `json:"goal,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type PetAHAOverview struct {
	Projects         int            `json:"projects"`
	Workspaces       int            `json:"workspaces"`
	Tasks            int            `json:"tasks"`
	Active           int            `json:"active"`
	WaitingUser      int            `json:"waiting_user"`
	Failed           int            `json:"failed"`
	Blocked          int            `json:"blocked"`
	DegradedChannels []string       `json:"degraded_channels,omitempty"`
	Attention        []PetTaskBrief `json:"attention,omitempty"`
	Note             string         `json:"note,omitempty"`
}

// PetAHAOverview is the pet's read-only view of AHA2. Aggregate counts are
// always available; task titles and goals require the owner's view switch.
func (s *Service) PetAHAOverview(ctx context.Context) (PetAHAOverview, error) {
	assistant, err := s.store.EnsureOwnerAssistant(ctx, s.now().UTC())
	if err != nil {
		return PetAHAOverview{}, err
	}
	projects, _ := s.store.ListProjects(ctx)
	workspaces, _ := s.store.ListWorkspaces(ctx, "")
	tasks, err := s.store.ListTasks(ctx, "")
	if err != nil {
		return PetAHAOverview{}, err
	}
	overview := PetAHAOverview{Projects: len(projects), Workspaces: len(workspaces), Tasks: len(tasks)}
	projectNames := make(map[string]string, len(projects))
	for _, project := range projects {
		projectNames[project.ID] = project.Name
	}
	workspaceNames := make(map[string]string, len(workspaces))
	for _, workspace := range workspaces {
		workspaceNames[workspace.ID] = workspace.Name
	}
	attention := make([]domain.Task, 0)
	for _, task := range tasks {
		switch task.Status {
		case domain.TaskActive:
			overview.Active++
		case domain.TaskWaitingUser:
			overview.WaitingUser++
		case domain.TaskFailed:
			overview.Failed++
		case domain.TaskBlocked:
			overview.Blocked++
		}
		if task.ID == petTaskID {
			continue
		}
		switch task.Status {
		case domain.TaskFailed, domain.TaskWaitingUser, domain.TaskBlocked, domain.TaskActive:
			attention = append(attention, task)
		}
	}
	channels, _ := s.store.ChannelInstances(ctx, "")
	for _, channel := range channels {
		if channel.Retired || (channel.Status != "degraded" && channel.Status != "error") {
			continue
		}
		overview.DegradedChannels = append(overview.DegradedChannels, channel.Name+"（"+channel.Status+"）")
	}
	if !assistant.PetAllowViewTasks {
		overview.Note = "主人未开启查看其他任务信息权限；这里只返回汇总数量。"
		return overview, nil
	}
	sort.SliceStable(attention, func(i, j int) bool { return attention[i].UpdatedAt.After(attention[j].UpdatedAt) })
	if len(attention) > 20 {
		attention = attention[:20]
	}
	for _, task := range attention {
		goal := strings.TrimSpace(task.CurrentGoal)
		if goal == "" {
			goal = task.Title
		}
		overview.Attention = append(overview.Attention, PetTaskBrief{
			ID: task.ID, Code: task.Code, Title: task.Title, Status: string(task.Status),
			Project: projectNames[task.ProjectID], Workspace: workspaceNames[task.WorkspaceID],
			Goal: truncateAssistantText(goal, 240), UpdatedAt: task.UpdatedAt.Format(time.RFC3339),
		})
	}
	return overview, nil
}

type PetRemoteHardwareBrief struct {
	ID                 string                       `json:"id"`
	Description        string                       `json:"description,omitempty"`
	Mode               string                       `json:"mode"`
	Serial             domain.HardwareSerialConfig  `json:"serial"`
	Network            domain.HardwareNetworkConfig `json:"network"`
	Username           string                       `json:"username,omitempty"`
	PasswordConfigured bool                         `json:"password_configured"`
}

type PetTaskDetail struct {
	domain.TaskReviewDecision
	ID            string                   `json:"id"`
	Code          string                   `json:"code,omitempty"`
	Title         string                   `json:"title"`
	Status        string                   `json:"status"`
	Project       string                   `json:"project,omitempty"`
	Workspace     string                   `json:"workspace,omitempty"`
	CurrentGoal   string                   `json:"current_goal,omitempty"`
	OriginalGoal  string                   `json:"original_request,omitempty"`
	UpdatedAt     string                   `json:"updated_at,omitempty"`
	LastTurn      string                   `json:"last_turn_status,omitempty"`
	LastError     string                   `json:"last_error,omitempty"`
	LastResult    string                   `json:"last_result,omitempty"`
	RecentTurns   int                      `json:"recent_turns"`
	ReadOnly      bool                     `json:"read_only,omitempty"`
	OwnerDeviceID string                   `json:"owner_device_id,omitempty"`
	Hardware      []PetRemoteHardwareBrief `json:"hardware,omitempty"`
}

// PetTaskDetail exposes one task to the pet only when the owner enabled the
// view-tasks permission. The result is bounded: no prompts, secrets, or full
// transcripts.
func (s *Service) PetTaskDetail(ctx context.Context, taskID string) (PetTaskDetail, error) {
	assistant, err := s.store.EnsureOwnerAssistant(ctx, s.now().UTC())
	if err != nil {
		return PetTaskDetail{}, err
	}
	if !assistant.PetAllowViewTasks {
		return PetTaskDetail{}, fmt.Errorf("主人未开启查看其他任务信息权限")
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return PetTaskDetail{}, fmt.Errorf("task_id 不能为空")
	}
	task, err := s.store.Task(ctx, taskID)
	var remote store.RemoteTaskMirror
	if err != nil {
		remote, err = s.store.RemoteTaskDetailMirror(ctx, taskID)
		if err != nil {
			return PetTaskDetail{}, fmt.Errorf("Task 不存在")
		}
		task = remote.Task
	}
	detail := PetTaskDetail{
		TaskReviewDecision: task.TaskReviewDecision,
		ID:                 task.ID, Code: task.Code, Title: task.Title, Status: string(task.Status),
		CurrentGoal:  truncateAssistantText(task.CurrentGoal, 1200),
		OriginalGoal: truncateAssistantText(task.OriginalRequest, 1200),
		UpdatedAt:    task.UpdatedAt.Format(time.RFC3339),
		ReadOnly:     task.ReadOnly, OwnerDeviceID: task.OwnerDeviceID,
	}
	for _, group := range remote.Hardware {
		detail.Hardware = append(detail.Hardware, PetRemoteHardwareBrief{
			ID: group.ID, Description: group.Description, Mode: group.Mode,
			Serial: group.Serial, Network: group.Network, Username: group.Username,
			PasswordConfigured: group.PasswordConfigured,
		})
	}
	if project, projectErr := s.store.Project(ctx, task.ProjectID); projectErr == nil {
		detail.Project = project.Name
	}
	if workspace, workspaceErr := s.store.Workspace(ctx, task.WorkspaceID); workspaceErr == nil {
		detail.Workspace = workspace.Name
	}
	turns, err := s.store.ListTurns(ctx, task.ID)
	if err == nil && len(turns) > 0 {
		detail.RecentTurns = len(turns)
		last := turns[len(turns)-1]
		detail.LastTurn = string(last.Status)
		detail.LastError = truncateAssistantText(last.Error, 1200)
		detail.LastResult = truncateAssistantText(last.Result, 2000)
	}
	return detail, nil
}

func (s *Service) PetPendingApprovalCount(ctx context.Context) (int, error) {
	return s.store.CountPendingPetApprovals(ctx)
}
