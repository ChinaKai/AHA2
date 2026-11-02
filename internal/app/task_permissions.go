package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const taskPermissionTTL = 15 * 60

type TaskPermissionRequestInput struct {
	TaskID                  string
	TurnID                  string
	InboundReceiptID        string
	ChannelInstanceID       string
	ChannelConversationID   string
	ApplicantIdentityLinkID string
	ApplicantName           string
	ApplicantIsBot          bool
	Action                  string
	Scope                   string
	Reason                  string
	Risk                    string
	ExpiresInSeconds        int
}

// CreateTaskPermissionRequest records an Owner decision request before the
// Task performs a sensitive action. It does not execute the action.
func (s *Service) CreateTaskPermissionRequest(ctx context.Context, input TaskPermissionRequestInput) (domain.TaskPermissionRequest, error) {
	input.TaskID = strings.TrimSpace(input.TaskID)
	input.Action = strings.TrimSpace(input.Action)
	input.Scope = strings.TrimSpace(input.Scope)
	input.Reason = strings.TrimSpace(input.Reason)
	input.Risk = strings.ToLower(strings.TrimSpace(input.Risk))
	if input.TaskID == "" || input.Action == "" {
		return domain.TaskPermissionRequest{}, fmt.Errorf("task_id and action are required")
	}
	if len([]rune(input.Action)) > 800 || len([]rune(input.Scope)) > 4000 || len([]rune(input.Reason)) > 2000 {
		return domain.TaskPermissionRequest{}, fmt.Errorf("permission request is too long")
	}
	if input.Risk != "low" && input.Risk != "medium" && input.Risk != "high" {
		input.Risk = "high"
	}
	if _, err := s.store.Task(ctx, input.TaskID); err != nil {
		return domain.TaskPermissionRequest{}, fmt.Errorf("Task 不存在")
	}
	if existing, listErr := s.store.ListTaskPermissionRequests(ctx, input.TaskID, 100); listErr == nil {
		now := s.now().UTC()
		for _, item := range existing {
			if item.Status == "pending" && item.ExpiresAt.After(now) {
				return domain.TaskPermissionRequest{}, fmt.Errorf("当前 Task 已有待审批的权限申请")
			}
		}
	}
	if input.ExpiresInSeconds <= 0 || input.ExpiresInSeconds > taskPermissionTTL {
		input.ExpiresInSeconds = taskPermissionTTL
	}
	now := s.now().UTC()
	item := domain.TaskPermissionRequest{
		ID: domain.NewID("task_permission"), TaskID: input.TaskID, TurnID: input.TurnID,
		InboundReceiptID: input.InboundReceiptID, ChannelInstanceID: input.ChannelInstanceID,
		ChannelConversationID:   input.ChannelConversationID,
		ApplicantIdentityLinkID: input.ApplicantIdentityLinkID,
		ApplicantName:           input.ApplicantName, ApplicantIsBot: input.ApplicantIsBot,
		Action: input.Action, Scope: input.Scope, Reason: input.Reason, Risk: input.Risk,
		Status: "pending", CreatedAt: now, UpdatedAt: now,
		ExpiresAt: now.Add(time.Duration(input.ExpiresInSeconds) * time.Second),
	}
	if err := s.store.CreateTaskPermissionRequest(ctx, item); err != nil {
		return domain.TaskPermissionRequest{}, err
	}
	return item, nil
}

func (s *Service) TaskPermissionRequests(ctx context.Context, taskID string, limit int) ([]domain.TaskPermissionRequest, error) {
	return s.store.ListTaskPermissionRequests(ctx, strings.TrimSpace(taskID), limit)
}

// ResolveTaskPermissionRequest consumes a pending request. The caller must
// supply a server-verified Owner identity, never a group display name.
func (s *Service) ResolveTaskPermissionRequest(
	ctx context.Context,
	id, decision, deciderIdentityLinkID, deciderName, reason string,
) (domain.TaskPermissionRequest, bool, error) {
	decision = strings.TrimSpace(decision)
	if decision == "approve" {
		decision = "approved"
	} else if decision == "reject" {
		decision = "rejected"
	}
	if decision != "approved" && decision != "rejected" {
		return domain.TaskPermissionRequest{}, false, fmt.Errorf("invalid decision")
	}
	return s.store.ResolveTaskPermissionRequest(
		ctx, strings.TrimSpace(id), decision, deciderIdentityLinkID, deciderName,
		strings.TrimSpace(reason), s.now().UTC(),
	)
}

func (s *Service) SetTaskPermissionRequestChannelAction(ctx context.Context, id, actionID string) error {
	return s.store.SetTaskPermissionRequestChannelAction(ctx, id, actionID, s.now().UTC())
}

// SubmitTaskPermissionApprovalContinuation resumes an approved channel request
// when the Owner decided in AHA Web instead of clicking the channel card. The
// continuation keeps the applicant's route and receipt so the follow-up Turn
// can answer the group and notify the applicant.
func (s *Service) SubmitTaskPermissionApprovalContinuation(
	ctx context.Context, request domain.TaskPermissionRequest, content string,
) (domain.Turn, error) {
	provenance, err := s.taskPermissionContinuationContext(ctx, request)
	if err != nil {
		return domain.Turn{}, err
	}
	return s.submitMessageWithSource(
		ctx, request.TaskID, "main", content, nil,
		request.InboundReceiptID, "", request.ChannelConversationID,
		provenance, "owner", "owner_message", true,
	)
}

func (s *Service) taskPermissionContinuationContext(
	ctx context.Context, request domain.TaskPermissionRequest,
) (map[string]any, error) {
	if strings.TrimSpace(request.ChannelInstanceID) == "" ||
		strings.TrimSpace(request.ChannelConversationID) == "" ||
		strings.TrimSpace(request.InboundReceiptID) == "" ||
		strings.TrimSpace(request.ApplicantIdentityLinkID) == "" {
		return nil, fmt.Errorf("permission request has no channel route")
	}
	instance, err := s.store.ChannelInstance(ctx, request.ChannelInstanceID)
	if err != nil {
		return nil, err
	}
	conversation, err := s.store.ChannelConversation(ctx, request.ChannelConversationID)
	if err != nil {
		return nil, err
	}
	if conversation.InstanceID != instance.ID {
		return nil, fmt.Errorf("permission request channel route is inconsistent")
	}
	return map[string]any{
		"schema": "aha.channel-context/v1", "instance_id": instance.ID, "provider": instance.ProviderKey,
		"endpoint": domain.ChannelEndpointGroupDigitalHuman, "conversation_id": conversation.ID,
		"chat_display_name": conversation.DisplayName,
		"actor": map[string]any{
			"identity_link_id": "", "role": "owner", "display_name": "Owner", "is_bot": false,
		},
		"route":              map[string]any{"mode": "task_route", "target_task_id": request.TaskID},
		"inbound_receipt_id": request.InboundReceiptID,
		"permission_request": map[string]any{
			"id":                         request.ID,
			"decision":                   "approved",
			"applicant_identity_link_id": request.ApplicantIdentityLinkID,
			"applicant_name":             request.ApplicantName,
			"applicant_is_bot":           request.ApplicantIsBot,
			"applicant_receipt_id":       request.InboundReceiptID,
		},
	}, nil
}
