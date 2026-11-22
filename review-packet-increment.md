# task-751 增量复审包（复用 task-752）

范围固定：`7bcc61f595aaf5dd6e45fbc9e41ab27f59f6705b..e183dfcc50ae0385b627ebe1862a0941ebd557e0`。分支 `aha/task_b0e0670c601199655542`；worktree `/srv/build/users/kaikai/kk-workspace/.aha2-worktrees/task_b0e0670c601199655542`；环境 Go 1.27.4 / Linux amd64。752 首轮 APPROVE 来自本轮 owner 消息；增量尚待复审，本 Agent 未关闭或合并 Task。

源码增量提交：`e183dfcc50ae0385b627ebe1862a0941ebd557e0`。本包随后单独提交，不改变该源码 head。原 `review-packet.md` 未覆盖，SHA-256 仍为 `b901fc4cd512c232690e607f83e8b10acb80dbb897196f8aa5f8b9b8b282061c`。

## S1：错误优先级收窄

7bcc61f 无条件优先返回 runFailure，导致真实 turn/completed(status=failed) 的额度等 provider 原因被普通 runner 收尾错误覆盖。现在仅未 started、真实扫描错误（errors.Is 标记，含包装）、意外退出 errCodexAppServerExited 优先取 runner 原因。已 started 且存在 session/provider 错误时，普通收尾错误不覆盖它；session 成功但 runner 失败仍返回 runner 错误。外部取消、idle/hard timeout 和既有 context.Canceled 分支优先级保持不变。

最小生产变更：codex_appserver.go 的两处错误返回顺序；local.go 将既有扫描错误判定导出为 IsProcessOutputError（不靠错误文案匹配）；wsl.go 及原 oversized 测试同步改名。未修改 SSH 实现、恢复协议或读取上限。

真实可失败回归：Go 子进程经 LocalRunner/scanOutput 发出失败 RPC，仅普通清理错误在进程结束后注入。还覆盖失败 Turn 后再出现真实 17 MiB 超限、成功 Turn 后 runner 失败和握手期间 runner 失败。前者保留 provider 原因；真实超限仍明确报告扫描错误，未退化成 exited/30 秒超时。

## S2：默认 4 MiB / stderr 策略的调用方与影响

静态核对范围是当前代码，未访问真实会话或生产凭据。OutputLimit 是保留尾部字节数，并不在 Scanner 前截断单行；例如 managed process 的 256 KiB tail 不能避免 4 MiB 行上限。

| 直接调用方 / 输出 | 可能合法超长单行 | 实际影响与选择理由 |
|---|---|---|
| backend/codex_appserver.go executeLiveInbox | 历史 JSON/RPC、单个巨大 item | stdout 已单独设 16 MiB，stderr 仍 4 MiB；超限明确取消并返回扫描原因，不能继续等不可达响应。大帧及第二响应、超限均有真实管道回归。 |
| backend/codex.go Execute、backend/claude.go Execute、backend/dsh.go Execute | CLI 的一条 JSON 事件含巨大工具输出/完整回答 | 默认 stdout/stderr 4 MiB。旧代码从首个超限起不再交付该流，可能丢最终事件或以不完整结果结束；现在杀树并报错。未观测到这些适配器的生产超限实例，没有擅自统一扩大缓冲。 |
| managedprocess/manager.go Manager.run | 用户服务打印一整条巨大 JSON、minified 内容或诊断 | **具体行为变化：现在会终止并将进程记为 failed；旧版可继续运行但日志从此丢失。** 256 KiB tail 不豁免扫描失败。本轮未发现实际受影响的服务实例，不把潜在场景说成已发生兼容事故；不新增基于猜测的日志分块/例外机制。 |
| backend/codex.go readModelsCache、codexRollout.drainRunner | 单行 models_cache.json；rollout 的长 JSON item | 远程 cat/tail 走默认上限；超限后缓存读取返回 nil、rollout drain 返回 nil，不再使用部分结果；之前可能静默缺失。模型目录和 reasoning 辅助信息可能不可用。没有读取或重放真实文件。 |
| workspace/claude_transcript.go readClaudeTranscriptTail；httpapi/runtime_context.go 的 Codex/Claude/DSH runner context 读取 | tail 400/4000 行中的单条巨大 JSON | 行数限制不限制字节数。超限返回空 transcript fallback / 无 runtime sample；可能少恢复一次辅助文本或上下文统计。此前已经丢超限行，现在还会拒绝该次部分结果。 |
| workspace/remote_files.go ReadRemoteTextFile -> codexaccount/manager.go 的远程 auth.json；ReadRemoteTextFileIfExists -> backend/mcp_secret_file.go readExecutionFile | 大型单行 JSON/配置文件 | 现在返回明确读取错误；配置备份读失败会中止准备，避免把不完整旧配置当作完整备份。无此文件大小的假设性改造；本轮未读取真实 auth/config 内容。 |
| workspace/agent_proxy_helper.go manifest/hash/version/temp probes；workspace/detect.go、git.go、backend_writer.go；app/pet_worktree.go gitRun | 通常为元数据；异常进程或 Git 输出也可能产生长行 | manifest 在远端另限 4096 字节，hash/version 多为短行。仍明确失败，不以部分结果宣称探测/命令成功；未发现需要放大上限的实际输出。 |
| 上述全部命令的 stderr，含 app-server | 一条巨大诊断或携带大载荷的错误 | 超过 4 MiB 会取消整个运行，即使 stdout 正常。Rejected frame 不进 handler/Result；错误不含帧正文。没有验证“正常 stderr 一定较小”的保证。 |

