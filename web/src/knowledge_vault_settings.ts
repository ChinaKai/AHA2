import {api} from "./api.js";
import {currentLocale, t} from "./i18n.js";
import type {KnowledgeVaultImportResponse, KnowledgeVaultMigrationResponse, KnowledgeVaultReadinessCheck, KnowledgeVaultReadinessStatus, KnowledgeVaultSettings} from "./types.js";

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
  // The import fold is re-rendered when a request starts, so its mode, open
  // state and draft survive that render instead of collapsing mid-import.
  importKind: "local" | "git";
  importOpen: boolean;
  importDraft: {path: string; remote: string; branch: string};
}

let state: SettingsPanelState = {
  loaded: false, loading: false, settings: null, error: "", unsupported: false, busy: "",
  importKind: "local", importOpen: false, importDraft: {path: "", remote: "", branch: ""},
};

// Copy for this lazy chunk. The settings panel is only loaded from 其他设置, so
// its bilingual strings stay here instead of growing the first-screen catalog.
// Keys are looked up with currentLocale() and fall back to the global t(key).
const localText: Record<string, [zhCN: string, enUS: string]> = {
  "kvs.title": ["文件知识库", "File knowledge vault"],
  "kvs.state.enabled": ["已启用", "Enabled"],
  "kvs.state.disabled": ["已关闭", "Disabled"],
  "kvs.state.unsupported": ["当前版本不支持", "Unsupported"],
  "kvs.state.loadFailed": ["加载失败", "Load failed"],
  "kvs.state.loading": ["加载中", "Loading"],

  "kvs.source.title": ["知识源", "Knowledge source"],
  "kvs.switch.title": ["启用文件知识库", "Enable the file knowledge vault"],
  "kvs.switch.help": ["开启后 Web、Agent 读写与技能使用文件知识库（vault）；关闭后立即回到旧知识库。", "When on, the Web UI, Agents, and skills read and write the vault; when off, they return to the legacy knowledge base immediately."],
  "kvs.switch.blockedMigration": ["检测到旧知识；迁移或导入验证后才能开启。", "Legacy knowledge detected; migrate or import and verify before enabling."],
  "kvs.switch.fresh": ["全新安装默认使用文件知识库，不需要切换。", "Fresh installs use the file knowledge vault by default; no switch is needed."],
  "kvs.switch.lockedBadge": ["已锁定", "Locked"],
  "kvs.switch.lockedMigrated": ["已迁移到文件知识库，不能回退旧库。", "Migrated to the file knowledge vault; rollback to the legacy knowledge base is not allowed."],
  "kvs.switch.lockedNoLegacy": ["文件知识库已启用并锁定；此实例固定使用文件知识库，不能关闭。", "The file knowledge vault is enabled and locked; this instance always uses it and cannot switch it off."],
  "kvs.switch.lockedEnable": ["已完成迁移或导入；请重新启用文件知识库，不能回退旧库。", "Migration or import is complete; turn the vault back on. Rollback to legacy knowledge is not allowed."],
  "kvs.source.line": ["状态来源：{source}", "State source: {source}"],
  "kvs.source.database": ["数据库设置", "saved setting"],
  "kvs.source.startup": ["启动默认", "startup default"],

  "kvs.health.title": ["Vault 健康", "Vault health"],
  "kvs.health.ok": ["正常", "Healthy"],
  "kvs.health.attention": ["需关注", "Needs attention"],
  "kvs.health.migration": ["待迁移", "Migration needed"],
  "kvs.health.uninitialized": ["待初始化", "Not initialized"],
  "kvs.health.rebuild": ["待重建索引", "Index rebuild needed"],
  "kvs.health.error": ["不可用", "Unavailable"],
  "kvs.fact.vaultPath": ["Vault 路径", "Vault path"],
  "kvs.fact.indexPath": ["索引路径", "Index path"],
  "kvs.fact.entries": ["索引条数", "Indexed entries"],
  "kvs.fact.entriesValue": ["{count} 条", "{count} entries"],
  "kvs.fact.entriesOne": ["1 条", "1 entry"],
  "kvs.fact.rebuilt": ["最近重建", "Last rebuilt"],
  "kvs.fact.never": ["尚未重建", "Never rebuilt"],
  "kvs.check.vaultInitOk": ["目录已初始化", "Vault directory initialized"],
  "kvs.check.vaultInitFail": ["目录未初始化", "Vault directory not initialized"],
  "kvs.check.vaultWritableOk": ["目录可写", "Vault directory writable"],
  "kvs.check.vaultWritableFail": ["目录不可写", "Vault directory not writable"],
  "kvs.check.indexSchemaOk": ["索引 schema 可读", "Index schema readable"],
  "kvs.check.indexSchemaFail": ["索引未建立或不可读", "Index missing or unreadable"],
  "kvs.check.indexSchemaError": ["索引不可读：{reason}", "Index unreadable: {reason}"],
  "kvs.check.indexWritableOk": ["索引目录可写", "Index directory writable"],
  "kvs.check.indexWritableFail": ["索引目录不可写", "Index directory not writable"],
  "kvs.check.indexFreshOk": ["索引与文件一致", "Index matches files"],
  "kvs.check.indexStale": ["知识文件比索引新，索引会自动修复", "Knowledge files are newer than the index; it will be repaired automatically"],
  "kvs.check.indexMissing": ["索引缺失", "Index missing"],
  "kvs.check.indexWarnings": ["{count} 条索引警告", "{count} index warnings"],
  "kvs.check.indexWarningsOne": ["1 条索引警告", "1 index warning"],
  "kvs.checks.passed": ["其余通过：{items}", "Also passing: {items}"],
  "kvs.health.emptyEntries": ["索引为空；导入或迁移后会生成知识。", "The index is empty; entries appear after an import or migration."],
  "kvs.health.unbound": ["{count} 个项目未绑定，继续使用旧库。", "{count} projects are unbound and keep using the legacy knowledge base."],
  "kvs.health.unboundOne": ["1 个项目未绑定，继续使用旧库。", "1 project is unbound and keeps using the legacy knowledge base."],

  "kvs.attention.uninitialized": ["文件知识库尚未初始化；创建示例或导入已有知识库后开始索引。", "The vault is not initialized yet; create the samples or import an existing vault to start indexing."],
  "kvs.attention.runtimeRebuild": ["文件知识库已启用，但索引尚未就绪；Agent 本次不会回退旧知识库，索引会自动修复。", "The vault is enabled but the index is not ready; Agents do not fall back to legacy knowledge, and the index is repaired automatically."],
  "kvs.attention.runtimeError": ["文件知识库已启用，但知识库或索引不可读写；Agent 本次会回退旧知识库，请检查目录权限。", "The vault is enabled but its directory or index is not readable/writable; Agents fall back to legacy knowledge for now. Check directory permissions."],
  "kvs.attention.blocked": ["当前状态不能启用文件知识库。", "The vault cannot be enabled in the current state."],
  "kvs.attention.missing": ["未满足：{items}", "Missing: {items}"],
  "kvs.status.needsMigration": ["升级后需要先迁移旧知识库，或导入已有知识库，才能启用文件知识库。", "After an upgrade, migrate legacy knowledge or import an existing vault before enabling the file knowledge vault."],
  "kvs.status.needsRebuild": ["知识库或索引尚未就绪，索引会自动修复。", "The vault or index is not ready; the index is repaired automatically."],
  "kvs.status.error": ["知识库或索引不可写或不可读，请检查目录权限。", "The vault or index is not writable or readable; check directory permissions."],
  "kvs.missing.vault_initialized": ["知识库初始化", "vault initialization"],
  "kvs.missing.vault_writable": ["目录可写", "directory writability"],
  "kvs.missing.index_schema": ["索引 schema", "index schema"],
  "kvs.missing.index_writable": ["索引可写", "index writability"],
  "kvs.missing.index_fresh": ["索引一致", "index freshness"],

  "kvs.legacy.title": ["旧库保留", "Legacy backup"],
  "kvs.legacy.badge": ["只读保留", "Read-only"],
  "kvs.legacy.entries": ["旧知识", "Legacy entries"],
  "kvs.legacy.entriesValue": ["{count} 条", "{count} entries"],
  "kvs.legacy.entriesOne": ["1 条", "1 entry"],
  "kvs.legacy.skills": ["旧技能", "Legacy skills"],
  "kvs.legacy.skillsValue": ["{count} 个", "{count} skills"],
  "kvs.legacy.skillsOne": ["1 个", "1 skill"],
  "kvs.legacy.unbound": ["未绑定项目", "Unbound projects"],
  "kvs.legacy.unboundValue": ["{count} 个，继续使用旧库", "{count}, still on legacy"],
  "kvs.legacy.unboundOne": ["1 个，继续使用旧库", "1, still on legacy"],
  "kvs.legacy.allBound": ["全部已绑定", "All bound"],
  "kvs.legacy.migration": ["迁移状态", "Migration"],
  "kvs.legacy.migrationPending": ["未验证", "Not verified"],
  "kvs.legacy.migrationVerified": ["已迁移验证", "Migrated and verified"],
  "kvs.legacy.importVerified": ["已导入验证", "Imported and verified"],
  "kvs.legacy.rollbackOnly": ["已迁移，仅作只读保留", "Migrated; read-only backup"],
  "kvs.legacy.inUseNote": ["文件知识库已启用；旧知识库仅作只读备份保留，不会被迁移覆盖，也不能通过开关回退。", "The vault is enabled; legacy knowledge is kept read-only as a backup, is not overwritten by migration, and cannot be restored through the switch."],
  "kvs.legacy.populatedNote": ["旧数据仅作只读备份保留，不能回退。", "Legacy data is kept read-only as a backup and cannot be used for rollback."],

  "kvs.action.migrate": ["迁移旧知识库并校验", "Migrate legacy knowledge and verify"],
  "kvs.action.migrating": ["迁移中…", "Migrating…"],
  "kvs.action.init": ["初始化示例知识库", "Initialize sample vault"],
  "kvs.action.initializing": ["初始化中…", "Initializing…"],
  "kvs.action.retry": ["重试", "Retry"],
  "kvs.action.import": ["导入并校验", "Import and verify"],
  "kvs.action.importing": ["导入中…", "Importing…"],
  "kvs.action.enableBlocked": ["当前状态不能启用文件知识库。", "The vault cannot be enabled in the current state."],

  "kvs.msg.enabled": ["文件知识库已启用", "File knowledge vault enabled"],
  "kvs.msg.enabledDegraded": ["文件知识库已启用；部分项目仍会回退旧知识库", "File knowledge vault enabled; some projects still fall back to legacy knowledge"],
  "kvs.msg.disabled": ["文件知识库已关闭，已立即回到旧知识库", "File knowledge vault disabled; back on legacy knowledge immediately"],
  "kvs.msg.rollbackLocked": ["文件知识库已迁移并锁定，不能关闭回退旧库", "The file knowledge vault is migrated and locked; it cannot be switched off"],
  "kvs.msg.toggleFailed": ["切换文件知识库失败：{error}", "Failed to switch the file knowledge vault: {error}"],
  "kvs.msg.initConfirm": ["将创建知识库目录骨架和 4 篇示例文档，并自动启用文件知识库。示例仅用于体验，可随时删除；不会导入或修改旧知识库。继续？", "Create the vault directory skeleton and 4 sample documents, then enable the vault. Samples are only for trying it out and can be deleted; legacy knowledge is neither imported nor changed. Continue?"],
  "kvs.msg.initDone": ["已创建 {count} 个示例文档，文件知识库已启用", "Created {count} sample documents; the file knowledge vault is enabled"],
  "kvs.msg.initFailed": ["初始化示例知识库失败：{error}", "Failed to initialize the sample vault: {error}"],
  "kvs.msg.migrateConfirm": ["将备份旧数据库与旧技能包，把 {entries} 条旧知识和 {skills} 个技能导出到文件知识库，重建并校验索引后启用。旧数据不会删除。继续？", "Back up the legacy database and skill packages, export {entries} legacy entries and {skills} skills into the vault, rebuild and verify the index, then enable it. Legacy data is not deleted. Continue?"],
  "kvs.msg.migrateDone": ["迁移完成：已校验 {entries} 条知识与 {skills} 个技能，备份在 {backup}", "Migration complete: verified {entries} entries and {skills} skills; backup at {backup}"],
  "kvs.msg.migrateWarnings": ["迁移有 {count} 条警告，请检查状态详情", "Migration reported {count} warnings; check the status details"],
  "kvs.msg.migrateFailed": ["迁移旧知识库失败：{error}", "Failed to migrate legacy knowledge: {error}"],
  "kvs.msg.importConfirm": ["导入会复制或克隆到当前知识库目录，重建索引并校验项目绑定；目标目录必须为空，不会自动合并。继续？", "The import copies or clones into the vault directory, rebuilds the index, and verifies project bindings. The target directory must be empty; nothing is merged automatically. Continue?"],
  "kvs.msg.importMissingPath": ["请填写已有知识库的本地目录", "Enter the local directory of the existing vault"],
  "kvs.msg.importMissingRemote": ["请填写 SSH Git 远端地址", "Enter the SSH Git remote"],
  "kvs.msg.importDone": ["导入完成：{entries} 条索引，{projects} 个项目；文件知识库已启用", "Import complete: {entries} indexed entries across {projects} projects; the file knowledge vault is enabled"],
  "kvs.msg.importUnbound": ["{count} 个项目未绑定，这些项目仍使用旧知识库", "{count} projects are unbound and still use legacy knowledge"],
  "kvs.msg.importFailed": ["导入已有知识库失败：{error}", "Failed to import the existing vault: {error}"],

  "kvs.unsupported": ["当前 AHA2 版本没有文件知识库设置接口；升级后这里会出现运行态开关。", "This AHA2 version has no file knowledge vault settings API; the runtime switch appears after an upgrade."],
  "kvs.loading": ["正在读取文件知识库状态…", "Loading file knowledge vault status…"],
  "kvs.loadFailed": ["读取文件知识库状态失败：{error}", "Failed to load file knowledge vault status: {error}"],
  "kvs.import.title": ["导入已有知识库（本地目录或 Git 远端）", "Import an existing vault (local directory or Git remote)"],
  "kvs.import.legend": ["来源", "Source"],
  "kvs.import.local": ["本地目录", "Local directory"],
  "kvs.import.git": ["Git 远端", "Git remote"],
  "kvs.import.localPlaceholder": ["例如 /srv/aha2-vault", "e.g. /srv/aha2-vault"],
  "kvs.import.remote": ["SSH Git 远端", "SSH Git remote"],
  "kvs.import.remotePlaceholder": ["例如 git@example.com:owner/vault.git", "e.g. git@example.com:owner/vault.git"],
  "kvs.import.branch": ["分支（可选）", "Branch (optional)"],
  "kvs.import.branchPlaceholder": ["留空使用远端默认分支", "Leave blank for the remote default branch"],
  "kvs.import.help": ["目标知识库目录必须为空；导入不会合并已有文件，也不会读取 Git 密码（仅支持 SSH 密钥）。", "The target vault directory must be empty; imports never merge existing files and never read Git passwords (SSH keys only)."],
  "kvs.footer": ["文件是知识正本；请用 VSCode 或 Obsidian 编辑，索引会随写入、文件变化和启动对账自动维护。", "Files are the source of truth; edit them in VS Code or Obsidian. The index is maintained automatically on writes, file changes, and startup reconciliation."],
};

