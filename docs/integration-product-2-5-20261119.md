# 产品优化 2-5 集成分支报告（task-666）

- 日期：2026-11-19
- 集成分支：`integration/product-2-5-20261119`
- base：`5008f0b629e5b06983a08fe71866901b54fa884c`（main，task-659）
- 合并后代码 HEAD：`e1c7307`（task-668 第二轮复审修复；此前为 `7db9f64`、task-664 修复 `254f45a`、四次 merge 的 `03bb2f1`）
- main 未修改，仍为 `5008f0b629e5b06983a08fe71866901b54fa884c`
- 未 push、未合 main、未部署；未修改 DB/vault/凭据，未修改提示词正文

## 1. 冻结提交

task-662/663/664/665 在 2026-11-19 23:43 被 Owner 中断；四个 worktree 都停在 base、原有 0 commit。本 Task 只把各 worktree 的现有改动原样提交，没有补功能、没有跑 per-task 测试。

| Task | 分支 | 冻结 commit | 内容 |
| --- | --- | --- | --- |
| task-662 | `aha/task_d25c5beb1dddf271bf07` | `778abe5cb81a3f75e430b5705085558b99a784ec` | 宠物任务可跨项目选择技能 |
| task-663 | `aha/task_355fa17980d3165afb66` | `c47d3758d0168262dfc7f14870d9f790e5041423` | 共享浏览器 owner 持续授权，移除下载配额/TTL |
| task-664 | `aha/task_d6f7b88d4216ef52b48a` | `9fd055f2fa9672a7951edcd5518ce708f7726172` | 宠物/渠道运行时按模型来源选择 |
| task-665 | `aha/task_7035b18357184483ad38` | `e61bd2ec219e87d0fce95327d3d98f7bac950d25` | 提示词四块结构、只读 lint 与预览 |

## 2. 合并顺序与结果

| 顺序 | 来源 | merge commit | 结果 |
| --- | --- | --- | --- |
| 1 | task-662 | `8a32fe5af207597f871341a0a04f0eb8468383ed` | 自动合并 |
| 2 | task-663 | `3c240727ed892ec3ad24c974ec20b4351ed33498` | 自动合并（`web/src/i18n.ts` 自动合并） |
| 3 | task-664 | `95d3112` | 自动合并（`i18n.ts`、`main.ts`、`types.ts`、`build.test.mjs` 自动合并） |
| 4 | task-665 | `03bb2f1e99281a6c76fe871329bcc99a71b4f9cf` | 自动合并 |

四次 `git merge --no-ff` 都没有冲突文件，没有手工冲突解决，也没有丢弃任何分支改动。按 Owner 要求，没有在每个 merge 后跑 per-task 测试，只在全部合并后做一次全量验证。

## 3. 全量验证

环境：Linux builder，go1.27.4，node v24.21.0；Go 测试显式使用 `TMPDIR=/tmp`，避免 XFS scratch 上的 SQLite 迁移慢约 100 倍。验证期间同一 worktree 另有一个旧 backend session 并发跑过一次 `go test ./...`；下面的主结果以本 Task 的单次运行日志为准，并发运行额外出现的 timing 失败没有复现，也没有计入。

| # | 命令 | 退出码 | 结果 |
| --- | --- | --- | --- |
| 1 | `go build ./...` | 0 | 通过，6.3s |
| 2 | `go test ./... -count=1 -timeout 1800s` | 1 | 除 `internal/app` 外全部通过；`internal/app` 有 1 个确定性失败，256.3s |
| 3 | `cd web && npm run build` | 0 | 通过，产出 `app.1997ce10835d.js`、`styles.2677c39119a6.css` |
| 4 | `cd web && npm test` | 1 | 593 tests / 586 pass / 7 fail（5 个测试文件因缺模块整体加载失败） |
| 5 | `cd browser-host && npm test` | 0 | 61 tests / 55 pass / 6 skipped / 0 fail |

