package app

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
	workspacepkg "github.com/ChinaKai/AHA2/internal/workspace"
)

func newAgentAPIProxyTestService(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	service, database, _ := newAgentAPIProxyTestServiceWithPath(t)
	return service, database
}

func newAgentAPIProxyTestServiceWithPath(t *testing.T) (*Service, *store.Store, string) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aha2.db")
	database, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	// Detection writes the tunnel failure onto the workspace row, and the
	// fallback log reads it back. The row has to exist for that to be observable.
	if err := database.CreateProject(ctx, domain.Project{ID: "project-1", Name: "project"}); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateWorkspace(ctx, domain.Workspace{
		ID: "workspace-1", ProjectID: "project-1", Name: "workspace",
		Locality: "remote", Transport: "ssh",
	}); err != nil {
		t.Fatal(err)
	}
	service := &Service{store: database, now: time.Now, agentAPIURL: "http://127.0.0.1:8766"}
	// Turn-time detection (the fallback path) now includes a stdio stage. Tests
	// that reach it must not open a real SSH connection.
	service.agentAPIStdioProbeFn = func(context.Context, domain.Workspace, string) (workspacepkg.AgentAPIStdioResult, error) {
		return workspacepkg.AgentAPIStdioResult{}, fmt.Errorf("stdio 不可用（测试默认）")
	}
	return service, database, path
}

func agentProxyTestWorkspaceItem() domain.Workspace {
	return domain.Workspace{
		ID: "workspace-1", Transport: "ssh", SSHHost: "127.0.0.1", SSHPort: 1,
		AgentAPIMode: "auto",
	}
}

// TestPrepareAgentAPIForTurnUsesStdioWithLegacyOptOut covers the upgrade path:
// a database row written while stdio was opt-in and turned off must not keep the
// workspace on the old transport. The stored flag is no longer read.
func TestPrepareAgentAPIForTurnUsesStdioWithLegacyOptOut(t *testing.T) {
	service, _, path := newAgentAPIProxyTestServiceWithPath(t)
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// Exactly the row an older build wrote when the switch was off.
	if _, err := raw.ExecContext(context.Background(), `UPDATE agent_api_settings SET stdio_proxy=0 WHERE id=1`); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	service.agentAPIProxyStartFn = func(context.Context, domain.Workspace, domain.Task, string, string, string, string) (*workspacepkg.AgentAPIProxy, error) {
		return &workspacepkg.AgentAPIProxy{URL: "http://127.0.0.1:41000", Runtime: "node"}, nil
	}
	item := agentProxyTestWorkspaceItem()
	item.AgentAPIStatus = "ready"
	item.AgentAPIResolvedURL = "http://192.168.7.187:8766"

	setup, err := service.prepareAgentAPIForTurn(context.Background(), item, domain.Task{ID: "task-1"}, t.TempDir(), "codex")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if setup.Proxy == nil || setup.Path != agentAPIPathStdioNode {
		t.Fatalf("setup = %#v, want the stdio path", setup)
	}
	if setup.URL != "http://127.0.0.1:41000" {
		t.Fatalf("url = %q", setup.URL)
	}
}

// TestPrepareAgentAPIForTurnUsesStdioNode covers the happy path: the Node helper
// starts and the Turn gets the tunnel's loopback URL.
func TestPrepareAgentAPIForTurnUsesStdioNode(t *testing.T) {
	service, _ := newAgentAPIProxyTestService(t)
	service.agentAPIProxyStartFn = func(context.Context, domain.Workspace, domain.Task, string, string, string, string) (*workspacepkg.AgentAPIProxy, error) {
		return &workspacepkg.AgentAPIProxy{URL: "http://127.0.0.1:41000", Runtime: "node"}, nil
	}

	setup, err := service.prepareAgentAPIForTurn(context.Background(), agentProxyTestWorkspaceItem(), domain.Task{ID: "task-1"}, t.TempDir(), "codex")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if setup.Proxy == nil || setup.Path != agentAPIPathStdioNode {
		t.Fatalf("setup = %#v, want the stdio-node path", setup)
	}
	if setup.URL != "http://127.0.0.1:41000" {
		t.Fatalf("url = %q", setup.URL)
	}
}

