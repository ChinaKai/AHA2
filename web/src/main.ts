import {api} from "./api.js";
import {renderConversationList} from "./conversation_ui.js";
import {icon} from "./icons.js";
import {bindHardwarePanel, stopHardwarePanel} from "./hardware_panel.js";
import {bindPromptAdmin, loadPromptCatalog, renderPromptAdmin} from "./prompt_admin.js";
import {renderComposerAgentOptions, renderComposerTools} from "./task_composer.js";
import {renderTaskToolButtons, renderTaskToolContent, renderTaskToolPanel} from "./task_tools.js";
import type {TaskTool} from "./task_tools.js";
import {TASK_SLASH_COMMANDS, bindMessageBubbleControls, clearNavigationSnapshot, exactSlashCommand, executeAgentSessionAction, loadNavigationSnapshot, matchingSlashCommands, saveNavigationSnapshot} from "./ui_helpers.js";
import {
  compactNumber,
  contextPercent,
  formatDuration,
  isActiveTurn,
  latestAgentTurns,
  renderAgentConfigDialog,
  renderAgentTurnCard,
  renderContextMetrics,
  usageNumber,
} from "./task_agents.js";
import type {
  AuthStatus,
  ConversationCategory,
  ConversationItem,
  DetectedModel,
  Knowledge,
  Model,
  Project,
  Provider,
  SystemInfo,
  Task,
  TaskContextDetail,
  TaskDetail,
  Workspace,
} from "./types.js";
type View = "projects" | "models" | "tasks" | "knowledge" | "prompts";
interface State {
  auth: AuthStatus | null;
  view: View;
  loading: boolean;
  error: string;
  notice: string;
  renderPending: boolean;
  system: SystemInfo;
  projects: Project[];
  workspaces: Workspace[];
  providers: Provider[];
  models: Model[];
  tasks: Task[];
  knowledge: Knowledge[];
  selectedTask: TaskDetail | null;
  taskConversation: ConversationItem[];
  taskConversationHasMore: boolean;
  taskConversationBefore: number;
  taskConversationLatest: number;
  taskEventCursor: number;
  taskCategories: Record<ConversationCategory, boolean>;
  taskContext: TaskContextDetail | null;
  taskRealtimeState: "connecting" | "live" | "fallback";
  taskDraft: string;
  taskDrafts: Record<string, string>;
  selectedTaskAgent: string;
  selectedProject: Project | null;
  dialogProjectID: string;
  taskTool: TaskTool | "";
  taskProjectFilter: string;
  taskStatusFilter: string;
}

const state: State = {
  auth: null,
  view: "projects",
  loading: true,
  error: "",
  notice: "",
  renderPending: false,
  system: {os: "windows", arch: "", wsl_available: false, wsl_distros: []},
  projects: [],
  workspaces: [],
  providers: [],
  models: [],
  tasks: [],
  knowledge: [],
  selectedTask: null,
  taskConversation: [],
  taskConversationHasMore: false,
  taskConversationBefore: 0,
  taskConversationLatest: 0,
  taskEventCursor: 0,
  taskCategories: {chat: true, update: true, tool: true, error: true},
  taskContext: null,
  taskRealtimeState: "connecting",
  taskDraft: "",
  taskDrafts: {},
  selectedTaskAgent: "main",
  selectedProject: null,
  dialogProjectID: "",
  taskTool: "",
  taskProjectFilter: "",
  taskStatusFilter: "",
};

const app = document.querySelector<HTMLDivElement>("#app");
let events: EventSource | null = null;
let taskRefreshTimer: number | null = null;
let taskClockFrame: number | null = null;
let taskClockSecond = -1;
let taskMonitorTimer: number | null = null;
let taskFallbackTimer: number | null = null;
let taskLastSignalAt = 0;
let scrollConversationToBottom = false;

