# 已添加模型健康检测（task-329）

「已添加的模型」列表每行有一个「检测」按钮。它回答的是**当前账号此刻能不能调用这个模型**，
而不是「这个网关支不支持某种协议」。后者仍由检测弹窗里的协议探测负责
（`POST /api/v1/providers/{id}/model-probes`，见 `docs/model-detection.md`）。

## 接口

`POST /api/v1/models/{id}/health-check`（Owner 会话 + CSRF）

- 从模型记录读取 `provider_id`、`backend`、`wire_model`、`wire_api`。存储值 `chat` 与
  `chat_completions` 等价（Provider/模型接口会把后者规范成 `chat`）；`wire_api` 为空时按
  backend 推断：`claude` → `anthropic_messages`，`codex`/`dsh` → `responses`。backend 与
  wire_api 不兼容（如 Claude + Responses）直接返回 `inconclusive`，不发请求。
- 从 Provider 的 `credential_ref` 取 API Key，并按运行时实际发送的请求头发请求：
  OpenAI 兼容 backend 始终用 `Authorization: Bearer`；Anthropic 默认用 `x-api-key`
  （`ANTHROPIC_API_KEY`），`auth_style=bearer` 时用 Bearer（`ANTHROPIC_AUTH_TOKEN`）。
  不按探测逻辑在两种头之间来回试，否则可能出现“某一种头可用但真实 Task 仍失败”的假阳性。
- 只请求真实运行时使用的端点，不做探测专用回退：
  - Responses：`provider.base_url + "/responses"`；
  - Chat：`provider.base_url + "/chat/completions"`；
  - Anthropic Messages：`(anthropic_base_url || base_url) + "/v1/messages"`（与
    `runtime_selection.go` 写入的 `ANTHROPIC_BASE_URL` 及 Claude Code SDK 追加
    `/v1/messages` 的行为一致）。
  不再尝试 `base_url + "/v1/..."` 或 `/anthropic` 候选：这些路径即使返回 200，真实 Task
  仍会失败，健康检测不能报 `ok`。
- 发一次最小真实请求：复用协议探测的官方请求体（Responses 的 typed `input` 数组与 16 token
  预算、Chat/Anthropic 的 `max_tokens:16`）。
- Responses 非流式请求被 400 拒绝、且响应体明确提到 streaming 时，同一端点用 `stream:true`
  重试一次并读取第一个 SSE `data:` 事件；普通 400 参数错误不重试。重试结果按信息量合并：
  401/402/403/404/5xx 或 SSE error 事件优先于原来的 400；SSE `data: {"type":"error",...}`
  会按配额/鉴权/模型关键词分类，不会被当成 `ok`。
- 不写数据库，不改模型配置，不自动禁用模型。

响应始终是 HTTP 200（模型不存在时仍返回 404 `model_not_found`），结果在 `health` 对象里：

```json
{
  "ok": true,
  "health": {
    "status": "rate_limited",
    "http_status": 429,
    "endpoint": "https://token-plan.example/compatible-mode/v1/chat/completions",
    "retry_after_seconds": 30,
    "detail": "{\"error\":{\"message\":\"rate limit exceeded\"}}",
    "checked_at": "2026-11-13T05:00:00Z"
  }
}
```

## 状态分类

| 条件 | status | UI 文案 |
| --- | --- | --- |
| 2xx（流式回退时需读到首个 SSE data 事件） | `ok` | 可用 |
| 429 | `rate_limited` | 限流；带 `Retry-After` 时显示「限流 · N 秒后重试」 |
| 401 / 403 | `unauthorized` | 鉴权失败 |
| 402 | `quota_exhausted` | 余额不足 |
| 400 且脱敏后响应体出现 quota/billing/余额/欠费/充值/配额等关键词 | `quota_exhausted` | 余额不足 |
| 400 且响应体明确说明 model 不存在/未找到/不支持 | `model_not_found` | 模型不存在 |
| 404 | `model_not_found` | 模型不存在 |
| 400（其余） | `rejected` | 请求被拒绝 |
| 5xx、超时、DNS、连接失败 | `unavailable` | 不可用 |
| 其他 4xx | `inconclusive` | 未确认 |

关键边界：

- 429 是限流，不是「协议不支持」，也不是「网关不可用」。
- 裸 400 不会被说成「余额不足」；只有脱敏后的响应体明确出现配额/计费措辞才归
  `quota_exhausted`。
- 402 与余额关键词优先于 400 的默认 `rejected`，但 400 + model 措辞优先于余额措辞。
- 没有 HTTP 响应的失败（超时/DNS/连接）保留传输错误原文作为 `detail`，不伪造 HTTP 状态。
- Provider 未配置 API Key 时直接返回 `unauthorized`，不向网关发请求。

## 证据与脱敏

`endpoint` 与 `detail` 离开 gateway 包前统一走 `probeRedact`：

- API Key 本体替换为 `[redacted]`；
- URL userinfo（`https://user:pass@host`）替换为 `https://[redacted]@host`；
- `api_key`/`key`/`token`/`access_token`/`auth`/`password`/`secret` 查询参数值替换；
- 响应体最多读 8 KiB、折叠空白、按 512 字符截断。

审计日志（`model.health_check`）只记录状态、HTTP 码、已脱敏端点、Retry-After 与脱敏摘要，
不记录 API Key。响应体本身不会进入模型配置或数据库。

## Web 行为

- 模型表新增「可用性」列：状态徽标 + 「检测」按钮；无结果时显示「未检测」。
- 点击后该行显示「检测中…」并禁用按钮；结果只替换该行，其他行仍可独立检测。
- 失败（网络错误、接口错误）只在该行显示「检测失败」，不弹全局错误提示。
- 状态徽标可展开「详情」，查看 HTTP 状态、端点与脱敏响应体摘要。
- 刷新模型列表时只保留仍存在的模型的检测结果。编辑/删除模型会清除该模型结果；编辑/创建/
  删除 Provider 或强制刷新模型列表会清除所有结果；每个模型带请求代次，清除后旧响应不能覆盖
  新状态。

## 测试

- `internal/gateway/model_health_test.go`：fake transport 覆盖 200/429(+Retry-After)/401/403/
  402/400(quota)/400(参数)/400(model 缺失)/404/5xx/超时；`chat`/`chat_completions`/空
  `wire_api`/大小写 backend 的端点选择；断言请求体形状、Anthropic base 优先、只使用运行时端点、
  普通 400 不重试、流式重试保留 401/402/5xx、SSE error 事件分类，以及端点/响应体/传输错误均
  不含 Key。
- `internal/httpapi/model_health_http_test.go`：真实 httptest 上游，断言 429 映射、Retry-After、
  响应不泄露 Key、模型配置未被修改、缺失模型 404、缺失凭据不发请求、未登录 401。
- `web/tests/model_health_display.test.mjs`：各状态文案与色调、429 不是「不可用/不支持」、
  余额不是「可用」、按钮 loading 只影响本行、详情转义、页面接线、编辑/Provider 变更清理旧结果。

## 代码位置

- 分类与请求：`internal/gateway/model_health.go`。
- 接口：`internal/httpapi/model_health_handlers.go`，路由在 `internal/httpapi/server.go`。
- 前端：`web/src/api.ts`、`web/src/main.ts`（`checkModelHealthByID`）、
  `web/src/ui_helpers.ts`（`modelHealthDisplay` / `renderModelHealthCell`）。
