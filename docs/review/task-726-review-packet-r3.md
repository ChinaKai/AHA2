# task-726 review packet r3：task-727 复审意见修订（M1 + S1–S4）

- 上一版：实现 336d47b，材料 59246d3（r2，评审结论 REQUEST_CHANGES）
- 本版：实现提交 c85a80f（c3），分支 aha/task_bf0fb2bc637d58c78441，worktree .aha2-worktrees/task_bf0fb2bc637d58c78441。没有 push，没有合并，没有接真实设备
- 本包是一个文件，不超过 65536 字节，不拆分卷。文末的 delta 是 `git diff 336d47b c85a80f -- . ':!docs/review'` 的原样输出，共 10 个文件，sha256 也在文末。之所以去掉 docs/review：336d47b 和 c85a80f 之间隔着 r2 的材料提交 59246d3，它只改 docs/review。去掉之后，这段 delta 和 `git diff 59246d3 c85a80f` 逐字节相同（已用 cmp 核对）。还原方法：`git show <材料提交>:docs/review/task-726-review-packet-r3.md`，取出 `~~~~diff` 与 `~~~~` 之间的内容即可。
- 只改了评审点涉及的代码和测试，没有重构，也没碰 task-722 的文件。agent_handlers.go 只在 blockedADBHostCommands 里加了一项。

## 逐条处理

### M1（必须修）：USB 路径把 adb 启动横幅当成设备状态
- 原因：`adbUSBDeviceState` 用 `CombinedOutput` 读 get-state。adb 服务冷启动时，stderr 先打出 `* daemon not running; starting now…`，这段横幅和 stdout 里的 `device` 混在一起，被截成 32 字节当作状态，于是报“状态异常”。
- 修法：新增 `runADBQuery`，把 stdout 和 stderr 分开收集；新增 `adbResultLine`，取最后一个非空、且不以 `*` 开头的行。退出码为 0 时只从 stdout 读状态。非 0 时，分类仍然看 stdout 加 stderr 里的关键字（横幅里没有这些关键字），报错文案只用 stderr（或 stdout）里最后一条非横幅行。
- 回归测试 `TestADBUSBIgnoresDaemonBannerOnColdStart`：假 adb 在 get-state 和 get-serialno 时，都先往 stderr 打两行横幅，再从 stdout 输出结果，退出码 0。断言执行和终端都能正常继续，而且没有 connect、pair、mdns、kill-server。
- 变异验证：把读取改回“stderr 和 stdout 混在一起取前 32 字节”，这个测试失败，报错正是“USB 设备 SER1 状态异常：* daemon not running; starting n”，和评审描述的现象一致。

### S1：`TestPetTakeoverKeepsADBConnectionForms` 断言太松
- 现在分两层断言：
  - 不给本机 serial：必须由 pet_takeover 层的守卫拒绝。断言的是守卫自己的完整文案“必须重新提供本机串口、网络端点或 USB 设备序列号”，NormalizeGroup 的“缺少 USB 设备序列号，…”匹配不上。
  - serial 是 IP 字面量：由 NormalizeGroup 层拒绝，断言“USB 设备序列号无效”。
- 变异验证：去掉 pet_takeover.go 里的 USB 序列号守卫，测试失败。
- HTTP 接管测试本来就靠错误码区分两层：守卫返回 `local_hardware_fields_required`，校验失败返回 `local_hardware_invalid`。

### S2：`ValidADBUSBSerial` 放行 IP 字面量和带点的主机名；运行时再核对序列号
- `domain.ValidADBUSBSerial` 新增三条拒绝规则：含 `.`、`localhost`（不区分大小写）、`net.ParseIP` 能解析的值。理由：`adb -s` 遇到裸主机名时，可能落到已连接的 `host:5555` 传输上。结果是 USB 资源实际操作的是网络设备，还绕开了同设备会话的互斥。限制：真实 USB 序列号里如果带点，现在也会被拒绝；常见的 USB 序列号里没有点。
- 运行时核对：`ensureADBUSBDevice` 在 get-state 返回 device 之后，调用 `verifyADBUSBSerial` 执行 `adb -s <serial> get-serialno`，同样只读 stdout、跳过横幅，结果必须和配置的 serial 完全相等，否则报“USB 设备序列号不一致”，命令不会执行。执行和终端都走 `ensureADBUSBDevice`。
- 测试：
  - 单元测试：hardwareconfig 的非法 serial 列表加了 `192.0.2.4`、`localhost`、`LocalHost`、`tv.lan`、`10.0.0.1`；hardware 层的列表加了 `192.0.2.4`、`localhost`、`tv.lan`，并断言这些值根本不会调用 adb。
  - 集成测试 `TestADBUSBRefusesWhenResolvedSerialDiffers`：假 adb 的 get-state 返回 device，get-serialno 返回 `192.0.2.4:5555`。断言执行和终端都被拒绝，日志里没有任何 shell 调用。
  - 原有 USB 测试的假 adb 加了 get-serialno 响应。
- 接管测试里也加了 `192.0.2.4` 和 `tv.lan` 两个拒绝用例。
- 变异验证：去掉 `verifyADBUSBSerial` 调用，集成测试失败。

### S3：Agent 可以对读写 ADB 资源执行 `tcpip`
- 包里已有现成的黑名单 `blockedADBHostCommands`（Agent `adb/exec` 入口，比较时不区分大小写），这次把 `tcpip` 加了进去，没有新造机制。`root`、`reboot`、`forward`、`reverse` 没动。
- 测试：`TestAgentADBExecRoutesEachConnectionFormToTheBoundTarget` 新增三条用例：board（USB）`tcpip 5555`、legacy（无线）`TCPIP 5555`、tv（网络直连）`tcpip 5601`。都返回 400，而且都不会调用执行器。
- 变异验证：从黑名单里删掉 tcpip，测试失败。
- 说明：交互式终端走的是 `adb -s X shell -t -t`，在设备 shell 里没有 adb 的 `tcpip` 子命令，所以这个入口不受影响。如果设备上的 root shell 自己用 setprop 改 adbd 端口，那属于设备侧行为，不在本次范围。

