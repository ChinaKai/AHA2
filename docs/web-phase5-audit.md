# AHA2 Web 阶段 5 只读体检报告

- 日期：2026-11-13
- 分支：`aha/task_924ec6856823ec0408e7`
- 性质：只读体检。未修改产品源码，未合并、未部署；仅新增本文档。
- 任务来源：AHA Inbox 6761（来自 pet，2026-11-14 14:29:51 +08:00）。

> 说明：任务指定的 `pet-lab/web-optimization/phase5-audit-brief.md` 在工作树、全部分支、AHA2 主检出及 `pet-lab` 目录中均不存在，因此本报告按 Inbox 消息与任务上下文中给出的完整范围执行，未引用缺失 brief 的额外标准。

## 0. 结论摘要

- **未发现 blocker。** 所有高频页面在 5 个指定断点下均可打开和操作，没有横向滚动、首屏遮挡或提交卡死。
- 共记录 **17 项**问题：**High 5、Medium 6、Low 6**。
- 最值得先处理的是键盘与读屏可用性，而不是视觉：
  1. 任务列表卡片只能鼠标点击打开，键盘无法聚焦卡片（High）。
  2. 12 个对话框的图标关闭按钮没有可访问名称（High，axe `button-name` critical）。
  3. 知识库树声明为 `role="tree"`，但子节点不是 treeitem，axe `aria-required-children` critical（High）。
  4. Provider 对话框空值保存时完全没有任何可见错误提示（High）。
  5. 生产 `index.html` 把 `aria-live="polite"` 放在整个 `#app` 上，每次渲染都会让读屏播报整页（High）。
- 响应式与性能在本轮没有发现需要立即修复的问题：60 个页面×断点组合横向溢出均为 0；CLS 为 0；既有的任务列表 SSE 刷新测试通过，滚动与焦点保持正常。性能数据来自本机 fixture，不能替代真实网络/真实后端结论。

## 1. 环境与方法

### 1.1 环境

| 项目 | 值 |
| --- | --- |
| 工作树 | `/srv/build/users/kaikai/kk-workspace/.aha2-worktrees/task_924ec6856823ec0408e7` |
| 构建 | `npm ci --prefix web` 后执行 `node scripts/build-web.mjs`，浏览器加载 `web/build` |
| 浏览器 | Playwright 1.65.1 + Chromium headless shell 1254 |
| 自动化审计 | axe-core 4.10.2，标签 `wcag2a/wcag2aa/wcag21a/wcag21aa/wcag22aa/best-practice` |
| 运行时 | Node v24.21.0，Linux x64 |
| 数据 | 本地 fixture：24 个任务、3 个项目、2 个 Workspace、6 个 Provider、14 个模型、4 个 MCP、7 篇知识库文档、3 条待审批与 9 条历史、2 个邮件账号、2 个渠道实例等 |

### 1.2 方法

- 使用与仓库现有 `web/tests/*.browser.mjs` 相同的模式：Node HTTP 服务提供 `web/build`，注入 fixture 脚本桩接 `/api/v1/*` 与 SSE，并复用生产 CSP（`internal/httpapi/server.go`）和生产 `index.html` 的外壳结构。
- 每个“页面×断点”组合强制整页加载，先等待页面稳定，再采集 axe、横向溢出、触控目标、标题层级、焦点序列、固定元素、图片与 PerformanceObserver 数据。
- 交互探针覆盖：任务创建对话框、Provider 对话框、命令面板、邮件账号对话框、表单错误与重复提交、任务详情输入区、任务列表 SSE 刷新、横屏与 200% 缩放等价视口。
- 探针脚本与截图只存在于 Agent scratch，按任务约束已在结束前删除；本文档中的结论均由可复现的选择器、DOM 属性、数值与源码位置支撑。

### 1.3 限制

- 未连接真实后端与真实网络，LCP/长任务只用于发现量级异常，不作为生产性能验收。
- 未使用真实读屏软件（NVDA/VoiceOver）逐项试听；live region 结论来自 DOM 与生产 HTML/CSS，属于代码级判断。
- 触控目标按 WCAG 2.2 AA（24×24 CSS px）判定，同时标注 44×44 的移动端推荐值，不把 44 当作硬失败。
- 本地 headless Chromium 的原生表单校验文案受浏览器 UI 语言影响（英文），不计入产品问题。

## 2. 覆盖范围

### 2.1 页面 × 断点矩阵

12 个页面场景 × 5 个断点，共 60 次运行，全部成功加载并通过基础交互：

