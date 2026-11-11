package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const petNavigationColumns = `id,task_id,status,created_at_ms,expires_at_ms,consumed_at_ms,consumed_by`

func (s *Store) CreatePetNavigationAction(ctx context.Context, item domain.PetNavigationAction) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO pet_navigation_actions(id,task_id,status,created_at_ms,expires_at_ms,consumed_at_ms,consumed_by)
		VALUES(?,?,?,?,?,?,?)`,
		item.ID, item.TaskID, item.Status, petNavigationMillis(item.CreatedAt), petNavigationMillis(item.ExpiresAt),
		petNavigationMillis(item.ConsumedAt), item.ConsumedBy)
	return err
}

func (s *Store) PetNavigationAction(ctx context.Context, id string) (domain.PetNavigationAction, error) {
	return scanPetNavigationAction(s.db.QueryRowContext(ctx,
		`SELECT `+petNavigationColumns+` FROM pet_navigation_actions WHERE id=?`, id))
}

// ClaimPetNavigationActions atomically expires stale rows and consumes up to
// limit pending rows. When ids is non-empty only those rows are eligible.
// Each row transitions pending -> consumed exactly once, so two Web sessions
// racing on the same queued navigation produce a single winner.
func (s *Store) ClaimPetNavigationActions(ctx context.Context, now time.Time, consumedBy string, ids []string, limit int) ([]domain.PetNavigationAction, int, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	consumedBy = strings.TrimSpace(consumedBy)
	if consumedBy == "" {
		consumedBy = "web"
	}
	ids = cleanPetNavigationIDs(ids)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	nowMillis := now.UTC().UnixMilli()
	expiredResult, err := tx.ExecContext(ctx, `
		UPDATE pet_navigation_actions SET status=?
		WHERE status=? AND expires_at_ms<=?`,
		domain.PetNavigationExpired, domain.PetNavigationQueued, nowMillis)
	if err != nil {
		return nil, 0, fmt.Errorf("expire pet navigations: %w", err)
	}
	expired, _ := expiredResult.RowsAffected()

	query := `SELECT ` + petNavigationColumns + ` FROM pet_navigation_actions WHERE status=?`
	args := []any{domain.PetNavigationQueued}
	if len(ids) > 0 {
		query += ` AND id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + `)`
		for _, id := range ids {
			args = append(args, id)
		}
	}
	query += ` ORDER BY created_at_ms,id LIMIT ?`
	args = append(args, limit)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	candidates := []domain.PetNavigationAction{}
	for rows.Next() {
		item, err := scanPetNavigationAction(rows)
		if err != nil {
			_ = rows.Close()
			return nil, 0, err
		}
		candidates = append(candidates, item)
	}
	if err := rows.Close(); err != nil {
		return nil, 0, err
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	claimed := make([]domain.PetNavigationAction, 0, len(candidates))
	for _, item := range candidates {
		result, err := tx.ExecContext(ctx, `
			UPDATE pet_navigation_actions SET status=?,consumed_at_ms=?,consumed_by=?
			WHERE id=? AND status=?`,
			domain.PetNavigationConsumed, nowMillis, consumedBy, item.ID, domain.PetNavigationQueued)
		if err != nil {
			return nil, 0, err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			continue
		}
		item.Status = domain.PetNavigationConsumed
		item.ConsumedAt = now.UTC()
		item.ConsumedBy = consumedBy
		claimed = append(claimed, item)
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	return claimed, int(expired), nil
}

func cleanPetNavigationIDs(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	result := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		result = append(result, id)
		if len(result) >= 50 {
			break
		}
	}
	return result
}

func petNavigationMillis(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UTC().UnixMilli()
}

func scanPetNavigationAction(row interface{ Scan(...any) error }) (domain.PetNavigationAction, error) {
	var item domain.PetNavigationAction
	var created, expires, consumed int64
	if err := row.Scan(&item.ID, &item.TaskID, &item.Status, &created, &expires, &consumed, &item.ConsumedBy); err != nil {
		return domain.PetNavigationAction{}, err
	}
	item.CreatedAt = time.UnixMilli(created).UTC()
	item.ExpiresAt = time.UnixMilli(expires).UTC()
	if consumed > 0 {
		item.ConsumedAt = time.UnixMilli(consumed).UTC()
	}
	return item, nil
}
