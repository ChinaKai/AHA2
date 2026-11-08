package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// PetRequestCodeReviewInput asks for an independent review Task using a model
// the owner considers strong enough to approve code changes. The run settings
// must be supplied at creation time because permission and Skill changes do not
// apply to a Review Task Turn that is already running.
type PetRequestCodeReviewInput struct {
	TaskID          string   `json:"task_id"`
	ReviewModelID   string   `json:"review_model_id"`
	Focus           string   `json:"focus"`
	Filesystem      string   `json:"filesystem"`
	ReasoningEffort string   `json:"reasoning_effort"`
	SkillIDs        []string `json:"skill_ids"`
}

// PetCodeReviewResult keeps the created Task's fields flat in MCP/JSON output
// while surfacing non-fatal setup notes, such as a missing auto-attach Skill.
type PetCodeReviewResult struct {
	domain.Task
	Warnings []string `json:"warnings,omitempty"`
}

const petCodeReviewApprove = "VERDICT: APPROVE"
const petCodeReviewRequestChanges = "VERDICT: REQUEST_CHANGES"

const (
	petCodeReviewFilesystem      = "read-only"
	petCodeReviewReasoningEffort = "high"
	petCodeReviewTieredSkillName = "tiered-code-review"
)

func (s *Service) PetRequestCodeReview(ctx context.Context, input PetRequestCodeReviewInput) (PetCodeReviewResult, error) {
	assistant, err := s.petAssistant(ctx)
	if err != nil {
		return PetCodeReviewResult{}, err
	}
	if !assistant.PetAllowCreateTasks {
		return PetCodeReviewResult{}, fmt.Errorf("主人未开启主动创建任务权限")
	}
	sourceID := strings.TrimSpace(input.TaskID)
	if sourceID == "" || sourceID == petTaskID {
		return PetCodeReviewResult{}, fmt.Errorf("task_id 无效")
	}
	source, err := s.store.Task(ctx, sourceID)
	if err != nil {
		return PetCodeReviewResult{}, fmt.Errorf("Task 不存在")
	}
	project, err := s.store.Project(ctx, source.ProjectID)
	if err != nil {
		return PetCodeReviewResult{}, fmt.Errorf("Task 所属项目不存在")
	}
	if domain.IsSystemManagedProject(project) {
		return PetCodeReviewResult{}, fmt.Errorf("系统托管任务不能创建代码评审")
	}
	reviewModelID := strings.TrimSpace(input.ReviewModelID)
	if reviewModelID == "" {
		return PetCodeReviewResult{}, fmt.Errorf("review_model_id 不能为空；请选择更强的评审模型")
	}
	reviewModel, err := s.store.Model(ctx, reviewModelID)
	if err != nil {
		return PetCodeReviewResult{}, fmt.Errorf("评审模型不存在")
	}
	workspace, err := s.store.Workspace(ctx, source.WorkspaceID)
	if err != nil {
		return PetCodeReviewResult{}, fmt.Errorf("Task 的 Workspace 不存在")
	}
	if workspace.ReadOnly {
		return PetCodeReviewResult{}, fmt.Errorf("Task 的 Workspace 只读，不能创建评审任务")
	}
	filesystem := strings.TrimSpace(input.Filesystem)
	if filesystem == "" {
		filesystem = petCodeReviewFilesystem
	}
	switch filesystem {
	case "read-only", "workspace-write", "danger-full-access":
	default:
		return PetCodeReviewResult{}, fmt.Errorf("filesystem 必须是 read-only、workspace-write 或 danger-full-access")
	}
	reasoningEffort := strings.ToLower(strings.TrimSpace(input.ReasoningEffort))
	if reasoningEffort == "" {
		reasoningEffort = petCodeReviewReasoningEffort
	}
	switch reasoningEffort {
	case "high", "xhigh", "max":
	default:
		return PetCodeReviewResult{}, fmt.Errorf("评审 reasoning_effort 必须是 high、xhigh 或 max")
	}
	sourceAgent := "main"
	sourceModelID := ""
	if agents, agentErr := s.TaskAgents(ctx, source.ID); agentErr == nil && len(agents) > 0 {
		sourceAgent = agents[0].AgentID
		sourceModelID = agents[0].ModelID
	}
	if sourceModelID == "" {
		return PetCodeReviewResult{}, fmt.Errorf("无法读取源 Task 模型，不能创建独立评审")
	}
	if sourceModelID == reviewModel.ID {
		return PetCodeReviewResult{}, fmt.Errorf("评审模型必须与源 Task 模型不同，避免同模型自审")
	}
	skillIDs, warning, err := s.petCodeReviewSkillIDs(ctx, project.ID, input.SkillIDs)
	if err != nil {
		return PetCodeReviewResult{}, err
	}
	request := petCodeReviewRequest(source, input.Focus, sourceAgent)
	title := truncateAssistantText("代码评审："+source.Code+" "+source.Title, 120)
	review, err := s.CreateTask(ctx, CreateTaskInput{
		ProjectID: source.ProjectID, WorkspaceID: source.WorkspaceID, Title: title, Request: request,
		ModelID: reviewModel.ID, ReasoningEffort: reasoningEffort, Filesystem: filesystem, Approval: "never",
		CollaborationMode: "single", MaxAgents: 1, KnowledgePolicy: project.KnowledgePolicy,
		Isolation: "inplace", StartMode: "manual", SkillIDs: skillIDs,
	})
	if err != nil {
		return PetCodeReviewResult{}, err
	}
	if err := s.store.CreatePetCodeReviewLink(ctx, review.ID, source.ID); err != nil {
		return PetCodeReviewResult{Task: review}, fmt.Errorf("Review Task 已创建但关联失败，未启动：%w", err)
	}
	s.watchPetTask(ctx, review.ID, "review")
	started, err := s.StartTask(ctx, review.ID)
	if err != nil {
		result := PetCodeReviewResult{Task: review}
		if warning != "" {
			result.Warnings = []string{warning}
		}
		return result, fmt.Errorf("Review Task 已创建但启动失败：%w", err)
	}
	review = started
	if _, err := s.SubmitPetDelegatedMessage(ctx, review.ID, request); err != nil {
		result := PetCodeReviewResult{Task: review}
		if warning != "" {
			result.Warnings = []string{warning}
		}
		return result, fmt.Errorf("Review Task 已启动但首条消息提交失败：%w", err)
	}
	s.recordPetWork(ctx, "task_created", review.ID, string(review.Status))
	result := PetCodeReviewResult{Task: review}
	if warning != "" {
		result.Warnings = []string{warning}
	}
	return result, nil
}

