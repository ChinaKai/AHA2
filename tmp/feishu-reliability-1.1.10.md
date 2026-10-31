# 飞书渠道可靠性修复（plugin 1.1.10）

- 触发：渠道显示 ready，但收不到消息；停用/启用后短暂恢复。定位到两个独立问题：
  1. 渠道 runtime capability 固定 24h，`TouchChannelCapability` 只更新 `last_used_at`，没有续期；插件也没有刷新端点。
  2. `oapi-sdk-go v3.9.10` 没有静默断线检测（无 read deadline / pong 超时）；半开连接下 `ReadMessage` 永久阻塞，不触发 reconnect。
- 修改：
  - `plugins/feishu/go.mod`：`oapi-sdk-go v3.9.10 -> v3.12.2`（上游 v3.11.0 `be391970` 修复静默断线）。
  - `plugins/feishu/main.go`：30s 健康心跳、`last_inbound_at`、异步 health reporter（5s 超时）、`heartbeat=1` 能力标记。
  - `internal/channel/process.go`：90s 心跳超时（启动宽限 180s）看门狗，仅对声明 heartbeat 的插件生效；插件 stderr 保留到控制面日志。
  - `internal/channel/service.go` + `internal/store/channel_runtime.go`：capability 认证成功时滑动续期；新增 `ttl_seconds`（迁移 v82，旧行默认 1200s）。
  - `internal/httpapi/channel_runtime_handlers.go`、`internal/store/channels.go`：接收并保存 heartbeat / last_inbound_at。
  - `plugins/feishu/plugin.json`：1.1.9 -> 1.1.10。
- 验证：
  - `go test -count=1 ./...`、`go vet ./...` 通过。
  - `plugins/feishu`：`go test -count=1 ./...`、`go vet ./...`、Windows amd64 交叉构建通过；插件 SHA256 `f5630739476e438efd4799ada578947e966257cc0c06a7196b47b2b4536a6f35`。
  - 未部署；真实飞书断网/重连与 24h 边界仍需部署后验收。
