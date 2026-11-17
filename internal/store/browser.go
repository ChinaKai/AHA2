package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const browserProfileColumns = `id,name,browser_kind,host_id,state,last_used_at,created_at,updated_at`

func scanBrowserProfile(scanner interface{ Scan(...any) error }) (domain.BrowserProfile, error) {
	var item domain.BrowserProfile
	var lastUsedAt, createdAt, updatedAt string
	err := scanner.Scan(&item.ID, &item.Name, &item.BrowserKind, &item.HostID, &item.State,
		&lastUsedAt, &createdAt, &updatedAt)
	item.LastUsedAt = parseTime(lastUsedAt)
	item.CreatedAt = parseTime(createdAt)
	item.UpdatedAt = parseTime(updatedAt)
	return item, err
}

func (s *Store) BrowserProfiles(ctx context.Context) ([]domain.BrowserProfile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+browserProfileColumns+` FROM browser_profiles ORDER BY name,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.BrowserProfile, 0)
	for rows.Next() {
		item, err := scanBrowserProfile(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) BrowserProfile(ctx context.Context, id string) (domain.BrowserProfile, error) {
	return scanBrowserProfile(s.db.QueryRowContext(ctx,
		`SELECT `+browserProfileColumns+` FROM browser_profiles WHERE id=?`, id))
}

func (s *Store) UpsertBrowserProfile(ctx context.Context, item domain.BrowserProfile) error {
	if strings.TrimSpace(item.ID) == "" {
		return fmt.Errorf("browser profile id is required")
	}
	now := time.Now().UTC()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = now
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO browser_profiles(id,name,browser_kind,host_id,state,last_used_at,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name,browser_kind=excluded.browser_kind,host_id=excluded.host_id,
			state=excluded.state,last_used_at=excluded.last_used_at,updated_at=excluded.updated_at`,
		item.ID, item.Name, item.BrowserKind, item.HostID, item.State, timeString(item.LastUsedAt),
		timeString(item.CreatedAt), timeString(item.UpdatedAt),
	)
	return err
}

func (s *Store) UpdateBrowserProfileState(ctx context.Context, id, state string, lastUsedAt time.Time) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE browser_profiles SET state=?,last_used_at=?,updated_at=? WHERE id=?`,
		state, timeString(lastUsedAt), timeString(time.Now().UTC()), id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) DeleteBrowserProfile(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM browser_profiles WHERE id=?`, id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

const browserSessionColumns = `id,profile_id,host_id,host_session_id,state,error,started_at,last_used_at,closed_at,created_at,updated_at`

func scanBrowserSession(scanner interface{ Scan(...any) error }) (domain.BrowserSession, error) {
	var item domain.BrowserSession
	var startedAt, lastUsedAt, closedAt, createdAt, updatedAt string
	err := scanner.Scan(&item.ID, &item.ProfileID, &item.HostID, &item.HostSessionID, &item.State, &item.Error,
		&startedAt, &lastUsedAt, &closedAt, &createdAt, &updatedAt)
	item.StartedAt = parseTime(startedAt)
	item.LastUsedAt = parseTime(lastUsedAt)
	item.ClosedAt = parseTime(closedAt)
	item.CreatedAt = parseTime(createdAt)
	item.UpdatedAt = parseTime(updatedAt)
	return item, err
}

func (s *Store) BrowserSession(ctx context.Context, id string) (domain.BrowserSession, error) {
	return scanBrowserSession(s.db.QueryRowContext(ctx,
		`SELECT `+browserSessionColumns+` FROM browser_sessions WHERE id=?`, id))
}

// ActiveBrowserSession returns the profile's non-closed session. sql.ErrNoRows
// means the profile currently has no instance.
func (s *Store) ActiveBrowserSession(ctx context.Context, profileID string) (domain.BrowserSession, error) {
	return scanBrowserSession(s.db.QueryRowContext(ctx,
		`SELECT `+browserSessionColumns+` FROM browser_sessions
		 WHERE profile_id=? AND state<>'closed' ORDER BY created_at DESC LIMIT 1`, profileID))
}