function escapeHTML(value: unknown): string {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

function statusClass(status: string): string {
  if (["ready", "healthy", "completed", "succeeded", "verified", "waiting_user"].includes(status)) return "good";
  if (["failed", "error", "unavailable", "cancelled"].includes(status)) return "bad";
  if (["active", "running", "starting", "preparing", "queued"].includes(status)) return "info";
  return "warn";
}

function statusLabel(status: string): string {
  const labels: Record<string, string> = {
    active: "执行中",
    waiting_user: "等待输入",
    completed: "已完成",
    failed: "失败",
    preparing: "准备中",
    queued: "排队",
    starting: "启动中",
    running: "执行中",
    waiting: "等待 Agent",
    succeeded: "成功",
    interrupted: "已中断",
    candidate: "Candidate",
    verified: "Verified",
    stale: "Stale",
    ready: "Ready",
    unknown: "未检测",
  };
  return labels[status] || status;
}

function projectTypeLabel(type?: string): string {
  return type === "git" ? "Git 仓库" : "普通文件夹";
}

function formatDateTime(value?: string): string {
  if (!value) return "";
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return "";
  const pad = (n: number): string => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

function formatTokenCount(value: number): string {
  const number = Number(value || 0);
  return new Intl.NumberFormat("en-US").format(Number.isFinite(number) && number > 0 ? Math.round(number) : 0);
}

function syncProjectTypeFields(): void {
  const select = document.querySelector<HTMLSelectElement>("#project-type");
  const gitFields = document.querySelector<HTMLElement>(".git-fields");
  if (!select || !gitFields) return;
  gitFields.style.display = select.value === "git" ? "" : "none";
}

function workspaceTransportOptions(locality: string): Array<[string, string]> {
  if (locality === "remote") return [["ssh", "SSH"]];
  const options: Array<[string, string]> = [["native", "Native"]];
  if (state.system?.os === "windows" && state.system?.wsl_available) {
    options.push(["wsl", "WSL"]);
  }
  return options;
}

function populateWSLDistros(): void {
  const select = document.querySelector<HTMLSelectElement>("#ws-distro");
  if (!select) return;
  const distros = state.system?.wsl_distros || [];
  if (!distros.length) {
    select.innerHTML = `<option value="">未检测到 WSL 发行版</option>`;
    return;
  }
  const current = select.value;
  if (!distros.includes(current)) {
    select.innerHTML = distros.map(name => `<option value="${escapeHTML(name)}">${escapeHTML(name)}</option>`).join("");
    select.value = distros[0];
  }
}

function openWorkspaceDialog(ws: Workspace | null): void {
  const dialog = document.querySelector<HTMLDialogElement>("#workspace-dialog");
  if (!dialog) return;
  const form = dialog.querySelector("form");
  const editId = dialog.querySelector<HTMLInputElement>("#ws-edit-id");
  const nameInput = dialog.querySelector<HTMLInputElement>("#ws-name");
  const localitySelect = dialog.querySelector<HTMLSelectElement>("#ws-locality");
  const projectSelect = dialog.querySelector<HTMLSelectElement>('[name="project_id"]');
  const rootInput = dialog.querySelector<HTMLInputElement>("#ws-root-path");
  const sshHost = dialog.querySelector<HTMLInputElement>('[name="ssh_host"]');
  const sshUser = dialog.querySelector<HTMLInputElement>('[name="ssh_user"]');
  const sshPort = dialog.querySelector<HTMLInputElement>('[name="ssh_port"]');
  const sshAuth = dialog.querySelector<HTMLSelectElement>('[name="ssh_auth"]');
  const sshPassword = dialog.querySelector<HTMLInputElement>('[name="ssh_password"]');
  const clearSSHPassword = dialog.querySelector<HTMLInputElement>('[name="clear_ssh_password"]');
  if (editId) editId.value = ws ? ws.id : "";
  if (projectSelect && ws) projectSelect.value = ws.project_id;
  if (nameInput) nameInput.value = ws ? ws.name : "本地开发";
  if (localitySelect) localitySelect.value = ws ? (ws.locality || "local") : "local";
  syncWorkspaceFields();
  const transportSelect = dialog.querySelector<HTMLSelectElement>("#ws-transport");
  const distroSelect = dialog.querySelector<HTMLSelectElement>("#ws-distro");
  if (transportSelect) transportSelect.value = ws ? (ws.transport || "native") : "native";
  if (ws?.distro && distroSelect && [...distroSelect.options].some(option => option.value === ws.distro)) {
    distroSelect.value = ws.distro;
  }
  if (rootInput) rootInput.value = ws ? ws.root_path : "";
  if (sshHost) sshHost.value = ws?.ssh_host || "";
  if (sshUser) sshUser.value = ws?.ssh_user || "";
  if (sshPort) sshPort.value = String(ws?.ssh_port || 22);
  if (sshAuth) sshAuth.value = ws?.ssh_auth || "auto";
  if (sshPassword) {
    sshPassword.value = "";
    sshPassword.dataset.configured = ws?.ssh_password_configured ? "true" : "false";
    sshPassword.placeholder = ws?.ssh_password_configured ? "已配置，留空保持不变" : "可选";
  }
  if (clearSSHPassword) {
    clearSSHPassword.checked = false;
    clearSSHPassword.disabled = !ws?.ssh_password_configured;
  }
  syncWorkspaceFields();
  const title = dialog.querySelector(".dialog-head h2");
  if (title) title.textContent = ws ? "编辑 Workspace" : "添加 Workspace";
  dialog.showModal();
}

function openProviderDialog(provider: Provider | null): void {
  const dialog = document.querySelector<HTMLDialogElement>("#provider-dialog");
  if (!dialog) return;
  const editId = dialog.querySelector<HTMLInputElement>("#provider-edit-id");
  const preset = dialog.querySelector<HTMLSelectElement>("#provider-preset");
  const nameInput = dialog.querySelector<HTMLInputElement>("#provider-name");
  const baseInput = dialog.querySelector<HTMLInputElement>("#provider-base-url");
  const anthropicInput = dialog.querySelector<HTMLInputElement>("#provider-anthropic-url");
  const keyInput = dialog.querySelector<HTMLInputElement>("#provider-api-key");
  if (editId) editId.value = provider ? provider.id : "";
  if (!provider) {
    // create: start from the first preset for convenience
    if (preset) preset.selectedIndex = 0;
    applyProviderPreset();
    if (keyInput) keyInput.value = "";
  } else {
    if (preset) preset.value = "custom";
    if (nameInput) nameInput.value = provider.name;
    if (baseInput) baseInput.value = provider.base_url;
    if (anthropicInput) anthropicInput.value = provider.anthropic_base_url || "";
    if (keyInput) keyInput.value = "";
  }
  const title = dialog.querySelector(".dialog-head h2");
  if (title) title.textContent = provider ? "编辑 Provider" : "添加 Provider";
  dialog.showModal();
}

const EFFORT_LEVELS: Record<string, string[]> = {
  codex: ["low", "medium", "high", "xhigh"],
  claude: ["low", "medium", "high", "xhigh", "max"],
};

// backendOptionsForWorkspace derives the task's allowed backends from the
// selected workspace's detection results. Falls back to the backends that have
// configured models when the workspace has not been detected yet.
function backendOptionsForWorkspace(ws?: Workspace): string[] {
  if (ws) {
    const caps = (ws.capabilities || {}) as Record<string, {status?: string}>;
    const ready: string[] = [];
    if (caps.codex?.status === "ready") ready.push("codex");
    if (caps.claude?.status === "ready") ready.push("claude");
    if (ready.length) return ready;
    if (ws.health !== "unknown") return ready; // detected, none ready
  }
  const present = new Set(state.models.map(item => item.backend));
  return ["codex", "claude"].filter(backend => present.has(backend));
}

function refreshTaskModels(): void {
  const backend = String((document.querySelector("#task-backend") as HTMLSelectElement)?.value || "");
  const modelSelect = document.querySelector<HTMLSelectElement>("#task-model");
  if (!modelSelect) return;
  const items = backend ? state.models.filter(item => item.backend === backend) : state.models;
  modelSelect.innerHTML = items.length
    ? items.map(item => `<option value="${item.id}">${escapeHTML(item.display_name)}</option>`).join("")
    : `<option value="">${backend ? "该 Backend 下暂无模型" : "请先选择 Backend"}</option>`;
}

function syncTaskBackend(): void {
  const wsID = String((document.querySelector<HTMLSelectElement>("#task-workspace"))?.value || "");
  const ws = state.workspaces.find(item => item.id === wsID);
  const options = backendOptionsForWorkspace(ws);
  const backendSelect = document.querySelector<HTMLSelectElement>("#task-backend");
  if (!backendSelect) return;
  const labels: Record<string, string> = {codex: "Codex", claude: "Claude Code"};
  const previous = backendSelect.value;
  backendSelect.innerHTML = options.length
    ? options.map(backend => `<option value="${backend}">${labels[backend] || backend}</option>`).join("")
    : `<option value="">该 Workspace 无可用的 Backend（请先检测）</option>`;
  if (options.includes(previous)) backendSelect.value = previous;
  else if (options.length) backendSelect.value = options[0];
  else backendSelect.value = "";
  refreshTaskModels();
  syncTaskEffort();
  syncTaskGitIsolation();
}

function syncTaskEffort(): void {
  const backend = String((document.querySelector("#task-backend") as HTMLSelectElement)?.value || "codex");
  const modelID = String((document.querySelector("#task-model") as HTMLSelectElement)?.value || "");
  const model = state.models.find(item => item.id === modelID);
  const levels = EFFORT_LEVELS[backend] || EFFORT_LEVELS.codex;
  const effortSelect = document.querySelector<HTMLSelectElement>("#task-effort");
  if (!effortSelect) return;
  const previous = effortSelect.value;
  const desired = model?.default_reasoning_effort || "";
  effortSelect.innerHTML = levels.map(level => `<option value="${level}">${level}</option>`).join("");
  effortSelect.value = levels.includes(desired) ? desired : (levels.includes(previous) ? previous : "medium");
}

function defaultTaskWorktreeDir(workspace: Workspace | undefined): string {
  const root = String(workspace?.root_path || "").replace(/[\\/]+$/, "");
  if (!root) return "";
  const separator = root.includes("\\") && !root.includes("/") ? "\\" : "/";
  const lastSeparator = Math.max(root.lastIndexOf("/"), root.lastIndexOf("\\"));
  const parent = lastSeparator >= 0 ? root.slice(0, lastSeparator) : root;
  return `${parent}${separator}.aha2-worktrees`;
}

function syncTaskGitIsolation(): void {
  const projectID = String((document.querySelector("#task-project") as HTMLSelectElement)?.value || "");
  const project = state.projects.find(item => item.id === projectID);
  const workspaceID = String((document.querySelector("#task-workspace") as HTMLSelectElement)?.value || "");
  const workspace = state.workspaces.find(item => item.id === workspaceID);
  const section = document.querySelector<HTMLElement>("#task-git-isolation");
  const isolation = document.querySelector<HTMLSelectElement>("#task-isolation");
  const worktreeSettings = document.querySelector<HTMLElement>("#task-worktree-settings");
  const worktreeDir = document.querySelector<HTMLInputElement>("#task-worktree-dir");
  const branches = document.querySelector<HTMLElement>("#task-branches");
  if (!section || !isolation || !worktreeSettings || !worktreeDir || !branches) return;
  const isGit = project?.project_type === "git";
  section.style.display = isGit ? "" : "none";
  if (!isGit) isolation.value = "inplace";
  const useWorktree = isGit && isolation.value === "worktree";
  worktreeSettings.style.display = useWorktree ? "" : "none";
  branches.style.display = useWorktree ? "" : "none";
  if (useWorktree && !worktreeDir.value) worktreeDir.value = defaultTaskWorktreeDir(workspace);
  if (!useWorktree) worktreeDir.value = "";
}

function syncTaskWorkspaces(): void {
  const projectID = String((document.querySelector("#task-project") as HTMLSelectElement)?.value || "");
  const wsSelect = document.querySelector<HTMLSelectElement>("#task-workspace");
  if (!wsSelect) return;
  const items = state.workspaces.filter(item => item.project_id === projectID);
  const current = wsSelect.value;
  wsSelect.innerHTML = items.length
    ? items.map(item => `<option value="${item.id}">${escapeHTML(item.name)}</option>`).join("")
    : `<option value="">该项目下暂无 Workspace</option>`;
  if (items.some(item => item.id === current)) wsSelect.value = current;
  else if (items.length) wsSelect.value = items[0].id;
  else wsSelect.value = "";
  const project = state.projects.find(item => item.id === projectID);
  const isolation = document.querySelector<HTMLSelectElement>("#task-isolation");
  if (isolation) isolation.value = project?.project_type === "git" ? "worktree" : "inplace";
  const worktreeDir = document.querySelector<HTMLInputElement>("#task-worktree-dir");
  if (worktreeDir) worktreeDir.value = "";
  syncTaskBackend();
}

function openProjectDialog(project: Project | null): void {
  const dialog = document.querySelector<HTMLDialogElement>("#project-dialog");
  if (!dialog) return;
  const editId = dialog.querySelector<HTMLInputElement>("#project-edit-id");
  const nameInput = dialog.querySelector<HTMLInputElement>("#project-name");
  const descInput = dialog.querySelector<HTMLTextAreaElement>("#project-desc");
  const typeSelect = dialog.querySelector<HTMLSelectElement>("#project-type");
  const repoInput = dialog.querySelector<HTMLInputElement>("#project-repo");
  const branchInput = dialog.querySelector<HTMLInputElement>("#project-branch");
  if (editId) editId.value = project ? project.id : "";
  if (nameInput) nameInput.value = project ? project.name : "";
  if (descInput) descInput.value = project ? (project.description || "") : "";
  if (typeSelect) typeSelect.value = project ? (project.project_type || "folder") : "folder";
  if (repoInput) repoInput.value = project?.repository_identity || "";
  if (branchInput) branchInput.value = project?.default_branch || "main";
  syncProjectTypeFields();
  const title = dialog.querySelector(".dialog-head h2");
  if (title) title.textContent = project ? "编辑项目" : "新建项目";
  dialog.showModal();
}

function syncWorkspaceFields(): void {
  const localitySelect = document.querySelector<HTMLSelectElement>("#ws-locality");
  const transportSelect = document.querySelector<HTMLSelectElement>("#ws-transport");
  const rootInput = document.querySelector<HTMLInputElement>("#ws-root-path");
  const sshFields = document.querySelector<HTMLElement>(".ssh-fields");
  const wslFields = document.querySelector<HTMLElement>(".wsl-fields");
  const sshHost = document.querySelector<HTMLInputElement>('[name="ssh_host"]');
  const sshUser = document.querySelector<HTMLInputElement>('[name="ssh_user"]');
  const sshAuth = document.querySelector<HTMLSelectElement>('[name="ssh_auth"]');
  const sshPassword = document.querySelector<HTMLInputElement>('[name="ssh_password"]');
  const clearSSHPassword = document.querySelector<HTMLInputElement>('[name="clear_ssh_password"]');
  if (!localitySelect || !transportSelect) return;
  const locality = localitySelect.value;
  const allowed = workspaceTransportOptions(locality);
  if (!allowed.some(([value]) => value === transportSelect.value)) {
    transportSelect.innerHTML = allowed.map(([value, label]) => `<option value="${value}">${label}</option>`).join("");
    transportSelect.value = allowed[0][0];
  }
  const transport = transportSelect.value;
  if (sshFields) sshFields.style.display = transport === "ssh" ? "" : "none";
  if (wslFields) wslFields.style.display = transport === "wsl" ? "" : "none";
  const configured = sshPassword?.dataset.configured === "true";
  const clearPassword = Boolean(clearSSHPassword?.checked);
  if (sshHost) sshHost.required = transport === "ssh";
  if (sshUser) sshUser.required = transport === "ssh";
  if (sshPassword) {
    sshPassword.disabled = transport !== "ssh" || clearPassword;
    sshPassword.required = transport === "ssh" && sshAuth?.value === "password" && !configured;
  }
  if (sshAuth) {
    sshAuth.disabled = transport !== "ssh";
    sshAuth.setCustomValidity(
      transport === "ssh" && sshAuth.value === "password" && clearPassword
        ? "密码认证不能同时清除已保存密码"
        : "",
    );
  }
  if (transport === "wsl") populateWSLDistros();
  if (rootInput) rootInput.placeholder = transport === "wsl" ? "/home/user/project（WSL 内路径）" : transport === "ssh" ? "/workspace 或远程路径" : "E:\\project 或本机路径";
}

const PROVIDER_PRESETS: Record<string, {name: string; base_url: string; anthropic_base_url?: string}> = {
  deepseek: {name: "DeepSeek", base_url: "https://api.deepseek.com/v1", anthropic_base_url: "https://api.deepseek.com/anthropic"},
  glm: {name: "智谱 GLM", base_url: "https://open.bigmodel.cn/api/paas/v4"},
  openai: {name: "OpenAI", base_url: "https://api.openai.com/v1"},
  anthropic: {name: "Anthropic", base_url: "https://api.anthropic.com"},
  openrouter: {name: "OpenRouter", base_url: "https://openrouter.ai/api/v1"},
  moonshot: {name: "Moonshot Kimi", base_url: "https://api.moonshot.cn/v1", anthropic_base_url: "https://api.moonshot.cn/anthropic"},
  siliconflow: {name: "SiliconFlow", base_url: "https://api.siliconflow.cn/v1"},
  ollama: {name: "Ollama 本地", base_url: "http://localhost:11434/v1"},
  custom: {name: "", base_url: ""},
};

function workspaceBackendInfo(ws: Workspace): string {
  const caps = (ws.capabilities || {}) as Record<string, {status?: string; version?: string}>;
  const parts: string[] = [];
  if (caps.codex?.status === "ready") parts.push(`<span class="proto codex" title="Codex ${caps.codex.version || ""}">Codex</span>`);
  if (caps.claude?.status === "ready") parts.push(`<span class="proto claude" title="Claude Code ${caps.claude.version || ""}">Claude</span>`);
  if (parts.length) return `<span class="proto-badges">${parts.join("")}</span>`;
  if (ws.health === "unknown") return `<span class="status warn">未检测</span>`;
  return `<span class="status warn">无可用 Backend</span>`;
}

function backendProtocolLabel(model: Model): string {
  const wire = model.wire_api || (model.backend === "claude" ? "anthropic_messages" : "responses");
  const protocol = wire === "anthropic_messages" ? "messages" : wire === "chat_completions" ? "chat" : wire;
  return `${model.backend}.${protocol}`;
}

function supportedWireAPIs(model: DetectedModel): string[] {
  const caps = model.capabilities || {};
  const order = ["responses", "chat_completions", "anthropic_messages"] as const;
  // rate_limited means the endpoint exists and accepted the request but the
  // gateway throttled the burst probe — the protocol is still usable.
  return order.filter(api => caps[api] === "supported" || caps[api] === "rate_limited").map(api => api === "chat_completions" ? "chat" : api);
}

function protoBadges(model: DetectedModel): string {
  const wire = supportedWireAPIs(model);
  const parts: string[] = [];
  if (wire.includes("responses") || wire.includes("chat")) parts.push(`<span class="proto codex">Codex</span>`);
  if (wire.includes("anthropic_messages")) parts.push(`<span class="proto claude">Claude</span>`);
  return parts.length ? parts.join("") : `<span class="proto none">不支持</span>`;
}

function protoChecks(model: DetectedModel): string {
  const wire = supportedWireAPIs(model);
  if (!wire.length) {
    const caps = model.capabilities || {};
    const reasons = Object.entries(caps).map(([key, value]) => `${key.replace("_", " ")}=${value}`).join(" · ");
    return `<span class="proto none" title="${escapeHTML(reasons)}">${reasons ? "无支持协议" : "无可用协议"}</span>`;
  }
  const labels: Record<string, string> = {
    responses: "Codex · Responses",
    chat: "Codex · Chat",
    anthropic_messages: "Claude · Messages",
  };
  return `<span class="proto-checks">${wire.map(api => `<label><input type="checkbox" value="${api}" checked><span>${labels[api] || api}</span></label>`).join("")}</span>`;
}

function applyProviderPreset(): void {
  const key = String((document.querySelector("#provider-preset") as HTMLSelectElement)?.value || "custom");
  const preset = PROVIDER_PRESETS[key] || PROVIDER_PRESETS.custom;
  const nameInput = document.querySelector<HTMLInputElement>("#provider-name");
  const baseInput = document.querySelector<HTMLInputElement>("#provider-base-url");
  const anthropicInput = document.querySelector<HTMLInputElement>("#provider-anthropic-url");
  if (nameInput) nameInput.value = preset.name;
  if (baseInput) baseInput.value = preset.base_url;
  if (anthropicInput) anthropicInput.value = preset.anthropic_base_url || "";
}

function setMessage(type: "error" | "notice", value: string): void {
  state[type] = value;
  window.setTimeout(() => {
    if (state[type] === value) {
      state[type] = "";
      render();
    }
  }, 5000);
}

async function runWithFeedback(button: HTMLElement | null, label: string, action: () => Promise<void>): Promise<void> {
  if (!button) {
    await action();
    return;
  }
  const original = button.innerHTML;
  const wasDisabled = button.disabled;
  button.disabled = true;
  button.innerHTML = `${icon("spinner", true)}<span>${label}</span>`;
  try {
    await action();
  } catch (error) {
    setMessage("error", error instanceof Error ? error.message : String(error));
    render();
  } finally {
    button.disabled = wasDisabled;
    button.innerHTML = original;
  }
}

async function bootstrap(): Promise<void> {
  try {
    state.auth = await api.authStatus();
    api.setCSRF(state.auth.csrf_token);
    if (state.auth.authenticated) {
      await loadAll();
      await restoreNavigationState();
      openGlobalEvents();
    }
  } catch (error) {
    state.error = error instanceof Error ? error.message : String(error);
  } finally {
    state.loading = false;
    render();
  }
}

async function loadAll(): Promise<void> {
  const [projects, workspaces, providers, models, tasks, knowledge, system] = await Promise.all([
    api.projects(), api.workspaces(), api.providers(), api.models(), api.tasks(), api.knowledge(), api.system(),
  ]);
  state.projects = projects.projects || [];
  state.workspaces = workspaces.workspaces || [];
  state.providers = providers.providers || [];
  state.models = models.models || [];
  state.tasks = tasks.tasks || [];
  state.knowledge = knowledge.knowledge || [];
  if (system?.system) state.system = system.system;
  await loadPromptCatalog();
}
function persistNavigationState(): void {
  saveNavigationSnapshot({
    view: state.view, projectID: state.selectedProject?.id, taskID: state.selectedTask?.task.id,
    agentID: state.selectedTaskAgent, draft: state.taskDraft, drafts: state.taskDrafts,
  });
}
async function restoreNavigationState(): Promise<void> {
  const saved = loadNavigationSnapshot();
  if (["projects", "models", "tasks", "knowledge", "prompts"].includes(saved.view || "")) state.view = saved.view as View;
  state.selectedProject = state.projects.find(project => project.id === saved.projectID) || null;
  if (!saved.taskID || !state.tasks.some(task => task.id === saved.taskID)) return;
  state.view = "tasks"; state.selectedProject = null;
  await openTask(saved.taskID);
  if (saved.agentID && saved.agentID !== "main" && state.selectedTask?.agents.some(agent => agent.agent_id === saved.agentID)) {
    await selectTaskAgent(saved.agentID);
  }
  state.taskDrafts = saved.drafts || {};
  state.taskDraft = saved.draft || state.taskDrafts[state.selectedTaskAgent] || "";
}
function syncVisualViewportHeight(): void {
  const height = Math.round(window.visualViewport?.height || window.innerHeight);
  document.documentElement.style.setProperty("--visual-viewport-height", `${height}px`);
}

function selectedConversationCategories(): ConversationCategory[] {
  return (Object.keys(state.taskCategories) as ConversationCategory[]).filter(category => state.taskCategories[category]);
}

async function openTask(taskID: string): Promise<void> {
  const [detail, page] = await Promise.all([
    api.task(taskID),
    api.agentConversation(taskID, "main", {limit: 50, categories: selectedConversationCategories()}),
  ]);
  detail.agents ||= [];
  state.selectedTask = detail;
  state.selectedTaskAgent = "main";
  state.taskConversation = page.conversation.items || [];
  state.taskConversationHasMore = page.conversation.has_more;
  state.taskConversationBefore = page.conversation.next_before || 0;
  state.taskConversationLatest = page.conversation.latest_sequence || 0;
  state.taskEventCursor = detail.event_cursor || 0;
  state.taskContext = null;
  state.taskTool = "";
  state.taskDraft = "";
  state.taskDrafts = {};
  scrollConversationToBottom = true;
  openEvents(taskID, state.taskEventCursor);
}

async function selectTaskAgent(agentID: string): Promise<void> {
  if (!state.selectedTask || agentID === state.selectedTaskAgent) return;
  state.taskDrafts[state.selectedTaskAgent] = state.taskDraft;
  state.selectedTaskAgent = agentID;
  state.taskDraft = state.taskDrafts[agentID] || "";
  const page = await api.agentConversation(state.selectedTask.task.id, agentID, {
    limit: 50,
    categories: selectedConversationCategories(),
  });
  state.taskConversation = page.conversation.items || [];
  state.taskConversationHasMore = page.conversation.has_more;
  state.taskConversationBefore = page.conversation.next_before || 0;
  state.taskConversationLatest = page.conversation.latest_sequence || 0;
  const agent = state.selectedTask.agents.find(item => item.agent_id === agentID);
  if (agent) agent.unread_count = 0;
  state.taskContext = state.taskTool === "context"
    ? await api.agentContext(state.selectedTask.task.id, agentID)
    : null;
  scrollConversationToBottom = true;
}

function syncAgentConfigFields(): void {
  const backend = String((document.querySelector<HTMLSelectElement>("#agent-config-backend"))?.value || "codex");
  const modelSelect = document.querySelector<HTMLSelectElement>("#agent-config-model");
  if (modelSelect) {
    const previous = modelSelect.value;
    const models = state.models.filter(model => model.backend === backend);
    modelSelect.innerHTML = models.map(model =>
      `<option value="${model.id}" ${model.id === previous ? "selected" : ""}>${escapeHTML(model.display_name)}</option>`
    ).join("") || `<option value="">该 Backend 暂无模型</option>`;
    if (!models.some(model => model.id === previous) && models.length) modelSelect.value = models[0].id;
  }
  const effortSelect = document.querySelector<HTMLSelectElement>("#agent-config-effort");
  if (effortSelect) {
    const previous = effortSelect.value;
    const levels = EFFORT_LEVELS[backend] || EFFORT_LEVELS.codex;
    effortSelect.innerHTML = levels.map(level => `<option value="${level}">${level}</option>`).join("");
    effortSelect.value = levels.includes(previous) ? previous : "medium";
  }
  const inherited = Boolean(document.querySelector<HTMLInputElement>('[name="inherit_main"]')?.checked);
  document.querySelectorAll<HTMLElement>(".agent-runtime-fields").forEach(element => {
    element.classList.toggle("disabled", inherited);
    element.querySelectorAll<HTMLInputElement | HTMLSelectElement>("input,select").forEach(input => {
      input.disabled = inherited;
    });
  });
}

function bindTaskAgentControls(): void {
  document.querySelectorAll<HTMLElement>("[data-agent-select]").forEach(button => button.addEventListener("click", async () => {
    await selectTaskAgent(button.dataset.agentSelect || "main");
    render();
  }));
  document.querySelectorAll<HTMLElement>("[data-agent-config]").forEach(button => button.addEventListener("click", async () => {
    await selectTaskAgent(button.dataset.agentConfig || "main");
    render();
    const dialog = document.querySelector<HTMLDialogElement>("#agent-config-dialog");
    dialog?.showModal();
    syncAgentConfigFields();
  }));
}

async function reloadConversation(): Promise<void> {
  if (!state.selectedTask) return;
  const page = await api.agentConversation(state.selectedTask.task.id, state.selectedTaskAgent, {
    limit: 50,
    categories: selectedConversationCategories(),
  });
  state.taskConversation = page.conversation.items || [];
  state.taskConversationHasMore = page.conversation.has_more;
  state.taskConversationBefore = page.conversation.next_before || 0;
  state.taskConversationLatest = page.conversation.latest_sequence || 0;
}

function taskRuntimeSignature(detail: TaskDetail | null): string {
  if (!detail) return "";
  return JSON.stringify({
    task: [detail.task.status, detail.task.updated_at],
    round: detail.latest_round ? [
      detail.latest_round.id, detail.latest_round.status, detail.latest_round.started_at, detail.latest_round.finished_at,
    ] : null,
    turns: (detail.turns || []).map(turn => [
      turn.id, turn.status, turn.attempt, turn.generation, turn.started_at, turn.finished_at,
      turn.context_window, turn.prompt_chars, turn.backend_session_id, turn.usage, turn.error,
    ]),
    agents: (detail.agents || []).map(agent => [
      agent.agent_id, agent.status, agent.runtime_config_snapshot_id, agent.unread_count, agent.updated_at,
    ]),
    memory: detail.memory,
  });
}

async function refreshTaskRuntime(taskID: string): Promise<boolean> {
  if (state.selectedTask?.task.id !== taskID) return false;
  const previousSignature = taskRuntimeSignature(state.selectedTask);
  const previousContext = state.taskContext ? JSON.stringify(state.taskContext) : "";
  const [detail, page] = await Promise.all([
    api.task(taskID),
    api.agentConversation(taskID, state.selectedTaskAgent, {
      after: state.taskConversationLatest,
      limit: 100,
      categories: selectedConversationCategories(),
    }),
  ]);
  detail.agents ||= [];
  state.selectedTask = detail;
  let changed = taskRuntimeSignature(detail) !== previousSignature;
  state.taskEventCursor = Math.max(state.taskEventCursor, detail.event_cursor || 0);
  if (page.conversation.items?.length) {
    const seen = new Set(state.taskConversation.map(item => item.sequence));
    const appended = page.conversation.items.filter(item => !seen.has(item.sequence));
    if (appended.length) {
      state.taskConversation.push(...appended);
      changed = true;
    }
    if (state.taskConversation.length > 300) {
      state.taskConversation = state.taskConversation.slice(-300);
      state.taskConversationHasMore = true;
      state.taskConversationBefore = state.taskConversation[0]?.sequence || 0;
    }
  }
  state.taskConversationLatest = Math.max(state.taskConversationLatest, page.conversation.latest_sequence || 0);
  const selectedAgent = detail.agents.find(agent => agent.agent_id === state.selectedTaskAgent);
  if (selectedAgent) selectedAgent.unread_count = 0;
  if (state.taskTool === "context" && state.taskContext) {
    state.taskContext = await api.agentContext(taskID, state.selectedTaskAgent);
    changed ||= JSON.stringify(state.taskContext) !== previousContext;
  }
  return changed;
}

async function refresh(): Promise<void> {
  try {
    await loadAll();
    if (state.selectedTask) await refreshTaskRuntime(state.selectedTask.task.id);
  } catch (error) {
    setMessage("error", error instanceof Error ? error.message : String(error));
  }
  render();
}

function loginView(): string {
  const register = state.auth?.registration_open;
  return `<div class="login-layout">
    <aside class="login-brand">
      <div class="brand-lockup"><span class="brand-mark">A</span><strong>AHA</strong></div>
      <p>以项目为基础，以任务为核心，通过知识闭环持续成长的个人 AI 工作流。</p>
      <div class="secure-note">${icon("shield")}<span>公网访问 · 所有业务操作均需认证</span></div>
    </aside>
    <main class="login-main">
      <form id="auth-form" class="auth-form">
        <h1>${register ? "初始化 Owner" : "登录"}</h1>
        <p>${register ? "创建唯一 Owner 后将关闭公开注册" : "进入项目、任务与知识工作区"}</p>
        ${register ? `<label>Setup Token<input name="setup_token" autocomplete="one-time-code" required></label>` : ""}
        <label>账号<input name="username" value="owner" autocomplete="username" required></label>
        <label>密码<input name="password" type="password" autocomplete="${register ? "new-password" : "current-password"}" minlength="10" required></label>
        <button class="primary full" type="submit">${register ? "创建 Owner" : "登录"}</button>
        ${state.error ? `<div class="form-error">${escapeHTML(state.error)}</div>` : ""}
      </form>
    </main>
  </div>`;
}

function shell(content: string): string {
  const nav = [
    ["projects", "projects", "项目"],
    ["tasks", "tasks", "任务"],
    ["knowledge", "knowledge", "知识库"],
    ["models", "model", "模型"],
    ["prompts", "bot", "提示词"],
  ] as const;
  return `<div class="app-shell">
    <aside class="sidebar">
      <div class="brand-lockup"><span class="brand-mark">A</span><div><strong>AHA</strong><small>个人 AI 工作流</small></div></div>
      <nav>${nav.map(([view, glyph, label]) => `<button data-view="${view}" class="${state.view === view ? "active" : ""}">${icon(glyph)}<span>${label}</span></button>`).join("")}</nav>
      <div class="owner-block"><span class="avatar">O</span><div><strong>${escapeHTML(state.auth?.username || "Owner")}</strong><small>已安全登录</small></div><button id="logout" class="icon-button" title="退出">${icon("logout")}</button></div>
    </aside>
    <header class="mobile-header"><div class="brand-lockup"><span class="brand-mark">A</span><strong>AHA</strong></div><button id="mobile-context" class="icon-button">${icon("menu")}</button></header>
    <main class="workspace">${banner()}${content}</main>
    <nav class="bottom-nav">${nav.map(([view, glyph, label]) => `<button data-view="${view}" class="${state.view === view ? "active" : ""}">${icon(glyph)}<span>${label}</span></button>`).join("")}</nav>
  </div>`;
}

function banner(): string {
  return `${state.error ? `<div class="banner error">${escapeHTML(state.error)}</div>` : ""}${state.notice ? `<div class="banner success">${escapeHTML(state.notice)}</div>` : ""}`;
}

function pageHead(title: string, description: string, actions = ""): string {
  return `<header class="page-head"><div><h1>${title}</h1><p>${description}</p></div><div class="actions">${actions}</div></header>`;
}

function projectsView(): string {
  const rows = state.projects.map(project => {
    const workspaces = state.workspaces.filter(item => item.project_id === project.id);
    const tasks = state.tasks.filter(item => item.project_id === project.id);
    return `<article class="list-row project-row" data-project="${project.id}">
      <div class="item-title"><span class="square-icon">${icon("projects")}</span><div><strong>${escapeHTML(project.name)}</strong><small>${projectTypeLabel(project.project_type)}</small></div><span class="row-actions"><button type="button" data-edit-project="${project.id}" class="icon-button" title="编辑项目">${icon("edit")}</button><button type="button" data-delete-project="${project.id}" class="icon-button project-del" title="删除项目">${icon("close")}</button></span></div>
      <div class="chips">${workspaces.map(item => `<span>${item.locality === "remote" ? icon("server") : icon("monitor")}${escapeHTML(item.name)}</span>`).join("") || "<span>暂无 Workspace</span>"}</div>
      <div><strong>${tasks.length}</strong><small>任务</small></div>
      <div><strong>${workspaces.filter(item => item.health === "ready").length}/${workspaces.length}</strong><small>Workspace Ready</small></div>
    </article>`;
  }).join("");
  return shell(`<section class="page">
    ${pageHead("项目", "管理逻辑 Project，点开项目配置其 Workspace。", `<button data-dialog="project">${icon("plus")}新建项目</button>`)}
    <div class="metrics"><div><small>项目</small><strong>${state.projects.length}</strong></div><div><small>Workspace</small><strong>${state.workspaces.length}</strong></div><div><small>活动任务</small><strong>${state.tasks.filter(item => item.status === "active").length}</strong></div><div><small>知识</small><strong>${state.knowledge.length}</strong></div></div>
    <div class="panel"><div class="panel-head"><strong>所有项目</strong><button id="refresh">${icon("refresh")}刷新</button></div>${rows || `<div class="empty">创建第一个项目，随后点开配置 Workspace。</div>`}</div>
    ${projectDialog()}
  </section>`);
}

function projectDetailView(project: Project): string {
  const workspaces = state.workspaces.filter(item => item.project_id === project.id);
  const tasks = state.tasks.filter(item => item.project_id === project.id);
  const rows = workspaces.map(item => `<article class="list-row ws-row">
    <div class="item-title">${item.locality === "remote" ? icon("server") : icon("monitor")}<div><strong>${escapeHTML(item.name)}</strong><small>${escapeHTML(item.root_path)}</small></div></div>
    <div class="ws-meta"><small>${escapeHTML(item.transport)}${item.distro ? ` · ${escapeHTML(item.distro)}` : ""}</small><strong>${escapeHTML(item.platform || "-")}</strong></div>
    ${workspaceBackendInfo(item)}
    <button data-detect="${item.id}">${icon("refresh")}检测</button>
    <span class="row-actions"><button type="button" data-edit-workspace="${item.id}" class="icon-button" title="编辑 Workspace">${icon("edit")}</button><button type="button" data-delete-workspace="${item.id}" class="icon-button" title="删除 Workspace">${icon("close")}</button></span>
  </article>`).join("");
  return shell(`<section class="page">
    <header class="page-head"><div><button id="back-projects" class="back-link">← 返回项目列表</button><h1>${escapeHTML(project.name)}</h1><p>${projectTypeLabel(project.project_type)}${project.repository_identity ? ` · ${escapeHTML(project.repository_identity)}` : ""}${project.default_branch ? ` · 默认分支 ${escapeHTML(project.default_branch)}` : ""}</p></div><div class="actions"><button data-dialog="workspace">${icon("plus")}添加 Workspace</button><button type="button" data-edit-project-detail="${project.id}" class="icon-button" title="编辑项目">${icon("edit")}</button><button id="delete-project" class="danger">${icon("close")}删除项目</button></div></header>
    <div class="metrics"><div><small>Workspace</small><strong>${workspaces.length}</strong></div><div><small>任务</small><strong>${tasks.length}</strong></div><div><small>Ready</small><strong>${workspaces.filter(item => item.health === "ready").length}</strong></div></div>
    <div class="panel"><div class="panel-head"><strong>Workspaces</strong><span>点击「检测」刷新 Backend 能力</span></div>${rows || `<div class="empty">尚无 Workspace，点击右上角添加。</div>`}</div>
    ${workspaceDialog()}
  </section>`);
}

function projectDialog(): string {
  return `<dialog id="project-dialog"><form id="project-form" method="dialog"><div class="dialog-head"><h2>新建项目</h2><button type="button" data-close class="icon-button">${icon("close")}</button></div><input type="hidden" id="project-edit-id" name="project_edit_id" value=""><label>名称<input name="name" id="project-name" required></label><label>描述<textarea name="description" id="project-desc"></textarea></label><label>项目类型<select name="project_type" id="project-type"><option value="folder">普通文件夹</option><option value="git">Git 仓库</option></select></label><div class="git-fields" style="display:none"><label>Git Repository Identity<input name="repository_identity" id="project-repo" placeholder="可选：remote URL"></label><label>默认分支<input name="default_branch" id="project-branch" value="main"></label></div><div class="dialog-actions"><button type="button" data-close>取消</button><button class="primary" value="default">创建项目</button></div></form></dialog>`;
}

function workspaceDialog(): string {
  return `<dialog id="workspace-dialog"><form id="workspace-form" method="dialog"><div class="dialog-head"><h2>添加 Workspace</h2><button type="button" data-close class="icon-button">${icon("close")}</button></div><input type="hidden" id="ws-edit-id" name="ws_edit_id" value=""><label>项目<select name="project_id" id="ws-project">${state.projects.map(item => `<option value="${item.id}" ${item.id === state.dialogProjectID ? "selected" : ""}>${escapeHTML(item.name)}</option>`).join("")}</select></label><label>名称<input name="name" id="ws-name" value="本地开发" required></label><div class="two"><label>位置<select name="locality" id="ws-locality"><option value="local">本地</option><option value="remote">远程</option></select></label><label>Transport<select name="transport" id="ws-transport"></select></label></div><label>Root Path<input name="root_path" id="ws-root-path" placeholder="E:\project 或 /home/user/project" required></label><div class="wsl-fields" style="display:none"><label>WSL Distro<select name="distro" id="ws-distro"></select></label><div class="field-help">Root Path 填 WSL 内的路径，如 /home/user/project</div></div><div class="ssh-fields" style="display:none"><div class="two"><label>SSH Host<input name="ssh_host" placeholder="192.168.1.10"></label><label>SSH User<input name="ssh_user" placeholder="root"></label></div><div class="two"><label>SSH Port<input name="ssh_port" type="number" min="1" max="65535" value="22"></label><label>SSH 登录方式<select name="ssh_auth"><option value="auto">自动（有密码时优先密码）</option><option value="password">密码</option><option value="key">Key (~/.ssh)</option></select></label></div><label>登录密码<input name="ssh_password" type="password" autocomplete="new-password" placeholder="可选"></label><label class="workspace-clear-secret"><input name="clear_ssh_password" type="checkbox">清除已保存密码</label><div class="field-help">密码保存在 Secret Store；编辑时留空会保留原密码。</div></div><div class="dialog-actions"><button type="button" data-close>取消</button><button class="primary" value="default">添加 Workspace</button></div></form></dialog>`;
}

function modelsView(): string {
  const providers = state.providers.map(item => `<article class="list-row provider-row">
    <div class="item-title">${icon("server")}<div><strong>${escapeHTML(item.name)}</strong><small>${escapeHTML(item.base_url || item.id)}</small></div></div>
    <span class="status ${item.credential_configured ? "good" : "warn"}">${item.credential_configured ? "Key Ready" : "No Key"}</span>
    <span class="row-actions"><button type="button" data-edit-provider="${item.id}" class="icon-button" title="编辑 Provider">${icon("edit")}</button><button type="button" data-delete-provider="${item.id}" class="icon-button" title="删除 Provider">${icon("close")}</button></span>
  </article>`).join("");
  const models = state.models.map(item => `<tr><td><strong>${escapeHTML(item.display_name)}</strong><small>${escapeHTML(item.wire_model)}</small></td><td>${escapeHTML(item.provider_id)}</td><td>${escapeHTML(backendProtocolLabel(item))}</td><td>${item.context_window ? Math.round(item.context_window / 1000) + "K" : "-"}</td><td class="row-actions"><button type="button" data-edit-model="${item.id}" class="icon-button" title="编辑模型">${icon("edit")}</button><button type="button" data-delete-model="${item.id}" class="icon-button" title="删除模型">${icon("close")}</button></td></tr>`).join("");
  return shell(`<section class="page">
    ${pageHead("模型", "左侧配置 Provider（API Base URL + API Key），右侧选择 Provider 检测并添加模型。Env Group 由系统自动生成。")}
    <div class="provider-layout">
      <section class="panel"><div class="panel-head"><strong>Providers</strong><button data-dialog="provider">${icon("plus")}添加 Provider</button></div>${providers || `<div class="empty">先添加 Provider（API Base URL + API Key）。</div>`}</section>
      <section class="panel"><div class="panel-head"><strong>Models</strong><button data-dialog="model">${icon("plus")}添加模型</button></div><div class="table-wrap"><table><thead><tr><th>模型</th><th>Provider</th><th>Backend</th><th>Context</th><th></th></tr></thead><tbody>${models || `<tr><td colspan="5">选择 Provider 后添加模型。</td></tr>`}</tbody></table></div></section>
    </div>
    ${providerDialog()}${modelDialog()}${editModelDialog()}
  </section>`);
}

function providerDialog(): string {
  const presets = Object.entries(PROVIDER_PRESETS)
    .filter(([key]) => key !== "custom")
    .map(([key, preset]) => `<option value="${key}">${escapeHTML(preset.name)}</option>`)
    .join("");
  return `<dialog id="provider-dialog"><div class="dialog-head"><h2>添加 Provider</h2><button type="button" data-close class="icon-button">${icon("close")}</button></div><div class="dialog-body"><input type="hidden" id="provider-edit-id" name="provider_edit_id" value=""><label>常用供应商<select id="provider-preset">${presets}<option value="custom">自定义</option></select></label><label>名称<input id="provider-name" placeholder="网关名称" required></label><label>API Base URL（OpenAI 风格）<input id="provider-base-url" placeholder="https://api.example.com/v1" required></label><label>Anthropic Base URL（可选，仅当与上方不同）<input id="provider-anthropic-url" placeholder="https://api.example.com/anthropic"></label><label>API Key<input id="provider-api-key" type="password" placeholder="留空则保持不变"></label><div class="dialog-actions"><button type="button" id="save-provider" class="primary">保存 Provider</button></div></div></dialog>`;
}

function modelDialog(): string {
  const options = state.providers.map(item => `<option value="${item.id}">${escapeHTML(item.name)}</option>`).join("");
  return `<dialog id="model-dialog" class="wide"><div class="dialog-head"><h2>添加模型</h2><button type="button" data-close class="icon-button">${icon("close")}</button></div><div class="dialog-body"><label>Provider<select id="model-provider">${options || `<option value="">先添加 Provider</option>`}</select></label><div class="dialog-actions"><button type="button" id="detect-models" class="primary">${icon("refresh")}检测模型</button></div><div id="model-detect-results"></div></div></dialog>`;
}

function editModelDialog(): string {
  return `<dialog id="model-edit-dialog"><div class="dialog-head"><h2>编辑模型</h2><button type="button" data-close class="icon-button">${icon("close")}</button></div><div class="dialog-body"><label>显示名称<input id="model-edit-name"></label><div class="two"><label>协议 / Backend<select id="model-edit-wire"><option value="responses">Codex · Responses</option><option value="chat">Codex · Chat Completions</option><option value="anthropic_messages">Claude Code · Messages</option></select></label><label>默认推理强度<select id="model-edit-effort"><option value="">继承默认</option><option>high</option><option>medium</option><option>low</option></select></label></div><div class="two"><label>Context Window<input id="model-edit-context" type="number" value="0"></label><label>Max Output Tokens<input id="model-edit-maxout" type="number" value="0"></label></div><div class="dialog-actions"><button type="button" data-close>取消</button><button type="button" id="save-model-edit" class="primary">保存</button></div></div></dialog>`;
}

const TASK_STATUS_FILTERS = ["active", "waiting_user", "preparing", "queued", "running", "completed", "failed", "blocked"];

function taskCardHtml(task: Task): string {
  const project = state.projects.find(item => item.id === task.project_id);
  const ws = state.workspaces.find(item => item.id === task.workspace_id);
  return `<article class="task-card" data-task="${task.id}">
    <div class="task-card-main">
      <div class="task-card-title"><span class="task-code">${escapeHTML(task.code || "")}</span><h3 data-task-title="${task.id}">${escapeHTML(task.title)}</h3><span class="status ${statusClass(task.status)}">${statusLabel(task.status)}</span></div>
      ${task.current_goal && task.current_goal !== task.title ? `<p class="task-goal">${escapeHTML(task.current_goal)}</p>` : ""}
      <div class="task-meta">
        <span title="所属项目">${icon("projects")}<span>${escapeHTML(project?.name || task.project_id)}</span></span>
        <span title="工作目录">${icon("monitor")}<span>${escapeHTML(ws?.root_path || "-")}</span></span>
        ${task.task_branch ? `<span title="任务分支">${icon("branch")}<span>${escapeHTML(task.task_branch)}</span></span>` : ""}
        <span title="Main 与 Sub Agent 累计 Token">${icon("model")}<span>${formatTokenCount(task.total_tokens)} tokens</span></span>
        ${task.created_at ? `<span title="创建时间">${icon("clock")}<span>${formatDateTime(task.created_at)}</span></span>` : ""}
      </div>
    </div>
    <div class="task-card-actions">
      <button type="button" data-edit-task-title="${task.id}" class="icon-button" title="编辑标题">${icon("edit")}</button>
      <button type="button" data-delete-task="${task.id}" class="icon-button" title="删除任务">${icon("close")}</button>
    </div>
  </article>`;
}

function tasksView(): string {
  if (state.selectedTask) return taskDetailView(state.selectedTask);
  const projectOptions = state.projects.map(item => `<option value="${item.id}" ${item.id === state.taskProjectFilter ? "selected" : ""}>${escapeHTML(item.name)}</option>`).join("");
  const statusOptions = TASK_STATUS_FILTERS.map(status => `<option value="${status}" ${status === state.taskStatusFilter ? "selected" : ""}>${statusLabel(status)}</option>`).join("");
  const filtered = state.tasks.filter(item =>
    (!state.taskProjectFilter || item.project_id === state.taskProjectFilter) &&
    (!state.taskStatusFilter || item.status === state.taskStatusFilter)
  );
  const rows = filtered.map(item => taskCardHtml(item)).join("");
  return shell(`<section class="page">
    ${pageHead("任务", "Task 是长期目标，每条用户输入创建一个独立 Turn。", `<button data-dialog="task">${icon("plus")}创建任务</button>`)}
    <div class="task-filters">
      <select id="task-filter-project" title="按项目筛选"><option value="">全部项目</option>${projectOptions}</select>
      <select id="task-filter-status" title="按状态筛选"><option value="">全部状态</option>${statusOptions}</select>
      <span class="task-count">${filtered.length} / ${state.tasks.length}</span>
    </div>
    <div class="task-list">${rows || `<div class="empty">${state.tasks.length ? "没有符合条件的任务" : "创建第一个任务开始执行。"}</div>`}</div>
    ${taskDialog()}
  </section>`);
}

function taskDialog(): string {
  return `<dialog id="task-dialog" class="wide"><form id="task-form" method="dialog"><div class="dialog-head"><h2>创建任务</h2><button type="button" data-close class="icon-button">${icon("close")}</button></div><label>标题<input name="title" required></label><label>需求<textarea name="request" required></textarea></label><div class="two"><label>项目<select name="project_id" id="task-project">${state.projects.map(item => `<option value="${item.id}">${escapeHTML(item.name)}</option>`).join("")}</select></label><label>Workspace<select name="workspace_id" id="task-workspace"></select></label></div><div class="two"><label>Backend<select id="task-backend"></select></label><label>模型<select name="model_id" id="task-model"></select></label></div><div class="two"><label>推理强度<select name="reasoning_effort" id="task-effort"></select></label><label>沙箱（文件访问）<select name="filesystem"><option value="workspace-write">工作区可写</option><option value="read-only">只读</option><option value="danger-full-access">完全访问</option></select></label></div><div class="two"><label>协作模式<select name="collaboration_mode"><option value="auto">Auto</option><option value="single">Single</option></select></label><label>最大 Agent 数<input name="max_agents" type="number" min="1" value="3"></label></div><label>审批<select name="approval"><option value="never">无需确认</option><option value="auto">自动批准（跳过权限检查）</option></select></label><div id="task-git-isolation"><label>任务隔离<select name="isolation" id="task-isolation"><option value="worktree">独立 Worktree（推荐）</option><option value="inplace">原地执行</option></select></label><div id="task-worktree-settings"><label>Worktree 根目录<input name="worktree_dir" id="task-worktree-dir" placeholder="默认：仓库上一级/.aha2-worktrees"></label><div class="field-help">系统会在根目录下追加 Task ID；切换为原地执行后不使用此配置。</div></div><div class="two" id="task-branches"><label>目标分支<input name="target_branch" placeholder="默认当前分支"></label><label>任务分支<input name="task_branch" placeholder="aha/task-name"></label></div></div><div class="dialog-actions"><button type="button" data-close>取消</button><button class="primary" value="default">创建任务</button></div></form></dialog>`;
}

let slashCommandSelection = 0;

function availableTaskSlashCommands(): typeof TASK_SLASH_COMMANDS[number][] {
  const detail = state.selectedTask;
  if (!detail) return [];
  const active = (detail.turns || []).some(turn => isActiveTurn(turn.status));
  const selectedActive = (detail.turns || []).some(turn => turn.agent_id === state.selectedTaskAgent && isActiveTurn(turn.status));
  return TASK_SLASH_COMMANDS.filter(command => {
    if (command.name === "/interrupt") return active;
    if (command.name === "/complete") return !active && detail.task.status !== "completed";
    if (command.name === "/compact" || command.name === "/reset") return !selectedActive;
    if (command.name === "/reopen") return ["completed", "failed", "blocked", "cancelled"].includes(detail.task.status);
    return false;
  });
}

function matchingTaskSlashCommands(value: string): typeof TASK_SLASH_COMMANDS[number][] {
  return matchingSlashCommands(value, availableTaskSlashCommands());
}

function slashCommandMenuHtml(value: string): string {
  const commands = matchingTaskSlashCommands(value);
  slashCommandSelection = Math.min(slashCommandSelection, Math.max(0, commands.length - 1));
  return commands.map((command, index) => `<button type="button" class="${index === slashCommandSelection ? "active" : ""}" data-slash-command="${escapeHTML(command.insert)}"><span>task</span><strong>${escapeHTML(command.name)}</strong><small>${escapeHTML(command.desc)}</small></button>`).join("");
}

function exactTaskSlashCommand(value: string): typeof TASK_SLASH_COMMANDS[number] | undefined {
  return exactSlashCommand(value, availableTaskSlashCommands());
}

function renderTaskSlashCommandMenu(textarea: HTMLTextAreaElement | null): void {
  const menu = document.querySelector<HTMLElement>("#slash-command-menu");
  if (!menu || !textarea) return;
  const html = slashCommandMenuHtml(textarea.value);
  menu.innerHTML = html;
  menu.hidden = !html;
}

function syncTaskComposerState(textarea: HTMLTextAreaElement | null): void {
  if (!textarea) return;
  renderTaskSlashCommandMenu(textarea);
  const send = document.querySelector<HTMLButtonElement>("#message-send");
  if (send) send.disabled = !textarea.value.trim();
}

function applyTaskSlashCommand(value: string): void {
  const textarea = document.querySelector<HTMLTextAreaElement>("#message-form textarea");
  if (!textarea) return;
  textarea.value = value;
  state.taskDraft = value;
  state.taskDrafts[state.selectedTaskAgent] = value;
  const menu = document.querySelector<HTMLElement>("#slash-command-menu");
  if (menu) menu.hidden = true;
  syncTaskComposerState(textarea);
  textarea.focus();
}

async function executeTaskSlashCommand(value: string): Promise<boolean> {
  const text = String(value || "").trim().toLowerCase();
  if (!text.startsWith("/")) return false;
  const command = TASK_SLASH_COMMANDS.find(item => item.name === text);
  if (!command) throw new Error(`未知命令：${text}`);
  const detail = state.selectedTask;
  if (!detail) throw new Error("未选择 Task");
  const taskID = detail.task.id;
  if (command.name === "/interrupt") {
    const round = detail.latest_round;
    if (!round || !(detail.turns || []).some(turn => isActiveTurn(turn.status))) {
      throw new Error("当前没有运行中的 Round");
    }
    await api.interruptRound(round.id);
  } else if (command.name === "/complete") {
    await api.completeTask(taskID);
  } else if (command.name === "/reopen") {
    await api.reopenTask(taskID);
  } else {
    const action = command.name === "/compact" ? "compact" : "reset";
    if (!await executeAgentSessionAction(api, taskID, state.selectedTaskAgent, action)) return true;
  }
  await refreshTaskRuntime(taskID);
  updateTaskLiveRegions();
  return true;
}

function updateLiveDurations(): void {
  const now = performance.now();
  document.querySelectorAll<HTMLElement>("[data-live-elapsed-ms]").forEach(element => {
    const base = Number(element.dataset.liveElapsedMs || 0);
    const syncedAt = Number(element.dataset.liveSyncedAt || now);
    const elapsed = base + (element.dataset.liveRunning === "true" ? Math.max(0, now - syncedAt) : 0);
    element.textContent = formatDuration(elapsed);
  });
}

function runTaskClock(): void {
  const second = Math.floor(Date.now() / 1000);
  if (second !== taskClockSecond) {
    taskClockSecond = second;
    updateLiveDurations();
  }
  if (state.selectedTask) {
    taskClockFrame = window.requestAnimationFrame(runTaskClock);
  } else {
    taskClockFrame = null;
  }
}

function startTaskClock(): void {
  if (taskClockFrame !== null) window.cancelAnimationFrame(taskClockFrame);
  taskClockSecond = -1;
  taskClockFrame = window.requestAnimationFrame(runTaskClock);
}
function conversationListHtml(): string {
  return renderConversationList(state.taskConversation, state.selectedTask, state.taskConversationHasMore);
}
function taskFailureBannerHtml(detail: TaskDetail): string {
  if (!["failed", "blocked"].includes(detail.task.status)) return "";
  const failure = [...(detail.turns || [])].reverse().find(turn => turn.status === "failed" || turn.status === "blocked");
  if (!failure) return "";
  return `<div class="task-failure-banner"><strong>失败原因</strong><span>${escapeHTML(failure.error || "Backend 执行失败，未返回详细原因")}</span><small>可直接发送下一条消息创建新 Round 重试</small></div>`;
}

function taskCtxHtml(): string {
  const value = state.taskContext;
  if (!value) return `<div class="ctx-loading">${icon("refresh")}正在加载 Context...</div>`;
  const context = value.context || {};
  const sessionActionDisabled = Boolean(state.selectedTask?.turns.some(turn => turn.agent_id === state.selectedTaskAgent && isActiveTurn(turn.status)));
  return `<div class="ctx-view">
    ${renderContextMetrics(context, sessionActionDisabled)}
    <details class="prompt-snapshot" open><summary>查看本轮 Prompt Snapshot</summary><pre>${escapeHTML(context.prompt || "尚无 Prompt Snapshot")}</pre></details>
    <div class="ctx-columns evidence-only">
      <section><h3>Context Evidence</h3>${[...(value.project_knowledge || []), ...(value.global_knowledge || [])].map(item => `<div class="knowledge-mini"><strong>${escapeHTML(item.title)}</strong><small>${escapeHTML(item.scope)} · ${escapeHTML(item.type)}</small><p>${escapeHTML(item.body)}</p></div>`).join("") || "<p>暂无已验证知识</p>"}</section>
    </div>
  </div>`;
}

function taskDetailView(detail: TaskDetail): string {
  const activeTurn = (detail.turns || []).find(item => item.agent_id === state.selectedTaskAgent && isActiveTurn(item.status));
  const taskFailed = detail.task.status === "failed";
  const project = state.projects.find(item => item.id === detail.task.project_id);
  const workspace = state.workspaces.find(item => item.id === detail.task.workspace_id);
  const taskMeta = `${project?.name || "-"} · ${workspace?.name || "-"} · ${detail.task.collaboration_mode || "auto"} · ${detail.task.max_agents || 3} Agents`;
  const chat = `<section class="conversation">
    <div id="task-failure-slot">${taskFailureBannerHtml(detail)}</div>
    <div class="messages" id="conversation-list">${conversationListHtml()}</div>
    <div id="agent-turn-slot">${renderAgentTurnCard(detail, state.taskRealtimeState)}</div>
    <form id="message-form" class="composer">${renderComposerTools(detail, state.selectedTaskAgent, state.taskCategories, state.taskConversation.length)}<div id="slash-command-menu" class="slash-command-menu" ${matchingTaskSlashCommands(state.taskDraft).length ? "" : "hidden"}>${slashCommandMenuHtml(state.taskDraft)}</div><textarea name="content" placeholder="${activeTurn ? `${state.selectedTaskAgent} 正在执行，发送后将排队` : taskFailed ? "输入消息重试，或输入 /reopen" : `发送给 ${state.selectedTaskAgent}，输入 / 查看命令`}" required>${escapeHTML(state.taskDraft)}</textarea><button id="message-send" class="primary" aria-label="发送">${icon("send")}<span class="send-label">发送</span></button></form>
  </section>`;
  return shell(`<section class="task-screen">
    <header class="task-head"><button id="back-tasks">←</button><div class="task-title-block"><h1><span class="task-code">${escapeHTML(detail.task.code || "")}</span><span class="task-title-text">${escapeHTML(detail.task.title)}</span></h1><div class="task-head-subline"><span class="task-head-meta" title="${escapeHTML(taskMeta)}">${escapeHTML(taskMeta)}</span><span id="task-detail-status" class="status ${statusClass(detail.task.status)}">${statusLabel(detail.task.status)}</span><span class="task-branch">${escapeHTML(detail.task.task_branch || "")}</span></div></div><div class="actions task-tool-actions">${renderTaskToolButtons(state.taskTool)}</div></header>
    <div class="task-grid">
      ${chat}
      ${renderTaskToolPanel(state.taskTool, detail, state.selectedTaskAgent, taskCtxHtml())}
    </div>
    ${renderAgentConfigDialog(detail, state.selectedTaskAgent, state.models)}
  </section>`);
}

function updateTaskLiveRegions(): void {
  const detail = state.selectedTask;
  if (!detail) return;
  const activeTurn = (detail.turns || []).find(item => item.agent_id === state.selectedTaskAgent && isActiveTurn(item.status));
  const list = document.querySelector<HTMLElement>("#conversation-list");
  if (list) {
    const previousHeight = list.scrollHeight;
    const previousTop = list.scrollTop;
    const wasAtBottom = previousHeight - previousTop - list.clientHeight < 80;
    list.innerHTML = conversationListHtml();
    if (wasAtBottom) list.scrollTop = list.scrollHeight;
    else list.scrollTop = previousTop;
    bindConversationLiveControls();
  }
  const turnSlot = document.querySelector<HTMLElement>("#agent-turn-slot");
  if (turnSlot) turnSlot.innerHTML = renderAgentTurnCard(detail, state.taskRealtimeState);
  const failureSlot = document.querySelector<HTMLElement>("#task-failure-slot");
  if (failureSlot) failureSlot.innerHTML = taskFailureBannerHtml(detail);
  const agentSelect = document.querySelector<HTMLSelectElement>("#composer-agent");
  if (agentSelect) agentSelect.innerHTML = renderComposerAgentOptions(detail, state.selectedTaskAgent);
  const taskStatus = document.querySelector<HTMLElement>("#task-detail-status");
  if (taskStatus) {
    taskStatus.className = `status ${statusClass(detail.task.status)}`;
    taskStatus.textContent = statusLabel(detail.task.status);
  }
  const toolBody = document.querySelector<HTMLElement>("#task-tool-panel-body");
  if (toolBody && state.taskTool && state.taskTool !== "hardware") {
    toolBody.innerHTML = renderTaskToolContent(state.taskTool, detail, taskCtxHtml());
    bindSessionActions();
  }
  const count = document.querySelector<HTMLElement>("#conversation-filter-loaded");
  if (count) count.textContent = `${state.taskConversation.length} 条已加载`;
  const textarea = document.querySelector<HTMLTextAreaElement>("#message-form textarea");
  if (textarea) {
    textarea.placeholder = activeTurn ? `${state.selectedTaskAgent} 正在执行，发送后将排队` : detail.task.status === "failed" ? "输入消息重试，或输入 /reopen" : `发送给 ${state.selectedTaskAgent}，输入 / 查看命令`;
    syncTaskComposerState(textarea);
  }
  updateLiveDurations();
}

function knowledgeView(): string {
  const rows = state.knowledge.map(item => `<article class="knowledge-row"><div><span class="status ${statusClass(item.status)}">${statusLabel(item.status)}</span><strong>${escapeHTML(item.title)}</strong><p>${escapeHTML(item.body)}</p><small>${escapeHTML(item.scope)} · ${escapeHTML(item.type)} · confidence ${item.confidence.toFixed(2)}</small></div>${item.status === "candidate" ? `<button data-verify="${item.id}">验证</button>` : ""}</article>`).join("");
  return shell(`<section class="page">
    ${pageHead("知识库", "Global 保存通用经验，Project 保存项目专用认知。知识在 Turn 中持续产生。", `<button data-dialog="knowledge">${icon("plus")}新建知识</button>`)}
    <div class="knowledge-tabs"><button class="active">全部</button><span>Candidate ${state.knowledge.filter(item => item.status === "candidate").length}</span><span>Verified ${state.knowledge.filter(item => item.status === "verified").length}</span></div>
    <div class="panel">${rows || `<div class="empty">知识会在 Task Turn 中持续增长。</div>`}</div>
    ${knowledgeDialog()}
  </section>`);
}

function knowledgeDialog(): string {
  return `<dialog id="knowledge-dialog"><form id="knowledge-form" method="dialog"><div class="dialog-head"><h2>新建知识</h2><button type="button" data-close class="icon-button">${icon("close")}</button></div><div class="two"><label>作用域<select name="scope"><option value="project">Project</option><option value="global">Global</option></select></label><label>项目<select name="project_id">${state.projects.map(item => `<option value="${item.id}">${escapeHTML(item.name)}</option>`).join("")}</select></label></div><label>类型<select name="type"><option>practice</option><option>navigation</option><option>decision</option><option>solution</option></select></label><label>标题<input name="title" required></label><label>正文<textarea name="body" required></textarea></label><label>置信度<input name="confidence" type="number" min="0" max="1" step="0.1" value="0.7"></label><div class="dialog-actions"><button type="button" data-close>取消</button><button class="primary" value="default">保存 Candidate</button></div></form></dialog>`;
}

function render(): void {
  if (!app) return;
  document.body.classList.toggle("task-view-active", Boolean(state.selectedTask));
  if (state.loading) {
    app.innerHTML = `<div class="loading">AHA2 正在加载...</div>`;
    return;
  }
  if (!state.auth?.authenticated) {
    app.innerHTML = loginView();
    bindAuth();
    return;
  }
  // Never tear down the DOM while a modal dialog is open, or the dialog would
  // close unexpectedly. Defer until the dialog closes (see close listener).
  if (document.querySelector("dialog[open]")) {
    state.renderPending = true;
    return;
  }
  persistNavigationState();
  if (document.activeElement instanceof HTMLTextAreaElement && document.activeElement.closest("#message-form")) {
    state.renderPending = true;
    return;
  }
  state.renderPending = false;
  const previousConversation = document.querySelector<HTMLElement>("#conversation-list");
  const previousScrollTop = previousConversation?.scrollTop || 0;
  const previousScrollHeight = previousConversation?.scrollHeight || 0;
  const wasAtConversationBottom = previousConversation
    ? previousConversation.scrollHeight - previousConversation.scrollTop - previousConversation.clientHeight < 80
    : false;
  let content: string;
  if (state.selectedProject) {
    content = projectDetailView(state.selectedProject);
  } else {
    const views: Record<View, () => string> = {
      projects: projectsView,
      models: modelsView,
      tasks: tasksView,
      knowledge: knowledgeView,
      prompts: () => shell(renderPromptAdmin()),
    };
    content = views[state.view]();
  }
  app.innerHTML = content;
  bindCommon();
  if (state.selectedTask) {
    window.scrollTo(0, 0);
    document.documentElement.scrollTop = 0;
    document.body.scrollTop = 0;
  }
  const nextConversation = document.querySelector<HTMLElement>("#conversation-list");
  if (nextConversation) {
    if (scrollConversationToBottom || wasAtConversationBottom) {
      nextConversation.scrollTop = nextConversation.scrollHeight;
    } else {
      nextConversation.scrollTop = previousScrollTop;
    }
  }
  scrollConversationToBottom = false;
}

function bindAuth(): void {
  document.querySelector<HTMLFormElement>("#auth-form")?.addEventListener("submit", async event => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const payload = Object.fromEntries(form.entries()) as Record<string, string>;
    try {
      const response = state.auth?.registration_open ? await api.register(payload) : await api.login(payload);
      state.auth = {...response, authenticated: true, registration_open: false, username: payload.username};
      api.setCSRF(response.csrf_token);
      state.error = "";
      await loadAll();
      openGlobalEvents();
    } catch (error) {
      state.error = error instanceof Error ? error.message : String(error);
    }
    render();
  });
}

function bindCommon(): void {
  document.querySelectorAll<HTMLElement>("[data-view]").forEach(button => button.addEventListener("click", () => {
    state.view = button.dataset.view as View;
    state.selectedTask = null;
    state.selectedProject = null;
    state.dialogProjectID = "";
    closeEvents();
    render();
  }));
  document.querySelector("#logout")?.addEventListener("click", () => {
    const button = document.querySelector<HTMLElement>("#logout");
    void runWithFeedback(button, "退出中", async () => {
      await api.logout();
      state.auth = {...state.auth!, authenticated: false};
      state.selectedTask = null;
      state.selectedProject = null;
      clearNavigationSnapshot();
      closeEvents();
      closeGlobalEvents();
      render();
    });
  });
  document.querySelector("#refresh")?.addEventListener("click", () => {
    const button = document.querySelector<HTMLElement>("#refresh");
    void runWithFeedback(button, "刷新中", refresh);
  });
  document.querySelector("#mobile-context")?.addEventListener("click", () => {
    state.view = "models";
    state.selectedProject = null;
    render();
  });
  document.querySelectorAll<HTMLElement>("[data-dialog]").forEach(button => button.addEventListener("click", () => {
    if (button.dataset.dialog === "workspace") {
      state.dialogProjectID = state.selectedProject?.id || "";
      openWorkspaceDialog(null);
      return;
    }
    if (button.dataset.dialog === "provider") {
      openProviderDialog(null);
      return;
    }
    if (button.dataset.dialog === "project") {
      openProjectDialog(null);
      return;
    }
    if (button.dataset.dialog === "task") {
      syncTaskWorkspaces();
    }
    document.querySelector<HTMLDialogElement>(`#${button.dataset.dialog}-dialog`)?.showModal();
  }));
  document.querySelectorAll<HTMLElement>("[data-edit-project]").forEach(button => button.addEventListener("click", async event => {
    event.stopPropagation();
    const project = state.projects.find(item => item.id === button.dataset.editProject!);
    if (project) openProjectDialog(project);
  }));
  document.querySelectorAll<HTMLElement>("[data-edit-project-detail]").forEach(button => button.addEventListener("click", () => {
    const project = state.projects.find(item => item.id === button.dataset.editProjectDetail!);
    if (project) openProjectDialog(project);
  }));
  document.querySelectorAll<HTMLElement>("[data-edit-workspace]").forEach(button => button.addEventListener("click", () => {
    const ws = state.workspaces.find(item => item.id === button.dataset.editWorkspace!);
    if (ws) openWorkspaceDialog(ws);
  }));
  document.querySelectorAll<HTMLElement>("[data-edit-provider]").forEach(button => button.addEventListener("click", () => {
    const provider = state.providers.find(item => item.id === button.dataset.editProvider!);
    if (provider) openProviderDialog(provider);
  }));
  document.querySelector<HTMLSelectElement>("#project-type")?.addEventListener("change", syncProjectTypeFields);
  document.querySelector<HTMLSelectElement>("#ws-locality")?.addEventListener("change", syncWorkspaceFields);
  document.querySelector<HTMLSelectElement>("#ws-transport")?.addEventListener("change", syncWorkspaceFields);
  document.querySelector<HTMLSelectElement>('[name="ssh_auth"]')?.addEventListener("change", syncWorkspaceFields);
  document.querySelector<HTMLInputElement>('[name="clear_ssh_password"]')?.addEventListener("change", syncWorkspaceFields);
  document.querySelector("#provider-preset")?.addEventListener("change", applyProviderPreset);
  document.querySelectorAll<HTMLElement>("[data-close]").forEach(button => button.addEventListener("click", () => {
    button.closest("dialog")?.close();
  }));
  document.querySelectorAll<HTMLElement>("[data-project]").forEach(element => element.addEventListener("click", () => {
    const id = element.dataset.project!;
    state.selectedProject = state.projects.find(item => item.id === id) || null;
    state.dialogProjectID = id;
    state.selectedTask = null;
    closeEvents();
    render();
  }));
  document.querySelector("#back-projects")?.addEventListener("click", () => {
    state.selectedProject = null;
    state.dialogProjectID = "";
    render();
  });
  bindForm("#project-form", async form => {
    const payload = Object.fromEntries(form.entries()) as Record<string, string>;
    const editId = payload["project_edit_id"] || "";
    delete payload["project_edit_id"];
    if (editId) await api.updateProject(editId, payload);
    else await api.createProject(payload);
  });
  bindForm("#workspace-form", async form => {
    const payload = Object.fromEntries(form.entries()) as Record<string, string>;
    const editId = payload["ws_edit_id"] || "";
    delete payload["ws_edit_id"];
    if (editId) delete payload["project_id"];
    const body = {
      ...payload,
      ssh_port: Number(payload.ssh_port || 22),
    };
    delete body.ws_edit_id;
    if (editId) {
      await api.updateWorkspace(editId, {
        ...body,
        clear_ssh_password: payload.clear_ssh_password === "on",
      });
    } else {
      delete body.clear_ssh_password;
      await api.createWorkspace(body);
    }
  });
  document.querySelector("#save-provider")?.addEventListener("click", () => {
    const button = document.querySelector<HTMLElement>("#save-provider");
    const editId = String((document.querySelector("#provider-edit-id") as HTMLInputElement)?.value || "");
    const name = String((document.querySelector("#provider-name") as HTMLInputElement)?.value || "").trim();
    const baseURL = String((document.querySelector("#provider-base-url") as HTMLInputElement)?.value || "").trim();
    const anthropicURL = String((document.querySelector("#provider-anthropic-url") as HTMLInputElement)?.value || "").trim();
    const apiKey = String((document.querySelector("#provider-api-key") as HTMLInputElement)?.value || "").trim();
    if (!name || !baseURL) {
      setMessage("error", "请填写 Provider 名称与 API Base URL");
      return;
    }
    void runWithFeedback(button, "保存中", async () => {
      const body = {name, base_url: baseURL, anthropic_base_url: anthropicURL, api_key: apiKey, auth_style: "auto"};
      if (editId) await api.updateProvider(editId, body);
      else await api.createProvider(body);
      document.querySelector<HTMLDialogElement>("#provider-dialog")?.close();
      setMessage("notice", "Provider 已保存");
      await loadAll();
      render();
    });
  });
  document.querySelector("#detect-models")?.addEventListener("click", () => {
    const button = document.querySelector<HTMLElement>("#detect-models");
    const providerID = String((document.querySelector("#model-provider") as HTMLSelectElement)?.value || "");
    if (!providerID) {
      setMessage("error", "请先添加并选择 Provider");
      return;
    }
    const results = document.querySelector<HTMLElement>("#model-detect-results");
    if (!results) return;
    void runWithFeedback(button, "检测中", async () => {
      const response = await api.detectModels({provider_id: providerID});
      const models = response.models || [];
      if (!models.length) {
        results.innerHTML = `<div class="detect-status">未检测到模型。</div>`;
        return;
      }
      const anthropicNote = response.anthropic_base_url ? `<div class="detect-status">检测到 Anthropic 端点：<code>${escapeHTML(response.anthropic_base_url)}</code></div>` : "";
      results.innerHTML = `<div class="detect-status">检测到 ${models.length} 个模型（认证：${escapeHTML(response.auth_style)}）。支持全选/全不选与搜索过滤。</div>${anthropicNote}<div class="detect-toolbar"><input type="search" id="detect-search" placeholder="搜索模型名称..."><button type="button" id="detect-select-all">全选</button><button type="button" id="detect-select-none">全不选</button></div><div class="detected-list">${models.map(item => `<div class="detected-item" data-model="${escapeHTML(item.id)}"><input type="checkbox" class="detected-model" value="${escapeHTML(item.id)}" checked><span><strong>${escapeHTML(item.id)}</strong>${item.max_input_tokens ? `<small>context ${Math.round(item.max_input_tokens / 1000)}K</small>` : ""}<span class="proto-badges">${protoBadges(item)}</span>${protoChecks(item)}</span></div>`).join("")}</div><div class="dialog-actions"><button type="button" id="add-selected-models" class="primary">添加所选模型</button></div>`;
      document.querySelector("#detect-search")?.addEventListener("input", event => {
        const query = String((event.target as HTMLInputElement).value || "").trim().toLowerCase();
        document.querySelectorAll<HTMLElement>("#model-detect-results .detected-item").forEach(row => {
          const model = String(row.dataset.model || "").toLowerCase();
          row.style.display = !query || model.includes(query) ? "" : "none";
        });
      });
      document.querySelector("#detect-select-all")?.addEventListener("click", () => {
        document.querySelectorAll<HTMLElement>("#model-detect-results .detected-item").forEach(row => {
          if (row.style.display === "none") return;
          const box = row.querySelector<HTMLInputElement>(".detected-model");
          if (box) box.checked = true;
        });
      });
      document.querySelector("#detect-select-none")?.addEventListener("click", () => {
        document.querySelectorAll<HTMLInputElement>("#model-detect-results .detected-model").forEach(box => {
          box.checked = false;
        });
      });
      document.querySelector<HTMLElement>("#add-selected-models")?.addEventListener("click", () => {
        const checked = [...document.querySelectorAll<HTMLInputElement>("#model-detect-results input.detected-model:checked")].map(input => input.value);
        if (!checked.length) {
          setMessage("error", "请至少选择一个模型");
          return;
        }
        const picks = checked.map(id => {
          const row = document.querySelector<HTMLInputElement>(`#model-detect-results input.detected-model[value="${CSS.escape(id)}"]`)?.closest(".detected-item");
          const wireAPIs = [...(row?.querySelectorAll<HTMLInputElement>(".proto-checks input:checked") || [])].map(input => input.value);
          const detected = models.find(item => item.id === id);
          return {
            id,
            wire_apis: wireAPIs.length ? wireAPIs : ["responses"],
            max_input_tokens: detected?.max_input_tokens || 0,
            max_output_tokens: detected?.max_output_tokens || 0,
          };
        });
        const addButton = document.querySelector<HTMLElement>("#add-selected-models");
        void runWithFeedback(addButton, "添加中", async () => {
          const result = await api.addModels({provider_id: providerID, models: picks, anthropic_base_url: response.anthropic_base_url || ""});
          document.querySelector<HTMLDialogElement>("#model-dialog")?.close();
          const added = (result.models || []).length;
          const skipped = result.skipped || 0;
          setMessage("notice", skipped ? `已添加 ${added} 个模型，跳过 ${skipped} 个已存在` : `已添加 ${added} 个模型`);
          await loadAll();
          render();
        });
      });
    });
  });
  document.querySelectorAll<HTMLElement>("[data-delete-provider]").forEach(button => button.addEventListener("click", async event => {
    event.stopPropagation();
    const id = button.dataset.deleteProvider!;
    if (!window.confirm("删除该 Provider 及其未占用的模型 / Env Group？")) return;
    void runWithFeedback(button, "删除中", async () => {
      const result = await api.deleteProvider(id);
      setMessage("notice", result.warning || "Provider 已删除");
      await loadAll();
      render();
    });
  }));
  document.querySelectorAll<HTMLElement>("[data-delete-project]").forEach(button => button.addEventListener("click", async event => {
    event.stopPropagation();
    const id = button.dataset.deleteProject!;
    if (!window.confirm("删除该项目及其所有 Workspace / 任务？此操作不可恢复。")) return;
    void runWithFeedback(button, "删除中", async () => {
      await api.deleteProject(id);
      if (state.selectedProject?.id === id) state.selectedProject = null;
      setMessage("notice", "项目已删除");
      await loadAll();
      render();
    });
  }));
  document.querySelectorAll<HTMLElement>("[data-delete-workspace]").forEach(button => button.addEventListener("click", async event => {
    event.stopPropagation();
    const id = button.dataset.deleteWorkspace!;
    if (!window.confirm("删除该 Workspace 及其所有任务？此操作不可恢复。")) return;
    void runWithFeedback(button, "删除中", async () => {
      await api.deleteWorkspace(id);
      setMessage("notice", "Workspace 已删除");
      await loadAll();
      render();
    });
  }));
  document.querySelectorAll<HTMLElement>("[data-delete-task]").forEach(button => button.addEventListener("click", async event => {
    event.stopPropagation();
    const id = button.dataset.deleteTask!;
    if (!window.confirm("删除该任务？此操作不可恢复。")) return;
    void runWithFeedback(button, "删除中", async () => {
      await api.deleteTask(id);
      if (state.selectedTask?.task.id === id) {
        state.selectedTask = null;
        closeEvents();
      }
      setMessage("notice", "任务已删除");
      await loadAll();
      render();
    });
  }));
  document.querySelector("#task-filter-project")?.addEventListener("change", event => {
    state.taskProjectFilter = (event.target as HTMLSelectElement).value;
    render();
  });
  document.querySelector("#task-filter-status")?.addEventListener("change", event => {
    state.taskStatusFilter = (event.target as HTMLSelectElement).value;
    render();
  });
  document.querySelectorAll<HTMLElement>("[data-edit-task-title]").forEach(button => button.addEventListener("click", async event => {
    event.stopPropagation();
    const taskID = button.dataset.editTaskTitle!;
    const task = state.tasks.find(item => item.id === taskID);
    const heading = document.querySelector<HTMLElement>(`[data-task-title="${CSS.escape(taskID)}"]`);
    if (!task || !heading) return;
    const input = document.createElement("input");
    input.className = "task-title-input";
    input.maxLength = 200;
    input.value = task.title;
    heading.replaceWith(input);
    input.focus();
    input.select();
    let settled = false;
    const finish = async (save: boolean) => {
      if (settled) return;
      settled = true;
      const title = input.value.trim();
      if (save && title && title !== task.title) {
        try {
          await api.updateTaskTitle(taskID, title);
          setMessage("notice", "标题已更新");
        } catch (error) {
          setMessage("error", error instanceof Error ? error.message : String(error));
        }
      }
      await loadAll();
      render();
    };
    input.addEventListener("click", event => event.stopPropagation());
    input.addEventListener("mousedown", event => event.stopPropagation());
    input.addEventListener("keydown", event => {
      if (event.key === "Enter") {
        event.preventDefault();
        void finish(true);
      } else if (event.key === "Escape") {
        event.preventDefault();
        void finish(false);
      }
    });
    input.addEventListener("blur", () => void finish(true));
  }));
  document.querySelector("#delete-project")?.addEventListener("click", () => {
    if (!state.selectedProject) return;
    const id = state.selectedProject.id;
    if (!window.confirm("删除该项目及其所有 Workspace / 任务？此操作不可恢复。")) return;
    const button = document.querySelector<HTMLElement>("#delete-project");
    void runWithFeedback(button, "删除中", async () => {
      await api.deleteProject(id);
      state.selectedProject = null;
      setMessage("notice", "项目已删除");
      await loadAll();
      render();
    });
  });
  document.querySelectorAll<HTMLElement>("[data-delete-model]").forEach(button => button.addEventListener("click", () => {
    const id = button.dataset.deleteModel!;
    if (!window.confirm("删除该模型及其 Env Group？")) return;
    void runWithFeedback(button, "删除中", async () => {
      await api.deleteModel(id);
      setMessage("notice", "模型已删除");
      await loadAll();
      render();
    });
  }));
  document.querySelectorAll<HTMLElement>("[data-edit-model]").forEach(button => button.addEventListener("click", () => {
    const id = button.dataset.editModel!;
    const model = state.models.find(item => item.id === id);
    if (!model) return;
    const nameInput = document.querySelector<HTMLInputElement>("#model-edit-name");
    const wireInput = document.querySelector<HTMLSelectElement>("#model-edit-wire");
    const effortInput = document.querySelector<HTMLSelectElement>("#model-edit-effort");
    const contextInput = document.querySelector<HTMLInputElement>("#model-edit-context");
    const maxoutInput = document.querySelector<HTMLInputElement>("#model-edit-maxout");
    if (nameInput) nameInput.value = model.display_name;
    if (wireInput) wireInput.value = model.backend === "claude" ? "anthropic_messages" : (model.wire_api || "responses");
    if (effortInput) effortInput.value = model.default_reasoning_effort || "";
    if (contextInput) contextInput.value = String(model.context_window || 0);
    if (maxoutInput) maxoutInput.value = String(model.max_output_tokens || 0);
    (document.querySelector<HTMLDialogElement>("#model-edit-dialog"))?.showModal();
    document.querySelector<HTMLElement>("#save-model-edit")?.setAttribute("data-model-id", id);
  }));
  document.querySelector("#save-model-edit")?.addEventListener("click", () => {
    const button = document.querySelector<HTMLElement>("#save-model-edit");
    const id = button?.getAttribute("data-model-id");
    if (!id) return;
    void runWithFeedback(button, "保存中", async () => {
      await api.updateModel(id, {
        display_name: String((document.querySelector<HTMLInputElement>("#model-edit-name"))?.value || "").trim(),
        wire_api: String((document.querySelector<HTMLSelectElement>("#model-edit-wire"))?.value || "responses"),
        context_window: Number((document.querySelector<HTMLInputElement>("#model-edit-context"))?.value || 0),
        max_output_tokens: Number((document.querySelector<HTMLInputElement>("#model-edit-maxout"))?.value || 0),
        default_reasoning_effort: String((document.querySelector<HTMLSelectElement>("#model-edit-effort"))?.value || ""),
      });
      document.querySelector<HTMLDialogElement>("#model-edit-dialog")?.close();
      setMessage("notice", "模型已更新");
      await loadAll();
      render();
    });
  });
  document.querySelector("#task-project")?.addEventListener("change", syncTaskWorkspaces);
  document.querySelector("#task-workspace")?.addEventListener("change", () => {
    const worktreeDir = document.querySelector<HTMLInputElement>("#task-worktree-dir");
    if (worktreeDir) worktreeDir.value = "";
    syncTaskBackend();
  });
  document.querySelector("#task-isolation")?.addEventListener("change", syncTaskGitIsolation);
  document.querySelector("#task-backend")?.addEventListener("change", () => {
    refreshTaskModels();
    syncTaskEffort();
  });
  document.querySelector("#task-model")?.addEventListener("change", syncTaskEffort);
  bindForm("#task-form", async form => {
    const payload = Object.fromEntries(form.entries()) as Record<string, string>;
    const result = await api.createTask({...payload, max_agents: Number(payload.max_agents || 3)});
    await openTask(result.task.id);
    if (result.start_error) setMessage("error", `Task 已创建，但首个 Turn 启动失败：${result.start_error}`);
  });
  bindForm("#knowledge-form", async form => {
    const payload = Object.fromEntries(form.entries()) as Record<string, string>;
    await api.createKnowledge({...payload, confidence: Number(payload.confidence || 0)});
  });
  document.querySelectorAll<HTMLElement>("[data-detect]").forEach(button => button.addEventListener("click", () => {
    const id = button.dataset.detect!;
    void runWithFeedback(button, "检测中", async () => {
      const result = await api.detectWorkspace(id);
      setMessage("notice", `检测完成：${statusLabel(result.workspace.health)}`);
      await loadAll();
      render();
    });
  }));
  document.querySelectorAll<HTMLElement>("[data-task]").forEach(button => button.addEventListener("click", async () => {
    await openTask(button.dataset.task!);
    render();
  }));
  bindTaskAgentControls();
  document.querySelector<HTMLSelectElement>("#composer-agent")?.addEventListener("change", async event => {
    await selectTaskAgent(event.currentTarget.value);
    render();
  });
  document.querySelector("#agent-config-backend")?.addEventListener("change", syncAgentConfigFields);
  document.querySelector<HTMLInputElement>('[name="inherit_main"]')?.addEventListener("change", syncAgentConfigFields);
  bindForm("#agent-config-form", async form => {
    if (!state.selectedTask) return;
    const agentID = String(form.get("agent_id") || state.selectedTaskAgent);
    if (agentID === "main") {
      await api.updateTask(state.selectedTask.task.id, {
        collaboration_mode: String(form.get("collaboration_mode") || "auto"),
        max_agents: Number(form.get("max_agents") || 3),
      });
    }
    const inheritMain = agentID !== "main" && form.get("inherit_main") === "on";
    await api.updateTaskAgent(state.selectedTask.task.id, agentID, inheritMain ? {
      inherit_main: true,
    } : {
      inherit_main: false,
      backend: String(form.get("backend") || ""),
      model_id: String(form.get("model_id") || ""),
      reasoning_effort: String(form.get("reasoning_effort") || ""),
      filesystem: String(form.get("filesystem") || ""),
      approval: String(form.get("approval") || ""),
    });
    await refreshTaskRuntime(state.selectedTask.task.id);
    setMessage("notice", `${agentID} 配置已更新，下一个 Turn 生效`);
  });
  document.querySelector("#back-tasks")?.addEventListener("click", () => {
    state.selectedTask = null;
    closeEvents();
    render();
  });
  document.querySelector<HTMLFormElement>("#message-form")?.addEventListener("submit", async event => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const content = String(form.get("content") || "").trim();
    if (!state.selectedTask) return;
    const textarea = document.querySelector<HTMLTextAreaElement>("#message-form textarea");
    try {
      const handled = await executeTaskSlashCommand(content);
      if (!handled) {
        await api.agentMessage(state.selectedTask.task.id, state.selectedTaskAgent, content);
        await refreshTaskRuntime(state.selectedTask.task.id);
        updateTaskLiveRegions();
      }
      state.taskDraft = "";
      state.taskDrafts[state.selectedTaskAgent] = "";
      if (textarea) {
        textarea.value = "";
        textarea.style.height = "auto";
        syncTaskComposerState(textarea);
      }
      const menu = document.querySelector<HTMLElement>("#slash-command-menu");
      if (menu) menu.hidden = true;
    } catch (error) {
      state.taskDraft = content;
      state.taskDrafts[state.selectedTaskAgent] = content;
      if (textarea) textarea.value = content;
      setMessage("error", error instanceof Error ? error.message : String(error));
    }
  });
  document.querySelector<HTMLTextAreaElement>("#message-form textarea")?.addEventListener("input", event => {
    const textarea = event.target as HTMLTextAreaElement;
    state.taskDraft = textarea.value;
    state.taskDrafts[state.selectedTaskAgent] = textarea.value;
    persistNavigationState();
    slashCommandSelection = 0;
    textarea.style.height = "auto";
    textarea.style.height = `${Math.min(textarea.scrollHeight, 160)}px`;
    syncTaskComposerState(textarea);
  });
  document.querySelector<HTMLTextAreaElement>("#message-form textarea")?.addEventListener("focus", () => {
    document.body.classList.add("composer-focused");
    syncVisualViewportHeight();
    syncTaskComposerState(document.querySelector<HTMLTextAreaElement>("#message-form textarea"));
  });
  document.querySelector<HTMLTextAreaElement>("#message-form textarea")?.addEventListener("keydown", event => {
    if (event.isComposing || event.keyCode === 229) return;
    const textarea = event.currentTarget;
    const commands = matchingTaskSlashCommands(textarea.value);
    if (commands.length && event.key === "ArrowDown") {
      event.preventDefault();
      slashCommandSelection = (slashCommandSelection + 1) % commands.length;
      renderTaskSlashCommandMenu(textarea);
      return;
    }
    if (commands.length && event.key === "ArrowUp") {
      event.preventDefault();
      slashCommandSelection = (slashCommandSelection + commands.length - 1) % commands.length;
      renderTaskSlashCommandMenu(textarea);
      return;
    }
    if (commands.length && event.key === "Tab") {
      event.preventDefault();
      applyTaskSlashCommand(commands[slashCommandSelection].insert);
      return;
    }
    if (commands.length && event.key === "Escape") {
      const menu = document.querySelector<HTMLElement>("#slash-command-menu");
      if (menu) menu.hidden = true;
      return;
    }
    const plainEnter = event.key === "Enter" && !event.shiftKey && !event.ctrlKey && !event.metaKey && !event.altKey;
    const touchInput = window.matchMedia("(max-width: 760px), (pointer: coarse)").matches || navigator.maxTouchPoints > 0;
    if (plainEnter && !touchInput) {
      event.preventDefault();
      if (commands.length && !exactTaskSlashCommand(textarea.value)) {
        applyTaskSlashCommand(commands[slashCommandSelection].insert);
      } else {
        event.currentTarget.form?.requestSubmit();
      }
    }
  });
  document.querySelector<HTMLElement>("#slash-command-menu")?.addEventListener("pointerdown", event => {
    const button = event.target instanceof Element ? event.target.closest<HTMLElement>("[data-slash-command]") : null;
    if (!button) return;
    event.preventDefault();
    applyTaskSlashCommand(button.dataset.slashCommand || "");
  });
  document.querySelector<HTMLTextAreaElement>("#message-form textarea")?.addEventListener("blur", () => {
    window.setTimeout(() => {
      if (!(document.activeElement instanceof HTMLTextAreaElement && document.activeElement.closest("#message-form"))) {
        document.body.classList.remove("composer-focused");
        const menu = document.querySelector<HTMLElement>("#slash-command-menu");
        if (menu) menu.hidden = true;
      }
      syncVisualViewportHeight();
      flushDeferredRender();
    }, 0);
  });
  syncTaskComposerState(document.querySelector<HTMLTextAreaElement>("#message-form textarea"));
  document.querySelectorAll<HTMLElement>("[data-conversation-category]").forEach(button => button.addEventListener("click", async () => {
    const category = button.dataset.conversationCategory as ConversationCategory;
    state.taskCategories[category] = !state.taskCategories[category];
    if (!selectedConversationCategories().length) state.taskCategories.chat = true;
    await reloadConversation();
    render();
  }));
  bindConversationLiveControls();
  document.querySelectorAll<HTMLElement>("[data-task-tool]").forEach(button => button.addEventListener("click", async () => {
    const tool = button.dataset.taskTool as TaskTool;
    if (state.taskTool === "hardware") stopHardwarePanel();
    if (state.taskTool === tool) {
      state.taskTool = "";
      render();
      return;
    }
    state.taskTool = tool;
    if (tool !== "context" || !state.selectedTask) {
      render();
      return;
    }
    state.taskContext = null;
    render();
    try {
      state.taskContext = await api.agentContext(state.selectedTask.task.id, state.selectedTaskAgent);
    } catch (error) {
      setMessage("error", error instanceof Error ? error.message : String(error));
    }
    render();
  }));
  bindSessionActions();
  document.querySelectorAll<HTMLElement>("[data-verify]").forEach(button => button.addEventListener("click", () => {
    const id = button.dataset.verify!;
    void runWithFeedback(button, "验证中", async () => {
      await api.verifyKnowledge(id);
      await loadAll();
      render();
    });
  }));
  document.querySelector("#close-task-tool")?.addEventListener("click", () => {
    if (state.taskTool === "hardware") stopHardwarePanel();
    state.taskTool = "";
    render();
  });
  if (state.taskTool === "hardware" && state.selectedTask) bindHardwarePanel(state.selectedTask, setMessage);
  if (state.view === "prompts") bindPromptAdmin(render, setMessage);
}

