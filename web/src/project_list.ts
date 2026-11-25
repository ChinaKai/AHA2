import {api} from "./api.js";
import {currentLocale, t} from "./i18n.js";
import {icon} from "./icons.js";
import type {Project, Task, Workspace} from "./types.js";

interface WorkspaceEntry {
  escapeHTML: (value: string) => string;
  workspaceEntryHTML: (className: string, options: {
    workspace?: string; project: string; reason: string; label?: string; content?: string; detailsID?: string;
  }) => string;
}

const copy = {
  "zh-CN": {
    choose: "选择工作区",
    noWorkspaces: "此项目还没有工作区。",
    archived: "该工作区已随渠道归档。",
    readOnly: "只读",
    readOnlyReason: "该工作区是其他设备的只读副本。",
    remote: "远程",
    "health.ready": "正常",
    "health.degraded": "部分可用",
    "health.error": "异常",
    "health.unknown": "状态未知",
    healthErrorReason: "工作区检测失败，请在项目详情刷新状态。",
    activeTasks: "{count} 个活动任务",
    taskCountUnavailable: "活动任务数暂不可用",
    lastUsed: "上次打开",
  },
  "en-US": {
    choose: "Choose a workspace",
    noWorkspaces: "This project has no workspaces yet.",
    archived: "This workspace was archived with its channel.",
    readOnly: "Read-only",
    readOnlyReason: "This workspace is a read-only copy from another device.",
    remote: "Remote",
    "health.ready": "Ready",
    "health.degraded": "Degraded",
    "health.error": "Error",
    "health.unknown": "Unknown health",
    healthErrorReason: "The workspace check failed. Refresh its status in the project details.",
    activeTasks: "Active tasks: {count}",
    taskCountUnavailable: "Active task count unavailable",
    lastUsed: "Last opened",
  },
};

function label(key: keyof typeof copy["en-US"], values: Record<string, number> = {}): string {
  return copy[currentLocale()][key].replace(/\{(\w+)\}/g, (_, name) => String(values[name] ?? ""));
}

function projectWorkspaceReason(workspace: Workspace): string {
  if (workspace.channel_retired) return label("archived");
  if (workspace.read_only) return label("readOnlyReason");
  if (workspace.health === "error") return label("healthErrorReason");
  if (!workspace.root_path) return t("workspaceViewer.workspaceUnavailable");
  return "";
}

function projectWorkspaceStatus(workspace: Workspace): string {
  return [workspace.locality === "remote" ? label("remote") : "",
    workspace.read_only || workspace.channel_retired ? label("readOnly") : ""].filter(Boolean).join(" · ");
}

function projectWorkspaceAction(project: Project, workspaces: Workspace[], {escapeHTML, workspaceEntryHTML}: WorkspaceEntry): string {
  const open = t("workspaceViewer.open");
  if (workspaces.length > 1) {
    return `<button type="button" class="icon-button" data-open-project-workspaces="${escapeHTML(project.id)}" aria-haspopup="dialog" title="${open}" aria-label="${open}">${icon("folder")}</button>`;
  }
  const workspace = workspaces[0];
  const reason = project.channel_retired ? label("archived")
    : workspace ? projectWorkspaceReason(workspace) : label("noWorkspaces");
  return workspaceEntryHTML("icon-button project-workspace-entry", {
    workspace: workspace?.id, project: project.id, reason,
  });
}

export function showProjectWorkspaces(project: Project, workspaces: Workspace[],
  {escapeHTML, workspaceEntryHTML}: WorkspaceEntry, bindWorkspaceEntries: (root: ParentNode) => void,
): void {
  const dialog = document.querySelector<HTMLDialogElement>("#project-workspace-dialog");
  if (!dialog) return;
  const choices = dialog.querySelector<HTMLElement>("[data-project-workspace-choices]")!;
  let last = "";
  try { last = localStorage.getItem(`aha2.projectWorkspace.${project.id}`) || ""; } catch {}
  choices.innerHTML = workspaces.filter(item => item.project_id === project.id).map(workspace => {
    const recent = last === workspace.id;
    const health = workspace.health === "ready" || workspace.health === "degraded" || workspace.health === "error"
      ? workspace.health : "unknown";
    const status = [label(`health.${health}`), projectWorkspaceStatus(workspace)].filter(Boolean).join(" · ");
    const reason = project.channel_retired ? label("archived") : projectWorkspaceReason(workspace);
    const detailsID = `project-workspace-details-${encodeURIComponent(workspace.id)}`;
    return workspaceEntryHTML(`project-workspace-option${recent ? " recent" : ""}`, {
      workspace: workspace.id, project: project.id, reason, label: workspace.name, detailsID,
      content: `<strong>${escapeHTML(workspace.name)}</strong>
        <span id="${detailsID}">${escapeHTML(status)} · <span data-project-workspace-count="${escapeHTML(workspace.id)}">${t("common.loading")}</span>${recent ? ` · ${label("lastUsed")}` : ""}</span>
        <small data-workspace-entry-reason ${reason ? "" : "hidden"}>${escapeHTML(reason)}</small>`,
    });
  }).join("");
  dialog.showModal();
  bindWorkspaceEntries(choices);
  const counts = [...choices.querySelectorAll<HTMLElement>("[data-project-workspace-count]")];
  // The main Task list is paged. Fetch summaries for this project so the
  // count also includes active Tasks outside the currently loaded page.
  void api.tasks(project.id, {summary: true}).then(({tasks}) => {
    const active = new Map<string, number>();
    for (const task of tasks) {
      if (task.status === "active") active.set(task.workspace_id, (active.get(task.workspace_id) || 0) + 1);
    }
    for (const count of counts) count.textContent = label("activeTasks", {count: active.get(count.dataset.projectWorkspaceCount!) || 0});
  }).catch(() => {
    for (const count of counts) count.textContent = label("taskCountUnavailable");
  });
}

