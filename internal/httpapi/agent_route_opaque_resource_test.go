package httpapi

import (
	"database/sql"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/browser"
	"github.com/ChinaKai/AHA2/internal/browser/browsertest"
	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestAgentRouteLongProcessFixture(t *testing.T) {
	if os.Getenv("AHA_MCP_ROUTE_PROCESS_FIXTURE") == "1" {
		<-time.After(5 * time.Minute)
	}
}

func TestOpaqueMCPResourceNamesCannotSkipLiveAuthorization(t *testing.T) {
	h := newCoreMCPHarness(t)
	base := createHardwareAPITask(t, h.database)
	task := createPetDomainHTTPTask(t, h, base)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if task.Status == domain.TaskDraft {
		if err := h.database.UpdateTaskStatus(h.ctx, task.ID, domain.TaskDraft, domain.TaskPreparing, now, ""); err != nil {
			t.Fatal(err)
		}
		if err := h.database.UpdateTaskStatus(h.ctx, task.ID, domain.TaskPreparing, domain.TaskActive, now, ""); err != nil {
			t.Fatal(err)
		}
	}
	turn := createCoreTurn(t, h.database, task, "main", domain.NewID("inbox_batch"))
	token := h.issue(task.ID, "main", turn.ID)
	host := browsertest.New()
	service := browser.NewService(h.database, host, "local")
	server := New(Config{Store: h.database, App: h.service, Auth: auth.NewService(h.database, "setup-test", time.Hour), AgentCapabilities: h.caps, ManagedProcesses: h.processes, Browser: service})
	actual := httptest.NewServer(server.Handler())
	t.Cleanup(actual.Close)
	jar, _ := cookiejar.New(nil)
	owner := &http.Client{Jar: jar}
	csrf := registerOwner(t, owner, actual.URL)
	response := requestJSON(t, owner, http.MethodPost, actual.URL+"/api/v1/browser/profiles", map[string]any{"name": "Opaque route fixture"}, csrf)
	var profile struct {
		Profile struct {
			ID string `json:"id"`
		} `json:"profile"`
	}
	decodeResponse(t, response, &profile)
	if response.StatusCode != http.StatusCreated {
		t.Fatal("owner fixture profile failed")
	}
	response = requestJSON(t, owner, http.MethodPost, actual.URL+"/api/v1/tasks/"+task.ID+"/browser-grants", map[string]any{"profile_id": profile.Profile.ID}, csrf)
	var granted struct {
		Grant struct {
			ID string `json:"id"`
		} `json:"grant"`
	}
	decodeResponse(t, response, &granted)
	if response.StatusCode != http.StatusCreated {
		t.Fatal("owner fixture grant failed")
	}
	// Use a real service-created session and grant. Only its opaque identifier
	// is changed in the isolated fixture to exercise the colliding route leaf.
	raw, err := sql.Open("sqlite", h.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`UPDATE browser_task_grants SET id='mcp' WHERE id=?`, granted.Grant.ID); err != nil {
		t.Fatal("opaque grant identifier fixture failed")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	response = agentRequest(t, actual.URL+"/api/v1/agent/processes", http.MethodPost, token, map[string]any{"name": "mcp", "executable": executable, "args": []string{"-test.run=^TestAgentRouteLongProcessFixture$"}, "env": map[string]string{"AHA_MCP_ROUTE_PROCESS_FIXTURE": "1"}})
	if response.StatusCode != http.StatusAccepted {
		response.Body.Close()
		t.Fatal("authorized process fixture failed")
	}
	response.Body.Close()
	beforeProcess, err := h.processes.Status(task.ID, "mcp")
	if err != nil || beforeProcess.State != "running" {
		t.Fatal("real managed process is not running")
	}
	var handlerCalls atomic.Int32
	observe := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) { handlerCalls.Add(1); next(w, r) }
	}
	// This second real HTTP mux instruments dispatch after the same production
	// authenticator, and delegates to the complete production handlers. The
	// actual Server.Handler is exercised for every attack path as well.
	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/agent/processes/{name}", server.withAgentCapability(observe(server.agentProcessStatus)))
	mux.Handle("DELETE /api/v1/agent/browser/grants/{grant}", server.withAgentCapability(observe(server.agentBrowserRelease)))
	observed := httptest.NewServer(mux)
	t.Cleanup(observed.Close)
	status := func(url, method string) int {
		t.Helper()
		response := agentRequest(t, url, method, token, nil)
		defer response.Body.Close()
		return response.StatusCode
	}
	if status(actual.URL+"/api/v1/agent/processes/mcp", http.MethodGet) != 200 || status(observed.URL+"/api/v1/agent/processes/mcp", http.MethodGet) != 200 || handlerCalls.Load() != 1 {
		t.Fatal("authorized opaque process route regressed")
	}
	if s, _, _ := coreMCPToolsList(t, actual.URL+"/api/v1/agent/core/mcp", token); s != 200 {
		t.Fatal("real MCP endpoint regressed before revocation")
	}
	beforeGrant, err := h.database.BrowserTaskGrant(h.ctx, "mcp")
	if err != nil || !beforeGrant.RevokedAt.IsZero() {
		t.Fatal("real grant fixture is not active")
	}
	beforeFence, beforeClear := host.Fences[profile.Profile.ID], host.ClearDownload
	assistant, _ := h.database.EnsureOwnerAssistant(h.ctx, time.Now())
	if err := h.database.UpdateOwnerPetPermissions(h.ctx, assistant.ID, false, false, false, false, false, false, false, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	beforeCalls := handlerCalls.Load()
	for _, entry := range []struct{ method, path string }{{http.MethodGet, "/api/v1/agent/processes/mcp"}, {http.MethodGet, "/api/v1/agent/processes/a%2Fmcp"}, {http.MethodDelete, "/api/v1/agent/browser/grants/mcp"}} {
		for _, root := range []string{actual.URL, observed.URL} {
			if got := status(root+entry.path, entry.method); got != http.StatusForbidden {
				t.Fatalf("revoked opaque route %s %s returned %d", entry.method, entry.path, got)
			}
		}
	}
	afterProcess, err := h.processes.Status(task.ID, "mcp")
	if err != nil || !reflect.DeepEqual(beforeProcess, afterProcess) {
		t.Fatal("denied requests changed the managed process")
	}
	afterGrant, err := h.database.BrowserTaskGrant(h.ctx, "mcp")
	if err != nil || !reflect.DeepEqual(beforeGrant, afterGrant) || host.Fences[profile.Profile.ID] != beforeFence || host.ClearDownload != beforeClear || handlerCalls.Load() != beforeCalls {
		t.Fatal("denied requests reached a handler or changed browser authorization")
	}
	if s, _, _ := coreMCPToolsList(t, actual.URL+"/api/v1/agent/core/mcp", token); s != 200 {
		t.Fatal("exact MCP entry was confused with a resource suffix")
	}
	if err := h.database.UpdateOwnerPetPermissions(h.ctx, assistant.ID, true, true, false, false, false, false, false, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	if status(actual.URL+"/api/v1/agent/processes/mcp", http.MethodGet) != 200 {
		t.Fatal("authorization restoration did not restore legal process access")
	}
	if status(actual.URL+"/api/v1/agent/browser/grants/mcp", http.MethodDelete) != 200 {
		t.Fatal("legal opaque grant release regressed")
	}
	released, err := h.database.BrowserTaskGrant(h.ctx, "mcp")
	if err != nil || released.RevokedAt.IsZero() || host.Fences[profile.Profile.ID] != "released:mcp" || host.ClearDownload <= beforeClear {
		t.Fatal("authorized release did not produce its real business effects")
	}
}

func TestUnclassifiedTokenRoutesFailBeforeEitherAuthWrapperDispatches(t *testing.T) {
	h := newCoreMCPHarness(t)
	task := createHardwareAPITask(t, h.database)
	turn := createCoreTurn(t, h.database, task, "main", domain.NewID("inbox_batch"))
	token := h.issue(task.ID, "main", turn.ID)
	server := New(Config{Store: h.database, App: h.service, AgentCapabilities: h.caps})
	var calls atomic.Int32
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(http.StatusNoContent) })
	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/agent/unclassified/{id}", server.withAgentCapability(next))
	mux.Handle("GET /api/v1/unclassified-dual/{id}", server.withOwnerOrAgentAuth(next))
	actual := httptest.NewServer(mux)
	defer actual.Close()
	for _, path := range []string{"/api/v1/agent/unclassified/mcp", "/api/v1/agent/unclassified/a%2Fmcp", "/api/v1/unclassified-dual/mcp"} {
		response := agentRequest(t, actual.URL+path, http.MethodGet, token, nil)
		code := response.StatusCode
		body := readBody(t, response)
		response.Body.Close()
		if code != 403 || !strings.Contains(body, "agent_route_policy_missing") {
			t.Fatal("unclassified token route was not closed")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("unknown policy reached the handler")
	}
}
