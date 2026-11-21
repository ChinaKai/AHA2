# task-732 review packet r2：补拦 raw 协议直通及 server 命令

本轮基线：`08c7dfeb4a8aba2e562c92a02310412209c62f7c`。本轮实现：`ea3146691c5c8941e8c8fe8eaa2a2baff6a183c0`。原始基线：`d7cee5b`；初版实现 `dc6eba3`，评审包 `08c7dfe`。分支仍为 `aha/task_f707a30188b507742e3a`。

按 task-734 的 REQUEST_CHANGES 修正 M1/S1/N1/N2。保留 `08c7dfe` 及之前全部历史，只新增提交；未合并 main、未 push、未部署、未操作硬件。评审由 owner 分身统一安排，本轮不另发起评审。

## 本轮改动与原因

- **M1**：将 `raw` 加入 `blockedADBHostCommands`。`adb raw SERVICE` 会把 SERVICE 原样交给连接函数，既能发 `tcpip:5555`，也能发绕过设备选择的 `host:*` 请求，必须整条禁用。沿用解析后子命令转小写的同一判断，不按 service 字符串零散补洞。`wait-for-device raw ...`、`-s <绑定目标> raw ...`、`-H ... -P ... raw ...` 都经过同一黑名单分支。
- **S1**：补拦 `track-devices`、`server-status`、`reconnect`。前两者直接请求 `host:track-devices*`、`host:server-status`；`reconnect offline` 使用 `host:reconnect-offline`，无法仅靠 `-s` 限定到当前设备。按命令名整体拒绝，因此其设备侧变体也不再开放。
- **N1**：恢复原先 `len(args)==0` 的 `adb_args_invalid`，包括空数组、null；仍是 HTTP 400 和固定提示，执行器零调用。原有参数长度/NUL 的 `adb_args_invalid` 校验保留；空 token、未知/缺值等无法判定命令前缀仍为 `adb_command_not_allowed`，继续 fail-closed，不扩大本轮错误码变更。
- **N2**：新增不带 `-s` 的 `TCPIP 5555`、`RAW host:devices`，三种连接模式均能独立验证大小写归一化，不会只因 serial 不匹配而通过。
- **N3**：MCP 对空数组/null/空字符串的既有 schema 错误分类不在本轮范围，保持现状。这些输入继续走 HTTP 测试边界，不更改 MCP schema 或错误封装。

仅改三个文件：

| 文件 | 本轮内容 |
| --- | --- |
| `internal/httpapi/agent_handlers.go` | 四个黑名单项；恢复空参数错误码 |
| `internal/httpapi/agent_handlers_test.go` | 扩展既有表驱动用例与空参数断言，未删除原用例 |
| `docs/review/task-732-review-packet.md` | 本包：当前结论、本轮增量与累计完整 diff |

产品与测试相对 `08c7dfe`：`+30/-3`（详细 numstat 见下）。前缀解析器与执行器本轮均未修改。

```text
5	1	internal/httpapi/agent_handlers.go
25	2	internal/httpapi/agent_handlers_test.go
```

## 回归覆盖

原 58 组输入保留，新增 13 组，现为 71 组 × USB/网络直连/无线调试 = **213 个 HTTP 子用例**；符合既有 MCP schema 的输入再走 MCP（201 次请求）。另保留 17 个解析用例。

新增输入：

| 分类 | argv |
| --- | --- |
| M1 | `raw tcpip:5555`、`raw host:devices`、`wait-for-device raw host:kill`、`RAW host:devices` |
| M1 前缀 | `-s <绑定目标> raw tcpip:5555`、`-H 127.0.0.1 -P 5037 raw host:devices` |
| S1 | `track-devices`、`server-status`、`reconnect offline` |
| N2 | `TCPIP 5555` |
| 保持可用 | `shell echo raw host:devices`、`forward --list`、`reverse --list` |

