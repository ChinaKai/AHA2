import {api} from "./api.js";
import {currentLocale} from "./i18n.js";
import {loadStylesheet, loadTerminalAssets} from "./terminal_assets.js";
import {icon} from "./icons.js";
import type {SecuritySettings} from "./types.js";

interface SessionInfo {
  id: string;
  number: number;
}

interface TerminalState {
  locked?: boolean;
  cwd?: string;
  ssh?: boolean;
  sessions?: SessionInfo[];
}

interface TerminalView {
  element: HTMLElement;
  show(parent: HTMLElement): void;
  focus(): void;
  dispose(): void;
}

type Pane = 0 | 1;

const copy = {
  "zh-CN": {
    workspace: "切换工作区",
    title: "终端",
    sessions: "终端会话",
    session: "终端 {number}",
    close: "关闭 {name}",
    new: "在当前工作区主机新建终端",
    split: "分屏（左右两栏）",
    unsplit: "取消分屏",
    collapse: "收起终端",
    expand: "展开终端",
    maximize: "最大化终端",
    restore: "恢复终端高度",
    resize: "拖动调整高度，双击恢复默认高度",
    locked: "局域网终端已锁定 · 请在本机设置开启",
    settings: "打开终端设置",
    settingsTitle: "工作区终端",
    settingsAllowLAN: "允许通过局域网访问终端",
    settingsAllowLANHelp: "默认关闭。在运行 AHA2 的电脑上通过 localhost 开启。Owner 可以从局域网关闭。",
    settingsExternalOrigin: "终端外部访问地址",
    settingsExternalOriginHelp: "通过反向代理访问时，填写浏览器使用的协议、域名和端口。留空则沿用直连校验。",
    settingsInvalidExternalOrigin: "请输入有效的 HTTP 或 HTTPS 地址，地址只应包含协议、主机和可选端口。",
    settingsEnableRequiresLoopback: "请在运行 AHA2 的电脑上通过 localhost 开启。",
    settingsSaved: "工作区终端设置已保存。",
    settingsSave: "保存终端设置",
    settingsSaving: "正在保存…",
    empty: "用 ＋ 新建终端，或选择已有标签。",
    unavailable: "当前工作区的终端不可用。",
    unsupported: "当前工作区不支持终端。",
    input: "终端输入",
    connecting: "正在连接…",
    disconnected: "连接已断开",
    componentFailed: "终端组件无法加载。",
    startFailed: "工作区主机启动终端失败，请查看服务端日志。",
    checkFailed: "暂时无法确认终端访问权限，请重试。",
    accessRevoked: "终端访问权限已撤销，请检查登录状态和局域网终端设置。",
    originRejected: "终端请求来源校验未通过，请检查终端外部访问地址和反向代理设置。",
    invalidSize: "终端尺寸超出允许范围，请调整窗口大小后重试。",
    invalidTarget: "终端请求指定的工作区无效，请重新选择工作区。",
    taskNotFound: "找不到该任务，请返回任务列表确认。",
    requestFailed: "创建终端的请求失败，请重试。",
    closeFailed: "无法关闭终端，请重试。",
    limit: "当前工作区最多开 6 个终端。",
  },
  "en-US": {
    workspace: "Switch workspace",
    title: "Terminal",
    sessions: "Terminal sessions",
    session: "Terminal {number}",
    close: "Close {name}",
    new: "New terminal on this workspace host",
    split: "Split terminal side by side",
    unsplit: "Close split view",
    collapse: "Collapse terminal",
    expand: "Expand terminal",
    maximize: "Maximize terminal",
    restore: "Restore terminal height",
    resize: "Drag to resize, double-click to reset the height",
    locked: "LAN terminal locked · Enable in settings on the host",
    settings: "Open terminal settings",
    settingsTitle: "Workspace terminal",
    settingsAllowLAN: "Allow workspace terminals over LAN",
    settingsAllowLANHelp: "Off by default. Enable via localhost on the computer running AHA2. The owner can disable it over the LAN.",
    settingsExternalOrigin: "External terminal address",
    settingsExternalOriginHelp: "If you use a reverse proxy, enter the scheme, host, and port used by your browser. Leave blank to use direct-connection checks.",
    settingsInvalidExternalOrigin: "Enter a valid HTTP or HTTPS address with a scheme, host, and optional port.",
    settingsEnableRequiresLoopback: "To enable LAN terminals, open this page via localhost on the computer running AHA2.",
    settingsSaved: "Workspace terminal setting saved.",
    settingsSave: "Save terminal setting",
    settingsSaving: "Saving…",
    empty: "Open a terminal with + or select an existing tab.",
    unavailable: "Terminal unavailable for this workspace.",
    unsupported: "This workspace does not support terminals.",
    input: "Terminal input",
    connecting: "Connecting…",
    disconnected: "Disconnected",
    componentFailed: "Unable to load terminal component.",
    startFailed: "The workspace host could not start the terminal. Check the server logs.",
    checkFailed: "Terminal access could not be verified. Try again.",
    accessRevoked: "Terminal access was revoked. Check your login and LAN terminal settings.",
    originRejected: "Terminal origin check failed. Check the external terminal address and reverse proxy settings.",
    invalidSize: "Terminal dimensions are out of range. Resize the window and try again.",
    invalidTarget: "The terminal request specifies an invalid workspace. Select the workspace again.",
    taskNotFound: "Task not found. Check the task list.",
    requestFailed: "Could not create the terminal. Try again.",
    closeFailed: "Unable to close the terminal. Try again.",
    limit: "This workspace can have up to 6 terminals.",
  },
};

