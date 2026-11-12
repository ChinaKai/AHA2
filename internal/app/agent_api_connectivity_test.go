package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
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
	// Candidate selection consults this machine's interfaces and the workspace's
	// view. Tests pin both to nothing by default so a case that cares opts in.
	service.agentAPILocalHostsFn = func() []string { return nil }
	service.agentAPIWorkspaceHostsFn = func(context.Context, domain.Workspace) []string { return nil }
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
	// Point the startup address at the test server too. Otherwise a real AHA2
	// listening on the default 8766 on the developer's machine is a valid native
	// candidate and wins before the override, making the test environment-dependent.
	service.SetAgentAPI(agentapi.NewCapabilities(), health.URL)
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
	// Two LAN sources: the workspace's view of AHA and this host's interfaces.
	// Both must be deduplicated and filtered to private IPv4 addresses.
	service.agentAPIWorkspaceHostsFn = func(context.Context, domain.Workspace) []string {
		return []string{"192.168.7.40"}
	}
	service.agentAPILocalHostsFn = func() []string {
		return []string{"192.168.7.44", "127.0.0.1", "not-an-ip"}
	}
	settings, err := service.AgentAPISettings(ctx)
	if err != nil {
		t.Fatal(err)
	}

	native := service.agentAPIProbeCandidates(ctx, domain.Workspace{Transport: "native", AgentAPIMode: "auto"}, settings)
	if len(native) != 1 || native[0] != "http://127.0.0.1:8766" {
		t.Fatalf("native candidates = %v", native)
	}

	// SSH tries the tunnel target first, then AHA's LAN addresses over plaintext.
	ssh := service.agentAPIProbeCandidates(ctx, domain.Workspace{Transport: "ssh", AgentAPIMode: "auto"}, settings)
	wantSSH := []string{"http://127.0.0.1:8766", "http://192.168.7.40:8766", "http://192.168.7.44:8766"}
	if !reflect.DeepEqual(ssh, wantSSH) {
		t.Fatalf("ssh candidates = %v, want %v", ssh, wantSSH)
	}

	wsl := service.agentAPIProbeCandidates(ctx, domain.Workspace{Transport: "wsl", AgentAPIMode: "auto"}, settings)
	if len(wsl) != 2 || wsl[0] != "http://127.0.0.1:8766" || wsl[1] != "http://localhost:8766" {
		t.Fatalf("wsl candidates = %v", wsl)
	}
	// The plaintext LAN fallback is an SSH-only path: neither native nor WSL may
	// ever gain a non-loopback plaintext candidate.
	for transport, candidates := range map[string][]string{"native": native, "wsl": wsl} {
		for _, candidate := range candidates {
			if strings.HasPrefix(candidate, "http://") && !strings.Contains(candidate, "127.0.0.1") && !strings.Contains(candidate, "localhost") {
				t.Fatalf("%s candidate %q is plaintext off loopback", transport, candidate)
			}
		}
	}

	// Reset the host sources for the exact override assertions below.
	service.agentAPILocalHostsFn = func() []string { return nil }
	service.agentAPIWorkspaceHostsFn = func(context.Context, domain.Workspace) []string { return nil }

	// Legacy manual plaintext value is not a candidate; a safe override is last.
	settings.URL = "https://override.example.test"
	candidates := service.agentAPIProbeCandidates(ctx, domain.Workspace{
		Transport: "ssh", AgentAPIMode: "manual", AgentAPIURL: "http://192.168.1.20:8766",
	}, settings)
	if len(candidates) != 2 || candidates[0] != "http://127.0.0.1:8766" || candidates[1] != "https://override.example.test" {
		t.Fatalf("override candidates = %v", candidates)
	}
}

// recordingAgentAPIProbe replaces the real workspace probe in detection tests
// and records the order in which candidates were tried.
type recordingAgentAPIProbe struct {
	calls []string
	ok    func(string) bool
}

func (probe *recordingAgentAPIProbe) probe(_ context.Context, _ domain.Workspace, candidate string) error {
	probe.calls = append(probe.calls, candidate)
	if probe.ok(candidate) {
		return nil
	}
	return fmt.Errorf("healthz 请求失败: connect: connection refused")
}

// TestDetectSSHWorkspaceAgentAPIFallsBackToPlaintextLAN is the regression the
// fallback exists for: the workspace's sshd refuses the reverse tunnel, but AHA
// is reachable over the local network, so detection must select that address.
func TestDetectSSHWorkspaceAgentAPIFallsBackToPlaintextLAN(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, database := newAgentAPIConnectivityService(t)
	defer database.Close()
	service.agentAPILocalHostsFn = func() []string { return []string{"192.168.7.44"} }
	lanURL := "http://192.168.7.44:8766"
	probe := &recordingAgentAPIProbe{ok: func(candidate string) bool { return candidate == lanURL }}
	service.agentAPIProbeFn = probe.probe
	item := service.DetectWorkspaceAgentAPI(ctx, domain.Workspace{
		Transport: "ssh", RootPath: "/repo", Health: "ready", AgentAPIMode: "auto", Capabilities: map[string]any{},
	})
	if item.AgentAPIStatus != "ready" || item.AgentAPIResolvedURL != lanURL || item.AgentAPIError != "" {
		t.Fatalf("detected=%#v", item)
	}
	wantCalls := []string{"http://127.0.0.1:8766", lanURL}
	if !reflect.DeepEqual(probe.calls, wantCalls) {
		t.Fatalf("probe order = %v, want tunnel then LAN %v", probe.calls, wantCalls)
	}
	capability, _ := item.Capabilities["agent_api"].(map[string]any)
	if capability["status"] != "ready" || capability["url"] != lanURL || capability["via"] != "direct" {
		t.Fatalf("capability=%#v", capability)
	}
	// The selected address must survive Turn resolution: the plaintext relaxation
	// used for probing has to be honoured when the stored result is read back.
	resolved, err := service.AgentAPIURLForWorkspace(ctx, item)
	if err != nil || resolved != lanURL {
		t.Fatalf("resolved=%q err=%v", resolved, err)
	}
}