// TestPrepareAgentAPIForTurnUsesStdioOverride distinguishes the retained
// explicit-helper path from the embedded Node script.
func TestPrepareAgentAPIForTurnUsesStdioOverride(t *testing.T) {
	service, _ := newAgentAPIProxyTestService(t)
	service.agentAPIProxyStartFn = func(context.Context, domain.Workspace, domain.Task, string, string, string, string) (*workspacepkg.AgentAPIProxy, error) {
		return &workspacepkg.AgentAPIProxy{URL: "http://127.0.0.1:41001", Runtime: "exec"}, nil
	}

	setup, err := service.prepareAgentAPIForTurn(context.Background(), agentProxyTestWorkspaceItem(), domain.Task{ID: "task-1"}, t.TempDir(), "codex")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if setup.Proxy == nil || setup.Path != agentAPIPathStdioOverride {
		t.Fatalf("setup = %#v, want the stdio-override path", setup)
	}
}

// TestPrepareAgentAPIForTurnFallsBackToTunnel covers the layer after a failed
// Node helper: the address is still loopback, so the Turn runs on the reverse
// tunnel and the reason for leaving stdio is recorded.
func TestPrepareAgentAPIForTurnFallsBackToTunnel(t *testing.T) {
	service, _ := newAgentAPIProxyTestService(t)
	service.agentAPIProxyStartFn = func(context.Context, domain.Workspace, domain.Task, string, string, string, string) (*workspacepkg.AgentAPIProxy, error) {
		return nil, fmt.Errorf("准备 helper：Node helper 不可用：工作区里没有 node")
	}
	item := agentProxyTestWorkspaceItem()
	item.AgentAPIStatus = "ready"
	item.AgentAPIResolvedURL = "http://127.0.0.1:8766"

	setup, err := service.prepareAgentAPIForTurn(context.Background(), item, domain.Task{ID: "task-1"}, t.TempDir(), "codex")
	if err != nil {
		t.Fatalf("a failed helper must not fail the turn by itself: %v", err)
	}
	if setup.Proxy != nil {
		t.Fatal("a proxy was reported ready after the helper failed")
	}
	if setup.Path != agentAPIPathTunnel {
		t.Fatalf("path = %q, want tunnel", setup.Path)
	}
	if setup.URL != "http://127.0.0.1:8766" {
		t.Fatalf("url = %q", setup.URL)
	}
	if !strings.Contains(setup.Note, "没有 node") {
		t.Fatalf("note does not carry the node failure: %q", setup.Note)
	}
}

// TestPrepareAgentAPIForTurnFallsBackToLAN is the last layer: the tunnel probe
// fails, detection selects a LAN address, and both the helper failure and the
// tunnel reason remain visible.
func TestPrepareAgentAPIForTurnFallsBackToLAN(t *testing.T) {
	service, _ := newAgentAPIProxyTestService(t)
	service.agentAPIProxyStartFn = func(context.Context, domain.Workspace, domain.Task, string, string, string, string) (*workspacepkg.AgentAPIProxy, error) {
		return nil, fmt.Errorf("准备 helper：Node helper 不可用：工作区里没有 node")
	}
	service.agentAPIWorkspaceHostsFn = func(context.Context, domain.Workspace) []string { return nil }
	service.agentAPILocalHostsFn = func() []string { return []string{"192.168.7.187"} }
	service.agentAPIProbeFn = func(_ context.Context, _ domain.Workspace, candidate string) error {
		if strings.Contains(candidate, "127.0.0.1") {
			return fmt.Errorf("服务端拒绝端口转发：administratively prohibited")
		}
		return nil
	}

	setup, err := service.prepareAgentAPIForTurn(context.Background(), agentProxyTestWorkspaceItem(), domain.Task{ID: "task-1"}, t.TempDir(), "codex")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if setup.Path != agentAPIPathLAN {
		t.Fatalf("path = %q, want lan", setup.Path)
	}
	if setup.URL != "http://192.168.7.187:8766" {
		t.Fatalf("url = %q, want the LAN address", setup.URL)
	}
	if !strings.Contains(setup.Note, "没有 node") {
		t.Fatalf("note does not carry the node failure: %q", setup.Note)
	}

	// The reason the tunnel was rejected is persisted by detection and is what
	// the fallback log line reports.
	if detail := service.agentAPIFallbackDetail(context.Background(), domain.Workspace{ID: "workspace-1"}); !strings.Contains(detail, "administratively prohibited") {
		t.Fatalf("fallback detail = %q, want the tunnel refusal", detail)
	}
}

