# Agent API

Agent API 让本地、WSL 和 SSH Workspace 中的 Agent 通过 HTTP 使用 AHA2 控制面能力，
不要求远程环境安装 AHA CLI。

## 配置与认证

Agent API 地址由 AHA 自动选择，普通 Workspace UI 不提供模式、地址或“测试连接”控件。
`aha2 serve --agent-api-url <url>`（或 `AHA2_AGENT_API_URL`）只配置可选的控制面基址：
地址必须是 `https://`，或 loopback 的 `http://127.0.0.1` / `http://[::1]` / `http://localhost`。
非 loopback 明文 HTTP 无法配置、也不会被自动探测选中；`--allow-insecure-agent-api`
和 `AHA2_ALLOW_INSECURE_AGENT_API` 已被移除，仅为旧服务定义保留为无效果的兼容参数。

未配置基址时，AHA2 从监听端口生成 loopback URL。自动选择顺序为：

1. Native Workspace：本机 loopback 直连；
2. SSH Workspace：复用已有 SSH 连接建立反向隧道，把 AHA 的 loopback 地址送到工作区；
3. WSL Workspace：WSL2 的 localhost 转发到 Windows 宿主；宿主网关地址只在基址为 HTTPS 时使用；
4. 隐藏的手动 HTTPS 覆盖：仅在高级设置中配置，作为前三者都失败时的排障出口。

旧版本保存的局域网明文地址在升级时会被禁用，并在 Workspace/高级设置中留下迁移说明；
仍被允许的旧地址（HTTPS 或 loopback）保留为隐藏覆盖。迁移不会阻止已有任务启动。

启动参数只提供初始默认值。Owner 可在“高级设置 → 其他设置 → Agent API 高级覆盖”中
持久化一个排障用基址；普通 Workspace 只显示自动选择结果（直连 / SSH 反向隧道 /
不可用及原因）。手工覆盖不会取代自动路径，只有自动探测全部失败时才使用。

每个 Turn 获得独立的 `AHA2_AGENT_API_TOKEN`。它是仅内存保存的 Bearer capability，
绑定 Task、Agent 和 Turn，Turn 结束立即撤销，且不进入 Prompt、Context、数据库或日志。

```text
Authorization: Bearer <AHA2_AGENT_API_TOKEN>
```

JSON 请求必须使用 UTF-8，并发送 `Content-Type: application/json; charset=utf-8`。
Windows PowerShell 5.1 不应直接把含中文的字符串作为 `-Body`，应显式发送 UTF-8 字节，
否则中文可能在请求发出前被永久替换成问号：

```powershell
$json = $payload | ConvertTo-Json -Depth 6
$body = [Text.Encoding]::UTF8.GetBytes($json)
Invoke-RestMethod -ContentType 'application/json; charset=utf-8' -Body $body
```

## Turn 状态

```text
GET   /api/v1/agent/capabilities
PATCH /api/v1/agent/turn/memory
POST  /api/v1/agent/turn/attachments
POST  /api/v1/agent/turn/attachments/materialize
POST  /api/v1/agent/turn/messages
POST  /api/v1/agent/collaboration/batches
GET   /api/v1/agent/project/workspaces
GET   /api/v1/agent/project/runtimes
POST  /api/v1/agent/tasks
GET   /api/v1/agent/tasks/{task}
```

Task Memory API 继续支持 `{"append":{...}}` 与 `{"replace":{...}}`，两种模式都可携带 `current_goal`。目标真实变化时必须同时提交 `goal_change_type`（`set`、`revise`、`pause`、`resume`、`complete` 或 `reopen`）与 `goal_reason`；服务端会把变更记为独立目标事件并更新 Task 的权威目标。普通补充、执行进度和结果反馈不应重复更新目标。

Task Memory 的旧 decisions/facts/progress/verification/next_actions 数组只为兼容和迁移保留，不再进入默认 Prompt 或 Web/Agent API 响应。Task Memory 页面和 Agent 状态使用当前目标及最近 20 次目标变更。普通 Turn 不应为了恢复语义而主动读取或维护旧数组；使用 replace 时必须由调用方保证携带所有仍有效内容。进度消息会立即写入 Conversation 并通过 Event Hub 推送。
只有 Main Agent 可以修改 Memory 和提交协作批次；协作请求提交后立即由 AHA 编排。
最终回复只包含自然语言，不再携带 Turn checkpoint。

