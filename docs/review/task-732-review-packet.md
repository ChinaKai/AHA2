# task-732 review packet：解析 ADB argv 后再拦截子命令

基线：`d7cee5b`。实现提交：`dc6eba37d243f79ba57cae73dece283059f101f5`。分支：`aha/task_f707a30188b507742e3a`。

在独立 worktree 中修复。未合并 main、未 push、未部署，未操作硬件。以下给出完整产品与测试 diff，评审无需另取附件。

## 问题与改动

旧逻辑只检查 `args[0]`，`wait-for-device tcpip 5555` 可绕过黑名单。新逻辑先解析全局选项，再跳过任意数量的 `wait-for-*`，对第一个实际子命令应用原有大小写不敏感的黑名单。到达设备子命令后停止解析，`shell echo tcpip -s`、`logcat -d`、`install -r app.apk` 等内部参数原样透传。

改动文件：

- `internal/httpapi/agent_handlers.go`：增加 argv 解析；保留绑定设备边界；空 args 改为统一返回 `adb_command_not_allowed`；补拦 ADB 的 `server`、`nodaemon`、`fork-server` 前置模式。
- `internal/httpapi/agent_handlers_test.go`：17 个解析用例；58 组 HTTP 表驱动用例各在 USB、网络直连、无线调试三种模式运行（174 个子用例），其中符合现有 MCP schema 的输入再走 MCP。拒绝时执行器调用次数必须不变，成功时只能调用一次且 argv 逐项不变。已有测试和断言全部保留。
- `docs/review/task-732-review-packet.md`：本评审包。

产品/测试增量：`+269/-2`，只有上述两个 Go 文件；加上本评审包共三个文件。

## 设计取舍

1. 全局语法：支持分离取值的 `-s`、`-t`、`-H`、`-P`、`-L`，以及 `-a`、`-d`、`-e`、`--exit-on-write-error`。按 ADB 的实际语法，`-H`/`-P` 可紧贴取值，`-s`/`-t` 的紧贴取值须以数字开头，`-L` 必须单独取值。未知选项、空/缺取值、只有全局选项、空命令都拒绝。选项名称大小写敏感，子命令黑名单继续大小写不敏感。
2. 解析全局选项不等于授权重定向。执行器会把用户 argv 原样接在绑定目标 `-s` 后面；直接放行任意 `-s`/`-t` 或 server 选项会越过资源边界。保留现有 `-s SERIAL-B shell` 拒绝用例；显式 `-s` 只允许等于当前 USB serial 或网络直连 host:port，重复 `-s` 按 ADB 语义取最后一个。无线调试通过 mDNS 才能确定实际 serial，因此仍拒绝显式 `-s`。`-t/-H/-P/-L/-a/-d/-e` 虽被解析，仍不授予执行权限；`--exit-on-write-error` 可用。
3. `--one-device`、`--reply-fd` 属于 server 启动用途，按未开放选项拒绝；`server`、`nodaemon`、`fork-server` 本身会触发/修饰 ADB server 模式，直接拒绝，避免把它们当普通设备命令。
4. 全局选项只在实际 ADB 的命令前缀位置解析；`wait-for-device -s SERIAL shell` 拒绝。允许独立 wait；连续 wait 前缀一律扫描到底，避免黑名单被第二层 wait 隐藏；不重写或拆成多次执行。
5. HTTP 拒绝统一用 400、`ok=false`、`adb_command_not_allowed` 及原有固定提示。MCP 继续沿用其现有 HTTP 200 / `isError=true` / 固定分类文本封装；空数组、null、空字符串由既有 MCP schema 先行拒绝，这些输入通过 HTTP 验证解析器边界。参数长度、stdin、超时等原有限制不变。

语法核对依据：仓库 `internal/hardware/adb.go:runADBDeviceCommand` 与 `internal/hardware/adb_usb.go` 的实际透传行为，以及 AOSP ADB `client/commandline.cpp` 的全局参数分支（本轮读取 https://android.googlesource.com/platform/packages/modules/adb/+/refs/heads/main/client/commandline.cpp ，未执行 adb）。

## 验证

在本 worktree 执行，Go `go1.27.4 linux/amd64`。`TMPDIR`、`GOTMPDIR` 都指向本 Task 的 scratch；所有测试使用假执行器或既有假 adb，未连接硬件。

