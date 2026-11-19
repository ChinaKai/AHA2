import {api} from "./api.js";
import type {PromptScenarioPreview, PromptTemplate} from "./types.js";

export const DEFAULT_PREVIEW_MESSAGE = "这是一条测试消息";

export interface PromptScenarioOption {
  id: string;
  name: string;
  description: string;
  messageSourceSwitch: boolean;
}

// The preview shapes are stable product scenarios. The server validates the
// same IDs; keeping the list here avoids an extra round trip just to open the
// dialog.
export const PROMPT_SCENARIOS: PromptScenarioOption[] = [
  {
    id: "web-task-single-main",
    name: "Web 普通 Task，单 Agent，主 Agent",
    description: "Web Owner 消息，普通 Task，单 Agent 主 Agent。",
    messageSourceSwitch: true,
  },
  {
    id: "web-task-multi-main",
    name: "Web 普通 Task，多 Agent，主 Agent（自动协作）",
    description: "Web Owner 消息，多 Agent 自动协作下的主 Agent。",
    messageSourceSwitch: true,
  },
  {
    id: "web-task-multi-sub",
    name: "Web 普通 Task，多 Agent，子 Agent（无协作策略段）",
    description: "多 Agent 自动协作下的子 Agent；不包含协作策略段。",
    messageSourceSwitch: true,
  },
  {
    id: "web-pet-task-single-main",
    name: "Web 宠物 Task，单 Agent，主 Agent",
    description: "宠物 Task 的主 Agent，使用宠物身份模板。",
    messageSourceSwitch: true,
  },
  {
    id: "channel-private-unbound-assistant-main",
    name: "渠道私聊，未绑定 Task，渠道助手，单 Agent，主 Agent",
    description: "渠道私聊未绑定 Task，渠道助手身份处理消息。",
    messageSourceSwitch: false,
  },
  {
    id: "channel-private-task-single-task-agent",
    name: "渠道私聊，已绑定 Task，单 Agent，任务 Agent",
    description: "渠道私聊 task_route，单 Agent 任务 Agent。",
    messageSourceSwitch: false,
  },
  {
    id: "channel-private-task-multi-main",
    name: "渠道私聊，已绑定 Task，多 Agent，主 Agent",
    description: "渠道私聊 task_route，多 Agent 自动协作主 Agent。",
    messageSourceSwitch: false,
  },
  {
    id: "channel-group-unbound-digital-human-main",
    name: "渠道群聊，未绑定 Task，群聊电子人，单 Agent，主 Agent，群聊规则",
    description: "渠道群聊未绑定 Task，群聊电子人身份与群聊规则。",
    messageSourceSwitch: false,
  },
  {
    id: "channel-group-task-single-task-agent",
    name: "渠道群聊，已绑定 Task，单 Agent，任务 Agent，群聊规则",
    description: "渠道群聊 task_route，单 Agent 任务 Agent 与群聊规则。",
    messageSourceSwitch: false,
  },
  {
    id: "channel-group-task-multi-main",
    name: "渠道群聊，已绑定 Task，多 Agent，主 Agent，群聊规则",
    description: "渠道群聊 task_route，多 Agent 自动协作主 Agent 与群聊规则。",
    messageSourceSwitch: false,
  },
];

export interface PromptAdminView {
  loaded: boolean;
  templates: PromptTemplate[];
  selectedTemplateID: string;
  editing: boolean;
  editContent: string;
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
  switchTargetID: "",
  previewScenario: PROMPT_SCENARIOS[0].id,
  previewMessage: DEFAULT_PREVIEW_MESSAGE,
  previewSource: "owner",
  preview: null,
  previewError: "",
  previewLoading: false,
};

let previewRequestID = 0;

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
  return `<header class="prompt-scenario-result-head">
      <div><strong>${escapeHTML(preview.scenario_name)}</strong><small>${escapeHTML(preview.scenario)} · ${escapeHTML(preview.message_source)}</small></div>
      <span><b class="prompt-scenario-badge">DEMO</b> ${preview.prompt_chars.toLocaleString()} chars</span>
    </header>
    <pre class="prompt-scenario-output" data-prompt-scenario-output>${escapeHTML(preview.prompt)}</pre>`;
}

