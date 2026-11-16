# 文件知识库

文件知识库（knowledge vault）把知识以 Markdown 文件保存为正本，SQLite 只保存可重建的索引。Web 知识页只读；编辑在 VSCode 或 Obsidian 中完成，索引由写路径、文件变更监听和启动对账自动维护。

## 启用与关闭

「高级设置 → 其他设置 → 文件知识库」只有一个 owner 可见的总开关：

- 开：Web 知识页、Agent 知识读写和技能加载全部使用 vault。
- 关：以上全部立即回到旧知识库，无需重启 AHA2；旧知识表和旧技能目录始终保留，不因切换删除。

启动默认由后端判断，不要求 owner 先做选择：

- 全新安装（没有旧知识或旧技能）：隐藏开关，只显示只读状态；默认启用文件知识库。
- 旧版本升级（检测到旧知识表或旧技能包，且迁移/导入尚未验证）：显示「旧知识库回退」状态和迁移入口；迁移或导入校验完成前不能启用 vault。
- 已启用且在用的升级（`configured=1`、`enabled=1`，且 vault 索引已有条目）：视为已经完成启用，就绪度只报告 `ready`/`degraded`，不显示迁移入口；旧知识库仅作只读回滚保留，不会被迁移覆盖。
- owner 一旦在 Web 切换过，选择写入 `knowledge_vault_settings` 表并优先生效，之后以数据库设置为准。
- `AHA2_KNOWLEDGE_VAULT_ENABLED` / `--knowledge-vault-enabled` 仅作为支持人员的紧急启动覆盖，不在普通 UI 展示，也不会写回数据库。

关闭时 7 个 `/api/v1/knowledge-vault/*` 数据接口返回 404，旧知识页与旧 API 不受影响。vault 和索引路径仍由 `AHA2_KNOWLEDGE_VAULT_PATH`、`AHA2_KNOWLEDGE_VAULT_INDEX`（或对应 CLI 参数）决定；默认位于 `<data-dir>/knowledge-vault` 与 `<data-dir>/knowledge-vault-index.db`。

## 就绪度

后端在设置接口中计算并返回就绪状态，Web 只负责展示，不再自行拼规则：

| 状态 | 含义 | 能否启用 |
| --- | --- | --- |
| `ready` | vault、索引、项目绑定和迁移验证都正常 | 可以 |
| `degraded` | 可以启用，但存在未绑定项目或索引 warning；相关项目继续用旧知识库 | 可以，并显示警告 |
| `needs_migration` | 检测到旧知识/旧技能，迁移或导入尚未验证，且 vault 尚未启用或在用 | 不可以 |
| `needs_rebuild` | vault 未初始化、索引缺失/不可读，或知识文件比索引新 | 不可以 |
| `error` | vault 或索引目录不可写/不可读 | 不可以 |

检查项包括 vault 初始化、vault 可写、索引 schema、索引目录可写、索引新鲜度、条目数、manifest 项目绑定、旧知识迁移验证和索引 warning。`missing` 列出未通过的检查项。启用后若索引发生异常，Agent 立即回退旧知识库并返回 warning，Web 状态显示错误；关闭开关立即整体回退。

## 迁移与导入

两类数据入口都完整执行「备份/复制 → 重建索引 → 校验」，校验通过前不会启用 vault：

- **旧知识迁移**：面板「迁移旧知识库并校验」或 `aha2 knowledge-vault migrate-legacy`。先备份 SQLite（`VACUUM INTO`，失败时字节复制）和旧技能包到 `<data-dir>/backups/knowledge-migration-*`，再导出旧知识和技能，重建索引，并逐条校验 ID、标题、文件 sha256 与抽样正文。成功后记录 `source=legacy-migration` 和 `migration_verified_at`；旧数据不删除。
- 迁移前会检查目标 vault：只要已有索引条目或 Markdown 页面（README 除外），HTTP 与 CLI 都直接拒绝并返回明确错误，不覆盖任何已有文件，也不提供 `--force` 覆盖入口。需要迁移时请先备份并移走现有目录。
- **已有 vault 导入**：面板「导入已有知识库」或 `aha2 knowledge-vault import --kind local|git`。支持本地目录和 SSH Git 远端（仅 SSH 密钥，不接受密码或 HTTPS token）。复制/克隆后校验 manifest 与项目绑定，补齐缺失的 `index.md`，重建索引并校验。成功后记录 `source=existing-vault` 和 `import_verified_at`。
- 目标 vault 目录非空时导入直接拒绝，不会自动合并；需要先移走旧目录。
- Git pull/sync 成功后会自动重建索引并重新计算就绪度；只拉取成功不代表 ready，重建失败会以错误返回并保留 Git 结果。