选择：保留首轮已评审的有界、显式失败策略，避免静默丢输出和错误成功；**不承诺普通长日志完全兼容**。已列出确定的行为变化与潜在合法场景，但没有实际新增兼容事故证据，因此本增量不改日志政策。若后续证实需要容忍超长日志，应按调用方定义有界截断/分块与可观测告警，不能让 RPC 重新静默吞帧。

## S3：取消与清理断言

SSH 新测试通过真实本机 SSH 会话制造 1024 字节扫描上限失败；onCancel 内用同一连接做往返 probe，断言 hook 执行时连接仍活着，Run 后连接已关闭。服务端保持等待 stdin，防止自然退出掩盖取消分支。

WSL 新测试只替换既有 wslProcessRunner hook，不启动 WSL/硬件。覆盖普通/streaming scan 错误、未标记 scan、普通错误、成功；验证包装扫描错误触发一次有界、unmarked 的同 mark 清理，保留原结果/错误且不改调用者环境。

## 真实命令与结果

工作目录同上，所有临时文件位于 AHA2_SCRATCH_DIR，TMPDIR 为其 tmp 子目录；无真实 Codex 请求、生产线程或外部 SSH 主机。

**旧基线**：修改实现前仅增加测试，校验四个生产文件逐字等于 7bcc61f（并非编译失败）。命令退出 1：provider-failed 断言看到 synthetic-runner-cleanup-failure，丢失 synthetic-provider-quota-exhausted。其余三个优先级子例通过；SSH/WSL 清理测试原实现就通过，不声称它们发现了旧代码的新故障。修复后同命令退出 0。

```sh
TMPDIR="$AHA2_SCRATCH_DIR/tmp" AHA2_CODEX_LIVE_TEST=0 go test ./internal/backend ./internal/workspace -run 'Test(CodexAppServerRealProcessErrorPrecedence|SSHScanFailureCallsCancelBeforeClosingConnection|WSLScanFailureCleansMarkedRun)$' -count=1 -v
```

