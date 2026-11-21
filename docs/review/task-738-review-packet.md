# task-738 review packet：拦截 forward --remove-all，限制 forward --remove

本轮基线：`a696f84`（main 合并后的 HEAD）。本轮实现：`1355ab1`。分支 `aha/task_9ee95bee101bad139502`。未合并 main、未 push、未部署、未操作真实 adb 或硬件。

本轮只改 `internal/httpapi` 的 Agent ADB 命令策略：新增 `internal/httpapi/agent_adb_forwards.go`，修改 `agent_handlers.go`、`agent_mcp_operation.go`、`server.go` 及两份测试。`reverse`、`--list`、普通 forward 添加的既有行为保留。

## 结论

- `adb forward --remove-all` 在所有被识别的写法下无条件拒绝：大小写、wait 前缀、`--exit-on-write-error`、分离/紧贴 `-s`。返回 `adb_forward_remove_all_not_allowed`，执行器 0 次。
- `adb forward --remove <local>` 只允许删除本 Task 通过同一硬件绑定、经 Agent ADB 执行器成功创建过的 local 名；其他一律拒绝，返回 `adb_forward_remove_not_allowed`，执行器 0 次。
- `forward --list` 和 `forward <local> <remote>`、`--no-rebind` 添加保持可用。
- HTTP 与 MCP 走同一个 `agentADBExec`，错误分类一致；MCP 的错误分类白名单补了两个新码。
- 审计字段未增加；被拒绝的 forward 与既有被拒命令一样不写执行审计。

## AOSP 依据

依据是 AOSP main 源码（`https://android.googlesource.com/platform/packages/modules/adb/+/refs/heads/main/`），本轮只读源码，未在真实 adb 上执行：

- `client/commandline.cpp` 的 forward 分派：`--list` → `adb_query_command("host:list-forward")`；`--remove-all` → `killforward-all`；`--remove <local>` → `killforward:<local>`；`--no-rebind <local> <remote>` 与 `<local> <remote>` → `forward:...`。`--remove-all`、`--list` 都要求没有多余参数（`argc != 1` 直接 `error_exit`）。
- `adb.cpp` 的 `handle_forward_request`：`list-forward` 调用 `format_listeners()`；`killforward-all` 调用 `remove_all_listeners()`；`killforward:<local>` 调用 `remove_listener(pieces[0], transport)`，但 `transport` 形参没有被使用。
- `adb_listeners.cpp`：`remove_listener(local_name, transport)` 只比较 `local_name == (*iter)->local_name`，不比较 transport；`remove_all_listeners()` 删除除 smartsocket 外的全部 listener；`format_listeners()` 遍历全部 listener，每行 `<serial> <local> <remote>`；`install_listener` 在 `tcp:0` 时把 `local_name` 改写成 `tcp:<resolved>`。
- `client/adb_client.cpp`：`_adb_connect` 只在 service 不以 `host` 开头或 `force_switch` 时切换 transport；`forward --list` 走的 `adb_query_command` 不强制切换，因此 `-s <目标>` 不会把列表过滤到当前设备。

这也解释了为什么不能只靠执行器固定的 `-s <target>`：`host:killforward-all` 和 `host:list-forward` 都是 server 级 host 服务，`-s` 只决定连接哪个 transport，不改变这两个服务的语义。

## 设计取舍

1. **整条拒绝 `--remove-all`**：没有可用的设备内替代形式，且 AOSP 明确删除所有 listener。用独立错误码，便于 Agent 区分。
2. **`--remove` 采用进程内记录，而不是整条拒绝**：`agent_handlers.go` 在 add 成功后按 `(task ID, hardware group ID, configured target)` 记录 local 名；remove 先查记录，命中才执行，执行成功（exit 0）后删除记录。这样保留正常清理路径，同时关闭“知道端口名就能删别的设备”的口子。目标串放进 key，避免 owner 把同一 group 改绑到另一台设备后旧记录仍然生效。
3. **只在 exit code 0 时记账**：失败的 add 没有创建 listener，不能记；失败的 remove 保留记录（fail closed）。
4. **`tcp:0` 记实际端口**：AOSP 会把 listener 的 local_name 改写成 `tcp:<resolved>`，add 成功后 stdout 只打印 resolved 端口。解析到纯数字端口就记 `tcp:<port>`；解析不到就不记，之后 remove 更严格。这样 `tcp:0` 的正常清理路径仍然可用。
5. **`--list` 不加工**：见“未覆盖风险”。改动会涉及解析 list 输出、wireless 的 resolved serial 与版本差异，超出本轮范围。
6. **`reverse` 不动**：`reverse:` 是设备侧服务，只影响当前设备。
7. **错误码**：新增 `adb_forward_remove_all_not_allowed`、`adb_forward_remove_not_allowed`，沿用现有 lower_snake 风格，并加入 MCP 的 `safeAgentOperationCode` 白名单。无法识别或参数数量不对的 forward 形态仍返回 `adb_command_not_allowed`，fail closed。

## 测试

新增 `internal/httpapi/agent_adb_forwards_test.go`：

