package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/workspace"
)

// The fake secret in these tests is a fixed string. It must never reach a
// process command line or a committed snapshot, so each test asserts both that
// the backend argv omits the value and that the generated config still carries
// it to the MCP server. The tests run the production LocalRunner against this
// test binary, which then acts as the backend process: its own os.Args is the
// command line the OS would expose.
const (
	mcpTestSecret     = "fake-board-token-6f1c2b7d"
	mcpProbeEnv       = "AHA2_TEST_MCP_PROBE"
	fakeMCPBackendEnv = "AHA2_TEST_FAKE_MCP_BACKEND"
	mcpBackendOK      = "OK"
	mcpBackendLeak    = "LEAK"
)

// TestMCPEnvProbe is not a real test. It is re-executed as the MCP server child
// by runFakeMCPBackend, prints the secret it received through its environment,
// and exits. A normal test run skips it.
func TestMCPEnvProbe(t *testing.T) {
	if os.Getenv(mcpProbeEnv) != "1" {
		t.Skip("helper process only")
	}
	fmt.Print(os.Getenv("BOARD_TOKEN"))
	os.Exit(0)
}

func testExecutable(t *testing.T) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return executable
}

// runFakeMCPBackend implements the child-process side of the integration tests.
// It inspects the config the adapter generated, verifies its own command line
// does not carry the secret, starts an MCP server child with the env value from
// that config, and reports the outcome through the backend's normal stream
// protocol.
func runFakeMCPBackend(mode string, args []string) int {
	var (
		configPath string
		secret     string
		ok         bool
	)
	switch mode {
	case "claude":
		configPath, secret, ok = claudeConfigSecret(args)
	case "codex":
		configPath, secret, ok = codexConfigSecret()
	case "dsh":
		configPath, secret, ok = dshPatchSecret(args)
	}
	if !ok || secret == "" {
		fmt.Fprintln(os.Stderr, "fake MCP backend: generated config has no MCP secret")
		return 2
	}
	status := mcpBackendOK
	if strings.Contains(strings.Join(args, "\n"), secret) {
		status = mcpBackendLeak
	}
	output := status + "|" + configPath + "|" + runMCPProbe(secret)
	if status == mcpBackendLeak {
		output = status + "|" + configPath + "|"
	}
	emitFakeMCPBackendResult(mode, output)
	return 0
}

func runMCPProbe(secret string) string {
	executable, err := os.Executable()
	if err != nil {
		return "PROBE_ERROR"
	}
	command := exec.Command(executable, "-test.run=TestMCPEnvProbe")
	command.Env = append(os.Environ(),
		mcpProbeEnv+"=1",
		fakeClaudeModeEnv+"=",
		fakeMCPBackendEnv+"=",
		"BOARD_TOKEN="+secret,
	)
	output, runErr := command.Output()
	if runErr != nil {
		return "PROBE_ERROR"
	}
	return strings.TrimSpace(string(output))
}

func emitFakeMCPBackendResult(mode, output string) {
	switch mode {
	case "claude":
		payload, _ := json.Marshal(map[string]any{
			"type": "result", "subtype": "success", "result": output, "session_id": "sess-mcp",
		})
		fmt.Println(string(payload))
	case "codex":
		started, _ := json.Marshal(map[string]any{"type": "thread.started", "thread_id": "thread-mcp"})
		message, _ := json.Marshal(map[string]any{
			"type": "item.completed", "item": map[string]any{"type": "agent_message", "text": output},
		})
		fmt.Println(string(started))
		fmt.Println(string(message))
	case "dsh":
		payload, _ := json.Marshal(map[string]any{"type": "final", "text": output, "sessionId": "dsh-mcp"})
		fmt.Println(string(payload))
	}
}

