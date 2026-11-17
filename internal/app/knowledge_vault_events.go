package app

import (
	"context"
	"strings"

	"github.com/ChinaKai/AHA2/internal/domain"
)

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
	_ = s.store.AppendPetEvent(ctx, domain.PetEvent{
		ID: domain.NewID("pet_event"), At: s.now().UTC(), Actor: "aha",
		Kind: kind, Detail: detail, Status: "open",
	})
	s.hub.Publish("", domain.Event{
		ID: domain.NewID("pet_event"), AggregateType: "pet", AggregateID: "knowledge_sync",
		Type: "pet_event", OccurredAt: s.now().UTC(),
		Data: map[string]any{"kind": kind, "detail": detail},
	})
}
