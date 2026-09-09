package backend

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/workspace"
)

type idleCodexRunner struct{}

func (idleCodexRunner) Run(ctx context.Context, _ workspace.Command, _ workspace.LineHandler) (workspace.Result, error) {
	<-ctx.Done()
	return workspace.Result{ExitCode: 1}, ctx.Err()
}

type codexCommandRunner struct{ command workspace.Command }

func (runner *codexCommandRunner) Run(_ context.Context, command workspace.Command, onLine workspace.LineHandler) (workspace.Result, error) {
	runner.command = command
	onLine(`{"type":"thread.started","thread_id":"session-tree"}`)
	onLine(`{"type":"item.completed","item":{"type":"agent_message","text":"done"}}`)
	return workspace.Result{ExitCode: 0}, nil
}

func TestCodexExecutionAlwaysOwnsTheBackendProcessTree(t *testing.T) {
	t.Parallel()
	runner := &codexCommandRunner{}
	if _, err := (Codex{}).Execute(context.Background(), Request{Runner: runner, WorkDir: t.TempDir()}, nil); err != nil {
		t.Fatal(err)
	}
	if !runner.command.KillTree {
		t.Fatal("Codex backend descendants would survive turn or service cancellation")
	}
}

func TestCodexIdleWatchdogReportsAndCancels(t *testing.T) {
	events := []string{}
	_, err := (Codex{IdleWarning: 10 * time.Millisecond, IdleTimeout: 35 * time.Millisecond, Heartbeat: 10 * time.Millisecond}).Execute(
		context.Background(), Request{Runner: idleCodexRunner{}, WorkDir: t.TempDir()}, func(event Event) { events = append(events, event.Type) },
	)
	if !errors.Is(err, ErrCodexIdleTimeout) {
		t.Fatalf("idle error = %v", err)
	}
	joined := strings.Join(events, ",")
	if !strings.Contains(joined, "agent_stalled") || !strings.Contains(joined, "agent_idle_timeout") {
		t.Fatalf("watchdog events = %v", events)
	}
}

func TestCodexArgumentsResumeAndProvider(t *testing.T) {
	t.Parallel()
	args := codexArguments(Request{
		WorkDir: "/workspace", Model: "gpt-test", ContextWindow: 200000,
		ReasoningEffort: "high", ProviderSessionID: "session-1",
		Environment: map[string]string{
			"AHA_PROVIDER_ID": "internal", "OPENAI_BASE_URL": "https://example.test/v1",
			"CODEX_WIRE_API": "responses", "CODEX_ENV_KEY": "OPENAI_API_KEY",
		},
	}, "")
	joined := strings.Join(args, " ")
	for _, expected := range []string{
		"model_context_window=200000", "model_provider", "--disable multi_agent",
		"exec", "resume", "session-1", "--json",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("arguments missing %q: %s", expected, joined)
		}
	}
}

type catalogRunner struct {
	cache       string
	writtenPath string
	writtenData string
}

func (runner *catalogRunner) Run(_ context.Context, command workspace.Command, _ workspace.LineHandler) (workspace.Result, error) {
	if len(command.Args) >= 2 && strings.Contains(command.Args[1], "models_cache.json") {
		return workspace.Result{Stdout: runner.cache, ExitCode: 0}, nil
	}
	if len(command.Args) >= 5 && strings.Contains(command.Args[1], "mkdir -p") {
		runner.writtenPath = command.Args[4]
		runner.writtenData = command.Stdin
		return workspace.Result{ExitCode: 0}, nil
	}
	return workspace.Result{ExitCode: 1}, nil
}

func TestCodexModelCatalogOverridesWindowForLocalAndRemoteRunners(t *testing.T) {
	t.Parallel()
	cache := testCodexModelsCache()
	localRoot := t.TempDir()
	cachePath := filepath.Join(localRoot, "models_cache.json")
	if err := os.WriteFile(cachePath, []byte(cache), 0o600); err != nil {
		t.Fatal(err)
	}
	request := Request{
		Runner: workspace.LocalRunner{}, WorkDir: localRoot,
		Model: "gpt-5.6-sol", ContextWindow: 1050000,
	}
	localPath := (Codex{ModelsCachePath: cachePath}).ensureModelCatalog(context.Background(), request)
	assertCodexCatalog(t, localPath)
	joined := strings.Join(codexArguments(request, localPath), " ")
	if !strings.Contains(joined, "model_catalog_json=") || strings.Contains(joined, "model_context_window=") {
		t.Fatalf("local catalog arguments = %s", joined)
	}

	remote := &catalogRunner{cache: cache}
	request.Runner = remote
	request.WorkDir = "/home/test/repo"
	remotePath := (Codex{}).ensureModelCatalog(context.Background(), request)
	if !strings.HasPrefix(remotePath, "/home/test/repo/.aha2-context/runtime/codex-models/") {
		t.Fatalf("remote catalog path = %q", remotePath)
	}
	if remote.writtenPath != remotePath || remote.writtenData == "" {
		t.Fatalf("remote catalog write = %q %d bytes", remote.writtenPath, len(remote.writtenData))
	}
	assertCodexCatalogJSON(t, []byte(remote.writtenData))
}

