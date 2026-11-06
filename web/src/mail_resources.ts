import {api} from "./api.js";
import {icon} from "./icons.js";
import {formatMailBytes, formatMailTime, mailAttachmentsHtml, mailCanCancel, mailCanDecide, mailCanRetry, mailDigestShort, mailStatusClass, mailStatusLabel} from "./mail_cards.js";
import type {MailAccount, MailAttachment, MailDelivery, MailDeliveryAttempt, MailMessage, MailTemplate, MailTemplateVersion, Project} from "./types.js";

type Notice = (type: "error" | "notice", message: string) => void;
type MailSubTab = "accounts" | "templates" | "sent";

export interface MailResourcesData {
  subTab: MailSubTab;
  accounts: MailAccount[];
  templates: MailTemplate[];
  messages: MailMessage[];
  projects: Project[];
  filterStatus: string;
  filterAccount: string;
  loading: boolean;
  error: string;
}

export interface MailResourceActions {
  reload: (options?: {status?: string; account?: string}) => Promise<void>;
  notify: Notice;
}

function escapeHTML(value: unknown): string {
  return String(value ?? "")
    .replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;").replaceAll("'", "&#039;");
}

function statusChip(account: MailAccount): string {
  const label = {ok: "正常", error: "认证失败", disabled: "已停用", unverified: "未验证"}[account.status] || account.status;
  return `<span class="mail-status ${account.status === "ok" ? "sent" : account.status === "error" ? "failed" : account.status === "disabled" ? "muted" : "pending"}">${escapeHTML(label)}</span>`;
}

function tabButton(active: MailSubTab, id: MailSubTab, label: string): string {
  return `<button type="button" class="mail-subtab ${active === id ? "active" : ""}" data-mail-subtab="${id}" role="tab" aria-selected="${active === id}">${label}</button>`;
}

function accountsPanel(data: MailResourcesData): string {
  const rows = data.accounts.map(account => `<article class="mail-resource-row" data-mail-account="${escapeHTML(account.id)}">
    <div class="mail-resource-identity"><span class="mail-resource-glyph">${icon("mail")}</span><div><strong>${escapeHTML(account.name || account.address)}</strong><small>${escapeHTML(account.address)} · ${escapeHTML(account.smtp_host)}:${Number(account.smtp_port)}</small><small>${escapeHTML(account.display_name || "无显示名")}${account.aliases?.length ? ` · 别名 ${account.aliases.map(alias => escapeHTML(alias.address)).join(", ")}` : ""}</small></div></div>
    <div class="mail-resource-meta">${statusChip(account)}<span>${account.credential_configured ? "授权码已配置" : "未配置授权码"}</span><span>${account.last_test_at ? `最近测试 ${escapeHTML(formatMailTime(account.last_test_at))}` : "尚未测试"}</span>${account.last_test_error_code ? `<span class="mail-error-code">${escapeHTML(account.last_test_error_code)}</span>` : ""}</div>
    <div class="mail-resource-actions">
      <button type="button" class="icon-button" data-mail-account-edit="${escapeHTML(account.id)}" title="编辑账号" aria-label="编辑账号">${icon("edit")}</button>
      <button type="button" class="icon-button" data-mail-account-test="${escapeHTML(account.id)}" title="连接测试" aria-label="连接测试">${icon("refresh")}</button>
      <button type="button" data-mail-account-toggle="${escapeHTML(account.id)}">${account.status === "disabled" ? "启用" : "停用"}</button>
      <button type="button" class="icon-button danger" data-mail-account-delete="${escapeHTML(account.id)}" title="删除账号" aria-label="删除账号">${icon("close")}</button>
    </div>
  </article>`).join("");
  return `<section class="mail-resources-panel">
    <div class="resource-panel-toolbar"><div><strong>发信账号</strong><p>授权码只写入 Secret Store，页面不回显授权码或 Secret 引用。</p></div><button type="button" id="add-mail-account" class="primary">${icon("plus")}添加账号</button></div>
    ${data.loading ? `<div class="mail-loading">${icon("spinner", true)}正在加载账号…</div>` : data.error ? `<div class="empty-state"><strong>加载失败</strong><span>${escapeHTML(data.error)}</span></div>` : data.accounts.length ? `<div class="mail-resource-list">${rows}</div>` : `<div class="empty-state">${icon("mail")}<strong>还没有邮箱账号</strong><span>添加网易企业邮箱账号后，可在 Task 中绑定并发起审批邮件。</span><button type="button" class="primary" data-mail-account-add-empty>${icon("plus")}添加账号</button></div>`}
  </section>`;
}

