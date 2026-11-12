# Agent API over SSH stdio（task-304）

远端 Workspace 的 Agent 需要访问 AHA API。原有路径是 `ssh -R` 反向隧道，失败后回退到
AHA 的局域网明文地址。本改动把 Agent API 放进 SSH 会话自带的 stdin/stdout：

只要 SSH 能登录并执行命令，Agent API 就可用；不需要 sshd 放开端口转发，不需要远端改配置，
也不需要 sudo，局域网里不再出现明文控制面地址。

默认关闭。开关打开前，现有「反向隧道 → LAN 回退」行为逐字节保持不变。

## 结构与代码

| 位置 | 作用 |
| --- | --- |
| `internal/agentproxy/frame.go` | 帧编解码、上限校验 |
| `internal/agentproxy/mux.go` | 连接号多路复用、半关闭、信用窗口背压 |
| `internal/agentproxy/helper.go` | 工作区侧：loopback 监听、READY、accept→帧 |
| `internal/agentproxy/client.go` | AHA 侧：解析 READY、按帧拨号 127.0.0.1:8766 |
| `cmd/aha/agent_proxy.go` | 隐藏子命令 `aha2 agent-proxy --stdio --protocol 1` |
| `internal/workspace/agent_proxy_helper.go` | helper 物化与校验 |
| `internal/workspace/agent_proxy.go` | 每条 Turn 的 SSH 会话、重连、回退 |
| `internal/app/agent_api_proxy.go` | 开关判定、Turn 接线与关闭 |

## 帧格式（protocol 1）

固定 9 字节头 + 可选负载，整数为大端：

```
type(1) | stream_id(4) | payload_length(4) | payload
```

- `OPEN(1)`：接受侧已 accept 一个 TCP 连接，通知对端拨号。负载为空。
- `DATA(2)`：流数据，单帧最大 32 KiB。
- `WINDOW(3)`：4 字节信用增量。
- `CLOSE(4)`：半关闭，表示本方向不再发数据；对端对该方向 `CloseWrite`。
- `RESET(5)`：异常终止，立即关闭整条流，不影响其他流。

长度超过 32 KiB、类型未知、控制帧长度不对，一律视为坏帧：整个多路复用器立刻拆掉，
已打开的流全部 reset。帧长度是位置性的，出错后没有可靠的重同步点，硬撑只会把错误数据
写进别的流。

两处远端输入的上限同样重要：READY 行最多 256 字节，超出按握手失败处理；每条通道最多
64 条并发流，超出的 OPEN 只回 RESET，不拨号也不建 goroutine。

## 背压与半关闭

每条流两个方向独立。发送方初始有 256 KiB 信用，每发一个 DATA 帧扣减；接收方把字节写进
自己的 TCP socket 后回 `WINDOW`。因此慢消费者最多让对端在内存里压住 256 KiB/流，
不会拖住同一通道上的其他流；单个流的写入阻塞只阻塞它自己。

接收侧按「已收未确认字节」计数：超过 256 KiB 直接 reset 该流。只靠发送方自觉是不够的，
一个不守约的对端可以让队列无限增长；超窗说明双方记账已经对不上，这条流不能再信。

`CLOSE` 只关一个方向。HTTP 请求体先结束时，Agent 侧读到 EOF 后仍能继续收响应；
响应结束后反向再发一次 `CLOSE`。两个方向都结束才释放 socket 和流号。

## 生命周期、重连与回退

每条 Turn 单独开一条 SSH 连接承载 helper（`RunnerFor(workspace).dial`，复用工作区已有的
凭据与 known_hosts），Turn 结束或失败时 `defer` 关闭该会话；helper 是会话的子进程，
stdin 收到 EOF 即退出，不会留下孤儿进程。AHA 本身崩溃时 SSH 连接断开，效果相同。

建立阶段失败（SSH 连不上、READY 超时 15s、协议版本不符、helper 退出）时最多尝试 3 次，
退避 200ms/600ms；全部失败才回退，并把原因写进日志
（`Agent API stdio 代理不可用，回退到反向隧道/LAN 流程 ...`）。回退后走原来的
`AgentAPIURLForWorkspace`，也就是隧道→LAN 的既有逻辑。Turn 已经开始运行后隧道再断，
不会静默换地址（URL 已经注入给 Agent），只会让在途连接 reset。

