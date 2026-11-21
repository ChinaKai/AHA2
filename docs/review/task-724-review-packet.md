# task-724 review packet：Thinking 独立筛选，默认显示 Agent Update

- base a3ed6cb → commit 7ed0479，分支 aha/task_dd1b8666a866ea43845f
- worktree .aha2-worktrees/task_dd1b8666a866ea43845f（未 push/merge）

## 改动与理由
1. 真实事件语义：claude.go / codex.go / codex_appserver.go / dsh.go 的 thinking/reasoning 块都发 agent_reasoning 执行事件；recordExecutionEvent 落库为 category=update、kind=agent_reasoning_update。没有别的 reasoning 落库路径。
2. Thinking 只存在于查询层，不加新的存储类别：store.conversationCategoryClause 按 kind 拆开 update。只选 update → (category='update' AND kind<>reasoning)；只选 thinking → (category='update' AND kind=reasoning)；两个都选 → category IN(...,'update')。本地 ConversationPageForAgent 和只读镜像 RemoteConversationPageForAgent（json_extract $.kind）用同一个谓词，所以 LIMIT/has_more/next_before/latest_sequence 都只算筛选后的行。旧记录不重写，数据保留策略（按存储 category）不受影响。
3. HTTP 接受 thinking。只读镜像在没带 categories 时原默认 chat,update,error,mail（update 当时包含 reasoning），现在显式补上 thinking，让不带筛选的调用方行为不变。返回的 item.category 仍是 update，渲染层继续靠 kind 显示“思考”徽标，不改渲染。
4. 前端：ConversationCategory 加 thinking；默认 {chat,update,thinking:false,tool:false,error}；按钮顺序 对话/Agent Update/Thinking/工具调用/错误（在 Update 后插一项）；计数分母从写死的 /4 改为标签数。没有筛选偏好持久化（只在内存 state），所以不加偏好兼容逻辑。
5. 顺手修复一个既有问题（在本需求的计数链路上）：loadOlderConversation 更新计数用的 #conversation-filter-count 不存在（实际是 #conversation-filter-loaded），点“加载更早记录”后已加载计数不会变。新浏览器测试覆盖了这条。

## 测试契约改动说明（没删断言）
- build.test.mjs：原来“恰好 4 类 / enabled}/4 / 默认 update:false”的断言改成 5 类 / 分母从标签表算 / 默认 update:true,thinking:false；仍保留“没有 mail”的断言。
- task_readonly_filter / task_round_activity：原 fixture 里的“update row”其实是 kind=agent_reasoning_update，按新语义属于 Thinking。改成 agent_progress，另加一条 thinking 行；fixture 的桩按服务端规则拆分。步骤和一致性断言全部保留，并扩展到 5 类。

## 命令与退出码（都在本 worktree 执行）
- go build ./... → 0
- go vet ./internal/store ./internal/httpapi ./internal/app ./internal/domain → 0
- go test ./internal/store -run 'Conversation|Remote' -count=1 → 0 (ok 134.9s)
- go test ./internal/httpapi -run 'Conversation|Remote|TaskDetailPerf' -count=1 → 0 (ok 134.7s)
- go test ./internal/app -run TestMultiAgentRoundCreatesIntegrationTurn -count=1 → 0（新增断言：真实 agent_reasoning 投影走 thinking 筛选、不走 update 筛选）
- (web) npm ci；node scripts/build-web.mjs → 0（首屏 gzip 预算断言在 build.test 内通过）
- (web) npm test → 0（619 pass / 0 fail）
- tsc --noEmit --strict（scratch 安装的 typescript，仓库没有 tsconfig）：HEAD 和 base 都是 exit 1、226 条，归一化行号后集合完全相同，没有新增类型错误
- 浏览器（scratch playwright + chromium-headless-shell-1255，AHA_PLAYWRIGHT_MODULE/AHA_BROWSER_EXECUTABLE 覆盖口）：
  task_readonly_filter → 0；task_round_activity → 0；新 task_conversation_thinking_filter → 0
- 变异验证（改后已还原并重建）：
  thinking 谓词 = 改成 <> → TestConversationPageSplitsThinkingFromUpdate FAIL；
  镜像 kind 列换成常量 → TestRemoteTaskConversationSplitsThinkingFromUpdate FAIL；
  恢复 #conversation-filter-count → 新浏览器测试在加载更早后的计数处超时 FAIL；
  默认 thinking:true → 新浏览器测试默认选中断言 FAIL。
- UI 冒烟：1280×800 / 390×844 截图，5 个按钮两列三行，菜单不越出视口。

## 覆盖
- store：11 行混合 fixture × 8 种筛选组合的精确序列；4 种筛选按 limit=1 逐页向前翻，不漏不重；after 增量：thinking 用 limit=1 两次取完，has_more/latest 正确；默认筛选的增量不混入新的 thinking/tool。
- 镜像 HTTP：5 种 query 的精确 id；thinking limit=1 + before 翻页。
- 新浏览器测试（真实 task，非只读）：150 行历史；默认 3/5 且挂载行里没有 thinking；标签顺序；只选 Thinking 时 20 条 + 加载更早 → 25 条，序列完整、加载按钮消失；SSE update 事件触发增量请求 categories=[thinking]、after=147，只追加新的 thinking 行；Update+Thinking 组合取最新 20 条，精确序列无重复；组合状态下的增量两类各追加一次；只选 Update 时新的 thinking 不会漏进来。

## 缺口 / 风险
- 未跑 Go 全仓与 web 全量浏览器套件；i18n 扫描未跑（新标签 Thinking 为 ASCII）。
- 外部 API 语义变化：显式传 categories=update 的调用方（包括旧版 Web）不再拿到 reasoning 行，这就是需求本身。仓库里没有其他传 categories 的调用方（channels.ts 不带 categories）。
- 移动端右下角的悬浮搜索按钮会盖住筛选菜单第一行右格，属于既有布局问题（改动前同一位置是 Agent Update），本次没处理。
- 快速连续切换筛选时的既有乱序竞态本次未改，也未加重。

## 相关调用链（HEAD，完整函数；省略处已标注）
### internal/app/service.go recordExecutionEvent 中的 reasoning 分支
```go
	case "agent_reasoning":
		category = domain.ConversationCategoryUpdate
		kind = domain.ConversationKindReasoning
		summary = firstText(event.Data, "text")
		durableUpdate = true
```
### internal/httpapi/task_handlers.go conversationForAgent
```go
func (s *Server) conversationForAgent(writer http.ResponseWriter, request *http.Request, agentID string) {
	taskID := request.PathValue("id")
	categories := []string{}
	categoryQuery := request.URL.Query().Get("categories")
	for _, value := range strings.Split(categoryQuery, ",") {
		value = strings.TrimSpace(value)
		switch value {
		case "chat", "update", "thinking", "tool", "error", "mail":
			categories = append(categories, value)
		}
	}
	if _, err := s.store.Task(request.Context(), taskID); err != nil {
		if strings.TrimSpace(categoryQuery) == "" {
			// "update" no longer includes reasoning rows; keep the historical
			// no-filter default by naming thinking explicitly.
			categories = []string{"chat", "update", "thinking", "error", "mail"}
		}
		page, pageErr := s.store.RemoteConversationPageForAgent(
			request.Context(),
			taskID,
			agentID,
			queryInt(request, "before", 0),
			queryInt(request, "after", 0),
			int(queryInt(request, "limit", 50)),
			categories,
		)
		if pageErr != nil {
			writeError(writer, http.StatusNotFound, "task_not_found")
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "conversation": page})
		return
	}
	// …(本地 Task：校验 agent 后以同一 categories 调 ConversationPageForAgent，未改)…
}
```
### internal/store/conversation.go ConversationPageForAgent / sync_graph.go RemoteConversationPageForAgent
只把 category IN 换成 conversationCategoryClause，完整新谓词和调用点都在下面的生产 diff 里；排序、LIMIT+1、has_more、next_before、latest 的逻辑没改。

