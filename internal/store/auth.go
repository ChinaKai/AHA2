package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Store) OwnerExists(ctx context.Context) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM owners`).Scan(&count); err != nil {
		return false, fmt.Errorf("count owners: %w", err)
	}
	return count > 0, nil
}

func (s *Store) CreateOwner(ctx context.Context, owner domain.Owner) error {
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO owners(id, username, password_hash, created_at, last_login_at) VALUES(?,?,?,?,?)`,
		owner.ID, owner.Username, owner.PasswordHash, timeString(owner.CreatedAt), timeString(owner.LastLoginAt),
	)
	if err != nil {
		return fmt.Errorf("create owner: %w", err)
	}
	return nil
}

func (s *Store) OwnerByUsername(ctx context.Context, username string) (domain.Owner, error) {
	var owner domain.Owner
	var createdAt, lastLoginAt string
	err := s.db.QueryRowContext(
		ctx,
		`SELECT id, username, password_hash, created_at, last_login_at FROM owners WHERE username = ?`,
		username,
	).Scan(&owner.ID, &owner.Username, &owner.PasswordHash, &createdAt, &lastLoginAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Owner{}, sql.ErrNoRows
	}
	if err != nil {
		return domain.Owner{}, fmt.Errorf("read owner: %w", err)
	}
	owner.CreatedAt = parseTime(createdAt)
	owner.LastLoginAt = parseTime(lastLoginAt)
	return owner, nil
}

func (s *Store) OwnerByID(ctx context.Context, id string) (domain.Owner, error) {
	var owner domain.Owner
	var createdAt, lastLoginAt string
	err := s.db.QueryRowContext(
		ctx,
		`SELECT id, username, password_hash, created_at, last_login_at FROM owners WHERE id = ?`,
		id,
	).Scan(&owner.ID, &owner.Username, &owner.PasswordHash, &createdAt, &lastLoginAt)
	if err != nil {
		return domain.Owner{}, err
	}
	owner.CreatedAt = parseTime(createdAt)
	owner.LastLoginAt = parseTime(lastLoginAt)
	return owner, nil
}

func (s *Store) TouchOwnerLogin(ctx context.Context, ownerID string, value string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE owners SET last_login_at = ? WHERE id = ?`, value, ownerID)
	return err
}

func (s *Store) CreateSession(ctx context.Context, session domain.Session) error {
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO sessions(id, owner_id, token_hash, csrf_token, created_at, last_seen_at, expires_at, revoked_at)
		 VALUES(?,?,?,?,?,?,?,?)`,
		session.ID, session.OwnerID, session.TokenHash, session.CSRFToken,
		timeString(session.CreatedAt), timeString(session.LastSeen), timeString(session.ExpiresAt), timeString(session.RevokedAt),
	)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

func (s *Store) SessionByTokenHash(ctx context.Context, tokenHash string) (domain.Session, error) {
	var session domain.Session
	var createdAt, lastSeen, expiresAt, revokedAt string
	err := s.db.QueryRowContext(
		ctx,
		`SELECT id, owner_id, token_hash, csrf_token, created_at, last_seen_at, expires_at, revoked_at
		 FROM sessions WHERE token_hash = ?`,
		tokenHash,
	).Scan(
		&session.ID, &session.OwnerID, &session.TokenHash, &session.CSRFToken,
		&createdAt, &lastSeen, &expiresAt, &revokedAt,
	)
	if err != nil {
		return domain.Session{}, err
	}
	session.CreatedAt = parseTime(createdAt)
	session.LastSeen = parseTime(lastSeen)
	session.ExpiresAt = parseTime(expiresAt)
	session.RevokedAt = parseTime(revokedAt)
	return session, nil
}

func (s *Store) TouchSession(ctx context.Context, sessionID, lastSeen string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ? WHERE id = ?`, lastSeen, sessionID)
	return err
}

func (s *Store) RevokeSession(ctx context.Context, sessionID, revokedAt string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET revoked_at = ? WHERE id = ?`, revokedAt, sessionID)
	return err
}
