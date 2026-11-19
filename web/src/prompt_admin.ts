import {api} from "./api.js";
import type {PromptScenarioPreview, PromptTemplate} from "./types.js";

export const DEFAULT_PREVIEW_MESSAGE = "这是一条测试消息";

export interface PromptScenarioSource {
  id: string;
  name: string;
}

export interface PromptScenarioOption {
  id: string;
  name: string;
  description: string;
  messageSources: PromptScenarioSource[];
}

const WEB_SOURCES: PromptScenarioSource[] = [
  {id: "owner", name: "Owner"},
  {id: "pet", name: "宠物"},
];

const CHANNEL_PRIVATE_SOURCES: PromptScenarioSource[] = [
  {id: "channel-owner", name: "渠道私聊 Owner"},
];

// Group scenarios keep SourceKind=channel_message in production and switch the
// sender actor between a member and a verified bot; the preview exposes the
// same two shapes instead of a separate message kind.
const CHANNEL_GROUP_SOURCES: PromptScenarioSource[] = [
  {id: "channel-group-member", name: "渠道群聊成员"},
  {id: "channel-group-bot", name: "群聊机器人"},
];

// The preview shapes are stable product scenarios. The server validates the
// same IDs; keeping the list here avoids an extra round trip just to open the
// dialog.
export const PROMPT_SCENARIOS: PromptScenarioOption[] = [
  {
    id: "web-task-single-main",
    name: "Web 普通 Task，单 Agent，主 Agent",
    description: "Web Owner 消息，普通 Task，单 Agent 主 Agent。",
    messageSources: WEB_SOURCES,
  },
  {
    id: "web-task-multi-main",
    name: "Web 普通 Task，多 Agent，主 Agent（自动协作）",
    description: "Web Owner 消息，多 Agent 自动协作下的主 Agent。",
    messageSources: WEB_SOURCES,
  },
  {
    id: "web-task-multi-sub",
    name: "Web 普通 Task，多 Agent，子 Agent（无协作策略段）",
    description: "多 Agent 自动协作下的子 Agent；不包含协作策略段。",
    messageSources: WEB_SOURCES,
  },
  {
    id: "web-pet-task-single-main",
    name: "Web 宠物 Task，单 Agent，主 Agent",
    description: "宠物 Task 的主 Agent，使用宠物身份模板。",
    messageSources: WEB_SOURCES,
  },
  {
    id: "channel-private-unbound-assistant-main",
    name: "渠道私聊，未绑定 Task，渠道助手，单 Agent，主 Agent",
    description: "渠道私聊未绑定 Task，渠道助手身份处理消息。",
    messageSources: CHANNEL_PRIVATE_SOURCES,
  },
  {
    id: "channel-private-task-single-task-agent",
    name: "渠道私聊，已绑定 Task，单 Agent，任务 Agent",
    description: "渠道私聊 task_route，单 Agent 任务 Agent。",
    messageSources: CHANNEL_PRIVATE_SOURCES,
  },
  {
    id: "channel-private-task-multi-main",
    name: "渠道私聊，已绑定 Task，多 Agent，主 Agent",
    description: "渠道私聊 task_route，多 Agent 自动协作主 Agent。",
    messageSources: CHANNEL_PRIVATE_SOURCES,
  },
  {
    id: "channel-group-unbound-digital-human-main",
    name: "渠道群聊，未绑定 Task，群聊电子人，单 Agent，主 Agent，群聊规则",
    description: "渠道群聊未绑定 Task，群聊电子人身份与群聊规则；消息来源可切换群聊成员或群聊机器人。",
    messageSources: CHANNEL_GROUP_SOURCES,
  },
  {
    id: "channel-group-task-single-task-agent",
    name: "渠道群聊，已绑定 Task，单 Agent，任务 Agent，群聊规则",
    description: "渠道群聊 task_route，单 Agent 任务 Agent 与群聊规则；消息来源可切换群聊成员或群聊机器人。",
    messageSources: CHANNEL_GROUP_SOURCES,
  },
  {
    id: "channel-group-task-multi-main",
    name: "渠道群聊，已绑定 Task，多 Agent，主 Agent，群聊规则",
    description: "渠道群聊 task_route，多 Agent 自动协作主 Agent 与群聊规则；消息来源可切换群聊成员或群聊机器人。",
    messageSources: CHANNEL_GROUP_SOURCES,
  },
];

