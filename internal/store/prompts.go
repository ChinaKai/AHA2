package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Store) PromptTemplateOverrides(ctx context.Context) (map[string]domain.PromptTemplate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,content,version,updated_at FROM prompt_template_overrides`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]domain.PromptTemplate{}
	for rows.Next() {
		var item domain.PromptTemplate
		var updatedAt string
		if err := rows.Scan(&item.ID, &item.Content, &item.Version, &updatedAt); err != nil {
			return nil, err
		}
		item.UpdatedAt = parseTime(updatedAt)
		result[item.ID] = item
	}
	return result, rows.Err()
}

func (s *Store) UpsertPromptTemplateOverride(ctx context.Context, id, content string, updatedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO prompt_template_overrides(id,content,version,updated_at) VALUES(?,?,1,?)
		ON CONFLICT(id) DO UPDATE SET content=excluded.content,version=prompt_template_overrides.version+1,updated_at=excluded.updated_at`,
		id, content, timeString(updatedAt),
	)
	return err
}

func (s *Store) DeletePromptTemplateOverride(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM prompt_template_overrides WHERE id=?`, id)
	return err
}

func (s *Store) PromptTemplateOverride(ctx context.Context, id string) (domain.PromptTemplate, error) {
	overrides, err := s.PromptTemplateOverrides(ctx)
	if err != nil {
		return domain.PromptTemplate{}, err
	}
	item, ok := overrides[id]
	if !ok {
		return domain.PromptTemplate{}, sql.ErrNoRows
	}
	return item, nil
}
