package agentapi

import (
	"errors"
	"testing"
	"time"
)

func TestCapabilityIsScopedExpiresAndRevokes(t *testing.T) {
	now := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	capabilities := NewCapabilities()
	capabilities.now = func() time.Time { return now }
	token, err := capabilities.Issue("task-1", "main", "turn-1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := capabilities.Authenticate(token)
	if err != nil || claims.TaskID != "task-1" || claims.AgentID != "main" || claims.TurnID != "turn-1" {
		t.Fatalf("unexpected claims: %#v %v", claims, err)
	}
	capabilities.Revoke(token)
	if _, err := capabilities.Authenticate(token); !errors.Is(err, ErrInvalidCapability) {
		t.Fatalf("revoked token accepted: %v", err)
	}

	token, err = capabilities.Issue("task-2", "sub-001", "turn-2", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := capabilities.Authenticate(token); !errors.Is(err, ErrInvalidCapability) {
		t.Fatalf("expired token accepted: %v", err)
	}
}