export interface PromptAdminView {
  loaded: boolean;
  templates: PromptTemplate[];
  selectedTemplateID: string;
  editing: boolean;
  editContent: string;
  generation: number;
  switchTargetID: string;
  previewScenario: string;
  previewMessage: string;
  previewSource: string;
  preview: PromptScenarioPreview | null;
  previewError: string;
  previewLoading: boolean;
}

const state: PromptAdminView = {
  loaded: false,
  templates: [],
  selectedTemplateID: "",
  editing: false,
  editContent: "",
  generation: 0,
  switchTargetID: "",
  previewScenario: PROMPT_SCENARIOS[0].id,
  previewMessage: DEFAULT_PREVIEW_MESSAGE,
  previewSource: "owner",
  preview: null,
  previewError: "",
  previewLoading: false,
};

let previewRequestID = 0;
let switchIntent = 0;

// Explicit edit-session state. A save response may only finish the session it
// started; a template switch, cancel, or further typing advances the generation
// so a late response can never close another template's editor.
export interface PromptEditorState {
  editing: boolean;
  selectedTemplateID: string;
  editContent: string;
  generation: number;
}

export interface PromptSaveToken {
  generation: number;
  templateID: string;
}

export function createPromptEditorState(): PromptEditorState {
  return {editing: false, selectedTemplateID: "", editContent: "", generation: 0};
}

export function beginPromptEdit(target: PromptEditorState, templateID: string, content: string): void {
  target.generation += 1;
  target.editing = true;
  target.selectedTemplateID = templateID;
  target.editContent = content;
}

export function updatePromptDraft(target: PromptEditorState, content: string): void {
  target.editContent = content;
  target.generation += 1;
}

export function cancelPromptEdit(target: PromptEditorState): void {
  target.generation += 1;
  target.editing = false;
  target.editContent = "";
}

export function beginPromptSave(target: PromptEditorState): PromptSaveToken {
  return {generation: target.generation, templateID: target.selectedTemplateID};
}

export function finishPromptSave(target: PromptEditorState, token: PromptSaveToken): boolean {
  if (!target.editing || target.generation !== token.generation || target.selectedTemplateID !== token.templateID) {
    return false;
  }
  target.generation += 1;
  target.editing = false;
  target.editContent = "";
  return true;
}

function escapeHTML(value: unknown): string {
  return String(value ?? "")
    .replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;").replaceAll("'", "&#039;");
}

function charCount(value: string): string {
  return Array.from(value).length.toLocaleString();
}

function scenarioOption(id: string): PromptScenarioOption | undefined {
  return PROMPT_SCENARIOS.find(item => item.id === id);
}

function scenarioSources(id: string | undefined): PromptScenarioSource[] {
  return scenarioOption(id || "")?.messageSources || [];
}

// A source only has meaning inside its scenario family: Owner/宠物 belong to Web
// Tasks, while group scenarios use the production channel member/bot shapes.
export function resolvePreviewSource(scenarioID: string | undefined, requested: string): string {
  const sources = scenarioSources(scenarioID);
  if (sources.some(item => item.id === requested)) return requested;
  return sources[0]?.id || "owner";
}

function activeTemplate(view: PromptAdminView): PromptTemplate | undefined {
  return view.templates.find(item => item.id === view.selectedTemplateID) || view.templates[0];
}

export async function loadPromptCatalog(): Promise<void> {
  const response = await api.promptTemplates();
  state.templates = response.templates || [];
  state.selectedTemplateID ||= state.templates[0]?.id || "";
  if (!state.templates.some(item => item.id === state.selectedTemplateID)) {
    state.selectedTemplateID = state.templates[0]?.id || "";
  }
  state.loaded = true;
}

function templateList(view: PromptAdminView, selected?: PromptTemplate): string {
  return view.templates.map(item => `<button type="button" data-prompt-template="${escapeHTML(item.id)}" class="prompt-template-item ${item.id === selected?.id ? "active" : ""}">
    <span><strong>${escapeHTML(item.name)}</strong><small>${escapeHTML(item.layer)} · v${item.version}</small></span>
    <span class="prompt-source ${escapeHTML(item.source)}">${escapeHTML(item.source)}</span>
    ${item.required ? '<b title="必需模板">Required</b>' : ""}
  </button>`).join("");
}