func assertCodexCatalog(t *testing.T, path string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	assertCodexCatalogJSON(t, raw)
}

func assertCodexCatalogJSON(t *testing.T, raw []byte) {
	t.Helper()
	var payload struct {
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Models) != 1 ||
		payload.Models[0]["slug"] != "gpt-5.6-sol" ||
		payload.Models[0]["context_window"] != float64(1050000) ||
		payload.Models[0]["max_context_window"] != float64(1050000) {
		t.Fatalf("catalog payload = %#v", payload)
	}
	if payload.Models[0]["effective_context_window_percent"] != float64(95) {
		t.Fatalf("effective percentage was not preserved: %#v", payload.Models[0])
	}
}

func testCodexModelsCache() string {
	return `{"models":[{
		"slug":"gpt-5.6-sol",
		"display_name":"GPT-5.6 Sol",
		"description":"test",
		"base_instructions":"test instructions",
		"supported_reasoning_levels":[],
		"default_reasoning_level":"medium",
		"shell_type":"shell_command",
		"visibility":"list",
		"supported_in_api":true,
		"priority":1,
		"context_window":272000,
		"max_context_window":272000,
		"effective_context_window_percent":95
	}]}`
}

func TestParseCodexLine(t *testing.T) {
	t.Parallel()
	event, reply, session := parseCodexLine(`{"type":"thread.started","thread_id":"abc"}`)
	if event.Type != "agent_session" || session != "abc" {
		t.Fatalf("unexpected session event: %#v %q", event, session)
	}
	event, reply, _ = parseCodexLine(`{"type":"item.completed","item":{"type":"agent_message","text":"done"}}`)
	if event.Type != "agent_message" || reply != "done" {
		t.Fatalf("unexpected reply event: %#v %q", event, reply)
	}
	event, _, _ = parseCodexLine(`{"type":"item.started","item":{"id":"tool-1","type":"command_execution","command":"go test ./...","status":"in_progress"}}`)
	if event.Type != "agent_command_started" || event.Data["tool_call_id"] != "tool-1" {
		t.Fatalf("tool start lost lifecycle id: %#v", event)
	}
	event, _, _ = parseCodexLine(`{"type":"item.completed","item":{"id":"tool-1","type":"command_execution","command":"go test ./...","status":"completed","exit_code":0,"aggregated_output":"ok"}}`)
	if event.Type != "agent_command_finished" || event.Data["tool_call_id"] != "tool-1" || event.Data["output_tail"] != "ok" {
		t.Fatalf("tool completion lost lifecycle data: %#v", event)
	}
}

func TestFilterEnvironmentDropsUnknownSecrets(t *testing.T) {
	t.Parallel()
	filtered := filterEnvironment(map[string]string{"OPENAI_API_KEY": "ok", "UNRELATED_SECRET": "drop"})
	if filtered["OPENAI_API_KEY"] != "ok" {
		t.Fatal("allowed key dropped")
	}
	if _, ok := filtered["UNRELATED_SECRET"]; ok {
		t.Fatal("unknown secret was forwarded")
	}
}

func TestFilterEnvironmentAllowsDeclaredCredentialKey(t *testing.T) {
	t.Parallel()
	filtered := filterEnvironment(map[string]string{
		"CODEX_ENV_KEY":       "INTERNAL_AUTH_TOKEN",
		"INTERNAL_AUTH_TOKEN": "ok",
		"PATH":                "drop",
	})
	if filtered["INTERNAL_AUTH_TOKEN"] != "ok" {
		t.Fatal("declared credential key was dropped")
	}
	if _, ok := filtered["PATH"]; ok {
		t.Fatal("dangerous environment variable was forwarded")
	}
}
