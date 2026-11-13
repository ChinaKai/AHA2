// Supported agent backends shown on the Models page. A backend is the agent
// runtime that executes a Turn (Claude Code, Codex CLI, DeepSeek Harness); it
// is not a Provider and not a model wire protocol. Keep this catalog static:
// the panel must render without probing versions or making network requests.
// The runtime option lists below derive from this file so the Models page and
// the runtime pickers cannot drift apart.

export type ModelWire = "responses" | "chat_completions" | "anthropic_messages";

export interface BackendProtocol {
  wire: ModelWire;
  label: string;
}

export interface BackendInstall {
  platform: string;
  command: string;
}

export interface SupportedBackend {
  id: "claude" | "codex" | "dsh";
  name: string;
  tagline: string;
  protocols: BackendProtocol[];
  docURL: string;
  requirement?: string;
  installs: BackendInstall[];
}

export interface ModelWireOption {
  value: ModelWire;
  label: string;
  backends: string[];
}

const WIRE_LABELS: Array<{wire: ModelWire; label: string}> = [
  {wire: "responses", label: "Responses"},
  {wire: "chat_completions", label: "Chat Completions"},
  {wire: "anthropic_messages", label: "Anthropic Messages"},
];

export const SUPPORTED_BACKENDS: readonly SupportedBackend[] = [
  {
    id: "claude",
    name: "Claude Code",
    tagline: "Anthropic 官方 agent CLI，通过 Anthropic Messages 协议运行。",
    protocols: [{wire: "anthropic_messages", label: "Anthropic Messages"}],
    docURL: "https://code.claude.com/docs/en/overview",
    installs: [
      {platform: "Windows PowerShell", command: "irm https://claude.ai/install.ps1 | iex"},
      {platform: "macOS / Linux / WSL", command: "curl -fsSL https://claude.ai/install.sh | bash"},
    ],
  },
  {
    id: "codex",
    name: "Codex CLI",
    tagline: "OpenAI 官方 agent CLI，通过 Responses 或 Chat Completions 运行。",
    protocols: [
      {wire: "responses", label: "Responses"},
      {wire: "chat_completions", label: "Chat Completions"},
    ],
    docURL: "https://learn.chatgpt.com/docs/codex/cli",
    installs: [
      {platform: "macOS / Linux", command: "curl -fsSL https://chatgpt.com/codex/install.sh | sh"},
    ],
  },
  {
    id: "dsh",
    name: "DeepSeek Harness",
    tagline: "DeepSeek Harness（DSH）agent 运行时，三种模型协议都支持。",
    protocols: [
      {wire: "responses", label: "Responses"},
      {wire: "chat_completions", label: "Chat Completions"},
      {wire: "anthropic_messages", label: "Anthropic Messages"},
    ],
    docURL: "https://deepseek-harness.github.io/deepseek-harness/",
    requirement: "Node.js >= 24.9.0",
    installs: [
      {platform: "任意平台", command: "npx @deepseek-ai/dsh web"},
    ],
  },
];

// MODEL_WIRE_OPTIONS is the single source for the model-add protocol picker.
// Deriving the backend lists from the catalog means a new backend cannot appear
// in the runtime matrix without also having a catalog entry (and vice versa).
export const MODEL_WIRE_OPTIONS: ModelWireOption[] = WIRE_LABELS.map(item => ({
  value: item.wire,
  label: item.label,
  backends: SUPPORTED_BACKENDS.filter(backend => backend.protocols.some(protocol => protocol.wire === item.wire)).map(backend => backend.id),
}));

export function supportedBackendName(id: string): string {
  return SUPPORTED_BACKENDS.find(backend => backend.id === id)?.name ?? id;
}

export function backendSelectOptionsHTML(): string {
  return SUPPORTED_BACKENDS.map(backend => `<option value="${escapeHTML(backend.id)}">${escapeHTML(backend.name)}</option>`).join("");
}

function escapeHTML(value: string): string {
  return value.replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;").replaceAll('"', "&quot;");
}

function protocolTagsHTML(protocols: BackendProtocol[]): string {
  return protocols
    .map(protocol => `<span class="backend-proto" data-wire="${escapeHTML(protocol.wire)}">${escapeHTML(protocol.label)}</span>`)
    .join("");
}

function installsHTML(backend: SupportedBackend): string {
  const requirement = backend.requirement
    ? `<div class="backend-install backend-requirement"><span>运行要求</span><code>${escapeHTML(backend.requirement)}</code></div>`
    : "";
  const commands = backend.installs
    .map(install => `<div class="backend-install" data-platform="${escapeHTML(install.platform)}"><span>${escapeHTML(install.platform)}</span><code>${escapeHTML(install.command)}</code></div>`)
    .join("");
  return `${requirement}${commands}`;
}

// backendsPanelHTML renders the read-only "supported backends" panel for the
// Models page. Links open in a new tab; install commands are text only.
export function backendsPanelHTML(): string {
  const cards = SUPPORTED_BACKENDS.map(backend => `<article class="backend-card" data-backend-id="${escapeHTML(backend.id)}">
      <div class="backend-card-head">
        <div class="backend-identity"><strong>${escapeHTML(backend.name)}</strong><code class="backend-identifier">${escapeHTML(backend.id)}</code></div>
        <a class="backend-doc-link" href="${escapeHTML(backend.docURL)}" target="_blank" rel="noopener noreferrer">官方文档</a>
      </div>
      <p class="backend-tagline">${escapeHTML(backend.tagline)}</p>
      <div class="backend-protocols">${protocolTagsHTML(backend.protocols)}</div>
      ${installsHTML(backend)}
    </article>`).join("");
  return `<section class="panel spaced supported-backends-panel" id="supported-backends" aria-labelledby="supported-backends-title">
    <div class="panel-head"><strong id="supported-backends-title">AHA 支持的 Backend</strong><span>Agent 运行时，不是 Provider 或模型协议</span></div>
    <div class="supported-backends-list">${cards}</div>
  </section>`;
}