function editorContent(view: PromptAdminView, selected: PromptTemplate): string {
  return view.editing ? view.editContent : selected.content;
}

function editorActions(view: PromptAdminView, selected: PromptTemplate): string {
  if (!selected.editable) return '<span class="managed-template">内置只读模板</span>';
  if (!view.editing) return '<button type="button" id="edit-prompt-template" data-prompt-edit>编辑</button>';
  return `<button type="button" id="reset-prompt-template" data-prompt-reset ${selected.source === "builtin" ? "disabled" : ""}>恢复内置</button>
    <button type="button" id="cancel-prompt-template" data-prompt-cancel>取消</button>
    <button type="button" id="save-prompt-template" class="primary" data-prompt-save>保存</button>`;
}

function switchDialog(view: PromptAdminView): string {
  if (!view.switchTargetID) return "";
  const target = view.templates.find(item => item.id === view.switchTargetID);
  return `<dialog class="prompt-switch-dialog" data-prompt-switch-dialog>
    <div class="prompt-switch-body">
      <div class="dialog-head"><div><h2>切换模板</h2><p>当前模板仍在编辑中。切换到「${escapeHTML(target?.name || view.switchTargetID)}」前要处理未保存的修改。</p></div></div>
      <div class="dialog-actions">
        <button type="button" data-prompt-switch-cancel>取消切换</button>
        <button type="button" data-prompt-switch-discard>放弃修改并切换</button>
        <button type="button" class="primary" data-prompt-switch-save>保存并切换</button>
      </div>
    </div>
  </dialog>`;
}

function previewResultBody(view: PromptAdminView): string {
  if (view.previewLoading) return '<div class="loading">正在生成场景预览...</div>';
  if (view.previewError) return `<p class="form-error">${escapeHTML(view.previewError)}</p>`;
  if (!view.preview) return '<div class="empty">选择场景后生成完整 Prompt Snapshot。</div>';
  const preview = view.preview;
  const sourceLabel = preview.message_source_name || preview.message_source;
  return `<header class="prompt-scenario-result-head">
      <div><strong>${escapeHTML(preview.scenario_name)}</strong><small>${escapeHTML(preview.scenario)} · ${escapeHTML(sourceLabel)}</small></div>
      <span><b class="prompt-scenario-badge">DEMO</b> ${preview.prompt_chars.toLocaleString()} chars</span>
    </header>
    <pre class="prompt-scenario-output" data-prompt-scenario-output>${escapeHTML(preview.prompt)}</pre>`;
}

function sourceOptions(scenario: PromptScenarioOption, selected: string): string {
  return scenario.messageSources
    .map(item => `<option value="${escapeHTML(item.id)}" ${item.id === selected ? "selected" : ""}>${escapeHTML(item.name)}</option>`)
    .join("");
}

function scenarioDialog(view: PromptAdminView): string {
  const selected = scenarioOption(view.previewScenario) || PROMPT_SCENARIOS[0];
  const options = PROMPT_SCENARIOS.map(item => `<option value="${escapeHTML(item.id)}" ${item.id === selected.id ? "selected" : ""}>${escapeHTML(item.name)}</option>`).join("");
  const sources = selected.messageSources;
  const activeSource = resolvePreviewSource(selected.id, view.previewSource);
  const canSwitchSource = sources.length > 1;
  const message = view.previewMessage || DEFAULT_PREVIEW_MESSAGE;
  return `<dialog class="prompt-scenario-dialog" data-prompt-scenario-dialog aria-labelledby="prompt-scenario-title">
    <header class="dialog-head">
      <div><h2 id="prompt-scenario-title">场景预览</h2><p>用真实 Prompt 构建器和合成 demo 数据生成完整 Snapshot，只读，不写数据库。</p></div>
      <button type="button" class="icon-button" data-prompt-scenario-close aria-label="关闭场景预览">×</button>
    </header>
    <div class="dialog-body prompt-scenario-body">
      <div class="prompt-scenario-controls">
        <label>场景
          <select id="prompt-scenario-select" data-prompt-scenario-select>${options}</select>
        </label>
        <label data-prompt-scenario-source-field ${canSwitchSource ? "" : "hidden"}>消息来源
          <select id="prompt-scenario-source" data-prompt-scenario-source ${canSwitchSource ? "" : "disabled"}>
            ${sourceOptions(selected, activeSource)}
          </select>
        </label>
        <label class="prompt-scenario-message">测试消息
          <textarea id="prompt-scenario-message" data-prompt-scenario-message rows="2">${escapeHTML(message)}</textarea>
        </label>
        <button type="button" data-prompt-scenario-refresh>刷新预览</button>
      </div>
      <p class="prompt-scenario-note">预览只使用已保存模板，不合并未保存草稿；不会创建 Turn/Task、发送渠道消息或调用模型。</p>
      <section class="prompt-scenario-result" data-prompt-scenario-result>${previewResultBody(view)}</section>
    </div>
  </dialog>`;
}