func (s *Store) BrowserSessions(ctx context.Context) ([]domain.BrowserSession, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+browserSessionColumns+` FROM browser_sessions ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.BrowserSession, 0)
	for rows.Next() {
		item, err := scanBrowserSession(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) CreateBrowserSession(ctx context.Context, item domain.BrowserSession) error {
	if strings.TrimSpace(item.ID) == "" {
		return fmt.Errorf("browser session id is required")
	}
	now := time.Now().UTC()
	if item.StartedAt.IsZero() {
		item.StartedAt = now
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = item.StartedAt
	}
	item.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO browser_sessions(id,profile_id,host_id,host_session_id,state,error,started_at,
			last_used_at,closed_at,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		item.ID, item.ProfileID, item.HostID, item.HostSessionID, item.State, item.Error,
		timeString(item.StartedAt), timeString(item.LastUsedAt), timeString(item.ClosedAt),
		timeString(item.CreatedAt), timeString(item.UpdatedAt),
	)
	return err
}

func (s *Store) UpdateBrowserSession(ctx context.Context, item domain.BrowserSession) error {
	item.UpdatedAt = time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
		UPDATE browser_sessions SET host_session_id=?,state=?,error=?,last_used_at=?,closed_at=?,updated_at=?
		WHERE id=?`,
		item.HostSessionID, item.State, item.Error, timeString(item.LastUsedAt),
		timeString(item.ClosedAt), timeString(item.UpdatedAt), item.ID)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

const browserGrantColumns = `id,task_id,profile_id,session_id,capabilities_json,expires_at,revoked_at,created_at,updated_at`

func scanBrowserGrant(scanner interface{ Scan(...any) error }) (domain.BrowserTaskGrant, error) {
	var item domain.BrowserTaskGrant
	var capabilities, expiresAt, revokedAt, createdAt, updatedAt string
	err := scanner.Scan(&item.ID, &item.TaskID, &item.ProfileID, &item.SessionID, &capabilities,
		&expiresAt, &revokedAt, &createdAt, &updatedAt)
	item.Capabilities = decodeJSON(capabilities, []string{})
	item.ExpiresAt = parseTime(expiresAt)
	item.RevokedAt = parseTime(revokedAt)
	item.CreatedAt = parseTime(createdAt)
	item.UpdatedAt = parseTime(updatedAt)
	return item, err
}

func (s *Store) BrowserTaskGrant(ctx context.Context, id string) (domain.BrowserTaskGrant, error) {
	return scanBrowserGrant(s.db.QueryRowContext(ctx,
		`SELECT `+browserGrantColumns+` FROM browser_task_grants WHERE id=?`, id))
}

func (s *Store) BrowserTaskGrants(ctx context.Context, taskID string) ([]domain.BrowserTaskGrant, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+browserGrantColumns+` FROM browser_task_grants WHERE task_id=? ORDER BY created_at DESC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.BrowserTaskGrant, 0)
	for rows.Next() {
		item, err := scanBrowserGrant(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// BrowserTaskGrantsForProfileActive returns unrevoked, unexpired grants for a
// profile. Profile deletion uses it to avoid orphaning a live Task permission.
func (s *Store) BrowserTaskGrantsForProfileActive(ctx context.Context, profileID string, now time.Time) ([]domain.BrowserTaskGrant, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+browserGrantColumns+` FROM browser_task_grants
		 WHERE profile_id=? AND revoked_at='' AND expires_at>? ORDER BY created_at`, profileID, timeString(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.BrowserTaskGrant, 0)
	for rows.Next() {
		item, err := scanBrowserGrant(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) BrowserGrantsForSession(ctx context.Context, sessionID string) ([]domain.BrowserTaskGrant, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+browserGrantColumns+` FROM browser_task_grants WHERE session_id=? AND revoked_at='' ORDER BY created_at`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.BrowserTaskGrant, 0)
	for rows.Next() {
		item, err := scanBrowserGrant(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) CreateBrowserTaskGrant(ctx context.Context, item domain.BrowserTaskGrant) error {
	if strings.TrimSpace(item.ID) == "" {
		return fmt.Errorf("browser grant id is required")
	}
	now := time.Now().UTC()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO browser_task_grants(id,task_id,profile_id,session_id,capabilities_json,
			expires_at,revoked_at,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?)`,
		item.ID, item.TaskID, item.ProfileID, item.SessionID, encodeJSON(item.Capabilities),
		timeString(item.ExpiresAt), timeString(item.RevokedAt),
		timeString(item.CreatedAt), timeString(item.UpdatedAt),
	)
	return err
}

func (s *Store) RevokeBrowserTaskGrant(ctx context.Context, id string, revokedAt time.Time) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE browser_task_grants SET revoked_at=?,updated_at=? WHERE id=? AND revoked_at=''`,
		timeString(revokedAt), timeString(time.Now().UTC()), id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) DeleteBrowserTaskGrant(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM browser_task_grants WHERE id=?`, id)
	return err
}

