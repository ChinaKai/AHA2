import type {
  AgentAPISettings,
	BackendSettings,
  Attachment,
  AssistantToolEvent,
  AuthStatus,
  CodexAccount,
  CodexLogin,
  ChannelContact,
  ChannelConversationSummary,
  ChannelDestination,
  ChannelGroupMember,
  ChannelEndpoint,
  ChannelDelivery,
  ChannelDeliveryAttempt,
  ChannelHandoff,
  ChannelInstance,
  ChannelOnboardingOptions,
  ChannelOnboardingSession,
  ChannelPlugin,
  ChannelPurgePreview,
  ChannelTaskRoute,
  DataCleanupResult,
  DataInventory,
  DatabaseStats,
  DetectedModel,
  EnvGroup,
  GeneralSettings,
  Knowledge,
  KnowledgeLibrary,
  KnowledgeProposal,
  KnowledgeReviewSettings,
  Model,
  OwnerAssistant,
  OwnerAssistantView,
  AssistantHint,
  AssistantMessage,
  AssistantModelOption,
  PetState,
  PetStatePatch,
  PetBrainResult,
  PetLayout,
  PetEvent,
  PetNavigationAction,
  PetPrompts,
  PetResetResult,
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
  TaskFacetCounts,
  ConversationCategory,
  ConversationPage,
  HardwareGroup,
  HardwareIOPage,
  HardwareIOPurgePlan,
  HardwareIOPurgeResult,
  HardwareIORetentionSettings,
  HardwareResource,
  HardwareTerminalStatus,
  SerialPort,
	SSHHostKeyInfo,
  Workspace,
  Skill,
  MailAccount,
  MailAttachment,
  MailBinding,
  MailDelivery,
  MailDeliveryAttempt,
  MailMessage,
  MailTemplate,
  MailTemplateVersion,
  TaskMailOptions,
} from "./types.js";

export interface APIRequestSample {
  method: string;
  path: string;
  status: number;
  durationMs: number;
  ok: boolean;
}

export type APIRequestObserver = (sample: APIRequestSample) => void;

let requestObserver: APIRequestObserver | null = null;

// Request timing is strictly opt-in. `?perf=1` installs an observer from main;
// every other visit leaves this null so `request` allocates no sample and keeps
// its original fetch behavior.
export function setAPIRequestObserver(observer: APIRequestObserver | null): void {
  requestObserver = observer;
}

// Only the pathname is recorded. Query strings carry setup tokens, file names,
// and search terms; bodies and headers carry credentials and content. None of
// those belong in performance metrics, and dropping the query also folds
// requests for the same endpoint into one Top-5 entry.
function requestPath(path: string): string {
  const query = path.indexOf("?");
  return query < 0 ? path : path.slice(0, query);
}

class APIClient {
  private csrf = "";

  setCSRF(value?: string): void {
    this.csrf = value || "";
  }

  csrfToken(): string {
    return this.csrf;
  }

