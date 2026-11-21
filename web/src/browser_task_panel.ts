// Lazy chunk for the Task shared-control "内置浏览器" mode. The targets
// popover only renders the compact profile/attach form (`selector`); the tab
// strip, address bar, bookmark bar and read-only preview live in the main
// panel (`container`), so picking a target never grows the popover into a
// second browser window.
import {api} from "./api.js";
import {icon} from "./icons.js";
import type {BrowserBookmark, BrowserHostView, BrowserPageAction, BrowserProfile, BrowserSession, BrowserTab, BrowserTaskGrant, TaskBrowserState} from "./types.js";

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
  // Folder labels whose bar chips are currently hidden by the overflow
  // measure. The "更多" menu lists these folders instead of every folder.
  bookmarkBarHiddenFolders: string[];
  inUseProfileID: string;
  urlDraft: string;
  error: string;
  frameError: string;
  frameLive: boolean;
  startPageGrantID: string;
  // Owner page control. Read-only is the default: input is only forwarded after
  // the explicit toggle, and the mode is dropped on lease/tab/host changes.
  controlMode: boolean;
  controlNotice: string;
  // CSS viewport captured with the currently displayed frame; 0 means unknown
  // and the mapper falls back to the image's own pixel size.
  frameViewportWidth: number;
  frameViewportHeight: number;
}

// Where Escape/close should put focus back. The menu and bar are rebuilt on
// every paint, so remember the trigger by role/label instead of by element.
type BookmarkMenuReturnFocus = {kind: "more"} | {kind: "folder"; folder: string};

// 300ms keeps the preview at ~3.3 fps; an unchanged page backs off to ~0.8 fps
// instead of re-encoding identical JPEGs forever.
export const BROWSER_FRAME_MIN_DELAY_MS = 300;
export const BROWSER_FRAME_MAX_DELAY_MS = 1200;
// The tab strip follows the real browser: new, closed and switched pages are
// re-read on a timer while the panel is open, independent of the frame stream.
// 3s is the middle of the 2-5s range the owner asked for.
export const BROWSER_STATE_SYNC_MS = 3000;
// A folder menu closes by itself once the pointer leaves the chip and the
// popover. The timer is short: it only bridges the few pixels between the two
// regions while the pointer crosses from one into the other.
export const BOOKMARK_MENU_HOVER_CLOSE_MS = 180;
// The chip and popover are separated by the bar's padding; pad the hover zone
// so crossing that gap never counts as leaving.
const BOOKMARK_MENU_HOVER_PAD = 8;
const httpStatusForbidden = 403;
const httpStatusNotFound = 404;
const httpStatusConflict = 409;
const httpStatusGone = 410;
const httpStatusServiceUnavailable = 503;
const browserHostUnavailableCode = "browser_host_unavailable";
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

// --- Owner control: viewport mapping, keyboard and text shaping ------------
//
// The preview is an <img> with object-fit: contain, so the page image is
// centered inside the stage and may be letterboxed. A click is mapped through
// the image's own natural pixel size and the CSS viewport captured with that
// frame: both are ratios, so a device scale factor never has to be known.

export const BROWSER_CONTROL_CLICK_DELAY_MS = 300;
export const BROWSER_CONTROL_MAX_TEXT_BYTES = 4096;
export const BROWSER_CONTROL_MAX_PASTE_BYTES = 16 * 1024;
export const BROWSER_CONTROL_MAX_QUEUE = 64;
export const BROWSER_CONTROL_MAX_SCROLL = 4000;

export interface BrowserDisplayRect {
  left: number;
  top: number;
  width: number;
  height: number;
}

export interface BrowserPoint {
  x: number;
  y: number;
}

const browserUTF8 = new TextEncoder();

export function browserTextByteLength(text: string): number {
  return browserUTF8.encode(String(text || "")).length;
}

// browserViewportPoint returns the page coordinate under a client point, or
// null when the point cannot be mapped safely: outside the displayed image
// (the letterbox bars), or the frame has no valid CSS viewport. A missing
// viewport is fail-closed: image pixels are not a substitute because a device
// scale factor would silently shift the click inside the viewport.
export const BROWSER_VIEWPORT_WAITING_NOTICE = "画面尺寸未知，坐标输入已暂停；请等待下一帧画面";

export function browserViewportPoint(
  clientX: number,
  clientY: number,
  rect: BrowserDisplayRect,
  imageWidth: number,
  imageHeight: number,
  viewportWidth: number,
  viewportHeight: number,
): BrowserPoint | null {
  if (!rect || !(rect.width > 0) || !(rect.height > 0) || !(imageWidth > 0) || !(imageHeight > 0)) return null;
  if (!(viewportWidth > 0) || !(viewportHeight > 0)) return null;
  const scale = Math.min(rect.width / imageWidth, rect.height / imageHeight);
  if (!Number.isFinite(scale) || scale <= 0) return null;
  const contentWidth = imageWidth * scale;
  const contentHeight = imageHeight * scale;
  const left = rect.left + (rect.width - contentWidth) / 2;
  const top = rect.top + (rect.height - contentHeight) / 2;
  if (clientX < left || clientX > left + contentWidth || clientY < top || clientY > top + contentHeight) return null;
  const x = Math.floor(((clientX - left) / contentWidth) * viewportWidth);
  const y = Math.floor(((clientY - top) / contentHeight) * viewportHeight);
  return {
    x: Math.max(0, Math.min(viewportWidth - 1, x)),
    y: Math.max(0, Math.min(viewportHeight - 1, y)),
  };
}

export type BrowserKeyCommand = {kind: "key"; keys: string[]} | {kind: "text"; text: string};

export interface BrowserKeyEventLike {
  key?: string;
  ctrlKey?: boolean;
  altKey?: boolean;
  shiftKey?: boolean;
  metaKey?: boolean;
  isComposing?: boolean;
}

// browserKeyCommand turns a Web keyboard event into the host's closed key/text
// vocabulary. Printable input becomes text (so IME and layout-produced
// characters survive); navigation keys and real combinations become keys.
// Meta/Win is deliberately never forwarded: on the host it is an OS-level
// modifier (Win+L locks the machine), not a page shortcut.
export function browserKeyCommand(event: BrowserKeyEventLike | null | undefined): BrowserKeyCommand | null {
  if (!event || event.isComposing) return null;
  const key = String(event.key || "");
  if (!key) return null;
  const ignored = new Set([
    "Control", "Shift", "Alt", "Meta", "AltGraph", "CapsLock", "NumLock", "ScrollLock",
    "OS", "Dead", "Unidentified", "Process", "Compose", "Super",
  ]);
  if (ignored.has(key)) return null;
  const ctrl = Boolean(event.ctrlKey);
  const alt = Boolean(event.altKey);
  const shift = Boolean(event.shiftKey);
  const meta = Boolean(event.metaKey);
  if (meta) return null;
  const names: Record<string, string> = {
    Enter: "Enter", Escape: "Escape", Tab: "Tab", Backspace: "Backspace", Delete: "Delete",
    Insert: "Insert", ArrowLeft: "ArrowLeft", ArrowRight: "ArrowRight",
    ArrowUp: "ArrowUp", ArrowDown: "ArrowDown", Home: "Home", End: "End",
    PageUp: "PageUp", PageDown: "PageDown", " ": "Space", ContextMenu: "ContextMenu",
  };
  const functionKey = /^F([1-9]|1[0-2])$/.test(key) ? key : "";
  const singleCharacter = [...key].length === 1;
  const printable = singleCharacter && key !== " " && /^[!-~]$/.test(key);
  if (ctrl || alt) {
    if (ctrl && alt && key === "Delete") return null; // OS secure attention
    let base = names[key] || functionKey;
    if (!base && printable) base = key;
    if (!base && singleCharacter && /^[a-z0-9]$/i.test(key)) base = key;
    if (!base) return null;
    // A letter combination is case-insensitive; Shift is its own modifier.
    if (/^[A-Z]$/.test(base)) base = base.toLowerCase();
    const keys = [
      ...(ctrl ? ["Control"] : []), ...(alt ? ["Alt"] : []),
      ...(shift ? ["Shift"] : []), base,
    ];
    if (keys.length > 4) return null;
    return {kind: "key", keys};
  }
  if (key === " " || printable || (singleCharacter && key !== "\u007f" && !names[key])) {
    // Shift already shaped event.key into the final character (e.g. "A", "?").
    return {kind: "text", text: key};
  }
  const special = names[key] || functionKey;
  if (!special) return null;
  return {kind: "key", keys: shift ? ["Shift", special] : [special]};
}

// splitBrowserText cuts text on UTF-8 byte boundaries, never splitting a
// code point, so one action stays inside the host's 4096-byte text budget.
export function splitBrowserText(text: string, maxBytes = BROWSER_CONTROL_MAX_TEXT_BYTES): string[] {
  const chunks: string[] = [];
  let current = "";
  let size = 0;
  for (const character of String(text || "")) {
    const bytes = browserUTF8.encode(character).length;
    if (current && size + bytes > maxBytes) {
      chunks.push(current);
      current = "";
      size = 0;
    }
    current += character;
    size += bytes;
  }
  if (current) chunks.push(current);
  return chunks;
}

// browserTextActions turns free text into ordered host actions. A newline is a
// real Enter press; everything else is chunked text typed at the page's focus.
export function browserTextActions(text: string, maxBytes = BROWSER_CONTROL_MAX_TEXT_BYTES): BrowserPageAction[] {
  const value = String(text || "").replace(/\r\n?/g, "\n");
  if (!value) return [];
  const actions: BrowserPageAction[] = [];
  value.split("\n").forEach((line, index) => {
    if (index > 0) actions.push({kind: "key", keys: ["Enter"]});
    for (const chunk of splitBrowserText(line, maxBytes)) actions.push({kind: "text", text: chunk});
  });
  return actions;
}

export function browserScrollStep(value: number, max = BROWSER_CONTROL_MAX_SCROLL): number {
  if (!Number.isFinite(value)) return 0;
  return Math.max(-max, Math.min(max, Math.trunc(value)));
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
    return `浏览器共享已开启 · 会话 ${stateLabel(session?.state || "ready")}${reachable}${error}`;
  }
  if (state.inUseProfileID && state.inUseProfileID === state.profileID) {
    return "该浏览器正由其他 Task 使用；请在对应 Task 关闭共享后再开启。";
  }
  return "选择 Profile 后打开共享开关；开启后当前 Task 可操作这个浏览器的全部页面。";
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

