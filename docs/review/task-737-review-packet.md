# task-737 评审包：内置浏览器 Web 端 Owner 控制

- 任务：task-737「内置浏览器 Web 端控制：owner 可在任务面板点击/输入/滚动」
- 基线：`a696f84`（main）
- 分支：`aha/task_5c2b60c8a9061790a345`
- 工作区：`/srv/build/users/kaikai/kk-workspace/.aha2-worktrees/task_5c2b60c8a9061790a345`
- 状态：未合并、未 push、未部署、未运行安装器；未登录真实账号，未读写任何凭据。
- 实现提交：`52913ac`
- r2 修复提交（task-741 L2 复审）：`e3f1336`
- 评审包提交：`b404743`（r1）；r2 更新随后单独提交

## 1. 只读核对结论（第一步）

### 1.1 现有 actions 端点能否直接承接 Owner Web 输入

不能直接调用，但服务层可以复用。

- Agent 侧已有 `POST /api/v1/agent/browser/grants/{grant}/tabs/{tab}/actions`，
  使用 Bearer capability（`withAgentCapability`），绑定 Task/Turn；Web 面板的
  登录态是 Session + CSRF，不能也不该拿 Agent token。
- 真正的执行链在 `internal/browser`：
  - `Service.Act` → `authorize`（`internal/browser/service.go`）检查 grant 状态、
    `interact` 能力、session 未关闭且仍是该 Profile 的 active session、Profile
    独占 lease 仍由该 grant 持有、tab 属于该 session。
  - `withOperationHold`（`service.go`）用独立 `hold_id` 在 DB 里临时续期 lease。
  - `Host.Action` → host `POST /v1/profiles/{profile}/tabs/{tab}/actions`，带
    `<leaseID>|<operationDeadline>` fence。
  - host `runMutation`（`browser-host/server.mjs`）拿 session gate 读槽、校验 fence、
    建立 operation hold；真正输入在 `session.inputSlot` 串行执行，slot 内再次校验
    fence，因此 Agent 和 Owner 的输入不会交错成半个手势。
  - 审计在 `Service.Act` 返回后统一写入：actor、kind、target、result；不写
    text/keys。frame 读取不审计。
- 结论：新增一条 **Session 认证** 的 Owner actions 路由，调用同一个服务层即可；
  不需要新开绕过 fence/lease/审计的通道。

### 1.2 frame 尺寸、CSS 缩放、DPR 与页面 viewport

- host 用 `chromium.launchPersistentContext(..., {viewport: null})` 启动真实 Edge
  窗口；`page.screenshot()` 截取的是**页面 viewport**，不是整页。
- 截图像素尺寸可能包含 `deviceScaleFactor`，与 CSS `window.innerWidth/innerHeight`
  不一定相等；`viewport: null` 时窗口缩放还会改变两者。
- Web 面板 CSS 是 `object-fit: contain` + `width/height:100%`，所以图片在面板里
  可能上下或左右留白（letterbox）。
- 现有 panel 只显示 `<img>`，没有任何坐标信息，直接按元素比例换算会在 DPR≠1
  或窗口变化时落到错误位置。
- 本次实现让 host 在同一次截图时读取 CSS viewport，并通过
  `X-AHA2-Viewport-Width/Height` 响应头返回；AHA 透传到 `/frame` 响应。
  Web 每次指针事件用 `getBoundingClientRect()`、`naturalWidth/naturalHeight` 和
  该 frame 的 CSS viewport 计算内容区，再做比例映射。DPR 在比例里抵消，留白被
  显式排除；移动端使用同一套 CSS client 坐标，不需要单独处理 `devicePixelRatio`。
  host 截图同时使用 `scale:"css"`，viewport 探测有 1.5s 上限；头缺失或无效时
  面板拒绝 click/drag/scroll/hover，不用图片像素兜底。
- tab 切换/lease 变化时清空 frame viewport 和待发输入；窗口缩放后的旧 frame 可能
  短暂用旧 viewport，host 的 `checkViewport` 会以 `browser_action_invalid` 拒绝越界
  坐标，面板提示刷新，不会静默错点。

