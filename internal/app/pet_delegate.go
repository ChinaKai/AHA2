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
	petApprovalApproved  = "approved"
	petApprovalRejected  = "rejected"
	petApprovalCancelled = "cancelled"
)

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
	now := s.now().UTC()
	item := domain.PetApproval{
		ID: domain.NewID("pet_approval"), TaskID: petTaskID, Kind: kind, Title: title,
		Summary: summary, Reason: reason, Risk: risk, Payload: payload, Status: petApprovalPending, CreatedAt: now,
	}
	if err := s.store.CreatePetApproval(ctx, item); err != nil {
		return domain.PetApproval{}, err
	}
	s.publishPetApproval(item)
	_ = s.store.AppendPetEvent(ctx, domain.PetEvent{
		ID: domain.NewID("pet_event"), At: now, Actor: "pet", Kind: "approval_request",
		Detail: summary, Status: petApprovalPending,
	})
	return item, nil
}

// ResolvePetApproval records the owner's decision and, when possible, wakes the
// pet Task with the result so the next Turn can continue the work.
func (s *Service) ResolvePetApproval(ctx context.Context, id, decision, note string) (domain.PetApproval, error) {
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
	s.publishPetApproval(item)
	_ = s.store.AppendPetEvent(ctx, domain.PetEvent{
		ID: domain.NewID("pet_event"), At: now, Actor: "owner", Kind: "approval_resolved",
		Detail: item.Title, Status: status,
	})
	message := ""
	if s.petUsesEnglish(ctx) {
		decisionText := "approved"
		switch status {
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
		switch status {
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
	return item, nil
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
		if err := s.deleteModelNow(ctx, payload.ModelID); err != nil {
			return err
		}
		s.recordPetWork(ctx, "model_deleted", payload.ModelID, "approved")
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