Web 面板之外也可以直接用 CLI（迁移会写入 vault 并重建索引；`--commit=false` 仅跳过本地 Git commit）：

```sh
aha2 knowledge-vault migrate-legacy --database <data-dir>/aha2.db --backup-dir <data-dir>/backups --commit
aha2 knowledge-vault import --database <data-dir>/aha2.db --kind local --path /srv/existing-vault
aha2 knowledge-vault import --database <data-dir>/aha2.db --kind git --remote git@example.com:owner/vault.git --branch main
```

设置接口 `POST /api/v1/settings/knowledge-vault/migrate` 和 `/import` 返回更新后的 `settings` 视图以及 `migration`/`import` 报告；导入参数为 `{"kind":"local|git","path":"...","remote":"...","branch":"..."}`。Git 远端仅接受 SSH 地址，错误信息会脱敏，不会回显凭据。

## 目录与知识边界

- `global/index.md`：全局知识入口；`global/general/` 放跨项目通用知识，`global/agent-lessons/` 放可复用的 Agent 经验。
- `projects/<slug>/index.md`：项目知识入口；主题组是子目录，根下叶子文档放 `projects/<slug>/inbox/`。
- `archive/<slug>/`：旧库归档，单独标记；`orphans/`：无法判断归属的隔离区。二者不参与正常读取。
- `global/skills/<slug>/`、`projects/<slug>/skills/<slug>/`：技能包。
- `.trash/`：删除暂存，不作为有效知识。

依赖具体仓库、分支或部署环境的内容只写项目库；可跨项目复用的方法、决策和参考资料才写 `global/`。项目文档不得跨项目引用。

规范 `type` 只有六种：`navigation`、`decision`、`practice`、`diagnostic`、`reference`、`architecture`。写入前先搜索标题、slug、标签和正文，优先更新已有文档；根下没有主题组的叶子文档先放 `inbox/`。过时、错误或对当前任务有帮助时分别提交 `stale`、`wrong`、`helped` 反馈，不要直接把状态改回 `verified`。

## 使用指南

`knowledge-base-guide.md`（vault 根目录）是完整的知识库使用指南，也是唯一的规范入口。初始化 vault 时如果文件不存在会自动创建；旧版本位于 `global/general/` 的指南会在启动时一次性迁移到根目录，写入只写根路径。

- `GET /api/v1/agent/knowledge/guide` 直接读取该文件，返回 `path`、`title`、完整 `content` 和精简 `prompt`；不依赖索引是否已重建。
- Task Prompt 只注入文件内 `<!-- aha-vault:prompt:start -->` 到 `<!-- aha-vault:prompt:end -->` 之间的精简区块，避免把完整指南塞进每轮上下文。
- 标记缺失或文件缺失时使用内置精简版并继续运行；指南不会作为普通知识条目重复注入 Agent 知识列表。

## 索引自动维护

索引是可重建的派生缓存，不需要 owner 手动触发。写入路径同步更新，外部编辑由文件轮询增量发现，启动时对账并按需重建；同一时间只运行一个索引任务，重复重建幂等：

- 自动生成 `.aha-vault.json` 的 `projects`、`project_names`、`skills`、`archives`（及归档名称）。项目 slug 与项目 ID 的绑定来自 `projects/<slug>/index.md` 的 frontmatter `project_id`；缺失时标记为 unbound 并给出 warning，不会猜测归属。
- manifest 使用稳定排序和同目录临时文件加改名原子写入；内容未变化时不写，重复执行幂等，也不会删除任何知识或技能文件。
- 对已存在的 `index.md`，只重写 `<!-- aha-vault:index:start -->` 到 `<!-- aha-vault:index:end -->` 之间的自动区块：先列直接子目录的 index 标题与摘要，再列直接子文档。标记外的手写导语和 frontmatter 原样保留，`updated` 不改。
- 不自动创建 vault 根、`projects/`、`archive/`、`inbox/` 的 index；只维护用户已经创建并带有标记的 index。缺少标记只给出 warning，不改写文件；内容没有变化时不落盘，写入失败会保留 warning，可再次重建重试。

## Agent 读写

Agent 读取、技能加载和写入都跟随同一个 owner 总开关，没有额外的用户可见开关：