### 1.3 共享控制里可复用 / 不应照搬的机制

可复用：

- 前台共享控制的“显式控制模式 + 状态提示 + 焦点处理”交互；
- 点击判别（单击/双击）、滚轮累积、移动端长按右键的输入整形；
- 串行输入、幂等/action_id 的思路（本任务只复用串行，见 §3.5 的取舍）；
- 稳定错误码到可读提示的映射；
- 审计只记元数据、不记输入内容；
- 页面隐藏时暂停/清空、SQLite lease hold、fence。

不照搬：

- desktop 的 observation revision / 观察过期机制：浏览器 frame 是只读截图流，
  host 已有自己的 frame 缓存代际与 tab 绑定，不需要第二套观察令牌；
- desktop 的 owner/agent controller 仲裁：浏览器已经是 Profile 独占 lease，
  同一时刻只有一个 grant，owner 和 agent 都受它约束；
- 前台输入、绝对屏幕坐标、窗口置顶确认、虚拟指针、缩放：浏览器输入是页面
  viewport 坐标，不碰宿主鼠标/前台窗口；
- desktop 的 `move/drag` 组合键语义与本任务不同（见 §3.2）。

### 1.4 现有测试入口

- Go：`go test ./internal/browser`、`go test ./internal/httpapi`；本任务新增/
  扩展的用例在 `internal/browser/interaction_test.go`、
  `internal/httpapi/browser_interaction_handlers_test.go`、
  `internal/httpapi/browser_handlers_test.go`。
- browser-host：`node --test browser-host/*.test.mjs`（无需真实 Edge）。
- Web 单测：`cd web && npm run build && npm test`（导入 `web/build/*.js`）。
- Web 回归：`AHA_PLAYWRIGHT_MODULE=<playwright-index.mjs> node web/tests/browser_task_panel.browser.mjs`
  （Playwright headless Chromium；不等于真实 Edge）。

## 2. 改动范围

### 2.1 browser-host

- `browser-host/interact.mjs`
  - 动作集新增 `context_click`、`text`、`drag`；`normalizeAction` 对三者做
    独立字段校验（text 不接目标、drag 必须起终点、右键同 click）。
  - 键盘 token 接受单个可打印 ASCII（例如 `Control+/`）、`Insert`、`ContextMenu`。
  - `performAction`：右键 `mouse.click(..., {button:"right"})`；text 直接
    `keyboard.type` 当前焦点；drag 为 `move/down/move{steps}/up`。
- `browser-host/server.mjs`
  - frame 截图与 `window.innerWidth/innerHeight` 并行采集，缓存 frame 时一并保存；
    `sendFrame` 输出 `X-AHA2-Viewport-Width/Height`。

### 2.2 AHA Go 控制面

- `internal/browser/host.go`
  - `HostFrame` 增加 `Width/Height`；`HostAction`/`HostActionResult` 增加
    `ToX/ToY`；HTTP 客户端透传 `to_x/to_y`；`Frame` 解析并校验 viewport 头。
- `internal/browser/interaction.go`
  - `Act`（Agent）与 `ActOwner`（Owner）共用 `act`：同一 `authorize`、
    `withOperationHold`、host 调用、审计。
  - 动作集按来源分离：Agent 仍是 `click/double_click/hover/type/key/scroll`；
    Owner 额外允许 `context_click/text/drag`。Owner 仍需要 `interact` 能力。
  - 审计增加 `grant_id`、`source`（owner/agent），target 对 drag 记
    `viewport:x,y->x,y`；text/keys 不入审计。
- `internal/httpapi/browser_handlers.go`
  - 新增 `POST /api/v1/browser-grants/{id}/tabs/{tab}/actions`（`withAuth`，
    Session + CSRF），调用 `Service.ActOwner`。
  - Owner/Agent 共用 `browserActionPayload`，线格式一致。
  - `/frame` 响应写出 viewport 头。
- `internal/browser/browsertest/fake.go`：fake frame 默认 1280×800，action 回显
  `ToX/ToY`，供路由/服务测试。

### 2.3 Web

