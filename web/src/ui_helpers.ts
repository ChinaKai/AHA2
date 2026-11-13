import type {Project, Task, Workspace, WorkspaceProbe} from "./types.js";

export type PetWatchAction = "follow" | "unfollow" | "ignore" | "restoreIgnored" | "restoreFollow";

// The follows tab lists both watched and unwatched Tasks. Ignoring is available
// in either case, but restore must return an ignore-only row to the unfollowed
// state instead of silently following it.
export function petWatchActionKeys(item: {watched: boolean; ignored: boolean}): PetWatchAction[] {
  if (!item.watched) return item.ignored ? ["restoreIgnored"] : ["follow", "ignore"];
  return item.ignored ? ["restoreFollow"] : ["unfollow", "ignore"];
}

export interface NavigationSnapshot {
  view?: string;
  projectID?: string;
  project?: Project;
  taskID?: string;
  task?: Task;
  agentID?: string;
  taskTab?: string;
  draft?: string;
  drafts?: Record<string, string>;
}

export function eventRefreshesTaskList(type: string): boolean {
  return ["task_", "turn_", "round_", "knowledge_"].some(prefix => type.startsWith(prefix));
}

export interface ResourceStateLike {
  loading: boolean;
  loaded: boolean;
  error: string;
  updatedAt: number;
}

export interface ResourceLoaderOptions {
  states: Record<string, ResourceStateLike>;
  requests: Map<string, Promise<void>>;
  onStart?: (key: string) => void;
  onFinish?: (key: string) => void;
}

// Cache plus single-flight for lazily loaded view resources. Both the cached-hit
// early return and the in-flight dedupe happen before onStart, so a caller can
// count real requests instead of prefetch attempts. Every view resource must go
// through this loader; a fetch helper that bypasses it is re-requested by the
// idle prefetch even when its data is already on screen.
export function createResourceLoader(options: ResourceLoaderOptions): (key: string, force: boolean, action: () => Promise<void>) => Promise<void> {
  return (key, force, action) => {
    const state = options.states[key];
    if (!force && state.loaded) return Promise.resolve();
    const current = options.requests.get(key);
    if (current) return current;
    state.loading = true;
    state.error = "";
    options.onStart?.(key);
    const request = action().then(() => {
      state.loaded = true;
      state.updatedAt = Date.now();
    }).catch(error => {
      state.error = error instanceof Error ? error.message : String(error);
    }).finally(() => {
      state.loading = false;
      options.requests.delete(key);
      options.onFinish?.(key);
    });
    options.requests.set(key, request);
    return request;
  };
}

export interface ListRefreshSchedulerOptions {
  debounceMs: number;
  maxWaitMs: number;
  now: () => number;
  schedule: (callback: () => void, delay: number) => number;
  cancel: (handle: number) => void;
  canRefresh: () => boolean;
  isInFlight: () => boolean;
  refresh: () => Promise<void>;
}

export interface ListRefreshScheduler {
  request: () => void;
  flush: () => void;
  pending: () => boolean;
  scheduled: () => boolean;
}

