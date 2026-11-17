import {api} from "./api.js";
import {icon} from "./icons.js";
import type {BrowserHostView, BrowserProfile, BrowserSession} from "./types.js";

type Notice = (type: "error" | "notice", message: string) => void;

function escapeHTML(value: unknown): string {
  return String(value ?? "")
    .replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;").replaceAll("'", "&#039;");
}

export interface BrowserResourcesData {
  profiles: BrowserProfile[];
  host: BrowserHostView | null;
  loading: boolean;
  error: string;
}

export interface BrowserResourceActions {
  reload: () => Promise<void>;
  notify: Notice;
}

export function browserProfileStateLabel(state: string): string {
  return {
    idle: "未初始化",
    starting: "启动中",
    ready: "可用",
    auth_required: "等待登录",
    busy: "使用中",
    error: "异常",
  }[state] || state;
}

export function browserSessionStateLabel(state: string): string {
  return {
    starting: "启动中",
    ready: "可用",
    auth_required: "等待登录",
    busy: "使用中",
    closed: "已关闭",
    error: "异常",
  }[state] || state;
}

function effectiveState(profile: BrowserProfile): string {
  const session = profile.session;
  if (!session || session.state === "closed") return profile.state === "idle" ? "idle" : profile.state;
  return session.state;
}

function stateClass(state: string): string {
  if (state === "ready") return "ready";
  if (state === "auth_required") return "attention";
  if (state === "error") return "error";
  if (state === "starting" || state === "busy") return "busy";
  return "idle";
}

function lastUsedLabel(profile: BrowserProfile): string {
  const value = profile.last_used_at || profile.session?.last_used_at;
  if (!value) return "尚未使用";
  const date = new Date(value);
  if (Number.isNaN(date.getTime()) || date.getFullYear() < 2000) return "尚未使用";
  return `最近使用 ${date.toLocaleString()}`;
}

export function renderBrowserHostBanner(host: BrowserHostView | null): string {
  if (!host) {
    return `<p class="browser-host-banner error" role="status">${icon("browser")}尚未读取浏览器宿主状态。</p>`;
  }
  if (!host.available) {
    return `<p class="browser-host-banner error" role="status">${icon("browser")}浏览器宿主不可用：${escapeHTML(host.error || host.code || "请启动 host 进程")}。Profile 可以创建，但打开登录窗口会明确失败，不会换用其他浏览器。</p>`;
  }
  if (!host.edge_available) {
    return `<p class="browser-host-banner error" role="status">${icon("browser")}浏览器宿主在线，但未找到 Microsoft Edge。POC 只支持 Edge，不会静默换用 Chrome/Chromium。</p>`;
  }
  return `<p class="browser-host-banner ready" role="status">${icon("browser")}宿主 ${escapeHTML(host.host_id)} · Edge 可用${host.version ? ` · v${escapeHTML(host.version)}` : ""}${host.platform ? ` · ${escapeHTML(host.platform)}` : ""}</p>`;
}

