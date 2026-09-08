# AHA2 渠道插件体系与飞书一期设计

状态：Owner 已确认，v1 已在本地源码实现；尚未部署、提交或发布。
基线：AHA2 当前源码，渠道迁移为 SQLite schema v46，核验日期 2026-09-09。

## 1. 结论

AHA2 核心增加与提供方无关的渠道领域、Owner 控制 API、系统宿主资源、入站幂等、服务端授权、Knowledge ACL、持久化 source event/outbox 和插件进程监管。飞书实现为可选的进程外插件；核心不链接飞书 SDK，也不引用飞书事件或卡片类型。插件不存在、被禁用、协议不兼容或运行失败时，只把该提供方标为不可用，不影响项目、任务、知识库、模型和现有 Web/SSE。

每个 `ChannelInstance` 固定包含两个逻辑端点：

- `assistant_dm`：唯一外部 Owner 的私聊 AHA 助手；可查询目录、预览并确认写操作、接管或退出真实 Task。
- `group_digital_human`：按“实例 + 群 + 提问人”隔离的群聊电子人；只能回答、追问或创建 Handoff，不能控制普通 Task。

渠道 Project/Workspace/Task 只是 Agent 运行宿主。身份、路由、权限、幂等、知识边界和投递状态全部以独立渠道表为准，不能从宿主资源的名称或字段推断。

## 2. 当前源码适配结论

- 启动装配集中在 `cmd/aha/main.go`，适合增加一个可空的 `ChannelService` 和独立 worker；插件监管失败不得让 `serve` 失败。
- 领域对象在 `internal/domain/`，SQLite 迁移是 `internal/store/migrations.go` 的单调版本迁移；下一版从 schema v46 开始。
- `internal/app/service.go` 已统一创建 Task、提交真实 Owner 消息、执行 Turn 和落 ConversationItem；渠道入站必须在它上面增加 `SubmitOwnerMessageWithProvenance`，把 inbox receipt、Owner message、round/inbox enqueue 与 provenance 关联放进一个幂等事务，不能旁路直接写 Task 表。
- Task 事件已持久化到 `events`，但 `EventHub` 是有界、丢满即弃的进程内通知，只适合 SSE 唤醒。可靠渠道投递必须使用新建的 durable source event/outbox。
- Prompt 已有 `core / role / identity / channel / policy / protocol` 分层，但当前 `Identity=task-agent`、`Channel=web` 是固定值。渠道 Turn 需要按可信的入站元数据选择通用渠道模板并注入结构化 `ChannelContext`。
- 当前 Knowledge 会按普通 Project 规则加载 global + applicable project verified entries；渠道宿主不能沿用该默认逻辑，必须先由 Channel Knowledge ACL 求出允许的稳定节点 ID 集合。
- Web 是零框架 TypeScript，一级导航由 `web/src/main.ts` 的 `View`、`state`、`loadAll`、`shell` 和 `render` 统一管理；“渠道”应是独立一级页面。
- AHA2 已有 Secret Store 引用模式。普通数据库只保存 `credential_ref` 与 `credential_configured`，Secret 值不得出现在 API 响应、日志、Prompt、ConversationItem、事件或普通表 JSON 中。

## 3. 飞书官方能力与一期绑定流程

### 3.1 已核验能力

飞书官方 Go SDK `v3.9.10` 已提供 `registration.RegisterApp`：它基于 OAuth 2.0 Device Authorization Grant（RFC 8628）返回十分钟有效的验证 URL，用户可直接用飞书/Lark 打开或扫码确认；确认后 SDK 自动创建应用并返回 `App ID`、`App Secret` 和扫码用户的应用作用域 `open_id`。这正是一期应采用的主流程，不再要求用户先进入开发者后台手工创建并回填凭据。

该能力还支持：

- `AppPreset` 预填名称、描述和头像，但用户在飞书确认页仍可修改；
- `CreateOnly=true` 强制只创建新应用，避免误改已有应用；
- `Addons` 预填权限、事件和卡片回调；`Addons.Preset=false` 选择最小底座，只申请 AHA2 明确列出的能力；
- 指定 `AppID` 时更新已有应用，作为显式的“绑定已有应用”分支，而不是默认路径。

SDK 返回的用户信息只有 `open_id` 与 `tenant_brand`，不返回 `tenant_key`。因此 Owner 首次绑定以 `instance_id + open_id` 为权威键；`tenant_key` 在目标应用建立长连接并收到可信事件或完成应用信息验活后补齐并做一致性校验。不能拿另一个中介应用的 `open_id` 冒充目标应用内身份。

### 3.2 一期主流程：二维码一键创建并绑定 Owner

1. 已登录的 AHA2 Owner 在“渠道”页选择飞书，创建 `draft` 实例并启动短时 `ChannelOnboardingSession`。
2. 核心向本实例插件下发 `register_app` command。飞书插件调用官方 `registration.RegisterApp`，使用 `CreateOnly=true`、AHA2 应用预设和 `Preset=false` 的最小权限清单。
3. 插件通过单次本地 Secret IPC 暂存验证 URL，command progress 只返回 URL ref 与过期时间；核心仅向发起该 onboarding 的 Web Owner 解析 ref，并用 `Cache-Control: no-store` 返回，Web 渲染二维码及“当前设备打开”按钮。Device flow 由插件主动轮询飞书，不要求 AHA2 暴露公网 OAuth callback。
4. 扫码用户在飞书官方页面审阅应用信息、权限、事件和回调并确认。拒绝、过期、租户策略/管理员审批限制都映射成明确、可重试的 onboarding 状态，不显示成已绑定。
5. SDK 返回 `ClientID`、`ClientSecret` 与扫码用户 `UserInfo.OpenID` 后，插件不得把 Secret 放入 command JSON、日志或普通 runtime HTTP。它通过核心创建的单次本地 Secret IPC 交接结果；核心把 App Secret 写入 `channel/<instance_id>/feishu/app_secret`，普通数据库只保存 App ID、Secret ref 和 configured 标志。
6. 核心在同一受控完成流程中把扫码用户建立为该实例唯一 active `owner` IdentityLink，并关联发起 onboarding 的 AHA Owner。Secret 已落但数据库提交失败时，核心清理暂存 Secret；重复完成按 onboarding ID 幂等返回原结果。
7. 插件用新凭据启动官方 Channel/WebSocket，核心逐项验证长连接、机器人收消息、发消息及卡片交互；全部通过后实例才从 `onboarding` 进入 `ready`。

