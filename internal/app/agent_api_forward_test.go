package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

// TestAgentAPIReverseForwardGating pins when AHA carries itself into a workspace.
//
// The decision matters in both directions: substituting an address for a workspace
// that could already reach AHA would silently change which endpoint the Agent uses,
// and skipping one that needed it leaves the Agent unable to call the API at all.
func TestAgentAPIReverseForwardGating(t *testing.T) {
	t.Parallel()
	cases := []struct {
		transport string
		url       string
		want      bool
	}{
		{"ssh", "http://127.0.0.1:8766", true},
		{"ssh", "http://192.168.0.39:8766", false},
		{"ssh", "https://aha.example.com", false},
		{"native", "http://127.0.0.1:8766", false},
		{"wsl", "http://127.0.0.1:8766", false},
	}
	for _, c := range cases {
		got := agentAPIReverseForward(domain.Workspace{Transport: c.transport}, c.url)
		t.Logf("%-8s %-28s -> %v", c.transport, c.url, got != nil)
		if (got != nil) != c.want {
			t.Errorf("transport=%s url=%s got=%v want=%v", c.transport, c.url, got != nil, c.want)
		}
	}
	// The target must be the AHA-side address the forward dials, not the URL text.
	got := agentAPIReverseForward(domain.Workspace{Transport: "ssh"}, "http://127.0.0.1:9000")
	if got == nil || got.Target != "127.0.0.1:9000" || got.EnvName != "AHA2_AGENT_API_URL" {
		t.Fatalf("forward = %#v", got)
	}
}

// TestAgentAPICapabilityTTLCoversTheTurnTimeout pins the invariant that a
// credential must not expire before the work it authorises.
//
// The previous fixed four-hour lifetime was shorter than the default ten-hour
// execution limit, so a long Turn would lose API access midway: the Turn kept
// running while every call began failing, which reads as the Agent misbehaving
// rather than as an expired credential.
func TestAgentAPICapabilityTTLOutlivesTheConfiguredTurnTimeout(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	service := &Service{store: database, now: time.Now}

	for _, timeoutSeconds := range []int{600, 10 * 60 * 60, 168 * 60 * 60} {
		settings, err := database.BackendSettings(ctx)
		if err != nil {
			t.Fatal(err)
		}
		settings.TurnTimeoutSeconds = timeoutSeconds
		if _, err := database.UpdateBackendSettings(ctx, settings); err != nil {
			t.Fatal(err)
		}
		ttl := service.agentAPICapabilityTTL(ctx)
		if ttl <= time.Duration(timeoutSeconds)*time.Second {
			t.Fatalf("timeout %ds: ttl %s does not outlive the turn", timeoutSeconds, ttl)
		}
		if ttl > time.Duration(timeoutSeconds)*time.Second+time.Hour {
			t.Fatalf("timeout %ds: ttl %s has an unexpectedly large margin", timeoutSeconds, ttl)
		}
	}
}

// TestAgentAPIForwardAppliesWhenDetectionFailed reproduces the real remote-SSH
// workspace: detection reported an error, no resolved address was stored, and the
// global URL is loopback.
//
// This is the case the tunnel exists for. Probe-based discovery cannot work here —
// the workspace would have to reach 127.0.0.1 on AHA's host, which is its own
// loopback — so a failed probe is the *expected* state, not a reason to withhold
// the forward. If the forward were gated on a successful probe, this workspace
// would never get a working Agent API address.
func TestAgentAPIForwardAppliesWhenDetectionFailed(t *testing.T) {
	t.Parallel()
	workspace := domain.Workspace{
		ID: "workspace-remote", Name: "阿里云",
		Locality: "remote", Transport: "ssh",
		SSHHost: "101.201.66.192", SSHUser: "root", SSHPort: 22,
		RootPath: "/home/kk-test",
		// The state a real probe leaves behind when it cannot reach loopback.
		AgentAPIMode:        "auto",
		AgentAPIStatus:      "error",
		AgentAPIResolvedURL: "",
		AgentAPIError:       "healthz 请求失败: curl: (7) Failed to connect to 127.0.0.1 port 8766",
	}
	forward := agentAPIReverseForward(workspace, "http://127.0.0.1:8766")
	if forward == nil {
		t.Fatal("a workspace whose probe failed must still receive the forward")
	}
	if forward.Target != "127.0.0.1:8766" {
		t.Fatalf("forward target = %q, want the AHA loopback address", forward.Target)
	}
	if forward.EnvName != "AHA2_AGENT_API_URL" {
		t.Fatalf("forward env = %q", forward.EnvName)
	}
}
