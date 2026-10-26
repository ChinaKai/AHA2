import {api} from "./api.js";
import {mountHardwareTerminal} from "./hardware_terminal.js";
import type {HardwareTerminalBinding} from "./hardware_terminal.js";
import {icon} from "./icons.js";
import {copyText} from "./ui_helpers.js";
import type {
  HardwareGroup,
  HardwareIOEvent,
  HardwareResource,
  HardwareTerminalStatus,
  TaskDetail,
} from "./types.js";

type Transport = "serial" | "network";
type Notice = (type: "error" | "notice", message: string) => void;
type HardwareDraft = HardwareGroup & {_password?: string; _clearPassword?: boolean};

interface StreamState {
  items: HardwareIOEvent[];
  latest: number;
  status: HardwareTerminalStatus | null;
  lastError: string;
}

interface PanelState {
  taskID: string;
  groups: HardwareDraft[];
  resources: HardwareResource[];
  selectedID: string;
  transport: Transport;
  sendEncoding: "text" | "hex";
  sendNewline: boolean;
  sendData: string;
  configCollapsed: boolean;
  streams: Record<string, StreamState>;
}

const panelStates = new Map<string, PanelState>();
const terminalKeys: Record<string, string> = {
  enter: "\r",
  esc: "\x1b",
  tab: "\t",
  "ctrl-c": "\x03",
  up: "\x1b[A",
  down: "\x1b[B",
  left: "\x1b[D",
  right: "\x1b[C",
};
let pollGeneration = 0;
// True only while the panel's DOM is being swapped. Replacing the tree tears down
// whatever control had focus, and the browser fires a change event for it on the
// way out; that event must not be mistaken for a user edit.
let swappingPanelDOM = false;
let pollTimer = 0;
let activeTerminal: HardwareTerminalBinding | null = null;
let terminalAssetsPromise: Promise<void> | null = null;

const HARDWARE_AUTO_COPY_KEY = "aha2.hardware.autoCopy";

// Auto-copy is the default because selecting output is almost always followed by
// a paste somewhere; the toggle exists only for operators who want to select
// without touching the clipboard.
// localStorage is absent in the server-side render tests and can throw in a
// privacy-restricted context, so every access is best effort.
function hardwareAutoCopyStorage(): Storage | null {
  try {
    return typeof window === "undefined" ? null : window.localStorage ?? null;
  } catch {
    return null;
  }
}

export function hardwareAutoCopyEnabled(): boolean {
  return hardwareAutoCopyStorage()?.getItem(HARDWARE_AUTO_COPY_KEY) !== "0";
}

function setHardwareAutoCopy(enabled: boolean): void {
  try {
    hardwareAutoCopyStorage()?.setItem(HARDWARE_AUTO_COPY_KEY, enabled ? "1" : "0");
  } catch {
    // A blocked storage API must not break the console; the choice just does not
    // survive a reload.
  }
}

function loadTerminalAssets(): Promise<void> {
  if (window.Terminal) return Promise.resolve();
  if (terminalAssetsPromise) return terminalAssetsPromise;
  const appScript = document.querySelector<HTMLScriptElement>('script[src*="/app.js"]');
  const version = appScript ? new URL(appScript.src, location.href).search : "";
  const stylesheet = document.createElement("link");
  stylesheet.rel = "stylesheet";
  stylesheet.href = `/vendor/xterm.css${version}`;
  document.head.appendChild(stylesheet);
  terminalAssetsPromise = new Promise<void>((resolve, reject) => {
    const script = document.createElement("script");
    script.src = `/vendor/xterm.js${version}`;
    script.onload = () => resolve();
    script.onerror = () => reject(new Error("终端组件加载失败"));
    document.head.appendChild(script);
  });
  return terminalAssetsPromise;
}

function escapeHTML(value: unknown): string {
  return String(value ?? "")
    .replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;").replaceAll("'", "&#039;");
}

function cloneGroups(groups: HardwareGroup[] = []): HardwareDraft[] {
  return groups.map(group => ({
    ...group,
    serial: {...group.serial},
    network: {...group.network, ssh_auth: group.network.ssh_auth || "auto"},
    _password: "",
    _clearPassword: false,
  }));
}