### S4：把评审探针发现的三处测试缺口收成正式测试
1. 真实 v155 库再迁移，`TestMigrationV156UpgradesV155DatabaseWithLegacyADBRow`（internal/store）：
   - 先建库，再执行 `ALTER TABLE … DROP COLUMN adb_serial/adb_connection` 和 `DELETE FROM schema_migrations WHERE version=156`，把库退回 v155 的表结构，并确认两列已经不存在。
   - 用 v155 的列清单插入一条已配对的无线手机，然后重新打开库触发迁移。
   - 断言：两列被加回来；v156 只记录一次；库里的 `adb_connection` 仍是 `''`，没有被改写；读出来是 wireless，host、port、adb_paired_at 都不变。
   - 说明：这个库是用当前代码建好后退回 v155 结构的，不是拿 main 43624a4 的二进制建出来的。v156 只做 ADD COLUMN，所以 v155 和 v156 的差别只有这两列加一条迁移记录。
2. 编辑或删除 USB 资源会断开已有终端，`TestEditingOrDeletingUSBResourceDisconnectsOpenTerminal`（internal/httpapi，`//go:build !windows`）：
   - 用真实的 hardware.Manager，`AHA2_ADB_PATH` 指向假 adb 脚本。
   - 完整走 HTTP：创建 USB 资源 → 绑定到 Task → connect（endpoint 是 `USB SER1`）→ PUT 把 serial 改成 SER2 → 断言终端已断开 → 重新 connect（`USB SER2`）→ DELETE → 断言终端已断开。
   - 变异验证：把 `HardwareResource.Supports` 改回只看 host，测试在“editing the USB resource left the USB terminal connected”处失败。
3. 接管被拒绝时不留半截数据：
   - HTTP 接管和宠物接管两条路径，都在每次拒绝前后对 `tasks`、`hardware_resources`、`task_hardware_bindings`、`runtime_config_snapshots` 四张表计数，要求完全相等。
   - 覆盖两类拒绝：守卫拒绝（发生在 CreateTask 之前），和校验失败（发生在 CreateTask 之后，走清理逻辑）。HTTP 路径测了 `192.0.2.4:5555`、`192.0.2.4`、`tv.lan` 三个值，宠物路径测了 `192.0.2.4`。

## 不做（按评审结论）
- N2（等待 authorizing 状态）、N3（接管模式不对称）、N4（INSERT OR IGNORE 静默撞号）这次都不改。

## 已知限制（新增）
- N5：无线调试模式遇到 unauthorized 时会执行一次 `adb kill-server`。adb server 是宿主机上所有设备共用的，这一步会把同一台机器上正在使用的 USB 和网络直连会话一起断掉。USB 和网络直连自己从不执行 kill-server。这是原有无线行为带来的副作用，这次不改。
- 沿用 r2 的已知限制：没接真实设备（Windows 下 `adb devices -l` 和 get-state、get-serialno 的实际输出需要 owner 验证）；schema v156 合并时谁后合并谁顺延；takeover_handlers.go 在 main 上本来就没 gofmt。

## 修复后的完整实现（c85a80f 的 internal/hardware/adb_usb.go 摘录）
~~~~go
// ensureADBUSBDevice confirms the serial is attached and authorized. An
// unauthorized device gets one window for the Owner to accept the prompt on
// the device; nothing here accepts or bypasses that prompt.
func ensureADBUSBDevice(ctx context.Context, executable, serial string) error {
	deadline := time.Now().Add(adbAuthorizeWait)
	for {
		state, detail := adbUSBDeviceState(ctx, executable, serial)
		switch state {
		case "device":
			return verifyADBUSBSerial(ctx, executable, serial)
		case "missing":
			return fmt.Errorf("未在 AHA 宿主机上找到 USB 设备 %s：请确认数据线已连接、设备已开启 USB 调试，并在硬件资源页刷新设备列表", serial)
		case "ambiguous":
			return fmt.Errorf("AHA 宿主机上有多台设备使用同一序列号 %s，无法确定目标：请只保留其中一台后重试", serial)
		case "offline":
			return fmt.Errorf("USB 设备 %s 处于离线状态：请重新插拔数据线，或在设备上关闭再打开 USB 调试后重试", serial)
		case "no_permissions":
			return fmt.Errorf("AHA 宿主机没有访问 USB 设备 %s 的权限：请检查宿主机 USB 驱动或权限设置", serial)
		case "unauthorized":
			if time.Now().After(deadline) {
				return fmt.Errorf("USB 设备 %s 未授权：请在设备上确认「允许 USB 调试」弹窗（建议勾选一律允许）后重试", serial)
			}
		case "error":
			return fmt.Errorf("读取 USB 设备 %s 状态失败：%s", serial, detail)
		default:
			return fmt.Errorf("USB 设备 %s 状态异常：%s", serial, state)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(adbAuthorizePollInterval):
		}
	}
}

// verifyADBUSBSerial asks the device that adb -s resolved to for its serial
// and requires an exact match, so -s can never land on another transport
// (for example a bare host adb expands to host:5555).
func verifyADBUSBSerial(ctx context.Context, executable, serial string) error {
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	stdout, stderr, err := runADBQuery(checkCtx, executable, "-s", serial, "get-serialno")
	reported := adbResultLine(stdout)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("读取 USB 设备 %s 的序列号失败：%s", serial, adbFailure(sanitizeADBText(adbResultLine(stderr), 512), err))
	}
	if reported != serial {
		return fmt.Errorf("USB 设备序列号不一致：配置为 %s，adb 实际连到 %s；已拒绝执行，请在硬件资源页重新选择设备", serial, sanitizeADBText(reported, 128))
	}
	return nil
}

// runADBQuery runs a short adb query and keeps stdout and stderr apart: a
// cold adb server prints "* daemon not running; starting now ..." banners on
// stderr that must never be read as a device answer.
func runADBQuery(ctx context.Context, executable string, arguments ...string) (string, string, error) {
	command := exec.CommandContext(ctx, executable, arguments...)
	configureADBCommand(command)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return stdout.String(), stderr.String(), err
}

// adbResultLine returns the last non-empty line that is not an adb daemon
// banner (lines starting with "*").
func adbResultLine(output string) string {
	lines := strings.Split(output, "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if line != "" && !strings.HasPrefix(line, "*") {
			return line
		}
	}
	return ""
}

