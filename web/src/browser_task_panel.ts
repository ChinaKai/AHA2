// Lazy chunk for the Task shared-control "内置浏览器" mode. Keeping the
// profile picker, attach flow, authorized tab list and read-only frame preview
// here avoids growing the first-screen bundle for a panel most Tasks never open.
import {api} from "./api.js";
import {icon} from "./icons.js";
import type {BrowserBookmark, BrowserHostView, BrowserProfile, BrowserSession, BrowserTab, BrowserTaskGrant} from "./types.js";

type Notice = (type: "error" | "notice", message: string) => void;

export interface BrowserTaskPanelOptions {
  container: HTMLElement;
  taskID: string;
  notify: Notice;
  onChange: () => void;
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
  bookmarksOpen: boolean;
  bookmarksLoading: boolean;
  inUseProfileID: string;
  urlDraft: string;
  error: string;
  frameError: string;
  frameLive: boolean;
}

// 300ms keeps the preview at ~3.3 fps; an unchanged page backs off to ~0.8 fps
// instead of re-encoding identical JPEGs forever.
export const BROWSER_FRAME_MIN_DELAY_MS = 300;
export const BROWSER_FRAME_MAX_DELAY_MS = 1200;
const httpStatusForbidden = 403;
const httpStatusNotFound = 404;
const httpStatusConflict = 409;

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

