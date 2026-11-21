package petcoverage

import (
	"net/http"
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
	routes, stats, err := ScanRoutes(root)
	if err != nil {
		t.Fatal(err)
	}
	// 7 routes from 6 Handle calls (each table call counts once); every call is accounted.
	if stats.Calls != 6 || stats.Registering != 6 || len(stats.NonRoute) != 0 {
		t.Fatalf("stats = %+v", stats)
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
		"default serve mux":     {`func (s *Server) Routes() { http.Handle("GET /x", http.HandlerFunc(s.list)) }`, "DefaultServeMux"},
		"unknown receiver":      {`func (s *Server) Routes(r interface{ Handle(string, http.Handler) }) { r.Handle("/x", http.HandlerFunc(s.list)) }`, "unrecognized receiver"},
		"dynamic pattern":       {`func (s *Server) Routes(pattern string) { mux := http.NewServeMux(); mux.Handle(pattern, http.HandlerFunc(s.list)) }`, "unresolvable route pattern"},
		"duplicate":             {`func (s *Server) Routes() { mux := http.NewServeMux(); mux.Handle("GET /x", http.HandlerFunc(s.list)); mux.Handle("GET /x", http.HandlerFunc(s.status)) }`, "duplicate route"},
		"unknown handler":       {`func (s *Server) Routes() { mux := http.NewServeMux(); mux.Handle("GET /x", http.HandlerFunc(s.missing)) }`, "no unique declaration"},
		"multi-line call":       {"func (s *Server) Routes(r interface{ Handle(string, http.Handler) }) {\n\tr.Handle(\n\t\t\"/x\",\n\t\thttp.HandlerFunc(s.list),\n\t)\n}", "unrecognized receiver"},
		"non-route lookalike":   {`func (s *Server) Routes(r interface{ Handle(int, int) }) { r.Handle(1, 2) }`, "unrecognized receiver"},
		"helper on field":       {`type holder struct{ mux *http.ServeMux }; func (s *Server) Routes(h holder) { h.mux.Handle("GET /x", http.HandlerFunc(s.list)) }`, "unrecognized receiver"},
		"package-level literal": {`var register = func(mux *http.ServeMux, s *Server) { mux.Handle("GET /x", http.HandlerFunc(s.list)) }`, "outside a function body"},
		"unknown wrapper":       {`func (s *Server) wrap(next http.Handler) http.Handler { return next }; func (s *Server) Routes() { mux := http.NewServeMux(); mux.Handle("GET /x", s.wrap(http.HandlerFunc(s.list))) }`, "no reviewed boundary"},
	}
	for name, item := range cases {
		t.Run(name, func(t *testing.T) {
			classify(t, map[string]string{"internal/api (*Server).Routes": "httpapi"})
			root := writeTree(t, map[string]string{"internal/api/server.go": serverPreamble + item[0] + "\n"})
			if _, _, err := ScanRoutes(root); err == nil || !strings.Contains(err.Error(), item[1]) {
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

func TestScanCountsNonRouteHandleCalls(t *testing.T) {
	classify(t, map[string]string{"internal/api (*Server).Routes": "httpapi"})
	root := writeTree(t, map[string]string{"internal/api/server.go": serverPreamble + `
type conv int

func Handle(value int) conv { return conv(value) }

// mux.Handle("GET /commented", s.list) is not a call.
var note = "mux.Handle(\"GET /string\", s.list)"

func (s *Server) Routes() {
	mux := http.NewServeMux()
	_ = api.Handle(1)
	mux.Handle("GET /x", http.HandlerFunc(s.list))
}

var api = struct{ Handle func(int) conv }{Handle}
`})
	routes, stats, err := ScanRoutes(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 || stats.Calls != 2 || stats.Registering != 1 || len(stats.NonRoute) != 1 || !strings.Contains(stats.NonRoute[0], "1 arguments") {
		t.Fatalf("routes=%d stats=%+v", len(routes), stats)
	}
}

func TestWebSitesMustMatchARegisteredRoute(t *testing.T) {
	routes := []Registration{
		{Operation: "GET /api/v1/projects", Server: "httpapi"},
		{Operation: "PATCH /api/v1/tasks/{id}", Server: "httpapi"},
		{Operation: "GET /api/v1/knowledge-vault/git/status", Server: "httpapi"},
		{Operation: "GET /api/v1/tasks/{id}/hardware/{hardware}/terminal/ws", Server: "httpapi"},
		{Operation: "GET /api/v1/other", Server: "centersync"},
	}
	sites := []WebSite{
		{Source: "a.ts", MethodHint: "GET/see-source", Expression: "/api/v1/projects${query.size ? `?${query}` : \"\"}"},
		{Source: "a.ts", MethodHint: "PATCH", Expression: "/api/v1/tasks/${encodeURIComponent(id)}"},
		{Source: "a.ts", MethodHint: "GET/see-source", Expression: "/api/v1/knowledge-vault/git/${action}"},
		{Source: "a.ts", MethodHint: "GET/see-source", Expression: "${protocol}//${location.host}/api/v1/tasks/${id}/hardware/${hw}/terminal/ws?${query}"},
		{Source: "b.ts", MethodHint: "DELETE", Expression: "/api/v1/tasks/${id}"},
		{Source: "b.ts", MethodHint: "GET/see-source", Expression: "/api/v1/unregistered"},
		{Source: "b.ts", MethodHint: "GET/see-source", Expression: "/api/v1/other"},
	}
	unmatched, err := UnmatchedWebSites(sites, routes)
	if err != nil {
		t.Fatal(err)
	}
	if len(unmatched) != 3 {
		t.Fatalf("unmatched = %+v, want the three b.ts sites", unmatched)
	}
	for _, site := range unmatched {
		if site.Source != "b.ts" {
			t.Fatalf("matched site reported: %+v", site)
		}
	}
}

func TestScanWebLexesCommentsRegexpsTemplatesAndDynamicBases(t *testing.T) {
	root := writeTree(t, map[string]string{"web/src/api.ts": strings.Join([]string{
		"return this.request(`/api/v1/tasks/${encodeURIComponent(id)}`, {method: \"PATCH\", body: \"{}\"});",
		"fetch(\"/api/v1/system\");",
		"fetch(\"/api/v1/system\");",
		"return this.request(`/api/v1/projects${query.size ? `?${query}` : \"\"}`);",
		"// fetch(\"/api/v1/commented-out\");",
		"/* fetch('/api/v1/block-comment'); */",
		"const pattern = /[\"']\\/api\\//g; const ratio = total / 2;",
		"const url = `${protocol}//${location.host}/api/v1/tasks/${id}/hardware/${hw}/terminal/ws?${query}`;",
		"const external = \"https://openrouter.ai/api/v1\";",
		"const nested = `${flag ? \"/api/v1/nested-a\" : '/api/v1/nested-b'}`;",
		"const link = \"http://example.test/x\"; fetch('/api/v1/after-url');",
		"state.innerHTML = `<img src=\"/api/v1/embedded/${encodeURIComponent(id)}/qr\" alt=\"x\">${flag ? \"/api/v1/inside-expr\" : \"\"}`;",
		"link.download = `${name.replace(/[\\\\/:*?\"<>|]+/g, \"-\")}-archive.json`;",
	}, "\n") + "\n"})
	sites, err := ScanWeb(root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]WebSite{}
	for _, site := range sites {
		got[site.Expression] = site
	}
	for _, expression := range []string{"/api/v1/commented-out", "/api/v1/block-comment", "https://openrouter.ai/api/v1"} {
		if _, ok := got[expression]; ok {
			t.Fatalf("%s should not be a call site", expression)
		}
	}
	want := map[string]string{
		"/api/v1/tasks/${encodeURIComponent(id)}": "PATCH",
		"/api/v1/system": "GET/see-source",
		"/api/v1/projects${query.size ? `?${query}` : \"\"}":                                   "GET/see-source",
		"${protocol}//${location.host}/api/v1/tasks/${id}/hardware/${hw}/terminal/ws?${query}": "GET/see-source",
		"/api/v1/nested-a":  "GET/see-source",
		"/api/v1/nested-b":  "GET/see-source",
		"/api/v1/after-url": "GET/see-source",
		"/api/v1/embedded/${encodeURIComponent(id)}/qr": "GET/see-source",
		"/api/v1/inside-expr":                           "GET/see-source",
	}
	if len(got) != len(want) {
		t.Fatalf("sites = %+v", sites)
	}
	for expression, hint := range want {
		if got[expression].MethodHint != hint {
			t.Fatalf("%s = %+v", expression, got[expression])
		}
	}
	if got["/api/v1/system"].Occurrences != 2 {
		t.Fatal("identical calls in one file were not folded")
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

func TestVerifyMuxRejectsScannedRoutesTheLiveMuxDoesNotServe(t *testing.T) {
	mux := http.NewServeMux()
	ok := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	mux.Handle("GET /api/v1/tasks/{id}", ok)
	mux.Handle("GET /api/v1/files/{path...}", ok)
	mux.Handle("/api/v1/prompts", ok)
	mux.Handle("GET /api/v1/prompts", ok)
	routes := []Registration{
		{Operation: "GET /api/v1/tasks/{id}", Server: "httpapi"},
		{Operation: "GET /api/v1/files/{path...}", Server: "httpapi"},
		{Operation: "ANY /api/v1/prompts", Server: "httpapi"},
		{Operation: "GET /api/v1/prompts", Server: "httpapi"},
		{Operation: "GET /elsewhere", Server: "centersync"},
	}
	if checked, err := VerifyMux(mux, routes, "httpapi"); err != nil || checked != 4 {
		t.Fatalf("checked=%d err=%v", checked, err)
	}
	routes = append(routes, Registration{Operation: "POST /api/v1/tasks/{id}", Server: "httpapi"}, Registration{Operation: "GET /api/v1/missing", Server: "httpapi"})
	if _, err := VerifyMux(mux, routes, "httpapi"); err == nil || !strings.Contains(err.Error(), "POST /api/v1/tasks/{id}") || !strings.Contains(err.Error(), "GET /api/v1/missing") {
		t.Fatalf("live mismatch not reported: %v", err)
	}
}