| # | 页面/场景 | 路由与入口 | 360×800 | 390×844 | 768×1024 | 1040×768 | 1280×800 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 1 | 任务列表 | `#tasks` | ✓ | ✓ | ✓ | ✓ | ✓ |
| 2 | 任务详情/会话 | `#tasks` → 首条任务卡 | ✓ | ✓ | ✓ | ✓ | ✓ |
| 3 | 任务内权限申请 | 任务详情 → 工具栏“权限申请” | ✓ | ✓ | ✓ | ✓ | ✓ |
| 4 | 审批中心 | `#pet-workbench` → 审批页签 | ✓ | ✓ | ✓ | ✓ | ✓ |
| 5 | 宠物工作台总览 | `#pet-workbench` | ✓ | ✓ | ✓ | ✓ | ✓ |
| 6 | 模型/Provider | `#models` | ✓ | ✓ | ✓ | ✓ | ✓ |
| 7 | MCP | `#mcp` | ✓ | ✓ | ✓ | ✓ | ✓ |
| 8 | 知识库 | `#knowledge` | ✓ | ✓ | ✓ | ✓ | ✓ |
| 9 | 高级设置 | `#advanced` | ✓ | ✓ | ✓ | ✓ | ✓ |
| 10 | 高级设置·其他 | `#other` | ✓ | ✓ | ✓ | ✓ | ✓ |
| 11 | 渠道配置 | `#channels` | ✓ | ✓ | ✓ | ✓ | ✓ |
| 12 | 资源·邮件 | `#resources?tab=mail` | ✓ | ✓ | ✓ | ✓ | ✓ |

补充视口：844×390、740×360、1024×600（横屏/矮屏）；640×400（1280 宽度的 200% 缩放等价）；任务创建、Provider、命令面板、邮件账号对话框在 390×844 与 1280×800 下的焦点与错误态探针。

### 2.2 通过项概览

- **横向溢出：0。** 60 个组合、3 个横屏视口、5 个 200% 缩放页面均未出现文档级横向滚动。
- **固定栏遮挡：无。** 任务详情输入区在 390×844 下 bottom=836/844，聚焦时底部导航按设计隐藏；未聚焦时输入区与导航之间保留 8px 间距。200% 缩放下输入区 bottom=337、导航 top=338。
- **任务列表 SSE 刷新：通过。** 现有 `web/tests/task_list_live_refresh.browser.mjs` 在本环境通过：状态变化后行更新、筛选浮层不关闭、滚动位置不变、标题编辑焦点与输入值保留、详情打开时暂停列表刷新。
- **重复提交防护：通过。** 任务创建表单连续两次 `requestSubmit()` 只产生 1 次 `POST /api/v1/tasks`；提交中 `aria-busy="true"`、提交按钮禁用、显示 spinner。
- **减少动效：基本通过。** `styles.css:208` 有全局 `prefers-reduced-motion` 规则；仅宠物“思考”指示器存在例外（F16）。
- **CLS：0。** 所有页面/断点均为 0；图片未写 `width/height` 属性，但 CSS 已预留尺寸，未观察到布局抖动。

## 3. 发现汇总表

严重度定义：blocker=核心流程不可用；high=明显阻碍某类用户完成核心操作或产生错误理解；medium=需要额外步骤/明显体验损失；low=局部瑕疵或一致性问题。

