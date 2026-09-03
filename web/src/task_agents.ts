import {icon} from "./icons.js";
import type {ConversationItem, Model, TaskAgent, TaskContextDetail, TaskDetail, TaskMemory, Turn} from "./types.js";

export type TaskRealtimeState = "connecting" | "live" | "fallback";

export function visibleAgentText(value: string): string {
  const startMarker = "<aha2_checkpoint>";
  const endMarker = "</aha2_checkpoint>";
  const start = value.lastIndexOf(startMarker);
  if (start < 0) return value.trim();
  const end = value.indexOf(endMarker, start + startMarker.length);
  const visible = (value.slice(0, start) + (end >= 0 ? value.slice(end + endMarker.length) : "")).trim();
  return visible || "Agent 已提交结构化结果";
}

function escapeHTML(value: unknown): string {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

function statusClass(status: string): string {
  if (["idle", "ready", "completed", "succeeded", "waiting_user"].includes(status)) return "good";
  if (["failed", "error", "cancelled"].includes(status)) return "bad";
  if (["active", "running", "starting", "preparing", "queued"].includes(status)) return "info";
  return "warn";
}

function statusLabel(status: string): string {
  const labels: Record<string, string> = {
    idle: "空闲",
    active: "执行中",
    completed: "已完成",
    failed: "失败",
    preparing: "准备中",
    queued: "排队",
    starting: "启动中",
    running: "执行中",
    waiting: "等待",
    succeeded: "成功",
    interrupted: "已中断",
    blocked: "阻塞",
  };
  return labels[status] || status;
}

function timestampMs(value?: string | number): number {
  if (typeof value === "number") return Number.isFinite(value) ? value : 0;
  if (!value) return 0;
  if (/^\d+$/.test(value)) return Number(value);
  const parsed = new Date(value).getTime();
  return Number.isFinite(parsed) ? parsed : 0;
}

function durationMs(start?: string | number, end?: string | number): number {
  const startMs = timestampMs(start);
  const endMs = end ? timestampMs(end) : Date.now();
  return startMs > 0 && endMs > 0 ? Math.max(0, endMs - startMs) : 0;
}

export function formatDuration(ms: number): string {
  if (ms > 0 && ms < 1000) return `${Math.max(1, Math.round(ms))}ms`;
  const seconds = Math.max(0, Math.floor(ms / 1000));
  if (seconds < 60) return `${seconds}s`;
  return `${String(Math.floor(seconds / 60)).padStart(2, "0")}:${String(seconds % 60).padStart(2, "0")}`;
}

function liveElapsedAttributes(elapsedMs: number, running: boolean): string {
  return ` data-live-elapsed-ms="${Math.max(0, Math.round(elapsedMs))}" data-live-synced-at="${performance.now()}"${running ? ' data-live-running="true"' : ""}`;
}

export function isActiveTurn(status: string): boolean {
  return !["succeeded", "failed", "interrupted", "blocked"].includes(status);
}

export function usageNumber(usage: Record<string, number> | undefined, key: string): number {
  const value = Number(usage?.[key] || 0);
  return Number.isFinite(value) ? value : 0;
}

export function compactNumber(value: number): string {
  if (!value) return "-";
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`;
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}K`;
  return String(Math.round(value));
}

function metricNumber(value: number): string {
  return Number.isFinite(value) && value > 0 ? new Intl.NumberFormat("en-US").format(Math.round(value)) : "0";
}

function metricBytes(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return "未知";
  if (value < 1024) return `${metricNumber(value)} B`;
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`;
  return `${(value / 1024 / 1024).toFixed(2)} MB`;
}

export function renderContextMetrics(context: TaskContextDetail["context"], sessionActionDisabled = false): string {
  const metrics = context.metrics || {};
  const controlsDisabled = sessionActionDisabled || !metrics.session_active;
  const sessionID = String(metrics.session_id || "");
  const percent = Number(metrics.context_percent ?? context.context_percent ?? 0);
  const contextTokens = Number(metrics.context_tokens || context.usage?.context_tokens || 0);
  const contextWindow = Number(metrics.context_window || context.context_window || 0);
  const rows = [
    ["Total", metricNumber(Number(metrics.total_tokens || 0)), "history + current (input + output)"],
    ["Input", metricNumber(Number(metrics.input_tokens || 0)), "agent usage"],
    ["Cached", metricNumber(Number(metrics.cached_input_tokens || 0)), "agent usage"],
    ["Output", metricNumber(Number(metrics.output_tokens || 0)), "model output"],
    ["Reasoning", metricNumber(Number(metrics.reasoning_output_tokens || 0)), "subset of output"],
    ["AHA", metricNumber(Number(metrics.aha_prompt_tokens || 0)), `${metricNumber(Number(metrics.aha_prompt_chars || context.prompt_chars || 0))} chars`],
    ["Context", percent ? `${percent.toFixed(1)}%` : "未知", `${compactNumber(contextTokens)} / ${compactNumber(contextWindow)}`],
    ["Session", metricBytes(Number(metrics.session_size_bytes || 0)), metrics.session_exists ? "backend session file" : "session file unavailable", sessionID],
  ];
  return `<section class="ctx-token-grid">${rows.map(([label, value, detail, id]) =>
    `<div class="${id ? "session-metric" : ""}"><small>${label}</small><strong>${value}</strong><span>${detail}</span>${id ? `<code class="session-id" title="${escapeHTML(id)}">${escapeHTML(id)}</code>` : ""}</div>`
  ).join("")}</section><div class="ctx-session-actions"><span>${escapeHTML(context.agent_id || "main")} Session</span><button type="button" data-session-action="compact" ${controlsDisabled ? "disabled" : ""}>Compact</button><button type="button" data-session-action="reset" class="danger" ${controlsDisabled ? "disabled" : ""}>Reset</button></div>`;
}

export function contextPercent(turn: Turn): number {
  const window = Number(turn.context_window || 0);
  if (!window) return 0;
  const usage = turn.usage;
  const contextTokens = usageNumber(usage, "context_tokens");
  const actual = contextTokens || usageNumber(usage, "input_tokens") +
    usageNumber(usage, "cache_read_input_tokens") + usageNumber(usage, "cache_creation_input_tokens");
  const estimated = Number(turn.prompt_chars || 0) / 4;
  return Math.max(0, Math.min(100, (actual || estimated) / window * 100));
}

export function latestAgentTurns(turns: Turn[]): Turn[] {
  const latest = new Map<string, Turn>();
  for (const turn of turns || []) {
    const current = latest.get(turn.agent_id);
    if (!current || turn.generation > current.generation ||
      (turn.generation === current.generation && turn.attempt >= current.attempt)) {
      latest.set(turn.agent_id, turn);
    }
  }
  return [...latest.values()].sort((left, right) =>
    left.agent_id === "main" ? -1 : right.agent_id === "main" ? 1 : left.agent_id.localeCompare(right.agent_id)
  );
}

export function renderAgentTurnCard(detail: TaskDetail, realtimeState: TaskRealtimeState): string {
  const round = detail.latest_round;
  const turns = latestAgentTurns(detail.turns || []);
  if (!round || !turns.length) {
    return `<section class="agent-turn-card empty-turn"><strong>等待下一轮</strong><small>新消息将创建 Agent Turn</small></section>`;
  }
  const active = turns.filter(turn => isActiveTurn(turn.status));
  const failure = [...turns].reverse().find(turn => turn.status === "failed" || turn.status === "blocked");
  const completed = turns.filter(turn => turn.status === "succeeded").length;
  const roundElapsed = Number(round.elapsed_ms || durationMs(
    round.started_at_ms || round.started_at || round.created_at_ms || round.created_at,
    round.finished_at_ms || round.finished_at,
  ));
  const rows = turns.map(turn => {
    const percent = contextPercent(turn);
    const state = turn.attempt > 1 && isActiveTurn(turn.status) ? "retrying" : turn.status;
    const elapsed = Number(turn.elapsed_ms || durationMs(
      turn.started_at_ms || turn.started_at || turn.queued_at_ms || turn.queued_at,
      turn.finished_at_ms || turn.finished_at,
    ));
    return `<div class="agent-turn-row">
      <strong>${escapeHTML(turn.agent_id)}</strong>
      <span class="agent-state ${statusClass(turn.status)}">${escapeHTML(state)}</span>
      <time${liveElapsedAttributes(elapsed, isActiveTurn(turn.status))}>${formatDuration(elapsed)}</time>
      <progress class="context-meter" max="100" value="${percent.toFixed(1)}"></progress>
      <code>ctx ${percent ? percent.toFixed(1) + "%" : "?"}</code>
      ${turn.attempt > 1 ? `<small>Attempt ${turn.attempt}</small>` : "<small></small>"}
    </div>`;
  }).join("");
  const focus = active.find(turn => turn.agent_id === "main") || active[0] || turns[0];
  const usage = focus.usage || {};
  return `<section class="agent-turn-card ${active.length ? "active" : ""}">
    <details>
      <summary><span>Round ${round.sequence} · ${statusLabel(round.status)} · <time${liveElapsedAttributes(roundElapsed, active.length > 0)}>${formatDuration(roundElapsed)}</time> · ${completed}/${turns.length}</span><small>${turns.length} Agents · ${active.length} active</small><code data-round-channel class="round-channel ${realtimeState}">${realtimeState === "live" ? "SSE" : realtimeState === "fallback" ? "2s" : "..."}</code></summary>
      <div class="agent-turn-rows">${rows}</div>
      <div class="turn-stage-grid">
        <span><small>排队</small><strong>${formatDuration(Number(focus.queue_duration_ms || 0))}</strong></span>
        <span><small>构建 / 启动</small><strong>${formatDuration(Number(focus.prepare_duration_ms || 0))}</strong></span>
        <span><small>Agent 执行</small><strong${liveElapsedAttributes(Number(focus.run_duration_ms || 0), isActiveTurn(focus.status) && Boolean(focus.started_at_ms || focus.started_at))}>${formatDuration(Number(focus.run_duration_ms || 0))}</strong></span>
        <span><small>Input</small><strong>${compactNumber(usageNumber(usage, "context_tokens") || usageNumber(usage, "input_tokens"))}</strong></span>
        <span><small>Cache</small><strong>${compactNumber(usageNumber(usage, "cached_input_tokens") || usageNumber(usage, "cache_read_input_tokens"))}</strong></span>
        <span><small>Output</small><strong>${compactNumber(usageNumber(usage, "output_tokens"))}</strong></span>
      </div>
      ${failure ? `<div class="turn-failure"><strong>${escapeHTML(failure.agent_id)} 失败</strong><span>${escapeHTML(failure.error || "Backend 执行失败，未返回详细原因")}</span></div>` : ""}
    </details>
  </section>`;
}

function agentSummary(agent: TaskAgent): string {
  const runtime = [agent.backend, agent.model_name || agent.model_id].filter(Boolean).join(" · ");
  return runtime || (agent.inherit_main ? "继承 Main 配置" : "等待配置");
}

export function renderTaskAgentSelectors(
  detail: TaskDetail,
  selectedAgentID: string,
  variant: "desktop" | "mobile",
): string {
  const agents = detail.agents || [];
  if (!agents.length) return "<p>尚无 Agent</p>";
  return agents.map(agent => `<div class="task-agent-selector ${variant} ${agent.agent_id === selectedAgentID ? "selected" : ""}">
    <button type="button" data-agent-select="${escapeHTML(agent.agent_id)}">
      <span><strong>${escapeHTML(agent.agent_id)}</strong><small>${escapeHTML(agentSummary(agent))}</small></span>
      <span class="agent-selector-state ${statusClass(agent.status)}">${statusLabel(agent.status)}</span>
      ${agent.unread_count ? `<b>${agent.unread_count > 99 ? "99+" : agent.unread_count}</b>` : ""}
    </button>
    <button type="button" data-agent-config="${escapeHTML(agent.agent_id)}" class="icon-button" title="编辑 Agent 配置">${icon("edit")}</button>
  </div>`).join("");
}

export function renderTaskContextSummary(detail: TaskDetail): string {
  return latestAgentTurns(detail.turns || []).map(turn =>
    `${escapeHTML(turn.agent_id)} · ctx ${contextPercent(turn) ? contextPercent(turn).toFixed(1) + "%" : "窗口未配置"}`
  ).join("<br>") || "-";
}

export function renderTaskMemory(memory?: TaskMemory): string {
  const list = (title: string, values: string[] = []) =>
    `<section class="memory-block"><strong>${title}</strong>${values.length ? `<ul>${values.map(item => `<li>${escapeHTML(item)}</li>`).join("")}</ul>` : "<small>-</small>"}</section>`;
  return `<div class="memory-goal"><small>Current Goal</small><p>${escapeHTML(memory?.current_goal || "-")}</p></div>${list("事实", memory?.facts)}${list("决策", memory?.decisions)}${list("排除项", memory?.excluded)}${list("进度", memory?.progress)}${list("验证", memory?.verification)}${list("下一步", memory?.next_actions)}`;
}

function renderOrchestrationCard(item: ConversationItem, detail: TaskDetail): string {
  const payload = item.payload || {};
  const parentTurnID = String(payload.parent_turn_id || item.turn_id || "");
  const agentIDs = Array.isArray(payload.agent_ids) ? payload.agent_ids.map(String) : [];
  const routeDetails = Array.isArray(payload.agent_routes) ? payload.agent_routes as Array<Record<string, unknown>> : [];
  const rows = agentIDs.map(agentID => {
    const route = routeDetails.find(candidate => String(candidate.agent_id || "") === agentID);
    const turn = [...(detail.turns || [])].reverse().find(candidate =>
      candidate.agent_id === agentID && candidate.parent_turn_id === parentTurnID
    );
    const agent = (detail.agents || []).find(candidate => candidate.agent_id === agentID);
    const status = String(route?.status || turn?.status || agent?.status || "queued");
    return `<div><strong>${escapeHTML(agentID)}</strong><span title="${escapeHTML(route?.assignment || "")}">${escapeHTML(route?.title || agent?.title || turn?.title || "子 Agent")}</span><b class="${statusClass(status)}">${statusLabel(status)}</b></div>`;
  }).join("");
  const time = new Date(item.created_at).toLocaleTimeString([], {hour: "2-digit", minute: "2-digit", second: "2-digit"});
  return `<article class="aha-orchestration-card"><header><strong>${icon("bot")}AHA 系统路由</strong><time>${time}</time></header><p>${escapeHTML(item.summary || `AHA 已向 ${agentIDs.length} 个子 Agent 路由任务`)}</p><div>${rows}</div></article>`;
}

export function renderConversationWithOrchestration(
  items: ConversationItem[],
  detail: TaskDetail | null,
  renderItem: (item: ConversationItem) => string,
): string {
  return items.map(item =>
    item.kind === "agent_batch_dispatched" && detail ? renderOrchestrationCard(item, detail) : renderItem(item)
  ).join("");
}

export function renderAgentConfigDialog(detail: TaskDetail, agentID: string, models: Model[]): string {
  const agent = (detail.agents || []).find(item => item.agent_id === agentID);
  if (!agent) return "";
  const effortLevels = agent.backend === "claude"
    ? ["low", "medium", "high", "xhigh", "max"]
    : ["low", "medium", "high", "xhigh"];
  const modelOptions = models.map(model =>
    `<option value="${escapeHTML(model.id)}" data-backend="${escapeHTML(model.backend)}" ${model.id === agent.model_id ? "selected" : ""}>${escapeHTML(model.display_name)} · ${escapeHTML(model.backend)}</option>`
  ).join("");
  const mainSettings = agent.agent_id === "main" ? `<fieldset class="agent-collaboration-settings"><legend>Task 协作</legend><div class="two">
    <label>模式<select name="collaboration_mode"><option value="single" ${detail.task.collaboration_mode === "single" ? "selected" : ""}>Single</option><option value="auto" ${detail.task.collaboration_mode !== "single" ? "selected" : ""}>Auto</option></select></label>
    <label>最大 Agent 数<input name="max_agents" type="number" min="1" value="${Math.max(1, Number(detail.task.max_agents || 3))}"></label>
  </div></fieldset>` : `<label class="agent-inherit-toggle"><input name="inherit_main" type="checkbox" ${agent.inherit_main ? "checked" : ""}>继承 Main 当前配置</label>`;
  return `<dialog id="agent-config-dialog"><form id="agent-config-form" method="dialog">
    <div class="dialog-head"><div><h2>${escapeHTML(agent.agent_id)} 配置</h2><small>${escapeHTML(agent.title || agent.role)}</small></div><button type="button" data-close class="icon-button">${icon("close")}</button></div>
    <input type="hidden" name="agent_id" value="${escapeHTML(agent.agent_id)}">
    ${mainSettings}
    <div class="two agent-runtime-fields">
      <label>Backend<select name="backend" id="agent-config-backend"><option value="codex" ${agent.backend === "codex" ? "selected" : ""}>Codex</option><option value="claude" ${agent.backend === "claude" ? "selected" : ""}>Claude Code</option></select></label>
      <label>模型<select name="model_id" id="agent-config-model">${modelOptions}</select></label>
    </div>
    <div class="two agent-runtime-fields">
      <label>推理强度<select name="reasoning_effort" id="agent-config-effort">${effortLevels.map(level => `<option value="${level}" ${level === agent.reasoning_effort ? "selected" : ""}>${level}</option>`).join("")}</select></label>
      <label>沙箱<select name="filesystem"><option value="read-only" ${agent.filesystem === "read-only" ? "selected" : ""}>只读</option><option value="workspace-write" ${agent.filesystem === "workspace-write" ? "selected" : ""}>工作区可写</option><option value="danger-full-access" ${agent.filesystem === "danger-full-access" ? "selected" : ""}>完全访问</option></select></label>
    </div>
    <label class="agent-runtime-fields">审批<select name="approval"><option value="never" ${agent.approval !== "auto" ? "selected" : ""}>无需确认</option><option value="auto" ${agent.approval === "auto" ? "selected" : ""}>自动批准</option></select></label>
    <div class="dialog-actions"><button type="button" data-close>取消</button><button class="primary" value="default">保存</button></div>
  </form></dialog>`;
}
