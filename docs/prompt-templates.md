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
- Turn Checkpoint Protocol

Web 的“提示词”一级模块只提供：

- 查看全部当前模板
- 编辑可修改模板
- 恢复内置模板
- 显示模板层级、来源和版本

Checkpoint Protocol 与解析代码绑定，只读展示，不能通过 Web 修改。

管理 API：

```text
GET  /api/v1/prompts/templates
PUT  /api/v1/prompts/templates/{id}
POST /api/v1/prompts/templates/{id}/reset
```

Main/Sub、Auto/Single 等模板的运行时选择由 Prompt Engine 按当前 Agent 和 Task 状态固定完成，不作为用户可配置的路由系统。

## Available context

每个 Turn 开始前，AHA2 会从数据库读取当前 Task 和 Agent 的持久化数据，生成一组只读上下文文件：

```text
<task-workdir>/.aha2-context/<task-id>/<agent-id>/
```

当前包括：

- `manifest.json`：资源索引，只保存路径、用途和字符数。
- `task.md`：完整 Task、Project 和 Workspace 信息。
- `task-memory.md`：完整 Task Memory。
- `conversation.md`：当前 Agent 最近的 Conversation。
- `turns.md`：当前 Agent 的 Turn 历史。
- `hardware.md`：Task 硬件组、Serial/Network 连接事实和权限；不包含密码。
- `knowledge-global.md`：已验证 Global Knowledge。
- `knowledge-project.md`：已验证 Project Knowledge。

Prompt 中的 `Available context` 只注入这些文件的路径、用途和字符数，不注入文件正文。Agent 根据当前工作需要自行读取。Task Memory 同样只通过 `task-memory.md` 提供，不再自动注入摘要。

上下文文件在每个 Turn 开始前重新生成并覆盖，因此反映当时最新的数据库状态。Main 和每个 Sub Agent 使用独立目录，不会读取其他 Agent 的 Conversation 或 Turn 历史。Git Workspace 会把 `.aha2-context/` 写入仓库本地 `info/exclude`，不会进入提交；普通文件夹 Workspace 直接保存该隐藏目录。
