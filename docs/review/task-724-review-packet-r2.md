# task-724 review packet r2：725 首轮 REQUEST_CHANGES 的修订

- **本轮审查范围**：`7ed0479..ccd0a6d`，6 个文件 +457/−9，只有源码和测试。
- **链路**：a3ed6cb → 7ed0479（实现）→ 42719ac（首轮材料包，只有文档，原样保留未改）→ ccd0a6d（本轮修订）→ 本文档提交。
- **分支 / worktree**：分支 `aha/task_dd1b8666a866ea43845f`，worktree `/srv/build/users/kaikai/kk-workspace/.aha2-worktrees/task_dd1b8666a866ea43845f`。未 push、未合并、未部署。
- **首轮结论更正**：42719ac 的材料包把手机端遮挡写成“既有布局问题”，这个判断错了。改动前搜索按钮压的是菜单标题；加了第五项后菜单变高，第一行进入搜索按钮范围，Agent Update 被挡住。这是本需求引入的回归，本轮已修复。

## 1. 修订内容与理由

### M1 手机端遮挡（styles.css，+3 行）
- **做法**：`@media (max-width:760px)` 里加一条 `body:has(.conversation-filter-popover[open]) .task-search-fab { display: none; }`。
- **为什么不用 z-index**：菜单在 composer 自己的层叠上下文里，抬 z-index 盖不住搜索按钮。同一段 CSS 已经因为同样的原因，在附件托盘出现时隐藏搜索按钮，这次沿用那个做法，不改布局。菜单关闭后搜索按钮恢复。桌面端本来就不显示搜索按钮（`display:none`），不受影响。
- **测试**（task_conversation_thinking_filter.browser.mjs）：在 360×740、390×844、414×896 三个视口，菜单打开时：
  - 断言搜索按钮已隐藏，菜单关闭后恢复。
  - 每个筛选按钮沿右边缘内 1px、圆角之外的直边每 2px 取一个点做 `elementFromPoint`，必须全部命中按钮本身或子节点，且至少取 8 个点。
  - 5 个按钮依次在右边缘内 2px 处真实点击：请求的 categories 必须和预期一致，`#command-palette[open]` 不能出现，选中态要同步变化。
  - 按钮都不越出视口。
  - 不只测中心点，也不靠隐藏标签或让按钮越出视口来避开遮挡。
- **变异验证**：
  - 删掉这条 CSS：测试失败，报“360: the search FAB still shows over the open menu”。
  - 再去掉搜索按钮可见性断言：右边缘取点断言单独失败，报“360: update right edge is covered”，命中的是 `command-palette-fab task-search-fab`。
  - 验证后已还原并重新构建。

### S1 镜像增量游标（store/sync_graph.go）
- **问题**：`RemoteConversationPageForAgent` 在 `after>0` 时，原来总是返回 `latest = COUNT(*)`，也就是镜像全长。筛选后 LIMIT 截断（has_more=true）时，下一次增量请求会跳过中间没返回的行。
- **修法**：after 模式下 latest 取 `max(after, 本页返回行的最大 sequence)`，和本地 store 一样；没有新行时 latest 等于 after，游标不前进。
- **非 after 模式不变**：仍返回 COUNT。客户端只用最新一页的 latest 作为增量起点，而最新一页按新到旧取，已经包含了最新的匹配行，所以安全。原有测试 `first.Latest == 80` 保持不变。
- **测试** `TestRemoteConversationIncrementalCursorDoesNotSkipRows`：14 行混合数据，从 after=1 开始用 limit=1 逐轮读到空页，覆盖 update、thinking、update+thinking、chat+error+update 四种筛选。断言：
  - 读到的序列和预期完全一致。
  - 游标从不后退。
  - 空读时游标不动。
  - 之后再同步 7 行，继续读能恰好拿到这些新行。
- **变异验证**：把修复短路掉，测试失败，`[update]` 只读到 `[in02-update]`，预期是 4 行。

### S2 categories 输入契约（httpapi/task_handlers.go）
新函数 `conversationCategoriesQuery` 统一处理本地 Task 和镜像：

| 输入 | 结果 |
|---|---|
| 不带参数、空串、只有逗号或空格 | 端点默认：本地返回全部类别；镜像返回 chat,update,thinking,error,mail（不含 tool，和旧行为一致） |
| 有效值，大小写和空格不限，可重复 | trim、小写、去重 |
| 有效值和未知值混合 | 忽略未知值，只按有效值筛选 |
| 全是未知值 | 400 `invalid_categories`，不再放大成包括 tool 在内的全量 |

- 改之前：本地和镜像遇到 `nope`、`THINKING` 都返回全量；镜像传 `,` 也返回全量（含 tool），传空串却返回默认集。
- 仓库内调用方只有 Web，传的都是小写有效值，见 §3.1。

### N1/N2/N3
- **N1**：更正 `conversationCategoryClause` 的注释：空列表不加谓词；未知名按字面匹配存储类别，因此 store 层匹配 0 行；名字由 HTTP 层校验。
- **N2**：新增 `TestRemoteConversationMissingKindStaysInUpdate`。payload 缺 kind 或 kind 为 null 的行归入 update，不进 thinking；两者都选时全部返回。
- **N3**：“N 条已加载”的 i18n 走 i18n.ts 已有的 `/^(\d+) 条已加载$/`，通过 MutationObserver 翻译。新浏览器测试用 en-US 加载更早记录后，断言显示 “70 loaded”。另外实跑了 i18n.browser.mjs 和 task_detail_i18n.browser.mjs。

## 2. 本轮命令与退出码（都在本 worktree 执行）
| 命令 | 退出码 |
|---|---|
| `go build ./...` | 0 |
| `go vet ./internal/store ./internal/httpapi` | 0 |
| `go test ./internal/httpapi -run 'TestConversationCategories\|TestRemoteConversation\|TestRemoteTaskConversation' -count=1` | 0 |
| `go test ./internal/httpapi -run 'TestConversationCategor\|TestRemoteConversation\|TestRemoteTaskConversation\|TestConversationCategoryMailIsVisible\|TestTaskDetailPerf\|Conversation' -count=1` | 0（68.4s） |
| `node scripts/build-web.mjs` | 0 |
| `node --test tests/build.test.mjs`（含首屏 gzip 预算） | 0（101 pass） |
| 浏览器测试：task_readonly_filter / task_round_activity / task_conversation_thinking_filter | 0 / 0 / 0 |
| i18n 扫描：task_detail_i18n.browser.mjs、i18n.browser.mjs（scratch harness 字节一致副本 + @sparticuz 桩模块） | 0 / 0 |
| 变异 4 个：M1 去掉 CSS、M1 只留取点断言、S1 短路、S2 去掉拒绝分支 | 测试都按预期 FAIL；之后已还原 |

- **浏览器环境**：scratch 里装的 playwright 1.65.1，加上 `~/.cache/ms-playwright/chromium_headless_shell-1255`，通过 `AHA_PLAYWRIGHT_MODULE` 和 `AHA_BROWSER_EXECUTABLE` 指定。
- **新增测试中途修过两次，都是测试写法问题**：
  - 第一次取点包含了圆角外的像素，改为只取直边段。
  - 第二次在菜单已打开时又点了一次 trigger，把菜单关掉了，改为菜单未打开时才点。
