package backend

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/workspace"
)

func TestDSHArgumentsResumeAndStdin(t *testing.T) {
	t.Parallel()
	args := dshArguments(Request{ProviderSessionID: "session-existing"}, "/tmp/aha.yml")
	joined := strings.Join(args, " ")
	for _, expected := range []string{
		"--profile headless", "--patch /tmp/aha.yml", "--json",
		"--session-id session-existing", "-",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("arguments missing %q: %s", expected, joined)
		}
	}
}

func TestDSHProfilePatchReusesAHAEnvProviderWithoutSecret(t *testing.T) {
	t.Parallel()
	patch, err := dshProfilePatch(Request{
		Model: "model-test", WireAPI: "responses", ContextWindow: 200000,
		ReasoningEffort: "high",
		Environment: map[string]string{
			"OPENAI_BASE_URL": "https://gateway.example/v1",
			"OPENAI_API_KEY":  "must-not-enter-patch",
			"CODEX_ENV_KEY":   "OPENAI_API_KEY",
		},
	}, "/tmp/aha-agent-api-env.mjs")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"api: openai-responses",
		`baseURL: "https://gateway.example/v1"`,
		"apiKeyEnv: OPENAI_API_KEY",
		`- id: "model-test"`,
		"contextWindow: 200000",
		"reasoningEfforts:",
		"off: none",
		"max: max",
		"provider: aha-env",
		`model: "model-test"`,
		`reasoningEffort: "high"`,
		"policy: never",
		"sandbox: read-only",
		`sandbox: workspace-write`,
		`sandbox: danger-full-access`,
		"- insert:",
		"id: aha-agent-api-env",
		`name: "/tmp/aha-agent-api-env.mjs"`,
		"id: tool-subagent",
		"id: tool-workflow",
	} {
		if !strings.Contains(patch, expected) {
			t.Fatalf("profile patch missing %q:\n%s", expected, patch)
		}
	}
	if strings.Contains(patch, "must-not-enter-patch") {
		t.Fatal("provider secret was written into the DSH profile patch")
	}
}

func TestDSHProfilePatchDeclaresImageInputUnlessModelOptsOut(t *testing.T) {
	t.Parallel()
	request := Request{
		Model: "vision-test", WireAPI: "anthropic_messages", ContextWindow: 1000000,
		SupportsImageInput: true,
		Environment: map[string]string{
			"ANTHROPIC_BASE_URL":   "https://gateway.example",
			"ANTHROPIC_API_KEY":    "must-not-enter-patch",
			"ANTHROPIC_AUTH_TOKEN": "must-not-enter-patch",
		},
	}
	patch, err := dshProfilePatch(request, "/tmp/plugin.mjs")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(patch, "input: [text, image]") {
		t.Fatalf("default image capability was not declared:\n%s", patch)
	}
	if strings.Contains(patch, "must-not-enter-patch") {
		t.Fatal("provider secret was written into the DSH profile patch")
	}

	request.SupportsImageInput = false
	textPatch, err := dshProfilePatch(request, "/tmp/plugin.mjs")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(textPatch, "input:") {
		t.Fatalf("opted-out model declared an input modality:\n%s", textPatch)
	}
}

func TestDSHAgentAPIEnvPluginUsesTrustedDSHNamespace(t *testing.T) {
	t.Parallel()
	plugin := dshAgentAPIEnvPlugin()
	for _, expected := range []string{
		"inject = ['shellEnv']", "DSH_AHA_AGENT_API_URL", "DSH_AHA_AGENT_API_TOKEN",
		"process.env.AHA2_AGENT_API_URL", "process.env.AHA2_AGENT_API_TOKEN",
	} {
		if !strings.Contains(plugin, expected) {
			t.Fatalf("Agent API environment plugin missing %q:\n%s", expected, plugin)
		}
	}
}