func claudeConfigSecret(args []string) (string, string, bool) {
	path := argumentValue(args, "--mcp-config")
	if path == "" {
		return "", "", false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return path, "", false
	}
	var config struct {
		MCPServers map[string]struct {
			Env map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if json.Unmarshal(raw, &config) != nil {
		return path, "", false
	}
	return path, config.MCPServers["mcp_test"].Env["BOARD_TOKEN"], true
}

func codexConfigSecret() (string, string, bool) {
	path := filepath.Join(os.Getenv("CODEX_HOME"), "config.toml")
	raw, err := os.ReadFile(path)
	if err != nil {
		return path, "", false
	}
	secret := quotedValue(string(raw), "BOARD_TOKEN", "=")
	return path, secret, secret != ""
}

func dshPatchSecret(args []string) (string, string, bool) {
	path := argumentValue(args, "--patch")
	if path == "" {
		return "", "", false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return path, "", false
	}
	secret := quotedValue(string(raw), "BOARD_TOKEN", ":")
	return path, secret, secret != ""
}

// quotedValue extracts the JSON-quoted value of the first assignment whose key
// contains key. It accepts both TOML (`"KEY" = "value"`) and the YAML-style
// (`"KEY": "value"`) shapes AHA generates.
func quotedValue(content, key, separator string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		parts := strings.SplitN(line, separator, 2)
		if len(parts) != 2 || !strings.Contains(parts[0], key) {
			continue
		}
		var value string
		if json.Unmarshal([]byte(strings.TrimSpace(parts[1])), &value) == nil && value != "" {
			return value
		}
	}
	return ""
}

func argumentValue(args []string, flag string) string {
	for index, arg := range args {
		if arg == flag && index+1 < len(args) {
			return args[index+1]
		}
	}
	return ""
}

// fakeBackendConfigPath reads the path the fake backend used from its reply.
func fakeBackendConfigPath(reply string) string {
	fields := strings.SplitN(reply, "|", 3)
	if len(fields) != 3 || fields[0] != mcpBackendOK {
		return ""
	}
	return fields[1]
}

func TestClaudeMCPSecretStaysOutOfArgvAndReachesServer(t *testing.T) {
	executable := testExecutable(t)
	t.Setenv(fakeMCPBackendEnv, "claude")
	t.Setenv(fakeClaudeModeEnv, "")
	result, err := (Claude{Binary: executable}).Execute(context.Background(), Request{
		Runner: workspace.LocalRunner{}, WorkDir: t.TempDir(),
		MCPServers: []MCPServer{{
			Name: "mcp_test", Transport: "stdio",
			Command: executable, Args: []string{"-test.run=TestMCPEnvProbe"},
			Env: map[string]string{"BOARD_TOKEN": mcpTestSecret},
		}},
	}, nil)
	if err != nil {
		t.Fatalf("claude run: %v", err)
	}
	if !strings.HasPrefix(result.Reply, mcpBackendOK+"|") {
		t.Fatalf("backend reported its command line carried the secret: %q", result.Reply)
	}
	if !strings.Contains(result.Reply, mcpTestSecret) {
		t.Fatalf("MCP server child did not receive the secret: %q", result.Reply)
	}
	path := fakeBackendConfigPath(result.Reply)
	if path == "" {
		t.Fatalf("fake backend did not report its config path: %q", result.Reply)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("generated Claude MCP config still exists after the run: %v", err)
	}
}

func TestCodexMCPSecretStaysOutOfArgvAndIsRestored(t *testing.T) {
	executable := testExecutable(t)
	t.Setenv(fakeMCPBackendEnv, "codex")
	t.Setenv(fakeClaudeModeEnv, "")
	home := t.TempDir()
	const original = "cli_auth_credentials_store = \"file\"\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := (Codex{Binary: executable}).Execute(context.Background(), Request{
		Runner: workspace.LocalRunner{}, WorkDir: t.TempDir(),
		Environment: map[string]string{"CODEX_HOME": home},
		MCPServers: []MCPServer{{
			Name: "mcp_test", Transport: "stdio", Command: executable,
			Args: []string{"-test.run=TestMCPEnvProbe"},
			Env:  map[string]string{"BOARD_TOKEN": mcpTestSecret},
		}},
	}, nil)
	if err != nil {
		t.Fatalf("codex run: %v", err)
	}
	if !strings.HasPrefix(result.Reply, mcpBackendOK+"|") {
		t.Fatalf("backend reported its command line carried the secret: %q", result.Reply)
	}
	if !strings.Contains(result.Reply, mcpTestSecret) {
		t.Fatalf("MCP server child did not receive the secret: %q", result.Reply)
	}
	restored, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != original {
		t.Fatalf("Codex config was not restored: %q", restored)
	}
}

