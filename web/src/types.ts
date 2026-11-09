export interface AuthStatus {
  ok: boolean;
  authenticated: boolean;
  registration_open: boolean;
  owner_id?: string;
  username?: string;
  csrf_token?: string;
}
export interface SecuritySettings {
  validate_origin: boolean;
  // "local" answers only this machine; "lan" also answers private network
  // addresses. The server binds every interface, so this is what narrows it.
  access_scope: string;
  startup_override?: boolean;
  updated_at?: string;
}
export interface NetworkSettings {
  // Persisted override; empty means the address the service was installed with.
  listen_address: string;
  startup_listen_address: string;
  restart_required: boolean;
}
export interface AgentAPISettings {
  url: string;
  allow_insecure: boolean;
  startup_url?: string;
  startup_allow_insecure?: boolean;
  effective_url: string;
  effective_allow_insecure: boolean;
  updated_at?: string;
}
export interface BackendSettings {
  idle_timeout_seconds: number;
  turn_timeout_seconds: number;
  updated_at?: string;
}
export interface GeneralSettings {
  language: string;
  updated_at?: string;
}
export interface SystemInfo {
  os: string;
  arch: string;
  wsl_available: boolean;
  wsl_distros: string[];
  version?: string;
  web_version?: string;
  started_at?: string;
}

export interface DataCategory {
  id: string;
  label: string;
  bytes: number;
  safe_cleanup: boolean;
  note?: string;
}

export interface DataInventory {
  data_dir: string;
  total_bytes: number;
  categories: DataCategory[];
}

export interface DataCleanupResult {
  freed_bytes: number;
  removed: number;
  skipped: number;
  inventory: DataInventory;
}

export interface HardwareIORetentionSettings {
  retention_days: number;
  max_delete_rows: number;
  updated_at?: string;
}

// rows is MAX(rowid)-MIN(rowid)+1 unless rows_exact; estimated_bytes is the
// sampled average payload times rows and excludes index/page overhead.
export interface DatabaseTableStat {
  name: string;
  rows: number;
  rows_exact: boolean;
  sample_rows: number;
  avg_row_bytes: number;
  estimated_bytes: number;
  deletable: boolean;
}

export type DatabaseSafety = "keep" | "read_only" | "preview_purge" | "retire";

// id is the catalog letter A-K, or "other" for uncategorized tables.
export interface DatabaseCategory {
  id: string;
  key: string;
  safety: DatabaseSafety;
  rows: number;
  rows_exact: boolean;
  estimated_bytes: number;
  tables: DatabaseTableStat[];
}

export interface DatabaseStats {
  file_bytes: number;
  wal_bytes: number;
  shm_bytes: number;
  page_size: number;
  page_count: number;
  freelist_count: number;
  used_bytes: number;
  free_bytes: number;
  table_count: number;
  categories: DatabaseCategory[];
  cached: boolean;
  hardware_io: {min_sequence: number; max_sequence: number; estimated_rows: number; oldest_at?: string; newest_at?: string};
  retention: HardwareIORetentionSettings;
  collected_at: string;
  duration_ms: number;
}

export interface HardwareIOPurgePlan {
  retention_days: number;
  max_delete_rows: number;
  cutoff: string;
  min_sequence: number;
  max_sequence: number;
  cutoff_sequence: number;
  eligible_rows_estimate: number;
  run_start_sequence: number;
  run_end_sequence: number;
  run_rows: number;
  avg_row_bytes: number;
  run_bytes_estimate: number;
  eligible_bytes_estimate: number;
  remaining_eligible_estimate: number;
  skip_reason?: string;
}

export interface HardwareIOPurgeResult {
  plan: HardwareIOPurgePlan;
  deleted_rows: number;
  batches: number;
  batch_size: number;
  partial: boolean;
  skip_reason?: string;
  duration_ms: number;
}

export interface ProxySettings {
  mode: "off" | "external" | "managed_hysteria2";
  http_proxy: string;
  https_proxy: string;
  no_proxy: string;
  managed_profile_id?: string;
  managed_node_id?: string;
  managed_refresh_interval_minutes: number;
  managed_subscription_at?: string;
  updated_at?: string;
}

export interface ManagedProxyView {
  configured: boolean;
  active_profile_id?: string;
  profiles: ManagedProxyProfile[];
  url_configured: boolean;
  nodes: Array<{id: string; name: string; protocol: "hysteria2" | "vless"}>;
  unsupported_count: number;
  unsupported_types: string[];
  status: string;
  last_error?: string;
}

export interface ManagedProxyProfile {
  id: string;
  name: string;
  active: boolean;
  url_configured: boolean;
  nodes: Array<{id: string; name: string; protocol: "hysteria2" | "vless"}>;
  selected_node_id: string;
  active_node_id?: string;
  needs_selection: boolean;
  unsupported_count: number;
  unsupported_types: string[];
  refresh_interval_minutes: number;
  subscription_at?: string;
}