// Edge stores the bookmark-bar root as part of every folder path. The bar
// itself already means that root, so drop it, tolerate the localized/English
// spellings (case-insensitive, optional spaces and full-width slashes), and
// group by the first remaining segment. That keeps "Docs", "Docs/HUALAI" and
// "Docs/蓝牙" behind one "Docs" chip instead of three parallel chips. A bare
// root path ("收藏夹栏") means the bookmark sits directly on the bar, so it
// belongs to the unfiled group instead of becoming its own folder chip.
const bookmarkRootPattern = /^(?:收藏夹栏|收藏夹|favorites?|favourites?)\s*[/／]\s*/i;
const bookmarkRootOnlyPattern = /^(?:收藏夹栏|收藏夹|favorites?|favourites?)\s*[/／]?\s*$/i;

function bookmarkFolderSegments(folder: string): string[] {
  const value = String(folder || "").trim();
  if (bookmarkRootOnlyPattern.test(value)) return [];
  return value.replace(bookmarkRootPattern, "").split(/[/／]/).map(segment => segment.trim()).filter(Boolean);
}

// The first segment below the Edge bookmark-bar root, or "" for an unfiled
// bookmark. This is the only label a bar chip may show.
export function browserBookmarkFolderLabel(folder: string): string {
  return bookmarkFolderSegments(folder)[0] || "";
}

// Everything below the first-level folder, for grouping a folder menu.
function bookmarkSubfolderLabel(folder: string): string {
  return bookmarkFolderSegments(folder).slice(1).join("/");
}

// "收藏栏级别" means the bookmark sits directly on the bar: its folder is
// empty, or only the bar root. A bookmark inside a first-level folder (or any
// deeper subfolder) belongs to that folder's menu and must never leak into the
// overflow menu's URL list.
export function isBookmarkBarLevel(bookmark: BrowserBookmark): boolean {
  return bookmarkFolderSegments(bookmark.folder || "").length === 0;
}

// Group the chevron menu's contents: only the first-level folders whose chips
// are currently hidden by overflow, plus the bookmark-bar's own URLs. An empty
// hidden list means the bar has no hidden folder — deliberately no "show every
// folder" fallback, because that turned the overflow entry into a second copy
// of the whole tree.
export function bookmarkOverflowEntries(
  bookmarks: BrowserBookmark[],
  hiddenFolders: string[],
): {folders: Array<[string, BrowserBookmark[]]>; urls: BrowserBookmark[]} {
  const folders = [...groupBookmarks(bookmarks)]
    .filter(([folder]) => Boolean(folder) && hiddenFolders.includes(folder));
  return {folders, urls: bookmarks.filter(isBookmarkBarLevel)};
}

// Pick the tab the preview should show. `active` only means "visible in its
// own window", so a Profile with several Edge windows has several active pages
// and cannot say which window the Owner is looking at. `focused` is the
// foreground-window signal. The order is strict: exactly one focused page wins,
// otherwise exactly one active page wins, otherwise keep the current selection.
// Several focused pages never outrank a single active page, because the extra
// focus answers are stale or partial, while the single active page is the only
// unambiguous window. A missing selection falls back to the first page.
export function browserFollowTabID(tabs: BrowserTab[], selectedID: string, followActive = true): string {
  if (!tabs.length) return "";
  const focused = tabs.filter(tab => tab.focused);
  const active = tabs.filter(tab => tab.active);
  const target = focused.length === 1
    ? focused[0]
    : active.length === 1 ? active[0] : null;
  if (!tabs.some(tab => tab.id === selectedID)) {
    return (target || tabs[0]).id;
  }
  if (followActive && target && target.id !== selectedID) return target.id;
  return selectedID;
}

// The preview stream is keyed by lease + selected tab, so a focus change that
// moves the selection also starts a new stream instead of reusing the old
// page's frames.
export function browserPreviewStreamKey(grantID: string, tabID: string): string {
  return grantID && tabID ? `${grantID}:${tabID}` : "";
}

export function groupBookmarks(bookmarks: BrowserBookmark[]): Map<string, BrowserBookmark[]> {
  const groups = new Map<string, BrowserBookmark[]>();
  for (const bookmark of bookmarks) {
    // Keying by the first-level label folds every subfolder into its parent
    // while keeping first-appearance order and every original bookmark.
    const folder = browserBookmarkFolderLabel(bookmark.folder || "");
    const items = groups.get(folder) || [];
    items.push(bookmark);
    groups.set(folder, items);
  }
  return groups;
}

