// Pet workbench page renderers that only the workbench needs: the overview
// (the desk and the pre-desk fallback), the "last 7 days" block and the
// records tab. They load as a route chunk when the workbench opens, so the
// first screen of every other page does not carry them. The loader, page
// keeper, tab mapping and approvals-tab helpers stay in pet_workbench.ts,
// which the shell needs before this chunk arrives.
import type {PetApproval, PetChannelHealth, PetCorrectionRecord, PetDesk, PetDeskCorrection, PetDeskLastReply, PetDeskNeed, PetDeskRun, PetDeskWait, PetInsights, PetInspection, PetKnowledgeHealth, PetKnowledgePending, PetLedgerCounts, PetTaskWatch} from "./types.js";
import {currentLocale} from "./i18n.js";
import {petApprovalDurationText, petApprovalRiskLabel} from "./pet_approvals.js";
import {petWatchActionKeys} from "./ui_helpers.js";

type Translate = (key: string, values?: Record<string, string | number>) => string;
type FormatTime = (value: string) => string;

function escapeHTML(value: unknown): string {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

function textValue(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

// Counts come from JSON as numbers. Reject strings, null and NaN instead of
// coercing them to 0, which would render dirty values as a fake healthy zero.
function countValue(value: unknown): number | null {
  if (typeof value !== "number" || !Number.isFinite(value) || value < 0) return null;
  return Math.trunc(value);
}

function uptimeText(minutes: number, translate: Translate): string {
  if (minutes < 60) return translate("petWorkbench.uptimeMinutes", {minutes});
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return translate("petWorkbench.uptimeHours", {hours, minutes: minutes % 60});
  return translate("petWorkbench.uptimeDays", {days: Math.floor(hours / 24), hours: hours % 24});
}

export interface PetDocEditorBinding {
  /** Fallback section for entries without their own data attribute. */
  section?: string;
  translate: Translate;
  save: (section: string, name: string, content: string) => Promise<void>;
  reload: () => Promise<void>;
  /** Restore the read-only view, normally by re-selecting the section tab. */
  cancel: () => void;
}

// bindPetDocEditorButtons wires every rendered `.pet-doc-entry` edit button.
// The button is identified by `data-pet-doc-edit` (the reader and writer must
// agree on that attribute), and the enclosing details is opened first: core
// documents now start collapsed, so an editor inserted into a closed details
// would be invisible and focus would land off screen.
export function bindPetDocEditorButtons(root: ParentNode, binding: PetDocEditorBinding): void {
  root.querySelectorAll<HTMLButtonElement>("[data-pet-doc-edit]").forEach(button => {
    button.addEventListener("click", event => {
      event.preventDefault();
      event.stopPropagation();
      const details = button.closest("details");
      const pre = details?.querySelector<HTMLElement>("pre");
      if (!details || !pre) return;
      details.open = true;
      const name = button.dataset.petDocName || "";
      // The merged records tab renders core and worklog entries in one body,
      // so each button carries its own section instead of relying on one
      // section per binding.
      const section = button.dataset.petDocSection || binding.section || "";
      const editor = document.createElement("div");
      editor.className = "pet-doc-editor";
      const textarea = document.createElement("textarea");
      textarea.value = pre.textContent || "";
      textarea.rows = Math.max(8, Math.min(24, textarea.value.split("\n").length + 2));
      const actions = document.createElement("div");
      actions.className = "pet-doc-editor-actions";
      const save = document.createElement("button");
      save.type = "button";
      save.className = "primary";
      save.textContent = binding.translate("common.save");
      const cancel = document.createElement("button");
      cancel.type = "button";
      cancel.textContent = binding.translate("common.cancel");
      actions.append(save, cancel);
      editor.append(textarea, actions);
      pre.replaceWith(editor);
      button.remove();
      textarea.focus();
      cancel.addEventListener("click", binding.cancel);
      save.addEventListener("click", async () => {
        save.disabled = true;
        try {
          await binding.save(section, name, textarea.value);
          await binding.reload();
        } catch (error) {
          save.disabled = false;
          editor.insertAdjacentHTML("beforeend", `<div class="field-help">${escapeHTML(error instanceof Error ? error.message : String(error))}</div>`);
        }
      });
    });
  });
}


// Task states that mean the pet's watched work needs the owner. "waiting_user"
// is not an anomaly: it is normally the approval itself.
export const PET_WATCH_ANOMALY_STATUSES = ["failed", "blocked", "interrupted", "error"] as const;

export function petWatchAnomaly(item: {status?: string}): boolean {
  const status = String(item.status ?? "").trim().toLowerCase();
  return (PET_WATCH_ANOMALY_STATUSES as readonly string[]).includes(status);
}

export function countPetWatchAnomalies(items: Array<{status?: string; watched?: boolean; ignored?: boolean}>): number {
  return items.filter(item => item.watched !== false && !item.ignored && petWatchAnomaly(item)).length;
}

export interface PetOverviewOptions {
  inspection?: PetInspection | null;
  approvals?: PetApproval[];
  watches?: PetTaskWatch[];
  channels?: PetChannelHealth[];
  channelTotal?: number;
  pendingApprovals?: number;
  translate: Translate;
  formatTime: FormatTime;
  statusLabel?: (status: string) => string;
  now?: number;
  /** The desk view; absent means an older server or a failed desk read. */
  desk?: PetDesk | null;
}

function overviewStatusLabel(status: string, options: PetOverviewOptions): string {
  if (options.statusLabel) return options.statusLabel(status);
  const key = `petWorkbench.status.${status}`;
  const label = options.translate(key);
  return label === key ? status : label;
}

function watchReasonLabel(reason: string, translate: Translate): string {
  const key = `petWorkbench.reason.${reason}`;
  const label = translate(key);
  return label === key ? reason : label;
}

function watchActionButtons(item: PetTaskWatch, translate: Translate): string {
  return petWatchActionKeys(item).map(action => {
    switch (action) {
      case "follow": return `<button type="button" data-pet-watch-follow data-pet-watch-task="${escapeHTML(item.task_id)}">${escapeHTML(translate("petWorkbench.followTask"))}</button>`;
      case "unfollow": return `<button type="button" data-pet-watch-unfollow data-pet-watch-task="${escapeHTML(item.task_id)}">${escapeHTML(translate("petWorkbench.unfollowTask"))}</button>`;
      case "ignore": return `<button type="button" data-pet-watch-ignore="true" data-pet-watch-task="${escapeHTML(item.task_id)}">${escapeHTML(translate("petWorkbench.ignoreTask"))}</button>`;
      case "restoreIgnored": return `<button type="button" data-pet-watch-ignore="false" data-pet-watch-task="${escapeHTML(item.task_id)}">${escapeHTML(translate("petWorkbench.restoreIgnored"))}</button>`;
      case "restoreFollow": return `<button type="button" data-pet-watch-ignore="false" data-pet-watch-task="${escapeHTML(item.task_id)}">${escapeHTML(translate("petWorkbench.restoreFollow"))}</button>`;
      default: return "";
    }
  }).join("");
}

// A watched task is one compact row: title, status and last change on the
// left, open plus the state-specific follow action on the right. Anomalous
// tasks are tinted so the overview card count points to something visible
// without opening every row.
function renderWatchRow(item: PetTaskWatch, options: PetOverviewOptions): string {
  const title = `${item.code || item.task_id} · ${item.title || item.task_id}`;
  const anomaly = petWatchAnomaly(item);
  const meta = `${overviewStatusLabel(item.status, options)} · ${options.formatTime(item.updated_at)}`;
  return `<article class="pet-watch-row${anomaly ? " anomaly" : ""}">
    <button type="button" class="pet-watch-row-main" data-pet-overview-task="${escapeHTML(item.task_id)}">
      <strong>${escapeHTML(title)}</strong>
      <small>${escapeHTML(meta)}${item.reason ? ` · ${escapeHTML(watchReasonLabel(item.reason, options.translate))}` : ""}</small>
    </button>
    <div class="pet-watch-row-actions">
      <button type="button" data-pet-overview-task="${escapeHTML(item.task_id)}">${escapeHTML(options.translate("petWorkbench.openTask"))}</button>
      ${watchActionButtons(item, options.translate)}
    </div>
  </article>`;
}

function overviewCard(jump: string, count: number, label: string, hint: string, extraClass = ""): string {
  return `<button type="button" class="pet-overview-card ${extraClass}" data-pet-overview-jump="${jump}">
    <strong>${count}</strong>
    <span class="pet-overview-card-label">${escapeHTML(label)}</span>
    <small>${escapeHTML(hint)}</small>
  </button>`;
}

// renderOverviewWatchLists renders the three collapsed follow lists shared by
// the legacy overview and the desk: watched, ignored and (when non-empty)
// unfollowed tasks.
function renderOverviewWatchLists(watches: PetTaskWatch[], options: PetOverviewOptions): {watched: PetTaskWatch[]; lists: string} {
  const translate = options.translate;
  // Deliberately not virtualized. The 2026-11 fixture with 500 watches (200
  // followed + 300 other/ignored) rendered 500 rows in two collapsed <details>,
  // each row a fixed 54px. The list is static unless the owner changes a
  // follow, so the DOM cost is bounded and paid once, unlike the conversation
  // which streams and measures variable rows. If the server ever returns a
  // watch list materially above 500, apply the conversation window pattern.
  const watched = watches.filter(item => item.watched && !item.ignored);
  const ignored = watches.filter(item => item.ignored);
  const unfollowed = watches.filter(item => !item.watched && !item.ignored);
  const followRows = watched.length
    ? watched.map(item => renderWatchRow(item, options)).join("")
    : `<div class="field-help">${escapeHTML(translate("petWorkbench.emptyFollows"))}</div>`;
  const followTitle = translate("petWorkbench.overviewFollows", {count: String(watched.length)});
  const ignoredTitle = translate("petWorkbench.otherFollows", {count: String(ignored.length)});
  const followDetails = `<details class="pet-overview-follows" id="pet-overview-follows" aria-label="${escapeHTML(followTitle)}"><summary>${escapeHTML(followTitle)}</summary><div class="pet-overview-follows-body">${followRows}</div></details>`;
  const ignoredRows = ignored.length
    ? ignored.map(item => renderWatchRow(item, options)).join("")
    : `<div class="field-help">${escapeHTML(translate("petWorkbench.emptyIgnored"))}</div>`;
  const ignoredDetails = `<details class="pet-overview-other" id="pet-overview-ignored" aria-label="${escapeHTML(ignoredTitle)}"><summary>${escapeHTML(ignoredTitle)}</summary><div class="pet-overview-other-body">${ignoredRows}</div></details>`;
  const unfollowedTitle = translate("petWorkbench.unfollowedTasks", {count: String(unfollowed.length)});
  const unfollowedDetails = unfollowed.length
    ? `<details class="pet-overview-other" id="pet-overview-unfollowed" aria-label="${escapeHTML(unfollowedTitle)}"><summary>${escapeHTML(unfollowedTitle)}</summary><div class="pet-overview-other-body">${unfollowed.map(item => renderWatchRow(item, options)).join("")}</div></details>`
    : "";

  return {watched, lists: `${followDetails}
    ${ignoredDetails}
    ${unfollowedDetails}`};
}

// renderPetOverview is the landing tab. With a desk (task-813) it is the pet's
// desk: what waits on the Owner, what moves without them, what runs, and the
// pet's own side. Without one it is the pre-desk overview: three numerals that
// jump to their page, the compact follow list and the last few changes.
export function renderPetOverview(options: PetOverviewOptions): string {
  if (petDeskPresent(options.desk)) return renderPetDesk(options.desk, options);
  return renderLegacyPetOverview(options);
}

// A desk is a JSON object. An array (or anything else) from a broken server is
// treated like a missing desk, so the pre-desk overview renders instead of an
// empty desk built from an array's missing fields.
export function petDeskPresent(desk: unknown): desk is PetDesk {
  return Boolean(desk) && typeof desk === "object" && !Array.isArray(desk);
}

// renderLegacyPetOverview is the pre-desk overview, kept for servers that do
// not return `desk` and for a desk read that failed on the server.
function renderLegacyPetOverview(options: PetOverviewOptions): string {
  const translate = options.translate;
  const inspection = options.inspection;
  const approvals = options.approvals || [];
  const watches = options.watches || [];
  const channels = options.channels || [];
  const pending = typeof options.pendingApprovals === "number" && Number.isFinite(options.pendingApprovals)
    ? Math.max(0, Math.trunc(options.pendingApprovals))
    : approvals.filter(item => item.status === "pending").length;
  const anomalies = countPetWatchAnomalies(watches);
  const degraded = channels.filter(item => item.degraded).length;
  const channelTotal = typeof options.channelTotal === "number" && options.channelTotal >= 0 ? options.channelTotal : channels.length;
  const health = textValue(inspection?.health);
  const healthLabel = health === "ok"
    ? translate("petWorkbench.inspectionHealthy")
    : health === "degraded"
      ? translate("petWorkbench.inspectionDegraded")
      : translate("petWorkbench.inspectionUnknown");
  const healthClass = health === "ok" ? "ok" : health === "degraded" ? "degraded" : "unknown";

  const meta: string[] = [];
  const version = textValue(inspection?.version);
  const webVersion = textValue(inspection?.web_version);
  if (version) meta.push(`${translate("petWorkbench.inspectionVersion")}: ${escapeHTML(version)}`);
  if (webVersion) meta.push(`${translate("petWorkbench.inspectionWebVersion")}: ${escapeHTML(webVersion)}`);
  const uptime = countValue(inspection?.uptime_minutes);
  if (uptime !== null) meta.push(`${translate("petWorkbench.inspectionUptime")}: ${escapeHTML(uptimeText(uptime, translate))}`);
  const checkedAt = textValue(inspection?.checked_at);
  if (checkedAt) meta.push(`${translate("petWorkbench.inspectionCheckedAt")}: ${escapeHTML(options.formatTime(checkedAt))}`);

  const {watched, lists: watchLists} = renderOverviewWatchLists(watches, options);

  const activities: Array<{time: string; html: string}> = [];
  for (const item of approvals) {
    activities.push({
      time: item.resolved_at || item.created_at || "",
      html: `<button type="button" class="pet-overview-activity" data-pet-approval-open="${escapeHTML(item.id)}"><span class="pet-overview-activity-kind">${escapeHTML(translate("petWorkbench.overviewApprovalKind"))}</span><strong>${escapeHTML(item.title || "")}</strong><small>${escapeHTML(options.formatTime(item.created_at || ""))}</small></button>`,
    });
  }
  for (const item of watched) {
    activities.push({
      time: item.updated_at || "",
      html: `<button type="button" class="pet-overview-activity" data-pet-overview-task="${escapeHTML(item.task_id)}"><span class="pet-overview-activity-kind">${escapeHTML(translate("petWorkbench.overviewTaskKind"))}</span><strong>${escapeHTML(item.title || item.task_id)}</strong><small>${escapeHTML(options.formatTime(item.updated_at || ""))}</small></button>`,
    });
  }
  activities.sort((left, right) => {
    const leftTime = Date.parse(left.time) || 0;
    const rightTime = Date.parse(right.time) || 0;
    return rightTime - leftTime;
  });
  const recent = activities.length
    ? activities.slice(0, 5).map(item => item.html).join("")
    : `<div class="field-help">${escapeHTML(translate("petWorkbench.overviewEmptyActivity"))}</div>`;

  return `<div class="pet-overview">
    <section class="pet-overview-status">
      <header><strong>${escapeHTML(translate("petWorkbench.overviewStatusTitle"))}</strong><span class="pet-inspection-health ${healthClass}">${escapeHTML(healthLabel)}</span></header>
      ${meta.length ? `<div class="pet-inspection-meta">${meta.map(item => `<span>${item}</span>`).join("")}</div>` : ""}
    </section>
    <div class="pet-overview-cards">
      ${overviewCard("approvals", pending, translate("petWorkbench.overviewPending"), translate("petWorkbench.overviewPendingHint"))}
      ${overviewCard("follows", anomalies, translate("petWorkbench.overviewAnomalies"), translate("petWorkbench.overviewAnomaliesHint"), anomalies ? "attention" : "")}
      ${overviewCard("channels", degraded, translate("petWorkbench.overviewChannels"), translate("petWorkbench.overviewChannelsHint", {total: String(channelTotal)}), degraded ? "attention" : "")}
    </div>
    ${watchLists}
    <section class="pet-overview-recent">
      <h3>${escapeHTML(translate("petWorkbench.overviewRecent"))}</h3>
      <div class="pet-overview-activity-list">${recent}</div>
    </section>
  </div>`;
}

/* Pet desk (task-813): the overview grouped by who has to move ------------- */

// Circled numerals cover the 50-row needs_owner bound: ①–⑳ U+2460, ㉑–㉟
// U+3251, ㊱–㊿ U+32B1. Anything past that (a future bigger bound) falls back
// to a plain number instead of an unrelated glyph.
export function petDeskIndexLabel(position: number): string {
  const value = Math.trunc(position);
  if (value >= 1 && value <= 20) return String.fromCodePoint(0x2460 + value - 1);
  if (value >= 21 && value <= 35) return String.fromCodePoint(0x3251 + value - 21);
  if (value >= 36 && value <= 50) return String.fromCodePoint(0x32b1 + value - 36);
  return `(${value})`;
}

// petDeskElapsedMinutes is how long an item has waited or run. The server
// sends Go's zero time ("0001-01-01T00:00:00Z") when it has no timestamp, so
// anything at or before the Unix epoch is unknown rather than "2000 years". A
// timestamp slightly ahead of the browser clock counts as zero.
export function petDeskElapsedMinutes(since: unknown, now: number): number | null {
  if (typeof since !== "string" || !since.trim()) return null;
  const start = Date.parse(since);
  if (!Number.isFinite(start) || start <= 0 || !Number.isFinite(now)) return null;
  return Math.max(0, Math.floor((now - start) / 60_000));
}

// petDeskElapsedText renders the elapsed time with `key` ("waited" or "ran"),
// or "" when the timestamp is unknown so the row shows no duration at all.
export function petDeskElapsedText(since: unknown, now: number, key: string, translate: Translate): string {
  const minutes = petDeskElapsedMinutes(since, now);
  if (minutes === null) return "";
  return translate(key, {duration: petApprovalDurationText(minutes, translate)});
}

function deskTotal(total: unknown, shown: number): number {
  const value = countValue(total);
  return value === null ? shown : Math.max(value, shown);
}

// List rows are objects; a nested array or a scalar is not a row.
function deskList<T>(value: unknown): T[] {
  return Array.isArray(value) ? value.filter(item => item && typeof item === "object" && !Array.isArray(item)) as T[] : [];
}

function deskStrings(value: unknown): string[] {
  return Array.isArray(value) ? value.map(textValue).filter(Boolean) : [];
}

// Server text is user content (titles, questions, notes). It is escaped and
// marked data-i18n-ignore so the English UI pass never rewrites it.
function deskText(value: string, tag = "span", className = ""): string {
  return `<${tag}${className ? ` class="${className}"` : ""} data-i18n-ignore>${escapeHTML(value)}</${tag}>`;
}

function deskTruncated(total: number, shown: number, translate: Translate): string {
  if (total <= shown) return "";
  return `<div class="field-help pet-desk-more">${escapeHTML(translate("petWorkbench.desk.notShown", {count: String(total - shown)}))}</div>`;
}

// The kinds below are the desk API contract (internal/app/pet_desk.go). A kind
// this page does not know yet gets the generic label of its list instead of an
// empty cell or a raw catalog key.
const PET_DESK_KINDS: Record<string, readonly string[]> = {
  need: ["approval", "question", "blocked", "failed", "stopped"],
  later: ["idea", "next_step"],
  wait: ["review", "executing", "channel", "loop"],
};

export function petDeskKnownKind(list: "need" | "later" | "wait", kind: unknown): boolean {
  return typeof kind === "string" && PET_DESK_KINDS[list].includes(kind);
}

function deskKindLabel(list: "need" | "later", kind: string, translate: Translate, values: Record<string, string> = {}): string {
  const key = petDeskKnownKind(list, kind) ? kind : "other";
  return translate(`petWorkbench.desk.${list}.${key}`, values);
}

// Kinds become a class name; anything outside the contract's alphabet is
// "other" so a server value can never add arbitrary classes.
function deskKindClass(kind: string): string {
  return /^[a-z_]+$/.test(kind) ? kind : "other";
}

function deskTaskLabel(item: {task_code?: string; task_id?: string}): string {
  return textValue(item.task_code) || textValue(item.task_id);
}

// deskTitleHTML is the row title: the server title, else the task code, else
// a translated placeholder (UI text, so it is not marked data-i18n-ignore).
function deskTitleHTML(item: {title?: string; task_code?: string; task_id?: string}, translate: Translate): string {
  const title = textValue(item.title) || deskTaskLabel(item);
  return title ? deskText(title) : escapeHTML(translate("petWorkbench.desk.untitled"));
}

// deskHeadlineHTML leads with the task code when it adds something, so a
// review or running row reads "task-805 · title".
function deskHeadlineHTML(item: {title?: string; task_code?: string; task_id?: string}, translate: Translate): string {
  const code = deskTaskLabel(item);
  const title = textValue(item.title);
  if (code && title && code !== title) return `${deskText(code)} · ${deskText(title)}`;
  return deskTitleHTML(item, translate);
}

function deskOpenTaskButton(taskID: string, translate: Translate): string {
  return `<button type="button" data-pet-overview-task="${escapeHTML(taskID)}">${escapeHTML(translate("petWorkbench.desk.openTask"))}</button>`;
}

// A needs/later row: the main button opens the approval detail for approvals
// and ideas, otherwise the Task. Approvals tied to a Task also get an explicit
// open-task button. There is deliberately no approve/reject on the desk: a
// delete approval must not be one click away from a one-line summary.
function renderDeskNeed(item: PetDeskNeed, position: number | null, options: PetOverviewOptions, now: number): string {
  const translate = options.translate;
  const kind = textValue(item.kind);
  const approvalID = textValue(item.approval_id);
  const taskID = textValue(item.task_id);
  const isApproval = (kind === "approval" || kind === "idea") && approvalID !== "";
  const questions = deskStrings(item.questions);
  const list = position === null ? "later" : "need";
  const kindLabel = deskKindLabel(list, kind, translate, {count: String(questions.length)});
  const meta: string[] = [escapeHTML(kindLabel)];
  const taskLabel = deskTaskLabel(item);
  if (taskLabel && textValue(item.title) !== taskLabel) meta.push(deskText(taskLabel));
  if (kind === "approval" && textValue(item.risk)) meta.push(escapeHTML(petApprovalRiskLabel(String(item.risk), translate)));
  const waited = petDeskElapsedText(item.since, now, "petWorkbench.desk.waited", translate);
  if (waited) meta.push(escapeHTML(waited));
  const target = isApproval
    ? `data-pet-approval-open="${escapeHTML(approvalID)}"`
    : taskID ? `data-pet-overview-task="${escapeHTML(taskID)}"` : "";
  const head = `<strong>${deskTitleHTML(item, translate)}</strong><small>${meta.join(" · ")}</small>`;
  const main = target
    ? `<button type="button" class="pet-desk-item-main" ${target}>${head}</button>`
    : `<div class="pet-desk-item-main">${head}</div>`;
  const detail = textValue(item.detail);
  let body = "";
  if (kind === "question" && questions.length) {
    body = `<ul class="pet-desk-questions">${questions.map(question => deskText(question, "li")).join("")}</ul>`;
  } else if (detail) {
    const label = kind === "next_step" ? `${escapeHTML(translate("petWorkbench.desk.nextStep"))}` : "";
    body = `<p class="pet-desk-detail">${label}${deskText(detail)}</p>`;
  }
  const actions: string[] = [];
  if (isApproval) actions.push(`<button type="button" data-pet-approval-open="${escapeHTML(approvalID)}">${escapeHTML(translate("petWorkbench.desk.review"))}</button>`);
  if (taskID) actions.push(deskOpenTaskButton(taskID, translate));
  const index = position === null ? "" : `<span class="pet-desk-index" aria-hidden="true">${petDeskIndexLabel(position)}</span>`;
  return `<li class="pet-desk-item kind-${deskKindClass(kind)}">
    ${index}
    <div class="pet-desk-item-body">${main}${body}</div>
    ${actions.length ? `<div class="pet-desk-item-actions">${actions.join("")}</div>` : ""}
  </li>`;
}

function renderDeskWait(item: PetDeskWait, options: PetOverviewOptions, now: number): string {
  const translate = options.translate;
  const kind = textValue(item.kind);
  const taskID = textValue(item.task_id);
  const on = textValue(item.on);
  const count = countValue(item.count) ?? 0;
  const meta: string[] = [];
  let headline: string;
  let target = taskID ? `data-pet-overview-task="${escapeHTML(taskID)}"` : "";
  if (kind === "review") {
    headline = deskHeadlineHTML(item, translate);
    meta.push(escapeHTML(translate("petWorkbench.desk.wait.review")) + (on ? ` → ${deskText(on)}` : ""));
  } else if (kind === "executing") {
    headline = deskTitleHTML(item, translate);
    meta.push(escapeHTML(translate("petWorkbench.desk.wait.executing")));
    const taskLabel = deskTaskLabel(item);
    if (taskLabel) meta.push(deskText(taskLabel));
  } else if (kind === "channel") {
    headline = deskTitleHTML(item, translate);
    meta.push(escapeHTML(translate("petWorkbench.desk.wait.channel", {count: String(count)})));
    // Channel rows jump to the channel cards; their `on` is the channel id.
    target = `data-pet-overview-jump="channels"`;
  } else if (kind === "loop") {
    // A waiting row the pet wrote in its ledger; the file has no time, so
    // the row shows the next step instead of a duration.
    headline = deskTitleHTML(item, translate);
    meta.push(escapeHTML(translate("petWorkbench.desk.wait.loop")));
    if (on) meta.push(deskText(on));
    target = `data-pet-overview-jump="records"`;
  } else {
    // Unknown wait kind: keep the row readable with the generic label.
    headline = deskTitleHTML(item, translate);
    meta.push(escapeHTML(translate("petWorkbench.desk.wait.other")));
  }
  const waited = petDeskElapsedText(item.since, now, kind === "channel" ? "petWorkbench.desk.oldestWaited" : "petWorkbench.desk.waited", translate);
  if (waited) meta.push(escapeHTML(waited));
  const head = `<strong>${headline}</strong><small>${meta.join(" · ")}</small>`;
  const main = target
    ? `<button type="button" class="pet-desk-item-main" ${target}>${head}</button>`
    : `<div class="pet-desk-item-main">${head}</div>`;
  const detail = textValue(item.detail);
  const next = kind === "loop" && detail
    ? `<p class="pet-desk-detail">${escapeHTML(translate("petWorkbench.desk.nextStep"))}${deskText(detail)}</p>`
    : "";
  return `<li class="pet-desk-item kind-${deskKindClass(kind)}"><div class="pet-desk-item-body">${main}${next}</div></li>`;
}

function renderDeskRun(item: PetDeskRun, options: PetOverviewOptions, now: number): string {
  const translate = options.translate;
  const taskID = textValue(item.task_id);
  const meta: string[] = [];
  const status = textValue(item.status);
  if (status) meta.push(escapeHTML(overviewStatusLabel(status, options)));
  const ran = petDeskElapsedText(item.since, now, "petWorkbench.desk.ran", translate);
  if (ran) meta.push(escapeHTML(ran));
  const head = `<strong>${deskHeadlineHTML(item, translate)}</strong><small>${meta.join(" · ")}</small>`;
  const main = taskID
    ? `<button type="button" class="pet-desk-item-main" data-pet-overview-task="${escapeHTML(taskID)}">${head}</button>`
    : `<div class="pet-desk-item-main">${head}</div>`;
  const detail = textValue(item.detail);
  const hardware = deskStrings(item.hardware);
  const lines = [
    detail ? `<p class="pet-desk-detail">${deskText(detail)}</p>` : "",
    hardware.length ? `<p class="pet-desk-hardware">${escapeHTML(translate("petWorkbench.desk.hardware"))}${hardware.map(name => deskText(name)).join(", ")}</p>` : "",
  ].join("");
  return `<li class="pet-desk-item kind-running"><div class="pet-desk-item-body">${main}${lines}</div></li>`;
}

function renderDeskSection(id: string, className: string, title: string, rows: string[], total: number, empty: string, translate: Translate, extra = ""): string {
  const list = rows.length
    ? `<ol class="pet-desk-list">${rows.join("")}</ol>${deskTruncated(total, rows.length, translate)}`
    : `<div class="field-help pet-desk-empty">${escapeHTML(empty)}</div>`;
  return `<section class="pet-desk-section ${className}" id="${id}" aria-label="${escapeHTML(title)}">
    <header><h3>${escapeHTML(title)}</h3></header>
    ${list}
    ${extra}
  </section>`;
}

function renderDeskStatus(desk: PetDesk, options: PetOverviewOptions, totals: {needs: number; waiting: number; running: number}): string {
  const translate = options.translate;
  const inspection = options.inspection;
  const health = textValue(inspection?.health);
  const healthLabel = health === "ok"
    ? translate("petWorkbench.inspectionHealthy")
    : health === "degraded"
      ? translate("petWorkbench.inspectionDegraded")
      : translate("petWorkbench.inspectionUnknown");
  const healthClass = health === "ok" ? "ok" : health === "degraded" ? "degraded" : "unknown";
  // Version and uptime are rarely needed on the desk; they move to the hover
  // hint of the status row.
  const hint: string[] = [];
  const version = textValue(inspection?.version);
  const webVersion = textValue(inspection?.web_version);
  if (version) hint.push(`${translate("petWorkbench.inspectionVersion")}: ${version}`);
  if (webVersion) hint.push(`${translate("petWorkbench.inspectionWebVersion")}: ${webVersion}`);
  const uptime = countValue(inspection?.uptime_minutes);
  if (uptime !== null) hint.push(`${translate("petWorkbench.inspectionUptime")}: ${uptimeText(uptime, translate)}`);
  const counts = [
    translate("petWorkbench.desk.statusNeeds", {count: String(totals.needs)}),
    translate("petWorkbench.desk.statusWaiting", {count: String(totals.waiting)}),
    translate("petWorkbench.desk.statusRunning", {count: String(totals.running)}),
  ];
  const degraded = (options.channels || []).filter(item => item.degraded).length;
  const channelAlert = degraded > 0
    ? `<button type="button" class="pet-desk-channel-alert" data-pet-overview-jump="channels">${escapeHTML(translate("petWorkbench.desk.statusChannels", {count: String(degraded)}))}</button>`
    : "";
  const checkedAt = textValue(inspection?.checked_at);
  const checked = checkedAt
    ? `<small class="pet-desk-checked">${escapeHTML(translate("petWorkbench.desk.checkedAt", {time: options.formatTime(checkedAt)}))}</small>`
    : "";
  return `<section class="pet-desk-status"${hint.length ? ` title="${escapeHTML(hint.join(" · "))}"` : ""}>
    <strong>${escapeHTML(translate("petWorkbench.overviewStatusTitle"))}</strong>
    <span class="pet-inspection-health ${healthClass}">${escapeHTML(healthLabel)}</span>
    <span class="pet-desk-status-counts">${counts.map(text => `<span>${escapeHTML(text)}</span>`).join("")}</span>
    ${channelAlert}
    ${checked}
  </section>`;
}

function renderDeskPetSide(desk: PetDesk, translate: Translate): string {
  const corrections = deskList<PetDeskCorrection>(desk.corrections).filter(item => textValue(item.owner_note));
  const loops = deskStrings(desk.open_loops);
  const agenda = deskStrings(desk.agenda);
  const loopsTotal = deskTotal(desk.open_loops_total, loops.length);
  const agendaTotal = deskTotal(desk.agenda_total, agenda.length);
  const title = translate("petWorkbench.desk.petSide");
  const correctionTitle = translate("petWorkbench.desk.corrections", {count: String(corrections.length)});
  const correctionBody = corrections.length
    ? `<ul class="pet-desk-ledger">${corrections.map(item => `<li><strong>${deskTitleHTML(item, translate)}</strong><span class="pet-desk-quote">${deskText(textValue(item.owner_note))}</span></li>`).join("")}</ul>`
    : `<div class="field-help">${escapeHTML(translate("petWorkbench.desk.emptyCorrections"))}</div>`;
  const ledger = (key: string, items: string[], total: number): string => `<div class="pet-desk-ledger-group">
      <h4>${escapeHTML(translate(key, {count: String(total)}))}</h4>
      ${items.length ? `<ul class="pet-desk-ledger">${items.map(item => deskText(item, "li")).join("")}</ul>${deskTruncated(total, items.length, translate)}` : ""}
    </div>`;
  const header = `<header><h3>${escapeHTML(title)}</h3><button type="button" data-pet-overview-jump="records">${escapeHTML(translate("petWorkbench.desk.toRecords"))}</button></header>`;
  if (!corrections.length && loopsTotal === 0 && agendaTotal === 0) {
    return `<section class="pet-desk-section pet-desk-pet" id="pet-desk-pet" aria-label="${escapeHTML(title)}">
    ${header}
    <div class="field-help pet-desk-empty">${escapeHTML(translate("petWorkbench.desk.emptyPetSide"))}</div>
  </section>`;
  }
  return `<section class="pet-desk-section pet-desk-pet" id="pet-desk-pet" aria-label="${escapeHTML(title)}">
    ${header}
    <div class="pet-desk-ledger-group"><h4>${escapeHTML(correctionTitle)}</h4>${correctionBody}</div>
    ${ledger("petWorkbench.desk.openLoops", loops, loopsTotal)}
    ${ledger("petWorkbench.desk.agenda", agenda, agendaTotal)}
  </section>`;
}

// renderDeskLastReply is one line under the status row: what the pet said
// in its latest finished round. No reply, no line.
function renderDeskLastReply(reply: PetDeskLastReply | undefined, options: PetOverviewOptions, now: number): string {
  if (!reply || typeof reply !== "object" || Array.isArray(reply)) return "";
  const text = textValue(reply.text);
  if (!text && !reply.noop) return "";
  const translate = options.translate;
  const label = translate(reply.from_wake ? "petWorkbench.desk.lastWake" : "petWorkbench.desk.lastReply");
  const finished = textValue(reply.finished_at);
  // A zero or unparsable time shows no time rather than year 1.
  const time = finished && petDeskElapsedMinutes(finished, now) !== null
    ? `<small>${escapeHTML(options.formatTime(finished))}</small>`
    : "";
  const body = reply.noop
    ? `<span class="pet-desk-reply-noop">${escapeHTML(translate("petWorkbench.desk.lastNoop"))}</span>`
    : deskText(text, "span", "pet-desk-reply-text");
  return `<section class="pet-desk-reply" aria-label="${escapeHTML(label)}"><strong>${escapeHTML(label)}</strong>${body}${time}</section>`;
}

/* Last 7 days ------------------------------------------------------------- */

// The block starts as a placeholder inside the desk; the page fills it after
// the insights request settles, so a slow or failed count never holds up the
// desk itself.
export function renderPetInsightsPlaceholder(translate: Translate): string {
  const title = translate("petWorkbench.insights.title");
  return `<section class="pet-desk-section pet-desk-insights" id="pet-desk-insights" aria-label="${escapeHTML(title)}" data-pet-insights>
    <header><h3>${escapeHTML(title)}</h3></header>
    <div class="field-help pet-desk-empty">${escapeHTML(translate("common.loading"))}</div>
  </section>`;
}

// petInsightsDate renders a window bound as the browser's local date
// (YYYY-MM-DD). The server aligns the window to its local midnight; for an
// Owner in the same time zone the two agree. Unparsable input is "".
export function petInsightsDate(value: unknown): string {
  if (typeof value !== "string" || !value.trim()) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime()) || date.getTime() <= 0) return "";
  const pad = (n: number): string => String(n).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`;
}

function insightCount(value: unknown): number {
  return countValue(value) ?? 0;
}

export interface PetInsightsRenderOptions {
  translate: Translate;
  formatTime: FormatTime;
}

// renderPetInsightsBody fills the "last 7 days" block. `insights` null means
// the read failed, timed out or the server has no such endpoint.
export function renderPetInsightsBody(insights: PetInsights | null | undefined, options: PetInsightsRenderOptions): string {
  const translate = options.translate;
  const title = translate("petWorkbench.insights.title");
  const header = `<header><h3>${escapeHTML(title)}</h3></header>`;
  if (!insights || typeof insights !== "object" || Array.isArray(insights)) {
    return `${header}<div class="field-help pet-desk-empty" role="status">${escapeHTML(translate("petWorkbench.insights.unavailable"))}</div>`;
  }
  const approvals = insights.approvals && typeof insights.approvals === "object" ? insights.approvals : ({} as PetInsights["approvals"]);
  const wakes = insights.wakes && typeof insights.wakes === "object" ? insights.wakes : ({} as PetInsights["wakes"]);
  const from = textValue(insights.from);
  const to = textValue(insights.to);
  // The window is whole local days ending today, so only the first day is
  // worth printing; the end is always "today".
  const fromDate = petInsightsDate(from);
  const range = fromDate && to
    ? `<small class="pet-desk-insights-range">${escapeHTML(translate("petWorkbench.insights.range", {from: fromDate}))}</small>`
    : "";
  const outcomes = [
    translate("petWorkbench.insights.approved", {count: insightCount(approvals.approved)}),
    translate("petWorkbench.insights.rejected", {count: insightCount(approvals.rejected)}),
    translate("petWorkbench.insights.cancelled", {count: insightCount(approvals.cancelled)}),
    translate("petWorkbench.insights.pending", {count: insightCount(approvals.pending)}),
  ].join(" · ");
  const tokensKey = textValue(wakes.token_scope) === "window" ? "petWorkbench.insights.tokensWindow" : "petWorkbench.insights.tokensCumulative";
  const tokens = typeof wakes.tokens === "number" && Number.isFinite(wakes.tokens) && wakes.tokens >= 0
    ? translate(tokensKey, {tokens: formatTokens(Math.trunc(wakes.tokens))})
    : "";
  const rows: Array<[string, string]> = [
    [translate("petWorkbench.insights.actions", {count: insightCount(insights.actions)}), ""],
    [translate("petWorkbench.insights.asked", {count: insightCount(approvals.total)}), outcomes],
    [translate("petWorkbench.insights.ideas", {count: insightCount(approvals.ideas)}), ""],
    [translate("petWorkbench.insights.corrections", {count: insightCount(insights.corrections)}), ""],
    [translate("petWorkbench.insights.wakes", {count: insightCount(wakes.turns), noop: insightCount(wakes.noop)}), tokens],
  ];
  return `${header}${range}<ul class="pet-desk-insights-list">${rows.map(([line, detail]) => `<li><strong>${escapeHTML(line)}</strong>${detail ? `<small>${escapeHTML(detail)}</small>` : ""}</li>`).join("")}</ul>`;
}

function formatTokens(value: number): string {
  return new Intl.NumberFormat(typeof document !== "undefined" ? document.documentElement.lang || undefined : undefined).format(value);
}

// renderPetDesk keeps the server's order in every list: the server already
// sorts needs_owner longest-wait first and later newest first, and it chose
// which rows survive truncation by that order. Counts come from the
// untruncated *_total fields.
function renderPetDesk(desk: PetDesk, options: PetOverviewOptions): string {
  const translate = options.translate;
  const now = typeof options.now === "number" && Number.isFinite(options.now) ? options.now : Date.now();
  const needs = deskList<PetDeskNeed>(desk.needs_owner);
  const later = deskList<PetDeskNeed>(desk.later);
  const waiting = deskList<PetDeskWait>(desk.waiting_others);
  const running = deskList<PetDeskRun>(desk.running);
  const totals = {
    needs: deskTotal(desk.needs_owner_total, needs.length),
    later: deskTotal(desk.later_total, later.length),
    waiting: deskTotal(desk.waiting_others_total, waiting.length),
    running: deskTotal(desk.running_total, running.length),
  };
  const laterTitle = translate("petWorkbench.desk.laterTitle", {count: String(totals.later)});
  const laterBody = later.length
    ? `<ol class="pet-desk-list">${later.map(item => renderDeskNeed(item, null, options, now)).join("")}</ol>${deskTruncated(totals.later, later.length, translate)}`
    : `<div class="field-help pet-desk-empty">${escapeHTML(translate("petWorkbench.desk.emptyLater"))}</div>`;
  const laterDetails = `<details class="pet-desk-later" id="pet-desk-later"><summary>${escapeHTML(laterTitle)}</summary>${laterBody}</details>`;
  const needsSection = renderDeskSection(
    "pet-desk-needs", "pet-desk-needs",
    translate("petWorkbench.desk.needsTitle", {count: String(totals.needs)}),
    needs.map((item, index) => renderDeskNeed(item, index + 1, options, now)),
    totals.needs, translate("petWorkbench.desk.emptyNeeds"), translate, laterDetails,
  );
  const waitingSection = renderDeskSection(
    "pet-desk-waiting", "pet-desk-waiting",
    translate("petWorkbench.desk.waitingTitle", {count: String(totals.waiting)}),
    waiting.map(item => renderDeskWait(item, options, now)),
    totals.waiting, translate("petWorkbench.desk.emptyWaiting"), translate,
  );
  const runningSection = renderDeskSection(
    "pet-desk-running", "pet-desk-running",
    translate("petWorkbench.desk.runningTitle", {count: String(totals.running)}),
    running.map(item => renderDeskRun(item, options, now)),
    totals.running, translate("petWorkbench.desk.emptyRunning"), translate,
  );
  const {lists} = renderOverviewWatchLists(options.watches || [], options);
  return `<div class="pet-overview pet-desk">
    ${renderDeskStatus(desk, options, totals)}
    ${renderDeskLastReply(desk.last_reply, options, now)}
    ${needsSection}
    <div class="pet-desk-pair">${waitingSection}${runningSection}</div>
    ${renderDeskPetSide(desk, translate)}
    ${renderPetInsightsPlaceholder(translate)}
    <div class="pet-desk-follows">${lists}</div>
  </div>`;
}

export interface PetRecordEntry {
  date: string;
  content: string;
}

export interface PetRecordsOptions {
  translate: Translate;
  emptyCore: string;
  emptyWorklog: string;
  /** Desktop starts open, phones start collapsed; the caller keeps the choice. */
  tocOpen?: boolean;
}

// Worklog entries are dated filenames, so newest-first is a plain string sort.
// Copy first: the tab keeps the server response for pagination counts.
export function sortPetWorklog<T extends {date?: string}>(entries: T[]): T[] {
  return [...entries].sort((left, right) => String(right.date || "").localeCompare(String(left.date || "")));
}

// A ledger entry gets a slot next to its name for the status counts, which
// arrive with the records insights read.
function renderCoreEntry(entry: {name: string; content: string}, index: number, translate: Translate, ledger = false): string {
  const counts = ledger ? `<span class="pet-record-ledger-slot" data-pet-ledger-counts="${escapeHTML(entry.name)}"></span>` : "";
  return `<details class="pet-doc-entry" id="pet-record-core-${index}"><summary><span>${escapeHTML(entry.name)}</span>${counts}<button type="button" class="pet-doc-edit" data-pet-doc-edit data-pet-doc-section="core" data-pet-doc-name="${escapeHTML(entry.name)}">${escapeHTML(translate("common.edit"))}</button></summary><pre>${escapeHTML(entry.content)}</pre></details>`;
}

function renderWorklogEntry(entry: PetRecordEntry, index: number, translate: Translate): string {
  return `<details class="pet-doc-entry" id="pet-record-worklog-${index}"><summary><span>${escapeHTML(entry.date)}</span><button type="button" class="pet-doc-edit" data-pet-doc-edit data-pet-doc-section="worklog" data-pet-doc-name="${escapeHTML(entry.date)}.md">${escapeHTML(translate("common.edit"))}</button></summary><pre>${escapeHTML(entry.content)}</pre></details>`;
}

// The records page groups the pet's documents by what they are to the Owner.
// Group membership is by file name; a core file that is not listed lands in
// the first group so nothing the pet writes disappears from the page.
const PET_RECORD_JUDGMENT = ["identity.md", "principles.md", "changes.md"];
const PET_RECORD_PROMISES = ["loops.md", "agenda.md"];

function petRecordGroup(name: string): "judgment" | "promises" {
  return PET_RECORD_PROMISES.includes(name) ? "promises" : "judgment";
}

function orderPetCore(entries: Array<{name: string; content: string; index: number}>, order: string[]) {
  const rank = (name: string) => {
    const position = order.indexOf(name);
    return position < 0 ? order.length : position;
  };
  return [...entries].sort((left, right) => rank(left.name) - rank(right.name) || left.index - right.index);
}

// renderPetRecords shows four groups: who the pet is and how it judges, the
// Owner's corrections, what it promised, and what it did. The corrections
// and the ledger status counts come from the records insights read and are
// filled in after this renders. Edit buttons, the table of contents (open on
// wide screens, folded on phones) and worklog paging are unchanged.
export function renderPetRecords(
  core: Array<{name: string; content: string}>,
  worklog: PetRecordEntry[],
  options: PetRecordsOptions,
): string {
  const translate = petRecordsText(options.translate);
  const sorted = sortPetWorklog(worklog);
  const tocOpen = options.tocOpen !== false;
  // Element ids keep the response index so an entry keeps its id whichever
  // group it is in.
  const indexed = core.map((entry, index) => ({...entry, index}));
  const judgment = orderPetCore(indexed.filter(entry => petRecordGroup(entry.name) === "judgment"), PET_RECORD_JUDGMENT);
  const promises = orderPetCore(indexed.filter(entry => petRecordGroup(entry.name) === "promises"), PET_RECORD_PROMISES);
  const tocGroup = (id: string, titleKey: string, count: number | null, items: string) =>
    `<button type="button" data-pet-record-goto="${id}">${escapeHTML(translate(titleKey))}${count === null ? "" : ` (${count})`}</button>${items}`;
  const tocCore = (entries: typeof indexed) => entries.map(entry => `<button type="button" class="pet-record-toc-sub" data-pet-record-goto="pet-record-core-${entry.index}">${escapeHTML(entry.name)}</button>`).join("");
  const toc = [
    tocGroup("pet-record-judgment", "petWorkbench.records.judgment", judgment.length, tocCore(judgment)),
    tocGroup("pet-record-corrections", "petWorkbench.records.corrections", null, ""),
    tocGroup("pet-record-promises", "petWorkbench.records.promises", promises.length, tocCore(promises)),
    tocGroup("pet-record-worklog", "petWorkbench.records.worklog", sorted.length,
      sorted.map((entry, index) => `<button type="button" class="pet-record-toc-sub" data-pet-record-goto="pet-record-worklog-${index}">${escapeHTML(entry.date)}</button>`).join("")),
  ].join("");
  const toggleLabel = tocOpen
    ? translate("petWorkbench.recordsTocCollapse")
    : translate("petWorkbench.recordsTocExpand");
  const coreBody = (entries: typeof indexed, ledger: boolean) => entries.length
    ? entries.map(entry => renderCoreEntry(entry, entry.index, translate, ledger)).join("")
    : `<div class="field-help">${escapeHTML(options.emptyCore)}</div>`;
  const worklogBody = sorted.length
    ? sorted.map((entry, index) => renderWorklogEntry(entry, index, translate)).join("")
    : `<div class="field-help">${escapeHTML(options.emptyWorklog)}</div>`;
  const section = (id: string, key: string, body: string) => `<section class="pet-record-section" id="${id}">
        <h3>${escapeHTML(translate(`petWorkbench.records.${key}`))}</h3>
        <p class="pet-record-intro">${escapeHTML(translate(`petWorkbench.records.${key}Intro`))}</p>
        ${body}
      </section>`;
  return `<div class="pet-records${tocOpen ? "" : " toc-collapsed"}">
    <aside class="pet-record-toc" id="pet-record-toc">
      <strong>${escapeHTML(translate("petWorkbench.recordsToc"))}</strong>
      <nav id="pet-record-toc-nav">${toc}</nav>
    </aside>
    <div class="pet-record-content">
      <div class="pet-record-toolbar"><button type="button" class="pet-record-toc-toggle" data-pet-record-toc-toggle aria-expanded="${tocOpen}" aria-controls="pet-record-toc-nav">${escapeHTML(toggleLabel)}</button></div>
      <div class="pet-record-knowledge" data-pet-record-knowledge role="status">${escapeHTML(translate("common.loading"))}</div>
      ${section("pet-record-judgment", "judgment", coreBody(judgment, false))}
      ${section("pet-record-corrections", "corrections", `<div class="pet-record-corrections" data-pet-record-corrections><div class="field-help">${escapeHTML(translate("common.loading"))}</div></div>`)}
      ${section("pet-record-promises", "promises", coreBody(promises, true))}
      ${section("pet-record-worklog", "worklog", worklogBody)}
    </div>
  </div>`;
}

/* Records page copy -------------------------------------------------------- */

// The records page's copy lives in this route chunk instead of the shared
// catalog, so it costs the first screen nothing (task-813 step three). Keys
// are looked up through recordsText, which falls back to the shared
// translate for anything else.
const PET_RECORDS_COPY: Record<"zh-CN" | "en-US", Record<string, string>> = {
  "zh-CN": {
    "judgment": "它是谁、怎么判断",
    "judgmentIntro": "宠物的身份和做事准则，以及 AHA 自动记下的每次修改。",
    "corrections": "你的纠正",
    "correctionsIntro": "你否决审批时写的原因。宠物把判断写进做事准则后，这些纠正就算消化了。",
    "correctionsEmpty": "你还没有纠正过宠物。",
    "correctionsUnavailable": "暂时读不到纠正记录。",
    "correctionState.open": "还没消化",
    "correctionState.absorbed": "已写进原则",
    "correctionState.expired": "已过期",
    "promises": "它答应的事",
    "promisesIntro": "宠物记下的未闭环事项和自己的议程，标题旁是各状态的条数。",
    "ledgerEmpty": "没有条目",
    "worklog": "它做过的事",
    "worklogIntro": "宠物按日期写的工作记录，新的在上。",
    "knowledgeTitle": "知识库：",
    "knowledgeEntries": "{entries} 条",
    "knowledgePending": "待修订 {pending} 条",
    "knowledgeHelped": "有帮助 {helped} 次",
    "knowledgeHelpedPartial": "有帮助 {helped} 次（新知识库不记这个数，未计入）",
    "knowledgeTruncated": "只读了前 {projects} 个项目",
    "knowledgeFailed": "{count} 个知识空间读取失败",
    "knowledgeUnavailable": "暂时读不到知识库的情况。",
    "knowledgeStatus.stale": "过时",
    "knowledgeStatus.wrong": "错误",
  },
  "en-US": {
    "judgment": "Who it is and how it judges",
    "judgmentIntro": "The pet's identity and principles, and every change AHA recorded.",
    "corrections": "Your corrections",
    "correctionsIntro": "The reasons you wrote when rejecting approvals. A correction is absorbed once the pet writes the judgment into its principles.",
    "correctionsEmpty": "You have not corrected the pet yet.",
    "correctionsUnavailable": "Corrections are not available right now.",
    "correctionState.open": "Not absorbed yet",
    "correctionState.absorbed": "In its principles",
    "correctionState.expired": "Expired",
    "promises": "What it promised",
    "promisesIntro": "The pet's open loops and its own agenda, with the count per status next to each title.",
    "ledgerEmpty": "no entries",
    "worklog": "What it did",
    "worklogIntro": "The pet's dated work log, newest first.",
    "knowledgeTitle": "Knowledge:",
    "knowledgeEntries": "{entries} entries",
    "knowledgePending": "{pending} to revise",
    "knowledgeHelped": "{helped} helped",
    "knowledgeHelpedPartial": "{helped} helped (the new knowledge base does not keep this count)",
    "knowledgeTruncated": "only the first {projects} projects read",
    "knowledgeFailed": "{count} knowledge spaces failed to read",
    "knowledgeUnavailable": "Knowledge health is not available right now.",
    "knowledgeStatus.stale": "stale",
    "knowledgeStatus.wrong": "wrong",
  },
};

function recordsLocale(): "zh-CN" | "en-US" {
  return currentLocale() === "en-US" ? "en-US" : "zh-CN";
}

// recordsText resolves "petWorkbench.records.*" keys from this chunk and
// everything else through the page's translate.
export function petRecordsText(translate: Translate): Translate {
  return (key, values = {}) => {
    if (!key.startsWith("petWorkbench.records.")) return translate(key, values);
    const name = key.slice("petWorkbench.records.".length);
    const template = PET_RECORDS_COPY[recordsLocale()][name] ?? PET_RECORDS_COPY["zh-CN"][name] ?? key;
    return template.replace(/\{(\w+)\}/g, (_, field: string) => String(values[field] ?? `{${field}}`));
  };
}

/* Records page data filled after render ----------------------------------- */

export interface PetRecordsDataOptions {
  translate: Translate;
  formatTime: FormatTime;
}

const PET_CORRECTION_STATES = ["open", "absorbed", "expired"];

// renderPetCorrectionHistory fills the corrections group. `records` null
// means the read failed or the server has no such data.
export function renderPetCorrectionHistory(records: PetCorrectionRecord[] | null | undefined, options: PetRecordsDataOptions): string {
  const translate = petRecordsText(options.translate);
  if (!Array.isArray(records)) {
    return `<div class="field-help" role="status">${escapeHTML(translate("petWorkbench.records.correctionsUnavailable"))}</div>`;
  }
  const rows = records.filter(item => item && typeof item === "object" && !Array.isArray(item));
  if (!rows.length) {
    return `<div class="field-help">${escapeHTML(translate("petWorkbench.records.correctionsEmpty"))}</div>`;
  }
  return `<ol class="pet-record-correction-list">${rows.map(item => {
    const state = PET_CORRECTION_STATES.includes(textValue(item.state)) ? textValue(item.state) : "open";
    const at = textValue(item.at);
    const time = at && petDeskElapsedMinutes(at, Date.now()) !== null ? `<small>${escapeHTML(options.formatTime(at))}</small>` : "";
    const title = textValue(item.title);
    return `<li class="pet-record-correction state-${state}">
      <div class="pet-record-correction-head"><strong>${title ? deskText(title) : escapeHTML(translate("petWorkbench.desk.untitled"))}</strong><span class="pet-record-correction-state">${escapeHTML(translate(`petWorkbench.records.correctionState.${state}`))}</span>${time}</div>
      <p>${deskText(textValue(item.owner_note))}</p>
    </li>`;
  }).join("")}</ol>`;
}

// renderPetLedgerCounts is the "open 2 · waiting 1" line next to a ledger's
// title. Statuses are the pet's own words, so they are shown as written.
export function renderPetLedgerCounts(counts: PetLedgerCounts | undefined, pageTranslate: Translate): string {
  const translate = petRecordsText(pageTranslate);
  if (!counts || typeof counts !== "object" || !counts.counts || typeof counts.counts !== "object") return "";
  const parts = Object.entries(counts.counts)
    .filter(([status, count]) => status && typeof count === "number" && count > 0)
    .sort((left, right) => right[1] - left[1] || left[0].localeCompare(right[0]))
    .map(([status, count]) => `${deskText(status)} ${count}`);
  if (!parts.length) return `<span class="pet-record-ledger-counts">${escapeHTML(translate("petWorkbench.records.ledgerEmpty"))}</span>`;
  return `<span class="pet-record-ledger-counts">${parts.join(" · ")}</span>`;
}

// petKnowledgeEntryHash is the knowledge page's deep link for one entry.
export function petKnowledgeEntryHash(item: {id?: string; scope?: string; project_id?: string}): string {
  const params = new URLSearchParams({doc: String(item.id || "")});
  if (item.scope === "global") {
    params.set("scope", "global");
  } else {
    params.set("scope", "project");
    if (item.project_id) params.set("project", item.project_id);
  }
  return `#knowledge?${params.toString()}`;
}