function bindSessionActions(): void {
  document.querySelectorAll<HTMLElement>("[data-session-action]").forEach(button => button.addEventListener("click", () => {
    if (!state.selectedTask) return;
    const action = button.dataset.sessionAction === "reset" ? "reset" : "compact";
    void runWithFeedback(button, action === "compact" ? "压缩中" : "重置中", async () => {
      const changed = await executeAgentSessionAction(api, state.selectedTask!.task.id, state.selectedTaskAgent, action);
      if (!changed) return;
      state.taskContext = await api.agentContext(state.selectedTask!.task.id, state.selectedTaskAgent);
      await refreshTaskRuntime(state.selectedTask!.task.id);
      const content = document.querySelector<HTMLElement>("#task-tool-panel-body");
      if (content) {
        content.innerHTML = renderTaskToolContent(state.taskTool, state.selectedTask!, taskCtxHtml());
        bindSessionActions();
      }
    });
  }));
}

function bindForm(selector: string, action: (form: FormData) => Promise<void>): void {
  document.querySelector<HTMLFormElement>(selector)?.addEventListener("submit", async event => {
    event.preventDefault();
    const dialog = event.currentTarget.closest("dialog");
    try {
      await action(new FormData(event.currentTarget));
      dialog?.close();
      await refresh();
    } catch (error) {
      setMessage("error", error instanceof Error ? error.message : String(error));
      render();
    }
  });
}

