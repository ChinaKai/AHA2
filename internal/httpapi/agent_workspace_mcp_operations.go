package httpapi

import "net/http"

func (s *Server) workspaceMCPOperations() []agentMCPOperation {
	return []agentMCPOperation{
		{Tool: mcpTool{Name: "workspace_process_start", Description: "Start a managed process in this Task's bound writable Workspace. Main only; cwd stays within the Task root. Env is for nonsecret runtime values; use configured credentials through their existing secure mechanisms.", InputSchema: mcpObject(map[string]any{
			"name": mcpString(), "executable": mcpString(), "args": map[string]any{"type": "array", "items": mcpString(), "maxItems": 128}, "cwd": mcpString(),
			"env": map[string]any{"type": "object", "properties": map[string]any{}, "required": []string{}, "maxProperties": 64, "additionalProperties": map[string]any{"type": "string", "maxLength": 8192}},
		}, "name", "executable")}, Handler: s.startAgentProcess, Method: http.MethodPost, Write: true, ResultFields: []string{"ok"}},
		{Tool: mcpTool{Name: "workspace_process_stop", Description: "Stop a named managed process belonging to this Task. Main only; another Task's process is never addressed.", InputSchema: mcpObject(map[string]any{"name": mcpString()}, "name")}, Handler: s.stopAgentProcess, Method: http.MethodPost, Write: true, PathFields: map[string]string{"name": "name"}, ResultFields: []string{"ok"}},
	}
}

func (s *Server) workspaceMCPToolsFor(write bool) []mcpTool {
	tools := agentWorkspaceMCPTools()
	if write {
		for _, op := range s.workspaceMCPOperations() {
			tools = append(tools, op.Tool)
		}
	}
	return tools
}