- `web/src/browser_task_panel.ts`
  - 新增控制模式、输入队列、坐标映射、键盘/IME/粘贴、滚动、点击判别、右键、
    拖拽、错误处理与生命周期清理；导出纯函数供单测：
    `browserViewportPoint`、`browserKeyCommand`、`browserTextActions`、
    `splitBrowserText`、`browserScrollStep`、`browserTextByteLength`。
  - `api.browserTabFrame` 返回 `{blob, viewportWidth, viewportHeight}`。
- `web/src/api.ts`、`web/src/types.ts`：frame 元数据与 Owner action 类型/方法。
- `web/src/styles.css`：控制条、模式高亮、透明输入 textarea；首屏 gzip 预算按
  既有惯例上移到下一个 256 字节边界（`build.test.mjs` 注释记录实测值）。
- `web/tests/browser_task_panel.test.mjs`、`browser_task_panel.browser.mjs`：
  新增坐标/键盘/文本/滚动单测和完整控制模式回归。

## 3. 设计与取舍

### 3.1 只读默认、显式控制

- 默认 `controlMode=false`；read-only 时 stage 上不绑定任何指针处理，只有
  预览、tab、地址栏、收藏栏。
- 控制模式下才渲染透明输入 sink、绑定指针/键盘/滚轮，并显示
  “控制中 · 点击画面操作页面；Shift+Esc 退出”。
- 授权被撤销/过期/转移、会话换代、tab 关闭、host 不可用、页面隐藏时退出控制
  模式并清空待发输入；`syncState` 的授权丢失路径也会立即退出。
- 切换 tab 时保留控制模式（Owner 的明确选择），但清空队列、未决单击、滚动累积
  和键盘组合状态，避免目标页切换后旧输入落到新页。

### 3.2 Owner 动作集只对 Owner 开放

- `context_click/text/drag` 由 `ActOwner` 接受，`Act` 直接拒绝。Agent MCP 工具
  的 schema/行为不变；这不是新增 Agent 权限。
- `text` 不点击目标，专门用于“先点输入框、再连续键入”，避免每个字符重复点击
  抢焦点；Agent 仍使用 `type`。
- 右键用真实 `button:"right"`，页面自定义菜单可工作；Chromium 也可能弹出原生
  Edge 菜单，这是已知限制而不是 bug。
- 一次右键手势只产生一个 `context_click`：`contextmenu` 事件在 Linux/macOS 可能
  先于 `pointerup` 到达，在 Windows 可能后到。手势上带 `contextQueued` 标记，
  `contextmenu` 认领后 `pointerup` 不再补发；只后到的情况用 `contextHandledAt`
  挡住。按住 150ms 再松开的 Playwright 用例专门覆盖旧 80ms 时间窗的缺陷。
- 拖拽为左键 `move → down → move{8 steps} → up`，起点/终点都做 viewport 校验。

### 3.3 坐标映射

- host 截图加 `scale:"css"`，让图片像素在 DPR≠1 时也等于 CSS 像素；viewport
  读取有 1.5s 上限，卡死页面不会拖住整个 frame 请求。
- 纯函数 `browserViewportPoint`：
  1. `scale = min(rect.w/img.w, rect.h/img.h)`，内容区居中；
  2. 事件点落在内容区外返回 `null`（留白/图片外不发动作）；
  3. `x = floor((clientX-left)/contentW * viewportW)`，clamp 到 `[0, W-1]`；
  4. **viewport 缺失/为 0/不完整一律返回 `null`**，不回退图片像素；面板对
     click/drag/scroll/hover 失败关闭，并提示等待下一帧。
- 单测覆盖：1280×800 图显示在 640×480 舞台（上下留白 40px）、2560×1600 图 +
  1280×800 CSS viewport 的 DPR 场景、边缘 clamp、0/单边缺失 viewport 全部
  返回 `null`。
- 每次事件实时读取 rect 和 natural size，窗口缩放不需要重建 handler。

### 3.4 串行、并发、错误

- 客户端同一时刻只发一个动作；点击经 300ms 判别，双击只发一个 `double_click`，
  不产生 click+click+double_click。
