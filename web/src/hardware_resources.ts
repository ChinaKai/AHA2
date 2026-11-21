import {api} from "./api.js";
import {icon} from "./icons.js";
import type {ADBUSBDevice, HardwareResource, SerialPort} from "./types.js";

type Notice = (type: "error" | "notice", message: string) => void;

function escapeHTML(value: unknown): string {
  return String(value ?? "")
    .replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;").replaceAll("'", "&#039;");
}

function modeLabel(mode: HardwareResource["mode"]): string {
  return {off: "关闭", serial: "串口", network: "网络", both: "串口 + 网络"}[mode] || mode;
}

function accessLabel(access: HardwareResource["access"]): string {
  return access === "read_only" ? "只读" : "读写";
}

type NetworkConfig = HardwareResource["network"];

// ADB has three connection forms under one protocol. A row without
// adb_connection predates the split and is wireless debugging.
function adbUSB(network: NetworkConfig): boolean {
  return network.protocol === "adb" && network.adb_connection === "usb";
}

function adbTCP(network: NetworkConfig): boolean {
  return network.protocol === "adb" && network.adb_connection === "tcp";
}

function adbWireless(network: NetworkConfig): boolean {
  return network.protocol === "adb" && network.adb_connection !== "usb" && network.adb_connection !== "tcp";
}

function networkProtocolLabel(network: NetworkConfig): string {
  if (adbUSB(network)) return "ADB（USB 直连）";
  if (adbTCP(network)) return "ADB（网络直连）";
  if (adbWireless(network)) return "ADB（无线调试）";
  return {telnet: "Telnet", raw: "Raw TCP", ssh: "SSH", adb: "ADB"}[network.protocol] || network.protocol;
}

// The protocol select carries the ADB connection form in its value so the
// Owner keeps choosing it in the same place as before.
type ProtocolChoice = "telnet" | "raw" | "ssh" | "adb" | "adb_tcp" | "adb_usb";

function protocolChoice(network: NetworkConfig | null): ProtocolChoice {
  if (!network) return "telnet";
  if (adbUSB(network)) return "adb_usb";
  if (adbTCP(network)) return "adb_tcp";
  return network.protocol;
}

const adbUSBStateLabels: Record<string, string> = {
  device: "已授权",
  unauthorized: "未授权，请在设备上允许 USB 调试",
  offline: "离线",
  no_permissions: "宿主机无 USB 访问权限",
};

function adbUSBDeviceLabel(device: ADBUSBDevice): string {
  const name = device.model || device.product || device.device || "";
  const state = adbUSBStateLabels[device.state] || device.state;
  return `${device.serial}${name ? ` · ${name.replaceAll("_", " ")}` : ""} · ${state}${device.selectable ? "" : " · 不可选择"}`;
}

function defaultNetworkPort(protocol: HardwareResource["network"]["protocol"]): number {
  if (protocol === "ssh") return 22;
  if (protocol === "adb") return 5555;
  return 23;
}

// A row from an older server or a partial fixture may omit a whole config
// block. One malformed row must not take down the entire Resources page, so
// reads go through these defaults instead of dereferencing `serial`/`network`
// directly.
function serialConfig(resource: HardwareResource): HardwareResource["serial"] {
  return resource.serial || {device: "", baudrate: 115200};
}

function networkConfig(resource: HardwareResource): HardwareResource["network"] {
  return resource.network || {host: "", port: 0, protocol: "telnet", ssh_auth: "auto"};
}

function endpointLabel(resource: HardwareResource): string {
  const serial = serialConfig(resource);
  const network = networkConfig(resource);
  const endpoints: string[] = [];
  if (serial.device) endpoints.push(serial.device);
  if (adbUSB(network)) {
    if (network.adb_serial) endpoints.push(`USB ${network.adb_serial}`);
  } else if (network.host) endpoints.push(`${network.host}:${network.port || defaultNetworkPort(network.protocol)}`);
  return endpoints.join(" / ") || "未配置端点";
}

function recoverySecretPlaceholder(resource?: HardwareResource): string {
  return resource?.password_configured ? "已配置，留空保持不变" : "可选";
}

