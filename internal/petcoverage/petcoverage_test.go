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

func withBoundary(t *testing.T, name, boundary string) {
	t.Helper()
	saved := boundaries
	boundaries = map[string]string{}
	for key, value := range saved {
		boundaries[key] = value
	}
	boundaries[name] = boundary
	t.Cleanup(func() { boundaries = saved })
}

const scopedPreamble = `
func (s *Server) withScope(scope string, next http.Handler) http.Handler { return next }
func (s *Server) factory(section string) http.HandlerFunc { return func(http.ResponseWriter, *http.Request) {} }
`

// r2 M1: a registered wrapper is a wrapper whatever its arity; the real
// handler, the wrapper's constant arguments and a factory's arguments all
// reach the fingerprint, and a wrapper is never recorded as the handler.
func TestMultiArgumentWrappersResolveToTheRealHandler(t *testing.T) {
	classify(t, map[string]string{"internal/api (*Server).Routes": "httpapi"})
	withBoundary(t, "withScope", "scoped")
	root := writeTree(t, map[string]string{"internal/api/server.go": serverPreamble + scopedPreamble + `
func (s *Server) Routes() {
	mux := http.NewServeMux()
	mux.Handle("POST /a", s.withScope("scope.a", http.HandlerFunc(s.list)))
	mux.Handle("POST /b", s.withScope("scope.b", http.HandlerFunc(s.status)))
	mux.Handle("POST /c", s.withScope("scope.a", http.HandlerFunc(s.status)))
	mux.Handle("PUT /core", s.withAuth(s.factory("core")))
	mux.Handle("PUT /worklog", s.withAuth(s.factory("worklog")))
}
`})
	routes, _, err := ScanRoutes(root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Registration{}
	fingerprints := map[string]string{}
	for _, route := range routes {
		got[route.Operation] = route
		if previous, duplicate := fingerprints[route.Fingerprint]; duplicate {
			t.Fatalf("%s and %s share fingerprint %s", previous, route.Operation, route.Fingerprint)
		}
		fingerprints[route.Fingerprint] = route.Operation
	}
	for operation, handler := range map[string]string{"POST /a": "list", "POST /b": "status", "POST /c": "status", "PUT /core": "factory", "PUT /worklog": "factory"} {
		if got[operation].Handler != handler {
			t.Fatalf("%s handler = %q, want %q", operation, got[operation].Handler, handler)
		}
	}
	if got["POST /a"].Boundary != "scoped" || got["POST /a"].Wrappers != `withScope("scope.a")` || got["PUT /core"].Boundary != "owner_session" {
		t.Fatalf("wrapper metadata: %+v %+v", got["POST /a"], got["PUT /core"])
	}
	for name, body := range map[string]string{
		"unregistered multi-arg wrapper": `func (s *Server) withOther(scope string, next http.Handler) http.Handler { return next }; func (s *Server) Routes() { mux := http.NewServeMux(); mux.Handle("POST /x", s.withOther("x", http.HandlerFunc(s.list))) }`,
		"non-constant wrapper argument":  `func (s *Server) Routes(scope string) { mux := http.NewServeMux(); mux.Handle("POST /x", s.withScope(scope, http.HandlerFunc(s.list))) }`,
		"wrapper used as handler":        `func (s *Server) Routes() { mux := http.NewServeMux(); mux.Handle("POST /x", http.HandlerFunc(s.withScope)) }`,
	} {
		t.Run(name, func(t *testing.T) {
			root := writeTree(t, map[string]string{"internal/api/server.go": serverPreamble + scopedPreamble + body + "\n"})
			if _, _, err := ScanRoutes(root); err == nil {
				t.Fatal("scan accepted it")
			}
		})
	}
}

// r2 M2: aliases and method expressions cannot register a route unseen.
func TestHandleAliasesAndMethodExpressionsFailTheScan(t *testing.T) {
	for name, item := range map[string][2]string{
		"method value alias":     {`func (s *Server) Routes() { mux := http.NewServeMux(); add := mux.Handle; add("POST /api/v1/alias", http.HandlerFunc(s.list)) }`, "used as a value"},
		"HandleFunc alias":       {`func (s *Server) Routes() { mux := http.NewServeMux(); register := mux.HandleFunc; register("POST /api/v1/alias", s.list) }`, "used as a value"},
		"method expression":      {`func (s *Server) Routes() { mux := http.NewServeMux(); (*http.ServeMux).Handle(mux, "POST /api/v1/expr", http.HandlerFunc(s.list)) }`, "method-expression"},
		"default mux value":      {`var register = http.Handle`, "used as a value"},
		"route in a 3-arg call":  {`func (s *Server) Routes(r interface{ Handle(string, http.Handler, int) }) { r.Handle("POST /api/v1/three", http.HandlerFunc(s.list), 1) }`, "carries route pattern"},
		"qualified type is fine": {`type holder struct{ h http.Handler }`, ""},
	} {
		t.Run(name, func(t *testing.T) {
			classify(t, map[string]string{"internal/api (*Server).Routes": "httpapi"})
			root := writeTree(t, map[string]string{"internal/api/server.go": serverPreamble + item[0] + "\n"})
			_, _, err := ScanRoutes(root)
			if item[1] == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), item[1]) {
				t.Fatalf("scan error = %v, want %q", err, item[1])
			}
		})
	}
}