export function terminalLabel(key: keyof typeof copy["en-US"], values: Record<string, string | number> = {}): string {
  return copy[currentLocale()][key].replace(/\{(\w+)\}/g, (_, name) => String(values[name] ?? ""));
}
const label = terminalLabel;
const other = (pane: Pane): Pane => pane === 0 ? 1 : 0;
const nameOf = (session: SessionInfo) => label("session", {number: session.number});

function errorCode(reason: unknown): string | undefined {
  return (reason as {code?: string})?.code;
}

export function renderWorkspaceTerminalSettings(settings: SecuritySettings, escapeHTML: (value: string) => string): string {
  return `<section class="panel spaced account-security-panel">
    <div class="panel-head"><strong>${label("settingsTitle")}</strong></div>
    <form id="workspace-terminal-settings-form">
      <label class="security-setting-toggle"><input name="workspace_terminal_lan" type="checkbox" ${settings.workspace_terminal_lan ? "checked" : ""}><span><strong>${label("settingsAllowLAN")}</strong><small>${label("settingsAllowLANHelp")}</small></span></label>
      <label>${label("settingsExternalOrigin")}<input name="workspace_terminal_external_origin" type="url" value="${escapeHTML(settings.workspace_terminal_external_origin || "")}" placeholder="https://aha.example.com" aria-describedby="workspace-terminal-external-origin-help"></label>
      <div class="field-help" id="workspace-terminal-external-origin-help">${label("settingsExternalOriginHelp")}</div>
      <div class="dialog-actions"><button class="primary" type="submit">${label("settingsSave")}</button></div>
    </form>
  </section>`;
}

export async function saveWorkspaceTerminalSettings(form: FormData, settings: SecuritySettings): Promise<SecuritySettings> {
  const lan = form.get("workspace_terminal_lan") === "on";
  const origin = String(form.get("workspace_terminal_external_origin") || "").trim();
  const payload = {
    workspace_terminal_lan: lan !== Boolean(settings.workspace_terminal_lan) ? lan : undefined,
    workspace_terminal_external_origin: origin !== (settings.workspace_terminal_external_origin || "") ? origin : undefined,
  };
  if (payload.workspace_terminal_lan === undefined && payload.workspace_terminal_external_origin === undefined) return settings;
  try {
    return (await api.updateWorkspaceTerminalSettings(payload)).security;
  } catch (error) {
    switch (errorCode(error)) {
      case "workspace_terminal_lan_enable_requires_loopback": throw new Error(label("settingsEnableRequiresLoopback"));
      case "workspace_terminal_invalid_external_origin": throw new Error(label("settingsInvalidExternalOrigin"));
      default: throw error;
    }
  }
}