export function mountBrowserTaskPanel(options: BrowserTaskPanelOptions): () => void {
  const {container, taskID, notify, onChange} = options;
  const state: PanelState = {
    profiles: [], host: null, grant: null, tabs: [], bookmarks: [], profileID: "", selectedTabID: "",
    loading: true, binding: false, bookmarksOpen: false, bookmarksLoading: false, inUseProfileID: "",
    urlDraft: "", error: "", frameError: "", frameLive: false,
  };

  let disposed = false;
  let frameTimer = 0;
  let frameAbort: AbortController | null = null;
  let frameURL = "";
  let frameSignature = "";
  let frameDelay = BROWSER_FRAME_MIN_DELAY_MS;
  let streamGeneration = 0;

  const current = (): boolean => container.isConnected && Boolean(container.closest("#desktop-tool"));
  const selectedTab = (): BrowserTab | null =>
    state.tabs.find(tab => tab.id === state.selectedTabID) || null;

  const frameStatusText = (): string => {
    if (!state.grant) return "先选择 Profile 并获取独占控制，之后可查看并操作全部页面";
    if (state.binding) return "正在处理浏览器操作…";
    if (state.host && !state.host.available) return "浏览器宿主不可用，无法获取画面";
    if (state.frameError) return `画面暂不可用：${state.frameError}`;
    if (!state.tabs.length) {
      return "当前浏览器窗口还没有网页；可以在这里打开页面，或从收藏夹打开。";
    }
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
    container.dataset.status = status;
    container.dataset.profile = profileName;
    const surfaceStatus = document.querySelector<HTMLElement>("#desktop-browser-surface-status");
    if (surfaceStatus) surfaceStatus.textContent = status;
    onChange();
  };

  const paint = (): void => {
    if (!current()) return;
    const session = state.grant?.session || state.profiles.find(item => item.id === state.profileID)?.session || null;
    const selectedProfile = state.profiles.find(item => item.id === state.profileID);
    const tabs = state.tabs.length
      ? state.tabs.map(tab => {
        const selected = tab.id === state.selectedTabID;
        const host = tabHost(tab.url);
        // Only the page currently shown in the preview is "当前". The host's
        // `active` flag means "visible in its window", and a Profile with two
        // browser windows can legitimately report several at once, so it must
        // never produce several "当前" labels.
        return `<li class="desktop-browser-tab-item">
          <button type="button" class="desktop-browser-tab ${selected ? "active" : ""}" data-browser-tab="${escapeHTML(tab.id)}" aria-pressed="${selected}" title="${escapeHTML(tab.url)}">
            <span class="desktop-browser-tab-title">${escapeHTML(tab.title || host || "未命名页面")}</span>
            <span class="desktop-browser-tab-meta">
              <span class="desktop-browser-tab-host">${escapeHTML(host)}</span>
              <span class="desktop-browser-tab-origin" title="页面来源">${tab.origin === "owner" ? "Owner" : "Agent"}</span>
              ${selected ? '<span class="desktop-browser-tab-current">当前</span>' : ""}
            </span>
          </button>
          <button type="button" class="icon-button desktop-browser-tab-close" data-browser-tab-close="${escapeHTML(tab.id)}" title="关闭页面" aria-label="关闭页面 ${escapeHTML(tab.title || host)}">${icon("close")}</button>
        </li>`;
      }).join("")
      : `<li class="desktop-empty desktop-browser-tabs-empty">${state.grant
        ? "打开页面后会显示在这里。"
        : "选择 Profile 并获取独占控制后，这里会列出浏览器当前的全部页面。"}</li>`;
    const busy = state.loading || state.binding;
    const canUseGrant = Boolean(state.grant) && state.host?.available !== false;
    const canTransfer = !state.grant && Boolean(state.inUseProfileID) && state.inUseProfileID === state.profileID;
    const noProfiles = !state.profiles.length;
    const hostDown = Boolean(state.host && !state.host.available);
    const bookmarks = `<section id="desktop-browser-bookmarks" class="desktop-browser-bookmarks" role="dialog" aria-modal="false" aria-labelledby="desktop-browser-bookmarks-title" ${state.bookmarksOpen ? "" : "hidden"}>
        <div class="desktop-browser-bookmarks-head">
          <strong id="desktop-browser-bookmarks-title">收藏夹</strong>
          <button type="button" id="desktop-browser-bookmarks-close" class="icon-button" title="收起收藏夹" aria-label="收起收藏夹">${icon("close")}</button>
        </div>
        ${state.bookmarksLoading
          ? '<p class="desktop-empty" role="status">正在读取收藏夹…</p>'
          : state.bookmarks.length
            ? `<ul>${state.bookmarks.map(bookmark => `<li><button type="button" data-browser-bookmark="${escapeHTML(bookmark.url)}" title="${escapeHTML(bookmark.url)}"><span>${escapeHTML(bookmark.title || bookmark.url)}</span><small>${bookmark.folder ? `${escapeHTML(bookmark.folder)} · ` : ""}${escapeHTML(tabHost(bookmark.url))}</small></button></li>`).join("")}</ul>`
            : '<p class="desktop-empty">收藏夹为空或暂时读不到。</p>'}
      </section>`;
    // Without a Profile or a reachable host there is nothing to preview; show
    // one deliberate empty state with its own action instead of a disabled
    // form and a large blank panel.
    const blocked = noProfiles || hostDown;
    // One compact control row: the exclusive control button and the bookmark
    // drawer toggle are mutually exclusive with the "获取独占控制" action.
    const controlButton = state.grant
      ? `<button type="button" id="desktop-browser-release" class="desktop-browser-control" ${busy ? "disabled" : ""}>释放控制</button>
        ${blocked ? "" : `<button type="button" id="desktop-browser-bookmarks-toggle" class="icon-button" title="收藏夹" aria-label="收藏夹" aria-expanded="${state.bookmarksOpen}" aria-controls="desktop-browser-bookmarks" ${busy ? "disabled" : ""}>${icon("browser")}</button>`}`
      : canTransfer
        ? `<button type="button" id="desktop-browser-transfer" class="primary desktop-browser-control" ${busy ? "disabled" : ""}>转移控制权</button>`
        : `<button type="button" id="desktop-browser-attach" class="primary desktop-browser-control" ${busy || noProfiles || hostDown ? "disabled" : ""}>${icon("link")}<span>获取独占控制</span></button>`;
    const emptyState = noProfiles
      ? `<div class="desktop-browser-empty-state" role="note">
          ${icon("browser")}
          <strong>创建一个浏览器 Profile</strong>
          <p>创建后即可选择 Profile、获取独占控制并查看实时画面。</p>
          <a class="desktop-browser-cta" href="#resources?tab=browser">前往资源页创建 Profile</a>
        </div>`
      : `<div class="desktop-browser-empty-state" role="note">
          ${icon("browser")}
          <strong>重新连接浏览器宿主</strong>
          <p>确认 AHA2 的浏览器宿主进程正在运行，然后重新检测。</p>
          <button type="button" id="desktop-browser-retry" class="desktop-browser-cta">重新检测浏览器宿主</button>
        </div>`;
    container.innerHTML = `
      <div class="desktop-browser-toolbar" role="group" aria-label="内置浏览器控制">
        <label class="desktop-window-label desktop-browser-profile-label"><span>Profile</span><select id="desktop-browser-profile" aria-label="浏览器 Profile">${profileOptions(state.profiles, state.profileID)}</select></label>
        <button type="button" id="desktop-browser-refresh" class="icon-button" title="刷新浏览器状态" aria-label="刷新浏览器状态" ${busy ? "disabled" : ""}>${icon("refresh")}</button>
        ${controlButton}
      </div>
      <p id="desktop-browser-status" class="desktop-browser-status" data-tone="${statusTone(state, session)}" role="status">${escapeHTML(statusText(state, session))}</p>
      ${blocked ? emptyState : `
      <form id="desktop-browser-open-form" class="desktop-browser-open">
        <input id="desktop-browser-url" type="url" inputmode="url" placeholder="https://example.com" aria-label="要打开的网址" value="${escapeHTML(state.urlDraft)}" ${busy || !canUseGrant ? "disabled" : ""}>
        <button type="submit" ${busy || !canUseGrant ? "disabled" : ""}>打开页面</button>
      </form>
      <figure class="desktop-browser-preview">
        <img id="desktop-browser-frame" alt="内置浏览器只读画面" draggable="false" hidden>
        <p id="desktop-browser-frame-status" class="desktop-empty" role="status">${escapeHTML(frameStatusText())}</p>
      </figure>
      <ul id="desktop-browser-tabs" class="desktop-browser-tabs" aria-label="浏览器页面">${tabs}</ul>
      <div class="desktop-browser-bookmarks-anchor">${bookmarks}</div>`}`;
    container.querySelector("#desktop-browser-profile")?.addEventListener("change", event => {
      state.profileID = (event.target as HTMLSelectElement).value;
    });
    container.querySelector("#desktop-browser-refresh")?.addEventListener("click", () => {
      if (!state.loading) void reload();
    });
    container.querySelector("#desktop-browser-retry")?.addEventListener("click", () => {
      if (!state.loading) void reload();
    });
    container.querySelector("#desktop-browser-attach")?.addEventListener("click", () => {
      if (!state.loading) void attach();
    });
    container.querySelector("#desktop-browser-release")?.addEventListener("click", () => {
      if (!state.binding) void releaseControl();
    });
    container.querySelector("#desktop-browser-bookmarks-toggle")?.addEventListener("click", () => {
      if (!state.binding) void toggleBookmarks();
    });
    container.querySelector("#desktop-browser-bookmarks-close")?.addEventListener("click", () => {
      closeBookmarks();
    });
    container.querySelector("#desktop-browser-transfer")?.addEventListener("click", () => {
      if (!state.binding) void transferControl();
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
        if (!state.binding && target) void openPage(target);
      });
    });
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

  const load = async (): Promise<void> => {
    state.loading = true;
    state.error = "";
    paint();
    try {
      const result = await api.taskBrowser(taskID);
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
        state.bookmarksOpen = false;
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
    } catch (error) {
      state.error = error instanceof Error ? error.message : String(error);
    } finally {
      state.loading = false;
    }
    paint();
    startStream();
  };

  const reload = async (): Promise<void> => {
    await load();
  };

  const errorDetails = (error: unknown): {message: string; code: string} => ({
    message: error instanceof Error ? error.message : String(error),
    code: String((error as {code?: unknown})?.code || ""),
  });

  const selectTab = (tabID: string): void => {
    if (!tabID) return;
    state.selectedTabID = tabID;
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

  const openPage = async (targetOverride?: string): Promise<void> => {
    if (!state.grant) return;
    const target = (targetOverride ?? state.urlDraft).trim();
    if (!target) {
      state.error = "请填写要打开的网址";
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
      selectTab(result.tab.id);
      paint();
      startStream();
      notify("notice", "页面已在内置浏览器中打开");
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

  const releaseControl = async (): Promise<void> => {
    if (!state.grant) return;
    state.binding = true;
    state.error = "";
    paint();
    try {
      await api.releaseTaskBrowserGrant(state.grant.id);
      state.grant = null;
      state.tabs = [];
      state.bookmarks = [];
      state.bookmarksOpen = false;
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

  const focusBookmarks = (): void => {
    const target = container.querySelector<HTMLElement>("#desktop-browser-bookmarks [data-browser-bookmark]")
      || container.querySelector<HTMLElement>("#desktop-browser-bookmarks-close");
    // Focusing must not scroll the popover: that would move the preview out
    // from under the Owner's cursor when the drawer opens.
    target?.focus({preventScroll: true});
  };

  const closeBookmarks = (): void => {
    if (!state.bookmarksOpen) return;
    state.bookmarksOpen = false;
    paint();
    container.querySelector<HTMLElement>("#desktop-browser-bookmarks-toggle")?.focus();
  };

  const toggleBookmarks = async (): Promise<void> => {
    if (!state.grant) return;
    if (state.bookmarksOpen) {
      closeBookmarks();
      return;
    }
    state.bookmarksOpen = true;
    state.bookmarksLoading = true;
    state.error = "";
    paint();
    focusBookmarks();
    try {
      const result = await api.taskBrowserBookmarks(state.grant.id);
      state.bookmarks = result.bookmarks || [];
    } catch (error) {
      const details = errorDetails(error);
      state.bookmarks = [];
      state.error = details.message;
      notify("error", details.message);
    } finally {
      state.bookmarksLoading = false;
    }
    paint();
    if (state.bookmarksOpen) focusBookmarks();
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
        capabilities: ["open_tab", "navigate", "snapshot"],
        expires_in_seconds: 1800,
      });
      state.inUseProfileID = "";
      state.grant = result.grant;
      state.profileID = result.grant.profile_id;
      await load();
      notify("notice", result.session
        ? `已获取独占控制（会话 ${stateLabel(result.session.state)}）`
        : "已获取该 Profile 的独占控制");
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

  // The bookmark drawer is non-modal, so Escape is scoped to the panel and
  // returns focus to the toolbar toggle.
  const onKeydown = (event: KeyboardEvent): void => {
    if (event.key !== "Escape" || !state.bookmarksOpen) return;
    event.stopPropagation();
    closeBookmarks();
  };

  paint();
  void load();
  document.addEventListener("visibilitychange", onVisibility);
  container.addEventListener("keydown", onKeydown);

  return () => {
    disposed = true;
    document.removeEventListener("visibilitychange", onVisibility);
    container.removeEventListener("keydown", onKeydown);
    stopStream();
  };
}