// TestDetectSSHWorkspaceAgentAPIKeepsTheTunnelWhenItWorks pins that a working
// tunnel is never superseded by the LAN fallback.
func TestDetectSSHWorkspaceAgentAPIKeepsTheTunnelWhenItWorks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, database := newAgentAPIConnectivityService(t)
	defer database.Close()
	service.agentAPILocalHostsFn = func() []string { return []string{"192.168.7.44"} }
	probe := &recordingAgentAPIProbe{ok: func(candidate string) bool { return candidate == "http://127.0.0.1:8766" }}
	service.agentAPIProbeFn = probe.probe
	item := service.DetectWorkspaceAgentAPI(ctx, domain.Workspace{
		Transport: "ssh", RootPath: "/repo", Health: "ready", AgentAPIMode: "auto", Capabilities: map[string]any{},
	})
	if item.AgentAPIStatus != "ready" || item.AgentAPIResolvedURL != "http://127.0.0.1:8766" {
		t.Fatalf("detected=%#v", item)
	}
	if len(probe.calls) != 1 || probe.calls[0] != "http://127.0.0.1:8766" {
		t.Fatalf("probe calls = %v, want only the tunnel", probe.calls)
	}
	capability, _ := item.Capabilities["agent_api"].(map[string]any)
	if capability["via"] != "tunnel" {
		t.Fatalf("capability=%#v", capability)
	}
}

// TestDetectFailureNamesEachStage checks that the error says which of tunnel,
// plaintext LAN, and override was attempted, without exposing a token.
func TestDetectFailureNamesEachStage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, database := newAgentAPIConnectivityService(t)
	defer database.Close()
	if _, err := service.UpdateAgentAPISettings(ctx, domain.AgentAPISettings{URL: "https://override.example.test"}); err != nil {
		t.Fatal(err)
	}
	service.agentAPILocalHostsFn = func() []string { return []string{"192.168.7.44"} }
	probe := &recordingAgentAPIProbe{ok: func(string) bool { return false }}
	service.agentAPIProbeFn = probe.probe
	item := service.DetectWorkspaceAgentAPI(ctx, domain.Workspace{
		Transport: "ssh", RootPath: "/repo", Health: "ready", AgentAPIMode: "auto", Capabilities: map[string]any{},
	})
	if item.AgentAPIStatus != "error" || item.AgentAPIError == "" {
		t.Fatalf("detected=%#v", item)
	}
	for _, stage := range []string{"隧道", "LAN 明文", "覆盖地址"} {
		if !strings.Contains(item.AgentAPIError, stage) {
			t.Fatalf("error %q does not name stage %q", item.AgentAPIError, stage)
		}
	}
	if strings.Contains(strings.ToLower(item.AgentAPIError), "token") || strings.Contains(item.AgentAPIError, "Bearer") {
		t.Fatalf("error leaks credential material: %q", item.AgentAPIError)
	}
}

// TestPlaintextProbeBoundaryIsSSHOnlyAndLANOnly pins the narrow relaxation: a
// detected plaintext result is usable only for SSH, and only for a private IPv4
// literal, while configured values keep the strict HTTPS-or-loopback rule.
func TestPlaintextProbeBoundaryIsSSHOnlyAndLANOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, database := newAgentAPIConnectivityService(t)
	defer database.Close()

	lanURL := "http://192.168.7.44:8766"
	if got, err := normalizeAgentAPIProbeResult(domain.Workspace{Transport: "ssh"}, lanURL); err != nil || got != lanURL {
		t.Fatalf("ssh probe result = %q, %v", got, err)
	}
	for _, transport := range []string{"native", "wsl", ""} {
		if _, err := normalizeAgentAPIProbeResult(domain.Workspace{Transport: transport}, lanURL); err == nil {
			t.Fatalf("transport %q accepted a plaintext probe result", transport)
		}
	}
	for _, raw := range []string{"http://aha.example.test:8766", "http://[fd00::1]:8766", "http://169.254.1.2:8766", "http://203.0.113.10:8766", "https://192.168.7.44:8766"} {
		if _, err := normalizeAgentAPIPlaintextCandidate(raw); err == nil {
			t.Fatalf("%q was accepted as a plaintext LAN candidate", raw)
		}
	}

	// A stored plaintext result is honoured only for the SSH workspace that
	// detected it. The same row on another transport falls back to strict.
	ready := domain.Workspace{Transport: "ssh", AgentAPIMode: "auto", AgentAPIStatus: "ready", AgentAPIResolvedURL: lanURL}
	if got, err := service.AgentAPIURLForWorkspace(ctx, ready); err != nil || got != lanURL {
		t.Fatalf("ssh resolved=%q err=%v", got, err)
	}
	ready.Transport = "native"
	if _, err := service.AgentAPIURLForWorkspace(ctx, ready); err == nil {
		t.Fatal("native workspace accepted a plaintext detected result")
	}

	// Configured values never gain the relaxation.
	if _, err := service.UpdateAgentAPISettings(ctx, domain.AgentAPISettings{URL: lanURL}); err == nil {
		t.Fatal("global settings accepted a plaintext LAN URL")
	}
	if _, _, err := service.ValidateWorkspaceAgentAPI(ctx, "manual", lanURL); err == nil {
		t.Fatal("manual workspace URL accepted a plaintext LAN URL")
	}
}
