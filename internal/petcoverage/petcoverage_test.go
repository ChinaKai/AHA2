package petcoverage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for path, content := range files {
		target := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func classify(t *testing.T, entries map[string]string) {
	t.Helper()
	saved := registeringFunctions
	registeringFunctions = map[string]string{}
	for key, value := range entries {
		registeringFunctions[key] = value
	}
	t.Cleanup(func() { registeringFunctions = saved })
}

const serverPreamble = `package api

import "net/http"

type Server struct{}

func (s *Server) withAuth(next http.Handler) http.Handler { return next }
func (s *Server) withAgentCapability(next http.Handler) http.Handler { return next }
func (s *Server) list(w http.ResponseWriter, r *http.Request) { s.withAuth(nil) }
func (s *Server) status(w http.ResponseWriter, r *http.Request) { s.list(w, r) }
func (s *Server) observe(w http.ResponseWriter, r *http.Request) { s.list(w, r) }
func (s *Server) reject(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Allow", "GET")
	writeError(w, http.StatusMethodNotAllowed, "read_only")
}
func writeError(w http.ResponseWriter, status int, code string) {}
`

func TestScanFindsEveryRegistrationShape(t *testing.T) {
	classify(t, map[string]string{"internal/api (*Server).Routes": "httpapi", "internal/api (*Server).tables": "httpapi"})
	root := writeTree(t, map[string]string{
		"internal/api/server.go": serverPreamble + `
const waitRoute = "POST /api/v1/agent/wait"
const prefixConst = "/api/v1/" + "projects"

func (s *Server) Routes() http.Handler {
	router := http.NewServeMux()
	router.Handle("GET /api/v1/system", s.withAuth(http.HandlerFunc(s.list)))
	router.Handle(waitRoute, s.withAgentCapability(http.HandlerFunc(s.status)))
	router.HandleFunc("GET "+prefixConst+"/{id}", s.observe)
	router.Handle("/api/v1/prompts", s.withAuth(http.HandlerFunc(s.reject)))
	s.tables(router)
	return router
}

func (s *Server) tables(mux *http.ServeMux) {
	prefix := "/api/v1/tasks/{id}/desktop"
	for _, route := range []struct {
		pattern string
		handler http.HandlerFunc
	}{
		{"GET " + prefix, s.status},
		{pattern: "POST " + prefix + "/stop", handler: s.observe},
	} {
		mux.Handle(route.pattern, s.withAuth(route.handler))
	}
	for _, route := range []struct {
		pattern string
		handler http.HandlerFunc
	}{
		{"GET /api/v1/agent/desktop", s.status},
	} {
		mux.Handle(route.pattern, s.withAgentCapability(route.handler))
	}
}
`,
	})
	routes, err := ScanRoutes(root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Registration{}
	for _, route := range routes {
		got[route.Operation] = route
	}
	want := map[string]string{
		"GET /api/v1/system":                   "list owner_session",
		"POST /api/v1/agent/wait":              "status agent_turn",
		"GET /api/v1/projects/{id}":            "observe transport_auth",
		"ANY /api/v1/prompts":                  "reject owner_session",
		"GET /api/v1/tasks/{id}/desktop":       "status owner_session",
		"POST /api/v1/tasks/{id}/desktop/stop": "observe owner_session",
		"GET /api/v1/agent/desktop":            "status agent_turn",
	}
	if len(got) != len(want) {
		t.Fatalf("scanned %d routes, want %d: %v", len(got), len(want), got)
	}
	for operation, expected := range want {
		route, ok := got[operation]
		if !ok || route.Handler+" "+route.Boundary != expected || route.Fingerprint == "" || route.Source != "internal/api/server.go" {
			t.Fatalf("%s = %+v, want %s", operation, route, expected)
		}
	}
	if !got["ANY /api/v1/prompts"].RejectOnly || got["GET /api/v1/system"].RejectOnly {
		t.Fatal("reject-only detection is wrong")
	}
}

func TestScanFailsClosedOnRegistrationsItCannotAccountFor(t *testing.T) {
	cases := map[string][2]string{
		"unclassified function": {`func (s *Server) Other() { mux := http.NewServeMux(); mux.Handle("GET /x", http.HandlerFunc(s.list)) }`, "not classified"},
		"default serve mux":     {`func (s *Server) Routes() { http.Handle("GET /x", http.HandlerFunc(s.list)) }`, "unrecognized receiver"},
		"unknown receiver":      {`func (s *Server) Routes(r interface{ Handle(string, http.Handler) }) { r.Handle("/x", http.HandlerFunc(s.list)) }`, "unrecognized receiver"},
		"dynamic pattern":       {`func (s *Server) Routes(pattern string) { mux := http.NewServeMux(); mux.Handle(pattern, http.HandlerFunc(s.list)) }`, "unresolvable route pattern"},
		"duplicate":             {`func (s *Server) Routes() { mux := http.NewServeMux(); mux.Handle("GET /x", http.HandlerFunc(s.list)); mux.Handle("GET /x", http.HandlerFunc(s.status)) }`, "duplicate route"},
		"unknown handler":       {`func (s *Server) Routes() { mux := http.NewServeMux(); mux.Handle("GET /x", http.HandlerFunc(s.missing)) }`, "no unique declaration"},
	}
	for name, item := range cases {
		t.Run(name, func(t *testing.T) {
			classify(t, map[string]string{"internal/api (*Server).Routes": "httpapi"})
			root := writeTree(t, map[string]string{"internal/api/server.go": serverPreamble + item[0] + "\n"})
			if _, err := ScanRoutes(root); err == nil || !strings.Contains(err.Error(), item[1]) {
				t.Fatalf("scan error = %v, want %q", err, item[1])
			}
		})
	}
}

func TestMergeMarksChangedReviewedRowsStaleAndKeepsPending(t *testing.T) {
	routes := []Registration{
		{Operation: "GET /a", Server: "httpapi", Source: "internal/api/a.go", Handler: "a", Boundary: "owner_session", Fingerprint: "new"},
		{Operation: "GET /b", Server: "httpapi", Source: "internal/api/a.go", Handler: "b", Boundary: "owner_session", Fingerprint: "same"},
		{Operation: "GET /c", Server: "httpapi", Source: "internal/api/a.go", Handler: "c", Boundary: "owner_session", Fingerprint: "changed"},
		{Operation: "GET /new", Server: "httpapi", Source: "internal/api/a.go", Handler: "n", Boundary: "owner_session", Fingerprint: "n"},
	}
	previous := []Row{
		{"operation": "GET /a", "server": "httpapi", "source": "internal/api/a.go", "handler": "a", "boundary": "owner_session", "fingerprint": "old", "mcp": "aha2_pet.aha_overview", "permission": "p", "protected_flow": "none", "tests": "x_test.go:TestX", "status": "direct"},
		{"operation": "GET /b", "server": "httpapi", "source": "internal/api/a.go", "handler": "b", "boundary": "owner_session", "fingerprint": "same", "mcp": "m", "permission": "p", "protected_flow": "f", "tests": "t", "status": "direct"},
		{"operation": "GET /c", "server": "httpapi", "source": "internal/api/a.go", "handler": "c", "boundary": "owner_session", "fingerprint": "old", "mcp": "pending", "permission": "pending", "protected_flow": "pending", "tests": "pending", "status": "pending"},
		{"operation": "GET /gone", "server": "httpapi", "status": "direct"},
	}
	rows, stale, removed := MergeLedger(previous, routes)
	status := map[string]string{}
	for _, row := range rows {
		status[row["operation"]] = row["status"]
	}
	if status["GET /a"] != "stale" || rows[0]["mcp"] != "aha2_pet.aha_overview" || rows[0]["fingerprint"] != "new" {
		t.Fatal("a changed handler kept its old verdict or lost the reviewed text")
	}
	if status["GET /b"] != "direct" || status["GET /c"] != "pending" || status["GET /new"] != "pending" {
		t.Fatalf("unexpected merge statuses %v", status)
	}
	if len(stale) != 1 || len(removed) != 1 || removed[0] != "httpapi GET /gone" {
		t.Fatalf("stale=%v removed=%v", stale, removed)
	}
}

func TestEvidenceRulesRejectVagueOrMisappliedClosure(t *testing.T) {
	root := writeTree(t, map[string]string{"internal/api/a_test.go": "package api\n\nimport \"testing\"\n\nfunc TestReal(t *testing.T) {}\n"})
	catalog := NewCatalog(map[string][]string{"aha2_pet": {"aha_overview"}})
	owner := Registration{Operation: "GET /api/v1/system", Handler: "system", Boundary: "owner_session"}
	protocol := Registration{Operation: "POST /api/v1/agent/core/mcp", Handler: "agentCoreMCP", Boundary: "agent_turn"}
	reject := Registration{Operation: "ANY /api/v1/prompts", Handler: "reject", Boundary: "owner_session", RejectOnly: true}
	good := Row{"mcp": "aha2_pet.aha_overview", "permission": "view tasks", "protected_flow": "none", "tests": "internal/api/a_test.go:TestReal", "status": "direct"}
	if err := ValidateRow(root, catalog, owner, good); err != nil {
		t.Fatalf("valid direct row rejected: %v", err)
	}
	with := func(changes Row) Row {
		row := Row{}
		for key, value := range good {
			row[key] = value
		}
		for key, value := range changes {
			row[key] = value
		}
		return row
	}
	bad := map[string]struct {
		route Registration
		row   Row
	}{
		"prose tests":             {owner, with(Row{"tests": "existing HTTP server tests"})},
		"glob tests":              {owner, with(Row{"tests": "internal/api/*_test.go"})},
		"missing test":            {owner, with(Row{"tests": "internal/api/a_test.go:TestMissing"})},
		"missing tool":            {owner, with(Row{"mcp": "aha2_pet.aha_missing"})},
		"no tool named":           {owner, with(Row{"mcp": "reuses Web flow"})},
		"pending field":           {owner, with(Row{"permission": "pending"})},
		"empty field":             {owner, with(Row{"protected_flow": ""})},
		"transport on owner op":   {owner, with(Row{"mcp": "none", "status": "transport_na"})},
		"prohibited not rejected": {owner, with(Row{"mcp": "none", "status": "web_prohibited_na"})},
		"na with tool":            {protocol, with(Row{"status": "transport_na"})},
		"unknown status":          {owner, with(Row{"status": "owner_only"})},
		"open row bad reference":  {owner, with(Row{"status": "pending", "tests": "see suite"})},
	}
	for name, item := range bad {
		if err := ValidateRow(root, catalog, item.route, item.row); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	for name, item := range map[string]struct {
		route Registration
		row   Row
	}{
		"protocol transport": {protocol, with(Row{"mcp": "none", "status": "transport_na"})},
		"web rejects":        {reject, with(Row{"mcp": "none", "status": "web_prohibited_na"})},
		"open with pending":  {owner, Row{"mcp": "pending", "permission": "pending", "protected_flow": "pending", "tests": "pending", "status": "stale"}},
	} {
		if err := ValidateRow(root, catalog, item.route, item.row); err != nil {
			t.Fatalf("%s rejected: %v", name, err)
		}
	}
}

func TestWebSitesMustMatchARegisteredRoute(t *testing.T) {
	routes := []Registration{
		{Operation: "GET /api/v1/projects", Server: "httpapi"},
		{Operation: "PATCH /api/v1/tasks/{id}", Server: "httpapi"},
		{Operation: "GET /api/v1/knowledge-vault/git/status", Server: "httpapi"},
		{Operation: "GET /api/v1/other", Server: "centersync"},
	}
	sites := []WebSite{
		{Source: "a.ts", Line: 1, MethodHint: "GET/see-source", Expression: "/api/v1/projects${query.size ? `?${query}` : \"\"}"},
		{Source: "a.ts", Line: 2, MethodHint: "PATCH", Expression: "/api/v1/tasks/${encodeURIComponent(id)}"},
		{Source: "a.ts", Line: 3, MethodHint: "GET/see-source", Expression: "/api/v1/knowledge-vault/git/${action}"},
		{Source: "a.ts", Line: 4, MethodHint: "DELETE", Expression: "/api/v1/tasks/${id}"},
		{Source: "a.ts", Line: 5, MethodHint: "GET/see-source", Expression: "/api/v1/unregistered"},
		{Source: "a.ts", Line: 6, MethodHint: "GET/see-source", Expression: "/api/v1/other"},
	}
	unmatched, err := UnmatchedWebSites(sites, routes)
	if err != nil {
		t.Fatal(err)
	}
	var lines []int
	for _, site := range unmatched {
		lines = append(lines, site.Line)
	}
	if len(lines) != 3 || lines[0] != 4 || lines[1] != 5 || lines[2] != 6 {
		t.Fatalf("unmatched lines = %v, want [4 5 6]", lines)
	}
}

func TestScanWebKeepsNestedTemplatesAndMethodHints(t *testing.T) {
	root := writeTree(t, map[string]string{"web/src/api.ts": "return this.request(`/api/v1/tasks/${encodeURIComponent(id)}`, {method: \"PATCH\", body: \"{}\"});\nfetch(\"/api/v1/system\");\nreturn this.request(`/api/v1/projects${query.size ? `?${query}` : \"\"}`);\n"})
	sites, err := ScanWeb(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 3 || sites[0].MethodHint != "PATCH" || sites[1].Line != 2 || sites[2].Expression != "/api/v1/projects${query.size ? `?${query}` : \"\"}" {
		t.Fatalf("sites = %+v", sites)
	}
}

func TestCheckDetectsDriftAndCompleteRequiresClosure(t *testing.T) {
	classify(t, map[string]string{"internal/api (*Server).Routes": "httpapi"})
	root := writeTree(t, map[string]string{
		"internal/api/server.go": serverPreamble + "func (s *Server) Routes() { mux := http.NewServeMux(); mux.Handle(\"GET /api/v1/system\", s.withAuth(http.HandlerFunc(s.list))) }\n",
		"web/src/api.ts":         "fetch(\"/api/v1/system\");\n",
		"docs/pet-mcp/.keep":     "",
	})
	catalog := NewCatalog(map[string][]string{"aha2_pet": {"aha_overview"}, "aha2_core": {"progress"}})
	if _, err := Check(root, catalog, false, false); err == nil || !strings.Contains(err.Error(), "differs from the scan") {
		t.Fatalf("missing ledger was not reported as drift: %v", err)
	}
	report, err := Check(root, catalog, true, false)
	if err != nil || report.Operations != 1 || report.Open != 1 || report.Tools != 1 {
		t.Fatalf("update failed: %+v %v", report, err)
	}
	if _, err := Check(root, catalog, false, false); err != nil {
		t.Fatalf("freshly updated ledger did not pass the drift check: %v", err)
	}
	if _, err := Check(root, catalog, false, true); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("open rows passed the complete gate: %v", err)
	}
	// A handler edit invalidates the stored fingerprint.
	path := filepath.Join(root, "internal/api/server.go")
	data, _ := os.ReadFile(path)
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), "s.withAuth(nil) }", "s.withAuth(nil); _ = r }", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Check(root, catalog, false, false); err == nil {
		t.Fatal("handler change did not invalidate the ledger")
	}
}