- 总开关开启且就绪：Turn 知识块、Agent 知识列表和技能优先使用 vault。每个项目还需索引 ready 且 manifest 已绑定；新建项目在同一流程初始化空间，未就绪的空间会明确报错并允许重试。旧表删除迁移（schema v143）执行后，任何入口都不再回退旧库。
- 旧表删除迁移执行前，总开关关闭、索引未 ready、项目未绑定或读取被回退名单排除仍可读取旧表作为迁移过渡；`AgentKnowledgeEntryResult`/`AgentKnowledgeFeedbackResult` 的 `warning` 字段说明回退原因。迁移后这些路径改为明确错误，不再静默返回旧知识。
- 项目已绑定且索引 ready 后，vault 读取失败必须 fail-closed：Turn 知识块留空、Agent 知识列表返回错误，并记录/返回 warning，**不回退旧库**。全局知识同理，且与项目绑定独立判断：全局读 vault 时即使项目仍未绑定，也只读 vault global。
- 渠道知识按每个来源项目各自当前生效的读取源解析；vault 项目的旧 grants 不再授权旧库内容，渠道可授权根来自当前读取源（vault 项目的根是 vault 索引 ID）。vault 生效时新建的渠道实例默认未配置知识访问范围：不会自动继承旧库，也不会自动授权 vault 内容；owner 在渠道设置中显式选择 vault 项目根后才读取，未配置时知识块为空且界面会明确提示。
- 技能优先从 `global/skills/<slug>/` 或 `projects/<slug>/skills/<slug>/` 加载；vault 中缺少对应包时回退到旧的 `<data-dir>/knowledge/skills`，旧目录不删除。
- 以下环境变量/CLI 参数只作为支持人员的紧急诊断手段，不在普通 UI 展示，也不要求配置：`AHA2_KNOWLEDGE_VAULT_RUNTIME_READ` / `--knowledge-vault-runtime-read`（关闭后所有项目强制读旧库）、`AHA2_KNOWLEDGE_VAULT_RUNTIME_WRITE` / `--knowledge-vault-runtime-write`（关闭后 vault 写入接口返回 409）、`AHA2_KNOWLEDGE_VAULT_READ_PROJECTS`（逗号分隔项目 allowlist，设为空字符串时所有项目回退旧库）。它们只收窄单个 manager 开关，不会打开总开关，也不会写回数据库。
- 回退：在 Web 关闭「文件知识库」即所有项目下一 Turn 回到旧知识库，无需重启；旧知识表与旧 skills 始终保留，不因切换删除。

## Agent 写入

- 写入来源跟随实际读取来源。项目正在读 vault 时，`POST /api/v1/agent/knowledge/entries` 与 `/feedback` 写 vault；读旧表的项目继续走旧知识 proposal/反馈流程，不会双写。
- 支持开关 `AHA2_KNOWLEDGE_VAULT_RUNTIME_WRITE=false`（或 `--knowledge-vault-runtime-write=false`）关闭写入时，正在读 vault 的项目返回 409 `knowledge_vault_write_disabled`，**不会**回退到旧表，避免同一 Turn 出现 vault 读、legacy 写的分裂；重新打开后立即恢复。此开关仅用于支持诊断，默认 true，不在 Web 展示。
- `POST /api/v1/agent/knowledge/entries` 支持 `mode=create|revise`。create 必须提供六种规范 `type` 之一、`title`、`summary`、`body`、以及 `search_query` 或 `duplicate_ack`；服务端按标题分配文件名，不接受客户端路径。与已有文档高度相似（Dice 相似度 ≥ 0.8）的 create 返回 409 `duplicate_knowledge` 与 `duplicate_candidates`，确认后可带 `duplicate_ack` 重试。
- revise 必须提供 `entry_id` 和 `base_revision`（可选 `base_hash`）；revision 或 hash 与磁盘不一致返回 409 `knowledge_revision_conflict`，不会覆盖更新版本。写入成功后 revision + 1 并同步重建索引。
- 路径策略：项目知识只能写入已存在的 topic group（含 `index.md` 的子目录）或 `projects/<slug>/inbox/`；全局知识只能进入 `global/inbox/`。Agent 不能创建项目根、topic group、`index.md`、`skills`、`archive`、`orphans`、`.trash`，路径穿越、绝对路径、反斜杠和 symlink 目标一律拒绝。
- `POST /api/v1/agent/knowledge/feedback` 支持 `helped|stale|wrong`。`stale`/`wrong` 只改 frontmatter `status` 并进入待修订队列，不改正文、不增加 revision；`helped` 只记录正向反馈。两种来源都不会因反馈改写正文。反馈按条目 ID 跟随实际读取源：global 条目不依赖项目绑定，全局知识读 vault 时就写 vault；不会因为项目未绑定而写旧表。
- 原子性：临时文件写入并 fsync 后 rename，随后重建索引；任一步失败会恢复旧文件并回滚索引重建，不留下半写文件。审计（`source_task`、`source_turn`、旧 revision、diff 摘要、writer）与幂等键、每 Task/Turn 默认 3 条写入预算保存在 vault 内 `.aha/agent-writes.json`。

