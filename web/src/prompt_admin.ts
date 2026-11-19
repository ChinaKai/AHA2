import {api} from "./api.js";
import type {
  PromptAnchorPreview,
  PromptEntry,
  PromptEntryAnchor,
  PromptMigrationPreview,
  PromptTemplate,
} from "./types.js";

export type PromptAdminTab = "entries" | "migration" | "legacy";

// PromptAdminView is the complete render input. The live page uses `state`;
// tests pass fixtures directly so every override shape can be rendered without
// a server or a browser.
export interface PromptAdminView {
  tab: PromptAdminTab;
  templates: PromptTemplate[];
  selectedTemplateID: string;
  drafts: Record<string, string>;
  catalog: PromptEntry[] | null;
  catalogError?: string;
  migration: PromptMigrationPreview | null;
  migrationError?: string;
  selectedEntryID: string;
  selectedAnchorKey: string;
  preview: PromptAnchorPreview | null;
  previewKey?: string;
  previewError?: string;
}

const state = {
  loaded: false,
  tab: "entries" as PromptAdminTab,
  templates: [] as PromptTemplate[],
  selectedTemplateID: "",
  drafts: {} as Record<string, string>,
  catalog: null as PromptEntry[] | null,
  catalogError: "",
  migration: null as PromptMigrationPreview | null,
  migrationError: "",
  selectedEntryID: "",
  selectedAnchorKey: "",
  preview: null as PromptAnchorPreview | null,
  previewKey: "",
  previewError: "",
};

function escapeHTML(value: unknown): string {
  return String(value ?? "")
    .replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;").replaceAll("'", "&#039;");
}

function anchorRef(entryID: string, key: string): string {
  return `${entryID}#${key}`;
}

function splitAnchorRef(value: string): [string, string] {
  const index = value.indexOf("#");
  if (index < 0) return ["", ""];
  return [value.slice(0, index), value.slice(index + 1)];
}

function entryFor(view: PromptAdminView): PromptEntry | undefined {
  const entries = view.catalog || [];
  return entries.find(item => item.id === view.selectedEntryID) || entries[0];
}

function anchorFor(entry: PromptEntry | undefined, view: PromptAdminView): PromptEntryAnchor | undefined {
  return entry?.anchors.find(item => item.key === view.selectedAnchorKey) || entry?.anchors[0];
}

function kindLabel(kind: string): string {
  if (kind === "generated") return "生成文件";
  if (kind === "resource") return "只读资源";
  if (kind === "fragment") return "内容片段";
  return "常驻模板";
}

function anchorBadges(anchor: PromptEntryAnchor): string {
  const badges: string[] = [];
  if (anchor.required) badges.push('<b class="prompt-chip required">必需</b>');
  if (anchor.dynamic) {
    badges.push('<b class="prompt-chip protected">运行时生成/不可单独编辑</b>');
  } else if (anchor.kind === "resource") {
    badges.push('<b class="prompt-chip protected">只读资源</b>');
  } else if (anchor.protected || !anchor.editable) {
    badges.push('<b class="prompt-chip protected">受保护/只读</b>');
  } else {
    badges.push('<span class="prompt-chip editable">可编辑（旧 ID）</span>');
  }
  if (anchor.kind === "fragment") badges.push('<span class="prompt-chip">内容片段</span>');
  return badges.join("");
}

function previewBadges(preview: PromptAnchorPreview): string {
  const badges: string[] = [];
  if (preview.required) badges.push('<b class="prompt-chip required">必需</b>');
  if (preview.dynamic) badges.push('<b class="prompt-chip protected">运行时生成/不可单独编辑</b>');
  else if (preview.kind === "resource") badges.push('<b class="prompt-chip protected">只读资源</b>');
  else if (preview.protected || !preview.editable) badges.push('<b class="prompt-chip protected">受保护/只读</b>');
  else badges.push('<span class="prompt-chip editable">可编辑（旧 ID）</span>');
  return badges.join("");
}