最小申请集一期锁定为应用身份权限 `im:message.p2p_msg:readonly`、`im:message.group_at_msg:readonly`、`im:message:send_as_bot`，事件 `im.message.receive_v1`，回调 `card.action.trigger`，以及经真实 SDK 验活证明为卡片发送所必需的最小 CardKit 权限。实施时用官方权限目录再次校验标识；未知标识不能因为 SDK 只校验形状而被当作已配置成功。

### 3.3 明示的兼容分支

若当前租户、区域或已安装 SDK 不支持一键注册，页面必须显示 `one_click_registration_unavailable` 及平台原因，再提供“官方后台快捷创建 + 一次性录入 App ID/App Secret”的手工分支；不得静默切换。手工 Secret 仍立即进入 Secret Store，响应只返回 configured。已存在应用的更新流程也必须由 Owner 显式选择，并在飞书确认页审阅增量权限。

## 4. 进程外插件边界

### 4.1 安装与发现

渠道插件包位于 AHA2 数据目录的受管插件根目录，包含 `plugin.json`、独立可执行文件和校验信息。核心只加载 Owner 安装且校验通过的 manifest，不扫描 PATH，不执行 manifest 之外的任意命令。v1 manifest 至少包含：

```json
{
  "manifest_version": 1,
  "plugin_id": "feishu",
  "provider_key": "feishu",
  "display_name": "飞书",
  "package_version": "1.x",
  "protocol_versions": ["channel-runtime/v1"],
  "endpoints": ["assistant_dm", "group_digital_human"],
  "executable": "aha2-channel-feishu.exe",
  "sha256": "...",
  "requested_capabilities": [
    "channel.inbound.write",
    "channel.delivery.claim",
    "channel.delivery.ack",
    "channel.command.claim",
    "channel.command.progress",
    "channel.command.complete",
    "channel.secret.read",
    "channel.registration.store",
    "channel.health.write"
  ]
}
```

`ChannelPlugin.enabled=false`、文件缺失、hash 不符或协议无交集时，provider 不可创建实例；已有实例在 API 中得到派生的 `effective_availability=unavailable`，但不覆盖其持久 lifecycle state，核心其他模块照常启动。v1 由核心按实例启动一个插件进程，天然隔离实例凭据和长连接。

### 4.2 出站式控制协议

插件不监听 AHA2 可访问端口。核心提供 `/api/channel-runtime/v1`，插件主动连接并长轮询命令和投递：

- `POST /handshake`
- `PUT /instances/{instance_id}/health`
- `POST /instances/{instance_id}/commands:claim`
- `POST /commands/{id}/progress`
- `POST /commands/{id}/complete`
- `POST /instances/{instance_id}/inbound-events`
- `POST /instances/{instance_id}/deliveries:claim`
- `POST /deliveries/{id}/ack`
- `POST /deliveries/{id}/nack`

Owner 发起的 `register_app`、`validate_credentials`、`start_runtime`、`verify_installation`、`stop_runtime` 等命令先写 `channel_plugin_commands`，插件领取后回报进度或结果。Command progress 只保存短时二维码 URL ref，不保存 URL 本身；App Secret、device code、access token 等不得进入 command payload/result。

Secret 不走普通 runtime HTTP。核心为每个受管插件进程建立受 OS ACL 保护、不可继承给子进程的 `channel-secret/v1` 本地 IPC（Windows named pipe；Unix domain socket/继承 handle），只提供：按批准 ref 取本实例凭据、按一次性 onboarding nonce 暂存验证 URL，以及暂存注册结果。插件 capability 通过独立继承 pipe 注入，不放 argv、环境变量、manifest 或日志。若部署形态无法提供该本地 IPC，实例拒绝启动，不降级为明文 HTTP。

所有 runtime 请求使用 opaque instance capability、`X-AHA-Channel-Protocol: 1` 和请求级 `Idempotency-Key`。Claim 返回短租约；ACK/NACK 必须同时匹配 delivery/command ID、lease ID、instance ID。协议 major 不兼容直接拒绝，错误码稳定，不从自由文本推断行为。

### 4.3 v1 wire contract

- Handshake request 只含 `plugin_id, package_version, manifest_digest, instance_id, boot_id, supported_protocols`；响应选择唯一 protocol，并给出 command/delivery batch 上限和 lease 秒数。没有共同 major 返回 `426 channel_protocol_incompatible`。
- 所有 envelope 都带 `schema_version=1, request_id, instance_id`；服务端从 capability 重建 instance，body 中的 instance 只用于一致性校验。
- Normalized inbound 只允许 `external_event_id, event_type, occurred_at, chat_type, external_chat_id, external_sender_id, external_message_id, content, resources[], card_action`。`role/scope/owner/task_id/capability` 不是插件可提交字段。
- Core 返回 `receipt_id` 和 `duplicate`；相同 event ID + 相同 digest 返回原 receipt，相同 ID + 不同 digest 返回 `409 inbound_digest_conflict` 并告警。
- 稳定错误采用 HTTP status + machine code：`401 capability_invalid`、`403 capability_scope_denied`、`409 lease_or_state_conflict`、`410 capability_or_onboarding_expired`、`422 invalid_envelope`、`429 retry_later`。响应和日志都不回显 credential、token、原始 provider payload。
- v1 JSON body 默认上限 1 MiB；二进制只传受限 resource descriptor，下载/上传由插件 SDK 完成。未来破坏性变更发布 `/v2`，v1 字段只能向后兼容新增。