function kt(key: string, values: Record<string, string | number> = {}): string {
  const [zh, en] = localText[key] ?? ["", ""];
  const template = (currentLocale() === "en-US" ? en : zh) || t(key);
  return template.replace(/\{(\w+)\}/g, (_, name: string) => String(values[name] ?? `{${name}}`));
}

// The panel is lazy-loaded from 其他设置, so its layout lives here instead of
// growing the first-screen stylesheet. The generic input rules stretch radios
// into full-width 38px blocks; scope the reset to this form so existing toggle
// and checkbox components keep their own sizes.
const IMPORT_STYLES = `
.knowledge-vault-settings-panel .knowledge-vault-toggle { margin: 0; }
.knowledge-vault-settings-panel .knowledge-vault-toggle input { cursor: pointer; }
.knowledge-vault-settings-panel .knowledge-vault-toggle input:disabled { cursor: progress; }
.knowledge-vault-settings-panel .knowledge-vault-toggle.is-locked { cursor: default; }
.knowledge-vault-settings-panel .knowledge-vault-toggle.is-locked input:disabled { cursor: not-allowed; }
.knowledge-vault-toggle.is-locked small { color: var(--color-text-muted); }
.knowledge-vault-settings-panel .panel-head > .knowledge-vault-state { flex: 0 0 auto; }
.knowledge-vault-state { flex: 0 0 auto; padding: 2px 8px; border-radius: var(--radius-pill); background: var(--color-surface-muted); color: var(--color-text-muted); font-size: 10px; font-weight: 700; white-space: nowrap; }
.knowledge-vault-state.is-on { background: var(--color-success-soft); color: var(--color-success-ink); }
.knowledge-vault-group { padding: 16px 18px 0; }
.knowledge-vault-group + .knowledge-vault-group { margin-top: 16px; border-top: 1px solid var(--color-border-subtle); padding-top: 16px; }
.knowledge-vault-group:last-of-type { padding-bottom: 16px; }
.knowledge-vault-group-head { display: flex; align-items: center; justify-content: space-between; gap: 8px 10px; flex-wrap: wrap; margin: 0 0 10px; }
.knowledge-vault-group-head h3 { margin: 0; color: var(--color-text-muted); font-size: var(--font-size-11); font-weight: 700; letter-spacing: .02em; }
.knowledge-vault-badge { flex: 0 0 auto; padding: 2px 8px; border-radius: var(--radius-pill); background: var(--color-surface-muted); color: var(--color-text-muted); font-size: 10px; font-weight: 700; white-space: nowrap; }
.knowledge-vault-badge.is-ok { background: var(--color-success-soft); color: var(--color-success-ink); }
.knowledge-vault-badge.is-warn { background: var(--color-warning-soft); color: var(--color-warning-ink); }
.knowledge-vault-badge.is-bad { background: var(--color-danger-soft); color: var(--color-danger-ink); }
.knowledge-vault-switch-note { margin: 0; }
.knowledge-vault-source-line { margin: 8px 0 0; color: var(--color-text-muted); font-size: var(--font-size-11); line-height: 1.5; overflow-wrap: anywhere; }
.knowledge-vault-attention { margin: 0 0 12px; padding: 10px 12px; border-left: 3px solid var(--color-warning); border-radius: 0 var(--radius-control) var(--radius-control) 0; background: var(--color-warning-soft); color: var(--color-warning-ink); font-size: var(--font-size-11); line-height: 1.6; overflow-wrap: anywhere; }
.knowledge-vault-attention.is-error { border-left-color: var(--color-danger); background: var(--color-danger-soft); color: var(--color-danger-ink); }
.knowledge-vault-attention p { margin: 0; }
.knowledge-vault-attention p + p { margin-top: 4px; }
.knowledge-vault-facts { display: grid; gap: 6px; margin: 0; }
.knowledge-vault-facts > div { display: grid; grid-template-columns: 104px minmax(0,1fr); gap: 2px 12px; align-items: start; }
.knowledge-vault-facts dt { color: var(--color-text-muted); font-size: var(--font-size-11); line-height: 1.5; }
.knowledge-vault-facts dd { min-width: 0; margin: 0; font-size: var(--font-size-12); line-height: 1.5; overflow-wrap: anywhere; }
.knowledge-vault-facts code { font-family: var(--font-mono); font-size: var(--font-size-11); overflow-wrap: anywhere; word-break: break-word; }
.knowledge-vault-checks { display: grid; gap: 6px; margin: 12px 0 0; padding: 0; list-style: none; }
.knowledge-vault-checks li, .knowledge-vault-checks-passed { display: grid; grid-template-columns: 14px minmax(0,1fr); gap: 6px; align-items: start; min-width: 0; font-size: var(--font-size-11); line-height: 1.5; color: var(--color-text-muted); }
.knowledge-vault-checks-passed { margin-top: 8px; }
.knowledge-vault-check-mark { font-weight: 700; text-align: center; line-height: 1.5; }
.knowledge-vault-checks li.is-warn { color: var(--color-warning-ink); }
.knowledge-vault-checks li.is-fail { color: var(--color-danger-ink); }
.knowledge-vault-checks li > span:last-child, .knowledge-vault-checks-passed > span:last-child { min-width: 0; overflow-wrap: anywhere; }
.knowledge-vault-health-note { margin: 10px 0 0; color: var(--color-text-muted); font-size: var(--font-size-11); line-height: 1.5; overflow-wrap: anywhere; }
.knowledge-vault-health-note.is-warn { color: var(--color-warning-ink); }
.knowledge-vault-legacy-note { margin: 10px 0 0; color: var(--color-text-muted); font-size: var(--font-size-11); line-height: 1.5; overflow-wrap: anywhere; }
.knowledge-vault-actions { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 12px; }
.knowledge-vault-settings-panel .knowledge-vault-note { margin: 12px 18px 18px; }
.knowledge-vault-settings-panel .knowledge-vault-actions + .knowledge-vault-note { margin-top: 16px; }
.knowledge-vault-settings-panel .knowledge-vault-import { margin: 14px 18px 0; border: 1px solid var(--line); border-radius: 6px; background: #f8fafc; }
.knowledge-vault-import > summary { display: flex; align-items: center; justify-content: space-between; gap: 10px; min-height: 44px; padding: 10px 12px; cursor: pointer; font-size: var(--font-size-12); font-weight: 700; list-style: none; }
.knowledge-vault-import > summary::-webkit-details-marker { display: none; }
.knowledge-vault-import > summary::after { content: "›"; flex: 0 0 auto; color: var(--color-text-muted); font-size: 14px; transform: rotate(90deg); transition: transform .15s ease; }
.knowledge-vault-import[open] > summary::after { transform: rotate(-90deg); }
.knowledge-vault-import-body { display: grid; gap: 12px; min-width: 0; padding: 0 12px 12px; }
.knowledge-vault-import-modes { display: flex; flex-wrap: wrap; align-items: center; gap: 8px 18px; min-width: 0; margin: 0; padding: 0; border: 0; }
.knowledge-vault-import-modes > legend { width: 100%; margin: 0 0 2px; padding: 0; color: var(--color-text-muted); font-size: var(--font-size-11); font-weight: 700; }
.knowledge-vault-import-mode { display: inline-flex; align-items: center; gap: 8px; margin: 0; font-size: var(--font-size-12); font-weight: 600; cursor: pointer; }
.knowledge-vault-import input[type="radio"], .knowledge-vault-import input[type="checkbox"] { width: auto; height: auto; min-height: 0; padding: 0; margin: 0; }
.knowledge-vault-import-fields { display: grid; gap: 10px; min-width: 0; }
.knowledge-vault-import-field { display: grid; gap: 5px; min-width: 0; margin: 0; color: var(--color-text); font-size: var(--font-size-11); font-weight: 700; }
.knowledge-vault-import-field[hidden] { display: none; }
.knowledge-vault-import-actions { display: flex; }
.knowledge-vault-import-actions button { width: 100%; min-height: 40px; }
.knowledge-vault-import-help { margin: 0; }
@media (max-width: 560px) {
  .knowledge-vault-facts > div { grid-template-columns: minmax(0,1fr); gap: 1px; }
}
@media (max-width: 760px) {
  .knowledge-vault-settings-panel { padding-bottom: calc(56px + env(safe-area-inset-bottom)); }
  .knowledge-vault-import > summary { min-height: 46px; padding: 12px; }
}
`;

