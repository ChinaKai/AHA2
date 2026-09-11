import {api} from "./api.js";
import {renderConversationList} from "./conversation_ui.js";
import {bindCodexAccounts, renderCodexAccounts} from "./codex_accounts.js";
import {bindChannels, exportRetiredChannelInstance, purgeRetiredChannelInstance, renderChannels} from "./channels.js";
import {icon} from "./icons.js";
import {bindKnowledgeWorkspace, renderKnowledgeWorkspace} from "./knowledge_workspace.js";
import {bindHardwarePanel, refreshHardwarePanel, stopHardwarePanel} from "./hardware_panel.js";
import {renderMarkdown} from "./markdown.js";
import {bindPromptAdmin, loadPromptCatalog, renderPromptAdmin} from "./prompt_admin.js";
import {bindProxySettings, renderProxySettings} from "./proxy_settings.js";
import {bindSyncSettings, isSyncSettingsFormEditing, renderSyncSettings} from "./sync_settings.js";
import {bindRuntimeFields, runtimeFieldsHTML, setRuntimeBackends, syncRuntimeFields} from "./runtime_picker.js";
import {renderComposerAgentOptions, renderComposerTools} from "./task_composer.js";
import {TASK_TOOL_DEFAULT_WIDTH, normalizeTaskToolMode, normalizeTaskToolWidth, renderTaskToolButtons, renderTaskToolContent, renderTaskToolPanel} from "./task_tools.js";
import type {TaskTool, TaskToolMode} from "./task_tools.js";
import {TASK_SLASH_COMMANDS, bindMessageBubbleControls, captureInteractiveRegion, clearNavigationSnapshot, eventRefreshesTaskList, exactSlashCommand, executeAgentSessionAction, loadNavigationSnapshot, matchingSlashCommands, renderWorkspaceDetection, restoreInteractiveRegion, saveNavigationSnapshot} from "./ui_helpers.js";
import type {InteractiveRegionState} from "./ui_helpers.js";
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
  AgentAPISettings,
	BackendSettings,
  Attachment,
  AuthStatus,
  CodexAccount,
  ChannelInstance,
  ChannelPlugin,
  ConversationCategory,
  ConversationItem,
  DetectedModel,
  EnvGroup,
  Knowledge,
  KnowledgeLibrary,
  KnowledgeProposal,
  KnowledgeReviewSettings,
  Model,
  Project,
  Provider,
  ProxySettings,
  ManagedProxyView,
  SecuritySettings,
  SyncConflict,
	SyncPreview,
	SyncRunProgress,
  SyncSettings,
  SyncState,
  Skill,
  SystemInfo,
  Task,
  TaskContextDetail,
  TaskDetail,
  Workspace,
} from "./types.js";
type View = "projects" | "channels" | "models" | "tasks" | "knowledge" | "prompts" | "proxy" | "sync" | "advanced";
type ResourceKey = "projects" | "workspaces" | "tasks" | "system" | "providers" | "models" | "env" | "accounts" | "skills" | "channels" | "knowledge" | "proxy" | "advanced" | "sync" | "prompts";
interface ResourceState {loading: boolean; loaded: boolean; error: string; updatedAt: number}
interface State {
  auth: AuthStatus | null;
  view: View;
  loading: boolean;
  hydrating: boolean;
  error: string;
  notice: string;
  renderPending: boolean;
  system: SystemInfo;
  proxySettings: ProxySettings;
  managedProxy: ManagedProxyView;
  securitySettings: SecuritySettings;
  agentAPISettings: AgentAPISettings;
	backendSettings: BackendSettings;
  syncSettings: SyncSettings;
  syncState: SyncState;
  syncPending: number;
  syncConflicts: SyncConflict[];
	syncPreview: SyncPreview;
	syncRun: SyncRunProgress;
  projects: Project[];
  workspaces: Workspace[];
  providers: Provider[];
  channelProviders: ChannelPlugin[];
  channelInstances: ChannelInstance[];
  envGroups: EnvGroup[];
  codexAccounts: CodexAccount[];
  models: Model[];
  tasks: Task[];
  knowledge: Knowledge[];
  knowledgeLibraries: KnowledgeLibrary[];
  knowledgeProposals: KnowledgeProposal[];
  knowledgeReviewSettings: KnowledgeReviewSettings;
  skills: Skill[];
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
  taskAttachmentDrafts: Record<string, Attachment[]>;
  selectedTaskAgent: string;
  selectedProject: Project | null;
  dialogProjectID: string;
  taskTool: TaskTool | "";
  taskToolMode: TaskToolMode;
  taskToolWidth: number;
  taskProjectFilter: string;
  taskStatusFilter: string;
}

const taskToolLayoutKey = "aha2.task-tool-layout";

function loadTaskToolLayout(): {mode: TaskToolMode; width: number} {
  try {
    const saved = JSON.parse(localStorage.getItem(taskToolLayoutKey) || "{}") as {mode?: unknown; width?: unknown};
    return {mode: normalizeTaskToolMode(saved.mode), width: normalizeTaskToolWidth(saved.width)};
  } catch {
    return {mode: "split", width: TASK_TOOL_DEFAULT_WIDTH};
  }
}

const savedTaskToolLayout = loadTaskToolLayout();

const state: State = {
  auth: null,
  view: "projects",
  loading: true,
  hydrating: false,
  error: "",
  notice: "",
  renderPending: false,
  system: {os: "windows", arch: "", wsl_available: false, wsl_distros: [], version: "dev", started_at: ""},
  proxySettings: {mode: "external", http_proxy: "http://127.0.0.1:7897", https_proxy: "http://127.0.0.1:7897", no_proxy: "localhost,127.0.0.1,::1", managed_refresh_interval_minutes: 1440},
  managedProxy: {configured: false, profiles: [], url_configured: false, nodes: [], unsupported_count: 0, unsupported_types: [], status: "idle"},
  securitySettings: {validate_origin: true, startup_override: false},
  agentAPISettings: {url: "", allow_insecure: false, effective_url: "http://127.0.0.1:8766", effective_allow_insecure: false},
	backendSettings: {idle_timeout_seconds: 10 * 60, turn_timeout_seconds: 10 * 60 * 60},
  syncSettings: {scope:"default",enabled:false,endpoint:"",device_id:"",device_name:"",interval_seconds:300,token_configured:false,passphrase_configured:false},
  syncState: {scope:"default",cursor:"",last_error:""},
  syncPending: 0,
  syncConflicts: [],
	syncPreview: {upserts: 0, deletes: 0, remote_upserts: 0, remote_deletes: 0, pending: 0, conflicts: 0},
	syncRun: {running: false, phase: "", completed: 0, total: 0},
  projects: [],
  workspaces: [],
  providers: [],
  channelProviders: [],
  channelInstances: [],
  envGroups: [],
  codexAccounts: [],
  models: [],
  tasks: [],
  knowledge: [],
  knowledgeLibraries: [],
  knowledgeProposals: [],
  knowledgeReviewSettings: {auto_approve: false},
  skills: [],
  selectedTask: null,
  taskConversation: [],
  taskConversationHasMore: false,
  taskConversationBefore: 0,
  taskConversationLatest: 0,
  taskEventCursor: 0,
  taskCategories: {chat: true, update: true, tool: false, error: true},
  taskContext: null,
  taskRealtimeState: "connecting",
  taskDraft: "",
  taskDrafts: {},
  taskAttachmentDrafts: {},
  selectedTaskAgent: "main",
  selectedProject: null,
  dialogProjectID: "",
  taskTool: "",
  taskToolMode: savedTaskToolLayout.mode,
  taskToolWidth: savedTaskToolLayout.width,
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
let conversationBottomPinVersion = 0;
let conversationAutoScrollBlockedUntil = 0;
let loadingOlderConversation = false;
let taskListPointerActive = false;
let appPointerActive = false;
let openingTaskID = "";
let listRefreshInFlight = false;
let listRefreshQueued = false;
let authRecoveryOpen = false;
let messageSubmitPending = false;
let ownerAvatarClicks = 0;
let ownerAvatarResetTimer = 0;
const resourceStates = Object.fromEntries([
  "projects", "workspaces", "tasks", "system", "providers", "models", "env", "accounts", "skills",
  "channels", "knowledge", "proxy", "advanced", "sync", "prompts",
].map(key => [key, {loading: false, loaded: false, error: "", updatedAt: 0}])) as Record<ResourceKey, ResourceState>;
const resourceRequests = new Map<ResourceKey, Promise<void>>();
const listPages: Record<"projects" | "tasks" | "knowledge", {cursor: string; hasMore: boolean; loadingMore: boolean}> = {
  projects: {cursor: "", hasMore: false, loadingMore: false},
  tasks: {cursor: "", hasMore: false, loadingMore: false},
  knowledge: {cursor: "", hasMore: false, loadingMore: false},
};
const initialListPageSize = 50;

function mergeByID<T extends {id: string}>(current: T[], incoming: T[]): T[] {
  const merged = new Map(current.map(item => [item.id, item]));
  for (const item of incoming) merged.set(item.id, item);
  return [...merged.values()];
}

function resourcesForView(view: View): ResourceKey[] {
  switch (view) {
    case "projects": return ["projects", "workspaces", "tasks"];
    case "tasks": return ["projects", "workspaces", "tasks", "models", "accounts", "skills"];
    case "channels": return ["channels", "projects", "workspaces", "models", "accounts", "knowledge"];
    case "models": return ["providers", "models", "env", "accounts"];
    case "knowledge": return ["projects", "workspaces", "knowledge"];
    case "prompts": return ["prompts"];
    case "proxy": return ["proxy"];
    case "sync": return ["sync"];
    case "advanced": return ["advanced"];
  }
}

async function loadResource(key: ResourceKey, force: boolean, action: () => Promise<void>): Promise<void> {
  if (!force && resourceStates[key].loaded) return;
  const current = resourceRequests.get(key);
  if (current) return current;
  resourceStates[key].loading = true;
  resourceStates[key].error = "";
  window.queueMicrotask(() => {
    if (state.auth?.authenticated) render();
  });
  const request = action().then(() => {
    resourceStates[key].loaded = true;
    resourceStates[key].updatedAt = Date.now();
  }).catch(error => {
    resourceStates[key].error = error instanceof Error ? error.message : String(error);
  }).finally(() => {
    resourceStates[key].loading = false;
    resourceRequests.delete(key);
    if (state.auth?.authenticated) render();
  });
  resourceRequests.set(key, request);
  return request;
}

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
    draft: "草稿",
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
    degraded: "部分可用",
    error: "不可访问",
  };
  return labels[status] || status;
}

function projectTypeLabel(type?: string): string {
  return type === "git" ? "Git 仓库" : type === "channel" ? "渠道宿主" : "普通文件夹";
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
  const agentAPIMode = dialog.querySelector<HTMLSelectElement>('[name="agent_api_mode"]');
  const agentAPIURL = dialog.querySelector<HTMLInputElement>('[name="agent_api_url"]');
  const agentAPIResult = dialog.querySelector<HTMLElement>(".workspace-agent-api-result");
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
  if (agentAPIMode) agentAPIMode.value = ws?.agent_api_mode || "auto";
  if (agentAPIURL) agentAPIURL.value = ws?.agent_api_url || "";
  if (agentAPIResult) {
    agentAPIResult.textContent = ws?.agent_api_status === "ready" && ws.agent_api_resolved_url
      ? `当前已验证：${ws.agent_api_resolved_url}`
      : ws?.agent_api_error || "保存后点击 Workspace 的“测试连接”，AHA2 会从该 Workspace 反向验证并保存有效地址。";
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
  if (state.codexAccounts.length) present.add("codex");
  return ["codex", "claude"].filter(backend => present.has(backend));
}

function syncTaskBackend(): void {
  const wsID = String((document.querySelector<HTMLSelectElement>("#task-workspace"))?.value || "");
  const ws = state.workspaces.find(item => item.id === wsID);
  const options = backendOptionsForWorkspace(ws);
  setRuntimeBackends("task", options);
  syncRuntimeFields("task", state.models, state.codexAccounts);
  syncTaskGitIsolation();
}

function syncTakeoverTaskBackend(): void {
  const wsID = String(document.querySelector<HTMLSelectElement>("#takeover-task-workspace")?.value || "");
  const workspace = state.workspaces.find(item => item.id === wsID);
  setRuntimeBackends("takeover-task", backendOptionsForWorkspace(workspace));
  syncRuntimeFields("takeover-task", state.models, state.codexAccounts);
}

async function detectWorkspaceWithHostKeyTrust(id: string): Promise<{workspace: Workspace} | null> {
  try {
    return await api.detectWorkspace(id);
  } catch (error) {
    const apiError = error as Error & {code?: string};
    if (apiError.code !== "ssh_host_key_unknown") throw error;
    const {host_key: hostKey} = await api.workspaceHostKey(id);
    const confirmed = window.confirm(`首次连接 SSH Workspace：${hostKey.endpoint}\n算法：${hostKey.algorithm}\nSHA256 指纹：${hostKey.fingerprint}\n\n请与目标机器或可信渠道提供的指纹核对。确认信任并重新检测？`);
    if (!confirmed) return null;
    await api.trustWorkspaceHostKey(id, hostKey.fingerprint);
    return api.detectWorkspace(id);
  }
}

function syncWorkspaceTakeoverFields(): void {
  const dialog = document.querySelector<HTMLDialogElement>("#workspace-takeover-dialog");
  const transport = dialog?.querySelector<HTMLSelectElement>('[name="transport"]')?.value || "native";
  const ssh = dialog?.querySelector<HTMLElement>(".workspace-takeover-ssh");
  const wsl = dialog?.querySelector<HTMLElement>(".workspace-takeover-wsl");
  if (ssh) ssh.hidden = transport !== "ssh";
  if (wsl) wsl.hidden = transport !== "wsl";
  dialog?.querySelectorAll<HTMLInputElement>('[name="ssh_host"], [name="ssh_user"]').forEach(input => {
    input.required = transport === "ssh";
  });
}

function openWorkspaceTakeoverDialog(workspace: Workspace): void {
  const dialog = document.querySelector<HTMLDialogElement>("#workspace-takeover-dialog");
  if (!dialog) return;
  const set = (name: string, value: string) => {
    const input = dialog.querySelector<HTMLInputElement | HTMLSelectElement>(`[name="${name}"]`);
    if (input) input.value = value;
  };
  set("source_id", workspace.id);
  set("name", `${workspace.name} (本机)`);
  set("transport", workspace.transport || "native");
  set("root_path", "");
  set("ssh_host", workspace.ssh_host || "");
  set("ssh_user", workspace.ssh_user || "");
  set("ssh_port", String(workspace.ssh_port || 22));
  set("ssh_auth", workspace.ssh_auth || "auto");
  set("distro", workspace.distro || "");
  set("ssh_password", "");
  const reuse = dialog.querySelector<HTMLInputElement>('[name="reuse_remote_credential"]');
  if (reuse) {
    reuse.checked = Boolean(workspace.ssh_password_configured);
    reuse.closest<HTMLElement>("label")!.hidden = !workspace.ssh_password_configured;
  }
  syncWorkspaceTakeoverFields();
  dialog.showModal();
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
  const items = state.workspaces.filter(item => item.project_id === projectID && !item.read_only);
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
  syncTaskSkills();
  syncTaskBackend();
}

function availableTaskSkills(projectID: string): Skill[] {
  return state.skills.filter(item => item.enabled && item.status === "active" && (item.scope === "global" || item.project_id === projectID || item.bound_project_id === projectID));
}

function taskSkillOptions(projectID: string, selected: string[] = []): string {
  const items = availableTaskSkills(projectID);
  return items.map(item => `<label class="task-skill-option"><input type="checkbox" name="skill_ids" value="${item.id}" ${selected.includes(item.id) ? "checked" : ""}><span><strong>${escapeHTML(item.name)}</strong><small>${escapeHTML(item.scope)} \u00b7 ${escapeHTML(item.description || "\u65e0\u63cf\u8ff0")}</small></span></label>`).join("") || `<div class="field-help">\u5f53\u524d Project \u6ca1\u6709\u53ef\u7528 Skill\uff0cTask \u4e0d\u4f1a\u6ce8\u5165\u4efb\u4f55 Skill \u8def\u5f84\u3002</div>`;
}

function syncTaskSkills(): void {
  const projectID = String((document.querySelector<HTMLSelectElement>("#task-project"))?.value || "");
  const container = document.querySelector<HTMLElement>("#task-skill-options");
  if (!container) return;
  const selected = [...container.querySelectorAll<HTMLInputElement>('input[name="skill_ids"]:checked')].map(input => input.value);
  container.innerHTML = taskSkillOptions(projectID, selected);
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
  const agentAPIMode = document.querySelector<HTMLSelectElement>("#ws-agent-api-mode");
  const agentAPIURL = document.querySelector<HTMLInputElement>("#ws-agent-api-url");
  const agentAPIManual = document.querySelector<HTMLElement>(".workspace-agent-api-manual");
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
  const manualAgentAPI = agentAPIMode?.value === "manual";
  if (agentAPIManual) agentAPIManual.style.display = manualAgentAPI ? "" : "none";
  if (agentAPIURL) {
    agentAPIURL.disabled = !manualAgentAPI;
    agentAPIURL.required = manualAgentAPI;
  }
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
      state.loading = false;
      state.hydrating = true;
      render();
      await loadCoreData();
      state.hydrating = false;
      render();
      await restoreNavigationState();
      openGlobalEvents();
      await ensureViewData(state.view);
      prefetchSecondaryData();
    }
  } catch (error) {
    state.error = error instanceof Error ? error.message : String(error);
  } finally {
    state.loading = false;
    render();
  }
}