`browser-host` 的 6 个 skip 是需要 `AHA2_PLAYWRIGHT_MODULE` 的真实 Chromium 用例；本机没有 playwright npm 模块，按测试自身逻辑跳过，属于环境缺口。

### 3.1 Go 失败：task-664 改了模型字段但没更新既有测试

失败用例：`internal/app` → `TestCreateTaskSelectsOfficialCodexModelFromAccount`（`runtime_selection_test.go:69`）。

```text
unexpected hidden runtime model: domain.Model{... ID:"model_codex_official_4585604d93d3e193",
Source:"official", CodexAccountID:"account-official", ...}
```

- task-664 在 `internal/app/runtime_selection.go` 给 official Codex 运行时模型补了 `CodexAccountID: account.ID`。
- 既有测试仍断言 `model.CodexAccountID == ""`。
- 基线对照：同一用例在 base `5008f0b` 上 PASS；在集成分支上单跑稳定 FAIL（0.26s），不是 flaky，也不是并发负载导致。
- 处理：按 Owner“照现状提交并标注”的要求没有改代码。评审需要决定是更新测试到新契约，还是撤掉这个字段赋值。

其余 Go 包全部通过，包括 task-665 新增的 `internal/prompt` 结构测试、`internal/browser`、`internal/httpapi`、`internal/store`。

### 3.2 Web 的 7 个失败：都指向 task-664 的构建接线

所有失败都能追到 task-664 的同一个根因：`scripts/build-web.mjs` 只在 `importers` 映射里给 `channels.js` / `assistant.js` 补了 `model_source_picker` 依赖，但没有把 `"model_source_picker"` 加进文件顶部的 `moduleNames` 数组，所以构建不会生成 `web/build/model_source_picker.js`。

| 失败项 | 原因 |
| --- | --- |
| `channel lifecycle renders active and retired actions with guarded purge` | `channels.js` 运行时 `ERR_MODULE_NOT_FOUND: web/build/model_source_picker.js` |
| `tests/channel_deliveries.test.mjs` | 同上 |
| `tests/channel_knowledge_picker.test.mjs` | 同上 |
| `tests/channel_knowledge_sources.test.mjs` | 同上 |
| `tests/model_source_picker.test.mjs` | 同上 |
| `built web contains responsive application` | task-664 把断言从 `channel_runtime` 改成 `channel-runtime-source`，但该字面量不会出现在 `build/channels.js`：拼接发生在 `model_source_picker.ts` 的 `${prefix}-source` 模板里，即使补上模块也不会出现 |
| `production web splits route chunks and keeps the first-screen budget` | 合并后 JS+CSS gzip 为 270,412 字节，超过 269,824 上限 588 字节 |

首屏预算细节：task-662 把上限从 268,800 提到 269,824（其实测 269,595）；task-664 增加了模型来源 UI 和 import，但没有调整预算。而且当前 270,412 还没有算 `model_source_picker.js` 本身（模块没被构建），补齐接线后大概率会更大，需要重新测量并由评审决定是加预算还是拆包。

基线对照：base `5008f0b` 没有 `web/src/model_source_picker.ts`，`scripts/build-web.mjs` 也没有任何 `model_source_picker` 引用；上述失败断言是 task-664 在 `9fd055f` 里新增/改写的。因此这 7 项都是本次合并暴露的 task-664 分支缺陷，不是基线既有失败。

### 3.3 补齐缺失模块后的补充验证：task-664 的测试与实现本身也不一致

为区分“只缺构建接线”还是“还有逻辑/断言问题”，按 `build-web.mjs` 相同的转换方式在 gitignore 的 `web/build/` 里临时生成了缺失的 `model_source_picker.js`（不改任何源码），再跑 `npm test`：**612 tests / 606 pass / 6 fail**。`channel_deliveries`、`channel_knowledge_picker`、`channel_knowledge_sources` 三个文件转为通过，说明它们只是被缺模块连带拖垮；但仍有以下独立失败：

