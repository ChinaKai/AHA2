package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/ChinaKai/AHA2/internal/workspace"
)

// fakeRemoteMCPRunner simulates the file operations the backend runs through a
// workspace runner: write, atomic write, read-if-exists, remove and mkdir. The
// backend command itself is delegated to backend. This lets cleanup and restore
// failures be tested without a remote host.
type fakeRemoteMCPRunner struct {
	mu sync.Mutex

	files    map[string]string
	commands []workspace.Command

	readExit   int
	readCalls  int
	writeExit  int
	writeCalls int
	// writeFailAt fails every write at or after this 1-based call number.
	writeFailAt int
	removeExit  int
	removeCalls int
	// removeFailAt fails every remove at or after this 1-based call number.
	removeFailAt int

	backend func(workspace.Command, workspace.LineHandler) (workspace.Result, error)
}

func (runner *fakeRemoteMCPRunner) DetectedPlatform() string { return "linux/amd64" }

func (runner *fakeRemoteMCPRunner) Run(_ context.Context, command workspace.Command, onLine workspace.LineHandler) (workspace.Result, error) {
	runner.mu.Lock()
	runner.commands = append(runner.commands, command)
	runner.mu.Unlock()

	joined := strings.Join(command.Args, " ")
	switch {
	case strings.Contains(joined, "aha-read-file"):
		target := lastArgument(command.Args)
		runner.mu.Lock()
		defer runner.mu.Unlock()
		runner.readCalls++
		if runner.readExit != 0 {
			return workspace.Result{ExitCode: runner.readExit, Stderr: "read failed"}, nil
		}
		content, ok := runner.files[target]
		if !ok {
			return workspace.Result{ExitCode: 3}, nil
		}
		return workspace.Result{ExitCode: 0, Stdout: content}, nil
	case strings.Contains(joined, "aha-write-atomic"), strings.Contains(joined, "aha-write-file"):
		target := lastArgument(command.Args)
		runner.mu.Lock()
		defer runner.mu.Unlock()
		runner.writeCalls++
		if runner.writeExit != 0 || (runner.writeFailAt > 0 && runner.writeCalls >= runner.writeFailAt) {
			return workspace.Result{ExitCode: 1, Stderr: "write failed"}, nil
		}
		if runner.files == nil {
			runner.files = map[string]string{}
		}
		runner.files[target] = command.Stdin
		return workspace.Result{ExitCode: 0}, nil
	case strings.Contains(joined, "aha-remove-file"):
		target := lastArgument(command.Args)
		runner.mu.Lock()
		defer runner.mu.Unlock()
		runner.removeCalls++
		if runner.removeExit != 0 || (runner.removeFailAt > 0 && runner.removeCalls >= runner.removeFailAt) {
			return workspace.Result{ExitCode: 1, Stderr: "remove failed"}, nil
		}
		delete(runner.files, target)
		return workspace.Result{ExitCode: 0}, nil
	case command.Executable == "mkdir":
		return workspace.Result{ExitCode: 0}, nil
	case command.Executable == "sh" && strings.Contains(joined, "rollout-"):
		// Codex rollout discovery; there is no rollout file in this fake.
		return workspace.Result{ExitCode: 0}, nil
	case command.Executable == "sh" && strings.Contains(joined, "wc -c"):
		return workspace.Result{ExitCode: 0, Stdout: "0"}, nil
	case command.Executable == "tail":
		return workspace.Result{ExitCode: 0}, nil
	default:
		if runner.backend == nil {
			return workspace.Result{ExitCode: 1}, fmt.Errorf("unexpected remote command: %s", joined)
		}
		return runner.backend(command, onLine)
	}
}

func lastArgument(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[len(args)-1]
}

func fakeClaudeRemoteBackend(reply string) func(workspace.Command, workspace.LineHandler) (workspace.Result, error) {
	return func(_ workspace.Command, onLine workspace.LineHandler) (workspace.Result, error) {
		payload, _ := json.Marshal(map[string]any{
			"type": "result", "subtype": "success", "result": reply, "session_id": "sess-remote",
		})
		if onLine != nil {
			onLine(string(payload))
		}
		return workspace.Result{ExitCode: 0}, nil
	}
}

func fakeCodexRemoteBackend(reply string) func(workspace.Command, workspace.LineHandler) (workspace.Result, error) {
	return func(_ workspace.Command, onLine workspace.LineHandler) (workspace.Result, error) {
		started, _ := json.Marshal(map[string]any{"type": "thread.started", "thread_id": "thread-remote"})
		message, _ := json.Marshal(map[string]any{
			"type": "item.completed", "item": map[string]any{"type": "agent_message", "text": reply},
		})
		if onLine != nil {
			onLine(string(started))
			onLine(string(message))
		}
		return workspace.Result{ExitCode: 0}, nil
	}
}