| ID | 页面 | 问题 | 实际 | 期望 | 影响 | 严重度 |
| --- | --- | --- | --- | --- | --- | --- |
| F01 | 任务列表 | 任务卡只能用鼠标打开 | `<article class="task-card" data-task>` 无 `tabindex`/`role`，Tab 只停在“编辑标题/删除任务”；回车无效 | 卡片主体可聚焦（链接/按钮）并支持 Enter/Space 打开 | 键盘用户无法直接遍历并打开任务，只能依赖隐藏的 Ctrl+K 命令面板 | High |
| F02 | 任务创建、Provider 等 12 个对话框 | 图标关闭按钮无可访问名称 | `<button data-close class="icon-button">` 内只有 `aria-hidden` 的 SVG，名称空 | `aria-label="关闭"`（邮件/硬件对话框已有此写法） | 读屏只读“按钮”；打开对话框时焦点默认落在该按钮上 | High |
| F03 | 知识库 | `role="tree"` 结构与子节点不合法 | `<nav class="knowledge-tree" role="tree">` 下是普通 div 与 button；无 treeitem/group 角色、无方向键模型 | 移除 tree 角色改用导航+展开按钮，或完整实现 tree/treeitem 与键盘模型 | axe critical；读屏树导航语义错误 | High |
| F04 | 模型/Provider | 空值保存无任何错误提示 | 清空名称/Base URL 后点“保存 Provider”：对话框保持打开，无 inline error、无 banner、无 `aria-invalid` | 对话框内 `role="alert"` 错误提示，并标记两个字段 | 用户不知道为何无法保存，只能反复点击 | High |
| F05 | 全站 | 整个 `#app` 是 polite live region | `web/index.html:14` 的 `<div id="app" aria-live="polite">`；每次 render 替换大段子树，live region 文本等于整页 | 移除根节点 live region；按用途增加局部 `role="status"`/`aria-live`，错误用 `role="alert"` | 导航/刷新/新消息时读屏可能整页播报，真正重要的更新被淹没；视图切换也没有 `document.title` 变化 | High |
| F06 | 命令面板 | listbox 结构错误且 Tab 会落到 BODY | `#command-palette-listbox[role=listbox]` 内 `section[role=group]` 直接包含 `<h3>`；15 次 Tab 中 4 次落在无可见焦点的 BODY | group 内只放 option，标题移出或用 aria-label；显式限制焦点在对话框内 | axe critical；键盘焦点出现“消失”一步 | Medium |
| F07 | 任务创建/全站 | 错误提示未与字段关联，banner 自身无角色 | `.form-error` 有 `role="alert"`，但出错字段 `aria-describedby=null`、`aria-invalid=null`；`.banner` 无 `role`/`aria-live` | 字段级 `aria-invalid`+`aria-describedby`；banner 使用 `role="alert"` 或只在局部播报 | 读屏用户不知道哪个字段出错；banner 是否播报依赖根节点 live region | Medium |
| F08 | 任务列表、任务详情、权限面板、宠物总览 | 标题层级从 h1 直接跳到 h3 | 页面 h1 后直接出现 h3（任务卡标题、“Task 工具”、“权限申请”、“最近动态”） | h1→h2→h3，或把卡片标题降为 h2/段落 | axe `heading-order` 20 个节点；读屏大纲不连续 | Medium |
| F09 | 任务详情、侧栏、模型、宠物总览 | 多处触控目标小于 24×24 | 消息“复制”20×20、便签色块 18×18、后端文档链接 26×13、宠物折叠 summary 高 19、便签预览高 22 | 至少 24×24；移动端常用操作建议 44×44 | 触屏误触，复制/换色尤其明显 | Medium |
| F10 | 全站（侧栏与知识库） | 小字号文本对比度略低于 4.5:1 | 3.96（#7f8996 on #272c33，9–10px）、4.39（#7f8996 on #20242a，11px）、4.34（#64748b on #f1f5f9，9px bold） | ≥4.5:1，或增大字号/字重 | 低视力用户在暗色侧栏与知识库状态标签上阅读困难 | Medium |
| F11 | 全站桌面端 | 无“跳到主内容”，主内容前需穿过 16+ 控件 | 1280 宽从页首 Tab：搜索触发器、8 个导航、便签折叠、5 个色块、复选框、便签预览，16 步仍未到主内容 | 提供 skip link，或让主内容标题可聚焦 | 键盘用户每次换页都要重复穿行 | Medium |
| F12 | 模型/Provider | Provider 对话框不是 form，回车不提交 | `#provider-dialog` 内无 `<form>`；在名称输入框按 Enter，对话框保持打开、焦点不动；`required` 属性不参与校验 | 用 form 包裹并处理 submit，保留手动校验 | 键盘用户需额外 Tab 到按钮，且 required 形同虚设 | Low |
| F13 | 任务创建/Provider 对话框 | 点击遮罩不关闭 | 在对话框外点击后 `open` 仍为 true；Esc 可关闭；宠物工作台对话框则支持点遮罩关闭 | 统一策略：要么都支持，要么都保留 Esc-only 并明确 | 交互不一致，用户可能以为页面卡住 | Low |
| F14 | 任务创建对话框 | Tab 循环末尾出现一个 BODY 停靠点 | 28 个可聚焦控件，第 24 次 Tab 落到 BODY（无焦点环），第 25 次回到关闭按钮 | 最后一个控件直接回到第一个 | 键盘用户短暂失去位置感 | Low |
| F15 | MCP | `article` 上使用 `role="listitem"` | `article[data-mcp-row][role=listitem]`，axe `aria-allowed-role` 20 个节点（4 行×5 断点） | 改用 div，或去掉显式 role 由 role=list 容器承载 | 语义不标准，辅助技术兼容性不稳 | Low |
| F16 | 宠物/助手 | 减少动效模式下仍强制无限动画 | `styles.css:2741`、`2752` 在 `prefers-reduced-motion: reduce` 中用 `!important` 重新启用 pet-think/thinking 点动画 | 减少动效时改为静态指示或仅透明度变化 | 对运动敏感用户不友好 | Low |
| F17 | 任务详情 | `aside` 嵌套在 `main` 内 | `aside.task-tool-panel` 位于 `main.workspace`；axe `landmark-complementary-is-top-level` 10 个节点 | 将 aside 移出 main，或改为带 aria-label 的 section/div | 地标导航语义略混乱 | Low |

