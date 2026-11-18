package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
)

// agentKnowledgeMCPMaxRequestBytes bounds one JSON-RPC request. Knowledge
// bodies are capped at the vault write limit; uploads stay on the attachment
// API and are not part of this domain.
const agentKnowledgeMCPMaxRequestBytes int64 = 1 << 20

// agentKnowledgeMCP is the HTTP MCP facade for the knowledge domain. Every
// method, including initialize and tools/list, is mounted behind
// withAgentCapability and revalidates AgentCallContext. A Task whose knowledge
// is disabled, the pet Task and channel-routed Turns are refused even when a
// caller knows the endpoint URL, because the server is never injected for them.
func (s *Server) agentKnowledgeMCP(writer http.ResponseWriter, request *http.Request) {
	if agentCoreMCPForwarded(request) {
		writeError(writer, http.StatusForbidden, "agent_knowledge_mcp_forwarded")
		return
	}
	claims, ok := agentClaimsFromContext(request.Context())
	if !ok {
		writeError(writer, http.StatusUnauthorized, "agent_capability_required")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, agentKnowledgeMCPMaxRequestBytes))
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
	if !s.app.AgentKnowledgeMCPAllowed(request.Context(), call) {
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
			"serverInfo":      map[string]any{"name": "aha2-knowledge", "version": "1.0.0"},
		})
	case "notifications/initialized", "notifications/cancelled", "notifications/progress":
		writer.WriteHeader(http.StatusAccepted)
	case "ping":
		respond(map[string]any{})
	case "tools/list":
		write := s.app.AgentWriteAllowed(request.Context(), call)
		respond(map[string]any{"tools": agentKnowledgeMCPTools(write)})
	case "tools/call":
		s.callAgentKnowledgeMCPTool(writer, request, claims, call, payload)
	default:
		if strings.HasPrefix(payload.Method, "notifications/") {
			writer.WriteHeader(http.StatusAccepted)
			return
		}
		respondError(-32601, "method not found")
	}
}

