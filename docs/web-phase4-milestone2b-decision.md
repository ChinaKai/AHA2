# Web 阶段 4 里程碑 2B：测量与结论（task-401）

结论：**不实施列表虚拟化，也不新增第二套 SSE 列表更新机制**。任务列表与审批列表在实测规模下滚动稳定（61fps，0 个 >33ms 帧），现有的“按行签名增量更新 + 350ms/1000ms 合并调度”已经够用。测量过程中发现并修复了两个会阻断现有增量更新链路的缺陷（缺失 import、标题编辑被刷新打断），属于最小正确性修复，不是 2B 优化本身。

## 1. 判定阈值

沿用 2A 文档（`docs/web-conversation-virtual-list.md`）的约 500 行门槛，并把它写成可判定的条件：

| 决策 | 触发条件（全部满足才做） |
| --- | --- |
| 列表虚拟化 | 行数 > 500；且滚动 p95 帧 > 33ms，或出现 >50ms 帧，或滚动时出现可感知的长任务 |
| 重写 SSE 列表更新 | 单次列表刷新 > 50ms，或每次刷新产生 >50ms 长任务，或刷新会打断滚动/输入/详情 |
| 保持现状 | 未达到上述条件 |

## 2. 环境与复现

| 项 | 值 |
| --- | --- |
| 基线提交 | `dcbacbb`（main），工作区另含本文第 5 节的最小修复 |
| 构建 | `npm --prefix web run build`（esbuild 0.28.2，`web/build` 未压缩 ES modules） |
| 浏览器 | Chrome for Testing 158.0.8127.18（`chromium_headless_shell-1254`，headless） |
| Node | v24.21.0 |
| 视口 | 1280×800，无缩放 |
| 数据 | 内存生成的 fetch/SSE fixture，不落盘、不入库 |

复现命令（在仓库根目录执行）：

```sh
npm --prefix web run build
AHA_PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs \
AHA_BROWSER_EXECUTABLE=/path/to/chrome-headless-shell \
AHA_MEASURE_OUTPUT=/tmp/list-refresh-result.json \
  node web/tests/task_list_perf_measure.mjs
# 或只跑一个场景：
AHA_SCENARIO=tasks_500 ... node web/tests/task_list_perf_measure.mjs
```

`web/tests/task_list_perf_measure.mjs` 会启动静态服务器，在真实构建产物前注入 fixture：任务接口按 `limit/cursor` 分页并返回服务端聚合计数，`EventSource` 可脚本触发全局 `task_*`/`pet_approval` 事件，任务详情、审批工作台、项目/Workspace 等接口都有对应响应。测量项：

- 首屏：`window.__ahaPerf.firstScreenMs`（从模块启动到首屏渲染完成）。
- 滚动：1.5s 内每帧 `requestAnimationFrame` 间隔，`window.scrollBy(0, 40)`，统计 fps、p95、>33ms/>50ms 帧数。
- 点击：从 `element.click()` 到 `.task-screen` 出现（任务）/审批详情对话框出现。
- 内存：CDP `HeapProfiler.collectGarbage` 后 `Runtime.getHeapUsage`。
- SSE：注入事件后读取 `__ahaPerf.listRefreshRequests`、`listRefreshTotalMs` 与 fixture 的请求日志。
- 长任务：`PerformanceObserver({entryTypes: ["longtask"]})`。

## 3. 任务列表

fixture：500 条任务，状态按 `active/active/active/active/waiting_user/active/failed/active/active/blocked` 循环，默认筛选（未完成/异常、本机）下全部可见；首屏 50 行，每次“加载更多”+50 行。

| 指标 | 200 行 | 500 行 |
| --- | --- | --- |
| 首屏行数 / 首屏耗时 | 50 / 612ms | 50 / 616ms |
| 加载全部后 DOM 节点 | 8,516 | 20,391 |
| 滚动高度 | 18,175px | 45,175px |
| 滚动 fps / p95 / >33ms / >50ms | 61 / 16.8ms / 0 / 0 | 61 / 16.8ms / 0 / 0 |
| 点击进入详情 | 34.1ms | 47.5ms |
| 详情 DOM 节点 | 620 | 620 |
| 强制 GC 后堆内存 | 4.2MB | 5.1MB |
| 加载更多：50→200 行（全量重绘） | 95→259ms | 96→265ms |
| 加载更多：450→500 行（全量重绘） | — | 1,143ms |
| 场景累计 longtask | 7 | 28（全部来自加载更多的全量重绘） |

