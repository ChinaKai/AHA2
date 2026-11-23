package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/app"
)

// agentCoreMCPMaxRequestBytes bounds one JSON-RPC request. The core MCP only
// carries Task metadata and short messages; attachments use the upload API.
const agentCoreMCPMaxRequestBytes int64 = 1 << 20

// agentCoreMCP is the HTTP MCP facade for the Task Agent API. Every method —
// including initialize and tools/list — is mounted behind withAgentCapability
// and revalidates AgentCallContext here, so a stale token, a finished Turn or a
// mismatched Task/Turn pair cannot use tool visibility as an authorization
// shortcut. Request body and forwarded headers are rejected before decoding.
func (s *Server) agentCoreMCP(writer http.ResponseWriter, request *http.Request) {
	if agentCoreMCPForwarded(request) {
		writeError(writer, http.StatusForbidden, "agent_core_mcp_forwarded")
		return
	}
	claims, ok := agentClaimsFromContext(request.Context())
	if !ok {
		writeError(writer, http.StatusUnauthorized, "agent_capability_required")
		return
	}
	// Read the whole request before dispatch. Decoding straight from the body
	// stops after the first JSON value, which would leave trailing whitespace
	// or a second value beyond the 1 MiB budget unexamined.
	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, agentCoreMCPMaxRequestBytes))
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			writeError(writer, http.StatusRequestEntityTooLarge, "request_too_large")
			return
		}
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	// A JSON-RPC request is one object. Reject an empty body, null, arrays and
	// primitives before decoding; json.Unmarshal accepts exactly one value plus
	// surrounding whitespace, so a second value or trailing garbage also fails.
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
			"serverInfo":      map[string]any{"name": "aha2-core", "version": "1.0.0"},
		})
	case "notifications/initialized", "notifications/cancelled", "notifications/progress":
		// JSON-RPC notifications have no response body. The capability and Turn
		// were already validated above.
		writer.WriteHeader(http.StatusAccepted)
	case "ping":
		respond(map[string]any{})
	case "tools/list":
		respond(map[string]any{"tools": agentCoreMCPToolsNamed(s.app.AgentCoreMCPToolNames(request.Context(), call))})
	case "tools/call":
		s.callAgentCoreMCPTool(writer, request, claims, call, payload)
	default:
		if strings.HasPrefix(payload.Method, "notifications/") {
			writer.WriteHeader(http.StatusAccepted)
			return
		}
		respondError(-32601, "method not found")
	}
}

func agentCoreMCPForwarded(request *http.Request) bool {
	for _, header := range []string{
		"Forwarded", "X-Forwarded-For", "X-Real-IP", "X-Forwarded-Host", "X-Forwarded-Proto",
	} {
		if strings.TrimSpace(request.Header.Get(header)) != "" {
			return true
		}
	}
	return false
}