## 4. 重点发现详情

### F01 任务卡片键盘不可操作（High）

复现：

1. 打开 `#tasks`（1280×800 或 390×844）。
2. 从页首按 Tab，观察焦点只落在每行的“编辑标题”“删除任务”上。
3. 尝试聚焦任务标题或卡片并按 Enter。

实际：卡片为 `<article class="task-card" data-task="t1">`，`tabIndex=-1`、无 `role`、无可聚焦主体；`card.focus()` 不生效。鼠标点击可打开详情（程序化 click 后 `.task-screen` 出现）。键盘唯一可用的打开方式是 Ctrl+K 命令面板，入口不可见。

期望：卡片主体成为链接或按钮（动作按钮不嵌套），或在卡片上提供显式“打开”控件；Enter/Space 打开详情。

影响：键盘-only 用户无法在列表中浏览和打开任务，属于 WCAG 2.1.1 键盘可操作性问题。

证据：运行时读取 `{tag:"ARTICLE", role:null, tabIndex:-1, hasTabindex:false, focusables:["编辑标题","删除任务"]}`；焦点序列 1–16 步无卡片主体。

### F02 12 个对话框的关闭按钮无可访问名称（High）

复现：`#tasks` → “创建任务”；或 `#models` → “添加 Provider”。用 axe 扫描打开的对话框，或查看对话框头部按钮的无障碍名称。

实际：`<button type="button" data-close class="icon-button"><svg aria-hidden="true">…</svg></button>`，`aria-label`/`title`/文本均为空；任务创建对话框打开后的 `document.activeElement` 正是这个按钮，且名称为空。

期望：`aria-label="关闭"`（`mail_resources.ts`、`mail_task.ts`、`hardware_resources.ts` 已是正确写法）。

影响：读屏用户只听到“按钮”，且初始焦点落在无法识别的控件上。

证据：axe 4.10.2 在 `#task-dialog`、`#provider-dialog` 上均报 `button-name` critical。源码中同类写法共 12 处：

- `web/src/main.ts:3873`（项目）、`3878`（Workspace）、`4397`（Provider）、`4402`（添加模型）、`4406`（编辑模型）、`4410`（接管 Workspace）、`4703`（创建任务）、`4816`（接管任务）、`5515`（重新打开任务）
- `web/src/codex_accounts.ts:86`
- `web/src/sync_settings.ts:205`
- `web/src/task_agents.ts:747`

### F03 知识库树的 ARIA 结构不合法（High）

复现：打开 `#knowledge`，检查 `.knowledge-tree`。

实际：

```html
<nav class="knowledge-tree" role="tree">
  <div class="knowledge-tree-node">
    <div class="knowledge-tree-row">
      <button class="knowledge-tree-toggle" aria-expanded="true">…</button>
      <button class="knowledge-tree-document">…</button>
    </div>
  </div>
</nav>
```

没有 `treeitem`/`group`，也没有方向键导航模型；axe 报 `aria-required-children` critical（“Element has children which are not allowed: button[tabindex]”）以及 `aria-allowed-role`（role tree 不适用于 nav）。

期望：两种可接受方案二选一：

- 简化：去掉 `role="tree"`，保持导航 + 展开按钮 + `aria-expanded`，让读屏按普通按钮/导航理解；
- 完整树：`tree`/`treeitem`/`group` 齐全，并实现 ↑↓←→/Home/End。

影响：读屏会把一棵“假树”当作可树形导航，但无法按树模型移动；这是知识库主页面，影响面大于局部按钮。

证据：5 个断点均出现，每页 1 个 `.knowledge-tree` 节点。

### F04 Provider 空值保存静默失败（High）

复现：