function panelState(detail: TaskDetail): PanelState {
  let state = panelStates.get(detail.task.id);
  if (!state) {
    const groups = cloneGroups(detail.hardware || []);
    const selectedID = groups[0]?.id || "";
    state = {
      taskID: detail.task.id,
      groups,
      resources: detail.hardware_resources || [],
      selectedID,
      transport: groups[0]?.mode === "network" ? "network" : "serial",
      sendEncoding: "text",
      sendNewline: true,
      sendData: "",
      configCollapsed: groups.length > 0,
      streams: {},
    };
    panelStates.set(detail.task.id, state);
  } else if (detail.hardware) {
    const selected = state.selectedID;
    state.groups = cloneGroups(detail.hardware);
    state.resources = detail.hardware_resources || state.resources;
    state.selectedID = state.groups.some(group => group.id === selected) ? selected : (state.groups[0]?.id || "");
    reconcileTransport(state);
  }
  return state;
}

function selectedGroup(state: PanelState): HardwareDraft | undefined {
  return state.groups.find(group => group.id === state.selectedID);
}

function supports(group: HardwareDraft | undefined, transport: Transport): boolean {
  if (!group) return false;
  if (transport === "serial") {
    return (group.mode === "serial" || group.mode === "both") && Boolean(group.serial.device);
  }
  return (group.mode === "network" || group.mode === "both") && Boolean(group.network.host);
}

// Whenever the group list or the selection changes without the user picking a
// transport, the transport has to be one the selected group actually supports.
// Otherwise the console stays bound to the previous group's transport: 连接 is
// disabled and the terminal never mounts, which reads as "the new hardware needs
// a page reload before it can be used".
function reconcileTransport(state: PanelState): void {
  const group = selectedGroup(state);
  if (!supports(group, state.transport)) {
    state.transport = group?.mode === "network" ? "network" : "serial";
  }
}

function streamKey(state: PanelState): string {
  return `${state.selectedID}:${state.transport}`;
}

function currentStream(state: PanelState): StreamState {
  const key = streamKey(state);
  return state.streams[key] ||= {items: [], latest: 0, status: null, lastError: ""};
}

function groupOptions(state: PanelState): string {
  return state.groups.map((group, index) =>
    `<option value="${escapeHTML(group.id)}" ${group.id === state.selectedID ? "selected" : ""}>${escapeHTML(group.description || `硬件 ${index + 1}`)}</option>`,
  ).join("");
}

function selectedGroupLabel(state: PanelState): string {
  const index = state.groups.findIndex(group => group.id === state.selectedID);
  const group = index >= 0 ? state.groups[index] : undefined;
  return group?.description || (group ? `硬件 ${index + 1}` : "暂无硬件资源");
}

function syncTitleGroupSwitcher(state: PanelState): void {
  const trigger = document.querySelector<HTMLButtonElement>("#hardware-title-group-trigger");
  const menu = document.querySelector<HTMLElement>("#hardware-title-group-menu");
  if (!trigger || !menu) return;
  const label = trigger.querySelector<HTMLElement>("span");
  if (label) label.textContent = selectedGroupLabel(state);
  trigger.disabled = state.groups.length === 0;
  menu.innerHTML = state.groups.map((group, index) =>
    `<button type="button" data-hardware-title-group="${escapeHTML(group.id)}" class="${group.id === state.selectedID ? "active" : ""}">${escapeHTML(group.description || `硬件 ${index + 1}`)}</button>`,
  ).join("");
}

function hardwareGroupsSignature(groups: HardwareGroup[]): string {
  return JSON.stringify(groups.map(group => ({
    id: group.id,
    resource_id: group.resource_id || "",
    position: group.position,
    description: group.description,
    mode: group.mode,
    serial: group.serial,
    network: group.network,
    username: group.username || "",
    password_configured: group.password_configured,
    access: group.access,
  })));
}

export function renderHardwareGroupSwitcher(detail: TaskDetail): string {
  const state = panelState(detail);
  const options = state.groups.map((group, index) =>
    `<button type="button" data-hardware-title-group="${escapeHTML(group.id)}" class="${group.id === state.selectedID ? "active" : ""}">${escapeHTML(group.description || `硬件 ${index + 1}`)}</button>`,
  ).join("");
  return `<div class="hardware-title-group-switcher">
    <button type="button" id="hardware-title-group-trigger" class="hardware-title-group-trigger" aria-haspopup="menu" aria-expanded="false" ${state.groups.length ? "" : "disabled"}><span>${escapeHTML(selectedGroupLabel(state))}</span>${icon("chevron-down")}</button>
    <div id="hardware-title-group-menu" class="hardware-title-group-menu" role="menu" hidden>${options}</div>
  </div>`;
}

