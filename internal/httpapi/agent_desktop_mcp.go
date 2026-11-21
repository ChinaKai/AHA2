package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

func (s *Server) desktopMCPOperations() []agentMCPOperation {
	base := func(extra map[string]any, required ...string) map[string]any {
		fields := map[string]any{"session_id": mcpString()}
		for key, value := range extra {
			fields[key] = value
		}
		return mcpObject(fields, append([]string{"session_id"}, required...)...)
	}
	return []agentMCPOperation{
		{Tool: mcpTool{Name: "desktop_status", Description: "Read this Task's shared-desktop status and current authorized target. Does not enumerate other windows or grant control.", InputSchema: mcpObject(map[string]any{})}, Handler: s.desktopStatus, Method: http.MethodGet},
		{Tool: mcpTool{Name: "desktop_observe", Description: "Observe the existing shared target once. Returns the observation id and revision required by a subsequent action. Existing sharing and foreground/background rules apply.", InputSchema: base(map[string]any{"view_id": mcpString(), "max_width": mcpInteger(1, 8192), "max_height": mcpInteger(1, 8192), "quality": mcpInteger(1, 100)})}, Handler: s.desktopObserve, Method: http.MethodGet, QueryFields: []string{"session_id", "view_id", "max_width", "max_height", "quality"}},
		{Tool: mcpTool{Name: "desktop_control", Description: "Register this Turn as the agent controller of an already shared session. Cannot select a new target, create a desktop, grant permission or claim owner identity.", InputSchema: base(map[string]any{"revision": mcpInteger(1, 9007199254740991), "controller": mcpEnum("agent")}, "revision", "controller")}, Handler: s.desktopControl, Method: http.MethodPost, Write: true},
		{Tool: mcpTool{Name: "desktop_action", Description: "Perform one action on the already shared target using an existing observation and revision. Replays, stale observations and revoked authorization follow the original desktop service rules.", InputSchema: base(map[string]any{
			"revision": mcpInteger(1, 9007199254740991), "observation_id": mcpString(), "element_id": mcpString(), "view_id": mcpString(), "action_id": mcpString(),
			"kind": mcpEnum("click", "double_click", "move", "drag", "scroll", "text", "key", "focus"), "value": map[string]any{"type": "string", "maxLength": 4096}, "x": map[string]any{"type": "number"}, "y": map[string]any{"type": "number"}, "end_x": map[string]any{"type": "number"}, "end_y": map[string]any{"type": "number"}, "delta_x": map[string]any{"type": "number"}, "delta_y": map[string]any{"type": "number"}, "button": mcpEnum("left", "right", "middle"), "keys": map[string]any{"type": "array", "items": mcpString(), "maxItems": 16},
		}, "revision", "observation_id", "element_id", "kind")}, Handler: s.desktopAct, Method: http.MethodPost, Write: true, ResultFields: []string{"ok", "status"}},
		{Tool: mcpTool{Name: "desktop_stop", Description: "Stop this Task's shared desktop session. Cannot stop another Task's session or select a replacement target.", InputSchema: base(nil)}, Handler: s.agentDesktopStop, Method: http.MethodPost, Write: true},
	}
}

func (s *Server) agentDesktopStop(writer http.ResponseWriter, request *http.Request) {
	claims, ok := agentClaimsFromContext(request.Context())
	if !ok {
		writeError(writer, http.StatusUnauthorized, "agent_capability_required")
		return
	}
	call, err := s.app.AgentCallContext(request.Context(), claims, true)
	if err != nil {
		writeAgentControlError(writer, err)
		return
	}
	if !s.app.AgentDesktopMCPWriteAllowed(request.Context(), call) {
		writeError(writer, http.StatusForbidden, "agent_operation_forbidden")
		return
	}
	request.SetPathValue("id", claims.TaskID)
	s.desktopStop(writer, request)
}

func (s *Server) agentDesktopMCP(writer http.ResponseWriter, request *http.Request) {
	if agentCoreMCPForwarded(request) {
		writeError(writer, http.StatusForbidden, "agent_desktop_mcp_forwarded")
		return
	}
	claims, ok := agentClaimsFromContext(request.Context())
	if !ok {
		writeError(writer, http.StatusUnauthorized, "agent_capability_required")
		return
	}
	call, err := s.app.AgentCallContext(request.Context(), claims, false)
	if err != nil {
		writeAgentControlError(writer, err)
		return
	}
	if !s.app.AgentDesktopMCPAllowed(request.Context(), call) {
		writeError(writer, http.StatusForbidden, "agent_operation_forbidden")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, 1<<20))
	if err != nil {
		writeError(writer, http.StatusRequestEntityTooLarge, "request_too_large")
		return
	}
	var payload mcpRequest
	if trimmed := bytes.TrimSpace(body); len(trimmed) == 0 || trimmed[0] != '{' || json.Unmarshal(body, &payload) != nil || len(payload.ID) > 256 {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	respond := func(value any) {
		writeJSON(writer, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": payload.ID, "result": value})
	}
	fail := func(message string) {
		respond(map[string]any{"isError": true, "content": []map[string]any{{"type": "text", "text": message}}})
	}
	write := s.app.AgentDesktopMCPWriteAllowed(request.Context(), call)
	switch payload.Method {
	case "initialize":
		respond(map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "aha2-desktop", "version": "1.0.0"}})
	case "ping":
		respond(map[string]any{})
	case "tools/list":
		items := []mcpTool{}
		for _, op := range s.desktopMCPOperations() {
			if !op.Write || write {
				items = append(items, op.Tool)
			}
		}
		respond(map[string]any{"tools": items})
	case "tools/call":
		var input struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if json.Unmarshal(payload.Params, &input) != nil {
			fail("invalid tool call")
			return
		}
		if len(input.Arguments) == 0 {
			input.Arguments = json.RawMessage(`{}`)
		}
		for _, op := range s.desktopMCPOperations() {
			if op.Tool.Name != input.Name {
				continue
			}
			if op.Write && !write {
				writeError(writer, http.StatusForbidden, "agent_operation_forbidden")
				return
			}
			result, callErr := runAgentMCPOperation(request, op, input.Arguments)
			s.audit(request, "agent.desktop_mcp.call", "task", call.Task.ID, map[string]any{"tool": input.Name, "turn_id": call.Turn.ID, "argument_keys": coreMCPArgumentKeys(input.Arguments), "success": callErr == nil})
			if callErr != nil {
				fail("desktop operation failed; check sharing, observation and permissions")
				return
			}
			if observation, ok := result["observation"].(map[string]any); ok {
				delete(observation, "capture_error")
				if elements, ok := observation["elements"].([]any); ok {
					for _, raw := range elements {
						if element, ok := raw.(map[string]any); ok {
							delete(element, "value")
						}
					}
				}
			}
			data, encodeErr := json.Marshal(result)
			if encodeErr != nil {
				fail("operation result unavailable")
				return
			}
			respond(map[string]any{"content": []map[string]any{{"type": "text", "text": string(data)}}})
			return
		}
		fail("unknown tool")
	default:
		if strings.HasPrefix(payload.Method, "notifications/") {
			writer.WriteHeader(http.StatusAccepted)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": payload.ID, "error": map[string]any{"code": -32601, "message": "method not found"}})
	}
}