function updateRealtimeState(value: State["taskRealtimeState"]): void {
  state.taskRealtimeState = value;
  const element = document.querySelector<HTMLElement>("[data-realtime-state]");
  if (element) {
    element.className = `realtime-state ${value}`;
    element.textContent = value === "live" ? "SSE 实时" : value === "fallback" ? "增量拉取兜底" : "连接中";
  }
  const badge = document.querySelector<HTMLElement>("[data-round-channel]");
  if (badge) {
    badge.className = `round-channel ${value}`;
    badge.textContent = value === "live" ? "SSE" : value === "fallback" ? "2s" : "...";
  }
}

function scheduleTaskRefresh(taskID: string): void {
  if (taskRefreshTimer !== null) window.clearTimeout(taskRefreshTimer);
  taskRefreshTimer = window.setTimeout(async () => {
    taskRefreshTimer = null;
    try {
      if (await refreshTaskRuntime(taskID)) updateTaskLiveRegions();
    } catch {
      startTaskFallback(taskID);
    }
  }, 100);
}

function stopTaskFallback(): void {
  if (taskFallbackTimer !== null) window.clearInterval(taskFallbackTimer);
  taskFallbackTimer = null;
}

function startTaskFallback(taskID: string, announce = true): void {
  if (state.selectedTask?.task.id !== taskID) return;
  if (announce) updateRealtimeState("fallback");
  if (taskFallbackTimer !== null) return;
  const refresh = async () => {
    if (state.selectedTask?.task.id !== taskID) return;
    try {
      if (await refreshTaskRuntime(taskID)) updateTaskLiveRegions();
    } catch {
      // Keep the existing UI and retry while the task remains open.
    }
  };
  void refresh();
  taskFallbackTimer = window.setInterval(() => void refresh(), 2000);
}

