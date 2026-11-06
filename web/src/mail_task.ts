import {api} from "./api.js";
import {icon} from "./icons.js";
import {formatMailBytes, mailCanCancel, mailCanDecide, mailCanRetry, mailCompactRowHtml, openMailPreviewDialog} from "./mail_cards.js";
import type {MailPreviewData} from "./mail_cards.js";
import type {MailAccount, MailAttachment, MailMessageCard, MailTemplate, TaskDetail, TaskMailAccount, TaskMailOptions} from "./types.js";

type Notice = (type: "error" | "notice", message: string) => void;

export interface TaskMailActions {
  reload: () => Promise<void>;
  notify: Notice;
}

function escapeHTML(value: unknown): string {
  return String(value ?? "")
    .replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;").replaceAll("'", "&#039;");
}

// These values mirror internal/mail policy for the visible hint and the
// pre-submit refusal only. The server remains the final authority.
export const MAIL_ATTACHMENT_MAX_COUNT = 10;
export const MAIL_ATTACHMENT_MAX_BYTES = 25 * 1024 * 1024;
export const MAIL_DIRECT_ATTACHMENT_THRESHOLD_BYTES = 20 * 1024 * 1024;
export const MAIL_ATTACHMENT_LIMIT_HINT = "最多 10 个、单文件 ≤25MiB、合计 >20MiB 转受控链接、可执行/脚本附件不支持";

const MAIL_DISALLOWED_EXTENSIONS = [".exe", ".bat", ".cmd", ".ps1", ".js", ".vbs", ".scr", ".msi"];

export function mailAttachmentUnavailableReason(name: string): string {
  const normalized = String(name ?? "").trim().toLowerCase();
  return MAIL_DISALLOWED_EXTENSIONS.some(extension => normalized.endsWith(extension))
    ? "可执行或脚本附件不支持"
    : "";
}

export interface MailArtifactSource {
  label: string;
  attachment_id?: string;
}

export interface MailAttachmentSource {
  id: string;
  name: string;
}

// The server checks the underlying Task attachment name, not the artifact
// label. Use the same source name for the client-side warning and validation.
export function mailArtifactSourceName(artifact: MailArtifactSource, attachments: MailAttachmentSource[]): string {
  if (artifact.attachment_id) {
    const source = attachments.find(item => item.id === artifact.attachment_id);
    if (source?.name) return source.name;
  }
  return artifact.label;
}

export interface MailAttachmentCandidate {
  name: string;
  size: number;
}

export function mailAttachmentSelectionError(items: MailAttachmentCandidate[]): string {
  if (items.length > MAIL_ATTACHMENT_MAX_COUNT) return `每封邮件最多 ${MAIL_ATTACHMENT_MAX_COUNT} 个附件`;
  const blocked = items.find(item => mailAttachmentUnavailableReason(item.name));
  if (blocked) return `附件“${blocked.name}”为可执行或脚本文件，不支持发送`;
  const oversized = items.find(item => item.size > MAIL_ATTACHMENT_MAX_BYTES);
  if (oversized) return `附件“${oversized.name}”超过 25MiB`;
  return "";
}

function scopeLabel(scope: string): string {
  return {task: "Task 绑定", workspace: "Workspace 继承", project: "Project 继承"}[scope] || scope;
}

function usableAccount(account: TaskMailAccount): boolean {
  return account.status !== "disabled" && account.credential_configured;
}

