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

func (s *Service) feishuGroupRoute(ctx context.Context, instance domain.ChannelInstance, envelope domain.ChannelInboundEnvelope, discover bool) (domain.ChannelConversation, domain.ChannelTaskRoute, error) {
	endpoint, err := s.store.ChannelEndpoint(ctx, instance.ID, domain.ChannelEndpointGroupDigitalHuman)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !endpoint.Enabled) {
		return domain.ChannelConversation{}, domain.ChannelTaskRoute{}, store.ErrChannelGroupUnbound
	}
	if err != nil {
		return domain.ChannelConversation{}, domain.ChannelTaskRoute{}, err
	}
	conversation, err := s.store.PreferredChannelGroupConversation(ctx, endpoint.ID, envelope.ExternalChatID)
	if errors.Is(err, sql.ErrNoRows) && discover && envelope.MentionedBot {
		// Make the group selectable for an Owner binding using just its chat
		// metadata. The unbound message itself is discarded.
		conversation, err = s.discoverFeishuGroup(ctx, instance, endpoint, envelope)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return conversation, domain.ChannelTaskRoute{}, store.ErrChannelGroupUnbound
	}
	if err != nil {
		return conversation, domain.ChannelTaskRoute{}, err
	}
	route, err := s.store.ActiveChannelTaskRoute(ctx, conversation.ID)
	if errors.Is(err, sql.ErrNoRows) {
		err = store.ErrChannelGroupUnbound
	}
	return conversation, route, err
}

func (s *Service) discoverFeishuGroup(ctx context.Context, instance domain.ChannelInstance, endpoint domain.ChannelEndpoint, envelope domain.ChannelInboundEnvelope) (domain.ChannelConversation, error) {
	scope, err := s.scopeKey(instance.ID, endpoint.Kind, "", envelope.ExternalChatID, "")
	if err != nil {
		return domain.ChannelConversation{}, err
	}
	now := s.now().UTC()
	conversation := domain.ChannelConversation{
		ID: domain.NewID("channel_conversation"), InstanceID: instance.ID, EndpointID: endpoint.ID,
		ScopeKeyVersion: 2, ScopeKey: scope, ExternalChatID: envelope.ExternalChatID,
		DisplayName: safeChannelDisplayName(envelope.ChatDisplayName, "群聊", instance.ID, envelope.ExternalChatID),
		Status:      "active", CreatedAt: now, UpdatedAt: now,
	}
	session := domain.ChannelSession{
		ID: domain.NewID("channel_session"), ConversationID: conversation.ID,
		Generation: 1, Mode: "group_qa", Status: "active", StartedAt: now,
	}
	if err := s.store.CreateChannelConversation(ctx, conversation, session); err != nil {
		if existing, lookupErr := s.store.ChannelConversationByScope(ctx, endpoint.ID, 2, scope); lookupErr == nil {
			return existing, nil
		}
		return domain.ChannelConversation{}, err
	}
	return conversation, nil
}

func normalizeGroupMetadata(envelope *domain.ChannelInboundEnvelope) error {
	envelope.ThreadID = strings.TrimSpace(envelope.ThreadID)
	envelope.RootID = strings.TrimSpace(envelope.RootID)
	envelope.SenderType = strings.ToLower(strings.TrimSpace(envelope.SenderType))
	if len(envelope.ThreadID) > 128 || len(envelope.RootID) > 128 || len(envelope.SenderType) > 32 {
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