export function renderHardwareResources(resources: HardwareResource[]): string {
  const rows = resources.map(resource => {
    const network = networkConfig(resource);
    const adbPair = adbWireless(network)
      ? `<button type="button" class="icon-button" data-hardware-adb-pair="${escapeHTML(resource.id)}" title="配对 ADB 设备" aria-label="配对 ADB 设备">${icon("link")}</button>`
      : "";
    const pairState = adbWireless(network)
      ? `<span class="hardware-resource-pair-state ${resource.adb_paired_at ? "paired" : ""}">${resource.adb_paired_at ? "已配对" : "未配对"}</span>`
      : "";
    return `<article class="hardware-resource-row" data-hardware-resource="${escapeHTML(resource.id)}">
    <div class="hardware-resource-identity"><span class="hardware-resource-glyph">${icon("hardware")}</span><div><strong>${escapeHTML(resource.description || resource.id)}</strong><small>${escapeHTML(modeLabel(resource.mode))}${resource.mode === "network" ? ` · ${escapeHTML(networkProtocolLabel(network))}` : ""} · ${escapeHTML(endpointLabel(resource))}</small></div></div>
    <div class="hardware-resource-meta">${pairState}<span>${escapeHTML(accessLabel(resource.access))}</span><span>${resource.password_configured ? "凭据已配置" : "无保存凭据"}</span><span>${resource.binding_count} 个任务启用</span></div>
    <div class="hardware-resource-actions">${adbPair}<button type="button" class="icon-button" data-hardware-resource-edit="${escapeHTML(resource.id)}" title="编辑硬件资源" aria-label="编辑硬件资源">${icon("edit")}</button><button type="button" class="icon-button danger" data-hardware-resource-delete="${escapeHTML(resource.id)}" title="删除硬件资源" aria-label="删除硬件资源">${icon("close")}</button></div>
  </article>`;
  }).join("");
  return `<section class="hardware-resources-view">
    <div class="resource-panel-toolbar"><div><strong>硬件</strong><p>维护系统级串口、网络设备和登录凭据。任务页只选择启用，不再重复创建硬件。</p></div><button type="button" id="add-hardware-resource" class="primary">${icon("plus")}添加硬件资源</button></div>
    ${resources.length ? `<div class="hardware-resource-list">${rows}</div>` : `<div class="empty-state hardware-resource-empty">${icon("hardware")}<strong>还没有硬件资源</strong><span>添加后可在任意任务中启用。</span><button type="button" class="primary" data-hardware-resource-add-empty>${icon("plus")}添加硬件资源</button></div>`}
  </section>`;
}

function modeOptions(value: string): string {
  return [
    ["serial", "串口"],
    ["network", "网络"],
  ].map(([id, label]) => `<option value="${id}" ${value === id ? "selected" : ""}>${label}</option>`).join("");
}

function accessOptions(value: string): string {
  return [
    ["read_only", "只读"],
    ["read_write", "读写"],
  ].map(([id, label]) => `<option value="${id}" ${value === id ? "selected" : ""}>${label}</option>`).join("");
}

function protocolOptions(value: string): string {
  return [
    ["telnet", "Telnet"],
    ["raw", "Raw TCP"],
    ["ssh", "SSH"],
    ["adb", "ADB（无线调试 / 配对码）"],
    ["adb_tcp", "ADB（网络直连 / IP:端口，免配对）"],
    ["adb_usb", "ADB（USB 直连）"],
  ].map(([id, label]) => `<option value="${id}" ${value === id ? "selected" : ""}>${label}</option>`).join("");
}

function sshAuthOptions(value: string): string {
  return [
    ["auto", "自动（有密码时优先密码）"],
    ["password", "密码"],
    ["key", "Key (~/.ssh)"],
  ].map(([id, label]) => `<option value="${id}" ${value === id ? "selected" : ""}>${label}</option>`).join("");
}