function statusClass(status: HardwareTerminalStatus | null): string {
  if (status?.connected) return "good";
  if (status?.status === "error") return "bad";
  return "muted";
}

function statusText(status: HardwareTerminalStatus | null): string {
  if (status?.connected) return "已连接";
  if (status?.status === "error") return "连接错误";
  return "未连接";
}

function terminalReplayData(stream: StreamState): string {
  return stream.items
    .filter(item => item.direction === "rx")
    .map(item => item.data)
    .join("");
}

function bytesToBase64(data: Uint8Array): string {
  let value = "";
  for (let offset = 0; offset < data.length; offset += 0x8000) {
    value += String.fromCharCode(...data.subarray(offset, offset+0x8000));
  }
  return btoa(value);
}

function networkProtocolLabel(resource: HardwareResource): string {
  return {telnet: "Telnet", raw: "Raw TCP", ssh: "SSH", adb: "ADB"}[resource.network.protocol] || resource.network.protocol;
}

function resourceNetworkPort(resource: HardwareResource): number {
  if (resource.network.port) return resource.network.port;
  if (resource.network.protocol === "ssh") return 22;
  if (resource.network.protocol === "adb") return 5555;
  return 23;
}

function resourceLabel(resource: HardwareResource): string {
  const endpoints: string[] = [];
  if (resource.serial.device) endpoints.push(resource.serial.device);
  if (resource.network.host) endpoints.push(`${networkProtocolLabel(resource)} ${resource.network.host}:${resourceNetworkPort(resource)}`);
  return `${resource.description || resource.id} · ${endpoints.join(" / ") || "未配置端点"}`;
}

function resourcePicker(state: PanelState): string {
  return state.resources.map(resource => {
    const binding = state.groups.find(group => (group.resource_id || group.id) === resource.id);
    const resourceState = resource.network.protocol === "adb"
      ? (resource.adb_paired_at ? " · 已配对" : " · 未配对")
      : (resource.password_configured ? " · 凭据已配置" : "");
    return `<article class="hardware-resource-choice ${binding ? "enabled" : ""} ${binding?.id === state.selectedID ? "selected" : ""}">
      <button type="button" class="hardware-resource-copy" data-hardware-resource-select="${escapeHTML(resource.id)}" ${binding ? "" : "disabled"}><span><strong>${escapeHTML(resource.description || resource.id)}</strong><small>${escapeHTML(resourceLabel(resource).split(" · ").slice(1).join(" · "))}${resourceState}</small></span><em>${resource.access === "read_write" ? "读写" : "只读"}</em></button>
      <button type="button" class="hardware-toggle ${binding ? "active" : ""}" data-hardware-resource-toggle="${escapeHTML(resource.id)}" role="switch" aria-checked="${binding ? "true" : "false"}" title="${binding ? "关闭任务硬件" : "开启任务硬件"}">${binding ? "关闭" : "开启"}</button>
    </article>`;
  }).join("");
}

