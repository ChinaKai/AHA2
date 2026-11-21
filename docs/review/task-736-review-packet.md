# task-736 review packet：渠道页合并权限申请 + 任务带字体收敛 + 审批结果通知

- **实现提交**：`5724d9c`（首轮），`681bc59`（task-740 L2 评审修订），`6e27038`（L2 复审回归 M3）。
- **评审包提交**：本文档与截图随后单独提交。
- **base**：`ce2ceef`（当前 main）。
- **分支 / worktree**：`aha/task_75ddc0ed40d96b2c3d5f` / `/srv/build/users/kaikai/kk-workspace/.aha2-worktrees/task_75ddc0ed40d96b2c3d5f`。
- **边界**：未合并、未 push、未部署、未运行安装器、未修改 AHA 数据。
- **改动范围**：`web/src`、`web/tests`、`internal/app`、`internal/store`、`internal/httpapi`、`internal/channel`、`docs/review`。没有 schema 迁移，没有新增依赖。

## L2 评审修订（681bc59）

| 评审项 | 修法 | 测试 |
|---|---|---|
| 1. 渠道托管/只读 Task 0 pending 时入口消失 | 渠道按钮改成始终渲染；`channel_instance_id` 非空或只读时渠道页只渲染审批区，不渲染绑定表单；pinned channel 不再被自动收起。`taskRuntimeSignature` 加入 `permission_requests`，SSE 新建待审批也会触发实时区域刷新，角标不再因为无变更而漏更新。 | `task_channel_permissions.browser.mjs` 新增 `t-hosted`（`channel_instance_id=instance-hosted`、初始 0 pending）：渠道按钮存在且无角标、无绑定表单、能打开已批准历史；随后模拟 SSE 新增 pending，断言角标变成 1 且待审批项出现。 |
| 2. 过期通知会漏 | `ListTaskPermissionRequests` 改成纯读，不再偷偷写 `expired`。过期只由 `ExpireTaskPermissionRequests` 条件更新；`TaskPermissionRequests` 和 `CreateTaskPermissionRequest` 都先调用同一个 `settleExpiredTaskPermissionRequests`，所以“过期后直接重新发起申请”也会恰好通知一次。 | 新增 `TestCreateAfterExpiryStillNotifiesOnce`：过期后直接 Create，断言新申请可创建、旧申请结果消息恰好 1 条。store 测试改为“list 不写过期、显式 Expire 才写”。 |
| 3. 终态 Task 批准后 receipt 停在 processing | 卡片批准命中终态分支时，先 `FinishChannelInbox(processed, permission_approved)`，再写历史记录。 | 卡片测试对批准和拒绝都断言 receipt 最终为 `processed`。 |
| 4. 过期文案 | 改成 `申请已过期（owner 未处理）`，不再写“owner 已过期”，也不列处理人。 | app 过期测试和 Create-after-expiry 测试按新文案断言。 |
| 保持项：failed 仍按终态 | 不改。`TaskAcceptsApprovalResultWake` 只放行 active / waiting_user；completed、failed、cancelled、blocked、draft、preparing 都只记历史。 | 现有终态测试覆盖 completed；代码路径对所有非 active/waiting 状态一致。 |
| N4 续跑失败兜底 | 本次不改。批准续跑失败仍只回 `continuation_error` 并记日志，没有自动补一条历史记录；如果强行补发，可能在“续跑其实已提交但响应失败”时造成双消息，风险更高。列为残余风险。 | — |
| N5 邮件取消竞态 | 本次不改。`cancelMailMessage` 先读旧状态决定是否通知；如果读到 draft 后、真正取消前被并发提交为 pending_approval，这一条取消通知会被跳过。要彻底修需要 store 返回“是否从已提交状态取消”，超出本次最小范围。列为残余风险。 | — |


## L2 复审回归 M3（本轮）

