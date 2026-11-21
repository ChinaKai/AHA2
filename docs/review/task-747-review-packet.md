# task-747 评审包：渠道页成员身份内联 + 区块顺序 + 任务带左对齐

- 任务：task-747「渠道页联调身份内联 + 区块顺序 + 任务卡片左对齐」
- 基线：`ce5fff6`（main）
- 分支：`aha/task_b1adab91769199061964`
- 工作区：`/srv/build/users/kaikai/kk-workspace/.aha2-worktrees/task_b1adab91769199061964`
- 实现提交：`6c676b5`
- 评审包提交：本文件随后单独提交
- 状态：未合并、未 push、未部署；未改生产环境，未读写凭据。

## 0. 结论

四项需求全部落地，前端单测与构建通过，相关浏览器用例在 headless Chromium
上实跑通过：

1. 「可 @ 联调人」整块删除；勾选成员后身份控件出现在该成员卡片内，取消勾选立即隐藏并清空，保存载荷只含已勾选成员。
2. 「Agent 只会看到这里的人」只保留在「群聊成员」标题右侧的「勾选加入联调」提示里。
3. 页面 DOM 顺序与渲染顺序均为 群聊成员 → 权限申请 → 同步群成员。
4. 任务带卡片内容左对齐；长标题、短标题都从同一左内边距开始，右侧 task 编号留在右内边距。

后端接口与数据模型未改，沿用原有 `PUT /api/v1/tasks/{id}/channel-contacts`。

## 1. 需求与实现对应

### 1.1 成员身份内联

`web/src/main.ts` 的成员卡片由原来的 `<label class="task-channel-member">` 改为：

```html
<div class="task-channel-member">
  <label class="task-channel-member-pick">
    <input type="checkbox" name="contact_id" ...>
    <span class="task-channel-member-text">姓名 / 来源标签</span>
  </label>
  <label class="task-channel-member-role">
    <span class="visually-hidden">身份</span>
    <input name="role_<id>" list="task-channel-role-options" ...>
  </label>
</div>
```

- 身份输入始终渲染，但未勾选时同时 `disabled` + CSS 隐藏；`change` 时
  `syncTaskChannelContactSelection` 统一执行 `role.disabled = !input.checked`，
  未勾选则 `role.value = ""`。
- CSS `.task-channel-member:has(input[name="contact_id"]:checked)
  .task-channel-member-role { display: block; }` 保证勾选后即时出现。
- 提交逻辑仍用 `new FormData(form).getAll("contact_id")` 驱动，只遍历已勾选
  成员；未勾选的身份输入被禁用，不会进入 FormData。
- 保存后 `reloadTaskChannelState()` 重新渲染，服务端返回的
  `collaboration_role` 回填到对应成员卡片。
- datalist 选项按需求收敛为 `APP / 云端 / 固件`；仍可直接输入任意自定义身份。

### 1.2 提示合并

原来独立的 `<section class="task-channel-contacts-section">` 与
「可 @ 联调人 / Agent 只会看到这里的人」一起删除。成员区标题现在是：

```html
<header><strong>群聊成员</strong><small>勾选加入联调 · Agent 只会看到这里的人</small></header>
```

浏览器用例断言整页 `Agent 只会看到这里的人` 只出现 1 次，且位于该标题内。

### 1.3 区块顺序

- 删除 `web/src/styles.css` 里 `.task-channel-tool > ... { order: ... }` 四条
  排序规则，以及窄屏把待审批提前的 `order: 2`。
- `同步群成员` 按钮从群名称 header 移到独立的 `.task-channel-sync`，放在
  `${approvals}` 之后。
- 当前 DOM 顺序：切换渠道 → 群名称 → 成员表单（群聊成员 + 保存）→ 权限申请
  → 同步群成员。浏览器用例用 `compareDocumentPosition` 与
  `getBoundingClientRect().top` 同时断言 DOM 与视觉顺序，桌面和 390px 窄屏
  各验证一次。

### 1.4 任务带卡片左对齐

根因：`.task-band-card` 是 `<button>`，全局 `button` 规则设置了
`justify-content: center`；卡片自身 `display: grid` 但未覆盖该属性。网格只有
一个隐式列，轨道按内容宽度生成后被居中：

- 长标题：轨道接近满宽，内容看起来大致靠左但仍有约 10px 偏移；
- 短标题：轨道只有内容宽，整块内容明显居中。

修复只加在 `.task-band-card`：

```css
.task-band-card {
  display: grid;
  grid-template-columns: minmax(0,1fr);
  justify-content: start;
  ...
}
```

修复前后实测（1280×900，卡片 left=74、padding=10、border=1）：

| 指标 | 修复前 | 修复后 |
| --- | --- | --- |
| 短标题卡片内容左边缘 | 149 | 85 |
| 长标题卡片内容左边缘 | 95 | 85 |
| 状态行左边缘（短标题） | 149 | 85 |
| task 编号右边缘（短标题） | 250 | 314 |
| 卡片内容左基准（74+1+10） | 85 | 85 |
| 卡片内容右基准（325-1-10） | 314 | 314 |

## 2. 关键文件

| 文件 | 改动 |
| --- | --- |
| `web/src/main.ts` | 成员卡片内联身份控件；删除独立联调人列表与移除按钮；合并提示；同步按钮移到权限申请之后 |
| `web/src/styles.css` | 成员卡片两列/窄屏一列；身份控件显示规则；删除 order 排序；`.task-band-card` 左对齐 |
| `web/src/i18n.ts` | 新增/整理渠道页英文文案；新增「固件」翻译 |
| `web/tests/task_channel_permissions.browser.mjs` | 身份出现/隐藏、禁用/清空、保存载荷、保存后回显、DOM/视觉顺序、中英文文案 |
| `web/tests/task_band_typography.browser.mjs` | 增加短标题卡片，断言短/长标题三行内容左对齐且右侧 task 编号不移动 |
| `web/tests/build.test.mjs` | 更新渠道页静态结构断言与 i18n 字典断言 |
| `web/tests/task_channel_ui.test.mjs` | 更新渠道页新文案的英文断言 |

## 3. 实际命令与结果

环境：Linux builder，Node 24.21.0；Playwright 1.65.x +
`chromium_headless_shell-1255`（headless，不是真实桌面浏览器）。
worktree 无 `.tools/web-validation`，Playwright 装在 scratch，用测试自带覆盖口运行
原始测试文件：

```bash
SCRATCH=<task>/scratch/main
export AHA_PLAYWRIGHT_MODULE=$SCRATCH/pw/node_modules/playwright/index.mjs
export AHA_BROWSER_EXECUTABLE=$HOME/.cache/ms-playwright/chromium_headless_shell-1255/chrome-headless-shell-linux64/chrome-headless-shell
export AHA_BROWSER_SCREENSHOT_DIR=$SCRATCH/shots
export TMPDIR=$SCRATCH/tmp
```

