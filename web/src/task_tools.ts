import {icon} from "./icons.js";
import {renderHardwareGroupSwitcher, renderHardwarePanel} from "./hardware_panel.js";
import {renderDesktopHeaderControls, renderDesktopPanel} from "./desktop_panel.js";
import {renderTaskMemory} from "./task_agents.js";
import type {TaskDetail} from "./types.js";

export type TaskTool = "channel" | "context" | "hardware" | "browser" | "memory";
export type TaskToolMode = "split" | "fullscreen";

export const TASK_TOOL_DEFAULT_WIDTH = 42;

export function normalizeTaskToolMode(value: unknown): TaskToolMode {
  return value === "fullscreen" ? "fullscreen" : "split";
}

export function normalizeTaskToolWidth(value: unknown): number {
  const width = Number(value);
  if (!Number.isFinite(width)) return TASK_TOOL_DEFAULT_WIDTH;
  return Math.min(70, Math.max(30, Math.round(width * 10) / 10));
}

const tools: Array<{id: TaskTool; label: string; iconName: string}> = [
  {id: "channel", label: "渠道", iconName: "bot"},
  {id: "context", label: "Context", iconName: "context"},
  {id: "hardware", label: "硬件调试", iconName: "hardware"},
  {id: "browser", label: "共享控制", iconName: "browser"},
  {id: "memory", label: "Task Memory", iconName: "knowledge"},
];

function escapeHTML(value: unknown): string {
  return String(value ?? "")
    .replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;").replaceAll("'", "&#039;");
}

export function renderTaskToolButtons(
  active: TaskTool | "",
  options: {includeChannel?: boolean; channelConnected?: boolean; channelLabel?: string} = {},
): string {
  return tools.filter(tool => tool.id !== "channel" || options.includeChannel !== false).map(tool => {
    const connected = tool.id === "channel" && options.channelConnected;
    const label = tool.id === "channel" && options.channelLabel ? options.channelLabel : tool.label;
    return `<button type="button" data-task-tool="${tool.id}" class="icon-button task-tool-button ${active === tool.id ? "active" : ""} ${connected ? "connected" : ""}" title="${escapeHTML(label)}" aria-label="${escapeHTML(label)}">${icon(tool.iconName)}</button>`;
  }).join("");
}

export function taskToolTitle(tool: TaskTool | ""): string {
  return tools.find(item => item.id === tool)?.label || "Task 工具";
}

export function renderTaskToolContent(
  tool: TaskTool | "",
  detail: TaskDetail,
  contextHTML: string,
  channelHTML = "",
): string {
  if (tool === "channel") return channelHTML;
  if (tool === "context") return contextHTML;
  if (tool === "memory") return `<div class="task-memory-tool">${renderTaskMemory(detail.memory)}</div>`;
  if (tool === "hardware") return renderHardwarePanel(detail);
  if (tool === "browser") return renderDesktopPanel(detail);
  return "";
}

export function renderTaskToolPanel(
  tool: TaskTool | "",
  detail: TaskDetail,
  agentID: string,
  contextHTML: string,
  mode: TaskToolMode,
  channelHTML = "",
): string {
  const switchLabel = mode === "split" ? "全屏" : "小窗";
  const switchIcon = mode === "split" ? "expand" : "panel";
  const hardwareSwitcher = tool === "hardware" ? renderHardwareGroupSwitcher(detail) : "";
  if (tool === "browser" && !detail.task.read_only) {
    const header = `<header class="task-tool-panel-head desktop-task-header"><div class="task-tool-panel-title"><h3>共享控制</h3></div>
      ${renderDesktopHeaderControls()}<div class="task-tool-panel-actions desktop-page-actions" role="group" aria-label="工具面板布局与关闭">
      <button type="button" id="toggle-task-tool-mode" class="icon-button" title="切换为${switchLabel}" aria-label="切换为${switchLabel}">${icon(switchIcon)}</button>
      <button type="button" id="close-task-tool" class="icon-button" title="关闭" aria-label="关闭">${icon("close")}</button></div></header>`;
    return `<aside class="task-tool-panel open ${mode} desktop-task-panel"><div id="task-tool-resizer" class="task-tool-resizer" role="separator" aria-label="调整工具面板宽度" aria-orientation="vertical" aria-valuemin="30" aria-valuemax="70" tabindex="0"></div>${renderDesktopPanel(detail, header)}</aside>`;
  }
  return `<aside class="task-tool-panel ${tool ? "open" : ""} ${mode}">
    <div id="task-tool-resizer" class="task-tool-resizer" role="separator" aria-label="调整工具面板宽度" aria-orientation="vertical" aria-valuemin="30" aria-valuemax="70" tabindex="0" title="拖动调整宽度，双击恢复默认"></div>
    <header class="task-tool-panel-head"><div class="task-tool-panel-title"><div class="task-tool-panel-title-row"><h3>${escapeHTML(taskToolTitle(tool))}</h3>${hardwareSwitcher}</div><small>${escapeHTML(agentID)}</small></div><div class="task-tool-panel-actions"><button type="button" id="toggle-task-tool-mode" class="task-tool-mode-toggle" title="切换为${switchLabel}" aria-label="切换为${switchLabel}">${icon(switchIcon)}<span>${switchLabel}</span></button><button type="button" id="close-task-tool" class="icon-button" title="关闭">${icon("close")}</button></div></header>
    <div id="task-tool-panel-body">${renderTaskToolContent(tool, detail, contextHTML, channelHTML)}</div>
  </aside>`;
}
