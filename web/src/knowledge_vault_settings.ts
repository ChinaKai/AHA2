import {api} from "./api.js";
import type {KnowledgeVaultImportResponse, KnowledgeVaultMigrationResponse, KnowledgeVaultReadinessStatus, KnowledgeVaultSettings} from "./types.js";

// Single-switch settings panel. Kept in its own lazy chunk so the first-screen
// bundle does not pay for copy and markup the Owner only sees in 高级设置 → 其他设置.
// The owner-visible control is one switch; readiness, migration and import are
// rendered as backend-reported facts, never as extra switches.

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
  busy: "" | "toggle" | "init" | "migrate" | "import";
}

let state: SettingsPanelState = {loaded: false, loading: false, settings: null, error: "", unsupported: false, busy: ""};
let activeContext: KnowledgeVaultSettingsContext | null = null;
let pendingLoad: Promise<void> | null = null;

const readinessLabels: Record<KnowledgeVaultReadinessStatus, string> = {
  ready: "已就绪",
  degraded: "可启用（部分项目回退）",
  needs_migration: "待迁移旧知识",
  needs_rebuild: "待初始化或重建索引",
  error: "不可用",
};

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

function populationRequest<T>(action: "migrate" | "import", options?: {kind: "local" | "git"; path?: string; remote?: string; branch?: string}): Promise<T> {
  return api.request<T>(`/api/v1/settings/knowledge-vault/${action}`, {method: "POST", body: options ? JSON.stringify(options) : undefined});
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
  if (enabled && !state.settings?.readiness.can_enable) {
    context.setMessage("error", state.settings?.readiness.message || "当前状态不能启用文件知识库。");
    context.render();
    return;
  }
  state.busy = "toggle";
  state.error = "";
  context.render();
  try {
    const result = await api.updateKnowledgeVaultSettings(enabled);
    applySettings(result.settings, context);
    const warning = result.settings.readiness.status === "degraded" ? `；${result.settings.readiness.message || "部分项目仍使用旧知识库"}` : "";
    context.setMessage("notice", enabled ? `文件知识库已启用${warning}` : "文件知识库已关闭，已立即回到旧知识库");
  } catch (error) {
    state.error = errorMessage(error);
    context.setMessage("error", `切换文件知识库失败：${state.error}`);
    await loadSettings(context);
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

async function migrateLegacy(): Promise<void> {
  const context = activeContext;
  if (state.busy || context === null) return;
  const legacy = state.settings?.legacy;
  const confirmed = window.confirm(
    `将备份旧数据库与旧技能包，把 ${legacy?.entries ?? 0} 条旧知识和 ${Math.max(legacy?.skills ?? 0, legacy?.skill_packages ?? 0)} 个技能导出到文件知识库，重建并校验索引后启用。旧数据不会删除。继续？`,
  );
  if (!confirmed) return;
  state.busy = "migrate";
  state.error = "";
  context.render();
  try {
    const result = await populationRequest<KnowledgeVaultMigrationResponse>("migrate");
    applySettings(result.settings, context);
    context.setMessage("notice", `迁移完成：已校验 ${result.migration.verified_entries} 条知识与 ${result.migration.verified_skills} 个技能，备份在 ${result.migration.backup_path}`);
    if (result.migration.warnings?.length) {
      context.setMessage("notice", `迁移有 ${result.migration.warnings.length} 条警告，请检查状态详情`);
    }
  } catch (error) {
    state.error = errorMessage(error);
    context.setMessage("error", `迁移旧知识库失败：${state.error}`);
    await loadSettings(context);
  } finally {
    state.busy = "";
    context.render();
  }
}

async function importExistingVault(): Promise<void> {
  const context = activeContext;
  if (state.busy || context === null) return;
  const kindInput = document.querySelector<HTMLInputElement>('input[name="knowledge-vault-import-kind"]:checked');
  const kind = kindInput?.value === "git" ? "git" : "local";
  const path = document.querySelector<HTMLInputElement>("#knowledge-vault-import-path")?.value.trim() ?? "";
  const remote = document.querySelector<HTMLInputElement>("#knowledge-vault-import-remote")?.value.trim() ?? "";
  const branch = document.querySelector<HTMLInputElement>("#knowledge-vault-import-branch")?.value.trim() ?? "";
  if (kind === "local" && path === "") {
    context.setMessage("error", "请填写已有知识库的本地目录");
    return;
  }
  if (kind === "git" && remote === "") {
    context.setMessage("error", "请填写 SSH Git 远端地址");
    return;
  }
  const confirmed = window.confirm("导入会复制或克隆到当前知识库目录，重建索引并校验项目绑定；目标目录必须为空，不会自动合并。继续？");
  if (!confirmed) return;
  state.busy = "import";
  state.error = "";
  context.render();
  try {
    const result = await populationRequest<KnowledgeVaultImportResponse>("import", {kind, path, remote, branch});
    applySettings(result.settings, context);
    context.setMessage("notice", `导入完成：${result.import.indexed_entries} 条索引，${result.import.project_count} 个项目；文件知识库已启用`);
    if (result.import.unbound_projects?.length) {
      context.setMessage("notice", `${result.import.unbound_projects.length} 个项目未绑定，这些项目仍使用旧知识库`);
    }
  } catch (error) {
    state.error = errorMessage(error);
    context.setMessage("error", `导入已有知识库失败：${state.error}`);
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
  const readiness = settings.readiness;
  const blocked = !settings.enabled && !readiness.can_enable;
  const vaultStatus = !settings.vault_initialized
    ? "未初始化"
    : settings.vault_ready ? `已就绪${settings.vault_name ? `（${escapeHTML(settings.vault_name)}）` : ""}` : "目录不可读";
  const rebuiltAt = settings.rebuilt_at ? new Date(settings.rebuilt_at) : null;
  const rebuiltLabel = rebuiltAt && !Number.isNaN(rebuiltAt.getTime()) ? rebuiltAt.toLocaleString() : settings.rebuilt_at;
  const indexStatus = settings.index_ready
    ? `已就绪 · ${settings.entry_count} 条${rebuiltLabel ? ` · 最近重建 ${escapeHTML(rebuiltLabel)}` : ""}`
    : "未就绪";
  const source = settings.source === "database" ? "数据库设置" : "启动默认";
  const switchVisible = readiness.switch_visible || settings.configured;
  const legacyHint = readiness.status === "needs_migration"
    ? `<small>当前使用旧知识库回退；迁移或导入校验完成前不能启用文件知识库。</small>`
    : `<small>运行态立即生效，无需重启 AHA2；关闭后 Web、Agent 读写与技能立即回到旧知识库。</small>`;
  const switchHTML = !switchVisible
    ? `<div class="field-help knowledge-vault-note">新安装默认使用文件知识库；当前状态${readinessLabels[readiness.status]}。</div>`
    : `<label class="security-setting-toggle knowledge-vault-toggle"><input id="knowledge-vault-enabled" name="knowledge_vault_enabled" type="checkbox" ${settings.enabled ? "checked" : ""} ${busy || blocked ? "disabled" : ""}><span><strong>启用文件知识库（Web 与 Agent 读写、技能）</strong>${legacyHint}</span></label>`;
  const checks = readiness.checks
    .map(check => `<div class="knowledge-vault-check is-${check.status}">${check.status === "ok" ? "✓" : check.status === "warn" ? "!" : "×"} ${escapeHTML(check.message || check.id)}</div>`)
    .join("");
  const missing = readiness.missing?.length
    ? `<div class="security-warning active">未满足：${escapeHTML(readiness.missing.join("、"))}</div>`
    : "";
  const degraded = readiness.status === "degraded"
    ? `<div class="security-warning">可以启用，但${escapeHTML(readiness.message || "部分项目仍会回退旧知识库。")}</div>`
    : "";
  const runtimeError = settings.enabled && (readiness.status === "error" || readiness.status === "needs_rebuild")
    ? `<div class="security-warning active" role="alert">文件知识库已启用，但${escapeHTML(readiness.message || "索引异常")}；Agent 本次会回退旧知识库，请重建索引。</div>`
    : "";
  const blockedHint = blocked
    ? `<div class="security-warning active">${escapeHTML(readiness.message || "当前状态不能启用文件知识库。")}</div>${missing}`
    : readiness.message ? `<div class="field-help knowledge-vault-note">${escapeHTML(readiness.message)}</div>` : "";
  const canInit = settings.legacy.entries === 0 && settings.legacy.skills === 0 && settings.legacy.skill_packages === 0 && !settings.vault_initialized;
  const canImport = !settings.vault_initialized;
  const actions: string[] = [];
  if (readiness.status === "needs_migration") {
    actions.push(`<button type="button" id="knowledge-vault-migrate" class="primary" ${busy ? "disabled" : ""}>${busy === "migrate" ? "迁移中…" : "迁移旧知识库并校验"}</button>`);
  }
  if (canInit) {
    actions.push(`<button type="button" id="knowledge-vault-init" class="primary" ${busy ? "disabled" : ""}>${busy === "init" ? "初始化中…" : "初始化示例知识库"}</button>`);
  }
  return `
    ${switchHTML}
    <dl class="knowledge-vault-status">
      <div><dt>状态</dt><dd>${settings.enabled ? "已启用" : "已关闭"} · 来源：${source} · 就绪度：${readinessLabels[readiness.status]}</dd></div>
      <div><dt>vault</dt><dd>${vaultStatus}</dd></div>
      <div><dt>index</dt><dd>${indexStatus}</dd></div>
      <div><dt>路径</dt><dd><code>${escapeHTML(settings.vault_path || "-")}</code></dd></div>
      <div><dt>旧知识</dt><dd>${settings.legacy.entries} 条 · ${Math.max(settings.legacy.skills, settings.legacy.skill_packages)} 个技能${settings.migration_verified_at ? " · 已迁移验证" : settings.import_verified_at ? " · 已导入验证" : ""}</dd></div>
    </dl>
    <div class="knowledge-vault-checks">${checks}</div>
    ${degraded}
    ${runtimeError}
    ${blockedHint}
    ${state.error ? `<div class="security-warning active" role="alert">${escapeHTML(state.error)}</div>` : ""}
    ${actions.length ? `<div class="knowledge-vault-actions">${actions.join("")}</div>` : ""}
    ${canImport ? `<details class="knowledge-vault-import"><summary>导入已有知识库（本地目录或 Git 远端）</summary>
      <div class="knowledge-vault-import-fields">
        <label><input type="radio" name="knowledge-vault-import-kind" value="local" checked> 本地目录</label>
        <label><input type="radio" name="knowledge-vault-import-kind" value="git"> Git 远端</label>
        <input id="knowledge-vault-import-path" type="text" placeholder="已有知识库目录，例如 /srv/aha2-vault">
        <input id="knowledge-vault-import-remote" type="text" placeholder="SSH 远端，例如 git@example.com:owner/vault.git">
        <input id="knowledge-vault-import-branch" type="text" placeholder="分支（可选）">
        <button type="button" id="knowledge-vault-import" ${busy ? "disabled" : ""}>${busy === "import" ? "导入中…" : "导入并校验"}</button>
      </div>
      <div class="field-help">目标知识库目录必须为空；导入不会合并已有文件，也不会读取 Git 密码（仅支持 SSH 密钥）。</div>
    </details>` : ""}
    <div class="field-help knowledge-vault-note">文件是知识正本；请用 VSCode 或 Obsidian 编辑，然后在知识库页的齿轮设置中重建索引（Git pull 后会自动重建）。示例仅用于体验，可随时删除；不会删除旧知识库。</div>`;
}

export function renderKnowledgeVaultSettingsPanel(context: KnowledgeVaultSettingsContext): string {
  activeContext = context;
  if (!state.loaded && !state.loading) void loadSettings(context);
  const statusLabel = state.unsupported
    ? "当前版本不支持"
    : state.settings ? `${state.settings.enabled ? "已启用" : "已关闭"} · ${readinessLabels[state.settings.readiness.status]}` : state.error ? "加载失败" : "加载中";
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
  document.querySelector<HTMLButtonElement>("#knowledge-vault-migrate")?.addEventListener("click", () => { void migrateLegacy(); });
  document.querySelector<HTMLButtonElement>("#knowledge-vault-import")?.addEventListener("click", () => { void importExistingVault(); });
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

export const __knowledgeVaultSettingsActionsForTest = {setEnabled, initializeSamples, migrateLegacy, importExistingVault};