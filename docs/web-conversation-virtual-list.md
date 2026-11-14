# 会话虚拟列表（阶段 4 里程碑 2A）

测量时间：2026-11-13。所有数字来自本机 Chromium（`chromium-headless-shell-1254`，1280x800）驱动真实 `web/build` 模块的 fixture，而不是生产数据库导出。fixture 按现有 API 响应结构构造，覆盖纯文本、工具输出、折叠长文与图片消息。

## 一、先量规模

| 列表 | fixture | 当前 DOM 行数 | 行高 | 渲染/打开耗时 | 决策 |
| --- | --- | --- | --- | --- | --- |
| 任务列表 | 200 个 Task（默认筛选） | 首屏 33 行，全部加载 133 行 | 2 种：98px / 74px | 列表刷新 4ms | 保留普通 DOM |
| 审批列表 | 190 条 | 10 行（首屏）/ 190 行（全部） | 固定 56px | 随列表渲染，无独立耗时 | 保留普通 DOM |
| 关注/忽略 | 500 条（200 关注 + 300 忽略） | 500 行 | 固定 54px，默认折叠 | 可接受 | 保留普通 DOM |
| 会话 | 单个 Task 1000 条消息 | 1000 个消息节点 | 56–102px（3 种以上，随内容变化） | `openTask` 1099ms，滚动高度 79,819px | **虚拟化** |

任务/审批/关注列表都受服务端 200 行分页上限约束，实际加载量远低于约 500 行的 DOM 阈值；它们的行高固定或只有两种，保留普通 DOM 更简单。阈值与测量依据写在这几处代码注释里：

- `web/src/main.ts` 的 `maxListPageSize` 注释（任务/审批 200 行上限、33/133/190 行实测）。
- `web/src/pet_workbench.ts` 的 `PET_APPROVAL_MAX_LIMIT` 注释（10/190 行、56px）。
- `web/src/pet_workbench.ts` 的 `renderPetOverview` 关注列表注释（500 行、54px、默认折叠）。
- `web/src/main.ts` 的 `initialConversationPageSize` 注释（1000 条会话、79,819px、1099ms）。

## 二、会话虚拟列表

实现位于 `web/src/conversation_ui.ts`（纯窗口计算与渲染）和 `web/src/main.ts`（测量、锚点与生命周期控制器）。

- **窗口**：`conversationWindow` 按消息 id 建立测量高度缓存与前缀和；未测量行用 80px 估算。视口上下各 600px overscan，上下用 `aria-hidden` spacer 补齐总高度。渲染行数与视口高度有关，1000 条 fixture 下实测 15 行。
- **可变高度**：`measureConversationMessages` 把 margin 折进行高；`ResizeObserver` 在展开/折叠、图片加载、工具卡输出变化后重测并重算 spacer。展开状态变化不会重建整份 HTML。
- **滚动锚点**：每次 patch 前记录“第一个可见消息 id + 相对视口偏移”；patch 后按节点位置反推 `scrollTop`。向上加载历史、插入实时消息、spacer 重算都走同一条恢复路径。若滚动条落在 spacer 中没有可见行，则按前缀和反查虚拟行作为锚点。
- **贴底策略**：只有用户已在底部（80px 内）且没有进行中的手势时才自动贴底。上滚后新消息只累加 `conversationUnreadMessages`，在列表右下角显示“有新消息（N）”；点击提示回到最新消息并恢复跟随。回到底部也会清除提示。
- **节点回收后的状态**：按消息 id 保存展开状态与 `<details>.open`；`patchConversationList` 复用 keyed 节点，并在新节点上恢复已保存状态。图片预览、复制、文本选择依赖的 DOM 节点在窗口内保持复用。
- **键盘与焦点**：聚焦中的消息行通过 `pinnedKey` 留在窗口内；`ui_helpers.ts` 的 region key 对消息内部控件使用“消息 id + 消息内路径”，避免回收后把焦点交给恰好占据同一位置的另一行。窗口跨度最多 240 行，防止焦点把 DOM 拉回上千行。
- **SSE/增量更新不变**：`refreshTaskRuntime`、去重、300 行运行期上限和 `updateTaskLiveRegions` 的数据流不改；虚拟列表只改变 `renderConversationList` 渲染哪些行，实时 patch 仍按 keyed 节点做增量更新。
- **能力保留**：加载更早记录按钮、空状态、过滤后的 key 列表（`conversationItemKeys` 与渲染共用 `conversationRenderableItems`）、图片对话框、工具卡、复制按钮都沿用原组件。

## 三、改造后实测

同一 1000 条消息 fixture：

| 指标 | 改造前 | 改造后 |
| --- | --- | --- |
| DOM 消息节点 | 1000 | 15（滚动中 24–26） |
| 滚动高度 | 79,819px | 80,076px（估算误差约 0.3%） |
| `openTask` | 1099ms | 36ms |
| 上滚 1600px 后页面锚点 | — | 实时追加前后 id 与偏移均不变（`ci-978`, -26.5px → -26.5px） |
| 加载更早 50 条 | — | 锚点 `ci-404`, -15.41px → -15.72px |
| 回到底部 | — | 恢复自动跟随，提示消失 |

`web/tests/conversation_virtual.browser.mjs` 覆盖：DOM 上限、消息顺序、展开/工具卡/图片/复制在回收前后的恢复、上滚后实时追加不抢滚动、提示出现与点击回底、回底后继续跟随、焦点跨 patch 保持在同一消息行、向上加载历史锚点稳定、无 page error。

## 四、验证

```text
npm --prefix web run build                         通过
npm --prefix web test                              422 pass / 0 fail
node web/tests/conversation_virtual.browser.mjs    通过（15/24/26 行，锚点误差 ≤2px）
conversation_wheel_follow.browser.mjs              通过（滚动后仍跟随，start 2415 → after 2702/2702）
首屏 JS gzip                                       199,735 B
首屏 CSS gzip                                      46,872 B
首屏 JS+CSS gzip                                   246,607 B（预算 256,000 B）
```

## 五、验证缺口

- 本机没有 `.tools/web-validation`；browser 测试使用 scratch 中安装的 Playwright 1.65.0 与 chromium-headless-shell，并通过 `AHA_PLAYWRIGHT_MODULE` / `AHA_BROWSER_EXECUTABLE` 指向。仓库内新增的 browser 测试已按该环境变量回退方式编写。
- 既有的 `conversation_wheel_follow.browser.mjs` 硬编码 `.tools/web-validation`，本次用 scratch 副本运行；副本只替换了 Playwright/浏览器路径和截图目录，断言未改。
- 规模数据来自高保真 fixture，不是生产数据导出；上线前可在真实 owner 数据上复核一次 `openTask` 与最大消息数。
- 会话内没有独立的“搜索跳转”控件；本次覆盖的是复制、图片、工具卡、展开状态和过滤后的 key 列表。若后续加入会话内搜索，跳转到窗口外消息时需要先设置滚动位置再渲染，可直接复用锚点恢复逻辑。