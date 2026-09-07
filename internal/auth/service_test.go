package auth

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

type authRepositoryStub struct {
	owner    domain.Owner
	sessions map[string]domain.Session
}

func (r *authRepositoryStub) OwnerExists(context.Context) (bool, error) { return r.owner.ID != "", nil }
func (r *authRepositoryStub) CreateOwner(_ context.Context, owner domain.Owner) error {
	r.owner = owner
	return nil
}
func (r *authRepositoryStub) OwnerByUsername(_ context.Context, username string) (domain.Owner, error) {
	if r.owner.Username != username {
		return domain.Owner{}, sql.ErrNoRows
	}
	return r.owner, nil
}
func (r *authRepositoryStub) OwnerByID(_ context.Context, id string) (domain.Owner, error) {
	if r.owner.ID != id {
		return domain.Owner{}, sql.ErrNoRows
	}
	return r.owner, nil
}
func (r *authRepositoryStub) UpdateOwnerPassword(_ context.Context, ownerID, passwordHash string) error {
	if r.owner.ID != ownerID {
		return sql.ErrNoRows
	}
	r.owner.PasswordHash = passwordHash
	return nil
}
func (r *authRepositoryStub) TouchOwnerLogin(context.Context, string, string) error { return nil }
func (r *authRepositoryStub) CreateSession(_ context.Context, session domain.Session) error {
	if r.sessions == nil {
		r.sessions = map[string]domain.Session{}
	}
	r.sessions[session.ID] = session
	return nil
}
func (r *authRepositoryStub) SessionByTokenHash(_ context.Context, tokenHash string) (domain.Session, error) {
	for _, session := range r.sessions {
		if session.TokenHash == tokenHash {
			return session, nil
		}
	}
	return domain.Session{}, sql.ErrNoRows
}
func (r *authRepositoryStub) TouchSession(context.Context, string, string) error { return nil }
func (r *authRepositoryStub) RevokeSession(_ context.Context, sessionID, revokedAt string) error {
	session := r.sessions[sessionID]
	session.RevokedAt, _ = time.Parse(time.RFC3339Nano, revokedAt)
	r.sessions[sessionID] = session
	return nil
}
func (r *authRepositoryStub) RevokeOwnerSessions(_ context.Context, ownerID, exceptSessionID, revokedAt string) error {
	for id, session := range r.sessions {
		if session.OwnerID != ownerID || id == exceptSessionID || !session.RevokedAt.IsZero() {
			continue
		}
		session.RevokedAt, _ = time.Parse(time.RFC3339Nano, revokedAt)
		r.sessions[id] = session
	}
	return nil
}

func TestChangePasswordKeepsCurrentSessionAndRevokesOthers(t *testing.T) {
	ctx := context.Background()
	repository := &authRepositoryStub{}
	service := NewService(repository, "setup-test", time.Hour)
	service.passwordParams = PasswordParams{Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}
	registered, err := service.Register(ctx, "setup-test", "owner", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	other, err := service.Login(ctx, "owner", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ChangePassword(ctx, registered.Owner.ID, registered.Session.ID, "wrong-password", "new-correct-password"); err != ErrUnauthorized {
		t.Fatalf("wrong current password error = %v", err)
	}
	if err := service.ChangePassword(ctx, registered.Owner.ID, registered.Session.ID, "correct-horse-battery", "new-correct-password"); err != nil {
		t.Fatal(err)
	}
	if !repository.sessions[registered.Session.ID].RevokedAt.IsZero() {
		t.Fatal("current session was revoked")
	}
	if repository.sessions[other.Session.ID].RevokedAt.IsZero() {
		t.Fatal("other session was not revoked")
	}
	if _, err := service.Login(ctx, "owner", "correct-horse-battery"); err != ErrUnauthorized {
		t.Fatalf("old password error = %v", err)
	}
	if _, err := service.Login(ctx, "owner", "new-correct-password"); err != nil {
		t.Fatalf("new password login failed: %v", err)
	}
}

func TestRecoverRequiresSetupTokenAndRotatesEverySession(t *testing.T) {
	ctx := context.Background()
	repository := &authRepositoryStub{}
	service := NewService(repository, "setup-test", time.Hour)
	service.passwordParams = PasswordParams{Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}
	first, err := service.Register(ctx, "setup-test", "owner", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Login(ctx, "owner", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Recover(ctx, "wrong-token", "owner", "recovered-password"); err != ErrInvalidSetupToken {
		t.Fatalf("wrong setup token error = %v", err)
	}
	recovered, err := service.Recover(ctx, "setup-test", "owner", "recovered-password")
	if err != nil {
		t.Fatal(err)
	}
	if repository.sessions[first.Session.ID].RevokedAt.IsZero() || repository.sessions[second.Session.ID].RevokedAt.IsZero() {
		t.Fatal("recovery did not revoke old sessions")
	}
	if !repository.sessions[recovered.Session.ID].RevokedAt.IsZero() {
		t.Fatal("recovery session was revoked")
	}
}