function updatedLabel(value?: string): string {
  if (!value) return "";
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? "" : ` · ${parsed.toLocaleString()}`;
}

export async function loadPromptCatalog(): Promise<void> {
  const response = await api.promptTemplates();
  state.templates = response.templates || [];
  state.selectedTemplateID ||= state.templates[0]?.id || "";
  if (!state.templates.some(item => item.id === state.selectedTemplateID)) {
    state.selectedTemplateID = state.templates[0]?.id || "";
  }
  state.loaded = true;

  try {
    const catalog = await api.promptEntries();
    state.catalog = catalog.entries || [];
    state.catalogError = "";
    const entry = state.catalog.find(item => item.id === state.selectedEntryID) || state.catalog[0];
    state.selectedEntryID = entry?.id || "";
    if (!entry?.anchors.some(item => item.key === state.selectedAnchorKey)) {
      state.selectedAnchorKey = entry?.anchors[0]?.key || "";
    }
  } catch (error) {
    state.catalog = state.catalog || [];
    state.catalogError = error instanceof Error ? error.message : String(error);
  }
  try {
    const migration = await api.promptMigrationPreview();
    state.migration = migration.migration || null;
    state.migrationError = "";
  } catch (error) {
    state.migration = null;
    state.migrationError = error instanceof Error ? error.message : String(error);
  }
  if (state.selectedEntryID && state.selectedAnchorKey) {
    await refreshAnchorPreview();
  }
}

async function refreshAnchorPreview(): Promise<void> {
  if (!state.selectedEntryID || !state.selectedAnchorKey) return;
  const expected = anchorRef(state.selectedEntryID, state.selectedAnchorKey);
  state.previewKey = expected;
  state.preview = null;
  state.previewError = "";
  try {
    const response = await api.promptEntryPreview(state.selectedEntryID, state.selectedAnchorKey);
    if (state.previewKey !== expected) return;
    state.preview = response.preview;
  } catch (error) {
    if (state.previewKey !== expected) return;
    state.previewError = error instanceof Error ? error.message : String(error);
  }
}

function renderTabs(tab: PromptAdminTab): string {
  return `<nav class="prompt-admin-tabs" aria-label="提示词视图">
    <button type="button" data-prompt-tab="entries" class="${tab === "entries" ? "active" : ""}">入口预览</button>
    <button type="button" data-prompt-tab="migration" class="${tab === "migration" ? "active" : ""}">迁移预览</button>
    <button type="button" data-prompt-tab="legacy" class="${tab === "legacy" ? "active" : ""}">旧模板管理</button>
  </nav>`;
}

function renderEntryList(view: PromptAdminView): string {
  return (view.catalog || []).map(entry => `<button type="button" data-prompt-entry="${escapeHTML(entry.id)}" class="prompt-entry-item ${entry.id === view.selectedEntryID ? "active" : ""}">
    <strong>${escapeHTML(entry.name)}</strong>
    <small>${escapeHTML(entry.id)} · ${entry.anchors.length} 个锚点</small>
  </button>`).join("");
}

function renderAnchorList(entry: PromptEntry, view: PromptAdminView): string {
  const activeKey = anchorFor(entry, view)?.key || "";
  return entry.anchors.map(anchor => `<button type="button" class="prompt-anchor-item ${anchor.key === activeKey ? "active" : ""}" data-prompt-anchor="${escapeHTML(anchorRef(entry.id, anchor.key))}">
    <span class="prompt-anchor-title"><strong>${escapeHTML(anchor.name)}</strong><code>${escapeHTML(anchor.legacy_id)}</code></span>
    <span class="prompt-anchor-tags">${anchorBadges(anchor)}</span>
    <small>${escapeHTML(anchor.key)} · ${escapeHTML(kindLabel(anchor.kind))} · v${anchor.version}${anchor.frozen_version !== anchor.version ? `（冻结 v${anchor.frozen_version}）` : ""}</small>
  </button>`).join("");
}

