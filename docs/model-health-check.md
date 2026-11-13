# 已添加模型健康检测（task-329）

「已添加的模型」列表每行有一个「检测」按钮。它回答的是**当前账号此刻能不能调用这个模型**，
而不是「这个网关支不支持某种协议」。后者仍由检测弹窗里的协议探测负责
（`POST /api/v1/providers/{id}/model-probes`，见 `docs/model-detection.md`）。

## 接口

`POST /api/v1/models/{id}/health-check`（Owner 会话 + CSRF）

- 从模型记录读取 `provider_id`、`wire_model`、`wire_api`；`wire_api` 为空时按 backend 推断：
  `claude` → `anthropic_messages`，其他 → `responses`。
- 从 Provider 的 `credential_ref` 取 API Key；Provider 配置了 `anthropic_base_url` 时，
  Anthropic Messages 优先走那个入口。
- 发一次最小真实请求：复用协议探测的官方请求体（Responses 的 typed `input` 数组与 16 token
  预算、Chat/Anthropic 的 `max_tokens:16`）和端点形式（`base/<rel>`，base 不带 `/v1` 时再试
  `base/v1/<rel>`）。
- Responses 的非流式请求被 400 拒绝时，同一端点用 `stream:true` 重试一次并等待第一个 SSE
  `data:` 事件，避免把只支持流式 Responses 的网关（例如 Ali Token Plan）误判为 rejected。
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
- 刷新模型列表时只保留仍存在的模型的检测结果。

## 测试

- `internal/gateway/model_health_test.go`：fake transport 覆盖 200/429(+Retry-After)/401/403/
  402/400(quota)/400(参数)/400(model 缺失)/404/5xx/超时；断言请求体形状、Anthropic base 优先、
  `/v1` 端点回退、Responses 流式重试、端点/响应体/传输错误均不含 Key。
- `internal/httpapi/model_health_http_test.go`：真实 httptest 上游，断言 429 映射、Retry-After、
  响应不泄露 Key、模型配置未被修改、缺失模型 404、缺失凭据不发请求、未登录 401。
- `web/tests/model_health_display.test.mjs`：各状态文案与色调、429 不是「不可用/不支持」、
  余额不是「可用」、按钮 loading 只影响本行、详情转义、页面接线。

## 代码位置

- 分类与请求：`internal/gateway/model_health.go`。
- 接口：`internal/httpapi/model_health_handlers.go`，路由在 `internal/httpapi/server.go`。
- 前端：`web/src/api.ts`、`web/src/main.ts`（`checkModelHealthByID`）、
  `web/src/ui_helpers.ts`（`modelHealthDisplay` / `renderModelHealthCell`）。