### 读取本 Task 的附件

优先用 `aha2_core.attachment_materialize`；没有这个 MCP 工具时，用 `POST /api/v1/agent/turn/attachments/materialize`。两者只收 `{"attachment_id":"<附件 ID>"}`，成功时返回 `path`（执行主机上的绝对路径）、`size`（字节数）、`sha256` 和 `media_type`。不返回文件内容，也不解包。附件 ID 可从当前 Task 的附件索引取得；不接受 Task ID、文件名、目标路径或 URL。

使用当前活跃 Turn 的 Agent capability，不需要 Owner 会话。宠物和普通 Task 都只能取自己的 Task 附件，子 Agent 也只能写到自己的 scratch。服务端从受控文件存储流式复制，校验字节数和 SHA-256，在已存在的 scratch 中用随机文件名发布，不覆盖已有文件。复制失败或超限时不保留半成品；进程被强杀或主机断电时，遗留文件随 Task scratch 清理。每次成功调用都会生成一份副本，读完可自行删除；如果响应丢失，重试可能生成第二份完整副本。审计记录附件 ID、大小和结果，不记内容或路径。

每份附件默认上限为 64 MiB，可用 `aha2 serve --attachment-materialize-max-mib <1-200>` 调整，不改变上传或上下文内联上限。超限返回 HTTP `413` / `attachment_too_large`，缺失或属于其他 Task 的附件统一返回 `404` / `attachment_not_found`。MCP 以工具错误返回同样的错误码。内容和记录不符时返回 `attachment_integrity_failed`；scratch 不存在、不安全或无法写入时返回 `attachment_scratch_unavailable`。

Native 使用本机文件系统；POSIX SSH 和 WSL 用执行主机上的 `python3` 写入 scratch。Windows SSH 暂不支持，不会回退到控制面主机写文件。任何附件内容都应按不可信输入处理。

### 协作批次重复 assignment

同一个 Main Turn 内，同一个子 Agent 只能有一条 `assignment`；同一批次的 `actions`
里也不能重复出现同一个 `agent_id`。重复派发不会静默丢弃：

- 全部 action 被拒时返回 `409`，`ok=false`，`error` 固定为
  `collaboration_assignment_duplicate`，`rejected_agents` 中给出被拒绝的
  `agent_id`、`code`、`reason` 与 `message`。
  `reason` 为 `duplicate_in_turn`（该 Turn 已派发过该 Agent）或
  `duplicate_in_batch`（同一批次内重复）。同批判重按请求里的 `agent_id` 独立判断，
  即使前一个同名 action 因配置错误没有入队，后一个也会被拒绝。
- 部分 action 成功时仍返回 `409` 和同样的失败结构，但额外携带 `batch_id`、
  `created_agents` 与 `accepted_agents`；已接受的 Agent 已经入队，可以继续等待该批次，
  被拒的 Agent 需要在新的 Main Turn 重新派发。
- 正常批次仍返回 `202`。新的 Main Turn 对同一子 Agent 的 assignment 不受影响。

项目 Workspace 列表和 Task 创建能力仅对 Main 开放，并强制限定到当前 Project。
`workspace_read`、`task_create`、`clone_hardware` 三项能力对 Main 默认可用，不再要求在
Task 上显式开启持久化授权；`GET /api/v1/agent/capabilities` 仍返回这三个字段以兼容旧客户端。
创建 Task 只在请求显式携带 `clone_hardware:true` 时，由服务端复制当前 Task 的硬件组和
Secret，Secret 使用新 Task 专属引用且不会进入响应。新 Task 固定使用单 Agent 模式，并继承
当前 Main 的运行时与权限上限，避免为调用 Agent API 意外关闭网络能力。

`GET /api/v1/agent/project/runtimes` 只返回已配置 Runtime 的非敏感选择字段，不返回环境变量或凭据。
`POST /api/v1/agent/tasks` 省略 Runtime 字段时继承当前 Main；需要覆盖时，先读取 Runtime 列表，
再原样提交 `backend`、`model_source`、`model_id`、`wire_model`、`codex_account_id` 和可选的
`reasoning_effort`。服务端重新校验模型与账号，并始终继承当前 Main 的文件系统和审批权限。

