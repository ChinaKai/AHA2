# 模型协议检测语义（task-322）

模型检测对每个模型分别探测 Responses、Chat Completions、Anthropic Messages 三种 wire API。
每个协议的探测结果按 HTTP 状态分类，前端默认文案只描述协议是否可用，不把「协议不支持」
写成网络故障。

## 状态分类

| 探测结果 | status | UI 默认文案 |
| --- | --- | --- |
| 2xx | `supported` | 显示对应协议标签 |
| 401/403 | `unauthorized` | 「X 鉴权失败（HTTP 401）」，提示检查 API Key 或鉴权方式 |
| 429 | `rate_limited` | 视为可用（网关限流不等于协议不支持） |
| 400/404/405/501 | `unsupported` | 「X 不可用（HTTP 400）」，灰色，提示该网关没有提供这个协议端点、不是网络故障 |
| 5xx | `unavailable` | 「X 服务异常（HTTP 500）」，提示稍后重试或检查 Base URL/代理 |
| 超时、DNS、连接失败（没有 HTTP 响应） | `unavailable` | 「X 传输错误」，提示检查网络连接、代理或 Base URL |
| 其他 4xx | `inconclusive` | 「X 未确认（HTTP 422）」，提示展开详情 |

400 归入 `unsupported`，是因为网关收到了一种它不支持的协议请求体：例如只实现 Chat 的网关
收到 Responses 请求会直接返回 400。这类结果既不应触发「全部 unavailable」的自动重试，也不应
显示成传输错误。

## 详情与重试

- 每个失败协议保留候选 URL 与 HTTP 状态：`protocol_errors[wire_api] = "<url>: HTTP <code>"`。
  Anthropic 会按「配置的 anthropic_base_url → base_url → /anthropic」顺序列出每个候选。
- UI 默认只显示简洁结论和可操作提示；候选 URL/HTTP 状态收在「查看详情」里。
- 全部协议为 `unavailable`（传输失败）时，服务端用同一认证重试一次；全部为 `unauthorized`
  且认证方式不是 `auto` 时，改用 `auto`（bearer + x-api-key）重试一次。
  `unsupported`/`inconclusive` 不重试，避免用重试掩盖真实的协议不支持。
- 检测进度只按「已完成的模型数/模型总数」统计，不受分类文案影响；`supported` 与
  `rate_limited` 的协议标签照常显示。

## 代码位置

- 分类：`internal/gateway/models.go` 的 `probeStatusForCode`。
- 文案渲染：`web/src/ui_helpers.ts` 的 `protocolProbeFailures` / `renderProtocolProbeStatus`。
- 测试：`internal/gateway/models_test.go`、`web/tests/model_protocol_display.test.mjs`。