func TestDSHPluginSpecifier(t *testing.T) {
	t.Parallel()
	if got := dshPluginSpecifier("/home/user/DSH Home/plugin.mjs"); got != "/home/user/DSH Home/plugin.mjs" {
		t.Fatalf("Linux DSH plugin path = %q", got)
	}
	if got := dshPluginSpecifier(`C:\Users\owner\plugin.mjs`); got != "C:/Users/owner/plugin.mjs" {
		t.Fatalf("Windows DSH plugin path = %q", got)
	}
}

func TestDSHProviderRuntimeMapsAHARoutes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		wire       string
		env        map[string]string
		wantAPI    string
		wantBase   string
		wantKeyEnv string
	}{
		{
			name: "responses",
			wire: "responses",
			env: map[string]string{
				"OPENAI_BASE_URL": "https://responses.example/v1",
				"OPENAI_API_KEY":  "configured",
			},
			wantAPI: "openai-responses", wantBase: "https://responses.example/v1", wantKeyEnv: "OPENAI_API_KEY",
		},
		{
			name: "chat",
			wire: "chat",
			env: map[string]string{
				"OPENAI_BASE_URL": "https://chat.example/v1",
				"OPENAI_API_KEY":  "configured",
			},
			wantAPI: "openai-completions", wantBase: "https://chat.example/v1", wantKeyEnv: "OPENAI_API_KEY",
		},
		{
			name: "anthropic",
			wire: "anthropic_messages",
			env: map[string]string{
				"ANTHROPIC_BASE_URL":   "https://messages.example",
				"ANTHROPIC_AUTH_TOKEN": "configured",
			},
			wantAPI: "anthropic-messages", wantBase: "https://messages.example", wantKeyEnv: "ANTHROPIC_AUTH_TOKEN",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			api, baseURL, keyEnv, err := dshProviderRuntime(Request{WireAPI: test.wire, Environment: test.env})
			if err != nil {
				t.Fatal(err)
			}
			if api != test.wantAPI || baseURL != test.wantBase || keyEnv != test.wantKeyEnv {
				t.Fatalf("runtime = %q %q %q, want %q %q %q", api, baseURL, keyEnv, test.wantAPI, test.wantBase, test.wantKeyEnv)
			}
		})
	}
}

func TestParseDSHLineEvents(t *testing.T) {
	t.Parallel()
	tests := []struct {
		line        string
		wantType    string
		wantReply   string
		wantSession string
	}{
		{`{"type":"session","sessionId":"session-1"}`, "agent_session", "", "session-1"},
		{`{"type":"text","text":"working"}`, "agent_message", "", ""},
		{`{"type":"thinking","text":"planning the next step"}`, "agent_reasoning", "", ""},
		{`{"type":"tool_call","callId":"c1","tool":"bash","input":{"command":"go test ./..."}}`, "agent_command_started", "", ""},
		{`{"type":"tool_result","callId":"c1","status":"completed","result":"ok"}`, "agent_command_finished", "", ""},
		{`{"type":"status","phase":"step_end","usage":{"inputTokens":12,"outputTokens":3}}`, "agent_usage", "", ""},
		{`{"type":"final","sessionId":"session-1","text":"done","reason":{"kind":"completed"}}`, "agent_message", "done", "session-1"},
		{`{"type":"final","text":"done without reason"}`, "agent_message", "done without reason", ""},
		{`{"type":"error","message":"failed"}`, "agent_error", "", ""},
	}
	for _, test := range tests {
		event, reply, session := parseDSHLine(test.line)
		if event.Type != test.wantType || reply != test.wantReply || session != test.wantSession {
			t.Fatalf("parse %s = event=%#v reply=%q session=%q", test.line, event, reply, session)
		}
	}
}

