import {icon} from "./icons.js";
import {tr} from "./i18n.js";
import type {ConversationCategory, TaskDetail} from "./types.js";

function escapeHTML(value: unknown): string {
  return String(value ?? "")
    .replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;").replaceAll("'", "&#039;");
}

// The server enforces these values; the client only surfaces the same limits
// and refuses obviously oversized selections before starting an upload.
export const TASK_ATTACHMENT_MAX_COUNT = 8;
export const TASK_ATTACHMENT_MAX_BYTES = 200 * 1024 * 1024;

export function taskAttachmentLimitHint(
  maxCount = TASK_ATTACHMENT_MAX_COUNT,
  maxBytes = TASK_ATTACHMENT_MAX_BYTES,
): string {
  return `每条消息最多 ${maxCount} 个，单文件 ≤${Math.round(maxBytes / (1024 * 1024))}MiB`;
}

export interface TaskAttachmentCandidate {
  name?: string;
  size: number;
}

export function taskAttachmentValidationError(
  existingCount: number,
  files: TaskAttachmentCandidate[],
  maxCount = TASK_ATTACHMENT_MAX_COUNT,
  maxBytes = TASK_ATTACHMENT_MAX_BYTES,
): string {
  if (existingCount + files.length > maxCount) return `每条消息最多发送 ${maxCount} 个附件`;
  const oversized = files.filter(file => file.size > maxBytes).map(file => file.name || "未命名文件");
  if (oversized.length) {
    return `以下附件超过 ${Math.round(maxBytes / (1024 * 1024))}MiB，未上传：${oversized.join("、")}`;
  }
  return "";
}

function statusLabel(status: string): string {
  const labels: Record<string, string> = {
    idle: "空闲", active: "执行中", running: "执行中", starting: "启动中",
    preparing: "准备中", queued: "排队", failed: "失败", interrupted: "已中断",
  };
  return labels[status] || status;
}

export function renderComposerAgentOptions(detail: TaskDetail, selectedAgentID: string): string {
  const agents = detail.agents || [];
  if (agents.length === 0) {
    const agentID = selectedAgentID || "main";
    return `<option value="${escapeHTML(agentID)}" selected>● ${escapeHTML(agentID)} · ${tr("加载中")}</option>`;
  }
  return agents.map(agent => {
    const unread = agent.unread_count ? ` · ${agent.unread_count > 99 ? "99+" : agent.unread_count} ${tr("未读")}` : "";
    const dot = ["failed", "interrupted"].includes(agent.status) ? "○" : "●";
    return `<option value="${escapeHTML(agent.agent_id)}" ${agent.agent_id === selectedAgentID ? "selected" : ""}>${dot} ${escapeHTML(agent.agent_id)} · ${escapeHTML(tr(statusLabel(agent.status)))}${unread}</option>`;
  }).join("");
}

export function renderComposerTools(
  detail: TaskDetail,
  selectedAgentID: string,
  categories: Record<ConversationCategory, boolean>,
  loadedCount: number,
): string {
  const options = renderComposerAgentOptions(detail, selectedAgentID);
  const labels: Record<ConversationCategory, string> = {
    chat: "对话", update: "Agent Update", tool: "工具调用", error: "错误",
  };
  const enabled = Object.values(categories).filter(Boolean).length;
  const filters = (Object.keys(labels) as ConversationCategory[]).map(category =>
    `<button type="button" data-conversation-category="${category}" class="${categories[category] ? "active" : ""}"><span>${categories[category] ? "✓" : ""}</span>${labels[category]}</button>`
  ).join("");
  return `<div class="composer-target-wrap">
    <select id="composer-agent" aria-label="发送目标">${options}</select>
    <button type="button" data-agent-config="${escapeHTML(selectedAgentID)}" class="icon-button composer-agent-config" title="编辑 ${escapeHTML(selectedAgentID)} 配置">${icon("edit")}</button>
  </div>
  <details class="conversation-filter-popover">
    <summary class="conversation-filter-trigger" title="筛选消息">${icon("filter")}<small>${enabled}/4</small></summary>
    <div class="conversation-filter-menu"><header><strong>消息筛选</strong><small id="conversation-filter-loaded">${loadedCount} 条已加载</small></header>${filters}</div>
  </details>`;
}
