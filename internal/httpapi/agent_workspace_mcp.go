package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/app"
)

// agentWorkspaceMCPMaxRequestBytes bounds one JSON-RPC request. The first slice
// only carries small identifiers; file and log payloads never pass through this
// endpoint.
const agentWorkspaceMCPMaxRequestBytes int64 = 1 << 20

// agentWorkspaceMCP is the HTTP MCP facade for scoped Workspace operations.
// Every method — including initialize and tools/list — is mounted behind
// withAgentCapability and revalidates AgentCallContext plus the domain gate, so
// a caller that knows the URL still cannot use tool visibility as an
// authorization shortcut. A matching bound Workspace and non-channel Turn are
// required; pet access rechecks live permissions.
func (s *Server) agentWorkspaceMCP(writer http.ResponseWriter, request *http.Request) {
	if agentCoreMCPForwarded(request) {
		writeError(writer, http.StatusForbidden, "agent_workspace_mcp_forwarded")
		return
	}
	claims, ok := agentClaimsFromContext(request.Context())
	if !ok {
		writeError(writer, http.StatusUnauthorized, "agent_capability_required")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, agentWorkspaceMCPMaxRequestBytes))
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			writeError(writer, http.StatusRequestEntityTooLarge, "request_too_large")
			return
		}
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	trimmed := bytes.TrimLeft(body, " \t\r\n")
	if len(trimmed) == 0 || trimmed[0] != '{' {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	var payload mcpRequest
	if json.Unmarshal(body, &payload) != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	call, err := s.app.AgentDomainCallContext(request.Context(), claims, "workspace", false)
	if err != nil {
		writeAgentControlError(writer, err)
		return
	}
	if !s.app.AgentWorkspaceMCPAllowed(request.Context(), call) {
		writeError(writer, http.StatusForbidden, "agent_operation_forbidden")
		return
	}
	respond := func(result any) {
		writeJSON(writer, http.StatusOK, map[string]any{
			"jsonrpc": "2.0", "id": payload.ID, "result": result,
		})
	}
	respondError := func(code int, message string) {
		writeJSON(writer, http.StatusOK, map[string]any{
			"jsonrpc": "2.0", "id": payload.ID,
			"error": map[string]any{"code": code, "message": message},
		})
	}
	switch payload.Method {
	case "initialize":
		respond(map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "aha2-workspace", "version": "1.0.0"},
		})
	case "notifications/initialized", "notifications/cancelled", "notifications/progress":
		writer.WriteHeader(http.StatusAccepted)
	case "ping":
		respond(map[string]any{})
	case "tools/list":
		respond(map[string]any{"tools": s.workspaceMCPToolsFor(s.app.AgentWorkspaceMCPWriteAllowed(request.Context(), call))})
	case "tools/call":
		s.callAgentWorkspaceMCPTool(writer, request, claims, call, payload)
	default:
		if strings.HasPrefix(payload.Method, "notifications/") {
			writer.WriteHeader(http.StatusAccepted)
			return
		}
		respondError(-32601, "method not found")
	}
}