## 5. 正式领域模型

| 聚合/实体 | 职责与关键不变量 |
| --- | --- |
| `ChannelPlugin` | 已安装提供方 manifest、协议范围、启用与健康状态；插件声明只代表请求，最终 capability 由核心批准。 |
| `ChannelInstance` | 一个租户/应用级渠道实例；属于一个 AHA Owner；绑定一个系统 Project/Workspace；有独立 revision、状态和 Secret 引用。 |
| `ChannelEndpoint` | 实例内固定的 `assistant_dm` 与 `group_digital_human` 两个逻辑 Channel，可分别启停和配置。 |
| `ChannelIdentityLink` | 目标应用作用域外部用户 ID 到 AHA principal 的服务端映射；每实例最多一个 active `owner`。`tenant_key` 可后补校验，群成员只是 `participant`。 |
| `ChannelConversation` | 外部会话与系统宿主 Task 的稳定映射；DM scope 是实例 + Owner，群 scope 是实例 + group_chat_id + sender_open_id。 |
| `ChannelSession` | 会话运行 epoch、当前模式和入/出站 cursor；旧 epoch 的回调不能改变新会话状态。 |
| `ChannelTaskRoute` | 私聊接管路由；只引用目标 Task，不迁移 Task；每个 DM conversation 最多一个 active route。 |
| `ChannelSubscription` | 把目标 Task 的允许事件订阅到某个 DM conversation；保存过滤器版本与 source cursor。 |
| `ChannelPendingAction` | 写操作预览、前置条件快照、一次性确认及 provider card 绑定；确认 ID 不是授权本身。 |
| `ChannelKnowledgePolicy` | endpoint 的固定索引、默认可见范围和 policy revision。 |
| `ChannelKnowledgeGrant` | Owner 按稳定 Knowledge entry ID 授予 `node` 或显式 `subtree`；不接受路径、glob 或自由 SQL。 |
| `ChannelKnowledgeRecord` | 规范化渠道问答记录与 Knowledge entry 的映射，带提问人、问题、回答、来源、时间、可见范围和审核状态。 |
| `ChannelInboxDedup` | Provider 事件收件箱、payload digest、处理租约、处理结果与 AHA message 映射；同实例事件只消费一次。 |
| `ChannelSourceEvent` | 核心从 ConversationItem/Task 状态事务性产生的、可供渠道投影的统一持久事件。 |
| `ChannelDeliveryOutbox` | 每个会话有序的语义投递意图；插件负责把它渲染成飞书文本/卡片并 ACK/NACK。 |
| `ChannelDeliveryAttempt` | 每次领取和投递的脱敏结果、错误码与 retry-after；支持审计重试但不保存 provider 原始响应。 |
| `ChannelHandoff` | 群聊执行诉求到唯一 Owner 收件箱的状态机；未接受前绝不创建或启动普通 Task。 |
| `ChannelServiceCapability` | 插件实例级、可撤销、可轮换、最小权限的长期凭证登记；数据库仅保存 hash 与 Secret ref。 |
| `ChannelOnboardingSession` | Device-flow 一键注册/手工兼容分支、二维码进度、扫码身份、Secret 暂存、验证步骤与过期状态。 |
| `ChannelPluginCommand` | 核心到插件的 durable 命令队列，支持租约、幂等与断点恢复。 |

### 5.1 关键状态机

- Instance：`draft -> onboarding -> ready <-> degraded -> disabled`；不可恢复错误进入 `error`，删除单独走确认流程。
- Route：`pending -> active -> exited | superseded | revoked`。退出只改变 route/subscription/session，不改变目标 Task。
- PendingAction：`pending -> executing -> succeeded | failed`，或 `pending -> cancelled | expired | superseded`；只有 `pending` 能原子消费一次。
- Handoff：`pending_owner -> accepted_todo | accepted_task | dismissed | expired`；`accepted_task -> task_created`。一期“待办”就是保留在渠道收件箱的 `accepted_todo` Handoff，不虚构当前 AHA2 尚不存在的通用 Memo/Todo 模型。
- Outbox：`pending -> leased -> delivered`；失败回 `pending`，超过策略阈值为 `dead_letter`；Owner 可显式 `replay` 或 `skip`，两者都有审计记录。

## 6. SQLite schema v46

时间统一存 UTC RFC3339Nano `TEXT`，布尔为 `INTEGER`，枚举使用 `CHECK`，外键保持 `PRAGMA foreign_keys=ON`。JSON 只保存非 Secret 的版本化配置或语义 payload。