| 失败项 | 实际输出 | 测试期望 | 性质 |
| --- | --- | --- | --- |
| `channel lifecycle renders active and retired actions with guarded purge` | `Codex official account`、`Provider model`、`data-accountId="account-b"` | `/Codex 官方账号/`、`/Provider 模型/`、`data-account-id="account-b"` | 实现是英文标签 + camelCase 属性，task-664 同时改写的测试按中文标签 + kebab-case 属性断言 |
| `source picker keeps an unavailable saved selection instead of falling back` | `<option value="claude-opus" data-accountId="" selected disabled>` | `/value="claude-opus" selected disabled/` | 属性名不一致，且正则要求属性紧邻 |
| `official saved account and model stay selected when the account is unavailable` | `data-accountId="account-expired"` | `/value="gpt-expired" data-account-id="account-expired" selected disabled/` | 同上 |
| `built web contains responsive application` | `channel-runtime-source` 由 `${prefix}-source` 运行时拼接 | 在 `build/channels.js` 里找字面量 | 断言位置错误（补模块也不会出现） |
| `production web splits route chunks and keeps the first-screen budget` | 270,412 字节 | ≤ 269,824 | 超 588 字节；且尚未计入缺失的 `model_source_picker.js` 对首屏的增量 |

结论：task-664 的提交说明写明“未跑测试”，其实现、测试和构建接线三处互相不一致，不是单一补一行 `moduleNames` 就能全部转绿。最小闭环是：补 `moduleNames` → 统一标签语言与 `data-account-id` 属性命名（实现或测试二选一，按产品文案定）→ 修正 `channel-runtime-source` 断言 → 重新测量首屏预算。

另：补充验证时 `web/node_modules` 已被外部清理，`vendor-only change bumps the production vendor URL but not the app hash` 因临时目录里解析不到 esbuild 而失败；该用例在上一轮（`node_modules` 存在时）通过，且本次四个提交未改动该测试或其解析路径，归为环境状态问题，不计入合并缺陷。

## 4. 残余风险与评审重点

1. **task-664（优先级最高）**：补齐 `moduleNames` 里的 `model_source_picker`；统一模型来源 UI 的标签语言（实现目前是英文，新增测试按中文断言）与账号属性命名（实现输出 `data-accountId`，测试和渠道断言用 `data-account-id`）；修正 `build.test.mjs` 里 `channel-runtime-source` 的断言位置；重新测量首屏预算（当前 270,412，已超 588，补模块后还会涨）；决定 `CodexAccountID` 测试契约。补模块只能让 3 个被连带拖垮的测试文件恢复，不能清掉上述独立失败。
2. **task-663**：代码实际移除了下载文件数、单文件大小、总量配额和 TTL，与“下载上限”这个标题的字面含义可能不一致。请确认产品意图是“取消上限”还是“调整上限”，并评估大文件/长期占盘风险。
3. **task-662**：宠物系统项目可以跨项目选择和读取技能，属于权限面扩大。评审重点看 `validateTaskSkillIDs` 和 `VaultSkillCatalog` 的 `petCatalog` 分支是否严格只对 `project_pet` 生效，普通系统项目不能被顺带放宽。
4. **task-665**：结构预览只重排既有片段，lint 只读；`internal/prompt` 测试已通过。请确认生产 `Build` 逐字不变这一契约仍被测试锁住。
5. 四个分支都没有单独跑过测试，本次只证明“合并后”的编译、Go 包测试、Web node 单测和 browser-host node 测试状态；Web 的 40 个 `.browser.mjs` 浏览器用例、真实 Chromium 用例和端到端启动都没有覆盖。

## 5. 复现命令

```bash
cd /srv/build/users/kaikai/kk-workspace/.aha2-worktrees/task_54066075fa10532411f5
TMPDIR=/tmp go build ./...
TMPDIR=/tmp go test ./... -count=1 -timeout 1800s
cd web && npm run build && npm test
cd ../browser-host && npm test
```

