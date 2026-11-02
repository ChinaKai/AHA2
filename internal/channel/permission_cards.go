package channel

import (
	"context"
	"fmt"
	"strings"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// FinishTaskPermissionCardFromWeb updates the channel card after an Owner
// decision in AHA Web. The card is updated in place by the provider plugin when
// the original message ID is known.
func (s *Service) FinishTaskPermissionCardFromWeb(ctx context.Context, requestID, decision string) error {
	request, err := s.store.TaskPermissionRequest(ctx, requestID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(request.ChannelActionID) == "" {
		return nil
	}
	action, err := s.store.ChannelPendingAction(ctx, request.ChannelActionID)
	if err != nil || action.Operation != "task_permission" {
		return fmt.Errorf("permission card is not available")
	}
	status := "succeeded"
	if decision == "rejected" {
		status = "cancelled"
	}
	return s.store.FinishChannelPendingActionFromOwner(
		ctx, action.ID, request.ID, status,
		map[string]any{"request_id": request.ID, "decision": decision}, s.now().UTC(),
	)
}

func (s *Service) resolveTaskPermissionCardAction(
	ctx context.Context,
	receipt domain.ChannelInboxReceipt,
	instance domain.ChannelInstance,
	identity domain.ChannelIdentityLink,
	action domain.ChannelPendingAction,
	decision string,
) error {
	request, err := s.store.TaskPermissionRequest(ctx, action.TargetID)
	if err != nil || request.TaskID == "" || strings.TrimSpace(request.ChannelConversationID) != action.ConversationID {
		return fmt.Errorf("permission request is not available")
	}
	if request.ChannelActionID != "" && request.ChannelActionID != action.ID {
		return fmt.Errorf("permission request card changed")
	}
	decision = map[string]string{"confirm": "approve", "cancel": "reject"}[decision]
	if decision == "" {
		return fmt.Errorf("invalid permission decision")
	}
	resolved, changed, err := s.application.ResolveTaskPermissionRequest(
		ctx, request.ID, decision, identity.ID, identity.DisplayName, "decided in channel group",
	)
	if err != nil {
		return err
	}
	if !changed {
		return fmt.Errorf("permission request is already decided or expired")
	}
	if decision == "reject" {
		if err := s.store.FinishChannelPendingAction(ctx, action.ID, resolved.ID, "cancelled",
			map[string]any{"request_id": resolved.ID, "decision": "rejected"}, s.now().UTC()); err != nil {
			return err
		}
		return s.store.FinishChannelInbox(ctx, receipt.ID, receipt.LeaseID, "processed", action.ConversationID, "", "permission_rejected", s.now().UTC())
	}
	if err := s.store.FinishChannelPendingAction(ctx, action.ID, resolved.ID, "succeeded",
		map[string]any{"request_id": resolved.ID, "decision": "approved"}, s.now().UTC()); err != nil {
		return err
	}
	content := fmt.Sprintf(
		"Owner 已批准权限申请 %s（申请人：%s）。请继续执行已授权的动作：%s。不要扩大范围，完成后向渠道汇报结果。",
		resolved.ID, resolved.ApplicantName, resolved.Action,
	)
	provenance := map[string]any{
		"schema": "aha.channel-context/v1", "instance_id": instance.ID, "provider": instance.ProviderKey,
		"endpoint": domain.ChannelEndpointGroupDigitalHuman, "conversation_id": action.ConversationID,
		"actor": map[string]any{
			"identity_link_id": identity.ID, "role": "owner",
			"display_name": identity.DisplayName, "is_bot": false,
		},
		"route":              map[string]any{"mode": "task_route", "target_task_id": resolved.TaskID},
		"inbound_receipt_id": receipt.ID,
		"permission_request": map[string]any{"id": resolved.ID, "decision": "approved"},
	}
	_, err = s.application.SubmitChannelMessageWithAttachments(
		ctx, receipt.ID, receipt.LeaseID, action.ConversationID, resolved.TaskID, content, provenance, nil,
	)
	return err
}

// SendTaskPermissionRequestCard sends the Owner-only confirmation card for a
// Task permission request. The pending action stores the verified Owner
// identity, so card clicks from other group members are rejected by the
// existing card-action path.
func (s *Service) SendTaskPermissionRequestCard(ctx context.Context, instanceID, conversationID string, request domain.TaskPermissionRequest) (string, error) {
	instanceID = strings.TrimSpace(instanceID)
	conversationID = strings.TrimSpace(conversationID)
	if instanceID == "" || conversationID == "" {
		return "", fmt.Errorf("permission request has no channel conversation")
	}
	owner, err := s.store.ChannelOwnerIdentity(ctx, instanceID)
	if err != nil || strings.TrimSpace(owner.ExternalUserID) == "" {
		return "", fmt.Errorf("channel Owner identity is not linked")
	}
	if owner.OwnerID != "" {
		instance, instanceErr := s.store.ChannelInstance(ctx, instanceID)
		if instanceErr != nil || instance.OwnerID != owner.OwnerID {
			return "", fmt.Errorf("channel Owner identity is not valid")
		}
	}
	now := s.now().UTC()
	action := domain.ChannelPendingAction{
		ID: domain.NewID("channel_action"), InstanceID: instanceID, ConversationID: conversationID,
		ActorIdentityLinkID: owner.ID, Operation: "task_permission",
		TargetType: "task_permission", TargetID: request.ID,
		Intent: map[string]any{
			"request_id": request.ID, "task_id": request.TaskID,
			"applicant": request.ApplicantName, "action": request.Action,
		},
		Preview: map[string]any{
			"request_id": request.ID, "task_id": request.TaskID,
			"applicant": request.ApplicantName, "action": request.Action,
			"scope": request.Scope, "reason": request.Reason, "risk": request.Risk,
		},
		Precondition: map[string]any{"request_status": "pending"},
		Status:       "pending", ExpiresAt: request.ExpiresAt, CreatedAt: now, UpdatedAt: now,
	}
	created, err := s.store.CreateChannelPendingAction(ctx, action)
	if err != nil {
		return "", err
	}
	return created.ID, nil
}