旧基线输出：
```text
=== RUN   TestCodexAppServerRealProcessErrorPrecedence
=== RUN   TestCodexAppServerRealProcessErrorPrecedence/provider-failed
    codex_appserver_process_test.go:154: provider failure was replaced by cleanup error: synthetic-runner-cleanup-failure
=== RUN   TestCodexAppServerRealProcessErrorPrecedence/provider-failed-scan
=== RUN   TestCodexAppServerRealProcessErrorPrecedence/large
=== RUN   TestCodexAppServerRealProcessErrorPrecedence/handshake-failure
--- FAIL: TestCodexAppServerRealProcessErrorPrecedence (0.16s)
    --- FAIL: TestCodexAppServerRealProcessErrorPrecedence/provider-failed (0.05s)
    --- PASS: TestCodexAppServerRealProcessErrorPrecedence/provider-failed-scan (0.07s)
    --- PASS: TestCodexAppServerRealProcessErrorPrecedence/large (0.03s)
    --- PASS: TestCodexAppServerRealProcessErrorPrecedence/handshake-failure (0.00s)
FAIL
FAIL	github.com/ChinaKai/AHA2/internal/backend	0.162s
=== RUN   TestSSHScanFailureCallsCancelBeforeClosingConnection
--- PASS: TestSSHScanFailureCallsCancelBeforeClosingConnection (0.00s)
=== RUN   TestWSLScanFailureCleansMarkedRun
=== RUN   TestWSLScanFailureCleansMarkedRun/scan
=== RUN   TestWSLScanFailureCleansMarkedRun/streaming-scan
=== RUN   TestWSLScanFailureCleansMarkedRun/unmarked-scan
=== RUN   TestWSLScanFailureCleansMarkedRun/ordinary-error
=== RUN   TestWSLScanFailureCleansMarkedRun/success
--- PASS: TestWSLScanFailureCleansMarkedRun (0.00s)
    --- PASS: TestWSLScanFailureCleansMarkedRun/scan (0.00s)
    --- PASS: TestWSLScanFailureCleansMarkedRun/streaming-scan (0.00s)
    --- PASS: TestWSLScanFailureCleansMarkedRun/unmarked-scan (0.00s)
    --- PASS: TestWSLScanFailureCleansMarkedRun/ordinary-error (0.00s)
    --- PASS: TestWSLScanFailureCleansMarkedRun/success (0.00s)
PASS
ok  	github.com/ChinaKai/AHA2/internal/workspace	0.007s
FAIL
```

**断言判别力**：只用 overlay 调换 SSH client.Close/onCancel 顺序，并去掉 WSL 的扫描错误清理条件；确认替换各命中一次。命令退出 1，SSH hook 报 closed network connection；WSL 普通/streaming scan 都由预期 2 次变为实际 1 次。控制组仍通过；退出是断言失败，不是构建失败，主 worktree 未改为变异源码。

```sh
TMPDIR="$AHA2_SCRATCH_DIR/tmp" go test -overlay "$AHA2_SCRATCH_DIR/cleanup-mutation-overlay.json" ./internal/workspace -run 'Test(SSHScanFailureCallsCancelBeforeClosingConnection|WSLScanFailureCleansMarkedRun)$' -count=1 -v
```

**必要验证**（均退出 0）：
```sh
TMPDIR="$AHA2_SCRATCH_DIR/tmp" AHA2_CODEX_LIVE_TEST=0 go test ./internal/backend ./internal/workspace -run 'Test(CodexLive|CodexAppServer|RunProcessRPC|RunProcessOversized|SSH.*(RPC|ScanFailure)|WSL.*(RPC|ScanFailure)|ScanOutput)' -count=1 -v
TMPDIR="$AHA2_SCRATCH_DIR/tmp" AHA2_CODEX_LIVE_TEST=0 go test -race ./internal/backend ./internal/workspace -run 'Test(CodexAppServerRealProcess.*|SSHScanFailureCallsCancelBeforeClosingConnection|WSLScanFailureCleansMarkedRun|SSHRunnerRPCOutputLimits|WSLHostCommandPreservesRPCOutputLimits)$' -count=1 -v
TMPDIR="$AHA2_SCRATCH_DIR/tmp" go vet ./internal/backend ./internal/workspace
git diff --check
```