export function renderHardwarePanel(detail: TaskDetail): string {
  const state = panelState(detail);
  const group = selectedGroup(state);
  const stream = currentStream(state);
  const canConnect = supports(group, state.transport);
  const connected = Boolean(stream.status?.connected);
  const readOnly = Boolean(stream.status?.read_only || group?.access !== "read_write" || detail.task.status === "completed" || detail.task.status === "failed" || detail.task.status === "cancelled");
  const remoteReadOnly = Boolean(detail.task.read_only);
  const serialSelected = state.transport === "serial";
  const sendEncoding = serialSelected ? state.sendEncoding : "text";
  return `<div id="hardware-tool" class="hardware-tool ${state.configCollapsed ? "config-collapsed" : ""}" data-task-id="${escapeHTML(detail.task.id)}">
    <details class="hardware-config" ${state.configCollapsed ? "" : "open"}>
      <summary>${icon("edit")}<span>连接配置</span></summary>
      <div class="hardware-config-body">
        <div class="hardware-group-row">
          <select id="hardware-group-select" aria-label="已启用硬件">${groupOptions(state) || '<option value="">当前任务未启用硬件</option>'}</select>
          <button type="button" id="hardware-config-collapse" class="icon-button" title="收起硬件资源" aria-label="收起硬件资源">${icon("panel")}</button>
          ${remoteReadOnly ? "" : `<button type="button" id="hardware-manage-resources" class="icon-button" data-view="hardware" title="管理硬件资源" aria-label="管理硬件资源">${icon("hardware")}</button>`}
        </div>
        <div class="hardware-binding-config">
          ${group ? `<div class="hardware-binding-summary"><div><strong>${escapeHTML(group.description || group.id)}</strong><small>${escapeHTML(resourceLabel(state.resources.find(item => item.id === (group.resource_id || group.id)) || {
            id: group.id, description: group.description, mode: group.mode, serial: group.serial, network: group.network,
            password_configured: group.password_configured, access: group.access, binding_count: 0,
            created_at: group.created_at, updated_at: group.updated_at,
          }))}</small></div><span>${group.access === "read_write" ? "任务读写" : "任务只读"}</span></div>` : ""}
          ${remoteReadOnly ? `<div class="field-help">这是其他设备的只读 Task，硬件绑定仅供查看；在本机接管后才能重新选择资源。</div>` : `<div class="hardware-resource-picker-head"><strong>系统硬件资源</strong><span>开启后仅供当前任务使用</span></div>
          <div class="hardware-resource-picker">${resourcePicker(state) || `<div class="hardware-empty"><p>系统还没有硬件资源</p><button type="button" data-view="hardware">前往硬件资源页添加</button></div>`}</div>`}
        </div>
      </div>
    </details>
    <section class="hardware-console">
      <header class="hardware-console-toolbar">
        <span class="hardware-transport-label">${serialSelected ? "串口" : "网络"}</span>
        <span id="hardware-status" class="hardware-status ${statusClass(stream.status)}">${statusText(stream.status)}</span>
        <span id="hardware-endpoint">${escapeHTML(stream.status?.endpoint || (serialSelected ? group?.serial.device : group ? `${group.network.host}:${group.network.port}` : "") || "-")}</span>
        <div class="hardware-console-actions">
          <button type="button" id="hardware-connect" ${canConnect && !connected ? "" : "disabled"}>连接</button>
          <button type="button" id="hardware-disconnect" ${connected ? "" : "disabled"}>断开</button>
          <button type="button" id="hardware-auto-copy" class="terminal-toggle${hardwareAutoCopyEnabled() ? " active" : ""}" aria-pressed="${hardwareAutoCopyEnabled() ? "true" : "false"}" title="选中终端内容后自动复制到剪贴板">选中即复制</button>
          <button type="button" id="hardware-copy-output" title="复制终端内容（有选中时只复制选中部分）">${icon("copy")}</button>
          <button type="button" id="hardware-clear-output" title="清空当前显示">${icon("close")}</button>
        </div>
      </header>
      <div id="hardware-xterm" class="hardware-xterm"></div>
      <div class="hardware-terminal-keys">
        <button type="button" data-hardware-terminal-key="enter" title="Enter" ${readOnly || !connected ? "disabled" : ""}>↵</button>
        <button type="button" data-hardware-terminal-key="esc" title="Escape" ${readOnly || !connected ? "disabled" : ""}>Esc</button>
        <button type="button" data-hardware-terminal-key="tab" title="Tab" ${readOnly || !connected ? "disabled" : ""}>Tab</button>
        <button type="button" data-hardware-terminal-key="ctrl-c" title="Ctrl+C" ${readOnly || !connected ? "disabled" : ""}>^C</button>
        <button type="button" data-hardware-terminal-key="left" title="Left" ${readOnly || !connected ? "disabled" : ""}>←</button>
        <button type="button" data-hardware-terminal-key="up" title="Up" ${readOnly || !connected ? "disabled" : ""}>↑</button>
        <button type="button" data-hardware-terminal-key="down" title="Down" ${readOnly || !connected ? "disabled" : ""}>↓</button>
        <button type="button" data-hardware-terminal-key="right" title="Right" ${readOnly || !connected ? "disabled" : ""}>→</button>
      </div>
      <form id="hardware-send-form" class="hardware-send">
        <select name="encoding" aria-label="发送格式" ${serialSelected ? "" : "disabled"}><option value="text" ${sendEncoding === "text" ? "selected" : ""}>文本</option><option value="hex" ${sendEncoding === "hex" ? "selected" : ""}>HEX</option></select>
        <textarea name="data" rows="1" placeholder="${readOnly ? "当前为只读" : "输入命令或数据"}" ${readOnly || !connected ? "disabled" : ""}>${escapeHTML(state.sendData)}</textarea>
        <button type="button" id="hardware-newline" class="terminal-toggle${state.sendNewline ? " active" : ""}" aria-pressed="${state.sendNewline ? "true" : "false"}" title="仅文本模式发送后追加回车换行" ${sendEncoding === "hex" ? "disabled" : ""}>CRLF</button>
        <button type="submit" class="primary" title="发送" aria-label="发送" ${readOnly || !connected ? "disabled" : ""}>${icon("send")}</button>
      </form>
      <div id="hardware-inline-error" class="hardware-inline-error">${escapeHTML(stream.lastError || stream.status?.error || "")}</div>
    </section>
  </div>`;
}

