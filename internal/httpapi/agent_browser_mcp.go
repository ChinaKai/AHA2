package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/browser"
)

// agentBrowserMCPMaxRequestBytes bounds one JSON-RPC request. The read-only
// first slice only carries opaque grant and tab identifiers, never page data.
const agentBrowserMCPMaxRequestBytes int64 = 1 << 20

// The first slice caps model-visible page data so one snapshot cannot flood a
// context window. Both caps are reported back as explicit truncation flags.
const (
	agentBrowserMCPMaxTextRunes = 16000
	agentBrowserMCPMaxElements  = 200
)

// agentBrowserMCP is the HTTP MCP facade for the read-only shared-browser
// domain. Every method — including initialize and tools/list — is mounted
// behind withAgentCapability and revalidates AgentCallContext plus the domain
// gate, so tool visibility is never authorization. The domain exists only while
// the Task holds an active browser grant, and never for the pet Task or a
// channel-hosted Turn.
func (s *Server) agentBrowserMCP(writer http.ResponseWriter, request *http.Request) {
	if agentCoreMCPForwarded(request) {
		writeError(writer, http.StatusForbidden, "agent_browser_mcp_forwarded")
		return
	}
	claims, ok := agentClaimsFromContext(request.Context())
	if !ok {
		writeError(writer, http.StatusUnauthorized, "agent_capability_required")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, agentBrowserMCPMaxRequestBytes))
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
	if !s.app.AgentBrowserMCPAllowed(request.Context(), call) {
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
			"serverInfo":      map[string]any{"name": "aha2-browser", "version": "1.0.0"},
		})
	case "notifications/initialized", "notifications/cancelled", "notifications/progress":
		writer.WriteHeader(http.StatusAccepted)
	case "ping":
		respond(map[string]any{})
	case "tools/list":
		respond(map[string]any{"tools": agentBrowserMCPTools()})
	case "tools/call":
		s.callAgentBrowserMCPTool(writer, request, call, payload)
	default:
		if strings.HasPrefix(payload.Method, "notifications/") {
			writer.WriteHeader(http.StatusAccepted)
			return
		}
		respondError(-32601, "method not found")
	}
}

func (s *Server) callAgentBrowserMCPTool(writer http.ResponseWriter, request *http.Request, call app.AgentCallContext, payload mcpRequest) {
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
		// Tool text is model-visible. Host diagnostics and store errors stay in
		// the server log; the model receives only the stable classification and
		// a public message that never carries page content.
		code, message := browserMCPErrorCode(err)
		// The raw error can carry page text through a host diagnostic. Only the
		// fixed classification and the tool name are logged.
		log.Printf("agent browser mcp tool %s failed: %s", input.Name, code)
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
	if !app.BrowserMCPToolIsRead(input.Name) {
		writeJSON(writer, http.StatusOK, map[string]any{
			"jsonrpc": "2.0", "id": payload.ID,
			"error": map[string]any{"code": -32602, "message": "unknown tool"},
		})
		return
	}
	auditResult := "ok"
	started := time.Now()
	// Only irreversible grant/tab identifiers, never page data, join the audit
	// row. The values are recorded after argument validation below.
	auditIDs := map[string]any{}
	defer func() {
		data := map[string]any{
			"tool": input.Name, "turn_id": call.Turn.ID, "result": auditResult,
			"duration_ms": time.Since(started).Milliseconds(), "argument_keys": coreMCPArgumentKeys(input.Arguments),
		}
		for key, value := range auditIDs {
			data[key] = value
		}
		s.audit(request, "agent.browser_mcp.call", "task", call.Task.ID, data)
	}()

	switch input.Name {
	case "browser_status":
		if err := decodeCoreToolArguments(input.Arguments, &struct{}{}); err != nil {
			auditResult = "invalid"
			invalidToolError("browser_status takes no arguments")
			return
		}
		result, err := s.agentBrowserMCPStatus(request.Context(), call)
		if err != nil {
			auditResult = "error"
			fail(err)
			return
		}
		respondJSON(result)

	case "browser_tabs_list":
		var args struct {
			GrantID string `json:"grant_id"`
		}
		if err := decodeCoreToolArguments(input.Arguments, &args); err != nil {
			auditResult = "invalid"
			invalidToolError("browser_tabs_list requires an opaque grant_id")
			return
		}
		args.GrantID = strings.TrimSpace(args.GrantID)
		if !validBrowserMCPID(args.GrantID) {
			auditResult = "invalid"
			invalidToolError("browser_tabs_list requires an opaque grant_id")
			return
		}
		tabs, verifiedGrant, err := s.agentBrowserMCPTabs(request.Context(), call, args.GrantID)
		if err != nil {
			auditResult = "error"
			fail(err)
			return
		}
		// Only the canonical server-side grant ID reaches the audit row. A
		// failed or unverified identifier never does.
		auditIDs["grant_id"] = verifiedGrant
		respondJSON(map[string]any{"tabs": tabs, "total": len(tabs)})

	case "browser_snapshot":
		var args struct {
			GrantID string `json:"grant_id"`
			TabID   string `json:"tab_id"`
		}
		if err := decodeCoreToolArguments(input.Arguments, &args); err != nil {
			auditResult = "invalid"
			invalidToolError("browser_snapshot requires an opaque grant_id and tab_id")
			return
		}
		args.GrantID = strings.TrimSpace(args.GrantID)
		args.TabID = strings.TrimSpace(args.TabID)
		if !validBrowserMCPID(args.GrantID) || !validBrowserMCPID(args.TabID) {
			auditResult = "invalid"
			invalidToolError("browser_snapshot requires an opaque grant_id and tab_id")
			return
		}
		snapshot, verifiedGrant, err := s.agentBrowserMCPSnapshot(request.Context(), request, call, args.GrantID, args.TabID)
		if err != nil {
			auditResult = "error"
			fail(err)
			return
		}
		auditIDs["grant_id"] = verifiedGrant
		auditIDs["tab_id"] = args.TabID
		respondJSON(snapshot)

	default:
		auditResult = "invalid"
		writeJSON(writer, http.StatusOK, map[string]any{
			"jsonrpc": "2.0", "id": payload.ID,
			"error": map[string]any{"code": -32602, "message": "unknown tool"},
		})
	}
}