function templateRows(data: MailResourcesData): string {
  return data.templates.map(template => `<article class="mail-resource-row" data-mail-template="${escapeHTML(template.id)}">
    <div class="mail-resource-identity"><span class="mail-resource-glyph">${icon("mail")}</span><div><strong>${escapeHTML(template.name)}</strong><small>${template.scope === "project" ? `项目模板 · ${escapeHTML(data.projects.find(project => project.id === template.project_id)?.name || template.project_id || "")}` : "全局模板"} · v${Number(template.current_version)}</small><small>默认收件人：${escapeHTML((template.default_to || []).join(", ") || "无")}${(template.default_cc || []).length ? ` · 默认 CC：${escapeHTML(template.default_cc.join(", "))}` : ""}</small></div></div>
    <div class="mail-resource-meta"><span class="mail-status ${template.status === "active" ? "sent" : "muted"}">${template.status === "active" ? "启用" : "已归档"}</span><span>更新 ${escapeHTML(formatMailTime(template.updated_at))}</span><span>${(template.required_variables || []).length} 个必填变量</span></div>
    <div class="mail-resource-actions">
      <button type="button" class="icon-button" data-mail-template-edit="${escapeHTML(template.id)}" title="编辑模板" aria-label="编辑模板">${icon("edit")}</button>
      <button type="button" class="icon-button" data-mail-template-preview="${escapeHTML(template.id)}" title="预览模板" aria-label="预览模板">${icon("eye")}</button>
      <button type="button" class="icon-button" data-mail-template-versions="${escapeHTML(template.id)}" title="版本历史" aria-label="版本历史">${icon("clock")}</button>
      ${template.status === "active" ? `<button type="button" class="icon-button danger" data-mail-template-archive="${escapeHTML(template.id)}" title="归档模板" aria-label="归档模板">${icon("close")}</button>` : ""}
    </div>
  </article>`).join("");
}

function templatesPanel(data: MailResourcesData): string {
  const rows = templateRows(data);
  return `<section class="mail-resources-panel">
    <div class="resource-panel-toolbar"><div><strong>邮件模板</strong><p>模板版本不可变；已提交邮件保留自己的渲染快照。</p></div><button type="button" id="add-mail-template" class="primary">${icon("plus")}新建模板</button></div>
    ${data.loading ? `<div class="mail-loading">${icon("spinner", true)}正在加载模板…</div>` : rows ? `<div class="mail-resource-list">${rows}</div>` : `<div class="empty-state">${icon("mail")}<strong>还没有邮件模板</strong><span>创建模板后可在发送弹窗中选择并预览。</span><button type="button" class="primary" data-mail-template-add-empty>${icon("plus")}新建模板</button></div>`}
  </section>`;
}

function sentPanel(data: MailResourcesData): string {
  const statusOptions = ["", "pending_approval", "queued", "sending", "sent", "failed", "dead_letter", "rejected", "cancelled"].map(status =>
    `<option value="${status}" ${data.filterStatus === status ? "selected" : ""}>${status ? escapeHTML(mailStatusLabel(status)) : "全部状态"}</option>`).join("");
  const accountOptions = `<option value="">全部账号</option>` + data.accounts.map(account =>
    `<option value="${escapeHTML(account.id)}" ${data.filterAccount === account.id ? "selected" : ""}>${escapeHTML(account.name || account.address)}</option>`).join("");
  const rows = data.messages.map(message => `<article class="mail-resource-row" data-mail-message-row="${escapeHTML(message.id)}">
    <div class="mail-resource-identity"><span class="mail-resource-glyph">${icon("mail")}</span><div><strong>${escapeHTML(message.subject || "（无主题）")}</strong><small>${escapeHTML((message.to || []).join(", ") || "无收件人")}</small><small>${escapeHTML(message.from_address)} · ${escapeHTML(formatMailTime(message.created_at))}</small></div></div>
    <div class="mail-resource-meta"><span class="mail-status ${mailStatusClass(message.status)}">${escapeHTML(mailStatusLabel(message.status))}</span><span>${escapeHTML(message.account_id)}</span><span>${message.source === "agent" ? "Agent 提交" : "Owner 提交"}</span></div>
    <div class="mail-resource-actions"><button type="button" data-mail-message-open="${escapeHTML(message.id)}">查看详情</button></div>
  </article>`).join("");
  return `<section class="mail-resources-panel">
    <div class="resource-panel-toolbar"><div><strong>发送记录</strong><p>审批、投递与失败原因都在这里查看；凭据和完整 SMTP 会话不会展示。</p></div><div class="mail-filters"><label>状态<select id="mail-sent-status">${statusOptions}</select></label><label>账号<select id="mail-sent-account">${accountOptions}</select></label></div></div>
    ${data.loading ? `<div class="mail-loading">${icon("spinner", true)}正在加载发送记录…</div>` : rows ? `<div class="mail-resource-list">${rows}</div>` : `<div class="empty-state">${icon("mail")}<strong>没有符合条件的发送记录</strong><span>Agent 或 Owner 提交邮件后会出现在这里。</span></div>`}
  </section>`;
}

