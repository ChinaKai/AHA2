import {api} from "./api.js";
import type {KnowledgeVaultSettings} from "./types.js";

// Phase 3 settings panel. Kept in its own lazy chunk so the first-screen bundle
// does not pay for copy and markup the Owner only sees in 高级设置 → 其他设置.

export interface KnowledgeVaultSettingsContext {
  render: () => void;
  setMessage: (kind: "notice" | "error", message: string) => void;
  // Lets the shell keep the knowledge-page probe and its module cache in step
  // with the runtime switch without a second request.
  onSettingsApplied: (settings: KnowledgeVaultSettings) => void;
}

interface SettingsPanelState {
  loaded: boolean;
  loading: boolean;
  settings: KnowledgeVaultSettings | null;
  error: string;
  unsupported: boolean;
  busy: "" | "toggle" | "init";
}

let state: SettingsPanelState = {loaded: false, loading: false, settings: null, error: "", unsupported: false, busy: ""};
let activeContext: KnowledgeVaultSettingsContext | null = null;
let pendingLoad: Promise<void> | null = null;

function escapeHTML(value: unknown): string {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function applySettings(settings: KnowledgeVaultSettings, context: KnowledgeVaultSettingsContext | null): void {
  state.settings = settings;
  state.loaded = true;
  state.error = "";
  state.unsupported = false;
  context?.onSettingsApplied(settings);
}

async function loadSettings(context: KnowledgeVaultSettingsContext | null): Promise<void> {
  if (pendingLoad) return pendingLoad;
  state.loading = true;
  state.error = "";
  pendingLoad = (async () => {
    try {
      const result = await api.knowledgeVaultSettings();
      applySettings(result.settings, context);
    } catch (error) {
      const status = (error as {status?: number} | null)?.status;
      if (status === 404) {
        state.unsupported = true;
        state.settings = null;
      } else {
        state.error = errorMessage(error);
      }
      // Mark the attempt as settled so render() does not immediately retry a
      // failing request in a loop; the retry button and page re-entry call
      // loadSettings explicitly.
      state.loaded = true;
    } finally {
      state.loading = false;
      pendingLoad = null;
      context?.render();
    }
  })();
  return pendingLoad;
}

// Called on entering the settings page when the chunk is already loaded. The
// first render in a session starts its own fetch, so the two never overlap.
export async function refreshKnowledgeVaultSettingsPanel(context: KnowledgeVaultSettingsContext): Promise<void> {
  activeContext = context;
  await loadSettings(context);
}

async function setEnabled(enabled: boolean): Promise<void> {
  const context = activeContext;
  if (state.busy || context === null || state.settings?.enabled === enabled) return;
  state.busy = "toggle";
  state.error = "";
  context.render();
  try {
    const result = await api.updateKnowledgeVaultSettings(enabled);
    applySettings(result.settings, context);
    context.setMessage("notice", enabled ? "文件知识库已启用" : "文件知识库已关闭");
  } catch (error) {
    state.error = errorMessage(error);
    context.setMessage("error", `切换文件知识库失败：${state.error}`);
  } finally {
    state.busy = "";
    context.render();
  }
}

async function initializeSamples(): Promise<void> {
  const context = activeContext;
  if (state.busy || context === null) return;
  const confirmed = window.confirm("将创建知识库目录骨架和 4 篇示例文档，并自动启用文件知识库。示例仅用于体验，可随时删除；不会导入或修改旧知识库。继续？");
  if (!confirmed) return;
  state.busy = "init";
  state.error = "";
  context.render();
  try {
    const result = await api.initializeKnowledgeVaultSamples();
    applySettings(result.settings, context);
    context.setMessage("notice", `已创建 ${result.samples} 个示例文档，文件知识库已启用`);
  } catch (error) {
    state.error = errorMessage(error);
    context.setMessage("error", `初始化示例知识库失败：${state.error}`);
    await loadSettings(context);
  } finally {
    state.busy = "";
    context.render();
  }
}

function settingsBody(): string {
  const settings = state.settings;
  if (state.unsupported) {
    return `<div class="field-help knowledge-vault-note">当前 AHA2 版本没有文件知识库设置接口；升级后这里会出现运行态开关。</div>`;
  }
  if (state.error && !settings) {
    return `<div class="security-warning active" role="alert">读取文件知识库状态失败：${escapeHTML(state.error)}</div>
      <div class="knowledge-vault-actions"><button type="button" id="knowledge-vault-retry">重试</button></div>`;
  }
  if (!settings) {
    return `<div class="field-help knowledge-vault-note" role="status">正在读取文件知识库状态…</div>`;
  }
  const busy = state.busy;
  const vaultStatus = !settings.vault_initialized
    ? "未初始化"
    : settings.vault_ready ? `已就绪${settings.vault_name ? `（${escapeHTML(settings.vault_name)}）` : ""}` : "目录不可读";
  const rebuiltAt = settings.rebuilt_at ? new Date(settings.rebuilt_at) : null;
  const rebuiltLabel = rebuiltAt && !Number.isNaN(rebuiltAt.getTime()) ? rebuiltAt.toLocaleString() : settings.rebuilt_at;
  const indexStatus = settings.index_ready
    ? `已就绪 · ${settings.entry_count} 条${rebuiltLabel ? ` · 最近重建 ${escapeHTML(rebuiltLabel)}` : ""}`
    : "未就绪";
  const source = settings.source === "database" ? "数据库设置" : "启动参数";
  const warning = settings.warning === "vault_missing"
    ? `<div class="security-warning">知识库目录尚未创建。可以先打开开关查看空态，或初始化示例知识库。</div>`
    : settings.warning === "index_missing"
      ? `<div class="security-warning">索引尚未建立。请在知识库页的齿轮设置中重建索引，或初始化示例知识库。</div>`
      : "";
  return `
    <label class="security-setting-toggle knowledge-vault-toggle"><input id="knowledge-vault-enabled" name="knowledge_vault_enabled" type="checkbox" ${settings.enabled ? "checked" : ""} ${busy ? "disabled" : ""}><span><strong>启用文件知识库（Web 只读）</strong><small>运行态生效，无需重启 AHA2；关闭后立即回到旧知识页。</small></span></label>
    <dl class="knowledge-vault-status">
      <div><dt>状态</dt><dd>${settings.enabled ? "已启用" : "已关闭"} · 来源：${source}</dd></div>
      <div><dt>vault</dt><dd>${vaultStatus}</dd></div>
      <div><dt>index</dt><dd>${indexStatus}</dd></div>
      <div><dt>路径</dt><dd><code>${escapeHTML(settings.vault_path || "-")}</code></dd></div>
    </dl>
    ${warning}
    ${state.error ? `<div class="security-warning active" role="alert">${escapeHTML(state.error)}</div>` : ""}
    ${!settings.vault_initialized ? `<div class="knowledge-vault-actions">
      <button type="button" id="knowledge-vault-init" class="primary" ${busy ? "disabled" : ""}>${busy === "init" ? "初始化中…" : "初始化示例知识库"}</button>
    </div>` : ""}
    <div class="field-help knowledge-vault-note">文件是知识正本，Web 只读；请用 VSCode 或 Obsidian 编辑，然后在知识库页的齿轮设置中重建索引（没有实时监听）。示例仅用于体验，可随时删除；不会导入或删除旧知识库。</div>`;
}

export function renderKnowledgeVaultSettingsPanel(context: KnowledgeVaultSettingsContext): string {
  activeContext = context;
  if (!state.loaded && !state.loading) void loadSettings(context);
  const statusLabel = state.unsupported
    ? "当前版本不支持"
    : state.settings ? state.settings.enabled ? "已启用" : "已关闭" : state.error ? "加载失败" : "加载中";
  return `<section class="panel spaced account-security-panel knowledge-vault-settings-panel" aria-labelledby="knowledge-vault-settings-title"${state.busy ? ` aria-busy="true"` : ""}>
    <div class="panel-head"><strong id="knowledge-vault-settings-title">文件知识库</strong><span>${statusLabel}</span></div>
    ${settingsBody()}
  </section>`;
}

export function bindKnowledgeVaultSettingsPanel(context: KnowledgeVaultSettingsContext): void {
  activeContext = context;
  const toggle = document.querySelector<HTMLInputElement>("#knowledge-vault-enabled");
  toggle?.addEventListener("change", () => { void setEnabled(toggle.checked); });
  document.querySelector<HTMLButtonElement>("#knowledge-vault-init")?.addEventListener("click", () => { void initializeSamples(); });
  document.querySelector<HTMLButtonElement>("#knowledge-vault-retry")?.addEventListener("click", () => {
    state.error = "";
    void loadSettings(context).then(() => context.render());
  });
}

export function __resetKnowledgeVaultSettingsForTest(): void {
  state = {loaded: false, loading: false, settings: null, error: "", unsupported: false, busy: ""};
  activeContext = null;
  pendingLoad = null;
}

export const __knowledgeVaultSettingsActionsForTest = {setEnabled, initializeSamples};