1. `#models` → “添加 Provider”。
2. 清空“名称”和“API Base URL（OpenAI 风格）”。
3. 点击“保存 Provider”。

实际：对话框保持打开；`#provider-dialog` 内无 `.form-error`/`[role=alert]`；`.banner` 不存在；焦点仍在 `#save-provider`。源码 `main.ts:6843` 调用 `setMessage("error", "请填写 Provider 名称与 API Base URL")`，但对话框打开期间没有触发 `render()`，banner 不会出现在当前视图。

期望：在对话框内插入 `role="alert"` 错误，并给名称/URL 加 `aria-invalid` 与 `aria-describedby`；或改为 form + 原生 required 校验。

影响：用户无法得知保存失败原因，是明确的表单可用性缺陷。

证据：运行时 `{open:true, errorAnywhere:false, banner:null}`；关闭对话框后也没有出现该 banner。

### F05 整个 `#app` 是 polite live region（High）

复现：查看 `web/index.html`；或在任意页面切换视图时观察 DOM 变化。

实际：`<div id="app" aria-live="polite">` 包住整个应用；`render()` 通过替换 `#app.innerHTML` 或大段子树更新。运行时 live region 文本为整页导航+内容；命令面板另有 `role="status"` 计数。`main.ts` 中没有 `document.title` 赋值，所有视图标题都是 `AHA2`。

期望：

- 根节点不再使用 `aria-live`；
- 加载态用 `role="status"`，新消息/审批用局部 `aria-live="polite"`，错误用 `role="alert"`；
- 切换视图时更新 `document.title`，并把焦点移到 h1 或提供跳过链接。

影响：读屏用户会遇到两类问题：整页级重复播报，以及真正需要播报的局部更新（新消息、错误）与整页文本混在一起；视图切换也没有标题或焦点提示。

证据：`web/index.html:14`；运行时 liveRegions 第一项文本为整页内容；`grep document.title web/src/main.ts` 无结果。

### F06 命令面板的 listbox 语义与焦点循环（Medium）

复现：Ctrl+K 打开命令面板，运行 axe；连续按 Tab。

实际：

```html
<div id="command-palette-listbox" role="listbox">
  <section role="group">
    <h3>页面</h3>
    <div role="option">…</div>
  </section>
</div>
```

axe 报 `aria-required-children` critical：“Element has children which are not allowed: h3[tabindex]”。Tab 序列为 输入框 → 关闭按钮 → listbox → BODY（4/15 步），再回到输入框；BODY 步骤没有可见焦点。

期望：group 的标题移出或使用 `aria-labelledby` 放到 group 上，group 内只保留 option；或改用 `role="list"` + 按钮。焦点应显式在对话框内循环，避免 BODY 停靠。

影响：读屏对搜索结果的“列表/选项”播报不可靠；键盘用户会短暂丢失焦点位置。

### F07 错误提示与字段未关联，banner 无角色（Medium）

复现：让任务创建接口返回 400，观察 `.form-error` 与出错输入框。

实际：`.form-error` 有 `role="alert"`（这部分正确），但输入框没有 `aria-invalid`/`aria-describedby`；全局 `.banner error` 只有样式类，没有 `role`/`aria-live`。Provider 的手动校验甚至不会生成 inline error（见 F04）。

期望：出错字段 `aria-invalid="true"` 并指向错误文本；banner 使用 `role="alert"`（或删除 banner 依赖，统一用对话框内错误）。

影响：读屏用户听到错误文本，但不知道对应哪个字段；banner 是否被播报取决于根节点 live region 的实现细节。

证据：任务表单 400 后 `describedBy=null`、`titleAriaInvalid=null`；`banner()` 位于 `main.ts:3800`，输出无 role。

### F08 标题层级跳级（Medium）

复现：任意页面查看 h1/h2/h3 序列。

实际：任务列表 h1 → 任务卡 h3；任务详情 h1 → “Task 工具” h3；权限面板 h1 → “权限申请” h3；宠物总览 h1 → “最近动态” h3。

期望：h1→h2→h3 连续；卡片标题可以是 h2，或页面增加 h2 分区。

影响：读屏标题大纲不连续，依赖标题跳转的用户会以为漏了内容。

证据：axe `heading-order` 共 20 个节点（4 个场景×5 断点）。

### F09 触控目标小于 24×24（Medium）

复现：390×844 或 360×800 下测量以下控件：