定向复现 Go 失败：

```bash
TMPDIR=/tmp go test ./internal/app \
  -run '^TestCreateTaskSelectsOfficialCodexModelFromAccount$' -count=1 -timeout 300s -v
```

## 6. 最终状态（task-664 修复后）

- 修复提交：`254f45a`「fix(model): 修复模型来源选择的集成测试与首屏预算（task-664）」，父 `1ad8d1a`，分支 `aha/task_d6f7b88d4216ef52b48a`
- 合并方式：`git merge --ff-only aha/task_d6f7b88d4216ef52b48a` 快进，无冲突、无强推；代码 tip `254f45a`
- main 仍为 `5008f0b629e5b06983a08fe71866901b54fa884c`；未 push、未合 main、未部署
- 本报告在 `254f45a` 之上以一笔 docs commit 提交，最终分支 tip 以 `git log -1` 为准，其父为 `254f45a`

### 6.1 最终全量结果

环境：Linux builder，go1.27.4，node v24.21.0；`/tmp` 每步前检查，可用空间稳定在 1.4G（≥1G 门槛）。命令串行执行，无并发。

| # | 命令 | 退出码 | 耗时 | 结果 |
| --- | --- | --- | --- | --- |
| 1 | `go build ./...` | 0 | 1s | 通过 |
| 2 | `go test ./... -count=1 -timeout 1800s` | 0 | 131s | 32 个包全部 ok，无失败 |
| 3 | `cd web && npm run build` | 0 | 1s | 通过；`app.dfa2537cd71d.js`，生成 `build/model_source_picker.js`（14,771 字节） |
| 4 | `cd web && npm test` | 0 | 2s | 613 tests / 613 pass / 0 fail |
| 5 | `cd browser-host && npm test` | 0 | 7s | 61 tests / 55 pass / 6 skipped / 0 fail |

上一轮的 4 个失败面全部清掉：`internal/app` 的 `CodexAccountID` 契约、Web 缺 `model_source_picker.js`、标签语言与 `data-account-id` 断言、首屏预算。

环境说明：本 worktree 的 `web/node_modules` 在验证前被外部清理，构建/测试期间临时软链到主 checkout 的 `web/node_modules`，结束后已移除；未写入任何源码或提交。

### 6.2 flaky、跳过与基线对照

| 项 | 本轮 | 基线/上一轮 | 判定 |
| --- | --- | --- | --- |
| `TestOwnerMessageIsDeliveredInsideActiveMainTurn` | 全量通过 | 上一轮并发全量超时（19.4s），单跑两次通过 | 并发 flaky，非确定性失败 |
| `internal/httpapi` | 全量通过（131s 整体） | 上一轮并发全量失败，单跑 218s 通过 | 并发干扰，非源码失败 |
| browser-host 6 skip | skip | 同上 | 需 `AHA2_PLAYWRIGHT_MODULE` 的真实 Chromium 用例，环境缺口 |
| `web/tests/*.browser.mjs` | 未运行 | `npm test` 只匹配 `tests/*.test.mjs` | 浏览器用例未覆盖，环境缺口 |
| 首屏预算 | 270,972 ≤ 271,104 | 上一轮实测 270,412，阈值 269,824 | 修复后按最终产物重测并上调到下一个 256 字节边界 |

### 6.3 残余风险与评审重点

给唯一一次 Claude native Opus 5.5 评审的重点，按优先级：