“加载更多”走的是整页 `render()`，它随已加载行数线性变慢；这是用户主动越界加载时的一次性成本，不影响滚动与 SSE 刷新。滚动路径在所有规模下都是 61fps，没有掉帧。

### SSE 更新（任务列表）

| 场景 | 200 行 | 500 行 |
| --- | --- | --- |
| 20 个 `task_status` / 2s：实际列表刷新 | 2 次（10:1 合并） | 2 次 |
| 单次刷新耗时 | 19ms | 22ms |
| 刷新期间 longtask | 0 | 0 |
| 刷新前后滚动位置 | 1200 → 1200 | 1200 → 1200 |
| 每 2s 一个事件、持续 10s：刷新次数 | 5 | 5 |
| 详情打开时 20 事件 / 2s：列表刷新 | 0 次（挂起） | 0 次 |
| 详情内输入框焦点/内容 | 保持 | 保持 |
| 关闭详情后的补刷 | 1 次（64ms） | 1 次（66ms） |

结论：合并调度按设计工作（350ms 去抖、1000ms 上限；2s 间隔下每个事件各触发一次刷新），增量 patch 在 200 行只需 19ms，远低于 50ms 阈值。500 行时刷新会收敛到 200 行（见 6.1）。

## 4. 审批列表

fixture：190 / 500 条 pending 审批。默认每页 10 条，点“加载更多”+10；工作台硬上限 200 条。

| 指标 | 190 条 | 500 条 |
| --- | --- | --- |
| 首屏行数 / 首屏耗时 | 10 / 85ms | 10 / 77ms |
| 加载到上限后行数 | 190 | 200（显示“审批仅加载最近 200 条。”） |
| 加载更多：10→20 行（全量重绘） | 1.4ms | 1.5ms |
| 加载更多：最后一页（全量重绘） | 4.9ms | 6.9ms |
| 切走再切回审批页（全量重绘） | 5.2ms | 6.4ms |
| 滚动 fps / p95 / >33ms | 61 / 16.9ms / 0 | 61 / 16.9ms / 0 |
| 点击审批打开详情 | 7.3ms | 7.3ms |
| DOM 节点 | 2,308 | 2,409 |
| 强制 GC 后堆内存 | 3.6MB | 3.7MB |

### `pet_approval` SSE

10 个事件 / 1s：

| 指标 | 190 条 | 500 条 |
| --- | --- | --- |
| pending 计数请求 | 10 | 10 |
| 工作台列表请求 | 0 | 0 |
| 列表行数变化 | 190 → 190 | 200 → 200 |
| 新增一条审批后是否出现在列表 | 否（徽标 191 已更新） | 否（徽标 501 已更新） |

审批列表当前只随事件刷新 pending 徽标，不重载列表。重绘成本只有 5–7ms，所以这不是性能问题，而是产品行为；是否实时插入留给后续项（6.2）。

## 5. 本次实施的最小修复

测量暴露的不是“需要更快的渲染”，而是现有增量链路有两处断点。修复都限定在任务列表内，不改会话虚拟列表、Round 卡片、任务详情活动、后端接口：