| 控件 | 选择器 | 实测 |
| --- | --- | --- |
| 消息复制 | `.message-copy` | 20×20 |
| 便签色块 | `.sidebar-memo-swatch` | 18×18（桌面/平板） |
| 后端文档链接 | `.backend-doc-link` | 26×13 |
| 宠物折叠标题 | `.pet-overview-follows summary` | 310×19 |
| 便签预览 | `.sidebar-memo-preview` | 120×22 |

期望：至少 24×24；复制、换色这类高频操作移动端建议 44×44。

补充：任务详情工具图标为 30×30、对话框关闭为 36×36、基础按钮高 36，均满足 WCAG 2.2 AA 的 24px 门槛，但低于 44px 推荐值；不作为硬失败，仅在后续视觉批次中一并评估。

### F10 小字号文本对比度不足（Medium）

复现：axe `color-contrast`；或读取计算样式：

| 选择器 | 前景/背景 | 比率 | 字号 |
| --- | --- | --- | --- |
| `.sidebar-memo-preview-text` | #7f8996 / #20242a | 4.39 | 11px |
| `#add-sidebar-memo-item` | #7f8996 / #272c33 | 3.96 | 10px |
| `#sidebar-memo-status` | #7f8996 / #272c33 | 3.96 | 9px |
| `.knowledge-status` | #64748b / #f1f5f9 | 4.34 | 9px bold |

60 次运行合计命中 111 个节点（页面/断点重复计数），去重后为以上 4 类。

期望：≥4.5:1；或增大字号/字重。

影响：暗色侧栏里的便签提示与知识库状态标签在低视力用户下可读性差。

### F11 桌面端没有跳到主内容（Medium）

复现：1280×800 打开任意页面，从地址栏开始连续 Tab。

实际：前 16 个焦点依次为全局搜索、项目/任务/渠道/知识库/模型/资源/宠物导航、便签折叠、5 个色块、便签完成复选框、便签预览；主内容仍未到达。DOM 中没有 skip link。

期望：页首提供“跳到主内容”链接，或让主内容 h1 成为可聚焦目标。

影响：键盘用户每次切页都要穿过约 16 个侧栏控件。

## 5. 明确“不修”项与原因

以下项目在体检中确认现状可用或属于有意设计，本轮不建议改动：

1. **横向溢出**：60 个页面×断点、844×390、740×360、1024×600、640×400（200% 等价）均为 0；无需为溢出改布局。
2. **固定底栏/输入框遮挡**：任务详情输入区聚焦时 bottom=836/844，底部导航按设计隐藏；未聚焦时输入区 bottom=781、导航 top=782。移动端安全区由 `env(safe-area-inset-*)` 覆盖（对话框头部/底部、命令面板、底部导航），无需调整。
3. **对话框 Esc、背景 inert、焦点返回**：原生 `<dialog>.showModal()` 生效；Esc 关闭；对话框打开时程序化聚焦背景 `#logout` 会被阻止；任务创建与邮件对话框 Esc 后焦点回到触发按钮。除 F13/F14 外不改。
4. **重复提交**：`bindForm` 的 `dataset.submitting` + `aria-busy` + 按钮禁用有效；连续两次提交只发出 1 次请求。
5. **任务列表实时刷新**：现有浏览器测试通过；刷新不改变滚动位置、不抢焦点、不关闭筛选浮层，详情打开时暂停列表刷新并做一次追赶刷新。阶段 4 的列表 patch 结论仍然成立，本轮不重新设计。
6. **图片尺寸/懒加载**：会话与 Markdown 图片已有 `loading="lazy"`；装饰图无 `width/height` 属性但 CSS 已预留尺寸，CLS 为 0，不改。
7. **侧栏/工具面板拖拽条 8–9px**：鼠标拖拽目标偏窄，但支持键盘（←/→/Home）且有 `role="separator"` 与 aria-valuenow；扩大宽度会改变既有布局，列为观察项而非缺陷。
8. **模型搜索框 readonly 直到聚焦**：这是防密码管理器/自动填充污染搜索的实现，聚焦后即恢复输入，不按表单缺陷处理。
9. **30–36px 图标按钮**：满足 WCAG 2.2 AA 的 24px 最小目标；若后续按 44px 移动端规范统一调整，属于视觉批次，不与本轮 High 项混做。
10. **原生 required 英文提示**：headless Chromium 的 UI locale 产物，不是产品文案问题；真实浏览器跟随用户语言。
11. **登录页、桌面端/移动端原生外壳、非高频页面**：不在本次范围。

## 6. 按严重度排序的前 10 项

