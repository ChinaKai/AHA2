import {icon} from "./icons.js";
import type {MailAttachment, MailDelivery, MailMessage, MailMessageCard} from "./types.js";

function escapeHTML(value: unknown): string {
  return String(value ?? "")
    .replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;").replaceAll("'", "&#039;");
}

export function mailStatusLabel(status: string): string {
  return {
    draft: "草稿",
    pending_approval: "待审批",
    queued: "排队中",
    sending: "发送中",
    sent: "已发送",
    failed: "发送失败",
    dead_letter: "死信",
    rejected: "已拒绝",
    cancelled: "已取消",
  }[status] || status;
}

export function mailStatusClass(status: string): string {
  if (status === "sent") return "sent";
  if (status === "failed" || status === "dead_letter") return "failed";
  if (status === "rejected" || status === "cancelled") return "muted";
  if (status === "sending") return "sending";
  if (status === "queued") return "queued";
  return "pending";
}

export function mailCanCancel(status: string): boolean {
  return ["pending_approval", "queued", "failed", "dead_letter"].includes(status);
}

export function mailCanRetry(status: string): boolean {
  return ["failed", "dead_letter"].includes(status);
}

export function mailCanDecide(status: string): boolean {
  return status === "pending_approval";
}

export function formatMailTime(value?: string): string {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  const pad = (part: number) => String(part).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

export function formatMailBytes(value: number): string {
  const size = Number(value) || 0;
  if (size >= 1 << 20) return `${(size / (1 << 20)).toFixed(size >= 10 << 20 ? 0 : 1)} MiB`;
  if (size >= 1 << 10) return `${Math.round(size / (1 << 10))} KiB`;
  return `${size} B`;
}

export function mailDigestShort(value?: string): string {
  return String(value || "").replace(/^sha256:/, "").slice(0, 12);
}

function recipientLine(label: string, values?: string[]): string {
  const list = (values || []).filter(Boolean);
  return list.length ? `<p><strong>${label}</strong><span>${escapeHTML(list.join(", "))}</span></p>` : "";
}

export function mailAttachmentsHtml(attachments: MailAttachment[] = []): string {
  if (!attachments.length) return `<p class="field-help">无附件</p>`;
  return `<ul class="mail-attachment-list">${attachments.map(attachment => {
    const link = attachment.delivery_mode === "link";
    return `<li><span class="mail-attachment-name">${icon("attachment")}${escapeHTML(attachment.file_name)}<small>${formatMailBytes(attachment.size_bytes)}${link ? " · 受控链接" : ""}</small></span><code title="SHA256">${escapeHTML(attachment.sha256)}</code>${link ? `<small>${attachment.link_max_downloads || 20} 次 / ${attachment.link_download_count || 0} 次已下载</small>` : ""}</li>`;
  }).join("")}</ul>`;
}

function deliveryLabel(delivery?: MailDelivery): string {
  if (!delivery) return "尚未投递";
  const attempts = delivery.attempts ? ` · 尝试 ${delivery.attempts} 次` : "";
  const error = delivery.last_error_code ? ` · ${delivery.last_error_code}` : "";
  return `${mailStatusLabel(delivery.state)}${attempts}${error}`;
}

export interface MailCardOptions {
  readOnly?: boolean;
  showBody?: boolean;
  showActions?: boolean;
}

// Built-in preview samples. They cover every documented server-resolved name
// plus the common build/test/release fields so "预览" never asks for JSON.
export const MAIL_SAMPLE_VARIABLES: Record<string, string> = {
  "task.id": "task_demo_20261106",
  "task.title": "示例任务：邮件预览",
  "task.url": "https://aha.example.com/tasks/task_demo_20261106",
  "project.name": "AHA2",
  "workspace.name": "WORK-AHA2",
  "build.version": "1.0.0-rc.1",
  "build.commit": "b5cd63f",
  "build.branch": "main",
  "build.time": "2026-11-06 15:00",
  "package.name": "aha-web.tar.gz",
  "package.size": "1048576",
  "package.sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
  "test.scope": "整机回归",
  "test.notes": "示例测试说明",
  "test.environment": "Android 14 / Chrome",
  "release.version": "v1.0.0",
  "release.date": "2026-11-06",
  "release.owner": "Owner",
  "agent.name": "AHA Task Agent",
  "owner.name": "Owner",
};

const SERVER_SAMPLE_NAMES = new Set([
  "task.id", "task.title", "task.url", "project.name", "workspace.name",
  "package.name", "package.size", "package.sha256", "agent.name", "owner.name",
]);

const CALLER_VARIABLE_PATTERN = /^(build|test|release|extra)\./;

// Mirrors the backend placeholder parser closely enough for the editor: names
// are exact, no whitespace, and braces must be balanced.
export function mailTemplatePlaceholders(subject: string, text: string, html: string): string[] {
  const names = new Set<string>();
  for (const match of `${subject || ""}\n${text || ""}\n${html || ""}`.matchAll(/\{\{([a-zA-Z0-9_.-]+)\}\}/g)) {
    names.add(match[1]);
  }
  return [...names].sort();
}

// required_variables only ever contains caller supplied namespaces; server
// values are resolved from the Task at send time.
export function mailTemplateVariables(subject: string, text: string, html: string): string[] {
  return mailTemplatePlaceholders(subject, text, html).filter(name => CALLER_VARIABLE_PATTERN.test(name));
}

export function mailPreviewSamples(names: string[]): Record<string, string> {
  const result: Record<string, string> = {};
  for (const name of names) {
    result[name] = MAIL_SAMPLE_VARIABLES[name] ?? `示例-${name}`;
  }
  return result;
}

// Splits preview values into the caller and trusted sample maps the preview
// endpoints expect and reports names that had no built-in sample.
export function mailPreviewVariablePayload(names: string[], samples: Record<string, string> = {}): {
  variables: Record<string, string>;
  sample_variables: Record<string, string>;
  warnings: string[];
} {
  const variables: Record<string, string> = {};
  const sampleVariables: Record<string, string> = {};
  const warnings: string[] = [];
  for (const name of names) {
    let value = samples[name];
    if (value === undefined || value === "") {
      value = MAIL_SAMPLE_VARIABLES[name] ?? "";
      if (!value) {
        value = `示例-${name}`;
        warnings.push(`缺少示例变量：${name}`);
      }
    }
    if (SERVER_SAMPLE_NAMES.has(name)) sampleVariables[name] = value;
    else variables[name] = value;
  }
  return {variables, sample_variables: sampleVariables, warnings};
}

export interface MailPreviewData {
  title?: string;
  subject: string;
  fromName?: string;
  fromAddress?: string;
  to?: string[];
  cc?: string[];
  bcc?: string[];
  bodyText?: string;
  bodyHtml?: string;
  attachments?: MailAttachment[];
  warnings?: string[];
  width?: 600 | 375;
  tab?: "html" | "text";
  samples?: Record<string, string>;
  onSampleRefresh?: (samples: Record<string, string>) => Promise<MailPreviewData>;
  onApprove?: () => Promise<boolean>;
  onReject?: () => Promise<boolean>;
}

function previewWidth(width: number | undefined): 600 | 375 {
  return width === 375 ? 375 : 600;
}

function previewTab(tab: string | undefined): "html" | "text" {
  return tab === "text" ? "text" : "html";
}

export function mailPreviewDialogHtml(data: MailPreviewData): string {
  const width = previewWidth(data.width);
  const tab = previewTab(data.tab);
  const attachments = data.attachments || [];
  const warnings = data.warnings || [];
  const recipient = (values?: string[]) => (values || []).filter(Boolean).join(", ") || "无";
  const sender = data.fromAddress
    ? `${data.fromName ? `${escapeHTML(data.fromName)} · ` : ""}${escapeHTML(data.fromAddress)}`
    : `<span class="field-help">未指定（实际发送时使用 Task 绑定的邮箱账号）</span>`;
  const samples = data.samples ? Object.entries(data.samples).sort(([left], [right]) => left.localeCompare(right)) : [];
  const sampleBlock = samples.length ? `<details class="mail-preview-samples"><summary>示例变量（可编辑）</summary>
    <div class="mail-preview-samples-grid">${samples.map(([name, value]) => `<label>${escapeHTML(name)}<input data-mail-sample-input="${escapeHTML(name)}" value="${escapeHTML(value)}"></label>`).join("")}</div>
    ${data.onSampleRefresh ? `<button type="button" data-mail-preview-refresh>用这些示例值刷新预览</button>` : ""}
    <p class="field-help">示例值只用于预览，不会写入模板或邮件。</p>
  </details>` : "";
  const htmlBody = String(data.bodyHtml || "").trim();
  const textBody = String(data.bodyText || "").trim();
  const htmlPane = htmlBody
    ? `<iframe class="mail-preview-frame" sandbox="" srcdoc="${escapeHTML(data.bodyHtml || "")}" title="HTML 邮件预览"></iframe>`
    : `<div class="mail-preview-empty" data-mail-preview-empty="html">邮件没有 HTML 正文。</div>`;
  const textPane = textBody
    ? `<pre class="mail-preview-body" data-mail-preview-empty="text">${escapeHTML(data.bodyText || "")}</pre>`
    : `<div class="mail-preview-empty" data-mail-preview-empty="text">邮件没有纯文本正文。</div>`;
  return `<div class="dialog-head"><h2>${escapeHTML(data.title || "邮件预览")}</h2><button type="button" data-close class="icon-button" aria-label="关闭">${icon("close")}</button></div>
    <div class="dialog-body mail-preview-dialog-body">
      ${warnings.length ? `<div class="mail-preview-warning" role="alert" data-mail-preview-warning><strong>预览提示</strong><ul>${warnings.map(warning => `<li>${escapeHTML(warning)}</li>`).join("")}</ul></div>` : ""}
      <dl class="mail-preview-envelope">
        <dt>发件人</dt><dd>${sender}</dd>
        <dt>To</dt><dd>${escapeHTML(recipient(data.to))}</dd>
        <dt>CC</dt><dd>${escapeHTML(recipient(data.cc))}</dd>
        ${(data.bcc || []).length ? `<dt>BCC</dt><dd>${escapeHTML(recipient(data.bcc))}</dd>` : ""}
        <dt>主题</dt><dd><strong>${escapeHTML(data.subject || "（无主题）")}</strong></dd>
        <dt>附件</dt><dd>${mailAttachmentsHtml(attachments)}</dd>
      </dl>
      <div class="mail-preview-toolbar">
        <div class="mail-preview-widths" role="group" aria-label="预览宽度">
          <button type="button" data-mail-preview-width="600" class="${width === 600 ? "active" : ""}" aria-pressed="${width === 600}">600px</button>
          <button type="button" data-mail-preview-width="375" class="${width === 375 ? "active" : ""}" aria-pressed="${width === 375}">375px</button>
        </div>
        <div class="mail-preview-tabs" role="tablist" aria-label="预览格式">
          <button type="button" data-mail-preview-tab="html" class="${tab === "html" ? "active" : ""}" role="tab" aria-selected="${tab === "html"}">HTML</button>
          <button type="button" data-mail-preview-tab="text" class="${tab === "text" ? "active" : ""}" role="tab" aria-selected="${tab === "text"}">纯文本</button>
        </div>
      </div>
      <div class="mail-preview-stage" data-mail-preview-stage data-width="${width}">
        <div class="mail-preview-sheet" data-mail-preview-pane="html" ${tab === "html" ? "" : "hidden"}>${htmlPane}</div>
        <div class="mail-preview-sheet" data-mail-preview-pane="text" ${tab === "text" ? "" : "hidden"}>${textPane}</div>
      </div>
      <p class="mail-preview-note">典型邮件客户端效果；Outlook、Gmail 与深色模式下的显示可能不同。</p>
      ${sampleBlock}
      <p class="mail-preview-error" data-mail-preview-error hidden></p>
    </div>
    <div class="dialog-actions">
      ${data.onApprove ? `<button type="button" class="primary" data-mail-preview-decision="approve">批准</button>` : ""}
      ${data.onReject ? `<button type="button" class="danger" data-mail-preview-decision="reject">拒绝</button>` : ""}
      <button type="button" data-close>关闭</button>
    </div>`;
}

function bindPreviewDialog(dialog: HTMLDialogElement, data: MailPreviewData): void {
  const paint = (next: MailPreviewData) => {
    dialog.innerHTML = mailPreviewDialogHtml(next);
    bindPreviewDialog(dialog, next);
  };
  dialog.querySelectorAll("[data-close]").forEach(button => button.addEventListener("click", () => {
    dialog.close();
    dialog.remove();
  }));
  dialog.addEventListener("cancel", () => {
    dialog.close();
    dialog.remove();
  });
  const stage = dialog.querySelector<HTMLElement>("[data-mail-preview-stage]");
  const setWidth = (width: 600 | 375) => {
    if (stage) stage.dataset.width = String(width);
    dialog.querySelectorAll<HTMLElement>("[data-mail-preview-width]").forEach(button => {
      const active = button.dataset.mailPreviewWidth === String(width);
      button.classList.toggle("active", active);
      button.setAttribute("aria-pressed", String(active));
    });
  };
  dialog.querySelectorAll<HTMLElement>("[data-mail-preview-width]").forEach(button => button.addEventListener("click", () => {
    setWidth(button.dataset.mailPreviewWidth === "375" ? 375 : 600);
  }));
  const setTab = (tab: "html" | "text") => {
    dialog.querySelectorAll<HTMLElement>("[data-mail-preview-tab]").forEach(button => {
      const active = button.dataset.mailPreviewTab === tab;
      button.classList.toggle("active", active);
      button.setAttribute("aria-selected", String(active));
    });
    dialog.querySelectorAll<HTMLElement>("[data-mail-preview-pane]").forEach(pane => {
      pane.hidden = pane.dataset.mailPreviewPane !== tab;
    });
  };
  dialog.querySelectorAll<HTMLElement>("[data-mail-preview-tab]").forEach(button => button.addEventListener("click", () => {
    setTab(button.dataset.mailPreviewTab === "text" ? "text" : "html");
  }));
  dialog.querySelector<HTMLButtonElement>("[data-mail-preview-refresh]")?.addEventListener("click", event => {
    const button = event.currentTarget as HTMLButtonElement;
    button.disabled = true;
    const values: Record<string, string> = {};
    dialog.querySelectorAll<HTMLInputElement>("[data-mail-sample-input]").forEach(input => {
      values[input.dataset.mailSampleInput || ""] = input.value;
    });
    void (data.onSampleRefresh?.(values) || Promise.reject(new Error("预览刷新不可用"))).then(next => {
      paint({...next, samples: values, onSampleRefresh: data.onSampleRefresh, onApprove: data.onApprove, onReject: data.onReject});
    }).catch(error => {
      const target = dialog.querySelector<HTMLElement>("[data-mail-preview-error]");
      if (target) {
        target.hidden = false;
        target.textContent = error instanceof Error ? error.message : String(error);
      }
      button.disabled = false;
    });
  });
  const decide = (handler: (() => Promise<boolean>) | undefined) => {
    if (!handler) return;
    dialog.querySelectorAll("[data-close]").forEach(button => (button as HTMLButtonElement).disabled = true);
    void handler().then(ok => {
      if (ok) {
        dialog.close();
        dialog.remove();
      } else {
        dialog.querySelectorAll("[data-close]").forEach(button => (button as HTMLButtonElement).disabled = false);
      }
    }).catch(() => {
      dialog.querySelectorAll("[data-close]").forEach(button => (button as HTMLButtonElement).disabled = false);
    }).finally(() => {
      dialog.querySelectorAll<HTMLButtonElement>("[data-mail-preview-decision]").forEach(button => button.disabled = false);
    });
  };
  dialog.querySelector<HTMLElement>('[data-mail-preview-decision="approve"]')?.addEventListener("click", () => decide(data.onApprove));
  dialog.querySelector<HTMLElement>('[data-mail-preview-decision="reject"]')?.addEventListener("click", () => decide(data.onReject));
  dialog.querySelectorAll<HTMLButtonElement>("[data-mail-preview-decision]").forEach(button => button.addEventListener("click", () => {
    button.disabled = true;
  }));
}

export function openMailPreviewDialog(data: MailPreviewData): HTMLDialogElement {
  const dialog = document.createElement("dialog");
  dialog.className = "wide mail-resource-dialog mail-preview-dialog";
  bindPreviewDialog(dialog, data);
  document.body.appendChild(dialog);
  dialog.showModal();
  return dialog;
}

// One Mail Message always renders as one card, keyed by message id. Status
// changes update that card instead of appending another one.
export function mailCardHtml(card: MailMessageCard, options: MailCardOptions = {}): string {
  const message = card.message;
  const attachments = card.attachments || [];
  const deliveries = card.deliveries || [];
  const latest = deliveries[0];
  const canDecide = !options.readOnly && mailCanDecide(message.status) && options.showActions !== false;
  const canCancel = !options.readOnly && mailCanCancel(message.status) && options.showActions !== false;
  const canRetry = !options.readOnly && mailCanRetry(message.status) && options.showActions !== false;
  const warnings: string[] = [];
  if ((message.bcc || []).length) warnings.push("含 BCC");
  if (attachments.some(attachment => attachment.delivery_mode === "link")) warnings.push("含受控下载链接");
  if (message.status === "sending") warnings.push("发送中，不可取消");
  const actions = canDecide || canCancel || canRetry
    ? `<div class="mail-card-actions">
      ${canDecide ? `<button type="button" data-mail-action="review" data-mail-message="${escapeHTML(message.id)}">查看并审批</button><button type="button" class="primary" data-mail-action="approve" data-mail-message="${escapeHTML(message.id)}">批准</button><button type="button" class="danger" data-mail-action="reject" data-mail-message="${escapeHTML(message.id)}">拒绝</button>` : ""}
      ${canRetry ? `<button type="button" data-mail-action="retry" data-mail-message="${escapeHTML(message.id)}">重试</button>` : ""}
      ${canCancel ? `<button type="button" data-mail-action="cancel" data-mail-message="${escapeHTML(message.id)}">取消</button>` : ""}
    </div>`
    : "";
  const body = options.showBody && (message.body_text || "")
    ? `<details class="mail-card-body"><summary>查看正文</summary><pre>${escapeHTML(message.body_text || "")}</pre></details>`
    : "";
  return `<article class="mail-card ${mailStatusClass(message.status)}" data-mail-card="${escapeHTML(message.id)}">
    <header class="mail-card-head">
      <div><strong>${escapeHTML(message.subject || "（无主题）")}</strong><small>${escapeHTML(message.from_name || message.from_address || "")} · ${formatMailTime(message.created_at)}</small></div>
      <span class="mail-status ${mailStatusClass(message.status)}">${escapeHTML(mailStatusLabel(message.status))}</span>
    </header>
    <div class="mail-card-meta">
      ${recipientLine("收件人", message.to)}
      ${recipientLine("CC", message.cc)}
      ${recipientLine("BCC", message.bcc)}
      <p><strong>账号</strong><span>${escapeHTML(message.account_id)}</span></p>
      ${message.template_id ? `<p><strong>模板</strong><span>${escapeHTML(message.template_id)} v${Number(message.template_version || 0)}</span></p>` : ""}
      <p><strong>审批摘要</strong><span><code>${escapeHTML(mailDigestShort(message.approval_digest))}</code></span></p>
      ${latest ? `<p><strong>投递</strong><span>${escapeHTML(deliveryLabel(latest))}${latest.sent_at ? ` · ${escapeHTML(formatMailTime(latest.sent_at))}` : ""}</span></p>` : ""}
      ${message.rejected_reason ? `<p><strong>拒绝原因</strong><span>${escapeHTML(message.rejected_reason)}</span></p>` : ""}
      ${warnings.length ? `<p class="mail-card-warning"><strong>提示</strong><span>${escapeHTML(warnings.join(" · "))}</span></p>` : ""}
    </div>
    <details class="mail-card-details"><summary>附件与正文</summary>${mailAttachmentsHtml(attachments)}${body}</details>
    ${actions}
  </article>`;
}

// The conversation timeline is informational only. Approval and delivery
// actions live in the Task mail tool panel, so this card never renders
// mailCardHtml or approve/reject/cancel/retry buttons.
export function mailConversationCardHtml(card: MailMessageCard | undefined, _readOnly: boolean): string {
  if (!card) {
    return `<article class="message agent mail-message-card"><header><strong>邮件</strong><span>邮件</span></header><div class="message-bubble"><div class="mail-card-loading">${icon("spinner", true)}正在加载邮件状态…</div></div></article>`;
  }
  const message = card.message;
  // The timeline never renders approval state or actions; both stay in the
  // mail tool panel so there is exactly one place to inspect and decide.
  const notice = message.status === "pending_approval"
    ? "已提交，等待 Owner 审批。请在任务工具栏「邮件」面板中查看和处理。"
    : "邮件状态已更新，请在任务工具栏「邮件」面板中查看和处理。";
  return `<article class="message agent mail-message-card" data-message-category="mail" data-mail-card="${escapeHTML(message.id)}">
    <header><strong>邮件</strong><span>通知</span><time>${escapeHTML(formatMailTime(message.created_at))}</time></header>
    <div class="message-bubble"><div class="mail-timeline-notice"><strong>${escapeHTML(message.subject || "（无主题）")}</strong><span>${escapeHTML(notice)}</span></div></div>
  </article>`;
}
