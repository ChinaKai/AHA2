import {api} from "./api.js";
import {icon} from "./icons.js";
import {formatMailBytes, mailCanCancel, mailCanRetry, mailCardHtml, openMailPreviewDialog} from "./mail_cards.js";
import type {MailAccount, MailMessageCard, MailTemplate, TaskDetail, TaskMailAccount, TaskMailOptions} from "./types.js";

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
  const accountsHTML = accounts.map(account => `<article class="task-mail-binding" data-task-mail-binding="${escapeHTML(account.binding_id)}">
    <div><strong>${escapeHTML(account.display_name || account.from_address)}</strong><small>${escapeHTML(account.from_address)} · ${escapeHTML(scopeLabel(account.binding_scope))}${account.default_template_id ? ` · 默认模板 ${escapeHTML(account.default_template_id)}` : ""}</small><small>审批模式：每封审批 · 白名单：${escapeHTML((account.allowed_recipients || []).join(", ") || "不限")}</small>${!usableAccount(account) ? `<small class="mail-binding-warning">${account.status === "disabled" ? "账号已停用" : "未配置授权码"}，不能发送</small>` : ""}</div>
    ${account.binding_scope === "task" ? `<button type="button" class="icon-button danger" data-task-mail-unbind="${escapeHTML(account.binding_id)}" title="解除 Task 绑定" aria-label="解除 Task 绑定">${icon("close")}</button>` : ""}
  </article>`).join("");
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
  const pendingHTML = pending.map(card => mailCardHtml(card, {readOnly: false, showBody: false}));
  const recentHTML = recent.map(card => mailCardHtml(card, {readOnly: false, showBody: false}));
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
  const accountOptions = accounts.map(account => `<option value="${escapeHTML(account.account_id)}" data-default-template="${escapeHTML(account.default_template_id || "")}">${escapeHTML(account.display_name || account.from_address)} · ${escapeHTML(account.from_address)}</option>`).join("");
  const templateOptions = templates.map(template => `<option value="${escapeHTML(template.id)}">${escapeHTML(template.name)} · v${Number(template.current_version)}</option>`).join("");
  const attachmentOptions = (options.attachments || []).map(attachment => `<label class="mail-attachment-option"><input type="checkbox" name="attachment_ids" value="${escapeHTML(attachment.id)}"><span><strong>${escapeHTML(attachment.name)}</strong><small>${formatMailBytes(attachment.size)} · ${escapeHTML(attachment.media_type || "")}</small><small>SHA256 ${escapeHTML((attachment.sha256 || "").slice(0, 16))}…</small></span></label>`).join("");
  const artifactOptions = (options.build_artifacts || []).map(artifact => `<label class="mail-attachment-option"><input type="checkbox" name="build_artifact_ids" value="${escapeHTML(artifact.id)}"><span><strong>${escapeHTML(artifact.label)}</strong><small>${escapeHTML(artifact.package_name || "")}${artifact.version ? ` · ${escapeHTML(artifact.version)}` : ""} · ${formatMailBytes(artifact.size_bytes)}</small><small>SHA256 ${escapeHTML((artifact.sha256 || "").slice(0, 16))}…</small></span></label>`).join("");
  const dialog = document.createElement("dialog");
  dialog.id = "task-mail-compose-dialog";
  dialog.className = "task-mail-compose-dialog";
  dialog.innerHTML = `<form id="task-mail-compose-form">
    <div class="dialog-head"><h2>发送邮件</h2><button type="button" data-close class="icon-button" aria-label="关闭">${icon("close")}</button></div>
    <div class="dialog-body">
      <div class="two"><label>账号<select name="account_id" required>${accountOptions}</select></label><label>模板<select name="template_id" required>${templateOptions}</select></label></div>
      <div class="two"><label>收件人 To（逗号分隔）<input name="to" required></label><label>CC（逗号分隔）<input name="cc"></label></div>
      <label>BCC（仅 Owner，可选）<input name="bcc"></label>
      <div id="mail-compose-variables">${variableInputs(templateDefaults(templates[0]?.id, templates))}</div>
      <div class="mail-compose-attachments">
        <section class="mail-compose-attachment-section">
          <header class="mail-compose-attachment-head"><strong>当前 Task 附件</strong><span data-mail-selected-count="attachment_ids">已选 0 项</span></header>
          <div class="mail-attachment-list-scroll">${attachmentOptions || `<p class="field-help">没有可用附件。</p>`}</div>
        </section>
        <section class="mail-compose-attachment-section">
          <header class="mail-compose-attachment-head"><strong>构建产物</strong><span data-mail-selected-count="build_artifact_ids">已选 0 项</span></header>
          <div class="mail-attachment-list-scroll">${artifactOptions || `<p class="field-help">没有登记的构建产物。</p>`}</div>
        </section>
      </div>
      <div class="mail-compose-preview" id="mail-compose-preview" hidden></div>
    </div>
    <div class="dialog-actions"><button type="button" data-close>取消</button><button type="button" id="mail-compose-preview-button">预览</button><button type="submit" class="primary">提交审批</button></div>
  </form>`;
  document.body.appendChild(dialog);
  const form = dialog.querySelector<HTMLFormElement>("#task-mail-compose-form")!;
  const templateSelect = form.querySelector<HTMLSelectElement>('select[name="template_id"]')!;
  const accountSelect = form.querySelector<HTMLSelectElement>('select[name="account_id"]')!;
  const variablesBox = form.querySelector<HTMLElement>("#mail-compose-variables")!;
  const previewBox = form.querySelector<HTMLElement>("#mail-compose-preview")!;
  const syncSelectedCounts = () => {
    form.querySelectorAll<HTMLElement>("[data-mail-selected-count]").forEach(element => {
      const name = element.dataset.mailSelectedCount || "";
      const count = form.querySelectorAll(`input[name="${name}"]:checked`).length;
      element.textContent = `已选 ${count} 项`;
    });
  };
  form.addEventListener("change", event => {
    const target = event.target;
    if (target instanceof HTMLInputElement && (target.name === "attachment_ids" || target.name === "build_artifact_ids")) {
      syncSelectedCounts();
    }
  });
  syncSelectedCounts();
  const syncTemplate = () => {
    const template = templateDefaults(templateSelect.value, templates);
    variablesBox.innerHTML = variableInputs(template);
    const to = form.querySelector<HTMLInputElement>('input[name="to"]')!;
    const cc = form.querySelector<HTMLInputElement>('input[name="cc"]')!;
    if (!to.value) to.value = (template?.default_to || []).join(", ");
    if (!cc.value) cc.value = (template?.default_cc || []).join(", ");
    previewBox.hidden = true;
  };
  templateSelect.addEventListener("change", syncTemplate);
  accountSelect.addEventListener("change", () => {
    const selected = accountSelect.selectedOptions[0];
    const defaultTemplate = selected?.dataset.defaultTemplate || "";
    if (defaultTemplate && templates.some(template => template.id === defaultTemplate)) {
      templateSelect.value = defaultTemplate;
      syncTemplate();
    }
  });
  const close = () => { dialog.close(); dialog.remove(); };
  dialog.querySelectorAll("[data-close]").forEach(button => button.addEventListener("click", close));
  dialog.addEventListener("cancel", close);
  const initialDefault = accountSelect.selectedOptions[0]?.dataset.defaultTemplate || "";
  if (initialDefault && templates.some(template => template.id === initialDefault)) {
    templateSelect.value = initialDefault;
  }
  syncTemplate();

  const collect = () => {
    const values = new FormData(form);
    const variables = Object.fromEntries([...form.querySelectorAll<HTMLInputElement>("[data-mail-variable-input]")].map(input => [input.dataset.mailVariableInput || "", input.value]));
    const variableValues = Object.fromEntries(Object.entries(variables).filter(([, value]) => String(value).trim() !== ""));
    const csv = (value: FormDataEntryValue | null) => String(value || "").split(",").map(item => item.trim()).filter(Boolean);
    const payload = {
      task_id: detail.task.id,
      account_id: String(values.get("account_id") || ""),
      template_id: String(values.get("template_id") || ""),
      to: csv(values.get("to")),
      cc: csv(values.get("cc")),
      bcc: csv(values.get("bcc")),
      variables: variableValues,
      attachment_ids: values.getAll("attachment_ids").map(String),
      build_artifact_ids: values.getAll("build_artifact_ids").map(String),
    };
    return payload;
  };
  form.querySelector("#mail-compose-preview-button")?.addEventListener("click", () => {
    const payload = collect();
    void api.previewMailTemplate(payload.template_id, {
      task_id: payload.task_id, variables: payload.variables, to: payload.to, cc: payload.cc,
      attachment_ids: payload.attachment_ids, build_artifact_ids: payload.build_artifact_ids,
    }).then(result => {
      const preview = result.preview as Record<string, any>;
      const attachments = Array.isArray(preview.attachments) ? preview.attachments as Array<Record<string, any>> : [];
      previewBox.hidden = false;
      previewBox.innerHTML = `<strong>预览：${escapeHTML(preview.subject || "")}</strong><p>To：${escapeHTML((preview.to || []).join(", "))}</p><p>CC：${escapeHTML((preview.cc || []).join(", "))}</p>${(preview.warnings || []).length ? `<p class="mail-card-warning">${escapeHTML((preview.warnings as string[]).join(" · "))}</p>` : ""}<p>附件：${escapeHTML(attachments.map(item => item.file_name).join(", ") || "无")}</p><pre>${escapeHTML(preview.body_text || "")}</pre>`;
    }).catch(error => actions.notify("error", error instanceof Error ? error.message : String(error)));
  });
  form.addEventListener("submit", event => {
    event.preventDefault();
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
      // Open the approval snapshot without closing the panel. The dialog owns
      // only presentation; the existing approve/reject APIs remain the single
      // decision path.
      openMailPreviewDialog({
        title: "查看并审批",
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
        onApprove: async () => {
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
        },
        onReject: async () => {
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
        },
      });
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