// TestPrepareAgentAPIForTurnKeepsTunnelReasonWithLoopbackOverride is the
// regression for a stored loopback override: that candidate is labelled
// "覆盖地址", not "隧道", but the probe still went through the reverse forward,
// so its refusal must not be dropped when a LAN candidate then succeeds.
func TestPrepareAgentAPIForTurnKeepsTunnelReasonWithLoopbackOverride(t *testing.T) {
	ctx := context.Background()
	service, _ := newAgentAPIProxyTestService(t)
	settings, err := service.store.AgentAPISettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	settings.URL = "http://127.0.0.1:8766"
	if _, err := service.store.UpdateAgentAPISettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	service.agentAPIProxyStartFn = func(context.Context, domain.Workspace, domain.Task, string, string, string, string) (*workspacepkg.AgentAPIProxy, error) {
		return nil, fmt.Errorf("准备 helper：Node helper 不可用：工作区里没有 node")
	}
	service.agentAPIWorkspaceHostsFn = func(context.Context, domain.Workspace) []string { return nil }
	service.agentAPILocalHostsFn = func() []string { return []string{"192.168.7.187"} }
	service.agentAPIProbeFn = func(_ context.Context, _ domain.Workspace, candidate string) error {
		if strings.Contains(candidate, "127.0.0.1") {
			return fmt.Errorf("服务端拒绝端口转发：administratively prohibited")
		}
		return nil
	}

	setup, err := service.prepareAgentAPIForTurn(ctx, agentProxyTestWorkspaceItem(), domain.Task{ID: "task-1"}, t.TempDir(), "codex")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if setup.Path != agentAPIPathLAN {
		t.Fatalf("path = %q, want lan", setup.Path)
	}
	detail := service.agentAPIFallbackDetail(ctx, domain.Workspace{ID: "workspace-1"})
	if !strings.Contains(detail, "administratively prohibited") {
		t.Fatalf("fallback detail = %q, want the loopback override's tunnel refusal", detail)
	}
}

// TestPrepareAgentAPIForTurnSkipsNonLoopbackAddresses checks the other side of
// the gate: a workspace that can already dial AHA must not be routed through a
// tunnel that would only add a hop and a failure mode.
func TestPrepareAgentAPIForTurnSkipsNonLoopbackAddresses(t *testing.T) {
	ctx := context.Background()
	service, _ := newAgentAPIProxyTestService(t)
	service.agentAPIURL = "http://192.168.7.187:8766"
	item := agentProxyTestWorkspaceItem()
	item.AgentAPIStatus = "ready"
	item.AgentAPIResolvedURL = "http://192.168.7.187:8766"

	setup, err := service.prepareAgentAPIForTurn(ctx, item, domain.Task{ID: "task-1"}, t.TempDir(), "codex")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if setup.Proxy != nil || setup.Note != "" {
		t.Fatalf("non-loopback address took a tunnel: %#v", setup)
	}
	if setup.Path != agentAPIPathDirect {
		t.Fatalf("path = %q, want direct", setup.Path)
	}
	if setup.URL != "http://192.168.7.187:8766" {
		t.Fatalf("url = %q", setup.URL)
	}
}