开关判定（`prepareAgentAPIForTurn`）：

- 开关关闭，或不是 SSH 工作区 → 完全走旧逻辑。
- 生效地址不是 loopback（局域网地址或外部 https）→ 不启动代理：工作区本来就能直连。
- 生效地址是 loopback → 物化 helper、起隧道，Turn 使用 `<scheme>://127.0.0.1:<远端端口>`。
  scheme 沿用生效地址：隧道只搬字节，`https://127.0.0.1` 的 TLS 会端到端透传，
  不会被悄悄降级成明文。
- 代理成功时不再计算 `ReverseForward`：否则反向隧道会指向代理自己的远端端口。

## helper 物化

`<task-workdir>/.aha2-context/share/agent-proxy/`：

- `manifest.json`：`protocol/version/os/arch/sha256/updated_at`
- `aha2-agent-proxy-<version>-<sha8>`，权限 0700

复用条件：manifest 的 protocol/version/os/arch/sha256 全部一致，且远端文件重新算出的
sha256 也一致。只信 manifest 不信文件是不够的——半写入或被替换的文件不能被当成合法 helper 执行。
manifest 读取在远端先查大小（>4 KiB 直接拒绝），不把工作区给的任意长度内容拉回内存。

上传顺序：临时文件 → `chmod 700` → 远端 sha256 校验 → 原子 `mv` → 保留当前与上一代 →
最后原子写 manifest。并发 ensure 允许重复上传：rename 是原子的，manifest 最后写，
读者不会看到「manifest 指向还不存在的文件」。旧一代保留一份给正在运行的 helper
（POSIX inode 语义），更早的删除。

回退顺序：共享目录 → 本 Task 的 `scratch/agent-proxy` → 工作区 `/tmp/aha2-agent-proxy-<uid>`。
放在 scratch 里是为了让 `RemoveTaskScratch` 收尾时把它一起删掉，不按 Task 留副本。
每次物化后执行 `<helper> version` 探活，noexec 挂载会在这里失败并继续回退；
回退原因写进日志。写目录前检查它不是符号链接、且属于当前用户，临时文件用 `mktemp` 创建。

工作区与 AHA 架构不一致时直接报错并回退。`AHA2_AGENT_PROXY_HELPER` 指定的预编译 helper
不参与这个比较：它的平台由提供者决定，manifest 记为工作区平台，`<helper> version`
探活仍会证明它能不能真的跑起来。Windows 宿主配 Linux 工作区就靠这条路径。

共享目录不在 Task 清理范围内：`RemoveTaskScratch` 只删 `.aha2-context/<task>/scratch`，
`share/` 随工作区一起删除；scratch 里的回退副本随 Task 收尾删除。

## 安全边界

- Token 仍只经 `AHA2_AGENT_API_TOKEN`（HTTP Authorization 头）传递，不进 argv、不落日志。
- helper 只在工作区 loopback 监听随机端口；AHA 侧不新增监听端口。
- 不需要修改远端 sshd，也不需要 sudo。
- 隐藏子命令不写入文档，不参与 `serve` 参数解析。

## 验证

- `go test ./internal/agentproxy`：帧往返、坏帧/超长帧、8 路并发、半关闭、2×窗口以上的
  数据、单流故障隔离、真实 HTTP 请求、READY 超时与协议不符、接收侧超窗 reset、
  单通道 64 流上限、超长 READY 行拒绝、https scheme 保留。
- `go test ./internal/workspace`：物化复用、版本升级与保留、4 路并发 ensure 且文件与
  manifest 一致、清理不删 `share/`、SSH 会话上的真实 HTTP 端到端、helper 缺失时有限重试后回退、
  显式 helper 跳过架构比较、超大 manifest 被覆盖。
- `go test ./internal/app`：开关关闭行为不变、开启但不可达时回退并记录原因、非 loopback 地址不建隧道。
- `go test ./internal/store ./internal/httpapi`：schema v133、设置持久化与 API 解析。
- `go test -race ./internal/agentproxy` 通过。
- `node --test web/tests/agent_api_stdio_proxy.test.mjs`：勾选框从 FormData 读取，
  以及设置保存确实发送 `stdio_proxy`。

不部署、不重启本机 AHA2、不改生产库、不改远端 sshd。