定向 33 个顶层测试通过、3 个 helper/真实 CLI 入口跳过；race 6 个顶层测试通过。实际包输出：
```text
increment-targeted.log
ok  	github.com/ChinaKai/AHA2/internal/backend	0.319s
ok  	github.com/ChinaKai/AHA2/internal/workspace	0.233s
increment-race-final.log
ok  	github.com/ChinaKai/AHA2/internal/backend	7.365s
ok  	github.com/ChinaKai/AHA2/internal/workspace	1.688s
```
首个 race 命令把 CodexAppServerRealProcess 写成无后缀的精确匹配，backend 回执为 no tests to run；该次只覆盖 workspace。随后用上面带 .* 的最终命令补齐，未把首次空跑计作 backend 验证。vet 执行两次均无输出、退出 0。未重复上一轮 247 秒全套进程 race 或全仓矩阵。

## 完整相关函数与调用方

以下为源码 head 原文，函数均完整：Codex.Execute 是 executeLiveInbox 的直接调用者；executeLiveInbox 处理 runDone/runnerErr/sessionErr；导出的扫描谓词只判定真实标记（不匹配字符串）；scanOutput 是产生该标记的路径；SSH execCommand 是取消 hook 的装配处；WSL Run/hostCommand 和 mark/cleanup 是标记链路。未改动的本地 runProcess/管道 spool 等完整上下文已在原评审包中，该文件全文也包含于末尾完整 diff，可无损还原；本轮没有重构这些实现。

### internal/backend/codex.go:38（完整未修改调用方，无损内嵌）

为满足 64 KiB，只有这一个较长且未修改的直接调用方使用 gzip/base64；其余下列函数均展开。原文 8880 字节，SHA-256 `4c9163d670ac9fde8b455e5150594d364b283746bb72388d571a7a984ebfed34`。用 `gzip.decompress(base64.b64decode(encoded))` 即得完整函数；可与 source head 的同名函数逐字比较。

<!-- CODEX_EXECUTE_GZIP_BASE64_BEGIN -->
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
V4qwIgAA
<!-- CODEX_EXECUTE_GZIP_BASE64_END -->

### internal/backend/codex_appserver.go:670