export function renderTaskMailPanel(
  detail: TaskDetail,
  options: TaskMailOptions | null,
  allAccounts: MailAccount[],
  loading: boolean,
  error = "",
): string {
  const terminal = ["completed", "failed", "cancelled", "archived"].includes(detail.task.status);
  const accounts = options?.accounts || [];
  const accountsHTML = accounts.map(account => {
    const templateName = account.default_template_id
      ? (options?.templates || []).find(template => template.id === account.default_template_id)?.name || account.default_template_id
      : "";
    const identity = account.display_name || account.from_address;
    return `<article class="task-mail-binding" data-task-mail-binding="${escapeHTML(account.binding_id)}">
    <div><strong title="${escapeHTML(identity)}">${escapeHTML(identity)}</strong><small title="${escapeHTML(account.from_address)}">${escapeHTML(account.from_address)} · ${escapeHTML(scopeLabel(account.binding_scope))}${templateName ? ` · 默认模板 ${escapeHTML(templateName)}` : ""}</small><small>审批模式：每封审批 · 白名单：${escapeHTML((account.allowed_recipients || []).join(", ") || "不限")}</small>${!usableAccount(account) ? `<small class="mail-binding-warning">${account.status === "disabled" ? "账号已停用" : "未配置授权码"}，不能发送</small>` : ""}</div>
    ${account.binding_scope === "task" ? `<button type="button" class="icon-button danger" data-task-mail-unbind="${escapeHTML(account.binding_id)}" title="解除 Task 绑定" aria-label="解除 Task 绑定">${icon("close")}</button>` : ""}
  </article>`;
  }).join("");
  const taskBinding = accounts.find(account => account.binding_scope === "task");
  const accountOptions = allAccounts.filter(account => account.status !== "deleted").map(account =>
    `<option value="${escapeHTML(account.id)}" ${taskBinding?.account_id === account.id ? "selected" : ""}>${escapeHTML(account.name || account.address)} · ${escapeHTML(account.address)}${account.status === "disabled" ? "（已停用）" : ""}</option>`).join("");
  const templateOptions = (options?.templates || []).map(template =>
    `<option value="${escapeHTML(template.id)}" ${taskBinding?.default_template_id === template.id ? "selected" : ""}>${escapeHTML(template.name)} · v${Number(template.current_version)}</option>`).join("");
  // Every pending message stays actionable: the badge counts all pending
  // approvals, so the panel must never hide one behind the recent-five cut.
  const messages = options?.messages || [];
  const pending = messages.filter(card => card.message.status === "pending_approval");
  const recent = messages.filter(card => card.message.status !== "pending_approval").slice(0, 5);
  // Pending and recent share one compact row. The pending section must keep
  // review/approve/reject visible, so the row renders state actions directly
  // and bindMailCardActions binds by data-mail-action as before.
  const pendingHTML = pending.map(card => mailCompactRowHtml(card));
  const recentHTML = recent.map(card => mailCompactRowHtml(card));
  return `<section class="task-mail-panel">
    <div class="task-mail-panel-head"><div><strong>邮件绑定</strong><small>审批模式：每封审批（一期固定）</small></div><button type="button" id="task-mail-compose" class="primary" ${accounts.some(usableAccount) && !terminal ? "" : "disabled"}>${icon("mail")}发送邮件</button></div>
    ${terminal ? `<p class="field-help">Task 已结束：不能新建邮件，但已有待审批邮件仍可由 Owner 处理，队列中的邮件继续投递。</p>` : ""}
    ${loading ? `<div class="mail-loading">${icon("spinner", true)}正在加载邮件绑定…</div>` : error ? `<div class="empty-state"><strong>加载失败</strong><span>${escapeHTML(error)}</span></div>` : `<div class="task-mail-bindings">${accountsHTML || `<div class="empty-state compact"><strong>当前 Task 没有可用邮箱账号</strong><span>可在下方创建 Task 绑定，或到「资源 → 邮件」维护账号。</span></div>`}</div>`}
    ${allAccounts.length ? `<form id="task-mail-binding-form" class="task-mail-binding-form">
      <label>账号<select name="account_id" required>${accountOptions}</select></label>
      <label>默认模板<select name="default_template_id"><option value="">不设置</option>${templateOptions}</select></label>
      <button type="submit">${taskBinding ? "更新 Task 绑定" : "添加 Task 绑定"}</button>
    </form>` : `<p class="field-help">还没有系统邮箱账号。请先到「资源 → 邮件 → 账号」添加账号。</p>`}
    ${pending.length ? `<div class="task-mail-recent task-mail-pending"><strong>待审批（${pending.length}）</strong>${pendingHTML.join("")}</div>` : ""}
    <div class="task-mail-recent"><strong>最近邮件</strong>${recentHTML.length ? recentHTML.join("") : `<p class="field-help">${pending.length ? "没有其他最近邮件。" : "当前 Task 还没有邮件记录。"}</p>`}</div>
  </section>`;
}

function callerVariablesOf(template: MailTemplate): string[] {
  const values = new Set<string>();
  const text = `${template.subject_template || ""}\n${template.body_text_template || ""}\n${template.body_html_template || ""}`;
  for (const match of text.matchAll(/\{\{\s*([a-zA-Z0-9_.-]+)\s*\}\}/g)) {
    const name = match[1];
    if (/^(build|test|release|extra)\./.test(name)) values.add(name);
  }
  for (const name of template.required_variables || []) {
    if (/^(build|test|release|extra)\./.test(name)) values.add(name);
  }
  return [...values].sort();
}

