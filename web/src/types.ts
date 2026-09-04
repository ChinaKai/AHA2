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
}

export interface Project {
  id: string;
  name: string;
  description: string;
  project_type?: string;
  repository_identity?: string;
  default_branch?: string;
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
  distro?: string;
  platform?: string;
  health: string;
  capabilities?: Record<string, unknown>;
}

export interface Model {
  id: string;
  display_name: string;
  provider_id: string;
  backend: string;
  wire_model: string;
  wire_api?: string;
  context_window?: number;
  max_output_tokens?: number;
  default_reasoning_effort?: string;
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
  created_at: string;
  updated_at: string;
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
  started_at?: string;
  started_at_ms?: number;
  finished_at?: string;
  finished_at_ms?: number;
  elapsed_ms: number;
  queue_duration_ms: number;
  prepare_duration_ms: number;
  run_duration_ms: number;
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
  model_id?: string;
  model_name?: string;
  reasoning_effort?: string;
  filesystem?: string;
  approval?: string;
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
  type: string;
  title: string;
  body: string;
  status: string;
  confidence: number;
  branch_scope?: string;
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