```go
func (adapter Codex) executeLiveInbox(
	ctx context.Context, request Request, emit func(Event), binary string, timeout time.Duration,
) (result Result, err error, started bool) {
	idleWarning, idleTimeout, heartbeat := backendWatchdogDurations(adapter.IdleWarning, adapter.IdleTimeout, adapter.Heartbeat)
	catalogPath := adapter.ensureModelCatalog(ctx, request)
	environment := filterEnvironment(request.Environment)
	runContext, cancelRun := context.WithCancelCause(ctx)
	rollout := &codexRollout{
		home:   strings.TrimSpace(request.Environment["CODEX_HOME"]),
		thread: strings.TrimSpace(request.ProviderSessionID),
		runner: request.Runner,
	}
	rollout.prime(runContext)
	var emitMu sync.Mutex
	send := func(event Event) {
		if event.Type == "" || emit == nil {
			return
		}
		emitMu.Lock()
		emit(event)
		emitMu.Unlock()
	}
	activity := make(chan string, 1)
	monitorDone := make(chan struct{})
	go monitorBackendActivity(runContext, idleWarning, idleTimeout, heartbeat, activity, send, cancelRun, monitorDone)

	input := make(chan string)
	messages := make(chan codexRPCMessage, 256)
	runDone := make(chan struct{})
	runnerResult := make(chan workspace.Result, 1)
	runnerErr := make(chan error, 1)
	go func() {
		defer close(runDone)
		value, runFailure := request.Runner.Run(runContext, workspace.Command{
			Executable: binary, Args: codexAppServerArguments(request, catalogPath),
			Dir: request.WorkDir, Env: environment, Input: input,
			Timeout: timeout, KillTree: true, ReverseForward: request.ReverseForward,
			// Large JSONL frames need a separate bound from retained diagnostics.
			StdoutLineLimit: 16 * 1024 * 1024, OutputLimit: 64 * 1024,
		}, func(line string) {
			select {
			case activity <- "app_server":
			default:
			}
			var message codexRPCMessage
			if json.Unmarshal([]byte(line), &message) != nil {
				return
			}
			select {
			case messages <- message:
			case <-runContext.Done():
			case <-runDone:
			}
		})
		runnerResult <- value
		runnerErr <- runFailure
	}()

	session := &codexAppServerSession{
		request: request, emit: send, input: input, messages: messages,
		runDone: runDone, ctx: runContext, poll: adapter.LivePoll,
		note:     func(text string) { rollout.note(text) },
		onThread: func(thread string) { rollout.thread = thread },
		tick: func() {
			for _, event := range rollout.drain(runContext, false) {
				send(event)
			}
		},
	}
	sessionErr := session.run()
	started = session.started
	// Flush reasoning the app-server stream did not carry (raw reasoning,
	// empty summary) the same way the exec path does at process end.
	for _, event := range rollout.drain(runContext, true) {
		send(event)
	}
	close(input)

	select {
	case <-runDone:
	case <-time.After(codexLiveShutdownGrace):
		cancelRun(errors.New("codex app-server did not exit after the turn"))
		<-runDone
	}
	runResult := <-runnerResult
	runFailure := <-runnerErr
	cause := context.Cause(runContext)
	// A runner command timeout kills the process inside the runner without
	// canceling runContext. Give the turn a deadline cause and remember it, so
	// the closed stream below cannot be mistaken for an app-server crash.
	hardTimeout := errors.Is(runFailure, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded)
	if hardTimeout {
		cancelRun(context.DeadlineExceeded)
	} else {
		cancelRun(context.Canceled)
	}
	<-monitorDone
	session.rollbackClaim()

	if errors.Is(cause, ErrBackendIdleTimeout) {
		return Result{Reply: session.reply, ExitCode: runResult.ExitCode, ProviderSessionID: session.threadID}, ErrBackendIdleTimeout, started
	}
	if ctx.Err() != nil {
		// The caller cancelled or its deadline expired. The context is the
		// authoritative cause; never start another app-server or exec.
		return Result{Reply: session.reply, ExitCode: runResult.ExitCode, ProviderSessionID: session.threadID}, ctx.Err(), started
	}
	if hardTimeout {
		// The runner's own timeout killed the process. This is a Turn timeout,
		// not an unexpected exit, so it must not be resumed or retried. The
		// caller's context is still live, so runFailure is the deadline here.
		return Result{Reply: session.reply, ExitCode: runResult.ExitCode, ProviderSessionID: session.threadID}, fmt.Errorf("codex app-server turn timed out: %w", runFailure), started
	}
	if errors.Is(runFailure, context.Canceled) {
		return Result{Reply: session.reply, ExitCode: runResult.ExitCode, ProviderSessionID: session.threadID}, context.Canceled, started
	}

	// An unexpected process exit has no turn/completed response, so the runner
	// result is the only place the exit code and stderr survive. Attach them to
	// the adapter error instead of discarding the only diagnostic evidence.
	// Before a turn starts, or when framing fails or the process disappears,
	// prefer the runner's concrete cause over a generic stream/exit error.
	// Once started, an ordinary cleanup failure must not hide a provider error.
	if runFailure != nil && (!started || workspace.IsProcessOutputError(runFailure) || errors.Is(sessionErr, errCodexAppServerExited)) {
		return Result{Reply: session.reply, ExitCode: runResult.ExitCode, ProviderSessionID: session.threadID}, runFailure, started
	}
	if errors.Is(sessionErr, errCodexAppServerExited) {
		sessionErr = &codexAppServerExitError{
			cause: sessionErr, exitCode: runResult.ExitCode, stderr: runResult.Stderr,
		}
	}

	if sessionErr != nil {
		if session.providerError != "" && !strings.Contains(sessionErr.Error(), session.providerError) {
			return Result{Reply: session.reply, ExitCode: runResult.ExitCode, ProviderSessionID: session.threadID}, fmt.Errorf("%v: %s", sessionErr, session.providerError), started
		}
		return Result{Reply: session.reply, ExitCode: runResult.ExitCode, ProviderSessionID: session.threadID}, sessionErr, started
	}
	if runFailure != nil {
		return Result{Reply: session.reply, ExitCode: runResult.ExitCode, ProviderSessionID: session.threadID}, runFailure, started
	}
	if strings.TrimSpace(session.reply) == "" {
		return Result{ExitCode: runResult.ExitCode, ProviderSessionID: session.threadID}, fmt.Errorf("codex returned no agent message"), started
	}
	return Result{
		Reply: strings.TrimSpace(session.reply), ExitCode: runResult.ExitCode, ProviderSessionID: session.threadID,
	}, nil, started
}
```

