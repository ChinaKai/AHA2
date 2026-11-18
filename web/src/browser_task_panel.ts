// Lazy chunk for the Task shared-control "内置浏览器" mode. The targets
// popover only renders the compact profile/attach form (`selector`); the tab
// strip, address bar, bookmark bar and read-only preview live in the main
// panel (`container`), so picking a target never grows the popover into a
// second browser window.
import {api} from "./api.js";
import {icon} from "./icons.js";
import type {BrowserBookmark, BrowserHostView, BrowserProfile, BrowserSession, BrowserTab, BrowserTaskGrant} from "./types.js";

type Notice = (type: "error" | "notice", message: string) => void;

export interface BrowserTaskPanelOptions {
  // Main-panel control surface: tab strip, address bar, bookmark bar, preview.
  container: HTMLElement;
  // Compact "共享目标" popover surface: profile picker and one primary action.
  selector?: HTMLElement | null;
  taskID: string;
  notify: Notice;
  onChange: () => void;
  onOpenTargets?: () => void;
  onGranted?: () => void;
}

export interface BrowserTaskPanelHandle {
  dispose: () => void;
  // Returns true when a live grant existed and a release has started.
  release: () => boolean;
  granted: () => boolean;
}

interface PanelState {
  profiles: BrowserProfile[];
  host: BrowserHostView | null;
  grant: BrowserTaskGrant | null;
  tabs: BrowserTab[];
  bookmarks: BrowserBookmark[];
  profileID: string;
  selectedTabID: string;
  loading: boolean;
  binding: boolean;
  bookmarksLoading: boolean;
  bookmarksLoaded: boolean;
  bookmarksError: string;
  bookmarksMenu: "" | "all" | "folder";
  bookmarksMenuFolder: string;
  inUseProfileID: string;
  urlDraft: string;
  error: string;
  frameError: string;
  frameLive: boolean;
  startPageGrantID: string;
}

// 300ms keeps the preview at ~3.3 fps; an unchanged page backs off to ~0.8 fps
// instead of re-encoding identical JPEGs forever.
export const BROWSER_FRAME_MIN_DELAY_MS = 300;
export const BROWSER_FRAME_MAX_DELAY_MS = 1200;
const httpStatusForbidden = 403;
const httpStatusNotFound = 404;
const httpStatusConflict = 409;
const searchEngine = "https://www.bing.com/search";
// AHA serves this page itself (see internal/httpapi/web.go). The host only
// accepts http(s) URLs, so the new-tab page must never be about:blank or an
// edge:// surface even though Edge can display those.
export const BROWSER_START_PAGE_PATH = "/browser-newtab";

// Comparing a small sample is enough to tell unchanged previews apart without
// decoding the JPEG or keeping the full previous frame in memory.
export function browserFrameSignature(bytes: Uint8Array): string {
  if (!bytes.length) return "0";
  const middle = Math.floor(bytes.length / 2);
  return [
    bytes.length,
    bytes[0], bytes[1],
    bytes[middle - 1] ?? 0, bytes[middle] ?? 0,
    bytes[bytes.length - 2] ?? 0, bytes[bytes.length - 1] ?? 0,
  ].join(":");
}

export function nextBrowserFrameDelay(previous: string, current: string, delay: number): number {
  const bounded = Math.max(BROWSER_FRAME_MIN_DELAY_MS, Math.min(BROWSER_FRAME_MAX_DELAY_MS, delay));
  if (!previous) return BROWSER_FRAME_MIN_DELAY_MS;
  if (previous === current) return Math.min(BROWSER_FRAME_MAX_DELAY_MS, Math.round(bounded * 1.7));
  return BROWSER_FRAME_MIN_DELAY_MS;
}

export function browserStartPageURL(origin: string): string {
  try {
    return new URL(BROWSER_START_PAGE_PATH, origin || "http://127.0.0.1").toString();
  } catch {
    return "http://127.0.0.1/browser-newtab";
  }
}

// Edge's address bar doubles as a search box. A plain word becomes a search;
// a host-like token gets https://; only http(s) reaches the host API.
export function browserAddressTarget(raw: string): string {
  const value = String(raw || "").trim();
  if (!value) return "";
  try {
    const parsed = new URL(value);
    if (parsed.protocol === "http:" || parsed.protocol === "https:") return parsed.toString();
  } catch {
    // Not an absolute URL; fall through to host-like/search handling.
  }
  if (!/\s/.test(value) && /^[^\s/:]+\.[^\s/:]+/.test(value)) {
    try {
      const parsed = new URL(`https://${value}`);
      if (parsed.protocol === "https:" && parsed.hostname.includes(".")) return parsed.toString();
    } catch {
      // Fall through to search.
    }
  }
  return `${searchEngine}?q=${encodeURIComponent(value)}`;
}

function escapeHTML(value: unknown): string {
  return String(value ?? "")
    .replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;").replaceAll("'", "&#039;");
}

function stateLabel(state: string): string {
  return {
    idle: "未初始化", starting: "启动中", ready: "可用", auth_required: "等待登录",
    busy: "使用中", closed: "已关闭", error: "异常",
  }[state] || state;
}

