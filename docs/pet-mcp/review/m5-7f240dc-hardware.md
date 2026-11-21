# M5：存储凭据回显与登录状态机（固定 7f240dc）

源码 `7f240dcd2a12a479b3954dc842961790a5a5b2de`，tree `309b9d42b5db5ec8734f27edd1c96b722f46b993`；增量 `9bed44d..7f240dc` 共 18 个文件，全部在 `internal/hardware/` 与 `internal/httpapi/`。`9bed44d` 是 M2-a 材料提交，本包不改 M2-a 与已投审包。下方 diff 与函数全文都从固定提交取得，diff 保留 3 行上下文，不截断；两个包按目录切分，合计覆盖本轮全部增量。

固定副本（git archive，源码不改）验证：

- `go test -json ./internal/hardware -count=1 -race -timeout=15m` → 退出 0，5.4s，具名用例 37 个全部 run/pass，遗漏 0
- `go test -json ./internal/httpapi -run 'Hardware|ADB|Login|Terminal|Credential|HostKey' -count=1 -timeout=40m` → 退出 0，274.3s，具名用例 41 个全部 run/pass，遗漏 0
- `go vet ./internal/hardware ./internal/httpapi` → 退出 0，1.1s

测试只用 fake 设备、loopback 监听、随机生成的 fixture 凭据和本地假 adb 脚本，不连接真实硬件、不读真实凭据。

## A. 终端流过滤、历史防线与登录状态机

问题：保存的登录凭据经设备回显后进入终端历史、实时广播和模型可读的 terminal_read；登录可被重复触发、超时/取消后仍可能写出凭据。

- 设备输出在解码、记录、广播之前先过 `secretStream`：只扣住"可能是凭据前缀"的字节，跨任意 read 边界匹配（KMP 回退，线性时间）；EOF/断开时残留前缀只输出掩码。
- 历史读取统一经 `RedactHistory`：兼容修复前写入的记录和逐键 tx 记录。分页窗口两端都按未知上下文处理：尾部前缀由 `Finish` 掩码，开头可能接续前一页凭据的片段由 `possibleContinuation` 掩码，任何 cursor/limit 都拼不出凭据。代价是页首偶尔会把恰好等于凭据后缀的少量普通字节显示成掩码，不会泄露。
- 登录：显式尝试有连接级预算（默认 1，最多 3）；已认证或进行中的重复调用幂等；失败/超时/取消/断开/连接错误都会让代次失效并关闭等待通道，排队中的凭据写入在写锁内复核代次、属主附件和只读状态。凭据与成功提示必须位于缓冲结尾；凭据已发送后，失败提示出现在任意位置都算失败（同一次 read 同时带回 `Login incorrect` 与新 `login:` 时报 failed，不会拖到 timeout，也不会重发）。凭据发送前横幅里提到 `access denied` 不会中止登录。
- 错误文本、状态、记录失败日志里出现已知凭据时一律替换。

本包对应固定副本具名用例（37 个，全部 run/pass）：`TestAppendTrustedSSHHostKeyRejectsChangedKey`, `TestBannerMentioningFailureBeforeAnyCredentialDoesNotAbortLogin`, `TestBase64FallbackInput`, `TestConnectADBAcceptsReadyDevice`, `TestConnectADBReportsOfflineDevice`, `TestConnectADBReportsUnauthorizedDevice`, `TestConnectADBRestartsServerBeforeGivingUp`, `TestConnectADBWaitsForLateAuthorization`, `TestDecodeHexAcceptsCommonByteFormats`, `TestDisconnectDisarmsWriterEvenWhileReadOnlyObserverRemains`, `TestExecuteADBReturnsBinaryResultAndExitCode`, `TestFailureAndFreshPromptInOneReadReportFailureWithoutResend`, `TestHistoryDefenseCannotReassembleCredentialAcrossPages`, `TestHistoryDefenseMasksRetainedSplitEventsWithoutChangingCursors`, `TestKnownCredentialDoesNotEscapeConnectionErrorsStatusOrLogs`, `TestLoginEchoIsMaskedAndAuthenticatedLoginCannotRearm`, `TestLoginFailureTimeoutAndCancelDisarmAndPermitExplicitBudgetedRetry`, `TestLoginReplayIsIdempotentAndExplicitRetriesHaveFiniteBudget`, `TestManagerExplicitSerialLoginRecognizesExistingShell`, `TestManagerExplicitSerialLoginWakesConsoleAndUsesConfiguredLineEnding`, `TestManagerLiveSubscriptionRawInputAndResize`, `TestManagerNetworkADBUsesDeviceShell`, `TestManagerNetworkSSHUsesInteractiveTerminal`, `TestManagerNetworkTelnetAndAutoLogin`, `TestManagerSerialReadWriteAndPermission`, `TestOpenADBTerminalConnectsAndStartsDeviceShell`, `TestOpenSSHTerminalInteractiveShell`, `TestPairADBUsesPairingEndpointAndRedactsCode`, `TestRecordFailureIsCountedLoggedAndShownOnStatus`, `TestResolveADBExecutableFindsWingetPackage`, `TestSSHHostKeyProbeAndExplicitTrust`, `TestSSHKnownHostsCallback`, `TestSSHRequiresKnownHostsAndAuthentication`, `TestSecretStreamProtectsEveryChunkBoundaryAndFinalPrefix`, `TestSharedHardwareAllowsOnlyOneReadWriteTask`, `TestStreamDecoderPreservesSplitUTF8Bytes`, `TestTelnetCodecStripsNegotiation`

反向验证（提交前工作区）：临时关闭页首接续遮蔽，`TestHistoryDefenseCannotReassembleCredentialAcrossPages` 失败；临时关闭"凭据发送后失败提示任意位置生效"，`TestFailureAndFreshPromptInOneReadReportFailureWithoutResend` 失败（5s 内未到 failed）；恢复后通过。

完整 diff 文件边界：

- `internal/hardware/history_secret.go`（新文件）
- `internal/hardware/login_security_test.go`（新文件）
- `internal/hardware/login_state.go`（新文件）
- `internal/hardware/manager.go`
- `internal/hardware/manager_test.go`
- `internal/hardware/secret_stream.go`（新文件）

## 必要调用链（固定提交全文）

### `internal/hardware/manager.go` · `Connect`

```go
func (manager *Manager) Connect(ctx context.Context, request ConnectRequest) (domain.HardwareTerminalStatus, error) {
	if request.TaskID == "" || request.HardwareID == "" {
		return domain.HardwareTerminalStatus{}, fmt.Errorf("task and hardware ids are required")
	}
	if !request.Group.Supports(request.Transport) {
		return domain.HardwareTerminalStatus{}, fmt.Errorf("hardware group does not support %s", request.Transport)
	}
	targetKey, endpoint, protocol, baudrate, err := sessionTarget(request.Group, request.Transport)
	if err != nil {
		return domain.HardwareTerminalStatus{}, err
	}
	attachmentKey := attachmentKey(request.TaskID, request.HardwareID, request.Transport)
	sshAuth := normalizeSSHAuth(request.Group.Network.SSHAuth)

	manager.mu.Lock()
	if current := manager.attachments[attachmentKey]; current != nil {
		if current.targetKey == targetKey && current.isConnected() {
			status := current.statusFor(request.TaskID, request.HardwareID, request.ReadOnly)
			manager.mu.Unlock()
			return status, nil
		}
		manager.detachLocked(attachmentKey, current)
	}
	if current := manager.sessions[targetKey]; current != nil && current.isConnected() {
		if current.username != request.Group.Username || current.password != request.Password ||
			current.sshAuth != sshAuth || current.baudrate != baudrate {
			manager.mu.Unlock()
			return domain.HardwareTerminalStatus{}, fmt.Errorf("%s is already connected with different settings", endpoint)
		}
		current.mu.Lock()
		if !request.ReadOnly {
			for key, existing := range current.attachments {
				if key != attachmentKey && !existing.readOnly {
					current.mu.Unlock()
					manager.mu.Unlock()
					return domain.HardwareTerminalStatus{}, fmt.Errorf("%s is already attached to another task for read-write access", endpoint)
				}
			}
		}
		current.attachments[attachmentKey] = attachment{
			taskID: request.TaskID, hardwareID: request.HardwareID, readOnly: request.ReadOnly,
		}
		current.mu.Unlock()
		manager.attachments[attachmentKey] = current
		status := current.statusFor(request.TaskID, request.HardwareID, request.ReadOnly)
		manager.mu.Unlock()
		current.record("system", "attached to shared hardware terminal", "runtime", nil, nil)
		return status, nil
	}

	var connection io.ReadWriteCloser
	if request.Transport == domain.HardwareTransportSerial {
		connection, err = manager.openSerial(request.Group.Serial.Device, baudrate)
	} else if protocol == domain.HardwareProtocolSSH {
		connection, err = manager.openSSH(ctx, endpoint, request.Group.Username, request.Password, sshAuth)
	} else if protocol == domain.HardwareProtocolADB {
		connection, err = manager.openADB(ctx, endpoint)
	} else {
		connection, err = manager.dialNetwork(ctx, "tcp", endpoint)
	}
	if err != nil {
		manager.mu.Unlock()
		return domain.HardwareTerminalStatus{}, fmt.Errorf("connect %s: %w", endpoint, redactHardwareError(err, request.Password))
	}
	if resolved, ok := connection.(interface{ ResolvedEndpoint() string }); ok {
		if value := strings.TrimSpace(resolved.ResolvedEndpoint()); value != "" {
			endpoint = value
		}
	}
	now := manager.now().UTC()
	session := &terminalSession{
		manager: manager, targetKey: targetKey, endpoint: endpoint, transport: request.Transport,
		protocol: protocol, sshAuth: sshAuth, username: request.Group.Username, password: request.Password, baudrate: baudrate,
		conn: connection, startedAt: now, updatedAt: now, status: "running",
		attachments: map[string]attachment{
			attachmentKey: {taskID: request.TaskID, hardwareID: request.HardwareID, readOnly: request.ReadOnly},
		},
		subscribers: map[string]liveSubscriber{},
	}
	session.loginOptions = normalizeLoginOptions(LoginOptions{})
	session.outputRedactor = newSecretStream(session.password)
	if protocol == domain.HardwareProtocolSSH || protocol == domain.HardwareProtocolADB {
		session.loginStatus = "authenticated"
	} else if session.username != "" || session.password != "" {
		if request.ReadOnly {
			session.loginStatus = "read_only"
		} else {
			session.armLoginLocked(session.loginOptions, attachmentKey, 1, true)
		}
	} else {
		session.loginStatus = "not_configured"
	}

	if protocol == domain.HardwareProtocolTelnet {
		session.codec = newTelnetCodec(100, 28)
	}
	manager.sessions[targetKey] = session
	manager.attachments[attachmentKey] = session
	manager.mu.Unlock()

	session.record("system", "hardware terminal connected", "runtime", nil, nil)
	if session.codec != nil {
		_ = session.writeRaw(session.codec.InitialNegotiation())
	}
	session.mu.RLock()
	loginWaiting, generation, seconds, done := session.loginArmed, session.loginGeneration, session.loginOptions.TimeoutSeconds, session.loginDone
	session.mu.RUnlock()
	go session.readLoop()
	if loginWaiting {
		go session.loginTimeout(generation, seconds, done)
	}

	return session.statusFor(request.TaskID, request.HardwareID, request.ReadOnly), nil
}
```