func (s *Server) callAgentKnowledgeMCPTool(writer http.ResponseWriter, request *http.Request, claims agentapi.Claims, call app.AgentCallContext, payload mcpRequest) {
	respond := func(result any) {
		writeJSON(writer, http.StatusOK, map[string]any{
			"jsonrpc": "2.0", "id": payload.ID, "result": result,
		})
	}
	toolError := func(err error) {
		code, message := knowledgeMCPErrorCode(err)
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
	invalidWriteToolError := func(message string) {
		respond(map[string]any{
			"isError": true,
			"content": []map[string]any{{"type": "text", "text": "knowledge_write_invalid: " + message}},
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
		case errors.Is(err, app.ErrAgentReadOnly), errors.Is(err, app.ErrKnowledgeReadOnly):
			writeError(writer, http.StatusForbidden, app.ErrCodeTaskReadOnly)
		default:
			toolError(err)
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
	for _, name := range app.KnowledgeMCPToolNames(true) {
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
		s.audit(request, "agent.knowledge_mcp.call", "task", call.Task.ID, map[string]any{
			"tool": input.Name, "turn_id": call.Turn.ID, "result": auditResult,
			"duration_ms":   time.Since(started).Milliseconds(),
			"argument_keys": coreMCPArgumentKeys(input.Arguments),
		})
	}()
	if app.KnowledgeMCPToolIsWrite(input.Name) {
		if call.Turn.AgentID != "main" {
			auditResult = "denied"
			writeError(writer, http.StatusForbidden, "agent_operation_forbidden")
			return
		}
		if !s.app.AgentWriteAllowed(request.Context(), call) {
			auditResult = "denied"
			writeError(writer, http.StatusForbidden, app.ErrCodeTaskReadOnly)
			return
		}
	}

	switch input.Name {
	case "knowledge_list":
		var args struct {
			Query  string `json:"query"`
			Type   string `json:"type"`
			Limit  int    `json:"limit"`
			Offset int    `json:"offset"`
		}
		if err := decodeCoreToolArguments(input.Arguments, &args); err != nil ||
			args.Limit < 0 || args.Limit > 100 || args.Offset < 0 || args.Offset > 10000 ||
			len([]rune(args.Query)) > 500 || len([]rune(args.Type)) > 100 {
			auditResult = "invalid"
			invalidToolError("invalid knowledge_list arguments")
			return
		}
		if args.Limit == 0 {
			args.Limit = 50
		}
		result, err := s.app.AgentKnowledgeMCPList(
			request.Context(), claims, args.Query, args.Type, args.Limit, args.Offset,
		)
		if err != nil {
			auditResult = "error"
			fail(err)
			return
		}
		respondJSON(result)

	case "knowledge_get":
		var args struct {
			ID string `json:"id"`
		}
		if err := decodeCoreToolArguments(input.Arguments, &args); err != nil ||
			strings.TrimSpace(args.ID) == "" || len([]rune(args.ID)) > 200 {
			auditResult = "invalid"
			invalidToolError("knowledge_get requires id")
			return
		}
		result, err := s.app.AgentKnowledgeMCPGet(request.Context(), claims, args.ID)
		if err != nil {
			auditResult = "error"
			fail(err)
			return
		}
		respondJSON(result)

	case "knowledge_guide":
		if err := decodeCoreToolArguments(input.Arguments, &struct{}{}); err != nil {
			auditResult = "invalid"
			invalidToolError("knowledge_guide takes no arguments")
			return
		}
		result, err := s.app.AgentKnowledgeMCPGuide(request.Context(), claims)
		if err != nil {
			auditResult = "error"
			fail(err)
			return
		}
		respondJSON(result)

	case "skill_list":
		if err := decodeCoreToolArguments(input.Arguments, &struct{}{}); err != nil {
			auditResult = "invalid"
			invalidToolError("skill_list takes no arguments")
			return
		}
		items, err := s.app.AgentKnowledgeMCPSkillList(request.Context(), claims)
		if err != nil {
			auditResult = "error"
			fail(err)
			return
		}
		respondJSON(map[string]any{"skills": items})

	case "skill_get":
		var args struct {
			SkillID string `json:"skill_id"`
		}
		if err := decodeCoreToolArguments(input.Arguments, &args); err != nil ||
			strings.TrimSpace(args.SkillID) == "" || len([]rune(args.SkillID)) > 300 {
			auditResult = "invalid"
			invalidToolError("skill_get requires skill_id")
			return
		}
		item, err := s.app.AgentKnowledgeMCPSkillGet(request.Context(), claims, args.SkillID)
		if err != nil {
			auditResult = "error"
			fail(err)
			return
		}
		respondJSON(map[string]any{"skill": item})

	case "knowledge_submit":
		var args app.AgentKnowledgeMCPSubmitInput
		if err := decodeCoreToolArguments(input.Arguments, &args); err != nil {
			auditResult = "invalid"
			invalidWriteToolError("invalid knowledge_submit arguments")
			return
		}
		result, err := s.app.SubmitAgentKnowledgeMCPEntry(request.Context(), claims, args)
		if err != nil {
			auditResult = "error"
			fail(err)
			return
		}
		respondJSON(result)

	case "knowledge_feedback":
		var args struct {
			EntryID string `json:"entry_id"`
			Kind    string `json:"kind"`
		}
		if err := decodeCoreToolArguments(input.Arguments, &args); err != nil ||
			strings.TrimSpace(args.EntryID) == "" || len([]rune(args.EntryID)) > 200 ||
			strings.TrimSpace(args.Kind) == "" || len([]rune(args.Kind)) > 20 {
			auditResult = "invalid"
			invalidWriteToolError("knowledge_feedback requires entry_id and kind")
			return
		}
		result, err := s.app.SubmitAgentKnowledgeMCPFeedback(
			request.Context(), claims, args.EntryID, args.Kind,
		)
		if err != nil {
			auditResult = "error"
			fail(err)
			return
		}
		respondJSON(result)

	default:
		auditResult = "invalid"
		writeJSON(writer, http.StatusOK, map[string]any{
			"jsonrpc": "2.0", "id": payload.ID,
			"error": map[string]any{"code": -32602, "message": "unknown tool"},
		})
	}
}

// knowledgeMCPErrorCode produces a stable machine classification plus a safe
// message. A write's code matches the HTTP knowledge API's error code, so the
// same rejection has the same class on both surfaces.
func knowledgeMCPErrorCode(err error) (string, string) {
	var writeErr *knowledgevault.WriteError
	if errors.As(err, &writeErr) {
		message := strings.TrimSpace(writeErr.Message)
		if message == "" {
			message = writeErr.Code
		}
		return writeErr.Code, message
	}
	switch {
	case errors.Is(err, app.ErrAgentCallForbidden):
		return "agent_operation_forbidden", "agent operation is forbidden"
	case errors.Is(err, app.ErrAgentTurnInactive):
		return "agent_turn_inactive", "agent turn is not active"
	case errors.Is(err, app.ErrAgentReadOnly), errors.Is(err, app.ErrKnowledgeReadOnly):
		return "task_read_only", "task is read-only"
	case errors.Is(err, app.ErrKnowledgeMCPEntryNotFound):
		return "knowledge_not_found", "knowledge entry is not in this Task's readable range"
	case errors.Is(err, app.ErrKnowledgeMCPSkillNotFound):
		return "skill_not_found", "skill is not selected on this Task"
	case errors.Is(err, app.ErrRevisionConflict):
		return knowledgevault.WriteErrorRevisionConflict, "knowledge revision conflict"
	default:
		return "operation_failed", safeCoreToolMessage(err)
	}
}

func agentKnowledgeMCPTools(write bool) []mcpTool {
	all := map[string]mcpTool{}
	for _, tool := range knowledgeMCPTools() {
		all[tool.Name] = tool
	}
	result := make([]mcpTool, 0, len(all))
	for _, name := range app.KnowledgeMCPToolNames(write) {
		if tool, ok := all[name]; ok {
			result = append(result, tool)
		}
	}
	return result
}

func knowledgeMCPTools() []mcpTool {
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
			Name: "knowledge_list",
			Description: "List knowledge metadata for the current Project plus the global library. " +
				"Never returns another Project or a body; use knowledge_get for content. Available to every Agent.",
			InputSchema: object(map[string]any{
				"query": map[string]any{
					"type": "string", "maxLength": 500,
					"description": "Case-insensitive title/body filter.",
				},
				"type": map[string]any{
					"type": "string", "maxLength": 100,
					"description": "Exact knowledge type filter.",
				},
				"limit": map[string]any{
					"type": "integer", "minimum": 0, "maximum": 100,
					"description": "Page size, 1-100; defaults to 50.",
				},
				"offset": map[string]any{
					"type": "integer", "minimum": 0, "maximum": 10000,
					"description": "Page offset; defaults to 0.",
				},
			}),
		},
		{
			Name: "knowledge_get",
			Description: "Read one knowledge entry from this Task's readable Project/global range and report " +
				"whether it came from the vault or the legacy store. A cross-Project ID is reported as not found.",
			InputSchema: object(map[string]any{
				"id": map[string]any{
					"type": "string", "minLength": 1, "maxLength": 200,
					"description": "Knowledge entry ID from knowledge_list.",
				},
			}, "id"),
		},
		{
			Name: "knowledge_guide",
			Description: "Read the static knowledge guide. The response omits internal filesystem paths " +
				"and credentials. Available to every Agent.",
			InputSchema: empty(),
		},
		{
			Name: "skill_list",
			Description: "List metadata for Skills selected on this Task. Package files, vault keys and " +
				"internal paths are never returned. Available to every Agent.",
			InputSchema: empty(),
		},
		{
			Name: "skill_get",
			Description: "Read one selected Skill's instructions. Read-only; package files, vault keys and " +
				"internal paths are never returned. Available to every Agent.",
			InputSchema: object(map[string]any{
				"skill_id": map[string]any{
					"type": "string", "minLength": 1, "maxLength": 300,
					"description": "Skill ID from skill_list.",
				},
			}, "skill_id"),
		},
		{
			Name: "knowledge_submit",
			Description: "Create or revise a knowledge candidate in the current Project. Main Agent only. " +
				"Create requires scope=project, type, title, summary, body and search_query or duplicate_ack. " +
				"Revise requires entry_id and the fresh base_revision; a stale base is a conflict, never an " +
				"overwrite. Global scope, publish, delete and review are not available. A legacy source result " +
				"is a pending proposal, not verified knowledge.",
			InputSchema: object(map[string]any{
				"mode": map[string]any{
					"type": "string", "enum": []string{"create", "revise"},
					"description": "Write mode; defaults to create.",
				},
				"scope": map[string]any{
					"type": "string", "enum": []string{"project"},
					"description": "Project scope only. Required for create.",
				},
				"entry_id": map[string]any{
					"type": "string", "maxLength": 200,
					"description": "Existing entry ID for revise.",
				},
				"base_revision": map[string]any{
					"type": "integer", "minimum": 0,
					"description": "Revision read from knowledge_get for revise.",
				},
				"type": map[string]any{
					"type": "string", "maxLength": 100,
					"description": "Knowledge type, e.g. practice or diagnostic.",
				},
				"title":   map[string]any{"type": "string", "maxLength": 300},
				"summary": map[string]any{"type": "string", "maxLength": 600},
				"body":    map[string]any{"type": "string", "maxLength": 20000},
				"search_query": map[string]any{
					"type": "string", "maxLength": 200,
					"description": "Duplicate search performed before a create.",
				},
				"duplicate_ack": map[string]any{
					"type":        "boolean",
					"description": "True when the Agent searched and found no duplicate.",
				},
			}),
		},
		{
			Name: "knowledge_feedback",
			Description: "Record helped, stale or wrong feedback for a knowledge entry in this Task's " +
				"readable range. Main Agent only. Feedback never rewrites the entry body.",
			InputSchema: object(map[string]any{
				"entry_id": map[string]any{
					"type": "string", "minLength": 1, "maxLength": 200,
				},
				"kind": map[string]any{
					"type": "string", "enum": []string{"helped", "stale", "wrong"},
				},
			}, "entry_id", "kind"),
		},
	}
}
