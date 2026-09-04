import {api} from "./api.js";
import {mountHardwareTerminal} from "./hardware_terminal.js";
import type {HardwareTerminalBinding} from "./hardware_terminal.js";
import {icon} from "./icons.js";
import type {
  HardwareGroup,
  HardwareIOEvent,
  HardwareTerminalStatus,
  SerialPort,
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
  selectedID: string;
  transport: Transport;
  sendEncoding: "text" | "hex";
  sendNewline: boolean;
  sendData: string;
  dirty: boolean;
  ports: SerialPort[];
  portsLoaded: boolean;
  streams: Record<string, StreamState>;
}

interface StoredDraft {
  groups: HardwareGroup[];
  selectedID: string;
  transport: Transport;
}

const panelStates = new Map<string, PanelState>();
const customOption = "__custom__";
const commonBaudrates = [9600, 19200, 38400, 57600, 115200, 230400, 460800, 921600];
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
let pollTimer = 0;
let activeTerminal: HardwareTerminalBinding | null = null;

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

function hardwareDraftKey(taskID: string): string {
  return `aha2:hardware-draft:${taskID}`;
}

function loadHardwareDraft(taskID: string): StoredDraft | null {
  if (typeof sessionStorage === "undefined") return null;
  try {
    const value = JSON.parse(sessionStorage.getItem(hardwareDraftKey(taskID)) || "null") as StoredDraft | null;
    return value && Array.isArray(value.groups) ? value : null;
  } catch {
    return null;
  }
}

function persistHardwareDraft(state: PanelState): void {
  if (typeof sessionStorage === "undefined") return;
  const groups = state.groups.map(group => ({
    task_id: group.task_id,
    id: group.id,
    position: group.position,
    description: group.description,
    mode: group.mode,
    serial: {...group.serial},
    network: {...group.network},
    username: group.username || "",
    password_configured: group.password_configured,
    access: group.access,
    created_at: group.created_at,
    updated_at: group.updated_at,
  }));
  sessionStorage.setItem(hardwareDraftKey(state.taskID), JSON.stringify({
    groups, selectedID: state.selectedID, transport: state.transport,
  }));
}

function clearHardwareDraft(taskID: string): void {
  if (typeof sessionStorage !== "undefined") sessionStorage.removeItem(hardwareDraftKey(taskID));
}

