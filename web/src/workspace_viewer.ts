import {api} from "./api.js";
import {currentLocale, formatLocaleDateTime, t} from "./i18n.js";
import {icon} from "./icons.js";

type Scope = "file" | "uncommitted" | "base" | "commit";
type Mode = "files" | "changes";

const commitPageSize = 20;

interface Change {
  path: string;
  status?: string;
  added: number;
  deleted: number;
  binary?: boolean;
  masked?: boolean;
  gitlink?: {old?: string; new?: string; index?: string};
}

interface Commit {
  id: string;
  short: string;
  subject: string;
  author: string;
  date: number;
  files: Change[];
  truncated: boolean;
}

interface Branch {
  ref: string;
  name: string;
}

interface ViewerState {
  name: string;
  taskTitle: string;
  taskCode: string;
  isGit: boolean;
  version?: string;
  branch?: string;
  checkedOutBranch?: string;
  branchOptions?: Branch[];
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
  repositoryDeadlineReached?: boolean;
  repositorySizeReached?: boolean;
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
  repo: string;
  branch: string;
  base: string;
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
    branch: "分支",
    currentBranch: "当前分支",
    localBranches: "本地分支",
    remoteBranches: "远程分支",
    invalidBranch: "所选分支不存在或已不可用。",
    repository: "仓库",
    repositoryUnavailableLabel: "{name}（不可用）",
    repositoryUnavailable: "这个仓库尚未初始化，或无法读取 Git 数据。",
    invalidRepository: "找不到所选仓库，或该仓库已不在工作区仓库列表中。",
    repositoryRecovered: "所选仓库已不可用，已切回工作区根仓库。",
    repositoriesTruncated: "子模块列表可能不完整。",
    repositoryDeadlineReached: "读取子模块登记超时。",
    repositorySizeReached: "仓库列表达到数据大小上限。",
    repositoryScanIncomplete: "子模块登记未读取完整，暂时无法确认所选仓库。",
    gitlinkChange: "子模块更新 {old} → {new}",
    gitlinkMissing: "无",
    baselineFallback: "仓库中没有基线 {requested}，当前使用 {actual}。",
    detached: "分离 HEAD · {head}",
    automatic: "自动",
    automaticBase: "自动 · {base}",
    comparison: "领先 {ahead} 个提交，落后 {behind} 个提交",
    nonGit: "非 Git 工作区",
    changes: "变更",
    files: "文件",
    worktreeFiles: "工作区文件",
    uncommitted: "未提交变更",
    commits: "最近提交",
    relativeCommits: "基线之后的提交",
    baseChanges: "与基线的差异",
    noChanges: "没有文件变更",
    noCommits: "暂无提交",
    loadMoreCommits: "加载更多",
    noSubject: "无提交说明",
    commitInfo: "{subject}\n完整哈希 {hash}\n作者 {author}\n提交时间 {date}",
    copyCommit: "复制完整提交哈希",
    copiedCommit: "已复制完整提交哈希。",
    copyCommitFailed: "无法复制完整提交哈希。",
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
    branch: "Branch",
    currentBranch: "Current branch",
    localBranches: "Local branches",
    remoteBranches: "Remote branches",
    invalidBranch: "The selected branch is missing or unavailable.",
    repository: "Repository",
    repositoryUnavailableLabel: "{name} (unavailable)",
    repositoryUnavailable: "This repository has not been initialized or its Git data could not be read.",
    invalidRepository: "The selected repository is missing or is no longer in the workspace repository list.",
    repositoryRecovered: "The selected repository is unavailable. Showing the workspace root.",
    repositoriesTruncated: "The submodule list may be incomplete.",
    repositoryDeadlineReached: "Reading registered submodules timed out.",
    repositorySizeReached: "The repository list reached its size limit.",
    repositoryScanIncomplete: "The registered submodule list is incomplete. The selected repository could not be verified.",
    gitlinkChange: "Submodule update {old} → {new}",
    gitlinkMissing: "None",
    baselineFallback: "Base {requested} is unavailable in this repository. Using {actual}.",
    detached: "Detached HEAD · {head}",
    automatic: "Automatic",
    automaticBase: "Automatic · {base}",
    comparison: "{ahead} commits ahead, {behind} behind",
    nonGit: "Non-Git workspace",
    changes: "Changes",
    files: "Files",
    worktreeFiles: "Working tree files",
    uncommitted: "Uncommitted changes",
    commits: "Recent commits",
    relativeCommits: "Commits relative to base",
    baseChanges: "Diff against base",
    noChanges: "No changed files",
    noCommits: "No commits",
    loadMoreCommits: "Load more",
    noSubject: "No commit message",
    commitInfo: "{subject}\nFull hash {hash}\nAuthor {author}\nCommitted {date}",
    copyCommit: "Copy full commit hash",
    copiedCommit: "Full commit hash copied.",
    copyCommitFailed: "Unable to copy the full commit hash.",
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
  workspace_viewer_invalid_branch: "invalidBranch",
  workspace_viewer_invalid_repository: "invalidRepository",
  workspace_viewer_repository_unavailable: "repositoryUnavailable",
  workspace_viewer_repository_discovery_incomplete: "repositoryScanIncomplete",
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

export function mountWorkspaceViewer(host: HTMLElement, task: string, copyText: (value: string) => Promise<void>): void {
  host.innerHTML = `<section class="workspace-viewer" data-i18n-ignore>
    <header class="wv-header"></header>
    <div class="wv-notice" role="status" hidden></div>
    <span class="visually-hidden" role="status" data-wv-copy-status></span>
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
  const copyStatus = root.querySelector<HTMLElement>("[data-wv-copy-status]")!;
  const modes = root.querySelector<HTMLElement>(".wv-modes")!;
  const navigation = root.querySelector<HTMLElement>(".wv-navigator")!;
  const tabBar = root.querySelector<HTMLElement>(".wv-tabs")!;
  const content = root.querySelector<HTMLElement>(".wv-content")!;
  const url = new URL(window.location.href);
  let initialFile = url.searchParams.get("file");
  const initialScope = url.searchParams.get("scope");
  const initialFileRepo = url.searchParams.get("fileRepo");
  const initialFileBase = url.searchParams.get("fileBase");
  const initialFileBranch = url.searchParams.get("fileBranch");
  let loaded = false;
  let mode: Mode = url.searchParams.get("tab") === "files" ? "files" : "changes";
  let base = url.searchParams.get("base") || "";
  let repo = url.searchParams.get("repo") || "";
  let branch = url.searchParams.get("branch") || "";
  const repositoryBases = new Map<string, string>();
  const repositoryBranches = new Map<string, string>();
  if (initialFileRepo !== null && initialFileBranch !== null) repositoryBranches.set(initialFileRepo, initialFileBranch);
  let branchMenu: {options: Branch[]; current: string; title: string; checkedOutRef: string} | undefined;
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
  let commitsOpen = false;
  let visibleCommitCount = commitPageSize;
  let baseOpen = false;

  function read<T>(view: string, params: Record<string, string>, signal: AbortSignal, repository = repo): Promise<T> {
    const query = new URLSearchParams(params);
    if (repository) query.set("repo", repository);
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
    if (branch) next.searchParams.set("branch", branch);
    if (selected) {
      next.searchParams.set("file", selected.path);
      next.searchParams.set("scope", selected.scope);
      if (selected.commit) next.searchParams.set("commit", selected.commit);
      if (selected.scope !== "file" && selected.branch !== (selected.repo === repo ? branch : "")) {
        next.searchParams.set("fileBranch", selected.branch);
      }
      if (selected.repo !== repo) {
        next.searchParams.set("fileRepo", selected.repo);
        if (selected.scope === "base" && selected.base) next.searchParams.set("fileBase", selected.base);
      }
    }
    history.replaceState(null, "", next);
  }

  function renderHeader(): void {
    const focused = document.activeElement?.closest("select, button");
    const focusAttr = focused && header.contains(focused) ? [...focused.attributes].find(attr => attr.name.startsWith("data-wv-"))?.name : undefined;
    const current = branchMenu?.current || label("currentBranch");
    const options = branchMenu?.options || [];
    const selectedBranch = options.find(option => option.ref === branch);
    const branchTitle = selectedBranch?.name || branch || branchMenu?.title || current;
    const branchOptions = (prefix: string) => options.filter(option =>
      option.ref.startsWith(prefix) && (option.ref !== branchMenu?.checkedOutRef || option.ref === branch))
      .map(({ref, name}) => `<option value="${html(ref)}" ${ref === branch ? "selected" : ""}>${html(name)}</option>`).join("");
    const local = branchOptions("refs/heads/");
    const remote = branchOptions("refs/remotes/");
    const baseline = state?.base || "";
    const shortBase = /^[a-f0-9]{40,64}$/.test(baseline) ? baseline.slice(0, 8) : baseline;
    const choices = new Set(state?.branches || []);
    const visibleBase = state?.baseFallback ? "" : base;
    if (visibleBase) choices.add(visibleBase);
    const automatic = shortBase ? label("automaticBase", {base: shortBase}) : label("automatic");
    const baseTitle = state?.baseFallback ? label("baselineFallback", {requested: base, actual: baseline || label("noCommits")})
      : visibleBase || (baseline ? label("automaticBase", {base: baseline}) : automatic);
    const repositoryLabel = (item: Repository) => {
      const name = item.path || item.name || state?.name || label("title");
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
        ${branchMenu || branch ? `<span class="wv-branch">${icon("branch")}
          <select data-wv-branch aria-label="${label("branch")}" title="${html(branchTitle)}">
            <option value="">${html(current)}</option>
            ${local ? `<optgroup label="${label("localBranches")}">${local}</optgroup>` : ""}
            ${remote ? `<optgroup label="${label("remoteBranches")}">${remote}</optgroup>` : ""}
            ${branch && !selectedBranch ? `<option value="${html(branch)}" selected>${html(branch)}</option>` : ""}
          </select></span>` : ""}
        ${state?.isGit ? `<label><span>${label("baseline")}</span><select data-wv-base aria-label="${label("baseline")}" title="${html(baseTitle)}">
            <option value="">${html(automatic)}</option>
            ${[...choices].map(value => `<option value="${html(value)}" ${value === visibleBase ? "selected" : ""}>${html(value === baseline ? shortBase : value)}</option>`).join("")}
          </select></label>
          ${baseline && !state.baseAtHead ? `<span class="wv-counts" title="${html(label("comparison", {ahead: state.ahead, behind: state.behind}))}">↑${state.ahead} ↓${state.behind}</span>` : ""}`
        : state && !state.repositoryUnavailable ? `<span class="wv-badge">${label("nonGit")}</span>` : ""}
        <button type="button" data-wv-action="refresh" title="${t("common.refresh")}" aria-label="${t("common.refresh")}">${icon("refresh")}</button>
      </div>`;
    if (focusAttr) header.querySelector<HTMLElement>(`[${focusAttr}]`)?.focus({preventScroll: true});
  }

