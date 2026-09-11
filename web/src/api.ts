import type {
  AgentAPISettings,
	BackendSettings,
  Attachment,
  AuthStatus,
  CodexAccount,
  CodexLogin,
  ChannelEndpoint,
  ChannelDelivery,
  ChannelHandoff,
  ChannelInstance,
  ChannelOnboardingSession,
  ChannelPlugin,
  ChannelPurgePreview,
  DetectedModel,
  EnvGroup,
  Knowledge,
  KnowledgeLibrary,
  KnowledgeProposal,
  KnowledgeReviewSettings,
  Model,
  Project,
  ProductLine,
  Provider,
  ProxySettings,
  ManagedProxyView,
  SecuritySettings,
  SyncSettings,
  SyncPreview,
	SyncRunProgress,
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
	SSHHostKeyInfo,
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
    if (options.body && !(options.body instanceof FormData) && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
    if (this.csrf && options.method && !["GET", "HEAD"].includes(options.method)) {
      headers.set("X-CSRF-Token", this.csrf);
    }
    const response = await fetch(path, {...options, headers, credentials: "same-origin", cache: "no-store"});
    const body = await response.json().catch(() => ({})) as Record<string, unknown>;
    if (!response.ok) {
      const error = new Error(String(body.message || body.error || `HTTP ${response.status}`)) as Error & {code?: string; status?: number};
      error.code = String(body.error || "");
      error.status = response.status;
      throw error;
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

  recoverPassword(payload: {setup_token: string; username: string; new_password: string}): Promise<AuthStatus> {
    return this.request<AuthStatus>("/api/v1/auth/recover", {method: "POST", body: JSON.stringify(payload)});
  }

  changePassword(payload: {current_password: string; new_password: string}): Promise<{ok: boolean}> {
    return this.request("/api/v1/auth/password", {method: "POST", body: JSON.stringify(payload)});
  }

  logout(): Promise<{ok: boolean}> {
    return this.request("/api/v1/auth/logout", {method: "POST", body: "{}"});
  }

  system(): Promise<{system: SystemInfo}> {
    return this.request("/api/v1/system");
  }

  proxySettings(): Promise<{proxy: ProxySettings; managed?: ManagedProxyView}> {
    return this.request("/api/v1/settings/proxy");
  }

  updateProxySettings(payload: ProxySettings): Promise<{proxy: ProxySettings}> {
    return this.request("/api/v1/settings/proxy", {method: "PUT", body: JSON.stringify(payload)});
  }

  testProxySettings(payload: ProxySettings): Promise<{ok: boolean; status_code: number; elapsed_ms: number}> {
    return this.request("/api/v1/settings/proxy/test", {method: "POST", body: JSON.stringify(payload)});
  }

  importProxySubscription(payload: {profile_id?: string; name?: string; subscription_url?: string; subscription_yaml?: string; selected_node_id?: string; refresh_interval_minutes?: number}): Promise<{proxy: ProxySettings; managed: ManagedProxyView}> {
    return this.request("/api/v1/settings/proxy/subscription", {method: "POST", body: JSON.stringify(payload)});
  }

  refreshProxySubscription(): Promise<{proxy: ProxySettings; managed: ManagedProxyView}> {
    return this.request("/api/v1/settings/proxy/subscription/refresh", {method: "POST", body: "{}"});
  }

  refreshProxyProfile(id: string): Promise<{proxy: ProxySettings; managed: ManagedProxyView}> {
    return this.request(`/api/v1/settings/proxy/profiles/${encodeURIComponent(id)}/refresh`, {method: "POST", body: "{}"});
  }

  updateProxyProfile(id: string, payload: {name?: string; selected_node_id?: string; refresh_interval_minutes?: number}): Promise<{proxy: ProxySettings; managed: ManagedProxyView}> {
    return this.request(`/api/v1/settings/proxy/profiles/${encodeURIComponent(id)}`, {method: "PATCH", body: JSON.stringify(payload)});
  }

  activateProxyProfile(id: string): Promise<{proxy: ProxySettings; managed: ManagedProxyView}> {
    return this.request(`/api/v1/settings/proxy/profiles/${encodeURIComponent(id)}/activate`, {method: "POST", body: "{}"});
  }

  deleteProxyProfile(id: string): Promise<{proxy: ProxySettings; managed: ManagedProxyView}> {
    return this.request(`/api/v1/settings/proxy/profiles/${encodeURIComponent(id)}`, {method: "DELETE"});
  }

  testProxyNode(profileID: string, nodeID: string): Promise<{ok: boolean; status_code: number; elapsed_ms: number}> {
    return this.request(`/api/v1/settings/proxy/profiles/${encodeURIComponent(profileID)}/nodes/${encodeURIComponent(nodeID)}/test`, {method: "POST", body: "{}"});
  }

  securitySettings(): Promise<{security: SecuritySettings}> {
    return this.request("/api/v1/settings/security");
  }

  updateSecuritySettings(payload: {validate_origin: boolean}): Promise<{security: SecuritySettings}> {
    return this.request("/api/v1/settings/security", {method: "PUT", body: JSON.stringify(payload)});
  }

  agentAPISettings(): Promise<{agent_api: AgentAPISettings}> {
    return this.request("/api/v1/settings/agent-api");
  }

  updateAgentAPISettings(payload: {url: string; allow_insecure: boolean}): Promise<{agent_api: AgentAPISettings}> {
    return this.request("/api/v1/settings/agent-api", {method: "PUT", body: JSON.stringify(payload)});
  }

  backendSettings(): Promise<{backend: BackendSettings}> {
		return this.request("/api/v1/settings/backend");
	}

	updateBackendSettings(payload: {idle_timeout_seconds: number; turn_timeout_seconds: number}): Promise<{backend: BackendSettings}> {
		return this.request("/api/v1/settings/backend", {method: "PUT", body: JSON.stringify(payload)});
	}

  syncSettings(): Promise<{sync: SyncSettings}> {
    return this.request("/api/v1/settings/sync");
  }

  updateSyncSettings(payload: Record<string, unknown>): Promise<{sync: SyncSettings}> {
    return this.request("/api/v1/settings/sync", {method: "PUT", body: JSON.stringify(payload)});
  }

  syncStatus(): Promise<{state: SyncState; pending: number; run: SyncRunProgress}> {
    return this.request("/api/v1/settings/sync/status");
  }

  channelProviders(): Promise<{providers: ChannelPlugin[]}> {
    return this.request("/api/v1/channel-providers");
  }

  updateChannelPlugin(id: string, enabled: boolean, revision: number): Promise<{plugin: ChannelPlugin}> {
    return this.request(`/api/v1/channel-plugins/${encodeURIComponent(id)}`, {
      method: "PATCH",
      headers: {"If-Match": `"${revision}"`},
      body: JSON.stringify({enabled}),
    });
  }

  channelInstances(): Promise<{instances: ChannelInstance[]}> {
    return this.request("/api/v1/channel-instances");
  }

  channelInstance(id: string): Promise<{instance: ChannelInstance; endpoints: ChannelEndpoint[]}> {
    return this.request(`/api/v1/channel-instances/${encodeURIComponent(id)}`);
  }

  createChannelInstance(pluginID: string, name: string): Promise<{instance: ChannelInstance}> {
    const idempotencyKey = globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random()}`;
    return this.request("/api/v1/channel-instances", {
      method: "POST",
      headers: {"Idempotency-Key": idempotencyKey},
      body: JSON.stringify({plugin_id: pluginID, name}),
    });
  }

  updateChannelInstance(id: string, payload: {name?: string; config?: Record<string, unknown>}, revision: number): Promise<{instance: ChannelInstance}> {
    return this.request(`/api/v1/channel-instances/${encodeURIComponent(id)}`, {
      method: "PATCH",
      headers: {"If-Match": `"${revision}"`},
      body: JSON.stringify(payload),
    });
  }

  setChannelInstanceEnabled(id: string, enabled: boolean, revision: number): Promise<{instance: ChannelInstance}> {
    return this.request(`/api/v1/channel-instances/${encodeURIComponent(id)}`, {method: "PATCH", headers: {"If-Match": `"${revision}"`}, body: JSON.stringify({enabled})});
  }

  resetChannelBinding(id: string, revision: number): Promise<{instance: ChannelInstance}> {
    return this.request(`/api/v1/channel-instances/${encodeURIComponent(id)}/reset-binding`, {
      method: "POST", headers: {"If-Match": `"${revision}"`}, body: "{}",
    });
  }

  archiveChannelInstance(id: string, revision: number): Promise<{instance: ChannelInstance}> {
    return this.request(`/api/v1/channel-instances/${encodeURIComponent(id)}/archive`, {
      method: "POST", headers: {"If-Match": `"${revision}"`}, body: "{}",
    });
  }

  channelInstancePurgePreview(id: string): Promise<{preview: ChannelPurgePreview}> {
    return this.request(`/api/v1/channel-instances/${encodeURIComponent(id)}/purge-preview`);
  }

  purgeChannelInstance(id: string, confirmationName: string, revision: number): Promise<{ok: boolean}> {
    return this.request(`/api/v1/channel-instances/${encodeURIComponent(id)}/purge`, {
      method: "POST", headers: {"If-Match": `"${revision}"`}, body: JSON.stringify({confirmation_name: confirmationName}),
    });
  }

  updateChannelCredentials(id: string, appID: string, appSecret: string, revision: number): Promise<{instance: ChannelInstance}> {
    return this.request(`/api/v1/channel-instances/${encodeURIComponent(id)}/credentials`, {
      method: "PUT",
      headers: {"If-Match": `"${revision}"`},
      body: JSON.stringify({app_id: appID, app_secret: appSecret}),
    });
  }

  startChannelOnboarding(id: string): Promise<{onboarding: ChannelOnboardingSession}> {
    const idempotencyKey = globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random()}`;
    return this.request(`/api/v1/channel-instances/${encodeURIComponent(id)}/onboarding-sessions`, {
      method: "POST", headers: {"Idempotency-Key": idempotencyKey}, body: "{}",
    });
  }

  channelOnboarding(id: string): Promise<{onboarding: ChannelOnboardingSession}> {
    return this.request(`/api/v1/channel-onboarding-sessions/${encodeURIComponent(id)}`);
  }

  cancelChannelOnboarding(id: string): Promise<{onboarding: ChannelOnboardingSession}> {
    return this.request(`/api/v1/channel-onboarding-sessions/${encodeURIComponent(id)}/cancel`, {method: "POST", body: "{}"});
  }

  channelHandoffs(id: string): Promise<{handoffs: ChannelHandoff[]}> {
    return this.request(`/api/v1/channel-instances/${encodeURIComponent(id)}/handoffs`);
  }

  channelDeliveries(id: string): Promise<{deliveries: ChannelDelivery[]}> {
    return this.request(`/api/v1/channel-instances/${encodeURIComponent(id)}/deliveries`);
  }

  replayChannelDelivery(id: string): Promise<{delivery: ChannelDelivery}> {
    return this.request(`/api/v1/channel-deliveries/${encodeURIComponent(id)}/replay`, {method: "POST", body: "{}"});
  }

  skipChannelDelivery(id: string): Promise<{ok: boolean}> {
    return this.request(`/api/v1/channel-deliveries/${encodeURIComponent(id)}/skip`, {method: "POST", body: "{}"});
  }

  channelKnowledgePolicies(id: string): Promise<{policies: Array<{id: string; endpoint: string; fixed_index_entry_id: string; scope_mode: "all" | "selected"; revision: number; grants: Array<{knowledge_entry_id: string; grant_scope: string}>}>}> {
    return this.request(`/api/v1/channel-instances/${encodeURIComponent(id)}/knowledge-policy`);
  }

  updateChannelKnowledgePolicy(id: string, endpoint: string, scopeMode: "all" | "selected", revision: number, grants: Array<{knowledge_entry_id: string; grant_scope: string}>): Promise<{policies: unknown[]}> {
    return this.request(`/api/v1/channel-instances/${encodeURIComponent(id)}/knowledge-policy`, {method: "PUT", headers: {"If-Match": `"${revision}"`}, body: JSON.stringify({endpoint, scope_mode: scopeMode, grants})});
  }

  channelKnowledgeRecords(id: string): Promise<{records: Array<{id: string; question: string; answer: string; visibility: string; authority_status: string}>}> {
    return this.request(`/api/v1/channel-instances/${encodeURIComponent(id)}/knowledge-records`);
  }

  promoteChannelKnowledgeRecord(id: string, title: string, body: string): Promise<{ok: boolean}> {
    return this.request(`/api/v1/channel-knowledge-records/${encodeURIComponent(id)}/promote`, {method: "POST", body: JSON.stringify({title, body})});
  }

  syncPreview(): Promise<{preview: SyncPreview}> {
    return this.request("/api/v1/settings/sync/preview");
  }

  runSync(): Promise<{state: SyncState; pending: number; summary: SyncPreview}> {
    return this.request("/api/v1/settings/sync/run", {method: "POST", body: "{}"});
  }

  syncConflicts(): Promise<{conflicts: SyncConflict[]}> {
    return this.request("/api/v1/settings/sync/conflicts");
  }

  projects(options: {limit?: number; cursor?: string; summary?: boolean} = {}): Promise<{projects: Project[]; has_more?: boolean; next_cursor?: string}> {
    const query = new URLSearchParams();
    if (options.limit) query.set("limit", String(options.limit));
    if (options.cursor) query.set("cursor", options.cursor);
    if (options.summary) query.set("summary", "true");
    return this.request(`/api/v1/projects${query.size ? `?${query}` : ""}`);
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

  workspaceHostKey(id: string): Promise<{host_key: SSHHostKeyInfo}> {
    return this.request(`/api/v1/workspaces/${encodeURIComponent(id)}/host-key`);
  }

  trustWorkspaceHostKey(id: string, fingerprint: string): Promise<{host_key: SSHHostKeyInfo}> {
    return this.request(`/api/v1/workspaces/${encodeURIComponent(id)}/host-key/trust`, {
      method: "POST", body: JSON.stringify({fingerprint}),
    });
  }

  deleteWorkspace(id: string): Promise<{ok: boolean}> {
    return this.request(`/api/v1/workspaces/${encodeURIComponent(id)}`, {method: "DELETE"});
  }

  retireRemoteWorkspaceMirror(id: string): Promise<{ok: boolean; owner_device_id: string; synchronized: boolean}> {
    return this.request(`/api/v1/workspaces/${encodeURIComponent(id)}/remote-mirror`, {method: "DELETE"});
  }

  updateWorkspace(id: string, payload: Record<string, unknown>): Promise<{workspace: Workspace}> {
    return this.request(`/api/v1/workspaces/${encodeURIComponent(id)}`, {method: "PUT", body: JSON.stringify(payload)});
  }

  takeoverWorkspace(id: string, payload: Record<string, unknown>): Promise<{workspace: Workspace}> {
    return this.request(`/api/v1/workspaces/${encodeURIComponent(id)}/takeover`, {method: "POST", body: JSON.stringify(payload)});
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

  createModelDetectionJob(providerID: string): Promise<{job: {id: string; provider_id: string; status: string}}> {
    return this.request(`/api/v1/providers/${encodeURIComponent(providerID)}/model-detection-jobs`, {method: "POST", body: "{}"});
  }

  cancelModelDetectionJob(providerID: string, jobID: string): Promise<{job: {id: string; status: string; completed: number; total: number}}> {
    return this.request(`/api/v1/providers/${encodeURIComponent(providerID)}/model-detection-jobs/${encodeURIComponent(jobID)}/cancel`, {method: "POST", body: "{}"});
  }

  modelDetectionEventsURL(providerID: string, jobID: string): string {
    return `/api/v1/providers/${encodeURIComponent(providerID)}/model-detection-jobs/${encodeURIComponent(jobID)}/events`;
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

  importLocalCodexAccount(): Promise<{account: CodexAccount}> {
    return this.request("/api/v1/codex-accounts/import-local", {method: "POST", body: "{}"});
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

  tasks(projectID = "", options: {limit?: number; cursor?: string; summary?: boolean} = {}): Promise<{tasks: Task[]; has_more?: boolean; next_cursor?: string}> {
    const query = new URLSearchParams();
    if (projectID) query.set("project_id", projectID);
    if (options.limit) query.set("limit", String(options.limit));
    if (options.cursor) query.set("cursor", options.cursor);
    if (options.summary) query.set("summary", "true");
    return this.request(`/api/v1/tasks${query.size ? `?${query}` : ""}`);
  }

  createTask(payload: Record<string, unknown>): Promise<{task: Task; turn?: {id: string}; started?: boolean; start_error?: string}> {
    return this.request("/api/v1/tasks", {method: "POST", body: JSON.stringify(payload)});
  }

  startTask(id: string): Promise<{task: Task; turn?: {id: string}; started: boolean; start_error?: string}> {
    return this.request(`/api/v1/tasks/${encodeURIComponent(id)}/start`, {method: "POST", body: "{}"});
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

  retireRemoteTaskMirror(id: string): Promise<{ok: boolean; source_task_id: string; owner_device_id: string}> {
    return this.request(`/api/v1/tasks/${encodeURIComponent(id)}/remote-mirror`, {method: "DELETE"});
  }

  takeoverTask(id: string, payload: Record<string, unknown>): Promise<{task: Task; hardware: HardwareGroup[]}> {
    return this.request(`/api/v1/tasks/${encodeURIComponent(id)}/takeover`, {method: "POST", body: JSON.stringify(payload)});
  }

  message(taskID: string, content: string): Promise<{turn: {id: string}}> {
    return this.request(`/api/v1/tasks/${taskID}/messages`, {method: "POST", body: JSON.stringify({content})});
  }

  agentMessage(taskID: string, agentID: string, content: string, attachmentIDs: string[] = []): Promise<{turn?: {id: string}; queued: boolean; started: boolean}> {
    return this.request(`/api/v1/tasks/${taskID}/agents/${encodeURIComponent(agentID)}/messages`, {
      method: "POST",
      body: JSON.stringify({content, attachment_ids: attachmentIDs}),
    });
  }

  uploadAttachment(taskID: string, file: File): Promise<{attachment: Attachment}> {
    const form = new FormData();
    form.set("file", file, file.name);
    return this.request(`/api/v1/tasks/${encodeURIComponent(taskID)}/attachments`, {method: "POST", body: form});
  }

  deleteAttachment(taskID: string, attachmentID: string): Promise<{ok: boolean}> {
    return this.request(`/api/v1/tasks/${encodeURIComponent(taskID)}/attachments/${encodeURIComponent(attachmentID)}`, {method: "DELETE"});
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

  hardwareHostKey(taskID: string, hardwareID: string): Promise<{host_key: SSHHostKeyInfo}> {
    return this.request(`/api/v1/tasks/${taskID}/hardware/${encodeURIComponent(hardwareID)}/host-key?transport=network`);
  }

  trustHardwareHostKey(taskID: string, hardwareID: string, fingerprint: string): Promise<{host_key: SSHHostKeyInfo}> {
    return this.request(`/api/v1/tasks/${taskID}/hardware/${encodeURIComponent(hardwareID)}/host-key/trust?transport=network`, {
      method: "POST", body: JSON.stringify({fingerprint}),
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

  knowledge(scope = "", projectID = "", status = "", options: {limit?: number; cursor?: string; summary?: boolean} = {}): Promise<{knowledge: Knowledge[]; proposals?: KnowledgeProposal[]; review_settings?: KnowledgeReviewSettings; has_more?: boolean; next_cursor?: string}> {
    const query = new URLSearchParams();
    if (scope) query.set("scope", scope);
    if (projectID) query.set("project_id", projectID);
    if (status) query.set("status", status);
    if (options.limit) query.set("limit", String(options.limit));
    if (options.cursor) query.set("cursor", options.cursor);
    if (options.summary) query.set("summary", "true");
    return this.request(`/api/v1/knowledge?${query}`);
  }

  knowledgeEntry(id: string): Promise<{knowledge: Knowledge}> {
    return this.request(`/api/v1/knowledge/${encodeURIComponent(id)}`);
  }

  knowledgeLibraries(): Promise<{libraries: KnowledgeLibrary[]}> {
    return this.request("/api/v1/knowledge/libraries");
  }

  bindKnowledgeLibrary(id: string, projectID: string, bindingMode: "project" | "external"): Promise<{ok: boolean}> {
    return this.request(`/api/v1/knowledge/libraries/${encodeURIComponent(id)}/bind`, {method: "POST", body: JSON.stringify({project_id: projectID, binding_mode: bindingMode})});
  }

  unbindKnowledgeLibrary(id: string): Promise<{ok: boolean}> {
    return this.request(`/api/v1/knowledge/libraries/${encodeURIComponent(id)}/unbind`, {method: "POST", body: "{}"});
  }

  deleteKnowledgeLibrary(id: string): Promise<{ok: boolean}> {
    return this.request(`/api/v1/knowledge/libraries/${encodeURIComponent(id)}`, {method: "DELETE"});
  }

  detachProjectKnowledge(projectID: string): Promise<{ok: boolean}> {
    return this.request(`/api/v1/projects/${encodeURIComponent(projectID)}/knowledge/detach`, {method: "POST", body: "{}"});
  }

  deleteProjectKnowledge(projectID: string): Promise<{ok: boolean}> {
    return this.request(`/api/v1/projects/${encodeURIComponent(projectID)}/knowledge`, {method: "DELETE"});
  }

  approveKnowledgeProposal(id: string): Promise<{ok: boolean; knowledge?: Knowledge; proposal?: KnowledgeProposal}> {
    return this.request(`/api/v1/knowledge/proposals/${encodeURIComponent(id)}/approve`, {method: "POST", body: "{}"});
  }

  rejectKnowledgeProposal(id: string): Promise<{ok: boolean; proposal?: KnowledgeProposal}> {
    return this.request(`/api/v1/knowledge/proposals/${encodeURIComponent(id)}/reject`, {method: "POST", body: "{}"});
  }

  updateKnowledgeReviewSettings(autoApprove: boolean): Promise<{review_settings: KnowledgeReviewSettings}> {
    return this.request("/api/v1/settings/knowledge-review", {method: "PUT", body: JSON.stringify({auto_approve: autoApprove})});
  }

  batchKnowledgeProposals(payload: {action: "approve" | "reject"; proposal_ids: string[]; legacy_ids: string[]}): Promise<{ok: boolean; processed: string[]; failures: Array<{id: string; error: string}>}> {
    return this.request("/api/v1/knowledge/proposals/batch", {method: "POST", body: JSON.stringify(payload)});
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