// adbUSBDeviceState maps `adb -s <serial> get-state` to device, unauthorized,
// offline, no_permissions, missing, ambiguous, error or another raw state.
func adbUSBDeviceState(ctx context.Context, executable, serial string) (string, string) {
	stateCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	stdout, stderr, err := runADBQuery(stateCtx, executable, "-s", serial, "get-state")
	if err == nil {
		// Only stdout carries the state; stderr may hold daemon banners.
		state := sanitizeADBText(strings.ToLower(adbResultLine(stdout)), 32)
		if state == "" {
			return "error", "adb 未返回设备状态"
		}
		return state, ""
	}
	message := strings.ToLower(strings.TrimSpace(stdout + "\n" + stderr))
	switch {
	case strings.Contains(message, "more than one"):
		return "ambiguous", ""
	case strings.Contains(message, "not found"), strings.Contains(message, "no devices"):
		return "missing", ""
	case strings.Contains(message, "unauthorized"):
		return "unauthorized", ""
	case strings.Contains(message, "no permissions"), strings.Contains(message, "insufficient permissions"):
		return "no_permissions", ""
	case strings.Contains(message, "offline"):
		return "offline", ""
	}
	if ctx.Err() != nil {
		return "error", ctx.Err().Error()
	}
	detail := adbResultLine(stderr)
	if detail == "" {
		detail = adbResultLine(stdout)
	}
	return "error", adbFailure(sanitizeADBText(detail, 512), err)
}

// internal/domain/hardware.go
var adbUSBSerialPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// ValidADBUSBSerial accepts only serials that adb reports for USB devices.
// Network transports (host:port), mDNS service names and emulators are
// rejected so a USB resource can never be pointed at a network device, and
// adb -s device qualifiers (model:, usb:, product:) cannot be smuggled in.
// adb -s also resolves a bare host (192.0.2.4, localhost, tv.lan) to a
// connected host:5555 transport, so IP literals, dotted host names and
// localhost are rejected too. A dot never appears in a real USB serial we
// accept; the runtime get-serialno check is the second line of defence.
func ValidADBUSBSerial(serial string) bool {
	if !adbUSBSerialPattern.MatchString(serial) {
		return false
	}
	lower := strings.ToLower(serial)
	if strings.HasPrefix(lower, "emulator-") || strings.Contains(lower, "._adb") || strings.HasSuffix(lower, "._tcp") {
		return false
	}
	if strings.Contains(serial, ".") || lower == "localhost" || net.ParseIP(serial) != nil {
		return false
	}
	return true
}
~~~~

## 命令与退出码（都在本 worktree、c85a80f 的内容上执行）
- `go build ./internal/...` → 0；`go vet ./internal/hardware ./internal/domain ./internal/hardwareconfig ./internal/store ./internal/httpapi ./internal/app` → 0；`gofmt -l` 检查本次改动的 .go 文件 → 没有输出
- `go test -race ./internal/hardware -count=1` → 0
- `go test ./internal/hardware ./internal/hardwareconfig ./internal/domain -count=1` → 0
- `go test ./internal/store -run 'Hardware|Migration|ReplaceHardware' -count=1 -timeout 30m` → 0（252 秒；其中 TestMigrationV156UpgradesV155DatabaseWithLegacyADBRow 用 -v 单独跑过一次，PASS）
- `go test ./internal/httpapi -run 'Hardware|ADB|Takeover|Remote|AgentHardware|USB' -count=1 -timeout 40m` → 0（270.7 秒；包含 TestEditingOrDeletingUSBResourceDisconnectsOpenTerminal、TestTaskTakeoverKeepsADBConnectionForms、TestAgentADBExecRoutesEachConnectionFormToTheBoundTarget，这三个也用 -v 单独跑过，都是 PASS）
- `go test ./internal/app -run 'TestPetHardware|TestPetTakeover' -count=1 -timeout 30m` → 0（111 秒）
- web：`npm test` → 619 通过，0 失败。336d47b 到 c3 之间没有改动 web/，所以浏览器测试没有重跑
- 变异验证（在 scratch 里用 `go test -overlay`，源码没改），全部被测试抓到：
  - get-state 改回 stderr 和 stdout 混读 → TestADBUSBIgnoresDaemonBannerOnColdStart FAIL（“状态异常：* daemon not running; starting n”）
  - 去掉 verifyADBUSBSerial → TestADBUSBRefusesWhenResolvedSerialDiffers FAIL
  - 去掉 pet_takeover 的 USB 守卫 → TestPetTakeoverKeepsADBConnectionForms FAIL（实际报的是 NormalizeGroup 的“缺少 USB 设备序列号”）
  - HardwareResource.Supports 改回只看 host → TestEditingOrDeletingUSBResourceDisconnectsOpenTerminal FAIL（“editing the USB resource left the USB terminal connected”）
  - 黑名单删掉 tcpip → TestAgentADBExecRoutesEachConnectionFormToTheBoundTarget FAIL（“board [tcpip 5555] status=200 want 400”）

## delta：`git diff 336d47b c85a80f -- . ':!docs/review'`（等同于 `git diff 59246d3 c85a80f`）
- 文件数 10，28441 字节，sha256 `1b4e5bd66345ebd18267167bc35e1ee79aae96a5f772eeec54cae97259dcf301`

~~~~diff
diff --git a/internal/app/pet_takeover_adb_test.go b/internal/app/pet_takeover_adb_test.go
index dce6273..e2ed8f5 100644
--- a/internal/app/pet_takeover_adb_test.go
+++ b/internal/app/pet_takeover_adb_test.go
@@ -1,6 +1,7 @@
 package app
 
 import (
+	"database/sql"
 	"encoding/json"
 	"strings"
 	"testing"
@@ -70,10 +71,27 @@ func TestPetTakeoverKeepsADBConnectionForms(t *testing.T) {
 			{ID: "phone", Network: domain.HardwareNetworkConfig{Host: "10.0.0.20"}},
 		}
 	}