export function renderBrowserResources(data: BrowserResourcesData): string {
  const rows = data.profiles.map(profile => {
    const state = effectiveState(profile);
    const active = Boolean(profile.session && profile.session.state !== "closed");
    const action = active
      ? `<button type="button" class="icon-button" data-browser-profile-close="${escapeHTML(profile.id)}" title="关闭会话" aria-label="关闭会话">${icon("logout")}</button>`
      : `<button type="button" class="icon-button" data-browser-profile-login="${escapeHTML(profile.id)}" title="打开登录窗口" aria-label="打开登录窗口">${icon("link")}</button>`;
    const sessionNote = profile.session && active
      ? `<span>会话 ${escapeHTML(browserSessionStateLabel(profile.session.state))}</span>`
      : "";
    return `<article class="hardware-resource-row browser-resource-row" data-browser-profile="${escapeHTML(profile.id)}">
    <div class="hardware-resource-identity"><span class="hardware-resource-glyph">${icon("browser")}</span><div><strong>${escapeHTML(profile.name || profile.id)}</strong><small>${profile.browser_kind === "edge" ? "Microsoft Edge" : escapeHTML(profile.browser_kind)} · 宿主 ${escapeHTML(profile.host_id)}</small></div></div>
    <div class="hardware-resource-meta"><span class="browser-state ${stateClass(state)}">${escapeHTML(browserProfileStateLabel(state))}</span>${sessionNote}<span>${escapeHTML(lastUsedLabel(profile))}</span></div>
    <div class="hardware-resource-actions">${action}<button type="button" class="icon-button" data-browser-profile-rename="${escapeHTML(profile.id)}" title="重命名" aria-label="重命名">${icon("edit")}</button><button type="button" class="icon-button danger" data-browser-profile-delete="${escapeHTML(profile.id)}" title="删除 Profile" aria-label="删除 Profile">${icon("close")}</button></div>
  </article>`;
  }).join("");
  const body = data.profiles.length
    ? `<div class="hardware-resource-list">${rows}</div>`
    : `<div class="empty-state hardware-resource-empty">${icon("browser")}<strong>还没有浏览器 Profile</strong><span>创建后打开可见登录窗口，登录一次即可被 Task 复用。</span><button type="button" class="primary" data-browser-profile-add-empty>${icon("plus")}新建 Profile</button></div>`;
  return `<section class="hardware-resources-view browser-resources-view">
    <div class="resource-panel-toolbar"><div><strong>浏览器</strong><p>Profile 保存网站登录态，真实用户数据目录由浏览器宿主管理；AHA 和 Web 只接触逻辑标识。</p></div><div class="browser-resource-toolbar-actions"><button type="button" id="refresh-browser-profiles" class="icon-button" title="刷新状态" aria-label="刷新状态">${icon("refresh")}</button><button type="button" id="add-browser-profile" class="primary">${icon("plus")}新建 Profile</button></div></div>
    ${renderBrowserHostBanner(data.host)}
    ${data.error ? `<p class="browser-resource-error" role="alert">${escapeHTML(data.error)}</p>` : ""}
    ${body}
  </section>`;
}

function profileDialog(profile?: BrowserProfile): HTMLDialogElement {
  const dialog = document.createElement("dialog");
  dialog.className = "browser-profile-dialog";
  dialog.innerHTML = `<form id="browser-profile-form">
    <div class="dialog-head"><h2>${profile ? "重命名浏览器 Profile" : "新建浏览器 Profile"}</h2><button type="button" data-close class="icon-button" aria-label="关闭">${icon("close")}</button></div>
    <label>名称<input name="name" maxlength="120" required value="${escapeHTML(profile?.name || "")}" placeholder="例如：工作浏览器"></label>
    ${profile ? "" : `<label>浏览器<select name="browser_kind"><option value="edge" selected>Microsoft Edge（POC 仅支持）</option></select></label><p class="field-help">登录窗口会使用本机已安装的 Edge。启动失败会明确报错，不会换用 Chrome/Chromium。真实 profile 目录由浏览器宿主保存，不会显示在 Web 中。</p>`}
    <div class="dialog-actions"><button type="button" data-close>取消</button><button type="submit" class="primary">${profile ? "保存名称" : "创建 Profile"}</button></div>
  </form>`;
  return dialog;
}

