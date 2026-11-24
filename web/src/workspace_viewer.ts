import {api} from "./api.js";
import {currentLocale, formatLocaleDateTime, t} from "./i18n.js";
import {icon} from "./icons.js";

type Scope = "file" | "uncommitted" | "base" | "commit";
type Mode = "files" | "changes";

interface Change {
  path: string;
  status?: string;
  added: number;
  deleted: number;
  binary?: boolean;
  masked?: boolean;
}

interface Commit {
  id: string;
  short: string;
  subject: string;
  date: number;
  files: Change[];
  truncated: boolean;
}

interface ViewerState {
  name: string;
  taskTitle: string;
  taskCode: string;
  isGit: boolean;
  version?: string;
  branch?: string;
  head?: string;
  base?: string;
  baseFallback?: boolean;
  baseAtHead?: boolean;
  mergeBase?: string;
  ahead: number;
  behind: number;
  branches: string[];
  uncommitted: Change[];
  baseChanges: Change[];
  commits: Commit[];
  truncated: boolean;
  repositories?: Repository[];
  repositoriesTruncated?: boolean;
  repositoryUnavailable?: boolean;
}

interface Repository {
  id: string;
  name: string;
  path: string;
  readable: boolean;
}

interface Entry {
  path: string;
  kind: "directory" | "file" | "symlink" | "other";
  size: number;
  mtime: string;
  masked?: boolean;
}

interface Tree {
  entries: Entry[];
  truncated: boolean;
}

interface Content {
  text: string;
  placeholder?: string;
  truncated?: boolean;
}

interface Tab {
  path: string;
  scope: Scope;
  commit: string;
  content?: Content;
  error?: string;
}

const copy: Record<string, Record<string, string>> = {
  "zh-CN": {
    title: "工作区查看器",
    navigation: "工作区导航",
    preview: "内容预览",
    baseline: "基线",
    repository: "仓库",
    rootRepository: "工作区根仓库",
    repositoryUnavailableLabel: "{name}（不可用）",
    repositoryUnavailable: "这个仓库尚未初始化，或无法读取 Git 数据。",
    invalidRepository: "找不到所选仓库，或该仓库已不在工作区仓库列表中。",
    repositoriesTruncated: "已达到仓库发现上限，列表可能不完整。",
    baselineFallback: "仓库中没有基线 {requested}，当前使用 {actual}。",
    detached: "分离 HEAD · {head}",
    automatic: "自动",
    automaticBase: "自动 · {base}",
    comparison: "领先 {ahead} 个提交，落后 {behind} 个提交",
    nonGit: "非 Git 工作区",
    changes: "变更",
    files: "文件",
    uncommitted: "未提交变更",
    commits: "最近提交",
    relativeCommits: "基线之后的提交",
    baseChanges: "与基线的差异",
    noChanges: "没有文件变更",
    noCommits: "暂无提交",
    noSubject: "无提交说明",
    tabs: "已打开的文件",
    closeTab: "关闭 {file}",
    closeAll: "关闭全部",
    empty: "从左侧选择文件或变更查看内容。",
    emptyFolder: "这个目录是空的。",
    emptyFile: "这个文件是空的。",
    noDiff: "当前选项下没有差异。",
    ignoreWhitespace: "忽略空白",
    wrap: "自动换行",
    previous: "上一处差异",
    next: "下一处差异",
    maskedLabel: "已遮蔽",
    binaryLabel: "二进制",
    masked: "这个文件的内容已遮蔽。",
    binary: "二进制文件无法预览。",
    tooLarge: "文件超过 1 MiB，无法预览。",
    truncatedList: "列表已截断。",
    truncatedDiff: "差异过大，已截断。",
    bytes: "{size} 字节",
    notFound: "找不到该任务。",
    gitFailed: "读取工作区的 Git 信息失败。",
    missing: "找不到该文件或目录。",
    invalidPath: "无法读取这个路径。符号链接和工作区之外的路径不可查看。",
    invalidRevision: "所选基线或提交已不可用。",
    failed: "读取工作区失败，请刷新重试。",
  },
  "en-US": {
    title: "Workspace viewer",
    navigation: "Workspace navigation",
    preview: "Content preview",
    baseline: "Base",
    repository: "Repository",
    rootRepository: "Workspace root",
    repositoryUnavailableLabel: "{name} (unavailable)",
    repositoryUnavailable: "This repository has not been initialized or its Git data could not be read.",
    invalidRepository: "The selected repository is missing or is no longer in the workspace repository list.",
    repositoriesTruncated: "Repository discovery reached its limit. The list may be incomplete.",
    baselineFallback: "Base {requested} is unavailable in this repository. Using {actual}.",
    detached: "Detached HEAD · {head}",
    automatic: "Automatic",
    automaticBase: "Automatic · {base}",
    comparison: "{ahead} commits ahead, {behind} behind",
    nonGit: "Non-Git workspace",
    changes: "Changes",
    files: "Files",
    uncommitted: "Uncommitted changes",
    commits: "Recent commits",
    relativeCommits: "Commits relative to base",
    baseChanges: "Diff against base",
    noChanges: "No changed files",
    noCommits: "No commits",
    noSubject: "No commit message",
    tabs: "Open files",
    closeTab: "Close {file}",
    closeAll: "Close all",
    empty: "Select a file or a change to preview it.",
    emptyFolder: "This folder is empty.",
    emptyFile: "This file is empty.",
    noDiff: "No differences with the current options.",
    ignoreWhitespace: "Ignore whitespace",
    wrap: "Wrap lines",
    previous: "Previous hunk",
    next: "Next hunk",
    maskedLabel: "Hidden",
    binaryLabel: "Binary",
    masked: "This file’s contents are hidden.",
    binary: "Binary file preview is unavailable.",
    tooLarge: "This file exceeds the 1 MiB preview limit.",
    truncatedList: "This list is truncated.",
    truncatedDiff: "This diff exceeds the display limit and was truncated.",
    bytes: "{size} bytes",
    notFound: "Task not found.",
    gitFailed: "Unable to read Git information for this workspace.",
    missing: "File or folder not found.",
    invalidPath: "This path cannot be read. Symlinks and paths outside the workspace are unavailable.",
    invalidRevision: "The selected base or commit is unavailable.",
    failed: "Unable to read the workspace. Refresh to try again.",
  },
};