export function renderPromptAdmin(fixture?: PromptAdminView): string {
  const view = fixture || state;
  if (!view.loaded) return '<section class="page"><div class="loading">正在加载提示词模板...</div></section>';
  const selected = activeTemplate(view);
  if (!selected) return '<section class="page"><div class="empty">暂无提示词模板</div></section>';
  const content = editorContent(view, selected);
  const previewHint = view.editing ? '<span class="prompt-preview-hint" data-prompt-preview-hint>先保存或取消编辑再预览</span>' : "";
  return `<section class="page prompt-page">
    <header class="page-head"><div><h1>提示词</h1><p>按旧模板 ID 查看和维护当前 AHA2 使用的全部提示词模板。</p></div>
      <div class="actions">${previewHint}<button type="button" id="preview-prompt-scenario" data-prompt-preview-open ${view.editing ? "disabled" : ""} title="${view.editing ? "先保存或取消编辑再预览" : "预览完整场景 Prompt Snapshot"}">预览</button></div>
    </header>
    <div class="prompt-template-layout">
      <aside class="prompt-template-list">${templateList(view, selected)}</aside>
      <section class="prompt-editor">
        <header><div><h2>${escapeHTML(selected.name)}</h2><p>${escapeHTML(selected.description)}</p></div><code>${escapeHTML(selected.id)}</code></header>
        <div class="prompt-editor-meta"><span>${escapeHTML(selected.layer)}</span><span>${escapeHTML(selected.source)}</span><span>v${selected.version}</span>${selected.required ? "<span>required</span>" : ""}${view.editing ? "<span>编辑中</span>" : "<span>只读</span>"}</div>
        <textarea data-prompt-template-content data-ui-key="prompt-template-content:${escapeHTML(selected.id)}" ${!view.editing || !selected.editable ? "readonly" : ""}>${escapeHTML(content)}</textarea>
        <footer>
          <span data-prompt-char-count>${charCount(content)} chars</span>
          ${editorActions(view, selected)}
        </footer>
      </section>
    </div>
    ${switchDialog(view)}
    ${scenarioDialog(view)}
  </section>`;
}

function syncSourceField(dialog: HTMLDialogElement): void {
  const scenario = scenarioOption(dialog.querySelector<HTMLSelectElement>("[data-prompt-scenario-select]")?.value || "");
  const field = dialog.querySelector<HTMLElement>("[data-prompt-scenario-source-field]");
  const select = dialog.querySelector<HTMLSelectElement>("[data-prompt-scenario-source]");
  const sources = scenario?.messageSources || [];
  const canSwitch = sources.length > 1;
  if (field) field.hidden = !canSwitch;
  if (select) {
    const activeSource = resolvePreviewSource(scenario?.id, select.value);
    select.innerHTML = scenario ? sourceOptions(scenario, activeSource) : "";
    select.value = activeSource;
    select.disabled = !canSwitch;
  }
}