// renderPetKnowledgeHealth is the line at the top of the records page.
// `health` null means the read failed; the rest of the page is unaffected.
export function renderPetKnowledgeHealth(health: PetKnowledgeHealth | null | undefined, pageTranslate: Translate): string {
  const translate = petRecordsText(pageTranslate);
  if (!health || typeof health !== "object" || Array.isArray(health)) {
    return `<span class="pet-record-knowledge-summary">${escapeHTML(translate("petWorkbench.records.knowledgeUnavailable"))}</span>`;
  }
  const helped = health.helped_partial
    ? translate("petWorkbench.records.knowledgeHelpedPartial", {helped: countValue(health.helped) ?? 0})
    : translate("petWorkbench.records.knowledgeHelped", {helped: countValue(health.helped) ?? 0});
  const parts = [
    translate("petWorkbench.records.knowledgeEntries", {entries: countValue(health.entries) ?? 0}),
    translate("petWorkbench.records.knowledgePending", {pending: countValue(health.pending) ?? 0}),
    helped,
  ];
  if (health.truncated) parts.push(translate("petWorkbench.records.knowledgeTruncated", {projects: countValue(health.projects) ?? 0}));
  if ((countValue(health.failed) ?? 0) > 0) parts.push(translate("petWorkbench.records.knowledgeFailed", {count: countValue(health.failed) ?? 0}));
  const items = deskList<PetKnowledgePending>(health.pending_items).filter(item => textValue(item.id));
  const list = items.length
    ? `<ul class="pet-record-knowledge-pending">${items.map(item => {
      const status = textValue(item.status) === "wrong" ? "wrong" : "stale";
      return `<li><a href="${escapeHTML(petKnowledgeEntryHash(item))}" data-pet-knowledge-link>${deskText(textValue(item.title) || textValue(item.id))}</a><small>${deskText(textValue(item.space))} · ${escapeHTML(translate(`petWorkbench.records.knowledgeStatus.${status}`))}</small></li>`;
    }).join("")}</ul>`
    : "";
  return `<span class="pet-record-knowledge-summary"><strong>${escapeHTML(translate("petWorkbench.records.knowledgeTitle"))}</strong> ${escapeHTML(parts.join(" · "))}</span>${list}`;
}
