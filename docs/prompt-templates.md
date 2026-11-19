# Prompt Templates

AHA2 的自然语言规则保存在 `internal/prompt/templates/`，通过 Go embed 随单文件程序发布。SQLite 的 `prompt_template_overrides` 只保存用户修改后的覆盖内容。

所有面向 Agent 的内置静态文案都来自 `internal/prompt/templates/*.md`，Go 代码只负责选择模板、
填充运行时数据和物化上下文，不再保存协议、Recovery、Prompt 段落或 `agent-api.md` 正文常量。

模板按用途分层：

- `core`、`role`、`identity`、`channel`、`policy`：Agent 行为与渠道边界。
- `protocol`：Knowledge、Agent API 和附件投递协议。
- `section`：Task、Available Context、Current Inbox 和 Compact Handoff 段落。
- `context`：`task.md`、`recent-context.md`、Current Inbox 内联 Recovery、Turn diagnostics、Hardware 和附件索引。
- `resource`：只读的 `agent-api.md` 参考文件。

Web 的“提示词”一级模块只提供：

- 查看全部当前模板
- 编辑可修改模板
- 恢复内置模板
- 显示模板层级、来源和版本

升级时，内容仍与 v0.5.19 或 v0.5.20 内置模板完全一致的历史 override 会按旧内置副本处理，
自动使用新的内置模板；真正修改过的自定义 override 继续保留。

Knowledge Protocol 与 Agent Control API Protocol 可以由 Owner 调整。附件投递协议和
`agent-api.md` 参考文件保持只读，防止模板覆盖破坏跨 Task 附件隔离或暴露不存在的 API 能力。
保存模板时会立即执行一次空数据渲染，未知运行时字段不会进入 override。

Knowledge Protocol 只保留索引读取、stale 边界、Turn closeout 和 proposal 验证规则；
Agent Control API Protocol 只保留 capability、Main/Sub 权限、进度和最终输出边界。完整端点
说明留在按需读取的 `agent-api.md`，附件上传与回执规则拆为独立必需协议，避免重复注入。

管理 API：

```text
GET  /api/v1/prompts/templates
PUT  /api/v1/prompts/templates/{id}
POST /api/v1/prompts/templates/{id}/reset
```

## 阶段 3 模板收敛（3a/3b 只读层）

阶段 3 把模板组织为两个顶层入口：`core`（安全、身份、角色、渠道、Agent API 与附件边界）和
`task-context`（协作策略、知识协议、Task/Workspace、Inbox 与生成文件）。3a 建立只读的注册
与预览层，3b 把它接到只读 HTTP/Web 预览，不切换渲染、不改写数据库：

- 旧模板 ID 全部保留，并作为别名解析到 `入口#锚点`（例如 `role.main` -> `core#role-main`）。
  旧 ID 的版本在映射表中冻结；内置模板升版时必须有意识地更新该冻结值。
- `context.task`、`context.recent-context`、`context.recovery-handoff`、`context.turn-diagnostics`、
  `context.hardware`、`context.mail`、`context.attachments-index` 是代码生成的动态片段：可预览，
  在新的入口模型中不可单独编辑。`protocol.attachment-delivery` 与三个 `resource.*` 保持只读。
- 迁移预览只读取现有 `prompt_template_overrides`：按锚点合并可迁移的 override，列出动态/只读
  锚点冲突、未知模板的孤儿 override，以及旧路径已忽略的过期内置覆盖；不写库、不静默覆盖。
- 有效提示词在本阶段保持不变：生产仍走旧 `Build` 路径；入口模型渲染器只用于逐字等价基线。

3b 新增的读取端点：

```text
GET /api/v1/prompts/entries
GET /api/v1/prompts/entries/{entry}/anchors/{anchor}
GET /api/v1/prompts/migration-preview
```

入口目录固定为 `core` 与 `task-context`，锚点携带冻结版本、必需/受保护/可编辑和运行时生成标记。
锚点预览返回源模板与示例渲染；未知或空白入口/锚点返回 4xx，不回退到默认模板。这些路径以及
迁移预览不接受任何写方法（显式 405）。Web 的“提示词”页把入口预览、迁移预览和旧模板管理分成
三个视图：入口视图只读，动态片段标注“运行时生成/不可单独编辑”，迁移面板只展示可迁移、冲突、
孤儿和已忽略四组，不提供保存或一键迁移。旧 ID 列表与 `PUT /api/v1/prompts/templates/{id}`、
`POST /api/v1/prompts/templates/{id}/reset` 的业务语义至少保留一个版本。