- `TestClassifyADBForward`：列表、remove-all、remove、add、no-rebind、大小写、缺参数、多参数、未知选项、空参数。
- `TestAgentADBForwardDeletePolicy`：USB、网络直连、无线调试三种连接形态 × HTTP/MCP。
  - add → remove 成功 → 再 remove 被拒（记录已删除）；
  - `--no-rebind` add → remove 成功；
  - `tcp:0` add（假执行器返回 `41234\r\n`）→ remove `tcp:41234` 成功；
  - 未记录的 `tcp:29999` remove 被拒；
  - add 返回 exit 1 时不记账，随后 remove 被拒；
  - `--remove-all`、`FORWARD --REMOVE-ALL`、`Forward --Remove-All <extra>`、wait 前缀、`--exit-on-write-error` 前缀、分离/紧贴 `-s <target>` 全部被拒且执行器 0 次；
  - 未知 forward 子命令被拒；
  - 跨绑定隔离：USB 绑定创建的 `tcp:22005` 不能从 TCP 绑定删除。
  - 每个被拒用例都断言执行器新增调用为 0；每个放行用例断言执行器恰好 1 次。

既有 `TestAgentADBExecParsesCommandBeforeApplyingPolicy` 扩充了 `forward tcp:... tcp:...` 与 `--no-rebind` 两个放行用例，原有用例未删除。`parseADBCommand` 的返回结构新增 `arguments` 字段，解析测试改用 `reflect.DeepEqual`。

## 实跑命令与结果

环境：本 worktree，`go1.27.4 linux/amd64`。`TMPDIR`、`GOTMPDIR` 指向本 Task scratch。

```text
go test ./internal/httpapi -run 'TestClassifyADBForward|TestParseADBCommandSkipsGlobalOptionsAndWaitPrefixes' -count=1 -timeout=5m
退出码 0，ok ... 0.014s

go test ./internal/httpapi -run 'TestAgentADBForwardDeletePolicy' -count=1 -timeout=10m
退出码 0，ok ... 8.175s

go test ./internal/httpapi -run 'TestAgentADBForwardDeletePolicy|TestClassifyADBForward' -race -count=1 -timeout=10m
退出码 0，ok ... 34.677s

go test ./internal/httpapi -run 'TestAgentADBExecParsesCommandBeforeApplyingPolicy|TestAgentADBExecUsesTaskDeviceAndReturnsBinary' -count=1 -timeout=10m
退出码 0，ok ... 12.526s

go test ./internal/httpapi -run 'ADB|Adb|adb' -count=1 -timeout=10m
退出码 0，ok ... 70.801s

go vet ./internal/httpapi
退出码 0，无诊断

git diff --check
退出码 0
```

未运行全仓测试，未合并、未 push、未部署。

## 未覆盖风险

- **`forward --list` 仍会列出其他设备**：AOSP 的 `format_listeners()` 不按设备过滤，`adb_query_command` 也不强制切换；返回内容里带其他设备的 serial，reverse 条目显示为 `(reverse)`。本轮按要求保留现状，未做输出过滤；如需封堵，应在 Agent API 层解析并过滤，或在评审后单独立项。这是只读信息泄露，不是删除副作用。
- **普通 add 可以 rebind 其他设备的 local 名**：AOSP `install_listener` 在没有 `--no-rebind` 时会把已存在的同名 listener 改绑到当前 transport。也就是说 `forward tcp:8080 tcp:9999` 可能把别的设备已有的 `tcp:8080` 抢过来。这是 add 侧的跨设备副作用，不在本轮“删除”范围内；任务又明确要求保留正常 add，所以未自动注入 `--no-rebind`。需要严格隔离时，调用方应使用 `--no-rebind`；后续如需默认防抢绑，可另开任务。
- **进程重启后记录丢失**：重启前创建的 forward 无法再通过 Agent API 删除（只能由 owner 在宿主清理或重建后再删）。这是 fail closed，不是放行。
- **记录可能过期**：forward 在 Agent API 之外被删除（owner 在宿主手工清理、adb server 重启）后，端口若被另一设备复用，旧的进程内记录仍可能允许一次 remove。触发需要同一进程内发生外部删除与端口复用，概率低；若要彻底消除，需要在 remove 前执行一次 `forward --list` 并按 serial 复核，这会把每次 remove 变成两次执行器调用，本轮未做。
- **`tcp:0` 的 stdout 依赖**：若某个 adb 版本的输出不是裸端口号，则不记账，之后 remove 被拒（fail closed）。
- **未在真实 adb / Windows 生产宿主验证**：Builder 上没有 `adb`。以上结论来自 AOSP main 源码和注入执行器的 HTTP/MCP 测试，不能称为实机协议验证。
- **未核对老版本 adb**：`--remove-all`/`--remove` 的语法在 AOSP main 上确认；部署用的旧版 platform-tools 若有别的子命令，按本轮 fail-closed 的 classifier 会被拒绝，但未逐个版本核对。

## 本轮完整 diff（a696f84 → 1355ab1）