- **本轮没重跑**（上轮已有证据，按要求不无理由重跑）：store 的 SQL/默认/组合测试（`TestConversationPageSplitsThinkingFromUpdate`）、app 投影测试、web 全量 npm test、tsc。
- **本轮改动对这些测试的影响**：store 层只改了注释和镜像函数，镜像相关测试已重跑。前端只改 CSS 和测试，build.test 已重跑。

## 3. 补材料（首轮 missing）

### 3.1 ConversationPage 系列的全部调用点
用 `grep -rn '\.ConversationPage(\|\.ConversationPageForAgent(\|\.RemoteConversationPageForAgent(' internal cmd` 检索，生产代码（非 _test.go）一共 5 处：

| 调用点 | 函数 | categories 实参 | 受本需求影响？ |
|---|---|---|---|
| internal/app/pet_task_admin.go:383 `PetTaskConversation`（宠物 MCP 工具 pet.go:3322 调用） | ConversationPageForAgent | `nil` | 否，不筛选，含 reasoning |
| internal/app/service.go:1413 构建 Turn prompt 与附件 | ConversationPageForAgent | `nil` | 否 |
| internal/app/service.go:1449 恢复上下文用的最近对话 | ConversationPageForAgent | `[]string{"chat"}` | 否 |
| internal/httpapi/task_handlers.go `conversationForAgent`，镜像分支 | RemoteConversationPageForAgent | 归一化后的 categories；为空时用默认集 | 是，见 §1 S2 |
| internal/httpapi/task_handlers.go `conversationForAgent`，本地分支 | ConversationPageForAgent | 归一化后的 categories；为空时不筛选 | 是 |

- `store.ConversationPage`（conversation.go:543）只是包装，生产代码里没有调用方。
- **HTTP 路由**：只有 `GET /api/v1/tasks/{id}/conversation` 和 `GET /api/v1/tasks/{id}/agents/{agent}/conversation`（server.go:499/508）。没有 Agent API、MCP、渠道或导出路由走这两个函数。
- **渠道**：`channelSemanticEvent` 对 `agent_reasoning` 走 default 分支返回 false，所以 reasoning 本来就不进渠道语义流，本需求没有改它。
- **数据保留**：`store/data_retention.go:303` 的 update kind 列表里本来就有 `agent_reasoning_update`，按存储类别和 kind 处理，不受影响。
- **Web 调用方**：
  - `channels.ts:83` 调 `api.conversation(taskID, {before, limit: 500})`，不带 categories，所以本地返回全部。
  - `main.ts` 有 6 处调 `agentConversation`，分布在 5 个函数：openTask（2 处）、selectTaskAgent、reloadConversation、refreshTaskRuntime、loadOlderConversation，都传 `selectedConversationCategories()`。
- **测试调用点**：store 的 conversation_page/agent_read/store/finalize/channel_runtime 测试，app 的 service/interrupt/recovery/pet_owner_tools 测试，httpapi 的 remote/perf/mail/agent/channel 测试。它们传的都是 nil 或小写有效值，本轮定向重跑的 httpapi 套件覆盖了 remote、perf 和 mail。

### 3.2 SSE：服务端事件与 Web 监听（完整函数）
- **结论**：Web 收到任何 `update` 事件，只比较 sequence，然后调 `scheduleTaskRefresh`，不看事件的 type 或 category。
- 是否追加、追加哪些行，完全由 `refreshTaskRuntime` 用当前筛选发出的 after 增量请求决定，服务端按筛选返回。
- 事件 payload 里本来就没有 category 字段。

```ts
function scheduleTaskRefresh(taskID: string): void {
  if (taskRefreshTimer !== null) window.clearTimeout(taskRefreshTimer);
  taskRefreshTimer = window.setTimeout(async () => {
    taskRefreshTimer = null;
    try {
      if (await refreshTaskRuntime(taskID)) updateTaskLiveRegions();
    } catch {
      startTaskFallback(taskID);
    }
  }, 100);
}
function openEvents(taskID: string, after = 0): void {
  closeEvents();
  taskLastSignalAt = Date.now();
  updateRealtimeState("connecting");
  startTaskClock();
  taskMonitorTimer = window.setInterval(() => {
    if (state.selectedTask?.task.id === taskID && Date.now() - taskLastSignalAt > 15000) {
      startTaskFallback(taskID);
    }
  }, 5000);
  events = new EventSource(`/api/v1/tasks/${taskID}/events?after=${after}`);
  startTaskFallback(taskID, false, false);
  events.addEventListener("open", () => {
    // The stream is connected: polling now would only repeat the requests the
    // open already made. A stream that goes quiet is caught by the monitor
    // below, which restarts the fallback after 15s without a signal.
    noteTaskStreamSignal(taskID);
  });
  events.addEventListener("heartbeat", () => {
    noteTaskStreamSignal(taskID);
    updateLiveDurations();
  });
  events.addEventListener("error", () => startTaskFallback(taskID));
  events.addEventListener("update", event => {
    if (state.selectedTask?.task.id !== taskID) return;
    noteTaskStreamSignal(taskID);
    try {
      const payload = JSON.parse((event as MessageEvent).data) as {sequence?: number; type?: string};
      const sequence = Number(payload.sequence || 0);
      if (String(payload.type || "").startsWith("mail_")) mailRefreshRequested = true;
      if (sequence && sequence <= state.taskEventCursor) return;
      state.taskEventCursor = Math.max(state.taskEventCursor, sequence);
    } catch {
      // The incremental refresh below remains authoritative.
    }
    scheduleTaskRefresh(taskID);
  });
}
```
服务端：`taskEvents` 把 EventsAfter 的历史和 hub 实时事件统一写成 `event: update`。
```go
func writeSSE(writer http.ResponseWriter, event any) {
	data, _ := json.Marshal(event)
	if item, ok := event.(domain.Event); ok && item.Sequence > 0 {
		_, _ = fmt.Fprintf(writer, "id: %d\nevent: update\ndata: %s\n\n", item.Sequence, data)
		return
	}
	_, _ = fmt.Fprintf(writer, "event: update\ndata: %s\n\n", data)
}
// recordExecutionEvent 末尾：落库之后把 conversation_item_id 写进事件，再 emitTurn
	if category != "" {
		if summary == "" {
			summary = event.Type
		}
		item := domain.ConversationItem{
			ID: domain.NewID("conversation"), TaskID: turn.TaskID, RoundID: turn.RoundID, TurnID: turn.ID,
			AgentID: turn.AgentID, Category: category, Kind: kind, Summary: summary,
			Payload: event.Data, CreatedAt: s.now().UTC(),
		}
		if durableUpdate {
			item.StreamAgentID = turn.AgentID
			item.FromAgentID = turn.AgentID
			item.ToAgentID = "owner"
			item.RouteKind = "agent_progress"
			if stored, storeErr := s.store.AddConversationItem(ctx, item); storeErr == nil {
				event.Data["conversation_item_id"] = stored.ID
			}
		} else if kind == "agent_message_update" {
			if stored, storeErr := s.store.UpsertBackendStreamItem(ctx, item); storeErr == nil {
				event.Data["conversation_item_id"] = stored.ID
			}
		} else {
			if stored, storeErr := s.store.AddConversationItem(ctx, item); storeErr == nil {
				event.Data["conversation_item_id"] = stored.ID
			}
		}
	}
	s.emitTurn(ctx, turn, event.Type, event.Data)
func (s *Service) emitTurn(ctx context.Context, turn domain.Turn, eventType string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	data["round_id"] = turn.RoundID
	data["turn_id"] = turn.ID
	data["agent_id"] = turn.AgentID
	data["attempt"] = turn.Attempt
	data["generation"] = turn.Generation
	data["required"] = turn.Required
	// The same snapshot type is returned by Task detail. Old clients ignore it.
	data["activity"] = s.activitySnapshot(turn)
	s.emit(ctx, turn.TaskID, "turn", turn.ID, eventType, data)
}
```
真实事件 payload：在本 worktree 临时给 `TestMultiAgentRoundCreatesIntegrationTurn` 加了一段 dump（StubExecutor 实际发出 agent_reasoning / agent_message 事件），测试退出码 0，dump 后已 `git checkout` 还原。截取 main 的两条：
```json
[
  {
    "sequence": 12,
    "id": "event_98eccfb06ba9e4479467",
    "aggregate_type": "task",
    "aggregate_id": "task_fc2503f26aa2b16b2016",
    "type": "agent_reasoning",
    "data": {
      "activity": {
        "agent_id": "main",
        "attempt": 1,
        "elapsed_ms": 407,
        "phase": "thinking",
        "round_id": "round_f3e4f089675610cd339a",
        "since": "2026-11-21T07:23:27.331824913Z",
        "since_ms": 1795245807331,
        "turn_id": "turn_289a3f4761e3db90d08a"
      },
      "agent_id": "main",
      "attempt": 1,
      "conversation_item_id": "conversation_00ce74b83ceb8a8c8b9d",
      "entity_id": "turn_289a3f4761e3db90d08a",
      "entity_type": "turn",
      "generation": 0,
      "reasoning": true,
      "required": true,
      "round_id": "round_f3e4f089675610cd339a",
      "text": "reasoning main",
      "turn_id": "turn_289a3f4761e3db90d08a"
    },
    "occurred_at": "2026-11-21T07:23:27.739016276Z"
  },
  {
    "sequence": 13,
    "id": "event_0f28b8d78e5f715b1de6",
    "aggregate_type": "task",
    "aggregate_id": "task_fc2503f26aa2b16b2016",
    "type": "agent_message",
    "data": {
      "activity": {
        "agent_id": "main",
        "attempt": 1,
        "elapsed_ms": 412,
        "phase": "thinking",
        "round_id": "round_f3e4f089675610cd339a",
        "since": "2026-11-21T07:23:27.331824913Z",
        "since_ms": 1795245807331,
        "turn_id": "turn_289a3f4761e3db90d08a"
      },
      "agent_id": "main",
      "attempt": 1,
      "conversation_item_id": "conversation_097ec8f0e283c59064a8",
      "entity_id": "turn_289a3f4761e3db90d08a",
      "entity_type": "turn",
      "generation": 0,
      "intermediate": true,
      "required": true,
      "round_id": "round_f3e4f089675610cd339a",
      "text": "commentary one main",
      "turn_id": "turn_289a3f4761e3db90d08a"
    },
    "occurred_at": "2026-11-21T07:23:27.744182116Z"
  }
]
```

