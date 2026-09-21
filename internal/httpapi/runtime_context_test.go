package httpapi

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/workspace"
)

type runtimeContextRunner struct {
	command workspace.Command
}

func (runner *runtimeContextRunner) Run(
	_ context.Context,
	command workspace.Command,
	_ workspace.LineHandler,
) (workspace.Result, error) {
	runner.command = command
	return workspace.Result{
		ExitCode: 0,
		Stdout: `{"payload":{"type":"token_count","info":{"model_context_window":258400,"last_token_usage":{"input_tokens":100}}}}
{"payload":{"type":"token_count","info":{"model_context_window":997500,"last_token_usage":{"input_tokens":128592}}}}
`,
	}, nil
}

func TestCodexRuntimeContextUsesRemoteRunner(t *testing.T) {
	t.Parallel()
	runner := &runtimeContextRunner{}
	sample, ok := codexRuntimeContextFromRunner(
		context.Background(),
		runner,
		"/home/test/repo",
		"__HOME_CODEX__",
		"session-123",
	)
	if !ok || sample.InputTokens != 128592 || sample.ContextWindow != 997500 {
		t.Fatalf("remote runtime sample = %#v, ok=%v", sample, ok)
	}
	if runner.command.Executable != "sh" ||
		runner.command.Dir != "/home/test/repo" ||
		!strings.Contains(strings.Join(runner.command.Args, " "), "session-123") {
		t.Fatalf("remote runtime command = %#v", runner.command)
	}
}

type artifactSizeRunner struct {
	command workspace.Command
}

func (runner *artifactSizeRunner) Run(
	_ context.Context,
	command workspace.Command,
	_ workspace.LineHandler,
) (workspace.Result, error) {
	runner.command = command
	return workspace.Result{ExitCode: 0, Stdout: "8192\n"}, nil
}

func TestBackendSessionArtifactSizeUsesRemoteRunner(t *testing.T) {
	t.Parallel()
	runner := &artifactSizeRunner{}
	size, ok := backendSessionArtifactSizeFromRunner(
		context.Background(), runner, "/home/test/repo", "__HOME_CLAUDE__", "", "session-456",
	)
	if !ok || size != 8192 {
		t.Fatalf("remote artifact size = %d, ok=%v", size, ok)
	}
	args := strings.Join(runner.command.Args, " ")
	if !strings.Contains(args, "__HOME_CLAUDE__") || !strings.Contains(args, "session-456") {
		t.Fatalf("remote artifact command = %#v", runner.command)
	}
}

// An Env-provider Claude session writes its transcript into the session's own
// config directory, not the operator's ~/.claude. The lookup has to search that
// directory, or the context page reports the session file as unavailable.
func TestClaudeSessionRootsIncludeIsolatedConfigDir(t *testing.T) {
	t.Parallel()
	workDir := t.TempDir()
	session := domain.BackendSession{ID: "backend_session_abc", Backend: "claude", ProviderSession: "ps-1"}
	item := domain.Workspace{ID: "ws", Locality: "local", Transport: "native"}

	root := isolatedClaudeSessionRoot(session, item, workDir)
	want := filepath.ToSlash(filepath.Join(workDir, ".aha2-context", "runtime", "claude-home", session.ID, "projects"))
	if root != want {
		t.Fatalf("isolated root = %q, want %q", root, want)
	}
	// A native-source session keeps the operator's config directory, so it must
	// not claim an isolated one.
	if got := isolatedClaudeSessionRoot(domain.BackendSession{ID: "x", Backend: "codex"}, item, workDir); got != "" {
		t.Fatalf("codex session claimed a Claude isolated root: %q", got)
	}
}

// A transcript that exists only in the isolated directory must be found, since
// that is where an Env-provider session actually writes it.
func TestClaudeArtifactSizeFindsIsolatedTranscript(t *testing.T) {
	t.Parallel()
	workDir := t.TempDir()
	session := domain.BackendSession{ID: "backend_session_abc", Backend: "claude", ProviderSession: "ps-1"}
	item := domain.Workspace{ID: "ws", Locality: "local", Transport: "native"}
	content := `{"type":"assistant"}` + "\n"
	file := filepath.Join(isolatedClaudeSessionRoot(session, item, workDir),
		"-home-test-repo", session.ProviderSession+".jsonl")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	size, ok := backendSessionArtifactSize(context.Background(), session, item, workDir)
	if !ok || size != int64(len(content)) {
		t.Fatalf("isolated artifact size = %d, exists=%v", size, ok)
	}
}