function installImportStyles(): void {
  if (typeof document === "undefined" || document.getElementById("knowledge-vault-import-styles")) return;
  const style = document.createElement("style");
  style.id = "knowledge-vault-import-styles";
  style.textContent = IMPORT_STYLES;
  document.head.append(style);
}
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

function legacySkillCount(settings: KnowledgeVaultSettings): number {
  return Math.max(settings.legacy.skills, settings.legacy.skill_packages);
}

function hasLegacy(settings: KnowledgeVaultSettings): boolean {
  return settings.legacy.entries > 0 || settings.legacy.skills > 0 || settings.legacy.skill_packages > 0;
}

// Mirrors knowledgevault.ReadinessInput.InUse: the owner already runs on a
// populated vault, so the legacy database is a rollback copy rather than a
// pending migration.
function inUseUpgrade(settings: KnowledgeVaultSettings): boolean {
  return hasLegacy(settings) && settings.configured && settings.enabled && settings.entry_count > 0;
}

// The backend closes rollback permanently once the instance has committed to
// the vault; the switch then stays on and the API refuses a direct close. Older
// servers do not send rollback_allowed, so mirror the same rule from the facts
// this payload already carries instead of making the switch closable again.
function rollbackAllowed(settings: KnowledgeVaultSettings): boolean {
  const reported = settings.readiness.rollback_allowed;
  if (typeof reported === "boolean") return reported;
  return !settings.configured && !hasTimestamp(settings.migration_verified_at) &&
    !hasTimestamp(settings.import_verified_at) && !inUseUpgrade(settings);
}