### 3.3 refreshTaskRuntime 完整函数，含 task_pet 通知
```ts
async function refreshTaskRuntime(taskID: string): Promise<boolean> {
  if (state.selectedTask?.task.id !== taskID) return false;
  const previousStatus = state.selectedTask.task.status;
  const previousSignature = taskRuntimeSignature(state.selectedTask);
  const previousContext = state.taskContext ? JSON.stringify(state.taskContext) : "";
  const previousMail = state.taskMailOptions ? JSON.stringify(state.taskMailOptions) : "";
  const refreshMail = mailRefreshRequested
    || Boolean(state.taskMailOptions?.accounts?.length || state.taskMailOptions?.messages?.length);
  const [detail, page] = await Promise.all([
    api.task(taskID),
    api.agentConversation(taskID, state.selectedTaskAgent, {
      after: state.taskConversationLatest,
      limit: 100,
      categories: selectedConversationCategories(),
    }),
  ]);
  detail.agents ||= [];
  state.selectedTask = detail;
  if (taskStatusNeedsAnnouncement(previousStatus, detail.task.status)) {
    announceStatus(tr(statusLabel(detail.task.status)), detail.task.code || detail.task.title);
  }
  let changed = taskRuntimeSignature(detail) !== previousSignature;
  state.taskEventCursor = Math.max(state.taskEventCursor, detail.event_cursor || 0);
  if (page.conversation.items?.length) {
    const seen = new Set(state.taskConversation.map(item => item.sequence));
    const appended = page.conversation.items.filter(item => !seen.has(item.sequence));
    if (appended.length) {
      // A reader who left the bottom keeps their position; the hint tells them
      // there is new content instead of yanking the viewport.
      if (!conversationFollowBottom) conversationUnreadMessages += appended.length;
      state.taskConversation.push(...appended);
      changed = true;
      if (taskID === "task_pet") {
        const user = appended.find(item => item.kind === "user_message");
        if (user) notifyPetTaskThinking();
        const reply = [...appended].reverse().find(item => item.kind === "agent_message");
        if (reply) notifyPetTaskReply(reply.summary || "");
        if (!user && !reply && appended.some(item => item.kind === "turn_duration")) {
          notifyPetTaskIdle();
        }
      }
    }
    if (state.taskConversation.length > 300) {
      state.taskConversation = state.taskConversation.slice(-300);
      state.taskConversationHasMore = true;
      state.taskConversationBefore = state.taskConversation[0]?.sequence || 0;
    }
  }
  state.taskConversationLatest = Math.max(state.taskConversationLatest, page.conversation.latest_sequence || 0);
  const selectedAgent = detail.agents.find(agent => agent.agent_id === state.selectedTaskAgent);
  if (selectedAgent) selectedAgent.unread_count = 0;
  if (state.taskContext) {
    state.taskContext = await api.agentContext(taskID, state.selectedTaskAgent);
    changed ||= JSON.stringify(state.taskContext) !== previousContext;
  }
  if (refreshMail) {
    const [options, accounts] = await Promise.all([
      api.taskMail(taskID).catch(() => null),
      api.mailAccounts().catch(() => null),
    ]);
    if (options) {
      state.taskMailOptions = options;
      changed ||= JSON.stringify(options) !== previousMail;
    }
    if (accounts) state.mailAccounts = accounts.accounts || [];
    mailRefreshRequested = false;
  }
  return changed;
}
```
assistant.ts 的三个通知函数（未改动）：
```ts
export function notifyPetTaskThinking(): void {
  if (!pet) return;
  pet.classList.add("thinking");
  setThinking(true);
}

export function notifyPetTaskReply(text: string): void {
  if (!pet) return;
  pet.classList.remove("thinking");
  setThinking(false);
  const content = String(text || "").trim();
  if (content) showPetSpeech(content, {holdMs: 3000, fade: true});
}

export function notifyPetTaskIdle(): void {
  if (!pet) return;
  pet.classList.remove("thinking");
  setThinking(false);
```
**默认带上 update 之后的影响（据实说明，本轮未改动）**

