# Task 755：短屏侧栏与渠道图标评审包

已完成实现与本地验证，等待调度者验收及必要的独立评审、合 main。未合并、push、tag、构建安装包或部署；未改后端权限、数据或路由。

- base：`6a5099dbe859974cc723929a467cbb444424f0bf`
- 源码 head：`49f048b2a22d82cce1ca455a9018c3f09c67d974`
- 分支：`aha/task_ac7b5b2e98bc5a2eed8a`
- worktree：`/srv/build/users/kaikai/kk-workspace/.aha2-worktrees/task_ac7b5b2e98bc5a2eed8a`
- 范围：5 个源码文件、3 个测试文件，198 行新增 / 10 行删除。评审材料另作 docs 提交。符合调度消息给出的 L1 小改规模；尚未做独立评审。

## 行为与断点

完整侧栏在 CSS 有效高度小于 800px 时隐藏整个便签，并缩小区间留白、底部信息内边距；字号、导航 36px、搜索 34px、账号操作 30px 保留。800px 起恢复便签，160px 最小、360px 最大，通过 flex 分配剩余高度，长列表仅内部滚动。折叠状态沿用原逻辑。高度变化不写 localStorage，也不加入尺寸监听或重复渲染。

正常布局实测：英文、长版本、24 条便签在 900px 高度下便签 307px，其他占位合计约 593px；160px 最小便签对应约 753px。选择 800px 给字体、版本换行留余量，800px 实测便签 207px；180px 最窄侧栏同样通过。799px 和 800px 两侧均验证。100dvh 带 100vh fallback，底部 padding 包含 safe-area。过低高度保留侧栏 overflow-y:auto。短屏规则和禁止核心区域收缩的规则限定完整侧栏；原 Task 固定图标 rail、760px 手机切换断点保持原布局。

系统版本和运行时间仍直接显示；长版本可换行，不截断，不新增菜单。机器人 bot 保留给宠物和真正机器人用途。新增 channel 对话气泡来源，供全局侧栏、手机底栏、Task rail、工具按钮、私聊标识与提供方通用占位图标复用。提供方原来的占位图是通用 bot，并非品牌 Logo；没有修改品牌资产。

## 验证与实际限制

所有构建与浏览器 profile、临时文件都在本 Task scratch，使用无生产登录态 API fixture 和 127.0.0.1 临时静态服务器；没有操作 owner 的浏览器，没有启动生产服务，没有安装 npm 依赖。复用 AHA2 主工作区 esbuild 和已有 Playwright 1.49.0 / Chromium headless shell 1255。系统缺中文字体，仅将 Noto Sans CJK SC 字体资源下载到 scratch，并以独立 fontconfig 使用；未安装系统字体。结束后 scratch 会清空。

真实命令与 exit（S 为运行时 AHA2_SCRATCH_DIR）：

```sh
S=/srv/build/users/kaikai/kk-workspace/.aha2-worktrees/task_ac7b5b2e98bc5a2eed8a/.aha2-context/task_ac7b5b2e98bc5a2eed8a/scratch/main
# baseline / fixed 中复制本仓库 scripts、assets、web/src、web/vendor、页面和 package.json；
# internal/httpapi/server.go 用于生产 CSP，web/node_modules 只读链接到主工作区现有依赖。
TMPDIR="$S/tmp" node "$S/baseline/scripts/build-web.mjs" # exit 0
TMPDIR="$S/tmp" node "$S/fixed/scripts/build-web.mjs"    # exit 0，最终源码构建
TMPDIR="$S/tmp" node --test "$S/fixed/web/tests/task_channel_ui.test.mjs" # exit 0，5/5
export TMPDIR="$S/tmp" FONTCONFIG_FILE="$S/fonts.conf"
export AHA_PLAYWRIGHT_MODULE=/tmp/aha2-pkg-0.5.554/browser-host/node_modules/playwright/index.mjs
export AHA_BROWSER_EXECUTABLE=/srv/build/users/kaikai/.cache/ms-playwright/chromium_headless_shell-1255/chrome-headless-shell-linux64/chrome-headless-shell
AHA_SIDEBAR_BASELINE=1 AHA_BROWSER_SCREENSHOT_DIR="$S/baseline-evidence" node "$S/baseline/web/tests/sidebar_height.browser.mjs" # exit 1，60 组几何案例，58 项失败记录
AHA_BROWSER_SCREENSHOT_DIR="$S/fixed-evidence" node "$S/fixed/web/tests/sidebar_height.browser.mjs" # exit 0，60 组，0 失败
AHA_BROWSER_SCREENSHOT_DIR="$S/channel-evidence" node "$S/fixed/web/tests/task_channel_permissions.browser.mjs" # 原文件 exit 1：旧 Playwright waitForFunction 的 eval 被 CSP 拒绝；兼容副本 exit 0
AHA_BROWSER_SCREENSHOT_DIR="$S/narrow-evidence" node "$S/fixed/web/tests/narrow_viewport_layout.browser.mjs" # 原文件 exit 0；另一次兼容副本也 exit 0
# channel 兼容副本仅将 browser.newPage({ 替换为 browser.newPage({bypassCSP: true,
# 所有 fixture 和断言保持；产品 CSP 与仓库测试文件均未因此修改。
git diff --check # exit 0
```

新增几何测试原文件在生产 CSP 下直接通过。覆盖中文/英文 × 0/1/24 条便签 × 1024 高 600/650/720/768/799/800/900、1280×600、1440×900、819×600 共 60 组。819×600 是 1024×750 在 125% 缩放后的等价 CSS 视口；另外验证了 body 字号 20px（默认 16px）、最窄侧栏 180px。检查搜索、七导航、头像、设置和退出的 bounding box、侧栏 scrollHeight/clientHeight。附加检查高屏长便签内部滚动、键盘定位和编辑最后一条、手动折叠偏好以及便签文字/颜色在短→高切换后保持，360px 高度滚动到退出可达，390×844 手机底栏渠道点击可达。0 条存储时沿用现有空白输入行行为。

渠道测试验证全局/Task 工具/私聊同一 SVG、宠物不同 SVG、connected 状态、权限 badge、点击打开渠道、成员筛选与保存、待审批/历史切换、SSE badge 更新、托管/只读 Task、英文文案。原 narrow 测试验证 390×844、844×390 的资源页和知识设置等原手机布局。

