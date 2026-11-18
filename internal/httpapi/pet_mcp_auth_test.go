package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
)

type petMCPAuthResult struct {
	Status int
	Error  string
}

// callPetMCPWithHeaders posts one JSON-RPC request through the real route and
// returns the HTTP status plus the stable error code AHA writes for rejections.
func callPetMCPWithHeaders(t *testing.T, server *Server, remoteAddr, token string, headers map[string]string, body string) petMCPAuthResult {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/pet/mcp", strings.NewReader(body))
	request.RemoteAddr = remoteAddr
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	var response struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(recorder.Body.Bytes(), &response)
	return petMCPAuthResult{Status: recorder.Code, Error: response.Error}
}

// TestPetMCPRequiresPetTaskCapability pins the identity rule: a read-only tool
// is not reachable anonymously, with a malformed token, or with another Task's
// valid capability. Only the pet Task's own capability passes.
func TestPetMCPRequiresPetTaskCapability(t *testing.T) {
	server, capabilities, petToken := newPetMCPAuthServer(t)
	foreignToken, err := capabilities.Issue("task_other", "main", "turn-other", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	readOnlyCall := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"aha_pet_work_state","arguments":{}}}`

	for _, scenario := range []struct {
		name       string
		token      string
		wantStatus int
		wantError  string
	}{
		{name: "anonymous read-only", token: "", wantStatus: http.StatusUnauthorized, wantError: "agent_capability_required"},
		{name: "malformed token", token: "not-a-real-token", wantStatus: http.StatusUnauthorized, wantError: "agent_capability_invalid"},
		{name: "foreign task capability", token: foreignToken, wantStatus: http.StatusForbidden, wantError: app.ErrCodePetIdentityRequired},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			result := callPetMCPWithHeaders(t, server, "127.0.0.1:12345", scenario.token, nil, readOnlyCall)
			if result.Status != scenario.wantStatus || result.Error != scenario.wantError {
				t.Fatalf("status=%d error=%q, want %d %q", result.Status, result.Error, scenario.wantStatus, scenario.wantError)
			}
		})
	}

	result := callPetMCPWithHeaders(t, server, "127.0.0.1:12345", petToken, nil,
		`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	if result.Status != http.StatusOK {
		t.Fatalf("pet capability rejected: status=%d error=%q", result.Status, result.Error)
	}
}

// TestPetMCPRejectsForwardedHeaders keeps a same-host proxy from laundering a
// LAN or tunnelled request into the loopback-only surface.
func TestPetMCPRejectsForwardedHeaders(t *testing.T) {
	server, _, petToken := newPetMCPAuthServer(t)
	for _, header := range []string{"Forwarded", "X-Forwarded-For", "X-Real-IP"} {
		t.Run(header, func(t *testing.T) {
			result := callPetMCPWithHeaders(t, server, "127.0.0.1:12345", petToken,
				map[string]string{header: "203.0.113.9"},
				`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
			if result.Status != http.StatusForbidden || result.Error != "pet_mcp_local_only" {
				t.Fatalf("status=%d error=%q, want 403 pet_mcp_local_only", result.Status, result.Error)
			}
		})
	}
}

func TestPetMCPRejectsNonLoopbackSource(t *testing.T) {
	server, _, petToken := newPetMCPAuthServer(t)
	result := callPetMCPWithHeaders(t, server, "192.0.2.10:4242", petToken, nil,
		`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	if result.Status != http.StatusForbidden || result.Error != "pet_mcp_local_only" {
		t.Fatalf("status=%d error=%q, want 403 pet_mcp_local_only", result.Status, result.Error)
	}
}