**未读提示**：
- `conversationUnreadMessages += appended.length`，这里的 appended 就是当前筛选下真正可见的新行。默认带上 update 后，进度和旁白行也计入“新消息”提示。
- 这和用户手动勾选 Update 时的行为相同，计数和可见行一致。

**task_pet 通知**（只在打开的 Task 是 `task_pet` 时触发）：
- `user_message` 属于 chat，`agent_message` 回复属于 chat，这两个触发条件不受影响。
- `turn_duration` 属于 update，以前默认不在 appended 里，所以默认筛选下 `notifyPetTaskIdle()` 实际上走不到；现在默认会走到。
- `notifyPetTaskIdle` 只做 `pet.classList.remove("thinking")` 和隐藏思考问号，不改动画实现。
- 结合服务端写入顺序（service.go）：
  - 成功的 Turn 先写回复，再写 `turn_duration`（1884–1886）。同一批里有 reply 时，不会走 idle 分支；分两批到达时，idle 只是重复清掉已经清掉的状态。
  - 失败的 Turn 依次写 turn_failed（error）、`turn_duration`、turn_failed 事件（1890–1899、2281–2287）。
  - 中断的 Turn 写 turn_interrupted（update）和 `turn_duration`（2310–2316）。
  - 以上两种情况现在默认会清掉宠物头顶的思考问号。以前要等下一条回复才清；如果没有下一条回复，问号会一直留着。
- 理论边界：main 的中间回复（agent_result，属于 update）加上后续 Turn 的编排场景，问号会在第一个 Turn 结束时提前清掉。宠物 Task 是 single 模式，没有子 Agent，按代码不会进入 `mainReplyIsIntermediate`，所以实际上遇不到。
- 这条代码路径以前只要用户勾选 Update 就会走到，本需求没有新增宠物逻辑。这个行为是否可以接受，请评审判断；如果要保持旧行为，最小改法是让 idle 只在 `turn_failed` / `turn_interrupted` 时触发，或者让宠物通知不依赖筛选结果，另行决定。

### 3.4 四个 Backend 的 reasoning 投影
所有 Backend 只通过 `Event{Type:"agent_reasoning", Data:{"text":…, "reasoning":true}}` 投影 thinking（`grep -n 'Type: "agent_reasoning"' internal/backend/*.go`，非测试共 5 处）。app 层唯一的 `case "agent_reasoning"` 在 service.go:2997，落库为 category=update、kind=agent_reasoning_update。
```go
// claude.go:414-421
			case "thinking":
				// Extended thinking arrives as a separate content block. Project
				// it the same way DSH does so the operator sees the reasoning
				// card instead of a silent gap before the tool call. Redacted
				// thoughts carry no readable text and are skipped.
				if text, ok := block["thinking"].(string); ok && strings.TrimSpace(text) != "" {
					events = append(events, Event{Type: "agent_reasoning", Data: map[string]any{"text": text, "reasoning": true}})
				}
// codex.go:563-570（exec --json 里的 reasoning item）
		if itemType == "reasoning" && rawType == "item.completed" {
			// Codex reports the model's reasoning summary as its own item. The
			// text lives in item.text on older builds and in summary[] entries
			// (type summary_text) on newer ones; accept either.
			if text := codexReasoningText(item); text != "" {
				return Event{Type: "agent_reasoning", Data: map[string]any{"text": text, "reasoning": true}}, "", ""
			}
		}
// codex.go:882-893（rollout 补齐 exec 流里没有的 raw reasoning）
		text := strings.TrimSpace(codexReasoningText(record.Payload))
		if text == "" || rollout.seenText[text] {
			continue
		}
		if id != "" {
			if rollout.seenIDs == nil {
				rollout.seenIDs = map[string]bool{}
			}
			rollout.seenIDs[id] = true
		}
		rollout.note(text)
		events = append(events, Event{Type: "agent_reasoning", Data: map[string]any{"text": text, "reasoning": true}})
// codex_appserver.go:536-548
	case "reasoning":
		if method != "item/completed" {
			return nil
		}
		text := codexReasoningText(item)
		if text == "" {
			return nil
		}
		session.flushPending(false)
		if session.note != nil {
			session.note(text)
		}
		session.emit(Event{Type: "agent_reasoning", Data: map[string]any{"text": text, "reasoning": true}})
// dsh.go:521-525
	case "thinking":
		text, _ := payload["text"].(string)
		if output := dshReasoningText(text); output != "" {
			return Event{Type: "agent_reasoning", Data: map[string]any{"text": output, "reasoning": true}}, "", ""
		}
// app/service.go:2997-3001
	case "agent_reasoning":
		category = domain.ConversationCategoryUpdate
		kind = domain.ConversationKindReasoning
		summary = firstText(event.Data, "text")
		durableUpdate = true
```

### 3.5 历史数据里的 kind
**git 历史（静态证据）**：
- `git log -S'agent_reasoning_update'` 显示，reasoning 的落库路径从 672dab9（2026-10-24）引入时起就是 `case "agent_reasoning": kind = "agent_reasoning_update"`。
- 672dab9 之前的 service.go 没有任何 reasoning 投影：`git show 672dab9^:internal/app/service.go | grep reasoning` 只命中 `reasoning_effort` 和 `reasoning_output_tokens`。
- legacyimport 不写 reasoning 类会话行。

**生产库只读聚合：没执行**
- 环境缺口：本机是构建服务器（AHA2-BUILDER-SERVER），找不到任何 aha2.db（`find / -xdev -name aha2.db`，排除其他 Task worktree，结果为空），也没有运行中的 AHA 服务。生产在 owner 的 Windows 机器上。
- 所以不能断言“生产历史只有一种 kind”，只能说代码和 git 历史里只有这一个来源。
- 可执行的只读聚合（只看 category 和 kind 名称，不读正文和凭据；路径请替换为生产数据目录下的 aha2.db；建议先用官方备份或复制件执行）：
```sh
sqlite3 "file:/path/to/aha2.db?mode=ro" <<'SQL'
-- 1) update 下各 kind 的行数
SELECT kind, COUNT(*) FROM conversation_items WHERE category='update' GROUP BY kind ORDER BY 2 DESC;
-- 2) 任何类别里名字像思考的 kind（检查是否有别名或旧名）
SELECT category, kind, COUNT(*) FROM conversation_items
 WHERE lower(kind) LIKE '%reason%' OR lower(kind) LIKE '%think%' GROUP BY category, kind;
-- 3) 只读镜像里的同样统计
SELECT json_extract(payload_json,'$.category') AS category, json_extract(payload_json,'$.kind') AS kind, COUNT(*)
  FROM sync_remote_task_objects WHERE object_type='conversation' GROUP BY 1,2 ORDER BY 3 DESC;
SQL
```
- 判读：查询 2 应该只出现 `update | agent_reasoning_update`。如果出现别的 kind，会归到 Agent Update，Thinking 里看不到，需要再决定是否把它加进 `ConversationKindReasoning` 的匹配范围。

## 4. 缺口与风险
- 没跑 Go 全仓和 web 全量浏览器套件；手机端只用无头 Chromium 模拟视口，没上真机。
- 生产库 kind 聚合没执行，原因见 §3.5。
- task_pet 的 idle 通知行为有变化，见 §3.3，等评审判断。
- 镜像的 sequence 由 `ROW_NUMBER() OVER (ORDER BY created_at, object_id)` 生成。如果晚同步进来的对象 created_at 更早，已有行的 sequence 会整体后移。这是既有设计，本轮没动；S1 的修复保证在 sequence 不变的前提下增量读不漏。
- 快速连续切换筛选时的既有乱序竞态本轮没改，也没加重。

