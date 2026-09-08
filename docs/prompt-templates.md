# Prompt Templates

AHA2 的自然语言规则保存在 `internal/prompt/templates/`，通过 Go embed 随单文件程序发布。SQLite 的 `prompt_template_overrides` 只保存用户修改后的覆盖内容。

当前模板包括：

- AHA Core
- Main Agent
- Sub Agent
- Task Agent Identity
- AHA Web Channel
- Auto Collaboration
- Single Agent
- Agent Control API Protocol

Web 的“提示词”一级模块只提供：

- 查看全部当前模板
- 编辑可修改模板
- 恢复内置模板
- 显示模板层级、来源和版本

Agent Control API Protocol 是只读模板，要求结构化状态通过当前 Turn capability API
提交，最终回复只保留自然语言。

管理 API：

```text
GET  /api/v1/prompts/templates
PUT  /api/v1/prompts/templates/{id}
POST /api/v1/prompts/templates/{id}/reset
```

Main/Sub、Auto/Single 等模板的运行时选择由 Prompt Engine 按当前 Agent 和 Task 状态固定完成，不作为用户可配置的路由系统。

## Available context

每个 Turn 开始前，AHA2 会从数据库读取当前 Task 和 Agent 的持久化数据，生成 Agent 私有上下文，并引用同一 Task 的不可变共享快照：

```text
<task-workdir>/.aha2-context/<task-id>/<agent-id>/       # Agent 私有
<task-workdir>/.aha2-context/<task-id>/shared-<sha256>/ # Task 共享
```

Agent 私有内容包括：

- `task.md`：完整 Task、Project 和 Workspace 信息。
- `task-memory.md`：当前有效的完整 Task Memory，是跨 Turn 恢复的权威工作状态。
- `recent-context.md`：仅在没有可复用 Backend Session、发生 Session 重置/压缩，或上一 Turn 异常时生成；只包含最近语义消息，不含工具流水。
- `diagnostics/turns.md`：仅在 failed/interrupted/blocked、停滞或重试时生成的 Turn 诊断记录。
- `hardware.md`：Task 硬件组、Serial/Network 连接事实和权限；不包含密码。
- `attachments/`：当前 Agent 可见消息引用的附件。

共享快照包括：

- `knowledge/global/index.md`、`knowledge/project/index.md`、`knowledge/project/navigation/index.md`：按 scope 和用途拆分的 Knowledge 入口。
- `knowledge/pending-updates/index.md`：仅在存在待修订 stale/wrong 文档时生成。
- `knowledge/**/<id>.md`：由 index 相对链接按需读取的 Knowledge 正文、revision 和 content hash。
- `skills/<slug>/`：Task 显式选择的 Skill 包及 `skill-manifest.json` 版本元数据。
- `agent-api.md`：当前 Agent 可调用的控制面 API，不包含 capability 值。

Prompt 中的 `Available context` 只注入实际生成的入口文件路径、用途和字符数，不注入文件正文，也不再物化重复的 `manifest.json`。Agent 根据当前工作需要自行读取。Task Memory 同样只通过 `task-memory.md` 提供，不再自动注入摘要。

Agent 私有文件在每个 Turn 开始前重新生成并覆盖；共享内容按文件路径和正文计算哈希，
同一内容只物化一次，Knowledge revision、Skill version 或 Agent API 地址变化时生成新快照。
两者都是只读快照而非回写入口。Agent 通过 Knowledge/Skill API 提交带 revision/version
校验的修改。Main 和每个 Sub Agent 的私有目录仍相互隔离，不会读取其他 Agent 的
Conversation、Turn 诊断或附件。Git Workspace 会把 `.aha2-context/` 写入仓库本地
`info/exclude`，不会进入提交。