// A dense SSE burst can outrun a list request by an order of magnitude. Coalesce
// events into one trailing refresh at min(last event + debounce, first event +
// maxWait), keep a minimum gap between request starts, and never overlap
// requests. The scheduler owns no DOM or API knowledge: the caller decides when
// a refresh is allowed and performs the fetch.
//
// A flush that cannot run (hidden page, open task detail, request in flight)
// leaves the request pending so the caller can retry it when the obstacle
// clears; the in-flight case is retried from the refresh promise's finally.
export function createListRefreshScheduler(options: ListRefreshSchedulerOptions): ListRefreshScheduler {
  let timer = 0;
  let windowStartedAt: number | null = null;
  let lastStartedAt = 0;
  let pending = false;

  function schedule(): void {
    if (!pending) return;
    const now = options.now();
    if (windowStartedAt === null) windowStartedAt = now;
    if (timer) options.cancel(timer);
    // Fire at the earlier of "quiet for one debounce window" and "the first
    // event has waited the maximum". A request start gap is a lower bound only:
    // it must not pin a continuous stream to the debounce cadence, because then
    // the max wait would never apply after the first refresh.
    const debounceAt = now + options.debounceMs;
    const maxWaitAt = windowStartedAt + options.maxWaitMs;
    const gapAt = lastStartedAt ? lastStartedAt + options.debounceMs : 0;
    const fireAt = Math.max(gapAt, Math.min(debounceAt, maxWaitAt));
    timer = options.schedule(flush, Math.max(0, fireAt - now));
  }

  function flush(): void {
    timer = 0;
    if (!pending) {
      windowStartedAt = null;
      return;
    }
    // Keep the request pending: a hidden page, an open task detail, or an
    // in-flight refresh must not drop the event. The caller retries this flush
    // when the page becomes visible or the detail closes, and the finally below
    // retries after the in-flight request settles.
    if (!options.canRefresh() || options.isInFlight()) return;
    pending = false;
    windowStartedAt = null;
    lastStartedAt = options.now();
    void options.refresh().catch(() => undefined).finally(() => {
      if (pending) schedule();
    });
  }

  return {
    request(): void {
      pending = true;
      schedule();
    },
    flush,
    pending: () => pending,
    scheduled: () => timer !== 0,
  };
}

