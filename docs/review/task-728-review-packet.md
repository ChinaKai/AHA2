# task-728 review packet：宠物模型设置统一（聊天 + 自主行为一个模型）

- 后续修订：本包是 r1（源码 `b285d54`）；评审意见的修复见 `task-728-review-packet-r2.md`（源码 `2a8f1e5`）。

- 基线：main `12b9be1`
- 实现提交：`b285d54`（分支 `aha/task_07933ff5b7797feffa4c`，worktree `.aha2-worktrees/task_07933ff5b7797feffa4c`）
- 材料：本文件 + 2 个 diff 分卷；没有 push、没有合并、没有部署、没有改生产数据
- 完整 diff：`git diff 12b9be1 b285d54`，93,256 字节，sha256 `d2dafec72f9301b034e3192ca2878ef0afdf7d109af6162f37dc94aacf87cdc1`
- 分卷（各自 ≤65536 字节，内容为对应文件的原样 `git diff`，按完整 diff 的路径顺序排列）：
  - `task-728-review-packet-diff-1-go.md`：Go 源码与测试，63,698 字节，sha256 `3176997e149ef5d7f68a4a96f3b48bab6cebb29fbb430d96860a0a61484451f9`（整卷 64,358 字节）
  - `task-728-review-packet-diff-2-web.md`：Web 源码与测试，29,558 字节，sha256 `4ce62fffbc5a2ab772fd00eaf8dbe52cec21246895710bf8f1290f20dac3d355`（整卷 29,895 字节）
- 还原：两个分卷按顺序取出 `~~~~diff` 与 `~~~~` 之间的内容拼接，与 `git diff 12b9be1 b285d54` 逐字节相同（已用 `cmp` 核对）。也可用 `git show <材料提交>:docs/review/task-728-review-packet-diff-N-*.md`。

## 做了什么

### 1. 服务端单一模型事实
- `owner_assistants.pet_brain_model_id`（Go `BrainModelID`）是唯一的模型事实；`owner_assistants.model_id`（Go `ModelID`）降为派生镜像，保存时两者写同一个目录模型 ID，读取时先归一化。
- 新增 `Store.UpdateOwnerAssistantModel`，一条 SQL 同时写两列，避免读到中间态。
- 新增 `MigrateOwnerAssistantUnifiedModel`，在 `cmd/aha/main.go` 启动时执行一次；`OwnerAssistant`、`AskOwnerAssistant`、`UpdateOwnerAssistant` 也会读时归一化，因此老库不重启也不会出现两个模型。
- 归一化优先级：`pet_brain_model_id` 非空时以它为准；为空才回落到旧的 `model_id`。两者都为空且目录里有可聊天的 Provider 模型时，按首次初始化选第一个——这是初始化，不是失败后的静默回退。
- 删除了旧的两处自动换模型：`OwnerAssistant` 里“当前 model_id 不在目录就换成 models[0]”和 `assistantRuntime` 里“找不到就换成第一个 Provider 模型”。现在模型删除/账号不可用会明确报错。

### 2. 保存入口只有一个
- `PUT /api/v1/pet/settings` 的 `pet_model_source` / `pet_model_id` / `pet_wire_model` / `pet_codex_account_id` 是唯一写入口。
- `chat_model_id` 仅在 payload 完全没有宠物选择时作为旧客户端兼容映射，把它当作统一 Provider 模型保存；与宠物选择同时出现时忽略，宠物选择优先。
- `brain_model_id` 旧字段仍然兼容，解析后同样写入统一字段。
- `PUT /api/v1/assistant` 不再允许单独改聊天模型：`model_id` 与当前统一模型不同就返回 400「聊天模型跟随宠物模型…」。名称和形象修改不受影响。

### 3. Web 设置页
- 「助手聊天模型」从 `<select>` 改为只读 `<input readonly>`；面板头部的模型快捷下拉 `#assistant-model` 删除。
- 只读框内容来自服务端新增的 `pet_model_label`，格式按来源生成：`Provider 模型 · <Provider> / <模型>`、`Codex 官方账号 · <账号> / <模型>`、`Claude native · <模型>`。
- 只读框和来源选择器共用同一组 `pet_model_*` 字段渲染；保存后重新渲染，两处指向同一个模型。
- 新增 `pet_model_available` / `pet_model_unavailable_reason`，不可用时只读框标红并把原因放进 title。
- 帮助文案改为「聊天和自主行为使用同一个模型；下面的模型来源 / 模型账号是唯一修改入口。不可用来源不会回退。」
- 保存请求不再携带 `chat_model_id`。

