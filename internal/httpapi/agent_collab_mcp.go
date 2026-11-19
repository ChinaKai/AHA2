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

// agentCollabMCPMaxRequestBytes bounds one JSON-RPC request. Collaboration
// payloads are small assignments; batch results never travel back through this
// endpoint as an upload.
const agentCollabMCPMaxRequestBytes int64 = 1 << 20

// agentCollabMCP is the HTTP MCP facade for the collaboration domain. Every
// method — including initialize and tools/list — is mounted behind
// withAgentCapability and revalidates AgentCallContext plus the domain gate, so
// a caller that knows the URL cannot use tool visibility as an authorization
// shortcut. The domain exists only for a Main agent in auto collaboration mode
// and is never injected for a channel-hosted Turn.
func (s *Server) agentCollabMCP(writer http.ResponseWriter, request *http.Request) {
	if agentCoreMCPForwarded(request) {
		writeError(writer, http.StatusForbidden, "agent_collab_mcp_forwarded")
		return
	}
	claims, ok := agentClaimsFromContext(request.Context())
	if !ok {
		writeError(writer, http.StatusUnauthorized, "agent_capability_required")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, agentCollabMCPMaxRequestBytes))
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
	call, err := s.app.AgentCallContext(request.Context(), claims, false)
	if err != nil {
		writeAgentControlError(writer, err)
		return
	}
	if !s.app.AgentCollabMCPAllowed(request.Context(), call) {
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
			"serverInfo":      map[string]any{"name": "aha2-collab", "version": "1.0.0"},
		})
	case "notifications/initialized", "notifications/cancelled", "notifications/progress":
		writer.WriteHeader(http.StatusAccepted)
	case "ping":
		respond(map[string]any{})
	case "tools/list":
		respond(map[string]any{"tools": agentCollabMCPTools()})
	case "tools/call":
		s.callAgentCollabMCPTool(writer, request, claims, call, payload)
	default:
		if strings.HasPrefix(payload.Method, "notifications/") {
			writer.WriteHeader(http.StatusAccepted)
			return
		}
		respondError(-32601, "method not found")
	}
}