// TestPrepareAgentAPIForTurnPassesBackendBinary pins the shebang input: the
// service resolves the configured backend command from the executor before the
// workspace probes for node.
func TestPrepareAgentAPIForTurnPassesBackendBinary(t *testing.T) {
	service, _ := newAgentAPIProxyTestService(t)
	service.executor = backendBinaryExecutor{binary: "/opt/backend/codex"}
	var received string
	service.agentAPIProxyStartFn = func(_ context.Context, _ domain.Workspace, _ domain.Task, _, _, _, backendBinary string) (*workspacepkg.AgentAPIProxy, error) {
		received = backendBinary
		return &workspacepkg.AgentAPIProxy{URL: "http://127.0.0.1:41002", Runtime: "node"}, nil
	}

	if _, err := service.prepareAgentAPIForTurn(context.Background(), agentProxyTestWorkspaceItem(), domain.Task{ID: "task-1"}, t.TempDir(), "codex"); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if received != "/opt/backend/codex" {
		t.Fatalf("backend binary = %q, want /opt/backend/codex", received)
	}
}

type backendBinaryExecutor struct {
	binary string
}

func (backendBinaryExecutor) Execute(context.Context, ExecutionRequest, func(ExecutionEvent)) (ExecutionResult, error) {
	return ExecutionResult{}, nil
}

func (executor backendBinaryExecutor) BackendBinary(string) string {
	return executor.binary
}

// TestPrepareAgentAPIForTurnFallbackSkipsStdioStage is the review regression:
// the Turn attempted stdio and it failed. Re-resolving the address must not
// repeat the whole stdio detection (another helper and SSH session), and the
// stdio detection succeeding must not hand the Turn AHA's loopback as if the
// reverse tunnel had been verified. The fallback must land on the probed LAN
// address instead.
func TestPrepareAgentAPIForTurnFallbackSkipsStdioStage(t *testing.T) {
	service, _ := newAgentAPIProxyTestService(t)
	item := agentProxyTestWorkspaceItem()
	item.AgentAPIStatus = "ready"
	item.AgentAPIResolvedURL = "http://127.0.0.1:8766"
	item.Capabilities = map[string]any{"agent_api": map[string]any{
		"status": "ready", "url": "http://127.0.0.1:8766", "via": "stdio", "runtime": "node",
	}}
	starts := 0
	service.agentAPIProxyStartFn = func(context.Context, domain.Workspace, domain.Task, string, string, string, string) (*workspacepkg.AgentAPIProxy, error) {
		starts++
		return nil, fmt.Errorf("Agent API stdio 代理启动失败")
	}
	stdioDetections := 0
	service.agentAPIStdioProbeFn = func(context.Context, domain.Workspace, string) (workspacepkg.AgentAPIStdioResult, error) {
		stdioDetections++
		return workspacepkg.AgentAPIStdioResult{URL: "http://127.0.0.1:42000", Runtime: "node"}, nil
	}
	lanURL := "http://192.168.7.187:8766"
	service.agentAPIWorkspaceHostsFn = func(context.Context, domain.Workspace) []string { return nil }
	service.agentAPILocalHostsFn = func() []string { return []string{"192.168.7.187"} }
	service.agentAPIProbeFn = func(_ context.Context, _ domain.Workspace, candidate string) error {
		if candidate == lanURL {
			return nil
		}
		return fmt.Errorf("服务端拒绝端口转发：administratively prohibited")
	}

	setup, err := service.prepareAgentAPIForTurn(context.Background(), item, domain.Task{ID: "task-1"}, t.TempDir(), "codex")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if starts != 1 {
		t.Fatalf("proxy start calls = %d, want exactly the Turn's one attempt", starts)
	}
	if stdioDetections != 0 {
		t.Fatalf("the fallback repeated stdio detection %d time(s)", stdioDetections)
	}
	if setup.Path != agentAPIPathLAN || setup.URL != lanURL || setup.Proxy != nil {
		t.Fatalf("setup = %#v, want the probed LAN address", setup)
	}
	if forward := agentAPIReverseForward(item, setup.URL); forward != nil {
		t.Fatalf("the fallback address still gets an unverified reverse tunnel: %#v", forward)
	}
}
