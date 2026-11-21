# P1c v2：覆盖门禁改为 Go 语法树与可核对证据（固定 6d785b3）

源码 `6d785b35e5455d06ad2e12ac6fd5d2827a9f724a`，tree `8b6209025d6dd874a931facd69fe18d8f9c23c7f`；增量 `322c154..6d785b3` 共 12 个文件（`322c154` 是 M5 材料提交，本包不改 M2-a/M5 及已投审包）。diff 与函数全文都从固定提交取得，diff 保留 3 行上下文。本轮按明确边界分 5 个包：A 扫描器与单元测试，B 账本规则/门禁/文档，L1、L2 新账本全文（按行号切两段），L3 逐行变更对照。合计覆盖全部增量，不截断。

旧扫描器的三类问题（宠物转述的 723 对 P1c 的 REQUEST_CHANGES 三类结论；报告原文不在本轮上下文中，若原文还有其他条目需另行补）：只用正则看 `internal/httpapi` 里的 `mux.Handle` 行，别处的注册会漏；`--complete` 只看状态字段，"existing HTTP server tests"、通配路径、不存在的测试名、任意 N/A 都能关闭一行；`--update` 只按路由键继承旧结论，handler 改了也不作废。

固定副本（git archive，blob 逐个核对一致）验证：

- `go test -json ./internal/petcoverage -count=1` → 退出 0，0.1s，具名用例 7 个全部 run/pass
- `go test -json ./internal/httpapi -run '^(TestPetCoverageLedgersMatchRegistrationsAndEvidence|TestMCPToolCatalogCoversEveryBuiltInServer|TestHealthProbeCarriesNoBusinessData)$' -count=1` → 退出 0，6.8s，具名用例 3 个全部 run/pass
- `go test -json ./internal/centersync -run '^(TestDeviceRenameKeepsCenterGeneratedID|TestHealthAndAuthentication|TestOneTimeDeviceRegistrationStoresOnlyHashes|TestPushIsIdempotentAndPullIsIncremental)$' -count=1 -timeout=30m` → 退出 0，1.9s，具名用例 4 个全部 run/pass
- `go test -json ./internal/httpapi -run '^(TestAgentBrowserMCPRequiresCapabilityAndDomainGate|TestAgentChannelMCPRequiresCapabilityAndExposesOnlyTwoTools|TestAgentCollabMCPRequiresCapabilityAndDomainGate|TestAgentCoreMCPRequiresCapabilityAndRunningTurn|TestAgentHardwareMCPRequiresCapabilityAndDomainGate|TestAgentKnowledgeMCPRequiresCapabilityAndDomainGate|TestAgentMailMCPTools|TestAgentWorkspaceMCPRequiresCapabilityAndDomainGate|TestDesktopMCPUsesExistingShareAndSingleUseObservation|TestHealthProbeCarriesNoBusinessData|TestPetMCPNotificationsAreAcceptedWithoutJSON|TestPetMCPPingReturnsResult|TestRemovedSkillWriteRoutesReturnMethodNotAllowed|TestServeWebServesBrowserNewTabAsHTTPPage|TestServeWebUnknownRouteStillFallsBackToSPA)$' -count=1 -timeout=30m` → 退出 0，70.4s，具名用例 15 个全部 run/pass
- `go vet ./internal/petcoverage ./cmd/aha-pet-coverage ./internal/httpapi` → 退出 0，2.2s；输出：
- `go run ./cmd/aha-pet-coverage` → 退出 0，0.8s；输出：HTTP operations: 433 (closed 21, open 412); Web literal call sites: 284; ordinary-Task MCP tools: 61 (closed 0)
- `go run ./cmd/aha-pet-coverage -complete` → 退出 1（预期 1：门禁必须失败），0.3s；输出：coverage is incomplete: 412 open operations, 61 open tool rows / exit status 1
- `GOOS=windows go vet ./internal/petcoverage ./cmd/aha-pet-coverage` → 退出 0，5.2s；输出：

已关闭行引用的每个测试都在固定副本里实际跑过（上表中按包列出），不只检查存在。

## A. 注册与 Web 调用点扫描

- `ScanRoutes` 解析 `internal/`、`cmd/` 下所有非测试 Go 文件，对每个两参 `Handle`/`HandleFunc` 调用：接收者必须是本函数里 `http.NewServeMux()` 赋值的变量或 `*http.ServeMux` 参数，否则只要第一个参数能求值成路由串就报错（覆盖 DefaultServeMux、接口接收者）。路由串支持字面量、包级常量、只赋值一次的局部变量、拼接；`route.pattern` 从包住这次调用的那个 range 的字面量表逐行展开（两张表同名循环变量不会串）。
- 注册所在函数必须在 `registeringFunctions` 里分类到监听（httpapi / mail-download / centersync），新函数不分类就失败。handler 必须有唯一声明，指纹是包装链加 handler 源码（go/printer 输出，不含注释）的 SHA-256 前 6 字节。`rejectOnly` 只认"所有调用都是回写响应的那几个函数、且写出 4xx/501"的 handler。
- `ScanWeb` 移植自原 Python 的字面量扫描（嵌套模板、方法提示不变，输出与旧 `web-call-sites.tsv` 逐字节相同）；`UnmatchedWebSites` 要求每个调用点都能对上已注册的 httpapi 路由。
- 单元测试用合成源码树逐类验证：换名接收者、常量/拼接/局部前缀、两张同名变量路由表、未分类函数、DefaultServeMux、接口接收者、动态路由串、重复、找不到 handler（逐条核对错误原因）；账本合并、证据规则、Web 匹配、漂移与 -complete。

完整 diff 文件边界：

- `internal/petcoverage/routes.go`（新文件）
- `internal/petcoverage/web.go`（新文件）
- `internal/petcoverage/petcoverage_test.go`（新文件）
- `cmd/aha-pet-coverage/main.go`（新文件）

## 该边界完整 diff