export function bindHardwarePanel(detail: TaskDetail, notify: Notice): void {
  stopHardwarePanel();
  const root = document.querySelector<HTMLElement>("#hardware-tool");
  if (!root) return;
  const state = panelState(detail);
  const generation = ++pollGeneration;
  syncTitleGroupSwitcher(state);
  if (window.innerWidth <= 760) root.querySelector<HTMLDetailsElement>(".hardware-config")?.removeAttribute("open");
  const config = root.querySelector<HTMLDetailsElement>(".hardware-config");
  config?.addEventListener("toggle", () => {
    state.configCollapsed = !config.open;
    root.classList.toggle("config-collapsed", state.configCollapsed);
  });
  root.querySelector("#hardware-config-collapse")?.addEventListener("click", () => {
    state.configCollapsed = true;
    if (config) config.open = false;
    root.classList.add("config-collapsed");
  });

  const rerender = () => {
    const body = document.querySelector<HTMLElement>("#task-tool-panel-body");
    if (!body) return;
    swappingPanelDOM = true;
    try {
      body.innerHTML = renderHardwarePanel(detail);
    } finally {
      swappingPanelDOM = false;
    }
    bindHardwarePanel(detail, notify);
  };
  const reportError = (error: unknown) => {
    const message = error instanceof Error ? error.message : String(error);
    currentStream(state).lastError = message;
    const inline = document.querySelector<HTMLElement>("#hardware-inline-error");
    if (inline) inline.textContent = message;
    notify("error", message);
  };
  const saveBindings = async (resourceIDs: string[], message: string) => {
    if (detail.task.status === "completed") {
      notify("error", "任务已完成，硬件启用关系只读；请先重新打开任务");
      return;
    }
    const bindings = resourceIDs.map(resourceID => {
      const existing = state.groups.find(group => (group.resource_id || group.id) === resourceID);
      return {id: existing?.id || resourceID, resource_id: resourceID};
    });
    const response = await api.updateTaskHardwareBindings(detail.task.id, bindings);
    detail.hardware = response.groups;
    detail.hardware_resources = response.resources;
    state.groups = cloneGroups(response.groups);
    state.resources = response.resources;
    state.selectedID = state.groups.some(group => group.id === state.selectedID) ? state.selectedID : (state.groups[0]?.id || "");
    // A group saved with a mode the current transport cannot serve (a new SSH
    // group while the console sits on 串口) would otherwise leave 连接 disabled
    // until the page is reloaded.
    reconcileTransport(state);
    notify("notice", message);
    rerender();
  };
  root.querySelectorAll<HTMLButtonElement>("[data-hardware-resource-toggle]").forEach(button => {
    button.addEventListener("click", () => {
      const resourceID = button.dataset.hardwareResourceToggle || "";
      const enabled = state.groups.some(group => (group.resource_id || group.id) === resourceID);
      const resourceIDs = state.groups.map(group => group.resource_id || group.id).filter(id => id !== resourceID);
      if (!enabled) resourceIDs.push(resourceID);
      button.disabled = true;
      void saveBindings(resourceIDs, enabled ? "已从任务关闭硬件" : "已为任务开启硬件").catch(reportError).finally(() => {
        if (button.isConnected) button.disabled = false;
      });
    });
  });
  root.querySelectorAll<HTMLButtonElement>("[data-hardware-resource-select]").forEach(button => {
    button.addEventListener("click", () => selectGroup(button.dataset.hardwareResourceSelect || ""));
  });
  const selectGroup = (value: string) => {
    state.selectedID = value;
    const group = selectedGroup(state);
    state.transport = group?.mode === "network" ? "network" : "serial";
    rerender();
  };
  root.querySelector("#hardware-group-select")?.addEventListener("change", event => {
    selectGroup((event.currentTarget as HTMLSelectElement).value);
  });
  const titleGroupTrigger = document.querySelector<HTMLButtonElement>("#hardware-title-group-trigger");
  const titleGroupMenu = document.querySelector<HTMLElement>("#hardware-title-group-menu");
  if (titleGroupTrigger && titleGroupMenu) {
    const switcher = titleGroupTrigger.closest<HTMLElement>(".hardware-title-group-switcher");
    const closeTitleGroupMenu = () => {
      titleGroupMenu.hidden = true;
      titleGroupTrigger.setAttribute("aria-expanded", "false");
    };
    titleGroupTrigger.onclick = () => {
      const open = titleGroupMenu.hidden;
      titleGroupMenu.hidden = !open;
      titleGroupTrigger.setAttribute("aria-expanded", String(open));
    };
    titleGroupMenu.querySelectorAll<HTMLButtonElement>("[data-hardware-title-group]").forEach(button => {
      button.onclick = () => selectGroup(button.dataset.hardwareTitleGroup || "");
    });
    switcher?.addEventListener("focusout", event => {
      if (event.relatedTarget instanceof Node && switcher.contains(event.relatedTarget)) return;
      closeTitleGroupMenu();
    });
    switcher?.addEventListener("keydown", event => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      closeTitleGroupMenu();
      titleGroupTrigger.focus();
    });
  }
  root.querySelector("#hardware-connect")?.addEventListener("click", () => {
    const button = root.querySelector<HTMLButtonElement>("#hardware-connect");
    if (button) button.disabled = true;
    const connect = async () => {
      try {
        return await api.connectHardware(detail.task.id, state.selectedID, state.transport);
      } catch (error) {
        const code = String((error as Error & {code?: string}).code || "");
        if (state.transport !== "network" || code !== "ssh_host_key_unknown") throw error;
        const {host_key: hostKey} = await api.hardwareHostKey(detail.task.id, state.selectedID);
        const confirmed = window.confirm(`首次连接 SSH 主机：${hostKey.endpoint}\n算法：${hostKey.algorithm}\nSHA256 指纹：${hostKey.fingerprint}\n\n请与设备控制台或可信渠道提供的指纹核对。确认信任并继续连接？`);
        if (!confirmed) return null;
        await api.trustHardwareHostKey(detail.task.id, state.selectedID, hostKey.fingerprint);
        notify("notice", `已信任 ${hostKey.endpoint} 的 SSH 主机键`);
        return api.connectHardware(detail.task.id, state.selectedID, state.transport);
      }
    };
    void connect().then(response => {
      if (!response) return;
      currentStream(state).status = response.status;
      currentStream(state).lastError = "";
      notify("notice", `${response.status.endpoint} 已连接`);
      rerender();
    }).catch(reportError).finally(() => { if (button) button.disabled = false; });
  });
  root.querySelector("#hardware-disconnect")?.addEventListener("click", () => {
    void api.disconnectHardware(detail.task.id, state.selectedID, state.transport).then(response => {
      currentStream(state).status = response.status;
      currentStream(state).lastError = "";
      activeTerminal?.setStatus(response.status);
      updateTerminalDOM(currentStream(state));
    }).catch(reportError);
  });
  root.querySelector<HTMLButtonElement>("#hardware-auto-copy")?.addEventListener("click", event => {
    const button = event.currentTarget as HTMLButtonElement;
    const enabled = button.getAttribute("aria-pressed") !== "true";
    setHardwareAutoCopy(enabled);
    button.setAttribute("aria-pressed", enabled ? "true" : "false");
    button.classList.toggle("active", enabled);
  });
  root.querySelector("#hardware-copy-output")?.addEventListener("click", () => {
    if (!activeTerminal) return;
    const selected = activeTerminal.selectedText();
    const text = selected || activeTerminal.bufferText();
    if (!text.trim()) {
      notify("notice", "终端没有可复制的内容");
      return;
    }
    void copyText(text).then(
      () => notify("notice", selected ? "已复制选中内容" : "已复制终端内容"),
      () => notify("error", "复制失败，请手动选择后复制"),
    );
  });
  root.querySelector("#hardware-clear-output")?.addEventListener("click", () => {
    currentStream(state).items = [];
    activeTerminal?.clear();
  });
  root.querySelectorAll<HTMLElement>("[data-hardware-terminal-key]").forEach(button => button.addEventListener("click", () => {
    const data = terminalKeys[button.dataset.hardwareTerminalKey || ""];
    if (data) activeTerminal?.send(data);
  }));
  const sendForm = root.querySelector<HTMLFormElement>("#hardware-send-form");
  const readSendForm = () => {
    if (!sendForm) return;
    const data = new FormData(sendForm);
    const newline = sendForm.querySelector<HTMLButtonElement>("#hardware-newline");
    state.sendEncoding = state.transport === "serial"
      ? String(data.get("encoding") || "text") as "text" | "hex"
      : "text";
    state.sendNewline = newline?.getAttribute("aria-pressed") === "true";
    state.sendData = String(data.get("data") || "");
    if (newline) newline.disabled = state.sendEncoding === "hex";
  };
  sendForm?.querySelector<HTMLButtonElement>("#hardware-newline")?.addEventListener("click", event => {
    const button = event.currentTarget as HTMLButtonElement;
    state.sendNewline = button.getAttribute("aria-pressed") !== "true";
    button.setAttribute("aria-pressed", state.sendNewline ? "true" : "false");
    button.classList.toggle("active", state.sendNewline);
  });
  sendForm?.addEventListener("input", readSendForm);
  sendForm?.addEventListener("change", readSendForm);
  sendForm?.addEventListener("submit", event => {
    event.preventDefault();
    readSendForm();
    let value = state.sendData;
    if (state.sendEncoding === "text" && state.sendNewline) value += "\r\n";
    void api.sendHardware(detail.task.id, state.selectedID, state.transport, value, state.sendEncoding).then(() => {
      state.sendData = "";
      const input = sendForm.querySelector<HTMLTextAreaElement>('textarea[name="data"]');
      if (input) input.value = "";
    }).catch(reportError);
  });
  void pollHardware(detail, state, generation);
  ensureHardwareTerminal(detail, state);
}

