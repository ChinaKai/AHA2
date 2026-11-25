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
	if !domain.FeishuGroupMessageAllowed(instance.Config, envelope.ExternalChatID, envelope.MentionedBot) {
		return domain.ChannelTaskRoute{}, store.ErrChannelGroupNotAllowed
	}
	_, route, err := s.feishuGroupRoute(ctx, instance.ID, envelope.ExternalChatID, false)
	if envelope.MentionedBot && errors.Is(err, store.ErrChannelGroupUnbound) {
		// An explicit mention can create or reuse a group host through the
		// original inbox. Only full reception requires an existing Task route.
		return domain.ChannelTaskRoute{}, nil
	}
	return route, err
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
