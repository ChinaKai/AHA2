// Loaded after the owner opens a task lifecycle.
import {currentLocale} from "./i18n.js";
import type {TaskLifecycle, TaskLifecycleEvent, TaskLine} from "./types.js";

const EN = {
  now: "Now", goal: "Current goal", next: "Next steps", questions: "Open questions",
  stuck: "Waiting on", idle: "Idle for", noStatus: "No current status recorded.", unknown: "Unknown",
  self: "The task can continue", owner: "Owner", other: "Someone else", none: "The next message",
  lines: "Workstreams", noLines: "This task has no recorded workstreams.", closedLines: "{count} closed",
  stream: "Workstream", responsible: "Responsible", taskItself: "Task itself",
  step: "Current step", blocker: "Blocker", updated: "Last updated",
  parallel: "Parallel work", subs: "Sub-agents", noSubs: "No sub-agents recorded.",
  reviews: "Reviews", noReviews: "No related reviews.",
  timeline: "Turning points", noEvents: "No turning points recorded.", noApprovals: "No approval events shown.",
  timelineTruncated: "Showing the latest 200 events.",
  turns: "Turns", turn: "Turn {sequence}", noTurns: "No turns recorded.", turnsTruncated: "Showing the latest 100 turns.",
  stats: "Totals", createdAt: "Created", totalTurns: "Turns", duration: "Total duration", tokens: "Tokens",
  created: "Task created", constraint: "Owner constraint", goalChange: "Goal changed",
  goalSet: "Goal set", goalRevise: "Goal revised", goalPause: "Goal paused", goalResume: "Goal resumed",
  goalComplete: "Goal completed", goalReopen: "Goal reopened",
  approvalRequested: "Approval requested", approvalResolved: "Approval resolved",
  approved: "Approval granted", rejected: "Approval rejected", cancelled: "Approval cancelled",
  review: "Review decision",
  lineOpened: "Workstream opened", lineUpdated: "Workstream updated", lineClosed: "Workstream closed",
  lineReopened: "Workstream reopened", event: "Task event",
  taskCompleted: "Task completed", taskCancelled: "Task cancelled",
  stateOpen: "in progress", stateWaiting: "waiting", stateBlocked: "blocked", stateClosed: "closed",
  blockerIs: "Blocker: {text}",
  reviewApprove: "Approved", reviewChanges: "Changes requested", reviewRequired: "Review required", reviewSkipped: "Review skipped",
} as const;

const ZH: Record<keyof typeof EN, string> = {
  now: "现在", goal: "当前目标", next: "下一步", questions: "待决定的问题",
  stuck: "卡在谁", idle: "停了多久", noStatus: "没有记录当前状态。", unknown: "未知",
  self: "任务自己能继续", owner: "Owner", other: "别人", none: "等下一句话",
  lines: "分线", noLines: "这个任务没有记录分线。", closedLines: "已闭环 {count} 条",
  stream: "线", responsible: "谁负责", taskItself: "任务自己",
  step: "走到哪", blocker: "卡点", updated: "更新时间",
  parallel: "系统知道的并行", subs: "Sub", noSubs: "没有记录派出的 Sub。",
  reviews: "关联评审", noReviews: "没有关联的评审任务。",
  timeline: "转折点", noEvents: "没有记录转折点。", noApprovals: "这里没有审批事件。",
  timelineTruncated: "显示最近 200 条事件。",
  turns: "每一轮", turn: "第 {sequence} 轮", noTurns: "没有轮次记录。", turnsTruncated: "显示最近 100 轮。",
  stats: "几个数", createdAt: "创建时间", totalTurns: "总轮数", duration: "总耗时", tokens: "Token",
  created: "任务创建", constraint: "Owner 定下的约束", goalChange: "目标变更",
  goalSet: "设定目标", goalRevise: "修订目标", goalPause: "暂停目标", goalResume: "继续目标",
  goalComplete: "目标完成", goalReopen: "重开目标",
  approvalRequested: "发起审批", approvalResolved: "审批有结果",
  approved: "审批已批准", rejected: "审批已否决", cancelled: "审批已取消",
  review: "评审结论",
  lineOpened: "分线开启", lineUpdated: "分线更新", lineClosed: "分线闭环",
  lineReopened: "分线重开", event: "任务事件",
  taskCompleted: "任务完成", taskCancelled: "任务取消",
  stateOpen: "进行中", stateWaiting: "等待中", stateBlocked: "受阻", stateClosed: "已闭环",
  blockerIs: "卡点：{text}",
  reviewApprove: "通过", reviewChanges: "要求修改", reviewRequired: "需要评审", reviewSkipped: "跳过评审",
};

export const PET_LIFECYCLE_COPY = {"en-US": EN, "zh-CN": ZH};

type CopyKey = keyof typeof EN;
const copy = (key: CopyKey, values: Record<string, number | string> = {}): string =>
  PET_LIFECYCLE_COPY[currentLocale() === "en-US" ? "en-US" : "zh-CN"][key].replace(/\{(\w+)\}/g, (match, name) => String(values[name] ?? match));