async function loadAll(): Promise<void> {
  await Promise.all([loadCoreData(true), ensureViewData(state.view, true)]);
}

interface ModelDetectionSession {
  providerID: string;
  jobID: string;
  authStyle: string;
  catalog: DetectedModel[];
  results: Map<string, DetectedModel>;
  selected: Set<string>;
  completed: number;
  total: number;
  anthropicBaseURL: string;
  status: "queued" | "running" | "cancelling" | "completed" | "cancelled" | "failed";
  source: EventSource;
  button: HTMLElement;
  originalButtonHTML: string;
}

let activeModelDetection: ModelDetectionSession | null = null;

function detectedModelRow(session: ModelDetectionSession, item: DetectedModel, ready: boolean): string {
  return `<div class="detected-item${ready ? "" : " pending"}" data-model="${escapeHTML(item.id)}"><input type="checkbox" class="detected-model" value="${escapeHTML(item.id)}" ${ready && session.selected.has(item.id) ? "checked" : ""} ${ready ? "" : "disabled"}><span><strong>${escapeHTML(item.id)}</strong>${item.max_input_tokens ? `<small>context ${Math.round(item.max_input_tokens / 1000)}K</small>` : ""}${ready ? `<span class="proto-badges">${protoBadges(item)}</span>${protoChecks(item)}` : `<span class="proto pending">等待检测</span>`}</span></div>`;
}

function applyModelDetectionFilter(root: HTMLElement, query: string): void {
  const normalized = query.trim().toLowerCase();
  root.querySelectorAll<HTMLElement>(".detected-item").forEach(row => {
    row.style.display = !normalized || String(row.dataset.model || "").toLowerCase().includes(normalized) ? "" : "none";
  });
}

function modelDetectionStatusText(session: ModelDetectionSession): string {
  const label = session.status === "completed" ? "检测完成" : session.status === "cancelled" ? "已停止" : session.status === "failed" ? "检测失败" : session.status === "cancelling" ? "正在停止" : session.catalog.length ? "正在检测协议能力" : "正在获取模型目录";
  return `${label} · ${session.completed}/${session.total || "?"}${session.authStyle ? `（认证：${session.authStyle}）` : ""}`;
}

function updateModelDetectionProgress(session: ModelDetectionSession): void {
  const root = document.querySelector<HTMLElement>("#model-detect-results");
  if (!root) return;
  const status = root.querySelector<HTMLElement>("#model-detect-progress");
  if (status) status.textContent = modelDetectionStatusText(session);
  const note = root.querySelector<HTMLElement>("#model-detect-anthropic");
  if (note) {
    note.hidden = !session.anthropicBaseURL;
    const value = note.querySelector<HTMLElement>("code");
    if (value) value.textContent = session.anthropicBaseURL;
  }
  const stop = root.querySelector<HTMLButtonElement>("#stop-model-detection");
  if (stop && session.status === "cancelling") {
    stop.disabled = true;
    stop.textContent = "停止中";
  }
  const add = root.querySelector<HTMLButtonElement>("#add-selected-models");
  if (add) add.disabled = session.results.size === 0;
}

function updateDetectedModelRow(session: ModelDetectionSession, modelID: string): void {
  const root = document.querySelector<HTMLElement>("#model-detect-results");
  const model = session.results.get(modelID);
  if (!root || !model) return;
  const row = [...root.querySelectorAll<HTMLElement>(".detected-item")].find(item => item.dataset.model === modelID);
  if (!row) {
    renderModelDetection(session);
    return;
  }
  row.outerHTML = detectedModelRow(session, model, true);
  applyModelDetectionFilter(root, root.querySelector<HTMLInputElement>("#detect-search")?.value || "");
  updateModelDetectionProgress(session);
}

function renderModelDetection(session: ModelDetectionSession): void {
  const root = document.querySelector<HTMLElement>("#model-detect-results");
  if (!root) return;
  const terminal = ["completed", "cancelled", "failed"].includes(session.status);
  const rows = session.catalog.map(item => detectedModelRow(session, session.results.get(item.id) || item, session.results.has(item.id))).join("");
  replaceRegionHTML(root, `<div class="detect-status" id="model-detect-progress">${escapeHTML(modelDetectionStatusText(session))}</div><div class="detect-status" id="model-detect-anthropic" ${session.anthropicBaseURL ? "" : "hidden"}>检测到 Anthropic 端点：<code>${escapeHTML(session.anthropicBaseURL)}</code></div><div class="detect-toolbar"><input type="search" id="detect-search" placeholder="搜索模型名称..."><button type="button" id="detect-select-all">全选</button><button type="button" id="detect-select-none">全不选</button>${terminal ? "" : `<button type="button" id="stop-model-detection" class="danger">停止检测</button>`}</div><div class="detected-list" data-ui-key="model-detection-list">${rows || `<div class="empty">正在读取模型目录…</div>`}</div><div class="dialog-actions"><button type="button" id="add-selected-models" class="primary" ${session.results.size ? "" : "disabled"}>添加已选模型</button></div>`);
  applyModelDetectionFilter(root, root.querySelector<HTMLInputElement>("#detect-search")?.value || "");
}

function finishModelDetection(session: ModelDetectionSession): void {
  session.source.close();
  session.button.disabled = false;
  session.button.innerHTML = session.originalButtonHTML;
  updateModelDetectionProgress(session);
  document.querySelector("#model-detect-results #stop-model-detection")?.remove();
  if (activeModelDetection === session) activeModelDetection = null;
}

async function startModelDetection(providerID: string, button: HTMLElement, results: HTMLElement): Promise<void> {
  if (activeModelDetection) return;
  const originalButtonHTML = button.innerHTML;
  button.disabled = true;
  button.innerHTML = `${icon("spinner", true)}<span>读取目录中</span>`;
  results.innerHTML = `<div class="detect-status">正在获取模型目录…</div>`;
  try {
    const created = await api.createModelDetectionJob(providerID);
    const source = new EventSource(api.modelDetectionEventsURL(providerID, created.job.id));
    const session: ModelDetectionSession = {
      providerID, jobID: created.job.id, authStyle: "", catalog: [], results: new Map(), selected: new Set(), completed: 0, total: 0,
      anthropicBaseURL: "", status: "queued", source, button, originalButtonHTML,
    };
    activeModelDetection = session;
    renderModelDetection(session);
    results.addEventListener("input", event => {
      if (!(event.target instanceof HTMLInputElement) || event.target.id !== "detect-search") return;
      applyModelDetectionFilter(results, event.target.value);
    });
    results.addEventListener("change", event => {
      if (!(event.target instanceof HTMLInputElement) || !event.target.classList.contains("detected-model")) return;
      if (event.target.checked) session.selected.add(event.target.value);
      else session.selected.delete(event.target.value);
    });
    results.addEventListener("click", event => {
      const target = event.target as HTMLElement;
      if (target.closest("#stop-model-detection")) {
        session.status = "cancelling";
        updateModelDetectionProgress(session);
        void api.cancelModelDetectionJob(session.providerID, session.jobID).catch(error => setMessage("error", error instanceof Error ? error.message : String(error)));
        return;
      }
      if (target.closest("#detect-select-all")) {
        results.querySelectorAll<HTMLInputElement>(".detected-item:not([style*='display: none']) .detected-model:not(:disabled)").forEach(box => { box.checked = true; session.selected.add(box.value); });
        return;
      }
      if (target.closest("#detect-select-none")) {
        results.querySelectorAll<HTMLInputElement>(".detected-model").forEach(box => { box.checked = false; session.selected.delete(box.value); });
        return;
      }
      if (!target.closest("#add-selected-models")) return;
      const picks = [...results.querySelectorAll<HTMLInputElement>(".detected-model:checked")].map(input => {
        const row = input.closest(".detected-item");
        const wireAPIs = [...(row?.querySelectorAll<HTMLInputElement>(".proto-checks input:checked") || [])].map(item => item.value);
        const detected = session.results.get(input.value);
        return {id: input.value, wire_apis: wireAPIs.length ? wireAPIs : ["responses"], max_input_tokens: detected?.max_input_tokens || 0, max_output_tokens: detected?.max_output_tokens || 0};
      });
      if (!picks.length) {
        setMessage("error", "请至少选择一个已检测模型");
        return;
      }
      const addButton = results.querySelector<HTMLElement>("#add-selected-models");
      void runWithFeedback(addButton, "添加中", async () => {
        const result = await api.addModels({provider_id: providerID, models: picks, anthropic_base_url: session.anthropicBaseURL});
        document.querySelector<HTMLDialogElement>("#model-dialog")?.close();
        setMessage("notice", `已添加 ${(result.models || []).length} 个模型${result.skipped ? `，跳过 ${result.skipped} 个已存在` : ""}`);
        await ensureViewData("models", true);
        render();
      });
    });
    source.addEventListener("catalog", event => {
      const data = JSON.parse((event as MessageEvent).data) as {models?: DetectedModel[]; total?: number; auth_style?: string};
      session.catalog = data.models || [];
      session.total = data.total || session.catalog.length;
      session.authStyle = data.auth_style || "";
      session.status = "running";
      button.innerHTML = `${icon("spinner", true)}<span>检测中 ${session.completed}/${session.total}</span>`;
      renderModelDetection(session);
    });
    source.addEventListener("result", event => {
      const data = JSON.parse((event as MessageEvent).data) as {model: DetectedModel; completed: number; total: number; anthropic_base_url?: string};
      session.results.set(data.model.id, data.model);
      session.selected.add(data.model.id);
      session.completed = data.completed;
      session.total = data.total;
      if (data.anthropic_base_url) session.anthropicBaseURL = data.anthropic_base_url;
      button.innerHTML = `${icon("spinner", true)}<span>检测中 ${session.completed}/${session.total}</span>`;
      updateDetectedModelRow(session, data.model.id);
    });
    source.addEventListener("progress", event => {
      const data = JSON.parse((event as MessageEvent).data) as {status?: ModelDetectionSession["status"]; completed?: number; total?: number};
      if (data.status) session.status = data.status;
      session.completed = data.completed ?? session.completed;
      session.total = data.total ?? session.total;
      updateModelDetectionProgress(session);
    });
    source.addEventListener("done", event => {
      const data = JSON.parse((event as MessageEvent).data) as {status: ModelDetectionSession["status"]; completed: number; total: number; anthropic_base_url?: string};
      session.status = data.status;
      session.completed = data.completed;
      session.total = data.total;
      if (data.anthropic_base_url) session.anthropicBaseURL = data.anthropic_base_url;
      finishModelDetection(session);
    });
    source.addEventListener("error", event => {
      if (event instanceof MessageEvent) {
        const data = JSON.parse(event.data || "{}") as {message?: string};
        session.status = "failed";
        if (data.message) setMessage("error", data.message);
        return;
      }
      if (!["completed", "cancelled", "failed"].includes(session.status)) {
        session.status = "failed";
        setMessage("error", "模型检测连接中断，可重新检测");
        finishModelDetection(session);
      }
    });
  } catch (error) {
    button.disabled = false;
    button.innerHTML = originalButtonHTML;
    results.innerHTML = `<div class="detect-status">检测启动失败：${escapeHTML(error instanceof Error ? error.message : String(error))}</div>`;
  }
}

function forgetProject(projectID: string): void {
  if (state.selectedProject?.id === projectID) state.selectedProject = null;
  state.projects = state.projects.filter(item => item.id !== projectID);
  state.workspaces = state.workspaces.filter(item => item.project_id !== projectID);
  state.tasks = state.tasks.filter(item => item.project_id !== projectID);
}

async function purgeArchivedChannelProject(project: Project): Promise<boolean> {
  if (!project.channel_instance_id) throw new Error("归档 Project 缺少渠道实例关联，无法安全永久删除。");
  const revision = state.channelInstances.find(item => item.id === project.channel_instance_id)?.revision || 0;
  const result = await purgeRetiredChannelInstance(project.channel_instance_id, revision);
  if (result === "name_mismatch") {
    setMessage("error", "输入的实例名不匹配，未执行永久删除。");
    return false;
  }
  if (result !== "purged") return false;
  forgetProject(project.id);
  state.channelInstances = state.channelInstances.filter(item => item.id !== project.channel_instance_id);
  setMessage("notice", "渠道归档及其本地宿主资源已永久删除");
  return true;
}

async function loadCoreData(force = false): Promise<void> {
  await Promise.all([
    loadResource("projects", force, async () => {
      const result = await api.projects({limit: initialListPageSize});
      state.projects = result.projects || [];
      listPages.projects = {cursor: result.next_cursor || "", hasMore: Boolean(result.has_more), loadingMore: false};
    }),
    loadResource("workspaces", force, async () => { state.workspaces = (await api.workspaces()).workspaces || []; }),
    loadResource("tasks", force, async () => {
      const result = await api.tasks("", {limit: initialListPageSize});
      state.tasks = result.tasks || [];
      listPages.tasks = {cursor: result.next_cursor || "", hasMore: Boolean(result.has_more), loadingMore: false};
    }),
  ]);
  void loadResource("system", force, async () => {
    const system = await api.system();
    if (system?.system) state.system = system.system;
  });
}

async function loadKnowledgeData(force = true): Promise<void> {
  await loadResource("knowledge", force, async () => {
    const [knowledge, libraries] = await Promise.all([api.knowledge("", "", "", {limit: initialListPageSize, summary: true}), api.knowledgeLibraries()]);
    state.knowledge = knowledge.knowledge || [];
    state.knowledgeLibraries = libraries.libraries || [];
    state.knowledgeProposals = knowledge.proposals || [];
    state.knowledgeReviewSettings = knowledge.review_settings || {auto_approve: false};
    listPages.knowledge = {cursor: knowledge.next_cursor || "", hasMore: Boolean(knowledge.has_more), loadingMore: false};
  });
}

async function loadNextPage(kind: "projects" | "tasks" | "knowledge"): Promise<void> {
  const page = listPages[kind];
  if (!page.hasMore || !page.cursor || page.loadingMore) return;
  page.loadingMore = true;
  render();
  try {
    if (kind === "projects") {
      const result = await api.projects({limit: initialListPageSize, cursor: page.cursor});
      state.projects = mergeByID(state.projects, result.projects || []);
      page.cursor = result.next_cursor || "";
      page.hasMore = Boolean(result.has_more);
    } else if (kind === "tasks") {
      const result = await api.tasks("", {limit: initialListPageSize, cursor: page.cursor});
      state.tasks = mergeByID(state.tasks, result.tasks || []);
      page.cursor = result.next_cursor || "";
      page.hasMore = Boolean(result.has_more);
    } else {
      const result = await api.knowledge("", "", "", {limit: initialListPageSize, cursor: page.cursor, summary: true});
      state.knowledge = mergeByID(state.knowledge, result.knowledge || []);
      state.knowledgeProposals = result.proposals || state.knowledgeProposals;
      page.cursor = result.next_cursor || "";
      page.hasMore = Boolean(result.has_more);
    }
  } finally {
    page.loadingMore = false;
    render();
  }
}