// Go serializes a zero time.Time as "0001-01-01T00:00:00Z" even with
// omitempty, so a missing verification timestamp still arrives as a truthy
// string. Treat the zero value as "not verified".
function hasTimestamp(value: string | undefined): boolean {
  return Boolean(value) && !String(value).startsWith("0001-01-01");
}

function populationRequest<T>(action: "migrate" | "import", options?: {kind: "local" | "git"; path?: string; remote?: string; branch?: string}): Promise<T> {
  return api.request<T>(`/api/v1/settings/knowledge-vault/${action}`, {method: "POST", body: options ? JSON.stringify(options) : undefined});
}

// Switching source mode only toggles the field rows; the inputs keep their
// values, and the module state keeps the choice if the panel re-renders.
function setImportKind(value: string): void {
  state.importKind = value === "git" ? "git" : "local";
  document.querySelectorAll<HTMLElement>(".knowledge-vault-import-field").forEach(field => {
    field.hidden = field.dataset.importKind !== state.importKind;
  });
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
  if (!enabled && state.settings && !rollbackAllowed(state.settings)) {
    // The rendered checkbox is disabled in this state, but the action is the
    // single entry point for a change event; keep the decision here too so a
    // programmatic click can never reach the API. The shell's region restore
    // keeps a control's live value across a re-render, so put the
    // authoritative checked state back after rendering.
    context.setMessage("error", kt("kvs.msg.rollbackLocked"));
    context.render();
    if (typeof document !== "undefined") {
      const toggle = document.querySelector<HTMLInputElement>("#knowledge-vault-enabled");
      if (toggle) toggle.checked = state.settings.enabled;
    }
    return;
  }
  if (enabled && !state.settings?.readiness.can_enable) {
    context.setMessage("error", kt("kvs.action.enableBlocked"));
    context.render();
    return;
  }
  state.busy = "toggle";
  state.error = "";
  context.render();
  try {
    const result = await api.updateKnowledgeVaultSettings(enabled);
    applySettings(result.settings, context);
    const degraded = result.settings.readiness.status === "degraded";
    context.setMessage("notice", enabled ? kt(degraded ? "kvs.msg.enabledDegraded" : "kvs.msg.enabled") : kt("kvs.msg.disabled"));
  } catch (error) {
    state.error = errorMessage(error);
    context.setMessage("error", kt("kvs.msg.toggleFailed", {error: state.error}));
    await loadSettings(context);
  } finally {
    state.busy = "";
    context.render();
  }
}