function renderAnchorPreview(entry: PromptEntry, view: PromptAdminView): string {
  const anchor = anchorFor(entry, view);
  if (!anchor) return '<section class="prompt-anchor-preview"><div class="empty">该入口没有锚点</div></section>';
  const expected = anchorRef(entry.id, anchor.key);
  const matching = view.preview && view.preview.entry_id === entry.id && view.preview.key === anchor.key;
  if (view.previewError && view.previewKey === expected) {
    return `<section class="prompt-anchor-preview" data-prompt-anchor-preview-panel><div class="banner error">${escapeHTML(view.previewError)}</div></section>`;
  }
  if (!matching) {
    return `<section class="prompt-anchor-preview" data-prompt-anchor-preview-panel><div class="loading">正在读取 ${escapeHTML(expected)} 的只读预览…</div></section>`;
  }
  const preview = view.preview as PromptAnchorPreview;
  let note = "入口预览只读；编辑与恢复仍只在「旧模板管理」页按旧 ID 操作，本页没有保存按钮。";
  if (preview.dynamic) {
    note = "运行时生成/不可单独编辑。预览来自代码模板与示例 Turn 数据，不会写入或覆盖提示词 override。";
  } else if (preview.protected || !preview.editable) {
    note = "只读资源/受保护锚点；入口预览不提供保存入口。";
  }
  const legacyWarning = preview.legacy_override
    ? `<div class="banner warning">旧 ID ${escapeHTML(preview.legacy_id)} 存在 Owner override（v${preview.legacy_override_version || 1}）。入口模型不会应用它，迁移预览将其列为冲突，当前内容不会被静默覆盖。</div>`
    : "";
  const renderError = preview.render_error
    ? `<div class="banner error">示例渲染失败：${escapeHTML(preview.render_error)}</div>`
    : "";
  return `<section class="prompt-anchor-preview" data-prompt-anchor-preview-panel>
    <header>
      <div><h2>${escapeHTML(preview.name)}</h2><p>${escapeHTML(preview.description || kindLabel(preview.kind))}</p></div>
      <code>${escapeHTML(anchorRef(entry.id, anchor.key))}</code>
    </header>
    <div class="prompt-anchor-meta">
      <code>${escapeHTML(preview.legacy_id)}</code>
      ${previewBadges(preview)}
      <span>${escapeHTML(preview.layer)}</span>
      <span>${escapeHTML(preview.source)} · v${preview.version}</span>
      <span>冻结 v${preview.frozen_version}</span>
    </div>
    <div class="prompt-readonly-note">${escapeHTML(note)}</div>
    ${legacyWarning}
    <pre class="prompt-anchor-content" data-prompt-anchor-preview="content" aria-label="锚点源模板（只读）">${escapeHTML(preview.content)}</pre>
    ${renderError}
    <details class="prompt-anchor-rendered">
      <summary>示例渲染（只读）</summary>
      <pre>${escapeHTML(preview.rendered || "无示例渲染")}</pre>
    </details>
  </section>`;
}

function renderEntriesTab(view: PromptAdminView): string {
  if (view.catalogError) {
    return `<div class="prompt-admin-error" data-prompt-entries-error><div class="banner error">入口目录加载失败：${escapeHTML(view.catalogError)}</div><p class="field-help">旧模板列表仍可在「旧模板管理」页使用。</p></div>`;
  }
  const entries = view.catalog || [];
  if (!entries.length) return '<div class="empty">暂无可预览的模板入口</div>';
  const entry = entryFor(view);
  if (!entry) return '<div class="empty">暂无可预览的模板入口</div>';
  return `<div class="prompt-entry-layout" data-prompt-entries-view>
    <aside class="prompt-entry-list" aria-label="模板入口">${renderEntryList(view)}</aside>
    <section class="prompt-anchor-pane">
      <header>
        <h2>${escapeHTML(entry.name)}</h2>
        <p>${escapeHTML(entry.description)}</p>
        <small>旧模板 ID 仍作为别名解析到「入口#锚点」；旧模板管理页及其编辑能力至少保留一个版本。</small>
      </header>
      <div class="prompt-anchor-list">${renderAnchorList(entry, view)}</div>
    </section>
    ${renderAnchorPreview(entry, view)}
  </div>`;
}