function loadMoreHTML(kind: "projects" | "tasks" | "knowledge"): string {
  const page = listPages[kind];
  if (!page.hasMore) return "";
  return `<div class="list-load-more"><button type="button" data-load-more="${kind}" ${page.loadingMore ? "disabled" : ""}>${page.loadingMore ? `${icon("spinner", true)}加载中…` : "加载更多"}</button></div>`;
}

async function ensureViewData(view: View, force = false): Promise<void> {
  const jobs: Promise<void>[] = [];
  if (["projects", "tasks", "channels", "knowledge"].includes(view)) jobs.push(loadCoreData(force));
  if (["models", "tasks", "channels"].includes(view)) {
    jobs.push(loadResource("providers", force, async () => { state.providers = (await api.providers()).providers || []; }));
    jobs.push(loadResource("models", force, async () => { state.models = (await api.models()).models || []; }));
    jobs.push(loadResource("accounts", force, async () => { state.codexAccounts = (await api.codexAccounts()).accounts || []; }));
  }
  if (view === "models") jobs.push(loadResource("env", force, async () => { state.envGroups = (await api.envGroups()).env_groups || []; }));
  if (view === "tasks") jobs.push(loadResource("skills", force, async () => { state.skills = (await api.skills()).skills || []; }));
  if (view === "channels") {
    jobs.push(loadResource("channels", force, async () => {
      const [providers, instances] = await Promise.all([api.channelProviders(), api.channelInstances()]);
      state.channelProviders = providers.providers || [];
      state.channelInstances = instances.instances || [];
    }));
    jobs.push(loadKnowledgeData(force));
  }
  if (view === "knowledge") jobs.push(loadKnowledgeData(force));
  if (view === "proxy") jobs.push(loadResource("proxy", force, async () => {
    const result = await api.proxySettings();
    if (result.proxy) state.proxySettings = result.proxy;
    if (result.managed) state.managedProxy = result.managed;
  }));
  if (view === "advanced") jobs.push(loadResource("advanced", force, async () => {
    const [security, agentAPI, backend] = await Promise.all([api.securitySettings(), api.agentAPISettings(), api.backendSettings()]);
    if (security.security) state.securitySettings = security.security;
    if (agentAPI.agent_api) state.agentAPISettings = agentAPI.agent_api;
    if (backend.backend) state.backendSettings = backend.backend;
  }));
  if (view === "sync") jobs.push(loadResource("sync", force, async () => {
    const [settings, status, conflicts, preview] = await Promise.all([api.syncSettings(), api.syncStatus(), api.syncConflicts(), api.syncPreview()]);
    state.syncSettings = settings.sync;
    state.syncState = status.state;
    state.syncPending = status.pending || 0;
    state.syncRun = status.run || state.syncRun;
    state.syncConflicts = conflicts.conflicts || [];
    state.syncPreview = preview.preview || state.syncPreview;
  }));
  if (view === "prompts") jobs.push(loadResource("prompts", force, loadPromptCatalog));
  await Promise.all(jobs);
}

