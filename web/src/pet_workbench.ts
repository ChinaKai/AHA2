import type {PetInspection} from "./types.js";

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

export function petApprovalMatches(
  item: {kind?: string; status?: string},
  kindFilter: PetApprovalKindFilter,
  statusFilter: PetApprovalStatusFilter,
): boolean {
  if (!petApprovalKindMatches(String(item.kind ?? ""), kindFilter)) return false;
  return statusFilter === "all" || String(item.status ?? "") === statusFilter;
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
      if (options.status !== "all" && String(group.status ?? "") !== options.status) continue;
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
  section: string;
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
          await binding.save(binding.section, name, textarea.value);
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
