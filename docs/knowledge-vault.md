# 文件知识库

文件知识库（knowledge vault）把知识以 Markdown 文件保存为正本，SQLite 只保存可重建的索引。Web 知识页只读；编辑在 VSCode 或 Obsidian 中完成，然后重建索引。

## 启用与关闭

在「高级设置 → 其他设置 → 文件知识库」中切换：

- 开关运行态生效，不需要重启 AHA2。
- 首次切换后，选择写入 `knowledge_vault_settings` 表；此后以数据库设置为准，`AHA2_KNOWLEDGE_VAULT_ENABLED` / `--knowledge-vault-enabled` 只作为从未在 Web 切换过时的启动默认值。
- 关闭时 7 个 `/api/v1/knowledge-vault/*` 数据接口返回 404，旧知识页与旧 API 不受影响。

vault 和索引路径仍由 `AHA2_KNOWLEDGE_VAULT_PATH`、`AHA2_KNOWLEDGE_VAULT_INDEX`（或对应 CLI 参数）决定；默认位于 `<data-dir>/knowledge-vault` 与 `<data-dir>/knowledge-vault-index.db`。

## 目录与知识边界

- `global/index.md`：全局知识入口；`global/general/` 放跨项目通用知识，`global/agent-lessons/` 放可复用的 Agent 经验。
- `projects/<slug>/index.md`：项目知识入口；主题组是子目录，根下叶子文档放 `projects/<slug>/inbox/`。
- `archive/<slug>/`：旧库归档，单独标记；`orphans/`：无法判断归属的隔离区。二者不参与正常读取。
- `global/skills/<slug>/`、`projects/<slug>/skills/<slug>/`：技能包。
- `.trash/`：删除暂存，不作为有效知识。

依赖具体仓库、分支或部署环境的内容只写项目库；可跨项目复用的方法、决策和参考资料才写 `global/`。项目文档不得跨项目引用。

规范 `type` 只有六种：`navigation`、`decision`、`practice`、`diagnostic`、`reference`、`architecture`。写入前先搜索标题、slug、标签和正文，优先更新已有文档；根下没有主题组的叶子文档先放 `inbox/`。过时、错误或对当前任务有帮助时分别提交 `stale`、`wrong`、`helped` 反馈，不要直接把状态改回 `verified`。

## 使用指南

`global/general/knowledge-base-guide.md` 是完整的知识库使用指南，也是唯一的规范入口。初始化 vault 时如果文件不存在会自动创建，已有文件不会被覆盖。

- `GET /api/v1/agent/knowledge/guide` 直接读取该文件，返回 `path`、`title`、完整 `content` 和精简 `prompt`；不依赖索引是否已重建。
- Task Prompt 只注入文件内 `<!-- aha-vault:prompt:start -->` 到 `<!-- aha-vault:prompt:end -->` 之间的精简区块，避免把完整指南塞进每轮上下文。
- 标记缺失或文件缺失时使用内置精简版并继续运行；指南不会作为普通知识条目重复注入 Agent 知识列表。

## 重建索引的自动维护

每次 `rebuild-index` 会同时更新可重建的索引与 vault 元数据，不修改知识正文：

- 自动生成 `.aha-vault.json` 的 `projects`、`project_names`、`skills`、`archives`（及归档名称）。项目 slug 与项目 ID 的绑定来自 `projects/<slug>/index.md` 的 frontmatter `project_id`；缺失时标记为 unbound 并给出 warning，不会猜测归属。
- manifest 使用稳定排序和同目录临时文件加改名原子写入；内容未变化时不写，重复执行幂等，也不会删除任何知识或技能文件。
- 对已存在的 `index.md`，只重写 `<!-- aha-vault:index:start -->` 到 `<!-- aha-vault:index:end -->` 之间的自动区块：先列直接子目录的 index 标题与摘要，再列直接子文档。标记外的手写导语和 frontmatter 原样保留，`updated` 不改。
- 不自动创建 vault 根、`projects/`、`archive/`、`inbox/` 的 index；只维护用户已经创建并带有标记的 index。缺少标记只给出 warning，不改写文件；内容没有变化时不落盘，写入失败会保留 warning，可再次重建重试。

## Agent 读取 canary

Agent 读取和技能加载切换按项目灰度。vault 总开关仍然默认关闭（legacy），本功能完成不会自动开启：

- `AHA2_KNOWLEDGE_VAULT_READ_PROJECTS` 是逗号分隔的项目 ID allowlist。未设置时默认为宠物项目 `project_pet` 与 AHA2 项目 `project_6ff866f348d8a892d5e8`；显式设为空表示不切换任何项目。
- 只有同时满足“vault 开关开启、项目在 allowlist、索引就绪、manifest 已绑定该项目”时，Turn 知识块和 Agent 知识列表才读取 vault；其他项目继续使用旧知识表和旧技能目录。
- 技能优先从 `global/skills/<slug>/` 或 `projects/<slug>/skills/<slug>/` 加载；vault 中找不到对应包时回退到旧的 `<data-dir>/knowledge/skills` 并记录日志，旧目录不会被删除。
- 回退是即时的：从 allowlist 移除项目或显式设为空，该项目下一 Turn 就回到旧 DB；关闭 vault 总开关则所有项目回退。canary 只影响读取，不写 vault、不删除旧数据。

## 示例知识库（体验数据，可删除）

新 vault 可以在面板里点「初始化示例知识库」创建，也可以运行：

```sh
aha2 knowledge-vault init --samples --commit=false
```

示例是**体验数据**，用于快速看到新知识页的效果；它们不会导入真实旧知识，也不会修改旧知识库。固定生成 4 个文件：

- `global/general/示例/欢迎使用.md`
- `global/general/示例/编辑与同步.md`
- `global/general/示例/写作约定.md`
- `global/agent-lessons/示例-Agent经验.md`

示例只会写进尚未初始化（没有 `.aha-vault.json`）的 vault；已有 vault 调用 `--samples` 或初始化接口会跳过或返回 409，不会覆盖任何文件。验证满意后可以直接删除 `global/general/示例/` 和 `global/agent-lessons/示例-Agent经验.md`，再点「重建索引」。

## 编辑与重建

当前没有文件监听。用 VSCode 或 Obsidian 打开 vault 目录，保存后到「高级设置 → 其他设置 → 文件知识库」点「重建索引」，或在服务器上运行：

```sh
aha2 knowledge-vault rebuild-index
aha2 knowledge-vault search <关键词>
```

索引只服务于新知识页；旧知识表和旧知识页保持原样。正式迁移旧知识、删除旧知识库不在本功能范围内。