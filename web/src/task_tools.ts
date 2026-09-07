import {icon} from "./icons.js";
import {renderHardwarePanel} from "./hardware_panel.js";
import {renderTaskMemory} from "./task_agents.js";
import type {TaskDetail} from "./types.js";

export type TaskTool = "context" | "hardware" | "browser" | "memory";
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
  {id: "context", label: "Context", iconName: "context"},
  {id: "hardware", label: "硬件调试", iconName: "hardware"},
  {id: "browser", label: "浏览器", iconName: "browser"},
  {id: "memory", label: "Task Memory", iconName: "knowledge"},
];

function escapeHTML(value: unknown): string {
  return String(value ?? "")
    .replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;").replaceAll("'", "&#039;");
}

export function renderTaskToolButtons(active: TaskTool | ""): string {
  return tools.map(tool => `<button type="button" data-task-tool="${tool.id}" class="icon-button task-tool-button ${active === tool.id ? "active" : ""}" title="${tool.label}">${icon(tool.iconName)}</button>`).join("");
}

export function taskToolTitle(tool: TaskTool | ""): string {
  return tools.find(item => item.id === tool)?.label || "Task 工具";
}

export function renderTaskToolContent(
  tool: TaskTool | "",
  detail: TaskDetail,
  contextHTML: string,
): string {
  if (tool === "context") return contextHTML;
  if (tool === "memory") return `<div class="task-memory-tool">${renderTaskMemory(detail.memory)}</div>`;
  if (tool === "hardware") return renderHardwarePanel(detail);
  if (tool === "browser") {
    const label = taskToolTitle(tool);
    return `<div class="task-tool-placeholder">${icon(tool)}<h3>${escapeHTML(label)}</h3><p>该工具将在后续版本接入。</p></div>`;
  }
  return "";
}

export function renderTaskToolPanel(
  tool: TaskTool | "",
  detail: TaskDetail,
  agentID: string,
  contextHTML: string,
  mode: TaskToolMode,
): string {
  const switchLabel = mode === "split" ? "全屏" : "小窗";
  const switchIcon = mode === "split" ? "expand" : "panel";
  return `<aside class="task-tool-panel ${tool ? "open" : ""} ${mode}">
    <div id="task-tool-resizer" class="task-tool-resizer" role="separator" aria-label="调整工具面板宽度" aria-orientation="vertical" aria-valuemin="30" aria-valuemax="70" tabindex="0" title="拖动调整宽度，双击恢复默认"></div>
    <header class="task-tool-panel-head"><div><h3>${escapeHTML(taskToolTitle(tool))}</h3><small>${escapeHTML(agentID)}</small></div><div class="task-tool-panel-actions"><button type="button" id="toggle-task-tool-mode" class="task-tool-mode-toggle" title="切换为${switchLabel}" aria-label="切换为${switchLabel}">${icon(switchIcon)}<span>${switchLabel}</span></button><button type="button" id="close-task-tool" class="icon-button" title="关闭">${icon("close")}</button></div></header>
    <div id="task-tool-panel-body">${renderTaskToolContent(tool, detail, contextHTML)}</div>
  </aside>`;
}
