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
| 400 | `rejected` | 「X 请求被拒绝（HTTP 400）」，提示可能不支持该协议，也可能是模型、参数或鉴权配置错误 |
| 404/405/501 | `unsupported` | 「X 不可用（HTTP 404）」，灰色，提示该网关没有提供这个协议端点、不是网络故障 |
| 5xx | `unavailable` | 「X 服务异常（HTTP 500）」，提示稍后重试或检查 Base URL/代理 |
| 超时、DNS、连接失败（没有 HTTP 响应） | `unavailable` | 「X 传输错误」，提示检查网络连接、代理或 Base URL |
| 其他 4xx | `inconclusive` | 「X 未确认（HTTP 422）」，提示展开详情 |

裸 HTTP 400 不能断言成「协议端点不存在」：只实现 Chat 的网关收到 Responses 请求可能返回
400，但未知模型、参数错误、鉴权/网关策略拒绝也可能返回 400。分类只依据状态码，因此 400 归为
`rejected` 并用中性文案；它不会触发「全部 unavailable」的自动重试，也不会显示成传输错误。
为了便于分辨原因，探测会额外读取有限、脱敏的错误响应体片段放进 `protocol_errors`，但这段
证据只用于人看，不参与分类。404/405/501 才表示该端点上没有这个协议。

## 详情与重试

- 每个实际探测过的失败候选都保留 URL 与 HTTP 状态：`protocol_errors[wire_api] = "<url>: HTTP <code>"`；
  4xx/5xx 还会附上截断、折叠空白并脱敏后的响应体片段（`"<url>: HTTP <code>: <snippet>"`）。
  URL 里的 userinfo、`api_key`/`token` 查询参数、Bearer token 和 API Key 统一替换为
  `[redacted]`，因为这段证据会返回 UI 并写入审计日志。
  Anthropic 先探测配置的 `anthropic_base_url`（未配置时用 `base_url`）；只有结果是路径类失败
  （`unsupported` 404/405/501 或 `rejected` 400）时，才继续探测 `base_url` 与 `/anthropic`。
  2xx 或 429 都视为终态成功并停止探测：429 保留 `rate_limited` 状态，同时把该候选记为
  `anthropic_base_url`，避免用户添加模型时拿到空端点。401/403、5xx/传输失败、422 等非路径
  状态保留首个候选结论，不再换候选，避免放大请求或掩盖鉴权/服务问题。
- UI 默认只显示简洁结论和可操作提示；候选 URL/HTTP 状态收在「查看详情」里。
- 全部协议为 `unavailable` 时，服务端用同一认证重试一次。`unavailable` 既包括超时/DNS/
  连接失败，也包括 HTTP 5xx 网关错误，二者都走这条重试路径。
- Responses 的 `stream:false` 探测被 400 拒绝时，会用 `stream:true` 再试同一端点，并等第一个
  SSE `data:` 事件；这是有界重试，每个端点/认证组合最多多一个请求，流读取有独立超时。空响应、
  仅心跳、或 64 KiB 内始终没有 `data:` 行都按 `inconclusive` 处理，不算 supported。
- 全部为 `unauthorized` 且认证方式不是 `auto` 时，改用 `auto`（bearer + x-api-key）重试一次。
- `unsupported`/`rejected`/`inconclusive` 不重试，避免用重试掩盖真实的协议或配置问题。
- 检测进度只按「已完成的模型数/模型总数」统计，不受分类文案影响；`supported` 与
  `rate_limited` 的协议标签照常显示。
- 协议徽标只有在上方协议结果里没有任何可用协议、且全部失败都是确认的端点缺失
  （`unsupported`）时才显示「不支持」；存在 `rejected`、`inconclusive`、鉴权失败或服务异常
  时显示中性的「未确认」。

## 代码位置

- 分类：`internal/gateway/models.go` 的 `probeStatusForCode`。
- 文案渲染：`web/src/ui_helpers.ts` 的 `protocolProbeFailures` / `renderProtocolProbeStatus`。
- 测试：`internal/gateway/models_test.go`、`web/tests/model_protocol_display.test.mjs`。

## 与「已添加模型」健康检测的区别

协议探测回答「这个网关是否提供某种 wire 协议」，已添加模型列表的「检测」按钮回答
「当前账号此刻能否成功调用这个模型」，状态包含限流、鉴权、余额不足、模型不存在等账号级结论。
接口见 `POST /api/v1/models/{id}/health-check`，说明见 `docs/model-health-check.md`。

## Backend 不是 Provider，也不是模型协议

Web 模型页底部的「AHA 支持的 Backend」列出可执行 Turn 的 agent 运行时。选择模型时，Backend
决定由哪个 Agent CLI 跑任务；Provider 只描述模型来源（Base URL 与凭据），wire 协议描述请求
格式。三者互不等同，模型行里的 `backend.protocol` 只是把当下组合展示出来。

| Backend | identifier | 支持的协议 | 官方安装/文档 |
| --- | --- | --- | --- |
| Claude Code | `claude` | Anthropic Messages | https://code.claude.com/docs/en/overview |
| Codex CLI | `codex` | Responses、Chat Completions | https://learn.chatgpt.com/docs/codex/cli |
| DeepSeek Harness | `dsh` | Responses、Chat Completions、Anthropic Messages | https://deepseek-harness.github.io/deepseek-harness/ |

安装命令按平台展示，并和官方页保持一致：

- Claude Code：Windows PowerShell `irm https://claude.ai/install.ps1 | iex`；macOS / Linux / WSL
  `curl -fsSL https://claude.ai/install.sh | bash`。
- Codex CLI：macOS / Linux `curl -fsSL https://chatgpt.com/codex/install.sh | sh`（Windows 与 npm
  安装方式以官方页为准）。
- DeepSeek Harness：需要 Node.js >= 24.9.0，执行 `npx @deepseek-ai/dsh web`。

该区块只做展示：不探测版本、不发网络请求、不改模型或 Provider 配置；安装命令以等宽文本给出，
不会自动执行。`web/src/backends.ts` 同时是运行时协议矩阵、Backend 下拉和显示名的单一来源，
实现与测试见 `web/tests/backends.test.mjs`。