// petCodeReviewSkillIDs keeps explicitly requested Skills as-is. When none are
// requested it looks up the active tiered-code-review Skill available to the
// source project; a missing Skill is reported as a warning instead of being
// fabricated, so the Review Task can still run.
func (s *Service) petCodeReviewSkillIDs(ctx context.Context, projectID string, requested []string) ([]string, string, error) {
	ids := make([]string, 0, len(requested))
	for _, id := range requested {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) > 0 {
		return ids, "", nil
	}
	items, err := s.store.ListSkills(ctx, "", "", true)
	if err != nil {
		return nil, "", fmt.Errorf("查找评审 Skill 失败：%w", err)
	}
	globalID := ""
	for _, item := range items {
		if !strings.EqualFold(strings.TrimSpace(item.Name), petCodeReviewTieredSkillName) ||
			(item.Scope != "global" && !(item.Scope == "project" && item.ProjectID == projectID)) {
			continue
		}
		if item.Scope == "project" {
			return []string{item.ID}, "", nil
		}
		if item.Scope == "global" && globalID == "" {
			globalID = item.ID
		}
	}
	if globalID != "" {
		return []string{globalID}, "", nil
	}
	return nil, fmt.Sprintf("未找到可用的 %s Skill，Review Task 没有附加该 Skill", petCodeReviewTieredSkillName), nil
}

func petCodeReviewRequest(source domain.Task, focus, sourceAgent string) string {
	var b strings.Builder
	b.WriteString("你是独立的高级代码评审 Agent。可以运行 git diff、测试和构建，但不得修改、提交、合并或部署源码。\n")
	fmt.Fprintf(&b, "源 Task：%s「%s」（%s，agent=%s）\n", source.Code, source.Title, source.ID, sourceAgent)
	if strings.TrimSpace(source.TaskBranch) != "" {
		fmt.Fprintf(&b, "源分支：%s\n", source.TaskBranch)
	}
	if strings.TrimSpace(source.TargetBranch) != "" {
		fmt.Fprintf(&b, "目标分支：%s\n", source.TargetBranch)
	}
	if strings.TrimSpace(source.WorktreeDir) != "" {
		fmt.Fprintf(&b, "源 Worktree：%s\n", source.WorktreeDir)
	} else {
		b.WriteString("源 Task 原地执行，直接检查当前 Workspace 的改动。\n")
	}
	if strings.TrimSpace(focus) != "" {
		fmt.Fprintf(&b, "主人额外关注：%s\n", strings.TrimSpace(focus))
	}
	b.WriteString("\n请检查源 Task 的完整改动（git diff、变更文件、测试和构建证据），重点看：正确性、回归风险、测试覆盖、安全与数据风险。\n")
	b.WriteString("最终结果第一行必须是以下二选一：\n")
	b.WriteString("Verdict: APPROVE\n")
	b.WriteString("Verdict: REQUEST_CHANGES\n")
	b.WriteString("随后给出证据和必须修复项。没有明确 APPROVE 不得通过；不要只复述源码。\n")
	return b.String()
}