export interface SyncSettings {
  scope: string;
  enabled: boolean;
  endpoint: string;
  device_id: string;
  device_name?: string;
  interval_seconds: number;
  token_configured: boolean;
  passphrase_configured: boolean;
  updated_at?: string;
}

export interface SyncState {
  scope: string;
  cursor: string;
  last_push_at?: string;
  last_pull_at?: string;
  last_error: string;
  updated_at?: string;
}

export interface SyncPreview {
  upserts: number;
  deletes: number;
	remote_upserts: number;
	remote_deletes: number;
	remote_truncated?: boolean;
  pending: number;
  conflicts: number;
}

export interface SyncRunProgress {
	running: boolean;
	phase: string;
	detail?: string;
	completed: number;
	total: number;
	error?: string;
	summary?: SyncPreview;
	started_at?: string;
	updated_at?: string;
	finished_at?: string;
}

export interface SyncConflict {
  id: string;
  object_type: string;
  object_id: string;
  local_version: string;
  remote_version: string;
  status: string;
  created_at: string;
}

export interface Project {
  id: string;
  name: string;
  description: string;
  project_type?: string;
  channel_instance_id?: string;
  channel_retired?: boolean;
  read_only_reason?: string;
  repository_identity?: string;
  default_branch?: string;
  knowledge_policy: "enabled" | "disabled" | string;
  knowledge_revision: number;
  updated_at: string;
}

export interface ProductLine {
  id: string;
  project_id: string;
  name: string;
  branch_pattern: string;
  default: boolean;
  created_at: string;
  updated_at: string;
}

export interface Workspace {
  id: string;
  project_id: string;
  name: string;
  locality: "local" | "remote";
  transport: "native" | "ssh" | "wsl" | string;
  root_path: string;
  ssh_host?: string;
  ssh_user?: string;
  ssh_port?: number;
  ssh_auth?: "auto" | "password" | "key";
  ssh_password_configured: boolean;
  distro?: string;
  platform?: string;
  health: string;
  capabilities?: Record<string, unknown> & {
    workspace?: WorkspaceProbe;
    platform?: WorkspaceProbe;
  codex?: WorkspaceProbe;
  claude?: WorkspaceProbe;
  dsh?: WorkspaceProbe;
    agent_api?: WorkspaceProbe & {url?: string};
    git?: boolean;
  };
  repository?: {
    status?: "ready" | "not_repository" | "execution_failed" | string;
    is_git?: boolean;
    root?: string;
    branch?: string;
    message?: string;
    error?: string;
  };
  owner_device_id?: string;
  read_only?: boolean;
  read_only_reason?: string;
  channel_instance_id?: string;
  channel_retired?: boolean;
}

export interface Model {
  id: string;
  display_name: string;
  provider_id: string;
  provider_name?: string;
  source: "provider" | "official" | "claude_native" | string;
  codex_account_id?: string;
  backend: string;
  wire_model: string;
  wire_api?: string;
  context_window?: number;
  max_output_tokens?: number;
  auto_compact_window?: number;
  default_reasoning_effort?: string;
  /** Model capability metadata; supports_image_input defaults to enabled. */
  capabilities?: Record<string, unknown>;
}

export interface WorkspaceProbe {
  status?: "ready" | "unavailable" | "not_installed" | "execution_failed" | string;
  version?: string;
  error?: string;
  auth_status?: "logged_in" | "not_logged_in" | "unknown" | string;
  auth_method?: string;
  api_provider?: string;
  models?: ClaudeModelOption[];
  models_error?: string;
  models_updated_at?: string;
}

export interface ClaudeModelOption {
  wire_model: string;
  resolved_model?: string;
  display_name: string;
  description?: string;
  supports_effort?: boolean;
  supported_effort_levels?: string[];
  supports_adaptive_thinking?: boolean;
  supports_fast_mode?: boolean;
  supports_auto_mode?: boolean;
}

export interface CodexAccount {
  id: string;
  label: string;
  email?: string;
  account_id?: string;
  plan_type?: string;
  status: string;
  proxy_enabled: boolean;
  credential_configured: boolean;
  owner_bound: boolean;
  usage?: CodexUsage;
  usage_updated_at?: string;
  usage_error?: string;
  available_models?: CodexModelOption[];
  models_updated_at?: string;
  models_error?: string;
  created_at: string;
  updated_at: string;
  last_used_at?: string;
}

export interface CodexUsage {
  rate_limits: CodexRateLimit[];
  credits: {
    has_credits: boolean;
    unlimited: boolean;
    overage_limit_reached: boolean;
    balance?: string;
  };
  reset_credits_available?: number;
}

