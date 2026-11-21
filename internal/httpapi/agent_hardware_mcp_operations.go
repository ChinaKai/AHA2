package httpapi

import "net/http"

func (s *Server) hardwareMCPOperations() []agentMCPOperation {
	base := func(extra map[string]any) map[string]any {
		fields := map[string]any{"hardware_id": mcpString(), "transport": mcpEnum("serial", "network")}
		for key, value := range extra {
			fields[key] = value
		}
		return fields
	}
	operation := func(name, description string, handler http.HandlerFunc, fields map[string]any, required ...string) agentMCPOperation {
		return agentMCPOperation{
			Tool:    mcpTool{Name: name, Description: description, InputSchema: mcpObject(base(fields), append([]string{"hardware_id"}, required...)...)},
			Handler: handler, Method: http.MethodPost, Write: true,
			PathFields: map[string]string{"hardware_id": "hardware"}, QueryFields: []string{"transport"}, ResultFields: []string{"ok"},
		}
	}
	connect := operation("hardware_connect", "Connect a device already bound to this Task using stored credentials. Requires Main and writable resource access. Unknown SSH host keys require owner confirmation in Web. Read connection state with hardware_list.", s.agentConnectHardware, nil)
	disconnect := operation("hardware_disconnect", "Disconnect one bound device transport. Does not change bindings or credentials.", s.agentDisconnectHardware, nil)
	send := operation("hardware_send", "Send data to a bound writable connected device. No credentials are accepted; use hardware_login for stored-credential login. Read output with hardware_terminal_read.", s.agentSendHardware, map[string]any{"data": map[string]any{"type": "string", "minLength": 1, "maxLength": 65536}, "encoding": mcpEnum("text", "base64")}, "data")
	login := operation("hardware_login", "Start stored-credential login on a bound writable device. Credential values are never accepted or returned. Read connection state and terminal output separately.", s.agentLoginHardware, map[string]any{
		"wakeup": map[string]any{"type": "boolean"}, "line_ending": mcpEnum("lf", "crlf", "cr"),
		"timeout_seconds": mcpInteger(1, 120), "retries": mcpInteger(0, 5),
	})
	adb := operation("hardware_adb_exec", "Run a device-scoped ADB command against a bound writable ADB resource. Host commands, pairing, host-key trust and credentials are excluded. Uses the same command validation, timeout and cancellation as the Agent API.", s.agentADBExec, map[string]any{
		"args":         map[string]any{"type": "array", "items": mcpString(), "minItems": 1, "maxItems": 64},
		"stdin_base64": map[string]any{"type": "string", "maxLength": 65536}, "timeout_seconds": mcpInteger(1, 120),
	}, "args")
	adb.ResultFields = []string{"ok", "exit_code", "stdout_base64", "stderr_base64", "duration_ms"}
	return []agentMCPOperation{connect, disconnect, send, login, adb}
}

func (s *Server) hardwareMCPToolsFor(write bool) []mcpTool {
	tools := agentHardwareMCPTools()
	if write {
		for _, op := range s.hardwareMCPOperations() {
			tools = append(tools, op.Tool)
		}
	}
	return tools
}