Task Agent 运行时的模型源切换（Owner `PATCH /api/v1/tasks/{id}/agents/{agent}` 与宠物 MCP
`aha_update_task_agent`）的字段、校验规则、稳定错误码和审计格式见
[Task Agent 模型源配置](task-agent-model-source.md)。跨模型源切换必须显式指定
`model_source` 或目录中的 `model_id`；被拒绝或未产生变化的请求会返回明确状态，不会静默成功。

## Task 主渠道主动联系

普通 Task 已连接群聊主渠道后，活动 Main Turn 可读取 Owner 在 Task 渠道右栏选定的联调人，
包括显示名、联调身份和是否机器人，并发送一条持久化的主动协调消息：

```text
GET  /api/v1/agent/channel/contacts
POST /api/v1/agent/channel/messages
POST /api/v1/agent/channel/reply-decision
```

发送体包含稳定的重试键、正文、1–5 个内部联系人 ID，以及可选的当前 Task 草稿附件 ID：

```json
{
  "request_id": "firmware-api-blocker-1",
  "purpose": "blocker",
  "message": "接口返回字段与约定不一致，请确认最终契约和可联调时间。",
  "mention_identity_link_ids": ["channel_identity_example"],
  "attachment_ids": ["attachment_example"]
}
```

联系人必须来自当前 Task 的活动群聊主渠道，并且已经被 Owner 在渠道界面明确绑定；不要求
联系人此前发言。服务端只向 Agent 暴露内部 identity link 和显示名，飞书原始用户 ID 仅在
渠道投递边界内解析。附件必须先通过当前 Turn 的 Task 附件接口上传，最多 8 个；正文和原生
图片/文件按顺序投递。`purpose` 只接受 `blocker`；相同 Turn 使用相同 `request_id` 重试不会
重复创建消息、附件投递或 Web 路由卡片。
该接口仅用于阻塞性联调协调，不替代 `POST /api/v1/agent/turn/messages` 的常规 Web 进度更新。

对于发送者已由渠道确认是机器人的群聊 Turn，显式、receipt-bound 的
`POST /api/v1/agent/channel/replies` 本身表示 `continue`：

```text
POST /api/v1/agent/channel/replies
{"request_id":"stable-token","inbound_receipt_id":"channel_inbox_...","message":"..."}
```

若明确判断某条 task-routed 消息不需要渠道回复，则先调用：

```text
POST /api/v1/agent/channel/reply-decision
{"decision":"end","inbound_receipt_id":"channel_inbox_...","reason":"这是玩笑或确认消息，无需回复"}
```

`reason` 必须为 1 到 300 字，只在该回执对应的 AHA Web 状态卡展示，不发送到渠道。
`end` 阻止之后回复该回执；机器人对话还会按 Turn 决策停止继续外发。
`reply-decision=continue` 仍可显式设置，但普通回复不再要求额外调用。服务端始终
执行渠道配置的机器人连续对话最大轮数。
每个 task-routed 回执都必须在最终回答前显式闭环：需要回答时调用 receipt-bound
回复，不需要回答时调用 `reply-decision` 并给出针对该消息的具体理由。只返回 Web
最终回答而没有上述任一调用属于未完成渠道处理，不能在 Web 状态卡中伪装成模型主动
选择“无需回复”。
每个 task-routed 渠道回执都会在 Web 中形成一张按 receipt 去重的状态卡片，
并随处理结果更新为回复已排队、等待回复、选择不回复及模型给出的具体原因、
整轮无回复、失败或中断。真人发送者与机器人发送者均可对已投递回执作此决策。

群机器人候选不通过 Profile Sync 推导。当前渠道实例的自身机器人由运行时确认；其他机器人
只有在当前群聊消息的 mention 等渠道事件中被实际观察到后，才可作为该群的联系人。渠道
无法确认的机器人身份不得生成可 @ 候选，避免跨应用或跨租户 ID 被发送成普通文本。
飞书应用仅有群聊 @ 消息读取权限时，观察目标机器人需要用户在同一条消息中同时 @ 当前
渠道机器人和目标机器人；仅 @ 目标机器人不会向当前渠道实例投递消息事件。

## Knowledge 与 Skill

```text
GET  /api/v1/agent/knowledge
GET  /api/v1/agent/knowledge/{id}
GET  /api/v1/agent/knowledge/guide
POST /api/v1/agent/knowledge/entries
POST /api/v1/agent/knowledge/feedback
GET  /api/v1/agent/skills
POST /api/v1/agent/skills
GET  /api/v1/agent/skills/{id}
PUT  /api/v1/agent/skills/{id}
```

