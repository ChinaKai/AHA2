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
	settings, err := service.UpdateAgentAPISettings(ctx, domain.AgentAPISettings{URL: "http://192.0.2.10:8766", AllowInsecure: true})
	if err != nil || settings.EffectiveURL != "http://192.0.2.10:8766" || !settings.EffectiveAllowInsecure {
		t.Fatalf("settings=%#v err=%v", settings, err)
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
		{item: domain.Workspace{AgentAPIMode: "auto", AgentAPIStatus: "ready", AgentAPIResolvedURL: "http://172.28.0.1:8766"}, want: "http://172.28.0.1:8766"},
	} {
		got, err := service.AgentAPIURLForWorkspace(ctx, test.item)
		if err != nil || got != test.want {
			t.Fatalf("item=%#v got=%q err=%v", test.item, got, err)
		}
	}
	if _, _, err := service.ValidateWorkspaceAgentAPI(ctx, "manual", "http://198.51.100.10:8766"); err != nil {
		t.Fatalf("trusted HTTP manual URL was rejected: %v", err)
	}
	if _, err := service.UpdateAgentAPISettings(ctx, domain.AgentAPISettings{}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AgentAPIURLForWorkspace(ctx, domain.Workspace{AgentAPIMode: "manual", AgentAPIURL: "http://198.51.100.10:8766"}); err == nil {
		t.Fatal("manual HTTP URL remained active after trusted-network permission was removed")
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
	if probe["status"] != "ready" || probe["url"] != health.URL {
		t.Fatalf("probe=%#v", probe)
	}
}

func TestAgentAPIURLValidationRejectsUnsafeHTTP(t *testing.T) {
	t.Parallel()
	if _, err := normalizeAgentAPIBaseURL("http://192.0.2.10:8766", false); err == nil {
		t.Fatal("unsafe HTTP URL was accepted")
	}
	if _, err := normalizeAgentAPIBaseURL("https://user:secret@example.test", false); err == nil {
		t.Fatal("URL credentials were accepted")
	}
}
