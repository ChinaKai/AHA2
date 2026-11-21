# P1c r2 修订：多参数包装与漏网注册写法（固定 0da575e）

源码 `0da575e5d7703b40a207d0cce0b1d629efd78e50`，tree `b4d6c2105e0e22c9dffdc5ba92b5f10693a73a0b`。按 723 要求，本材料只覆盖 `6acfdb6..0da575e`。

范围声明：`6acfdb6..0da575e` 共 4 个提交——
- `9f54aa6 Archive immutable P1c v3 incremental review packets`
- `302d491 Prove pet access to shared browser, desktop, processes, mail and core tools`
- `37132df Archive immutable P2 shared-control evidence review packets`
- `0da575e Resolve multi-argument wrappers and refuse unseen Handle shapes in the coverage scan`

其中 9f54aa6、37132df 只有评审材料（docs/pet-mcp/review/*.md），302d491 是 P2 测试与账本（已获 APPROVE，材料 `p2-302d491-*`）。这三个提交都没有改 P1c 代码路径（`internal/petcoverage`、`cmd/aha-pet-coverage`、`internal/httpapi/pet_coverage_ledger_test.go`、`server.go`、`internal/centersync`、README），已用 `git diff 6acfdb6 37132df` 核对为空。所以下面代码包里 P1c 路径的 `6acfdb6..0da575e` 完整 diff，就是本次修订提交 `0da575e` 的全部代码改动。本次修订提交改的 7 个文件：`docs/pet-mcp/README.md`, `docs/pet-mcp/coverage.tsv`, `internal/httpapi/pet_coverage_ledger_test.go`, `internal/petcoverage/ledger.go`, `internal/petcoverage/petcoverage_test.go`, `internal/petcoverage/routes.go`, `internal/petcoverage/runtime.go`。

固定副本（git archive，blob 逐个核对）验证：

- `go test -json ./internal/petcoverage -count=1` → 退出 0，0.2s，具名用例 14 个全部 run/pass
- `go test -json ./cmd/aha-pet-coverage -count=1` → 退出 0，1.2s，具名用例 1 个全部 run/pass
- `go test -json ./internal/centersync -count=1` → 退出 0，3.5s，具名用例 12 个全部 run/pass
- `go test -json ./internal/httpapi -run '^(TestPetCoverageLedgersMatchRegistrationsAndEvidence|TestPetCoverageScanMatchesLiveMuxes|TestChannelRuntimeRoutesResolvePastTheirTwoArgumentWrapper|TestMCPToolCatalogCoversEveryBuiltInServer|TestHealthProbeCarriesNoBusinessData|TestEveryAgentTokenRouteHasExplicitPolicy|TestOpaqueMCPResourceNamesCannotSkipLiveAuthorization|TestUnclassifiedTokenRoutesFailBeforeEitherAuthWrapperDispatches|TestSecurityHeadersAllowXTermStylesWithoutInlineScripts)$' -count=1` → 退出 0，22.1s，具名用例 9 个全部 run/pass
- `go test -json ./internal/app -run '^(TestPetHardwareBindingReplacementPreservesIdentityAndRejectsWithoutMutation)$' -count=1 -timeout=30m` → 退出 0，10.4s，具名用例 1 个全部 run/pass
- `go test -json ./internal/centersync -run '^(TestDeviceRenameKeepsCenterGeneratedID|TestHealthAndAuthentication|TestOneTimeDeviceRegistrationStoresOnlyHashes|TestPushIsIdempotentAndPullIsIncremental)$' -count=1 -timeout=30m` → 退出 0，1.1s，具名用例 4 个全部 run/pass
- `go test -json ./internal/httpapi -run '^(TestADBDeadlineReportsPossibleExecutionOnHTTPAndMCP|TestAgentBrowserMCPRequiresCapabilityAndDomainGate|TestAgentChannelMCPRequiresCapabilityAndExposesOnlyTwoTools|TestAgentCollabMCPRequiresCapabilityAndDomainGate|TestAgentCoreMCPAttachmentBinding|TestAgentCoreMCPRequiresCapabilityAndRunningTurn|TestAgentHardwareFourLifecyclesUseFakeTransportForPetAndOrdinaryTask|TestAgentHardwareMCPListOnlyExposesBoundNonSensitiveFields|TestAgentHardwareMCPRequiresCapabilityAndDomainGate|TestAgentHardwareMCPTerminalReadPagingAndCaps|TestAgentHardwareSharedAuthorizationRejectsWritesBeforeEffects|TestAgentKnowledgeMCPRequiresCapabilityAndDomainGate|TestAgentLiveInboxClaimAckHTTP|TestAgentMailMCPTools|TestAgentWorkspaceMCPRequiresCapabilityAndDomainGate|TestBrowserMCPEveryOperationForOrdinaryAndPetTasks|TestBrowserMCPOperationsUseExistingGrantAndKeepSecretValuesOut|TestDesktopMCPOperationsForPetUseOwnerShareAndLivePermission|TestDesktopMCPUsesExistingShareAndSingleUseObservation|TestHealthProbeCarriesNoBusinessData|TestLoginAndSendFailuresHaveFixedDistinctStateCodes|TestPetCoreKeepsOwnTurnAccessWithoutReadingOtherInternalTasks|TestPetCoreMCPCallsUseRealTurnAndKeepManagementBoundary|TestPetHardwareMCPExecUsesBoundResourceAndLivePermissions|TestPetMCPNotificationsAreAcceptedWithoutJSON|TestPetMCPPingReturnsResult|TestPetUploadsAndBindsOwnTurnAttachment|TestRemovedSkillWriteRoutesReturnMethodNotAllowed|TestRetainedCredentialFragmentsStayMaskedAcrossPagedHistorySurfaces|TestServeWebServesBrowserNewTabAsHTTPPage|TestServeWebUnknownRouteStillFallsBackToSPA|TestStoredLoginEchoCannotReachHistoryHTTPMCPOrWebSocket|TestWorkspaceMCPStartsAndStopsOnlyCurrentTaskProcess|TestWorkspaceProcessMCPForPetIsScopedAndPermissionGated)$' -count=1 -timeout=30m` → 退出 0，352.3s，具名用例 34 个全部 run/pass
- `go vet ./internal/petcoverage ./cmd/aha-pet-coverage ./internal/httpapi ./internal/centersync` → 退出 0，1.1s
- `go run ./cmd/aha-pet-coverage` → 退出 0，0.9s；输出：Handle/HandleFunc calls: 423 (route registrations 421, non-route 2) / HTTP operations: 433 (closed 53, open 380); Web API call sites: 282; ordinary-Task MCP tools: 61 (closed 57)
- `go run ./cmd/aha-pet-coverage -complete` → 退出 1（预期 1：门禁必须失败），0.4s；输出：coverage is incomplete: 380 open operations, 4 open tool rows / exit status 1
- `GOOS=windows go vet ./internal/petcoverage ./cmd/aha-pet-coverage ./internal/httpapi` → 退出 0，5.2s

## A. 代码、测试与文档（P1c 路径完整 diff）

对应 r2 必修项：
- **M1 多参数包装**：`boundaries` 里登记的名字不论参数个数都按包装处理，最后一个参数下钻为 handler，其余参数必须可求值成常量并记入 Wrappers；参数里带 handler 的未登记调用报错；不变式"记录的 handler 名不能是已登记包装名"。工厂（只有常量实参）的调用文本计入指纹。真实仓库 12 条 `/api/channel-runtime/` 路由现在各自解析到真实 handler，边界 `channel_runtime`，指纹两两不同（`TestChannelRuntimeRoutesResolvePastTheirTwoArgumentWrapper`）。
- **M3 对多参数包装成立**：指纹 v2 覆盖包装链与常量参数、每个包装函数自身源码、工厂实参、handler 源码。`TestReviewedRowUnderMultiArgumentWrapperGoesStaleOnAnyChange` 分别改 handler、改包装参数、改包装函数体，三种情况都是：漂移检查失败 → `-update` 标 stale → `-complete` 失败。旧 v1 指纹只有在等于"当前代码按 v1 算出的值"时才保留结论（`TestV1FingerprintMigrationKeepsOnlyUnchangedVerdicts`）。
- **M2 漏网写法**：`.Handle`/`.HandleFunc` 不在直接调用位置（`add := mux.Handle`、`register := mux.HandleFunc`、`var r = http.Handle`）报错；方法表达式 `(*http.ServeMux).Handle(mux, ...)` 明确报错；参数个数不是 2 但带路由串的调用报错（`TestHandleAliasesAndMethodExpressionsFailTheScan`）。
- **反向运行期对照**：`VerifyMux` 改为双向，`LivePatterns` 读出真实 mux 登记的全部模式（net/http 的内部冲突索引；结构变化时报错，不会空跑通过），mux 有而扫描没有即失败（`TestVerifyMuxFindsPatternsTheScanMissed`，以及三个真实监听的 `TestPetCoverageScanMatchesLiveMuxes`/`TestCoverageScanMatchesLiveCenterMux`）。

反向验证（提交前工作区，逐项临时去掉修复后跑对应测试，恢复后通过）：去掉多参数包装下钻 → `TestMultiArgumentWrappersResolveToTheRealHandler`、`TestReviewedRowUnderMultiArgumentWrapperGoesStaleOnAnyChange` 失败；去掉 `.Handle` 作值检查 → `TestHandleAliasesAndMethodExpressionsFailTheScan` 失败；去掉方法表达式检查 → 同上失败；去掉反向对照 → `TestVerifyMuxFindsPatternsTheScanMissed` 失败；指纹不含包装函数体 → `TestReviewedRowUnderMultiArgumentWrapperGoesStaleOnAnyChange`（wrapper body 子用例）失败；指纹不含工厂实参 → `TestMultiArgumentWrappersResolveToTheRealHandler` 失败。

完整 diff 文件边界：`internal/petcoverage/routes.go`, `internal/petcoverage/runtime.go`, `internal/petcoverage/ledger.go`, `internal/petcoverage/petcoverage_test.go`, `internal/httpapi/pet_coverage_ledger_test.go`, `docs/pet-mcp/README.md`

```diff
diff --git a/docs/pet-mcp/README.md b/docs/pet-mcp/README.md
index 80982ef..5f70e06 100644
--- a/docs/pet-mcp/README.md
+++ b/docs/pet-mcp/README.md
@@ -8,9 +8,11 @@
 
 三份账本都由 `internal/petcoverage` 生成和校验，它用 Go 自己的语法解析器读源码，不靠文本正则：
 
-- `coverage.tsv`：`internal/` 与 `cmd/` 下所有 `ServeMux` 注册（任意变量名、常量、拼接、局部前缀、字面量路由表都会展开），按监听分 `httpapi`、`mail-download`、`centersync`。每行带源文件、handler、认证边界（由外层包装推出）和 handler 指纹（包装链加 handler 源码的哈希）。
-  - 对账：源码里每个 `.Handle(` / `.HandleFunc(` 词法记号都要对上一处解析出的调用，并且要么是路由注册，要么列为非路由调用并写明原因（目前只有 `instancelock` 里两处单参数的 `windows.Handle` 类型转换）。函数体外的调用、注册到 DefaultServeMux 或非 `http.NewServeMux()`/`*http.ServeMux` 参数的接收者、未登记的注册函数、无法求值的路由串、找不到唯一声明的 handler、没有登记边界的包装函数，扫描都会直接失败。
-  - 运行期对照：`TestPetCoverageScanMatchesLiveMuxes`（httpapi 与 mail-download）和 `TestCoverageScanMatchesLiveCenterMux`（aha-sync）用真实 mux 的 `Handler(r)` 逐条确认，扫描出的每个操作都被路由到完全相同的模式。主监听的注册放在 `routeMux()` 里，就是为了这一步能拿到真实 mux。
+- `coverage.tsv`：`internal/` 与 `cmd/` 下所有 `ServeMux` 注册（任意变量名、常量、拼接、局部前缀、字面量路由表都会展开），按监听分 `httpapi`、`mail-download`、`centersync`。每行带源文件、真实 handler、认证边界（由最外层包装推出）和 `v2:` 指纹。
+  - 包装与 handler：`boundaries` 里登记的包装不论几个参数都按包装处理（最后一个参数是被包装的 handler，其余参数必须是可求值的常量），一路下钻到真实 handler；参数里带 handler 的未登记调用当作未审包装报错；记录下来的 handler 名不能是包装名。只有常量参数的工厂（如 `s.savePetWorkbenchDocument("core")`）记为 handler，实参计入指纹。
+  - 指纹：包装链及其常量参数、每个包装函数自己的源码、工厂调用的实参、handler 源码，一起做 SHA-256。handler、包装参数或包装实现任一变化，已审行在 `-update` 时退回 stale。从旧格式迁移时，只有旧指纹恰好等于"用旧算法重算当前代码"的行才保留结论。
+  - 对账：源码里每个 `.Handle(` / `.HandleFunc(` 词法记号都要对上一处解析出的调用，并且要么是路由注册，要么列为非路由调用并写明原因（目前只有 `instancelock` 里两处单参数的 `windows.Handle` 类型转换）。`.Handle`/`.HandleFunc` 出现在非调用位置（`add := mux.Handle` 这类方法值别名，或把 `http.Handle` 当值）、方法表达式 `(*http.ServeMux).Handle(...)`、参数个数不是 2 但带路由串的调用、函数体外的调用、注册到 DefaultServeMux 或非 `http.NewServeMux()`/`*http.ServeMux` 参数的接收者、未登记的注册函数、无法求值的路由串、找不到唯一声明的 handler，扫描都会直接失败。
+  - 运行期对照（双向）：`TestPetCoverageScanMatchesLiveMuxes`（httpapi 与 mail-download）和 `TestCoverageScanMatchesLiveCenterMux`（aha-sync）用真实 mux 的 `Handler(r)` 确认扫描出的每个操作都被路由到完全相同的模式；同时读出 mux 实际登记的全部模式（Go 1.22+ 的内部冲突索引，结构变化时直接报错），mux 里有而扫描没有的同样失败。主监听的注册放在 `routeMux()` 里，就是为了这一步能拿到真实 mux。
 - `web-call-sites.tsv`：`web/src` 里所有点名 AHA2 API 的字面量：以 `/api/` 开头的字符串/模板、经同源动态基址（只由 `${...}`、`/`、`:` 组成，例如 `${protocol}//${location.host}`）到达 `/api/` 的模板，以及字面量文本里带引号的 `/api/` 路径（例如 `src="/api/..."`）。扫描是真正的 TS 词法：注释、正则字面量（含模板表达式里的正则）跳过，`${...}` 里的嵌套字面量单独计入且不重复。清单不带行号，同一文件里相同的调用合并为 occurrences，挪动代码不会改账本。每个调用点必须能对上一条已注册的 httpapi 路由（方法明确时方法也要一致），对不上就说明有注册逃过了扫描。
 - `task-mcp-tools.tsv`：普通 Task 可用的全部内置 MCP 工具（`httpapi.MCPToolCatalog()`，宠物自己的 server 除外），逐个记录宠物 Turn 如何拿到同样能力。
 
diff --git a/internal/httpapi/pet_coverage_ledger_test.go b/internal/httpapi/pet_coverage_ledger_test.go
index b511e98..3c95362 100644
--- a/internal/httpapi/pet_coverage_ledger_test.go
+++ b/internal/httpapi/pet_coverage_ledger_test.go
@@ -5,6 +5,7 @@ import (
 	"net/http"
 	"net/http/httptest"
 	"path/filepath"
+	"strings"
 	"testing"
 
 	"github.com/ChinaKai/AHA2/internal/knowledgevault"
@@ -84,3 +85,28 @@ func TestPetCoverageScanMatchesLiveMuxes(t *testing.T) {
 		t.Fatalf("verified %d main and %d mail-download operations", main, mail)
 	}
 }
+
+// r2 M1 on the real repository: the channel-runtime routes sit behind the
+// two-argument withChannelCapability(scope, next); each must resolve to its
+// own handler and fingerprint under the channel_runtime boundary.
+func TestChannelRuntimeRoutesResolvePastTheirTwoArgumentWrapper(t *testing.T) {
+	routes, _, err := petcoverage.ScanRoutes(filepath.Join("..", ".."))
+	if err != nil {
+		t.Fatal(err)
+	}
+	handlers, fingerprints := map[string]bool{}, map[string]bool{}
+	count := 0
+	for _, route := range routes {
+		if route.Server != "httpapi" || !strings.Contains(route.Operation, " /api/channel-runtime/") {
+			continue
+		}
+		count++
+		if route.Handler == "withChannelCapability" || route.Boundary != "channel_runtime" || !strings.HasPrefix(route.Wrappers, "withChannelCapability(") {
+			t.Fatalf("%s resolved to %+v", route.Operation, route)
+		}
+		handlers[route.Handler], fingerprints[route.Fingerprint] = true, true
+	}
+	if count != 12 || len(handlers) != 12 || len(fingerprints) != 12 {
+		t.Fatalf("channel-runtime routes=%d distinct handlers=%d fingerprints=%d", count, len(handlers), len(fingerprints))
+	}
+}
diff --git a/internal/petcoverage/ledger.go b/internal/petcoverage/ledger.go
index 1ee946f..939f65b 100644
--- a/internal/petcoverage/ledger.go
+++ b/internal/petcoverage/ledger.go
@@ -127,7 +127,10 @@ func MergeLedger(previous []Row, routes []Registration) (rows []Row, stale, remo
 			if !strings.Contains(oldSource, "/") && oldSource != "" {
 				oldSource = "internal/httpapi/" + oldSource // v1 paths were package-relative
 			}
-			if oldSource != route.Source || old["handler"] != route.Handler || old["boundary"] != route.Boundary || old["fingerprint"] != route.Fingerprint {
+			// A v1 fingerprint equal to the v1 hash of the current code proves
+			// nothing changed since review; only the hash scheme moved on.
+			migrated := !strings.HasPrefix(old["fingerprint"], fingerprintVersion) && old["fingerprint"] == route.LegacyFingerprint
+			if oldSource != route.Source || old["handler"] != route.Handler || old["boundary"] != route.Boundary || (old["fingerprint"] != route.Fingerprint && !migrated) {
 				row["status"] = "stale"
 				stale = append(stale, key)
 			}
diff --git a/internal/petcoverage/petcoverage_test.go b/internal/petcoverage/petcoverage_test.go
index 9592701..311dc68 100644
--- a/internal/petcoverage/petcoverage_test.go
+++ b/internal/petcoverage/petcoverage_test.go
@@ -400,3 +400,190 @@ func TestVerifyMuxRejectsScannedRoutesTheLiveMuxDoesNotServe(t *testing.T) {
 		t.Fatalf("live mismatch not reported: %v", err)
 	}
 }
+
+func withBoundary(t *testing.T, name, boundary string) {
+	t.Helper()
+	saved := boundaries
+	boundaries = map[string]string{}
+	for key, value := range saved {
+		boundaries[key] = value
+	}
+	boundaries[name] = boundary
+	t.Cleanup(func() { boundaries = saved })
+}
+
+const scopedPreamble = `
+func (s *Server) withScope(scope string, next http.Handler) http.Handler { return next }
+func (s *Server) factory(section string) http.HandlerFunc { return func(http.ResponseWriter, *http.Request) {} }
+`
+
+// r2 M1: a registered wrapper is a wrapper whatever its arity; the real
+// handler, the wrapper's constant arguments and a factory's arguments all
+// reach the fingerprint, and a wrapper is never recorded as the handler.
+func TestMultiArgumentWrappersResolveToTheRealHandler(t *testing.T) {
+	classify(t, map[string]string{"internal/api (*Server).Routes": "httpapi"})
+	withBoundary(t, "withScope", "scoped")
+	root := writeTree(t, map[string]string{"internal/api/server.go": serverPreamble + scopedPreamble + `
+func (s *Server) Routes() {
+	mux := http.NewServeMux()
+	mux.Handle("POST /a", s.withScope("scope.a", http.HandlerFunc(s.list)))
+	mux.Handle("POST /b", s.withScope("scope.b", http.HandlerFunc(s.status)))
+	mux.Handle("POST /c", s.withScope("scope.a", http.HandlerFunc(s.status)))
+	mux.Handle("PUT /core", s.withAuth(s.factory("core")))
+	mux.Handle("PUT /worklog", s.withAuth(s.factory("worklog")))
+}
+`})
+	routes, _, err := ScanRoutes(root)
+	if err != nil {
+		t.Fatal(err)
+	}
+	got := map[string]Registration{}
+	fingerprints := map[string]string{}
+	for _, route := range routes {
+		got[route.Operation] = route
+		if previous, duplicate := fingerprints[route.Fingerprint]; duplicate {
+			t.Fatalf("%s and %s share fingerprint %s", previous, route.Operation, route.Fingerprint)
+		}
+		fingerprints[route.Fingerprint] = route.Operation
+	}
+	for operation, handler := range map[string]string{"POST /a": "list", "POST /b": "status", "POST /c": "status", "PUT /core": "factory", "PUT /worklog": "factory"} {
+		if got[operation].Handler != handler {
+			t.Fatalf("%s handler = %q, want %q", operation, got[operation].Handler, handler)
+		}
+	}
+	if got["POST /a"].Boundary != "scoped" || got["POST /a"].Wrappers != `withScope("scope.a")` || got["PUT /core"].Boundary != "owner_session" {
+		t.Fatalf("wrapper metadata: %+v %+v", got["POST /a"], got["PUT /core"])
+	}
+	for name, body := range map[string]string{
+		"unregistered multi-arg wrapper": `func (s *Server) withOther(scope string, next http.Handler) http.Handler { return next }; func (s *Server) Routes() { mux := http.NewServeMux(); mux.Handle("POST /x", s.withOther("x", http.HandlerFunc(s.list))) }`,
+		"non-constant wrapper argument":  `func (s *Server) Routes(scope string) { mux := http.NewServeMux(); mux.Handle("POST /x", s.withScope(scope, http.HandlerFunc(s.list))) }`,
+		"wrapper used as handler":        `func (s *Server) Routes() { mux := http.NewServeMux(); mux.Handle("POST /x", http.HandlerFunc(s.withScope)) }`,
+	} {
+		t.Run(name, func(t *testing.T) {
+			root := writeTree(t, map[string]string{"internal/api/server.go": serverPreamble + scopedPreamble + body + "\n"})
+			if _, _, err := ScanRoutes(root); err == nil {
+				t.Fatal("scan accepted it")
+			}
+		})
+	}
+}
+
+// r2 M2: aliases and method expressions cannot register a route unseen.
+func TestHandleAliasesAndMethodExpressionsFailTheScan(t *testing.T) {
+	for name, item := range map[string][2]string{
+		"method value alias":     {`func (s *Server) Routes() { mux := http.NewServeMux(); add := mux.Handle; add("POST /api/v1/alias", http.HandlerFunc(s.list)) }`, "used as a value"},
+		"HandleFunc alias":       {`func (s *Server) Routes() { mux := http.NewServeMux(); register := mux.HandleFunc; register("POST /api/v1/alias", s.list) }`, "used as a value"},
+		"method expression":      {`func (s *Server) Routes() { mux := http.NewServeMux(); (*http.ServeMux).Handle(mux, "POST /api/v1/expr", http.HandlerFunc(s.list)) }`, "method-expression"},
+		"default mux value":      {`var register = http.Handle`, "used as a value"},
+		"route in a 3-arg call":  {`func (s *Server) Routes(r interface{ Handle(string, http.Handler, int) }) { r.Handle("POST /api/v1/three", http.HandlerFunc(s.list), 1) }`, "carries route pattern"},
+		"qualified type is fine": {`type holder struct{ h http.Handler }`, ""},
+	} {
+		t.Run(name, func(t *testing.T) {
+			classify(t, map[string]string{"internal/api (*Server).Routes": "httpapi"})
+			root := writeTree(t, map[string]string{"internal/api/server.go": serverPreamble + item[0] + "\n"})
+			_, _, err := ScanRoutes(root)
+			if item[1] == "" {
+				if err != nil {
+					t.Fatalf("unexpected error: %v", err)
+				}
+				return
+			}
+			if err == nil || !strings.Contains(err.Error(), item[1]) {
+				t.Fatalf("scan error = %v, want %q", err, item[1])
+			}
+		})
+	}
+}
+
+func TestVerifyMuxFindsPatternsTheScanMissed(t *testing.T) {
+	mux := http.NewServeMux()
+	ok := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
+	mux.Handle("GET /api/v1/known", ok)
+	mux.Handle("POST /api/v1/alias", ok)
+	mux.Handle("/static/", ok)
+	routes := []Registration{{Operation: "GET /api/v1/known", Server: "httpapi"}, {Operation: "ANY /static/", Server: "httpapi"}}
+	_, err := VerifyMux(mux, routes, "httpapi")
+	if err == nil || !strings.Contains(err.Error(), `"POST /api/v1/alias", which the scan did not find`) {
+		t.Fatalf("reverse check missed the extra pattern: %v", err)
+	}
+	live, err := LivePatterns(mux)
+	if err != nil || strings.Join(live, ",") != "/static/,GET /api/v1/known,POST /api/v1/alias" {
+		t.Fatalf("live patterns = %v err=%v", live, err)
+	}
+}
+
+// r2 M3 for multi-argument wrappers: a reviewed row goes stale and the
+// complete gate fails when the handler, the wrapper's argument or the
+// wrapper's own code changes.
+func TestReviewedRowUnderMultiArgumentWrapperGoesStaleOnAnyChange(t *testing.T) {
+	withBoundary(t, "withScope", "scoped")
+	for name, change := range map[string][2]string{
+		"handler body":     {"func (s *Server) status(w http.ResponseWriter, r *http.Request) { s.list(w, r) }", "func (s *Server) status(w http.ResponseWriter, r *http.Request) { s.list(w, r); s.list(w, r) }"},
+		"wrapper argument": {`s.withScope("scope.b", http.HandlerFunc(s.status))`, `s.withScope("scope.z", http.HandlerFunc(s.status))`},
+		"wrapper body":     {"func (s *Server) withScope(scope string, next http.Handler) http.Handler { return next }", "func (s *Server) withScope(scope string, next http.Handler) http.Handler { _ = scope; return next }"},
+	} {
+		t.Run(name, func(t *testing.T) {
+			classify(t, map[string]string{"internal/api (*Server).Routes": "httpapi"})
+			root := writeTree(t, map[string]string{
+				"internal/api/server.go":   serverPreamble + scopedPreamble + "func (s *Server) Routes() { mux := http.NewServeMux(); mux.Handle(\"POST /api/v1/b\", s.withScope(\"scope.b\", http.HandlerFunc(s.status))) }\n",
+				"internal/api/api_test.go": "package api\n\nimport \"testing\"\n\nfunc TestReal(t *testing.T) {}\n",
+				"web/src/api.ts":           "fetch(\"/api/v1/b\", {method: \"POST\"});\n",
+				"docs/pet-mcp/.keep":       "",
+			})
+			catalog := NewCatalog(map[string][]string{"aha2_pet": {"aha_overview"}})
+			if _, err := Check(root, catalog, true, false); err != nil {
+				t.Fatal(err)
+			}
+			ledger := filepath.Join(root, LedgerPath)
+			data, _ := os.ReadFile(ledger)
+			closed := strings.Replace(string(data), "pending\tpending\tpending\tpending\tpending", "aha2_pet.aha_overview\tscoped\tnone\tinternal/api/api_test.go:TestReal\tdirect", 1)
+			if closed == string(data) {
+				t.Fatal("fixture row not found")
+			}
+			if err := os.WriteFile(ledger, []byte(closed), 0o644); err != nil {
+				t.Fatal(err)
+			}
+			if report, err := Check(root, catalog, false, true); err != nil || report.Final != 1 {
+				t.Fatalf("closed fixture row did not pass: %+v %v", report, err)
+			}
+			source := filepath.Join(root, "internal/api/server.go")
+			code, _ := os.ReadFile(source)
+			edited := strings.Replace(string(code), change[0], change[1], 1)
+			if edited == string(code) {
+				t.Fatal("change not applied")
+			}
+			if err := os.WriteFile(source, []byte(edited), 0o644); err != nil {
+				t.Fatal(err)
+			}
+			if _, err := Check(root, catalog, false, false); err == nil || !strings.Contains(err.Error(), "differs from the scan") {
+				t.Fatalf("change was not detected: %v", err)
+			}
+			report, err := Check(root, catalog, true, false)
+			if err != nil || len(report.Stale) != 1 || report.Final != 0 {
+				t.Fatalf("update did not mark the row stale: %+v %v", report, err)
+			}
+			if _, err := Check(root, catalog, false, true); err == nil || !strings.Contains(err.Error(), "incomplete") {
+				t.Fatalf("complete gate passed after the change: %v", err)
+			}
+		})
+	}
+}
+
+func TestV1FingerprintMigrationKeepsOnlyUnchangedVerdicts(t *testing.T) {
+	route := Registration{Operation: "GET /a", Server: "httpapi", Source: "internal/api/a.go", Handler: "a", Boundary: "owner_session", Fingerprint: "v2:aaaaaaaaaaaa", LegacyFingerprint: "111111111111"}
+	row := func(fingerprint string) []Row {
+		return []Row{{"operation": "GET /a", "server": "httpapi", "source": "internal/api/a.go", "handler": "a", "boundary": "owner_session", "fingerprint": fingerprint, "mcp": "m", "permission": "p", "protected_flow": "f", "tests": "t", "status": "direct"}}
+	}
+	for fingerprint, want := range map[string]string{
+		"111111111111":    "direct", // v1 hash of unchanged code: verdict kept
+		"222222222222":    "stale",  // v1 hash of different code
+		"v2:aaaaaaaaaaaa": "direct",
+		"v2:bbbbbbbbbbbb": "stale",
+		"v2:111111111111": "stale", // a v2 value never takes the migration path
+	} {
+		rows, _, _ := MergeLedger(row(fingerprint), []Registration{route})
+		if rows[0]["status"] != want || rows[0]["fingerprint"] != route.Fingerprint {
+			t.Fatalf("stored %s: status=%s fingerprint=%s, want %s", fingerprint, rows[0]["status"], rows[0]["fingerprint"], want)
+		}
+	}
+}
diff --git a/internal/petcoverage/routes.go b/internal/petcoverage/routes.go
index af94891..b9d6a00 100644
--- a/internal/petcoverage/routes.go
+++ b/internal/petcoverage/routes.go
@@ -31,8 +31,12 @@ type Registration struct {
 	Handler     string // handler method, factory, or "inline"
 	Wrappers    string // authentication/authorization wrappers, outermost first
 	Boundary    string // authentication boundary derived from Wrappers
-	Fingerprint string // hash of wrappers and the handler's source
-	RejectOnly  bool   // the handler only writes a client error
+	Fingerprint string // "v2:" + hash of wrappers (with arguments and source) and handler source
+	// LegacyFingerprint is the pre-v2 hash (wrapper names and handler source)
+	// of the current code. MergeLedger uses it once to keep a verdict whose
+	// stored v1 fingerprint proves the code has not changed since review.
+	LegacyFingerprint string
+	RejectOnly        bool // the handler only writes a client error
 }
 
 // registeringFunctions classifies every function that registers routes. A
@@ -62,6 +66,9 @@ var boundaries = map[string]string{
 	"auth":                  "device_token",
 }
 
+// fingerprintVersion prefixes every fingerprint of the current scheme.
+const fingerprintVersion = "v2:"
+
 // scanned Go roots; everything else (web, tooling output, vendored copies)
 // cannot register a route served by AHA2 binaries.
 var goRoots = []string{"internal", "cmd"}
@@ -202,6 +209,9 @@ func ScanRoutes(root string) ([]Registration, ScanStats, error) {
 				return nil, stats, fmt.Errorf("%s: %d .Handle/.HandleFunc tokens but %d parsed calls; review the scanner", source, tokens, calls)
 			}
 			stats.Calls += calls
+			if err := refuseHandleValues(source, pkg.files[source]); err != nil {
+				return nil, stats, err
+			}
 			accounted := 0
 			for _, declaration := range pkg.files[source].Decls {
 				function, ok := declaration.(*ast.FuncDecl)
@@ -247,6 +257,53 @@ func ScanRoutes(root string) ([]Registration, ScanStats, error) {
 	return result, stats, nil
 }
 
+// refuseHandleValues fails on any .Handle/.HandleFunc that is not called
+// directly, such as a method value `add := mux.Handle`, because a later call
+// through the alias registers a route the scan cannot see. A qualified name
+// from another imported package (the windows.Handle type) is not a mux method.
+func refuseHandleValues(source string, file *ast.File) error {
+	packages := map[string]bool{}
+	for _, spec := range file.Imports {
+		path, _ := strconv.Unquote(spec.Path.Value)
+		name := path[strings.LastIndex(path, "/")+1:]
+		if spec.Name != nil {
+			name = spec.Name.Name
+		}
+		if path != "net/http" {
+			packages[name] = true
+		}
+	}
+	var failure error
+	var stack []ast.Node
+	ast.Inspect(file, func(node ast.Node) bool {
+		if node == nil {
+			stack = stack[:len(stack)-1]
+			return true
+		}
+		if failure != nil {
+			return false
+		}
+		parent := ast.Node(nil)
+		if len(stack) > 0 {
+			parent = stack[len(stack)-1]
+		}
+		stack = append(stack, node)
+		selector, ok := node.(*ast.SelectorExpr)
+		if !ok || (selector.Sel.Name != "Handle" && selector.Sel.Name != "HandleFunc") {
+			return true
+		}
+		if call, isCall := parent.(*ast.CallExpr); isCall && call.Fun == selector {
+			return true
+		}
+		if base, isIdent := selector.X.(*ast.Ident); isIdent && packages[base.Name] {
+			return true
+		}
+		failure = fmt.Errorf("%s: .%s used as a value; register routes by calling the mux directly", source, selector.Sel.Name)
+		return false
+	})
+	return failure
+}
+
 func isHandleCall(call *ast.CallExpr) bool {
 	selector, ok := call.Fun.(*ast.SelectorExpr)
 	return ok && (selector.Sel.Name == "Handle" || selector.Sel.Name == "HandleFunc")
@@ -346,9 +403,20 @@ func scanFunction(pkg *goPackage, source string, function *ast.FuncDecl, stats *
 		var printedReceiver bytes.Buffer
 		printer.Fprint(&printedReceiver, token.NewFileSet(), method.X)
 		where := fmt.Sprintf("%s %s %s.%s", source, functionKey(function), printedReceiver.String(), method.Sel.Name)
+		if isServeMuxMethodExpression(method.X) {
+			failure = fmt.Errorf("%s: method-expression registration is not supported; call Handle on the mux", where)
+			return false
+		}
 		if len(call.Args) != 2 {
 			// ServeMux.Handle/HandleFunc always take two arguments; anything else
-			// (for example the windows.Handle conversion) is not a route.
+			// (for example the windows.Handle conversion) is not a route, unless
+			// an argument spells a route pattern.
+			for _, argument := range call.Args {
+				if pattern, ok := current.eval(argument); ok && looksLikePattern(pattern) {
+					failure = fmt.Errorf("%s: %d-argument call carries route pattern %q", where, len(call.Args), pattern)
+					return false
+				}
+			}
 			stats.NonRoute = append(stats.NonRoute, fmt.Sprintf("%s: %d arguments, not ServeMux.%s", where, len(call.Args), method.Sel.Name))
 			return true
 		}
@@ -463,8 +531,10 @@ func (current *scope) registration(source, server, pattern string, handler ast.E
 		method, route = "ANY", pattern
 	}
 	item := Registration{Operation: method + " " + strings.TrimSpace(route), Server: server, Source: source}
-	var wrappers []string
+	var wrappers []string     // wrapper names, outermost first
+	var wrapperCalls []string // wrapper names with their non-handler arguments
 	var body ast.Node
+	var handlerCall ast.Node // a factory call, so its literal arguments count
 	expr := handler
 	for depth := 0; ; depth++ {
 		if depth > 16 {
@@ -482,17 +552,41 @@ func (current *scope) registration(source, server, pattern string, handler ast.E
 				continue
 			}
 			selector, ok := value.Fun.(*ast.SelectorExpr)
-			if !ok {
+			if !ok || len(value.Args) == 0 {
 				return item, fmt.Errorf("%s: unsupported handler expression for %s", source, item.Operation)
 			}
-			if len(value.Args) == 1 && !isBasicLiteral(value.Args[0]) {
-				wrappers = append(wrappers, selector.Sel.Name)
-				expr = value.Args[0]
+			name := selector.Sel.Name
+			// The handler argument of a wrapper is its last argument; every
+			// other argument must be a resolvable constant. A call whose
+			// arguments include a handler is a wrapper and must be reviewed.
+			last := value.Args[len(value.Args)-1]
+			_, registered := boundaries[name]
+			if registered || isHandlerLike(last, substitute) {
+				if !registered {
+					return item, fmt.Errorf("%s: wrapper %s on %s has no reviewed boundary", source, name, item.Operation)
+				}
+				var parameters []string
+				for _, argument := range value.Args[:len(value.Args)-1] {
+					literal, ok := current.eval(argument)
+					if !ok {
+						return item, fmt.Errorf("%s: wrapper %s on %s has a non-constant argument", source, name, item.Operation)
+					}
+					parameters = append(parameters, strconv.Quote(literal))
+				}
+				wrappers = append(wrappers, name)
+				wrapperCalls = append(wrapperCalls, name+"("+strings.Join(parameters, ",")+")")
+				expr = last
 				continue
 			}
+			for _, argument := range value.Args {
+				if _, ok := current.eval(argument); !ok {
+					return item, fmt.Errorf("%s: handler factory %s on %s has a non-constant argument", source, name, item.Operation)
+				}
+			}
 			// Handler factory such as s.savePetWorkbenchDocument("core").
-			item.Handler = selector.Sel.Name
-			body = current.pkg.methods[selector.Sel.Name]
+			item.Handler = name
+			handlerCall = value
+			body = current.pkg.methods[name]
 		case *ast.SelectorExpr:
 			item.Handler = value.Sel.Name
 			body = current.pkg.methods[value.Sel.Name]
@@ -504,25 +598,47 @@ func (current *scope) registration(source, server, pattern string, handler ast.E
 		}
 		break
 	}
+	if _, isWrapper := boundaries[item.Handler]; isWrapper {
+		return item, fmt.Errorf("%s: %s resolved to the wrapper %s instead of its handler", source, item.Operation, item.Handler)
+	}
 	if body == nil || body == (*ast.FuncDecl)(nil) {
 		return item, fmt.Errorf("%s: handler %s for %s has no unique declaration in %s", source, item.Handler, item.Operation, current.pkg.dir)
 	}
-	item.Wrappers = strings.Join(wrappers, ">")
+	item.Wrappers = strings.Join(wrapperCalls, ">")
 	item.Boundary = "transport_auth"
-	for _, wrapper := range wrappers {
-		if _, ok := boundaries[wrapper]; !ok {
-			return item, fmt.Errorf("%s: wrapper %s on %s has no reviewed boundary", source, wrapper, item.Operation)
-		}
-	}
 	if len(wrappers) > 0 {
 		item.Boundary = boundaries[wrappers[0]] // the outermost wrapper authenticates first
 	}
-	var printed bytes.Buffer
-	if err := printer.Fprint(&printed, current.pkg.fset, body); err != nil {
+	// The fingerprint covers everything a reviewed verdict depends on: the
+	// wrapper chain with its arguments, each wrapper's own source, a factory's
+	// arguments and the handler's source.
+	var fingerprint bytes.Buffer
+	fingerprint.WriteString(item.Wrappers + "\n")
+	for _, name := range wrappers {
+		declaration := current.pkg.methods[name]
+		if declaration == nil {
+			return item, fmt.Errorf("%s: wrapper %s on %s has no unique declaration in %s", source, name, item.Operation, current.pkg.dir)
+		}
+		if err := printer.Fprint(&fingerprint, current.pkg.fset, declaration); err != nil {
+			return item, err
+		}
+		fingerprint.WriteString("\n")
+	}
+	if handlerCall != nil {
+		if err := printer.Fprint(&fingerprint, current.pkg.fset, handlerCall); err != nil {
+			return item, err
+		}
+		fingerprint.WriteString("\n")
+	}
+	var handlerSource bytes.Buffer
+	if err := printer.Fprint(&handlerSource, current.pkg.fset, body); err != nil {
 		return item, err
 	}
-	sum := sha256.Sum256([]byte(item.Wrappers + "\n" + printed.String()))
-	item.Fingerprint = hex.EncodeToString(sum[:6])
+	fingerprint.Write(handlerSource.Bytes())
+	sum := sha256.Sum256(fingerprint.Bytes())
+	item.Fingerprint = fingerprintVersion + hex.EncodeToString(sum[:6])
+	legacy := sha256.Sum256([]byte(strings.Join(wrappers, ">") + "\n" + handlerSource.String()))
+	item.LegacyFingerprint = hex.EncodeToString(legacy[:6])
 	item.RejectOnly = rejectOnly(body)
 	return item, nil
 }
@@ -553,6 +669,54 @@ func (current *scope) eval(expr ast.Expr) (string, bool) {
 	return "", false
 }
 
+// isHandlerLike reports an expression that can only be an http.Handler or
+// handler function: a method value, a function literal, http.HandlerFunc(...)
+// or a call that itself wraps one.
+func isHandlerLike(expr ast.Expr, substitute func(ast.Expr) ast.Expr) bool {
+	if substitute != nil {
+		if replaced := substitute(expr); replaced != nil {
+			expr = replaced
+		}
+	}
+	switch value := expr.(type) {
+	case *ast.SelectorExpr, *ast.FuncLit:
+		return true
+	case *ast.CallExpr:
+		if isSelector(value.Fun, "http", "HandlerFunc") {
+			return true
+		}
+		for _, argument := range value.Args {
+			if isHandlerLike(argument, substitute) {
+				return true
+			}
+		}
+	}
+	return false
+}
+
+func looksLikePattern(value string) bool {
+	if strings.HasPrefix(value, "/") {
+		return true
+	}
+	method, route, found := strings.Cut(value, " ")
+	return found && strings.HasPrefix(strings.TrimSpace(route), "/") && method != "" && strings.ToUpper(method) == method
+}
+
+// isServeMuxMethodExpression matches (*http.ServeMux).Handle style calls.
+func isServeMuxMethodExpression(expr ast.Expr) bool {
+	for {
+		paren, ok := expr.(*ast.ParenExpr)
+		if !ok {
+			break
+		}
+		expr = paren.X
+	}
+	if star, ok := expr.(*ast.StarExpr); ok {
+		expr = star.X
+	}
+	return isSelector(expr, "http", "ServeMux")
+}
+
 func isServeMuxType(expr ast.Expr) bool {
 	star, ok := expr.(*ast.StarExpr)
 	return ok && isSelector(star.X, "http", "ServeMux")
diff --git a/internal/petcoverage/runtime.go b/internal/petcoverage/runtime.go
index 72ac3ff..235ab5a 100644
--- a/internal/petcoverage/runtime.go
+++ b/internal/petcoverage/runtime.go
@@ -4,16 +4,21 @@ import (
 	"fmt"
 	"net/http"
 	"net/http/httptest"
+	"reflect"
+	"sort"
 	"strings"
 )
 
-// VerifyMux checks the scan against a live ServeMux: for every scanned
-// operation of server, a request built from its pattern must be routed by
-// mux to exactly that registered pattern. A scanned route the mux does not
-// serve, or serves under a different pattern, is an error.
+// VerifyMux checks the scan against a live ServeMux in both directions.
+// Forward: for every scanned operation of server, a request built from its
+// pattern must be routed by mux to exactly that registered pattern. Reverse:
+// every pattern the mux holds must be a scanned operation of server, so a
+// registration the scan missed (an alias, a method expression, generated
+// code) fails here even though no source rule matched it.
 func VerifyMux(mux *http.ServeMux, routes []Registration, server string) (int, error) {
 	checked := 0
 	var problems []string
+	scanned := map[string]bool{}
 	for _, route := range routes {
 		if route.Server != server {
 			continue
@@ -23,18 +28,76 @@ func VerifyMux(mux *http.ServeMux, routes []Registration, server string) (int, e
 		if method == "ANY" {
 			method, want = http.MethodPatch, path
 		}
+		scanned[want] = true
 		request := httptest.NewRequest(method, concretePath(path), nil)
 		if _, pattern := mux.Handler(request); pattern != want {
 			problems = append(problems, fmt.Sprintf("%s: live mux routes it to %q", route.Operation, pattern))
 		}
 		checked++
 	}
+	live, err := LivePatterns(mux)
+	if err != nil {
+		return checked, err
+	}
+	for _, pattern := range live {
+		if !scanned[pattern] {
+			problems = append(problems, fmt.Sprintf("live mux serves %q, which the scan did not find", pattern))
+		}
+	}
+	if len(live) != checked {
+		problems = append(problems, fmt.Sprintf("live mux holds %d patterns, scan has %d", len(live), checked))
+	}
 	if len(problems) > 0 {
 		return checked, fmt.Errorf("scan and live %s mux disagree:\n%s", server, strings.Join(problems, "\n"))
 	}
 	return checked, nil
 }
 
+// LivePatterns lists every pattern registered on mux. net/http has no public
+// enumeration, so this reads the conflict index the mux keeps of all its
+// patterns (Go 1.22+ routingIndex: segments and multis). If that layout
+// changes, it returns an error instead of an empty list, so the check fails
+// closed rather than passing vacuously.
+func LivePatterns(mux *http.ServeMux) ([]string, error) {
+	index := reflect.ValueOf(mux).Elem().FieldByName("index")
+	if !index.IsValid() {
+		return nil, fmt.Errorf("http.ServeMux has no routing index; update LivePatterns for this Go version")
+	}
+	segments, multis := index.FieldByName("segments"), index.FieldByName("multis")
+	if segments.Kind() != reflect.Map || multis.Kind() != reflect.Slice {
+		return nil, fmt.Errorf("http.ServeMux routing index changed shape; update LivePatterns")
+	}
+	seen := map[string]bool{}
+	add := func(list reflect.Value) error {
+		for position := 0; position < list.Len(); position++ {
+			text := list.Index(position).Elem().FieldByName("str")
+			if text.Kind() != reflect.String {
+				return fmt.Errorf("http pattern has no original string; update LivePatterns")
+			}
+			seen[text.String()] = true
+		}
+		return nil
+	}
+	iterator := segments.MapRange()
+	for iterator.Next() {
+		if err := add(iterator.Value()); err != nil {
+			return nil, err
+		}
+	}
+	if err := add(multis); err != nil {
+		return nil, err
+	}
+	patterns := make([]string, 0, len(seen))
+	for pattern := range seen {
+		patterns = append(patterns, pattern)
+	}
+	sort.Strings(patterns)
+	if len(patterns) == 0 {
+		return nil, fmt.Errorf("no patterns read from a mux; update LivePatterns")
+	}
+	return patterns, nil
+}
+
 // concretePath fills every wildcard of a ServeMux path pattern.
 func concretePath(pattern string) string {
 	var builder strings.Builder
```