function noteTaskStreamSignal(taskID: string): void {
  if (state.selectedTask?.task.id !== taskID) return;
  taskLastSignalAt = Date.now();
  stopTaskFallback();
  updateRealtimeState("live");
}

async function loadOlderConversation(): Promise<void> {
  if (!state.selectedTask || !state.taskConversationBefore) return;
  const list = document.querySelector<HTMLElement>("#conversation-list");
  const previousHeight = list?.scrollHeight || 0;
  const previousTop = list?.scrollTop || 0;
  const page = await api.agentConversation(state.selectedTask.task.id, state.selectedTaskAgent, {
    before: state.taskConversationBefore,
    limit: 50,
    categories: selectedConversationCategories(),
  });
  const older = page.conversation.items || [];
  const seen = new Set(state.taskConversation.map(item => item.sequence));
  state.taskConversation = [...older.filter(item => !seen.has(item.sequence)), ...state.taskConversation];
  if (state.taskConversation.length > 300) {
    state.taskConversation = state.taskConversation.slice(0, 300);
  }
  state.taskConversationHasMore = page.conversation.has_more && state.taskConversation.length < 300;
  state.taskConversationBefore = page.conversation.next_before || 0;
  if (list) {
    list.innerHTML = conversationListHtml();
    list.scrollTop = previousTop + Math.max(0, list.scrollHeight - previousHeight);
    bindConversationLiveControls();
  }
  const count = document.querySelector<HTMLElement>("#conversation-filter-count");
  if (count) count.textContent = `${state.taskConversation.length} 条已加载`;
}