async function loadScenarioPreview(dialog: HTMLDialogElement): Promise<void> {
  const scenario = dialog.querySelector<HTMLSelectElement>("[data-prompt-scenario-select]")?.value || PROMPT_SCENARIOS[0].id;
  const message = dialog.querySelector<HTMLTextAreaElement>("[data-prompt-scenario-message]")?.value ?? DEFAULT_PREVIEW_MESSAGE;
  const sourceSelect = dialog.querySelector<HTMLSelectElement>("[data-prompt-scenario-source]");
  const scenarioMeta = scenarioOption(scenario);
  const source = resolvePreviewSource(scenarioMeta?.id, sourceSelect?.value || "");
  state.previewScenario = scenario;
  state.previewMessage = message;
  state.previewSource = source;
  const result = dialog.querySelector<HTMLElement>("[data-prompt-scenario-result]");
  const requestID = ++previewRequestID;
  state.preview = null;
  state.previewError = "";
  state.previewLoading = true;
  if (result) result.innerHTML = '<div class="loading">正在生成场景预览...</div>';
  try {
    const response = await api.promptScenarioPreview(scenario, message, source);
    if (requestID !== previewRequestID || !dialog.open) return;
    state.preview = response.preview;
    state.previewError = "";
    state.previewLoading = false;
    if (result) result.innerHTML = previewResultBody(state);
  } catch (error) {
    if (requestID !== previewRequestID || !dialog.open) return;
    state.preview = null;
    state.previewLoading = false;
    state.previewError = `预览失败：${error instanceof Error ? error.message : String(error)}`;
    if (result) result.innerHTML = previewResultBody(state);
  }
}

function blurEditor(): void {
  const active = document.activeElement;
  if (active instanceof HTMLElement && active.matches("[data-prompt-template-content]")) active.blur();
}

// The shared render captures and restores interactive controls by data-ui-key.
// A dirty editor would otherwise replay its draft into the freshly rendered
// read-only textarea after Cancel/Reset, so mark it pristine first.
function markEditorPristine(): void {
  const textarea = document.querySelector<HTMLTextAreaElement>("[data-prompt-template-content]");
  if (textarea) textarea.value = textarea.defaultValue;
}

interface PromptSaveResult {
  ok: boolean;
  // current is false when the user cancelled, switched templates, or typed
  // further while this save was in flight. The response must not touch that
  // newer editor session.
  current: boolean;
}

async function saveCurrent(
  notify: (kind: "notice" | "error", message: string) => void,
  guard?: () => boolean,
): Promise<PromptSaveResult> {
  const token = beginPromptSave(state);
  const content = document.querySelector<HTMLTextAreaElement>("[data-prompt-template-content]")?.value ?? state.editContent;
  try {
    await api.updatePromptTemplate(token.templateID, content);
    await loadPromptCatalog();
    // A switch intent may have been cancelled while the PUT was in flight. The
    // save may still have persisted, but it must not end the current editor.
    const current = (guard?.() ?? true) && finishPromptSave(state, token);
    notify("notice", "提示词模板已保存");
    return {ok: true, current};
  } catch (error) {
    notify("error", error instanceof Error ? error.message : String(error));
    return {ok: false, current: false};
  }
}