| 表 | 主要列 | 核心约束/索引 |
| --- | --- | --- |
| `channel_plugins` | `id, provider_key, display_name, manifest_version, package_version, protocol_min, protocol_max, executable_path, executable_sha256, manifest_json, install_state, enabled, last_error, discovered_at, updated_at` | `provider_key UNIQUE`；状态仅 `installed/missing/invalid/incompatible`。 |
| `channel_instances` | `id, plugin_id, owner_id, runtime_device_id, name, status, revision, app_id, provider_tenant_id, credential_ref, credential_configured, host_project_id, host_workspace_id, config_json, last_seen_at, created_at, updated_at` | `(owner_id,plugin_id,name) UNIQUE`；`owner_id/projects/workspaces` FK；`revision >= 1`；runtime 固定到本设备。 |
| `channel_endpoints` | `id, instance_id, kind, enabled, config_json, created_at, updated_at` | `(instance_id,kind) UNIQUE`；kind 仅两种。 |
| `channel_identity_links` | `id, instance_id, owner_id, provider_tenant_id, external_user_id, union_id, role, display_name, status, linked_at, revoked_at` | `(instance_id,external_user_id) UNIQUE`；partial unique `(instance_id) WHERE role='owner' AND status='active'`；owner role 必须有 `owner_id`，participant 的 `owner_id` 为空。 |
| `channel_conversations` | `id, instance_id, endpoint_id, scope_key_version, scope_key, external_chat_id, external_sender_id, owner_identity_link_id, host_task_id, status, created_at, updated_at` | `(endpoint_id,scope_key_version,scope_key) UNIQUE`；`host_task_id UNIQUE`；scope key 由核心对长度前缀元组做 HMAC 计算，不信任插件传值。 |
| `channel_sessions` | `id, conversation_id, generation, mode, status, inbound_cursor, outbound_cursor, started_at, closed_at` | `(conversation_id,generation) UNIQUE`；partial unique active session；mode 为 `assistant/group_qa/task_route`。 |
| `channel_task_routes` | `id, instance_id, conversation_id, target_task_id, state, revision, pending_action_id, activated_at, exited_at, exit_reason` | partial unique active `(conversation_id)`；普通 Task 原属 Project/Workspace 不变。 |
| `channel_subscriptions` | `id, instance_id, conversation_id, route_id, source_task_id, kind, filter_version, filter_json, source_cursor, state, created_at, updated_at` | kind=`conversation_host/task_route/owner_global`；宿主会话、active route 和 owner_global 分别唯一；owner_global 的 `source_task_id` 为空；cursor 只在 source event 与 outbox 同事务提交后前移。 |
| `channel_pending_actions` | `id, instance_id, conversation_id, actor_identity_link_id, operation, target_type, target_id, intent_json, preview_json, precondition_json, precondition_hash, status, provider_message_id, expires_at, consumed_at, created_at, updated_at` | 一次性状态 CAS；callback 必须同时匹配 actor、conversation、provider message 和未过期前置条件。 |
| `channel_knowledge_policies` | `id, instance_id, endpoint_id, fixed_index_entry_id, default_visibility, revision, created_at, updated_at` | `endpoint_id UNIQUE`；默认 group 为 `conversation_only`。 |
| `channel_knowledge_grants` | `id, policy_id, knowledge_entry_id, grant_scope, granted_by_owner_id, created_at, revoked_at` | active `(policy_id,knowledge_entry_id,grant_scope) UNIQUE`；只接受已存在稳定 ID。 |
| `channel_knowledge_records` | `id, instance_id, conversation_id, knowledge_entry_id, requester_identity_link_id, question, answer, source_json, visibility, authority_status, occurred_at, created_at` | `knowledge_entry_id UNIQUE`；authority 默认 `observed`，不得自动变 `verified`。 |
| `channel_inbox_dedup` | `id, instance_id, external_event_id, event_type, payload_digest, normalized_payload_json, state, lease_id, lease_until, conversation_id, aha_message_id, outcome, occurred_at, received_at, processed_at` | `(instance_id,external_event_id) UNIQUE`；同 payload 返回原结果，ID 相同但 digest 不同进入安全错误。 |
| `channel_source_events` | `sequence INTEGER PK AUTOINCREMENT, id, source_key, source_revision, task_id, round_id, turn_id, conversation_item_id, event_class, event_type, semantic_payload_json, occurred_at` | `id UNIQUE`、`(source_key,source_revision) UNIQUE`；stream item 每次更新递增 revision，Task transition 使用稳定 source key。 |
| `channel_delivery_outbox` | `id, instance_id, conversation_id, subscription_id, source_event_sequence, stream_sequence, replay_generation, replay_of_id, idempotency_key, coalesce_key, payload_version, semantic_payload_json, state, attempts, first_attempt_at, available_at, lease_id, lease_until, provider_message_id, last_error_code, outcome_certainty, created_at, updated_at, delivered_at` | `idempotency_key UNIQUE`、`(conversation_id,stream_sequence,replay_generation) UNIQUE`；只能领取该会话最小未完成序号/代。 |
| `channel_delivery_attempts` | `id, delivery_id, attempt_no, lease_id, outcome, error_code, retry_after_ms, provider_request_id, started_at, finished_at` | `(delivery_id,attempt_no) UNIQUE`；只存稳定错误码和脱敏 request ID。 |
| `channel_handoffs` | `id, instance_id, origin_conversation_id, origin_inbox_id, requester_identity_link_id, owner_conversation_id, summary, details, source_json, state, decision, accepted_action_id, created_task_id, created_at, updated_at, resolved_at` | origin idempotency unique；接受者必须是该实例唯一 active Owner。 |
| `channel_service_capabilities` | `id, plugin_id, instance_id, token_hash, token_secret_ref, scopes_json, status, issued_at, expires_at, last_used_at, revoked_at, rotated_from_id` | token hash unique；raw token 只在 Secret Store；实例/插件/scope 每次请求都校验。 |
| `channel_onboarding_sessions` | `id, instance_id, owner_session_id, mode, registration_command_id, verification_url_secret_ref, status, step, secret_stage_ref, scanner_external_user_id, expires_at, consumed_at, created_at, updated_at` | 一次性、短时、绑定发起 Web Owner；verification URL/Secret 暂存只保存 ref。 |
| `channel_plugin_commands` | `id, instance_id, kind, idempotency_key, payload_json, progress_json, state, attempts, available_at, lease_id, lease_until, result_json, last_error_code, created_at, completed_at` | `(instance_id,idempotency_key) UNIQUE`；progress/result 都禁止 Secret。 |

