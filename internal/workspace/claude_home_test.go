package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestClaudeConfigDirIsolatesPerSession(t *testing.T) {
	t.Parallel()
	native := domain.Workspace{Transport: "native", RootPath: "/work"}
	first := ClaudeConfigDir(native, "/work/task", "backend_session_a")
	second := ClaudeConfigDir(native, "/work/task", "backend_session_b")
	if first == second {
		t.Fatalf("rotated sessions must not share a Claude config dir: %q", first)
	}
	if want := filepath.Join("/work/task", ".aha2-context", "runtime", "claude-home", "backend_session_a"); first != want {
		t.Fatalf("native Claude config dir = %q, want %q", first, want)
	}
	remote := domain.Workspace{Transport: "wsl", Distro: "Ubuntu", RootPath: "/home/user/repo"}
	if got := ClaudeConfigDir(remote, "/home/user/repo/task", "session_1"); got != "/home/user/repo/task/.aha2-context/runtime/claude-home/session_1" {
		t.Fatalf("remote Claude config dir = %q", got)
	}
}

func TestSessionHomeForRedirectsOnlyIsolatableRuns(t *testing.T) {
	t.Parallel()
	item := domain.Workspace{Transport: "native", RootPath: "/work"}
	base := SessionHomeInput{Workspace: item, WorkDir: "/work/task", SessionID: "session_1"}

	codex := base
	codex.Backend = "codex"
	home, ok := SessionHomeFor(codex)
	if !ok || home.EnvName != "CODEX_HOME" {
		t.Fatalf("codex home = %+v ok=%t, want CODEX_HOME", home, ok)
	}

	// An env-provider Claude run owns its credentials, so it can be redirected.
	envClaude := base
	envClaude.Backend = "claude"
	envClaude.EnvGroupID = "env_custom"
	home, ok = SessionHomeFor(envClaude)
	if !ok || home.EnvName != "CLAUDE_CONFIG_DIR" {
		t.Fatalf("env claude home = %+v ok=%t, want CLAUDE_CONFIG_DIR", home, ok)
	}

	// The native source authenticates with the operator's logged-in account,
	// whose credentials live in the default config dir, so it must not move.
	nativeClaude := base
	nativeClaude.Backend = "claude"
	nativeClaude.EnvGroupID = domain.ClaudeNativeEnvGroupID
	home, ok = SessionHomeFor(nativeClaude)
	if !ok {
		t.Fatal("native claude must still support writer lookup")
	}
	if home.EnvName != "" {
		t.Fatalf("native claude must not be redirected, got env %q", home.EnvName)
	}
	if home.Dir == "" {
		t.Fatal("native claude still needs a reported dir")
	}

	if _, ok := SessionHomeFor(SessionHomeInput{Backend: "stub"}); ok {
		t.Fatal("a backend without a session home must not claim support")
	}
	if SupportsSessionHome("stub") {
		t.Fatal("stub backend must not report session-home support")
	}
	if !SupportsSessionHome("codex") || !SupportsSessionHome("claude") {
		t.Fatal("codex and claude must report session-home support")
	}
}

func TestEnsureClaudeConfigDirCreatesDirectory(t *testing.T) {
	t.Parallel()
	item := domain.Workspace{Transport: "native", RootPath: t.TempDir()}
	target := ClaudeConfigDir(item, item.RootPath, "session_1")
	if err := EnsureClaudeConfigDir(context.Background(), item, LocalRunner{}, target); err != nil {
		t.Fatalf("ensure Claude config dir: %v", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("Claude config dir was not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("Claude config dir is not a directory: %q", target)
	}
}
