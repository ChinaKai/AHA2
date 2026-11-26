package channel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func (s *Service) feishuGroupIngressRoute(ctx context.Context, instance domain.ChannelInstance, envelope domain.ChannelInboundEnvelope) (domain.ChannelTaskRoute, error) {
	allowed, err := s.store.FeishuGroupMessageAllowed(ctx, instance, envelope.ExternalChatID, envelope.MentionedBot)
	if err != nil {
		return domain.ChannelTaskRoute{}, err
	}
	if !allowed {
		return domain.ChannelTaskRoute{}, store.ErrChannelGroupNotAllowed
	}
	conversation, route, err := s.feishuGroupRoute(ctx, instance.ID, envelope.ExternalChatID, false)
	if !errors.Is(err, store.ErrChannelGroupUnbound) {
		return route, err
	}
	if envelope.MentionedBot && envelope.ThreadID == "" {
		// A top-level mention still uses the original host inbox.
		return domain.ChannelTaskRoute{}, nil
	}
	if conversation.ID == "" {
		conversation, err = s.ensureFeishuGroupMetadata(ctx, instance, envelope)
		if envelope.MentionedBot && errors.Is(err, store.ErrChannelGroupUnbound) {
			return domain.ChannelTaskRoute{}, nil
		}
		if err != nil {
			return domain.ChannelTaskRoute{}, err
		}
	}
	return domain.ChannelTaskRoute{
		InstanceID: instance.ID, ConversationID: conversation.ID, TargetTaskID: conversation.HostTaskID,
	}, nil
}

func (s *Service) ensureFeishuGroupMetadata(ctx context.Context, instance domain.ChannelInstance, envelope domain.ChannelInboundEnvelope) (domain.ChannelConversation, error) {
	endpoint, err := s.store.ChannelEndpoint(ctx, instance.ID, domain.ChannelEndpointGroupDigitalHuman)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !endpoint.Enabled) {
		return domain.ChannelConversation{}, store.ErrChannelGroupUnbound
	}
	if err != nil {
		return domain.ChannelConversation{}, err
	}
	scopeKey, err := s.scopeKey(instance.ID, endpoint.Kind, "", envelope.ExternalChatID, "")
	if err != nil {
		return domain.ChannelConversation{}, err
	}
	now := s.now().UTC()
	conversation := domain.ChannelConversation{
		ID: domain.NewID("channel_conversation"), InstanceID: instance.ID, EndpointID: endpoint.ID,
		ScopeKeyVersion: 2, ScopeKey: scopeKey, ExternalChatID: envelope.ExternalChatID,
		DisplayName: safeChannelDisplayName(envelope.ChatDisplayName, "群聊", instance.ID, envelope.ExternalChatID),
		Status:      "active", CreatedAt: now, UpdatedAt: now,
	}
	session := domain.ChannelSession{ID: domain.NewID("channel_session"), ConversationID: conversation.ID,
		Generation: 1, Mode: "group_qa", Status: "active", StartedAt: now}
	if err := s.store.CreateChannelConversation(ctx, conversation, session); err != nil {
		if existing, lookupErr := s.store.PreferredChannelGroupConversation(ctx, endpoint.ID, envelope.ExternalChatID); lookupErr == nil {
			return existing, nil
		}
		return domain.ChannelConversation{}, err
	}
	return conversation, nil
}

func (s *Service) feishuGroupRoute(ctx context.Context, instanceID, chatID string, mentioned bool) (domain.ChannelConversation, domain.ChannelTaskRoute, error) {
	endpoint, err := s.store.ChannelEndpoint(ctx, instanceID, domain.ChannelEndpointGroupDigitalHuman)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !endpoint.Enabled) {
		return domain.ChannelConversation{}, domain.ChannelTaskRoute{}, store.ErrChannelGroupUnbound
	}
	if err != nil {
		return domain.ChannelConversation{}, domain.ChannelTaskRoute{}, err
	}
	conversation, err := s.store.PreferredChannelGroupConversation(ctx, endpoint.ID, chatID)
	if errors.Is(err, sql.ErrNoRows) {
		return conversation, domain.ChannelTaskRoute{}, store.ErrChannelGroupUnbound
	}
	if err != nil {
		return conversation, domain.ChannelTaskRoute{}, err
	}
	var route domain.ChannelTaskRoute
	if mentioned {
		route, err = s.store.ChannelTaskRouteForMention(ctx, conversation.ID)
	} else {
		route, err = s.store.ActiveChannelTaskRoute(ctx, conversation.ID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		err = store.ErrChannelGroupUnbound
	}
	return conversation, route, err
}

func (s *Service) logFeishuGroupRejection(ctx context.Context, envelope domain.ChannelInboundEnvelope, err error) {
	if errors.Is(err, store.ErrChannelGroupUnbound) || errors.Is(err, store.ErrChannelGroupNotAllowed) {
		s.logger.InfoContext(ctx, "Feishu group message rejected before storage",
			"instance_id", envelope.InstanceID, "group_ref", stableID("group", envelope.InstanceID, envelope.ExternalChatID),
			"reason", err.Error())
	}
}

func normalizeGroupMetadata(envelope *domain.ChannelInboundEnvelope) error {
	envelope.ThreadID = strings.TrimSpace(envelope.ThreadID)
	envelope.RootID = strings.TrimSpace(envelope.RootID)
	envelope.SenderType = strings.ToLower(strings.TrimSpace(envelope.SenderType))
	if len(envelope.ThreadID) > 128 || len(envelope.RootID) > 128 || len(envelope.SenderType) > 32 ||
		strings.ContainsAny(envelope.ThreadID+envelope.RootID+envelope.SenderType, " \t\r\n") {
		return fmt.Errorf("invalid_envelope")
	}
	// Older plugins supplied only sender_is_bot. The new typed field is
	// authoritative when present, including unknown and app.
	if envelope.SenderType != "" {
		envelope.SenderIsBot = envelope.SenderType != "user"
	} else if envelope.SenderIsBot {
		envelope.SenderType = "bot"
	} else {
		envelope.SenderType = "user"
	}
	return nil
}

func (s *Service) maintainGroupScheduling(ctx context.Context, lastPrune *time.Time) error {
	now := s.now().UTC()
	if now.Sub(*lastPrune) >= time.Hour {
		if err := s.store.PruneChannelListening(ctx, now); err != nil {
			return err
		}
		*lastPrune = now
	}
	if s.application == nil {
		return nil
	}
	ids, err := s.store.PendingChannelGroupTasks(ctx)
	if err != nil {
		return err
	}
	var failures error
	for _, id := range ids {
		failures = errors.Join(failures, s.application.ScheduleChannelGroupInbox(ctx, id))
	}
	return failures
}
