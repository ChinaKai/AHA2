# 模型协议探测的请求体与 400 语义

`internal/gateway/models.go` 的 `probeWireAPI` 用最小合法请求探测
`responses`、`chat_completions`、`anthropic_messages` 三个协议。可用性写入
`capabilities`，失败细节写入 `protocol_errors`。探针请求不是“越短越好”：字段
形态必须符合官方 schema，输出预算也不能低于严格网关的最小值，否则会把可用协议
误判为不可用。

## 请求体

Responses（`POST <base>/responses`）：

```json
{"model":"<model>","input":[{"role":"user","content":[{"type":"input_text","text":"ping"}]}],"max_output_tokens":16,"stream":false}
```

- `input` 必须是消息数组，content 是带 `type` 的 `input_text` 部分。裸字符串
  `"input":"ping"` 会被严格网关以 400 拒绝。
- 输出预算统一为 16（`probeMaxOutputTokens`）。1 token 会被部分网关视为低于最小
  值或非法参数。
- 先发 `stream:false`。若该端点返回 400，再用 `stream:true` 重试同一端点，并读取
  首个 SSE 事件（`data:` 行）后正常结束；连接、响应头和首个事件的读取共享
  `probeStreamTimeout`（10s）。只有 2xx 且流能正常开始才算 `supported`。

Chat Completions（`POST <base>/chat/completions`）：

```json
{"model":"<model>","messages":[{"role":"user","content":"ping"}],"max_tokens":16}
```

Anthropic Messages（`POST <base>/messages`）：

```json
{"model":"<model>","max_tokens":16,"messages":[{"role":"user","content":"ping"}]}
```

两个非 Responses 协议同样使用 16 的输出预算；Anthropic 还带
`anthropic-version: 2023-06-01`。

## 400 的含义与证据

400 不是“协议不存在”。常见原因：

- 请求体不符合官方 schema（例如 `input` 裸字符串）；
- 输出 token 预算低于网关最小值；
- 模型不支持该协议，或模型不存在；
- 鉴权/网关策略用 400 表达（少见，优先看 401/403）。

因此 400 归类为 `inconclusive`（未确认），UI 不能据此显示“不支持”。
`probeOneEndpoint` 会读取最多 8 KiB 的错误响应，折叠空白、按 512 个字符截断，并把
API Key 替换为 `[redacted]`，写入 `protocol_errors[wire_api]`，形如
`<endpoint>: HTTP 400: {"error":...}`。判断时先看响应体：参数错误通常明确指出字段，
模型/协议不支持会提到 model/protocol，鉴权问题多为 401/403。

## 验证

- `TestProbeWireAPIUsesCanonicalRequestBodies`：断言三种请求体形状（input 数组、
  `max_output_tokens`/`max_tokens` ≥ 16、显式 `stream`）。
- `TestProbeResponsesRejectsLegacyBodyAndRetriesStreaming`：模拟严格网关——旧形态或
  非流式请求返回 400，规范体 + 流式 200 SSE 才成功，断言 responses 为 supported。
- `TestProbeResponses400EvidenceIsTruncatedAndRedacted`：断言 400 证据保留响应片段、
  截断并脱敏，不泄露 API Key。
- `TestProbeResponsesStreamingProbeHonorsTimeout`：流一直不产生事件时按超时结束。
- 变异验证：把 Responses 请求体改回 `input` 字符串 + 1 token，前两个测试变红。
