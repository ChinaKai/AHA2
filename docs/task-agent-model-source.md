# Task Agent 模型源配置

Task Agent 的运行时模型由「模型源 + 模型」共同决定。Owner 通过 Task 详情页或
`PATCH /api/v1/tasks/{id}/agents/{agent}` 修改，宠物（pet）通过 MCP 工具
`aha_update_task_agent` 修改。两个入口共用同一套服务端校验、持久化和审计逻辑。

模型源切换必须真实生效：请求被拒绝、被忽略或没有产生变化时，API 不会再返回
看似成功的 200 空操作。

## 模型源

| `model_source` | 含义 | 选择方式 |
| --- | --- | --- |
| `provider`（别名 `env`） | Env Provider 模型，使用 Provider 的凭据与 Env Group | 必须提供 `model_id` |
| `official`（别名 `codex_official`） | Codex 官方账号托管模型 | 提供 `codex_account_id` + `wire_model`，或用目录中的 official 模型 `model_id` |
| `claude_native` | Claude Code 官方账号 | 提供 `wire_model`，或用目录中的 claude_native 模型 `model_id` |

`model_source` 省略时按以下顺序推断：

1. 提供了 `model_id`：使用该目录模型自身的模型源；
2. 未提供 `model_id`：沿用当前 Agent 运行时的模型源（仅适用于不切换模型的编辑）。

因此 `{"model_id": "<official 目录模型>"}` 会切换到 official，而不是继续停留在
provider。反过来，`model_source=provider` 搭配 official/claude_native 目录模型会返回
`model_source_mismatch`，不会静默忽略 `model_id`。

official 和 claude_native 只能选择当前运行时确实存在的源：Codex 账号必须已配置且在
`available_models` 中提供该 `wire_model`，Claude Native 必须存在对应的 native 模型。
官方账号不可用时返回明确错误，不会降级到 Provider。

## Owner HTTP API

```text
PATCH /api/v1/tasks/{task}/agents/{agent}
```

请求体（除 `inherit_main` 外均可选）：

```json
{
  "model_source": "provider",
  "model_id": "model_example",
  "wire_model": "gpt-5.1-codex",
  "codex_account_id": "account_example",
  "backend": "codex",
  "reasoning_effort": "high",
  "filesystem": "workspace-write",
  "approval": "never",
  "fast_mode": false
}
```

成功响应：

```json
{
  "ok": true,
  "status": "updated",
  "changed": true,
  "agent": { "task_id": "task_example", "agent_id": "main", "...": "..." }
}
```

- `status=updated`、`changed=true`：新运行时已写入 Agent 和 Task，下一个 Turn 使用。
- `status=unchanged`、`changed=false`：请求与当前运行时完全一致，没有写库，也不会
  生成重复的运行时快照。调用方应把这种情况视为“已经是目标状态”，而不是切换失败。
  子 Agent 提交 `inherit_main=false` 属于关联状态变化：即使快照内容相同也会返回
  `updated` 并落库，之后 Main 的更新不再覆盖它。

示例：Official → Provider

```bash
curl -sS -X PATCH \
  -H "Authorization: Bearer $AHA_TOKEN" \
  -H 'Content-Type: application/json; charset=utf-8' \
  -d '{"model_source":"provider","model_id":"model_example"}' \
  http://127.0.0.1:<port>/api/v1/tasks/<task>/agents/main
```

示例：Provider → Official（必须使用该账号实际提供的 wire model）

```bash
curl -sS -X PATCH \
  -H "Authorization: Bearer $AHA_TOKEN" \
  -H 'Content-Type: application/json; charset=utf-8' \
  -d '{"model_source":"official","codex_account_id":"account_example","wire_model":"gpt-5.1-codex"}' \
  http://127.0.0.1:<port>/api/v1/tasks/<task>/agents/main
```

`$AHA_TOKEN` 是 Owner 会话凭据，只应通过环境变量或凭据管理器传入，不要写进脚本、
日志或提交记录。Agent API 的 Turn Token 不能调用该 Owner 接口。