export function renderMailResources(data: MailResourcesData): string {
  const body = data.subTab === "accounts" ? accountsPanel(data) : data.subTab === "templates" ? templatesPanel(data) : sentPanel(data);
  return `<section class="mail-resources-view" data-mail-subtab-active="${data.subTab}">
    <div class="mail-subtabs" role="tablist" aria-label="邮件资源分类">${tabButton(data.subTab, "accounts", "账号")}${tabButton(data.subTab, "templates", "模板")}${tabButton(data.subTab, "sent", "发送记录")}</div>
    ${body}
  </section>`;
}

function dialogShell(title: string, body: string, submitLabel: string | null = "保存"): HTMLDialogElement {
  const dialog = document.createElement("dialog");
  dialog.className = "wide mail-resource-dialog";
  dialog.innerHTML = `<form class="mail-dialog-form"><div class="dialog-head"><h2>${escapeHTML(title)}</h2><button type="button" data-close class="icon-button" aria-label="关闭">${icon("close")}</button></div>${body}<div class="dialog-actions"><button type="button" data-close>${submitLabel ? "取消" : "关闭"}</button>${submitLabel ? `<button type="submit" class="primary">${submitLabel}</button>` : ""}</div></form>`;
  return dialog;
}

function accountForm(account?: MailAccount): string {
  const aliases = (account?.aliases || []).map(alias => alias.address).join(", ");
  const hostOptions = [`smtp.qiye.163.com`, `smtphz.qiye.163.com`].map(host =>
    `<option value="${host}" ${account?.smtp_host === host ? "selected" : ""}>${host}</option>`).join("");
  return `<div class="mail-form-grid">
    <label>名称<input name="name" maxlength="200" required value="${escapeHTML(account?.name || "")}" placeholder="例如：构建通知"></label>
    <label>发件地址<input name="address" type="email" required value="${escapeHTML(account?.address || "")}" placeholder="noreply@example.com"></label>
    <label>显示名<input name="display_name" value="${escapeHTML(account?.display_name || "")}" placeholder="AHA 构建通知"></label>
    <label>SMTP 用户名<input name="smtp_username" value="${escapeHTML(account?.smtp_username || "")}" placeholder="默认与发件地址相同"></label>
    <label>SMTP 主机<select name="smtp_host">${hostOptions}</select></label>
    <label>端口<input name="smtp_port" type="number" value="${Number(account?.smtp_port || 465)}" readonly></label>
    <label>加密<select name="smtp_security"><option value="ssl" selected>SSL（隐式 TLS）</option></select></label>
    <label>别名（逗号分隔）<input name="aliases" value="${escapeHTML(aliases)}" placeholder="build-bot@example.com"></label>
    <label class="mail-form-wide">授权码<input name="authorization_code" type="password" autocomplete="new-password" placeholder="${account?.credential_configured ? "已配置，留空保持不变" : "写入一次，不会回显"}"></label>
  </div><p class="field-help">授权码只写入现有 Secret Store；页面、日志和事件都不会保存或回显它。</p>`;
}