async function initializeSamples(): Promise<void> {
  const context = activeContext;
  if (state.busy || context === null) return;
  if (!window.confirm(kt("kvs.msg.initConfirm"))) return;
  state.busy = "init";
  state.error = "";
  context.render();
  try {
    const result = await api.initializeKnowledgeVaultSamples();
    applySettings(result.settings, context);
    context.setMessage("notice", kt("kvs.msg.initDone", {count: result.samples}));
  } catch (error) {
    state.error = errorMessage(error);
    context.setMessage("error", kt("kvs.msg.initFailed", {error: state.error}));
    await loadSettings(context);
  } finally {
    state.busy = "";
    context.render();
  }
}

async function migrateLegacy(): Promise<void> {
  const context = activeContext;
  if (state.busy || context === null) return;
  const settings = state.settings;
  if (!window.confirm(kt("kvs.msg.migrateConfirm", {
    entries: settings?.legacy.entries ?? 0,
    skills: settings ? legacySkillCount(settings) : 0,
  }))) return;
  state.busy = "migrate";
  state.error = "";
  context.render();
  try {
    const result = await populationRequest<KnowledgeVaultMigrationResponse>("migrate");
    applySettings(result.settings, context);
    context.setMessage("notice", kt("kvs.msg.migrateDone", {
      entries: result.migration.verified_entries,
      skills: result.migration.verified_skills,
      backup: result.migration.backup_path,
    }));
    if (result.migration.warnings?.length) {
      context.setMessage("notice", kt("kvs.msg.migrateWarnings", {count: result.migration.warnings.length}));
    }
  } catch (error) {
    state.error = errorMessage(error);
    context.setMessage("error", kt("kvs.msg.migrateFailed", {error: state.error}));
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
  state.importKind = kind;
  state.importDraft = {path, remote, branch};
  if (kind === "local" && path === "") {
    context.setMessage("error", kt("kvs.msg.importMissingPath"));
    return;
  }
  if (kind === "git" && remote === "") {
    context.setMessage("error", kt("kvs.msg.importMissingRemote"));
    return;
  }
  if (!window.confirm(kt("kvs.msg.importConfirm"))) return;
  state.busy = "import";
  state.error = "";
  context.render();
  try {
    const result = await populationRequest<KnowledgeVaultImportResponse>("import", {kind, path, remote, branch});
    applySettings(result.settings, context);
    context.setMessage("notice", kt("kvs.msg.importDone", {entries: result.import.indexed_entries, projects: result.import.project_count}));
    if (result.import.unbound_projects?.length) {
      context.setMessage("notice", kt("kvs.msg.importUnbound", {count: result.import.unbound_projects.length}));
    }
  } catch (error) {
    state.error = errorMessage(error);
    context.setMessage("error", kt("kvs.msg.importFailed", {error: state.error}));
    await loadSettings(context);
  } finally {
    state.busy = "";
    context.render();
  }
}

interface HealthBadge {
  label: string;
  tone: "is-ok" | "is-warn" | "is-bad";
}

function healthBadge(settings: KnowledgeVaultSettings): HealthBadge {
  const status: KnowledgeVaultReadinessStatus = settings.readiness.status;
  switch (status) {
    case "ready":
      return {label: kt("kvs.health.ok"), tone: "is-ok"};
    case "degraded":
      return {label: kt("kvs.health.attention"), tone: "is-warn"};
    case "needs_migration":
      return {label: kt("kvs.health.migration"), tone: "is-warn"};
    case "needs_rebuild":
      return settings.vault_initialized
        ? {label: kt("kvs.health.rebuild"), tone: "is-warn"}
        : {label: kt("kvs.health.uninitialized"), tone: "is-warn"};
    default:
      return {label: kt("kvs.health.error"), tone: "is-bad"};
  }
}

// Short labels keep the checklist compact; the backend message stays the
// fallback for check ids this build does not know yet.
function checkLabel(settings: KnowledgeVaultSettings, check: KnowledgeVaultReadinessCheck): string {
  const ok = check.status === "ok";
  switch (check.id) {
    case "vault_initialized":
      return kt(ok ? "kvs.check.vaultInitOk" : "kvs.check.vaultInitFail");
    case "vault_writable":
      return kt(ok ? "kvs.check.vaultWritableOk" : "kvs.check.vaultWritableFail");
    case "index_schema":
      if (ok) return kt("kvs.check.indexSchemaOk");
      return settings.index_error
        ? kt("kvs.check.indexSchemaError", {reason: escapeHTML(settings.index_error)})
        : kt("kvs.check.indexSchemaFail");
    case "index_writable":
      return kt(ok ? "kvs.check.indexWritableOk" : "kvs.check.indexWritableFail");
    case "index_fresh":
      if (ok) return kt("kvs.check.indexFreshOk");
      return settings.index_stale ? kt("kvs.check.indexStale") : kt("kvs.check.indexMissing");
    case "index_warnings": {
      const count = settings.index_warnings ?? 0;
      return kt(count === 1 ? "kvs.check.indexWarningsOne" : "kvs.check.indexWarnings", {count});
    }
    default:
      return escapeHTML(check.message || check.id);
  }
}

interface Attention {
  tone: "is-warn" | "is-error";
  message: string;
  missing: string[];
}

function attentionFor(settings: KnowledgeVaultSettings): Attention | null {
  const readiness = settings.readiness;
  if (settings.enabled && readiness.status === "error") {
    return {tone: "is-error", message: kt("kvs.attention.runtimeError"), missing: []};
  }
  if (settings.enabled && readiness.status === "needs_rebuild" && settings.vault_initialized) {
    return {tone: "is-error", message: kt("kvs.attention.runtimeRebuild"), missing: []};
  }
  if (!settings.enabled && !readiness.can_enable) {
    if (readiness.status === "needs_migration") {
      return {tone: "is-warn", message: kt("kvs.status.needsMigration"), missing: []};
    }
    const missing = (readiness.missing ?? []).map(id => {
      const key = `kvs.missing.${id}`;
      const label = kt(key);
      return label === key ? id : label;
    });
    return {
      tone: readiness.status === "error" ? "is-error" : "is-warn",
      message: readiness.status === "error" ? kt("kvs.status.error") : kt("kvs.status.needsRebuild"),
      missing,
    };
  }
  if (settings.enabled && !settings.vault_initialized) {
    return {tone: "is-warn", message: kt("kvs.attention.uninitialized"), missing: []};
  }
  return null;
}

function attentionHTML(attention: Attention | null): string {
  if (!attention) return "";
  const missing = attention.missing.length
    ? `<p>${kt("kvs.attention.missing", {items: escapeHTML(attention.missing.join("、"))})}</p>`
    : "";
  return `<div class="knowledge-vault-attention ${attention.tone}" role="${attention.tone === "is-error" ? "alert" : "note"}"><p>${attention.message}</p>${missing}</div>`;
}

function healthHTML(settings: KnowledgeVaultSettings, busy: string): string {
  const readiness = settings.readiness;
  const badge = healthBadge(settings);
  const rebuiltAt = settings.rebuilt_at ? new Date(settings.rebuilt_at) : null;
  const rebuiltLabel = rebuiltAt && !Number.isNaN(rebuiltAt.getTime())
    ? rebuiltAt.toLocaleString(currentLocale())
    : settings.rebuilt_at || "";
  const indexBroken = readiness.checks.some(check => check.id === "index_schema" && check.status === "fail");
  // The migration and binding checks carry copy that now lives in the legacy
  // section and the single unbound line below; keeping them here duplicated the
  // same sentence up to five times.
  const checks = readiness.checks
    .filter(check => check.id !== "migration" && check.id !== "manifest_binding" && check.id !== "index_entries")
    .filter(check => !(indexBroken && check.id === "index_fresh"))
    .map(check => ({...check, label: checkLabel(settings, check)}));
  const problems = checks.filter(check => check.status !== "ok");
  const passed = checks.filter(check => check.status === "ok").map(check => check.label);
  const problemRows = problems.map(check => `<li class="is-${check.status}"><span class="knowledge-vault-check-mark" aria-hidden="true">${check.status === "warn" ? "!" : "×"}</span><span>${check.label}</span></li>`).join("");
  const passedRow = passed.length
    ? `<div class="knowledge-vault-checks-passed"><span class="knowledge-vault-check-mark" aria-hidden="true">✓</span><span>${kt("kvs.checks.passed", {items: passed.join(" · ")})}</span></div>`
    : "";
  const unbound = readiness.unbound_projects ?? [];
  const unboundNote = !hasLegacy(settings) && unbound.length
    ? `<p class="knowledge-vault-health-note is-warn">${kt(unbound.length === 1 ? "kvs.health.unboundOne" : "kvs.health.unbound", {count: unbound.length})}</p>`
    : "";
  const emptyNote = settings.index_ready && settings.entry_count === 0
    ? `<p class="knowledge-vault-health-note">${kt("kvs.health.emptyEntries")}</p>`
    : "";
  const canInit = !hasLegacy(settings) && !settings.vault_initialized;
  const initAction = canInit
    ? `<div class="knowledge-vault-actions"><button type="button" id="knowledge-vault-init" class="primary" ${busy ? "disabled" : ""}>${busy === "init" ? kt("kvs.action.initializing") : kt("kvs.action.init")}</button></div>`
    : "";
  return `<div class="knowledge-vault-group knowledge-vault-health">
    <div class="knowledge-vault-group-head"><h3>${kt("kvs.health.title")}</h3><span class="knowledge-vault-badge ${badge.tone}">${badge.label}</span></div>
    ${attentionHTML(attentionFor(settings))}
    <dl class="knowledge-vault-facts">
      <div><dt>${kt("kvs.fact.vaultPath")}</dt><dd><code>${escapeHTML(settings.vault_path || "-")}</code></dd></div>
      <div><dt>${kt("kvs.fact.indexPath")}</dt><dd><code>${escapeHTML(settings.index_path || "-")}</code></dd></div>
      <div><dt>${kt("kvs.fact.entries")}</dt><dd>${kt(settings.entry_count === 1 ? "kvs.fact.entriesOne" : "kvs.fact.entriesValue", {count: settings.entry_count})}</dd></div>
      <div><dt>${kt("kvs.fact.rebuilt")}</dt><dd>${escapeHTML(rebuiltLabel || kt("kvs.fact.never"))}</dd></div>
    </dl>
    ${problemRows ? `<ul class="knowledge-vault-checks">${problemRows}</ul>` : ""}
    ${passedRow}
    ${emptyNote}
    ${unboundNote}
    ${initAction}
  </div>`;
}

function legacyHTML(settings: KnowledgeVaultSettings, busy: string): string {
  if (!hasLegacy(settings)) return "";
  const inUse = inUseUpgrade(settings);
  const unbound = settings.readiness.unbound_projects ?? [];
  const skills = legacySkillCount(settings);
  const verified = hasTimestamp(settings.migration_verified_at)
    ? kt("kvs.legacy.migrationVerified")
    : hasTimestamp(settings.import_verified_at)
      ? kt("kvs.legacy.importVerified")
      : inUse
        ? kt("kvs.legacy.rollbackOnly")
        : kt("kvs.legacy.migrationPending");
  const note = inUse
    ? kt("kvs.legacy.inUseNote")
    : hasTimestamp(settings.migration_verified_at) || hasTimestamp(settings.import_verified_at)
      ? kt("kvs.legacy.populatedNote")
      : "";
  const migrate = settings.readiness.status === "needs_migration"
    ? `<div class="knowledge-vault-actions"><button type="button" id="knowledge-vault-migrate" class="primary" ${busy ? "disabled" : ""}>${busy === "migrate" ? kt("kvs.action.migrating") : kt("kvs.action.migrate")}</button></div>`
    : "";
  return `<div class="knowledge-vault-group knowledge-vault-legacy">
    <div class="knowledge-vault-group-head"><h3>${kt("kvs.legacy.title")}</h3>${inUse ? `<span class="knowledge-vault-badge">${kt("kvs.legacy.badge")}</span>` : ""}</div>
    <dl class="knowledge-vault-facts">
      <div><dt>${kt("kvs.legacy.entries")}</dt><dd>${kt(settings.legacy.entries === 1 ? "kvs.legacy.entriesOne" : "kvs.legacy.entriesValue", {count: settings.legacy.entries})}</dd></div>
      ${skills > 0 ? `<div><dt>${kt("kvs.legacy.skills")}</dt><dd>${kt(skills === 1 ? "kvs.legacy.skillsOne" : "kvs.legacy.skillsValue", {count: skills})}</dd></div>` : ""}
      <div><dt>${kt("kvs.legacy.unbound")}</dt><dd>${unbound.length ? kt(unbound.length === 1 ? "kvs.legacy.unboundOne" : "kvs.legacy.unboundValue", {count: unbound.length}) : kt("kvs.legacy.allBound")}</dd></div>
      ${!inUse || hasTimestamp(settings.migration_verified_at) || hasTimestamp(settings.import_verified_at) ? `<div><dt>${kt("kvs.legacy.migration")}</dt><dd>${verified}</dd></div>` : ""}
    </dl>
    ${note ? `<p class="knowledge-vault-legacy-note">${note}</p>` : ""}
    ${migrate}
  </div>`;
}

function switchHTML(settings: KnowledgeVaultSettings, busy: string, blocked: boolean): string {
  const switchVisible = settings.readiness.switch_visible || settings.configured;
  const source = settings.source === "database" ? kt("kvs.source.database") : kt("kvs.source.startup");
  const sourceLine = `<p class="knowledge-vault-source-line">${kt("kvs.source.line", {source})}</p>`;
  if (!switchVisible) {
    return `<div class="knowledge-vault-group knowledge-vault-source">
      <div class="knowledge-vault-group-head"><h3>${kt("kvs.source.title")}</h3></div>
      <p class="field-help knowledge-vault-switch-note">${kt("kvs.switch.fresh")}</p>
      ${sourceLine}
    </div>`;
  }
  // Once migration/import commits the instance to the vault, the switch is a
  // locked-on status rather than a control: a disabled checked box plus the
  // explicit no-rollback copy, so the owner never reads it as "off is one
  // click away".
  const rollbackLocked = !rollbackAllowed(settings);
  const lockedOn = rollbackLocked && settings.enabled;
  const hint = lockedOn
    ? kt(hasLegacy(settings) ? "kvs.switch.lockedMigrated" : "kvs.switch.lockedNoLegacy")
    : rollbackLocked
      ? kt("kvs.switch.lockedEnable")
      : settings.readiness.status === "needs_migration"
        ? kt("kvs.switch.blockedMigration")
        : kt("kvs.switch.help");
  const lockedBadge = lockedOn ? `<span class="knowledge-vault-badge">${kt("kvs.switch.lockedBadge")}</span>` : "";
  return `<div class="knowledge-vault-group knowledge-vault-source">
    <div class="knowledge-vault-group-head"><h3>${kt("kvs.source.title")}</h3>${lockedBadge}</div>
    <label class="security-setting-toggle knowledge-vault-toggle${lockedOn ? " is-locked" : ""}"><input id="knowledge-vault-enabled" name="knowledge_vault_enabled" type="checkbox" ${settings.enabled ? "checked" : ""} ${busy || blocked || lockedOn ? "disabled" : ""}><span><strong>${kt("kvs.switch.title")}</strong><small>${hint}</small></span></label>
    ${sourceLine}
  </div>`;
}

function importHTML(settings: KnowledgeVaultSettings, busy: string): string {
  if (settings.vault_initialized) return "";
  return `<details class="knowledge-vault-import"${state.importOpen ? " open" : ""}><summary>${kt("kvs.import.title")}</summary>
    <div class="knowledge-vault-import-body">
      <fieldset class="knowledge-vault-import-modes">
        <legend>${kt("kvs.import.legend")}</legend>
        <label class="knowledge-vault-import-mode"><input type="radio" name="knowledge-vault-import-kind" value="local" ${state.importKind === "local" ? "checked" : ""}> <span>${kt("kvs.import.local")}</span></label>
        <label class="knowledge-vault-import-mode"><input type="radio" name="knowledge-vault-import-kind" value="git" ${state.importKind === "git" ? "checked" : ""}> <span>${kt("kvs.import.git")}</span></label>
      </fieldset>
      <div class="knowledge-vault-import-fields">
        <label class="knowledge-vault-import-field" data-import-kind="local"${state.importKind === "local" ? "" : " hidden"}><span>${kt("kvs.import.local")}</span><input id="knowledge-vault-import-path" type="text" autocomplete="off" placeholder="${kt("kvs.import.localPlaceholder")}" value="${escapeHTML(state.importDraft.path)}"></label>
        <label class="knowledge-vault-import-field" data-import-kind="git"${state.importKind === "git" ? "" : " hidden"}><span>${kt("kvs.import.remote")}</span><input id="knowledge-vault-import-remote" type="text" autocomplete="off" placeholder="${kt("kvs.import.remotePlaceholder")}" value="${escapeHTML(state.importDraft.remote)}"></label>
        <label class="knowledge-vault-import-field" data-import-kind="git"${state.importKind === "git" ? "" : " hidden"}><span>${kt("kvs.import.branch")}</span><input id="knowledge-vault-import-branch" type="text" autocomplete="off" placeholder="${kt("kvs.import.branchPlaceholder")}" value="${escapeHTML(state.importDraft.branch)}"></label>
      </div>
      <div class="knowledge-vault-import-actions"><button type="button" id="knowledge-vault-import" ${busy ? "disabled" : ""}>${busy === "import" ? kt("kvs.action.importing") : kt("kvs.action.import")}</button></div>
      <p class="field-help knowledge-vault-import-help">${kt("kvs.import.help")}</p>
    </div>
  </details>`;
}

function settingsBody(): string {
  const settings = state.settings;
  if (state.unsupported) {
    return `<div class="field-help knowledge-vault-note">${kt("kvs.unsupported")}</div>`;
  }
  if (state.error && !settings) {
    return `<div class="security-warning active" role="alert">${kt("kvs.loadFailed", {error: escapeHTML(state.error)})}</div>
      <div class="knowledge-vault-actions"><button type="button" id="knowledge-vault-retry">${kt("kvs.action.retry")}</button></div>`;
  }
  if (!settings) {
    return `<div class="field-help knowledge-vault-note" role="status">${kt("kvs.loading")}</div>`;
  }
  const busy = state.busy;
  const blocked = !settings.enabled && !settings.readiness.can_enable;
  return `
    ${switchHTML(settings, busy, blocked)}
    ${healthHTML(settings, busy)}
    ${legacyHTML(settings, busy)}
    ${state.error ? `<div class="security-warning active" role="alert">${escapeHTML(state.error)}</div>` : ""}
    ${importHTML(settings, busy)}
    <div class="field-help knowledge-vault-note">${kt("kvs.footer")}</div>`;
}

export function renderKnowledgeVaultSettingsPanel(context: KnowledgeVaultSettingsContext): string {
  activeContext = context;
  installImportStyles();
  if (!state.loaded && !state.loading) void loadSettings(context);
  const statusLabel = state.unsupported
    ? kt("kvs.state.unsupported")
    : state.settings
      ? kt(state.settings.enabled ? "kvs.state.enabled" : "kvs.state.disabled")
      : state.error
        ? kt("kvs.state.loadFailed")
        : kt("kvs.state.loading");
  const stateClass = state.settings?.enabled ? " is-on" : "";
  return `<section class="panel spaced account-security-panel knowledge-vault-settings-panel" aria-labelledby="knowledge-vault-settings-title"${state.busy ? ` aria-busy="true"` : ""}>
    <div class="panel-head"><strong id="knowledge-vault-settings-title" tabindex="-1">${kt("kvs.title")}</strong><span class="knowledge-vault-state${stateClass}">${statusLabel}</span></div>
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
  const details = document.querySelector<HTMLDetailsElement>(".knowledge-vault-import");
  details?.addEventListener("toggle", () => { state.importOpen = details.open; });
  document.querySelectorAll<HTMLInputElement>('input[name="knowledge-vault-import-kind"]').forEach(input => {
    input.addEventListener("change", () => { if (input.checked) setImportKind(input.value); });
  });
  const draftInputs: Array<[string, keyof SettingsPanelState["importDraft"]]> = [
    ["#knowledge-vault-import-path", "path"],
    ["#knowledge-vault-import-remote", "remote"],
    ["#knowledge-vault-import-branch", "branch"],
  ];
  draftInputs.forEach(([selector, key]) => {
    const input = document.querySelector<HTMLInputElement>(selector);
    input?.addEventListener("input", () => { state.importDraft[key] = input.value; });
  });
}

export function __resetKnowledgeVaultSettingsForTest(): void {
  state = {
    loaded: false, loading: false, settings: null, error: "", unsupported: false, busy: "",
    importKind: "local", importOpen: false, importDraft: {path: "", remote: "", branch: ""},
  };
  activeContext = null;
  pendingLoad = null;
}

export const __knowledgeVaultSettingsActionsForTest = {setEnabled, initializeSamples, migrateLegacy, importExistingVault};