function renderMigrationGroup(kind: string, title: string, count: number, emptyText: string, rows: string): string {
  return `<section class="prompt-migration-group" data-prompt-migration-group="${escapeHTML(kind)}">
    <header><h3>${escapeHTML(title)}</h3><span>${count}</span></header>
    ${count ? `<ul>${rows}</ul>` : `<p class="prompt-migration-empty">${escapeHTML(emptyText)}</p>`}
  </section>`;
}

function renderMigrationTab(view: PromptAdminView): string {
  if (view.migrationError) {
    return `<div class="prompt-admin-error" data-prompt-migration-error><div class="banner error">迁移预览加载失败：${escapeHTML(view.migrationError)}</div></div>`;
  }
  const migration = view.migration;
  if (!migration) return '<div class="prompt-admin-error"><div class="loading">正在读取迁移预览…</div></div>';
  const mergedRows = migration.merged.map(item => `<li data-prompt-migration-merged>
    <div><strong><code>${escapeHTML(anchorRef(item.entry_id, item.anchor_key))}</code></strong><span class="prompt-chip ok">可迁移</span></div>
    <small>来源旧 ID：${item.legacy_ids.map(id => `<code>${escapeHTML(id)}</code>`).join("、")} · v${item.version}${escapeHTML(updatedLabel(item.updated_at))}</small>
  </li>`).join("");
  const conflictRows = migration.conflicts.map(item => `<li data-prompt-migration-conflict>
    <div><strong><code>${escapeHTML(item.legacy_id)}</code> → <code>${escapeHTML(anchorRef(item.entry_id, item.anchor_key))}</code></strong><span class="prompt-chip danger">${escapeHTML(item.code)}</span></div>
    <small>${escapeHTML(item.message)} · v${item.version}</small>
  </li>`).join("");
  const orphanRows = migration.orphans.map(item => `<li data-prompt-migration-orphan>
    <div><strong><code>${escapeHTML(item.legacy_id)}</code></strong><span class="prompt-chip warning">保留只读</span></div>
    <small>未映射到入口；保留只读、不删除、不覆盖 · v${item.version}${escapeHTML(updatedLabel(item.updated_at))}</small>
  </li>`).join("");
  const ignoredRows = migration.ignored.map(item => `<li data-prompt-migration-ignored>
    <div><strong><code>${escapeHTML(item.legacy_id)}</code> → <code>${escapeHTML(anchorRef(item.entry_id, item.anchor_key))}</code></strong><span class="prompt-chip">旧路径已忽略</span></div>
    <small>${escapeHTML(item.message)} · v${item.version}</small>
  </li>`).join("");
  const mappings = migration.mappings.map(item => `<li>
    <code>${escapeHTML(item.legacy_id)}</code><span>→</span><code>${escapeHTML(anchorRef(item.entry_id, item.anchor_key))}</code>
    <small>${escapeHTML(item.kind)}${item.dynamic ? " · 运行时生成" : item.editable ? "" : " · 只读"} · 冻结 v${item.frozen_version}</small>
  </li>`).join("");
  return `<div class="prompt-migration-panel" data-prompt-migration-panel>
    <div class="prompt-readonly-note">只读预览：不会写入数据库，不会自动迁移，也不会覆盖 Owner override。每个旧 override 只出现一次。</div>
    <div class="prompt-migration-summary">
      <span><b>${migration.merged.length}</b> 可迁移</span>
      <span><b>${migration.conflicts.length}</b> 冲突</span>
      <span><b>${migration.orphans.length}</b> 孤儿</span>
      <span><b>${migration.ignored.length}</b> 已忽略</span>
    </div>
    <div class="prompt-migration-groups">
      ${renderMigrationGroup("merged", "可迁移到锚点", migration.merged.length, "没有可迁移的 override", mergedRows)}
      ${renderMigrationGroup("conflicts", "冲突（不会写入）", migration.conflicts.length, "没有冲突", conflictRows)}
      ${renderMigrationGroup("orphans", "孤儿 override（保留只读）", migration.orphans.length, "没有孤儿 override", orphanRows)}
      ${renderMigrationGroup("ignored", "旧路径已忽略", migration.ignored.length, "没有已忽略的 override", ignoredRows)}
    </div>
    <details class="prompt-migration-mappings">
      <summary>旧 ID 映射（${migration.mappings.length}）</summary>
      <ul>${mappings}</ul>
    </details>
  </div>`;
}