func fakeDSHRemoteBackend(reply string) func(workspace.Command, workspace.LineHandler) (workspace.Result, error) {
	return func(_ workspace.Command, onLine workspace.LineHandler) (workspace.Result, error) {
		payload, _ := json.Marshal(map[string]any{"type": "final", "text": reply, "sessionId": "dsh-remote"})
		if onLine != nil {
			onLine(string(payload))
		}
		return workspace.Result{ExitCode: 0}, nil
	}
}

func remoteMCPRequest(runner *fakeRemoteMCPRunner) Request {
	return Request{
		Runner: runner, WorkDir: "/workspace",
		MCPServers: []MCPServer{{
			Name: "mcp_test", Transport: "stdio", Command: "node",
			Args: []string{"server.mjs"},
			Env:  map[string]string{"BOARD_TOKEN": mcpTestSecret},
		}},
	}
}

func TestLocalFileAtomicWriteReplacesAndCleansTemp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "config.toml")
	if err := writeLocalFileAtomic(target, "first"); err != nil {
		t.Fatal(err)
	}
	if err := writeLocalFileAtomic(target, "second"); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "second" {
		t.Fatalf("atomic replace produced %q", content)
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".aha2-tmp-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary files were not cleaned up: %v %v", matches, err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(target)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("atomic file mode = %o, want 600", info.Mode().Perm())
		}
	}
}

func TestJoinRemotePathPreservesUNC(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		root   string
		values []string
		want   string
	}{
		{root: `\\server\share\base`, values: []string{".aha2-context", "runtime"}, want: "//server/share/base/.aha2-context/runtime"},
		{root: "//server/share", values: []string{"a", "b"}, want: "//server/share/a/b"},
		{root: `C:\work`, values: []string{"a"}, want: "C:/work/a"},
		{root: "/home/u/work", values: []string{"a"}, want: "/home/u/work/a"},
	} {
		if got := joinRemotePath(scenario.root, scenario.values...); got != scenario.want {
			t.Fatalf("joinRemotePath(%q, %v) = %q, want %q", scenario.root, scenario.values, got, scenario.want)
		}
	}
}

func TestMCPConfigPathsKeepUNC(t *testing.T) {
	t.Parallel()
	runner := &fakeRemoteMCPRunner{}
	path, local := claudeMCPConfigPath(Request{Runner: runner, WorkDir: `\\server\share\work`})
	if local {
		t.Fatal("remote runner treated as local")
	}
	if !strings.HasPrefix(path, "//server/share/work/") {
		t.Fatalf("Claude config path lost UNC semantics: %q", path)
	}
	codexPath := codexConfigPath(`\\server\share\codex`, false)
	if !strings.HasPrefix(codexPath, "//server/share/codex/") {
		t.Fatalf("Codex config path lost UNC semantics: %q", codexPath)
	}
}

