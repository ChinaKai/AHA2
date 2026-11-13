// Supported agent backends shown on the Models page. A backend is the agent
// runtime that executes a Turn (Claude Code, Codex CLI, DeepSeek Harness); it
// is not a Provider and not a model wire protocol. Keep this catalog static:
// the panel must render without probing versions or making network requests.

export interface BackendProtocol {
  wire: "responses" | "chat_completions" | "anthropic_messages";
  label: string;
}

export interface SupportedBackend {
  id: "claude" | "codex" | "dsh";
  name: string;
  tagline: string;
  protocols: BackendProtocol[];
  docURL: string;
  installCommand: string;
}

export const SUPPORTED_BACKENDS: readonly SupportedBackend[] = [
  {
    id: "claude",
    name: "Claude Code",
    tagline: "Anthropic 官方 agent CLI，通过 Anthropic Messages 协议运行。",
    protocols: [{wire: "anthropic_messages", label: "Anthropic Messages"}],
    docURL: "https://code.claude.com/docs/en/overview",
    installCommand: "irm https://claude.ai/install.ps1 | iex",
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
    installCommand: "curl -fsSL https://chatgpt.com/codex/install.sh | sh",
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
    installCommand: "npm install -g @deepseek-ai/dsh",
  },
];

function escapeHTML(value: string): string {
  return value.replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;").replaceAll('"', "&quot;");
}

function protocolTagsHTML(protocols: BackendProtocol[]): string {
  return protocols
    .map(protocol => `<span class="backend-proto" data-wire="${escapeHTML(protocol.wire)}">${escapeHTML(protocol.label)}</span>`)
    .join("");
}

// backendsPanelHTML renders the read-only "supported backends" panel for the
// Models page. Links open in a new tab; the install command is text only.
export function backendsPanelHTML(): string {
  const cards = SUPPORTED_BACKENDS.map(backend => `<article class="backend-card" data-backend-id="${escapeHTML(backend.id)}">
      <div class="backend-card-head">
        <div class="backend-identity"><strong>${escapeHTML(backend.name)}</strong><code class="backend-identifier">${escapeHTML(backend.id)}</code></div>
        <a class="backend-doc-link" href="${escapeHTML(backend.docURL)}" target="_blank" rel="noopener noreferrer">官方文档</a>
      </div>
      <p class="backend-tagline">${escapeHTML(backend.tagline)}</p>
      <div class="backend-protocols">${protocolTagsHTML(backend.protocols)}</div>
      <div class="backend-install"><span>安装命令</span><code>${escapeHTML(backend.installCommand)}</code></div>
    </article>`).join("");
  return `<section class="panel spaced supported-backends-panel" id="supported-backends" aria-labelledby="supported-backends-title">
    <div class="panel-head"><strong id="supported-backends-title">AHA 支持的 Backend</strong><span>Agent 运行时，不是 Provider 或模型协议</span></div>
    <div class="supported-backends-list">${cards}</div>
  </section>`;
}