function variableInputs(template: MailTemplate | undefined): string {
  if (!template) return "";
  const names = callerVariablesOf(template);
  if (!names.length) return `<p class="field-help">该模板不需要调用方变量。</p>`;
  return `<div class="mail-compose-variables">${names.map(name => `<label>${escapeHTML(name)}<input data-mail-variable-input="${escapeHTML(name)}" value=""></label>`).join("")}</div>`;
}

function templateDefaults(templateID: string, templates: MailTemplate[]): MailTemplate | undefined {
  return templates.find(template => template.id === templateID);
}

function csvValues(value: unknown): string[] {
  return String(value || "").split(",").map(item => item.trim()).filter(Boolean);
}

// The compose default is exactly the selected account's Task/Workspace/Project
// binding template. A binding with no template must select "不使用模板", never
// fall through to the first template in the list.
export function mailComposeTemplateSelection(account: TaskMailAccount | undefined, templates: MailTemplate[]): string {
  const requested = account?.default_template_id || "";
  return requested && templates.some(template => template.id === requested) ? requested : "";
}

// Checkbox state is the single source for the submitted attachment ids. It is
// exported so the picker, chips and payload path can be tested without a DOM.
export function mailComposeAttachments(
  attachments: Array<{value: string; checked: boolean}>,
  artifacts: Array<{value: string; checked: boolean}>,
): {attachment_ids: string[]; build_artifact_ids: string[]} {
  return {
    attachment_ids: attachments.filter(item => item.checked).map(item => item.value),
    build_artifact_ids: artifacts.filter(item => item.checked).map(item => item.value),
  };
}

// Builds the shared WYSIWYG preview model from the server render result and
// the currently selected account. Used by both the template and direct send
// paths so the Owner always sees the real HTML before submitting.
export function taskMailPreviewData(
  preview: Record<string, any>,
  account?: Pick<TaskMailAccount, "display_name" | "from_address">,
  title = "发送预览",
  width: 600 | 375 = 600,
): MailPreviewData {
  return {
    title,
    subject: String(preview.subject || ""),
    fromName: account?.display_name || "",
    fromAddress: account?.from_address || "",
    to: Array.isArray(preview.to) ? preview.to as string[] : [],
    cc: Array.isArray(preview.cc) ? preview.cc as string[] : [],
    bodyText: String(preview.body_text || ""),
    bodyHtml: String(preview.body_html || ""),
    attachments: Array.isArray(preview.attachments) ? preview.attachments as MailAttachment[] : [],
    warnings: Array.isArray(preview.warnings) ? preview.warnings as string[] : [],
    width,
  };
}

// Direct mail keeps template_id empty and supplies subject + body_html. The
// server derives body_text from HTML when it is empty. Template mail keeps the
// existing payload shape; subject/body inputs are disabled and absent.
export function mailComposePayload(
  values: Record<string, unknown>,
  taskID: string,
  variableValues: Record<string, string>,
  attachmentIDs: string[],
  buildArtifactIDs: string[],
): Record<string, any> {
  const payload: Record<string, any> = {
    task_id: taskID,
    account_id: String(values.account_id || ""),
    template_id: String(values.template_id || "").trim(),
    to: csvValues(values.to),
    cc: csvValues(values.cc),
    bcc: csvValues(values.bcc),
    variables: variableValues,
    attachment_ids: attachmentIDs,
    build_artifact_ids: buildArtifactIDs,
  };
  if (!payload.template_id) {
    payload.subject = String(values.subject || "").trim();
    payload.body_html = String(values.body_html || "");
    payload.body_text = "";
  }
  return payload;
}