  function changeStat(change: Change): string {
    if (change.masked) return label("maskedLabel");
    if (change.gitlink) {
      const {old, index, new: next} = change.gitlink;
      const short = (id?: string) => id?.slice(0, 8) || label("gitlinkMissing");
      const transition = index ? `${short(index)} → ${short(next)}` : short(next);
      return html(label("gitlinkChange", {old: short(old), new: transition}));
    }
    if (change.status) return html(change.status.replaceAll(".", "·"));
    if (change.binary) return label("binaryLabel");
    return `<span class="wv-plus">+${change.added}</span> <span class="wv-minus">−${change.deleted}</span>`;
  }

  function changeList(changes: Change[], scope: Scope, commit = ""): string {
    if (!changes.length) return `<p class="wv-hint">${label("noChanges")}</p>`;
    return `<ul class="wv-list">${changes.map(change => `
      <li><button type="button" class="wv-row" data-wv-path="${encodeURIComponent(change.path)}" data-wv-scope="${scope}" data-wv-commit="${commit}" title="${html(change.path)}">
        <span class="wv-row-name">${icon(change.masked ? "shield" : "file")}<span>${html(change.path)}</span></span>
        <span class="wv-stat${change.gitlink ? " wv-gitlink-stat" : ""}">${changeStat(change)}</span>
      </button></li>`).join("")}</ul>`;
  }