```diff
diff --git a/cmd/aha-pet-coverage/main.go b/cmd/aha-pet-coverage/main.go
new file mode 100644
index 0000000..1738f82
--- /dev/null
+++ b/cmd/aha-pet-coverage/main.go
@@ -0,0 +1,59 @@
+// Command aha-pet-coverage checks the pet MCP coverage ledgers against the
+// registered HTTP operations, Web call sites and built-in MCP tool catalog.
+//
+//	go run ./cmd/aha-pet-coverage            # drift and evidence check
+//	go run ./cmd/aha-pet-coverage -update    # rewrite inventories, then review
+//	go run ./cmd/aha-pet-coverage -complete  # release gate: every row closed
+package main
+
+import (
+	"errors"
+	"flag"
+	"fmt"
+	"os"
+	"path/filepath"
+
+	"github.com/ChinaKai/AHA2/internal/httpapi"
+	"github.com/ChinaKai/AHA2/internal/petcoverage"
+)
+
+func main() {
+	update := flag.Bool("update", false, "rewrite the inventories; reviewed columns are kept, changed rows become stale")
+	complete := flag.Bool("complete", false, "fail unless every operation and tool row has a closing status with valid evidence")
+	flag.Parse()
+	root, err := moduleRoot()
+	if err != nil {
+		fmt.Fprintln(os.Stderr, err)
+		os.Exit(2)
+	}
+	report, err := petcoverage.Check(root, petcoverage.NewCatalog(httpapi.MCPToolCatalog()), *update, *complete)
+	for _, key := range report.Stale {
+		fmt.Println("stale (re-verify):", key)
+	}
+	for _, key := range report.Removed {
+		fmt.Println("removed:", key)
+	}
+	fmt.Printf("HTTP operations: %d (closed %d, open %d); Web literal call sites: %d; ordinary-Task MCP tools: %d (closed %d)\n",
+		report.Operations, report.Final, report.Open, report.WebSites, report.Tools, report.ToolsFinal)
+	if err != nil {
+		fmt.Fprintln(os.Stderr, err)
+		os.Exit(1)
+	}
+}
+
+func moduleRoot() (string, error) {
+	dir, err := os.Getwd()
+	if err != nil {
+		return "", err
+	}
+	for {
+		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
+			return dir, nil
+		}
+		parent := filepath.Dir(dir)
+		if parent == dir {
+			return "", errors.New("go.mod not found above the working directory")
+		}
+		dir = parent
+	}
+}
diff --git a/internal/petcoverage/petcoverage_test.go b/internal/petcoverage/petcoverage_test.go
new file mode 100644
index 0000000..0de518c
--- /dev/null
+++ b/internal/petcoverage/petcoverage_test.go
@@ -0,0 +1,297 @@
+package petcoverage
+
+import (
+	"os"
+	"path/filepath"
+	"strings"
+	"testing"
+)
+
+func writeTree(t *testing.T, files map[string]string) string {
+	t.Helper()
+	root := t.TempDir()
+	for path, content := range files {
+		target := filepath.Join(root, filepath.FromSlash(path))
+		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
+			t.Fatal(err)
+		}
+		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
+			t.Fatal(err)
+		}
+	}
+	return root
+}
+
+func classify(t *testing.T, entries map[string]string) {
+	t.Helper()
+	saved := registeringFunctions
+	registeringFunctions = map[string]string{}
+	for key, value := range entries {
+		registeringFunctions[key] = value
+	}
+	t.Cleanup(func() { registeringFunctions = saved })
+}
+
+const serverPreamble = `package api
+
+import "net/http"
+
+type Server struct{}
+
+func (s *Server) withAuth(next http.Handler) http.Handler { return next }
+func (s *Server) withAgentCapability(next http.Handler) http.Handler { return next }
+func (s *Server) list(w http.ResponseWriter, r *http.Request) { s.withAuth(nil) }
+func (s *Server) status(w http.ResponseWriter, r *http.Request) { s.list(w, r) }
+func (s *Server) observe(w http.ResponseWriter, r *http.Request) { s.list(w, r) }
+func (s *Server) reject(w http.ResponseWriter, r *http.Request) {
+	w.Header().Set("Allow", "GET")
+	writeError(w, http.StatusMethodNotAllowed, "read_only")
+}
+func writeError(w http.ResponseWriter, status int, code string) {}
+`
+
+func TestScanFindsEveryRegistrationShape(t *testing.T) {
+	classify(t, map[string]string{"internal/api (*Server).Routes": "httpapi", "internal/api (*Server).tables": "httpapi"})
+	root := writeTree(t, map[string]string{
+		"internal/api/server.go": serverPreamble + `
+const waitRoute = "POST /api/v1/agent/wait"
+const prefixConst = "/api/v1/" + "projects"
+
+func (s *Server) Routes() http.Handler {
+	router := http.NewServeMux()
+	router.Handle("GET /api/v1/system", s.withAuth(http.HandlerFunc(s.list)))
+	router.Handle(waitRoute, s.withAgentCapability(http.HandlerFunc(s.status)))
+	router.HandleFunc("GET "+prefixConst+"/{id}", s.observe)
+	router.Handle("/api/v1/prompts", s.withAuth(http.HandlerFunc(s.reject)))
+	s.tables(router)
+	return router
+}
+
+func (s *Server) tables(mux *http.ServeMux) {
+	prefix := "/api/v1/tasks/{id}/desktop"
+	for _, route := range []struct {
+		pattern string
+		handler http.HandlerFunc
+	}{
+		{"GET " + prefix, s.status},
+		{pattern: "POST " + prefix + "/stop", handler: s.observe},
+	} {
+		mux.Handle(route.pattern, s.withAuth(route.handler))
+	}
+	for _, route := range []struct {
+		pattern string
+		handler http.HandlerFunc
+	}{
+		{"GET /api/v1/agent/desktop", s.status},
+	} {
+		mux.Handle(route.pattern, s.withAgentCapability(route.handler))
+	}
+}
+`,
+	})
+	routes, err := ScanRoutes(root)
+	if err != nil {
+		t.Fatal(err)
+	}
+	got := map[string]Registration{}
+	for _, route := range routes {
+		got[route.Operation] = route
+	}
+	want := map[string]string{
+		"GET /api/v1/system":                   "list owner_session",
+		"POST /api/v1/agent/wait":              "status agent_turn",
+		"GET /api/v1/projects/{id}":            "observe transport_auth",
+		"ANY /api/v1/prompts":                  "reject owner_session",
+		"GET /api/v1/tasks/{id}/desktop":       "status owner_session",
+		"POST /api/v1/tasks/{id}/desktop/stop": "observe owner_session",
+		"GET /api/v1/agent/desktop":            "status agent_turn",
+	}
+	if len(got) != len(want) {
+		t.Fatalf("scanned %d routes, want %d: %v", len(got), len(want), got)
+	}
+	for operation, expected := range want {
+		route, ok := got[operation]
+		if !ok || route.Handler+" "+route.Boundary != expected || route.Fingerprint == "" || route.Source != "internal/api/server.go" {
+			t.Fatalf("%s = %+v, want %s", operation, route, expected)
+		}
+	}
+	if !got["ANY /api/v1/prompts"].RejectOnly || got["GET /api/v1/system"].RejectOnly {
+		t.Fatal("reject-only detection is wrong")
+	}
+}
+
+func TestScanFailsClosedOnRegistrationsItCannotAccountFor(t *testing.T) {
+	cases := map[string][2]string{
+		"unclassified function": {`func (s *Server) Other() { mux := http.NewServeMux(); mux.Handle("GET /x", http.HandlerFunc(s.list)) }`, "not classified"},
+		"default serve mux":     {`func (s *Server) Routes() { http.Handle("GET /x", http.HandlerFunc(s.list)) }`, "unrecognized receiver"},
+		"unknown receiver":      {`func (s *Server) Routes(r interface{ Handle(string, http.Handler) }) { r.Handle("/x", http.HandlerFunc(s.list)) }`, "unrecognized receiver"},
+		"dynamic pattern":       {`func (s *Server) Routes(pattern string) { mux := http.NewServeMux(); mux.Handle(pattern, http.HandlerFunc(s.list)) }`, "unresolvable route pattern"},
+		"duplicate":             {`func (s *Server) Routes() { mux := http.NewServeMux(); mux.Handle("GET /x", http.HandlerFunc(s.list)); mux.Handle("GET /x", http.HandlerFunc(s.status)) }`, "duplicate route"},
+		"unknown handler":       {`func (s *Server) Routes() { mux := http.NewServeMux(); mux.Handle("GET /x", http.HandlerFunc(s.missing)) }`, "no unique declaration"},
+	}
+	for name, item := range cases {
+		t.Run(name, func(t *testing.T) {
+			classify(t, map[string]string{"internal/api (*Server).Routes": "httpapi"})
+			root := writeTree(t, map[string]string{"internal/api/server.go": serverPreamble + item[0] + "\n"})
+			if _, err := ScanRoutes(root); err == nil || !strings.Contains(err.Error(), item[1]) {
+				t.Fatalf("scan error = %v, want %q", err, item[1])
+			}
+		})
+	}
+}
+
+func TestMergeMarksChangedReviewedRowsStaleAndKeepsPending(t *testing.T) {
+	routes := []Registration{
+		{Operation: "GET /a", Server: "httpapi", Source: "internal/api/a.go", Handler: "a", Boundary: "owner_session", Fingerprint: "new"},
+		{Operation: "GET /b", Server: "httpapi", Source: "internal/api/a.go", Handler: "b", Boundary: "owner_session", Fingerprint: "same"},
+		{Operation: "GET /c", Server: "httpapi", Source: "internal/api/a.go", Handler: "c", Boundary: "owner_session", Fingerprint: "changed"},
+		{Operation: "GET /new", Server: "httpapi", Source: "internal/api/a.go", Handler: "n", Boundary: "owner_session", Fingerprint: "n"},
+	}
+	previous := []Row{
+		{"operation": "GET /a", "server": "httpapi", "source": "internal/api/a.go", "handler": "a", "boundary": "owner_session", "fingerprint": "old", "mcp": "aha2_pet.aha_overview", "permission": "p", "protected_flow": "none", "tests": "x_test.go:TestX", "status": "direct"},
+		{"operation": "GET /b", "server": "httpapi", "source": "internal/api/a.go", "handler": "b", "boundary": "owner_session", "fingerprint": "same", "mcp": "m", "permission": "p", "protected_flow": "f", "tests": "t", "status": "direct"},
+		{"operation": "GET /c", "server": "httpapi", "source": "internal/api/a.go", "handler": "c", "boundary": "owner_session", "fingerprint": "old", "mcp": "pending", "permission": "pending", "protected_flow": "pending", "tests": "pending", "status": "pending"},
+		{"operation": "GET /gone", "server": "httpapi", "status": "direct"},
+	}
+	rows, stale, removed := MergeLedger(previous, routes)
+	status := map[string]string{}
+	for _, row := range rows {
+		status[row["operation"]] = row["status"]
+	}
+	if status["GET /a"] != "stale" || rows[0]["mcp"] != "aha2_pet.aha_overview" || rows[0]["fingerprint"] != "new" {
+		t.Fatal("a changed handler kept its old verdict or lost the reviewed text")
+	}
+	if status["GET /b"] != "direct" || status["GET /c"] != "pending" || status["GET /new"] != "pending" {
+		t.Fatalf("unexpected merge statuses %v", status)
+	}
+	if len(stale) != 1 || len(removed) != 1 || removed[0] != "httpapi GET /gone" {
+		t.Fatalf("stale=%v removed=%v", stale, removed)
+	}
+}
+
+func TestEvidenceRulesRejectVagueOrMisappliedClosure(t *testing.T) {
+	root := writeTree(t, map[string]string{"internal/api/a_test.go": "package api\n\nimport \"testing\"\n\nfunc TestReal(t *testing.T) {}\n"})
+	catalog := NewCatalog(map[string][]string{"aha2_pet": {"aha_overview"}})
+	owner := Registration{Operation: "GET /api/v1/system", Handler: "system", Boundary: "owner_session"}
+	protocol := Registration{Operation: "POST /api/v1/agent/core/mcp", Handler: "agentCoreMCP", Boundary: "agent_turn"}
+	reject := Registration{Operation: "ANY /api/v1/prompts", Handler: "reject", Boundary: "owner_session", RejectOnly: true}
+	good := Row{"mcp": "aha2_pet.aha_overview", "permission": "view tasks", "protected_flow": "none", "tests": "internal/api/a_test.go:TestReal", "status": "direct"}
+	if err := ValidateRow(root, catalog, owner, good); err != nil {
+		t.Fatalf("valid direct row rejected: %v", err)
+	}
+	with := func(changes Row) Row {
+		row := Row{}
+		for key, value := range good {
+			row[key] = value
+		}
+		for key, value := range changes {
+			row[key] = value
+		}
+		return row
+	}
+	bad := map[string]struct {
+		route Registration
+		row   Row
+	}{
+		"prose tests":             {owner, with(Row{"tests": "existing HTTP server tests"})},
+		"glob tests":              {owner, with(Row{"tests": "internal/api/*_test.go"})},
+		"missing test":            {owner, with(Row{"tests": "internal/api/a_test.go:TestMissing"})},
+		"missing tool":            {owner, with(Row{"mcp": "aha2_pet.aha_missing"})},
+		"no tool named":           {owner, with(Row{"mcp": "reuses Web flow"})},
+		"pending field":           {owner, with(Row{"permission": "pending"})},
+		"empty field":             {owner, with(Row{"protected_flow": ""})},
+		"transport on owner op":   {owner, with(Row{"mcp": "none", "status": "transport_na"})},
+		"prohibited not rejected": {owner, with(Row{"mcp": "none", "status": "web_prohibited_na"})},
+		"na with tool":            {protocol, with(Row{"status": "transport_na"})},
+		"unknown status":          {owner, with(Row{"status": "owner_only"})},
+		"open row bad reference":  {owner, with(Row{"status": "pending", "tests": "see suite"})},
+	}
+	for name, item := range bad {
+		if err := ValidateRow(root, catalog, item.route, item.row); err == nil {
+			t.Fatalf("%s was accepted", name)
+		}
+	}
+	for name, item := range map[string]struct {
+		route Registration
+		row   Row
+	}{
+		"protocol transport": {protocol, with(Row{"mcp": "none", "status": "transport_na"})},
+		"web rejects":        {reject, with(Row{"mcp": "none", "status": "web_prohibited_na"})},
+		"open with pending":  {owner, Row{"mcp": "pending", "permission": "pending", "protected_flow": "pending", "tests": "pending", "status": "stale"}},
+	} {
+		if err := ValidateRow(root, catalog, item.route, item.row); err != nil {
+			t.Fatalf("%s rejected: %v", name, err)
+		}
+	}
+}
+
+func TestWebSitesMustMatchARegisteredRoute(t *testing.T) {
+	routes := []Registration{
+		{Operation: "GET /api/v1/projects", Server: "httpapi"},
+		{Operation: "PATCH /api/v1/tasks/{id}", Server: "httpapi"},
+		{Operation: "GET /api/v1/knowledge-vault/git/status", Server: "httpapi"},
+		{Operation: "GET /api/v1/other", Server: "centersync"},
+	}
+	sites := []WebSite{
+		{Source: "a.ts", Line: 1, MethodHint: "GET/see-source", Expression: "/api/v1/projects${query.size ? `?${query}` : \"\"}"},
+		{Source: "a.ts", Line: 2, MethodHint: "PATCH", Expression: "/api/v1/tasks/${encodeURIComponent(id)}"},
+		{Source: "a.ts", Line: 3, MethodHint: "GET/see-source", Expression: "/api/v1/knowledge-vault/git/${action}"},
+		{Source: "a.ts", Line: 4, MethodHint: "DELETE", Expression: "/api/v1/tasks/${id}"},
+		{Source: "a.ts", Line: 5, MethodHint: "GET/see-source", Expression: "/api/v1/unregistered"},
+		{Source: "a.ts", Line: 6, MethodHint: "GET/see-source", Expression: "/api/v1/other"},
+	}
+	unmatched, err := UnmatchedWebSites(sites, routes)
+	if err != nil {
+		t.Fatal(err)
+	}
+	var lines []int
+	for _, site := range unmatched {
+		lines = append(lines, site.Line)
+	}
+	if len(lines) != 3 || lines[0] != 4 || lines[1] != 5 || lines[2] != 6 {
+		t.Fatalf("unmatched lines = %v, want [4 5 6]", lines)
+	}
+}
+
+func TestScanWebKeepsNestedTemplatesAndMethodHints(t *testing.T) {
+	root := writeTree(t, map[string]string{"web/src/api.ts": "return this.request(`/api/v1/tasks/${encodeURIComponent(id)}`, {method: \"PATCH\", body: \"{}\"});\nfetch(\"/api/v1/system\");\nreturn this.request(`/api/v1/projects${query.size ? `?${query}` : \"\"}`);\n"})
+	sites, err := ScanWeb(root)
+	if err != nil {
+		t.Fatal(err)
+	}
+	if len(sites) != 3 || sites[0].MethodHint != "PATCH" || sites[1].Line != 2 || sites[2].Expression != "/api/v1/projects${query.size ? `?${query}` : \"\"}" {
+		t.Fatalf("sites = %+v", sites)
+	}
+}
+
+func TestCheckDetectsDriftAndCompleteRequiresClosure(t *testing.T) {
+	classify(t, map[string]string{"internal/api (*Server).Routes": "httpapi"})
+	root := writeTree(t, map[string]string{
+		"internal/api/server.go": serverPreamble + "func (s *Server) Routes() { mux := http.NewServeMux(); mux.Handle(\"GET /api/v1/system\", s.withAuth(http.HandlerFunc(s.list))) }\n",
+		"web/src/api.ts":         "fetch(\"/api/v1/system\");\n",
+		"docs/pet-mcp/.keep":     "",
+	})
+	catalog := NewCatalog(map[string][]string{"aha2_pet": {"aha_overview"}, "aha2_core": {"progress"}})
+	if _, err := Check(root, catalog, false, false); err == nil || !strings.Contains(err.Error(), "differs from the scan") {
+		t.Fatalf("missing ledger was not reported as drift: %v", err)
+	}
+	report, err := Check(root, catalog, true, false)
+	if err != nil || report.Operations != 1 || report.Open != 1 || report.Tools != 1 {
+		t.Fatalf("update failed: %+v %v", report, err)
+	}
+	if _, err := Check(root, catalog, false, false); err != nil {
+		t.Fatalf("freshly updated ledger did not pass the drift check: %v", err)
+	}
+	if _, err := Check(root, catalog, false, true); err == nil || !strings.Contains(err.Error(), "incomplete") {
+		t.Fatalf("open rows passed the complete gate: %v", err)
+	}
+	// A handler edit invalidates the stored fingerprint.
+	path := filepath.Join(root, "internal/api/server.go")
+	data, _ := os.ReadFile(path)
+	if err := os.WriteFile(path, []byte(strings.Replace(string(data), "s.withAuth(nil) }", "s.withAuth(nil); _ = r }", 1)), 0o644); err != nil {
+		t.Fatal(err)
+	}
+	if _, err := Check(root, catalog, false, false); err == nil {
+		t.Fatal("handler change did not invalidate the ledger")
+	}
+}
diff --git a/internal/petcoverage/routes.go b/internal/petcoverage/routes.go
new file mode 100644
index 0000000..c2655e3
--- /dev/null
+++ b/internal/petcoverage/routes.go
@@ -0,0 +1,535 @@
+// Package petcoverage inventories every registered HTTP operation and every
+// literal Web API call site, and checks the pet MCP coverage ledger against
+// them. It parses Go source with go/parser (the compiler's own syntax), so a
+// registration cannot hide behind a receiver name, a constant or a route
+// table; any registration shape it cannot resolve fails the scan.
+package petcoverage
+
+import (
+	"bytes"
+	"crypto/sha256"
+	"encoding/hex"
+	"fmt"
+	"go/ast"
+	"go/parser"
+	"go/printer"
+	"go/token"
+	"io/fs"
+	"os"
+	"path/filepath"
+	"sort"
+	"strconv"
+	"strings"
+)
+
+// Registration is one HTTP operation as the router sees it.
+type Registration struct {
+	Operation   string // "METHOD /path", or "ANY /path" for a methodless pattern
+	Server      string // which listener serves it (see registeringFunctions)
+	Source      string // repo-relative file of the registration
+	Handler     string // handler method, factory, or "inline"
+	Wrappers    string // authentication/authorization wrappers, outermost first
+	Boundary    string // authentication boundary derived from Wrappers
+	Fingerprint string // hash of wrappers and the handler's source
+	RejectOnly  bool   // the handler only writes a client error
+}
+
+// registeringFunctions classifies every function that registers routes. A
+// new registering function fails the scan until a reviewer classifies it.
+var registeringFunctions = map[string]string{
+	"internal/httpapi (*Server).Handler":               "httpapi",
+	"internal/httpapi (*Server).registerBrowserRoutes": "httpapi",
+	"internal/httpapi (*Server).registerDesktopRoutes": "httpapi",
+	"internal/httpapi (*Server).MailDownloadHandler":   "mail-download",
+	"internal/centersync (*Store).Handler":             "centersync",
+}
+
+var boundaries = map[string]string{
+	"withAgentCapability":   "agent_turn",
+	"withOwnerOrAgentAuth":  "owner_or_agent",
+	"withAuth":              "owner_session",
+	"withChannelCapability": "channel_runtime",
+	"auth":                  "device_token",
+}
+
+// scanned Go roots; everything else (web, tooling output, vendored copies)
+// cannot register a route served by AHA2 binaries.
+var goRoots = []string{"internal", "cmd"}
+
+type goPackage struct {
+	dir       string
+	fset      *token.FileSet
+	files     map[string]*ast.File
+	constants map[string]ast.Expr
+	methods   map[string]*ast.FuncDecl // handler name -> method declaration
+}
+
+func loadPackages(root string) (map[string]*goPackage, error) {
+	packages := map[string]*goPackage{}
+	for _, base := range goRoots {
+		err := filepath.WalkDir(filepath.Join(root, base), func(path string, entry fs.DirEntry, err error) error {
+			if err != nil {
+				return err
+			}
+			if entry.IsDir() {
+				if name := entry.Name(); name == "testdata" || name == "vendor" || name == "node_modules" || strings.HasPrefix(name, ".") {
+					return filepath.SkipDir
+				}
+				return nil
+			}
+			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
+				return nil
+			}
+			dir, err := filepath.Rel(root, filepath.Dir(path))
+			if err != nil {
+				return err
+			}
+			dir = filepath.ToSlash(dir)
+			pkg := packages[dir]
+			if pkg == nil {
+				pkg = &goPackage{dir: dir, fset: token.NewFileSet(), files: map[string]*ast.File{}, constants: map[string]ast.Expr{}, methods: map[string]*ast.FuncDecl{}}
+				packages[dir] = pkg
+			}
+			file, err := parser.ParseFile(pkg.fset, path, nil, parser.SkipObjectResolution)
+			if err != nil {
+				return err
+			}
+			pkg.files[dir+"/"+entry.Name()] = file
+			return nil
+		})
+		if err != nil && !os.IsNotExist(err) {
+			return nil, err
+		}
+	}
+	for _, pkg := range packages {
+		for _, file := range pkg.files {
+			for _, declaration := range file.Decls {
+				switch value := declaration.(type) {
+				case *ast.GenDecl:
+					if value.Tok != token.CONST {
+						continue
+					}
+					for _, spec := range value.Specs {
+						item := spec.(*ast.ValueSpec)
+						for index, name := range item.Names {
+							if index < len(item.Values) {
+								pkg.constants[name.Name] = item.Values[index]
+							}
+						}
+					}
+				case *ast.FuncDecl:
+					if value.Recv != nil && value.Body != nil {
+						if _, exists := pkg.methods[value.Name.Name]; exists {
+							// Same-named methods on different receivers would make the
+							// handler fingerprint ambiguous.
+							pkg.methods[value.Name.Name] = nil
+						} else {
+							pkg.methods[value.Name.Name] = value
+						}
+					}
+				}
+			}
+		}
+	}
+	return packages, nil
+}
+
+func functionKey(declaration *ast.FuncDecl) string {
+	if declaration.Recv == nil || len(declaration.Recv.List) == 0 {
+		return declaration.Name.Name
+	}
+	var receiver bytes.Buffer
+	printer.Fprint(&receiver, token.NewFileSet(), declaration.Recv.List[0].Type)
+	return "(" + receiver.String() + ")." + declaration.Name.Name
+}
+
+// ScanRoutes returns every route registration under root, sorted by operation.
+func ScanRoutes(root string) ([]Registration, error) {
+	packages, err := loadPackages(root)
+	if err != nil {
+		return nil, err
+	}
+	var result []Registration
+	seen := map[string]string{}
+	dirs := make([]string, 0, len(packages))
+	for dir := range packages {
+		dirs = append(dirs, dir)
+	}
+	sort.Strings(dirs)
+	for _, dir := range dirs {
+		pkg := packages[dir]
+		names := make([]string, 0, len(pkg.files))
+		for name := range pkg.files {
+			names = append(names, name)
+		}
+		sort.Strings(names)
+		for _, source := range names {
+			for _, declaration := range pkg.files[source].Decls {
+				function, ok := declaration.(*ast.FuncDecl)
+				if !ok || function.Body == nil {
+					continue
+				}
+				found, err := scanFunction(pkg, source, function)
+				if err != nil {
+					return nil, err
+				}
+				for _, item := range found {
+					key := item.Server + " " + item.Operation
+					if previous, exists := seen[key]; exists {
+						return nil, fmt.Errorf("duplicate route %s (%s and %s)", key, previous, item.Source)
+					}
+					seen[key] = item.Source
+					result = append(result, item)
+				}
+			}
+		}
+	}
+	sort.Slice(result, func(i, j int) bool {
+		if result[i].Operation != result[j].Operation {
+			return result[i].Operation < result[j].Operation
+		}
+		return result[i].Server < result[j].Server
+	})
+	return result, nil
+}
+
+type scope struct {
+	pkg      *goPackage
+	function *ast.FuncDecl
+	locals   map[string]ast.Expr // string variables assigned exactly once
+	muxes    map[string]bool
+	// enclosing holds the ancestors of the call being resolved, innermost last.
+	enclosing []ast.Node
+}
+
+func scanFunction(pkg *goPackage, source string, function *ast.FuncDecl) ([]Registration, error) {
+	current := &scope{pkg: pkg, function: function, locals: map[string]ast.Expr{}, muxes: map[string]bool{}}
+	if function.Type.Params != nil {
+		for _, field := range function.Type.Params.List {
+			if isServeMuxType(field.Type) {
+				for _, name := range field.Names {
+					current.muxes[name.Name] = true
+				}
+			}
+		}
+	}
+	assigned := map[string]int{}
+	ast.Inspect(function.Body, func(node ast.Node) bool {
+		switch value := node.(type) {
+		case *ast.AssignStmt:
+			for index, left := range value.Lhs {
+				name, ok := left.(*ast.Ident)
+				if !ok || index >= len(value.Rhs) {
+					continue
+				}
+				assigned[name.Name]++
+				current.locals[name.Name] = value.Rhs[index]
+				if call, ok := value.Rhs[index].(*ast.CallExpr); ok && isSelector(call.Fun, "http", "NewServeMux") {
+					current.muxes[name.Name] = true
+				}
+			}
+		}
+		return true
+	})
+	for name, count := range assigned {
+		if count != 1 {
+			delete(current.locals, name)
+		}
+	}
+	var result []Registration
+	var failure error
+	var stack []ast.Node
+	ast.Inspect(function.Body, func(node ast.Node) bool {
+		if node == nil {
+			stack = stack[:len(stack)-1]
+			return true
+		}
+		stack = append(stack, node)
+		if failure != nil {
+			return false
+		}
+		call, ok := node.(*ast.CallExpr)
+		if !ok {
+			return true
+		}
+		current.enclosing = stack
+		method, ok := call.Fun.(*ast.SelectorExpr)
+		if !ok || (method.Sel.Name != "Handle" && method.Sel.Name != "HandleFunc") || len(call.Args) != 2 {
+			return true
+		}
+		receiver, _ := method.X.(*ast.Ident)
+		if receiver == nil || !current.muxes[receiver.Name] {
+			// A 2-argument Handle on something else is only acceptable when the
+			// first argument cannot be a route pattern (e.g. slog handlers).
+			if pattern, ok := current.eval(call.Args[0]); ok && looksLikePattern(pattern) {
+				failure = fmt.Errorf("%s: route registered on an unrecognized receiver in %s", source, functionKey(function))
+			} else if _, isSelector := call.Args[0].(*ast.SelectorExpr); isSelector && receiver != nil && receiver.Name == "http" {
+				failure = fmt.Errorf("%s: route registered on http.DefaultServeMux in %s", source, functionKey(function))
+			}
+			return true
+		}
+		server, classified := registeringFunctions[pkg.dir+" "+functionKey(function)]
+		if !classified {
+			failure = fmt.Errorf("%s: %s registers routes but is not classified in registeringFunctions", source, functionKey(function))
+			return false
+		}
+		items, err := current.registrations(source, server, call)
+		if err != nil {
+			failure = err
+			return false
+		}
+		result = append(result, items...)
+		return true
+	})
+	return result, failure
+}
+
+func (current *scope) registrations(source, server string, call *ast.CallExpr) ([]Registration, error) {
+	if pattern, ok := current.eval(call.Args[0]); ok {
+		item, err := current.registration(source, server, pattern, call.Args[1], nil)
+		return []Registration{item}, err
+	}
+	// mux.Handle(route.pattern, wrap(route.handler)) inside a range over a
+	// literal table: expand every row of that table.
+	field, ok := call.Args[0].(*ast.SelectorExpr)
+	if !ok {
+		return nil, fmt.Errorf("%s: unresolvable route pattern in %s", source, functionKey(current.function))
+	}
+	variable, ok := field.X.(*ast.Ident)
+	var loop *ast.RangeStmt
+	for index := len(current.enclosing) - 1; ok && index >= 0 && loop == nil; index-- {
+		if candidate, isRange := current.enclosing[index].(*ast.RangeStmt); isRange {
+			if value, isIdent := candidate.Value.(*ast.Ident); isIdent && value.Name == variable.Name {
+				loop = candidate
+			}
+		}
+	}
+	if !ok || loop == nil {
+		return nil, fmt.Errorf("%s: unresolvable route pattern in %s", source, functionKey(current.function))
+	}
+	table, ok := loop.X.(*ast.CompositeLit)
+	if !ok {
+		return nil, fmt.Errorf("%s: route table in %s is not a literal", source, functionKey(current.function))
+	}
+	structType, ok := table.Type.(*ast.ArrayType)
+	if !ok {
+		return nil, fmt.Errorf("%s: route table in %s is not a slice literal", source, functionKey(current.function))
+	}
+	fields, ok := structType.Elt.(*ast.StructType)
+	if !ok {
+		return nil, fmt.Errorf("%s: route table in %s has no inline struct type", source, functionKey(current.function))
+	}
+	var order []string
+	for _, item := range fields.Fields.List {
+		for _, name := range item.Names {
+			order = append(order, name.Name)
+		}
+	}
+	var result []Registration
+	for _, element := range table.Elts {
+		row, ok := element.(*ast.CompositeLit)
+		if !ok {
+			return nil, fmt.Errorf("%s: route table row in %s is not a literal", source, functionKey(current.function))
+		}
+		values := map[string]ast.Expr{}
+		for index, value := range row.Elts {
+			if keyed, ok := value.(*ast.KeyValueExpr); ok {
+				values[keyed.Key.(*ast.Ident).Name] = keyed.Value
+			} else if index < len(order) {
+				values[order[index]] = value
+			}
+		}
+		pattern, ok := current.eval(values[field.Sel.Name])
+		if !ok {
+			return nil, fmt.Errorf("%s: unresolvable route table pattern in %s", source, functionKey(current.function))
+		}
+		item, err := current.registration(source, server, pattern, call.Args[1], func(expr ast.Expr) ast.Expr {
+			if selector, ok := expr.(*ast.SelectorExpr); ok {
+				if base, ok := selector.X.(*ast.Ident); ok && base.Name == variable.Name {
+					return values[selector.Sel.Name]
+				}
+			}
+			return nil
+		})
+		if err != nil {
+			return nil, err
+		}
+		result = append(result, item)
+	}
+	return result, nil
+}
+
+func (current *scope) registration(source, server, pattern string, handler ast.Expr, substitute func(ast.Expr) ast.Expr) (Registration, error) {
+	method, route, found := strings.Cut(pattern, " ")
+	if !found {
+		method, route = "ANY", pattern
+	}
+	item := Registration{Operation: method + " " + strings.TrimSpace(route), Server: server, Source: source}
+	var wrappers []string
+	var body ast.Node
+	expr := handler
+	for depth := 0; ; depth++ {
+		if depth > 16 {
+			return item, fmt.Errorf("%s: handler for %s is nested too deeply", source, item.Operation)
+		}
+		if substitute != nil {
+			if replaced := substitute(expr); replaced != nil {
+				expr = replaced
+			}
+		}
+		switch value := expr.(type) {
+		case *ast.CallExpr:
+			if isSelector(value.Fun, "http", "HandlerFunc") && len(value.Args) == 1 {
+				expr = value.Args[0]
+				continue
+			}
+			selector, ok := value.Fun.(*ast.SelectorExpr)
+			if !ok {
+				return item, fmt.Errorf("%s: unsupported handler expression for %s", source, item.Operation)
+			}
+			if len(value.Args) == 1 && !isBasicLiteral(value.Args[0]) {
+				wrappers = append(wrappers, selector.Sel.Name)
+				expr = value.Args[0]
+				continue
+			}
+			// Handler factory such as s.savePetWorkbenchDocument("core").
+			item.Handler = selector.Sel.Name
+			body = current.pkg.methods[selector.Sel.Name]
+		case *ast.SelectorExpr:
+			item.Handler = value.Sel.Name
+			body = current.pkg.methods[value.Sel.Name]
+		case *ast.FuncLit:
+			item.Handler = "inline"
+			body = value
+		default:
+			return item, fmt.Errorf("%s: unsupported handler expression for %s", source, item.Operation)
+		}
+		break
+	}
+	if body == nil || body == (*ast.FuncDecl)(nil) {
+		return item, fmt.Errorf("%s: handler %s for %s has no unique declaration in %s", source, item.Handler, item.Operation, current.pkg.dir)
+	}
+	item.Wrappers = strings.Join(wrappers, ">")
+	item.Boundary = "transport_auth"
+	for _, wrapper := range wrappers {
+		if boundary, ok := boundaries[wrapper]; ok {
+			item.Boundary = boundary
+			break
+		}
+	}
+	var printed bytes.Buffer
+	if err := printer.Fprint(&printed, current.pkg.fset, body); err != nil {
+		return item, err
+	}
+	sum := sha256.Sum256([]byte(item.Wrappers + "\n" + printed.String()))
+	item.Fingerprint = hex.EncodeToString(sum[:6])
+	item.RejectOnly = rejectOnly(body)
+	return item, nil
+}
+
+func (current *scope) eval(expr ast.Expr) (string, bool) {
+	switch value := expr.(type) {
+	case *ast.BasicLit:
+		if value.Kind == token.STRING {
+			decoded, err := strconv.Unquote(value.Value)
+			return decoded, err == nil
+		}
+	case *ast.Ident:
+		if local, ok := current.locals[value.Name]; ok {
+			return current.eval(local)
+		}
+		if constant, ok := current.pkg.constants[value.Name]; ok {
+			return current.eval(constant)
+		}
+	case *ast.BinaryExpr:
+		if value.Op == token.ADD {
+			left, leftOK := current.eval(value.X)
+			right, rightOK := current.eval(value.Y)
+			return left + right, leftOK && rightOK
+		}
+	case *ast.ParenExpr:
+		return current.eval(value.X)
+	}
+	return "", false
+}
+
+func looksLikePattern(value string) bool {
+	if strings.HasPrefix(value, "/") {
+		return true
+	}
+	method, route, found := strings.Cut(value, " ")
+	return found && strings.HasPrefix(strings.TrimSpace(route), "/") && strings.ToUpper(method) == method
+}
+
+func isServeMuxType(expr ast.Expr) bool {
+	star, ok := expr.(*ast.StarExpr)
+	return ok && isSelector(star.X, "http", "ServeMux")
+}
+
+func isSelector(expr ast.Expr, base, name string) bool {
+	selector, ok := expr.(*ast.SelectorExpr)
+	if !ok || selector.Sel.Name != name {
+		return false
+	}
+	ident, ok := selector.X.(*ast.Ident)
+	return ok && ident.Name == base
+}
+
+func isBasicLiteral(expr ast.Expr) bool {
+	_, ok := expr.(*ast.BasicLit)
+	return ok
+}
+
+// rejectOnly reports a handler that performs no work: every call in it is
+// one of the response-writing helpers below, and it writes a client error.
+// Only such handlers may be ledgered as web_prohibited_na.
+func rejectOnly(body ast.Node) bool {
+	var block *ast.BlockStmt
+	switch value := body.(type) {
+	case *ast.FuncDecl:
+		block = value.Body
+	case *ast.FuncLit:
+		block = value.Body
+	}
+	if block == nil {
+		return false
+	}
+	allowed := map[string]bool{"writeError": true, "writeErrorDetail": true, "writeJSON": true, "Error": true, "Header": true, "Set": true, "PathValue": true, "WriteHeader": true}
+	inert, rejects := true, false
+	ast.Inspect(block, func(node ast.Node) bool {
+		switch value := node.(type) {
+		case *ast.FuncLit:
+			inert = false
+		case *ast.CallExpr:
+			name := ""
+			switch fun := value.Fun.(type) {
+			case *ast.Ident:
+				name = fun.Name
+			case *ast.SelectorExpr:
+				name = fun.Sel.Name
+			}
+			if !allowed[name] {
+				inert = false
+			}
+			for _, argument := range value.Args {
+				if isClientErrorStatus(argument) {
+					rejects = true
+				}
+			}
+		}
+		return true
+	})
+	return inert && rejects
+}
+
+func isClientErrorStatus(expr ast.Expr) bool {
+	selector, ok := expr.(*ast.SelectorExpr)
+	if !ok {
+		return false
+	}
+	switch selector.Sel.Name {
+	case "StatusMethodNotAllowed", "StatusGone", "StatusNotFound", "StatusForbidden", "StatusBadRequest", "StatusNotImplemented":
+		return isSelector(expr, "http", selector.Sel.Name)
+	}
+	return false
+}
diff --git a/internal/petcoverage/web.go b/internal/petcoverage/web.go
new file mode 100644
index 0000000..e1fc6de
--- /dev/null
+++ b/internal/petcoverage/web.go
@@ -0,0 +1,208 @@
+package petcoverage
+
+import (
+	"fmt"
+	"io/fs"
+	"os"
+	"path/filepath"
+	"regexp"
+	"sort"
+	"strings"
+)
+
+// WebSite is one literal API call in the Web client.
+type WebSite struct {
+	Source     string
+	Line       int
+	MethodHint string // concrete method, or GET/see-source when not literal
+	Expression string // literal text between the quotes, templates kept
+}
+
+var webMethod = regexp.MustCompile(`method:\s*["'](GET|POST|PUT|PATCH|DELETE)["']`)
+
+// ScanWeb lists every string/template literal under web/src that starts with
+// /api/. It covers API wrappers, raw fetch, downloads and streams alike.
+func ScanWeb(root string) ([]WebSite, error) {
+	var result []WebSite
+	base := filepath.Join(root, "web", "src")
+	err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, err error) error {
+		if err != nil {
+			return err
+		}
+		if entry.IsDir() || !strings.HasSuffix(path, ".ts") {
+			return nil
+		}
+		data, err := os.ReadFile(path)
+		if err != nil {
+			return err
+		}
+		source := string(data)
+		relative, _ := filepath.Rel(root, path)
+		relative = filepath.ToSlash(relative)
+		for index := 0; index < len(source); index++ {
+			quote := source[index]
+			if (quote != '"' && quote != '\'' && quote != '`') || !strings.HasPrefix(source[index+1:], "/api/") {
+				continue
+			}
+			end, err := jsLiteralEnd(source, index)
+			if err != nil {
+				return fmt.Errorf("%s:%d: %w", relative, strings.Count(source[:index], "\n")+1, err)
+			}
+			tail := source[end:]
+			if semicolon := strings.IndexByte(tail, ';'); semicolon >= 0 {
+				tail = tail[:semicolon+1]
+			}
+			hint := "GET/see-source"
+			if match := webMethod.FindStringSubmatch(tail); match != nil {
+				hint = match[1]
+			}
+			result = append(result, WebSite{Source: relative, Line: strings.Count(source[:index], "\n") + 1, MethodHint: hint, Expression: source[index+1 : end-1]})
+			index = end - 1
+		}
+		return nil
+	})
+	if err != nil && !os.IsNotExist(err) {
+		return nil, err
+	}
+	sort.SliceStable(result, func(i, j int) bool {
+		if result[i].Source != result[j].Source {
+			return result[i].Source < result[j].Source
+		}
+		return result[i].Line < result[j].Line
+	})
+	return result, nil
+}
+
+func jsLiteralEnd(source string, start int) (int, error) {
+	quote := source[start]
+	index := start + 1
+	for index < len(source) {
+		char := source[index]
+		switch {
+		case char == '\\':
+			index += 2
+			continue
+		case char == quote:
+			return index + 1, nil
+		case quote == '`' && strings.HasPrefix(source[index:], "${"):
+			index += 2
+			depth := 1
+			for index < len(source) && depth > 0 {
+				char = source[index]
+				if char == '"' || char == '\'' || char == '`' {
+					end, err := jsLiteralEnd(source, index)
+					if err != nil {
+						return 0, err
+					}
+					index = end
+					continue
+				}
+				if char == '{' {
+					depth++
+				} else if char == '}' {
+					depth--
+				}
+				index++
+			}
+			if depth > 0 {
+				return 0, fmt.Errorf("unterminated Web template expression")
+			}
+			continue
+		}
+		index++
+	}
+	return 0, fmt.Errorf("unterminated Web literal")
+}
+
+// sitePattern turns a call-site literal into a regexp over concrete paths:
+// each ${...} may stand for any text up to a query string, and a literal
+// query string is dropped.
+func sitePattern(expression string) (*regexp.Regexp, error) {
+	var builder strings.Builder
+	builder.WriteString("^")
+	for index := 0; index < len(expression); {
+		if strings.HasPrefix(expression[index:], "${") {
+			depth, cursor := 1, index+2
+			for cursor < len(expression) && depth > 0 {
+				switch expression[cursor] {
+				case '{':
+					depth++
+				case '}':
+					depth--
+				case '"', '\'', '`':
+					stop, err := jsLiteralEnd(expression, cursor)
+					if err != nil {
+						return nil, err
+					}
+					cursor = stop
+					continue
+				}
+				cursor++
+			}
+			if depth > 0 {
+				return nil, fmt.Errorf("unterminated template expression")
+			}
+			builder.WriteString(`[^?#]*`)
+			index = cursor
+			continue
+		}
+		if expression[index] == '?' {
+			break
+		}
+		builder.WriteString(regexp.QuoteMeta(expression[index : index+1]))
+		index++
+	}
+	builder.WriteString(`(?:[?#].*)?$`)
+	return regexp.Compile(builder.String())
+}
+
+// samplePath replaces every {param} of a route with a concrete segment.
+func samplePath(route string) string {
+	var builder strings.Builder
+	for index := 0; index < len(route); {
+		if route[index] == '{' {
+			end := strings.IndexByte(route[index:], '}')
+			if end < 0 {
+				builder.WriteString(route[index:])
+				break
+			}
+			builder.WriteString("sample")
+			index += end + 1
+			continue
+		}
+		builder.WriteByte(route[index])
+		index++
+	}
+	return strings.TrimSuffix(builder.String(), "/")
+}
+
+// UnmatchedWebSites returns call sites no registered httpapi route can serve.
+// A Web call to an unregistered path means a registration escaped the scan.
+func UnmatchedWebSites(sites []WebSite, routes []Registration) ([]WebSite, error) {
+	var unmatched []WebSite
+	for _, site := range sites {
+		pattern, err := sitePattern(site.Expression)
+		if err != nil {
+			return nil, fmt.Errorf("%s:%d: %w", site.Source, site.Line, err)
+		}
+		found := false
+		for _, route := range routes {
+			if route.Server != "httpapi" {
+				continue
+			}
+			method, path, _ := strings.Cut(route.Operation, " ")
+			if site.MethodHint != "GET/see-source" && method != "ANY" && method != site.MethodHint {
+				continue
+			}
+			sample := samplePath(path)
+			if pattern.MatchString(sample) || pattern.MatchString(sample+"/") {
+				found = true
+				break
+			}
+		}
+		if !found {
+			unmatched = append(unmatched, site)
+		}
+	}
+	return unmatched, nil
+}
```
