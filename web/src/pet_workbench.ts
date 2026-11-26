import type {PetApproval, PetChannelHealth, PetDesk, PetDeskCorrection, PetDeskNeed, PetDeskRun, PetDeskWait, PetInspection, PetTaskWatch} from "./types.js";
import {petApprovalDurationText, petApprovalKindIcon, petApprovalRiskLabel, petApprovalStatusLabel} from "./pet_approvals.js";
import {petWatchActionKeys} from "./ui_helpers.js";

export type PetApprovalKindFilter = "all" | "idea" | "decision";
export type PetApprovalStatusFilter = "all" | "pending" | "approved" | "rejected" | "cancelled";

// The server bounds approval_limit at 200 (app.petWorkbenchLimit); the page
// must stop growing there instead of offering a button that cannot load more.
// Measured 2026-11 with a 200-approval fixture: 10 rows on the first page and
// 190 after loading every page under the default pending filter, all the same
// 56px height. Below the ~500-row DOM threshold, so the list keeps its plain
// rendering; revisit this if the server cap grows.
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
  /** Background reload for live events; set by the bound page. */
  refresh?: () => void;
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
  if (options.desk && typeof options.desk === "object") return renderPetDesk(options.desk, options);
  return renderLegacyPetOverview(options);
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

function deskList<T>(value: unknown): T[] {
  return Array.isArray(value) ? value.filter(item => item && typeof item === "object") as T[] : [];
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
  wait: ["review", "executing", "channel"],
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
  return `<li class="pet-desk-item kind-${deskKindClass(kind)}"><div class="pet-desk-item-body">${main}</div></li>`;
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
    ${needsSection}
    <div class="pet-desk-pair">${waitingSection}${runningSection}</div>
    ${renderDeskPetSide(desk, translate)}
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

function renderCoreEntry(entry: {name: string; content: string}, index: number, translate: Translate): string {
  return `<details class="pet-doc-entry" id="pet-record-core-${index}"><summary><span>${escapeHTML(entry.name)}</span><button type="button" class="pet-doc-edit" data-pet-doc-edit data-pet-doc-section="core" data-pet-doc-name="${escapeHTML(entry.name)}">${escapeHTML(translate("common.edit"))}</button></summary><pre>${escapeHTML(entry.content)}</pre></details>`;
}

function renderWorklogEntry(entry: PetRecordEntry, index: number, translate: Translate): string {
  return `<details class="pet-doc-entry" id="pet-record-worklog-${index}"><summary><span>${escapeHTML(entry.date)}</span><button type="button" class="pet-doc-edit" data-pet-doc-edit data-pet-doc-section="worklog" data-pet-doc-name="${escapeHTML(entry.date)}.md">${escapeHTML(translate("common.edit"))}</button></summary><pre>${escapeHTML(entry.content)}</pre></details>`;
}

// renderPetRecords merges the old core and worklog tabs into one body. Core
// documents stay pinned above the dated log. On wide screens the table of
// contents sits in a left column and can collapse so the body takes the full
// width; on phones it starts folded and expands above the body.
export function renderPetRecords(
  core: Array<{name: string; content: string}>,
  worklog: PetRecordEntry[],
  options: PetRecordsOptions,
): string {
  const translate = options.translate;
  const sorted = sortPetWorklog(worklog);
  const tocOpen = options.tocOpen !== false;
  const toc = [
    `<button type="button" data-pet-record-goto="pet-record-core">${escapeHTML(translate("petWorkbench.core"))} (${core.length})</button>`,
    ...core.map((entry, index) => `<button type="button" class="pet-record-toc-sub" data-pet-record-goto="pet-record-core-${index}">${escapeHTML(entry.name)}</button>`),
    `<button type="button" data-pet-record-goto="pet-record-worklog">${escapeHTML(translate("petWorkbench.worklog"))} (${sorted.length})</button>`,
    ...sorted.map((entry, index) => `<button type="button" class="pet-record-toc-sub" data-pet-record-goto="pet-record-worklog-${index}">${escapeHTML(entry.date)}</button>`),
  ].join("");
  const toggleLabel = tocOpen
    ? translate("petWorkbench.recordsTocCollapse")
    : translate("petWorkbench.recordsTocExpand");
  const coreBody = core.length
    ? core.map((entry, index) => renderCoreEntry(entry, index, translate)).join("")
    : `<div class="field-help">${escapeHTML(options.emptyCore)}</div>`;
  const worklogBody = sorted.length
    ? sorted.map((entry, index) => renderWorklogEntry(entry, index, translate)).join("")
    : `<div class="field-help">${escapeHTML(options.emptyWorklog)}</div>`;
  return `<div class="pet-records${tocOpen ? "" : " toc-collapsed"}">
    <aside class="pet-record-toc" id="pet-record-toc">
      <strong>${escapeHTML(translate("petWorkbench.recordsToc"))}</strong>
      <nav id="pet-record-toc-nav">${toc}</nav>
    </aside>
    <div class="pet-record-content">
      <div class="pet-record-toolbar"><button type="button" class="pet-record-toc-toggle" data-pet-record-toc-toggle aria-expanded="${tocOpen}" aria-controls="pet-record-toc-nav">${escapeHTML(toggleLabel)}</button></div>
      <section class="pet-record-section" id="pet-record-core">
        <h3>${escapeHTML(translate("petWorkbench.core"))}</h3>
        ${coreBody}
      </section>
      <section class="pet-record-section" id="pet-record-worklog">
        <h3>${escapeHTML(translate("petWorkbench.worklog"))}</h3>
        ${worklogBody}
      </section>
    </div>
  </div>`;
}