### 4. 聊天链路支持非 Provider 来源
- `assistantRuntime` 按统一模型行的 `source` 分流：
  - Provider：沿用原来的 `gateway.ChatWithTools` HTTP 直连（同一个目录模型、WireModel、Provider 凭据）。
  - `official` / `claude_native`：调用 `Service.askAssistantBackend`，通过注入的 `Executor` 走与宠物 Task 相同的 Claude/Codex backend，模型行、账号、Env Group、`CODEX_HOME` profile 都取自同一套解析结果。
- 非 Provider 聊天的环境构建与 Turn 对齐：Env Group 变量与 secret、Codex 官方 profile、以及和宠物 Task 快照一致的代理开关；没有 MCP、没有 Agent API。
- CLI 聊天用 `filesystem=read-only`、`approval=never`；提示词里明确「本轮是 Web 助手聊天，不要巡检、不要调用工具」，并把宠物动作的 `<tool_call>…</tool_call_end>` 文本协议附在末尾，沿用现有的 `parseTextToolCalls` 动作处理。
- 新增 `assistantBackendTimeout`（3 分钟），避免一次聊天占用完整 Turn 预算。

### 5. 自主行为
- 保存后仍调用 `syncPetTaskRuntime(assistant.BrainModelID)`，下一 Turn 使用新模型；不可用来源仍是报错，没有新增回退。

## 实际改动文件与字段

| 文件 | 改动 |
| --- | --- |
| `internal/store/assistant.go` | 新增 `UpdateOwnerAssistantModel`（同时写 `model_id` 与 `pet_brain_model_id`） |
| `internal/app/assistant.go` | 统一模型归一化/读回/标签/可用性；`assistantRuntime` 按来源分流；新增 CLI 聊天、提示词、环境；`UpdateOwnerAssistant` 拒绝独立改聊天模型；删除自动回退 |
| `internal/app/pet.go` | `PetSettingsInput.ChatModelID` 仅作兼容；保存只写统一模型；忽略冲突的 `chat_model_id` |
| `internal/app/pet_task.go` | 抽出 `ensurePetWorkspace`；新增 `MigrateOwnerAssistantUnifiedModel` |
| `internal/httpapi/assistant_handlers.go` / `pet_handlers.go` | 响应增加 `pet_account_name`、`pet_model_label`、`pet_model_available`、`pet_model_unavailable_reason` |
| `cmd/aha/main.go` | 启动时执行统一模型迁移 |
| `web/src/assistant.ts` | 只读聊天框、移除面板模型下拉、保存去掉 `chat_model_id`、读回渲染 |
| `web/src/api.ts` / `types.ts` | `chat_model_id` 改为可选；补新增响应字段 |
| `web/src/i18n.ts` | 新只读标签与帮助文案的中英文 |
| `web/src/styles.css` | 只读框与不可用态样式 |
| 测试 | 见下 |

数据库字段：没有新增列或改表。`owner_assistants.model_id` 保留但只作派生/兼容读取；`owner_assistants.pet_brain_model_id` 是权威字段。API 请求兼容 `chat_model_id`、`brain_model_id`；响应新增四个只读字段。

## 迁移 / 映射策略

1. 启动迁移：`pet_brain_model_id` 非空 → 以它为准，把 `model_id` 覆盖成同值；为空 → 用旧的 `model_id` 填充两者。
2. 读时归一化兜底：页面读取、聊天、设置保存前都会执行同一函数，老库/老进程写出的不一致行在第一次读取时被纠正并持久化。
3. 旧 API：`chat_model_id` 只在没有 source-first 宠物选择时映射为统一 Provider 模型；`brain_model_id` 解析后写统一字段。二者都不再产生第二个聊天模型。
4. 旧前端：`PUT /api/v1/assistant` 的 `model_id` 只有在与统一模型相同时才是 no-op，否则 400。

## 验证（实跑）