宿主删除保护由应用服务强制：`channel_instances.host_project_id/host_workspace_id` 和 `channel_conversations.host_task_id` 仍被 active 实例引用时，普通删除 API 返回 `409 managed_channel_resource`。禁用插件不删除历史。实例删除必须先预览，确认后停进程、撤销 capability、清理 Secret、关闭 route/subscription，再按保留策略归档或删除宿主。

## 7. Owner 控制 API v1

浏览器 API 沿用 Session + CSRF + Origin 校验，并在未来多 Owner 场景中仍检查 `channel_instances.owner_id == session.owner_id`。响应使用 ETag（实例/策略/route revision）；状态性修改要求 `If-Match`，创建类要求 `Idempotency-Key`。

| Method | Path | 语义 |
| --- | --- | --- |
| `GET` | `/api/v1/channel-providers` | 列出已安装 manifest、启用、兼容和健康状态；无插件返回空数组。 |
| `PATCH` | `/api/v1/channel-plugins/{id}` | Owner 启停已安装插件，需 `If-Match`；停用会停实例进程并撤销 capability，但不删除历史。 |
| `GET/POST` | `/api/v1/channel-instances` | 列出/创建 draft 实例；不因插件缺失影响其他 API。 |
| `GET/PATCH` | `/api/v1/channel-instances/{id}` | 读取/更新非 Secret 配置，PATCH 需 `If-Match`。 |
| `POST` | `/api/v1/channel-instances/{id}/onboarding-sessions` | 启动一键注册，返回 onboarding ID；创建类要求 `Idempotency-Key`。 |
| `GET` | `/api/v1/channel-onboarding-sessions/{id}` | 仅发起 Owner 读取状态；二维码就绪时返回短时 verification URL，响应 `no-store`。 |
| `POST` | `/api/v1/channel-onboarding-sessions/{id}/cancel` | 幂等取消 SDK polling、撤销 nonce 并清理暂存 Secret。 |
| `PUT` | `/api/v1/channel-instances/{id}/credentials` | 仅兼容分支接收已有应用 Secret 并立即写 Secret Store；响应只返回 configured。 |
| `GET/PUT` | `/api/v1/channel-instances/{id}/knowledge-policy` | 读写固定索引和稳定 Knowledge ID grants，PUT 需 ETag。 |
| `POST` | `/api/v1/channel-instances/{id}/actions/preview` | 对 create-task/takeover/exit/status-change/delete 等生成服务端预览和前置条件。 |
| `POST` | `/api/v1/channel-actions/{id}/confirm` | 原子消费一次性 action；重新校验 Owner、会话、provider card、ETag 和目标状态。 |
| `POST` | `/api/v1/channel-actions/{id}/cancel` | 幂等取消并使卡片终态化。 |
| `GET` | `/api/v1/channel-instances/{id}/handoffs` | Owner 收件箱和 accepted_todo 列表。 |
| `POST` | `/api/v1/channel-handoffs/{id}/actions/preview` | 预览“保留为渠道待办 / 创建 Task / 忽略”。 |
| `GET` | `/api/v1/channel-instances/{id}/deliveries` | 查看 pending/dead-letter，脱敏错误。 |
| `POST` | `/api/v1/channel-deliveries/{id}/replay` | Owner 显式重放：原记录标为 replayed，新记录沿用 stream sequence、递增 replay generation 并生成新 idempotency key。 |
| `POST` | `/api/v1/channel-deliveries/{id}/skip` | Owner 明确跳过 dead-letter 并解除本会话顺序阻塞；审计不可删除。 |

飞书卡片回调不直接调用这些 Owner Cookie API。插件把回调作为 inbound event 提交；核心根据 instance capability 定位实例，再按 PendingAction 的 Owner/会话/card 绑定执行同一应用服务方法。所有列表默认只返回当前 Web Owner 的实例；即使未来 AHA2 支持多 Owner，也不得依赖前端过滤。

## 8. 权限与 capability

### 8.1 三道服务端边界

1. **Web Owner**：Session/CSRF/Origin + instance ownership。当前 AHA2 只有一个 Owner，但不能把“已登录”等价为未来所有实例可访问。
2. **Channel plugin service capability**：长期但可撤销，严格绑定 `plugin_id + instance_id + scopes + expiry`。raw token 由 Secret Store 持有，启动时经继承匿名管道交给插件，不放 argv、manifest 或日志。
3. **Channel host Turn capability**：仍是现有 Turn 级短期 token，但 capability 集由宿主类型决定；它不能替代插件 capability，也不允许插件持有。

Capability 随实例阶段收窄：`draft/onboarding` 只签发 handshake、command、onboarding Secret 暂存和 health；Owner 成功绑定后撤销该 token，轮换为 runtime token，才加入 credential read、inbound 和 delivery scope。禁用实例、manifest/hash 变化、插件升级或 Owner 重绑都会立即撤销并轮换；过期 token 不靠进程重启失效。

### 8.2 最小 capability 集

| 主体 | 允许 | 明确禁止 |
| --- | --- | --- |
| 飞书插件实例 | 本实例 inbound、command progress/complete、delivery、health；经本地 Secret IPC 读取批准 ref 或一次性暂存注册结果 | 任意 Project/Task/Knowledge API、其他实例、Owner session、Agent 控制、Secret 枚举或普通 HTTP Secret 传输。 |
| 私聊助手 Turn | Owner 可见的 project/workspace/task 查询；channel action preview；创建 Handoff/候选写操作 | 直接写数据库、读取 Secret、绕过 confirm、跨 Owner、删除/部署/发布。 |
| 接管后的私聊入站 | 核心以内部联系调用把消息作为真实 Owner 消息提交给目标 Task | 插件直接提交 Task message；Prompt 自己决定目标 Task。 |
| 群聊电子人 Turn | 回答、追问、`handoff.create`、读取 ACL 允许的知识节点 | create/complete/reopen/interrupt Task，项目写入，通用 Agent Control API，Owner 私聊数据。 |