// petCodeReviewRequired reports whether a task is write-capable and therefore
// needs an independent review before the pet may mark it complete.
func petCodeReviewRequired(agents []domain.TaskAgent) bool {
	if len(agents) == 0 {
		return true
	}
	switch strings.TrimSpace(agents[0].Filesystem) {
	case "read-only":
		return false
	default:
		return true
	}
}

// petCodeReviewVerdict accepts only a complete verdict on the first non-empty
// line, never a quoted example or a verdict-like prefix elsewhere in the result.
func petCodeReviewVerdict(result string) string {
	for _, line := range strings.Split(result, "\n") {
		line = strings.ToUpper(strings.TrimSpace(line))
		if line == "" {
			continue
		}
		switch line {
		case petCodeReviewApprove, petCodeReviewRequestChanges:
			return line
		default:
			return ""
		}
	}
	return ""
}

func (s *Service) requirePetCodeReview(ctx context.Context, source domain.Task, reviewTaskID string) error {
	if _, err := s.store.PetCodeReviewSource(ctx, source.ID); err == nil {
		// Finishing an actual review is not approval of its source. In particular,
		// REQUEST_CHANGES may finish the review but must still block the source.
		if source.Status != domain.TaskWaitingUser && source.Status != domain.TaskCompleted {
			return fmt.Errorf("Review Task 尚未结束评审执行")
		}
		turn, err := s.store.LatestTurnForAgent(ctx, source.ID, "main")
		if err != nil || turn.Status != domain.TurnSucceeded {
			return fmt.Errorf("Review Task 没有成功的评审结果")
		}
		if petCodeReviewVerdict(turn.Result) == "" {
			return fmt.Errorf("Review Task 最新结果首个非空行必须是 Verdict: APPROVE 或 Verdict: REQUEST_CHANGES")
		}
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("无法读取 Review Task 关联：%w", err)
	}
	agents, err := s.TaskAgents(ctx, source.ID)
	if err != nil {
		return fmt.Errorf("无法读取源 Task 的权限配置：%w", err)
	}
	if !petCodeReviewRequired(agents) {
		return nil
	}
	reviewTaskID = strings.TrimSpace(reviewTaskID)
	if reviewTaskID == "" || reviewTaskID == source.ID {
		return fmt.Errorf("代码任务完成前必须提供高级模型 Review Task 的 review_task_id")
	}
	review, err := s.store.Task(ctx, reviewTaskID)
	if err != nil {
		return fmt.Errorf("Review Task 不存在")
	}
	if review.ProjectID != source.ProjectID {
		return fmt.Errorf("Review Task 必须与源 Task 属于同一项目")
	}
	linkedSourceID, err := s.store.PetCodeReviewSource(ctx, review.ID)
	if err != nil || linkedSourceID != source.ID {
		return fmt.Errorf("Review Task 未关联到源 Task")
	}
	if review.Status != domain.TaskCompleted {
		return fmt.Errorf("Review Task 尚未完成")
	}
	reviewAgents, err := s.TaskAgents(ctx, review.ID)
	if err != nil || len(reviewAgents) == 0 {
		return fmt.Errorf("无法读取 Review Task 的权限配置")
	}
	// The review must remain an independent, strong review regardless of how
	// the task was created or later edited.
	if len(agents) == 0 {
		return fmt.Errorf("无法读取源 Task 的模型配置")
	}
	sourceModelID := strings.TrimSpace(agents[0].ModelID)
	if sourceModelID == "" || strings.TrimSpace(reviewAgents[0].ModelID) == "" || reviewAgents[0].ModelID == sourceModelID {
		return fmt.Errorf("Review Task 必须使用与源 Task 不同的模型")
	}
	switch strings.TrimSpace(reviewAgents[0].Filesystem) {
	case "read-only", "workspace-write", "danger-full-access":
	default:
		return fmt.Errorf("Review Task 的 filesystem 配置无效")
	}
	switch strings.ToLower(strings.TrimSpace(reviewAgents[0].ReasoningEffort)) {
	case "high", "xhigh", "max":
	default:
		return fmt.Errorf("Review Task 必须使用 high、xhigh 或 max 推理")
	}
	if reviewAgents[0].Approval != "never" {
		return fmt.Errorf("Review Task 必须使用 never 审批")
	}
	if strings.TrimSpace(review.Isolation) != "inplace" {
		return fmt.Errorf("Review Task 必须使用 inplace 隔离")
	}
	turn, err := s.store.LatestTurnForAgent(ctx, review.ID, "main")
	if err != nil || turn.Status != domain.TurnSucceeded {
		return fmt.Errorf("Review Task 没有成功的评审结果")
	}
	result := strings.ToUpper(turn.Result)
	if strings.Contains(result, petCodeReviewRequestChanges) {
		return fmt.Errorf("Review Task 要求修改，不能完成源 Task")
	}
	if petCodeReviewVerdict(turn.Result) != petCodeReviewApprove {
		return fmt.Errorf("Review Task 未给出 Verdict: APPROVE")
	}
	return nil
}