### internal/workspace/local.go:194

```go
func IsProcessOutputError(err error) bool { return errors.Is(err, errProcessOutputScan) }
```

### internal/workspace/local.go:392

```go
func scanOutput(reader io.Reader, destination io.Writer, handler LineHandler, wait *sync.WaitGroup, lineLimit int, fail func(error)) {
	defer wait.Done()
	scanner := bufio.NewScanner(reader)
	if lineLimit <= 0 {
		lineLimit = 4 * 1024 * 1024
	}
	initialSize := min(64*1024, lineLimit)
	scanner.Buffer(make([]byte, initialSize), lineLimit)
	for scanner.Scan() {
		line := scanner.Text()
		_, _ = io.WriteString(destination, line+"\n")
		if handler != nil {
			handler(line)
		}
	}
	if err := scanner.Err(); err != nil && fail != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			// Never include the rejected frame: it can contain session history
			// and credentials. Only the configured limit is safe to report.
			fail(fmt.Errorf("%w: line exceeds %d byte scan limit", errProcessOutputScan, lineLimit))
		} else {
			fail(fmt.Errorf("%w: %w", errProcessOutputScan, err))
		}
	}
	// Keep draining after cancellation to release backpressured pipe writers.
	// Do not deliver later frames after a framing failure.
	_, _ = io.Copy(io.Discard, reader)
}
```

### internal/workspace/ssh.go:220

```go
func (runner *SSHRunner) execCommand(ctx context.Context, client *ssh.Client, command Command, platform string, onLine LineHandler) (Result, error) {
	if isWindowsPlatform(platform) {
		// The PowerShell bootstrap reads a length-prefixed metadata frame and
		// then copies the rest of its stdin to the child, so a running command
		// receives Command.Input through the same channel.
		payload, err := windowsRemotePayload(command)
		if err != nil {
			return Result{}, err
		}
		return runner.execOnClient(ctx, client, windowsBootstrapCommand(), payload, command.Input, command.OutputLimit, onLine, command.StdoutLineLimit)
	}
	// Tag the remote tree so an interrupt or timeout can end it; closing the
	// connection alone leaves the remote processes running.
	command, mark := markCommand(command)
	onCancel := func() { runner.killMarked(client, mark) }
	if command.Input != nil {
		// The script carries the Turn's environment, including its capability
		// token. It cannot travel as a "sh -c" argument (the SSH exec request and
		// the remote process list would expose it) and it cannot be the session's
		// whole stdin (the backend needs that stream). Send it as one base64
		// metadata line instead: the constant bootstrap below duplicates stdin to
		// fd 3, decodes that line into a script, and execs the backend with fd 3.
		metadata, err := streamRemoteMetadata(command)
		if err != nil {
			return Result{}, err
		}
		return runner.execOnClientWithCancel(ctx, client, streamBootstrapCommand(), metadata+command.Stdin, command.Input, command.OutputLimit, onLine, onCancel, command.StdoutLineLimit)
	}
	script, err := remoteScript(command)
	if err != nil {
		return Result{}, err
	}
	return runner.execOnClientWithCancel(ctx, client, "sh -s", script, nil, command.OutputLimit, onLine, onCancel, command.StdoutLineLimit)
}
```

### internal/workspace/ssh.go:389