const errorLabels: Record<string, string> = {
  task_not_found: "notFound",
  workspace_viewer_unavailable: "unavailable",
  workspace_viewer_transport_unsupported: "transport",
  workspace_viewer_git_failed: "gitFailed",
  workspace_viewer_path_not_found: "missing",
  workspace_viewer_invalid_path: "invalidPath",
  workspace_viewer_invalid_revision: "invalidRevision",
  workspace_viewer_invalid_repository: "invalidRepository",
  workspace_viewer_repository_unavailable: "repositoryUnavailable",
  workspace_viewer_not_git: "nonGit",
};

function label(key: string, values?: Record<string, string | number>): string {
  const text = copy[currentLocale()][key];
  return text ? text.replace(/\{(\w+)\}/g, (match, name) => String(values?.[name] ?? match))
    : t(`workspaceViewer.${key}`, values);
}

function errorText(reason: unknown): string {
  return label(errorLabels[(reason as {code?: string})?.code || ""] || "failed");
}

function html(value: unknown): string {
  return String(value ?? "").replaceAll("&", "&amp;").replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;").replaceAll('"', "&quot;").replaceAll("'", "&#39;");
}

function fileName(path: string): string {
  return path.slice(path.lastIndexOf("/") + 1);
}

function diffHTML(text: string): string {
  const groups: string[] = [];
  let start = 0;
  let groupStart = 0;
  let kind = "";
  let inHunk = false;
  const flush = (end: number) => {
    if (end > groupStart) groups.push(`<span class="wv-line ${kind}">${html(text.slice(groupStart, end))}</span>`);
    groupStart = end;
  };
  while (start < text.length) {
    const end = text.indexOf("\n", start);
    const next = end === -1 ? text.length : end + 1;
    let nextKind = "";
    if (text.startsWith("@@", start)) {
      nextKind = "wv-hunk";
      inHunk = true;
    } else if (!inHunk) nextKind = "wv-diff-header";
    else if (text[start] === "+") nextKind = "wv-added";
    else if (text[start] === "-") nextKind = "wv-deleted";
    if (nextKind !== kind || kind === "wv-hunk") {
      flush(start);
      kind = nextKind;
    }
    start = next;
  }
  flush(text.length);
  return groups.join("");
}