export function bindBrowserResources(data: BrowserResourcesData, actions: BrowserResourceActions): void {
  const openDialog = (profile?: BrowserProfile): void => {
    const dialog = profileDialog(profile);
    document.body.appendChild(dialog);
    dialog.addEventListener("close", () => dialog.remove());
    dialog.querySelectorAll("[data-close]").forEach(button => button.addEventListener("click", () => dialog.close()));
    dialog.querySelector("form")?.addEventListener("submit", event => {
      event.preventDefault();
      const name = (new FormData(dialog.querySelector("form") as HTMLFormElement).get("name") || "").toString().trim();
      if (!name) return;
      const run = profile
        ? api.renameBrowserProfile(profile.id, name)
        : api.createBrowserProfile({name, browser_kind: "edge"});
      void run.then(async () => {
        dialog.close();
        await actions.reload();
        actions.notify("notice", profile ? "Profile 已重命名" : "Profile 已创建，可打开登录窗口");
      }).catch(error => actions.notify("error", error instanceof Error ? error.message : String(error)));
    });
    dialog.showModal();
    dialog.querySelector<HTMLInputElement>('input[name="name"]')?.focus();
  };

  document.querySelector("#add-browser-profile")?.addEventListener("click", () => openDialog());
  document.querySelector("[data-browser-profile-add-empty]")?.addEventListener("click", () => openDialog());
  document.querySelector("#refresh-browser-profiles")?.addEventListener("click", () => {
    void actions.reload().catch(error => actions.notify("error", error instanceof Error ? error.message : String(error)));
  });
  document.querySelectorAll<HTMLElement>("[data-browser-profile-rename]").forEach(button => button.addEventListener("click", () => {
    const profile = data.profiles.find(item => item.id === button.dataset.browserProfileRename);
    if (profile) openDialog(profile);
  }));
  document.querySelectorAll<HTMLElement>("[data-browser-profile-login]").forEach(button => button.addEventListener("click", () => {
    const profile = data.profiles.find(item => item.id === button.dataset.browserProfileLogin);
    if (!profile) return;
    void api.openBrowserLoginWindow(profile.id).then(async result => {
      await actions.reload();
      actions.notify("notice", result.reused
        ? "已复用该 Profile 的可见浏览器窗口"
        : "登录窗口已打开；请在宿主窗口完成登录，正常关闭后状态会自动刷新");
    }).catch(error => actions.notify("error", error instanceof Error ? error.message : String(error)));
  }));
  document.querySelectorAll<HTMLElement>("[data-browser-profile-close]").forEach(button => button.addEventListener("click", () => {
    const profile = data.profiles.find(item => item.id === button.dataset.browserProfileClose);
    if (!profile) return;
    if (!window.confirm(`关闭浏览器 Profile“${profile.name || profile.id}”的当前会话？\n\nProfile 和登录态会保留，正在使用它的 Task 授权会立即撤销。`)) return;
    void api.closeBrowserProfileSession(profile.id).then(async () => {
      await actions.reload();
      actions.notify("notice", "浏览器会话已关闭");
    }).catch(error => actions.notify("error", error instanceof Error ? error.message : String(error)));
  }));
  document.querySelectorAll<HTMLElement>("[data-browser-profile-delete]").forEach(button => button.addEventListener("click", () => {
    const profile = data.profiles.find(item => item.id === button.dataset.browserProfileDelete);
    if (!profile) return;
    const active = profile.session && profile.session.state !== "closed";
    const detail = active
      ? "\n\n该 Profile 仍有活跃会话，服务端会拒绝删除；请先关闭会话。"
      : "\n\n删除只移除 AHA 中的逻辑 Profile；宿主上的真实用户数据目录不会被 AHA 删除。";
    if (!window.confirm(`删除浏览器 Profile“${profile.name || profile.id}”？${detail}`)) return;
    void api.deleteBrowserProfile(profile.id).then(async () => {
      await actions.reload();
      actions.notify("notice", "浏览器 Profile 已删除");
    }).catch(error => actions.notify("error", error instanceof Error ? error.message : String(error)));
  }));
}

// Shared with the Task shared-control panel.
export function browserProfileOptions(profiles: BrowserProfile[], selectedID = ""): string {
  if (!profiles.length) return '<option value="">还没有 Profile，请先到资源页创建</option>';
  return profiles.map(profile =>
    `<option value="${escapeHTML(profile.id)}" ${profile.id === selectedID ? "selected" : ""}>${escapeHTML(profile.name)} · ${escapeHTML(browserProfileStateLabel(effectiveState(profile)))}</option>`
  ).join("");
}

export function browserSessionStatusText(session: BrowserSession | null | undefined, host?: BrowserHostView | null): string {
  if (host && !host.available) return `浏览器宿主不可用：${host.error || host.code || "请启动 host 进程"}`;
  if (host && !host.edge_available) return "浏览器宿主在线，但未找到 Microsoft Edge";
  if (!session) return "尚未附加会话";
  const reachable = session.host_reachable === false ? " · 宿主不可达" : "";
  const error = session.error ? `：${session.error}` : "";
  return `会话 ${browserSessionStateLabel(session.state)}${reachable}${error}`;
}