- 显式动作（click/key/text/scroll/drag）进有序队列，不丢弃；hover 单独
  best-effort、最多一个待发位置，有显式动作排队/在途时跳过。
- host `inputSlot` 是最终串行点；Agent/Owner 并发都排它。
- 可证明未执行的 `browser_operation_busy` 提示稍后重试；`browser_action_invalid`
  （多为窗口缩放导致越界）提示刷新；授权/会话/tab/host 终态错误立即退出控制
  模式并提示。
- **不做自动重试**：HTTP 超时后动作可能已执行，重放会造成重复点击/重复键入。
  代价是用户需要确认页面状态；评审包把它列为取舍而不是遗漏。

### 3.5 键盘、IME、粘贴与限制

- 透明 textarea 只做输入通道（`pointer-events:none`，不影响画面点击）：
  - `keydown` 处理特殊键/组合键；可打印字符走 text；
  - `input` 兜底虚拟键盘/移动端，`compositionstart/end` 处理中文 IME，
    并去重 composition 后的重复 input；
  - `paste` 直接取 `clipboardData`，不依赖远程页面可编辑。
- 文本每次 action ≤4096 UTF-8 字节（与 host/Go 一致），按码点切分；换行转为
  Enter；一次粘贴 ≤16 KB，超出给出可读错误。
- 特殊键：Enter/Tab/Escape/Backspace/Delete/Insert/方向键/Home/End/PageUp/PageDown/
  F1–F12；组合键最多 4 个 token。
- 安全：Meta/Win 组合键不转发（在宿主是 OS 级快捷键，Win+L 可能锁屏）；
  Ctrl+Alt+Delete 也拒绝。Ctrl/Cmd+滚轮留给面板缩放。
- 焦点：点画面时聚焦输入 sink；地址栏获得焦点后按键只进地址栏；重绘时只在
  输入 sink 原本有焦点时恢复焦点，不主动抢。

### 3.6 授权与审计

- Owner 路由复用 `authorize` 的 profile 独占 lease、session、tab 归属检查；不
  另开授权通道。
- 审计 actor 是 Owner 会话 id（`owner_...`），`source="owner"`，含
  `task_id/grant_id/tab_id/kind/target/result`；Agent 行 `source="agent"`。
- text/keys 永不写审计、日志或响应；frame 仍不审计。

## 4. 测试与实跑结果

环境：Linux builder（32 核，Go 1.27.4，Node 24.21.0），worktree 在 XFS；Web 回归
用 Playwright headless Chromium，**不是真实 Edge/Windows**。

| 命令 | 结果 |
| --- | --- |
| `node --test browser-host/*.test.mjs` | 57 pass，6 skipped（平台/真实 Chromium 相关），0 fail |
| `cd web && npm test` | 632 pass，0 fail |
| `node web/tests/browser_task_panel.browser.mjs`（`AHA_PLAYWRIGHT_MODULE` 指向已装 Playwright） | pass；覆盖只读不发输入、控制开关、中心点击映射、letterbox 忽略、双击、单击右键、**按住 150ms 的右键只发一次**、键盘、滚轮、**缺 viewport 头不发坐标输入并在恢复后继续**、403 退出、撤销退出 |
| `go build ./...` | OK |
| `go test ./internal/browser -run 'TestOwnerActionsUseOwnerSet|TestPageActionValidation|TestPageActionsRequireInteract|TestSnapshotPasses'` | PASS（约 49s） |
| `go test ./internal/httpapi -run 'TestBrowserTabFrameReturnsAuthorizedImageAndStopsAfterRevoke\|TestAgentBrowserPageActionsOverHTTP\|TestOwnerBrowserPageActionsOverHTTP\|TestOwnerBrowserActionsRequireInteractOverHTTP' -count=1 -v` | 4/4 PASS（23.4s；Owner actor 断言为 `owner_...`） |
| `go test ./internal/browser -count=1 -timeout 800s` | exit 0（约 9 分钟，XFS 上的完整包；后台任务结束时 stdout 已被清理，仅保留退出码） |

新增用例覆盖：