## 删除旧知识（受控，默认 dry-run）

`delete-prep` 只生成备份、导出和删除计划，不会删除任何行。真正删除使用 `knowledge-vault delete`；默认 dry-run，只有 `--execute` 才写库：

```sh
# 只读预演：打印计划、策略 ID、skills 清单和阻断项，不写数据库、不写 delete-result.json
aha2 knowledge-vault delete --prep <data-dir>/backups/knowledge-delete-<timestamp>

# 执行：必须先停服务；confirm 必须是 <prep>/delete-plan.json 的 SHA256
aha2 knowledge-vault delete --prep <prep> --execute --confirm <sha256> \
  --delete-skills --delete-skill-files
```

主要参数：`-data-dir`、`-database`、`-prep`（必需）、`-execute`、`-confirm`、`-health-url`（默认 `http://127.0.0.1:8766/healthz`）、`-delete-skills`、`-delete-skill-files`（默认 false，必须同时给 `-delete-skills`）。

执行前逐项校验，任一不满足即拒绝，且没有绕过开关：

- `backup.json` 与 `delete-plan.json` 存在；两者 mode 都是 `backup-and-dry-run`、`delete_executed=false`、完整性为 `ok`，备份 SHA256/大小/路径一致，备份文件存在且大小匹配。
- 当前库与 plan 的 in-scope 表行数**动态**一致；`skills` 行数单独与 plan 的 skills 行一致。两个数字都从 plan 读取，不写死。
- `channel_knowledge_policies` 恰好 4 行、`channel_knowledge_grants` 总数为 0；遗留 `channel_knowledge_records` 必须为 0。
- vault 已启用且索引 ready（复用服务启动时的开关与 readiness 判断）。
- `--execute` 必须同时提供 `--confirm`，且等于 `delete-plan.json` 的 SHA256。
- 执行时若 `-health-url` 返回 HTTP 200，拒绝执行，必须先停服务。
- 行删除在单个 SQLite 事务内完成：策略 → proposals → entries → bindings → libraries → skills；任何错误整体回滚。
- `-delete-skills` 会扫描仍处于 draft/preparing/active/waiting_user/blocked/failed 的任务；只要 `skill_ids_json` 仍引用将被删除的旧 `skill_*` ID，就拒绝整个 execute 并列出任务，需要先迁移到 vault-skill key 或清空选择。
- `-delete-skill-files` 会在事务前逐个校验：目录位于 `<data-dir>/knowledge/skills` 内、不是符号链接/不越界、delete-prep 备份中有同名目录、vault 中有对应 slug 的 `SKILL.md`。文件删除在数据库事务提交后执行。
- 成功写 `<prep>/delete-result.json`，包含前后行数、删除的策略 ID、`skills_deleted`、`skill_files_deleted`、`blocking_task_refs`（成功时应为空）、`delete_executed=true`、确认哈希和时间。

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

示例只会写进尚未初始化（没有 `.aha-vault.json`）的 vault；已有 vault 调用 `--samples` 或初始化接口会跳过或返回 409，不会覆盖任何文件。验证满意后可以直接删除 `global/general/示例/` 和 `global/agent-lessons/示例-Agent经验.md`，索引会在下次自动对账时更新。

## 编辑与索引

用 VSCode 或 Obsidian 打开 vault 目录，保存文件即可。写路径会同步更新索引；外部编辑由文件变更轮询（约 3 秒）发现，启动时还会对账并修复缺失或损坏的索引。只有一个单写者索引队列，重复任务会被合并。CLI 仍可用于诊断：

```sh
aha2 knowledge-vault rebuild-index
aha2 knowledge-vault search <关键词>
```

索引只服务于新知识页。旧表切黑是独立的一次性迁移（备份 → 导出 → 校验 → 删除旧表），不再提供运行时回退；删除动作不可逆，必须由 owner 在受控流程中执行。