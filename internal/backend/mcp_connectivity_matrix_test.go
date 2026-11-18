package backend

import (
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/workspace"
)

// mcpConnectionModes are the three ways a Turn reaches AHA. Native and SSH
// stdio runs dial the configured address, so the rendered config is the same;
// the reverse tunnel is different because the reachable port only exists once
// the Turn starts.
var mcpConnectionModes = []struct {
	name    string
	reverse *workspace.ReverseForward
}{
	{name: "native"},
	{name: "ssh stdio"},
	{name: "reverse tunnel", reverse: &workspace.ReverseForward{
		Target: "127.0.0.1:8766", EnvName: "AHA2_AGENT_API_URL",
	}},
}

func mcpMatrixServers() []MCPServer {
	return []MCPServer{
		{
			Name: "aha2_channel", Transport: "http",
			URL:            "http://127.0.0.1:8766/api/v1/agent/channel/mcp",
			URLFromEnv:     "AHA2_AGENT_API_URL",
			URLPath:        "/api/v1/agent/channel/mcp",
			BearerTokenEnv: "AHA2_AGENT_API_TOKEN",
		},
		{
			Name: "mcp_local", Transport: "stdio", Command: "node",
			Args: []string{"server.mjs"}, Env: map[string]string{"MODE": "test"},
		},
	}
}

// TestMCPConfigMatrixAcrossBackendsAndConnectionModes pins the address rule for
// every backend x connection-mode pair. Claude and DSH resolve the AHA address
// inside the workspace, so they keep the env placeholder under a reverse tunnel.
// Codex renders config.toml literally and must drop an env-resolved server
// rather than bake in an address the tunnel never exposes.
func TestMCPConfigMatrixAcrossBackendsAndConnectionModes(t *testing.T) {
	t.Parallel()
	const channelURL = "http://127.0.0.1:8766/api/v1/agent/channel/mcp"
	const channelPath = "/api/v1/agent/channel/mcp"
	for _, mode := range mcpConnectionModes {
		mode := mode
		t.Run(mode.name, func(t *testing.T) {
			t.Parallel()
			servers := mcpMatrixServers()

			t.Run("claude", func(t *testing.T) {
				config := claudeMCPConfig(servers)
				if !strings.Contains(config, `"url":"${AHA2_AGENT_API_URL}`+channelPath+`"`) {
					t.Fatalf("Claude lost the runtime URL placeholder: %s", config)
				}
				if !strings.Contains(config, `"command":"node"`) {
					t.Fatalf("Claude lost the stdio server: %s", config)
				}
				if strings.Contains(config, channelURL) {
					t.Fatalf("Claude baked in the configured address: %s", config)
				}
			})

			t.Run("dsh", func(t *testing.T) {
				patch, err := dshProfilePatch(Request{
					Model: "model-test", WireAPI: "responses",
					Environment: map[string]string{
						"OPENAI_BASE_URL": "https://example.test/v1",
						"OPENAI_API_KEY":  "test-key",
					},
					MCPServers: servers,
				}, "/tmp/plugin.mjs", "/tmp/inbox.mjs")
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(patch, "url: !!js '`${process.env.AHA2_AGENT_API_URL}"+channelPath+"`'") {
					t.Fatalf("DSH lost the runtime URL expression:\n%s", patch)
				}
				if !strings.Contains(patch, `command: "node"`) {
					t.Fatalf("DSH lost the stdio server:\n%s", patch)
				}
				if strings.Contains(patch, channelURL) {
					t.Fatalf("DSH baked in the configured address:\n%s", patch)
				}
			})

			t.Run("codex", func(t *testing.T) {
				kept, skipped := codexMCPServersForRequest(Request{
					MCPServers: servers, ReverseForward: mode.reverse,
				})
				config := codexMCPConfigTOML(kept)
				if !strings.Contains(config, `command = "node"`) {
					t.Fatalf("Codex lost the stdio server:\n%s", config)
				}
				if mode.reverse == nil {
					if len(skipped) != 0 {
						t.Fatalf("Codex skipped %v without a reverse tunnel", skipped)
					}
					if !strings.Contains(config, `url = "`+channelURL+`"`) {
						t.Fatalf("Codex lost the reachable URL:\n%s", config)
					}
					return
				}
				if len(skipped) != 1 || !strings.Contains(skipped[0], "aha2_channel") ||
					!strings.Contains(skipped[0], "AHA2_AGENT_API_URL") {
					t.Fatalf("skipped=%v, want the env-resolved channel server", skipped)
				}
				if strings.Contains(config, channelURL) || strings.Contains(config, "mcp_servers.aha2_channel") {
					t.Fatalf("Codex wrote an unreachable MCP URL:\n%s", config)
				}
				event := codexMCPDiagnosticEvent(skipped)
				message, _ := event.Data["message"].(string)
				if event.Type != "agent_progress" || event.Data["diagnostic"] != "codex_mcp_address_unresolved" ||
					!strings.Contains(message, "aha2_channel") {
					t.Fatalf("diagnostic event=%#v", event)
				}
			})
		})
	}
}

// TestCodexKeepsLiteralMCPURLsUnderReverseTunnel makes sure the filter only
// drops servers whose address is deferred to the environment. A user server or
// the pet endpoint already has a concrete URL the workspace can dial.
func TestCodexKeepsLiteralMCPURLsUnderReverseTunnel(t *testing.T) {
	t.Parallel()
	kept, skipped := codexMCPServersForRequest(Request{
		ReverseForward: &workspace.ReverseForward{Target: "127.0.0.1:8766", EnvName: "AHA2_AGENT_API_URL"},
		MCPServers: []MCPServer{
			{Name: "user_http", Transport: "http", URL: "http://127.0.0.1:9000/mcp"},
			{Name: "aha2-pet", Transport: "http", URL: "http://127.0.0.1:8766/api/v1/pet/mcp"},
			{Name: "aha2_mail", Transport: "http", URL: "http://127.0.0.1:8766/api/v1/agent/mail/mcp",
				URLFromEnv: "AHA2_AGENT_API_URL", URLPath: "/api/v1/agent/mail/mcp"},
		},
	})
	if len(skipped) != 1 || !strings.Contains(skipped[0], "aha2_mail") {
		t.Fatalf("skipped=%v, want only the mail server", skipped)
	}
	if len(kept) != 2 || kept[0].Name != "user_http" || kept[1].Name != "aha2-pet" {
		t.Fatalf("kept=%#v", kept)
	}
}
