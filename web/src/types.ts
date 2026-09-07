export interface AuthStatus {
  ok: boolean;
  authenticated: boolean;
  registration_open: boolean;
  owner_id?: string;
  username?: string;
  csrf_token?: string;
}
export interface SystemInfo {
  os: string;
  arch: string;
  wsl_available: boolean;
  wsl_distros: string[];
	version?: string;
	started_at?: string;
}

export interface ProxySettings {
  http_proxy: string;
  https_proxy: string;
  no_proxy: string;
  updated_at?: string;
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
	completed: number;
	total: number;
	error?: string;
	started_at?: string;
	updated_at?: string;
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
}

export interface Model {
  id: string;
  display_name: string;
  provider_id: string;
  provider_name?: string;
  source: "provider" | "official" | string;
  codex_account_id?: string;
  backend: string;
  wire_model: string;
  wire_api?: string;
  context_window?: number;
  max_output_tokens?: number;
  default_reasoning_effort?: string;
}

export interface WorkspaceProbe {
  status?: "ready" | "unavailable" | "not_installed" | "execution_failed" | string;
  version?: string;
  error?: string;
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
  total_tokens: number;
  created_at: string;
  updated_at: string;
  owner_device_id?: string;
  read_only?: boolean;
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

export interface TaskAgent {
  task_id: string;
  agent_id: string;
  role: "main" | "sub";
  status: string;
  title: string;
  runtime_config_snapshot_id: string;
  inherit_main: boolean;
  backend?: string;
  model_source?: "env" | "official" | string;
  model_id?: string;
  model_name?: string;
  wire_model?: string;
  codex_account_id?: string;
  reasoning_effort?: string;
  filesystem?: string;
  approval?: string;
  proxy_enabled: boolean;
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
  decisions: string[];
  facts: string[];
  excluded: string[];
  progress: string[];
  verification: string[];
  next_actions: string[];
}

export interface HardwareSerialConfig {
  device: string;
  baudrate: number;
}

export interface HardwareNetworkConfig {
  host: string;
  port: number;
  protocol: "telnet" | "raw" | "ssh";
  ssh_auth: "auto" | "password" | "key";
}

export interface HardwareGroup {
  task_id: string;
  id: string;
  position: number;
  description: string;
  mode: "off" | "serial" | "network" | "both";
  serial: HardwareSerialConfig;
  network: HardwareNetworkConfig;
  username?: string;
  password_configured: boolean;
  access: "read_only" | "read_write";
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

export interface Skill {
  id: string;
  package_slug: string;
  scope: "global" | "project";
  project_id?: string;
  bound_project_id?: string;
  name: string;
  description: string;
  instructions: string;
  version: number;
  status: string;
  enabled: boolean;
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
  knowledge_count: number;
  skill_count: number;
  created_at: string;
  updated_at: string;
}

export interface TaskDetail {
  task: Task;
  latest_round?: TaskRound;
  turns: Turn[];
  agents: TaskAgent[];
  memory?: TaskMemory;
  hardware?: HardwareGroup[];
  event_cursor: number;
  server_time_ms: number;
}

export interface TaskContextDetail {
  task: Task;
  latest_round?: TaskRound;
  turns: Turn[];
  memory: TaskMemory;
  project_knowledge: Knowledge[];
  global_knowledge: Knowledge[];
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