function bindConversationLiveControls(): void {
  bindMessageBubbleControls(document.querySelector("#conversation-list") || document);
  document.querySelector("#load-older-conversation")?.addEventListener("click", () => {
    void loadOlderConversation();
  });
}

function openEvents(taskID: string, after = 0): void {
  closeEvents();
  taskLastSignalAt = Date.now();
  updateRealtimeState("connecting");
  startTaskClock();
  taskMonitorTimer = window.setInterval(() => {
    if (state.selectedTask?.task.id === taskID && Date.now() - taskLastSignalAt > 15000) {
      startTaskFallback(taskID);
    }
  }, 5000);
  events = new EventSource(`/api/v1/tasks/${taskID}/events?after=${after}`);
  startTaskFallback(taskID, false);
  events.addEventListener("open", () => {
    taskLastSignalAt = Date.now();
    updateRealtimeState("connecting");
  });
  events.addEventListener("heartbeat", () => {
    noteTaskStreamSignal(taskID);
    updateLiveDurations();
  });
  events.addEventListener("error", () => startTaskFallback(taskID));
  events.addEventListener("update", event => {
    if (state.selectedTask?.task.id !== taskID) return;
    noteTaskStreamSignal(taskID);
    try {
      const payload = JSON.parse((event as MessageEvent).data) as {sequence?: number};
      const sequence = Number(payload.sequence || 0);
      if (sequence && sequence <= state.taskEventCursor) return;
      state.taskEventCursor = Math.max(state.taskEventCursor, sequence);
    } catch {
      // The incremental refresh below remains authoritative.
    }
    scheduleTaskRefresh(taskID);
  });
}

