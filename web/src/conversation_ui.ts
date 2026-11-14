import {icon} from "./icons.js";
import {renderMarkdown} from "./markdown.js";
import {conversationItemsWithContent, renderConversationWithOrchestration, visibleAgentText} from "./task_agents.js";
import {stableTextSignature} from "./ui_helpers.js";
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
  const routed = ["assignment", "agent_result", "agent_error", "main_followup", "collaboration_fallback", "collaboration_result", "channel_reply", "session_control"].includes(routeKind) || ["agent_assignment", "agent_result_routed", "agent_error_routed", "agent_main_followup", "agent_collaboration_fallback", "collaboration_result_routed", "collaboration_error_routed", "agent_channel_reply", "backend_session_compacted", "backend_session_reset"].includes(item.kind);
  const user = item.kind === "user_message";
  const update = !user && item.category === "update";
  const reasoning = update && item.kind === "agent_reasoning_update";
  const tool = !user && item.category === "tool";
  const error = !user && item.category === "error";
  const payload = item.payload || {};
  const channelContext = payload.channel_context && typeof payload.channel_context === "object" ? payload.channel_context as Record<string, unknown> : {};
  const actor = channelContext.actor && typeof channelContext.actor === "object" ? channelContext.actor as Record<string, unknown> : {};
  const actorName = String(actor.display_name || "").trim();
  const chatName = String(channelContext.chat_display_name || "").trim();
  const sender = actorName ? `${actorName}${chatName ? ` · ${chatName}` : ""}` : item.from_agent_id || item.agent_id || (user ? "owner" : "main");
  const badge = error ? "错误" : tool ? "工具" : routed ? "路由" : actorName ? "渠道" : reasoning ? "思考" : update ? "Update" : "";
  const text = readableConversationText(item);
  const characters = Array.from(text);
  const characterCount = characters.length;
  const collapsible = characterCount > messageCollapseChars;
  const preview = collapsible ? `${Array.from(text.replace(/\s+/g, " ")).slice(0, messagePreviewChars - 1).join("").trim()}…` : text;
  const output = String(payload.output_tail || "");
  const signature = stableTextSignature([item.kind, item.category, item.route_kind, item.summary, payload, item.created_at, sender]);
  return `<article data-message-signature="${signature}" class="message ${user ? "user" : "agent"} ${update ? "agent-update-message" : ""} ${reasoning ? "agent-reasoning-message" : ""} ${tool ? "agent-tool-message" : ""} ${routed ? "agent-routed-message" : ""} ${error ? "agent-error-message" : ""}" data-message-id="${escapeHTML(item.id)}" data-message-chars="${characterCount}" data-copy-message-source="${escapeHTML(text)}"><header><strong>${escapeHTML(sender)}</strong>${badge ? `<span>${badge}</span>` : ""}<time>${time}</time><button type="button" data-copy-message class="message-copy icon-button" title="复制">${icon("copy")}</button></header><div class="message-bubble"><div class="message-text markdown-body message-preview">${renderMarkdown(preview)}</div>${collapsible ? `<div class="message-text markdown-body message-full-text" hidden>${renderMarkdown(text)}</div><button type="button" data-toggle-message class="message-toggle">展开 · ${characterCount.toLocaleString()}字符</button>` : ""}${renderAttachments(item)}${output ? `<details class="message-output"><summary>查看输出摘要</summary><pre>${escapeHTML(output)}</pre></details>` : ""}</div></article>`;
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

// The rows the conversation actually renders. Tool lifecycles are folded into
// one row and delivered channel replies are hidden by their status card, so
// these are also the only rows that can be measured or windowed.
export function conversationRenderableItems(items: ConversationItem[]): ConversationItem[] {
  return conversationItemsWithContent(coalesceToolConversationItems(items));
}

export function conversationItemKeys(items: ConversationItem[]): string[] {
  return conversationRenderableItems(items).map(item => item.id);
}

export interface ConversationWindowOptions {
  heights: ReadonlyMap<string, number>;
  scrollTop: number;
  clientHeight: number;
  /** Height of the "load earlier" row above the items, including its margin. */
  headerHeight?: number;
  /** Padding at the top of the scroll container. */
  paddingTop?: number;
  overscanPx?: number;
  estimateHeight?: number;
  /** The reader is at (or is being sent to) the newest message. */
  atBottom?: boolean;
  /** First item currently in view; the window must keep it rendered. */
  anchorKey?: string;
  /** Focused message; recycling must not drop its focus. */
  pinnedKey?: string;
}