function templateForm(template: MailTemplate | undefined, projects: Project[]): string {
  const scope = template?.scope || "global";
  const projectOptions = projects.filter(project => !project.channel_retired).map(project =>
    `<option value="${escapeHTML(project.id)}" ${template?.project_id === project.id ? "selected" : ""}>${escapeHTML(project.name)}</option>`).join("");
  const variables = ["task.id", "task.title", "task.url", "project.name", "workspace.name", "build.version", "build.commit", "build.branch", "build.time", "package.name", "package.size", "package.sha256", "test.scope", "test.notes", "test.environment", "release.version", "release.date", "release.owner", "agent.name"];
  return `<div class="mail-form-grid">
    <label>名称<input name="name" maxlength="200" required value="${escapeHTML(template?.name || "")}"></label>
    <label>作用域<select name="scope"><option value="global" ${scope === "global" ? "selected" : ""}>全局</option><option value="project" ${scope === "project" ? "selected" : ""}>项目</option></select></label>
    <label data-mail-template-project>项目<select name="project_id">${projectOptions}</select></label>
    <label>默认收件人（逗号分隔）<input name="default_to" value="${escapeHTML((template?.default_to || []).join(", "))}"></label>
    <label>默认 CC（逗号分隔）<input name="default_cc" value="${escapeHTML((template?.default_cc || []).join(", "))}"></label>
    <label class="mail-form-wide">必填变量（逗号分隔）<input name="required_variables" value="${escapeHTML((template?.required_variables || []).join(", "))}" placeholder="test.scope, test.notes"></label>
  </div>
  <div class="mail-variable-bar" aria-label="插入变量">${variables.map(name => `<button type="button" data-mail-variable="${name}">{{${name}}}</button>`).join("")}</div>
  <label>主题<input name="subject_template" required value="${escapeHTML(template?.subject_template || "")}"></label>
  <label>纯文本正文<textarea name="body_text_template" rows="7">${escapeHTML(template?.body_text_template || "")}</textarea></label>
  <label>HTML 正文（可选，安全子集）<textarea name="body_html_template" rows="5">${escapeHTML(template?.body_html_template || "")}</textarea></label>
  <p class="field-help">变量只做字符串替换；HTML 只允许安全子集，变量替换后会再次 sanitize。</p>`;
}

function openPreviewDialog(preview: Record<string, any>, notify: Notice): void {
  document.querySelectorAll<HTMLDialogElement>(".mail-resource-dialog[open]").forEach(open => {
    open.close();
    open.remove();
  });
  const previous = document.querySelector<HTMLDialogElement>("#mail-template-preview-dialog");
  previous?.remove();
  const dialog = document.createElement("dialog");
  dialog.id = "mail-template-preview-dialog";
  dialog.className = "wide mail-resource-dialog";
  const attachments = Array.isArray(preview.attachments) ? preview.attachments as MailAttachment[] : [];
  dialog.innerHTML = `<div class="dialog-head"><h2>模板预览</h2><button type="button" data-close class="icon-button" aria-label="关闭">${icon("close")}</button></div>
    <div class="dialog-body"><p><strong>主题：</strong>${escapeHTML(preview.subject || "")}</p><p><strong>收件人：</strong>${escapeHTML((preview.to || []).join(", "))}</p><p><strong>CC：</strong>${escapeHTML((preview.cc || []).join(", "))}</p>${attachments.length ? mailAttachmentsHtml(attachments) : ""}${(preview.warnings || []).length ? `<ul class="mail-warnings">${(preview.warnings as string[]).map(warning => `<li>${escapeHTML(warning)}</li>`).join("")}</ul>` : ""}<h3>纯文本</h3><pre class="mail-preview-body">${escapeHTML(preview.body_text || "")}</pre><h3>HTML</h3><iframe class="mail-preview-frame" sandbox="" srcdoc="${escapeHTML(preview.body_html || "")}" title="HTML 预览"></iframe></div><div class="dialog-actions"><button type="button" data-close>关闭</button></div>`;
  document.body.appendChild(dialog);
  dialog.querySelectorAll("[data-close]").forEach(button => button.addEventListener("click", () => { dialog.close(); dialog.remove(); }));
  dialog.addEventListener("cancel", () => { dialog.close(); dialog.remove(); });
  dialog.showModal();
  if (!preview.subject) notify("error", "预览未返回内容");
}