func TestClaudeStableConfigReplacesStaleAndKeepsUnrelated(t *testing.T) {
	executable := testExecutable(t)
	workDir := t.TempDir()
	configDir := t.TempDir()
	request := Request{Runner: workspace.LocalRunner{}, WorkDir: workDir, ClaudeConfigDir: configDir}
	path, local := claudeMCPConfigPath(request)
	if !local {
		t.Fatal("LocalRunner was not treated as local")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	stale := claudeMCPConfig([]MCPServer{{
		Name: "mcp_test", Transport: "stdio", Command: "python",
		Env: map[string]string{"BOARD_TOKEN": "stale-mcp-secret-0000"},
	}})
	if err := os.WriteFile(path, []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	unrelated, err := os.CreateTemp(filepath.Dir(path), "unrelated-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unrelated.WriteString("keep-me"); err != nil {
		t.Fatal(err)
	}
	_ = unrelated.Close()
	defer os.Remove(unrelated.Name())

	request.MCPServers = []MCPServer{{
		Name: "mcp_test", Transport: "stdio", Command: executable,
		Args: []string{"-test.run=TestMCPEnvProbe"},
		Env:  map[string]string{"BOARD_TOKEN": mcpTestSecret},
	}}
	t.Setenv(fakeMCPBackendEnv, "claude")
	t.Setenv(fakeClaudeModeEnv, "")
	result, err := (Claude{Binary: executable}).Execute(context.Background(), request, nil)
	if err != nil {
		t.Fatalf("claude run: %v", err)
	}
	if !strings.Contains(result.Reply, mcpTestSecret) || strings.Contains(result.Reply, "stale-mcp-secret-0000") {
		t.Fatalf("stale config was not replaced: %q", result.Reply)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("generated config still exists after the run: %v", err)
	}
	if _, err := os.Stat(unrelated.Name()); err != nil {
		t.Fatalf("unrelated file was removed: %v", err)
	}
}

func TestClaudeMCPConfigFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows uses inherited ACLs rather than POSIX mode bits")
	}
	t.Parallel()
	request := Request{Runner: workspace.LocalRunner{}, WorkDir: t.TempDir()}
	file, err := writeClaudeMCPConfig(context.Background(), request, `{"mcpServers":{}}`)
	if err != nil {
		t.Fatal(err)
	}
	defer file.cleanup(context.Background())
	info, err := os.Stat(file.path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %o, want 600", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(filepath.Dir(file.path))
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm()&0o077 != 0 {
		t.Fatalf("config directory mode = %o, want owner-only", dirInfo.Mode().Perm())
	}
}

func TestClaudeCleanupFailureFailsTurn(t *testing.T) {
	runner := &fakeRemoteMCPRunner{removeFailAt: 2, backend: fakeClaudeRemoteBackend("done")}
	_, err := (Claude{Binary: "fake-claude"}).Execute(context.Background(), remoteMCPRequest(runner), nil)
	if err == nil || !strings.Contains(err.Error(), "remove generated MCP config") {
		t.Fatalf("cleanup failure was not reported: %v", err)
	}
}

func TestDSHCleanupFailureFailsTurn(t *testing.T) {
	runner := &fakeRemoteMCPRunner{removeFailAt: 1, backend: fakeDSHRemoteBackend("done")}
	request := remoteMCPRequest(runner)
	request.Model = "model-test"
	request.WireAPI = "responses"
	request.Environment = map[string]string{
		"DSH_HOME": "/remote/dsh", "OPENAI_BASE_URL": "https://example.test/v1",
		"OPENAI_API_KEY": "fake-provider-key",
	}
	_, err := (DSH{Binary: "fake-dsh"}).Execute(context.Background(), request, nil)
	if err == nil || !strings.Contains(err.Error(), "remove generated DSH profile patch") {
		t.Fatalf("cleanup failure was not reported: %v", err)
	}
}

func TestCodexReadFailureDoesNotOverwrite(t *testing.T) {
	runner := &fakeRemoteMCPRunner{readExit: 1}
	request := remoteMCPRequest(runner)
	request.Environment = map[string]string{"CODEX_HOME": "/remote/codex"}
	_, err := (Codex{Binary: "fake-codex"}).Execute(context.Background(), request, nil)
	if err == nil {
		t.Fatal("read failure was reported as a missing config")
	}
	runner.mu.Lock()
	writes := runner.writeCalls
	runner.mu.Unlock()
	if writes != 0 {
		t.Fatalf("a read failure caused %d config writes", writes)
	}
}

func TestCodexCleanupFailureFailsTurn(t *testing.T) {
	runner := &fakeRemoteMCPRunner{
		files:       map[string]string{"/remote/codex/config.toml": "cli_auth_credentials_store = \"file\"\n"},
		writeFailAt: 3,
		backend:     fakeCodexRemoteBackend("done"),
	}
	request := remoteMCPRequest(runner)
	request.Environment = map[string]string{"CODEX_HOME": "/remote/codex"}
	_, err := (Codex{Binary: "fake-codex"}).Execute(context.Background(), request, nil)
	if err == nil || !strings.Contains(err.Error(), "restore Codex MCP config") {
		t.Fatalf("restore failure was not reported: %v", err)
	}
}

func TestClaudeCleanupRunsOnCancelAndTimeout(t *testing.T) {
	for _, scenario := range []struct {
		name string
		err  error
	}{
		{name: "cancel", err: context.Canceled},
		{name: "timeout", err: context.DeadlineExceeded},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			runner := &fakeRemoteMCPRunner{backend: func(workspace.Command, workspace.LineHandler) (workspace.Result, error) {
				return workspace.Result{ExitCode: 1}, scenario.err
			}}
			_, err := (Claude{Binary: "fake-claude"}).Execute(context.Background(), remoteMCPRequest(runner), nil)
			if !errors.Is(err, scenario.err) {
				t.Fatalf("run error = %v, want %v", err, scenario.err)
			}
			runner.mu.Lock()
			removes := runner.removeCalls
			runner.mu.Unlock()
			if removes < 2 {
				t.Fatalf("cleanup did not run after %s: %d remove calls", scenario.name, removes)
			}
		})
	}
}

func TestClaudeCanceledRunReportsCleanupFailure(t *testing.T) {
	runner := &fakeRemoteMCPRunner{
		removeFailAt: 2,
		backend: func(workspace.Command, workspace.LineHandler) (workspace.Result, error) {
			return workspace.Result{ExitCode: 1}, context.Canceled
		},
	}
	_, err := (Claude{Binary: "fake-claude"}).Execute(context.Background(), remoteMCPRequest(runner), nil)
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "remove generated MCP config") {
		t.Fatalf("cancellation and cleanup failure were not both reported: %v", err)
	}
}