```go
func (runner *SSHRunner) execOnClientWithCancel(ctx context.Context, client *ssh.Client, remoteCommand, stdin string, input <-chan string, outputLimit int, onLine LineHandler, onCancel func(), stdoutLineLimit int) (Result, error) {
	readContext, stopOutput := context.WithCancel(ctx)
	defer stopOutput()
	start := time.Now()
	session, err := client.NewSession()
	if err != nil {
		return Result{}, err
	}
	defer session.Close()
	var stdinPipe io.WriteCloser
	if input == nil {
		session.Stdin = strings.NewReader(stdin)
	} else {
		// Keep the write end open after the initial payload so the backend can
		// receive messages while it runs. Closing it ends the remote stdin.
		if stdinPipe, err = session.StdinPipe(); err != nil {
			return Result{}, err
		}
	}
	stdoutPipe, err := session.StdoutPipe()
	if err != nil {
		return Result{}, err
	}
	stderrPipe, err := session.StderrPipe()
	if err != nil {
		return Result{}, err
	}
	stdout, stderr := newOutputBuffer(outputLimit), newOutputBuffer(outputLimit)
	var wait sync.WaitGroup
	wait.Add(2)
	outputErrors := make(chan error, 2)
	failOutput := func(err error) {
		outputErrors <- err
		stopOutput()
	}
	go scanOutput(stdoutPipe, stdout, onLine, &wait, stdoutLineLimit, failOutput)
	go scanOutput(stderrPipe, stderr, nil, &wait, 0, failOutput)
	done := make(chan error, 1)
	go func() { done <- session.Run(remoteCommand) }()
	exited := make(chan struct{})
	feederDone := make(chan struct{})
	if stdinPipe != nil {
		go feedInput(stdinPipe, stdin, input, exited, feederDone)
	} else {
		close(feederDone)
	}
	var runErr error
	select {
	case runErr = <-done:
	case <-readContext.Done():
		if onCancel != nil {
			onCancel()
		}
		_ = client.Close()
		runErr = <-done
	}
	close(exited)
	if stdinPipe != nil {
		waitInputFeeder(feederDone)
	} else {
		<-feederDone
	}
	wait.Wait()
	result := Result{Stdout: stdout.String(), Stderr: stderr.String(), Duration: time.Since(start)}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	select {
	case err := <-outputErrors:
		return result, err
	default:
	}
	if runErr == nil {
		return result, nil
	}
	var exitErr *ssh.ExitError
	if errors.As(runErr, &exitErr) {
		result.ExitCode = exitErr.ExitStatus()
		return result, nil
	}
	return result, runErr
}
```

### internal/workspace/ssh.go:258

```go
func (runner *SSHRunner) killMarked(client *ssh.Client, mark string) {
	if mark == "" || client == nil {
		return
	}
	session, err := client.NewSession()
	if err != nil {
		return
	}
	defer session.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = session.Run("sh -c " + shellQuote(killMarkedScript) + " aha-kill " + shellQuote(mark))
	}()
	timer := time.NewTimer(remoteKillTimeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
}
```

### internal/workspace/wsl.go:31

```go
func (runner WSLRunner) Run(parent context.Context, command Command, onLine LineHandler) (Result, error) {
	distro := strings.TrimSpace(runner.Distro)
	if distro == "" {
		return Result{}, fmt.Errorf("wsl distro is required")
	}
	// Killing wsl.exe ends the Windows-side client, not the processes inside
	// the distro, so tag the run and end the tagged processes when the context
	// is cancelled.
	command, mark := markCommand(command)
	host, err := wslHostCommand(distro, command)
	if err != nil {
		return Result{}, err
	}
	result, err := wslProcessRunner(parent, host, wslHostEnvironment(), onLine)
	if mark != "" && (parent.Err() != nil || errors.Is(err, context.DeadlineExceeded) || IsProcessOutputError(err)) {
		KillMarkedProcesses(WSLRunner{Distro: distro}, mark)
	}
	return result, err
}
```

### internal/workspace/wsl.go:62