| 命令 | 结果 |
| --- | --- |
| `node scripts/build-web.mjs` | exit 0；产出 `web/build` 与 `web/dist` |
| `cd web && node --test tests/*.test.mjs` | exit 0；637 pass / 0 fail |
| `node web/tests/task_channel_permissions.browser.mjs` | exit 0；`merged channel page verified in the built Web bundle` |
| `node web/tests/task_band_typography.browser.mjs` | exit 0；`task band typography verified at 1280 / 272 / 390` |
| `node web/tests/task_card_mobile.browser.mjs` | exit 0；桌面与 390px 任务卡片回归通过 |
| `node web/tests/task_detail_band.browser.mjs` | exit 0 |

构建预算用例 `production web splits route chunks and keeps the first-screen budget`
同样通过；本次新增文案与代码未突破 278,528 字节的 first-screen gzip 预算。

## 4. 验证缺口

- 浏览器验证是 headless Chromium，不是 owner 生产环境的真实 Windows/Edge；
  没有在浏览器里连接真实飞书渠道，保存/回显由 fixture 模拟服务端回显 payload。
  后端接口语义未改，真实链路仍走原有 `PUT channel-contacts`。
- 没有跑完 `web/tests` 下的全部 48 个 `.browser.mjs`；跑了与改动有交集的
  渠道页、任务带排版、移动端任务卡片、任务详情任务带。全量 `npm test`
  （637 项）全通过。
- 截图机器没有中文字体，PNG 里中文显示为方框；文案与几何由 DOM 断言验证，
  截图仅用于核对布局。
- 任务带的左对齐修复针对 `.task-band-card`（button + grid 的场景）。任务列表
  的 `.task-card` 是 article，本身没有该继承问题，未做无关改动。
- 保存请求仍由 Web 端按已勾选成员组装；若服务端未来改变 contacts 语义，需要
  另开任务，本次未调整接口。

## 5. 未做/未提交

- `web/node_modules`、`web/build`、`web/dist`、`.aha2-context` 均为忽略项，
  未进入提交；scratch 中的 Playwright、截图和临时探针已随本 Turn 清理。
- 只提交到本 worktree 分支，未 push、未合并、未部署。

## 6. 完整 diff（`6c676b5`，相对 `ce5fff6`）

```diff
diff --git a/web/src/i18n.ts b/web/src/i18n.ts
index 55e65ac..04452a3 100644
--- a/web/src/i18n.ts
+++ b/web/src/i18n.ts
@@ -1641,6 +1641,7 @@ const legacyEnglish: Record<string, string> = {
   "系统": "System",
   "本机": "This device",
   "云端": "Cloud",
+  "固件": "Firmware",
   "仓库": "Repository",
   "工作区": "Workspace",
   "工作区可写": "Workspace writable",
@@ -2084,7 +2085,6 @@ const legacyEnglish: Record<string, string> = {
   "正在上传附件": "Uploading attachments",
   "选择旧任务后载入其 Main 配置，可继续修改。": "Select an old Task to load its Main configuration.",
   "用这条便签创建任务": "Create a Task from this note",
-  "尚未选择联调人": "No contact selected",
   "其他 Task": "Other Task",
   "接管到本机": "Take over locally",
   "自动压缩": "Auto-compact",
@@ -2810,11 +2810,23 @@ const legacyEnglish: Record<string, string> = {
   "kv.status.unverified": "Unverified",
   "kv.status.conflict": "Conflict",
   // Task channel page: merged permission requests and the compact member list.
+  // main.ts renders literal zh-CN copy and startUITranslationObserver swaps it
+  // in en-US; these entries are that dictionary.
+  "群聊成员": "Group members",
+  "勾选加入联调 · Agent 只会看到这里的人": "Select to join integration · The Agent only sees the people here",
+  "同步群成员": "Sync group members",
+  "身份": "Role",
+  "APP / 云端 / 固件": "APP / Cloud / Firmware",
+  "当前渠道机器人": "Current channel bot",
+  "渠道已观察机器人": "Observed channel bot",
+  "已观察成员": "Observed member",
+  "未命名群聊": "Unnamed group chat",
+  "暂无成员，点击“同步群成员”读取。": "No members yet; click \"Sync group members\" to load them.",
+  "群成员来自渠道同步；观察其他机器人时，请在同一条群消息里同时 @ 当前渠道机器人和目标机器人。": "Group members come from channel sync; to observe another bot, mention the current channel bot and the target bot in the same group message.",
+  "联调人及身份已保存": "Contact identities saved",
   "按姓名搜索成员": "Search members by name",
   "按姓名搜索": "Search by name",
   "没有匹配的成员": "No matching members",
-  "移除联调人": "Remove contact",
-  "身份": "Role",
   "查看历史": "View history",
   "只看待审批": "View pending only",
   "没有待审批": "Nothing pending",
diff --git a/web/src/main.ts b/web/src/main.ts
index d35d790..4ef0a37 100644
--- a/web/src/main.ts
+++ b/web/src/main.ts
@@ -5978,22 +5978,27 @@ function taskChannelPanelHTML(): string {
   const memberRows = result.members.map(member => {
     const contact = selected.get(member.identity_link_id);
     const hidden = !taskChannelMemberMatches(member, query);
-    return `<label class="task-channel-member ${member.is_bot ? "bot" : ""} ${member.is_self ? "self" : ""}" data-member-name="${escapeHTML(member.display_name)}" ${hidden ? "hidden" : ""}>
-      <input type="checkbox" name="contact_id" value="${escapeHTML(member.identity_link_id)}" data-member-name="${escapeHTML(member.display_name)}" data-member-bot="${member.is_bot}" data-member-role="${escapeHTML(contact?.collaboration_role || "")}" ${member.is_self ? "disabled" : ""} ${contact ? "checked" : ""}>
-      <span class="task-channel-member-text"><strong>${escapeHTML(member.display_name)}</strong><small class="task-channel-member-tag">${taskChannelMemberSourceLabel(member)}</small></span>
-    </label>`;
+    return `<div class="task-channel-member ${member.is_bot ? "bot" : ""} ${member.is_self ? "self" : ""}" data-member-name="${escapeHTML(member.display_name)}" ${hidden ? "hidden" : ""}>
+      <label class="task-channel-member-pick">
+        <input type="checkbox" name="contact_id" value="${escapeHTML(member.identity_link_id)}" data-member-name="${escapeHTML(member.display_name)}" data-member-bot="${member.is_bot}" ${member.is_self ? "disabled" : ""} ${contact ? "checked" : ""}>
+        <span class="task-channel-member-text"><strong>${escapeHTML(member.display_name)}</strong><small class="task-channel-member-tag">${taskChannelMemberSourceLabel(member)}</small></span>
+      </label>
+      <label class="task-channel-member-role">
+        <span class="visually-hidden">身份</span>
+        <input name="role_${escapeHTML(member.identity_link_id)}" list="task-channel-role-options" maxlength="40" value="${escapeHTML(contact?.collaboration_role || "")}" placeholder="APP / 云端 / 固件" aria-label="身份" ${contact ? "" : "disabled"}>
+      </label>
+    </div>`;
   }).join("");
-  const selectedRows = result.contacts.map(contact => taskChannelSelectedContactHTML(contact.identity_link_id, contact.display_name, contact.is_bot, contact.collaboration_role || "")).join("");
   const memberEmpty = result.members.length
     ? `<div class="task-channel-member-empty empty-state" ${query ? "" : "hidden"}>没有匹配的成员</div>`
     : '<div class="empty-state">暂无成员，点击“同步群成员”读取。</div>';
   return `<div class="task-channel-tool">
     ${switcher}
