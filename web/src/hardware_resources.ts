import {api} from "./api.js";
import {icon} from "./icons.js";
import type {HardwareResource, SerialPort} from "./types.js";

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

function networkProtocolLabel(protocol: HardwareResource["network"]["protocol"]): string {
  return {telnet: "Telnet", raw: "Raw TCP", ssh: "SSH", adb: "ADB"}[protocol] || protocol;
}

function defaultNetworkPort(protocol: HardwareResource["network"]["protocol"]): number {
  if (protocol === "ssh") return 22;
  if (protocol === "adb") return 5555;
  return 23;
}

function endpointLabel(resource: HardwareResource): string {
  const endpoints: string[] = [];
  if (resource.serial.device) endpoints.push(resource.serial.device);
  if (resource.network.host) endpoints.push(`${resource.network.host}:${resource.network.port || defaultNetworkPort(resource.network.protocol)}`);
  return endpoints.join(" / ") || "未配置端点";
}

function recoverySecretPlaceholder(resource?: HardwareResource): string {
  return resource?.password_configured ? "已配置，留空保持不变" : "可选";
}

export function renderHardwareResources(resources: HardwareResource[]): string {
  const rows = resources.map(resource => {
    const adbPair = resource.network.protocol === "adb"
      ? `<button type="button" class="icon-button" data-hardware-adb-pair="${escapeHTML(resource.id)}" title="配对 ADB 设备" aria-label="配对 ADB 设备">${icon("link")}</button>`
      : "";
    const pairState = resource.network.protocol === "adb"
      ? `<span class="hardware-resource-pair-state ${resource.adb_paired_at ? "paired" : ""}">${resource.adb_paired_at ? "已配对" : "未配对"}</span>`
      : "";
    return `<article class="hardware-resource-row" data-hardware-resource="${escapeHTML(resource.id)}">
    <div class="hardware-resource-identity"><span class="hardware-resource-glyph">${icon("hardware")}</span><div><strong>${escapeHTML(resource.description || resource.id)}</strong><small>${escapeHTML(modeLabel(resource.mode))}${resource.mode === "network" ? ` · ${escapeHTML(networkProtocolLabel(resource.network.protocol))}` : ""} · ${escapeHTML(endpointLabel(resource))}</small></div></div>
    <div class="hardware-resource-meta">${pairState}<span>${escapeHTML(accessLabel(resource.access))}</span><span>${resource.password_configured ? "凭据已配置" : "无保存凭据"}</span><span>${resource.binding_count} 个任务启用</span></div>
    <div class="hardware-resource-actions">${adbPair}<button type="button" class="icon-button" data-hardware-resource-edit="${escapeHTML(resource.id)}" title="编辑硬件资源" aria-label="编辑硬件资源">${icon("edit")}</button><button type="button" class="icon-button danger" data-hardware-resource-delete="${escapeHTML(resource.id)}" title="删除硬件资源" aria-label="删除硬件资源">${icon("close")}</button></div>
  </article>`;
  }).join("");
  return `<section class="hardware-resources-view">
    <header class="page-head"><div><h1>硬件资源</h1><p>在这里维护系统级串口、网络设备和登录凭据。任务页只选择启用，不再重复创建硬件。</p></div><div class="actions"><button type="button" id="add-hardware-resource" class="primary">${icon("plus")}添加硬件资源</button></div></header>
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
    ["adb", "ADB"],
  ].map(([id, label]) => `<option value="${id}" ${value === id ? "selected" : ""}>${label}</option>`).join("");
}

function sshAuthOptions(value: string): string {
  return [
    ["auto", "自动（有密码时优先密码）"],
    ["password", "密码"],
    ["key", "Key (~/.ssh)"],
  ].map(([id, label]) => `<option value="${id}" ${value === id ? "selected" : ""}>${label}</option>`).join("");
}

function resourceDialog(resource?: HardwareResource, ports: SerialPort[] = []): HTMLDialogElement {
  const dialog = document.createElement("dialog");
  dialog.className = "wide hardware-resource-dialog";
  dialog.innerHTML = `<form id="hardware-resource-form">
    <div class="dialog-head"><h2>${resource ? "编辑硬件资源" : "添加硬件资源"}</h2><button type="button" data-close class="icon-button" aria-label="关闭">${icon("close")}</button></div>
    <label>名称<input name="description" maxlength="2000" required value="${escapeHTML(resource?.description || "")}" placeholder="例如：主控板串口"></label>
    <div class="two"><label>连接方式<select name="mode">${modeOptions(resource?.mode === "network" ? "network" : "serial")}</select></label><label>权限<select name="access">${accessOptions(resource?.access || "read_write")}</select></label></div>
    <section data-resource-config="serial">
      <div class="two"><label>串口设备<input name="serial_device" list="hardware-resource-ports" value="${escapeHTML(resource?.serial.device || "")}" placeholder="COM3 或 /dev/ttyUSB0"></label><label>波特率<input name="serial_baudrate" type="number" min="1" value="${Number(resource?.serial.baudrate || 115200)}"></label></div>
      <datalist id="hardware-resource-ports">${ports.map(port => `<option value="${escapeHTML(port.device)}">${escapeHTML(port.description || port.device)}</option>`).join("")}</datalist>
    </section>
    <section data-resource-config="network">
      <div class="two"><label>地址<input name="network_host" value="${escapeHTML(resource?.network.host || "")}" placeholder="192.168.1.20"></label><label>端口<input name="network_port" type="number" min="1" max="65535" value="${Number(resource?.network.port || defaultNetworkPort(resource?.network.protocol || "telnet"))}"></label></div>
      <div class="two"><label>协议<select name="network_protocol">${protocolOptions(resource?.network.protocol || "telnet")}</select></label><label data-resource-config="ssh">SSH 登录方式<select name="ssh_auth">${sshAuthOptions(resource?.network.ssh_auth || "auto")}</select></label></div>
      <p class="field-help" data-resource-config="adb">保存资源后使用资源行的配对按钮。配对端口和六位配对码由手机无线调试页同时生成，不会保存。</p>
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
      form.querySelectorAll<HTMLElement>('[data-resource-config="serial"]').forEach(element => { element.hidden = mode !== "serial" && mode !== "both"; });
      form.querySelectorAll<HTMLElement>('[data-resource-config="network"]').forEach(element => { element.hidden = mode !== "network" && mode !== "both"; });
      form.querySelectorAll<HTMLElement>('[data-resource-config="ssh"]').forEach(element => { element.hidden = (mode !== "network" && mode !== "both") || protocol !== "ssh"; });
      form.querySelectorAll<HTMLElement>('[data-resource-config="adb"]').forEach(element => { element.hidden = (mode !== "network" && mode !== "both") || protocol !== "adb"; });
      form.querySelectorAll<HTMLElement>('[data-resource-config="login"]').forEach(element => { element.hidden = (mode === "network" || mode === "both") && protocol === "adb"; });
    };
    form?.addEventListener("change", syncFields);
    form?.querySelector('select[name="network_protocol"]')?.addEventListener("change", event => {
      const protocol = String((event.currentTarget as HTMLSelectElement).value);
      const port = form?.querySelector<HTMLInputElement>('input[name="network_port"]');
      if (port && (!port.value || ["22", "23", "5555"].includes(port.value))) port.value = String(defaultNetworkPort(protocol as HardwareResource["network"]["protocol"]));
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
        const protocol = String(data.get("network_protocol") || "telnet");
        const adb = protocol === "adb";
        const payload = {
          description: String(data.get("description") || "").trim(),
          mode: String(data.get("mode") || "serial"),
          access: String(data.get("access") || "read_write"),
          serial: {
            device: String(data.get("serial_device") || "").trim(),
            baudrate: Number(data.get("serial_baudrate") || 115200),
          },
          network: {
            host: String(data.get("network_host") || "").trim(),
            port: Number(data.get("network_port") || defaultNetworkPort(protocol as HardwareResource["network"]["protocol"])),
            protocol,
            ssh_auth: String(data.get("ssh_auth") || "auto"),
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
    closeDialog();
    dialog = document.createElement("dialog");
    dialog.className = "hardware-pair-dialog";
    const connectionEndpoint = `${resource.network.host}:${resource.network.port || 5555}`;
    dialog.innerHTML = `<form id="adb-pair-form">
      <div class="dialog-head"><h2>配对 Android 设备</h2><button type="button" data-close class="icon-button" aria-label="关闭">${icon("close")}</button></div>
      <p class="field-help">连接端点：${escapeHTML(connectionEndpoint)}</p>
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