Main/Sub、Auto/Single 等模板的运行时选择由 Prompt Engine 按当前 Agent 和 Task 状态固定完成，不作为用户可配置的路由系统。

## Available context

每个 Turn 开始前，AHA2 会从数据库读取当前 Task 和 Agent 的持久化数据，生成 Agent 私有上下文，并引用同一 Task 的不可变共享快照：

```text
<task-workdir>/.aha2-context/<task-id>/<agent-id>/       # Agent 私有（只读）
<task-workdir>/.aha2-context/<task-id>/shared-<sha256>/ # Task 共享（只读）
<task-workdir>/.aha2-context/<task-id>/scratch/<agent-id>/ # Agent 可写 scratch
```

`scratch/<agent-id>/` 是 Agent 唯一可写的上下文目录，以 0700 创建，路径同时通过
`AHA2_SCRATCH_DIR` 环境变量提供，并写在 Prompt 的 Task 与工作区段落中。测试程序、
临时日志、临时截图和一次性脚本只能写在这里；不要写入 workspace 根、用户目录、
AHA 数据目录或 `attachments/`。scratch 不是交付位置，正式附件走附件协议。
Task 完成或删除时清理该 Task 的 scratch；失败的 Task 保留到完成、取消或删除；
git worktree Task 由 worktree finalize 一并删除。cleanup 只接受
`<task-workdir>/.aha2-context/<task-id>/scratch` 这一精确路径，并校验解析后的真实路径，
不会跟随符号链接删除 workdir 之外的数据。

Agent 私有内容包括：

- `task.md`：完整 Task、Project 和 Workspace 信息，由 `context.task` 模板生成。
- `recent-context.md`：仅在没有可复用 Backend Session、发生 Session 重置/压缩，或上一 Turn 异常时生成；最多包含最近 6 组已完成的 Owner 用户消息与 Main 最终回复，不含 Agent Update、工具、错误、耗时或 Sub Agent 结果。
- Recovery handoff：由可编辑的 `context.recovery-handoff` 模板生成，仅在同一 Round、同一输入消息的上一 Turn 被中断时，内联到 `Current Inbox Batch` 开头。内容包含上一 Turn、中断原因、最近持久化进度和最新工具生命周期，不再物化为 `recovery-handoff.md` 或出现在 `Available context`。
- `diagnostics/turns.md`：仅在 failed/interrupted/blocked、停滞或重试时生成的 Turn 诊断记录。
- `hardware.md`：Task 硬件组、Serial/Network 连接事实和权限；不包含密码。
- `attachments/`：当前 Agent 可见消息引用的附件。

共享快照包括：

- `knowledge/global/index.md`、`knowledge/project/index.md`、`knowledge/project/navigation/index.md`：按 scope 和用途拆分的 Knowledge 入口。
- `knowledge/pending-updates/index.md`：仅在存在待修订 stale/wrong 文档时生成。
- `knowledge/**/<id>.md`：由 index 相对链接按需读取的 Knowledge 正文、revision 和 content hash。
- `skills/<slug>/`：Task 显式选择的 Skill 包及 `skill-manifest.json` 版本元数据。
- `agent-api.md`：当前 Agent 可调用的控制面 API，不包含 capability 值。

Prompt 中的 `Available context` 只注入实际生成的入口文件路径、用途和字符数，不注入文件正文，也不再物化重复的 `manifest.json`。Agent 根据当前工作需要自行读取。Task Memory 继续保留在数据库、Web 工具和 Agent API 中，但不再进入默认 Prompt、`Available context` 或 Backend Session 压缩 handoff。

Agent 私有文件在每个 Turn 开始前重新生成并覆盖；共享内容按文件路径和正文计算哈希，
同一内容只物化一次，Knowledge revision、Skill version 或 Agent API 地址变化时生成新快照。
两者除 `scratch/<agent-id>/` 外都是只读快照而非回写入口。Agent 通过 Knowledge/Skill API
提交带 revision/version 校验的修改。Main 和每个 Sub Agent 的私有目录仍相互隔离，不会读取
其他 Agent 的 Conversation、Turn 诊断或附件。Git Workspace 会把 `.aha2-context/` 写入仓库
本地 `info/exclude`，scratch 同样不会进入提交。