export function bindMailResources(data: MailResourcesData, actions: MailResourceActions): void {
  const closeDialogs = () => document.querySelectorAll<HTMLDialogElement>(".mail-resource-dialog[open]").forEach(dialog => { dialog.close(); dialog.remove(); });
  const reload = async (options?: {status?: string; account?: string}) => {
    closeDialogs();
    await actions.reload(options);
  };
  const appendDialog = (dialog: HTMLDialogElement) => {
    document.querySelectorAll<HTMLDialogElement>(".mail-resource-dialog[open]").forEach(open => { open.close(); open.remove(); });
    document.body.appendChild(dialog);
    dialog.querySelectorAll("[data-close]").forEach(button => button.addEventListener("click", () => { dialog.close(); dialog.remove(); }));
    dialog.addEventListener("cancel", () => { dialog.close(); dialog.remove(); });
    dialog.showModal();
    return dialog;
  };

  document.querySelectorAll<HTMLElement>("[data-mail-subtab]").forEach(button => button.addEventListener("click", () => {
    const selected = String(button.dataset.mailSubtab) as MailSubTab;
    // The tab switch is a display change; main updates its state synchronously
    // on this event, then the reload renders the selected panel.
    window.dispatchEvent(new CustomEvent("aha-mail-subtab", {detail: selected}));
    void actions.reload().catch(error => actions.notify("error", error instanceof Error ? error.message : String(error)));
  }));
  document.querySelector<HTMLSelectElement>("#mail-sent-status")?.addEventListener("change", event => {
    const value = (event.currentTarget as HTMLSelectElement).value;
    void actions.reload({status: value, account: data.filterAccount});
  });
  document.querySelector<HTMLSelectElement>("#mail-sent-account")?.addEventListener("change", event => {
    const value = (event.currentTarget as HTMLSelectElement).value;
    void actions.reload({status: data.filterStatus, account: value});
  });

  const openAccountDialog = (account?: MailAccount) => {
    const dialog = dialogShell(account ? "编辑邮箱账号" : "添加邮箱账号", accountForm(account));
    appendDialog(dialog);
    const form = dialog.querySelector<HTMLFormElement>(".mail-dialog-form");
    form?.addEventListener("submit", event => {
      event.preventDefault();
      const submit = form.querySelector<HTMLButtonElement>('button[type="submit"]');
      if (submit) submit.disabled = true;
      const values = new FormData(form);
      const aliases = String(values.get("aliases") || "").split(",").map(value => value.trim()).filter(Boolean);
      const authorizationCode = String(values.get("authorization_code") || "");
      const payload: Record<string, unknown> = {
        name: String(values.get("name") || "").trim(),
        address: String(values.get("address") || "").trim(),
        display_name: String(values.get("display_name") || "").trim(),
        provider: "netease_qiye",
        smtp_host: String(values.get("smtp_host") || "smtp.qiye.163.com"),
        smtp_port: Number(values.get("smtp_port") || 465),
        smtp_security: "ssl",
        smtp_username: String(values.get("smtp_username") || "").trim(),
        aliases,
      };
      if (authorizationCode) payload.authorization_code = authorizationCode;
      const request = account ? api.updateMailAccount(account.id, payload) : api.createMailAccount({...payload, authorization_code: authorizationCode});
      void request.then(async () => {
        await reload();
        actions.notify("notice", account ? "邮箱账号已保存" : "邮箱账号已添加");
      }).catch(error => actions.notify("error", error instanceof Error ? error.message : String(error))).finally(() => {
        if (submit?.isConnected) submit.disabled = false;
      });
    });
  };

  document.querySelector("#add-mail-account")?.addEventListener("click", () => openAccountDialog());
  document.querySelector("[data-mail-account-add-empty]")?.addEventListener("click", () => openAccountDialog());
  document.querySelectorAll<HTMLElement>("[data-mail-account-edit]").forEach(button => button.addEventListener("click", () => {
    const account = data.accounts.find(item => item.id === button.dataset.mailAccountEdit);
    if (account) openAccountDialog(account);
  }));
  document.querySelectorAll<HTMLElement>("[data-mail-account-test]").forEach(button => button.addEventListener("click", () => {
    const id = button.dataset.mailAccountTest || "";
    button.disabled = true;
    void api.testMailAccount(id).then(() => reload()).then(() => actions.notify("notice", "连接测试完成")).catch(error => {
      actions.notify("error", error instanceof Error ? error.message : String(error));
      void reload();
    }).finally(() => { if (button.isConnected) button.disabled = false; });
  }));
  document.querySelectorAll<HTMLElement>("[data-mail-account-toggle]").forEach(button => button.addEventListener("click", () => {
    const account = data.accounts.find(item => item.id === button.dataset.mailAccountToggle);
    if (!account) return;
    const next = account.status === "disabled" ? "enabled" : "disabled";
    if (!window.confirm(`${next === "disabled" ? "停用" : "启用"}邮箱账号“${account.name || account.address}”？`)) return;
    void api.updateMailAccount(account.id, {status: next}).then(() => reload()).then(() => actions.notify("notice", "账号状态已更新")).catch(error => actions.notify("error", error instanceof Error ? error.message : String(error)));
  }));
  document.querySelectorAll<HTMLElement>("[data-mail-account-delete]").forEach(button => button.addEventListener("click", () => {
    const account = data.accounts.find(item => item.id === button.dataset.mailAccountDelete);
    if (!account || !window.confirm(`删除邮箱账号“${account.name || account.address}”？存在历史邮件或绑定时服务端会拒绝。`)) return;
    void api.deleteMailAccount(account.id).then(() => reload()).then(() => actions.notify("notice", "邮箱账号已删除")).catch(error => actions.notify("error", error instanceof Error ? error.message : String(error)));
  }));

  const openTemplateDialog = (template?: MailTemplate) => {
    const dialog = dialogShell(template ? "编辑邮件模板" : "新建邮件模板", templateForm(template, data.projects));
    appendDialog(dialog);
    const form = dialog.querySelector<HTMLFormElement>(".mail-dialog-form");
    const syncScope = () => {
      const scope = String(new FormData(form!).get("scope") || "global");
      form?.querySelectorAll<HTMLElement>("[data-mail-template-project]").forEach(element => { element.hidden = scope !== "project"; });
    };
    form?.querySelector('select[name="scope"]')?.addEventListener("change", syncScope);
    syncScope();
    form?.querySelectorAll<HTMLButtonElement>("[data-mail-variable]").forEach(button => button.addEventListener("click", () => {
      const target = document.activeElement instanceof HTMLTextAreaElement || document.activeElement instanceof HTMLInputElement
        ? document.activeElement as HTMLTextAreaElement | HTMLInputElement
        : form.querySelector<HTMLTextAreaElement>('textarea[name="body_text_template"]');
      if (!target) return;
      const token = `{{${button.dataset.mailVariable}}}`;
      const start = target.selectionStart ?? target.value.length;
      const end = target.selectionEnd ?? start;
      target.value = `${target.value.slice(0, start)}${token}${target.value.slice(end)}`;
      target.focus();
      target.setSelectionRange(start + token.length, start + token.length);
    }));
    form?.addEventListener("submit", event => {
      event.preventDefault();
      const values = new FormData(form);
      const payload: Record<string, unknown> = {
        scope: String(values.get("scope") || "global"),
        project_id: String(values.get("project_id") || ""),
        name: String(values.get("name") || "").trim(),
        subject_template: String(values.get("subject_template") || ""),
        body_text_template: String(values.get("body_text_template") || ""),
        body_html_template: String(values.get("body_html_template") || ""),
        default_to: String(values.get("default_to") || "").split(",").map(value => value.trim()).filter(Boolean),
        default_cc: String(values.get("default_cc") || "").split(",").map(value => value.trim()).filter(Boolean),
        required_variables: String(values.get("required_variables") || "").split(",").map(value => value.trim()).filter(Boolean),
      };
      if (template) payload.expected_version = template.current_version;
      const request = template ? api.updateMailTemplate(template.id, payload) : api.createMailTemplate(payload);
      void request.then(async () => {
        await reload();
        actions.notify("notice", template ? "邮件模板已保存" : "邮件模板已创建");
      }).catch(error => actions.notify("error", error instanceof Error ? error.message : String(error)));
    });
  };
  document.querySelector("#add-mail-template")?.addEventListener("click", () => openTemplateDialog());
  document.querySelector("[data-mail-template-add-empty]")?.addEventListener("click", () => openTemplateDialog());
  document.querySelectorAll<HTMLElement>("[data-mail-template-edit]").forEach(button => button.addEventListener("click", () => {
    const template = data.templates.find(item => item.id === button.dataset.mailTemplateEdit);
    if (template) openTemplateDialog(template);
  }));
  document.querySelectorAll<HTMLElement>("[data-mail-template-archive]").forEach(button => button.addEventListener("click", () => {
    const template = data.templates.find(item => item.id === button.dataset.mailTemplateArchive);
    if (!template || !window.confirm(`归档模板“${template.name}”？已提交邮件仍保留快照。`)) return;
    void api.archiveMailTemplate(template.id).then(() => reload()).then(() => actions.notify("notice", "模板已归档")).catch(error => actions.notify("error", error instanceof Error ? error.message : String(error)));
  }));
  document.querySelectorAll<HTMLElement>("[data-mail-template-preview]").forEach(button => button.addEventListener("click", () => {
    const template = data.templates.find(item => item.id === button.dataset.mailTemplatePreview);
    if (!template) return;
    const dialog = dialogShell(`预览：${template.name}`, `<label>变量（JSON）<textarea name="variables" rows="4">{}</textarea></label><label>收件人（逗号分隔）<input name="to" value="${escapeHTML((template.default_to || []).join(", "))}"></label><p class="field-help">预览不会创建邮件。</p>`, null);
    appendDialog(dialog);
    const form = dialog.querySelector<HTMLFormElement>(".mail-dialog-form");
    form?.addEventListener("submit", event => event.preventDefault());
    const actionsRow = dialog.querySelector<HTMLElement>(".dialog-actions");
    if (actionsRow) actionsRow.insertAdjacentHTML("afterbegin", `<button type="button" id="run-mail-template-preview">生成预览</button>`);
    dialog.querySelector("#run-mail-template-preview")?.addEventListener("click", () => {
      let variables: Record<string, string> = {};
      try { variables = JSON.parse(String(new FormData(form!).get("variables") || "{}")) as Record<string, string>; } catch { actions.notify("error", "变量 JSON 格式不正确"); return; }
      const to = String(new FormData(form!).get("to") || "").split(",").map(value => value.trim()).filter(Boolean);
      void api.previewMailTemplate(template.id, {variables, to}).then(result => openPreviewDialog(result.preview, actions.notify)).catch(error => actions.notify("error", error instanceof Error ? error.message : String(error)));
    });
  }));
  document.querySelectorAll<HTMLElement>("[data-mail-template-versions]").forEach(button => button.addEventListener("click", () => {
    const template = data.templates.find(item => item.id === button.dataset.mailTemplateVersions);
    if (!template) return;
    void api.mailTemplateVersions(template.id).then(result => {
      const versions = result.versions || [];
      const dialog = document.createElement("dialog");
      dialog.className = "wide mail-resource-dialog";
      dialog.innerHTML = `<div class="dialog-head"><h2>${escapeHTML(template.name)} · 版本历史</h2><button type="button" data-close class="icon-button" aria-label="关闭">${icon("close")}</button></div><div class="dialog-body">${versions.length ? versions.map((version: MailTemplateVersion) => `<details class="mail-version"><summary>v${Number(version.version)} · ${escapeHTML(formatMailTime(version.created_at))}</summary><p><strong>主题：</strong>${escapeHTML(version.subject_template)}</p><pre>${escapeHTML(version.body_text_template || "")}</pre></details>`).join("") : `<p class="field-help">没有版本记录。</p>`}</div><div class="dialog-actions"><button type="button" data-close>关闭</button></div>`;
      document.body.appendChild(dialog);
      dialog.querySelectorAll("[data-close]").forEach(close => close.addEventListener("click", () => { dialog.close(); dialog.remove(); }));
      dialog.addEventListener("cancel", () => { dialog.close(); dialog.remove(); });
      dialog.showModal();
    }).catch(error => actions.notify("error", error instanceof Error ? error.message : String(error)));
  }));

  document.querySelectorAll<HTMLElement>("[data-mail-message-open]").forEach(button => button.addEventListener("click", () => {
    void openMessageDetail(button.dataset.mailMessageOpen || "", actions, data.accounts, reload);
  }));
}