export interface CodexRateLimit {
  id: string;
  name: string;
  allowed: boolean;
  limit_reached: boolean;
  primary_window?: CodexUsageWindow;
  secondary_window?: CodexUsageWindow;
}

export interface CodexUsageWindow {
  used_percent: number;
  limit_window_seconds: number;
  reset_at?: number;
}

export interface CodexModelOption {
  wire_model: string;
  display_name: string;
  description?: string;
  context_window?: number;
  max_context_window?: number;
  default_reasoning_effort?: string;
  reasoning_efforts?: string[];
}

export interface CodexLogin {
  id: string;
  status: string;
  auth_url: string;
  account?: CodexAccount;
  error?: string;
  login_status?: string;
  login_error?: string;
  started_at: string;
  updated_at: string;
}

export interface DetectedModel {
  id: string;
  max_input_tokens?: number;
  max_output_tokens?: number;
  capabilities?: Record<string, string>;
}

export interface Provider {
  id: string;
  name: string;
  base_url: string;
  anthropic_base_url?: string;
  auth_style?: string;
  credential_configured: boolean;
}

export interface EnvGroup {
  id: string;
  name: string;
  provider_id: string;
  backend: string;
  revision: number;
  environment: Record<string, string>;
  secret_names: string[];
  secret_configured: boolean;
}

export interface Task {
  review_decision?: "required" | "skipped";
  review_reason?: string;
  review_task_id?: string;
  review_decided_at?: string;
  review_decided_by?: "main" | "pet";
  id: string;
  code?: string;
  project_id: string;
  workspace_id: string;
  title: string;
  original_request: string;
  current_goal: string;
  status: string;
  target_branch?: string;
  task_branch?: string;
  isolation: "worktree" | "inplace";
  worktree_dir?: string;
  task_workspace_path?: string;
  collaboration_mode: "single" | "auto";
  max_agents: number;
  knowledge_policy: "inherit" | "enabled" | "disabled" | string;
  skill_ids: string[];
  agent_capabilities: Record<string, boolean>;
  mcp_server_ids?: string[];
  total_tokens: number;
  created_at: string;
  updated_at: string;
  owner_device_id?: string;
  read_only?: boolean;
  read_only_reason?: string;
  channel_instance_id?: string;
  channel_retired?: boolean;
  agent_api_mode: "auto" | "manual" | string;
  agent_api_url?: string;
  agent_api_resolved_url?: string;
  agent_api_status: "unknown" | "ready" | "error" | string;
  agent_api_error?: string;
  agent_api_last_checked_at?: string;
}

export interface Turn {
  id: string;
  task_id: string;
  round_id: string;
  agent_id: string;
  sequence: number;
  parent_turn_id?: string;
  attempt: number;
  generation: number;
  required: boolean;
  title?: string;
  status: string;
  waiting_reason?: string;
  backend_session_id?: string;
  context_window?: number;
  prompt_chars?: number;
  usage?: Record<string, number>;
  queued_at: string;
  queued_at_ms: number;
  prepared_at?: string;
  prepared_at_ms?: number;
  context_ready_at?: string;
  context_ready_at_ms?: number;
  session_ready_at?: string;
  session_ready_at_ms?: number;
  started_at?: string;
  started_at_ms?: number;
  first_event_at?: string;
  first_event_at_ms?: number;
  last_activity_at?: string;
  last_activity_at_ms?: number;
  stalled_at?: string;
  stalled_at_ms?: number;
  backend_finished_at?: string;
  backend_finished_at_ms?: number;
  finished_at?: string;
  finished_at_ms?: number;
  elapsed_ms: number;
  queue_duration_ms: number;
  prepare_duration_ms: number;
  run_duration_ms: number;
  context_prepare_duration_ms: number;
  session_wake_duration_ms: number;
  backend_start_duration_ms: number;
  active_duration_ms: number;
  finalize_duration_ms: number;
  result?: string;
  error?: string;
}

export interface TaskRound {
  id: string;
  task_id: string;
  sequence: number;
  input_message_id: string;
  status: string;
  created_at: string;
  created_at_ms: number;
  started_at?: string;
  started_at_ms?: number;
  finished_at?: string;
  finished_at_ms?: number;
  elapsed_ms: number;
}

export type ConversationCategory = "chat" | "update" | "tool" | "error";

export interface Attachment {
  id: string;
  task_id: string;
  message_id?: string;
  name: string;
  media_type: string;
  size: number;
  sha256: string;
  created_at: string;
}