export interface ConversationWindow {
  /** First rendered item index (inclusive). */
  start: number;
  /** Last rendered item index (exclusive). */
  end: number;
  topHeight: number;
  bottomHeight: number;
  totalHeight: number;
}

const conversationWindowMaxSpan = 240;

// Window math for the variable-height message list. Item offsets come from the
// measured-height cache with an estimate for rows that have never been on
// screen; spacers stand in for everything outside [start, end), so the scroll
// height stays continuous while only a bounded set of rows is in the DOM.
export function conversationWindow(keys: string[], options: ConversationWindowOptions): ConversationWindow {
  const count = keys.length;
  if (!count) return {start: 0, end: 0, topHeight: 0, bottomHeight: 0, totalHeight: 0};
  const estimate = Number.isFinite(options.estimateHeight) && (options.estimateHeight || 0) > 0
    ? options.estimateHeight as number : 96;
  const overscan = Math.max(0, options.overscanPx ?? 600);
  const clientHeight = Math.max(0, options.clientHeight || 0);
  const prefix = new Array<number>(count + 1);
  prefix[0] = 0;
  for (let index = 0; index < count; index += 1) {
    const measured = options.heights.get(keys[index]);
    prefix[index + 1] = prefix[index] + (Number.isFinite(measured) && (measured as number) >= 0 ? measured as number : estimate);
  }
  const totalHeight = prefix[count];
  const chrome = Math.max(0, (options.headerHeight || 0) + (options.paddingTop || 0));
  const viewTop = options.atBottom
    ? Math.max(0, totalHeight - clientHeight)
    : Math.max(0, (options.scrollTop || 0) - chrome);
  const viewBottom = viewTop + clientHeight;
  // First item whose bottom edge is past the overscanned top of the viewport.
  const startAt = (offset: number): number => {
    let low = 0;
    let high = count;
    while (low < high) {
      const middle = (low + high) >> 1;
      if (prefix[middle + 1] > offset) high = middle;
      else low = middle + 1;
    }
    return Math.min(count - 1, low);
  };
  // First item whose top edge is at/after the overscanned bottom edge.
  const endAt = (offset: number): number => {
    let low = 1;
    let high = count;
    while (low < high) {
      const middle = (low + high) >> 1;
      if (prefix[middle] >= offset) high = middle;
      else low = middle + 1;
    }
    return low;
  };
  let start = startAt(viewTop - overscan);
  let end = Math.max(start + 1, endAt(viewBottom + overscan));
  for (const key of [options.anchorKey, options.pinnedKey]) {
    if (!key) continue;
    const index = keys.indexOf(key);
    // Keep the anchor/pinned row rendered. The span cap protects the DOM bound
    // if focus or the scrollbar sits far outside the viewport.
    if (index < start && end - index <= conversationWindowMaxSpan) start = index;
    else if (index >= end && index + 1 - start <= conversationWindowMaxSpan) end = index + 1;
  }
  return {
    start,
    end,
    topHeight: prefix[start],
    bottomHeight: totalHeight - prefix[end],
    totalHeight,
  };
}

function conversationSpacer(position: "top" | "bottom", height: number): string {
  if (height <= 0) return "";
  return `<div class="conversation-spacer" data-ui-key="conversation-spacer-${position}" aria-hidden="true" style="height:${Math.round(height * 100) / 100}px"></div>`;
}

export function renderConversationList(
  items: ConversationItem[],
  detail: TaskDetail | null,
  hasMore: boolean,
  windowOptions?: ConversationWindowOptions,
): string {
  const renderable = conversationRenderableItems(items);
  const window = windowOptions
    ? conversationWindow(renderable.map(item => item.id), windowOptions)
    : null;
  const visible = window ? renderable.slice(window.start, window.end) : renderable;
  const history = (renderConversationWithOrchestration(visible, detail, renderConversationItem) || `<div class="empty">暂无符合筛选条件的记录。</div>`) + imagePreviewDialog();
  return `${hasMore ? `<button type="button" id="load-older-conversation" class="load-older">加载更早记录</button>` : ""}${window ? conversationSpacer("top", window.topHeight) : ""}${history}${window ? conversationSpacer("bottom", window.bottomHeight) : ""}`;
}
