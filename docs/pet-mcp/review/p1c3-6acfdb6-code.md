# P1c v3：Handle 调用逐个对账、运行期 mux 对照与 Web 词法扫描（固定 6acfdb6）

源码 `6acfdb617caef3eb15b11ca8a474c2266e952a93`，tree `598aeccd1fbcab1c7ca798da07a52102c23e00d1`；增量 `cc25be4..6acfdb6` 共 12 个文件。`cc25be4` 是 P1c v2 材料提交；v2 的源码 6d785b3 及其 5 个包（`p1c-6d785b3-*`）不改写，本包是在其上的增量，两者合起来是 P1c 的完整修订。分 2 个包：A 代码、测试与文档，B Web 清单（格式变化，附逐条语义对照）。diff 保留 3 行上下文，不截断。

对应 723 r1（宠物转述原文）：M1 要求所有 `.Handle(`/`.HandleFunc(` 计数对账、解析不了一律报错、桌面表条目对账、最好加运行期对照；S1 未知包装不能一律算 transport_auth；S3 给命令补用例；S4 Web 比对去掉行号；S5 动态基址；S6 注释行。v2 已覆盖 M1 的大部分、M2、M3，本包补齐其余。

固定副本（git archive，blob 逐个核对；测试 TMPDIR 按宠物批准放在 `/tmp/aha2-task722-fixed-6acfdb6`，跑完已删除）验证：

- `go test -json ./internal/petcoverage -count=1` → 退出 0，0.2s，具名用例 9 个全部 run/pass
- `go test -json ./cmd/aha-pet-coverage -count=1` → 退出 0，1.2s，具名用例 1 个全部 run/pass
- `go test -json ./internal/centersync -count=1` → 退出 0，2.9s，具名用例 12 个全部 run/pass
- `go test -json ./internal/httpapi -run '^(TestPetCoverageLedgersMatchRegistrationsAndEvidence|TestPetCoverageScanMatchesLiveMuxes|TestMCPToolCatalogCoversEveryBuiltInServer|TestHealthProbeCarriesNoBusinessData|TestEveryAgentTokenRouteHasExplicitPolicy|TestOpaqueMCPResourceNamesCannotSkipLiveAuthorization|TestUnclassifiedTokenRoutesFailBeforeEitherAuthWrapperDispatches|TestSecurityHeadersAllowXTermStylesWithoutInlineScripts)$' -count=1` → 退出 0，18.6s，具名用例 8 个全部 run/pass
- `go test -json ./internal/centersync -run '^(TestDeviceRenameKeepsCenterGeneratedID|TestHealthAndAuthentication|TestOneTimeDeviceRegistrationStoresOnlyHashes|TestPushIsIdempotentAndPullIsIncremental)$' -count=1 -timeout=30m` → 退出 0，0.9s，具名用例 4 个全部 run/pass
- `go test -json ./internal/httpapi -run '^(TestAgentBrowserMCPRequiresCapabilityAndDomainGate|TestAgentChannelMCPRequiresCapabilityAndExposesOnlyTwoTools|TestAgentCollabMCPRequiresCapabilityAndDomainGate|TestAgentCoreMCPRequiresCapabilityAndRunningTurn|TestAgentHardwareMCPRequiresCapabilityAndDomainGate|TestAgentKnowledgeMCPRequiresCapabilityAndDomainGate|TestAgentMailMCPTools|TestAgentWorkspaceMCPRequiresCapabilityAndDomainGate|TestDesktopMCPUsesExistingShareAndSingleUseObservation|TestHealthProbeCarriesNoBusinessData|TestPetMCPNotificationsAreAcceptedWithoutJSON|TestPetMCPPingReturnsResult|TestRemovedSkillWriteRoutesReturnMethodNotAllowed|TestServeWebServesBrowserNewTabAsHTTPPage|TestServeWebUnknownRouteStillFallsBackToSPA)$' -count=1 -timeout=30m` → 退出 0，68.2s，具名用例 15 个全部 run/pass
- `go vet ./internal/petcoverage ./cmd/aha-pet-coverage ./internal/httpapi ./internal/centersync` → 退出 0，1.2s
- `go run ./cmd/aha-pet-coverage` → 退出 0，0.9s；输出：Handle/HandleFunc calls: 423 (route registrations 421, non-route 2) / HTTP operations: 433 (closed 21, open 412); Web API call sites: 282; ordinary-Task MCP tools: 61 (closed 0)
- `go run ./cmd/aha-pet-coverage -complete` → 退出 1（预期 1：门禁必须失败），0.4s；输出：coverage is incomplete: 412 open operations, 61 open tool rows / exit status 1
- `GOOS=windows go vet ./internal/petcoverage ./cmd/aha-pet-coverage ./internal/httpapi` → 退出 0，5.4s

## A. 代码、测试与文档

- **调用对账（M1）**：`handleTokens` 用 go/scanner 数出每个文件里 `.` Handle|HandleFunc `(` 记号，必须等于 AST 里的调用数；每个调用要么是路由注册（计入 Registering），要么是带原因的非路由调用（目前只有 `internal/instancelock/lock_windows.go` 两处单参数 `windows.Handle` 类型转换），两者之和必须等于调用数。函数体外（例如包级变量里的函数字面量）的调用、两参调用落在非 `http.NewServeMux()` 局部变量 / `*http.ServeMux` 参数的接收者上（含 DefaultServeMux、字段 `h.mux`、接口），都直接报错，不再静默跳过。真实仓库：423 个调用 = 421 个注册 + 2 个非路由。
- **运行期对照（M1）**：`Handler()` 的路由部分原样移进 `routeMux()`（路由行一行未改，diff 只有函数头尾）。`VerifyMux` 对每个扫描出的操作构造请求，经真实 mux 的 `Handler(r)` 必须返回完全相同的模式；`TestPetCoverageScanMatchesLiveMuxes` 覆盖 httpapi（打开 localOwnerIngest 与 knowledgeVault 两个条件分组）和 mail-download，`TestCoverageScanMatchesLiveCenterMux` 覆盖 aha-sync。路由表条目逐行展开后也在这里与运行期逐条对上。
- **S1**：包装函数必须在 `boundaries` 里登记，未知包装直接报错；边界取最外层包装。
- **S3**：命令改为可测的 `run()`，`main_test.go` 在真实检出上验证：检查退出 0、`-complete` 退出 1、未知参数或多余参数退出 2。`-update` 只在合成源码树上测（v2 已有），不在测试里改真实检出。
- **S4–S6**：Web 扫描改为 TS 词法：跳过注释和正则字面量（含模板表达式里的正则，例如 `channels.ts` 的 `/[\\/:*?"<>|]+/g`），`${...}` 里的嵌套字面量单独计入一次；同源动态基址（只由 `${...}`、`/`、`:` 组成）也算，外部绝对 URL 不算；字面量文本里带引号的 `/api/` 路径（`src="/api/..."`）保留。清单去掉行号，同文件相同调用合并为 occurrences。

完整 diff 文件边界：

- `cmd/aha-pet-coverage/main.go`
- `cmd/aha-pet-coverage/main_test.go`（新文件）
- `docs/pet-mcp/README.md`
- `internal/centersync/service_test.go`
- `internal/httpapi/pet_coverage_ledger_test.go`
- `internal/httpapi/server.go`
- `internal/petcoverage/ledger.go`
- `internal/petcoverage/petcoverage_test.go`
- `internal/petcoverage/routes.go`
- `internal/petcoverage/runtime.go`（新文件）
- `internal/petcoverage/web.go`

## 该边界完整 diff