function closeEvents(): void {
  events?.close();
  events = null;
  if (taskRefreshTimer !== null) window.clearTimeout(taskRefreshTimer);
  if (taskClockFrame !== null) window.cancelAnimationFrame(taskClockFrame);
  if (taskMonitorTimer !== null) window.clearInterval(taskMonitorTimer);
  stopTaskFallback();
  taskRefreshTimer = null;
  taskClockFrame = null;
  taskClockSecond = -1;
  taskMonitorTimer = null;
  taskLastSignalAt = 0;
  document.body.classList.remove("composer-focused");
}

// If a render was deferred while a dialog was open, apply it once the dialog
// closes so the UI never goes stale but the dialog is never yanked away.
function flushDeferredRender(): void {
  if (!state.renderPending || document.querySelector("dialog[open]")) return;
  if (document.activeElement instanceof HTMLTextAreaElement && document.activeElement.closest("#message-form")) return;
  render();
}

document.addEventListener("close", event => {
  if (event.target instanceof HTMLDialogElement) flushDeferredRender();
}, true);

// Event-driven list refresh: one global SSE stream keeps the task list (and
// project/workspace labels) fresh without polling. The task detail view keeps
// its own per-task SSE; while a detail is open we still refresh list state in
// the background but only re-render when not inside the detail.
let globalEvents: EventSource | null = null;

