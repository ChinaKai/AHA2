import type {
  AuthStatus,
  DetectedModel,
  EnvGroup,
  Knowledge,
  Model,
  Project,
  Provider,
  PromptTemplate,
  SystemInfo,
  Task,
  TaskContextDetail,
  TaskDetail,
  TaskAgent,
  ConversationCategory,
  ConversationPage,
  Workspace,
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

  interruptTurn(id: string): Promise<{ok: boolean}> {
    return this.request(`/api/v1/turns/${id}/interrupt`, {method: "POST", body: "{}"});
  }

  interruptRound(id: string): Promise<{ok: boolean}> {
    return this.request(`/api/v1/rounds/${id}/interrupt`, {method: "POST", body: "{}"});
  }

  knowledge(scope = "", projectID = ""): Promise<{knowledge: Knowledge[]}> {
    const query = new URLSearchParams();
    if (scope) query.set("scope", scope);
    if (projectID) query.set("project_id", projectID);
    return this.request(`/api/v1/knowledge?${query}`);
  }

  createKnowledge(payload: Record<string, unknown>): Promise<{knowledge: Knowledge}> {
    return this.request("/api/v1/knowledge", {method: "POST", body: JSON.stringify(payload)});
  }

  verifyKnowledge(id: string): Promise<{ok: boolean}> {
    return this.request(`/api/v1/knowledge/${id}/verify`, {method: "POST", body: "{}"});
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