const browserTabColumns = `id,session_id,grant_id,task_id,host_tab_id,url,title,created_at,updated_at`

func scanBrowserTab(scanner interface{ Scan(...any) error }) (domain.BrowserTab, error) {
	var item domain.BrowserTab
	var createdAt, updatedAt string
	err := scanner.Scan(&item.ID, &item.SessionID, &item.GrantID, &item.TaskID, &item.HostTabID,
		&item.URL, &item.Title, &createdAt, &updatedAt)
	item.CreatedAt = parseTime(createdAt)
	item.UpdatedAt = parseTime(updatedAt)
	return item, err
}

func (s *Store) BrowserTab(ctx context.Context, id string) (domain.BrowserTab, error) {
	return scanBrowserTab(s.db.QueryRowContext(ctx,
		`SELECT `+browserTabColumns+` FROM browser_tabs WHERE id=?`, id))
}

func (s *Store) BrowserTabsForGrant(ctx context.Context, grantID string) ([]domain.BrowserTab, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+browserTabColumns+` FROM browser_tabs WHERE grant_id=? ORDER BY created_at,id`, grantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.BrowserTab, 0)
	for rows.Next() {
		item, err := scanBrowserTab(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) BrowserTabsForSession(ctx context.Context, sessionID string) ([]domain.BrowserTab, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+browserTabColumns+` FROM browser_tabs WHERE session_id=? ORDER BY created_at,id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.BrowserTab, 0)
	for rows.Next() {
		item, err := scanBrowserTab(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) BrowserTabsForTask(ctx context.Context, taskID string) ([]domain.BrowserTab, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+browserTabColumns+` FROM browser_tabs WHERE task_id=? ORDER BY created_at,id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.BrowserTab, 0)
	for rows.Next() {
		item, err := scanBrowserTab(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) CreateBrowserTab(ctx context.Context, item domain.BrowserTab) error {
	if strings.TrimSpace(item.ID) == "" {
		return fmt.Errorf("browser tab id is required")
	}
	now := time.Now().UTC()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO browser_tabs(id,session_id,grant_id,task_id,host_tab_id,url,title,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?)`,
		item.ID, item.SessionID, item.GrantID, item.TaskID, item.HostTabID, item.URL, item.Title,
		timeString(item.CreatedAt), timeString(item.UpdatedAt),
	)
	return err
}

func (s *Store) UpdateBrowserTab(ctx context.Context, item domain.BrowserTab) error {
	item.UpdatedAt = time.Now().UTC()
	result, err := s.db.ExecContext(ctx,
		`UPDATE browser_tabs SET url=?,title=?,updated_at=? WHERE id=?`,
		item.URL, item.Title, timeString(item.UpdatedAt), item.ID)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) DeleteBrowserTab(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM browser_tabs WHERE id=?`, id)
	return err
}

// BrowserAuditEvent is a minimal audit row used by tests and diagnostics.
type BrowserAuditEvent struct {
	Sequence     int64          `json:"sequence"`
	ID           string         `json:"id"`
	OwnerID      string         `json:"owner_id"`
	Action       string         `json:"action"`
	ResourceType string         `json:"resource_type"`
	ResourceID   string         `json:"resource_id"`
	Data         map[string]any `json:"data"`
	OccurredAt   time.Time      `json:"occurred_at"`
}

// BrowserAuditEvents exposes the audit trail for tests and diagnostics without
// opening the SQLite handle. Only metadata rows are returned; page content is
// never written to the audit table by the browser service.
func (s *Store) BrowserAuditEvents(ctx context.Context, action string, limit int) ([]BrowserAuditEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT sequence,id,owner_id,action,resource_type,resource_id,data_json,occurred_at
		FROM audit_events WHERE action=? ORDER BY sequence DESC LIMIT ?`, action, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]BrowserAuditEvent, 0)
	for rows.Next() {
		var item BrowserAuditEvent
		var data, occurredAt string
		if err := rows.Scan(&item.Sequence, &item.ID, &item.OwnerID, &item.Action,
			&item.ResourceType, &item.ResourceID, &data, &occurredAt); err != nil {
			return nil, err
		}
		item.Data = decodeJSON(data, map[string]any{})
		item.OccurredAt = parseTime(occurredAt)
		result = append(result, item)
	}
	return result, rows.Err()
}