func (s *Server) callAgentCollabMCPTool(writer http.ResponseWriter, request *http.Request, claims agentapi.Claims, call app.AgentCallContext, payload mcpRequest) {
	respond := func(result any) {
		writeJSON(writer, http.StatusOK, map[string]any{
			"jsonrpc": "2.0", "id": payload.ID, "result": result,
		})
	}
	var input struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	invalidToolError := func(message string) {
		respond(map[string]any{
			"isError": true,
			"content": []map[string]any{{"type": "text", "text": "invalid_arguments: " + message}},
		})
	}
	respondText := func(text string, isError bool) {
		result := map[string]any{
			"content": []map[string]any{{"type": "text", "text": text}},
		}
		if isError {
			result["isError"] = true
		}
		respond(result)
	}
	respondJSON := func(value any) {
		data, err := json.Marshal(value)
		if err != nil {
			respondText("collaboration_operation_failed: result could not be encoded", true)
			return
		}
		respondText(string(data), false)
	}
	respondJSONError := func(value any) {
		data, err := json.Marshal(value)
		if err != nil {
			respondText("collaboration_operation_failed: result could not be encoded", true)
			return
		}
		respondText(string(data), true)
	}
	toolError := func(err error) {
		// Tool text is model-visible. Store errors and assignment messages can
		// carry Turn identifiers or other Task detail; operators get the error in
		// the server log and the model gets only the stable classification.
		log.Printf("agent collab mcp tool %s failed: %v", input.Name, err)
		var rejected *app.CollaborationAssignmentError
		if errors.As(err, &rejected) {
			// The existing HTTP contract reports a partial batch as a conflict.
			// Expose the accepted batch and per-action rejection metadata, but
			// never the server-side message text.
			respondJSONError(map[string]any{
				"error":           app.CollaborationAssignmentDuplicate,
				"batch_id":        rejected.BatchID,
				"created_agents":  rejected.Created,
				"accepted_agents": rejected.Accepted,
				"rejected_agents": app.CollabMCPRejectionViews(rejected.Rejected),
			})
			return
		}
		code, message := collabMCPErrorCode(err)
		respondText(code+": "+message, true)
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
	if !app.CollabMCPToolIsKnown(input.Name) {
		writeJSON(writer, http.StatusOK, map[string]any{
			"jsonrpc": "2.0", "id": payload.ID,
			"error": map[string]any{"code": -32602, "message": "unknown tool"},
		})
		return
	}
	auditResult := "ok"
	started := time.Now()
	defer func() {
		// Only the tool identity, result classification, duration and argument
		// key names are recorded. Assignment text, integration instructions,
		// batch result bodies and client-supplied identifiers can all carry
		// task data, so none of them reach the audit row.
		s.audit(request, "agent.collab_mcp.call", "task", call.Task.ID, map[string]any{
			"tool": input.Name, "turn_id": call.Turn.ID, "result": auditResult,
			"duration_ms":   time.Since(started).Milliseconds(),
			"argument_keys": coreMCPArgumentKeys(input.Arguments),
		})
	}()

	switch input.Name {
	case "collab_batch_submit":
		var args app.AgentCollabMCPSubmitInput
		if err := decodeCoreToolArguments(input.Arguments, &args); err != nil {
			auditResult = "invalid"
			invalidToolError("collab_batch_submit received an unknown field or malformed action")
			return
		}
		result, err := s.app.AgentCollabMCPSubmit(request.Context(), claims, args)
		if err != nil {
			auditResult = "error"
			fail(err)
			return
		}
		respondJSON(result)

	case "collab_batch_wait":
		var args struct {
			BatchID        string `json:"batch_id"`
			AfterCursor    int64  `json:"after_cursor"`
			TimeoutSeconds int    `json:"timeout_seconds"`
		}
		if err := decodeCoreToolArguments(input.Arguments, &args); err != nil {
			auditResult = "invalid"
			invalidToolError("collab_batch_wait received an unknown field")
			return
		}
		if strings.TrimSpace(args.BatchID) == "" {
			auditResult = "invalid"
			invalidToolError("collab_batch_wait requires a batch_id")
			return
		}
		if args.AfterCursor < 0 || args.TimeoutSeconds < 0 || args.TimeoutSeconds > 30 {
			auditResult = "invalid"
			invalidToolError("after_cursor must be non-negative and timeout_seconds must be 0-30")
			return
		}
		timeout := time.Duration(args.TimeoutSeconds) * time.Second
		progress, err := s.app.AgentCollabMCPWait(request.Context(), claims, args.BatchID, args.AfterCursor, timeout)
		if err != nil {
			auditResult = "error"
			fail(err)
			return
		}
		respondJSON(progress)

	default:
		auditResult = "invalid"
		writeJSON(writer, http.StatusOK, map[string]any{
			"jsonrpc": "2.0", "id": payload.ID,
			"error": map[string]any{"code": -32602, "message": "unknown tool"},
		})
	}
}

// collabMCPErrorCode produces a stable machine classification plus a safe
// message. No assignment text, batch result body or store error is included.
func collabMCPErrorCode(err error) (string, string) {
	switch {
	case errors.Is(err, app.ErrAgentCallForbidden):
		return "agent_operation_forbidden", "agent operation is forbidden"
	case errors.Is(err, app.ErrAgentTurnInactive):
		return "agent_turn_inactive", "agent turn is not active"
	case errors.Is(err, app.ErrAgentCollaborationRequiresAuto):
		return "collaboration_requires_auto_mode", "collaboration requires auto mode"
	case errors.Is(err, app.ErrAgentCollabMCPInvalidInput):
		return "collaboration_input_invalid", "actions must contain 1 to 8 entries with agent_id, title and assignment, and integration_instructions is required"
	case errors.Is(err, app.ErrAgentCollabMCPNothingQueued):
		return "collaboration_no_agents_queued", "no agent assignment was queued"
	case errors.Is(err, app.ErrAgentCollabMCPBatchInvalid):
		return "collaboration_batch_invalid", "batch id is invalid"
	default:
		// A fixed copy here is deliberate: assignment and store errors can carry
		// Turn identifiers or other Task detail, which stay in the server log.
		return "collaboration_operation_failed", "collaboration operation failed"
	}
}

func agentCollabMCPTools() []mcpTool {
	object := func(properties map[string]any, required ...string) map[string]any {
		if required == nil {
			required = []string{}
		}
		return map[string]any{
			"type": "object", "properties": properties,
			"required": required, "additionalProperties": false,
		}
	}
	actionProperties := map[string]any{
		"agent_id":   map[string]any{"type": "string", "description": "Agent id, or empty to let the server pick the next free slot."},
		"title":      map[string]any{"type": "string", "description": "Short display title for the sub-agent."},
		"assignment": map[string]any{"type": "string", "description": "Self-contained task assignment for the sub-agent."},
		"required":   map[string]any{"type": "boolean", "description": "Accepted for compatibility with the batches endpoint; the server normalizes every action to required."},
	}
	return []mcpTool{
		{
			Name: "collab_batch_submit",
			Description: "Submit one collaboration batch to this Task's sub-agents. Main only, auto " +
				"collaboration mode only. The existing server-side agent cap, duplicate and permission " +
				"checks apply; this tool cannot raise them. Available only when auto mode is enabled.",
			InputSchema: object(map[string]any{
				"actions": map[string]any{
					"type": "array", "minItems": 1, "maxItems": 8,
					"items": object(actionProperties, "agent_id", "title", "assignment"),
				},
				"integration_instructions": map[string]any{
					"type": "string", "description": "How Main should integrate the sub-agent results.",
				},
			}, "actions", "integration_instructions"),
		},
		{
			Name: "collab_batch_wait",
			Description: "Poll one collaboration batch owned by this Task and Round. Main only. The cursor " +
				"is monotonic; a timeout returns complete=false instead of an error. Another Task's or " +
				"unknown batch id is rejected without revealing whether it exists.",
			InputSchema: object(map[string]any{
				"batch_id": map[string]any{
					"type": "string", "minLength": 1,
					"description": "Batch id returned by collab_batch_submit.",
				},
				"after_cursor": map[string]any{
					"type": "integer", "minimum": 0, "default": 0,
					"description": "Only return events after this cursor.",
				},
				"timeout_seconds": map[string]any{
					"type": "integer", "minimum": 0, "maximum": 30, "default": 20,
					"description": "Long-poll window. A timeout is a normal result.",
				},
			}, "batch_id"),
		},
	}
}
