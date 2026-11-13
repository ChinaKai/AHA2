import type {PetApproval, PetChannelHealth, PetInspection, PetTaskWatch} from "./types.js";
import {petApprovalKindIcon, petApprovalStatusLabel} from "./pet_approvals.js";
import {petWatchActionKeys} from "./ui_helpers.js";

export type PetApprovalKindFilter = "all" | "idea" | "decision";
export type PetApprovalStatusFilter = "all" | "pending" | "approved" | "rejected" | "cancelled";

// The server bounds approval_limit at 200 (app.petWorkbenchLimit); the page
// must stop growing there instead of offering a button that cannot load more.
export const PET_APPROVAL_MAX_LIMIT = 200;

export interface PetApprovalPage {
  shown: number;
  total: number;
  canLoadMore: boolean;
}

export interface PetApprovalPageOptions {
  kind: PetApprovalKindFilter;
  status: PetApprovalStatusFilter;
  /** Untruncated kind/status totals from the workbench response. */
  counts?: Array<{kind: string; status: string; count: number}> | null;
  /** Total approval rows; only used when grouped counts are unavailable. */
  total?: number | null;
  /** Pending total from the inspection snapshot; fallback for old responses. */
  pendingTotal?: number | null;
  /** Server page ceiling; the button must not offer an unreachable next page. */
  maxItems?: number | null;
}

export function petApprovalKindMatches(kind: string, filter: PetApprovalKindFilter): boolean {
  return filter === "all" || (filter === "idea" ? kind === "idea" : kind !== "idea");
}

function petApprovalStatusMatches(status: string, filter: PetApprovalStatusFilter): boolean {
  if (filter === "all") return true;
  // A heavy approval being executed in the background is still awaiting the
  // owner's final state, so the pending filter keeps its card visible.
  if (filter === "pending") return status === "pending" || status === "executing";
  return status === filter;
}

export function petApprovalMatches(
  item: {kind?: string; status?: string},
  kindFilter: PetApprovalKindFilter,
  statusFilter: PetApprovalStatusFilter,
): boolean {
  if (!petApprovalKindMatches(String(item.kind ?? ""), kindFilter)) return false;
  return petApprovalStatusMatches(String(item.status ?? ""), statusFilter);
}

function nonNegativeCount(value: unknown): number {
  return typeof value === "number" && Number.isFinite(value) && value > 0 ? Math.trunc(value) : 0;
}

// petApprovalPage reports how many loaded approvals match the active filter and
// whether loading more can still reveal matches. The server pages the table
// pending-first but the filter runs in the browser, so a slice-based "shown vs
// approval_total" footer would keep offering "load more" after every pending
// row was already visible. Prefer the untruncated kind/status totals and fall
// back to the inspection pending count for responses without grouped counts.
export function petApprovalPage(
  items: Array<{kind?: string; status?: string}>,
  options: PetApprovalPageOptions,
): PetApprovalPage {
  const shown = items.filter(item => petApprovalMatches(item, options.kind, options.status)).length;
  const loaded = items.length;
  const maxItems = typeof options.maxItems === "number" && Number.isFinite(options.maxItems) && options.maxItems > 0
    ? Math.trunc(options.maxItems)
    : Number.POSITIVE_INFINITY;
  const capReached = loaded >= maxItems;
  const counts = options.counts;
  if (Array.isArray(counts) && counts.length > 0) {
    let total = 0;
    for (const group of counts) {
      if (!group || !petApprovalKindMatches(String(group.kind ?? ""), options.kind)) continue;
      if (!petApprovalStatusMatches(String(group.status ?? ""), options.status)) continue;
      total += nonNegativeCount(group.count);
    }
    return {shown, total, canLoadMore: shown < total && !capReached};
  }
  const allTotal = typeof options.total === "number" && Number.isFinite(options.total) && options.total >= 0
    ? Math.trunc(options.total)
    : loaded;
  const allLoaded = loaded >= allTotal;
  if (options.status === "pending") {
    const pendingTotal = typeof options.pendingTotal === "number" && Number.isFinite(options.pendingTotal) && options.pendingTotal >= 0
      ? Math.trunc(options.pendingTotal)
      : allTotal;
    return {shown, total: pendingTotal, canLoadMore: shown < pendingTotal && !allLoaded && !capReached};
  }
  return {shown, total: allTotal, canLoadMore: loaded < allTotal && !capReached};
}