function templateList(templates: PromptTemplate[], selected?: PromptTemplate): string {
  return templates.map(item => `<button type="button" data-prompt-template="${escapeHTML(item.id)}" class="prompt-template-item ${item.id === selected?.id ? "active" : ""}">
    <span><strong>${escapeHTML(item.name)}</strong><small>${escapeHTML(item.layer)} · v${item.version}</small></span>
    <span class="prompt-source ${escapeHTML(item.source)}">${escapeHTML(item.source)}</span>
    ${item.required ? '<b title="必需模板">Required</b>' : ""}
  </button>`).join("");
}

function renderLegacyTab(view: PromptAdminView): string {
  const selected = view.templates.find(item => item.id === view.selectedTemplateID) || view.templates[0];
  if (!selected) return '<div class="empty">暂无提示词模板</div>';
  const content = view.drafts[selected.id] ?? selected.content;
  return `<div class="prompt-template-layout">
    <aside class="prompt-template-list">${templateList(view.templates, selected)}</aside>
    <section class="prompt-editor">
      <header><div><h2>${escapeHTML(selected.name)}</h2><p>${escapeHTML(selected.description)}</p></div><code>${escapeHTML(selected.id)}</code></header>
      <div class="prompt-editor-meta"><span>${escapeHTML(selected.layer)}</span><span>${escapeHTML(selected.source)}</span><span>v${selected.version}</span>${selected.required ? "<span>required</span>" : ""}</div>
      <textarea data-prompt-template-content data-ui-key="prompt-template-content:${escapeHTML(selected.id)}" ${selected.editable ? "" : "readonly"}>${escapeHTML(content)}</textarea>
      <footer>
        <span>${Array.from(content).length.toLocaleString()} chars</span>
        ${selected.editable ? `<button type="button" id="reset-prompt-template" ${selected.source === "builtin" ? "disabled" : ""}>恢复内置</button><button type="button" id="save-prompt-template" class="primary">保存模板</button>` : '<span class="managed-template">内置只读模板</span>'}
      </footer>
    </section>
  </div>`;
}