复审指出：上一轮把 `state.taskTool = state.taskToolPinned` 无条件保留后，`openTask` 只要看到 `taskTool==="channel"` 就会调 `reloadTaskChannelState()`，没有判断 Task 是否能绑定渠道。真实服务端对渠道托管/只读 Task 返回 400 `task is not eligible for a primary channel`，结果 pinned 渠道页每次打开 hosted Task 都弹错误。

修法：

- `reloadTaskChannelState` 开头统一拦截：`!taskChannelRouteEditable(state.selectedTask)` 直接返回，不设置 loading、不发请求。任何调用点都被覆盖。
- `openTask` 的 pinned reload 条件也显式加上 `taskChannelRouteEditable(detail)`，和点击渠道按钮的路径一致。
- 测试 fixture 把 `t-hosted` / `t-readonly` 的 `channel-routes` 改成真实服务端的 400，并统计 `window.__channelRouteCalls`。pinned 打开 hosted 和只读 Task 都断言：调用次数为 0、没有 `.banner.error`、没有绑定表单、审批区和“查看历史”仍在。
- 补了 `t-readonly`（`read_only: true`）用例。它不在本机默认任务列表里，所以测试通过 navigation snapshot 深链打开，不为了测试改默认筛选。
- 变异验证：临时去掉两处 `taskChannelRouteEditable` 判断后，`task_channel_permissions.browser.mjs` 立即失败，报 `pinned hosted Task requested channel-routes`；恢复后通过。这说明测试确实能抓住这条回归。

截图：`task-channel-hosted-pinned.png`、`task-channel-readonly-pinned.png`。

## 0. owner 原话

第一段（2026-11-21 22:29）：

> 目标：改 AHA2 Web 任务详情页的两处展示层 UI，独立 worktree + 提交 + 评审包。owner 已批准方案。
>
> 【范围一：渠道页与权限申请合并】……待审批数量改为“渠道”工具上的角标……群成员区：桌面三列紧凑勾选 chip 网格，每行 36px……可@联调人区：每行约 38px……权限申请区：并入渠道页底部，全宽……无渠道绑定时，渠道页仍要渲染审批区兜底……
>
> 【范围二：左侧任务带字体收敛】……`.task-band-head small` 字号下调一档（目标 9px 左右）……`.task-band-card-meta` 改为两行网格……

追加需求（2026-11-21 22:31）：

> “给736增加一个需求，当前渠道和邮件的申请owner处理完好像没有通知任务处理结果，是不是向宠物审批那样处理完通知一下任务”
>
> 这覆盖原请求里“只改展示层、不改 Go 后端”的限制：后端通知语义现在是本 Task 的一部分。……不要重复唤醒……只通知发起申请的 Task……终态只记录、不唤醒……重复/并发 resolve 幂等……不引入破坏性 schema 迁移……

## 1. 现状追踪（先只读，再改）

### 1.1 Web 任务权限申请

入口：`POST /api/v1/tasks/{id}/permission-requests/{request}/resolve`，见 `internal/httpapi/task_handlers.go` 的 `resolveTaskPermissionRequest`。

- `store.ResolveTaskPermissionRequest` 用 `WHERE id=? AND status='pending' AND expires_at>?` 做原子消费；重复点返回 `changed=false`，handler 回 409。
- **批准**：
  - 有 `ChannelInstanceID + ChannelConversationID + InboundReceiptID + ApplicantIdentityLinkID` 时，走 `SubmitTaskPermissionApprovalContinuation`，保留 route、inbound receipt 和 applicant 上下文，让后续 Turn 能回原群、@ 原申请人。
  - 没有渠道字段时，走普通 `SubmitMessage(taskID, content)`。
- **拒绝**：只更新状态并 `FinishTaskPermissionCardFromWeb` 更新卡片，没有任何 Task 消息或唤醒。
- **过期**：`ListTaskPermissionRequests` 在读列表时把过期 pending 改成 `expired`，同样没有消息。

