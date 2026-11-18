package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
)

// Only these background-sync events are surfaced as pet notifications. The
// whitelist keeps unrelated pet_events (task/project/control rows) out of the
// wake-up briefing and gives the ack/resolve paths a bounded kind set.
var knowledgeSyncNotificationKinds = []string{
	knowledgevault.SyncEventConflict,
	knowledgevault.SyncEventPaused,
}

const (
	// The work-state budget trims notification detail by serialized bytes, so
	// this only bounds how many unresolved rows one wake-up shows.
	knowledgeSyncNotificationLimit = 10
	// The pet acknowledging a notification is deliberately not the same as
	// resolving the divergence: the sync loop keeps its pause until a manual
	// Git operation or a successful automatic sync proves the cause is gone.
	knowledgeSyncNotificationAcknowledged = "acknowledged"
	knowledgeSyncNotificationResolved     = "resolved"
)

// PetNotification is the pet-facing summary of one unresolved background
// knowledge-sync event. Detail is the same short commit/file-name text the
// syncer emitted; document bodies and credentials never appear here.
type PetNotification struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	At     string `json:"at"`
	Detail string `json:"detail"`
	Status string `json:"status"`
}

// RecordKnowledgeSyncEvent persists a background knowledge-sync event for the
// pet and nudges connected Web clients. The syncer already de-duplicates, so
// this method always writes what it is given. Detail contains only commit
// identifiers and file names, never document bodies or credentials.
func (s *Service) RecordKnowledgeSyncEvent(ctx context.Context, kind, detail string) {
	if s == nil || s.store == nil {
		return
	}
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	detail = strings.TrimSpace(detail)
	at := s.now().UTC()
	id := domain.NewID("pet_event")
	_ = s.store.AppendPetEvent(ctx, domain.PetEvent{
		ID: id, At: at, Actor: "aha",
		Kind: kind, Detail: detail, Status: "open",
	})
	s.hub.Publish("", domain.Event{
		ID: id, AggregateType: "pet", AggregateID: "knowledge_sync",
		Type: "pet_event", OccurredAt: at,
		Data: map[string]any{"id": id, "kind": kind, "detail": detail, "at": at.UTC().Format(time.RFC3339)},
	})
}

// KnowledgeSyncNotifications lists the still-open knowledge-sync events the
// pet has not acknowledged or resolved yet, newest first. It is independent of
// the history limit so an unresolved conflict stays visible across wake-ups.
func (s *Service) KnowledgeSyncNotifications(ctx context.Context) ([]PetNotification, error) {
	if s == nil || s.store == nil {
		return []PetNotification{}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	events, err := s.store.ListOpenPetEventsByKind(ctx, knowledgeSyncNotificationKinds, knowledgeSyncNotificationLimit)
	if err != nil {
		return nil, err
	}
	result := make([]PetNotification, 0, len(events))
	for _, event := range events {
		result = append(result, PetNotification{
			ID:     event.ID,
			Kind:   event.Kind,
			At:     event.At.UTC().Format(time.RFC3339),
			Detail: event.Detail,
			Status: event.Status,
		})
	}
	return result, nil
}

// AcknowledgeKnowledgeSyncNotifications marks the listed open notifications as
// acknowledged. It only changes the event rows: a paused syncer stays paused
// and the Git divergence is untouched, so the pet must still fix the cause and
// run a manual pull/push/sync (or let the next automatic sync succeed).
func (s *Service) AcknowledgeKnowledgeSyncNotifications(ctx context.Context, ids []string) (int, error) {
	if s == nil || s.store == nil {
		return 0, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cleaned := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		cleaned = append(cleaned, id)
	}
	if len(cleaned) == 0 {
		return 0, fmt.Errorf("ids 不能为空")
	}
	return s.store.MarkOpenPetEvents(ctx, knowledgeSyncNotificationKinds, cleaned, knowledgeSyncNotificationAcknowledged)
}

// ResolveKnowledgeSyncNotifications closes every open knowledge-sync
// notification after sync demonstrably recovered (a successful manual
// pull/push/sync, vault re-init/import, or an automatic sync cycle). It never
// changes the syncer pause state itself; the caller only invokes it after the
// operation that proves the cause is gone.
func (s *Service) ResolveKnowledgeSyncNotifications(ctx context.Context) (int, error) {
	if s == nil || s.store == nil {
		return 0, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return s.store.MarkOpenPetEvents(ctx, knowledgeSyncNotificationKinds, nil, knowledgeSyncNotificationResolved)
}