// Route-local styles keep the first-screen stylesheet unchanged. The prompt
// page is lazy-loaded, so this text ships with its chunk and is emitted only
// while the read-only entry views are on screen.
const PROMPT_ADMIN_STYLES = `
.prompt-admin-body { display:grid; grid-template-rows:auto minmax(0,1fr); min-height:0; height:100%; overflow:hidden; }
.prompt-admin-tabs { display:flex; gap:4px; padding:4px; border:1px solid var(--line); border-bottom:0; background:#fff; }
.prompt-admin-tabs button { min-height:30px; padding:4px 12px; border-color:transparent; background:transparent; color:var(--muted); }
.prompt-admin-tabs button.active { border-color:#bfdbfe; background:#eff6ff; color:#1d4ed8; }
.prompt-entry-layout { display:grid; grid-template-columns:220px 360px minmax(0,1fr); min-height:0; overflow:hidden; border:1px solid var(--line); background:#fff; }
.prompt-entry-list { min-height:0; overflow-y:auto; border-right:1px solid var(--line); background:#fafbfc; }
.prompt-entry-item { display:grid; gap:5px; width:100%; min-height:68px; padding:12px 14px; border:0; border-bottom:1px solid var(--line); border-radius:0; background:transparent; text-align:left; }
.prompt-entry-item.active { background:#edf4ff; box-shadow:inset 3px 0 var(--blue); }
.prompt-entry-item strong { font-size:12px; }
.prompt-entry-item small { color:var(--muted); font-size:9px; }
.prompt-anchor-pane { display:grid; grid-template-rows:auto minmax(0,1fr); min-width:0; min-height:0; overflow:hidden; border-right:1px solid var(--line); }
.prompt-anchor-pane > header { padding:14px 16px 11px; border-bottom:1px solid var(--line); }
.prompt-anchor-pane h2 { margin:0; font-size:16px; }
.prompt-anchor-pane header p { margin:5px 0 0; color:var(--muted); font-size:10px; }
.prompt-anchor-pane header small { display:block; margin-top:7px; color:#64748b; font-size:9px; line-height:1.45; }
.prompt-anchor-list { min-height:0; overflow:auto; }
.prompt-anchor-item { display:grid; gap:5px; width:100%; padding:10px 12px; border:0; border-bottom:1px solid var(--line); border-radius:0; background:transparent; text-align:left; }
.prompt-anchor-item.active { background:#edf4ff; box-shadow:inset 3px 0 var(--blue); }
.prompt-anchor-title { display:flex; align-items:baseline; justify-content:space-between; gap:8px; min-width:0; }
.prompt-anchor-title strong { min-width:0; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; font-size:11px; }
.prompt-anchor-title code, .prompt-anchor-item small { color:var(--muted); font-size:9px; }
.prompt-anchor-tags { display:flex; flex-wrap:wrap; gap:3px; }
.prompt-chip { display:inline-block; padding:2px 5px; border-radius:4px; background:#eef1f4; color:#64748b; font-size:8px; font-weight:700; }
.prompt-chip.required { background:#fee2e2; color:#b91c1c; }
.prompt-chip.protected { background:#f1f5f9; color:#475569; }
.prompt-chip.editable, .prompt-chip.ok { background:#ecfdf5; color:#047857; }
.prompt-chip.danger { background:#fee2e2; color:#b91c1c; }
.prompt-chip.warning { background:#fef3c7; color:#b45309; }
.prompt-anchor-preview { display:grid; grid-template-rows:auto auto auto auto minmax(140px,1fr) auto; gap:8px; min-width:0; min-height:0; overflow:auto; padding:16px; }
.prompt-anchor-preview > header { display:flex; align-items:flex-start; justify-content:space-between; gap:12px; }
.prompt-anchor-preview h2 { margin:0; font-size:16px; }
.prompt-anchor-preview header p { margin:5px 0 0; color:var(--muted); font-size:10px; }
.prompt-anchor-preview header code { color:var(--muted); font-size:9px; white-space:nowrap; }
.prompt-anchor-meta { display:flex; flex-wrap:wrap; align-items:center; gap:5px; }
.prompt-anchor-meta > span { padding:2px 5px; background:#eef1f4; color:#53606d; font-size:8px; }
.prompt-anchor-meta > code { padding:2px 5px; background:#f8fafc; color:#475569; font-size:9px; }
.prompt-readonly-note { padding:8px 10px; border:1px solid #dbeafe; background:#eff6ff; color:#1e40af; font-size:10px; line-height:1.5; }
.prompt-anchor-content { min-height:140px; max-height:46vh; overflow:auto; margin:0; padding:12px; border:1px solid #e2e8f0; background:#fafbfc; white-space:pre-wrap; overflow-wrap:anywhere; font:10px/1.55 ui-monospace,SFMono-Regular,Menlo,monospace; }
.prompt-anchor-rendered { min-width:0; }
.prompt-anchor-rendered summary { cursor:pointer; color:var(--muted); font-size:10px; }
.prompt-anchor-rendered pre { max-height:28vh; overflow:auto; margin:8px 0 0; padding:10px; border:1px solid #e2e8f0; background:#fafbfc; white-space:pre-wrap; overflow-wrap:anywhere; font-size:10px; }
.prompt-migration-panel { min-height:0; overflow:auto; padding:16px; border:1px solid var(--line); background:#fff; }
.prompt-migration-summary { display:flex; flex-wrap:wrap; gap:8px; margin:10px 0; }
.prompt-migration-summary span { padding:5px 8px; border:1px solid var(--line); background:#fafbfc; color:var(--muted); font-size:10px; }
.prompt-migration-summary b { color:var(--text); }
.prompt-migration-groups { display:grid; grid-template-columns:repeat(2,minmax(0,1fr)); gap:10px; }
.prompt-migration-group { min-width:0; border:1px solid var(--line); background:#fff; }
.prompt-migration-group > header { display:flex; align-items:center; justify-content:space-between; gap:8px; padding:9px 11px; border-bottom:1px solid var(--line); background:#fafbfc; }
.prompt-migration-group h3 { margin:0; font-size:11px; }
.prompt-migration-group > header span { min-width:20px; padding:1px 5px; border-radius:9px; background:#eef1f4; color:var(--muted); font-size:9px; text-align:center; }
.prompt-migration-group ul, .prompt-migration-mappings ul { margin:0; padding:0; list-style:none; }
.prompt-migration-group li { display:grid; gap:4px; padding:9px 11px; border-bottom:1px solid var(--line); overflow-wrap:anywhere; }
.prompt-migration-group li:last-child { border-bottom:0; }
.prompt-migration-group li > div { display:flex; flex-wrap:wrap; align-items:center; gap:5px; font-size:10px; }
.prompt-migration-group li small { color:var(--muted); font-size:9px; line-height:1.45; }
.prompt-migration-group code { font-size:9px; }
.prompt-migration-empty { margin:0; padding:12px; color:var(--muted); font-size:10px; }
.prompt-migration-mappings { margin-top:12px; border-top:1px solid var(--line); padding-top:10px; }
.prompt-migration-mappings summary { cursor:pointer; color:var(--muted); font-size:10px; }
.prompt-migration-mappings li { display:flex; flex-wrap:wrap; align-items:baseline; gap:6px; padding:5px 0; border-bottom:1px dashed #eef1f4; font-size:9px; }
.prompt-migration-mappings li small { color:var(--muted); }
.prompt-admin-error { min-height:0; overflow:auto; padding:16px; border:1px solid var(--line); background:#fff; }
@media (max-width:760px) {
  .prompt-admin-tabs { overflow-x:auto; }
  .prompt-admin-tabs button { flex:0 0 auto; }
  .prompt-entry-layout { grid-template-columns:1fr; grid-template-rows:auto auto minmax(0,1fr); overflow-y:auto; }
  .prompt-entry-list { display:flex; max-width:100%; overflow-x:auto; overflow-y:hidden; border-right:0; border-bottom:1px solid var(--line); }
  .prompt-entry-item { flex:0 0 180px; border-right:1px solid var(--line); border-bottom:0; }
  .prompt-anchor-pane { border-right:0; border-bottom:1px solid var(--line); }
  .prompt-anchor-pane > header { padding:11px 12px 9px; }
  .prompt-anchor-list { max-height:190px; }
  .prompt-anchor-preview { padding:12px; }
  .prompt-anchor-content { max-height:34vh; }
  .prompt-migration-panel { padding:12px; }
  .prompt-migration-groups { grid-template-columns:1fr; }
}
`;