每个拒绝请求均断言 HTTP 400 / 固定错误分类，或 MCP HTTP 200 / `isError=true` / 固定分类文本，且执行器新增调用为 **0**。每个允许请求均断言只调用一次，argv 逐项不变。无线调试显式 `-s` 原本无法在 HTTP 层核实 mDNS 后 serial，仍拒绝；因此无线形态的 serial 前缀用例同时受资源边界保护，直接和 wait 的 raw 用例负责独立证明黑名单本身有效。

## forward / reverse 的决定与残余风险

按本轮明确范围，**不扩大拦截 forward 或 reverse**。执行器仍通过 `runADBDeviceCommand` 固定加 `-s <绑定目标>`，且拒绝调用者切换目标。常规转发操作依赖选定的 transport，本轮只补上更明确的协议直通/全局 server 命令；新增正向用例锁定两者的现有可用性。

但不能据此保证所有 forward 子操作均受 `-s` 限制。本轮读取 AOSP `client/commandline.cpp`：`forward --list` 直接走 `adb_query_command("host:list-forward")`，与需要强制选择 transport 的常规 forward 分支不同；`client/adb_client.cpp` 中 `_adb_connect` 对 `host` 开头且没有 force_switch 的 service 不切换设备。**因此列表可能暴露其他设备的转发表；本轮未封堵这个例外，也未把“固定 -s”当作其隔离证明。** `forward --remove-all` 等各子操作的宿主影响、不同版本差异仍需另行评估。`reverse` 保持原逻辑，未做全子操作审计。

## 指定验证与结果

环境：本 worktree，Go `go1.27.4 linux/amd64`。`TMPDIR` 和 `GOTMPDIR` 均指向本 Task scratch，不将测试临时库移到外部目录。测试只使用注入的假执行器或现有假 adb 脚本。

```text
go test ./internal/httpapi -run 'ADB|Adb|adb' -count=1 -timeout=10m
退出码：0
ok github.com/ChinaKai/AHA2/internal/httpapi 344.012s

go vet ./internal/httpapi
退出码：0，无诊断

git diff --check
退出码：0
```

反证：将 `internal/httpapi/agent_handlers.go` 通过 overlay 替换为 **未经改动的 08c7dfe 文件**，保留当前测试，运行：

```text
go test -overlay <scratch>/baseline-r1-overlay.json ./internal/httpapi -run '^TestAgentADBExecParsesCommandBeforeApplyingPolicy/(usb|tcp|wireless)/(direct_raw_tcpip|direct_raw_host_devices|wait_raw_host_kill|direct_uppercase_raw|direct_track_devices|direct_server_status|direct_reconnect)$' -count=1 -timeout=10m
退出码：1（预期），21 个用例均失败；每条均为 mcp=false status=200 error="" executions=1。
FAIL github.com/ChinaKai/AHA2/internal/httpapi 4.461s
```

此反证证明用例能抓到上一版的实际放行，不是编译失败。因为 HTTP 断言先失败，反证不会继续跑该用例的 MCP 分支；修复版的指定测试会同时验证 HTTP 与 MCP。task-734 已有的三形态 HTTP/MCP 复现亦为此次修复依据。

## 验证缺口与边界

- 本轮 `command -v adb` / `shutil.which("adb")` 均未找到真实 adb。未安装 adb、未启动 adb server、未连接 USB/网络设备。不能将注入执行器的 HTTP/MCP 结果称为真实 adb 协议或 Windows 实机验证。
- 原理依据是 AOSP main 的 `client/commandline.cpp`（raw/forward/track-devices/reconnect/server-status）和 `client/adb_client.cpp`（_adb_connect），本轮只读取源码；未核对所有老版本。参考地址：`https://android.googlesource.com/platform/packages/modules/adb/+/refs/heads/main/client/`。
- 延续黑名单策略，没有改成设备命令白名单；新添子命令或另一种协议直通入口仍需审查。设备 `shell` 的内容不在此层分析。
- 前缀解析沿用初版：分离值的 `-s/-H/-P/-L/-t`、ADB 支持的紧贴取值形式、连续 wait；遇到设备子命令后停止解析。只允许匹配当前 USB/TCP 资源的显式 `-s`；其他 server/transport 重定向仍拒绝。多层 wait 被保守扫描，不保证底层各版本都支持执行。
- 未运行全仓或 httpapi 全包测试；未合并、推送、部署或操作硬件。评审包大小限制仍为 64 KiB。