export function mountWorkspaceViewer(host: HTMLElement, task: string): void {
  host.innerHTML = `<section class="workspace-viewer" data-i18n-ignore>
    <header class="wv-header"></header>
    <div class="wv-notice" role="status" hidden></div>
    <div class="wv-layout">
      <aside class="wv-side"><nav class="wv-modes" aria-label="${label("navigation")}"></nav><div class="wv-navigator"></div></aside>
      <section class="wv-editor" aria-label="${label("preview")}">
        <div class="wv-tabs"></div>
        <div class="wv-content"></div>
      </section>
    </div>
  </section>`;
  const root = host.querySelector<HTMLElement>(".workspace-viewer")!;
  const header = root.querySelector<HTMLElement>(".wv-header")!;
  const notice = root.querySelector<HTMLElement>(".wv-notice")!;
  const modes = root.querySelector<HTMLElement>(".wv-modes")!;
  const navigation = root.querySelector<HTMLElement>(".wv-navigator")!;
  const tabBar = root.querySelector<HTMLElement>(".wv-tabs")!;
  const content = root.querySelector<HTMLElement>(".wv-content")!;
  const url = new URL(window.location.href);
  let initialFile = url.searchParams.get("file");
  const initialScope = url.searchParams.get("scope");
  let mode: Mode = url.searchParams.get("tab") === "files" ? "files" : "changes";
  let base = url.searchParams.get("base") || "";
  let repo = url.searchParams.get("repo") || "";
  const repositoryBases = new Map<string, string>();
  let repositories: Repository[] = [];
  let state: ViewerState | undefined;
  let wrap = false;
  let ignoreWhitespace = false;
  let selected: Tab | undefined;
  let statusRequest: AbortController | undefined;
  let contentRequest: AbortController | undefined;
  let treeSession = new AbortController();
  let currentHunk = -1;
  const tabs: Tab[] = [];
  const expandedFolders = new Set([""]);
  const expandedCommits = new Set<string>();
  const tree = new Map<string, Tree>();
  const treeErrors = new Map<string, string>();
  const treeRequests = new Map<string, Promise<void>>();
  let groupOpen = true;
  let baseOpen = false;

  function read<T>(view: string, params: Record<string, string>, signal?: AbortSignal): Promise<T> {
    const query = new URLSearchParams(params);
    if (repo) query.set("repo", repo);
    return api.request<T>(`/api/v1/tasks/${encodeURIComponent(task)}/workspace/${view}?${query}`, {signal});
  }

  function showNotice(message = ""): void {
    notice.textContent = message;
    notice.hidden = !message;
  }

  function saveLocation(): void {
    const next = new URL(window.location.href);
    next.search = new URLSearchParams({task, tab: mode}).toString();
    const savedBase = base || (!repo ? state?.base : "");
    if (savedBase) next.searchParams.set("base", savedBase);
    if (repo) next.searchParams.set("repo", repo);
    if (selected) {
      next.searchParams.set("file", selected.path);
      next.searchParams.set("scope", selected.scope);
      if (selected.commit) next.searchParams.set("commit", selected.commit);
    }
    history.replaceState(null, "", next);
  }

  function renderHeader(): void {
    const focused = document.activeElement?.closest("select, button");
    const focusAttr = focused && header.contains(focused) ? [...focused.attributes].find(attr => attr.name.startsWith("data-wv-"))?.name : undefined;
    const branch = state?.branch || (state?.head ? label("detached", {head: state.head.slice(0, 8)}) : label("noCommits"));
    const baseline = state?.base || "";
    const shortBase = /^[a-f0-9]{40,64}$/.test(baseline) ? baseline.slice(0, 8) : baseline;
    const choices = new Set(state?.branches || []);
    const visibleBase = state?.baseFallback ? "" : base;
    if (visibleBase) choices.add(visibleBase);
    const automatic = shortBase ? label("automaticBase", {base: shortBase}) : label("automatic");
    const baseTitle = state?.baseFallback ? label("baselineFallback", {requested: base, actual: baseline || label("noCommits")})
      : visibleBase || (baseline ? label("automaticBase", {base: baseline}) : automatic);
    const repositoryLabel = (item: Repository) => {
      const name = item.path || label("rootRepository");
      return item.readable ? name : label("repositoryUnavailableLabel", {name});
    };
    const currentRepository = repositories.find(item => item.id === repo);
    header.innerHTML = `<div class="wv-heading">
        <span class="wv-heading-icon">${icon("folder")}</span>
        <div><h1>${html(state?.name || repositories[0]?.name || label("title"))}</h1>
        ${state ? `<p>${html([state.taskCode, state.taskTitle].filter(Boolean).join(" · "))}</p>` : ""}</div>
      </div>
      <div class="wv-meta">
        ${repositories.length > 0 ? `<select data-wv-repo aria-label="${label("repository")}" title="${html(currentRepository ? repositoryLabel(currentRepository) : label("repository"))}">
          ${repositories.map(item => `<option value="${html(item.id)}" ${item.id === repo ? "selected" : ""}>${html(repositoryLabel(item))}</option>`).join("")}
        </select>` : ""}
        ${state?.isGit ? `<span class="wv-branch" title="${html(state.head && !state.branch ? label("detached", {head: state.head}) : branch)}">${icon("branch")}<span>${html(branch)}</span></span>
          <label><span>${label("baseline")}</span><select data-wv-base aria-label="${label("baseline")}" title="${html(baseTitle)}">
            <option value="">${html(automatic)}</option>
            ${[...choices].map(value => `<option value="${html(value)}" ${value === visibleBase ? "selected" : ""}>${html(value === baseline ? shortBase : value)}</option>`).join("")}
          </select></label>
          ${baseline && !state.baseAtHead ? `<span class="wv-counts" title="${html(label("comparison", {ahead: state.ahead, behind: state.behind}))}">↑${state.ahead} ↓${state.behind}</span>` : ""}`
        : state && !state.repositoryUnavailable ? `<span class="wv-badge">${label("nonGit")}</span>` : ""}
        <button type="button" data-wv-action="refresh" title="${t("common.refresh")}" aria-label="${t("common.refresh")}">${icon("refresh")}</button>
      </div>`;
    if (focusAttr) header.querySelector<HTMLElement>(`[${focusAttr}]`)?.focus({preventScroll: true});
  }

  function changeList(changes: Change[], scope: Scope, commit = ""): string {
    if (!changes.length) return `<p class="wv-hint">${label("noChanges")}</p>`;
    return `<ul class="wv-list">${changes.map(change => `
      <li><button type="button" class="wv-row" data-wv-path="${encodeURIComponent(change.path)}" data-wv-scope="${scope}" data-wv-commit="${commit}" title="${html(change.path)}">
        <span class="wv-row-name">${icon(change.masked ? "shield" : "file")}<span>${html(change.path)}</span></span>
        <span class="wv-stat">${change.status ? html(change.status.replaceAll(".", "·")) : change.masked ? label("maskedLabel") : change.binary ? label("binaryLabel")
          : `<span class="wv-plus">+${change.added}</span> <span class="wv-minus">−${change.deleted}</span>`}</span>
      </button></li>`).join("")}</ul>`;
  }

  function renderChanges(): string {
    const commits = state!.commits;
    const history = commits.map(commit => {
      const opened = expandedCommits.has(commit.id);
      return `<section class="wv-commit">
        <button type="button" class="wv-row wv-commit-title" data-wv-toggle-commit="${commit.id}" aria-expanded="${opened}">
          <span class="wv-row-name"><span class="wv-disclosure" aria-hidden="true">${opened ? "▾" : "▸"}</span><span title="${html(commit.subject)}">${html(commit.subject || label("noSubject"))}</span></span>
          <code>${html(commit.short)}</code>
        </button>
        ${opened ? `<div class="wv-commit-files">${changeList(commit.files, "commit", commit.id)}${commit.truncated ? `<p class="wv-hint">${label("truncatedList")}</p>` : ""}</div>` : ""}
      </section>`;
    }).join("");
    return `<section class="wv-group">
      <button type="button" class="wv-group-title" data-wv-action="uncommitted" aria-expanded="${groupOpen}">
        <span aria-hidden="true">${groupOpen ? "▾" : "▸"}</span> ${label("uncommitted")} <span class="wv-count">${state!.uncommitted.length}</span>
      </button>
      ${groupOpen ? changeList(state!.uncommitted, "uncommitted") : ""}
    </section>
    <section class="wv-group">
      <h2 class="wv-group-title">${state!.base && !state!.baseAtHead ? label("relativeCommits") : label("commits")} <span class="wv-count">${commits.length}</span></h2>
      ${history || `<p class="wv-hint">${label("noCommits")}</p>`}
    </section>
    ${state!.mergeBase ? `<section class="wv-group">
      <button type="button" class="wv-group-title" data-wv-action="baseChanges" aria-expanded="${baseOpen}">
        <span aria-hidden="true">${baseOpen ? "▾" : "▸"}</span> ${label("baseChanges")} <span class="wv-count">${state!.baseChanges.length}</span>
      </button>
      ${baseOpen ? changeList(state!.baseChanges, "base") : ""}
    </section>` : ""}
    ${state!.truncated ? `<p class="wv-hint">${label("truncatedList")}</p>` : ""}`;
  }

  function renderTree(path = ""): string {
    if (treeErrors.has(path)) return `<p class="wv-hint">${html(treeErrors.get(path))}</p>`;
    const listing = tree.get(path);
    if (!listing) return `<p class="wv-hint">${t("common.loading")}</p>`;
    if (!listing.entries.length) return `<p class="wv-hint">${label("emptyFolder")}</p>`;
    const entries = listing.entries.map(entry => {
      const directory = entry.kind === "directory" && !entry.masked;
      const opened = expandedFolders.has(entry.path);
      const disabled = entry.kind !== "file" && !directory && !entry.masked;
      const name = html(fileName(entry.path));
      const tooltip = directory || disabled || entry.masked ? entry.path
        : `${entry.path} · ${label("bytes", {size: entry.size})} · ${formatLocaleDateTime(entry.mtime)}`;
      const action = directory ? `data-wv-folder="${encodeURIComponent(entry.path)}" aria-expanded="${opened}"`
        : `data-wv-path="${encodeURIComponent(entry.path)}" data-wv-scope="file" ${disabled ? "disabled" : ""}`;
      return `<li><button type="button" class="wv-row" ${action} title="${html(tooltip)}">
        <span class="wv-row-name">${directory ? `<span class="wv-disclosure" aria-hidden="true">${opened ? "▾" : "▸"}</span>` : ""}
        ${icon(entry.masked ? "shield" : directory ? "folder" : entry.kind === "symlink" ? "link" : "file")}<span>${name}</span></span>
        ${entry.masked ? `<small>${label("maskedLabel")}</small>` : ""}
      </button>${directory && opened ? renderTree(entry.path) : ""}</li>`;
    }).join("");
    return `<ul class="wv-list wv-tree">${entries}</ul>${listing.truncated ? `<p class="wv-hint">${label("truncatedList")}</p>` : ""}`;
  }

  function renderNavigation(): void {
    const scroll = navigation.scrollTop;
    const focused = navigation.contains(document.activeElement) ? document.activeElement as HTMLButtonElement : undefined;
    const focusKey = focused && Object.entries(focused.dataset).filter(([key]) => key.startsWith("wv"));
    const ready = state && !state.repositoryUnavailable;
    modes.innerHTML = `${state?.isGit ? `<button type="button" data-wv-mode="changes" aria-pressed="${mode === "changes"}">${icon("branch")}${label("changes")}</button>` : ""}
      ${ready ? `<button type="button" data-wv-mode="files" aria-pressed="${mode === "files"}">${icon("folder")}${label("files")}</button>` : ""}`;
    navigation.innerHTML = ready ? (mode === "changes" ? renderChanges() : renderTree()) : "";
    navigation.scrollTop = scroll;
    if (focusKey?.length) {
      [...navigation.querySelectorAll("button")].find(button =>
        focusKey.every(([key, value]) => button.dataset[key] === value))?.focus({preventScroll: true});
    }
  }

  function renderTabs(): void {
    const scroll = tabBar.querySelector(".wv-tab-list")?.scrollLeft || 0;
    tabBar.hidden = tabs.length === 0;
    tabBar.innerHTML = `<div class="wv-tab-list" aria-label="${label("tabs")}">${tabs.map((tab, index) => `
      <div class="wv-tab" data-wv-tab="${index}">
        <button type="button" data-wv-select="${index}" aria-pressed="${tab === selected}" title="${html(tab.path)}">${icon("file")}<span>${html(fileName(tab.path))}</span></button>
        <button type="button" data-wv-close="${index}" title="${html(label("closeTab", {file: fileName(tab.path)}))}" aria-label="${html(label("closeTab", {file: fileName(tab.path)}))}">${icon("close")}</button>
      </div>`).join("")}</div>
      <button type="button" class="wv-close-all" data-wv-action="closeAll">${label("closeAll")}</button>`;
    const list = tabBar.querySelector<HTMLElement>(".wv-tab-list")!;
    list.scrollLeft = scroll;
    const active = list.querySelector<HTMLElement>('[aria-pressed="true"]')?.parentElement;
    if (active) {
      const outer = list.getBoundingClientRect();
      const inner = active.getBoundingClientRect();
      if (inner.left < outer.left) list.scrollLeft -= outer.left - inner.left;
      if (inner.right > outer.right) list.scrollLeft += inner.right - outer.right;
    }
  }

  function renderContent(keepScroll = false): void {
    const top = keepScroll ? content.querySelector(".wv-code")?.scrollTop || 0 : 0;
    const left = keepScroll ? content.querySelector(".wv-code")?.scrollLeft || 0 : 0;
    currentHunk = -1;
    if (!selected) {
      content.innerHTML = state?.repositoryUnavailable ? "" : `<div class="wv-empty">${icon("file")}<p>${label("empty")}</p></div>`;
      return;
    }
    const tab = selected;
    const info = tab.content;
    const diff = tab.scope !== "file";
    const placeholder = info?.placeholder;
    let body = "";
    if (tab.error) body = `<p class="wv-hint" role="alert">${html(tab.error)}</p>`;
    else if (!info) body = `<p class="wv-hint" role="status">${t("common.loading")}</p>`;
    else if (placeholder) body = `<p class="wv-hint">${label(placeholder === "too_large" ? "tooLarge" : placeholder)}</p>`;
    else if (!info.text) body = `<p class="wv-hint">${label(diff ? "noDiff" : "emptyFile")}</p>`;
    else body = `<pre class="wv-code ${wrap ? "wv-wrap" : ""}" tabindex="0"><code>${diff ? diffHTML(info.text) : html(info.text)}</code></pre>`;
    const source = tab.scope === "file" ? "" : tab.scope === "uncommitted" ? label("uncommitted")
      : tab.scope === "base" ? label("baseChanges") : tab.commit.slice(0, 8);
    content.innerHTML = `<div class="wv-content-head"><strong title="${html(tab.path)}">${html(tab.path)}</strong>
      ${source ? `<span class="wv-badge">${html(source)}</span>` : ""}
    </div>
    <div class="wv-toolbar">
      ${diff ? `<label><input type="checkbox" data-wv-ignore ${ignoreWhitespace ? "checked" : ""}> ${label("ignoreWhitespace")}</label>` : ""}
      <label><input type="checkbox" data-wv-wrap ${wrap ? "checked" : ""}> ${label("wrap")}</label>
      ${diff ? `<button type="button" data-wv-action="previous" ${info && /^@@/m.test(info.text) ? "" : "disabled"} title="${label("previous")}">↑ <span>${label("previous")}</span></button>
      <button type="button" data-wv-action="next" ${info && /^@@/m.test(info.text) ? "" : "disabled"} title="${label("next")}">↓ <span>${label("next")}</span></button>` : ""}
    </div>
    ${info?.truncated ? `<p class="wv-truncated" role="status">${label("truncatedDiff")}</p>` : ""}
    ${body}`;
    const scroll = content.querySelector(".wv-code");
    if (scroll) {
      scroll.scrollTop = top;
      scroll.scrollLeft = left;
    }
  }

  async function readTab(tab: Tab, background = false): Promise<void> {
    contentRequest?.abort();
    const controller = new AbortController();
    contentRequest = controller;
    const params: Record<string, string> = {path: tab.path};
    if (tab.scope !== "file") {
      params.scope = tab.scope;
      if (tab.scope === "commit") params.commit = tab.commit;
      if (tab.scope === "base" && (base || (!repo && state?.base))) params.base = base || state!.base!;
      if (ignoreWhitespace) params.ignore_whitespace = "1";
    }
    try {
      const value = await read<Content>(tab.scope === "file" ? "file" : "diff", params, controller.signal);
      if (controller.signal.aborted || selected !== tab) return;
      const old = tab.content;
      const changed = tab.error || !old || old.text !== value.text || old.placeholder !== value.placeholder || old.truncated !== value.truncated;
      tab.content = value;
      delete tab.error;
      if (changed) renderContent(background);
    } catch (reason) {
      if (controller.signal.aborted || selected !== tab) return;
      const message = errorText(reason);
      if (tab.error !== message) {
        tab.error = message;
        renderContent(background);
      }
    } finally {
      if (contentRequest === controller) contentRequest = undefined;
    }
  }

  function select(tab: Tab | undefined): void {
    contentRequest?.abort();
    selected = tab;
    renderTabs();
    renderContent();
    saveLocation();
    if (tab) void readTab(tab);
  }

  function open(path: string, scope: Scope, commit = ""): void {
    let tab = tabs.find(item => item.path === path);
    if (!tab) {
      tab = {path, scope, commit};
      tabs.push(tab);
    } else if (tab.scope !== scope || tab.commit !== commit) {
      Object.assign(tab, {scope, commit, content: undefined, error: undefined});
    }
    select(tab);
  }

  function close(index: number): void {
    const [removed] = tabs.splice(index, 1);
    if (removed === selected) select(tabs[index] || tabs[index - 1]);
    else renderTabs();
    tabBar.querySelector<HTMLElement>('[aria-pressed="true"]')?.focus();
  }

  function readTree(path: string): Promise<void> {
    const pending = treeRequests.get(path);
    if (pending) return pending;
    const signal = treeSession.signal;
    const promise = (async () => {
      try {
        const value = await read<Tree>("tree", {path}, signal);
        if (signal.aborted) return;
        const hadError = treeErrors.delete(path);
        if (!hadError && JSON.stringify(tree.get(path)) === JSON.stringify(value)) return;
        tree.set(path, value);
      } catch (reason) {
        if (signal.aborted) return;
        const message = errorText(reason);
        if (treeErrors.get(path) === message) return;
        treeErrors.set(path, message);
      } finally {
        if (!signal.aborted) treeRequests.delete(path);
      }
      if (mode === "files" && expandedFolders.has(path)) renderNavigation();
    })();
    treeRequests.set(path, promise);
    return promise;
  }

  async function refresh(poll = false): Promise<void> {
    if (!task) return;
    if (poll && (document.hidden || statusRequest)) return;
    statusRequest?.abort();
    const controller = new AbortController();
    statusRequest = controller;
    try {
      let fetchFull = !poll || !state;
      if (!fetchFull) {
        const summary = await read<Pick<ViewerState, "version" | "isGit">>("state", {summary: "1"}, controller.signal);
        fetchFull = summary.isGit !== state?.isGit || summary.version !== state?.version;
      }
      if (controller.signal.aborted) return;
      if (fetchFull) {
        const params = base ? {base} : {};
        const next = await read<ViewerState>("state", params, controller.signal);
        if (controller.signal.aborted) return;
        const first = !state;
        const latest = next.commits?.[0]?.id;
        if (latest && latest !== state?.commits?.[0]?.id) expandedCommits.add(latest);
        const lostGit = state?.isGit && !next.isGit && !next.repositoryUnavailable;
        state = next;
        repositories = next.repositories || [];
        document.title = `${next.name} · AHA2`;
        if (!next.isGit && !next.repositoryUnavailable) {
          mode = "files";
          base = "";
        }
        if (lostGit) {
          for (const tab of tabs) {
            if (tab.scope !== "file") Object.assign(tab, {scope: "file", commit: "", content: undefined, error: undefined});
          }
          select(selected);
        }
        renderHeader();
        renderNavigation();
        if (first && initialFile && !next.repositoryUnavailable) {
          const scope = next.isGit && (initialScope === "uncommitted" || initialScope === "commit" || initialScope === "base")
            ? initialScope : "file";
          if (mode === "files") {
            for (let parent = initialFile.lastIndexOf("/"); parent > 0; parent = initialFile.lastIndexOf("/", parent - 1)) {
              expandedFolders.add(initialFile.slice(0, parent));
            }
          }
          open(initialFile, scope, url.searchParams.get("commit") || "");
        }
        initialFile = null;
        if (next.repositoryUnavailable) {
          tabs.length = 0;
          select(undefined);
        } else if (!selected) renderContent();
        saveLocation();
      }
      if (state?.repositoryUnavailable) {
        showNotice(label("repositoryUnavailable"));
        return;
      }
      showNotice(state?.repositoriesTruncated ? label("repositoriesTruncated") : "");
      if (mode === "files") {
        const activePath = selected?.path || "";
        const selectedFolder = activePath.slice(0, Math.max(0, activePath.lastIndexOf("/")));
        const folders = poll ? [...new Set(["", selectedFolder])].filter(path => expandedFolders.has(path)) : [...expandedFolders];
        void Promise.all(folders.map(readTree));
      }
      if (selected && !contentRequest) void readTab(selected, true);
    } catch (reason) {
      if (!controller.signal.aborted) showNotice(errorText(reason));
    } finally {
      if (statusRequest === controller) statusRequest = undefined;
    }
  }

  navigation.addEventListener("click", event => {
    const button = (event.target as Element).closest<HTMLButtonElement>("button");
    if (!button || !state) return;
    const {wvFolder, wvPath, wvScope, wvCommit, wvToggleCommit, wvAction} = button.dataset;
    if (wvPath !== undefined) {
      open(decodeURIComponent(wvPath), (wvScope || "file") as Scope, wvCommit);
      return;
    }
    if (wvFolder !== undefined) {
      const path = decodeURIComponent(wvFolder);
      if (expandedFolders.delete(path)) {
        for (const sub of expandedFolders) if (sub.startsWith(path + "/")) expandedFolders.delete(sub);
      } else {
        expandedFolders.add(path);
        void readTree(path);
      }
    } else if (wvToggleCommit) {
      if (!expandedCommits.delete(wvToggleCommit)) expandedCommits.add(wvToggleCommit);
    } else if (wvAction === "uncommitted") {
      groupOpen = !groupOpen;
    } else if (wvAction === "baseChanges") {
      baseOpen = !baseOpen;
    }
    renderNavigation();
  });

  modes.addEventListener("click", event => {
    const button = (event.target as Element).closest<HTMLElement>("[data-wv-mode]");
    if (!button) return;
    mode = button.dataset.wvMode as Mode;
    renderNavigation();
    saveLocation();
    if (mode === "files") void readTree("");
    modes.querySelector<HTMLElement>('[aria-pressed="true"]')?.focus();
  });

  tabBar.addEventListener("click", event => {
    const {wvClose, wvSelect, wvAction} = (event.target as Element).closest("button")?.dataset || {};
    if (wvClose !== undefined) close(Number(wvClose));
    if (wvSelect !== undefined) {
      select(tabs[Number(wvSelect)]);
      tabBar.querySelector<HTMLElement>('[aria-pressed="true"]')?.focus();
    }
    if (wvAction === "closeAll") {
      tabs.length = 0;
      select(undefined);
    }
  });
  tabBar.addEventListener("auxclick", event => {
    const tab = (event.target as Element).closest<HTMLElement>("[data-wv-tab]");
    if (event.button === 1 && tab) {
      event.preventDefault();
      close(Number(tab.dataset.wvTab));
    }
  });
  header.addEventListener("click", event => {
    if ((event.target as Element).closest('[data-wv-action="refresh"]')) void refresh();
  });
  header.addEventListener("change", event => {
    const input = event.target as HTMLSelectElement;
    if (input.hasAttribute("data-wv-repo")) {
      repositoryBases.set(repo, base);
      repo = input.value;
      base = repositoryBases.get(repo) ?? (repo ? repositoryBases.get("") || "" : "");
      initialFile = null;
      state = undefined;
      treeSession.abort();
      treeSession = new AbortController();
      treeRequests.clear();
      treeErrors.clear();
      tree.clear();
      expandedFolders.clear();
      expandedFolders.add("");
      expandedCommits.clear();
      groupOpen = true;
      baseOpen = false;
      tabs.length = 0;
      select(undefined);
      renderHeader();
      renderNavigation();
      showNotice(t("common.loading"));
      void refresh();
      return;
    }
    if (!input.hasAttribute("data-wv-base")) return;
    base = input.value;
    if (selected?.scope === "base") {
      contentRequest?.abort();
      contentRequest = undefined;
      delete selected.content;
      renderContent();
    }
    void refresh();
  });
  content.addEventListener("change", event => {
    const input = event.target as HTMLInputElement;
    if (input.hasAttribute("data-wv-wrap")) {
      wrap = input.checked;
      content.querySelector(".wv-code")?.classList.toggle("wv-wrap", wrap);
    }
    if (input.hasAttribute("data-wv-ignore")) {
      ignoreWhitespace = input.checked;
      if (selected) void readTab(selected, true);
    }
  });
  content.addEventListener("click", event => {
    const action = (event.target as Element).closest<HTMLElement>("[data-wv-action]")?.dataset.wvAction;
    if (action !== "previous" && action !== "next") return;
    const code = content.querySelector<HTMLElement>(".wv-code");
    const hunks = content.querySelectorAll<HTMLElement>(".wv-hunk");
    if (!code || !hunks.length) return;
    currentHunk = currentHunk === -1 && action === "previous" ? hunks.length - 1
      : (currentHunk + (action === "next" ? 1 : -1) + hunks.length) % hunks.length;
    content.querySelector(".wv-current-hunk")?.classList.remove("wv-current-hunk");
    const hunk = hunks[currentHunk];
    hunk.classList.add("wv-current-hunk");
    code.scrollTop += hunk.getBoundingClientRect().top - code.getBoundingClientRect().top;
  });
  renderHeader();
  renderTabs();
  renderContent();
  showNotice(task ? t("common.loading") : label("notFound"));
  void refresh();
  window.setInterval(() => { void refresh(true); }, 10_000);
}