function escapeHTML(value: string): string {
  return value.replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;").replaceAll('"', "&quot;").replaceAll("'", "&#039;");
}

function serverText(value?: string): string {
  return value ? `<span data-i18n-ignore>${escapeHTML(value)}</span>` : "";
}

function empty(key: CopyKey): string {
  return `<p class="field-help">${copy(key)}</p>`;
}

function textList(values: string[] | null | undefined): string {
  return values?.length ? `<ul>${values.map(value => `<li>${serverText(value)}</li>`).join("")}</ul>` : "—";
}

function facts(pairs: Array<[CopyKey, string]>): string {
  return `<dl>${pairs.map(([name, value]) => `<div><dt>${copy(name)}</dt><dd>${value || "—"}</dd></div>`).join("")}</dl>`;
}

function section(key: CopyKey, html: string): string {
  return `<section><h3>${copy(key)}</h3>${html}</section>`;
}

function disclosure(key: CopyKey, html: string, open = false): string {
  return `<details${open ? " open" : ""}><summary>${copy(key)}</summary>${html}</details>`;
}

function lineTable(lines: TaskLine[], date: (value?: string) => string): string {
  const headers: CopyKey[] = ["stream", "responsible", "step", "blocker", "updated"];
  return `<table><thead><tr>${headers.map(header => `<th scope="col">${copy(header)}</th>`).join("")}</tr></thead><tbody>
    ${lines.map(line => {
      const responsible = responsibleFor(line);
      return `<tr><th scope="row">${serverText(line.title) || "—"}</th><td>${responsible}</td><td>${serverText(line.step) || "—"}</td><td>${serverText(line.blocker) || "—"}</td><td>${date(line.updated_at) || "—"}</td></tr>`;
    }).join("")}
  </tbody></table>`;
}

const EVENT_LABELS = new Map<string, CopyKey>([
  ["created", "created"], ["constraint", "constraint"], ["approval_requested", "approvalRequested"],
  ["review", "review"], ["line_opened", "lineOpened"], ["line_updated", "lineUpdated"],
  ["line_closed", "lineClosed"], ["line_reopened", "lineReopened"],
  ["completed", "taskCompleted"], ["cancelled", "taskCancelled"],
]);
// These events are about the task itself; the dialog header already names it.
const UNTITLED_EVENTS = new Set(["created", "completed", "cancelled"]);
const LINE_STATES = new Map<string, CopyKey>([
  ["open", "stateOpen"], ["waiting", "stateWaiting"], ["blocked", "stateBlocked"], ["closed", "stateClosed"],
]);
// A review verdict or the completion declaration, as the server stores them.
const REVIEW_DECISIONS = new Map<string, CopyKey>([
  ["APPROVE", "reviewApprove"], ["REQUEST_CHANGES", "reviewChanges"], ["required", "reviewRequired"], ["skipped", "reviewSkipped"],
]);

function reviewDecision(value: string): string {
  const key = REVIEW_DECISIONS.get(value);
  return key ? copy(key) : serverText(value);
}

function responsibleFor(line: {owner: string; who?: string}): string {
  return line.owner === "other" ? serverText(line.who) || copy("other") : copy(line.owner === "self" ? "taskItself" : "owner");
}

// Who holds the line, where it stands, and what is in the way, in the page's
// language. An older server sends only the plain-text detail.
function lineChange(item: TaskLifecycleEvent): string {
  const line = item.line;
  if (!line) return serverText(item.detail);
  const state = LINE_STATES.get(line.state);
  return [
    responsibleFor(line), state ? copy(state) : serverText(line.state), serverText(line.step),
    line.blocker ? copy("blockerIs", {text: serverText(line.blocker)}) : "",
  ].filter(Boolean).join(" · ");
}

const GOAL_LABELS = new Map<string, CopyKey>([
  ["set", "goalSet"], ["revise", "goalRevise"], ["pause", "goalPause"], ["resume", "goalResume"],
  ["complete", "goalComplete"], ["reopen", "goalReopen"],
]);
const APPROVAL_LABELS = new Map<string, CopyKey>([
  ["approved", "approved"], ["rejected", "rejected"], ["cancelled", "cancelled"],
]);

function milestone(item: TaskLifecycleEvent, date: (value?: string) => string): string {
  const codedTitle = item.kind === "goal" ? GOAL_LABELS.get(item.title || "") :
    item.kind === "approval_resolved" ? APPROVAL_LABELS.get(item.title || "") : undefined;
  const label = codedTitle ?? (item.kind === "goal" ? "goalChange" : item.kind === "approval_resolved" ? "approvalResolved" : EVENT_LABELS.get(item.kind) ?? "event");
  const title = codedTitle || UNTITLED_EVENTS.has(item.kind) || !item.title ? ""
    : item.kind === "review" ? reviewDecision(item.title) : serverText(item.title);
  const detail = item.kind.startsWith("line_") ? lineChange(item) : serverText(item.detail);
  return `<li><div><strong>${copy(label)}</strong>${title ? ` · ${title}` : ""} <small>${date(item.at)}</small></div>
    ${detail ? `<p>${detail}</p>` : ""}</li>`;
}