func (s *Server) callAgentWorkspaceMCPTool(writer http.ResponseWriter, request *http.Request, claims agentapi.Claims, call app.AgentCallContext, payload mcpRequest) {
	respond := func(result any) {
		writeJSON(writer, http.StatusOK, map[string]any{
			"jsonrpc": "2.0", "id": payload.ID, "result": result,
		})
	}
	var input struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	toolError := func(err error) {
		// Tool text is model-visible. Store and process errors can carry host
		// paths, command lines or ssh diagnostics; operators get the detail in
		// the server log and the model gets only the stable classification.
		log.Printf("agent workspace mcp tool %s failed: %v", input.Name, err)
		code, message := workspaceMCPErrorCode(err)
		respond(map[string]any{
			"isError": true,
			"content": []map[string]any{{"type": "text", "text": code + ": " + message}},
		})
	}
	invalidToolError := func(message string) {
		respond(map[string]any{
			"isError": true,
			"content": []map[string]any{{"type": "text", "text": "invalid_arguments: " + message}},
		})
	}
	respondJSON := func(value any) {
		data, err := json.Marshal(value)
		if err != nil {
			toolError(errors.New("result could not be encoded"))
			return
		}
		respond(map[string]any{
			"content": []map[string]any{{"type": "text", "text": string(data)}},
		})
	}
	fail := func(err error) {
		switch {
		case errors.Is(err, app.ErrAgentCallForbidden):
			writeError(writer, http.StatusForbidden, "agent_operation_forbidden")
		case errors.Is(err, app.ErrAgentTurnInactive):
			writeError(writer, http.StatusConflict, "agent_turn_inactive")
		default:
			toolError(err)
		}
	}

	if err := json.Unmarshal(payload.Params, &input); err != nil || strings.TrimSpace(input.Name) == "" {
		writeJSON(writer, http.StatusOK, map[string]any{
			"jsonrpc": "2.0", "id": payload.ID,
			"error": map[string]any{"code": -32602, "message": "invalid tool call"},
		})
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if len(input.Arguments) == 0 {
		input.Arguments = json.RawMessage(`{}`)
	}
	if !app.WorkspaceMCPToolIsKnown(input.Name) {
		writeJSON(writer, http.StatusOK, map[string]any{
			"jsonrpc": "2.0", "id": payload.ID,
			"error": map[string]any{"code": -32602, "message": "unknown tool"},
		})
		return
	}
	auditResult := "ok"
	started := time.Now()
	defer func() {
		s.audit(request, "agent.workspace_mcp.call", "task", call.Task.ID, map[string]any{
			"tool": input.Name, "turn_id": call.Turn.ID, "result": auditResult,
			"duration_ms":   time.Since(started).Milliseconds(),
			"workspace_id":  call.Task.WorkspaceID,
			"argument_keys": coreMCPArgumentKeys(input.Arguments),
		})
	}()

	for _, operation := range s.workspaceMCPOperations() {
		if operation.Tool.Name != input.Name {
			continue
		}
		if !s.app.AgentWorkspaceMCPWriteAllowed(request.Context(), call) {
			auditResult = "denied"
			writeError(writer, http.StatusForbidden, "agent_operation_forbidden")
			return
		}
		result, err := runAgentMCPOperation(request, operation, input.Arguments)
		if err != nil {
			auditResult = "error"
			toolError(err)
			return
		}
		respondJSON(result)
		return
	}

	switch input.Name {
	case "workspace_list":
		if err := decodeCoreToolArguments(input.Arguments, &struct{}{}); err != nil {
			auditResult = "invalid"
			invalidToolError("workspace_list takes no arguments")
			return
		}
		result, err := s.app.AgentWorkspaceMCPList(request.Context(), claims)
		if err != nil {
			auditResult = "error"
			fail(err)
			return
		}
		respondJSON(result)

	case "workspace_process_list":
		if err := decodeCoreToolArguments(input.Arguments, &struct{}{}); err != nil {
			auditResult = "invalid"
			invalidToolError("workspace_process_list takes no arguments")
			return
		}
		items, err := s.app.AgentWorkspaceMCPProcessList(request.Context(), claims)
		if err != nil {
			auditResult = "error"
			fail(err)
			return
		}
		respondJSON(map[string]any{"processes": items})

	case "workspace_process_get":
		var args struct {
			Name string `json:"name"`
		}
		// Only the JSON shape is checked here. The name grammar (including an
		// empty name) is validated in the app layer, so every illegal name gets
		// the same classification instead of splitting on a length threshold.
		if err := decodeCoreToolArguments(input.Arguments, &args); err != nil {
			auditResult = "invalid"
			invalidToolError("workspace_process_get requires a process name")
			return
		}
		item, err := s.app.AgentWorkspaceMCPProcessGet(request.Context(), claims, args.Name)
		if err != nil {
			auditResult = "error"
			fail(err)
			return
		}
		respondJSON(map[string]any{"process": item})

	case "workspace_runtime_list":
		if err := decodeCoreToolArguments(input.Arguments, &struct{}{}); err != nil {
			auditResult = "invalid"
			invalidToolError("workspace_runtime_list takes no arguments")
			return
		}
		items, err := s.app.AgentWorkspaceMCPRuntimeList(request.Context(), claims)
		if err != nil {
			auditResult = "error"
			fail(err)
			return
		}
		respondJSON(map[string]any{"runtimes": items})

	default:
		auditResult = "invalid"
		writeJSON(writer, http.StatusOK, map[string]any{
			"jsonrpc": "2.0", "id": payload.ID,
			"error": map[string]any{"code": -32602, "message": "unknown tool"},
		})
	}
}

// workspaceMCPErrorCode produces a stable machine classification plus a safe
// message. It never includes a command line, a path, an ssh target or a store
// error; those stay in the server log.
func workspaceMCPErrorCode(err error) (string, string) {
	switch {
	case errors.Is(err, app.ErrAgentCallForbidden):
		return "agent_operation_forbidden", "agent operation is forbidden"
	case errors.Is(err, app.ErrAgentTurnInactive):
		return "agent_turn_inactive", "agent turn is not active"
	case errors.Is(err, app.ErrWorkspaceMCPProcessNameInvalid):
		return "workspace_process_name_invalid", "process name is invalid"
	case errors.Is(err, app.ErrWorkspaceMCPProcessNotFound):
		return "workspace_process_not_found", "managed process is not in this Task"
	case errors.Is(err, app.ErrWorkspaceMCPUnavailable):
		return "workspace_unavailable", "workspace is unavailable"
	default:
		// A fixed copy here is deliberate: workspace and process reads can wrap
		// host paths or ssh diagnostics, which must only reach the server log.
		return "workspace_operation_failed", "workspace operation failed"
	}
}

func agentWorkspaceMCPTools() []mcpTool {
	object := func(properties map[string]any, required ...string) map[string]any {
		if required == nil {
			required = []string{}
		}
		return map[string]any{
			"type": "object", "properties": properties,
			"required": required, "additionalProperties": false,
		}
	}
	empty := func() map[string]any { return object(map[string]any{}) }
	return []mcpTool{
		{
			Name: "workspace_list",
			Description: "Read non-sensitive metadata for the Workspace bound to this Task. Returns only the " +
				"bound Workspace; root paths, ssh credentials, capabilities and internal addresses are never " +
				"returned. Available to every Agent.",
			InputSchema: empty(),
		},
		{
			Name: "workspace_process_list",
			Description: "List status summaries of managed processes started by this Task. Command line, " +
				"environment values, working directory and output are never returned. Available to every Agent.",
			InputSchema: empty(),
		},
		{
			Name: "workspace_process_get",
			Description: "Read one managed process status summary by name. The name must match the managed " +
				"process grammar; a process owned by another Task is reported as not found. Available to every Agent.",
			InputSchema: object(map[string]any{
				"name": map[string]any{
					"type": "string", "minLength": 1, "maxLength": 64,
					"pattern":     "^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$",
					"description": "Managed process name from workspace_process_list.",
				},
			}, "name"),
		},
		{
			Name: "workspace_runtime_list",
			Description: "List runtime option field names and non-secret values available to the current " +
				"Project. Tokens, keys, environment values and internal addresses are never returned. " +
				"Available to every Agent.",
			InputSchema: empty(),
		},
	}
}