export function renderProjectList({projects, workspaces, tasks, knowledgeCount, isSystemProject, projectTypeLabel, header, form, pagination, ...entry}: WorkspaceEntry & {
  projects: Project[];
  workspaces: Workspace[];
  tasks: Task[];
  knowledgeCount: number;
  isSystemProject: (project: Project) => boolean;
  projectTypeLabel: (type?: string) => string;
  header: string;
  form: string;
  pagination: string;
}): string {
  const {escapeHTML} = entry;
  const rows = projects.map(project => {
    const projectWorkspaces = workspaces.filter(item => item.project_id === project.id);
    const projectTasks = tasks.filter(item => item.project_id === project.id);
    const archived = project.channel_retired === true;
    const system = isSystemProject(project);
    return `<article class="list-row project-row" data-project="${escapeHTML(project.id)}">
      <div class="item-title"><span class="square-icon">${icon("projects")}</span><div><strong>${escapeHTML(project.name)}</strong><small>${projectTypeLabel(project.project_type)}${archived ? " · 渠道归档" : system ? " · 系统托管" : ""}</small></div><span class="row-actions">${projectWorkspaceAction(project, projectWorkspaces, entry)}${archived ? `<span class="status warn">已归档</span><button type="button" data-delete-project="${escapeHTML(project.id)}" class="icon-button danger project-del" title="永久删除归档">${icon("close")}</button>` : system ? `<span class="status warn">系统托管</span>` : `<button type="button" data-edit-project="${escapeHTML(project.id)}" class="icon-button" title="编辑项目">${icon("edit")}</button><button type="button" data-delete-project="${escapeHTML(project.id)}" class="icon-button project-del" title="删除项目">${icon("close")}</button>`}</span></div>
      <div class="chips">${projectWorkspaces.map(item => `<span>${item.locality === "remote" ? icon("server") : icon("monitor")}${escapeHTML([item.name, projectWorkspaceStatus(item), item.health === "error" ? label("health.error") : ""].filter(Boolean).join(" · "))}</span>`).join("") || "<span>暂无 Workspace</span>"}</div>
      <div><strong>${projectTasks.length}</strong><small>任务</small></div>
      <div><strong>${projectWorkspaces.filter(item => item.health === "ready").length}/${projectWorkspaces.length}</strong><small>Workspace Ready</small></div>
    </article>`;
  }).join("");
  return `<section class="page">
    ${header}
    <div data-onboarding-host></div>
    <div class="metrics"><div><small>项目</small><strong>${projects.length}</strong></div><div><small>Workspace</small><strong>${workspaces.length}</strong></div><div><small>活动任务</small><strong>${tasks.filter(item => item.status === "active").length}</strong></div><div><small>知识</small><strong>${knowledgeCount}</strong></div></div>
    <div class="panel"><div class="panel-head"><strong>所有项目</strong><button id="refresh">${icon("refresh")}刷新</button></div>${rows || `<div class="empty">创建第一个项目，随后点开配置 Workspace。</div>`}${pagination}</div>
    ${form}
    <dialog id="project-workspace-dialog" aria-labelledby="project-workspace-title">
      <div class="dialog-head"><h2 id="project-workspace-title">${label("choose")}</h2><button type="button" class="icon-button" data-close autofocus aria-label="${t("common.closeDialog")}">${icon("close")}</button></div>
      <div class="dialog-body project-workspace-choices" data-project-workspace-choices></div>
    </dialog>
  </section>`;
}