结论：批准本来就有续跑消息；拒绝和过期完全没有反馈，这就是“处理完像没发生”的主因。

### 1.2 飞书卡片权限申请

入口：`internal/channel/permission_cards.go` 的 `resolveTaskPermissionCardAction`。

- `confirm -> approve`，`cancel -> reject`；复用同一个原子 `ResolveTaskPermissionRequest`。
- 有 route / receipt / permission_request 时，批准会组好 provenance，走 `SubmitChannelMessageWithAttachments` 续跑。
- 拒绝只 `FinishChannelPendingAction(cancelled)` + `FinishChannelInbox(processed)`，没有 Task 消息。
- Web 处理的卡片由 `FinishTaskPermissionCardFromWeb` 原地更新。

### 1.3 邮件审批

前端任务详情邮件面板和资源页邮件列表调用同一组 API：`approveMailMessage` / `rejectMailMessage` / `cancelMailMessage` / `retryMailMessage`，服务端都在 `internal/httpapi/mail_message_handlers.go`。

- `MailMessage` 已经带 `TaskID`、`ProjectID`、`WorkspaceID`、`TurnID`、`AgentID`，所以能定位发起 Task，不需要迁移。
- 状态机：`draft / pending_approval / queued / sending / sent / failed / dead_letter / rejected / cancelled`。
- 批准把 `pending_approval -> queued` 并创建投递；拒绝 `pending_approval -> rejected`；取消覆盖 pending/queued/failed/dead_letter；重试只针对 failed/dead_letter。
- **改前没有任何消息、事件或 Turn 唤醒**；批准只影响投递，Task 不知道结果。邮件没有 pending 过期机制，`pending_approval` 不会自动过期。

### 1.4 宠物审批参考

`internal/app/pet_delegate.go` 的 `finishPetApprovalResolution`：

- `ResolvePetApproval` 先检查状态，再用 store 原子落库；重复处理报“审批已经处理过”。
- 落库后向宠物 Task `SubmitMessage` 一条 `[审批结果] owner…` 消息。
- 重审批在后台 claim 前先判重；执行失败 `failPetApprovalExecution` 把记录放回 `pending`，不会留下半完成状态。
- 消息形态是“结果一行 + 可选备注”。

本次沿用“结果一行”的形态和原子落库的思路，但把“续跑”和“通知”合并到同一条消息，避免照搬宠物实现造成双唤醒。

## 2. 实现与取舍

### 2.1 渠道页 / 权限申请（前端展示层）

- 删除 `permissions` 工具；`normalizePinnedTaskTool("permissions") -> "channel"`，`renderTaskToolContent` 也兜底把旧值映射到渠道，因此旧 pinned 布局或深链不会出现空白页。
- 渠道按钮显示待审批角标，沿用 `.task-tool-badge` / `has-pending`，超过 99 显示 `99+`；`syncTaskPermissionIndicators` 改为更新渠道按钮。
- 成员区：桌面 `repeat(3,minmax(0,1fr))`、chip 高 36px；≤760px 两列。每个 chip 有复选框、姓名、来源/身份小标签，`is_self` 保持 disabled。
- 顶部工具条：搜索框、全选、清空、`已选 N / 共 M`。搜索只切换 chip 的 `hidden`，不重渲染表单；全选/清空只作用于未隐藏且非 disabled 的成员。
- 可@联调人：38px 单行，姓名 + 行内身份输入（保留 datalist、`maxlength=40`）+ 删除按钮；删除即取消对应成员勾选并重建列表。
- 审批区：`#task-channel-permissions` 全宽放在渠道页底部。默认只列 pending；“查看历史”切到已处理；每行一行摘要（申请人 + Bot 标记 + 动作 + 状态徽标），详情列范围、原因、风险、申请时间、处理人、处理说明；批准/拒绝按钮都是 `type="button"`。
- 渠道按钮始终存在；无渠道绑定时审批区照常渲染；channel-hosted / 只读 Task 只渲染审批区，不显示无法使用的绑定表单，0 条待审批时仍可查看历史。
- 窄屏顺序：`.task-channel-permissions.has-pending { order: 2 }`，即“审批 → 群成员 → 可@联调人”；没有 pending 时审批区回到最底部。
- 决定/切换历史只重渲染审批区（`refreshTaskPermissionSection`），不会丢掉成员表单里尚未保存的身份输入。

