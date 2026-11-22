# task-751 评审包：Codex 长历史恢复与 RPC 大帧读取

## 问题、目标和边界

`thread/resume` 未省略历史，AHA 却只消费返回的 thread.id。进程读取器的 bufio.Scanner 单行上限为 4 MiB；超限后停止调用 handler 并丢弃剩余流，未报告扫描错误。表现可以是恢复等待 30 秒后回退 exec。

提供的诊断证据：同一线程的只读 thread/read 回包为 4,715,792 字节，已超过 4 MiB；没有捕获原失败 resume 帧。本轮通过旧代码与合成回归证明接入缺陷，没有重放生产线程，也不把原故障推断描述为真实失败帧重放。

目标：恢复只取客户端需要的元数据；合理大帧可达且后续响应继续可达；超出明确上限时取消生产者、排空并明确报错。30 秒恢复时限不变。仅修改源码和测试，未构建发布包、部署、发版、推送或合并 main，未修改生产配置、凭据及会话。

协议依据：已通过 OpenAI Docs 搜索并读取 [官方 app-server 文档](https://learn.chatgpt.com/docs/app-server)。文档支持 thread/resume 的 excludeTurns:true，用于返回线程元数据和 live 状态，历史另行分页获取；这里不请求客户端历史，模型上下文仍由 Codex 会话恢复。本轮没有增加 experimental 能力或改变 thread/start 参数。

## 基线与交付定位

- base: `8dba81b859ca2b90681cd1a80ddbcebb3629d8df`
- source/review head: `7bcc61f595aaf5dd6e45fbc9e41ab27f59f6705b`
- branch: `aha/task_b0e0670c601199655542`
- worktree: `/srv/build/users/kaikai/kk-workspace/.aha2-worktrees/task_b0e0670c601199655542`
- 环境：Linux amd64，Go 1.27.4。
- 本文完整 diff 的范围是 base 到 source/review head 的全部 11 个源码/测试文件。分支随后只增加一个提交来保存本文件；该文档提交不改变源码，避免把评审包递归收入自身 diff。审查代码以这里的精确 source head 为准。
- 没有新建或删除临时 worktree；旧基线验证使用只读 Go overlay，未回退或覆盖本工作区源码。scratch 清理前已把结果和重现方法收进本文件。

## 改动与设计

1. resume 请求增加 excludeTurns:true，保留原 threadId、cwd、model、modelProvider、approvalPolicy 和 sandbox。新线程不带此字段。
2. Command.StdoutLineLimit 单独控制 stdout 扫描缓冲上限。app-server 使用 16 MiB，其他命令默认 4 MiB；stderr 始终 4 MiB。这里是 Scanner 缓冲上限，单行（包括分隔符）必须能放入缓冲；不是可用无限长度字符串。未增加超时时间。
3. 扫描失败先写入错误通道并取消进程，再排空流。错误最多每个流一个（通道容量 2），不包含被拒绝帧的任何正文；不会把部分帧交给 handler 或保存在 Result 中。失败后不再交付同一流的后续帧，整个运行明确失败。
4. 本地 runner 为内部读流失败使用独立取消上下文，保留外部取消/命令超时的错误优先级。原有每流 4 MiB spool、背压、3 秒管道排空、3 秒 stdin feeder 等待和截断错误逻辑保持不变。
5. SSH 把扫描失败接入既有取消分支，在关闭连接前调用 marked-process 清理 hook。Windows SSH 的原有连接取消方式保持不变。WSL 透传 stdout 上限，扫描失败也清理已标记的远端进程树。
6. app-server 每流保留的 Result 尾部上限为 64 KiB；stdout handler 仍收到完整合法帧。既有 messages 队列容量 256 不变，扫描缓冲、spool 和队列均有界；此变更不是大队列内存压力基准。握手阶段也优先返回 runner 错误，防止明确扫描错误被泛化成“app-server exited”。
7. live inbox steer/ack/rollback、单次恢复上限和 fallback 分支未修改；真实生产者结束、后代持有管道、慢 handler、stdin 阻塞及 timeout 的已有测试继续覆盖这些约束。

生产变更为 5 个文件（codex_appserver.go、local.go、runner.go、ssh.go、wsl.go）；测试变更为 6 个文件（2 个已有测试调整、4 个新增回归文件）。完整列表和内容均在末尾 diff。

## 验证命令和真实结果

以下命令均在上述 worktree 执行。TMPDIR 指向本 Task scratch，真实 Codex 测试通过 AHA2_CODEX_LIVE_TEST=0 禁用；子进程均为 Go 测试二进制，SSH 只使用本机测试服务器。没有真实模型请求。

### 旧基线：预期失败，退出码 1

```sh
TMPDIR="$AHA2_SCRATCH_DIR/tmp" AHA2_CODEX_LIVE_TEST=0 go test -overlay "$AHA2_SCRATCH_DIR/baseline-overlay.json" ./internal/workspace ./internal/backend -run '^(TestRunProcessRPCFrameAbove4MiBAndFollowingResponse|TestRunProcessRPCFrameLimitFailsPromptly|TestCodexAppServerResumeExcludesHistoryKeepsConfiguration)$' -count=1 -v
```

等价基线说明：overlay 将 codex_appserver.go、local.go、ssh.go、wsl.go、local_output_test.go、local_process_test.go 全部替换为 base 内容。只保留新测试和 Command 中新增但旧实现完全不使用的 StdoutLineLimit 字段以兼容测试编译，没有带入任何行为修复。不是编译失败。

完整基线测试输出：

```text
=== RUN   TestRunProcessRPCFrameAbove4MiBAndFollowingResponse
    rpc_output_test.go:79: delivered IDs=[], want both large and following response
--- FAIL: TestRunProcessRPCFrameAbove4MiBAndFollowingResponse (0.02s)
=== RUN   TestRunProcessRPCFrameLimitFailsPromptly
=== RUN   TestRunProcessRPCFrameLimitFailsPromptly/stdout
    rpc_output_test.go:97: want explicit scan limit error, got context deadline exceeded
=== RUN   TestRunProcessRPCFrameLimitFailsPromptly/stderr
    rpc_output_test.go:97: want explicit scan limit error, got context deadline exceeded
--- FAIL: TestRunProcessRPCFrameLimitFailsPromptly (16.01s)
    --- FAIL: TestRunProcessRPCFrameLimitFailsPromptly/stdout (8.01s)
    --- FAIL: TestRunProcessRPCFrameLimitFailsPromptly/stderr (8.00s)
FAIL
FAIL	github.com/ChinaKai/AHA2/internal/workspace	16.042s
=== RUN   TestCodexAppServerResumeExcludesHistoryKeepsConfiguration
=== RUN   TestCodexAppServerResumeExcludesHistoryKeepsConfiguration/start
=== RUN   TestCodexAppServerResumeExcludesHistoryKeepsConfiguration/resume
    codex_resume_test.go:66: thread params=map[approvalPolicy:never cwd:/synthetic-workspace model:synthetic-model modelProvider:synthetic-provider sandbox:workspace-write threadId:original-thread], want map[approvalPolicy:never cwd:/synthetic-workspace excludeTurns:true model:synthetic-model modelProvider:synthetic-provider sandbox:workspace-write threadId:original-thread]
--- FAIL: TestCodexAppServerResumeExcludesHistoryKeepsConfiguration (0.00s)
    --- PASS: TestCodexAppServerResumeExcludesHistoryKeepsConfiguration/start (0.00s)
    --- FAIL: TestCodexAppServerResumeExcludesHistoryKeepsConfiguration/resume (0.00s)
FAIL
FAIL	github.com/ChinaKai/AHA2/internal/backend	0.004s
FAIL
```

重现 overlay（保留当前测试和 additive Command 字段，在自己的 scratch 运行）：

```python
from pathlib import Path
import json, os, subprocess
root = Path.cwd()
scratch = Path(os.environ["AHA2_SCRATCH_DIR"])
old = scratch / "baseline-overlay"
old.mkdir(exist_ok=True)
files = ["internal/backend/codex_appserver.go", "internal/workspace/local.go",
         "internal/workspace/ssh.go", "internal/workspace/wsl.go",
         "internal/workspace/local_output_test.go", "internal/workspace/local_process_test.go"]
replace = {}
for name in files:
    target = old / Path(name).name
    target.write_bytes(subprocess.check_output(["git", "show", "8dba81b859ca2b90681cd1a80ddbcebb3629d8df:" + name]))
    replace[str(root / name)] = str(target)
(scratch / "baseline-overlay.json").write_text(json.dumps({"Replace": replace}))
```

### 修复后：均通过，退出码 0

```sh
TMPDIR="$AHA2_SCRATCH_DIR/tmp" AHA2_CODEX_LIVE_TEST=0 go test ./internal/workspace ./internal/backend -run '^(TestRunProcessRPC|TestCodexAppServerResumeExcludes)' -count=1 -v
TMPDIR="$AHA2_SCRATCH_DIR/tmp" AHA2_CODEX_LIVE_TEST=0 go test ./internal/workspace ./internal/backend -run '^(TestRunProcessRPC|TestCodexAppServerRealProcess|TestSSHRunnerRPC|TestWSLHostCommandPreserves)' -count=1 -v
TMPDIR="$AHA2_SCRATCH_DIR/tmp" AHA2_CODEX_LIVE_TEST=0 go test ./internal/workspace ./internal/backend -run 'Test(RunProcess|OutputBuffer|ScanOutput|CodexLive|CodexAppServer|SSHRunner.*(Input|Cancel|RPC)|WSL|Stream)' -count=1 -v
TMPDIR="$AHA2_SCRATCH_DIR/tmp" AHA2_CODEX_LIVE_TEST=0 go test -race ./internal/workspace ./internal/backend -run 'Test(RunProcess|OutputBuffer|ScanOutput|CodexLive|CodexAppServer|SSHRunner.*(Input|Cancel|RPC)|WSL|Stream)' -count=1 -v
TMPDIR="$AHA2_SCRATCH_DIR/tmp" go vet ./internal/workspace ./internal/backend
git diff --check
```

首次 process/transport 回归命令退出码 1：SSH 测试服务器过早关闭 TCP，导致截断及缺少 exit-status。修正测试 fixture：接收方读到第二帧后关闭 stdin，服务器收到 EOF 后才结束，避免提前断开；没有为此修改生产实现。上面同一命令重新执行后退出码 0。本轮 go vet 共执行两次，均退出码 0，无输出。

定向及 race 的包结果（下面是实际汇总，未粘贴冗长的逐条 PASS 日志）：

- targeted.log: 61 个顶层测试通过，5 个跳过。

```text
--- SKIP: TestRunProcessHelperProcess (0.00s)
--- SKIP: TestRunProcessRPCOutputHelper (0.00s)
--- SKIP: TestWSLRunnerStreamsInputWithRealWSL (0.00s)
ok  	github.com/ChinaKai/AHA2/internal/workspace	38.059s
--- SKIP: TestCodexAppServerProcessHelper (0.00s)
--- SKIP: TestCodexLiveInboxRealCLI (0.00s)
ok  	github.com/ChinaKai/AHA2/internal/backend	0.131s
```

- race.log: 61 个顶层测试通过，5 个跳过。

```text
--- SKIP: TestRunProcessHelperProcess (0.00s)
--- SKIP: TestRunProcessRPCOutputHelper (0.00s)
--- SKIP: TestWSLRunnerStreamsInputWithRealWSL (0.00s)
ok  	github.com/ChinaKai/AHA2/internal/workspace	247.518s
--- SKIP: TestCodexAppServerProcessHelper (0.00s)
--- SKIP: TestCodexLiveInboxRealCLI (0.00s)
ok  	github.com/ChinaKai/AHA2/internal/backend	2.921s
```

关键断言：5 MiB 有效 JSON/RPC 必须完整解析，紧随其后的第二响应必须可达；真实 adapter 的 resume 大帧后仍进入 turn/start 并得到最终答复；17 MiB stdout 或超过 4 MiB 的 stderr 必须明确报扫描上限错误并在 6 秒内退出，部分帧和合成敏感标记不得出现在结果/错误；SSH 同样验证大帧及超限，WSL 验证参数透传。恢复配置深比较同时覆盖 start/resume 两条分支。

## provided / missing / not_applicable

| 分类 | 材料或验证 | 理由 |
|---|---|---|
| provided | 精确 base/source head/分支/worktree、全部源码测试 diff | 可直接审查或重现 |
| provided | 下附生产生命周期和调用方代码 | 覆盖协议、进程等待、输入释放、管道排空、SSH 取消；不要求扫描全仓 |
| provided | 旧基线行为失败、修复后定向/race/vet 结果 | 断言能抓住旧缺陷，非绕过 scanOutput 的假 handler |
| provided | 真实本地子进程及本地 SSH 通道 | 验证实际读取路径，全部数据为合成 JSON |
| missing | 原失败 resume 帧原文及生产重放 | 未捕获原帧；按授权约束不接管正在运行的宠物线程、不保存原始历史 |
| missing | 真实 Codex/模型端到端验证 | owner 明确禁止真实模型请求，本轮保持禁用 |
| missing | 原生 Windows、真实 WSL 及真实远程 SSH 主机实测 | 当前为 Linux；WSL 真实测试跳过，桥接参数及原有流/取消测试通过；Windows SSH 仅透传及沿用既有关闭方式 |
| not_applicable | 发布包、部署、发版、推送、main 合并及生产配置迁移 | 超出授权；仅提交给调度者评审合并 |
| not_applicable | 全仓测试、前端构建、构建矩阵及大规模性能压测 | 变更限于协议和进程读取路径，没有发现扩大验证范围的必要 |

残余限制：合法帧达到扫描上限仍会明确失败；此上限是有意的内存边界。普通日志的超大单行现在会显式终止对应运行，代替旧行为“继续丢弃输出直到进程退出/超时”。已有超限测试已据此更新。慢 handler 的既有背压/等待契约未改变。

## 供 L2 就地审查的生命周期上下文

为符合 65,536 字节上限，未修改的 spool/drainer 原文改用下述契约说明；保留取消、排空与返回路径的实现原文，以及最后的全部修改 diff，没有截断补丁。以下为 source head 的相关实现原文。未改动部分的契约：processSpool 用互斥锁/条件变量按字节限制 backlog，满时 Write 等待；读出释放空间；CloseWrite 只在生产端结束后设置 EOF。drainer 每次读取 32 KiB 并写入 spool，closeSource 只关闭读取端；finishProcessDrainers 判断是否正在背压，再关闭后代持有的管道，报告丢失数据。configureProcessTree 在 POSIX 使用独立进程组并在取消时 SIGKILL 整组；Windows 沿用既有 taskkill 实现。

### internal/backend/codex.go:81

```go
	if request.LiveInbox != nil && workspace.StreamsInput(request.Runner) {
		liveResult, liveErr, started := adapter.executeLiveInbox(ctx, request, emit, binary, timeout)
		if started {
			if liveErr == nil || ctx.Err() != nil || errors.Is(liveErr, ErrBackendIdleTimeout) {
				return liveResult, liveErr
			}
			if errors.Is(liveErr, errCodexAppServerExited) && liveResult.ProviderSessionID != "" {
				// The app-server process exited on its own while the turn was still
				// running. This is exactly the long-silent-tool case: the tool may
				// run for minutes without producing a line, and a backend crash or
				// an external kill then looks identical to a finished turn unless the
				// thread is resumed. The thread, workspace changes and completed tool
				// results live in the rollout, so resume that thread once instead of
				// failing a Turn that was doing useful work.
				recovery := request
				recovery.ProviderSessionID = liveResult.ProviderSessionID
				recovery.Prompt = codexAppServerRecoveryPrompt
				if emit != nil {
					emit(Event{Type: "agent_progress", Data: map[string]any{
						"message":  "Codex app-server 在 Turn 中退出，正在用同一会话恢复；已投递的 live inbox 消息保留在会话历史中",
						"recovery": "app_server_resume", "cause": clipError(providerErrorText(liveErr.Error()), 500),
					}})
				}
				recovered, recoveryErr, recoveryStarted := adapter.executeLiveInbox(ctx, recovery, emit, binary, timeout)
				if recoveryStarted || recoveryErr == nil || ctx.Err() != nil || errors.Is(recoveryErr, ErrBackendIdleTimeout) {
					return recovered, recoveryErr
				}
				// The resumed app-server could not start a new turn. Continue the
				// same thread through the one-shot exec path instead.
				request = recovery
				if emit != nil {
					message := "恢复用的 Codex app-server 未能启动，已回退 codex exec 继续同一会话；本 Turn 不会实时接收 live inbox 消息"
					if recoveryErr != nil {
						message += "：" + clipError(providerErrorText(recoveryErr.Error()), 300)
					}
					emit(Event{Type: "agent_progress", Data: map[string]any{
						"message": message, "fallback": "codex_exec",
					}})
				}
			} else {
				return liveResult, liveErr
			}
		} else if liveErr == nil || ctx.Err() != nil || errors.Is(liveErr, ErrBackendIdleTimeout) {
			return liveResult, liveErr
		} else {
			// app-server could not start the turn (an unsupported CLI or a
			// transport that cannot carry a live stdin channel, such as a Windows
			// SSH remote). No message was claimed, so the Turn still runs through
			// the one-shot exec path and the next Turn receives the messages.
			if emit != nil {
				message := "Codex app-server 未能在 Turn 开始前启动，已回退到 codex exec；本 Turn 不会实时接收 live inbox 消息"
				if liveErr != nil {
					message += "：" + clipError(providerErrorText(liveErr.Error()), 300)
				}
				emit(Event{Type: "agent_progress", Data: map[string]any{
					"message": message, "fallback": "codex_exec",
				}})
			}
		}
	}
```

### internal/backend/codex_appserver.go:171

```go
func (session *codexAppServerSession) requestWithTimeout(method string, params any, timeout time.Duration) (json.RawMessage, error) {
	id, err := session.send(method, params)
	if err != nil {
		return nil, err
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case message, ok := <-session.messages:
			if !ok {
				return nil, fmt.Errorf("%s: codex app-server stream closed", method)
			}
			if message.ID != nil && *message.ID == id && message.Method == "" {
				if message.Error != nil {
					return nil, errors.New(message.Error.text(method))
				}
				return message.Result, nil
			}
			if err := session.handle(message); err != nil {
				return nil, err
			}
		case <-timer.C:
			return nil, fmt.Errorf("%s: timed out after %s", method, timeout)
		case <-session.runDone:
			return nil, fmt.Errorf("%s: codex app-server exited", method)
		case <-session.ctx.Done():
			return nil, context.Cause(session.ctx)
		}
	}
}

func (session *codexAppServerSession) run() error {
	initializeParams := map[string]any{
		"clientInfo": map[string]any{"name": "aha2", "version": "1"},
	}
	if _, err := session.requestWithTimeout("initialize", initializeParams, codexLiveInitializeTimeout); err != nil {
		return err
	}
	if err := session.sendNotification("initialized", map[string]any{}); err != nil {
		return err
	}

	threadParams := map[string]any{
		"cwd": session.request.WorkDir, "model": session.request.Model,
		"modelProvider":  codexProviderID(session.request),
		"approvalPolicy": "never", "sandbox": codexSandbox(session.request.Filesystem),
	}
	threadMethod := "thread/start"
	if session.request.ProviderSessionID != "" {
		threadMethod = "thread/resume"
		threadParams["threadId"] = session.request.ProviderSessionID
		// AHA needs only metadata; Codex restores model context from its session.
		threadParams["excludeTurns"] = true
	}
	response, err := session.requestWithTimeout(threadMethod, threadParams, codexLiveHandshakeTimeout)
	if err != nil {
		return err
	}
	session.threadID = nestedString(response, "thread", "id")
	if session.threadID == "" {
		return fmt.Errorf("%s returned no thread id", threadMethod)
	}
	if session.onThread != nil {
		session.onThread(session.threadID)
	}
	session.emit(Event{Type: "agent_session", Data: map[string]any{"provider_session_id": session.threadID}})

	response, err = session.requestWithTimeout("turn/start", map[string]any{
		"threadId": session.threadID,
		"cwd":      session.request.WorkDir,
		"input":    []any{map[string]any{"type": "text", "text": session.request.Prompt}},
	}, codexLiveHandshakeTimeout)
	if err != nil {
		return err
	}
	session.turnID = nestedString(response, "turn", "id")
	if session.turnID == "" {
		return fmt.Errorf("turn/start returned no turn id")
	}
	session.started = true
	if session.poll <= 0 {
		session.poll = defaultCodexLivePoll
	}
	return session.loop()
}
```

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
	// the adapter error instead of discarding the only diagnostic evidence. A
	// runner failure stays authoritative: the stream closed because the runner
	// failed, not because the process exited on its own. This also applies during
	// initialize/resume, before the turn loop can report an unexpected exit.
	if runFailure != nil {
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
	if strings.TrimSpace(session.reply) == "" {
		return Result{ExitCode: runResult.ExitCode, ProviderSessionID: session.threadID}, fmt.Errorf("codex returned no agent message"), started
	}
	return Result{
		Reply: strings.TrimSpace(session.reply), ExitCode: runResult.ExitCode, ProviderSessionID: session.threadID,
	}, nil, started
}
```

### internal/workspace/local.go:201

```go
func runProcess(parent context.Context, command Command, env []string, onLine LineHandler) (Result, error) {
	ctx := parent
	cancel := func() {}
	if command.Timeout > 0 {
		ctx, cancel = context.WithTimeout(parent, command.Timeout)
	}
	defer cancel()
	processContext, stopOutput := context.WithCancel(ctx)
	defer stopOutput()
	start := time.Now()
	process := exec.CommandContext(processContext, command.Executable, command.Args...)
	if command.KillTree {
		configureProcessTree(process)
	}
	process.Dir = command.Dir
	process.Env = env
	// With Input the runner owns the stdin pipe instead of using StdinPipe:
	// Wait would close that write end synchronously, and closing a pipe waits
	// for a pending write, so a write nothing releases would hang Wait itself.
	// awaitProcessInputFeeder closes it after the child exits, with a bound.
	var stdinRead, stdinPipe *os.File
	if command.Input == nil {
		process.Stdin = strings.NewReader(command.Stdin)
	} else {
		var err error
		if stdinRead, stdinPipe, err = os.Pipe(); err != nil {
			return Result{}, err
		}
		process.Stdin = stdinRead
	}
	closeStdin := func() {
		if stdinPipe != nil {
			_ = stdinRead.Close()
			_ = stdinPipe.Close()
		}
	}
	// Own the stdout/stderr pipes instead of letting os/exec copy them. The
	// runner has to decide when a pipe held open by a descendant may be closed
	// and whether the drainer was idle or blocked on a full spool at that
	// moment; os/exec's WaitDelay exposes neither, and it also discards the
	// delay error when the process exits non-zero.
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		closeStdin()
		return Result{}, err
	}
	stderrRead, stderrWrite, err := os.Pipe()
	if err != nil {
		_ = stdoutRead.Close()
		_ = stdoutWrite.Close()
		closeStdin()
		return Result{}, err
	}
	process.Stdout = stdoutWrite
	process.Stderr = stderrWrite
	// No c.Pipe is managed by exec here, so WaitDelay only bounds a child that
	// ignores cancellation; it cannot cut off our output pipes.
	process.WaitDelay = processPipeDrainTimeout
	if err := process.Start(); err != nil {
		_ = stdoutRead.Close()
		_ = stdoutWrite.Close()
		_ = stderrRead.Close()
		_ = stderrWrite.Close()
		closeStdin()
		return Result{}, err
	}
	// The child inherited the write ends; keeping the parent's copies open
	// would keep the drainers from ever seeing EOF. Likewise the parent's copy
	// of the stdin read end would keep a write to an exited child pending
	// instead of failing.
	_ = stdoutWrite.Close()
	_ = stderrWrite.Close()
	if stdinRead != nil {
		_ = stdinRead.Close()
	}
	stdoutSpool := newProcessSpool(processOutputSpoolLimit)
	stderrSpool := newProcessSpool(processOutputSpoolLimit)
	stdoutDrainer := startProcessDrainer(stdoutRead, stdoutSpool)
	stderrDrainer := startProcessDrainer(stderrRead, stderrSpool)
	stdout, stderr := newOutputBuffer(command.OutputLimit), newOutputBuffer(command.OutputLimit)
	var wait sync.WaitGroup
	wait.Add(2)
	outputErrors := make(chan error, 2)
	failOutput := func(err error) {
		outputErrors <- err
		stopOutput()
	}
	go scanOutput(stdoutSpool, stdout, onLine, &wait, command.StdoutLineLimit, failOutput)
	go scanOutput(stderrSpool, stderr, nil, &wait, 0, failOutput)
	exited := make(chan struct{})
	feederDone := make(chan struct{})
	if stdinPipe != nil {
		go processInputFeeder(stdinPipe, command.Stdin, command.Input, exited, feederDone)
	} else {
		close(feederDone)
	}
	processErr := process.Wait()
	close(exited)
	if stdinPipe != nil {
		awaitInputFeeder(stdinPipe, feederDone)
	} else {
		<-feederDone
	}
	// Wait returns as soon as the direct child exits. Give the drainers a
	// bounded window to reach EOF; a descendant holding the pipes keeps them
	// open, so close the read ends after the grace period. If a drainer was
	// blocked writing into a full spool at that point, unread pipe data is
	// lost and the result must be reported as truncated regardless of the
	// process exit code.
	truncated := finishProcessDrainers(processPipeDrainTimeout, stdoutDrainer, stderrDrainer)
	stdoutSpool.CloseWrite()
	stderrSpool.CloseWrite()
	wait.Wait()
	if errors.Is(processErr, exec.ErrWaitDelay) {
		processErr = nil
	}
	result := Result{Stdout: stdout.String(), Stderr: stderr.String(), Duration: time.Since(start)}
	if process.ProcessState != nil {
		result.ExitCode = process.ProcessState.ExitCode()
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	select {
	case err := <-outputErrors:
		return result, err
	default:
	}
	if truncated {
		return result, errProcessOutputTruncated
	}
	if processErr != nil {
		var exitErr *exec.ExitError
		if errors.As(processErr, &exitErr) {
			return result, nil
		}
		return result, processErr
	}
	return result, nil
}
```

### internal/workspace/local.go:347

```go
func feedInput(stdin io.WriteCloser, initial string, input <-chan string, exited <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	defer func() { _ = stdin.Close() }()
	if initial != "" {
		if _, err := io.WriteString(stdin, initial); err != nil {
			return
		}
	}
	for {
		select {
		case <-exited:
			return
		case value, ok := <-input:
			if !ok {
				return
			}
			if _, err := io.WriteString(stdin, value); err != nil {
				return
			}
		}
	}
}

// awaitInputFeeder closes the stdin write end after the command exited and
// waits at most processInputFeederTimeout for the feeder to return. The close
// runs on its own goroutine because closing a pipe waits for a pending write;
// when that write is released (by the close itself, or once whatever still
// holds the read end reads or exits) both goroutines end on their own. The
// runner does not wait longer than that: values the feeder has not written are
// dropped, which callers already treat as undelivered.
func awaitInputFeeder(stdin io.Closer, done <-chan struct{}) {
	go func() { _ = stdin.Close() }()
	timer := time.NewTimer(processInputFeederTimeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
}
```

### internal/workspace/local.go:584

```go
func finishProcessDrainers(grace time.Duration, drainers ...*processDrainer) (truncated bool) {
	done := make(chan *processDrainer, len(drainers))
	for _, drainer := range drainers {
		go func(drainer *processDrainer) {
			drainer.wg.Wait()
			done <- drainer
		}(drainer)
	}
	pending := make(map[*processDrainer]bool, len(drainers))
	for _, drainer := range drainers {
		pending[drainer] = true
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	for len(pending) > 0 {
		select {
		case drainer := <-done:
			delete(pending, drainer)
			if drainer.failed() {
				truncated = true
			}
		case <-timer.C:
			for drainer := range pending {
				if drainer.spool.WriterWaiting() {
					truncated = true
				}
				drainer.closeSource()
			}
			// A drainer blocked on a full spool resumes once the handler
			// catches up; Run must not return before that handler call ends.
			for len(pending) > 0 {
				drainer := <-done
				delete(pending, drainer)
				if drainer.failed() {
					truncated = true
				}
			}
			return truncated
		}
	}
	return truncated
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

## 完整源码和测试 diff

```diff
diff --git a/internal/backend/codex_appserver.go b/internal/backend/codex_appserver.go
index 1877a7a..23885f3 100644
--- a/internal/backend/codex_appserver.go
+++ b/internal/backend/codex_appserver.go
@@ -220,6 +220,8 @@ func (session *codexAppServerSession) run() error {
 	if session.request.ProviderSessionID != "" {
 		threadMethod = "thread/resume"
 		threadParams["threadId"] = session.request.ProviderSessionID
+		// AHA needs only metadata; Codex restores model context from its session.
+		threadParams["excludeTurns"] = true
 	}
 	response, err := session.requestWithTimeout(threadMethod, threadParams, codexLiveHandshakeTimeout)
 	if err != nil {
@@ -702,6 +704,8 @@ func (adapter Codex) executeLiveInbox(
 			Executable: binary, Args: codexAppServerArguments(request, catalogPath),
 			Dir: request.WorkDir, Env: environment, Input: input,
 			Timeout: timeout, KillTree: true, ReverseForward: request.ReverseForward,
+			// Large JSONL frames need a separate bound from retained diagnostics.
+			StdoutLineLimit: 16 * 1024 * 1024, OutputLimit: 64 * 1024,
 		}, func(line string) {
 			select {
 			case activity <- "app_server":
@@ -784,8 +788,9 @@ func (adapter Codex) executeLiveInbox(
 	// result is the only place the exit code and stderr survive. Attach them to
 	// the adapter error instead of discarding the only diagnostic evidence. A
 	// runner failure stays authoritative: the stream closed because the runner
-	// failed, not because the process exited on its own.
-	if errors.Is(sessionErr, errCodexAppServerExited) && runFailure != nil {
+	// failed, not because the process exited on its own. This also applies during
+	// initialize/resume, before the turn loop can report an unexpected exit.
+	if runFailure != nil {
 		return Result{Reply: session.reply, ExitCode: runResult.ExitCode, ProviderSessionID: session.threadID}, runFailure, started
 	}
 	if errors.Is(sessionErr, errCodexAppServerExited) {
@@ -800,9 +805,6 @@ func (adapter Codex) executeLiveInbox(
 		}
 		return Result{Reply: session.reply, ExitCode: runResult.ExitCode, ProviderSessionID: session.threadID}, sessionErr, started
 	}
-	if runFailure != nil {
-		return Result{Reply: session.reply, ExitCode: runResult.ExitCode, ProviderSessionID: session.threadID}, runFailure, started
-	}
 	if strings.TrimSpace(session.reply) == "" {
 		return Result{ExitCode: runResult.ExitCode, ProviderSessionID: session.threadID}, fmt.Errorf("codex returned no agent message"), started
 	}
diff --git a/internal/backend/codex_appserver_process_test.go b/internal/backend/codex_appserver_process_test.go
new file mode 100644
index 0000000..da684b2
--- /dev/null
+++ b/internal/backend/codex_appserver_process_test.go
@@ -0,0 +1,108 @@
+package backend
+
+import (
+	"bufio"
+	"context"
+	"encoding/json"
+	"io"
+	"os"
+	"strings"
+	"testing"
+	"time"
+
+	"github.com/ChinaKai/AHA2/internal/workspace"
+)
+
+// This helper speaks JSON-RPC over real process pipes without invoking Codex.
+func TestCodexAppServerProcessHelper(t *testing.T) {
+	mode := os.Getenv("AHA2_APP_SERVER_PROCESS_TEST")
+	if mode == "" {
+		t.Skip("subprocess helper")
+	}
+	scanner := bufio.NewScanner(os.Stdin)
+	encoder := json.NewEncoder(os.Stdout)
+	for scanner.Scan() {
+		var message struct {
+			ID     int64  `json:"id"`
+			Method string `json:"method"`
+		}
+		if json.Unmarshal(scanner.Bytes(), &message) != nil {
+			os.Exit(90)
+		}
+		result := map[string]any{}
+		switch message.Method {
+		case "initialize":
+		case "initialized":
+			continue
+		case "thread/resume":
+			size := 5 * 1024 * 1024
+			if mode == "overflow" {
+				size = 17 * 1024 * 1024
+			}
+			result = map[string]any{"thread": map[string]any{"id": "synthetic-thread"}, "padding": strings.Repeat("x", size)}
+		case "turn/start":
+			result = map[string]any{"turn": map[string]any{"id": "synthetic-turn"}}
+		default:
+			os.Exit(91)
+		}
+		if encoder.Encode(map[string]any{"id": message.ID, "result": result}) != nil {
+			os.Exit(92)
+		}
+		if mode == "overflow" && message.Method == "thread/resume" {
+			time.Sleep(30 * time.Second)
+		}
+		if message.Method == "turn/start" {
+			_, _ = io.WriteString(os.Stdout, `{"method":"item/completed","params":{"threadId":"synthetic-thread","turnId":"synthetic-turn","item":{"type":"agentMessage","id":"reply","text":"synthetic-result","phase":"final_answer"}}}`+"\n")
+			_, _ = io.WriteString(os.Stdout, `{"method":"turn/completed","params":{"threadId":"synthetic-thread","turn":{"id":"synthetic-turn","status":"completed"}}}`+"\n")
+		}
+	}
+	os.Exit(0)
+}
+
+type appServerProcessTestRunner struct {
+	mode    string
+	command workspace.Command
+}
+
+func (*appServerProcessTestRunner) StreamsInput() bool { return true }
+func (runner *appServerProcessTestRunner) Run(ctx context.Context, command workspace.Command, handler workspace.LineHandler) (workspace.Result, error) {
+	runner.command = command
+	// Replace only the executable/argv with the test binary. The adapter's input,
+	// limits, context and line handler still cross LocalRunner and scanOutput.
+	command.Args = []string{"-test.run=^TestCodexAppServerProcessHelper$"}
+	command.Env = map[string]string{"AHA2_APP_SERVER_PROCESS_TEST": runner.mode}
+	return (workspace.LocalRunner{}).Run(ctx, command, handler)
+}
+
+func TestCodexAppServerRealProcessLargeResumeAndLimitFailure(t *testing.T) {
+	executable, err := os.Executable()
+	if err != nil {
+		t.Fatal(err)
+	}
+	for _, mode := range []string{"large", "overflow"} {
+		t.Run(mode, func(t *testing.T) {
+			runner := &appServerProcessTestRunner{mode: mode}
+			request := Request{Runner: runner, WorkDir: t.TempDir(), Prompt: "synthetic", ProviderSessionID: "synthetic-thread"}
+			start := time.Now()
+			result, err, started := (Codex{}).executeLiveInbox(context.Background(), request, nil, executable, 8*time.Second)
+			if mode == "large" {
+				if err != nil || !started || result.Reply != "synthetic-result" || result.ProviderSessionID != "synthetic-thread" {
+					t.Fatalf("started=%v result=%+v err=%v", started, result, err)
+				}
+			} else {
+				if err == nil || !strings.Contains(err.Error(), "line exceeds 16777216 byte scan limit") || strings.Contains(err.Error(), "timed out") {
+					t.Fatalf("explicit frame error lost: %v", err)
+				}
+				if started {
+					t.Fatal("turn started after failed resume frame")
+				}
+				if time.Since(start) > 6*time.Second {
+					t.Fatalf("frame failure waited too long: %s", time.Since(start))
+				}
+			}
+			if runner.command.StdoutLineLimit != 16*1024*1024 || runner.command.OutputLimit != 64*1024 {
+				t.Fatal("adapter did not configure bounded protocol output")
+			}
+		})
+	}
+}
diff --git a/internal/backend/codex_resume_test.go b/internal/backend/codex_resume_test.go
new file mode 100644
index 0000000..75eaf68
--- /dev/null
+++ b/internal/backend/codex_resume_test.go
@@ -0,0 +1,73 @@
+package backend
+
+import (
+	"context"
+	"encoding/json"
+	"reflect"
+	"testing"
+	"time"
+)
+
+func TestCodexAppServerResumeExcludesHistoryKeepsConfiguration(t *testing.T) {
+	for _, resume := range []bool{false, true} {
+		name := "start"
+		if resume {
+			name = "resume"
+		}
+		t.Run(name, func(t *testing.T) {
+			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
+			defer cancel()
+			input := make(chan string)
+			messages := make(chan codexRPCMessage, 8)
+			captured := make(chan map[string]any, 1)
+			request := Request{WorkDir: "/synthetic-workspace", Model: "synthetic-model", Environment: map[string]string{"AHA_PROVIDER_ID": "synthetic-provider"}}
+			if resume {
+				request.ProviderSessionID = "original-thread"
+			}
+			session := &codexAppServerSession{request: request, input: input, messages: messages, runDone: make(chan struct{}), ctx: ctx, emit: func(Event) {}}
+			done := make(chan struct{})
+			go func() {
+				defer close(done)
+				for {
+					select {
+					case <-ctx.Done():
+						return
+					case line := <-input:
+						var msg struct {
+							ID     int64          `json:"id"`
+							Method string         `json:"method"`
+							Params map[string]any `json:"params"`
+						}
+						_ = json.Unmarshal([]byte(line), &msg)
+						switch msg.Method {
+						case "initialize":
+							messages <- codexRPCMessage{ID: int64Ptr(msg.ID), Result: json.RawMessage(`{}`)}
+						case "thread/start", "thread/resume":
+							captured <- msg.Params
+							messages <- codexRPCMessage{ID: int64Ptr(msg.ID), Result: json.RawMessage(`{"thread":{"id":"original-thread"}}`)}
+						case "turn/start":
+							messages <- codexRPCMessage{ID: int64Ptr(msg.ID), Result: json.RawMessage(`{"turn":{"id":"turn-1"}}`)}
+							messages <- codexRPCMessage{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"original-thread","turn":{"id":"turn-1","status":"completed"}}`)}
+							return
+						}
+					}
+				}
+			}()
+			if err := session.run(); err != nil {
+				t.Fatal(err)
+			}
+			<-done
+			want := map[string]any{"cwd": request.WorkDir, "model": request.Model, "modelProvider": codexProviderID(request), "approvalPolicy": "never", "sandbox": codexSandbox(request.Filesystem)}
+			if resume {
+				want["threadId"] = "original-thread"
+				want["excludeTurns"] = true
+			}
+			if got := <-captured; !reflect.DeepEqual(got, want) {
+				t.Fatalf("thread params=%v, want %v", got, want)
+			}
+			if session.threadID != "original-thread" {
+				t.Fatalf("thread changed: %s", session.threadID)
+			}
+		})
+	}
+}
diff --git a/internal/workspace/local.go b/internal/workspace/local.go
index 4aa4639..6cea64c 100644
--- a/internal/workspace/local.go
+++ b/internal/workspace/local.go
@@ -186,6 +186,11 @@ var processInputFeeder = feedInput
 // still blocked writing here, the capture is reported as truncated.
 const processOutputSpoolLimit = 4 * 1024 * 1024
 
