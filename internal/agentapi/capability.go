package agentapi

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"sync"
	"time"
)

var ErrInvalidCapability = errors.New("invalid or expired agent capability")

type Claims struct {
	TaskID    string
	AgentID   string
	TurnID    string
	ExpiresAt time.Time
}

// Capabilities keeps short-lived, opaque Agent API credentials in memory.
// Raw tokens are only returned to the owning Turn and are never persisted.
type Capabilities struct {
	mu     sync.Mutex
	now    func() time.Time
	tokens map[[sha256.Size]byte]Claims
}

func NewCapabilities() *Capabilities {
	return &Capabilities{now: time.Now, tokens: make(map[[sha256.Size]byte]Claims)}
}

func (c *Capabilities) Issue(taskID, agentID, turnID string, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = 4 * time.Hour
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	c.mu.Lock()
	defer c.mu.Unlock()
	c.removeExpiredLocked()
	c.tokens[hash] = Claims{
		TaskID: taskID, AgentID: agentID, TurnID: turnID, ExpiresAt: c.now().UTC().Add(ttl),
	}
	return token, nil
}

func (c *Capabilities) Authenticate(token string) (Claims, error) {
	if token == "" {
		return Claims{}, ErrInvalidCapability
	}
	hash := sha256.Sum256([]byte(token))
	c.mu.Lock()
	defer c.mu.Unlock()
	claims, ok := c.tokens[hash]
	if !ok || !claims.ExpiresAt.After(c.now().UTC()) {
		delete(c.tokens, hash)
		return Claims{}, ErrInvalidCapability
	}
	return claims, nil
}

func (c *Capabilities) Revoke(token string) {
	if token == "" {
		return
	}
	hash := sha256.Sum256([]byte(token))
	c.mu.Lock()
	delete(c.tokens, hash)
	c.mu.Unlock()
}

func (c *Capabilities) removeExpiredLocked() {
	now := c.now().UTC()
	for hash, claims := range c.tokens {
		if !claims.ExpiresAt.After(now) {
			delete(c.tokens, hash)
		}
	}
}
