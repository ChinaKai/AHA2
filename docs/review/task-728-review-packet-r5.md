# task-728 review packet r5：合入 main 的冲突解决

- 上一版实现：`0455c0f`；上一版材料：`27a9827`（r4）
- 本版 merge commit：`53ce62d`，两个父提交是 `27a9827`（task-728 分支）和 `f518dfc`（main，含 task-730 的 f858cd2 / b80d876 / f518dfc）
- worktree `.aha2-worktrees/task_07933ff5b7797feffa4c`，分支 `aha/task_07933ff5b7797feffa4c`
- 没有 push、没有合并 main、没有 complete；现有提交历史未被改写
- 冲突只有一个文件：`web/tests/build.test.mjs`。产品代码、task-730 的详情/任务带/草稿实现和断言都没有被删弱。

## 主 checkout 与 worktree 状态

- 主 checkout `/srv/build/users/kaikai/kk-workspace/AHA2`：`## main...origin/main [ahead 13]`，工作区干净，HEAD `f518dfc`，没有 `MERGE_HEAD` / 未完成 merge；因此没有执行 `git merge --abort`，也没有清理任何文件。
- worktree：`## aha/task_07933ff5b7797feffa4c`，工作区干净，HEAD `53ce62d`；merge 已提交，没有冲突现场残留。

## 冲突原因与解法定

冲突块只有首屏 gzip 预算常量及其注释：

- task-728 在自己的分支上把上限从 273,408 提到 273,664（只读聊天框 + 中英文案 + 不可用态样式，实测 273,544）。
- main 上的 task-730 把上限提到 276,480（详情三栏、任务带、草稿存储；r2 实测 276,244）。

解法不是取任意一边，而是合并两边代码后**重新实测**：用同一个掩码规则和 level-5 gzip 测出合并后的 JS+CSS 为 **276,402**，按仓库惯例 ceil 到下一个 256 字节边界 = **276,480**。所以最终上限取 task-730 的 276,480，注释同时保留 task-728、task-730 r1/r2 的历史测量和这次合并后的 276,402 实测值。

## 两边测试意图都保留

- 相对 main，merge 结果里的 `build.test.mjs` 只多了 task-728 的首屏预算注释（task-728 其他测试在 `assistant.test.mjs` / `pet_model_source.test.mjs`，不受这次冲突影响）。见下面的“相对 main”diff。
- 相对 task-728 分支，merge 结果原样带入了 task-730 的三组断言：详情打开时列表刷新只 patch 任务带、失败的草稿按发起 Task 回写、详情/任务带相关断言。见下面的“相对 task-728”diff。
- 其余 task-730 文件（`web/src/main.ts`、`task_drafts.ts`、`task_filters.ts`、`scripts/build-web.mjs`、三个新测试文件）与 main 逐字节相同；`web/src/styles.css` 只额外保留 task-728 的只读框样式，没有改动 task-730 的规则。

## 实际命令与退出码

| 命令 | 退出码 | 结果 |
| --- | --- | --- |
| `node scripts/build-web.mjs` | 0 | 合并后重新构建，`app.f4e247bd4939.js` / `styles.e5900571fbd7.css`，22 route chunks |
| `cd web && npm test` | 0 | 627/627（620 + task-730 新增 7） |
| `go build ./...` | 0 | 通过 |
| `go vet ./internal/app` | 0 | 通过 |
| `go test -c -o <scratch>/bin/app.test ./internal/app` | 0 | 编译通过 |
| `env -u GOTMPDIR TMPDIR=/tmp/task-728-app <scratch>/bin/app.test -test.count=1 -test.run '^TestClaudeNativeAvailabilityIgnoresRemoteProbe$\|^TestAssistantChatPersistsRefreshedCodexCredential\|^TestNormalizeOwnerAssistantModelDoesNotOverwriteConcurrentSave$'` | 0 | PASS |
| 同一测试二进制，app 全包 `-test.timeout=25m` | 0 | 0 FAIL |

## 未验证

- 没有运行 task-730 新增的 Playwright `.browser.mjs` 用例（需要浏览器夹具），只跑了 `npm test` 的 627 个 Node 测试；合并后的浏览器行为未实测。
- 没有真实 Claude Code / Codex 账号，M4 与本 task 的 CLI 聊天仍未真实鉴权实测。
- 没有 push、没有合并 main、没有部署。

