package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const onboardingMirrorExists = `EXISTS(SELECT 1 FROM workspaces WHERE read_only=1)
	OR EXISTS(SELECT 1 FROM sync_remote_task_objects WHERE object_type='task')`

const onboardingTaskExists = `EXISTS(SELECT 1 FROM tasks
	WHERE id<>'task_pet' AND project_id<>'project_pet'
	AND NOT EXISTS(SELECT 1 FROM owner_assistants WHERE pet_task_id=tasks.id))`

func (s *Store) migrateOnboarding(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var applied bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=159)`).Scan(&applied); err != nil || applied {
		return err
	}
	var hasDismissedAt bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pragma_table_info('general_settings')
		WHERE name='onboarding_dismissed_at')`).Scan(&hasDismissedAt); err != nil {
		return err
	}
	if !hasDismissedAt {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE general_settings ADD COLUMN onboarding_dismissed_at TEXT`); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO general_settings(id,language,updated_at)
		VALUES(1,'auto',strftime('%Y-%m-%dT%H:%M:%fZ','now'))`); err != nil {
		return err
	}
	// Classify rows already present at upgrade. A model added during this
	// version's onboarding must leave the rest of the checklist visible.
	if _, err := tx.ExecContext(ctx, `
		UPDATE general_settings SET onboarding_dismissed_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')
		WHERE id=1 AND onboarding_dismissed_at IS NULL
		AND (EXISTS(SELECT 1 FROM projects WHERE project_type NOT IN ('system','channel'))
			OR EXISTS(SELECT 1 FROM models WHERE deleted_at='')
			OR `+onboardingTaskExists+` OR `+onboardingMirrorExists+`)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at)
		VALUES(159,strftime('%Y-%m-%dT%H:%M:%fZ','now'))`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) OnboardingVisibility(ctx context.Context) (dismissedAt *time.Time, showAutomatically bool, err error) {
	var dismissed sql.NullString
	err = s.db.QueryRowContext(ctx, `SELECT onboarding_dismissed_at,
		NOT (`+onboardingMirrorExists+` OR `+onboardingTaskExists+`)
		FROM general_settings WHERE id=1`).Scan(&dismissed, &showAutomatically)
	if err == nil && dismissed.Valid {
		at := parseTime(dismissed.String)
		dismissedAt, showAutomatically = &at, false
	}
	return
}

func (s *Store) DismissOnboarding(ctx context.Context, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE general_settings
		SET onboarding_dismissed_at=COALESCE(onboarding_dismissed_at,?) WHERE id=1`, timeString(now))
	return err
}

// OnboardingTask returns the first template Task still present in a local
// Workspace. Its ID is an ordinary Task ID with a prefix for this entry point.
func (s *Store) OnboardingTask(ctx context.Context) (domain.Task, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT t.id FROM tasks t JOIN workspaces w ON w.id=t.workspace_id
		WHERE t.id GLOB 'task_starter_*' AND w.read_only=0 ORDER BY t.created_at,t.id LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Task{}, nil
	}
	if err != nil {
		return domain.Task{}, err
	}
	return s.Task(ctx, id)
}