function prefetchSecondaryData(): void {
  window.setTimeout(() => {
    if (!state.auth?.authenticated) return;
    void ensureViewData("models").then(() => ensureViewData("channels")).then(() => ensureViewData("knowledge"));
  }, 250);
}
function persistNavigationState(): void {
  saveNavigationSnapshot({
    view: state.view, projectID: state.selectedProject?.id, taskID: state.selectedTask?.task.id,
    agentID: state.selectedTaskAgent, draft: state.taskDraft, drafts: state.taskDrafts,
  });
}
async function restoreNavigationState(): Promise<void> {
  const saved = loadNavigationSnapshot();
  if (["projects", "channels", "models", "tasks", "knowledge", "prompts", "proxy", "sync", "advanced"].includes(saved.view || "")) state.view = saved.view as View;
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

function authErrorMessage(error: unknown, fallback: string): string {
  const typed = error as Error & {code?: string};
  if (typed.code === "recovery_failed") return "Setup Token 或账号不正确";
  if (typed.code === "invalid_current_password") return "当前密码不正确";
  if (typed.code === "invalid_new_password") return "新密码至少需要 10 个字符";
  if (typed.code === "auth_rate_limited") return "尝试次数过多，请稍后再试";
  return error instanceof Error ? error.message : fallback;
}

function persistTaskToolLayout(): void {
  try {
    localStorage.setItem(taskToolLayoutKey, JSON.stringify({mode: state.taskToolMode, width: state.taskToolWidth}));
  } catch {
    // Layout persistence is best-effort in restricted WebViews.
  }
}

function applyTaskToolLayout(): void {
  const grid = document.querySelector<HTMLElement>(".task-grid");
  const panel = document.querySelector<HTMLElement>(".task-tool-panel");
  if (!grid || !panel) return;
  const split = state.taskToolMode === "split";
  grid.classList.toggle("task-tool-split", split);
  grid.classList.toggle("task-tool-fullscreen", !split);
  grid.style.setProperty("--task-tool-width", `${state.taskToolWidth}%`);
  panel.classList.toggle("split", split);
  panel.classList.toggle("fullscreen", !split);
  const resizer = panel.querySelector<HTMLElement>("#task-tool-resizer");
  resizer?.setAttribute("aria-valuenow", String(state.taskToolWidth));
  const toggle = panel.querySelector<HTMLButtonElement>("#toggle-task-tool-mode");
  if (toggle) {
    const label = split ? "全屏" : "小窗";
    toggle.title = `切换为${label}`;
    toggle.setAttribute("aria-label", `切换为${label}`);
    toggle.innerHTML = `${icon(split ? "expand" : "panel")}<span>${label}</span>`;
  }
}

function resizeTaskTool(clientX: number, grid: HTMLElement): void {
  const rect = grid.getBoundingClientRect();
  if (rect.width <= 0) return;
  const minTool = Math.min(320, rect.width * .4);
  const minConversation = Math.min(360, rect.width * .45);
  const minWidth = Math.max(rect.width * .3, minTool);
  const maxWidth = Math.min(rect.width * .7, rect.width - minConversation);
  const width = Math.min(maxWidth, Math.max(minWidth, rect.right - clientX));
  state.taskToolWidth = normalizeTaskToolWidth(width / rect.width * 100);
  applyTaskToolLayout();
}

function bindTaskToolLayout(): void {
  const grid = document.querySelector<HTMLElement>(".task-grid");
  const toggle = document.querySelector<HTMLButtonElement>("#toggle-task-tool-mode");
  const resizer = document.querySelector<HTMLElement>("#task-tool-resizer");
  toggle?.addEventListener("click", () => {
    state.taskToolMode = state.taskToolMode === "split" ? "fullscreen" : "split";
    persistTaskToolLayout();
    applyTaskToolLayout();
  });
  if (!grid || !resizer) return;
  resizer.setAttribute("aria-valuenow", String(state.taskToolWidth));
  resizer.addEventListener("dblclick", () => {
    state.taskToolWidth = TASK_TOOL_DEFAULT_WIDTH;
    persistTaskToolLayout();
    applyTaskToolLayout();
  });
  resizer.addEventListener("keydown", event => {
    if (event.key !== "ArrowLeft" && event.key !== "ArrowRight" && event.key !== "Home") return;
    event.preventDefault();
    state.taskToolWidth = event.key === "Home"
      ? TASK_TOOL_DEFAULT_WIDTH
      : normalizeTaskToolWidth(state.taskToolWidth + (event.key === "ArrowLeft" ? 2 : -2));
    persistTaskToolLayout();
    applyTaskToolLayout();
  });
  resizer.addEventListener("pointerdown", event => {
    if (window.matchMedia("(max-width: 760px)").matches || state.taskToolMode !== "split") return;
    event.preventDefault();
    resizer.setPointerCapture?.(event.pointerId);
    document.body.classList.add("task-tool-resizing");
    const move = (moveEvent: PointerEvent) => resizeTaskTool(moveEvent.clientX, grid);
    const finish = () => {
      document.body.classList.remove("task-tool-resizing");
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", finish);
      window.removeEventListener("pointercancel", finish);
      persistTaskToolLayout();
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", finish);
    window.addEventListener("pointercancel", finish);
  });
}

function selectedConversationCategories(): ConversationCategory[] {
  return (Object.keys(state.taskCategories) as ConversationCategory[]).filter(category => state.taskCategories[category]);
}

async function openTask(taskID: string): Promise<void> {
  const detail = await api.task(taskID);
  const categories: ConversationCategory[] = detail.task.read_only ? ["chat", "update", "error"] : selectedConversationCategories();
  if (detail.task.read_only) state.taskCategories.tool = false;
  const [page, context] = await Promise.all([
    api.agentConversation(taskID, "main", {limit: 50, categories}),
    api.agentContext(taskID, "main"),
  ]);
  detail.agents ||= [];
  state.selectedTask = detail;
  state.selectedTaskAgent = "main";
  state.taskConversation = page.conversation.items || [];
  state.taskConversationHasMore = page.conversation.has_more;
  state.taskConversationBefore = page.conversation.next_before || 0;
  state.taskConversationLatest = page.conversation.latest_sequence || 0;
  state.taskEventCursor = detail.event_cursor || 0;
  state.taskContext = context;
  state.taskTool = "";
  state.taskDraft = "";
  state.taskDrafts = {};
  state.taskAttachmentDrafts = {};
  loadingOlderConversation = false;
  requestConversationBottom();
  if (detail.task.read_only) closeEvents();
  else openEvents(taskID, state.taskEventCursor);
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
  state.taskContext = await api.agentContext(state.selectedTask.task.id, agentID);
  requestConversationBottom();
}

function syncAgentConfigFields(): void {
  const inherited = Boolean(document.querySelector<HTMLInputElement>('[name="inherit_main"]')?.checked);
  document.querySelectorAll<HTMLElement>(".agent-runtime-fields").forEach(element => {
    element.classList.toggle("disabled", inherited);
    element.querySelectorAll<HTMLInputElement | HTMLSelectElement>("input,select").forEach(input => {
      input.disabled = inherited;
    });
  });
  if (!inherited) syncRuntimeFields("agent-config", state.models, state.codexAccounts, false);
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
    hardware: detail.hardware,
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
  if (state.taskContext) {
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
  const recovery = !register && authRecoveryOpen;
  return `<div class="login-layout">
    <aside class="login-brand">
      <div class="brand-lockup"><span class="brand-mark">A</span><strong>AHA</strong></div>
      <p>以项目为基础，以任务为核心，通过知识闭环持续成长的个人 AI 工作流。</p>
      <div class="secure-note">${icon("shield")}<span>公网访问 · 所有业务操作均需认证</span></div>
    </aside>
    <main class="login-main">
      ${recovery ? `<form id="password-recovery-form" class="auth-form">
        <h1>找回密码</h1>
        <p>使用 AHA2 数据目录中的 setup-token 验证本机所有权；重置后其他登录会话将失效。</p>
        <label>账号<input name="username" value="${escapeHTML(state.auth?.username || "owner")}" autocomplete="username" required></label>
        <label>Setup Token<input name="setup_token" type="password" autocomplete="one-time-code" required></label>
        <label>新密码<input name="new_password" type="password" autocomplete="new-password" minlength="10" required></label>
        <label>确认新密码<input name="confirm_password" type="password" autocomplete="new-password" minlength="10" required></label>
        <button class="primary full" type="submit">重置并登录</button>
        <button id="back-to-login" class="auth-link" type="button">返回登录</button>
        ${state.error ? `<div class="form-error">${escapeHTML(state.error)}</div>` : ""}
      </form>` : `<form id="auth-form" class="auth-form">
        <h1>${register ? "初始化 Owner" : "登录"}</h1>
        <p>${register ? "创建唯一 Owner 后将关闭公开注册" : "进入项目、任务与知识工作区"}</p>
        ${register ? `<label>Setup Token<input name="setup_token" autocomplete="one-time-code" required></label>` : ""}
        <label>账号<input name="username" value="owner" autocomplete="username" required></label>
        <label>密码<input name="password" type="password" autocomplete="${register ? "new-password" : "current-password"}" minlength="10" required></label>
        <button class="primary full" type="submit">${register ? "创建 Owner" : "登录"}</button>
        ${register ? "" : `<button id="forgot-password" class="auth-link" type="button">忘记密码？</button>`}
        ${state.error ? `<div class="form-error">${escapeHTML(state.error)}</div>` : ""}
      </form>`}
    </main>
  </div>`;
}

function shell(content: string): string {
  const nav = [
    ["projects", "projects", "项目"],
    ["tasks", "tasks", "任务"],
    ["channels", "bot", "渠道"],
    ["knowledge", "knowledge", "知识库"],
    ["models", "model", "模型"],
    ["proxy", "proxy", "代理"],
  ] as const;
  return `<div class="app-shell">
    <aside class="sidebar">
      <div class="brand-lockup"><span class="brand-mark">A</span><div><strong>AHA</strong><small>个人 AI 工作流</small></div></div>
      <nav>${nav.map(([view, glyph, label]) => `<button data-view="${view}" class="${state.view === view ? "active" : ""}">${icon(glyph)}<span>${label}</span></button>`).join("")}</nav>
      <div class="system-meta"><span>AHA2 ${escapeHTML(state.system.version || "dev")}</span><span id="system-uptime">${systemUptimeText()}</span></div>
      <div class="owner-block"><button id="owner-avatar" class="avatar" type="button" title="Owner">O</button><div><strong>${escapeHTML(state.auth?.username || "Owner")}</strong><small>已安全登录</small></div><button id="logout" class="icon-button" title="退出">${icon("logout")}</button></div>
    </aside>
    <header class="mobile-header"><div class="brand-lockup"><span class="brand-mark">A</span><strong>AHA</strong></div><button id="mobile-context" class="icon-button">${icon("menu")}</button></header>
    <main class="workspace">${resourceStatusHTML()}${banner()}${content}</main>
    <nav class="bottom-nav">${nav.map(([view, glyph, label]) => `<button data-view="${view}" class="${state.view === view ? "active" : ""}">${icon(glyph)}<span>${label}</span></button>`).join("")}</nav>
  </div>`;
}

function banner(): string {
  const messages = `${state.error ? `<div class="banner error">${escapeHTML(state.error)}</div>` : ""}${state.notice ? `<div class="banner success">${escapeHTML(state.notice)}</div>` : ""}`;
  return messages ? `<div class="banner-stack">${messages}</div>` : "";
}

function resourceStatusHTML(): string {
  const resources = resourcesForView(state.view);
  const failed = resources.filter(key => resourceStates[key].error);
  if (failed.length) return `<div class="app-hydrating resource-error" role="alert"><span>部分数据加载失败，不影响其他区域。</span><button type="button" id="retry-view-data">重试</button></div>`;
  return "";
}

function systemUptimeText(now = Date.now()): string {
  const started = Date.parse(state.system.started_at || "");
  if (!Number.isFinite(started)) return "在线时间 —";
  const seconds = Math.max(0, Math.floor((now - started) / 1000));
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  return `在线 ${days ? `${days}天 ` : ""}${hours ? `${hours}小时 ` : ""}${minutes}分`;
}

function updateSystemUptime(): void {
  const element = document.querySelector<HTMLElement>("#system-uptime");
  if (element) element.textContent = systemUptimeText();
}

function pageHead(title: string, description: string, actions = ""): string {
  return `<header class="page-head"><div><h1>${title}</h1><p>${description}</p></div><div class="actions">${actions}</div></header>`;
}

function projectsView(): string {
  const rows = state.projects.map(project => {
    const workspaces = state.workspaces.filter(item => item.project_id === project.id);
    const tasks = state.tasks.filter(item => item.project_id === project.id);
    const archived = project.channel_retired === true;
    return `<article class="list-row project-row" data-project="${project.id}">
      <div class="item-title"><span class="square-icon">${icon("projects")}</span><div><strong>${escapeHTML(project.name)}</strong><small>${projectTypeLabel(project.project_type)}${archived ? " · 渠道归档" : ""}</small></div><span class="row-actions">${archived ? `<span class="status warn">已归档</span><button type="button" data-delete-project="${project.id}" class="icon-button danger project-del" title="永久删除归档">${icon("close")}</button>` : `<button type="button" data-edit-project="${project.id}" class="icon-button" title="编辑项目">${icon("edit")}</button><button type="button" data-delete-project="${project.id}" class="icon-button project-del" title="删除项目">${icon("close")}</button>`}</span></div>
      <div class="chips">${workspaces.map(item => `<span>${item.locality === "remote" ? icon("server") : icon("monitor")}${escapeHTML(item.name)}</span>`).join("") || "<span>暂无 Workspace</span>"}</div>
      <div><strong>${tasks.length}</strong><small>任务</small></div>
      <div><strong>${workspaces.filter(item => item.health === "ready").length}/${workspaces.length}</strong><small>Workspace Ready</small></div>
    </article>`;
  }).join("");
  return shell(`<section class="page">
    ${pageHead("项目", "管理逻辑 Project，点开项目配置其 Workspace。", `<button data-dialog="project">${icon("plus")}新建项目</button>`)}
    <div class="metrics"><div><small>项目</small><strong>${state.projects.length}</strong></div><div><small>Workspace</small><strong>${state.workspaces.length}</strong></div><div><small>活动任务</small><strong>${state.tasks.filter(item => item.status === "active").length}</strong></div><div><small>知识</small><strong>${state.knowledge.length}</strong></div></div>
    <div class="panel"><div class="panel-head"><strong>所有项目</strong><button id="refresh">${icon("refresh")}刷新</button></div>${rows || `<div class="empty">创建第一个项目，随后点开配置 Workspace。</div>`}${loadMoreHTML("projects")}</div>
    ${projectDialog()}
  </section>`);
}

function projectDetailView(project: Project): string {
  const workspaces = state.workspaces.filter(item => item.project_id === project.id);
  const tasks = state.tasks.filter(item => item.project_id === project.id);
  const archived = project.channel_retired === true;
  const rows = workspaces.map(item => `<article class="list-row ws-row ${item.read_only ? "read-only" : ""}">
    <div class="item-title">${item.locality === "remote" ? icon("server") : icon("monitor")}<div><strong>${escapeHTML(item.name)}</strong><small>${escapeHTML(item.root_path)}</small></div></div>
    <div class="ws-meta"><small>${escapeHTML(item.transport)}${item.distro ? ` · ${escapeHTML(item.distro)}` : ""}</small><strong>${item.channel_retired ? "渠道归档只读" : item.read_only ? `只读 · ${escapeHTML(item.owner_device_id || "其他设备")}` : `本机 · ${escapeHTML(item.owner_device_id || "待首次同步绑定")}`}</strong></div>
    ${renderWorkspaceDetection(item)}
    ${archived ? `<span class="status warn">归档只读</span>` : item.read_only ? `<span class="row-actions workspace-remote-actions"><button type="button" data-takeover-workspace="${item.id}">${icon("copy")}接管到本机</button><button type="button" data-retire-remote-workspace="${item.id}" class="icon-button danger" title="移除孤立 Workspace">${icon("close")}</button></span><span class="status warn">远端只读</span>` : `<button data-detect="${item.id}">${icon("refresh")}测试连接</button><span class="row-actions"><button type="button" data-edit-workspace="${item.id}" class="icon-button" title="编辑 Workspace">${icon("edit")}</button><button type="button" data-delete-workspace="${item.id}" class="icon-button" title="删除 Workspace">${icon("close")}</button></span>`}
  </article>`).join("");
  return shell(`<section class="page">
    <header class="page-head"><div><button id="back-projects" class="back-link">← 返回项目列表</button><h1>${escapeHTML(project.name)}${archived ? ` <span class="status warn">已归档</span>` : ""}</h1><p>${projectTypeLabel(project.project_type)}${project.repository_identity ? ` · ${escapeHTML(project.repository_identity)}` : ""}${project.default_branch ? ` · 默认分支 ${escapeHTML(project.default_branch)}` : ""}${archived ? " · 渠道宿主历史只读" : ""}</p></div><div class="actions">${archived ? `<button type="button" data-channel-project-export="${escapeHTML(project.channel_instance_id || "")}">导出归档</button><button id="delete-project" class="danger">${icon("close")}永久删除</button>` : `<button data-dialog="workspace">${icon("plus")}添加 Workspace</button><button type="button" data-edit-project-detail="${project.id}" class="icon-button" title="编辑项目">${icon("edit")}</button><button id="delete-project" class="danger">${icon("close")}删除项目</button>`}</div></header>
    <div class="metrics"><div><small>Workspace</small><strong>${workspaces.length}</strong></div><div><small>任务</small><strong>${tasks.length}</strong></div><div><small>Ready</small><strong>${workspaces.filter(item => item.health === "ready").length}</strong></div></div>
    <div class="panel"><div class="panel-head"><strong>Workspaces</strong><span>“测试连接”会刷新 Backend 能力并验证 Workspace → AHA2 Agent API</span></div>${rows || `<div class="empty">尚无 Workspace，点击右上角添加。</div>`}</div>
    ${workspaceDialog()}${workspaceTakeoverDialog()}
  </section>`);
}

function projectDialog(): string {
  return `<dialog id="project-dialog"><form id="project-form" method="dialog"><div class="dialog-head"><h2>新建项目</h2><button type="button" data-close class="icon-button">${icon("close")}</button></div><input type="hidden" id="project-edit-id" name="project_edit_id" value=""><label>名称<input name="name" id="project-name" required></label><label>描述<textarea name="description" id="project-desc"></textarea></label><label>项目类型<select name="project_type" id="project-type"><option value="folder">普通文件夹</option><option value="git">Git 仓库</option></select></label><div class="git-fields" style="display:none"><label>Git Repository Identity<input name="repository_identity" id="project-repo" placeholder="可选：remote URL"></label><label>默认分支<input name="default_branch" id="project-branch" value="main"></label></div><div class="dialog-actions"><button type="button" data-close>取消</button><button class="primary" value="default">创建项目</button></div></form></dialog>`;
}

function workspaceDialog(): string {
  return `<dialog id="workspace-dialog"><form id="workspace-form" method="dialog">
    <div class="dialog-head"><h2>添加 Workspace</h2><button type="button" data-close class="icon-button">${icon("close")}</button></div>
    <input type="hidden" id="ws-edit-id" name="ws_edit_id" value="">
    <label>项目<select name="project_id" id="ws-project">${state.projects.filter(item => !item.channel_retired).map(item => `<option value="${item.id}" ${item.id === state.dialogProjectID ? "selected" : ""}>${escapeHTML(item.name)}</option>`).join("")}</select></label>
    <label>名称<input name="name" id="ws-name" value="本地开发" required></label>
    <div class="two"><label>位置<select name="locality" id="ws-locality"><option value="local">本地</option><option value="remote">远程</option></select></label><label>Transport<select name="transport" id="ws-transport"></select></label></div>
    <label>Root Path<input name="root_path" id="ws-root-path" placeholder="E:\project 或 /home/user/project" required></label>
    <div class="wsl-fields" style="display:none"><label>WSL Distro<select name="distro" id="ws-distro"></select></label><div class="field-help">Root Path 填 WSL 内的路径，如 /home/user/project</div></div>
    <div class="ssh-fields" style="display:none"><div class="two"><label>SSH Host<input name="ssh_host" placeholder="192.168.1.10"></label><label>SSH User<input name="ssh_user" placeholder="root"></label></div><div class="two"><label>SSH Port<input name="ssh_port" type="number" min="1" max="65535" value="22"></label><label>SSH 登录方式<select name="ssh_auth"><option value="auto">自动（有密码时优先密码）</option><option value="password">密码</option><option value="key">Key (~/.ssh)</option></select></label></div><label>登录密码<input name="ssh_password" type="password" autocomplete="new-password" placeholder="可选"></label><label class="workspace-clear-secret"><input name="clear_ssh_password" type="checkbox">清除已保存密码</label><div class="field-help">密码保存在 Secret Store；编辑时留空会保留原密码。</div></div>
    <fieldset class="workspace-agent-api-fields"><legend>Agent API 反向连接</legend><label>地址策略<select name="agent_api_mode" id="ws-agent-api-mode"><option value="auto">自动探测（推荐）</option><option value="global">继承全局默认</option><option value="manual">手动覆盖</option></select></label><label class="workspace-agent-api-manual">Agent API URL<input name="agent_api_url" id="ws-agent-api-url" type="url" placeholder="https://aha.example.com"></label><div class="field-help workspace-agent-api-result">保存后点击 Workspace 的“测试连接”，AHA2 会从该 Workspace 反向验证并保存有效地址。</div></fieldset>
    <div class="dialog-actions"><button type="button" data-close>取消</button><button class="primary" value="default">添加 Workspace</button></div>
  </form></dialog>`;
}

function modelsView(): string {
  const providers = state.providers.map(item => `<article class="list-row provider-row">
    <div class="item-title">${icon("server")}<div><strong>${escapeHTML(item.name)}</strong><small>${escapeHTML(item.base_url || item.id)}</small></div></div>
    <span class="status ${item.credential_configured ? "good" : "warn"}">${item.credential_configured ? "Key Ready" : "No Key"}</span>
    <span class="row-actions"><button type="button" data-edit-provider="${item.id}" class="icon-button" title="编辑 Provider">${icon("edit")}</button><button type="button" data-delete-provider="${item.id}" class="icon-button" title="删除 Provider">${icon("close")}</button></span>
  </article>`).join("");
  const models = state.models.map(item => `<tr><td><strong>${escapeHTML(item.display_name)}</strong><small>${escapeHTML(item.wire_model)}</small></td><td><strong>${escapeHTML(item.provider_name || item.provider_id)}</strong><small>${escapeHTML(item.provider_id)}</small></td><td>${escapeHTML(backendProtocolLabel(item))}</td><td>${item.context_window ? Math.round(item.context_window / 1000) + "K" : "-"}</td><td class="row-actions"><button type="button" data-edit-model="${item.id}" class="icon-button" title="编辑模型">${icon("edit")}</button><button type="button" data-delete-model="${item.id}" class="icon-button" title="删除模型">${icon("close")}</button></td></tr>`).join("");
  return shell(`<section class="page">
    ${pageHead("模型", "Models 仅管理 Provider / Env 模型；官方模型在创建 Task 时按账号选择。")}
    <div class="provider-layout">
      <div class="provider-sidebar">
        ${renderCodexAccounts(state.codexAccounts)}
        <section class="panel"><div class="panel-head"><strong>Providers</strong><button data-dialog="provider">${icon("plus")}添加 Provider</button></div>${providers || `<div class="empty">先添加 Provider（API Base URL + API Key）。</div>`}</section>
      </div>
      <section class="panel"><div class="panel-head"><strong>Models</strong><button data-dialog="model">${icon("plus")}添加模型</button></div><div class="table-wrap"><table class="models-table"><thead><tr><th>模型</th><th>Provider</th><th>Backend</th><th>Context</th><th></th></tr></thead><tbody>${models || `<tr><td colspan="5">选择 Provider 后添加模型。</td></tr>`}</tbody></table></div></section>
    </div>
    ${providerDialog()}${modelDialog()}${editModelDialog()}
  </section>`);
}

function advancedSettingsView(): string {
  const origin = state.securitySettings;
  const agentAPI = state.agentAPISettings;
	const backendSettings = state.backendSettings;
  return shell(`<section class="page advanced-settings-page">
    <header class="page-head"><div><h1>高级设置</h1><p>管理 Backend、Agent API、Owner 账号与本机恢复方式</p></div></header>
    <section class="advanced-tools-grid">
      <article class="panel advanced-tool-card"><div>${icon("bot")}<span><strong>提示词</strong><small>查看和维护 AHA2 的提示词模板</small></span></div><button type="button" data-view="prompts">进入提示词设置</button></article>
      <article class="panel advanced-tool-card"><div>${icon("sync")}<span><strong>同步</strong><small>配置设备同步、检查差异与冲突</small></span></div><button type="button" data-view="sync">进入同步设置</button></article>
    </section>
    <section class="panel account-security-panel agent-api-settings-panel">
      <div class="panel-head"><strong>Agent API 访问地址</strong><span>${escapeHTML(agentAPI.effective_url || "未配置")}</span></div>
      <form id="agent-api-settings-form">
        <label>全局默认 URL<input name="url" type="url" value="${escapeHTML(agentAPI.url || "")}" placeholder="留空继承启动地址 ${escapeHTML(agentAPI.startup_url || "")}"></label>
        <label class="security-setting-toggle"><input name="allow_insecure" type="checkbox" ${agentAPI.allow_insecure ? "checked" : ""}><span><strong>允许受信网络中的非 loopback HTTP</strong><small>公网和跨网络访问应使用 HTTPS；修改后 Workspace 需重新测试连接。</small></span></label>
        <div class="field-help">当前有效地址：<code>${escapeHTML(agentAPI.effective_url || "-")}</code>${agentAPI.startup_url ? ` · 启动默认：<code>${escapeHTML(agentAPI.startup_url)}</code>` : ""}</div>
        <div class="dialog-actions"><button class="primary" type="submit">保存 Agent API 设置</button></div>
      </form>
    </section>
    <section class="panel spaced account-security-panel backend-timeout-settings-panel">
      <div class="panel-head"><strong>Backend 超时</strong><span>Codex / Claude 统一生效</span></div>
      <form id="backend-settings-form">
			<div class="two">
				<label>无活动超时（分钟）<input name="idle_timeout_minutes" type="number" min="1" max="1440" step="1" value="${Math.round(backendSettings.idle_timeout_seconds / 60)}" required></label>
				<label>单次执行总时限（小时）<input name="turn_timeout_hours" type="number" min="0.0167" max="168" step="any" value="${Number((backendSettings.turn_timeout_seconds / 3600).toFixed(4))}" required></label>
			</div>
			<div class="field-help">无活动超时只在 Backend 持续没有输出时触发；总时限从本次执行启动开始计算。两项可以独立设置，保存后对后续执行生效。</div>
			<div class="dialog-actions"><button class="primary" type="submit">保存 Backend 设置</button></div>
		</form>
	</section>
    <section class="panel spaced account-security-panel">
      <div class="panel-head"><strong>修改密码</strong><span>${escapeHTML(state.auth?.username || "Owner")}</span></div>
      <form id="change-password-form">
        <label>当前密码<input name="current_password" type="password" autocomplete="current-password" required></label>
        <label>新密码<input name="new_password" type="password" autocomplete="new-password" minlength="10" required></label>
        <label>确认新密码<input name="confirm_password" type="password" autocomplete="new-password" minlength="10" required></label>
        <div class="dialog-actions"><button class="primary" type="submit">修改密码</button></div>
      </form>
    </section>
    <section class="panel spaced account-security-panel">
      <div class="panel-head"><strong>Origin 安全校验</strong><span>${origin.validate_origin ? "已启用" : "已关闭"}</span></div>
      <form id="origin-validation-form">
        <label class="security-setting-toggle"><input name="validate_origin" type="checkbox" ${origin.validate_origin ? "checked" : ""} ${origin.startup_override ? "disabled" : ""}><span><strong>校验浏览器请求 Origin</strong><small>建议保持启用。只有反向代理无法正确传递外部 Host 时才关闭。</small></span></label>
        <div class="security-warning ${origin.validate_origin ? "" : "active"}">${origin.startup_override ? `启动参数 <code>--allow-cross-origin</code> 已强制关闭校验；如需重新启用，请移除参数并重启 AHA2。` : `关闭后，登录、密码恢复、业务写入和硬件 WebSocket 将不再拒绝 Origin 与 Host 不一致的请求；CSRF Token 与登录认证仍然有效。`}</div>
        <div class="dialog-actions"><button class="primary" type="submit" ${origin.startup_override ? "disabled" : ""}>保存安全设置</button></div>
      </form>
    </section>
    <section class="panel spaced recovery-note"><div class="panel-head"><strong>密码恢复</strong></div><div><p>忘记密码时，可在登录页使用数据目录中的 <code>setup-token</code> 重置。重置成功会撤销其他登录会话。</p><p>默认位置：<code>C:\\ProgramData\\AHA2\\setup-token</code></p></div></section>
  </section>`);
}

function advancedSubview(content: string): string {
  return shell(`<div class="advanced-subview"><div class="advanced-subview-back"><button type="button" data-view="advanced">← 返回高级设置</button></div>${content}</div>`);
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

function workspaceTakeoverDialog(): string {
  return `<dialog id="workspace-takeover-dialog"><form id="workspace-takeover-form"><div class="dialog-head"><h2>接管只读 Workspace</h2><button type="button" data-close class="icon-button">${icon("close")}</button></div><input type="hidden" name="source_id"><p class="field-help">接管会创建新的本机 Workspace；远端镜像保持只读且不会被修改。</p><label>名称<input name="name" required></label><div class="two"><label>Transport<select name="transport"><option value="native">Native</option><option value="wsl">WSL</option><option value="ssh">SSH</option></select></label><label>本机 Root Path<input name="root_path" required placeholder="必须重新确认本机路径"></label></div><label class="workspace-takeover-wsl">WSL Distro<input name="distro" placeholder="例如 Ubuntu"></label><div class="workspace-takeover-ssh"><div class="two"><label>SSH Host<input name="ssh_host"></label><label>SSH User<input name="ssh_user"></label></div><div class="two"><label>SSH Port<input name="ssh_port" type="number" min="1" max="65535" value="22"></label><label>SSH Auth<select name="ssh_auth"><option value="auto">Auto</option><option value="password">Password</option><option value="key">Key</option></select></label></div><label class="reuse-remote-secret"><input name="reuse_remote_credential" type="checkbox">复用已同步的端到端加密 SSH 凭据</label><label>或输入新密码<input name="ssh_password" type="password" autocomplete="new-password"></label></div><div class="dialog-actions"><button type="button" data-close>取消</button><button class="primary" type="submit">创建本机副本</button></div></form></dialog>`;
}

const TASK_STATUS_FILTERS = ["draft", "active", "waiting_user", "preparing", "queued", "running", "completed", "failed", "blocked"];

function taskCardHtml(task: Task): string {
  const project = state.projects.find(item => item.id === task.project_id);
  const ws = state.workspaces.find(item => item.id === task.workspace_id);
  const archived = task.channel_retired === true || task.read_only_reason === "channel_retired";
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
      ${archived ? `<span class="status warn">归档只读</span>` : task.read_only ? `<button type="button" data-retire-remote-task="${task.id}" class="icon-button danger" title="移除孤立只读任务">${icon("close")}</button><span class="status warn" title="所属设备：${escapeHTML(task.owner_device_id || "未知")}">只读</span>` : `<button type="button" data-edit-task-title="${task.id}" class="icon-button" title="编辑标题">${icon("edit")}</button><button type="button" data-delete-task="${task.id}" class="icon-button" title="删除任务">${icon("close")}</button>`}
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
    <div class="task-list">${rows || `<div class="empty">${state.tasks.length ? "没有符合条件的任务" : "创建第一个任务开始执行。"}</div>`}${loadMoreHTML("tasks")}</div>
    ${taskDialog()}
  </section>`);
}

function taskDialog(): string {
  const projectID = state.projects.find(project => !project.channel_retired)?.id || "";
  return taskDialogBase().replace(
    '<div id="task-git-isolation">',
    `<div class="two"><label>Knowledge<select name="knowledge_policy"><option value="inherit">\u7ee7\u627f Project</option><option value="enabled">\u5f00\u542f</option><option value="disabled">\u5173\u95ed</option></select></label><label class="proxy-toggle"><input name="proxy_enabled" type="checkbox">Backend \u4f7f\u7528\u5171\u4eab\u4ee3\u7406</label></div><fieldset class="task-skill-picker"><legend>Task Skills</legend><p>\u4ec5\u5c06\u9009\u4e2d Skill \u7684\u8def\u5f84\u5199\u5165 Context\uff0c\u672a\u9009\u4e2d\u7684 Skill \u4e0d\u4f1a\u88ab Agent \u770b\u5230\u3002</p><div id="task-skill-options">${taskSkillOptions(projectID)}</div></fieldset><div id="task-git-isolation">`,
  );
}

function taskDialogBase(): string {
  return `<dialog id="task-dialog" class="wide"><form id="task-form"><div class="dialog-head"><h2>创建任务</h2><button type="button" data-close class="icon-button">${icon("close")}</button></div><label>标题<input name="title" required></label><label>需求<textarea name="request" required></textarea></label><div class="two"><label>项目<select name="project_id" id="task-project" required>${state.projects.filter(item => !item.channel_retired).map(item => `<option value="${item.id}">${escapeHTML(item.name)}</option>`).join("")}</select></label><label>Workspace<select name="workspace_id" id="task-workspace" required></select></label></div>${runtimeFieldsHTML("task", state.models, state.codexAccounts)}<div class="two"><label>推理强度<select name="reasoning_effort" id="task-effort"></select></label><label>沙箱（文件访问）<select name="filesystem"><option value="workspace-write">工作区可写</option><option value="read-only">只读</option><option value="danger-full-access">完全访问</option></select></label></div><div class="two"><label>协作模式<select name="collaboration_mode"><option value="auto">Auto</option><option value="single">Single</option></select></label><label>最大 Agent 数<input name="max_agents" type="number" min="1" value="3"></label></div><label>审批<select name="approval"><option value="never">无需确认</option><option value="auto">自动批准（跳过权限检查）</option></select></label><div id="task-git-isolation"><label>任务隔离<select name="isolation" id="task-isolation"><option value="worktree">独立 Worktree（推荐）</option><option value="inplace">原地执行</option></select></label><div id="task-worktree-settings"><label>Worktree 根目录<input name="worktree_dir" id="task-worktree-dir" placeholder="默认：仓库上一级/.aha2-worktrees"></label><div class="field-help">系统会在根目录下追加 Task ID；切换为原地执行后不使用此配置。</div></div><div class="two" id="task-branches"><label>目标分支<input name="target_branch" placeholder="默认当前分支"></label><label>任务分支<input name="task_branch" placeholder="aha/task-name"></label></div></div><div class="dialog-actions"><button type="button" data-close>取消</button><button type="submit" name="start_mode" value="manual">创建，稍后启动</button><button class="primary" type="submit" name="start_mode" value="immediate">创建并立即启动</button></div></form></dialog>`;
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

function taskTakeoverDialog(detail: TaskDetail): string {
  if (!detail.task.read_only) return "";
  const workspaces = state.workspaces.filter(item => item.project_id === detail.task.project_id && !item.read_only && Boolean(item.root_path));
  const hardware = detail.hardware || [];
  const groups = hardware.map(group => `<fieldset class="takeover-hardware" data-hardware-id="${escapeHTML(group.id)}"><legend>${escapeHTML(group.description || group.id)}</legend><input type="hidden" data-field="id" value="${escapeHTML(group.id)}"><input type="hidden" data-field="description" value="${escapeHTML(group.description || "")}"><input type="hidden" data-field="mode" value="${escapeHTML(group.mode)}"><div class="two"><label>本机串口<input data-field="serial-device" placeholder="${escapeHTML(group.serial?.device || "无需串口")}" ${group.serial?.device ? "required" : ""}></label><label>Baudrate<input data-field="serial-baudrate" type="number" value="${Number(group.serial?.baudrate || 115200)}"></label></div><div class="two"><label>本机网络地址<input data-field="network-host" placeholder="${escapeHTML(group.network?.host || "无需网络")}" ${group.network?.host ? "required" : ""}></label><label>端口<input data-field="network-port" type="number" value="${Number(group.network?.port || 22)}"></label></div><input type="hidden" data-field="network-protocol" value="${escapeHTML(group.network?.protocol || "raw")}"><input type="hidden" data-field="network-ssh-auth" value="${escapeHTML(group.network?.ssh_auth || "auto")}"><label>用户名<input data-field="username" value="${escapeHTML(group.username || "")}"></label>${group.password_configured ? `<label class="reuse-remote-secret"><input data-field="reuse-remote-credential" type="checkbox" checked>复用已同步的端到端加密凭据</label>` : ""}<label>${group.password_configured ? "或输入新密码" : "密码（可选）"}<input data-field="password" type="password" autocomplete="new-password"></label></fieldset>`).join("");
  return `<dialog id="task-takeover-dialog" class="wide"><form id="task-takeover-form"><div class="dialog-head"><h2>接管只读 Task</h2><button type="button" data-close class="icon-button">${icon("close")}</button></div><p class="field-help">接管会创建新的本机 Task。请重新选择本机 Workspace，并确认所有设备专属路径和端点。</p><label>标题<input name="title" required value="${escapeHTML(detail.task.title)} (本机)"></label><label>需求<textarea name="request" required>${escapeHTML(detail.task.original_request || detail.task.current_goal || detail.task.title)}</textarea></label><label>本机 Workspace<select name="workspace_id" id="takeover-task-workspace" required>${workspaces.map(item => `<option value="${item.id}">${escapeHTML(item.name)} · ${escapeHTML(item.root_path)}</option>`).join("") || '<option value="">请先为此项目创建本机 Workspace</option>'}</select></label>${runtimeFieldsHTML("takeover-task", state.models, state.codexAccounts)}<div class="two"><label>推理强度<select name="reasoning_effort" id="takeover-task-effort"></select></label><label>文件访问<select name="filesystem"><option value="workspace-write">工作区可写</option><option value="read-only">只读</option><option value="danger-full-access">完全访问</option></select></label></div><div class="two"><label>协作模式<select name="collaboration_mode"><option value="auto" ${detail.task.collaboration_mode === "auto" ? "selected" : ""}>Auto</option><option value="single" ${detail.task.collaboration_mode === "single" ? "selected" : ""}>Single</option></select></label><label>最大 Agent 数<input name="max_agents" type="number" min="1" value="${Number(detail.task.max_agents || 3)}"></label></div><label>审批<select name="approval"><option value="never">无需确认</option><option value="auto">自动批准</option></select></label>${groups ? `<section><h3>本机硬件重绑定</h3>${groups}</section>` : ""}<div class="dialog-actions"><button type="button" data-close>取消</button><button class="primary" type="submit" ${workspaces.length ? "" : "disabled"}>创建本机 Task</button></div></form></dialog>`;
}

function currentAttachmentDrafts(): Attachment[] {
  return state.taskAttachmentDrafts[state.selectedTaskAgent] || [];
}

function attachmentURL(item: Pick<Attachment, "task_id" | "id">): string {
  return `/api/v1/tasks/${encodeURIComponent(item.task_id)}/attachments/${encodeURIComponent(item.id)}`;
}

function attachmentSize(size: number): string {
  if (size < 1024) return `${size} B`;
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`;
  return `${(size / (1024 * 1024)).toFixed(1)} MB`;
}

function previewableAttachment(mediaType: string): boolean {
  return ["image/png", "image/jpeg", "image/gif", "image/webp"].includes(mediaType);
}

function attachmentTrayHTML(attachments: Attachment[]): string {
  return attachments.length ? `<div class="composer-attachment-tray">${attachments.map(item => `<article>${previewableAttachment(item.media_type) ? `<img src="${attachmentURL(item)}" alt="">` : icon("attachment")}<span><strong>${escapeHTML(item.name)}</strong><small>${attachmentSize(item.size)}</small></span><button type="button" class="icon-button" data-remove-attachment="${escapeHTML(item.id)}" title="移除附件">${icon("close")}</button></article>`).join("")}</div>` : "";
}

function renderAttachmentComposer(disabled: boolean): string {
  const attachments = currentAttachmentDrafts();
  return `${attachmentTrayHTML(attachments)}<input id="message-attachment-input" type="file" multiple hidden ${disabled ? "disabled" : ""}><button id="message-attachment-pick" class="composer-attachment-button" type="button" title="添加附件或直接粘贴图片" ${disabled || attachments.length >= 8 ? "disabled" : ""}>${icon("attachment")}${attachments.length ? `<small>${attachments.length}</small>` : ""}</button>`;
}

let messageAttachmentUploadPending = false;

function clipboardImageFiles(event: ClipboardEvent): File[] {
  const clipboard = event.clipboardData;
  if (!clipboard) return [];
  const source = Array.from(clipboard.items || [])
    .filter(item => item.kind === "file" && item.type.startsWith("image/"))
    .map(item => item.getAsFile())
    .filter((file): file is File => Boolean(file));
  const images = source.length ? source : Array.from(clipboard.files || []).filter(file => file.type.startsWith("image/"));
  return images.map((file, index) => {
    if (file.name && !/^image\.(png|jpe?g|gif|webp)$/i.test(file.name)) return file;
    const extension = file.type === "image/jpeg" ? "jpg" : file.type.split("/")[1] || "png";
    return new File([file], `pasted-image-${Date.now()}-${index + 1}.${extension}`, {type: file.type, lastModified: Date.now()});
  });
}

function refreshMessageAttachmentDOM(): void {
  const input = document.querySelector<HTMLInputElement>("#message-attachment-input");
  const button = document.querySelector<HTMLButtonElement>("#message-attachment-pick");
  if (!input || !button) return;
  document.querySelector(".composer-attachment-tray")?.remove();
  const attachments = currentAttachmentDrafts();
  input.insertAdjacentHTML("beforebegin", attachmentTrayHTML(attachments));
  button.disabled = messageAttachmentUploadPending || attachments.length >= 8;
  button.title = messageAttachmentUploadPending ? "正在上传附件" : "添加附件或直接粘贴图片";
  button.innerHTML = messageAttachmentUploadPending
    ? `${icon("spinner", true)}<small>${attachments.length}</small>`
    : `${icon("attachment")}${attachments.length ? `<small>${attachments.length}</small>` : ""}`;
  bindRemoveAttachmentButtons();
  syncTaskComposerState(document.querySelector<HTMLTextAreaElement>("#message-form textarea"));
}

async function uploadMessageAttachments(files: File[]): Promise<void> {
  if (!files.length || !state.selectedTask || messageAttachmentUploadPending) return;
  const taskID = state.selectedTask.task.id;
  const agentID = state.selectedTaskAgent;
  const drafts = currentAttachmentDrafts();
  if (drafts.length + files.length > 8) {
    setMessage("error", "每条消息最多发送 8 个附件");
    return;
  }
  state.taskAttachmentDrafts[agentID] = drafts;
  messageAttachmentUploadPending = true;
  refreshMessageAttachmentDOM();
  try {
    for (const file of files) {
      const result = await api.uploadAttachment(taskID, file);
      drafts.push(result.attachment);
      refreshMessageAttachmentDOM();
    }
    setMessage("notice", `已添加 ${files.length} 个附件`);
  } catch (error) {
    setMessage("error", error instanceof Error ? error.message : String(error));
  } finally {
    messageAttachmentUploadPending = false;
    if (state.selectedTask?.task.id === taskID && state.selectedTaskAgent === agentID) refreshMessageAttachmentDOM();
  }
}

function bindRemoveAttachmentButtons(): void {
  document.querySelectorAll<HTMLButtonElement>("[data-remove-attachment]").forEach(button => button.addEventListener("click", async () => {
    if (!state.selectedTask) return;
    const id = button.dataset.removeAttachment || "";
    button.disabled = true;
    try {
      await api.deleteAttachment(state.selectedTask.task.id, id);
      state.taskAttachmentDrafts[state.selectedTaskAgent] = currentAttachmentDrafts().filter(item => item.id !== id);
      refreshMessageAttachmentDOM();
    } catch (error) {
      button.disabled = false;
      setMessage("error", error instanceof Error ? error.message : String(error));
    }
  }));
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
  const agent = state.selectedTask?.agents.find(item => item.agent_id === state.selectedTaskAgent);
  const runtimeError = agent?.runtime_config_valid === false
    ? (agent.runtime_config_error || "模型配置不可用，请修改模型配置") : "";
  const readOnly = Boolean(state.selectedTask?.task.read_only);
  textarea.disabled = Boolean(runtimeError) || readOnly || messageSubmitPending;
  if (runtimeError) textarea.placeholder = runtimeError;
  else if (messageSubmitPending) textarea.placeholder = "消息发送中…";
  renderTaskSlashCommandMenu(textarea);
  const send = document.querySelector<HTMLButtonElement>("#message-send");
  if (send) send.disabled = Boolean(runtimeError) || readOnly || messageSubmitPending || (!textarea.value.trim() && currentAttachmentDrafts().length === 0);
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
    <details class="prompt-snapshot" open><summary>查看本轮 Prompt Snapshot</summary><div class="prompt-snapshot-body markdown-body">${renderMarkdown(context.prompt || "尚无 Prompt Snapshot")}</div></details>
  </div>`;
}

function taskDetailView(detail: TaskDetail): string {
  const archivedReadOnly = detail.task.channel_retired === true || detail.task.read_only_reason === "channel_retired";
  const remoteReadOnly = Boolean(detail.task.read_only && !archivedReadOnly);
  const readOnly = archivedReadOnly || remoteReadOnly;
  const draft = detail.task.status === "draft";
  const activeTurn = (detail.turns || []).find(item => item.agent_id === state.selectedTaskAgent && isActiveTurn(item.status));
  const taskFailed = detail.task.status === "failed";
  const selectedAgent = detail.agents.find(item => item.agent_id === state.selectedTaskAgent);
  const runtimeError = selectedAgent?.runtime_config_valid === false
    ? (selectedAgent.runtime_config_error || "模型配置不可用，请修改模型配置") : "";
  const project = state.projects.find(item => item.id === detail.task.project_id);
  const workspace = state.workspaces.find(item => item.id === detail.task.workspace_id);
  const taskMeta = `${project?.name || "-"} · ${workspace?.name || "-"} · ${detail.task.collaboration_mode || "auto"} · ${detail.task.max_agents || 3} Agents`;
  const composerDisabled = Boolean(runtimeError || readOnly || draft);
  const chat = `<section class="conversation">
    ${archivedReadOnly ? `<div class="task-failure-banner remote-readonly"><strong>渠道归档只读</strong><span>该渠道实例已归档</span><small>历史内容可查看和导出，不能继续执行或修改</small></div>` : remoteReadOnly ? `<div class="task-failure-banner remote-readonly"><strong>其他设备的只读 Task</strong><span>所属设备：${escapeHTML(detail.task.owner_device_id || "未知")}</span><small>可查看同步历史，不能在本机执行或修改</small><div class="remote-readonly-actions"><button type="button" id="takeover-task">${icon("copy")}接管到本机</button><button type="button" class="danger" data-retire-remote-task="${detail.task.id}">${icon("close")}移除孤立任务</button></div></div>` : ""}
    <div id="task-failure-slot">${taskFailureBannerHtml(detail)}</div>
    ${draft ? `<div class="task-failure-banner task-draft-banner"><strong>任务尚未启动</strong><span>可以先配置 Agent、Skill 和硬件调试，准备好后再启动。</span><button type="button" id="start-task" class="primary">${icon("play")}启动任务</button></div>` : ""}
    <div class="messages" id="conversation-list">${conversationListHtml()}</div>
    <div id="agent-turn-slot">${renderAgentTurnCard(detail, state.taskRealtimeState, state.taskContext?.context.metrics)}</div>
    <form id="message-form" class="composer${composerDisabled ? " runtime-invalid" : ""}">${composerDisabled ? `<div class="composer-runtime-warning">${escapeHTML(draft ? "先启动任务后再发送消息" : archivedReadOnly ? "渠道实例已归档，此 Task 只读" : remoteReadOnly ? "该 Task 属于其他设备，本机只读" : runtimeError)}</div>` : ""}${renderComposerTools(detail, state.selectedTaskAgent, state.taskCategories, state.taskConversation.length)}<div id="slash-command-menu" class="slash-command-menu" ${matchingTaskSlashCommands(state.taskDraft).length ? "" : "hidden"}>${slashCommandMenuHtml(state.taskDraft)}</div>${renderAttachmentComposer(composerDisabled)}<textarea name="content" placeholder="${escapeHTML(draft ? "启动任务后可发送消息" : readOnly ? "只读 Task" : runtimeError || (activeTurn ? `${state.selectedTaskAgent} 正在执行，发送后将排队` : taskFailed ? "输入消息重试，或输入 /reopen" : `发送给 ${state.selectedTaskAgent}，输入 / 查看命令`))}" ${composerDisabled ? "disabled" : ""}>${escapeHTML(state.taskDraft)}</textarea><button id="message-send" class="primary" aria-label="发送" ${composerDisabled ? "disabled" : ""}>${icon("send")}<span class="send-label">发送</span></button></form>
  </section>`;
  const toolLayout = state.taskTool ? ` task-tool-open task-tool-${state.taskToolMode}` : "";
  return shell(`<section class="task-screen">
    <header class="task-head"><button id="back-tasks">←</button><div class="task-title-block"><h1><span class="task-code">${escapeHTML(detail.task.code || "")}</span><span class="task-title-text">${escapeHTML(detail.task.title)}</span></h1><div class="task-head-subline"><span class="task-head-meta" title="${escapeHTML(taskMeta)}">${escapeHTML(taskMeta)}</span><span id="task-detail-status" class="status ${statusClass(detail.task.status)}">${statusLabel(detail.task.status)}</span><span class="task-branch">${escapeHTML(detail.task.task_branch || "")}</span></div></div><div class="actions task-tool-actions">${renderTaskToolButtons(state.taskTool)}</div></header>
    <div class="task-grid${toolLayout}" style="--task-tool-width:${state.taskToolWidth}%">
      ${chat}
      ${renderTaskToolPanel(state.taskTool, detail, state.selectedTaskAgent, taskCtxHtml(), state.taskToolMode)}
    </div>
    ${renderAgentConfigDialog(detail, state.selectedTaskAgent, state.models, state.codexAccounts, state.skills)}${taskTakeoverDialog(detail)}
  </section>`);
}

type RegionUIState = InteractiveRegionState;

function captureRegionUI(root: HTMLElement | null): RegionUIState | null {
  return captureInteractiveRegion(root);
}

function restoreRegionUI(root: HTMLElement | null, state: RegionUIState | null, restoreRootScroll = true): void {
  restoreInteractiveRegion(root, state, restoreRootScroll);
  if (!root) return;
  root.querySelectorAll<HTMLElement>(".message").forEach(message => {
    if (!message.classList.contains("expanded")) return;
    message.classList.add("expanded");
    const preview = message.querySelector<HTMLElement>(".message-preview");
    const full = message.querySelector<HTMLElement>(".message-full-text");
    const toggle = message.querySelector<HTMLButtonElement>("[data-toggle-message]");
    if (preview) preview.hidden = true;
    if (full) full.hidden = false;
    if (toggle) toggle.textContent = "收起";
  });
}

function replaceRegionHTML(root: HTMLElement, html: string): void {
  const ui = captureRegionUI(root);
  root.innerHTML = html;
  restoreRegionUI(root, ui);
}

function requestConversationBottom(): void {
  scrollConversationToBottom = true;
  conversationBottomPinVersion++;
}

function cancelConversationBottomPin(): void {
  conversationBottomPinVersion++;
  conversationAutoScrollBlockedUntil = Date.now() + 800;
}

function shouldAutoScrollConversation(wasAtBottom: boolean): boolean {
  return scrollConversationToBottom || (wasAtBottom && Date.now() >= conversationAutoScrollBlockedUntil);
}

function stabilizeConversationBottom(list: HTMLElement): void {
  const pinVersion = conversationBottomPinVersion;
  const apply = () => {
    if (pinVersion !== conversationBottomPinVersion || !list.isConnected) return;
    list.scrollTop = list.scrollHeight;
  };
  apply();
  window.requestAnimationFrame(() => {
    apply();
    window.requestAnimationFrame(apply);
  });
  list.querySelectorAll<HTMLImageElement>("img").forEach(image => {
    if (!image.complete) image.addEventListener("load", apply, {once: true});
  });
}

function renderUIScope(): string {
  if (state.selectedTask) return `task:${state.selectedTask.task.id}`;
  if (state.selectedProject) return `project:${state.selectedProject.id}`;
  return `view:${state.view}`;
}

function hasActiveTextEditor(): boolean {
  const active = document.activeElement;
  if (!(active instanceof HTMLElement) || !app?.contains(active)) return false;
  if (active instanceof HTMLTextAreaElement || active.isContentEditable) return true;
  if (!(active instanceof HTMLInputElement)) return false;
  return !["button", "checkbox", "color", "file", "hidden", "image", "radio", "range", "reset", "submit"].includes(active.type);
}

function updateTaskLiveRegions(): void {
  const detail = state.selectedTask;
  if (!detail) return;
  if (appPointerActive) {
    state.renderPending = true;
    return;
  }
  const activeTurn = (detail.turns || []).find(item => item.agent_id === state.selectedTaskAgent && isActiveTurn(item.status));
  const list = document.querySelector<HTMLElement>("#conversation-list");
  if (list) {
    const ui = captureRegionUI(list);
    const previousHeight = list.scrollHeight;
    const previousTop = list.scrollTop;
    const wasAtBottom = previousHeight - previousTop - list.clientHeight < 80;
    list.innerHTML = conversationListHtml();
    restoreRegionUI(list, ui, false);
    if (shouldAutoScrollConversation(wasAtBottom)) stabilizeConversationBottom(list);
    else list.scrollTop = previousTop;
    scrollConversationToBottom = false;
    bindConversationLiveControls();
  }
  const turnSlot = document.querySelector<HTMLElement>("#agent-turn-slot");
  if (turnSlot) replaceRegionHTML(turnSlot, renderAgentTurnCard(detail, state.taskRealtimeState, state.taskContext?.context.metrics));
  const failureSlot = document.querySelector<HTMLElement>("#task-failure-slot");
  const nextFailure = taskFailureBannerHtml(detail);
  if (failureSlot && failureSlot.innerHTML !== nextFailure) failureSlot.innerHTML = nextFailure;
  const agentSelect = document.querySelector<HTMLSelectElement>("#composer-agent");
  const nextAgentOptions = renderComposerAgentOptions(detail, state.selectedTaskAgent);
  if (agentSelect && document.activeElement !== agentSelect && agentSelect.innerHTML !== nextAgentOptions) agentSelect.innerHTML = nextAgentOptions;
  const taskStatus = document.querySelector<HTMLElement>("#task-detail-status");
  if (taskStatus) {
    taskStatus.className = `status ${statusClass(detail.task.status)}`;
    taskStatus.textContent = statusLabel(detail.task.status);
  }
  const toolBody = document.querySelector<HTMLElement>("#task-tool-panel-body");
  if (toolBody && state.taskTool === "hardware") {
    refreshHardwarePanel(detail, setMessage);
  } else if (toolBody && state.taskTool) {
    replaceRegionHTML(toolBody, renderTaskToolContent(state.taskTool, detail, taskCtxHtml()));
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

function render(): void {
  if (!app) return;
  document.body.classList.toggle("task-view-active", Boolean(state.selectedTask));
  if (state.loading) {
    delete app.dataset.uiScope;
    app.innerHTML = `<div class="loading boot-loading"><span class="brand-mark">A</span><strong>AHA2</strong><small>正在连接服务…</small></div>`;
    return;
  }
  if (!state.auth?.authenticated) {
    delete app.dataset.uiScope;
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
  if (state.view === "sync" && isSyncSettingsFormEditing(document.querySelector<HTMLFormElement>("#sync-settings-form"), document.activeElement)) {
    state.renderPending = true;
    return;
  }
  if (appPointerActive) {
    state.renderPending = true;
    return;
  }
  if (hasActiveTextEditor()) {
    state.renderPending = true;
    return;
  }
  persistNavigationState();
  if (document.activeElement instanceof HTMLTextAreaElement && document.activeElement.closest("#message-form")) {
    state.renderPending = true;
    return;
  }
  state.renderPending = false;
  const nextUIScope = renderUIScope();
  const preservePageUI = app.dataset.uiScope === nextUIScope;
  const previousAppUI = preservePageUI ? captureRegionUI(app) : null;
  const previousConversation = document.querySelector<HTMLElement>("#conversation-list");
  const previousWindowScroll = window.scrollY;
  const previousTurnUI = captureRegionUI(document.querySelector<HTMLElement>("#agent-turn-slot"));
  const previousToolUI = captureRegionUI(document.querySelector<HTMLElement>("#task-tool-panel-body"));
  const previousConversationUI = captureRegionUI(previousConversation);
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
		channels: () => shell(renderChannels(state.channelProviders, state.channelInstances, {models: state.models, accounts: state.codexAccounts, projects: state.projects, workspaces: state.workspaces, knowledge: state.knowledge, libraries: state.knowledgeLibraries})),
      models: modelsView,
      tasks: tasksView,
      knowledge: () => shell(`${renderKnowledgeWorkspace({
        projects: state.projects,
        workspaces: state.workspaces,
        knowledge: state.knowledge,
        libraries: state.knowledgeLibraries,
        proposals: state.knowledgeProposals,
        reviewSettings: state.knowledgeReviewSettings,
        refreshData: loadKnowledgeData,
        render,
        setMessage,
      })}${loadMoreHTML("knowledge")}`),
      prompts: () => advancedSubview(renderPromptAdmin()),
      proxy: () => shell(renderProxySettings(state.proxySettings, state.managedProxy)),
      sync: () => advancedSubview(renderSyncSettings(state.syncSettings, state.syncState, state.syncPending, state.syncConflicts, state.syncPreview, state.syncRun)),
      advanced: advancedSettingsView,
    };
    content = views[state.view]();
  }
  app.innerHTML = content;
  app.dataset.uiScope = nextUIScope;
  bindCommon();
  restoreRegionUI(app, previousAppUI, false);
  if (state.selectedTask) {
    restoreRegionUI(document.querySelector<HTMLElement>("#agent-turn-slot"), previousTurnUI);
    restoreRegionUI(document.querySelector<HTMLElement>("#task-tool-panel-body"), previousToolUI);
  }
  const nextConversation = document.querySelector<HTMLElement>("#conversation-list");
  if (nextConversation) {
    restoreRegionUI(nextConversation, previousConversationUI, false);
    if (shouldAutoScrollConversation(wasAtConversationBottom)) {
      stabilizeConversationBottom(nextConversation);
    } else {
      nextConversation.scrollTop = previousScrollTop;
    }
  }
  if (preservePageUI) window.scrollTo(0, previousWindowScroll);
  else window.scrollTo(0, 0);
  scrollConversationToBottom = false;
}

function bindAuth(): void {
  document.querySelector("#forgot-password")?.addEventListener("click", () => {
    authRecoveryOpen = true;
    state.error = "";
    render();
  });
  document.querySelector("#back-to-login")?.addEventListener("click", () => {
    authRecoveryOpen = false;
    state.error = "";
    render();
  });
  document.querySelector<HTMLFormElement>("#password-recovery-form")?.addEventListener("submit", async event => {
    event.preventDefault();
    const element = event.currentTarget;
    const form = new FormData(element);
    const newPassword = String(form.get("new_password") || "");
    if (newPassword !== String(form.get("confirm_password") || "")) {
      state.error = "两次输入的新密码不一致";
      render();
      return;
    }
    const button = element.querySelector<HTMLButtonElement>('button[type="submit"]');
    const original = button?.innerHTML || "";
    if (button) {
      button.disabled = true;
      button.innerHTML = `${icon("spinner", true)}<span>重置中</span>`;
    }
    try {
      const username = String(form.get("username") || "").trim();
      const response = await api.recoverPassword({
        setup_token: String(form.get("setup_token") || "").trim(),
        username,
        new_password: newPassword,
      });
      state.auth = {...response, authenticated: true, registration_open: false, username};
      api.setCSRF(response.csrf_token);
      authRecoveryOpen = false;
      state.error = "";
      await loadAll();
      openGlobalEvents();
    } catch (error) {
      state.error = authErrorMessage(error, "密码重置失败");
    } finally {
      if (button?.isConnected) {
        button.disabled = false;
        button.innerHTML = original;
      }
    }
    render();
  });
  document.querySelector<HTMLFormElement>("#auth-form")?.addEventListener("submit", async event => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const payload = Object.fromEntries(form.entries()) as Record<string, string>;
    try {
      const response = state.auth?.registration_open ? await api.register(payload) : await api.login(payload);
      state.auth = {...response, authenticated: true, registration_open: false, username: payload.username};
      api.setCSRF(response.csrf_token);
      state.error = "";
      authRecoveryOpen = false;
      await loadAll();
      openGlobalEvents();
    } catch (error) {
      state.error = error instanceof Error ? error.message : String(error);
    }
    render();
  });
}

function bindCommon(): void {
  bindCodexAccounts({
    accounts: state.codexAccounts,
    onChanged: async () => { await loadAll(); render(); },
    setMessage,
  });
  bindProxySettings({
    settings: state.proxySettings,
    managed: state.managedProxy,
    onChanged: (settings, managed) => {
      state.proxySettings = settings;
      if (managed) state.managedProxy = managed;
      render();
    },
    setMessage,
  });
  bindSyncSettings({
    settings: state.syncSettings,
    refresh: async () => {
      const [settings, status, conflicts, preview] = await Promise.all([api.syncSettings(), api.syncStatus(), api.syncConflicts(), api.syncPreview()]);
      state.syncSettings = settings.sync; state.syncState = status.state; state.syncPending = status.pending || 0; state.syncRun = status.run || state.syncRun; state.syncConflicts = conflicts.conflicts || []; state.syncPreview = preview.preview;
      render();
    },
	pollStatus: async () => {
		const status = await api.syncStatus();
		state.syncState = status.state; state.syncPending = status.pending || 0; state.syncRun = status.run || state.syncRun;
		return state.syncRun;
	},
    setMessage,
    flushDeferredRender,
  });
  bindChannels({
		context: {models: state.models, accounts: state.codexAccounts, projects: state.projects, workspaces: state.workspaces, knowledge: state.knowledge, libraries: state.knowledgeLibraries},
    refresh: async () => {
      const [providers, instances, projects, workspaces, tasks] = await Promise.all([
        api.channelProviders(), api.channelInstances(), api.projects({limit: initialListPageSize}), api.workspaces(), api.tasks("", {limit: initialListPageSize}),
      ]);
      state.channelProviders = providers.providers || [];
      state.channelInstances = instances.instances || [];
      state.projects = projects.projects || [];
      state.workspaces = workspaces.workspaces || [];
      state.tasks = tasks.tasks || [];
      listPages.projects = {cursor: projects.next_cursor || "", hasMore: Boolean(projects.has_more), loadingMore: false};
      listPages.tasks = {cursor: tasks.next_cursor || "", hasMore: Boolean(tasks.has_more), loadingMore: false};
      render();
    },
		setMessage: (kind, message) => setMessage(kind === "error" ? "error" : "notice", message),
		openProject: projectID => {
			state.view = "projects";
			state.selectedProject = state.projects.find(project => project.id === projectID) || null;
			state.dialogProjectID = projectID;
			state.selectedTask = null;
			closeEvents();
			render();
		},
  });
  bindRuntimeFields("task", state.models, state.codexAccounts, syncTaskGitIsolation);
  bindRuntimeFields("takeover-task", state.models, state.codexAccounts, syncTakeoverTaskBackend);
  bindRuntimeFields("agent-config", state.models, state.codexAccounts, syncAgentConfigFields, true);
  document.querySelectorAll<HTMLElement>("[data-view]").forEach(button => button.addEventListener("click", () => {
    state.view = button.dataset.view as View;
    state.selectedTask = null;
    state.selectedProject = null;
    state.dialogProjectID = "";
    closeEvents();
    render();
    void ensureViewData(state.view).then(() => render());
  }));
  document.querySelector<HTMLElement>("#retry-view-data")?.addEventListener("click", () => {
    void ensureViewData(state.view, true).then(() => render());
  });
  document.querySelectorAll<HTMLElement>("[data-load-more]").forEach(button => button.addEventListener("click", () => {
    void loadNextPage(button.dataset.loadMore as "projects" | "tasks" | "knowledge");
  }));
  document.querySelector("#owner-avatar")?.addEventListener("click", () => {
    ownerAvatarClicks++;
    if (ownerAvatarResetTimer) window.clearTimeout(ownerAvatarResetTimer);
    if (ownerAvatarClicks >= 5) {
      ownerAvatarClicks = 0;
      state.view = "advanced";
      state.selectedTask = null;
      state.selectedProject = null;
      closeEvents();
      render();
      void ensureViewData(state.view).then(() => render());
      return;
    }
    ownerAvatarResetTimer = window.setTimeout(() => { ownerAvatarClicks = 0; }, 1800);
  });
  document.querySelector("#logout")?.addEventListener("click", () => {
    const button = document.querySelector<HTMLElement>("#logout");
    void runWithFeedback(button, "退出中", async () => {
      await api.logout();
      state.auth = {...state.auth!, authenticated: false};
      state.selectedTask = null;
      state.selectedProject = null;
      authRecoveryOpen = false;
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
    void ensureViewData("models").then(() => render());
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
  document.querySelectorAll<HTMLElement>("[data-takeover-workspace]").forEach(button => button.addEventListener("click", () => {
    const workspace = state.workspaces.find(item => item.id === button.dataset.takeoverWorkspace);
    if (workspace?.read_only) openWorkspaceTakeoverDialog(workspace);
  }));
  document.querySelectorAll<HTMLElement>("[data-retire-remote-workspace]").forEach(button => button.addEventListener("click", event => {
    event.stopPropagation();
    const id = button.dataset.retireRemoteWorkspace || "";
    const workspace = state.workspaces.find(item => item.id === id);
    if (!workspace?.read_only) return;
    if (!window.confirm(`仅当来源设备已停用或源 Workspace 已删除时才能移除“${workspace.name}”。若它仍有只读任务，需先移除这些任务。确定继续？`)) return;
    void runWithFeedback(button, "移除中", async () => {
      const result = await api.retireRemoteWorkspaceMirror(id);
      let pushed = false;
      if (result.synchronized) {
        try {
          await api.runSync();
          pushed = true;
        } catch {}
      }
      setMessage("notice", !result.synchronized
        ? "孤立 Workspace 已从本机移除，并已防止历史同步重放"
        : pushed ? "孤立 Workspace 已移除，删除标记已推送" : "孤立 Workspace 已移除，删除标记将在下次同步时推送");
      await loadAll();
      render();
    });
  }));
  document.querySelectorAll<HTMLElement>("[data-edit-provider]").forEach(button => button.addEventListener("click", () => {
    const provider = state.providers.find(item => item.id === button.dataset.editProvider!);
    if (provider) openProviderDialog(provider);
  }));
  document.querySelector<HTMLSelectElement>("#project-type")?.addEventListener("change", syncProjectTypeFields);
  document.querySelector<HTMLSelectElement>("#ws-locality")?.addEventListener("change", syncWorkspaceFields);
  document.querySelector<HTMLSelectElement>("#ws-transport")?.addEventListener("change", syncWorkspaceFields);
  document.querySelector<HTMLSelectElement>("#ws-agent-api-mode")?.addEventListener("change", syncWorkspaceFields);
  document.querySelector<HTMLSelectElement>('[name="ssh_auth"]')?.addEventListener("change", syncWorkspaceFields);
  document.querySelector<HTMLSelectElement>('#workspace-takeover-dialog [name="transport"]')?.addEventListener("change", syncWorkspaceTakeoverFields);
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
    const result = editId ? await api.updateProject(editId, payload) : await api.createProject(payload);
    state.projects = editId
      ? state.projects.map(item => item.id === result.project.id ? result.project : item)
      : [result.project, ...state.projects.filter(item => item.id !== result.project.id)];
    if (state.selectedProject?.id === result.project.id) state.selectedProject = result.project;
    setMessage("notice", editId ? "项目已更新" : "项目已创建");
  }, "保存中", false);
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
    let result;
    if (editId) {
      result = await api.updateWorkspace(editId, {
        ...body,
        clear_ssh_password: payload.clear_ssh_password === "on",
      });
    } else {
      delete body.clear_ssh_password;
      result = await api.createWorkspace(body);
    }
    state.workspaces = editId
      ? state.workspaces.map(item => item.id === result.workspace.id ? result.workspace : item)
      : [result.workspace, ...state.workspaces.filter(item => item.id !== result.workspace.id)];
    setMessage("notice", editId ? "Workspace 已更新" : "Workspace 已创建");
  }, "保存中", false);
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
    if (!results || !button) return;
    void startModelDetection(providerID, button, results);
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
		const project = state.projects.find(item => item.id === id);
		if (project?.channel_retired) {
			void runWithFeedback(button, "永久删除中", async () => {
				if (await purgeArchivedChannelProject(project)) render();
			});
			return;
		}
    if (!window.confirm("删除该项目及其所有 Workspace / 任务？已绑定知识库会自动解绑并保留。此操作不可恢复。")) return;
    void runWithFeedback(button, "删除中", async () => {
      await api.deleteProject(id);
			forgetProject(id);
      setMessage("notice", "项目已删除");
      render();
    });
  }));
  document.querySelectorAll<HTMLElement>("[data-delete-workspace]").forEach(button => button.addEventListener("click", async event => {
    event.stopPropagation();
    const id = button.dataset.deleteWorkspace!;
    if (!window.confirm("删除该 Workspace 及其所有任务？此操作不可恢复。")) return;
    void runWithFeedback(button, "删除中", async () => {
      await api.deleteWorkspace(id);
      state.workspaces = state.workspaces.filter(item => item.id !== id);
      state.tasks = state.tasks.filter(item => item.workspace_id !== id);
      setMessage("notice", "Workspace 已删除");
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
      state.tasks = state.tasks.filter(item => item.id !== id);
      setMessage("notice", "任务已删除");
      render();
    });
  }));
  document.querySelectorAll<HTMLElement>("[data-retire-remote-task]").forEach(button => button.addEventListener("click", async event => {
    event.stopPropagation();
    const id = button.dataset.retireRemoteTask!;
    const task = state.tasks.find(item => item.id === id);
    const title = task?.title || state.selectedTask?.task.title || "该任务";
    if (!window.confirm(`仅当来源设备已停用或数据已清空时才能移除“${title}”。此操作会生成同步删除标记，让所有设备删除该只读镜像，且不可恢复。确定继续？`)) return;
    void runWithFeedback(button, "移除中", async () => {
      await api.retireRemoteTaskMirror(id);
      let synchronized = true;
      try {
        await api.runSync();
      } catch {
        synchronized = false;
      }
      if (state.selectedTask?.task.id === id) {
        state.selectedTask = null;
        closeEvents();
      }
      setMessage("notice", synchronized
        ? "孤立只读任务已移除，删除标记已推送；其他设备将在下次同步时移除镜像"
        : "孤立只读任务已移除；删除标记将在下次同步时传播到其他设备");
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
		const project = state.selectedProject;
    const id = project.id;
		const button = document.querySelector<HTMLElement>("#delete-project");
		if (project.channel_retired) {
			void runWithFeedback(button, "永久删除中", async () => {
				if (await purgeArchivedChannelProject(project)) render();
			});
			return;
		}
    if (!window.confirm("删除该项目及其所有 Workspace / 任务？已绑定知识库会自动解绑并保留。此操作不可恢复。")) return;
    void runWithFeedback(button, "删除中", async () => {
      await api.deleteProject(id);
			forgetProject(id);
      setMessage("notice", "项目已删除");
      render();
    });
  });
  document.querySelectorAll<HTMLElement>("[data-delete-model]").forEach(button => button.addEventListener("click", () => {
    const id = button.dataset.deleteModel!;
    if (!window.confirm("删除该模型？使用此模型的任务将暂停输入，直到修改 Agent 模型配置。")) return;
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
    if (wireInput) {
      wireInput.value = model.backend === "claude" ? "anthropic_messages" : (model.wire_api || "responses");
      wireInput.disabled = false;
    }
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
  document.querySelector("#takeover-task-workspace")?.addEventListener("change", syncTakeoverTaskBackend);
  document.querySelector("#takeover-task")?.addEventListener("click", () => {
    syncTakeoverTaskBackend();
    document.querySelector<HTMLDialogElement>("#task-takeover-dialog")?.showModal();
  });
  document.querySelector("#task-isolation")?.addEventListener("change", syncTaskGitIsolation);
  bindForm("#task-form", async form => {
    const payload = Object.fromEntries(form.entries()) as Record<string, string>;
    const result = await api.createTask({...payload, skill_ids: form.getAll("skill_ids").map(String), max_agents: Number(payload.max_agents || 3), proxy_enabled: payload.proxy_enabled === "on"});
    state.tasks = [result.task, ...state.tasks.filter(item => item.id !== result.task.id)];
    setMessage("notice", result.task.status === "draft" ? "任务草稿已创建，可先完成配置" : "任务已创建并启动");
    await openTask(result.task.id);
    if (result.start_error) setMessage("error", `Task 已创建，但首个 Turn 启动失败：${result.start_error}`);
  }, "创建中", false);
  document.querySelector<HTMLElement>("#start-task")?.addEventListener("click", () => {
    if (!state.selectedTask) return;
    const taskID = state.selectedTask.task.id;
    const button = document.querySelector<HTMLElement>("#start-task");
    void runWithFeedback(button, "启动中", async () => {
      const result = await api.startTask(taskID);
      await openTask(taskID);
      setMessage("notice", "任务已启动");
      if (result.start_error) setMessage("error", `任务已启动，但首个 Turn 启动失败：${result.start_error}`);
      render();
    });
  });
  document.querySelectorAll<HTMLElement>("[data-detect]").forEach(button => button.addEventListener("click", () => {
    const id = button.dataset.detect!;
    void runWithFeedback(button, "检测中", async () => {
      try {
        const result = await detectWorkspaceWithHostKeyTrust(id);
        if (!result) return;
        const agentAPI = result.workspace.agent_api_status === "ready" && result.workspace.agent_api_resolved_url
          ? ` · Agent API ${result.workspace.agent_api_resolved_url}`
          : result.workspace.agent_api_error ? ` · Agent API 不可达：${result.workspace.agent_api_error}` : "";
        setMessage("notice", `连接测试完成：${statusLabel(result.workspace.health)}${agentAPI}`);
      } finally {
        await loadAll();
        render();
      }
    });
  }));
  document.querySelectorAll<HTMLElement>("[data-task]").forEach(button => {
	button.addEventListener("pointerdown", () => {
	  taskListPointerActive = true;
	});
	button.addEventListener("click", async event => {
	  if ((event.target as HTMLElement).closest(".task-card-actions")) return;
	  const taskID = button.dataset.task!;
	  if (openingTaskID) return;
	  openingTaskID = taskID;
	  button.classList.add("opening");
	  button.setAttribute("aria-busy", "true");
	  try {
		await openTask(taskID);
		render();
	  } catch (error) {
		setMessage("error", error instanceof Error ? error.message : String(error));
		render();
	  } finally {
		openingTaskID = "";
		button.classList.remove("opening");
		button.removeAttribute("aria-busy");
	  }
	});
  });
  bindTaskAgentControls();
  document.querySelector<HTMLSelectElement>("#composer-agent")?.addEventListener("change", async event => {
    await selectTaskAgent(event.currentTarget.value);
    render();
  });
  document.querySelector<HTMLInputElement>('[name="inherit_main"]')?.addEventListener("change", syncAgentConfigFields);
  bindForm("#agent-config-form", async form => {
    if (!state.selectedTask) return;
    const agentID = String(form.get("agent_id") || state.selectedTaskAgent);
    if (agentID === "main") {
      await api.updateTask(state.selectedTask.task.id, {
        collaboration_mode: String(form.get("collaboration_mode") || "auto"),
        max_agents: Number(form.get("max_agents") || 3),
        knowledge_policy: String(form.get("knowledge_policy") || "inherit"),
        skill_ids: form.getAll("skill_ids").map(String),
        agent_capabilities: {
          workspace_read: form.get("cap_workspace_read") === "on",
          task_create: form.get("cap_task_create") === "on",
          clone_hardware: form.get("cap_clone_hardware") === "on",
        },
      });
    }
    const inheritMain = agentID !== "main" && form.get("inherit_main") === "on";
    await api.updateTaskAgent(state.selectedTask.task.id, agentID, inheritMain ? {
      inherit_main: true,
    } : {
      inherit_main: false,
      backend: String(form.get("backend") || ""),
      model_source: String(form.get("model_source") || "env"),
      model_id: String(form.get("model_id") || ""),
      wire_model: String(form.get("wire_model") || ""),
      codex_account_id: String(form.get("codex_account_id") || ""),
      reasoning_effort: String(form.get("reasoning_effort") || ""),
      filesystem: String(form.get("filesystem") || ""),
      approval: String(form.get("approval") || ""),
      proxy_enabled: form.get("proxy_enabled") === "on",
    });
    await refreshTaskRuntime(state.selectedTask.task.id);
    setMessage("notice", `${agentID} 配置已更新，下一个 Turn 生效`);
  });
  document.querySelector("#back-tasks")?.addEventListener("click", () => {
    state.selectedTask = null;
    closeEvents();
    render();
  });
  document.querySelector<HTMLFormElement>("#task-takeover-form")?.addEventListener("submit", event => {
    event.preventDefault();
    if (!state.selectedTask?.task.read_only) return;
    const element = event.currentTarget;
    const form = new FormData(element);
    const groups = [...element.querySelectorAll<HTMLElement>(".takeover-hardware")].map(group => {
      const value = (name: string) => group.querySelector<HTMLInputElement>(`[data-field="${name}"]`)?.value || "";
      const checked = (name: string) => Boolean(group.querySelector<HTMLInputElement>(`[data-field="${name}"]`)?.checked);
      return {
        id: value("id"), description: value("description"), mode: value("mode"),
        serial: {device: value("serial-device"), baudrate: Number(value("serial-baudrate") || 115200)},
        network: {host: value("network-host"), port: Number(value("network-port") || 0), protocol: value("network-protocol"), ssh_auth: value("network-ssh-auth")},
        username: value("username"), password: value("password"), reuse_remote_credential: checked("reuse-remote-credential"), access: "read_write",
      };
    });
    const taskID = state.selectedTask.task.id;
    const button = element.querySelector<HTMLElement>('button[type="submit"]');
    void runWithFeedback(button, "接管中", async () => {
      const result = await api.takeoverTask(taskID, {
        workspace_id: String(form.get("workspace_id") || ""), title: String(form.get("title") || ""), request: String(form.get("request") || ""),
        backend: String(form.get("backend") || ""), model_source: String(form.get("model_source") || "env"), model_id: String(form.get("model_id") || ""),
        wire_model: String(form.get("wire_model") || ""), codex_account_id: String(form.get("codex_account_id") || ""), reasoning_effort: String(form.get("reasoning_effort") || "medium"),
        filesystem: String(form.get("filesystem") || "workspace-write"), approval: String(form.get("approval") || "never"), collaboration_mode: String(form.get("collaboration_mode") || "auto"),
        max_agents: Number(form.get("max_agents") || 3), groups,
      });
      element.closest<HTMLDialogElement>("dialog")?.close();
      setMessage("notice", "已创建可编辑的本机 Task，远端 Task 保持只读");
      await loadAll();
      await openTask(result.task.id);
      render();
    });
  });
  document.querySelector<HTMLButtonElement>("[data-channel-project-export]")?.addEventListener("click", event => {
    const button = event.currentTarget;
    const instanceID = button.dataset.channelProjectExport || "";
    if (!instanceID) return;
    button.disabled = true;
    void exportRetiredChannelInstance(instanceID).then(() => {
      setMessage("notice", "归档 JSON 已生成并开始下载");
    }).catch(error => {
      setMessage("error", error instanceof Error ? error.message : String(error));
    }).finally(() => {
      button.disabled = false;
    });
  });
  bindForm("#change-password-form", async form => {
    const currentPassword = String(form.get("current_password") || "");
    const newPassword = String(form.get("new_password") || "");
    if (newPassword !== String(form.get("confirm_password") || "")) throw new Error("两次输入的新密码不一致");
    if (newPassword === currentPassword) throw new Error("新密码不能与当前密码相同");
    try {
      await api.changePassword({current_password: currentPassword, new_password: newPassword});
    } catch (error) {
      throw new Error(authErrorMessage(error, "密码修改失败"));
    }
    setMessage("notice", "密码已修改，其他登录会话已退出");
  }, "修改中");
  bindForm("#origin-validation-form", async form => {
    const validateOrigin = form.get("validate_origin") === "on";
    if (!validateOrigin && !window.confirm("确认关闭 Origin 校验？仅应在可信反向代理环境中使用。")) return;
    const response = await api.updateSecuritySettings({validate_origin: validateOrigin});
    state.securitySettings = response.security;
    setMessage("notice", validateOrigin ? "Origin 校验已启用" : "Origin 校验已关闭");
  }, "保存中");
  bindForm("#agent-api-settings-form", async form => {
    const allowInsecure = form.get("allow_insecure") === "on";
    if (allowInsecure && !state.agentAPISettings.allow_insecure && !window.confirm("确认允许受信网络中的非 loopback HTTP Agent API？公网和不受信网络必须使用 HTTPS。")) return;
    const response = await api.updateAgentAPISettings({
      url: String(form.get("url") || "").trim(),
      allow_insecure: allowInsecure,
    });
    state.agentAPISettings = response.agent_api;
    setMessage("notice", "Agent API 全局设置已保存，请重新测试相关 Workspace 连接");
  }, "保存中");
	bindForm("#backend-settings-form", async form => {
		const idleTimeoutSeconds = Math.round(Number(form.get("idle_timeout_minutes") || 0) * 60);
		const turnTimeoutSeconds = Math.round(Number(form.get("turn_timeout_hours") || 0) * 60 * 60);
		const response = await api.updateBackendSettings({
			idle_timeout_seconds: idleTimeoutSeconds,
			turn_timeout_seconds: turnTimeoutSeconds,
		});
		state.backendSettings = response.backend;
		setMessage("notice", "Backend 超时设置已保存，将对后续执行生效");
	}, "保存中");
  document.querySelector<HTMLButtonElement>("#message-attachment-pick")?.addEventListener("click", () => {
    document.querySelector<HTMLInputElement>("#message-attachment-input")?.click();
  });
  document.querySelector<HTMLInputElement>("#message-attachment-input")?.addEventListener("change", async event => {
    const input = event.currentTarget;
    const files = Array.from(input.files || []);
    input.value = "";
    await uploadMessageAttachments(files);
  });
  document.querySelector<HTMLFormElement>("#workspace-takeover-form")?.addEventListener("submit", event => {
    event.preventDefault();
    const element = event.currentTarget;
    const form = new FormData(element);
    const sourceID = String(form.get("source_id") || "");
    const button = element.querySelector<HTMLElement>('button[type="submit"]');
    void runWithFeedback(button, "接管中", async () => {
      await api.takeoverWorkspace(sourceID, {
        name: String(form.get("name") || ""), locality: String(form.get("transport")) === "ssh" ? "remote" : "local", transport: String(form.get("transport") || "native"),
        root_path: String(form.get("root_path") || ""), ssh_host: String(form.get("ssh_host") || ""),
        ssh_user: String(form.get("ssh_user") || ""), ssh_port: Number(form.get("ssh_port") || 22),
        ssh_auth: String(form.get("ssh_auth") || "auto"), ssh_password: String(form.get("ssh_password") || ""),
        distro: String(form.get("distro") || ""), reuse_remote_credential: form.get("reuse_remote_credential") === "on",
      });
      element.closest<HTMLDialogElement>("dialog")?.close();
      setMessage("notice", "已创建本机 Workspace，远端镜像保持只读");
      await loadAll();
      render();
    });
  });
  bindRemoveAttachmentButtons();
  document.querySelector<HTMLFormElement>("#message-form")?.addEventListener("submit", async event => {
    event.preventDefault();
    if (messageSubmitPending) return;
    const form = new FormData(event.currentTarget);
    const content = String(form.get("content") || "").trim();
    if (!state.selectedTask) return;
    const taskID = state.selectedTask.task.id;
    const agentID = state.selectedTaskAgent;
    const attachments = [...currentAttachmentDrafts()];
    if (!content && attachments.length === 0) return;
    messageSubmitPending = true;
    state.taskDrafts[agentID] = "";
    if (state.selectedTaskAgent === agentID) state.taskDraft = "";
    state.taskAttachmentDrafts[agentID] = [];
    const clearTextarea = document.querySelector<HTMLTextAreaElement>("#message-form textarea");
    if (clearTextarea) {
      clearTextarea.value = "";
      clearTextarea.style.height = "auto";
      syncTaskComposerState(clearTextarea);
    }
    document.querySelector(".composer-attachment-tray")?.remove();
    const attachmentButton = document.querySelector<HTMLButtonElement>("#message-attachment-pick");
    if (attachmentButton) attachmentButton.innerHTML = icon("attachment");
    persistNavigationState();
    let accepted = false;
    try {
      const handled = attachments.length === 0 && await executeTaskSlashCommand(content);
      accepted = handled;
      if (!handled) {
        const submission = await api.agentMessage(taskID, agentID, content, attachments.map(item => item.id));
        accepted = true;
        if (!submission.started) setMessage("notice", `${agentID} 正在执行，消息已排队；输入 /interrupt 可中断当前 Round`);
        await refreshTaskRuntime(taskID);
        requestConversationBottom();
        updateTaskLiveRegions();
      }
      const menu = document.querySelector<HTMLElement>("#slash-command-menu");
      if (menu) menu.hidden = true;
    } catch (error) {
      if (!accepted && state.selectedTask?.task.id === taskID && state.selectedTaskAgent === agentID) {
        state.taskDrafts[agentID] = content;
        state.taskAttachmentDrafts[agentID] = attachments;
        state.taskDraft = content;
        const currentTextarea = document.querySelector<HTMLTextAreaElement>("#message-form textarea");
        if (currentTextarea) currentTextarea.value = content;
        refreshMessageAttachmentDOM();
      }
      persistNavigationState();
      const message = error instanceof Error ? error.message : String(error);
      setMessage("error", accepted ? `消息已发送，但刷新失败：${message}` : message);
    } finally {
      messageSubmitPending = false;
      syncTaskComposerState(document.querySelector<HTMLTextAreaElement>("#message-form textarea"));
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
  document.querySelector("#close-task-tool")?.addEventListener("click", () => {
    if (state.taskTool === "hardware") stopHardwarePanel();
    state.taskTool = "";
    render();
  });
  document.querySelector<HTMLTextAreaElement>("#message-form textarea")?.addEventListener("paste", event => {
    const files = clipboardImageFiles(event);
    if (!files.length) return;
    event.preventDefault();
    void uploadMessageAttachments(files);
  });
  bindTaskToolLayout();
  if (state.taskTool === "hardware" && state.selectedTask) bindHardwarePanel(state.selectedTask, setMessage);
  if (state.view === "knowledge" && !state.selectedProject && !state.selectedTask) bindKnowledgeWorkspace({
    projects: state.projects,
    workspaces: state.workspaces,
    knowledge: state.knowledge,
    libraries: state.knowledgeLibraries,
    proposals: state.knowledgeProposals,
    refreshData: loadKnowledgeData,
    render,
    setMessage,
  });
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

function bindForm(selector: string, action: (form: FormData) => Promise<void>, pendingLabel = "提交中", refreshAfter = true): void {
  document.querySelector<HTMLFormElement>(selector)?.addEventListener("submit", async event => {
    event.preventDefault();
    const form = event.currentTarget;
    if (form.dataset.submitting === "true") return;
    const dialog = form.closest("dialog");
    const submitButton = event.submitter instanceof HTMLButtonElement
      ? event.submitter
      : form.querySelector<HTMLButtonElement>('button[type="submit"]');
    const originalButtonHTML = submitButton?.innerHTML || "";
    form.dataset.submitting = "true";
    form.setAttribute("aria-busy", "true");
    if (submitButton) {
      submitButton.disabled = true;
      submitButton.innerHTML = `${icon("spinner", true)}<span>${pendingLabel}</span>`;
    }
    form.querySelector<HTMLElement>("[data-form-error]")?.remove();
    try {
      const payload = new FormData(form);
      if (submitButton?.name) payload.set(submitButton.name, submitButton.value);
      await action(payload);
      dialog?.close();
      if (refreshAfter) await refresh();
      else render();
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      setMessage("error", message);
      if (dialog?.open) {
        const feedback = document.createElement("div");
        feedback.className = "form-error";
        feedback.dataset.formError = "true";
        feedback.setAttribute("role", "alert");
        feedback.textContent = message;
        form.querySelector(".dialog-actions")?.before(feedback);
      } else {
        render();
      }
    } finally {
      delete form.dataset.submitting;
      form.removeAttribute("aria-busy");
      if (submitButton?.isConnected) {
        submitButton.disabled = false;
        submitButton.innerHTML = originalButtonHTML;
      }
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
  if (!state.selectedTask || !state.taskConversationBefore || loadingOlderConversation) return;
  loadingOlderConversation = true;
  const list = document.querySelector<HTMLElement>("#conversation-list");
  const ui = captureRegionUI(list);
  const previousHeight = list?.scrollHeight || 0;
  const previousTop = list?.scrollTop || 0;
  try {
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
      restoreRegionUI(list, ui, false);
      list.scrollTop = previousTop + Math.max(0, list.scrollHeight - previousHeight);
      bindConversationLiveControls();
    }
    const count = document.querySelector<HTMLElement>("#conversation-filter-count");
    if (count) count.textContent = `${state.taskConversation.length} 条已加载`;
  } finally {
    loadingOlderConversation = false;
  }
}

function bindConversationLiveControls(): void {
  const list = document.querySelector<HTMLElement>("#conversation-list");
  bindMessageBubbleControls(list || document);
  document.querySelector("#load-older-conversation")?.addEventListener("click", () => {
    void loadOlderConversation();
  });
  if (list && list.dataset.paginationBound !== "true") {
    list.dataset.paginationBound = "true";
    let previousTop = list.scrollTop;
    list.addEventListener("scroll", () => {
      const currentTop = list.scrollTop;
      if (currentTop < previousTop && currentTop <= 80 && state.taskConversationHasMore) {
        void loadOlderConversation();
      }
      previousTop = currentTop;
    }, {passive: true});
  }
  if (list && list.dataset.bottomPinBound !== "true") {
    list.dataset.bottomPinBound = "true";
    for (const eventName of ["wheel", "touchstart", "pointerdown"] as const) {
      list.addEventListener(eventName, cancelConversationBottomPin, {passive: true});
    }
  }
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
  if (appPointerActive || hasActiveTextEditor()) return;
  if (document.activeElement instanceof HTMLTextAreaElement && document.activeElement.closest("#message-form")) return;
  if (state.view === "sync" && isSyncSettingsFormEditing(document.querySelector<HTMLFormElement>("#sync-settings-form"), document.activeElement)) return;
  render();
}

document.addEventListener("close", event => {
  if (event.target instanceof HTMLDialogElement && event.target.id === "model-dialog" && activeModelDetection) {
    const session = activeModelDetection;
    session.source.close();
    session.status = "cancelling";
    activeModelDetection = null;
    void api.cancelModelDetectionJob(session.providerID, session.jobID).catch(() => undefined);
  }
  if (event.target instanceof HTMLDialogElement) flushDeferredRender();
}, true);
document.addEventListener("focusout", () => window.setTimeout(flushDeferredRender, 0), true);

// Event-driven list refresh: one global SSE stream keeps the task list (and
// project/workspace labels) fresh without polling. The task detail view keeps
// its own per-task SSE; while a detail is open we still refresh list state in
// the background but only re-render when not inside the detail.
let globalEvents: EventSource | null = null;

async function refreshListData(): Promise<void> {
  if (!state.auth?.authenticated) return;
  if (listRefreshInFlight) {
	listRefreshQueued = true;
	return;
  }
  listRefreshInFlight = true;
  try {
	do {
	  listRefreshQueued = false;
	  try {
		const [tasks, projects, workspaces] = await Promise.all([api.tasks("", {limit: initialListPageSize}), api.projects({limit: initialListPageSize}), api.workspaces()]);
		state.tasks = tasks.tasks || [];
		state.projects = projects.projects || [];
		state.workspaces = workspaces.workspaces || [];
		listPages.projects = {cursor: projects.next_cursor || "", hasMore: Boolean(projects.has_more), loadingMore: false};
		listPages.tasks = {cursor: tasks.next_cursor || "", hasMore: Boolean(tasks.has_more), loadingMore: false};
		const listPageVisible = state.view === "projects" || state.view === "tasks" || Boolean(state.selectedProject);
		if (!state.selectedTask && listPageVisible) {
		  if (taskListPointerActive) state.renderPending = true;
		  else render();
		}
	  } catch {
		// Transient failures are retried by a queued event or SSE reconnect.
	  }
	} while (listRefreshQueued);
  } finally {
	listRefreshInFlight = false;
  }
}

app?.addEventListener("pointerdown", event => {
  if (event.isPrimary) appPointerActive = true;
});
window.addEventListener("pointerup", () => {
  if (!appPointerActive) return;
  appPointerActive = false;
  window.setTimeout(flushDeferredRender, 0);
});
window.addEventListener("pointercancel", () => {
  appPointerActive = false;
  flushDeferredRender();
});
window.addEventListener("blur", () => {
  appPointerActive = false;
  taskListPointerActive = false;
  flushDeferredRender();
});

window.addEventListener("pointerup", () => {
  if (!taskListPointerActive) return;
  taskListPointerActive = false;
  window.setTimeout(flushDeferredRender, 0);
});
window.addEventListener("pointercancel", () => {
  taskListPointerActive = false;
  flushDeferredRender();
});

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
    if (eventRefreshesTaskList(type)) {
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
window.setInterval(updateSystemUptime, 30_000);
render();
void bootstrap();