## 5. 完整增量 diff：`git diff 7ed0479..ccd0a6d -- . ':!docs'`
```diff
diff --git a/internal/httpapi/conversation_categories_test.go b/internal/httpapi/conversation_categories_test.go
new file mode 100644
index 0000000..b0dfe36
--- /dev/null
+++ b/internal/httpapi/conversation_categories_test.go
@@ -0,0 +1,322 @@
+package httpapi
+
+import (
+	"context"
+	"encoding/json"
+	"fmt"
+	"net/http"
+	"net/http/cookiejar"
+	"net/http/httptest"
+	"net/url"
+	"path/filepath"
+	"strings"
+	"testing"
+	"time"
+
+	"github.com/ChinaKai/AHA2/internal/app"
+	"github.com/ChinaKai/AHA2/internal/auth"
+	"github.com/ChinaKai/AHA2/internal/domain"
+	"github.com/ChinaKai/AHA2/internal/store"
+)
+
+func TestConversationCategoriesQueryNormalization(t *testing.T) {
+	t.Parallel()
+	cases := []struct {
+		raw  string
+		want string
+		ok   bool
+	}{
+		{"", "[]", true},
+		{" , ,", "[]", true},
+		{"chat,update", "[chat update]", true},
+		{"THINKING", "[thinking]", true},
+		{" Update , thinking ,update", "[update thinking]", true},
+		{"nope,tool", "[tool]", true},
+		{"nope", "", false},
+		{"nope, Other", "", false},
+	}
+	for _, tc := range cases {
+		got, ok := conversationCategoriesQuery(tc.raw)
+		if ok != tc.ok || (ok && fmt.Sprint(got) != tc.want) {
+			t.Fatalf("%q: got %v ok=%t, want %s ok=%t", tc.raw, got, ok, tc.want, tc.ok)
+		}
+	}
+}
+
+type categoryQueryCase struct {
+	query  string
+	status int
+	want   string
+}
+
+// The same normalization runs for a local Task and a read-only mirror. The
+// only intended difference is the default for a missing/blank value: every
+// category locally, the readable set (no tool) on a mirror.
+func TestConversationCategoriesLocalAndMirrorAgree(t *testing.T) {
+	ctx := context.Background()
+	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
+	if err != nil {
+		t.Fatal(err)
+	}
+	defer database.Close()
+	now := time.Now().UTC().Truncate(time.Second)
+	rows := []struct{ id, category, kind string }{
+		{"cc1-chat", "chat", "agent_message"},
+		{"cc2-update", "update", "agent_progress"},
+		{"cc3-thinking", "update", domain.ConversationKindReasoning},
+		{"cc4-tool", "tool", "agent_command_finished"},
+		{"cc5-error", "error", "agent_error"},
+	}
+
+	local := createHardwareAPITask(t, database)
+	for index, row := range rows {
+		if _, err := database.AddConversationItem(ctx, domain.ConversationItem{
+			ID: row.id, TaskID: local.ID, AgentID: "main", StreamAgentID: "main",
+			Category: row.category, Kind: row.kind, Summary: row.id, CreatedAt: now.Add(time.Duration(index) * time.Second),
+		}); err != nil {
+			t.Fatal(err)
+		}
+	}
+
+	project := domain.Project{ID: "project-categories", Name: "Remote", CreatedAt: now, UpdatedAt: now}
+	if err := database.CreateProject(ctx, project); err != nil {
+		t.Fatal(err)
+	}
+	source := domain.Task{ID: "source-categories", ProjectID: project.ID, Title: "Remote categories", Status: domain.TaskWaitingUser, CreatedAt: now, UpdatedAt: now}
+	putRemoteTaskObject(t, database, store.RemoteTaskObject{OwnerDeviceID: "remote-categories-owner", ObjectType: "task", ObjectID: source.ID, TaskID: source.ID, ProjectID: project.ID, Payload: mustJSON(t, source), CreatedAt: now, UpdatedAt: now})
+	for index, row := range rows {
+		item := domain.ConversationItem{
+			ID: row.id, TaskID: source.ID, AgentID: "main", StreamAgentID: "main",
+			Category: row.category, Kind: row.kind, Summary: row.id, CreatedAt: now.Add(time.Duration(index) * time.Second),
+		}
+		putRemoteTaskObject(t, database, store.RemoteTaskObject{
+			OwnerDeviceID: "remote-categories-owner", ObjectType: "conversation", ObjectID: item.ID, TaskID: source.ID,
+			ProjectID: project.ID, Payload: mustJSON(t, item), CreatedAt: item.CreatedAt, UpdatedAt: item.CreatedAt,
+		})
+	}
+	mirrors, err := database.RemoteTaskMirrors(ctx, "")
+	if err != nil || len(mirrors) != 1 {
+		t.Fatalf("mirrors=%d err=%v", len(mirrors), err)
+	}
+	mirrorID := mirrors[0].Task.ID
+
+	authService := auth.NewService(database, "setup-test", time.Hour)
+	server := httptest.NewServer(New(Config{Store: database, Auth: authService, App: app.NewService(database, nil, app.StubExecutor{})}).Handler())
+	defer server.Close()
+	jar, _ := cookiejar.New(nil)
+	client := &http.Client{Jar: jar}
+	_ = registerOwner(t, client, server.URL)
+
+	shared := []categoryQueryCase{
+		{"categories=chat,error,update", http.StatusOK, "[cc1-chat cc2-update cc5-error]"},
+		{"categories=THINKING", http.StatusOK, "[cc3-thinking]"},
+		{"categories=%20Update%20,Thinking", http.StatusOK, "[cc2-update cc3-thinking]"},
+		{"categories=nope,tool", http.StatusOK, "[cc4-tool]"},
+		{"categories=nope,Thinking,", http.StatusOK, "[cc3-thinking]"},
+		{"categories=nope", http.StatusBadRequest, ""},
+		{"categories=NOPE,other", http.StatusBadRequest, ""},
+	}
+	localCases := append([]categoryQueryCase{
+		{"", http.StatusOK, "[cc1-chat cc2-update cc3-thinking cc4-tool cc5-error]"},
+		{"categories=", http.StatusOK, "[cc1-chat cc2-update cc3-thinking cc4-tool cc5-error]"},
+		{"categories=,%20,", http.StatusOK, "[cc1-chat cc2-update cc3-thinking cc4-tool cc5-error]"},
+	}, shared...)
+	mirrorCases := append([]categoryQueryCase{
+		{"", http.StatusOK, "[cc1-chat cc2-update cc3-thinking cc5-error]"},
+		{"categories=", http.StatusOK, "[cc1-chat cc2-update cc3-thinking cc5-error]"},
+		{"categories=,%20,", http.StatusOK, "[cc1-chat cc2-update cc3-thinking cc5-error]"},
+	}, shared...)
+	for name, target := range map[string]struct {
+		taskID string
+		cases  []categoryQueryCase
+	}{"local": {local.ID, localCases}, "mirror": {mirrorID, mirrorCases}} {
+		for _, tc := range target.cases {
+			status, ids := conversationIDs(t, client, server.URL, target.taskID, "limit=50&"+tc.query, "cc")
+			if status != tc.status || (status == http.StatusOK && ids != tc.want) {
+				t.Fatalf("%s %q: status=%d ids=%s, want %d %s", name, tc.query, status, ids, tc.status, tc.want)
+			}
+		}
+	}
+}
+
+// Older synced payloads may lack "kind" or carry null. They are not
+// reasoning rows, so they stay in Agent Update and never reach Thinking.
+func TestRemoteConversationMissingKindStaysInUpdate(t *testing.T) {
+	ctx := context.Background()
+	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
+	if err != nil {
+		t.Fatal(err)
+	}
+	defer database.Close()
+	now := time.Now().UTC().Truncate(time.Second)
+	project := domain.Project{ID: "project-missing-kind", Name: "Remote", CreatedAt: now, UpdatedAt: now}
+	if err := database.CreateProject(ctx, project); err != nil {
+		t.Fatal(err)
+	}
+	source := domain.Task{ID: "source-missing-kind", ProjectID: project.ID, Title: "Missing kind", Status: domain.TaskWaitingUser, CreatedAt: now, UpdatedAt: now}
+	owner := "remote-missing-kind-owner"
+	putRemoteTaskObject(t, database, store.RemoteTaskObject{OwnerDeviceID: owner, ObjectType: "task", ObjectID: source.ID, TaskID: source.ID, ProjectID: project.ID, Payload: mustJSON(t, source), CreatedAt: now, UpdatedAt: now})
+	payloads := []struct {
+		id      string
+		payload string
+	}{
+		{"mk1-nokind", `{"id":"mk1-nokind","task_id":"source-missing-kind","agent_id":"main","category":"update","summary":"no kind"}`},
+		{"mk2-nullkind", `{"id":"mk2-nullkind","task_id":"source-missing-kind","agent_id":"main","category":"update","kind":null,"summary":"null kind"}`},
+		{"mk3-thinking", `{"id":"mk3-thinking","task_id":"source-missing-kind","agent_id":"main","category":"update","kind":"agent_reasoning_update","summary":"thinking"}`},
+	}
+	for index, row := range payloads {
+		at := now.Add(time.Duration(index) * time.Second)
+		putRemoteTaskObject(t, database, store.RemoteTaskObject{
+			OwnerDeviceID: owner, ObjectType: "conversation", ObjectID: row.id, TaskID: source.ID,
+			ProjectID: project.ID, Payload: json.RawMessage(row.payload), CreatedAt: at, UpdatedAt: at,
+		})
+	}
+	mirrors, err := database.RemoteTaskMirrors(ctx, "")
+	if err != nil || len(mirrors) != 1 {
+		t.Fatalf("mirrors=%d err=%v", len(mirrors), err)
+	}
+	for query, want := range map[string]string{
+		"update":          "[mk1-nokind mk2-nullkind]",
+		"thinking":        "[mk3-thinking]",
+		"update,thinking": "[mk1-nokind mk2-nullkind mk3-thinking]",
+	} {
+		page, err := database.RemoteConversationPageForAgent(ctx, mirrors[0].Task.ID, "main", 0, 0, 50, strings.Split(query, ","))
+		if err != nil {
+			t.Fatal(err)
+		}
+		ids := []string{}
+		for _, item := range page.Items {
+			ids = append(ids, item.ID)
+		}
+		if fmt.Sprint(ids) != want {
+			t.Fatalf("%s: got %v want %s", query, ids, want)
+		}
+	}
+}
+
+// A filtered incremental read on a mirror must advance only through the rows
+// it returned. limit=1 rounds walk every matching row exactly once, also after
+// new rows sync in, and a read with nothing new keeps the cursor in place.
+func TestRemoteConversationIncrementalCursorDoesNotSkipRows(t *testing.T) {
+	ctx := context.Background()
+	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
+	if err != nil {
+		t.Fatal(err)
+	}
+	defer database.Close()
+	now := time.Now().UTC().Truncate(time.Second)
+	project := domain.Project{ID: "project-incremental", Name: "Remote", CreatedAt: now, UpdatedAt: now}
+	if err := database.CreateProject(ctx, project); err != nil {
+		t.Fatal(err)
+	}
+	source := domain.Task{ID: "source-incremental", ProjectID: project.ID, Title: "Incremental", Status: domain.TaskWaitingUser, CreatedAt: now, UpdatedAt: now}
+	owner := "remote-incremental-owner"
+	putRemoteTaskObject(t, database, store.RemoteTaskObject{OwnerDeviceID: owner, ObjectType: "task", ObjectID: source.ID, TaskID: source.ID, ProjectID: project.ID, Payload: mustJSON(t, source), CreatedAt: now, UpdatedAt: now})
+	cycle := []struct{ bucket, category, kind string }{
+		{"chat", "chat", "agent_message"},
+		{"update", "update", "agent_progress"},
+		{"thinking", "update", domain.ConversationKindReasoning},
+		{"tool", "tool", "agent_command_finished"},
+		{"thinking", "update", domain.ConversationKindReasoning},
+		{"update", "update", "agent_message_update"},
+		{"error", "error", "agent_error"},
+	}
+	count := 0
+	add := func(n int) {
+		for index := 0; index < n; index++ {
+			count++
+			row := cycle[(count-1)%len(cycle)]
+			item := domain.ConversationItem{
+				ID: fmt.Sprintf("in%02d-%s", count, row.bucket), TaskID: source.ID, AgentID: "main", StreamAgentID: "main",
+				Category: row.category, Kind: row.kind, Summary: row.bucket, CreatedAt: now.Add(time.Duration(count) * time.Second),
+			}
+			putRemoteTaskObject(t, database, store.RemoteTaskObject{
+				OwnerDeviceID: owner, ObjectType: "conversation", ObjectID: item.ID, TaskID: source.ID,
+				ProjectID: project.ID, Payload: mustJSON(t, item), CreatedAt: item.CreatedAt, UpdatedAt: item.CreatedAt,
+			})
+		}
+	}
+	add(14)
+	mirrors, err := database.RemoteTaskMirrors(ctx, "")
+	if err != nil || len(mirrors) != 1 {
+		t.Fatalf("mirrors=%d err=%v", len(mirrors), err)
+	}
+	mirrorID := mirrors[0].Task.ID
+	expect := func(from int, buckets ...string) string {
+		want := map[string]bool{}
+		for _, bucket := range buckets {
+			want[bucket] = true
+		}
+		ids := []string{}
+		for index := from + 1; index <= count; index++ {
+			row := cycle[(index-1)%len(cycle)]
+			if want[row.bucket] {
+				ids = append(ids, fmt.Sprintf("in%02d-%s", index, row.bucket))
+			}
+		}
+		return fmt.Sprint(ids)
+	}
+	walk := func(categories []string, cursor int64) ([]string, int64) {
+		t.Helper()
+		ids := []string{}
+		for guard := 0; guard < 40; guard++ {
+			page, err := database.RemoteConversationPageForAgent(ctx, mirrorID, "main", 0, cursor, 1, categories)
+			if err != nil {
+				t.Fatal(err)
+			}
+			if page.Latest < cursor {
+				t.Fatalf("%v: cursor moved back from %d to %d", categories, cursor, page.Latest)
+			}
+			for _, item := range page.Items {
+				ids = append(ids, item.ID)
+			}
+			cursor = page.Latest
+			if len(page.Items) == 0 {
+				return ids, cursor
+			}
+		}
+		t.Fatalf("%v: incremental walk did not finish", categories)
+		return nil, cursor
+	}
+	for _, categories := range [][]string{{"update"}, {"thinking"}, {"update", "thinking"}, {"chat", "error", "update"}} {
+		ids, cursor := walk(categories, 1)
+		if fmt.Sprint(ids) != expect(1, categories...) {
+			t.Fatalf("%v: walked %v want %s", categories, ids, expect(1, categories...))
+		}
+		// Nothing new: the cursor stays where it is.
+		again, stable := walk(categories, cursor)
+		if len(again) != 0 || stable != cursor {
+			t.Fatalf("%v: empty read returned %v and moved cursor %d -> %d", categories, again, cursor, stable)
+		}
+		// New rows sync in later; the next walk picks up exactly the new ones.
+		before := count
+		add(7)
+		more, _ := walk(categories, cursor)
+		if fmt.Sprint(more) != expect(before, categories...) {
+			t.Fatalf("%v: after sync walked %v want %s", categories, more, expect(before, categories...))
+		}
+	}
+}
+
+func conversationIDs(t *testing.T, client *http.Client, baseURL, taskID, query, prefix string) (int, string) {
+	t.Helper()
+	endpoint := baseURL + "/api/v1/tasks/" + url.PathEscape(taskID) + "/agents/main/conversation?" + query
+	response := requestJSON(t, client, http.MethodGet, endpoint, nil, "")
+	defer response.Body.Close()
+	if response.StatusCode != http.StatusOK {
+		return response.StatusCode, ""
+	}
+	var body struct {
+		Conversation domain.ConversationPage `json:"conversation"`
+	}
+	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
+		t.Fatal(err)
+	}
+	ids := []string{}
+	for _, item := range body.Conversation.Items {
+		if strings.HasPrefix(item.ID, prefix) {
+			ids = append(ids, item.ID)
+		}
+	}
+	return response.StatusCode, fmt.Sprint(ids)
+}
diff --git a/internal/httpapi/task_handlers.go b/internal/httpapi/task_handlers.go
index 1b41e44..9f1e254 100644
--- a/internal/httpapi/task_handlers.go
+++ b/internal/httpapi/task_handlers.go
@@ -643,19 +643,47 @@ func (s *Server) agentConversation(writer http.ResponseWriter, request *http.Req
 	s.conversationForAgent(writer, request, request.PathValue("agent"))
 }
 
-func (s *Server) conversationForAgent(writer http.ResponseWriter, request *http.Request, agentID string) {
-	taskID := request.PathValue("id")
+// conversationCategoriesQuery normalizes the conversation "categories" query
+// value. Names are trimmed and lower-cased; duplicates collapse. A missing or
+// blank value (including bare commas) returns no categories, which means the
+// endpoint default: every category for a local Task, the readable set for a
+// read-only mirror. Unknown names next to at least one known name are ignored,
+// so a newer client's extra name narrows nothing it did not ask for. A value
+// naming only unknown categories is rejected (ok=false) instead of widening to
+// every category, tool rows included.
+func conversationCategoriesQuery(raw string) ([]string, bool) {
 	categories := []string{}
-	categoryQuery := request.URL.Query().Get("categories")
-	for _, value := range strings.Split(categoryQuery, ",") {
-		value = strings.TrimSpace(value)
+	seen := map[string]bool{}
+	named := false
+	for _, value := range strings.Split(raw, ",") {
+		value = strings.ToLower(strings.TrimSpace(value))
+		if value == "" {
+			continue
+		}
+		named = true
 		switch value {
 		case "chat", "update", "thinking", "tool", "error", "mail":
-			categories = append(categories, value)
+			if !seen[value] {
+				seen[value] = true
+				categories = append(categories, value)
+			}
 		}
 	}
+	if named && len(categories) == 0 {
+		return nil, false
+	}
+	return categories, true
+}
+
+func (s *Server) conversationForAgent(writer http.ResponseWriter, request *http.Request, agentID string) {
+	taskID := request.PathValue("id")
+	categories, ok := conversationCategoriesQuery(request.URL.Query().Get("categories"))
+	if !ok {
+		writeError(writer, http.StatusBadRequest, "invalid_categories")
+		return
+	}
 	if _, err := s.store.Task(request.Context(), taskID); err != nil {
-		if strings.TrimSpace(categoryQuery) == "" {
+		if len(categories) == 0 {
 			// "update" no longer includes reasoning rows; keep the historical
 			// no-filter default by naming thinking explicitly.
 			categories = []string{"chat", "update", "thinking", "error", "mail"}
diff --git a/internal/store/conversation.go b/internal/store/conversation.go
index 1faa713..5ce8ed8 100644
--- a/internal/store/conversation.go
+++ b/internal/store/conversation.go
@@ -550,7 +550,9 @@ func (s *Store) ConversationPage(
 // selects only them, and both together select every update row. The filter
 // is applied in SQL, so LIMIT, has_more, next_before and the incremental
 // latest_sequence all describe exactly the rows the caller asked for.
-// An empty or fully unknown list returns no predicate (all categories).
+// An empty list returns no predicate (all categories). Any other name is
+// matched literally against the stored category, so an unknown name matches
+// no row; the HTTP layer validates and normalizes names before calling.
 func conversationCategoryClause(categories []string, categoryColumn, kindColumn string) (string, []any) {
 	plain := make([]string, 0, len(categories))
 	seen := map[string]bool{}
diff --git a/internal/store/sync_graph.go b/internal/store/sync_graph.go
index 2102f44..ed0c7d3 100644
--- a/internal/store/sync_graph.go
+++ b/internal/store/sync_graph.go
@@ -238,6 +238,20 @@ func (s *Store) RemoteConversationPageForAgent(
 	if len(items) > 0 {
 		page.NextBefore = items[0].Sequence
 	}
+	if after > 0 {
+		// An incremental read advances only through the rows it returned, like
+		// the local store: a filtered page cut by LIMIT must leave the rest of
+		// its range reachable by the next after-request.
+		page.Latest = after
+		for _, item := range items {
+			if item.Sequence > page.Latest {
+				page.Latest = item.Sequence
+			}
+		}
+		return page, nil
+	}
+	// Only the newest page's Latest seeds incremental reads, and that page
+	// already holds the newest matching rows, so the mirror length is safe.
 	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_remote_task_objects WHERE `+strings.Join(baseWhere, " AND "), baseArgs...).Scan(&page.Latest)
 	return page, nil
 }