// The USB select never preselects a detected device: with several devices
// attached, a default could silently point the resource at the wrong one.
// A saved serial stays listed even when it is not attached right now.
function adbUSBOptions(devices: ADBUSBDevice[], saved: string, loaded: boolean): string {
  const options = [`<option value="" ${saved ? "" : "selected"}>${loaded && !devices.length ? "未检测到 USB 设备" : "请选择 USB 设备"}</option>`];
  let savedListed = false;
  for (const device of devices) {
    if (device.serial === saved) savedListed = true;
    const selected = device.serial === saved && device.selectable;
    options.push(`<option value="${escapeHTML(device.serial)}" ${selected ? "selected" : ""} ${device.selectable ? "" : "disabled"}>${escapeHTML(adbUSBDeviceLabel(device))}</option>`);
  }
  if (saved && !savedListed) {
    options.push(`<option value="${escapeHTML(saved)}" selected>${escapeHTML(saved)} · ${loaded ? "当前未检测到" : "已保存"}</option>`);
  }
  return options.join("");
}

function resourceDialog(resource?: HardwareResource, ports: SerialPort[] = []): HTMLDialogElement {
  const serial = resource ? serialConfig(resource) : null;
  const network = resource ? networkConfig(resource) : null;
  const dialog = document.createElement("dialog");
  dialog.className = "wide hardware-resource-dialog";
  dialog.innerHTML = `<form id="hardware-resource-form">
    <div class="dialog-head"><h2>${resource ? "编辑硬件资源" : "添加硬件资源"}</h2><button type="button" data-close class="icon-button" aria-label="关闭">${icon("close")}</button></div>
    <label>名称<input name="description" maxlength="2000" required value="${escapeHTML(resource?.description || "")}" placeholder="例如：主控板串口"></label>
    <div class="two"><label>连接方式<select name="mode">${modeOptions(resource?.mode === "network" ? "network" : "serial")}</select></label><label>权限<select name="access">${accessOptions(resource?.access || "read_write")}</select></label></div>
    <section data-resource-config="serial">
      <div class="two"><label>串口设备<input name="serial_device" list="hardware-resource-ports" value="${escapeHTML(serial?.device || "")}" placeholder="COM3 或 /dev/ttyUSB0"></label><label>波特率<input name="serial_baudrate" type="number" min="1" value="${Number(serial?.baudrate || 115200)}"></label></div>
      <datalist id="hardware-resource-ports">${ports.map(port => `<option value="${escapeHTML(port.device)}">${escapeHTML(port.description || port.device)}</option>`).join("")}</datalist>
    </section>
    <section data-resource-config="network">
      <div class="two"><label>协议<select name="network_protocol">${protocolOptions(protocolChoice(network))}</select></label><label data-resource-config="ssh">SSH 登录方式<select name="ssh_auth">${sshAuthOptions(network?.ssh_auth || "auto")}</select></label></div>
      <div class="two" data-resource-config="network-endpoint"><label>地址<input name="network_host" value="${escapeHTML(network && !adbUSB(network) ? network.host : "")}" placeholder="192.168.1.20"></label><label><span data-resource-port-label>端口</span><input name="network_port" type="number" min="1" max="65535" value="${Number((network && !adbUSB(network) ? network.port : 0) || defaultNetworkPort(network?.protocol || "telnet"))}"></label></div>
      <p class="field-help" data-resource-config="adb">地址和端口填手机「无线调试」页显示的 IP 地址和端口（调试连接端口）。保存后用资源行的配对按钮配对：配对端口和六位配对码在手机「使用配对码配对设备」里另外生成，和连接端口不同，也不会保存。</p>
      <p class="field-help" data-resource-config="adb-tcp">设备需已开启网络 ADB（例如之前用数据线执行过 adb tcpip 5555，或设备自带的网络调试）。AHA 直接连接这里填写的 IP:端口，默认 5555，不需要配对码，也不会按 mDNS 换成别的端口。首次连接时请在设备上确认「允许调试」。</p>
      <div data-resource-config="adb-usb">
        <div class="hardware-adb-usb-row"><label>USB 设备<select name="adb_serial" data-adb-usb-select>${adbUSBOptions([], network && adbUSB(network) ? network.adb_serial || "" : "", false)}</select></label><button type="button" data-adb-usb-refresh>${icon("refresh")}<span>刷新设备</span></button></div>
        <p class="field-help" data-adb-usb-status>设备列表来自运行 AHA 的电脑。用数据线连接设备，并在设备的开发者选项里开启「USB 调试」。</p>
        <p class="field-help">USB 直连不需要 IP、端口或配对码。首次连接时设备会弹出「允许 USB 调试」，请在设备上确认。</p>
      </div>
    </section>
    <section data-resource-config="login">
      <div class="two"><label>登录用户名<input name="username" autocomplete="off" value="${escapeHTML(resource?.username || "")}"></label><label>登录密码<input name="password" type="password" autocomplete="new-password" placeholder="${recoverySecretPlaceholder(resource)}"></label></div>
      ${resource ? `<label class="hardware-clear-secret"><input name="clear_password" type="checkbox">清除已保存密码</label>` : ""}
    </section>
    <p class="field-help">一个资源只对应一种连接方式。串口设备属于当前运行设备；网络资源只要当前设备能够访问即可使用。任务启用后直接继承这里的权限。</p>
    <div class="dialog-actions"><button type="button" data-close>取消</button><button type="submit" class="primary">${resource ? "保存资源" : "添加资源"}</button></div>
  </form>`;
  return dialog;
}