### 2.2 任务带字体

- `.task-band-head small`：9px、`white-space:nowrap`、超出省略。
- `.task-band-card-meta`：两行 grid，项目一行、Workspace 一行，9px，图标 9px，左对齐、省略；`title="所属项目"` / `title="所属 Workspace"` 保留。
- 标题字号、两行截断、状态徽标、task-code 未动。

### 2.3 审批结果通知（后端）

新增 `internal/app/approval_results.go`：

- `TaskApprovalResultContent` 统一生成 `[审批结果] <类型>「<动作/主题>」（<id>）：owner 已<批准/拒绝/取消>。处理人：…。时间：…。原因：…`，过期则写成 `申请已过期（owner 未处理）`；可选 `Instruction` 拼接“请继续执行已授权动作”。
- `NotifyTaskApprovalResult`：
  - active / waiting_user：`SubmitMessage`，正常开一个 Turn；
  - completed / failed / cancelled / blocked / draft / preparing：只 `AddConversationItem`（`agent_message`，payload `approval_result=true`），不改 Task 状态、不唤醒。
  - 写入前再读一次状态，覆盖读与写之间并发完成/取消的窗口。

**不重复唤醒的取舍**：

| 路径 | 改法 | 为什么 |
|---|---|---|
| Web 批准，有渠道 route | 在原有续跑消息前拼 `[审批结果]`，仍只有一条 `SubmitTaskPermissionApprovalContinuation` | 续跑必须保留原 receipt/route；再发一条会双唤醒 |
| Web 批准，无渠道 | 只调用一次 `NotifyTaskApprovalResult`，消息里带继续指令 | 原子替代“SubmitMessage + 再通知” |
| 飞书卡片批准 | 在原有 `SubmitChannelMessageWithAttachments` 内容前拼结果 | 同上；Task 不可唤醒时改成历史记录 |
| Web / 卡片拒绝 | 只发一条结果消息 | 拒绝没有续跑，不存在等价唤醒 |
| 过期 | 只发一条结果消息 | 同上 |
| 邮件批准/拒绝/取消 | 只发一条结果消息 | 邮件审批本来没有续跑消息 |

**幂等**：

- 权限：store 条件更新 `pending && expires_at>now`；第二次 resolve 拿不到变更，Web 回 409，卡片报“已经处理过”，不会发第二条。
- 邮件批准：store 对重复 approve 返回 nil `events`；handler 只在 `len(events)>0` 时通知。
- 过期：新增 `ExpireTaskPermissionRequests`，逐行 `WHERE status='pending' AND expires_at<=?` 条件更新，只返回本次真正改掉的行；`TaskPermissionRequests` 和 `CreateTaskPermissionRequest` 都走 `settleExpiredTaskPermissionRequests` 逐条通知，第二次读或紧接着的新申请不会再发。`ListTaskPermissionRequests` 已改成纯读。
- 已处理/过期后重复点击不会产生新的 Turn 或消息。

**公开 API**：形状不变；仅在通知失败时给原响应加可选 `notification_error`（权限 resolve / 邮件 approve）。不新增接口、不改 schema。

## 3. 关键 diff

