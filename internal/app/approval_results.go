package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// Task approval results are owner decisions on channel permission requests and
// mail approvals. The result must reach the Task that asked for the decision,
// but it must never add a second wake-up when that path already submits a
// continuation Turn.
const (
	TaskApprovalKindChannelPermission = "channel_permission"
	TaskApprovalKindMail              = "mail"
)

type TaskApprovalResultInput struct {
	TaskID      string
	Kind        string
	Subject     string
	ReferenceID string
	Decision    string
	Decider     string
	Reason      string
	// Instruction is appended after the result line for approvals that resume
	// work (for example "continue the authorized action"). It stays inside the
	// same message so a resuming approval never wakes the Task twice.
	Instruction string
	At          time.Time
}

// TaskApprovalResultContent renders the message the initiating Task receives.
// The shape mirrors the pet approval result: one bracketed result line with the
// decision, decider, time, and an optional reason.
func TaskApprovalResultContent(input TaskApprovalResultInput) string {
	kind := "审批"
	switch strings.TrimSpace(input.Kind) {
	case TaskApprovalKindChannelPermission:
		kind = "渠道权限申请"
	case TaskApprovalKindMail:
		kind = "邮件"
	}
	subject := strings.TrimSpace(input.Subject)
	if subject == "" {
		subject = "未命名申请"
	}
	if runes := []rune(subject); len(runes) > 160 {
		subject = string(runes[:159]) + "…"
	}
	decision := "处理"
	switch strings.TrimSpace(input.Decision) {
	case "approved", "approve":
		decision = "批准"
	case "rejected", "reject":
		decision = "拒绝"
	case "expired":
		decision = "过期"
	case "cancelled", "canceled", "cancel":
		decision = "取消"
	}
	decisionText := "owner 已" + decision
	decidedBy := ""
	if decision == "过期" {
		decisionText = "申请已过期（owner 未处理）"
	} else {
		decider := strings.TrimSpace(input.Decider)
		if decider == "" {
			decider = "owner"
		}
		decidedBy = "处理人：" + decider + "。"
	}
	at := input.At
	if at.IsZero() {
		at = time.Now().UTC()
	}
	reason := strings.TrimSpace(input.Reason)
	message := fmt.Sprintf(
		"[审批结果] %s「%s」（%s）：%s。%s时间：%s。",
		kind, subject, strings.TrimSpace(input.ReferenceID), decisionText, decidedBy,
		at.Local().Format("2006-01-02 15:04:05"),
	)
	if reason != "" {
		message += " 原因：" + reason
	}
	if instruction := strings.TrimSpace(input.Instruction); instruction != "" {
		message += " " + instruction
	}
	return message
}

// NotifyTaskApprovalResult delivers one owner decision to the Task that
// initiated the request. Active and waiting Tasks get a normal message Turn;
// terminal (or otherwise non-runnable) Tasks only keep a conversation record so
// their state machine is not reopened. Callers must only invoke it after the
// transition that actually changed the approval state, which keeps repeated or
// concurrent decisions from producing a second result.
func (s *Service) NotifyTaskApprovalResult(ctx context.Context, input TaskApprovalResultInput) error {
	taskID := strings.TrimSpace(input.TaskID)
	if taskID == "" {
		return fmt.Errorf("task_id is required")
	}
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		return err
	}
	content := TaskApprovalResultContent(input)
	if !taskAcceptsResultWake(task.Status) {
		return s.recordTaskApprovalResult(ctx, taskID, content, input.At)
	}
	if err := s.submitTaskApprovalWake(ctx, taskID, content); err != nil {
		if errors.Is(err, errPetDisabled) {
			return s.recordTaskApprovalResult(ctx, taskID, content, input.At)
		}
		// A concurrent Complete/Cancel/Block can land between the read and the
		// write. Re-read once and keep the result as history instead of failing
		// or forcing the Task out of its terminal state.
		if current, readErr := s.store.Task(ctx, taskID); readErr == nil && !taskAcceptsResultWake(current.Status) {
			return s.recordTaskApprovalResult(ctx, taskID, content, input.At)
		}
		return err
	}
	return nil
}

func (s *Service) submitTaskApprovalWake(ctx context.Context, taskID, content string) error {
	if taskID == petTaskID {
		return s.submitPetWake(ctx, content)
	}
	_, err := s.SubmitMessage(ctx, taskID, content)
	return err
}

// taskAcceptsResultWake lists the states where a result message may start a
// Turn. Everything else is history-only.
func taskAcceptsResultWake(status domain.TaskStatus) bool {
	return status == domain.TaskActive || status == domain.TaskWaitingUser
}

// TaskAcceptsApprovalResultWake reports whether an approval result can wake the
// Task. Channel continuations use it to fall back to a history-only record on
// terminal Tasks instead of trying to submit a message that the state machine
// would reject.
func TaskAcceptsApprovalResultWake(status domain.TaskStatus) bool {
	return taskAcceptsResultWake(status)
}

func (s *Service) recordTaskApprovalResult(ctx context.Context, taskID, content string, at time.Time) error {
	if at.IsZero() {
		at = s.now().UTC()
	}
	_, err := s.store.AddConversationItem(ctx, domain.ConversationItem{
		ID:            domain.NewID("conversation"),
		TaskID:        taskID,
		AgentID:       "main",
		StreamAgentID: "main",
		FromAgentID:   "main",
		ToAgentID:     "main",
		Category:      "chat",
		Kind:          "agent_message",
		Summary:       content,
		Payload:       map[string]any{"approval_result": true},
		CreatedAt:     at,
	})
	if err != nil {
		log.Printf("record approval result for task %s: %v", taskID, err)
	}
	return err
}