func TestOfficialCodexRuntimeContextUsesIsolatedProfile(t *testing.T) {
	t.Parallel()
	workDir := t.TempDir()
	session := domain.BackendSession{
		ID: "backend-session-official", Backend: "codex", CodexAccountID: "account-1",
		ProviderSession: "provider-session-official",
	}
	file := filepath.Join(
		workDir, ".aha2-context", "runtime", "codex-auth", session.ID,
		"sessions", "2026", "09", "04", "rollout-"+session.ProviderSession+".jsonl",
	)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	content := `{"payload":{"type":"token_count","info":{"model_context_window":872000,"last_token_usage":{"input_tokens":32000}}}}` + "\n"
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	sample, ok := codexRuntimeContext(
		context.Background(), session, domain.Workspace{Transport: "native"}, workDir,
	)
	if !ok || sample.ContextWindow != 872000 || sample.InputTokens != 32000 {
		t.Fatalf("official runtime sample = %#v, ok=%v", sample, ok)
	}
	if size, exists := backendSessionArtifactSize(
		context.Background(), session, domain.Workspace{Transport: "native"}, workDir,
	); !exists || size != int64(len(content)) {
		t.Fatalf("official artifact size = %d, exists=%v", size, exists)
	}
}

// A Claude transcript records usage per assistant message, and the newest one is
// the current occupancy. That is what the context page shows; a turn's own usage
// is the CLI's session cumulative, which after a few turns exceeds the window and
// gets rejected as inconsistent -- leaving the page blank.
func TestClaudeContextSampleReadsSingleRequestUsage(t *testing.T) {
	t.Parallel()
	sample, ok := claudeContextSample(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"usage": map[string]any{
				"input_tokens":                float64(1116),
				"cache_read_input_tokens":     float64(420146),
				"cache_creation_input_tokens": float64(2000),
			},
		},
	})
	if !ok {
		t.Fatal("a usage-bearing assistant message must yield a sample")
	}
	want := float64(1116 + 420146 + 2000)
	if sample.InputTokens != want {
		t.Fatalf("input tokens = %v, want %v", sample.InputTokens, want)
	}
	// Window is not recorded per message; the turn's value is authoritative and
	// the caller must not overwrite it.
	if sample.ContextWindow != 0 {
		t.Fatalf("transcript sample claimed a window: %d", sample.ContextWindow)
	}
}

func TestClaudeContextSampleIgnoresRecordsWithoutUsage(t *testing.T) {
	t.Parallel()
	for name, record := range map[string]map[string]any{
		"no message":    {"type": "summary"},
		"no usage":      {"message": map[string]any{"content": "hi"}},
		"empty usage":   {"message": map[string]any{"usage": map[string]any{}}},
		"zero counters": {"message": map[string]any{"usage": map[string]any{"input_tokens": float64(0)}}},
	} {
		t.Run(name, func(t *testing.T) {
			if sample, ok := claudeContextSample(record); ok {
				t.Fatalf("record yielded a sample: %#v", sample)
			}
		})
	}
}

// A WSL workspace is reachable from a Windows AHA through \\wsl.localhost, but
// from a locally-run AHA the same root is simply a local path. Rewriting it
// unconditionally produces a path no process can resolve, so the session is
// reported as having no transcript and the context page falls back to an
// estimate. This is what the running WSL-side AHA did.
func TestBackendSessionArtifactPathKeepsLocalPathsLocal(t *testing.T) {
	t.Parallel()
	workDir := t.TempDir()
	session := domain.BackendSession{ID: "bs-local", Backend: "claude", ProviderSession: "ps-local"}
	item := domain.Workspace{ID: "ws", Transport: "wsl", Distro: "Ubuntu-24.04", RootPath: workDir}
	file := filepath.Join(
		isolatedClaudeSessionRoot(session, item, workDir),
		"-home-repo", session.ProviderSession+".jsonl",
	)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path, ok := backendSessionArtifactPath(session, item, workDir)
	if !ok {
		t.Fatalf("a WSL workspace's transcript was not found; looked for %q", file)
	}
	if strings.HasPrefix(path, `\\`) {
		t.Fatalf("path %q was rewritten as a Windows share", path)
	}
	if path != file {
		t.Fatalf("path = %q, want %q", path, file)
	}
}
