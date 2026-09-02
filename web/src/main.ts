import {api} from "./api.js";
import {icon} from "./icons.js";
import type {
  AuthStatus,
  DetectedModel,
  Knowledge,
  Model,
  Project,
  Provider,
  SystemInfo,
  Task,
  TaskDetail,
  Workspace,
} from "./types.js";

type View = "projects" | "models" | "tasks" | "knowledge";

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
  selectedProject: Project | null;
  dialogProjectID: string;
  contextOpen: boolean;
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
  selectedProject: null,
  dialogProjectID: "",
  contextOpen: false,
  taskProjectFilter: "",
  taskStatusFilter: "",
};

const app = document.querySelector<HTMLDivElement>("#app");
let events: EventSource | null = null;

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

function syncWorkspaceGitIsolation(): void {
  const projectSelect = document.querySelector<HTMLSelectElement>("#ws-project");
  const project = state.projects.find(item => item.id === projectSelect?.value);
  const section = document.querySelector<HTMLElement>(".git-isolation");
  const isoSelect = document.querySelector<HTMLSelectElement>("#ws-isolation");
  if (!section || !isoSelect) return;
  const isGit = project?.project_type === "git";
  section.style.display = isGit ? "" : "none";
  if (!isGit) {
    isoSelect.value = "inplace";
    const worktreeDir = document.querySelector<HTMLInputElement>("#ws-worktree-dir");
    if (worktreeDir) worktreeDir.value = "";
    return;
  }
  const root = String((document.querySelector("#ws-root-path") as HTMLInputElement)?.value || "");
  const worktreeDir = document.querySelector<HTMLInputElement>("#ws-worktree-dir");
  if (worktreeDir && !worktreeDir.value && root) {
    const parent = root.replace(/[\\/]+$/, "").replace(/[\\/][^\\/]*$/, "");
    worktreeDir.value = parent + "/.aha2-worktrees";
  }
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
  syncWorkspaceFields();
  const isoSelect = dialog.querySelector<HTMLSelectElement>("#ws-isolation");
  const worktreeDir = dialog.querySelector<HTMLInputElement>("#ws-worktree-dir");
  const project = state.projects.find(item => item.id === (projectSelect?.value || ""));
  if (isoSelect) isoSelect.value = ws?.isolation || (project?.project_type === "git" ? "worktree" : "inplace");
  if (worktreeDir) worktreeDir.value = ws?.worktree_dir || "";
  syncWorkspaceGitIsolation();
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
  syncTaskBranches();
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

function syncTaskBranches(): void {
  const projectID = String((document.querySelector("#task-project") as HTMLSelectElement)?.value || "");
  const project = state.projects.find(item => item.id === projectID);
  const branches = document.querySelector<HTMLElement>("#task-branches");
  if (!branches) return;
  branches.style.display = project?.project_type === "git" ? "" : "none";
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
}

async function refresh(): Promise<void> {
  try {
    await loadAll();
    if (state.selectedTask) state.selectedTask = await api.task(state.selectedTask.task.id);
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
    <div class="ws-meta"><small title="${item.worktree_dir ? escapeHTML(item.worktree_dir) : ""}">${escapeHTML(item.transport)}${item.distro ? ` · ${escapeHTML(item.distro)}` : ""}${item.isolation === "inplace" ? " · 原地执行" : item.isolation === "worktree" ? ` · Worktree隔离${item.worktree_dir ? " @ " + escapeHTML(item.worktree_dir) : ""}` : ""}</small><strong>${escapeHTML(item.platform || "-")}</strong></div>
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
  return `<dialog id="workspace-dialog"><form id="workspace-form" method="dialog"><div class="dialog-head"><h2>添加 Workspace</h2><button type="button" data-close class="icon-button">${icon("close")}</button></div><input type="hidden" id="ws-edit-id" name="ws_edit_id" value=""><label>项目<select name="project_id" id="ws-project">${state.projects.map(item => `<option value="${item.id}" ${item.id === state.dialogProjectID ? "selected" : ""}>${escapeHTML(item.name)}</option>`).join("")}</select></label><label>名称<input name="name" id="ws-name" value="本地开发" required></label><div class="two"><label>位置<select name="locality" id="ws-locality"><option value="local">本地</option><option value="remote">远程</option></select></label><label>Transport<select name="transport" id="ws-transport"></select></label></div><label>Root Path<input name="root_path" id="ws-root-path" placeholder="E:\project 或 /home/user/project" required></label><div class="wsl-fields" style="display:none"><label>WSL Distro<select name="distro" id="ws-distro"></select></label><div class="field-help">Root Path 填 WSL 内的路径，如 /home/user/project</div></div><div class="ssh-fields" style="display:none"><div class="two"><label>SSH Host<input name="ssh_host" placeholder="192.168.1.10"></label><label>SSH User<input name="ssh_user" placeholder="root"></label></div><label>SSH Port<input name="ssh_port" type="number" value="22"></label></div><div class="git-isolation" style="display:none"><div class="two"><label>任务隔离<select name="isolation" id="ws-isolation"><option value="worktree">独立 Worktree（推荐）</option><option value="inplace">原地执行</option></select></label><label>Worktree 目录<input name="worktree_dir" id="ws-worktree-dir" placeholder="默认：仓库上一级/.aha2-worktrees"></label></div><div class="field-help">任务将在此目录建独立 worktree+分支（task-001），主工作区不动。</div></div><div class="dialog-actions"><button type="button" data-close>取消</button><button class="primary" value="default">添加 Workspace</button></div></form></dialog>`;
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
  return `<dialog id="task-dialog" class="wide"><form id="task-form" method="dialog"><div class="dialog-head"><h2>创建任务</h2><button type="button" data-close class="icon-button">${icon("close")}</button></div><label>标题<input name="title" required></label><label>需求<textarea name="request" required></textarea></label><div class="two"><label>项目<select name="project_id" id="task-project">${state.projects.map(item => `<option value="${item.id}">${escapeHTML(item.name)}</option>`).join("")}</select></label><label>Workspace<select name="workspace_id" id="task-workspace"></select></label></div><div class="two"><label>Backend<select id="task-backend"></select></label><label>模型<select name="model_id" id="task-model"></select></label></div><div class="two"><label>推理强度<select name="reasoning_effort" id="task-effort"></select></label><label>沙箱（文件访问）<select name="filesystem"><option value="workspace-write">工作区可写</option><option value="read-only">只读</option><option value="danger-full-access">完全访问</option></select></label></div><label>审批<select name="approval"><option value="never">无需确认</option><option value="auto">自动批准（跳过权限检查）</option></select></label><div class="two" id="task-branches"><label>目标分支<input name="target_branch" placeholder="默认当前分支"></label><label>任务分支<input name="task_branch" placeholder="aha/task-name"></label></div><div class="dialog-actions"><button type="button" data-close>取消</button><button class="primary" value="default">创建任务</button></div></form></dialog>`;
}

function taskDetailView(detail: TaskDetail): string {
  const taskTurns = detail.turns || [];
  const taskMessages = detail.messages || [];
  const candidates = detail.knowledge_candidates || [];
  const activeTurn = [...taskTurns].reverse().find(item => !["succeeded", "failed", "interrupted", "blocked"].includes(item.status));
  const messages = taskMessages.map(item => `<article class="message ${item.role === "user" ? "user" : "agent"}"><header><strong>${escapeHTML(item.sender)}</strong><time>${new Date(item.created_at).toLocaleTimeString([], {hour: "2-digit", minute: "2-digit"})}</time></header><div>${escapeHTML(item.content).replaceAll("\n", "<br>")}</div></article>`).join("");
  const turns = [...taskTurns].reverse().slice(0, 8).map(item => `<div class="turn-line"><span class="status ${statusClass(item.status)}">Turn ${item.sequence} · ${statusLabel(item.status)}</span><small>${escapeHTML(item.backend_session_id || "new session")}</small></div>`).join("");
  const memory = detail.memory || {facts: [], decisions: [], progress: [], verification: [], next_actions: []};
  return shell(`<section class="task-screen">
    <header class="task-head"><button id="back-tasks">←</button><div><h1><span class="task-code">${escapeHTML(detail.task.code || "")}</span> ${escapeHTML(detail.task.title)}</h1><p><span class="status ${statusClass(detail.task.status)}">${statusLabel(detail.task.status)}</span> ${escapeHTML(detail.task.task_branch || "")}</p></div><div class="actions">${activeTurn ? `<button id="interrupt">${icon("close")}中断 Turn</button>` : ""}<button id="complete-task">完成 Task</button><button id="delete-task" class="danger">${icon("close")}删除 Task</button><button id="toggle-context" class="mobile-only">${icon("menu")}</button></div></header>
    <div class="task-grid">
      <aside class="task-summary"><h3>执行上下文</h3><dl><dt>Project</dt><dd>${escapeHTML(state.projects.find(item => item.id === detail.task.project_id)?.name || "-")}</dd><dt>Workspace</dt><dd>${escapeHTML(state.workspaces.find(item => item.id === detail.task.workspace_id)?.name || "-")}</dd><dt>Branch</dt><dd>${escapeHTML(detail.task.task_branch || "-")}</dd></dl><h3>Turn 历史</h3>${turns || "<p>尚无 Turn</p>"}</aside>
      <section class="conversation"><div class="turn-card ${activeTurn ? "active" : ""}">${activeTurn ? `<strong>Turn ${activeTurn.sequence} · ${statusLabel(activeTurn.status)}</strong><small>权威状态来自持久化 Turn Projection</small>` : `<strong>等待下一条消息</strong><small>新消息将创建独立 Turn，并尝试复用 Backend Session</small>`}</div><div class="messages">${messages || `<div class="empty">发送第一条消息开始任务。</div>`}</div><form id="message-form" class="composer"><textarea name="content" placeholder="${activeTurn ? "当前 Turn 执行中，新消息将在完成后发送" : "输入下一步要求"}" required></textarea><button class="primary" aria-label="发送" ${activeTurn ? "disabled" : ""}>${icon("send")}<span class="send-label">发送</span></button></form></section>
      <aside class="context-panel ${state.contextOpen ? "open" : ""}"><button id="close-context" class="mobile-only icon-button">${icon("close")}</button><h3>Task Memory</h3>${memoryList("事实", memory.facts)}${memoryList("决策", memory.decisions)}${memoryList("进度", memory.progress)}${memoryList("验证", memory.verification)}${memoryList("下一步", memory.next_actions)}<h3>Knowledge Candidate</h3>${candidates.filter(item => item.status === "candidate").slice(0, 4).map(item => `<div class="knowledge-mini"><strong>${escapeHTML(item.title)}</strong><small>${escapeHTML(item.body)}</small></div>`).join("") || "<p>暂无 Candidate</p>"}</aside>
    </div>
  </section>`);
}

function memoryList(title: string, values: string[] = []): string {
  const items = values || [];
  return `<section class="memory-block"><strong>${title}</strong>${items.length ? `<ul>${items.map(item => `<li>${escapeHTML(item)}</li>`).join("")}</ul>` : "<small>-</small>"}</section>`;
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
  state.renderPending = false;
  let content: string;
  if (state.selectedProject) {
    content = projectDetailView(state.selectedProject);
  } else {
    const views: Record<View, () => string> = {
      projects: projectsView,
      models: modelsView,
      tasks: tasksView,
      knowledge: knowledgeView,
    };
    content = views[state.view]();
  }
  app.innerHTML = content;
  bindCommon();
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
  document.querySelector<HTMLSelectElement>("#ws-project")?.addEventListener("change", syncWorkspaceGitIsolation);
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
    const body = {...payload, ssh_port: Number(payload.ssh_port || 22)};
    if (editId) await api.updateWorkspace(editId, body);
    else await api.createWorkspace(body);
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
          return {id, wire_apis: wireAPIs.length ? wireAPIs : ["responses"]};
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
  document.querySelector("#delete-task")?.addEventListener("click", () => {
    if (!state.selectedTask) return;
    const id = state.selectedTask.task.id;
    if (!window.confirm("删除该任务？此操作不可恢复。")) return;
    const button = document.querySelector<HTMLElement>("#delete-task");
    void runWithFeedback(button, "删除中", async () => {
      await api.deleteTask(id);
      state.selectedTask = null;
      closeEvents();
      setMessage("notice", "任务已删除");
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
  document.querySelector("#task-workspace")?.addEventListener("change", syncTaskBackend);
  document.querySelector("#task-backend")?.addEventListener("change", () => {
    refreshTaskModels();
    syncTaskEffort();
  });
  document.querySelector("#task-model")?.addEventListener("change", syncTaskEffort);
  bindForm("#task-form", async form => {
    const payload = Object.fromEntries(form.entries()) as Record<string, string>;
    const result = await api.createTask(payload);
    state.selectedTask = await api.task(result.task.id);
    openEvents(result.task.id);
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
    state.selectedTask = await api.task(button.dataset.task!);
    openEvents(button.dataset.task!);
    render();
  }));
  document.querySelector("#back-tasks")?.addEventListener("click", () => {
    state.selectedTask = null;
    closeEvents();
    render();
  });
  document.querySelector<HTMLFormElement>("#message-form")?.addEventListener("submit", async event => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const content = String(form.get("content") || "");
    if (!state.selectedTask) return;
    await api.message(state.selectedTask.task.id, content);
    state.selectedTask = await api.task(state.selectedTask.task.id);
    render();
  });
  document.querySelector("#interrupt")?.addEventListener("click", () => {
    const button = document.querySelector<HTMLElement>("#interrupt");
    const turn = [...(state.selectedTask?.turns || [])].reverse().find(item => !["succeeded", "failed", "interrupted", "blocked"].includes(item.status));
    if (!turn) return;
    void runWithFeedback(button, "中断中", async () => {
      await api.interruptTurn(turn.id);
    });
  });
  document.querySelector("#complete-task")?.addEventListener("click", () => {
    const button = document.querySelector<HTMLElement>("#complete-task");
    if (!state.selectedTask) return;
    const taskID = state.selectedTask.task.id;
    void runWithFeedback(button, "完成中", async () => {
      await api.completeTask(taskID);
      state.selectedTask = await api.task(taskID);
      render();
    });
  });
  document.querySelectorAll<HTMLElement>("[data-verify]").forEach(button => button.addEventListener("click", () => {
    const id = button.dataset.verify!;
    void runWithFeedback(button, "验证中", async () => {
      await api.verifyKnowledge(id);
      await loadAll();
      render();
    });
  }));
  document.querySelector("#toggle-context")?.addEventListener("click", () => {
    state.contextOpen = true;
    render();
  });
  document.querySelector("#close-context")?.addEventListener("click", () => {
    state.contextOpen = false;
    render();
  });
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

function openEvents(taskID: string): void {
  closeEvents();
  events = new EventSource(`/api/v1/tasks/${taskID}/events`);
  events.addEventListener("update", async () => {
    if (state.selectedTask?.task.id === taskID) {
      state.selectedTask = await api.task(taskID);
      render();
    }
  });
}

function closeEvents(): void {
  events?.close();
  events = null;
}

// If a render was deferred while a dialog was open, apply it once the dialog
// closes so the UI never goes stale but the dialog is never yanked away.
document.addEventListener("close", event => {
  if (event.target instanceof HTMLDialogElement && state.renderPending) {
    render();
  }
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

void bootstrap();