| 排名 | ID | 严重度 | 问题 | 一句话影响 |
| --- | --- | --- | --- | --- |
| 1 | F01 | High | 任务卡片键盘不可打开 | 键盘用户无法在列表中进入任务 |
| 2 | F02 | High | 12 个对话框关闭按钮无名称 | 读屏只听到“按钮”，初始焦点即落在其上 |
| 3 | F03 | High | 知识库树 ARIA 结构错误 | 读屏树导航语义失效 |
| 4 | F04 | High | Provider 空值保存无提示 | 用户被静默卡住，不知道缺什么 |
| 5 | F05 | High | 整个 `#app` 为 polite live region | 读屏整页播报，重要更新被淹没 |
| 6 | F06 | Medium | 命令面板 listbox 语义 + BODY 焦点停靠 | 搜索结果播报不可靠，焦点短暂丢失 |
| 7 | F07 | Medium | 错误未关联字段、banner 无角色 | 读屏不知道哪个字段失败 |
| 8 | F08 | Medium | h1 直接跳 h3 | 标题大纲断裂 |
| 9 | F09 | Medium | 多处触控目标 <24×24 | 触屏误触复制、换色等操作 |
| 10 | F10 | Medium | 小字号对比度 3.96–4.39 | 暗色侧栏与知识库状态难读 |

## 7. 建议修复批次

每批都可以独立提交、独立验收；建议按 1→2→4→5→3→6→7 的顺序排期，前四批风险最低、收益最高。

### 批次 1：任务列表键盘可达（F01）

- 把卡片主体改为链接/按钮（动作按钮保持独立，不嵌套交互元素），或增加显式“打开”按钮。
- 验收：Tab 可逐卡聚焦；Enter/Space 打开详情；编辑/删除按钮仍独立可用；现有任务列表与实时刷新测试通过；axe 不新增违规。

### 批次 2：可访问名称与列表语义（F02、F15）

- 12 处对话框关闭按钮补 `aria-label="关闭"`；MCP 行去掉不合适的 `role="listitem"`（或改为 div）。
- 验收：对话框 axe `button-name` 为 0；MCP `aria-allowed-role` 为 0；关闭按钮被读屏读作“关闭”。

### 批次 3：知识库树与命令面板 ARIA（F03、F06、F14）

- 知识库二选一：降级为普通导航+展开按钮，或完整实现 tree/treeitem + 方向键。
- 命令面板把 group 标题移出 option 子节点，或改 list 语义；为对话框补显式焦点循环，去掉 BODY 停靠。
- 验收：知识库与命令面板的 `aria-required-children`/`aria-allowed-role` 为 0；Tab 循环不离开对话框；方向键/Enter 行为与现有交互一致；相关浏览器测试通过。

### 批次 4：live region 与视图切换（F05、F07 的 banner 部分）

- 移除 `#app` 的 `aria-live`；加载、消息、审批、错误分别使用局部 live region/alert。
- 视图切换更新 `document.title`，并把焦点移到页面 h1（或提供 skip link 作为等价手段）。
- 验收：根节点不再整页播报；错误通过 `role="alert"` 可播报；切换 12 个视图时标题变化；现有 axe 不新增问题。

### 批次 5：表单反馈（F04、F07 字段关联、F12）

- Provider 对话框改为 form（或补 submit 处理），空值时在对话框内插入 `role="alert"`，字段加 `aria-invalid`/`aria-describedby`。
- 任务创建等使用 `bindForm` 的对话框，在 catch 中把错误文本与对应字段关联（至少给标题/目标/名称/URL）。
- 验收：空值保存有可见提示且 Enter 可提交；双击仍只发 1 次请求；读屏能读出错误与字段关系。

### 批次 6：视觉可读性与触控（F08、F09、F10）

- 修正标题层级（h1→h2→h3），或把卡片标题降级。
- 把 <24px 的复制、色块、文档链接、summary、便签预览提升到至少 24×24；移动端高频操作优先 44×44。
- 调整侧栏便签提示与知识库状态标签的颜色/字号，达到 4.5:1。
- 验收：axe `heading-order` 与指定选择器的 `color-contrast` 为 0；触控目标实测 ≥24；桌面/移动布局无回归。

### 批次 7：一致性与低风险打磨（F11、F13、F16、F17）

- 增加“跳到主内容”skip link。
- 统一对话框遮罩关闭策略（任务/Provider 与宠物工作台一致）。
- 减少动效模式下取消宠物思考动画的 `!important` 覆盖。
- 任务详情工具面板地标：移出 main 或改为带标签的 section。
- 验收：Tab 一次即可进入主内容；三类对话框行为一致；`prefers-reduced-motion: reduce` 下无无限动画；axe landmark 规则为 0。