- Go：Owner/Agent 动作集边界、Owner text/drag 参数校验、审计来源/grant/tab 且不
  泄露 text、snapshot-only grant 拒绝、释放后 403、frame viewport 头。
- host：右键、text 不点击、drag 序列、非法参数、单字符按键（`Control+/`）、
  frame viewport 头（含缓存帧）、截图使用 `scale:"css"`。
- Web：留白/DPR/缺失 viewport 的坐标换算（缺失时返回 `null`）、键盘映射（含
  Meta/Ctrl+Alt+Del 拒绝、IME）、UTF-8 切分/换行、滚动上限；浏览器回归覆盖完整
  控制路径、按住 150ms 的右键去重，以及缺 viewport 头时 click/scroll 都不发、
  恢复有效帧后重新可用。
- 变异验证：把构建产物里的右键判断临时改回旧的 80ms 时间窗，浏览器回归在
  “按住 150ms”用例上得到 `3 !== 2`（多出一次 `context_click`），确认该用例确实
  能抓住旧缺陷；随后重新构建恢复，用例通过。

全包结果：`internal/browser` 完整包退出码 0（约 9 分钟，XFS；stdout 未保留，只有
退出码），新增的三个 Owner 用例包含在内。`internal/httpapi` 未跑完整包（XFS 上
耗时长），跑的是 Browser/Owner 相关定向用例，全部通过；未跑到的部分是本任务未
改动的 desktop/channel 等路由，`go build ./...` 已覆盖编译。

## 5. 未覆盖风险与 Owner 实机验收项

必须实机（真实 Windows Edge）验收：

1. 真实 Edge 下左键/双击/右键/滚轮/拖拽的位置是否准确（尤其窗口最大化、
   多显示器 DPI 缩放、窗口拖动后）。
2. 中文 IME：候选框位置、组合过程中不丢字、composition 结束后不重复发送。
3. `Ctrl+C/V`、`Tab`、`Escape`、方向键；粘贴长文本和换行。
4. 移动端/触屏：单指拖动滚动、轻点、长按右键、软键盘弹出/收起、输入时画面
   不被原生滚动带走。
5. 右键是否被 Edge 原生菜单挡住后续点击；页面自定义菜单是否正常。
6. 控制中切 tab、撤销/转移 lease、host 重启、Profile 被另一个 Task 占用时，
   面板是否立即回到只读并给出可读提示。
7. 多窗口 Profile 下选中/激活行为，以及点击打开的新 tab 在下一次同步后出现。

已知/未覆盖：

- 帧有 300–1200ms 间隔，点击作用于最近一帧的内容；页面在两次帧之间变化时可能
  点到旧位置（host viewport 校验只能拦越界，不拦“旧内容”）。
- 窗口缩放后到下一帧前，旧 viewport 可能短暂不匹配；越界返回
  `browser_action_invalid`，不静默。帧没有有效 viewport 头时坐标输入直接暂停，
  不会用图片像素猜。
- 一次右键手势只发一次 `context_click`（已按手势去重）；右键仍可能弹原生菜单，
  这是页面/浏览器行为，不属于重复发送。
- Ctrl/Cmd+滚轮是面板缩放而不是页面缩放；无双指缩放。
- hover 是 best-effort，可能跳过中间位置；不保证 hover 与点击严格同帧。
- 队列上限 64 个显式动作，超出提示“操作太频繁”，不静默丢弃。
- 未改动上传/下载/Cookie 导入导出、Agent API、profile lease、frame 轮询频率。
- 未在真实 Windows Edge 上执行；未验证宿主 OS 的 IME/软键盘行为。

## 6. 现场与后续

- 所有改动只在上述 worktree 的分支上，未合并、未 push、未部署。
- 未接触真实账号、凭据、Cookie；测试只用 fake host/Playwright headless。
- `web/node_modules`、`web/build`、`web/dist`、`.aha2-context` 均为 gitignore，
  不在提交里。
- 需要后续 Owner 决策：是否接受“无自动重试 + 明确错误提示”的取舍；是否在真实
  Edge 验收后再考虑 action_id 幂等/重放缓存。

## 7. 复审修复（r2，task-741 L2）