未运行 TypeScript 语义类型检查：仓库无 tsconfig，本环境没有可用的 typescript 编译器（import.meta.resolve("typescript") 失败），按不新装依赖约束保留缺口。构建通过只证明 TypeScript 语法剥离和 esbuild 生产打包通过；没有声称 tsc 通过。本次 TS 修改均为图标名称常量。未跑 Go 全仓或无关前端全矩阵；未在实际 iPad/Safari、真实浏览器栏或非零 safe-area 设备上验收。Task rail 的既有极短屏行为不属于此次重排范围。

## 几何证据

下表均为中文、24 条便签，单位 CSS px。全部 bounding box 明细在 baseline-geometry.json / fixed-geometry.json。

| 案例 | 原整栏溢出 | 修复后溢出 | 修复后退出底边 | 修复后便签高 |
|---|---:|---:|---:|---:|
| zh-CN-24-1024x600 | 1348 | 0 | 585.5 | 0 |
| zh-CN-24-1024x650 | 1298 | 0 | 635.5 | 0 |
| zh-CN-24-1024x720 | 1228 | 0 | 705.5 | 0 |
| zh-CN-24-1024x768 | 1180 | 0 | 753.5 | 0 |
| zh-CN-24-1024x799 | 1149 | 0 | 784.5 | 0 |
| zh-CN-24-1024x800 | 1148 | 0 | 781.5 | 198 |
| zh-CN-24-1024x900 | 1048 | 0 | 881.5 | 298 |
| zh-CN-24-1280x600 | 1348 | 0 | 585.5 | 0 |
| zh-CN-24-1440x900 | 1048 | 0 | 881.5 | 298 |
| zh-CN-24-819x600 | 1348 | 0 | 585.5 | 0 |

截图：before-1024x600.png / after-1024x600.png 为同 fixture 对比；after-1024x800.png 展示断点恢复；after-en-1024x900.png 展示长便签内部滚动；mobile-390x844.png 展示原手机底栏；task-channel-private-1024.png 展示 rail/工具栏/私聊共用渠道图标。图中版本与账号来自 fixture，不代表部署状态。

## 必要组件上下文

### web/src/main.ts:4289
```
function shell(content: string, options: {taskShell?: boolean; taskBand?: string} = {}): string {
  const nav = [
    ["projects", "projects", "nav.projects"],
    ["tasks", "tasks", "nav.tasks"],
    ["channels", "channel", "nav.channels"],
    ["knowledge", "knowledge", "nav.knowledge"],
    ["models", "model", "nav.models"],
    ["resources", "hardware", "nav.resources"],
    ["pet-workbench", "bot", "nav.pet"],
  ] as const;
  const mobileNav = [...nav, ["advanced", "settings", "nav.advanced"] as const];
  // The Task detail shell fixes the sidebar to its icon rail and inserts the
  // task band between nav and workspace. Every other page keeps the existing
  // resizable sidebar.
  const taskShell = options.taskShell === true;
  const bandExpanded = taskShell && taskBandExpanded();
  const bandOverlay = taskShell && taskBandNarrow() && state.taskBandNarrowOpen;
  const shellClass = `app-shell${taskShell ? " task-shell" : ""}${taskShell && !bandExpanded ? " task-band-collapsed" : ""}${bandOverlay ? " task-band-overlay" : ""}`;
  return `<div class="${shellClass}">
    <aside class="sidebar">
      <div class="brand-lockup"><span class="brand-mark"><img src="/assets/aha-logo.svg" alt=""></span><div><strong>AHA</strong><small>${t("brand.tagline")}</small></div></div>
      <button type="button" class="command-palette-trigger" data-command-palette-open aria-label="${t("palette.ariaLabel")}（${commandPaletteShortcutLabel()}）" title="${t("palette.placeholder")}">${icon("search")}<span>${t("palette.search")}</span><kbd>${commandPaletteShortcutLabel()}</kbd></button>
      <nav>${nav.map(([view, glyph, label]) => navButton(view, glyph, label)).join("")}</nav>
      <section class="sidebar-memo ${state.sidebarMemoCollapsed ? "collapsed" : ""}">
        <header><strong>${icon("edit")}便签</strong><button id="toggle-sidebar-memo" class="icon-button" type="button" aria-label="${state.sidebarMemoCollapsed ? "展开便签" : "折叠便签"}" title="${state.sidebarMemoCollapsed ? "展开便签" : "折叠便签"}">${icon(state.sidebarMemoCollapsed ? "chevron-up" : "chevron-down")}</button></header>
        <div class="sidebar-memo-colors" role="group" aria-label="便签背景颜色">${Object.entries(SIDEBAR_MEMO_COLORS).map(([id, color]) => `<button type="button" class="sidebar-memo-swatch ${state.sidebarMemoColor === id ? "active" : ""}" data-sidebar-memo-color="${id}" aria-label="${color.label}" title="${color.label}" style="--swatch-color:${color.surface}"></button>`).join("")}</div>
        <div class="sidebar-memo-body">${sidebarMemoItemsHTML()}<small id="sidebar-memo-status">已保存</small></div>
      </section>
      <div class="system-meta"><span>AHA2 ${escapeHTML(state.system.web_version || state.system.version || "dev")}</span><span id="system-uptime">${systemUptimeText()}</span></div>
      <div class="owner-block"><button id="owner-avatar" class="avatar" type="button" title="Owner">O</button><div><strong>${escapeHTML(state.auth?.username || "Owner")}</strong><small>${t("owner.signedIn")}</small></div><div class="owner-actions"><button id="advanced-settings" class="icon-button" type="button" data-view="advanced" title="${t("owner.advanced")}" aria-label="${t("owner.advanced")}">${icon("settings")}</button><button id="logout" class="icon-button" title="${t("owner.logout")}">${icon("logout")}</button></div></div>
    </aside>
    ${taskShell ? "" : `<div id="sidebar-resizer" class="sidebar-resizer" role="separator" aria-label="调整侧栏宽度" aria-orientation="vertical" aria-valuemin="${SIDEBAR_MIN_WIDTH}" aria-valuemax="${SIDEBAR_MAX_WIDTH}" aria-valuenow="${state.sidebarWidth}" tabindex="0" title="拖动调整侧栏宽度，双击恢复默认"></div>`}
    ${taskShell ? (options.taskBand || "") : ""}
    <header class="mobile-header"><div class="brand-lockup"><span class="brand-mark"><img src="/assets/aha-logo.svg" alt=""></span><strong>AHA</strong></div><button id="mobile-context" class="icon-button">${icon("menu")}</button></header>
    <main class="workspace">${resourceStatusHTML()}${banner()}${content}</main>
    <button type="button" class="command-palette-fab" data-command-palette-open aria-label="${t("palette.ariaLabel")}（${commandPaletteShortcutLabel()}）" title="${t("palette.placeholder")}">${icon("search")}</button>
    <nav class="bottom-nav">${mobileNav.map(([view, glyph, label]) => navButton(view, glyph, label)).join("")}</nav>
  </div>`;
}

```