`GET /api/v1/agent/knowledge/guide` returns the vault usage guide at the vault root
`knowledge-base-guide.md` with `path`, `title`, full `content`
and the slim `prompt` block injected into Task Prompts. It reads the file
directly, so it still works while the index is rebuilding; when the file is
missing the endpoint returns 404 and Task Prompts use the built-in fallback.
The guide is not repeated inside the regular knowledge list.

Agent 模板只教 `entries` 与 `feedback` 两个写入端点。旧 candidates 与按 ID feedback
路径仅为读取旧表项目的兼容层保留，新流程和文档不再推荐，也不会把 vault 生效项目的
反馈写回旧库。

`GET /api/v1/agent/knowledge` marks bound project entries with `binding_mode` and
`can_propose_revision`. A `project` binding allows the current Project's Main
Agent to submit a manual review proposal back to the source library; an
`external` binding is read-only and returns `knowledge_entry_read_only` for
revision attempts. `knowledge_publish` is the Task-level capability, while
`knowledge_contribute_bound` reports whether the Project has at least one
project-collaboration binding.

`POST /api/v1/agent/skills` accepts `name`, `description`, and `instructions`. Main Agent creates an active, enabled Skill scoped to the current Project, and AHA2 automatically selects it for the current Task. It is available through the Agent API immediately and is materialized into context on the next Turn.

`POST /api/v1/agent/knowledge/entries` 是统一写入入口：`mode=create|revise`，
服务端从 Task token 解析 Project，忽略客户端传入的路径；`scope` 只接受 `project` 或
`global`（全局写入仍需 Main Agent 与 task-route 权限）。create 校验六种规范 type、
title、summary、body、tags、禁止内容以及 `search_query`/`duplicate_ack`；高相似度返回
409 与 `duplicate_candidates`。revise 需要 `entry_id` + `base_revision`，冲突返回 409。
响应中的 `source` 说明本次写入落在 `vault` 还是 `legacy`。

Agent 读写跟随 owner 可见的单一「文件知识库」总开关：开启且就绪时读写 vault，
关闭、索引未 ready、项目未绑定或读取失败时立即使用旧表，不阻塞 Turn。回退时响应中的
`warning` 说明原因，`source` 仍为 `legacy`。`--knowledge-vault-runtime-read` /
`--knowledge-vault-runtime-write` 及 `AHA2_KNOWLEDGE_VAULT_READ_PROJECTS` 只保留为支持
人员的紧急诊断覆盖，不在普通 UI 展示，也不会打开总开关。写入来源跟随实际读取来源：
项目读 vault 但 runtime write 被显式关闭时返回 409 `knowledge_vault_write_disabled`，
**不会**静默回退旧表；项目本身读旧表时该接口继续复用 proposal 流程，旧表和旧 skills 保留。

`POST /api/v1/agent/knowledge/feedback` 接受 `entry_id` 和 `kind=helped|stale|wrong`。
stale/wrong 只更新状态并进入待修订队列，不修改正文；helped 只记录正向反馈。反馈按条目
ID 解析 scope 后跟随读取源：global 条目不依赖项目绑定，只要全局知识读 vault 就写 vault；
project 条目跟随所属 Project 的读取源。vault 可读但写入关闭时返回 409，绝不回退旧表。
反馈写入后立即可被同一 Turn 的 `GET /api/v1/agent/knowledge` 读到。

Knowledge 只返回当前 Project/Product Line 可用的已发布条目；更新已有条目必须携带
`base_revision`。Agent 提交的新知识或修订统一先创建 proposal；手动评审模式保持 pending，
Owner 开启自动评审后立即尝试批准。`review_mode` 记录自动或手动来源，只有 proposal 为
approved 且 Knowledge 为 verified 才能作为当前事实。修订现有知识时，旧版本暂时标记为 stale。Owner 通过
`POST /api/v1/knowledge/proposals/{id}/approve` 或 `/reject` 审批，批准时校验基础 revision
并原子发布下一版本。`GET /api/v1/knowledge` 同时返回 `knowledge` 与 `proposals`。

Skill 只允许更新当前 Task 已选择的包，并要求 `base_version`；提交的是
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