function escapeWorkspaceHTML(value: unknown): string {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

function failedProbe(label: string, probe?: WorkspaceProbe): string {
  if (probe?.status === "not_installed") return `${label} 未安装`;
  if (probe?.status === "execution_failed") return probe.error || `${label} 检测执行失败`;
  return "";
}

function backendBadge(label: string, className: string, probe: WorkspaceProbe): string {
  const version = String(probe.version || "").trim();
  const text = version ? `${label} · ${version}` : label;
  return `<span class="proto ${className}" title="${escapeWorkspaceHTML(text)}">${escapeWorkspaceHTML(text)}</span>`;
}

export function renderWorkspaceDetection(workspace: Workspace): string {
  const capabilities = workspace.capabilities || {};
  const repository = workspace.repository || {};
  const workspaceProbe = capabilities.workspace;
  const agentAPIProbe = capabilities.agent_api;
  const badges: string[] = [];
  const details: Array<{kind: "bad" | "muted"; text: string}> = [];
  if (capabilities.codex?.status === "ready") {
    badges.push(backendBadge("Codex", "codex", capabilities.codex));
  }
  if (capabilities.claude?.status === "ready") {
    badges.push(backendBadge("Claude", "claude", capabilities.claude));
  }
  if (capabilities.dsh?.status === "ready") {
    badges.push(backendBadge("DSH", "dsh", capabilities.dsh));
  }
  if (workspaceProbe?.status === "unavailable") {
    details.push({kind: "bad", text: workspaceProbe.error || "Workspace 不可访问"});
  } else {
    if (repository.status === "ready") {
      details.push({kind: "muted", text: `Git${repository.branch ? ` · ${repository.branch}` : ""}`});
    } else if (repository.status === "not_repository") {
      details.push({kind: "muted", text: repository.message || "非 Git 仓库"});
    } else if (repository.status === "execution_failed") {
      details.push({kind: "bad", text: repository.error || "Git 检测执行失败"});
    }
    if (capabilities.platform?.status === "execution_failed") {
      details.push({kind: "bad", text: capabilities.platform.error || "平台检测执行失败"});
    }
    for (const [label, probe] of [["Codex", capabilities.codex], ["Claude", capabilities.claude], ["DSH", capabilities.dsh]] as Array<[string, WorkspaceProbe | undefined]>) {
      const text = failedProbe(label, probe);
      if (text) details.push({kind: probe?.status === "execution_failed" ? "bad" : "muted", text});
    }
    if (agentAPIProbe?.status === "ready" || workspace.agent_api_status === "ready") {
      // A tunnel-reached workspace is reached over a per-Turn port, not at the
      // stored address. Showing the stored loopback address on its own would claim
      // the workspace connects to 127.0.0.1, which is never true remotely.
      const via = (agentAPIProbe as {via?: string} | undefined)?.via;
      const address = agentAPIProbe?.url || workspace.agent_api_resolved_url || "";
      details.push({
        kind: "muted",
        text: via === "tunnel" ? "Agent API · SSH 反向隧道已连通" : `Agent API · 直连 ${address || "已连接"}`,
      });
    } else if (agentAPIProbe?.status === "unavailable" || workspace.agent_api_status === "error") {
      details.push({kind: "bad", text: `Agent API · 不可用：${agentAPIProbe?.error || workspace.agent_api_error || "反向连接失败"}`});
    } else {
      // Never probed. Naming that state is the point: it previously rendered
      // nothing, so a workspace whose address could not work looked identical to
      // a healthy one until an Agent Turn started failing part-way through.
      details.push({kind: "muted", text: "Agent API · 自动选择（未检测）"});
    }
    if (workspace.agent_api_migration_note) {
      details.push({kind: "muted", text: `Agent API 配置迁移：${workspace.agent_api_migration_note}`});
    }
  }
  const summary = badges.length
    ? `<span class="proto-badges">${badges.join("")}</span>`
    : workspace.health === "unknown"
      ? `<span class="status warn">未检测</span>`
      : workspaceProbe?.status === "unavailable"
        ? `<span class="status bad">Workspace 不可访问</span>`
        : `<span class="status warn">无可用 Backend</span>`;
  return `<div class="workspace-detection">${summary}${details.map(detail => `<small class="${detail.kind}" title="${escapeWorkspaceHTML(detail.text)}">${escapeWorkspaceHTML(detail.text)}</small>`).join("")}</div>`;
}

// Per-protocol probe results for the detection row. "unsupported" stays muted
// because an absent endpoint is normal; "rejected" (HTTP 400) keeps neutral
// wording because the status alone cannot say whether the protocol, model,
// parameters, or auth is at fault. Only transport failures use fault wording.
const PROBE_WIRE_ORDER = ["responses", "chat_completions", "anthropic_messages"] as const;
const PROBE_WIRE_LABELS: Record<string, string> = {
  responses: "Responses",
  chat_completions: "Chat",
  anthropic_messages: "Messages",
};

export interface ProtocolProbeFailure {
  wire: string;
  label: string;
  status: string;
  tone: "bad" | "muted";
  message: string;
  hint: string;
  detail: string;
}

// probeHTTPStatus picks the HTTP code that matches the classified status out
// of the candidate evidence (Anthropic probes can list several URLs). The
// first matching code is the one the backend used to pick the status.
function probeHTTPStatus(status: string, detail: string): number | undefined {
  const codes = Array.from(detail.matchAll(/HTTP\s+(\d{3})/g), match => Number(match[1]));
  const matchesStatus = (code: number): boolean => {
    if (status === "unsupported") return [404, 405, 501].includes(code);
    if (status === "rejected") return code === 400;
    if (status === "unauthorized") return code === 401 || code === 403;
    if (status === "unavailable") return code >= 500;
    return code < 500 && ![400, 401, 403, 404, 405, 429, 501].includes(code);
  };
  return codes.find(matchesStatus) ?? codes[0];
}

export function protocolProbeFailures(capabilities: Record<string, string> | undefined, protocolErrors: Record<string, string> | undefined): ProtocolProbeFailure[] {
  const caps = capabilities || {};
  const errors = protocolErrors || {};
  const failures: ProtocolProbeFailure[] = [];
  for (const wire of PROBE_WIRE_ORDER) {
    const status = caps[wire];
    if (!status || status === "supported" || status === "rate_limited") continue;
    const label = PROBE_WIRE_LABELS[wire] || wire;
    const detail = String(errors[wire] || "");
    const code = probeHTTPStatus(status, detail);
    const suffix = code ? `（HTTP ${code}）` : "";
    let message = `${label} ${status}`;
    let hint = "可展开详情查看探测结果。";
    let tone: "bad" | "muted" = "bad";
    switch (status) {
      case "unsupported":
        message = `${label} 不可用${suffix}`;
        hint = "该网关没有提供这个协议端点，不是网络故障。";
        tone = "muted";
        break;
      case "rejected":
        // A bare HTTP 400 cannot distinguish an unsupported protocol from an
        // unknown model, bad parameters, or auth/gateway policy. Keep the
        // wording neutral instead of claiming the endpoint is absent.
        message = `${label} 请求被拒绝${suffix}`;
        hint = "可能不支持该协议，也可能是模型、参数或鉴权配置错误。";
        break;
      case "unauthorized":
        message = `${label} 鉴权失败${suffix}`;
        hint = "检查 API Key 或鉴权方式。";
        break;
      case "unavailable":
        if (code && code >= 500) {
          message = `${label} 服务异常${suffix}`;
          hint = "网关返回服务端错误，稍后重试；若持续失败请检查 Base URL 或代理。";
        } else {
          message = `${label} 传输错误${suffix}`;
          hint = "检查网络连接、代理或 Base URL 后重试。";
        }
        break;
      case "inconclusive":
        message = `${label} 未确认${suffix}`;
        hint = "网关拒绝了这次探测，可展开详情查看。";
        break;
    }
    failures.push({wire, label, status, tone, message, hint, detail});
  }
  return failures;
}

// renderProtocolProbeStatus renders the per-protocol result for one detected
// model. The default line states what happened in plain terms; the candidate
// URLs and HTTP statuses stay available behind "查看详情".
export function renderProtocolProbeStatus(capabilities: Record<string, string> | undefined, protocolErrors: Record<string, string> | undefined): string {
  const failures = protocolProbeFailures(capabilities, protocolErrors);
  if (!failures.length) return "";
  const summary = failures.map(failure => `<span class="proto-result ${failure.tone}">${escapeWorkspaceHTML(failure.message)}</span>`).join("");
  const hints = [...new Set(failures.map(failure => failure.hint))];
  const details = failures.filter(failure => failure.detail);
  const detailHTML = details.length
    ? `<details class="proto-detail"><summary>查看详情</summary>${details.map(failure => `<small>${escapeWorkspaceHTML(failure.label)}：${escapeWorkspaceHTML(failure.detail)}</small>`).join("")}</details>`
    : "";
  return `<div class="proto-failures">${summary}<small class="proto-hint">${escapeWorkspaceHTML(hints.join(" "))}</small>${detailHTML}</div>`;
}

export const TASK_SLASH_COMMANDS = [
  {name: "/interrupt", insert: "/interrupt", desc: "中断当前 Round"},
  {name: "/complete", insert: "/complete", desc: "将当前 Task 标记为完成"},
  {name: "/reopen", insert: "/reopen", desc: "重新打开已完成或失败的 Task"},
  {name: "/compact", insert: "/compact", desc: "压缩并重置当前 Agent Session"},
  {name: "/reset", insert: "/reset", desc: "重置当前 Agent Session"},
] as const;

export type TaskSlashCommand = typeof TASK_SLASH_COMMANDS[number];

export function matchingSlashCommands(value: string, commands: TaskSlashCommand[]): TaskSlashCommand[] {
  const text = String(value || "").trimStart().toLowerCase();
  if (!text.startsWith("/") || /\s/.test(text)) return [];
  return commands.filter(command => command.name.startsWith(text));
}

export function exactSlashCommand(value: string, commands: readonly TaskSlashCommand[]): TaskSlashCommand | undefined {
  const text = String(value || "").trim().toLowerCase();
  return commands.find(command => command.name === text);
}

// The performance panel is a local debugging preference. It lives only in this
// browser's localStorage and is never sent to the server.
export const PERF_PANEL_STORAGE_KEY = "aha.perfPanel";

// localStorage access throws in some privacy modes. A missing or broken storage
// means "off" rather than a boot failure.
function defaultPerfPanelStorage(): Storage | null {
  try {
    return window.localStorage;
  } catch {
    return null;
  }
}

export function readPerfPanelPreference(storage: Storage | null = defaultPerfPanelStorage()): boolean {
  try {
    return storage?.getItem(PERF_PANEL_STORAGE_KEY) === "1";
  } catch {
    return false;
  }
}

export function writePerfPanelPreference(enabled: boolean, storage: Storage | null = defaultPerfPanelStorage()): void {
  try {
    if (!storage) return;
    if (enabled) storage.setItem(PERF_PANEL_STORAGE_KEY, "1");
    else storage.removeItem(PERF_PANEL_STORAGE_KEY);
  } catch {
    // Quota/security failures must not break the page; the in-memory toggle
    // still works for the current visit.
  }
}

const navigationKey = "aha2.navigation";

export function loadNavigationSnapshot(): NavigationSnapshot {
  try {
    return JSON.parse(sessionStorage.getItem(navigationKey) || "{}") as NavigationSnapshot;
  } catch {
    return {};
  }
}

export function saveNavigationSnapshot(value: NavigationSnapshot): void {
  try {
    sessionStorage.setItem(navigationKey, JSON.stringify(value));
  } catch {
    // Navigation persistence is best-effort in restricted WebViews.
  }
}

export function clearNavigationSnapshot(): void {
  try {
    sessionStorage.removeItem(navigationKey);
  } catch {
    // Ignore unavailable session storage.
  }
}

export interface InteractiveRegionState {
  rootScrollTop: number;
  rootScrollLeft: number;
  scroll: Array<{key: string; top: number; left: number}>;
  details: Array<{key: string; open: boolean}>;
  controls: Array<{
    key: string;
    value: string;
    checked?: boolean;
    selected?: string[];
    disabled: boolean;
    selectionStart?: number | null;
    selectionEnd?: number | null;
  }>;
  expanded: string[];
  focusKey: string;
  focusSelectionStart?: number | null;
  focusSelectionEnd?: number | null;
}

function regionElements(root: HTMLElement): HTMLElement[] {
  return [root, ...root.querySelectorAll<HTMLElement>("*")];
}

function regionElementPath(root: HTMLElement, element: HTMLElement): string {
  const parts: number[] = [];
  let current: HTMLElement | null = element;
  while (current && current !== root) {
    const parent = current.parentElement;
    if (!parent) break;
    parts.push(Array.prototype.indexOf.call(parent.children, current));
    current = parent;
  }
  return parts.reverse().join(".");
}

function regionElementKey(root: HTMLElement, element: HTMLElement): string {
  if (element === root) return "$root";
  if (element.id) return `id:${element.id}`;
  if (element.dataset.uiKey) return `ui:${element.dataset.uiKey}`;
  if (element.dataset.messageId) return `message:${element.dataset.messageId}`;
  const tag = element.tagName.toLowerCase();
  const control = element as HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement;
  if (["input", "select", "textarea"].includes(tag) && control.name) {
    const input = element as HTMLInputElement;
    const choice = tag === "input" && ["checkbox", "radio"].includes(input.type) ? `:${input.type}:${input.value}` : "";
    return `control:${tag}:${control.name}${choice}`;
  }
  return `path:${regionElementPath(root, element)}`;
}

function isRegionControl(element: HTMLElement): element is HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement {
  return ["INPUT", "SELECT", "TEXTAREA"].includes(element.tagName);
}

function shouldCaptureControl(element: HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement): boolean {
  if (element.tagName !== "INPUT") return true;
  return !["button", "submit", "reset", "file", "hidden", "password"].includes((element as HTMLInputElement).type);
}

function shouldCaptureScroll(root: HTMLElement, element: HTMLElement): boolean {
  if (element === root || element.dataset.preserveScroll !== undefined) return true;
  if (["detected-list", "table-wrap", "channel-checkbox-list", "ctx-scroll", "ctx-view", "hardware-config", "terminal-history"].some(name => element.classList.contains(name))) return true;
  if (element.tagName === "PRE") return true;
  return element.scrollHeight > element.clientHeight || element.scrollWidth > element.clientWidth;
}

export function captureInteractiveRegion(root: HTMLElement | null): InteractiveRegionState | null {
  if (!root) return null;
  const elements = regionElements(root);
  const focus = document.activeElement instanceof HTMLElement && elements.includes(document.activeElement) ? document.activeElement : null;
  const selection = focus && isRegionControl(focus) ? focus : null;
  return {
    rootScrollTop: root.scrollTop,
    rootScrollLeft: root.scrollLeft,
    scroll: elements.filter(element => shouldCaptureScroll(root, element)).map(element => ({
      key: regionElementKey(root, element), top: element.scrollTop, left: element.scrollLeft,
    })),
    details: elements.filter(element => element.tagName === "DETAILS").map(element => ({
      key: regionElementKey(root, element), open: (element as HTMLDetailsElement).open,
    })),
    controls: elements.filter(isRegionControl).filter(shouldCaptureControl).map(element => {
      const input = element as HTMLInputElement;
      const select = element as HTMLSelectElement;
      return {
        key: regionElementKey(root, element),
        value: element.value,
        checked: element.tagName === "INPUT" && ["checkbox", "radio"].includes(input.type) ? input.checked : undefined,
        selected: element.tagName === "SELECT" && select.multiple ? [...select.selectedOptions].map(option => option.value) : undefined,
        disabled: element.disabled,
        selectionStart: "selectionStart" in element ? element.selectionStart : undefined,
        selectionEnd: "selectionEnd" in element ? element.selectionEnd : undefined,
      };
    }),
    expanded: elements.filter(element => element.classList.contains("expanded")).map(element => regionElementKey(root, element)),
    focusKey: focus ? regionElementKey(root, focus) : "",
    focusSelectionStart: selection && "selectionStart" in selection ? selection.selectionStart : undefined,
    focusSelectionEnd: selection && "selectionEnd" in selection ? selection.selectionEnd : undefined,
  };
}

// A select can only hold a value one of its options carries; an empty value is
// valid only when a placeholder option exists for it.
function selectValueSupported(select: HTMLSelectElement, value: string): boolean {
  return [...select.options].some(option => option.value === value);
}

export function restoreInteractiveRegion(root: HTMLElement | null, state: InteractiveRegionState | null, restoreRootScroll = true): void {
  if (!root || !state) return;
  const elements = regionElements(root);
  const byKey = new Map(elements.map(element => [regionElementKey(root, element), element]));
  for (const detail of state.details) {
    const element = byKey.get(detail.key);
    if (element?.tagName === "DETAILS") (element as HTMLDetailsElement).open = detail.open;
  }
  for (const saved of state.controls) {
    const element = byKey.get(saved.key);
    if (!element || !isRegionControl(element) || !shouldCaptureControl(element)) continue;
    // A disabled placeholder becoming enabled represents new live data. Keep
    // its newly rendered default rather than restoring the placeholder value.
    if (saved.disabled && !element.disabled) continue;
    const input = element as HTMLInputElement;
    const select = element as HTMLSelectElement;
    if (saved.checked !== undefined && element.tagName === "INPUT") input.checked = saved.checked;
    else if (saved.selected && element.tagName === "SELECT") {
      const selected = new Set(saved.selected);
      [...select.options].forEach(option => { option.selected = selected.has(option.value); });
    } else if (element.tagName === "SELECT") {
      // A select must never be left holding a value none of its options carry:
      // that is selectedIndex -1, and everything derived from the control (the
      // workspace list, the offered skills, the runtime fields) silently empties
      // out. The saved value can be stale this way when the previous render
      // captured the select before its options loaded or its option was removed,
      // so an unsupported value falls back to the freshly rendered default.
      if (selectValueSupported(select, saved.value)) element.value = saved.value;
      else if (select.selectedIndex < 0 && select.options.length > 0) select.selectedIndex = 0;
    } else element.value = saved.value;
    if (saved.selectionStart !== undefined && "setSelectionRange" in element) {
      try { element.setSelectionRange(saved.selectionStart, saved.selectionEnd ?? saved.selectionStart); } catch { /* unsupported input type */ }
    }
  }
  const expanded = new Set(state.expanded);
  for (const element of elements) {
    if (expanded.has(regionElementKey(root, element))) element.classList.add("expanded");
  }
  const focus = state.focusKey ? byKey.get(state.focusKey) : null;
  if (focus && !focus.hasAttribute("disabled")) {
    try { focus.focus({preventScroll: true}); } catch { focus.focus(); }
    if (isRegionControl(focus) && state.focusSelectionStart !== undefined && "setSelectionRange" in focus) {
      try { focus.setSelectionRange(state.focusSelectionStart, state.focusSelectionEnd ?? state.focusSelectionStart); } catch { /* unsupported input type */ }
    }
  }
  for (const saved of state.scroll) {
    if (!restoreRootScroll && saved.key === "$root") continue;
    const element = byKey.get(saved.key);
    if (element) {
      element.scrollTop = saved.top;
      element.scrollLeft = saved.left;
    }
  }
  if (restoreRootScroll) {
    root.scrollTop = state.rootScrollTop;
    root.scrollLeft = state.rootScrollLeft;
  }
}

export function replaceInteractiveRegion(root: HTMLElement, html: string, restoreRootScroll = true): void {
  const state = captureInteractiveRegion(root);
  root.innerHTML = html;
  restoreInteractiveRegion(root, state, restoreRootScroll);
}

interface AgentSessionAPI {
  compactAgentSession(taskID: string, agentID: string): Promise<unknown>;
  resetAgentSession(taskID: string, agentID: string): Promise<unknown>;
}

export async function executeAgentSessionAction(
  api: AgentSessionAPI,
  taskID: string,
  agentID: string,
  action: "compact" | "reset",
): Promise<boolean> {
  const label = action === "compact" ? "压缩并重置" : "重置";
  if (!window.confirm(`${label} ${agentID} 的 Backend Session？`)) return false;
  if (action === "compact") await api.compactAgentSession(taskID, agentID);
  else await api.resetAgentSession(taskID, agentID);
  return true;
}

// Shared so the hardware terminal copies through the same path as message
// bubbles: the async clipboard API where it exists, and a hidden-textarea
// fallback where it does not (plain HTTP, or a browser that withholds it).
export async function copyText(value: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(value);
    return;
  }
  const textarea = document.createElement("textarea");
  textarea.value = value;
  textarea.style.position = "fixed";
  textarea.style.opacity = "0";
  document.body.append(textarea);
  textarea.select();
  document.execCommand("copy");
  textarea.remove();
}