### web/src/main.ts（筛选状态 → 请求 → 追加/前插）
```ts
function selectedConversationCategories(): ConversationCategory[] {
  return (Object.keys(state.taskCategories) as ConversationCategory[]).filter(category => state.taskCategories[category]);
}
async function reloadConversation(): Promise<void> {
  if (!state.selectedTask) return;
  const page = await api.agentConversation(state.selectedTask.task.id, state.selectedTaskAgent, {
    limit: initialConversationPageSize,
    categories: selectedConversationCategories(),
  });
  state.taskConversation = page.conversation.items || [];
  state.taskConversationHasMore = page.conversation.has_more;
  state.taskConversationBefore = page.conversation.next_before || 0;
  state.taskConversationLatest = page.conversation.latest_sequence || 0;
}
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
      // …(task_pet 通知，未改)…
    }
    if (state.taskConversation.length > 300) {
      state.taskConversation = state.taskConversation.slice(-300);
      state.taskConversationHasMore = true;
      state.taskConversationBefore = state.taskConversation[0]?.sequence || 0;
    }
  }
  state.taskConversationLatest = Math.max(state.taskConversationLatest, page.conversation.latest_sequence || 0);
  // …(context / mail 刷新，未改)…
  document.querySelectorAll<HTMLElement>("[data-conversation-category]").forEach(button => button.addEventListener("click", async () => {
    const category = button.dataset.conversationCategory as ConversationCategory;
    state.taskCategories[category] = !state.taskCategories[category];
    if (!selectedConversationCategories().length) state.taskCategories.chat = true;
    await reloadConversation();
    render();
  }));
async function loadOlderConversation(): Promise<void> {
  if (!state.selectedTask || !state.taskConversationBefore || loadingOlderConversation) return;
  loadingOlderConversation = true;
  try {
    const page = await api.agentConversation(state.selectedTask.task.id, state.selectedTaskAgent, {
      before: state.taskConversationBefore,
      limit: 50,
      categories: selectedConversationCategories(),
    });
    const older = page.conversation.items || [];
    const seen = new Set(state.taskConversation.map(item => item.sequence));
    state.taskConversation = [...older.filter(item => !seen.has(item.sequence)), ...state.taskConversation];
    if (state.taskConversation.length > 300) {
      state.taskConversation = state.taskConversation.slice(0, 300);
    }
    state.taskConversationHasMore = page.conversation.has_more && state.taskConversation.length < 300;
    state.taskConversationBefore = page.conversation.next_before || 0;
    // …(虚拟列表 patch，未改)…
    const count = document.querySelector<HTMLElement>("#conversation-filter-loaded");
    if (count) count.textContent = `${state.taskConversation.length} 条已加载`;
  } finally {
    loadingOlderConversation = false;
  }
}
```

## 完整 diff：生产代码 (git diff -U3 a3ed6cb..HEAD)
```diff
diff --git a/internal/app/service.go b/internal/app/service.go
index 2e64c22..f61df55 100644
--- a/internal/app/service.go
+++ b/internal/app/service.go
@@ -2995,8 +2995,8 @@ func (s *Service) recordExecutionEvent(ctx context.Context, turn domain.Turn, ev
 		category = "update"
 		summary = firstText(event.Data, "message", "phase", "status")
 	case "agent_reasoning":
-		category = "update"
-		kind = "agent_reasoning_update"
+		category = domain.ConversationCategoryUpdate
+		kind = domain.ConversationKindReasoning
 		summary = firstText(event.Data, "text")
 		durableUpdate = true
 	case "agent_message":
diff --git a/internal/domain/conversation.go b/internal/domain/conversation.go
index 277f92f..c2b2232 100644
--- a/internal/domain/conversation.go
+++ b/internal/domain/conversation.go
@@ -49,6 +49,18 @@ type ConversationItem struct {
 	CreatedAt     time.Time      `json:"created_at"`
 }
 
+// Thinking is a query-only conversation filter. Model reasoning is stored as
+// category "update" with kind ConversationKindReasoning (every Backend projects
+// its thinking/reasoning blocks through the agent_reasoning execution event),
+// so history written before the filter existed is split by kind instead of
+// being rewritten. Filtering by "update" excludes these rows; "thinking"
+// selects only them.
+const (
+	ConversationCategoryUpdate   = "update"
+	ConversationCategoryThinking = "thinking"
+	ConversationKindReasoning    = "agent_reasoning_update"
+)
+
 type ConversationPage struct {
 	Items      []ConversationItem `json:"items"`
 	HasMore    bool               `json:"has_more"`
diff --git a/internal/httpapi/task_handlers.go b/internal/httpapi/task_handlers.go
index 639828f..1b41e44 100644
--- a/internal/httpapi/task_handlers.go
+++ b/internal/httpapi/task_handlers.go
@@ -650,13 +650,15 @@ func (s *Server) conversationForAgent(writer http.ResponseWriter, request *http.
 	for _, value := range strings.Split(categoryQuery, ",") {
 		value = strings.TrimSpace(value)
 		switch value {
-		case "chat", "update", "tool", "error", "mail":
+		case "chat", "update", "thinking", "tool", "error", "mail":
 			categories = append(categories, value)
 		}
 	}
 	if _, err := s.store.Task(request.Context(), taskID); err != nil {
 		if strings.TrimSpace(categoryQuery) == "" {
-			categories = []string{"chat", "update", "error", "mail"}
+			// "update" no longer includes reasoning rows; keep the historical
+			// no-filter default by naming thinking explicitly.
+			categories = []string{"chat", "update", "thinking", "error", "mail"}
 		}
 		page, pageErr := s.store.RemoteConversationPageForAgent(
 			request.Context(),
diff --git a/internal/store/conversation.go b/internal/store/conversation.go
index 904b1ab..1faa713 100644
--- a/internal/store/conversation.go
+++ b/internal/store/conversation.go
@@ -543,6 +543,60 @@ func (s *Store) ConversationPage(
 	return s.ConversationPageForAgent(ctx, taskID, "main", before, after, limit, categories)
 }
 
+// conversationCategoryClause turns the conversation filter categories into one
+// SQL predicate. Plain categories match the stored category column. "update"
+// and "thinking" share the stored "update" category and are split by the
+// reasoning kind: "update" alone excludes reasoning rows, "thinking" alone
+// selects only them, and both together select every update row. The filter
+// is applied in SQL, so LIMIT, has_more, next_before and the incremental
+// latest_sequence all describe exactly the rows the caller asked for.
+// An empty or fully unknown list returns no predicate (all categories).
+func conversationCategoryClause(categories []string, categoryColumn, kindColumn string) (string, []any) {
+	plain := make([]string, 0, len(categories))
+	seen := map[string]bool{}
+	update, thinking := false, false
+	for _, category := range categories {
+		category = strings.TrimSpace(category)
+		if category == "" || seen[category] {
+			continue
+		}
+		seen[category] = true
+		switch category {
+		case domain.ConversationCategoryUpdate:
+			update = true
+		case domain.ConversationCategoryThinking:
+			thinking = true
+		default:
+			plain = append(plain, category)
+		}
+	}
+	if update && thinking {
+		plain = append(plain, domain.ConversationCategoryUpdate)
+	}
+	terms := make([]string, 0, 2)
+	args := make([]any, 0, len(plain)+2)
+	if len(plain) > 0 {
+		placeholders := make([]string, 0, len(plain))
+		for _, category := range plain {
+			placeholders = append(placeholders, "?")
+			args = append(args, category)
+		}
+		terms = append(terms, categoryColumn+" IN ("+strings.Join(placeholders, ",")+")")
+	}
+	if update != thinking {
+		operator := "<>"
+		if thinking {
+			operator = "="
+		}
+		terms = append(terms, "("+categoryColumn+"=? AND "+kindColumn+operator+"?)")
+		args = append(args, domain.ConversationCategoryUpdate, domain.ConversationKindReasoning)
+	}
+	if len(terms) == 0 {
+		return "", nil
+	}
+	return "(" + strings.Join(terms, " OR ") + ")", args
+}
+
 func (s *Store) ConversationPageForAgent(
 	ctx context.Context,
 	taskID string,
@@ -565,13 +619,9 @@ func (s *Store) ConversationPageForAgent(
 		where = append(where, "item.sequence>?")
 		args = append(args, after)
 	}
-	if len(categories) > 0 {
-		placeholders := make([]string, 0, len(categories))
-		for _, category := range categories {
-			placeholders = append(placeholders, "?")
-			args = append(args, category)
-		}
-		where = append(where, "item.category IN ("+strings.Join(placeholders, ",")+")")
+	if clause, clauseArgs := conversationCategoryClause(categories, "item.category", "item.kind"); clause != "" {
+		where = append(where, clause)
+		args = append(args, clauseArgs...)
 	}
 	order := "DESC"
 	if after > 0 {
diff --git a/internal/store/sync_graph.go b/internal/store/sync_graph.go
index 0e265b7..2102f44 100644
--- a/internal/store/sync_graph.go
+++ b/internal/store/sync_graph.go
@@ -181,13 +181,9 @@ func (s *Store) RemoteConversationPageForAgent(
 		outerWhere = append(outerWhere, "sequence>?")
 		outerArgs = append(outerArgs, after)
 	}
-	if len(categories) > 0 {
-		placeholders := make([]string, 0, len(categories))
-		for _, category := range categories {
-			placeholders = append(placeholders, "?")
-			outerArgs = append(outerArgs, category)
-		}
-		outerWhere = append(outerWhere, "category IN ("+strings.Join(placeholders, ",")+")")
+	if clause, clauseArgs := conversationCategoryClause(categories, "category", "kind"); clause != "" {
+		outerWhere = append(outerWhere, clause)
+		outerArgs = append(outerArgs, clauseArgs...)
 	}
 	order := "DESC"
 	if after > 0 {
@@ -199,7 +195,8 @@ func (s *Store) RemoteConversationPageForAgent(
 		WITH ordered AS (
 			SELECT ROW_NUMBER() OVER (ORDER BY COALESCE(json_extract(payload_json,'$.created_at'),created_at),object_id) AS sequence,
 			       payload_json,
-			       COALESCE(json_extract(payload_json,'$.category'),'') AS category
+			       COALESCE(json_extract(payload_json,'$.category'),'') AS category,
+			       COALESCE(json_extract(payload_json,'$.kind'),'') AS kind
 			FROM sync_remote_task_objects
 			WHERE `+strings.Join(baseWhere, " AND ")+`
 		)
diff --git a/web/src/main.ts b/web/src/main.ts
index b0c16d3..6870663 100644
--- a/web/src/main.ts
+++ b/web/src/main.ts
@@ -708,7 +708,7 @@ const state: State = {
   taskConversationBefore: 0,
   taskConversationLatest: 0,
   taskEventCursor: 0,
-  taskCategories: {chat: true, update: false, tool: false, error: true},
+  taskCategories: {chat: true, update: true, thinking: false, tool: false, error: true},
   taskContext: null,
   taskRealtimeState: "connecting",
   taskDraft: "",
@@ -8737,7 +8737,7 @@ async function loadOlderConversation(): Promise<void> {
       // The patch anchored on the first visible row before the prepend and
       // restored it after; prepended rows above it never move the viewport.
     }
-    const count = document.querySelector<HTMLElement>("#conversation-filter-count");
+    const count = document.querySelector<HTMLElement>("#conversation-filter-loaded");
     if (count) count.textContent = `${state.taskConversation.length} 条已加载`;
   } finally {
     loadingOlderConversation = false;
diff --git a/web/src/task_composer.ts b/web/src/task_composer.ts
index 0e2488e..577cbdf 100644
--- a/web/src/task_composer.ts
+++ b/web/src/task_composer.ts
@@ -68,8 +68,9 @@ export function renderComposerTools(
 ): string {
   const options = renderComposerAgentOptions(detail, selectedAgentID);
   const labels: Record<ConversationCategory, string> = {
-    chat: "对话", update: "Agent Update", tool: "工具调用", error: "错误",
+    chat: "对话", update: "Agent Update", thinking: "Thinking", tool: "工具调用", error: "错误",
   };
+  const total = Object.keys(labels).length;
   const enabled = Object.values(categories).filter(Boolean).length;
   const filters = (Object.keys(labels) as ConversationCategory[]).map(category =>
     `<button type="button" data-conversation-category="${category}" class="${categories[category] ? "active" : ""}"><span>${categories[category] ? "✓" : ""}</span>${labels[category]}</button>`
@@ -79,7 +80,7 @@ export function renderComposerTools(
     <button type="button" data-agent-config="${escapeHTML(selectedAgentID)}" class="icon-button composer-agent-config" title="编辑 ${escapeHTML(selectedAgentID)} 配置">${icon("edit")}</button>
   </div>
   <details class="conversation-filter-popover">
-    <summary class="conversation-filter-trigger" title="筛选消息">${icon("filter")}<small>${enabled}/4</small></summary>
+    <summary class="conversation-filter-trigger" title="筛选消息">${icon("filter")}<small>${enabled}/${total}</small></summary>
     <div class="conversation-filter-menu"><header><strong>消息筛选</strong><small id="conversation-filter-loaded">${loadedCount} 条已加载</small></header>${filters}</div>
   </details>`;
 }