function terminalError(code: string | undefined, fallback: "unavailable" | "requestFailed" | "closeFailed"): string {
  switch (code) {
    case "origin_rejected": return label("originRejected");
    case "workspace_terminal_invalid_size": return label("invalidSize");
    case "workspace_terminal_invalid_target": return label("invalidTarget");
    case "task_not_found": return label("taskNotFound");
    case "workspace_terminal_unsupported": return label("unsupported");
    case "workspace_terminal_unavailable": return label("unavailable");
    case "workspace_terminal_start_failed": return label("startFailed");
    case "workspace_terminal_check_failed": return label("checkFailed");
    case "workspace_terminal_access_revoked": return label("accessRevoked");
    case "workspace_terminal_limit": return label("limit");
    default: return label(fallback);
  }
}

function makeTerminalView(endpoint: string, session: SessionInfo, cwd: string, ssh: boolean, socketClosed: () => void): TerminalView {
  const element = document.createElement("div");
  element.className = "wv-term-view";
  element.innerHTML = '<div class="wv-term-pane-head"><span class="wv-term-dot"></span><strong></strong><span class="wv-term-cwd"></span><span class="wv-term-connection" role="status"></span></div><div class="wv-term-screen"></div>';
  element.classList.toggle("ssh", ssh);
  element.querySelector("strong")!.textContent = nameOf(session) + (ssh ? " · SSH" : "");
  const directory = element.querySelector<HTMLElement>(".wv-term-cwd")!;
  directory.title = directory.textContent = cwd;
  const screen = element.querySelector<HTMLElement>(".wv-term-screen")!;
  const connection = element.querySelector<HTMLElement>(".wv-term-connection")!;
  const Constructor = window.Terminal;
  if (!Constructor) {
    connection.textContent = label("componentFailed");
    return {element, show: parent => parent.append(element), focus() {}, dispose: () => element.remove()};
  }
  const terminal = new Constructor({
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace',
    fontSize: 13, scrollback: 3000, cursorBlink: true, disableStdin: true,
    theme: {background: "#0d1117", foreground: "#e6edf3", cursor: "#e6edf3", selectionBackground: "#304565"},
  });
  const encoder = new TextEncoder();
  let socket: WebSocket | undefined;
  let opened = false;
  let disposed = false;
  let attempts = 0;
  let reconnect = 0;
  let lastSize = "";

  function send(data: Uint8Array): void {
    if (socket?.readyState !== WebSocket.OPEN) return;
    for (let i = 0; i < data.byteLength; i += 32_768) socket.send(data.subarray(i, i + 32_768));
  }

  terminal.onData(data => send(encoder.encode(data)));
  terminal.onBinary(data => send(Uint8Array.from(data, value => value.charCodeAt(0))));

  function fit(): void {
    if (!opened || screen.clientWidth === 0 || screen.clientHeight === 0) return;
    const rendered = screen.querySelector<HTMLElement>(".xterm-screen")?.getBoundingClientRect();
    const cellWidth = rendered?.width ? rendered.width / terminal.cols : 8;
    const cellHeight = rendered?.height ? rendered.height / terminal.rows : 16;
    const viewport = screen.querySelector<HTMLElement>(".xterm-viewport");
    const width = Math.min(screen.clientWidth, viewport?.clientWidth || screen.clientWidth);
    const cols = Math.max(2, Math.min(500, Math.floor(width / cellWidth)));
    const rows = Math.max(1, Math.min(250, Math.floor(screen.clientHeight / cellHeight)));
    if (cols !== terminal.cols || rows !== terminal.rows) terminal.resize(cols, rows);
    const size = JSON.stringify({cols, rows});
    if (socket?.readyState === WebSocket.OPEN && size !== lastSize) {
      socket.send(size);
      lastSize = size;
    }
  }

  const observer = new ResizeObserver(fit);
  observer.observe(screen);

  function connect(): void {
    if (disposed) return;
    connection.textContent = label("connecting");
    const url = new URL(endpoint, location.href);
    url.pathname += "/" + encodeURIComponent(session.id) + "/ws";
    url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
    const ws = new WebSocket(url);
    socket = ws;
    ws.binaryType = "arraybuffer";
    ws.onopen = () => {
      if (disposed) return;
      attempts = 0;
      lastSize = "";
      connection.textContent = "";
      terminal.options.disableStdin = false;
      // A reconnect starts with the server's bounded transcript. Reset in
      // xterm's write queue so pending frames from the old socket come first.
      terminal.write("\x1bc");
      fit();
    };
    ws.onmessage = event => {
      if (disposed || !(event.data instanceof ArrayBuffer)) return;
      terminal.write(new Uint8Array(event.data));
    };
    ws.onclose = event => {
      if (disposed) return;
      terminal.options.disableStdin = true;
      connection.textContent = label("disconnected");
      socketClosed();
      if (event.code === 1000 && event.reason === "terminal ended") return;
      reconnect = window.setTimeout(connect, Math.min(1000 * 2 ** attempts++, 15_000));
    };
  }

  return {
    element,
    show(parent) {
      if (element.parentElement !== parent) parent.append(element);
      if (opened) return;
      terminal.open(screen);
      terminal.textarea?.setAttribute("inputmode", "text");
      terminal.textarea?.setAttribute("enterkeyhint", "enter");
      terminal.textarea?.setAttribute("aria-label", label("input"));
      opened = true;
      fit();
      connect();
    },
    focus() { if (opened) terminal.focus(); },
    dispose() {
      disposed = true;
      clearTimeout(reconnect);
      observer.disconnect();
      socket?.close();
      terminal.dispose();
      element.remove();
    },
  };
}