func TestDSHSecretStaysOutOfArgvAndPatchIsRemoved(t *testing.T) {
	executable := testExecutable(t)
	t.Setenv(fakeMCPBackendEnv, "dsh")
	t.Setenv(fakeClaudeModeEnv, "")
	home := t.TempDir()
	result, err := (DSH{Binary: executable}).Execute(context.Background(), Request{
		Runner: workspace.LocalRunner{}, WorkDir: t.TempDir(), Model: "model-test", WireAPI: "responses",
		Environment: map[string]string{
			"DSH_HOME": home, "OPENAI_BASE_URL": "https://example.test/v1",
			"OPENAI_API_KEY": "fake-provider-key",
		},
		MCPServers: []MCPServer{{
			Name: "mcp_test", Transport: "stdio", Command: executable,
			Args: []string{"-test.run=TestMCPEnvProbe"},
			Env:  map[string]string{"BOARD_TOKEN": mcpTestSecret},
		}},
	}, nil)
	if err != nil {
		t.Fatalf("dsh run: %v", err)
	}
	if !strings.HasPrefix(result.Reply, mcpBackendOK+"|") {
		t.Fatalf("backend reported its command line carried the secret: %q", result.Reply)
	}
	if !strings.Contains(result.Reply, mcpTestSecret) {
		t.Fatalf("MCP server child did not receive the secret: %q", result.Reply)
	}
	path := fakeBackendConfigPath(result.Reply)
	if path == "" {
		t.Fatalf("fake backend did not report its patch path: %q", result.Reply)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("DSH profile patch still exists after the run: %v", err)
	}
}

func TestStripGeneratedCodexMCPConfigRemovesCrashedBlock(t *testing.T) {
	t.Parallel()
	const original = "cli_auth_credentials_store = \"file\"\n"
	block := codexMCPConfigTOML([]MCPServer{{
		Name: "mcp_test", Transport: "stdio", Command: "node",
		Env: map[string]string{"BOARD_TOKEN": mcpTestSecret},
	}})
	if block == "" {
		t.Fatal("generated block is empty")
	}
	if got := stripGeneratedCodexMCPConfig(original + block); got != original {
		t.Fatalf("stale block was not removed: %q", got)
	}
	if got := stripGeneratedCodexMCPConfig(block); strings.TrimSpace(got) != "" {
		t.Fatalf("block-only config left content behind: %q", got)
	}
}

func TestCodexMCPConfigTOMLRendersHeadersAndEnv(t *testing.T) {
	t.Parallel()
	config := codexMCPConfigTOML([]MCPServer{
		{
			Name: "aha2_channel", Transport: "http",
			URL:            "http://127.0.0.1:8766/api/v1/agent/channel/mcp",
			BearerTokenEnv: "AHA2_AGENT_API_TOKEN",
			Headers:        map[string]string{"X-Test": "yes", "Authorization": "Bearer literal"},
		},
		{
			Name: "mcp_local", Transport: "stdio", Command: "node",
			Args: []string{"server.mjs"}, Env: map[string]string{"MODE": "test"},
		},
	})
	for _, want := range []string{
		"[mcp_servers.aha2_channel]",
		`bearer_token_env_var = "AHA2_AGENT_API_TOKEN"`,
		"[mcp_servers.aha2_channel.http_headers]",
		`"X-Test" = "yes"`,
		"[mcp_servers.mcp_local]",
		`command = "node"`,
		`args = ["server.mjs"]`,
		"[mcp_servers.mcp_local.env]",
		`"MODE" = "test"`,
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("missing %q in Codex config:\n%s", want, config)
		}
	}
	if strings.Contains(config, "Bearer literal") {
		t.Fatalf("literal Authorization header must not be written when bearer_token_env_var is set:\n%s", config)
	}
}