### web/src/main.ts:599
```
function loadSidebarSettings(): {width: number; memoItems: SidebarMemoItem[]; memoCollapsed: boolean; memoColor: string} {
  try {
    const layout = JSON.parse(localStorage.getItem(sidebarLayoutKey) || "{}") as {width?: unknown; memoCollapsed?: unknown; memoColor?: unknown};
    return {
      width: normalizeSidebarWidth(layout.width),
      memoItems: loadSidebarMemoItems(),
      memoCollapsed: layout.memoCollapsed === true,
      memoColor: normalizeSidebarMemoColor(layout.memoColor),
    };
  } catch {
    return {width: SIDEBAR_DEFAULT_WIDTH, memoItems: [newSidebarMemoItem()], memoCollapsed: false, memoColor: "graphite"};
  }
}
```

### web/src/main.ts:3603
```
}

function applySidebarLayout(): void {
  const shellElement = document.querySelector<HTMLElement>(".app-shell");
  if (!shellElement) return;
  shellElement.style.setProperty("--sidebar-width", `${state.sidebarWidth}px`);
  const memo = shellElement.querySelector<HTMLElement>(".sidebar-memo");
  memo?.classList.toggle("collapsed", state.sidebarMemoCollapsed);
  const color = SIDEBAR_MEMO_COLORS[state.sidebarMemoColor] || SIDEBAR_MEMO_COLORS.graphite;
  if (memo) {
    memo.dataset.memoColor = state.sidebarMemoColor;
    memo.style.setProperty("--memo-surface", color.surface);
    memo.style.setProperty("--memo-input", color.input);
    memo.style.setProperty("--memo-border", color.border);
    memo.style.setProperty("--memo-text", color.text);
    memo.style.setProperty("--memo-placeholder", color.placeholder);
  }
  const toggle = shellElement.querySelector<HTMLButtonElement>("#toggle-sidebar-memo");
  if (toggle) {
    toggle.setAttribute("aria-label", state.sidebarMemoCollapsed ? "展开便签" : "折叠便签");
    toggle.title = state.sidebarMemoCollapsed ? "展开便签" : "折叠便签";
    toggle.innerHTML = icon(state.sidebarMemoCollapsed ? "chevron-up" : "chevron-down");
  }
  shellElement.querySelector<HTMLElement>("#sidebar-resizer")?.setAttribute("aria-valuenow", String(state.sidebarWidth));
}
```

### web/src/styles.css:235
```
.sidebar-memo-swatch { width: 18px; min-height: 18px; height: 18px; padding: 0; border: 2px solid transparent; border-radius: 50%; background: var(--swatch-color); box-shadow: inset 0 0 0 1px rgba(15,23,42,.22); }
.sidebar-memo-swatch:hover, .sidebar-memo-swatch:focus-visible, .sidebar-memo-swatch.active { border-color: var(--color-nav-text); outline: 2px solid var(--focus-ring-nav-color); outline-offset: 1px; }
.sidebar-memo-body { display: flex; flex: 1 1 auto; flex-direction: column; min-height: 0; gap: 8px; padding: 8px; }
.sidebar-memo-list { display: grid; align-content: start; gap: 5px; min-height: 0; overflow: auto; }
.sidebar-memo-item { display: grid; grid-template-columns: 16px minmax(0,1fr) 24px; align-items: start; gap: 6px; min-width: 0; padding: 5px 4px; border: 1px solid transparent; border-radius: 5px; background: var(--memo-input); }
.sidebar-memo-item.editing { border-color: var(--blue); }
.sidebar-memo-item > input[type="checkbox"] { width: 15px; height: 15px; margin: 3px 0 0; accent-color: var(--blue); }
.sidebar-memo-preview, .sidebar-memo-item > textarea { grid-column: 2; grid-row: 1; min-width: 0; }
```

### web/src/styles.css:633
```
.app-shell.task-shell.task-band-collapsed { --task-band-width: 48px; }
.app-shell.task-shell.task-band-overlay { --task-band-width: 0px; }
.app-shell.task-shell .sidebar { padding: 18px 8px 14px; overflow: hidden; }
.app-shell.task-shell .sidebar .brand-lockup { justify-content: center; }
.app-shell.task-shell .sidebar .brand-lockup > div,
.app-shell.task-shell .command-palette-trigger > span,
.app-shell.task-shell .command-palette-trigger kbd,
.app-shell.task-shell .sidebar nav button > span:not(.task-tool-badge),
.app-shell.task-shell .sidebar-memo,
.app-shell.task-shell .system-meta,
.app-shell.task-shell .owner-block > div:not(.owner-actions) { display: none; }
.app-shell.task-shell .command-palette-trigger { justify-content: center; gap: 0; width: 48px; margin: 22px auto 0; padding: 0; }
.app-shell.task-shell .sidebar nav { margin-top: 18px; }
.app-shell.task-shell .sidebar nav button { justify-content: center; width: 48px; min-height: 40px; margin: 0 auto; padding: 0; }
.app-shell.task-shell .sidebar nav button .icon { width: 18px; height: 18px; }
.app-shell.task-shell .owner-block { display: flex; flex-direction: column; align-items: center; gap: 8px; padding-top: 14px; }
.app-shell.task-shell .owner-actions { flex-direction: column; }
.app-shell.task-shell .workspace { min-width: 0; }
```

## 完整源码与测试增量

