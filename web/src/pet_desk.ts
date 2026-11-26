// Pet workbench page renderers that only the workbench needs: the overview
// (the desk and the pre-desk fallback), the "last 7 days" block and the
// records tab. They load as a route chunk when the workbench opens, so the
// first screen of every other page does not carry them. The loader, page
// keeper, tab mapping and approvals-tab helpers stay in pet_workbench.ts,
// which the shell needs before this chunk arrives.
import type {PetApproval, PetChannelHealth, PetCorrectionRecord, PetDesk, PetDeskCorrection, PetDeskLastReply, PetDeskLine, PetDeskNeed, PetDeskRecent, PetDeskRun, PetDeskStalled, PetDeskWait, PetInsights, PetInspection, PetKnowledgeHealth, PetKnowledgePending, PetLedgerCounts, PetTaskWatch, TaskLifecycle} from "./types.js";
import {currentLocale} from "./i18n.js";
import {petApprovalDurationText, petApprovalRiskLabel, renderPetApprovalRow} from "./pet_approvals.js";
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
  /** True for an approval that needs a choice in the detail dialog first. */
  approvalNeedsChoice?: (approvalID: string) => boolean;
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

function watchActionButtons(item: Pick<PetTaskWatch, "task_id" | "watched" | "ignored">, translate: Translate, recent: boolean): string {
  const actions = petWatchActionKeys(item);
  return actions.filter(action => !recent || action === "follow" || action === "ignore").map(action => {
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
function renderWatchRow(item: Pick<PetTaskWatch, "task_id" | "code" | "title" | "status" | "updated_at" | "reason" | "watched" | "ignored">, options: PetOverviewOptions, recent = false): string {
  const title = `${item.code || item.task_id} · ${item.title || item.task_id}`;
  const tone = recent && ["completed", "cancelled"].includes(item.status) ? " terminal" : petWatchAnomaly(item) ? " anomaly" : "";
  const status = overviewStatusLabel(item.status, options);
  const time = petDeskElapsedMinutes(item.updated_at, Date.now()) === null ? "" : options.formatTime(item.updated_at);
  const meta = recent
    ? `<small class="pet-desk-recent-meta">${deskText(status)}${time ? `<time datetime="${escapeHTML(item.updated_at)}">${escapeHTML(time)}</time>` : ""}</small>`
    : `<small>${deskText(status)}${time ? ` · ${escapeHTML(time)}` : ""}${item.reason ? ` · ${deskText(watchReasonLabel(item.reason, options.translate))}` : ""}</small>`;
  return `<article class="pet-watch-row${tone}">
    <button type="button" class="pet-watch-row-main" data-pet-task-lifecycle="${escapeHTML(item.task_id)}">
      <strong data-i18n-ignore>${escapeHTML(title)}</strong>
      ${meta}
    </button>
    <div class="pet-watch-row-actions">
      <button type="button" data-pet-overview-task="${escapeHTML(item.task_id)}">${escapeHTML(options.translate("petWorkbench.openTask"))}</button>
      ${watchActionButtons(item, options.translate, recent)}
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

// Watch lists and recent activity share the same bounded, independently
// scrolling body. Leave headings outside it and omit an empty tab stop.
function renderOverviewWatchLists(watches: PetTaskWatch[], options: PetOverviewOptions, desk?: PetDesk): {watched: PetTaskWatch[]; lists: string} {
  const translate = options.translate;
  const watched = watches.filter(item => item.watched && !item.ignored);
  const ignored = watches.filter(item => item.ignored);
  const watchRows = (items: PetTaskWatch[]) => items.map(item => renderWatchRow(item, options));
  const list = (id: string, className: string, title: string, rows: string[], empty: string, more = "") => {
    const focus = rows.length ? ` tabindex="0" aria-labelledby="${id}-title"` : "";
    const content = rows.length ? rows.join("") : `<div class="field-help pet-follow-empty">${escapeHTML(empty)}</div>`;
    return `<section class="${className} pet-follow-list" id="${id}" aria-labelledby="${id}-title"><h3 class="pet-follow-title" id="${id}-title">${escapeHTML(title)}</h3><div class="${className}-body pet-follow-body"${focus}>${content}${more}</div></section>`;
  };
  const follows = list("pet-overview-follows", "pet-overview-follows", translate("petWorkbench.overviewFollows", {count: String(watched.length)}), watchRows(watched), translate("petWorkbench.emptyFollows"));
  const ignoredList = list("pet-overview-ignored", "pet-overview-other", translate("petWorkbench.otherFollows", {count: String(ignored.length)}), watchRows(ignored), translate("petWorkbench.emptyIgnored"));
  if (desk?.recent !== undefined) {
    const recent = deskList<PetDeskRecent>(desk.recent);
    const total = deskTotal(desk.recent_total, recent.length);
    const recentList = list("pet-overview-recent", "pet-overview-other", deskCopy("recent", {count: String(total)}),
      recent.map(item => renderWatchRow({...item, code: item.task_code, updated_at: item.last_active, ignored: false}, options, true)),
      deskCopy("emptyRecent"), deskTruncated(total, recent.length, translate));
    return {watched, lists: [follows, recentList, ignoredList].join("")};
  }
  const unfollowed = watches.filter(item => !item.watched && !item.ignored);
  return {watched, lists: [follows, ignoredList,
    list("pet-overview-unfollowed", "pet-overview-other", translate("petWorkbench.unfollowedTasks", {count: String(unfollowed.length)}), watchRows(unfollowed), deskCopy("emptyUnfollowed")),
  ].join("")};
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
      html: `<button type="button" class="pet-overview-activity" data-pet-task-lifecycle="${escapeHTML(item.task_id)}"><span class="pet-overview-activity-kind">${escapeHTML(translate("petWorkbench.overviewTaskKind"))}</span><strong data-i18n-ignore>${escapeHTML(item.title || item.task_id)}</strong><small>${escapeHTML(options.formatTime(item.updated_at || ""))}</small></button>`,
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
    <section class="pet-overview-recent" id="pet-overview-legacy-recent">
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
  const duration = petApprovalDurationText(minutes, translate);
  return key ? translate(key, {duration}) : duration;
}

// deskElapsed is an elapsed-time label that can be recomputed in place: the
// span keeps its start time and catalog key, so tickPetDeskElapsed rewrites
// the text every half minute without a new read. "" when the time is unknown.
function deskElapsed(since: unknown, now: number, key: string, translate: Translate): string {
  const text = petDeskElapsedText(since, now, key, translate);
  if (!text) return "";
  return `<span class="pet-desk-elapsed" data-pet-desk-since="${escapeHTML(String(since))}" data-pet-desk-elapsed-key="${escapeHTML(key)}">${escapeHTML(text)}</span>`;
}

// tickPetDeskElapsed recomputes every elapsed-time label under root and
// returns how many it updated.
export function tickPetDeskElapsed(root: ParentNode, now: number, translate: Translate): number {
  let updated = 0;
  root.querySelectorAll<HTMLElement>("[data-pet-desk-since]").forEach(node => {
    const text = petDeskElapsedText(node.dataset.petDeskSince, now, node.dataset.petDeskElapsedKey || "", translate);
    if (text && node.textContent !== text) {
      node.textContent = text;
      updated++;
    }
  });
  return updated;
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

// deskScroll wraps a block's rows. On a wide screen the desk fits the
// viewport and each block scrolls inside this wrapper under its fixed header;
// tabindex lets the keyboard reach a wrapper whose rows have no buttons.
function deskScroll(inner: string): string {
  return `<div class="pet-desk-scroll" tabindex="0">${inner}</div>`;
}

// deskDetail is a row's explanatory line. CSS clamps it to two lines; the
// full text stays in the hover hint, and focusing the line (click or Tab)
// lifts the clamp so keyboard and touch users can read all of it.
function deskDetail(text: string, label = ""): string {
  // The hint sits on the ignored span: it is server text, not UI copy.
  return `<p class="pet-desk-detail" tabindex="0">${label}<span data-i18n-ignore title="${escapeHTML(text)}">${escapeHTML(text)}</span></p>`;
}

// The kinds below are the desk API contract (internal/app/pet_desk.go). A kind
// this page does not know yet gets the generic label of its list instead of an
// empty cell or a raw catalog key.
const PET_DESK_KINDS: Record<string, readonly string[]> = {
  need: ["approval", "question", "blocked", "failed", "stopped"],
  later: ["idea", "next_step"],
  wait: ["review", "executing", "channel", "loop", "line"],
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

function deskOpenTaskButton(taskID: string, translate: Translate, compact = false): string {
  const label = escapeHTML(translate("petWorkbench.desk.openTask"));
  const target = `data-pet-overview-task="${escapeHTML(taskID)}"`;
  if (!compact) return `<button type="button" ${target}>${label}</button>`;
  return `<button type="button" class="pet-desk-open-task" ${target} title="${label}" aria-label="${label}"><span class="pet-desk-open-label">${label}</span><span class="pet-desk-open-mark" aria-hidden="true">↗</span></button>`;
}

function deskLifecycleTarget(taskID: string): string {
  return taskID ? `data-pet-task-lifecycle="${escapeHTML(taskID)}"` : "";
}

function deskWorkflowLines(lines: PetDeskLine[]): string {
  return `<ul class="pet-desk-questions">${lines.map(line => `<li>
    ${deskText(line.title, "strong")}
    ${line.step ? deskText(line.step, "div") : ""}
    ${line.blocker ? `<div>${escapeHTML(deskCopy("blocker"))} ${deskText(line.blocker)}</div>` : ""}
  </li>`).join("")}</ul>`;
}

function renderDeskStalled(item: PetDeskStalled, options: PetOverviewOptions, now: number): string {
  const id = escapeHTML(item.task_id);
  const elapsed = deskElapsed(item.since, now, "petWorkbench.desk.waited", options.translate);
  const meta = [item.line_title ? deskText(item.line_title) : "", elapsed].filter(Boolean).join(" · ");
  return `<li class="pet-desk-item pet-desk-stalled-item" data-pet-desk-stalled="${id}">
    <div class="pet-desk-item-body">
      <button type="button" class="pet-desk-item-main" ${deskLifecycleTarget(item.task_id)}>
        <strong>${deskHeadlineHTML(item, options.translate)}</strong>${meta ? `<small>${meta}</small>` : ""}
      </button>
      ${item.detail ? deskDetail(item.detail) : ""}
      <div class="pet-desk-continue-form" data-pet-desk-continue-form hidden>
        <p data-pet-desk-continue-message></p>
        <div class="pet-desk-item-actions">
          <button type="button" class="primary" data-pet-desk-send="${id}">${escapeHTML(deskCopy("send"))}</button>
          <button type="button" data-pet-desk-cancel-continue="${id}">${escapeHTML(deskCopy("cancel"))}</button>
        </div>
      </div>
      <p class="pet-desk-decision-status" data-pet-desk-continue-status role="status" data-i18n-ignore></p>
    </div>
    <div class="pet-desk-item-actions">
      <button type="button" data-pet-desk-continue="${id}" aria-expanded="false">${escapeHTML(deskCopy("continueTask"))}</button>
      ${deskOpenTaskButton(item.task_id, options.translate)}
    </div>
  </li>`;
}

/* Inline approve / reject (task-813 round 4) ------------------------------ */

// Approval kinds the server runs as a background delete
// (petApprovalRunsInBackground in internal/app/pet_delegate.go).
const PET_DESK_HEAVY_DELETES = ["delete_task", "delete_project", "delete_workspace"];

// petDeskApprovalNeedsConfirm decides which approvals need a second click on
// the desk: a high-risk approval, or one that deletes something directly
// (the server's background deletes and any other delete_* kind). Everything
// else is approved with one click.
export function petDeskApprovalNeedsConfirm(item: {approval_kind?: unknown; risk?: unknown}): boolean {
  const kind = textValue(item.approval_kind);
  const risk = textValue(item.risk).toLowerCase();
  return risk === "high" || PET_DESK_HEAVY_DELETES.includes(kind) || kind.startsWith("delete_");
}

// How long the "Approve?" confirmation waits before it folds back.
export const PET_DESK_CONFIRM_MS = 5000;

export interface PetDeskApprovalChoice {
  /** The approval needs a choice in the detail dialog before it can be approved. */
  needsChoice: boolean;
}

// The inline controls for one approval or idea row. An approval that asks for
// a choice gets only "Review" (the choice is made in the detail dialog). The
// buttons' names include the approval title (server text), so they carry
// data-i18n-ignore like other desk text; their own words are already in the
// page's language.
function deskDecisionControls(approvalID: string, title: string, needsChoice: boolean, confirm: boolean, translate: Translate): string {
  const id = escapeHTML(approvalID);
  const name = escapeHTML(title);
  if (needsChoice) {
    return `<button type="button" data-pet-approval-open="${id}" data-i18n-ignore aria-label="${escapeHTML(deskCopy("reviewNamed", {title}))}">${escapeHTML(translate("petWorkbench.desk.review"))}</button>`;
  }
  return `<button type="button" class="pet-desk-approve" data-pet-desk-approve="${id}"${confirm ? " data-pet-desk-confirm" : ""} data-i18n-ignore aria-label="${escapeHTML(deskCopy("approveNamed", {title}))}" data-pet-desk-title="${name}">${escapeHTML(deskCopy("approve"))}</button><button type="button" class="pet-desk-reject" data-pet-desk-reject="${id}" data-i18n-ignore aria-label="${escapeHTML(deskCopy("rejectNamed", {title}))}" data-pet-desk-title="${name}">${escapeHTML(deskCopy("reject"))}</button>`;
}

// renderPetDeskRejectForm is the one-line form under a row: a reason (the
// pet's correction record) and confirm / never mind. The confirm button stays
// disabled until there is a reason, because the server requires one.
export function renderPetDeskRejectForm(approvalID: string, title: string, reason = ""): string {
  const id = escapeHTML(approvalID);
  const label = deskCopy("rejectReasonNamed", {title});
  return `<form class="pet-desk-reject-form" data-pet-desk-reject-form="${id}">
    <input type="text" data-pet-desk-reject-reason="${id}" data-i18n-ignore aria-label="${escapeHTML(label)}" placeholder="${escapeHTML(deskCopy("rejectReason"))}" maxlength="500" value="${escapeHTML(reason)}" autocomplete="off">
    <button type="submit" class="danger" data-pet-desk-reject-confirm="${id}"${reason.trim() ? "" : " disabled"}>${escapeHTML(deskCopy("rejectConfirm"))}</button>
    <button type="button" data-pet-desk-reject-cancel="${id}">${escapeHTML(deskCopy("cancel"))}</button>
  </form>`;
}

// A needs/later row: the main button opens the approval detail for approvals
// and ideas, otherwise the task lifecycle. Approvals tied to a Task also get an explicit
// open-task button. Approvals and ideas carry inline approve / reject
// (task-813 round 4); high-risk and delete approvals ask once more.
function renderDeskNeed(item: PetDeskNeed, position: number | null, options: PetOverviewOptions, now: number): string {
  const translate = options.translate;
  const kind = textValue(item.kind);
  const approvalID = textValue(item.approval_id);
  const taskID = textValue(item.task_id);
  const isApproval = (kind === "approval" || kind === "idea") && approvalID !== "";
  const questions = deskStrings(item.questions);
  const lines = deskList<PetDeskLine>(item.lines);
  const showLines = kind === "question" && Array.isArray(item.lines);
  const list = position === null ? "later" : "need";
  const kindLabel = showLines
    ? deskCopy("linesForYou", {count: String(lines.length)})
    : deskKindLabel(list, kind, translate, {count: String(questions.length)});
  const meta: string[] = [escapeHTML(kindLabel)];
  const taskLabel = deskTaskLabel(item);
  if (taskLabel && textValue(item.title) !== taskLabel) meta.push(deskText(taskLabel));
  if (kind === "approval" && textValue(item.risk)) meta.push(escapeHTML(petApprovalRiskLabel(String(item.risk), translate)));
  const waited = deskElapsed(item.since, now, "petWorkbench.desk.waited", translate);
  if (waited) meta.push(waited);
  if (taskID && item.stuck_on === "unknown") meta.push(escapeHTML(deskCopy("noLines")));
  const target = isApproval
    ? `data-pet-approval-open="${escapeHTML(approvalID)}"`
    : deskLifecycleTarget(taskID);
  const head = `<strong>${deskTitleHTML(item, translate)}</strong><small>${meta.join(" · ")}</small>`;
  const main = target
    ? `<button type="button" class="pet-desk-item-main" ${target}>${head}</button>`
    : `<div class="pet-desk-item-main">${head}</div>`;
  const detail = textValue(item.detail);
  let body = "";
  if (showLines) {
    if (lines.length) body = deskWorkflowLines(lines);
  } else if (kind === "question" && questions.length) {
    body = `<ul class="pet-desk-questions">${questions.map(question => deskText(question, "li")).join("")}</ul>`;
  } else if (detail) {
    body = deskDetail(detail, kind === "next_step" ? escapeHTML(translate("petWorkbench.desk.nextStep")) : "");
  }
  const actions: string[] = [];
  const needsChoice = isApproval && Boolean(options.approvalNeedsChoice?.(approvalID));
  if (isApproval) actions.push(deskDecisionControls(approvalID, textValue(item.title) || approvalID, needsChoice, petDeskApprovalNeedsConfirm(item), translate));
  if (taskID) actions.push(deskOpenTaskButton(taskID, translate));
  const index = position === null ? "" : `<span class="pet-desk-index" aria-hidden="true">${petDeskIndexLabel(position)}</span>`;
  // The status line (busy, error, confirmation) is announced to screen readers.
  const status = isApproval ? `<p class="pet-desk-decision-status" data-pet-desk-status="${escapeHTML(approvalID)}" role="status" aria-live="polite"></p>` : "";
  return `<li class="pet-desk-item kind-${deskKindClass(kind)}"${isApproval ? ` data-pet-desk-approval="${escapeHTML(approvalID)}"` : ""}>
    ${index}
    <div class="pet-desk-item-body">${main}${body}${status}</div>
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
  let target = deskLifecycleTarget(taskID);
  if (kind === "review") {
    headline = deskHeadlineHTML(item, translate);
    meta.push(escapeHTML(translate("petWorkbench.desk.wait.review")) + (on ? ` → ${deskText(on)}` : ""));
  } else if (kind === "line") {
    headline = deskHeadlineHTML(item, translate);
    meta.push(escapeHTML(deskCopy("waitingOn")) + (on ? ` ${deskText(on)}` : ""));
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
  const waited = deskElapsed(item.since, now, kind === "channel" ? "petWorkbench.desk.oldestWaited" : "petWorkbench.desk.waited", translate);
  if (waited) meta.push(waited);
  const head = `<strong>${headline}</strong><small>${meta.join(" · ")}</small>`;
  const main = target
    ? `<button type="button" class="pet-desk-item-main" ${target}>${head}</button>`
    : `<div class="pet-desk-item-main">${head}</div>`;
  const detail = textValue(item.detail);
  const lines = kind === "line" ? deskList<PetDeskLine>(item.lines) : [];
  const next = kind === "loop" && detail
    ? deskDetail(detail, escapeHTML(translate("petWorkbench.desk.nextStep")))
    : lines.length ? deskWorkflowLines(lines) : "";
  const actions = taskID ? `<div class="pet-desk-item-actions">${deskOpenTaskButton(taskID, translate, true)}</div>` : "";
  return `<li class="pet-desk-item kind-${deskKindClass(kind)}"><div class="pet-desk-item-body">${main}${next}</div>${actions}</li>`;
}

function renderDeskRun(item: PetDeskRun, options: PetOverviewOptions, now: number): string {
  const translate = options.translate;
  const taskID = textValue(item.task_id);
  const meta: string[] = [];
  const status = textValue(item.status);
  if (status) meta.push(deskText(overviewStatusLabel(status, options)));
  const ran = deskElapsed(item.since, now, "petWorkbench.desk.ran", translate);
  if (ran) meta.push(ran);
  const head = `<strong>${deskHeadlineHTML(item, translate)}</strong><small>${meta.join(" · ")}</small>`;
  const main = taskID
    ? `<button type="button" class="pet-desk-item-main" ${deskLifecycleTarget(taskID)}>${head}</button>`
    : `<div class="pet-desk-item-main">${head}</div>`;
  const detail = textValue(item.detail);
  const hardware = deskStrings(item.hardware);
  const lines = [
    detail ? deskDetail(detail) : "",
    hardware.length ? `<p class="pet-desk-hardware">${escapeHTML(translate("petWorkbench.desk.hardware"))}${hardware.map(name => deskText(name)).join(", ")}</p>` : "",
  ].join("");
  const actions = taskID ? `<div class="pet-desk-item-actions">${deskOpenTaskButton(taskID, translate, true)}</div>` : "";
  return `<li class="pet-desk-item kind-running"><div class="pet-desk-item-body">${main}${lines}</div>${actions}</li>`;
}

function renderDeskSection(id: string, className: string, title: string, rows: string[], total: number, empty: string, translate: Translate, extra = "", leading = ""): string {
  const content = rows.length
    ? `<ol class="pet-desk-list">${rows.join("")}</ol>${deskTruncated(total, rows.length, translate)}`
    : `<div class="field-help pet-desk-empty">${escapeHTML(empty)}</div>`;
  const list = rows.length || leading ? deskScroll(leading + content) : content;
  return `<section class="pet-desk-section ${className}" id="${id}" aria-label="${escapeHTML(title)}">
    <header><h3 tabindex="-1">${escapeHTML(title)}</h3></header>
    ${list}
    ${extra}
  </section>`;
}

function renderDeskStatus(desk: PetDesk, options: PetOverviewOptions, totals: {needs: number; stalled: number; waiting: number; running: number}): string {
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
    ...(totals.stalled ? [deskCopy("stalledCount", {count: String(totals.stalled)})] : []),
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
    <small class="pet-desk-updated" data-pet-desk-updated role="status" aria-live="polite"></small>
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
    ${deskScroll(`<div class="pet-desk-ledger-group"><h4>${escapeHTML(correctionTitle)}</h4>${correctionBody}</div>
    ${ledger("petWorkbench.desk.openLoops", loops, loopsTotal)}
    ${ledger("petWorkbench.desk.agenda", agenda, agendaTotal)}`)}
  </section>`;
}

// petDeskReplyPreview is the one-line preview of the last reply: the reply
// is Markdown-ish chat text, so bold, underline and code markers are dropped
// and line breaks fold into spaces. The expanded text keeps the original.
export function petDeskReplyPreview(text: string): string {
  return text.replace(/\*\*|__|`/g, "").replace(/\s+/g, " ").trim();
}

// The expand control's copy lives in this chunk, like the records copy, so
// the shared first-screen catalog does not grow for a desk-only button.
export const PET_DESK_COPY = {
  "zh-CN": {expand: "展开", collapse: "收起", emptyUnfollowed: "没有未关注的任务。", updatedAt: "更新于 {time}", updateFailed: "刷新失败，稍后会再试；显示的是 {time} 的数据",
    stalled: "停着没人推", stalledCount: "{count} 个停着", continueTask: "让它继续", send: "发送", sending: "发送中…", sent: "已发送继续消息",
    continueMessage: "继续：按状态卡上的下一步往下做；做不了就说明卡在哪。", sendFailed: "发送失败：{message}", sendFailedUnknown: "没有收到失败原因",
    noLines: "没有记录分线", linesForYou: "有 {count} 条线等你", blocker: "卡点：", waitingOn: "等", recent: "最近活动（{count}）", emptyRecent: "没有最近活动。",
    lifecycleTitle: "任务生命周期", lifecycleOpen: "打开任务", lifecycleRefresh: "刷新", lifecycleClose: "关闭", lifecycleRetry: "重试",
    lifecycleLoading: "正在读取任务生命周期…", lifecycleLoaded: "已读取任务生命周期。",
    lifecycleMissing: "这个任务已不存在。", lifecycleUnavailable: "当前服务端无法提供任务生命周期。", lifecycleFailed: "读取任务生命周期失败。", lifecycleTimedOut: "请求超时。",
    approve: "批准", reject: "否决", approveNamed: "批准：{title}", rejectNamed: "否决：{title}", reviewNamed: "去处理：{title}",
    confirmApprove: "确认批准？", confirm: "确认", cancel: "算了", working: "处理中…", approveFailed: "没有处理成功：{message}",
    rejectReason: "原因（宠物会记下来，写一句）", rejectReasonNamed: "否决“{title}”的原因", rejectConfirm: "确认否决", rejectNeedsReason: "写一句原因再否决"},
  "en-US": {expand: "Expand", collapse: "Collapse", emptyUnfollowed: "No unfollowed tasks.", updatedAt: "Updated {time}", updateFailed: "Refresh failed, will retry; showing data from {time}",
    stalled: "Stalled", stalledCount: "{count} stalled", continueTask: "Ask to continue", send: "Send", sending: "Sending…", sent: "Continue message sent",
    continueMessage: "Continue with the next step on your status card. If you cannot proceed, say what is blocking you.", sendFailed: "Could not send: {message}", sendFailedUnknown: "No reason was returned",
    noLines: "No workstreams recorded", linesForYou: "Workstreams for you ({count})", blocker: "Blocker:", waitingOn: "Waiting for", recent: "Recent activity ({count})", emptyRecent: "No recent activity.",
    lifecycleTitle: "Task lifecycle", lifecycleOpen: "Open task", lifecycleRefresh: "Refresh", lifecycleClose: "Close", lifecycleRetry: "Retry",
    lifecycleLoading: "Loading task lifecycle…", lifecycleLoaded: "Task lifecycle loaded.",
    lifecycleMissing: "This task no longer exists.", lifecycleUnavailable: "Task lifecycle is unavailable on this server.", lifecycleFailed: "Could not load the task lifecycle.", lifecycleTimedOut: "The request timed out.",
    approve: "Approve", reject: "Reject", approveNamed: "Approve: {title}", rejectNamed: "Reject: {title}", reviewNamed: "Review: {title}",
    confirmApprove: "Approve?", confirm: "Confirm", cancel: "Never mind", working: "Working…", approveFailed: "That did not work: {message}",
    rejectReason: "Reason (the pet keeps it; one line)", rejectReasonNamed: "Reason for rejecting “{title}”", rejectConfirm: "Confirm reject", rejectNeedsReason: "Write a reason to reject"},
} satisfies Record<"zh-CN" | "en-US", Record<string, string>>;
type PetDeskCopyKey = keyof (typeof PET_DESK_COPY)["en-US" | "zh-CN"];

function deskCopy(key: PetDeskCopyKey, values: Record<string, string> = {}): string {
  return PET_DESK_COPY[currentLocale() === "en-US" ? "en-US" : "zh-CN"][key].replace(/\{(\w+)\}/g, (_, name: string) => values[name] ?? `{${name}}`);
}

// petDeskClock is HH:MM:SS in local time, the "updated at" stamp.
export function petDeskClock(at: number): string {
  const date = new Date(at);
  if (!Number.isFinite(at) || Number.isNaN(date.getTime())) return "";
  const pad = (n: number): string => String(n).padStart(2, "0");
  return `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;
}

// paintPetDeskUpdated writes the status row's freshness note: when the data
// on screen was fetched, and whether the last refresh failed.
export function paintPetDeskUpdated(root: ParentNode, fetchedAt: number, failed: boolean): void {
  const node = root.querySelector<HTMLElement>("[data-pet-desk-updated]");
  if (!node || !fetchedAt) return;
  const time = petDeskClock(fetchedAt);
  node.textContent = deskCopy(failed ? "updateFailed" : "updatedAt", {time});
  node.classList.toggle("failed", failed);
}

// A reply this short, without markers or line breaks, already fits the
// preview line on the narrowest board, so it gets no expand button.
const PET_DESK_REPLY_SHORT = 40;

// renderDeskLastReply is one line under the status row: what the pet said
// in its latest finished round. No reply, no line. A longer reply shows a
// one-line preview and an expand button; bindPetDesk wires the button.
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
  if (reply.noop) {
    return `<section class="pet-desk-reply" aria-label="${escapeHTML(label)}"><strong>${escapeHTML(label)}</strong><span class="pet-desk-reply-noop">${escapeHTML(translate("petWorkbench.desk.lastNoop"))}</span>${time}</section>`;
  }
  const preview = petDeskReplyPreview(text);
  if (preview === text && text.length <= PET_DESK_REPLY_SHORT) {
    return `<section class="pet-desk-reply" aria-label="${escapeHTML(label)}"><strong>${escapeHTML(label)}</strong>${deskText(text, "span", "pet-desk-reply-text")}${time}</section>`;
  }
  const toggle = `<button type="button" class="pet-desk-reply-toggle" data-pet-desk-reply-toggle aria-expanded="false" aria-controls="pet-desk-reply-full">${escapeHTML(deskCopy("expand"))}</button>`;
  return `<section class="pet-desk-reply" aria-label="${escapeHTML(label)}"><strong>${escapeHTML(label)}</strong>${deskText(preview, "span", "pet-desk-reply-text")}${toggle}${time}<div class="pet-desk-reply-full" id="pet-desk-reply-full" tabindex="0" data-i18n-ignore hidden>${escapeHTML(text)}</div></section>`;
}

// bindPetDesk wires the desk's own controls after every paint: the last
// reply's expand button swaps the preview line for the full text.
export function bindPetDesk(root: ParentNode): void {
  root.querySelectorAll<HTMLButtonElement>("[data-pet-desk-reply-toggle]").forEach(button => {
    const section = button.closest(".pet-desk-reply");
    const preview = section?.querySelector<HTMLElement>(".pet-desk-reply-text");
    const full = section?.querySelector<HTMLElement>(".pet-desk-reply-full");
    if (!preview || !full) return;
    button.addEventListener("click", () => {
      const open = button.getAttribute("aria-expanded") !== "true";
      button.setAttribute("aria-expanded", String(open));
      button.textContent = deskCopy(open ? "collapse" : "expand");
      preview.hidden = open;
      full.hidden = !open;
    });
  });
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
  return `${header}${deskScroll(`${range}<ul class="pet-desk-insights-list">${rows.map(([line, detail]) => `<li><strong>${escapeHTML(line)}</strong>${detail ? `<small>${escapeHTML(detail)}</small>` : ""}</li>`).join("")}</ul>`)}`;
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
  const stalled = deskList<PetDeskStalled>(desk.stalled);
  const later = deskList<PetDeskNeed>(desk.later);
  const waiting = deskList<PetDeskWait>(desk.waiting_others);
  const running = deskList<PetDeskRun>(desk.running);
  const totals = {
    needs: deskTotal(desk.needs_owner_total, needs.length),
    stalled: deskTotal(desk.stalled_total, stalled.length),
    later: deskTotal(desk.later_total, later.length),
    waiting: deskTotal(desk.waiting_others_total, waiting.length),
    running: deskTotal(desk.running_total, running.length),
  };
  const laterTitle = translate("petWorkbench.desk.laterTitle", {count: String(totals.later)});
  const laterBody = later.length
    ? `<ol class="pet-desk-list">${later.map(item => renderDeskNeed(item, null, options, now)).join("")}</ol>${deskTruncated(totals.later, later.length, translate)}`
    : `<div class="field-help pet-desk-empty">${escapeHTML(translate("petWorkbench.desk.emptyLater"))}</div>`;
  const laterDetails = `<details class="pet-desk-later" id="pet-desk-later"><summary>${escapeHTML(laterTitle)}</summary><div class="pet-desk-later-body">${laterBody}</div></details>`;
  const stalledGroup = totals.stalled
    ? `<section class="pet-desk-stalled" aria-labelledby="pet-desk-stalled-title">
      <h4 id="pet-desk-stalled-title">${escapeHTML(deskCopy("stalled"))}</h4>
      <ul class="pet-desk-list">${stalled.map(item => renderDeskStalled(item, options, now)).join("")}</ul>
      ${deskTruncated(totals.stalled, stalled.length, translate)}
    </section>` : "";
  const needsSection = renderDeskSection(
    "pet-desk-needs", "pet-desk-needs",
    translate("petWorkbench.desk.needsTitle", {count: String(totals.needs)}),
    needs.map((item, index) => renderDeskNeed(item, index + 1, options, now)),
    totals.needs, translate("petWorkbench.desk.emptyNeeds"), translate, laterDetails, stalledGroup,
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
  const {lists} = renderOverviewWatchLists(options.watches || [], options, desk);
  // The board is three columns on a wide screen: for-you in the middle (first
  // in the markup, so it is also first for the keyboard and on a phone),
  // waiting and the pet's side on the left, running and the last 7 days on
  // the right. Narrower screens dissolve the columns back into one flow.
  return `<div class="pet-overview pet-desk">
    ${renderDeskStatus(desk, options, totals)}
    ${renderDeskLastReply(desk.last_reply, options, now)}
    <div class="pet-desk-board">
      <div class="pet-desk-col pet-desk-col-main">${needsSection}</div>
      <div class="pet-desk-col pet-desk-col-side">${waitingSection}${renderDeskPetSide(desk, translate)}</div>
      <div class="pet-desk-col pet-desk-col-run">${runningSection}${renderPetInsightsPlaceholder(translate)}</div>
    </div>
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
    tocGroup("pet-record-approvals", "petWorkbench.records.approvals", null, ""),
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
  // Headings take focus (tabindex -1) so a link to a group, such as the old
  // approvals tab now landing on the approval history, can move focus there.
  const section = (id: string, key: string, body: string) => `<section class="pet-record-section" id="${id}" aria-labelledby="${id}-title">
        <h3 id="${id}-title" tabindex="-1">${escapeHTML(translate(`petWorkbench.records.${key}`))}</h3>
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
      ${section("pet-record-approvals", "approvals", `<div class="pet-approvals-toolbar" data-pet-approval-toolbar></div><div id="pet-approval-rows-host" aria-live="polite"></div><div id="pet-approval-more-host"></div>`)}
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
    "approvals": "审批记录",
    "approvalsIntro": "宠物提给你的审批和想法，新的在上，可以按种类和状态筛选。待处理的在概览的“要你定”里。",
    "approvalsEmpty": "还没有审批记录。",
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
    "approvals": "Approval history",
    "approvalsIntro": "Approvals and ideas the pet put to you, newest first, filterable by kind and status. Pending ones are on the overview under “For you”.",
    "approvalsEmpty": "No approvals yet.",
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

/* Records page: approval history ------------------------------------------- */

// Newest first: the history is for looking back, unlike the old approvals
// tab, which was the pending queue (oldest pending first). Pending and
// executing rows sort by when they were created, resolved rows by when they
// were resolved. The input array is not touched.
export function sortPetApprovalHistory<T extends {status?: string; created_at?: string; resolved_at?: string}>(items: T[]): T[] {
  const time = (item: T): number => {
    const parsed = Date.parse(String(item.resolved_at || item.created_at || ""));
    return Number.isFinite(parsed) ? parsed : 0;
  };
  return [...items].sort((left, right) => time(right) - time(left));
}

export interface PetApprovalHistoryOptions {
  translate: Translate;
  now: number;
  /** Shown when nothing matches; the caller picks "no approvals" or "no matches". */
  empty: string;
}

export function renderPetApprovalHistory(items: PetApproval[], options: PetApprovalHistoryOptions): string {
  const rows = items.filter(item => item && typeof item === "object" && !Array.isArray(item));
  if (!rows.length) return `<div class="field-help">${escapeHTML(options.empty)}</div>`;
  return `<div class="pet-approval-rows">${sortPetApprovalHistory(rows).map(item => renderPetApprovalRow(item, {translate: options.translate, now: options.now})).join("")}</div>`;
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

/* Live overview refresh (task-813 round 4) ----------------------------------- */

// Global event types that change what the overview shows. Task, Turn and
// Round events move items between "for you", "waiting", "running" and the
// last reply (the same prefixes the task list refreshes on, minus knowledge:
// the overview shows no knowledge). pet_approval adds or settles approvals
// and ideas; pet_event is a knowledge-sync note in the pet's work state.
// pet_state and pet_action are the pet character's animation and speech and
// do not change the desk, so they are not listed.
const PET_DESK_EVENT_PREFIXES = ["task_", "turn_", "round_"];
const PET_DESK_EVENT_TYPES = ["pet_approval", "pet_event"];

export function petDeskRefreshEvent(type: string): boolean {
  return PET_DESK_EVENT_TYPES.includes(type) || PET_DESK_EVENT_PREFIXES.some(prefix => type.startsWith(prefix));
}

export const PET_DESK_REFRESH = {
  /** A burst of events collapses into one refresh this long after the last. */
  debounceMs: 1000,
  /** A stream of events that never pauses still refreshes this often. */
  maxWaitMs: 5000,
  /** Two refreshes are at least this far apart. */
  minIntervalMs: 3000,
  /** Fallback refresh while the overview is visible. */
  pollMs: 60_000,
  /** Elapsed times ("waited 5 min") are recomputed this often without a read. */
  tickMs: 30_000,
  /** The last-7-days block is re-read at most this often. */
  insightsMinMs: 60_000,
} as const;

export interface PetDeskRefresherOptions {
  /** Read the workbench; resolves when it settles. */
  refresh: () => Promise<unknown>;
  /** Repaint elapsed times from the data already on screen. */
  tick: () => void;
  /** True while the overview tab is mounted and the document is visible. */
  active: () => boolean;
  now?: () => number;
  setTimer?: (callback: () => void, ms: number) => unknown;
  clearTimer?: (handle: unknown) => void;
}

export interface PetDeskRefresher {
  /** An event that may change the overview arrived. */
  notify: () => void;
  /** Refresh now (visibility regained, focus, reconnect), still rate limited. */
  wake: () => void;
  /** Start or resume the fallback poll and the elapsed-time tick. */
  start: () => void;
  /** Stop every timer (left the overview, page hidden or released). */
  stop: () => void;
  /** For tests: whether a read is in flight. */
  busy: () => boolean;
}

// createPetDeskRefresher owns the overview's refresh timing: events are
// debounced with a bounded wait, refreshes keep a minimum interval, a read in flight is never
// doubled (one follow-up is queued instead), and a fallback poll and an
// elapsed-time tick run only while active() holds. Time and timers are
// injectable so the behaviour is testable without waiting.
export function createPetDeskRefresher(options: PetDeskRefresherOptions): PetDeskRefresher {
  const now = options.now ?? Date.now;
  const setTimer = options.setTimer ?? ((callback: () => void, ms: number) => globalThis.setTimeout(callback, ms));
  const clearTimer = options.clearTimer ?? ((handle: unknown) => globalThis.clearTimeout(handle as ReturnType<typeof setTimeout>));
  let pending: unknown = null;
  let poll: unknown = null;
  let tick: unknown = null;
  let inFlight = false;
  let queued = false;
  let lastStart = Number.NEGATIVE_INFINITY;
  let running = false;
  // When the oldest event not yet covered by a read arrived. A plain trailing
  // debounce is pushed back by every new event, so while several Tasks stream
  // events the desk would only move with the fallback poll; this bounds the
  // wait.
  let burstStart: number | null = null;

  const clear = (handle: unknown) => { if (handle !== null) clearTimer(handle); };

  const run = (): void => {
    pending = null;
    if (!options.active()) {
      burstStart = null;
      return;
    }
    if (inFlight) {
      // The follow-up read covers every event seen so far.
      queued = true;
      burstStart = null;
      return;
    }
    const wait = lastStart + PET_DESK_REFRESH.minIntervalMs - now();
    if (wait > 0) {
      schedule(wait);
      return;
    }
    inFlight = true;
    burstStart = null;
    lastStart = now();
    void Promise.resolve().then(options.refresh).catch(() => undefined).finally(() => {
      inFlight = false;
      if (queued) {
        queued = false;
        schedule(0);
      }
      armPoll();
    });
  };

  const schedule = (ms: number): void => {
    clear(pending);
    pending = setTimer(run, Math.max(0, ms));
  };

  const armPoll = (): void => {
    clear(poll);
    poll = running ? setTimer(() => { poll = null; schedule(0); }, PET_DESK_REFRESH.pollMs) : null;
  };

  const armTick = (): void => {
    clear(tick);
    tick = running ? setTimer(() => {
      tick = null;
      if (options.active()) options.tick();
      armTick();
    }, PET_DESK_REFRESH.tickMs) : null;
  };

  return {
    notify() {
      if (!running) return;
      const at = now();
      if (burstStart === null) burstStart = at;
      schedule(Math.min(PET_DESK_REFRESH.debounceMs, Math.max(0, burstStart + PET_DESK_REFRESH.maxWaitMs - at)));
    },
    wake() {
      if (!running) return;
      schedule(0);
    },
    start() {
      if (running) return;
      running = true;
      armPoll();
      armTick();
    },
    stop() {
      running = false;
      queued = false;
      burstStart = null;
      clear(pending); clear(poll); clear(tick);
      pending = poll = tick = null;
    },
    busy: () => inFlight,
  };
}

/* Keeping the owner's place across a background repaint ------------------- */

// petDeskNodeKey is a CSS selector that finds the same control again after
// the overview repaints: the block id plus the control's data attribute and
// target (for example the scroll area of #pet-desk-needs, or the button that
// opens approval a-1). "" when the node has no stable identity.
export function petDeskNodeKey(node: HTMLElement): string {
  const block = node.closest<HTMLElement>("[id]");
  const scope = block?.id ? `#${CSS.escape(block.id)}` : "";
  if (node.id) return `#${CSS.escape(node.id)}`;
  for (const attribute of ["data-pet-approval-open", "data-pet-task-lifecycle", "data-pet-overview-task", "data-pet-desk-continue", "data-pet-desk-send", "data-pet-desk-cancel-continue", "data-pet-desk-approve", "data-pet-desk-reject", "data-pet-watch-task"]) {
    const value = node.getAttribute(attribute);
    if (value !== null) return `${scope} [${attribute}="${CSS.escape(value)}"]${node.classList.contains("pet-desk-item-main") ? ".pet-desk-item-main" : ""}`.trim();
  }
  for (const className of ["pet-desk-scroll", "pet-follow-body", "pet-desk-col", "pet-desk-later-body", "pet-desk-reply-full"]) {
    if (node.classList.contains(className)) {
      if (className === "pet-desk-col") return `.pet-desk-col.${CSS.escape(node.classList[1] || "")}`;
      return scope ? `${scope} .${className}` : "";
    }
  }
  return "";
}


const PET_DESK_SCROLLERS = ".pet-desk-scroll, .pet-follow-body, .pet-desk-col, .pet-desk-later-body, .pet-desk-reply-full";

export interface PetDeskPlace {
  scrolls: Array<[string, number]>;
  laterOpen: boolean;
  replyOpen: boolean;
  focused: string;
}

// capturePetDeskState records what the owner did to the overview: every
// block's scroll position, the open "later" list and reply, and the focused
// control. restorePetDeskState puts it back after the repaint.
export function capturePetDeskState(root: HTMLElement): PetDeskPlace {
  const scrolls: Array<[string, number]> = [];
  root.querySelectorAll<HTMLElement>(PET_DESK_SCROLLERS).forEach(node => {
    const key = petDeskNodeKey(node);
    if (key && node.scrollTop) scrolls.push([key, node.scrollTop]);
  });
  const active = document.activeElement;
  return {
    scrolls,
    laterOpen: root.querySelector<HTMLDetailsElement>("#pet-desk-later")?.open ?? false,
    replyOpen: root.querySelector("[data-pet-desk-reply-toggle]")?.getAttribute("aria-expanded") === "true",
    focused: active instanceof HTMLElement && root.contains(active) ? petDeskNodeKey(active) : "",
  };
}

export function restorePetDeskState(root: HTMLElement, place: PetDeskPlace, restoreFocus: boolean): void {
  const later = root.querySelector<HTMLDetailsElement>("#pet-desk-later");
  if (later && place.laterOpen) later.open = true;
  if (place.replyOpen) {
    const toggle = root.querySelector<HTMLButtonElement>("[data-pet-desk-reply-toggle]");
    if (toggle?.getAttribute("aria-expanded") === "false") toggle.click();
  }
  for (const [key, top] of place.scrolls) {
    const node = root.querySelector<HTMLElement>(key);
    if (node) node.scrollTop = top;
  }
  if (restoreFocus && place.focused) root.querySelector<HTMLElement>(place.focused)?.focus({preventScroll: true});
}

/* Inline approve / reject controller -------------------------------------- */

export interface PetDeskDecisionOptions {
  root: HTMLElement;
  /** Resolve the approval; reason is "" for approve. Rejects with an error. */
  resolve: (approvalID: string, decision: "approve" | "reject", reason: string) => Promise<void>;
  /** After a successful decision: refresh the overview (the row leaves). */
  done: () => void;
  setTimer?: (callback: () => void, ms: number) => unknown;
  clearTimer?: (handle: unknown) => void;
}

export interface PetDeskDecisionState {
  approvalID: string;
  reason: string;
  selectionStart: number | null;
  selectionEnd: number | null;
  focused: boolean;
}

export interface PetDeskDecisions {
  /** Wire the controls of a freshly painted overview. */
  bind: () => void;
  /** The open reject form, if any, so a repaint can put it back. */
  capture: () => PetDeskDecisionState | null;
  restore: (state: PetDeskDecisionState) => void;
}

// createPetDeskDecisions drives the desk's inline approve / reject:
// - approve: one click; for high-risk and delete approvals the row first asks
//   "Approve?" with Confirm / Never mind, folding back after
//   PET_DESK_CONFIRM_MS without a click;
// - reject: opens a one-line reason form; Enter confirms (once there is a
//   reason), Escape folds it;
// - while a decision runs the row's buttons are disabled and the row says
//   "Working…"; a failure says why in the row and re-enables them.
export function createPetDeskDecisions(options: PetDeskDecisionOptions): PetDeskDecisions {
  const setTimer = options.setTimer ?? ((callback: () => void, ms: number) => globalThis.setTimeout(callback, ms));
  const clearTimer = options.clearTimer ?? ((handle: unknown) => globalThis.clearTimeout(handle as ReturnType<typeof setTimeout>));
  const root = options.root;
  let confirmTimer: unknown = null;
  const busy = new Set<string>();

  const row = (id: string) => root.querySelector<HTMLElement>(`[data-pet-desk-approval="${CSS.escape(id)}"]`);
  const status = (id: string, text: string, failed = false) => {
    const node = root.querySelector<HTMLElement>(`[data-pet-desk-status="${CSS.escape(id)}"]`);
    if (!node) return;
    node.textContent = text;
    node.classList.toggle("failed", failed);
  };
  const setBusy = (id: string, on: boolean) => {
    const item = row(id);
    if (!item) return;
    item.classList.toggle("busy", on);
    item.setAttribute("aria-busy", String(on));
    item.querySelectorAll<HTMLButtonElement | HTMLInputElement>("button, input").forEach(control => {
      if (on) control.disabled = true;
      else control.disabled = control.matches("[data-pet-desk-reject-confirm]") && !(item.querySelector<HTMLInputElement>("[data-pet-desk-reject-reason]")?.value.trim());
    });
  };

  const run = async (id: string, decision: "approve" | "reject", reason: string) => {
    if (busy.has(id)) return;
    busy.add(id);
    setBusy(id, true);
    status(id, deskCopy("working"));
    try {
      await options.resolve(id, decision, reason);
      busy.delete(id);
      options.done();
    } catch (error) {
      busy.delete(id);
      setBusy(id, false);
      status(id, deskCopy("approveFailed", {message: error instanceof Error ? error.message : String(error)}), true);
      row(id)?.querySelector<HTMLElement>(decision === "reject" ? "[data-pet-desk-reject-reason]" : "[data-pet-desk-approve]")?.focus();
    }
  };

  const foldConfirm = () => {
    if (confirmTimer !== null) clearTimer(confirmTimer);
    confirmTimer = null;
    root.querySelectorAll<HTMLElement>(".pet-desk-confirm").forEach(node => {
      const id = node.dataset.petDeskConfirmFor || "";
      node.remove();
      const item = row(id);
      item?.querySelectorAll<HTMLElement>("[data-pet-desk-approve], [data-pet-desk-reject]").forEach(button => { button.hidden = false; });
      if (item && !busy.has(id)) status(id, "");
    });
  };

  const askConfirm = (button: HTMLButtonElement, id: string) => {
    foldConfirm();
    const actions = button.closest(".pet-desk-item-actions");
    if (!actions) return;
    actions.querySelectorAll<HTMLElement>("[data-pet-desk-approve], [data-pet-desk-reject]").forEach(other => { other.hidden = true; });
    const holder = document.createElement("span");
    holder.className = "pet-desk-confirm";
    holder.dataset.petDeskConfirmFor = id;
    holder.innerHTML = `<button type="button" class="primary" data-pet-desk-confirm-yes>${escapeHTML(deskCopy("confirm"))}</button><button type="button" data-pet-desk-confirm-no>${escapeHTML(deskCopy("cancel"))}</button>`;
    actions.prepend(holder);
    status(id, deskCopy("confirmApprove"));
    holder.querySelector<HTMLButtonElement>("[data-pet-desk-confirm-yes]")?.addEventListener("click", () => {
      foldConfirm();
      void run(id, "approve", "");
    });
    holder.querySelector<HTMLButtonElement>("[data-pet-desk-confirm-no]")?.addEventListener("click", () => {
      foldConfirm();
      row(id)?.querySelector<HTMLElement>("[data-pet-desk-approve]")?.focus();
    });
    holder.querySelector<HTMLButtonElement>("[data-pet-desk-confirm-yes]")?.focus();
    confirmTimer = setTimer(foldConfirm, PET_DESK_CONFIRM_MS);
  };

  const closeReject = (id: string, focusButton = true) => {
    const form = root.querySelector<HTMLElement>(`[data-pet-desk-reject-form="${CSS.escape(id)}"]`);
    form?.remove();
    if (focusButton) row(id)?.querySelector<HTMLElement>("[data-pet-desk-reject]")?.focus();
  };

  const openReject = (id: string, title: string, reason = "") => {
    root.querySelectorAll<HTMLElement>("[data-pet-desk-reject-form]").forEach(form => form.remove());
    foldConfirm();
    const body = row(id)?.querySelector<HTMLElement>(".pet-desk-item-body");
    if (!body) return null;
    body.insertAdjacentHTML("beforeend", renderPetDeskRejectForm(id, title, reason));
    const form = body.querySelector<HTMLFormElement>(`[data-pet-desk-reject-form="${CSS.escape(id)}"]`);
    const input = form?.querySelector<HTMLInputElement>("[data-pet-desk-reject-reason]");
    const confirm = form?.querySelector<HTMLButtonElement>("[data-pet-desk-reject-confirm]");
    if (!form || !input || !confirm) return null;
    input.addEventListener("input", () => {
      confirm.disabled = !input.value.trim() || busy.has(id);
      status(id, "");
    });
    input.addEventListener("keydown", event => {
      if (event.key === "Escape") {
        event.preventDefault();
        event.stopPropagation();
        closeReject(id);
      }
    });
    form.addEventListener("submit", event => {
      event.preventDefault();
      const reason = input.value.trim();
      if (!reason) {
        status(id, deskCopy("rejectNeedsReason"), true);
        input.focus();
        return;
      }
      void run(id, "reject", reason);
    });
    form.querySelector<HTMLButtonElement>("[data-pet-desk-reject-cancel]")?.addEventListener("click", () => closeReject(id));
    return input;
  };

  return {
    bind() {
      confirmTimer = null;
      root.querySelectorAll<HTMLButtonElement>("[data-pet-desk-approve]").forEach(button => button.addEventListener("click", () => {
        const id = button.dataset.petDeskApprove || "";
        if (!id || busy.has(id)) return;
        if (button.hasAttribute("data-pet-desk-confirm")) askConfirm(button, id);
        else void run(id, "approve", "");
      }));
      root.querySelectorAll<HTMLButtonElement>("[data-pet-desk-reject]").forEach(button => button.addEventListener("click", () => {
        const id = button.dataset.petDeskReject || "";
        if (!id || busy.has(id)) return;
        openReject(id, button.dataset.petDeskTitle || "")?.focus();
      }));
    },
    capture() {
      const form = root.querySelector<HTMLElement>("[data-pet-desk-reject-form]");
      const input = form?.querySelector<HTMLInputElement>("[data-pet-desk-reject-reason]");
      if (!form || !input) return null;
      return {
        approvalID: form.dataset.petDeskRejectForm || "", reason: input.value,
        selectionStart: input.selectionStart, selectionEnd: input.selectionEnd,
        focused: document.activeElement === input,
      };
    },
    restore(state) {
      const title = row(state.approvalID)?.querySelector<HTMLElement>("[data-pet-desk-reject]")?.dataset.petDeskTitle;
      // The approval left the list (resolved elsewhere): nothing to restore.
      if (title === undefined) return;
      const input = openReject(state.approvalID, title, state.reason);
      if (!input) return;
      if (state.focused) {
        input.focus({preventScroll: true});
        if (state.selectionStart !== null) input.setSelectionRange(state.selectionStart, state.selectionEnd ?? state.selectionStart);
      }
    },
  };
}

/* Continue a stalled task -------------------------------------------------- */

interface PetDeskContinueOptions {
  root: HTMLElement;
  send: (taskID: string, message: string) => Promise<unknown>;
  done: () => void;
}

export function createPetDeskContinue(options: PetDeskContinueOptions) {
  const {root} = options;
  const confirming = new Map<string, {message: string; busy: boolean; error?: string}>();
  const sent = new Set<string>();
  const live = document.createElement("span");
  live.className = "visually-hidden";
  live.setAttribute("role", "status");
  root.before(live);

  const rowFor = (id: string) => root.querySelector<HTMLElement>(`[data-pet-desk-stalled="${CSS.escape(id)}"]`);
  const paint = (row: HTMLElement, id: string): void => {
    const state = confirming.get(id);
    const form = row.querySelector<HTMLElement>("[data-pet-desk-continue-form]")!;
    const toggle = row.querySelector<HTMLButtonElement>("[data-pet-desk-continue]")!;
    const quote = row.querySelector<HTMLElement>("[data-pet-desk-continue-message]")!;
    const status = row.querySelector<HTMLElement>("[data-pet-desk-continue-status]")!;
    form.hidden = state === undefined;
    toggle.setAttribute("aria-expanded", String(Boolean(state)));
    quote.textContent = state?.message ?? "";
    const busy = state?.busy ?? false;
    row.classList.toggle("busy", busy);
    form.setAttribute("aria-busy", String(busy));
    row.querySelectorAll<HTMLButtonElement>("button").forEach(button => { button.disabled = busy; });
    status.classList.toggle("failed", Boolean(state?.error));
    status.textContent = state?.error ? deskCopy("sendFailed", {message: state.error}) : busy ? deskCopy("sending") : "";
  };

  root.addEventListener("click", event => {
    if (!(event.target instanceof Element)) return;
    const button = event.target.closest<HTMLButtonElement>("button");
    const row = button?.closest<HTMLElement>("[data-pet-desk-stalled]");
    const id = row?.dataset.petDeskStalled;
    if (!button || !row || !id) return;
    const state = confirming.get(id);
    if (state?.busy || sent.has(id)) return;
    if (button.hasAttribute("data-pet-desk-continue")) {
      if (!state) confirming.set(id, {message: deskCopy("continueMessage"), busy: false});
      paint(row, id);
      row.querySelector<HTMLButtonElement>("[data-pet-desk-send]")?.focus();
    } else if (button.hasAttribute("data-pet-desk-cancel-continue")) {
      confirming.delete(id);
      paint(row, id);
      row.querySelector<HTMLButtonElement>("[data-pet-desk-continue]")?.focus();
    } else if (state && button.hasAttribute("data-pet-desk-send")) {
      state.busy = true;
      state.error = undefined;
      const returnFocus = document.activeElement === button;
      live.textContent = "";
      paint(row, id);
      void options.send(id, state.message).then(() => {
        confirming.delete(id);
        sent.add(id);
        options.done();
        live.textContent = deskCopy("sent");
        if (returnFocus && document.activeElement === document.body) {
          root.querySelector<HTMLElement>("#pet-desk-needs h3")?.focus({preventScroll: true});
        }
      }, error => {
        state.busy = false;
        state.error = (error instanceof Error ? error.message : String(error)) || deskCopy("sendFailedUnknown");
        const current = rowFor(id);
        if (!current) return;
        paint(current, id);
        if (returnFocus && document.activeElement === document.body) {
          current.querySelector<HTMLButtonElement>("[data-pet-desk-send]")?.focus({preventScroll: true});
        }
      });
    }
  });

  return {
    // A refresh begun before the send can still return the just-sent row.
    // Keep it out of the UI until the server no longer lists that task.
    visible(desk: PetDesk | undefined): PetDesk | undefined {
      if (!desk || !sent.size) return desk;
      const rows = deskList<PetDeskStalled>(desk.stalled);
      const visible = rows.filter(item => !sent.has(item.task_id));
      if (rows.length === visible.length) return desk;
      return {...desk, stalled: visible, stalled_total: deskTotal(desk.stalled_total, rows.length) - (rows.length - visible.length)};
    },
    accept(desk: PetDesk | undefined): void {
      if (!desk || (!confirming.size && !sent.size)) return;
      const listed = new Set(deskList<PetDeskStalled>(desk.stalled).map(item => item.task_id));
      for (const id of sent) if (!listed.has(id)) sent.delete(id);
      for (const [id, state] of confirming) if (!state.busy && !listed.has(id)) confirming.delete(id);
    },
    restore(): void {
      for (const id of confirming.keys()) {
        const row = rowFor(id);
        if (row) paint(row, id);
      }
    },
  };
}


/* Task lifecycle ---------------------------------------------------------- */

interface PetDeskLifecycleOptions {
  createDialog: () => HTMLDialogElement;
  read: (taskID: string, signal: AbortSignal) => Promise<TaskLifecycle>;
  timeoutMs: number;
  openTask: (taskID: string) => void;
  close: () => void;
  canReturnFocus: () => boolean;
  formatTime: FormatTime;
  formatDuration: (milliseconds: number) => string;
  statusLabel: (status: string) => string;
  translate: Translate;
}

export function bindPetDeskLifecycle(root: HTMLElement, options: PetDeskLifecycleOptions): void {
  root.querySelectorAll<HTMLButtonElement>("[data-pet-task-lifecycle]").forEach(button => {
    button.addEventListener("click", () => {
      const taskID = button.dataset.petTaskLifecycle;
      if (!taskID) return;
      const key = petDeskNodeKey(button);
      bindTaskLifecycleDialog(options.createDialog(), {
        ...options,
        triggerTitle: button.querySelector("strong")?.textContent || "",
        read: signal => options.read(taskID, signal),
        openTask: () => options.openTask(taskID),
        returnFocus: () => {
          if (!root.isConnected || !options.canReturnFocus()) return;
          const target = (key && root.querySelector<HTMLElement>(key)) || root.querySelector<HTMLElement>("#pet-desk-needs h3");
          target?.focus({preventScroll: true});
        },
        elapsed: since => petDeskElapsedText(since, Date.now(), "", options.translate),
      });
    });
  });
}

function bindTaskLifecycleDialog(dialog: HTMLDialogElement, options: Pick<PetDeskLifecycleOptions, "close" | "formatTime" | "formatDuration" | "statusLabel" | "timeoutMs"> & {
  read: (signal: AbortSignal) => Promise<TaskLifecycle>;
  openTask: () => void;
  returnFocus: () => void;
  triggerTitle: string;
  elapsed: (since: string) => string;
}): void {
  dialog.setAttribute("aria-modal", "true");
  dialog.setAttribute("aria-labelledby", "pet-lifecycle-title");
  dialog.firstElementChild!.innerHTML = `<header>
    <div><p>${deskCopy("lifecycleTitle")}</p><h2 id="pet-lifecycle-title" data-i18n-ignore></h2><div data-pet-lifecycle-meta></div></div>
    <div class="pet-lifecycle-actions">
      <button type="button" data-pet-lifecycle-open>${deskCopy("lifecycleOpen")}</button>
      <button type="button" data-pet-lifecycle-refresh>${deskCopy("lifecycleRefresh")}</button>
      <button type="button" data-pet-lifecycle-close autofocus>${deskCopy("lifecycleClose")}</button>
    </div>
  </header>
  <div class="visually-hidden" role="status" data-pet-lifecycle-live></div>
  <div class="pet-lifecycle-scroll" tabindex="0" aria-labelledby="pet-lifecycle-title"></div>`;
  const title = dialog.querySelector<HTMLElement>("#pet-lifecycle-title")!;
  title.textContent = options.triggerTitle || deskCopy("lifecycleTitle");
  const meta = dialog.querySelector<HTMLElement>("[data-pet-lifecycle-meta]")!;
  const content = dialog.querySelector<HTMLElement>(".pet-lifecycle-scroll")!;
  const live = dialog.querySelector<HTMLElement>("[data-pet-lifecycle-live]")!;
  const refresh = dialog.querySelector<HTMLButtonElement>("[data-pet-lifecycle-refresh]")!;
  let controller: AbortController | null = null;
  const message = (key: PetDeskCopyKey, detail?: string) => {
    live.textContent = deskCopy(key);
    content.innerHTML = `<p>${deskCopy(key)}${detail ? ` ${deskText(detail)}` : ""}</p>`;
  };
  const read = async (): Promise<void> => {
    if (controller || !dialog.open) return;
    const requested = new AbortController();
    controller = requested;
    refresh.setAttribute("aria-disabled", "true");
    refresh.textContent = deskCopy("lifecycleRefresh");
    content.setAttribute("aria-busy", "true");
    message("lifecycleLoading");
    let timeout: ReturnType<typeof setTimeout> | undefined;
    try {
      const [lifecycle, renderer] = await Promise.race([
        Promise.all([options.read(requested.signal), import("./pet_lifecycle.js")]),
        new Promise<never>((_, reject) => {
          timeout = setTimeout(() => reject(new Error(deskCopy("lifecycleTimedOut"))), options.timeoutMs);
        }),
      ]);
      if (requested.signal.aborted || !dialog.open) return;
      const task = lifecycle.task;
      const html = renderer.renderTaskLifecycle(lifecycle, {...options, idleDuration: options.elapsed});
      title.textContent = [task.code, task.title].filter(Boolean).join(" · ") || deskCopy("lifecycleTitle");
      meta.innerHTML = [task.project ? deskText(task.project) : "", task.status ? deskText(options.statusLabel(task.status)) : ""].filter(Boolean).join(" · ");
      content.innerHTML = html;
      content.scrollTop = 0;
      live.textContent = deskCopy("lifecycleLoaded");
    } catch (error) {
      if (requested.signal.aborted || !dialog.open) return;
      requested.abort();
      const failure = error as {status?: number; code?: string; message?: string};
      if (failure?.status === 404) message(failure.code === "task_not_found" ? "lifecycleMissing" : "lifecycleUnavailable");
      else {
        message("lifecycleFailed", failure?.message);
        refresh.textContent = deskCopy("lifecycleRetry");
      }
    } finally {
      clearTimeout(timeout);
      if (controller === requested) controller = null;
      refresh.removeAttribute("aria-disabled");
      content.setAttribute("aria-busy", "false");
    }
  };
  refresh.addEventListener("click", () => void read());
  dialog.querySelector("[data-pet-lifecycle-close]")?.addEventListener("click", () => {
    controller?.abort();
    options.close();
  });
  dialog.querySelector("[data-pet-lifecycle-open]")?.addEventListener("click", options.openTask);
  dialog.addEventListener("close", () => {
    controller?.abort();
    options.returnFocus();
  }, {once: true});
  dialog.showModal();
  void read();
}
