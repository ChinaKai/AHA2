import type {
  AuthStatus,
  CodexAccount,
  CodexLogin,
  DetectedModel,
  EnvGroup,
  Knowledge,
  Model,
  Project,
  ProductLine,
  Provider,
  ProxySettings,
  SyncSettings,
  SyncState,
  SyncConflict,
  PromptTemplate,
  SystemInfo,
  Task,
  TaskContextDetail,
  TaskDetail,
  TaskAgent,
  ConversationCategory,
  ConversationPage,
  HardwareGroup,
  HardwareIOPage,
  HardwareTerminalStatus,
  SerialPort,
  Workspace,
  Skill,
} from "./types.js";

class APIClient {
  private csrf = "";

  setCSRF(value?: string): void {
    this.csrf = value || "";
  }

  async request<T>(path: string, options: RequestInit = {}): Promise<T> {
    const headers = new Headers(options.headers || {});
    if (options.body && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
    if (this.csrf && options.method && !["GET", "HEAD"].includes(options.method)) {
      headers.set("X-CSRF-Token", this.csrf);
    }
    const response = await fetch(path, {...options, headers, credentials: "same-origin", cache: "no-store"});
    const body = await response.json().catch(() => ({})) as Record<string, unknown>;
    if (!response.ok) {
      throw new Error(String(body.message || body.error || `HTTP ${response.status}`));
    }
    return body as T;
  }

  authStatus(): Promise<AuthStatus> {
    return this.request<AuthStatus>("/api/v1/auth/status");
  }

  register(payload: Record<string, string>): Promise<AuthStatus> {
    return this.request<AuthStatus>("/api/v1/auth/register", {method: "POST", body: JSON.stringify(payload)});
  }

  login(payload: Record<string, string>): Promise<AuthStatus> {
    return this.request<AuthStatus>("/api/v1/auth/login", {method: "POST", body: JSON.stringify(payload)});
  }

  logout(): Promise<{ok: boolean}> {
    return this.request("/api/v1/auth/logout", {method: "POST", body: "{}"});
  }

  system(): Promise<{system: SystemInfo}> {
    return this.request("/api/v1/system");
  }

  proxySettings(): Promise<{proxy: ProxySettings}> {
    return this.request("/api/v1/settings/proxy");
  }

  updateProxySettings(payload: ProxySettings): Promise<{proxy: ProxySettings}> {
    return this.request("/api/v1/settings/proxy", {method: "PUT", body: JSON.stringify(payload)});
  }

  testProxySettings(payload: ProxySettings): Promise<{ok: boolean; status_code: number; elapsed_ms: number}> {
    return this.request("/api/v1/settings/proxy/test", {method: "POST", body: JSON.stringify(payload)});
  }

  syncSettings(): Promise<{sync: SyncSettings}> {
    return this.request("/api/v1/settings/sync");
  }

  updateSyncSettings(payload: Record<string, unknown>): Promise<{sync: SyncSettings}> {
    return this.request("/api/v1/settings/sync", {method: "PUT", body: JSON.stringify(payload)});
  }

  syncStatus(): Promise<{state: SyncState; pending: number}> {
    return this.request("/api/v1/settings/sync/status");
  }

  runSync(): Promise<{state: SyncState; pending: number}> {
    return this.request("/api/v1/settings/sync/run", {method: "POST", body: "{}"});
  }

  syncConflicts(): Promise<{conflicts: SyncConflict[]}> {
    return this.request("/api/v1/settings/sync/conflicts");
  }

  projects(): Promise<{projects: Project[]}> {
    return this.request("/api/v1/projects");
  }

  createProject(payload: Record<string, string>): Promise<{project: Project}> {
    return this.request("/api/v1/projects", {method: "POST", body: JSON.stringify(payload)});
  }

  deleteProject(id: string): Promise<{ok: boolean}> {
    return this.request(`/api/v1/projects/${encodeURIComponent(id)}`, {method: "DELETE"});
  }

  updateProject(id: string, payload: Record<string, string>): Promise<{project: Project}> {
    return this.request(`/api/v1/projects/${encodeURIComponent(id)}`, {method: "PUT", body: JSON.stringify(payload)});
  }

  workspaces(projectID = ""): Promise<{workspaces: Workspace[]}> {
    const query = projectID ? `?project_id=${encodeURIComponent(projectID)}` : "";
    return this.request(`/api/v1/workspaces${query}`);
  }

  createWorkspace(payload: Record<string, unknown>): Promise<{workspace: Workspace}> {
    return this.request("/api/v1/workspaces", {method: "POST", body: JSON.stringify(payload)});
  }

  detectWorkspace(id: string): Promise<{workspace: Workspace}> {
    return this.request(`/api/v1/workspaces/${id}/detect`, {method: "POST", body: "{}"});
  }

  deleteWorkspace(id: string): Promise<{ok: boolean}> {
    return this.request(`/api/v1/workspaces/${encodeURIComponent(id)}`, {method: "DELETE"});
  }

  updateWorkspace(id: string, payload: Record<string, unknown>): Promise<{workspace: Workspace}> {
    return this.request(`/api/v1/workspaces/${encodeURIComponent(id)}`, {method: "PUT", body: JSON.stringify(payload)});
  }

  models(): Promise<{models: Model[]}> {
    return this.request("/api/v1/models");
  }

  createModel(payload: Record<string, unknown>): Promise<{model: Model}> {
    return this.request("/api/v1/models", {method: "POST", body: JSON.stringify(payload)});
  }

  envGroups(): Promise<{env_groups: EnvGroup[]}> {
    return this.request("/api/v1/env-groups");
  }

  createEnvGroup(payload: Record<string, unknown>): Promise<{env_group: EnvGroup}> {
    return this.request("/api/v1/env-groups", {method: "POST", body: JSON.stringify(payload)});
  }

  detectModels(payload: Record<string, unknown>): Promise<{models: DetectedModel[]; auth_style: string; anthropic_base_url?: string}> {
    return this.request("/api/v1/providers/detect-models", {method: "POST", body: JSON.stringify(payload)});
  }

  addModels(payload: Record<string, unknown>): Promise<{models: Model[]; provider_id: string; skipped?: number}> {
    return this.request("/api/v1/providers/add-models", {method: "POST", body: JSON.stringify(payload)});
  }

  providers(): Promise<{providers: Provider[]}> {
    return this.request("/api/v1/providers");
  }

  createProvider(payload: Record<string, string>): Promise<{provider: Provider}> {
    return this.request("/api/v1/providers", {method: "POST", body: JSON.stringify(payload)});
  }

  deleteProvider(id: string): Promise<{ok: boolean; warning?: string}> {
    return this.request(`/api/v1/providers/${encodeURIComponent(id)}`, {method: "DELETE"});
  }

  updateProvider(id: string, payload: Record<string, unknown>): Promise<{provider: Provider}> {
    return this.request(`/api/v1/providers/${encodeURIComponent(id)}`, {method: "PUT", body: JSON.stringify(payload)});
  }

  deleteModel(id: string): Promise<{ok: boolean}> {
    return this.request(`/api/v1/models/${encodeURIComponent(id)}`, {method: "DELETE"});
  }

  updateModel(id: string, payload: Record<string, unknown>): Promise<{model: Model}> {
    return this.request(`/api/v1/models/${encodeURIComponent(id)}`, {method: "PUT", body: JSON.stringify(payload)});
  }

  codexAccounts(): Promise<{accounts: CodexAccount[]}> {
    return this.request("/api/v1/codex-accounts");
  }

  startCodexLogin(proxyEnabled: boolean): Promise<{login: CodexLogin}> {
    return this.request("/api/v1/codex-accounts/login", {
      method: "POST", body: JSON.stringify({proxy_enabled: proxyEnabled}),
    });
  }

  submitCodexCallback(id: string, callbackURL: string, label: string): Promise<{login: CodexLogin; account: CodexAccount}> {
    return this.request(`/api/v1/codex-accounts/login/${encodeURIComponent(id)}/callback`, {
      method: "POST", body: JSON.stringify({callback_url: callbackURL, label}),
    });
  }

  deleteCodexAccount(id: string): Promise<{ok: boolean}> {
    return this.request(`/api/v1/codex-accounts/${encodeURIComponent(id)}`, {method: "DELETE"});
  }

  refreshCodexAccount(id: string): Promise<{account: CodexAccount; warnings?: string[]}> {
    return this.request(`/api/v1/codex-accounts/${encodeURIComponent(id)}/refresh`, {
      method: "POST", body: "{}",
    });
  }

  tasks(projectID = ""): Promise<{tasks: Task[]}> {
    const query = projectID ? `?project_id=${encodeURIComponent(projectID)}` : "";
    return this.request(`/api/v1/tasks${query}`);
  }

  createTask(payload: Record<string, unknown>): Promise<{task: Task; turn?: {id: string}; start_error?: string}> {
    return this.request("/api/v1/tasks", {method: "POST", body: JSON.stringify(payload)});
  }

  task(id: string): Promise<TaskDetail> {
    return this.request(`/api/v1/tasks/${id}`);
  }

  conversation(
    id: string,
    options: {before?: number; after?: number; limit?: number; categories?: ConversationCategory[]} = {},
  ): Promise<{conversation: ConversationPage}> {
    const query = new URLSearchParams();
    if (options.before) query.set("before", String(options.before));
    if (options.after) query.set("after", String(options.after));
    if (options.limit) query.set("limit", String(options.limit));
    if (options.categories?.length) query.set("categories", options.categories.join(","));
    return this.request(`/api/v1/tasks/${id}/conversation?${query}`);
  }

  agentConversation(
    id: string,
    agentID: string,
    options: {before?: number; after?: number; limit?: number; categories?: ConversationCategory[]} = {},
  ): Promise<{conversation: ConversationPage}> {
    const query = new URLSearchParams();
    if (options.before) query.set("before", String(options.before));
    if (options.after) query.set("after", String(options.after));
    if (options.limit) query.set("limit", String(options.limit));
    if (options.categories?.length) query.set("categories", options.categories.join(","));
    return this.request(`/api/v1/tasks/${id}/agents/${encodeURIComponent(agentID)}/conversation?${query}`);
  }

  taskContext(id: string): Promise<TaskContextDetail> {
    return this.request(`/api/v1/tasks/${id}/context`);
  }

  agentContext(id: string, agentID: string): Promise<TaskContextDetail> {
    return this.request(`/api/v1/tasks/${id}/agents/${encodeURIComponent(agentID)}/context`);
  }

  taskAgents(id: string): Promise<{agents: TaskAgent[]}> {
    return this.request(`/api/v1/tasks/${id}/agents`);
  }

  deleteTask(id: string): Promise<{ok: boolean}> {
    return this.request(`/api/v1/tasks/${encodeURIComponent(id)}`, {method: "DELETE"});
  }

  message(taskID: string, content: string): Promise<{turn: {id: string}}> {
    return this.request(`/api/v1/tasks/${taskID}/messages`, {method: "POST", body: JSON.stringify({content})});
  }

  agentMessage(taskID: string, agentID: string, content: string): Promise<{turn?: {id: string}; queued: boolean; started: boolean}> {
    return this.request(`/api/v1/tasks/${taskID}/agents/${encodeURIComponent(agentID)}/messages`, {
      method: "POST",
      body: JSON.stringify({content}),
    });
  }

  completeTask(id: string): Promise<{ok: boolean}> {
    return this.request(`/api/v1/tasks/${id}/complete`, {method: "POST", body: "{}"});
  }

  reopenTask(id: string): Promise<{ok: boolean}> {
    return this.request(`/api/v1/tasks/${id}/reopen`, {method: "POST", body: "{}"});
  }

  updateTaskTitle(id: string, title: string): Promise<{task: Task}> {
    return this.request(`/api/v1/tasks/${encodeURIComponent(id)}`, {method: "PATCH", body: JSON.stringify({title})});
  }

  updateTask(id: string, payload: Record<string, unknown>): Promise<{task: Task}> {
    return this.request(`/api/v1/tasks/${encodeURIComponent(id)}`, {method: "PATCH", body: JSON.stringify(payload)});
  }

  updateTaskAgent(id: string, agentID: string, payload: Record<string, unknown>): Promise<{agent: TaskAgent}> {
    return this.request(`/api/v1/tasks/${id}/agents/${encodeURIComponent(agentID)}`, {
      method: "PATCH",
      body: JSON.stringify(payload),
    });
  }

  compactAgentSession(id: string, agentID: string): Promise<{old_backend_session_id: string; status: string}> {
    return this.request(`/api/v1/tasks/${id}/agents/${encodeURIComponent(agentID)}/session/compact`, {
      method: "POST", body: "{}",
    });
  }

  resetAgentSession(id: string, agentID: string): Promise<{old_backend_session_id: string; status: string}> {
    return this.request(`/api/v1/tasks/${id}/agents/${encodeURIComponent(agentID)}/session/reset`, {
      method: "POST", body: "{}",
    });
  }

  serialPorts(): Promise<{ports: SerialPort[]}> {
    return this.request("/api/v1/hardware/serial-ports");
  }

  updateTaskHardware(id: string, groups: Record<string, unknown>[]): Promise<{groups: HardwareGroup[]}> {
    return this.request(`/api/v1/tasks/${id}/hardware`, {
      method: "PUT", body: JSON.stringify({groups}),
    });
  }

  hardwareTerminal(
    taskID: string,
    hardwareID: string,
    transport: "serial" | "network",
    after = 0,
  ): Promise<{group: HardwareGroup; status: HardwareTerminalStatus; stream: HardwareIOPage}> {
    const query = new URLSearchParams({transport, limit: "1000"});
    if (after > 0) query.set("after", String(after));
    return this.request(`/api/v1/tasks/${taskID}/hardware/${encodeURIComponent(hardwareID)}/terminal?${query}`);
  }

  connectHardware(
    taskID: string,
    hardwareID: string,
    transport: "serial" | "network",
  ): Promise<{status: HardwareTerminalStatus}> {
    return this.request(`/api/v1/tasks/${taskID}/hardware/${encodeURIComponent(hardwareID)}/connect?transport=${transport}`, {
      method: "POST", body: "{}",
    });
  }

  disconnectHardware(
    taskID: string,
    hardwareID: string,
    transport: "serial" | "network",
  ): Promise<{status: HardwareTerminalStatus}> {
    return this.request(`/api/v1/tasks/${taskID}/hardware/${encodeURIComponent(hardwareID)}/disconnect?transport=${transport}`, {
      method: "POST", body: "{}",
    });
  }

  sendHardware(
    taskID: string,
    hardwareID: string,
    transport: "serial" | "network",
    data: string,
    encoding: "text" | "hex" | "base64",
  ): Promise<{ok: boolean}> {
    return this.request(`/api/v1/tasks/${taskID}/hardware/${encodeURIComponent(hardwareID)}/send?transport=${transport}`, {
      method: "POST", body: JSON.stringify({data, encoding}),
    });
  }

  interruptTurn(id: string): Promise<{ok: boolean}> {
    return this.request(`/api/v1/turns/${id}/interrupt`, {method: "POST", body: "{}"});
  }

  interruptRound(id: string): Promise<{ok: boolean}> {
    return this.request(`/api/v1/rounds/${id}/interrupt`, {method: "POST", body: "{}"});
  }

  knowledge(scope = "", projectID = "", status = ""): Promise<{knowledge: Knowledge[]}> {
    const query = new URLSearchParams();
    if (scope) query.set("scope", scope);
    if (projectID) query.set("project_id", projectID);
    if (status) query.set("status", status);
    return this.request(`/api/v1/knowledge?${query}`);
  }

  createKnowledge(payload: Record<string, unknown>): Promise<{knowledge: Knowledge}> {
    return this.request("/api/v1/knowledge", {method: "POST", body: JSON.stringify(payload)});
  }

  updateKnowledge(id: string, payload: Record<string, unknown>): Promise<{knowledge: Knowledge}> {
    return this.request(`/api/v1/knowledge/${encodeURIComponent(id)}`, {method: "PUT", body: JSON.stringify(payload)});
  }

  deleteKnowledge(id: string): Promise<{ok: boolean}> {
    return this.request(`/api/v1/knowledge/${encodeURIComponent(id)}`, {method: "DELETE"});
  }

  verifyKnowledge(id: string): Promise<{ok: boolean}> {
    return this.request(`/api/v1/knowledge/${id}/verify`, {method: "POST", body: "{}"});
  }

  feedbackKnowledge(id: string, kind: "helped" | "stale" | "wrong"): Promise<{knowledge: Knowledge}> {
    return this.request(`/api/v1/knowledge/${encodeURIComponent(id)}/feedback`, {method: "POST", body: JSON.stringify({kind})});
  }

  productLines(projectID: string): Promise<{product_lines: ProductLine[]}> {
    return this.request(`/api/v1/projects/${encodeURIComponent(projectID)}/product-lines`);
  }

  createProductLine(projectID: string, payload: Record<string, unknown>): Promise<{product_line: ProductLine}> {
    return this.request(`/api/v1/projects/${encodeURIComponent(projectID)}/product-lines`, {method: "POST", body: JSON.stringify(payload)});
  }

  deleteProductLine(projectID: string, id: string): Promise<{ok: boolean}> {
    return this.request(`/api/v1/projects/${encodeURIComponent(projectID)}/product-lines/${encodeURIComponent(id)}`, {method: "DELETE"});
  }

  skills(scope = "", projectID = ""): Promise<{skills: Skill[]}> {
    const query = new URLSearchParams();
    if (scope) query.set("scope", scope);
    if (projectID) query.set("project_id", projectID);
    return this.request(`/api/v1/skills?${query}`);
  }

  createSkill(payload: Record<string, unknown>): Promise<{skill: Skill}> {
    return this.request("/api/v1/skills", {method: "POST", body: JSON.stringify(payload)});
  }

  updateSkill(id: string, payload: Record<string, unknown>): Promise<{skill: Skill}> {
    return this.request(`/api/v1/skills/${encodeURIComponent(id)}`, {method: "PUT", body: JSON.stringify(payload)});
  }

  deleteSkill(id: string): Promise<{ok: boolean}> {
    return this.request(`/api/v1/skills/${encodeURIComponent(id)}`, {method: "DELETE"});
  }

  promptTemplates(): Promise<{templates: PromptTemplate[]}> {
    return this.request("/api/v1/prompts/templates");
  }

  updatePromptTemplate(id: string, content: string): Promise<{ok: boolean}> {
    return this.request(`/api/v1/prompts/templates/${encodeURIComponent(id)}`, {
      method: "PUT", body: JSON.stringify({content}),
    });
  }

  resetPromptTemplate(id: string): Promise<{ok: boolean}> {
    return this.request(`/api/v1/prompts/templates/${encodeURIComponent(id)}/reset`, {method: "POST", body: "{}"});
  }

}

export const api = new APIClient();
