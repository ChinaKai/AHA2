# Agent API over SSH stdio（task-304 / task-314 / task-328）

远端 Workspace 的 Agent 需要访问 AHA API。原有路径是 `ssh -R` 反向隧道，失败后回退到
AHA 的局域网明文地址。本改动把 Agent API 放进 SSH 会话自带的 stdin/stdout：

只要 SSH 能登录并执行命令，Agent API 就可能可用；不需要 sshd 放开端口转发，不需要远端改配置，
也不需要 sudo。工作区里只落一个几十 KB 的 Node 脚本，不再出现完整服务端二进制。

SSH 工作区默认且无条件优先走 stdio；只有这一层失败时才回到「反向隧道 → LAN」。高级设置里不再有
stdio 开关，只保留排障用的手动覆盖地址；旧版本存下的 `stdio_proxy=0` 由 schema v134 统一改写为
1，当前版本也不再读取该列。

## 运行时选择顺序

每个 Turn 只走一层；上一层失败时把原因写进日志再进入下一层，不会静默跳过：

1. `AHA2_AGENT_PROXY_HELPER` 显式覆盖。文件被上传到工作区，按 aha2 helper 的入口执行：
   先用 `<helper> version` 探活（输出须含 `aha2`），再用
   `<helper> agent-proxy --stdio --protocol 1` 启动。它不是"裸 `--stdio` 可执行文件"，
   指向一个只有 `--stdio` 参数的脚本会在探活或启动阶段失败。这是给测试和特殊运行时留的
   逃生口，默认不使用。
2. 内嵌 Node helper。先确认工作区有可用的 node：
   - backend 命令是脚本时，读取其 shebang。`#!/usr/bin/env node` 或 `/path/to/node` 都按
     node 解释器处理，并且优先于 PATH 里的 node——backend 能跑，就说明这个解释器一定存在。
   - backend 不是 node 脚本（例如 claude 的原生 ELF）或 shebang 不是 node 时，执行
     `command -v node`。
   两者都失败时记录「工作区里没有 node」和 backend 名称，进入下一层。
3. SSH 反向隧道。既有逻辑在 backend 命令上挂 `ReverseForward`（x/crypto/ssh 的
   `tcpip-forward` 请求，等价于 `ssh -R 127.0.0.1:<port>:127.0.0.1:8766`），并先在工作区侧
   通过隧道探测 `/healthz`。sshd 拒绝转发（`AllowTcpForwarding local` 等）时，拒绝原因写进
   workspace 的检测记录（`capabilities.agent_api.tunnel_error`）并出现在回退日志里。
4. LAN HTTP 明文兜底：既有检测流程继续按候选地址探测，选到 AHA 的局域网地址后按旧行为使用。

日志依次可见：`Agent API stdio 代理不可用，尝试 SSH 反向隧道 ...：<helper 失败原因>`，
然后是 `Agent API 回退到 SSH 反向隧道 ...` 或
`Agent API SSH 反向隧道不可用（<拒绝原因>），回退 LAN 明文 ...`。

## 结构与代码

| 位置 | 作用 |
| --- | --- |
| `internal/agentproxy/frame.go` | 帧编解码、上限校验 |
| `internal/agentproxy/mux.go` | 连接号多路复用、半关闭、信用窗口背压（AHA 侧） |
| `internal/agentproxy/node_helper.js` | 工作区侧 Node helper：loopback 监听、READY、accept→帧 |
| `internal/agentproxy/node_helper.go` | `go:embed` 脚本并暴露 sha256 |
| `internal/agentproxy/client.go` | AHA 侧：解析 READY、按帧拨号 127.0.0.1:8766 |
| `internal/agentproxy/helper.go` | Go 版 helper 参考实现，仅用于显式覆盖与测试 |
| `internal/workspace/agent_proxy_helper.go` | node 探测、helper 物化、manifest 与探活 |
| `internal/workspace/agent_proxy.go` | 每条 Turn 的 SSH 会话、重连、回退 |
| `internal/app/agent_api_proxy.go` | 开关判定、逐层回退与日志 |