```diff
diff --git a/cmd/aha-pet-coverage/main.go b/cmd/aha-pet-coverage/main.go
index 1738f82..95bca9f 100644
--- a/cmd/aha-pet-coverage/main.go
+++ b/cmd/aha-pet-coverage/main.go
@@ -10,6 +10,7 @@ import (
 	"errors"
 	"flag"
 	"fmt"
+	"io"
 	"os"
 	"path/filepath"
 
@@ -18,27 +19,44 @@ import (
 )
 
 func main() {
-	update := flag.Bool("update", false, "rewrite the inventories; reviewed columns are kept, changed rows become stale")
-	complete := flag.Bool("complete", false, "fail unless every operation and tool row has a closing status with valid evidence")
-	flag.Parse()
+	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
+}
+
+func run(args []string, stdout, stderr io.Writer) int {
+	flags := flag.NewFlagSet("aha-pet-coverage", flag.ContinueOnError)
+	flags.SetOutput(stderr)
+	update := flags.Bool("update", false, "rewrite the inventories; reviewed columns are kept, changed rows become stale")
+	complete := flags.Bool("complete", false, "fail unless every operation and tool row has a closing status with valid evidence")
+	if err := flags.Parse(args); err != nil {
+		return 2
+	}
+	if flags.NArg() > 0 {
+		fmt.Fprintln(stderr, "unexpected arguments:", flags.Args())
+		return 2
+	}
 	root, err := moduleRoot()
 	if err != nil {
-		fmt.Fprintln(os.Stderr, err)
-		os.Exit(2)
+		fmt.Fprintln(stderr, err)
+		return 2
 	}
 	report, err := petcoverage.Check(root, petcoverage.NewCatalog(httpapi.MCPToolCatalog()), *update, *complete)
 	for _, key := range report.Stale {
-		fmt.Println("stale (re-verify):", key)
+		fmt.Fprintln(stdout, "stale (re-verify):", key)
 	}
 	for _, key := range report.Removed {
-		fmt.Println("removed:", key)
+		fmt.Fprintln(stdout, "removed:", key)
+	}
+	for _, call := range report.NonRouteCalls {
+		fmt.Fprintln(stdout, "non-route Handle call:", call)
 	}
-	fmt.Printf("HTTP operations: %d (closed %d, open %d); Web literal call sites: %d; ordinary-Task MCP tools: %d (closed %d)\n",
+	fmt.Fprintf(stdout, "Handle/HandleFunc calls: %d (route registrations %d, non-route %d)\n", report.HandleCalls, report.RegisteringCalls, len(report.NonRouteCalls))
+	fmt.Fprintf(stdout, "HTTP operations: %d (closed %d, open %d); Web API call sites: %d; ordinary-Task MCP tools: %d (closed %d)\n",
 		report.Operations, report.Final, report.Open, report.WebSites, report.Tools, report.ToolsFinal)
 	if err != nil {
-		fmt.Fprintln(os.Stderr, err)
-		os.Exit(1)
+		fmt.Fprintln(stderr, err)
+		return 1
 	}
+	return 0
 }
 
 func moduleRoot() (string, error) {
diff --git a/cmd/aha-pet-coverage/main_test.go b/cmd/aha-pet-coverage/main_test.go
new file mode 100644
index 0000000..7446455
--- /dev/null
+++ b/cmd/aha-pet-coverage/main_test.go
@@ -0,0 +1,31 @@
+package main
+
+import (
+	"bytes"
+	"strings"
+	"testing"
+)
+
+// The CLI runs against the real repository: the checked-in ledgers pass the
+// drift/evidence check, -complete still fails while rows are open, and bad
+// arguments are rejected. -update is exercised on synthetic trees in
+// internal/petcoverage, never on the real checkout from a test.
+func TestCommandChecksRealLedgersAndKeepsCompleteGateClosed(t *testing.T) {
+	var stdout, stderr bytes.Buffer
+	if code := run(nil, &stdout, &stderr); code != 0 {
+		t.Fatalf("check exit %d: %s%s", code, stdout.String(), stderr.String())
+	}
+	if !strings.Contains(stdout.String(), "HTTP operations: ") || !strings.Contains(stdout.String(), "Handle/HandleFunc calls: ") {
+		t.Fatalf("summary missing: %s", stdout.String())
+	}
+	stdout.Reset()
+	stderr.Reset()
+	if code := run([]string{"-complete"}, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "coverage is incomplete") {
+		t.Fatalf("complete gate exit %d: %s", code, stderr.String())
+	}
+	for _, args := range [][]string{{"-unknown"}, {"extra"}} {
+		if code := run(args, &stdout, &stderr); code != 2 {
+			t.Fatalf("%v exit %d", args, code)
+		}
+	}
+}
diff --git a/docs/pet-mcp/README.md b/docs/pet-mcp/README.md
index 476e4aa..80982ef 100644
--- a/docs/pet-mcp/README.md
+++ b/docs/pet-mcp/README.md
@@ -8,8 +8,10 @@
 
 三份账本都由 `internal/petcoverage` 生成和校验，它用 Go 自己的语法解析器读源码，不靠文本正则：
 
-- `coverage.tsv`：`internal/` 与 `cmd/` 下所有 `ServeMux` 注册（任意变量名、常量、拼接、局部前缀、字面量路由表都会展开），按监听分 `httpapi`、`mail-download`、`centersync`。每行带源文件、handler、认证边界（由外层包装推出）和 handler 指纹（包装链加 handler 源码的哈希）。注册在未登记的函数里、注册到 DefaultServeMux 或无法识别的接收者上、路由串无法求值、handler 找不到唯一声明，扫描都会直接失败。
-- `web-call-sites.tsv`：`web/src` 里所有以 `/api/` 开头的字符串/模板字面量。每个调用点必须能对上一条已注册的 httpapi 路由（方法明确时方法也要一致），对不上就说明有注册逃过了扫描，校验失败。
+- `coverage.tsv`：`internal/` 与 `cmd/` 下所有 `ServeMux` 注册（任意变量名、常量、拼接、局部前缀、字面量路由表都会展开），按监听分 `httpapi`、`mail-download`、`centersync`。每行带源文件、handler、认证边界（由外层包装推出）和 handler 指纹（包装链加 handler 源码的哈希）。
+  - 对账：源码里每个 `.Handle(` / `.HandleFunc(` 词法记号都要对上一处解析出的调用，并且要么是路由注册，要么列为非路由调用并写明原因（目前只有 `instancelock` 里两处单参数的 `windows.Handle` 类型转换）。函数体外的调用、注册到 DefaultServeMux 或非 `http.NewServeMux()`/`*http.ServeMux` 参数的接收者、未登记的注册函数、无法求值的路由串、找不到唯一声明的 handler、没有登记边界的包装函数，扫描都会直接失败。
+  - 运行期对照：`TestPetCoverageScanMatchesLiveMuxes`（httpapi 与 mail-download）和 `TestCoverageScanMatchesLiveCenterMux`（aha-sync）用真实 mux 的 `Handler(r)` 逐条确认，扫描出的每个操作都被路由到完全相同的模式。主监听的注册放在 `routeMux()` 里，就是为了这一步能拿到真实 mux。
+- `web-call-sites.tsv`：`web/src` 里所有点名 AHA2 API 的字面量：以 `/api/` 开头的字符串/模板、经同源动态基址（只由 `${...}`、`/`、`:` 组成，例如 `${protocol}//${location.host}`）到达 `/api/` 的模板，以及字面量文本里带引号的 `/api/` 路径（例如 `src="/api/..."`）。扫描是真正的 TS 词法：注释、正则字面量（含模板表达式里的正则）跳过，`${...}` 里的嵌套字面量单独计入且不重复。清单不带行号，同一文件里相同的调用合并为 occurrences，挪动代码不会改账本。每个调用点必须能对上一条已注册的 httpapi 路由（方法明确时方法也要一致），对不上就说明有注册逃过了扫描。
 - `task-mcp-tools.tsv`：普通 Task 可用的全部内置 MCP 工具（`httpapi.MCPToolCatalog()`，宠物自己的 server 除外），逐个记录宠物 Turn 如何拿到同样能力。
 
 ```sh
@@ -19,7 +21,7 @@ go run ./cmd/aha-pet-coverage -complete  # 最终门禁：所有行都关闭且
 go test ./internal/petcoverage ./internal/httpapi -run 'PetCoverage|MCPToolCatalog|HealthProbe'
 ```
 
-`go test ./internal/httpapi` 里的 `TestPetCoverageLedgersMatchRegistrationsAndEvidence` 做的就是第一条命令：新增、删除或改动路由而不更新账本，测试失败。
+`go test ./internal/httpapi` 里的 `TestPetCoverageLedgersMatchRegistrationsAndEvidence` 做的就是第一条命令：新增、删除或改动路由而不更新账本，测试失败。`go test ./cmd/aha-pet-coverage` 在真实仓库上验证命令本身：检查通过、`-complete` 失败、错误参数退出 2（`-update` 只在合成源码树上测，不在测试里改真实检出）。
 
 状态分两类。开放：`pending`、`implemented`、`implemented_unverified`、`stale`。`stale` 表示该行之前审过，但 handler、源文件、边界或指纹已经变了，旧结论不再成立，必须重新核对后才能改回关闭状态；从 v1 账本迁移时，所有没有指纹的已审行都按这个规则退回了 stale。关闭：`direct`、`equivalent`、`protected_handoff`、`transport_na`、`web_prohibited_na`。关闭行的 mcp、permission、protected_flow、tests 四列都不能为空或 pending，并且：
 
diff --git a/internal/centersync/service_test.go b/internal/centersync/service_test.go
index fe9671b..11fb906 100644
--- a/internal/centersync/service_test.go
+++ b/internal/centersync/service_test.go
@@ -15,6 +15,7 @@ import (
 	"time"
 
 	"github.com/ChinaKai/AHA2/internal/domain"
+	"github.com/ChinaKai/AHA2/internal/petcoverage"
 )
 
 func testStore(t *testing.T) *Store {
@@ -427,3 +428,20 @@ func TestConflictResponseEncodesEmptyRemotePayload(t *testing.T) {
 		t.Fatalf("conflicts=%#v", response.Conflicts)
 	}
 }
+
+// The pet MCP coverage scan inventories the aha-sync listener statically;
+// every scanned operation must be served by the real mux under that pattern.
+func TestCoverageScanMatchesLiveCenterMux(t *testing.T) {
+	routes, _, err := petcoverage.ScanRoutes(filepath.Join("..", ".."))
+	if err != nil {
+		t.Fatal(err)
+	}
+	mux, ok := testStore(t).Handler().(*http.ServeMux)
+	if !ok {
+		t.Fatal("center handler is not a ServeMux")
+	}
+	checked, err := petcoverage.VerifyMux(mux, routes, "centersync")
+	if err != nil || checked != 6 {
+		t.Fatalf("checked=%d err=%v", checked, err)
+	}
+}
diff --git a/internal/httpapi/pet_coverage_ledger_test.go b/internal/httpapi/pet_coverage_ledger_test.go
index 21ffc3e..b511e98 100644
--- a/internal/httpapi/pet_coverage_ledger_test.go
+++ b/internal/httpapi/pet_coverage_ledger_test.go
@@ -7,6 +7,7 @@ import (
 	"path/filepath"
 	"testing"
 
+	"github.com/ChinaKai/AHA2/internal/knowledgevault"
 	"github.com/ChinaKai/AHA2/internal/petcoverage"
 )
 
@@ -52,3 +53,34 @@ func TestHealthProbeCarriesNoBusinessData(t *testing.T) {
 		t.Fatalf("health probe changed: %d %s", recorder.Code, recorder.Body.String())
 	}
 }
+
+// The static scan must agree with the muxes the server really builds: every
+// scanned httpapi and mail-download operation is routed to exactly its
+// pattern. Conditional groups are switched on so all of them are registered.
+func TestPetCoverageScanMatchesLiveMuxes(t *testing.T) {
+	routes, stats, err := petcoverage.ScanRoutes(filepath.Join("..", ".."))
+	if err != nil {
+		t.Fatal(err)
+	}
+	if stats.Calls != stats.Registering+len(stats.NonRoute) {
+		t.Fatalf("unreconciled Handle calls: %+v", stats)
+	}
+	server := New(Config{})
+	server.localOwnerIngest = true
+	server.knowledgeVault = &knowledgevault.Manager{}
+	main, err := petcoverage.VerifyMux(server.routeMux(), routes, "httpapi")
+	if err != nil {
+		t.Fatal(err)
+	}
+	download, ok := server.MailDownloadHandler().(*http.ServeMux)
+	if !ok {
+		t.Fatal("mail download handler is not a ServeMux")
+	}
+	mail, err := petcoverage.VerifyMux(download, routes, "mail-download")
+	if err != nil {
+		t.Fatal(err)
+	}
+	if main < 400 || mail != 2 {
+		t.Fatalf("verified %d main and %d mail-download operations", main, mail)
+	}
+}
diff --git a/internal/httpapi/server.go b/internal/httpapi/server.go
index 26ebeb5..5d76ff4 100644
--- a/internal/httpapi/server.go
+++ b/internal/httpapi/server.go
@@ -263,6 +263,12 @@ func New(config Config) *Server {
 }
 
 func (s *Server) Handler() http.Handler {
+	return s.securityHeaders(s.requestLog(s.slowRequestLog(s.enforceAccessScope(s.repairTextEncoding(s.routeMux())))))
+}
+
+// routeMux registers every route of the main listener. It is separate from
+// Handler so the coverage inventory can be checked against the real mux.
+func (s *Server) routeMux() *http.ServeMux {
 	mux := http.NewServeMux()
 	mux.HandleFunc("GET /healthz", s.health)
 	if s.localOwnerIngest {
@@ -692,7 +698,7 @@ func (s *Server) Handler() http.Handler {
 	mux.Handle("DELETE /api/v1/skills/{id}", s.withAuth(http.HandlerFunc(s.rejectRemovedSkillWrite)))
 
 	mux.HandleFunc("/", s.serveWeb)
-	return s.securityHeaders(s.requestLog(s.slowRequestLog(s.enforceAccessScope(s.repairTextEncoding(mux)))))
+	return mux
 }
 
 func (s *Server) securityHeaders(next http.Handler) http.Handler {
diff --git a/internal/petcoverage/ledger.go b/internal/petcoverage/ledger.go
index 0a99b66..1ee946f 100644
--- a/internal/petcoverage/ledger.go
+++ b/internal/petcoverage/ledger.go
@@ -20,7 +20,7 @@ const (
 )
 
 var ledgerFields = []string{"operation", "server", "source", "handler", "boundary", "fingerprint", "mcp", "permission", "protected_flow", "tests", "status"}
-var webFields = []string{"source", "line", "method_hint", "expression"}
+var webFields = []string{"source", "method_hint", "expression", "occurrences"}
 var toolFields = []string{"tool", "pet_access", "permission", "tests", "status"}
 
 // Final statuses close a row. Everything else keeps the gate open.
@@ -240,6 +240,10 @@ func ValidateRow(root string, catalog Catalog, route Registration, row Row) erro
 type Report struct {
 	Operations, Final, Open, WebSites, Tools, ToolsFinal int
 	Stale, Removed                                       []string
+	// Every .Handle/.HandleFunc call in scanned Go source is either one of
+	// RegisteringCalls or listed in NonRouteCalls with its reason.
+	HandleCalls, RegisteringCalls int
+	NonRouteCalls                 []string
 }
 
 // Check regenerates every inventory, compares it with the checked-in files
@@ -247,10 +251,11 @@ type Report struct {
 // complete it also requires every operation and tool row to be closed.
 func Check(root string, catalog Catalog, update, complete bool) (Report, error) {
 	var report Report
-	routes, err := ScanRoutes(root)
+	routes, stats, err := ScanRoutes(root)
 	if err != nil {
 		return report, err
 	}
+	report.HandleCalls, report.RegisteringCalls, report.NonRouteCalls = stats.Calls, stats.Registering, stats.NonRoute
 	previous, _, err := readTSV(filepath.Join(root, LedgerPath))
 	if err != nil {
 		return report, err
@@ -268,13 +273,13 @@ func Check(root string, catalog Catalog, update, complete bool) (Report, error)
 	if len(unmatched) > 0 {
 		var lines []string
 		for _, site := range unmatched {
-			lines = append(lines, fmt.Sprintf("%s:%d %s %s", site.Source, site.Line, site.MethodHint, site.Expression))
+			lines = append(lines, fmt.Sprintf("%s %s %s", site.Source, site.MethodHint, site.Expression))
 		}
 		return report, fmt.Errorf("Web calls with no registered route (a registration escaped the scan):\n%s", strings.Join(lines, "\n"))
 	}
 	webRows := make([]Row, 0, len(sites))
 	for _, site := range sites {
-		webRows = append(webRows, Row{"source": site.Source, "line": fmt.Sprint(site.Line), "method_hint": site.MethodHint, "expression": site.Expression})
+		webRows = append(webRows, Row{"source": site.Source, "method_hint": site.MethodHint, "expression": site.Expression, "occurrences": fmt.Sprint(site.Occurrences)})
 	}
 	toolsPrevious, _, err := readTSV(filepath.Join(root, ToolsPath))
 	if err != nil {
diff --git a/internal/petcoverage/petcoverage_test.go b/internal/petcoverage/petcoverage_test.go
index 0de518c..9592701 100644
--- a/internal/petcoverage/petcoverage_test.go
+++ b/internal/petcoverage/petcoverage_test.go
@@ -1,6 +1,7 @@
 package petcoverage
 
 import (
+	"net/http"
 	"os"
 	"path/filepath"
 	"strings"
@@ -89,10 +90,14 @@ func (s *Server) tables(mux *http.ServeMux) {
 }
 `,
 	})
-	routes, err := ScanRoutes(root)
+	routes, stats, err := ScanRoutes(root)
 	if err != nil {
 		t.Fatal(err)
 	}
+	// 7 routes from 6 Handle calls (each table call counts once); every call is accounted.
+	if stats.Calls != 6 || stats.Registering != 6 || len(stats.NonRoute) != 0 {
+		t.Fatalf("stats = %+v", stats)
+	}
 	got := map[string]Registration{}
 	for _, route := range routes {
 		got[route.Operation] = route
@@ -123,17 +128,22 @@ func (s *Server) tables(mux *http.ServeMux) {
 func TestScanFailsClosedOnRegistrationsItCannotAccountFor(t *testing.T) {
 	cases := map[string][2]string{
 		"unclassified function": {`func (s *Server) Other() { mux := http.NewServeMux(); mux.Handle("GET /x", http.HandlerFunc(s.list)) }`, "not classified"},
-		"default serve mux":     {`func (s *Server) Routes() { http.Handle("GET /x", http.HandlerFunc(s.list)) }`, "unrecognized receiver"},
+		"default serve mux":     {`func (s *Server) Routes() { http.Handle("GET /x", http.HandlerFunc(s.list)) }`, "DefaultServeMux"},
 		"unknown receiver":      {`func (s *Server) Routes(r interface{ Handle(string, http.Handler) }) { r.Handle("/x", http.HandlerFunc(s.list)) }`, "unrecognized receiver"},
 		"dynamic pattern":       {`func (s *Server) Routes(pattern string) { mux := http.NewServeMux(); mux.Handle(pattern, http.HandlerFunc(s.list)) }`, "unresolvable route pattern"},
 		"duplicate":             {`func (s *Server) Routes() { mux := http.NewServeMux(); mux.Handle("GET /x", http.HandlerFunc(s.list)); mux.Handle("GET /x", http.HandlerFunc(s.status)) }`, "duplicate route"},
 		"unknown handler":       {`func (s *Server) Routes() { mux := http.NewServeMux(); mux.Handle("GET /x", http.HandlerFunc(s.missing)) }`, "no unique declaration"},
+		"multi-line call":       {"func (s *Server) Routes(r interface{ Handle(string, http.Handler) }) {\n\tr.Handle(\n\t\t\"/x\",\n\t\thttp.HandlerFunc(s.list),\n\t)\n}", "unrecognized receiver"},
+		"non-route lookalike":   {`func (s *Server) Routes(r interface{ Handle(int, int) }) { r.Handle(1, 2) }`, "unrecognized receiver"},
+		"helper on field":       {`type holder struct{ mux *http.ServeMux }; func (s *Server) Routes(h holder) { h.mux.Handle("GET /x", http.HandlerFunc(s.list)) }`, "unrecognized receiver"},
+		"package-level literal": {`var register = func(mux *http.ServeMux, s *Server) { mux.Handle("GET /x", http.HandlerFunc(s.list)) }`, "outside a function body"},
+		"unknown wrapper":       {`func (s *Server) wrap(next http.Handler) http.Handler { return next }; func (s *Server) Routes() { mux := http.NewServeMux(); mux.Handle("GET /x", s.wrap(http.HandlerFunc(s.list))) }`, "no reviewed boundary"},
 	}
 	for name, item := range cases {
 		t.Run(name, func(t *testing.T) {
 			classify(t, map[string]string{"internal/api (*Server).Routes": "httpapi"})
 			root := writeTree(t, map[string]string{"internal/api/server.go": serverPreamble + item[0] + "\n"})
-			if _, err := ScanRoutes(root); err == nil || !strings.Contains(err.Error(), item[1]) {
+			if _, _, err := ScanRoutes(root); err == nil || !strings.Contains(err.Error(), item[1]) {
 				t.Fatalf("scan error = %v, want %q", err, item[1])
 			}
 		})
@@ -225,43 +235,115 @@ func TestEvidenceRulesRejectVagueOrMisappliedClosure(t *testing.T) {
 	}
 }
 
+func TestScanCountsNonRouteHandleCalls(t *testing.T) {
+	classify(t, map[string]string{"internal/api (*Server).Routes": "httpapi"})
+	root := writeTree(t, map[string]string{"internal/api/server.go": serverPreamble + `
+type conv int
+
+func Handle(value int) conv { return conv(value) }
+
+// mux.Handle("GET /commented", s.list) is not a call.
+var note = "mux.Handle(\"GET /string\", s.list)"
+
+func (s *Server) Routes() {
+	mux := http.NewServeMux()
+	_ = api.Handle(1)
+	mux.Handle("GET /x", http.HandlerFunc(s.list))
+}
+
+var api = struct{ Handle func(int) conv }{Handle}
+`})
+	routes, stats, err := ScanRoutes(root)
+	if err != nil {
+		t.Fatal(err)
+	}
+	if len(routes) != 1 || stats.Calls != 2 || stats.Registering != 1 || len(stats.NonRoute) != 1 || !strings.Contains(stats.NonRoute[0], "1 arguments") {
+		t.Fatalf("routes=%d stats=%+v", len(routes), stats)
+	}
+}
+
 func TestWebSitesMustMatchARegisteredRoute(t *testing.T) {
 	routes := []Registration{
 		{Operation: "GET /api/v1/projects", Server: "httpapi"},
 		{Operation: "PATCH /api/v1/tasks/{id}", Server: "httpapi"},
 		{Operation: "GET /api/v1/knowledge-vault/git/status", Server: "httpapi"},
+		{Operation: "GET /api/v1/tasks/{id}/hardware/{hardware}/terminal/ws", Server: "httpapi"},
 		{Operation: "GET /api/v1/other", Server: "centersync"},
 	}
 	sites := []WebSite{
-		{Source: "a.ts", Line: 1, MethodHint: "GET/see-source", Expression: "/api/v1/projects${query.size ? `?${query}` : \"\"}"},
-		{Source: "a.ts", Line: 2, MethodHint: "PATCH", Expression: "/api/v1/tasks/${encodeURIComponent(id)}"},
-		{Source: "a.ts", Line: 3, MethodHint: "GET/see-source", Expression: "/api/v1/knowledge-vault/git/${action}"},
-		{Source: "a.ts", Line: 4, MethodHint: "DELETE", Expression: "/api/v1/tasks/${id}"},
-		{Source: "a.ts", Line: 5, MethodHint: "GET/see-source", Expression: "/api/v1/unregistered"},
-		{Source: "a.ts", Line: 6, MethodHint: "GET/see-source", Expression: "/api/v1/other"},
+		{Source: "a.ts", MethodHint: "GET/see-source", Expression: "/api/v1/projects${query.size ? `?${query}` : \"\"}"},
+		{Source: "a.ts", MethodHint: "PATCH", Expression: "/api/v1/tasks/${encodeURIComponent(id)}"},
+		{Source: "a.ts", MethodHint: "GET/see-source", Expression: "/api/v1/knowledge-vault/git/${action}"},
+		{Source: "a.ts", MethodHint: "GET/see-source", Expression: "${protocol}//${location.host}/api/v1/tasks/${id}/hardware/${hw}/terminal/ws?${query}"},
+		{Source: "b.ts", MethodHint: "DELETE", Expression: "/api/v1/tasks/${id}"},
+		{Source: "b.ts", MethodHint: "GET/see-source", Expression: "/api/v1/unregistered"},
+		{Source: "b.ts", MethodHint: "GET/see-source", Expression: "/api/v1/other"},
 	}
 	unmatched, err := UnmatchedWebSites(sites, routes)
 	if err != nil {
 		t.Fatal(err)
 	}
-	var lines []int
-	for _, site := range unmatched {
-		lines = append(lines, site.Line)
+	if len(unmatched) != 3 {
+		t.Fatalf("unmatched = %+v, want the three b.ts sites", unmatched)
 	}
-	if len(lines) != 3 || lines[0] != 4 || lines[1] != 5 || lines[2] != 6 {
-		t.Fatalf("unmatched lines = %v, want [4 5 6]", lines)
+	for _, site := range unmatched {
+		if site.Source != "b.ts" {
+			t.Fatalf("matched site reported: %+v", site)
+		}
 	}
 }
 
-func TestScanWebKeepsNestedTemplatesAndMethodHints(t *testing.T) {
-	root := writeTree(t, map[string]string{"web/src/api.ts": "return this.request(`/api/v1/tasks/${encodeURIComponent(id)}`, {method: \"PATCH\", body: \"{}\"});\nfetch(\"/api/v1/system\");\nreturn this.request(`/api/v1/projects${query.size ? `?${query}` : \"\"}`);\n"})
+func TestScanWebLexesCommentsRegexpsTemplatesAndDynamicBases(t *testing.T) {
+	root := writeTree(t, map[string]string{"web/src/api.ts": strings.Join([]string{
+		"return this.request(`/api/v1/tasks/${encodeURIComponent(id)}`, {method: \"PATCH\", body: \"{}\"});",
+		"fetch(\"/api/v1/system\");",
+		"fetch(\"/api/v1/system\");",
+		"return this.request(`/api/v1/projects${query.size ? `?${query}` : \"\"}`);",
+		"// fetch(\"/api/v1/commented-out\");",
+		"/* fetch('/api/v1/block-comment'); */",
+		"const pattern = /[\"']\\/api\\//g; const ratio = total / 2;",
+		"const url = `${protocol}//${location.host}/api/v1/tasks/${id}/hardware/${hw}/terminal/ws?${query}`;",
+		"const external = \"https://openrouter.ai/api/v1\";",
+		"const nested = `${flag ? \"/api/v1/nested-a\" : '/api/v1/nested-b'}`;",
+		"const link = \"http://example.test/x\"; fetch('/api/v1/after-url');",
+		"state.innerHTML = `<img src=\"/api/v1/embedded/${encodeURIComponent(id)}/qr\" alt=\"x\">${flag ? \"/api/v1/inside-expr\" : \"\"}`;",
+		"link.download = `${name.replace(/[\\\\/:*?\"<>|]+/g, \"-\")}-archive.json`;",
+	}, "\n") + "\n"})
 	sites, err := ScanWeb(root)
 	if err != nil {
 		t.Fatal(err)
 	}
-	if len(sites) != 3 || sites[0].MethodHint != "PATCH" || sites[1].Line != 2 || sites[2].Expression != "/api/v1/projects${query.size ? `?${query}` : \"\"}" {
+	got := map[string]WebSite{}
+	for _, site := range sites {
+		got[site.Expression] = site
+	}
+	for _, expression := range []string{"/api/v1/commented-out", "/api/v1/block-comment", "https://openrouter.ai/api/v1"} {
+		if _, ok := got[expression]; ok {
+			t.Fatalf("%s should not be a call site", expression)
+		}
+	}
+	want := map[string]string{
+		"/api/v1/tasks/${encodeURIComponent(id)}": "PATCH",
+		"/api/v1/system": "GET/see-source",
+		"/api/v1/projects${query.size ? `?${query}` : \"\"}":                                   "GET/see-source",
+		"${protocol}//${location.host}/api/v1/tasks/${id}/hardware/${hw}/terminal/ws?${query}": "GET/see-source",
+		"/api/v1/nested-a":  "GET/see-source",
+		"/api/v1/nested-b":  "GET/see-source",
+		"/api/v1/after-url": "GET/see-source",
+		"/api/v1/embedded/${encodeURIComponent(id)}/qr": "GET/see-source",
+		"/api/v1/inside-expr":                           "GET/see-source",
+	}
+	if len(got) != len(want) {
 		t.Fatalf("sites = %+v", sites)
 	}
+	for expression, hint := range want {
+		if got[expression].MethodHint != hint {
+			t.Fatalf("%s = %+v", expression, got[expression])
+		}
+	}
+	if got["/api/v1/system"].Occurrences != 2 {
+		t.Fatal("identical calls in one file were not folded")
+	}
 }
 
 func TestCheckDetectsDriftAndCompleteRequiresClosure(t *testing.T) {
@@ -295,3 +377,26 @@ func TestCheckDetectsDriftAndCompleteRequiresClosure(t *testing.T) {
 		t.Fatal("handler change did not invalidate the ledger")
 	}
 }
+
+func TestVerifyMuxRejectsScannedRoutesTheLiveMuxDoesNotServe(t *testing.T) {
+	mux := http.NewServeMux()
+	ok := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
+	mux.Handle("GET /api/v1/tasks/{id}", ok)
+	mux.Handle("GET /api/v1/files/{path...}", ok)
+	mux.Handle("/api/v1/prompts", ok)
+	mux.Handle("GET /api/v1/prompts", ok)
+	routes := []Registration{
+		{Operation: "GET /api/v1/tasks/{id}", Server: "httpapi"},
+		{Operation: "GET /api/v1/files/{path...}", Server: "httpapi"},
+		{Operation: "ANY /api/v1/prompts", Server: "httpapi"},
+		{Operation: "GET /api/v1/prompts", Server: "httpapi"},
+		{Operation: "GET /elsewhere", Server: "centersync"},
+	}
+	if checked, err := VerifyMux(mux, routes, "httpapi"); err != nil || checked != 4 {
+		t.Fatalf("checked=%d err=%v", checked, err)
+	}
+	routes = append(routes, Registration{Operation: "POST /api/v1/tasks/{id}", Server: "httpapi"}, Registration{Operation: "GET /api/v1/missing", Server: "httpapi"})
+	if _, err := VerifyMux(mux, routes, "httpapi"); err == nil || !strings.Contains(err.Error(), "POST /api/v1/tasks/{id}") || !strings.Contains(err.Error(), "GET /api/v1/missing") {
+		t.Fatalf("live mismatch not reported: %v", err)
+	}
+}
diff --git a/internal/petcoverage/routes.go b/internal/petcoverage/routes.go
index c2655e3..af94891 100644
--- a/internal/petcoverage/routes.go
+++ b/internal/petcoverage/routes.go
@@ -13,6 +13,7 @@ import (
 	"go/ast"
 	"go/parser"
 	"go/printer"
+	"go/scanner"
 	"go/token"
 	"io/fs"
 	"os"
@@ -37,13 +38,22 @@ type Registration struct {
 // registeringFunctions classifies every function that registers routes. A
 // new registering function fails the scan until a reviewer classifies it.
 var registeringFunctions = map[string]string{
-	"internal/httpapi (*Server).Handler":               "httpapi",
+	"internal/httpapi (*Server).routeMux":              "httpapi",
 	"internal/httpapi (*Server).registerBrowserRoutes": "httpapi",
 	"internal/httpapi (*Server).registerDesktopRoutes": "httpapi",
 	"internal/httpapi (*Server).MailDownloadHandler":   "mail-download",
 	"internal/centersync (*Store).Handler":             "centersync",
 }
 
+// nonRouteHandleCalls lists two-argument Handle/HandleFunc calls that are
+// reviewed as not registering an HTTP route, keyed by
+// "<package dir> <function> <receiver>". Every other such call on a value that
+// is not a ServeMux fails the scan.
+var nonRouteHandleCalls = map[string]string{}
+
+// boundaries maps every accepted wrapper to its authentication boundary. A
+// wrapper missing here fails the scan instead of silently becoming
+// transport_auth.
 var boundaries = map[string]string{
 	"withAgentCapability":   "agent_turn",
 	"withOwnerOrAgentAuth":  "owner_or_agent",
@@ -60,6 +70,7 @@ type goPackage struct {
 	dir       string
 	fset      *token.FileSet
 	files     map[string]*ast.File
+	sources   map[string][]byte
 	constants map[string]ast.Expr
 	methods   map[string]*ast.FuncDecl // handler name -> method declaration
 }
@@ -87,14 +98,19 @@ func loadPackages(root string) (map[string]*goPackage, error) {
 			dir = filepath.ToSlash(dir)
 			pkg := packages[dir]
 			if pkg == nil {
-				pkg = &goPackage{dir: dir, fset: token.NewFileSet(), files: map[string]*ast.File{}, constants: map[string]ast.Expr{}, methods: map[string]*ast.FuncDecl{}}
+				pkg = &goPackage{dir: dir, fset: token.NewFileSet(), files: map[string]*ast.File{}, sources: map[string][]byte{}, constants: map[string]ast.Expr{}, methods: map[string]*ast.FuncDecl{}}
 				packages[dir] = pkg
 			}
-			file, err := parser.ParseFile(pkg.fset, path, nil, parser.SkipObjectResolution)
+			content, err := os.ReadFile(path)
+			if err != nil {
+				return err
+			}
+			file, err := parser.ParseFile(pkg.fset, path, content, parser.SkipObjectResolution)
 			if err != nil {
 				return err
 			}
 			pkg.files[dir+"/"+entry.Name()] = file
+			pkg.sources[dir+"/"+entry.Name()] = content
 			return nil
 		})
 		if err != nil && !os.IsNotExist(err) {
@@ -143,11 +159,21 @@ func functionKey(declaration *ast.FuncDecl) string {
 	return "(" + receiver.String() + ")." + declaration.Name.Name
 }
 
+// ScanStats reconciles every Handle/HandleFunc call in the scanned source:
+// Calls counts the token sequences `.Handle(` / `.HandleFunc(`, and each one
+// is either a route registration or a reviewed non-route call.
+type ScanStats struct {
+	Calls       int
+	Registering int
+	NonRoute    []string
+}
+
 // ScanRoutes returns every route registration under root, sorted by operation.
-func ScanRoutes(root string) ([]Registration, error) {
+func ScanRoutes(root string) ([]Registration, ScanStats, error) {
+	var stats ScanStats
 	packages, err := loadPackages(root)
 	if err != nil {
-		return nil, err
+		return nil, stats, err
 	}
 	var result []Registration
 	seen := map[string]string{}
@@ -164,24 +190,51 @@ func ScanRoutes(root string) ([]Registration, error) {
 		}
 		sort.Strings(names)
 		for _, source := range names {
+			tokens := handleTokens(pkg.sources[source])
+			calls := 0
+			ast.Inspect(pkg.files[source], func(node ast.Node) bool {
+				if call, ok := node.(*ast.CallExpr); ok && isHandleCall(call) {
+					calls++
+				}
+				return true
+			})
+			if tokens != calls {
+				return nil, stats, fmt.Errorf("%s: %d .Handle/.HandleFunc tokens but %d parsed calls; review the scanner", source, tokens, calls)
+			}
+			stats.Calls += calls
+			accounted := 0
 			for _, declaration := range pkg.files[source].Decls {
 				function, ok := declaration.(*ast.FuncDecl)
 				if !ok || function.Body == nil {
+					outside := 0
+					ast.Inspect(declaration, func(node ast.Node) bool {
+						if call, ok := node.(*ast.CallExpr); ok && isHandleCall(call) {
+							outside++
+						}
+						return true
+					})
+					if outside > 0 {
+						return nil, stats, fmt.Errorf("%s: Handle/HandleFunc call outside a function body", source)
+					}
 					continue
 				}
-				found, err := scanFunction(pkg, source, function)
+				found, handled, err := scanFunction(pkg, source, function, &stats)
 				if err != nil {
-					return nil, err
+					return nil, stats, err
 				}
+				accounted += handled
 				for _, item := range found {
 					key := item.Server + " " + item.Operation
 					if previous, exists := seen[key]; exists {
-						return nil, fmt.Errorf("duplicate route %s (%s and %s)", key, previous, item.Source)
+						return nil, stats, fmt.Errorf("duplicate route %s (%s and %s)", key, previous, item.Source)
 					}
 					seen[key] = item.Source
 					result = append(result, item)
 				}
 			}
+			if accounted != calls {
+				return nil, stats, fmt.Errorf("%s: %d Handle/HandleFunc calls but %d accounted for", source, calls, accounted)
+			}
 		}
 	}
 	sort.Slice(result, func(i, j int) bool {
@@ -190,7 +243,38 @@ func ScanRoutes(root string) ([]Registration, error) {
 		}
 		return result[i].Server < result[j].Server
 	})
-	return result, nil
+	sort.Strings(stats.NonRoute)
+	return result, stats, nil
+}
+
+func isHandleCall(call *ast.CallExpr) bool {
+	selector, ok := call.Fun.(*ast.SelectorExpr)
+	return ok && (selector.Sel.Name == "Handle" || selector.Sel.Name == "HandleFunc")
+}
+
+// handleTokens counts `.` Handle|HandleFunc `(` in the token stream, so a call
+// the AST walk cannot see (or a scanner bug) shows up as a mismatch. Comments
+// and string contents are not tokens and do not count.
+func handleTokens(source []byte) int {
+	var lexer scanner.Scanner
+	fileSet := token.NewFileSet()
+	lexer.Init(fileSet.AddFile("", fileSet.Base(), len(source)), source, nil, 0)
+	count := 0
+	var previous [2]token.Token
+	var name string
+	for {
+		_, tok, literal := lexer.Scan()
+		if tok == token.EOF {
+			return count
+		}
+		if tok == token.LPAREN && previous[1] == token.IDENT && previous[0] == token.PERIOD && (name == "Handle" || name == "HandleFunc") {
+			count++
+		}
+		if tok == token.IDENT {
+			name = literal
+		}
+		previous[0], previous[1] = previous[1], tok
+	}
 }
 
 type scope struct {
@@ -202,7 +286,7 @@ type scope struct {
 	enclosing []ast.Node
 }
 
-func scanFunction(pkg *goPackage, source string, function *ast.FuncDecl) ([]Registration, error) {
+func scanFunction(pkg *goPackage, source string, function *ast.FuncDecl, stats *ScanStats) ([]Registration, int, error) {
 	current := &scope{pkg: pkg, function: function, locals: map[string]ast.Expr{}, muxes: map[string]bool{}}
 	if function.Type.Params != nil {
 		for _, field := range function.Type.Params.List {
@@ -239,6 +323,7 @@ func scanFunction(pkg *goPackage, source string, function *ast.FuncDecl) ([]Regi
 	var result []Registration
 	var failure error
 	var stack []ast.Node
+	handled := 0
 	ast.Inspect(function.Body, func(node ast.Node) bool {
 		if node == nil {
 			stack = stack[:len(stack)-1]
@@ -253,21 +338,34 @@ func scanFunction(pkg *goPackage, source string, function *ast.FuncDecl) ([]Regi
 			return true
 		}
 		current.enclosing = stack
-		method, ok := call.Fun.(*ast.SelectorExpr)
-		if !ok || (method.Sel.Name != "Handle" && method.Sel.Name != "HandleFunc") || len(call.Args) != 2 {
+		if !isHandleCall(call) {
+			return true
+		}
+		handled++
+		method := call.Fun.(*ast.SelectorExpr)
+		var printedReceiver bytes.Buffer
+		printer.Fprint(&printedReceiver, token.NewFileSet(), method.X)
+		where := fmt.Sprintf("%s %s %s.%s", source, functionKey(function), printedReceiver.String(), method.Sel.Name)
+		if len(call.Args) != 2 {
+			// ServeMux.Handle/HandleFunc always take two arguments; anything else
+			// (for example the windows.Handle conversion) is not a route.
+			stats.NonRoute = append(stats.NonRoute, fmt.Sprintf("%s: %d arguments, not ServeMux.%s", where, len(call.Args), method.Sel.Name))
 			return true
 		}
 		receiver, _ := method.X.(*ast.Ident)
 		if receiver == nil || !current.muxes[receiver.Name] {
-			// A 2-argument Handle on something else is only acceptable when the
-			// first argument cannot be a route pattern (e.g. slog handlers).
-			if pattern, ok := current.eval(call.Args[0]); ok && looksLikePattern(pattern) {
-				failure = fmt.Errorf("%s: route registered on an unrecognized receiver in %s", source, functionKey(function))
-			} else if _, isSelector := call.Args[0].(*ast.SelectorExpr); isSelector && receiver != nil && receiver.Name == "http" {
-				failure = fmt.Errorf("%s: route registered on http.DefaultServeMux in %s", source, functionKey(function))
+			if reason, ok := nonRouteHandleCalls[pkg.dir+" "+functionKey(function)+" "+printedReceiver.String()]; ok {
+				stats.NonRoute = append(stats.NonRoute, where+": "+reason)
+				return true
 			}
-			return true
+			if receiver != nil && receiver.Name == "http" {
+				failure = fmt.Errorf("%s: route registered on http.DefaultServeMux", where)
+			} else {
+				failure = fmt.Errorf("%s: route registered on an unrecognized receiver (not a local http.NewServeMux() or *http.ServeMux parameter)", where)
+			}
+			return false
 		}
+		stats.Registering++
 		server, classified := registeringFunctions[pkg.dir+" "+functionKey(function)]
 		if !classified {
 			failure = fmt.Errorf("%s: %s registers routes but is not classified in registeringFunctions", source, functionKey(function))
@@ -281,7 +379,7 @@ func scanFunction(pkg *goPackage, source string, function *ast.FuncDecl) ([]Regi
 		result = append(result, items...)
 		return true
 	})
-	return result, failure
+	return result, handled, failure
 }
 
 func (current *scope) registrations(source, server string, call *ast.CallExpr) ([]Registration, error) {
@@ -412,11 +510,13 @@ func (current *scope) registration(source, server, pattern string, handler ast.E
 	item.Wrappers = strings.Join(wrappers, ">")
 	item.Boundary = "transport_auth"
 	for _, wrapper := range wrappers {
-		if boundary, ok := boundaries[wrapper]; ok {
-			item.Boundary = boundary
-			break
+		if _, ok := boundaries[wrapper]; !ok {
+			return item, fmt.Errorf("%s: wrapper %s on %s has no reviewed boundary", source, wrapper, item.Operation)
 		}
 	}
+	if len(wrappers) > 0 {
+		item.Boundary = boundaries[wrappers[0]] // the outermost wrapper authenticates first
+	}
 	var printed bytes.Buffer
 	if err := printer.Fprint(&printed, current.pkg.fset, body); err != nil {
 		return item, err
@@ -453,14 +553,6 @@ func (current *scope) eval(expr ast.Expr) (string, bool) {
 	return "", false
 }
 
-func looksLikePattern(value string) bool {
-	if strings.HasPrefix(value, "/") {
-		return true
-	}
-	method, route, found := strings.Cut(value, " ")
-	return found && strings.HasPrefix(strings.TrimSpace(route), "/") && strings.ToUpper(method) == method
-}
-
 func isServeMuxType(expr ast.Expr) bool {
 	star, ok := expr.(*ast.StarExpr)
 	return ok && isSelector(star.X, "http", "ServeMux")
diff --git a/internal/petcoverage/runtime.go b/internal/petcoverage/runtime.go
new file mode 100644
index 0000000..72ac3ff
--- /dev/null
+++ b/internal/petcoverage/runtime.go
@@ -0,0 +1,63 @@
+package petcoverage
+
+import (
+	"fmt"
+	"net/http"
+	"net/http/httptest"
+	"strings"
+)
+
+// VerifyMux checks the scan against a live ServeMux: for every scanned
+// operation of server, a request built from its pattern must be routed by
+// mux to exactly that registered pattern. A scanned route the mux does not
+// serve, or serves under a different pattern, is an error.
+func VerifyMux(mux *http.ServeMux, routes []Registration, server string) (int, error) {
+	checked := 0
+	var problems []string
+	for _, route := range routes {
+		if route.Server != server {
+			continue
+		}
+		method, path, _ := strings.Cut(route.Operation, " ")
+		want := route.Operation
+		if method == "ANY" {
+			method, want = http.MethodPatch, path
+		}
+		request := httptest.NewRequest(method, concretePath(path), nil)
+		if _, pattern := mux.Handler(request); pattern != want {
+			problems = append(problems, fmt.Sprintf("%s: live mux routes it to %q", route.Operation, pattern))
+		}
+		checked++
+	}
+	if len(problems) > 0 {
+		return checked, fmt.Errorf("scan and live %s mux disagree:\n%s", server, strings.Join(problems, "\n"))
+	}
+	return checked, nil
+}
+
+// concretePath fills every wildcard of a ServeMux path pattern.
+func concretePath(pattern string) string {
+	var builder strings.Builder
+	for index := 0; index < len(pattern); {
+		if pattern[index] != '{' {
+			builder.WriteByte(pattern[index])
+			index++
+			continue
+		}
+		end := strings.IndexByte(pattern[index:], '}')
+		if end < 0 {
+			builder.WriteString(pattern[index:])
+			break
+		}
+		name := pattern[index+1 : index+end]
+		switch {
+		case name == "$":
+		case strings.HasSuffix(name, "..."):
+			builder.WriteString("sample/rest")
+		default:
+			builder.WriteString("sample")
+		}
+		index += end + 1
+	}
+	return builder.String()
+}
diff --git a/internal/petcoverage/web.go b/internal/petcoverage/web.go
index e1fc6de..f88bfc5 100644
--- a/internal/petcoverage/web.go
+++ b/internal/petcoverage/web.go
@@ -10,20 +10,28 @@ import (
 	"strings"
 )
 
-// WebSite is one literal API call in the Web client.
+// WebSite is one literal API call in the Web client. Identical calls in one
+// file are folded into Occurrences, so moving code does not churn the ledger.
 type WebSite struct {
-	Source     string
-	Line       int
-	MethodHint string // concrete method, or GET/see-source when not literal
-	Expression string // literal text between the quotes, templates kept
+	Source      string
+	MethodHint  string // concrete method, or GET/see-source when not literal
+	Expression  string // literal text between the quotes, templates kept
+	Occurrences int
 }
 
 var webMethod = regexp.MustCompile(`method:\s*["'](GET|POST|PUT|PATCH|DELETE)["']`)
 
-// ScanWeb lists every string/template literal under web/src that starts with
-// /api/. It covers API wrappers, raw fetch, downloads and streams alike.
+// dynamicBase accepts a same-origin prefix made only of template expressions,
+// slashes and colons (for example ${protocol}//${location.host}); a literal
+// host such as https://openrouter.ai/api/v1 is an external API, not ours.
+var dynamicBase = regexp.MustCompile(`^(?:\$\{[^{}]*\}|[/:])+$`)
+
+// ScanWeb lists every string/template literal under web/src that names an
+// AHA2 API path: it starts with /api/, or reaches /api/ through a dynamic
+// same-origin base. Comments, regular expressions and other strings are
+// lexed and skipped, and expressions inside templates are scanned too.
 func ScanWeb(root string) ([]WebSite, error) {
-	var result []WebSite
+	counts := map[WebSite]int{}
 	base := filepath.Join(root, "web", "src")
 	err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, err error) error {
 		if err != nil {
@@ -39,15 +47,7 @@ func ScanWeb(root string) ([]WebSite, error) {
 		source := string(data)
 		relative, _ := filepath.Rel(root, path)
 		relative = filepath.ToSlash(relative)
-		for index := 0; index < len(source); index++ {
-			quote := source[index]
-			if (quote != '"' && quote != '\'' && quote != '`') || !strings.HasPrefix(source[index+1:], "/api/") {
-				continue
-			}
-			end, err := jsLiteralEnd(source, index)
-			if err != nil {
-				return fmt.Errorf("%s:%d: %w", relative, strings.Count(source[:index], "\n")+1, err)
-			}
+		_, err = lexTS(source, 0, false, func(start, end int) {
 			tail := source[end:]
 			if semicolon := strings.IndexByte(tail, ';'); semicolon >= 0 {
 				tail = tail[:semicolon+1]
@@ -56,27 +56,175 @@ func ScanWeb(root string) ([]WebSite, error) {
 			if match := webMethod.FindStringSubmatch(tail); match != nil {
 				hint = match[1]
 			}
-			result = append(result, WebSite{Source: relative, Line: strings.Count(source[:index], "\n") + 1, MethodHint: hint, Expression: source[index+1 : end-1]})
-			index = end - 1
+			for _, found := range apiPaths(source[start+1 : end-1]) {
+				if !found.embedded {
+					counts[WebSite{Source: relative, MethodHint: hint, Expression: found.expression}]++
+				} else {
+					// An attribute inside markup (src="/api/...") has no fetch options.
+					counts[WebSite{Source: relative, MethodHint: "GET/see-source", Expression: found.expression}]++
+				}
+			}
+		})
+		if err != nil {
+			return fmt.Errorf("%s: %w", relative, err)
 		}
 		return nil
 	})
 	if err != nil && !os.IsNotExist(err) {
 		return nil, err
 	}
-	sort.SliceStable(result, func(i, j int) bool {
-		if result[i].Source != result[j].Source {
-			return result[i].Source < result[j].Source
+	result := make([]WebSite, 0, len(counts))
+	for site, count := range counts {
+		site.Occurrences = count
+		result = append(result, site)
+	}
+	sort.Slice(result, func(i, j int) bool {
+		a, b := result[i], result[j]
+		if a.Source != b.Source {
+			return a.Source < b.Source
 		}
-		return result[i].Line < result[j].Line
+		if a.Expression != b.Expression {
+			return a.Expression < b.Expression
+		}
+		return a.MethodHint < b.MethodHint
 	})
 	return result, nil
 }
 
-func jsLiteralEnd(source string, start int) (int, error) {
+type apiPath struct {
+	expression string
+	embedded   bool
+}
+
+// apiPaths finds the AHA2 API paths a literal names: the whole literal when
+// it starts with /api/ (or reaches it through a same-origin dynamic base), and
+// every quoted /api/ path embedded in its text, such as src="/api/...".
+// ${...} regions are masked so their own nested literals, which the lexer
+// visits separately, are not counted twice.
+func apiPaths(content string) []apiPath {
+	masked := []byte(content)
+	for index := 0; index+1 < len(content); index++ {
+		if content[index] == '\\' {
+			index++
+			continue
+		}
+		if content[index] == '$' && content[index+1] == '{' {
+			close, err := lexTS(content, index+2, true, nil)
+			if err != nil {
+				return nil
+			}
+			for cursor := index + 2; cursor < close; cursor++ {
+				masked[cursor] = 'x'
+			}
+			index = close
+		}
+	}
+	var result []apiPath
+	text := string(masked)
+	for offset := 0; ; {
+		found := strings.Index(text[offset:], "/api/")
+		if found < 0 {
+			return result
+		}
+		index := offset + found
+		offset = index + len("/api/")
+		switch {
+		case index == 0 || dynamicBase.MatchString(content[:index]):
+			result = append(result, apiPath{expression: content})
+			return result // the literal is one API expression
+		case text[index-1] == '"' || text[index-1] == '\'':
+			stop := strings.IndexByte(text[index:], text[index-1])
+			if stop < 0 {
+				stop = len(text) - index
+			}
+			result = append(result, apiPath{expression: content[index : index+stop], embedded: true})
+		}
+	}
+}
+
+var regexKeywords = map[string]bool{"return": true, "typeof": true, "case": true, "do": true, "else": true, "in": true, "of": true, "new": true, "delete": true, "void": true, "throw": true, "yield": true, "await": true}
+
+func isIdentByte(value byte) bool {
+	return value == '_' || value == '$' || (value >= 'a' && value <= 'z') || (value >= 'A' && value <= 'Z') || (value >= '0' && value <= '9')
+}
+
+func lineOf(source string, index int) int { return strings.Count(source[:index], "\n") + 1 }
+
+// lexTS walks source from `from` and calls visit(start, end) for every string
+// or template literal, including literals nested in ${...}. It skips comments
+// and regular-expression literals. With untilBrace it stops at the `}` that
+// closes an enclosing ${ and returns its index; otherwise it returns len.
+func lexTS(source string, from int, untilBrace bool, visit func(start, end int)) (int, error) {
+	previous, word, depth := byte(0), "", 0
+	for index := from; index < len(source); {
+		char := source[index]
+		next := byte(0)
+		if index+1 < len(source) {
+			next = source[index+1]
+		}
+		switch {
+		case char == '/' && next == '/':
+			stop := strings.IndexByte(source[index:], '\n')
+			if stop < 0 {
+				index = len(source)
+				continue
+			}
+			index += stop + 1
+		case char == '/' && next == '*':
+			stop := strings.Index(source[index+2:], "*/")
+			if stop < 0 {
+				return 0, fmt.Errorf("line %d: unterminated comment", lineOf(source, index))
+			}
+			index += stop + 4
+		case char == '"' || char == '\'' || char == '`':
+			end, err := lexLiteral(source, index, visit)
+			if err != nil {
+				return 0, err
+			}
+			if visit != nil {
+				visit(index, end)
+			}
+			index, previous, word = end, 'a', ""
+		case char == '/' && (previous == 0 || strings.IndexByte("(,=:[!&|?{};+-*%<>~^", previous) >= 0 || regexKeywords[word]):
+			end, err := regexEnd(source, index)
+			if err != nil {
+				return 0, err
+			}
+			index, previous, word = end, 'a', ""
+		case isIdentByte(char):
+			begin := index
+			for index < len(source) && isIdentByte(source[index]) {
+				index++
+			}
+			word, previous = source[begin:index], 'a'
+		default:
+			switch char {
+			case '{':
+				depth++
+			case '}':
+				if untilBrace && depth == 0 {
+					return index, nil
+				}
+				depth--
+			}
+			if char != ' ' && char != '\t' && char != '\n' && char != '\r' {
+				previous, word = char, ""
+			}
+			index++
+		}
+	}
+	if untilBrace {
+		return 0, fmt.Errorf("line %d: unterminated template expression", lineOf(source, from))
+	}
+	return len(source), nil
+}
+
+// lexLiteral returns the end (one past the closing quote) of the literal at
+// start. Template expressions are lexed, so their nested literals, comments
+// and regular expressions cannot end the template early.
+func lexLiteral(source string, start int, visit func(start, end int)) (int, error) {
 	quote := source[start]
-	index := start + 1
-	for index < len(source) {
+	for index := start + 1; index < len(source); {
 		char := source[index]
 		switch {
 		case char == '\\':
@@ -84,34 +232,44 @@ func jsLiteralEnd(source string, start int) (int, error) {
 			continue
 		case char == quote:
 			return index + 1, nil
+		case char == '\n' && quote != '`':
+			return 0, fmt.Errorf("line %d: unterminated Web literal", lineOf(source, start))
 		case quote == '`' && strings.HasPrefix(source[index:], "${"):
-			index += 2
-			depth := 1
-			for index < len(source) && depth > 0 {
-				char = source[index]
-				if char == '"' || char == '\'' || char == '`' {
-					end, err := jsLiteralEnd(source, index)
-					if err != nil {
-						return 0, err
-					}
-					index = end
-					continue
-				}
-				if char == '{' {
-					depth++
-				} else if char == '}' {
-					depth--
-				}
-				index++
-			}
-			if depth > 0 {
-				return 0, fmt.Errorf("unterminated Web template expression")
+			close, err := lexTS(source, index+2, true, visit)
+			if err != nil {
+				return 0, err
 			}
+			index = close + 1
 			continue
 		}
 		index++
 	}
-	return 0, fmt.Errorf("unterminated Web literal")
+	return 0, fmt.Errorf("line %d: unterminated Web literal", lineOf(source, start))
+}
+
+func regexEnd(source string, start int) (int, error) {
+	inClass := false
+	for index := start + 1; index < len(source); index++ {
+		switch source[index] {
+		case '\\':
+			index++
+		case '[':
+			inClass = true
+		case ']':
+			inClass = false
+		case '\n':
+			return 0, fmt.Errorf("line %d: unterminated regular expression", lineOf(source, start))
+		case '/':
+			if !inClass {
+				index++
+				for index < len(source) && source[index] >= 'a' && source[index] <= 'z' {
+					index++
+				}
+				return index, nil
+			}
+		}
+	}
+	return 0, fmt.Errorf("line %d: unterminated regular expression", lineOf(source, start))
 }
 
 // sitePattern turns a call-site literal into a regexp over concrete paths:
@@ -122,26 +280,11 @@ func sitePattern(expression string) (*regexp.Regexp, error) {
 	builder.WriteString("^")
 	for index := 0; index < len(expression); {
 		if strings.HasPrefix(expression[index:], "${") {
-			depth, cursor := 1, index+2
-			for cursor < len(expression) && depth > 0 {
-				switch expression[cursor] {
-				case '{':
-					depth++
-				case '}':
-					depth--
-				case '"', '\'', '`':
-					stop, err := jsLiteralEnd(expression, cursor)
-					if err != nil {
-						return nil, err
-					}
-					cursor = stop
-					continue
-				}
-				cursor++
-			}
-			if depth > 0 {
-				return nil, fmt.Errorf("unterminated template expression")
+			close, err := lexTS(expression, index+2, true, nil)
+			if err != nil {
+				return nil, err
 			}
+			cursor := close + 1
 			builder.WriteString(`[^?#]*`)
 			index = cursor
 			continue
@@ -181,9 +324,13 @@ func samplePath(route string) string {
 func UnmatchedWebSites(sites []WebSite, routes []Registration) ([]WebSite, error) {
 	var unmatched []WebSite
 	for _, site := range sites {
-		pattern, err := sitePattern(site.Expression)
+		expression := site.Expression
+		if index := strings.Index(expression, "/api/"); index > 0 {
+			expression = expression[index:] // same-origin dynamic base
+		}
+		pattern, err := sitePattern(expression)
 		if err != nil {
-			return nil, fmt.Errorf("%s:%d: %w", site.Source, site.Line, err)
+			return nil, fmt.Errorf("%s %s: %w", site.Source, site.Expression, err)
 		}
 		found := false
 		for _, route := range routes {
```
