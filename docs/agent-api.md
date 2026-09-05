# Agent API

Agent API 让本地、WSL 和 SSH Workspace 中的 Agent 通过 HTTP 使用 AHA2 控制面能力，
不要求远程环境安装 AHA CLI。

## 配置与认证

`aha2 serve --agent-api-url <url>`（或 `AHA2_AGENT_API_URL`）配置 Agent 可访问的
控制面基址。未配置时，AHA2 从监听端口生成本机 `127.0.0.1` URL。非 loopback URL
强制使用 HTTPS；仅受信开发网络可显式传 `--allow-insecure-agent-api` 临时允许 HTTP。

每个 Turn 获得独立的 `AHA2_AGENT_API_TOKEN`。它是仅内存保存的 Bearer capability，
绑定 Task、Agent 和 Turn，Turn 结束立即撤销，且不进入 Prompt、Context、数据库或日志。

```text
Authorization: Bearer <AHA2_AGENT_API_TOKEN>
```

## Turn 状态

```text
GET   /api/v1/agent/capabilities
PATCH /api/v1/agent/turn/memory
POST  /api/v1/agent/turn/messages
POST  /api/v1/agent/collaboration/batches
GET   /api/v1/agent/project/workspaces
POST  /api/v1/agent/tasks
GET   /api/v1/agent/tasks/{task}
```

Task Memory 使用追加语义，进度消息会立即写入 Conversation 并通过 Event Hub 推送。
只有 Main Agent 可以修改 Memory 和提交协作批次；协作请求提交后立即由 AHA 编排。
最终回复只包含自然语言，不再携带 Turn checkpoint。

项目 Workspace 列表和 Task 创建能力仅对 Main 开放，并强制限定到当前 Project；还要求
Owner 在 Task Main 配置中显式开启持久化的 `workspace_read/task_create/clone_hardware`
授权，默认全部关闭。
创建 Task 时可使用 `clone_hardware:true`，由服务端复制当前 Task 的硬件组和 Secret，
Secret 使用新 Task 专属引用且不会进入响应。新 Task 固定使用单 Agent 模式，并继承当前
Main 的运行时与权限上限，避免为调用 Agent API 意外关闭网络能力。

## Knowledge 与 Skill

```text
GET  /api/v1/agent/knowledge
GET  /api/v1/agent/knowledge/{id}
POST /api/v1/agent/knowledge/candidates
POST /api/v1/agent/knowledge/{id}/feedback
GET  /api/v1/agent/skills
GET  /api/v1/agent/skills/{id}
PUT  /api/v1/agent/skills/{id}
```

Knowledge 只返回当前 Project/Product Line 可用条目；更新已有条目必须携带
`base_revision`。Skill 只允许更新当前 Task 已选择的包，并要求 `base_version`；提交的是
完整文本文件集合，服务端校验相对路径、大小、UTF-8 文本和 `SKILL.md` frontmatter，
随后原子替换托管包。下一 Turn 会从新版本重新物化。

## 托管进程

```text
GET  /api/v1/agent/processes
POST /api/v1/agent/processes
GET  /api/v1/agent/processes/{name}
POST /api/v1/agent/processes/{name}/stop
```

启动请求：

```json
{"name":"dev-server","executable":"node","args":["server.js"],"cwd":".","env":{}}
```

进程由 AHA2 服务而非 Agent Turn 子进程持有，因此 Turn 结束后继续运行。进程名在
Task 内唯一，每个 Task 最多 16 个活动进程；cwd 必须位于 Task Workspace 内，输出只
保留最新 256 KiB。AHA2 服务关闭时会停止全部托管进程。当前版本不跨 AHA2 重启恢复。

## 硬件

```text
GET  /api/v1/agent/hardware
GET  /api/v1/agent/hardware/{hardware}/terminal?transport=serial|network
POST /api/v1/agent/hardware/{hardware}/connect?transport=serial|network
POST /api/v1/agent/hardware/{hardware}/disconnect?transport=serial|network
POST /api/v1/agent/hardware/{hardware}/send?transport=serial|network
POST /api/v1/agent/hardware/{hardware}/login?transport=serial|network
```

发送体与 Web API 相同：`{"data":"...","encoding":"text|hex"}`。Agent API 复用
Hardware Manager 的只读、Task 终态、传输类型和 64 KiB 限制，且不提供配置写接口。

显式 login API 使用服务端保存的凭据，支持用户名/密码/成功/失败提示集合、
`cr|lf|crlf`、连接后唤醒、3–300 秒超时和最多 3 次密码重试。Terminal status 返回
`login_status` 与脱敏的 `login_error`。
