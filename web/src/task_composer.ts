import {icon} from "./icons.js";
import type {ConversationCategory, TaskDetail} from "./types.js";

function escapeHTML(value: unknown): string {
  return String(value ?? "")
    .replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;").replaceAll("'", "&#039;");
}

function statusLabel(status: string): string {
  const labels: Record<string, string> = {
    idle: "空闲", active: "执行中", running: "执行中", starting: "启动中",
    preparing: "准备中", queued: "排队", failed: "失败", interrupted: "已中断",
  };
  return labels[status] || status;
}

export function renderComposerAgentOptions(detail: TaskDetail, selectedAgentID: string): string {
  return (detail.agents || []).map(agent => {
    const unread = agent.unread_count ? ` · ${agent.unread_count > 99 ? "99+" : agent.unread_count}未读` : "";
    const dot = ["failed", "interrupted"].includes(agent.status) ? "○" : "●";
    return `<option value="${escapeHTML(agent.agent_id)}" ${agent.agent_id === selectedAgentID ? "selected" : ""}>${dot} ${escapeHTML(agent.agent_id)} · ${escapeHTML(statusLabel(agent.status))}${unread}</option>`;
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