以下 diff 完整覆盖 base 到源码 head，不包含单独 docs 提交。

```diff
diff --git a/web/src/channels.ts b/web/src/channels.ts
index aeca424..6e71858 100644
--- a/web/src/channels.ts
+++ b/web/src/channels.ts
@@ -472,7 +472,7 @@ function openChannelConversations(instance: ChannelInstance, uiOptions: {setMess
 export function renderChannels(providers: ChannelPlugin[], instances: ChannelInstance[], context: ChannelUIContext): string {
   const available = providers.filter(item => item.available);
   const providerRows = providers.length ? providers.map(plugin => `<article class="channel-provider-card ${plugin.available ? "available" : "unavailable"}">
-    <div class="channel-provider-main"><span class="square-icon">${icon("bot")}</span><div><strong>${escapeHTML(plugin.display_name)}</strong><small>${escapeHTML(plugin.provider_key)} · ${escapeHTML(plugin.package_version)} · ${pluginState(plugin)}</small></div></div>
+    <div class="channel-provider-main"><span class="square-icon">${icon("channel")}</span><div><strong>${escapeHTML(plugin.display_name)}</strong><small>${escapeHTML(plugin.provider_key)} · ${escapeHTML(plugin.package_version)} · ${pluginState(plugin)}</small></div></div>
     <div class="channel-provider-actions">${plugin.install_state === "installed" ? `<button type="button" data-channel-plugin-toggle="${escapeHTML(plugin.id)}" data-enabled="${plugin.enabled}" data-revision="${plugin.revision}">${plugin.enabled ? "停用" : "启用"}</button>` : ""}</div>
     ${plugin.last_error ? `<p>${escapeHTML(plugin.last_error)}</p>` : ""}
   </article>`).join("") : `<div class="empty channel-empty"><strong>无可用渠道提供方</strong><p>未安装渠道插件时，项目、任务和知识库仍可正常使用。</p></div>`;
diff --git a/web/src/icons.ts b/web/src/icons.ts
index 585cdb7..b174638 100644
--- a/web/src/icons.ts
+++ b/web/src/icons.ts
@@ -12,6 +12,7 @@ const paths: Record<string, string> = {
   server: '<rect width="20" height="8" x="2" y="2" rx="2"/><rect width="20" height="8" x="2" y="14" rx="2"/><path d="M6 6h.01M6 18h.01"/>',
   monitor: '<rect width="20" height="14" x="2" y="3" rx="2"/><path d="M8 21h8M12 17v4"/>',
   bot: '<rect width="18" height="12" x="3" y="8" rx="2"/><path d="M12 4v4M8 13h.01M16 13h.01"/>',
+  channel: '<path d="M21 11.5a8.4 8.4 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.4 8.4 0 0 1-3.8-.9L3 21l1.9-5.7a8.4 8.4 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.4 8.4 0 0 1 3.8-.9h.5a8.5 8.5 0 0 1 8 8v.5z"/>',
   user: '<circle cx="12" cy="8" r="4"/><path d="M4 21a8 8 0 0 1 16 0"/>',
   link: '<path d="M10 13a5 5 0 0 0 7.5.5l2-2a5 5 0 0 0-7-7l-1.2 1.2"/><path d="M14 11a5 5 0 0 0-7.5-.5l-2 2a5 5 0 0 0 7 7l1.2-1.2"/>',
   branch: '<line x1="6" x2="6" y1="3" y2="15"/><circle cx="18" cy="6" r="3"/><circle cx="6" cy="18" r="3"/><path d="M18 9a9 9 0 0 1-9 9"/>',
diff --git a/web/src/main.ts b/web/src/main.ts
index 4ef0a37..b72d5bc 100644
--- a/web/src/main.ts
+++ b/web/src/main.ts
@@ -4290,7 +4290,7 @@ function shell(content: string, options: {taskShell?: boolean; taskBand?: string
   const nav = [
     ["projects", "projects", "nav.projects"],
     ["tasks", "tasks", "nav.tasks"],
-    ["channels", "bot", "nav.channels"],
+    ["channels", "channel", "nav.channels"],
     ["knowledge", "knowledge", "nav.knowledge"],
     ["models", "model", "nav.models"],
     ["resources", "hardware", "nav.resources"],
@@ -5971,7 +5971,7 @@ function taskChannelPanelHTML(): string {
     return `<div class="task-channel-tool">${switcher}<div class="task-channel-empty"><strong>未连接主渠道</strong><p>先在上方选择已登记的私聊或群聊。每个 Task 只能连接一个主渠道。</p></div>${approvals}</div>`;
   }
   if (currentDestination.endpoint_kind !== "group_digital_human") {
-    return `<div class="task-channel-tool">${switcher}<section class="task-channel-private"><span class="channel-route-icon">${icon("bot")}</span><div><strong>${escapeHTML(currentDestination.display_name || "Owner 私聊")}</strong><small>${escapeHTML(currentDestination.instance_name)} · 私聊主渠道</small></div></section>${approvals}</div>`;
+    return `<div class="task-channel-tool">${switcher}<section class="task-channel-private"><span class="channel-route-icon">${icon("channel")}</span><div><strong>${escapeHTML(currentDestination.display_name || "Owner 私聊")}</strong><small>${escapeHTML(currentDestination.instance_name)} · 私聊主渠道</small></div></section>${approvals}</div>`;
   }
   const selected = new Map(result.contacts.map(contact => [contact.identity_link_id, contact]));
   const query = state.taskChannelMemberQuery.trim().toLocaleLowerCase();
diff --git a/web/src/styles.css b/web/src/styles.css
index 6f85afa..af1db03 100644
--- a/web/src/styles.css
+++ b/web/src/styles.css
@@ -213,7 +213,8 @@ input:focus-visible, select:focus-visible, textarea:focus-visible { border-color
 }
 
 .app-shell { --sidebar-width: 228px; display: grid; grid-template-columns: var(--sidebar-width) 8px minmax(0,1fr); min-height: 100vh; }
-.sidebar { position: sticky; top: 0; display: flex; flex-direction: column; height: 100vh; padding: 22px 14px 16px; overflow-y: auto; background: var(--color-nav); color: var(--color-nav-text); }
+.sidebar { position: sticky; top: 0; display: flex; flex-direction: column; height: 100vh; height: 100dvh; padding: 22px 14px max(16px,env(safe-area-inset-bottom)); overflow-y: auto; background: var(--color-nav); color: var(--color-nav-text); }
+.app-shell:not(.task-shell) .sidebar > :not(.sidebar-memo) { flex-shrink: 0; }
 .sidebar nav { display: grid; gap: 5px; margin-top: 30px; }
 .sidebar nav button { justify-content: flex-start; border: 0; background: transparent; color: var(--color-nav-muted); }
 .sidebar nav button:hover:not(:disabled) { border-color: transparent; background: var(--color-nav-active); color: var(--color-nav-text); }
@@ -224,7 +225,8 @@ input:focus-visible, select:focus-visible, textarea:focus-visible { border-color
 .sidebar-resizer:hover::after, body.sidebar-resizing .sidebar-resizer::after { background: var(--color-accent); box-shadow: var(--focus-ring); }
 .sidebar-resizer:focus-visible::after { background: var(--focus-ring-nav-color); box-shadow: var(--focus-ring-nav); }
 body.sidebar-resizing { cursor: col-resize; user-select: none; }
-.sidebar-memo { --memo-surface: #352a22; --memo-input: var(--color-nav); --memo-border: var(--color-nav-border); --memo-text: var(--color-nav-text); --memo-placeholder: var(--color-nav-muted); display: flex; flex: 0 0 auto; flex-direction: column; min-height: 120px; margin-top: 24px; overflow: hidden; border: 1px solid var(--memo-border); border-radius: var(--radius-card); background: var(--memo-surface); color: var(--memo-text); }
+.sidebar-memo { --memo-surface: #352a22; --memo-input: var(--color-nav); --memo-border: var(--color-nav-border); --memo-text: var(--color-nav-text); --memo-placeholder: var(--color-nav-muted); display: flex; flex: 1 1 0; flex-direction: column; min-height: 160px; max-height: 360px; margin-top: 24px; overflow: hidden; border: 1px solid var(--memo-border); border-radius: var(--radius-card); background: var(--memo-surface); color: var(--memo-text); }
+.sidebar-memo > header, .sidebar-memo-colors, .sidebar-memo-add, .sidebar-memo-body > small { flex-shrink: 0; }
 .sidebar-memo > header { display: flex; align-items: center; justify-content: space-between; gap: 8px; min-height: 38px; padding: 0 8px 0 10px; border-bottom: 1px solid var(--memo-border); color: var(--memo-text); }
 .sidebar-memo > header strong { display: inline-flex; align-items: center; gap: 6px; font-size: 11px; }
 .sidebar-memo > header .icon { width: 14px; height: 14px; color: var(--color-nav-muted); }
@@ -268,7 +270,7 @@ body.sidebar-resizing { cursor: col-resize; user-select: none; }
 .sidebar-memo.collapsed { flex: 0 0 auto; min-height: 38px; }
 .sidebar-memo.collapsed .sidebar-memo-colors { display: none; }
 .sidebar-memo.collapsed .sidebar-memo-body { display: none; }
-.system-meta { display: grid; gap: 3px; margin-top: auto; padding: 12px 4px; color: var(--color-nav-muted); font-size: var(--font-size-11); }
+.system-meta { display: grid; gap: 3px; margin-top: auto; padding: 12px 4px; overflow-wrap: anywhere; color: var(--color-nav-muted); font-size: var(--font-size-11); }
 .owner-block { display: grid; grid-template-columns: 32px minmax(0,1fr) auto; align-items: center; gap: 9px; padding-top: 16px; border-top: 1px solid var(--color-nav-border); }
 .owner-block strong, .owner-block small { display: block; }
 .owner-block small { color: var(--color-nav-muted); font-size: var(--font-size-11); }
@@ -277,6 +279,17 @@ body.sidebar-resizing { cursor: col-resize; user-select: none; }
 .owner-block .icon-button:hover:not(:disabled), .sidebar .icon-button:hover:not(:disabled) { border-color: transparent; background: var(--color-nav-active); color: var(--color-nav-text); }
 .owner-block .owner-actions .icon-button { width: 30px; min-height: 30px; }
 .owner-block .avatar { min-height: 32px; padding: 0; border: 0; cursor: default; }
+/* Keep the full navigation and account controls above notes on short screens.
+   CSS-only visibility preserves the locally saved text, colour and fold state.
+   Scope this to the full sidebar; the Task detail icon rail has its own layout. */
+@media (min-width: 761px) and (max-height: 799px) {
+  .app-shell:not(.task-shell) .sidebar { padding-top: 16px; padding-bottom: max(12px,env(safe-area-inset-bottom)); }
+  .app-shell:not(.task-shell) .sidebar nav { margin-top: 12px; gap: 3px; }
+  .app-shell:not(.task-shell) .command-palette-trigger { margin-top: 12px; }
+  .app-shell:not(.task-shell) .sidebar-memo { display: none; }
+  .app-shell:not(.task-shell) .system-meta { padding-block: 8px; }
+  .app-shell:not(.task-shell) .owner-block { padding-top: 10px; }
+}
 .avatar { display: grid; place-items: center; width: 32px; height: 32px; border-radius: 50%; background: var(--color-accent-soft); color: var(--color-accent-ink); font-weight: 800; }
 .workspace { grid-column: 3; min-width: 0; }
 .mobile-header, .bottom-nav { display: none; }
diff --git a/web/src/task_tools.ts b/web/src/task_tools.ts
index 28dbbae..0cf24d7 100644
--- a/web/src/task_tools.ts
+++ b/web/src/task_tools.ts
@@ -27,7 +27,7 @@ export function normalizePinnedTaskTool(value: unknown): TaskTool | "" {
 }
 
 const tools: Array<{id: TaskTool; label: string; iconName: string}> = [
-  {id: "channel", label: "渠道", iconName: "bot"},
+  {id: "channel", label: "渠道", iconName: "channel"},
   {id: "context", label: "Context", iconName: "context"},
   {id: "hardware", label: "硬件调试", iconName: "hardware"},
   {id: "mail", label: "邮件", iconName: "mail"},
diff --git a/web/tests/sidebar_height.browser.mjs b/web/tests/sidebar_height.browser.mjs
new file mode 100644
index 0000000..6d0af33
--- /dev/null
+++ b/web/tests/sidebar_height.browser.mjs
@@ -0,0 +1,153 @@
+// Real shell geometry against local API fixtures; run after build-web.mjs.
+import assert from "node:assert/strict";
+import {createServer} from "node:http";
+import {mkdir, readFile, writeFile} from "node:fs/promises";
+import {resolve} from "node:path";
+import {pathToFileURL} from "node:url";
+
+const root = resolve(import.meta.dirname, "../..");
+const output = process.env.AHA_BROWSER_SCREENSHOT_DIR;
+assert.ok(output, "Set AHA_BROWSER_SCREENSHOT_DIR to a scratch directory");
+await mkdir(output, {recursive: true});
+const tools = process.env.AHA_WEB_VALIDATION || resolve(root, ".tools/web-validation");
+const {chromium} = await import(pathToFileURL(process.env.AHA_PLAYWRIGHT_MODULE || resolve(tools, "node_modules/playwright/index.mjs")));
+const version = "v0.5.557+6a5099dbe859974cc723929a467cbb444424f0bf";
+const bootstrap = `
+const json = value => new Response(JSON.stringify(value), {headers: {"Content-Type": "application/json"}});
+const language = new URL(location.href).searchParams.get("lang") || "zh-CN";
+window.fetch = async input => {
+  const path = new URL(typeof input === "string" ? input : input.url, location.origin).pathname;
+  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "fixture", owner: {id: "owner", username: "owner"}});
+  if (path === "/api/v1/settings/general") return json({general: {language}});
+  if (path === "/api/v1/system") return json({system: {web_version: ${JSON.stringify(version)}, started_at: "2025-01-01T00:00:00Z"}});
+  if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", enabled: false}, messages: [], models: [], tools: []});
+  if (path === "/api/v1/projects") return json({projects: [{id: "p1", name: "Demo", project_type: "folder"}], has_more: false});
+  if (path === "/api/v1/tasks") return json({tasks: [], has_more: false, counts: {status: {}, project: {}, device: {}}});
+  if (path === "/api/v1/workspaces") return json({workspaces: []});
+  if (path === "/api/v1/events") return json({events: [], cursor: 0});
+  return json({});
+};
+window.EventSource = class {close() {} addEventListener() {}};
+`;
+const html = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/styles.css"></head><body><div id="app"></div><script src="/fixture.js"></script><script type="module" src="/app.js"></script></body></html>`;
+const csp = (await readFile(resolve(root, "internal/httpapi/server.go"), "utf8")).match(/Set\("Content-Security-Policy", "([^"]+)"\)/)[1];
+const server = createServer(async (request, response) => {
+  const path = new URL(request.url, "http://localhost").pathname;
+  try {
+    response.setHeader("Content-Security-Policy", csp);
+    response.setHeader("Content-Type", path.endsWith(".js") ? "text/javascript" : path.endsWith(".css") ? "text/css" : path.endsWith(".svg") ? "image/svg+xml" : path.endsWith(".png") ? "image/png" : "text/html");
+    if (path === "/") response.end(html);
+    else if (path === "/fixture.js") response.end(bootstrap);
+    else if (/^\/(?:[a-z0-9_]+\.(?:js|css)|assets\/[a-zA-Z0-9_/-]+\.(?:svg|png))$/.test(path)) response.end(await readFile(resolve(root, "web/build", path.slice(1))));
+    else response.writeHead(404).end();
+  } catch { response.writeHead(404).end(); }
+});
+await new Promise(done => server.listen(0, "127.0.0.1", done));
+const url = `http://127.0.0.1:${server.address().port}`;
+const browser = await chromium.launch({headless: true, executablePath: process.env.AHA_BROWSER_EXECUTABLE, args: ["--no-sandbox"]});
+const results = [], failures = [], errors = [];
+const nav = ["projects", "tasks", "channels", "knowledge", "models", "resources", "pet-workbench"];
+const selectors = [".command-palette-trigger", ...nav.map(id => `.sidebar nav [data-view="${id}"]`), "#owner-avatar", "#advanced-settings", "#logout"];
+async function geometry(page) {
+  return page.evaluate(selectors => {
+    const sidebar = document.querySelector(".sidebar");
+    const read = selector => {
+      const node = document.querySelector(selector), r = node.getBoundingClientRect();
+      return {selector, x: r.x, y: r.y, right: r.right, bottom: r.bottom, height: r.height};
+    };
+    const memo = document.querySelector(".sidebar-memo"), list = document.querySelector(".sidebar-memo-list");
+    return {height: innerHeight, width: innerWidth, overflow: sidebar.scrollHeight - sidebar.clientHeight,
+      sidebarBottom: sidebar.getBoundingClientRect().bottom, boxes: selectors.map(read),
+      memoVisible: getComputedStyle(memo).display !== "none", memoHeight: memo.getBoundingClientRect().height,
+      listOverflow: list.scrollHeight - list.clientHeight};
+  }, selectors);
+}
+function fits(g, label) {
+  assert.ok(g.overflow <= 1, `${label}: whole sidebar overflows by ${g.overflow}px`);
+  assert.ok(g.sidebarBottom <= g.height + 1, `${label}: sidebar taller than viewport`);
+  for (const box of g.boxes) {
+    assert.ok(box.height >= 30, `${label}: ${box.selector} retains its target size`);
+    assert.ok(box.y >= 0 && box.bottom <= g.height + 1 && box.x >= 0 && box.right <= g.width, `${label}: ${box.selector} outside viewport: ${JSON.stringify(box)}`);
+  }
+}
+try {
+  for (const language of ["zh-CN", "en-US"]) {
+    for (const count of [0, 1, 24]) {
+      const page = await browser.newPage({viewport: {width: 1024, height: 900}});
+      page.on("pageerror", error => errors.push(error.message));
+      await page.addInitScript(({count}) => {
+        localStorage.setItem("aha2.sidebar-memo", JSON.stringify(Array.from({length: count}, (_, i) => ({id: `memo-${i}`, text: `便签 ${i} / Note ${i}: long text that wraps onto a second line`, done: i === 1}))));
+        localStorage.setItem("aha2.sidebar-layout", JSON.stringify({width: 228, memoCollapsed: false, memoColor: "blue"}));
+      }, {count});
+      await page.goto(`${url}/?lang=${language}#projects`, {waitUntil: "networkidle"});
+      await page.waitForSelector("#logout");
+      const saved = await page.evaluate(() => [localStorage.getItem("aha2.sidebar-memo"), localStorage.getItem("aha2.sidebar-layout")]);
+      // 819x600 is the effective CSS viewport of 1024x750 at 125% browser zoom.
+      for (const [width, height] of [[1024,600], [1024,650], [1024,720], [1024,768], [1024,799], [1024,800], [1024,900], [1280,600], [1440,900], [819,600]]) {
+        await page.setViewportSize({width, height});
+        const label = `${language}-${count}-${width}x${height}`;
+        const g = await geometry(page);
+        results.push({label, ...g});
+        try {
+          fits(g, label);
+          if (height < 800) assert.equal(g.memoVisible, false, `${label}: short viewport hides notes`);
+          if (height >= 800) assert.equal(g.memoVisible, true, `${label}: tall viewport restores notes`);
+          if (height === 900 && count === 24) assert.ok(g.listOverflow > 0, `${label}: long notes scroll internally`);
+        } catch (error) { failures.push(error.message); }
+        if (count === 24 && ((width === 1024 && [600,800,900].includes(height)) || (language === "en-US" && width === 819))) {
+          await page.screenshot({path: resolve(output, `${label}.png`)});
+        }
+      }
+      assert.deepEqual(await page.evaluate(() => [localStorage.getItem("aha2.sidebar-memo"), localStorage.getItem("aha2.sidebar-layout")]), saved, "height changes preserve note data, color and fold preference");
+      assert.equal(await page.locator(".system-meta > span").first().textContent(), `AHA2 ${version}`, "full version remains readable");
+      assert.ok((await page.locator("#system-uptime").innerText()).length > 5);
+      await page.setViewportSize({width: 1024, height: 600});
+      await page.evaluate(() => { document.body.style.fontSize = "20px"; });
+      try { fits(await geometry(page), `${language}-${count}-larger-text`); } catch (error) { failures.push(error.message); }
+      await page.evaluate(() => { document.body.style.fontSize = ""; });
+      await page.setViewportSize({width: 1024, height: 800});
+      await page.evaluate(() => document.querySelector(".app-shell").style.setProperty("--sidebar-width", "180px"));
+      try { fits(await geometry(page), `${language}-${count}-minimum-sidebar-width`); } catch (error) { failures.push(error.message); }
+      await page.evaluate(() => document.querySelector(".app-shell").style.setProperty("--sidebar-width", "228px"));
+      if (count === 24) {
+        await page.setViewportSize({width: 1024, height: 900});
+        const last = page.locator('[data-sidebar-memo-edit="memo-23"]');
+        await last.focus();
+        await page.keyboard.press("Enter");
+        assert.equal(await page.locator('[data-sidebar-memo-text="memo-23"]').isVisible(), true, "keyboard reaches last note");
+        try { fits(await geometry(page), "editing last note"); } catch (error) { failures.push(error.message); }
+        await page.locator("#toggle-sidebar-memo").click();
+        const folded = await page.evaluate(() => localStorage.getItem("aha2.sidebar-layout"));
+        await page.setViewportSize({width: 1024, height: 600});
+        await page.setViewportSize({width: 1024, height: 900});
+        assert.equal(await page.locator(".sidebar-memo").evaluate(node => node.classList.contains("collapsed")), true);
+        assert.equal(await page.evaluate(() => localStorage.getItem("aha2.sidebar-layout")), folded);
+      }
+      // Below the supported height, scroll must still expose the account controls.
+      await page.setViewportSize({width: 1024, height: 360});
+      await page.locator("#logout").scrollIntoViewIfNeeded();
+      const logout = await page.locator("#logout").boundingBox();
+      assert.ok(logout.y >= 0 && logout.y + logout.height <= 361, "very short screen retains scroll access");
+      await page.setViewportSize({width: 390, height: 844});
+      assert.equal(await page.locator(".sidebar").isVisible(), false);
+      const mobile = page.locator('.bottom-nav [data-view="channels"]');
+      const pet = page.locator('.bottom-nav [data-view="pet-workbench"]');
+      const mobileBox = await mobile.boundingBox();
+      assert.ok(mobileBox.y >= 0 && mobileBox.y + mobileBox.height <= 845);
+      if (!process.env.AHA_SIDEBAR_BASELINE) {
+        assert.notEqual(await mobile.locator("svg").innerHTML(), await pet.locator("svg").innerHTML());
+      }
+      await mobile.click();
+      await page.waitForSelector(".channels-page");
+      if (count === 1 && language === "zh-CN") await page.screenshot({path: resolve(output, "mobile-390x844.png")});
+      await page.close();
+    }
+  }
+  assert.deepEqual(errors, [], "no runtime / ResizeObserver errors");
+} finally {
+  await writeFile(resolve(output, "geometry.json"), JSON.stringify({results, failures, errors}, null, 2));
+  await browser.close();
+  server.close();
+}
+console.log(`${results.length} geometry cases; ${failures.length} failures`);
+assert.deepEqual(failures, []);
diff --git a/web/tests/task_channel_permissions.browser.mjs b/web/tests/task_channel_permissions.browser.mjs
index f0c8a67..37af753 100644
--- a/web/tests/task_channel_permissions.browser.mjs
+++ b/web/tests/task_channel_permissions.browser.mjs
@@ -23,6 +23,7 @@ const csp = (await readFile(resolve(root, "internal/httpapi/server.go"), "utf8")
 const now = new Date().toISOString();
 const bootstrap = `
 const LANG = new URL(location.href).searchParams.get("lang") || "zh-CN";
+const PRIVATE = new URL(location.href).searchParams.has("private");
 const task = {id: "t-ui", code: "task-736", title: "Channel UI", status: "waiting_user", project_id: "project-1", workspace_id: "workspace-1", channel_instance_id: "", collaboration_mode: "auto", max_agents: 3};
 const hostedTask = {id: "t-hosted", code: "task-737", title: "Channel hosted", status: "waiting_user", project_id: "project-1", workspace_id: "workspace-1", channel_instance_id: "instance-hosted", collaboration_mode: "auto", max_agents: 3};
 const readonlyTask = {id: "t-readonly", code: "task-738", title: "Read only channel", status: "waiting_user", project_id: "project-1", workspace_id: "workspace-1", read_only: true, collaboration_mode: "auto", max_agents: 3};
@@ -62,7 +63,7 @@ window.fetch = async (input, init = {}) => {
   if (path === "/api/v1/codex/accounts") return json({accounts: []});
   if (["t-ui", "t-hosted", "t-readonly"].includes(path.split("/")[4]) && path.split("/").length === 5 && method === "GET") return json(detailFor(path.split("/")[4]));
   if (path === "/api/v1/tasks/t-ui/channel-routes" && method === "GET") return json({
-    destinations: [{conversation_id: "conversation-1", instance_id: "instance-1", instance_name: "Feishu", provider_key: "feishu", endpoint_kind: "group_digital_human", display_name: "联调群", status: "active", updated_at: ${JSON.stringify(now)}}],
+    destinations: [{conversation_id: "conversation-1", instance_id: "instance-1", instance_name: "Feishu", provider_key: "feishu", endpoint_kind: PRIVATE ? "assistant_dm" : "group_digital_human", display_name: "联调群", status: "active", updated_at: ${JSON.stringify(now)}}],
     route: {id: "route-1", instance_id: "instance-1", conversation_id: "conversation-1", target_task_id: "t-ui", state: "active", revision: 1},
     contacts, members,
   });
@@ -115,8 +116,8 @@ const server = createServer(async (request, response) => {
       response.setHeader("Content-Type", "text/javascript");
       response.end(bootstrap);
     } else {
-      if (!/^\/[a-z0-9_]+\.(?:js|css)$/.test(name)) { response.writeHead(404).end(); return; }
-      response.setHeader("Content-Type", name.endsWith(".css") ? "text/css" : "text/javascript");
+      if (!/^\/(?:[a-z0-9_]+\.(?:js|css)|assets\/[a-zA-Z0-9_/-]+\.(?:svg|png))$/.test(name)) { response.writeHead(404).end(); return; }
+      response.setHeader("Content-Type", name.endsWith(".css") ? "text/css" : name.endsWith(".svg") ? "image/svg+xml" : name.endsWith(".png") ? "image/png" : "text/javascript");
       response.end(await readFile(resolve(root, "web/build", name.slice(1))));
     }
   } catch { response.writeHead(404).end(); }
@@ -134,9 +135,13 @@ try {
   await page.waitForSelector('.task-card[data-task="t-ui"]', {timeout: 15000});
   await page.click('.task-card[data-task="t-ui"]');
   await page.waitForSelector('[data-task-tool="channel"]', {timeout: 15000});
+  const globalChannel = await page.locator('.sidebar [data-view="channels"] svg').innerHTML();
+  assert.notEqual(globalChannel, await page.locator('.sidebar [data-view="pet-workbench"] svg').innerHTML());
+  assert.equal(globalChannel, await page.locator('[data-task-tool="channel"] svg').innerHTML());
   assert.equal(await page.locator('[data-task-tool="permissions"]').count(), 0, "the permissions tool is still in the toolbar");
   await page.locator('[data-task-tool="channel"]').click();
   await page.waitForSelector("#task-channel-contacts-form", {timeout: 8000});
+  assert.equal(await page.locator('[data-task-tool="channel"]').evaluate(node => node.classList.contains("connected")), true);
 
   const columns = await page.locator(".task-channel-member-list").evaluate(node => getComputedStyle(node).gridTemplateColumns.split(" ").length);
   assert.equal(columns, 2, `desktop member grid has ${columns} columns`);
@@ -327,6 +332,14 @@ try {
   assert.equal((await english.locator("#toggle-task-permission-history").innerText()).trim(), "View history");
   assert.match(await english.locator("#task-channel-permissions").innerText(), /Pending review/);
   assert.deepEqual(englishErrors, [], "the English channel page threw");
+  const privatePage = await browser.newPage({viewport: {width: 1024, height: 900}});
+  privatePage.on("pageerror", error => errors.push(error.message));
+  await privatePage.goto(`${url}/?private=1`, {waitUntil: "networkidle"});
+  await privatePage.locator('.task-card[data-task="t-ui"]').click();
+  await privatePage.locator('[data-task-tool="channel"]').click();
+  await privatePage.waitForSelector(".task-channel-private .channel-route-icon svg");
+  assert.equal(globalChannel, await privatePage.locator(".channel-route-icon svg").innerHTML());
+  await privatePage.screenshot({path: resolve(output, "task-channel-private-1024.png")});
   assert.deepEqual(errors, [], "the channel page threw while being driven");
   console.log("merged channel page verified in the built Web bundle");
 } finally {
diff --git a/web/tests/task_channel_ui.test.mjs b/web/tests/task_channel_ui.test.mjs
index 5abb91a..b99d046 100644
--- a/web/tests/task_channel_ui.test.mjs
+++ b/web/tests/task_channel_ui.test.mjs
@@ -7,6 +7,7 @@ import {pathToFileURL} from "node:url";
 const root = resolve(import.meta.dirname, "..");
 const tools = await import(pathToFileURL(resolve(root, "build", "task_tools.js")));
 const i18n = await import(pathToFileURL(resolve(root, "build", "i18n.js")));
+const {icon} = await import(pathToFileURL(resolve(root, "build", "icons.js")));
 const detail = {
   task: {id: "t-1", code: "task-1", title: "T", status: "active", project_id: "p", workspace_id: "w"},
   turns: [],
@@ -14,6 +15,13 @@ const detail = {
   memory: {},
 };
 
+test("channel tools share the channel glyph and keep connection state", () => {
+  const channel = tools.renderTaskToolButtons("channel", {channelConnected: true}).match(/<button[^>]*data-task-tool="channel"[^>]*>.*?<\/button>/)?.[0] || "";
+  assert.ok(channel.includes(icon("channel")));
+  assert.ok(!channel.includes(icon("bot")));
+  assert.match(channel, /connected/);
+});
+
 test("the permissions tool is removed and old links resolve to the channel page", () => {
   assert.equal(tools.normalizePinnedTaskTool("permissions"), "channel");
   assert.equal(tools.normalizePinnedTaskTool("channel"), "channel");

```