async function refreshListData(): Promise<void> {
  if (!state.auth?.authenticated) return;
  try {
    const [tasks, projects, workspaces] = await Promise.all([api.tasks(), api.projects(), api.workspaces()]);
    state.tasks = tasks.tasks || [];
    state.projects = projects.projects || [];
    state.workspaces = workspaces.workspaces || [];
    if (!state.selectedTask) render();
  } catch {
    // transient network/backend errors are ignored
  }
}

function openGlobalEvents(): void {
  if (globalEvents) return;
  globalEvents = new EventSource("/api/v1/events");
  globalEvents.addEventListener("update", event => {
    let type = "";
    try {
      type = String((JSON.parse((event as MessageEvent).data) as {type?: string}).type || "");
    } catch {
      return;
    }
    if (type.startsWith("task_") || type.startsWith("turn_") || type.startsWith("knowledge_")) {
      void refreshListData();
    }
  });
  // The stream auto-reconnects; refresh on (re)open so events missed while
  // disconnected are caught up.
  globalEvents.addEventListener("open", () => void refreshListData());
}

function closeGlobalEvents(): void {
  globalEvents?.close();
  globalEvents = null;
}

syncVisualViewportHeight();
window.addEventListener("resize", syncVisualViewportHeight);
window.visualViewport?.addEventListener("resize", syncVisualViewportHeight);
window.visualViewport?.addEventListener("scroll", syncVisualViewportHeight);
void bootstrap();