| 命令 | 退出码 | 结果 |
| --- | --- | --- |
| `go test ./internal/httpapi -run 'ADB\|Adb\|adb' -count=1 -timeout=10m` | 0 | 通过，`ok github.com/ChinaKai/AHA2/internal/httpapi 293.902s` |
| `go vet ./internal/httpapi` | 0 | 通过，无诊断 |
| `go test ./internal/httpapi -run '^(TestParseADBCommandSkipsGlobalOptionsAndWaitPrefixes\|TestAgentADBExecParsesCommandBeforeApplyingPolicy)$' -v -count=1 -timeout=2m` | 0 | 新增用例单独复核通过，45.462s |
| `git diff --check` | 0 | 通过 |

针对缺陷的反证：用 `go test -overlay <scratch>/baseline-overlay.json ./internal/httpapi -run '^TestAgentADBExecParsesCommandBeforeApplyingPolicy/usb/(wait_tcpip|wait_host_features|multiple_waits_tcpip)$' -count=1 -timeout=10m` 保留基线原有拦截分支（新解析类型/函数仅为新增测试编译而存在，未被旧分支调用），得到退出码 1，三个用例均因 HTTP 200 且执行器调用一次而失败。这证明回归用例能检出原绕过，而非仅覆盖新的实现。

```text
--- FAIL: TestAgentADBExecParsesCommandBeforeApplyingPolicy/usb/wait_tcpip
    mcp=false status=200 error="" executions=1
--- FAIL: TestAgentADBExecParsesCommandBeforeApplyingPolicy/usb/wait_host_features
    mcp=false status=200 error="" executions=1
--- FAIL: TestAgentADBExecParsesCommandBeforeApplyingPolicy/usb/multiple_waits_tcpip
    mcp=false status=200 error="" executions=1
```

## 残余风险与范围

- 按要求未跑全仓或 httpapi 全包测试，未实机验证平台/ADB 版本差异。
- 延续黑名单策略，只加固命令前缀识别与资源重定向边界；没有改成完整设备命令白名单，也不分析设备 shell 的内容。之后增加全局语法或 host 命令时仍需同步更新策略与测试。
- 解析器对多层 wait 做保守检查，不保证所有 ADB 版本都能执行多层 wait；实际 argv 不变，底层 ADB 仍可能返回语法错误。单独 `wait-for-device` 和其后的合法设备命令可用。
- 与目标不一致的 selector、无法确认目标的 transport ID、无线调试显式 serial 及 server 重定向继续拒绝；这是保留资源边界的有意限制。

## 完整产品与测试 diff（相对 d7cee5b，零行上下文）

~~~~diff
diff --git a/internal/httpapi/agent_handlers.go b/internal/httpapi/agent_handlers.go
index cf5587d..3a42768 100644
--- a/internal/httpapi/agent_handlers.go
+++ b/internal/httpapi/agent_handlers.go
@@ -99,0 +100,2 @@ var blockedADBHostCommands = map[string]bool{
+	// These are also parsed as server-mode modifiers before device commands.
+	"server": true, "nodaemon": true, "fork-server": true,
@@ -104,0 +107,63 @@ var blockedADBHostCommands = map[string]bool{
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
@@ -130 +195 @@ func (s *Server) agentADBExec(writer http.ResponseWriter, request *http.Request)
-	if len(payload.Args) == 0 || len(payload.Args) > 64 {
+	if len(payload.Args) > 64 {
@@ -134 +199,9 @@ func (s *Server) agentADBExec(writer http.ResponseWriter, request *http.Request)
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
index 71edfb3..9cf1b47 100644
--- a/internal/httpapi/agent_handlers_test.go
+++ b/internal/httpapi/agent_handlers_test.go
@@ -12,0 +13,2 @@ import (
+	"reflect"
+	"slices"
@@ -14,0 +17 @@ import (
+	"sync/atomic"
@@ -191,0 +195,191 @@ func TestAgentADBExecUsesTaskDeviceAndReturnsBinary(t *testing.T) {
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
+		for _, tc := range []struct {
+			name    string
+			args    []string
+			allowed bool
+		}{
+			{"shell", []string{"shell", "id"}, true},
+			{"logcat", []string{"logcat", "-d"}, true},
+			{"install", []string{"install", "-r", "app.apk"}, true},
+			{"shell_arguments_are_not_commands", []string{"shell", "echo", "tcpip", "-s"}, true},
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
+					} else if status != http.StatusBadRequest || payload.OK || payload.Error != "adb_command_not_allowed" || executions.Load() != before {
+						t.Fatalf("mcp=%v status=%d error=%q executions=%d", mcp, status, payload.Error, executions.Load()-before)
+					} else if payload.Message != "这里只允许面向当前设备的 ADB 子命令" {
+						t.Fatalf("mcp=%v unexpected public message %q", mcp, payload.Message)
+					}
+				}
+			})
+		}
+	}
+}
+
~~~~
