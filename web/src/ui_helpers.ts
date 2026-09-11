import type {Workspace, WorkspaceProbe} from "./types.js";

export interface NavigationSnapshot {
  view?: string;
  projectID?: string;
  taskID?: string;
  agentID?: string;
  taskTab?: string;
  draft?: string;
  drafts?: Record<string, string>;
}

export function eventRefreshesTaskList(type: string): boolean {
  return ["task_", "turn_", "round_", "knowledge_"].some(prefix => type.startsWith(prefix));
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
    for (const [label, probe] of [["Codex", capabilities.codex], ["Claude", capabilities.claude]] as Array<[string, WorkspaceProbe | undefined]>) {
      const text = failedProbe(label, probe);
      if (text) details.push({kind: probe?.status === "execution_failed" ? "bad" : "muted", text});
    }
    if (agentAPIProbe?.status === "ready" || workspace.agent_api_status === "ready") {
      details.push({kind: "muted", text: `Agent API · ${agentAPIProbe?.url || workspace.agent_api_resolved_url || "已连接"}`});
    } else if (agentAPIProbe?.status === "unavailable" || workspace.agent_api_status === "error") {
      details.push({kind: "bad", text: agentAPIProbe?.error || workspace.agent_api_error || "Agent API 反向连接失败"});
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

async function copyText(value: string): Promise<void> {
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