1. **task-664 修复 diff（`254f45a`，6 文件 +116/−55）**：`moduleNames` 接线；选择器文案改走共享 i18n（中文留在懒加载模块、英文本地化在 `i18n.ts` 补 22 条）；账号属性统一 `data-account-id` 并用 `dataset.accountId` 回读、空账号不输出空属性；`build.test.mjs` 改断言真实生成的两个 select id；`CodexAccountID` 保留赋值并把既有测试契约改为 `account.ID`，新增空账号/不存在账号拒绝用例。重点看这是否改变宠物设置的回读语义。
2. **首屏预算余量只有 132 字节**（270,972 / 271,104，JS 218,689 + CSS 52,283）；后续任何首屏静态 chunk 改动都要重新测量，不能再靠“补一行”过关。
3. **`data-account-id` → `dataset.accountId` 的回读**：Node 单测只验证字符串输出，真实 DOM 的 dataset 大小写转换、以及既有 HTML 里残留的 `data-accountId` 需要浏览器侧确认。
4. **`CodexAccountID` 回读路径**：确认历史运行时模型（无该字段）和账号删除后的宠物/渠道设置展示不会误报。
5. task-662/663/665 的代码未变，第 4 节原有评审重点仍然有效。
6. 未覆盖：真实 Chromium 用例、`*.browser.mjs`、端到端启动、打包与安装。

## 7. 评审阻塞项修复（task-668）

唯一一次 Claude native Opus 5.5 评审给出 `REQUEST_CHANGES`，三个阻塞项及修复如下。修复提交 `7db9f64`（父 `5ebaa75`），已用 `git merge --ff-only` 快进到集成分支。

### 7.1 三个阻塞项

| # | 阻塞项 | 问题 | `7db9f64` 的修复 |
| --- | --- | --- | --- |
| 1 | task-662 同名技能与项目级 vault 门禁 | 不同项目/scope 的同名技能包目录会互相覆盖；宠物项目级技能只在存在性校验时看 vault 开关，实际加载仍读得到被门禁的技能 | 技能包目录按 `skills/global/<slug>` 与 `skills/project/<projectID>/<slug>` 隔离；同 scope 重名按出现顺序确定性加后缀，不静默覆盖；存在性校验与实际加载两处都检查 `vaultReadEnabled(skill.ProjectID)`，全局技能继续用全局开关，被门禁技能从结果排除并给一次性告警 |
| 2 | task-663 下载整包缓冲与超时 | Go 侧把宿主下载响应体整体读入内存，且 stream client 受总超时限制，大文件会占内存/超时；宿主侧 `fs.readFile` 同样整包读入 | Go 改为流式转发，新增无总超时的 stream client（保留拨号与响应头超时），context 随客户端断开取消；非 2xx 先判状态并最多读 64 KiB 错误体；成功路径才写成功头，Content-Length 仅大小已知时设置；三条路径都关闭宿主响应体。宿主 `createReadStream` 直接 pipe，断开/流错误时销毁读流 |
| 3 | task-664 无效宠物模型阻塞设置保存 | 已删除的模型/账号会让宠物设置页无法关闭或改权限；自动选择官方账号时静默取第一个；错误暴露 `sql: no rows` | 模型选择未变化时跳过重新解析，已删除的模型/账号不再阻塞关闭宠物或改权限；实际变更仍拒绝保存并输出中文业务错误；官方账号自动选择只考虑已配置且 `ready` 的账号，0 个/多个分别报错。渠道 runtime 同样在 `runtime_default` 未变时跳过重新校验，保留 proxy/effort 等非身份字段 |

附带修复：MCP lease 的零值 `expires_at` 不再输出 `0001-01-01T00:00:00Z`。

### 7.2 触碰文件

21 个文件，+1,140 / −113：19 个修改 + 2 个新测试文件。

- 新增测试：`internal/browser/host_download_test.go`、`internal/httpapi/browser_download_stream_test.go`
- browser-host：`downloads.mjs`、`server.mjs`、`interaction.test.mjs`
- Go：`internal/app/{pet.go,pet_model_source_test.go,pet_task_cross_skill_test.go,service_test.go,vault_skills.go}`；`internal/browser/{host.go,interaction.go,interaction_test.go}`；`internal/channel/{service.go,runtime_source_test.go}`；`internal/httpapi/{agent_browser_mcp.go,agent_browser_mcp_test.go,browser_handlers.go,browser_interaction_handlers_test.go}`；`internal/prompt/{registry.go,registry_test.go}`

