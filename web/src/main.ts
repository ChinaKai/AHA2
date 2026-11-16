import {api, setAPIRequestObserver} from "./api.js";
import type {APIRequestSample} from "./api.js";
import {COMMAND_PALETTE_COMPOSITION_GUARD_MS, COMMAND_PALETTE_DEBOUNCE_MS, buildCommandSections, createDebouncer, flattenCommandSections, isPaletteShortcut, paletteKeyAction, readRecentTaskIDs, rememberRecentTaskID, runPaletteSelection} from "./command_palette.js";
import type {CommandItem, CommandSection, CommandSectionID} from "./command_palette.js";
import {handlePetStreamEvent, mountAssistant, showPetSettings, notifyPetTaskIdle, notifyPetTaskReply, notifyPetTaskThinking, resumePetNavigation, setAssistantVisible} from "./assistant.js";
import {MODEL_WIRE_OPTIONS, backendSelectOptionsHTML, backendsPanelHTML, modelWireEditOptionsHTML, persistedWireValue, resolveBackendOptions} from "./backends.js";
import {bindPetDocEditorButtons, createPetWorkbenchLoader, createPetWorkbenchPageKeeper, mapPetWorkbenchTab, PET_APPROVAL_MAX_LIMIT, petApprovalMatches, petApprovalPage, renderPetApprovalLoadMore, renderPetOverview, renderPetRecords, renderPetWorkbenchLoadError, swapPetWorkbenchPage} from "./pet_workbench.js";
import type {PetWorkbenchPageBinding} from "./pet_workbench.js";
import {conversationItemKeys, conversationRenderableItems, conversationWindow, renderConversationList} from "./conversation_ui.js";
import type {ConversationWindow, ConversationWindowOptions} from "./conversation_ui.js";
import {chunkErrorHTML, chunkLoadingHTML, createLazyPage, retryLazyPage} from "./lazy_page.js";
import type {LazyPage} from "./lazy_page.js";
import {icon} from "./icons.js";
import {createPetApprovalCounter, petApprovalFilterCount, petPendingApprovalBadge, petApprovalOptions, renderPetApprovalDetail, renderPetApprovalFilters, renderPetApprovalList, selectedPetApprovalOption} from "./pet_approvals.js";
import {applyLocale, normalizeLanguagePreference, startUITranslationObserver, t, tr, translateUI} from "./i18n.js";
import {bindHardwarePanel, refreshHardwarePanel, stopHardwarePanel} from "./hardware_panel.js";
import type {ResourcesTab} from "./resources.js";
import type {MailResourcesData} from "./mail_resources.js";
import {bindDesktopPanel, desktopPanelInteracting, refreshDesktopPanel, stopDesktopPanel} from "./desktop_panel.js";
import {renderMarkdown} from "./markdown.js";
import {forgetAllModelHealth as clearAllModelHealth, forgetModelHealth as clearModelHealth, runModelHealthCheck, runModelHealthChecks} from "./model_health.js";
import type {ModelHealthState} from "./model_health.js";
import {MODEL_PAGE_SIZE, buildModelPage, modelBatchBarHTML, modelSelectionIndicator, modelSelectionState, pruneModelSelection, selectModelIDs, toggleModelSelection} from "./model_selection.js";
import type {DataRetentionViewState} from "./data_retention.js";
import {bindRuntimeFields, proxyEnabledFrom, proxyOptionsHTML, runtimeFieldsHTML, setClaudeOfficialCatalog, setClaudeOfficialLoading, setRuntimeBackends, syncRuntimeFields} from "./runtime_picker.js";
import type {ClaudeOfficialCatalog} from "./runtime_picker.js";
import {renderComposerAgentOptions, renderComposerTools, TASK_ATTACHMENT_MAX_COUNT, taskAttachmentLimitHint, taskAttachmentValidationError} from "./task_composer.js";
import {DEFAULT_TASK_STATUS_FILTERS, LOCAL_TASK_DEVICE_FILTER, TASK_STATUS_GROUP_LABELS, hiddenTaskStatusGroups, hiddenTaskStatusText, taskDeviceFilterOptions, taskMatchesFilters, taskStatusGroupCounts} from "./task_filters.js";
import type {TaskStatusGroup} from "./task_filters.js";
import {TASK_TOOL_DEFAULT_WIDTH, normalizePinnedTaskTool, normalizeTaskToolMode, normalizeTaskToolWidth, renderTaskToolButtons, renderTaskToolContent, renderTaskToolPanel} from "./task_tools.js";
import type {TaskTool, TaskToolMode} from "./task_tools.js";
import {TASK_SLASH_COMMANDS, bindMessageBubbleControls, captureInteractiveRegion, clearNavigationSnapshot, createListRefreshScheduler, createResourceLoader, createStatusAnnouncer, eventRefreshesTaskList, taskStatusNeedsAnnouncement, exactSlashCommand, executeAgentSessionAction, installDialogEntryFocus, loadNavigationSnapshot, matchingSlashCommands, paginateItems, paginationHTML, planKeyedNodes, stableTextSignature, protoBadges, readPerfPanelPreference, renderModelHealthCell, renderProtocolProbeStatus, renderWorkspaceDetection, replaceInteractiveRegion, restoreInteractiveRegion, saveNavigationSnapshot, staleViewHashTarget, textMatchesQuery, writePerfPanelPreference} from "./ui_helpers.js";
import type {InteractiveRegionState, ModelHealthView} from "./ui_helpers.js";
import {
  compactNumber,
  contextPercent,
  formatDuration,
  isActiveTurn,
  latestAgentTurns,
  agentConfigChanged,
  agentConfigSnapshot,
  agentConfigSummary,
  renderAgentConfigDialog,
  renderAgentConfigForm,
  renderAgentTurnCard,
  renderMCPOptions,
  renderTaskSkills,
  renderTaskReviewDecision,
  renderContextMetrics,
  usageNumber,
  validateAgentConfigInput,
} from "./task_agents.js";
import type {
  AgentAPISettings,
	BackendSettings,
  Attachment,
  AuthStatus,
  ClaudeModelOption,
  CodexAccount,
  ChannelContact,
  ChannelDestination,
  ChannelGroupMember,
  ChannelInstance,
  ChannelPlugin,
  ChannelTaskRoute,
  ConversationCategory,
  ConversationItem,
  DataInventory,
  DetectedModel,
  EnvGroup,
  GeneralSettings,
  HardwareResource,
  Knowledge,
  KnowledgeLibrary,
  NetworkSettings,
  KnowledgeProposal,
  KnowledgeReviewSettings,
  KnowledgeVaultLibrary,
  KnowledgeVaultSettings,
  MailAccount,
  MailMessage,
  MailTemplate,
  TaskMailOptions,
  Model,
  MCPServer,
  Project,
  Provider,
  ProxySettings,
  ManagedProxyView,
  SecuritySettings,
  SyncConflict,
	SyncRunProgress,
  SyncSettings,
  SyncState,
  Skill,
  SystemInfo,
  Task,
  TaskContextDetail,
  TaskDetail,
  TaskFacetCounts,
  TaskPermissionRequest,
  Workspace,
} from "./types.js";
type View = "projects" | "channels" | "models" | "resources" | "tasks" | "knowledge" | "prompts" | "proxy" | "sync" | "advanced" | "data" | "other" | "mcp" | "pet-settings" | "pet-workbench";
type ResourceKey = "projects" | "workspaces" | "tasks" | "system" | "providers" | "models" | "env" | "accounts" | "skills" | "channels" | "hardware" | "mail-accounts" | "mail-templates" | "mail-messages" | "knowledge" | "proxy" | "advanced" | "sync" | "prompts" | "mcp" | "data";
type TaskFilterKind = "project" | "status" | "device";
interface ResourceState {loading: boolean; loaded: boolean; error: string; updatedAt: number}
interface TaskChannelState {
  destinations: ChannelDestination[];
  route?: ChannelTaskRoute;
  contacts: ChannelContact[];
  members: ChannelGroupMember[];
}
interface State extends ModelHealthState {
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
  networkSettings: NetworkSettings;
  agentAPISettings: AgentAPISettings;
	backendSettings: BackendSettings;
  generalSettings: GeneralSettings;
  dataInventory: DataInventory | null;
  dataInventoryLoading: boolean;
  dataInventoryError: string;
  dataRetention: DataRetentionViewState;
  syncSettings: SyncSettings;
  syncSettingsReady: boolean;
  syncState: SyncState;
  syncPending: number;
  syncConflicts: SyncConflict[];
	syncNextRunAt: string;
	syncRun: SyncRunProgress;
  projects: Project[];
  workspaces: Workspace[];
  providers: Provider[];
  channelProviders: ChannelPlugin[];
  channelInstances: ChannelInstance[];
  envGroups: EnvGroup[];
  codexAccounts: CodexAccount[];
  models: Model[];
  modelPage: number;
  modelSearch: string;
  modelSelected: Set<string>;
  providerPage: number;
  codexAccountPage: number;
  hardwareResources: HardwareResource[];
  resourcesTab: ResourcesTab;
  mailSubTab: "accounts" | "templates" | "sent";
  mailAccounts: MailAccount[];
  mailTemplates: MailTemplate[];
  mailMessages: MailMessage[];
  mailFilterStatus: string;
  mailFilterAccount: string;
  mailShowArchived: boolean;
  mailResourcesLoading: boolean;
  mailResourcesError: string;
  taskMailOptions: TaskMailOptions | null;
  taskMailLoading: boolean;
  taskMailError: string;
  tasks: Task[];
  knowledge: Knowledge[];
  // Every project with a verified knowledge root, for scoping UI. Separate from
  // `knowledge`, which is only the current page of entries.
  knowledgeIndexes: Knowledge[];
  knowledgeLibraries: KnowledgeLibrary[];
  knowledgeProposals: KnowledgeProposal[];
  knowledgeReviewSettings: KnowledgeReviewSettings;
  // Phase 2 file-vault page. null means "not probed yet"; false keeps the old
  // knowledge workspace and its API calls completely unchanged.
  knowledgeVaultEnabled: boolean | null;
  // Channel knowledge sources when the vault is the live read source. The
  // legacy index/library arrays stay untouched for unbound (fallback) projects.
  vaultLibraries: KnowledgeVaultLibrary[];
  skills: Skill[];
  // Vault packages are not rows in the legacy skills table. They are merged into
  // the task picker only when runtime reads are on and the project is bound.
  vaultSkills: Skill[];
  vaultSkillsOn: boolean;
  vaultSkillsRead: boolean;
  vaultSkillsIndex: boolean;
  vaultSkillsBound: string[];
  mcpServers: MCPServer[];
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
  taskChannelDestination: ChannelDestination | null | undefined;
  taskChannelState: TaskChannelState | null;
  taskChannelLoading: boolean;
  selectedTaskAgent: string;
  selectedProject: Project | null;
  dialogProjectID: string;
  taskTool: TaskTool | "";
  taskToolPinned: TaskTool | "";
  taskToolMode: TaskToolMode;
  taskToolWidth: number;
  taskDetailLoading: boolean;
  taskProjectFilters: Set<string>;
  taskStatusFilters: Set<string>;
  // Server totals behind the filter popovers; null until the first list load.
  taskFacetCounts: TaskFacetCounts | null;
  taskDeviceFilters: Set<string>;
  taskOpenFilter: TaskFilterKind | "";
  sidebarWidth: number;
  sidebarMemoItems: SidebarMemoItem[];
  sidebarMemoCollapsed: boolean;
  sidebarMemoColor: string;
}

interface SidebarMemoItem {
  id: string;
  text: string;
  done: boolean;
}

/* Route-level page chunks ------------------------------------------------ */

// Heavy, low-frequency pages are fetched on first entry. The wrappers keep the
// existing call sites and return one shared loading card (or a retryable error)
// until the chunk arrives. createLazyPage dedupes concurrent loads, so repeated
// renders and re-entering the same page never start a second request.
function lazyPageState<T>(page: LazyPage<T>): string {
  return page.status() === "failed"
    ? chunkErrorHTML(page.key, t("chunk.error"), page.error(), t("common.retry"))
    : chunkLoadingHTML(t("chunk.loading"));
}

type KnowledgePageModule = typeof import("./knowledge_workspace.js");
const knowledgePage = createLazyPage<KnowledgePageModule>("knowledge", () => import("./knowledge_workspace.js"), {onChange: () => render()});

function renderKnowledgeWorkspace(...args: Parameters<KnowledgePageModule["renderKnowledgeWorkspace"]>): string {
  const module = knowledgePage.loaded();
  if (!module) { void knowledgePage.load(); return lazyPageState(knowledgePage); }
  return module.renderKnowledgeWorkspace(...args);
}

function bindKnowledgeWorkspace(...args: Parameters<KnowledgePageModule["bindKnowledgeWorkspace"]>): void {
  const module = knowledgePage.loaded();
  if (module) module.bindKnowledgeWorkspace(...args);
  else void knowledgePage.load();
}

function knowledgeEntryListVisible(...args: Parameters<KnowledgePageModule["knowledgeEntryListVisible"]>): boolean {
  const module = knowledgePage.loaded();
  if (module) return module.knowledgeEntryListVisible(...args);
  void knowledgePage.load();
  return false;
}

type KnowledgeVaultPageModule = typeof import("./knowledge_vault.js");
const knowledgeVaultPage = createLazyPage<KnowledgeVaultPageModule>("knowledge-vault", () => import("./knowledge_vault.js"), {onChange: () => render()});

function renderKnowledgeVaultPage(...args: Parameters<KnowledgeVaultPageModule["renderKnowledgeVaultPage"]>): string {
  const module = knowledgeVaultPage.loaded();
  if (!module) { void knowledgeVaultPage.load(); return lazyPageState(knowledgeVaultPage); }
  return module.renderKnowledgeVaultPage(...args);
}

function bindKnowledgeVaultPage(...args: Parameters<KnowledgeVaultPageModule["bindKnowledgeVaultPage"]>): void {
  const module = knowledgeVaultPage.loaded();
  if (module) module.bindKnowledgeVaultPage(...args);
  else void knowledgeVaultPage.load();
}

type KnowledgeVaultSettingsModule = typeof import("./knowledge_vault_settings.js");
const knowledgeVaultSettingsPage = createLazyPage<KnowledgeVaultSettingsModule>("knowledge-vault-settings", () => import("./knowledge_vault_settings.js"), {onChange: () => render()});

function knowledgeVaultSettingsContext(): Parameters<KnowledgeVaultSettingsModule["renderKnowledgeVaultSettingsPanel"]>[0] {
  return {render, setMessage, onSettingsApplied: applyKnowledgeVaultSettings};
}

function renderKnowledgeVaultSettingsPanel(): string {
  const module = knowledgeVaultSettingsPage.loaded();
  if (!module) { void knowledgeVaultSettingsPage.load(); return lazyPageState(knowledgeVaultSettingsPage); }
  return module.renderKnowledgeVaultSettingsPanel(knowledgeVaultSettingsContext());
}

function bindKnowledgeVaultSettingsPanel(): void {
  const module = knowledgeVaultSettingsPage.loaded();
  if (module) module.bindKnowledgeVaultSettingsPanel(knowledgeVaultSettingsContext());
  else void knowledgeVaultSettingsPage.load();
}

function refreshKnowledgeVaultSettings(): Promise<void> {
  const module = knowledgeVaultSettingsPage.loaded();
  return module ? module.refreshKnowledgeVaultSettingsPanel(knowledgeVaultSettingsContext()) : Promise.resolve();
}

type CodexPageModule = typeof import("./codex_accounts.js");
const codexPage = createLazyPage<CodexPageModule>("codex", () => import("./codex_accounts.js"), {onChange: () => render()});

function renderCodexAccounts(...args: Parameters<CodexPageModule["renderCodexAccounts"]>): string {
  const module = codexPage.loaded();
  if (!module) { void codexPage.load(); return lazyPageState(codexPage); }
  return module.renderCodexAccounts(...args);
}

function bindCodexAccounts(...args: Parameters<CodexPageModule["bindCodexAccounts"]>): void {
  const module = codexPage.loaded();
  if (module) module.bindCodexAccounts(...args);
  else void codexPage.load();
}

type ChannelsPageModule = typeof import("./channels.js");
const channelsPage = createLazyPage<ChannelsPageModule>("channels", () => import("./channels.js"), {onChange: () => render()});

function renderChannels(...args: Parameters<ChannelsPageModule["renderChannels"]>): string {
  const module = channelsPage.loaded();
  if (!module) { void channelsPage.load(); return lazyPageState(channelsPage); }
  return module.renderChannels(...args);
}

function bindChannels(...args: Parameters<ChannelsPageModule["bindChannels"]>): void {
  const module = channelsPage.loaded();
  if (module) module.bindChannels(...args);
  else void channelsPage.load();
}

// The retired-instance actions are only reachable from an already-rendered
// channels page, but await the chunk anyway so a fast click during the first
// render cannot run against an unloaded module.
async function purgeRetiredChannelInstance(...args: Parameters<ChannelsPageModule["purgeRetiredChannelInstance"]>): ReturnType<ChannelsPageModule["purgeRetiredChannelInstance"]> {
  const module = channelsPage.loaded() ?? await channelsPage.load();
  if (!module) throw new Error(t("chunk.error"));
  return module.purgeRetiredChannelInstance(...args);
}

async function exportRetiredChannelInstance(...args: Parameters<ChannelsPageModule["exportRetiredChannelInstance"]>): Promise<void> {
  const module = channelsPage.loaded() ?? await channelsPage.load();
  if (!module) throw new Error(t("chunk.error"));
  return module.exportRetiredChannelInstance(...args);
}

type ResourcesPageModule = typeof import("./resources.js");
const resourcesPage = createLazyPage<ResourcesPageModule>("resources", () => import("./resources.js"), {onChange: () => render()});

function renderResourcesPage(...args: Parameters<ResourcesPageModule["renderResourcesPage"]>): string {
  const module = resourcesPage.loaded();
  if (!module) { void resourcesPage.load(); return lazyPageState(resourcesPage); }
  return module.renderResourcesPage(...args);
}

function bindResourcesPage(...args: Parameters<ResourcesPageModule["bindResourcesPage"]>): void {
  const module = resourcesPage.loaded();
  if (module) module.bindResourcesPage(...args);
  else void resourcesPage.load();
}

type MailTaskPageModule = typeof import("./mail_task.js");
const mailTaskPage = createLazyPage<MailTaskPageModule>("mail-task", () => import("./mail_task.js"), {onChange: () => render()});

function renderTaskMailPanel(...args: Parameters<MailTaskPageModule["renderTaskMailPanel"]>): string {
  const module = mailTaskPage.loaded();
  if (!module) { void mailTaskPage.load(); return lazyPageState(mailTaskPage); }
  return module.renderTaskMailPanel(...args);
}

function bindTaskMailPanel(...args: Parameters<MailTaskPageModule["bindTaskMailPanel"]>): void {
  const module = mailTaskPage.loaded();
  if (module) module.bindTaskMailPanel(...args);
  else void mailTaskPage.load();
}

type MCPPageModule = typeof import("./mcp_page.js");
const mcpPage = createLazyPage<MCPPageModule>("mcp", () => import("./mcp_page.js"), {onChange: () => render()});

function renderMCPPage(...args: Parameters<MCPPageModule["renderMCPPage"]>): string {
  const module = mcpPage.loaded();
  if (!module) { void mcpPage.load(); return lazyPageState(mcpPage); }
  return module.renderMCPPage(...args);
}

function bindMCPPage(...args: Parameters<MCPPageModule["bindMCPPage"]>): void {
  const module = mcpPage.loaded();
  if (module) module.bindMCPPage(...args);
  else void mcpPage.load();
}

type PromptPageModule = typeof import("./prompt_admin.js");
const promptPage = createLazyPage<PromptPageModule>("prompts", () => import("./prompt_admin.js"), {onChange: () => render()});

function renderPromptAdmin(): string {
  const module = promptPage.loaded();
  if (!module) { void promptPage.load(); return lazyPageState(promptPage); }
  return module.renderPromptAdmin();
}

function bindPromptAdmin(...args: Parameters<PromptPageModule["bindPromptAdmin"]>): void {
  const module = promptPage.loaded();
  if (module) module.bindPromptAdmin(...args);
  else void promptPage.load();
}

async function loadPromptCatalog(): Promise<void> {
  const module = promptPage.loaded() ?? await promptPage.load();
  await module?.loadPromptCatalog();
}

type ProxyPageModule = typeof import("./proxy_settings.js");
const proxyPage = createLazyPage<ProxyPageModule>("proxy", () => import("./proxy_settings.js"), {onChange: () => render()});

function renderProxySettings(...args: Parameters<ProxyPageModule["renderProxySettings"]>): string {
  const module = proxyPage.loaded();
  if (!module) { void proxyPage.load(); return lazyPageState(proxyPage); }
  return module.renderProxySettings(...args);
}

function bindProxySettings(...args: Parameters<ProxyPageModule["bindProxySettings"]>): void {
  const module = proxyPage.loaded();
  if (module) module.bindProxySettings(...args);
  else void proxyPage.load();
}

type SyncPageModule = typeof import("./sync_settings.js");
const syncPage = createLazyPage<SyncPageModule>("sync", () => import("./sync_settings.js"), {onChange: () => render()});

function renderSyncSettings(...args: Parameters<SyncPageModule["renderSyncSettings"]>): string {
  const module = syncPage.loaded();
  if (!module) { void syncPage.load(); return lazyPageState(syncPage); }
  return module.renderSyncSettings(...args);
}

function bindSyncSettings(...args: Parameters<SyncPageModule["bindSyncSettings"]>): void {
  const module = syncPage.loaded();
  if (module) module.bindSyncSettings(...args);
  else void syncPage.load();
}

function isSyncSettingsFormEditing(...args: Parameters<SyncPageModule["isSyncSettingsFormEditing"]>): boolean {
  const module = syncPage.loaded();
  if (module) return module.isSyncSettingsFormEditing(...args);
  void syncPage.load();
  return false;
}

type DataRetentionPageModule = typeof import("./data_retention.js");
const dataRetentionPage = createLazyPage<DataRetentionPageModule>("data-retention", () => import("./data_retention.js"), {onChange: () => render()});

function renderDatabasePanel(...args: Parameters<DataRetentionPageModule["renderDatabasePanel"]>): string {
  const module = dataRetentionPage.loaded();
  if (!module) { void dataRetentionPage.load(); return lazyPageState(dataRetentionPage); }
  return module.renderDatabasePanel(...args);
}

function formatBytes(...args: Parameters<DataRetentionPageModule["formatBytes"]>): string {
  const module = dataRetentionPage.loaded();
  if (module) return module.formatBytes(...args);
  void dataRetentionPage.load();
  return `${args[0]} B`;
}

function retentionFormError(...args: Parameters<DataRetentionPageModule["retentionFormError"]>): string {
  const module = dataRetentionPage.loaded();
  if (module) return module.retentionFormError(...args);
  void dataRetentionPage.load();
  return "";
}

function purgeEnabled(...args: Parameters<DataRetentionPageModule["purgeEnabled"]>): boolean {
  const module = dataRetentionPage.loaded();
  if (module) return module.purgeEnabled(...args);
  void dataRetentionPage.load();
  return false;
}

function skipReasonText(...args: Parameters<DataRetentionPageModule["skipReasonText"]>): string {
  const module = dataRetentionPage.loaded();
  if (module) return module.skipReasonText(...args);
  void dataRetentionPage.load();
  return String(args[0] ?? "");
}

const taskToolLayoutKey = "aha2.task-tool-layout";
const sidebarLayoutKey = "aha2.sidebar-layout";
const sidebarMemoKey = "aha2.sidebar-memo";
const SIDEBAR_DEFAULT_WIDTH = 228;
const SIDEBAR_MIN_WIDTH = 180;
const SIDEBAR_MAX_WIDTH = 360;
const SIDEBAR_MEMO_MAX_LENGTH = 20000;
const SIDEBAR_MEMO_COLORS: Record<string, {label: string; surface: string; input: string; border: string; text: string; placeholder: string}> = {
  graphite: {label: "石墨", surface: "#272c33", input: "#20242a", border: "#424953", text: "#eef1f5", placeholder: "#7f8996"},
  yellow: {label: "浅黄", surface: "#fff8dc", input: "#fffdf1", border: "#e6dca8", text: "#3e3a2e", placeholder: "#8e8259"},
  blue: {label: "浅蓝", surface: "#e8f3ff", input: "#f7fbff", border: "#bcd8f6", text: "#24384c", placeholder: "#6d849c"},
  green: {label: "浅绿", surface: "#e9f7ee", input: "#f7fcf8", border: "#b9ddc4", text: "#244433", placeholder: "#6c9077"},
  lavender: {label: "浅紫", surface: "#f2edff", input: "#fbfaff", border: "#d5c9f4", text: "#3b3150", placeholder: "#84759d"},
};

function normalizeSidebarMemoColor(value: unknown): string {
  const color = String(value || "");
  return SIDEBAR_MEMO_COLORS[color] ? color : "graphite";
}

function normalizeSidebarWidth(value: unknown): number {
  const width = Number(value);
  if (!Number.isFinite(width)) return SIDEBAR_DEFAULT_WIDTH;
  return Math.round(Math.min(SIDEBAR_MAX_WIDTH, Math.max(SIDEBAR_MIN_WIDTH, width)));
}

function newSidebarMemoItem(text = "", done = false): SidebarMemoItem {
  const randomID = globalThis.crypto?.randomUUID?.();
  return {id: randomID || `memo-${Date.now()}-${Math.random().toString(36).slice(2)}`, text, done};
}

function loadSidebarMemoItems(): SidebarMemoItem[] {
  const raw = localStorage.getItem(sidebarMemoKey) || "";
  try {
    const parsed = JSON.parse(raw);
    if (Array.isArray(parsed)) {
      const items = parsed
        .filter(item => item && typeof item === "object")
        .map(item => ({
          id: String(item.id || newSidebarMemoItem().id),
          text: String(item.text || "").slice(0, SIDEBAR_MEMO_MAX_LENGTH),
          done: item.done === true,
        }));
      if (items.length) return items;
    }
  } catch {
    // Older versions stored plain text; migrate it line-by-line below.
  }
  const lines = raw.split(/\r?\n/).map(text => text.slice(0, SIDEBAR_MEMO_MAX_LENGTH));
  return (lines.length ? lines : [""]).map(text => newSidebarMemoItem(text));
}

function loadSidebarSettings(): {width: number; memoItems: SidebarMemoItem[]; memoCollapsed: boolean; memoColor: string} {
  try {
    const layout = JSON.parse(localStorage.getItem(sidebarLayoutKey) || "{}") as {width?: unknown; memoCollapsed?: unknown; memoColor?: unknown};
    return {
      width: normalizeSidebarWidth(layout.width),
      memoItems: loadSidebarMemoItems(),
      memoCollapsed: layout.memoCollapsed === true,
      memoColor: normalizeSidebarMemoColor(layout.memoColor),
    };
  } catch {
    return {width: SIDEBAR_DEFAULT_WIDTH, memoItems: [newSidebarMemoItem()], memoCollapsed: false, memoColor: "graphite"};
  }
}

function loadTaskToolLayout(): {mode: TaskToolMode; width: number; pinnedTool: TaskTool | ""} {
  try {
    const saved = JSON.parse(localStorage.getItem(taskToolLayoutKey) || "{}") as {mode?: unknown; width?: unknown; pinnedTool?: unknown};
    return {mode: normalizeTaskToolMode(saved.mode), width: normalizeTaskToolWidth(saved.width), pinnedTool: normalizePinnedTaskTool(saved.pinnedTool)};
  } catch {
    return {mode: "split", width: TASK_TOOL_DEFAULT_WIDTH, pinnedTool: ""};
  }
}

const savedTaskToolLayout = loadTaskToolLayout();
const savedSidebarSettings = loadSidebarSettings();

const state: State = {
  auth: null,
  view: "projects",
  loading: true,
  hydrating: false,
  error: "",
  notice: "",
  renderPending: false,
  system: {os: "windows", arch: "", wsl_available: false, wsl_distros: [], version: "dev", web_version: "", started_at: ""},
  proxySettings: {mode: "external", http_proxy: "http://127.0.0.1:7897", https_proxy: "http://127.0.0.1:7897", no_proxy: "localhost,127.0.0.1,::1", managed_refresh_interval_minutes: 1440},
  managedProxy: {configured: false, profiles: [], url_configured: false, nodes: [], unsupported_count: 0, unsupported_types: [], status: "idle"},
  securitySettings: {validate_origin: true, access_scope: "lan", startup_override: false},
  networkSettings: {listen_address: "", startup_listen_address: "", restart_required: false},
  agentAPISettings: {url: "", effective_url: "http://127.0.0.1:8766"},
	backendSettings: {idle_timeout_seconds: 10 * 60, turn_timeout_seconds: 10 * 60 * 60},
  generalSettings: {language: "auto"},
  dataInventory: null,
  dataInventoryLoading: false,
  dataInventoryError: "",
  dataRetention: {database: null, loading: false, error: "", plan: null, result: null, busy: ""},
  syncSettings: {scope:"default",enabled:false,endpoint:"",device_id:"",device_name:"",interval_seconds:300,token_configured:false,passphrase_configured:false},
  syncSettingsReady: false,
  syncState: {scope:"default",cursor:"",last_error:""},
  syncPending: 0,
  syncConflicts: [],
	syncNextRunAt: "",
	syncRun: {running: false, phase: "", completed: 0, total: 0},
  projects: [],
  workspaces: [],
  providers: [],
  channelProviders: [],
  channelInstances: [],
  envGroups: [],
  codexAccounts: [],
  models: [],
  modelPage: 1,
  modelSearch: "",
  modelSelected: new Set<string>(),
  modelHealth: {} as Record<string, ModelHealthView>,
  modelHealthSeq: {} as Record<string, number>,
  providerPage: 1,
  codexAccountPage: 1,
  hardwareResources: [],
  resourcesTab: "hardware",
  mailSubTab: "accounts",
  mailAccounts: [],
  mailTemplates: [],
  mailMessages: [],
  mailFilterStatus: "",
  mailFilterAccount: "",
  mailShowArchived: false,
  mailResourcesLoading: false,
  mailResourcesError: "",
  taskMailOptions: null,
  taskMailLoading: false,
  taskMailError: "",
  tasks: [],
  knowledge: [],
  knowledgeIndexes: [],
  knowledgeLibraries: [],
  knowledgeProposals: [],
  knowledgeReviewSettings: {auto_approve: false},
  knowledgeVaultEnabled: null,
  vaultLibraries: [],
  skills: [],
  vaultSkills: [],
  vaultSkillsOn: false,
  vaultSkillsRead: false,
  vaultSkillsIndex: false,
  vaultSkillsBound: [],
  mcpServers: [],
  selectedTask: null,
  taskConversation: [],
  taskConversationHasMore: false,
  taskConversationBefore: 0,
  taskConversationLatest: 0,
  taskEventCursor: 0,
  taskCategories: {chat: true, update: false, tool: false, error: true},
  taskContext: null,
  taskRealtimeState: "connecting",
  taskDraft: "",
  taskDrafts: {},
  taskAttachmentDrafts: {},
  taskChannelDestination: undefined,
  taskChannelState: null,
  taskChannelLoading: false,
  selectedTaskAgent: "main",
  selectedProject: null,
  dialogProjectID: "",
  taskTool: "",
  taskToolPinned: savedTaskToolLayout.pinnedTool,
  taskToolMode: savedTaskToolLayout.mode,
  taskToolWidth: savedTaskToolLayout.width,
  taskDetailLoading: false,
  taskProjectFilters: new Set(),
  taskStatusFilters: new Set<string>(DEFAULT_TASK_STATUS_FILTERS),
  taskDeviceFilters: new Set([LOCAL_TASK_DEVICE_FILTER]),
  taskFacetCounts: null,
  taskOpenFilter: "",
  sidebarWidth: savedSidebarSettings.width,
  sidebarMemoItems: savedSidebarSettings.memoItems,
  sidebarMemoCollapsed: savedSidebarSettings.memoCollapsed,
  sidebarMemoColor: savedSidebarSettings.memoColor,
};

applyLocale("auto");
const app = document.querySelector<HTMLDivElement>("#app");
// render() replaces #app wholesale, so the live region that speaks for the page
// lives beside it and survives. Only explicit events write here; an ordinary
// render, a poll, or an unchanged list patch never does.
const appStatus = document.querySelector<HTMLElement>("#app-status");
const statusAnnouncer = createStatusAnnouncer();

function announceStatus(text: string, detail = ""): void {
  const message = detail ? `${text} ${detail}` : text;
  if (!appStatus || !statusAnnouncer.claim(message)) return;
  // Emptying first makes a genuine repeat after the dedup window a new change
  // inside the polite region instead of an ignored identical write.
  appStatus.textContent = "";
  setTimeout(() => {
    if (appStatus) appStatus.textContent = message;
  }, 30);
}

const petApprovalCounter = createPetApprovalCounter(() => api.petPendingApprovalCount(), updatePetPendingApprovalIndicators);
// The first count is the baseline; only a later increase means a new approval.
let seenPendingApprovals: number | null = null;
let petPendingApprovalTimer = 0;
let petWorkbenchTab = "overview";
// Records table of contents: wide screens start open, phones start folded. The
// choice lives here, not in the rendered node, so switching tabs (which
// replaces the body) keeps it for the rest of the session.
let petRecordsTocOpen: boolean | null = null;
// The workbench binds against a node that render() replaces wholesale. The
// keeper lets the mounted page survive same-view renders so an unrelated
// render cannot abort and restart its request; leaving the view releases it.
const petWorkbenchPageKeeper = createPetWorkbenchPageKeeper();
let petApprovalFilter: "all" | "idea" | "decision" = "all";
let petApprovalStatusFilter: "all" | "pending" | "approved" | "rejected" | "cancelled" = "pending";
// Mobile folds the approval detail and filters into full-screen dialogs. They
// live outside #app so a background refresh cannot detach them mid-decision.
let petWorkbenchDialog: HTMLDialogElement | null = null;
let events: EventSource | null = null;
// A direct #resources/#hardware link is an explicit navigation request; it must
// not be overridden by a restored Task from the previous session.
let initialHashRoute = false;
let taskRefreshTimer: number | null = null;
let taskClockFrame: number | null = null;
let taskClockSecond = -1;
let taskMonitorTimer: number | null = null;
let taskFallbackTimer: number | null = null;
let taskLastSignalAt = 0;
let scrollConversationToBottom = false;
let conversationBottomPinVersion = 0;
let conversationAutoScrollBlockedUntil = 0;
// Whether new content should keep the view at the bottom. A position test alone
// cannot answer this: the bottom test carries an 80px tolerance, so while a
// reader is inside that band every render snaps them back to the exact bottom and
// they never accumulate enough upward movement to leave it. On a phone that is
// terminal, because touches arrive as one gesture rather than a stream of wheel
// events. So the reader's intent is recorded when they scroll up during a
// gesture, and only a return to the bottom clears it.
let conversationFollowBottom = true;
let composingConversationTouch = false;
// A wheel has no end event, so it cannot be tracked as a gesture the way a drag
// can: a flag set by a wheel is never cleared, and every later render would
// decline to follow. Wheel input opens a short window instead, refreshed by each
// event, during which a scroll is attributed to the reader.
const conversationUserScrollWindowMs = 1200;
let conversationUserScrollUntil = 0;
let composerFocusWasAtBottom = false;
const conversationRenderedHTML = new WeakMap<HTMLElement, string>();
// Virtual conversation state. Message heights vary (code blocks, tool output,
// images), so each row is measured after it renders and the cache feeds the
// spacer offsets. The cache and per-row UI state are scoped to the open Task
// and Agent: switching either one starts from fresh measurements.
const conversationHeights = new Map<string, number>();
const conversationMessageUI = new Map<string, {expanded: boolean; details: boolean[]}>();
let conversationHeaderHeight = 0;
let conversationPaddingTop = 0;
let conversationWindowFrame = 0;
let conversationMeasureFrame = 0;
let conversationResizeObserver: ResizeObserver | null = null;
let conversationUnreadMessages = 0;
const conversationVirtualOverscanPx = 600;
// Measured average row (including margins) on the 1000-message fixture. Rows
// outside the window use this until the reader brings them on screen.
const conversationVirtualEstimatePx = 80;
let loadingOlderConversation = false;
let taskListPointerActive = false;
let appPointerActive = false;
let openingTaskID = "";
// Set only when a Task was opened from its list card, so returning to the list
// can put focus back on that card instead of the top of the page.
let taskCardFocusReturnID = "";

// Any exit from the Task detail other than the back button drops the return
// target: the row may be gone, and a later open from another entry point must
// not steal focus back to an old card.
function clearTaskCardFocusReturn(): void {
  taskCardFocusReturnID = "";
}

// After the detail closes, focus belongs on the card that opened it. When that
// row is already gone (filtered out, or removed by a list refresh) focus must
// still land on the visible list instead of being dropped onto BODY.
function focusTaskListReturnTarget(taskID: string): void {
  const card = taskID
    ? document.querySelector<HTMLElement>(`.task-card[data-task="${CSS.escape(taskID)}"] [data-task-open]`)
    : null;
  const target = card || document.querySelector<HTMLElement>(".task-list");
  target?.focus({preventScroll: true});
}
let listRefreshInFlight = false;
// Startup prefetch never chains other views. It runs at most once, on idle, and
// only while the user has not interacted or opened a Task.
let secondaryPrefetchHandle: number | null = null;
let secondaryPrefetchTimer = 0;
let secondaryPrefetchDone = false;
let userInteracted = false;
// Bumped while a prefetch is issuing loadResource() calls so those requests can
// be counted without treating the first-screen loads as prefetch.
let prefetchRequestTracking = 0;

// Lightweight, UI-free metrics for real-browser checks. The performance panel is
// opt-in; production stays silent and the counters are readable from
// `window.__ahaPerf`.
interface APISlowRequestGroup {
  method: string;
  path: string;
  status: number;
  count: number;
  maxMs: number;
  totalMs: number;
}

interface AhaPerfMetrics {
  firstScreenMs: number;
  prefetchRuns: number;
  prefetchRequests: number;
  listRefreshRequests: number;
  listRefreshLastMs: number;
  listRefreshTotalMs: number;
  taskOpenCount: number;
  taskOpenLastMs: number;
  taskOpenTotalMs: number;
  apiRequestCount: number;
  apiSlowest: APIRequestSample | null;
  // Null unless `?perf=1`: a normal visit never allocates a sample buffer.
  apiSamples: APIRequestSample[] | null;
}
const initialSearchParams = new URLSearchParams(window.location.search);
const perfRequestedByURL = initialSearchParams.get("perf") === "1";
// `?perf=1` is a shortcut that turns the persistent preference on, so a refresh
// without the query parameter still shows the panel. The preference stays in
// localStorage; it is never sent to the server.
if (perfRequestedByURL) writePerfPanelPreference(true);
const perfEnabled = perfRequestedByURL || readPerfPanelPreference();
// Drop only the perf parameter so other query parameters and the hash navigate
// exactly as before; the stored preference now carries the panel across loads.
if (initialSearchParams.has("perf")) {
  const url = new URL(window.location.href);
  url.searchParams.delete("perf");
  window.history.replaceState(null, "", `${url.pathname}${url.search}${url.hash}`);
}
const apiSampleLimit = 200;
const perfMetrics: AhaPerfMetrics = {
  firstScreenMs: 0,
  prefetchRuns: 0,
  prefetchRequests: 0,
  listRefreshRequests: 0,
  listRefreshLastMs: 0,
  listRefreshTotalMs: 0,
  taskOpenCount: 0,
  taskOpenLastMs: 0,
  taskOpenTotalMs: 0,
  apiRequestCount: 0,
  apiSlowest: null,
  apiSamples: perfEnabled ? [] : null,
};
(window as unknown as {__ahaPerf: AhaPerfMetrics}).__ahaPerf = perfMetrics;
const bootStartedAt = performance.now();

function recordAPIRequest(sample: APIRequestSample): void {
  perfMetrics.apiRequestCount++;
  const recorded = {...sample, durationMs: Math.round(sample.durationMs)};
  if (!perfMetrics.apiSlowest || recorded.durationMs > perfMetrics.apiSlowest.durationMs) {
    perfMetrics.apiSlowest = recorded;
  }
  const samples = perfMetrics.apiSamples;
  if (!samples) return;
  samples.push(recorded);
  // Keep only the most recent attempts bounded; the lifetime total and slowest
  // request stay accurate even after older samples are evicted.
  if (samples.length > apiSampleLimit) samples.splice(0, samples.length - apiSampleLimit);
}
if (perfEnabled) setAPIRequestObserver(recordAPIRequest);

function topSlowAPIRequests(limit = 5): APISlowRequestGroup[] {
  const samples = perfMetrics.apiSamples;
  if (!samples || samples.length === 0) return [];
  const groups = new Map<string, APISlowRequestGroup>();
  for (const sample of samples) {
    const key = `${sample.method} ${sample.path} ${sample.status}`;
    const group = groups.get(key);
    if (group) {
      group.count++;
      group.maxMs = Math.max(group.maxMs, sample.durationMs);
      group.totalMs += sample.durationMs;
    } else {
      groups.set(key, {method: sample.method, path: sample.path, status: sample.status, count: 1, maxMs: sample.durationMs, totalMs: sample.durationMs});
    }
  }
  return [...groups.values()].sort((left, right) => right.maxMs - left.maxMs || right.totalMs - left.totalMs).slice(0, limit);
}

// The panel owns one refresh timer and one copy-feedback timer. Keeping them at
// module scope lets the close button and the advanced-settings switch stop them
// through the same path.
let perfPanelTimer: number | undefined;
let perfPanelFeedbackTimer: number | undefined;

// The stored preference is the normal source of truth, but storage can be
// unavailable while `?perf=1` still shows the panel for this load. Fall back to
// the rendered panel so the settings switch never claims "off" for a visible
// panel.
function isPerfPanelOn(): boolean {
  return Boolean(document.getElementById("perf-panel")) || readPerfPanelPreference();
}

// Keep the advanced-settings control truthful when the same preference changes
// from somewhere else, such as the panel's own close button.
function syncPerfPanelSettingsUI(enabled: boolean): void {
  const toggle = document.querySelector<HTMLInputElement>("#perf-panel-setting");
  if (toggle) toggle.checked = enabled;
  const status = document.querySelector<HTMLElement>("#perf-panel-status");
  if (status) status.textContent = enabled ? "已开启" : "已关闭";
}

// Enabling at runtime is cheap: the observer is one function reference and the
// panel owns its timer. Reusing this path avoids a reload, so the Owner keeps the
// page state they are diagnosing.
function enablePerfPanel(): void {
  writePerfPanelPreference(true);
  if (!perfMetrics.apiSamples) perfMetrics.apiSamples = [];
  setAPIRequestObserver(recordAPIRequest);
  startPerfPanel();
  syncPerfPanelSettingsUI(true);
}

// Closing the panel is also an opt-out. Clearing the preference, the observer,
// and the sample buffer together keeps a disabled visit at zero overhead.
function disablePerfPanel(): void {
  writePerfPanelPreference(false);
  setAPIRequestObserver(null);
  perfMetrics.apiSamples = null;
  if (perfPanelTimer !== undefined) {
    window.clearInterval(perfPanelTimer);
    perfPanelTimer = undefined;
  }
  if (perfPanelFeedbackTimer !== undefined) {
    window.clearTimeout(perfPanelFeedbackTimer);
    perfPanelFeedbackTimer = undefined;
  }
  document.getElementById("perf-panel")?.remove();
  syncPerfPanelSettingsUI(false);
}

// A normal visit never reaches this function, so no panel DOM and no shared
// sample buffer are allocated unless the preference or `?perf=1` turned it on.
function startPerfPanel(): void {
  if (!perfMetrics.apiSamples || document.getElementById("perf-panel")) return;

  const panel = document.createElement("section");
  panel.id = "perf-panel";
  panel.className = "perf-panel";
  panel.setAttribute("role", "status");
  panel.setAttribute("aria-live", "off");

  const head = document.createElement("div");
  head.className = "perf-panel-head";
  const title = document.createElement("strong");
  title.textContent = "性能";
  const hideButton = document.createElement("button");
  hideButton.type = "button";
  hideButton.id = "perf-hide";
  hideButton.className = "perf-panel-action";
  hideButton.textContent = "关闭性能面板";
  hideButton.title = "关闭性能面板并清除偏好";
  hideButton.setAttribute("aria-label", "关闭性能面板");
  head.append(title, hideButton);

  const rows = document.createElement("dl");
  rows.className = "perf-panel-rows";
  const addRow = (label: string): HTMLElement => {
    const term = document.createElement("dt");
    term.textContent = label;
    const value = document.createElement("dd");
    rows.append(term, value);
    return value;
  };
  const firstScreenValue = addRow("首屏");
  const prefetchValue = addRow("预取");
  const listRefreshValue = addRow("列表刷新");
  const taskOpenValue = addRow("任务打开");
  const apiCountValue = addRow("API 请求");
  const apiSlowestValue = addRow("最慢请求");

  const slowSection = document.createElement("div");
  slowSection.className = "perf-slow-requests";
  const slowTitle = document.createElement("strong");
  slowTitle.textContent = "慢请求 Top 5";
  const slowTotal = document.createElement("div");
  slowTotal.className = "perf-slow-total";
  const slowSlowest = document.createElement("div");
  slowSlowest.className = "perf-slow-slowest";
  const slowTop = document.createElement("ol");
  slowTop.className = "perf-slow-top";
  slowSection.append(slowTitle, slowTotal, slowSlowest, slowTop);

  const copyButton = document.createElement("button");
  copyButton.type = "button";
  copyButton.id = "perf-copy";
  copyButton.className = "perf-panel-action";
  copyButton.textContent = "复制";

  const snapshot = () => {
    const listRefreshAvgMs = perfMetrics.listRefreshRequests > 0 ? Math.round(perfMetrics.listRefreshTotalMs / perfMetrics.listRefreshRequests) : 0;
    const taskOpenAvgMs = perfMetrics.taskOpenCount > 0 ? Math.round(perfMetrics.taskOpenTotalMs / perfMetrics.taskOpenCount) : 0;
    // 0 means "not measured yet" in `perfMetrics`, so show a placeholder instead
    // of a 0ms that looks like a real measurement.
    const firstScreen = perfMetrics.firstScreenMs > 0 ? `${perfMetrics.firstScreenMs}ms` : "—";
    const slowest = perfMetrics.apiSlowest;
    const slowestText = slowest ? `${slowest.method} ${slowest.path} ${slowest.status} ${slowest.durationMs}ms` : "—";
    const slowTopLines = topSlowAPIRequests().map(item => `${item.method} ${item.path} ${item.status} ${item.maxMs}ms ×${item.count}`);
    return {
      firstScreen,
      prefetch: `${perfMetrics.prefetchRuns} 次 / ${perfMetrics.prefetchRequests} 请求`,
      listRefresh: `${perfMetrics.listRefreshRequests} 次 / 最近 ${perfMetrics.listRefreshLastMs}ms / 平均 ${listRefreshAvgMs}ms`,
      taskOpen: `${perfMetrics.taskOpenCount} 次 / 最近 ${perfMetrics.taskOpenLastMs}ms / 平均 ${taskOpenAvgMs}ms`,
      apiCount: `${perfMetrics.apiRequestCount} 次`,
      slowest: slowestText,
      slowTop: slowTopLines,
      text: [
        `首屏: ${firstScreen}`,
        `预取: ${perfMetrics.prefetchRuns} 次 / ${perfMetrics.prefetchRequests} 请求`,
        `列表刷新: ${perfMetrics.listRefreshRequests} 次 / 最近 ${perfMetrics.listRefreshLastMs}ms / 平均 ${listRefreshAvgMs}ms`,
        `任务打开: ${perfMetrics.taskOpenCount} 次 / 最近 ${perfMetrics.taskOpenLastMs}ms / 平均 ${taskOpenAvgMs}ms`,
        `API 请求: ${perfMetrics.apiRequestCount} 次`,
        `最慢请求: ${slowestText}`,
        `慢请求 Top 5:${slowTopLines.length ? `\n${slowTopLines.map((line, index) => `${index + 1}. ${line}`).join("\n")}` : " —"}`,
      ].join("\n"),
    };
  };
  let latestText = "";
  const renderValues = () => {
    const values = snapshot();
    firstScreenValue.textContent = values.firstScreen;
    prefetchValue.textContent = values.prefetch;
    listRefreshValue.textContent = values.listRefresh;
    taskOpenValue.textContent = values.taskOpen;
    apiCountValue.textContent = values.apiCount;
    apiSlowestValue.textContent = values.slowest;
    slowTotal.textContent = `共 ${perfMetrics.apiRequestCount} 次`;
    slowSlowest.textContent = `最慢: ${values.slowest}`;
    slowTop.replaceChildren(...values.slowTop.map(line => {
      const item = document.createElement("li");
      item.textContent = line;
      return item;
    }));
    latestText = values.text;
  };

  const showCopyFeedback = (label: string) => {
    // An async clipboard result may arrive after the panel was hidden; touching
    // a detached button would only arm a timer nobody can see.
    if (!panel.isConnected) return;
    // Rapid clicks must replace the pending reset instead of stacking timers.
    if (perfPanelFeedbackTimer !== undefined) window.clearTimeout(perfPanelFeedbackTimer);
    copyButton.textContent = label;
    perfPanelFeedbackTimer = window.setTimeout(() => {
      perfPanelFeedbackTimer = undefined;
      copyButton.textContent = "复制";
    }, 1200);
  };
  const fallbackCopy = (text: string) => {
    // The clipboard promise may reject only after the panel was hidden. In that
    // case selecting the panel rows would wipe the user's current page
    // selection, so bail out before touching the selection or feedback timer.
    if (!panel.isConnected) return;
    console.warn(`[aha-perf] clipboard unavailable, metrics text:\n${text}`);
    const selection = window.getSelection();
    if (!selection) {
      // Without a selection API the fallback cannot highlight anything; still
      // answer the click instead of leaving the button silent.
      showCopyFeedback("复制失败");
      return;
    }
    const range = document.createRange();
    range.selectNodeContents(rows);
    selection.removeAllRanges();
    selection.addRange(range);
    showCopyFeedback("已选中");
  };
  copyButton.addEventListener("click", () => {
    const text = latestText || snapshot().text;
    const clipboard = navigator.clipboard;
    if (!clipboard || typeof clipboard.writeText !== "function") {
      fallbackCopy(text);
      return;
    }
    try {
      void clipboard.writeText(text).then(() => {
        showCopyFeedback("已复制");
      }, () => fallbackCopy(text));
    } catch {
      fallbackCopy(text);
    }
  });

  panel.append(head, rows, slowSection, copyButton);
  document.body.append(panel);
  renderValues();
  perfPanelTimer = window.setInterval(() => {
    if (!panel.isConnected) {
      if (perfPanelTimer !== undefined) window.clearInterval(perfPanelTimer);
      perfPanelTimer = undefined;
      return;
    }
    renderValues();
  }, 1000);
  // Closing is a real opt-out, not just a hidden panel: the same path clears the
  // stored preference and the observer, so the next visit stays lean.
  hideButton.addEventListener("click", () => disablePerfPanel());
}

const listRefreshScheduler = createListRefreshScheduler({
  debounceMs: 350,
  maxWaitMs: 1000,
  now: () => Date.now(),
  schedule: (callback, delay) => window.setTimeout(callback, delay),
  cancel: handle => window.clearTimeout(handle),
  // While a Task detail is open (or opening), the per-Task SSE keeps the detail
  // current; a list refresh would only contend with it and reset the list behind
  // the user. It stays pending until the list is visible again.
  canRefresh: () => Boolean(state.auth?.authenticated) && !document.hidden && !state.selectedTask && !openingTaskID,
  isInFlight: () => listRefreshInFlight,
  refresh: performListRefresh,
});
let authRecoveryOpen = false;
let messageSubmitPending = false;
let navigationRestoring = true;
let taskOpenVersion = 0;
let taskChannelLoadVersion = 0;
let sidebarMemoSaveTimer = 0;
let sidebarMemoResizeObserver: ResizeObserver | null = null;

document.addEventListener("click", event => {
  const target = event.target;
  if (target instanceof Element && target.closest("[data-task-filter]")) return;
  state.taskOpenFilter = "";
  document.querySelectorAll<HTMLDetailsElement>("[data-task-filter][open]").forEach(details => {
    details.open = false;
  });
});

document.addEventListener("keydown", event => {
  if (event.key !== "Escape" || !state.taskOpenFilter) return;
  state.taskOpenFilter = "";
  document.querySelectorAll<HTMLDetailsElement>("[data-task-filter][open]").forEach(details => {
    details.open = false;
  });
});

const resourceStates = Object.fromEntries([
  "projects", "workspaces", "tasks", "system", "providers", "models", "env", "accounts", "skills",
  "channels", "hardware", "mail-accounts", "mail-templates", "mail-messages", "knowledge", "proxy", "advanced", "sync", "prompts", "mcp", "data",
].map(key => [key, {loading: false, loaded: false, error: "", updatedAt: 0}])) as Record<ResourceKey, ResourceState>;
const resourceRequests = new Map<ResourceKey, Promise<void>>();
const listPages: Record<"projects" | "tasks" | "knowledge", {cursor: string; hasMore: boolean; loadingMore: boolean}> = {
  projects: {cursor: "", hasMore: false, loadingMore: false},
  tasks: {cursor: "", hasMore: false, loadingMore: false},
  knowledge: {cursor: "", hasMore: false, loadingMore: false},
};
const initialListPageSize = 50;
// Matches the server's maxListPageSize.
const maxListPageSize = 200;
// Kept as a plain DOM list, not virtualized. Measured 2026-11 on a 200-Task
// fixture: 33 rows rendered on first load under the default status filters, 133
// after every load-more (the other Tasks were completed and hidden), with two
// row heights (98px / 74px). That is far below the ~500-row point where keeping
// every row mounted stops being reasonable; 200 is also the server's cap. The
// approval list uses the same 200 cap and measured 190 rows of one fixed 56px
// height, so it stays a plain DOM list too. Re-measure before raising either cap.
// Set when "加载更多" brought in tasks but none of them pass the current filters,
// so the unchanged list does not read as a broken page.
let taskLoadMoreNoMatch = false;

// The task page asks for unfinished and failed Tasks ahead of completed ones,
// so the first page shows the work that needs the owner even when completed
// Tasks are the majority. Every task list request must use the same order,
// because a cursor only continues the order that minted it.
const taskListOrder = "attention" as const;

// Live refreshes re-read as many tasks as are already loaded, so pages the owner
// pulled in with "加载更多" do not collapse back to the first page.
function taskRefreshLimit(): number {
  return Math.min(Math.max(initialListPageSize, state.tasks.length), maxListPageSize);
}

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
    case "resources": return ["hardware", "mail-accounts", "mail-templates", "mail-messages"];
    case "knowledge": return ["projects", "workspaces", "knowledge"];
    case "prompts": return ["prompts"];
    case "proxy": return ["proxy"];
    case "sync": return ["sync"];
    case "advanced": return ["advanced"];
    case "data": return [];
    case "mcp": return [];
    case "other": return ["advanced"];
    case "pet-settings": return ["advanced"];
    case "pet-workbench": return ["advanced"];
  }
}

const loadResource = createResourceLoader({
  states: resourceStates,
  requests: resourceRequests,
  onStart: () => {
    // Counted after the cache/in-flight early returns, so this reflects requests
    // actually issued during an idle prefetch.
    if (prefetchRequestTracking > 0) perfMetrics.prefetchRequests++;
    window.queueMicrotask(() => {
      if (state.auth?.authenticated) render();
    });
  },
  onFinish: () => {
    if (state.auth?.authenticated) render();
  },
});

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
    blocked: "受阻",
    cancelled: "已取消",
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
  return type === "git" ? "Git 仓库" : type === "channel" ? "渠道宿主" : type === "system" ? "系统托管" : "普通文件夹";
}

function isSystemProject(project: Project): boolean {
  return project.project_type === "system" || project.project_type === "channel";
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
  const agentAPIStatus = dialog.querySelector<HTMLElement>(".workspace-agent-api-status");
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
  if (agentAPIStatus) {
    agentAPIStatus.textContent = workspaceAgentAPIStatusText(ws);
  }
  syncWorkspaceFields();
  const title = dialog.querySelector(".dialog-head h2");
  if (title) title.textContent = ws ? "编辑 Workspace" : "添加 Workspace";
  dialog.showModal();
}

// Provider fields live in a button-driven dialog, so the browser's native
// `required` validation never runs. The error text sits next to the input and
// doubles as its accessible description.
function setProviderFieldError(input: HTMLInputElement | null, message: string): void {
  if (!input) return;
  const error = input.nextElementSibling as HTMLElement | null;
  if (message) input.setAttribute("aria-invalid", "true");
  else input.removeAttribute("aria-invalid");
  if (error) {
    error.textContent = message;
    error.hidden = !message;
  }
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
  setProviderFieldError(nameInput, "");
  setProviderFieldError(baseInput, "");
  const title = dialog.querySelector(".dialog-head h2");
  if (title) title.textContent = provider ? "编辑 Provider" : "添加 Provider";
  dialog.showModal();
}

// backendOptionsForWorkspace derives the task's allowed backends from the
// selected workspace's detection results. Falls back to the backends that have
// configured models when the workspace has not been detected yet.
function backendOptionsForWorkspace(ws?: Workspace): string[] {
  return resolveBackendOptions({
    capabilities: ws ? ((ws.capabilities || {}) as Record<string, {status?: string}>) : undefined,
    detected: Boolean(ws && ws.health !== "unknown"),
    presentBackends: state.models.map(item => item.backend),
    hasCodexAccount: state.codexAccounts.length > 0,
  });
}

function claudeOfficialCatalog(workspace?: Workspace): ClaudeOfficialCatalog {
  const probe = (workspace?.capabilities || {}).claude as {
    status?: string;
    auth_status?: string;
    models?: ClaudeModelOption[];
    models_error?: string;
    auth_error?: string;
  } | undefined;
  if (!probe || probe.status !== "ready") {
    return {availability: "unavailable", models: [], message: "暂不可用：当前 Workspace 未检测到 Claude Code CLI。"};
  }
  if (probe.auth_status === "not_logged_in") {
    return {availability: "not_logged_in", models: [], message: "暂不可用：当前 Workspace 尚未登录 Claude Code，请先执行 claude auth login。"};
  }
  if (probe.auth_status !== "logged_in") {
    // The probe keeps the reason it could not read a status. Showing it is the
    // difference between the operator knowing what failed and guessing.
    const reason = typeof probe.auth_error === "string" ? probe.auth_error.trim() : "";
    return {
      availability: "unknown",
      models: [],
      message: reason
        ? `暂不可用：Claude Code 登录状态检测失败（${reason}）。`
        : "暂不可用：尚未完成 Claude Code 登录状态检测。",
    };
  }
  const models = Array.isArray(probe.models) ? probe.models : [];
  if (models.length > 0) {
    return {availability: "ready", models, message: `已检测到 ${models.length} 个官方模型。`};
  }
  return {
    availability: "unavailable",
    models: [],
    message: probe.models_error ? `暂不可用：${probe.models_error}` : "暂不可用：未能获取官方模型列表。",
  };
}

function syncTaskBackend(): void {
  const wsID = String((document.querySelector<HTMLSelectElement>("#task-workspace"))?.value || "");
  const ws = state.workspaces.find(item => item.id === wsID);
  const options = backendOptionsForWorkspace(ws);
  setRuntimeBackends("task", options);
  setClaudeOfficialCatalog("task", claudeOfficialCatalog(ws));
  // Create Task always starts at high; a model default must not replace it.
  syncRuntimeFields("task", state.models, state.codexAccounts, false);
  syncTaskGitIsolation();
}

function syncTakeoverTaskBackend(): void {
  const wsID = String(document.querySelector<HTMLSelectElement>("#takeover-task-workspace")?.value || "");
  const workspace = state.workspaces.find(item => item.id === wsID);
  setRuntimeBackends("takeover-task", backendOptionsForWorkspace(workspace));
  setClaudeOfficialCatalog("takeover-task", claudeOfficialCatalog(workspace));
  syncRuntimeFields("takeover-task", state.models, state.codexAccounts);
}

const claudeOfficialDetectionInFlight = new Map<string, Promise<void>>();

async function refreshClaudeOfficialCatalog(prefix: string, workspaceID: string): Promise<void> {
  const workspace = state.workspaces.find(item => item.id === workspaceID);
  if (!workspace) {
    setClaudeOfficialCatalog(prefix, {availability: "unavailable", models: [], message: "暂不可用：未选择 Workspace。"});
    syncRuntimeFields(prefix, state.models, state.codexAccounts);
    return;
  }
  const key = `${prefix}:${workspaceID}`;
  const running = claudeOfficialDetectionInFlight.get(key);
  if (running) return running;
  const detection = (async () => {
    setClaudeOfficialLoading(prefix);
    syncRuntimeFields(prefix, state.models, state.codexAccounts, false);
    try {
      const result = await detectWorkspaceWithHostKeyTrust(workspaceID);
      if (!result) {
        setClaudeOfficialCatalog(prefix, {availability: "unknown", models: [], message: "检测已取消。"});
        return;
      }
      state.workspaces = state.workspaces.map(item => item.id === result.workspace.id ? result.workspace : item);
      setClaudeOfficialCatalog(prefix, claudeOfficialCatalog(result.workspace));
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      setClaudeOfficialCatalog(prefix, {availability: "unavailable", models: [], message: `暂不可用：${message}`});
    } finally {
      syncRuntimeFields(prefix, state.models, state.codexAccounts, false);
    }
  })();
  claudeOfficialDetectionInFlight.set(key, detection);
  try {
    await detection;
  } finally {
    if (claudeOfficialDetectionInFlight.get(key) === detection) claudeOfficialDetectionInFlight.delete(key);
  }
}

function bindClaudeOfficialDetection(prefix: string, workspaceID: () => string): void {
  document.querySelector<HTMLSelectElement>(`#${prefix}-model-source`)?.addEventListener("change", event => {
    const source = event.currentTarget as HTMLSelectElement;
    delete source.dataset.initialOfficial;
    if (source.value === "claude_native") void refreshClaudeOfficialCatalog(prefix, workspaceID());
  });
}

function assertClaudeOfficialReady(prefix: string): void {
  const source = document.querySelector<HTMLSelectElement>(`#${prefix}-model-source`);
  const model = document.querySelector<HTMLSelectElement>(`#${prefix}-claude-native-model`);
  if (source?.value !== "claude_native" || model?.dataset.ready === "true" || source.dataset.initialOfficial === "true") return;
  const help = document.querySelector<HTMLElement>(`#${prefix}-claude-native-help`)?.textContent || "Claude Code Official 暂不可用。";
  throw new Error(help);
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
  const isolation = document.querySelector<HTMLSelectElement>("#task-isolation");
  if (isolation) isolation.value = "inplace";
  const worktreeDir = document.querySelector<HTMLInputElement>("#task-worktree-dir");
  if (worktreeDir) worktreeDir.value = "";
  syncTaskSkills();
  syncTaskBackend();
}

let taskSkillsDirty = false;

// The Agent config dialog compares the live form with the snapshot taken when
// it opened. Saving or discarding resets the baseline, so only edits that have
// not been written prompt on close.
let agentConfigBaseline = "";

function agentConfigFormSnapshot(form: HTMLFormElement): string {
  const values: Record<string, unknown> = {};
  for (const [key, value] of new FormData(form).entries()) {
    const existing = values[key];
    if (existing === undefined) values[key] = String(value);
    else if (Array.isArray(existing)) existing.push(String(value));
    else values[key] = [String(existing), String(value)];
  }
  return agentConfigSnapshot(values);
}

function agentConfigDirty(dialog: HTMLDialogElement): boolean {
  const form = dialog.querySelector<HTMLFormElement>("#agent-config-form");
  if (!form || !agentConfigBaseline) return false;
  return agentConfigChanged(agentConfigBaseline, agentConfigFormSnapshot(form));
}

function confirmAgentConfigClose(dialog: HTMLDialogElement): boolean {
  if (!agentConfigDirty(dialog)) return true;
  return window.confirm("Agent 配置尚未保存，关闭将丢弃这些修改。确定关闭？");
}

let vaultTaskSkillsModule: typeof import("./task_vault_skills.js") | null = null;

function availableVaultTaskSkills(projectID: string, includeIDs: string[] = []): Skill[] {
  return vaultTaskSkillsModule
    ? vaultTaskSkillsModule.vaultTaskSkillsForProject(state.vaultSkills, state.vaultSkillsRead, state.vaultSkillsIndex, state.vaultSkillsBound, projectID, includeIDs)
    : [];
}

function vaultTaskSkillSourceActive(projectID: string): boolean {
  return vaultTaskSkillsModule
    ? vaultTaskSkillsModule.vaultTaskSkillSourceActive(state.vaultSkillsOn, state.vaultSkillsRead, state.vaultSkillsIndex, state.vaultSkillsBound, projectID)
    : false;
}

function autoTaskSkillIDs(projectID: string): string[] {
  const items = availableTaskSkills(projectID);
  return vaultTaskSkillsModule
    ? vaultTaskSkillsModule.taskAutoSkillIDs(items, vaultTaskSkillSourceActive(projectID), projectID)
    : items
      .filter(item => item.auto_attach &&
        (item.scope === "global" || (item.scope === "project" && item.project_id === projectID)))
      .map(item => item.id);
}

function availableTaskSkills(projectID: string, includeIDs: string[] = []): Skill[] {
  const legacy = state.skills.filter(item => item.enabled && item.status === "active" && (item.scope === "global" || item.project_id === projectID || item.bound_project_id === projectID));
  return [...legacy, ...availableVaultTaskSkills(projectID, includeIDs)];
}

function taskSkillSourceHint(projectID: string): string {
  if (!vaultTaskSkillsModule) return "";
  const readiness = vaultTaskSkillsModule.vaultTaskSkillHint(state.vaultSkillsOn, state.vaultSkillsRead, state.vaultSkillsIndex, state.vaultSkillsBound, projectID);
  if (readiness) return readiness;
  return vaultTaskSkillsModule.vaultTaskAutoSourceHint(vaultTaskSkillSourceActive(projectID));
}

function taskSkillOptions(projectID: string, selected: string[] = [], includeAutoDefaults = false): string {
  const items = availableTaskSkills(projectID);
  const defaults = includeAutoDefaults ? autoTaskSkillIDs(projectID) : [];
  const hint = taskSkillSourceHint(projectID);
  return renderTaskSkills(items, projectID, [...new Set([...selected, ...defaults])])
    + (hint ? `<div class="field-help task-skill-source-hint">${escapeHTML(hint)}</div>` : "");
}

function syncTaskSkills(): void {
  const projectID = String((document.querySelector<HTMLSelectElement>("#task-project"))?.value || "");
  const container = document.querySelector<HTMLElement>("#task-skill-options");
  if (!container) return;
  const selected = [...container.querySelectorAll<HTMLInputElement>('input[name="skill_ids"]:checked')].map(input => input.value);
  const nextSelected = taskSkillsDirty ? selected : autoTaskSkillIDs(projectID);
  container.innerHTML = taskSkillOptions(projectID, nextSelected);
  syncTaskFormFoldCounts();
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

// workspaceAgentAPIStatusText describes the connection AHA selected. It is
// deliberately read-only: the normal Workspace dialog no longer offers a mode or
// address, because choosing either by hand is what made remote workspaces break
// when they left the LAN.
function workspaceAgentAPIStatusText(workspace: Workspace | null): string {
  if (!workspace) return "保存后由 AHA 自动选择连接方式。";
  const probe = (workspace.capabilities?.agent_api || {}) as {via?: string; url?: string; error?: string};
  const migration = workspace.agent_api_migration_note ? `配置迁移：${workspace.agent_api_migration_note}` : "";
  if ((workspace.agent_api_status === "ready" || probe.status === "ready") && (workspace.agent_api_resolved_url || probe.url)) {
    const via = probe.via === "stdio" ? "SSH stdio" : probe.via === "tunnel" ? "SSH 反向隧道" : "直连";
    return `${via}（自动选择）${migration ? ` · ${migration}` : ""}`;
  }
  if (workspace.agent_api_status === "error" || probe.status === "unavailable") {
    const reason = workspace.agent_api_error || probe.error || "自动连接失败";
    return `不可用：${reason}${migration ? ` · ${migration}` : ""}`;
  }
  return `自动选择（尚未检测）${migration ? ` · ${migration}` : ""}`;
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

function modelSupportsImage(model: Model): boolean {
  return model.capabilities?.supports_image_input !== false;
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
  // Selecting a preset is an edit too: do not leave a stale "required" error
  // next to a field the preset just filled.
  setProviderFieldError(nameInput, "");
  setProviderFieldError(baseInput, "");
}

function setMessage(type: "error" | "notice", value: string): void {
  state[type] = value;
  // Feedback banners are re-created by the next render, so announce from the
  // persistent region instead of relying on a newly inserted live node.
  announceStatus(String(value));
  window.setTimeout(() => {
    if (state[type] === value) {
      state[type] = "";
      render();
    }
  }, 5000);
}

async function runWithFeedback(
  button: HTMLElement | null,
  label: string,
  action: () => Promise<void>,
  onError?: (message: string) => void,
): Promise<void> {
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
    const message = error instanceof Error ? error.message : String(error);
    if (onError) {
      onError(message);
    } else {
      setMessage("error", message);
      render();
    }
  } finally {
    button.disabled = wasDisabled;
    button.innerHTML = original;
  }
}

function viewFromName(value: unknown): View | null {
  if (value === "pet-diary") return "pet-workbench"; // Legacy bookmarks and saved navigation.
  if (value === "hardware") return "resources";
  const views: View[] = ["projects", "channels", "models", "resources", "tasks", "knowledge", "prompts", "proxy", "sync", "advanced", "data", "other", "mcp", "pet-settings", "pet-workbench"];
  return views.includes(value as View) ? value as View : null;
}

// #hardware is the pre-resources bookmark. Rewrite it once at boot so old
// links land on the resources page's hardware tab instead of disappearing.
function applyLocationHash(): boolean {
  const raw = window.location.hash.replace(/^#/, "");
  if (!raw) return false;
  const [name, query = ""] = raw.split("?");
  const view = viewFromName(name);
  if (!view) return false;
  state.view = view;
  if (name === "pet-diary") window.history.replaceState(null, "", "#pet-workbench");
  if (view === "resources") {
    const params = new URLSearchParams(query);
    state.resourcesTab = params.get("tab") === "mail" ? "mail" : "hardware";
    if (name === "hardware") {
      window.history.replaceState(null, "", "#resources?tab=hardware");
    }
  }
  return true;
}

// applyLocationHash() gives a hash priority over the navigation snapshot at
// boot. Keep the address bar honest when the user leaves a deep-linked view:
// drop the old hash so the next refresh restores the view they are on now.
function syncViewHash(): void {
  const target = staleViewHashTarget(
    window.location.hash,
    window.location.pathname,
    window.location.search,
    state.view,
    name => viewFromName(name) !== null,
  );
  if (target === null) return;
  window.history.replaceState(null, "", target);
}

// Switching the top-level view is shared by the sidebar, the palette and the
// mobile search entry so every path resets the same per-view state.
function switchView(nextView: View): void {
  if (nextView !== "pet-workbench") closePetWorkbenchDialogs();
  cancelTaskOpen();
  state.view = nextView;
  if (nextView === "models") {
    state.modelSearch = "";
    state.modelPage = 1;
    state.modelSelected.clear();
    state.providerPage = 1;
    state.codexAccountPage = 1;
  }
  if (nextView === "proxy") resourceStates.proxy.loaded = false;
  state.selectedTask = null;
  clearTaskCardFocusReturn();
  state.selectedProject = null;
  state.dialogProjectID = "";
  closeEvents();
  render();
  void ensureViewData(state.view, nextView === "proxy").then(() => {
    // Entering the workbench already rendered its page and started exactly one
    // load; a second full render here used to rebind and abort that request.
    if (state.view === "pet-workbench" && petWorkbenchPageKeeper.binding) return;
    render();
  });
}

function showTaskCreateDialog(): boolean {
  const dialog = document.querySelector<HTMLDialogElement>("#task-dialog");
  if (!dialog) return false;
  taskSkillsDirty = false;
  syncTaskWorkspaces();
  dialog.showModal();
  return true;
}

/* Global command palette -------------------------------------------------- */

const commandPaletteDebouncer = createDebouncer(COMMAND_PALETTE_DEBOUNCE_MS, query => applyCommandPaletteQuery(query));
let commandPaletteRoot: HTMLDialogElement | null = null;
let commandPaletteInput: HTMLInputElement | null = null;
let commandPaletteListbox: HTMLElement | null = null;
let commandPaletteCount: HTMLElement | null = null;
let commandPaletteItems: CommandItem[] = [];
let commandPaletteRecent: CommandItem[] = [];
let commandPaletteSections: CommandSection[] = [];
let commandPaletteVisible: CommandItem[] = [];
let commandPaletteActiveIndex = 0;
let commandPaletteComposing = false;
let commandPaletteCompositionEndedAt = 0;
let commandPaletteOpenedFrom: HTMLElement | null = null;

function commandPaletteStorage(): Storage | null {
  try {
    return window.localStorage;
  } catch {
    return null;
  }
}

function commandPaletteShortcutLabel(): string {
  return /Mac|iPhone|iPad/.test(navigator.platform || "") ? "⌘K" : "Ctrl K";
}

function commandPaletteTaskItems(): CommandItem[] {
  return state.tasks.map(task => {
    const project = state.projects.find(item => item.id === task.project_id);
    const workspace = state.workspaces.find(item => item.id === task.workspace_id);
    const status = statusLabel(task.status);
    const code = task.code || "";
    return {
      id: `task:${task.id}`,
      kind: "task" as const,
      label: task.title,
      detail: [code, project?.name, workspace?.name, status].filter(Boolean).join(" · "),
      keywords: [code, code.replace(/^task-/, ""), task.status, status, project?.name || "", workspace?.name || "", task.current_goal || ""],
      target: task.id,
    };
  });
}

function commandPalettePages(): CommandItem[] {
  return [
    {id: "page:models", kind: "page", label: t("nav.models"), detail: t("palette.page.modelsDetail"), keywords: ["model", "models", "模型"], target: "models"},
    {id: "page:pet-workbench", kind: "page", label: t("palette.petApprovalsPage"), detail: t("palette.page.approvalsDetail"), keywords: ["approval", "approvals", "审批", "pet", "宠物"], target: "pet-workbench"},
    {id: "page:knowledge", kind: "page", label: t("nav.knowledge"), detail: t("palette.page.knowledgeDetail"), keywords: ["knowledge", "doc", "知识", "文档"], target: "knowledge"},
    {id: "page:channels", kind: "page", label: t("nav.channels"), detail: t("palette.page.channelsDetail"), keywords: ["channel", "channels", "bot", "渠道"], target: "channels"},
    {id: "page:mcp", kind: "page", label: "MCP", detail: t("palette.page.mcpDetail"), keywords: ["mcp", "tool", "工具"], target: "mcp"},
    {id: "page:advanced", kind: "page", label: t("nav.advancedSettings"), detail: t("palette.page.advancedDetail"), keywords: ["settings", "advanced", "设置", "高级"], target: "advanced"},
    {id: "page:data", kind: "page", label: t("advanced.data.title"), detail: t("palette.page.dataDetail"), keywords: ["data", "storage", "数据", "清理"], target: "data"},
  ];
}

function commandPaletteActions(): CommandItem[] {
  return [
    {id: "action:new-task", kind: "action", label: t("palette.newTask"), detail: t("palette.newTaskDetail"), keywords: ["new", "create", "task", "新建", "任务"], target: "new-task"},
    {id: "action:open-models", kind: "action", label: t("palette.openModels"), detail: t("nav.models"), keywords: ["model", "models", "模型"], target: "models"},
    {id: "action:open-approvals", kind: "action", label: t("palette.openApprovals"), detail: t("petWorkbench.approvals"), keywords: ["approval", "approvals", "审批"], target: "approvals"},
    {id: "action:open-pet-workbench", kind: "action", label: t("palette.openPetWorkbench"), detail: t("petWorkbench.title"), keywords: ["pet", "workbench", "宠物", "工作台"], target: "pet-workbench"},
  ];
}

function commandPaletteGroupLabels(): Partial<Record<CommandSectionID, string>> {
  return {
    tasks: t("nav.tasks"),
    projects: t("nav.projects"),
    workspaces: "Workspace",
    pages: t("palette.group.pages"),
    actions: t("palette.group.actions"),
    recent: t("palette.group.recent"),
  };
}

function commandPaletteAllItems(): CommandItem[] {
  const items = [...commandPaletteTaskItems()];
  for (const project of state.projects) {
    items.push({id: `project:${project.id}`, kind: "project", label: project.name, detail: t("palette.projectDetail"), keywords: [project.name, "project", "项目"], target: project.id});
  }
  for (const workspace of state.workspaces) {
    const project = state.projects.find(item => item.id === workspace.project_id);
    items.push({
      id: `workspace:${workspace.id}`,
      kind: "workspace",
      label: workspace.name,
      detail: ["Workspace", project?.name, workspace.root_path].filter(Boolean).join(" · "),
      keywords: [workspace.name, workspace.root_path, project?.name || "", "workspace"],
      target: workspace.project_id,
    });
  }
  items.push(...commandPalettePages(), ...commandPaletteActions());
  return items;
}

// Recents are resolved against the loaded Task store on every open, so a Task
// deleted (or no longer visible) since it was opened cannot come back from
// localStorage. The store is already the server's filtered view.
function commandPaletteRecentItems(items: CommandItem[]): CommandItem[] {
  const tasks = new Map(items.filter(item => item.kind === "task").map(item => [item.target, item]));
  return readRecentTaskIDs(commandPaletteStorage())
    .map(taskID => tasks.get(taskID))
    .filter((item): item is CommandItem => Boolean(item));
}

function rememberCommandPaletteTask(taskID: string): void {
  rememberRecentTaskID(commandPaletteStorage(), taskID);
}

// A background list refresh may add, rename or delete Tasks while the panel is
// open. Rebuild the index from the new store so a removed Task cannot be
// selected; the current query is applied immediately instead of waiting.
function rebuildOpenCommandPaletteIndex(): void {
  if (!commandPaletteRoot?.open) return;
  commandPaletteDebouncer.cancel();
  commandPaletteItems = commandPaletteAllItems();
  commandPaletteRecent = commandPaletteRecentItems(commandPaletteItems);
  applyCommandPaletteQuery(commandPaletteInput?.value || "");
}

function ensureCommandPalette(): HTMLDialogElement | null {
  if (commandPaletteRoot) return commandPaletteRoot;
  if (!document.body) return null;
  const root = document.createElement("dialog");
  root.id = "command-palette";
  root.className = "command-palette";
  root.setAttribute("aria-label", t("palette.ariaLabel"));
  root.innerHTML = `<div class="command-palette-box">
    <div class="command-palette-field">
      ${icon("search")}
      <input id="command-palette-input" type="text" role="combobox" aria-expanded="true" aria-controls="command-palette-listbox" aria-autocomplete="list" aria-label="${t("palette.placeholder")}" placeholder="${t("palette.placeholder")}" autocomplete="off" spellcheck="false">
      <kbd class="command-palette-key">${commandPaletteShortcutLabel()}</kbd>
      <button type="button" class="icon-button" data-command-palette-close aria-label="${t("palette.close")}" title="${t("palette.close")}">${icon("close")}</button>
    </div>
    <p id="command-palette-count" class="command-palette-count" role="status" aria-live="polite"></p>
    <div id="command-palette-listbox" class="command-palette-results" role="listbox" aria-label="${t("palette.ariaLabel")}"></div>
    <footer class="command-palette-help"><span>${t("palette.selectHint")}</span><span>${t("palette.openHint")}</span><span>${t("palette.closeHint")}</span></footer>
  </div>`;
  document.body.appendChild(root);
  commandPaletteRoot = root;
  commandPaletteInput = root.querySelector<HTMLInputElement>("#command-palette-input");
  commandPaletteListbox = root.querySelector<HTMLElement>("#command-palette-listbox");
  commandPaletteCount = root.querySelector<HTMLElement>("#command-palette-count");
  bindCommandPalette();
  return root;
}

function bindCommandPalette(): void {
  const root = commandPaletteRoot;
  const input = commandPaletteInput;
  const listbox = commandPaletteListbox;
  if (!root || !input || !listbox) return;
  input.addEventListener("input", () => {
    if (commandPaletteComposing) return;
    commandPaletteDebouncer.schedule(input.value);
  });
  input.addEventListener("compositionstart", () => {
    commandPaletteComposing = true;
    commandPaletteCompositionEndedAt = 0;
  });
  input.addEventListener("compositionend", () => {
    commandPaletteComposing = false;
    // WebKit fires this before the keydown that commits the composition with
    // Enter, so remember when composition ended to swallow that Enter.
    commandPaletteCompositionEndedAt = performance.now();
    commandPaletteDebouncer.schedule(input.value);
  });
  input.addEventListener("keydown", event => {
    const justComposed = commandPaletteCompositionEndedAt > 0
      && performance.now() - commandPaletteCompositionEndedAt <= COMMAND_PALETTE_COMPOSITION_GUARD_MS;
    const result = paletteKeyAction(event, {
      count: commandPaletteVisible.length,
      activeIndex: commandPaletteActiveIndex,
      composing: commandPaletteComposing,
      justComposed,
    });
    if (!result.handled) return;
    event.preventDefault();
    if (result.action === "close") {
      closeCommandPalette();
      return;
    }
    if (result.action === "up" || result.action === "down") {
      setCommandPaletteActive(result.activeIndex ?? 0);
      return;
    }
    if (result.action === "select") {
      // Enter right after typing must search the text on screen, not the
      // section from before the 100ms debounce fired.
      commandPaletteDebouncer.flush();
      const item = commandPaletteVisible[commandPaletteActiveIndex];
      if (item) void executeCommandPaletteItem(item);
    }
  });
  listbox.addEventListener("click", event => {
    const option = event.target instanceof Element ? event.target.closest<HTMLElement>("[data-command-index]") : null;
    const index = Number(option?.dataset.commandIndex);
    const item = Number.isInteger(index) ? commandPaletteVisible[index] : undefined;
    if (item) void executeCommandPaletteItem(item);
  });
  root.querySelector<HTMLElement>("[data-command-palette-close]")?.addEventListener("click", () => closeCommandPalette());
  // Clicking the backdrop is outside .command-palette-box.
  root.addEventListener("click", event => {
    if (event.target === root) closeCommandPalette();
  });
  root.addEventListener("cancel", event => {
    event.preventDefault();
    if (!commandPaletteComposing) closeCommandPalette();
  });
}

function applyCommandPaletteQuery(query: string): void {
  commandPaletteSections = buildCommandSections({query, items: commandPaletteItems, recent: commandPaletteRecent, labels: commandPaletteGroupLabels()});
  commandPaletteVisible = flattenCommandSections(commandPaletteSections);
  commandPaletteActiveIndex = 0;
  renderCommandPaletteResults();
  updateCommandPaletteActive();
}

function renderCommandPaletteResults(): void {
  const listbox = commandPaletteListbox;
  const count = commandPaletteCount;
  if (!listbox || !count) return;
  let index = 0;
  const groups = commandPaletteSections.map(section => {
    const options = section.items.map(item => {
      const optionIndex = index++;
      return `<div role="option" id="command-palette-option-${optionIndex}" data-command-index="${optionIndex}" class="command-palette-option" aria-selected="false">
        <span class="command-palette-option-label">${escapeHTML(item.label)}</span>
        <small class="command-palette-option-detail">${escapeHTML(item.detail)}</small>
      </div>`;
    }).join("");
    return `<section class="command-palette-group" role="group" aria-labelledby="command-palette-group-${section.id}"><h3 id="command-palette-group-${section.id}">${escapeHTML(section.label)}</h3>${options}</section>`;
  }).join("");
  listbox.innerHTML = groups || `<div class="command-palette-empty">${t("palette.noResults")}</div>`;
  count.textContent = commandPaletteVisible.length ? t("palette.resultCount", {count: commandPaletteVisible.length}) : t("palette.noResults");
  commandPaletteInput?.removeAttribute("aria-activedescendant");
}

function setCommandPaletteActive(index: number): void {
  if (!commandPaletteVisible.length) {
    commandPaletteActiveIndex = 0;
    updateCommandPaletteActive();
    return;
  }
  commandPaletteActiveIndex = Math.min(Math.max(0, index), commandPaletteVisible.length - 1);
  updateCommandPaletteActive();
}

function updateCommandPaletteActive(): void {
  const options = commandPaletteListbox?.querySelectorAll<HTMLElement>("[data-command-index]") || [];
  const activeOption = commandPaletteActiveIndex >= 0 && commandPaletteActiveIndex < options.length ? options[commandPaletteActiveIndex] : null;
  options.forEach(option => {
    const active = option === activeOption;
    option.setAttribute("aria-selected", String(active));
    option.classList.toggle("active", active);
  });
  if (activeOption) {
    commandPaletteInput?.setAttribute("aria-activedescendant", activeOption.id);
    activeOption.scrollIntoView({block: "nearest"});
  } else {
    commandPaletteInput?.removeAttribute("aria-activedescendant");
  }
}

function openCommandPalette(): void {
  if (!state.auth?.authenticated) return;
  const root = ensureCommandPalette();
  const input = commandPaletteInput;
  if (!root || !input) return;
  commandPaletteOpenedFrom = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  // Rebuild from the live stores on every open: a refresh may have added,
  // renamed or removed Tasks since the palette was last used.
  commandPaletteItems = commandPaletteAllItems();
  commandPaletteRecent = commandPaletteRecentItems(commandPaletteItems);
  commandPaletteComposing = false;
  commandPaletteCompositionEndedAt = 0;
  commandPaletteDebouncer.cancel();
  input.value = "";
  applyCommandPaletteQuery("");
  if (!root.open) root.showModal();
  input.focus();
}

// Dismissing the palette returns focus where it was. Executing an item does
// not: that focus may be a Task composer whose render guard would keep the old
// Task on screen while the selected Task loads underneath.
function closeCommandPalette(restoreFocus = true): void {
  commandPaletteDebouncer.cancel();
  commandPaletteComposing = false;
  commandPaletteCompositionEndedAt = 0;
  if (commandPaletteRoot?.open) commandPaletteRoot.close();
  const previous = commandPaletteOpenedFrom;
  commandPaletteOpenedFrom = null;
  if (!restoreFocus) return;
  if (previous?.isConnected) previous.focus();
  else focusCommandPaletteTrigger();
}

function focusCommandPaletteTrigger(): void {
  const triggers = Array.from(document.querySelectorAll<HTMLElement>("[data-command-palette-open]"));
  const visible = triggers.find(trigger => trigger.getClientRects().length > 0);
  (visible || triggers[0])?.focus();
}

function openCommandPaletteProject(projectID: string): void {
  const project = state.projects.find(item => item.id === projectID) || null;
  state.view = "projects";
  state.selectedProject = project;
  state.dialogProjectID = projectID;
  state.selectedTask = null;
  clearTaskCardFocusReturn();
  closeEvents();
  render();
  if (!project) void ensureViewData("projects").then(() => render());
}

async function openTaskCreateDialogFromPalette(): Promise<void> {
  state.view = "tasks";
  state.selectedTask = null;
  clearTaskCardFocusReturn();
  state.selectedProject = null;
  state.dialogProjectID = "";
  closeEvents();
  render();
  await ensureViewData("tasks");
  render();
  showTaskCreateDialog();
}

async function runCommandPaletteAction(action: string): Promise<void> {
  if (action === "new-task") {
    await openTaskCreateDialogFromPalette();
  } else if (action === "models") {
    switchView("models");
  } else if (action === "approvals") {
    petWorkbenchTab = "approvals";
    switchView("pet-workbench");
  } else if (action === "pet-workbench") {
    petWorkbenchTab = "overview";
    switchView("pet-workbench");
  }
}

async function navigateCommandPaletteItem(item: CommandItem): Promise<void> {
  if (item.kind === "task") {
    rememberCommandPaletteTask(item.target);
    await openTask(item.target);
  } else if (item.kind === "project" || item.kind === "workspace") {
    openCommandPaletteProject(item.target);
  } else if (item.kind === "page") {
    const view = viewFromName(item.target);
    if (!view) return;
    if (view === "pet-workbench") petWorkbenchTab = "approvals";
    switchView(view);
  } else {
    await runCommandPaletteAction(item.target);
  }
}

async function executeCommandPaletteItem(item: CommandItem): Promise<void> {
  try {
    // runPaletteSelection closes without restoring focus and focuses the
    // trigger before navigation; otherwise a focused Task composer makes
    // render() defer and the old Task stays on screen while the new one loads.
    await runPaletteSelection({
      closePalette: restoreFocus => closeCommandPalette(restoreFocus),
      focusTrigger: () => focusCommandPaletteTrigger(),
      navigate: () => navigateCommandPaletteItem(item),
    });
  } catch (error) {
    setMessage("error", error instanceof Error ? error.message : String(error));
    render();
  } finally {
    // The navigation replaced the shell, so the element that opened the
    // palette may be detached; put focus on the freshly rendered trigger.
    focusCommandPaletteTrigger();
  }
}

document.addEventListener("keydown", event => {
  if (event.defaultPrevented || !isPaletteShortcut(event)) return;
  event.preventDefault();
  if (commandPaletteRoot?.open) closeCommandPalette();
  else openCommandPalette();
});

// Escape dismisses the Task detail like its back control. A dialog, the command
// palette, an open Task tool panel and text controls keep their own Escape
// behavior, so only an otherwise plain detail layer is closed here.
document.addEventListener("keydown", event => {
  if (event.defaultPrevented || event.isComposing || event.keyCode === 229 || event.key !== "Escape") return;
  if (!state.selectedTask || state.taskTool || document.querySelector("dialog[open]")) return;
  if (event.target instanceof Element && event.target.closest("input, textarea, select, [contenteditable='true']")) return;
  event.preventDefault();
  closeTaskDetail();
});

async function bootstrap(): Promise<void> {
  const savedNavigation = loadNavigationSnapshot();
  const hashRouted = applyLocationHash();
  initialHashRoute = hashRouted;
  if (!hashRouted) {
    const savedView = viewFromName(savedNavigation.view);
    if (savedView) state.view = savedView;
  }
  try {
    // generalSettings is a plain GET that does not depend on the auth response,
    // so start both requests together and only await settings once the session
    // says the owner is signed in. The catch keeps a 401 for anonymous screens
    // from surfacing as an unhandled rejection.
    const authRequest = api.authStatus();
    const generalRequest = api.generalSettings().catch(() => null);
    state.auth = await authRequest;
    api.setCSRF(state.auth.csrf_token);
    if (state.auth.authenticated) {
      const general = await generalRequest;
      if (general?.general) {
        state.generalSettings = general.general;
        applyLocale(general.general.language);
      }
      state.loading = false;
      state.hydrating = true;
      render();
      await loadCoreData();
      state.hydrating = false;
      render();
      await restoreNavigationState(savedNavigation);
      navigationRestoring = false;
      persistNavigationState();
      openGlobalEvents();
      await ensureViewData(state.view);
      // View-specific hydration can render again after restoreNavigationState.
      // Make the final startup render authoritative for a restored conversation.
      if (state.selectedTask) requestConversationBottom();
      prefetchSecondaryData();
    }
  } catch (error) {
    state.error = error instanceof Error ? error.message : String(error);
  } finally {
    navigationRestoring = false;
    state.loading = false;
    if (state.selectedTask) requestConversationBottom();
    render();
    if (state.auth?.authenticated && !perfMetrics.firstScreenMs) {
      perfMetrics.firstScreenMs = Math.round(performance.now() - bootStartedAt);
      if (perfEnabled) console.info(`[aha-perf] first screen ${perfMetrics.firstScreenMs}ms`);
    }
  }
}

async function loadAll(): Promise<void> {
  await Promise.all([loadCoreData(true), ensureViewData(state.view, true)]);
}

interface ModelDetectionSession {
  providerID: string;
  authStyle: string;
  catalog: DetectedModel[];
  results: Map<string, DetectedModel>;
  detecting: Set<string>;
  probeErrors: Map<string, string>;
  protocolErrors: Map<string, Record<string, string>>;
  selected: Set<string>;
  anthropicBaseURL: string;
  loading: boolean;
  error: string;
  abort?: AbortController;
  listeners?: AbortController;
  button: HTMLElement;
  originalButtonHTML: string;
}

const MODEL_CATALOG_TIMEOUT_MS = 20_000;
const MODEL_PROBE_CONCURRENCY = 4;

let activeModelDetection: ModelDetectionSession | null = null;
let modelAddRunning = false;
let modelAddCancelled = false;

function modelWireLabel(wire: string): string {
  return MODEL_WIRE_OPTIONS.find(option => option.value === wire)?.label || wire;
}

function modelWireSupported(model: DetectedModel | undefined, wire: string): boolean {
  const status = model?.capabilities?.[wire];
  return status === "supported" || status === "rate_limited";
}

// Probes only report protocol capabilities. Keep the catalog's token limits so
// a probed row and the added model still carry the context window.
function mergeProbedModel(catalogModel: DetectedModel | undefined, probed: DetectedModel): DetectedModel {
  if (!catalogModel) return probed;
  return {
    ...catalogModel,
    ...probed,
    max_input_tokens: probed.max_input_tokens || catalogModel.max_input_tokens || 0,
    max_output_tokens: probed.max_output_tokens || catalogModel.max_output_tokens || 0,
  };
}

function detectedModelRow(session: ModelDetectionSession, item: DetectedModel, ready: boolean): string {
  const detecting = session.detecting.has(item.id);
  const probeError = session.probeErrors.get(item.id) || "";
  const protocolErrors = session.protocolErrors.get(item.id) || {};
  const selected = session.selected.has(item.id);
  return `<div class="detected-item${ready ? "" : " pending"}${selected ? " selected" : ""}" data-model="${escapeHTML(item.id)}"><input type="checkbox" class="detected-model" value="${escapeHTML(item.id)}" ${selected ? "checked" : ""}><div class="detected-model-body"><strong>${escapeHTML(item.id)}</strong>${item.max_input_tokens ? `<small>context ${Math.round(item.max_input_tokens / 1000)}K</small>` : ""}${ready ? `<span class="proto-badges">${protoBadges(item)}</span>${renderProtocolProbeStatus(item.capabilities, protocolErrors)}` : `<span class="proto pending">未检测</span>`}${probeError ? `<small class="bad">${escapeHTML(probeError)}</small>` : ""}</div><button type="button" data-probe-model="${escapeHTML(item.id)}" ${detecting ? "disabled" : ""}>${detecting ? "检测中…" : ready ? "重新检测" : "检测"}</button></div>`;
}

function applyModelDetectionFilter(root: HTMLElement, query: string): void {
  const tokens = query.trim().toLowerCase().split(/\s+/).filter(Boolean);
  let visible = 0;
  let total = 0;
  root.querySelectorAll<HTMLElement>(".detected-item").forEach(row => {
    total++;
    const haystack = String(row.dataset.model || "").toLowerCase();
    const match = tokens.every(token => haystack.includes(token));
    row.style.display = match ? "" : "none";
    if (match) visible++;
  });
  const count = root.querySelector<HTMLElement>("#detect-match-count");
  if (count) count.textContent = `匹配 ${visible}/${total}`;
}

function modelDetectionStatusText(session: ModelDetectionSession): string {
  if (session.loading) return "正在获取模型列表";
  if (session.error) return `获取模型列表失败：${session.error}`;
  const inspected = session.results.size;
  return `模型列表已获取 · ${inspected}/${session.catalog.length} 已检测${session.authStyle ? `（认证：${session.authStyle}）` : ""}`;
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
  const selected = root.querySelector<HTMLElement>("#detect-selected-count");
  if (selected) selected.textContent = `已选 ${session.selected.size}`;
  const add = document.querySelector<HTMLButtonElement>("#open-model-add");
  if (add) {
    add.disabled = session.selected.size === 0 || modelAddRunning;
    add.textContent = `添加 (${session.selected.size})`;
  }
}

function updateDetectedModelRow(session: ModelDetectionSession, modelID: string): void {
  const root = document.querySelector<HTMLElement>("#model-detect-results");
  const model = session.results.get(modelID) || session.catalog.find(item => item.id === modelID);
  if (!root || !model) return;
  const row = [...root.querySelectorAll<HTMLElement>(".detected-item")].find(item => item.dataset.model === modelID);
  if (!row) {
    renderModelDetection(session);
    return;
  }
  row.outerHTML = detectedModelRow(session, model, session.results.has(modelID));
  applyModelDetectionFilter(root, root.querySelector<HTMLInputElement>("#detect-search")?.value || "");
  updateModelDetectionProgress(session);
}

function renderModelDetection(session: ModelDetectionSession): void {
  const root = document.querySelector<HTMLElement>("#model-detect-results");
  if (!root) return;
  const rows = session.catalog.map(item => detectedModelRow(session, session.results.get(item.id) || item, session.results.has(item.id))).join("");
  const empty = session.loading ? "正在获取模型列表…" : session.error ? "获取模型列表失败" : "没有检测到模型";
  replaceRegionHTML(root, `<div class="detect-status" id="model-detect-progress">${escapeHTML(modelDetectionStatusText(session))}</div><div class="detect-status" id="model-detect-anthropic" ${session.anthropicBaseURL ? "" : "hidden"}>检测到 Anthropic 端点：<code>${escapeHTML(session.anthropicBaseURL)}</code></div><div class="detect-toolbar"><input type="search" id="detect-search" placeholder="搜索模型名称（支持空格分词）..."><button type="button" id="detect-select-all">全选</button><button type="button" id="detect-select-none">全不选</button><span class="detect-count" id="detect-match-count"></span><span class="detect-count" id="detect-selected-count">已选 ${session.selected.size}</span></div><div class="detected-list" data-ui-key="model-detection-list">${rows || `<div class="empty">${empty}</div>`}</div>`);
  applyModelDetectionFilter(root, root.querySelector<HTMLInputElement>("#detect-search")?.value || "");
}

function syncModelAddWireOptions(): void {
  const backend = String(document.querySelector<HTMLSelectElement>("#model-add-backend")?.value || "dsh");
  const select = document.querySelector<HTMLSelectElement>("#model-add-wire");
  if (!select) return;
  const current = select.value;
  const options = MODEL_WIRE_OPTIONS.filter(option => option.backends.includes(backend));
  select.innerHTML = options.map(option => `<option value="${option.value}">${option.label}</option>`).join("");
  if (options.some(option => option.value === current)) select.value = current;
}

function openModelAddDialog(): void {
  const session = activeModelDetection;
  const dialog = document.querySelector<HTMLDialogElement>("#model-add-dialog");
  if (!session || !dialog || !session.selected.size) return;
  modelAddCancelled = false;
  const backend = document.querySelector<HTMLSelectElement>("#model-add-backend");
  if (backend) backend.value = "dsh";
  syncModelAddWireOptions();
  const summary = document.querySelector<HTMLElement>("#model-add-summary");
  if (summary) summary.textContent = `已选 ${session.selected.size} 个模型`;
  const confirm = document.querySelector<HTMLButtonElement>("#confirm-model-add");
  if (confirm) {
    confirm.disabled = false;
    confirm.textContent = "添加";
  }
  if (!dialog.open) dialog.showModal();
  updateModelDetectionProgress(session);
}

async function probeSelectedModels(session: ModelDetectionSession, ids: string[], onProgress: (done: number, total: number) => void): Promise<void> {
  const pending = ids.filter(id => !session.results.has(id));
  if (!pending.length) return;
  let next = 0;
  let finished = 0;
  const worker = async () => {
    while (true) {
      const index = next++;
      if (index >= pending.length) return;
      const modelID = pending[index];
      session.detecting.add(modelID);
      session.probeErrors.delete(modelID);
      session.protocolErrors.delete(modelID);
      updateDetectedModelRow(session, modelID);
      try {
        const response = await api.probeModel(session.providerID, modelID, session.authStyle);
        session.results.set(modelID, mergeProbedModel(session.catalog.find(item => item.id === modelID), response.model));
        if (response.anthropic_base_url) session.anthropicBaseURL = response.anthropic_base_url;
        const errors = response.protocol_errors || {};
        if (Object.keys(errors).length) session.protocolErrors.set(modelID, errors);
        else session.protocolErrors.delete(modelID);
      } catch (error) {
        session.probeErrors.set(modelID, error instanceof Error ? error.message : String(error));
      } finally {
        session.detecting.delete(modelID);
      }
      finished++;
      onProgress(finished, pending.length);
      updateDetectedModelRow(session, modelID);
    }
  };
  await Promise.all(Array.from({length: Math.min(MODEL_PROBE_CONCURRENCY, pending.length)}, () => worker()));
}

async function confirmModelAdd(): Promise<void> {
  const session = activeModelDetection;
  const dialog = document.querySelector<HTMLDialogElement>("#model-add-dialog");
  if (!session || !dialog?.open || modelAddRunning) return;
  const backend = String(document.querySelector<HTMLSelectElement>("#model-add-backend")?.value || "dsh");
  const wire = String(document.querySelector<HTMLSelectElement>("#model-add-wire")?.value || "responses");
  const confirm = document.querySelector<HTMLButtonElement>("#confirm-model-add");
  const summary = document.querySelector<HTMLElement>("#model-add-summary");
  const ids = [...session.selected];
  if (!ids.length) return;
  modelAddRunning = true;
  modelAddCancelled = false;
  if (confirm) {
    confirm.disabled = true;
    confirm.textContent = "检测中…";
  }
  updateModelDetectionProgress(session);
  try {
    const needProbe = ids.filter(id => !session.results.has(id));
    if (needProbe.length) {
      if (summary) summary.textContent = `正在检测所选模型 0/${needProbe.length}…`;
      await probeSelectedModels(session, ids, (done, total) => {
        if (summary) summary.textContent = `正在检测所选模型 ${done}/${total}…`;
      });
    }
    if (modelAddCancelled || activeModelDetection !== session) return;
    const supported = ids.filter(id => modelWireSupported(session.results.get(id), wire));
    const skipped = ids.length - supported.length;
    if (!supported.length) {
      if (summary) summary.textContent = `所选模型都不支持 ${modelWireLabel(wire)}，未添加`;
      return;
    }
    if (confirm) confirm.textContent = "添加中…";
    if (summary) summary.textContent = `将添加 ${supported.length} 个模型${skipped ? `，跳过 ${skipped} 个不支持该协议` : ""}`;
    const models = supported.map(id => {
      const model = session.results.get(id) || session.catalog.find(item => item.id === id);
      return {id, wire_apis: [wire], max_input_tokens: model?.max_input_tokens || 0, max_output_tokens: model?.max_output_tokens || 0};
    });
    const result = await api.addModels({provider_id: session.providerID, backend, models, anthropic_base_url: session.anthropicBaseURL});
    dialog.close();
    document.querySelector<HTMLDialogElement>("#model-dialog")?.close();
    setMessage("notice", `已添加 ${(result.models || []).length} 个模型${skipped ? `，跳过 ${skipped} 个不支持该协议` : ""}${result.skipped ? `，${result.skipped} 个已存在` : ""}`);
    await ensureViewData("models", true);
    render();
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    if (summary) summary.textContent = `添加失败：${message}`;
    setMessage("error", `添加模型失败：${message}`);
  } finally {
    modelAddRunning = false;
    if (confirm) {
      confirm.disabled = false;
      confirm.textContent = "添加";
    }
    if (activeModelDetection === session) updateModelDetectionProgress(session);
  }
}

async function startModelDetection(providerID: string, button: HTMLElement, results: HTMLElement): Promise<void> {
  const previous = activeModelDetection;
  if (previous) {
    if (previous.loading) return;
    previous.abort?.abort();
    previous.listeners?.abort();
  }
  const originalButtonHTML = button.innerHTML;
  button.disabled = true;
  button.innerHTML = `${icon("spinner", true)}<span>获取中</span>`;
  results.innerHTML = `<div class="detect-status">正在获取模型列表…</div>`;
  const abort = new AbortController();
  const listeners = new AbortController();
  const session: ModelDetectionSession = {
    providerID, authStyle: "", catalog: [], results: new Map(), detecting: new Set(),
    probeErrors: new Map(), protocolErrors: new Map(), selected: new Set(),
    anthropicBaseURL: "", loading: true, error: "", abort, listeners, button, originalButtonHTML,
  };
  activeModelDetection = session;
  renderModelDetection(session);
  results.addEventListener("input", event => {
    if (!(event.target instanceof HTMLInputElement) || event.target.id !== "detect-search") return;
    applyModelDetectionFilter(results, event.target.value);
  }, {signal: listeners.signal});
  results.addEventListener("change", event => {
    if (!(event.target instanceof HTMLInputElement) || !event.target.classList.contains("detected-model")) return;
    const box = event.target;
    if (box.checked) session.selected.add(box.value);
    else session.selected.delete(box.value);
    box.closest(".detected-item")?.classList.toggle("selected", box.checked);
    updateModelDetectionProgress(session);
  }, {signal: listeners.signal});
  results.addEventListener("click", event => {
    const target = event.target as HTMLElement;
    const probeButton = target.closest<HTMLElement>("[data-probe-model]");
    if (probeButton) {
      const modelID = probeButton.dataset.probeModel || "";
      if (!modelID || session.detecting.has(modelID)) return;
      session.detecting.add(modelID);
      session.probeErrors.delete(modelID);
      session.protocolErrors.delete(modelID);
      updateDetectedModelRow(session, modelID);
      void api.probeModel(session.providerID, modelID, session.authStyle).then(response => {
        session.results.set(modelID, mergeProbedModel(session.catalog.find(item => item.id === modelID), response.model));
        if (response.anthropic_base_url) session.anthropicBaseURL = response.anthropic_base_url;
        const errors = response.protocol_errors || {};
        if (Object.keys(errors).length) session.protocolErrors.set(modelID, errors);
        else session.protocolErrors.delete(modelID);
      }).catch(error => {
        session.probeErrors.set(modelID, error instanceof Error ? error.message : String(error));
      }).finally(() => {
        session.detecting.delete(modelID);
        updateDetectedModelRow(session, modelID);
      });
      return;
    }
    if (target.closest("#detect-select-all")) {
      [...results.querySelectorAll<HTMLElement>(".detected-item")].filter(row => row.style.display !== "none").forEach(row => {
        const box = row.querySelector<HTMLInputElement>(".detected-model");
        if (!box) return;
        box.checked = true;
        row.classList.add("selected");
        session.selected.add(box.value);
      });
      updateModelDetectionProgress(session);
      return;
    }
    if (target.closest("#detect-select-none")) {
      results.querySelectorAll<HTMLInputElement>(".detected-model").forEach(box => {
        box.checked = false;
        box.closest(".detected-item")?.classList.remove("selected");
        session.selected.delete(box.value);
      });
      updateModelDetectionProgress(session);
      return;
    }
    if (target instanceof HTMLInputElement) return;
    const row = target.closest<HTMLElement>(".detected-item");
    if (!row) return;
    const box = row.querySelector<HTMLInputElement>(".detected-model");
    if (!box) return;
    box.checked = !box.checked;
    box.dispatchEvent(new Event("change", {bubbles: true}));
  }, {signal: listeners.signal});
  const timeout = window.setTimeout(() => abort.abort(), MODEL_CATALOG_TIMEOUT_MS);
  try {
    const response = await api.detectModels({provider_id: providerID}, {signal: abort.signal});
    session.catalog = response.models || [];
    session.authStyle = response.auth_style || "";
    session.anthropicBaseURL = response.anthropic_base_url || "";
    if (!session.catalog.length) throw new Error("Provider 没有返回任何模型");
  } catch (error) {
    session.error = abort.signal.aborted
      ? `请求 ${MODEL_CATALOG_TIMEOUT_MS / 1000} 秒内没有完成，请检查是否有太多 AHA 页面占用了浏览器连接`
      : error instanceof Error ? error.message : String(error);
    if (activeModelDetection === session) setMessage("error", `获取模型列表失败：${session.error}`);
  } finally {
    window.clearTimeout(timeout);
    session.loading = false;
    session.abort = undefined;
    button.disabled = false;
    button.innerHTML = originalButtonHTML;
  }
  if (activeModelDetection === session) renderModelDetection(session);
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
      const result = await api.tasks("", {limit: taskRefreshLimit(), order: taskListOrder});
      state.tasks = result.tasks || [];
      state.taskFacetCounts = result.counts || null;
      taskLoadMoreNoMatch = false;
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
    const [knowledge, libraries, indexes, pendingProposals] = await Promise.all([
      api.knowledge("", "", "", {limit: initialListPageSize, summary: true, includeProposals: false}),
      api.knowledgeLibraries(),
      api.knowledgeIndexes(),
      api.knowledgeProposals({status: "pending", limit: 0}).catch(() => ({proposals: []})),
    ]);
    state.knowledge = knowledge.knowledge || [];
    state.knowledgeLibraries = libraries.libraries || [];
    state.knowledgeIndexes = indexes.indexes || [];
    state.knowledgeProposals = pendingProposals.proposals || [];
    state.knowledgeReviewSettings = knowledge.review_settings || {auto_approve: false};
    listPages.knowledge = {cursor: knowledge.next_cursor || "", hasMore: Boolean(knowledge.has_more), loadingMore: false};
  });
}

// The Phase 2 page is opt-in on the server: its data routes answer 404 while
// the Phase 3 switch is off. Probe once and keep the legacy workspace path
// (including all of its API calls) byte-for-byte unchanged when it is off.
// The token invalidates a probe that was already in flight when the Owner
// toggled the switch, so a slow stale answer cannot overwrite the new state.
let knowledgeVaultProbe: Promise<boolean> | null = null;
let knowledgeVaultProbeToken = 0;
async function detectKnowledgeVault(force = false): Promise<boolean> {
  if (!force && state.knowledgeVaultEnabled !== null) return state.knowledgeVaultEnabled;
  if (knowledgeVaultProbe) return knowledgeVaultProbe;
  const token = knowledgeVaultProbeToken;
  knowledgeVaultProbe = api.knowledgeVaultLibraries()
    .then(result => {
      if (token !== knowledgeVaultProbeToken) return state.knowledgeVaultEnabled === true;
      state.knowledgeVaultEnabled = result?.enabled === true;
      return state.knowledgeVaultEnabled;
    })
    .catch(() => {
      if (token === knowledgeVaultProbeToken) state.knowledgeVaultEnabled = false;
      return state.knowledgeVaultEnabled === true;
    })
    .finally(() => {
      if (token === knowledgeVaultProbeToken) knowledgeVaultProbe = null;
    });
  return knowledgeVaultProbe;
}

function applyKnowledgeVaultSettings(settings: KnowledgeVaultSettings): void {
  knowledgeVaultProbeToken++;
  knowledgeVaultProbe = null;
  state.knowledgeVaultEnabled = settings.enabled;
  state.vaultLibraries = [];
  // Libraries, tree, documents and search all cached data from before the
  // switch/rebuild; drop it so the next visit fetches the current generation.
  knowledgeVaultPage.loaded()?.resetKnowledgeVaultPage();
}

async function loadKnowledgeViewData(force = true): Promise<void> {
  // The vault page loads its own libraries and renders the blocked state when
  // the switch is off. Keep the shared probe warm for the channel/task source
  // pickers, but never fetch the legacy workspace lists for this route.
  await detectKnowledgeVault(force);
}

// The channels page used to load the legacy knowledge lists unconditionally,
// which kept requesting the old library even after the owner switched to the
// vault. Keep the legacy calls byte-for-byte when the vault is off, and load
// only vault libraries when it is on. A vault probe that succeeds but cannot
// list libraries leaves the list empty; the dialog then shows its notice
// instead of falling back to legacy sources.
async function loadChannelKnowledgeSources(force = true): Promise<void> {
  if (await detectKnowledgeVault(false)) {
    state.vaultLibraries = [];
    try {
      const result = await api.knowledgeVaultLibraries();
      state.vaultLibraries = result.libraries || [];
    } catch {
      // Hard failure stays visible as the empty-source notice.
    }
    return;
  }
  await loadKnowledgeData(force);
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
      const result = await api.tasks("", {limit: initialListPageSize, cursor: page.cursor, order: taskListOrder}).catch(error => {
        if ((error as {code?: string})?.code !== "invalid_cursor") throw error;
        // The cursor no longer matches the list order, so continuing it would
        // skip or repeat Tasks. Start the list over from the first page.
        page.cursor = "";
        page.hasMore = false;
        return null;
      });
      if (!result) {
        await loadCoreData(true);
        return;
      }
      const matchedBefore = filteredTasks().length;
      state.tasks = mergeByID(state.tasks, result.tasks || []);
      if (result.counts) state.taskFacetCounts = result.counts;
      taskLoadMoreNoMatch = filteredTasks().length === matchedBefore;
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
    jobs.push(loadResource("models", force, async () => {
      if (force) forgetAllModelHealth();
      state.models = (await api.models()).models || [];
      // Drop health results and selections for models that no longer exist so a
      // stale badge or batch action cannot outlive its model record.
      const ids = new Set(state.models.map(item => item.id));
      for (const id of Object.keys(state.modelHealth)) {
        if (!ids.has(id)) forgetModelHealth(id);
      }
      pruneModelSelection(state.modelSelected, state.models);
    }));
    jobs.push(loadResource("accounts", force, async () => { state.codexAccounts = (await api.codexAccounts()).accounts || []; }));
  }
  if (view === "models") jobs.push(loadResource("env", force, async () => { state.envGroups = (await api.envGroups()).env_groups || []; }));
  if (view === "resources") {
    jobs.push(loadResource("hardware", force, async () => {
      state.hardwareResources = (await api.hardwareResources()).resources || [];
    }));
    jobs.push(loadResource("mail-accounts", force, async () => {
      state.mailAccounts = (await api.mailAccounts()).accounts || [];
    }));
    jobs.push(loadResource("mail-templates", force, async () => {
      state.mailTemplates = (await api.mailTemplates()).templates || [];
    }));
    jobs.push(loadResource("mail-messages", force, async () => {
      state.mailMessages = (await api.mailMessages({status: state.mailFilterStatus, account_id: state.mailFilterAccount, limit: 100})).messages || [];
    }));
  }
  if (view === "tasks") jobs.push(loadResource("skills", force, async () => {
    state.skills = (await api.skills()).skills || [];
    // Vault support is a lazy chunk: the mapping and copy are only needed on
    // the Tasks view, and a disabled vault must leave legacy skills untouched.
    // A stale cached index can miss the chunk, so degrade to legacy skills
    // rather than failing the whole Tasks view.
    try {
      const module = await import("./task_vault_skills.js");
      vaultTaskSkillsModule = module;
      const vault = await module.loadVaultTaskSkills();
      state.vaultSkills = vault.skills;
      state.vaultSkillsOn = vault.available;
      state.vaultSkillsRead = vault.runtimeRead;
      state.vaultSkillsIndex = vault.indexReady;
      state.vaultSkillsBound = vault.boundProjects;
    } catch {
      // A stale cached index can miss the chunk; legacy skills still load.
    }
  }));
  if (view === "tasks" || view === "mcp") jobs.push(loadMCPServers(force));
  if (view === "channels") {
    jobs.push(loadResource("channels", force, async () => {
      const [providers, instances] = await Promise.all([api.channelProviders(), api.channelInstances()]);
      state.channelProviders = providers.providers || [];
      state.channelInstances = instances.instances || [];
    }));
    jobs.push(loadChannelKnowledgeSources(force));
  }
  if (view === "knowledge") jobs.push(loadKnowledgeViewData(force));
  if (view === "proxy") jobs.push(loadResource("proxy", force, async () => {
    const result = await api.proxySettings();
    if (result.proxy) state.proxySettings = result.proxy;
    if (result.managed) state.managedProxy = result.managed;
  }));
  // Agent API settings are rendered by the "other" settings page as well as
  // the advanced page. Keying the load off resourcesForView keeps a direct
  // refresh of #other from rendering the default (off) state before the real
  // stored value is fetched.
  if (resourcesForView(view).includes("advanced")) jobs.push(loadResource("advanced", force, async () => {
    const [security, agentAPI, backend, network] = await Promise.all([api.securitySettings(), api.agentAPISettings(), api.backendSettings(), api.networkSettings()]);
    if (security.security) state.securitySettings = security.security;
    if (agentAPI.agent_api) state.agentAPISettings = agentAPI.agent_api;
    if (backend.backend) state.backendSettings = backend.backend;
    if (network.network) state.networkSettings = network.network;
  }));
  if (view === "other") jobs.push(refreshKnowledgeVaultSettings());
  if (view === "data") jobs.push(loadDataInventory(force), loadDatabaseStats(force));
  if (view === "sync") jobs.push(loadResource("sync", force, async () => {
    const [settings, status, conflicts] = await Promise.all([api.syncSettings(), api.syncStatus(), api.syncConflicts()]);
    state.syncSettings = settings.sync;
    state.syncSettingsReady = true;
    state.syncState = status.state;
    state.syncPending = status.pending || 0;
    state.syncRun = status.run || state.syncRun;
    state.syncConflicts = conflicts.conflicts || [];
    state.syncNextRunAt = status.next_run_at || "";
    if (state.view === "sync") render();
  }));
  if (view === "prompts") jobs.push(loadResource("prompts", force, loadPromptCatalog));
  await Promise.all(jobs);
}

function mailResourcesData(): MailResourcesData {
  const keys: ResourceKey[] = ["mail-accounts", "mail-templates", "mail-messages"];
  const error = state.mailResourcesError || keys.map(key => resourceStates[key].error).find(Boolean) || "";
  const loading = state.mailResourcesLoading || keys.some(key => resourceStates[key].loading)
    || (keys.every(key => !resourceStates[key].loaded) && !error);
  return {
    subTab: state.mailSubTab,
    accounts: state.mailAccounts,
    templates: state.mailTemplates,
    messages: state.mailMessages,
    projects: state.projects,
    filterStatus: state.mailFilterStatus,
    filterAccount: state.mailFilterAccount,
    showArchived: state.mailShowArchived,
    loading,
    error,
  };
}

async function reloadMailResources(options: {status?: string; account?: string} = {}): Promise<void> {
  if (options.status !== undefined) state.mailFilterStatus = options.status;
  if (options.account !== undefined) state.mailFilterAccount = options.account;
  state.mailResourcesLoading = true;
  state.mailResourcesError = "";
  render();
  try {
    await Promise.all([
      loadResource("mail-accounts", true, async () => {
        state.mailAccounts = (await api.mailAccounts()).accounts || [];
      }),
      loadResource("mail-templates", true, async () => {
        state.mailTemplates = (await api.mailTemplates()).templates || [];
      }),
      loadResource("mail-messages", true, async () => {
        state.mailMessages = (await api.mailMessages({status: state.mailFilterStatus, account_id: state.mailFilterAccount, limit: 100})).messages || [];
      }),
    ]);
  } catch (error) {
    state.mailResourcesError = error instanceof Error ? error.message : String(error);
  } finally {
    state.mailResourcesLoading = false;
    render();
  }
}

async function reloadHardwareResources(): Promise<void> {
  await loadResource("hardware", true, async () => {
    state.hardwareResources = (await api.hardwareResources()).resources || [];
  });
}

function loadDataInventory(force = false): Promise<void> {
  return loadResource("data", force, async () => {
    state.dataInventoryLoading = true;
    state.dataInventoryError = "";
    try {
      const result = await api.dataInventory();
      state.dataInventory = result.inventory;
    } catch (error) {
      state.dataInventoryError = error instanceof Error ? error.message : String(error);
      // Rethrow so the loader does not mark a failed inventory as loaded; an
      // idle prefetch may retry it later.
      throw error;
    } finally {
      state.dataInventoryLoading = false;
    }
  });
}

// Database statistics load separately from the file inventory so a slow or
// failing database read never hides the directory sizes.
let databaseStatsRequest: Promise<void> | null = null;
function loadDatabaseStats(force = false): Promise<void> {
  const view = state.dataRetention;
  if (!force && view.database) return Promise.resolve();
  if (databaseStatsRequest) return databaseStatsRequest;
  view.loading = true;
  view.error = "";
  databaseStatsRequest = api.databaseStats(force).then(result => {
    view.database = result.database;
  }).catch((error: unknown) => {
    view.error = error instanceof Error ? error.message : String(error);
  }).finally(() => {
    view.loading = false;
    databaseStatsRequest = null;
  });
  return databaseStatsRequest;
}

function cancelSecondaryPrefetch(): void {
  if (secondaryPrefetchHandle !== null) {
    const cancelIdle = (window as unknown as {cancelIdleCallback?: (handle: number) => void}).cancelIdleCallback;
    if (cancelIdle) cancelIdle(secondaryPrefetchHandle);
    secondaryPrefetchHandle = null;
  }
  if (secondaryPrefetchTimer) {
    window.clearTimeout(secondaryPrefetchTimer);
    secondaryPrefetchTimer = 0;
  }
}

// Warm only the current view once the page is idle. The previous version chained
// models -> channels -> knowledge 250 ms after paint, which fired ~10 requests
// into the exact window where a user opens a Task. Here a skip is final: once the
// user has interacted or opened a Task there is no background warm-up at all, and
// loaded resources are deduplicated by loadResource().
function prefetchSecondaryData(): void {
  cancelSecondaryPrefetch();
  if (secondaryPrefetchDone || userInteracted) return;
  const run = (): void => {
    secondaryPrefetchHandle = null;
    secondaryPrefetchTimer = 0;
    if (secondaryPrefetchDone) return;
    if (!state.auth?.authenticated || userInteracted || state.selectedTask || openingTaskID) {
      secondaryPrefetchDone = true;
      return;
    }
    // A hidden tab stays idle-worthy for later; the visibility handler retries.
    if (document.hidden) return;
    secondaryPrefetchDone = true;
    perfMetrics.prefetchRuns++;
    // ensureViewData issues all of its loader calls synchronously, so the count
    // window only needs to cover the call itself; counting in its finally would
    // also attribute unrelated loads that start while it is in flight.
    prefetchRequestTracking++;
    try {
      void ensureViewData(state.view);
    } finally {
      prefetchRequestTracking = Math.max(0, prefetchRequestTracking - 1);
    }
  };
  const requestIdle = (window as unknown as {requestIdleCallback?: (callback: () => void, options?: {timeout: number}) => number}).requestIdleCallback;
  if (requestIdle) {
    secondaryPrefetchHandle = requestIdle(run, {timeout: 2500});
  } else {
    // Short fallback for browsers/WebViews without requestIdleCallback.
    secondaryPrefetchTimer = window.setTimeout(run, 1200);
  }
}

function noteUserInteraction(): void {
  if (userInteracted) return;
  userInteracted = true;
  cancelSecondaryPrefetch();
}
window.addEventListener("pointerdown", noteUserInteraction, {capture: true, passive: true});
window.addEventListener("keydown", noteUserInteraction, {capture: true});
function persistNavigationState(): void {
  if (navigationRestoring) return;
  saveNavigationSnapshot({
    view: state.view, projectID: state.selectedProject?.id, taskID: state.selectedTask?.task.id,
    project: state.selectedProject || undefined, task: state.selectedTask?.task,
    agentID: state.selectedTaskAgent,
    draft: state.taskDraft, drafts: state.taskDrafts,
  });
}
async function restoreNavigationState(saved = loadNavigationSnapshot()): Promise<void> {
  const savedView = viewFromName(saved.view);
  if (!initialHashRoute && savedView) state.view = savedView;
  if (initialHashRoute) return;
  const savedProject = state.projects.find(project => project.id === saved.projectID) || saved.project;
  state.selectedProject = savedProject || null;
  if (savedProject && !state.projects.some(project => project.id === savedProject.id)) {
    state.projects.push(savedProject);
  }
  const savedTask = state.tasks.find(task => task.id === saved.taskID) || saved.task;
  if (!saved.taskID || !savedTask) return;
  if (!state.tasks.some(task => task.id === savedTask.id)) state.tasks.push(savedTask);
  state.view = "tasks"; state.selectedProject = null;
  await openTask(saved.taskID);
  if (saved.agentID && saved.agentID !== "main" && state.selectedTask?.agents.some(agent => agent.agent_id === saved.agentID)) {
    await selectTaskAgent(saved.agentID);
  }
  state.taskDrafts = saved.drafts || {};
  state.taskDraft = saved.draft || state.taskDrafts[state.selectedTaskAgent] || "";
}
function syncVisualViewportHeight(): void {
  const viewport = window.visualViewport;
  const height = Math.round(viewport?.height || window.innerHeight);
  const offsetTop = Math.round(viewport?.offsetTop || 0);
  const layoutHeight = Math.round(window.innerHeight || height);
  const bottomInset = Math.max(0, layoutHeight - offsetTop - height);
  document.documentElement.style.setProperty("--visual-viewport-height", `${height}px`);
  document.documentElement.style.setProperty("--visual-viewport-offset-top", `${offsetTop}px`);
  document.documentElement.style.setProperty("--visual-viewport-bottom-inset", `${bottomInset}px`);
}

function authErrorMessage(error: unknown, fallback: string): string {
  const typed = error as Error & {code?: string};
  if (typed.code === "recovery_failed") return t("login.recoveryFailed");
  if (typed.code === "invalid_current_password") return t("login.invalidCurrentPassword");
  if (typed.code === "invalid_new_password") return t("login.invalidNewPassword");
  if (typed.code === "auth_rate_limited") return t("login.rateLimited");
  return error instanceof Error ? error.message : fallback;
}

function persistTaskToolLayout(): void {
  try {
    localStorage.setItem(taskToolLayoutKey, JSON.stringify({mode: state.taskToolMode, width: state.taskToolWidth, pinnedTool: state.taskToolPinned}));
  } catch {
    // Layout persistence is best-effort in restricted WebViews.
  }
}

function persistSidebarLayout(): void {
  try {
    localStorage.setItem(sidebarLayoutKey, JSON.stringify({
      width: state.sidebarWidth,
      memoCollapsed: state.sidebarMemoCollapsed,
      memoColor: state.sidebarMemoColor,
    }));
  } catch {
    // Layout persistence is best-effort in restricted WebViews.
  }
}

function persistSidebarMemo(): void {
  if (sidebarMemoSaveTimer) {
    window.clearTimeout(sidebarMemoSaveTimer);
    sidebarMemoSaveTimer = 0;
  }
  try {
    localStorage.setItem(sidebarMemoKey, JSON.stringify(state.sidebarMemoItems));
  } catch {
    // Memo persistence is best-effort in restricted WebViews.
  }
}

function scheduleSidebarMemoSave(): void {
  if (sidebarMemoSaveTimer) window.clearTimeout(sidebarMemoSaveTimer);
  sidebarMemoSaveTimer = window.setTimeout(() => {
    sidebarMemoSaveTimer = 0;
    persistSidebarMemo();
    const status = document.querySelector<HTMLElement>("#sidebar-memo-status");
    if (status) status.textContent = "已保存";
  }, 500);
}

// Only a memo that actually holds text can become a Task, and a finished one is
// already dealt with. The button is always rendered and hidden by CSS off the
// row's own empty/done classes, which the input and checkbox handlers already
// maintain, so no re-render is needed as the owner types.
function sidebarMemoCanCreateTask(item: SidebarMemoItem): boolean {
  return Boolean(item.text.trim()) && !item.done;
}

function sidebarMemoItemsHTML(): string {
  const items = state.sidebarMemoItems.length ? state.sidebarMemoItems : [newSidebarMemoItem()];
  return `<div class="sidebar-memo-list">${items.map(item => `<div class="sidebar-memo-item ${item.done ? "done" : ""} ${item.text ? "" : "sidebar-memo-empty"}" data-sidebar-memo-item="${escapeHTML(item.id)}">
    <input type="checkbox" data-sidebar-memo-done="${escapeHTML(item.id)}" ${item.done ? "checked" : ""} aria-label="标记完成">
    <button type="button" class="sidebar-memo-preview" data-sidebar-memo-edit="${escapeHTML(item.id)}" aria-label="编辑备忘"><span class="sidebar-memo-preview-text">${escapeHTML(item.text || t("sidebar.notesPlaceholder"))}</span></button>
    <textarea data-sidebar-memo-text="${escapeHTML(item.id)}" placeholder="${t("sidebar.notesPlaceholder")}" maxlength="${SIDEBAR_MEMO_MAX_LENGTH}" rows="1" aria-label="编辑备忘">${escapeHTML(item.text)}</textarea>
    <div class="sidebar-memo-actions"><button type="button" class="icon-button sidebar-memo-task" data-sidebar-memo-task="${escapeHTML(item.id)}" title="用这条便签创建任务" aria-label="用这条便签创建任务">${icon("tasks")}</button><button type="button" class="icon-button" data-sidebar-memo-remove="${escapeHTML(item.id)}" title="删除事项" aria-label="删除事项">${icon("close")}</button></div>
  </div>`).join("")}</div><button type="button" class="sidebar-memo-add" id="add-sidebar-memo-item">${icon("plus")}${t("sidebar.addItem")}</button>`;
}

// The first line becomes the Task title so the create form is immediately
// submittable; `title` is required and a memo is usually one long paragraph.
function sidebarMemoTaskTitle(text: string): string {
  const firstLine = text.split(/\r?\n/).map(line => line.trim()).find(Boolean) || "";
  return firstLine.length > 60 ? `${firstLine.slice(0, 60)}…` : firstLine;
}

// The create panel only exists in the tasks view and reads that view's loaded
// projects, workspaces, models and skills, so this navigates there first and
// fills the panel once its data has settled. It deliberately does not add a
// second create form: there stays exactly one Task creation path.
async function createTaskFromMemo(memoID: string): Promise<void> {
  const item = state.sidebarMemoItems.find(candidate => candidate.id === memoID);
  const text = item?.text.trim() || "";
  if (!item || !text) return;
  persistSidebarMemo();
  cancelTaskOpen();
  state.view = "tasks";
  state.selectedTask = null;
  clearTaskCardFocusReturn();
  state.selectedProject = null;
  state.dialogProjectID = "";
  closeEvents();
  render();
  await ensureViewData("tasks");
  render();
  const form = document.querySelector<HTMLFormElement>("#task-form");
  const dialog = document.querySelector<HTMLDialogElement>("#task-dialog");
  if (!form || !dialog) return;
  // Populates the workspace list for the selected project and, with it, the
  // runtime fields and the offered skills.
  taskSkillsDirty = false;
  syncTaskWorkspaces();
  const title = form.querySelector<HTMLInputElement>('input[name="title"]');
  const request = form.querySelector<HTMLTextAreaElement>('textarea[name="request"]');
  if (title) title.value = sidebarMemoTaskTitle(text);
  if (request) request.value = text;
  dialog.showModal();
  title?.focus();
  title?.setSelectionRange(title.value.length, title.value.length);
}

function focusSidebarMemoItem(id: string): void {
  window.setTimeout(() => {
    enterSidebarMemoEdit(id);
  }, 0);
}

function fitSidebarMemoTextarea(input: HTMLTextAreaElement): void {
  input.style.height = "22px";
  if (!input.value) return;
  input.style.height = "0px";
  input.style.height = `${Math.max(30, input.scrollHeight)}px`;
}

function fitSidebarMemoTextareas(root: ParentNode = document): void {
  root.querySelectorAll<HTMLTextAreaElement>("[data-sidebar-memo-text]").forEach(fitSidebarMemoTextarea);
}

function enterSidebarMemoEdit(id: string, root: ParentNode = document): void {
  const item = root.querySelector<HTMLElement>(`[data-sidebar-memo-item="${CSS.escape(id)}"]`);
  const input = item?.querySelector<HTMLTextAreaElement>("[data-sidebar-memo-text]");
  if (!item || !input) return;
  item.classList.add("editing");
  fitSidebarMemoTextarea(input);
  input.focus();
  input.setSelectionRange(input.value.length, input.value.length);
}

function bindSidebarMemoItems(root: HTMLElement): void {
  root.querySelectorAll<HTMLInputElement>("[data-sidebar-memo-done]").forEach(input => input.addEventListener("change", () => {
    const item = state.sidebarMemoItems.find(candidate => candidate.id === input.dataset.sidebarMemoDone);
    if (!item) return;
    item.done = input.checked;
    input.closest<HTMLElement>(".sidebar-memo-item")?.classList.toggle("done", item.done);
    persistSidebarMemo();
  }));
  root.querySelectorAll<HTMLTextAreaElement>("[data-sidebar-memo-text]").forEach(input => input.addEventListener("input", () => {
    const item = state.sidebarMemoItems.find(candidate => candidate.id === input.dataset.sidebarMemoText);
    if (!item) return;
    item.text = input.value.slice(0, SIDEBAR_MEMO_MAX_LENGTH);
    const row = input.closest<HTMLElement>(".sidebar-memo-item");
    const preview = row?.querySelector<HTMLElement>(".sidebar-memo-preview-text");
    if (preview) preview.textContent = item.text || t("sidebar.notesPlaceholder");
    row?.classList.toggle("sidebar-memo-empty", !item.text);
    fitSidebarMemoTextarea(input);
    scheduleSidebarMemoSave();
    const status = root.querySelector<HTMLElement>("#sidebar-memo-status");
    if (status) status.textContent = "正在保存";
  }));
  root.querySelectorAll<HTMLTextAreaElement>("[data-sidebar-memo-text]").forEach(input => input.addEventListener("blur", () => {
    persistSidebarMemo();
    const row = input.closest<HTMLElement>(".sidebar-memo-item");
    row?.classList.remove("editing");
    const status = root.querySelector<HTMLElement>("#sidebar-memo-status");
    if (status) status.textContent = "已保存";
  }));
  root.querySelectorAll<HTMLButtonElement>("[data-sidebar-memo-edit]").forEach(button => button.addEventListener("click", () => {
    enterSidebarMemoEdit(button.dataset.sidebarMemoEdit || "", root);
  }));
  root.querySelectorAll<HTMLButtonElement>("[data-sidebar-memo-task]").forEach(button => button.addEventListener("click", () => {
    void createTaskFromMemo(button.dataset.sidebarMemoTask || "");
  }));
  fitSidebarMemoTextareas(root);
  root.querySelectorAll<HTMLButtonElement>("[data-sidebar-memo-remove]").forEach(button => button.addEventListener("click", () => {
    const index = state.sidebarMemoItems.findIndex(item => item.id === button.dataset.sidebarMemoRemove);
    if (index < 0) return;
    if (state.sidebarMemoItems.length === 1) {
      state.sidebarMemoItems[0] = newSidebarMemoItem();
    } else {
      state.sidebarMemoItems.splice(index, 1);
    }
    persistSidebarMemo();
    root.innerHTML = sidebarMemoItemsHTML();
    bindSidebarMemoItems(root);
    focusSidebarMemoItem(state.sidebarMemoItems[Math.min(index, state.sidebarMemoItems.length - 1)].id);
  }));
  root.querySelector<HTMLButtonElement>("#add-sidebar-memo-item")?.addEventListener("click", () => {
    const item = newSidebarMemoItem();
    state.sidebarMemoItems.push(item);
    persistSidebarMemo();
    root.innerHTML = sidebarMemoItemsHTML();
    bindSidebarMemoItems(root);
    focusSidebarMemoItem(item.id);
  });
}

function applySidebarLayout(): void {
  const shellElement = document.querySelector<HTMLElement>(".app-shell");
  if (!shellElement) return;
  shellElement.style.setProperty("--sidebar-width", `${state.sidebarWidth}px`);
  const memo = shellElement.querySelector<HTMLElement>(".sidebar-memo");
  memo?.classList.toggle("collapsed", state.sidebarMemoCollapsed);
  const color = SIDEBAR_MEMO_COLORS[state.sidebarMemoColor] || SIDEBAR_MEMO_COLORS.graphite;
  if (memo) {
    memo.dataset.memoColor = state.sidebarMemoColor;
    memo.style.setProperty("--memo-surface", color.surface);
    memo.style.setProperty("--memo-input", color.input);
    memo.style.setProperty("--memo-border", color.border);
    memo.style.setProperty("--memo-text", color.text);
    memo.style.setProperty("--memo-placeholder", color.placeholder);
  }
  const toggle = shellElement.querySelector<HTMLButtonElement>("#toggle-sidebar-memo");
  if (toggle) {
    toggle.setAttribute("aria-label", state.sidebarMemoCollapsed ? "展开便签" : "折叠便签");
    toggle.title = state.sidebarMemoCollapsed ? "展开便签" : "折叠便签";
    toggle.innerHTML = icon(state.sidebarMemoCollapsed ? "chevron-up" : "chevron-down");
  }
  shellElement.querySelector<HTMLElement>("#sidebar-resizer")?.setAttribute("aria-valuenow", String(state.sidebarWidth));
}

function resizeSidebar(clientX: number): void {
  const availableMax = Math.max(SIDEBAR_MIN_WIDTH, Math.min(SIDEBAR_MAX_WIDTH, window.innerWidth - 420));
  state.sidebarWidth = Math.round(Math.min(availableMax, Math.max(SIDEBAR_MIN_WIDTH, clientX)));
  applySidebarLayout();
}

function adjustSidebarWidth(delta: number): void {
  const availableMax = Math.max(SIDEBAR_MIN_WIDTH, Math.min(SIDEBAR_MAX_WIDTH, window.innerWidth - 420));
  state.sidebarWidth = Math.round(Math.min(availableMax, Math.max(SIDEBAR_MIN_WIDTH, state.sidebarWidth + delta)));
}

function bindSidebarControls(): void {
  const shellElement = document.querySelector<HTMLElement>(".app-shell");
  const resizer = shellElement?.querySelector<HTMLElement>("#sidebar-resizer");
  const memoToggle = shellElement?.querySelector<HTMLButtonElement>("#toggle-sidebar-memo");
  const memoList = shellElement?.querySelector<HTMLElement>(".sidebar-memo-body");
  applySidebarLayout();
  sidebarMemoResizeObserver?.disconnect();
  sidebarMemoResizeObserver = null;
  if (memoList && typeof ResizeObserver !== "undefined") {
    sidebarMemoResizeObserver = new ResizeObserver(() => fitSidebarMemoTextareas(memoList));
    const sidebar = shellElement?.querySelector<HTMLElement>(".sidebar");
    if (sidebar) sidebarMemoResizeObserver.observe(sidebar);
  }
  memoToggle?.addEventListener("click", () => {
    state.sidebarMemoCollapsed = !state.sidebarMemoCollapsed;
    persistSidebarLayout();
    applySidebarLayout();
  });
  if (memoList) bindSidebarMemoItems(memoList);
  shellElement.querySelectorAll<HTMLButtonElement>("[data-sidebar-memo-color]").forEach(button => {
    button.addEventListener("click", () => {
      const color = normalizeSidebarMemoColor(button.dataset.sidebarMemoColor);
      state.sidebarMemoColor = color;
      persistSidebarLayout();
      applySidebarLayout();
    });
  });
  if (!resizer) return;
  resizer.setAttribute("aria-valuenow", String(state.sidebarWidth));
  resizer.addEventListener("dblclick", () => {
    state.sidebarWidth = SIDEBAR_DEFAULT_WIDTH;
    persistSidebarLayout();
    applySidebarLayout();
  });
  resizer.addEventListener("keydown", event => {
    if (event.key !== "ArrowLeft" && event.key !== "ArrowRight" && event.key !== "Home") return;
    event.preventDefault();
    state.sidebarWidth = event.key === "Home" ? SIDEBAR_DEFAULT_WIDTH : state.sidebarWidth;
    if (event.key !== "Home") adjustSidebarWidth(event.key === "ArrowRight" ? 8 : -8);
    persistSidebarLayout();
    applySidebarLayout();
  });
  resizer.addEventListener("pointerdown", event => {
    if (window.matchMedia("(max-width: 760px)").matches) return;
    event.preventDefault();
    resizer.setPointerCapture?.(event.pointerId);
    document.body.classList.add("sidebar-resizing");
    const move = (moveEvent: PointerEvent) => resizeSidebar(moveEvent.clientX);
    const finish = () => {
      document.body.classList.remove("sidebar-resizing");
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", finish);
      window.removeEventListener("pointercancel", finish);
      persistSidebarLayout();
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", finish);
    window.addEventListener("pointercancel", finish);
  });
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
    toggle.innerHTML = `${icon(split ? "expand" : "panel")}${state.taskTool === "browser" ? "" : `<span>${label}</span>`}`;
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
  if (toggle) toggle.onclick = () => {
    state.taskToolMode = state.taskToolMode === "split" ? "fullscreen" : "split";
    persistTaskToolLayout();
    applyTaskToolLayout();
  };
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

// Measured 2026-11 with a 1000-message fixture at 1280x800: 1000 DOM message
// nodes, 79,819px of scroll height and a 1,099ms openTask. Rows are variable
// (56-102px: plain text, tool output, collapsed code), so the conversation is
// windowed around the viewport (see conversationWindow in conversation_ui.ts and
// the controller in main.ts). Live state is capped at 300 rows, but 300 mounted
// variable-height rows are still enough to make every patch expensive.
const initialConversationPageSize = 20;

function cancelTaskOpen(): void {
  taskOpenVersion++;
  openingTaskID = "";
  state.taskDetailLoading = false;
}

// One exit for both dismiss paths (Escape and the back control): stop the live
// feeds, put focus back on the row that opened the detail, and let a later list
// refresh fall back to the list container when that row disappears.
function closeTaskDetail(): void {
  cancelTaskOpen();
  if (state.taskTool === "hardware") stopHardwarePanel();
  if (state.taskTool === "browser") stopDesktopPanel();
  const returnID = taskCardFocusReturnID;
  state.selectedTask = null;
  clearTaskCardFocusReturn();
  closeEvents();
  render();
  focusTaskListReturnTarget(returnID);
}

function showTaskLoading(task: Task): void {
  closeEvents();
  state.view = "tasks";
  state.selectedProject = null;
  state.selectedTask = {
    task,
    turns: [],
    agents: [],
    event_cursor: 0,
    server_time_ms: Date.now(),
  };
  state.selectedTaskAgent = "main";
  resetConversationVirtualState();
  state.taskConversation = [];
  state.taskConversationHasMore = false;
  state.taskConversationBefore = 0;
  state.taskConversationLatest = 0;
  state.taskContext = null;
  state.taskMailOptions = null;
  state.taskMailLoading = true;
  state.taskMailError = "";
  state.taskTool = "";
  state.taskDraft = "";
  state.taskDrafts = {};
  state.taskAttachmentDrafts = {};
  state.taskChannelDestination = undefined;
  state.taskChannelState = null;
  state.taskChannelLoading = false;
  taskChannelLoadVersion++;
  state.taskDetailLoading = true;
  loadingOlderConversation = false;
  render();
}

async function openTask(taskID: string): Promise<void> {
  // Opening a Task takes priority over any idle warm-up or queued list refresh.
  cancelSecondaryPrefetch();
  secondaryPrefetchDone = true;
  const openStartedAt = performance.now();
  const version = ++taskOpenVersion;
  openingTaskID = taskID;
  const summary = state.tasks.find(task => task.id === taskID);
  if (summary) showTaskLoading(summary);
  try {
    // Read-only mirrors use the same filter state as local Tasks: the control
    // and the request must always name the same categories.
    const categories: ConversationCategory[] = selectedConversationCategories();
    let pagePromise = summary?.status === "draft"
      ? Promise.resolve(null)
      : api.agentConversation(taskID, "main", {limit: initialConversationPageSize, categories});
    const detail = await api.task(taskID);
    if (!summary && version === taskOpenVersion) showTaskLoading(detail.task);
    if (summary?.status === "draft" && detail.task.status !== "draft") {
      pagePromise = api.agentConversation(taskID, "main", {limit: initialConversationPageSize, categories});
    }
    const page = await pagePromise;
    if (version !== taskOpenVersion) return;
    detail.agents ||= [];
    state.selectedTask = detail;
    state.selectedTaskAgent = detail.agents.some(agent => agent.agent_id === "main")
      ? "main"
      : (detail.agents[0]?.agent_id || "main");
    state.taskConversation = page?.conversation.items || [];
    state.taskConversationHasMore = page?.conversation.has_more || false;
    state.taskConversationBefore = page?.conversation.next_before || 0;
    state.taskConversationLatest = page?.conversation.latest_sequence || 0;
    state.taskEventCursor = detail.event_cursor || 0;
    state.taskContext = null;
    state.taskTool = state.taskToolPinned === "channel" && (detail.task.read_only || detail.task.channel_instance_id)
      ? ""
      : state.taskToolPinned;
    state.taskDetailLoading = false;
    rememberCommandPaletteTask(taskID);
    requestConversationBottom();
    if (detail.task.read_only || detail.task.status === "draft") closeEvents();
    else openEvents(taskID, state.taskEventCursor);
    render();
    void loadTaskMail(taskID);
    void loadTaskChannelDestination(taskID, version);
    if (state.taskTool === "channel") {
      void reloadTaskChannelState().catch(error => {
        if (version === taskOpenVersion) setMessage("error", error instanceof Error ? error.message : String(error));
      });
    } else if (state.taskTool === "context" && detail.task.status !== "draft") {
      void api.agentContext(taskID, "main").then(context => {
        if (version !== taskOpenVersion || !state.selectedTask || state.selectedTask.task.id !== taskID || state.taskTool !== "context") return;
        state.taskContext = context;
        render();
      }).catch(error => {
        if (version === taskOpenVersion) setMessage("error", error instanceof Error ? error.message : String(error));
      });
    }
  } catch (error) {
    if (version === taskOpenVersion) {
      state.selectedTask = null;
      state.taskDetailLoading = false;
    }
    throw error;
  } finally {
    if (version === taskOpenVersion) {
      openingTaskID = "";
      const elapsed = performance.now() - openStartedAt;
      perfMetrics.taskOpenCount++;
      perfMetrics.taskOpenLastMs = Math.round(elapsed);
      perfMetrics.taskOpenTotalMs += elapsed;
      if (perfEnabled) console.debug(`[aha-perf] openTask ${perfMetrics.taskOpenLastMs}ms`);
    }
  }
}

function taskChannelButtonLabel(destination: ChannelDestination | null | undefined): string {
  if (!destination) return "连接渠道";
  const kind = destination.endpoint_kind === "group_digital_human" ? "群聊" : "私聊";
  const name = destination.display_name || (kind === "群聊" ? "未命名群聊" : "Owner 私聊");
  return `主渠道已连接 · ${kind} · ${destination.instance_name} · ${name}`;
}

function updateTaskChannelButton(): void {
  const button = document.querySelector<HTMLButtonElement>('[data-task-tool="channel"]');
  if (!button) return;
  const connected = Boolean(state.taskChannelDestination);
  const label = taskChannelButtonLabel(state.taskChannelDestination);
  button.classList.toggle("connected", connected);
  button.title = label;
  button.setAttribute("aria-label", label);
}

async function loadTaskChannelDestination(taskID: string, version: number): Promise<void> {
  if (!state.selectedTask || state.selectedTask.task.id !== taskID || state.selectedTask.task.read_only || state.selectedTask.task.channel_instance_id) return;
  const loadVersion = ++taskChannelLoadVersion;
  try {
    const result = await api.taskChannelRoutes(taskID);
    if (loadVersion !== taskChannelLoadVersion || version !== taskOpenVersion || !state.selectedTask || state.selectedTask.task.id !== taskID) return;
    state.taskChannelState = result;
    const current = result.route;
    state.taskChannelDestination = current
      ? result.destinations.find(item => item.conversation_id === current.conversation_id) || null
      : null;
    updateTaskChannelButton();
  } catch {
    // Opening the channel tool retries the full request and surfaces the error.
  }
}

async function selectTaskAgent(agentID: string): Promise<void> {
  if (!state.selectedTask || !state.selectedTask.agents.some(agent => agent.agent_id === agentID) || agentID === state.selectedTaskAgent) return;
  state.taskDrafts[state.selectedTaskAgent] = state.taskDraft;
  state.selectedTaskAgent = agentID;
  state.taskDraft = state.taskDrafts[agentID] || "";
  const page = await api.agentConversation(state.selectedTask.task.id, agentID, {
    limit: initialConversationPageSize,
    categories: selectedConversationCategories(),
  });
  resetConversationVirtualState();
  state.taskConversation = page.conversation.items || [];
  state.taskConversationHasMore = page.conversation.has_more;
  state.taskConversationBefore = page.conversation.next_before || 0;
  state.taskConversationLatest = page.conversation.latest_sequence || 0;
  const agent = state.selectedTask.agents.find(item => item.agent_id === agentID);
  if (agent) agent.unread_count = 0;
  state.taskContext = null;
  if (state.taskTool === "context") {
    state.taskContext = await api.agentContext(state.selectedTask.task.id, agentID);
  }
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
  if (!inherited) {
    const workspace = state.workspaces.find(item => item.id === state.selectedTask?.task.workspace_id);
    setClaudeOfficialCatalog("agent-config", claudeOfficialCatalog(workspace));
    syncRuntimeFields("agent-config", state.models, state.codexAccounts, false);
  }
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
    const form = dialog?.querySelector<HTMLFormElement>("#agent-config-form");
    agentConfigBaseline = form ? agentConfigFormSnapshot(form) : "";
    syncAgentFoldCounts();
    if (document.querySelector<HTMLSelectElement>("#agent-config-model-source")?.value === "claude_native") {
      void refreshClaudeOfficialCatalog("agent-config", state.selectedTask?.task.workspace_id || "");
    }
  }));
}

// Completed Tasks expose exactly one write action: reopening. The button only
// opens the confirmation dialog; the reopen call happens on dialog submit.
// Assigning onclick/onsubmit keeps this idempotent, because the composer is
// rebuilt whenever the Task freezes or reopens.
function bindReopenTaskControls(): void {
  const dialog = document.querySelector<HTMLDialogElement>("#reopen-task-dialog");
  const button = document.querySelector<HTMLButtonElement>("#reopen-task");
  if (button) button.onclick = () => dialog?.showModal();
  const form = document.querySelector<HTMLFormElement>("#reopen-task-form");
  if (!form) return;
  // The generic [data-close] binding only runs at bind time, which predates a
  // dialog inserted when the Task freezes live, so wire the dismissals here.
  dialog?.querySelectorAll<HTMLElement>("[data-close]").forEach(element => {
    element.onclick = () => dialog.close();
  });
  form.onsubmit = async event => {
    event.preventDefault();
    const taskID = state.selectedTask?.task.id;
    dialog?.close();
    if (!taskID) return;
    try {
      await api.reopenTask(taskID);
      setMessage("notice", "任务已重新打开，可以继续编辑和发送消息");
    } catch (error) {
      setMessage("error", error instanceof Error ? error.message : String(error));
    }
    await refreshTaskRuntime(taskID);
    updateTaskLiveRegions();
  };
}

async function reloadConversation(): Promise<void> {
  if (!state.selectedTask) return;
  const page = await api.agentConversation(state.selectedTask.task.id, state.selectedTaskAgent, {
    limit: initialConversationPageSize,
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
    // Elapsed time is intentionally absent: the card ticks it locally, while a
    // phase or tool change must be able to trigger an incremental re-render.
    activity: detail.activity ? [
      detail.activity.phase, detail.activity.tool_name, detail.activity.since_ms,
      detail.activity.turn_id, detail.activity.attempt,
    ] : null,
    hardware: detail.hardware,
    memory: detail.memory,
  });
}

let mailRefreshRequested = false;

async function loadTaskMail(taskID: string): Promise<void> {
  if (state.selectedTask?.task.id !== taskID && state.selectedTask) return;
  state.taskMailLoading = true;
  state.taskMailError = "";
  try {
    const [options, accounts] = await Promise.all([
      api.taskMail(taskID),
      api.mailAccounts().catch(() => null),
    ]);
    if (state.selectedTask?.task.id !== taskID) return;
    state.taskMailOptions = options;
    if (accounts) state.mailAccounts = accounts.accounts || [];
  } catch (error) {
    state.taskMailError = error instanceof Error ? error.message : String(error);
  } finally {
    state.taskMailLoading = false;
    if (state.selectedTask?.task.id === taskID) render();
  }
}

async function refreshTaskRuntime(taskID: string): Promise<boolean> {
  if (state.selectedTask?.task.id !== taskID) return false;
  const previousStatus = state.selectedTask.task.status;
  const previousSignature = taskRuntimeSignature(state.selectedTask);
  const previousContext = state.taskContext ? JSON.stringify(state.taskContext) : "";
  const previousMail = state.taskMailOptions ? JSON.stringify(state.taskMailOptions) : "";
  const refreshMail = mailRefreshRequested
    || Boolean(state.taskMailOptions?.accounts?.length || state.taskMailOptions?.messages?.length);
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
  if (taskStatusNeedsAnnouncement(previousStatus, detail.task.status)) {
    announceStatus(tr(statusLabel(detail.task.status)), detail.task.code || detail.task.title);
  }
  let changed = taskRuntimeSignature(detail) !== previousSignature;
  state.taskEventCursor = Math.max(state.taskEventCursor, detail.event_cursor || 0);
  if (page.conversation.items?.length) {
    const seen = new Set(state.taskConversation.map(item => item.sequence));
    const appended = page.conversation.items.filter(item => !seen.has(item.sequence));
    if (appended.length) {
      // A reader who left the bottom keeps their position; the hint tells them
      // there is new content instead of yanking the viewport.
      if (!conversationFollowBottom) conversationUnreadMessages += appended.length;
      state.taskConversation.push(...appended);
      changed = true;
      if (taskID === "task_pet") {
        const user = appended.find(item => item.kind === "user_message");
        if (user) notifyPetTaskThinking();
        const reply = [...appended].reverse().find(item => item.kind === "agent_message");
        if (reply) notifyPetTaskReply(reply.summary || "");
        if (!user && !reply && appended.some(item => item.kind === "turn_duration")) {
          notifyPetTaskIdle();
        }
      }
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
  if (refreshMail) {
    const [options, accounts] = await Promise.all([
      api.taskMail(taskID).catch(() => null),
      api.mailAccounts().catch(() => null),
    ]);
    if (options) {
      state.taskMailOptions = options;
      changed ||= JSON.stringify(options) !== previousMail;
    }
    if (accounts) state.mailAccounts = accounts.accounts || [];
    mailRefreshRequested = false;
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
      <div class="brand-lockup"><span class="brand-mark"><img src="/assets/aha-logo.svg" alt=""></span><strong>AHA</strong></div>
      <p>${t("login.tagline")}</p>
      <div class="secure-note">${icon("shield")}<span>${t("login.secure")}</span></div>
    </aside>
    <main class="login-main">
      ${recovery ? `<form id="password-recovery-form" class="auth-form">
        <h1>${t("login.recoveryTitle")}</h1>
        <p>${t("login.recoveryIntro")}</p>
        <label>${t("login.account")}<input name="username" value="${escapeHTML(state.auth?.username || "owner")}" autocomplete="username" required></label>
        <label>${t("login.setupToken")}<input name="setup_token" type="password" autocomplete="one-time-code" required></label>
        <label>${t("login.newPassword")}<input name="new_password" type="password" autocomplete="new-password" minlength="10" required></label>
        <label>${t("login.confirmPassword")}<input name="confirm_password" type="password" autocomplete="new-password" minlength="10" required></label>
        <button class="primary full" type="submit">${t("login.resetLogin")}</button>
        <button id="back-to-login" class="auth-link" type="button">${t("login.backLogin")}</button>
        ${state.error ? `<div class="form-error" role="alert">${escapeHTML(state.error)}</div>` : ""}
      </form>` : `<form id="auth-form" class="auth-form">
        <h1>${register ? t("login.initOwner") : t("login.login")}</h1>
        <p>${register ? t("login.initIntro") : t("login.loginIntro")}</p>
        ${register ? `<label>${t("login.setupToken")}<input name="setup_token" autocomplete="one-time-code" required></label>` : ""}
        <label>${t("login.account")}<input name="username" value="owner" autocomplete="username" required></label>
        <label>${t("login.password")}<input name="password" type="password" autocomplete="${register ? "new-password" : "current-password"}" minlength="10" required></label>
        <button class="primary full" type="submit">${register ? t("login.createOwner") : t("login.login")}</button>
        ${register ? "" : `<button id="forgot-password" class="auth-link" type="button">${t("login.forgot")}</button>`}
        ${state.error ? `<div class="form-error" role="alert">${escapeHTML(state.error)}</div>` : ""}
      </form>`}
    </main>
  </div>`;
}

function petPendingApprovalLabel(): string {
  return `${t("petWorkbench.title")} · ${t("petWorkbench.pendingCount", {count: petApprovalCounter.pending})}`;
}

function updatePetPendingApprovalIndicators(): void {
  const pending = petApprovalCounter.pending;
  const added = seenPendingApprovals === null ? 0 : pending - seenPendingApprovals;
  seenPendingApprovals = pending;
  if (added > 0) announceStatus(t("a11y.newApprovals", {count: added}));
  document.querySelectorAll<HTMLElement>('[data-view="pet-workbench"]').forEach(button => {
    button.classList.toggle("has-pending", pending > 0);
    const label = pending > 0 ? petPendingApprovalLabel() : t("nav.pet");
    button.title = label;
    button.setAttribute("aria-label", label);
    button.querySelector(".task-tool-badge")?.remove();
    button.insertAdjacentHTML("beforeend", petPendingApprovalBadge(pending));
  });
  const count = document.querySelector<HTMLElement>("#pet-approval-count");
  if (count) count.textContent = pending > 0 ? String(pending) : "";
}

async function refreshPetPendingApprovalCount(afterMutation = false): Promise<void> {
  if (!state.auth?.authenticated) return;
  try { await petApprovalCounter.refresh(afterMutation); }
  catch { /* Retain the last successful count; the bounded poll retries. */ }
}

// A pet_approval event changes the badge and, when the approvals tab is open,
// the mounted list. Reconnect uses the same path to catch up on missed events.
function refreshPetApprovalState(): void {
  void refreshPetPendingApprovalCount(true);
  petWorkbenchPageKeeper.binding?.refresh?.();
}

function navButton(view: View, glyph: string, label: Parameters<typeof t>[0]): string {
  const pending = view === "pet-workbench" ? petApprovalCounter.pending : 0;
  const description = pending > 0 ? petPendingApprovalLabel() : t(label);
  return `<button data-view="${view}" class="${state.view === view ? "active" : ""}${pending > 0 ? " has-pending" : ""}" title="${escapeHTML(description)}" aria-label="${escapeHTML(description)}">${icon(glyph)}<span>${t(label)}</span>${petPendingApprovalBadge(pending)}</button>`;
}

function shell(content: string): string {
  const nav = [
    ["projects", "projects", "nav.projects"],
    ["tasks", "tasks", "nav.tasks"],
    ["channels", "bot", "nav.channels"],
    ["knowledge", "knowledge", "nav.knowledge"],
    ["models", "model", "nav.models"],
    ["resources", "hardware", "nav.resources"],
    ["pet-workbench", "bot", "nav.pet"],
  ] as const;
  const mobileNav = [...nav, ["advanced", "settings", "nav.advanced"] as const];
  return `<div class="app-shell">
    <aside class="sidebar">
      <div class="brand-lockup"><span class="brand-mark"><img src="/assets/aha-logo.svg" alt=""></span><div><strong>AHA</strong><small>${t("brand.tagline")}</small></div></div>
      <button type="button" class="command-palette-trigger" data-command-palette-open aria-label="${t("palette.ariaLabel")}（${commandPaletteShortcutLabel()}）" title="${t("palette.placeholder")}">${icon("search")}<span>${t("palette.search")}</span><kbd>${commandPaletteShortcutLabel()}</kbd></button>
      <nav>${nav.map(([view, glyph, label]) => navButton(view, glyph, label)).join("")}</nav>
      <section class="sidebar-memo ${state.sidebarMemoCollapsed ? "collapsed" : ""}">
        <header><strong>${icon("edit")}便签</strong><button id="toggle-sidebar-memo" class="icon-button" type="button" aria-label="${state.sidebarMemoCollapsed ? "展开便签" : "折叠便签"}" title="${state.sidebarMemoCollapsed ? "展开便签" : "折叠便签"}">${icon(state.sidebarMemoCollapsed ? "chevron-up" : "chevron-down")}</button></header>
        <div class="sidebar-memo-colors" role="group" aria-label="便签背景颜色">${Object.entries(SIDEBAR_MEMO_COLORS).map(([id, color]) => `<button type="button" class="sidebar-memo-swatch ${state.sidebarMemoColor === id ? "active" : ""}" data-sidebar-memo-color="${id}" aria-label="${color.label}" title="${color.label}" style="--swatch-color:${color.surface}"></button>`).join("")}</div>
        <div class="sidebar-memo-body">${sidebarMemoItemsHTML()}<small id="sidebar-memo-status">已保存</small></div>
      </section>
      <div class="system-meta"><span>AHA2 ${escapeHTML(state.system.web_version || state.system.version || "dev")}</span><span id="system-uptime">${systemUptimeText()}</span></div>
      <div class="owner-block"><button id="owner-avatar" class="avatar" type="button" title="Owner">O</button><div><strong>${escapeHTML(state.auth?.username || "Owner")}</strong><small>${t("owner.signedIn")}</small></div><div class="owner-actions"><button id="advanced-settings" class="icon-button" type="button" data-view="advanced" title="${t("owner.advanced")}" aria-label="${t("owner.advanced")}">${icon("settings")}</button><button id="logout" class="icon-button" title="${t("owner.logout")}">${icon("logout")}</button></div></div>
    </aside>
    <div id="sidebar-resizer" class="sidebar-resizer" role="separator" aria-label="调整侧栏宽度" aria-orientation="vertical" aria-valuemin="${SIDEBAR_MIN_WIDTH}" aria-valuemax="${SIDEBAR_MAX_WIDTH}" aria-valuenow="${state.sidebarWidth}" tabindex="0" title="拖动调整侧栏宽度，双击恢复默认"></div>
    <header class="mobile-header"><div class="brand-lockup"><span class="brand-mark"><img src="/assets/aha-logo.svg" alt=""></span><strong>AHA</strong></div><button id="mobile-context" class="icon-button">${icon("menu")}</button></header>
    <main class="workspace">${resourceStatusHTML()}${banner()}${content}</main>
    <button type="button" class="command-palette-fab" data-command-palette-open aria-label="${t("palette.ariaLabel")}（${commandPaletteShortcutLabel()}）" title="${t("palette.placeholder")}">${icon("search")}</button>
    <nav class="bottom-nav">${mobileNav.map(([view, glyph, label]) => navButton(view, glyph, label)).join("")}</nav>
  </div>`;
}

function banner(): string {
  const messages = `${state.error ? `<div class="banner error">${escapeHTML(state.error)}</div>` : ""}${state.notice ? `<div class="banner success">${escapeHTML(state.notice)}</div>` : ""}`;
  return `<div class="banner-stack">${messages}</div>`;
}

function resourceStatusHTML(): string {
  const resources = resourcesForView(state.view);
  const failed = resources.filter(key => resourceStates[key].error);
  if (failed.length) return `<div class="app-hydrating resource-error" role="alert"><span>${t("resource.partialFailure")}</span><button type="button" id="retry-view-data">${t("common.retry")}</button></div>`;
  return "";
}

function systemUptimeText(now = Date.now()): string {
  const started = Date.parse(state.system.started_at || "");
  if (!Number.isFinite(started)) return t("uptime.unknown");
  const seconds = Math.max(0, Math.floor((now - started) / 1000));
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  const value = `${days ? t("uptime.days", {count: days}) : ""}${hours ? t("uptime.hours", {count: hours}) : ""}${t("uptime.minutes", {count: minutes})}`;
  return t("uptime.online", {value: value.trim()});
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
    const system = isSystemProject(project);
    return `<article class="list-row project-row" data-project="${project.id}">
      <div class="item-title"><span class="square-icon">${icon("projects")}</span><div><strong>${escapeHTML(project.name)}</strong><small>${projectTypeLabel(project.project_type)}${archived ? " · 渠道归档" : system ? " · 系统托管" : ""}</small></div><span class="row-actions">${archived ? `<span class="status warn">已归档</span><button type="button" data-delete-project="${project.id}" class="icon-button danger project-del" title="永久删除归档">${icon("close")}</button>` : system ? `<span class="status warn">系统托管</span>` : `<button type="button" data-edit-project="${project.id}" class="icon-button" title="编辑项目">${icon("edit")}</button><button type="button" data-delete-project="${project.id}" class="icon-button project-del" title="删除项目">${icon("close")}</button>`}</span></div>
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
  const system = isSystemProject(project);
  const rows = workspaces.map(item => `<article class="list-row ws-row ${item.read_only ? "read-only" : ""}">
    <div class="item-title">${item.locality === "remote" ? icon("server") : icon("monitor")}<div><strong>${escapeHTML(item.name)}</strong><small>${escapeHTML(item.root_path)}</small></div></div>
    <div class="ws-meta"><small>${escapeHTML(item.transport)}${item.distro ? ` · ${escapeHTML(item.distro)}` : ""}</small><strong>${item.channel_retired ? "渠道归档只读" : item.read_only ? `只读 · ${escapeHTML(item.owner_device_id || "其他设备")}` : `本机 · ${escapeHTML(item.owner_device_id || "待首次同步绑定")}`}</strong></div>
    ${system ? "" : renderWorkspaceDetection(item)}
    ${archived ? `<span class="status warn">归档只读</span>` : system ? `<span class="status warn">系统托管 · 只读</span>` : item.read_only ? `<span class="row-actions workspace-remote-actions"><button type="button" data-takeover-workspace="${item.id}">${icon("copy")}接管到本机</button><button type="button" data-retire-remote-workspace="${item.id}" class="icon-button danger" title="移除孤立 Workspace">${icon("close")}</button></span><span class="status warn">远端只读</span>` : `<button data-detect="${item.id}">${icon("refresh")}刷新状态</button><span class="row-actions"><button type="button" data-edit-workspace="${item.id}" class="icon-button" title="编辑 Workspace">${icon("edit")}</button><button type="button" data-delete-workspace="${item.id}" class="icon-button" title="删除 Workspace">${icon("close")}</button></span>`}
  </article>`).join("");
  return shell(`<section class="page">
    <header class="page-head"><div><button id="back-projects" class="back-link">← 返回项目列表</button><h1>${escapeHTML(project.name)}${archived ? ` <span class="status warn">已归档</span>` : system ? ` <span class="status warn">系统托管</span>` : ""}</h1><p>${projectTypeLabel(project.project_type)}${project.repository_identity ? ` · ${escapeHTML(project.repository_identity)}` : ""}${project.default_branch ? ` · 默认分支 ${escapeHTML(project.default_branch)}` : ""}${archived ? " · 渠道宿主历史只读" : system ? " · 系统托管，只读" : ""}</p></div><div class="actions">${archived ? `<button type="button" data-channel-project-export="${escapeHTML(project.channel_instance_id || "")}">导出归档</button><button id="delete-project" class="danger">${icon("close")}永久删除</button>` : system ? `<span class="status warn">系统托管 · 只读</span>` : `<button data-dialog="workspace">${icon("plus")}添加 Workspace</button><button type="button" data-edit-project-detail="${project.id}" class="icon-button" title="编辑项目">${icon("edit")}</button><button id="delete-project" class="danger">${icon("close")}删除项目</button>`}</div></header>
    <div class="metrics"><div><small>Workspace</small><strong>${workspaces.length}</strong></div><div><small>任务</small><strong>${tasks.length}</strong></div><div><small>Ready</small><strong>${workspaces.filter(item => item.health === "ready").length}</strong></div></div>
    <div class="panel"><div class="panel-head"><strong>Workspaces</strong><span>状态由 AHA 自动检测；Agent API 连接方式自动选择</span></div>${rows || `<div class="empty">尚无 Workspace，点击右上角添加。</div>`}</div>
    ${workspaceDialog()}${workspaceTakeoverDialog()}
  </section>`);
}

function projectDialog(): string {
  return `<dialog id="project-dialog"><form id="project-form" method="dialog"><div class="dialog-head"><h2>新建项目</h2><button type="button" data-close class="icon-button" aria-label="${t("common.closeDialog")}">${icon("close")}</button></div><input type="hidden" id="project-edit-id" name="project_edit_id" value=""><label>名称<input name="name" id="project-name" required></label><label>描述<textarea name="description" id="project-desc"></textarea></label><label>项目类型<select name="project_type" id="project-type"><option value="folder">普通文件夹</option><option value="git">Git 仓库</option></select></label><div class="git-fields" style="display:none"><label>Git Repository Identity<input name="repository_identity" id="project-repo" placeholder="可选：remote URL"></label><label>默认分支<input name="default_branch" id="project-branch" value="main"></label></div><div class="dialog-actions"><button type="button" data-close>取消</button><button class="primary" value="default">创建项目</button></div></form></dialog>`;
}

function workspaceDialog(): string {
  return `<dialog id="workspace-dialog"><form id="workspace-form" method="dialog">
    <div class="dialog-head"><h2>添加 Workspace</h2><button type="button" data-close class="icon-button" aria-label="${t("common.closeDialog")}">${icon("close")}</button></div>
    <input type="hidden" id="ws-edit-id" name="ws_edit_id" value="">
    <label>项目<select name="project_id" id="ws-project">${state.projects.filter(item => !item.channel_retired && !isSystemProject(item)).map(item => `<option value="${item.id}" ${item.id === state.dialogProjectID ? "selected" : ""}>${escapeHTML(item.name)}</option>`).join("")}</select></label>
    <label>名称<input name="name" id="ws-name" value="本地开发" required></label>
    <div class="two"><label>位置<select name="locality" id="ws-locality"><option value="local">本地</option><option value="remote">远程</option></select></label><label>Transport<select name="transport" id="ws-transport"></select></label></div>
    <label>Root Path<input name="root_path" id="ws-root-path" placeholder="E:\project 或 /home/user/project" required></label>
    <div class="wsl-fields" style="display:none"><label>WSL Distro<select name="distro" id="ws-distro"></select></label><div class="field-help">Root Path 填 WSL 内的路径，如 /home/user/project</div></div>
    <div class="ssh-fields" style="display:none"><div class="two"><label>SSH Host<input name="ssh_host" placeholder="192.168.1.10"></label><label>SSH User<input name="ssh_user" placeholder="root"></label></div><div class="two"><label>SSH Port<input name="ssh_port" type="number" min="1" max="65535" value="22"></label><label>SSH 登录方式<select name="ssh_auth"><option value="auto">自动（有密码时优先密码）</option><option value="password">密码</option><option value="key">Key (~/.ssh)</option></select></label></div><label>登录密码<input name="ssh_password" type="password" autocomplete="new-password" placeholder="可选"></label><label class="workspace-clear-secret"><input name="clear_ssh_password" type="checkbox">清除已保存密码</label><div class="field-help">密码保存在 Secret Store；编辑时留空会保留原密码。</div></div>
    <div class="workspace-agent-api-status-block"><strong>Agent API</strong><span class="workspace-agent-api-status">保存后由 AHA 自动选择连接方式。</span><div class="field-help">AHA 自动选择：SSH 工作区优先走 SSH stdio（自动准备并复用 helper），失败后回退 SSH 反向隧道，再回退局域网；WSL 工作区使用 localhost。排障用的手动覆盖在“高级设置 → 其他设置”中。</div></div>
    <div class="dialog-actions"><button type="button" data-close>取消</button><button class="primary" value="default">添加 Workspace</button></div>
  </form></dialog>`;
}

const PROVIDER_PAGE_SIZE = 5;
const CODEX_ACCOUNT_PAGE_SIZE = 3;
let modelSearchRenderTimer: number | null = null;

function filteredModels(): Model[] {
  return state.models.filter(item => textMatchesQuery([
    item.display_name,
    item.wire_model,
    item.provider_name,
    item.provider_id,
    item.backend,
    backendProtocolLabel(item),
  ], state.modelSearch));
}

function scheduleModelPageRender(): void {
  if (modelSearchRenderTimer !== null) window.clearTimeout(modelSearchRenderTimer);
  modelSearchRenderTimer = window.setTimeout(() => {
    modelSearchRenderTimer = null;
    refreshModelResults();
  }, 120);
}

function selectedModelIDs(): string[] {
  const live = new Set(state.models.map(item => item.id));
  return [...state.modelSelected].filter(id => live.has(id));
}

function currentModelPage(): ReturnType<typeof buildModelPage> {
  return buildModelPage(filteredModels(), state.modelPage, MODEL_PAGE_SIZE);
}

function modelRowHTML(item: Model): string {
  const selected = state.modelSelected.has(item.id);
  // The row is one compact line at every width. Provider, Backend, Context and
  // the wire_model small print are gone; the full name and wire_model survive
  // in the title so a truncated name can still be identified on hover.
  const title = item.wire_model ? `${item.display_name} · ${item.wire_model}` : item.display_name;
  return `<tr class="model-row" data-model-card="${escapeHTML(item.id)}"><td class="model-select-cell"><input type="checkbox" data-select-model="${escapeHTML(item.id)}" ${selected ? "checked" : ""} aria-label="选择 ${escapeHTML(item.display_name)}"></td><td class="model-name-cell"><strong title="${escapeHTML(title)}"><span class="model-name-text">${escapeHTML(item.display_name)}</span>${modelSupportsImage(item) ? '<span class="capability-badge">图片</span>' : ''}</strong></td><td class="model-actions-cell"><div class="model-row-actions"><div class="model-health-cell">${renderModelHealthCell(item.id, state.modelHealth[item.id])}</div><div class="row-actions"><button type="button" data-edit-model="${item.id}" class="icon-button" title="编辑模型">${icon("edit")}</button><button type="button" data-delete-model="${item.id}" class="icon-button" title="删除模型">${icon("close")}</button></div></div></td></tr>`;
}

function modelGroupRowsHTML(group: ReturnType<typeof buildModelPage>["groups"][number]): string {
  const groupIDs = group.items.map(item => item.id);
  const groupAllSelected = modelSelectionState(state.modelSelected, groupIDs) === "all";
  const count = group.total > group.items.length
    ? `共 ${group.total} 个 · 本页 ${group.items.length} 个`
    : `共 ${group.total} 个`;
  return `<tr class="model-group-row" data-model-group="${escapeHTML(group.providerId)}"><td class="model-select-cell"><input type="checkbox" data-select-model-group="${escapeHTML(group.providerId)}" ${groupAllSelected ? "checked" : ""} aria-label="全选本页分组 ${escapeHTML(group.providerName)}"><span class="model-group-select-label">全选本页</span></td><td colspan="2" class="model-group-title"><strong>${escapeHTML(group.providerName)}</strong><span class="model-group-count">${count}</span></td></tr>${group.items.map(modelRowHTML).join("")}`;
}

function modelResultsHTML(): string {
  const visibleModels = filteredModels();
  const modelPage = buildModelPage(visibleModels, state.modelPage, MODEL_PAGE_SIZE);
  const pageAllSelected = modelSelectionState(state.modelSelected, modelPage.pageIDs) === "all";
  const groups = modelPage.groups.map(modelGroupRowsHTML).join("");
  const modelEmpty = state.models.length === 0 ? "选择 Provider 后添加模型。" : "没有匹配的模型。";
  const modelTotal = state.modelSearch.trim()
    ? `匹配 ${visibleModels.length} / 共 ${state.models.length} 个`
    : `共 ${state.models.length} 个`;
  return `${modelBatchBarHTML(selectedModelIDs().length)}<div class="table-wrap models-table-wrap"><table class="models-table"><thead><tr><th class="model-select-cell"><input type="checkbox" data-select-models-page ${pageAllSelected ? "checked" : ""} aria-label="全选本页模型"><span class="model-page-select-label">全选本页</span></th><th>模型</th><th class="model-actions-head">操作</th></tr></thead><tbody>${groups || `<tr><td colspan="3">${modelEmpty}</td></tr>`}</tbody></table></div>${paginationHTML("models", modelPage.slice, modelTotal)}`;
}

function batchCheckSelectedModels(): void {
  const ids = selectedModelIDs();
  if (!ids.length) return;
  // Each model keeps its own row state; a rejection is recorded on that row and
  // the bounded worker pool continues with the remaining models.
  void runModelHealthChecks(state, ids, async modelID => (await api.checkModelHealth(modelID)).health, refreshModelResults);
}

function batchDeleteSelectedModels(button: HTMLElement | null): void {
  const ids = selectedModelIDs();
  if (!ids.length) {
    state.modelSelected.clear();
    refreshModelResults();
    return;
  }
  if (!window.confirm(`提交批量删除 ${ids.length} 个模型的审批？批准后才会全部删除。`)) return;
  void runWithFeedback(button, "提交中", async () => {
    await api.requestBatchDeleteModels(ids);
    state.modelSelected.clear();
    setMessage("notice", `已创建 1 张批量删除审批（${ids.length} 个模型）。请在「宠物工作台 → 审批」批准后执行。`);
    await loadAll();
    render();
  });
}

// Search results must not go through render(): the global render defers while
// a text input is focused. Replace only the results region so the search box
// and its focus (and caret) stay intact while typing.
function refreshModelResults(): void {
  const container = document.querySelector<HTMLElement>("#model-results");
  if (container) {
    container.innerHTML = modelResultsHTML();
    syncModelSelectionIndicators(container);
  }
}

// The rendered checked attributes only cover "all"; a partial selection is the
// browser's indeterminate state, which markup cannot express.
function syncModelSelectionIndicators(container: HTMLElement): void {
  const page = currentModelPage();
  const pageBox = container.querySelector<HTMLInputElement>("[data-select-models-page]");
  if (pageBox) {
    const indicator = modelSelectionIndicator(state.modelSelected, page.pageIDs);
    pageBox.checked = indicator.checked;
    pageBox.indeterminate = indicator.indeterminate;
  }
  container.querySelectorAll<HTMLInputElement>("[data-select-model-group]").forEach(box => {
    const group = page.groups.find(item => item.providerId === box.dataset.selectModelGroup);
    const indicator = modelSelectionIndicator(state.modelSelected, group ? group.items.map(item => item.id) : []);
    box.checked = indicator.checked;
    box.indeterminate = indicator.indeterminate;
  });
}

function openEditModelDialog(id: string): void {
  const model = state.models.find(item => item.id === id);
  if (!model) return;
  const nameInput = document.querySelector<HTMLInputElement>("#model-edit-name");
  const wireInput = document.querySelector<HTMLSelectElement>("#model-edit-wire");
  const backendInput = document.querySelector<HTMLSelectElement>("#model-edit-backend");
  const effortInput = document.querySelector<HTMLSelectElement>("#model-edit-effort");
  const contextInput = document.querySelector<HTMLInputElement>("#model-edit-context");
  const maxoutInput = document.querySelector<HTMLInputElement>("#model-edit-maxout");
  const compactInput = document.querySelector<HTMLInputElement>("#model-edit-compact");
  const imageInput = document.querySelector<HTMLInputElement>("#model-edit-image");
  if (nameInput) nameInput.value = model.display_name;
  const wireModelInput = document.querySelector<HTMLInputElement>("#model-edit-wire-model");
  if (wireModelInput) wireModelInput.value = model.wire_model || "";
  if (wireInput) {
    wireInput.value = persistedWireValue(model.wire_api || (model.backend === "claude" ? "anthropic_messages" : "responses"));
    wireInput.disabled = false;
  }
  if (backendInput) backendInput.value = model.backend;
  if (effortInput) effortInput.value = model.default_reasoning_effort || "";
  if (contextInput) contextInput.value = String(model.context_window || 0);
  if (maxoutInput) maxoutInput.value = String(model.max_output_tokens || 0);
  if (compactInput) compactInput.value = String(model.auto_compact_window || 0);
  if (imageInput) imageInput.checked = modelSupportsImage(model);
  (document.querySelector<HTMLDialogElement>("#model-edit-dialog"))?.showModal();
  document.querySelector<HTMLElement>("#save-model-edit")?.setAttribute("data-model-id", id);
}

// checkModelHealthByID runs one model row's health check. Progress and the
// result stay in that row: other rows keep their own state and stay clickable,
// and a failure must not raise a global error banner. The state machine lives in
// model_health.ts so clearing and request generations are behavior-tested.
async function checkModelHealthByID(id: string): Promise<void> {
  await runModelHealthCheck(state, id, async modelID => (await api.checkModelHealth(modelID)).health, refreshModelResults);
}

// forgetModelHealth clears one model's result and invalidates any request that
// is still in flight for it.
function forgetModelHealth(id: string): void {
  clearModelHealth(state, id);
}

// forgetAllModelHealth is for Provider changes and forced reloads, where the
// stored credential or endpoint can change for every model at once.
function forgetAllModelHealth(): void {
  clearAllModelHealth(state);
}

function deleteModelByID(id: string, button: HTMLElement | null): void {
  if (!id) return;
  if (!window.confirm("删除该模型？使用此模型的任务会暂停输入，进入任务后可重新选择模型。")) return;
  void runWithFeedback(button, "删除中", async () => {
    await api.deleteModel(id);
    forgetModelHealth(id);
    state.modelSelected.delete(id);
    setMessage("notice", "模型已删除");
    await loadAll();
    render();
  });
}

function modelsView(): string {
  const providerPage = paginateItems(state.providers, state.providerPage, PROVIDER_PAGE_SIZE);
  const providers = providerPage.items.map(item => `<article class="list-row provider-row">
    <div class="item-title">${icon("server")}<div><strong>${escapeHTML(item.name)}</strong><small>${escapeHTML(item.base_url || item.id)}</small></div></div>
    <span class="status ${item.credential_configured ? "good" : "warn"}">${item.credential_configured ? "Key Ready" : "No Key"}</span>
    <span class="row-actions"><button type="button" data-edit-provider="${item.id}" class="icon-button" title="编辑 Provider">${icon("edit")}</button><button type="button" data-delete-provider="${item.id}" class="icon-button" title="删除 Provider">${icon("close")}</button></span>
  </article>`).join("");
  const codexPage = paginateItems(state.codexAccounts, state.codexAccountPage, CODEX_ACCOUNT_PAGE_SIZE);
  return shell(`<section class="page models-page">
    ${pageHead("模型", "Models 管理 Provider / Env 模型与 Agent Backend；Codex 与 Claude Code 官方账号在创建或编辑 Task 时选择。")}
    ${backendsPanelHTML()}
    <div class="provider-layout">
      <div class="provider-sidebar">
        ${renderCodexAccounts(codexPage.items, paginationHTML("codex-accounts", codexPage))}
        <section class="panel"><div class="panel-head"><strong>Providers</strong><button data-dialog="provider">${icon("plus")}添加 Provider</button></div>${providers || `<div class="empty">先添加 Provider（API Base URL + API Key）。</div>`}${paginationHTML("providers", providerPage)}</section>
      </div>
      <section class="panel model-panel"><div class="panel-head"><strong>Models</strong><div class="panel-head-actions"><input id="model-search" name="model-query" class="model-search" type="search" autocomplete="off" data-1p-ignore="true" data-lpignore="true" data-bwignore="true" spellcheck="false" readonly placeholder="搜索模型 / Provider / Backend" value="${escapeHTML(state.modelSearch)}"><button data-dialog="model">${icon("plus")}添加模型</button></div></div><div id="model-results">${modelResultsHTML()}</div></section>
    </div>
    ${providerDialog()}${modelDialog()}${editModelDialog()}
  </section>`);
}

function advancedSettingsView(): string {
  const language = normalizeLanguagePreference(state.generalSettings.language);
  return shell(`<section class="page advanced-settings-page">
    <header class="page-head"><div><h1>${t("advanced.title")}</h1><p>${t("advanced.subtitle")}</p></div></header>
    <section class="panel language-settings-panel">
      <div class="language-settings-copy">${icon("settings")}<div><strong>${t("language.title")}</strong><small>${t("language.desc")}</small></div></div>
      <label class="language-settings-control"><span>${t("language.select")}</span><select id="language-setting">
        <option value="auto" ${language === "auto" ? "selected" : ""}>${t("language.auto")}</option>
        <option value="zh-CN" ${language === "zh-CN" ? "selected" : ""}>${t("language.zh")}</option>
        <option value="en-US" ${language === "en-US" ? "selected" : ""}>${t("language.en")}</option>
      </select></label>
      <p class="field-help">${t("language.note")}</p>
    </section>
    <section class="advanced-tools-grid">
      <article class="panel advanced-tool-card"><div>${icon("bot")}<span><strong>${t("advanced.prompts.title")}</strong><small>${t("advanced.prompts.desc")}</small></span></div><button type="button" data-view="prompts">${t("advanced.prompts.action")}</button></article>
      <article class="panel advanced-tool-card"><div>${icon("sync")}<span><strong>${t("advanced.sync.title")}</strong><small>${t("advanced.sync.desc")}</small></span></div><button type="button" data-view="sync">${t("advanced.sync.action")}</button></article>
      <article class="panel advanced-tool-card"><div>${icon("proxy")}<span><strong>${t("advanced.proxy.title")}</strong><small>${t("advanced.proxy.desc")}</small></span></div><button type="button" data-view="proxy">${t("advanced.proxy.action")}</button></article>
      <article class="panel advanced-tool-card"><div>${icon("server")}<span><strong>${t("advanced.data.title")}</strong><small>${t("advanced.data.desc")}</small></span></div><button type="button" data-view="data">${t("advanced.data.action")}</button></article>
      <article class="panel advanced-tool-card"><div>${icon("bot")}<span><strong>${t("advanced.mcp.title")}</strong><small>${t("advanced.mcp.desc")}</small></span></div><button type="button" data-view="mcp">${t("advanced.mcp.action")}</button></article>
      <article class="panel advanced-tool-card"><div>${icon("shield")}<span><strong>${t("advanced.other.title")}</strong><small>${t("advanced.other.desc")}</small></span></div><button type="button" data-view="other">${t("advanced.other.action")}</button></article>
    </section>
  </section>`);
}

function formatDataSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  return `${value >= 100 || unit === 0 ? Math.round(value) : value.toFixed(1)} ${units[unit]}`;
}

function dataCategoryText(category: {id: string; label: string; note?: string}, field: "label" | "note"): string {
  const key = `data.category.${category.id}${field === "note" ? ".note" : ""}`;
  const translated = t(key);
  if (translated !== key) return translated;
  return field === "note" ? (category.note || "") : category.label;
}

function dataManagementView(): string {
  const inventory = state.dataInventory;
  const rows = inventory
    ? inventory.categories.map(category => {
      const cleanup = category.id === "backups"
        ? `<div class="data-cleanup-actions"><label>${t("data.keepRecent")} <input id="data-backup-keep" type="number" min="1" max="50" value="5" aria-label="${t("data.keepRecent")}"> ${t("data.copies")}</label><button type="button" data-data-cleanup="backups" ${category.bytes === 0 ? "disabled" : ""}>${t("data.cleanBackups")}</button></div>`
        : category.id === "logs"
          ? `<button type="button" data-data-cleanup="logs" ${category.bytes === 0 ? "disabled" : ""}>${t("data.cleanLogs")}</button>`
          : `<span class="field-help">${t("common.readOnly")}</span>`;
      const note = dataCategoryText(category, "note");
      return `<tr>
        <td><div class="data-category"><strong>${escapeHTML(dataCategoryText(category, "label"))}</strong>${note ? `<small class="field-help">${escapeHTML(note)}</small>` : ""}</div></td>
        <td class="data-size">${formatDataSize(category.bytes)}</td>
        <td>${cleanup}</td>
      </tr>`;
    }).join("")
    : "";
  return advancedSubview(`<section class="page data-management-page">
    <header class="page-head"><div><h1>${t("data.title")}</h1><p>${t("data.subtitle")}</p></div><button type="button" data-data-refresh ${state.dataInventoryLoading ? "disabled" : ""}>${state.dataInventoryLoading ? `${icon("spinner", true)}${t("common.loading")}` : t("common.refresh")}</button></header>
    ${state.dataInventoryError ? `<div class="security-warning active">${escapeHTML(state.dataInventoryError)}</div>` : ""}
    <section class="panel data-summary">
      <div><span class="field-help">${t("data.dataDir")}</span><code>${escapeHTML(inventory?.data_dir || t("common.loading"))}</code></div>
      <strong>${inventory ? formatDataSize(inventory.total_bytes) : "—"}</strong>
    </section>
    <section class="panel spaced">
      <div class="panel-head"><strong>${t("data.details")}</strong><small class="field-help">${t("data.detailsNote")}</small></div>
      <div class="table-wrap"><table class="data-table"><thead><tr><th>${t("data.category")}</th><th>${t("data.size")}</th><th>${t("data.safeAction")}</th></tr></thead><tbody>${rows || `<tr><td colspan="3" class="field-help">${t("data.reading")}</td></tr>`}</tbody></table></div>
    </section>
    ${renderDatabasePanel(state.dataRetention)}
    <section class="panel spaced">
      <div class="panel-head"><strong>${t("data.cleanupScope")}</strong></div>
      <div class="data-cleanup-note"><p class="field-help">${t("data.cleanupScopeBody")}</p></div>
    </section>
  </section>`);
}

function bindLanguageSetting(): void {
  const select = document.querySelector<HTMLSelectElement>("#language-setting");
  select?.addEventListener("change", () => {
    const language = normalizeLanguagePreference(select.value);
    select.disabled = true;
    void api.updateGeneralSettings(language).then(result => {
      state.generalSettings = result.general;
      applyLocale(result.general.language);
      setMessage("notice", t("language.saved"));
      render();
    }).catch((error: unknown) => {
      select.disabled = false;
      const message = error instanceof Error ? error.message : String(error);
      setMessage("error", `${t("language.saveFailed")}: ${message}`);
    });
  });
}

// The switch applies immediately: enabling installs the observer and starts the
// panel in place, so the Owner can keep the page they are diagnosing.
function bindOtherSettingsPage(): void {
  bindPerfPanelSetting();
  bindKnowledgeVaultSettingsPanel();
}

function bindPerfPanelSetting(): void {
  const toggle = document.querySelector<HTMLInputElement>("#perf-panel-setting");
  if (!toggle) return;
  toggle.addEventListener("change", () => {
    if (toggle.checked) enablePerfPanel();
    else disablePerfPanel();
    // Re-read the effective state so a storage failure leaves the control
    // showing the truth instead of the optimistic click state.
    toggle.checked = isPerfPanelOn();
    setMessage("notice", toggle.checked ? "性能面板已开启" : "性能面板已关闭");
    render();
  });
}

function bindDataManagementPage(): void {
  bindDataRetentionPanel();
  const refresh = document.querySelector<HTMLButtonElement>("[data-data-refresh]");
  refresh?.addEventListener("click", () => {
    void runWithFeedback(refresh, t("common.loading"), async () => {
      await Promise.all([loadDataInventory(true), loadDatabaseStats(true)]);
      render();
    });
  });
  document.querySelectorAll<HTMLButtonElement>("[data-data-cleanup]").forEach(button => {
    button.addEventListener("click", () => {
      const category = button.dataset.dataCleanup || "";
      if (category === "backups") {
        const keepInput = document.querySelector<HTMLInputElement>("#data-backup-keep");
        const keep = Math.max(1, Math.min(50, Number(keepInput?.value) || 5));
        const backups = state.dataInventory?.categories.find(item => item.id === "backups");
        if (!window.confirm(t("data.confirmBackups", {keep, size: formatDataSize(backups?.bytes || 0)}))) return;
        void runWithFeedback(button, t("data.cleaning"), async () => {
          const result = await api.cleanupData({category, keep, confirm: true});
          state.dataInventory = result.result.inventory;
          const skipped = result.result.skipped ? t("data.skipped", {count: result.result.skipped}) : "";
          setMessage("notice", `${t("data.cleanedBackups", {count: result.result.removed, size: formatDataSize(result.result.freed_bytes)})}${skipped}`);
          render();
        });
        return;
      }
      if (category === "logs") {
        const logs = state.dataInventory?.categories.find(item => item.id === "logs");
        if (!window.confirm(t("data.confirmLogs", {size: formatDataSize(logs?.bytes || 0)}))) return;
        void runWithFeedback(button, t("data.cleaning"), async () => {
          const result = await api.cleanupData({category, confirm: true});
          state.dataInventory = result.result.inventory;
          const skipped = result.result.skipped ? t("data.skipped", {count: result.result.skipped}) : "";
          setMessage("notice", `${t("data.cleanedLogs", {count: result.result.removed, size: formatDataSize(result.result.freed_bytes)})}${skipped}`);
          render();
        });
      }
    });
  });
}

function bindDataRetentionPanel(): void {
  const view = state.dataRetention;
  const daysInput = document.querySelector<HTMLInputElement>("#data-hardware-io-days");
  const rowsInput = document.querySelector<HTMLInputElement>("#data-hardware-io-rows");
  const save = document.querySelector<HTMLButtonElement>("[data-hardware-io-save]");
  const preview = document.querySelector<HTMLButtonElement>("[data-hardware-io-preview]");
  const purge = document.querySelector<HTMLButtonElement>("[data-hardware-io-purge]");
  const formError = document.querySelector<HTMLElement>("[data-hardware-io-form-error]");
  if (!daysInput || !rowsInput || !save || !preview || !purge) return;
  // Re-check the form on every edit: invalid values disable every action, and
  // a preview only enables cleanup while the form still matches it.
  const formValues = (): {retention_days: number; max_delete_rows: number} | null => {
    const message = retentionFormError(daysInput.value, rowsInput.value);
    if (formError) {
      formError.textContent = message;
      formError.hidden = !message;
    }
    if (message) return null;
    return {retention_days: Number(daysInput.value), max_delete_rows: Number(rowsInput.value)};
  };
  const syncButtons = (): void => {
    const values = formValues();
    const busy = Boolean(view.busy);
    save.disabled = busy || !values;
    preview.disabled = busy || !values;
    purge.disabled = !values || !purgeEnabled(view, values.retention_days, values.max_delete_rows);
    const stale = Boolean(values && view.plan && (view.plan.retention_days !== values.retention_days || view.plan.max_delete_rows !== values.max_delete_rows));
    purge.title = stale ? t("data.db.previewStale") : "";
  };
  daysInput.addEventListener("input", syncButtons);
  rowsInput.addEventListener("input", syncButtons);
  syncButtons();
  const run = (kind: DataRetentionViewState["busy"], button: HTMLButtonElement, action: (values: {retention_days: number; max_delete_rows: number}) => Promise<void>): void => {
    const values = formValues();
    if (!values || view.busy) return;
    view.busy = kind;
    void runWithFeedback(button, t("common.loading"), () => action(values)).finally(() => {
      view.busy = "";
      render();
    });
  };
  save.addEventListener("click", () => run("save", save, async values => {
    const result = await api.updateHardwareIORetention(values);
    if (view.database) view.database.retention = result.retention;
    setMessage("notice", t("data.db.saved"));
  }));
  preview.addEventListener("click", () => run("preview", preview, async values => {
    view.result = null;
    view.plan = (await api.previewHardwareIOPurge(values)).plan;
    // Keep the previewed values in the form after the re-render.
    if (view.database) view.database.retention = {...view.database.retention, ...values};
  }));
  purge.addEventListener("click", () => {
    const values = formValues();
    const plan = view.plan;
    if (!values || !plan || !purgeEnabled(view, values.retention_days, values.max_delete_rows)) return;
    if (!window.confirm(t("data.db.confirmPurge", {rows: plan.run_rows, cutoff: new Date(plan.cutoff).toLocaleString(), size: formatBytes(plan.run_bytes_estimate)}))) return;
    run("purge", purge, async confirmed => {
      const response = await api.purgeHardwareIO({...confirmed, confirm: true});
      view.result = response.result;
      view.plan = null;
      if (response.database) view.database = {...response.database, retention: {...response.database.retention, ...confirmed}};
      const skip = response.result.skip_reason ? ` ${skipReasonText(response.result.skip_reason)}` : "";
      setMessage("notice", `${t("data.db.resultDeleted", {rows: response.result.deleted_rows, batches: response.result.batches})}${skip}`);
    });
  });
}

function otherSettingsView(): string {
  const origin = state.securitySettings;
  const backendSettings = state.backendSettings;
  const network = state.networkSettings;
  const accessScope = origin.access_scope === "local" ? "local" : "lan";
  const restartRequired = Boolean(network.restart_required);
  const agentAPI = state.agentAPISettings;
  const agentAPIMigrationNote = String(agentAPI.migration_note || "");
  const perfPanelOn = isPerfPanelOn();
  return advancedSubview(`<section class="page advanced-settings-page"><header class="page-head"><div><h1>其他设置</h1><p>Backend、Owner 账号、访问范围与本机网络</p></div></header>
    <section class="panel spaced account-security-panel perf-panel-settings-panel">
      <div class="panel-head"><strong>性能面板</strong><span id="perf-panel-status">${perfPanelOn ? "已开启" : "已关闭"}</span></div>
      <label class="security-setting-toggle"><input id="perf-panel-setting" name="perf_panel" type="checkbox" ${perfPanelOn ? "checked" : ""}><span><strong>显示性能面板</strong><small>开启后显示 API/首屏耗时面板；关闭后立即隐藏。</small></span></label>
      <div class="field-help">偏好只保存在当前浏览器；带 <code>?perf=1</code> 打开页面也会自动开启，不会同步到服务端。</div>
    </section>
    ${renderKnowledgeVaultSettingsPanel()}
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
    <section class="panel spaced account-security-panel access-scope-panel">
      <div class="panel-head"><strong>访问范围</strong><span>${accessScope === "local" ? "仅本机" : "本机与局域网"}</span></div>
      <form id="access-scope-form">
        <label>允许访问的来源<select name="access_scope"><option value="lan" ${accessScope === "lan" ? "selected" : ""}>本机与局域网</option><option value="local" ${accessScope === "local" ? "selected" : ""}>仅本机</option></select></label>
        <div class="field-help">AHA2 监听所有网卡，此处决定哪些来源会被响应。改为“仅本机”后，局域网内其他设备的访问会立即被拒绝；保存即生效，无需重启。</div>
        <div class="security-warning ${accessScope === "local" ? "active" : ""}">${accessScope === "local" ? `当前只响应本机地址的请求。其他设备上的浏览器、Agent 与同步都无法连接。工作区若通过局域网地址回连 AHA（Agent API 地址不是 127.0.0.1），也会一并失败。` : `局域网内可达的设备都能打开登录页，实际进入仍需 Owner 密码。`}</div>
        <div class="dialog-actions"><button class="primary" type="submit">保存访问范围</button></div>
      </form>
    </section>
    <section class="panel spaced account-security-panel agent-api-override-panel">
      <div class="panel-head"><strong>Agent API 高级覆盖</strong><span>仅用于排障</span></div>
      <form id="agent-api-override-form">
        <label>手动覆盖地址<input name="url" type="url" value="${escapeHTML(agentAPI.url)}" placeholder="https://aha.example.com"></label>
        <div class="field-help">默认不需要填写。AHA 会自动选择：SSH 工作区优先 SSH stdio，失败后回退 SSH 反向隧道 → 局域网；WSL 工作区使用 localhost。只有这些都不可用时，才在这里指定地址；只接受 <code>https://</code> 或 loopback <code>http://</code>，非 loopback 明文地址不会被接受。</div>
        <div class="field-help">当前自动地址：<code>${escapeHTML(agentAPI.effective_url || "-")}</code>${agentAPI.startup_url ? ` · 启动推导：<code>${escapeHTML(agentAPI.startup_url)}</code>` : ""}</div>
        <div class="field-help">SSH 工作区默认通过会话自带的 stdio 承载 Agent API，不需要 sshd 端口转发；helper 放在工作区的 <code>.aha2-context/share/agent-proxy</code>。这里只保留排障用的手动覆盖。</div>
        ${agentAPIMigrationNote ? `<div class="security-warning active">配置迁移：${escapeHTML(agentAPIMigrationNote)}</div>` : ""}
        <div class="dialog-actions"><button class="primary" type="submit">保存覆盖</button><button type="button" id="clear-agent-api-override">清除覆盖</button></div>
      </form>
    </section>
    <section class="panel spaced account-security-panel listen-address-panel">
      <div class="panel-head"><strong>监听地址</strong><span>${restartRequired ? "需重启生效" : "已生效"}</span></div>
      <form id="listen-address-form">
        <label>监听地址<input name="listen_address" value="${escapeHTML(network.listen_address)}" placeholder="${escapeHTML(network.startup_listen_address || "0.0.0.0:8766")}"></label>
        <div class="field-help">格式为 <code>IP:端口</code>，例如 <code>0.0.0.0:8766</code>（所有网卡）或 <code>127.0.0.1:8766</code>（仅本机）。留空则继续使用服务安装时选择的地址：<code>${escapeHTML(network.startup_listen_address || "-")}</code>。</div>
        <div class="security-warning ${restartRequired ? "active" : ""}">${restartRequired ? `已保存 <code>${escapeHTML(network.listen_address)}</code>，当前进程仍监听 <code>${escapeHTML(network.startup_listen_address)}</code>，重启 AHA2 后生效。` : ""}</div>
        <div class="dialog-actions"><button class="primary" type="submit">保存监听地址</button></div>
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
    <section class="panel spaced recovery-note"><div class="panel-head"><strong>密码恢复</strong></div><div><p>忘记密码时，可在登录页使用 AHA2 数据目录中的 <code>setup-token</code> 重置；它不是程序安装目录中的同名文件。</p><p>默认位置：<code>C:\\ProgramData\\AHA2\\setup-token</code>。安装时自定义过数据目录时，以计划任务 <code>--data-dir</code> 的实际路径为准；修改或恢复该文件后需重启 AHA2。重置成功会撤销其他登录会话。</p></div></section>
  </section>`);
}

function advancedSubview(content: string): string {
  return shell(`<div class="advanced-subview"><div class="advanced-subview-back"><button type="button" data-view="advanced">${t("common.backAdvanced")}</button></div>${content}</div>`);
}

function providerDialog(): string {
  const presets = Object.entries(PROVIDER_PRESETS)
    .filter(([key]) => key !== "custom")
    .map(([key, preset]) => `<option value="${key}">${escapeHTML(preset.name)}</option>`)
    .join("");
  return `<dialog id="provider-dialog"><div class="dialog-head"><h2>添加 Provider</h2><button type="button" data-close class="icon-button" aria-label="${t("common.closeDialog")}">${icon("close")}</button></div><div class="dialog-body"><input type="hidden" id="provider-edit-id" name="provider_edit_id" value=""><label>常用供应商<select id="provider-preset">${presets}<option value="custom">自定义</option></select></label><label>名称<input id="provider-name" placeholder="网关名称" aria-describedby="provider-name-error"><span id="provider-name-error" class="field-error" hidden></span></label><label>API Base URL（OpenAI 风格）<input id="provider-base-url" placeholder="https://api.example.com/v1" aria-describedby="provider-base-url-error"><span id="provider-base-url-error" class="field-error" hidden></span></label><label>Anthropic Base URL（可选，仅当与上方不同）<input id="provider-anthropic-url" placeholder="https://api.example.com/anthropic"></label><label>API Key<input id="provider-api-key" type="password" placeholder="留空则保持不变"></label><div class="dialog-actions"><button type="button" id="save-provider" class="primary">保存 Provider</button></div></div></dialog>`;
}

function modelDialog(): string {
  const options = state.providers.map(item => `<option value="${item.id}">${escapeHTML(item.name)}</option>`).join("");
  return `<dialog id="model-dialog" class="wide"><div class="dialog-head"><h2>添加模型</h2><button type="button" data-close class="icon-button" aria-label="${t("common.closeDialog")}">${icon("close")}</button></div><div class="dialog-body"><label>Provider<select id="model-provider">${options || `<option value="">先添加 Provider</option>`}</select></label><div class="dialog-actions"><button type="button" id="detect-models" class="primary">${icon("refresh")}获取模型列表</button><button type="button" id="open-model-add" disabled>添加 (0)</button></div><div id="model-detect-results"></div></div></dialog><dialog id="model-add-dialog"><div class="dialog-head"><h2>添加所选模型</h2><button type="button" data-close class="icon-button" aria-label="${t("common.closeDialog")}">${icon("close")}</button></div><div class="dialog-body"><div class="two"><label>Backend<select id="model-add-backend">${backendSelectOptionsHTML()}</select></label><label>协议<select id="model-add-wire"></select></label></div><div class="field-help" id="model-add-summary">已选 0 个模型</div><div class="dialog-actions"><button type="button" data-close>取消</button><button type="button" id="confirm-model-add" class="primary">添加</button></div></div></dialog>`;
}

function editModelDialog(): string {
  return `<dialog id="model-edit-dialog"><div class="dialog-head"><h2>编辑模型</h2><button type="button" data-close class="icon-button" aria-label="${t("common.closeDialog")}">${icon("close")}</button></div><div class="dialog-body"><label>显示名称<input id="model-edit-name"></label><label>模型 ID（发送给后端）<input id="model-edit-wire-model" spellcheck="false"><span class="field-help">决定实际调用的模型；Claude 走网关且需要 1M 上下文时，在此追加 [1m]。</span></label><div class="two"><label>模型协议<select id="model-edit-wire">${modelWireEditOptionsHTML()}</select></label><label>Agent Backend<select id="model-edit-backend">${backendSelectOptionsHTML()}</select></label></div><div class="two"><label>默认推理强度<select id="model-edit-effort"><option value="">继承默认</option><option>high</option><option>medium</option><option>low</option></select></label><label>Context Window<input id="model-edit-context" type="number" value="0"></label></div><label>Max Output Tokens<input id="model-edit-maxout" type="number" value="0"></label><label>自动压缩窗口（tokens）<input id="model-edit-compact" type="number" min="0" step="1000" value="0"><span class="field-help">按上下文窗口的 80% 计算，并预留最大输出与 16K 余量；0 表示交给 Agent CLI 自己决定。Claude Code 和 Codex 会读取该值。</span></label><label class="security-setting-toggle"><input id="model-edit-image" type="checkbox"><span><strong>支持图片输入</strong><small>默认开启；仅当该模型确实不能接收图片时关闭。DSH 会据此在 profile 里声明 input 模态。</small></span></label><div class="dialog-actions"><button type="button" data-close>取消</button><button type="button" id="save-model-edit" class="primary">保存</button></div></div></dialog>`;
}

function workspaceTakeoverDialog(): string {
  return `<dialog id="workspace-takeover-dialog"><form id="workspace-takeover-form"><div class="dialog-head"><h2>接管只读 Workspace</h2><button type="button" data-close class="icon-button" aria-label="${t("common.closeDialog")}">${icon("close")}</button></div><input type="hidden" name="source_id"><p class="field-help">接管会创建新的本机 Workspace；远端镜像保持只读且不会被修改。</p><label>名称<input name="name" required></label><div class="two"><label>Transport<select name="transport"><option value="native">Native</option><option value="wsl">WSL</option><option value="ssh">SSH</option></select></label><label>本机 Root Path<input name="root_path" required placeholder="必须重新确认本机路径"></label></div><label class="workspace-takeover-wsl">WSL Distro<input name="distro" placeholder="例如 Ubuntu"></label><div class="workspace-takeover-ssh"><div class="two"><label>SSH Host<input name="ssh_host"></label><label>SSH User<input name="ssh_user"></label></div><div class="two"><label>SSH Port<input name="ssh_port" type="number" min="1" max="65535" value="22"></label><label>SSH Auth<select name="ssh_auth"><option value="auto">Auto</option><option value="password">Password</option><option value="key">Key</option></select></label></div><label class="reuse-remote-secret"><input name="reuse_remote_credential" type="checkbox">复用已同步的端到端加密 SSH 凭据</label><label>或输入新密码<input name="ssh_password" type="password" autocomplete="new-password"></label></div><div class="dialog-actions"><button type="button" data-close>取消</button><button class="primary" type="submit">创建本机副本</button></div></form></dialog>`;
}

interface TaskFilterOption {
  value: string;
  label: string;
  // Absent until the server totals load; the popover then omits the number
  // instead of showing a page-derived count that would be wrong.
  count?: number;
}

function taskFilterSelection(kind: TaskFilterKind): Set<string> {
  if (kind === "project") return state.taskProjectFilters;
  if (kind === "status") return state.taskStatusFilters;
  return state.taskDeviceFilters;
}

function taskFilterPopover(
  kind: TaskFilterKind,
  label: string,
  allLabel: string,
  options: TaskFilterOption[],
): string {
  const selected = taskFilterSelection(kind);
  const selectedLabels = options.filter(option => selected.has(option.value)).map(option => option.label);
  const summary = selected.size === 0
    ? allLabel
    : selectedLabels.length === 1
      ? selectedLabels[0]
      : `已选 ${selected.size} 项`;
  const rows = options.map(option => `<label class="task-filter-option" title="${escapeHTML(option.label)}">
    <input type="checkbox" data-task-filter-option="${kind}" value="${escapeHTML(option.value)}" ${selected.has(option.value) ? "checked" : ""}>
    <span><strong>${escapeHTML(option.label)}</strong>${option.count === undefined ? "" : `<small>${option.count}</small>`}</span>
  </label>`).join("");
  return `<details class="task-filter-popover" data-task-filter="${kind}" ${state.taskOpenFilter === kind ? "open" : ""}>
    <summary class="task-filter-trigger" title="按${label}筛选">${icon("filter")}<span><small>${label}</small><strong>${escapeHTML(summary)}</strong></span></summary>
    <div class="task-filter-menu">
      <header><strong>${label}筛选</strong><button type="button" data-task-filter-clear="${kind}" ${selected.size ? "" : "disabled"}>全部</button></header>
      <div>${rows || `<div class="empty">暂无可选项</div>`}</div>
    </div>
  </details>`;
}

function taskCardSignature(task: Task, project?: Project, workspace?: Workspace): string {
  // A stable signature lets a list refresh keep every unchanged row as the
  // same DOM node, so focus, text selection and in-row state survive.
  return stableTextSignature([task, project?.name || "", workspace?.root_path || ""]);
}

function taskCardHtml(task: Task): string {
  const project = state.projects.find(item => item.id === task.project_id);
  const ws = state.workspaces.find(item => item.id === task.workspace_id);
  const archived = task.channel_retired === true || task.read_only_reason === "channel_retired";
  // Read-only rows already carry their own state badge in the action column, so
  // only ordinary rows get the mobile action-column status copy; the mobile
  // breakpoint hides the title badge for every row to avoid showing it twice.
  const readonlyBadge = archived || task.read_only || task.status === "completed";
  // The card body is the keyboard entry point; the edit/delete buttons are
  // siblings so they stay independently operable and never open the Task.
  const openLabel = [task.code, task.title, tr(statusLabel(task.status))].filter(Boolean).join(" ");
  return `<article class="task-card" data-task="${task.id}" data-task-signature="${taskCardSignature(task, project, ws)}">
    <div class="task-card-main" role="button" tabindex="0" data-task-open aria-label="${escapeHTML(openLabel)}">
      <div class="task-card-title"><span class="task-code">${escapeHTML(task.code || "")}</span><h3 data-task-title="${task.id}">${escapeHTML(task.title)}</h3><span class="status task-card-title-status ${statusClass(task.status)}">${statusLabel(task.status)}</span></div>
      ${task.current_goal && task.current_goal !== task.title ? `<p class="task-goal">${escapeHTML(task.current_goal)}</p>` : ""}
      <div class="task-meta">
        <span title="所属项目">${icon("projects")}<span>${escapeHTML(project?.name || task.project_id)}</span></span>
        <span title="工作目录">${icon("monitor")}<span>${escapeHTML(ws?.root_path || "-")}</span></span>
        ${task.task_branch ? `<span title="任务分支">${icon("branch")}<span>${escapeHTML(task.task_branch)}</span></span>` : ""}
        <span title="Main 与 Sub Agent 累计 Token">${icon("model")}<span>${formatTokenCount(task.total_tokens)} tokens</span></span>
        ${task.created_at ? `<span title="创建时间">${icon("clock")}<span>${formatDateTime(task.created_at)}</span></span>` : ""}
      </div>
    </div>
    <div class="task-card-actions${readonlyBadge ? "" : " has-status"}">
      ${readonlyBadge ? "" : `<span class="status task-card-actions-status ${statusClass(task.status)}"><span class="task-card-actions-status-text">${statusLabel(task.status)}</span></span>`}
      ${archived ? `<span class="status warn">归档只读</span>` : task.read_only ? `<button type="button" data-retire-remote-task="${task.id}" class="icon-button danger" title="移除孤立只读任务">${icon("close")}</button><span class="status warn" title="所属设备：${escapeHTML(task.owner_device_id || "未知")}">只读</span>` : task.status === "completed" ? `<span class="status warn" title="任务已完成，需先重新打开才能编辑">已完成只读</span><button type="button" data-delete-task="${task.id}" class="icon-button" title="删除任务">${icon("close")}</button>` : `<button type="button" data-edit-task-title="${task.id}" class="icon-button" title="编辑标题">${icon("edit")}</button><button type="button" data-delete-task="${task.id}" class="icon-button" title="删除任务">${icon("close")}</button>`}
    </div>
  </article>`;
}

function filteredTasks(): Task[] {
  return state.tasks.filter(item => taskMatchesFilters(
    item,
    state.taskProjectFilters,
    state.taskStatusFilters,
    state.taskDeviceFilters,
  ));
}

function tasksPageHTML(): string {
  // Counts describe the whole set, so they come from the server aggregate
  // rather than the loaded page; a page-derived count is wrong as soon as the
  // list outgrows one page. Until the first response lands, show no numbers.
  const counts = state.taskFacetCounts;
  const projectOptions = state.projects.map(item => ({
    value: item.id, label: item.name, count: counts?.project?.[item.id],
  }));
  const groupedStatus = taskStatusGroupCounts(counts?.status || {});
  const statusOptions = (Object.keys(TASK_STATUS_GROUP_LABELS) as TaskStatusGroup[]).map(group => ({
    value: group, label: TASK_STATUS_GROUP_LABELS[group], count: groupedStatus[group],
  }));
  const deviceOptions = taskDeviceFilterOptions(counts?.device || {});
  const filtered = filteredTasks();
  const rows = filtered.map(item => taskCardHtml(item)).join("");
  const hiddenText = counts ? hiddenTaskStatusText(hiddenTaskStatusGroups(groupedStatus, state.taskStatusFilters)) : "";
  const totalTasks = counts ? Object.values(counts.status || {}).reduce((sum, value) => sum + value, 0) : 0;
  const countTitle = `符合筛选 ${filtered.length} 个 / 已加载 ${state.tasks.length} 个${counts ? ` / 全部 ${totalTasks} 个` : ""}；“加载更多”只增加已加载数量，筛选保持不变`;
  const noMatchHint = taskLoadMoreNoMatch
    ? `<div class="task-filter-hint" data-task-load-more-empty>${icon("filter")}<span>新加载的任务都不符合当前筛选${listPages.tasks.hasMore ? "，可继续加载或显示全部" : "，可显示全部"}</span>${state.taskStatusFilters.size && !hiddenText ? `<button type="button" data-task-show-all>显示全部</button>` : ""}</div>`
    : "";
  return `<section class="page">
    ${pageHead("任务", "Task 是长期目标，每条用户输入创建一个独立 Turn。", `<button data-dialog="task">${icon("plus")}创建任务</button>`)}
    <div class="task-filters">
      ${taskFilterPopover("project", "项目", "全部项目", projectOptions)}
      ${taskFilterPopover("status", "状态", "全部状态", statusOptions)}
      ${taskFilterPopover("device", "设备", "全部设备", deviceOptions)}
      <span class="task-count" title="${escapeHTML(countTitle)}">匹配 ${filtered.length} / 已加载 ${state.tasks.length}${counts ? ` / 全部 ${totalTasks}` : ""}</span>
    </div>
    ${hiddenText ? `<div class="task-filter-hint" data-task-hidden-status>${icon("filter")}<span>${escapeHTML(hiddenText)}</span><button type="button" data-task-show-all>显示全部</button></div>` : ""}
    <div class="task-list" tabindex="-1">${rows || `<div class="empty">${state.tasks.length ? "没有符合条件的任务" : "创建第一个任务开始执行。"}</div>`}${noMatchHint}${loadMoreHTML("tasks")}</div>
    ${taskDialog()}
  </section>`;
}

function tasksView(): string {
  if (state.selectedTask) return taskDetailView(state.selectedTask);
  return shell(tasksPageHTML());
}

interface TaskListFocus {
  task: string;
  tag: string;
  attributes: string;
  selectionStart?: number | null;
  selectionEnd?: number | null;
}

function taskListFocusAttributes(element: HTMLElement): string {
  return [...element.attributes]
    .filter(attribute => attribute.name.startsWith("data-"))
    .map(attribute => `${attribute.name}=${attribute.value}`)
    .sort()
    .join(";");
}

function captureTaskListFocus(root: HTMLElement): TaskListFocus | null {
  const active = document.activeElement;
  if (!(active instanceof HTMLElement) || !root.contains(active)) return null;
  const control = active as HTMLInputElement;
  return {
    task: active.closest<HTMLElement>("[data-task]")?.dataset.task || "",
    tag: active.tagName,
    attributes: taskListFocusAttributes(active),
    selectionStart: "selectionStart" in control ? control.selectionStart : undefined,
    selectionEnd: "selectionEnd" in control ? control.selectionEnd : undefined,
  };
}

function restoreTaskListFocus(root: HTMLElement, state: TaskListFocus | null): void {
  if (!state) return;
  const scopes = state.task
    ? [...root.querySelectorAll<HTMLElement>(`[data-task="${CSS.escape(state.task)}"]`)]
    : [root];
  for (const scope of scopes) {
    const candidates = [scope, ...scope.querySelectorAll<HTMLElement>("*")]
      .filter(element => element.tagName === state.tag && taskListFocusAttributes(element) === state.attributes);
    const target = candidates.find(element => !element.hasAttribute("disabled"));
    if (!target) continue;
    try { target.focus({preventScroll: true}); } catch { target.focus(); }
    const control = target as HTMLInputElement;
    if (state.selectionStart !== undefined && "setSelectionRange" in control) {
      try { control.setSelectionRange(state.selectionStart, state.selectionEnd ?? state.selectionStart); } catch { /* unsupported input type */ }
    }
    return;
  }
  // The row (or the focused control in it) was removed by the refresh. Keep
  // focus on the list container instead of letting it drop onto BODY.
  try { root.focus({preventScroll: true}); } catch { root.focus(); }
}

function patchTaskRows(list: HTMLElement, nextList: HTMLElement): void {
  const selector = ":scope > article[data-task]";
  const desired = [...nextList.querySelectorAll<HTMLElement>(selector)];
  const current = new Map([...list.querySelectorAll<HTMLElement>(selector)].map(row => [row.dataset.task || "", row]));
  const plan = planKeyedNodes(
    [...current].map(([key, row]) => ({key, signature: row.dataset.taskSignature || ""})),
    desired.map(row => ({key: row.dataset.task || "", signature: row.dataset.taskSignature || ""})),
  );
  const source = new Map(desired.map(row => [row.dataset.task || "", row]));
  const replace = new Set(plan.replace);
  const focus = captureTaskListFocus(list);
  for (const key of plan.remove) current.get(key)?.remove();
  let previous: Element | null = null;
  for (const key of plan.order) {
    const next = source.get(key);
    if (!next) continue;
    let row = current.get(key) || null;
    // Unchanged rows stay the exact same node. Only changed rows are replaced,
    // which is what keeps scroll and in-row focus stable across refreshes.
    if (row && replace.has(key)) {
      const replacement = next.cloneNode(true) as HTMLElement;
      row.replaceWith(replacement);
      row = replacement;
    }
    if (!row) {
      row = next.cloneNode(true) as HTMLElement;
      list.insertBefore(row, previous ? previous.nextSibling : list.firstElementChild);
    } else {
      const expected = previous ? previous.nextElementSibling : list.firstElementChild;
      if (row !== expected) list.insertBefore(row, previous ? previous.nextSibling : list.firstElementChild);
    }
    previous = row;
  }
  restoreTaskListFocus(list, focus);
}

function patchTaskListTail(list: HTMLElement, nextList: HTMLElement): void {
  const isRow = (element: Element) => element.matches("article[data-task]");
  const current = [...list.children].filter(element => !isRow(element));
  const desired = [...nextList.children].filter(element => !isRow(element));
  if (current.map(element => element.outerHTML).join("") === desired.map(element => element.outerHTML).join("")) return;
  const focus = captureTaskListFocus(list);
  current.forEach(element => element.remove());
  list.append(...desired.map(element => element.cloneNode(true)));
  restoreTaskListFocus(list, focus);
}

function patchTaskHiddenHint(section: HTMLElement, nextSection: HTMLElement): void {
  const current = section.querySelector<HTMLElement>("[data-task-hidden-status]");
  const desired = nextSection.querySelector<HTMLElement>("[data-task-hidden-status]");
  if (!desired) {
    current?.remove();
    return;
  }
  if (!current) {
    section.querySelector(".task-list")?.before(desired.cloneNode(true));
    return;
  }
  if (current.outerHTML !== desired.outerHTML) replaceInteractiveRegion(current, desired.innerHTML);
}

// performListRefresh reconciles only the task list's changed rows and chrome.
// Returning false means the expected list shell is absent (another view, or a
// detail just opened), and the caller falls back to a full render.
function patchTaskList(): boolean {
  if (state.selectedTask || openingTaskID) return false;
  const section = document.querySelector<HTMLElement>("section.page");
  const list = section?.querySelector<HTMLElement>(".task-list");
  const filters = section?.querySelector<HTMLElement>(".task-filters");
  if (!section || !list || !filters) return false;
  const template = document.createElement("template");
  template.innerHTML = tasksPageHTML();
  const nextSection = template.content.querySelector<HTMLElement>("section.page");
  const nextList = nextSection?.querySelector<HTMLElement>(".task-list");
  const nextFilters = nextSection?.querySelector<HTMLElement>(".task-filters");
  if (!nextSection || !nextList || !nextFilters) return false;

  // The filters popover holds open state, checkbox selection and focus. Swapping
  // only its inner HTML through the interactive-region helper restores all of
  // them, while the surrounding page and scroll position stay untouched.
  if (filters.outerHTML !== nextFilters.outerHTML) replaceInteractiveRegion(filters, nextFilters.innerHTML);

  const count = section.querySelector<HTMLElement>(".task-count");
  const nextCount = nextSection.querySelector<HTMLElement>(".task-count");
  if (count && nextCount && (count.textContent !== nextCount.textContent || count.title !== nextCount.title)) {
    count.textContent = nextCount.textContent;
    count.title = nextCount.title;
  }

  patchTaskHiddenHint(section, nextSection);
  patchTaskRows(list, nextList);
  patchTaskListTail(list, nextList);
  // Replaced rows, filter controls and the tail are fresh nodes; bind them now
  // so actions work immediately, without unsettling the untouched rows.
  bindTaskListControls(section);
  return true;
}

function taskDialog(): string {
  const projectID = state.projects.find(project => !project.channel_retired && !isSystemProject(project))?.id || "";
  const autoSkills = autoTaskSkillIDs(projectID);
  const mcp = renderMCPOptions(state.mcpServers, [], {excludeBuiltin: true});
  const agentConfig = renderAgentConfigForm({
    prefix: "task",
    models: state.models,
    accounts: state.codexAccounts,
    skills: state.skills,
    mcpServers: state.mcpServers,
    projectID,
    effort: "high",
    skillIDs: autoSkills,
    mcpIDs: [],
    fastMode: false,
    skillsBody: taskSkillOptions(projectID, autoSkills),
    skillsBodyID: "task-skill-options",
    mcpBody: mcp.body,
    mcpBodyID: "task-mcp-options",
    mcpTotal: mcp.total,
  });
  return `<dialog id="task-dialog" class="wide agent-config-dialog"><form id="task-form">
    <div class="dialog-head"><div><h2>创建任务</h2><small>模型与推理强度决定新 Agent 的执行方式，创建后仍可调整。</small></div><button type="button" data-close class="icon-button" aria-label="${t("common.closeDialog")}">${icon("close")}</button></div>
    <div class="dialog-body">
      <section class="agent-config-section">
        <h3>基本信息</h3>
        <label>标题<input name="title" required></label>
        <div class="two"><label>项目<select name="project_id" id="task-project" required>${state.projects.filter(item => !item.channel_retired && !isSystemProject(item)).map(item => `<option value="${item.id}">${escapeHTML(item.name)}</option>`).join("")}</select></label><label>Workspace<select name="workspace_id" id="task-workspace" required></select></label></div>
        <label>目标<textarea name="request" required></textarea></label>
      </section>
      ${agentConfig}
      <section class="agent-config-section agent-config-task">
        <h3>任务设置</h3>
        <div class="two"><label>协作模式<select name="collaboration_mode"><option value="auto">Auto</option><option value="single">Single</option></select></label><label>最大 Agent 数<input name="max_agents" type="number" min="1" value="3"></label></div>
        <label>Knowledge<select name="knowledge_policy"><option value="inherit">继承 Project</option><option value="enabled">开启</option><option value="disabled">关闭</option></select></label>
        <div id="task-git-isolation"><label>任务隔离<select name="isolation" id="task-isolation"><option value="inplace" selected>原地执行（默认）</option><option value="worktree">独立 Worktree</option></select></label><div id="task-worktree-settings"><label>Worktree 根目录<input name="worktree_dir" id="task-worktree-dir" placeholder="默认：仓库上一级/.aha2-worktrees"></label><div class="field-help">系统会在根目录下追加 Task ID；切换为原地执行后不使用此配置。</div></div><div class="two" id="task-branches"><label>目标分支<input name="target_branch" placeholder="默认当前分支"></label><label>任务分支<input name="task_branch" placeholder="aha/task-name"></label></div></div>
      </section>
    </div>
    <div class="dialog-actions agent-config-actions"><button type="button" data-close>取消</button><button type="submit" name="start_mode" value="manual">创建，稍后启动</button><button class="primary" type="submit" name="start_mode" value="immediate">创建任务</button></div>
  </form></dialog>`;
}

// Keeps each collapsed summary truthful about what is switched on inside it.
// Both dialogs share the same helpers, so the create panel and the Agent config
// dialog report counts in exactly the same words.
function syncMCPFoldCount(scope: string, id: string): void {
  const root = document.querySelector<HTMLElement>(`#${scope}`);
  if (!root) return;
  const selected = root.querySelectorAll<HTMLInputElement>(`#${id} input[name="mcp_server_ids"]`).length;
  const checked = root.querySelectorAll<HTMLInputElement>(`#${id} input[name="mcp_server_ids"]:checked`).length;
  const badge = root.querySelector<HTMLElement>(`#${id} [data-fold-count]`);
  if (badge) badge.textContent = checked ? `已选 ${checked}` : (selected ? "未选择" : "无可用");
}

function syncSkillsFoldCount(scope: string, id: string): void {
  const root = document.querySelector<HTMLElement>(`#${scope}`);
  if (!root) return;
  const selected = root.querySelectorAll<HTMLInputElement>(`#${id} input[name="skill_ids"]:checked`).length;
  const badge = root.querySelector<HTMLElement>(`#${id} [data-fold-count]`);
  if (badge) badge.textContent = selected ? `已选 ${selected}` : "未选择";
}

function syncFastFoldCount(scope: string, prefix: string): void {
  const root = document.querySelector<HTMLElement>(`#${scope}`);
  if (!root) return;
  const checked = root.querySelector<HTMLInputElement>(`#${prefix}-fast-mode-row input[name="fast_mode"]`)?.checked;
  const badge = root.querySelector<HTMLElement>(`#${prefix}-fast-fold [data-fold-count]`);
  if (badge) badge.textContent = checked ? "开" : "关";
}

// The advanced section carries one combined summary next to its title, matching
// the fold badges underneath: 技能 N · MCP N · Fast 开/关.
function syncAdvancedSummary(scope: string, prefix: string): void {
  const root = document.querySelector<HTMLElement>(`#${scope}`);
  if (!root) return;
  const skills = root.querySelectorAll<HTMLInputElement>(`#${prefix}-skills-fold input[name="skill_ids"]:checked`).length;
  const mcp = root.querySelectorAll<HTMLInputElement>(`#${prefix}-mcp-fold input[name="mcp_server_ids"]:checked`).length;
  const fast = Boolean(root.querySelector<HTMLInputElement>(`#${prefix}-fast-mode-row input[name="fast_mode"]`)?.checked);
  const summary = root.querySelector<HTMLElement>(`#${prefix}-advanced-summary`);
  if (!summary) return;
  if (!root.querySelector<HTMLElement>(`#${prefix}-skills-fold`)) {
    summary.textContent = `Fast ${fast ? "开" : "关"}`;
    return;
  }
  summary.textContent = agentConfigSummary(skills, mcp, fast);
}

function syncTaskFormFoldCounts(): void {
  syncSkillsFoldCount("task-dialog", "task-skills-fold");
  syncMCPFoldCount("task-dialog", "task-mcp-fold");
  syncFastFoldCount("task-dialog", "task");
  syncAdvancedSummary("task-dialog", "task");
}

function syncAgentFoldCounts(): void {
  syncSkillsFoldCount("agent-config-dialog", "agent-config-skills-fold");
  syncMCPFoldCount("agent-config-dialog", "agent-config-mcp-fold");
  syncFastFoldCount("agent-config-dialog", "agent-config");
  syncAdvancedSummary("agent-config-dialog", "agent-config");
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
  return `<dialog id="task-takeover-dialog" class="wide"><form id="task-takeover-form"><div class="dialog-head"><h2>接管只读 Task</h2><button type="button" data-close class="icon-button" aria-label="${t("common.closeDialog")}">${icon("close")}</button></div><p class="field-help">接管会创建新的本机 Task。请重新选择本机 Workspace，并确认所有设备专属路径和端点。</p><label>标题<input name="title" required value="${escapeHTML(detail.task.title)} (本机)"></label><label>需求<textarea name="request" required>${escapeHTML(detail.task.original_request || detail.task.current_goal || detail.task.title)}</textarea></label><label>本机 Workspace<select name="workspace_id" id="takeover-task-workspace" required>${workspaces.map(item => `<option value="${item.id}">${escapeHTML(item.name)} · ${escapeHTML(item.root_path)}</option>`).join("") || '<option value="">请先为此项目创建本机 Workspace</option>'}</select></label>${runtimeFieldsHTML("takeover-task", state.models, state.codexAccounts)}<div class="two"><label>推理强度<select name="reasoning_effort" id="takeover-task-effort"></select></label><label>文件访问<select name="filesystem"><option value="workspace-write">工作区可写</option><option value="read-only">只读</option><option value="danger-full-access">完全访问</option></select></label></div><div class="two"><label>协作模式<select name="collaboration_mode"><option value="auto" ${detail.task.collaboration_mode === "auto" ? "selected" : ""}>Auto</option><option value="single" ${detail.task.collaboration_mode === "single" ? "selected" : ""}>Single</option></select></label><label>最大 Agent 数<input name="max_agents" type="number" min="1" value="${Number(detail.task.max_agents || 3)}"></label></div><label>审批<select name="approval"><option value="never">无需确认</option><option value="auto">自动批准</option></select></label>${groups ? `<section><h3>本机硬件重绑定</h3>${groups}</section>` : ""}<div class="dialog-actions"><button type="button" data-close>取消</button><button class="primary" type="submit" ${workspaces.length ? "" : "disabled"}>创建本机 Task</button></div></form></dialog>`;
}

function taskChannelPanelHTML(): string {
  if (state.taskChannelLoading) {
    return `<div class="ctx-loading">${icon("spinner", true)}正在读取可用渠道...</div>`;
  }
  const result = state.taskChannelState;
  const taskID = state.selectedTask?.task.id || "";
  if (!result) {
    return `<div class="task-channel-empty"><strong>渠道信息尚未加载</strong><button type="button" id="reload-task-channel">${icon("refresh")}重新加载</button></div>`;
  }
  const current = result.route;
  const currentDestination = current ? result.destinations.find(item => item.conversation_id === current.conversation_id) : undefined;
  const options = result.destinations.map(item => {
    const kind = item.endpoint_kind === "group_digital_human" ? "群聊" : "私聊";
    const label = item.display_name || (kind === "群聊" ? "未命名群聊" : "Owner 私聊");
    const occupied = Boolean(item.target_task_id && item.target_task_id !== taskID);
    const occupiedBy = item.target_task_code || item.target_task_name || "其他 Task";
    const suffix = occupied ? ` · 将从 ${occupiedBy} 接管` : "";
    return `<option value="${escapeHTML(item.conversation_id)}" ${item.conversation_id === current?.conversation_id ? "selected" : ""}>${escapeHTML(item.instance_name)} · ${kind} · ${escapeHTML(label + suffix)}</option>`;
  }).join("");
  const switcher = `<form id="task-channel-route-form" class="task-channel-switcher">
    <label>绑定渠道<select name="conversation_id" required>${options || '<option value="">暂无可用渠道</option>'}</select></label>
    <button type="submit" class="primary" ${result.destinations.length ? "" : "disabled"}>${icon("link")}${current ? "切换" : "连接"}</button>
    ${current ? `<button type="button" id="disconnect-task-channel" class="icon-button danger-quiet" title="解除主渠道" aria-label="解除主渠道">${icon("close")}</button>` : ""}
  </form>`;
  if (!current || !currentDestination) {
    return `<div class="task-channel-tool">${switcher}<div class="task-channel-empty"><strong>未连接主渠道</strong><p>先在上方选择已登记的私聊或群聊。每个 Task 只能连接一个主渠道。</p></div></div>`;
  }
  if (currentDestination.endpoint_kind !== "group_digital_human") {
    return `<div class="task-channel-tool">${switcher}<section class="task-channel-private"><span class="channel-route-icon">${icon("bot")}</span><div><strong>${escapeHTML(currentDestination.display_name || "Owner 私聊")}</strong><small>${escapeHTML(currentDestination.instance_name)} · 私聊主渠道</small></div></section></div>`;
  }
  const selected = new Map(result.contacts.map(contact => [contact.identity_link_id, contact]));
  const memberRows = result.members.map(member => {
    const contact = selected.get(member.identity_link_id);
    return `<label class="task-channel-member ${member.is_bot ? "bot" : ""}">
      <input type="checkbox" name="contact_id" value="${escapeHTML(member.identity_link_id)}" data-member-name="${escapeHTML(member.display_name)}" data-member-bot="${member.is_bot}" data-member-role="${escapeHTML(contact?.collaboration_role || "")}" ${member.is_self ? "disabled" : ""} ${contact ? "checked" : ""}>
      <span>${icon(member.is_bot ? "bot" : "user")}<span><strong>${escapeHTML(member.display_name)}</strong><small>${member.is_self ? "当前渠道机器人" : member.is_bot ? "渠道已观察机器人" : member.source === "provider" ? "群成员" : "已观察成员"}</small></span></span>
    </label>`;
  }).join("");
  const selectedRows = result.contacts.map(contact => taskChannelSelectedContactHTML(contact.identity_link_id, contact.display_name, contact.is_bot, contact.collaboration_role || "")).join("");
  return `<div class="task-channel-tool">
    ${switcher}
    <div class="task-channel-group-head"><div><strong>${escapeHTML(currentDestination.display_name || "未命名群聊")}</strong><small>${escapeHTML(currentDestination.instance_name)} · ${result.members.length} 名已登记成员</small></div><button type="button" id="refresh-task-channel-members">${icon("refresh")}同步群成员</button></div>
    <form id="task-channel-contacts-form">
      <div class="task-channel-people">
        <section><header><strong>群聊成员</strong><small>勾选加入联调</small></header><div class="task-channel-member-list">${memberRows || '<div class="empty-state">暂无成员，点击“同步群成员”读取。</div>'}</div></section>
        <section><header><strong>可 @ 联调人</strong><small>Agent 只会看到这里的人</small></header><div class="task-channel-contact-list" data-task-channel-selected>${selectedRows || '<div class="empty-state">尚未选择联调人</div>'}</div></section>
      </div>
      <datalist id="task-channel-role-options"><option value="APP"><option value="云端"><option value="固件"><option value="测试"><option value="产品"></datalist>
      <footer><small>群成员来自渠道同步；观察其他机器人时，请在同一条群消息里同时 @ 当前渠道机器人和目标机器人。</small><button type="submit" class="primary">${icon("save")}保存联调人</button></footer>
    </form>
  </div>`;
}

function taskChannelSelectedContactHTML(identityID: string, name: string, isBot: boolean, role: string): string {
  return `<div class="task-channel-contact" data-contact-id="${escapeHTML(identityID)}"><span>${icon(isBot ? "bot" : "user")}<strong>${escapeHTML(name)}</strong></span><label>身份<input name="role_${escapeHTML(identityID)}" list="task-channel-role-options" maxlength="40" value="${escapeHTML(role)}" placeholder="APP / 云端 / 固件"></label></div>`;
}

async function reloadTaskChannelState(renderAfter = true): Promise<void> {
  if (!state.selectedTask) return;
  const taskID = state.selectedTask.task.id;
  const loadVersion = ++taskChannelLoadVersion;
  state.taskChannelLoading = true;
  if (renderAfter) render();
  try {
    const result = await api.taskChannelRoutes(taskID);
    if (loadVersion !== taskChannelLoadVersion || !state.selectedTask || state.selectedTask.task.id !== taskID) return;
    state.taskChannelState = result;
    state.taskChannelDestination = result.route
      ? result.destinations.find(item => item.conversation_id === result.route?.conversation_id) || null
      : null;
  } finally {
    if (loadVersion !== taskChannelLoadVersion) return;
    state.taskChannelLoading = false;
    if (renderAfter) render();
  }
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

function attachmentButtonTitle(): string {
  return `添加附件或直接粘贴图片（${taskAttachmentLimitHint()}）`;
}

function renderAttachmentComposer(disabled: boolean): string {
  const attachments = currentAttachmentDrafts();
  return `${attachmentTrayHTML(attachments)}<input id="message-attachment-input" type="file" multiple hidden ${disabled ? "disabled" : ""}><button id="message-attachment-pick" class="composer-attachment-button" type="button" title="${escapeHTML(attachmentButtonTitle())}" aria-label="${escapeHTML(`添加附件（${taskAttachmentLimitHint()}）`)}" ${disabled || attachments.length >= TASK_ATTACHMENT_MAX_COUNT ? "disabled" : ""}>${icon("attachment")}${attachments.length ? `<small>${attachments.length}</small>` : ""}</button>`;
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
  button.disabled = messageAttachmentUploadPending || attachments.length >= TASK_ATTACHMENT_MAX_COUNT;
  button.title = messageAttachmentUploadPending ? "正在上传附件" : attachmentButtonTitle();
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
  const validationError = taskAttachmentValidationError(drafts.length, files);
  if (validationError) {
    setMessage("error", validationError);
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
interface ConversationAnchor {
  key: string;
  /** The item's top relative to the scroll container's viewport. */
  viewportOffset: number;
}

interface ConversationViewport {
  scrollTop: number;
  clientHeight: number;
  atBottom: boolean;
  anchor: ConversationAnchor | null;
}

function conversationMessageNodes(list: HTMLElement | null): HTMLElement[] {
  return list ? [...list.querySelectorAll<HTMLElement>("[data-message-id]")] : [];
}

function conversationContentTop(): number {
  return conversationHeaderHeight + conversationPaddingTop;
}

function captureConversationAnchor(list: HTMLElement | null): ConversationAnchor | null {
  if (!list) return null;
  const listTop = list.getBoundingClientRect().top;
  for (const node of conversationMessageNodes(list)) {
    const rect = node.getBoundingClientRect();
    if (rect.bottom > listTop + 1) {
      const key = node.dataset.messageId || "";
      if (key) return {key, viewportOffset: rect.top - listTop};
    }
  }
  // A scrollbar drag can land inside a spacer with no row rendered around the
  // viewport. Anchor on the virtual row at that offset instead, so rebuilding
  // the window keeps the same content under the reader.
  const keys = conversationItemKeys(state.taskConversation);
  if (!keys.length) return null;
  let offset = Math.max(0, list.scrollTop - conversationContentTop());
  for (const key of keys) {
    const height = conversationHeights.get(key) ?? conversationVirtualEstimatePx;
    if (offset < height) return {key, viewportOffset: -offset};
    offset -= height;
  }
  const last = keys[keys.length - 1];
  return {key: last, viewportOffset: -(conversationHeights.get(last) ?? conversationVirtualEstimatePx)};
}

function restoreConversationAnchor(list: HTMLElement, anchor: ConversationAnchor | null): void {
  if (!anchor?.key) return;
  const node = conversationMessageNodes(list).find(item => item.dataset.messageId === anchor.key);
  if (node) {
    const delta = (node.getBoundingClientRect().top - list.getBoundingClientRect().top) - anchor.viewportOffset;
    if (Math.abs(delta) >= 0.5) list.scrollTop += delta;
    return;
  }
  const items = conversationRenderableItems(state.taskConversation);
  const index = items.findIndex(item => item.id === anchor.key);
  if (index < 0) return;
  let top = 0;
  for (let position = 0; position < index; position += 1) {
    top += conversationHeights.get(items[position].id) ?? conversationVirtualEstimatePx;
  }
  list.scrollTop = conversationContentTop() + top - anchor.viewportOffset;
}

function measureConversationChrome(list: HTMLElement): void {
  const header = list.querySelector<HTMLElement>("#load-older-conversation");
  let headerHeight = 0;
  if (header) {
    const style = window.getComputedStyle(header);
    headerHeight = header.getBoundingClientRect().height
      + (Number.parseFloat(style.marginTop) || 0)
      + (Number.parseFloat(style.marginBottom) || 0);
  }
  const paddingTop = Number.parseFloat(window.getComputedStyle(list).paddingTop) || 0;
  if (Math.abs(headerHeight - conversationHeaderHeight) > 0.5 || Math.abs(paddingTop - conversationPaddingTop) > 0.5) {
    conversationHeaderHeight = headerHeight;
    conversationPaddingTop = paddingTop;
  }
}

// Margins are folded into the measured height because the spacers must cover
// the same distance the normal flow would: message and status-card margins
// differ, and adjacent sibling margins do not collapse through a spacer.
function conversationNodeHeight(node: HTMLElement): number {
  const style = window.getComputedStyle(node);
  return node.getBoundingClientRect().height
    + (Number.parseFloat(style.marginTop) || 0)
    + (Number.parseFloat(style.marginBottom) || 0);
}

function measureConversationMessages(list: HTMLElement): boolean {
  let changed = false;
  for (const node of conversationMessageNodes(list)) {
    const key = node.dataset.messageId || "";
    if (!key) continue;
    const height = conversationNodeHeight(node);
    if (!Number.isFinite(height) || height <= 0) continue;
    const previous = conversationHeights.get(key);
    if (previous === undefined || Math.abs(previous - height) > 0.5) {
      conversationHeights.set(key, height);
      changed = true;
    }
  }
  return changed;
}

function captureConversationMessageState(list: HTMLElement | null): Map<string, {expanded: boolean; details: boolean[]}> {
  const captured = new Map<string, {expanded: boolean; details: boolean[]}>();
  for (const node of conversationMessageNodes(list)) {
    const key = node.dataset.messageId || "";
    if (!key) continue;
    const state = {
      expanded: node.classList.contains("expanded"),
      details: [...node.querySelectorAll("details")].map(detail => detail.open),
    };
    if (state.expanded || state.details.some(Boolean)) {
      captured.set(key, state);
      conversationMessageUI.set(key, state);
    } else {
      // The reader collapsed this row: drop the remembered state so recycling
      // cannot resurrect it later.
      conversationMessageUI.delete(key);
    }
  }
  return captured;
}

function restoreConversationMessageState(root: HTMLElement, captured: Map<string, {expanded: boolean; details: boolean[]}>): void {
  for (const node of conversationMessageNodes(root)) {
    const key = node.dataset.messageId || "";
    const state = captured.get(key) || conversationMessageUI.get(key);
    if (!state) continue;
    if (state.expanded && !node.classList.contains("expanded")) {
      node.classList.add("expanded");
      const preview = node.querySelector<HTMLElement>(".message-preview");
      const full = node.querySelector<HTMLElement>(".message-full-text");
      const toggle = node.querySelector<HTMLButtonElement>("[data-toggle-message]");
      if (preview) preview.hidden = true;
      if (full) full.hidden = false;
      if (toggle) toggle.textContent = "收起";
    }
    node.querySelectorAll("details").forEach((detail, index) => {
      if (state.details[index] !== undefined) detail.open = state.details[index];
    });
  }
}

function conversationFocusedMessageKey(): string {
  const active = document.activeElement;
  if (!(active instanceof Element)) return "";
  return active.closest<HTMLElement>("[data-message-id]")?.dataset.messageId || "";
}

function liveConversationViewport(list: HTMLElement | null): ConversationViewport {
  if (!list) {
    return {scrollTop: 0, clientHeight: Math.max(320, Math.round(window.innerHeight * 0.6)), atBottom: true, anchor: null};
  }
  return {
    scrollTop: list.scrollTop,
    clientHeight: list.clientHeight,
    atBottom: scrollConversationToBottom || (conversationFollowBottom && conversationIsAtBottom(list)),
    anchor: captureConversationAnchor(list),
  };
}

function conversationWindowOptions(viewport: ConversationViewport): ConversationWindowOptions {
  return {
    heights: conversationHeights,
    scrollTop: viewport.scrollTop,
    clientHeight: viewport.clientHeight,
    headerHeight: conversationHeaderHeight,
    paddingTop: conversationPaddingTop,
    overscanPx: conversationVirtualOverscanPx,
    estimateHeight: conversationVirtualEstimatePx,
    atBottom: viewport.atBottom,
    anchorKey: viewport.anchor?.key || "",
    pinnedKey: conversationFocusedMessageKey(),
  };
}

function conversationWindowFor(viewport: ConversationViewport): {window: ConversationWindow; count: number} {
  const keys = conversationItemKeys(state.taskConversation);
  return {window: conversationWindow(keys, conversationWindowOptions(viewport)), count: keys.length};
}

function conversationListHtml(viewport?: ConversationViewport): string {
  const list = document.querySelector<HTMLElement>("#conversation-list");
  return renderConversationList(
    state.taskConversation,
    state.selectedTask,
    state.taskConversationHasMore,
    conversationWindowOptions(viewport || liveConversationViewport(list)),
  );
}

function syncConversationSpacers(list: HTMLElement, window: ConversationWindow, count: number): void {
  const top = list.querySelector<HTMLElement>('[data-ui-key="conversation-spacer-top"]');
  const bottom = list.querySelector<HTMLElement>('[data-ui-key="conversation-spacer-bottom"]');
  if (window.start <= 0) top?.remove();
  else if (top) top.style.height = `${Math.max(0, window.topHeight)}px`;
  if (window.end >= count) bottom?.remove();
  else if (bottom) bottom.style.height = `${Math.max(0, window.bottomHeight)}px`;
}

function noteConversationWindowBounds(list: HTMLElement, window: ConversationWindow): void {
  list.dataset.windowStart = String(window.start);
  list.dataset.windowEnd = String(window.end);
}

function resetConversationVirtualState(): void {
  conversationHeights.clear();
  conversationMessageUI.clear();
  conversationUnreadMessages = 0;
  conversationHeaderHeight = 0;
  conversationPaddingTop = 0;
  if (conversationWindowFrame) window.cancelAnimationFrame(conversationWindowFrame);
  conversationWindowFrame = 0;
  if (conversationMeasureFrame) window.cancelAnimationFrame(conversationMeasureFrame);
  conversationMeasureFrame = 0;
  conversationResizeObserver?.disconnect();
}

function scheduleConversationWindowUpdate(): void {
  if (conversationWindowFrame) return;
  conversationWindowFrame = window.requestAnimationFrame(() => {
    conversationWindowFrame = 0;
    updateConversationWindow();
  });
}

function updateConversationWindow(force = false): void {
  const list = document.querySelector<HTMLElement>("#conversation-list");
  if (!list || state.taskDetailLoading) return;
  measureConversationChrome(list);
  const viewport = liveConversationViewport(list);
  const {window, count} = conversationWindowFor(viewport);
  const renderedStart = Number(list.dataset.windowStart ?? "-1");
  const renderedEnd = Number(list.dataset.windowEnd ?? "-1");
  if (!force && renderedStart === window.start && renderedEnd === window.end) {
    syncConversationSpacers(list, window, count);
    return;
  }
  const nextHTML = conversationListHtml(viewport);
  if (conversationRenderedHTML.get(list) === nextHTML) {
    noteConversationWindowBounds(list, window);
    syncConversationSpacers(list, window, count);
    return;
  }
  patchConversationList(list, nextHTML, window, true);
  conversationRenderedHTML.set(list, nextHTML);
  syncConversationNewMessagesHint();
}

// Growth that happened above the viewport shifts every row the reader is
// looking at, but the browser does not adjust scrollTop for it. The height
// cache still holds the previous size when the resize notification or image
// load arrives, so the shift can be applied before the anchor is captured.
// Doing it here rather than in the image handler also covers the case where
// the observer reports the image's new size before its load event runs.
function compensateConversationGrowthAboveViewport(list: HTMLElement): void {
  const listTop = list.getBoundingClientRect().top;
  let growth = 0;
  for (const node of conversationMessageNodes(list)) {
    const previous = conversationHeights.get(node.dataset.messageId || "");
    if (previous === undefined) continue;
    const delta = conversationNodeHeight(node) - previous;
    if (Math.abs(delta) < 0.5) continue;
    // Layout already reflects the change, so reconstruct where the row ended
    // before it: a row that overlapped the viewport then is the reader's
    // anchor row, its top did not move, and the existing anchor capture
    // handles it. Only a row entirely above the viewport shifts every visible
    // row.
    if (node.getBoundingClientRect().bottom - delta > listTop + 1) continue;
    growth += delta;
  }
  if (Math.abs(growth) >= 0.5) list.scrollTop += growth;
}

function refreshConversationMeasurements(): void {
  const list = document.querySelector<HTMLElement>("#conversation-list");
  if (!list || state.taskDetailLoading) return;
  measureConversationChrome(list);
  compensateConversationGrowthAboveViewport(list);
  const anchor = captureConversationAnchor(list);
  if (!measureConversationMessages(list)) return;
  const {window, count} = conversationWindowFor(liveConversationViewport(list));
  syncConversationSpacers(list, window, count);
  restoreConversationAnchor(list, anchor);
  scheduleConversationWindowUpdate();
}

function observeConversationRows(list: HTMLElement): void {
  // A long-lived observer can stop reporting after being disconnected and
  // re-observed on the same nodes within one turn (the browser deduplicates
  // the same-node notification), which leaves an expanded or image-grown row
  // unmeasured. Rebuild the observer so the next resize is delivered.
  conversationResizeObserver?.disconnect();
  const observer = new ResizeObserver(() => {
    if (conversationMeasureFrame) return;
    conversationMeasureFrame = window.requestAnimationFrame(() => {
      conversationMeasureFrame = 0;
      refreshConversationMeasurements();
    });
  });
  conversationResizeObserver = observer;
  observer.observe(list);
  conversationMessageNodes(list).forEach(node => observer.observe(node));
  watchConversationImages(list);
}

function syncConversationNewMessagesHint(): void {
  const button = document.querySelector<HTMLButtonElement>("#conversation-new-messages");
  if (!button) return;
  button.hidden = conversationUnreadMessages <= 0;
  if (conversationUnreadMessages > 0) {
    button.textContent = t("conversation.newMessages", {count: conversationUnreadMessages});
  }
}
function taskFailureBannerHtml(detail: TaskDetail): string {
  if (!["failed", "blocked"].includes(detail.task.status)) return "";
  const failure = [...(detail.turns || [])].reverse().find(turn => turn.status === "failed" || turn.status === "blocked");
  if (!failure) return "";
  return `<div class="task-failure-banner"><strong>失败原因</strong><span>${escapeHTML(failure.error || "Backend 执行失败，未返回详细原因")}</span><small>可直接发送下一条消息创建新 Round 重试</small></div>`;
}

function taskComposerFlags(detail: TaskDetail): {
  archivedReadOnly: boolean; remoteReadOnly: boolean; completed: boolean; readOnly: boolean; draft: boolean;
} {
  const archivedReadOnly = detail.task.channel_retired === true || detail.task.read_only_reason === "channel_retired";
  const remoteReadOnly = Boolean(detail.task.read_only && !archivedReadOnly);
  const completed = detail.task.status === "completed";
  return {archivedReadOnly, remoteReadOnly, completed, readOnly: archivedReadOnly || remoteReadOnly || completed, draft: detail.task.status === "draft"};
}

// A completed Task is frozen: the input box is replaced by a reopen affordance
// that still requires an explicit confirmation dialog before reopening.
function completedComposerHtml(detail: TaskDetail): string {
  const remoteReadOnly = Boolean(detail.task.read_only && detail.task.read_only_reason !== "channel_retired");
  if (remoteReadOnly) {
    return `<div class="composer-completed-hint">${icon("lock")}<div><strong>任务已完成</strong><small>该 Task 属于其他设备，本机只读，不能重新打开。</small></div></div>`;
  }
  return `<div class="composer-reopen-hint">${icon("lock")}<div><strong>任务已完成</strong><small>继续这个目标前需要先重新打开任务。</small></div></div><button type="button" id="reopen-task" class="primary composer-reopen-button">${icon("refresh")}<span>重新打开任务</span></button>`;
}

// The composer is marked invalid while it cannot accept input, which dims the
// input box and shows the reason banner.
function messageComposerInvalid(detail: TaskDetail, runtimeError = ""): boolean {
  const {readOnly, draft} = taskComposerFlags(detail);
  return !state.taskDetailLoading && Boolean(runtimeError || readOnly || draft);
}

// The composer is a <form> in every state except "completed": the send button is
// type=submit and the Enter key calls requestSubmit(), so the frozen state is the
// only one allowed to drop the form.
function messageComposerElement(detail: TaskDetail): "form" | "div" {
  return taskComposerFlags(detail).completed ? "div" : "form";
}

function messageComposerClass(detail: TaskDetail, options: {runtimeError?: string} = {}): string {
  const {completed} = taskComposerFlags(detail);
  if (completed) return "composer composer-completed";
  return `composer${messageComposerInvalid(detail, options.runtimeError) ? " runtime-invalid" : ""}`;
}

function messageComposerOpenTag(detail: TaskDetail, options: {runtimeError?: string} = {}): string {
  return `<${messageComposerElement(detail)} id="message-form" class="${messageComposerClass(detail, options)}">`;
}

function messageComposerHtml(detail: TaskDetail, options: {activeTurn?: boolean; runtimeError?: string} = {}): string {
  const {archivedReadOnly, remoteReadOnly, completed, readOnly, draft} = taskComposerFlags(detail);
  if (completed) return completedComposerHtml(detail);
  const runtimeError = options.runtimeError || "";
  const disabled = Boolean(state.taskDetailLoading || runtimeError || readOnly || draft);
  const warning = state.taskDetailLoading ? "" : draft ? "先启动任务后再发送消息"
    : archivedReadOnly ? "渠道实例已归档，此 Task 只读"
    : remoteReadOnly ? "该 Task 属于其他设备，本机只读" : runtimeError;
  const placeholder = state.taskDetailLoading ? "正在加载任务…" : draft ? "启动任务后可发送消息" : readOnly ? "只读 Task"
    : runtimeError || (options.activeTurn ? `${state.selectedTaskAgent} 正在执行，发送后将排队`
      : detail.task.status === "failed" ? "输入消息重试，或输入 /reopen" : `发送给 ${state.selectedTaskAgent}，输入 / 查看命令`);
  return `${warning ? `<div class="composer-runtime-warning">${escapeHTML(warning)}</div>` : ""}${renderComposerTools(detail, state.selectedTaskAgent, state.taskCategories, state.taskConversation.length)}<div id="slash-command-menu" class="slash-command-menu" ${matchingTaskSlashCommands(state.taskDraft).length ? "" : "hidden"}>${slashCommandMenuHtml(state.taskDraft)}</div>${renderAttachmentComposer(disabled)}<textarea name="content" placeholder="${escapeHTML(placeholder)}" ${disabled ? "disabled" : ""}>${escapeHTML(state.taskDraft)}</textarea><button id="message-send" class="primary" aria-label="发送" ${disabled ? "disabled" : ""}>${icon("send")}<span class="send-label">发送</span></button>`;
}

// Rebuilds the composer as a whole node so the element type, event bindings and
// frozen class all follow the Task status. The node is built from the same
// messageComposerOpenTag/messageComposerHtml pair the first render uses, so the
// live path and a full render cannot drift apart.
function messageComposerNode(detail: TaskDetail, options: {activeTurn?: boolean; runtimeError?: string} = {}): HTMLElement {
  const template = document.createElement("template");
  template.innerHTML = `${messageComposerOpenTag(detail, options)}${messageComposerHtml(detail, options)}</${messageComposerElement(detail)}>`;
  return template.content.firstElementChild as HTMLElement;
}

// Completion can arrive over SSE, rebuilding only part of the screen. The frozen
// composer, its reopen dialog and the turn placeholder are reconciled here so a
// Task that finishes (or is reopened) while the page is open stays consistent
// without waiting for a full render.
function syncCompletedTaskFrozen(detail: TaskDetail): void {
  const completed = detail.task.status === "completed";
  const composer = document.querySelector<HTMLElement>("#message-form");
  if (composer && completed !== composer.classList.contains("composer-completed")) {
    const focusedBefore = document.activeElement === composer || document.activeElement === document.body;
    composer.replaceWith(messageComposerNode(detail));
    bindMessageComposer();
    // Replacing a focused node drops focus, so hand it back after a reopen.
    if (focusedBefore) document.querySelector<HTMLTextAreaElement>("#message-form textarea")?.focus();
  }
  let dialog = document.querySelector<HTMLDialogElement>("#reopen-task-dialog");
  if (completed && !dialog) {
    document.querySelector("#task-screen-dialogs")?.insertAdjacentHTML("beforeend", reopenTaskDialog(detail));
    dialog = document.querySelector<HTMLDialogElement>("#reopen-task-dialog");
  } else if (!completed && dialog) {
    dialog.remove();
  }
  bindReopenTaskControls();
  const frozenSlot = document.querySelector<HTMLElement>("#task-frozen-slot");
  if (frozenSlot) frozenSlot.innerHTML = completedTaskBannerHtml(detail);
  const turnSlot = document.querySelector<HTMLElement>("#agent-turn-slot");
  const nextTurn = renderAgentTurnCard(detail, state.taskRealtimeState, state.taskContext?.context.metrics);
  if (turnSlot && turnSlot.innerHTML !== nextTurn) turnSlot.innerHTML = nextTurn;
}

function reopenTaskDialog(detail: TaskDetail): string {
  return `<dialog id="reopen-task-dialog"><form id="reopen-task-form"><div class="dialog-head"><h2>重新打开任务</h2><button type="button" data-close class="icon-button" aria-label="${t("common.closeDialog")}">${icon("close")}</button></div><div class="dialog-body"><p class="field-help">${escapeHTML(detail.task.code || detail.task.title)}</p><p>重新打开后 Task 回到「等待处理」状态，可以继续编辑配置和发送消息。</p><p class="dialog-warning">已完成的记录不会被删除，但新消息会开启新的 Round。</p></div><div class="dialog-actions"><button type="button" data-close>取消</button><button type="submit" class="primary">确认重新打开</button></div></form></dialog>`;
}

// Explains in the frozen state why input is gone and what the way forward is.
function completedTaskBannerHtml(detail: TaskDetail): string {
  if (detail.task.status !== "completed") return "";
  return `<div class="task-failure-banner task-completed-banner"><strong>任务已完成</strong><span>已完成的任务禁止编辑和继续输入，历史内容仍可查看和导出。</span><small>需要继续这个目标时，先重新打开任务再发送消息。</small></div>`;
}

function visibleTaskPermissionRequests(items: TaskPermissionRequest[]): TaskPermissionRequest[] {
  return items.filter(item => item.status === "pending" || item.status === "approved" || item.status === "rejected" || item.status === "expired");
}

function taskPermissionPendingCount(detail: TaskDetail): number {
  return (detail.permission_requests || []).filter(item => item.status === "pending").length;
}

function taskPermissionRiskLabel(risk?: string): string {
  if (risk === "high") return "高";
  if (risk === "medium") return "中";
  if (risk === "low") return "低";
  return risk || "";
}

// Requests are collapsed rows so a long history does not take over the panel.
// Expanding one shows the full request and, when it is still pending, the
// Owner decision buttons.
function taskPermissionCardsHtml(items: TaskPermissionRequest[]): string {
  return visibleTaskPermissionRequests(items).slice(0, 12).map(item => {
    const pending = item.status === "pending";
    const status = item.status === "approved" ? "已批准" : item.status === "rejected" ? "已拒绝" : item.status === "expired" ? "已过期" : "待审批";
    const statusClass = item.status === "approved" ? "approved" : item.status === "rejected" ? "rejected" : item.status === "expired" ? "expired" : "pending";
    const applicant = item.applicant_name || "群成员";
    const action = item.action || "未填写动作";
    const risk = taskPermissionRiskLabel(item.risk);
    const actions = pending
      ? `<div class="task-permission-actions"><button type="button" class="primary" data-task-permission-decision="approve" data-task-permission-id="${escapeHTML(item.id)}">批准</button><button type="button" data-task-permission-decision="reject" data-task-permission-id="${escapeHTML(item.id)}">拒绝</button></div>`
      : "";
    return `<details class="task-permission-item ${pending ? "pending" : ""}" data-task-permission-request="${escapeHTML(item.id)}">
      <summary>
        <span class="task-permission-summary"><strong>${escapeHTML(applicant)}${item.applicant_is_bot ? "（Bot）" : ""}</strong><span class="task-permission-summary-action">${escapeHTML(action)}</span></span>
        <span class="task-permission-status ${statusClass}">${escapeHTML(status)}</span>
      </summary>
      <div class="task-permission-detail">
        <p><strong>动作</strong><span>${escapeHTML(action)}</span></p>
        ${item.scope ? `<p><strong>范围</strong><span>${escapeHTML(item.scope)}</span></p>` : ""}
        ${item.reason ? `<p><strong>原因</strong><span>${escapeHTML(item.reason)}</span></p>` : ""}
        ${risk ? `<p><strong>风险</strong><span>${escapeHTML(risk)}</span></p>` : ""}
        ${item.decision_reason ? `<p><strong>审批说明</strong><span>${escapeHTML(item.decision_reason)}</span></p>` : ""}
        <p><strong>申请时间</strong><span>${escapeHTML(formatDateTime(item.created_at))}${item.decider_name ? ` · ${escapeHTML(item.decider_name)}` : ""}</span></p>
        ${actions}
      </div>
    </details>`;
  }).join("");
}

// The toolbar panel stays reachable even when there are no requests, so the
// entry point never disappears from the Task toolbar.
function taskPermissionPanelHtml(detail: TaskDetail): string {
  const cards = taskPermissionCardsHtml(detail.permission_requests || []);
  return `<section class="task-permission-requests panel-permissions"><div class="task-permission-head"><strong>权限申请</strong><small>点击申请查看详情；批准只对这一次动作有效。</small></div>${cards || `<div class="task-permission-empty">当前没有权限申请记录。</div>`}</section>`;
}

// SSE refreshes only patch the conversation. Keep the toolbar badge in sync so
// a request created while the Task is open is visible without reloading.
function syncTaskPermissionIndicators(detail: TaskDetail): void {
  const button = document.querySelector<HTMLButtonElement>('[data-task-tool="permissions"]');
  if (!button) return;
  const pending = taskPermissionPendingCount(detail);
  const badge = button.querySelector<HTMLElement>(".task-tool-badge");
  if (pending > 0) {
    const label = `权限申请（${pending} 条待审批）`;
    button.classList.add("has-pending");
    button.title = label;
    button.setAttribute("aria-label", label);
    if (badge) badge.textContent = pending > 99 ? "99+" : String(pending);
    else button.insertAdjacentHTML("beforeend", `<span class="task-tool-badge">${pending > 99 ? "99+" : pending}</span>`);
  } else {
    button.classList.remove("has-pending");
    button.title = "权限申请";
    button.setAttribute("aria-label", "权限申请");
    badge?.remove();
  }
}

// The mail tool is the only entry point; mirror the pending approval count on
// its toolbar button so a mail_* SSE event is visible without a full render.
function syncTaskMailIndicator(): void {
  const button = document.querySelector<HTMLButtonElement>('[data-task-tool="mail"]');
  if (!button) return;
  const pending = taskMailPendingCount();
  const badge = button.querySelector<HTMLElement>(".task-tool-badge");
  const label = pending > 0 ? `邮件（${pending} 条待审批）` : "邮件";
  button.classList.toggle("has-pending", pending > 0);
  button.title = label;
  button.setAttribute("aria-label", label);
  if (pending > 0) {
    if (badge) badge.textContent = pending > 99 ? "99+" : String(pending);
    else button.insertAdjacentHTML("beforeend", `<span class="task-tool-badge">${pending > 99 ? "99+" : pending}</span>`);
  } else {
    badge?.remove();
  }
}

function taskCtxHtml(): string {
  if (state.selectedTask?.task.status === "draft") {
    return `<div class="task-tool-placeholder">${icon("context")}<h3>尚无 Context</h3><p>启动任务后将生成 Agent Context。</p></div>`;
  }
  const value = state.taskContext;
  if (!value) return `<div class="ctx-loading">${icon("refresh")}正在加载 Context...</div>`;
  const context = value.context || {};
  const sessionActionDisabled = state.selectedTask?.task.status === "completed"
    || Boolean(state.selectedTask?.turns.some(turn => turn.agent_id === state.selectedTaskAgent && isActiveTurn(turn.status)));
  return `<div class="ctx-view">
    ${renderContextMetrics(context, sessionActionDisabled)}
    <details class="prompt-snapshot" open><summary>查看本轮 Prompt Snapshot</summary><div class="prompt-snapshot-body markdown-body">${renderMarkdown(context.prompt || "尚无 Prompt Snapshot")}</div></details>
  </div>`;
}

function taskMailActions(): {reload: () => Promise<void>; notify: (type: "error" | "notice", message: string) => void} {
  return {
    notify: setMessage,
    reload: async () => {
      const taskID = state.selectedTask?.task.id;
      if (!taskID) return;
      mailRefreshRequested = true;
      await refreshTaskRuntime(taskID);
      render();
    },
  };
}

function taskMailPanelHTML(detail: TaskDetail): string {
  // Other task tools receive this string but ignore it. Building it eagerly
  // would fetch the mail chunk the first time any task detail opens.
  if (state.taskTool !== "mail") return "";
  return renderTaskMailPanel(detail, state.taskMailOptions, state.mailAccounts, state.taskMailLoading, state.taskMailError);
}

function taskMailPendingCount(): number {
  return (state.taskMailOptions?.messages || []).filter(card => card.message.status === "pending_approval").length;
}

function taskDetailView(detail: TaskDetail): string {
  const loadingDetail = state.taskDetailLoading;
  const {archivedReadOnly, remoteReadOnly, completed, readOnly} = taskComposerFlags(detail);
  const draft = detail.task.status === "draft";
  const activeTurn = (detail.turns || []).find(item => item.agent_id === state.selectedTaskAgent && isActiveTurn(item.status));
  const selectedAgent = detail.agents.find(item => item.agent_id === state.selectedTaskAgent);
  const runtimeError = selectedAgent?.runtime_config_valid === false
    ? (selectedAgent.runtime_config_error || "模型配置不可用，请修改模型配置") : "";
  const project = state.projects.find(item => item.id === detail.task.project_id);
  const workspace = state.workspaces.find(item => item.id === detail.task.workspace_id);
  const taskMeta = `${project?.name || "-"} · ${workspace?.name || "-"} · ${detail.task.collaboration_mode || "auto"} · ${detail.task.max_agents || 3} Agents`;
  const chat = `<section class="conversation">
    ${archivedReadOnly ? `<div class="task-failure-banner remote-readonly"><strong>渠道归档只读</strong><span>该渠道实例已归档</span><small>历史内容可查看和导出，不能继续执行或修改</small></div>` : remoteReadOnly ? `<div class="task-failure-banner remote-readonly"><strong>其他设备的只读 Task</strong><span>所属设备：${escapeHTML(detail.task.owner_device_id || "未知")}</span><small>可查看同步历史，不能在本机执行或修改</small><div class="remote-readonly-actions"><button type="button" id="takeover-task">${icon("copy")}接管到本机</button><button type="button" class="danger" data-retire-remote-task="${detail.task.id}">${icon("close")}移除孤立任务</button></div></div>` : ""}
    <div id="task-failure-slot">${taskFailureBannerHtml(detail)}</div>
    ${draft ? `<div class="task-failure-banner task-draft-banner"><strong>任务尚未启动</strong><span>可以先配置 Agent、Skill 和硬件调试，准备好后再启动。</span><button type="button" id="start-task" class="primary">${icon("play")}启动任务</button></div>` : ""}
    <div class="conversation-stream"><div class="messages" id="conversation-list">${loadingDetail ? `<div class="task-detail-loading" role="status">${icon("spinner", true)}<strong>正在打开任务</strong><small>加载最近 ${initialConversationPageSize} 条消息…</small></div>` : conversationListHtml()}</div><button type="button" id="conversation-new-messages" class="conversation-new-messages" hidden>${t("conversation.newMessagesFallback")}</button><button class="command-palette-fab task-search-fab" data-command-palette-open aria-label="${t("palette.search")}">${icon("search")}</button></div>
    <div id="agent-turn-slot">${renderAgentTurnCard(detail, state.taskRealtimeState, state.taskContext?.context.metrics)}</div>
    <div id="task-frozen-slot">${completedTaskBannerHtml(detail)}</div>
    ${messageComposerOpenTag(detail, {runtimeError})}${messageComposerHtml(detail, {activeTurn: Boolean(activeTurn), runtimeError})}</${messageComposerElement(detail)}>
  </section>`;
  const toolLayout = state.taskTool ? ` task-tool-open task-tool-${state.taskToolMode}` : "";
  const channelVisible = !readOnly && !detail.task.channel_instance_id;
  return shell(`<section class="task-screen">
    <header class="task-head"><button id="back-tasks">←</button><div class="task-title-block"><h1><span class="task-code">${escapeHTML(detail.task.code || "")}</span><span class="task-title-text">${escapeHTML(detail.task.title)}</span></h1><div class="task-head-subline"><span class="task-head-meta" title="${escapeHTML(taskMeta)}">${escapeHTML(taskMeta)}</span><span id="task-detail-status" class="status ${statusClass(detail.task.status)}">${statusLabel(detail.task.status)}</span><span class="task-branch">${escapeHTML(detail.task.task_branch || "")}</span><span id="task-review-slot">${renderTaskReviewDecision(detail.task, tr)}</span></div></div><div class="actions task-tool-actions">${renderTaskToolButtons(state.taskTool, {includeChannel: channelVisible, channelConnected: Boolean(state.taskChannelDestination), channelLabel: taskChannelButtonLabel(state.taskChannelDestination), pendingPermissions: taskPermissionPendingCount(detail), pendingMail: taskMailPendingCount()})}</div></header>
    <div class="task-grid${toolLayout}" style="--task-tool-width:${state.taskToolWidth}%">
      ${chat}
      ${renderTaskToolPanel(state.taskTool, detail, state.selectedTaskAgent, taskCtxHtml(), state.taskToolMode, taskChannelPanelHTML(), state.taskToolPinned === state.taskTool, taskPermissionPanelHtml(detail), taskMailPanelHTML(detail))}
    </div>
    ${renderAgentConfigDialog(detail, state.selectedTaskAgent, state.models, state.codexAccounts, availableTaskSkills(detail.task.project_id, detail.task.skill_ids || []), state.mcpServers)}${taskTakeoverDialog(detail)}<div id="task-screen-dialogs">${completed ? reopenTaskDialog(detail) : ""}</div>
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
  // An explicit request means the reader wants to be at the bottom from here on,
  // so following resumes even if they had scrolled away before.
  conversationFollowBottom = true;
  composingConversationTouch = false;
  conversationUnreadMessages = 0;
  conversationBottomPinVersion++;
  syncConversationNewMessagesHint();
}

function cancelConversationBottomPin(): void {
  conversationBottomPinVersion++;
  conversationAutoScrollBlockedUntil = Date.now() + 800;
  composerFocusWasAtBottom = false;
}

// A gesture that reaches for the scrollback stops the view from following new
// content until the reader comes back to the bottom. This is deliberately not a
// timeout: a phone drag lasts as long as it lasts, and an expiry that fires
// mid-gesture hands the list back to the auto-scroll the reader is fighting.
function beginConversationGesture(): void {
  composingConversationTouch = true;
  conversationUserScrollUntil = Date.now() + conversationUserScrollWindowMs;
  cancelConversationBottomPin();
}

function noteConversationWheel(): void {
  conversationUserScrollUntil = Date.now() + conversationUserScrollWindowMs;
  cancelConversationBottomPin();
}

function endConversationGesture(): void {
  composingConversationTouch = false;
  const list = document.querySelector<HTMLElement>("#conversation-list");
  if (list && conversationIsAtBottom(list)) {
    conversationFollowBottom = true;
    conversationUnreadMessages = 0;
    syncConversationNewMessagesHint();
  }
}

function conversationIsAtBottom(list: HTMLElement): boolean {
  return list.scrollHeight - list.scrollTop - list.clientHeight < 80;
}

function restoreComposerConversationBottom(): void {
  if (!composerFocusWasAtBottom || !document.body.classList.contains("composer-focused")) return;
  const list = document.querySelector<HTMLElement>("#conversation-list");
  if (list) stabilizeConversationBottom(list);
}

function shouldAutoScrollConversation(wasAtBottom: boolean): boolean {
  // An explicit request (opening a task, sending a message) always wins: the
  // reader asked to be taken to the bottom.
  if (scrollConversationToBottom) return true;
  if (composingConversationTouch) return false;
  if (!conversationFollowBottom) return false;
  return wasAtBottom && Date.now() >= conversationAutoScrollBlockedUntil;
}

function stabilizeConversationBottom(list: HTMLElement): void {
  const pinVersion = ++conversationBottomPinVersion;
  const apply = () => {
    if (pinVersion !== conversationBottomPinVersion || !list.isConnected) return;
    list.scrollTop = list.scrollHeight;
  };
  apply();
  window.requestAnimationFrame(() => {
    apply();
    window.requestAnimationFrame(apply);
  });
  for (const delay of [80, 240, 600]) {
    window.setTimeout(apply, delay);
  }
}

const conversationImageLoadBound = new WeakSet<HTMLImageElement>();

function watchConversationImages(list: HTMLElement): void {
  list.querySelectorAll<HTMLImageElement>("img").forEach(image => {
    if (conversationImageLoadBound.has(image)) return;
    // Do not gate on `complete`: a lazy image can report complete before the
    // browser has queued its request, and the load event then arrives with no
    // listener attached.
    conversationImageLoadBound.add(image);
    image.addEventListener("load", () => conversationImageLoaded(image), {once: true});
  });
}

function conversationImageLoaded(image: HTMLImageElement): void {
  const list = document.querySelector<HTMLElement>("#conversation-list");
  if (!list?.isConnected || !image.isConnected) return;
  // Only an image that occupies space above the reader's viewport pushes every
  // row the reader is looking at downwards.
  const grewAboveViewport = image.getBoundingClientRect().top < list.getBoundingClientRect().top + 1;
  refreshConversationMeasurements();
  observeConversationRows(list);
  if (!grewAboveViewport || !conversationFollowBottom || composingConversationTouch) return;
  // Measurement shifts the scroll offset with the growth, so a reader who was
  // exactly at the bottom stays there and one inside the follow band stays at
  // the same distance. Re-pin anyway: this is not a new message, so the short
  // post-gesture suppression window does not apply, and the new pin version
  // invalidates any older pending pin.
  stabilizeConversationBottom(list);
}

function captureConversationImages(list: HTMLElement | null): Map<string, HTMLImageElement[]> {
  const images = new Map<string, HTMLImageElement[]>();
  list?.querySelectorAll<HTMLButtonElement>("[data-image-preview]").forEach(button => {
    const key = button.dataset.imagePreview || "";
    const image = button.querySelector<HTMLImageElement>("img");
    if (key && image) images.set(key, [...(images.get(key) || []), image]);
  });
  return images;
}

function restoreConversationImages(list: HTMLElement, images: Map<string, HTMLImageElement[]>): void {
  list.querySelectorAll<HTMLButtonElement>("[data-image-preview]").forEach(button => {
    const image = button.querySelector<HTMLImageElement>("img");
    const preserved = images.get(button.dataset.imagePreview || "")?.shift();
    if (image && preserved && image.getAttribute("src") === preserved.getAttribute("src")) {
      image.replaceWith(preserved);
    }
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
  // Search boxes opt into full re-renders: the region restore puts focus and
  // caret back, so filtering can update while the user is still typing.
  if (active.dataset.allowRerender === "true") return false;
  if (active instanceof HTMLTextAreaElement || active.isContentEditable) return true;
  if (!(active instanceof HTMLInputElement)) return false;
  return !["button", "checkbox", "color", "file", "hidden", "image", "radio", "range", "reset", "submit"].includes(active.type);
}

function conversationNodeKey(element: Element): string {
  if (!(element instanceof HTMLElement)) return "";
  if (element.dataset.messageId) return `message:${element.dataset.messageId}`;
  if (element.id) return `id:${element.id}`;
  if (element.dataset.uiKey) return `ui:${element.dataset.uiKey}`;
  return "";
}

function conversationNodeSignature(element: Element): string {
  if (element instanceof HTMLElement) {
    // Content signatures ignore local view state (expanded message, open
    // output details); a node is only replaced when the server data changed.
    if (element.dataset.messageSignature) return element.dataset.messageSignature;
    if (element.id) return `id:${element.id}`;
  }
  return element.outerHTML;
}

// Live SSE updates reconcile the conversation per message: unchanged messages
// keep their DOM nodes (expanded state, selected text, image elements), and an
// append-only update only inserts the new message. Replacing the whole list
// used to rebuild every row on each token update.
function patchConversationList(
  list: HTMLElement,
  nextHTML: string,
  windowInfo?: ConversationWindow,
  scrollDriven = false,
): void {
  const template = document.createElement("template");
  template.innerHTML = nextHTML;
  const desired = [...template.content.children];
  const current = [...list.children];
  const currentByKey = new Map<string, Element>();
  const currentState: Array<{key: string; signature: string}> = [];
  const desiredByKey = new Map<string, Element>();
  const desiredState: Array<{key: string; signature: string}> = [];
  let currentUnkeyed = 0;
  let desiredUnkeyed = 0;
  for (const node of current) {
    // Items without their own identity fall back to their position among the
    // unkeyed nodes (the empty state), which keeps the keyed messages around it
    // stable.
    const key = conversationNodeKey(node) || `~${currentUnkeyed++}`;
    currentByKey.set(key, node);
    currentState.push({key, signature: conversationNodeSignature(node)});
  }
  for (const node of desired) {
    const key = conversationNodeKey(node) || `~${desiredUnkeyed++}`;
    desiredByKey.set(key, node);
    desiredState.push({key, signature: conversationNodeSignature(node)});
  }
  const plan = planKeyedNodes(currentState, desiredState);
  const replace = new Set(plan.replace);
  const focus = captureRegionUI(list);
  const messageUI = captureConversationMessageState(list);
  const images = captureConversationImages(list);
  const anchor = captureConversationAnchor(list);
  const previousTop = list.scrollTop;
  const wasAtBottom = conversationIsAtBottom(list);
  const autoScroll = !scrollDriven && shouldAutoScrollConversation(wasAtBottom);
  let changed = plan.replace.length > 0 || plan.insert.length > 0 || plan.remove.length > 0;
  for (const key of plan.remove) currentByKey.get(key)?.remove();
  let previous: Element | null = null;
  for (const key of plan.order) {
    const source = desiredByKey.get(key);
    if (!source) continue;
    let node = currentByKey.get(key) || null;
    if (node && replace.has(key)) {
      const replacement = source.cloneNode(true) as Element;
      node.replaceWith(replacement);
      node = replacement;
    }
    if (!node) {
      node = source.cloneNode(true) as Element;
      list.insertBefore(node, previous ? previous.nextSibling : list.firstElementChild);
    } else {
      const expected = previous ? previous.nextElementSibling : list.firstElementChild;
      if (node !== expected) {
        list.insertBefore(node, previous ? previous.nextSibling : list.firstElementChild);
        changed = true;
      }
    }
    previous = node;
  }
  if (windowInfo) noteConversationWindowBounds(list, windowInfo);
  if (!changed) {
    if (scrollConversationToBottom) stabilizeConversationBottom(list);
    observeConversationRows(list);
    return;
  }
  restoreConversationImages(list, images);
  restoreRegionUI(list, focus, false);
  restoreConversationMessageState(list, messageUI);
  measureConversationChrome(list);
  measureConversationMessages(list);
  const renderedWindow = windowInfo || conversationWindowFor(liveConversationViewport(list)).window;
  syncConversationSpacers(list, renderedWindow, conversationItemKeys(state.taskConversation).length);
  if (autoScroll) stabilizeConversationBottom(list);
  else {
    // Restoring the anchored row keeps the same content under the reader when a
    // prepend, a live insert, or a spacer recalculation changes the heights
    // above it. Unlike restoring the previous pixel offset, this does not fight
    // an in-progress drag.
    restoreConversationAnchor(list, anchor);
    if (!anchor) list.scrollTop = previousTop;
  }
  observeConversationRows(list);
  const after = conversationWindowFor(liveConversationViewport(list)).window;
  if (!windowInfo || after.start !== windowInfo.start || after.end !== windowInfo.end) {
    scheduleConversationWindowUpdate();
  }
  bindConversationLiveControls();
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
    measureConversationChrome(list);
    const viewport = liveConversationViewport(list);
    const {window} = conversationWindowFor(viewport);
    const nextHTML = conversationListHtml(viewport);
    if (conversationRenderedHTML.get(list) !== nextHTML) {
      patchConversationList(list, nextHTML, window);
      conversationRenderedHTML.set(list, nextHTML);
    } else if (scrollConversationToBottom) {
      stabilizeConversationBottom(list);
    }
    syncConversationNewMessagesHint();
    scrollConversationToBottom = false;
  }
  const turnSlot = document.querySelector<HTMLElement>("#agent-turn-slot");
  if (turnSlot) replaceRegionHTML(turnSlot, renderAgentTurnCard(detail, state.taskRealtimeState, state.taskContext?.context.metrics));
  const failureSlot = document.querySelector<HTMLElement>("#task-failure-slot");
  const nextFailure = taskFailureBannerHtml(detail);
  if (failureSlot && failureSlot.innerHTML !== nextFailure) failureSlot.innerHTML = nextFailure;
  syncTaskPermissionIndicators(detail);
  syncTaskMailIndicator();
  const agentSelect = document.querySelector<HTMLSelectElement>("#composer-agent");
  if (!detail.agents.some(agent => agent.agent_id === state.selectedTaskAgent)) {
    state.selectedTaskAgent = detail.agents.some(agent => agent.agent_id === "main")
      ? "main"
      : (detail.agents[0]?.agent_id || "main");
  }
  const nextAgentOptions = renderComposerAgentOptions(detail, state.selectedTaskAgent);
  const selectedOptionExists = Boolean(agentSelect && [...agentSelect.options].some(option => option.value === state.selectedTaskAgent));
  if (agentSelect && (document.activeElement !== agentSelect || !agentSelect.options.length || !selectedOptionExists) && agentSelect.innerHTML !== nextAgentOptions) {
    agentSelect.innerHTML = nextAgentOptions;
  }
  const reviewSlot = document.querySelector<HTMLElement>("#task-review-slot");
  if (reviewSlot) replaceRegionHTML(reviewSlot, renderTaskReviewDecision(detail.task, tr));
  const taskStatus = document.querySelector<HTMLElement>("#task-detail-status");
  if (taskStatus) {
    taskStatus.className = `status ${statusClass(detail.task.status)}`;
    taskStatus.textContent = statusLabel(detail.task.status);
  }
  const toolBody = document.querySelector<HTMLElement>("#task-tool-panel-body");
  if (toolBody && state.taskTool === "hardware") {
    refreshHardwarePanel(detail, setMessage);
  } else if (toolBody && state.taskTool === "browser") {
    refreshDesktopPanel(detail, setMessage);
  } else if (toolBody && state.taskTool) {
    replaceRegionHTML(toolBody, renderTaskToolContent(state.taskTool, detail, taskCtxHtml(), taskChannelPanelHTML(), taskPermissionPanelHtml(detail), taskMailPanelHTML(detail)));
    bindSessionActions();
    if (state.taskTool === "channel") bindTaskChannelPanel();
    if (state.taskTool === "permissions") bindTaskPermissionActions(toolBody);
    if (state.taskTool === "mail") bindTaskMailPanel(detail, state.taskMailOptions, taskMailActions());
  }
  const count = document.querySelector<HTMLElement>("#conversation-filter-loaded");
  if (count) count.textContent = `${state.taskConversation.length} 条已加载`;
  syncCompletedTaskFrozen(detail);
  const textarea = document.querySelector<HTMLTextAreaElement>("#message-form textarea");
  if (textarea) {
    textarea.placeholder = activeTurn ? `${state.selectedTaskAgent} 正在执行，发送后将排队` : detail.task.status === "failed" ? "输入消息重试，或输入 /reopen" : `发送给 ${state.selectedTaskAgent}，输入 / 查看命令`;
    syncTaskComposerState(textarea);
  }
  updateLiveDurations();
}

function render(): void {
  if (!app) return;
  flushDeferredListRefresh();
  // Keep the mounted workbench page across same-view renders: the body identity
  // survives, so bindPetWorkbenchPage reuses its loader instead of aborting the
  // request and starting another. Leaving the view or ending the session
  // (logout) releases and aborts it before any other render path can return
  // early. A changed programmatic tab is followed on the preserved page.
  const preservedWorkbenchPage = petWorkbenchPageKeeper.beforeRender({
    view: state.view,
    authenticated: Boolean(state.auth?.authenticated),
    tab: normalizedPetWorkbenchTab(),
  });
  if (state.auth?.authenticated) {
    mountAssistant();
    setAssistantVisible(true);
  } else {
    setAssistantVisible(false);
  }
  if (!state.auth?.authenticated || !state.selectedTask || state.taskTool !== "browser"
    || document.querySelector<HTMLElement>("#desktop-tool")?.dataset.taskId !== state.selectedTask.task.id) stopDesktopPanel();
  document.body.classList.toggle("task-view-active", Boolean(state.selectedTask));
  if (state.loading) {
    delete app.dataset.uiScope;
    app.innerHTML = `<div class="loading boot-loading" role="status" aria-busy="true"><span class="brand-mark"><img src="/assets/aha-logo.svg" alt=""></span><strong>AHA2</strong><span class="boot-spinner" aria-hidden="true"></span></div>`;
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
  // The knowledge settings dialog opts out: it reopens itself and restores
  // focus after each render so index/Git status can update in place.
  if (document.querySelector("dialog[open]:not(#kv-settings-dialog)")) {
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
  if (state.taskTool === "browser" && state.selectedTask
    && document.querySelector<HTMLElement>("#desktop-tool")?.dataset.taskId === state.selectedTask.task.id
    && desktopPanelInteracting()) {
    state.renderPending = true;
    updateTaskLiveRegions();
    return;
  }
  persistNavigationState();
  // The snapshot and the address bar describe the same committed view. Doing
  // this here (not at the top of render) keeps the boot-time deep link intact
  // while still covering every navigation path that sets state.view directly.
  syncViewHash();
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
  const previousConversationImages = captureConversationImages(previousConversation);
  const previousConversationMessageUI = captureConversationMessageState(previousConversation);
  if (previousConversation) measureConversationChrome(previousConversation);
  const previousConversationAnchor = captureConversationAnchor(previousConversation);
  const previousScrollTop = previousConversation?.scrollTop || 0;
  const wasAtConversationBottom = previousConversation
    ? previousConversation.scrollHeight - previousConversation.scrollTop - previousConversation.clientHeight < 80
    : false;
  const autoScrollConversation = shouldAutoScrollConversation(wasAtConversationBottom);
  // A whole-view render can replace the conversation too (tool panels, status
  // changes). Rebuild the window around the same anchor instead of rendering
  // all messages or rewinding the reader.
  const nextConversationViewport: ConversationViewport | null = previousConversation && !state.taskDetailLoading
    ? {
      scrollTop: previousScrollTop,
      clientHeight: previousConversation.clientHeight,
      atBottom: autoScrollConversation,
      anchor: previousConversationAnchor,
    }
    : null;
  let content: string;
  if (state.selectedProject) {
    content = projectDetailView(state.selectedProject);
  } else {
    const views: Record<View, () => string> = {
      projects: projectsView,
		channels: () => shell(renderChannels(state.channelProviders, state.channelInstances, {models: state.models, accounts: state.codexAccounts, projects: state.projects, workspaces: state.workspaces, knowledgeIndexes: state.knowledgeIndexes, libraries: state.knowledgeLibraries, knowledgeVaultEnabled: state.knowledgeVaultEnabled === true, vaultLibraries: state.vaultLibraries})),
      models: modelsView,
      resources: () => shell(renderResourcesPage({
        tab: state.resourcesTab,
        hardware: state.hardwareResources,
        mail: mailResourcesData(),
      })),
      tasks: tasksView,
      // The knowledge navigation is always the vault page. A disabled or
      // not-yet-migrated instance gets the vault chunk's blocked landing with
      // a settings CTA; it never falls back to the legacy workspace UI.
      knowledge: () => shell(renderKnowledgeVaultPage({render, setMessage, announce: announceStatus})),
      prompts: () => advancedSubview(renderPromptAdmin()),
      proxy: () => advancedSubview(renderProxySettings(
        state.proxySettings,
        state.managedProxy,
        !resourceStates.proxy.loaded || resourceStates.proxy.loading,
      )),
      sync: () => advancedSubview(renderSyncSettings(state.syncSettings, state.syncState, state.syncPending, state.syncConflicts, state.syncRun, state.syncSettingsReady, state.syncNextRunAt)),
      advanced: advancedSettingsView,
      data: dataManagementView,
      other: otherSettingsView,
      mcp: mcpView,
      "pet-settings": () => advancedSubview(`<section class="page pet-settings-page"><div id="pet-settings-host"><div class="field-help">正在打开宠物设置…</div></div></section>`),
      "pet-workbench": () => shell(petWorkbenchView()),
    };
    content = views[state.view]();
  }
  const preservedDesktop = state.taskTool === "browser" && preservePageUI
    ? document.querySelector<HTMLElement>(`#desktop-tool[data-read-only="${Boolean(state.selectedTask?.task.read_only)}"]`) : null;
  const nextConversationHTML = state.selectedTask && !state.taskDetailLoading
    ? conversationListHtml(nextConversationViewport || undefined)
    : "";
  const nextConversationWindow = nextConversationViewport && !state.taskDetailLoading
    ? conversationWindowFor(nextConversationViewport).window
    : null;
  const preservedConversation = previousConversation && preservePageUI
    && conversationRenderedHTML.get(previousConversation) === nextConversationHTML ? previousConversation : null;
  app.innerHTML = content;
  // Put the live workbench page back after the shell replacement. Its tab and
  // body listeners stay attached, and bindPetWorkbenchPage sees the same body
  // and returns without restarting the load.
  if (preservedWorkbenchPage) {
    // The shell replacement detached the old page; re-insert it as-is so its
    // tab/body listeners and loader stay live. If the new shell has no
    // workbench slot, the page really is gone: release its request now.
    if (!swapPetWorkbenchPage(app, preservedWorkbenchPage)) petWorkbenchPageKeeper.release();
  }
  translateUI(app);
  if (preservedDesktop) document.querySelector("#desktop-tool")?.replaceWith(preservedDesktop);
  app.dataset.uiScope = nextUIScope;
  const renderedConversation = document.querySelector<HTMLElement>("#conversation-list");
  if (!preservedConversation && renderedConversation) restoreConversationImages(renderedConversation, previousConversationImages);
  let workbenchBindFailed = false;
  try {
    bindCommon();
  } catch (error) {
    // An earlier binding throwing must not leave the freshly rendered workbench
    // template on its "loading" placeholder with no loader running.
    if (state.view !== "pet-workbench") throw error;
    workbenchBindFailed = true;
    showPetWorkbenchBindFailure(error);
  }
  if (state.view === "pet-workbench" && !workbenchBindFailed && !petWorkbenchBindCurrent()) {
    showPetWorkbenchBindFailure(new Error(t("petWorkbench.loadFailed")));
  }
  if (preservedConversation) document.querySelector("#conversation-list")?.replaceWith(preservedConversation);
  if (state.view === "pet-settings") void showPetSettings();
  restoreRegionUI(app, previousAppUI, false);
  if (state.selectedTask) {
    restoreRegionUI(document.querySelector<HTMLElement>("#agent-turn-slot"), previousTurnUI);
    restoreRegionUI(document.querySelector<HTMLElement>("#task-tool-panel-body"), previousToolUI);
  }
  // Markup can only express "all selected"; a partial page selection is the
  // checkbox's live indeterminate state. A whole-view render rebuilds the model
  // results from markup, so re-apply the indicator exactly like the local
  // results refresh does, after region restore has finished writing values.
  const modelResults = document.querySelector<HTMLElement>("#model-results");
  if (modelResults) syncModelSelectionIndicators(modelResults);
  const nextConversation = document.querySelector<HTMLElement>("#conversation-list");
  if (nextConversation) {
    if (preservedConversation) {
      if (scrollConversationToBottom) stabilizeConversationBottom(nextConversation);
      syncConversationNewMessagesHint();
    } else {
      restoreRegionUI(nextConversation, previousConversationUI, false);
      if (!state.taskDetailLoading) {
        conversationRenderedHTML.set(nextConversation, nextConversationHTML);
        restoreConversationMessageState(nextConversation, previousConversationMessageUI);
        measureConversationChrome(nextConversation);
        measureConversationMessages(nextConversation);
        const count = conversationItemKeys(state.taskConversation).length;
        if (nextConversationWindow) {
          noteConversationWindowBounds(nextConversation, nextConversationWindow);
          syncConversationSpacers(nextConversation, nextConversationWindow, count);
          observeConversationRows(nextConversation);
        }
        if (autoScrollConversation) {
          stabilizeConversationBottom(nextConversation);
        } else if (previousConversationAnchor) {
          restoreConversationAnchor(nextConversation, previousConversationAnchor);
        } else {
          nextConversation.scrollTop = previousScrollTop;
        }
        scheduleConversationWindowUpdate();
        syncConversationNewMessagesHint();
      } else if (!composingConversationTouch) {
        nextConversation.scrollTop = previousScrollTop;
      }
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

const boundTaskListControls = new WeakSet<Element>();

// The task list refreshes in place, replacing changed rows, filter controls and
// the load-more tail. Bind each replacement exactly once so row actions keep
// working after a patch without stacking duplicate handlers on untouched rows.
function bindTaskListControls(root: ParentNode = document): void {
  const each = (selector: string, setup: (element: HTMLElement) => void) => {
    root.querySelectorAll<HTMLElement>(selector).forEach(element => {
      if (boundTaskListControls.has(element)) return;
      boundTaskListControls.add(element);
      setup(element);
    });
  };
  const openTaskFromCard = (taskID: string): void => {
    if (!taskID || openingTaskID) return;
    taskCardFocusReturnID = taskID;
    void openTask(taskID).catch(error => {
      taskCardFocusReturnID = "";
      setMessage("error", error instanceof Error ? error.message : String(error));
      render();
    });
  };
  each("[data-task]", card => {
    card.addEventListener("pointerdown", () => {
      taskListPointerActive = true;
    });
    card.addEventListener("click", event => {
      if ((event.target as HTMLElement).closest(".task-card-actions")) return;
      openTaskFromCard(card.dataset.task!);
    });
  });
  // Enter/Space only activate the card body itself. Key events bubbling from
  // the edit/delete buttons are left to those controls.
  each("[data-task-open]", open => {
    open.addEventListener("keydown", event => {
      if (event.target !== open || (event.key !== "Enter" && event.key !== " ")) return;
      event.preventDefault();
      openTaskFromCard(open.closest<HTMLElement>("[data-task]")?.dataset.task || "");
    });
  });
  each("[data-delete-task]", button => button.addEventListener("click", async event => {
    event.stopPropagation();
    const id = button.dataset.deleteTask!;
    if (!window.confirm("删除该任务？此操作不可恢复。")) return;
    void runWithFeedback(button, "删除中", async () => {
      await api.deleteTask(id);
      if (state.selectedTask?.task.id === id) {
        state.selectedTask = null;
        clearTaskCardFocusReturn();
        closeEvents();
      }
      state.tasks = state.tasks.filter(item => item.id !== id);
      setMessage("notice", "任务已删除");
      render();
    });
  }));
  each("[data-retire-remote-task]", button => button.addEventListener("click", async event => {
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
        clearTaskCardFocusReturn();
        closeEvents();
      }
      setMessage("notice", synchronized
        ? "孤立只读任务已移除，删除标记已推送；其他设备将在下次同步时移除镜像"
        : "孤立只读任务已移除；删除标记将在下次同步时传播到其他设备");
      await loadAll();
      render();
    });
  }));
  each("[data-task-filter]", element => {
    const details = element as HTMLDetailsElement;
    details.addEventListener("toggle", () => {
      const kind = details.dataset.taskFilter as TaskFilterKind;
      if (details.open) {
        state.taskOpenFilter = kind;
        document.querySelectorAll<HTMLDetailsElement>("[data-task-filter]").forEach(other => {
          if (other !== details) other.open = false;
        });
      } else if (state.taskOpenFilter === kind) {
        state.taskOpenFilter = "";
      }
    });
  });
  each("[data-task-filter-option]", element => {
    const input = element as HTMLInputElement;
    input.addEventListener("change", () => {
      const kind = input.dataset.taskFilterOption as TaskFilterKind;
      const selected = taskFilterSelection(kind);
      if (input.checked) selected.add(input.value);
      else selected.delete(input.value);
      state.taskOpenFilter = kind;
      taskLoadMoreNoMatch = false;
      render();
    });
  });
  each("[data-task-filter-clear]", button => button.addEventListener("click", () => {
    const kind = button.dataset.taskFilterClear as TaskFilterKind;
    taskFilterSelection(kind).clear();
    state.taskOpenFilter = kind;
    taskLoadMoreNoMatch = false;
    render();
  }));
  each("[data-task-show-all]", button => button.addEventListener("click", () => {
    state.taskStatusFilters.clear();
    state.taskOpenFilter = "";
    taskLoadMoreNoMatch = false;
    render();
  }));
  each("[data-edit-task-title]", button => button.addEventListener("click", async event => {
    event.stopPropagation();
    const taskID = button.dataset.editTaskTitle!;
    const task = state.tasks.find(item => item.id === taskID);
    const heading = document.querySelector<HTMLElement>(`[data-task-title="${CSS.escape(taskID)}"]`);
    if (!task || !heading) return;
    // A role="button" subtree may not contain focusable controls. While the
    // inline editor is open, expose the row body as a plain container; the
    // card template's button semantics come back in finish().
    const cardMain = heading.closest<HTMLElement>(".task-card-main");
    const cardRole = cardMain?.getAttribute("role") ?? null;
    const cardTabIndex = cardMain?.getAttribute("tabindex") ?? null;
    const cardLabel = cardMain?.getAttribute("aria-label") ?? null;
    cardMain?.removeAttribute("role");
    cardMain?.removeAttribute("tabindex");
    cardMain?.removeAttribute("aria-label");
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
      // render() refuses to touch the DOM while a text editor still holds
      // focus, so an Enter/Escape close must release focus before re-rendering;
      // otherwise the editor stays stuck in place with its typed value.
      if (document.activeElement === input) input.blur();
      // The list patch keeps an unchanged row as the same DOM node, so put the
      // heading and the row's button semantics back before re-rendering. A
      // cancelled edit leaves the signature unchanged and would otherwise keep
      // the editor markup on screen.
      if (input.isConnected) input.replaceWith(heading);
      if (cardMain?.isConnected) {
        if (cardRole !== null) cardMain.setAttribute("role", cardRole);
        if (cardTabIndex !== null) cardMain.setAttribute("tabindex", cardTabIndex);
        if (cardLabel !== null) cardMain.setAttribute("aria-label", cardLabel);
      }
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
  each('[data-load-more="tasks"]', button => button.addEventListener("click", () => {
    void loadNextPage("tasks");
  }));
}

function bindCommon(): void {
  bindSidebarControls();
  bindTaskListControls();
  if (state.view === "models") bindCodexAccounts({
    accounts: state.codexAccounts,
    onChanged: async () => { await loadAll(); render(); },
    setMessage,
  });
  if (state.view === "proxy") bindProxySettings({
    settings: state.proxySettings,
    managed: state.managedProxy,
    onChanged: (settings, managed) => {
      state.proxySettings = settings;
      if (managed) state.managedProxy = managed;
      render();
    },
    setMessage,
  });
  if (state.view === "sync") bindSyncSettings({
    settings: state.syncSettings,
    refresh: async () => {
      const [settings, status, conflicts] = await Promise.all([api.syncSettings(), api.syncStatus(), api.syncConflicts()]);
      state.syncSettings = settings.sync; state.syncSettingsReady = true; state.syncState = status.state; state.syncPending = status.pending || 0; state.syncRun = status.run || state.syncRun; state.syncConflicts = conflicts.conflicts || []; state.syncNextRunAt = status.next_run_at || "";
      render();
    },
	pollStatus: async () => {
		const status = await api.syncStatus();
		state.syncState = status.state; state.syncPending = status.pending || 0; state.syncRun = status.run || state.syncRun; state.syncNextRunAt = status.next_run_at || "";
		return state.syncRun;
	},
    setMessage,
    flushDeferredRender,
    initialRun: state.syncRun,
    conflicts: state.syncConflicts,
  });
  if (state.view === "channels") bindChannels({
		context: {models: state.models, accounts: state.codexAccounts, projects: state.projects, workspaces: state.workspaces, knowledgeIndexes: state.knowledgeIndexes, libraries: state.knowledgeLibraries, knowledgeVaultEnabled: state.knowledgeVaultEnabled === true, vaultLibraries: state.vaultLibraries},
    instances: state.channelInstances,
    refresh: async () => {
      const [providers, instances, projects, workspaces, tasks] = await Promise.all([
        api.channelProviders(), api.channelInstances(), api.projects({limit: initialListPageSize}), api.workspaces(), api.tasks("", {limit: taskRefreshLimit(), order: taskListOrder}),
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
			clearTaskCardFocusReturn();
			closeEvents();
			render();
		},
  });
  bindRuntimeFields("task", state.models, state.codexAccounts, syncTaskGitIsolation, true);
  bindRuntimeFields("takeover-task", state.models, state.codexAccounts, syncTakeoverTaskBackend);
  bindRuntimeFields("agent-config", state.models, state.codexAccounts, syncAgentConfigFields, true);
  bindClaudeOfficialDetection("task", () => String(document.querySelector<HTMLSelectElement>("#task-workspace")?.value || ""));
  bindClaudeOfficialDetection("takeover-task", () => String(document.querySelector<HTMLSelectElement>("#takeover-task-workspace")?.value || ""));
  bindClaudeOfficialDetection("agent-config", () => state.selectedTask?.task.workspace_id || "");
  document.querySelectorAll<HTMLElement>("[data-pet-settings]").forEach(button => button.addEventListener("click", () => {
    window.dispatchEvent(new CustomEvent("aha:pet-settings"));
  }));
  document.querySelectorAll<HTMLElement>("[data-view]").forEach(button => button.addEventListener("click", () => {
    switchView(button.dataset.view as View);
  }));
  document.querySelectorAll<HTMLElement>("[data-command-palette-open]").forEach(button => button.addEventListener("click", () => openCommandPalette()));
  document.querySelector<HTMLElement>("#retry-view-data")?.addEventListener("click", () => {
    void ensureViewData(state.view, true).then(() => render());
  });
  document.querySelectorAll<HTMLElement>('[data-load-more]:not([data-load-more="tasks"])').forEach(button => button.addEventListener("click", () => {
    void loadNextPage(button.dataset.loadMore as "projects" | "tasks" | "knowledge");
  }));
  document.querySelector("#logout")?.addEventListener("click", () => {
    const button = document.querySelector<HTMLElement>("#logout");
    void runWithFeedback(button, "退出中", async () => {
      await api.logout();
      state.auth = {...state.auth!, authenticated: false};
      state.selectedTask = null;
      clearTaskCardFocusReturn();
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
      showTaskCreateDialog();
      return;
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
  const modelSearch = document.querySelector<HTMLInputElement>("#model-search");
  if (modelSearch) {
    // Browser autofill can target the first text input on the page and fill it
    // with the saved username. Keep it read-only until the user interacts, and
    // ignore input events that did not come from a real edit.
    let modelSearchUserInput = false;
    const markModelSearchInput = () => { modelSearchUserInput = true; };
    modelSearch.addEventListener("focus", () => {
      modelSearchUserInput = false;
      modelSearch.readOnly = false;
      if (modelSearch.value !== state.modelSearch) modelSearch.value = state.modelSearch;
    });
    modelSearch.addEventListener("blur", () => {
      modelSearchUserInput = false;
      modelSearch.readOnly = true;
      modelSearch.value = state.modelSearch;
    });
    for (const eventName of ["keydown", "pointerdown", "paste", "compositionend"]) {
      modelSearch.addEventListener(eventName, markModelSearchInput);
    }
    modelSearch.addEventListener("beforeinput", event => {
      if (event.inputType !== "insertReplacementText") modelSearchUserInput = true;
    });
    modelSearch.addEventListener("input", () => {
      if (modelSearch.readOnly || !modelSearchUserInput) {
        modelSearch.value = state.modelSearch;
        return;
      }
      state.modelSearch = modelSearch.value;
      state.modelPage = 1;
      // A filter change hides rows; keeping the old selection would let a
      // batch action delete models the owner can no longer see.
      state.modelSelected.clear();
      scheduleModelPageRender();
    });
  }
  const modelResults = document.querySelector<HTMLElement>("#model-results");
  modelResults?.addEventListener("click", event => {
    const target = event.target as HTMLElement;
    const edit = target.closest<HTMLElement>("[data-edit-model]");
    if (edit) {
      openEditModelDialog(edit.dataset.editModel || "");
      return;
    }
    const remove = target.closest<HTMLElement>("[data-delete-model]");
    if (remove) {
      deleteModelByID(remove.dataset.deleteModel || "", remove);
      return;
    }
    const check = target.closest<HTMLElement>("[data-check-model]");
    if (check) {
      void checkModelHealthByID(check.dataset.checkModel || "");
      return;
    }
    const batchCheck = target.closest<HTMLElement>("[data-batch-check-models]");
    if (batchCheck) {
      batchCheckSelectedModels();
      return;
    }
    const batchDelete = target.closest<HTMLElement>("[data-batch-delete-models]");
    if (batchDelete) {
      batchDeleteSelectedModels(batchDelete);
      return;
    }
    const clearSelection = target.closest<HTMLElement>("[data-clear-model-selection]");
    if (clearSelection) {
      state.modelSelected.clear();
      refreshModelResults();
      return;
    }
    const pager = target.closest<HTMLElement>('[data-page-kind="models"]');
    if (!pager) return;
    const delta = Number(pager.dataset.pageDelta || 0);
    if (!delta) return;
    const pages = currentModelPage().slice.pages;
    state.modelPage = Math.min(pages, Math.max(1, state.modelPage + delta));
    refreshModelResults();
  });
  modelResults?.addEventListener("change", event => {
    if (!(event.target instanceof HTMLInputElement)) return;
    const input = event.target;
    if (input.dataset.selectModel) {
      toggleModelSelection(state.modelSelected, input.dataset.selectModel, input.checked);
      refreshModelResults();
      return;
    }
    if (input.hasAttribute("data-select-models-page")) {
      selectModelIDs(state.modelSelected, currentModelPage().pageIDs, input.checked);
      refreshModelResults();
      return;
    }
    const providerID = input.dataset.selectModelGroup;
    if (providerID !== undefined) {
      const group = currentModelPage().groups.find(item => item.providerId === providerID);
      selectModelIDs(state.modelSelected, group ? group.items.map(item => item.id) : [], input.checked);
      refreshModelResults();
    }
  });
  document.querySelectorAll<HTMLElement>("[data-page-kind]").forEach(button => button.addEventListener("click", () => {
    const kind = button.dataset.pageKind || "";
    if (!kind || kind === "models") return;
    const delta = Number(button.dataset.pageDelta || 0);
    if (!delta) return;
    if (kind === "providers") {
      const pages = paginateItems(state.providers, state.providerPage, PROVIDER_PAGE_SIZE).pages;
      state.providerPage = Math.min(pages, Math.max(1, state.providerPage + delta));
    } else if (kind === "codex-accounts") {
      const pages = paginateItems(state.codexAccounts, state.codexAccountPage, CODEX_ACCOUNT_PAGE_SIZE).pages;
      state.codexAccountPage = Math.min(pages, Math.max(1, state.codexAccountPage + delta));
    }
    render();
  }));
  document.querySelector<HTMLSelectElement>("#project-type")?.addEventListener("change", syncProjectTypeFields);
  document.querySelector<HTMLSelectElement>("#ws-locality")?.addEventListener("change", syncWorkspaceFields);
  document.querySelector<HTMLSelectElement>("#ws-transport")?.addEventListener("change", syncWorkspaceFields);
  document.querySelector<HTMLSelectElement>('[name="ssh_auth"]')?.addEventListener("change", syncWorkspaceFields);
  document.querySelector<HTMLSelectElement>('#workspace-takeover-dialog [name="transport"]')?.addEventListener("change", syncWorkspaceTakeoverFields);
  document.querySelector<HTMLInputElement>('[name="clear_ssh_password"]')?.addEventListener("change", syncWorkspaceFields);
  document.querySelector("#provider-preset")?.addEventListener("change", applyProviderPreset);
  document.querySelectorAll<HTMLInputElement>("#provider-name, #provider-base-url").forEach(input => {
    input.addEventListener("input", () => setProviderFieldError(input, ""));
  });
  document.querySelectorAll<HTMLElement>("[data-close]").forEach(button => button.addEventListener("click", () => {
    const dialog = button.closest("dialog") as HTMLDialogElement | null;
    if (dialog?.id === "agent-config-dialog" && !confirmAgentConfigClose(dialog)) return;
    dialog?.close();
  }));
  document.querySelectorAll<HTMLElement>("[data-project]").forEach(element => element.addEventListener("click", () => {
    const id = element.dataset.project!;
    state.selectedProject = state.projects.find(item => item.id === id) || null;
    state.dialogProjectID = id;
    state.selectedTask = null;
    clearTaskCardFocusReturn();
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
    const nameInput = document.querySelector<HTMLInputElement>("#provider-name");
    const baseURLInput = document.querySelector<HTMLInputElement>("#provider-base-url");
    const name = String(nameInput?.value || "").trim();
    const baseURL = String(baseURLInput?.value || "").trim();
    const anthropicURL = String((document.querySelector("#provider-anthropic-url") as HTMLInputElement)?.value || "").trim();
    const apiKey = String((document.querySelector("#provider-api-key") as HTMLInputElement)?.value || "").trim();
    setProviderFieldError(nameInput, name ? "" : "请填写 Provider 名称");
    setProviderFieldError(baseURLInput, baseURL ? "" : "请填写 API Base URL");
    const firstInvalid = name ? (baseURL ? null : baseURLInput) : nameInput;
    if (firstInvalid) {
      firstInvalid.focus();
      return;
    }
    void runWithFeedback(button, "保存中", async () => {
      const body = {name, base_url: baseURL, anthropic_base_url: anthropicURL, api_key: apiKey, auth_style: "auto"};
      if (editId) await api.updateProvider(editId, body);
      else await api.createProvider(body);
      // A Provider edit can change the credential, base URL, or Anthropic
      // entry for every model it backs; old health badges are no longer true.
      forgetAllModelHealth();
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
  document.querySelector("#open-model-add")?.addEventListener("click", openModelAddDialog);
  document.querySelector("#model-add-backend")?.addEventListener("change", syncModelAddWireOptions);
  document.querySelector("#confirm-model-add")?.addEventListener("click", () => void confirmModelAdd());
  document.querySelectorAll<HTMLElement>("[data-delete-provider]").forEach(button => button.addEventListener("click", async event => {
    event.stopPropagation();
    const id = button.dataset.deleteProvider!;
    if (!window.confirm("删除该 Provider 及其模型和凭据？使用这些模型的任务会暂停输入，进入任务后可重新选择模型。")) return;
    void runWithFeedback(button, "删除中", async () => {
      const result = await api.deleteProvider(id);
      forgetAllModelHealth();
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
  // Model row actions are handled by the delegated #model-results listener so
  // the same handlers survive search-result refreshes.
  document.querySelector("#save-model-edit")?.addEventListener("click", () => {
    const button = document.querySelector<HTMLElement>("#save-model-edit");
    const id = button?.getAttribute("data-model-id");
    if (!id) return;
    void runWithFeedback(button, "保存中", async () => {
      const wireModelValue = String((document.querySelector<HTMLInputElement>("#model-edit-wire-model"))?.value || "").trim();
      await api.updateModel(id, {
        display_name: String((document.querySelector<HTMLInputElement>("#model-edit-name"))?.value || "").trim(),
        // Only send when actually changed: the server treats an empty value as
        // "leave it alone", so sending the current value is harmless but
        // sending "" would silently keep the old name.
        ...(wireModelValue ? {wire_model: wireModelValue} : {}),
        wire_api: persistedWireValue(String((document.querySelector<HTMLSelectElement>("#model-edit-wire"))?.value || "responses")),
        backend: String((document.querySelector<HTMLSelectElement>("#model-edit-backend"))?.value || ""),
        context_window: Number((document.querySelector<HTMLInputElement>("#model-edit-context"))?.value || 0),
        max_output_tokens: Number((document.querySelector<HTMLInputElement>("#model-edit-maxout"))?.value || 0),
        auto_compact_window: Number((document.querySelector<HTMLInputElement>("#model-edit-compact"))?.value || 0),
        default_reasoning_effort: String((document.querySelector<HTMLSelectElement>("#model-edit-effort"))?.value || ""),
        supports_image_input: Boolean((document.querySelector<HTMLInputElement>("#model-edit-image"))?.checked),
      });
      forgetModelHealth(id);
      document.querySelector<HTMLDialogElement>("#model-edit-dialog")?.close();
      setMessage("notice", "模型已更新");
      await loadAll();
      render();
    });
  });
  document.querySelector("#task-project")?.addEventListener("change", syncTaskWorkspaces);
  // The collapsed summaries report what is selected inside them, so they must
  // follow every checkbox change, not just the initial render.
  document.querySelector<HTMLElement>("#task-dialog")?.addEventListener("change", event => {
    const target = event.target;
    if (target instanceof HTMLInputElement && target.type === "checkbox") {
      if (target.name === "skill_ids") taskSkillsDirty = true;
      syncTaskFormFoldCounts();
    }
  });
  document.querySelector("#task-workspace")?.addEventListener("change", () => {
    const worktreeDir = document.querySelector<HTMLInputElement>("#task-worktree-dir");
    if (worktreeDir) worktreeDir.value = "";
    syncTaskBackend();
    if (document.querySelector<HTMLSelectElement>("#task-model-source")?.value === "claude_native") {
      void refreshClaudeOfficialCatalog("task", String(document.querySelector<HTMLSelectElement>("#task-workspace")?.value || ""));
    }
  });
  document.querySelector("#takeover-task-workspace")?.addEventListener("change", () => {
    syncTakeoverTaskBackend();
    if (document.querySelector<HTMLSelectElement>("#takeover-task-model-source")?.value === "claude_native") {
      void refreshClaudeOfficialCatalog("takeover-task", String(document.querySelector<HTMLSelectElement>("#takeover-task-workspace")?.value || ""));
    }
  });
  document.querySelector("#takeover-task")?.addEventListener("click", () => {
    syncTakeoverTaskBackend();
    document.querySelector<HTMLDialogElement>("#task-takeover-dialog")?.showModal();
  });
  document.querySelector("#task-isolation")?.addEventListener("change", syncTaskGitIsolation);
  bindForm("#task-form", async form => {
    assertClaudeOfficialReady("task");
    const payload = Object.fromEntries(form.entries()) as Record<string, string>;
    const {stream_idle_timeout_seconds: streamIdleTimeoutSeconds, ...taskPayload} = payload;
    const modelSource = String(form.get("model_source") || "env");
    const modelID = modelSource === "env" ? String(form.get("model_id") || "") : String(form.get("wire_model") || "");
    const configErrors = validateAgentConfigInput({
      backend: String(form.get("backend") || ""),
      modelSource,
      modelID,
      reasoningEffort: String(form.get("reasoning_effort") || ""),
      skillIDs: form.getAll("skill_ids").map(String),
      availableSkillIDs: [...document.querySelectorAll<HTMLInputElement>('#task-skills-fold input[name="skill_ids"]')].map(input => input.value),
      mcpIDs: form.getAll("mcp_server_ids").map(String),
      availableMCPIDs: [...document.querySelectorAll<HTMLInputElement>('#task-mcp-fold input[name="mcp_server_ids"]')].map(input => input.value),
    });
    if (configErrors.length) throw new Error(configErrors[0]);
    const result = await api.createTask({
      ...taskPayload,
      skill_ids: form.getAll("skill_ids").map(String),
      apply_auto_skills: false,
      mcp_server_ids: form.getAll("mcp_server_ids").map(String),
      max_agents: Number(payload.max_agents || 3),
      proxy_enabled: proxyEnabledFrom(payload.proxy_enabled),
      fast_mode: payload.fast_mode === "on",
      stream_idle_timeout_ms: Number(streamIdleTimeoutSeconds || 120) * 1000,
      stream_max_retries: Number(payload.stream_max_retries || 0),
    });
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
        setMessage("notice", `状态已刷新：${statusLabel(result.workspace.health)}`);
      } finally {
        await loadAll();
        render();
      }
    });
  }));
  bindTaskAgentControls();
  bindReopenTaskControls();
  document.querySelector<HTMLSelectElement>("#composer-agent")?.addEventListener("change", async event => {
    await selectTaskAgent(event.currentTarget.value);
    render();
  });
  document.querySelector<HTMLInputElement>('[name="inherit_main"]')?.addEventListener("change", syncAgentConfigFields);
  // Esc closes a native dialog without going through [data-close]; it must ask
  // the same question when there are unsaved edits.
  document.querySelector<HTMLDialogElement>("#agent-config-dialog")?.addEventListener("cancel", event => {
    const dialog = event.currentTarget as HTMLDialogElement;
    if (!confirmAgentConfigClose(dialog)) event.preventDefault();
  });
  // Collapsed summaries report what is selected inside them.
  document.querySelector<HTMLElement>("#agent-config-dialog")?.addEventListener("change", event => {
    const target = event.target;
    if (target instanceof HTMLInputElement && target.type === "checkbox") syncAgentFoldCounts();
  });
  bindForm("#agent-config-form", async form => {
    if (!state.selectedTask) return;
    assertClaudeOfficialReady("agent-config");
    const agentID = String(form.get("agent_id") || state.selectedTaskAgent);
    const inheritMain = agentID !== "main" && form.get("inherit_main") === "on";
    if (!inheritMain) {
      const modelSource = String(form.get("model_source") || "env");
      const modelID = modelSource === "env" ? String(form.get("model_id") || "") : String(form.get("wire_model") || "");
      const errors = validateAgentConfigInput({
        backend: String(form.get("backend") || ""),
        modelSource,
        modelID,
        reasoningEffort: String(form.get("reasoning_effort") || ""),
        skillIDs: agentID === "main" ? form.getAll("skill_ids").map(String) : [],
        availableSkillIDs: agentID === "main"
          ? [...document.querySelectorAll<HTMLInputElement>('#agent-config-skills-fold input[name="skill_ids"]')].map(input => input.value)
          : undefined,
        mcpIDs: agentID === "main" ? form.getAll("mcp_server_ids").map(String) : [],
        availableMCPIDs: agentID === "main"
          ? [...document.querySelectorAll<HTMLInputElement>('#agent-config-mcp-fold input[name="mcp_server_ids"]')].map(input => input.value)
          : undefined,
      });
      if (errors.length) throw new Error(errors[0]);
    }
    if (agentID === "main") {
      await api.updateTask(state.selectedTask.task.id, {
        mcp_server_ids: form.getAll("mcp_server_ids").map(String),
        collaboration_mode: String(form.get("collaboration_mode") || "auto"),
        max_agents: Number(form.get("max_agents") || 3),
        knowledge_policy: String(form.get("knowledge_policy") || "inherit"),
        skill_ids: form.getAll("skill_ids").map(String),
      });
    }
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
      stream_idle_timeout_ms: Number(form.get("stream_idle_timeout_seconds") || 300) * 1000,
      stream_max_retries: Number(form.get("stream_max_retries") || 0),
      filesystem: String(form.get("filesystem") || ""),
      approval: String(form.get("approval") || ""),
      proxy_enabled: proxyEnabledFrom(form.get("proxy_enabled")),
      fast_mode: form.get("fast_mode") === "on",
    });
    await refreshTaskRuntime(state.selectedTask.task.id);
    agentConfigBaseline = "";
    setMessage("notice", `${agentID} 配置已更新，下一个 Turn 生效`);
  });
  document.querySelector("#back-tasks")?.addEventListener("click", () => closeTaskDetail());
  document.querySelector<HTMLFormElement>("#task-takeover-form")?.addEventListener("submit", event => {
    event.preventDefault();
    if (!state.selectedTask?.task.read_only) return;
    try {
      assertClaudeOfficialReady("takeover-task");
    } catch (error) {
      setMessage("error", error instanceof Error ? error.message : String(error));
      return;
    }
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
        stream_idle_timeout_ms: Number(form.get("stream_idle_timeout_seconds") || 120) * 1000,
        stream_max_retries: Number(form.get("stream_max_retries") || 0),
        filesystem: String(form.get("filesystem") || "workspace-write"), approval: String(form.get("approval") || "never"), collaboration_mode: String(form.get("collaboration_mode") || "auto"),
        fast_mode: form.get("fast_mode") === "on",
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
    const response = await api.updateSecuritySettings({validate_origin: validateOrigin, access_scope: state.securitySettings.access_scope});
    state.securitySettings = response.security;
    setMessage("notice", validateOrigin ? "Origin 校验已启用" : "Origin 校验已关闭");
  }, "保存中");
  bindForm("#access-scope-form", async form => {
    const accessScope = String(form.get("access_scope") || "lan");
    if (accessScope === "local" && state.securitySettings.access_scope !== "local" &&
      !window.confirm("改为“仅本机”后，局域网内其他设备将立即无法访问 AHA2。确定继续？")) return;
    const response = await api.updateSecuritySettings({
      validate_origin: state.securitySettings.validate_origin,
      access_scope: accessScope,
    });
    state.securitySettings = response.security;
    setMessage("notice", accessScope === "local" ? "访问范围已改为仅本机" : "访问范围已改为本机与局域网");
  }, "保存中");
  bindForm("#agent-api-override-form", async form => {
    const url = String(form.get("url") || "").trim();
    const response = await api.updateAgentAPISettings({url});
    state.agentAPISettings = response.agent_api;
    setMessage("notice", url ? "Agent API 手动覆盖已保存" : "Agent API 手动覆盖已清除");
  }, "保存中");
  document.querySelector("#clear-agent-api-override")?.addEventListener("click", () => {
    const form = document.querySelector<HTMLFormElement>("#agent-api-override-form");
    const input = form?.querySelector<HTMLInputElement>('[name="url"]');
    if (!form || !input) return;
    input.value = "";
    form.requestSubmit();
  });
  bindForm("#listen-address-form", async form => {
    const listenAddress = String(form.get("listen_address") || "").trim();
    if (listenAddress && state.networkSettings.startup_listen_address &&
      listenAddress !== state.networkSettings.startup_listen_address &&
      !window.confirm(`监听地址将改为 ${listenAddress}，需要重启 AHA2 才生效。确定保存？`)) return;
    const response = await api.updateNetworkSettings(listenAddress);
    state.networkSettings = response.network;
    setMessage("notice", response.network.restart_required ? "监听地址已保存，重启 AHA2 后生效" : "监听地址已保存");
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
  bindMessageComposer();
  document.querySelector<HTMLTextAreaElement>("#message-form textarea")?.addEventListener("paste", event => {
    const files = clipboardImageFiles(event);
    if (!files.length) return;
    event.preventDefault();
    void uploadMessageAttachments(files);
  });
  bindTaskToolsAndPanels();
}

// The composer element is replaced whenever the Task freezes or reopens, so
// its listeners are attached from one place that can be re-run on a new node.
function bindMessageComposer(): void {
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
    if (taskID === "task_pet") notifyPetTaskThinking();
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
    const list = document.querySelector<HTMLElement>("#conversation-list");
    composerFocusWasAtBottom = Boolean(list && conversationIsAtBottom(list));
    document.body.classList.add("composer-focused");
    syncVisualViewportHeight();
    restoreComposerConversationBottom();
    window.setTimeout(() => {
      syncVisualViewportHeight();
      restoreComposerConversationBottom();
    }, 180);
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
        composerFocusWasAtBottom = false;
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
    if (state.taskTool === "browser") stopDesktopPanel();
    if (state.taskTool === tool) {
      if (state.taskToolPinned === tool) {
        state.taskToolPinned = "";
        persistTaskToolLayout();
      }
      state.taskTool = "";
      render();
      return;
    }
    state.taskTool = tool;
    if (state.taskToolPinned) {
      state.taskToolPinned = tool;
      persistTaskToolLayout();
    }
    if (tool === "permissions") {
      if (state.selectedTask) {
        try {
          const detail = await api.task(state.selectedTask.task.id);
          if (state.selectedTask && state.selectedTask.task.id === detail.task.id) {
            state.selectedTask.permission_requests = detail.permission_requests || [];
          }
        } catch (error) {
          setMessage("error", error instanceof Error ? error.message : String(error));
        }
      }
      render();
      return;
    }
    if (tool === "channel") {
      try {
        await reloadTaskChannelState();
      } catch (error) {
        setMessage("error", error instanceof Error ? error.message : String(error));
      }
      return;
    }
    if (tool !== "context" || !state.selectedTask) {
      render();
      return;
    }
    if (state.selectedTask.task.status === "draft") {
      state.taskContext = null;
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
  const closeTaskTool = document.querySelector<HTMLButtonElement>("#close-task-tool");
  if (closeTaskTool) closeTaskTool.onclick = () => {
    if (state.taskTool === "hardware") stopHardwarePanel();
    if (state.taskTool === "browser") stopDesktopPanel();
    if (state.taskToolPinned === state.taskTool) {
      state.taskToolPinned = "";
      persistTaskToolLayout();
    }
    state.taskTool = "";
    render();
  };
  const pinTaskTool = document.querySelector<HTMLButtonElement>("#pin-task-tool");
  if (pinTaskTool) pinTaskTool.onclick = () => {
    if (!state.taskTool) return;
    state.taskToolPinned = state.taskToolPinned === state.taskTool ? "" : state.taskTool;
    persistTaskToolLayout();
    const pinned = state.taskToolPinned === state.taskTool;
    pinTaskTool.classList.toggle("active", pinned);
    pinTaskTool.title = pinned ? "取消固定" : "固定面板";
    pinTaskTool.setAttribute("aria-label", pinTaskTool.title);
    pinTaskTool.setAttribute("aria-pressed", String(pinned));
  };
}

function bindTaskToolsAndPanels(): void {
  bindTaskToolLayout();
  bindTaskPermissionActions();
  if (state.taskTool === "mail" && state.selectedTask) bindTaskMailPanel(state.selectedTask, state.taskMailOptions, taskMailActions());
  if (state.taskTool === "hardware" && state.selectedTask) bindHardwarePanel(state.selectedTask, setMessage);
  if (state.taskTool === "browser" && state.selectedTask) bindDesktopPanel(state.selectedTask, setMessage);
  if (state.taskTool === "channel" && state.selectedTask) bindTaskChannelPanel();
  if (state.view === "knowledge" && !state.selectedProject && !state.selectedTask) {
    bindKnowledgeVaultPage({render, setMessage, announce: announceStatus});
  }
  if (state.view === "mcp") bindMCPView();
  if (state.view === "advanced") bindLanguageSetting();
  if (state.view === "data") bindDataManagementPage();
  if (state.view === "other") bindOtherSettingsPage();
  if (state.view === "pet-workbench") void bindPetWorkbenchPage().catch(showPetWorkbenchBindFailure);
  if (state.view === "prompts") bindPromptAdmin(render, setMessage);
  if (state.view === "resources") bindResourcesPage({
    tab: state.resourcesTab,
    hardware: state.hardwareResources,
    mail: mailResourcesData(),
  }, {
    reloadHardware: reloadHardwareResources,
    mail: {reload: reloadMailResources, notify: setMessage},
    notify: setMessage,
  });
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
        content.innerHTML = renderTaskToolContent(state.taskTool, state.selectedTask!, taskCtxHtml(), taskChannelPanelHTML(), taskPermissionPanelHtml(state.selectedTask!), taskMailPanelHTML(state.selectedTask!));
        bindSessionActions();
        if (state.taskTool === "permissions") bindTaskPermissionActions(content);
        if (state.taskTool === "mail") bindTaskMailPanel(state.selectedTask!, state.taskMailOptions, taskMailActions());
      }
    });
  }));
}

function syncTaskChannelContactSelection(): void {
  const form = document.querySelector<HTMLFormElement>("#task-channel-contacts-form");
  const selected = form?.querySelector<HTMLElement>("[data-task-channel-selected]");
  if (!form || !selected) return;
  selected.querySelectorAll<HTMLInputElement>('input[name^="role_"]').forEach(input => {
    const identityID = input.name.slice("role_".length);
    const checkbox = Array.from(form.querySelectorAll<HTMLInputElement>('input[name="contact_id"]')).find(item => item.value === identityID);
    if (checkbox) checkbox.dataset.memberRole = input.value;
  });
  const rows = Array.from(form.querySelectorAll<HTMLInputElement>('input[name="contact_id"]:checked')).map(input =>
    taskChannelSelectedContactHTML(
      input.value,
      input.dataset.memberName || "群成员",
      input.dataset.memberBot === "true",
      input.dataset.memberRole || "",
    ),
  ).join("");
  selected.innerHTML = rows || '<div class="empty-state">尚未选择联调人</div>';
  selected.querySelectorAll<HTMLInputElement>('input[name^="role_"]').forEach(input => input.addEventListener("input", () => {
    const identityID = input.name.slice("role_".length);
    const checkbox = Array.from(form.querySelectorAll<HTMLInputElement>('input[name="contact_id"]')).find(item => item.value === identityID);
    if (checkbox) checkbox.dataset.memberRole = input.value;
  }));
}

function bindTaskPermissionActions(root: ParentNode = document): void {
  const task = state.selectedTask;
  if (!task) return;
  root.querySelectorAll<HTMLButtonElement>("[data-task-permission-decision]").forEach(button => {
    button.addEventListener("click", async () => {
      const requestID = button.dataset.taskPermissionId || "";
      const decision = button.dataset.taskPermissionDecision as "approve" | "reject";
      if (!requestID || !decision) return;
      button.disabled = true;
      try {
        const result = await api.resolveTaskPermissionRequest(task.task.id, requestID, decision);
        if (state.selectedTask && state.selectedTask.task.id === task.task.id) {
          // Keep the panel open and patch the decided request in place instead
          // of reloading the whole Task, which would close an unpinned panel.
          const requests = state.selectedTask.permission_requests || [];
          const index = requests.findIndex(item => item.id === requestID);
          if (index >= 0) requests[index] = result.request;
          else requests.unshift(result.request);
          state.selectedTask.permission_requests = requests;
          render();
        }
      } catch (error) {
        button.disabled = false;
        setMessage("error", error instanceof Error ? error.message : String(error));
      }
    });
  });
}

function bindTaskChannelPanel(): void {
  const taskID = state.selectedTask?.task.id;
  if (!taskID) return;
  document.querySelector("#reload-task-channel")?.addEventListener("click", () => {
    void reloadTaskChannelState().catch(error => setMessage("error", error instanceof Error ? error.message : String(error)));
  });
  document.querySelector<HTMLFormElement>("#task-channel-route-form")?.addEventListener("submit", event => {
    event.preventDefault();
    const conversationID = String(new FormData(event.currentTarget).get("conversation_id") || "");
    const current = state.taskChannelState?.route;
    if (!conversationID || conversationID === current?.conversation_id) return;
    const destination = state.taskChannelState?.destinations.find(item => item.conversation_id === conversationID);
    const takeover = Boolean(destination?.target_task_id && destination.target_task_id !== taskID);
    const occupiedBy = destination?.target_task_code || destination?.target_task_name || "其他 Task";
    const detail = takeover
      ? `该渠道当前绑定 ${occupiedBy}，连接后会从原 Task 自动解绑并接管。`
      : current ? "切换后旧渠道将停止接收该 Task 的回复。" : "";
    if (detail && !window.confirm(`${detail}继续？`)) return;
    void runWithFeedback(event.submitter as HTMLElement | null, current ? "切换中" : "连接中", async () => {
      await api.bindTaskChannelRoute(taskID, conversationID);
      await reloadTaskChannelState();
      setMessage("notice", takeover ? "Task 主渠道已连接，原 Task 已自动解绑" : current ? "Task 主渠道已切换" : "Task 主渠道已连接");
      window.setTimeout(() => void reloadTaskChannelState().catch(() => undefined), 2200);
    });
  });
  document.querySelector("#disconnect-task-channel")?.addEventListener("click", () => {
    const route = state.taskChannelState?.route;
    if (!route || !window.confirm("解除主渠道连接？Task 状态不会改变。")) return;
    void runWithFeedback(document.querySelector("#disconnect-task-channel"), "解除中", async () => {
      await api.unbindTaskChannelRoute(taskID, route.id);
      await reloadTaskChannelState();
      setMessage("notice", "Task 主渠道已解除");
    });
  });
  document.querySelector("#refresh-task-channel-members")?.addEventListener("click", () => {
    const button = document.querySelector<HTMLElement>("#refresh-task-channel-members");
    void runWithFeedback(button, "同步中", async () => {
      const result = await api.refreshTaskChannelMembers(taskID);
      await reloadTaskChannelState();
      setMessage("notice", result.completed ? "群成员目录已同步" : "同步请求已提交，渠道恢复后会自动完成");
    });
  });
  document.querySelectorAll<HTMLInputElement>('#task-channel-contacts-form input[name="contact_id"]').forEach(input =>
    input.addEventListener("change", syncTaskChannelContactSelection),
  );
  document.querySelector<HTMLFormElement>("#task-channel-contacts-form")?.addEventListener("submit", event => {
    event.preventDefault();
    const form = event.currentTarget;
    form.querySelector<HTMLElement>("[data-form-error]")?.remove();
    const values = new FormData(form);
    const contacts = values.getAll("contact_id").map(value => {
      const identityID = String(value);
      return {
        identity_link_id: identityID,
        collaboration_role: String(values.get(`role_${identityID}`) || "").trim(),
      };
    });
    void runWithFeedback(event.submitter as HTMLElement | null, "保存中", async () => {
      await api.updateTaskChannelContacts(taskID, contacts);
      await reloadTaskChannelState();
      setMessage("notice", "联调人及身份已保存");
    }, message => {
      const feedback = document.createElement("div");
      feedback.className = "form-error";
      feedback.dataset.formError = "true";
      feedback.setAttribute("role", "alert");
      feedback.textContent = message;
      form.querySelector("footer")?.before(feedback);
    });
  });
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

function startTaskFallback(taskID: string, announce = true, immediate = true): void {
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
  // A connection attempt starts the timer as a safety net, but refreshing right
  // away would repeat the Task detail and conversation requests the open just
  // issued. The 2s tick still covers a stream that never connects.
  if (immediate) void refresh();
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
    const list = document.querySelector<HTMLElement>("#conversation-list");
    if (list) {
      measureConversationChrome(list);
      const viewport = liveConversationViewport(list);
      const {window} = conversationWindowFor(viewport);
      const nextHTML = conversationListHtml(viewport);
      patchConversationList(list, nextHTML, window);
      conversationRenderedHTML.set(list, nextHTML);
      // The patch anchored on the first visible row before the prepend and
      // restored it after; prepended rows above it never move the viewport.
    }
    const count = document.querySelector<HTMLElement>("#conversation-filter-count");
    if (count) count.textContent = `${state.taskConversation.length} 条已加载`;
  } finally {
    loadingOlderConversation = false;
  }
}

const boundConversationLoadOlder = new WeakSet<Element>();
const boundConversationNewMessages = new WeakSet<Element>();

function bindConversationLiveControls(): void {
  const list = document.querySelector<HTMLElement>("#conversation-list");
  bindMessageBubbleControls(list || document);
  // Live patches call this repeatedly; bind the pagination button once.
  const loadOlder = document.querySelector("#load-older-conversation");
  if (loadOlder && !boundConversationLoadOlder.has(loadOlder)) {
    boundConversationLoadOlder.add(loadOlder);
    loadOlder.addEventListener("click", () => {
      void loadOlderConversation();
    });
  }
  const newMessages = document.querySelector<HTMLButtonElement>("#conversation-new-messages");
  if (newMessages && !boundConversationNewMessages.has(newMessages)) {
    boundConversationNewMessages.add(newMessages);
    newMessages.addEventListener("click", () => {
      requestConversationBottom();
      updateTaskLiveRegions();
      syncConversationNewMessagesHint();
    });
  }
  if (list && list.dataset.heightChangesBound !== "true") {
    list.dataset.heightChangesBound = "true";
    // Expansion happens in the message control itself and may not change the
    // window HTML, so the patching path cannot be relied on to re-arm the
    // observer. Re-observe before the default toggle action (click) and after
    // a details element changes (toggle).
    const rearm = () => {
      observeConversationRows(list);
      scheduleConversationWindowUpdate();
    };
    list.addEventListener("click", event => {
      const target = event.target instanceof Element ? event.target.closest("[data-toggle-message], summary") : null;
      if (target) rearm();
    });
    list.addEventListener("toggle", rearm, true);
  }
  if (list) observeConversationRows(list);
  if (list && list.dataset.paginationBound !== "true") {
    list.dataset.paginationBound = "true";
    let previousTop = list.scrollTop;
    list.addEventListener("scroll", () => {
      const currentTop = list.scrollTop;
      // Reaching for the scrollback during a gesture ends auto-follow, and
      // returning to the bottom resumes it. Recording intent here, rather than
      // inferring it from position at render time, is what lets a reader leave
      // the bottom band at all.
      // A scroll is the reader's own while a drag is in progress or shortly after
      // wheel input; anything else is the app restoring a position, which must
      // not be read as intent.
      const userDriven = composingConversationTouch || Date.now() < conversationUserScrollUntil;
      if (currentTop < previousTop - 2 && userDriven) conversationFollowBottom = false;
      else if (conversationIsAtBottom(list)) {
        conversationFollowBottom = true;
        if (conversationUnreadMessages) {
          conversationUnreadMessages = 0;
          syncConversationNewMessagesHint();
        }
      }
      if (currentTop < previousTop && currentTop <= 80 && state.taskConversationHasMore) {
        void loadOlderConversation();
      }
      previousTop = currentTop;
      scheduleConversationWindowUpdate();
    }, {passive: true});
  }
  if (list && list.dataset.bottomPinBound !== "true") {
    list.dataset.bottomPinBound = "true";
    // Wheel is tracked by a window, not as a gesture: it has no end event, so a
    // gesture flag set here would never be cleared and new messages would stop
    // following for the rest of the session.
    list.addEventListener("wheel", noteConversationWheel, {passive: true});
    for (const eventName of ["touchstart", "pointerdown"] as const) {
      list.addEventListener(eventName, beginConversationGesture, {passive: true});
    }
    // touchend/touchcancel close the touch gesture; pointerup/pointercancel do
    // the same for mouse and pen. Without these the suppression would outlive the
    // gesture and new messages would stop following while the reader sits at the
    // bottom.
    for (const eventName of ["touchend", "touchcancel", "pointerup", "pointercancel"] as const) {
      list.addEventListener(eventName, endConversationGesture, {passive: true});
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
  startTaskFallback(taskID, false, false);
  events.addEventListener("open", () => {
    // The stream is connected: polling now would only repeat the requests the
    // open already made. A stream that goes quiet is caught by the monitor
    // below, which restarts the fallback after 15s without a signal.
    noteTaskStreamSignal(taskID);
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
      const payload = JSON.parse((event as MessageEvent).data) as {sequence?: number; type?: string};
      const sequence = Number(payload.sequence || 0);
      if (String(payload.type || "").startsWith("mail_")) mailRefreshRequested = true;
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
  if (event.target instanceof HTMLDialogElement && event.target.id === "model-add-dialog" && modelAddRunning) {
    modelAddCancelled = true;
  }
  if (event.target instanceof HTMLDialogElement && event.target.id === "model-dialog" && activeModelDetection) {
    const session = activeModelDetection;
    activeModelDetection = null;
    session.abort?.abort();
    session.listeners?.abort();
    const addDialog = document.querySelector<HTMLDialogElement>("#model-add-dialog");
    if (addDialog?.open) addDialog.close();
  }
  if (event.target instanceof HTMLDialogElement) flushDeferredRender();
}, true);
document.addEventListener("focusout", () => window.setTimeout(flushDeferredRender, 0), true);

// Route chunks are retryable without a page reload. The button lives inside the
// failed page's card, so re-rendering immediately swaps it for the loading card.
document.addEventListener("click", event => {
  const target = event.target instanceof Element ? event.target.closest<HTMLElement>("[data-chunk-retry]") : null;
  if (!target) return;
  if (!retryLazyPage(target.dataset.chunkRetry || "")) return;
  event.preventDefault();
  if (target instanceof HTMLButtonElement) target.disabled = true;
  render();
});

// Event-driven list refresh: one global SSE stream keeps the task list (and
// project/workspace labels) fresh without polling. The task detail view keeps
// its own per-task SSE, so global list refreshes are coalesced and deferred
// while a detail is open instead of resetting the list underneath it.
let globalEvents: EventSource | null = null;

function refreshListData(): void {
  listRefreshScheduler.request();
}

// Called by render(): once the detail view closes there is no pending timer for
// a refresh that was held back, so run it now (the scheduler keeps it pending
// when a task is still open or the page is hidden).
function flushDeferredListRefresh(): void {
  if (!listRefreshScheduler.pending() || listRefreshScheduler.scheduled()) return;
  listRefreshScheduler.flush();
}

// Only the scheduler calls this, and it never starts a second request while
// `listRefreshInFlight` is set, so there is no in-flight guard here that could
// silently drop a pending refresh.
async function performListRefresh(): Promise<void> {
  listRefreshInFlight = true;
  perfMetrics.listRefreshRequests++;
  const startedAt = performance.now();
  try {
    const [tasks, projects, workspaces] = await Promise.all([api.tasks("", {limit: taskRefreshLimit(), order: taskListOrder}), api.projects({limit: initialListPageSize}), api.workspaces()]);
    state.tasks = tasks.tasks || [];
    if (tasks.counts) state.taskFacetCounts = tasks.counts;
    state.projects = projects.projects || [];
    state.workspaces = workspaces.workspaces || [];
    listPages.projects = {cursor: projects.next_cursor || "", hasMore: Boolean(projects.has_more), loadingMore: false};
    listPages.tasks = {cursor: tasks.next_cursor || "", hasMore: Boolean(tasks.has_more), loadingMore: false};
    rebuildOpenCommandPaletteIndex();
    const listPageVisible = state.view === "projects" || state.view === "tasks" || Boolean(state.selectedProject);
    if (!state.selectedTask && !openingTaskID && listPageVisible) {
      // An in-row title editor is a live text control the row patch cannot
      // carry over: replacing its row drops the typed value and focus. Hold the
      // patch until the editor closes; finish() re-renders the list with the
      // already-fresh state instead.
      if (taskListPointerActive || hasActiveTextEditor()) state.renderPending = true;
      else if (!patchTaskList()) render();
    }
  } catch {
    // Transient failures are retried by a queued event or SSE reconnect.
  } finally {
    listRefreshInFlight = false;
    const elapsed = performance.now() - startedAt;
    perfMetrics.listRefreshLastMs = Math.round(elapsed);
    perfMetrics.listRefreshTotalMs += elapsed;
    if (perfEnabled) console.debug(`[aha-perf] list refresh ${perfMetrics.listRefreshLastMs}ms`);
  }
}

// A hidden page pauses refresh traffic; on return, one coalesced catch-up runs.
document.addEventListener("visibilitychange", () => {
  if (document.hidden) return;
  resumePetNavigation();
  if (listRefreshScheduler.pending()) listRefreshScheduler.request();
  if (!secondaryPrefetchDone) prefetchSecondaryData();
});

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
  void refreshPetPendingApprovalCount();
  petPendingApprovalTimer = window.setInterval(() => void refreshPetPendingApprovalCount(), 60_000);
  globalEvents.addEventListener("update", event => {
    let type = "";
    let data: unknown = null;
    try {
      const payload = JSON.parse((event as MessageEvent).data) as {type?: string; data?: unknown};
      type = String(payload.type || "");
      data = payload.data ?? null;
    } catch {
      return;
    }
    // The global stream is the page's single long-lived SSE: pet events ride on
    // it so the assistant does not open a second connection per page.
    if (type === "pet_state" || type === "pet_action") {
      handlePetStreamEvent(type, data);
      return;
    }
    if (type === "pet_approval") refreshPetApprovalState();
    if (eventRefreshesTaskList(type)) {
      refreshListData();
    }
    if (type.startsWith("mail_") && state.view === "resources" && state.resourcesTab === "mail") {
      void reloadMailResources();
    }
  });
  // The stream auto-reconnects; refresh on (re)open so events missed while
  // disconnected are caught up.
  globalEvents.addEventListener("open", () => {
    refreshListData();
    refreshPetApprovalState();
    // A background Turn may have queued an open_task while no tab was
    // connected; claim it now that this session has a live event stream.
    resumePetNavigation();
  });
}

function closeGlobalEvents(): void {
  globalEvents?.close();
  globalEvents = null;
  window.clearInterval(petPendingApprovalTimer);
  petPendingApprovalTimer = 0;
}

window.addEventListener("aha-resources-tab", event => {
  const tab = (event as CustomEvent).detail === "mail" ? "mail" : "hardware";
  state.resourcesTab = tab;
  // Tab switches are display-only; the address bar stays untouched. The tab is
  // also not persisted, so a refresh starts at the default hardware tab.
  void ensureViewData("resources").then(() => render());
});
window.addEventListener("aha-mail-subtab", event => {
  const tab = String((event as CustomEvent).detail || "accounts");
  state.mailSubTab = tab === "templates" || tab === "sent" ? tab : "accounts";
});
window.addEventListener("aha-mail-show-archived", event => {
  state.mailShowArchived = Boolean((event as CustomEvent).detail);
});
window.addEventListener("hashchange", () => {
  if (!applyLocationHash()) return;
  void ensureViewData(state.view).then(() => render());
});
syncVisualViewportHeight();
window.addEventListener("resize", syncVisualViewportHeight);
window.visualViewport?.addEventListener("resize", () => {
  syncVisualViewportHeight();
  restoreComposerConversationBottom();
});
window.visualViewport?.addEventListener("scroll", () => {
  syncVisualViewportHeight();
  restoreComposerConversationBottom();
});
window.setInterval(updateSystemUptime, 30_000);
window.addEventListener("aha:open-vault-settings", () => {
  // The blocked vault page sends the Owner here. The settings panel is lazy, so
  // wait for its first render before scrolling it into view.
  switchView("other");
  void ensureViewData("other").then(() => {
    const deadline = Date.now() + 5000;
    const reveal = () => {
      const panel = document.querySelector<HTMLElement>(".knowledge-vault-settings-panel");
      if (panel) {
        panel.scrollIntoView({block: "start", behavior: "smooth"});
        // Move keyboard/reader focus to the panel heading, not just the scroll
        // position, so the destination is announced after the CTA jump.
        panel.querySelector<HTMLElement>("#knowledge-vault-settings-title")?.focus({preventScroll: true});
        return;
      }
      if (Date.now() < deadline) requestAnimationFrame(reveal);
    };
    requestAnimationFrame(reveal);
  });
});
window.addEventListener("aha:open-pet-settings", () => {
  cancelTaskOpen();
  petWorkbenchTab = "settings";
  state.view = "pet-workbench";
  state.selectedTask = null;
  clearTaskCardFocusReturn();
  state.selectedProject = null;
  closeEvents();
  render();
  void ensureViewData("advanced").then(() => render());
});
window.addEventListener("aha:open-advanced", () => {
  cancelTaskOpen();
  state.view = "advanced";
  state.selectedTask = null;
  clearTaskCardFocusReturn();
  state.selectedProject = null;
  closeEvents();
  render();
  void ensureViewData("advanced").then(() => render());
});
window.addEventListener("aha:pet-reset", event => {
  const detail = (event as CustomEvent<{tasks?: number; workspaces?: number; warnings?: string[]}>).detail;
  const warning = detail?.warnings?.length ? `，${detail.warnings.length} 项文件清理警告` : "";
  setMessage("notice", `宠物本机数据已重置：清理 ${detail?.tasks ?? 0} 个本机 Task、${detail?.workspaces ?? 0} 个本机 Workspace；宠物项目和其他设备数据保留${warning}`);
  void loadAll().then(() => render()).catch(error => {
    setMessage("error", error instanceof Error ? error.message : String(error));
    render();
  });
});
window.addEventListener("aha:assistant-open-task", event => {
  const taskID = (event as CustomEvent<string>).detail;
  if (taskID) void openTask(taskID);
});
document.addEventListener("click", event => {
  if (!(event.target instanceof Element) || !event.target.closest("[data-pet-settings]")) return;
  window.dispatchEvent(new CustomEvent("aha:pet-settings"));
});
startUITranslationObserver();
installDialogEntryFocus();
render();
if (perfEnabled) startPerfPanel();
void bootstrap();

function loadMCPServers(force = false): Promise<void> {
  return loadResource("mcp", force, async () => {
    try {
      const result = await api.mcpServers();
      state.mcpServers = result.servers || [];
    } catch {
      state.mcpServers = [];
    }
  });
}

function mcpView(): string {
  return advancedSubview(renderMCPPage(state.mcpServers));
}

function bindMCPView(): void {
  const mcpPage = document.querySelector<HTMLElement>(".mcp-page");
  if (!mcpPage) return;
  bindMCPPage({
    servers: state.mcpServers,
    reload: () => loadMCPServers(true),
    render,
    setMessage,
  });
}

function petWorkbenchView(): string {
  return `<section class="page pet-workbench-page">
    <header class="page-head"><div><h1>${t("petWorkbench.title")}</h1><p>${t("petWorkbench.subtitle")}</p></div></header>
    <div class="pet-workbench-tabs" role="tablist">
      <button type="button" role="tab" class="active" data-pet-workbench-tab="overview">${t("petWorkbench.overview")}</button>
      <button type="button" role="tab" data-pet-workbench-tab="approvals">${t("petWorkbench.approvals")} <span id="pet-approval-count">${petApprovalCounter.pending > 0 ? petApprovalCounter.pending : ""}</span></button>
      <button type="button" role="tab" data-pet-workbench-tab="records">${t("petWorkbench.records")}</button>
      <button type="button" role="tab" data-pet-workbench-tab="settings">${t("petWorkbench.settings")}</button>
    </div>
    <div id="pet-workbench-body" class="pet-workbench-body"><div class="field-help">${t("common.loading")}</div></div>
  </section>`;
}

// petWorkbenchBindCurrent reports whether the live workbench body already has
// its loader attached. bindPetWorkbenchPage assigns the binding synchronously,
// so a missing current binding right after bindCommon means binding failed.
function petWorkbenchBindCurrent(): boolean {
  return petWorkbenchPageKeeper.owns(document.querySelector<HTMLElement>("#pet-workbench-body"));
}

// showPetWorkbenchBindFailure replaces the template's "loading" placeholder
// with a retryable error when binding itself throws, for example from an
// earlier binding in bindCommon; the async loader never got to start there, so
// without this fallback the body would sit on "loading" forever.
function showPetWorkbenchBindFailure(error: unknown): void {
  const body = document.querySelector<HTMLElement>("#pet-workbench-body");
  if (!body) return;
  petWorkbenchPageKeeper.release();
  const message = error instanceof Error ? error.message : String(error);
  body.innerHTML = renderPetWorkbenchLoadError(message, t);
  body.querySelector<HTMLButtonElement>("[data-pet-workbench-retry]")?.addEventListener("click", () => render());
}

// Legacy bookmarks and the pet panel use "ideas"; the tab is now "approvals".
// Old five-tab names (core/worklog/inspection/follows) fold into the four tabs.
function normalizedPetWorkbenchTab(): string {
  return mapPetWorkbenchTab(petWorkbenchTab);
}

function closePetWorkbenchDialogs(): void {
  const dialog = petWorkbenchDialog;
  petWorkbenchDialog = null;
  if (!dialog) return;
  if (dialog.open) dialog.close();
  dialog.remove();
}

async function bindPetWorkbenchPage(): Promise<void> {
  const body = document.querySelector<HTMLElement>("#pet-workbench-body");
  const tabs = Array.from(document.querySelectorAll<HTMLButtonElement>("[data-pet-workbench-tab]"));
  if (!body || tabs.length === 0) return;
  // render() carries the live page across same-view renders, so this exact
  // connected body is already wired and must keep its in-flight loader. Only a
  // genuinely new body starts a new request.
  if (petWorkbenchPageKeeper.owns(body)) return;
  let active = normalizedPetWorkbenchTab();
  petWorkbenchTab = active;
  let view: Awaited<ReturnType<typeof api.petWorkbench>> | null = null;
  let loadError = "";
  // The approvals tab opens on pending and pages 10 rows at a time; the worklog
  // keeps its existing 30-row page.
  let approvalLimit = 10;
  let worklogLimit = 30;
  let loadingMore: "approval" | "worklog" | null = null;
  let approvalRefreshQueued = false;

  // `t` resolves catalog keys; legacy Chinese literals still go through `tr`
  // so the English locale never shows a mixed sentence.
  const petApprovalText = (key: string, values: Record<string, string | number> = {}): string => {
    const value = t(key, values);
    return value === key ? tr(key) : value;
  };
  const taskStatusText = (status: string): string => {
    const literal = statusLabel(status);
    const legacy = tr(literal);
    if (legacy !== literal) return legacy;
    // A couple of task literals have no legacy translation; keep the overview
    // fully translated through its own keys instead of leaking Chinese.
    const key = `petWorkbench.status.${status}`;
    const label = t(key);
    return label === key ? literal : label;
  };

  const loadMoreFooter = (kind: "worklog", shown: number, total: number): string => {
    if (!total || shown >= total) return "";
    const loading = loadingMore === kind;
    return `<div class="knowledge-list-footer"><span>${t("petWorkbench.loadedCount", {shown: String(shown), total: String(total)})}</span><button type="button" class="knowledge-load-more" data-pet-load-more="${kind}" ${loading ? "disabled" : ""}>${loading ? `${icon("spinner", true)}${t("petWorkbench.loadingMore")}` : t("petWorkbench.loadMore")}</button></div>`;
  };

  // The approval footer is derived from the active kind/status filter. Using
  // approval_total here would keep advertising "load more" while the pending
  // filter had already shown every pending row.
  const approvalLoadMoreFooter = (): string => {
    if (!view) return "";
    const page = petApprovalPage(view.approvals || [], {
      kind: petApprovalFilter,
      status: petApprovalStatusFilter,
      counts: view.approval_counts,
      total: view.approval_total ?? (view.approvals || []).length,
      pendingTotal: view.inspection?.pending_approvals,
      maxItems: PET_APPROVAL_MAX_LIMIT,
    });
    // The server ignores approval_limit above 200, so stop offering a next
    // page there and say why instead of leaving a dead button.
    const capped = approvalLimit >= PET_APPROVAL_MAX_LIMIT && page.total > page.shown;
    return renderPetApprovalLoadMore(page, loadingMore === "approval", t, icon("spinner", true), capped);
  };

  const bindLoadMore = () => {
    body.querySelectorAll<HTMLButtonElement>("[data-pet-load-more]").forEach(button => button.addEventListener("click", () => {
      const kind = button.dataset.petLoadMore;
      if (kind !== "approval" && kind !== "worklog") return;
      // A full load (document save, retry) may already be in flight; folding a
      // bigger limit into it is impossible, so wait for the next click.
      if (loadingMore || loader.busy()) return;
      if (kind === "approval") approvalLimit = Math.min(approvalLimit + 10, PET_APPROVAL_MAX_LIMIT);
      else worklogLimit += 30;
      loadingMore = kind;
      // Repaint so the button shows the same disabled/spinner state as the
      // task list; the in-flight guard prevents a double request.
      renderTab();
      void load().finally(() => {
        // onData/onError already repaint when the request finishes; only a
        // superseded or disposed load reaches here with the flag still set.
        if (loadingMore !== kind) return;
        loadingMore = null;
        if (body.isConnected && petWorkbenchPageKeeper.binding === binding) renderTab();
      });
    }));
  };

  const showLoadState = () => {
    if (loadError) {
      body.innerHTML = renderPetWorkbenchLoadError(loadError, t);
    } else {
      body.innerHTML = `<div class="field-help">${escapeHTML(t("common.loading"))}</div>`;
    }
  };

  const renderTab = () => {
    active = mapPetWorkbenchTab(active);
    tabs.forEach(tab => tab.classList.toggle("active", tab.dataset.petWorkbenchTab === active));
    if (!view) {
      showLoadState();
      return;
    }
    if (active === "overview") {
      body.innerHTML = renderPetOverview({
        inspection: view.inspection,
        approvals: view.approvals || [],
        watches: view.watches || [],
        channels: view.channels || [],
        channelTotal: view.channel_total,
        pendingApprovals: typeof view.inspection?.pending_approvals === "number"
          ? view.inspection.pending_approvals
          : petApprovalCounter.pending,
        translate: t,
        formatTime: formatDateTime,
        statusLabel: taskStatusText,
      });
      bindOverviewActions();
      bindPetWatchActions();
    } else if (active === "approvals") {
      body.innerHTML = (loadError ? renderPetWorkbenchLoadError(loadError, t) : "") + `<div class="pet-approvals-toolbar"><button type="button" class="pet-approval-filter-button" data-pet-approval-filters-open>${icon("filter")}<span>${escapeHTML(t("petWorkbench.filterButton"))}</span><span class="pet-approval-filter-count" data-pet-approval-filter-count>${petApprovalFilterCount(petApprovalFilter, petApprovalStatusFilter)}</span></button></div><div id="pet-approval-rows-host"></div><div id="pet-approval-more-host"></div>`;
      body.querySelector<HTMLButtonElement>("[data-pet-approval-filters-open]")?.addEventListener("click", openApprovalFilters);
      renderApprovalRows();
    } else if (active === "records") {
      if (petRecordsTocOpen === null) {
        // Phones open the records tab on the body, not on a full-height index.
        petRecordsTocOpen = !window.matchMedia("(max-width: 640px)").matches;
      }
      body.innerHTML = renderPetRecords(view.core, view.worklog, {
        translate: t,
        emptyCore: t("petWorkbench.emptyCore"),
        emptyWorklog: t("petWorkbench.emptyWorklog"),
        tocOpen: petRecordsTocOpen,
      }) + loadMoreFooter("worklog", view.worklog.length, view.worklog_total ?? view.worklog.length);
      bindPetDocEditorButtons(body, {
        translate: t,
        save: (section, name, content) => api.savePetWorkbenchDocument(section, name, content),
        reload: load,
        cancel: renderTab,
      });
      bindRecordToc();
      bindRecordTocToggle();
      bindLoadMore();
    } else if (active === "settings") {
      body.innerHTML = `<div id="pet-settings-host" class="pet-workbench-settings"></div>`;
      void showPetSettings();
    }
  };

  let binding: PetWorkbenchPageBinding | null = null;
  const loader = createPetWorkbenchLoader({
    read: signal => api.petWorkbench({approvalLimit, worklogLimit}, signal),
    // A render replaces the body; a superseded loader must not write its result
    // or error into the detached node, and must not clear the new loader's state.
    isCurrent: () => body.isConnected && petWorkbenchPageKeeper.binding === binding,
    onLoading: () => {
      loadError = "";
      if (!view) showLoadState();
    },
    onData: value => {
      view = value;
      loadingMore = null;
      void refreshPetPendingApprovalCount();
      renderTab();
      if ((view.approvals || []).some(item => item.status === "executing")) scheduleApprovalExecutionPoll();
    },
    onError: message => {
      loadingMore = null;
      loadError = message;
      // A failed list refresh keeps the last good approvals on screen; other
      // tabs keep the original full retry state.
      if (view && active === "approvals") renderTab();
      else {
        view = null;
        showLoadState();
      }
    },
    timeoutMessage: t("petWorkbench.loadTimeout"),
  });
  binding = petWorkbenchPageKeeper.adopt(body, body.closest<HTMLElement>(".pet-workbench-page"), loader, active);
  binding.refresh = () => { if (active === "approvals") refreshApprovals(); };
  const load = () => loader.load();

  // Live approval events use the same loader as user actions. An event during
  // an in-flight request queues exactly one follow-up so a commit that landed
  // after the read is not missed.
  const refreshApprovals = (): void => {
    if (loader.busy()) {
      // The in-flight request may have read the database before this event's
      // commit; attach one follow-up instead of dropping the event.
      if (approvalRefreshQueued) return;
      approvalRefreshQueued = true;
      void load().then(() => {
        approvalRefreshQueued = false;
        refreshApprovals();
      });
      return;
    }
    void load();
  };

  // A heavy delete is executed in the background after the resolve returns
  // 202, so the card is refreshed until the approval leaves "executing".
  // Polling stops as soon as the page is detached or no execution remains.
  let approvalPollTimer: ReturnType<typeof setTimeout> | null = null;
  function scheduleApprovalExecutionPoll(): void {
    if (approvalPollTimer !== null) return;
    approvalPollTimer = setTimeout(async () => {
      approvalPollTimer = null;
      if (!body.isConnected || !binding || petWorkbenchPageKeeper.binding !== binding) return;
      if (!(view?.approvals || []).some(item => item.status === "executing")) return;
      await load();
      if (body.isConnected && binding && petWorkbenchPageKeeper.binding === binding &&
          (view?.approvals || []).some(item => item.status === "executing")) {
        scheduleApprovalExecutionPoll();
      }
    }, 3000);
  }

  // Dialogs live outside #app so a background refresh cannot detach them while
  // the owner is reading an approval or adjusting filters.
  const createWorkbenchDialog = (className: string, html: string): HTMLDialogElement => {
    closePetWorkbenchDialogs();
    const dialog = document.createElement("dialog");
    dialog.className = className;
    dialog.innerHTML = `<div class="pet-approval-dialog-body">${html}</div>`;
    document.body.appendChild(dialog);
    petWorkbenchDialog = dialog;
    dialog.addEventListener("cancel", event => {
      event.preventDefault();
      closePetWorkbenchDialogs();
    });
    dialog.addEventListener("click", event => {
      if (event.target === dialog) closePetWorkbenchDialogs();
    });
    return dialog;
  };

  const showDialogError = (dialog: HTMLDialogElement, message: string): void => {
    const host = dialog.querySelector<HTMLElement>("[data-pet-approval-error]");
    if (!host) return;
    host.hidden = false;
    host.className = "field-help pet-approval-error";
    host.setAttribute("role", "alert");
    host.textContent = message;
    host.scrollIntoView({block: "nearest"});
  };

  const openOverviewTask = (taskID: string): void => {
    if (!taskID) return;
    closePetWorkbenchDialogs();
    switchView("tasks");
    void openTask(taskID).catch(error => {
      setMessage("error", error instanceof Error ? error.message : String(error));
      render();
    });
  };

  const openApprovalDetail = (approvalID: string): void => {
    const approval = view?.approvals.find(item => item.id === approvalID);
    if (!approval) return;
    const dialog = createWorkbenchDialog("pet-approval-detail-dialog", renderPetApprovalDetail(approval, {
      translate: petApprovalText,
      now: Date.now(),
    }));
    dialog.querySelector<HTMLButtonElement>("[data-pet-approval-detail-close]")?.addEventListener("click", closePetWorkbenchDialogs);
    dialog.querySelectorAll<HTMLButtonElement>("[data-pet-approval-decision]").forEach(button => {
      button.addEventListener("click", async () => {
        const decision = button.dataset.petApprovalDecision as "approve" | "reject" | "cancel";
        if (!decision) return;
        let decisionReason = "";
        if (decision === "approve") {
          // Owner-decision approvals with payload options require a choice; the
          // chosen option value is the note so the pet knows which plan won.
          const options = petApprovalOptions(approval);
          if (options.length) {
            const select = [...dialog.querySelectorAll<HTMLSelectElement>("[data-pet-approval-option]")]
              .find(item => item.dataset.petApprovalOption === approvalID);
            decisionReason = selectedPetApprovalOption(options, select?.value);
            if (!decisionReason) {
              showDialogError(dialog, tr("请先选择一个方案再批准。"));
              select?.focus();
              return;
            }
          }
        }
        if (decision === "reject") {
          decisionReason = (window.prompt(t("petApproval.rejectReasonPrompt")) || "").trim();
          if (!decisionReason) return;
        }
        const buttons = [...dialog.querySelectorAll<HTMLButtonElement>("[data-pet-approval-decision]")];
        buttons.forEach(item => { item.disabled = true; });
        try {
          const result = await api.resolvePetApproval(approvalID, decision, decisionReason);
          closePetWorkbenchDialogs();
          await refreshPetPendingApprovalCount(true);
          refreshApprovals();
          if (result.status === "executing") scheduleApprovalExecutionPoll();
        } catch (error) {
          buttons.forEach(item => { item.disabled = false; });
          showDialogError(dialog, error instanceof Error ? error.message : String(error));
        }
      });
    });
    dialog.showModal();
  };

  const bindApprovalRows = (): void => {
    body.querySelectorAll<HTMLButtonElement>("[data-pet-approval-open]").forEach(button => {
      button.addEventListener("click", () => openApprovalDetail(button.dataset.petApprovalOpen || ""));
    });
  };

  const renderApprovalRows = (): void => {
    if (!view) return;
    const items = (view.approvals || []).filter(item => petApprovalMatches(item, petApprovalFilter, petApprovalStatusFilter));
    const filtering = petApprovalFilter !== "all" || (petApprovalStatusFilter !== "all" && petApprovalStatusFilter !== "pending");
    const host = body.querySelector<HTMLElement>("#pet-approval-rows-host");
    if (host) {
      host.innerHTML = renderPetApprovalList(items, {
        translate: petApprovalText,
        now: Date.now(),
        empty: filtering ? t("petWorkbench.emptyFiltered") : t("petWorkbench.emptyApprovalsPending"),
      });
      host.querySelectorAll<HTMLButtonElement>("[data-pet-approval-open]").forEach(button => {
        button.addEventListener("click", () => openApprovalDetail(button.dataset.petApprovalOpen || ""));
      });
    }
    const more = body.querySelector<HTMLElement>("#pet-approval-more-host");
    if (more) more.innerHTML = approvalLoadMoreFooter();
    const badge = body.querySelector<HTMLElement>("[data-pet-approval-filter-count]");
    if (badge) badge.textContent = String(petApprovalFilterCount(petApprovalFilter, petApprovalStatusFilter));
    bindLoadMore();
  };

  const openApprovalFilters = (): void => {
    const dialog = createWorkbenchDialog("pet-approval-filter-dialog", renderPetApprovalFilters(
      petApprovalFilter,
      petApprovalStatusFilter,
      petApprovalText,
    ));
    dialog.querySelector<HTMLButtonElement>("[data-pet-approval-filters-close]")?.addEventListener("click", closePetWorkbenchDialogs);
    const syncCount = (): void => {
      const count = petApprovalFilterCount(petApprovalFilter, petApprovalStatusFilter);
      const summary = dialog.querySelector<HTMLElement>(".pet-approval-filter-summary");
      if (summary) summary.textContent = petApprovalText("petWorkbench.filterActive", {count: String(count)});
      const badge = body.querySelector<HTMLElement>("[data-pet-approval-filter-count]");
      if (badge) badge.textContent = String(count);
    };
    dialog.querySelectorAll<HTMLButtonElement>("[data-pet-approval-filter]").forEach(button => {
      button.addEventListener("click", () => {
        const next = button.dataset.petApprovalFilter;
        if (next !== "all" && next !== "idea" && next !== "decision") return;
        petApprovalFilter = next;
        dialog.querySelectorAll("[data-pet-approval-filter]").forEach(other => other.classList.toggle("active", other === button));
        syncCount();
        renderApprovalRows();
      });
    });
    dialog.querySelectorAll<HTMLButtonElement>("[data-pet-approval-status-filter]").forEach(button => {
      button.addEventListener("click", () => {
        const next = button.dataset.petApprovalStatusFilter;
        if (next !== "all" && next !== "pending" && next !== "approved" && next !== "rejected" && next !== "cancelled") return;
        petApprovalStatusFilter = next;
        dialog.querySelectorAll("[data-pet-approval-status-filter]").forEach(other => other.classList.toggle("active", other === button));
        syncCount();
        renderApprovalRows();
      });
    });
    dialog.showModal();
  };

  const bindOverviewActions = (): void => {
    body.querySelectorAll<HTMLButtonElement>("[data-pet-overview-jump]").forEach(button => {
      button.addEventListener("click", () => {
        const jump = button.dataset.petOverviewJump;
        if (jump === "approvals") {
          closePetWorkbenchDialogs();
          active = "approvals";
          petWorkbenchTab = active;
          if (binding) binding.tab = active;
          renderTab();
        } else if (jump === "channels") {
          switchView("channels");
        } else {
          // The watch list starts collapsed like the other overview sections,
          // so the jump has to reveal it before scrolling.
          const follows = document.getElementById("pet-overview-follows");
          if (follows instanceof HTMLDetailsElement) follows.open = true;
          follows?.scrollIntoView({block: "start", behavior: "smooth"});
        }
      });
    });
    bindApprovalRows();
    body.querySelectorAll<HTMLButtonElement>("[data-pet-overview-task]").forEach(button => {
      button.addEventListener("click", () => openOverviewTask(button.dataset.petOverviewTask || ""));
    });
  };

  const bindRecordToc = (): void => {
    body.querySelectorAll<HTMLButtonElement>("[data-pet-record-goto]").forEach(button => {
      button.addEventListener("click", () => {
        const target = document.getElementById(button.dataset.petRecordGoto || "");
        if (!target) return;
        if (target instanceof HTMLDetailsElement) target.open = true;
        target.scrollIntoView({block: "start", behavior: "smooth"});
      });
    });
  };

  // Collapse/expand without a repaint, so open document entries stay open and
  // the state stays in petRecordsTocOpen for the next render.
  const bindRecordTocToggle = (): void => {
    body.querySelectorAll<HTMLButtonElement>("[data-pet-record-toc-toggle]").forEach(button => {
      button.addEventListener("click", () => {
        const open = !(petRecordsTocOpen ?? true);
        petRecordsTocOpen = open;
        body.querySelector<HTMLElement>(".pet-records")?.classList.toggle("toc-collapsed", !open);
        body.querySelectorAll<HTMLButtonElement>("[data-pet-record-toc-toggle]").forEach(toggle => {
          toggle.setAttribute("aria-expanded", String(open));
          toggle.textContent = t(open ? "petWorkbench.recordsTocCollapse" : "petWorkbench.recordsTocExpand");
        });
      });
    });
  };

  const bindPetWatchActions = () => {
    body.querySelectorAll<HTMLButtonElement>("[data-pet-watch-follow]").forEach(button => {
      button.addEventListener("click", async () => {
        const taskID = button.dataset.petWatchTask || "";
        if (!taskID) return;
        button.disabled = true;
        try {
          await api.followPetTask(taskID);
          await load();
        } catch (error) {
          button.disabled = false;
          body.insertAdjacentHTML("afterbegin", `<div class="field-help">${escapeHTML(error instanceof Error ? error.message : String(error))}</div>`);
        }
      });
    });
    body.querySelectorAll<HTMLButtonElement>("[data-pet-watch-unfollow]").forEach(button => {
      button.addEventListener("click", async () => {
        const taskID = button.dataset.petWatchTask || "";
        if (!taskID) return;
        button.disabled = true;
        try {
          await api.removePetTaskWatch(taskID);
          await load();
        } catch (error) {
          button.disabled = false;
          body.insertAdjacentHTML("afterbegin", `<div class="field-help">${escapeHTML(error instanceof Error ? error.message : String(error))}</div>`);
        }
      });
    });
    body.querySelectorAll<HTMLButtonElement>("[data-pet-watch-ignore]").forEach(button => {
      button.addEventListener("click", async () => {
        const taskID = button.dataset.petWatchTask || "";
        const ignored = button.dataset.petWatchIgnore === "true";
        if (!taskID) return;
        button.disabled = true;
        try {
          await api.setPetTaskWatchIgnored(taskID, ignored);
          await load();
        } catch (error) {
          button.disabled = false;
          body.insertAdjacentHTML("afterbegin", `<div class="field-help">${escapeHTML(error instanceof Error ? error.message : String(error))}</div>`);
        }
      });
    });
  };

  body.addEventListener("click", event => {
    if (!(event.target instanceof Element) || !event.target.closest("[data-pet-workbench-retry]")) return;
    loadError = "";
    void load();
  });
  tabs.forEach(tab => tab.addEventListener("click", () => {
    active = mapPetWorkbenchTab(tab.dataset.petWorkbenchTab);
    petWorkbenchTab = active;
    closePetWorkbenchDialogs();
    if (binding) binding.tab = active;
    renderTab();
    // Entering approvals always re-reads, because events that arrived while
    // another tab was open cannot patch the mounted rows.
    if (active === "approvals") void load();
  }));
  await load();
}