## 相对 main 的 build.test.mjs 增量（task-728 侧保留）

~~~~diff
diff --git a/web/tests/build.test.mjs b/web/tests/build.test.mjs
index 8dcdfc4..6ef40cf 100644
--- a/web/tests/build.test.mjs
+++ b/web/tests/build.test.mjs
@@ -3907,11 +3907,16 @@ test("production web splits route chunks and keeps the first-screen budget", asy
   // the shared bundle gained the per-form labels, endpoints and fields (+480
   // JS, +41 CSS over main's 272,829); measured 273,350, next 256-byte boundary.
   // ADB 三模式新增 UI，实测 273,350，owner 侧分身上限 +512 字节（272,896 → 273,408）。
+  // task-728 unified pet model: the readonly chat field, its shared Chinese/English
+  // copy and the unavailable-state style add 136 bytes over 273,408; measured
+  // 273,544 on its own branch before task-730 merged in.
   // task-730 的任务详情三栏布局把图标侧栏、任务带（范围文案、卡片、收起
   // rail）和按任务隔离的草稿存储放进首屏包；同一掩码版本、level-5 实测
   // 275,929 JS+CSS，ceil 到下一个 256 字节边界。
   // r2 修复任务带实时更新（patchTaskBand / syncTaskListTask）、上传与失败
   // 发送的草稿回写，以及浮层 Esc；实测 276,244，ceil 到下一个边界。
+  // 合入 task-728 后同一掩码、level-5 实测 276,402 JS+CSS，ceil 到下一个
+  // 256 字节边界。
   const FIRST_SCREEN_GZIP_BUDGET = 276480;
   // The version query parameter is a content hash and its hex digits change
   // how the same code gzips: main already sits exactly on the budget, so a
~~~~

## 相对 task-728 的 build.test.mjs 增量（task-730 侧保留）

~~~~diff
diff --git a/web/tests/build.test.mjs b/web/tests/build.test.mjs
index 8c8fcd7..6ef40cf 100644
--- a/web/tests/build.test.mjs
+++ b/web/tests/build.test.mjs
@@ -1076,17 +1076,22 @@ test("mail phase two keeps navigation stable, one mail entry, and table tools",
   assert.match(styles, /\.task-mail-compose-dialog \.dialog-actions \{[^}]*position: sticky;[^}]*bottom: 0;/);
 });
 