Prompt 中可展示 capability 名称帮助模型选择动作，但服务端始终重新鉴权、校验资源归属和状态；Prompt 不是授权源。

## 9. 关键时序

### 9.1 一键创建与 Owner 绑定

```mermaid
sequenceDiagram
  participant O as AHA2 Owner
  participant C as Channel Core
  participant DB as SQLite/Secret Store
  participant P as Feishu Plugin
  participant F as Feishu RegisterApp
  O->>C: POST onboarding-sessions (Idempotency-Key)
  C->>DB: create session + register_app command
  P->>C: claim command (instance capability)
  P->>F: RegisterApp(CreateOnly, minimal Addons)
  F-->>P: verification URL + expiry
  P->>C: Secret IPC stores URL; progress carries ref
  C-->>O: owner-only no-store QR status
  O->>F: scan, review and confirm
  F-->>P: App ID + App Secret + scanner open_id
  P->>C: one-time local Secret IPC handoff
  C->>DB: stage Secret; CAS bind unique Owner; save refs
  P->>F: start Channel/WebSocket and verify
  P->>C: command complete + health
  C->>DB: instance=ready
```

若唯一 Owner partial index 冲突、onboarding 已取消/过期、发起 Web Owner 不匹配或 Secret IPC nonce 已消费，完成操作全部失败并清理暂存；不会用“最后一次扫码者”覆盖原 Owner。

### 9.2 入站与会话映射

```mermaid
sequenceDiagram
  participant F as Feishu
  participant P as Feishu Plugin
  participant C as Channel Core
  participant DB as SQLite
  participant A as App Service
  F->>P: message/card event
  P->>P: SDK validate + normalize
  P->>C: POST inbound-events (instance capability, event_id)
  C->>DB: INSERT inbox dedup + normalized payload
  alt duplicate same digest
    DB-->>C: existing receipt/outcome
  else new event
    DB-->>C: committed receipt
  end
  C-->>P: 202/200 within provider deadline
  P-->>F: ACK
  C->>DB: claim receipt; resolve server-side identity and scope
  alt DM actor is not unique Owner
    C->>DB: mark rejected; optional fixed denial outbox
  else DM with active route
    C->>A: SubmitOwnerMessageWithProvenance(target Task, receipt ID)
  else DM assistant / group mention
    C->>A: SubmitOwnerMessageWithProvenance(host Task, receipt ID)
  end
  A->>DB: idempotent transaction: message + round/inbox + provenance + receipt processed
```

核心自己计算 scope：DM 元组=`instance_id,owner_identity_link_id`；group 元组=`instance_id,external_chat_id,external_sender_id`，再用 Secret Store 中的 scope pepper 做版本化 HMAC。插件提供的 scope key、role、Owner 标志均不可信。应用服务以 `channel_inbox_dedup.id` 为提交幂等键；处理进程在事务提交后崩溃，恢复时只能读回同一 `aha_message_id`，不能再生成一条 Owner 消息。

### 9.3 接管

```mermaid
sequenceDiagram
  participant O as Feishu Owner
  participant P as Plugin
  participant C as Channel Core
  participant DB as SQLite
  O->>P: 查询并选择 Task
  P->>C: inbound intent/selection
  C->>C: owner + task visibility + route preconditions
  C->>DB: create PendingAction(preview, target snapshot, expiry)
  C->>DB: enqueue confirmation card
  P-->>O: preview card
  O->>P: click Confirm
  P->>C: inbound card callback
  C->>C: recheck actor, conversation, card id, action state, Task snapshot
  C->>DB: transaction consume action; supersede old route; create active route + subscription; session mode=task_route
  C->>DB: enqueue route-entered result
  P-->>O: terminal card
  Note over C,DB: Target Task Project/Workspace never changes
```

### 9.4 退出 Task

```mermaid
sequenceDiagram
  participant O as Feishu Owner
  participant C as Channel Core
  participant DB as SQLite
  O->>C: request exit / click exit
  C->>DB: create one-time exit preview: only unbind
  O->>C: confirm bound action
  C->>DB: transaction route=exited; subscription=closed; session mode=assistant
  C->>DB: enqueue exit result
  Note over C,DB: no CompleteTask, InterruptTurn or Task move
```

### 9.5 出站、去重、顺序与重试

```mermaid
sequenceDiagram
  participant A as App/Store transaction
  participant S as Channel Source Projector
  participant DB as SQLite
  participant P as Plugin
  participant F as Feishu
  A->>DB: persist allowed ConversationItem/Task transition + revisioned ChannelSourceEvent
  S->>DB: read after subscription cursor
  S->>DB: transaction filter/coalesce; INSERT outbox(idempotency); advance cursor
  P->>DB: claim lowest stream_sequence with lease
  P->>F: render and send with idempotency context
  alt success
    P->>DB: ACK(lease, provider_message_id)
    DB->>DB: delivered; advance session outbound cursor
  else rate limit/transient
    P->>DB: NACK(code, retry_after)
    DB->>DB: pending; attempts++; available_at=backoff
  else permanent
    P->>DB: NACK(permanent code)
    DB->>DB: dead_letter; later rows remain ordered/explicitly skipped by Owner
  end
```