### `internal/hardware/manager.go` · `readLoop`

```go
func (session *terminalSession) readLoop() {
	defer func() {
		if pending := session.outputRedactor.Finish(); len(pending) > 0 {
			if text, raw := session.decoder.Decode(pending); len(raw) > 0 {
				session.record("rx", text, "runtime", raw, nil)
			}
		}
	}()
	buffer := make([]byte, 4096)
	for {
		count, err := session.conn.Read(buffer)
		if count > 0 {
			payload := append([]byte(nil), buffer[:count]...)
			if session.codec != nil {
				var reply []byte
				payload, reply = session.codec.Feed(payload)
				if len(reply) > 0 {
					_ = session.writeRaw(reply)
				}
			}
			session.maybeAutoLogin(payload)
			if text, terminalData := session.decoder.Decode(session.outputRedactor.Feed(payload)); len(terminalData) > 0 {
				session.record("rx", text, "runtime", terminalData, nil)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) || session.isClosing() {
				break
			}
			session.fail(err)
			break
		}
	}
}
```

### `internal/hardware/manager.go` · `record`

```go
func (session *terminalSession) record(direction, data, source string, raw []byte, only *attachment) {
	data = redactSecretText(data, session.password)
	if len(raw) > 0 && session.password != "" {
		raw = bytes.ReplaceAll(raw, []byte(session.password), []byte(secretMaskFor(session.password)))
	}
	if direction == "rx" {
		if data == "" && len(raw) == 0 {
			return
		}
	} else if strings.TrimSpace(data) == "" {
		return
	}
	session.mu.RLock()
	targets := make([]attachment, 0, len(session.attachments))
	if only != nil {
		targets = append(targets, *only)
	} else {
		for _, item := range session.attachments {
			targets = append(targets, item)
		}
	}
	session.mu.RUnlock()
	now := session.manager.now().UTC()
	for _, target := range targets {
		var sequence int64
		if session.manager.recorder == nil {
			if direction == "rx" && len(raw) > 0 {
				session.broadcastOutput(target, sequence, raw)
			}
			continue
		}
		var recordErr error
		sequence, recordErr = session.manager.recorder.AppendHardwareIO(context.Background(), domain.HardwareIOEvent{
			ID: domain.NewID("hardware_io"), TaskID: target.taskID, HardwareID: target.hardwareID,
			Transport: session.transport, Direction: direction, Data: data, Encoding: "text",
			Source: source, CreatedAt: now,
		})
		session.noteRecordResult(target, direction, recordErr)
		if direction == "rx" && len(raw) > 0 {
			session.broadcastOutput(target, sequence, raw)
		}
	}
	session.mu.Lock()
	session.updatedAt = now
	session.mu.Unlock()
}
```

### `internal/hardware/manager.go` · `detachLocked`

```go
func (manager *Manager) detachLocked(key string, session *terminalSession) {
	delete(manager.attachments, key)
	session.mu.Lock()
	if session.loginOwner == key && session.loginArmed {
		session.stopLoginLocked("cancelled", "")
	}
	delete(session.attachments, key)
	remaining := len(session.attachments)
	session.mu.Unlock()
	if remaining == 0 {
		delete(manager.sessions, session.targetKey)
		session.close("hardware terminal disconnected")
	}
}
```

### `internal/hardware/manager.go` · `fail`

```go
func (session *terminalSession) fail(err error) {
	session.mu.Lock()
	if session.closing {
		session.mu.Unlock()
		return
	}
	session.status = "error"
	session.lastError = redactSecretText(err.Error(), session.password)
	session.stopLoginLocked("failed", "hardware connection failed")
	session.updatedAt = session.manager.now().UTC()
	session.mu.Unlock()
	session.record("system", "hardware terminal error: "+redactSecretText(err.Error(), session.password), "runtime", nil, nil)
	session.broadcastStatus()
	_ = session.conn.Close()
}
```

### `internal/hardware/manager.go` · `close`

```go
func (session *terminalSession) close(reason string) {
	session.mu.Lock()
	if session.closing {
		session.mu.Unlock()
		return
	}
	session.stopLoginLocked("cancelled", "")
	session.closing = true
	session.status = "disconnected"
	session.updatedAt = session.manager.now().UTC()
	session.mu.Unlock()
	session.record("system", reason, "runtime", nil, nil)
	session.broadcastStatus()
	_ = session.conn.Close()
}
```

## 该边界完整 diff