~~~~diff
diff --git a/internal/httpapi/agent_adb_forwards.go b/internal/httpapi/agent_adb_forwards.go
new file mode 100644
index 0000000..f4dfdef
--- /dev/null
+++ b/internal/httpapi/agent_adb_forwards.go
@@ -0,0 +1,99 @@
+package httpapi
+
+import (
+	"strconv"
+	"strings"
+	"sync"
+
+	"github.com/ChinaKai/AHA2/internal/hardware"
+)
+
+// adbForwardScope names one Task hardware binding for the forward registry.
+// The configured target is part of the key: if the owner re-points the same
+// group at another device, forwards created against the old target must not
+// become removable through the new binding.
+func adbForwardScope(taskID, groupID, target string) string {
+	return taskID + "\x00" + groupID + "\x00" + target
+}
+
+// registeredADBForwardLocal is the local name recorded after a successful
+// add. adb prints the port it picked for "tcp:0" instead of storing tcp:0 as
+// the listener name, so that port is what a later --remove must name. Any
+// unexpected output leaves the forward unrecorded, which only makes the later
+// remove stricter.
+func registeredADBForwardLocal(local string, result hardware.ADBExecResult) string {
+	if local != "tcp:0" {
+		return local
+	}
+	value := strings.TrimSpace(string(result.Stdout))
+	if value == "" {
+		return ""
+	}
+	for _, character := range value {
+		if character < '0' || character > '9' {
+			return ""
+		}
+	}
+	port, err := strconv.Atoi(value)
+	if err != nil || port < 1 || port > 65535 {
+		return ""
+	}
+	return "tcp:" + strconv.Itoa(port)
+}
+
+// adbForwardRegistry records the forward local names this AHA process created
+// through the Agent ADB executor, scoped to one Task hardware binding. adb
+// forward --remove compares only the local name and not the device transport,
+// so without this record one Task could delete another device's forward just
+// by knowing (or reading from --list) the port name.
+//
+// The registry is deliberately in-process. After a restart previously created
+// forwards are no longer removable through the Agent API until they are
+// created again; that is stricter, not permissive. A forward removed outside
+// this path (owner on the host, adb server restart) can leave a stale entry
+// for a reused port; see the review packet for that residual risk.
+type adbForwardRegistry struct {
+	mu      sync.Mutex
+	byScope map[string]map[string]struct{}
+}
+
+func newADBForwardRegistry() *adbForwardRegistry {
+	return &adbForwardRegistry{byScope: map[string]map[string]struct{}{}}
+}
+
+func (registry *adbForwardRegistry) add(scope, local string) {
+	if registry == nil || scope == "" || local == "" {
+		return
+	}
+	registry.mu.Lock()
+	defer registry.mu.Unlock()
+	locals := registry.byScope[scope]
+	if locals == nil {
+		locals = map[string]struct{}{}
+		registry.byScope[scope] = locals
+	}
+	locals[local] = struct{}{}
+}
+
+func (registry *adbForwardRegistry) remove(scope, local string) {
+	if registry == nil {
+		return
+	}
+	registry.mu.Lock()
+	defer registry.mu.Unlock()
+	locals := registry.byScope[scope]
+	delete(locals, local)
+	if len(locals) == 0 {
+		delete(registry.byScope, scope)
+	}
+}
+
+func (registry *adbForwardRegistry) allows(scope, local string) bool {
+	if registry == nil || scope == "" || local == "" {
+		return false
+	}
+	registry.mu.Lock()
+	defer registry.mu.Unlock()
+	_, ok := registry.byScope[scope][local]
+	return ok
+}
diff --git a/internal/httpapi/agent_adb_forwards_test.go b/internal/httpapi/agent_adb_forwards_test.go
new file mode 100644
index 0000000..c74f20b
--- /dev/null
+++ b/internal/httpapi/agent_adb_forwards_test.go
@@ -0,0 +1,221 @@
+package httpapi
+
+import (
+	"context"
+	"encoding/json"
+	"net/http"
+	"net/http/httptest"
+	"strings"
+	"sync/atomic"
+	"testing"
+	"time"
+
+	"github.com/ChinaKai/AHA2/internal/domain"
+	"github.com/ChinaKai/AHA2/internal/hardware"
+)
+
+func TestClassifyADBForward(t *testing.T) {
+	for _, tc := range []struct {
+		name   string
+		args   []string
+		action adbForwardAction
+		local  string
+		ok     bool
+	}{
+		{"list", []string{"--list"}, adbForwardList, "", true},
+		{"uppercase_list", []string{"--LIST"}, adbForwardList, "", true},
+		{"remove_all", []string{"--remove-all"}, adbForwardRemoveAll, "", true},
+		{"uppercase_remove_all", []string{"--REMOVE-ALL"}, adbForwardRemoveAll, "", true},
+		{"remove_all_with_trailing", []string{"--remove-all", "tcp:1"}, adbForwardRemoveAll, "", true},
+		{"remove", []string{"--remove", "tcp:1"}, adbForwardRemove, "tcp:1", true},
+		{"uppercase_remove", []string{"--REMOVE", "tcp:1"}, adbForwardRemove, "tcp:1", true},
+		{"remove_missing_local", []string{"--remove"}, adbForwardInvalid, "", false},
+		{"remove_extra", []string{"--remove", "tcp:1", "tcp:2"}, adbForwardInvalid, "", false},
+		{"remove_option_local", []string{"--remove", "--remove-all"}, adbForwardInvalid, "", false},
+		{"add", []string{"tcp:1", "tcp:2"}, adbForwardAdd, "tcp:1", true},
+		{"add_missing_remote", []string{"tcp:1"}, adbForwardInvalid, "", false},
+		{"add_extra", []string{"tcp:1", "tcp:2", "tcp:3"}, adbForwardInvalid, "", false},
+		{"no_rebind", []string{"--no-rebind", "tcp:1", "tcp:2"}, adbForwardAdd, "tcp:1", true},
+		{"no_rebind_missing_remote", []string{"--no-rebind", "tcp:1"}, adbForwardInvalid, "", false},
+		{"unknown_option", []string{"--rebind", "tcp:1", "tcp:2"}, adbForwardInvalid, "", false},
+		{"empty", nil, adbForwardInvalid, "", false},
+		{"empty_local", []string{"", "tcp:2"}, adbForwardInvalid, "", false},
+	} {
+		t.Run(tc.name, func(t *testing.T) {
+			action, local, ok := classifyADBForward(tc.args)
+			if action != tc.action || local != tc.local || ok != tc.ok {
+				t.Fatalf("classifyADBForward(%q)=(%v, %q, %v), want (%v, %q, %v)", tc.args, action, local, ok, tc.action, tc.local, tc.ok)
+			}
+		})
+	}
+}
+
+type adbExecOutcome struct {
+	allowed bool
+	code    string
+	message string
+}
+
+// callAgentADBExec issues one hardware_adb_exec request over HTTP or MCP and
+// normalizes both answers: allowed means the handler reached the executor.
+func callAgentADBExec(t *testing.T, serverURL, token string, group domain.HardwareGroup, args []string, mcp bool) adbExecOutcome {
+	t.Helper()
+	if mcp {
+		result := coreMCPCallTool(t, serverURL+"/api/v1/agent/hardware/mcp", token, "hardware_adb_exec", map[string]any{"hardware_id": group.ID, "args": args})
+		if result.Status != http.StatusOK {
+			t.Fatalf("MCP status=%d args=%q", result.Status, args)
+		}
+		if result.IsError {
+			code, message := result.Text, ""
+			if index := strings.Index(result.Text, ": "); index >= 0 {
+				code, message = result.Text[:index], result.Text[index+2:]
+			}
+			return adbExecOutcome{code: code, message: message}
+		}
+		var payload struct {
+			OK bool `json:"ok"`
+		}
+		if err := json.Unmarshal([]byte(result.Text), &payload); err != nil {
+			t.Fatalf("MCP result is not JSON: %v text=%q", err, result.Text)
+		}
+		return adbExecOutcome{allowed: payload.OK}
+	}
+	response := agentRequest(t, serverURL+"/api/v1/agent/hardware/"+group.ID+"/adb/exec", http.MethodPost, token, map[string]any{"args": args})
+	var payload struct {
+		OK      bool   `json:"ok"`
+		Error   string `json:"error"`
+		Message string `json:"message"`
+	}
+	decodeResponse(t, response, &payload)
+	if response.StatusCode == http.StatusOK && payload.OK {
+		return adbExecOutcome{allowed: true}
+	}
+	return adbExecOutcome{code: payload.Error, message: payload.Message}
+}
+
+func expectAgentADBExec(t *testing.T, serverURL, token string, group domain.HardwareGroup, args []string, mcp bool, executions *atomic.Int32, wantAllowed bool, wantCode string) {
+	t.Helper()
+	before := executions.Load()
+	outcome := callAgentADBExec(t, serverURL, token, group, args, mcp)
+	if outcome.allowed != wantAllowed {
+		t.Fatalf("mcp=%v args=%q allowed=%v code=%q message=%q, want allowed=%v", mcp, args, outcome.allowed, outcome.code, outcome.message, wantAllowed)
+	}
+	if !wantAllowed && outcome.code != wantCode {
+		t.Fatalf("mcp=%v args=%q code=%q message=%q, want code=%q", mcp, args, outcome.code, outcome.message, wantCode)
+	}
+	wantExecutions := int32(0)
+	if wantAllowed {
+		wantExecutions = 1
+	}
+	if got := executions.Load() - before; got != wantExecutions {
+		t.Fatalf("mcp=%v args=%q executions=%d, want %d", mcp, args, got, wantExecutions)
+	}
+}
+
+// TestAgentADBForwardDeletePolicy covers the host-wide forward deletes over
+// both transports: --remove-all is always refused, --remove is limited to the
+// local names this Task created through the same hardware binding, and the
+// add/list forms keep working.
+func TestAgentADBForwardDeletePolicy(t *testing.T) {
+	h := newCoreMCPHarness(t)
+	task := createHardwareAPITask(t, h.database)
+	now := time.Now().UTC()
+	groups := []domain.HardwareGroup{
+		{ID: "usb", Network: domain.HardwareNetworkConfig{ADBConnection: "usb", ADBSerial: "123SERIAL"}},
+		{ID: "tcp", Network: domain.HardwareNetworkConfig{ADBConnection: "tcp", Host: "192.0.2.10", Port: 5555}},
+		{ID: "wireless", Network: domain.HardwareNetworkConfig{Host: "192.0.2.11", Port: 41234}},
+	}
+	for index := range groups {
+		groups[index].Mode, groups[index].Access = domain.HardwareModeNetwork, domain.HardwareAccessReadWrite
+		groups[index].Network.Protocol = domain.HardwareProtocolADB
+		groups[index].CreatedAt, groups[index].UpdatedAt = now, now
+	}
+	if err := h.database.ReplaceHardwareGroups(h.ctx, task.ID, groups); err != nil {
+		t.Fatal(err)
+	}
+	var executions atomic.Int32
+	results := map[string]hardware.ADBExecResult{
+		// adb prints the port it picked for tcp:0; only that port can be removed.
+		"forward tcp:0 tcp:8080":     {Stdout: []byte("41234\r\n")},
+		"forward tcp:22003 tcp:8080": {ExitCode: 1, Stderr: []byte("error: cannot bind listener")},
+	}
+	executor := func(_ context.Context, _ string, args []string, _ []byte, _ time.Duration) (hardware.ADBExecResult, error) {
+		executions.Add(1)
+		if result, ok := results[strings.Join(args, " ")]; ok {
+			return result, nil
+		}
+		return hardware.ADBExecResult{Stdout: []byte("ok")}, nil
+	}
+	server := httptest.NewServer(New(Config{
+		Store: h.database, App: h.service, AgentCapabilities: h.caps,
+		ExecuteADB: executor, ExecuteADBTCP: executor, ExecuteADBUSB: executor,
+	}).Handler())
+	defer server.Close()
+	turn := createCoreTurn(t, h.database, task, "main", "batch-adb-forward-delete-policy")
+	token := h.issue(task.ID, "main", turn.ID)
+
+	for _, group := range groups {
+		target := group.Network.ADBSerial
+		switch group.ID {
+		case "tcp":
+			target = "192.0.2.10:5555"
+		case "wireless":
+			target = "192.0.2.11:41234"
+		}
+		// A wireless binding cannot verify a caller-supplied serial at all, so
+		// its -s forms stop at the resource boundary with the generic code.
+		serialBlockedCode := "adb_forward_remove_all_not_allowed"
+		if group.Network.ADBWireless() {
+			serialBlockedCode = "adb_command_not_allowed"
+		}
+		for _, mcp := range []bool{false, true} {
+			name := group.ID + "/http"
+			if mcp {
+				name = group.ID + "/mcp"
+			}
+			t.Run(name, func(t *testing.T) {
+				// Add then remove the same local name: the first remove succeeds
+				// and drops the record, so the second is refused.
+				expectAgentADBExec(t, server.URL, token, group, []string{"forward", "tcp:22001", "tcp:8080"}, mcp, &executions, true, "")
+				expectAgentADBExec(t, server.URL, token, group, []string{"forward", "--remove", "tcp:22001"}, mcp, &executions, true, "")
+				expectAgentADBExec(t, server.URL, token, group, []string{"forward", "--remove", "tcp:22001"}, mcp, &executions, false, "adb_forward_remove_not_allowed")
+
+				// --no-rebind is an add form and records its local name too.
+				expectAgentADBExec(t, server.URL, token, group, []string{"forward", "--no-rebind", "tcp:22004", "tcp:8080"}, mcp, &executions, true, "")
+				expectAgentADBExec(t, server.URL, token, group, []string{"forward", "--remove", "tcp:22004"}, mcp, &executions, true, "")
+
+				// tcp:0 is recorded under the port adb actually picked.
+				expectAgentADBExec(t, server.URL, token, group, []string{"forward", "tcp:0", "tcp:8080"}, mcp, &executions, true, "")
+				expectAgentADBExec(t, server.URL, token, group, []string{"forward", "--remove", "tcp:41234"}, mcp, &executions, true, "")
+				expectAgentADBExec(t, server.URL, token, group, []string{"forward", "--remove", "tcp:41234"}, mcp, &executions, false, "adb_forward_remove_not_allowed")
+
+				// An unknown local name is never removable.
+				expectAgentADBExec(t, server.URL, token, group, []string{"forward", "--remove", "tcp:29999"}, mcp, &executions, false, "adb_forward_remove_not_allowed")
+
+				// A failed add created nothing and must not be recorded.
+				expectAgentADBExec(t, server.URL, token, group, []string{"forward", "tcp:22003", "tcp:8080"}, mcp, &executions, true, "")
+				expectAgentADBExec(t, server.URL, token, group, []string{"forward", "--remove", "tcp:22003"}, mcp, &executions, false, "adb_forward_remove_not_allowed")
+
+				// --remove-all stays blocked in every spelling and prefix form.
+				expectAgentADBExec(t, server.URL, token, group, []string{"forward", "--remove-all"}, mcp, &executions, false, "adb_forward_remove_all_not_allowed")
+				expectAgentADBExec(t, server.URL, token, group, []string{"FORWARD", "--REMOVE-ALL"}, mcp, &executions, false, "adb_forward_remove_all_not_allowed")
+				expectAgentADBExec(t, server.URL, token, group, []string{"Forward", "--Remove-All", "tcp:1"}, mcp, &executions, false, "adb_forward_remove_all_not_allowed")
+				expectAgentADBExec(t, server.URL, token, group, []string{"wait-for-device", "forward", "--remove-all"}, mcp, &executions, false, "adb_forward_remove_all_not_allowed")
+				expectAgentADBExec(t, server.URL, token, group, []string{"--exit-on-write-error", "forward", "--remove-all"}, mcp, &executions, false, "adb_forward_remove_all_not_allowed")
+				expectAgentADBExec(t, server.URL, token, group, []string{"-s", target, "forward", "--remove-all"}, mcp, &executions, false, serialBlockedCode)
+				expectAgentADBExec(t, server.URL, token, group, []string{"-s" + target, "forward", "--remove-all"}, mcp, &executions, false, serialBlockedCode)
+
+				// Unknown forward subcommands still fail closed without running.
+				expectAgentADBExec(t, server.URL, token, group, []string{"forward", "--rebind", "tcp:1", "tcp:2"}, mcp, &executions, false, "adb_command_not_allowed")
+			})
+		}
+	}
+
+	// The registry is scoped by hardware binding: a local name created through
+	// the USB binding is not removable through the TCP binding, even though
+	// both hit the same adb server.
+	usb, tcp := groups[0], groups[1]
+	expectAgentADBExec(t, server.URL, token, usb, []string{"forward", "tcp:22005", "tcp:8080"}, false, &executions, true, "")
+	expectAgentADBExec(t, server.URL, token, tcp, []string{"forward", "--remove", "tcp:22005"}, false, &executions, false, "adb_forward_remove_not_allowed")
+	expectAgentADBExec(t, server.URL, token, usb, []string{"forward", "--remove", "tcp:22005"}, false, &executions, true, "")
+}
diff --git a/internal/httpapi/agent_handlers.go b/internal/httpapi/agent_handlers.go
index 9ae9799..50e52de 100644
--- a/internal/httpapi/agent_handlers.go
+++ b/internal/httpapi/agent_handlers.go
@@ -112,6 +112,63 @@ type adbCommand struct {
 	name              string
 	serial            string
 	unsafeGlobalFlags bool
+	// arguments are the tokens after the command name. They are only used by
+	// commands with device-scoped subcommands (forward today); a wait-only
+	// prefix such as "wait-for-device" leaves them empty.
+	arguments []string
+}
+
+type adbForwardAction int
+
+const (
+	adbForwardInvalid adbForwardAction = iota
+	adbForwardAdd
+	adbForwardList
+	adbForwardRemove
+	adbForwardRemoveAll
+)
+
+// classifyADBForward recognizes only the device-scoped forward shapes. Any
+// other argv fails closed: a future or vendor subcommand (or the usage text's
+// unknown-option path) must never become a way to reach the host-wide
+// listener table. adb compares the subcommand literally, but the check is
+// case-insensitive so an uppercase spelling cannot slip past the policy.
+func classifyADBForward(arguments []string) (adbForwardAction, string, bool) {
+	if len(arguments) == 0 {
+		return adbForwardInvalid, "", false
+	}
+	switch strings.ToLower(arguments[0]) {
+	case "--remove-all":
+		// Trailing tokens are ignored by adb's usage path; still refuse the
+		// host-wide operation rather than letting it fall through as an add.
+		return adbForwardRemoveAll, "", true
+	case "--remove":
+		if len(arguments) != 2 || !validADBForwardSpec(arguments[1]) {
+			return adbForwardInvalid, "", false
+		}
+		return adbForwardRemove, arguments[1], true
+	case "--list":
+		if len(arguments) != 1 {
+			return adbForwardInvalid, "", false
+		}
+		return adbForwardList, "", true
+	case "--no-rebind":
+		if len(arguments) != 3 || !validADBForwardSpec(arguments[1]) || !validADBForwardSpec(arguments[2]) {
+			return adbForwardInvalid, "", false
+		}
+		return adbForwardAdd, arguments[1], true
+	}
+	if !validADBForwardSpec(arguments[0]) || len(arguments) != 2 || !validADBForwardSpec(arguments[1]) {
+		return adbForwardInvalid, "", false
+	}
+	return adbForwardAdd, arguments[0], true
+}
+
+// validADBForwardSpec rejects empty and option-looking tokens. Every real
+// forward endpoint is a non-empty spec such as tcp:8080, localabstract:name
+// or jdwp:1234, never an option.
+func validADBForwardSpec(value string) bool {
+	return value != "" && !strings.HasPrefix(value, "-")
 }
 
 // parseADBCommand only parses the global prefix. Once a device command is
