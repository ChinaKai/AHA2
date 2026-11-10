# 本地 owner 消息 ingest

Luckfox 语音桥在宿主本地完成 STT 后，通过这个接口把文字写成 owner 消息。AHA 不接收音频，不接任何云服务。

## 开关与端点

- Feature 开关：`--local-owner-ingest` 或环境变量 `AHA2_LOCAL_OWNER_INGEST=1`。关闭时路由不注册，请求返回 404。
- 端点：`POST /api/v1/local/owner-messages`
- 只接受本机回环直连：`RemoteAddr` 必须是 loopback（含 IPv4-mapped IPv6），Host 必须是 `127.0.0.1`、`::1` 或 `localhost`。请求带 `Forwarded`、`X-Forwarded-For` 或 `X-Real-IP` 时一律拒绝，避免同机反向代理把局域网请求伪装成本地。
- query 里出现 `secret`、`token`、`password`、`passwd`、`api_key`、`apikey` 或 `credential` 时直接返回 400，不接受 query 传凭据。

请求体：

```json
{
  "task_id": "task_pet",
  "content": "把客厅的灯打开",
  "source": "voice",
  "client_request_id": "luckfox-voice-0001"
}
```

`source` 可省略，省略时按 `voice` 记录；目前只接受 `voice`。`client_request_id` 可省略，提供时用于幂等。

成功返回 `202 Accepted`：

```json
{
  "ok": true,
  "queued": true,
  "message_id": "message_...",
  "turn_id": "turn_...",
  "started": true,
  "duplicate": false
}
```

同一 `client_request_id` 重试时不会重复插入，返回 `duplicate: true` 和第一次真正入库的 `message_id`。并发请求也不会返回临时 ID；抢到幂等键的那个请求负责调度，其他请求不重复调度。响应不回显正文和 secret。

## Secret Store

- 键名：`local-owner-messages/secret`
- 接口只读这个键。缺失或为空时返回 `503 local_ingest_unavailable`；值错误时返回 `401 local_ingest_unauthorized`。
- 比较方式是先对两边做 SHA-256，再用 `crypto/subtle.ConstantTimeCompare` 比较定长摘要。
- 不复用 Web session/CSRF。query 参数里的敏感键名会被显式拒绝，secret 只从 `Authorization: Bearer` 读取。
- 限流只在认证成功后计数。错误凭据不会消耗语音桥的每分钟额度。

写入命令（AHA 需要先停止，写入后重启生效；命令从 stdin 读 secret，不回显）：

```bash
printf '%s' "$TOKEN" | aha2 local-ingest-secret --data-dir <data-dir>
aha2 local-ingest-secret --data-dir <data-dir> --clear
```

secret 至少 32 字节，只接受可打印 ASCII，不能带空白。

## 写入语义

- Task 白名单目前只有 `task_pet`。其他 Task 返回 `403 task_not_allowed`。
- 消息按 `role=user`、`sender=owner` 写入，走正常 Round/Turn 流程。
- `source` 写到 `messages.source` 和 inbox payload；`client_request_id` 写到 `messages.client_request_id`，并建唯一索引。
- 重试时用 `task_id + 规范化 content + source` 作为请求指纹。指纹不一致返回 409，不会把不同正文静默当成重复。
- 重试在调度前重新读取 Task 状态。draft、completed、cancelled、blocked 的 Task 返回 409 `task_not_runnable`，不会在终态 Task 上补起挂起消息的新 Turn。
- `ListMessages` 会读取 `source` 和 `client_request_id`，标准消息读取不再丢 v129 元数据。
- 接口不自动批准审批、不执行工具、不改变任何权限。高风险动作仍然走原有的 owner 审批。

## 限额与错误

| 条件 | 状态码 |
| --- | --- |
| 请求体超过 16 KiB | 413 `request_too_large` |
| 不是 `application/json` | 415 `unsupported_media_type` |
| JSON 非法或字段未知 | 400 `invalid_json` |
| content 去空白后为空 | 400 `content_required` |
| content 超过 4000 字符 | 400 `content_too_long` |
| content 含控制字符（保留 `\n`、`\r`、`\t`） | 400 `content_control_characters` |
| source 不是 `voice` | 400 `source_invalid` |
| client_request_id 超过 128 或含不可打印字符 | 400 `client_request_id_invalid` |
| 同一 client_request_id 用于不同 Task、或正文/source 不一致 | 409 `client_request_id_conflict` |
| 重试时 Task 已处于终态或 draft | 409 `task_not_runnable` |
| query 中出现敏感参数名 | 400 `query_secret_forbidden` |
| 认证成功后每分钟超过 30 次 | 429 `rate_limited` |

审计事件 `local.owner_message.ingest` 记录 `task_id`、`source`、`content_length`、`message_id`、`client_request_id`、`duplicate`、`started`。不记录正文，也不记录 secret。

## 威胁模型与已知限制

- 监听地址仍由现有 `--listen` 决定，AHA 默认绑定 `0.0.0.0`。这个接口靠请求级过滤只接受回环直连，不额外开一个 127.0.0.1 监听端口，也没有 Unix socket。
- 同机反向代理如果剥离 `Forwarded`/`X-Forwarded-For`/`X-Real-IP`，并把 Host 改写成 `127.0.0.1`，AHA 在 HTTP 层无法区分它和真实本地进程。当前明确接受这个风险：secret 仍是唯一的身份边界，部署时不要把该路径经反向代理暴露出去。需要更强隔离时应另开 loopback listener 或 Unix socket，本次未实现。
- Secret Store 在启动时载入内存，修改 secret 需要重启 AHA。不要在 AHA 运行时并发修改 `secrets.json`。
- 限流是单进程内存固定窗口，服务重启后清零。
- 白名单写死在 `internal/app/local_ingest.go` 的 `localOwnerIngestTaskAllowed`，新增 Task 需要改代码。