  function renderChanges(): string {
    const commits = state!.commits;
    const history = commitsOpen ? commits.slice(0, visibleCommitCount).map(commit => {
      const opened = expandedCommits.has(commit.id);
      const subject = commit.subject || label("noSubject");
      const details = html(label("commitInfo", {
        subject, hash: commit.id, author: commit.author, date: formatLocaleDateTime(commit.date * 1000),
      }));
      return `<section class="wv-commit">
        <div class="wv-commit-heading">
          <button type="button" class="wv-row wv-commit-title" data-wv-toggle-commit="${html(commit.id)}" aria-expanded="${opened}" title="${details}" aria-label="${details}">
            <span class="wv-row-name"><span class="wv-disclosure" aria-hidden="true">${opened ? "▾" : "▸"}</span><span>${html(subject)}</span></span>
            <code>${html(commit.short)}</code>
          </button>
          <button type="button" class="wv-copy-commit" data-wv-copy-commit="${html(commit.id)}" title="${label("copyCommit")}" aria-label="${label("copyCommit")}">${icon("copy")}</button>
          <div class="wv-commit-info" aria-hidden="true">${details}</div>
        </div>
        ${opened ? `<div class="wv-commit-files">${changeList(commit.files, "commit", commit.id)}${commit.truncated ? `<p class="wv-hint">${label("truncatedList")}</p>` : ""}</div>` : ""}
      </section>`;
    }).join("") : "";
    return `${branch ? "" : `<section class="wv-group">
      <button type="button" class="wv-group-title" data-wv-action="uncommitted" aria-expanded="${groupOpen}">
        <span aria-hidden="true">${groupOpen ? "▾" : "▸"}</span> ${label("uncommitted")} <span class="wv-count">${state!.uncommitted.length}</span>
      </button>
      ${groupOpen ? changeList(state!.uncommitted, "uncommitted") : ""}
    </section>`}
    <section class="wv-group">
      <button type="button" class="wv-group-title" data-wv-action="commits" aria-expanded="${commitsOpen}">
        <span aria-hidden="true">${commitsOpen ? "▾" : "▸"}</span> ${state!.base && !state!.baseAtHead ? label("relativeCommits") : label("commits")} <span class="wv-count">${commits.length}</span>
      </button>
      ${commitsOpen ? history || `<p class="wv-hint">${label("noCommits")}</p>` : ""}
      ${commitsOpen && visibleCommitCount < commits.length ? `<button type="button" class="wv-row" data-wv-action="loadMoreCommits">${label("loadMoreCommits")}</button>` : ""}
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
      ${ready ? `<button type="button" data-wv-mode="files" aria-pressed="${mode === "files"}">${icon("folder")}${label(branch ? "worktreeFiles" : "files")}</button>` : ""}`;
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
    tabBar.innerHTML = `<div class="wv-tab-list" aria-label="${label("tabs")}">${tabs.map((tab, index) => {
      const name = fileName(tab.path);
      const repository = tab.repo || repositories[0]?.name || state?.name;
      const duplicate = tabs.some(other => other.repo !== tab.repo && fileName(other.path) === name);
      const caption = duplicate && repository ? `${name} · ${repository}` : name;
      const closeLabel = html(label("closeTab", {file: caption}));
      return `<div class="wv-tab" data-wv-tab="${index}">
        <button type="button" data-wv-select="${index}" aria-pressed="${tab === selected}" title="${html(repository ? `${repository} · ${tab.path}` : tab.path)}">${icon("file")}<span>${html(caption)}</span></button>
        <button type="button" data-wv-close="${index}" title="${closeLabel}" aria-label="${closeLabel}">${icon("close")}</button>
      </div>`;
    }).join("")}</div>
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
    const toolbar = keepScroll ? content.querySelector<HTMLElement>(".wv-toolbar") : null;
    const toolbarScroll = toolbar?.scrollLeft || 0;
    const focusedControl = toolbar?.querySelector(":focus");
    const focusAttr = focusedControl?.getAttributeNames().find(name => name.startsWith("data-wv-"));
    const focusValue = focusAttr ? focusedControl?.getAttribute(focusAttr) : null;
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
    if (!info && !tab.error) body = `<p class="wv-hint" role="status">${t("common.loading")}</p>`;
    else if (placeholder) body = `<p class="wv-hint">${label(placeholder === "too_large" ? "tooLarge" : placeholder)}</p>`;
    else if (info && !info.text) body = `<p class="wv-hint">${label(diff ? "noDiff" : "emptyFile")}</p>`;
    else if (info) body = `<pre class="wv-code ${wrap ? "wv-wrap" : ""}" tabindex="0"><code>${diff ? diffHTML(info.text) : html(info.text)}</code></pre>`;
    const source = tab.scope === "file" ? "" : tab.scope === "uncommitted" ? label("uncommitted")
      : tab.scope === "base" ? label("baseChanges") : tab.commit.slice(0, 7);
    content.innerHTML = `<div class="wv-content-head"><strong title="${html(tab.path)}">${html(tab.path)}</strong>
      ${source ? `<span class="wv-badge">${html(source)}</span>` : ""}
    </div>
    <div class="wv-toolbar">
      ${diff ? `<label title="${label("ignoreWhitespace")}"><input type="checkbox" data-wv-ignore aria-label="${label("ignoreWhitespace")}" title="${label("ignoreWhitespace")}" ${ignoreWhitespace ? "checked" : ""}> ${label("ignoreWhitespace")}</label>` : ""}
      <label title="${label("wrap")}"><input type="checkbox" data-wv-wrap aria-label="${label("wrap")}" title="${label("wrap")}" ${wrap ? "checked" : ""}> ${label("wrap")}</label>
      ${diff ? `<button type="button" data-wv-action="previous" ${info && /^@@/m.test(info.text) ? "" : "disabled"} aria-label="${label("previous")}" title="${label("previous")}">↑ <span>${label("previous")}</span></button>
      <button type="button" data-wv-action="next" ${info && /^@@/m.test(info.text) ? "" : "disabled"} aria-label="${label("next")}" title="${label("next")}">↓ <span>${label("next")}</span></button>` : ""}
    </div>
    ${tab.error ? `<p class="wv-hint" role="alert">${html(tab.error)}</p>` : ""}
    ${info?.truncated ? `<p class="wv-truncated" role="status">${label("truncatedDiff")}</p>` : ""}
    ${body}`;
    const scroll = content.querySelector(".wv-code");
    if (scroll) {
      scroll.scrollTop = top;
      scroll.scrollLeft = left;
    }
    const nextToolbar = content.querySelector<HTMLElement>(".wv-toolbar")!;
    nextToolbar.scrollLeft = toolbarScroll;
    if (focusAttr) {
      [...nextToolbar.querySelectorAll<HTMLElement>(`[${focusAttr}]`)]
        .find(element => element.getAttribute(focusAttr) === focusValue)?.focus({preventScroll: true});
    }
  }

  async function readTab(tab: Tab, background = false): Promise<void> {
    contentRequest?.abort();
    const controller = new AbortController();
    contentRequest = controller;
    const params: Record<string, string> = {path: tab.path};
    if (tab.scope !== "file") {
      params.scope = tab.scope;
      if (tab.branch) params.branch = tab.branch;
      if (tab.scope === "commit") params.commit = tab.commit;
      if (tab.scope === "base" && tab.base) params.base = tab.base;
      if (ignoreWhitespace) params.ignore_whitespace = "1";
    }
    try {
      const value = await read<Content>(tab.scope === "file" ? "file" : "diff", params, controller.signal, tab.repo);
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
        if (tab.branch) delete tab.content;
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

  function open(path: string, scope: Scope, commit = "", repository = repo, comparison = base || (!repo ? state?.base || "" : ""), revision = branch): void {
    const tabBase = scope === "base" ? comparison : "";
    const tabBranch = scope === "file" ? "" : revision;
    let tab = tabs.find(item => item.repo === repository && item.path === path);
    if (!tab) {
      tab = {repo: repository, branch: tabBranch, base: tabBase, path, scope, commit};
      tabs.push(tab);
    } else if (tab.scope !== scope || tab.commit !== commit || tab.base !== tabBase || tab.branch !== tabBranch) {
      Object.assign(tab, {scope, commit, base: tabBase, branch: tabBranch, content: undefined, error: undefined});
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

  function switchRepository(nextRepo: string, recoveryNotice = ""): void {
    repositoryBases.set(repo, base);
    repositoryBranches.set(repo, branch);
    repo = nextRepo;
    base = repositoryBases.get(repo) ?? (repo ? repositoryBases.get("") || "" : "");
    branch = repositoryBranches.get(repo) || "";
    branchMenu = undefined;
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
    commitsOpen = false;
    visibleCommitCount = commitPageSize;
    baseOpen = false;
    saveLocation();
    renderHeader();
    renderNavigation();
    showNotice(t("common.loading"));
    void refresh(false, recoveryNotice);
  }

  function switchBranch(nextBranch: string): void {
    branch = nextBranch;
    mode = "changes";
    initialFile = null;
    state = undefined;
    expandedCommits.clear();
    visibleCommitCount = commitPageSize;
    for (let index = tabs.length - 1; index >= 0; index--) {
      if (tabs[index].repo === repo && tabs[index].scope !== "file") tabs.splice(index, 1);
    }
    if (selected && !tabs.includes(selected)) select(undefined);
    else renderTabs();
    saveLocation();
    renderHeader();
    renderNavigation();
    showNotice(t("common.loading"));
    void refresh();
  }

  async function refresh(poll = false, recoveryNotice = ""): Promise<void> {
    if (!task) return;
    if (poll && (document.hidden || statusRequest)) return;
    statusRequest?.abort();
    const controller = new AbortController();
    statusRequest = controller;
    try {
      const branchParam: Record<string, string> = branch ? {branch} : {};
      let fetchFull = !poll || !state;
      if (!fetchFull) {
        const summary = await read<Pick<ViewerState, "version" | "isGit">>("state", {...branchParam, summary: "1"}, controller.signal);
        fetchFull = summary.isGit !== state?.isGit || summary.version !== state?.version;
      }
      if (controller.signal.aborted) return;
      if (fetchFull) {
        const params = base ? {...branchParam, base} : branchParam;
        const next = await read<ViewerState>("state", params, controller.signal);
        if (controller.signal.aborted) return;
        const first = !loaded;
        loaded = true;
        const latest = next.commits?.[0]?.id;
        if (latest && latest !== state?.commits?.[0]?.id) expandedCommits.add(latest);
        const lostGit = state?.isGit && !next.isGit && !next.repositoryUnavailable;
        state = next;
        const checkedOut = next.checkedOutBranch || (!branch ? next.branch : "");
        branchMenu = next.isGit ? {
          options: next.branchOptions || [],
          current: checkedOut || (!branch && next.head ? label("detached", {head: next.head.slice(0, 8)}) : label("currentBranch")),
          title: checkedOut || (!branch && next.head ? label("detached", {head: next.head}) : label("currentBranch")),
          checkedOutRef: checkedOut ? `refs/heads/${checkedOut}` : "",
        } : undefined;
        repositories = next.repositories || [];
        document.title = `${next.name} · AHA2`;
        if (!next.isGit && !next.repositoryUnavailable) {
          mode = "files";
          base = "";
        }
        if (lostGit) {
          for (const tab of tabs) {
            if (tab.repo === repo && tab.scope !== "file") Object.assign(tab, {scope: "file", commit: "", base: "", branch: "", content: undefined, error: undefined});
          }
          select(selected);
        }
        renderHeader();
        renderNavigation();
        if (first && initialFile && (!next.repositoryUnavailable || (initialFileRepo !== null && initialFileRepo !== repo))) {
          const fileRepo = initialFileRepo ?? repo;
          const fileIsGit = fileRepo === repo ? next.isGit : repositories.some(item => item.id === fileRepo);
          const scope = fileIsGit && (initialScope === "uncommitted" || initialScope === "commit" || initialScope === "base")
            ? initialScope : "file";
          if (mode === "files" && fileRepo === repo) {
            for (let parent = initialFile.lastIndexOf("/"); parent > 0; parent = initialFile.lastIndexOf("/", parent - 1)) {
              expandedFolders.add(initialFile.slice(0, parent));
            }
          }
          open(initialFile, scope, url.searchParams.get("commit") || "", fileRepo,
            fileRepo === repo ? undefined : initialFileBase || "", initialFileBranch ?? (fileRepo === repo ? branch : ""));
        }
        initialFile = null;
        renderTabs();
        if (!selected) renderContent();
        saveLocation();
      }
      const notices = [recoveryNotice];
      if (state?.repositoriesTruncated) {
        notices.push(label("repositoriesTruncated"));
        if (state.repositoryDeadlineReached) notices.push(label("repositoryDeadlineReached"));
        if (state.repositorySizeReached) notices.push(label("repositorySizeReached"));
      }
      if (state?.repositoryUnavailable) notices.push(label("repositoryUnavailable"));
      showNotice(notices.filter(Boolean).join(" "));
      if (state?.repositoryUnavailable) return;
      if (mode === "files") {
        const activePath = selected?.repo === repo ? selected.path : "";
        const selectedFolder = activePath.slice(0, Math.max(0, activePath.lastIndexOf("/")));
        const folders = poll ? [...new Set(["", selectedFolder])].filter(path => expandedFolders.has(path)) : [...expandedFolders];
        void Promise.all(folders.map(readTree));
      }
      if (selected?.repo === repo && !contentRequest) void readTab(selected, true);
    } catch (reason) {
      if (controller.signal.aborted) return;
      if (!loaded && repo && !branch && (reason as {code?: string})?.code === "workspace_viewer_invalid_repository") {
        switchRepository("", label("repositoryRecovered"));
      } else {
        const message = errorText(reason);
        if (branch && state) {
          state = undefined;
          renderHeader();
          renderNavigation();
          if (selected?.repo === repo && selected.branch === branch) {
            contentRequest?.abort();
            contentRequest = undefined;
            delete selected.content;
            selected.error = message;
            renderContent();
          }
        }
        showNotice(message);
      }
    } finally {
      if (statusRequest === controller) statusRequest = undefined;
    }
  }

  navigation.addEventListener("click", event => {
    const button = (event.target as Element).closest<HTMLButtonElement>("button");
    if (!button || !state) return;
    const {wvFolder, wvPath, wvScope, wvCommit, wvToggleCommit, wvCopyCommit, wvAction} = button.dataset;
    if (wvCopyCommit) {
      copyStatus.textContent = "";
      void copyText(wvCopyCommit).then(
        () => { copyStatus.textContent = label("copiedCommit"); },
        () => { copyStatus.textContent = label("copyCommitFailed"); },
      );
      return;
    }
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
    } else if (wvAction === "commits") {
      commitsOpen = !commitsOpen;
    } else if (wvAction === "loadMoreCommits") {
      visibleCommitCount += commitPageSize;
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
      switchRepository(input.value);
      return;
    }
    if (input.hasAttribute("data-wv-branch")) {
      switchBranch(input.value);
      return;
    }
    if (!input.hasAttribute("data-wv-base")) return;
    base = input.value;
    visibleCommitCount = commitPageSize;
    for (const tab of tabs) {
      if (tab.repo !== repo || tab.branch !== branch || tab.scope !== "base") continue;
      tab.base = base;
      delete tab.content;
      delete tab.error;
      if (tab === selected) {
        contentRequest?.abort();
        contentRequest = undefined;
        renderContent();
      }
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
  content.addEventListener("focusin", event => {
    (event.target as HTMLElement).closest(".wv-toolbar :focus-visible")
      ?.scrollIntoView({block: "nearest", inline: "nearest"});
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