复审结论是 REQUEST_CHANGES，两个必须项均已复现并修复；修复提交见本节末尾。

### 7.1 按住右键 >80ms 会发两次 context_click

- 根因：旧实现只在 `contextmenu` 与 `pointerup` 之间用 `Date.now()-contextClickAt`
  的 80ms 时间窗去重。Linux/macOS 的 `contextmenu` 可能在按钮按下时就到，Owner
  按住超过 80ms 再松开，`pointerup` 会认为时间窗已过而补发第二次。
- 修复：`ControlGesture` 增加 `contextQueued`。`contextmenu` 一旦认领当前手势就
  置位并立即发一次；`pointerup` 看到该标记只更新时间戳、不再补发。若平台把
  `contextmenu` 放在 `pointerup` 之后，`pointerup` 先发一次并设置
  `contextHandledAt`，后到的 `contextmenu` 在 250ms 内跳过。每只右键手势只产生
  一个 `context_click`，与按住时长无关。
- 测试：浏览器回归新增“按住 150ms 再松开”用例，并在按住期间显式派发一个
  `contextmenu`，确定性地模拟 Linux/macOS 顺序。变异验证把判断改回旧的 80ms
  时间窗后，该用例得到 `3 !== 2`；修复后通过。

### 7.2 缺 viewport 头时回退图片像素

- 根因：旧 `browserViewportPoint` 在 `viewportWidth/Height` 缺失时退回
  `naturalWidth/naturalHeight`。DPR≠1 时截图像素不等于 CSS 像素，点击仍可能落在
  视口内，因此 host 的越界校验也拦不住，属于静默点偏。
- 修复（两层）：
  1. host 截图改用 `scale:"css"`，让图片像素等于 CSS 像素；viewport 探测加
     1.5s 上限，卡死页面不会拖住 frame。
  2. Web 端失败关闭：`browserViewportPoint` 对缺失/为 0/单边缺失的 viewport
     一律返回 `null`；`pointerup`、`wheel`、`contextmenu` 在坐标输入前检查
     `frameViewportReady()`，不满足时不发 click/drag/scroll/hover，并提示
     “画面尺寸未知，坐标输入已暂停；请等待下一帧画面”。下一帧带回有效 viewport
     后自动恢复。
- 测试：单测断言 `0/0`、`1280/0`、`0/800` 全部返回 `null`；浏览器回归新增
  “缺 viewport 头”用例，设置 fixture 省略响应头，断言一次 click 和一次 wheel
  都不产生坐标动作、提示可见；恢复有效帧后点击重新可用。

### 7.3 未改动的边界

- 授权、lease/fence、跨 Task 边界、审计脱敏（text/keys 不入审计）和“不自动
  重试”保持不变。
- Agent 动作集没有扩大；`context_click/text/drag` 仍只走 Owner 路由。
- 非阻塞项：evaluate 超时已顺带加 1.5s 上限；Agent 按键能力与文档不一致、
  `browser_action_invalid` 提示偏泛、非中心点/非 1:1 坐标 fixture、新路由缺
  CSRF 缺失用例、hover 与点击顺序，均未在本轮扩大范围，继续如实披露。

### 7.4 r2 验证

| 命令 | 结果 |
| --- | --- |
| `node --test browser-host/*.test.mjs` | 57 pass，6 skipped，0 fail |
| `cd web && npm test` | 632 pass，0 fail |
| `node web/tests/browser_task_panel.browser.mjs` | pass（含按住 150ms 右键、缺 viewport 头两个新用例） |
| `go build ./...` | OK |
| `go test ./internal/browser -run 'TestOwnerActionsUseOwnerSet\|TestPageActionValidation' -count=1` | PASS（17.4s） |
| `go test ./internal/httpapi -run 'TestOwnerBrowserPageActionsOverHTTP\|TestOwnerBrowserActionsRequireInteractOverHTTP\|TestBrowserTabFrameReturnsAuthorizedImageAndStopsAfterRevoke' -count=1` | PASS（17.5s） |

修复提交：`e3f1336`（Fix owner browser control review findings）；本评审包的 r2
更新在随后单独的提交里。