## 8. 复现与证据

### 8.1 构建

```bash
npm ci --prefix web
node scripts/build-web.mjs
```

浏览器加载 `web/build`，fixture 使用与 `web/tests/*.browser.mjs` 相同的 CSP 与 `index.html` 外壳，桩接 `/api/v1/*` 与 EventSource。

### 8.2 既有回归测试

```bash
AHA_PLAYWRIGHT_MODULE=<playwright>/index.mjs \
AHA_BROWSER_EXECUTABLE=<chrome-headless-shell> \
node web/tests/task_list_live_refresh.browser.mjs
```

结果：`task_list_live_refresh.browser: ok`。

### 8.3 axe 范围

对 60 个页面/断点运行 `wcag2a/wcag2aa/wcag21a/wcag21aa/wcag22aa/best-practice`；另对任务创建、Provider、命令面板、邮件账号对话框单独运行同一标签集。重点规则与命中数：

| 规则 | 影响 | 命中 |
| --- | --- | --- |
| `button-name` | critical | 任务创建、Provider 对话框各 1 |
| `aria-required-children` | critical | 知识库 5 个断点；命令面板 1 |
| `aria-allowed-role` | minor | 知识库 5；MCP 20 |
| `heading-order` | moderate | 20（4 场景×5 断点） |
| `color-contrast` | serious | 合计 111（去重 4 类选择器） |
| `landmark-complementary-is-top-level` | moderate | 10（任务详情×5、权限面板×5） |

### 8.4 交互探针关键读数

| 场景 | 读数 |
| --- | --- |
| 任务卡 | `tag=ARTICLE, role=null, tabIndex=-1, hasTabindex=false`；可聚焦子元素只有编辑/删除 |
| 任务创建对话框 | 打开后 activeElement 为无名关闭按钮；对话框 390×844 全屏；操作区 top=699 bottom=844 可见；Esc 后焦点回到“创建任务” |
| 任务创建表单 | 空 required 触发原生校验；两次 requestSubmit 仅 1 次 POST；400 时 `.form-error[role=alert]` 出现，`aria-describedby`/`aria-invalid` 为空 |
| 双击提交时 | `dataset.submitting=true`、`aria-busy=true`、提交按钮 disabled、spinner 出现 |
| Provider 对话框 | 无 `<form>`；清空值保存后 `open=true`、`errorAnywhere=false`、`banner=null` |
| 命令面板 | 初始焦点输入框；Tab 序列 input→关闭→listbox→BODY，4/15 步在 BODY；Esc 关闭 |
| 邮件账号对话框 | 390×844 下 352×775，bottom=810；关闭按钮 aria-label=关闭；9 个输入全部有关联 label；Esc 后焦点回 `#add-mail-account` |
| 任务详情输入区 | 390×844：未聚焦 bottom=781、navTop=782；聚焦后 bottom=836、nav 隐藏 |
| 交互延迟（本地 fixture） | 命令面板 7–16ms；任务详情 57–75ms（390 与 1280 各 5 次） |
| 首屏性能（本地 fixture） | LCP 120–300ms、CLS 0、启动期 1 个 138–152ms 长任务 |

## 9. 建议下一步

1. **先做批次 1、2、4、5**：这四批改动小、收益直接，覆盖键盘打开任务、对话框名称、live region 与表单反馈；可分别提交并独立验收。
2. **在真实环境复测性能**：本轮 LCP/长任务来自本地 fixture，不能代表真实网络与真实后端；建议在真实设备上以生产构建复测 LCP、INP、首屏长任务与 SSE 刷新，再决定是否需要进一步优化。
3. **补一次读屏手工验证**：用 NVDA/VoiceOver 走任务列表→详情→审批中心→知识库，重点验证 F05 修复后的局部播报、F02 的关闭按钮名称、F03 的树导航。
4. **把探针沉淀为可回归测试**：将 axe 标签集、60 个页面/断点矩阵与对话框交互探针整理进 `web/tests/`，避免后续页面改动重新引入同类问题；本轮按约束没有把临时 fixture 入库。
5. **批次 3、6、7 排期处理**：知识库树语义与命令面板可单独排期；视觉与触控批次建议与下一次样式调整合并，减少布局回归面。

---

本报告只记录问题，未修改任何源码；临时探针、截图与 fixture 均位于 Agent scratch，已在任务结束前清理。