## 本轮新增 diff（08c7dfe → r2，零行上下文）

~~~~diff
diff --git a/internal/httpapi/agent_handlers.go b/internal/httpapi/agent_handlers.go
index 3a42768..9ae9799 100644
--- a/internal/httpapi/agent_handlers.go
+++ b/internal/httpapi/agent_handlers.go
@@ -99,0 +100,4 @@ var blockedADBHostCommands = map[string]bool{
+	"track-devices": true, "server-status": true, "reconnect": true,
+	// raw forwards arbitrary ADB services, including host services that bypass
+	// the selected device and services such as tcpip otherwise blocked by name.
+	"raw": true,
@@ -195 +199 @@ func (s *Server) agentADBExec(writer http.ResponseWriter, request *http.Request)
-	if len(payload.Args) > 64 {
+	if len(payload.Args) == 0 || len(payload.Args) > 64 {
diff --git a/internal/httpapi/agent_handlers_test.go b/internal/httpapi/agent_handlers_test.go
index 9cf1b47..6fa9737 100644
--- a/internal/httpapi/agent_handlers_test.go
+++ b/internal/httpapi/agent_handlers_test.go
@@ -261,0 +262,6 @@ func TestAgentADBExecParsesCommandBeforeApplyingPolicy(t *testing.T) {
+		target := group.Network.ADBSerial
+		if group.ID == "tcp" {
+			target = "192.0.2.10:5555"
+		} else if group.ID == "wireless" {
+			target = "192.0.2.11:41234"
+		}
@@ -270,0 +277,3 @@ func TestAgentADBExecParsesCommandBeforeApplyingPolicy(t *testing.T) {
+			{"shell_raw_argument_is_not_command", []string{"shell", "echo", "raw", "host:devices"}, true},
+			{"forward_list_remains_available", []string{"forward", "--list"}, true},
+			{"reverse_list_remains_available", []string{"reverse", "--list"}, true},
@@ -282,0 +292,7 @@ func TestAgentADBExecParsesCommandBeforeApplyingPolicy(t *testing.T) {
+			{"direct_uppercase_tcpip", []string{"TCPIP", "5555"}, false},
+			{"direct_raw_tcpip", []string{"raw", "tcpip:5555"}, false},
+			{"direct_raw_host_devices", []string{"raw", "host:devices"}, false},
+			{"direct_uppercase_raw", []string{"RAW", "host:devices"}, false},
+			{"direct_track_devices", []string{"track-devices"}, false},
+			{"direct_server_status", []string{"server-status"}, false},
+			{"direct_reconnect", []string{"reconnect", "offline"}, false},
@@ -292,0 +309,3 @@ func TestAgentADBExecParsesCommandBeforeApplyingPolicy(t *testing.T) {
+			{"wait_raw_host_kill", []string{"wait-for-device", "raw", "host:kill"}, false},
+			{"serial_raw_tcpip", []string{"-s", target, "raw", "tcpip:5555"}, false},
+			{"host_port_raw_host_devices", []string{"-H", "127.0.0.1", "-P", "5037", "raw", "host:devices"}, false},
@@ -335,0 +355,4 @@ func TestAgentADBExecParsesCommandBeforeApplyingPolicy(t *testing.T) {
+					wantError, wantMessage := "adb_command_not_allowed", "这里只允许面向当前设备的 ADB 子命令"
+					if len(tc.args) == 0 {
+						wantError, wantMessage = "adb_args_invalid", "ADB 参数数量必须在 1 到 64 之间"
+					}
@@ -375 +398 @@ func TestAgentADBExecParsesCommandBeforeApplyingPolicy(t *testing.T) {
-					} else if status != http.StatusBadRequest || payload.OK || payload.Error != "adb_command_not_allowed" || executions.Load() != before {
+					} else if status != http.StatusBadRequest || payload.OK || payload.Error != wantError || executions.Load() != before {
@@ -377 +400 @@ func TestAgentADBExecParsesCommandBeforeApplyingPolicy(t *testing.T) {
-					} else if payload.Message != "这里只允许面向当前设备的 ADB 子命令" {
+					} else if payload.Message != wantMessage {
~~~~

## 累计完整产品与测试 diff（d7cee5b → r2，零行上下文）

~~~~diff
diff --git a/internal/httpapi/agent_handlers.go b/internal/httpapi/agent_handlers.go
index cf5587d..9ae9799 100644
--- a/internal/httpapi/agent_handlers.go
+++ b/internal/httpapi/agent_handlers.go
@@ -99,0 +100,6 @@ var blockedADBHostCommands = map[string]bool{
+	"track-devices": true, "server-status": true, "reconnect": true,
+	// raw forwards arbitrary ADB services, including host services that bypass
+	// the selected device and services such as tcpip otherwise blocked by name.
+	"raw": true,
+	// These are also parsed as server-mode modifiers before device commands.
+	"server": true, "nodaemon": true, "fork-server": true,
@@ -104,0 +111,63 @@ var blockedADBHostCommands = map[string]bool{
+type adbCommand struct {
+	name              string
+	serial            string
+	unsafeGlobalFlags bool
+}
+
+// parseADBCommand only parses the global prefix. Once a device command is
+// found, its arguments belong to that command (including options and names
+// such as "tcpip" passed to shell). Unknown or incomplete prefixes fail closed.
+func parseADBCommand(args []string) (adbCommand, bool) {
+	var command adbCommand
+	i := 0
+	for i < len(args) && strings.HasPrefix(args[i], "-") {
+		option, value := args[i], ""
+		switch option {
+		case "-s", "-t", "-H", "-P", "-L":
+			i++
+			if i == len(args) {
+				return adbCommand{}, false
+			}
+			value = args[i]
+		case "-a", "-d", "-e", "--exit-on-write-error":
+		default:
+			// adb accepts attached -H/-P values, and attached -s/-t values
+			// only when their first character is a digit. -L needs a token.
+			if len(option) <= 2 {
+				return adbCommand{}, false
+			}
+			prefix := option[:2]
+			if prefix != "-H" && prefix != "-P" &&
+				!((prefix == "-s" || prefix == "-t") && option[2] >= '0' && option[2] <= '9') {
+				return adbCommand{}, false
+			}
+			option, value = prefix, option[2:]
+		}
+		switch option {
+		case "-s", "-t", "-H", "-P", "-L":
+			if value == "" || strings.HasPrefix(value, "-") {
+				return adbCommand{}, false
+			}
+		}
+		if option == "-s" {
+			command.serial = value // Like adb, the last -s wins.
+		} else if option != "--exit-on-write-error" {
+			// Recognizing an option must not grant access to another device,
+			// transport ID, or ADB server. Keep the existing resource boundary.
+			command.unsafeGlobalFlags = true
+		}
+		i++
+	}
+	for i < len(args) && strings.HasPrefix(args[i], "wait-for-") {
+		if args[i] == "wait-for-" {
+			return adbCommand{}, false
+		}
+		command.name = args[i]
+		i++
+	}
+	if i < len(args) {
+		command.name = args[i]
+	}
+	return command, command.name != "" && !strings.HasPrefix(command.name, "-")
+}
+
@@ -134 +203,9 @@ func (s *Server) agentADBExec(writer http.ResponseWriter, request *http.Request)
-	if strings.HasPrefix(payload.Args[0], "-") || blockedADBHostCommands[strings.ToLower(payload.Args[0])] {
+	command, parsed := parseADBCommand(payload.Args)
+	target := net.JoinHostPort(group.Network.Host, strconv.Itoa(group.Network.Port))
+	if group.Network.ADBUSB() {
+		target = group.Network.ADBSerial
+	}
+	// Wireless debugging resolves its serial via mDNS at execution time, so a
+	// caller-supplied serial cannot be verified against that resource here.
+	if !parsed || blockedADBHostCommands[strings.ToLower(command.name)] || command.unsafeGlobalFlags ||
+		(command.serial != "" && (group.Network.ADBWireless() || command.serial != target)) {
diff --git a/internal/httpapi/agent_handlers_test.go b/internal/httpapi/agent_handlers_test.go
index 71edfb3..6fa9737 100644
--- a/internal/httpapi/agent_handlers_test.go
+++ b/internal/httpapi/agent_handlers_test.go
@@ -12,0 +13,2 @@ import (
+	"reflect"
+	"slices"
@@ -14,0 +17 @@ import (
+	"sync/atomic"
@@ -191,0 +195,214 @@ func TestAgentADBExecUsesTaskDeviceAndReturnsBinary(t *testing.T) {
+func TestParseADBCommandSkipsGlobalOptionsAndWaitPrefixes(t *testing.T) {
+	for _, tc := range []struct {
+		name   string
+		args   []string
+		want   adbCommand
+		parsed bool
+	}{
+		{"serial", []string{"-s", "SERIAL", "shell"}, adbCommand{name: "shell", serial: "SERIAL"}, true},
+		{"serial_value_is_not_command", []string{"-s", "tcpip", "shell"}, adbCommand{name: "shell", serial: "tcpip"}, true},
+		{"host_port", []string{"-H", "host", "-P", "5037", "host-features"}, adbCommand{name: "host-features", unsafeGlobalFlags: true}, true},
+		{"socket", []string{"-L", "tcp:localhost:5037", "shell"}, adbCommand{name: "shell", unsafeGlobalFlags: true}, true},
+		{"transport", []string{"-t", "1", "wait-for-device", "tcpip"}, adbCommand{name: "tcpip", unsafeGlobalFlags: true}, true},
+		{"attached_host_port", []string{"-Hhost", "-P5037", "wait-for-device", "host-features"}, adbCommand{name: "host-features", unsafeGlobalFlags: true}, true},
+		{"attached_transport", []string{"-t1", "shell"}, adbCommand{name: "shell", unsafeGlobalFlags: true}, true},
+		{"attached_numeric_serial", []string{"-s123ABC", "shell"}, adbCommand{name: "shell", serial: "123ABC"}, true},
+		{"attached_nonnumeric_serial", []string{"-sSERIAL", "shell"}, adbCommand{}, false},
+		{"attached_socket_is_unknown", []string{"-Ltcp:5037", "shell"}, adbCommand{}, false},
+		{"multiple_flags_waits", []string{"-d", "-a", "--exit-on-write-error", "-s", "SERIAL", "wait-for-device", "wait-for-usb-device", "install", "-r", "app.apk"}, adbCommand{name: "install", serial: "SERIAL", unsafeGlobalFlags: true}, true},
+		{"last_serial_wins", []string{"-s", "OTHER", "-s", "SERIAL", "shell"}, adbCommand{name: "shell", serial: "SERIAL"}, true},
+		{"only_wait", []string{"wait-for-device"}, adbCommand{name: "wait-for-device"}, true},
+		{"options_only", []string{"-H", "host", "-P", "5037"}, adbCommand{unsafeGlobalFlags: true}, false},
+		{"missing_value", []string{"-s"}, adbCommand{}, false},
+		{"unknown", []string{"--unknown", "tcpip"}, adbCommand{}, false},
+		{"empty", nil, adbCommand{}, false},
+	} {
+		t.Run(tc.name, func(t *testing.T) {
+			got, parsed := parseADBCommand(tc.args)
+			if got != tc.want || parsed != tc.parsed {
+				t.Fatalf("parseADBCommand(%q)=(%#v, %v), want (%#v, %v)", tc.args, got, parsed, tc.want, tc.parsed)
+			}
+		})
+	}
+}
+
+func TestAgentADBExecParsesCommandBeforeApplyingPolicy(t *testing.T) {
+	h := newCoreMCPHarness(t)
+	task := createHardwareAPITask(t, h.database)
+	now := time.Now().UTC()
+	groups := []domain.HardwareGroup{
+		{ID: "usb", Network: domain.HardwareNetworkConfig{ADBConnection: "usb", ADBSerial: "123SERIAL"}},
+		{ID: "tcp", Network: domain.HardwareNetworkConfig{ADBConnection: "tcp", Host: "192.0.2.10", Port: 5555}},
+		{ID: "wireless", Network: domain.HardwareNetworkConfig{Host: "192.0.2.11", Port: 41234}},
+	}
+	for i := range groups {
+		groups[i].Mode, groups[i].Access = domain.HardwareModeNetwork, domain.HardwareAccessReadWrite
+		groups[i].Network.Protocol = domain.HardwareProtocolADB
+		groups[i].CreatedAt, groups[i].UpdatedAt = now, now
+	}
+	if err := h.database.ReplaceHardwareGroups(h.ctx, task.ID, groups); err != nil {
+		t.Fatal(err)
+	}
+	var executions atomic.Int32
+	forwarded := make(chan []string, 1)
+	executor := func(_ context.Context, _ string, args []string, _ []byte, _ time.Duration) (hardware.ADBExecResult, error) {
+		executions.Add(1)
+		forwarded <- append([]string(nil), args...)
+		return hardware.ADBExecResult{Stdout: []byte("fixture")}, nil
+	}
+	server := httptest.NewServer(New(Config{
+		Store: h.database, App: h.service, AgentCapabilities: h.caps,
+		ExecuteADB: executor, ExecuteADBTCP: executor, ExecuteADBUSB: executor,
+	}).Handler())
+	defer server.Close()
+	turn := createCoreTurn(t, h.database, task, "main", "batch-adb-command-policy")
+	token := h.issue(task.ID, "main", turn.ID)
+
+	for _, group := range groups {
+		target := group.Network.ADBSerial
+		if group.ID == "tcp" {
+			target = "192.0.2.10:5555"
+		} else if group.ID == "wireless" {
+			target = "192.0.2.11:41234"
+		}
+		for _, tc := range []struct {
+			name    string
+			args    []string
+			allowed bool
+		}{
+			{"shell", []string{"shell", "id"}, true},
+			{"logcat", []string{"logcat", "-d"}, true},
+			{"install", []string{"install", "-r", "app.apk"}, true},
+			{"shell_arguments_are_not_commands", []string{"shell", "echo", "tcpip", "-s"}, true},
+			{"shell_raw_argument_is_not_command", []string{"shell", "echo", "raw", "host:devices"}, true},
+			{"forward_list_remains_available", []string{"forward", "--list"}, true},
+			{"reverse_list_remains_available", []string{"reverse", "--list"}, true},
+			{"wait_only", []string{"wait-for-device"}, true},
+			{"wait_shell", []string{"wait-for-device", "shell"}, true},
+			{"multiple_waits_shell", []string{"wait-for-device", "wait-for-usb-device", "shell"}, true},
+			{"multiple_waits_only", []string{"wait-for-device", "wait-for-disconnect"}, true},
+			{"exit_on_write_error", []string{"--exit-on-write-error", "shell"}, true},
+			{"serial_shell", []string{"-s", "123SERIAL", "shell"}, group.ID == "usb"},
+			{"attached_serial_shell", []string{"-s123SERIAL", "shell"}, group.ID == "usb"},
+			{"serial_wait_shell", []string{"-s", "123SERIAL", "wait-for-device", "shell"}, group.ID == "usb"},
+			{"tcp_serial_shell", []string{"-s", "192.0.2.10:5555", "shell"}, group.ID == "tcp"},
+			{"wireless_serial_cannot_be_verified", []string{"-s", "192.0.2.11:41234", "shell"}, false},
+			// N-a: direct blocked commands must stay blocked.
+			{"direct_tcpip", []string{"tcpip", "5555"}, false},
+			{"direct_uppercase_tcpip", []string{"TCPIP", "5555"}, false},
+			{"direct_raw_tcpip", []string{"raw", "tcpip:5555"}, false},
+			{"direct_raw_host_devices", []string{"raw", "host:devices"}, false},
+			{"direct_uppercase_raw", []string{"RAW", "host:devices"}, false},
+			{"direct_track_devices", []string{"track-devices"}, false},
+			{"direct_server_status", []string{"server-status"}, false},
+			{"direct_reconnect", []string{"reconnect", "offline"}, false},
+			{"direct_host_features", []string{"host-features"}, false},
+			{"direct_devices", []string{"devices"}, false},
+			{"direct_connect", []string{"connect", "192.0.2.99:5555"}, false},
+			{"direct_disconnect", []string{"disconnect"}, false},
+			{"direct_pair", []string{"pair", "192.0.2.99:37000"}, false},
+			{"direct_start_server", []string{"start-server"}, false},
+			{"direct_kill_server", []string{"kill-server"}, false},
+			{"direct_mdns", []string{"mdns", "services"}, false},
+			// N-b: prefixes must not conceal a blocked command.
+			{"wait_tcpip", []string{"wait-for-device", "tcpip", "5555"}, false},
+			{"wait_raw_host_kill", []string{"wait-for-device", "raw", "host:kill"}, false},
+			{"serial_raw_tcpip", []string{"-s", target, "raw", "tcpip:5555"}, false},
+			{"host_port_raw_host_devices", []string{"-H", "127.0.0.1", "-P", "5037", "raw", "host:devices"}, false},
+			{"serial_tcpip", []string{"-s", "123SERIAL", "tcpip", "5555"}, false},
+			{"host_port_host_features", []string{"-H", "127.0.0.1", "-P", "5037", "host-features"}, false},
+			{"wait_host_features", []string{"wait-for-device", "host-features"}, false},
+			{"multiple_waits_tcpip", []string{"wait-for-device", "wait-for-any-device", "tcpip", "5555"}, false},
+			{"serial_multiple_waits_host_features", []string{"-s", "123SERIAL", "wait-for-device", "wait-for-local-device", "host-features"}, false},
+			{"serial_wait_uppercase_tcpip", []string{"-s", "123SERIAL", "wait-for-device", "TCPIP", "5555"}, false},
+			{"socket_wait_tcpip", []string{"-L", "tcp:localhost:5037", "wait-for-device", "tcpip", "5555"}, false},
+			{"transport_wait_tcpip", []string{"-t", "1", "wait-for-device", "tcpip", "5555"}, false},
+			{"server_mode", []string{"server", "nodaemon"}, false},
+			{"nodaemon_prefix", []string{"nodaemon", "tcpip", "5555"}, false},
+			{"fork_server_prefix", []string{"fork-server", "tcpip", "5555"}, false},
+			{"empty", []string{}, false},
+			{"null", nil, false},
+			{"empty_command", []string{""}, false},
+			{"serial_only", []string{"-s", "123SERIAL"}, false},
+			{"unknown_option", []string{"--unknown", "shell"}, false},
+			{"missing_serial", []string{"-s"}, false},
+			{"missing_host", []string{"-H"}, false},
+			{"missing_port", []string{"-P"}, false},
+			{"missing_socket", []string{"-L"}, false},
+			{"missing_transport", []string{"-t"}, false},
+			{"empty_option_value", []string{"-s", "", "shell"}, false},
+			{"option_instead_of_value", []string{"-s", "-H", "shell"}, false},
+			{"option_after_wait", []string{"wait-for-device", "-s", "123SERIAL", "shell"}, false},
+			{"empty_wait_suffix", []string{"wait-for-"}, false},
+			{"host_override_shell", []string{"-H", "127.0.0.1", "shell"}, false},
+			{"port_override_shell", []string{"-P", "5037", "shell"}, false},
+			{"socket_override_shell", []string{"-L", "tcp:localhost:5037", "shell"}, false},
+			{"transport_override_shell", []string{"-t", "1", "shell"}, false},
+			{"other_serial_shell", []string{"-s", "OTHER", "shell"}, false},
+			{"usb_selector_shell", []string{"-d", "shell"}, false},
+			{"emulator_selector_shell", []string{"-e", "shell"}, false},
+			{"listen_all_shell", []string{"-a", "shell"}, false},
+			{"server_only_option", []string{"--one-device", "123SERIAL", "shell"}, false},
+		} {
+			t.Run(group.ID+"/"+tc.name, func(t *testing.T) {
+				for _, mcp := range []bool{false, true} {
+					// The MCP schema rejects empty arrays/nulls and empty strings
+					// before the handler. Exercise those parser boundaries via HTTP.
+					if mcp && (len(tc.args) == 0 || slices.Contains(tc.args, "")) {
+						continue
+					}
+					before := executions.Load()
+					wantError, wantMessage := "adb_command_not_allowed", "这里只允许面向当前设备的 ADB 子命令"
+					if len(tc.args) == 0 {
+						wantError, wantMessage = "adb_args_invalid", "ADB 参数数量必须在 1 到 64 之间"
+					}
+					var status int
+					var payload struct {
+						OK      bool   `json:"ok"`
+						Error   string `json:"error"`
+						Message string `json:"message"`
+					}
+					if mcp {
+						result := coreMCPCallTool(t, server.URL+"/api/v1/agent/hardware/mcp", token, "hardware_adb_exec", map[string]any{"hardware_id": group.ID, "args": tc.args})
+						status = result.Status
+						if !tc.allowed {
+							select {
+							case <-forwarded:
+							default:
+							}
+							if status != http.StatusOK || !result.IsError || result.Text != "adb_command_not_allowed: adb_command_not_allowed" || executions.Load() != before {
+								t.Fatalf("MCP status=%d isError=%v text=%q executions=%d", status, result.IsError, result.Text, executions.Load()-before)
+							}
+							continue
+						}
+						if err := json.Unmarshal([]byte(result.Text), &payload); err != nil {
+							t.Fatal(err)
+						}
+					} else {
+						response := agentRequest(t, server.URL+"/api/v1/agent/hardware/"+group.ID+"/adb/exec", http.MethodPost, token, map[string]any{"args": tc.args})
+						status = response.StatusCode
+						decodeResponse(t, response, &payload)
+					}
+					var got []string
+					select {
+					case got = <-forwarded:
+					default:
+					}
+					if tc.allowed {
+						if status != http.StatusOK || !payload.OK || executions.Load() != before+1 {
+							t.Fatalf("mcp=%v status=%d ok=%v executions=%d", mcp, status, payload.OK, executions.Load()-before)
+						}
+						if !reflect.DeepEqual(got, tc.args) {
+							t.Fatalf("forwarded args=%q want=%q", got, tc.args)
+						}
+					} else if status != http.StatusBadRequest || payload.OK || payload.Error != wantError || executions.Load() != before {
+						t.Fatalf("mcp=%v status=%d error=%q executions=%d", mcp, status, payload.Error, executions.Load()-before)
+					} else if payload.Message != wantMessage {
+						t.Fatalf("mcp=%v unexpected public message %q", mcp, payload.Message)
+					}
+				}
+			})
+		}
+	}
+}
+
~~~~
