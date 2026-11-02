package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/channel"
)

// agentChannelMCP is the narrow MCP endpoint attached to group task_route
// Turns. It exposes exactly two tools: reply to the message that triggered this
// Turn, or record that it deliberately needs no reply. A read-only Agent cannot
// run curl, and a task_route has no automatic group mirror, so without this
// endpoint a successful Turn can only end in no_channel_reply.
//
// The endpoint is mounted behind withAgentCapability. It therefore acts with
// the same Task/Turn claims as the Agent API and cannot be used by another Task
// or after the Turn ends.
func (s *Server) agentChannelMCP(writer http.ResponseWriter, request *http.Request) {
	claims, ok := agentClaimsFromContext(request.Context())
	if !ok {
		writeError(writer, http.StatusUnauthorized, "agent_capability_required")
		return
	}
	var payload mcpRequest
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
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
			"serverInfo":      map[string]any{"name": "aha2-channel", "version": "1.0.0"},
		})
	case "notifications/initialized", "notifications/cancelled":
		// A notification has no response body; 202 tells Streamable HTTP
		// clients the message was accepted.
		writer.WriteHeader(http.StatusAccepted)
	case "ping":
		respond(map[string]any{})
	case "tools/list":
		respond(map[string]any{"tools": channelMCPTools()})
	case "tools/call":
		s.callChannelMCPTool(writer, request, claims, payload)
	default:
		respondError(-32601, "method not found")
	}
}

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func channelMCPTools() []mcpTool {
	return []mcpTool{
		{
			Name: "channel_reply",
			Description: "Reply to the channel message that triggered this Turn. " +
				"Use this instead of finishing silently when the conversation needs an answer. " +
				"The current message's receipt is resolved automatically.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"message": map[string]any{
						"type":        "string",
						"description": "Reply text, 1 to 4000 characters, suitable for chat rendering.",
					},
					"inbound_receipt_id": map[string]any{
						"type":        "string",
						"description": "Optional receipt override. Defaults to the current Turn's routed message.",
					},
					"attachment_ids": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Optional attachment IDs uploaded during this Turn.",
					},
				},
				"required":             []string{"message"},
				"additionalProperties": false,
			},
		},
		{
			Name: "channel_reply_decision",
			Description: "End this channel Turn without replying. Use only when the message " +
				"deliberately needs no reply. The reason is shown on the AHA Web status card, " +
				"not sent to the group.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"reason": map[string]any{
						"type":        "string",
						"description": "Why this message needs no reply, 1 to 300 characters.",
					},
					"inbound_receipt_id": map[string]any{
						"type":        "string",
						"description": "Optional receipt override. Defaults to the current Turn's routed message.",
					},
				},
				"required":             []string{"reason"},
				"additionalProperties": false,
			},
		},
	}
}

func (s *Server) callChannelMCPTool(writer http.ResponseWriter, request *http.Request, claims agentapi.Claims, payload mcpRequest) {
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
	if s.channels == nil {
		toolError("channel reply is unavailable")
		return
	}
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(payload.Params, &call); err != nil || strings.TrimSpace(call.Name) == "" {
		writeJSON(writer, http.StatusOK, map[string]any{
			"jsonrpc": "2.0", "id": payload.ID,
			"error": map[string]any{"code": -32602, "message": "invalid tool call"},
		})
		return
	}
	if len(call.Arguments) == 0 {
		call.Arguments = json.RawMessage(`{}`)
	}
	switch call.Name {
	case "channel_reply":
		var input struct {
			Message          string   `json:"message"`
			InboundReceiptID string   `json:"inbound_receipt_id"`
			AttachmentIDs    []string `json:"attachment_ids"`
		}
		if err := json.Unmarshal(call.Arguments, &input); err != nil {
			toolError("invalid channel_reply arguments")
			return
		}
		receiptID, err := s.channelMCPReceiptID(request, claims, input.InboundReceiptID)
		if err != nil {
			toolError("this Turn has no routed channel message to reply to")
			return
		}
		_, err = s.channels.SendAgentChannelReply(request.Context(), claims, channel.AgentChannelReplyInput{
			RequestID:        channelMCPRequestID(claims.TurnID, receiptID, input.Message),
			InboundReceiptID: receiptID,
			Message:          input.Message,
			AttachmentIDs:    input.AttachmentIDs,
		})
		if err != nil {
			toolError(err.Error())
			return
		}
		respond(map[string]any{
			"content": []map[string]any{{"type": "text", "text": "Reply queued for the channel."}},
		})
	case "channel_reply_decision":
		var input struct {
			Reason           string `json:"reason"`
			InboundReceiptID string `json:"inbound_receipt_id"`
		}
		if err := json.Unmarshal(call.Arguments, &input); err != nil {
			toolError("invalid channel_reply_decision arguments")
			return
		}
		receiptID, err := s.channelMCPReceiptID(request, claims, input.InboundReceiptID)
		if err != nil {
			toolError("this Turn has no routed channel message to resolve")
			return
		}
		if err := s.app.SetAgentChannelReplyDecision(request.Context(), claims, "end", receiptID, input.Reason); err != nil {
			toolError(err.Error())
			return
		}
		respond(map[string]any{
			"content": []map[string]any{{"type": "text", "text": "No channel reply will be sent for this message."}},
		})
	default:
		writeJSON(writer, http.StatusOK, map[string]any{
			"jsonrpc": "2.0", "id": payload.ID,
			"error": map[string]any{"code": -32602, "message": "unknown tool"},
		})
	}
}

func (s *Server) channelMCPReceiptID(request *http.Request, claims agentapi.Claims, requested string) (string, error) {
	if receiptID := strings.TrimSpace(requested); receiptID != "" {
		return receiptID, nil
	}
	return s.app.AgentChannelReceiptID(request.Context(), claims)
}

// channelMCPRequestID is stable for a retry of the same reply and distinct for
// a different message, so EnqueueAgentChannelReply stays idempotent without
// collapsing two intentional replies into one.
func channelMCPRequestID(turnID, receiptID, message string) string {
	sum := sha256.Sum256([]byte(turnID + "\x00" + receiptID + "\x00" + message))
	return "mcp-" + hex.EncodeToString(sum[:12])
}