diff --git a/web/src/types.ts b/web/src/types.ts
index 8646e20..dbb46f0 100644
--- a/web/src/types.ts
+++ b/web/src/types.ts
@@ -721,7 +721,10 @@ export interface TaskActivity {
   agent_id: string;
 }
 
-export type ConversationCategory = "chat" | "update" | "tool" | "error";
+// "thinking" is a filter-only category: reasoning rows are stored and returned
+// as category "update" with kind "agent_reasoning_update", and the server splits
+// them out of "update" when the filter asks for it.
+export type ConversationCategory = "chat" | "update" | "thinking" | "tool" | "error";
 
 export interface Attachment {
   id: string;
```

## 完整 diff：测试（-U0，零上下文以控制体积）
```diff
diff --git a/internal/app/service_test.go b/internal/app/service_test.go
index caf4233..2df99a0 100644
--- a/internal/app/service_test.go
+++ b/internal/app/service_test.go
@@ -1341,0 +1342,31 @@ func TestMultiAgentRoundCreatesIntegrationTurn(t *testing.T) {
+		// The live agent_reasoning projection must land in the Thinking filter
+		// and stay out of the Agent Update filter, while progress and
+		// commentary updates stay in Update.
+		thinkingPage, err := database.ConversationPageForAgent(ctx, task.ID, turn.AgentID, 0, 0, 100, []string{"thinking"})
+		if err != nil {
+			t.Fatal(err)
+		}
+		updatePage, err := database.ConversationPageForAgent(ctx, task.ID, turn.AgentID, 0, 0, 100, []string{"update"})
+		if err != nil {
+			t.Fatal(err)
+		}
+		thinkingRows, updateRows := 0, 0
+		for _, item := range thinkingPage.Items {
+			if item.Kind != domain.ConversationKindReasoning {
+				t.Fatalf("thinking filter returned a non-reasoning row: %#v", item)
+			}
+			if item.TurnID == turn.ID {
+				thinkingRows++
+			}
+		}
+		for _, item := range updatePage.Items {
+			if item.Kind == domain.ConversationKindReasoning {
+				t.Fatalf("update filter returned a reasoning row: %#v", item)
+			}
+			if item.TurnID == turn.ID && (item.Kind == "agent_progress" || item.Kind == "agent_message_update") {
+				updateRows++
+			}
+		}
+		if thinkingRows != 1 || updateRows != progress+commentary {
+			t.Fatalf("turn %s thinking/update split: thinking=%d update=%d want 1/%d", turn.ID, thinkingRows, updateRows, progress+commentary)
+		}
diff --git a/internal/httpapi/remote_task_conversation_test.go b/internal/httpapi/remote_task_conversation_test.go
index 0d1afc9..2186333 100644
--- a/internal/httpapi/remote_task_conversation_test.go
+++ b/internal/httpapi/remote_task_conversation_test.go
@@ -91,0 +92,76 @@ func TestRemoteTaskConversationIsPagedAndDefaultsToReadableCategories(t *testing
+// Read-only mirrors keep the synced item payload as-is, so Thinking must be
+// split from Agent Update by the payload kind there too, and a request without
+// categories keeps returning reasoning rows like it did before the split.
+func TestRemoteTaskConversationSplitsThinkingFromUpdate(t *testing.T) {
+	ctx := context.Background()
+	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
+	if err != nil {
+		t.Fatal(err)
+	}
+	defer database.Close()
+	now := time.Now().UTC().Truncate(time.Second)
+	project := domain.Project{ID: "project-remote-thinking", Name: "Remote", CreatedAt: now, UpdatedAt: now}
+	if err := database.CreateProject(ctx, project); err != nil {
+		t.Fatal(err)
+	}
+	task := domain.Task{ID: "source-task-thinking", ProjectID: project.ID, Title: "Remote thinking", Status: domain.TaskWaitingUser, CreatedAt: now, UpdatedAt: now}
+	putRemoteTaskObject(t, database, store.RemoteTaskObject{OwnerDeviceID: "remote-thinking-owner", ObjectType: "task", ObjectID: task.ID, TaskID: task.ID, ProjectID: project.ID, Payload: mustJSON(t, task), CreatedAt: now, UpdatedAt: now})
+	rows := []struct{ id, category, kind string }{
+		{"r1-chat", "chat", "agent_message"},
+		{"r2-update", "update", "agent_progress"},
+		{"r3-thinking", "update", domain.ConversationKindReasoning},
+		{"r4-tool", "tool", "agent_command_finished"},
+		{"r5-thinking", "update", domain.ConversationKindReasoning},
+		{"r6-error", "error", "agent_error"},
+		{"r7-update", "update", "agent_message_update"},
+	}
+	for index, row := range rows {
+		item := domain.ConversationItem{
+			ID: row.id, TaskID: task.ID, AgentID: "main", StreamAgentID: "main",
+			Category: row.category, Kind: row.kind, Summary: row.id, CreatedAt: now.Add(time.Duration(index) * time.Second),
+		}
+		putRemoteTaskObject(t, database, store.RemoteTaskObject{
+			OwnerDeviceID: "remote-thinking-owner", ObjectType: "conversation", ObjectID: item.ID, TaskID: task.ID,
+			ProjectID: project.ID, Payload: mustJSON(t, item), CreatedAt: item.CreatedAt, UpdatedAt: item.CreatedAt,
+		})
+	}
+	mirrors, err := database.RemoteTaskMirrors(ctx, "")
+	if err != nil || len(mirrors) != 1 {
+		t.Fatalf("mirrors=%d err=%v", len(mirrors), err)
+	}
+	publicID := mirrors[0].Task.ID
+	authService := auth.NewService(database, "setup-test", time.Hour)
+	server := httptest.NewServer(New(Config{Store: database, Auth: authService, App: app.NewService(database, nil, app.StubExecutor{})}).Handler())
+	defer server.Close()
+	jar, _ := cookiejar.New(nil)
+	client := &http.Client{Jar: jar}
+	_ = registerOwner(t, client, server.URL)
+	ids := func(page domain.ConversationPage) string {
+		result := []string{}
+		for _, item := range page.Items {
+			result = append(result, item.ID)
+		}
+		return fmt.Sprint(result)
+	}
+	cases := map[string]string{
+		"limit=20&categories=chat,error,update":          "[r1-chat r2-update r6-error r7-update]",
+		"limit=20&categories=thinking":                   "[r3-thinking r5-thinking]",
+		"limit=20&categories=update,thinking":            "[r2-update r3-thinking r5-thinking r7-update]",
+		"limit=20&categories=chat,error,update,thinking": "[r1-chat r2-update r3-thinking r5-thinking r6-error r7-update]",
+		"limit=20": "[r1-chat r2-update r3-thinking r5-thinking r6-error r7-update]",
+	}
+	for query, want := range cases {
+		if got := ids(getRemoteConversationPage(t, client, server.URL, publicID, query)); got != want {
+			t.Fatalf("%s: got %s want %s", query, got, want)
+		}
+	}
+	first := getRemoteConversationPage(t, client, server.URL, publicID, "limit=1&categories=thinking")
+	if ids(first) != "[r5-thinking]" || !first.HasMore {
+		t.Fatalf("first thinking page: %#v", first)
+	}
+	older := getRemoteConversationPage(t, client, server.URL, publicID, fmt.Sprintf("limit=1&categories=thinking&before=%d", first.NextBefore))
+	if ids(older) != "[r3-thinking]" || older.HasMore {
+		t.Fatalf("older thinking page: %#v", older)
+	}
+}
+
diff --git a/internal/store/conversation_page_test.go b/internal/store/conversation_page_test.go
index 716e7f2..21052ec 100644
--- a/internal/store/conversation_page_test.go
+++ b/internal/store/conversation_page_test.go
@@ -4,0 +5 @@ import (
+	"fmt"
@@ -5,0 +7 @@ import (
+	"sort"
@@ -67,0 +70,149 @@ func TestConversationPageLatestDoesNotSkipUnreturnedItems(t *testing.T) {
+
+// Thinking is split from Agent Update by the stored reasoning kind. The mixed
+// fixture interleaves every visible category so filters, before-pagination
+// and after-increments are checked against the exact expected row sets.
+func TestConversationPageSplitsThinkingFromUpdate(t *testing.T) {
+	t.Parallel()
+	ctx := context.Background()
+	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
+	if err != nil {
+		t.Fatal(err)
+	}
+	defer database.Close()
+	task := createStoreTestTask(t, database)
+	now := time.Now().UTC()
+	type row struct{ category, kind string }
+	fixture := []row{
+		{"chat", "user_message"},
+		{"update", "agent_progress"},
+		{"update", domain.ConversationKindReasoning},
+		{"tool", "agent_command_started"},
+		{"update", "agent_message_update"},
+		{"update", domain.ConversationKindReasoning},
+		{"tool", "agent_command_finished"},
+		{"error", "agent_error"},
+		{"update", domain.ConversationKindReasoning},
+		{"update", "turn_duration"},
+		{"chat", "agent_message"},
+	}
+	bucket := func(r row) string {
+		if r.category == "update" && r.kind == domain.ConversationKindReasoning {
+			return "thinking"
+		}
+		return r.category
+	}
+	sequences := map[string][]int64{}
+	add := func(r row) int64 {
+		t.Helper()
+		item, addErr := database.AddConversationItem(ctx, domain.ConversationItem{
+			ID: domain.NewID("conversation"), TaskID: task.ID, TurnID: "turn-split",
+			AgentID: "main", StreamAgentID: "main", Category: r.category, Kind: r.kind,
+			Summary: r.category + "/" + r.kind, CreatedAt: now,
+		})
+		if addErr != nil {
+			t.Fatal(addErr)
+		}
+		sequences[bucket(r)] = append(sequences[bucket(r)], item.Sequence)
+		return item.Sequence
+	}
+	for _, r := range fixture {
+		add(r)
+	}
+	expected := func(categories ...string) []int64 {
+		var result []int64
+		for _, category := range categories {
+			result = append(result, sequences[category]...)
+		}
+		sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
+		return result
+	}
+	got := func(page domain.ConversationPage) []int64 {
+		result := make([]int64, 0, len(page.Items))
+		for _, item := range page.Items {
+			result = append(result, item.Sequence)
+		}
+		return result
+	}
+	cases := []struct {
+		name       string
+		categories []string
+		want       []int64
+	}{
+		{"default chat+error+update has no thinking", []string{"chat", "error", "update"}, expected("chat", "error", "update")},
+		{"update only", []string{"update"}, expected("update")},
+		{"thinking only", []string{"thinking"}, expected("thinking")},
+		{"update and thinking", []string{"update", "thinking"}, expected("update", "thinking")},
+		{"thinking with chat and tool", []string{"chat", "thinking", "tool"}, expected("chat", "thinking", "tool")},
+		{"all five", []string{"chat", "error", "update", "thinking", "tool"}, expected("chat", "error", "update", "thinking", "tool")},
+		{"duplicates collapse", []string{"thinking", "thinking", "error"}, expected("thinking", "error")},
+		{"no filter returns all", nil, expected("chat", "error", "update", "thinking", "tool")},
+	}
+	for _, tc := range cases {
+		page, err := database.ConversationPageForAgent(ctx, task.ID, "main", 0, 0, 100, tc.categories)
+		if err != nil {
+			t.Fatal(err)
+		}
+		if fmt.Sprint(got(page)) != fmt.Sprint(tc.want) || page.HasMore {
+			t.Fatalf("%s: got %v has_more=%t, want %v", tc.name, got(page), page.HasMore, tc.want)
+		}
+	}
+
+	// Paging backwards one row at a time visits every filtered row once.
+	for _, categories := range [][]string{{"thinking"}, {"update"}, {"chat", "error", "update"}, {"update", "thinking"}} {
+		var walked []int64
+		before := int64(0)
+		for guard := 0; guard < 50; guard++ {
+			page, err := database.ConversationPageForAgent(ctx, task.ID, "main", before, 0, 1, categories)
+			if err != nil {
+				t.Fatal(err)
+			}
+			walked = append(got(page), walked...)
+			if !page.HasMore {
+				break
+			}
+			before = page.NextBefore
+		}
+		want := map[string]bool{}
+		for _, c := range categories {
+			want[c] = true
+		}
+		var cats []string
+		for c := range want {
+			cats = append(cats, c)
+		}
+		if fmt.Sprint(walked) != fmt.Sprint(expected(cats...)) {
+			t.Fatalf("paging %v: walked %v, want %v", categories, walked, expected(cats...))
+		}
+	}
+
+	// Incremental refresh after the latest cursor: new rows of every kind
+	// arrive; each filter sees only its own new rows and the cursor never
+	// skips a row it did not return.
+	thinkingLatest, _ := database.ConversationPageForAgent(ctx, task.ID, "main", 0, 0, 100, []string{"thinking"})
+	updateLatest, _ := database.ConversationPageForAgent(ctx, task.ID, "main", 0, 0, 100, []string{"chat", "error", "update"})
+	newUpdate := add(row{"update", "agent_progress"})
+	newThinking := add(row{"update", domain.ConversationKindReasoning})
+	newTool := add(row{"tool", "agent_command_started"})
+	secondThinking := add(row{"update", domain.ConversationKindReasoning})
+	incThinking, err := database.ConversationPageForAgent(ctx, task.ID, "main", 0, thinkingLatest.Latest, 1, []string{"thinking"})
+	if err != nil {
+		t.Fatal(err)
+	}
+	if fmt.Sprint(got(incThinking)) != fmt.Sprint([]int64{newThinking}) || !incThinking.HasMore || incThinking.Latest != newThinking {
+		t.Fatalf("incremental thinking page: %#v", incThinking)
+	}
+	incThinking, err = database.ConversationPageForAgent(ctx, task.ID, "main", 0, incThinking.Latest, 1, []string{"thinking"})
+	if err != nil {
+		t.Fatal(err)
+	}
+	if fmt.Sprint(got(incThinking)) != fmt.Sprint([]int64{secondThinking}) || incThinking.HasMore {
+		t.Fatalf("second incremental thinking page: %#v", incThinking)
+	}
+	incUpdate, err := database.ConversationPageForAgent(ctx, task.ID, "main", 0, updateLatest.Latest, 100, []string{"chat", "error", "update"})
+	if err != nil {
+		t.Fatal(err)
+	}
+	if fmt.Sprint(got(incUpdate)) != fmt.Sprint([]int64{newUpdate}) {
+		t.Fatalf("incremental default page leaked thinking/tool: got %v want [%d] (tool %d)", got(incUpdate), newUpdate, newTool)
+	}
+}
diff --git a/web/tests/build.test.mjs b/web/tests/build.test.mjs
index 3c02755..7109ce0 100644
--- a/web/tests/build.test.mjs
+++ b/web/tests/build.test.mjs
@@ -1522,2 +1522,4 @@ test("built web contains responsive application", async () => {
-  // The message filter keeps exactly four categories; mail is not one of them.
-  assert.match(taskComposer, /chat: "对话",\s*update: "Agent Update",\s*tool: "工具调用",\s*error: "错误",?\s*\}/);
+  // The message filter has exactly five categories (task-724 split Thinking out
+  // of Agent Update); mail is still not one of them. The count denominator is
+  // derived from the label table so it cannot drift from the buttons.
+  assert.match(taskComposer, /chat: "对话",\s*update: "Agent Update",\s*thinking: "Thinking",\s*tool: "工具调用",\s*error: "错误",?\s*\}/);
@@ -1525,2 +1527,3 @@ test("built web contains responsive application", async () => {
-  assert.match(taskComposer, /enabled}\/4/);
-  assert.doesNotMatch(taskComposer, /enabled}\/5/);
+  assert.match(taskComposer, /const total = Object\.keys\(labels\)\.length;/);
+  assert.match(taskComposer, /enabled}\/\$\{total}/);
+  assert.doesNotMatch(taskComposer, /enabled}\/[0-9]/);
@@ -1530 +1533,2 @@ test("built web contains responsive application", async () => {
-  assert.match(script, /taskCategories:\s*\{\s*chat:\s*true,\s*update:\s*false,\s*tool:\s*false,\s*error:\s*true\s*\}/);
+  // Default filter (task-724): chat + error + Agent Update; Thinking and tool off.
+  assert.match(script, /taskCategories:\s*\{\s*chat:\s*true,\s*update:\s*true,\s*thinking:\s*false,\s*tool:\s*false,\s*error:\s*true\s*\}/);
diff --git a/web/tests/task_conversation_thinking_filter.browser.mjs b/web/tests/task_conversation_thinking_filter.browser.mjs
new file mode 100644
index 0000000..1ed868b
--- /dev/null
+++ b/web/tests/task_conversation_thinking_filter.browser.mjs
@@ -0,0 +1,294 @@
+// Live Task Thinking/Agent Update filter regression (task-724).
+//
+// Reasoning rows are stored as category "update" + kind
+// "agent_reasoning_update" and the server splits them out of "update" for the
+// "thinking" filter. The fixture's conversation endpoint reproduces the store
+// contract (category split, before/after cursors, limit+has_more, latest only
+// advancing through returned rows) so this test checks the Web side end to end:
+// default 3/5 without Thinking, Thinking alone/combined, "load older" paging
+// with no gaps or duplicates, and SSE-driven incremental refresh appending only
+// the selected categories.
+import assert from "node:assert/strict";
+import {createServer} from "node:http";
+import {readFile} from "node:fs/promises";
+import {resolve} from "node:path";
+import {pathToFileURL} from "node:url";
+
+const root = resolve(import.meta.dirname, "../..");
+const tools = resolve(root, ".tools/web-validation");
+const {chromium} = await import(pathToFileURL(
+  process.env.AHA_PLAYWRIGHT_MODULE || resolve(tools, "node_modules/playwright/index.mjs"),
+));
+const useInstalledBrowser = Boolean(process.env.AHA_BROWSER_EXECUTABLE);
+const binary = useInstalledBrowser
+  ? {executablePath: async () => process.env.AHA_BROWSER_EXECUTABLE, args: []}
+  : (await import(pathToFileURL(resolve(tools, "node_modules/@sparticuz/chromium/build/index.js")))).default;
+const csp = (await readFile(resolve(root, "internal/httpapi/server.go"), "utf8"))
+  .match(/Set\("Content-Security-Policy", "([^"]+)"\)/)[1];
+
+const TASK = {
+  id: "task-live", code: "task-724", title: "Thinking filter", status: "waiting_user",
+  project_id: "project-1", workspace_id: "workspace-1",
+  collaboration_mode: "single", max_agents: 1, original_request: "work", current_goal: "work",
+  task_branch: "", runtime_config_snapshot_id: "runtime-1", total_tokens: 0,
+  created_at: "2026-11-20T00:00:00Z", updated_at: "2026-11-20T00:00:00Z",
+};
+
+// 150 history rows cycling through every visible kind: 25 chat, 50 Agent
+// Update (progress + commentary), 25 Thinking, 25 tool, 25 error.
+const CYCLE = [
+  ["chat", "agent_message", "chat"],
+  ["update", "agent_progress", "update"],
+  ["update", "agent_reasoning_update", "thinking"],
+  ["tool", "agent_command_finished", "tool"],
+  ["error", "agent_error", "error"],
+  ["update", "agent_message_update", "update"],
+];
+const row = (sequence, [category, kind, bucket]) => ({
+  sequence, id: `r${sequence}-${bucket}`, task_id: TASK.id, agent_id: "main", category, kind,
+  summary: `${bucket} row ${sequence}`, payload: category === "tool" ? {command: "ls"} : {},
+  created_at: new Date(Date.UTC(2026, 10, 20, 0, 0, sequence)).toISOString(),
+});
+const HISTORY = Array.from({length: 150}, (_, index) => row(index + 1, CYCLE[index % CYCLE.length]));
+
+const bootstrap = `
+const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
+const TASK = ${JSON.stringify(TASK)};
+window.__rows = ${JSON.stringify(HISTORY)};
+window.__requests = [];
+const bucket = item => item.category === "update" && item.kind === "agent_reasoning_update" ? "thinking" : item.category;
+const page = url => {
+  const categories = (url.searchParams.get("categories") || "").split(",").filter(Boolean);
+  const before = Number(url.searchParams.get("before") || 0);
+  const after = Number(url.searchParams.get("after") || 0);
+  let limit = Number(url.searchParams.get("limit") || 50);
+  if (limit <= 0 || limit > 200) limit = 50;
+  window.__requests.push({categories, before, after, limit});
+  let rows = window.__rows.filter(item => (!categories.length || categories.includes(bucket(item)))
+    && (!before || item.sequence < before) && (!after || item.sequence > after));
+  rows = after ? rows.sort((a, b) => a.sequence - b.sequence) : rows.sort((a, b) => b.sequence - a.sequence);
+  const hasMore = rows.length > limit;
+  rows = rows.slice(0, limit);
+  if (!after) rows.reverse();
+  const latest = rows.reduce((value, item) => Math.max(value, item.sequence), after);
+  return {items: rows, has_more: hasMore, next_before: rows[0]?.sequence || 0, latest_sequence: latest};
+};
+const detail = () => ({
+  task: TASK, turns: [], latest_round: null,
+  agents: [{agent_id: "main", status: "idle", title: "Main", runtime_config_valid: true, unread_count: 0}],
+  memory: {}, hardware: [], event_cursor: 0, server_time_ms: Date.now(),
+});
+window.fetch = async (input, init = {}) => {
+  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
+  const path = url.pathname;
+  const method = (init.method || "GET").toUpperCase();
+  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
+  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: "zh-CN"}});
+  if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", nickname: "AHA", enabled: false}, messages: [], models: [], tools: []});
+  if (path === "/api/v1/projects") return json({projects: [{id: "project-1", name: "Project", project_type: "folder", channel_retired: false}], has_more: false});
+  if (path === "/api/v1/workspaces") return json({workspaces: [{id: "workspace-1", project_id: "project-1", name: "W", root_path: "/tmp", transport: "native", health: "ready"}]});
+  if (path === "/api/v1/tasks/task-live" && method === "GET") return json(detail());
+  if (path === "/api/v1/tasks/task-live/channel-routes") return json({ok: true, destinations: [], route: null, contacts: [], members: []});
+  if (path === "/api/v1/tasks/task-live/agents") return json({agents: detail().agents});
+  if (path === "/api/v1/tasks") return json({tasks: [TASK], has_more: false});
+  if (path.includes("/conversation")) return json({conversation: page(url)});
+  if (path.includes("/context")) return json({context: {}});
+  if (path.endsWith("/events")) return json({events: [], cursor: 0});
+  if (path === "/api/v1/pet/approvals/pending-count") return json({ok: true, pending: 0});
+  if (path.startsWith("/api/v1/")) return json({ok: true});
+  return json({});
+};
+window.__sources = [];
+window.EventSource = class {
+  constructor(url) { this.url = url; this.listeners = {}; this.closed = false; window.__sources.push(this); }
+  close() { this.closed = true; }
+  addEventListener(type, listener) { (this.listeners[type] ||= []).push(listener); }
+};
+window.__sseSequence = 0;
+window.__emitUpdate = () => {
+  const source = window.__sources.filter(item => !item.closed).at(-1);
+  if (!source) return false;
+  window.__sseSequence += 1;
+  for (const listener of source.listeners.update || []) listener({data: JSON.stringify({sequence: window.__sseSequence, type: "conversation"})});
+  return true;
+};
+sessionStorage.setItem("aha2.navigation", JSON.stringify({view: "tasks", taskID: TASK.id, task: TASK, agentID: "main"}));
+`;
+
+const html = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><link rel="stylesheet" href="/styles.css"></head><body><main id="app"></main><script src="/fixture.js"></script><script type="module" src="/app.js"></script></body></html>`;
+
+const server = createServer(async (request, response) => {
+  const name = new URL(request.url, "http://localhost").pathname;
+  try {
+    response.setHeader("Content-Security-Policy", csp);
+    if (name === "/" || name === "/index.html") {
+      response.setHeader("Content-Type", "text/html");
+      response.end(html);
+    } else if (name === "/fixture.js") {
+      response.setHeader("Content-Type", "text/javascript");
+      response.end(bootstrap);
+    } else if (name.startsWith("/assets/")) {
+      response.setHeader("Content-Type", name.endsWith(".svg") ? "image/svg+xml" : name.endsWith(".png") ? "image/png" : "application/octet-stream");
+      response.end(await readFile(resolve(root, "web/build", name.replace(/^\//, ""))));
+    } else {
+      if (!/^\/(?:vendor\/)?[a-z0-9_]+\.(?:js|css|png)$/.test(name)) { response.writeHead(404).end(); return; }
+      const base = name.startsWith("/vendor/") ? resolve(root, "web") : resolve(root, "web/build");
+      response.setHeader("Content-Type", name.endsWith(".css") ? "text/css" : name.endsWith(".png") ? "image/png" : "text/javascript");
+      response.end(await readFile(resolve(base, name.replace(/^\/(vendor\/)?/, ""))));
+    }
+  } catch { response.writeHead(404).end(); }
+});
+await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
+const url = `http://127.0.0.1:${server.address().port}`;
+
+async function snapshot(page) {
+  return page.evaluate(() => {
+    const selected = [...document.querySelectorAll("[data-conversation-category].active")]
+      .map(node => node.dataset.conversationCategory).sort();
+    const mounted = [...document.querySelectorAll("#conversation-list [data-message-id]")].map(node => node.dataset.messageId);
+    return {
+      selected,
+      count: document.querySelector(".conversation-filter-trigger small")?.textContent?.trim() || "",
+      loaded: document.querySelector("#conversation-filter-loaded")?.textContent?.trim() || "",
+      last: window.__requests.at(-1) || null,
+      requests: window.__requests.length,
+      mounted,
+      buckets: [...new Set(mounted.map(id => id.replace(/^r\d+-/, "")))].sort(),
+      hasOlder: Boolean(document.querySelector("#load-older-conversation")),
+    };
+  });
+}
+
+async function toggle(page, category, expected) {
+  const before = await page.evaluate(() => window.__requests.length);
+  await page.evaluate(() => {
+    const popover = document.querySelector(".conversation-filter-popover");
+    if (popover) popover.open = true;
+  });
+  await page.click(`[data-conversation-category="${category}"]`);
+  await page.waitForFunction(({count, expected}) => {
+    if (window.__requests.length <= count) return false;
+    const selected = [...document.querySelectorAll("[data-conversation-category].active")]
+      .map(node => node.dataset.conversationCategory).sort();
+    return selected.join(",") === expected.join(",");
+  }, {count: before, expected}, {timeout: 10000});
+  // Let the post-reload render settle before reading the DOM.
+  await page.waitForFunction(expected => {
+    const request = window.__requests.at(-1);
+    return request && [...request.categories].sort().join(",") === expected.join(",");
+  }, expected, {timeout: 10000});
+}
+
+const sorted = values => [...values].sort();
+const seqs = ids => ids.map(id => Number(id.match(/^r(\d+)-/)[1]));
+
+let browser;
+try {
+  browser = await chromium.launch({
+    executablePath: await binary.executablePath(),
+    args: useInstalledBrowser ? ["--no-sandbox"] : binary.args,
+    headless: true,
+  });
+  // Tall viewport so the windowed list mounts every loaded Thinking row.
+  const page = await browser.newPage({viewport: {width: 1280, height: 4000}});
+  const errors = [];
+  page.on("pageerror", error => errors.push(error.message));
+  page.on("console", message => { if (message.type() === "error") errors.push(message.text()); });
+
+  await page.goto(url, {waitUntil: "domcontentloaded"});
+  await page.waitForSelector(".conversation-filter-trigger", {timeout: 20000});
+  await page.waitForFunction(() => window.__requests.length > 0 && document.querySelectorAll("#conversation-list [data-message-id]").length > 0, null, {timeout: 20000});
+
+  // Default: chat + error + Agent Update, 3/5, Thinking and tool off.
+  const initial = await snapshot(page);
+  assert.deepEqual(initial.selected, ["chat", "error", "update"]);
+  assert.deepEqual(sorted(initial.last.categories), ["chat", "error", "update"]);
+  assert.equal(initial.count, "3/5");
+  assert.equal(initial.loaded, "20 条已加载");
+  assert.deepEqual(initial.buckets, ["chat", "error", "update"], `default mounted buckets: ${initial.buckets}`);
+  const labels = await page.evaluate(() => [...document.querySelectorAll("[data-conversation-category]")]
+    .map(node => [node.dataset.conversationCategory, node.textContent.replace("✓", "").trim()]));
+  assert.deepEqual(labels, [["chat", "对话"], ["update", "Agent Update"], ["thinking", "Thinking"], ["tool", "工具调用"], ["error", "错误"]]);
+
+  // Thinking alone: switch Agent Update, chat and error off, Thinking on.
+  await toggle(page, "thinking", ["chat", "error", "thinking", "update"]);
+  await toggle(page, "update", ["chat", "error", "thinking"]);
+  await toggle(page, "chat", ["error", "thinking"]);
+  await toggle(page, "error", ["thinking"]);
+  const thinkingOnly = await snapshot(page);
+  assert.equal(thinkingOnly.count, "1/5");
+  assert.deepEqual(thinkingOnly.last.categories, ["thinking"]);
+  assert.equal(thinkingOnly.loaded, "20 条已加载");
+  assert.deepEqual(thinkingOnly.buckets, ["thinking"], `thinking-only buckets: ${thinkingOnly.buckets}`);
+  assert.ok(thinkingOnly.hasOlder, "25 Thinking rows must leave a load-older control after the first 20");
+
+  // Load older: the remaining 5 Thinking rows arrive with no gap or duplicate.
+  const beforeOlder = thinkingOnly.requests;
+  await page.click("#load-older-conversation");
+  await page.waitForFunction(count => window.__requests.length > count && document.querySelector("#conversation-filter-loaded")?.textContent?.trim() === "25 条已加载", beforeOlder, {timeout: 10000});
+  const older = await snapshot(page);
+  assert.deepEqual(older.last.categories, ["thinking"]);
+  assert.ok(older.last.before > 0, "load-older must page with a before cursor");
+  assert.ok(!older.hasOlder, "load-older control must disappear once every Thinking row is loaded");
+  const expectedThinking = Array.from({length: 25}, (_, index) => index * 6 + 3);
+  assert.deepEqual(seqs(older.mounted), expectedThinking, `thinking rows after paging: ${older.mounted}`);
+
+  // Live: new rows of every kind arrive; the SSE-driven refresh appends only
+  // the new Thinking row, using the current filter and the latest cursor.
+  const appendRows = kinds => page.evaluate(kinds => {
+    const cycle = {chat: ["chat", "agent_message"], update: ["update", "agent_progress"], thinking: ["update", "agent_reasoning_update"], tool: ["tool", "agent_command_started"], error: ["error", "agent_error"]};
+    for (const bucket of kinds) {
+      const sequence = window.__rows.length + 1;
+      const [category, kind] = cycle[bucket];
+      window.__rows.push({sequence, id: `r${sequence}-${bucket}`, task_id: "task-live", agent_id: "main", category, kind, summary: `${bucket} live ${sequence}`, payload: {}, created_at: new Date().toISOString()});
+    }
+  }, kinds);
+  await appendRows(["update", "thinking", "tool", "chat", "error"]);
+  const beforeLive = (await snapshot(page)).requests;
+  assert.ok(await page.evaluate(() => window.__emitUpdate()), "the task must hold an open event stream");
+  await page.waitForFunction(() => document.querySelector('[data-message-id="r152-thinking"]'), null, {timeout: 10000});
+  const live = await snapshot(page);
+  const liveRequest = await page.evaluate(count => window.__requests.slice(count).find(item => item.after > 0), beforeLive);
+  assert.ok(liveRequest, "SSE refresh must issue an incremental after-cursor request");
+  assert.deepEqual(liveRequest.categories, ["thinking"]);
+  assert.equal(liveRequest.after, 147, "incremental cursor must start at the newest returned Thinking row");
+  assert.equal(live.loaded, "26 条已加载");
+  assert.deepEqual(live.buckets, ["thinking"], `live thinking-only buckets: ${live.buckets}`);
+  assert.deepEqual(seqs(live.mounted), [...expectedThinking, 152]);
+
+  // Agent Update back on together with Thinking: both appear, no duplicates.
+  await toggle(page, "update", ["thinking", "update"]);
+  const combined = await snapshot(page);
+  assert.equal(combined.count, "2/5");
+  assert.deepEqual(sorted(combined.last.categories), ["thinking", "update"]);
+  assert.deepEqual(combined.buckets, ["thinking", "update"]);
+  assert.equal(new Set(combined.mounted).size, combined.mounted.length, "combined filter mounted a duplicate row");
+  // Newest 20 of (50+1 Update, 25+1 Thinking) by sequence.
+  const combinedExpected = [...Array.from({length: 150}, (_, index) => index + 1).filter(seq => [2, 3, 0].includes(seq % 6)), 151, 152].slice(-20);
+  assert.deepEqual(seqs(combined.mounted), combinedExpected, `combined rows: ${combined.mounted}`);
+
+  // A later live batch in the combined filter appends both kinds once each.
+  await appendRows(["thinking", "tool", "update", "error"]);
+  assert.ok(await page.evaluate(() => window.__emitUpdate()));
+  await page.waitForFunction(() => document.querySelector('[data-message-id="r158-update"]') && document.querySelector('[data-message-id="r156-thinking"]'), null, {timeout: 10000});
+  const combinedLive = await snapshot(page);
+  assert.equal(combinedLive.loaded, "22 条已加载");
+  assert.deepEqual(seqs(combinedLive.mounted), [...combinedExpected, 156, 158]);
+
+  // Agent Update alone after Thinking off: no reasoning row remains, chat/error/tool untouched.
+  await toggle(page, "thinking", ["update"]);
+  const updateOnly = await snapshot(page);
+  assert.deepEqual(updateOnly.buckets, ["update"], `update-only buckets: ${updateOnly.buckets}`);
+  await appendRows(["thinking", "update"]);
+  assert.ok(await page.evaluate(() => window.__emitUpdate()));
+  await page.waitForFunction(() => document.querySelector('[data-message-id="r161-update"]'), null, {timeout: 10000});
+  const updateLive = await snapshot(page);
+  assert.ok(!updateLive.mounted.includes("r160-thinking"), "a live Thinking row leaked into the Agent Update filter");
+  assert.deepEqual(updateLive.buckets, ["update"]);
+
+  assert.deepEqual(errors, []);
+  console.log("task_conversation_thinking_filter: ok");
+} finally {
+  await browser?.close();
+  server.close();
+}
diff --git a/web/tests/task_readonly_filter.browser.mjs b/web/tests/task_readonly_filter.browser.mjs
index d3db064..574a233 100644
--- a/web/tests/task_readonly_filter.browser.mjs
+++ b/web/tests/task_readonly_filter.browser.mjs
@@ -7,2 +7,7 @@
-// the requested categories stay identical, including after toggling update and
-// tool one at a time.
+// the requested categories stay identical, including after toggling update,
+// thinking and tool one at a time.
+//
+// task-724: five categories, default chat+error+update (3/5). Reasoning rows are
+// stored as category "update" + kind "agent_reasoning_update"; the fixture
+// filters them the way the server does, so Agent Update never shows them and
+// Thinking shows only them.
@@ -37,3 +42,4 @@ const CONVERSATION = [
-  {sequence: 2, id: "ci-update", task_id: TASK.id, agent_id: "main", category: "update", kind: "agent_reasoning_update", summary: "update row", payload: {}, created_at: "2026-11-13T00:00:01Z"},
-  {sequence: 3, id: "ci-tool", task_id: TASK.id, agent_id: "main", category: "tool", kind: "agent_command_finished", summary: "tool row", payload: {command: "ls"}, created_at: "2026-11-13T00:00:02Z"},
-  {sequence: 4, id: "ci-error", task_id: TASK.id, agent_id: "main", category: "error", kind: "agent_error", summary: "error row", payload: {}, created_at: "2026-11-13T00:00:03Z"},
+  {sequence: 2, id: "ci-update", task_id: TASK.id, agent_id: "main", category: "update", kind: "agent_progress", summary: "update row", payload: {}, created_at: "2026-11-13T00:00:01Z"},
+  {sequence: 3, id: "ci-thinking", task_id: TASK.id, agent_id: "main", category: "update", kind: "agent_reasoning_update", summary: "thinking row", payload: {}, created_at: "2026-11-13T00:00:02Z"},
+  {sequence: 4, id: "ci-tool", task_id: TASK.id, agent_id: "main", category: "tool", kind: "agent_command_finished", summary: "tool row", payload: {command: "ls"}, created_at: "2026-11-13T00:00:03Z"},
+  {sequence: 5, id: "ci-error", task_id: TASK.id, agent_id: "main", category: "error", kind: "agent_error", summary: "error row", payload: {}, created_at: "2026-11-13T00:00:04Z"},
@@ -68 +74,2 @@ window.fetch = async (input, init = {}) => {
-    const items = CONVERSATION.filter(item => !categories.length || categories.includes(item.category));
+    const bucket = item => item.category === "update" && item.kind === "agent_reasoning_update" ? "thinking" : item.category;
+    const items = CONVERSATION.filter(item => !categories.length || categories.includes(bucket(item)));
@@ -128 +135 @@ function assertConsistent(state, expected) {
-  assert.equal(state.count, `${expected.length}/4`, `control count: ${state.count}`);
+  assert.equal(state.count, `${expected.length}/5`, `control count: ${state.count}`);
@@ -159 +166 @@ try {
-  await page.waitForFunction(() => window.__requests.length > 0 && document.querySelectorAll("#conversation-list [data-message-id]").length === 2, null, {timeout: 20000});
+  await page.waitForFunction(() => window.__requests.length > 0 && document.querySelectorAll("#conversation-list [data-message-id]").length === 3, null, {timeout: 20000});
@@ -161,2 +168,2 @@ try {
-  // Read-only default is the same chat+error filter the control displays. No
-  // update row may slip in from a hardcoded read-only request.
+  // Read-only default is the same chat+error+update filter the control
+  // displays. No thinking or tool row may slip in.
@@ -164,2 +171,2 @@ try {
-  assertConsistent(initial, ["chat", "error"]);
-  assert.deepEqual(initial.visible, ["ci-chat", "ci-error"], `initial rows: ${JSON.stringify(initial.visible)}`);
+  assertConsistent(initial, ["chat", "error", "update"]);
+  assert.deepEqual(initial.visible, ["ci-chat", "ci-error", "ci-update"], `initial rows: ${JSON.stringify(initial.visible)}`);
@@ -167,5 +174,5 @@ try {
-  await toggle(page, "update", initial.requests, ["chat", "error", "update"]);
-  const withUpdate = await filterState(page);
-  assertConsistent(withUpdate, ["chat", "error", "update"]);
-  assert.deepEqual(withUpdate.visible, ["ci-chat", "ci-error", "ci-update"], `update rows: ${JSON.stringify(withUpdate.visible)}`);
-  assert.ok(!withUpdate.requested.includes("tool"), "toggling update must not add tool");
+  await toggle(page, "thinking", initial.requests, ["chat", "error", "thinking", "update"]);
+  const withThinking = await filterState(page);
+  assertConsistent(withThinking, ["chat", "error", "thinking", "update"]);
+  assert.deepEqual(withThinking.visible, ["ci-chat", "ci-error", "ci-thinking", "ci-update"], `thinking rows: ${JSON.stringify(withThinking.visible)}`);
+  assert.ok(!withThinking.requested.includes("tool"), "toggling thinking must not add tool");
@@ -173 +180 @@ try {
-  await toggle(page, "tool", withUpdate.requests, ["chat", "error", "tool", "update"]);
+  await toggle(page, "tool", withThinking.requests, ["chat", "error", "thinking", "tool", "update"]);
@@ -175,2 +182,2 @@ try {
-  assertConsistent(withTool, ["chat", "error", "tool", "update"]);
-  assert.deepEqual(withTool.visible, ["ci-chat", "ci-error", "ci-tool", "ci-update"], `all rows: ${JSON.stringify(withTool.visible)}`);
+  assertConsistent(withTool, ["chat", "error", "thinking", "tool", "update"]);
+  assert.deepEqual(withTool.visible, ["ci-chat", "ci-error", "ci-thinking", "ci-tool", "ci-update"], `all rows: ${JSON.stringify(withTool.visible)}`);
@@ -179,2 +186,3 @@ try {
-  // Switching categories off only removes that category too.
-  await toggle(page, "update", withTool.requests, ["chat", "error", "tool"]);
+  // Switching categories off only removes that category too: Agent Update off
+  // keeps Thinking, and Thinking off keeps tool rows.
+  await toggle(page, "update", withTool.requests, ["chat", "error", "thinking", "tool"]);
@@ -182 +190 @@ try {
-  assertConsistent(updateOff, ["chat", "error", "tool"]);
+  assertConsistent(updateOff, ["chat", "error", "thinking", "tool"]);
@@ -183,0 +192 @@ try {
+  assert.ok(updateOff.visible.includes("ci-thinking"), "thinking row disappeared with the update toggle");
@@ -186 +195,6 @@ try {
-  await toggle(page, "tool", updateOff.requests, ["chat", "error"]);
+  await toggle(page, "thinking", updateOff.requests, ["chat", "error", "tool"]);
+  const thinkingOff = await filterState(page);
+  assertConsistent(thinkingOff, ["chat", "error", "tool"]);
+  assert.deepEqual(thinkingOff.visible, ["ci-chat", "ci-error", "ci-tool"], `thinking off rows: ${JSON.stringify(thinkingOff.visible)}`);
+
+  await toggle(page, "tool", thinkingOff.requests, ["chat", "error"]);
@@ -189 +203,7 @@ try {
-  assert.deepEqual(toolOff.visible, ["ci-chat", "ci-error"], `final rows: ${JSON.stringify(toolOff.visible)}`);
+  assert.deepEqual(toolOff.visible, ["ci-chat", "ci-error"], `tool off rows: ${JSON.stringify(toolOff.visible)}`);
+
+  // Agent Update back on alone still excludes Thinking.
+  await toggle(page, "update", toolOff.requests, ["chat", "error", "update"]);
+  const updateOn = await filterState(page);
+  assertConsistent(updateOn, ["chat", "error", "update"]);
+  assert.deepEqual(updateOn.visible, ["ci-chat", "ci-error", "ci-update"], `final rows: ${JSON.stringify(updateOn.visible)}`);
diff --git a/web/tests/task_round_activity.browser.mjs b/web/tests/task_round_activity.browser.mjs
index ad4987d..615e361 100644
--- a/web/tests/task_round_activity.browser.mjs
+++ b/web/tests/task_round_activity.browser.mjs
@@ -38 +38 @@ const CONVERSATION = [
-  {sequence: 2, id: "ci-update", task_id: TASK.id, agent_id: "main", category: "update", kind: "agent_reasoning_update", summary: "update row", payload: {}, created_at: "2026-11-13T00:00:01Z"},
+  {sequence: 2, id: "ci-update", task_id: TASK.id, agent_id: "main", category: "update", kind: "agent_progress", summary: "update row", payload: {}, created_at: "2026-11-13T00:00:01Z"},
@@ -40,0 +41 @@ const CONVERSATION = [
+  {sequence: 5, id: "ci-thinking", task_id: TASK.id, agent_id: "main", category: "update", kind: "agent_reasoning_update", summary: "thinking row", payload: {}, created_at: "2026-11-13T00:00:04Z"},
@@ -103 +104,2 @@ window.fetch = async (input, init = {}) => {
-    const items = CONVERSATION.filter(item => !categories.length || categories.includes(item.category));
+    const bucket = item => item.category === "update" && item.kind === "agent_reasoning_update" ? "thinking" : item.category;
+    const items = CONVERSATION.filter(item => !categories.length || categories.includes(bucket(item)));
@@ -296 +298,2 @@ try {
-  // Default message filter: chat + error, count 2/4, manual tool toggle adds rows.
+  // Default message filter (task-724): chat + error + Agent Update, count 3/5;
+  // manual tool and Thinking toggles add their rows.
@@ -299 +302 @@ try {
-  assert.match(trigger || "", /2\/4/);
+  assert.match(trigger || "", /3\/5/);
@@ -301 +304 @@ try {
-  assert.deepEqual(initialCategories.sort(), ["chat", "error"]);
+  assert.deepEqual(initialCategories.sort(), ["chat", "error", "update"]);
@@ -303 +306 @@ try {
-  assert.deepEqual(initialIDs.sort(), ["ci-chat", "ci-error"]);
+  assert.deepEqual(initialIDs.sort(), ["ci-chat", "ci-error", "ci-update"]);
@@ -309,2 +312,2 @@ try {
-  assert.deepEqual(toolCategories.sort(), ["chat", "error", "tool"]);
-  assert.match(await page.textContent(".conversation-filter-trigger") || "", /3\/4/);
+  assert.deepEqual(toolCategories.sort(), ["chat", "error", "tool", "update"]);
+  assert.match(await page.textContent(".conversation-filter-trigger") || "", /4\/5/);
@@ -313,5 +316,5 @@ try {
-  await page.click('[data-conversation-category="update"]');
-  await page.waitForFunction(() => document.querySelector('[data-message-id="ci-update"]'), null, {timeout: 10000});
-  const updateCategories = await page.evaluate(() => window.__lastCategories.at(-1) || []);
-  assert.deepEqual(updateCategories.sort(), ["chat", "error", "tool", "update"]);
-  assert.match(await page.textContent(".conversation-filter-trigger") || "", /4\/4/);
+  await page.click('[data-conversation-category="thinking"]');
+  await page.waitForFunction(() => document.querySelector('[data-message-id="ci-thinking"]'), null, {timeout: 10000});
+  const thinkingCategories = await page.evaluate(() => window.__lastCategories.at(-1) || []);
+  assert.deepEqual(thinkingCategories.sort(), ["chat", "error", "thinking", "tool", "update"]);
+  assert.match(await page.textContent(".conversation-filter-trigger") || "", /5\/5/);
```
