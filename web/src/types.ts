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
  isolation?: string;
  worktree_dir?: string;
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
  created_at: string;
  updated_at: string;
}

export interface Turn {
  id: string;
  task_id: string;
  sequence: number;
  status: string;
  waiting_reason?: string;
  backend_session_id?: string;
  queued_at: string;
  started_at?: string;
  finished_at?: string;
  result?: string;
  error?: string;
}

export interface Message {
  id: string;
  task_id: string;
  turn_id?: string;
  role: "user" | "assistant" | string;
  sender: string;
  content: string;
  created_at: string;
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
  messages: Message[];
  turns: Turn[];
  memory: TaskMemory;
  knowledge_candidates: Knowledge[];
}