-test("SSE list refresh is debounced and deferred while a task detail is open", async () => {
+test("SSE list refresh runs behind a task detail but only patches the band", async () => {
   const root = resolve(import.meta.dirname, "..");
   const script = await readFile(resolve(root, "build", "app.js"), "utf8");
   assert.match(script, /debounceMs: 350/);
   assert.match(script, /maxWaitMs: 1000/);
-  assert.match(script, /canRefresh: \(\)=>Boolean\(state\.auth\?\.authenticated\) && !document\.hidden && !state\.selectedTask && !openingTaskID/);
+  // A detail no longer pauses the data refresh: the band reads state.tasks.
+  assert.match(script, /canRefresh: \(\)=>Boolean\(state\.auth\?\.authenticated\) && !document\.hidden && !openingTaskID/);
   assert.match(script, /isInFlight: \(\)=>listRefreshInFlight/);
   assert.match(script, /function refreshListData\(\)/);
   assert.match(script, /listRefreshScheduler\.request\(\)/);
   assert.match(script, /addEventListener\("visibilitychange"/);
-  assert.match(script, /!state\.selectedTask && !openingTaskID && listPageVisible/);
+  // With a detail open the list stays parked and only the band is replaced.
+  assert.match(script, /if \(state\.selectedTask \|\| openingTaskID\) \{[\s\S]{0,500}patchTaskBand\(\)/);
+  assert.match(script, /else if \(listPageVisible\)/);
+  // The per-Task runtime refresh feeds the same band copy.
+  assert.match(script, /syncTaskListTask\(detail\.task\)/);
   assert.match(script, /listRefreshRequests\+\+/);
   assert.match(script, /taskOpenLastMs/);
 });
@@ -2352,15 +2357,21 @@ test("knowledge review queue folds legacy candidates into the same one-at-a-time
 test("message send clears draft state before awaiting the request", async () => {
   const root = resolve(import.meta.dirname, "..");
   const script = await readFile(resolve(root, "build", "app.js"), "utf8");
-  const clearAt = script.indexOf('state.taskDrafts[agentID] = ""');
+  const clearAt = script.indexOf("clearTaskDraft(state.taskDraftStore, taskID, agentID)");
   const sendAt = script.indexOf("await api.agentMessage(taskID, agentID");
   const acceptedAt = script.indexOf("accepted = true", sendAt);
   const refreshAt = script.indexOf("await refreshTaskRuntime(taskID)", sendAt);
   assert.ok(clearAt >= 0 && sendAt > clearAt);
   assert.ok(acceptedAt > sendAt && refreshAt > acceptedAt);
   assert.match(script, /messageSubmitPending = true/);
-  assert.match(script, /state\.taskAttachmentDrafts\[agentID\] = \[\]/);
-  assert.match(script, /if \(!accepted && state\.selectedTask/);
+  // The clear is scoped to the Task that sent the message, not to whatever
+  // Task happens to be open when a request settles.
+  assert.match(script, /state\.taskDraftStore = clearTaskDraft\(state\.taskDraftStore, taskID, agentID\)/);
+  // A failed send restores the Task that owned the request even after the
+  // owner has already switched away; only the visible composer is conditional.
+  assert.match(script, /if \(!accepted\) \{/);
+  assert.match(script, /state\.taskDraftStore = writeTaskDraft\(state\.taskDraftStore, taskID, agentID, \{\s*text: content,\s*attachments\s*\}\)/);
+  assert.match(script, /if \(state\.selectedTask\?\.task\.id === taskID && state\.selectedTaskAgent === agentID\) \{/);
   assert.match(script, /消息已发送，但刷新失败/);
   assert.match(script, /finally\s*\{\s*messageSubmitPending = false/);
 });
@@ -3022,7 +3033,10 @@ test("task list protects clicks from live refresh and reports open failures", as
   // guard that silently drops a pending refresh.
   assert.doesNotMatch(source, /if \(listRefreshInFlight\) return;/);
   assert.match(source, /async function performListRefresh\(\): Promise<void> \{\s*listRefreshInFlight = true;/);
-  assert.match(source, /!state\.selectedTask && !openingTaskID && listPageVisible/);
+  // The detail branch patches only the band; the list patch stays behind the
+  // visible-list guard.
+  assert.match(source, /if \(state\.selectedTask \|\| openingTaskID\) \{[\s\S]{0,500}patchTaskBand\(\)/);
+  assert.match(source, /else if \(listPageVisible\)/);
 });
 
 test("task list filters combine multi-select project, status, and device choices", async () => {
@@ -3895,8 +3909,15 @@ test("production web splits route chunks and keeps the first-screen budget", asy
   // ADB 三模式新增 UI，实测 273,350，owner 侧分身上限 +512 字节（272,896 → 273,408）。
   // task-728 unified pet model: the readonly chat field, its shared Chinese/English
   // copy and the unavailable-state style add 136 bytes over 273,408; measured
-  // 273,544, next 256-byte boundary.
-  const FIRST_SCREEN_GZIP_BUDGET = 273664;
+  // 273,544 on its own branch before task-730 merged in.
+  // task-730 的任务详情三栏布局把图标侧栏、任务带（范围文案、卡片、收起
+  // rail）和按任务隔离的草稿存储放进首屏包；同一掩码版本、level-5 实测
+  // 275,929 JS+CSS，ceil 到下一个 256 字节边界。
+  // r2 修复任务带实时更新（patchTaskBand / syncTaskListTask）、上传与失败
+  // 发送的草稿回写，以及浮层 Esc；实测 276,244，ceil 到下一个边界。
+  // 合入 task-728 后同一掩码、level-5 实测 276,402 JS+CSS，ceil 到下一个
+  // 256 字节边界。
+  const FIRST_SCREEN_GZIP_BUDGET = 276480;
   // The version query parameter is a content hash and its hex digits change
   // how the same code gzips: main already sits exactly on the budget, so a
   // hash-only change could trip it without growing the payload. Mask the
~~~~