export function bindHardwareResources(
  resources: HardwareResource[],
  reload: () => Promise<void>,
  notify: Notice,
): void {
  let dialog: HTMLDialogElement | null = null;
  const closeDialog = () => {
    dialog?.close();
    dialog?.remove();
    dialog = null;
  };
  const openDialog = async (resource?: HardwareResource) => {
    closeDialog();
    let ports: SerialPort[] = [];
    try {
      ports = (await api.serialPorts()).ports || [];
    } catch {
      // A resource using a manually typed serial device remains editable.
    }
    dialog = resourceDialog(resource, ports);
    document.body.appendChild(dialog);
    const form = dialog.querySelector<HTMLFormElement>("#hardware-resource-form");
    const syncFields = () => {
      if (!form) return;
      const mode = String(new FormData(form).get("mode") || "serial");
      const protocol = String(new FormData(form).get("network_protocol") || "telnet");
      const network = mode === "network" || mode === "both";
      const anyADB = protocol === "adb" || protocol === "adb_tcp" || protocol === "adb_usb";
      form.querySelectorAll<HTMLElement>('[data-resource-config="serial"]').forEach(element => { element.hidden = mode !== "serial" && mode !== "both"; });
      form.querySelectorAll<HTMLElement>('[data-resource-config="network"]').forEach(element => { element.hidden = !network; });
      form.querySelectorAll<HTMLElement>('[data-resource-config="network-endpoint"]').forEach(element => { element.hidden = !network || protocol === "adb_usb"; });
      form.querySelectorAll<HTMLElement>('[data-resource-config="ssh"]').forEach(element => { element.hidden = !network || protocol !== "ssh"; });
      form.querySelectorAll<HTMLElement>('[data-resource-config="adb"]').forEach(element => { element.hidden = !network || protocol !== "adb"; });
      form.querySelectorAll<HTMLElement>('[data-resource-config="adb-tcp"]').forEach(element => { element.hidden = !network || protocol !== "adb_tcp"; });
      form.querySelectorAll<HTMLElement>('[data-resource-config="adb-usb"]').forEach(element => { element.hidden = !network || protocol !== "adb_usb"; });
      form.querySelectorAll<HTMLElement>('[data-resource-config="login"]').forEach(element => { element.hidden = network && anyADB; });
      const portLabel = form.querySelector<HTMLElement>("[data-resource-port-label]");
      if (portLabel) portLabel.textContent = protocol === "adb" ? "调试连接端口" : "端口";
      const usbSelect = form.querySelector<HTMLSelectElement>("[data-adb-usb-select]");
      if (usbSelect) usbSelect.required = network && protocol === "adb_usb";
      if (network && protocol === "adb_usb" && !usbLoaded && !usbLoading) void loadUSBDevices();
    };
    let usbLoaded = false;
    let usbLoading = false;
    const loadUSBDevices = async () => {
      const select = form?.querySelector<HTMLSelectElement>("[data-adb-usb-select]");
      const status = form?.querySelector<HTMLElement>("[data-adb-usb-status]");
      const refresh = form?.querySelector<HTMLButtonElement>("[data-adb-usb-refresh]");
      if (!select || !status) return;
      usbLoading = true;
      if (refresh) refresh.disabled = true;
      status.textContent = "正在读取 AHA 宿主机上的 USB 设备…";
      try {
        const result = await api.adbUSBDevices();
        if (!select.isConnected) return;
        const devices = result.devices || [];
        select.innerHTML = adbUSBOptions(devices, select.value, true);
        usbLoaded = true;
        const other = result.other_count ? `另有 ${result.other_count} 个网络或模拟器设备未列出。` : "";
        if (!devices.length) status.textContent = `没有检测到 USB 设备。请用数据线连接设备并开启「USB 调试」后刷新。${other}`;
        else if (devices.length > 1) status.textContent = `检测到 ${devices.length} 台 USB 设备，请按序列号选择要用的那一台。${other}`;
        else status.textContent = `检测到 1 台 USB 设备，请确认序列号后选择。${other}`;
      } catch (error) {
        if (select.isConnected) status.textContent = error instanceof Error ? error.message : String(error);
      } finally {
        usbLoading = false;
        if (refresh?.isConnected) refresh.disabled = false;
      }
    };
    form?.querySelector("[data-adb-usb-refresh]")?.addEventListener("click", () => void loadUSBDevices());
    form?.addEventListener("change", syncFields);
    form?.querySelector('select[name="network_protocol"]')?.addEventListener("change", event => {
      const choice = String((event.currentTarget as HTMLSelectElement).value);
      const protocol = (choice === "adb_usb" || choice === "adb_tcp" ? "adb" : choice) as NetworkConfig["protocol"];
      const port = form?.querySelector<HTMLInputElement>('input[name="network_port"]');
      if (choice !== "adb_usb" && port && (!port.value || ["22", "23", "5555"].includes(port.value))) port.value = String(defaultNetworkPort(protocol));
      syncFields();
    });
    syncFields();
    dialog.querySelectorAll("[data-close]").forEach(button => button.addEventListener("click", closeDialog));
    dialog.addEventListener("cancel", closeDialog);
    form?.addEventListener("submit", event => {
      event.preventDefault();
      const submit = form.querySelector<HTMLButtonElement>('button[type="submit"]');
      const original = submit?.innerHTML || "";
      if (submit) {
        submit.disabled = true;
        submit.innerHTML = `${icon("spinner", true)}<span>保存中</span>`;
      }
      void (async () => {
        const data = new FormData(form);
        const choice = String(data.get("network_protocol") || "telnet");
        const usb = choice === "adb_usb";
        const tcp = choice === "adb_tcp";
        const protocol = usb || tcp ? "adb" : choice;
        const adb = protocol === "adb";
        const payload = {
          description: String(data.get("description") || "").trim(),
          mode: String(data.get("mode") || "serial"),
          access: String(data.get("access") || "read_write"),
          serial: {
            device: String(data.get("serial_device") || "").trim(),
            baudrate: Number(data.get("serial_baudrate") || 115200),
          },
          // USB direct sends only the serial: no host, port or pairing data.
          network: usb ? {
            host: "",
            port: 0,
            protocol,
            ssh_auth: "auto",
            adb_connection: "usb",
            adb_serial: String(data.get("adb_serial") || "").trim(),
          } : {
            host: String(data.get("network_host") || "").trim(),
            port: Number(data.get("network_port") || defaultNetworkPort(protocol as NetworkConfig["protocol"])),
            protocol,
            ssh_auth: String(data.get("ssh_auth") || "auto"),
            ...(adb ? {adb_connection: tcp ? "tcp" : "wireless"} : {}),
          },
          username: adb ? "" : String(data.get("username") || "").trim(),
          password: adb ? "" : String(data.get("password") || ""),
          clear_password: adb || data.get("clear_password") === "on",
        };
        if (resource) await api.updateHardwareResource(resource.id, payload);
        else await api.createHardwareResource(payload);
        closeDialog();
        await reload();
        notify("notice", resource ? "硬件资源已保存" : "硬件资源已添加");
      })().catch(error => notify("error", error instanceof Error ? error.message : String(error))).finally(() => {
        if (submit?.isConnected) {
          submit.disabled = false;
          submit.innerHTML = original;
        }
      });
    });
    dialog.showModal();
  };

  const openADBPairDialog = (resource: HardwareResource) => {
    // Pairing codes belong to Android wireless debugging only; USB direct
    // relies on the device's own "allow USB debugging" prompt.
    if (!adbWireless(networkConfig(resource))) return;
    closeDialog();
    dialog = document.createElement("dialog");
    dialog.className = "hardware-pair-dialog";
    const network = networkConfig(resource);
    const connectionEndpoint = `${network.host}:${network.port || 5555}`;
    dialog.innerHTML = `<form id="adb-pair-form">
      <div class="dialog-head"><h2>配对 Android 设备</h2><button type="button" data-close class="icon-button" aria-label="关闭">${icon("close")}</button></div>
      <p class="field-help">调试连接端点：${escapeHTML(connectionEndpoint)}。在手机「无线调试 → 使用配对码配对设备」里查看配对端口和配对码，配对端口和连接端口不同。</p>
      <div class="two"><label>配对端口<input name="pairing_port" type="number" min="1" max="65535" inputmode="numeric" placeholder="手机显示的端口" required></label><label>6 位配对码<input name="pairing_code" type="password" inputmode="numeric" autocomplete="one-time-code" minlength="6" maxlength="6" pattern="[0-9]{6}" required></label></div>
      <div class="dialog-actions"><button type="button" data-close>取消</button><button type="submit" class="primary">开始配对</button></div>
    </form>`;
    document.body.appendChild(dialog);
    const form = dialog.querySelector<HTMLFormElement>("#adb-pair-form");
    dialog.querySelectorAll("[data-close]").forEach(button => button.addEventListener("click", closeDialog));
    dialog.addEventListener("cancel", closeDialog);
    form?.addEventListener("submit", event => {
      event.preventDefault();
      const submit = form.querySelector<HTMLButtonElement>('button[type="submit"]');
      const pairingPort = Number(new FormData(form).get("pairing_port") || 0);
      const pairingCode = String(new FormData(form).get("pairing_code") || "").trim();
      const original = submit?.innerHTML || "";
      if (submit) {
        submit.disabled = true;
        submit.innerHTML = `${icon("spinner", true)}<span>配对中</span>`;
      }
      void api.pairHardwareADB(resource.id, pairingPort, pairingCode).then(async response => {
        closeDialog();
        await reload();
        notify("notice", response.message || "Android 设备配对成功");
      }).catch(error => notify("error", error instanceof Error ? error.message : String(error))).finally(() => {
        if (submit?.isConnected) {
          submit.disabled = false;
          submit.innerHTML = original;
        }
      });
    });
    dialog.showModal();
    dialog.querySelector<HTMLInputElement>('input[name="pairing_code"]')?.focus();
  };

  document.querySelector("#add-hardware-resource")?.addEventListener("click", () => void openDialog());
  document.querySelector("[data-hardware-resource-add-empty]")?.addEventListener("click", () => void openDialog());
  document.querySelectorAll<HTMLElement>("[data-hardware-resource-edit]").forEach(button => button.addEventListener("click", () => {
    const resource = resources.find(item => item.id === button.dataset.hardwareResourceEdit);
    if (resource) void openDialog(resource);
  }));
  document.querySelectorAll<HTMLElement>("[data-hardware-adb-pair]").forEach(button => button.addEventListener("click", () => {
    const resource = resources.find(item => item.id === button.dataset.hardwareAdbPair);
    if (resource) openADBPairDialog(resource);
  }));
  document.querySelectorAll<HTMLElement>("[data-hardware-resource-delete]").forEach(button => button.addEventListener("click", () => {
    const resource = resources.find(item => item.id === button.dataset.hardwareResourceDelete);
    if (!resource) return;
    const detail = resource.binding_count ? `\n\n当前有 ${resource.binding_count} 个任务启用它，删除后这些任务会立即失去该硬件。` : "";
    if (!window.confirm(`删除硬件资源“${resource.description || resource.id}”？${detail}`)) return;
    void api.deleteHardwareResource(resource.id).then(async () => {
      await reload();
      notify("notice", "硬件资源已删除");
    }).catch(error => notify("error", error instanceof Error ? error.message : String(error)));
  }));
}