function profileOptions(profiles: BrowserProfile[], selectedID: string): string {
  if (!profiles.length) return '<option value="">还没有 Profile，请先到资源页创建</option>';
  return profiles.map(profile => {
    const state = profile.session && profile.session.state !== "closed" ? profile.session.state : profile.state;
    return `<option value="${escapeHTML(profile.id)}" ${profile.id === selectedID ? "selected" : ""}>${escapeHTML(profile.name)} · ${escapeHTML(stateLabel(state))}</option>`;
  }).join("");
}

function statusText(state: PanelState, session: BrowserSession | null): string {
  if (state.error) return state.error;
  if (state.loading && !state.host) return "正在读取浏览器状态…";
  if (state.host && !state.host.available) {
    if (state.host.code === "browser_host_starting") return "浏览器宿主正在启动，请稍候…";
    return `浏览器宿主不可用：${state.host.error || state.host.code || "请查看 AHA2 启动日志"}`;
  }
  if (state.host && !state.host.edge_available) return "浏览器宿主在线，但未找到 Microsoft Edge";
  if (!state.profiles.length) return "还没有浏览器 Profile，请先到资源页创建";
  if (state.grant) {
    const reachable = session?.host_reachable === false ? " · 宿主不可达" : "";
    const error = session?.error ? `：${session.error}` : "";
    return `当前 Task 独占控制中 · 会话 ${stateLabel(session?.state || "ready")}${reachable}${error}`;
  }
  if (state.inUseProfileID && state.inUseProfileID === state.profileID) {
    return "该 Profile 正由其他 Task 独占控制；请等它释放，或由 Owner 在本面板转移控制权。";
  }
  return "选择 Profile 后点「获取独占控制」；获取成功后 Agent 可操作这个浏览器的全部页面。";
}

function statusTone(state: PanelState, session: BrowserSession | null): "ok" | "warning" | "error" | "muted" {
  if (state.error || (state.host && !state.host.available)) return "error";
  if (state.loading && !state.host) return "muted";
  if (state.host && !state.host.edge_available) return "warning";
  if (!state.profiles.length) return "warning";
  if (state.grant) return session?.error || session?.host_reachable === false ? "warning" : "ok";
  if (state.inUseProfileID && state.inUseProfileID === state.profileID) return "warning";
  return "muted";
}

