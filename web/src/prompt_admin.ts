import {api} from "./api.js";
import type {PromptTemplate} from "./types.js";

const state = {
  loaded: false,
  templates: [] as PromptTemplate[],
  selectedTemplateID: "",
};

function escapeHTML(value: unknown): string {
  return String(value ?? "")
    .replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;").replaceAll("'", "&#039;");
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

function templateList(selected?: PromptTemplate): string {
  return state.templates.map(item => `<button type="button" data-prompt-template="${escapeHTML(item.id)}" class="prompt-template-item ${item.id === selected?.id ? "active" : ""}">
    <span><strong>${escapeHTML(item.name)}</strong><small>${escapeHTML(item.layer)} · v${item.version}</small></span>
    <span class="prompt-source ${escapeHTML(item.source)}">${escapeHTML(item.source)}</span>
    ${item.required ? '<b title="必需模板">Required</b>' : ""}
  </button>`).join("");
}

export function renderPromptAdmin(): string {
  if (!state.loaded) return '<section class="page"><div class="loading">正在加载提示词模板...</div></section>';
  const selected = state.templates.find(item => item.id === state.selectedTemplateID) || state.templates[0];
  if (!selected) return '<section class="page"><div class="empty">暂无提示词模板</div></section>';
  return `<section class="page prompt-page">
    <header class="page-head"><div><h1>提示词</h1><p>查看和维护当前 AHA2 使用的全部提示词模板。</p></div></header>
    <div class="prompt-template-layout">
      <aside class="prompt-template-list">${templateList(selected)}</aside>
      <section class="prompt-editor">
        <header><div><h2>${escapeHTML(selected.name)}</h2><p>${escapeHTML(selected.description)}</p></div><code>${escapeHTML(selected.id)}</code></header>
        <div class="prompt-editor-meta"><span>${escapeHTML(selected.layer)}</span><span>${escapeHTML(selected.source)}</span><span>v${selected.version}</span>${selected.required ? "<span>required</span>" : ""}</div>
        <textarea id="prompt-template-content" ${selected.editable ? "" : "readonly"}>${escapeHTML(selected.content)}</textarea>
        <footer>
          <span>${Array.from(selected.content).length.toLocaleString()} chars</span>
          ${selected.editable ? `<button type="button" id="reset-prompt-template" ${selected.source === "builtin" ? "disabled" : ""}>恢复内置</button><button type="button" id="save-prompt-template" class="primary">保存模板</button>` : '<span class="managed-template">协议由代码管理</span>'}
        </footer>
      </section>
    </div>
  </section>`;
}

export function bindPromptAdmin(
  render: () => void,
  notify: (kind: "notice" | "error", message: string) => void,
): void {
  document.querySelectorAll<HTMLElement>("[data-prompt-template]").forEach(button => button.addEventListener("click", () => {
    state.selectedTemplateID = button.dataset.promptTemplate || "";
    render();
  }));
  document.querySelector("#save-prompt-template")?.addEventListener("click", async () => {
    const content = document.querySelector<HTMLTextAreaElement>("#prompt-template-content")?.value || "";
    try {
      await api.updatePromptTemplate(state.selectedTemplateID, content);
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
      await loadPromptCatalog();
      notify("notice", "已恢复内置模板");
    } catch (error) {
      notify("error", error instanceof Error ? error.message : String(error));
    }
    render();
  });
}