@@ -167,6 +224,9 @@ func parseADBCommand(args []string) (adbCommand, bool) {
 	}
 	if i < len(args) {
 		command.name = args[i]
+		if i+1 < len(args) {
+			command.arguments = args[i+1:]
+		}
 	}
 	return command, command.name != "" && !strings.HasPrefix(command.name, "-")
 }
@@ -212,6 +272,30 @@ func (s *Server) agentADBExec(writer http.ResponseWriter, request *http.Request)
 		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "adb_command_not_allowed", "message": "这里只允许面向当前设备的 ADB 子命令"})
 		return
 	}
+	// adb forward --remove-all and --remove act on the host-wide listener table,
+	// not on the selected device: --remove-all deletes every device's forwards
+	// on this adb server, and --remove matches only the local name. Keep the
+	// read-only and add forms, but refuse the host-wide delete and only allow
+	// removing a local name this binding previously created through this path.
+	forwardAction := adbForwardInvalid
+	forwardLocal := ""
+	forwardScope := adbForwardScope(task.ID, group.ID, target)
+	if strings.EqualFold(command.name, "forward") {
+		action, local, recognized := classifyADBForward(command.arguments)
+		if !recognized {
+			writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "adb_command_not_allowed", "message": "这里只允许面向当前设备的 ADB 子命令"})
+			return
+		}
+		if action == adbForwardRemoveAll {
+			writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "adb_forward_remove_all_not_allowed", "message": "forward --remove-all 会清除同一 ADB server 上所有设备的转发，禁止使用"})
+			return
+		}
+		if action == adbForwardRemove && !s.adbForwards.allows(forwardScope, local) {
+			writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "adb_forward_remove_not_allowed", "message": "只能删除本 Task 通过该硬件资源创建的转发端口"})
+			return
+		}
+		forwardAction, forwardLocal = action, local
+	}
 	argumentBytes := 0
 	for _, argument := range payload.Args {
 		if strings.ContainsRune(argument, '\x00') || len(argument) > 4096 {
@@ -279,6 +363,18 @@ func (s *Server) agentADBExec(writer http.ResponseWriter, request *http.Request)
 		writeJSON(writer, code, map[string]any{"ok": false, "error": errorCode, "may_have_executed": mayHaveExecuted, "message": err.Error()})
 		return
 	}
+	// A forward only enters or leaves the registry when adb reported success:
+	// a failed add created nothing to remove, and a failed remove leaves the
+	// record in place (fail closed). tcp:0 is stored under the port adb picked,
+	// because the literal tcp:0 cannot be removed later.
+	if result.ExitCode == 0 {
+		switch forwardAction {
+		case adbForwardAdd:
+			s.adbForwards.add(forwardScope, registeredADBForwardLocal(forwardLocal, result))
+		case adbForwardRemove:
+			s.adbForwards.remove(forwardScope, forwardLocal)
+		}
+	}
 	s.audit(request, "task.hardware.adb_exec", "task", task.ID, map[string]any{
 		"hardware_id": group.ID, "argument_count": len(payload.Args),
 		"input_bytes": len(input), "stdout_bytes": len(result.Stdout),
diff --git a/internal/httpapi/agent_handlers_test.go b/internal/httpapi/agent_handlers_test.go
index 6fa9737..abadefc 100644
--- a/internal/httpapi/agent_handlers_test.go
+++ b/internal/httpapi/agent_handlers_test.go
@@ -209,7 +209,7 @@ func TestParseADBCommandSkipsGlobalOptionsAndWaitPrefixes(t *testing.T) {
 		{"attached_numeric_serial", []string{"-s123ABC", "shell"}, adbCommand{name: "shell", serial: "123ABC"}, true},
 		{"attached_nonnumeric_serial", []string{"-sSERIAL", "shell"}, adbCommand{}, false},
 		{"attached_socket_is_unknown", []string{"-Ltcp:5037", "shell"}, adbCommand{}, false},
-		{"multiple_flags_waits", []string{"-d", "-a", "--exit-on-write-error", "-s", "SERIAL", "wait-for-device", "wait-for-usb-device", "install", "-r", "app.apk"}, adbCommand{name: "install", serial: "SERIAL", unsafeGlobalFlags: true}, true},
+		{"multiple_flags_waits", []string{"-d", "-a", "--exit-on-write-error", "-s", "SERIAL", "wait-for-device", "wait-for-usb-device", "install", "-r", "app.apk"}, adbCommand{name: "install", serial: "SERIAL", unsafeGlobalFlags: true, arguments: []string{"-r", "app.apk"}}, true},
 		{"last_serial_wins", []string{"-s", "OTHER", "-s", "SERIAL", "shell"}, adbCommand{name: "shell", serial: "SERIAL"}, true},
 		{"only_wait", []string{"wait-for-device"}, adbCommand{name: "wait-for-device"}, true},
 		{"options_only", []string{"-H", "host", "-P", "5037"}, adbCommand{unsafeGlobalFlags: true}, false},
@@ -219,7 +219,7 @@ func TestParseADBCommandSkipsGlobalOptionsAndWaitPrefixes(t *testing.T) {
 	} {
 		t.Run(tc.name, func(t *testing.T) {
 			got, parsed := parseADBCommand(tc.args)
-			if got != tc.want || parsed != tc.parsed {
+			if !reflect.DeepEqual(got, tc.want) || parsed != tc.parsed {
 				t.Fatalf("parseADBCommand(%q)=(%#v, %v), want (%#v, %v)", tc.args, got, parsed, tc.want, tc.parsed)
 			}
 		})
@@ -276,6 +276,8 @@ func TestAgentADBExecParsesCommandBeforeApplyingPolicy(t *testing.T) {
 			{"shell_arguments_are_not_commands", []string{"shell", "echo", "tcpip", "-s"}, true},
 			{"shell_raw_argument_is_not_command", []string{"shell", "echo", "raw", "host:devices"}, true},
 			{"forward_list_remains_available", []string{"forward", "--list"}, true},
+			{"forward_add_remains_available", []string{"forward", "tcp:22001", "tcp:8080"}, true},
+			{"forward_add_no_rebind_remains_available", []string{"forward", "--no-rebind", "tcp:22002", "tcp:8080"}, true},
 			{"reverse_list_remains_available", []string{"reverse", "--list"}, true},
 			{"wait_only", []string{"wait-for-device"}, true},
 			{"wait_shell", []string{"wait-for-device", "shell"}, true},
diff --git a/internal/httpapi/agent_mcp_operation.go b/internal/httpapi/agent_mcp_operation.go
index 2cb2fdc..ab08dbf 100644
--- a/internal/httpapi/agent_mcp_operation.go
+++ b/internal/httpapi/agent_mcp_operation.go
@@ -42,7 +42,7 @@ func safeAgentOperationCode(body []byte) string {
 	}
 	_ = json.Unmarshal(body, &failure)
 	switch failure.Error {
-	case "hardware_not_connected", "hardware_credentials_not_configured", "hardware_login_options_invalid", "hardware_login_retry_limit", "hardware_encoding_invalid", "adb_output_limit_may_have_executed", "adb_timeout_may_have_executed", "hardware_read_only", "hardware_runtime_unavailable", "hardware_group_not_found", "hardware_transport_invalid", "hardware_connect_failed", "hardware_send_failed", "hardware_login_failed", "ssh_host_key_unknown", "adb_exec_unavailable", "adb_command_not_allowed", "adb_args_invalid", "adb_stdin_invalid", "adb_timeout_invalid", "adb_exec_failed", "managed_process_not_found", "managed_process_already_running", "managed_process_start_failed", "browser_capability_denied", "browser_grant_forbidden", "browser_grant_revoked", "browser_grant_expired", "browser_tab_not_found", "agent_operation_forbidden", "agent_turn_inactive", "task_read_only":
+	case "hardware_not_connected", "hardware_credentials_not_configured", "hardware_login_options_invalid", "hardware_login_retry_limit", "hardware_encoding_invalid", "adb_output_limit_may_have_executed", "adb_timeout_may_have_executed", "hardware_read_only", "hardware_runtime_unavailable", "hardware_group_not_found", "hardware_transport_invalid", "hardware_connect_failed", "hardware_send_failed", "hardware_login_failed", "ssh_host_key_unknown", "adb_exec_unavailable", "adb_command_not_allowed", "adb_forward_remove_all_not_allowed", "adb_forward_remove_not_allowed", "adb_args_invalid", "adb_stdin_invalid", "adb_timeout_invalid", "adb_exec_failed", "managed_process_not_found", "managed_process_already_running", "managed_process_start_failed", "browser_capability_denied", "browser_grant_forbidden", "browser_grant_revoked", "browser_grant_expired", "browser_tab_not_found", "agent_operation_forbidden", "agent_turn_inactive", "task_read_only":
 		return failure.Error
 	default:
 		return "operation_failed"
diff --git a/internal/httpapi/server.go b/internal/httpapi/server.go
index 2e7e0e3..9b0f986 100644
--- a/internal/httpapi/server.go
+++ b/internal/httpapi/server.go
@@ -128,6 +128,7 @@ type Server struct {
 	executeADBUSB            func(context.Context, string, []string, []byte, time.Duration) (hardware.ADBExecResult, error)
 	executeADBTCP            func(context.Context, string, []string, []byte, time.Duration) (hardware.ADBExecResult, error)
 	listADBUSBDevices        func(context.Context) (hardware.ADBUSBDeviceList, error)
+	adbForwards              *adbForwardRegistry
 	version                  string
 	webVersion               string
 	startedAt                time.Time
@@ -255,6 +256,7 @@ func New(config Config) *Server {
 		executeADBUSB:            executeADBUSB,
 		executeADBTCP:            executeADBTCP,
 		listADBUSBDevices:        listADBUSBDevices,
+		adbForwards:              newADBForwardRegistry(),
 		version:                  config.Version,
 		webVersion:               config.WebVersion,
 		startedAt:                startedAt,
~~~~