### 7.3 验证结果

- `254f45a` 时（task-664 修复后）：`go build`、`go test ./...` 32 包、Web build、Web 613/613、browser-host 55 pass/6 skip 全通过。
- `7db9f64` 合入后的最终全量结果见第 7.4 节（本节随全量执行追加）。

### 7.4 `7db9f64` 最终全量

环境：Linux builder，go1.27.4，node v24.21.0；命令串行，每步前检查 `/tmp`（可用 1.3–1.4G，≥1G 门槛）。

| # | 命令 | 退出码 | 耗时 | 结果 |
| --- | --- | --- | --- | --- |
| 1 | `go build ./...` | 0 | 4s | 通过 |
| 2 | `TMPDIR=/tmp go test ./... -count=1 -timeout 1800s` | 1 | 225s | 31 个包 ok；`internal/httpapi` 被 `signal: killed`，无断言失败 |
| 3 | `cd web && npm run build` | 0 | 1s | 通过；`app.dfa2537cd71d.js`，`build/model_source_picker.js`（14,771 字节） |
| 4 | `cd web && npm test` | 0 | 2s | 613 tests / 613 pass / 0 fail |
| 5 | `cd browser-host && npm test` | 0 | 7s | 61 tests / 55 pass / 6 skipped / 0 fail |

`internal/httpapi` 的排查：

1. 全量里是进程被 `signal: killed`（198.9s），日志只有大量 slow request（`workspaces/.../detect` 单次 64.9s），没有断言失败或 panic 输出。
2. 单包复跑第 1 次（107s）只出现一次 `TempDir RemoveAll cleanup: directory not empty`，属后台写入与清理竞态；目标用例单独 `-count=3` 全部通过。
3. 单包复跑第 2 次（136s）整包 `ok`。

结论：不是确定性源码失败，归为资源/并发压力下的 killed + 一次清理竞态 flaky。其余 31 个包在 225s 全量里全部通过，包括 task-668 新增的 `internal/browser`、`internal/prompt`、`internal/app` 测试。

Web 构建/测试期间 `web/node_modules` 仍被外部清理，临时软链到主 checkout 的依赖，结束后已移除；未写入源码或提交。

### 7.5 残余风险

1. **大文件下载只有单元/流式契约测试**：没有端到端下载真实大文件（数百 MB）验证内存峰值、客户端中途断开和宿主错误码解析；真实 Chromium 用例仍需 `AHA2_PLAYWRIGHT_MODULE`。
2. **无总超时的 stream client**：这是流式下载的刻意选择（保留拨号与响应头超时），长连接的终止依赖客户端断开、宿主错误或上层 context；需要评审确认服务端没有残留的全局读写超时会把大文件截断，以及僵尸连接能被及时回收。
3. **技能上下文目录换布局**：`skillResources` 现在写到 `skills/global/<slug>` 与 `skills/project/<projectID>/<slug>`，同 scope 重名加后缀。需要确认长期存在的 Task 上下文目录里旧的扁平 `skills/<slug>` 文件不会作为重复技能残留。
4. **宠物项目级 vault 门禁**：`vaultSkillReadEnabled` 按每个技能的 scope/project 判定，存在性校验和实际加载两处都走同一函数；一次性的缺失告警按 project + id 集合去重，多项目混合选择时是否只出一条可操作告警需要人工确认。
5. **宠物设置跳过重新解析**：运行时选择未变化时跳过校验，意味着后台删除模型/账号后仍可关闭宠物或改权限（产品意图），但“实际变更才拒绝”的边界需要评审确认不会让失效运行时继续跑。
6. 首屏预算与 `data-account-id`/`CodexAccountID` 回读风险仍按第 6.3 节保留。

## 8. 第二轮复审修复（task-668 复审）