function mountDrawer(host: HTMLElement, endpoint: string, layoutKey: string): {refresh(): Promise<void>; shortcut(event: KeyboardEvent): void; dispose(): void} {
  const element = document.createElement("section");
  element.className = "wv-terminal";
  element.setAttribute("aria-label", label("title"));
  element.innerHTML = `
    <div class="wv-term-resizer" title="${label("resize")}"></div>
    <div class="wv-term-bar">
      <button type="button" class="wv-term-toggle" aria-keyshortcuts="Control+&#96;">${icon("chevron-down")}</button>
      <span class="wv-term-brand"><span aria-hidden="true">&gt;_</span>${label("title")}</span>
      <a class="wv-term-lock" href="/#other" target="_blank" rel="noopener" hidden>${label("locked")}</a>
      <div class="wv-term-tabs" role="tablist" aria-label="${label("sessions")}"></div>
      <div class="wv-term-actions">
        <button type="button" data-wvt-new aria-label="${label("new")}" title="${label("new")}">＋</button>
        <button type="button" data-wvt-split>▥</button>
        <button type="button" data-wvt-max>⤢</button>
      </div>
    </div>
    <div class="wv-term-feedback" role="status" hidden></div>
    <div class="wv-term-body">
      <div class="wv-term-pane" data-pane="0"></div>
      <div class="wv-term-pane" data-pane="1" hidden></div>
    </div>
    <div class="wv-term-parking" hidden></div>`;
  host.append(element);

  const bar = element.querySelector<HTMLElement>(".wv-term-bar")!;
  const toggleButton = element.querySelector<HTMLButtonElement>(".wv-term-toggle")!;
  const lockLink = element.querySelector<HTMLAnchorElement>(".wv-term-lock")!;
  const feedback = element.querySelector<HTMLElement>(".wv-term-feedback")!;
  const tabs = element.querySelector<HTMLElement>(".wv-term-tabs")!;
  const actions = element.querySelector<HTMLElement>(".wv-term-actions")!;
  const newButton = actions.querySelector<HTMLButtonElement>("[data-wvt-new]")!;
  const splitButton = actions.querySelector<HTMLButtonElement>("[data-wvt-split]")!;
  const maxButton = actions.querySelector<HTMLButtonElement>("[data-wvt-max]")!;
  const resizer = element.querySelector<HTMLElement>(".wv-term-resizer")!;
  const body = element.querySelector<HTMLElement>(".wv-term-body")!;
  body.id = "wv-term-body-" + encodeURIComponent(layoutKey);
  toggleButton.setAttribute("aria-controls", body.id);
  const panels = [...body.querySelectorAll<HTMLElement>(".wv-term-pane")];
  const parking = element.querySelector<HTMLElement>(".wv-term-parking")!;
  const editor = element.closest<HTMLElement>(".wv-editor");
  const editorTabs = editor?.querySelector(".wv-tabs");
  const empty = panels.map(panel => {
    const note = document.createElement("div");
    note.className = "wv-term-empty";
    note.textContent = label("empty");
    panel.append(note);
    return note;
  });
  const views = new Map<string, TerminalView>();
  const selection = ["", ""];
  let sessions: SessionInfo[] = [];
  let cwd = "";
  let ssh = false;
  let expanded = false;
  let split = false;
  let focused: Pane = 0;
  let maximized = false;
  let ready = false;
  let locked = false;
  let busy = false;
  let refreshing = false;
  let refreshPending = false;
  let disposed = false;
  let autoStarted = false;
  let error = "";
  let height = 0;
  // Closing the last shell suppresses auto-creation even if the drawer is open.
  let explicitlyClosed = false;
  try {
    height = Number(localStorage.getItem("aha2.workspaceTerminal.height")) || 0;
  } catch {}
  try {
    const saved = sessionStorage.getItem(layoutKey);
    if (saved === "opened" || saved === "closed") {
      expanded = saved === "opened";
      explicitlyClosed = saved === "closed";
    } else if (saved) {
      const layout = JSON.parse(saved);
      expanded = layout.expanded === true;
      explicitlyClosed = layout.explicitlyClosed === true;
    }
  } catch {
    // Storage may be disabled in a private browser.
  }

  function buttonLabel(button: HTMLButtonElement, name: keyof typeof copy["en-US"]): void {
    button.title = label(name);
    button.setAttribute("aria-label", button.title);
  }

  function updateHeight(): void {
    const reserve = Math.max(18, editorTabs?.clientHeight || 0);
    const max = Math.max(37, (editor?.clientHeight || innerHeight) - reserve);
    const normal = height || Math.round(innerHeight * 0.42);
    element.style.setProperty("--drawer-height", (maximized ? max : Math.min(max, Math.max(160, normal))) + "px");
  }

  function renderTabs(): void {
    const previous = (document.activeElement as HTMLElement | null)?.closest<HTMLButtonElement>(".wv-term-tab button");
    tabs.replaceChildren();
    for (const session of sessions) {
      const tab = document.createElement("div");
      tab.className = "wv-term-tab";
      tab.classList.toggle("active", selection[focused] === session.id);
      tab.classList.toggle("in-other", split && selection[other(focused)] === session.id);
      tab.classList.toggle("ssh", ssh);
      const pick = document.createElement("button");
      pick.type = "button";
      pick.dataset.session = session.id;
      pick.setAttribute("role", "tab");
      pick.setAttribute("aria-selected", String(selection[focused] === session.id));
      const dot = document.createElement("span");
      dot.className = "wv-term-dot";
      const title = document.createElement("span");
      title.textContent = nameOf(session);
      pick.append(dot, title);
      if (ssh) {
        const badge = document.createElement("span");
        badge.className = "wv-term-ssh";
        badge.textContent = "SSH";
        pick.append(badge);
      }
      const close = document.createElement("button");
      close.type = "button";
      close.disabled = busy || refreshing;
      close.dataset.close = session.id;
      close.textContent = "×";
      close.title = label("close", {name: nameOf(session)});
      close.setAttribute("aria-label", close.title);
      tab.append(pick, close);
      tabs.append(tab);
      if (previous?.dataset.session === session.id) pick.focus({preventScroll: true});
      if (previous?.dataset.close === session.id) close.focus({preventScroll: true});
    }
  }

  function render(): void {
    if (disposed) return;
    const open = ready && expanded && !locked;
    element.classList.toggle("collapsed", !open);
    element.classList.toggle("locked", locked);
    lockLink.hidden = !locked;
    lockLink.title = label("locked");
    feedback.textContent = error;
    feedback.title = error;
    feedback.hidden = !error;
    actions.hidden = locked || !ready;
    tabs.hidden = locked;
    body.hidden = !open;
    resizer.hidden = !open;
    toggleButton.disabled = !ready && !locked;
    toggleButton.setAttribute("aria-expanded", String(open));
    buttonLabel(toggleButton, locked ? "settings" : open ? "collapse" : "expand");
    if (!locked) toggleButton.title += " (Ctrl+`)";
    newButton.disabled = busy || refreshing || sessions.length >= 6;
    splitButton.setAttribute("aria-pressed", String(split));
    buttonLabel(splitButton, split ? "unsplit" : "split");
    maxButton.setAttribute("aria-pressed", String(maximized));
    buttonLabel(maxButton, maximized ? "restore" : "maximize");
    maxButton.textContent = maximized ? "⤡" : "⤢";
    updateHeight();
    renderTabs();
    panels[1].hidden = !split;
    panels.forEach((panel, i) => {
      panel.classList.toggle("focused", i === focused);
      const id = i === 1 && !split ? "" : selection[i];
      const child = panel.querySelector(".wv-term-view");
      const view = views.get(id);
      if (child && child !== view?.element) parking.append(child);
      empty[i].hidden = Boolean(id);
      if (!open || !id) return;
      const session = sessions.find(item => item.id === id);
      if (!session) return;
      const active = view || makeTerminalView(endpoint, session, cwd, ssh, () => { void refresh(); });
      views.set(id, active);
      active.show(panel);
    });
  }

  function selectAvailable(): void {
    if (!sessions.some(item => item.id === selection[0])) selection[0] = sessions[0]?.id || "";
    if (!selection[1] || selection[0] === selection[1] || !sessions.some(item => item.id === selection[1])) {
      selection[1] = sessions.find(item => item.id !== selection[0])?.id || "";
    }
  }

  function saveLayout(): void {
    try { sessionStorage.setItem(layoutKey, JSON.stringify({expanded, explicitlyClosed})); } catch {}
  }

  function setExpanded(value: boolean): void {
    expanded = value;
    saveLayout();
  }

  async function autoStart(): Promise<void> {
    if (!ready || locked || busy || refreshing || !expanded || host.hidden || sessions.length || autoStarted || explicitlyClosed) return;
    autoStarted = true;
    await create(true);
  }

  function pruneViews(): void {
    for (const [id, view] of views) {
      if (!sessions.some(item => item.id === id)) {
        view.dispose();
        views.delete(id);
      }
    }
  }

  async function refresh(): Promise<void> {
    if (disposed) return;
    if (busy || refreshing) {
      refreshPending = true;
      return;
    }
    refreshing = true;
    for (const button of element.querySelectorAll<HTMLButtonElement>("[data-wvt-new], [data-close]")) button.disabled = true;
    try {
      const data = await api.request<TerminalState>(endpoint);
      if (disposed) return;
      locked = Boolean(data.locked);
      if (!locked) await loadTerminalAssets();
      if (disposed) return;
      ready = !locked;
      error = "";
      sessions = data.sessions || [];
      cwd = data.cwd || "";
      ssh = Boolean(data.ssh);
    } catch (reason) {
      const code = errorCode(reason);
      if (!ready || code) {
        ready = false;
        sessions = [];
        locked = code === "workspace_terminal_lan_locked";
      }
      error = locked ? "" : terminalError(code, "unavailable");
    } finally {
      refreshing = false;
      selectAvailable();
      pruneViews();
      render();
    }
    await autoStart();
    if (refreshPending) {
      refreshPending = false;
      void refresh();
    }
  }

  async function create(initial = false): Promise<void> {
    if (!ready || locked || busy || refreshing || disposed) return;
    if (!initial) setExpanded(true);
    busy = true;
    error = "";
    render();
    try {
      const info = await api.request<SessionInfo>(endpoint, {
        method: "POST", body: JSON.stringify({cols: 80, rows: 24, initial}),
      });
      if (disposed) return;
      explicitlyClosed = false;
      saveLayout();
      sessions.push(info);
      selection[focused] = info.id;
      selectAvailable();
    } catch (reason) {
      const code = errorCode(reason);
      if (code === "workspace_terminal_already_exists") window.setTimeout(() => { void refresh(); }, 500);
      else if (code === "workspace_terminal_lan_locked") {
        locked = true;
        ready = false;
        refreshPending = true;
      } else error = terminalError(code, "requestFailed");
    } finally {
      busy = false;
      render();
      if (!initial) focusTerminal();
      if (refreshPending) {
        refreshPending = false;
        void refresh();
      }
    }
  }

  async function close(id: string): Promise<void> {
    if (busy || refreshing || locked) return;
    busy = true;
    const url = new URL(endpoint, location.href);
    url.pathname += "/" + encodeURIComponent(id);
    try {
      await api.request(url.pathname + url.search, {method: "DELETE"});
    } catch (reason) {
      const code = errorCode(reason);
      if (code !== "workspace_terminal_not_found") {
        locked = code === "workspace_terminal_lan_locked";
        error = locked ? "" : terminalError(code, "closeFailed");
        busy = false;
        render();
        void refresh();
        return;
      }
    }
    sessions = sessions.filter(item => item.id !== id);
    if (sessions.length === 0) {
      explicitlyClosed = true;
      saveLayout();
    }
    selectAvailable();
    pruneViews();
    busy = false;
    render();
    focusTerminal();
    if (refreshPending) {
      refreshPending = false;
      void refresh();
    }
  }

  function focusTerminal(): void {
    if (!host.hidden && expanded && ready) views.get(selection[focused])?.focus();
  }

  function toggle(): void {
    if (locked) {
      lockLink.click();
      return;
    }
    if (!ready) return;
    setExpanded(!expanded);
    render();
    if (expanded) {
      focusTerminal();
      void autoStart();
    } else {
      toggleButton.focus({preventScroll: true});
    }
  }

  bar.addEventListener("click", event => {
    const target = event.target as Element;
    if (target.closest(".wv-term-toggle") || !target.closest("a, button, .wv-term-tab")) toggle();
  });
  newButton.addEventListener("click", () => { void create(); });
  splitButton.addEventListener("click", () => {
    split = !split;
    if (!split && focused === 1) {
      selection[0] = selection[1] || selection[0];
      focused = 0;
      selectAvailable();
    }
    setExpanded(true);
    render();
    focusTerminal();
    void autoStart();
  });
  maxButton.addEventListener("click", () => {
    setExpanded(true);
    maximized = !maximized;
    render();
    focusTerminal();
    void autoStart();
  });
  tabs.addEventListener("click", event => {
    const button = (event.target as Element).closest<HTMLButtonElement>("button");
    const id = button?.dataset.session;
    if (button?.dataset.close) void close(button.dataset.close);
    if (!id) return;
    if (split && selection[other(focused)] === id) focused = other(focused);
    else selection[focused] = id;
    setExpanded(true);
    render();
    focusTerminal();
  });
  body.addEventListener("focusin", selectPane);
  body.addEventListener("pointerdown", selectPane);

  function selectPane(event: Event): void {
    const panel = (event.target as Element).closest<HTMLElement>("[data-pane]");
    const index = panel?.dataset.pane === "1" ? 1 : 0;
    if (focused === index) return;
    focused = index;
    panels.forEach((pane, i) => pane.classList.toggle("focused", i === focused));
    renderTabs();
  }

  let drag: {id: number; y: number; height: number} | undefined;
  resizer.addEventListener("pointerdown", event => {
    if (event.button !== 0) return;
    event.preventDefault();
    drag = {id: event.pointerId, y: event.clientY, height: element.clientHeight};
    resizer.setPointerCapture(event.pointerId);
    maximized = false;
  });
  resizer.addEventListener("pointermove", event => {
    if (drag?.id !== event.pointerId) return;
    height = Math.max(160, drag.height + drag.y - event.clientY);
    updateHeight();
  });
  function saveHeight(): void {
    try { localStorage.setItem("aha2.workspaceTerminal.height", String(height)); } catch {}
  }
  resizer.addEventListener("lostpointercapture", () => {
    if (!drag) return;
    drag = undefined;
    saveHeight();
    render();
  });
  resizer.addEventListener("dblclick", () => {
    height = 0;
    maximized = false;
    saveHeight();
    render();
  });
  const layout = new ResizeObserver(updateHeight);
  if (editor) layout.observe(editor);
  if (editorTabs) layout.observe(editorTabs);
  window.addEventListener("resize", updateHeight);

  render();
  return {
    refresh,
    shortcut(event: KeyboardEvent) {
      if (locked || !ready) return;
      event.preventDefault();
      event.stopPropagation();
      if (!event.repeat) toggle();
    },
    dispose() {
      disposed = true;
      for (const view of views.values()) view.dispose();
      window.removeEventListener("resize", updateHeight);
      layout.disconnect();
      element.remove();
    },
  };
}