1. **`replaceInteractiveRegion` 漏 import**（`web/src/main.ts`）。task-368 在 `patchTaskList`/`patchTaskHiddenHint` 中开始调用它，但没有加入 `./ui_helpers.js` 的 import 列表。于是只要筛选计数发生变化（例如某个任务完成），`patchTaskList` 就在替换筛选区时抛 `ReferenceError`，被 `performListRefresh` 的 `catch` 吞掉：`state.tasks` 已经更新，DOM 却停在上一次渲染。实测 500 行时 20 事件后 `state=200 / DOM=500`，且筛选计数不更新。补上 import 后 patch 恢复正常（`patchTaskList()` 返回 true）。
2. **标题编辑器被刷新打断 / 无法退出**（`web/src/main.ts`）：
   - 刷新时若行内 `.task-title-input` 正在编辑，`patchTaskList` 会替换该行，输入值与焦点一起丢失；`render()` 又因 `hasActiveTextEditor()` 拒绝重绘，焦点也无法恢复。现在 `performListRefresh` 在文本编辑器聚焦时挂起 patch（与详情打开、指针按下同一处理），编辑器关闭后由 `finish()` 的 `loadAll()+render()` 落到新状态。
   - `finish()` 在重绘前先 `input.blur()`。此前按 Enter/Escape 时输入框仍持有焦点，`render()` 一直命中 `hasActiveTextEditor()` 分支、只标记 `renderPending` 就返回，编辑器卡在原地无法关闭。

浏览器回归测试：`web/tests/task_list_live_refresh.browser.mjs`（真实构建产物 + fixture）断言：

- 改变会导致筛选计数变化的状态后：changed row 离开/更新，其余行与顺序不变，筛选弹层保持展开，计数更新，滚动位置不变，点击仍能打开详情；
- 标题编辑中输入并触发刷新：输入框仍在、焦点与已输入值保持、滚动不动；Escape 后恢复服务端标题；
- 详情打开时全局 SSE 不触发列表刷新，输入框焦点/内容不被抢；关闭详情后恰好补刷一次并应用挂起的变化；
- 无 page error、无横向溢出。

该测试在修复前会超时（`task-045` 行不消失），修复后通过。

## 6. 后续项（不在 2B 实施）

1. **已加载超过 200 行时，实时刷新会把列表收敛回 200 行**。服务端单页上限是 200，`taskRefreshLimit()` 也封顶 200；用户用“加载更多”把列表拉到 500 行后，任意 `task_*` 事件都会让 `state.tasks` 与 DOM 回到 200 行（实测 `rows 500 → 200`，`scrollY 1200 → 1200` 但可视内容跳动）。这是本次测量才发现的行为，修复前被第 5.1 节的异常掩盖（DOM 停留旧值，下一次全量 render 仍会收敛）。建议二选一：把任务“加载更多”也限制在 200 行（与审批列表一致），或刷新时按已加载页数分页回读。
2. **审批列表不随 `pet_approval` 原位插入新审批**。目前只刷新 pending 徽标；如果产品希望“审批到达即出现”，直接重载工作台审批页即可（实测全量重绘 5–7ms），不需要增量机制。
3. **从任务详情返回列表会把滚动归零**（`scroll_before_detail 800 → 0`）。这是详情关闭时整页 `render()` 的既有行为，属于任务详情交互，本次按要求未改。
4. **加载更多的整页重绘在 500 行时约 1.1s**。只在用户主动越过 200 行服务端上限时出现，滚动与 SSE 刷新均不受影响；若后续决定支持 >200 行常驻，可再评估对加载更多做分区更新或虚拟化。

## 7. 验证结果

| 命令 | 结果 |
| --- | --- |
| `npm --prefix web test` | 424 passed / 0 failed |
| `npm --prefix web run build` | 成功；`app.4bffa90a6754.js`、`styles.694a3975c75f.css`，15 个路由 chunk |
| `node web/tests/task_list_live_refresh.browser.mjs` | 通过（修复前同一测试超时） |
| `node web/tests/task_card_mobile.browser.mjs` | 通过 |
| `node web/tests/task_readonly_filter.browser.mjs` | 通过 |
| `node web/tests/completed_task.browser.mjs` | 未运行：该文件硬编码 `.tools/web-validation`，本环境没有该目录 |

“加载更多”计时包含点击后同步 `render()` 与异步数据到达后的重绘，因此表中首列是同步重绘耗时，末列是到新行出现的总耗时。首屏耗时包含完整启动链路（鉴权、设置、项目/Workspace/任务并发加载），不是 2B 的优化目标。