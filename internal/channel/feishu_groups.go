package channel

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func (s *Service) SetFeishuGroupReception(ctx context.Context, ownerID, instanceID, chatID string, enabled bool) (domain.FeishuGroup, error) {
	if !validFeishuGroupID(chatID) {
		return domain.FeishuGroup{}, fmt.Errorf("invalid Feishu group chat ID")
	}
	group, changed, err := s.store.SetFeishuGroupReception(ctx, instanceID, ownerID, chatID, enabled, s.now().UTC())
	if err == nil && changed {
		s.stopPluginProcess(instanceID)
	}
	return group, err
}

func (s *Service) RefreshFeishuConversations(ctx context.Context, ownerID, instanceID string) ([]domain.ChannelConversationSummary, error) {
	instance, err := s.store.ChannelInstance(ctx, instanceID)
	if err != nil || instance.OwnerID != ownerID || instance.ProviderKey != "feishu" || instance.Retired {
		return nil, sql.ErrNoRows
	}
	if instance.Status != "ready" && instance.Status != "degraded" {
		return nil, fmt.Errorf("飞书渠道尚未就绪")
	}
	command, err := s.EnqueueCommand(ctx, instanceID, store.FeishuGroupRefreshCommand, domain.NewID("feishu_group_refresh"), nil)
	if err != nil {
		return nil, err
	}
	waitCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-waitCtx.Done():
			return nil, fmt.Errorf("刷新飞书群聊超时")
		case <-ticker.C:
			current, err := s.store.ChannelCommand(waitCtx, instanceID, command.ID)
			if err != nil {
				return nil, err
			}
			switch current.State {
			case "completed":
				return s.OwnerConversationSummaries(ctx, ownerID, instanceID)
			case "failed", "cancelled":
				return nil, fmt.Errorf("刷新飞书群聊失败（%s）", current.LastErrorCode)
			}
		}
	}
}

func (s *Service) completeFeishuGroupRefresh(ctx context.Context, command domain.ChannelPluginCommand, result map[string]any, leaseID string) error {
	data, err := json.Marshal(result["groups"])
	if err != nil {
		return fmt.Errorf("invalid_envelope")
	}
	var groups []domain.FeishuGroup
	if err := json.Unmarshal(data, &groups); err != nil || groups == nil {
		return fmt.Errorf("invalid_envelope")
	}
	for i := range groups {
		group := &groups[i]
		if !validFeishuGroupID(group.ChatID) {
			return fmt.Errorf("invalid_envelope")
		}
		group.DisplayName = safeChannelDisplayName(group.DisplayName, "群聊", command.InstanceID, group.ChatID)
	}
	return s.store.CompleteFeishuGroupRefresh(ctx, command.InstanceID, command.ID, leaseID, groups, s.now().UTC())
}