func (s *Server) callAgentCoreMCPTool(writer http.ResponseWriter, request *http.Request, claims agentapi.Claims, call app.AgentCallContext, payload mcpRequest) {
	respond := func(result any) {
		writeJSON(writer, http.StatusOK, map[string]any{
			"jsonrpc": "2.0", "id": payload.ID, "result": result,
		})
	}
	toolError := func(message string) {
		respond(map[string]any{
			"isError": true,
			"content": []map[string]any{{"type": "text", "text": message}},
		})
	}
	respondJSON := func(value any) {
		data, err := json.Marshal(value)
		if err != nil {
			toolError("result could not be encoded")
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
		case errors.Is(err, app.ErrAgentReadOnly):
			writeError(writer, http.StatusForbidden, app.ErrCodeTaskReadOnly)
		default:
			toolError(safeCoreToolMessage(err))
		}
	}

	var input struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
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
	known := false
	for _, name := range app.CoreMCPToolNames(true) {
		if name == input.Name {
			known = true
			break
		}
	}
	if !known {
		writeJSON(writer, http.StatusOK, map[string]any{
			"jsonrpc": "2.0", "id": payload.ID,
			"error": map[string]any{"code": -32602, "message": "unknown tool"},
		})
		return
	}
	auditResult := "ok"
	started := time.Now()
	defer func() {
		if input.Name == "attachment_materialize" {
			return
		}
		s.audit(request, "agent.core_mcp.call", "task", call.Task.ID, map[string]any{
			"tool": input.Name, "turn_id": call.Turn.ID, "result": auditResult,
			"duration_ms":   time.Since(started).Milliseconds(),
			"argument_keys": coreMCPArgumentKeys(input.Arguments),
		})
	}()
	if app.CoreMCPToolIsWrite(input.Name) {
		if call.Turn.AgentID != "main" {
			auditResult = "denied"
			writeError(writer, http.StatusForbidden, "agent_operation_forbidden")
			return
		}
		if !coreMCPNameIncluded(s.app.AgentCoreMCPToolNames(request.Context(), call), input.Name) {
			auditResult = "denied"
			writeError(writer, http.StatusForbidden, app.ErrCodeTaskReadOnly)
			return
		}
	}

	switch input.Name {
	case "inbox_wait":
		var args struct {
			AfterCursor    int64 `json:"after_cursor"`
			TimeoutSeconds int   `json:"timeout_seconds"`
		}
		if err := decodeCoreToolArguments(input.Arguments, &args); err != nil ||
			args.AfterCursor < 0 || args.TimeoutSeconds < 0 || args.TimeoutSeconds > 30 {
			auditResult = "invalid"
			toolError("invalid inbox_wait arguments")
			return
		}
		messages, next, err := s.app.WaitAgentInbox(
			request.Context(), claims, args.AfterCursor, time.Duration(args.TimeoutSeconds)*time.Second,
		)
		if err != nil {
			auditResult = "error"
			fail(err)
			return
		}
		respondJSON(map[string]any{"messages": messages, "next_cursor": next})

	case "task_get":
		var args struct {
			TaskID string `json:"task_id"`
		}
		if err := decodeCoreToolArguments(input.Arguments, &args); err != nil {
			auditResult = "invalid"
			toolError("invalid task_get arguments")
			return
		}
		view, err := s.app.AgentCoreTaskGet(request.Context(), claims, args.TaskID)
		if err != nil {
			auditResult = "error"
			fail(err)
			return
		}
		respondJSON(view)

	case "task_list":
		var args struct {
			Limit int `json:"limit"`
		}
		if err := decodeCoreToolArguments(input.Arguments, &args); err != nil || args.Limit < 0 || args.Limit > 50 {
			auditResult = "invalid"
			toolError("invalid task_list arguments")
			return
		}
		views, err := s.app.AgentCoreTaskList(request.Context(), claims, args.Limit)
		if err != nil {
			auditResult = "error"
			fail(err)
			return
		}
		respondJSON(map[string]any{"tasks": views})

	case "capabilities":
		if err := decodeCoreToolArguments(input.Arguments, &struct{}{}); err != nil {
			auditResult = "invalid"
			toolError("capabilities takes no arguments")
			return
		}
		respondJSON(map[string]any{"capabilities": s.agentCapabilityMap(request.Context(), claims, call)})

	case "memory_update":
		var args app.MemoryUpdate
		if err := decodeCoreToolArguments(input.Arguments, &args); err != nil {
			auditResult = "invalid"
			toolError("invalid memory_update arguments")
			return
		}
		if args.Empty() {
			auditResult = "invalid"
			toolError("memory_update needs at least one field")
			return
		}
		memory, err := s.app.UpdateAgentMemory(request.Context(), claims, args)
		if err != nil {
			auditResult = "error"
			fail(err)
			return
		}
		respondJSON(map[string]any{"ok": true, "memory": compactTaskMemory(memory)})

	case "progress":
		var args struct {
			Message string `json:"message"`
		}
		if err := decodeCoreToolArguments(input.Arguments, &args); err != nil {
			auditResult = "invalid"
			toolError("invalid progress arguments")
			return
		}
		if err := s.app.AddAgentCoreProgress(request.Context(), claims, args.Message); err != nil {
			auditResult = "error"
			fail(err)
			return
		}
		respondJSON(map[string]any{"ok": true})

	case "task_message":
		var args struct {
			TargetTaskID string `json:"target_task_id"`
			Content      string `json:"content"`
		}
		if err := decodeCoreToolArguments(input.Arguments, &args); err != nil ||
			strings.TrimSpace(args.TargetTaskID) == "" ||
			strings.TrimSpace(args.Content) == "" || len([]rune(args.Content)) > 8000 {
			auditResult = "invalid"
			toolError("task_message needs target_task_id and 1-8000 characters of content")
			return
		}
		turn, err := s.app.SubmitPeerTaskMessage(request.Context(), claims, args.TargetTaskID, args.Content)
		if err != nil {
			auditResult = "error"
			fail(err)
			return
		}
		respondJSON(map[string]any{"ok": true, "target_task_id": args.TargetTaskID, "turn_id": turn.ID})

	case "attachment_materialize":
		var args materializeAttachmentInput
		if err := decodeCoreToolArguments(input.Arguments, &args); err != nil {
			toolError("invalid_attachment_request")
			return
		}
		writer.Header().Set("Cache-Control", "private, no-store")
		result, _, code := s.agentAttachmentToScratch(request, call, args.AttachmentID)
		if code != "ok" {
			toolError(code)
			return
		}
		respondJSON(result)

	case "attachment_bind":
		var args struct {
			AttachmentIDs []string `json:"attachment_ids"`
			Message       string   `json:"message"`
		}
		if err := decodeCoreToolArguments(input.Arguments, &args); err != nil ||
			len(args.AttachmentIDs) == 0 || len(args.AttachmentIDs) > 8 ||
			len([]rune(args.Message)) > 4000 {
			auditResult = "invalid"
			toolError("attachment_bind needs 1-8 attachment_ids and an optional message of at most 4000 characters")
			return
		}
		attachments, err := s.app.BindAgentAttachments(request.Context(), claims, args.Message, args.AttachmentIDs)
		if err != nil {
			auditResult = "error"
			fail(err)
			return
		}
		respondJSON(map[string]any{"ok": true, "attachments": attachments})

	default:
		auditResult = "invalid"
		writeJSON(writer, http.StatusOK, map[string]any{
			"jsonrpc": "2.0", "id": payload.ID,
			"error": map[string]any{"code": -32602, "message": "unknown tool"},
		})
	}
}

