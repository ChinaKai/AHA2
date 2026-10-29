package domain

import "time"

// MCPServer is an external MCP capability provider. Builtin servers are
// system-managed and cannot be removed by the owner.
type MCPServer struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Description   string            `json:"description"`
	Transport     string            `json:"transport"`
	URL           string            `json:"url,omitempty"`
	Command       string            `json:"command,omitempty"`
	Args          []string          `json:"args,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
	Tools         []MCPTool         `json:"tools,omitempty"`
	DisabledTools []string          `json:"disabled_tools,omitempty"`
	Enabled       bool              `json:"enabled"`
	Builtin       bool              `json:"builtin"`
	Health        string            `json:"health"`
	LastError     string            `json:"last_error,omitempty"`
	LastCheckedAt time.Time         `json:"last_checked_at,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

type MCPTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
}