func TestDSHStreamBuffersCommentaryAndDropsFinalDuplicate(t *testing.T) {
	t.Parallel()
	stream := &dshStream{}
	var events []Event
	for _, line := range []string{
		`{"type":"session","sessionId":"session-1"}`,
		`{"type":"thinking","text":"inspect the repository"}`,
		`{"type":"text","text":"I will inspect the repository."}`,
		`{"type":"text","text":"Then I will run the tests."}`,
		`{"type":"tool_call","callId":"c1","tool":"bash","input":{"command":"go test ./..."}}`,
		`{"type":"tool_result","callId":"c1","status":"completed","result":"ok"}`,
		`{"type":"text","text":"final answer"}`,
		`{"type":"status","phase":"turn_end"}`,
		`{"type":"final","text":"final answer"}`,
	} {
		events = append(events, stream.Consume(line)...)
	}
	if got := stream.Reply(); got != "final answer" {
		t.Fatalf("reply = %q", got)
	}
	if got := stream.SessionID(); got != "session-1" {
		t.Fatalf("session = %q", got)
	}
	var types, commentary []string
	for _, event := range events {
		types = append(types, event.Type)
		if text, ok := event.Data["text"].(string); ok && event.Data["intermediate"] == true {
			commentary = append(commentary, text)
		}
	}
	wantTypes := []string{
		"agent_session", "agent_reasoning", "agent_message", "agent_command_started",
		"agent_command_finished", "agent_message",
	}
	if strings.Join(types, ",") != strings.Join(wantTypes, ",") {
		t.Fatalf("events = %v, want %v", types, wantTypes)
	}
	if len(commentary) != 1 || commentary[0] != "I will inspect the repository.\n\nThen I will run the tests." {
		t.Fatalf("commentary = %#v", commentary)
	}
	if remaining := stream.Drain(); remaining != "" {
		t.Fatalf("pending commentary after final = %q", remaining)
	}
}

func TestDSHStreamKeepsCommentaryBeforeDifferentFinalText(t *testing.T) {
	t.Parallel()
	stream := &dshStream{}
	var events []Event
	for _, line := range []string{
		`{"type":"text","text":"interim status"}`,
		`{"type":"final","text":"the complete answer"}`,
	} {
		events = append(events, stream.Consume(line)...)
	}
	if len(events) != 2 || events[0].Type != "agent_message" || events[0].Data["text"] != "interim status" {
		t.Fatalf("events = %#v", events)
	}
	if events[1].Data["final"] != true || events[1].Data["text"] != "the complete answer" {
		t.Fatalf("final event = %#v", events[1])
	}
}

func TestDSHStreamDrainsPendingTextWithoutFinal(t *testing.T) {
	t.Parallel()
	stream := &dshStream{}
	stream.Consume(`{"type":"text","text":"partial commentary"}`)
	if got := stream.Drain(); got != "partial commentary" {
		t.Fatalf("drained commentary = %q", got)
	}
	if got := stream.Drain(); got != "" {
		t.Fatalf("second drain = %q", got)
	}
}

func TestDSHReasoningTextIsBounded(t *testing.T) {
	t.Parallel()
	text := dshReasoningText(strings.Repeat("思", dshReasoningUpdateLimit+1))
	runes := []rune(text)
	if len(runes) != dshReasoningUpdateLimit+1 || runes[len(runes)-1] != '…' {
		t.Fatalf("reasoning text was not bounded: %d runes", len(runes))
	}
	if got := dshReasoningText("  "); got != "" {
		t.Fatalf("blank reasoning = %q", got)
	}
}

func TestDSHUsageIsNormalizedAndAccumulated(t *testing.T) {
	t.Parallel()
	first := normalizeDSHUsage(map[string]any{
		"inputTokens": float64(10), "outputTokens": float64(2), "cacheReadTokens": float64(3),
	})
	second := normalizeDSHUsage(map[string]any{
		"inputTokens": float64(4), "outputTokens": float64(1), "cacheReadTokens": float64(2),
	})
	total := addDSHUsage(addDSHUsage(nil, first), second)
	if total["input_tokens"] != float64(14) || total["output_tokens"] != float64(3) ||
		total["cached_input_tokens"] != float64(5) {
		t.Fatalf("usage total = %#v", total)
	}
}