- `web/src/task_tools.ts`：移除 permissions 工具，渠道承载 badge，旧值映射。
- `web/src/main.ts`：渠道页成员/联系人/审批区/历史切换；`refreshTaskPermissionSection`；渠道按钮角标同步；只在可编辑 Task 上加载 route。
- `web/src/styles.css`：3/2 列 36px chip、38px 联系人行、审批一行摘要、窄屏 order、任务带 9px 两行 meta。
- `web/src/i18n.ts`：新增可见文案的中英映射与动态计数 pattern。
- `internal/app/approval_results.go`：结果文案 + 唤醒/历史分流。
- `internal/app/task_permissions.go`：过期检测后通知一次。
- `internal/store/task_permissions.go`：`ExpireTaskPermissionRequests` 条件更新。
- `internal/httpapi/task_handlers.go`：批准合并结果，拒绝发结果；终态 Task 不续跑。
- `internal/channel/permission_cards.go`：卡片批准合并结果，拒绝发结果，终态只记录。
- `internal/httpapi/mail_message_handlers.go`：approve / reject / cancel 各发一条结果，重复 approve 不重复发。

## 4. 三种宽度验证（截图）

截图都在 `docs/review/task-736-screenshots/`，由 `task_band_typography.browser.mjs` 实际渲染生成：

| 宽度 | 截图 | 断言 |
|---|---|---|
| 1280（正常展开） | `task-band-1280.png` | 头部 9px / 单行；meta 两行、左对齐、9px、图标 9px；卡片无横向溢出；标题仍 `-webkit-line-clamp: 2` |
| 1000 视口 + 272px 窄展开 | `task-band-272.png` | 浮层宽度 272±2；头部仍单行；meta 仍两行；卡片无溢出 |
| 390（移动端） | `task-band-390.png` | 任务带隐藏；页面无横向溢出 |

渠道页截图：

- `task-channel-merged.png`：1280 下三列成员、联系人行、底部审批区。
- `task-channel-merged-390.png`：390 下两列成员，待审批区在成员区上方。
- `task-permissions-panel.png`：渠道页内的待审批详情与批准/拒绝按钮。
- `task-permissions-history.png`：切到历史后的已批准记录。
- `task-channel-hosted-sse.png`：渠道托管 Task 在 0 pending 时打开历史，随后模拟 SSE 新 pending，渠道角标变为 1。
- `task-channel-hosted-pinned.png`：pinned 渠道页打开渠道托管 Task，不发 channel-routes、不报错。
- `task-channel-readonly-pinned.png`：pinned 渠道页打开只读 Task，同样不发请求、不报错。

## 5. 实际命令与退出码

全部在本 worktree 执行：

| 命令 | 退出码 |
|---|---|
| `go build ./...` | 0 |
| `go vet ./internal/app ./internal/channel ./internal/httpapi ./internal/store` | 0 |
| `go test ./internal/app -run 'TestTaskApprovalResult\|TestNotifyTaskApproval\|TestExpiredPermission\|TestCreateAfterExpiry' -count=1` | 0 |
| `go test ./internal/channel -run 'TestSendTaskPermissionRequestCard\|TestTaskPermissionCardDecisions' -count=1` | 0 |
| `go test ./internal/store -run 'TestResolveTaskPermissionRequestIsAtomic\|TestExpireTaskPermission\|TestListTaskPermission\|TestTaskPermissionRequestResolvesOnce' -count=1` | 0 |
| `go test ./internal/httpapi -run 'TestWebPermission\|TestResolveTaskPermissionFromWebKeepsChannelReplyRoute\|TestMailApprovalDecisions\|TestMailApprovalOnTerminal\|TestMailApprovalIsOwnerOnlyAndRejectsChangedDigest' -count=1` | 0 |
| `node scripts/build-web.mjs` | 0 |
| `node --test tests/*.test.mjs` | 0（632 pass / 0 fail） |
| `node web/tests/task_permissions.browser.mjs` | 0 |
| `node web/tests/task_channel_permissions.browser.mjs`（含 hosted Task、SSE 角标、英文） | 0 |
| `node web/tests/task_band_typography.browser.mjs` | 0 |
| `node web/tests/task_detail_band.browser.mjs` | 0 |