export interface ConversationItem {
  sequence: number;
  id: string;
  task_id: string;
  round_id?: string;
  turn_id?: string;
  agent_id?: string;
  stream_agent_id?: string;
  from_agent_id?: string;
  to_agent_id?: string;
  route_kind?: string;
  category: ConversationCategory;
  kind: string;
  summary: string;
  payload?: Record<string, unknown>;
  created_at: string;
}

// Whole-set totals for the task list filter popovers, keyed by raw task status,
// project id and device filter key.
export interface TaskFacetCounts {
  status: Record<string, number>;
  project: Record<string, number>;
  device: Record<string, number>;
}

export interface TaskAgent {
  task_id: string;
  agent_id: string;
  role: "main" | "sub";
  status: string;
  title: string;
  runtime_config_snapshot_id: string;
  inherit_main: boolean;
  backend?: string;
  model_source?: "env" | "official" | "claude_native" | string;
  model_id?: string;
  model_name?: string;
  wire_model?: string;
  codex_account_id?: string;
  reasoning_effort?: string;
  stream_idle_timeout_ms?: number;
  stream_max_retries?: number;
  filesystem?: string;
  approval?: string;
  proxy_enabled: boolean;
  fast_mode: boolean;
  runtime_config_valid: boolean;
  runtime_config_error?: string;
  unread_count: number;
  created_at: string;
  updated_at: string;
}

export interface ConversationPage {
  items: ConversationItem[];
  has_more: boolean;
  next_before?: number;
  latest_sequence?: number;
}

export interface TaskMemory {
  task_id: string;
  current_goal: string;
  goal_history?: TaskGoalChange[];
  decisions?: string[];
  facts?: string[];
  excluded?: string[];
  progress?: string[];
  verification?: string[];
  next_actions?: string[];
  updated_at?: string;
}

export interface TaskGoalChange {
  sequence: number;
  id: string;
  task_id: string;
  round_id?: string;
  turn_id?: string;
  source_message_id?: string;
  goal_before?: string;
  goal_after: string;
  change_type: "set" | "revise" | "pause" | "resume" | "complete" | "reopen";
  reason: string;
  created_at: string;
}

export interface HardwareSerialConfig {
  device: string;
  baudrate: number;
}

export interface HardwareNetworkConfig {
  host: string;
  port: number;
  protocol: "telnet" | "raw" | "ssh" | "adb";
  ssh_auth: "auto" | "password" | "key";
}

export interface HardwareGroup {
  task_id: string;
  id: string;
  resource_id?: string;
  position: number;
  description: string;
  mode: "off" | "serial" | "network" | "both";
  serial: HardwareSerialConfig;
  network: HardwareNetworkConfig;
  username?: string;
  password_configured: boolean;
  adb_paired_at?: string;
  access: "read_only" | "read_write";
  created_at: string;
  updated_at: string;
}

export interface HardwareResource {
  id: string;
  description: string;
  mode: "off" | "serial" | "network" | "both";
  serial: HardwareSerialConfig;
  network: HardwareNetworkConfig;
  username?: string;
  password_configured: boolean;
  access: "read_only" | "read_write";
  binding_count: number;
  created_at: string;
  updated_at: string;
}

export interface HardwareTerminalStatus {
  task_id: string;
  hardware_id: string;
  transport: "serial" | "network";
  endpoint: string;
  status: string;
  connected: boolean;
  read_only: boolean;
  error?: string;
  started_at?: string;
  updated_at?: string;
}

export interface HardwareIOEvent {
  sequence: number;
  id: string;
  task_id: string;
  hardware_id: string;
  transport: "serial" | "network";
  direction: "rx" | "tx" | "system";
  data: string;
  encoding: string;
  source?: string;
  created_at: string;
}

export interface HardwareIOPage {
  items: HardwareIOEvent[];
  latest_sequence: number;
  has_more: boolean;
}

export interface SerialPort {
  device: string;
  description: string;
  hardware_id?: string;
}

export interface Knowledge {
  id: string;
  scope: "global" | "project";
  project_id?: string;
  bound_project_id?: string;
  binding_mode?: "project" | "external";
  can_propose_revision?: boolean;
  parent_id?: string;
  slug?: string;
  sort_order: number;
  is_index: boolean;
  type: string;
  title: string;
  body: string;
  status: string;
  confidence: number;
  branch_scope?: string;
  product_line_id?: string;
  revision: number;
  content_hash?: string;
  verified_commit?: string;
  helped_count: number;
  stale_count: number;
  feedback_state?: string;
  source_task_id?: string;
  source_turn_id?: string;
  created_at: string;
  updated_at: string;
  last_verified_at?: string;
}

export interface SSHHostKeyInfo {
	endpoint: string;
	algorithm: string;
	fingerprint: string;
}