export function renderPromptAdmin(view?: PromptAdminView): string {
  if (!view && !state.loaded) return '<section class="page"><div class="loading">正在加载提示词模板...</div></section>';
  const data: PromptAdminView = view ?? state;
  const content = data.tab === "entries"
    ? renderEntriesTab(data)
    : data.tab === "migration"
      ? renderMigrationTab(data)
      : renderLegacyTab(data);
  return `<section class="page prompt-page">
    <style>${PROMPT_ADMIN_STYLES}</style>
    <header class="page-head"><div><h1>提示词</h1><p>两个入口的只读预览、迁移预览，以及保留的旧 ID 模板管理。</p></div></header>
    <div class="prompt-admin-body">
      ${renderTabs(data.tab)}
      ${content}
    </div>
  </section>`;
}

export function bindPromptAdmin(
  render: () => void,
  notify: (kind: "notice" | "error", message: string) => void,
): void {
  document.querySelectorAll<HTMLElement>("[data-prompt-tab]").forEach(button => button.addEventListener("click", () => {
    const tab = button.dataset.promptTab as PromptAdminTab | undefined;
    if (tab === "entries" || tab === "migration" || tab === "legacy") state.tab = tab;
    if (state.tab === "legacy") state.drafts = state.drafts || {};
    render();
  }));
  document.querySelectorAll<HTMLElement>("[data-prompt-entry]").forEach(button => button.addEventListener("click", async () => {
    state.selectedEntryID = button.dataset.promptEntry || "";
    const entry = state.catalog?.find(item => item.id === state.selectedEntryID);
    state.selectedAnchorKey = entry?.anchors[0]?.key || "";
    state.preview = null;
    state.previewKey = "";
    state.previewError = "";
    render();
    await refreshAnchorPreview();
    render();
  }));
  document.querySelectorAll<HTMLElement>("[data-prompt-anchor]").forEach(button => button.addEventListener("click", async () => {
    const [entryID, key] = splitAnchorRef(button.dataset.promptAnchor || "");
    if (!entryID || !key) return;
    state.selectedEntryID = entryID;
    state.selectedAnchorKey = key;
    state.preview = null;
    state.previewKey = "";
    state.previewError = "";
    render();
    await refreshAnchorPreview();
    render();
  }));
  document.querySelectorAll<HTMLElement>("[data-prompt-template]").forEach(button => button.addEventListener("click", () => {
    const current = document.querySelector<HTMLTextAreaElement>("[data-prompt-template-content]");
    if (current && state.selectedTemplateID) state.drafts[state.selectedTemplateID] = current.value;
    state.selectedTemplateID = button.dataset.promptTemplate || "";
    render();
  }));
  document.querySelector<HTMLTextAreaElement>("[data-prompt-template-content]")?.addEventListener("input", event => {
    if (state.selectedTemplateID) state.drafts[state.selectedTemplateID] = (event.currentTarget as HTMLTextAreaElement).value;
  });
  document.querySelector("#save-prompt-template")?.addEventListener("click", async () => {
    const content = document.querySelector<HTMLTextAreaElement>("[data-prompt-template-content]")?.value || "";
    try {
      await api.updatePromptTemplate(state.selectedTemplateID, content);
      delete state.drafts[state.selectedTemplateID];
      await loadPromptCatalog();
      notify("notice", "提示词模板已保存");
    } catch (error) {
      notify("error", error instanceof Error ? error.message : String(error));
    }
    render();
  });
  document.querySelector("#reset-prompt-template")?.addEventListener("click", async () => {
    if (!window.confirm("恢复该模板的内置内容？")) return;
    try {
      await api.resetPromptTemplate(state.selectedTemplateID);
      delete state.drafts[state.selectedTemplateID];
      await loadPromptCatalog();
      notify("notice", "已恢复内置模板");
    } catch (error) {
      notify("error", error instanceof Error ? error.message : String(error));
    }
    render();
  });
}
