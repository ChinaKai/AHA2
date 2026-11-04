package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// PetRequestCodeReviewInput asks for an independent, read-only review Task using
// a model the owner considers strong enough to approve code changes.
type PetRequestCodeReviewInput struct {
	TaskID        string `json:"task_id"`
	ReviewModelID string `json:"review_model_id"`
	Focus         string `json:"focus"`
}

const petCodeReviewApprove = "VERDICT: APPROVE"
const petCodeReviewRequestChanges = "VERDICT: REQUEST_CHANGES"

func (s *Service) PetRequestCodeReview(ctx context.Context, input PetRequestCodeReviewInput) (domain.Task, error) {
	assistant, err := s.petAssistant(ctx)
	if err != nil {
		return domain.Task{}, err
	}
	if !assistant.PetAllowCreateTasks {
		return domain.Task{}, fmt.Errorf("主人未开启主动创建任务权限")
	}
	sourceID := strings.TrimSpace(input.TaskID)
	if sourceID == "" || sourceID == petTaskID {
		return domain.Task{}, fmt.Errorf("task_id 无效")
	}
	source, err := s.store.Task(ctx, sourceID)
	if err != nil {
		return domain.Task{}, fmt.Errorf("Task 不存在")
	}
	project, err := s.store.Project(ctx, source.ProjectID)
	if err != nil {
		return domain.Task{}, fmt.Errorf("Task 所属项目不存在")
	}
	if domain.IsSystemManagedProject(project) {
		return domain.Task{}, fmt.Errorf("系统托管任务不能创建代码评审")
	}
	reviewModelID := strings.TrimSpace(input.ReviewModelID)
	if reviewModelID == "" {
		return domain.Task{}, fmt.Errorf("review_model_id 不能为空；请选择更强的评审模型")
	}
	reviewModel, err := s.store.Model(ctx, reviewModelID)
	if err != nil {
		return domain.Task{}, fmt.Errorf("评审模型不存在")
	}
	workspace, err := s.store.Workspace(ctx, source.WorkspaceID)
	if err != nil {
		return domain.Task{}, fmt.Errorf("Task 的 Workspace 不存在")
	}
	if workspace.ReadOnly {
		return domain.Task{}, fmt.Errorf("Task 的 Workspace 只读，不能创建评审任务")
	}
	sourceAgent := "main"
	sourceModelID := ""
	if agents, agentErr := s.TaskAgents(ctx, source.ID); agentErr == nil && len(agents) > 0 {
		sourceAgent = agents[0].AgentID
		sourceModelID = agents[0].ModelID
	}
	if sourceModelID != "" && sourceModelID == reviewModel.ID {
		return domain.Task{}, fmt.Errorf("评审模型必须与源 Task 模型不同，避免同模型自审")
	}
	request := petCodeReviewRequest(source, input.Focus, sourceAgent)
	title := truncateAssistantText("代码评审："+source.Code+" "+source.Title, 120)
	review, err := s.CreateTask(ctx, CreateTaskInput{
		ProjectID: source.ProjectID, WorkspaceID: source.WorkspaceID, Title: title, Request: request,
		ModelID: reviewModel.ID, ReasoningEffort: "high", Filesystem: "read-only", Approval: "never",
		CollaborationMode: "single", MaxAgents: 1, KnowledgePolicy: project.KnowledgePolicy,
		Isolation: "inplace", StartMode: "manual",
	})
	if err != nil {
		return domain.Task{}, err
	}
	s.watchPetTask(ctx, review.ID, "review")
	started, err := s.StartTask(ctx, review.ID)
	if err != nil {
		return review, fmt.Errorf("Review Task 已创建但启动失败：%w", err)
	}
	review = started
	if _, err := s.SubmitPetDelegatedMessage(ctx, review.ID, request); err != nil {
		return review, fmt.Errorf("Review Task 已启动但首条消息提交失败：%w", err)
	}
	s.recordPetWork(ctx, "task_created", review.ID, string(review.Status))
	return review, nil
}

func petCodeReviewRequest(source domain.Task, focus, sourceAgent string) string {
	var b strings.Builder
	b.WriteString("你是独立的高级代码评审 Agent。只做只读评审，不修改代码、不执行合并或部署。\n")
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

func (s *Service) requirePetCodeReview(ctx context.Context, source domain.Task, reviewTaskID string) error {
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
	if review.Status != domain.TaskCompleted {
		return fmt.Errorf("Review Task 尚未完成")
	}
	reviewAgents, err := s.TaskAgents(ctx, review.ID)
	if err != nil || len(reviewAgents) == 0 {
		return fmt.Errorf("无法读取 Review Task 的权限配置")
	}
	if reviewAgents[0].Filesystem != "read-only" {
		return fmt.Errorf("Review Task 必须使用 read-only 文件权限")
	}
	switch strings.ToLower(strings.TrimSpace(reviewAgents[0].ReasoningEffort)) {
	case "high", "xhigh", "max":
	default:
		return fmt.Errorf("Review Task 必须使用 high 或更高推理强度")
	}
	turn, err := s.store.LatestTurnForAgent(ctx, review.ID, "main")
	if err != nil || turn.Status != domain.TurnSucceeded {
		return fmt.Errorf("Review Task 没有成功的评审结果")
	}
	result := strings.ToUpper(turn.Result)
	if strings.Contains(result, petCodeReviewRequestChanges) {
		return fmt.Errorf("Review Task 要求修改，不能完成源 Task")
	}
	if !strings.Contains(result, petCodeReviewApprove) {
		return fmt.Errorf("Review Task 未给出 Verdict: APPROVE")
	}
	return nil
}