export interface KnowledgeProposal {
  id: string;
  entry_id: string;
  base_revision: number;
  scope?: "global" | "project";
  project_id?: string;
  type?: string;
  title?: string;
  body?: string;
  status: string;
  review_mode?: "auto" | "manual" | string;
  reason?: string;
  decision_reason?: string;
  source_task_id?: string;
  source_turn_id?: string;
  created_at: string;
  updated_at?: string;
  decided_at?: string;
  base_title?: string;
  base_body?: string;
  previous_title?: string;
  previous_body?: string;
  current_title?: string;
  current_body?: string;
  base_entry?: Pick<Knowledge, "title" | "body" | "revision">;
  proposed?: Knowledge;
}

export interface KnowledgeReviewSettings {
  auto_approve: boolean;
  updated_at?: string;
}

export interface Skill {
  id: string;
  package_slug: string;
  scope: "global" | "project";
  project_id?: string;
  bound_project_id?: string;
  binding_mode?: "project" | "external";
  can_update?: boolean;
  name: string;
  description: string;
  instructions: string;
  version: number;
  status: string;
  enabled: boolean;
  auto_attach?: boolean;
  source_path?: string;
  files?: string[];
  created_at: string;
  updated_at: string;
}

export interface KnowledgeLibrary {
  id: string;
  container_project_id: string;
  name: string;
  description: string;
  source_identity?: string;
  bound_project_id?: string;
  binding_mode?: "project" | "external";
  knowledge_count: number;
  skill_count: number;
  created_at: string;
  updated_at: string;
}

export interface TaskPermissionRequest {
  id: string;
  task_id: string;
  turn_id?: string;
  applicant_identity_link_id?: string;
  applicant_name?: string;
  applicant_is_bot?: boolean;
  action: string;
  scope?: string;
  reason?: string;
  risk?: string;
  status: "pending" | "approved" | "rejected" | "expired" | string;
  decider_name?: string;
  decision_reason?: string;
  created_at: string;
  decided_at?: string;
  expires_at: string;
}

export interface TaskDetail {
  task: Task;
  latest_round?: TaskRound;
  turns: Turn[];
  agents: TaskAgent[];
  memory?: TaskMemory;
  hardware?: HardwareGroup[];
  hardware_resources?: HardwareResource[];
  permission_requests?: TaskPermissionRequest[];
  event_cursor: number;
  server_time_ms: number;
}

export interface TaskContextDetail {
  task: Task;
  latest_round?: TaskRound;
  turns: Turn[];
  memory: TaskMemory;
  context: {
    turn_id?: string;
    agent_id?: string;
    prompt?: string;
    prompt_chars?: number;
    context_window?: number;
    usage?: Record<string, number>;
    context_percent?: number;
    metrics?: {
      total_tokens?: number;
      history_tokens?: number;
      current_total_tokens?: number;
      input_tokens?: number;
      cached_input_tokens?: number;
      output_tokens?: number;
      reasoning_output_tokens?: number;
      aha_prompt_chars?: number;
      aha_prompt_tokens?: number;
      context_tokens?: number;
      context_window?: number;
      context_percent?: number;
      session_size_bytes?: number;
      session_exists?: boolean;
      session_active?: boolean;
      session_id?: string;
      session_status?: string;
      session_count?: number;
    };
  };
}

export interface PromptTemplate {
  id: string;
  name: string;
  layer: "core" | "role" | "identity" | "channel" | "policy" | "protocol" | string;
  description: string;
  content: string;
  source: "builtin" | "override" | string;
  editable: boolean;
  required: boolean;
  version: number;
  updated_at?: string;
}

export interface ChannelPlugin {
  id: string;
  provider_key: string;
  display_name: string;
  manifest_version: number;
  package_version: string;
  protocol_min: number;
  protocol_max: number;
  executable_sha256?: string;
  install_state: "installed" | "missing" | "invalid" | "incompatible" | string;
  enabled: boolean;
  revision: number;
  last_error?: string;
  available: boolean;
  updated_at: string;
}

export interface ChannelInstance {
  id: string;
  plugin_id: string;
  provider_key?: string;
  owner_id: string;
  runtime_device_id: string;
  name: string;
  status: "draft" | "onboarding" | "ready" | "degraded" | "disabled" | "error" | "retired" | string;
  effective_availability: "available" | "unavailable" | string;
  revision: number;
  app_id?: string;
  provider_tenant_id?: string;
  credential_configured: boolean;
	owner_bound: boolean;
  host_project_id: string;
  host_workspace_id: string;
  config?: Record<string, unknown>;
  last_seen_at?: string;
  retired_at?: string;
  retired: boolean;
  created_at: string;
  updated_at: string;
}

export interface ChannelPurgePreview {
  name: string;
  tasks: number;
  conversations: number;
  messages: number;
  attachments: number;
}