export interface TaskLifecycleRenderOptions {
  formatTime: (value: string) => string;
  formatDuration: (milliseconds: number) => string;
  statusLabel: (status: string) => string;
  idleDuration: (since: string) => string;
}

export function renderTaskLifecycle(lifecycle: TaskLifecycle, options: TaskLifecycleRenderOptions): string {
  const {task, now, lines, parallel, timeline, turns, stats} = lifecycle;
  const date = (value?: string): string => value && Number.isFinite(Date.parse(value)) && !value.startsWith("0001-01-01")
    ? escapeHTML(options.formatTime(value)) : "";
  const status = (value: string) => serverText(options.statusLabel(value));
  const elapsed = (seconds: number): string => Number.isFinite(seconds) ? escapeHTML(options.formatDuration(seconds * 1000)) : "—";
  const stuck = now.stuck_on === "other" && now.stuck_who?.length
    ? now.stuck_who.map(serverText).join(", ")
    : now.stuck_on ? copy(Object.hasOwn(EN, now.stuck_on) ? now.stuck_on : "unknown") : "";
  const currentFacts: Array<[CopyKey, string]> = [];
  if (task.current_goal) currentFacts.push(["goal", serverText(task.current_goal)]);
  currentFacts.push(["next", textList(now.next_steps)]);
  if (now.open_questions?.length) currentFacts.push(["questions", textList(now.open_questions)]);
  if (stuck) currentFacts.push(["stuck", stuck]);
  const idle = options.idleDuration(now.idle_since);
  if (idle) currentFacts.push(["idle", escapeHTML(idle)]);
  const current = section("now", `${now.status ? `<p>${serverText(now.status)}</p>` : empty("noStatus")}${facts(currentFacts)}`);
  const openLines = lines?.filter(line => line.state !== "closed") ?? [];
  const closedLines = lines?.filter(line => line.state === "closed") ?? [];
  const workstreams = section("lines", !lines?.length ? empty("noLines") :
    (openLines.length ? lineTable(openLines, date) : "") +
    (closedLines.length ? `<details><summary>${copy("closedLines", {count: closedLines.length})}</summary>${lineTable(closedLines, date)}</details>` : ""));
  const subs = parallel.subs?.map(sub =>
    `<li>${serverText(sub.title || sub.agent_id)} · ${status(sub.status)} <small>${date(sub.finished_at) || date(sub.started_at)}</small></li>`).join("");
  const reviews = parallel.reviews?.map(review =>
    `<li>${serverText(review.code || review.task_id)} · ${review.decision ? reviewDecision(review.decision) : status(review.status)}
    <small>${date(review.decided_at)}</small>${review.reason ? `<p>${serverText(review.reason)}</p>` : ""}</li>`).join("");
  const parallelWork = section("parallel", `<div class="pet-lifecycle-parallel">
    <div><h4>${copy("subs")}</h4>${subs ? `<ul>${subs}</ul>` : empty("noSubs")}</div>
    <div><h4>${copy("reviews")}</h4>${reviews ? `<ul>${reviews}</ul>` : empty("noReviews")}</div>
  </div>`);
  const events = (timeline ?? []).map(item => milestone(item, date)).join("");
  const chronology = disclosure("timeline",
    (events ? `<ol>${events}</ol>` : empty("noEvents")) +
    (timeline?.some(item => item.kind === "approval_requested" || item.kind === "approval_resolved") ? "" : empty("noApprovals")) +
    (lifecycle.timeline_truncated ? empty("timelineTruncated") : ""), true);
  const roundRows = turns?.map(turn => {
    const result = serverText(turn.summary || turn.error);
    return `<li><div><strong>${copy("turn", {sequence: turn.sequence})}</strong> · ${status(turn.status)} · ${elapsed(turn.duration_seconds)}
      <small>${date(turn.started_at) || date(turn.finished_at)}</small></div>${result ? `<p>${result}</p>` : ""}</li>`;
  }).join("");
  const rounds = disclosure("turns", (roundRows ? `<ol>${roundRows}</ol>` : empty("noTurns")) +
    (lifecycle.turns_truncated ? empty("turnsTruncated") : ""));
  const numbers = new Intl.NumberFormat(currentLocale());
  const count = (value: number) => Number.isFinite(value) ? escapeHTML(numbers.format(value)) : "—";
  const totals = section("stats", facts([
    ["createdAt", date(task.created_at)], ["totalTurns", count(stats.turns)],
    ["duration", elapsed(stats.duration_seconds)], ["tokens", count(stats.tokens)],
  ]));
  return current + workstreams + parallelWork + chronology + rounds + totals;
}