function panelState(detail: TaskDetail): PanelState {
  let state = panelStates.get(detail.task.id);
  if (!state) {
    const stored = loadHardwareDraft(detail.task.id);
    const groups = cloneGroups(stored?.groups || detail.hardware || []);
    const selectedID = groups.some(group => group.id === stored?.selectedID) ? stored!.selectedID : (groups[0]?.id || "");
    state = {
      taskID: detail.task.id,
      groups,
      selectedID,
      transport: stored?.transport || (groups[0]?.mode === "network" ? "network" : "serial"),
      sendEncoding: "text",
      sendNewline: true,
      sendData: "",
      dirty: Boolean(stored),
      ports: [],
      portsLoaded: false,
      streams: {},
    };
    panelStates.set(detail.task.id, state);
  } else if (!state.dirty && detail.hardware) {
    const selected = state.selectedID;
    state.groups = cloneGroups(detail.hardware);
    state.selectedID = state.groups.some(group => group.id === selected) ? selected : (state.groups[0]?.id || "");
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

function streamKey(state: PanelState): string {
  return `${state.selectedID}:${state.transport}`;
}

function currentStream(state: PanelState): StreamState {
  const key = streamKey(state);
  return state.streams[key] ||= {items: [], latest: 0, status: null, lastError: ""};
}

function modeOptions(value: string): string {
  return [
    ["off", "关闭"],
    ["serial", "串口"],
    ["network", "网络"],
    ["both", "串口 + 网络"],
  ].map(([id, label]) => `<option value="${id}" ${value === id ? "selected" : ""}>${label}</option>`).join("");
}

function accessOptions(value: string): string {
  return [
    ["read_write", "读写"],
    ["read_only", "只读"],
  ].map(([id, label]) => `<option value="${id}" ${value === id ? "selected" : ""}>${label}</option>`).join("");
}

function groupOptions(state: PanelState): string {
  return state.groups.map((group, index) =>
    `<option value="${escapeHTML(group.id)}" ${group.id === state.selectedID ? "selected" : ""}>${escapeHTML(group.description || `硬件 ${index + 1}`)}</option>`,
  ).join("");
}

function serialDeviceOptions(state: PanelState, current = ""): string {
  const values = new Set<string>();
  const detected = state.ports.some(port => port.device === current);
  const options = [`<option value="" ${current ? "" : "selected"}>选择串口设备</option>`];
  for (const port of state.ports) {
    if (!port.device || values.has(port.device)) continue;
    values.add(port.device);
    const label = port.description && port.description !== port.device ? `${port.device} - ${port.description}` : port.device;
    options.push(`<option value="${escapeHTML(port.device)}" ${current === port.device ? "selected" : ""}>${escapeHTML(label)}</option>`);
  }
  options.push(`<option value="${customOption}" ${current && !detected ? "selected" : ""}>自定义...</option>`);
  return options.join("");
}

function baudrateOptions(current: number): string {
  const known = commonBaudrates.includes(current);
  return [
    ...commonBaudrates.map(value => `<option value="${value}" ${current === value ? "selected" : ""}>${value}</option>`),
    `<option value="${customOption}" ${!known ? "selected" : ""}>自定义...</option>`,
  ].join("");
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

export function renderHardwarePanel(detail: TaskDetail): string {
  const state = panelState(detail);
  const group = selectedGroup(state);
  const stream = currentStream(state);
  const canConnect = supports(group, state.transport) && !state.dirty;
  const connected = Boolean(stream.status?.connected);
  const readOnly = Boolean(stream.status?.read_only || group?.access !== "read_write" || detail.task.status === "completed" || detail.task.status === "failed" || detail.task.status === "cancelled");
  const serialSelected = state.transport === "serial";
  const sendEncoding = serialSelected ? state.sendEncoding : "text";
  return `<div id="hardware-tool" class="hardware-tool" data-task-id="${escapeHTML(detail.task.id)}">
    <details class="hardware-config" open>
      <summary>${icon("edit")}<span>连接配置</span></summary>
      <div class="hardware-config-body">
        <div class="hardware-group-row">
          <select id="hardware-group-select" aria-label="硬件组">${groupOptions(state) || '<option value="">暂无硬件组</option>'}</select>
          <button type="button" id="hardware-save-top" class="icon-button ${state.dirty ? "primary" : ""}" title="保存全部硬件组" ${state.dirty ? "" : "disabled"}>${icon("save")}</button>
          <button type="button" id="hardware-add" class="icon-button" title="添加硬件组">${icon("plus")}</button>
          <button type="button" id="hardware-remove" class="icon-button" title="删除当前硬件组" ${group ? "" : "disabled"}>${icon("close")}</button>
        </div>
        ${group ? `<form id="hardware-config-form">
          <label>描述<textarea name="description" rows="2">${escapeHTML(group.description)}</textarea></label>
          <div class="two">
            <label>连接方式<select name="mode">${modeOptions(group.mode)}</select></label>
            <label>权限<select name="access">${accessOptions(group.access)}</select></label>
          </div>
          <section data-hardware-config="serial">
            <label>串口设备<div class="hardware-picker-row"><select name="serial_device_choice">${serialDeviceOptions(state, group.serial.device)}</select><button type="button" id="hardware-refresh-ports" class="icon-button" title="刷新串口">${icon("refresh")}</button></div></label>
            <input name="serial_device_custom" value="${escapeHTML(group.serial.device)}" placeholder="COM3 或 /dev/ttyUSB0" ${group.serial.device && !state.ports.some(port => port.device === group.serial.device) ? "" : "hidden"}>
            <label>波特率<select name="serial_baudrate_choice">${baudrateOptions(group.serial.baudrate || 115200)}</select></label>
            <input name="serial_baudrate_custom" type="number" min="1" value="${group.serial.baudrate || 115200}" ${commonBaudrates.includes(group.serial.baudrate || 115200) ? "hidden" : ""}>
          </section>
          <section data-hardware-config="network">
            <div class="two">
              <label>地址<input name="network_host" value="${escapeHTML(group.network.host)}" placeholder="192.168.1.20"></label>
              <label>端口<input name="network_port" type="number" min="1" max="65535" value="${group.network.port || 23}"></label>
            </div>
            <label>协议<select name="network_protocol"><option value="telnet" ${group.network.protocol === "telnet" ? "selected" : ""}>Telnet</option><option value="raw" ${group.network.protocol === "raw" ? "selected" : ""}>Raw TCP</option><option value="ssh" ${group.network.protocol === "ssh" ? "selected" : ""}>SSH</option></select></label>
            <section data-hardware-config="ssh">
              <label>SSH 登录方式<select name="ssh_auth"><option value="auto" ${group.network.ssh_auth === "auto" ? "selected" : ""}>自动（有密码时优先密码）</option><option value="password" ${group.network.ssh_auth === "password" ? "selected" : ""}>密码</option><option value="key" ${group.network.ssh_auth === "key" ? "selected" : ""}>Key (~/.ssh)</option></select></label>
            </section>
          </section>
          <section data-hardware-config="login">
            <label>登录用户名<input name="username" value="${escapeHTML(group.username || "")}" autocomplete="off"></label>
            <label>登录密码<input name="password" type="password" value="${escapeHTML(group._password || "")}" placeholder="${group.password_configured ? "已配置，留空保持不变" : "可选"}" autocomplete="new-password"></label>
            <label class="hardware-clear-secret"><input name="clear_password" type="checkbox" ${group._clearPassword ? "checked" : ""}>清除已保存密码</label>
          </section>
          <div class="hardware-config-actions"><span id="hardware-save-state">${state.dirty ? "有未保存修改" : "配置已保存"}</span><button type="submit" class="primary">保存</button></div>
        </form>` : `<div class="hardware-empty"><p>暂无硬件组</p><button type="button" data-hardware-add-empty>${icon("plus")}添加硬件</button></div>`}
      </div>
    </details>
    <section class="hardware-console">
      <header class="hardware-console-toolbar">
        <div class="hardware-transport-switch">
          <button type="button" data-hardware-transport="serial" class="${serialSelected ? "active" : ""}" ${supports(group, "serial") ? "" : "disabled"}>串口</button>
          <button type="button" data-hardware-transport="network" class="${!serialSelected ? "active" : ""}" ${supports(group, "network") ? "" : "disabled"}>网络</button>
        </div>
        <span id="hardware-status" class="hardware-status ${statusClass(stream.status)}">${statusText(stream.status)}</span>
        <span id="hardware-endpoint">${escapeHTML(stream.status?.endpoint || (serialSelected ? group?.serial.device : group ? `${group.network.host}:${group.network.port}` : "") || "-")}</span>
        <div class="hardware-console-actions">
          <button type="button" id="hardware-connect" ${canConnect && !connected ? "" : "disabled"}>连接</button>
          <button type="button" id="hardware-disconnect" ${connected ? "" : "disabled"}>断开</button>
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
        <label title="仅文本模式发送后追加回车换行"><input name="newline" type="checkbox" ${state.sendNewline ? "checked" : ""} ${sendEncoding === "hex" ? "disabled" : ""}>CRLF</label>
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
  if (window.innerWidth <= 760) root.querySelector<HTMLDetailsElement>(".hardware-config")?.removeAttribute("open");

  const rerender = () => {
    const body = document.querySelector<HTMLElement>("#task-tool-panel-body");
    if (!body) return;
    body.innerHTML = renderHardwarePanel(detail);
    bindHardwarePanel(detail, notify);
  };
  const reportError = (error: unknown) => {
    const message = error instanceof Error ? error.message : String(error);
    currentStream(state).lastError = message;
    const inline = document.querySelector<HTMLElement>("#hardware-inline-error");
    if (inline) inline.textContent = message;
    notify("error", message);
  };
  const form = root.querySelector<HTMLFormElement>("#hardware-config-form");
  const readForm = () => {
    const group = selectedGroup(state);
    if (!group || !form) return;
    const data = new FormData(form);
    group.description = String(data.get("description") || "").trim();
    group.mode = String(data.get("mode") || "off") as HardwareGroup["mode"];
    group.access = String(data.get("access") || "read_write") as HardwareGroup["access"];
    const deviceChoice = String(data.get("serial_device_choice") || "");
    group.serial.device = String(deviceChoice === customOption ? data.get("serial_device_custom") : deviceChoice || "").trim();
    const baudrateChoice = String(data.get("serial_baudrate_choice") || "115200");
    group.serial.baudrate = Number(baudrateChoice === customOption ? data.get("serial_baudrate_custom") : baudrateChoice) || 115200;
    group.network.host = String(data.get("network_host") || "").trim();
    group.network.port = Number(data.get("network_port") || 23);
    group.network.protocol = String(data.get("network_protocol") || "telnet") as HardwareGroup["network"]["protocol"];
    group.network.ssh_auth = String(data.get("ssh_auth") || "auto") as HardwareGroup["network"]["ssh_auth"];
    group.username = String(data.get("username") || "").trim();
    group._password = String(data.get("password") || "");
    group._clearPassword = data.get("clear_password") === "on";
  };
  const markDirty = () => {
    state.dirty = true;
    persistHardwareDraft(state);
    const label = root.querySelector("#hardware-save-state");
    if (label) label.textContent = "有未保存修改";
    const save = root.querySelector<HTMLButtonElement>("#hardware-save-top");
    if (save) {
      save.disabled = false;
      save.classList.add("primary");
    }
  };
  const syncMode = () => {
    const mode = String(new FormData(form || undefined).get("mode") || "off");
    const protocol = String(new FormData(form || undefined).get("network_protocol") || "telnet");
    root.querySelectorAll<HTMLElement>('[data-hardware-config="serial"]').forEach(element => {
      element.hidden = mode !== "serial" && mode !== "both";
    });
    root.querySelectorAll<HTMLElement>('[data-hardware-config="network"]').forEach(element => {
      element.hidden = mode !== "network" && mode !== "both";
    });
    root.querySelectorAll<HTMLElement>('[data-hardware-config="ssh"]').forEach(element => {
      element.hidden = (mode !== "network" && mode !== "both") || protocol !== "ssh";
    });
    root.querySelectorAll<HTMLElement>('[data-hardware-config="login"]').forEach(element => {
      element.hidden = mode === "off";
    });
    const username = form?.querySelector<HTMLInputElement>('input[name="username"]');
    if (username) username.required = (mode === "network" || mode === "both") && protocol === "ssh";
    const deviceChoice = form?.querySelector<HTMLSelectElement>('select[name="serial_device_choice"]');
    const deviceCustom = form?.querySelector<HTMLInputElement>('input[name="serial_device_custom"]');
    if (deviceCustom) deviceCustom.hidden = deviceChoice?.value !== customOption;
    const baudrateChoice = form?.querySelector<HTMLSelectElement>('select[name="serial_baudrate_choice"]');
    const baudrateCustom = form?.querySelector<HTMLInputElement>('input[name="serial_baudrate_custom"]');
    if (baudrateCustom) baudrateCustom.hidden = baudrateChoice?.value !== customOption;
  };
  form?.addEventListener("input", () => {
    readForm();
    markDirty();
    syncMode();
  });
  form?.addEventListener("change", event => {
    const target = event.target as HTMLInputElement | HTMLSelectElement;
    if (target.name === "network_protocol") {
      const port = form.querySelector<HTMLInputElement>('input[name="network_port"]');
      if (port && target.value === "ssh" && (!port.value || port.value === "23")) port.value = "22";
      if (port && target.value !== "ssh" && port.value === "22") port.value = "23";
    }
    readForm();
    markDirty();
    syncMode();
  });
  const saveGroups = async () => {
    readForm();
    const response = await api.updateTaskHardware(detail.task.id, state.groups.map(group => ({
      id: group.id,
      description: group.description,
      mode: group.mode,
      serial: group.serial,
      network: group.network,
      username: group.username || "",
      password: group._password || "",
      clear_password: Boolean(group._clearPassword),
      access: group.access,
    })));
    detail.hardware = response.groups;
    state.groups = cloneGroups(response.groups);
    state.dirty = false;
    state.selectedID = state.groups.some(group => group.id === state.selectedID) ? state.selectedID : (state.groups[0]?.id || "");
    clearHardwareDraft(detail.task.id);
    notify("notice", "硬件配置已保存");
    rerender();
  };
  form?.addEventListener("submit", event => {
    event.preventDefault();
    void saveGroups().catch(reportError);
  });
  root.querySelector("#hardware-save-top")?.addEventListener("click", () => void saveGroups().catch(reportError));
  root.querySelector("#hardware-group-select")?.addEventListener("change", event => {
    readForm();
    state.selectedID = (event.currentTarget as HTMLSelectElement).value;
    const group = selectedGroup(state);
    state.transport = group?.mode === "network" ? "network" : "serial";
    if (state.dirty) persistHardwareDraft(state);
    rerender();
  });
  const addGroup = () => {
    readForm();
    const existing = new Set(state.groups.map(group => group.id));
    let index = state.groups.length + 1;
    while (existing.has(`hardware-${index}`)) index++;
    const now = new Date().toISOString();
    state.groups.push({
      task_id: detail.task.id, id: `hardware-${index}`, position: state.groups.length,
      description: "", mode: "serial", serial: {device: "", baudrate: 115200},
      network: {host: "", port: 23, protocol: "telnet", ssh_auth: "auto"}, password_configured: false,
      access: "read_write", created_at: now, updated_at: now,
    });
    state.selectedID = `hardware-${index}`;
    state.transport = "serial";
    state.dirty = true;
    persistHardwareDraft(state);
    rerender();
  };
  root.querySelector("#hardware-add")?.addEventListener("click", addGroup);
  root.querySelector("[data-hardware-add-empty]")?.addEventListener("click", addGroup);
  root.querySelector("#hardware-remove")?.addEventListener("click", () => {
    const index = state.groups.findIndex(group => group.id === state.selectedID);
    if (index < 0) return;
    state.groups.splice(index, 1);
    state.selectedID = state.groups[Math.min(index, state.groups.length - 1)]?.id || "";
    state.transport = selectedGroup(state)?.mode === "network" ? "network" : "serial";
    state.dirty = true;
    persistHardwareDraft(state);
    rerender();
  });
  root.querySelector("#hardware-refresh-ports")?.addEventListener("click", () => {
    state.portsLoaded = false;
    void loadPorts(state, root, rerender);
  });
  root.querySelectorAll<HTMLElement>("[data-hardware-transport]").forEach(button => button.addEventListener("click", () => {
    state.transport = button.dataset.hardwareTransport as Transport;
    rerender();
  }));
  root.querySelector("#hardware-connect")?.addEventListener("click", () => {
    if (state.dirty) {
      notify("error", "请先保存硬件配置");
      return;
    }
    void api.connectHardware(detail.task.id, state.selectedID, state.transport).then(response => {
      currentStream(state).status = response.status;
      currentStream(state).lastError = "";
      notify("notice", `${response.status.endpoint} 已连接`);
      rerender();
    }).catch(reportError);
  });
  root.querySelector("#hardware-disconnect")?.addEventListener("click", () => {
    void api.disconnectHardware(detail.task.id, state.selectedID, state.transport).then(response => {
      currentStream(state).status = response.status;
      currentStream(state).lastError = "";
      activeTerminal?.setStatus(response.status);
      updateTerminalDOM(currentStream(state));
    }).catch(reportError);
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
    const newline = sendForm.querySelector<HTMLInputElement>('input[name="newline"]');
    state.sendEncoding = state.transport === "serial"
      ? String(data.get("encoding") || "text") as "text" | "hex"
      : "text";
    state.sendNewline = newline?.checked ?? state.sendNewline;
    state.sendData = String(data.get("data") || "");
    if (newline) newline.disabled = state.sendEncoding === "hex";
  };
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
  syncMode();
  void loadPorts(state, root, rerender);
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

async function loadPorts(state: PanelState, root: HTMLElement, rerender: () => void): Promise<void> {
  if (state.portsLoaded) return;
  try {
    const response = await api.serialPorts();
    state.ports = response.ports || [];
    state.portsLoaded = true;
    if (document.querySelector("#hardware-tool") === root) rerender();
  } catch {
    state.portsLoaded = true;
    // Manual serial device input remains available.
  }
}

async function pollHardware(detail: TaskDetail, state: PanelState, generation: number): Promise<void> {
  if (generation !== pollGeneration || !document.querySelector("#hardware-tool")) return;
  const group = selectedGroup(state);
  if (!group || !supports(group, state.transport) || state.dirty) {
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
  if (!container || !group || state.dirty || !stream.status) {
    activeTerminal?.dispose();
    activeTerminal = null;
    if (container) container.replaceChildren();
    return;
  }
  const key = `${detail.task.id}:${group.id}:${state.transport}`;
  if (activeTerminal?.key === key) return;
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