export function stopHardwarePanel(): void {
  pollGeneration++;
  if (pollTimer) window.clearTimeout(pollTimer);
  pollTimer = 0;
  activeTerminal?.dispose();
  activeTerminal = null;
}

export function refreshHardwarePanel(detail: TaskDetail, notify: Notice): boolean {
  const state = panelStates.get(detail.task.id);
  if (!state) return false;
  const groups = cloneGroups(detail.hardware || []);
  const resources = detail.hardware_resources || [];
  if (hardwareGroupsSignature(state.groups) === hardwareGroupsSignature(groups)
    && JSON.stringify(state.resources) === JSON.stringify(resources)) return false;
  const selected = state.selectedID;
  state.groups = groups;
  state.resources = resources;
  state.selectedID = groups.some(group => group.id === selected) ? selected : (groups[0]?.id || "");
  reconcileTransport(state);
  const body = document.querySelector<HTMLElement>("#task-tool-panel-body");
  if (!body) return false;
  body.innerHTML = renderHardwarePanel(detail);
  bindHardwarePanel(detail, notify);
  return true;
}

async function pollHardware(detail: TaskDetail, state: PanelState, generation: number): Promise<void> {
  if (generation !== pollGeneration || !document.querySelector("#hardware-tool")) return;
  const group = selectedGroup(state);
  if (!group || !supports(group, state.transport)) {
    pollTimer = window.setTimeout(() => void pollHardware(detail, state, generation), 1000);
    return;
  }
  const stream = currentStream(state);
  try {
    const terminalKey = `${detail.task.id}:${group.id}:${state.transport}`;
    const existingTerminal = activeTerminal?.key === terminalKey;
    const response = await api.hardwareTerminal(detail.task.id, group.id, state.transport, stream.latest);
    stream.status = response.status;
    if (response.status.connected) stream.lastError = "";
    if (response.stream.items?.length) {
      stream.items.push(...response.stream.items);
      if (stream.items.length > 2000) stream.items.splice(0, stream.items.length - 2000);
    }
    stream.latest = Math.max(stream.latest, response.stream.latest_sequence || 0);
    activeTerminal?.setStatus(response.status);
    updateTerminalDOM(stream);
    ensureHardwareTerminal(detail, state);
    if (existingTerminal) {
      for (const item of response.stream.items || []) {
        if (item.direction === "rx" && item.data) {
          activeTerminal?.writeFallback(new TextEncoder().encode(item.data));
        }
      }
    }
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    const inline = document.querySelector<HTMLElement>("#hardware-inline-error");
    if (inline) inline.textContent = message;
  }
  if (generation === pollGeneration) {
    pollTimer = window.setTimeout(() => void pollHardware(detail, state, generation), 600);
  }
}

