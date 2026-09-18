package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestCodexEnvHomeDirIsolatesPerSession(t *testing.T) {
	t.Parallel()
	native := domain.Workspace{Transport: "native", RootPath: "/work"}
	first := CodexEnvHomeDir(native, "/work/task", "backend_session_a")
	second := CodexEnvHomeDir(native, "/work/task", "backend_session_b")
	if first == second {
		t.Fatalf("rotated sessions must not share a Codex home: %q", first)
	}
	if want := filepath.Join("/work/task", ".aha2-context", "runtime", "codex-env", "backend_session_a"); first != want {
		t.Fatalf("native Codex home = %q, want %q", first, want)
	}
	if strings.HasPrefix(first, os.Getenv("HOME")) && os.Getenv("HOME") != "" {
		t.Fatalf("Codex home must not live under the operator home: %q", first)
	}
	remote := domain.Workspace{Transport: "wsl", Distro: "Ubuntu", RootPath: "/home/user/repo"}
	if got := CodexEnvHomeDir(remote, "/home/user/repo/task", "session_1"); got != "/home/user/repo/task/.aha2-context/runtime/codex-env/session_1" {
		t.Fatalf("remote Codex home = %q", got)
	}
}

func TestEnsureCodexEnvHomeCreatesDirectory(t *testing.T) {
	t.Parallel()
	item := domain.Workspace{Transport: "native", RootPath: t.TempDir()}
	target := CodexEnvHomeDir(item, item.RootPath, "session_1")
	if err := EnsureCodexEnvHome(context.Background(), item, LocalRunner{}, target); err != nil {
		t.Fatalf("ensure Codex home: %v", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("Codex home was not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("Codex home is not a directory: %q", target)
	}
}