  async request<T>(path: string, options: RequestInit = {}): Promise<T> {
    const observer = requestObserver;
    const startedAt = observer ? performance.now() : 0;
    const headers = new Headers(options.headers || {});
    if (options.body && !(options.body instanceof FormData) && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
    if (this.csrf && options.method && !["GET", "HEAD"].includes(options.method)) {
      headers.set("X-CSRF-Token", this.csrf);
    }
    let status = 0;
    let ok = false;
    try {
      const response = await fetch(path, {...options, headers, credentials: "same-origin", cache: "no-store"});
      status = response.status;
      ok = response.ok;
      const body = await response.json().catch(() => ({})) as Record<string, unknown>;
      if (!response.ok) {
        // Mail routes use {"ok":false,"error":{"code","message"}}; other routes
        // use a string error code. Normalize both so callers can branch on code.
        const nested = body.error && typeof body.error === "object" ? body.error as Record<string, unknown> : null;
        const error = new Error(String(body.message || nested?.message || body.error || `HTTP ${response.status}`)) as Error & {code?: string; status?: number};
        error.code = String(nested?.code || (typeof body.error === "string" ? body.error : ""));
        error.status = response.status;
        throw error;
      }
      return body as T;
    } finally {
      // A failed fetch has no response; recording status 0 / ok false keeps the
      // failure visible without inventing an HTTP status.
      if (observer) {
        observer({
          method: (options.method || "GET").toUpperCase(),
          path: requestPath(path),
          status,
          durationMs: performance.now() - startedAt,
          ok,
        });
      }
    }
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

  dataInventory(): Promise<{ok: boolean; inventory: DataInventory}> {
    return this.request("/api/v1/system/data");
  }

  cleanupData(payload: {category: string; keep?: number; confirm: boolean}): Promise<{ok: boolean; result: DataCleanupResult}> {
    return this.request("/api/v1/system/data/cleanup", {method: "POST", body: JSON.stringify(payload)});
  }

  databaseStats(refresh = false): Promise<{ok: boolean; database: DatabaseStats}> {
    return this.request(`/api/v1/system/data/database${refresh ? "?refresh=1" : ""}`);
  }

  updateHardwareIORetention(payload: {retention_days: number; max_delete_rows: number}): Promise<{ok: boolean; retention: HardwareIORetentionSettings}> {
    return this.request("/api/v1/system/data/hardware-io/retention", {method: "PUT", body: JSON.stringify(payload)});
  }

  previewHardwareIOPurge(payload: {retention_days: number; max_delete_rows: number}): Promise<{ok: boolean; plan: HardwareIOPurgePlan}> {
    return this.request("/api/v1/system/data/hardware-io/preview", {method: "POST", body: JSON.stringify(payload)});
  }

  purgeHardwareIO(payload: {retention_days: number; max_delete_rows: number; confirm: boolean}): Promise<{ok: boolean; result: HardwareIOPurgeResult; database?: DatabaseStats}> {
    return this.request("/api/v1/system/data/hardware-io/purge", {method: "POST", body: JSON.stringify(payload)});
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

  updateSecuritySettings(payload: {validate_origin: boolean; access_scope: string}): Promise<{security: SecuritySettings}> {
    return this.request("/api/v1/settings/security", {method: "PUT", body: JSON.stringify(payload)});
  }

  networkSettings(): Promise<{network: NetworkSettings}> {
    return this.request("/api/v1/settings/network");
  }

  updateNetworkSettings(listenAddress: string): Promise<{network: NetworkSettings}> {
    return this.request("/api/v1/settings/network", {method: "PUT", body: JSON.stringify({listen_address: listenAddress})});
  }

  agentAPISettings(): Promise<{agent_api: AgentAPISettings}> {
    return this.request("/api/v1/settings/agent-api");
  }

  updateAgentAPISettings(payload: {url: string; stdio_proxy?: boolean}): Promise<{agent_api: AgentAPISettings}> {
    return this.request("/api/v1/settings/agent-api", {method: "PUT", body: JSON.stringify(payload)});
  }

  backendSettings(): Promise<{backend: BackendSettings}> {
		return this.request("/api/v1/settings/backend");
	}

  generalSettings(): Promise<{ok: boolean; general: GeneralSettings}> {
    return this.request("/api/v1/settings/general");
  }

  updateGeneralSettings(language: string): Promise<{ok: boolean; general: GeneralSettings}> {
    return this.request("/api/v1/settings/general", {method: "PUT", body: JSON.stringify({language})});
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

  syncStatus(): Promise<{state: SyncState; pending: number; run: SyncRunProgress; next_run_at?: string}> {
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

  channelOnboardingOptions(id: string): Promise<{options: ChannelOnboardingOptions}> {
    return this.request(`/api/v1/channel-instances/${encodeURIComponent(id)}/onboarding-options`);
  }

  resetChannelRuntimeMenus(id: string, revision: number): Promise<{command: {id: string; kind: string; state: string}}> {
    const idempotencyKey = globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random()}`;
    return this.request(`/api/v1/channel-instances/${encodeURIComponent(id)}/runtime-menus/reset`, {
      method: "POST", headers: {"If-Match": `"${revision}"`, "Idempotency-Key": idempotencyKey}, body: "{}",
    });
  }

  startChannelOnboarding(id: string, payload: {permission_ids: string[]}): Promise<{onboarding: ChannelOnboardingSession}> {
    const idempotencyKey = globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random()}`;
    return this.request(`/api/v1/channel-instances/${encodeURIComponent(id)}/onboarding-sessions`, {
      method: "POST", headers: {"Idempotency-Key": idempotencyKey}, body: JSON.stringify(payload),
    });
  }

  channelOnboarding(id: string): Promise<{onboarding: ChannelOnboardingSession}> {
    return this.request(`/api/v1/channel-onboarding-sessions/${encodeURIComponent(id)}`);
  }

  cancelChannelOnboarding(id: string): Promise<{onboarding: ChannelOnboardingSession}> {
    return this.request(`/api/v1/channel-onboarding-sessions/${encodeURIComponent(id)}/cancel`, {method: "POST", body: "{}"});
  }

  channelConversations(id: string): Promise<{conversations: ChannelConversationSummary[]}> {
    return this.request(`/api/v1/channel-instances/${encodeURIComponent(id)}/conversations`);
  }

  channelHandoffs(id: string): Promise<{handoffs: ChannelHandoff[]}> {
    return this.request(`/api/v1/channel-instances/${encodeURIComponent(id)}/handoffs`);
  }

  channelDeliveries(id: string): Promise<{deliveries: ChannelDelivery[]}> {
    return this.request(`/api/v1/channel-instances/${encodeURIComponent(id)}/deliveries`);
  }

  channelDeliveryAttempts(id: string): Promise<{attempts: ChannelDeliveryAttempt[]}> {
    return this.request(`/api/v1/channel-deliveries/${encodeURIComponent(id)}/attempts`);
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

  syncPreview(): Promise<{preview: SyncPreview}> {
    return this.request("/api/v1/settings/sync/preview");
  }

  runSync(): Promise<{ok: boolean; run?: SyncRunProgress}> {
    return this.request("/api/v1/settings/sync/run", {method: "POST", body: "{}"});
  }

  syncConflicts(): Promise<{conflicts: SyncConflict[]}> {
    return this.request("/api/v1/settings/sync/conflicts");
  }

  resolveSyncConflict(id: string, action: "local" | "remote" | "ignore"): Promise<{ok: boolean}> {
    return this.request(`/api/v1/settings/sync/conflicts/${encodeURIComponent(id)}/resolve`, {method: "POST", body: JSON.stringify({action})});
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

  detectModels(payload: Record<string, unknown>, options: {signal?: AbortSignal} = {}): Promise<{models: DetectedModel[]; auth_style: string; anthropic_base_url?: string}> {
    return this.request("/api/v1/providers/detect-models", {method: "POST", body: JSON.stringify(payload), signal: options.signal});
  }

  probeModel(providerID: string, modelID: string, authStyle: string): Promise<{model: DetectedModel; anthropic_base_url?: string; protocol_errors?: Record<string, string>}> {
    return this.request(`/api/v1/providers/${encodeURIComponent(providerID)}/model-probes`, {
      method: "POST", body: JSON.stringify({model_id: modelID, auth_style: authStyle}),
    });
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

  // order "attention" lists every non-completed Task before the completed ones,
  // newest first within each group. A cursor only continues the order that
  // minted it; the server answers invalid_cursor otherwise.
  tasks(projectID = "", options: {limit?: number; cursor?: string; summary?: boolean; order?: "attention"} = {}): Promise<{
    tasks: Task[];
    has_more?: boolean;
    next_cursor?: string;
    // Totals for the filter popovers. The response is paged, so these count the
    // whole set and must not be re-derived from the loaded tasks.
    counts?: TaskFacetCounts;
  }> {
    const query = new URLSearchParams();
    if (projectID) query.set("project_id", projectID);
    if (options.limit) query.set("limit", String(options.limit));
    if (options.cursor) query.set("cursor", options.cursor);
    if (options.summary) query.set("summary", "true");
    if (options.order) query.set("order", options.order);
    return this.request(`/api/v1/tasks${query.size ? `?${query}` : ""}`);
  }

  taskCloneSources(): Promise<{tasks: Task[]}> {
    return this.request("/api/v1/task-clone-sources");
  }

  petPendingApprovalCount(): Promise<{ok: boolean; pending: number}> {
    return this.request("/api/v1/pet/approvals/pending-count", {signal: AbortSignal.timeout(10_000)});
  }

  petWorkbench(options: {approvalLimit?: number; worklogLimit?: number} = {}, signal?: AbortSignal): Promise<import("./types").PetWorkbench & {ok: boolean}> {
    const query = new URLSearchParams();
    if (options.approvalLimit) query.set("approval_limit", String(options.approvalLimit));
    if (options.worklogLimit) query.set("worklog_limit", String(options.worklogLimit));
    const suffix = query.toString();
    return this.request(`/api/v1/pet/workbench${suffix ? `?${suffix}` : ""}`, {signal});
  }

  savePetWorkbenchDocument(section: string, name: string, content: string): Promise<{ok: boolean}> {
    return this.request(`/api/v1/pet/workbench/${encodeURIComponent(section)}/${encodeURIComponent(name)}`, {
      method: "PUT", body: JSON.stringify({content}),
    });
  }

  followPetTask(taskID: string): Promise<{ok: boolean; watch: import("./types").PetTaskWatch}> {
    return this.request(`/api/v1/pet/watches/${encodeURIComponent(taskID)}`, {method: "POST"});
  }

  removePetTaskWatch(taskID: string): Promise<{ok: boolean}> {
    return this.request(`/api/v1/pet/watches/${encodeURIComponent(taskID)}`, {method: "DELETE"});
  }

  setPetTaskWatchIgnored(taskID: string, ignored: boolean): Promise<{ok: boolean; watch: import("./types").PetTaskWatch}> {
    return this.request(`/api/v1/pet/watches/${encodeURIComponent(taskID)}`, {
      method: "PATCH", body: JSON.stringify({ignored}),
    });
  }

  // Heavy deletes answer 202 with status "executing"; the approval is absent
  // from that response because the background execution has not finished yet.
  resolvePetApproval(id: string, decision: "approve" | "reject" | "cancel", note = ""): Promise<{ok: boolean; status?: string; approval?: import("./types").PetApproval}> {
    return this.request(`/api/v1/pet/approvals/${encodeURIComponent(id)}/resolve`, {
      method: "POST", body: JSON.stringify({decision, note}),
    });
  }

  mcpServers(): Promise<{servers: import("./types").MCPServer[]}> {
    return this.request("/api/v1/mcp-servers");
  }

  mcpServer(id: string): Promise<{server: import("./types").MCPServer}> {
    return this.request(`/api/v1/mcp-servers/${encodeURIComponent(id)}`);
  }

  testMCPServer(id: string): Promise<{server: import("./types").MCPServer}> {
    return this.request("/api/v1/mcp-servers/test", {method: "POST", body: JSON.stringify({id})});
  }

  saveMCPServer(payload: Record<string, unknown>): Promise<{server: import("./types").MCPServer}> {
    return this.request("/api/v1/mcp-servers", {method: "POST", body: JSON.stringify(payload)});
  }

  deleteMCPServer(id: string): Promise<{ok: boolean}> {
    return this.request(`/api/v1/mcp-servers/${encodeURIComponent(id)}`, {method: "DELETE"});
  }

  setTaskMCPServers(id: string, serverIDs: string[]): Promise<{task: import("./types").Task}> {
    return this.request(`/api/v1/tasks/${encodeURIComponent(id)}/mcp-servers`, {method: "PUT", body: JSON.stringify({server_ids: serverIDs})});
  }

  createTask(payload: Record<string, unknown>): Promise<{task: Task; turn?: {id: string}; started?: boolean; start_error?: string; clone_warnings?: string[]}> {
    return this.request("/api/v1/tasks", {method: "POST", body: JSON.stringify(payload)});
  }

  startTask(id: string): Promise<{task: Task; turn?: {id: string}; started: boolean; start_error?: string}> {
    return this.request(`/api/v1/tasks/${encodeURIComponent(id)}/start`, {method: "POST", body: "{}"});
  }

  task(id: string): Promise<TaskDetail> {
    return this.request(`/api/v1/tasks/${id}`);
  }

  resolveTaskPermissionRequest(taskID: string, requestID: string, decision: "approve" | "reject", reason = ""): Promise<{ok: boolean; request: import("./types").TaskPermissionRequest}> {
    return this.request(`/api/v1/tasks/${encodeURIComponent(taskID)}/permission-requests/${encodeURIComponent(requestID)}/resolve`, {
      method: "POST", body: JSON.stringify({decision, reason}),
    });
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

  taskChannelRoutes(id: string): Promise<{destinations: ChannelDestination[]; route?: ChannelTaskRoute; contacts: ChannelContact[]; members: ChannelGroupMember[]}> {
    return this.request(`/api/v1/tasks/${encodeURIComponent(id)}/channel-routes`);
  }

  bindTaskChannelRoute(id: string, conversationID: string): Promise<{route: ChannelTaskRoute}> {
    return this.request(`/api/v1/tasks/${encodeURIComponent(id)}/channel-routes`, {
      method: "POST", body: JSON.stringify({conversation_id: conversationID}),
    });
  }

  unbindTaskChannelRoute(id: string, routeID: string): Promise<{ok: boolean}> {
    return this.request(`/api/v1/tasks/${encodeURIComponent(id)}/channel-routes/${encodeURIComponent(routeID)}`, {method: "DELETE"});
  }

  refreshTaskChannelMembers(id: string): Promise<{ok: boolean; completed: boolean; command: {id: string; state: string}; members?: ChannelGroupMember[]}> {
    return this.request(`/api/v1/tasks/${encodeURIComponent(id)}/channel-members:refresh`, {method: "POST"});
  }

  updateTaskChannelContacts(id: string, contacts: Array<{identity_link_id: string; collaboration_role: string}>): Promise<{contacts: ChannelContact[]}> {
    return this.request(`/api/v1/tasks/${encodeURIComponent(id)}/channel-contacts`, {
      method: "PUT", body: JSON.stringify({contacts}),
    });
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

  hardwareResources(): Promise<{resources: HardwareResource[]}> {
    return this.request("/api/v1/hardware-resources");
  }

  createHardwareResource(payload: Record<string, unknown>): Promise<{resource: HardwareResource}> {
    return this.request("/api/v1/hardware-resources", {
      method: "POST", body: JSON.stringify(payload),
    });
  }

  updateHardwareResource(id: string, payload: Record<string, unknown>): Promise<{resource: HardwareResource}> {
    return this.request(`/api/v1/hardware-resources/${encodeURIComponent(id)}`, {
      method: "PUT", body: JSON.stringify(payload),
    });
  }

  deleteHardwareResource(id: string): Promise<{deleted_bindings: number}> {
    return this.request(`/api/v1/hardware-resources/${encodeURIComponent(id)}`, {method: "DELETE"});
  }

  pairHardwareADB(id: string, pairingPort: number, pairingCode: string): Promise<{message: string; output: string; resource: HardwareResource}> {
    return this.request(`/api/v1/hardware-resources/${encodeURIComponent(id)}/adb/pair`, {
      method: "POST", body: JSON.stringify({pairing_port: pairingPort, pairing_code: pairingCode}),
    });
  }

  updateTaskHardware(id: string, groups: Record<string, unknown>[]): Promise<{groups: HardwareGroup[]}> {
    return this.request(`/api/v1/tasks/${id}/hardware`, {
      method: "PUT", body: JSON.stringify({groups}),
    });
  }

  updateTaskHardwareBindings(id: string, bindings: Record<string, unknown>[]): Promise<{groups: HardwareGroup[]; resources: HardwareResource[]}> {
    return this.request(`/api/v1/tasks/${encodeURIComponent(id)}/hardware-bindings`, {
      method: "PUT", body: JSON.stringify({bindings}),
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

  knowledge(scope = "", projectID = "", status = "", options: {limit?: number; cursor?: string; summary?: boolean; includeProposals?: boolean} = {}): Promise<{knowledge: Knowledge[]; proposals?: KnowledgeProposal[]; review_settings?: KnowledgeReviewSettings; has_more?: boolean; next_cursor?: string}> {
    const query = new URLSearchParams();
    if (scope) query.set("scope", scope);
    if (projectID) query.set("project_id", projectID);
    if (status) query.set("status", status);
    if (options.limit) query.set("limit", String(options.limit));
    if (options.cursor) query.set("cursor", options.cursor);
    if (options.summary) query.set("summary", "true");
    if (options.includeProposals === false) query.set("include_proposals", "false");
    return this.request(`/api/v1/knowledge?${query}`);
  }

  knowledgeProposals(options: {status?: "pending" | "approved" | "rejected" | "all"; page?: number; limit?: number; scope?: string; projectID?: string} = {}): Promise<{proposals: KnowledgeProposal[]; total: number; page: number; pages: number; has_more: boolean}> {
    const query = new URLSearchParams();
    query.set("status", options.status || "all");
    if (options.page) query.set("page", String(options.page));
    if (options.limit !== undefined) query.set("limit", String(options.limit));
    if (options.scope) query.set("scope", options.scope);
    if (options.projectID) query.set("project_id", options.projectID);
    return this.request(`/api/v1/knowledge/proposals?${query}`);
  }

  knowledgeEntry(id: string): Promise<{knowledge: Knowledge}> {
    return this.request(`/api/v1/knowledge/${encodeURIComponent(id)}`);
  }

  knowledgeLibraries(): Promise<{libraries: KnowledgeLibrary[]}> {
    return this.request("/api/v1/knowledge/libraries");
  }

  // Every project that has a verified knowledge root, independent of any page of
  // entries. Scoping UI must use this: a page is recency-ordered, so it reports
  // only the projects whose knowledge changed most recently.
  knowledgeIndexes(): Promise<{indexes: Knowledge[]}> {
    return this.request("/api/v1/knowledge/indexes");
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

  batchKnowledgeProposals(payload: {action: "approve" | "reject"; proposal_ids: string[]; legacy_ids: string[]; decision_reason?: string}): Promise<{ok: boolean; processed: string[]; failures: Array<{id: string; error: string}>}> {
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

  ownerAssistant(): Promise<{ok: boolean} & OwnerAssistantView> {
    return this.request("/api/v1/assistant");
  }

  updateOwnerAssistant(payload: {model_id?: string; name?: string; character?: string}): Promise<{ok: boolean; assistant: OwnerAssistant; models: AssistantModelOption[]; model_name?: string}> {
    return this.request("/api/v1/assistant", {method: "PUT", body: JSON.stringify(payload)});
  }
  petState(): Promise<{ok: boolean; state: PetState}> {
    return this.request("/api/v1/pet");
  }

  updatePetState(patch: PetStatePatch): Promise<{ok: boolean; state: PetState}> {
    return this.request("/api/v1/pet", {method: "PUT", body: JSON.stringify(patch)});
  }
  controlPet(payload: {say: string; actions: Array<{type: string; name?: string; zone?: string; task?: string}>}): Promise<{ok: boolean; say: string; actions: unknown[]}> {
    return this.request("/api/v1/pet/control", {method: "POST", body: JSON.stringify(payload)});
  }

  claimPetNavigation(payload: {session_id: string; ids?: string[]}): Promise<{ok: boolean; actions: PetNavigationAction[]; expired: number}> {
    return this.request("/api/v1/pet/navigation/claim", {method: "POST", body: JSON.stringify(payload)});
  }

  petBrain(payload: {page: string; idle_seconds: number; device_id: string; layout?: PetLayout}): Promise<PetBrainResult> {
    return this.request("/api/v1/pet/brain", {method: "POST", body: JSON.stringify(payload)});
  }

  petPrompts(): Promise<{ok: boolean; chat: string; brain: string}> {
    return this.request("/api/v1/pet/prompts");
  }

  petActivity(limit = 20): Promise<{ok: boolean; events: PetEvent[]}> {
    return this.request(`/api/v1/pet/activity?limit=${limit}`);
  }

  updatePetSettings(payload: {enabled: boolean; character: string; chat_model_id: string; brain_model_id: string; actions: string[]; identity?: string; principles?: string; idle_minutes: number; quiet_start: number; quiet_end: number; hourly_limit: number; allow_view_tasks?: boolean; allow_create_tasks?: boolean; allow_create_workspaces?: boolean; allow_message_tasks?: boolean; allow_manage_models?: boolean; allow_manage_knowledge?: boolean; allow_manage_channels?: boolean; allow_manage_system?: boolean}): Promise<{ok: boolean; assistant: OwnerAssistant; models: AssistantModelOption[]; model_name?: string; brain_model_name?: string}> {
    return this.request("/api/v1/pet/settings", {method: "PUT", body: JSON.stringify(payload)});
  }

  resetPet(confirm: string): Promise<{ok: boolean; result: PetResetResult}> {
    return this.request("/api/v1/pet/reset", {method: "POST", body: JSON.stringify({confirm})});
  }

  askOwnerAssistant(content: string, layout?: PetLayout): Promise<{ok: boolean; message: AssistantMessage; reply: AssistantMessage; tool_events?: AssistantToolEvent[]; actions?: PetBrainAction[]}> {
    return this.request("/api/v1/assistant/messages", {method: "POST", body: JSON.stringify({content, layout})});
  }

  assistantHints(): Promise<{ok: boolean; hints: AssistantHint[]}> {
    return this.request("/api/v1/assistant/hints");
  }

  dismissAssistantHint(kind: string, targetID: string): Promise<{ok: boolean}> {
    return this.request("/api/v1/assistant/hints/dismiss", {
      method: "POST", body: JSON.stringify({kind, target_id: targetID}),
    });
  }

  mailAccounts(): Promise<{ok: boolean; accounts: MailAccount[]}> {
    return this.request("/api/v1/mail-accounts");
  }

  createMailAccount(payload: Record<string, unknown>): Promise<{ok: boolean; account: MailAccount}> {
    return this.request("/api/v1/mail-accounts", {method: "POST", body: JSON.stringify(payload)});
  }

  updateMailAccount(id: string, payload: Record<string, unknown>): Promise<{ok: boolean; account: MailAccount}> {
    return this.request(`/api/v1/mail-accounts/${encodeURIComponent(id)}`, {method: "PUT", body: JSON.stringify(payload)});
  }

  deleteMailAccount(id: string): Promise<{ok: boolean}> {
    return this.request(`/api/v1/mail-accounts/${encodeURIComponent(id)}`, {method: "DELETE"});
  }

  testMailAccount(id: string): Promise<{ok: boolean; level?: string; error_code?: string; message?: string; duration_ms?: number}> {
    return this.request(`/api/v1/mail-accounts/${encodeURIComponent(id)}/test`, {method: "POST", body: "{}"});
  }

  mailTemplates(params: {scope?: string; project_id?: string; limit?: number} = {}): Promise<{ok: boolean; templates: MailTemplate[]}> {
    const query = new URLSearchParams();
    if (params.scope) query.set("scope", params.scope);
    if (params.project_id) query.set("project_id", params.project_id);
    if (params.limit) query.set("limit", String(params.limit));
    const suffix = query.toString() ? `?${query}` : "";
    return this.request(`/api/v1/mail-templates${suffix}`);
  }

  createMailTemplate(payload: Record<string, unknown>): Promise<{ok: boolean; template: MailTemplate}> {
    return this.request("/api/v1/mail-templates", {method: "POST", body: JSON.stringify(payload)});
  }

  updateMailTemplate(id: string, payload: Record<string, unknown>): Promise<{ok: boolean; template: MailTemplate}> {
    return this.request(`/api/v1/mail-templates/${encodeURIComponent(id)}`, {method: "PUT", body: JSON.stringify(payload)});
  }

  archiveMailTemplate(id: string): Promise<{ok: boolean}> {
    return this.request(`/api/v1/mail-templates/${encodeURIComponent(id)}`, {method: "DELETE"});
  }

  purgeMailTemplate(id: string): Promise<{ok: boolean; purged: boolean}> {
    return this.request(`/api/v1/mail-templates/${encodeURIComponent(id)}?purge=1`, {method: "DELETE"});
  }

  mailTemplateVersions(id: string): Promise<{ok: boolean; versions: MailTemplateVersion[]}> {
    return this.request(`/api/v1/mail-templates/${encodeURIComponent(id)}/versions`);
  }

  previewMailTemplate(id: string, payload: Record<string, unknown>): Promise<{ok: boolean; preview: Record<string, unknown>}> {
    return this.request(`/api/v1/mail-templates/${encodeURIComponent(id)}/preview`, {method: "POST", body: JSON.stringify(payload)});
  }

  previewMailTemplateContent(payload: Record<string, unknown>): Promise<{ok: boolean; preview: Record<string, unknown>}> {
    return this.request("/api/v1/mail-templates/preview", {method: "POST", body: JSON.stringify(payload)});
  }

  mailBindings(scopeType: string, scopeID: string): Promise<{ok: boolean; bindings: MailBinding[]}> {
    const query = new URLSearchParams({scope_type: scopeType, scope_id: scopeID});
    return this.request(`/api/v1/mail-bindings?${query}`);
  }

  createMailBinding(payload: Record<string, unknown>): Promise<{ok: boolean; binding: MailBinding}> {
    return this.request("/api/v1/mail-bindings", {method: "POST", body: JSON.stringify(payload)});
  }

  updateMailBinding(id: string, payload: Record<string, unknown>): Promise<{ok: boolean; binding: MailBinding}> {
    return this.request(`/api/v1/mail-bindings/${encodeURIComponent(id)}`, {method: "PUT", body: JSON.stringify(payload)});
  }

  deleteMailBinding(id: string): Promise<{ok: boolean}> {
    return this.request(`/api/v1/mail-bindings/${encodeURIComponent(id)}`, {method: "DELETE"});
  }

  mailMessages(params: {task_id?: string; status?: string; account_id?: string; limit?: number} = {}): Promise<{ok: boolean; messages: MailMessage[]}> {
    const query = new URLSearchParams();
    if (params.task_id) query.set("task_id", params.task_id);
    if (params.status) query.set("status", params.status);
    if (params.account_id) query.set("account_id", params.account_id);
    if (params.limit) query.set("limit", String(params.limit));
    const suffix = query.toString() ? `?${query}` : "";
    return this.request(`/api/v1/mail-messages${suffix}`);
  }

  createMailMessage(payload: Record<string, unknown>): Promise<{ok: boolean; message: MailMessage; attachments: MailAttachment[]; deduplicated: boolean}> {
    return this.request("/api/v1/mail-messages", {method: "POST", body: JSON.stringify(payload)});
  }

  mailMessage(id: string): Promise<{ok: boolean; message: MailMessage; attachments: MailAttachment[]; deliveries: MailDelivery[]}> {
    return this.request(`/api/v1/mail-messages/${encodeURIComponent(id)}`);
  }

  approveMailMessage(id: string, expectedDigest: string, note = ""): Promise<{ok: boolean; message: MailMessage; delivery: MailDelivery}> {
    return this.request(`/api/v1/mail-messages/${encodeURIComponent(id)}/approve`, {
      method: "POST", body: JSON.stringify({expected_digest: expectedDigest, note}),
    });
  }

  rejectMailMessage(id: string, reason: string): Promise<{ok: boolean}> {
    return this.request(`/api/v1/mail-messages/${encodeURIComponent(id)}/reject`, {
      method: "POST", body: JSON.stringify({reason}),
    });
  }

  cancelMailMessage(id: string): Promise<{ok: boolean; message: MailMessage}> {
    return this.request(`/api/v1/mail-messages/${encodeURIComponent(id)}/cancel`, {method: "POST", body: "{}"});
  }

  retryMailMessage(id: string, reason = ""): Promise<{ok: boolean; delivery: MailDelivery}> {
    return this.request(`/api/v1/mail-messages/${encodeURIComponent(id)}/retry`, {
      method: "POST", body: JSON.stringify({reason}),
    });
  }

  mailMessageAttempts(id: string): Promise<{ok: boolean; deliveries: Array<{delivery: MailDelivery; attempts: MailDeliveryAttempt[]}>}> {
    return this.request(`/api/v1/mail-messages/${encodeURIComponent(id)}/attempts`);
  }

  mailMessageDownloadLinks(id: string): Promise<{ok: boolean; links: MailAttachment[]}> {
    return this.request(`/api/v1/mail-messages/${encodeURIComponent(id)}/download-links`);
  }

  revokeMailDownloadLink(messageID: string, attachmentID: string): Promise<{ok: boolean}> {
    return this.request(`/api/v1/mail-messages/${encodeURIComponent(messageID)}/download-links/${encodeURIComponent(attachmentID)}/revoke`, {method: "POST", body: "{}"});
  }

  taskMail(taskID: string): Promise<TaskMailOptions> {
    return this.request(`/api/v1/tasks/${encodeURIComponent(taskID)}/mail`);
  }

}

export const api = new APIClient();