// renderPetApprovalLoadMore uses the same load-more component and loading
// affordance as the task list (`list-load-more`, disabled + spinner while the
// next page is in flight). The count always reflects the active filter.
export function renderPetApprovalLoadMore(
  page: PetApprovalPage,
  loading: boolean,
  translate: Translate,
  spinnerHTML = "",
  capped = false,
): string {
  if (page.total <= 0) return "";
  const summary = `<span>${escapeHTML(translate("petWorkbench.loadedCount", {shown: String(page.shown), total: String(page.total)}))}</span>`;
  const hint = capped
    ? `<span class="pet-approval-limit-hint">${escapeHTML(translate("petWorkbench.approvalLimitReached", {limit: String(PET_APPROVAL_MAX_LIMIT)}))}</span>`
    : "";
  if (!page.canLoadMore) return `<div class="list-load-more pet-approval-load-more">${summary}${hint}</div>`;
  const label = loading
    ? `${spinnerHTML}${escapeHTML(translate("petWorkbench.loadingMore"))}`
    : escapeHTML(translate("petWorkbench.loadMore"));
  return `<div class="list-load-more pet-approval-load-more">${summary}<button type="button" data-pet-load-more="approval" ${loading ? "disabled" : ""}>${label}</button></div>`;
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

// The workbench handler bounds the database work at 10s on the server. The
// client waits slightly longer per attempt so a server-side timeout is reported
// as a normal 5xx and retried once instead of racing the response.
export const PET_WORKBENCH_TIMEOUT_MS = 12_000;
export const PET_WORKBENCH_RETRY_DELAY_MS = 400;

type Translate = (key: string, values?: Record<string, string | number>) => string;
type FormatTime = (value: string) => string;

export interface PetWorkbenchLoaderOptions<T> {
  read: (signal: AbortSignal) => Promise<T>;
  /** False once the rendered node was replaced or the page navigated away. */
  isCurrent: () => boolean;
  onLoading: () => void;
  onData: (value: T) => void;
  onError: (message: string) => void;
  timeoutMs?: number;
  timeoutMessage?: string;
  retryDelayMs?: number;
  /** Bounded automatic retries; defaults to exactly one. */
  maxRetries?: number;
  sleep?: (ms: number) => Promise<void>;
}

export interface PetWorkbenchLoader {
  load: () => Promise<void>;
  busy: () => boolean;
  dispose: () => void;
}

class PetWorkbenchTimeoutError extends Error {}

function retryableError(error: unknown): boolean {
  if (error instanceof PetWorkbenchTimeoutError) return true;
  const status = (error as {status?: unknown} | null)?.status;
  if (typeof status === "number") return status >= 500;
  // A fetch network failure has no HTTP status; retry it once like a 5xx.
  return true;
}

function errorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

// createPetWorkbenchLoader owns the loading state machine so a superseded
// render can never leave the visible body on "loading" forever: every path
// clears `loading`, stale attempts cannot write, and every final failure is
// reported through onError (which renders the retry affordance).
export function createPetWorkbenchLoader<T>(options: PetWorkbenchLoaderOptions<T>): PetWorkbenchLoader {
  const timeoutMs = options.timeoutMs ?? PET_WORKBENCH_TIMEOUT_MS;
  const retryDelayMs = options.retryDelayMs ?? PET_WORKBENCH_RETRY_DELAY_MS;
  const maxRetries = options.maxRetries ?? 1;
  const timeoutMessage = options.timeoutMessage ?? "加载超时，请重试";
  const sleep = options.sleep ?? ((ms: number) => new Promise<void>(resolve => { globalThis.setTimeout(resolve, ms); }));
  let inFlight: Promise<void> | null = null;
  let loading = false;
  let disposed = false;
  let controller: AbortController | null = null;

  const current = () => !disposed && options.isCurrent();

  // A render callback is caller code: if it throws, the failure must still be
  // visible instead of leaving the body on "loading" with an unhandled
  // rejection. onError itself is also guarded so load() never rejects.
  const reportError = (message: string): void => {
    if (!current()) return;
    try {
      options.onError(message);
    } catch {
      // Nothing else can surface the error; keep loading state clearing in finally.
    }
  };

  const attempt = async (): Promise<{ok: true; value: T} | {ok: false; error: unknown; retryable: boolean}> => {
    const request = new AbortController();
    controller = request;
    let timer: ReturnType<typeof setTimeout> | undefined;
    try {
      const value = await Promise.race([
        options.read(request.signal),
        new Promise<never>((_, reject) => {
          timer = globalThis.setTimeout(() => {
            request.abort();
            reject(new PetWorkbenchTimeoutError(timeoutMessage));
          }, timeoutMs);
        }),
      ]);
      return {ok: true, value};
    } catch (error) {
      return {ok: false, error, retryable: current() && retryableError(error)};
    } finally {
      if (timer !== undefined) globalThis.clearTimeout(timer);
      if (controller === request) controller = null;
    }
  };

  const run = async (): Promise<void> => {
    loading = true;
    let message = "";
    try {
      if (current()) {
        try {
          options.onLoading();
        } catch (error) {
          reportError(errorText(error));
          return;
        }
      }
      for (let index = 0; index <= maxRetries; index++) {
        if (!current()) return;
        if (index > 0) {
          await sleep(retryDelayMs * index);
          if (!current()) return;
        }
        const result = await attempt();
        if (!current()) return;
        if (result.ok) {
          try {
            options.onData(result.value);
          } catch (error) {
            // Rendering failed after the request succeeded; report it as the
            // final error so the retry affordance is rendered.
            reportError(errorText(error));
          }
          return;
        }
        message = errorText(result.error);
        if (!result.retryable) break;
      }
      reportError(message);
    } finally {
      loading = false;
    }
  };

  return {
    load(): Promise<void> {
      if (disposed) return Promise.resolve();
      if (inFlight) return inFlight;
      const task = run().finally(() => {
        if (inFlight === task) inFlight = null;
      });
      inFlight = task;
      return task;
    },
    busy: () => loading,
    dispose(): void {
      disposed = true;
      controller?.abort();
    },
  };
}

export interface PetWorkbenchPageBinding {
  body: HTMLElement;
  page: HTMLElement | null;
  loader: {dispose: () => void};
  /** The tab this binding last rendered; kept in sync by the caller. */
  tab: string;
}

export interface PetWorkbenchRenderContext {
  /** The view the shell is about to render. */
  view: string;
  /**
   * False once the session ended (logout or expiry). The page must not survive
   * an unauthenticated render, or its in-flight request would keep the
   * connection busy after the user is back on the login screen.
   */
  authenticated: boolean;
  /** Latest programmatic tab request; a preserved page follows it. */
  tab?: string;
}

export interface PetWorkbenchPageKeeper {
  readonly binding: PetWorkbenchPageBinding | null;
  /** True when this exact live body is already owned by the keeper. */
  owns: (body: HTMLElement | null) => boolean;
  /**
   * Called once per render before `#app` is replaced. Returns the mounted page
   * to carry over while staying on the workbench, and releases the binding
   * (aborting its in-flight request) when the render leaves the view or the
   * session ends. A changed programmatic tab is selected on the preserved page
   * so its listeners keep driving the live body.
   */
  beforeRender: (context: PetWorkbenchRenderContext) => HTMLElement | null;
  adopt: (body: HTMLElement, page: HTMLElement | null, loader: {dispose: () => void}, tab: string) => PetWorkbenchPageBinding;
  release: () => void;
}

// swapPetWorkbenchPage re-inserts a page carried across a render. The shell's
// innerHTML assignment has already detached the old page, so the caller must not
// test isConnected here; keeper.beforeRender() decided reuse while it was live.
export function swapPetWorkbenchPage(app: ParentNode, preservedPage: HTMLElement | null): boolean {
  if (!preservedPage) return false;
  const freshPage = app.querySelector<HTMLElement>(".pet-workbench-page");
  if (!freshPage) return false;
  freshPage.replaceWith(preservedPage);
  return true;
}

// render() replaces the whole app shell. Re-binding the workbench to a fresh
// body on every render aborts the in-flight request and starts another; with a
// slow response, unrelated renders (navigation, resource loads, messages) can
// keep restarting it so the page never leaves "loading". This keeper makes the
// mounted workbench page survive same-view renders: the caller re-inserts the
// existing page, the body stays identical, and the loader keeps its request.
// Only actually leaving the view releases (aborts) it.
export function createPetWorkbenchPageKeeper(): PetWorkbenchPageKeeper {
  let binding: PetWorkbenchPageBinding | null = null;
  const release = (): void => {
    const previous = binding;
    binding = null;
    previous?.loader.dispose();
  };
  return {
    get binding() {
      return binding;
    },
    owns(body) {
      return Boolean(body && binding && binding.body === body && body.isConnected);
    },
    beforeRender(context) {
      // Logout renders the login view while state.view may still be
      // "pet-workbench"; the session flag is what actually ends the page.
      if (!context.authenticated || context.view !== "pet-workbench") {
        release();
        return null;
      }
      if (!binding || !binding.body.isConnected) return null;
      if (!binding.page || !binding.page.isConnected) return null;
      // A programmatic tab change (for example the pet panel's settings entry)
      // updates petWorkbenchTab without a click on the preserved page. Clicking
      // here runs the same listener the user would, so the tab becomes active
      // and its body renders while the loader keeps its request.
      if (context.tab && binding.tab !== context.tab) {
        binding.page.querySelector<HTMLButtonElement>(`[data-pet-workbench-tab="${context.tab}"]`)?.click();
      }
      return binding.page;
    },
    adopt(body, page, loader, tab) {
      // A different body means the old page was not carried across a render
      // (binding failure, or the view was rebuilt); abort its request. The same
      // page keeps its loader because render() preserved the body node.
      if (binding && binding.body !== body) release();
      binding = {body, page, loader, tab};
      return binding;
    },
    release,
  };
}

function escapeHTML(value: unknown): string {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

// renderPetWorkbenchLoadError is the single retryable failure state, shared by
// the loader's onError and the outer binding fallback. Plain text only, so a
// server/renderer error message cannot inject markup.
export function renderPetWorkbenchLoadError(message: string, translate: Translate): string {
  const text = textValue(message) || translate("petWorkbench.loadFailed");
  return `<div class="field-help">${escapeHTML(text)} <button type="button" data-pet-workbench-retry>${escapeHTML(translate("common.retry"))}</button></div>`;
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

// renderPetInspectionSummary renders the compact snapshot above the channel
// cards. Missing or non-numeric values are omitted instead of rendering
// "undefined"/NaN; a snapshot with nothing trustworthy shows an empty state.
export function renderPetInspectionSummary(inspection: PetInspection | null | undefined, translate: Translate, formatTime: FormatTime): string {
  if (!inspection || typeof inspection !== "object") {
    return `<div class="field-help">${escapeHTML(translate("petWorkbench.inspectionUnavailable"))}</div>`;
  }
  const health = textValue(inspection.health);
  const healthLabel = health === "ok"
    ? translate("petWorkbench.inspectionHealthy")
    : health === "degraded"
      ? translate("petWorkbench.inspectionDegraded")
      : translate("petWorkbench.inspectionUnknown");

  const meta: string[] = [];
  const version = textValue(inspection.version);
  const webVersion = textValue(inspection.web_version);
  if (version) meta.push(`${translate("petWorkbench.inspectionVersion")}: ${escapeHTML(version)}`);
  if (webVersion) meta.push(`${translate("petWorkbench.inspectionWebVersion")}: ${escapeHTML(webVersion)}`);
  const startedAt = textValue(inspection.started_at);
  if (startedAt) meta.push(`${translate("petWorkbench.inspectionStartedAt")}: ${escapeHTML(formatTime(startedAt))}`);
  const uptime = countValue(inspection.uptime_minutes);
  if (uptime !== null) meta.push(`${translate("petWorkbench.inspectionUptime")}: ${escapeHTML(uptimeText(uptime, translate))}`);
  const checkedAt = textValue(inspection.checked_at);
  if (checkedAt) meta.push(`${translate("petWorkbench.inspectionCheckedAt")}: ${escapeHTML(formatTime(checkedAt))}`);
  const degraded = countValue(inspection.degraded_channel_count);
  if (degraded !== null) meta.push(`${translate("petWorkbench.inspectionDegradedChannels")}: ${degraded}`);

  const taskCounts: Array<[string, number | null]> = [
    ["petWorkbench.inspectionTasksTotal", countValue(inspection.tasks_total)],
    ["petWorkbench.inspectionActive", countValue(inspection.active)],
    ["petWorkbench.inspectionWaitingUser", countValue(inspection.waiting_user)],
    ["petWorkbench.inspectionFailed", countValue(inspection.failed)],
    ["petWorkbench.inspectionBlocked", countValue(inspection.blocked)],
    ["petWorkbench.inspectionCompleted", countValue(inspection.completed)],
  ];
  const stats = taskCounts
    .filter(([, value]) => value !== null)
    .map(([key, value]) => `<span><small>${escapeHTML(translate(key))}</small><strong>${value}</strong></span>`)
    .join("");
  const loops: string[] = [];
  const openTasks = countValue(inspection.open_tasks);
  if (openTasks !== null) loops.push(`${translate("petWorkbench.inspectionOpenTasks")}: ${openTasks}`);
  const attentionTasks = countValue(inspection.attention_tasks);
  if (attentionTasks !== null) loops.push(`${translate("petWorkbench.inspectionAttentionTasks")}: ${attentionTasks}`);
  const pendingApprovals = countValue(inspection.pending_approvals);
  if (pendingApprovals !== null) loops.push(`${translate("petWorkbench.inspectionPendingApprovals")}: ${pendingApprovals}`);

  const note = textValue(inspection.note);
  const hasContent = meta.length > 0 || stats !== "" || loops.length > 0 || note !== "" || health === "ok" || health === "degraded";
  if (!hasContent) {
    return `<div class="field-help">${escapeHTML(translate("petWorkbench.inspectionUnavailable"))}</div>`;
  }

  const badgeClass = health === "ok" ? "ok" : health === "degraded" ? "degraded" : "unknown";
  return `<section class="pet-inspection-summary">
    <header class="pet-inspection-summary-head"><strong>${escapeHTML(translate("petWorkbench.inspectionSummary"))}</strong><span class="pet-inspection-health ${badgeClass}">${escapeHTML(healthLabel)}</span></header>
    ${meta.length ? `<div class="pet-inspection-meta">${meta.map(item => `<span>${item}</span>`).join("")}</div>` : ""}
    ${stats ? `<div class="pet-inspection-stats">${stats}</div>` : ""}
    ${loops.length ? `<div class="pet-inspection-loops">${loops.map(item => `<span>${escapeHTML(item)}</span>`).join("")}</div>` : ""}
    ${note ? `<div class="pet-inspection-note">${escapeHTML(note)}</div>` : ""}
  </section>`;
}

/* Four-tab workbench: overview, approvals, records, settings ---------------- */

export type PetWorkbenchTab = "overview" | "approvals" | "records" | "settings";
export const PET_WORKBENCH_TABS: readonly PetWorkbenchTab[] = ["overview", "approvals", "records", "settings"];

// Legacy tab names stay routable: "ideas" was the old approvals tab, and the
// five-tab layout folded core/worklog into records and inspection/follows into
// overview. Anything unknown falls back to overview, the default tab.
export function mapPetWorkbenchTab(value: unknown): PetWorkbenchTab {
  switch (String(value ?? "").trim()) {
    case "approvals":
    case "ideas":
      return "approvals";
    case "records":
    case "core":
    case "worklog":
      return "records";
    case "settings":
      return "settings";
    case "overview":
    case "inspection":
    case "follows":
      return "overview";
    default:
      return "overview";
  }
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

// renderPetOverview is the landing tab: what needs attention right now, the
// three numerals that jump to their page, the compact follow list and the last
// few changes. Inspection details and channel cards stay collapsed so the
// owner is not greeted by a dashboard wall.
export function renderPetOverview(options: PetOverviewOptions): string {
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

  const watched = watches.filter(item => item.watched && !item.ignored);
  const others = watches.filter(item => !item.watched || item.ignored);
  const followRows = watched.length
    ? watched.map(item => renderWatchRow(item, options)).join("")
    : `<div class="field-help">${escapeHTML(translate("petWorkbench.emptyFollows"))}</div>`;
  const otherRows = others.length
    ? `<details class="pet-overview-other"><summary>${escapeHTML(translate("petWorkbench.otherFollows", {count: String(others.length)}))}</summary>${others.map(item => renderWatchRow(item, options)).join("")}</details>`
    : "";

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
    <section class="pet-overview-follows" id="pet-overview-follows">
      <header><h3>${escapeHTML(translate("petWorkbench.overviewFollows"))}</h3><small>${escapeHTML(translate("petWorkbench.followHint"))}</small></header>
      ${followRows}
      ${otherRows}
    </section>
    <section class="pet-overview-recent">
      <h3>${escapeHTML(translate("petWorkbench.overviewRecent"))}</h3>
      <div class="pet-overview-activity-list">${recent}</div>
    </section>
    <details class="pet-overview-inspection">
      <summary>${escapeHTML(translate("petWorkbench.inspectionSummary"))}</summary>
      ${renderPetInspectionSummary(inspection, translate, options.formatTime)}
    </details>
    <details class="pet-overview-channels" id="pet-overview-channels">
      <summary>${escapeHTML(translate("petWorkbench.channelHealth"))}</summary>
      ${renderPetChannelHealth(channels, textValue(inspection?.note), channelTotal, translate)}
    </details>
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
}

// Worklog entries are dated filenames, so newest-first is a plain string sort.
// Copy first: the tab keeps the server response for pagination counts.
export function sortPetWorklog<T extends {date?: string}>(entries: T[]): T[] {
  return [...entries].sort((left, right) => String(right.date || "").localeCompare(String(left.date || "")));
}

function renderCoreEntry(entry: {name: string; content: string}, index: number, translate: Translate): string {
  return `<details class="pet-doc-entry" id="pet-record-core-${index}"><summary><span>${escapeHTML(entry.name)}</span><button type="button" class="pet-doc-edit" data-pet-doc-edit data-pet-doc-section="core" data-pet-doc-name="${escapeHTML(entry.name)}">${escapeHTML(translate("common.edit"))}</button></summary><pre>${escapeHTML(entry.content)}</pre></details>`;
}

function renderWorklogEntry(entry: PetRecordEntry, translate: Translate): string {
  return `<details class="pet-doc-entry"><summary><span>${escapeHTML(entry.date)}</span><button type="button" class="pet-doc-edit" data-pet-doc-edit data-pet-doc-section="worklog" data-pet-doc-name="${escapeHTML(entry.date)}.md">${escapeHTML(translate("common.edit"))}</button></summary><pre>${escapeHTML(entry.content)}</pre></details>`;
}

// renderPetRecords merges the old core and worklog tabs into one body. Core
// documents stay pinned above the dated log; the table of contents is a
// collapsible details that jumps to (and opens) one record.
export function renderPetRecords(
  core: Array<{name: string; content: string}>,
  worklog: PetRecordEntry[],
  options: PetRecordsOptions,
): string {
  const translate = options.translate;
  const sorted = sortPetWorklog(worklog);
  const toc = [
    `<button type="button" data-pet-record-goto="pet-record-core">${escapeHTML(translate("petWorkbench.core"))} (${core.length})</button>`,
    ...core.map((entry, index) => `<button type="button" class="pet-record-toc-sub" data-pet-record-goto="pet-record-core-${index}">${escapeHTML(entry.name)}</button>`),
    `<button type="button" data-pet-record-goto="pet-record-worklog">${escapeHTML(translate("petWorkbench.worklog"))} (${sorted.length})</button>`,
  ].join("");
  const coreBody = core.length
    ? core.map((entry, index) => renderCoreEntry(entry, index, translate)).join("")
    : `<div class="field-help">${escapeHTML(options.emptyCore)}</div>`;
  const worklogBody = sorted.length
    ? sorted.map(entry => renderWorklogEntry(entry, translate)).join("")
    : `<div class="field-help">${escapeHTML(options.emptyWorklog)}</div>`;
  return `<div class="pet-records">
    <details class="pet-record-toc"><summary>${escapeHTML(translate("petWorkbench.recordsToc"))}</summary><nav>${toc}</nav></details>
    <section class="pet-record-section" id="pet-record-core">
      <h3>${escapeHTML(translate("petWorkbench.core"))}</h3>
      ${coreBody}
    </section>
    <section class="pet-record-section" id="pet-record-worklog">
      <h3>${escapeHTML(translate("petWorkbench.worklog"))}</h3>
      ${worklogBody}
    </section>
  </div>`;
}

export function renderPetChannelHealth(items: PetChannelHealth[], note: string, total: number, translate: Translate): string {
  if (items.length === 0) {
    return `<div class="field-help">${escapeHTML(translate("petWorkbench.emptyChannels"))}</div>${note ? `<div class="pet-channel-note">${escapeHTML(note)}</div>` : ""}`;
  }
  const summary = note
    ? `<div class="pet-channel-note">${escapeHTML(note)}</div>`
    : `<div class="field-help">${escapeHTML(translate("petWorkbench.channelHealthy"))}</div>`;
  const truncated = total > items.length
    ? `<div class="field-help">${escapeHTML(translate("petWorkbench.channelShown", {shown: String(items.length), total: String(total)}))}</div>`
    : "";
  const cards = items.map(item => {
    const availability = item.effective_availability ? ` / ${item.effective_availability}` : "";
    const oldest = item.oldest_pending_minutes > 0
      ? translate("petWorkbench.channelAgeMinutes", {minutes: String(item.oldest_pending_minutes)})
      : "-";
    const attempts = item.stuck_pending || item.queued_pending || item.attempted_pending
      ? ` (attempts=0 ${item.stuck_pending}，${translate("petWorkbench.channelQueuedPending")} ${item.queued_pending}，attempts>0 ${item.attempted_pending})`
      : "";
    const expired = item.expired_leases > 0
      ? `<span>${translate("petWorkbench.channelExpiredLeases")}: ${item.expired_leases}</span>`
      : "";
    return `<article class="pet-channel-card${item.degraded ? " degraded" : ""}">
      <header><strong>${escapeHTML(item.name)}</strong><span class="pet-channel-badge ${item.degraded ? "degraded" : "ok"}">${escapeHTML(item.degraded ? translate("petWorkbench.channelDegraded") : translate("petWorkbench.channelHealthyLabel"))}</span></header>
      <div class="pet-channel-grid">
        <span>${translate("petWorkbench.channelStatus")}: ${escapeHTML(item.status || "-")}${escapeHTML(availability)}</span>
        <span>${translate("petWorkbench.channelPending")}: ${item.pending}${attempts}</span>
        <span>${translate("petWorkbench.channelOldestPending")}: ${escapeHTML(oldest)}</span>
        ${expired}
        <span>${translate("petWorkbench.channelDeadLetter")}: ${item.dead_letter}</span>
        ${item.last_error_code ? `<span>${translate("petWorkbench.channelLastError")}: ${escapeHTML(item.last_error_code)}</span>` : ""}
      </div>
      ${item.note ? `<small>${escapeHTML(item.note)}</small>` : ""}
    </article>`;
  }).join("");
  return `${summary}${truncated}<div class="pet-channel-list">${cards}</div>`;
}