重试采用 provider `Retry-After` 优先，否则 full-jitter 指数退避（1s、5s、30s、2m、10m、1h 上限），默认 12 次后 dead-letter。租约到期自动回收。同会话严格按 `(stream_sequence,replay_generation)`，不同会话可并行；dead-letter 默认阻塞该会话后续序号，Owner 明确 skip 或 replay 后才能继续。Outbox 至少保留 30 天，delivered receipt 至少保留 7 天。Replay 原子地把原记录标为 `replayed`，并创建同一 stream sequence、下一 generation 的新 delivery；因此重放先于后续序号，且不会把原记录改回 pending。

飞书 create/reply message body 都支持 `uuid` 请求去重。插件必须把 AHA2 delivery idempotency key 稳定映射为合法 UUID，并在每次重试复用；官方 Go SDK 高层 Channel `Send` 当前没有暴露调用方 UUID，因此一期收件/归一化可复用 Channel 模块，可靠出站要使用底层 `im.v1.message.create/reply` 适配器或经测试的上层补丁。若请求得到明确 429/5xx，可按策略重试；若连接在响应前断开且可能已被飞书接受，只能在 provider 去重窗口内自动重试，超窗后进入 `dead_letter + outcome_certainty=unknown`，禁止盲发。AHA2 保证自身队列 exactly-once 投影和 provider 窗口内幂等，但不虚假承诺跨越外部平台去重窗口的绝对 exactly-once。

### 9.6 可推送事件与重复抑制

允许：主 Agent 普通回复、`agent_message_update`、可读 `agent_progress`、`agent_error`、`waiting_user`、Task 终态。拒绝：usage/token、tool/internal action、AHA action envelope、协作路由/inbox、Secret/Prompt/原始事件。

每个 source event 先转成 provider-neutral semantic payload。`agent_message_update` 对同一 ConversationItem 的多次 upsert 以递增 source revision 入流，最终回复通过 coalesce key 关闭/替换未送达 update，不能因 `(conversation_item_id,event_type)` 去重而吞掉后续更新。终轮回复与 `waiting_user/terminal` 使用同一 `coalesce_key=task/round/final`，形成“一条回复 + 状态控制”。

每个 ready 实例的 Owner DM 有一个 `owner_global` subscription，用于所有 Owner 可见普通 Task 的状态通知；active takeover route 另建 `task_route` subscription。同实例同 Task 存在 active takeover 时，投影器抑制 `owner_global` 的重复状态，只保留 takeover 流。抑制在 source event -> outbox 的同一事务规则中完成，不靠插件猜测文本。

## 10. 群聊 Handoff

群 Agent 判断需要执行时，只能调用 `handoff.create`。核心校验它来自 `group_digital_human` 宿主 Turn，按 `origin_inbox_id` 幂等创建 Handoff，并投递到唯一 Owner DM；没有已绑定 Owner 或 Owner DM 时保持 `pending_owner` 并在 Web 渠道页告警，绝不自动开工。

Owner 对 Handoff 的三个动作都先生成预览并一次性确认：

- `accepted_todo`：保留为渠道待办，可后续再次选择创建 Task。
- `accepted_task`：选择真实 Project/Workspace/runtime，确认后创建普通 Task；创建成功才进入 `task_created`，并可选择是否启动第一轮。
- `dismissed`：终止该 Handoff，不删除原始群聊证据。

群聊后续回复只在 Owner 明确选择回群或 Task 的公开结果满足安全策略时产生；一期不把普通 Task 全量对话自动暴露给群成员。

## 11. Knowledge ACL 与 ChannelContext

### 11.1 ACL 求值

每个 group endpoint 固定一个渠道知识根/索引。Turn 开始前，服务端按以下顺序求交集：

1. 当前 instance、endpoint、conversation 和 actor 必须 active。
2. 加入固定渠道索引和当前 conversation 的 `conversation_only` 记录。
3. 加入经人工整理、状态已 verified、visibility=`instance_shared` 的渠道知识。
4. 加入 Owner grant 的稳定 Knowledge entry ID；`node` 只含该节点，`subtree` 明确展开当前有效后代。
5. 再应用 Knowledge 自身 status/product-line/bound-project 约束；任一层拒绝即不可读。

Agent 只收到允许节点的索引。正文通过 Turn-scoped `/api/v1/agent/channel-knowledge/{entry_id}` 按需读取并再次执行同一 ACL。API 不接受文件路径，物化目录也不包含未授权正文。问答记录创建为 `observed` 且默认 `conversation_only`；只有 Owner/Knowledge 审批流程整理并验证后，才可成为 `instance_shared`，绝不由回答成功自动晋升权威事实。

规范化记录至少包含 requester identity、question、answer、source event/message、occurred_at、visibility、authority status。日志只使用 identity/chat hash；Prompt 使用内部 opaque ID 和必要显示名，不包含 tenant token、App Secret、authorization code、raw open_id/chat_id。

### 11.2 ChannelContext

渠道入站通过 Inbox provenance 生成只读结构，不写入普通用户文本：

```json
{
  "schema": "aha.channel-context/v1",
  "instance_id": "channel_instance_...",
  "provider": "feishu",
  "endpoint": "assistant_dm | group_digital_human",
  "conversation_id": "channel_conversation_...",
  "session_generation": 3,
  "actor": {
    "identity_link_id": "channel_identity_...",
    "role": "owner | participant",
    "display_name": "sanitized"
  },
  "route": {
    "mode": "assistant | group_qa | task_route",
    "target_task_id": "optional"
  },
  "knowledge_policy_id": "channel_knowledge_policy_...",
  "advertised_capabilities": ["non_authoritative_display_only"]
}
```

Prompt Engine 将 Identity 切换为核心维护的 `channel-assistant` 或 `channel-digital-human`，Channel 切换为通用 `external-channel` 模板。提供方只负责渲染和 ID 转换，不注入授权文字；同一普通 Task 从 Web 和飞书进入时继续共用同一 Task conversation/backend session。