export function bindPromptAdmin(
  render: () => void,
  notify: (kind: "notice" | "error", message: string) => void,
): void {
  document.querySelectorAll<HTMLElement>("[data-prompt-template]").forEach(button => button.addEventListener("click", () => {
    const id = button.dataset.promptTemplate || "";
    if (!id || id === state.selectedTemplateID) return;
    if (state.editing) {
      // Never drop edits silently: offer save / discard / cancel.
      blurEditor();
      switchIntent += 1;
      state.switchTargetID = id;
      render();
      return;
    }
    blurEditor();
    state.selectedTemplateID = id;
    render();
  }));

  document.querySelector("#edit-prompt-template")?.addEventListener("click", () => {
    const selected = activeTemplate(state);
    if (!selected?.editable) return;
    blurEditor();
    beginPromptEdit(state, selected.id, selected.content);
    render();
  });

  document.querySelector<HTMLTextAreaElement>("[data-prompt-template-content]")?.addEventListener("input", event => {
    if (!state.editing) return;
    updatePromptDraft(state, (event.currentTarget as HTMLTextAreaElement).value);
    const count = document.querySelector<HTMLElement>("[data-prompt-char-count]");
    if (count) count.textContent = `${charCount(state.editContent)} chars`;
  });

  document.querySelector("#save-prompt-template")?.addEventListener("click", async () => {
    const result = await saveCurrent(notify);
    if (result.current) {
      blurEditor();
      render();
    } else if (!state.editing) {
      // A late response for an already-cancelled edit may still refresh the
      // saved content, but it must never disturb a newer edit session.
      render();
    }
  });

  document.querySelector("#cancel-prompt-template")?.addEventListener("click", () => {
    blurEditor();
    markEditorPristine();
    cancelPromptEdit(state);
    render();
  });

  document.querySelector("#reset-prompt-template")?.addEventListener("click", async () => {
    if (!window.confirm("恢复该模板的内置内容？")) return;
    const token = beginPromptSave(state);
    try {
      await api.resetPromptTemplate(token.templateID);
      await loadPromptCatalog();
      const current = finishPromptSave(state, token);
      notify("notice", "已恢复内置模板");
      if (current) {
        blurEditor();
        markEditorPristine();
        render();
      } else if (!state.editing) {
        render();
      }
    } catch (error) {
      notify("error", error instanceof Error ? error.message : String(error));
      if (!state.editing) render();
    }
  });

  document.querySelector("[data-prompt-switch-save]")?.addEventListener("click", async () => {
    const target = state.switchTargetID;
    if (!target) return;
    const intent = switchIntent;
    const result = await saveCurrent(notify, () => switchIntent === intent && state.switchTargetID === target);
    if (!result.ok || !result.current) return;
    switchIntent += 1;
    blurEditor();
    state.switchTargetID = "";
    document.querySelector<HTMLDialogElement>("[data-prompt-switch-dialog]")?.close();
    state.selectedTemplateID = target;
    render();
  });
  document.querySelector("[data-prompt-switch-discard]")?.addEventListener("click", () => {
    const target = state.switchTargetID;
    if (!target) return;
    blurEditor();
    switchIntent += 1;
    state.switchTargetID = "";
    document.querySelector<HTMLDialogElement>("[data-prompt-switch-dialog]")?.close();
    cancelPromptEdit(state);
    state.selectedTemplateID = target;
    render();
  });
  document.querySelector("[data-prompt-switch-cancel]")?.addEventListener("click", () => {
    blurEditor();
    switchIntent += 1;
    state.switchTargetID = "";
    document.querySelector<HTMLDialogElement>("[data-prompt-switch-dialog]")?.close();
    render();
  });

  // switchDialog is created by render while editing. Use the modal top layer so
  // it is centered and cannot be clipped by the prompt page's overflow.
  const switchDialog = document.querySelector<HTMLDialogElement>("[data-prompt-switch-dialog]");
  if (switchDialog) {
    if (!switchDialog.open) switchDialog.showModal();
    switchDialog.addEventListener("close", () => {
      // Esc/backdrop close without choosing: treat it as cancel switch.
      if (!state.switchTargetID) return;
      blurEditor();
      switchIntent += 1;
      state.switchTargetID = "";
      render();
    });
  }

  const previewDialog = document.querySelector<HTMLDialogElement>("[data-prompt-scenario-dialog]");
  document.querySelector("[data-prompt-preview-open]")?.addEventListener("click", () => {
    if (state.editing) {
      notify("error", "先保存或取消编辑再预览");
      return;
    }
    if (!previewDialog) return;
    state.previewError = "";
    state.previewLoading = true;
    syncSourceField(previewDialog);
    if (!previewDialog.open) previewDialog.showModal();
    void loadScenarioPreview(previewDialog);
  });
  document.querySelector("[data-prompt-scenario-close]")?.addEventListener("click", () => previewDialog?.close());
  previewDialog?.querySelector("[data-prompt-scenario-select]")?.addEventListener("change", () => {
    syncSourceField(previewDialog);
    void loadScenarioPreview(previewDialog);
  });
  previewDialog?.querySelector("[data-prompt-scenario-source]")?.addEventListener("change", () => {
    void loadScenarioPreview(previewDialog);
  });
  previewDialog?.querySelector("[data-prompt-scenario-refresh]")?.addEventListener("click", () => {
    void loadScenarioPreview(previewDialog);
  });
}