`cmd/aha/agent_proxy.go` 的隐藏子命令保留为显式覆盖的入口：它不被默认使用，也不随发行包
提供任何预编译 helper；只有把 `AHA2_AGENT_PROXY_HELPER` 指向实现了 `version` 与
`agent-proxy --stdio` 的 aha2 helper 时才会走到它。默认路径使用内嵌 Node 脚本，
启动命令是 `<node> <script> --stdio --protocol 1`。

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

Node helper 对 socket 读到的数据按 32 KiB 切片后再发，因为 Node 单次最多读出 64 KiB；
不切片会直接被 Go 侧判为坏帧。

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
退避 200ms/600ms；全部失败才回退，并把原因写进日志。Turn 已经开始运行后隧道再断，
不会静默换地址（URL 已经注入给 Agent），只会让在途连接 reset。

选择顺序（`prepareAgentAPIForTurn`）：

- 不是 SSH 工作区 → 完全走旧逻辑（本机直连 / WSL localhost）。
- 生效地址不是 loopback（局域网地址或外部 https）→ 不启动代理：工作区本来就能直连。
- 生效地址是 loopback → 无条件先按上面的顺序尝试 stdio（显式覆盖 → Node helper），
  失败后再走反向隧道 → LAN。
  stdio 成功时 Turn 使用 `<scheme>://127.0.0.1:<远端端口>`；scheme 沿用生效地址，
  `https://127.0.0.1` 的 TLS 会端到端透传，不会被悄悄降级成明文。
- stdio 成功时不再计算 `ReverseForward`：否则反向隧道会指向代理自己的远端端口。

backend 命令从 runtime snapshot 的 backend 名解析（`BackendBinaryResolver`，由
`internal/execution.Executor` 实现），只作为 shebang 提示；拿不到时退回 PATH 探测。

## Workspace 检测

`POST /api/v1/workspaces/{id}/detect` 的 Agent API 部分（`DetectWorkspaceAgentAPI`）对 SSH 工作区
先尝试 stdio，再按隧道 → LAN 顺序继续：

1. 用与 Turn 相同的 `EnsureAgentProxyHelper` 物化 helper。工作目录取 Workspace 的 `root_path`，
   没有 Task，所以不走「本 Task 的 scratch」这一层回退；共享目录里已有版本与 sha256 都一致的
   helper 时直接复用，不会重复上传。
2. `StartAgentAPIProxy` 起 SSH stdio 会话，然后从工作区内部通过代理 URL 请求 `/healthz`，并校验
   响应确实是 AHA2。上传 helper 本身不算通过，必须真正拿到健康响应。
3. 成功时记录 `agent_api_status=ready`、`agent_api_resolved_url=<生效 loopback 地址>`，并在
   `capabilities.agent_api` 写入 `via=stdio`、`runtime=node|exec` 与实际探测到的
   `proxy_url`。`resolved_url` 不写代理端口：每次 Turn 都会启动新 helper、端口会变。
   Turn 解析也不会把 `via=stdio` 的缓存地址当作用户可用地址：它只证明 helper 当时能到 loopback，
   反向隧道并未验证；stdio 后来失败时重新检测，再选已验证的隧道或 LAN。
4. 无论成功还是失败都会关闭 proxy/SSH 会话，helper 是会话子进程，不会留在远端；失败原因以
   `SSH stdio：<原因>` 进入阶段摘要，继续探测隧道与 LAN，成功的回退结果会保留
   `capabilities.agent_api.stdio_error`。
5. 整个 stdio 阶段有 90 秒上限（`agentAPIStdioDetectTimeout`），不会让检测请求一直卡住。

检测没有 runtime snapshot，因此 node 探测只用工作区 PATH；某个工作区若只有 backend shebang 能
证明 node 存在，Turn 仍可能成功，而检测会记录为 stdio 不可用并回退。

## helper 物化

`<task-workdir>/.aha2-context/share/agent-proxy/`：

- `manifest.json`：`protocol/version/runtime/sha256/updated_at`。`runtime` 为 `node`（内嵌脚本）
  或 `exec`（显式覆盖）。
- `aha2-agent-proxy-<version>-<sha8>.js`（Node）或 `aha2-agent-proxy-<version>-<sha8>`（exec），
  权限 0700。