+	before := takeoverRowCounts(t, fixture.databasePath)
+	// Layer 1 (pet_takeover): a remote USB group without a local serial is
+	// refused before any Task is created. The message is the takeover guard's,
+	// not NormalizeGroup's "缺少 USB 设备序列号".
 	if _, _, err := fixture.service.PetTakeoverRemoteTask(fixture.ctx, PetTakeoverTaskInput{
 		TaskID: mirrors[0].ID, WorkspaceID: localWorkspace.ID, ModelID: fixture.model.ID, Groups: groups(""),
-	}); err == nil || !strings.Contains(err.Error(), "USB 设备序列号") {
-		t.Fatalf("takeover without a local USB serial: %v", err)
+	}); err == nil || !strings.Contains(err.Error(), "必须重新提供本机串口、网络端点或 USB 设备序列号") {
+		t.Fatalf("takeover without a local USB serial must be refused by the takeover guard: %v", err)
+	}
+	if after := takeoverRowCounts(t, fixture.databasePath); after != before {
+		t.Fatalf("guard rejection left rows behind: before=%v after=%v", before, after)
+	}
+	// Layer 2 (NormalizeGroup, after CreateTask): an invalid serial is refused
+	// and the half-created Task is cleaned up.
+	if _, _, err := fixture.service.PetTakeoverRemoteTask(fixture.ctx, PetTakeoverTaskInput{
+		TaskID: mirrors[0].ID, WorkspaceID: localWorkspace.ID, ModelID: fixture.model.ID, Groups: groups("192.0.2.4"),
+	}); err == nil || !strings.Contains(err.Error(), "USB 设备序列号无效") {
+		t.Fatalf("takeover with an IP-literal USB serial must be refused by validation: %v", err)
+	}
+	if after := takeoverRowCounts(t, fixture.databasePath); after != before {
+		t.Fatalf("validation rejection left a half-created Task behind: before=%v after=%v", before, after)
 	}
 	_, taken, err := fixture.service.PetTakeoverRemoteTask(fixture.ctx, PetTakeoverTaskInput{
 		TaskID: mirrors[0].ID, WorkspaceID: localWorkspace.ID, ModelID: fixture.model.ID, Groups: groups("LOCAL-SER"),
@@ -95,3 +113,21 @@ func TestPetTakeoverKeepsADBConnectionForms(t *testing.T) {
 		t.Fatalf("taken wireless=%#v", phone)
 	}
 }
+
+// takeoverRowCounts snapshots the rows a takeover may create, so a rejected
+// takeover can be shown to leave nothing behind.
+func takeoverRowCounts(t *testing.T, databasePath string) [4]int {
+	t.Helper()
+	db, err := sql.Open("sqlite", databasePath)
+	if err != nil {
+		t.Fatal(err)
+	}
+	defer db.Close()
+	var counts [4]int
+	for index, table := range []string{"tasks", "hardware_resources", "task_hardware_bindings", "runtime_config_snapshots"} {
+		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&counts[index]); err != nil {
+			t.Fatalf("count %s: %v", table, err)
+		}
+	}
+	return counts
+}
diff --git a/internal/domain/hardware.go b/internal/domain/hardware.go
index fdc0c40..63e78b1 100644
--- a/internal/domain/hardware.go
+++ b/internal/domain/hardware.go
@@ -1,6 +1,7 @@
 package domain
 
 import (
+	"net"
 	"regexp"
 	"strings"
 	"time"
@@ -111,6 +112,10 @@ var adbUSBSerialPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$
 // Network transports (host:port), mDNS service names and emulators are
 // rejected so a USB resource can never be pointed at a network device, and
 // adb -s device qualifiers (model:, usb:, product:) cannot be smuggled in.
+// adb -s also resolves a bare host (192.0.2.4, localhost, tv.lan) to a
+// connected host:5555 transport, so IP literals, dotted host names and
+// localhost are rejected too. A dot never appears in a real USB serial we
+// accept; the runtime get-serialno check is the second line of defence.
 func ValidADBUSBSerial(serial string) bool {
 	if !adbUSBSerialPattern.MatchString(serial) {
 		return false
@@ -119,6 +124,9 @@ func ValidADBUSBSerial(serial string) bool {
 	if strings.HasPrefix(lower, "emulator-") || strings.Contains(lower, "._adb") || strings.HasSuffix(lower, "._tcp") {
 		return false
 	}
+	if strings.Contains(serial, ".") || lower == "localhost" || net.ParseIP(serial) != nil {
+		return false
+	}
 	return true
 }
 
diff --git a/internal/hardware/adb_modes_test.go b/internal/hardware/adb_modes_test.go
index af42f44..1065d60 100644
--- a/internal/hardware/adb_modes_test.go
+++ b/internal/hardware/adb_modes_test.go
@@ -128,7 +128,8 @@ fi`)
 
 func TestExecuteADBUSBTargetsSerialWithoutNetworkCommands(t *testing.T) {
 	// Not parallel: the authorization wait is a package variable other tests shrink.
-	executable, logPath := fakeADB(t, `if [ "$1" = "-s" ] && [ "$2" = "R58M12345A" ] && [ "$3" = "get-state" ]; then
+	executable, logPath := fakeADB(t, `if [ "$1" = "-s" ] && [ "$3" = "get-serialno" ]; then echo "$2"; exit 0; fi
+if [ "$1" = "-s" ] && [ "$2" = "R58M12345A" ] && [ "$3" = "get-state" ]; then
   echo device
   exit 0
 fi
@@ -187,7 +188,8 @@ func TestExecuteADBUSBWaitsForOwnerAuthorization(t *testing.T) {
 	shrinkADBAuthorizeWait(t, 2*time.Second)
 	directory := t.TempDir()
 	marker := filepath.Join(directory, "count")
-	executable, logPath := fakeADB(t, `if [ "$3" = "get-state" ]; then
+	executable, logPath := fakeADB(t, `if [ "$1" = "-s" ] && [ "$3" = "get-serialno" ]; then echo "$2"; exit 0; fi
+if [ "$3" = "get-state" ]; then
   count=$(cat "`+marker+`" 2>/dev/null || echo 0)
   count=$((count+1))
   echo $count > "`+marker+`"
@@ -205,7 +207,7 @@ if [ "$3" = "shell" ]; then echo ok; exit 0; fi`)
 func TestExecuteADBUSBRejectsNonUSBSerialsBeforeRunningADB(t *testing.T) {
 	executable, logPath := fakeADB(t, ``)
 	t.Setenv(adbExecutableEnvironment, executable)
-	for _, serial := range []string{"", "192.0.2.4:5555", "adb-x._adb-tls-connect._tcp", "emulator-5554", "-s", "model:Pixel", "a b", "usb:1-1", "x;rm"} {
+	for _, serial := range []string{"", "192.0.2.4:5555", "adb-x._adb-tls-connect._tcp", "emulator-5554", "-s", "model:Pixel", "a b", "usb:1-1", "x;rm", "192.0.2.4", "localhost", "tv.lan"} {
 		if _, err := ExecuteADBUSB(context.Background(), serial, []string{"shell", "id"}, nil, time.Second); err == nil {
 			t.Fatalf("serial %q was accepted", serial)
 		}
@@ -220,7 +222,8 @@ func TestExecuteADBUSBRejectsNonUSBSerialsBeforeRunningADB(t *testing.T) {
 
 func TestOpenADBUSBTerminalStartsShellOnSerial(t *testing.T) {
 	// Not parallel: the authorization wait is a package variable other tests shrink.
-	executable, logPath := fakeADB(t, `if [ "$3" = "get-state" ]; then echo device; exit 0; fi
+	executable, logPath := fakeADB(t, `if [ "$1" = "-s" ] && [ "$3" = "get-serialno" ]; then echo "$2"; exit 0; fi
+if [ "$3" = "get-state" ]; then echo device; exit 0; fi
 if [ "$1" = "-s" ] && [ "$2" = "SER1" ] && [ "$3" = "shell" ] && [ "$4" = "-t" ] && [ "$5" = "-t" ]; then
   echo "usb shell"
   IFS= read -r line
@@ -350,3 +353,50 @@ func TestManagerRoutesEachADBConnectionFormToItsOpener(t *testing.T) {
 	default:
 	}
 }
+
+// A cold adb server prints daemon banners on stderr before answering. The USB
+// path must read the state and serial from stdout only, so the first command
+// after a host reboot or a killed server still works.
+func TestADBUSBIgnoresDaemonBannerOnColdStart(t *testing.T) {
+	// Not parallel: the authorization wait is a package variable other tests shrink.
+	banner := `echo "* daemon not running; starting now at tcp:5037" >&2; echo "* daemon started successfully" >&2;`
+	executable, logPath := fakeADB(t, `if [ "$1" = "-s" ] && [ "$2" = "SER1" ] && [ "$3" = "get-state" ]; then `+banner+` echo device; exit 0; fi
+if [ "$1" = "-s" ] && [ "$2" = "SER1" ] && [ "$3" = "get-serialno" ]; then `+banner+` echo SER1; exit 0; fi
+if [ "$1" = "-s" ] && [ "$2" = "SER1" ] && [ "$3" = "shell" ] && [ "$4" = "id" ]; then echo "uid=2000(shell)"; exit 0; fi
+if [ "$1" = "-s" ] && [ "$2" = "SER1" ] && [ "$3" = "shell" ] && [ "$4" = "-t" ]; then echo "usb shell"; IFS= read -r line; exit 0; fi`)
+	result, err := executeADBUSBWithExecutable(context.Background(), executable, "SER1", []string{"shell", "id"}, nil, 5*time.Second)
+	if err != nil || string(result.Stdout) != "uid=2000(shell)\n" {
+		t.Fatalf("exec after a daemon banner: result=%#v err=%v", result, err)
+	}
+	terminal, err := openADBUSBTerminalWithExecutable(context.Background(), executable, "SER1")
+	if err != nil {
+		t.Fatalf("terminal after a daemon banner: %v", err)
+	}
+	defer terminal.Close()
+	buffer := make([]byte, 64)
+	count, _ := terminal.Read(buffer)
+	if !strings.Contains(string(buffer[:count]), "usb shell") {
+		t.Fatalf("terminal output %q", buffer[:count])
+	}
+	assertNoHostADBCommands(t, adbCalls(t, logPath), "connect", "pair", "mdns", "kill-server")
+}
+
+// adb -s with a bare host can resolve to a connected host:5555 transport.
+// get-serialno must confirm the device is the configured USB serial before
+// any command runs; a mismatch is refused for exec and terminal alike.
+func TestADBUSBRefusesWhenResolvedSerialDiffers(t *testing.T) {
+	executable, logPath := fakeADB(t, `if [ "$3" = "get-state" ]; then echo device; exit 0; fi
+if [ "$3" = "get-serialno" ]; then echo "192.0.2.4:5555"; exit 0; fi
+if [ "$3" = "shell" ]; then echo "must not run"; exit 0; fi`)
+	if _, err := executeADBUSBWithExecutable(context.Background(), executable, "SER1", []string{"shell", "id"}, nil, 2*time.Second); err == nil || !strings.Contains(err.Error(), "序列号不一致") {
+		t.Fatalf("exec on a mismatched serial: %v", err)
+	}
+	if _, err := openADBUSBTerminalWithExecutable(context.Background(), executable, "SER1"); err == nil || !strings.Contains(err.Error(), "序列号不一致") {
+		t.Fatalf("terminal on a mismatched serial: %v", err)
+	}
+	for _, call := range adbCalls(t, logPath) {
+		if strings.Contains(call, " shell") {
+			t.Fatalf("a command ran on a mismatched device: %q", adbCalls(t, logPath))
+		}
+	}
+}
diff --git a/internal/hardware/adb_usb.go b/internal/hardware/adb_usb.go
index 20c8209..c051cf4 100644
--- a/internal/hardware/adb_usb.go
+++ b/internal/hardware/adb_usb.go
@@ -1,6 +1,7 @@
 package hardware
 
 import (
+	"bytes"
 	"context"
 	"fmt"
 	"io"
@@ -208,7 +209,7 @@ func ensureADBUSBDevice(ctx context.Context, executable, serial string) error {
 		state, detail := adbUSBDeviceState(ctx, executable, serial)
 		switch state {
 		case "device":
-			return nil
+			return verifyADBUSBSerial(ctx, executable, serial)
 		case "missing":
 			return fmt.Errorf("未在 AHA 宿主机上找到 USB 设备 %s：请确认数据线已连接、设备已开启 USB 调试，并在硬件资源页刷新设备列表", serial)
 		case "ambiguous":
@@ -234,22 +235,67 @@ func ensureADBUSBDevice(ctx context.Context, executable, serial string) error {
 	}
 }
 
+// verifyADBUSBSerial asks the device that adb -s resolved to for its serial
+// and requires an exact match, so -s can never land on another transport
+// (for example a bare host adb expands to host:5555).
+func verifyADBUSBSerial(ctx context.Context, executable, serial string) error {
+	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
+	defer cancel()
+	stdout, stderr, err := runADBQuery(checkCtx, executable, "-s", serial, "get-serialno")
+	reported := adbResultLine(stdout)
+	if err != nil {
+		if ctx.Err() != nil {
+			return ctx.Err()
+		}
+		return fmt.Errorf("读取 USB 设备 %s 的序列号失败：%s", serial, adbFailure(sanitizeADBText(adbResultLine(stderr), 512), err))
+	}
+	if reported != serial {
+		return fmt.Errorf("USB 设备序列号不一致：配置为 %s，adb 实际连到 %s；已拒绝执行，请在硬件资源页重新选择设备", serial, sanitizeADBText(reported, 128))
+	}
+	return nil
+}
+
+// runADBQuery runs a short adb query and keeps stdout and stderr apart: a
+// cold adb server prints "* daemon not running; starting now ..." banners on
+// stderr that must never be read as a device answer.
+func runADBQuery(ctx context.Context, executable string, arguments ...string) (string, string, error) {
+	command := exec.CommandContext(ctx, executable, arguments...)
+	configureADBCommand(command)
+	var stdout, stderr bytes.Buffer
+	command.Stdout = &stdout
+	command.Stderr = &stderr
+	err := command.Run()
+	return stdout.String(), stderr.String(), err
+}
+
+// adbResultLine returns the last non-empty line that is not an adb daemon
+// banner (lines starting with "*").
+func adbResultLine(output string) string {
+	lines := strings.Split(output, "\n")
+	for index := len(lines) - 1; index >= 0; index-- {
+		line := strings.TrimSpace(lines[index])
+		if line != "" && !strings.HasPrefix(line, "*") {
+			return line
+		}
+	}
+	return ""
+}
+
 // adbUSBDeviceState maps `adb -s <serial> get-state` to device, unauthorized,
 // offline, no_permissions, missing, ambiguous, error or another raw state.
 func adbUSBDeviceState(ctx context.Context, executable, serial string) (string, string) {
 	stateCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
 	defer cancel()
-	command := exec.CommandContext(stateCtx, executable, "-s", serial, "get-state")
-	configureADBCommand(command)
-	output, err := command.CombinedOutput()
-	message := strings.ToLower(strings.TrimSpace(string(output)))
+	stdout, stderr, err := runADBQuery(stateCtx, executable, "-s", serial, "get-state")
 	if err == nil {
-		state := sanitizeADBText(message, 32)
+		// Only stdout carries the state; stderr may hold daemon banners.
+		state := sanitizeADBText(strings.ToLower(adbResultLine(stdout)), 32)
 		if state == "" {
 			return "error", "adb 未返回设备状态"
 		}
 		return state, ""
 	}
+	message := strings.ToLower(strings.TrimSpace(stdout + "\n" + stderr))
 	switch {
 	case strings.Contains(message, "more than one"):
 		return "ambiguous", ""
@@ -265,5 +311,9 @@ func adbUSBDeviceState(ctx context.Context, executable, serial string) (string,
 	if ctx.Err() != nil {
 		return "error", ctx.Err().Error()
 	}
-	return "error", adbFailure(sanitizeADBText(string(output), 512), err)
+	detail := adbResultLine(stderr)
+	if detail == "" {
+		detail = adbResultLine(stdout)
+	}
+	return "error", adbFailure(sanitizeADBText(detail, 512), err)
 }
diff --git a/internal/hardwareconfig/config_test.go b/internal/hardwareconfig/config_test.go
index 222eec3..28eed45 100644
--- a/internal/hardwareconfig/config_test.go
+++ b/internal/hardwareconfig/config_test.go
@@ -48,7 +48,7 @@ func TestNormalizeGroupADBConnectionForms(t *testing.T) {
 	if _, err := normalizeNetwork(t, domain.HardwareNetworkConfig{Protocol: "adb", ADBConnection: "usb"}); err == nil || !strings.Contains(err.Error(), "USB 设备序列号") {
 		t.Fatalf("usb without serial: %v", err)
 	}
-	for _, serial := range []string{"192.0.2.4:5555", "adb-R58M._adb-tls-connect._tcp", "emulator-5554", "-s", "model:Pixel", "usb:1-1", "a b", "x;rm -rf", strings.Repeat("a", 129)} {
+	for _, serial := range []string{"192.0.2.4:5555", "adb-R58M._adb-tls-connect._tcp", "emulator-5554", "-s", "model:Pixel", "usb:1-1", "a b", "x;rm -rf", strings.Repeat("a", 129), "192.0.2.4", "localhost", "LocalHost", "tv.lan", "10.0.0.1"} {
 		if _, err := normalizeNetwork(t, domain.HardwareNetworkConfig{Protocol: "adb", ADBConnection: "usb", ADBSerial: serial}); err == nil {
 			t.Fatalf("invalid USB serial %q was accepted", serial)
 		}
diff --git a/internal/httpapi/agent_handlers.go b/internal/httpapi/agent_handlers.go
index 906c710..e061d88 100644
--- a/internal/httpapi/agent_handlers.go
+++ b/internal/httpapi/agent_handlers.go
@@ -92,6 +92,9 @@ var blockedADBHostCommands = map[string]bool{
 	"connect": true, "disconnect": true, "pair": true,
 	"kill-server": true, "start-server": true, "devices": true,
 	"mdns": true, "host-features": true,
+	// tcpip restarts adbd in TCP mode, silently turning a USB or wireless
+	// resource into a network device outside the resource's connection form.
+	"tcpip": true,
 }
 
 func (s *Server) agentADBExec(writer http.ResponseWriter, request *http.Request) {
diff --git a/internal/httpapi/hardware_adb_modes_test.go b/internal/httpapi/hardware_adb_modes_test.go
index e56bd9d..7930dbe 100644
--- a/internal/httpapi/hardware_adb_modes_test.go
+++ b/internal/httpapi/hardware_adb_modes_test.go
@@ -126,6 +126,9 @@ func TestAgentADBExecRoutesEachConnectionFormToTheBoundTarget(t *testing.T) {
 		{"board", []string{"-s", "SERIAL-B", "shell", "id"}, http.StatusBadRequest},
 		{"tv", []string{"pair", "192.0.2.11:37000"}, http.StatusBadRequest},
 		{"tv", []string{"kill-server"}, http.StatusBadRequest},
+		{"board", []string{"tcpip", "5555"}, http.StatusBadRequest},
+		{"legacy", []string{"TCPIP", "5555"}, http.StatusBadRequest},
+		{"tv", []string{"tcpip", "5601"}, http.StatusBadRequest},
 	} {
 		if status := exec(testCase.id, testCase.args...); status != testCase.status {
 			t.Fatalf("%s %v status=%d want %d", testCase.id, testCase.args, status, testCase.status)
diff --git a/internal/httpapi/hardware_adb_usb_disconnect_test.go b/internal/httpapi/hardware_adb_usb_disconnect_test.go
new file mode 100644
index 0000000..bca0c2f
--- /dev/null
+++ b/internal/httpapi/hardware_adb_usb_disconnect_test.go
@@ -0,0 +1,124 @@
+//go:build !windows
+
+package httpapi
+
+import (
+	"context"
+	"net/http"
+	"net/http/cookiejar"
+	"net/http/httptest"
+	"os"
+	"path/filepath"
+	"testing"
+	"time"
+
+	"github.com/ChinaKai/AHA2/internal/app"
+	"github.com/ChinaKai/AHA2/internal/auth"
+	"github.com/ChinaKai/AHA2/internal/domain"
+	"github.com/ChinaKai/AHA2/internal/hardware"
+	"github.com/ChinaKai/AHA2/internal/store"
+)
+
+// End to end through the real hardware.Manager and a fake adb: a Task's open
+// USB terminal is disconnected when the Owner edits the USB resource, and
+// again when the resource is deleted. A USB resource has no host, so this
+// depends on Supports(network) recognising the serial.
+func TestEditingOrDeletingUSBResourceDisconnectsOpenTerminal(t *testing.T) {
+	adb := filepath.Join(t.TempDir(), "adb")
+	script := `#!/bin/sh
+if [ "$1" = "-s" ] && [ "$3" = "get-state" ]; then echo device; exit 0; fi
+if [ "$1" = "-s" ] && [ "$3" = "get-serialno" ]; then echo "$2"; exit 0; fi
+if [ "$1" = "-s" ] && [ "$3" = "shell" ]; then while IFS= read -r line; do echo "$line"; done; exit 0; fi
+echo "unexpected arguments: $*" >&2
+exit 2
+`
+	if err := os.WriteFile(adb, []byte(script), 0o700); err != nil {
+		t.Fatal(err)
+	}
+	t.Setenv("AHA2_ADB_PATH", adb)
+
+	ctx := context.Background()
+	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
+	if err != nil {
+		t.Fatal(err)
+	}
+	defer database.Close()
+	task := createHardwareAPITask(t, database)
+	manager := hardware.NewManager(database)
+	defer manager.Close()
+	server := httptest.NewServer(New(Config{
+		Store: database, Auth: auth.NewService(database, "setup-test", time.Hour),
+		App: app.NewService(database, nil, app.StubExecutor{}), Secrets: &fakeSecretStore{}, Hardware: manager,
+	}).Handler())
+	defer server.Close()
+	jar, _ := cookiejar.New(nil)
+	client := &http.Client{Jar: jar}
+	csrf := registerOwner(t, client, server.URL)
+
+	usbNetwork := func(serial string) map[string]any {
+		return map[string]any{"protocol": "adb", "adb_connection": "usb", "adb_serial": serial}
+	}
+	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/hardware-resources", map[string]any{
+		"description": "USB board", "mode": "network", "access": "read_write", "network": usbNetwork("SER1"),
+	}, csrf)
+	var created struct {
+		Resource domain.HardwareResource `json:"resource"`
+	}
+	decodeResponse(t, response, &created)
+	if response.StatusCode != http.StatusCreated {
+		t.Fatalf("create USB resource status=%d", response.StatusCode)
+	}
+	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/tasks/"+task.ID+"/hardware-bindings",
+		map[string]any{"bindings": []map[string]any{{"resource_id": created.Resource.ID}}}, csrf)
+	var bound struct {
+		Groups []domain.HardwareGroup `json:"groups"`
+	}
+	decodeResponse(t, response, &bound)
+	if response.StatusCode != http.StatusOK || len(bound.Groups) != 1 {
+		t.Fatalf("bind status=%d groups=%#v", response.StatusCode, bound.Groups)
+	}
+	hardwareID := bound.Groups[0].ID
+	connected := func() bool {
+		return manager.Status(task.ID, hardwareID, domain.HardwareTransportNetwork, false).Connected
+	}
+	connect := func(wantEndpoint string) {
+		t.Helper()
+		response := requestJSON(t, client, http.MethodPost,
+			server.URL+"/api/v1/tasks/"+task.ID+"/hardware/"+hardwareID+"/connect?transport=network", nil, csrf)
+		var payload struct {
+			Status domain.HardwareTerminalStatus `json:"status"`
+		}
+		decodeResponse(t, response, &payload)
+		if response.StatusCode != http.StatusOK || !payload.Status.Connected || payload.Status.Endpoint != wantEndpoint || !connected() {
+			t.Fatalf("connect status=%d payload=%#v", response.StatusCode, payload)
+		}
+	}
+	waitDisconnected := func(step string) {
+		t.Helper()
+		deadline := time.Now().Add(5 * time.Second)
+		for connected() {
+			if time.Now().After(deadline) {
+				t.Fatalf("%s left the USB terminal connected", step)
+			}
+			time.Sleep(20 * time.Millisecond)
+		}
+	}
+
+	connect("USB SER1")
+	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/hardware-resources/"+created.Resource.ID, map[string]any{
+		"description": "USB board", "mode": "network", "access": "read_write", "network": usbNetwork("SER2"),
+	}, csrf)
+	response.Body.Close()
+	if response.StatusCode != http.StatusOK {
+		t.Fatalf("update status=%d", response.StatusCode)
+	}
+	waitDisconnected("editing the USB resource")
+
+	connect("USB SER2")
+	response = requestJSON(t, client, http.MethodDelete, server.URL+"/api/v1/hardware-resources/"+created.Resource.ID, nil, csrf)
+	response.Body.Close()
+	if response.StatusCode != http.StatusOK {
+		t.Fatalf("delete status=%d", response.StatusCode)
+	}
+	waitDisconnected("deleting the USB resource")
+}
diff --git a/internal/httpapi/takeover_adb_test.go b/internal/httpapi/takeover_adb_test.go
index 64499b3..9462a03 100644
--- a/internal/httpapi/takeover_adb_test.go
+++ b/internal/httpapi/takeover_adb_test.go
@@ -2,6 +2,7 @@ package httpapi
 
 import (
 	"context"
+	"database/sql"
 	"encoding/json"
 	"net/http"
 	"net/http/cookiejar"
@@ -21,7 +22,8 @@ import (
 // resource so USB / network direct / wireless never swap meaning.
 func TestTaskTakeoverKeepsADBConnectionForms(t *testing.T) {
 	ctx := context.Background()
-	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
+	databasePath := filepath.Join(t.TempDir(), "aha2.db")
+	database, err := store.Open(ctx, databasePath)
 	if err != nil {
 		t.Fatal(err)
 	}
@@ -80,16 +82,41 @@ func TestTaskTakeoverKeepsADBConnectionForms(t *testing.T) {
 			},
 		}, csrf)
 	}
+	countRows := func() [4]int {
+		t.Helper()
+		db, err := sql.Open("sqlite", databasePath)
+		if err != nil {
+			t.Fatal(err)
+		}
+		defer db.Close()
+		var counts [4]int
+		for index, table := range []string{"tasks", "hardware_resources", "task_hardware_bindings", "runtime_config_snapshots"} {
+			if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&counts[index]); err != nil {
+				t.Fatalf("count %s: %v", table, err)
+			}
+		}
+		return counts
+	}
+	before := countRows()
 	response := takeover(map[string]any{"protocol": "adb"})
 	var failed map[string]any
 	decodeResponse(t, response, &failed)
 	if response.StatusCode != http.StatusBadRequest || failed["error"] != "local_hardware_fields_required" || !strings.Contains(failed["message"].(string), "USB 设备序列号") {
 		t.Fatalf("takeover without a local USB serial status=%d payload=%#v", response.StatusCode, failed)
 	}
-	response = takeover(map[string]any{"protocol": "adb", "adb_serial": "192.0.2.4:5555"})
-	response.Body.Close()
-	if response.StatusCode != http.StatusBadRequest {
-		t.Fatalf("takeover with a network-style USB serial status=%d", response.StatusCode)
+	if after := countRows(); after != before {
+		t.Fatalf("guard rejection left rows behind: before=%v after=%v", before, after)
+	}
+	// Validation runs after the Task is created; the failure must clean it up.
+	for _, serial := range []string{"192.0.2.4:5555", "192.0.2.4", "tv.lan"} {
+		response = takeover(map[string]any{"protocol": "adb", "adb_serial": serial})
+		decodeResponse(t, response, &failed)
+		if response.StatusCode != http.StatusBadRequest || failed["error"] != "local_hardware_invalid" {
+			t.Fatalf("takeover with USB serial %q status=%d payload=%#v", serial, response.StatusCode, failed)
+		}
+		if after := countRows(); after != before {
+			t.Fatalf("validation rejection (%q) left a half-created Task behind: before=%v after=%v", serial, before, after)
+		}
 	}
 	response = takeover(map[string]any{"protocol": "adb", "adb_serial": "LOCAL-SER"})
 	var created struct {
diff --git a/internal/store/hardware_adb_test.go b/internal/store/hardware_adb_test.go
index 213b9e6..4e4c911 100644
--- a/internal/store/hardware_adb_test.go
+++ b/internal/store/hardware_adb_test.go
@@ -133,3 +133,80 @@ func TestReplaceHardwareGroupsKeepsUSBNetworkSideOfBothMode(t *testing.T) {
 		t.Fatalf("USB side of a both-mode group was lost: %#v", groups)
 	}
 }
+
+// A database at v155 (no ADB connection columns, no v156 row) holding a
+// paired wireless phone is migrated in place: the columns appear, v156 is
+// recorded once, and the phone keeps host, port and pairing as wireless.
+func TestMigrationV156UpgradesV155DatabaseWithLegacyADBRow(t *testing.T) {
+	t.Parallel()
+	ctx := context.Background()
+	path := filepath.Join(t.TempDir(), "aha2.db")
+	database, err := Open(ctx, path)
+	if err != nil {
+		t.Fatal(err)
+	}
+	database.Close()
+	raw, err := sql.Open("sqlite", path)
+	if err != nil {
+		t.Fatal(err)
+	}
+	// Roll the schema back to its v155 shape.
+	for _, statement := range []string{
+		`ALTER TABLE hardware_resources DROP COLUMN adb_serial`,
+		`ALTER TABLE hardware_resources DROP COLUMN adb_connection`,
+		`DELETE FROM schema_migrations WHERE version=156`,
+	} {
+		if _, err := raw.ExecContext(ctx, statement); err != nil {
+			t.Fatalf("%s: %v", statement, err)
+		}
+	}
+	columnExists := func(name string) bool {
+		var exists bool
+		if err := raw.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pragma_table_info('hardware_resources') WHERE name=?)`, name).Scan(&exists); err != nil {
+			t.Fatal(err)
+		}
+		return exists
+	}
+	if columnExists("adb_connection") || columnExists("adb_serial") {
+		t.Fatal("rollback to the v155 shape failed")
+	}
+	// Insert with the v155 column list, as a pre-upgrade server would.
+	if _, err := raw.ExecContext(ctx, `
+		INSERT INTO hardware_resources(id,description,mode,serial_device,serial_baudrate,network_host,network_port,
+			network_protocol,ssh_auth,username,credential_ref,password_configured,adb_paired_at,access,created_at,updated_at)
+		VALUES('phone','Phone','network','',115200,'192.0.2.5',41235,'adb','auto','','',0,'2026-11-01T00:00:00Z','read_write',
+			'2026-11-01T00:00:00Z','2026-11-01T00:00:00Z')`); err != nil {
+		t.Fatal(err)
+	}
+	raw.Close()
+
+	database, err = Open(ctx, path)
+	if err != nil {
+		t.Fatalf("migrate v155 database: %v", err)
+	}
+	defer database.Close()
+	phone, err := database.HardwareResource(ctx, "phone")
+	if err != nil {
+		t.Fatal(err)
+	}
+	if !phone.Network.ADBWireless() || phone.Network.ADBConnection != "wireless" || phone.Network.ADBSerial != "" ||
+		phone.Network.Host != "192.0.2.5" || phone.Network.Port != 41235 || phone.ADBPairedAt != "2026-11-01T00:00:00Z" {
+		t.Fatalf("legacy phone changed meaning after migration: %#v", phone)
+	}
+	raw, err = sql.Open("sqlite", path)
+	if err != nil {
+		t.Fatal(err)
+	}
+	defer raw.Close()
+	if !columnExists("adb_connection") || !columnExists("adb_serial") {
+		t.Fatal("v156 did not add the ADB columns")
+	}
+	var stored string
+	var recorded int
+	if err := raw.QueryRowContext(ctx, `SELECT adb_connection FROM hardware_resources WHERE id='phone'`).Scan(&stored); err != nil || stored != "" {
+		t.Fatalf("migration rewrote the legacy row: %q err=%v", stored, err)
+	}
+	if err := raw.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=156`).Scan(&recorded); err != nil || recorded != 1 {
+		t.Fatalf("v156 recorded=%d err=%v", recorded, err)
+	}
+}
~~~~