export function bindMessageBubbleControls(root: ParentNode = document): void {
  root.querySelectorAll<HTMLElement>(".message").forEach(message => {
    const preview = message.querySelector<HTMLElement>(".message-preview");
    const full = message.querySelector<HTMLElement>(".message-full-text");
    const toggle = message.querySelector<HTMLButtonElement>("[data-toggle-message]");
    if (preview && full && toggle) {
      toggle.addEventListener("click", () => {
        const expanded = message.classList.toggle("expanded");
        preview.hidden = expanded;
        full.hidden = !expanded;
        toggle.textContent = expanded ? "收起" : `展开 · ${Number(message.dataset.messageChars || 0).toLocaleString()}字符`;
      });
    }
    const copy = message.querySelector<HTMLButtonElement>("[data-copy-message]");
    copy?.addEventListener("click", async () => {
      const text = full || preview;
      if (!text) return;
      await copyText(message.dataset.copyMessageSource ?? text.innerText);
      copy.classList.add("copied");
      window.setTimeout(() => copy.classList.remove("copied"), 1200);
    });
  });
  const dialog = root.querySelector<HTMLDialogElement>("#image-preview-dialog");
  const previewImage = dialog?.querySelector<HTMLImageElement>("[data-image-preview-image]");
  const previewTitle = dialog?.querySelector<HTMLElement>("[data-image-preview-title]");
  const download = dialog?.querySelector<HTMLAnchorElement>("[data-image-preview-download]");
  root.querySelectorAll<HTMLButtonElement>("[data-image-preview]").forEach(button => button.addEventListener("click", () => {
    if (!dialog || !previewImage || !download) return;
    const url = button.dataset.imagePreview || "";
    const name = button.dataset.imageName || "图片";
    previewImage.src = url;
    previewImage.alt = name;
    download.href = url;
    download.download = name;
    if (previewTitle) previewTitle.textContent = name;
    dialog.showModal();
  }));
  dialog?.querySelectorAll<HTMLElement>("[data-image-preview-close]").forEach(button => button.addEventListener("click", () => dialog.close()));
  dialog?.addEventListener("click", event => {
    if (event.target === dialog) dialog.close();
  });
  dialog?.addEventListener("close", () => {
    if (previewImage) previewImage.removeAttribute("src");
  });
}