function ensureHardwareTerminal(detail: TaskDetail, state: PanelState): void {
  const stream = currentStream(state);
  const container = document.querySelector<HTMLElement>("#hardware-xterm");
  const group = selectedGroup(state);
  if (!container || !group || !stream.status) {
    activeTerminal?.dispose();
    activeTerminal = null;
    if (container) container.replaceChildren();
    return;
  }
  const key = `${detail.task.id}:${group.id}:${state.transport}`;
  if (activeTerminal?.key === key) return;
  if (!window.Terminal) {
    container.textContent = "正在加载终端组件…";
    void loadTerminalAssets().then(() => ensureHardwareTerminal(detail, state)).catch(error => {
      container.textContent = error instanceof Error ? error.message : String(error);
    });
    return;
  }
  activeTerminal?.dispose();
  const transport = state.transport;
  activeTerminal = mountHardwareTerminal({
    taskID: detail.task.id,
    hardwareID: group.id,
    transport,
    status: stream.status,
    initialData: terminalReplayData(stream),
    afterSequence: stream.latest,
    container,
    onSelectionCopy(text) {
      if (!hardwareAutoCopyEnabled()) return;
      void copyText(text).then(
        () => notify("notice", `已复制选中内容（${text.length} 字符）`),
        () => notify("error", "自动复制失败，可用复制按钮或手动复制"),
      );
    },
    onFallbackSend(data) {
      return api.sendHardware(
        detail.task.id,
        group.id,
        transport,
        bytesToBase64(data),
        "base64",
      ).then(() => undefined);
    },
    onStatus(status) {
      stream.status = status;
      updateTerminalDOM(stream);
    },
    onError(message) {
      stream.lastError = message;
      const inline = document.querySelector<HTMLElement>("#hardware-inline-error");
      if (inline) inline.textContent = message;
    },
  });
}