export function workspaceTerminals(slot: HTMLElement, task: string): {show(workspace: string): void} {
  const drawers = new Map<string, {host: HTMLElement; drawer: ReturnType<typeof mountDrawer>}>();
  let active: ReturnType<typeof mountDrawer> | undefined;
  let current = "";
  const refresh = () => { void active?.refresh(); };
  function shortcut(event: KeyboardEvent): void {
    if (!active || event.defaultPrevented || !event.ctrlKey || event.altKey || event.metaKey || event.code !== "Backquote") return;
    const target = event.target;
    if (target instanceof HTMLElement && (target.isContentEditable || target.closest("input, textarea, select")) &&
        !target.matches(".wv-term-screen textarea.xterm-helper-textarea")) return;
    active.shortcut(event);
  }
  function dispose(event: PageTransitionEvent): void {
    if (event.persisted) return;
    for (const {drawer} of drawers.values()) drawer.dispose();
    window.removeEventListener("focus", refresh);
    window.removeEventListener("pageshow", refresh);
    window.removeEventListener("pagehide", dispose);
    window.removeEventListener("keydown", shortcut, true);
  }
  window.addEventListener("focus", refresh);
  window.addEventListener("pageshow", refresh);
  window.addEventListener("pagehide", dispose);
  window.addEventListener("keydown", shortcut, true);
  return {
    show(workspace) {
      if (!task) return;
      if (current === workspace && active) {
        refresh();
        return;
      }
      current = workspace;
      for (const {host} of drawers.values()) host.hidden = true;
      active = undefined;
      void loadStylesheet("/workspace-terminal.css").then(() => {
        if (current === workspace && slot.isConnected) showDrawer(workspace);
      }).catch(() => {
        if (current === workspace) slot.textContent = label("componentFailed");
      });
    },
  };

  function showDrawer(workspace: string): void {
    let entry = drawers.get(workspace);
    if (!entry) {
      const host = document.createElement("div");
      if (!drawers.size) slot.replaceChildren();
      slot.append(host);
      const path = "/api/v1/tasks/" + encodeURIComponent(task) + "/workspace-terminal";
      const endpoint = workspace ? path + "?" + new URLSearchParams({workspace}) : path;
      entry = {host, drawer: mountDrawer(host, endpoint, "aha2.workspaceTerminal." + endpoint)};
      drawers.set(workspace, entry);
    }
    for (const {host} of drawers.values()) host.hidden = host !== entry.host;
    active = entry.drawer;
    refresh();
  }
}