-    <div class="task-channel-group-head"><div><strong>${escapeHTML(currentDestination.display_name || "未命名群聊")}</strong><small>${escapeHTML(currentDestination.instance_name)} · ${result.members.length} 名已登记成员</small></div><button type="button" id="refresh-task-channel-members">${icon("refresh")}同步群成员</button></div>
+    <div class="task-channel-group-head"><div><strong>${escapeHTML(currentDestination.display_name || "未命名群聊")}</strong><small>${escapeHTML(currentDestination.instance_name)} · ${result.members.length} 名已登记成员</small></div></div>
     <form id="task-channel-contacts-form">
       <div class="task-channel-people">
         <section class="task-channel-members-section">
-          <header><strong>群聊成员</strong><small>勾选加入联调</small></header>
+          <header><strong>群聊成员</strong><small>勾选加入联调 · Agent 只会看到这里的人</small></header>
           <div class="task-channel-member-tools">
             <label class="task-channel-member-search"><span class="visually-hidden">按姓名搜索成员</span><input id="task-channel-member-search" type="search" placeholder="按姓名搜索" value="${escapeHTML(state.taskChannelMemberQuery)}" autocomplete="off"></label>
             <button type="button" id="task-channel-select-all" class="task-channel-member-action">全选</button>
@@ -6002,23 +6007,12 @@ function taskChannelPanelHTML(): string {
           </div>
           <div class="task-channel-member-list" data-task-channel-member-list>${memberRows}${memberEmpty}</div>
         </section>
-        <section class="task-channel-contacts-section">
-          <header><strong>可 @ 联调人</strong><small>Agent 只会看到这里的人</small></header>
-          <div class="task-channel-contact-list" data-task-channel-selected>${selectedRows || '<div class="empty-state">尚未选择联调人</div>'}</div>
-        </section>
       </div>
-      <datalist id="task-channel-role-options"><option value="APP"><option value="云端"><option value="固件"><option value="测试"><option value="产品"></datalist>
+      <datalist id="task-channel-role-options"><option value="APP"><option value="云端"><option value="固件"></datalist>
       <footer><small>群成员来自渠道同步；观察其他机器人时，请在同一条群消息里同时 @ 当前渠道机器人和目标机器人。</small><button type="submit" class="primary">${icon("save")}保存联调人</button></footer>
     </form>
     ${approvals}
-  </div>`;
-}
-
-function taskChannelSelectedContactHTML(identityID: string, name: string, isBot: boolean, role: string): string {
-  return `<div class="task-channel-contact" data-contact-id="${escapeHTML(identityID)}">
-    <span class="task-channel-contact-person">${icon(isBot ? "bot" : "user")}<strong>${escapeHTML(name)}</strong></span>
-    <label class="task-channel-contact-role"><span class="visually-hidden">身份</span><input name="role_${escapeHTML(identityID)}" list="task-channel-role-options" maxlength="40" value="${escapeHTML(role)}" placeholder="APP / 云端 / 固件" aria-label="身份"></label>
-    <button type="button" class="icon-button task-channel-contact-remove" data-remove-task-contact="${escapeHTML(identityID)}" title="移除联调人" aria-label="移除联调人">${icon("close")}</button>
+    <div class="task-channel-sync"><button type="button" id="refresh-task-channel-members">${icon("refresh")}同步群成员</button></div>
   </div>`;
 }
 
@@ -8856,36 +8850,17 @@ function applyTaskChannelMemberFilter(): void {
 
 function syncTaskChannelContactSelection(): void {
   const form = document.querySelector<HTMLFormElement>("#task-channel-contacts-form");
-  const selected = form?.querySelector<HTMLElement>("[data-task-channel-selected]");
-  if (!form || !selected) return;
-  // Preserve whatever the owner typed before rebuilding the rows.
-  selected.querySelectorAll<HTMLInputElement>('input[name^="role_"]').forEach(input => {
-    const identityID = input.name.slice("role_".length);
-    const checkbox = Array.from(form.querySelectorAll<HTMLInputElement>('input[name="contact_id"]')).find(item => item.value === identityID);
-    if (checkbox) checkbox.dataset.memberRole = input.value;
-  });
-  const rows = Array.from(form.querySelectorAll<HTMLInputElement>('input[name="contact_id"]:checked')).map(input =>
-    taskChannelSelectedContactHTML(
-      input.value,
-      input.dataset.memberName || "群成员",
-      input.dataset.memberBot === "true",
-      input.dataset.memberRole || "",
-    ),
-  ).join("");
-  selected.innerHTML = rows || '<div class="empty-state">尚未选择联调人</div>';
-  selected.querySelectorAll<HTMLInputElement>('input[name^="role_"]').forEach(input => input.addEventListener("input", () => {
-    const identityID = input.name.slice("role_".length);
-    const checkbox = Array.from(form.querySelectorAll<HTMLInputElement>('input[name="contact_id"]')).find(item => item.value === identityID);
-    if (checkbox) checkbox.dataset.memberRole = input.value;
-  }));
-  selected.querySelectorAll<HTMLButtonElement>("[data-remove-task-contact]").forEach(button => button.addEventListener("click", () => {
-    const identityID = button.dataset.removeTaskContact || "";
-    const checkbox = Array.from(form.querySelectorAll<HTMLInputElement>('input[name="contact_id"]')).find(item => item.value === identityID);
-    if (!checkbox || !checkbox.checked) return;
-    // Removing a contact row is the same action as unticking its member chip.
-    checkbox.checked = false;
-    checkbox.dispatchEvent(new Event("change", {bubbles: true}));
-  }));
+  if (!form) return;
+  // Unchecking a member removes their identity. The role control lives in the
+  // same row and stays hidden until the member is selected again; clearing and
+  // disabling it keeps a re-check from restoring a stale identity and keeps the
+  // value out of FormData for members that are not selected.
+  form.querySelectorAll<HTMLInputElement>('input[name="contact_id"]').forEach(input => {
+    const role = form.querySelector<HTMLInputElement>(`input[name="role_${CSS.escape(input.value)}"]`);
+    if (!role) return;
+    role.disabled = !input.checked;
+    if (!input.checked) role.value = "";
+  });
   syncTaskChannelMemberCounts();
 }
 
diff --git a/web/src/styles.css b/web/src/styles.css
index a2b525f..6f85afa 100644
--- a/web/src/styles.css
+++ b/web/src/styles.css
@@ -645,7 +645,7 @@ td small { margin-top: 3px; color: var(--muted); }
 .task-band-head .icon-button:hover { background: var(--color-surface-muted); color: var(--color-text); }
 .task-band-body { flex: 1 1 auto; min-height: 0; overflow-y: auto; padding: 10px; }
 .task-band-list { display: grid; align-content: start; gap: 8px; }
-.task-band-card { display: grid; gap: 5px; width: 100%; min-width: 0; padding: 9px 10px; border: 1px solid var(--line); border-radius: var(--radius-card); background: var(--color-surface); color: inherit; text-align: left; cursor: pointer; }
+.task-band-card { display: grid; grid-template-columns: minmax(0,1fr); justify-content: start; gap: 5px; width: 100%; min-width: 0; padding: 9px 10px; border: 1px solid var(--line); border-radius: var(--radius-card); background: var(--color-surface); color: inherit; text-align: left; cursor: pointer; }
 .task-band-card:hover { border-color: var(--color-border-strong); background: var(--color-surface-muted); }
 .task-band-card.active { border-color: var(--color-accent); background: var(--color-accent-soft); box-shadow: inset 3px 0 0 var(--color-accent); }
 .task-band-card-top { display: flex; align-items: center; justify-content: space-between; gap: 6px; min-width: 0; }
@@ -1444,10 +1444,6 @@ dialog > .dialog-head { margin: 0; padding: 18px 18px 0; }
 .takeover-hardware legend { padding: 0 6px; font-size: 11px; font-weight: 800; }
 .channel-route-icon { display: grid; place-items: center; width: 36px; height: 36px; border-radius: var(--radius-control); background: var(--color-accent-soft); color: var(--color-accent-ink); }
 .task-channel-tool { display: flex; flex-direction: column; min-height: 100%; background: var(--color-surface); }
-.task-channel-tool > .task-channel-switcher { order: 0; }
-.task-channel-tool > .task-channel-group-head { order: 1; }
-.task-channel-tool > #task-channel-contacts-form { order: 3; }
-.task-channel-tool > .task-channel-permissions { order: 4; }
 .task-channel-switcher { position: sticky; top: 0; z-index: 4; display: grid; grid-template-columns: minmax(0,1fr) auto 34px; align-items: end; gap: 7px; padding: 12px; border-bottom: 1px solid var(--color-border); background: var(--color-surface); }
 .task-channel-switcher label { min-width: 0; margin: 0; gap: 4px; font-size: 9px; }
 .task-channel-switcher select { height: 34px; min-width: 0; }
@@ -1476,28 +1472,25 @@ dialog > .dialog-head { margin: 0; padding: 18px 18px 0; }
 .task-channel-member-search input { width: 100%; height: 28px; min-width: 0; }
 .task-channel-member-action { flex: 0 0 auto; min-height: 28px; padding: 0 9px; font-size: 9px; }
 .task-channel-member-count { margin-left: auto; color: var(--muted); font-size: 9px; font-variant-numeric: tabular-nums; white-space: nowrap; }
-.task-channel-member-list { display: grid; grid-template-columns: repeat(3,minmax(0,1fr)); align-content: start; gap: 4px; max-height: 34vh; min-height: 96px; overflow: auto; padding: 8px; }
-.task-channel-member { display: flex; align-items: center; gap: 6px; min-width: 0; height: 36px; margin: 0; padding: 0 7px; border: 1px solid transparent; border-radius: 5px; cursor: pointer; }
+.task-channel-member-list { display: grid; grid-template-columns: repeat(2,minmax(0,1fr)); align-content: start; gap: 4px; max-height: 34vh; min-height: 96px; overflow: auto; padding: 8px; }
+.task-channel-member { display: grid; align-content: start; gap: 5px; min-width: 0; min-height: 36px; margin: 0; padding: 5px 7px; border: 1px solid transparent; border-radius: 5px; cursor: pointer; }
 .task-channel-member:hover, .task-channel-member:has(input:checked) { border-color: #bfdbfe; background: #eff6ff; }
 .task-channel-member.bot:has(input:checked) { border-color: #bbf7d0; background: #f0fdf4; }
 .task-channel-member[hidden] { display: none; }
-.task-channel-member input { flex: 0 0 auto; width: 14px; height: 14px; margin: 0; }
+.task-channel-member-pick { display: flex; align-items: center; gap: 6px; min-width: 0; margin: 0; cursor: pointer; }
+.task-channel-member-pick input { flex: 0 0 auto; width: 14px; height: 14px; margin: 0; }
 .task-channel-member-text { display: flex; flex: 1 1 auto; align-items: center; gap: 5px; min-width: 0; }
 .task-channel-member strong, .task-channel-member-tag { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
 .task-channel-member strong { flex: 0 1 auto; font-size: 10px; }
 .task-channel-member-tag { flex: 0 1 auto; max-width: 74px; padding: 1px 5px; border-radius: 999px; background: #f1f5f9; color: #64748b; font-size: 8px; }
 .task-channel-member.bot .task-channel-member-tag { background: #f0fdf4; color: #15803d; }
 .task-channel-member.self .task-channel-member-tag { background: #eff6ff; color: #1d4ed8; }
+.task-channel-member-role { display: none; min-width: 0; margin: 0; }
+.task-channel-member:has(input[name="contact_id"]:checked) .task-channel-member-role { display: block; }
+.task-channel-member-role input { width: 100%; height: 28px; min-width: 0; }
 .task-channel-member-empty { padding: 14px 8px; }
-.task-channel-contact-list { display: grid; align-content: start; gap: 0; max-height: 34vh; min-height: 96px; overflow: auto; padding: 4px 8px; }
-.task-channel-contact { display: grid; grid-template-columns: minmax(80px,1fr) minmax(110px,1.4fr) 28px; align-items: center; gap: 6px; min-height: 38px; padding: 0 4px; border-bottom: 1px solid #edf0f3; }
-.task-channel-contact-person { display: flex; align-items: center; min-width: 0; gap: 5px; }
-.task-channel-contact-person strong { min-width: 0; overflow: hidden; font-size: 10px; text-overflow: ellipsis; white-space: nowrap; }
-.task-channel-contact .icon { flex: 0 0 auto; width: 14px; color: #64748b; }
-.task-channel-contact-role { min-width: 0; margin: 0; }
-.task-channel-contact-role input { width: 100%; height: 28px; min-width: 0; }
-.task-channel-contact-remove { width: 28px; min-height: 28px; padding: 0; color: var(--muted); }
-.task-channel-contact-remove:hover { color: var(--danger); }
+.task-channel-sync { display: flex; justify-content: flex-end; padding: 10px 12px; border-top: 1px solid var(--line); }
+.task-channel-sync button { min-height: 32px; padding: 0 10px; font-size: 9px; }
 #task-channel-contacts-form > footer { position: sticky; bottom: 0; display: flex; align-items: center; justify-content: flex-end; gap: 10px; padding: 9px 12px; border-top: 1px solid var(--line); background: #fff; }
 #task-channel-contacts-form > footer small { margin-right: auto; color: var(--muted); font-size: 8px; line-height: 1.45; }
 #task-channel-contacts-form > footer button { flex: 0 0 auto; min-height: 34px; }
@@ -2150,12 +2143,7 @@ dialog.agent-config-dialog { width: min(760px,calc(100vw - 32px)); }
 @media (max-width: 760px) {
   .task-channel-switcher { grid-template-columns: minmax(0,1fr) auto 34px; }
   .task-channel-people { grid-template-columns: 1fr; }
-  .task-channel-people > section + section { border-top: 1px solid var(--line); }
-  /* Narrow order: approvals first while something is pending, otherwise the
-     member list and contacts keep their normal order. */
-  .task-channel-tool > .task-channel-permissions.has-pending { order: 2; }
-  .task-channel-member-list { grid-template-columns: repeat(2,minmax(0,1fr)); max-height: 32vh; min-height: 120px; }
-  .task-channel-contact-list { max-height: 32vh; min-height: 120px; }
+  .task-channel-member-list { grid-template-columns: minmax(0,1fr); max-height: 32vh; min-height: 120px; }
   .task-channel-member-count { margin-left: 0; }
   #task-channel-contacts-form > footer { align-items: stretch; flex-direction: column; }
   #task-channel-contacts-form > footer small { margin-right: 0; }
diff --git a/web/tests/build.test.mjs b/web/tests/build.test.mjs
index a9cd32e..2f3fb4d 100644
--- a/web/tests/build.test.mjs
+++ b/web/tests/build.test.mjs
@@ -1180,11 +1180,12 @@ test("task detail can bind one primary private or group channel", async () => {
   const api = await readFile(resolve(root, "build", "api.js"), "utf8");
   const conversation = await readFile(resolve(root, "build", "conversation_ui.js"), "utf8");
   const styles = await readFile(resolve(root, "build", "styles.css"), "utf8");
-  for (const marker of ["task-channel-route-form", "disconnect-task-channel", "task-channel-contacts-form", "群聊成员", "可 @ 联调人", "保存联调人"]) {
+  for (const marker of ["task-channel-route-form", "disconnect-task-channel", "task-channel-contacts-form", "群聊成员", "勾选加入联调 · Agent 只会看到这里的人", "同步群成员", "保存联调人", "task-channel-member-role", "task-channel-sync"]) {
     assert.match(script, new RegExp(marker));
   }
   assert.match(script, /同时 @ 当前渠道机器人和目标机器人/);
   assert.doesNotMatch(script, /task-channel-route-dialog/);
+  assert.doesNotMatch(script, /可 @ 联调人/);
   assert.match(script, /renderTaskToolPanel\([\s\S]*?taskChannelPanelHTML\(\)/);
   assert.match(api, /\/tasks\/\$\{encodeURIComponent\(id\)\}\/channel-routes/);
   assert.match(api, /channel-members:refresh/);
@@ -1192,7 +1193,8 @@ test("task detail can bind one primary private or group channel", async () => {
   assert.match(conversation, /actor\.display_name/);
   assert.match(conversation, /chat_display_name/);
   assert.match(styles, /\.task-channel-people/);
-  assert.match(styles, /\.task-channel-contact-list/);
+  assert.match(styles, /\.task-channel-member-role/);
+  assert.match(styles, /\.task-channel-sync/);
 });
 
 test("task channel entry keeps a stable tool slot and exposes connected state", async () => {
@@ -3433,7 +3435,8 @@ test("task channel page is a compact member picker with pending approvals", asyn
     'id="task-channel-clear"',
     'id="task-channel-member-count"',
     "已选 ${result.contacts.length} / 共 ${result.members.length}",
-    'data-remove-task-contact',
+    'class="task-channel-member-role"',
+    'name="role_${escapeHTML(member.identity_link_id)}"',
     'id="toggle-task-permission-history"',
     'data-task-permission-history="${history}"',
     "只看待审批",
@@ -3441,19 +3444,27 @@ test("task channel page is a compact member picker with pending approvals", asyn
   ]) {
     assert.ok(main.includes(marker), `main.ts missing ${marker}`);
   }
+  // The separate contact list is gone; identities render inside the member row.
+  assert.doesNotMatch(main, /可 @ 联调人/);
+  assert.doesNotMatch(main, /task-channel-contact-list/);
+  assert.doesNotMatch(main, /data-remove-task-contact/);
   assert.match(main, /applyTaskChannelMemberFilter\(\)/);
   assert.match(main, /\.task-channel-member:not\(\[hidden\]\) input\[name="contact_id"\]/);
-  assert.match(main, /checkbox\.checked = false;[\s\S]*?dispatchEvent\(new Event\("change"/);
-  // Desktop is a three-column chip grid; narrow screens fall to two.
-  assert.match(styles, /\.task-channel-member-list \{[^}]*grid-template-columns: repeat\(3,minmax\(0,1fr\)\)/);
-  assert.match(styles, /\.task-channel-member \{[^}]*height: 36px/);
-  assert.match(styles, /@media \(max-width: 760px\)[\s\S]*?\.task-channel-member-list \{ grid-template-columns: repeat\(2,minmax\(0,1fr\)\)/);
-  assert.match(styles, /\.task-channel-contact \{[^}]*min-height: 38px/);
-  assert.match(styles, /\.task-channel-permissions\.has-pending \{ order: 2; \}/);
+  assert.match(main, /role\.disabled = !input\.checked/);
+  assert.match(main, /role\.value = ""/);
+  // Two-column cards carry the identity control; narrow screens use one.
+  assert.match(styles, /\.task-channel-member-list \{[^}]*grid-template-columns: repeat\(2,minmax\(0,1fr\)\)/);
+  assert.match(styles, /\.task-channel-member:has\(input\[name="contact_id"\]:checked\) \.task-channel-member-role \{ display: block; \}/);
+  assert.match(styles, /@media \(max-width: 760px\)[\s\S]*?\.task-channel-member-list \{ grid-template-columns: minmax\(0,1fr\)/);
+  assert.doesNotMatch(styles, /\.task-channel-permissions\.has-pending \{ order:/);
   // New visible copy has both directions in the dictionary.
   assert.match(i18n, /"查看历史": "View history"/);
   assert.match(i18n, /"只看待审批": "View pending only"/);
   assert.match(i18n, /"按姓名搜索": "Search by name"/);
+  assert.match(i18n, /"群聊成员": "Group members"/);
+  assert.match(i18n, /"勾选加入联调 · Agent 只会看到这里的人": "Select to join integration/);
+  assert.match(i18n, /"同步群成员": "Sync group members"/);
+  assert.match(i18n, /"APP \/ 云端 \/ 固件": "APP \/ Cloud \/ Firmware"/);
   assert.ok(i18n.includes("已选 (\\d+) \\/ 共 (\\d+)"), "selected/total count pattern is missing");
 });
 
diff --git a/web/tests/task_band_typography.browser.mjs b/web/tests/task_band_typography.browser.mjs
index 5cfd426..7951e58 100644
--- a/web/tests/task_band_typography.browser.mjs
+++ b/web/tests/task_band_typography.browser.mjs
@@ -25,9 +25,16 @@ const task = {
   status: "active", project_id: "project-1", workspace_id: "workspace-1",
   project_name: "一个名字很长很长的项目用于验证省略号", workspace_name: "一个名字很长时间很长的 Workspace 用于验证省略号", created_at: now,
 };
+// A short title exposes the bug where the grid track was centered inside the
+// card instead of starting at the left padding.
+const shortTask = {
+  id: "t-band-short", code: "task-737", title: "短标题",
+  status: "active", project_id: "project-1", workspace_id: "workspace-1", created_at: now,
+};
 const bootstrap = `
 const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
 const task = ${JSON.stringify(task)};
+const shortTask = ${JSON.stringify(shortTask)};
 window.fetch = async (input, init = {}) => {
   const url = new URL(typeof input === "string" ? input : input.url, location.origin);
   const path = url.pathname;
@@ -36,7 +43,7 @@ window.fetch = async (input, init = {}) => {
   if (path === "/api/v1/settings/general") return json({ok: true, general: {language: "zh-CN"}});
   if (path === "/api/v1/system") return json({system: {web_version: "v0.0.0.test", version: "v0.0.0.test", started_at: new Date().toISOString()}});
   if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", nickname: "AHA", enabled: false}, messages: [], models: [], tools: []});
-  if (path === "/api/v1/tasks" && method === "GET") return json({tasks: [task], has_more: false, counts: {status: {active: 1}, project: {"project-1": 1}, device: {local: 1}}});
+  if (path === "/api/v1/tasks" && method === "GET") return json({tasks: [task, shortTask], has_more: false, counts: {status: {active: 2}, project: {"project-1": 2}, device: {local: 2}}});
   if (path === "/api/v1/projects") return json({projects: [{id: "project-1", name: "一个名字很长很长的项目用于验证省略号", project_type: "folder", channel_retired: false}]});
   if (path === "/api/v1/workspaces") return json({workspaces: [{id: "workspace-1", project_id: "project-1", name: "一个名字很长时间很长的 Workspace 用于验证省略号", root_path: "/tmp/ws", transport: "native", health: "ready"}]});
   if (path === "/api/v1/skills" || path === "/api/v1/models" || path === "/api/v1/providers" || path === "/api/v1/env-groups") return json({skills: [], models: [], providers: [], env_groups: []});
@@ -96,6 +103,24 @@ const bandMetrics = page => page.evaluate(() => {
   };
 });
 
+// Every content row must start at the card's left padding, and the right-side
+// task code must stay at the right padding for both short and long titles.
+const cardAlignment = (page, id) => page.locator(`.task-band-card[data-task-band-open="${id}"]`).evaluate(card => {
+  const style = getComputedStyle(card);
+  const rect = selector => card.querySelector(selector).getBoundingClientRect();
+  const cardRect = card.getBoundingClientRect();
+  const contentLeft = Math.round(cardRect.left + parseFloat(style.borderLeftWidth) + parseFloat(style.paddingLeft));
+  const contentRight = Math.round(cardRect.right - parseFloat(style.borderRightWidth) - parseFloat(style.paddingRight));
+  return {
+    justify: style.justifyContent,
+    contentLeft,
+    contentRight,
+    rowLefts: [".task-band-card-top", ".task-band-card-title", ".task-band-card-meta"].map(selector => Math.round(rect(selector).left)),
+    statusLeft: Math.round(rect(".status").left),
+    codeRight: Math.round(rect(".task-code").right),
+  };
+});
+
 let browser;
 try {
   browser = await chromium.launch({...launchOptions, headless: true});
@@ -121,6 +146,13 @@ try {
   assert.equal(normal.titleClamp, "2");
   assert.ok(normal.cardOverflow <= 1, `card overflows at 1280 (${normal.cardOverflow}px)`);
   assert.equal(normal.docOverflow <= 0, true);
+  for (const id of ["t-band", "t-band-short"]) {
+    const align = await cardAlignment(page, id);
+    assert.equal(align.justify, "start", `${id} grid track is not left aligned (${align.justify})`);
+    assert.deepEqual(align.rowLefts, [align.contentLeft, align.contentLeft, align.contentLeft], `${id} content rows start at ${align.rowLefts}, expected ${align.contentLeft}`);
+    assert.equal(align.statusLeft, align.contentLeft, `${id} status row starts at ${align.statusLeft}, expected ${align.contentLeft}`);
+    assert.equal(align.codeRight, align.contentRight, `${id} task code moved away from the right padding (${align.codeRight} vs ${align.contentRight})`);
+  }
   await page.screenshot({path: resolve(output, "task-band-1280.png")});
 
   // 1000px: the band collapses to the rail; opening it overlays at 272px.
diff --git a/web/tests/task_channel_permissions.browser.mjs b/web/tests/task_channel_permissions.browser.mjs
index 2b3c698..f0c8a67 100644
--- a/web/tests/task_channel_permissions.browser.mjs
+++ b/web/tests/task_channel_permissions.browser.mjs
@@ -32,7 +32,7 @@ const members = [
   {identity_link_id: "carol", display_name: "Carol Wu", is_bot: false, source: "observed"},
   {identity_link_id: "self", display_name: "AHA Bot", is_bot: true, source: "provider", is_self: true},
 ];
-const contacts = [{identity_link_id: "alice", display_name: "Alice Chen", role: "participant", collaboration_role: "APP", is_bot: false, last_seen_at: ${JSON.stringify(now)}}];
+let contacts = [{identity_link_id: "alice", display_name: "Alice Chen", role: "participant", collaboration_role: "APP", is_bot: false, last_seen_at: ${JSON.stringify(now)}}];
 const pending = {id: "perm-pending", task_id: "t-ui", action: "在群聊里公布构建产物下载地址", scope: "release artifacts", reason: "测试同学要看包", status: "pending", applicant_name: "Alice Chen", applicant_is_bot: false, risk: "medium", created_at: ${JSON.stringify(now)}, expires_at: ${JSON.stringify(new Date(Date.now() + 600000).toISOString())}};
 const approved = {id: "perm-approved", task_id: "t-ui", action: "重跑 CI", scope: "workflow", reason: "偶发失败", status: "approved", applicant_name: "Bob Bot", applicant_is_bot: true, decider_name: "owner", decision_reason: "同意重跑", created_at: ${JSON.stringify(now)}, decided_at: ${JSON.stringify(now)}, expires_at: ${JSON.stringify(new Date(Date.now() + 600000).toISOString())}};
 const hostedApproved = {id: "perm-hosted-approved", task_id: "t-hosted", action: "重跑历史 CI", scope: "workflow", reason: "偶发失败", status: "approved", applicant_name: "Bob Bot", applicant_is_bot: true, decider_name: "owner", decision_reason: "同意重跑", created_at: ${JSON.stringify(now)}, decided_at: ${JSON.stringify(now)}, expires_at: ${JSON.stringify(new Date(Date.now() + 600000).toISOString())}};
@@ -46,6 +46,7 @@ const detailFor = id => ({
 });
 const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
 window.__channelRouteCalls = 0;
+window.__contactPayloads = [];
 window.fetch = async (input, init = {}) => {
   const url = new URL(typeof input === "string" ? input : input.url, location.origin);
   const path = url.pathname;
@@ -70,7 +71,15 @@ window.fetch = async (input, init = {}) => {
     return json({ok: false, error: "task_not_eligible", message: "task is not eligible for a primary channel"}, 400);
   }
   if (path.startsWith("/api/v1/tasks/") && path.endsWith("/agents/main/conversation")) return json({conversation: {items: [], has_more: false, next_before: 0, latest_sequence: 0}});
-  if (path === "/api/v1/tasks/t-ui/channel-contacts" && method === "POST") return json({ok: true});
+  if (path === "/api/v1/tasks/t-ui/channel-contacts" && method === "PUT") {
+    const payload = JSON.parse(init.body || "{}");
+    window.__contactPayloads.push(payload);
+    contacts = (payload.contacts || []).map(contact => {
+      const member = members.find(item => item.identity_link_id === contact.identity_link_id) || {};
+      return {...contact, display_name: member.display_name || contact.identity_link_id, is_bot: Boolean(member.is_bot), source: member.source || "provider", last_seen_at: new Date().toISOString()};
+    });
+    return json({contacts});
+  }
   if (path.startsWith("/api/v1/")) return json({});
   return json({});
 };
@@ -130,34 +139,64 @@ try {
   await page.waitForSelector("#task-channel-contacts-form", {timeout: 8000});
 
   const columns = await page.locator(".task-channel-member-list").evaluate(node => getComputedStyle(node).gridTemplateColumns.split(" ").length);
-  assert.equal(columns, 3, `desktop member grid has ${columns} columns`);
-  const heights = await page.locator(".task-channel-member").evaluateAll(nodes => nodes.map(node => Math.round(node.getBoundingClientRect().height)));
-  assert.deepEqual(heights, [36, 36, 36, 36], `member chip heights: ${heights}`);
+  assert.equal(columns, 2, `desktop member grid has ${columns} columns`);
   assert.equal(await page.locator("#task-channel-member-count").innerText(), "已选 1 / 共 4");
   assert.equal(await page.locator('#task-channel-contacts-form input[value="self"]').isDisabled(), true);
 
+  // The identity control lives inside the checked member's row and is absent
+  // from unchecked rows. The separate "可 @ 联调人" block is gone.
+  assert.equal(await page.locator('.task-channel-member[data-member-name="Alice Chen"] .task-channel-member-role').isVisible(), true);
+  assert.equal(await page.locator('.task-channel-member[data-member-name="Bob Bot"] .task-channel-member-role').isVisible(), false);
+  assert.equal(await page.locator('input[name="role_alice"]').inputValue(), "APP");
+  assert.equal(await page.locator('input[name="role_bob"]').isDisabled(), true);
+  assert.equal(await page.locator("text=可 @ 联调人").count(), 0);
+  assert.equal(await page.locator(".task-channel-contact-list").count(), 0);
+  assert.equal(await page.locator('.task-channel-members-section header small').innerText(), "勾选加入联调 · Agent 只会看到这里的人");
+  assert.equal((await page.locator("body").innerText()).match(/Agent 只会看到这里的人/g)?.length, 1);
+
   // Search only hides rows; it never clears the existing selection.
   await page.fill("#task-channel-member-search", "bo");
   const visibleNames = await page.locator(".task-channel-member:not([hidden]) .task-channel-member-text strong").allInnerTexts();
   assert.deepEqual(visibleNames, ["Bob Bot", "AHA Bot"]);
   assert.equal(await page.locator("#task-channel-member-count").innerText(), "已选 1 / 共 4");
 
-  // Select all / clear act on the filtered rows only.
+  // Select all / clear act on the filtered rows only. Checking a member shows
+  // their identity control immediately; clearing hides it and drops the value.
   await page.click("#task-channel-select-all");
   assert.equal(await page.locator("#task-channel-member-count").innerText(), "已选 2 / 共 4");
-  assert.equal(await page.locator(".task-channel-contact").count(), 2);
+  assert.equal(await page.locator('.task-channel-member[data-member-name="Bob Bot"] .task-channel-member-role').isVisible(), true);
+  assert.equal(await page.locator('input[name="role_bob"]').isDisabled(), false);
+  await page.fill('input[name="role_bob"]', "自定义身份");
   await page.click("#task-channel-clear");
   assert.equal(await page.locator("#task-channel-member-count").innerText(), "已选 1 / 共 4");
-  assert.equal(await page.locator(".task-channel-contact").count(), 1);
+  assert.equal(await page.locator('.task-channel-member[data-member-name="Bob Bot"] .task-channel-member-role').isVisible(), false);
+  assert.equal(await page.locator('input[name="role_bob"]').inputValue(), "", "unchecking a member must drop their identity");
+  assert.equal(await page.locator('input[name="role_bob"]').isDisabled(), true);
 
   await page.fill("#task-channel-member-search", "");
   await page.waitForFunction(() => document.querySelectorAll(".task-channel-member:not([hidden])").length === 4);
-  // Removing a contact row is the same as unticking its member chip.
-  await page.click('[data-remove-task-contact="alice"]');
-  await page.waitForFunction(() => document.querySelectorAll(".task-channel-contact").length === 0);
-  assert.equal(await page.locator('#task-channel-contacts-form input[value="alice"]').isChecked(), false);
-  assert.equal(await page.locator("#task-channel-member-count").innerText(), "已选 0 / 共 4");
-  assert.equal(await page.locator('.task-channel-contact-remove').count(), 0);
+
+  // Re-checking starts empty, then a custom identity can be saved. Only checked
+  // members are submitted.
+  await page.locator('input[name="contact_id"][value="bob"]').check();
+  assert.equal(await page.locator('input[name="role_bob"]').inputValue(), "");
+  await page.fill('input[name="role_bob"]', "field-ops");
+  assert.equal(await page.locator('input[name="role_alice"]').inputValue(), "APP");
+  const loadCallsBeforeSave = await page.evaluate(() => window.__channelRouteCalls);
+  await page.click('#task-channel-contacts-form button[type="submit"]');
+  await page.waitForFunction(() => window.__contactPayloads.length === 1, null, {timeout: 8000});
+  const payload = await page.evaluate(() => window.__contactPayloads[0]);
+  assert.deepEqual(payload.contacts, [
+    {identity_link_id: "alice", collaboration_role: "APP"},
+    {identity_link_id: "bob", collaboration_role: "field-ops"},
+  ]);
+  // The fixture echoes the saved contacts, so a post-save reload must re-render
+  // the custom identity on the checked member.
+  await page.waitForFunction(baseline => window.__channelRouteCalls > baseline, loadCallsBeforeSave, {timeout: 8000});
+  assert.equal(await page.locator('input[name="role_bob"]').inputValue(), "field-ops");
+  assert.equal(await page.locator('input[name="contact_id"][value="bob"]').isChecked(), true);
+  assert.equal(await page.locator("#task-channel-member-count").innerText(), "已选 2 / 共 4");
+  assert.equal(await page.locator(".banner.error").count(), 0, "saving contacts surfaced an error");
 
   // The approval section is part of the channel page and starts with pending
   // work only; its buttons are type=button so they cannot submit the form.
@@ -173,24 +212,34 @@ try {
   assert.match(await page.locator("#task-tool-panel-body .task-permission-item").innerText(), /Bob Bot/);
   await page.click("#toggle-task-permission-history");
   await page.waitForFunction(() => document.querySelector("#task-tool-panel-body .task-permission-item")?.innerText.includes("待审批"));
-  await page.screenshot({path: resolve(output, "task-channel-merged.png")});
 
-  // Narrow: two member columns, and a pending approval section moves above the
-  // member list.
-  await page.setViewportSize({width: 390, height: 844});
-  await page.waitForTimeout(150);
-  const narrowColumns = await page.locator(".task-channel-member-list").evaluate(node => getComputedStyle(node).gridTemplateColumns.split(" ").length);
-  assert.equal(narrowColumns, 2, `narrow member grid has ${narrowColumns} columns`);
-  const order = await page.evaluate(() => {
-    const approvals = document.querySelector("#task-channel-permissions");
+  // Members, approvals, sync: both DOM order and rendered order.
+  const order = () => page.evaluate(() => {
     const members = document.querySelector(".task-channel-members-section");
+    const approvals = document.querySelector("#task-channel-permissions");
+    const sync = document.querySelector(".task-channel-sync");
+    const top = node => node?.getBoundingClientRect().top ?? NaN;
+    const follows = (first, second) => Boolean(first && second && (first.compareDocumentPosition(second) & Node.DOCUMENT_POSITION_FOLLOWING));
     return {
-      approvalsFirst: Boolean(approvals && members) && approvals.getBoundingClientRect().top < members.getBoundingClientRect().top,
+      dom: follows(members, approvals) && follows(approvals, sync),
+      visual: top(members) < top(approvals) && top(approvals) < top(sync),
       overflow: document.documentElement.scrollWidth > window.innerWidth,
     };
   });
-  assert.equal(order.approvalsFirst, true, "pending approvals should come first on a narrow screen");
-  assert.equal(order.overflow, false, "the channel page overflows on a phone");
+  const desktopOrder = await order();
+  assert.equal(desktopOrder.dom, true, "DOM order must be members, approvals, sync");
+  assert.equal(desktopOrder.visual, true, "rendered order must be members, approvals, sync");
+  await page.screenshot({path: resolve(output, "task-channel-merged.png")});
+
+  // Narrow: one member column; the three blocks keep the same order.
+  await page.setViewportSize({width: 390, height: 844});
+  await page.waitForTimeout(150);
+  const narrowColumns = await page.locator(".task-channel-member-list").evaluate(node => getComputedStyle(node).gridTemplateColumns.split(" ").length);
+  assert.equal(narrowColumns, 1, `narrow member grid has ${narrowColumns} columns`);
+  const narrowOrder = await order();
+  assert.equal(narrowOrder.dom, true, "narrow DOM order must be members, approvals, sync");
+  assert.equal(narrowOrder.visual, true, "narrow rendered order must be members, approvals, sync");
+  assert.equal(narrowOrder.overflow, false, "the channel page overflows on a phone");
   await page.screenshot({path: resolve(output, "task-channel-merged-390.png")});
 
   // Channel-hosted Tasks keep the channel entry at zero pending and can open
@@ -272,8 +321,10 @@ try {
   await english.waitForSelector("#task-channel-contacts-form", {timeout: 8000});
   assert.equal(await english.locator("#task-channel-member-search").getAttribute("placeholder"), "Search by name");
   assert.equal(await english.locator("#task-channel-member-count").innerText(), "Selected 1 / 4 total");
+  assert.equal((await english.locator(".task-channel-members-section header small").innerText()).trim(), "Select to join integration · The Agent only sees the people here");
+  assert.equal(await english.locator('input[name="role_alice"]').getAttribute("placeholder"), "APP / Cloud / Firmware");
+  assert.equal((await english.locator(".task-channel-sync button").innerText()).trim(), "Sync group members");
   assert.equal((await english.locator("#toggle-task-permission-history").innerText()).trim(), "View history");
-  assert.equal(await english.locator(".task-channel-contact-remove").getAttribute("title"), "Remove contact");
   assert.match(await english.locator("#task-channel-permissions").innerText(), /Pending review/);
   assert.deepEqual(englishErrors, [], "the English channel page threw");
   assert.deepEqual(errors, [], "the channel page threw while being driven");
diff --git a/web/tests/task_channel_ui.test.mjs b/web/tests/task_channel_ui.test.mjs
index d1499f3..5abb91a 100644
--- a/web/tests/task_channel_ui.test.mjs
+++ b/web/tests/task_channel_ui.test.mjs
@@ -47,8 +47,11 @@ test("new channel and approval copy has English translations", () => {
     "没有待审批": "Nothing pending",
     "当前没有待审批的权限申请。": "No pending permission requests.",
     "没有已处理的权限申请。": "No processed permission requests.",
-    "移除联调人": "Remove contact",
+    "群聊成员": "Group members",
+    "勾选加入联调 · Agent 只会看到这里的人": "Select to join integration · The Agent only sees the people here",
+    "同步群成员": "Sync group members",
     "身份": "Role",
+    "APP / 云端 / 固件": "APP / Cloud / Firmware",
     "按姓名搜索": "Search by name",
     "没有匹配的成员": "No matching members",
     "申请时间": "Requested",
```