export async function openTaskMailCompose(
  detail: TaskDetail,
  options: TaskMailOptions,
  actions: TaskMailActions,
): Promise<void> {
  const accounts = options.accounts.filter(usableAccount);
  const templates = options.templates || [];
  if (!accounts.length) {
    actions.notify("error", "当前 Task 没有可用的邮箱账号，请先在资源页绑定账号");
    return;
  }
  document.querySelector<HTMLDialogElement>("#task-mail-compose-dialog")?.remove();
  const accountOptions = accounts.map(account => `<option value="${escapeHTML(account.account_id)}" data-default-template="${escapeHTML(account.default_template_id || "")}" data-binding-scope="${escapeHTML(account.binding_scope)}">${escapeHTML(account.display_name || account.from_address)} · ${escapeHTML(account.from_address)}</option>`).join("");
  // "不使用模板" is first so an unset binding naturally falls back to it. A
  // valid binding default is still selected explicitly below.
  const templateOptions = `<option value="">不使用模板</option>` + templates.map(template => `<option value="${escapeHTML(template.id)}">${escapeHTML(template.name)} · v${Number(template.current_version)}</option>`).join("");
  // Executable and script names are disabled in place with the reason visible
  // instead of failing later in preview/send. The checkbox stays unchecked and
  // the server still validates every submitted attachment.
  const attachmentOptions = (options.attachments || []).map(attachment => {
    const unavailable = mailAttachmentUnavailableReason(attachment.name);
    return `<label class="mail-attachment-option${unavailable ? " unavailable" : ""}" title="${escapeHTML(unavailable ? `${attachment.name} · ${unavailable}` : attachment.name)}"><input type="checkbox" name="attachment_ids" value="${escapeHTML(attachment.id)}" data-mail-name="${escapeHTML(attachment.name)}" data-mail-size="${Number(attachment.size) || 0}"${unavailable ? " disabled" : ""}><span class="mail-attachment-option-name">${escapeHTML(attachment.name)}</span><small>${formatMailBytes(attachment.size)}${attachment.media_type ? ` · ${escapeHTML(attachment.media_type)}` : ""}${unavailable ? ` · <span class="mail-attachment-unavailable">不支持：${escapeHTML(unavailable)}</span>` : ""}</small></label>`;
  }).join("");
  const artifactOptions = (options.build_artifacts || []).map(artifact => {
    const sourceName = mailArtifactSourceName(artifact, options.attachments || []);
    const unavailable = mailAttachmentUnavailableReason(sourceName);
    return `<label class="mail-attachment-option${unavailable ? " unavailable" : ""}" title="${escapeHTML(`${artifact.label} · SHA256 ${artifact.sha256 || ""}${unavailable ? ` · ${unavailable}` : ""}`)}"><input type="checkbox" name="build_artifact_ids" value="${escapeHTML(artifact.id)}" data-mail-name="${escapeHTML(sourceName)}" data-mail-size="${Number(artifact.size_bytes) || 0}"${unavailable ? " disabled" : ""}><span class="mail-attachment-option-name">${escapeHTML(artifact.label)}</span><small>${escapeHTML(artifact.package_name || "")}${artifact.version ? ` · ${escapeHTML(artifact.version)}` : ""} · ${formatMailBytes(artifact.size_bytes)}${unavailable ? ` · <span class="mail-attachment-unavailable">不支持：${escapeHTML(unavailable)}</span>` : ""}</small></label>`;
  }).join("");
  const dialog = document.createElement("dialog");
  dialog.id = "task-mail-compose-dialog";
  dialog.className = "task-mail-compose-dialog";
  dialog.innerHTML = `<form id="task-mail-compose-form">
    <div class="dialog-head"><h2>发送邮件</h2><button type="button" data-close class="icon-button" aria-label="关闭">${icon("close")}</button></div>
    <div class="dialog-body">
      <div class="two"><label>账号<select name="account_id" required>${accountOptions}</select></label><label>模板<select name="template_id">${templateOptions}</select></label></div>
      <p class="field-help" data-mail-template-hint></p>
      <div id="mail-compose-direct" class="mail-compose-direct" hidden>
        <label>主题<input name="subject"></label>
        <label>正文（HTML 主编辑区）<textarea name="body_html" rows="8" placeholder="<p>...</p>"></textarea></label>
        <p class="field-help">不使用模板时主题必填，正文至少一个；纯文本留空由后端从 HTML 自动生成。</p>
      </div>
      <div class="two"><label>收件人 To（逗号分隔）<input name="to" required></label><label>CC（逗号分隔）<input name="cc"></label></div>
      <label>BCC（仅 Owner，可选）<input name="bcc"></label>
      <div id="mail-compose-variables">${variableInputs(templateDefaults(mailComposeTemplateSelection(accounts[0], templates), templates))}</div>
      <div class="mail-compose-attachments" data-mail-attachments>
        <div class="mail-compose-attachment-bar">
          <div class="mail-compose-attachment-summary"><strong>附件</strong><span data-mail-selected-total aria-live="polite">已选 0 项</span></div>
          <button type="button" id="mail-compose-attachment-toggle" class="mail-compose-attachment-toggle" aria-expanded="false" aria-controls="mail-compose-attachment-picker">添加附件（已选 0）</button>
        </div>
        <p class="field-help" data-mail-attachment-limit>${escapeHTML(MAIL_ATTACHMENT_LIMIT_HINT)}</p>
        <div class="mail-compose-chips" data-mail-attachment-chips aria-live="polite"><span class="field-help">尚未选择附件。</span></div>
        <div class="mail-compose-picker" id="mail-compose-attachment-picker" data-mail-attachment-picker hidden>
          <div class="mail-compose-picker-tabs" role="tablist" aria-label="附件来源">
            <button type="button" data-mail-picker-tab="task" role="tab" aria-selected="true">当前 Task 附件（${(options.attachments || []).length}）</button>
            <button type="button" data-mail-picker-tab="build" role="tab" aria-selected="false">构建产物（${(options.build_artifacts || []).length}）</button>
          </div>
          <p class="field-help">构建产物只来自当前 Task 已登记、存储类型为 AHA Task 附件的记录（TaskMailOptions.build_artifacts）；不支持外部 URL 或本地路径。</p>
          <div class="mail-attachment-list-scroll" data-mail-picker-pane="task" role="tabpanel" aria-label="当前 Task 附件">${attachmentOptions || `<p class="field-help">没有可用附件。</p>`}</div>
          <div class="mail-attachment-list-scroll" data-mail-picker-pane="build" role="tabpanel" aria-label="构建产物" hidden>${artifactOptions || `<p class="field-help">没有登记的构建产物。</p>`}</div>
        </div>
      </div>
    </div>
    <div class="dialog-actions"><button type="button" data-close>取消</button><button type="button" id="mail-compose-preview-button">预览</button><button type="submit" class="primary">提交审批</button></div>
  </form>`;
  document.body.appendChild(dialog);
  // This local close must stay in the compose scope: without it the submit
  // success path would resolve `close` to window.close and cancel/X would do
  // nothing, leaving the dialog (and a fresh idempotency key) reusable.
  const close = () => { dialog.close(); dialog.remove(); };
  dialog.querySelectorAll("[data-close]").forEach(button => button.addEventListener("click", close));
  dialog.addEventListener("cancel", close);
  const form = dialog.querySelector<HTMLFormElement>("#task-mail-compose-form")!;
  const templateSelect = form.querySelector<HTMLSelectElement>('select[name="template_id"]')!;
  const accountSelect = form.querySelector<HTMLSelectElement>('select[name="account_id"]')!;
  const variablesBox = form.querySelector<HTMLElement>("#mail-compose-variables")!;
  const directBox = form.querySelector<HTMLElement>("#mail-compose-direct")!;
  const directFields = [...directBox.querySelectorAll<HTMLInputElement | HTMLTextAreaElement>("input, textarea")];
  const syncDirectFields = (enabled: boolean) => {
    directBox.hidden = !enabled;
    directFields.forEach(field => {
      field.disabled = !enabled;
      if (field.name === "subject" || field.name === "body_html") field.required = enabled;
    });
  };

  const picker = form.querySelector<HTMLElement>("[data-mail-attachment-picker]")!;
  const pickerToggle = form.querySelector<HTMLButtonElement>("#mail-compose-attachment-toggle")!;
  const chipsBox = form.querySelector<HTMLElement>("[data-mail-attachment-chips]")!;
  const selectedTotal = form.querySelector<HTMLElement>("[data-mail-selected-total]")!;
  const attachmentInputs = () => [...form.querySelectorAll<HTMLInputElement>('input[name="attachment_ids"]')];
  const artifactInputs = () => [...form.querySelectorAll<HTMLInputElement>('input[name="build_artifact_ids"]')];
  const optionName = (input: HTMLInputElement) =>
    (input.closest("label")?.querySelector<HTMLElement>(".mail-attachment-option-name")?.textContent || input.value).trim();
  const syncSelected = () => {
    const selected = [...attachmentInputs(), ...artifactInputs()].filter(input => input.checked);
    selectedTotal.textContent = `已选 ${selected.length} 项`;
    pickerToggle.textContent = `添加附件（已选 ${selected.length}）`;
    chipsBox.replaceChildren();
    if (!selected.length) {
      const empty = document.createElement("span");
      empty.className = "field-help";
      empty.textContent = "尚未选择附件。";
      chipsBox.append(empty);
      return;
    }
    for (const input of selected) {
      const chip = document.createElement("span");
      chip.className = "mail-attachment-chip";
      const name = document.createElement("span");
      name.className = "mail-attachment-chip-name";
      name.textContent = optionName(input);
      name.title = name.textContent;
      const remove = document.createElement("button");
      remove.type = "button";
      remove.className = "mail-attachment-chip-remove";
      remove.setAttribute("aria-label", `移除附件 ${name.textContent}`);
      remove.innerHTML = icon("close");
      remove.addEventListener("click", () => {
        input.checked = false;
        syncSelected();
      });
      chip.append(name, remove);
      chipsBox.append(chip);
    }
  };
  // Disabled rows cannot be checked through the UI, but an already-checked
  // entry must fail loudly rather than be silently dropped from the payload.
  const selectedAttachmentItems = () => [...attachmentInputs(), ...artifactInputs()]
    .filter(input => input.checked)
    .map(input => ({
      name: input.dataset.mailName || optionName(input),
      size: Number(input.dataset.mailSize || 0),
    }));
  const attachmentSelectionValid = () => {
    const error = mailAttachmentSelectionError(selectedAttachmentItems());
    if (!error) return true;
    actions.notify("error", error);
    return false;
  };
  const setPickerTab = (tab: "task" | "build") => {
    form.querySelectorAll<HTMLElement>("[data-mail-picker-tab]").forEach(button => {
      const active = button.dataset.mailPickerTab === tab;
      button.classList.toggle("active", active);
      button.setAttribute("aria-selected", String(active));
    });
    form.querySelectorAll<HTMLElement>("[data-mail-picker-pane]").forEach(pane => {
      pane.hidden = pane.dataset.mailPickerPane !== tab;
    });
  };
  form.querySelectorAll<HTMLElement>("[data-mail-picker-tab]").forEach(button => button.addEventListener("click", () => {
    setPickerTab(button.dataset.mailPickerTab === "build" ? "build" : "task");
  }));
  pickerToggle.addEventListener("click", () => {
    const opening = picker.hidden;
    picker.hidden = !opening;
    pickerToggle.setAttribute("aria-expanded", String(opening));
    if (opening) form.querySelector<HTMLButtonElement>('[data-mail-picker-tab][aria-selected="true"]')?.focus();
  });
  form.addEventListener("change", event => {
    const target = event.target;
    if (target instanceof HTMLInputElement && (target.name === "attachment_ids" || target.name === "build_artifact_ids")) {
      syncSelected();
    }
  });
  syncSelected();

  let autoTo = "";
  let autoCc = "";
  const applyTemplateDefaults = (template: MailTemplate | undefined) => {
    const to = form.querySelector<HTMLInputElement>('input[name="to"]')!;
    const cc = form.querySelector<HTMLInputElement>('input[name="cc"]')!;
    const nextTo = (template?.default_to || []).join(", ");
    const nextCc = (template?.default_cc || []).join(", ");
    // Replace only values the Owner has not edited; keep manual recipients.
    if (!to.value || to.value === autoTo) to.value = nextTo;
    if (!cc.value || cc.value === autoCc) cc.value = nextCc;
    autoTo = nextTo;
    autoCc = nextCc;
  };
  const templateHint = form.querySelector<HTMLElement>("[data-mail-template-hint]")!;
  const syncTemplate = () => {
    const template = templateDefaults(templateSelect.value, templates);
    const noTemplate = !templateSelect.value;
    // The template variables and the direct subject/body are mutually
    // exclusive. Disabling the hidden fields also keeps them out of FormData.
    syncDirectFields(noTemplate);
    variablesBox.innerHTML = noTemplate ? "" : variableInputs(template);
    applyTemplateDefaults(noTemplate ? undefined : template);
    const account = accounts.find(item => item.account_id === accountSelect.value);
    const defaultID = account?.default_template_id || "";
    const defaultAvailable = Boolean(defaultID) && templates.some(item => item.id === defaultID);
    const scope = account ? scopeLabel(account.binding_scope) : "Task/Workspace/Project 绑定";
    if (!defaultID) {
      templateHint.textContent = noTemplate
        ? "当前账号绑定未设置默认模板，默认不使用模板；可逐封选择模板。"
        : "当前账号绑定未设置默认模板，本次逐封选择了模板。";
    } else if (!defaultAvailable) {
      templateHint.textContent = `绑定的默认模板不可用（可能已归档），请逐封选择；默认来自 ${scope}。`;
    } else if (templateSelect.value === defaultID) {
      templateHint.textContent = `默认来自 ${scope}，可逐封覆盖。`;
    } else {
      templateHint.textContent = `已逐封覆盖默认模板（默认来自 ${scope}）。`;
    }
  };
  templateSelect.addEventListener("change", syncTemplate);
  accountSelect.addEventListener("change", () => {
    const account = accounts.find(item => item.account_id === accountSelect.value);
    // Switching account always switches to that account's binding default;
    // an unset binding explicitly selects "不使用模板".
    templateSelect.value = mailComposeTemplateSelection(account, templates);
    syncTemplate();
  });
  const initialAccount = accounts.find(item => item.account_id === accountSelect.value);
  templateSelect.value = mailComposeTemplateSelection(initialAccount, templates);
  syncTemplate();

  const collect = () => {
    const values = new FormData(form);
    const variables = Object.fromEntries([...form.querySelectorAll<HTMLInputElement>("[data-mail-variable-input]")].map(input => [input.dataset.mailVariableInput || "", input.value]));
    const variableValues = Object.fromEntries(Object.entries(variables).filter(([, value]) => String(value).trim() !== ""));
    const selected = mailComposeAttachments(
      attachmentInputs().map(input => ({value: input.value, checked: input.checked})),
      artifactInputs().map(input => ({value: input.value, checked: input.checked})),
    );
    return mailComposePayload(
      {
        account_id: values.get("account_id"), template_id: values.get("template_id"),
        to: values.get("to"), cc: values.get("cc"), bcc: values.get("bcc"),
        subject: values.get("subject"), body_html: values.get("body_html"),
      },
      detail.task.id,
      variableValues,
      selected.attachment_ids,
      selected.build_artifact_ids,
    );
  };
  form.querySelector("#mail-compose-preview-button")?.addEventListener("click", () => {
    if (!attachmentSelectionValid()) return;
    const payload = collect();
    const previewRequest = payload.template_id
      ? api.previewMailTemplate(payload.template_id, {
        task_id: payload.task_id, variables: payload.variables, to: payload.to, cc: payload.cc,
        attachment_ids: payload.attachment_ids, build_artifact_ids: payload.build_artifact_ids,
      })
      : api.previewMailTemplateContent({
        task_id: payload.task_id,
        subject_template: payload.subject, body_text_template: payload.body_text,
        body_html_template: payload.body_html,
        variables: payload.variables, to: payload.to, cc: payload.cc,
        attachment_ids: payload.attachment_ids, build_artifact_ids: payload.build_artifact_ids,
      });
    void previewRequest.then(result => {
      // Both template and direct paths render the real HTML through the same
      // server pipeline; show it in the shared sandboxed preview dialog.
      const account = options.accounts.find(item => item.account_id === payload.account_id);
      openMailPreviewDialog(taskMailPreviewData(result.preview as Record<string, any>, account, "发送预览", 600));
    }).catch(error => actions.notify("error", error instanceof Error ? error.message : String(error)));
  });
  form.addEventListener("submit", event => {
    event.preventDefault();
    if (!attachmentSelectionValid()) return;
    const submit = form.querySelector<HTMLButtonElement>('button[type="submit"]')!;
    submit.disabled = true;
    const payload = collect();
    const idempotencyKey = `owner-${detail.task.id}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
    void api.createMailMessage({...payload, submit: true, idempotency_key: idempotencyKey}).then(async () => {
      close();
      await actions.reload();
      actions.notify("notice", "邮件已提交，等待 Owner 审批");
    }).catch(error => actions.notify("error", error instanceof Error ? error.message : String(error))).finally(() => {
      if (submit.isConnected) submit.disabled = false;
    });
  });
  dialog.showModal();
}

export function bindTaskMailPanel(
  detail: TaskDetail,
  options: TaskMailOptions | null,
  actions: TaskMailActions,
): void {
  document.querySelector("#task-mail-compose")?.addEventListener("click", () => {
    if (!options) {
      actions.notify("error", "邮件绑定尚未加载完成");
      return;
    }
    void openTaskMailCompose(detail, options, actions);
  });
  document.querySelector<HTMLFormElement>("#task-mail-binding-form")?.addEventListener("submit", event => {
    event.preventDefault();
    const form = event.currentTarget as HTMLFormElement;
    const values = new FormData(form);
    const accountID = String(values.get("account_id") || "");
    const templateID = String(values.get("default_template_id") || "");
    const existing = (options?.accounts || []).find(account => account.binding_scope === "task" && account.account_id === accountID);
    const payload = {
      scope_type: "task", scope_id: detail.task.id, account_id: accountID,
      default_template_id: templateID, approval_mode: "always", enabled: true,
    };
    const request = existing
      ? api.updateMailBinding(existing.binding_id, {default_template_id: templateID, enabled: true, approval_mode: "always"})
      : api.createMailBinding(payload);
    void request.then(async () => {
      await actions.reload();
      actions.notify("notice", "Task 邮件绑定已保存");
    }).catch(error => actions.notify("error", error instanceof Error ? error.message : String(error)));
  });
  document.querySelectorAll<HTMLElement>("[data-task-mail-unbind]").forEach(button => button.addEventListener("click", () => {
    const bindingID = button.dataset.taskMailUnbind || "";
    if (!bindingID || !window.confirm("解除当前 Task 的邮件绑定？Workspace/Project 继承的绑定不受影响。")) return;
    void api.deleteMailBinding(bindingID).then(async () => {
      await actions.reload();
      actions.notify("notice", "Task 邮件绑定已解除");
    }).catch(error => actions.notify("error", error instanceof Error ? error.message : String(error)));
  }));
  const panel = document.querySelector<HTMLElement>("#task-tool-panel-body");
  if (panel) bindMailCardActions(panel, options, actions);
}

export function mailCardByID(options: TaskMailOptions | null, messageID: string): MailMessageCard | undefined {
  return (options?.messages || []).find(card => card.message.id === messageID);
}

// The row's primary action opens this preview. Decision callbacks exist only
// while the message is pending_approval; terminal messages must open read-only
// so the dialog footer keeps a single "关闭" button.
export function mailReviewPreviewData(card: MailMessageCard, actions: TaskMailActions): MailPreviewData {
  const message = card.message;
  const canDecide = mailCanDecide(message.status);
  const data: MailPreviewData = {
    title: canDecide ? "查看并审批" : "邮件预览",
    subject: message.subject,
    fromName: message.from_name,
    fromAddress: message.from_address,
    to: message.to,
    cc: message.cc,
    bcc: message.bcc,
    bodyText: message.body_text,
    bodyHtml: message.body_html,
    attachments: card.attachments || [],
    width: 600,
  };
  if (!canDecide) return data;
  data.onApprove = async () => {
    if (!message.approval_digest || !window.confirm("批准这封邮件并进入投递队列？")) return false;
    try {
      await api.approveMailMessage(message.id, message.approval_digest);
      await actions.reload();
      actions.notify("notice", "邮件已批准");
      return true;
    } catch (error) {
      actions.notify("error", error instanceof Error ? error.message : String(error));
      return false;
    }
  };
  data.onReject = async () => {
    const reason = window.prompt("拒绝原因（必填）");
    if (!reason || !window.confirm("拒绝这封邮件？")) return false;
    try {
      await api.rejectMailMessage(message.id, reason);
      await actions.reload();
      actions.notify("notice", "邮件已拒绝");
      return true;
    } catch (error) {
      actions.notify("error", error instanceof Error ? error.message : String(error));
      return false;
    }
  };
  return data;
}

export function bindMailCardActions(container: ParentNode, options: TaskMailOptions | null, actions: TaskMailActions): void {
  container.querySelectorAll<HTMLElement>("[data-mail-action]").forEach(button => button.addEventListener("click", () => {
    const messageID = button.dataset.mailMessage || "";
    const card = mailCardByID(options, messageID);
    if (!card) {
      actions.notify("error", "邮件状态尚未加载，请稍后重试");
      return;
    }
    const message = card.message;
    const action = button.dataset.mailAction;
    if (action === "review") {
      // Open the snapshot without closing the panel. The dialog owns only
      // presentation; the existing approve/reject APIs remain the single
      // decision path and are attached only for pending messages.
      openMailPreviewDialog(mailReviewPreviewData(card, actions));
    } else if (action === "approve") {
      if (!window.confirm("批准这封邮件并进入投递队列？")) return;
      void api.approveMailMessage(message.id, message.approval_digest || "").then(() => actions.reload()).then(() => actions.notify("notice", "邮件已批准")).catch(error => actions.notify("error", error instanceof Error ? error.message : String(error)));
    } else if (action === "reject") {
      const reason = window.prompt("拒绝原因（必填）");
      if (!reason || !window.confirm("拒绝这封邮件？")) return;
      void api.rejectMailMessage(message.id, reason).then(() => actions.reload()).then(() => actions.notify("notice", "邮件已拒绝")).catch(error => actions.notify("error", error instanceof Error ? error.message : String(error)));
    } else if (action === "retry") {
      if (!mailCanRetry(message.status) || !window.confirm("按原快照创建新的投递 generation 重试？")) return;
      void api.retryMailMessage(message.id).then(() => actions.reload()).then(() => actions.notify("notice", "已创建重试投递")).catch(error => actions.notify("error", error instanceof Error ? error.message : String(error)));
    } else if (action === "cancel") {
      if (!mailCanCancel(message.status) || !window.confirm("取消这封邮件？发送中不可取消。")) return;
      void api.cancelMailMessage(message.id).then(() => actions.reload()).then(() => actions.notify("notice", "邮件已取消")).catch(error => actions.notify("error", error instanceof Error ? error.message : String(error)));
    }
  }));
}
