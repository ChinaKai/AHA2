package httpapi

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/gateway"
)

type petMCPRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type petMCPTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// petMCP is a local MCP endpoint for the pet Task backend. It is intentionally
// loopback-only and exposes only the pet actions plus the owner-gated read-only
// AHA overview/detail and approval-request tools.
func (s *Server) petMCP(writer http.ResponseWriter, request *http.Request) {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil || !isLoopbackHost(host) {
		writeError(writer, http.StatusForbidden, "pet_mcp_local_only")
		return
	}
	var payload petMCPRequest
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	respond := func(result any, rpcErr map[string]any) {
		body := map[string]any{"jsonrpc": "2.0", "id": payload.ID}
		if rpcErr != nil {
			body["error"] = rpcErr
		} else {
			body["result"] = result
		}
		writeJSON(writer, http.StatusOK, body)
	}
	switch payload.Method {
	case "initialize":
		respond(map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "aha2-pet", "version": "1.0.0"},
		}, nil)
	case "notifications/initialized", "ping":
		respond(map[string]any{}, nil)
	case "tools/list":
		tools := make([]petMCPTool, 0)
		for _, tool := range s.app.PetMCPTools(request.Context()) {
			tools = append(tools, petMCPTool{Name: tool.Name, Description: tool.Description, InputSchema: tool.Parameters})
		}
		respond(map[string]any{"tools": tools}, nil)
	case "tools/call":
		var call struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(payload.Params, &call); err != nil || strings.TrimSpace(call.Name) == "" {
			respond(nil, map[string]any{"code": -32602, "message": "invalid tool call"})
			return
		}
		if len(call.Arguments) == 0 {
			call.Arguments = json.RawMessage(`{}`)
		}
		text, err := s.app.PetMCPTool(request.Context(), call.Name, call.Arguments)
		if err != nil {
			respond(map[string]any{
				"isError": true,
				"content": []map[string]any{{"type": "text", "text": err.Error()}},
			}, nil)
			return
		}
		respond(map[string]any{
			"content": []map[string]any{{"type": "text", "text": text}},
		}, nil)
	default:
		respond(nil, map[string]any{"code": -32601, "message": "method not found"})
	}
}

func isLoopbackHost(host string) bool {
	ip := net.ParseIP(strings.TrimSpace(host))
	return ip != nil && ip.IsLoopback()
}

// compile-time guard that MCP schemas stay compatible with gateway tools.
var _ = gateway.ChatTool{}
var _ = app.PetToolInfo{}
