import {icon} from "./icons.js";
import {renderMarkdown} from "./markdown.js";
import {renderConversationWithOrchestration, visibleAgentText} from "./task_agents.js";
import type {ConversationItem, TaskDetail} from "./types.js";

const messageCollapseChars = 900;
const messagePreviewChars = 220;

function escapeHTML(value: unknown): string {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

function messageTime(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  const pad = (part: number) => String(part).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;
}

function renderAttachments(item: ConversationItem): string {
  const values = Array.isArray(item.payload?.attachments) ? item.payload.attachments : [];
  if (!values.length) return "";
  const attachments = values.filter(value => value && typeof value === "object") as Array<Record<string, unknown>>;
  return `<div class="message-attachments">${attachments.map(attachment => {
    const id = String(attachment.id || "");
    const name = String(attachment.name || "attachment");
    const mediaType = String(attachment.media_type || "application/octet-stream");
    const size = Number(attachment.size || 0);
    const url = `/api/v1/tasks/${encodeURIComponent(item.task_id)}/attachments/${encodeURIComponent(id)}`;
    if (["image/png", "image/jpeg", "image/gif", "image/webp"].includes(mediaType)) {
      return `<button type="button" class="message-image" data-image-preview="${url}" data-image-name="${escapeHTML(name)}" aria-label="预览 ${escapeHTML(name)}"><img src="${url}" loading="lazy" alt="${escapeHTML(name)}"><span>${escapeHTML(name)}</span></button>`;
    }
    return `<a class="message-file" href="${url}" download>${icon("attachment")}<span><strong>${escapeHTML(name)}</strong><small>${size > 0 ? `${Math.max(1, Math.round(size / 1024))} KB` : mediaType}</small></span></a>`;
  }).join("")}</div>`;
}

function imagePreviewDialog(): string {
  return `<dialog id="image-preview-dialog" class="image-preview-dialog"><header><strong data-image-preview-title>图片预览</strong><div><a class="primary image-preview-download" data-image-preview-download download>${icon("attachment")}下载</a><button type="button" class="icon-button" data-image-preview-close aria-label="关闭">${icon("close")}</button></div></header><div class="image-preview-stage"><img data-image-preview-image alt=""></div></dialog>`;
}

export function readableConversationText(item: Pick<ConversationItem, "category" | "summary">): string {
  const text = visibleAgentText(item.summary);
  if (item.category === "update" && /\?{6,}/.test(text)) {
    return "该历史进度消息在旧版中发生编码损坏，原文无法恢复。";
  }
  return text;
}

function renderConversationItem(item: ConversationItem): string {
  const time = messageTime(item.created_at);
  const routeKind = item.route_kind || "";
  const routed = ["assignment", "agent_result", "agent_error", "main_followup", "session_control"].includes(routeKind) || ["agent_assignment", "agent_result_routed", "agent_error_routed", "agent_main_followup", "backend_session_compacted", "backend_session_reset"].includes(item.kind);
  const user = item.kind === "user_message";
  const update = !user && item.category === "update";
  const tool = !user && item.category === "tool";
  const error = !user && item.category === "error";
  const sender = item.from_agent_id || item.agent_id || (user ? "owner" : "main");
  const badge = error ? "错误" : tool ? "工具" : routed ? "路由" : update ? "Update" : "";
  const text = readableConversationText(item);
  const characters = Array.from(text);
  const characterCount = characters.length;
  const collapsible = characterCount > messageCollapseChars;
  const preview = collapsible ? `${Array.from(text.replace(/\s+/g, " ")).slice(0, messagePreviewChars - 1).join("").trim()}…` : text;
  const payload = item.payload || {};
  const output = String(payload.output_tail || "");
  return `<article class="message ${user ? "user" : "agent"} ${update ? "agent-update-message" : ""} ${tool ? "agent-tool-message" : ""} ${routed ? "agent-routed-message" : ""} ${error ? "agent-error-message" : ""}" data-message-chars="${characterCount}" data-copy-message-source="${escapeHTML(text)}"><header><strong>${escapeHTML(sender)}</strong>${badge ? `<span>${badge}</span>` : ""}<time>${time}</time><button type="button" data-copy-message class="message-copy icon-button" title="复制">${icon("copy")}</button></header><div class="message-bubble"><div class="message-text markdown-body message-preview">${renderMarkdown(preview)}</div>${collapsible ? `<div class="message-text markdown-body message-full-text" hidden>${renderMarkdown(text)}</div><button type="button" data-toggle-message class="message-toggle">展开 · ${characterCount.toLocaleString()}字符</button>` : ""}${renderAttachments(item)}${output ? `<details class="message-output"><summary>查看输出摘要</summary><pre>${escapeHTML(output)}</pre></details>` : ""}</div></article>`;
}

function toolLifecycleKey(item: ConversationItem): string {
  if (item.category !== "tool" || !["agent_command_started", "agent_command_finished"].includes(item.kind)) return "";
  const payload = item.payload || {};
  const lifecycleID = String(payload.tool_call_id || payload.tool_use_id || "").trim();
  const scope = `${item.task_id}:${item.turn_id || item.round_id || ""}:${item.agent_id || item.from_agent_id || ""}`;
  if (lifecycleID) return `${scope}:id:${lifecycleID}`;
  const command = String(payload.command || item.summary || "").trim();
  return command ? `${scope}:command:${command}` : "";
}

// Backends emit separate lifecycle events for one tool call. Keep the started
// row visible while it is running, then fold the matching completion payload
// into that row. The command fallback also repairs history recorded before
// tool-call IDs were persisted.
export function coalesceToolConversationItems(items: ConversationItem[]): ConversationItem[] {
  const result: ConversationItem[] = [];
  const pending = new Map<string, number[]>();
  for (const item of items) {
    const key = toolLifecycleKey(item);
    if (key && item.kind === "agent_command_started") {
      const indices = pending.get(key) || [];
      indices.push(result.length);
      pending.set(key, indices);
      result.push(item);
      continue;
    }
    if (key && item.kind === "agent_command_finished") {
      const indices = pending.get(key);
      const index = indices?.pop();
      if (index !== undefined) {
        if (!indices?.length) pending.delete(key);
        const started = result[index];
        result[index] = {
          ...started,
          kind: item.kind,
          summary: item.summary && item.summary !== item.kind ? item.summary : started.summary,
          payload: {...(started.payload || {}), ...(item.payload || {})},
        };
        continue;
      }
    }
    result.push(item);
  }
  return result;
}

export function renderConversationList(
  items: ConversationItem[],
  detail: TaskDetail | null,
  hasMore: boolean,
): string {
  const history = (renderConversationWithOrchestration(coalesceToolConversationItems(items), detail, renderConversationItem) || `<div class="empty">暂无符合筛选条件的记录。</div>`) + imagePreviewDialog();
  return `${hasMore ? `<button type="button" id="load-older-conversation" class="load-older">加载更早记录</button>` : ""}${history || `<div class="empty">暂无符合筛选条件的记录。</div>`}`;
}