func TestDSHPermissionMode(t *testing.T) {
	t.Parallel()
	if got := dshPermissionMode("read-only", "never"); got != "read-only" {
		t.Fatalf("read-only mode = %q", got)
	}
	if got := dshPermissionMode("workspace-write", "never"); got != "workspace-write" {
		t.Fatalf("workspace-write mode = %q", got)
	}
	if got := dshPermissionMode("workspace-write", "auto"); got != "danger-full-access" {
		t.Fatalf("auto approval mode = %q", got)
	}
}

// TestDSHLiveEnvProvider is opt-in because it spends model tokens. It exercises
// the full adapter surface: generated profile patch, isolated DSH_HOME,
// inherited AHA provider environment, session id, and final reply.
func TestDSHLiveEnvProvider(t *testing.T) {
	binary := strings.TrimSpace(os.Getenv("AHA2_TEST_DSH_BINARY"))
	baseURL := strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
	model := strings.TrimSpace(os.Getenv("OPENAI_MODEL"))
	agentAPIURL := strings.TrimSpace(os.Getenv("AHA2_AGENT_API_URL"))
	agentAPIToken := strings.TrimSpace(os.Getenv("AHA2_AGENT_API_TOKEN"))
	if binary == "" || baseURL == "" || model == "" || os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("set AHA2_TEST_DSH_BINARY and an AHA Env Provider to run the live DSH test")
	}
	environment := map[string]string{
		"OPENAI_BASE_URL":      baseURL,
		"OPENAI_MODEL":         model,
		"OPENAI_API_KEY":       os.Getenv("OPENAI_API_KEY"),
		"CODEX_WIRE_API":       os.Getenv("CODEX_WIRE_API"),
		"CODEX_ENV_KEY":        os.Getenv("CODEX_ENV_KEY"),
		"DSH_HOME":             t.TempDir(),
		"AHA2_AGENT_API_URL":   agentAPIURL,
		"AHA2_AGENT_API_TOKEN": agentAPIToken,
	}
	workDir := t.TempDir()
	result, err := (DSH{Binary: binary, Timeout: 2 * time.Minute}).Execute(context.Background(), Request{
		Runner: workspace.LocalRunner{}, WorkDir: workDir,
		Model: model, WireAPI: os.Getenv("CODEX_WIRE_API"),
		ReasoningEffort: "high",
		Environment:     environment, Filesystem: "workspace-write",
		Prompt: "Reply exactly DSH_ADAPTER_LIVE_OK. Do not call any tools.",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Reply != "DSH_ADAPTER_LIVE_OK" || result.ProviderSessionID == "" {
		t.Fatalf("unexpected live DSH result: reply=%q session=%q", result.Reply, result.ProviderSessionID)
	}
	resumeModel := strings.TrimSpace(os.Getenv("AHA2_TEST_DSH_MODEL_B"))
	if resumeModel == "" {
		resumeModel = model
	}
	resumed, err := (DSH{Binary: binary, Timeout: 2 * time.Minute}).Execute(context.Background(), Request{
		Runner: workspace.LocalRunner{}, WorkDir: workDir,
		Model: resumeModel, WireAPI: os.Getenv("CODEX_WIRE_API"),
		ReasoningEffort: "high",
		Environment:     environment, Filesystem: "workspace-write",
		Prompt: "Use the bash tool to call \"$DSH_AHA_AGENT_API_URL/api/v1/agent/capabilities\" with " +
			"\"Authorization: Bearer $DSH_AHA_AGENT_API_TOKEN\" and discard the response. Do not print either value. " +
			"If curl succeeds, reply exactly DSH_AGENT_API_ENV_OK.",
		ProviderSessionID: result.ProviderSessionID,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Reply != "DSH_AGENT_API_ENV_OK" || resumed.ProviderSessionID != result.ProviderSessionID {
		t.Fatalf("unexpected resumed DSH result: reply=%q session=%q want=%q", resumed.Reply, resumed.ProviderSessionID, result.ProviderSessionID)
	}
}