diff --git a/web/src/styles.css b/web/src/styles.css
index b1bf5ed..193af10 100644
--- a/web/src/styles.css
+++ b/web/src/styles.css
@@ -3232,6 +3232,9 @@ dialog.pet-approval-detail-dialog::backdrop, dialog.pet-approval-filter-dialog::
   /* The composer establishes its own stacking context, so the attachment tray
      cannot rise above the FAB. Hide the FAB while drafts are attached. */
   body:has(.composer-attachment-tray) .task-search-fab { display: none; }
+  /* Same stacking limit for the open message-filter menu: its rows reach the
+     FAB's corner, so the FAB steps aside until the menu closes. */
+  body:has(.conversation-filter-popover[open]) .task-search-fab { display: none; }
 }
 
 /* MCP page: the four-column line needs the full 1041px desktop width. Between
diff --git a/web/tests/task_conversation_thinking_filter.browser.mjs b/web/tests/task_conversation_thinking_filter.browser.mjs
index 1ed868b..fc2bd91 100644
--- a/web/tests/task_conversation_thinking_filter.browser.mjs
+++ b/web/tests/task_conversation_thinking_filter.browser.mjs
@@ -8,6 +8,12 @@
 // default 3/5 without Thinking, Thinking alone/combined, "load older" paging
 // with no gaps or duplicates, and SSE-driven incremental refresh appending only
 // the selected categories.
+//
+// Mobile (task-724 review M1): the open filter menu reaches the floating
+// search button's corner on phones. Every filter button's right edge must hit
+// the button itself and a click there must toggle the filter, never open the
+// command palette. en-US: the "N loaded" count updated after load-older goes
+// through the existing i18n pattern.
 import assert from "node:assert/strict";
 import {createServer} from "node:http";
 import {readFile} from "node:fs/promises";
@@ -83,7 +89,7 @@ window.fetch = async (input, init = {}) => {
   const path = url.pathname;
   const method = (init.method || "GET").toUpperCase();
   if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
-  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: "zh-CN"}});
+  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: window.__ahaLanguage || "zh-CN"}});
   if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", nickname: "AHA", enabled: false}, messages: [], models: [], tools: []});
   if (path === "/api/v1/projects") return json({projects: [{id: "project-1", name: "Project", project_type: "folder", channel_retired: false}], has_more: false});
   if (path === "/api/v1/workspaces") return json({workspaces: [{id: "workspace-1", project_id: "project-1", name: "W", root_path: "/tmp", transport: "native", health: "ready"}]});
@@ -286,6 +292,79 @@ try {
   assert.ok(!updateLive.mounted.includes("r160-thinking"), "a live Thinking row leaked into the Agent Update filter");
   assert.deepEqual(updateLive.buckets, ["update"]);
 
+  assert.deepEqual(errors, []);
+
+  // Phones: the FAB steps aside while the menu is open; every button's right
+  // edge is hit-testable and a right-edge click only toggles that filter.
+  for (const viewport of [{width: 360, height: 740}, {width: 390, height: 844}, {width: 414, height: 896}]) {
+    const phone = await browser.newPage({viewport});
+    phone.on("pageerror", error => errors.push(`${viewport.width}: ${error.message}`));
+    await phone.goto(url, {waitUntil: "domcontentloaded"});
+    await phone.waitForFunction(() => window.__requests.length > 0 && document.querySelector(".conversation-filter-trigger"), null, {timeout: 20000});
+    const fabVisible = () => phone.evaluate(() => {
+      const fab = document.querySelector(".task-search-fab");
+      return Boolean(fab) && getComputedStyle(fab).display !== "none";
+    });
+    assert.ok(await fabVisible(), `${viewport.width}: the search FAB must stay available while the menu is closed`);
+    let selected = ["chat", "error", "update"];
+    for (const category of ["chat", "update", "thinking", "tool", "error"]) {
+      // The menu may stay open across the re-render after a toggle; open it
+      // with a real tap only when it is closed.
+      if (!(await phone.evaluate(() => Boolean(document.querySelector(".conversation-filter-popover")?.open)))) {
+        await phone.click(".conversation-filter-trigger");
+      }
+      await phone.waitForSelector(`[data-conversation-category="${category}"]`, {state: "visible", timeout: 5000});
+      assert.ok(!(await fabVisible()), `${viewport.width}: the search FAB still shows over the open menu`);
+      const hits = await phone.evaluate(() => [...document.querySelectorAll("[data-conversation-category]")].map(button => {
+        const rect = button.getBoundingClientRect();
+        // Sample the whole straight part of the right edge, 1px inside it;
+        // the rounded corners are outside the button's hit area by design.
+        const radius = parseFloat(getComputedStyle(button).borderTopRightRadius) || 0;
+        const ys = [];
+        for (let y = rect.top + radius + 1; y <= rect.bottom - radius - 1; y += 2) ys.push(y);
+        const covered = ys.filter(y => {
+          const hit = document.elementFromPoint(rect.right - 1, y);
+          return !(hit && (hit === button || button.contains(hit)));
+        }).map(y => {
+          const hit = document.elementFromPoint(rect.right - 1, y);
+          return `${Math.round(y)}:${hit ? hit.className || hit.tagName : "none"}`;
+        });
+        return {category: button.dataset.conversationCategory, samples: ys.length, covered, inside: rect.right <= innerWidth && rect.left >= 0};
+      }));
+      for (const hit of hits) {
+        assert.ok(hit.inside, `${viewport.width}: ${hit.category} button leaves the viewport`);
+        assert.ok(hit.samples >= 8, `${viewport.width}: ${hit.category} right edge too short to sample (${hit.samples})`);
+        assert.deepEqual(hit.covered, [], `${viewport.width}: ${hit.category} right edge is covered`);
+      }
+      const box = await phone.locator(`[data-conversation-category="${category}"]`).boundingBox();
+      const before = await phone.evaluate(() => window.__requests.length);
+      await phone.mouse.click(box.x + box.width - 2, box.y + box.height / 2);
+      selected = selected.includes(category) ? selected.filter(item => item !== category) : [...selected, category].sort();
+      await phone.waitForFunction(({count, expected}) => {
+        const request = window.__requests.at(-1);
+        return window.__requests.length > count && request && [...request.categories].sort().join(",") === expected.join(",");
+      }, {count: before, expected: selected}, {timeout: 10000});
+      assert.equal(await phone.evaluate(() => Boolean(document.querySelector("#command-palette[open]"))), false, `${viewport.width}: clicking ${category} opened the search palette`);
+      await phone.waitForFunction(expected => [...document.querySelectorAll("[data-conversation-category].active")]
+        .map(node => node.dataset.conversationCategory).sort().join(",") === expected.join(","), selected, {timeout: 10000});
+    }
+    await phone.evaluate(() => { const popover = document.querySelector(".conversation-filter-popover"); if (popover) popover.open = false; });
+    assert.ok(await fabVisible(), `${viewport.width}: the search FAB must come back after the menu closes`);
+    await phone.close();
+  }
+
+  // en-US: the loaded count rewritten after "load older" is translated.
+  const english = await browser.newPage({viewport: {width: 1280, height: 800}});
+  await english.addInitScript(() => { window.__ahaLanguage = "en-US"; });
+  english.on("pageerror", error => errors.push(`en-US: ${error.message}`));
+  await english.goto(url, {waitUntil: "domcontentloaded"});
+  await english.waitForSelector("#load-older-conversation", {timeout: 20000});
+  await english.evaluate(() => { const popover = document.querySelector(".conversation-filter-popover"); if (popover) popover.open = true; });
+  assert.equal((await english.textContent("#conversation-filter-loaded"))?.trim(), "20 loaded");
+  await english.click("#load-older-conversation");
+  await english.waitForFunction(() => document.querySelector("#conversation-filter-loaded")?.textContent?.trim() === "70 loaded", null, {timeout: 10000});
+  await english.close();
+
   assert.deepEqual(errors, []);
   console.log("task_conversation_thinking_filter: ok");
 } finally {
```