### 11.3 设备与 Profile Sync 边界

一期 `ChannelInstance`、identity/conversation/session/route、onboarding、capability、inbox/outbox、Handoff 和渠道问答记录均为运行设备本地状态，不进入现有 Profile Sync；App Secret 也不加入通用 Secret bundle。渠道系统宿主 Project/Workspace/Task 虽在本机普通项目列表可见，但 sync exporter 必须通过独立渠道映射识别并排除整棵宿主运行图，避免另一设备重复启动连接或同步外部私聊内容。

接管候选只能是当前设备可写、非 remote mirror、非渠道宿主的普通 Task；服务端 preview 与 confirm 两次检查。普通 Task 本身继续遵守既有同步规则，但 route/outbox 永远属于当前 runtime device。Owner allowlist 指向的 Knowledge 节点若本机不存在、stale、未 verified 或不再满足绑定关系，ACL 取交集后拒绝，不因远端曾存在而放行。跨设备渠道主从/迁移需要单独的 lease 与 Secret 迁移协议，不在一期隐式实现。

## 12. 一期实施拆分与验收

1. **P1 核心骨架**：schema v46、domain/store、manifest loader、可空 ChannelService、provider/instance 只读与 CRUD、Profile Sync 排除规则；Web 增加“渠道”和无可用提供方页。验证插件目录为空、无效和禁用时 Go/Web 全量通过，渠道宿主图不进入 sync outbox。
2. **P2 capability 与插件协议**：实例进程监管、持久 capability、command queue、health、协议 contract tests；验证跨实例、越权 scope、撤销、轮换、过期和协议不兼容。
3. **P3 宿主与入站**：系统 Project/Workspace/Task、两个 endpoint、Owner 唯一约束、DM/group scope、inbox dedup、统一 Task 提交、ChannelContext。验证重复/乱序/崩溃恢复和系统资源删除保护。
4. **P4 私聊控制**：目录查询、create Task/takeover/exit/status-change 的 preview + CAS precondition + one-time confirm；验证旧卡、跨会话、非 Owner、状态变化和重复 callback 全部拒绝。
5. **P5 source event/outbox**：事务性 source event、subscription、过滤/coalesce、严格顺序、lease、retry/dead-letter/replay；验证 Web+飞书同一对话、断点续传和全局/takeover 去重。
6. **P6 飞书插件与 onboarding**：官方 Go SDK `RegisterApp` device flow、最小 Addons、Secret IPC、扫码 Owner 原子绑定、手工兼容分支、Channel/WebSocket 收件与归一化、带确定性 UUID 的底层 IM 出站、卡片 render 和配置验活；单测使用 fake registration/channel server，不依赖真实凭据。
7. **P7 群聊与 Knowledge ACL**：三分支群聊行为、Handoff 状态机、Owner 收件箱、渠道待办、稳定 ID grants、按需正文读取与人工晋升；补齐越权和隐私测试。
8. **P8 完整验收**：使用本 Task 选定的 AHA2 Windows 本地构建技能，执行 Web build/tests、Go tests/vet、Windows amd64 build、插件缺失/禁用/崩溃矩阵和真实飞书人工 smoke。未经 Owner 另行要求，不部署、不提交 Git、不打 tag、不发布。

必须具备的测试包括：SQLite partial unique/FK/migration 重放；Secret 不进 DB/API/log/Prompt；Owner 唯一与非 Owner 私聊拒绝；群会话不跨群共享；PendingAction 一次性与 stale precondition；inbound 同 ID 同 digest 幂等、同 ID 异 digest 告警；outbox 顺序/租约/重试/重放；事件白名单；Knowledge ID ACL；系统宿主可见但受保护且不进入 Profile Sync；remote/read-only Task 不可接管；插件缺失核心仍可启动和使用全部原功能。

## 13. Owner 已确认决策

以下三项已于 2026-09-09 锁定：

1. 采用飞书官方 SDK `registration.RegisterApp` 的 Device Flow 作为默认绑定：一个二维码创建/配置应用并把扫码者绑定为唯一 Owner；仅在平台明确不支持时展示手工 App ID/Secret 兼容分支。
2. AHA2 当前没有通用 Memo/Todo；一期 `accepted_todo` 由 ChannelHandoff 收件箱承载，不在本期引入新的全局待办领域。
3. v1 插件采用“每实例一个进程 + 插件只出站连接核心 + durable command/outbox”，暂不开放第三方插件监听端口或 Go 动态 plugin。

实现已按以上决策完成；后续若调整边界，应先修订本设计和协议测试再修改实现。

## 14. 官方参考

- 飞书官方 Go SDK `v3.9.10` 一键注册说明：https://github.com/larksuite/oapi-sdk-go/blob/v3.9.10/README.zh.md#%E4%B8%80%E9%94%AE%E5%88%9B%E5%BB%BA%E5%BA%94%E7%94%A8
- 飞书官方 Go SDK 注册结果与参数定义：https://github.com/larksuite/oapi-sdk-go/blob/v3.9.10/scene/registration/types.go
- 飞书官方 Go SDK Channel 模块：https://github.com/larksuite/oapi-sdk-go/blob/v3.9.10/doc/channel.zh.md
- 飞书官方 Go SDK create/reply message UUID 字段：https://github.com/larksuite/oapi-sdk-go/blob/v3.9.10/service/im/v1/model.go
- 飞书开放平台开发者后台：https://open.feishu.cn/app
- 飞书官方 2026 年“创建飞书智能体应用”说明：https://www.feishu.cn/content/article/7651905073454304222
- 飞书事件处理与长连接：https://open.feishu.cn/document/server-docs/event-subscription-guide/overview
- 飞书 API 权限申请与批量导入：https://open.feishu.cn/document/server-docs/application-scope/introduction