```diff
diff --git a/internal/hardware/history_secret.go b/internal/hardware/history_secret.go
new file mode 100644
index 0000000..0689b7b
--- /dev/null
+++ b/internal/hardware/history_secret.go
@@ -0,0 +1,47 @@
+package hardware
+
+import "github.com/ChinaKai/AHA2/internal/domain"
+
+// RedactHistory is a defense for retained records from older versions and for
+// stored credentials typed as separate keystroke events. Newly received output
+// is already sanitized before persistence and broadcast. A page is only a window
+// of each direction's stream, so both ends are treated as unknown context: a
+// trailing credential prefix is masked (Finish) and a leading fragment that
+// could finish an earlier credential is masked (possibleContinuation). Paging
+// with any cursor or limit therefore cannot reassemble a credential. Event
+// identity/cursors stay unchanged, including an event emptied by prefix hold.
+func RedactHistory(page domain.HardwareIOPage, secret string) domain.HardwareIOPage {
+	if secret == "" {
+		return page
+	}
+	page.Items = append([]domain.HardwareIOEvent(nil), page.Items...)
+	heads := map[string][]byte{}
+	totals := map[string]int{}
+	for _, item := range page.Items {
+		key := item.Transport + "\x00" + item.Direction
+		totals[key] += len(item.Data)
+		if room := len(secret) - len(heads[key]); room > 0 {
+			heads[key] = append(heads[key], item.Data[:min(room, len(item.Data))]...)
+		}
+	}
+	filters := map[string]*secretStream{}
+	last := map[string]int{}
+	for index := range page.Items {
+		item := &page.Items[index]
+		key := item.Transport + "\x00" + item.Direction
+		filter := filters[key]
+		if filter == nil {
+			filter = newSecretStream(secret)
+			filter.forced = possibleContinuation(filter.secret, heads[key], totals[key])
+			filters[key] = filter
+		}
+		item.Data = string(filter.Feed([]byte(item.Data)))
+		last[key] = index
+	}
+	for key, filter := range filters {
+		if tail := filter.Finish(); len(tail) > 0 {
+			page.Items[last[key]].Data += string(tail)
+		}
+	}
+	return page
+}
diff --git a/internal/hardware/login_security_test.go b/internal/hardware/login_security_test.go
new file mode 100644
index 0000000..e071a2d
--- /dev/null
+++ b/internal/hardware/login_security_test.go
@@ -0,0 +1,477 @@
+package hardware
+
+import (
+	"bufio"
+	"bytes"
+	"context"
+	"crypto/rand"
+	"encoding/hex"
+	"errors"
+	"io"
+	"log/slog"
+	"net"
+	"strings"
+	"sync"
+	"sync/atomic"
+	"testing"
+	"time"
+
+	"github.com/ChinaKai/AHA2/internal/domain"
+)
+
+func privateFixtureSecret(t *testing.T) string {
+	t.Helper()
+	var value [24]byte
+	if _, err := rand.Read(value[:]); err != nil {
+		t.Fatal("fixture secret generation failed")
+	}
+	return hex.EncodeToString(value[:])
+}
+
+func TestSecretStreamProtectsEveryChunkBoundaryAndFinalPrefix(t *testing.T) {
+	secret := privateFixtureSecret(t)
+	for split := 0; split <= len(secret); split++ {
+		filter := newSecretStream(secret)
+		output := filter.Feed([]byte("before:" + secret[:split]))
+		output = append(output, filter.Feed([]byte(secret[split:]+":after"))...)
+		output = append(output, filter.Finish()...)
+		if string(output) != "before:"+secretMask+":after" || bytes.Contains(output, []byte(secret)) {
+			t.Fatal("credential escaped across chunk boundary")
+		}
+	}
+	filter := newSecretStream(secret)
+	var output []byte
+	for _, value := range []byte(secret + secret) {
+		output = append(output, filter.Feed([]byte{value})...)
+	}
+	output = append(output, filter.Finish()...)
+	if string(output) != secretMask+secretMask {
+		t.Fatal("single-byte/repeated echo was not masked")
+	}
+	filter = newSecretStream(secret)
+	output = filter.Feed([]byte(secret[:len(secret)-1]))
+	output = append(output, filter.Finish()...)
+	if string(output) != secretMask {
+		t.Fatal("EOF released a credential prefix")
+	}
+}
+
+type oneByteReadConn struct{ net.Conn }
+
+func (connection oneByteReadConn) Read(buffer []byte) (int, error) {
+	if len(buffer) > 1 {
+		buffer = buffer[:1]
+	}
+	return connection.Conn.Read(buffer)
+}
+
+type privateLoginFixture struct {
+	manager  *Manager
+	recorder *memoryRecorder
+	peer     net.Conn
+	secret   string
+	writes   sync.Mutex
+	sends    atomic.Int32
+	quiet    atomic.Bool // receive credentials without the echo/shell reply
+	outputMu sync.Mutex
+	output   []byte
+}
+
+func newPrivateLoginFixture(t *testing.T) *privateLoginFixture {
+	t.Helper()
+	return newPrivateLoginFixtureReads(t, true)
+}
+
+// oneByte forces every chunk boundary; false keeps each device write as one
+// read, the shape where several prompts arrive together.
+func newPrivateLoginFixtureReads(t *testing.T, oneByte bool) *privateLoginFixture {
+	t.Helper()
+	recorder := &memoryRecorder{}
+	manager := NewManager(recorder)
+	client, peer := net.Pipe()
+	manager.dialNetwork = func(context.Context, string, string) (net.Conn, error) {
+		if oneByte {
+			return oneByteReadConn{client}, nil
+		}
+		return client, nil
+	}
+	f := &privateLoginFixture{manager: manager, recorder: recorder, peer: peer, secret: privateFixtureSecret(t)}
+	t.Cleanup(func() { _ = manager.Close(); _ = peer.Close() })
+	group := domain.HardwareGroup{ID: "board", Mode: domain.HardwareModeNetwork, Network: domain.HardwareNetworkConfig{Host: "127.0.0.1", Port: 12345, Protocol: domain.HardwareProtocolRaw}, Access: domain.HardwareAccessReadWrite}
+	if _, err := manager.Connect(context.Background(), ConnectRequest{TaskID: "task", HardwareID: "board", Transport: "network", Group: group, Password: f.secret}); err != nil {
+		t.Fatal("fake transport connect failed")
+	}
+	live, unsubscribe, err := manager.Subscribe("task", "board", "network")
+	if err != nil {
+		t.Fatal(err)
+	}
+	t.Cleanup(unsubscribe)
+	go func() {
+		for event := range live {
+			if event.Type == "output" {
+				f.outputMu.Lock()
+				f.output = append(f.output, event.Data...)
+				f.outputMu.Unlock()
+			}
+		}
+	}()
+	go func() {
+		reader := bufio.NewReader(peer)
+		for {
+			line, err := reader.ReadString('\n')
+			if err != nil {
+				return
+			}
+			if strings.TrimSpace(line) == f.secret {
+				f.sends.Add(1)
+				if !f.quiet.Load() {
+					f.write(t, []byte("echo:"+f.secret+"\n$ "))
+				}
+			}
+		}
+	}()
+	return f
+}
+
+func (f *privateLoginFixture) write(t *testing.T, data []byte) {
+	t.Helper()
+	f.writes.Lock()
+	defer f.writes.Unlock()
+	_ = f.peer.SetWriteDeadline(time.Now().Add(3 * time.Second))
+	if _, err := f.peer.Write(data); err != nil {
+		return
+	}
+}
+
+func (f *privateLoginFixture) waitHistory(t *testing.T, marker string) {
+	t.Helper()
+	deadline := time.Now().Add(3 * time.Second)
+	for {
+		f.recorder.mu.Lock()
+		var joined strings.Builder
+		for _, event := range f.recorder.events {
+			if event.Direction == "rx" {
+				joined.WriteString(event.Data)
+			}
+		}
+		found := strings.Contains(joined.String(), marker)
+		leaked := strings.Contains(joined.String(), f.secret)
+		f.recorder.mu.Unlock()
+		if leaked {
+			t.Fatal("credential persisted in rx history")
+		}
+		if found {
+			return
+		}
+		if time.Now().After(deadline) {
+			t.Fatal("fake device marker did not reach sanitized history")
+		}
+		time.Sleep(time.Millisecond)
+	}
+}
+
+func (f *privateLoginFixture) waitLive(t *testing.T, marker string) {
+	t.Helper()
+	deadline := time.Now().Add(3 * time.Second)
+	for {
+		f.outputMu.Lock()
+		found := bytes.Contains(f.output, []byte(marker))
+		f.outputMu.Unlock()
+		if found {
+			return
+		}
+		if time.Now().After(deadline) {
+			t.Fatal("sanitized live subscriber did not receive marker")
+		}
+		time.Sleep(time.Millisecond)
+	}
+}
+
+func (f *privateLoginFixture) assertPrivate(t *testing.T) {
+	t.Helper()
+	f.recorder.mu.Lock()
+	for _, event := range f.recorder.events {
+		if strings.Contains(event.Data, f.secret) {
+			f.recorder.mu.Unlock()
+			t.Fatal("credential persisted in a terminal record")
+		}
+	}
+	f.recorder.mu.Unlock()
+	f.outputMu.Lock()
+	leaked := bytes.Contains(f.output, []byte(f.secret))
+	f.outputMu.Unlock()
+	if leaked {
+		t.Fatal("credential reached the live subscriber")
+	}
+}
+
+func TestLoginEchoIsMaskedAndAuthenticatedLoginCannotRearm(t *testing.T) {
+	f := newPrivateLoginFixture(t)
+	if _, err := f.manager.Login("task", "board", "network", LoginOptions{TimeoutSeconds: 3, Retries: 3}); err != nil {
+		t.Fatal(err)
+	}
+	f.write(t, []byte("password:"))
+	waitForHardwareLoginStatusAt(t, f.manager, "task", "board", "network", "authenticated")
+	f.waitHistory(t, secretMask)
+	if f.sends.Load() != 1 {
+		t.Fatal("initial login did not send exactly one credential")
+	}
+	if _, err := f.manager.Login("task", "board", "network", LoginOptions{Wakeup: true, Retries: 3}); err != nil {
+		t.Fatal(err)
+	}
+	f.write(t, []byte("password:\nPOST-AUTH-MARKER\n"))
+	f.waitHistory(t, "POST-AUTH-MARKER")
+	f.waitLive(t, "POST-AUTH-MARKER")
+	if f.sends.Load() != 1 {
+		t.Fatal("authenticated replay sent the credential again")
+	}
+	status := f.manager.Status("task", "board", "network", false)
+	if status.LoginStatus != "authenticated" {
+		t.Fatal("idempotent login reset authenticated state")
+	}
+	f.assertPrivate(t)
+}
+
+func TestLoginFailureTimeoutAndCancelDisarmAndPermitExplicitBudgetedRetry(t *testing.T) {
+	for _, outcome := range []string{"failed", "timeout", "cancelled"} {
+		t.Run(outcome, func(t *testing.T) {
+			f := newPrivateLoginFixture(t)
+			if _, err := f.manager.Login("task", "board", "network", LoginOptions{TimeoutSeconds: 3, Retries: 3}); err != nil {
+				t.Fatal(err)
+			}
+			switch outcome {
+			case "failed":
+				f.write(t, []byte("access denied\n"))
+			case "cancelled":
+				if _, err := f.manager.Login("task", "board", "network", LoginOptions{Cancel: true}); err != nil {
+					t.Fatal(err)
+				}
+			}
+			waitForHardwareLoginStatusAt(t, f.manager, "task", "board", "network", outcome)
+			f.write(t, []byte("password:\nDISARMED-MARKER\n"))
+			f.waitHistory(t, "DISARMED-MARKER")
+			f.waitLive(t, "DISARMED-MARKER")
+			if f.sends.Load() != 0 {
+				t.Fatal("a disarmed attempt sent a credential")
+			}
+			if _, err := f.manager.Login("task", "board", "network", LoginOptions{TimeoutSeconds: 3, Retries: 3}); err != nil {
+				t.Fatal("explicit retry within budget was refused")
+			}
+			f.write(t, []byte("password:"))
+			waitForHardwareLoginStatusAt(t, f.manager, "task", "board", "network", "authenticated")
+			if f.sends.Load() != 1 {
+				t.Fatal("explicit retry did not authenticate exactly once")
+			}
+			f.assertPrivate(t)
+		})
+	}
+}
+
+func TestFailureAndFreshPromptInOneReadReportFailureWithoutResend(t *testing.T) {
+	f := newPrivateLoginFixtureReads(t, false)
+	f.quiet.Store(true)
+	if _, err := f.manager.Login("task", "board", "network", LoginOptions{TimeoutSeconds: 30, Retries: 3}); err != nil {
+		t.Fatal(err)
+	}
+	f.write(t, []byte("password:"))
+	waitForHardwareLoginStatusAt(t, f.manager, "task", "board", "network", "credentials_sent")
+	deadline := time.Now().Add(3 * time.Second)
+	for f.sends.Load() != 1 {
+		if time.Now().After(deadline) {
+			t.Fatal("fake device did not receive the credential")
+		}
+		time.Sleep(time.Millisecond)
+	}
+	f.write(t, []byte("\r\nLogin incorrect\r\n\r\nboard login: password:"))
+	waitForHardwareLoginStatusAt(t, f.manager, "task", "board", "network", "failed")
+	if f.sends.Load() != 1 {
+		t.Fatal("failure with a fresh prompt resent the credential")
+	}
+	f.assertPrivate(t)
+}
+
+func TestBannerMentioningFailureBeforeAnyCredentialDoesNotAbortLogin(t *testing.T) {
+	f := newPrivateLoginFixtureReads(t, false)
+	if _, err := f.manager.Login("task", "board", "network", LoginOptions{TimeoutSeconds: 30, Retries: 3}); err != nil {
+		t.Fatal(err)
+	}
+	f.write(t, []byte("NOTICE: access denied to unauthorized users\r\npassword:"))
+	waitForHardwareLoginStatusAt(t, f.manager, "task", "board", "network", "authenticated")
+	if f.sends.Load() != 1 {
+		t.Fatal("banner text changed the credential flow")
+	}
+	f.assertPrivate(t)
+}
+
+func waitForHardwareLoginStatusAt(t *testing.T, manager *Manager, task, id, transport, want string) {
+	t.Helper()
+	deadline := time.Now().Add(5 * time.Second)
+	for manager.Status(task, id, transport, false).LoginStatus != want {
+		if time.Now().After(deadline) {
+			t.Fatal("login did not reach expected state")
+		}
+		time.Sleep(time.Millisecond)
+	}
+}
+
+func TestHistoryDefenseMasksRetainedSplitEventsWithoutChangingCursors(t *testing.T) {
+	secret := privateFixtureSecret(t)
+	cut := len(secret) / 2
+	page := domain.HardwareIOPage{LatestSequence: 3, HasMore: true, Items: []domain.HardwareIOEvent{{Sequence: 1, Transport: "network", Direction: "rx", Data: secret[:cut]}, {Sequence: 2, Transport: "serial", Direction: "rx", Data: "ordinary"}, {Sequence: 3, Transport: "network", Direction: "rx", Data: secret[cut:]}}}
+	view := RedactHistory(page, secret)
+	var joined strings.Builder
+	for _, item := range view.Items {
+		joined.WriteString(item.Data)
+	}
+	if strings.Contains(joined.String(), secret) || !strings.Contains(joined.String(), secretMask) || view.LatestSequence != page.LatestSequence || !view.HasMore {
+		t.Fatal("history defense leaked credential or changed cursors")
+	}
+	if page.Items[0].Data != secret[:cut] {
+		t.Fatal("read projection modified its input records")
+	}
+}
+
+func TestHistoryDefenseCannotReassembleCredentialAcrossPages(t *testing.T) {
+	secret := privateFixtureSecret(t)
+	// Fixture text has no lowercase hex, so any hex byte in a view is a leak.
+	leaked := func(value string) bool { return strings.ContainsAny(value, "0123456789abcdef") }
+	for _, direction := range []string{"rx", "tx"} {
+		for first := 1; first < len(secret); first++ {
+			for _, second := range []int{first, (first + len(secret)) / 2, len(secret) - 1, len(secret)} {
+				var items []domain.HardwareIOEvent
+				for _, part := range []string{"PRE:" + secret[:first], secret[first:second], secret[second:] + "\nPOST\n"} {
+					if part != "" {
+						items = append(items, domain.HardwareIOEvent{Sequence: int64(len(items) + 1), Transport: "network", Direction: direction, Data: part})
+					}
+				}
+				for size := 1; size <= len(items); size++ {
+					var joined strings.Builder
+					for start := 0; start < len(items); start += size {
+						window := domain.HardwareIOPage{Items: items[start:min(start+size, len(items))]}
+						for _, item := range RedactHistory(window, secret).Items {
+							joined.WriteString(item.Data)
+						}
+					}
+					if leaked(joined.String()) || !strings.HasPrefix(joined.String(), "PRE:") || !strings.HasSuffix(joined.String(), "\nPOST\n") {
+						t.Fatal("paged history revealed part of a credential or lost ordinary output")
+					}
+				}
+			}
+		}
+	}
+	keystrokes := make([]domain.HardwareIOEvent, 0, len(secret))
+	for index := range secret {
+		keystrokes = append(keystrokes, domain.HardwareIOEvent{Sequence: int64(index + 1), Transport: "network", Direction: "tx", Data: secret[index : index+1]})
+	}
+	for start := range keystrokes {
+		for _, item := range RedactHistory(domain.HardwareIOPage{Items: keystrokes[start : start+1]}, secret).Items {
+			if leaked(item.Data) {
+				t.Fatal("single keystroke page revealed a credential byte")
+			}
+		}
+	}
+	ordinary := domain.HardwareIOPage{Items: []domain.HardwareIOEvent{{Sequence: 9, Transport: "network", Direction: "rx", Data: "ORDINARY OUTPUT\n"}}}
+	if view := RedactHistory(ordinary, secret); view.Items[0].Data != "ORDINARY OUTPUT\n" {
+		t.Fatal("unrelated page start was masked")
+	}
+}
+
+type credentialErrorRecorder struct{ secret string }
+
+func (r credentialErrorRecorder) AppendHardwareIO(context.Context, domain.HardwareIOEvent) (int64, error) {
+	return 0, errors.New("fixture storage error " + r.secret)
+}
+
+func TestKnownCredentialDoesNotEscapeConnectionErrorsStatusOrLogs(t *testing.T) {
+	secret := privateFixtureSecret(t)
+	manager := NewManager(nil)
+	manager.openSSH = func(context.Context, string, string, string, string) (io.ReadWriteCloser, error) {
+		return nil, errors.New("fixture connect error " + secret)
+	}
+	group := domain.HardwareGroup{ID: "board", Mode: domain.HardwareModeNetwork, Network: domain.HardwareNetworkConfig{Host: "127.0.0.1", Port: 22, Protocol: domain.HardwareProtocolSSH}, Access: domain.HardwareAccessReadWrite}
+	_, err := manager.Connect(context.Background(), ConnectRequest{TaskID: "task", HardwareID: "board", Transport: "network", Group: group, Password: secret})
+	if err == nil || strings.Contains(err.Error(), secret) {
+		t.Fatal("connection error exposed credential")
+	}
+	var logs bytes.Buffer
+	manager = NewManager(credentialErrorRecorder{secret: secret})
+	manager.logger = slog.New(slog.NewTextHandler(&logs, nil))
+	client, peer := net.Pipe()
+	defer peer.Close()
+	defer manager.Close()
+	manager.dialNetwork = func(context.Context, string, string) (net.Conn, error) { return client, nil }
+	group.Network.Protocol = domain.HardwareProtocolRaw
+	status, err := manager.Connect(context.Background(), ConnectRequest{TaskID: "task", HardwareID: "board", Transport: "network", Group: group, Password: secret})
+	if err != nil || strings.Contains(status.RecordError, secret) || strings.Contains(logs.String(), secret) {
+		t.Fatal("recording failure exposed credential in status/log")
+	}
+}
+
+func TestDisconnectDisarmsWriterEvenWhileReadOnlyObserverRemains(t *testing.T) {
+	f := newPrivateLoginFixture(t)
+	group := domain.HardwareGroup{ID: "board", Mode: domain.HardwareModeNetwork, Network: domain.HardwareNetworkConfig{Host: "127.0.0.1", Port: 12345, Protocol: domain.HardwareProtocolRaw}, Access: domain.HardwareAccessReadWrite}
+	if _, err := f.manager.Connect(context.Background(), ConnectRequest{TaskID: "observer", HardwareID: "board", Transport: "network", Group: group, Password: f.secret, ReadOnly: true}); err != nil {
+		t.Fatal("observer fixture failed")
+	}
+	f.manager.Disconnect("task", "board", "network")
+	if !f.manager.Status("observer", "board", "network", true).Connected {
+		t.Fatal("disconnect dropped read-only observer connection")
+	}
+	f.write(t, []byte("password:\nOBSERVER-MARKER\n"))
+	f.waitHistory(t, "OBSERVER-MARKER")
+	deadline := time.Now().Add(3 * time.Second)
+	for f.manager.Status("observer", "board", "network", true).LoginStatus != "cancelled" {
+		if time.Now().After(deadline) {
+			t.Fatal("writer disconnect did not disarm login")
+		}
+		time.Sleep(time.Millisecond)
+	}
+	if f.sends.Load() != 0 {
+		t.Fatal("detached writer sent a credential")
+	}
+	if _, err := f.manager.Login("task", "board", "network", LoginOptions{}); !errors.Is(err, ErrNotConnected) {
+		t.Fatal("disconnected writer rearmed login")
+	}
+}
+
+func TestLoginReplayIsIdempotentAndExplicitRetriesHaveFiniteBudget(t *testing.T) {
+	f := newPrivateLoginFixture(t)
+	if _, err := f.manager.Login("task", "board", "network", LoginOptions{TimeoutSeconds: 3, Retries: 2}); err != nil {
+		t.Fatal(err)
+	}
+	f.manager.mu.Lock()
+	session := f.manager.attachments[attachmentKey("task", "board", "network")]
+	f.manager.mu.Unlock()
+	session.mu.RLock()
+	before := session.loginGeneration
+	session.mu.RUnlock()
+	if _, err := f.manager.Login("task", "board", "network", LoginOptions{Wakeup: true, Retries: 2}); err != nil {
+		t.Fatal(err)
+	}
+	session.mu.RLock()
+	after := session.loginGeneration
+	session.mu.RUnlock()
+	if before != after {
+		t.Fatal("duplicate in-progress login restarted attempt")
+	}
+	f.write(t, []byte("access denied\n"))
+	waitForHardwareLoginStatusAt(t, f.manager, "task", "board", "network", "failed")
+	if _, err := f.manager.Login("task", "board", "network", LoginOptions{TimeoutSeconds: 3, Retries: 2}); err != nil {
+		t.Fatal("eligible explicit retry rejected")
+	}
+	f.write(t, []byte("access denied\n"))
+	waitForHardwareLoginStatusAt(t, f.manager, "task", "board", "network", "failed")
+	if _, err := f.manager.Login("task", "board", "network", LoginOptions{Retries: 2}); !errors.Is(err, ErrLoginRetryLimit) {
+		t.Fatal("retry budget could be silently reset")
+	}
+	f.write(t, []byte("password:\nBUDGET-MARKER\n"))
+	f.waitHistory(t, "BUDGET-MARKER")
+	if f.sends.Load() != 0 {
+		t.Fatal("rejected retry sent a credential")
+	}
+	for _, options := range []LoginOptions{{TimeoutSeconds: 1}, {TimeoutSeconds: 301}, {Retries: 4}, {LineEnding: "invalid"}} {
+		if _, err := f.manager.Login("task", "board", "network", options); !errors.Is(err, ErrLoginOptionsInvalid) {
+			t.Fatal("manager accepted options outside published bounds")
+		}
+	}
+}
diff --git a/internal/hardware/login_state.go b/internal/hardware/login_state.go
new file mode 100644
index 0000000..86f93b0
--- /dev/null
+++ b/internal/hardware/login_state.go
@@ -0,0 +1,203 @@
+package hardware
+
+import (
+	"regexp"
+	"strings"
+	"time"
+
+	"github.com/ChinaKai/AHA2/internal/domain"
+)
+
+var loginANSI = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
+
+// stopLoginLocked invalidates every queued credential write and timer. Sent
+// markers remain as facts about the completed attempt; only a new explicit
+// eligible attempt resets them. Callers hold session.mu.
+func (session *terminalSession) stopLoginLocked(status, message string) {
+	session.loginGeneration++
+	session.loginArmed = false
+	session.loginImplicit = false
+	session.loginBuffer = ""
+	clear(session.promptDecoder.pending)
+	session.promptDecoder.pending = nil
+	if session.loginDone != nil {
+		close(session.loginDone)
+		session.loginDone = nil
+	}
+	session.loginStatus = status
+	session.loginError = message
+}
+
+func (session *terminalSession) armLoginLocked(options LoginOptions, key string, attempt int, implicit bool) {
+	session.stopLoginLocked("waiting", "")
+	session.loginOptions = options
+	session.loginOwner = key
+	session.loginAttempts = attempt
+	session.usernameSent = false
+	session.passwordSent = false
+	session.loginArmed = true
+	session.loginImplicit = implicit
+	session.loginDone = make(chan struct{})
+}
+
+func validLoginOptions(options LoginOptions) bool {
+	return (options.TimeoutSeconds == 0 || (options.TimeoutSeconds >= 3 && options.TimeoutSeconds <= 300)) && (options.Retries >= 0 && options.Retries <= 3) && (options.LineEnding == "" || options.LineEnding == "lf" || options.LineEnding == "cr" || options.LineEnding == "crlf")
+}
+
+// retry count is a connection-local explicit-attempt limit. A replayed prompt
+// never resends a credential. Failure/timeout/cancel disarms; a further explicit
+// login needs remaining budget (1 by default, at most 3) or a new connection.
+func (manager *Manager) Login(taskID, hardwareID, transport string, options LoginOptions) (domain.HardwareTerminalStatus, error) {
+	key := attachmentKey(taskID, hardwareID, transport)
+	manager.mu.Lock()
+	session := manager.attachments[key]
+	manager.mu.Unlock()
+	if session == nil {
+		return domain.HardwareTerminalStatus{}, ErrNotConnected
+	}
+	if !validLoginOptions(options) {
+		return domain.HardwareTerminalStatus{}, ErrLoginOptionsInvalid
+	}
+	session.writeMu.Lock()
+	defer session.writeMu.Unlock()
+	session.mu.Lock()
+	attachment, ok := session.attachments[key]
+	if !ok || session.closing || session.status != "running" {
+		session.mu.Unlock()
+		return domain.HardwareTerminalStatus{}, ErrNotConnected
+	}
+	if attachment.readOnly {
+		session.mu.Unlock()
+		return domain.HardwareTerminalStatus{}, ErrReadOnly
+	}
+	if options.Cancel {
+		if session.loginArmed {
+			session.stopLoginLocked("cancelled", "")
+		}
+		status := session.statusForLocked(taskID, hardwareID, attachment.readOnly)
+		session.mu.Unlock()
+		return status, nil
+	}
+	if session.loginStatus == "authenticated" || (session.loginArmed && (!session.loginImplicit || session.usernameSent || session.passwordSent)) {
+		status := session.statusForLocked(taskID, hardwareID, attachment.readOnly)
+		session.mu.Unlock()
+		return status, nil
+	}
+	if session.username == "" && session.password == "" {
+		session.mu.Unlock()
+		return domain.HardwareTerminalStatus{}, ErrLoginCredentialsNotConfigured
+	}
+	normalized := normalizeLoginOptions(options)
+	attempt := session.loginAttempts + 1
+	if session.loginImplicit {
+		attempt = session.loginAttempts
+	}
+	if attempt < 1 {
+		attempt = 1
+	}
+	if attempt > normalized.Retries {
+		session.mu.Unlock()
+		return domain.HardwareTerminalStatus{}, ErrLoginRetryLimit
+	}
+	session.armLoginLocked(normalized, key, attempt, false)
+	generation, done := session.loginGeneration, session.loginDone
+	session.mu.Unlock()
+	if normalized.Wakeup {
+		if err := session.writeLoginLocked(generation, key, loginLineEnding(normalized.LineEnding), "<wakeup>"); err != nil {
+			return domain.HardwareTerminalStatus{}, err
+		}
+	}
+	go session.loginTimeout(generation, normalized.TimeoutSeconds, done)
+	return session.statusFor(taskID, hardwareID, attachment.readOnly), nil
+}
+
+func (session *terminalSession) maybeAutoLogin(chunk []byte) {
+	session.mu.Lock()
+	if !session.loginArmed || session.closing {
+		session.mu.Unlock()
+		return
+	}
+	text, _ := session.promptDecoder.Decode(chunk)
+	session.loginBuffer = appendLoginBuffer(session.loginBuffer, text)
+	lower := strings.ToLower(loginANSI.ReplaceAllString(session.loginBuffer, ""))
+	options := session.loginOptions
+	// Credential and success prompts must end the buffered output, so text
+	// that merely mentions a prompt never triggers a credential write. A
+	// failure only disarms, so after a credential was sent (the buffer then
+	// holds only later output) it counts anywhere, including when a fresh
+	// login prompt arrives in the same read.
+	sent := session.usernameSent || session.passwordSent
+	if matchesLoginPrompt(lower, options.FailurePrompts) || (sent && containsLoginPrompt(lower, options.FailurePrompts)) {
+		session.stopLoginLocked("failed", "hardware reported a login failure")
+		session.mu.Unlock()
+		return
+	}
+	if matchesLoginPrompt(lower, options.SuccessPrompts) {
+		session.stopLoginLocked("authenticated", "")
+		session.mu.Unlock()
+		return
+	}
+	sendUsername := session.username != "" && !session.usernameSent && matchesLoginPrompt(lower, options.UsernamePrompts)
+	sendPassword := session.password != "" && !session.passwordSent && matchesLoginPrompt(lower, options.PasswordPrompts)
+	if sendUsername {
+		session.usernameSent = true
+		session.loginStatus = "username_sent"
+	}
+	if sendPassword {
+		session.passwordSent = true
+		session.loginStatus = "credentials_sent"
+	}
+	if sendUsername || sendPassword {
+		session.loginBuffer = ""
+	}
+	generation, key := session.loginGeneration, session.loginOwner
+	username, password := session.username, session.password
+	session.mu.Unlock()
+	ending := loginLineEnding(options.LineEnding)
+	if sendUsername {
+		_ = session.writeLogin(generation, key, username+ending, username+displayLineEnding(options.LineEnding))
+	}
+	if sendPassword {
+		_ = session.writeLogin(generation, key, password+ending, "<password>"+displayLineEnding(options.LineEnding))
+	}
+}
+
+func (session *terminalSession) writeLogin(generation int, key, value, display string) error {
+	session.writeMu.Lock()
+	defer session.writeMu.Unlock()
+	return session.writeLoginLocked(generation, key, value, display)
+}
+
+func (session *terminalSession) writeLoginLocked(generation int, key, value, display string) error {
+	session.mu.RLock()
+	attachment, present := session.attachments[key]
+	allowed := session.loginArmed && !session.closing && session.loginGeneration == generation && session.loginOwner == key && present && !attachment.readOnly
+	session.mu.RUnlock()
+	if !allowed {
+		return nil
+	}
+	payload := []byte(value)
+	if session.codec != nil {
+		payload = session.codec.Encode(payload)
+	}
+	if err := session.writeRawLocked(payload); err != nil {
+		return err
+	}
+	session.record("tx", display, "auto-login", nil, nil)
+	return nil
+}
+
+func (session *terminalSession) loginTimeout(generation, seconds int, done <-chan struct{}) {
+	timer := time.NewTimer(time.Duration(seconds) * time.Second)
+	defer timer.Stop()
+	select {
+	case <-done:
+		return
+	case <-timer.C:
+	}
+	session.mu.Lock()
+	defer session.mu.Unlock()
+	if session.loginArmed && session.loginGeneration == generation {
+		session.stopLoginLocked("timeout", "hardware login timed out")
+	}
+}
diff --git a/internal/hardware/manager.go b/internal/hardware/manager.go
index c992d78..dde954f 100644
--- a/internal/hardware/manager.go
+++ b/internal/hardware/manager.go
@@ -1,6 +1,7 @@
 package hardware
 
 import (
+	"bytes"
 	"context"
 	"encoding/base64"
 	"encoding/hex"
@@ -38,6 +39,7 @@ type ConnectRequest struct {
 }
 
 type LoginOptions struct {
+	Cancel          bool     `json:"cancel,omitempty"`
 	UsernamePrompts []string `json:"username_prompts"`
 	PasswordPrompts []string `json:"password_prompts"`
 	SuccessPrompts  []string `json:"success_prompts"`
@@ -67,19 +69,21 @@ type liveSubscriber struct {
 }
 
 type terminalSession struct {
-	manager   *Manager
-	targetKey string
-	endpoint  string
-	transport string
-	protocol  string
-	sshAuth   string
-	username  string
-	password  string
-	baudrate  int
-	conn      io.ReadWriteCloser
-	codec     *telnetCodec
-	decoder   streamDecoder
-	startedAt time.Time
+	manager        *Manager
+	targetKey      string
+	endpoint       string
+	transport      string
+	protocol       string
+	sshAuth        string
+	username       string
+	password       string
+	baudrate       int
+	conn           io.ReadWriteCloser
+	codec          *telnetCodec
+	decoder        streamDecoder
+	promptDecoder  streamDecoder
+	outputRedactor *secretStream
+	startedAt      time.Time
 
 	mu              sync.RWMutex
 	writeMu         sync.Mutex
@@ -98,6 +102,10 @@ type terminalSession struct {
 	loginOptions    LoginOptions
 	loginAttempts   int
 	loginGeneration int
+	loginArmed      bool
+	loginImplicit   bool
+	loginOwner      string
+	loginDone       chan struct{}
 	// recordError is the latest storage failure, cleared on the next
 	// successfully stored event.
 	recordError string
@@ -213,7 +221,7 @@ func (manager *Manager) Connect(ctx context.Context, request ConnectRequest) (do
 	}
 	if err != nil {
 		manager.mu.Unlock()
-		return domain.HardwareTerminalStatus{}, fmt.Errorf("connect %s: %w", endpoint, err)
+		return domain.HardwareTerminalStatus{}, fmt.Errorf("connect %s: %w", endpoint, redactHardwareError(err, request.Password))
 	}
 	if resolved, ok := connection.(interface{ ResolvedEndpoint() string }); ok {
 		if value := strings.TrimSpace(resolved.ResolvedEndpoint()); value != "" {
@@ -231,15 +239,19 @@ func (manager *Manager) Connect(ctx context.Context, request ConnectRequest) (do
 		subscribers: map[string]liveSubscriber{},
 	}
 	session.loginOptions = normalizeLoginOptions(LoginOptions{})
+	session.outputRedactor = newSecretStream(session.password)
 	if protocol == domain.HardwareProtocolSSH || protocol == domain.HardwareProtocolADB {
 		session.loginStatus = "authenticated"
 	} else if session.username != "" || session.password != "" {
-		session.loginStatus = "waiting"
-		session.loginAttempts = 1
-		session.loginGeneration = 1
+		if request.ReadOnly {
+			session.loginStatus = "read_only"
+		} else {
+			session.armLoginLocked(session.loginOptions, attachmentKey, 1, true)
+		}
 	} else {
 		session.loginStatus = "not_configured"
 	}
+
 	if protocol == domain.HardwareProtocolTelnet {
 		session.codec = newTelnetCodec(100, 28)
 	}
@@ -252,60 +264,14 @@ func (manager *Manager) Connect(ctx context.Context, request ConnectRequest) (do
 		_ = session.writeRaw(session.codec.InitialNegotiation())
 	}
 	session.mu.RLock()
-	loginWaiting := session.loginStatus == "waiting"
+	loginWaiting, generation, seconds, done := session.loginArmed, session.loginGeneration, session.loginOptions.TimeoutSeconds, session.loginDone
 	session.mu.RUnlock()
 	go session.readLoop()
 	if loginWaiting {
-		go session.loginTimeout(session.loginGeneration, session.loginOptions.TimeoutSeconds)
+		go session.loginTimeout(generation, seconds, done)
 	}
-	return session.statusFor(request.TaskID, request.HardwareID, request.ReadOnly), nil
-}
 
-func (manager *Manager) Login(taskID, hardwareID, transport string, options LoginOptions) (domain.HardwareTerminalStatus, error) {
-	key := attachmentKey(taskID, hardwareID, transport)
-	manager.mu.Lock()
-	session := manager.attachments[key]
-	manager.mu.Unlock()
-	if session == nil {
-		return domain.HardwareTerminalStatus{}, ErrNotConnected
-	}
-	session.mu.Lock()
-	if session.protocol == domain.HardwareProtocolSSH {
-		status := session.statusForLocked(taskID, hardwareID, false)
-		session.mu.Unlock()
-		return status, nil
-	}
-	attachment, ok := session.attachments[key]
-	if !ok || session.closing {
-		session.mu.Unlock()
-		return domain.HardwareTerminalStatus{}, ErrNotConnected
-	}
-	if attachment.readOnly {
-		session.mu.Unlock()
-		return domain.HardwareTerminalStatus{}, ErrReadOnly
-	}
-	if session.username == "" && session.password == "" {
-		session.mu.Unlock()
-		return domain.HardwareTerminalStatus{}, fmt.Errorf("hardware login credentials are not configured")
-	}
-	session.loginOptions = normalizeLoginOptions(options)
-	session.usernameSent = false
-	session.passwordSent = false
-	session.loginBuffer = ""
-	session.loginStatus = "waiting"
-	session.loginError = ""
-	session.loginAttempts = 1
-	session.loginGeneration++
-	generation := session.loginGeneration
-	wakeup := session.loginOptions.Wakeup
-	ending := loginLineEnding(session.loginOptions.LineEnding)
-	timeout := session.loginOptions.TimeoutSeconds
-	session.mu.Unlock()
-	if wakeup {
-		session.automaticSend(ending, "<wakeup>")
-	}
-	go session.loginTimeout(generation, timeout)
-	return session.statusFor(taskID, hardwareID, attachment.readOnly), nil
+	return session.statusFor(request.TaskID, request.HardwareID, request.ReadOnly), nil
 }
 
 func (manager *Manager) Status(taskID, hardwareID, transport string, readOnly bool) domain.HardwareTerminalStatus {
@@ -332,8 +298,9 @@ func (manager *Manager) Send(taskID, hardwareID, transport, data, encoding, sour
 	}
 	session.mu.RLock()
 	item, ok := session.attachments[key]
+	connected := !session.closing && session.status == "running"
 	session.mu.RUnlock()
-	if !ok {
+	if !ok || !connected {
 		return ErrNotConnected
 	}
 	if item.readOnly {
@@ -348,21 +315,21 @@ func (manager *Manager) Send(taskID, hardwareID, transport, data, encoding, sour
 		payload = []byte(data)
 	case "hex":
 		if transport != domain.HardwareTransportSerial {
-			return fmt.Errorf("hex input is supported for serial only")
+			return fmt.Errorf("%w: hex is supported for serial only", ErrHardwareEncodingInvalid)
 		}
 		var err error
 		payload, err = decodeHex(data)
 		if err != nil {
-			return err
+			return fmt.Errorf("%w: hex data is invalid", ErrHardwareEncodingInvalid)
 		}
 	case "base64":
 		var err error
 		payload, err = base64.StdEncoding.DecodeString(data)
 		if err != nil {
-			return fmt.Errorf("invalid base64 input: %w", err)
+			return fmt.Errorf("%w: base64 data is invalid", ErrHardwareEncodingInvalid)
 		}
 	default:
-		return fmt.Errorf("unsupported encoding %q", encoding)
+		return fmt.Errorf("%w: unsupported encoding", ErrHardwareEncodingInvalid)
 	}
 	if len(payload) == 0 {
 		return fmt.Errorf("hardware input is empty")
@@ -520,6 +487,9 @@ func (manager *Manager) Close() error {
 func (manager *Manager) detachLocked(key string, session *terminalSession) {
 	delete(manager.attachments, key)
 	session.mu.Lock()
+	if session.loginOwner == key && session.loginArmed {
+		session.stopLoginLocked("cancelled", "")
+	}
 	delete(session.attachments, key)
 	remaining := len(session.attachments)
 	session.mu.Unlock()
@@ -545,13 +515,20 @@ func (session *terminalSession) statusForLocked(taskID, hardwareID string, readO
 	return domain.HardwareTerminalStatus{
 		TaskID: taskID, HardwareID: hardwareID, Transport: session.transport,
 		Endpoint: session.endpoint, Status: session.status, Connected: !session.closing && session.status == "running",
-		ReadOnly: readOnly, Error: session.lastError, StartedAt: session.startedAt, UpdatedAt: session.updatedAt,
-		LoginStatus: session.loginStatus, LoginError: session.loginError,
-		RecordError: session.recordError,
+		ReadOnly: readOnly, Error: redactSecretText(session.lastError, session.password), StartedAt: session.startedAt, UpdatedAt: session.updatedAt,
+		LoginStatus: session.loginStatus, LoginError: redactSecretText(session.loginError, session.password),
+		RecordError: redactSecretText(session.recordError, session.password),
 	}
 }
 
 func (session *terminalSession) readLoop() {
+	defer func() {
+		if pending := session.outputRedactor.Finish(); len(pending) > 0 {
+			if text, raw := session.decoder.Decode(pending); len(raw) > 0 {
+				session.record("rx", text, "runtime", raw, nil)
+			}
+		}
+	}()
 	buffer := make([]byte, 4096)
 	for {
 		count, err := session.conn.Read(buffer)
@@ -564,9 +541,9 @@ func (session *terminalSession) readLoop() {
 					_ = session.writeRaw(reply)
 				}
 			}
-			if text, terminalData := session.decoder.Decode(payload); len(terminalData) > 0 {
+			session.maybeAutoLogin(payload)
+			if text, terminalData := session.decoder.Decode(session.outputRedactor.Feed(payload)); len(terminalData) > 0 {
 				session.record("rx", text, "runtime", terminalData, nil)
-				session.maybeAutoLogin(text)
 			}
 		}
 		if err != nil {
@@ -579,65 +556,6 @@ func (session *terminalSession) readLoop() {
 	}
 }
 
-func (session *terminalSession) maybeAutoLogin(text string) {
-	session.mu.Lock()
-	session.loginBuffer = appendLoginBuffer(session.loginBuffer, text)
-	lower := strings.ToLower(session.loginBuffer)
-	options := session.loginOptions
-	username := session.username
-	password := session.password
-	if matchesLoginPrompt(lower, options.FailurePrompts) {
-		session.loginStatus = "failed"
-		session.loginError = "hardware reported a login failure"
-		session.mu.Unlock()
-		return
-	}
-	if matchesLoginPrompt(lower, options.SuccessPrompts) {
-		session.loginStatus = "authenticated"
-		session.loginError = ""
-		session.mu.Unlock()
-		return
-	}
-	sendUsername := username != "" && !session.usernameSent && matchesLoginPrompt(lower, options.UsernamePrompts)
-	sendPassword := password != "" && !session.passwordSent && matchesLoginPrompt(lower, options.PasswordPrompts)
-	if password != "" && session.passwordSent && matchesLoginPrompt(lower, options.PasswordPrompts) && session.loginAttempts < options.Retries {
-		session.passwordSent = false
-		session.loginAttempts++
-		sendPassword = true
-	}
-	if sendUsername {
-		session.usernameSent = true
-		session.loginStatus = "username_sent"
-	}
-	if sendPassword {
-		session.passwordSent = true
-		session.loginStatus = "credentials_sent"
-	}
-	if sendUsername || sendPassword {
-		session.loginBuffer = ""
-	}
-	ending := loginLineEnding(options.LineEnding)
-	session.mu.Unlock()
-	if sendUsername {
-		session.automaticSend(username+ending, username+displayLineEnding(options.LineEnding))
-	}
-	if sendPassword {
-		session.automaticSend(password+ending, "<password>"+displayLineEnding(options.LineEnding))
-	}
-}
-
-func (session *terminalSession) loginTimeout(generation, seconds int) {
-	timer := time.NewTimer(time.Duration(seconds) * time.Second)
-	defer timer.Stop()
-	<-timer.C
-	session.mu.Lock()
-	defer session.mu.Unlock()
-	if generation == session.loginGeneration && session.loginStatus != "authenticated" && session.loginStatus != "failed" {
-		session.loginStatus = "timeout"
-		session.loginError = "hardware login timed out"
-	}
-}
-
 func normalizeLoginOptions(options LoginOptions) LoginOptions {
 	if len(options.UsernamePrompts) == 0 {
 		options.UsernamePrompts = []string{"login:", "username:"}
@@ -669,6 +587,15 @@ func normalizeLoginOptions(options LoginOptions) LoginOptions {
 }
 
 func matchesLoginPrompt(lower string, prompts []string) bool {
+	for _, prompt := range prompts {
+		if value := strings.ToLower(strings.TrimSpace(prompt)); value != "" && strings.HasSuffix(strings.TrimSpace(lower), value) {
+			return true
+		}
+	}
+	return false
+}
+
+func containsLoginPrompt(lower string, prompts []string) bool {
 	for _, prompt := range prompts {
 		if value := strings.ToLower(strings.TrimSpace(prompt)); value != "" && strings.Contains(lower, value) {
 			return true
@@ -707,16 +634,6 @@ func displayLineEnding(value string) string {
 	}
 }
 
-func (session *terminalSession) automaticSend(value, display string) {
-	payload := []byte(value)
-	if session.codec != nil {
-		payload = session.codec.Encode(payload)
-	}
-	if session.writeRaw(payload) == nil {
-		session.record("tx", display, "auto-login", nil, nil)
-	}
-}
-
 func (session *terminalSession) sendPayload(payload []byte, display, source string) error {
 	wire := payload
 	if session.codec != nil {
@@ -732,16 +649,20 @@ func (session *terminalSession) sendPayload(payload []byte, display, source stri
 func (session *terminalSession) writeRaw(payload []byte) error {
 	session.writeMu.Lock()
 	defer session.writeMu.Unlock()
+	return session.writeRawLocked(payload)
+}
+
+func (session *terminalSession) writeRawLocked(payload []byte) error {
 	for len(payload) > 0 {
 		count, err := session.conn.Write(payload)
 		if err != nil {
 			session.fail(err)
-			return err
+			return redactHardwareError(err, session.password)
 		}
 		if count <= 0 {
 			err = io.ErrShortWrite
 			session.fail(err)
-			return err
+			return redactHardwareError(err, session.password)
 		}
 		payload = payload[count:]
 	}
@@ -749,6 +670,10 @@ func (session *terminalSession) writeRaw(payload []byte) error {
 }
 
 func (session *terminalSession) record(direction, data, source string, raw []byte, only *attachment) {
+	data = redactSecretText(data, session.password)
+	if len(raw) > 0 && session.password != "" {
+		raw = bytes.ReplaceAll(raw, []byte(session.password), []byte(secretMaskFor(session.password)))
+	}
 	if direction == "rx" {
 		if data == "" && len(raw) == 0 {
 			return
@@ -798,10 +723,11 @@ func (session *terminalSession) fail(err error) {
 		return
 	}
 	session.status = "error"
-	session.lastError = err.Error()
+	session.lastError = redactSecretText(err.Error(), session.password)
+	session.stopLoginLocked("failed", "hardware connection failed")
 	session.updatedAt = session.manager.now().UTC()
 	session.mu.Unlock()
-	session.record("system", "hardware terminal error: "+err.Error(), "runtime", nil, nil)
+	session.record("system", "hardware terminal error: "+redactSecretText(err.Error(), session.password), "runtime", nil, nil)
 	session.broadcastStatus()
 	_ = session.conn.Close()
 }
@@ -812,6 +738,7 @@ func (session *terminalSession) close(reason string) {
 		session.mu.Unlock()
 		return
 	}
+	session.stopLoginLocked("cancelled", "")
 	session.closing = true
 	session.status = "disconnected"
 	session.updatedAt = session.manager.now().UTC()
@@ -989,6 +916,7 @@ func (session *terminalSession) noteRecordResult(target attachment, direction st
 		session.mu.Unlock()
 		return
 	}
+	err = redactHardwareError(err, session.password)
 	session.recordError = "terminal output could not be saved: " + err.Error()
 	session.mu.Unlock()
 	manager.recordFailures.Add(1)
diff --git a/internal/hardware/manager_test.go b/internal/hardware/manager_test.go
index ba99eec..32c8512 100644
--- a/internal/hardware/manager_test.go
+++ b/internal/hardware/manager_test.go
@@ -42,7 +42,7 @@ func TestBase64FallbackInput(t *testing.T) {
 	manager := NewManager(nil)
 	key := attachmentKey("task-1", "board", "serial")
 	session := &terminalSession{
-		manager: manager, conn: device, transport: "serial",
+		manager: manager, conn: device, transport: "serial", status: "running",
 		attachments: map[string]attachment{
 			key: {taskID: "task-1", hardwareID: "board"},
 		},
diff --git a/internal/hardware/secret_stream.go b/internal/hardware/secret_stream.go
new file mode 100644
index 0000000..fb302d8
--- /dev/null
+++ b/internal/hardware/secret_stream.go
@@ -0,0 +1,154 @@
+package hardware
+
+import (
+	"errors"
+	"strings"
+)
+
+const secretMask = "••••"
+
+// secretStream holds only a possible credential prefix. Nothing in that
+// pending prefix may be recorded or broadcast before the next read resolves
+// it. KMP fallback makes adversarial overlapping input linear in stream size.
+type secretStream struct {
+	secret   []byte
+	fallback []int
+	pending  []byte
+	mask     []byte
+	// forced counts leading input bytes whose preceding context is unknown
+	// (a retained-history page boundary). They may continue a credential that
+	// started before the page, so they are released only as a mask.
+	forced int
+}
+
+func newSecretStream(secret string) *secretStream {
+	filter := &secretStream{secret: []byte(secret), fallback: make([]int, len(secret)), mask: []byte(secretMaskFor(secret))}
+	for i, j := 1, 0; i < len(filter.secret); i++ {
+		for j > 0 && filter.secret[i] != filter.secret[j] {
+			j = filter.fallback[j-1]
+		}
+		if filter.secret[i] == filter.secret[j] {
+			j++
+		}
+		filter.fallback[i] = j
+	}
+	return filter
+}
+
+func (filter *secretStream) Feed(input []byte) []byte {
+	if len(filter.secret) == 0 {
+		return append([]byte(nil), input...)
+	}
+	output := make([]byte, 0, len(input))
+	forcedMasked := false
+	// Every input byte leaves the filter exactly once, in input order: either
+	// released here or consumed by a full match. Forced bytes are a leading
+	// run of that order, so the counter stays aligned with the bytes.
+	release := func(values []byte) {
+		for _, value := range values {
+			if filter.forced > 0 {
+				filter.forced--
+				if !forcedMasked {
+					output = append(output, filter.mask...)
+					forcedMasked = true
+				}
+				continue
+			}
+			output = append(output, value)
+		}
+	}
+	for _, value := range input {
+		for len(filter.pending) > 0 && filter.secret[len(filter.pending)] != value {
+			keep := filter.fallback[len(filter.pending)-1]
+			emit := len(filter.pending) - keep
+			release(filter.pending[:emit])
+			filter.pending = append(filter.pending[:0], filter.pending[emit:]...)
+		}
+		if filter.secret[len(filter.pending)] == value {
+			filter.pending = append(filter.pending, value)
+			if len(filter.pending) == len(filter.secret) {
+				filter.forced -= min(filter.forced, len(filter.pending))
+				output = append(output, filter.mask...)
+				filter.pending = filter.pending[:0]
+			}
+		} else {
+			release([]byte{value})
+		}
+	}
+	return output
+}
+
+// possibleContinuation returns how many leading bytes of a stream fragment
+// could finish a credential occurrence that began before the fragment: the
+// longest head that equals secret[k:] for some k>0 and either completes the
+// credential or runs to the end of the fragment (total bytes). Masking them
+// can hide a few ordinary bytes at a page start; it never reveals a tail.
+func possibleContinuation(secret []byte, head []byte, total int) int {
+	best := 0
+	for start := 1; start < len(secret); start++ {
+		rest := secret[start:]
+		count := 0
+		for count < len(rest) && count < len(head) && rest[count] == head[count] {
+			count++
+		}
+		if count > best && (count == len(rest) || count == total) {
+			best = count
+		}
+	}
+	return best
+}
+
+func (filter *secretStream) Finish() []byte {
+	if len(filter.pending) == 0 {
+		return nil
+	}
+	// Even a final partial echo is not released on EOF/disconnect.
+	clear(filter.pending)
+	filter.pending = nil
+	return append([]byte(nil), filter.mask...)
+}
+
+func secretMaskFor(secret string) string {
+	// The marker cannot itself contain a credential, even for short or unusual
+	// configured secrets. A rune absent from the secret also separates output
+	// fragments so replacing a match cannot join them into another match.
+	marker := rune('•')
+	for strings.ContainsRune(secret, marker) {
+		marker++
+		if marker > 0x10ffff {
+			marker = 0xe000
+		}
+	}
+	return strings.Repeat(string(marker), 4)
+}
+
+func redactSecretText(value, secret string) string {
+	if secret == "" {
+		return value
+	}
+	return strings.ReplaceAll(value, secret, secretMaskFor(secret))
+}
+
+type redactedHardwareError struct {
+	cause error
+	text  string
+}
+
+func (err *redactedHardwareError) Error() string { return err.text }
+func (err *redactedHardwareError) Unwrap() error { return err.cause }
+func redactHardwareError(err error, secret string) error {
+	if err == nil {
+		return nil
+	}
+	if secret == "" || !strings.Contains(err.Error(), secret) {
+		return err
+	}
+	return &redactedHardwareError{cause: err, text: redactSecretText(err.Error(), secret)}
+}
+
+var (
+	ErrLoginCredentialsNotConfigured = errors.New("hardware login credentials are not configured")
+	ErrLoginOptionsInvalid           = errors.New("hardware login options are invalid")
+	ErrLoginRetryLimit               = errors.New("hardware login retry limit reached; reconnect or explicitly raise retries within the allowed limit")
+	ErrHardwareEncodingInvalid       = errors.New("hardware input encoding is invalid")
+)
```
