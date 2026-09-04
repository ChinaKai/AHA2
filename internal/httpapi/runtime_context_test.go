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
		context.Background(), runner, "/home/test/repo", "__HOME_CLAUDE__", "session-456",
	)
	if !ok || size != 8192 {
		t.Fatalf("remote artifact size = %d, ok=%v", size, ok)
	}
	args := strings.Join(runner.command.Args, " ")
	if !strings.Contains(args, "__HOME_CLAUDE__") || !strings.Contains(args, "session-456") {
		t.Fatalf("remote artifact command = %#v", runner.command)
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
