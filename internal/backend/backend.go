package backend

import (
	"context"

	"github.com/ChinaKai/AHA2/internal/workspace"
)

type Request struct {
	Runner            workspace.Runner
	WorkDir           string
	Model             string
	WireAPI           string
	ContextWindow     int64
	AutoCompactWindow int64
	ReasoningEffort   string
	// SupportsImageInput states whether the model may receive image content.
	// AHA treats an unset capability as enabled, so a backend that can carry
	// images reads the resolved value instead of re-guessing the default.
	SupportsImageInput bool
	// FastMode selects the accelerated service tier where the backend has one.
	FastMode bool
	StreamIdleTimeoutMS int
	StreamMaxRetries    int
	Environment         map[string]string
	Prompt              string
	ProviderSessionID   string
	// ClaudeConfigDir is the exact CLAUDE_CONFIG_DIR chosen for this run. It is
	// used to locate the transcript when the CLI exits before emitting a session
	// id (for example an upstream 403).
	ClaudeConfigDir string
	// ReverseForward asks the transport to carry an AHA-side address into the
	// workspace for the lifetime of this run. Nil when the workspace can already
	// reach AHA by address.
	ReverseForward *workspace.ReverseForward
	Filesystem     string
	Approval       string
	// MCPServers is the structured MCP registration for this Turn. Each backend
	// renders it in its own CLI format; keeping the list neutral is what lets
	// Codex and DSH receive the same tools Claude already received through
	// --mcp-config.
	MCPServers   []MCPServer
	AllowedTools []string
	// LiveInbox lets a backend that can take input while a Turn is running
	// pull Owner and channel messages into it. Nil, or a backend that cannot
	// stream input, leaves those messages to the Agent API and the next Turn.
	LiveInbox LiveInbox
}

// LiveBatch is one claim of live inbox messages, rendered as the text the
// model should read.
type LiveBatch struct {
	IDs  []string
	Text string
}

// LiveInbox claims messages for the running Turn and settles each claim.
// Claim returns an empty batch when nothing is pending. Ack with delivered
// false returns the claim to the inbox so the next Turn still receives it.
type LiveInbox interface {
	Claim(context.Context) (LiveBatch, error)
	Ack(ctx context.Context, batch LiveBatch, delivered bool) error
}

// MCPServer is one MCP registration for a Turn. Name is the model-facing
// server namespace, so the tool name is mcp__<Name>__<tool> on every backend.
// BearerTokenEnv names an environment variable that carries an HTTP bearer
// token; adapters must reference the variable rather than copy its value into
// argv or a generated file.
type MCPServer struct {
	Name      string
	Transport string
	// URL is the concrete endpoint. URLFromEnv/URLPath are set when the
	// reachable address is only known inside the backend process (for example a
	// workspace reached through the Agent API tunnel). Claude and DSH resolve
	// the variable inline; Codex, which cannot, keeps using URL.
	URL            string
	URLFromEnv     string
	URLPath        string
	Command        string
	Args           []string
	Env            map[string]string
	Headers        map[string]string
	BearerTokenEnv string
}

type Event struct {
	Type string
	Data map[string]any
}

type Result struct {
	Reply             string
	ExitCode          int
	ProviderSessionID string
}

type Adapter interface {
	Execute(context.Context, Request, func(Event)) (Result, error)
}
