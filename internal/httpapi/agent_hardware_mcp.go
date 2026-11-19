package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
)

// agentHardwareMCPMaxRequestBytes bounds one JSON-RPC request. Terminal output
// only travels in the response; the request carries identifiers and cursors.
const agentHardwareMCPMaxRequestBytes int64 = 1 << 20

// The terminal read returns a bounded page so one call cannot fill a context
// window. Truncation is explicit and the caller can continue from next_after.
const (
	agentHardwareMCPMaxResponseBytes = 64 << 10
	agentHardwareMCPDefaultLimit     = 100
	agentHardwareMCPMaxLimit         = 1000
)

// agentHardwareMCPIDPattern is the stored hardware group grammar after
// hardwareconfig.NormalizeID: lowercase letters, digits, underscore and hyphen,
// never starting with a separator. Anything else is rejected before a store
// lookup, so traversal fragments never reach the query.
var agentHardwareMCPIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// agentHardwareMCP is the HTTP MCP facade for the read-only Task hardware
// domain. Its injection trigger is independent from the browser domain: the
// Task needs at least one bound hardware group. Every method revalidates the
// Turn capability, AgentCallContext and this domain gate server-side.
func (s *Server) agentHardwareMCP(writer http.ResponseWriter, request *http.Request) {
	if agentCoreMCPForwarded(request) {
		writeError(writer, http.StatusForbidden, "agent_hardware_mcp_forwarded")
		return
	}
	claims, ok := agentClaimsFromContext(request.Context())
	if !ok {
		writeError(writer, http.StatusUnauthorized, "agent_capability_required")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, agentHardwareMCPMaxRequestBytes))
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
	if !s.app.AgentHardwareMCPAllowed(request.Context(), call) {
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
			"serverInfo":      map[string]any{"name": "aha2-hardware", "version": "1.0.0"},
		})
	case "notifications/initialized", "notifications/cancelled", "notifications/progress":
		writer.WriteHeader(http.StatusAccepted)
	case "ping":
		respond(map[string]any{})
	case "tools/list":
		respond(map[string]any{"tools": agentHardwareMCPTools()})
	case "tools/call":
		s.callAgentHardwareMCPTool(writer, request, call, payload)
	default:
		if strings.HasPrefix(payload.Method, "notifications/") {
			writer.WriteHeader(http.StatusAccepted)
			return
		}
		respondError(-32601, "method not found")
	}
}