function updateTerminalDOM(stream: StreamState): void {
  const status = document.querySelector<HTMLElement>("#hardware-status");
  if (status) {
    status.className = `hardware-status ${statusClass(stream.status)}`;
    status.textContent = statusText(stream.status);
  }
  const endpoint = document.querySelector<HTMLElement>("#hardware-endpoint");
  if (endpoint && stream.status?.endpoint) endpoint.textContent = stream.status.endpoint;
  const connect = document.querySelector<HTMLButtonElement>("#hardware-connect");
  const disconnect = document.querySelector<HTMLButtonElement>("#hardware-disconnect");
  if (connect) connect.disabled = Boolean(stream.status?.connected);
  if (disconnect) disconnect.disabled = !stream.status?.connected;
  const readOnly = Boolean(stream.status?.read_only);
  document.querySelectorAll<HTMLInputElement | HTMLTextAreaElement | HTMLButtonElement>("#hardware-send-form textarea, #hardware-send-form button").forEach(element => {
    element.disabled = !stream.status?.connected || readOnly;
  });
  document.querySelectorAll<HTMLButtonElement>("[data-hardware-terminal-key]").forEach(element => {
    element.disabled = !stream.status?.connected || readOnly;
  });
  const inline = document.querySelector<HTMLElement>("#hardware-inline-error");
  if (inline) inline.textContent = stream.lastError || stream.status?.error || "";
}