```go
func wslHostCommand(distro string, command Command) (Command, error) {
	host := Command{
		Executable:      findWSLExecutable(),
		Timeout:         command.Timeout,
		OutputLimit:     command.OutputLimit,
		StdoutLineLimit: command.StdoutLineLimit,
		KillTree:        command.KillTree,
	}
	if command.Input != nil {
		// The script carries the Turn's environment, including its capability
		// token, so it must not become the wsl.exe command line. Feed it as one
		// base64 metadata line on stdin; the constant bootstrap saves stdin to
		// fd 3, loads the script, and execs the backend on that stream.
		metadata, err := streamRemoteMetadata(command)
		if err != nil {
			return Command{}, err
		}
		host.Args = []string{"-d", distro, "-e", "bash", "-c", streamBootstrapCommand()}
		host.Stdin = metadata + command.Stdin
		host.Input = command.Input
		return host, nil
	}
	script, err := remoteScript(command)
	if err != nil {
		return Command{}, err
	}
	host.Args = []string{"-d", distro, "--", "bash", "-s"}
	host.Stdin = script
	return host, nil
}
```

### internal/workspace/run_mark.go:40

```go
func markCommand(command Command) (Command, string) {
	if !command.KillTree {
		return command, ""
	}
	mark := newRunMark()
	if mark == "" {
		return command, ""
	}
	env := make(map[string]string, len(command.Env)+1)
	for key, value := range command.Env {
		env[key] = value
	}
	env[runMarkEnv] = mark
	command.Env = env
	return command, mark
}
```

### internal/workspace/run_mark.go:89

```go
func KillMarkedProcesses(runner Runner, mark string) {
	if mark == "" || runner == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), remoteKillTimeout)
	defer cancel()
	_, _ = runner.Run(ctx, Command{
		Executable: "sh",
		Args:       []string{"-c", killMarkedScript, "aha-kill", mark},
		Timeout:    remoteKillTimeout,
	}, nil)
}
```

## provided / missing / not_applicable

- provided：固定 base/head、完整全文件 diff（含旧评审包，无范围排除）、完整增量函数原文、完整直接调用函数（Codex.Execute 无损编码）、调用方影响清单、真实进程与取消 hook 回归、旧代码失败及变异失败证据、定向/race/vet 结果。
- missing：未捕获原始生产 resume 失败帧，未运行真实 Codex/模型、原生 Windows、真实 WSL 或远程硬件；均在授权限制或当前环境以外。fake WSL 验证调用参数/mark 清理，不宣称验证了真实 WSL 杀树。
- not_applicable：未重新跑已通过的 4 分钟全套 race、全仓/前端/构建矩阵；没有发布包、部署、推送、合并或生产配置/会话变更。752 保持开放，增量尚待评审。

## 完整固定范围 diff（无损内嵌）

`git diff --no-ext-diff --binary 7bcc61f595aaf5dd6e45fbc9e41ab27f59f6705b e183dfcc50ae0385b627ebe1862a0941ebd557e0` 原始 UTF-8 diff 共 79128 字节，SHA-256 `2933f1b73140d5fe48dec7ef553802ef5fe6254d28498a6cb66219c34b637b9d`。范围含此前 docs 提交新增的原 review-packet.md；若全部原文展开则单独 diff 已超 64 KiB。因此本节使用标准 gzip/base64 完整存储，没有截断或排除任何文件。此编码只用于传输容积，不是 Git binary patch；解码后是可读的完整 Git diff。

还原到自己的 scratch 并校验（仅写 scratch）：
```python
from pathlib import Path
import base64, gzip, hashlib, os
packet = Path("review-packet-increment.md").read_text()
encoded = packet.split("<!-- FULL_DIFF_GZIP_BASE64_BEGIN -->\n", 1)[1].split("\n<!-- FULL_DIFF_GZIP_BASE64_END -->", 1)[0]
diff = gzip.decompress(base64.b64decode(encoded))
assert len(diff) == 79128
assert hashlib.sha256(diff).hexdigest() == "2933f1b73140d5fe48dec7ef553802ef5fe6254d28498a6cb66219c34b637b9d"
(Path(os.environ["AHA2_SCRATCH_DIR"]) / "increment-full.diff").write_bytes(diff)
```

<!-- FULL_DIFF_GZIP_BASE64_BEGIN -->
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
<!-- FULL_DIFF_GZIP_BASE64_END -->
