import {icon} from "./icons.js";
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
  const text = visibleAgentText(item.summary);
  const characters = Array.from(text);
  const characterCount = characters.length;
  const collapsible = characterCount > messageCollapseChars;
  const preview = collapsible ? `${Array.from(text.replace(/\s+/g, " ")).slice(0, messagePreviewChars - 1).join("").trim()}…` : text;
  const payload = item.payload || {};
  const output = String(payload.output_tail || "");
  return `<article class="message ${user ? "user" : "agent"} ${update ? "agent-update-message" : ""} ${tool ? "agent-tool-message" : ""} ${routed ? "agent-routed-message" : ""} ${error ? "agent-error-message" : ""}" data-message-chars="${characterCount}"><header><strong>${escapeHTML(sender)}</strong>${badge ? `<span>${badge}</span>` : ""}<time>${time}</time><button type="button" data-copy-message class="message-copy icon-button" title="复制">${icon("copy")}</button></header><div class="message-bubble"><div class="message-text message-preview">${escapeHTML(preview).replaceAll("\n", "<br>")}</div>${collapsible ? `<div class="message-text message-full-text" hidden>${escapeHTML(text).replaceAll("\n", "<br>")}</div><button type="button" data-toggle-message class="message-toggle">展开 · ${characterCount.toLocaleString()}字符</button>` : ""}${output ? `<details class="message-output"><summary>查看输出摘要</summary><pre>${escapeHTML(output)}</pre></details>` : ""}</div></article>`;
}

export function renderConversationList(
  items: ConversationItem[],
  detail: TaskDetail | null,
  hasMore: boolean,
): string {
  const history = renderConversationWithOrchestration(items, detail, renderConversationItem);
  return `${hasMore ? `<button type="button" id="load-older-conversation" class="load-older">加载更早记录</button>` : ""}${history || `<div class="empty">暂无符合筛选条件的记录。</div>`}`;
}