export interface ChannelEndpoint {
  id: string;
  instance_id: string;
  kind: "assistant_dm" | "group_digital_human";
  enabled: boolean;
  config?: Record<string, unknown>;
  created_at: string;
  updated_at: string;
}

export interface ChannelTaskRoute {
  id: string;
  instance_id: string;
  conversation_id: string;
  target_task_id: string;
  state: string;
  revision: number;
  activated_at?: string;
}

export interface ChannelDestination {
  conversation_id: string;
  instance_id: string;
  instance_name: string;
  provider_key: string;
  endpoint_kind: "assistant_dm" | "group_digital_human";
  display_name: string;
  status: string;
  route_id?: string;
  route_revision?: number;
  target_task_id?: string;
  target_task_code?: string;
  target_task_name?: string;
  updated_at: string;
}

export interface ChannelConversationSummary {
  id: string;
  endpoint_kind: "assistant_dm" | "group_digital_human";
  display_name: string;
  status: string;
  bound_task_id?: string;
  bound_task_code?: string;
  bound_task_title?: string;
  inbound_count: number;
  outbound_count: number;
  last_inbound_at?: string;
  last_inbound_state?: string;
  last_inbound_outcome?: string;
  last_outbound_at?: string;
  last_outbound_state?: string;
  last_outbound_error?: string;
}

export interface ChannelContact {
  identity_link_id: string;
  display_name: string;
  role: "participant";
  collaboration_role?: string;
  is_bot: boolean;
  last_seen_at: string;
}

export interface ChannelGroupMember {
  identity_link_id: string;
  display_name: string;
  is_bot: boolean;
  source: "provider" | "observed";
  is_self?: boolean;
  last_seen_at?: string;
}

export interface ChannelOnboardingSession {
  id: string;
  instance_id: string;
  mode: "register_app" | "existing_app";
  registration_command_id: string;
  verification_url?: string;
  status: "pending" | "qr_ready" | "completing" | "succeeded" | "failed" | "cancelled" | "expired" | string;
  step: string;
  expires_at: string;
  consumed_at?: string;
  created_at: string;
  updated_at: string;
}

export interface ChannelOnboardingPermission {
  id: string;
  name: string;
  description: string;
  required: boolean;
  enabled: boolean;
}

export interface ChannelOnboardingOptions {
  mode: "register_app" | "existing_app";
  permission_catalog_version: number;
  permissions: ChannelOnboardingPermission[];
}

export interface ChannelHandoff {
  id: string;
  instance_id: string;
  summary: string;
  details?: string;
  state: string;
  decision?: string;
  created_task_id?: string;
  created_at: string;
  updated_at: string;
}

export interface ChannelDelivery {
  id: string;
  instance_id: string;
  stream_sequence: number;
  replay_generation: number;
  replay_of_id?: string;
  semantic_payload: Record<string, unknown>;
  state: string;
  attempts: number;
  last_error_code?: string;
  outcome_certainty?: string;
  created_at: string;
  delivered_at?: string;
}

export interface ChannelDeliveryAttempt {
  id: string;
  delivery_id: string;
  attempt_no: number;
  outcome: string;
  error_code?: string;
  retry_after_ms?: number;
  provider_request_id?: string;
  started_at: string;
  finished_at?: string;
}

export interface OwnerAssistant {
  id: string;
  name: string;
  model_id: string;
  character: string;
  pet_enabled: boolean;
  brain_model_id: string;
  pet_actions: string[];
  pet_identity: string;
  pet_principles: string;
  pet_idle_minutes: number;
  pet_quiet_enabled: boolean;
  pet_quiet_start: number;
  pet_quiet_end: number;
  pet_hourly_limit: number;
  pet_task_id?: string;
  pet_allow_view_tasks: boolean;
  pet_allow_create_tasks: boolean;
  pet_allow_create_workspaces: boolean;
  pet_allow_message_tasks: boolean;
  pet_allow_manage_models: boolean;
  pet_allow_manage_knowledge: boolean;
  pet_allow_manage_channels: boolean;
  pet_allow_manage_system: boolean;
  created_at: string;
  updated_at: string;
}

export interface AssistantMessage {
  id: string;
  assistant_id: string;
  role: "user" | "assistant" | string;
  content: string;
  created_at: string;
}

export interface AssistantToolEvent {
  name: string;
  status: "ok" | "error" | "warning" | string;
  message: string;
  arguments?: string;
}

export interface PetToolInfo {
  name: string;
  description: string;
}

export interface AssistantModelOption {
  id: string;
  display_name: string;
  provider_name?: string;
  wire_api: string;
}

export interface PetPrompts {
  chat: string;
  brain: string;
}

export interface PetEvent {
  id: string;
  at: string;
  device_id: string;
  actor: string;
  kind: string;
  page: string;
  detail: string;
  status: string;
}