async function openMessageDetail(messageID: string, actions: MailResourceActions, accounts: MailAccount[], reload: (options?: {status?: string; account?: string}) => Promise<void>): Promise<void> {
  if (!messageID) return;
  const [detail, attemptsResult, linksResult] = await Promise.all([
    api.mailMessage(messageID),
    api.mailMessageAttempts(messageID).catch(() => ({ok: true, deliveries: []})),
    api.mailMessageDownloadLinks(messageID).catch(() => ({ok: true, links: []})),
  ]);
  const message = detail.message;
  const account = accounts.find(item => item.id === message.account_id);
  const dialog = document.createElement("dialog");
  dialog.className = "wide mail-message-detail-dialog";
  const actionsRow: string[] = [];
  if (mailCanDecide(message.status)) actionsRow.push(`<button type="button" class="primary" data-detail-action="approve">批准</button><button type="button" class="danger" data-detail-action="reject">拒绝</button>`);
  if (mailCanRetry(message.status)) actionsRow.push(`<button type="button" data-detail-action="retry">重试</button>`);
  if (mailCanCancel(message.status)) actionsRow.push(`<button type="button" data-detail-action="cancel">取消</button>`);
  const links = linksResult.links || [];
  dialog.innerHTML = `<div class="dialog-head"><h2>${escapeHTML(message.subject || "邮件详情")}</h2><button type="button" data-close class="icon-button" aria-label="关闭">${icon("close")}</button></div>
    <div class="dialog-body">
      <p><strong>状态：</strong><span class="mail-status ${mailStatusClass(message.status)}">${escapeHTML(mailStatusLabel(message.status))}</span></p>
      <p><strong>账号：</strong>${escapeHTML(account?.name || message.account_id)} · ${escapeHTML(message.from_address)}</p>
      <p><strong>收件人：</strong>${escapeHTML((message.to || []).join(", "))}</p>
      ${(message.cc || []).length ? `<p><strong>CC：</strong>${escapeHTML((message.cc || []).join(", "))}</p>` : ""}
      ${(message.bcc || []).length ? `<p><strong>BCC：</strong>${escapeHTML((message.bcc || []).join(", "))}</p>` : ""}
      ${message.template_id ? `<p><strong>模板：</strong>${escapeHTML(message.template_id)} v${Number(message.template_version || 0)}</p>` : ""}
      <p><strong>审批摘要：</strong><code>${escapeHTML(mailDigestShort(message.approval_digest))}</code></p>
      ${message.approved_by ? `<p><strong>审批人：</strong>${escapeHTML(message.approved_by)} · ${escapeHTML(formatMailTime(message.approved_at))}</p>` : ""}
      ${message.rejected_reason ? `<p><strong>拒绝原因：</strong>${escapeHTML(message.rejected_reason)}</p>` : ""}
      ${mailAttachmentsHtml(detail.attachments || [])}
      ${links.length ? `<h3>受控下载链接</h3><ul class="mail-link-list">${links.map(link => `<li>${escapeHTML(link.file_name)} · ${Number(link.link_download_count || 0)}/${Number(link.link_max_downloads || 0)} 次 <button type="button" data-mail-link-revoke="${escapeHTML(link.id)}">撤销链接</button></li>`).join("")}</ul>` : ""}
      <h3>投递</h3>
      ${(attemptsResult.deliveries || []).length ? (attemptsResult.deliveries as Array<{delivery: MailDelivery; attempts: MailDeliveryAttempt[]}>).map(item => `<details class="mail-version" open><summary>generation ${Number(item.delivery.generation)} · ${escapeHTML(mailStatusLabel(item.delivery.state))} · 尝试 ${Number(item.delivery.attempts || 0)} 次</summary><p>${escapeHTML(item.delivery.last_error_code || "无错误码")} ${escapeHTML(item.delivery.outcome_certainty || "")}</p>${(item.attempts || []).map(attempt => `<p class="field-help">#${Number(attempt.attempt_no)} ${escapeHTML(attempt.outcome)} ${escapeHTML(attempt.error_code || "")} ${escapeHTML(formatMailTime(attempt.started_at))}</p>`).join("")}</details>`).join("") : `<p>尚未创建投递。</p>`}
      <details class="mail-version"><summary>正文快照</summary><pre>${escapeHTML(message.body_text || "")}</pre></details>
    </div>
    <div class="dialog-actions">${actionsRow.join("")}<button type="button" data-close>关闭</button></div>`;
  document.body.appendChild(dialog);
  const close = () => { dialog.close(); dialog.remove(); };
  dialog.querySelectorAll("[data-close]").forEach(button => button.addEventListener("click", close));
  dialog.addEventListener("cancel", close);
  dialog.showModal();
  const refreshAfter = async (messageText: string) => {
    close();
    await reload();
    actions.notify("notice", messageText);
  };
  dialog.querySelector('[data-detail-action="approve"]')?.addEventListener("click", () => {
    if (!window.confirm("批准这封邮件并进入投递队列？")) return;
    void api.approveMailMessage(message.id, message.approval_digest || "").then(() => refreshAfter("邮件已批准")).catch(error => actions.notify("error", error instanceof Error ? error.message : String(error)));
  });
  dialog.querySelector('[data-detail-action="reject"]')?.addEventListener("click", () => {
    const reason = window.prompt("拒绝原因（必填）");
    if (!reason || !window.confirm("拒绝这封邮件？")) return;
    void api.rejectMailMessage(message.id, reason).then(() => refreshAfter("邮件已拒绝")).catch(error => actions.notify("error", error instanceof Error ? error.message : String(error)));
  });
  dialog.querySelector('[data-detail-action="retry"]')?.addEventListener("click", () => {
    if (!window.confirm("按原快照新建一个投递 generation 重试？")) return;
    void api.retryMailMessage(message.id).then(() => refreshAfter("已创建重试投递")).catch(error => actions.notify("error", error instanceof Error ? error.message : String(error)));
  });
  dialog.querySelector('[data-detail-action="cancel"]')?.addEventListener("click", () => {
    if (!window.confirm("取消这封邮件？发送中不可取消。")) return;
    void api.cancelMailMessage(message.id).then(() => refreshAfter("邮件已取消")).catch(error => actions.notify("error", error instanceof Error ? error.message : String(error)));
  });
  dialog.querySelectorAll<HTMLElement>("[data-mail-link-revoke]").forEach(button => button.addEventListener("click", () => {
    if (!window.confirm("撤销该受控下载链接？撤销立即生效。")) return;
    void api.revokeMailDownloadLink(message.id, button.dataset.mailLinkRevoke || "").then(() => refreshAfter("下载链接已撤销")).catch(error => actions.notify("error", error instanceof Error ? error.message : String(error)));
  }));
}