func (s *Server) callAgentHardwareMCPTool(writer http.ResponseWriter, request *http.Request, call app.AgentCallContext, payload mcpRequest) {
	respond := func(result any) {
		writeJSON(writer, http.StatusOK, map[string]any{
			"jsonrpc": "2.0", "id": payload.ID, "result": result,
		})
	}
	var input struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	// hardwareMCPError is a stable classification with a fixed public message.
	// Store errors, host details and terminal output stay in the server log.
	hardwareMCPError := func(code, message string) {
		respond(map[string]any{
			"isError": true,
			"content": []map[string]any{{"type": "text", "text": code + ": " + message}},
		})
	}
	toolError := func(err error) {
		log.Printf("agent hardware mcp tool %s failed: %v", input.Name, err)
		code, message := hardwareMCPErrorCode(err)
		hardwareMCPError(code, message)
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
	if !app.HardwareMCPToolIsRead(input.Name) {
		writeJSON(writer, http.StatusOK, map[string]any{
			"jsonrpc": "2.0", "id": payload.ID,
			"error": map[string]any{"code": -32602, "message": "unknown tool"},
		})
		return
	}
	auditResult := "ok"
	started := time.Now()
	auditIDs := map[string]any{}
	defer func() {
		data := map[string]any{
			"tool": input.Name, "turn_id": call.Turn.ID, "result": auditResult,
			"duration_ms": time.Since(started).Milliseconds(), "argument_keys": coreMCPArgumentKeys(input.Arguments),
		}
		for key, value := range auditIDs {
			data[key] = value
		}
		s.audit(request, "agent.hardware_mcp.call", "task", call.Task.ID, data)
	}()

	switch input.Name {
	case "hardware_list":
		if err := decodeCoreToolArguments(input.Arguments, &struct{}{}); err != nil {
			auditResult = "invalid"
			invalidToolError("hardware_list takes no arguments")
			return
		}
		result, err := s.agentHardwareMCPList(request.Context(), call)
		if err != nil {
			auditResult = "error"
			fail(err)
			return
		}
		respondJSON(result)

	case "hardware_terminal_read":
		var args struct {
			HardwareID string `json:"hardware_id"`
			Transport  string `json:"transport"`
			After      int64  `json:"after"`
			Limit      int    `json:"limit"`
		}
		if err := decodeCoreToolArguments(input.Arguments, &args); err != nil {
			auditResult = "invalid"
			invalidToolError("hardware_terminal_read accepts hardware_id, transport, after and limit")
			return
		}
		hardwareID := strings.TrimSpace(args.HardwareID)
		if !agentHardwareMCPIDPattern.MatchString(hardwareID) {
			auditResult = "invalid"
			invalidToolError("hardware_id must be a bound hardware ID")
			return
		}
		transport := strings.TrimSpace(args.Transport)
		if transport != "" && transport != domain.HardwareTransportSerial && transport != domain.HardwareTransportNetwork {
			auditResult = "invalid"
			invalidToolError("transport must be serial or network")
			return
		}
		if args.After < 0 {
			auditResult = "invalid"
			invalidToolError("after must be a non-negative cursor")
			return
		}
		limit := args.Limit
		if limit == 0 {
			limit = agentHardwareMCPDefaultLimit
		}
		if limit < 1 || limit > agentHardwareMCPMaxLimit {
			auditResult = "invalid"
			invalidToolError("limit must be between 1 and 1000")
			return
		}
		auditIDs["hardware_id"] = hardwareID
		result, err := s.agentHardwareMCPTerminalRead(request.Context(), call, hardwareID, transport, args.After, limit)
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

// agentHardwareMCPTransport is the per-transport health view. It never carries
// an endpoint, login status or host diagnostic.
type agentHardwareMCPTransport struct {
	Transport string `json:"transport"`
	Status    string `json:"status"`
	Connected bool   `json:"connected"`
}

// agentHardwareMCPItem is the non-sensitive resource shape. Serial device,
// network host/port/protocol/ssh auth, username, credential ref and password
// state are deliberately absent.
type agentHardwareMCPItem struct {
	HardwareID  string                      `json:"hardware_id"`
	ResourceID  string                      `json:"resource_id,omitempty"`
	Description string                      `json:"description,omitempty"`
	Mode        string                      `json:"mode"`
	Access      string                      `json:"access"`
	ReadOnly    bool                        `json:"read_only"`
	Transports  []agentHardwareMCPTransport `json:"transports"`
}

// agentHardwareMCPList returns only the groups bound to the calling Task. The
// system-level resource list that the Owner UI uses is never exposed.
func (s *Server) agentHardwareMCPList(ctx context.Context, call app.AgentCallContext) (map[string]any, error) {
	groups, err := s.store.HardwareGroups(ctx, call.Task.ID)
	if err != nil {
		return nil, err
	}
	readOnlyTask := call.Task.Status.Terminal()
	items := make([]agentHardwareMCPItem, 0, len(groups))
	for _, group := range groups {
		readOnly := readOnlyTask || !group.Writable()
		transports := make([]agentHardwareMCPTransport, 0, 2)
		for _, transport := range []string{domain.HardwareTransportSerial, domain.HardwareTransportNetwork} {
			if !group.Supports(transport) {
				continue
			}
			status := disconnectedHardwareStatus(call.Task.ID, group.ID, transport, readOnly)
			if s.hardware != nil {
				status = s.hardware.Status(call.Task.ID, group.ID, transport, readOnly)
			}
			transports = append(transports, agentHardwareMCPTransport{
				Transport: transport, Status: status.Status, Connected: status.Connected,
			})
		}
		items = append(items, agentHardwareMCPItem{
			HardwareID: group.ID, ResourceID: group.ResourceID, Description: group.Description,
			Mode: group.Mode, Access: group.Access, ReadOnly: readOnly, Transports: transports,
		})
	}
	return map[string]any{"hardware": items, "total": len(items)}, nil
}

// agentHardwareMCPEvent is the terminal row shape. TaskID and HardwareID are
// already known from the enclosing result, so they are not repeated per row.
type agentHardwareMCPEvent struct {
	Sequence  int64     `json:"sequence"`
	Direction string    `json:"direction"`
	Data      string    `json:"data"`
	Encoding  string    `json:"encoding"`
	Source    string    `json:"source,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type agentHardwareMCPTerminal struct {
	HardwareID string                  `json:"hardware_id"`
	Transport  string                  `json:"transport"`
	Items      []agentHardwareMCPEvent `json:"items"`
	Truncated  bool                    `json:"truncated"`
	NextAfter  int64                   `json:"next_after"`
}

// agentHardwareMCPTerminalRead mirrors the HTTP terminal handler: the same
// bound-group lookup, transport default and IO page query. Unbound hardware,
// another Task's hardware and a cross-Project hardware ID all return the same
// hardware_group_not_found, so the tool cannot probe existence.
func (s *Server) agentHardwareMCPTerminalRead(ctx context.Context, call app.AgentCallContext, hardwareID, transport string, after int64, limit int) (agentHardwareMCPTerminal, error) {
	group, err := s.store.HardwareGroup(ctx, call.Task.ID, hardwareID)
	if errors.Is(err, sql.ErrNoRows) {
		return agentHardwareMCPTerminal{}, &hardwareMCPError{
			code: "hardware_group_not_found", message: "hardware group is not bound to this Task",
		}
	}
	if err != nil {
		return agentHardwareMCPTerminal{}, err
	}
	if transport == "" {
		if group.Supports(domain.HardwareTransportSerial) {
			transport = domain.HardwareTransportSerial
		} else {
			transport = domain.HardwareTransportNetwork
		}
	}
	if !group.Supports(transport) {
		return agentHardwareMCPTerminal{}, &hardwareMCPError{
			code: "hardware_transport_invalid", message: "hardware group does not configure that transport",
		}
	}
	page, err := s.store.HardwareIOPage(ctx, call.Task.ID, group.ID, transport, after, limit)
	if err != nil {
		return agentHardwareMCPTerminal{}, &hardwareMCPError{
			code: "hardware_io_failed", message: "terminal output could not be read",
		}
	}
	return agentHardwareMCPTerminalView(group.ID, transport, page), nil
}

// agentHardwareMCPTerminalView bounds the JSON result to 64 KiB. It keeps the
// oldest events of the page first, so next_after always advances by the events
// the caller actually received. A single oversized event is truncated in place
// and flagged; the caller can move past it with next_after.
func agentHardwareMCPTerminalView(hardwareID, transport string, page domain.HardwareIOPage) agentHardwareMCPTerminal {
	result := agentHardwareMCPTerminal{
		HardwareID: hardwareID, Transport: transport,
		Items: make([]agentHardwareMCPEvent, 0, len(page.Items)), NextAfter: 0,
	}
	base, _ := json.Marshal(result)
	remaining := agentHardwareMCPMaxResponseBytes - len(base) - 8
	for _, item := range page.Items {
		event := agentHardwareMCPEvent{
			Sequence: item.Sequence, Direction: item.Direction, Data: item.Data,
			Encoding: item.Encoding, Source: item.Source, CreatedAt: item.CreatedAt,
		}
		encoded, _ := json.Marshal(event)
		if len(encoded)+1 > remaining {
			if len(result.Items) == 0 {
				event = fitHardwareMCPEvent(event, remaining)
				result.Items = append(result.Items, event)
			}
			result.Truncated = true
			break
		}
		result.Items = append(result.Items, event)
		remaining -= len(encoded) + 1
	}
	if page.HasMore {
		result.Truncated = true
	}
	for _, item := range result.Items {
		if item.Sequence > result.NextAfter {
			result.NextAfter = item.Sequence
		}
	}
	return result
}

// fitHardwareMCPEvent trims Data until the encoded event fits the remaining
// byte budget. Data can be hex or base64, so a truncated row is only useful as
// a bounded hint; the format is preserved as-is and the caller sees the flag.
func fitHardwareMCPEvent(item agentHardwareMCPEvent, available int) agentHardwareMCPEvent {
	if available < 0 {
		available = 0
	}
	for attempt := 0; attempt < 8; attempt++ {
		encoded, _ := json.Marshal(item)
		if len(encoded) <= available {
			return item
		}
		over := len(encoded) - available
		keep := len(item.Data) - over - 8
		if keep <= 0 {
			item.Data = ""
			return item
		}
		data := item.Data[:keep]
		for len(data) > 0 && !utf8.ValidString(data) {
			data = data[:len(data)-1]
		}
		if data == item.Data {
			item.Data = ""
			return item
		}
		item.Data = data
	}
	return item
}

// hardwareMCPError is the stable classification used by the hardware tools.
type hardwareMCPError struct {
	code    string
	message string
}

func (e *hardwareMCPError) Error() string {
	return e.code + ": " + e.message
}

func hardwareMCPErrorCode(err error) (string, string) {
	var typed *hardwareMCPError
	if errors.As(err, &typed) {
		return typed.code, typed.message
	}
	// Store and runtime errors can name hosts, devices or credentials; only
	// the server log sees them.
	return "hardware_operation_failed", "hardware operation failed"
}

func agentHardwareMCPTools() []mcpTool {
	object := func(properties map[string]any, required ...string) map[string]any {
		if required == nil {
			required = []string{}
		}
		return map[string]any{
			"type": "object", "properties": properties,
			"required": required, "additionalProperties": false,
		}
	}
	return []mcpTool{
		{
			Name: "hardware_list",
			Description: "List the hardware resources bound to this Task. Each row carries the hardware and " +
				"resource ID, mode, access, read-only state and per-transport health. Passwords, credential " +
				"references, serial device paths and network configuration are never returned.",
			InputSchema: object(map[string]any{}),
		},
		{
			Name: "hardware_terminal_read",
			Description: "Read bounded terminal output for one hardware group bound to this Task. transport " +
				"is optional (serial or network). The response is capped at 64 KiB; truncated and next_after " +
				"report whether more output remains. Read-only: no connect, send, login, ADB exec or config.",
			InputSchema: object(map[string]any{
				"hardware_id": map[string]any{
					"type": "string", "minLength": 1, "maxLength": 64,
					"pattern":     "^[a-z0-9][a-z0-9_-]{0,63}$",
					"description": "Hardware ID from hardware_list.",
				},
				"transport": map[string]any{
					"type": "string", "enum": []string{"serial", "network"},
					"description": "Optional transport; defaults to the group's configured transport.",
				},
				"after": map[string]any{
					"type": "integer", "minimum": 0,
					"description": "Opaque cursor from a previous next_after; 0 reads the most recent retained output page.",
				},
				"limit": map[string]any{
					"type": "integer", "minimum": 1, "maximum": agentHardwareMCPMaxLimit, "default": agentHardwareMCPDefaultLimit,
					"description": "Maximum events to read before the 64 KiB response cap.",
				},
			}, "hardware_id"),
		},
	}
}