func TestVerifyMuxFindsPatternsTheScanMissed(t *testing.T) {
	mux := http.NewServeMux()
	ok := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	mux.Handle("GET /api/v1/known", ok)
	mux.Handle("POST /api/v1/alias", ok)
	mux.Handle("/static/", ok)
	routes := []Registration{{Operation: "GET /api/v1/known", Server: "httpapi"}, {Operation: "ANY /static/", Server: "httpapi"}}
	_, err := VerifyMux(mux, routes, "httpapi")
	if err == nil || !strings.Contains(err.Error(), `"POST /api/v1/alias", which the scan did not find`) {
		t.Fatalf("reverse check missed the extra pattern: %v", err)
	}
	live, err := LivePatterns(mux)
	if err != nil || strings.Join(live, ",") != "/static/,GET /api/v1/known,POST /api/v1/alias" {
		t.Fatalf("live patterns = %v err=%v", live, err)
	}
}

// r2 M3 for multi-argument wrappers: a reviewed row goes stale and the
// complete gate fails when the handler, the wrapper's argument or the
// wrapper's own code changes.
func TestReviewedRowUnderMultiArgumentWrapperGoesStaleOnAnyChange(t *testing.T) {
	withBoundary(t, "withScope", "scoped")
	for name, change := range map[string][2]string{
		"handler body":     {"func (s *Server) status(w http.ResponseWriter, r *http.Request) { s.list(w, r) }", "func (s *Server) status(w http.ResponseWriter, r *http.Request) { s.list(w, r); s.list(w, r) }"},
		"wrapper argument": {`s.withScope("scope.b", http.HandlerFunc(s.status))`, `s.withScope("scope.z", http.HandlerFunc(s.status))`},
		"wrapper body":     {"func (s *Server) withScope(scope string, next http.Handler) http.Handler { return next }", "func (s *Server) withScope(scope string, next http.Handler) http.Handler { _ = scope; return next }"},
	} {
		t.Run(name, func(t *testing.T) {
			classify(t, map[string]string{"internal/api (*Server).Routes": "httpapi"})
			root := writeTree(t, map[string]string{
				"internal/api/server.go":   serverPreamble + scopedPreamble + "func (s *Server) Routes() { mux := http.NewServeMux(); mux.Handle(\"POST /api/v1/b\", s.withScope(\"scope.b\", http.HandlerFunc(s.status))) }\n",
				"internal/api/api_test.go": "package api\n\nimport \"testing\"\n\nfunc TestReal(t *testing.T) {}\n",
				"web/src/api.ts":           "fetch(\"/api/v1/b\", {method: \"POST\"});\n",
				"docs/pet-mcp/.keep":       "",
			})
			catalog := NewCatalog(map[string][]string{"aha2_pet": {"aha_overview"}})
			if _, err := Check(root, catalog, true, false); err != nil {
				t.Fatal(err)
			}
			ledger := filepath.Join(root, LedgerPath)
			data, _ := os.ReadFile(ledger)
			closed := strings.Replace(string(data), "pending\tpending\tpending\tpending\tpending", "aha2_pet.aha_overview\tscoped\tnone\tinternal/api/api_test.go:TestReal\tdirect", 1)
			if closed == string(data) {
				t.Fatal("fixture row not found")
			}
			if err := os.WriteFile(ledger, []byte(closed), 0o644); err != nil {
				t.Fatal(err)
			}
			if report, err := Check(root, catalog, false, true); err != nil || report.Final != 1 {
				t.Fatalf("closed fixture row did not pass: %+v %v", report, err)
			}
			source := filepath.Join(root, "internal/api/server.go")
			code, _ := os.ReadFile(source)
			edited := strings.Replace(string(code), change[0], change[1], 1)
			if edited == string(code) {
				t.Fatal("change not applied")
			}
			if err := os.WriteFile(source, []byte(edited), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Check(root, catalog, false, false); err == nil || !strings.Contains(err.Error(), "differs from the scan") {
				t.Fatalf("change was not detected: %v", err)
			}
			report, err := Check(root, catalog, true, false)
			if err != nil || len(report.Stale) != 1 || report.Final != 0 {
				t.Fatalf("update did not mark the row stale: %+v %v", report, err)
			}
			if _, err := Check(root, catalog, false, true); err == nil || !strings.Contains(err.Error(), "incomplete") {
				t.Fatalf("complete gate passed after the change: %v", err)
			}
		})
	}
}

func TestV1FingerprintMigrationKeepsOnlyUnchangedVerdicts(t *testing.T) {
	route := Registration{Operation: "GET /a", Server: "httpapi", Source: "internal/api/a.go", Handler: "a", Boundary: "owner_session", Fingerprint: "v2:aaaaaaaaaaaa", LegacyFingerprint: "111111111111"}
	row := func(fingerprint string) []Row {
		return []Row{{"operation": "GET /a", "server": "httpapi", "source": "internal/api/a.go", "handler": "a", "boundary": "owner_session", "fingerprint": fingerprint, "mcp": "m", "permission": "p", "protected_flow": "f", "tests": "t", "status": "direct"}}
	}
	for fingerprint, want := range map[string]string{
		"111111111111":    "direct", // v1 hash of unchanged code: verdict kept
		"222222222222":    "stale",  // v1 hash of different code
		"v2:aaaaaaaaaaaa": "direct",
		"v2:bbbbbbbbbbbb": "stale",
		"v2:111111111111": "stale", // a v2 value never takes the migration path
	} {
		rows, _, _ := MergeLedger(row(fingerprint), []Registration{route})
		if rows[0]["status"] != want || rows[0]["fingerprint"] != route.Fingerprint {
			t.Fatalf("stored %s: status=%s fingerprint=%s, want %s", fingerprint, rows[0]["status"], rows[0]["fingerprint"], want)
		}
	}
}

const bareHandlerPreamble = `
func methodNotAllowed(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Allow", "GET")
	writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
}
`

// A handler written as a bare package-level function name resolves to that
// function: its source is the fingerprint and the reject-only rule applies.
func TestBarePackageFunctionHandlersResolveAndFingerprint(t *testing.T) {
	classify(t, map[string]string{"internal/api (*Server).Routes": "httpapi"})
	scan := func(t *testing.T, preamble string) Registration {
		t.Helper()
		root := writeTree(t, map[string]string{"internal/api/server.go": serverPreamble + preamble + `
func (s *Server) Routes() {
	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/view", s.withAuth(http.HandlerFunc(s.list)))
	mux.HandleFunc("/api/v1/view", methodNotAllowed)
}
`})
		routes, stats, err := ScanRoutes(root)
		if err != nil {
			t.Fatal(err)
		}
		if stats.Calls != 2 || stats.Registering != 2 {
			t.Fatalf("stats = %+v", stats)
		}
		for _, route := range routes {
			if route.Operation == "ANY /api/v1/view" {
				return route
			}
		}
		t.Fatalf("ANY /api/v1/view not scanned: %+v", routes)
		return Registration{}
	}
	route := scan(t, bareHandlerPreamble)
	if route.Handler != "methodNotAllowed" || route.Boundary != "transport_auth" || route.Wrappers != "" || !route.RejectOnly || !strings.HasPrefix(route.Fingerprint, fingerprintVersion) {
		t.Fatalf("bare handler resolved to %+v", route)
	}
	// The fingerprint follows the function body.
	changed := scan(t, strings.Replace(bareHandlerPreamble, `"method_not_allowed"`, `"not_allowed"`, 1))
	if changed.Fingerprint == route.Fingerprint {
		t.Fatal("changing the function body kept the fingerprint")
	}
	// A function that does work is not reject-only.
	working := scan(t, strings.Replace(bareHandlerPreamble, `writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")`, `doWork(); writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")`, 1)+"\nfunc doWork() {}\n")
	if working.RejectOnly {
		t.Fatal("a handler that calls other code was marked reject-only")
	}
}

func TestBareHandlerNamesThatAreNotPackageFunctionsFailTheScan(t *testing.T) {
	for name, item := range map[string][2]string{
		"undeclared name":       {`func (s *Server) Routes() { mux := http.NewServeMux(); mux.HandleFunc("/x", missing) }`, "not a package-level function"},
		"local handler value":   {`func (s *Server) Routes() { mux := http.NewServeMux(); h := s.list; mux.HandleFunc("/x", h) }`, "local value h"},
		"parameter handler":     {`func (s *Server) Routes(h http.HandlerFunc) { mux := http.NewServeMux(); mux.HandleFunc("/x", h) }`, "local value h"},
		"local shadows package": {bareHandlerPreamble + `func (s *Server) Routes() { mux := http.NewServeMux(); var methodNotAllowed http.HandlerFunc = s.list; mux.HandleFunc("/x", methodNotAllowed) }`, "local value methodNotAllowed"},
		"package variable":      {`var dynamic http.HandlerFunc; func (s *Server) Routes() { mux := http.NewServeMux(); mux.HandleFunc("/x", dynamic) }`, "not a package-level function"},
		"method name, no s.":    {`func (s *Server) Routes() { mux := http.NewServeMux(); mux.HandleFunc("/x", list) }`, "not a package-level function"},
	} {
		t.Run(name, func(t *testing.T) {
			classify(t, map[string]string{"internal/api (*Server).Routes": "httpapi"})
			root := writeTree(t, map[string]string{"internal/api/server.go": serverPreamble + item[0] + "\n"})
			if _, _, err := ScanRoutes(root); err == nil || !strings.Contains(err.Error(), item[1]) {
				t.Fatalf("scan error = %v, want %q", err, item[1])
			}
		})
	}
}

// A classified helper that ranges over a literal []string of bases expands
// one route per element; the helper itself must still be classified.
func TestStringSliceRouteLoopsExpandInAClassifiedHelper(t *testing.T) {
	classify(t, map[string]string{"internal/api (*Server).Routes": "httpapi", "internal/api (*Server).registerTerminals": "httpapi"})
	root := writeTree(t, map[string]string{"internal/api/server.go": serverPreamble + `
const terminalSuffix = "/terminal"

func (s *Server) Routes() {
	mux := http.NewServeMux()
	s.registerTerminals(mux)
}

func (s *Server) registerTerminals(mux *http.ServeMux) {
	for _, base := range []string{"/api/v1/tasks/{id}" + terminalSuffix, "/api/v1/workspaces/{workspace}" + terminalSuffix} {
		mux.Handle("GET "+base, s.withAuth(http.HandlerFunc(s.list)))
		mux.Handle("DELETE "+base+"/{terminal}", s.withAuth(http.HandlerFunc(s.status)))
	}
	mux.Handle("PUT /api/v1/settings/terminal", s.withAuth(http.HandlerFunc(s.observe)))
}
`})
	routes, stats, err := ScanRoutes(root)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Calls != 3 || stats.Registering != 3 {
		t.Fatalf("stats = %+v", stats)
	}
	var operations []string
	for _, route := range routes {
		operations = append(operations, route.Operation+" "+route.Handler)
	}
	want := "DELETE /api/v1/tasks/{id}/terminal/{terminal} status|DELETE /api/v1/workspaces/{workspace}/terminal/{terminal} status|GET /api/v1/tasks/{id}/terminal list|GET /api/v1/workspaces/{workspace}/terminal list|PUT /api/v1/settings/terminal observe"
	if strings.Join(operations, "|") != want {
		t.Fatalf("routes = %v", operations)
	}
	unclassified := writeTree(t, map[string]string{"internal/api/server.go": serverPreamble + `
func (s *Server) Routes() { mux := http.NewServeMux(); s.other(mux) }
func (s *Server) other(mux *http.ServeMux) {
	for _, base := range []string{"/a", "/b"} {
		mux.Handle("GET "+base, s.withAuth(http.HandlerFunc(s.list)))
	}
}
`})
	if _, _, err := ScanRoutes(unclassified); err == nil || !strings.Contains(err.Error(), "not classified") {
		t.Fatalf("an unclassified helper must still fail: %v", err)
	}
}

func TestRouteLoopsTheScanCannotEnumerateStillFail(t *testing.T) {
	for name, item := range map[string][2]string{
		"range over a variable":  {`func (s *Server) Routes(bases []string) { mux := http.NewServeMux(); for _, base := range bases { mux.Handle("GET "+base, s.withAuth(http.HandlerFunc(s.list))) } }`, "does not range over a literal"},
		"non-constant element":   {`func (s *Server) Routes(extra string) { mux := http.NewServeMux(); for _, base := range []string{"/a", extra} { mux.Handle("GET "+base, s.withAuth(http.HandlerFunc(s.list))) } }`, "non-constant element"},
		"loop variable reassign": {`func (s *Server) Routes() { mux := http.NewServeMux(); for _, base := range []string{"/a"} { base = base + "/x"; mux.Handle("GET "+base, s.withAuth(http.HandlerFunc(s.list))) } }`, "reassigned"},
		"array, not slice":       {`func (s *Server) Routes() { mux := http.NewServeMux(); for _, base := range [1]string{"/a"} { mux.Handle("GET "+base, s.withAuth(http.HandlerFunc(s.list))) } }`, "not range over a slice literal"},
		"non-string slice":       {`func (s *Server) Routes() { mux := http.NewServeMux(); for _, n := range []int{1} { _ = n; mux.Handle("GET /"+string(rune(n)), s.withAuth(http.HandlerFunc(s.list))) } }`, "non-string slice"},
	} {
		t.Run(name, func(t *testing.T) {
			classify(t, map[string]string{"internal/api (*Server).Routes": "httpapi"})
			root := writeTree(t, map[string]string{"internal/api/server.go": serverPreamble + item[0] + "\n"})
			if _, _, err := ScanRoutes(root); err == nil || !strings.Contains(err.Error(), item[1]) {
				t.Fatalf("scan error = %v, want %q", err, item[1])
			}
		})
	}
}

// A reviewed wrapper whose handler is its first argument resolves to that
// handler; its other arguments stay constants in the fingerprint.
func TestReviewedWrapperWithLeadingHandlerArgument(t *testing.T) {
	classify(t, map[string]string{"internal/api (*Server).Routes": "httpapi"})
	withBoundary(t, "withTerminal", "owner_session")
	saved := wrapperHandlerArgument
	wrapperHandlerArgument = map[string]int{"withTerminal": 0}
	t.Cleanup(func() { wrapperHandlerArgument = saved })
	const preamble = `
func (s *Server) withTerminal(handler http.HandlerFunc, strict bool) http.Handler { return handler }
func (s *Server) withOther(handler http.HandlerFunc, strict bool) http.Handler { return handler }
`
	root := writeTree(t, map[string]string{"internal/api/server.go": serverPreamble + preamble + `
func (s *Server) Routes() {
	mux := http.NewServeMux()
	mux.Handle("GET /a", s.withTerminal(s.list, false))
	mux.Handle("GET /b", s.withTerminal(s.list, true))
}
`})
	routes, _, err := ScanRoutes(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 2 || routes[0].Handler != "list" || routes[0].Boundary != "owner_session" || routes[0].Wrappers != `withTerminal("false")` || routes[1].Wrappers != `withTerminal("true")` {
		t.Fatalf("routes = %+v", routes)
	}
	if routes[0].Fingerprint == routes[1].Fingerprint {
		t.Fatal("the constant argument must reach the fingerprint")
	}
	for name, item := range map[string][2]string{
		// The same shape without a reviewed boundary is still an unreviewed wrapper.
		"unreviewed wrapper": {`func (s *Server) Routes() { mux := http.NewServeMux(); mux.Handle("GET /x", s.withOther(s.list, true)) }`, "no reviewed boundary"},
		"non-constant flag":  {`func (s *Server) Routes(strict bool) { mux := http.NewServeMux(); mux.Handle("GET /x", s.withTerminal(s.list, strict)) }`, "non-constant argument"},
		"shadowed true":      {`func (s *Server) Routes() { mux := http.NewServeMux(); true := false; mux.Handle("GET /x", s.withTerminal(s.list, true)) }`, "non-constant argument"},
		"missing argument":   {`func (s *Server) withTerminalShort() {}; func (s *Server) Routes() { mux := http.NewServeMux(); mux.Handle("GET /x", s.withTerminal()) }`, "unsupported handler expression"},
	} {
		t.Run(name, func(t *testing.T) {
			root := writeTree(t, map[string]string{"internal/api/server.go": serverPreamble + preamble + item[0] + "\n"})
			if _, _, err := ScanRoutes(root); err == nil || !strings.Contains(err.Error(), item[1]) {
				t.Fatalf("scan error = %v, want %q", err, item[1])
			}
		})
	}
}