// The tab strip shows the page title and host only. A full OAuth URL would be
// several wrapped lines wide; it stays in the button title for hover/inspection.
function tabHost(url: string): string {
  try {
    return new URL(url).host || url;
  } catch {
    return url.replace(/^[a-z]+:\/\//i, "").split("/")[0] || url;
  }
}

// Local, request-free favicon stand-in: loading the site's own favicon from the
// AHA origin would leak every visited host before the Owner acts on the page.
function faviconMark(url: string, className: string): string {
  const host = tabHost(url).replace(/^www\./i, "");
  const letter = (host.match(/[a-z0-9]/i)?.[0] || "?").toUpperCase();
  return `<span class="${className}" aria-hidden="true">${escapeHTML(letter)}</span>`;
}

function groupBookmarks(bookmarks: BrowserBookmark[]): Map<string, BrowserBookmark[]> {
  const groups = new Map<string, BrowserBookmark[]>();
  for (const bookmark of bookmarks) {
    const folder = bookmark.folder || "";
    const items = groups.get(folder) || [];
    items.push(bookmark);
    groups.set(folder, items);
  }
  return groups;
}

function bookmarkChip(bookmark: BrowserBookmark): string {
  const title = bookmark.title || tabHost(bookmark.url);
  return `<button type="button" class="desktop-browser-bookmark" data-browser-bookmark="${escapeHTML(bookmark.url)}" title="${escapeHTML(bookmark.url)}">
    ${faviconMark(bookmark.url, "desktop-browser-bookmark-favicon")}
    <span class="desktop-browser-bookmark-title">${escapeHTML(title)}</span>
  </button>`;
}

// The bar is a single horizontal row. Bookmarks with a folder collapse into a
// folder chip; unfiled bookmarks stay as chips. Neither renders a full URL.
function bookmarkBarHTML(bookmarks: BrowserBookmark[], loading: boolean, error: string): string {
  if (loading) return '<span class="desktop-browser-bookmark-empty" role="status">正在读取收藏夹…</span>';
  if (error) return `<span class="desktop-browser-bookmark-empty" role="status">收藏夹不可用：${escapeHTML(error)}</span>`;
  if (!bookmarks.length) return '<span class="desktop-browser-bookmark-empty">收藏夹为空</span>';
  const parts: string[] = [];
  for (const [folder, items] of groupBookmarks(bookmarks)) {
    if (!folder) {
      parts.push(...items.map(bookmarkChip));
      continue;
    }
    parts.push(`<button type="button" class="desktop-browser-bookmark desktop-browser-bookmark-folder" data-browser-bookmark-folder="${escapeHTML(folder)}" title="${escapeHTML(folder)}">
      ${icon("folder")}<span class="desktop-browser-bookmark-title">${escapeHTML(folder)}</span>
    </button>`);
  }
  return parts.join("");
}

function bookmarkMenuHTML(state: PanelState): string {
  if (!state.bookmarksMenu) return "";
  const all = state.bookmarksMenu === "all";
  const visible = all
    ? state.bookmarks
    : state.bookmarks.filter(bookmark => (bookmark.folder || "") === state.bookmarksMenuFolder);
  const heading = all ? "全部收藏" : state.bookmarksMenuFolder;
  const items = visible.length
    ? [...groupBookmarks(visible)].map(([folder, group]) => `${folder ? `<li class="desktop-browser-bookmark-menu-folder">${escapeHTML(folder)}</li>` : ""}
      ${group.map(bookmark => `<li><button type="button" data-browser-bookmark="${escapeHTML(bookmark.url)}" title="${escapeHTML(bookmark.url)}"><span>${escapeHTML(bookmark.title || tabHost(bookmark.url))}</span><small>${escapeHTML(tabHost(bookmark.url))}</small></button></li>`).join("")}`).join("")
    : '<li class="desktop-browser-bookmark-menu-empty">收藏夹为空</li>';
  return `<div id="desktop-browser-bookmarks-menu" class="desktop-browser-bookmark-menu" role="menu" aria-label="${escapeHTML(heading)}">
    <div class="desktop-browser-bookmark-menu-head"><strong>${escapeHTML(heading)}</strong><button type="button" id="desktop-browser-bookmarks-menu-close" class="icon-button" title="关闭收藏菜单" aria-label="关闭收藏菜单">${icon("close")}</button></div>
    <ul>${items}</ul>
  </div>`;
}

export function mountBrowserTaskPanel(options: BrowserTaskPanelOptions): BrowserTaskPanelHandle {
  const {container, selector = null, taskID, notify, onChange, onOpenTargets, onGranted} = options;
  const state: PanelState = {
    profiles: [], host: null, grant: null, tabs: [], bookmarks: [], profileID: "", selectedTabID: "",
    loading: true, binding: false, bookmarksLoading: false, bookmarksLoaded: false, bookmarksError: "",
    bookmarksMenu: "", bookmarksMenuFolder: "", inUseProfileID: "", urlDraft: "", error: "",
    frameError: "", frameLive: false, startPageGrantID: "",
  };

  let disposed = false;
  let frameTimer = 0;
  let frameAbort: AbortController | null = null;
  let frameURL = "";
  let frameSignature = "";
  let frameDelay = BROWSER_FRAME_MIN_DELAY_MS;
  let streamGeneration = 0;
  let bookmarkRequest = 0;
  let releaseTimer = 0;
  let releaseRequested = false;
  const painted = new WeakMap<HTMLElement, string>();

  const current = (): boolean => container.isConnected && Boolean(container.closest("#desktop-tool"));
  const selectedTab = (): BrowserTab | null =>
    state.tabs.find(tab => tab.id === state.selectedTabID) || null;
  const selectorTarget = (): HTMLElement | null => selector && selector.isConnected ? selector : null;

  const frameStatusText = (): string => {
    if (!state.grant) return "先选择 Profile 并获取独占控制，之后可查看并操作全部页面";
    if (state.binding) return "正在处理浏览器操作…";
    if (state.host && !state.host.available) return "浏览器宿主不可用，无法获取画面";
    if (state.frameError) return `画面暂不可用：${state.frameError}`;
    if (!state.tabs.length) return "当前浏览器还没有可用网页，正在准备开始页…";
    if (!selectedTab()) return "选择页面后显示画面";
    if (!state.frameLive) return "正在获取画面…";
    return "实时画面 · 只读（不会转发点击或输入）";
  };

  const paintFrameStatus = (): void => {
    const status = container.querySelector<HTMLElement>("#desktop-browser-frame-status");
    if (status) status.textContent = frameStatusText();
    const image = container.querySelector<HTMLImageElement>("#desktop-browser-frame");
    if (image && frameURL) {
      if (image.src !== frameURL) image.src = frameURL;
      image.hidden = false;
    }
  };

  const syncShell = (session: BrowserSession | null, profileName: string): void => {
    const status = statusText(state, session);
    const shell = selectorTarget() || container;
    shell.dataset.status = status;
    shell.dataset.profile = profileName;
    shell.dataset.granted = String(Boolean(state.grant));
    container.dataset.granted = String(Boolean(state.grant));
    const surfaceStatus = document.querySelector<HTMLElement>("#desktop-browser-surface-status");
    if (surfaceStatus) surfaceStatus.textContent = status;
    onChange();
  };

  const setNodeHTML = (node: HTMLElement, html: string): boolean => {
    if (painted.get(node) === html) return false;
    painted.set(node, html);
    node.innerHTML = html;
    return true;
  };

  const bindSelector = (): void => {
    const node = selectorTarget();
    if (!node) return;
    node.querySelector("#desktop-browser-profile")?.addEventListener("change", event => {
      state.profileID = (event.target as HTMLSelectElement).value;
      paint();
    });
    node.querySelector("#desktop-browser-refresh")?.addEventListener("click", () => {
      if (!state.loading) void reload();
    });
    node.querySelector("#desktop-browser-attach")?.addEventListener("click", () => {
      if (!state.loading) void attach();
    });
    node.querySelector("#desktop-browser-switch")?.addEventListener("click", () => {
      if (!state.loading) void switchTarget();
    });
    node.querySelector("#desktop-browser-transfer")?.addEventListener("click", () => {
      if (!state.binding) void transferControl();
    });
  };

  const bindControl = (): void => {
    container.querySelector("#desktop-browser-retry")?.addEventListener("click", () => {
      if (!state.loading) void reload();
    });
    container.querySelector("#desktop-browser-open-targets")?.addEventListener("click", () => {
      onOpenTargets?.();
    });
    container.querySelector("#desktop-browser-newtab")?.addEventListener("click", () => {
      if (!state.binding) void openStartPage();
    });
    container.querySelector("#desktop-browser-bookmarks-more")?.addEventListener("click", () => {
      if (!state.grant) return;
      if (state.bookmarksMenu) closeBookmarksMenu();
      else openBookmarksMenu("all", "");
    });
    container.querySelector("#desktop-browser-bookmarks-menu-close")?.addEventListener("click", () => {
      closeBookmarksMenu();
    });
    const urlInput = container.querySelector<HTMLInputElement>("#desktop-browser-url");
    urlInput?.addEventListener("input", () => {
      state.urlDraft = urlInput.value;
    });
    container.querySelector("#desktop-browser-open-form")?.addEventListener("submit", event => {
      event.preventDefault();
      if (!state.binding) void openPage();
    });
    container.querySelectorAll<HTMLElement>("[data-browser-tab]").forEach(button => {
      button.addEventListener("click", () => {
        const tabID = button.dataset.browserTab || "";
        if (tabID === state.selectedTabID) return;
        state.selectedTabID = tabID;
        state.frameError = "";
        state.frameLive = false;
        const tab = selectedTab();
        if (tab && document.activeElement?.id !== "desktop-browser-url") state.urlDraft = tab.url;
        paint();
        startStream();
        if (state.grant) {
          void api.activateTaskBrowserTab(state.grant.id, tabID).catch(error => {
            const details = errorDetails(error);
            state.error = details.message;
            paint();
          });
        }
      });
    });
    container.querySelectorAll<HTMLElement>("[data-browser-tab-close]").forEach(button => {
      button.addEventListener("click", event => {
        event.stopPropagation();
        const tabID = button.dataset.browserTabClose || "";
        if (!state.binding && tabID) void closeTab(tabID);
      });
    });
    container.querySelectorAll<HTMLElement>("[data-browser-bookmark]").forEach(button => {
      button.addEventListener("click", () => {
        const target = button.dataset.browserBookmark || "";
        state.bookmarksMenu = "";
        state.bookmarksMenuFolder = "";
        if (!state.binding && target) void openPage(target);
      });
    });
    container.querySelectorAll<HTMLElement>("[data-browser-bookmark-folder]").forEach(button => {
      button.addEventListener("click", () => {
        openBookmarksMenu("folder", button.dataset.browserBookmarkFolder || "");
      });
    });
  };

  const selectorHTML = (session: BrowserSession | null): string => {
    const busy = state.loading || state.binding;
    const noProfiles = !state.profiles.length;
    const hostDown = Boolean(state.host && !state.host.available);
    const canTransfer = !state.grant && Boolean(state.inUseProfileID) && state.inUseProfileID === state.profileID;
    let primary = "";
    if (state.grant) {
      primary = `<button type="button" id="desktop-browser-switch" class="primary desktop-browser-control" ${busy ? "disabled" : ""}>切换到所选目标</button>`;
    } else if (canTransfer) {
      primary = `<button type="button" id="desktop-browser-transfer" class="primary desktop-browser-control" ${busy ? "disabled" : ""}>转移控制权</button>`;
    } else {
      primary = `<button type="button" id="desktop-browser-attach" class="primary desktop-browser-control" ${busy || noProfiles || hostDown ? "disabled" : ""}>${icon("link")}<span>获取独占控制</span></button>`;
    }
    // This popover is only a target picker. No preview, tab strip, address bar
    // or bookmark bar may be rendered here.
    return `
      <div class="desktop-browser-toolbar" role="group" aria-label="内置浏览器控制">
        <label class="desktop-window-label desktop-browser-profile-label"><span>Profile</span><select id="desktop-browser-profile" aria-label="浏览器 Profile">${profileOptions(state.profiles, state.profileID)}</select></label>
        <button type="button" id="desktop-browser-refresh" class="icon-button" title="刷新浏览器状态" aria-label="刷新浏览器状态" ${busy ? "disabled" : ""}>${icon("refresh")}</button>
        ${primary}
      </div>
      <p id="desktop-browser-status" class="desktop-browser-status" data-tone="${statusTone(state, session)}" role="status">${escapeHTML(statusText(state, session))}</p>`;
  };

  const controlHTML = (): string => {
    if (!state.grant) {
      // Before the first status read lands there is no basis for "create a
      // Profile" or "host down"; avoid a misleading flash in the main panel.
      if (state.loading && !state.host) {
        return `<p class="desktop-empty" role="status" aria-busy="true">正在读取浏览器状态…</p>`;
      }
      const noProfiles = !state.profiles.length;
      const hostDown = Boolean(state.host && !state.host.available);
      const title = noProfiles ? "创建一个浏览器 Profile" : hostDown ? "重新连接浏览器宿主" : "还没有获取浏览器控制";
      const hint = noProfiles
        ? "创建后即可选择 Profile、获取独占控制并查看实时画面。"
        : hostDown ? "确认 AHA2 的浏览器宿主进程正在运行，然后重新检测。"
          : "在「共享目标」里选择 Profile 并获取独占控制，之后这里会显示标签页、地址栏、收藏夹和实时画面。";
      const action = noProfiles
        ? `<a class="desktop-browser-cta" href="#resources?tab=browser">前往资源页创建 Profile</a>`
        : hostDown
          ? `<button type="button" id="desktop-browser-retry" class="desktop-browser-cta">重新检测浏览器宿主</button>`
          : `<button type="button" id="desktop-browser-open-targets" class="desktop-browser-cta">选择共享目标</button>`;
      return `<div class="desktop-browser-empty-state" role="note">
        ${icon("browser")}
        <strong>${title}</strong>
        <p>${hint}</p>
        ${action}
      </div>`;
    }
    const tabs = state.tabs.length
      ? state.tabs.map(tab => {
        const selected = tab.id === state.selectedTabID;
        const host = tabHost(tab.url);
        // Only the page currently shown in the preview is "当前". The host's
        // `active` flag means "visible in its window", and a Profile with two
        // browser windows can legitimately report several at once, so it must
        // never produce several "当前" labels or highlighted tabs.
        return `<li class="desktop-browser-tab-item ${selected ? "active" : ""}" data-browser-tab-item="${escapeHTML(tab.id)}">
          <button type="button" class="desktop-browser-tab ${selected ? "active" : ""}" data-browser-tab="${escapeHTML(tab.id)}" aria-pressed="${selected}" ${selected ? 'aria-current="page"' : ""} title="${escapeHTML(tab.url)}">
            ${faviconMark(tab.url, "desktop-browser-tab-favicon")}
            <span class="desktop-browser-tab-title">${escapeHTML(tab.title || host || "未命名页面")}</span>
            <span class="desktop-browser-tab-origin" title="页面来源">${tab.origin === "owner" ? "Owner" : "Agent"}</span>
          </button>
          <button type="button" class="icon-button desktop-browser-tab-close" data-browser-tab-close="${escapeHTML(tab.id)}" title="关闭页面" aria-label="关闭页面 ${escapeHTML(tab.title || host)}">${icon("close")}</button>
        </li>`;
      }).join("")
      : `<li class="desktop-empty desktop-browser-tabs-empty">正在打开开始页…</li>`;
    const busy = state.binding;
    const menuOpen = Boolean(state.bookmarksMenu);
    return `<div class="desktop-browser-shell">
      <div class="desktop-browser-tabstrip" role="group" aria-label="浏览器标签页">
        <ul id="desktop-browser-tabs" class="desktop-browser-tabs" aria-label="浏览器页面">${tabs}</ul>
        <button type="button" id="desktop-browser-newtab" class="desktop-browser-newtab" title="新建标签页（AHA 开始页）" aria-label="新建标签页" ${busy ? "disabled" : ""}>${icon("plus")}</button>
      </div>
      <form id="desktop-browser-open-form" class="desktop-browser-open">
        <input id="desktop-browser-url" type="text" inputmode="url" placeholder="输入网址或搜索" aria-label="地址与搜索" value="${escapeHTML(state.urlDraft)}" ${busy ? "disabled" : ""} autocomplete="off" spellcheck="false">
        <button type="submit" ${busy ? "disabled" : ""}>打开</button>
      </form>
      <div class="desktop-browser-bookmarkbar" id="desktop-browser-bookmarkbar">
        <div class="desktop-browser-bookmark-items" id="desktop-browser-bookmarks">${bookmarkBarHTML(state.bookmarks, state.bookmarksLoading, state.bookmarksError)}</div>
        <button type="button" id="desktop-browser-bookmarks-more" class="icon-button desktop-browser-bookmarks-more" title="更多收藏" aria-label="更多收藏" aria-expanded="${menuOpen}" hidden>${icon("chevron-down")}</button>
        ${bookmarkMenuHTML(state)}
      </div>
      <figure class="desktop-browser-preview">
        <img id="desktop-browser-frame" alt="内置浏览器只读画面" draggable="false" hidden>
        <p id="desktop-browser-frame-status" class="desktop-empty" role="status">${escapeHTML(frameStatusText())}</p>
      </figure>
    </div>`;
  };

  const measureBookmarkOverflow = (): void => {
    const items = container.querySelector<HTMLElement>("#desktop-browser-bookmarks");
    const more = container.querySelector<HTMLElement>("#desktop-browser-bookmarks-more");
    if (!items || !more) return;
    more.hidden = items.scrollWidth <= items.clientWidth + 1;
  };

  const paint = (): void => {
    if (!current()) return;
    const session = state.grant?.session || state.profiles.find(item => item.id === state.profileID)?.session || null;
    const selectedProfile = state.profiles.find(item => item.id === state.profileID);
    const selectorNode = selectorTarget();
    if (selectorNode && setNodeHTML(selectorNode, selectorHTML(session))) bindSelector();
    if (setNodeHTML(container, controlHTML())) {
      bindControl();
      requestAnimationFrame(measureBookmarkOverflow);
    } else {
      measureBookmarkOverflow();
    }
    paintFrameStatus();
    syncShell(session, selectedProfile?.name || "");
  };

  const stopStream = (): void => {
    streamGeneration++;
    window.clearTimeout(frameTimer);
    frameTimer = 0;
    frameAbort?.abort();
    frameAbort = null;
    if (frameURL) URL.revokeObjectURL(frameURL);
    frameURL = "";
    const image = container.querySelector<HTMLImageElement>("#desktop-browser-frame");
    if (image) {
      image.removeAttribute("src");
      image.hidden = true;
    }
    frameSignature = "";
    frameDelay = BROWSER_FRAME_MIN_DELAY_MS;
    state.frameLive = false;
  };

  const schedule = (delay: number): void => {
    window.clearTimeout(frameTimer);
    frameTimer = window.setTimeout(() => void tick(), delay);
  };

  const tick = async (): Promise<void> => {
    if (disposed || !current() || document.visibilityState === "hidden") return;
    const grant = state.grant;
    const tab = selectedTab();
    if (!grant || !tab || (state.host && !state.host.available)) {
      paintFrameStatus();
      return;
    }
    const controller = new AbortController();
    frameAbort = controller;
    const generation = streamGeneration;
    try {
      const blob = await api.browserTabFrame(grant.id, tab.id, controller.signal);
      // The owner may have switched tabs or closed the panel while this frame
      // was in flight; a stale tab screenshot must never reach the new view.
      if (disposed || generation !== streamGeneration) return;
      const buffer = new Uint8Array(await blob.arrayBuffer());
      if (disposed || generation !== streamGeneration) return;
      const nextSignature = browserFrameSignature(buffer);
      frameDelay = nextBrowserFrameDelay(frameSignature, nextSignature, frameDelay);
      frameSignature = nextSignature;
      state.frameLive = true;
      state.frameError = "";
      const nextURL = URL.createObjectURL(new Blob([buffer], {type: blob.type || "image/jpeg"}));
      const previousURL = frameURL;
      frameURL = nextURL;
      const image = container.querySelector<HTMLImageElement>("#desktop-browser-frame");
      if (image) {
        image.src = nextURL;
        image.hidden = false;
      }
      if (previousURL) URL.revokeObjectURL(previousURL);
      paintFrameStatus();
    } catch (error) {
      if (disposed || (error instanceof DOMException && error.name === "AbortError")) return;
      // A stale tab's 403/404/409 must not hide or stop the new tab's stream.
      if (generation !== streamGeneration) return;
      state.frameLive = false;
      state.frameError = error instanceof Error ? error.message : String(error);
      paintFrameStatus();
      const status = (error as {status?: number}).status;
      // Revoked/closed/wrong-session answers are terminal for the preview; do
      // not keep hammering the API until the owner refreshes.
      if (status === httpStatusForbidden || status === httpStatusNotFound || status === httpStatusConflict) {
        stopStream();
        return;
      }
      frameDelay = 2000;
    } finally {
      if (frameAbort === controller) frameAbort = null;
      // A tab switch, panel close or visibility pause bumps the generation;
      // the stale loop must not schedule another frame.
      if (!disposed && generation === streamGeneration) schedule(frameDelay);
    }
  };

  const startStream = (): void => {
    stopStream();
    if (disposed || !current() || !state.grant || !selectedTab() || (state.host && !state.host.available)) {
      paintFrameStatus();
      return;
    }
    state.frameError = "";
    paintFrameStatus();
    schedule(0);
  };

  const loadBookmarks = async (grant: BrowserTaskGrant): Promise<void> => {
    const request = ++bookmarkRequest;
    state.bookmarksLoading = true;
    state.bookmarksError = "";
    paint();
    try {
      const result = await api.taskBrowserBookmarks(grant.id);
      if (request !== bookmarkRequest || state.grant?.id !== grant.id) return;
      state.bookmarks = result.bookmarks || [];
    } catch (error) {
      if (request !== bookmarkRequest || state.grant?.id !== grant.id) return;
      state.bookmarks = [];
      state.bookmarksError = errorDetails(error).message;
    } finally {
      if (request === bookmarkRequest) {
        state.bookmarksLoading = false;
        state.bookmarksLoaded = true;
        paint();
      }
    }
  };

  const load = async (): Promise<void> => {
    if (disposed) return;
    state.loading = true;
    state.error = "";
    paint();
    try {
      const result = await api.taskBrowser(taskID);
      // The Owner may have left browser mode while this read was in flight;
      // a disposed panel must not open a start page or repaint stale state.
      if (disposed) return;
      const data = result?.browser;
      if (!data) throw new Error("浏览器状态不可用");
      state.profiles = data.profiles || [];
      state.host = data.host || null;
      const active = (data.grants || []).find(grant => grant.status === "active") || null;
      const previousGrant = state.grant?.id || "";
      state.grant = active;
      // The server syncs every visible http(s) page under the exclusive lease,
      // including pages the Owner opened by hand.
      state.tabs = active ? (data.tabs || []) : [];
      if (active) state.inUseProfileID = "";
      if (active && active.id !== previousGrant) {
        state.bookmarks = [];
        state.bookmarksLoaded = false;
        state.bookmarksError = "";
        state.bookmarksMenu = "";
        state.urlDraft = "";
        state.startPageGrantID = "";
      }
      if (!active) {
        state.bookmarksLoaded = false;
        state.bookmarksMenu = "";
      }
      if (!state.profiles.some(item => item.id === state.profileID)) {
        state.profileID = active?.profile_id || state.profiles[0]?.id || "";
      }
      if (!active) {
        const selected = state.profiles.find(item => item.id === state.profileID);
        if (selected?.lease_task_id) state.inUseProfileID = selected.id;
      }
      if (!state.tabs.some(tab => tab.id === state.selectedTabID)) {
        state.selectedTabID = (state.tabs.find(tab => tab.active) || state.tabs[0])?.id || "";
      }
      const selected = selectedTab();
      if (selected && document.activeElement?.id !== "desktop-browser-url") state.urlDraft = selected.url;
    } catch (error) {
      state.error = error instanceof Error ? error.message : String(error);
    } finally {
      state.loading = false;
    }
    paint();
    startStream();
    if (state.grant && !state.bookmarksLoaded && !state.bookmarksLoading) void loadBookmarks(state.grant);
    ensureStartPage();
  };

  const reload = async (): Promise<void> => {
    await load();
  };

  const errorDetails = (error: unknown): {message: string; code: string} => ({
    message: error instanceof Error ? error.message : String(error),
    code: String((error as {code?: unknown})?.code || ""),
  });

  // Selecting a freshly opened page must also refresh the address bar. load()
  // still points urlDraft at the previously selected tab, and changing only
  // the id would leave the old URL visible.
  const selectTab = (tabID: string, url = ""): void => {
    if (!tabID) return;
    state.selectedTabID = tabID;
    if (url) state.urlDraft = url;
    state.frameError = "";
    state.frameLive = false;
  };

  // The exclusive holder may close any visible page, including one the Owner
  // opened by hand. origin is only a display label.
  const closeTab = async (tabID: string): Promise<void> => {
    if (!state.grant) return;
    state.binding = true;
    state.error = "";
    paint();
    try {
      await api.closeTaskBrowserTab(state.grant.id, tabID);
      await load();
      notify("notice", "页面已关闭");
    } catch (error) {
      const details = errorDetails(error);
      state.error = details.message;
      notify("error", details.message);
    } finally {
      state.binding = false;
    }
    paint();
    startStream();
  };

  const openPage = async (targetOverride?: string, quiet = false): Promise<void> => {
    if (!state.grant) return;
    const target = browserAddressTarget(targetOverride ?? state.urlDraft);
    if (!target) {
      state.error = "请填写要打开的网址或搜索词";
      paint();
      return;
    }
    state.binding = true;
    state.error = "";
    paint();
    try {
      const result = await api.openTaskBrowserTab(state.grant.id, target);
      if (targetOverride === undefined) state.urlDraft = "";
      await load();
      selectTab(result.tab.id, result.tab.url || target);
      paint();
      startStream();
      if (!quiet) notify("notice", "页面已在内置浏览器中打开");
    } catch (error) {
      const details = errorDetails(error);
      state.error = details.message;
      notify("error", details.message);
    } finally {
      state.binding = false;
    }
    paint();
    startStream();
  };

  // The owner asked for a real http(s) start page instead of about:blank or an
  // Edge internal surface. Open it once per grant when the profile has no
  // enumerable page yet; never close pages the Owner opened by hand.
  const ensureStartPage = (): void => {
    const grant = state.grant;
    if (!grant || state.tabs.length || state.startPageGrantID === grant.id) return;
    state.startPageGrantID = grant.id;
    void openPage(browserStartPageURL(window.location.origin), true);
  };

  const openStartPage = async (): Promise<void> => {
    if (!state.grant) return;
    await openPage(browserStartPageURL(window.location.origin), true);
  };

  const releaseControl = async (): Promise<void> => {
    if (!state.grant) return;
    const grantID = state.grant.id;
    state.binding = true;
    state.error = "";
    paint();
    try {
      await api.releaseTaskBrowserGrant(grantID);
      if (state.grant?.id === grantID) state.grant = null;
      state.tabs = [];
      state.bookmarks = [];
      state.bookmarksLoaded = false;
      state.bookmarksMenu = "";
      state.startPageGrantID = "";
      await load();
      notify("notice", "已释放独占控制；浏览器页面保持打开，其他 Task 可以接手");
    } catch (error) {
      const details = errorDetails(error);
      state.error = details.message;
      notify("error", details.message);
    } finally {
      state.binding = false;
    }
    paint();
    startStream();
  };

  const transferControl = async (): Promise<void> => {
    if (!state.inUseProfileID) return;
    state.binding = true;
    state.error = "";
    paint();
    try {
      await api.transferBrowserProfile(state.inUseProfileID, taskID);
      state.inUseProfileID = "";
      await load();
      notify("notice", "Owner 已把该 Profile 的独占控制转移给当前 Task");
      onGranted?.();
    } catch (error) {
      const details = errorDetails(error);
      state.error = details.message;
      notify("error", details.message);
    } finally {
      state.binding = false;
    }
    paint();
    startStream();
  };

  // Same profile: just go back to the main controls. A different profile has to
  // release the old lease first because the API allows one browser profile per
  // Task; this is a UI sequence over the existing grant endpoints.
  const switchTarget = async (): Promise<void> => {
    if (!state.grant) return;
    if (state.profileID === state.grant.profile_id) {
      onGranted?.();
      return;
    }
    if (!state.profileID) {
      state.error = "请先选择浏览器 Profile";
      paint();
      return;
    }
    const previousGrant = state.grant;
    state.binding = true;
    state.error = "";
    paint();
    try {
      await api.releaseTaskBrowserGrant(previousGrant.id);
      if (state.grant?.id === previousGrant.id) state.grant = null;
      state.tabs = [];
      state.bookmarks = [];
      state.bookmarksLoaded = false;
      state.bookmarksMenu = "";
      state.startPageGrantID = "";
      await attach();
    } catch (error) {
      const details = errorDetails(error);
      state.error = details.message;
      notify("error", details.message);
    } finally {
      state.binding = false;
    }
    paint();
    startStream();
  };

  const openBookmarksMenu = (kind: "all" | "folder", folder: string): void => {
    state.bookmarksMenu = kind;
    state.bookmarksMenuFolder = folder;
    paint();
    container.querySelector<HTMLElement>("#desktop-browser-bookmarks-menu button[data-browser-bookmark]")?.focus({preventScroll: true});
  };

  const closeBookmarksMenu = (): void => {
    if (!state.bookmarksMenu) return;
    state.bookmarksMenu = "";
    state.bookmarksMenuFolder = "";
    paint();
    container.querySelector<HTMLElement>("[data-browser-bookmark-folder], #desktop-browser-bookmarks-more")?.focus({preventScroll: true});
  };

  const attach = async (): Promise<void> => {
    if (!state.profileID) {
      state.error = "请先选择浏览器 Profile";
      paint();
      return;
    }
    state.loading = true;
    state.error = "";
    paint();
    try {
      const result = await api.createTaskBrowserGrant(taskID, {
        profile_id: state.profileID,
        // New grants carry the Owner-approved full interaction scope. Existing
        // grants keep their stored capability array until the Owner re-grants.
        capabilities: ["open_tab", "navigate", "snapshot", "interact", "files", "cookies"],
        expires_in_seconds: 1800,
      });
      state.inUseProfileID = "";
      state.grant = result.grant;
      state.profileID = result.grant.profile_id;
      state.startPageGrantID = "";
      await load();
      notify("notice", result.session
        ? `已获取独占控制（会话 ${stateLabel(result.session.state)}）`
        : "已获取该 Profile 的独占控制");
      onGranted?.();
    } catch (error) {
      const details = errorDetails(error);
      state.error = details.message;
      if (details.code === "browser_profile_in_use") {
        state.inUseProfileID = state.profileID;
        notify("notice", "该 Profile 正由其他 Task 独占控制；等它释放，或由 Owner 在这里转移控制权。");
      } else if (details.code === "browser_task_profile_limit") {
        notify("notice", "当前 Task 已独占另一个 Profile；请先释放它，再获取新的 Profile。");
      } else {
        notify("error", details.message);
      }
    } finally {
      state.loading = false;
    }
    paint();
    startStream();
  };

  const onVisibility = (): void => {
    if (document.visibilityState === "hidden") {
      window.clearTimeout(frameTimer);
      frameTimer = 0;
      frameAbort?.abort();
    } else {
      startStream();
    }
  };

  // The bookmark menu is non-modal, so Escape is scoped to the panel and
  // returns focus to the overflow toggle.
  const onKeydown = (event: KeyboardEvent): void => {
    if (event.key !== "Escape" || !state.bookmarksMenu) return;
    event.stopPropagation();
    closeBookmarksMenu();
  };

  const onOutsidePointer = (event: Event): void => {
    if (!state.bookmarksMenu || !(event.target instanceof Element)) return;
    if (event.target.closest("#desktop-browser-bookmarkbar")) return;
    // Close without repainting: this runs on pointerdown, and rebuilding the
    // main surface here would detach the element that is about to receive the
    // click (for example the new-tab button).
    state.bookmarksMenu = "";
    state.bookmarksMenuFolder = "";
    container.querySelector("#desktop-browser-bookmarks-menu")?.remove();
    container.querySelector("#desktop-browser-bookmarks-more")?.setAttribute("aria-expanded", "false");
    // The menu was removed outside paint(); drop the cached HTML so reopening
    // the same folder menu is not mistaken for an identical (already painted)
    // tree and skipped.
    painted.delete(container);
  };

  paint();
  void load();
  document.addEventListener("visibilitychange", onVisibility);
  document.addEventListener("pointerdown", onOutsidePointer, true);
  container.addEventListener("keydown", onKeydown);
  const observer = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(() => measureBookmarkOverflow());
  observer?.observe(container);
  window.addEventListener("resize", measureBookmarkOverflow);

  return {
    dispose: () => {
      disposed = true;
      bookmarkRequest++;
      // An accepted stop outlives the panel: dispose must not cancel the
      // pending release timer, or switching control mode mid-open would keep
      // the exclusive lease alive with no UI to release it.
      document.removeEventListener("visibilitychange", onVisibility);
      document.removeEventListener("pointerdown", onOutsidePointer, true);
      container.removeEventListener("keydown", onKeydown);
      window.removeEventListener("resize", measureBookmarkOverflow);
      observer?.disconnect();
      stopStream();
    },
    release: () => {
      if (!state.grant) return false;
      releaseRequested = true;
      // The header stop stays available while a tab open/close is in flight;
      // release once the current operation settles instead of dropping it.
      // A wedged request must not delay the lease forever, so DELETE proceeds
      // after a bounded wait as well.
      const deadline = Date.now() + 15_000;
      const waitRelease = () => {
        if (!releaseRequested) return;
        if (state.binding && Date.now() < deadline) {
          window.clearTimeout(releaseTimer);
          releaseTimer = window.setTimeout(waitRelease, 120);
          return;
        }
        releaseRequested = false;
        if (state.grant) void releaseControl();
      };
      waitRelease();
      return true;
    },
    granted: () => Boolean(state.grant),
  };
}