// agentBrowserMCPLease is the non-sensitive lease shape. Profile, session,
// host, URL and page fields are deliberately absent.
type agentBrowserMCPLease struct {
	GrantID      string     `json:"grant_id"`
	Status       string     `json:"status"`
	Capabilities []string   `json:"capabilities"`
	TabCount     int        `json:"tab_count"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
}

func (s *Server) agentBrowserMCPStatus(ctx context.Context, call app.AgentCallContext) (map[string]any, error) {
	if s.browser == nil {
		return nil, newUnavailable()
	}
	state, err := s.browser.AgentBrowserState(ctx, call.Task.ID)
	if err != nil {
		return nil, err
	}
	leases := make([]agentBrowserMCPLease, 0, len(state.Grants))
	for _, grant := range state.Grants {
		lease := agentBrowserMCPLease{
			GrantID: grant.ID, Status: grant.Status, Capabilities: grant.Capabilities,
			TabCount: len(grant.Tabs),
		}
		if !grant.ExpiresAt.IsZero() {
			expiresAt := grant.ExpiresAt
			lease.ExpiresAt = &expiresAt
		}
		leases = append(leases, lease)
	}
	return map[string]any{"leases": leases, "total": len(leases)}, nil
}

// agentBrowserMCPTab is the sanitized tab shape. The URL keeps only
// scheme://host[:port]/path; userinfo, query and fragment are dropped. Site is
// the same origin, so a model can reason about a page without receiving its
// parameters.
type agentBrowserMCPTab struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Site    string `json:"site"`
	URL     string `json:"url"`
	Active  bool   `json:"active"`
	Focused bool   `json:"focused"`
}

// sanitizeBrowserMCPURL strips everything that can carry a token or a session
// identifier. A URL that is not an ordinary http(s) page is reported as empty
// rather than echoed back.
func sanitizeBrowserMCPURL(raw string) (sanitized, site string) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return "", ""
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", ""
	}
	site = parsed.Scheme + "://" + parsed.Host
	sanitized = site + parsed.EscapedPath()
	return sanitized, site
}

// validBrowserMCPID enforces the same 128-character bound the tool schemas
// advertise. Opaque grant and tab IDs are short server-generated tokens; a
// longer or empty value is rejected before any store or host lookup.
func validBrowserMCPID(value string) bool {
	if value == "" {
		return false
	}
	return utf8.RuneCountInString(value) <= 128
}

func (s *Server) agentBrowserMCPTabs(ctx context.Context, call app.AgentCallContext, grantID string) ([]agentBrowserMCPTab, string, error) {
	service, grant, err := s.agentBrowserMCPGrant(ctx, call.Task.ID, grantID)
	if err != nil {
		return nil, "", err
	}
	tabs, err := service.Tabs(ctx, grant.ID)
	if err != nil {
		return nil, "", err
	}
	result := make([]agentBrowserMCPTab, 0, len(tabs))
	for _, tab := range tabs {
		sanitized, site := sanitizeBrowserMCPURL(tab.URL)
		result = append(result, agentBrowserMCPTab{
			ID: tab.ID, Title: tab.Title, Site: site, URL: sanitized,
			Active: tab.Active, Focused: tab.Focused,
		})
	}
	return result, grant.ID, nil
}

// agentBrowserMCPElement is the structured element shape. Actions, input
// values, HTML and host handles are deliberately absent.
type agentBrowserMCPElement struct {
	Ref    string             `json:"ref"`
	Role   string             `json:"role"`
	Name   string             `json:"name"`
	Bounds browser.HostBounds `json:"bounds"`
}

// agentBrowserMCPSnapshot is the bounded page view. Text and element names are
// untrusted task data: they are returned to the caller and never persisted,
// audited or placed into an AHA prompt.
type agentBrowserMCPSnapshot struct {
	Text              string                   `json:"text"`
	TextTruncated     bool                     `json:"text_truncated"`
	Elements          []agentBrowserMCPElement `json:"elements"`
	ElementsTruncated bool                     `json:"elements_truncated"`
}

func (s *Server) agentBrowserMCPSnapshot(ctx context.Context, request *http.Request, call app.AgentCallContext, grantID, tabID string) (agentBrowserMCPSnapshot, string, error) {
	service, grant, err := s.agentBrowserMCPGrant(ctx, call.Task.ID, grantID)
	if err != nil {
		return agentBrowserMCPSnapshot{}, "", err
	}
	snapshot, err := service.SnapshotTab(ctx, browserActor(request), grant.ID, tabID)
	if err != nil {
		return agentBrowserMCPSnapshot{}, "", err
	}
	text, textTruncated := truncateBrowserMCPText(snapshot.Text)
	elements, elementsTruncated := truncateBrowserMCPElements(snapshot.Elements)
	return agentBrowserMCPSnapshot{
		Text: text, TextTruncated: textTruncated,
		Elements: elements, ElementsTruncated: elementsTruncated,
	}, grant.ID, nil
}

func truncateBrowserMCPText(raw string) (string, bool) {
	runes := []rune(raw)
	if len(runes) <= agentBrowserMCPMaxTextRunes {
		return raw, false
	}
	return string(runes[:agentBrowserMCPMaxTextRunes]), true
}

func truncateBrowserMCPElements(items []browser.HostElement) ([]agentBrowserMCPElement, bool) {
	truncated := len(items) > agentBrowserMCPMaxElements
	if truncated {
		items = items[:agentBrowserMCPMaxElements]
	}
	result := make([]agentBrowserMCPElement, 0, len(items))
	for _, item := range items {
		result = append(result, agentBrowserMCPElement{
			Ref: item.Ref, Role: item.Role, Name: item.Name, Bounds: item.Bounds,
		})
	}
	return result, truncated
}

// agentBrowserMCPGrant reuses the HTTP Agent API authorization rules before any
// host call: a grant from another Task, a revoked grant and an expired grant
// all fail closed with the same classification as the HTTP endpoint.
func (s *Server) agentBrowserMCPGrant(ctx context.Context, taskID, grantID string) (*browser.Service, browser.GrantView, error) {
	if s.browser == nil {
		return nil, browser.GrantView{}, newUnavailable()
	}
	grant, err := s.browser.Grant(ctx, grantID)
	if err != nil {
		return nil, browser.GrantView{}, err
	}
	if accessErr := agentBrowserGrantAccessError(grant, taskID); accessErr != nil {
		return nil, browser.GrantView{}, accessErr
	}
	return s.browser, grant, nil
}

// browserMCPErrorCode produces a stable classification plus a public message.
// The message is browser.PublicMessage, which already hides host diagnostics;
// page text, element names, titles and URLs never enter an error.
func browserMCPErrorCode(err error) (string, string) {
	var typed *browser.Error
	if errors.As(err, &typed) {
		code := strings.TrimSpace(typed.Code)
		if code == "" {
			code = "browser_operation_failed"
		}
		return code, browser.PublicMessage(typed)
	}
	return "browser_operation_failed", "browser operation failed"
}

func agentBrowserMCPTools() []mcpTool {
	object := func(properties map[string]any, required ...string) map[string]any {
		if required == nil {
			required = []string{}
		}
		return map[string]any{
			"type": "object", "properties": properties,
			"required": required, "additionalProperties": false,
		}
	}
	grantID := map[string]any{
		"type": "string", "minLength": 1, "maxLength": 128,
		"description": "Opaque grant ID from browser_status.",
	}
	return []mcpTool{
		{
			Name: "browser_status",
			Description: "Read the shared-browser leases held by this Task. Returns only the lease status, " +
				"opaque grant ID, capability list, tab count and expiry. Profile paths, cookies, CDP " +
				"addresses and credentials are never returned. Available to every Agent.",
			InputSchema: object(map[string]any{}),
		},
		{
			Name: "browser_tabs_list",
			Description: "List the tabs under one active grant. Each row carries an opaque tab ID, title, " +
				"origin and a URL reduced to scheme://host[:port]/path; userinfo, query and fragment are " +
				"removed. Page text and input values are never returned.",
			InputSchema: object(map[string]any{"grant_id": grantID}, "grant_id"),
		},
		{
			Name: "browser_snapshot",
			Description: "Read one authorized tab's readable text and at most 200 structured elements " +
				"(ref/role/name/bounds). Text is truncated to 16000 characters with an explicit flag. " +
				"Actions, input values, HTML source, cookies and CDP handles are never returned.",
			InputSchema: object(map[string]any{
				"grant_id": grantID,
				"tab_id": map[string]any{
					"type": "string", "minLength": 1, "maxLength": 128,
					"description": "Opaque tab ID from browser_tabs_list.",
				},
			}, "grant_id", "tab_id"),
		},
	}
}
