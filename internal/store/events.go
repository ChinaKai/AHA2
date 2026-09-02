package store

import (
	"context"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Store) AppendEvent(ctx context.Context, item domain.Event) (int64, error) {
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO events(id,aggregate_type,aggregate_id,type,data_json,occurred_at)
		VALUES(?,?,?,?,?,?)`,
		item.ID, item.AggregateType, item.AggregateID, item.Type, encodeJSON(item.Data), timeString(item.OccurredAt),
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (s *Store) EventsAfter(ctx context.Context, aggregateType, aggregateID string, after int64, limit int) ([]domain.Event, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT sequence,id,aggregate_type,aggregate_id,type,data_json,occurred_at
		FROM events
		WHERE aggregate_type=? AND aggregate_id=? AND sequence>?
		ORDER BY sequence LIMIT ?`,
		aggregateType, aggregateID, after, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.Event
	for rows.Next() {
		var item domain.Event
		var data, occurredAt string
		if err := rows.Scan(&item.Sequence, &item.ID, &item.AggregateType, &item.AggregateID, &item.Type, &data, &occurredAt); err != nil {
			return nil, err
		}
		item.Data = decodeJSON(data, map[string]any{})
		item.OccurredAt = parseTime(occurredAt)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) AppendAudit(ctx context.Context, ownerID, action, resourceType, resourceID string, data map[string]any, occurredAt string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO audit_events(id,owner_id,action,resource_type,resource_id,data_json,occurred_at)
		VALUES(?,?,?,?,?,?,?)`,
		domain.NewID("audit"), ownerID, action, resourceType, resourceID, encodeJSON(data), occurredAt,
	)
	return err
}
