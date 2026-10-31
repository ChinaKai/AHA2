package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ChinaKai/AHA2/internal/domain"
)

type PetChannelBrief struct {
	ID                    string `json:"id"`
	PluginID              string `json:"plugin_id"`
	ProviderKey           string `json:"provider_key,omitempty"`
	Name                  string `json:"name"`
	Status                string `json:"status"`
	EffectiveAvailability string `json:"effective_availability,omitempty"`
	Revision              int    `json:"revision"`
	OwnerBound            bool   `json:"owner_bound"`
	CredentialConfigured  bool   `json:"credential_configured"`
	HostProjectID         string `json:"host_project_id,omitempty"`
	HostWorkspaceID       string `json:"host_workspace_id,omitempty"`
	LastSeenAt            string `json:"last_seen_at,omitempty"`
	Retired               bool   `json:"retired,omitempty"`
}

type PetChannelEndpointBrief struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Enabled bool   `json:"enabled"`
}

type PetChannelDetail struct {
	PetChannelBrief
	Endpoints []PetChannelEndpointBrief `json:"endpoints,omitempty"`
}

type PetChannelDestinationBrief struct {
	ConversationID string `json:"conversation_id"`
	InstanceID     string `json:"instance_id"`
	InstanceName   string `json:"instance_name"`
	ProviderKey    string `json:"provider_key,omitempty"`
	EndpointKind   string `json:"endpoint_kind"`
	DisplayName    string `json:"display_name"`
	Status         string `json:"status"`
	RouteID        string `json:"route_id,omitempty"`
	TargetTaskID   string `json:"target_task_id,omitempty"`
	TargetTaskCode string `json:"target_task_code,omitempty"`
	TargetTaskName string `json:"target_task_name,omitempty"`
	UpdatedAt      string `json:"updated_at,omitempty"`
}

type PetChannelContactBrief struct {
	IdentityLinkID string `json:"identity_link_id"`
	DisplayName    string `json:"display_name"`
	Role           string `json:"role"`
	IsBot          bool   `json:"is_bot"`
	LastSeenAt     string `json:"last_seen_at,omitempty"`
}

type PetChannelContactsView struct {
	Channel  PetChannelDestinationBrief `json:"channel"`
	Contacts []PetChannelContactBrief   `json:"contacts"`
}

func (s *Service) petChannelReadable(ctx context.Context) (domain.OwnerAssistant, error) {
	assistant, err := s.petAssistant(ctx)
	if err != nil {
		return domain.OwnerAssistant{}, err
	}
	if !assistant.PetAllowViewTasks && !assistant.PetAllowManageChannels {
		return domain.OwnerAssistant{}, fmt.Errorf("主人未开启查看渠道信息权限")
	}
	return assistant, nil
}

func (s *Service) petChannelWritable(ctx context.Context) (domain.OwnerAssistant, error) {
	assistant, err := s.petAssistant(ctx)
	if err != nil {
		return domain.OwnerAssistant{}, err
	}
	if !assistant.PetAllowManageChannels {
		return domain.OwnerAssistant{}, fmt.Errorf("主人未开启管理渠道权限")
	}
	return assistant, nil
}

func (s *Service) PetListChannels(ctx context.Context) ([]PetChannelBrief, error) {
	if _, err := s.petChannelReadable(ctx); err != nil {
		return nil, err
	}
	items, err := s.store.ChannelInstances(ctx, "")
	if err != nil {
		return nil, err
	}
	result := make([]PetChannelBrief, 0, len(items))
	for _, item := range items {
		if len(result) == 50 {
			break
		}
		result = append(result, petChannelBrief(item))
	}
	return result, nil
}