第二轮复审又发现两项阻塞：Web 表单发来的空 `pet_model_id` 被误判为“已变化”，以及渠道 provider 的空 `model_id` 被当成可继承值，导致“自动选择”无法正确落盘。修复提交 `e1c7307`（父 `1f7d6c1`），`git merge --ff-only` 快进到集成分支，无冲突。

### 8.1 两项修复

| # | 问题 | 修复 |
| --- | --- | --- |
| 1 | 宠物设置：Web 对 official / claude_native 来源不发 `pet_model_id`，由服务端推导 catalog id；旧逻辑把空 id 当作 id 不同，直接判为“已变化”，已删模型/账号时阻塞保存 | `petSelectionMatchesStored` 在 `pet_model_id` 为空时按 source/wire/account 推导预期模型 ID 再与 `assistant.BrainModelID` 比较；wire/account 缺省时从存储模型行继承；模型行已删除时仍用推导 ID 判断。provider 的空 `model_id` 是显式“自动选择”，必须走重新解析，不能算未变化 |
| 2 | 渠道 runtime：provider 的空 `model_id` 原被当作“继承存储值”，导致显式自动选择无法与已存模型双向切换 | `sameChannelRuntimeSelection` 先归一化存储行 source；provider 的空 `model_id` 直接与存储值比较（空 == 自动），只有 official/claude_native 的空 `model_id` 才继承；provider 显式模型与自动选择双向切换都能正确落盘 |

测试同步改用真实 Web payload（`pet_model_id` 为空字符串），覆盖：已删账号下关闭/启用并改权限、claude_native 未变、provider 自动选择被正常拒绝、模型行删除后仍可保存，以及渠道 provider 自动/显式双向切换。

### 8.2 触碰文件

4 个文件，+295 / −53：

- `internal/app/pet.go`
- `internal/app/pet_model_source_test.go`
- `internal/channel/service.go`
- `internal/channel/runtime_source_test.go`

### 8.3 验证结果

本轮按 Owner 要求把 Go 全量改为 `-p 1` 串行包，避免上次并发导致的 `internal/httpapi` 被 killed。

环境：Linux builder，go1.27.4，node v24.21.0；命令串行，每步前检查 `/tmp`（可用 1.3G，≥1G 门槛）。

| # | 命令 | 退出码 | 耗时 | 结果 |
| --- | --- | --- | --- | --- |
| 1 | `go build ./...` | 0 | 4s | 通过 |
| 2 | `TMPDIR=/tmp go test ./... -p 1 -count=1 -timeout 1800s` | 0 | 478s | 32 个包全部 ok，无失败、无 killed |
| 3 | `cd web && npm run build` | 0 | 1s | 通过；`app.dfa2537cd71d.js`，`build/model_source_picker.js`（14,771 字节） |
| 4 | `cd web && npm test` | 0 | 2s | 613 tests / 613 pass / 0 fail |
| 5 | `cd browser-host && npm test` | 0 | 8s | 61 tests / 55 pass / 6 skipped / 0 fail |

`-p 1` 的效果：上一轮并发全量 225s 内 `internal/httpapi` 被 `signal: killed`；本轮串行 478s 整包通过，没有再出现 killed 或 TempDir 清理竞态。browser-host 的 6 个 skip 仍是需要 `AHA2_PLAYWRIGHT_MODULE` 的真实 Chromium 用例，属于环境缺口。

Web 构建/测试期间 `web/node_modules` 仍被外部清理，临时软链到主 checkout 的依赖，结束后已移除；未写入源码或提交。

残余风险：第 7.5 节的大文件下载端到端、无总超时 stream client、技能上下文目录布局、宠物门禁告警、跳过重新解析边界仍然有效；本轮两个修复把“空 id 判定”补上后，宠物与渠道这两条保存路径不再有已知的确定性失败，但真实浏览器表单的 payload 仍只有单测覆盖。