复用条件：manifest 的 protocol/version/runtime/sha256 全部一致，且远端文件重新算出的
sha256 也一致。只信 manifest 不信文件是不够的——半写入或被替换的文件不能被当成合法 helper 执行。
manifest 读取在远端先查大小（>4 KiB 直接拒绝），不把工作区给的任意长度内容拉回内存。

上传顺序：临时文件 → `chmod 700` → 远端 sha256 校验 → 原子 `mv` → 保留当前与上一代 →
最后原子写 manifest。并发 ensure 允许重复上传：rename 是原子的，manifest 最后写，
读者不会看到「manifest 指向还不存在的文件」。旧一代保留一份给正在运行的 helper
（POSIX inode 语义），更早的删除；同一 glob 会顺带清掉旧版本安装留下的预编译 helper。

物化后探活：Node 用探测到的解释器执行 `node <script> --version`，输出必须含
`protocol=1`；显式覆盖执行 `<helper> version`。noexec 挂载或 node 是坏 shim 会在这里失败。

回退顺序：共享目录 → 本 Task 的 `scratch/agent-proxy` → 工作区 `/tmp/aha2-agent-proxy-<uid>`。
放在 scratch 里是为了让 `RemoveTaskScratch` 收尾时把它一起删掉，不按 Task 留副本。
回退原因写进日志。写目录前检查它不是符号链接、且属于当前用户，临时文件用 `mktemp` 创建。

共享目录不在 Task 清理范围内：`RemoveTaskScratch` 只删 `.aha2-context/<task>/scratch`，
`share/` 随工作区一起删除；scratch 里的回退副本随 Task 收尾删除。

## 安全边界

- Token 仍只经 `AHA2_AGENT_API_TOKEN`（HTTP Authorization 头）传递，不进 argv、不落日志。
- helper 只在工作区 loopback 监听随机端口；AHA 侧不新增监听端口。
- 工作区里不再有可 serve、可读本地文件与网络的完整服务端二进制；helper 只用 Node 标准库
  （`net`、`events`），只做 stdio↔loopback 转发。
- 不需要修改远端 sshd，也不需要 sudo。
- 隐藏子命令不写入文档，不参与 `serve` 参数解析。

## 验证

- `go test ./internal/agentproxy`：帧往返、坏帧/超长帧、8 路并发、半关闭、2×窗口以上的
  数据、单流故障隔离、真实 HTTP 请求、READY 超时与协议不符、接收侧超窗 reset、
  单通道 64 流上限、超长 READY 行拒绝、https scheme 保留；以及 Node helper 作为真实进程
  的 HTTP/并发/多窗口/坏帧/半截帧 EOF/退出测试（无 node 时跳过）。
- `go test ./internal/workspace`：Node helper 物化与复用、版本升级与保留、4 路并发 ensure、
  manifest 字段（protocol/version/runtime/sha256）与文件哈希一致、清理不删 `share/`、
  SSH 会话上的 Node helper 真实 HTTP 端到端、显式覆盖走 exec runtime、node 探测的
  shebang 优先/PATH 兜底/缺失原因；检测用 stdio 时 helper 缺失会先物化再探活，检测返回后
  代理端口不再响应（成功与失败两条路径都验证）。
- `go test ./internal/app`：旧配置 `stdio_proxy=0` 仍走 stdio、stdio 成功路径、显式覆盖路径、
  node 失败 → 隧道 → LAN 的选择顺序与原因、非 loopback 地址不建隧道；loopback 全局覆盖
  被标成「覆盖地址」时，隧道拒绝原因仍然保留；检测优先 stdio、stdio 失败回退并把
  `SSH stdio` 与原因写进摘要、非 SSH 不尝试 stdio、阶段超时会取消。
- `go test ./scripts`：安装器 `[InstallDelete]` 只允许 `{app}`/`{group}` 内的路径，
  并且保留删除旧 `{app}\agent-proxy` 的条目。
- 变异验证：检测跳过 stdio、Turn 跳过 stdio、检测结束不关闭 proxy、状态文案去掉 stdio
  分支，对应测试都会变红（不关闭 proxy 的变异还会因泄漏的 SSH 会话在清理阶段超时）。

不部署、不重启本机 AHA2、不改生产库、不改远端 sshd。