## 宠物 MCP

`POST /api/v1/pet/mcp` 只接受 loopback 请求，并且每个请求都必须携带宠物 Task 自己的
Turn capability（`AHA2_AGENT_API_TOKEN`）；loopback 地址本身不是身份，匿名、其他
Task 的 capability 以及带转发头的请求都会被拒绝。工具
`aha_update_task_agent` 新增 `model_source`，参数与 Owner API 的模型字段一致：

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "aha_update_task_agent",
    "arguments": {
      "task_id": "task_example",
      "model_source": "provider",
      "model_id": "model_example"
    }
  }
}
```

工具结果直接包含 Agent 与该次操作的状态：

```json
{
  "task_id": "task_example",
  "agent_id": "main",
  "model_source": "provider",
  "model_id": "model_example",
  "ok": true,
  "status": "updated",
  "changed": true
}
```

调用方必须携带宠物 Task 自己的 Turn capability（`Authorization: Bearer <AHA2_AGENT_API_TOKEN>`）。
loopback 入口本身不识别调用者，`create_tasks` 又是宠物全局权限，所以该写工具还会校验
Token 身份必须为 `task_pet`：无 Token、其他 Task 的合法 Token 都会在读写任何 Task 状态前
返回 `pet_identity_required`；无效或过期 Token 在入口直接返回 401
`agent_capability_invalid`，不会执行 JSON-RPC 工具。

调用仍受宠物权限约束：需要 Owner 开启 `create_tasks`（与 `aha_update_task`、
`aha_start_task` 等写操作相同）。权限不足、Task 不存在、Task 已完成/只读等都会返回
MCP `isError: true`，并在文本中给出稳定错误码，不会返回成功。

## 错误码

所有拒绝路径返回 4xx，响应体为：

```json
{"ok": false, "error": "model_source_mismatch", "message": "模型 \"...\" 属于模型源 official，不能通过 model_source=provider 选择"}
```

| `error` | HTTP | 含义 |
| --- | --- | --- |
| `model_source_invalid` | 400 | `model_source` 不是 `provider`/`official`/`claude_native`（或别名） |
| `model_source_mismatch` | 400 | 显式模型源与 `model_id` 所属模型源冲突 |
| `model_not_found` | 400 | `model_id` 不存在或已删除 |
| `model_required` | 400 | provider 模型源没有 `model_id`，且无法沿用当前 provider 模型 |
| `wire_model_required` | 400 | official/claude_native 缺少 `wire_model` 和可用目录模型 |
| `model_selection_invalid` | 400 | backend 与模型源冲突、wire_model 与目录模型不一致等 |
| `codex_account_unavailable` | 409 | 没有已配置的 Codex 账号提供该 official 模型 |
| `agent_not_found` | 404 | Task 下不存在该 Agent |
| `task_not_found` | 404 | Task 不存在 |
| `task_read_only` | 403 | Task 为只读（如跨机镜像） |
| `task_not_editable` | 409 | Task 已完成或已取消 |
| `pet_permission_denied` | 403 | 宠物未获得对应写权限（仅 MCP） |
| `pet_identity_required` | 403 | MCP 写操作未携带宠物 Task 的 Turn capability（仅 MCP） |

错误码是稳定契约；`message` 用于展示，可能措辞微调。调用方应按 `error` 分支处理。

## 审计

每次成功切换（包括由宠物发起的切换）都会写入：

- 会话审计卡 `agent_config_updated`；
- 宠物事件 `task_agent_config_updated`（仅宠物路径）；
- 服务端审计日志 `agent.update_config`。

记录字段包括 `task_id`、`agent_id`、`status`、`changed`，以及变更前后的
`model_source`、`model_id`、`wire_model`、`codex_account_id`。审计只记录模型身份，
不记录任何凭据、token、环境变量值或 Secret 引用内容。