export interface PageSlice<T> {
  items: T[];
  page: number;
  pages: number;
  total: number;
}

// Client-side list pagination. The requested page is clamped rather than
// trusted: a filter, delete, or data refresh can shrink the list between the
// click and the next render.
export function paginateItems<T>(items: T[], requestedPage: number, pageSize: number): PageSlice<T> {
  const size = Math.max(1, Math.floor(pageSize) || 1);
  const total = items.length;
  const pages = Math.max(1, Math.ceil(total / size));
  const page = Math.min(pages, Math.max(1, Math.floor(requestedPage) || 1));
  const start = (page - 1) * size;
  return {items: items.slice(start, start + size), page, pages, total};
}

export function textMatchesQuery(values: Array<string | null | undefined>, query: string): boolean {
  const needle = query.trim().toLowerCase();
  if (!needle) return true;
  return values.some(value => String(value ?? "").toLowerCase().includes(needle));
}

export function paginationHTML(kind: string, slice: PageSlice<unknown>, totalLabel = ""): string {
  if (slice.total === 0) return "";
  const label = totalLabel || `共 ${slice.total} 个`;
  const controls = slice.pages > 1
    ? `<div class="pager-controls"><button type="button" data-page-kind="${kind}" data-page-delta="-1"${slice.page <= 1 ? " disabled" : ""}>上一页</button><span>第 ${slice.page}/${slice.pages} 页</span><button type="button" data-page-kind="${kind}" data-page-delta="1"${slice.page >= slice.pages ? " disabled" : ""}>下一页</button></div>`
    : "";
  return `<div class="list-footer list-footer-${kind}"><span>${label}</span>${controls}</div>`;
}

// Form checkbox state as bindForm sees it. Its callback receives a FormData,
// not the form element, and a checkbox appears in FormData only when it is
// checked; calling querySelector on FormData throws before any request is made.
export function formCheckbox(form: FormData, name: string): boolean {
  return form.get(name) !== null;
}
