package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

// TestPrepareAgentAPIForTurnKeepsTheOldPathWhenTheSwitchIsOff pins the "off
// means unchanged" half of the contract: with the setting at its default, an
// SSH workspace must resolve exactly as before and no tunnel is started.
func TestPrepareAgentAPIForTurnKeepsTheOldPathWhenTheSwitchIsOff(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	service := &Service{store: database, now: time.Now, agentAPIURL: "http://127.0.0.1:8766"}

	item := domain.Workspace{
		ID: "workspace-1", Transport: "ssh", SSHHost: "127.0.0.1", SSHPort: 1,
		AgentAPIMode: "auto", AgentAPIStatus: "ready", AgentAPIResolvedURL: "http://192.168.7.187:8766",
	}
	setup, err := service.prepareAgentAPIForTurn(ctx, item, domain.Task{ID: "task-1"}, t.TempDir())
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if setup.Proxy != nil {
		t.Fatal("a tunnel was started with the setting off")
	}
	if setup.URL != "http://192.168.7.187:8766" {
		t.Fatalf("url = %q, want the previously resolved address", setup.URL)
	}
	if setup.Note != "" {
		t.Fatalf("note = %q, want none", setup.Note)
	}
}

// TestPrepareAgentAPIForTurnFallsBackWithAReason covers the enabled switch on an
// unreachable workspace: the Turn still runs on the previous path, and the
// reason is recorded instead of the failure being silent.
func TestPrepareAgentAPIForTurnFallsBackWithAReason(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	service := &Service{store: database, now: time.Now, agentAPIURL: "http://127.0.0.1:8766"}
	settings, err := database.AgentAPISettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	settings.StdioProxy = true
	if _, err := database.UpdateAgentAPISettings(ctx, settings); err != nil {
		t.Fatal(err)
	}

	item := domain.Workspace{
		ID: "workspace-1", Transport: "ssh", SSHHost: "127.0.0.1", SSHPort: 1,
		AgentAPIMode: "auto", AgentAPIStatus: "ready", AgentAPIResolvedURL: "http://192.168.7.187:8766",
	}
	setup, err := service.prepareAgentAPIForTurn(ctx, item, domain.Task{ID: "task-1"}, t.TempDir())
	if err != nil {
		t.Fatalf("a failed tunnel must not fail the turn by itself: %v", err)
	}
	if setup.Proxy != nil {
		t.Fatal("a tunnel was reported ready for an unreachable workspace")
	}
	if setup.URL != "http://192.168.7.187:8766" {
		t.Fatalf("url = %q, want the fallback address", setup.URL)
	}
	if !strings.Contains(setup.Note, "Agent API stdio 代理启动失败") && !strings.Contains(setup.Note, "准备 helper") {
		t.Fatalf("note does not explain the fallback: %q", setup.Note)
	}
}

// TestPrepareAgentAPIForTurnSkipsNonLoopbackAddresses checks the other side of
// the gate: a workspace that can already dial AHA must not be routed through a
// tunnel that would only add a hop and a failure mode.
func TestPrepareAgentAPIForTurnSkipsNonLoopbackAddresses(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	// The global override points at a LAN address, so there is nothing to carry.
	service := &Service{store: database, now: time.Now, agentAPIURL: "http://192.168.7.187:8766"}
	settings, err := database.AgentAPISettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	settings.StdioProxy = true
	if _, err := database.UpdateAgentAPISettings(ctx, settings); err != nil {
		t.Fatal(err)
	}

	item := domain.Workspace{
		ID: "workspace-1", Transport: "ssh", SSHHost: "127.0.0.1", SSHPort: 1,
		AgentAPIMode: "auto", AgentAPIStatus: "ready", AgentAPIResolvedURL: "http://192.168.7.187:8766",
	}
	setup, err := service.prepareAgentAPIForTurn(ctx, item, domain.Task{ID: "task-1"}, t.TempDir())
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if setup.Proxy != nil || setup.Note != "" {
		t.Fatalf("non-loopback address took a tunnel: %#v", setup)
	}
	if setup.URL != "http://192.168.7.187:8766" {
		t.Fatalf("url = %q", setup.URL)
	}
}