+// errProcessOutputScan distinguishes a framing/read failure from cancellation.
+var errProcessOutputScan = errors.New("workspace: process output scan failed")
+
+func isProcessOutputError(err error) bool { return errors.Is(err, errProcessOutputScan) }
+
 // errProcessOutputTruncated reports that a process pipe was closed while the
 // consumer was backpressured, so output still in that pipe was lost. Callers
 // must not mistake this for a successful run with complete output.
@@ -200,8 +205,10 @@ func runProcess(parent context.Context, command Command, env []string, onLine Li
 		ctx, cancel = context.WithTimeout(parent, command.Timeout)
 	}
 	defer cancel()
+	processContext, stopOutput := context.WithCancel(ctx)
+	defer stopOutput()
 	start := time.Now()
-	process := exec.CommandContext(ctx, command.Executable, command.Args...)
+	process := exec.CommandContext(processContext, command.Executable, command.Args...)
 	if command.KillTree {
 		configureProcessTree(process)
 	}
@@ -273,8 +280,13 @@ func runProcess(parent context.Context, command Command, env []string, onLine Li
 	stdout, stderr := newOutputBuffer(command.OutputLimit), newOutputBuffer(command.OutputLimit)
 	var wait sync.WaitGroup
 	wait.Add(2)
-	go scanOutput(stdoutSpool, stdout, onLine, &wait)
-	go scanOutput(stderrSpool, stderr, nil, &wait)
+	outputErrors := make(chan error, 2)
+	failOutput := func(err error) {
+		outputErrors <- err
+		stopOutput()
+	}
+	go scanOutput(stdoutSpool, stdout, onLine, &wait, command.StdoutLineLimit, failOutput)
+	go scanOutput(stderrSpool, stderr, nil, &wait, 0, failOutput)
 	exited := make(chan struct{})
 	feederDone := make(chan struct{})
 	if stdinPipe != nil {
@@ -309,6 +321,11 @@ func runProcess(parent context.Context, command Command, env []string, onLine Li
 	if ctx.Err() != nil {
 		return result, ctx.Err()
 	}
+	select {
+	case err := <-outputErrors:
+		return result, err
+	default:
+	}
 	if truncated {
 		return result, errProcessOutputTruncated
 	}
@@ -367,10 +384,17 @@ func awaitInputFeeder(stdin io.Closer, done <-chan struct{}) {
 	}
 }
 
-func scanOutput(reader io.Reader, destination io.Writer, handler LineHandler, wait *sync.WaitGroup) {
+// scanOutput bounds per-line allocation separately from the retained output tail.
+// On failure it cancels the producer before draining, so a persistent RPC server
+// cannot leave its caller waiting for a response that will never be delivered.
+func scanOutput(reader io.Reader, destination io.Writer, handler LineHandler, wait *sync.WaitGroup, lineLimit int, fail func(error)) {
 	defer wait.Done()
 	scanner := bufio.NewScanner(reader)
-	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
+	if lineLimit <= 0 {
+		lineLimit = 4 * 1024 * 1024
+	}
+	initialSize := min(64*1024, lineLimit)
+	scanner.Buffer(make([]byte, initialSize), lineLimit)
 	for scanner.Scan() {
 		line := scanner.Text()
 		_, _ = io.WriteString(destination, line+"\n")
@@ -378,10 +402,17 @@ func scanOutput(reader io.Reader, destination io.Writer, handler LineHandler, wa
 			handler(line)
 		}
 	}
-	// A line longer than the scanner's maximum stops the scan loop, but the
-	// pipe drainer may still be delivering data. Keep consuming so a
-	// backpressured drainer (and the command behind it) can finish instead of
-	// blocking forever.
+	if err := scanner.Err(); err != nil && fail != nil {
+		if errors.Is(err, bufio.ErrTooLong) {
+			// Never include the rejected frame: it can contain session history
+			// and credentials. Only the configured limit is safe to report.
+			fail(fmt.Errorf("%w: line exceeds %d byte scan limit", errProcessOutputScan, lineLimit))
+		} else {
+			fail(fmt.Errorf("%w: %w", errProcessOutputScan, err))
+		}
+	}
+	// Keep draining after cancellation to release backpressured pipe writers.
+	// Do not deliver later frames after a framing failure.
 	_, _ = io.Copy(io.Discard, reader)
 }
 
diff --git a/internal/workspace/local_output_test.go b/internal/workspace/local_output_test.go
index 11d23e5..b8f5520 100644
--- a/internal/workspace/local_output_test.go
+++ b/internal/workspace/local_output_test.go
@@ -332,7 +332,7 @@ func TestScanOutputAppliesOutputLimit(t *testing.T) {
 	var received []string
 	var wait sync.WaitGroup
 	wait.Add(1)
-	scanOutput(strings.NewReader(input), buffer, func(line string) { received = append(received, line) }, &wait)
+	scanOutput(strings.NewReader(input), buffer, func(line string) { received = append(received, line) }, &wait, 0, nil)
 	wait.Wait()
 
 	if got, want := buffer.String(), input[len(input)-limit:]; got != want {
diff --git a/internal/workspace/local_process_test.go b/internal/workspace/local_process_test.go
index ea95995..dda5fcd 100644
--- a/internal/workspace/local_process_test.go
+++ b/internal/workspace/local_process_test.go
@@ -398,8 +398,8 @@ func TestRunProcessOversizedLineReturnsUnderTimeout(t *testing.T) {
 		command.Timeout = 2 * time.Second
 	}, nil)
 	elapsed := time.Since(start)
-	if !errors.Is(err, context.DeadlineExceeded) {
-		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
+	if !isProcessOutputError(err) {
+		t.Fatalf("error = %v, want explicit output scan failure", err)
 	}
 	if elapsed > 15*time.Second {
 		t.Fatalf("Run took %s for an oversized line with a 2s timeout", elapsed)
diff --git a/internal/workspace/rpc_output_test.go b/internal/workspace/rpc_output_test.go
new file mode 100644
index 0000000..f336814
--- /dev/null
+++ b/internal/workspace/rpc_output_test.go
@@ -0,0 +1,110 @@
+package workspace
+
+import (
+	"context"
+	"encoding/json"
+	"fmt"
+	"io"
+	"os"
+	"strings"
+	"testing"
+	"time"
+)
+
+const rpcOutputHelperEnv = "AHA2_RPC_OUTPUT_TEST_HELPER"
+const rpcTestLineLimit = 16 * 1024 * 1024
+
+// Only synthetic JSON is written by this subprocess; no CLI or model is used.
+func TestRunProcessRPCOutputHelper(t *testing.T) {
+	mode := os.Getenv(rpcOutputHelperEnv)
+	if mode == "" {
+		t.Skip("subprocess helper")
+	}
+	if mode == "large" {
+		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"id": 1, "result": map[string]any{"padding": strings.Repeat("x", 5*1024*1024)}})
+		_, _ = io.WriteString(os.Stdout, `{"id":2,"result":{"ok":true}}`+"\n")
+		os.Exit(0)
+	}
+	stream := os.Stdout
+	size := rpcTestLineLimit + 1024
+	if mode == "stderr" {
+		stream = os.Stderr
+		size = 5 * 1024 * 1024
+	}
+	_, _ = io.WriteString(stream, "synthetic-private-history-api_key=not-a-real-key"+strings.Repeat("x", size))
+	time.Sleep(30 * time.Second) // Explicit scan failure must stop a live producer.
+	os.Exit(0)
+}
+
+func runRPCOutputHelper(t *testing.T, mode string, handler LineHandler) (Result, error) {
+	t.Helper()
+	exe, err := os.Executable()
+	if err != nil {
+		t.Fatal(err)
+	}
+	return (LocalRunner{}).Run(context.Background(), Command{
+		Executable: exe, Args: []string{"-test.run=^TestRunProcessRPCOutputHelper$"},
+		Env:     map[string]string{rpcOutputHelperEnv: mode},
+		Timeout: 8 * time.Second, KillTree: true, OutputLimit: 512, StdoutLineLimit: rpcTestLineLimit,
+	}, handler)
+}
+
+func TestRunProcessRPCFrameAbove4MiBAndFollowingResponse(t *testing.T) {
+	var ids []int
+	var parseErr error
+	result, err := runRPCOutputHelper(t, "large", func(line string) {
+		var frame struct {
+			ID     int `json:"id"`
+			Result struct {
+				Padding string `json:"padding"`
+				OK      bool   `json:"ok"`
+			} `json:"result"`
+		}
+		if e := json.Unmarshal([]byte(line), &frame); e != nil {
+			parseErr = e
+			return
+		}
+		if frame.ID == 1 && len(frame.Result.Padding) != 5*1024*1024 {
+			parseErr = fmt.Errorf("wrong padding length")
+		}
+		if frame.ID == 2 && !frame.Result.OK {
+			parseErr = fmt.Errorf("missing following result")
+		}
+		ids = append(ids, frame.ID)
+	})
+	if err != nil || result.ExitCode != 0 || parseErr != nil {
+		t.Fatalf("exit=%d err=%v parse=%v", result.ExitCode, err, parseErr)
+	}
+	if fmt.Sprint(ids) != "[1 2]" {
+		t.Fatalf("delivered IDs=%v, want both large and following response", ids)
+	}
+	if len(result.Stdout) > 512 || !strings.Contains(result.Stdout, `"id":2`) {
+		t.Fatal("retained output tail changed")
+	}
+}
+
+func TestRunProcessRPCFrameLimitFailsPromptly(t *testing.T) {
+	for _, mode := range []string{"stdout", "stderr"} {
+		t.Run(mode, func(t *testing.T) {
+			start := time.Now()
+			calls := 0
+			result, err := runRPCOutputHelper(t, mode, func(string) { calls++ })
+			limit := rpcTestLineLimit
+			if mode == "stderr" {
+				limit = 4 * 1024 * 1024
+			}
+			if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("line exceeds %d byte scan limit", limit)) {
+				t.Fatalf("want explicit scan limit error, got %v", err)
+			}
+			if time.Since(start) > 6*time.Second {
+				t.Fatalf("scan error did not stop live producer promptly: %s", time.Since(start))
+			}
+			if calls != 0 || result.Stdout != "" || result.Stderr != "" {
+				t.Fatal("partial rejected frame was delivered or retained")
+			}
+			if strings.Contains(err.Error(), "synthetic-private-history") || strings.Contains(err.Error(), "not-a-real-key") {
+				t.Fatal("scan error disclosed rejected frame")
+			}
+		})
+	}
+}
diff --git a/internal/workspace/rpc_transport_test.go b/internal/workspace/rpc_transport_test.go
new file mode 100644
index 0000000..42c2bc1
--- /dev/null
+++ b/internal/workspace/rpc_transport_test.go
@@ -0,0 +1,86 @@
+package workspace
+
+import (
+	"context"
+	"encoding/json"
+	"fmt"
+	"io"
+	"net"
+	"strings"
+	"testing"
+	"time"
+)
+
+func TestSSHRunnerRPCOutputLimits(t *testing.T) {
+	for _, overflow := range []bool{false, true} {
+		name := "large"
+		if overflow {
+			name = "overflow"
+		}
+		t.Run(name, func(t *testing.T) {
+			signer, key := newSSHKeyPair(t)
+			listener, _, serverErrors := startStreamingSSHServer(t, signer, func(_ string, stdin io.Reader, stdout io.Writer) error {
+				size := 5 * 1024 * 1024
+				if overflow {
+					size = 17 * 1024 * 1024
+				}
+				if err := json.NewEncoder(stdout).Encode(map[string]any{"id": 1, "result": strings.Repeat("x", size)}); err != nil {
+					return err
+				}
+				if _, err := io.WriteString(stdout, `{"id":2,"result":true}`+"\n"); err != nil {
+					return err
+				}
+				// Wait for the receiver to acknowledge the final frame by closing
+				// stdin, before the fixture closes its TCP connection.
+				_, err := io.Copy(io.Discard, stdin)
+				return err
+			})
+			runner := &SSHRunner{Host: "127.0.0.1", User: "dev", Platform: "linux", Port: listener.Addr().(*net.TCPAddr).Port, KnownHostsPaths: []string{writeKnownHosts(t, listener.Addr().String(), signer.PublicKey())}, PrivateKeyPaths: []string{writePrivateKey(t, key)}}
+			var ids []int
+			var parseErr error
+			input := make(chan string)
+			start := time.Now()
+			result, err := runner.Run(context.Background(), Command{Executable: "synthetic", Input: input, Timeout: 8 * time.Second, StdoutLineLimit: rpcTestLineLimit, OutputLimit: 512}, func(line string) {
+				var frame struct {
+					ID int `json:"id"`
+				}
+				if e := json.Unmarshal([]byte(line), &frame); e != nil {
+					parseErr = e
+				}
+				ids = append(ids, frame.ID)
+				if frame.ID == 2 {
+					close(input)
+				}
+			})
+			if overflow {
+				if err == nil || !strings.Contains(err.Error(), "line exceeds 16777216 byte scan limit") || len(ids) != 0 {
+					t.Fatalf("limit error=%v IDs=%v", err, ids)
+				}
+				if time.Since(start) > 6*time.Second {
+					t.Fatal("SSH scan failure did not terminate promptly")
+				}
+			} else if err != nil || result.ExitCode != 0 || parseErr != nil || fmt.Sprint(ids) != "[1 2]" {
+				t.Fatalf("exit=%d err=%v parse=%v IDs=%v", result.ExitCode, err, parseErr, ids)
+			}
+			if err := <-serverErrors; err != nil {
+				t.Fatal(err)
+			}
+		})
+	}
+}
+
+func TestWSLHostCommandPreservesRPCOutputLimits(t *testing.T) {
+	for _, stream := range []bool{false, true} {
+		command := Command{Executable: "synthetic", OutputLimit: 512, StdoutLineLimit: rpcTestLineLimit}
+		if stream {
+			command.Input = make(chan string)
+		}
+		host, err := wslHostCommand("synthetic-distro", command)
+		if err != nil {
+			t.Fatal(err)
+		}
+		if host.StdoutLineLimit != command.StdoutLineLimit || host.OutputLimit != command.OutputLimit {
+			t.Fatalf("WSL lost output bounds: line=%d tail=%d", host.StdoutLineLimit, host.OutputLimit)
+		}
+	}
+}
diff --git a/internal/workspace/runner.go b/internal/workspace/runner.go
index 7af448c..c0b5c2f 100644
--- a/internal/workspace/runner.go
+++ b/internal/workspace/runner.go
@@ -19,6 +19,10 @@ type Command struct {
 	Timeout time.Duration
 	// OutputLimit keeps only the latest bytes from stdout and stderr. Zero means unlimited.
 	OutputLimit int
+	// StdoutLineLimit bounds a stdout line including its delimiter. Values <= 0
+	// use 4 MiB. Stderr always uses 4 MiB. Exceeding either limit fails the run
+	// and cancels the process; no partial line is delivered to the handler.
+	StdoutLineLimit int
 	// KillTree terminates the local process tree when the command context is cancelled.
 	KillTree bool
 	// ReverseForward, when set, asks the runner to expose an AHA-side address
diff --git a/internal/workspace/ssh.go b/internal/workspace/ssh.go
index 6a095cd..7c487ad 100644
--- a/internal/workspace/ssh.go
+++ b/internal/workspace/ssh.go
@@ -226,7 +226,7 @@ func (runner *SSHRunner) execCommand(ctx context.Context, client *ssh.Client, co
 		if err != nil {
 			return Result{}, err
 		}
-		return runner.execOnClient(ctx, client, windowsBootstrapCommand(), payload, command.Input, command.OutputLimit, onLine)
+		return runner.execOnClient(ctx, client, windowsBootstrapCommand(), payload, command.Input, command.OutputLimit, onLine, command.StdoutLineLimit)
 	}
 	// Tag the remote tree so an interrupt or timeout can end it; closing the
 	// connection alone leaves the remote processes running.
@@ -243,13 +243,13 @@ func (runner *SSHRunner) execCommand(ctx context.Context, client *ssh.Client, co
 		if err != nil {
 			return Result{}, err
 		}
-		return runner.execOnClientWithCancel(ctx, client, streamBootstrapCommand(), metadata+command.Stdin, command.Input, command.OutputLimit, onLine, onCancel)
+		return runner.execOnClientWithCancel(ctx, client, streamBootstrapCommand(), metadata+command.Stdin, command.Input, command.OutputLimit, onLine, onCancel, command.StdoutLineLimit)
 	}
 	script, err := remoteScript(command)
 	if err != nil {
 		return Result{}, err
 	}
-	return runner.execOnClientWithCancel(ctx, client, "sh -s", script, nil, command.OutputLimit, onLine, onCancel)
+	return runner.execOnClientWithCancel(ctx, client, "sh -s", script, nil, command.OutputLimit, onLine, onCancel, command.StdoutLineLimit)
 }
 
 // killMarked ends the remote processes of one marked run over a second session
@@ -311,7 +311,7 @@ func (runner *SSHRunner) runRaw(parent context.Context, remoteCommand, stdin str
 		return Result{}, err
 	}
 	defer client.Close()
-	return runner.execOnClient(ctx, client, remoteCommand, stdin, nil, outputLimit, onLine)
+	return runner.execOnClient(ctx, client, remoteCommand, stdin, nil, outputLimit, onLine, 0)
 }
 
 // runScriptWithStdin runs script through `sh -c` with arguments, feeding the
@@ -380,13 +380,15 @@ func (runner *SSHRunner) runScriptWithStdin(ctx context.Context, script string,
 // execOnClient runs one command over an already-established connection. Splitting
 // it out lets a caller reuse the same connection for a reverse forward, whose
 // lifetime must match the command's.
-func (runner *SSHRunner) execOnClient(ctx context.Context, client *ssh.Client, remoteCommand, stdin string, input <-chan string, outputLimit int, onLine LineHandler) (Result, error) {
-	return runner.execOnClientWithCancel(ctx, client, remoteCommand, stdin, input, outputLimit, onLine, nil)
+func (runner *SSHRunner) execOnClient(ctx context.Context, client *ssh.Client, remoteCommand, stdin string, input <-chan string, outputLimit int, onLine LineHandler, stdoutLineLimit int) (Result, error) {
+	return runner.execOnClientWithCancel(ctx, client, remoteCommand, stdin, input, outputLimit, onLine, nil, stdoutLineLimit)
 }
 
 // execOnClientWithCancel is execOnClient with a hook that runs when the context
 // ends while the command is still running, before the connection is closed.
-func (runner *SSHRunner) execOnClientWithCancel(ctx context.Context, client *ssh.Client, remoteCommand, stdin string, input <-chan string, outputLimit int, onLine LineHandler, onCancel func()) (Result, error) {
+func (runner *SSHRunner) execOnClientWithCancel(ctx context.Context, client *ssh.Client, remoteCommand, stdin string, input <-chan string, outputLimit int, onLine LineHandler, onCancel func(), stdoutLineLimit int) (Result, error) {
+	readContext, stopOutput := context.WithCancel(ctx)
+	defer stopOutput()
 	start := time.Now()
 	session, err := client.NewSession()
 	if err != nil {
@@ -414,8 +416,13 @@ func (runner *SSHRunner) execOnClientWithCancel(ctx context.Context, client *ssh
 	stdout, stderr := newOutputBuffer(outputLimit), newOutputBuffer(outputLimit)
 	var wait sync.WaitGroup
 	wait.Add(2)
-	go scanOutput(stdoutPipe, stdout, onLine, &wait)
-	go scanOutput(stderrPipe, stderr, nil, &wait)
+	outputErrors := make(chan error, 2)
+	failOutput := func(err error) {
+		outputErrors <- err
+		stopOutput()
+	}
+	go scanOutput(stdoutPipe, stdout, onLine, &wait, stdoutLineLimit, failOutput)
+	go scanOutput(stderrPipe, stderr, nil, &wait, 0, failOutput)
 	done := make(chan error, 1)
 	go func() { done <- session.Run(remoteCommand) }()
 	exited := make(chan struct{})
@@ -428,7 +435,7 @@ func (runner *SSHRunner) execOnClientWithCancel(ctx context.Context, client *ssh
 	var runErr error
 	select {
 	case runErr = <-done:
-	case <-ctx.Done():
+	case <-readContext.Done():
 		if onCancel != nil {
 			onCancel()
 		}
@@ -446,6 +453,11 @@ func (runner *SSHRunner) execOnClientWithCancel(ctx context.Context, client *ssh
 	if ctx.Err() != nil {
 		return result, ctx.Err()
 	}
+	select {
+	case err := <-outputErrors:
+		return result, err
+	default:
+	}
 	if runErr == nil {
 		return result, nil
 	}
diff --git a/internal/workspace/wsl.go b/internal/workspace/wsl.go
index 58f574f..8bab936 100644
--- a/internal/workspace/wsl.go
+++ b/internal/workspace/wsl.go
@@ -42,7 +42,7 @@ func (runner WSLRunner) Run(parent context.Context, command Command, onLine Line
 		return Result{}, err
 	}
 	result, err := wslProcessRunner(parent, host, wslHostEnvironment(), onLine)
-	if mark != "" && (parent.Err() != nil || errors.Is(err, context.DeadlineExceeded)) {
+	if mark != "" && (parent.Err() != nil || errors.Is(err, context.DeadlineExceeded) || isProcessOutputError(err)) {
 		KillMarkedProcesses(WSLRunner{Distro: distro}, mark)
 	}
 	return result, err
@@ -61,10 +61,11 @@ func (runner WSLRunner) Run(parent context.Context, command Command, onLine Line
 // exported environment (measured: $#=0, env empty). -e execs the argv directly.
 func wslHostCommand(distro string, command Command) (Command, error) {
 	host := Command{
-		Executable:  findWSLExecutable(),
-		Timeout:     command.Timeout,
-		OutputLimit: command.OutputLimit,
-		KillTree:    command.KillTree,
+		Executable:      findWSLExecutable(),
+		Timeout:         command.Timeout,
+		OutputLimit:     command.OutputLimit,
+		StdoutLineLimit: command.StdoutLineLimit,
+		KillTree:        command.KillTree,
 	}
 	if command.Input != nil {
 		// The script carries the Turn's environment, including its capability
```
