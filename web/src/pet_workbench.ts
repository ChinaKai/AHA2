import type {PetInspection} from "./types.js";

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
