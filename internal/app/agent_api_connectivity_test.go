package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
)

func newAgentAPIConnectivityService(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	secretStore, err := secrets.Open(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	service := NewService(database, secretStore, StubExecutor{})
	service.SetAgentAPI(agentapi.NewCapabilities(), "http://127.0.0.1:8766")
	return service, database
}

func TestAgentAPISettingsAndWorkspaceResolution(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, database := newAgentAPIConnectivityService(t)
	defer database.Close()
	settings, err := service.UpdateAgentAPISettings(ctx, domain.AgentAPISettings{URL: "https://aha.example.test/"})
	if err != nil || settings.EffectiveURL != "https://aha.example.test" || settings.URL != "https://aha.example.test" {
		t.Fatalf("settings=%#v err=%v", settings, err)
	}
	// The override is a troubleshooting fallback, so it accepts only the same
	// addresses automatic selection may use.
	if _, err := service.UpdateAgentAPISettings(ctx, domain.AgentAPISettings{URL: "http://192.0.2.10:8766"}); err == nil {
		t.Fatal("plaintext LAN override was accepted")
	}
	manualMode, manualURL, err := service.ValidateWorkspaceAgentAPI(ctx, "manual", "https://aha.example.test/")
	if err != nil || manualMode != "manual" || manualURL != "https://aha.example.test" {
		t.Fatalf("manual=%q %q err=%v", manualMode, manualURL, err)
	}
	for _, test := range []struct {
		item domain.Workspace
		want string
	}{
		{item: domain.Workspace{AgentAPIMode: "global"}, want: settings.EffectiveURL},
		{item: domain.Workspace{AgentAPIMode: "manual", AgentAPIURL: manualURL}, want: manualURL},
		{item: domain.Workspace{AgentAPIMode: "auto", AgentAPIStatus: "ready", AgentAPIResolvedURL: "http://127.0.0.1:8766"}, want: "http://127.0.0.1:8766"},
		// A detected automatic path wins over the manual fallback.
		{item: domain.Workspace{AgentAPIMode: "manual", AgentAPIURL: "https://manual.example.test", AgentAPIStatus: "ready", AgentAPIResolvedURL: "http://127.0.0.1:8766"}, want: "http://127.0.0.1:8766"},
	} {
		got, err := service.AgentAPIURLForWorkspace(ctx, test.item)
		if err != nil || got != test.want {
			t.Fatalf("item=%#v got=%q err=%v", test.item, got, err)
		}
	}
	if _, _, err := service.ValidateWorkspaceAgentAPI(ctx, "manual", "http://198.51.100.10:8766"); err == nil {
		t.Fatal("plaintext LAN manual URL was accepted")
	}
	if _, _, err := service.ValidateWorkspaceAgentAPI(ctx, "manual", "http://127.0.0.1:8766"); err != nil {
		t.Fatalf("loopback manual URL was rejected: %v", err)
	}
	if _, err := service.UpdateAgentAPISettings(ctx, domain.AgentAPISettings{}); err != nil {
		t.Fatal(err)
	}
	// A row written by an older build (or a synced one) must not become usable
	// merely because the global override was cleared.
	if _, err := service.AgentAPIURLForWorkspace(ctx, domain.Workspace{AgentAPIMode: "manual", AgentAPIURL: "http://198.51.100.10:8766"}); err == nil {
		t.Fatal("plaintext LAN manual URL remained active")
	}
}

func TestDetectNativeWorkspaceAgentAPI(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, database := newAgentAPIConnectivityService(t)
	defer database.Close()
	health := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"ok":true,"service":"aha2"}`))
	}))
	defer health.Close()
	if _, err := service.UpdateAgentAPISettings(ctx, domain.AgentAPISettings{URL: health.URL}); err != nil {
		t.Fatal(err)
	}
	item := service.DetectWorkspaceAgentAPI(ctx, domain.Workspace{
		Transport: "native", RootPath: t.TempDir(), Health: "ready", AgentAPIMode: "auto", Capabilities: map[string]any{}, UpdatedAt: time.Now().UTC(),
	})
	if item.AgentAPIStatus != "ready" || item.AgentAPIResolvedURL != health.URL || item.Health != "ready" {
		t.Fatalf("detected=%#v", item)
	}
	probe, _ := item.Capabilities["agent_api"].(map[string]any)
	if probe["status"] != "ready" || probe["url"] != health.URL || probe["via"] != "direct" {
		t.Fatalf("probe=%#v", probe)
	}
}

func TestAgentAPIURLValidationRejectsUnsafeHTTP(t *testing.T) {
	t.Parallel()
	if _, err := normalizeAgentAPIBaseURL("http://192.0.2.10:8766"); err == nil {
		t.Fatal("unsafe HTTP URL was accepted")
	}
	for _, allowed := range []string{"http://127.0.0.1:8766", "http://[::1]:8766", "http://localhost:8766", "https://aha.example.test"} {
		if _, err := normalizeAgentAPIBaseURL(allowed); err != nil {
			t.Errorf("%s rejected: %v", allowed, err)
		}
	}
	if _, err := normalizeAgentAPIBaseURL("https://user:secret@example.test"); err == nil {
		t.Fatal("URL credentials were accepted")
	}
}

// TestAgentAPIProbeCandidatesFollowTheConnectionPolicy pins the selection order
// and the boundary in one place.
//
// The order is the policy: the transport's own path first, the hidden override
// last. The filtering is the other half: a legacy plaintext LAN address must
// never become a candidate merely because a row still holds one.
// TestAgentAPISettingsIgnoreStoredPlaintextAddress covers a value that reaches
// the database without passing through the HTTP update path: sync, an import, or
// a build that predates the boundary. Resolution must not inject it.
func TestAgentAPISettingsIgnoreStoredPlaintextAddress(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, database := newAgentAPIConnectivityService(t)
	defer database.Close()
	if _, err := database.UpdateAgentAPISettings(ctx, domain.AgentAPISettings{URL: "http://192.168.1.10:8766"}); err != nil {
		t.Fatal(err)
	}
	settings, err := service.AgentAPISettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.URL != "" || settings.EffectiveURL != "http://127.0.0.1:8766" {
		t.Fatalf("unsafe stored value survived: %#v", settings)
	}
	got, err := service.AgentAPIURLForWorkspace(ctx, domain.Workspace{AgentAPIMode: "auto"})
	if err != nil || got != "http://127.0.0.1:8766" {
		t.Fatalf("resolved=%q err=%v", got, err)
	}
}

func TestAgentAPIProbeCandidatesFollowTheConnectionPolicy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, database := newAgentAPIConnectivityService(t)
	defer database.Close()
	settings, err := service.AgentAPISettings(ctx)
	if err != nil {
		t.Fatal(err)
	}

	native := service.agentAPIProbeCandidates(ctx, domain.Workspace{Transport: "native", AgentAPIMode: "auto"}, settings)
	if len(native) != 1 || native[0] != "http://127.0.0.1:8766" {
		t.Fatalf("native candidates = %v", native)
	}

	ssh := service.agentAPIProbeCandidates(ctx, domain.Workspace{Transport: "ssh", AgentAPIMode: "auto"}, settings)
	if len(ssh) != 1 || ssh[0] != "http://127.0.0.1:8766" {
		t.Fatalf("ssh candidates = %v", ssh)
	}

	wsl := service.agentAPIProbeCandidates(ctx, domain.Workspace{Transport: "wsl", AgentAPIMode: "auto"}, settings)
	if len(wsl) < 2 || wsl[0] != "http://127.0.0.1:8766" || wsl[1] != "http://localhost:8766" {
		t.Fatalf("wsl candidates = %v", wsl)
	}

	// Legacy manual plaintext value is not a candidate; a safe override is last.
	settings.URL = "https://override.example.test"
	candidates := service.agentAPIProbeCandidates(ctx, domain.Workspace{
		Transport: "ssh", AgentAPIMode: "manual", AgentAPIURL: "http://192.168.1.20:8766",
	}, settings)
	if len(candidates) != 2 || candidates[0] != "http://127.0.0.1:8766" || candidates[1] != "https://override.example.test" {
		t.Fatalf("override candidates = %v", candidates)
	}
}
