// Lazy chunk for the Task shared-control "内置浏览器" mode. Keeping the
// profile picker, attach flow and tab list here avoids growing the first-screen
// bundle for a panel most Tasks never open.
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
  loading: boolean;
  error: string;
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
    return `浏览器宿主不可用：${state.host.error || state.host.code || "请启动 host 进程"}`;
  }
  if (state.host && !state.host.edge_available) return "浏览器宿主在线，但未找到 Microsoft Edge";
  if (!state.profiles.length) return "还没有浏览器 Profile，请先到资源页创建";
  if (!session) return "尚未附加会话；启动/附加后 Owner 可在宿主可见窗口中登录";
  const reachable = session.host_reachable === false ? " · 宿主不可达" : "";
  const error = session.error ? `：${session.error}` : "";
  return `会话 ${stateLabel(session.state)}${reachable}${error}`;
}

export function mountBrowserTaskPanel(options: BrowserTaskPanelOptions): void {
  const {container, taskID, notify, onChange} = options;
  const state: PanelState = {
    profiles: [], host: null, grant: null, tabs: [], profileID: "", loading: true, error: "",
  };

  const current = (): boolean => container.isConnected && Boolean(container.closest("#desktop-tool"));

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
      ? state.tabs.map(tab => `<li><span>${escapeHTML(tab.title || "未命名页面")}</span><small>${escapeHTML(tab.url)}</small></li>`).join("")
      : `<li class="desktop-empty">这个 Task 还没有自己的标签页</li>`;
    container.innerHTML = `
      <label class="desktop-window-label">浏览器 Profile<select id="desktop-browser-profile" aria-label="浏览器 Profile">${profileOptions(state.profiles, state.profileID)}</select></label>
      <div class="desktop-target-actions">
        <button type="button" id="desktop-browser-refresh" class="icon-button" title="刷新浏览器状态" aria-label="刷新浏览器状态" ${state.loading ? "disabled" : ""}>${icon("refresh")}</button>
        <button type="button" id="desktop-browser-attach" class="primary" ${state.loading || !state.profiles.length ? "disabled" : ""}>${icon("link")}<span>启动 / 附加会话</span></button>
      </div>
      <p id="desktop-browser-status" class="desktop-empty" role="status">${escapeHTML(statusText(state, session))}</p>
      <ul id="desktop-browser-tabs" class="desktop-browser-tabs" aria-label="Task 标签页">${tabs}</ul>`;
    container.querySelector("#desktop-browser-profile")?.addEventListener("change", event => {
      state.profileID = (event.target as HTMLSelectElement).value;
    });
    container.querySelector("#desktop-browser-refresh")?.addEventListener("click", () => {
      if (!state.loading) void reload();
    });
    container.querySelector("#desktop-browser-attach")?.addEventListener("click", () => {
      if (!state.loading) void attach();
    });
    syncShell(session, selectedProfile?.name || "");
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
      state.tabs = data.tabs || [];
      if (!state.profiles.some(item => item.id === state.profileID)) {
        state.profileID = active?.profile_id || state.profiles[0]?.id || "";
      }
    } catch (error) {
      state.error = error instanceof Error ? error.message : String(error);
    } finally {
      state.loading = false;
    }
    paint();
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
      state.tabs = result.grant.tabs || [];
      state.profileID = result.grant.profile_id;
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
  };

  paint();
  void load();
}