Go 全包测试按项目知识里的做法编译后用 `TMPDIR=/tmp/...` 运行（避免 XFS 上的 SQLite 临时库拖慢）：

| 命令 | 退出码 | 结果 |
| --- | --- | --- |
| `go build ./...` | 0 | 全仓编译通过 |
| `go vet ./internal/app ./internal/httpapi ./internal/store ./cmd/aha` | 0 | 无告警 |
| `go test -c` → `env -u GOTMPDIR TMPDIR=/tmp/task-728-app app.test -test.count=1 -test.timeout=25m` | 0 | 0 个 FAIL |
| `env -u GOTMPDIR TMPDIR=/tmp/task-728-http httpapi.test …` | 0 | 0 个 FAIL |
| `env -u GOTMPDIR TMPDIR=/tmp/task-728-store store.test …` | 0 | 0 个 FAIL |
| `npm test`（web） | 0 | 620/620 |
| `node web/tests/pet_model_readonly.browser.mjs` | 0 | 真实 Chromium：只读框、保存 payload、保存后两处一致 |

关键新增测试：

- `TestOwnerAssistantNormalizesLegacyDivergentModels`、`TestOwnerAssistantNormalizesLegacyChatOnlyRow`：旧行归一化并持久化。
- `TestUpdateOwnerAssistantRejectsIndependentChatModel`：旧接口不能单独改聊天模型，名称/形象仍可改。
- `TestPetSettingsLegacyChatModelMapsToUnifiedModel`、`TestPetSettingsPetSelectionWinsOverChatModelID`：旧字段映射与冲突优先级。
- `TestAssistantChatUsesUnifiedClaudeNativeModel`：CLI 聊天请求的 ModelID/Backend/Source/账号与统一模型一致，且提示词带用户消息与动作协议。
- `TestUnifiedModelSurvivesRestart`：同一数据库换一个 Service 后，读回、宠物 Task runtime 快照、聊天请求三者仍是同一个模型。
- `TestAssistantChatDeletedUnifiedModelDoesNotFallBack`：模型删除后聊天报错、不写消息、读取也不换模型。
- `TestOwnerAssistantModelLabelIncludesSourceAndAccount`：只读框标签包含来源、账号、模型。
- `TestOwnerPetSettingsAPIUnifiesChatAndPetModel`（httpapi）：同时提交冲突的 `chat_model_id` 时，响应与刷新读取仍是一个模型；旧 assistant 接口返回 400。
- `web/tests/assistant.test.mjs`：构建产物里没有 `#assistant-model`，聊天框是 readonly，保存不出现 `chat_model_id`。
- `web/tests/pet_model_readonly.browser.mjs`：浏览器层面切换来源→保存→只读框与选择器一致。

## 未验证的缺口 / 已知限制

- 没有可用真人账号，**没有对真实 Claude Code 登录或 Codex 官方账号发起过 CLI 聊天**。Claude native / Codex 官方聊天路径由单测验证「请求形状、模型、账号、环境与宠物 Task 一致」，真实鉴权与网络行为需要 owner 在装有账号的机器上实跑。
- 浏览器验证用的是 Playwright + 固定 API 夹具，不是跑真实 Go 服务；真实服务端的保存/刷新链路由 `TestOwnerPetSettingsAPIUnifiesChatAndPetModel` 与 `TestUnifiedModelSurvivesRestart` 覆盖。
- Provider 模型聊天仍走 gateway HTTP，宠物 Task 走 CLI backend；两者使用同一个目录模型行、WireModel 与凭据，但传输实现不同。Env Group 若被手工改成别的 `OPENAI_MODEL`，不在本次统一范围内。
- Claude 后端的 `read-only` 当前映射为 `--permission-mode bypassPermissions`（`internal/backend/claude.go` 既有行为），CLI 聊天因此仍可能执行命令；本次没有改后端权限映射。
- 没有与 task-722 合并或做冲突演练：本分支改动的 `internal/app/assistant.go`、`internal/app/pet.go`、`internal/app/pet_task.go`、Web 宠物设置文件都不在 `aha/task_e3d0848747ddc2018605` 的改动文件列表里（该分支是 pet MCP 覆盖与文档）。如果之后合并出现冲突，按约束用普通 merge 保留现场，不强推。