export interface PetResetResult {
  tasks: number;
  workspaces: number;
  approvals: number;
  events: number;
  watches: number;
  messages: number;
  hints: number;
  warnings?: string[];
}

export interface PetBrainAction {
  type: "play" | "move_to" | "walk_path" | "open_task" | string;
  name?: string;
  zone?: string;
  task?: string;
  x?: number;
  y?: number;
  radius?: number;
  turns?: number;
  points?: Array<{x: number; y: number} | [number, number]>;
  duration_ms?: number;
  active?: boolean;
}

export interface PetLayoutRect {
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface PetLayoutElement extends PetLayoutRect {
  name: string;
}

export interface PetLayout {
  viewport_width: number;
  viewport_height: number;
  scroll_y: number;
  pet: PetLayoutRect;
  panel?: PetLayoutRect;
  elements?: PetLayoutElement[];
}

export interface PetBrainResult {
  ok: boolean;
  say: string;
  actions: PetBrainAction[];
}

export interface PetState {
  id: string;
  character: string;
  mood: string;
  action: string;
  zone: string;
  x: number;
  y: number;
  facing: number;
  sleeping: boolean;
  quiet: boolean;
  version: number;
  updated_at: string;
}

export interface PetStatePatch {
  mood?: string;
  action?: string;
  zone?: string;
  x?: number;
  y?: number;
  facing?: number;
  sleeping?: boolean;
  quiet?: boolean;
  version?: number;
}

export interface AssistantHint {
  kind: string;
  target_type: "task" | "channel" | string;
  target_id: string;
  title: string;
  body: string;
  priority: number;
}

export interface OwnerAssistantView {
  assistant: OwnerAssistant;
  messages: AssistantMessage[];
  models: AssistantModelOption[];
  model_name?: string;
  brain_model_name?: string;
  tools?: PetToolInfo[];
  pet_last_brain_at?: string;
  pet_next_brain_at?: string;
  pet_brain_runs?: number;
}

export interface MCPTool {
  name: string;
  description?: string;
  input_schema?: Record<string, unknown>;
}

export interface MCPServer {
  id: string;
  name: string;
  description: string;
  transport: "http" | "stdio" | string;
  url?: string;
  command?: string;
  args?: string[];
  env?: Record<string, string>;
  headers?: Record<string, string>;
  tools?: MCPTool[];
  disabled_tools?: string[];
  enabled: boolean;
  builtin: boolean;
  auto_inject?: boolean;
  health?: string;
  last_error?: string;
  last_checked_at?: string;
  created_at: string;
  updated_at: string;
}

export interface PetMarkdownEntry {
  date: string;
  content: string;
}

export interface PetWorkspaceDocument {
  name: string;
  content: string;
}

export interface PetApproval {
  id: string;
  task_id: string;
  kind: string;
  title: string;
  summary: string;
  reason?: string;
  risk: string;
  payload?: string;
  status: "pending" | "approved" | "rejected" | string;
  resolution?: string;
  decision_reason?: string;
  created_at: string;
  resolved_at?: string;
}

export interface PetTaskWatch {
  task_id: string;
  code: string;
  title: string;
  status: string;
  watched: boolean;
  reason: string;
  ignored: boolean;
  ignored_at?: string;
  created_at: string;
  updated_at: string;
}

export interface PetChannelHealth {
  id: string;
  name: string;
  status: string;
  effective_availability?: string;
  pending: number;
  stuck_pending: number;
  queued_pending: number;
  attempted_pending: number;
  expired_leases: number;
  oldest_pending_minutes: number;
  oldest_unattempted_minutes: number;
  oldest_queued_minutes: number;
  oldest_attempted_minutes: number;
  expired_lease_minutes: number;
  dead_letter: number;
  last_error_code?: string;
  degraded: boolean;
  note?: string;
}

export interface PetInspection {
  checked_at: string;
  version?: string;
  web_version?: string;
  started_at?: string;
  uptime_minutes?: number;
  health: string;
  tasks_total: number;
  active: number;
  waiting_user: number;
  failed: number;
  blocked: number;
  completed: number;
  pending_approvals: number;
  degraded_channels?: string[];
  degraded_channel_count: number;
  channels?: PetChannelHealth[];
  channels_total?: number;
  open_tasks: number;
  attention_tasks: number;
  note?: string;
}

export interface PetApprovalCount {
  kind: string;
  status: string;
  count: number;
}

export interface PetWorkbench {
  core: PetWorkspaceDocument[];
  worklog: PetMarkdownEntry[];
  worklog_total?: number;
  approvals: PetApproval[];
  approval_total?: number;
  approval_counts?: PetApprovalCount[];
  watches: PetTaskWatch[];
  task?: Task;
  channels: PetChannelHealth[];
  channel_total?: number;
  channel_note?: string;
  inspection?: PetInspection;
  updated_at: string;
}

// --- Mail resources -------------------------------------------------------

export interface MailAccountAlias {
  address: string;
  display_name?: string;
  position?: number;
}

export interface MailAccount {
  id: string;
  name: string;
  address: string;
  display_name: string;
  provider: string;
  smtp_host: string;
  smtp_port: number;
  smtp_security: string;
  smtp_username: string;
  credential_configured: boolean;
  status: "unverified" | "ok" | "error" | "disabled" | string;
  last_test_at?: string;
  last_test_error_code?: string;
  aliases?: MailAccountAlias[];
  created_at: string;
  updated_at: string;
}

export interface MailTemplate {
  id: string;
  scope: "global" | "project";
  project_id?: string;
  name: string;
  description?: string;
  mode?: "guide" | "render" | string;
  subject_template: string;
  body_text_template: string;
  body_html_template: string;
  default_to: string[];
  default_cc: string[];
  required_variables: string[];
  current_version: number;
  status: "active" | "archived" | string;
  created_at: string;
  updated_at: string;
}

export interface MailTemplateVersion {
  template_id: string;
  version: number;
  mode?: "guide" | "render" | string;
  subject_template: string;
  body_text_template: string;
  body_html_template: string;
  required_variables: string[];
  created_at: string;
}

export interface MailBinding {
  id: string;
  scope_type: "project" | "workspace" | "task";
  scope_id: string;
  account_id: string;
  default_template_id?: string;
  approval_mode: string;
  allowed_recipients: string[];
  auto_send_enabled: boolean;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface MailMessage {
  id: string;
  task_id: string;
  project_id?: string;
  workspace_id?: string;
  turn_id?: string;
  agent_id?: string;
  account_id: string;
  binding_id?: string;
  template_id?: string;
  template_version: number;
  source: "agent" | "owner";
  idempotency_key: string;
  from_address: string;
  from_name?: string;
  to: string[];
  cc?: string[];
  bcc?: string[];
  reply_to?: string;
  subject: string;
  body_text?: string;
  body_html?: string;
  content_revision: number;
  approval_digest?: string;
  status: "draft" | "pending_approval" | "queued" | "sending" | "sent" | "failed" | "dead_letter" | "rejected" | "cancelled" | string;
  approved_by?: string;
  approved_at?: string;
  rejected_reason?: string;
  submitted_at?: string;
  created_at: string;
  updated_at: string;
}

export interface MailAttachment {
  id: string;
  message_id: string;
  position: number;
  source_type: "task_attachment" | "build_artifact";
  source_id: string;
  file_name: string;
  media_type: string;
  size_bytes: number;
  sha256: string;
  delivery_mode: "direct" | "link";
  disposition: "attachment" | "inline";
  link_expires_at?: string;
  link_max_downloads?: number;
  link_download_count?: number;
  link_revoked_at?: string;
  created_at: string;
}

export interface MailDelivery {
  id: string;
  message_id: string;
  generation: number;
  account_id: string;
  state: "queued" | "sending" | "sent" | "failed" | "dead_letter" | "cancelled" | string;
  attempts: number;
  available_at: string;
  smtp_phase?: string;
  message_id_header?: string;
  last_error_code?: string;
  outcome_certainty?: string;
  sent_at?: string;
  created_at: string;
  updated_at: string;
}

export interface MailDeliveryAttempt {
  id: string;
  delivery_id: string;
  attempt_no: number;
  outcome: string;
  error_code?: string;
  smtp_code?: number;
  smtp_phase?: string;
  backoff_ms?: number;
  started_at: string;
  finished_at?: string;
}

export interface BuildArtifact {
  id: string;
  task_id: string;
  label: string;
  package_name?: string;
  version?: string;
  size_bytes: number;
  sha256: string;
  storage_type: string;
  attachment_id?: string;
  registered_by?: string;
  created_at: string;
}

export interface TaskMailAccount {
  account_id: string;
  binding_id: string;
  binding_scope: "task" | "workspace" | "project" | string;
  from_address: string;
  display_name: string;
  aliases: string[];
  credential_configured: boolean;
  status: string;
  default_template_id?: string;
  approval_mode?: string;
  allowed_recipients?: string[];
}

export interface MailMessageCard {
  message: MailMessage;
  attachments: MailAttachment[];
  deliveries: MailDelivery[];
}

export interface TaskMailOptions {
  ok: boolean;
  task_id: string;
  accounts: TaskMailAccount[];
  templates: MailTemplate[];
  attachments: Attachment[];
  build_artifacts: BuildArtifact[];
  messages: MailMessageCard[];
}
