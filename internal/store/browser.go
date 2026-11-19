package store

import (
	"context"
	"database/sql"
	"errors"
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

// ErrBrowserAccessInactive is returned by the guarded tab writes when the
// grant, session or active-session ownership changed between authorize and the
// database write.
var ErrBrowserAccessInactive = errors.New("browser access is inactive")

// TouchBrowserSession updates only the usage timestamp. It deliberately never
// writes state/closed_at: a slow host call must not resurrect a session that
// was closed while the call was in flight.
func (s *Store) TouchBrowserSession(ctx context.Context, id string, lastUsedAt time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE browser_sessions SET last_used_at=?,updated_at=? WHERE id=? AND state<>'closed'`,
		timeString(lastUsedAt), timeString(time.Now().UTC()), id)
	return err
}

func (s *Store) TouchBrowserProfile(ctx context.Context, id string, lastUsedAt time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE browser_profiles SET last_used_at=?,updated_at=? WHERE id=?`,
		timeString(lastUsedAt), timeString(time.Now().UTC()), id)
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

const browserGrantColumns = `id,task_id,profile_id,session_id,capabilities_json,expires_at,revoked_at,created_at,updated_at,
	hold_id,hold_original_expires_at,hold_expires_at`

func scanBrowserGrant(scanner interface{ Scan(...any) error }) (domain.BrowserTaskGrant, error) {
	var item domain.BrowserTaskGrant
	var capabilities, expiresAt, revokedAt, createdAt, updatedAt string
	var holdID, holdOriginalExpiresAt, holdExpiresAt string
	err := scanner.Scan(&item.ID, &item.TaskID, &item.ProfileID, &item.SessionID, &capabilities,
		&expiresAt, &revokedAt, &createdAt, &updatedAt,
		&holdID, &holdOriginalExpiresAt, &holdExpiresAt)
	item.Capabilities = decodeJSON(capabilities, []string{})
	item.ExpiresAt = parseTime(expiresAt)
	item.HoldID = holdID
	item.HoldOriginalExpiresAt = parseTime(holdOriginalExpiresAt)
	item.HoldExpiresAt = parseTime(holdExpiresAt)
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
// profile. An empty expires_at means the Owner switch is on and the grant has
// no time limit; only an explicit release or session close clears it. Profile
// deletion uses it to avoid orphaning a live Task permission.
func (s *Store) BrowserTaskGrantsForProfileActive(ctx context.Context, profileID string, now time.Time) ([]domain.BrowserTaskGrant, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+browserGrantColumns+` FROM browser_task_grants
		 WHERE profile_id=? AND revoked_at='' AND (expires_at='' OR expires_at>?) ORDER BY created_at`, profileID, timeString(now))
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

// BrowserProfileInUseError means another Task already holds the exclusive
// lease for this Profile. The service maps it to 409 browser_profile_in_use.
type BrowserProfileInUseError struct {
	Grant domain.BrowserTaskGrant
}

func (e *BrowserProfileInUseError) Error() string {
	return "browser profile is already controlled by another task"
}

// BrowserTaskProfileLimitError means the Task already holds an active lease on
// another Profile. It must release that one first; acquisition never silently
// revokes it.
type BrowserTaskProfileLimitError struct {
	Grant domain.BrowserTaskGrant
}

func (e *BrowserTaskProfileLimitError) Error() string {
	return "task already controls another browser profile"
}

// AcquireBrowserTaskGrantExclusive creates the Profile's single active lease.
// A same-Task or expired lease is revoked; a live lease owned by another Task
// aborts the transaction. The partial unique index on profile_id is the
// cross-process backstop.
func (s *Store) AcquireBrowserTaskGrantExclusive(ctx context.Context, item domain.BrowserTaskGrant, now time.Time) (domain.BrowserTaskGrant, []domain.BrowserTaskGrant, bool, error) {
	if strings.TrimSpace(item.ID) == "" {
		return domain.BrowserTaskGrant{}, nil, false, fmt.Errorf("browser grant id is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.BrowserTaskGrant{}, nil, false, err
	}
	defer tx.Rollback()
	// A Task holds at most one Profile lease. An active same-Profile lease is
	// reused idempotently; an active lease on another Profile rejects the
	// acquisition instead of silently revoking it. Expired rows are cleaned up
	// in this transaction.
	rows, err := tx.QueryContext(ctx,
		`SELECT `+browserGrantColumns+` FROM browser_task_grants
		 WHERE revoked_at='' AND (profile_id=? OR task_id=?)
		 ORDER BY created_at,id`, item.ProfileID, item.TaskID)
	if err != nil {
		return domain.BrowserTaskGrant{}, nil, false, err
	}
	revoked := make([]domain.BrowserTaskGrant, 0)
	var existingLease *domain.BrowserTaskGrant
	for rows.Next() {
		existing, scanErr := scanBrowserGrant(rows)
		if scanErr != nil {
			rows.Close()
			return domain.BrowserTaskGrant{}, nil, false, scanErr
		}
		if existing.TaskID == item.TaskID {
			if existing.ProfileID == item.ProfileID {
				if existing.Active(now) {
					copy := existing
					existingLease = &copy
					continue
				}
				revoked = append(revoked, existing)
				continue
			}
			if existing.Active(now) {
				rows.Close()
				return domain.BrowserTaskGrant{}, nil, false, &BrowserTaskProfileLimitError{Grant: existing}
			}
			revoked = append(revoked, existing)
			continue
		}
		if existing.Active(now) {
			rows.Close()
			return domain.BrowserTaskGrant{}, nil, false, &BrowserProfileInUseError{Grant: existing}
		}
		revoked = append(revoked, existing)
	}
	if err := rows.Close(); err != nil {
		return domain.BrowserTaskGrant{}, nil, false, err
	}
	if err := rows.Err(); err != nil {
		return domain.BrowserTaskGrant{}, nil, false, err
	}
	if existingLease != nil {
		// Still clean up any expired rows seen for this Task/Profile.
		for _, existing := range revoked {
			if _, err := tx.ExecContext(ctx,
				`UPDATE browser_task_grants SET revoked_at=?,updated_at=? WHERE id=? AND revoked_at=''`,
				timeString(now), timeString(now), existing.ID); err != nil {
				return domain.BrowserTaskGrant{}, nil, false, err
			}
		}
		if err := tx.Commit(); err != nil {
			return domain.BrowserTaskGrant{}, nil, false, err
		}
		return *existingLease, nil, true, nil
	}
	for _, existing := range revoked {
		if _, err := tx.ExecContext(ctx,
			`UPDATE browser_task_grants SET revoked_at=?,updated_at=? WHERE id=? AND revoked_at=''`,
			timeString(now), timeString(now), existing.ID); err != nil {
			return domain.BrowserTaskGrant{}, nil, false, err
		}
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO browser_task_grants(id,task_id,profile_id,session_id,capabilities_json,
			expires_at,revoked_at,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?)`,
		item.ID, item.TaskID, item.ProfileID, item.SessionID, encodeJSON(item.Capabilities),
		timeString(item.ExpiresAt), timeString(item.RevokedAt),
		timeString(item.CreatedAt), timeString(item.UpdatedAt),
	); err != nil {
		return domain.BrowserTaskGrant{}, nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return domain.BrowserTaskGrant{}, nil, false, err
	}
	for index := range revoked {
		revoked[index].RevokedAt = now
		revoked[index].UpdatedAt = now
	}
	return item, revoked, false, nil
}

// TransferBrowserTaskGrantExclusive revokes oldGrantID and inserts the new
// lease in one transaction. The caller only reaches this after an Owner action.
func (s *Store) TransferBrowserTaskGrantExclusive(ctx context.Context, oldGrantID string, item domain.BrowserTaskGrant, now time.Time) (domain.BrowserTaskGrant, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.BrowserTaskGrant{}, err
	}
	defer tx.Rollback()
	old, err := scanBrowserGrant(tx.QueryRowContext(ctx,
		`SELECT `+browserGrantColumns+` FROM browser_task_grants WHERE id=?`, oldGrantID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.BrowserTaskGrant{}, ErrBrowserAccessInactive
	}
	if err != nil {
		return domain.BrowserTaskGrant{}, err
	}
	if !old.Active(now) || old.ProfileID != item.ProfileID {
		return domain.BrowserTaskGrant{}, ErrBrowserAccessInactive
	}
	// The target Task must not already control another Profile; the Owner can
	// release that lease first. This avoids a transfer silently revoking it.
	other, err := tx.QueryContext(ctx,
		`SELECT `+browserGrantColumns+` FROM browser_task_grants
		 WHERE task_id=? AND revoked_at='' AND profile_id<>? ORDER BY created_at,id`,
		item.TaskID, item.ProfileID)
	if err != nil {
		return domain.BrowserTaskGrant{}, err
	}
	var otherLeases []domain.BrowserTaskGrant
	for other.Next() {
		lease, scanErr := scanBrowserGrant(other)
		if scanErr != nil {
			other.Close()
			return domain.BrowserTaskGrant{}, scanErr
		}
		otherLeases = append(otherLeases, lease)
	}
	if err := other.Close(); err != nil {
		return domain.BrowserTaskGrant{}, err
	}
	if err := other.Err(); err != nil {
		return domain.BrowserTaskGrant{}, err
	}
	for _, lease := range otherLeases {
		if lease.Active(now) {
			return domain.BrowserTaskGrant{}, &BrowserTaskProfileLimitError{Grant: lease}
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE browser_task_grants SET revoked_at=?,updated_at=? WHERE id=? AND revoked_at=''`,
			timeString(now), timeString(now), lease.ID); err != nil {
			return domain.BrowserTaskGrant{}, err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE browser_task_grants SET revoked_at=?,updated_at=? WHERE id=? AND revoked_at=''`,
		timeString(now), timeString(now), old.ID); err != nil {
		return domain.BrowserTaskGrant{}, err
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO browser_task_grants(id,task_id,profile_id,session_id,capabilities_json,
			expires_at,revoked_at,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?)`,
		item.ID, item.TaskID, item.ProfileID, item.SessionID, encodeJSON(item.Capabilities),
		timeString(item.ExpiresAt), timeString(item.RevokedAt),
		timeString(item.CreatedAt), timeString(item.UpdatedAt),
	); err != nil {
		return domain.BrowserTaskGrant{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.BrowserTaskGrant{}, err
	}
	old.RevokedAt = now
	old.UpdatedAt = now
	return old, nil
}

func (s *Store) CreateBrowserTaskGrant(ctx context.Context, item domain.BrowserTaskGrant) error {
	if strings.TrimSpace(item.ID) == "" {
		return fmt.Errorf("browser grant id is required")
	}
	now := time.Now().UTC()
	// Compatibility for callers that only revoke "active" grants: an expired
	// but unrevoked row would otherwise occupy the v148 unique slot and make
	// the new insert fail. Revoking expired rows here is still forward-only.
	if _, err := s.db.ExecContext(ctx,
		`UPDATE browser_task_grants SET revoked_at=?,updated_at=?
		 WHERE task_id=? AND profile_id=? AND revoked_at='' AND expires_at<>'' AND expires_at<=?`,
		timeString(now), timeString(now), item.TaskID, item.ProfileID, timeString(now)); err != nil {
		return err
	}
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

// RefreshBrowserTaskGrantLease keeps the same active lease row while applying
// the caller's capabilities/TTL. Idempotent re-acquire must not revoke and
// rebuild the lease.
func (s *Store) RefreshBrowserTaskGrantLease(ctx context.Context, id string, capabilities []string, expiresAt time.Time, now time.Time) (domain.BrowserTaskGrant, error) {
	result, err := s.db.ExecContext(ctx,
		`UPDATE browser_task_grants SET capabilities_json=?,expires_at=?,
			hold_id='',hold_original_expires_at='',hold_expires_at='',updated_at=?
		 WHERE id=? AND revoked_at=''`,
		encodeJSON(capabilities), timeString(expiresAt), timeString(now), id)
	if err != nil {
		return domain.BrowserTaskGrant{}, err
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return domain.BrowserTaskGrant{}, sql.ErrNoRows
	}
	return s.BrowserTaskGrant(ctx, id)
}

var ErrBrowserGrantHoldActive = errors.New("browser grant hold is already active")

// BeginBrowserTaskGrantHold installs a uniquely identified temporary lease
// extension. It only updates an active row with the expected expiry and no
// existing hold, so a concurrent owner renewal/revoke or a second hold cannot
// be overwritten. The caller must later release exactly this holdID.
func (s *Store) BeginBrowserTaskGrantHold(ctx context.Context, id string, expectedExpiry time.Time, holdID string, holdUntil, now time.Time) (time.Time, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return time.Time{}, err
	}
	defer tx.Rollback()
	current, err := scanBrowserGrant(tx.QueryRowContext(ctx,
		`SELECT `+browserGrantColumns+` FROM browser_task_grants WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, ErrBrowserAccessInactive
	}
	if err != nil {
		return time.Time{}, err
	}
	if !current.RevokedAt.IsZero() || !current.ExpiresAt.After(now) ||
		!current.ExpiresAt.Equal(expectedExpiry) || current.HoldID != "" ||
		!holdUntil.After(current.ExpiresAt) {
		return time.Time{}, ErrBrowserGrantHoldActive
	}
	result, err := tx.ExecContext(ctx,
		`UPDATE browser_task_grants SET expires_at=?,hold_id=?,hold_original_expires_at=?,
			hold_expires_at=?,updated_at=?
		 WHERE id=? AND revoked_at='' AND expires_at=? AND hold_id=''`,
		timeString(holdUntil), holdID, timeString(current.ExpiresAt), timeString(holdUntil),
		timeString(now), id, timeString(current.ExpiresAt))
	if err != nil {
		return time.Time{}, err
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return time.Time{}, ErrBrowserGrantHoldActive
	}
	if err := tx.Commit(); err != nil {
		return time.Time{}, err
	}
	return current.ExpiresAt, nil
}

// ReleaseBrowserTaskGrantHold restores only the expiry snapshot owned by
// holdID. If the owner renewed/revoked the lease or another hold replaced it,
// no row is touched and released=false.
func (s *Store) ReleaseBrowserTaskGrantHold(ctx context.Context, id, holdID string, now time.Time) (bool, error) {
	result, err := s.db.ExecContext(ctx,
		`UPDATE browser_task_grants
		 SET expires_at=hold_original_expires_at,hold_id='',hold_original_expires_at='',
		     hold_expires_at='',updated_at=?
		 WHERE id=? AND hold_id=? AND revoked_at=''`,
		timeString(now), id, holdID)
	if err != nil {
		return false, err
	}
	affected, _ := result.RowsAffected()
	return affected == 1, nil
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

const browserTabColumns = `id,session_id,grant_id,task_id,host_tab_id,url,title,origin,created_at,updated_at`

func browserTabOrigin(item domain.BrowserTab) string {
	if item.Origin == domain.BrowserTabOriginOwner {
		return domain.BrowserTabOriginOwner
	}
	return domain.BrowserTabOriginAgent
}

func scanBrowserTab(scanner interface{ Scan(...any) error }) (domain.BrowserTab, error) {
	var item domain.BrowserTab
	var createdAt, updatedAt string
	err := scanner.Scan(&item.ID, &item.SessionID, &item.GrantID, &item.TaskID, &item.HostTabID,
		&item.URL, &item.Title, &item.Origin, &createdAt, &updatedAt)
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

// UpsertBrowserTabForActiveGrant registers a host page under the exclusive
// lease. Existing rows are re-assigned to the current Task/grant and refreshed;
// the row ID and origin stay stable. The grant/session check and write share one
// transaction so a revoke during the host call cannot leave an orphan binding.
func (s *Store) UpsertBrowserTabForActiveGrant(ctx context.Context, item domain.BrowserTab, now time.Time) (domain.BrowserTab, error) {
	if strings.TrimSpace(item.ID) == "" {
		return domain.BrowserTab{}, fmt.Errorf("browser tab id is required")
	}
	if strings.TrimSpace(item.HostTabID) == "" {
		return domain.BrowserTab{}, fmt.Errorf("browser host tab id is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.BrowserTab{}, err
	}
	defer tx.Rollback()
	var active int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM browser_task_grants g
		JOIN browser_sessions s ON s.id=g.session_id
		WHERE g.id=? AND g.task_id=? AND g.session_id=?
		  AND g.revoked_at='' AND (g.expires_at='' OR g.expires_at>?)
		  AND s.state<>'closed'
		  AND NOT EXISTS(
			SELECT 1 FROM browser_sessions other
			WHERE other.profile_id=g.profile_id AND other.state<>'closed' AND other.id<>g.session_id
		  )`,
		item.GrantID, item.TaskID, item.SessionID, timeString(now)).Scan(&active); err != nil {
		return domain.BrowserTab{}, err
	}
	if active != 1 {
		return domain.BrowserTab{}, ErrBrowserAccessInactive
	}
	now = now.UTC()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO browser_tabs(id,session_id,grant_id,task_id,host_tab_id,url,title,origin,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(session_id,host_tab_id) WHERE host_tab_id <> '' DO UPDATE SET
			grant_id=excluded.grant_id,task_id=excluded.task_id,url=excluded.url,
			title=excluded.title,updated_at=excluded.updated_at`,
		item.ID, item.SessionID, item.GrantID, item.TaskID, item.HostTabID, item.URL, item.Title,
		browserTabOrigin(item), timeString(item.CreatedAt), timeString(item.UpdatedAt),
	); err != nil {
		return domain.BrowserTab{}, err
	}
	stored, err := scanBrowserTab(tx.QueryRowContext(ctx,
		`SELECT `+browserTabColumns+` FROM browser_tabs WHERE session_id=? AND host_tab_id=?`,
		item.SessionID, item.HostTabID))
	if err != nil {
		return domain.BrowserTab{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.BrowserTab{}, err
	}
	return stored, nil
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
		INSERT INTO browser_tabs(id,session_id,grant_id,task_id,host_tab_id,url,title,origin,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)`,
		item.ID, item.SessionID, item.GrantID, item.TaskID, item.HostTabID, item.URL, item.Title,
		browserTabOrigin(item), timeString(item.CreatedAt), timeString(item.UpdatedAt),
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
