package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

var (
	ErrUnauthorized      = errors.New("unauthorized")
	ErrOwnerExists       = errors.New("owner already exists")
	ErrInvalidSetupToken = errors.New("invalid setup token")
)

type Repository interface {
	OwnerExists(context.Context) (bool, error)
	CreateOwner(context.Context, domain.Owner) error
	OwnerByUsername(context.Context, string) (domain.Owner, error)
	OwnerByID(context.Context, string) (domain.Owner, error)
	UpdateOwnerPassword(context.Context, string, string) error
	TouchOwnerLogin(context.Context, string, string) error
	CreateSession(context.Context, domain.Session) error
	SessionByTokenHash(context.Context, string) (domain.Session, error)
	TouchSession(context.Context, string, string) error
	RevokeSession(context.Context, string, string) error
	RevokeOwnerSessions(context.Context, string, string, string) error
}

type Service struct {
	repository     Repository
	setupToken     string
	sessionTTL     time.Duration
	passwordParams PasswordParams
	now            func() time.Time
}

type LoginResult struct {
	Owner        domain.Owner
	Session      domain.Session
	SessionToken string
}

func NewService(repository Repository, setupToken string, sessionTTL time.Duration) *Service {
	if sessionTTL <= 0 {
		sessionTTL = 14 * 24 * time.Hour
	}
	return &Service{
		repository:     repository,
		setupToken:     setupToken,
		sessionTTL:     sessionTTL,
		passwordParams: DefaultPasswordParams,
		now:            time.Now,
	}
}

func (s *Service) RegistrationOpen(ctx context.Context) (bool, error) {
	exists, err := s.repository.OwnerExists(ctx)
	return !exists, err
}

func (s *Service) Register(ctx context.Context, setupToken, username, password string) (LoginResult, error) {
	exists, err := s.repository.OwnerExists(ctx)
	if err != nil {
		return LoginResult{}, err
	}
	if exists {
		return LoginResult{}, ErrOwnerExists
	}
	if !s.validSetupToken(setupToken) {
		return LoginResult{}, ErrInvalidSetupToken
	}
	username = strings.TrimSpace(username)
	if len(username) < 3 || len(username) > 64 {
		return LoginResult{}, fmt.Errorf("username must contain 3 to 64 characters")
	}
	hash, err := HashPassword(password, s.passwordParams)
	if err != nil {
		return LoginResult{}, err
	}
	now := s.now().UTC()
	owner := domain.Owner{
		ID:           domain.NewID("owner"),
		Username:     username,
		PasswordHash: hash,
		CreatedAt:    now,
		LastLoginAt:  now,
	}
	if err := s.repository.CreateOwner(ctx, owner); err != nil {
		return LoginResult{}, err
	}
	return s.newSession(ctx, owner)
}

func (s *Service) ChangePassword(ctx context.Context, ownerID, currentSessionID, currentPassword, newPassword string) error {
	owner, err := s.repository.OwnerByID(ctx, ownerID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUnauthorized
	}
	if err != nil {
		return err
	}
	if !VerifyPassword(owner.PasswordHash, currentPassword) {
		return ErrUnauthorized
	}
	hash, err := HashPassword(newPassword, s.passwordParams)
	if err != nil {
		return err
	}
	if err := s.repository.UpdateOwnerPassword(ctx, owner.ID, hash); err != nil {
		return err
	}
	return s.repository.RevokeOwnerSessions(
		ctx, owner.ID, currentSessionID, s.now().UTC().Format(time.RFC3339Nano),
	)
}

func (s *Service) Recover(ctx context.Context, setupToken, username, newPassword string) (LoginResult, error) {
	if !s.validSetupToken(setupToken) {
		return LoginResult{}, ErrInvalidSetupToken
	}
	hash, err := HashPassword(newPassword, s.passwordParams)
	if err != nil {
		return LoginResult{}, err
	}
	owner, err := s.repository.OwnerByUsername(ctx, strings.TrimSpace(username))
	if errors.Is(err, sql.ErrNoRows) {
		return LoginResult{}, ErrUnauthorized
	}
	if err != nil {
		return LoginResult{}, err
	}
	if err := s.repository.UpdateOwnerPassword(ctx, owner.ID, hash); err != nil {
		return LoginResult{}, err
	}
	if err := s.repository.RevokeOwnerSessions(
		ctx, owner.ID, "", s.now().UTC().Format(time.RFC3339Nano),
	); err != nil {
		return LoginResult{}, err
	}
	owner.PasswordHash = hash
	return s.newSession(ctx, owner)
}

func (s *Service) Login(ctx context.Context, username, password string) (LoginResult, error) {
	owner, err := s.repository.OwnerByUsername(ctx, strings.TrimSpace(username))
	if errors.Is(err, sql.ErrNoRows) {
		return LoginResult{}, ErrUnauthorized
	}
	if err != nil {
		return LoginResult{}, err
	}
	if !VerifyPassword(owner.PasswordHash, password) {
		return LoginResult{}, ErrUnauthorized
	}
	now := s.now().UTC()
	owner.LastLoginAt = now
	if err := s.repository.TouchOwnerLogin(ctx, owner.ID, now.Format(time.RFC3339Nano)); err != nil {
		return LoginResult{}, err
	}
	return s.newSession(ctx, owner)
}

func (s *Service) Authenticate(ctx context.Context, rawToken string) (domain.Session, error) {
	if rawToken == "" {
		return domain.Session{}, ErrUnauthorized
	}
	session, err := s.repository.SessionByTokenHash(ctx, tokenHash(rawToken))
	if err != nil {
		return domain.Session{}, ErrUnauthorized
	}
	now := s.now().UTC()
	if !session.RevokedAt.IsZero() || !session.ExpiresAt.After(now) {
		return domain.Session{}, ErrUnauthorized
	}
	session.LastSeen = now
	_ = s.repository.TouchSession(ctx, session.ID, now.Format(time.RFC3339Nano))
	return session, nil
}

func (s *Service) Logout(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return nil
	}
	return s.repository.RevokeSession(ctx, sessionID, s.now().UTC().Format(time.RFC3339Nano))
}

func (s *Service) newSession(ctx context.Context, owner domain.Owner) (LoginResult, error) {
	rawToken, err := randomToken(32)
	if err != nil {
		return LoginResult{}, err
	}
	csrf, err := randomToken(24)
	if err != nil {
		return LoginResult{}, err
	}
	now := s.now().UTC()
	session := domain.Session{
		ID:        domain.NewID("session"),
		OwnerID:   owner.ID,
		TokenHash: tokenHash(rawToken),
		CSRFToken: csrf,
		CreatedAt: now,
		LastSeen:  now,
		ExpiresAt: now.Add(s.sessionTTL),
	}
	if err := s.repository.CreateSession(ctx, session); err != nil {
		return LoginResult{}, err
	}
	return LoginResult{Owner: owner, Session: session, SessionToken: rawToken}, nil
}

func (s *Service) validSetupToken(candidate string) bool {
	if s.setupToken == "" {
		return false
	}
	expected := sha256.Sum256([]byte(s.setupToken))
	actual := sha256.Sum256([]byte(candidate))
	return subtle.ConstantTimeCompare(actual[:], expected[:]) == 1
}

func randomToken(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func tokenHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
