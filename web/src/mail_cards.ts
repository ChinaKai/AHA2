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
      ${canDecide ? `<button type="button" class="primary" data-mail-action="approve" data-mail-message="${escapeHTML(message.id)}">批准</button><button type="button" class="danger" data-mail-action="reject" data-mail-message="${escapeHTML(message.id)}">拒绝</button>` : ""}
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

export function mailConversationCardHtml(card: MailMessageCard | undefined, readOnly: boolean): string {
  if (!card) {
    return `<article class="message agent mail-message-card"><header><strong>邮件</strong><span>邮件</span></header><div class="message-bubble"><div class="mail-card-loading">${icon("spinner", true)}正在加载邮件状态…</div></div></article>`;
  }
  return `<article class="message agent mail-message-card" data-message-category="mail" data-mail-card="${escapeHTML(card.message.id)}">
    <header><strong>邮件</strong><span>${escapeHTML(mailStatusLabel(card.message.status))}</span><time>${escapeHTML(formatMailTime(card.message.created_at))}</time></header>
    <div class="message-bubble">${mailCardHtml(card, {readOnly, showBody: true})}</div>
  </article>`;
}