M3 变异验证：临时去掉 `reloadTaskChannelState` 和 `openTask` 两处 `taskChannelRouteEditable` 判断后，`task_channel_permissions.browser.mjs` 退出码 1，报 `pinned hosted Task requested channel-routes`；恢复后重新构建并跑回 0。

浏览器环境：`.tools/web-validation` 里 Playwright 1.65.1，配 `~/.cache/ms-playwright/chromium_headless_shell-1255`，用 `AHA_PLAYWRIGHT_MODULE` / `AHA_BROWSER_EXECUTABLE` 指定。

**既有测试**：

- `task_panels.browser.mjs` 在本 worktree 和 `ce2ceef` base 上都会在同一行失败：等不到 `#task-skills-fold input[value="skill-a"]`（任务创建对话框的 skills fold）。用同一份 env-aware 测试文件在 base worktree 复现，确认与本次改动无关，没有改动相关代码；因此没有把它计入通过项。
- `task_permissions.browser.mjs` 已同步改成渠道入口；新增 `task_channel_permissions.browser.mjs`、`task_band_typography.browser.mjs`；`task_panels.browser.mjs` 只补了本地浏览器 env 入口，便于复现上面这条基线失败。

## 6. i18n 检查

- `web/src/i18n.ts` 增加：按姓名搜索/搜索成员、没有匹配的成员、移除联调人、身份、查看历史、只看待审批、没有待审批、无待审批/无历史记录、范围、原因、申请时间、处理人、处理说明、群成员、未填写动作、已过期、高/中/低。
- 动态文本 pattern：`已选 N / 共 M`、`N 条待审批`、`<label>（N 条待审批）`、`N 名已登记成员`。
- `web/tests/task_channel_ui.test.mjs` 逐条校验 `tr()` 英文；`task_channel_permissions.browser.mjs` 用 `?lang=en-US` 实际打开渠道页，断言 `View history`、`Selected 1 / 4 total`、`Search by name`、`Remove contact`、`Pending review`，以及渠道按钮 title 的 `(1 pending)`。英文界面没有回落中文。

## 7. 缺口与残余风险

1. **过期结算时机**：权限申请过期由“读详情/列表”或“同 Task 新建申请”触发 `settleExpiredTaskPermissionRequests`，没有独立定时器。一个长期既没人打开、也没有新申请的任务，过期通知会推迟到下一次结算；不会漏，但可能晚。列表读取本身已不再写过期。
2. **邮件没有过期终态**：`pending_approval` 不会自动过期，所以没有“邮件过期”通知。邮件投递失败/`dead_letter` 是投递结果、不是 owner 审批决定，本次也没有加通知；如果 owner 希望知道投递失败，需要另开需求。
3. **通知不是事务内提交**：审批状态先原子落库，再写结果消息。如果进程恰好在两者之间崩溃，结果消息会丢（状态不会回退）。当前用日志记录通知失败；没有重试队列。
4. **结果文案是中文**：后端消息直接进会话，和宠物审批的默认中文一致；不在 `i18n.ts` 的 UI 文案范围内，英文界面里这条会话消息仍是中文。
5. **历史视图上限**：已处理记录最多显示 50 条，pending 最多 20 条；超出部分没有分页，和原权限面板的 12 条上限相比已放宽但仍有界。
6. **`task_panels.browser.mjs` 基线失败**：见 §5，已在 base 复现，不是本次回归，但没有修它。
7. **N4 续跑失败兜底**：批准时如果续跑消息提交失败，只返回 `continuation_error` 并记日志，不再自动补一条历史记录。没有自动补发的原因是：响应失败不等于提交一定没成功，补发可能造成双消息；需要更明确的提交幂等信号后再做。
8. **N5 邮件取消竞态**：`cancelMailMessage` 先读旧状态、再调用 store 取消。如果读到 draft 后、取消前被并发提交为 pending_approval，这一次取消会成功但通知被跳过。彻底修需要 store 返回“是否从已提交状态取消”，本次没有扩大接口范围。