func (s *Service) PetChannelDetail(ctx context.Context, id string) (PetChannelDetail, error) {
	if _, err := s.petChannelReadable(ctx); err != nil {
		return PetChannelDetail{}, err
	}
	item, err := s.store.ChannelInstance(ctx, strings.TrimSpace(id))
	if err != nil {
		return PetChannelDetail{}, fmt.Errorf("渠道实例不存在")
	}
	endpoints, err := s.store.ChannelEndpoints(ctx, item.ID)
	if err != nil {
		return PetChannelDetail{}, err
	}
	detail := PetChannelDetail{PetChannelBrief: petChannelBrief(item)}
	for _, endpoint := range endpoints {
		detail.Endpoints = append(detail.Endpoints, PetChannelEndpointBrief{
			ID: endpoint.ID, Kind: endpoint.Kind, Enabled: endpoint.Enabled,
		})
	}
	return detail, nil
}

func petChannelBrief(item domain.ChannelInstance) PetChannelBrief {
	lastSeen := ""
	if !item.LastSeenAt.IsZero() {
		lastSeen = item.LastSeenAt.Format("2006-01-02T15:04:05Z07:00")
	}
	return PetChannelBrief{
		ID: item.ID, PluginID: item.PluginID, ProviderKey: item.ProviderKey, Name: item.Name,
		Status: item.Status, EffectiveAvailability: item.EffectiveAvailability, Revision: item.Revision,
		OwnerBound: item.OwnerBound, CredentialConfigured: item.CredentialConfigured,
		HostProjectID: item.HostProjectID, HostWorkspaceID: item.HostWorkspaceID,
		LastSeenAt: lastSeen, Retired: item.Retired,
	}
}