function scenarioDialog(view: PromptAdminView): string {
  const selected = scenarioOption(view.previewScenario) || PROMPT_SCENARIOS[0];
  const options = PROMPT_SCENARIOS.map(item => `<option value="${escapeHTML(item.id)}" ${item.id === selected.id ? "selected" : ""}>${escapeHTML(item.name)}</option>`).join("");
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
        <label data-prompt-scenario-source-field ${selected.messageSourceSwitch ? "" : "hidden"}>消息来源
          <select id="prompt-scenario-source" data-prompt-scenario-source>
            <option value="owner" ${view.previewSource !== "pet" ? "selected" : ""}>Owner</option>
            <option value="pet" ${view.previewSource === "pet" ? "selected" : ""}>宠物</option>
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
  const canSwitch = scenario?.messageSourceSwitch === true;
  if (field) field.hidden = !canSwitch;
  if (select) {
    select.disabled = !canSwitch;
    if (!canSwitch) select.value = "owner";
  }
}

async function loadScenarioPreview(dialog: HTMLDialogElement): Promise<void> {
  const scenario = dialog.querySelector<HTMLSelectElement>("[data-prompt-scenario-select]")?.value || PROMPT_SCENARIOS[0].id;
  const message = dialog.querySelector<HTMLTextAreaElement>("[data-prompt-scenario-message]")?.value ?? DEFAULT_PREVIEW_MESSAGE;
  const sourceSelect = dialog.querySelector<HTMLSelectElement>("[data-prompt-scenario-source]");
  const scenarioMeta = scenarioOption(scenario);
  const source = scenarioMeta?.messageSourceSwitch ? (sourceSelect?.value || "owner") : "owner";
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

async function saveCurrent(notify: (kind: "notice" | "error", message: string) => void): Promise<boolean> {
  const content = document.querySelector<HTMLTextAreaElement>("[data-prompt-template-content]")?.value ?? state.editContent;
  try {
    await api.updatePromptTemplate(state.selectedTemplateID, content);
    state.editing = false;
    state.editContent = "";
    await loadPromptCatalog();
    notify("notice", "提示词模板已保存");
    return true;
  } catch (error) {
    notify("error", error instanceof Error ? error.message : String(error));
    return false;
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
    state.editing = true;
    state.editContent = selected.content;
    render();
  });

  document.querySelector<HTMLTextAreaElement>("[data-prompt-template-content]")?.addEventListener("input", event => {
    if (!state.editing) return;
    state.editContent = (event.currentTarget as HTMLTextAreaElement).value;
    const count = document.querySelector<HTMLElement>("[data-prompt-char-count]");
    if (count) count.textContent = `${charCount(state.editContent)} chars`;
  });

  document.querySelector("#save-prompt-template")?.addEventListener("click", async () => {
    const saved = await saveCurrent(notify);
    if (saved) blurEditor();
    render();
  });

  document.querySelector("#cancel-prompt-template")?.addEventListener("click", () => {
    blurEditor();
    markEditorPristine();
    state.editing = false;
    state.editContent = "";
    render();
  });

  document.querySelector("#reset-prompt-template")?.addEventListener("click", async () => {
    if (!window.confirm("恢复该模板的内置内容？")) return;
    try {
      await api.resetPromptTemplate(state.selectedTemplateID);
      blurEditor();
      markEditorPristine();
      state.editing = false;
      state.editContent = "";
      await loadPromptCatalog();
      notify("notice", "已恢复内置模板");
    } catch (error) {
      notify("error", error instanceof Error ? error.message : String(error));
    }
    render();
  });

  document.querySelector("[data-prompt-switch-save]")?.addEventListener("click", async () => {
    const target = state.switchTargetID;
    if (!target) return;
    if (!await saveCurrent(notify)) return;
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
    state.switchTargetID = "";
    document.querySelector<HTMLDialogElement>("[data-prompt-switch-dialog]")?.close();
    state.selectedTemplateID = target;
    state.editing = false;
    state.editContent = "";
    render();
  });
  document.querySelector("[data-prompt-switch-cancel]")?.addEventListener("click", () => {
    blurEditor();
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
