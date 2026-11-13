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

export interface SupportedBackend {
  id: "claude" | "codex" | "dsh";
  name: string;
  tagline: string;
  protocols: BackendProtocol[];
  docURL: string;
}

export interface ModelWireOption {
  value: ModelWire;
  label: string;
  backends: string[];
}

export interface ModelWireEditOption {
  value: string;
  label: string;
  runtime: ModelWire;
}

export interface BackendOptionInput {
  capabilities?: Record<string, {status?: string}>;
  detected: boolean;
  presentBackends: Iterable<string>;
  hasCodexAccount: boolean;
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

// Runtime pickers historically list Codex first; keep that order while making
// the catalog the membership source. Backends added to the catalog later append
// here automatically.
const PREFERRED_RUNTIME_ORDER = ["codex", "claude", "dsh"];
export const SUPPORTED_BACKEND_IDS: string[] = [
  ...PREFERRED_RUNTIME_ORDER.filter(id => SUPPORTED_BACKENDS.some(backend => backend.id === id)),
  ...SUPPORTED_BACKENDS.map(backend => backend.id).filter(id => !PREFERRED_RUNTIME_ORDER.includes(id)),
];

// resolveBackendOptions decides which backends a Task may select. Detected
// workspaces contribute their ready backends; before detection, fall back to
// backends that already have models or accounts configured.
export function resolveBackendOptions(input: BackendOptionInput): string[] {
  const capabilities = input.capabilities;
  if (capabilities) {
    const ready = SUPPORTED_BACKEND_IDS.filter(id => capabilities[id]?.status === "ready");
    if (ready.length) return ready;
    if (input.detected) return ready;
  }
  const present = new Set(input.presentBackends);
  if (input.hasCodexAccount) present.add("codex");
  return SUPPORTED_BACKEND_IDS.filter(id => present.has(id));
}

// The API persists Chat Completions as "chat"; probes and add-model requests
// use the wire name "chat_completions". Keep both directions in one place.
export function persistedWireValue(wire: string): string {
  return wire === "chat_completions" ? "chat" : wire;
}

export function runtimeWireValue(wire: string): ModelWire {
  return (wire === "chat" ? "chat_completions" : wire) as ModelWire;
}

export const MODEL_WIRE_EDIT_OPTIONS: ModelWireEditOption[] = MODEL_WIRE_OPTIONS.map(option => ({
  value: persistedWireValue(option.value),
  label: option.label,
  runtime: option.value,
}));

export function modelWireEditOptionsHTML(): string {
  return MODEL_WIRE_EDIT_OPTIONS.map(option => `<option value="${escapeHTML(option.value)}">${escapeHTML(option.label)}</option>`).join("");
}

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

// backendsPanelHTML renders the read-only "supported backends" panel for the
// Models page. Links open in a new tab; installation is left to the linked docs.
export function backendsPanelHTML(): string {
  const cards = SUPPORTED_BACKENDS.map(backend => `<article class="backend-card" data-backend-id="${escapeHTML(backend.id)}">
      <div class="backend-card-head">
        <div class="backend-identity"><strong>${escapeHTML(backend.name)}</strong><code class="backend-identifier">${escapeHTML(backend.id)}</code></div>
        <a class="backend-doc-link" href="${escapeHTML(backend.docURL)}" target="_blank" rel="noopener noreferrer">官方文档</a>
      </div>
      <p class="backend-tagline">${escapeHTML(backend.tagline)}</p>
      <div class="backend-protocols">${protocolTagsHTML(backend.protocols)}</div>
    </article>`).join("");
  return `<section class="panel spaced supported-backends-panel" id="supported-backends" aria-labelledby="supported-backends-title">
    <div class="panel-head"><strong id="supported-backends-title">AHA 支持的 Backend</strong><span>Agent 运行时，不是 Provider 或模型协议</span></div>
    <div class="supported-backends-list">${cards}</div>
  </section>`;
}