func (s *Service) PetListChannelDestinations(ctx context.Context) ([]PetChannelDestinationBrief, error) {
	if _, err := s.petChannelReadable(ctx); err != nil {
		return nil, err
	}
	ownerID, err := s.store.PrimaryOwnerID(ctx)
	if err != nil {
		return nil, fmt.Errorf("本机还没有 Owner 账号")
	}
	items, err := s.store.ChannelDestinations(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	result := make([]PetChannelDestinationBrief, 0, len(items))
	for _, item := range items {
		if len(result) == 100 {
			break
		}
		result = append(result, petChannelDestinationBrief(item))
	}
	return result, nil
}

func (s *Service) PetChannelContacts(ctx context.Context, conversationID string) (PetChannelContactsView, error) {
	if _, err := s.petChannelReadable(ctx); err != nil {
		return PetChannelContactsView{}, err
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return PetChannelContactsView{}, fmt.Errorf("conversation_id 不能为空")
	}
	ownerID, err := s.store.PrimaryOwnerID(ctx)
	if err != nil {
		return PetChannelContactsView{}, fmt.Errorf("本机还没有 Owner 账号")
	}
	destination, err := s.store.ChannelDestination(ctx, ownerID, conversationID)
	if err != nil {
		return PetChannelContactsView{}, fmt.Errorf("渠道目的地不存在")
	}
	contacts, err := s.store.ChannelContactsForConversation(ctx, destination.InstanceID, conversationID, 50)
	if err != nil {
		return PetChannelContactsView{}, err
	}
	view := PetChannelContactsView{
		Channel:  petChannelDestinationBrief(destination),
		Contacts: make([]PetChannelContactBrief, 0, len(contacts)),
	}
	for _, contact := range contacts {
		seenAt := ""
		if !contact.LastSeenAt.IsZero() {
			seenAt = contact.LastSeenAt.Format("2006-01-02T15:04:05Z07:00")
		}
		view.Contacts = append(view.Contacts, PetChannelContactBrief{
			IdentityLinkID: contact.IdentityLinkID, DisplayName: contact.DisplayName,
			Role: contact.Role, IsBot: contact.IsBot, LastSeenAt: seenAt,
		})
	}
	return view, nil
}

func petChannelDestinationBrief(item domain.ChannelDestination) PetChannelDestinationBrief {
	updated := ""
	if !item.UpdatedAt.IsZero() {
		updated = item.UpdatedAt.Format("2006-01-02T15:04:05Z07:00")
	}
	return PetChannelDestinationBrief{
		ConversationID: item.ConversationID, InstanceID: item.InstanceID, InstanceName: item.InstanceName,
		ProviderKey: item.ProviderKey, EndpointKind: item.EndpointKind, DisplayName: item.DisplayName,
		Status: item.Status, RouteID: item.RouteID, TargetTaskID: item.TargetTaskID,
		TargetTaskCode: item.TargetTaskCode, TargetTaskName: item.TargetTaskName, UpdatedAt: updated,
	}
}

func (s *Service) PetRequestChannelLifecycle(ctx context.Context, instanceID, action string, reason ...string) (domain.PetApproval, error) {
	if _, err := s.petChannelWritable(ctx); err != nil {
		return domain.PetApproval{}, err
	}
	reasonText, reasonErr := requiredPetApprovalReason(reason...)
	if reasonErr != nil {
		return domain.PetApproval{}, reasonErr
	}
	action = strings.ToLower(strings.TrimSpace(action))
	if action != "reset" && action != "archive" && action != "purge" {
		return domain.PetApproval{}, fmt.Errorf("渠道操作必须是 reset、archive 或 purge")
	}
	item, err := s.store.ChannelInstance(ctx, strings.TrimSpace(instanceID))
	if err != nil {
		return domain.PetApproval{}, fmt.Errorf("渠道实例不存在")
	}
	ownerID, err := s.store.PrimaryOwnerID(ctx)
	if err != nil || item.OwnerID != ownerID {
		return domain.PetApproval{}, fmt.Errorf("渠道实例不属于当前 Owner")
	}
	if action == "purge" {
		if !item.Retired {
			return domain.PetApproval{}, fmt.Errorf("只能清理已归档的渠道实例")
		}
	} else if item.Retired {
		return domain.PetApproval{}, fmt.Errorf("渠道实例已归档")
	}
	kind, title, summary := "channel_reset_binding", "重置渠道绑定："+item.Name,
		fmt.Sprintf("宠物申请重置渠道「%s」（%s）的绑定和凭据。批准后才执行。", item.Name, item.ID)
	switch action {
	case "archive":
		kind, title = "channel_archive", "归档渠道："+item.Name
		summary = fmt.Sprintf("宠物申请归档渠道「%s」（%s）。批准后才执行；归档会停止运行时并清理绑定。", item.Name, item.ID)
	case "purge":
		kind, title = "channel_purge", "清理渠道："+item.Name
		summary = fmt.Sprintf("宠物申请永久清理已归档渠道「%s」（%s）。批准后才执行，此操作不可恢复。", item.Name, item.ID)
	}
	payload, _ := json.Marshal(map[string]any{"instance_id": item.ID, "revision": item.Revision, "action": action})
	return s.CreatePetApproval(ctx, PetApprovalInput{
		Kind: kind, Title: title, Summary: summary, Reason: reasonText, Risk: "high", Payload: string(payload),
	})
}

func (s *Service) PetRequestChannelEnabled(ctx context.Context, instanceID string, enabled bool) (domain.PetApproval, error) {
	if _, err := s.petChannelWritable(ctx); err != nil {
		return domain.PetApproval{}, err
	}
	item, err := s.store.ChannelInstance(ctx, strings.TrimSpace(instanceID))
	if err != nil {
		return domain.PetApproval{}, fmt.Errorf("渠道实例不存在")
	}
	if item.Retired {
		return domain.PetApproval{}, fmt.Errorf("渠道实例已归档")
	}
	ownerID, err := s.store.PrimaryOwnerID(ctx)
	if err != nil || item.OwnerID != ownerID {
		return domain.PetApproval{}, fmt.Errorf("渠道实例不属于当前 Owner")
	}
	action := "停用"
	if enabled {
		action = "启用"
	}
	payload, _ := json.Marshal(map[string]any{
		"instance_id": item.ID, "enabled": enabled, "revision": item.Revision,
	})
	return s.CreatePetApproval(ctx, PetApprovalInput{
		Kind:  "channel_set_enabled",
		Title: action + "渠道：" + item.Name,
		Summary: fmt.Sprintf(
			"宠物申请%s渠道「%s」（%s）。批准后才执行，并会停止/恢复渠道运行时。",
			action, item.Name, item.ID,
		),
		Risk: "high", Payload: string(payload),
	})
}

type PetChannelSendInput struct {
	TaskID                 string   `json:"task_id"`
	Reason                 string   `json:"reason"`
	Message                string   `json:"message"`
	MentionIdentityLinkIDs []string `json:"mention_identity_link_ids"`
	AttachmentIDs          []string `json:"attachment_ids"`
}

func (s *Service) PetRequestChannelSend(ctx context.Context, input PetChannelSendInput) (domain.PetApproval, error) {
	if _, err := s.petChannelWritable(ctx); err != nil {
		return domain.PetApproval{}, err
	}
	reasonText, reasonErr := requirePetApprovalReason(input.Reason)
	if reasonErr != nil {
		return domain.PetApproval{}, reasonErr
	}
	taskID := strings.TrimSpace(input.TaskID)
	message := strings.TrimSpace(input.Message)
	if taskID == "" {
		return domain.PetApproval{}, fmt.Errorf("task_id 不能为空")
	}
	if message == "" || len([]rune(message)) > 1000 {
		return domain.PetApproval{}, fmt.Errorf("消息不能为空且不能超过 1000 个字符")
	}
	if len(input.MentionIdentityLinkIDs) > 5 {
		return domain.PetApproval{}, fmt.Errorf("最多提及 5 个渠道联系人")
	}
	task, err := s.store.Task(ctx, taskID)
	if err != nil || task.ReadOnly {
		return domain.PetApproval{}, fmt.Errorf("Task 不存在或只读")
	}
	route, err := s.store.ActiveChannelTaskRouteForTask(ctx, task.ID)
	if err != nil {
		return domain.PetApproval{}, fmt.Errorf("该 Task 没有活动渠道路由，请先绑定渠道")
	}
	ownerID, err := s.store.PrimaryOwnerID(ctx)
	if err != nil {
		return domain.PetApproval{}, fmt.Errorf("本机还没有 Owner 账号")
	}
	destination, err := s.store.ChannelDestination(ctx, ownerID, route.ConversationID)
	if err != nil {
		return domain.PetApproval{}, fmt.Errorf("渠道目的地不存在，可能已失效")
	}
	if destination.EndpointKind != domain.ChannelEndpointGroupDigitalHuman || destination.Status != "active" {
		return domain.PetApproval{}, fmt.Errorf("只有群聊数字人渠道支持对外发送")
	}
	contacts, err := s.store.TaskChannelContacts(ctx, task.ID, route.InstanceID, route.ConversationID, 50)
	if err != nil {
		return domain.PetApproval{}, err
	}
	available := map[string]bool{}
	for _, contact := range contacts {
		available[contact.IdentityLinkID] = true
	}
	mentions := make([]string, 0, len(input.MentionIdentityLinkIDs))
	seen := map[string]bool{}
	for _, raw := range input.MentionIdentityLinkIDs {
		identityID := strings.TrimSpace(raw)
		if identityID == "" || seen[identityID] || !available[identityID] {
			return domain.PetApproval{}, fmt.Errorf("渠道联系人 %s 不在当前群聊", identityID)
		}
		seen[identityID] = true
		mentions = append(mentions, identityID)
	}
	requestID := domain.NewID("pet_channel_send")
	turnID := "pet_channel_" + requestID
	payload, _ := json.Marshal(map[string]any{
		"task_id": task.ID, "request_id": requestID, "turn_id": turnID,
		"purpose": "blocker", "message": message,
		"mention_identity_link_ids": mentions, "attachment_ids": input.AttachmentIDs,
	})
	return s.CreatePetApproval(ctx, PetApprovalInput{
		Kind:    "external_channel_send",
		Title:   "对外渠道发送：" + destination.DisplayName,
		Summary: fmt.Sprintf("宠物申请向渠道「%s」发送消息：%s", destination.DisplayName, truncateAssistantText(message, 300)),
		Reason:  reasonText, Risk: "high", Payload: string(payload),
	})
}
