// Lazy chunk for the Task shared-control "内置浏览器" mode. Keeping the
// profile picker, attach flow, authorized tab list and read-only frame preview
// here avoids growing the first-screen bundle for a panel most Tasks never open.
import {api} from "./api.js";
import {icon} from "./icons.js";
import type {BrowserHostView, BrowserProfile, BrowserSession, BrowserTab, BrowserTaskGrant} from "./types.js";

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
  profileID: string;
  selectedTabID: string;
  loading: boolean;
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
  if (!session) return "尚未附加会话；启动/附加后 Owner 可在宿主可见窗口中登录";
  const reachable = session.host_reachable === false ? " · 宿主不可达" : "";
  const error = session.error ? `：${session.error}` : "";
  return `会话 ${stateLabel(session.state)}${reachable}${error}`;
}

export function mountBrowserTaskPanel(options: BrowserTaskPanelOptions): () => void {
  const {container, taskID, notify, onChange} = options;
  const state: PanelState = {
    profiles: [], host: null, grant: null, tabs: [], profileID: "", selectedTabID: "",
    loading: true, error: "", frameError: "", frameLive: false,
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
    if (!state.grant) return "附加会话后可查看授权标签页的实时画面";
    if (state.host && !state.host.available) return "浏览器宿主不可用，无法获取画面";
    if (state.frameError) return `画面暂不可用：${state.frameError}`;
    if (!state.tabs.length) return "这个 Task 还没有自己的标签页";
    if (!selectedTab()) return "选择标签页后显示画面";
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
      ? state.tabs.map(tab => `<li><button type="button" class="desktop-browser-tab ${tab.id === state.selectedTabID ? "active" : ""}" data-browser-tab="${escapeHTML(tab.id)}" aria-pressed="${tab.id === state.selectedTabID}"><span>${escapeHTML(tab.title || "未命名页面")}</span><small>${escapeHTML(tab.url)}</small></button></li>`).join("")
      : `<li class="desktop-empty">这个 Task 还没有自己的标签页</li>`;
    container.innerHTML = `
      <label class="desktop-window-label">浏览器 Profile<select id="desktop-browser-profile" aria-label="浏览器 Profile">${profileOptions(state.profiles, state.profileID)}</select></label>
      <div class="desktop-target-actions">
        <button type="button" id="desktop-browser-refresh" class="icon-button" title="刷新浏览器状态" aria-label="刷新浏览器状态" ${state.loading ? "disabled" : ""}>${icon("refresh")}</button>
        <button type="button" id="desktop-browser-attach" class="primary" ${state.loading || !state.profiles.length ? "disabled" : ""}>${icon("link")}<span>启动 / 附加会话</span></button>
      </div>
      <p id="desktop-browser-status" class="desktop-empty" role="status">${escapeHTML(statusText(state, session))}</p>
      <ul id="desktop-browser-tabs" class="desktop-browser-tabs" aria-label="Task 标签页">${tabs}</ul>
      <figure class="desktop-browser-preview">
        <img id="desktop-browser-frame" alt="内置浏览器只读画面" draggable="false" hidden>
        <p id="desktop-browser-frame-status" class="desktop-empty" role="status">${escapeHTML(frameStatusText())}</p>
      </figure>`;
    container.querySelector("#desktop-browser-profile")?.addEventListener("change", event => {
      state.profileID = (event.target as HTMLSelectElement).value;
    });
    container.querySelector("#desktop-browser-refresh")?.addEventListener("click", () => {
      if (!state.loading) void reload();
    });
    container.querySelector("#desktop-browser-attach")?.addEventListener("click", () => {
      if (!state.loading) void attach();
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
    frameAbort = new AbortController();
    const generation = streamGeneration;
    try {
      const blob = await api.browserTabFrame(grant.id, tab.id, frameAbort.signal);
      if (disposed) return;
      const buffer = new Uint8Array(await blob.arrayBuffer());
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
      frameAbort = null;
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
      state.grant = active;
      // The Task view returns every tab this Task owns. The preview only asks
      // for tabs under the active grant; anything else would be rejected by
      // the server's tab-ownership check.
      state.tabs = active ? (data.tabs || []).filter(tab => tab.grant_id === active.id) : (data.tabs || []);
      if (!state.profiles.some(item => item.id === state.profileID)) {
        state.profileID = active?.profile_id || state.profiles[0]?.id || "";
      }
      if (!state.tabs.some(tab => tab.id === state.selectedTabID)) {
        state.selectedTabID = state.tabs[0]?.id || "";
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
      state.grant = result.grant;
      state.tabs = (result.grant.tabs || []).filter(tab => tab.grant_id === result.grant.id);
      state.profileID = result.grant.profile_id;
      state.selectedTabID = state.tabs[0]?.id || "";
      notify("notice", result.session
        ? `已附加浏览器会话（${stateLabel(result.session.state)}）`
        : "已附加浏览器 Profile");
    } catch (error) {
      state.error = error instanceof Error ? error.message : String(error);
      notify("error", state.error);
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

  paint();
  void load();
  document.addEventListener("visibilitychange", onVisibility);

  return () => {
    disposed = true;
    document.removeEventListener("visibilitychange", onVisibility);
    stopStream();
  };
}