func TestCodexCrashBackupIsRecovered(t *testing.T) {
	home := t.TempDir()
	const original = "cli_auth_credentials_store = \"file\"\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	request := Request{
		Runner:      workspace.LocalRunner{},
		Environment: map[string]string{"CODEX_HOME": home},
	}
	firstContent := codexMCPConfigTOML([]MCPServer{{
		Name: "mcp_test", Transport: "stdio", Command: "node",
		Env: map[string]string{"BOARD_TOKEN": "first-crash-secret-1111"},
	}})
	first, err := prepareCodexMCPConfig(context.Background(), request, firstContent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(first.backupPath); err != nil {
		t.Fatalf("backup missing before simulated crash: %v", err)
	}

	secondContent := codexMCPConfigTOML([]MCPServer{{
		Name: "mcp_test", Transport: "stdio", Command: "node",
		Env: map[string]string{"BOARD_TOKEN": "second-crash-secret-2222"},
	}})
	second, err := prepareCodexMCPConfig(context.Background(), request, secondContent)
	if err != nil {
		t.Fatal(err)
	}
	main, err := os.ReadFile(second.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(main), "first-crash-secret-1111") {
		t.Fatalf("stale block survived recovery: %s", main)
	}
	if !strings.Contains(string(main), "second-crash-secret-2222") {
		t.Fatalf("new block missing after recovery: %s", main)
	}
	if err := second.cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(second.path)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != original {
		t.Fatalf("original config was not restored: %q", restored)
	}
	if _, err := os.Stat(second.backupPath); !os.IsNotExist(err) {
		t.Fatalf("backup still exists after cleanup: %v", err)
	}
}

func TestCodexCleanupOnlyRestoresWhenMarkerPresent(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, "config.toml")
	const original = "cli_auth_credentials_store = \"file\"\n"
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	request := Request{
		Runner:      workspace.LocalRunner{},
		Environment: map[string]string{"CODEX_HOME": home},
	}
	file, err := prepareCodexMCPConfig(context.Background(), request, codexMCPConfigTOML([]MCPServer{{
		Name: "mcp_test", Transport: "stdio", Command: "node",
		Env: map[string]string{"BOARD_TOKEN": mcpTestSecret},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	const userEdit = "user-edited\n"
	if err := os.WriteFile(configPath, []byte(userEdit), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := file.cleanup(context.Background()); err == nil || !strings.Contains(err.Error(), "changed during the run") {
		t.Fatalf("external edit was not reported: %v", err)
	}
	current, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != userEdit {
		t.Fatalf("cleanup overwrote an external edit: %q", current)
	}
	if _, err := os.Stat(file.backupPath); err != nil {
		t.Fatalf("recovery backup was removed despite the conflict: %v", err)
	}
}

func TestCodexStaleBackupDoesNotOverwriteExternalEdit(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, "config.toml")
	backupPath := configPath + codexMCPConfigBackupSuffix
	if err := os.WriteFile(configPath, []byte("external-edit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backupPath, []byte("old-original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	request := Request{
		Runner:      workspace.LocalRunner{},
		Environment: map[string]string{"CODEX_HOME": home},
	}
	_, err := prepareCodexMCPConfig(context.Background(), request, codexMCPConfigTOML([]MCPServer{{
		Name: "mcp_test", Transport: "stdio", Command: "node",
		Env: map[string]string{"BOARD_TOKEN": mcpTestSecret},
	}}))
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("conflicting backup was not reported: %v", err)
	}
	current, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != "external-edit\n" {
		t.Fatalf("external edit was overwritten: %q", current)
	}
}

func TestCodexCleanupRestoresMissingFile(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, "config.toml")
	const original = "cli_auth_credentials_store = \"file\"\n"
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	request := Request{
		Runner:      workspace.LocalRunner{},
		Environment: map[string]string{"CODEX_HOME": home},
	}
	file, err := prepareCodexMCPConfig(context.Background(), request, codexMCPConfigTOML([]MCPServer{{
		Name: "mcp_test", Transport: "stdio", Command: "node",
		Env: map[string]string{"BOARD_TOKEN": mcpTestSecret},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(configPath); err != nil {
		t.Fatal(err)
	}
	if err := file.cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != original {
		t.Fatalf("missing config was not restored: %q", restored)
	}
}

func TestStripGeneratedCodexMCPConfigHandlesTruncatedBlock(t *testing.T) {
	t.Parallel()
	const original = "cli_auth_credentials_store = \"file\"\n"
	truncated := original + "\n" + codexMCPConfigBegin + "\n[mcp_servers.mcp_test]\n"
	if got := stripGeneratedCodexMCPConfig(truncated); got != original {
		t.Fatalf("truncated block left content behind: %q", got)
	}
}
