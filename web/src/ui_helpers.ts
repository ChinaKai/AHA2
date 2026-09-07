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

export function renderWorkspaceDetection(workspace: Workspace): string {
  const capabilities = workspace.capabilities || {};
  const repository = workspace.repository || {};
  const workspaceProbe = capabilities.workspace;
  const badges: string[] = [];
  const details: Array<{kind: "bad" | "muted"; text: string}> = [];
  if (capabilities.codex?.status === "ready") {
    badges.push(`<span class="proto codex" title="Codex ${escapeWorkspaceHTML(capabilities.codex.version || "")}">Codex</span>`);
  }
  if (capabilities.claude?.status === "ready") {
    badges.push(`<span class="proto claude" title="Claude Code ${escapeWorkspaceHTML(capabilities.claude.version || "")}">Claude</span>`);
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