// decodeCoreToolArguments enforces the schema's additionalProperties:false
// contract: a field the tool does not declare is rejected instead of silently
// ignored. It also rejects a second JSON value after the object.
func decodeCoreToolArguments(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("unexpected trailing data")
	}
	return nil
}

// safeCoreToolMessage keeps stable, human-readable validation failures while
// reducing store and SQL errors to a generic message. Tool text is model-visible
// and must not become an internal error or credential channel.
func safeCoreToolMessage(err error) string {
	if coded, ok := app.AsCodedError(err); ok && strings.TrimSpace(coded.Message) != "" {
		return coded.Message
	}
	message := strings.TrimSpace(err.Error())
	lower := strings.ToLower(message)
	if message == "" ||
		strings.Contains(lower, "sql") ||
		strings.Contains(lower, "database") ||
		strings.Contains(lower, "constraint") ||
		strings.Contains(lower, "disk") {
		return "operation failed"
	}
	return message
}

// coreMCPArgumentKeys audits which argument fields were used, never their
// values. A malformed arguments object yields no keys rather than raw text.
func coreMCPArgumentKeys(raw json.RawMessage) []string {
	var values map[string]json.RawMessage
	if json.Unmarshal(raw, &values) != nil {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func coreMCPNameIncluded(names []string, name string) bool {
	for _, item := range names {
		if item == name {
			return true
		}
	}
	return false
}

func agentCoreMCPTools(write bool) []mcpTool {
	return agentCoreMCPToolsNamed(app.CoreMCPToolNames(write))
}

func agentCoreMCPToolsNamed(names []string) []mcpTool {
	all := map[string]mcpTool{}
	for _, tool := range coreMCPTools() {
		all[tool.Name] = tool
	}
	result := make([]mcpTool, 0, len(all))
	for _, name := range names {
		if tool, ok := all[name]; ok {
			result = append(result, tool)
		}
	}
	return result
}

func coreMCPTools() []mcpTool {
	stringArray := func(description string) map[string]any {
		return map[string]any{
			"type": "array", "items": map[string]any{"type": "string"}, "description": description,
		}
	}
	return []mcpTool{
		{
			Name: "inbox_wait",
			Description: "Read and claim live inbox messages for this Turn. Use after_cursor from the last " +
				"response. Returns an empty list when the timeout expires. Available to every Agent in this Task.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"after_cursor": map[string]any{
						"type": "integer", "minimum": 0,
						"description": "Last processed inbox cursor; defaults to 0.",
					},
					"timeout_seconds": map[string]any{
						"type": "integer", "minimum": 0, "maximum": 30,
						"description": "How long to wait, 0-30 seconds; 0 uses the 20 second default.",
					},
				},
				"required": []string{}, "additionalProperties": false,
			},
		},
		{
			Name: "task_get",
			Description: "Read a bounded summary of this Task or another Task in the same Project. " +
				"A Task in a different Project is refused. Available to every Agent in this Task.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{
						"type": "string", "description": "Target Task ID; defaults to the current Task.",
					},
				},
				"required": []string{}, "additionalProperties": false,
			},
		},
		{
			Name:        "task_list",
			Description: "List recent Task summaries in this Task's Project. Available to every Agent in this Task.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"limit": map[string]any{
						"type": "integer", "minimum": 1, "maximum": 50,
						"description": "Maximum number of Tasks to return; defaults to 20.",
					},
				},
				"required": []string{}, "additionalProperties": false,
			},
		},
		{
			Name:        "capabilities",
			Description: "Read this Turn's Agent capability map. Available to every Agent in this Task.",
			InputSchema: map[string]any{
				"type": "object", "properties": map[string]any{},
				"required": []string{}, "additionalProperties": false,
			},
		},
		{
			Name: "memory_update",
			Description: "Update this Task's state card: the short note the Owner reads on the Task page and the " +
				"next Turn reads in its prompt. Main Agent only. Call it before the final answer of every Turn; " +
				"status, next_steps and open_questions replace what was there, so send the current picture, not a log. " +
				"A goal change goes in the same call.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"status": map[string]any{
						"type": "string", "maxLength": 600,
						"description": "Where the work stands right now, in one short paragraph (up to 600 characters). Replaces the previous status.",
					},
					"next_steps":     stringArray("What comes next, up to 6 short entries. Replaces the previous list; send [] when nothing is left."),
					"open_questions": stringArray("Decisions waiting on the Owner, up to 6 entries. Replaces the previous list; send [] once they are answered."),
					"add_constraints": stringArray("Standing decisions or limits the Owner stated that later Turns must keep (for example \"do not change the public API\"). " +
						"Appended; at most 12 are kept."),
					"remove_constraints": stringArray("Exact text of constraints that no longer apply."),
					"current_goal": map[string]any{
						"type": "string", "maxLength": 2000,
						"description": "The new Task goal, only when the goal really changed. Requires goal_reason.",
					},
					"goal_change_type": map[string]any{
						"type": "string", "enum": []string{"set", "revise", "pause", "resume", "complete", "reopen"},
						"description": "Kind of goal change; defaults to revise.",
					},
					"goal_reason": map[string]any{
						"type": "string", "maxLength": 500,
						"description": "Why the goal changed. Required with current_goal.",
					},
				},
				"required": []string{}, "additionalProperties": false,
			},
		},
		{
			Name: "progress",
			Description: "Report one progress message to the Owner. Main Agent only; use it for material " +
				"state changes, not tool-by-tool narration.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"message": map[string]any{
						"type": "string", "minLength": 1, "maxLength": 4000,
						"description": "Progress text, 1 to 4000 characters.",
					},
				},
				"required": []string{"message"}, "additionalProperties": false,
			},
		},
		{
			Name: "task_message",
			Description: "Send a message to another Task in the same Project. Main Agent only. " +
				"The target sees the source as a peer Task, never as the Owner.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"target_task_id": map[string]any{
						"type": "string", "description": "Target Task ID in the same Project.",
					},
					"content": map[string]any{
						"type": "string", "minLength": 1, "maxLength": 8000,
						"description": "Message text, 1 to 8000 characters.",
					},
				},
				"required": []string{"target_task_id", "content"}, "additionalProperties": false,
			},
		},
		{
			Name: "attachment_materialize",
			Description: "Stream an attachment from this Task to this Agent's scratch on the execution host. " +
				"Returns only its local path, size, SHA-256 and media type. Does not extract archives or return bytes. Default per-file limit: 64 MiB.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"attachment_id": map[string]any{"type": "string", "minLength": 1, "maxLength": 75},
				},
				"required": []string{"attachment_id"}, "additionalProperties": false,
			},
		},
		{
			Name: "attachment_bind",
			Description: "Bind already uploaded, unsent attachment IDs to a new attachment message in this " +
				"Task. Main Agent only. Upload stays on the HTTP attachment API.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"attachment_ids": map[string]any{
						"type": "array", "minItems": 1, "maxItems": 8,
						"items":       map[string]any{"type": "string"},
						"description": "1 to 8 attachment IDs uploaded for this Task and not yet bound.",
					},
					"message": map[string]any{
						"type": "string", "maxLength": 4000,
						"description": "Optional message to bind with the attachments.",
					},
				},
				"required": []string{"attachment_ids"}, "additionalProperties": false,
			},
		},
	}
}