function bookmarkChip(bookmark: BrowserBookmark, index: number): string {
  const title = bookmark.title || tabHost(bookmark.url);
  // The visible title comes from Edge. Tooltips stay at the host: a bookmark
  // URL may carry query tokens and must never leak through title/aria-label.
  return `<button type="button" class="desktop-browser-bookmark" data-browser-bookmark-index="${index}" title="${escapeHTML(tabHost(bookmark.url))}">
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
  // Click payloads are indexes into state.bookmarks, so no URL fragments or
  // tokens need to appear in the DOM at all.
  const indexes = new Map(bookmarks.map((bookmark, index) => [bookmark, index]));
  const parts: string[] = [];
  for (const [folder, items] of groupBookmarks(bookmarks)) {
    if (!folder) {
      parts.push(...items.map(bookmark => bookmarkChip(bookmark, indexes.get(bookmark) ?? -1)));
      continue;
    }
    parts.push(`<button type="button" class="desktop-browser-bookmark desktop-browser-bookmark-folder" data-browser-bookmark-folder="${escapeHTML(folder)}" title="${escapeHTML(folder)}" aria-label="收藏夹文件夹 ${escapeHTML(folder)}">
      ${icon("folder")}<span class="desktop-browser-bookmark-title">${escapeHTML(folder)}</span>
    </button>`);
  }
  return parts.join("");
}

function bookmarkMenuEntry(bookmark: BrowserBookmark, index: number): string {
  return `<li><button type="button" data-browser-bookmark-index="${index}" title="${escapeHTML(tabHost(bookmark.url))}"><span>${escapeHTML(bookmark.title || tabHost(bookmark.url))}</span><small>${escapeHTML(tabHost(bookmark.url))}</small></button></li>`;
}

function bookmarkMenuHTML(state: PanelState): string {
  if (!state.bookmarksMenu) return "";
  const all = state.bookmarksMenu === "all";
  const heading = all ? "更多收藏" : browserBookmarkFolderLabel(state.bookmarksMenuFolder);
  const indexes = new Map(state.bookmarks.map((bookmark, index) => [bookmark, index]));
  let items = "";
  if (all) {
    // The chevron is the overflow entry: only the first-level folders whose
    // chips do not fit, plus the bookmark-bar's own URLs. Both are actionable.
    const {folders, urls} = bookmarkOverflowEntries(state.bookmarks, state.bookmarkBarHiddenFolders);
    const parts: string[] = [];
    if (folders.length) {
      parts.push('<li class="desktop-browser-bookmark-menu-section">更多文件夹</li>');
      parts.push(...folders.map(([folder, group]) => `<li><button type="button" class="desktop-browser-bookmark-menu-folder-entry" data-browser-bookmark-menu-folder="${escapeHTML(folder)}" title="打开文件夹 ${escapeHTML(folder)}">${icon("folder")}<span>${escapeHTML(folder)}</span><small>${group.length} 个</small></button></li>`));
    }
    if (urls.length) {
      parts.push('<li class="desktop-browser-bookmark-menu-section">收藏栏网址</li>');
      parts.push(...urls.map(bookmark => bookmarkMenuEntry(bookmark, indexes.get(bookmark) ?? -1)));
    }
    items = parts.join("") || '<li class="desktop-browser-bookmark-menu-empty">没有隐藏的文件夹或收藏栏网址</li>';
  } else {
    const visible = state.bookmarks.filter(bookmark => browserBookmarkFolderLabel(bookmark.folder || "") === state.bookmarksMenuFolder);
    const groups = new Map<string, BrowserBookmark[]>();
    for (const bookmark of visible) {
      const subfolder = bookmarkSubfolderLabel(bookmark.folder || "");
      groups.set(subfolder, [...(groups.get(subfolder) || []), bookmark]);
    }
    items = visible.length
      ? [...groups].map(([subfolder, group]) => `${subfolder ? `<li class="desktop-browser-bookmark-menu-folder">${icon("folder")}<span>${escapeHTML(subfolder)}</span></li>` : ""}${group.map(bookmark => bookmarkMenuEntry(bookmark, indexes.get(bookmark) ?? -1)).join("")}`).join("")
      : '<li class="desktop-browser-bookmark-menu-empty">收藏夹为空</li>';
  }
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
    bookmarksMenu: "", bookmarksMenuFolder: "", bookmarkBarHiddenFolders: [], inUseProfileID: "", urlDraft: "", error: "",
    frameError: "", frameLive: false, startPageGrantID: "",
    controlMode: false, controlNotice: "", frameViewportWidth: 0, frameViewportHeight: 0,
  };

  let disposed = false;
  let bookmarkMenuReturnFocus: BookmarkMenuReturnFocus | null = null;
  let bookmarkHoverTimer = 0;
  const cancelBookmarkHoverClose = (): void => {
    window.clearTimeout(bookmarkHoverTimer);
    bookmarkHoverTimer = 0;
  };
  let frameTimer = 0;
  let frameAbort: AbortController | null = null;
  let frameURL = "";
  let frameSignature = "";
  let frameDelay = BROWSER_FRAME_MIN_DELAY_MS;
  let streamGeneration = 0;
  let syncTimer = 0;
  // State reads are asynchronous. A response that started before a release,
  // tab switch or hiding must never be applied afterwards: it carries the old
  // lease and would flip granted() back to true after the Owner released it.
  // The epoch invalidates in-flight reads on those actions; the request serial
  // makes only the newest concurrent read win.
  let stateSyncEpoch = 0;
  let stateSyncRequest = 0;
  // The explicit load() owns the loading flag through its own serial so a
  // superseded read cannot clear it while a newer read is still in flight.
  let stateLoadRequest = 0;
  // A terminal frame answer (403/404/409/410) must not be retried on the timer;
  // only a tab/lease change or an explicit reload clears this key.
  let terminalStreamKey = "";
  let lastManualSelectAt = 0;
  let bookmarkRequest = 0;
  let releaseTimer = 0;
  let releaseRequested = false;
  const painted = new WeakMap<HTMLElement, string>();

  // --- Owner control state -------------------------------------------------
  // The queue is explicit user intent (click/key/text/scroll). It is sent one
  // action at a time so the host input slot never has to interleave gestures.
  // A generation bump (tab/mode/lease change) discards pending input and makes
  // an in-flight reply harmless. Hover is a separate best-effort hint and never
  // blocks or reorders explicit input.
  let controlGeneration = 0;
  const controlQueue: BrowserPageAction[] = [];
  let controlInFlight: {generation: number} | null = null;
  let controlPendingClick: {x: number; y: number; at: number} | null = null;
  let controlClickTimer = 0;
  let controlWheel: {x: number; y: number; dx: number; dy: number} | null = null;
  let controlWheelTimer = 0;
  let controlHover: {x: number; y: number} | null = null;
  let controlHoverTimer = 0;
  let controlComposing = false;
  let controlCompositionDone = {text: "", at: 0};
  let controlFocusWanted = false;
  // Relative position of the last pointer inside the stage, used to keep the
  // hidden input sink (and therefore the IME candidate window) near the click
  // even after a repaint.
  let controlLastPointer = {rx: 0.5, ry: 0.5};
  let controlNotifyAt = 0;
  interface ControlGesture {
    startX: number;
    startY: number;
    lastX: number;
    lastY: number;
    pointerId: number;
    pointerType: string;
    button: number;
    startedAt: number;
    moved: boolean;
    scrolling: boolean;
    // Set once a contextmenu event has already claimed this gesture, so
    // pointerup cannot turn the same right-button press into a second click.
    contextQueued: boolean;
  }
  let controlGesture: ControlGesture | null = null;
  const controlPointers = new Map<number, {x: number; y: number}>();

  const coarsePointer = (): boolean =>
    typeof window.matchMedia === "function" && window.matchMedia("(pointer: coarse)").matches;
  const controlCapable = (): boolean => Boolean(state.grant?.capabilities?.includes("interact"));
  const controlReady = (): boolean => state.controlMode && Boolean(state.grant) && Boolean(selectedTab())
    && !(state.host && !state.host.available);
  // Coordinate actions need both the frame and the CSS viewport it belongs to.
  // Without that pair a click cannot be mapped safely (DPR is unknowable), so
  // the panel refuses the action instead of guessing from image pixels.
  const frameViewportReady = (): boolean => state.frameViewportWidth > 0 && state.frameViewportHeight > 0;
  const missingViewportNotice = (): void => controlNotice(BROWSER_VIEWPORT_WAITING_NOTICE);

  const controlHintText = (): string => {
    if (!state.grant) return "";
    if (!controlCapable()) return "当前授权不含页面操作能力；重新开启共享后可控制页面";
    if (state.host && !state.host.available) return "浏览器宿主不可用，输入已停用";
    if (!selectedTab()) return "当前没有可控制的页面";
    if (state.controlMode && !frameViewportReady()) return BROWSER_VIEWPORT_WAITING_NOTICE;
    if (state.controlNotice) return state.controlNotice;
    if (state.controlMode) return "控制中 · 点击画面操作页面；Shift+Esc 退出";
    return "只读浏览 · 打开控制后可点击、输入和滚动";
  };

  const paintControlBar = (): void => {
    const hint = container.querySelector<HTMLElement>("#desktop-browser-control-hint");
    if (hint) {
      hint.textContent = controlHintText();
      hint.title = controlHintText();
    }
    const toggle = container.querySelector<HTMLButtonElement>("#desktop-browser-control-toggle");
    if (toggle) {
      toggle.setAttribute("aria-pressed", String(state.controlMode));
      toggle.textContent = state.controlMode ? "退出控制" : "控制页面";
    }
    const preview = container.querySelector<HTMLElement>(".desktop-browser-preview");
    if (preview) preview.dataset.control = state.controlMode ? "on" : "off";
    const keyboard = container.querySelector<HTMLButtonElement>("#desktop-browser-keyboard");
    if (keyboard) keyboard.hidden = !state.controlMode;
  };

  const controlNotice = (message: string, tone: "error" | "notice" = "error"): void => {
    state.controlNotice = message;
    paintControlBar();
    if (Date.now() - controlNotifyAt > 1200) {
      controlNotifyAt = Date.now();
      notify(tone, message);
    }
  };

  const resetControlInput = (): void => {
    // Invalidate every in-flight and pending action. The request itself is not
    // aborted (the host may already be executing it), but its result can no
    // longer touch the new tab/mode.
    controlGeneration++;
    controlQueue.length = 0;
    controlInFlight = null;
    window.clearTimeout(controlClickTimer);
    controlClickTimer = 0;
    controlPendingClick = null;
    window.clearTimeout(controlWheelTimer);
    controlWheelTimer = 0;
    controlWheel = null;
    window.clearTimeout(controlHoverTimer);
    controlHoverTimer = 0;
    controlHover = null;
    controlGesture = null;
    controlPointers.clear();
    controlComposing = false;
    controlCompositionDone = {text: "", at: 0};
  };

  const exitControlMode = (reason = ""): void => {
    if (!state.controlMode) return;
    state.controlMode = false;
    state.controlNotice = reason;
    resetControlInput();
    controlFocusWanted = false;
    if (reason) notify("notice", reason);
  };

  const placeControlSink = (sink: HTMLTextAreaElement): void => {
    const rect = sink.parentElement?.getBoundingClientRect();
    if (!rect || rect.width <= 0 || rect.height <= 0) return;
    const left = Math.max(0, Math.min(rect.width - 2, controlLastPointer.rx * rect.width - 1));
    const top = Math.max(0, Math.min(rect.height - 4, controlLastPointer.ry * rect.height - 2));
    sink.style.left = `${Math.round(left)}px`;
    sink.style.top = `${Math.round(top)}px`;
  };

  const focusControlInput = (force = false): void => {
    if (!state.controlMode) return;
    // A coarse pointer opens the virtual keyboard only from the explicit
    // keyboard button; otherwise every tap would cover the page.
    if (!force && coarsePointer()) return;
    const sink = container.querySelector<HTMLTextAreaElement>("#desktop-browser-input");
    if (!sink) return;
    placeControlSink(sink);
    controlFocusWanted = true;
    sink.focus({preventScroll: true});
  };

  const toggleControlMode = (): void => {
    if (state.controlMode) {
      exitControlMode();
      paint();
      return;
    }
    if (!state.grant) return;
    if (!controlCapable()) {
      controlNotice("当前授权不含页面操作能力；请重新开启共享后再控制页面");
      return;
    }
    if (state.host && !state.host.available) {
      controlNotice("浏览器宿主不可用，暂时无法控制页面");
      return;
    }
    if (!selectedTab()) {
      controlNotice("请先选择一个页面，再打开控制");
      return;
    }
    state.controlMode = true;
    state.controlNotice = "";
    resetControlInput();
    paint();
    focusControlInput(false);
  };

  const mergeQueuedText = (text: string): boolean => {
    const last = controlQueue[controlQueue.length - 1];
    if (!last || last.kind !== "text") return false;
    const merged = `${last.text || ""}${text}`;
    if (browserTextByteLength(merged) > BROWSER_CONTROL_MAX_TEXT_BYTES) return false;
    last.text = merged;
    return true;
  };

  const enqueueControl = (action: BrowserPageAction, mergeText = false): boolean => {
    if (!controlReady()) return false;
    if (mergeText && mergeQueuedText(action.text || "")) {
      state.controlNotice = "";
      void pumpControl();
      return true;
    }
    if (controlQueue.length >= BROWSER_CONTROL_MAX_QUEUE) {
      controlNotice("操作太频繁，请稍后再试");
      return false;
    }
    state.controlNotice = "";
    controlQueue.push(action);
    paintControlBar();
    void pumpControl();
    return true;
  };

  const handleControlFailure = (error: unknown): void => {
    const details = errorDetails(error);
    const status = (error as {status?: number}).status;
    const terminal = status === 403 || status === 410
      || ["browser_grant_revoked", "browser_grant_expired", "browser_profile_in_use",
        "browser_session_changed", "browser_session_closed", "browser_tab_forbidden",
        "browser_tab_not_found", "browser_fence_stale", "browser_fence_expired",
        "browser_host_unavailable", "browser_host_timeout", "browser_lease_hold_failed"].includes(details.code);
    if (terminal) {
      const reason = details.code === "browser_host_unavailable" || details.code === "browser_host_timeout"
        ? "浏览器宿主不可用，已退出控制模式"
        : status === 403
          ? "浏览器授权已失效，已退出控制模式"
          : "浏览器状态已变化，已退出控制模式";
      exitControlMode(reason);
      paint();
      void syncState();
      return;
    }
    if (details.code === "browser_operation_busy") {
      controlNotice("页面正忙，操作未发送，请稍后重试");
      return;
    }
    if (details.code === "browser_action_invalid") {
      // Most often the Edge window was resized and the click now falls outside
      // the live viewport; the next frame carries the new size.
      controlNotice("页面尺寸已变化，操作未发送；请刷新画面后重试");
      return;
    }
    controlNotice(details.message || "页面操作失败");
  };

  const pumpControl = async (): Promise<void> => {
    if (controlInFlight || !controlReady() || !controlQueue.length) return;
    const grant = state.grant;
    const tab = selectedTab();
    if (!grant || !tab) return;
    const action = controlQueue.shift();
    if (!action) return;
    const ticket = {generation: controlGeneration};
    controlInFlight = ticket;
    try {
      await api.ownerBrowserAction(grant.id, tab.id, action);
      if (ticket.generation === controlGeneration) {
        // The host invalidated its cached frame; fetch the result at the live
        // rate instead of waiting out an unchanged-page backoff.
        frameDelay = BROWSER_FRAME_MIN_DELAY_MS;
        if (!disposed && current() && state.grant && selectedTab() && !frameAbort
          && terminalStreamKey !== streamKey()) schedule(0);
      }
    } catch (error) {
      if (ticket.generation === controlGeneration) handleControlFailure(error);
    } finally {
      if (controlInFlight === ticket) controlInFlight = null;
      if (ticket.generation === controlGeneration && controlQueue.length) void pumpControl();
    }
  };

  const sendControlText = (text: string, paste = false): void => {
    const value = String(text || "");
    if (!value || !controlReady()) return;
    const bytes = browserTextByteLength(value);
    if (paste && bytes > BROWSER_CONTROL_MAX_PASTE_BYTES) {
      controlNotice("粘贴内容过长，一次最多 16 KB");
      return;
    }
    if (!paste && bytes > BROWSER_CONTROL_MAX_TEXT_BYTES * 4) {
      controlNotice("输入内容过长");
      return;
    }
    for (const action of browserTextActions(value)) enqueueControl(action, !paste);
  };

  const flushPendingClick = (): void => {
    window.clearTimeout(controlClickTimer);
    controlClickTimer = 0;
    const pending = controlPendingClick;
    controlPendingClick = null;
    if (pending) enqueueControl({kind: "click", x: pending.x, y: pending.y});
  };

  const queueControlClick = (point: BrowserPoint, kind: "click" | "context_click", pointerType: string): void => {
    if (!controlReady()) return;
    flushControlWheel();
    if (kind === "context_click") {
      flushPendingClick();
      enqueueControl({kind: "context_click", x: point.x, y: point.y});
      return;
    }
    const now = Date.now();
    const doubleClickWindow = pointerType === "touch" ? BROWSER_CONTROL_CLICK_DELAY_MS + 60 : BROWSER_CONTROL_CLICK_DELAY_MS;
    if (controlPendingClick && now - controlPendingClick.at <= doubleClickWindow &&
        Math.hypot(point.x - controlPendingClick.x, point.y - controlPendingClick.y) <= 12) {
      window.clearTimeout(controlClickTimer);
      controlClickTimer = 0;
      const first = controlPendingClick;
      controlPendingClick = null;
      // The first click was still waiting; send only the double-click so the
      // page does not see three clicks.
      enqueueControl({kind: "double_click", x: first.x, y: first.y});
      return;
    }
    flushPendingClick();
    controlPendingClick = {x: point.x, y: point.y, at: now};
    const delay = pointerType === "touch" ? BROWSER_CONTROL_CLICK_DELAY_MS + 40 : BROWSER_CONTROL_CLICK_DELAY_MS;
    controlClickTimer = window.setTimeout(() => {
      controlClickTimer = 0;
      const pending = controlPendingClick;
      controlPendingClick = null;
      if (pending) enqueueControl({kind: "click", x: pending.x, y: pending.y});
    }, delay);
  };

  const flushControlWheel = (): void => {
    window.clearTimeout(controlWheelTimer);
    controlWheelTimer = 0;
    const wheel = controlWheel;
    if (!wheel) return;
    const stepX = browserScrollStep(wheel.dx);
    const stepY = browserScrollStep(wheel.dy);
    if (stepX === 0 && stepY === 0) {
      controlWheel = null;
      return;
    }
    wheel.dx -= stepX;
    wheel.dy -= stepY;
    if (!enqueueControl({kind: "scroll", x: wheel.x, y: wheel.y, delta_x: stepX, delta_y: stepY})) {
      controlWheel = null;
      return;
    }
    if (Math.abs(wheel.dx) >= 1 || Math.abs(wheel.dy) >= 1) {
      controlWheelTimer = window.setTimeout(() => {
        controlWheelTimer = 0;
        flushControlWheel();
      }, 30);
    } else {
      controlWheel = null;
    }
  };

  const queueControlScroll = (point: BrowserPoint, dx: number, dy: number): void => {
    if (!controlReady()) return;
    controlWheel ||= {x: point.x, y: point.y, dx: 0, dy: 0};
    controlWheel.x = point.x;
    controlWheel.y = point.y;
    controlWheel.dx += dx;
    controlWheel.dy += dy;
    if (!controlWheelTimer) {
      controlWheelTimer = window.setTimeout(() => {
        controlWheelTimer = 0;
        flushControlWheel();
      }, 40);
    }
  };

  // Hover is a best-effort pointer hint: one position at a time, skipped while
  // an explicit action is pending or in flight. Dropping an intermediate hover
  // never loses a click, key or text.
  const queueControlHover = (point: BrowserPoint): void => {
    controlHover = {x: point.x, y: point.y};
    if (controlHoverTimer) return;
    controlHoverTimer = window.setTimeout(() => {
      controlHoverTimer = 0;
      const hover = controlHover;
      controlHover = null;
      if (!hover || !controlReady() || controlInFlight || controlQueue.length
        || controlPendingClick || controlGesture) {
        if (hover) queueControlHover(hover);
        return;
      }
      const grant = state.grant;
      const tab = selectedTab();
      if (!grant || !tab) return;
      void api.ownerBrowserAction(grant.id, tab.id, {kind: "hover", x: hover.x, y: hover.y}).catch(() => {});
    }, 120);
  };

  const current = (): boolean => container.isConnected && Boolean(container.closest("#desktop-tool"));
  const selectedTab = (): BrowserTab | null =>
    state.tabs.find(tab => tab.id === state.selectedTabID) || null;
  const selectorTarget = (): HTMLElement | null => selector && selector.isConnected ? selector : null;
  const streamKey = (): string => browserPreviewStreamKey(state.grant?.id || "", state.selectedTabID);
  // Called whenever the lease or the visible selection changes underneath an
  // in-flight state read; see stateSyncEpoch.
  const invalidateStateSync = (): void => { stateSyncEpoch++; };

  const frameStatusText = (): string => {
    if (!state.grant) return "先选择 Profile 并打开共享开关，之后可查看并操作全部页面";
    if (state.binding) return "正在处理浏览器操作…";
    if (state.host && !state.host.available) return "浏览器宿主不可用，无法获取画面";
    if (state.frameError) return `画面暂不可用：${state.frameError}`;
    if (!state.tabs.length) return "当前没有打开的页面";
    if (!selectedTab()) return "选择页面后显示画面";
    if (!state.frameLive) return "正在获取画面…";
    return state.controlMode ? "实时画面 · 控制中" : "实时画面 · 只读";
  };

  // The preview bottom bar owns the two previously scattered status lines:
  // the exclusive-control state (left) and the read-only frame state (right).
  const controlStatusText = (session: BrowserSession | null): string => {
    if (state.error) return state.error;
    if (!state.grant) return "尚未开启浏览器共享";
    if (session?.error) return `浏览器共享中 · ${session.error}`;
    if (session?.host_reachable === false) return "浏览器共享中 · 宿主不可达";
    return "浏览器共享中";
  };

  const paintFrameStatus = (): void => {
    const session = state.grant?.session || state.profiles.find(item => item.id === state.profileID)?.session || null;
    const status = container.querySelector<HTMLElement>("#desktop-browser-frame-status");
    if (status) status.textContent = frameStatusText();
    const control = container.querySelector<HTMLElement>("#desktop-browser-control-status");
    if (control) {
      control.textContent = controlStatusText(session);
      control.title = statusText(state, session);
      control.dataset.tone = statusTone(state, session);
    }
    const image = container.querySelector<HTMLImageElement>("#desktop-browser-frame");
    const empty = container.querySelector<HTMLElement>("#desktop-browser-frame-empty");
    if (image && frameURL) {
      if (image.src !== frameURL) image.src = frameURL;
      image.hidden = false;
    }
    if (empty) {
      const live = Boolean(image && frameURL);
      empty.hidden = live;
      if (!live) empty.textContent = state.frameError ? "画面暂不可用" : frameStatusText();
    }
    // Every frame may change whether coordinate mapping is possible.
    paintControlBar();
  };

  const syncShell = (session: BrowserSession | null, profileName: string): void => {
    const status = statusText(state, session);
    const shell = selectorTarget() || container;
    shell.dataset.status = status;
    shell.dataset.profile = profileName;
    shell.dataset.granted = String(Boolean(state.grant));
    container.dataset.granted = String(Boolean(state.grant));
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
    node.querySelector("#desktop-browser-release")?.addEventListener("click", () => {
      if (!state.binding) void releaseControl();
    });
  };

  // bindControlSurface attaches the pointer/keyboard handlers that only exist
  // while the Owner enabled control mode. In read-only mode the stage has no
  // handlers at all, so a stray click can never reach the page.
  const bindControlSurface = (): void => {
    if (!state.controlMode) return;
    const stage = container.querySelector<HTMLElement>("#desktop-browser-stage");
    const image = container.querySelector<HTMLImageElement>("#desktop-browser-frame");
    const sink = container.querySelector<HTMLTextAreaElement>("#desktop-browser-input");
    if (!stage || !image || !sink) return;

    // Timestamp only for the "contextmenu arrived after pointerup" platform
    // order; the per-gesture contextQueued flag is what makes a held right
    // click produce exactly one context_click.
    let contextHandledAt = 0;
    const pointAt = (clientX: number, clientY: number): BrowserPoint | null =>
      browserViewportPoint(clientX, clientY, image.getBoundingClientRect(),
        image.naturalWidth, image.naturalHeight, state.frameViewportWidth, state.frameViewportHeight);

    const rememberPointer = (clientX: number, clientY: number): void => {
      const rect = image.getBoundingClientRect();
      if (rect.width <= 0 || rect.height <= 0) return;
      controlLastPointer = {
        rx: Math.max(0, Math.min(1, (clientX - rect.left) / rect.width)),
        ry: Math.max(0, Math.min(1, (clientY - rect.top) / rect.height)),
      };
    };

    const focusSinkAt = (clientX: number, clientY: number): void => {
      if (coarsePointer()) return;
      rememberPointer(clientX, clientY);
      placeControlSink(sink);
      controlFocusWanted = true;
      sink.focus({preventScroll: true});
    };

    // Touch drag scrolls by the equivalent page pixels. Moving the finger up
    // (decreasing clientY) must scroll the page down (positive deltaY).
    const controlScale = (): {x: number; y: number} => {
      const rect = image.getBoundingClientRect();
      if (rect.width <= 0 || rect.height <= 0 || image.naturalWidth <= 0 || image.naturalHeight <= 0) {
        return {x: 1, y: 1};
      }
      const scale = Math.min(rect.width / image.naturalWidth, rect.height / image.naturalHeight);
      const width = state.frameViewportWidth || image.naturalWidth;
      const height = state.frameViewportHeight || image.naturalHeight;
      return {x: width / (image.naturalWidth * scale), y: height / (image.naturalHeight * scale)};
    };

    stage.addEventListener("pointerdown", event => {
      if (!controlReady()) return;
      if (event.pointerType === "mouse" && event.button !== 0 && event.button !== 2) return;
      event.preventDefault();
      focusSinkAt(event.clientX, event.clientY);
      controlPointers.set(event.pointerId, {x: event.clientX, y: event.clientY});
      if (controlPointers.size > 1) {
        // No pinch support: a second finger cancels the in-flight gesture
        // instead of turning it into an accidental drag or click.
        controlGesture = null;
        return;
      }
      controlGesture = {
        startX: event.clientX, startY: event.clientY,
        lastX: event.clientX, lastY: event.clientY,
        pointerId: event.pointerId, pointerType: event.pointerType,
        button: event.button, startedAt: Date.now(), moved: false, scrolling: false,
        contextQueued: false,
      };
      try { stage.setPointerCapture(event.pointerId); } catch { /* synthetic pointer */ }
    });

    stage.addEventListener("pointermove", event => {
      if (!controlReady()) return;
      if (controlPointers.has(event.pointerId)) {
        controlPointers.set(event.pointerId, {x: event.clientX, y: event.clientY});
      }
      const gesture = controlGesture;
      if (!gesture || gesture.pointerId !== event.pointerId) {
        if (event.pointerType !== "touch" && controlPointers.size === 0) {
          const point = pointAt(event.clientX, event.clientY);
          if (point) queueControlHover(point);
        }
        return;
      }
      const distance = Math.hypot(event.clientX - gesture.startX, event.clientY - gesture.startY);
      // A hand click wobbles a few pixels; only a deliberate move becomes a drag.
      if (distance > 8) gesture.moved = true;
      if (gesture.pointerType === "touch") {
        if (!gesture.scrolling && distance > 10) gesture.scrolling = true;
        if (gesture.scrolling) {
          event.preventDefault();
          const point = pointAt(gesture.lastX, gesture.lastY) || pointAt(event.clientX, event.clientY);
          const scale = controlScale();
          const dx = (gesture.lastX - event.clientX) * scale.x;
          const dy = (gesture.lastY - event.clientY) * scale.y;
          if (point && (dx || dy)) queueControlScroll(point, dx, dy);
        }
      }
      gesture.lastX = event.clientX;
      gesture.lastY = event.clientY;
    }, {passive: false});

    stage.addEventListener("pointerup", event => {
      controlPointers.delete(event.pointerId);
      const gesture = controlGesture;
      if (!gesture || gesture.pointerId !== event.pointerId) return;
      controlGesture = null;
      try { stage.releasePointerCapture(event.pointerId); } catch { /* not captured */ }
      if (!controlReady()) return;
      // Without the frame's CSS viewport, a click/drag cannot be mapped safely;
      // refuse it and tell the Owner to wait for the next frame.
      if (!frameViewportReady()) {
        missingViewportNotice();
        return;
      }
      const startPoint = pointAt(gesture.startX, gesture.startY);
      const endPoint = pointAt(event.clientX, event.clientY);
      if (gesture.pointerType === "touch") {
        if (gesture.scrolling) {
          flushControlWheel();
          return;
        }
        const distance = Math.hypot(event.clientX - gesture.startX, event.clientY - gesture.startY);
        if (!startPoint || distance > 12) return;
        if (Date.now() - gesture.startedAt >= 500) {
          // Long press is the touch equivalent of the right mouse button.
          queueControlClick(startPoint, "context_click", "touch");
          return;
        }
        queueControlClick(startPoint, "click", "touch");
        return;
      }
      if (gesture.button === 2) {
        // contextmenu may fire before pointerup (Linux/macOS) or after
        // (Windows). contextQueued covers the first order regardless of how
        // long the button was held; contextHandledAt covers the second.
        if (!gesture.contextQueued && !gesture.moved && startPoint) {
          contextHandledAt = Date.now();
          queueControlClick(startPoint, "context_click", "mouse");
        } else {
          contextHandledAt = Date.now();
        }
        return;
      }
      // A contextmenu already consumed this gesture (macOS ctrl+left-click);
      // do not also send the normal click.
      if (gesture.contextQueued) return;
      if (!startPoint || !endPoint) return;
      if (gesture.moved) {
        if (Math.hypot(endPoint.x - startPoint.x, endPoint.y - startPoint.y) > 4) {
          enqueueControl({
            kind: "drag", x: startPoint.x, y: startPoint.y,
            to_x: endPoint.x, to_y: endPoint.y,
          });
        }
        return;
      }
      queueControlClick(startPoint, "click", "mouse");
    });

    stage.addEventListener("pointercancel", () => {
      controlGesture = null;
      controlPointers.clear();
      flushControlWheel();
    });
    stage.addEventListener("contextmenu", event => {
      event.preventDefault();
      if (!controlReady()) return;
      if (!frameViewportReady()) {
        missingViewportNotice();
        return;
      }
      const gesture = controlGesture;
      if (gesture) {
        // Linux/macOS can fire contextmenu while the button is still down.
        // Claim the gesture once; pointerup then only records the timestamp.
        const rightButton = gesture.button === 2;
        const macControlClick = gesture.button === 0 && event.ctrlKey;
        if ((!rightButton && !macControlClick) || gesture.contextQueued || gesture.moved) return;
        const point = pointAt(event.clientX, event.clientY);
        if (!point) return;
        gesture.contextQueued = true;
        contextHandledAt = Date.now();
        queueControlClick(point, "context_click", "mouse");
        return;
      }
      // Windows can fire it after pointerup. The pointerup path already set
      // contextHandledAt for a right-button gesture; do not queue a second one.
      if (Date.now() - contextHandledAt < 250) return;
      const point = pointAt(event.clientX, event.clientY);
      if (!point) return;
      contextHandledAt = Date.now();
      queueControlClick(point, "context_click", "mouse");
    });
    stage.addEventListener("dragstart", event => event.preventDefault());
    stage.addEventListener("wheel", event => {
      if (!controlReady()) return;
      // Ctrl/Cmd+wheel is the Owner zooming the panel, not page input.
      if (event.ctrlKey || event.metaKey) return;
      event.preventDefault();
      if (!frameViewportReady()) {
        missingViewportNotice();
        return;
      }
      const point = pointAt(event.clientX, event.clientY);
      if (!point || (!event.deltaX && !event.deltaY)) return;
      const scale = event.deltaMode === 1 ? 16 : event.deltaMode === 2 ? stage.clientHeight : 1;
      queueControlScroll(point, event.deltaX * scale, event.deltaY * scale);
    }, {passive: false});

    sink.addEventListener("keydown", event => {
      if (!controlReady()) return;
      if (event.key === "Escape" && event.shiftKey) {
        event.preventDefault();
        event.stopPropagation();
        exitControlMode("已退出控制模式");
        paint();
        return;
      }
      const command = browserKeyCommand(event);
      if (!command) return;
      event.preventDefault();
      event.stopPropagation();
      if (command.kind === "text") sendControlText(command.text);
      else enqueueControl({kind: "key", keys: command.keys});
    });
    sink.addEventListener("input", event => {
      if (!controlReady()) return;
      if (controlComposing) return;
      const input = event as InputEvent;
      const data = String(input.data || "");
      const inputType = String(input.inputType || "");
      // The sink is a transport, never a local editor.
      sink.value = "";
      if (data) {
        if (controlCompositionDone.text === data && Date.now() - controlCompositionDone.at < 150) return;
        sendControlText(data);
        return;
      }
      if (inputType === "deleteContentBackward") enqueueControl({kind: "key", keys: ["Backspace"]});
      else if (inputType === "deleteContentForward") enqueueControl({kind: "key", keys: ["Delete"]});
      else if (inputType === "insertLineBreak" || inputType === "insertParagraph") {
        enqueueControl({kind: "key", keys: ["Enter"]});
      }
    });
    sink.addEventListener("compositionstart", () => {
      controlComposing = true;
    });
    sink.addEventListener("compositionend", event => {
      controlComposing = false;
      const text = String(event.data || sink.value || "");
      sink.value = "";
      controlCompositionDone = {text, at: Date.now()};
      if (text) sendControlText(text);
    });
    sink.addEventListener("paste", event => {
      if (!controlReady()) return;
      event.preventDefault();
      const text = event.clipboardData?.getData("text/plain") || "";
      sendControlText(text, true);
    });
    sink.addEventListener("focus", () => {
      controlFocusWanted = true;
    });
    sink.addEventListener("blur", () => {
      if (document.activeElement !== sink) controlFocusWanted = false;
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
      else openBookmarksMenu("all", "", {kind: "more"});
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
        invalidateStateSync();
        lastManualSelectAt = Date.now();
        // Input aimed at the previous page must never land on the new tab.
        resetControlInput();
        state.controlNotice = "";
        state.frameViewportWidth = 0;
        state.frameViewportHeight = 0;
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
    container.querySelectorAll<HTMLElement>("[data-browser-bookmark-index]").forEach(button => {
      button.addEventListener("click", () => {
        const index = Number.parseInt(button.dataset.browserBookmarkIndex || "", 10);
        const bookmark = Number.isInteger(index) && index >= 0 ? state.bookmarks[index] : undefined;
        const target = bookmark?.url || "";
        state.bookmarksMenu = "";
        state.bookmarksMenuFolder = "";
        bookmarkMenuReturnFocus = null;
        if (!state.binding && target) void openPage(target);
      });
    });
    container.querySelectorAll<HTMLElement>("[data-browser-bookmark-folder]").forEach(button => {
      button.addEventListener("click", () => {
        const folder = button.dataset.browserBookmarkFolder || "";
        openBookmarksMenu("folder", folder, {kind: "folder", folder});
      });
    });
    container.querySelectorAll<HTMLElement>("[data-browser-bookmark-menu-folder]").forEach(button => {
      button.addEventListener("click", () => {
        const folder = button.dataset.browserBookmarkMenuFolder || "";
        openBookmarksMenu("folder", folder, {kind: "folder", folder});
      });
    });
    container.querySelector("#desktop-browser-control-toggle")?.addEventListener("click", () => {
      toggleControlMode();
    });
    container.querySelector("#desktop-browser-keyboard")?.addEventListener("click", () => {
      // On touch this button is the only way to open the soft keyboard; the
      // same button closes it again so the page is not permanently covered.
      const sink = container.querySelector<HTMLTextAreaElement>("#desktop-browser-input");
      if (sink && document.activeElement === sink) {
        controlFocusWanted = false;
        sink.blur();
        return;
      }
      focusControlInput(true);
    });
    bindControlSurface();
  };

  const selectorHTML = (session: BrowserSession | null): string => {
    const busy = state.loading || state.binding;
    const noProfiles = !state.profiles.length;
    const hostDown = Boolean(state.host && !state.host.available);
    const heldByOther = !state.grant && Boolean(state.inUseProfileID) && state.inUseProfileID === state.profileID;
    // One Owner-facing switch. On creates the continuous grant; off releases
    // it. There is no expiry, renewal or transfer control to show.
    const primary = state.grant
      ? `<button type="button" id="desktop-browser-release" class="primary desktop-browser-control" ${busy ? "disabled" : ""}>关闭共享浏览器</button>`
      : `<button type="button" id="desktop-browser-attach" class="primary desktop-browser-control" ${busy || noProfiles || hostDown || heldByOther ? "disabled" : ""}>${icon("link")}<span>共享浏览器</span></button>`;
    // This popover is only a target picker. No preview, tab strip, address bar
    // or bookmark bar may be rendered here.
    return `
      <div class="desktop-browser-toolbar" role="group" aria-label="内置浏览器控制">
        <label class="desktop-window-label desktop-browser-profile-label"><span>Profile</span><select id="desktop-browser-profile" aria-label="浏览器 Profile" ${state.grant ? "disabled" : ""}>${profileOptions(state.profiles, state.profileID)}</select></label>
        <button type="button" id="desktop-browser-refresh" class="icon-button" title="刷新浏览器状态" aria-label="刷新浏览器状态" ${busy ? "disabled" : ""}>${icon("refresh")}</button>
        ${primary}
      </div>
      <p id="desktop-browser-status" class="desktop-browser-status" data-tone="${statusTone(state, session)}" role="status">${escapeHTML(statusText(state, session))}</p>`;
  };

  const controlHTML = (session: BrowserSession | null): string => {
    if (!state.grant) {
      // Before the first status read lands there is no basis for "create a
      // Profile" or "host down"; avoid a misleading flash in the main panel.
      if (state.loading && !state.host) {
        return `<p class="desktop-empty" role="status" aria-busy="true">正在读取浏览器状态…</p>`;
      }
      const noProfiles = !state.profiles.length;
      const hostDown = Boolean(state.host && !state.host.available);
      const title = noProfiles ? "创建一个浏览器 Profile" : hostDown ? "重新连接浏览器宿主" : "还没有开启浏览器共享";
      const hint = noProfiles
        ? "创建后即可选择 Profile、开启共享并查看实时画面。"
        : hostDown ? "确认 AHA2 的浏览器宿主进程正在运行，然后重新检测。"
          : "在「共享目标」里选择 Profile 并打开共享开关，之后这里会显示标签页、地址栏、收藏夹和实时画面。";
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
        // Only the page currently shown in the preview is "当前". Both the
        // preview and this strip read state.selectedTabID, so they cannot
        // diverge. The host's `active`/`focused` flags describe the real
        // windows and must never produce several "当前" labels or highlights.
        return `<li class="desktop-browser-tab-item ${selected ? "active" : ""}" data-browser-tab-item="${escapeHTML(tab.id)}">
          <button type="button" class="desktop-browser-tab ${selected ? "active" : ""}" data-browser-tab="${escapeHTML(tab.id)}" aria-pressed="${selected}" ${selected ? 'aria-current="page"' : ""} title="${escapeHTML(tab.url)}">
            ${faviconMark(tab.url, "desktop-browser-tab-favicon")}
            <span class="desktop-browser-tab-title">${escapeHTML(tab.title || host || "未命名页面")}</span>
            <span class="desktop-browser-tab-origin" title="页面来源">${tab.origin === "owner" ? "Owner" : "Agent"}</span>
          </button>
          <button type="button" class="icon-button desktop-browser-tab-close" data-browser-tab-close="${escapeHTML(tab.id)}" title="关闭页面" aria-label="关闭页面 ${escapeHTML(tab.title || host)}">${icon("close")}</button>
        </li>`;
      }).join("")
      : `<li class="desktop-empty desktop-browser-tabs-empty">当前没有打开的页面</li>`;
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
      <div class="desktop-browser-controlbar" role="group" aria-label="页面控制">
        <button type="button" id="desktop-browser-control-toggle" class="desktop-browser-control-toggle" aria-pressed="${state.controlMode}" title="${state.controlMode ? "退出控制模式，回到只读浏览" : "打开控制模式后可点击、输入和滚动页面"}" ${controlCapable() && !(state.host && !state.host.available) && state.tabs.length ? "" : "disabled"}>${state.controlMode ? "退出控制" : "控制页面"}</button>
        <span id="desktop-browser-control-hint" class="desktop-browser-control-hint" role="status" aria-live="polite" title="${escapeHTML(controlHintText())}">${escapeHTML(controlHintText())}</span>
        <button type="button" id="desktop-browser-keyboard" class="desktop-browser-keyboard" title="打开输入键盘" ${state.controlMode ? "" : "hidden"}>键盘输入</button>
      </div>
      <figure class="desktop-browser-preview" data-control="${state.controlMode ? "on" : "off"}">
        <div id="desktop-browser-stage" class="desktop-browser-frame-stage">
          <img id="desktop-browser-frame" alt="${state.controlMode ? "内置浏览器画面，控制模式" : "内置浏览器只读画面"}" draggable="false" hidden>
          <p id="desktop-browser-frame-empty" class="desktop-browser-frame-empty" role="status">${escapeHTML(frameStatusText())}</p>
          ${state.controlMode ? '<textarea id="desktop-browser-input" class="desktop-browser-input" aria-label="页面键盘输入，按键将发送到当前页面" autocomplete="off" autocapitalize="off" autocorrect="off" spellcheck="false"></textarea>' : ""}
        </div>
        <figcaption class="desktop-browser-statusbar">
          <span id="desktop-browser-control-status" class="desktop-browser-control-status" data-tone="${statusTone(state, session)}" title="${escapeHTML(statusText(state, session))}">${escapeHTML(controlStatusText(session))}</span>
          <span id="desktop-browser-frame-status" class="desktop-browser-frame-status" role="status" title="${state.controlMode ? "实时画面，控制模式开启" : "只读预览，不转发点击、输入或滚动"}">${escapeHTML(frameStatusText())}</span>
        </figcaption>
      </figure>
    </div>`;
  };

  // The chip a folder menu is anchored to. A chip hidden by the overflow
  // measure cannot be an anchor (or a hover target); the chevron takes over.
  const visibleFolderChip = (folder: string): HTMLElement | null =>
    [...container.querySelectorAll<HTMLElement>("[data-browser-bookmark-folder]")]
      .find(chip => chip.dataset.browserBookmarkFolder === folder && !chip.hidden) || null;

  // One overflow entry only: chips that do not fit are hidden and the "更多"
  // menu is the single way to reach them (it lists the hidden folders and the
  // bar's own URLs). The bar itself never scrolls horizontally.
  const measureBookmarkOverflow = (): void => {
    const items = container.querySelector<HTMLElement>("#desktop-browser-bookmarks");
    const more = container.querySelector<HTMLElement>("#desktop-browser-bookmarks-more");
    if (!items || !more) return;
    const chips = [...items.querySelectorAll<HTMLElement>(".desktop-browser-bookmark")];
    if (!chips.length) {
      more.hidden = true;
      state.bookmarkBarHiddenFolders = [];
      return;
    }
    more.hidden = false;
    chips.forEach(chip => { chip.hidden = false; });
    const right = items.getBoundingClientRect().right;
    let overflow = false;
    for (const chip of chips) {
      if (chip.getBoundingClientRect().right > right + 1) {
        chip.hidden = true;
        overflow = true;
      }
    }
    more.hidden = !overflow;
    // The chevron menu is built during paint, so keep the hidden-folder list
    // current for the next paint (resize, state sync, reopen). An open "all"
    // menu is refreshed right away: shrinking the panel must not leave it
    // listing only the folders that were hidden at the previous width.
    const hiddenFolders = chips
      .filter(chip => chip.hidden && chip.dataset.browserBookmarkFolder)
      .map(chip => chip.dataset.browserBookmarkFolder || "");
    const hiddenChanged = hiddenFolders.length !== state.bookmarkBarHiddenFolders.length
      || hiddenFolders.some((folder, index) => folder !== state.bookmarkBarHiddenFolders[index]);
    state.bookmarkBarHiddenFolders = hiddenFolders;
    if (hiddenChanged && state.bookmarksMenu === "all") refreshOpenBookmarksMenu();
  };

  // Anchor the popover to the clicked folder chip: its left edge, immediately
  // below the bar. A chip hidden by overflow (or the chevron itself) anchors to
  // the "更多" toggle, and the panel/viewport edge wins when they conflict.
  const positionBookmarkMenu = (): void => {
    const bar = container.querySelector<HTMLElement>("#desktop-browser-bookmarkbar");
    const menu = container.querySelector<HTMLElement>("#desktop-browser-bookmarks-menu");
    if (!bar || !menu) return;
    let anchor: HTMLElement | null = null;
    if (state.bookmarksMenu === "folder") anchor = visibleFolderChip(state.bookmarksMenuFolder);
    anchor = anchor || container.querySelector<HTMLElement>("#desktop-browser-bookmarks-more");
    const barRect = bar.getBoundingClientRect();
    const style = getComputedStyle(bar);
    const borderLeft = Number.parseFloat(style.borderLeftWidth) || 0;
    const borderTop = Number.parseFloat(style.borderTopWidth) || 0;
    const anchorRect = anchor?.getBoundingClientRect();
    // Absolute children are positioned from the bar's padding box.
    const maxLeft = Math.max(0, bar.clientWidth - menu.offsetWidth);
    const desiredLeft = anchorRect && anchorRect.width
      ? anchorRect.left - (barRect.left + borderLeft)
      : maxLeft; // Chevron not measured yet: start right-aligned, then relayout.
    menu.style.left = `${Math.round(Math.min(Math.max(0, desiredLeft), maxLeft))}px`;
    menu.style.right = "auto";
    // Same origin: place the outer top edge exactly at the bar's bottom edge.
    menu.style.top = `${Math.round(barRect.height - borderTop)}px`;
    // Never let the popover poke out of the panel or the viewport. The actual
    // available space wins over the CSS preferred height; a very short window
    // gets a short, internally scrollable menu instead of a 96px minimum that
    // overflows.
    const panelRect = container.getBoundingClientRect();
    const viewportBottom = Math.min(window.innerHeight, panelRect.bottom) - 8;
    const menuRect = menu.getBoundingClientRect();
    const available = Math.max(0, viewportBottom - menuRect.top);
    const preferred = Math.min(window.innerHeight * 0.45, 320);
    menu.style.maxHeight = `${Math.max(0, Math.floor(Math.min(available, preferred)))}px`;
  };

  const relayoutBookmarks = (): void => {
    measureBookmarkOverflow();
    positionBookmarkMenu();
  };

  const paint = (): void => {
    if (!current()) return;
    const session = state.grant?.session || state.profiles.find(item => item.id === state.profileID)?.session || null;
    const selectedProfile = state.profiles.find(item => item.id === state.profileID);
    const selectorNode = selectorTarget();
    if (selectorNode && setNodeHTML(selectorNode, selectorHTML(session))) bindSelector();
    if (setNodeHTML(container, controlHTML(session))) {
      bindControl();
      // A repaint while the Owner is typing must not steal focus from the
      // address bar or drop the page input sink. Only restore the sink when it
      // owned focus before the rebuild; never focus it proactively.
      if (state.controlMode && controlFocusWanted) {
        const sink = container.querySelector<HTMLTextAreaElement>("#desktop-browser-input");
        if (sink) {
          placeControlSink(sink);
          sink.focus({preventScroll: true});
        }
      }
      // The overflow measure decides which chips (and the chevron) are
      // visible; reposition once that layout pass has run.
      requestAnimationFrame(relayoutBookmarks);
    } else {
      measureBookmarkOverflow();
    }
    positionBookmarkMenu();
    paintFrameStatus();
    syncShell(session, selectedProfile?.name || "");
  };

  // Rebuild an open "all" menu after the overflow set changes. Keep the
  // keyboard position on the same folder/bookmark when it still exists.
  const refreshOpenBookmarksMenu = (): void => {
    const menu = container.querySelector<HTMLElement>("#desktop-browser-bookmarks-menu");
    if (!menu) return;
    const active = document.activeElement instanceof HTMLElement && menu.contains(document.activeElement)
      ? document.activeElement
      : null;
    const folder = active?.dataset.browserBookmarkMenuFolder || "";
    const index = active?.dataset.browserBookmarkIndex || "";
    paint();
    if (!active) return;
    const next = container.querySelector<HTMLElement>("#desktop-browser-bookmarks-menu");
    if (!next) return;
    const folderEntry = folder
      ? [...next.querySelectorAll<HTMLElement>("[data-browser-bookmark-menu-folder]")].find(button => button.dataset.browserBookmarkMenuFolder === folder)
      : null;
    const bookmarkEntry = index
      ? [...next.querySelectorAll<HTMLElement>("[data-browser-bookmark-index]")].find(button => button.dataset.browserBookmarkIndex === index)
      : null;
    (folderEntry || bookmarkEntry || next.querySelector<HTMLElement>("[data-browser-bookmark-menu-folder], [data-browser-bookmark-index]"))?.focus({preventScroll: true});
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
    // Clear the id when the timer fires: `frameTimer` then means "a frame is
    // pending", which lets the sync timer restart a loop that the host-down
    // early return left unscheduled.
    frameTimer = window.setTimeout(() => { frameTimer = 0; void tick(); }, delay);
  };

  const tick = async (): Promise<void> => {
    if (disposed || !current()) return;
    // Hidden pages pause the stream; the visibility handler restarts it with a
    // fresh tab sync, so a stale screenshot is never left behind.
    if (document.visibilityState === "hidden") {
      stopStream();
      invalidateStateSync();
      resetControlInput();
      controlFocusWanted = false;
      return;
    }
    const grant = state.grant;
    const tab = selectedTab();
    if (!grant || !tab || (state.host && !state.host.available)) {
      // A dead host means the last bitmap is no longer a live view; clear it
      // and let the sync timer restart the loop when the host comes back.
      if (state.host && !state.host.available) stopStream();
      paintFrameStatus();
      return;
    }
    const controller = new AbortController();
    frameAbort = controller;
    const generation = streamGeneration;
    try {
      const frame = await api.browserTabFrame(grant.id, tab.id, controller.signal);
      // The owner may have switched tabs or closed the panel while this frame
      // was in flight; a stale tab screenshot must never reach the new view.
      if (disposed || generation !== streamGeneration) return;
      const buffer = new Uint8Array(await frame.blob.arrayBuffer());
      if (disposed || generation !== streamGeneration) return;
      const nextSignature = browserFrameSignature(buffer);
      frameDelay = nextBrowserFrameDelay(frameSignature, nextSignature, frameDelay);
      frameSignature = nextSignature;
      state.frameLive = true;
      state.frameError = "";
      // The viewport belongs to this exact image. A frame without headers is
      // not mappable: coordinate input stays paused until a valid pair arrives.
      state.frameViewportWidth = frame.viewportWidth;
      state.frameViewportHeight = frame.viewportHeight;
      if (frame.viewportWidth > 0 && frame.viewportHeight > 0
        && state.controlNotice === BROWSER_VIEWPORT_WAITING_NOTICE) {
        state.controlNotice = "";
      }
      const nextURL = URL.createObjectURL(new Blob([buffer], {type: frame.blob.type || "image/jpeg"}));
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
      const status = (error as {status?: number}).status;
      const code = String((error as {code?: string}).code || "");
      // A dead host is not a terminal answer for this tab: clearing the bitmap
      // and stopping the loop avoids hammering a host that cannot answer. The
      // tab-sync timer restarts the preview once /browser reports it healthy.
      if (status === httpStatusServiceUnavailable || code === browserHostUnavailableCode) {
        stopStream();
        paintFrameStatus();
        return;
      }
      // Revoked/closed/wrong-session answers are terminal for the preview; do
      // not keep hammering the API until the owner refreshes or the tab/lease
      // changes. A closed tab is removed immediately and the next page (or the
      // explicit empty state) takes over.
      if (status === httpStatusForbidden || status === httpStatusNotFound || status === httpStatusConflict || status === httpStatusGone) {
        const closed = status === httpStatusNotFound || status === httpStatusGone;
        if (closed) {
          state.tabs = state.tabs.filter(item => item.id !== tab.id);
          state.selectedTabID = browserFollowTabID(state.tabs, "", false);
          state.frameError = "";
          state.urlDraft = selectedTab()?.url || "";
        }
        stopStream();
        // The terminal answer belongs to the tab that just failed, never to the
        // next tab we switch to after a close.
        terminalStreamKey = closed ? "" : `${grant.id}:${tab.id}`;
        paint();
        if (closed && selectedTab()) startStream();
        void syncState();
        return;
      }
      paintFrameStatus();
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
    terminalStreamKey = "";
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

  interface AppliedBrowserState {
    changed: boolean;
    grantChanged: boolean;
    selectionChanged: boolean;
    tabsChanged: boolean;
    setChanged: boolean;
  }

  // Shared by the explicit load and the tab-sync timer so both paths agree on
  // how grants, tabs and the preview selection are derived from one read.
  const applyBrowserState = (data: TaskBrowserState): AppliedBrowserState => {
    const beforeGrant = state.grant?.id || "";
    const beforeIDs = state.tabs.map(tab => tab.id).join("\n");
    const beforeTabs = state.tabs.map(tab => `${tab.id}|${tab.url}|${tab.title}|${tab.active ? 1 : 0}|${tab.focused ? 1 : 0}`).join("\n");
    const beforeProfile = state.profileID;
    const beforeHost = state.host ? `${state.host.available}|${state.host.edge_available}|${state.host.code || ""}` : "";
    const beforeControl = state.controlMode;

    state.profiles = data.profiles || [];
    state.host = data.host || null;
    const active = (data.grants || []).find(grant => grant.status === "active") || null;
    state.grant = active;
    // The server syncs every visible http(s) page under the exclusive lease,
    // including pages the Owner opened by hand.
    state.tabs = active ? (data.tabs || []) : [];
    if (active) state.inUseProfileID = "";
    if (active && active.id !== beforeGrant) {
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
      const profile = state.profiles.find(item => item.id === state.profileID);
      if (profile?.lease_task_id) state.inUseProfileID = profile.id;
    }
    // A tab click in the panel is optimistic until the host confirms it; do
    // not let a sync that started earlier pull the selection back.
    const followActive = Date.now() - lastManualSelectAt > 2500;
    const nextSelected = browserFollowTabID(state.tabs, state.selectedTabID, followActive);
    const selectionChanged = nextSelected !== state.selectedTabID;
    if (selectionChanged) {
      // Input aimed at the previous page must never land on the new tab.
      resetControlInput();
      state.controlNotice = "";
      state.frameViewportWidth = 0;
      state.frameViewportHeight = 0;
      state.selectedTabID = nextSelected;
      state.frameError = "";
      state.frameLive = false;
      terminalStreamKey = "";
    }
    if (active && active.id !== beforeGrant) {
      resetControlInput();
      state.controlNotice = "";
      state.frameViewportWidth = 0;
      state.frameViewportHeight = 0;
    }
    // A revoked/expired/transferred lease or a dead host drops the mode back
    // to read-only immediately; pending input is cleared, never replayed.
    if (state.controlMode) {
      if (!active) exitControlMode("浏览器共享已结束，已退出控制模式");
      else if (active.id !== beforeGrant) exitControlMode("浏览器共享已更换，已退出控制模式");
      else if (state.host && !state.host.available) exitControlMode("浏览器宿主不可用，已退出控制模式");
    }
    const selected = selectedTab();
    if (selected && document.activeElement?.id !== "desktop-browser-url") state.urlDraft = selected.url;
    const afterIDs = state.tabs.map(tab => tab.id).join("\n");
    const afterTabs = state.tabs.map(tab => `${tab.id}|${tab.url}|${tab.title}|${tab.active ? 1 : 0}|${tab.focused ? 1 : 0}`).join("\n");
    const afterHost = state.host ? `${state.host.available}|${state.host.edge_available}|${state.host.code || ""}` : "";
    return {
      changed: beforeGrant !== (active?.id || "") || beforeTabs !== afterTabs
        || selectionChanged || beforeProfile !== state.profileID || beforeHost !== afterHost
        || beforeControl !== state.controlMode,
      grantChanged: beforeGrant !== (active?.id || ""),
      selectionChanged,
      tabsChanged: beforeTabs !== afterTabs,
      setChanged: beforeIDs !== afterIDs,
    };
  };

  const syncState = async (): Promise<void> => {
    // Opening/closing a tab already reloads through load(); a sync racing it
    // would repaint a half-applied state. Hidden pages wait for the
    // visibility handler instead of polling behind the Owner's back.
    if (disposed || !current() || state.loading || state.binding || document.visibilityState === "hidden") return;
    const epoch = stateSyncEpoch;
    const request = ++stateSyncRequest;
    try {
      const result = await api.taskBrowser(taskID);
      // Drop a read that was invalidated (release/tab switch/hidden) or
      // superseded by a newer one; applying it could resurrect a stale lease.
      if (disposed || epoch !== stateSyncEpoch || request !== stateSyncRequest) return;
      const data = result?.browser;
      if (!data) return;
      const applied = applyBrowserState(data);
      // A successfully read, healthy state supersedes an error from an earlier
      // operation; leaving it pinned would keep the panel red after recovery.
      const clearedError = Boolean(state.error);
      if (clearedError && state.grant && !(state.host && !state.host.available)) state.error = "";
      // Only a tab added/removed is new evidence about the lease/session. A
      // title update is not, so it must not retry a terminal 403/409 frame.
      if (applied.setChanged) terminalStreamKey = "";
      // Rebuilding the surface while the Owner is typing in the address bar
      // would drop focus and the caret; restore both after the repaint.
      const urlInput = document.activeElement instanceof HTMLInputElement && document.activeElement.id === "desktop-browser-url"
        ? document.activeElement : null;
      const caretStart = urlInput?.selectionStart ?? null;
      const caretEnd = urlInput?.selectionEnd ?? null;
      if (applied.changed || clearedError) paint();
      if (urlInput && (applied.changed || clearedError)) {
        const next = container.querySelector<HTMLInputElement>("#desktop-browser-url");
        if (next) {
          next.focus({preventScroll: true});
          if (caretStart !== null && caretEnd !== null) next.setSelectionRange(caretStart, caretEnd);
        }
      }
      if (applied.selectionChanged || applied.grantChanged) {
        startStream();
      } else if (!frameTimer && !frameAbort && state.grant && selectedTab()
        && !(state.host && !state.host.available) && terminalStreamKey !== streamKey()) {
        startStream();
      } else {
        paintFrameStatus();
      }
    } catch {
      // A transient read failure must not blank the current tab or preview;
      // the next tick retries.
    }
    if (state.grant && !state.bookmarksLoaded && !state.bookmarksLoading) void loadBookmarks(state.grant);
    ensureStartPage();
  };

  const scheduleSync = (delay = BROWSER_STATE_SYNC_MS): void => {
    window.clearTimeout(syncTimer);
    if (disposed) return;
    syncTimer = window.setTimeout(() => {
      syncTimer = 0;
      void syncState().finally(() => scheduleSync());
    }, delay);
  };

  const load = async (): Promise<void> => {
    if (disposed) return;
    // An explicit read supersedes any tab-sync read already in flight. Capture
    // the epoch and a request serial of its own: a response that lands after a
    // release, tab switch or hide must not repaint the old lease, restart its
    // preview or open a start page.
    const epoch = stateSyncEpoch;
    const request = ++stateSyncRequest;
    const loadRequest = ++stateLoadRequest;
    state.loading = true;
    state.error = "";
    paint();
    const stale = (): boolean =>
      disposed || epoch !== stateSyncEpoch || request !== stateSyncRequest;
    let dropped = false;
    try {
      const result = await api.taskBrowser(taskID);
      // The Owner may have left browser mode, released the lease or switched
      // tabs while this read was in flight; a stale answer must not be applied.
      if (stale()) {
        dropped = true;
      } else {
        const data = result?.browser;
        if (!data) throw new Error("浏览器状态不可用");
        applyBrowserState(data);
      }
    } catch (error) {
      // A failure from a superseded read is equally stale: it must not pin an
      // error over the state the newer read already applied.
      if (stale()) {
        dropped = true;
      } else {
        state.error = error instanceof Error ? error.message : String(error);
      }
    } finally {
      // Only the newest load owns the loading flag; a superseded one must not
      // clear it while the newer read is still in flight.
      if (loadRequest === stateLoadRequest) state.loading = false;
    }
    if (dropped) {
      // The stale answer is not applied, but the loading flag it owned may
      // still be disabling the controls painted when the read started. Repaint
      // the current (never the stale) state so the buttons match the cleared
      // flag; a newer load owns the flag and will repaint for itself.
      if (!disposed && loadRequest === stateLoadRequest) paint();
      return;
    }
    // Dropped answers returned above, so none of the frame restart or
    // follow-up reads below can run for them.
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
    invalidateStateSync();
    lastManualSelectAt = Date.now();
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
    if (!grant || state.startPageGrantID === grant.id) return;
    // The start page is only a bootstrap for a brand-new grant. Consume the
    // marker even when pages already exist, so the Owner closing the last real
    // page clears the panel instead of silently opening a replacement.
    state.startPageGrantID = grant.id;
    if (state.tabs.length) return;
    void openPage(browserStartPageURL(window.location.origin), true);
  };

  const openStartPage = async (): Promise<void> => {
    if (!state.grant) return;
    await openPage(browserStartPageURL(window.location.origin), true);
  };

  const releaseControl = async (): Promise<void> => {
    if (!state.grant) return;
    // Any state read still in flight belongs to the lease being released.
    invalidateStateSync();
    exitControlMode();
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
      notify("notice", "已关闭浏览器共享；页面保持打开，其他 Task 可以接手");
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

  const openBookmarksMenu = (kind: "all" | "folder", folder: string, returnFocus?: BookmarkMenuReturnFocus): void => {
    if (returnFocus) bookmarkMenuReturnFocus = returnFocus;
    // A pending hover-close belongs to the menu that was open before this
    // switch; it must not close the freshly opened one.
    cancelBookmarkHoverClose();
    state.bookmarksMenu = kind;
    state.bookmarksMenuFolder = folder;
    paint();
    // Focus a real menu entry, not just URL rows: an overflow menu that only
    // contains folder rows must still receive keyboard focus so Escape works.
    const menu = container.querySelector<HTMLElement>("#desktop-browser-bookmarks-menu");
    const first = menu?.querySelector<HTMLElement>("[data-browser-bookmark-menu-folder], [data-browser-bookmark-index]")
      || menu?.querySelector<HTMLElement>("#desktop-browser-bookmarks-menu-close");
    first?.focus({preventScroll: true});
  };

  const closeBookmarksMenu = (): void => {
    cancelBookmarkHoverClose();
    if (!state.bookmarksMenu) return;
    const returnFocus = bookmarkMenuReturnFocus;
    bookmarkMenuReturnFocus = null;
    state.bookmarksMenu = "";
    state.bookmarksMenuFolder = "";
    paint();
    // A fresh paint hides the chevron until the overflow pass runs again.
    measureBookmarkOverflow();
    const folderChip = returnFocus?.kind === "folder" ? visibleFolderChip(returnFocus.folder) : null;
    const visibleFolder = [...container.querySelectorAll<HTMLElement>("[data-browser-bookmark-folder]")]
      .find(chip => !chip.hidden);
    const target = returnFocus?.kind === "more"
      ? container.querySelector<HTMLElement>("#desktop-browser-bookmarks-more")
      : folderChip || container.querySelector<HTMLElement>("#desktop-browser-bookmarks-more:not([hidden])") || visibleFolder;
    target?.focus({preventScroll: true});
  };

  // A folder menu stays open while the pointer is over the chip that opened it
  // or over the popover itself. Leaving both closes it, so the Owner does not
  // have to hunt for the close button after looking through a folder.
  const bookmarkMenuZoneRects = (): DOMRect[] => {
    if (state.bookmarksMenu !== "folder") return [];
    const rects: DOMRect[] = [];
    const menu = container.querySelector<HTMLElement>("#desktop-browser-bookmarks-menu");
    if (menu) rects.push(menu.getBoundingClientRect());
    const anchor = visibleFolderChip(state.bookmarksMenuFolder)
      || container.querySelector<HTMLElement>("#desktop-browser-bookmarks-more:not([hidden])");
    if (anchor) rects.push(anchor.getBoundingClientRect());
    return rects;
  };

  const insideBookmarkMenuZone = (event: PointerEvent): boolean => {
    const target = event.target instanceof Element ? event.target : null;
    if (target?.closest("#desktop-browser-bookmarks-menu")) return true;
    const chip = visibleFolderChip(state.bookmarksMenuFolder);
    if (chip && target && chip.contains(target)) return true;
    if (!chip && target?.closest("#desktop-browser-bookmarks-more")) return true;
    return bookmarkMenuZoneRects().some(rect => event.clientX >= rect.left - BOOKMARK_MENU_HOVER_PAD
      && event.clientX <= rect.right + BOOKMARK_MENU_HOVER_PAD
      && event.clientY >= rect.top - BOOKMARK_MENU_HOVER_PAD
      && event.clientY <= rect.bottom + BOOKMARK_MENU_HOVER_PAD);
  };

  const onBookmarkPointerMove = (event: PointerEvent): void => {
    if (state.bookmarksMenu !== "folder") return;
    if (insideBookmarkMenuZone(event)) {
      cancelBookmarkHoverClose();
      return;
    }
    // Schedule once: a pointer that keeps travelling outside must still close
    // the menu after the grace period instead of postponing it forever.
    if (bookmarkHoverTimer) return;
    bookmarkHoverTimer = window.setTimeout(() => {
      bookmarkHoverTimer = 0;
      if (state.bookmarksMenu === "folder") closeBookmarksMenu();
    }, BOOKMARK_MENU_HOVER_CLOSE_MS);
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
      });
      state.inUseProfileID = "";
      state.grant = result.grant;
      state.profileID = result.grant.profile_id;
      state.startPageGrantID = "";
      await load();
      notify("notice", result.session
        ? `已开启浏览器共享（会话 ${stateLabel(result.session.state)}）`
        : "已开启该 Profile 的浏览器共享");
      onGranted?.();
    } catch (error) {
      const details = errorDetails(error);
      state.error = details.message;
      if (details.code === "browser_profile_in_use") {
        state.inUseProfileID = state.profileID;
        notify("notice", "该浏览器正由其他 Task 使用；请先在对应 Task 关闭共享。");
      } else if (details.code === "browser_task_profile_limit") {
        notify("notice", "当前 Task 正在共享另一个浏览器 Profile；请先关闭该共享。");
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
      // Drop the bitmap as well as the timer: an AHA Web tab hidden behind the
      // real browser window must never come back showing a stale screenshot.
      stopStream();
      invalidateStateSync();
      // Pending input belongs to a view the Owner can no longer see.
      resetControlInput();
      controlFocusWanted = false;
      paintFrameStatus();
      return;
    }
    // Coming back must both re-read the tab strip (the Owner may have opened,
    // switched or closed pages meanwhile) and restart the frame stream. A
    // terminal 403/409 answer survives the pause: syncState() only restarts on
    // a new selection/lease, and this fallback must not clear its suppression.
    void syncState().finally(() => {
      if (!disposed && !state.loading && !frameTimer && !frameAbort && terminalStreamKey !== streamKey()) startStream();
    });
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
    cancelBookmarkHoverClose();
    state.bookmarksMenu = "";
    state.bookmarksMenuFolder = "";
    bookmarkMenuReturnFocus = null;
    container.querySelector("#desktop-browser-bookmarks-menu")?.remove();
    container.querySelector("#desktop-browser-bookmarks-more")?.setAttribute("aria-expanded", "false");
    // The menu was removed outside paint(); drop the cached HTML so reopening
    // the same folder menu is not mistaken for an identical (already painted)
    // tree and skipped.
    painted.delete(container);
  };

  paint();
  void load();
  scheduleSync();
  document.addEventListener("visibilitychange", onVisibility);
  document.addEventListener("pointerdown", onOutsidePointer, true);
  document.addEventListener("pointermove", onBookmarkPointerMove, {passive: true});
  container.addEventListener("keydown", onKeydown);
  const observer = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(relayoutBookmarks);
  observer?.observe(container);
  window.addEventListener("resize", relayoutBookmarks);

  return {
    dispose: () => {
      disposed = true;
      bookmarkRequest++;
      state.controlMode = false;
      resetControlInput();
      window.clearTimeout(syncTimer);
      syncTimer = 0;
      // An accepted stop outlives the panel: dispose must not cancel the
      // pending release timer, or switching control mode mid-open would keep
      // the exclusive lease alive with no UI to release it.
      document.removeEventListener("visibilitychange", onVisibility);
      document.removeEventListener("pointerdown", onOutsidePointer, true);
      document.removeEventListener("pointermove", onBookmarkPointerMove);
      cancelBookmarkHoverClose();
      container.removeEventListener("keydown", onKeydown);
      window.removeEventListener("resize", relayoutBookmarks);
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
