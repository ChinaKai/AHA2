# 产品优化 2-5 集成分支报告（task-666）

- 日期：2026-11-19
- 集成分支：`integration/product-2-5-20261119`
- base：`5008f0b629e5b06983a08fe71866901b54fa884c`（main，task-659）
- 合并后代码 HEAD：`03bb2f1e99281a6c76fe871329bcc99a71b4f9cf`
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
| 4 | `cd web && npm test` | 1 | 593 tests / 586 pass / 7 fail |
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

## 4. 残余风险与评审重点

1. **task-664（优先级最高）**：补齐 `moduleNames` 里的 `model_source_picker`；修正 `build.test.mjs` 里 `channel-runtime-source` 的断言位置；重新测量首屏预算；决定 `CodexAccountID` 测试契约。这是让 Web 构建/单测和 `internal/app` 转绿的最小闭环。
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
