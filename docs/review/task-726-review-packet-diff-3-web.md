# task-726 review packet diff-3-web：Web 生产代码与 build.test 断言/预算

`git diff 43624a4 336d47b -- web/src/hardware_resources.ts web/src/hardware_panel.ts web/src/main.ts web/src/types.ts web/src/api.ts web/src/i18n.ts web/src/styles.css web/tests/build.test.mjs` 原样输出。

~~~~diff
diff --git a/web/src/api.ts b/web/src/api.ts
index 8455e2d..160758a 100644
--- a/web/src/api.ts
+++ b/web/src/api.ts
@@ -97,6 +97,7 @@ import type {
   HardwareIOPurgeResult,
   HardwareIORetentionSettings,
   HardwareResource,
+  ADBUSBDevice,
   HardwareTerminalStatus,
   BrowserBookmark,
   BrowserProfile,
@@ -1055,6 +1056,10 @@ class APIClient {
     });
   }
 
+  adbUSBDevices(): Promise<{devices: ADBUSBDevice[]; other_count: number}> {
+    return this.request("/api/v1/hardware/adb/usb-devices", {method: "POST", body: "{}"});
+  }
+
   pairHardwareADB(id: string, pairingPort: number, pairingCode: string): Promise<{message: string; output: string; resource: HardwareResource}> {
     return this.request(`/api/v1/hardware-resources/${encodeURIComponent(id)}/adb/pair`, {
       method: "POST", body: JSON.stringify({pairing_port: pairingPort, pairing_code: pairingCode}),
diff --git a/web/src/hardware_panel.ts b/web/src/hardware_panel.ts
index 71f96ac..2d6ee2d 100644
--- a/web/src/hardware_panel.ts
+++ b/web/src/hardware_panel.ts
@@ -158,7 +158,19 @@ function supports(group: HardwareDraft | undefined, transport: Transport): boole
   if (transport === "serial") {
     return (group.mode === "serial" || group.mode === "both") && Boolean(group.serial.device);
   }
-  return (group.mode === "network" || group.mode === "both") && Boolean(group.network.host);
+  if (!(group.mode === "network" || group.mode === "both")) return false;
+  return adbUSB(group.network) ? Boolean(group.network.adb_serial) : Boolean(group.network.host);
+}
+
+// ADB USB direct is addressed by device serial only; a row without
+// adb_connection predates the split and is wireless debugging.
+function adbUSB(network: HardwareResource["network"]): boolean {
+  return network.protocol === "adb" && network.adb_connection === "usb";
+}
+
+function networkEndpointText(network: HardwareResource["network"]): string {
+  if (adbUSB(network)) return network.adb_serial ? `USB ${network.adb_serial}` : "";
+  return network.host ? `${network.host}:${network.port}` : "";
 }
 
 // Whenever the group list or the selection changes without the user picking a
@@ -260,6 +272,10 @@ function bytesToBase64(data: Uint8Array): string {
 }
 
 function networkProtocolLabel(resource: HardwareResource): string {
+  if (resource.network.protocol === "adb") {
+    if (adbUSB(resource.network)) return "ADB（USB 直连）";
+    return resource.network.adb_connection === "tcp" ? "ADB（网络直连）" : "ADB（无线调试）";
+  }
   return {telnet: "Telnet", raw: "Raw TCP", ssh: "SSH", adb: "ADB"}[resource.network.protocol] || resource.network.protocol;
 }
 
@@ -273,14 +289,18 @@ function resourceNetworkPort(resource: HardwareResource): number {
 function resourceLabel(resource: HardwareResource): string {
   const endpoints: string[] = [];
   if (resource.serial.device) endpoints.push(resource.serial.device);
-  if (resource.network.host) endpoints.push(`${networkProtocolLabel(resource)} ${resource.network.host}:${resourceNetworkPort(resource)}`);
+  if (adbUSB(resource.network)) {
+    if (resource.network.adb_serial) endpoints.push(`${networkProtocolLabel(resource)} ${resource.network.adb_serial}`);
+  } else if (resource.network.host) endpoints.push(`${networkProtocolLabel(resource)} ${resource.network.host}:${resourceNetworkPort(resource)}`);
   return `${resource.description || resource.id} · ${endpoints.join(" / ") || "未配置端点"}`;
 }
 
 function resourcePicker(state: PanelState): string {
   return state.resources.map(resource => {
     const binding = state.groups.find(group => (group.resource_id || group.id) === resource.id);
-    const resourceState = resource.network.protocol === "adb"
+    const resourceState = adbUSB(resource.network) || resource.network.adb_connection === "tcp"
+      ? ""
+      : resource.network.protocol === "adb"
       ? (resource.adb_paired_at ? " · 已配对" : " · 未配对")
       : (resource.password_configured ? " · 凭据已配置" : "");
     return `<article class="hardware-resource-choice ${binding ? "enabled" : ""} ${binding?.id === state.selectedID ? "selected" : ""}">
@@ -324,7 +344,7 @@ export function renderHardwarePanel(detail: TaskDetail): string {
       <header class="hardware-console-toolbar">
         <span class="hardware-transport-label">${serialSelected ? "串口" : "网络"}</span>
         <span id="hardware-status" class="hardware-status ${statusClass(stream.status)}">${statusText(stream.status)}</span>
-        <span id="hardware-endpoint">${escapeHTML(stream.status?.endpoint || (serialSelected ? group?.serial.device : group ? `${group.network.host}:${group.network.port}` : "") || "-")}</span>
+        <span id="hardware-endpoint">${escapeHTML(stream.status?.endpoint || (serialSelected ? group?.serial.device : group ? networkEndpointText(group.network) : "") || "-")}</span>
         <div class="hardware-console-actions">
           <button type="button" id="hardware-connect" ${canConnect && !connected ? "" : "disabled"}>连接</button>
           <button type="button" id="hardware-disconnect" ${connected ? "" : "disabled"}>断开</button>
diff --git a/web/src/hardware_resources.ts b/web/src/hardware_resources.ts
index 275656c..fda2961 100644
--- a/web/src/hardware_resources.ts
+++ b/web/src/hardware_resources.ts
@@ -1,6 +1,6 @@
 import {api} from "./api.js";
 import {icon} from "./icons.js";
-import type {HardwareResource, SerialPort} from "./types.js";
+import type {ADBUSBDevice, HardwareResource, SerialPort} from "./types.js";
 
 type Notice = (type: "error" | "notice", message: string) => void;
 
@@ -18,8 +18,51 @@ function accessLabel(access: HardwareResource["access"]): string {
   return access === "read_only" ? "只读" : "读写";
 }
 
-function networkProtocolLabel(protocol: HardwareResource["network"]["protocol"]): string {
-  return {telnet: "Telnet", raw: "Raw TCP", ssh: "SSH", adb: "ADB"}[protocol] || protocol;
+type NetworkConfig = HardwareResource["network"];
+
+// ADB has three connection forms under one protocol. A row without
+// adb_connection predates the split and is wireless debugging.
+function adbUSB(network: NetworkConfig): boolean {
+  return network.protocol === "adb" && network.adb_connection === "usb";
+}
+
+function adbTCP(network: NetworkConfig): boolean {
+  return network.protocol === "adb" && network.adb_connection === "tcp";
+}
+
+function adbWireless(network: NetworkConfig): boolean {
+  return network.protocol === "adb" && network.adb_connection !== "usb" && network.adb_connection !== "tcp";
+}
+
+function networkProtocolLabel(network: NetworkConfig): string {
+  if (adbUSB(network)) return "ADB（USB 直连）";
+  if (adbTCP(network)) return "ADB（网络直连）";
+  if (adbWireless(network)) return "ADB（无线调试）";
+  return {telnet: "Telnet", raw: "Raw TCP", ssh: "SSH", adb: "ADB"}[network.protocol] || network.protocol;
+}
+
+// The protocol select carries the ADB connection form in its value so the
+// Owner keeps choosing it in the same place as before.
+type ProtocolChoice = "telnet" | "raw" | "ssh" | "adb" | "adb_tcp" | "adb_usb";
+
+function protocolChoice(network: NetworkConfig | null): ProtocolChoice {
+  if (!network) return "telnet";
+  if (adbUSB(network)) return "adb_usb";
+  if (adbTCP(network)) return "adb_tcp";
+  return network.protocol;
+}
+
+const adbUSBStateLabels: Record<string, string> = {
+  device: "已授权",
+  unauthorized: "未授权，请在设备上允许 USB 调试",
+  offline: "离线",
+  no_permissions: "宿主机无 USB 访问权限",
+};
+
+function adbUSBDeviceLabel(device: ADBUSBDevice): string {
+  const name = device.model || device.product || device.device || "";
+  const state = adbUSBStateLabels[device.state] || device.state;
+  return `${device.serial}${name ? ` · ${name.replaceAll("_", " ")}` : ""} · ${state}${device.selectable ? "" : " · 不可选择"}`;
 }
 
 function defaultNetworkPort(protocol: HardwareResource["network"]["protocol"]): number {
@@ -45,7 +88,9 @@ function endpointLabel(resource: HardwareResource): string {
   const network = networkConfig(resource);
   const endpoints: string[] = [];
   if (serial.device) endpoints.push(serial.device);
-  if (network.host) endpoints.push(`${network.host}:${network.port || defaultNetworkPort(network.protocol)}`);
+  if (adbUSB(network)) {
+    if (network.adb_serial) endpoints.push(`USB ${network.adb_serial}`);
+  } else if (network.host) endpoints.push(`${network.host}:${network.port || defaultNetworkPort(network.protocol)}`);
   return endpoints.join(" / ") || "未配置端点";
 }
 
@@ -56,14 +101,14 @@ function recoverySecretPlaceholder(resource?: HardwareResource): string {
 export function renderHardwareResources(resources: HardwareResource[]): string {
   const rows = resources.map(resource => {
     const network = networkConfig(resource);
-    const adbPair = network.protocol === "adb"
+    const adbPair = adbWireless(network)
       ? `<button type="button" class="icon-button" data-hardware-adb-pair="${escapeHTML(resource.id)}" title="配对 ADB 设备" aria-label="配对 ADB 设备">${icon("link")}</button>`
       : "";
-    const pairState = network.protocol === "adb"
+    const pairState = adbWireless(network)
       ? `<span class="hardware-resource-pair-state ${resource.adb_paired_at ? "paired" : ""}">${resource.adb_paired_at ? "已配对" : "未配对"}</span>`
       : "";
     return `<article class="hardware-resource-row" data-hardware-resource="${escapeHTML(resource.id)}">
-    <div class="hardware-resource-identity"><span class="hardware-resource-glyph">${icon("hardware")}</span><div><strong>${escapeHTML(resource.description || resource.id)}</strong><small>${escapeHTML(modeLabel(resource.mode))}${resource.mode === "network" ? ` · ${escapeHTML(networkProtocolLabel(network.protocol))}` : ""} · ${escapeHTML(endpointLabel(resource))}</small></div></div>
+    <div class="hardware-resource-identity"><span class="hardware-resource-glyph">${icon("hardware")}</span><div><strong>${escapeHTML(resource.description || resource.id)}</strong><small>${escapeHTML(modeLabel(resource.mode))}${resource.mode === "network" ? ` · ${escapeHTML(networkProtocolLabel(network))}` : ""} · ${escapeHTML(endpointLabel(resource))}</small></div></div>
     <div class="hardware-resource-meta">${pairState}<span>${escapeHTML(accessLabel(resource.access))}</span><span>${resource.password_configured ? "凭据已配置" : "无保存凭据"}</span><span>${resource.binding_count} 个任务启用</span></div>
     <div class="hardware-resource-actions">${adbPair}<button type="button" class="icon-button" data-hardware-resource-edit="${escapeHTML(resource.id)}" title="编辑硬件资源" aria-label="编辑硬件资源">${icon("edit")}</button><button type="button" class="icon-button danger" data-hardware-resource-delete="${escapeHTML(resource.id)}" title="删除硬件资源" aria-label="删除硬件资源">${icon("close")}</button></div>
   </article>`;
@@ -93,7 +138,9 @@ function protocolOptions(value: string): string {
     ["telnet", "Telnet"],
     ["raw", "Raw TCP"],
     ["ssh", "SSH"],
-    ["adb", "ADB"],
+    ["adb", "ADB（无线调试 / 配对码）"],
+    ["adb_tcp", "ADB（网络直连 / IP:端口，免配对）"],
+    ["adb_usb", "ADB（USB 直连）"],
   ].map(([id, label]) => `<option value="${id}" ${value === id ? "selected" : ""}>${label}</option>`).join("");
 }
 
@@ -105,6 +152,23 @@ function sshAuthOptions(value: string): string {
   ].map(([id, label]) => `<option value="${id}" ${value === id ? "selected" : ""}>${label}</option>`).join("");
 }
 
+// The USB select never preselects a detected device: with several devices
+// attached, a default could silently point the resource at the wrong one.
+// A saved serial stays listed even when it is not attached right now.
+function adbUSBOptions(devices: ADBUSBDevice[], saved: string, loaded: boolean): string {
+  const options = [`<option value="" ${saved ? "" : "selected"}>${loaded && !devices.length ? "未检测到 USB 设备" : "请选择 USB 设备"}</option>`];
+  let savedListed = false;
+  for (const device of devices) {
+    if (device.serial === saved) savedListed = true;
+    const selected = device.serial === saved && device.selectable;
+    options.push(`<option value="${escapeHTML(device.serial)}" ${selected ? "selected" : ""} ${device.selectable ? "" : "disabled"}>${escapeHTML(adbUSBDeviceLabel(device))}</option>`);
+  }
+  if (saved && !savedListed) {
+    options.push(`<option value="${escapeHTML(saved)}" selected>${escapeHTML(saved)} · ${loaded ? "当前未检测到" : "已保存"}</option>`);
+  }
+  return options.join("");
+}
+
 function resourceDialog(resource?: HardwareResource, ports: SerialPort[] = []): HTMLDialogElement {
   const serial = resource ? serialConfig(resource) : null;
   const network = resource ? networkConfig(resource) : null;
@@ -119,9 +183,15 @@ function resourceDialog(resource?: HardwareResource, ports: SerialPort[] = []):
       <datalist id="hardware-resource-ports">${ports.map(port => `<option value="${escapeHTML(port.device)}">${escapeHTML(port.description || port.device)}</option>`).join("")}</datalist>
     </section>
     <section data-resource-config="network">
-      <div class="two"><label>地址<input name="network_host" value="${escapeHTML(network?.host || "")}" placeholder="192.168.1.20"></label><label>端口<input name="network_port" type="number" min="1" max="65535" value="${Number(network?.port || defaultNetworkPort(network?.protocol || "telnet"))}"></label></div>
-      <div class="two"><label>协议<select name="network_protocol">${protocolOptions(network?.protocol || "telnet")}</select></label><label data-resource-config="ssh">SSH 登录方式<select name="ssh_auth">${sshAuthOptions(network?.ssh_auth || "auto")}</select></label></div>
-      <p class="field-help" data-resource-config="adb">保存资源后使用资源行的配对按钮。配对端口和六位配对码由手机无线调试页同时生成，不会保存。</p>
+      <div class="two"><label>协议<select name="network_protocol">${protocolOptions(protocolChoice(network))}</select></label><label data-resource-config="ssh">SSH 登录方式<select name="ssh_auth">${sshAuthOptions(network?.ssh_auth || "auto")}</select></label></div>
+      <div class="two" data-resource-config="network-endpoint"><label>地址<input name="network_host" value="${escapeHTML(network && !adbUSB(network) ? network.host : "")}" placeholder="192.168.1.20"></label><label><span data-resource-port-label>端口</span><input name="network_port" type="number" min="1" max="65535" value="${Number((network && !adbUSB(network) ? network.port : 0) || defaultNetworkPort(network?.protocol || "telnet"))}"></label></div>
+      <p class="field-help" data-resource-config="adb">地址和端口填手机「无线调试」页显示的 IP 地址和端口（调试连接端口）。保存后用资源行的配对按钮配对：配对端口和六位配对码在手机「使用配对码配对设备」里另外生成，和连接端口不同，也不会保存。</p>
+      <p class="field-help" data-resource-config="adb-tcp">设备需已开启网络 ADB（例如之前用数据线执行过 adb tcpip 5555，或设备自带的网络调试）。AHA 直接连接这里填写的 IP:端口，默认 5555，不需要配对码，也不会按 mDNS 换成别的端口。首次连接时请在设备上确认「允许调试」。</p>
+      <div data-resource-config="adb-usb">
+        <div class="hardware-adb-usb-row"><label>USB 设备<select name="adb_serial" data-adb-usb-select>${adbUSBOptions([], network && adbUSB(network) ? network.adb_serial || "" : "", false)}</select></label><button type="button" data-adb-usb-refresh>${icon("refresh")}<span>刷新设备</span></button></div>
+        <p class="field-help" data-adb-usb-status>设备列表来自运行 AHA 的电脑。用数据线连接设备，并在设备的开发者选项里开启「USB 调试」。</p>
+        <p class="field-help">USB 直连不需要 IP、端口或配对码。首次连接时设备会弹出「允许 USB 调试」，请在设备上确认。</p>
+      </div>
     </section>
     <section data-resource-config="login">
       <div class="two"><label>登录用户名<input name="username" autocomplete="off" value="${escapeHTML(resource?.username || "")}"></label><label>登录密码<input name="password" type="password" autocomplete="new-password" placeholder="${recoverySecretPlaceholder(resource)}"></label></div>
@@ -159,17 +229,56 @@ export function bindHardwareResources(
       if (!form) return;
       const mode = String(new FormData(form).get("mode") || "serial");
       const protocol = String(new FormData(form).get("network_protocol") || "telnet");
+      const network = mode === "network" || mode === "both";
+      const anyADB = protocol === "adb" || protocol === "adb_tcp" || protocol === "adb_usb";
       form.querySelectorAll<HTMLElement>('[data-resource-config="serial"]').forEach(element => { element.hidden = mode !== "serial" && mode !== "both"; });
-      form.querySelectorAll<HTMLElement>('[data-resource-config="network"]').forEach(element => { element.hidden = mode !== "network" && mode !== "both"; });
-      form.querySelectorAll<HTMLElement>('[data-resource-config="ssh"]').forEach(element => { element.hidden = (mode !== "network" && mode !== "both") || protocol !== "ssh"; });
-      form.querySelectorAll<HTMLElement>('[data-resource-config="adb"]').forEach(element => { element.hidden = (mode !== "network" && mode !== "both") || protocol !== "adb"; });
-      form.querySelectorAll<HTMLElement>('[data-resource-config="login"]').forEach(element => { element.hidden = (mode === "network" || mode === "both") && protocol === "adb"; });
+      form.querySelectorAll<HTMLElement>('[data-resource-config="network"]').forEach(element => { element.hidden = !network; });
+      form.querySelectorAll<HTMLElement>('[data-resource-config="network-endpoint"]').forEach(element => { element.hidden = !network || protocol === "adb_usb"; });
+      form.querySelectorAll<HTMLElement>('[data-resource-config="ssh"]').forEach(element => { element.hidden = !network || protocol !== "ssh"; });
+      form.querySelectorAll<HTMLElement>('[data-resource-config="adb"]').forEach(element => { element.hidden = !network || protocol !== "adb"; });
+      form.querySelectorAll<HTMLElement>('[data-resource-config="adb-tcp"]').forEach(element => { element.hidden = !network || protocol !== "adb_tcp"; });
+      form.querySelectorAll<HTMLElement>('[data-resource-config="adb-usb"]').forEach(element => { element.hidden = !network || protocol !== "adb_usb"; });
+      form.querySelectorAll<HTMLElement>('[data-resource-config="login"]').forEach(element => { element.hidden = network && anyADB; });
+      const portLabel = form.querySelector<HTMLElement>("[data-resource-port-label]");
+      if (portLabel) portLabel.textContent = protocol === "adb" ? "调试连接端口" : "端口";
+      const usbSelect = form.querySelector<HTMLSelectElement>("[data-adb-usb-select]");
+      if (usbSelect) usbSelect.required = network && protocol === "adb_usb";
+      if (network && protocol === "adb_usb" && !usbLoaded && !usbLoading) void loadUSBDevices();
+    };
+    let usbLoaded = false;
+    let usbLoading = false;
+    const loadUSBDevices = async () => {
+      const select = form?.querySelector<HTMLSelectElement>("[data-adb-usb-select]");
+      const status = form?.querySelector<HTMLElement>("[data-adb-usb-status]");
+      const refresh = form?.querySelector<HTMLButtonElement>("[data-adb-usb-refresh]");
+      if (!select || !status) return;
+      usbLoading = true;
+      if (refresh) refresh.disabled = true;
+      status.textContent = "正在读取 AHA 宿主机上的 USB 设备…";
+      try {
+        const result = await api.adbUSBDevices();
+        if (!select.isConnected) return;
+        const devices = result.devices || [];
+        select.innerHTML = adbUSBOptions(devices, select.value, true);
+        usbLoaded = true;
+        const other = result.other_count ? `另有 ${result.other_count} 个网络或模拟器设备未列出。` : "";
+        if (!devices.length) status.textContent = `没有检测到 USB 设备。请用数据线连接设备并开启「USB 调试」后刷新。${other}`;
+        else if (devices.length > 1) status.textContent = `检测到 ${devices.length} 台 USB 设备，请按序列号选择要用的那一台。${other}`;
+        else status.textContent = `检测到 1 台 USB 设备，请确认序列号后选择。${other}`;
+      } catch (error) {
+        if (select.isConnected) status.textContent = error instanceof Error ? error.message : String(error);
+      } finally {
+        usbLoading = false;
+        if (refresh?.isConnected) refresh.disabled = false;
+      }
     };
+    form?.querySelector("[data-adb-usb-refresh]")?.addEventListener("click", () => void loadUSBDevices());
     form?.addEventListener("change", syncFields);
     form?.querySelector('select[name="network_protocol"]')?.addEventListener("change", event => {
-      const protocol = String((event.currentTarget as HTMLSelectElement).value);
+      const choice = String((event.currentTarget as HTMLSelectElement).value);
+      const protocol = (choice === "adb_usb" || choice === "adb_tcp" ? "adb" : choice) as NetworkConfig["protocol"];
       const port = form?.querySelector<HTMLInputElement>('input[name="network_port"]');
-      if (port && (!port.value || ["22", "23", "5555"].includes(port.value))) port.value = String(defaultNetworkPort(protocol as HardwareResource["network"]["protocol"]));
+      if (choice !== "adb_usb" && port && (!port.value || ["22", "23", "5555"].includes(port.value))) port.value = String(defaultNetworkPort(protocol));
       syncFields();
     });
     syncFields();
@@ -185,7 +294,10 @@ export function bindHardwareResources(
       }
       void (async () => {
         const data = new FormData(form);
-        const protocol = String(data.get("network_protocol") || "telnet");
+        const choice = String(data.get("network_protocol") || "telnet");
+        const usb = choice === "adb_usb";
+        const tcp = choice === "adb_tcp";
+        const protocol = usb || tcp ? "adb" : choice;
         const adb = protocol === "adb";
         const payload = {
           description: String(data.get("description") || "").trim(),
@@ -195,11 +307,20 @@ export function bindHardwareResources(
             device: String(data.get("serial_device") || "").trim(),
             baudrate: Number(data.get("serial_baudrate") || 115200),
           },
-          network: {
+          // USB direct sends only the serial: no host, port or pairing data.
+          network: usb ? {
+            host: "",
+            port: 0,
+            protocol,
+            ssh_auth: "auto",
+            adb_connection: "usb",
+            adb_serial: String(data.get("adb_serial") || "").trim(),
+          } : {
             host: String(data.get("network_host") || "").trim(),
-            port: Number(data.get("network_port") || defaultNetworkPort(protocol as HardwareResource["network"]["protocol"])),
+            port: Number(data.get("network_port") || defaultNetworkPort(protocol as NetworkConfig["protocol"])),
             protocol,
             ssh_auth: String(data.get("ssh_auth") || "auto"),
+            ...(adb ? {adb_connection: tcp ? "tcp" : "wireless"} : {}),
           },
           username: adb ? "" : String(data.get("username") || "").trim(),
           password: adb ? "" : String(data.get("password") || ""),
@@ -221,6 +342,9 @@ export function bindHardwareResources(
   };
 
   const openADBPairDialog = (resource: HardwareResource) => {
+    // Pairing codes belong to Android wireless debugging only; USB direct
+    // relies on the device's own "allow USB debugging" prompt.
+    if (!adbWireless(networkConfig(resource))) return;
     closeDialog();
     dialog = document.createElement("dialog");
     dialog.className = "hardware-pair-dialog";
@@ -228,7 +352,7 @@ export function bindHardwareResources(
     const connectionEndpoint = `${network.host}:${network.port || 5555}`;
     dialog.innerHTML = `<form id="adb-pair-form">
       <div class="dialog-head"><h2>配对 Android 设备</h2><button type="button" data-close class="icon-button" aria-label="关闭">${icon("close")}</button></div>
-      <p class="field-help">连接端点：${escapeHTML(connectionEndpoint)}</p>
+      <p class="field-help">调试连接端点：${escapeHTML(connectionEndpoint)}。在手机「无线调试 → 使用配对码配对设备」里查看配对端口和配对码，配对端口和连接端口不同。</p>
       <div class="two"><label>配对端口<input name="pairing_port" type="number" min="1" max="65535" inputmode="numeric" placeholder="手机显示的端口" required></label><label>6 位配对码<input name="pairing_code" type="password" inputmode="numeric" autocomplete="one-time-code" minlength="6" maxlength="6" pattern="[0-9]{6}" required></label></div>
       <div class="dialog-actions"><button type="button" data-close>取消</button><button type="submit" class="primary">开始配对</button></div>
     </form>`;
diff --git a/web/src/i18n.ts b/web/src/i18n.ts
index 7baa09c..a52bf95 100644
--- a/web/src/i18n.ts
+++ b/web/src/i18n.ts
@@ -1948,6 +1948,16 @@ const legacyEnglish: Record<string, string> = {
   "无保存凭据": "No saved credentials",
   "已配对": "Paired",
   "未配对": "Not paired",
+  "ADB（无线调试 / 配对码）": "ADB (wireless debugging / pairing code)",
+  "ADB（网络直连 / IP:端口，免配对）": "ADB (network direct / IP:port, no pairing)",
+  "ADB（USB 直连）": "ADB (USB direct)",
+  "ADB（无线调试）": "ADB (wireless debugging)",
+  "ADB（网络直连）": "ADB (network direct)",
+  "USB 设备": "USB device",
+  "刷新设备": "Refresh devices",
+  "调试连接端口": "Debugging connection port",
+  "请选择 USB 设备": "Select a USB device",
+  "未检测到 USB 设备": "No USB device detected",
   "硬件资源已保存": "Hardware resource saved",
   "硬件资源已添加": "Hardware resource added",
   "硬件资源已删除": "Hardware resource deleted",
diff --git a/web/src/main.ts b/web/src/main.ts
index 6870663..19f2d3f 100644
--- a/web/src/main.ts
+++ b/web/src/main.ts
@@ -5689,7 +5689,7 @@ function taskTakeoverDialog(detail: TaskDetail): string {
   if (!detail.task.read_only) return "";
   const workspaces = state.workspaces.filter(item => item.project_id === detail.task.project_id && !item.read_only && Boolean(item.root_path));
   const hardware = detail.hardware || [];
-  const groups = hardware.map(group => `<fieldset class="takeover-hardware" data-hardware-id="${escapeHTML(group.id)}"><legend>${escapeHTML(group.description || group.id)}</legend><input type="hidden" data-field="id" value="${escapeHTML(group.id)}"><input type="hidden" data-field="description" value="${escapeHTML(group.description || "")}"><input type="hidden" data-field="mode" value="${escapeHTML(group.mode)}"><div class="two"><label>本机串口<input data-field="serial-device" placeholder="${escapeHTML(group.serial?.device || "无需串口")}" ${group.serial?.device ? "required" : ""}></label><label>Baudrate<input data-field="serial-baudrate" type="number" value="${Number(group.serial?.baudrate || 115200)}"></label></div><div class="two"><label>本机网络地址<input data-field="network-host" placeholder="${escapeHTML(group.network?.host || "无需网络")}" ${group.network?.host ? "required" : ""}></label><label>端口<input data-field="network-port" type="number" value="${Number(group.network?.port || 22)}"></label></div><input type="hidden" data-field="network-protocol" value="${escapeHTML(group.network?.protocol || "raw")}"><input type="hidden" data-field="network-ssh-auth" value="${escapeHTML(group.network?.ssh_auth || "auto")}"><label>用户名<input data-field="username" value="${escapeHTML(group.username || "")}"></label>${group.password_configured ? `<label class="reuse-remote-secret"><input data-field="reuse-remote-credential" type="checkbox" checked>复用已同步的端到端加密凭据</label>` : ""}<label>${group.password_configured ? "或输入新密码" : "密码（可选）"}<input data-field="password" type="password" autocomplete="new-password"></label></fieldset>`).join("");
+  const groups = hardware.map(group => `<fieldset class="takeover-hardware" data-hardware-id="${escapeHTML(group.id)}"><legend>${escapeHTML(group.description || group.id)}</legend><input type="hidden" data-field="id" value="${escapeHTML(group.id)}"><input type="hidden" data-field="description" value="${escapeHTML(group.description || "")}"><input type="hidden" data-field="mode" value="${escapeHTML(group.mode)}"><div class="two"><label>本机串口<input data-field="serial-device" placeholder="${escapeHTML(group.serial?.device || "无需串口")}" ${group.serial?.device ? "required" : ""}></label><label>Baudrate<input data-field="serial-baudrate" type="number" value="${Number(group.serial?.baudrate || 115200)}"></label></div>${group.network?.protocol === "adb" && group.network?.adb_connection === "usb" ? `<label>本机 USB 设备序列号<input data-field="network-adb-serial" placeholder="${escapeHTML(group.network?.adb_serial || "")}" required></label><input type="hidden" data-field="network-adb-connection" value="usb">` : `<div class="two"><label>本机网络地址<input data-field="network-host" placeholder="${escapeHTML(group.network?.host || "无需网络")}" ${group.network?.host ? "required" : ""}></label><label>端口<input data-field="network-port" type="number" value="${Number(group.network?.port || 22)}"></label></div><input type="hidden" data-field="network-adb-connection" value="${group.network?.protocol === "adb" ? (group.network?.adb_connection === "tcp" ? "tcp" : "wireless") : ""}">`}<input type="hidden" data-field="network-protocol" value="${escapeHTML(group.network?.protocol || "raw")}"><input type="hidden" data-field="network-ssh-auth" value="${escapeHTML(group.network?.ssh_auth || "auto")}"><label>用户名<input data-field="username" value="${escapeHTML(group.username || "")}"></label>${group.password_configured ? `<label class="reuse-remote-secret"><input data-field="reuse-remote-credential" type="checkbox" checked>复用已同步的端到端加密凭据</label>` : ""}<label>${group.password_configured ? "或输入新密码" : "密码（可选）"}<input data-field="password" type="password" autocomplete="new-password"></label></fieldset>`).join("");
   return `<dialog id="task-takeover-dialog" class="wide"><form id="task-takeover-form"><div class="dialog-head"><h2>接管只读 Task</h2><button type="button" data-close class="icon-button" aria-label="${t("common.closeDialog")}">${icon("close")}</button></div><p class="field-help">接管会创建新的本机 Task。请重新选择本机 Workspace，并确认所有设备专属路径和端点。</p><label>标题<input name="title" required value="${escapeHTML(detail.task.title)} (本机)"></label><label>需求<textarea name="request" required>${escapeHTML(detail.task.original_request || detail.task.current_goal || detail.task.title)}</textarea></label><label>本机 Workspace<select name="workspace_id" id="takeover-task-workspace" required>${workspaces.map(item => `<option value="${item.id}">${escapeHTML(item.name)} · ${escapeHTML(item.root_path)}</option>`).join("") || '<option value="">请先为此项目创建本机 Workspace</option>'}</select></label>${runtimeFieldsHTML("takeover-task", state.models, state.codexAccounts)}<div class="two"><label>推理强度<select name="reasoning_effort" id="takeover-task-effort"></select></label><label>文件访问<select name="filesystem"><option value="workspace-write">工作区可写</option><option value="read-only">只读</option><option value="danger-full-access">完全访问</option></select></label></div><div class="two"><label>协作模式<select name="collaboration_mode"><option value="auto" ${detail.task.collaboration_mode === "auto" ? "selected" : ""}>Auto</option><option value="single" ${detail.task.collaboration_mode === "single" ? "selected" : ""}>Single</option></select></label><label>最大 Agent 数<input name="max_agents" type="number" min="1" value="${Number(detail.task.max_agents || 3)}"></label></div><label>审批<select name="approval"><option value="never">无需确认</option><option value="auto">自动批准</option></select></label>${groups ? `<section><h3>本机硬件重绑定</h3>${groups}</section>` : ""}<div class="dialog-actions"><button type="button" data-close>取消</button><button class="primary" type="submit" ${workspaces.length ? "" : "disabled"}>创建本机 Task</button></div></form></dialog>`;
 }
 
@@ -8062,7 +8062,7 @@ function bindCommon(): void {
       return {
         id: value("id"), description: value("description"), mode: value("mode"),
         serial: {device: value("serial-device"), baudrate: Number(value("serial-baudrate") || 115200)},
-        network: {host: value("network-host"), port: Number(value("network-port") || 0), protocol: value("network-protocol"), ssh_auth: value("network-ssh-auth")},
+        network: {host: value("network-host"), port: Number(value("network-port") || 0), protocol: value("network-protocol"), ssh_auth: value("network-ssh-auth"), adb_connection: value("network-adb-connection"), adb_serial: value("network-adb-serial")},
         username: value("username"), password: value("password"), reuse_remote_credential: checked("reuse-remote-credential"), access: "read_write",
       };
     });
diff --git a/web/src/styles.css b/web/src/styles.css
index 193af10..d53bd4c 100644
--- a/web/src/styles.css
+++ b/web/src/styles.css
@@ -1511,6 +1511,9 @@ dialog.agent-config-dialog { width: min(760px,calc(100vw - 32px)); }
 .detect-status { margin-top: 10px; color: var(--muted); font-size: 11px; }
 .two { display: grid; grid-template-columns: repeat(2,minmax(0,1fr)); gap: 12px; }
 .hardware-resource-dialog [hidden] { display: none !important; }
+.hardware-adb-usb-row { display: flex; gap: 8px; align-items: flex-end; }
+.hardware-adb-usb-row label { flex: 1; min-width: 0; }
+.hardware-adb-usb-row button { flex: 0 0 auto; display: inline-flex; gap: 6px; align-items: center; }
 .runtime-picker[hidden] { display: none !important; }
 .mobile-only { display: none; }
 
diff --git a/web/src/types.ts b/web/src/types.ts
index dbb46f0..e6b85a1 100644
--- a/web/src/types.ts
+++ b/web/src/types.ts
@@ -844,6 +844,20 @@ export interface HardwareNetworkConfig {
   port: number;
   protocol: "telnet" | "raw" | "ssh" | "adb";
   ssh_auth: "auto" | "password" | "key";
+  /** ADB only: wireless debugging (host + connection port, pairing code), tcp network direct (host:port, no pairing) or USB direct (serial). Missing means wireless. */
+  adb_connection?: "wireless" | "tcp" | "usb";
+  /** ADB USB direct only: device serial on the AHA host. */
+  adb_serial?: string;
+}
+
+export interface ADBUSBDevice {
+  serial: string;
+  state: string;
+  model?: string;
+  product?: string;
+  device?: string;
+  usb?: string;
+  selectable: boolean;
 }
 
 export interface HardwareGroup {
@@ -871,6 +885,7 @@ export interface HardwareResource {
   network: HardwareNetworkConfig;
   username?: string;
   password_configured: boolean;
+  adb_paired_at?: string;
   access: "read_only" | "read_write";
   binding_count: number;
   created_at: string;
diff --git a/web/tests/build.test.mjs b/web/tests/build.test.mjs
index 7109ce0..1ead8bb 100644
--- a/web/tests/build.test.mjs
+++ b/web/tests/build.test.mjs
@@ -1799,7 +1799,11 @@ test("built web contains responsive application", async () => {
   assert.match(mailTask, /mailCanCancel/);
   assert.match(hardwarePanel, /\.\/api\.js\?v=[a-f0-9]{12}/);
   assert.match(hardwareResources, /\.\/api\.js\?v=[a-f0-9]{12}/);
-  assert.match(hardwareResources, /["']adb["'],\s*["']ADB["']/);
+  // ADB is split into three connection forms chosen in the same protocol select.
+  assert.match(hardwareResources, /["']adb["'],\s*["']ADB（无线调试 \/ 配对码）["']/);
+  assert.match(hardwareResources, /["']adb_tcp["'],\s*["']ADB（网络直连 \/ IP:端口，免配对）["']/);
+  assert.match(hardwareResources, /["']adb_usb["'],\s*["']ADB（USB 直连）["']/);
+  assert.match(hardwareResources, /\/api\/v1\/hardware\/adb\/usb-devices|adbUSBDevices/);
   assert.match(hardwareResources, /protocol === "adb"\) return 5555/);
   assert.match(hardwarePanel, /\.\/hardware_terminal\.js\?v=[a-f0-9]{12}/);
   assert.match(agents, /\.\/icons\.js\?v=[a-f0-9]{12}/);
@@ -3884,7 +3888,12 @@ test("production web splits route chunks and keeps the first-screen budget", asy
   // data-retention copy (Chinese and English) plus its render function to the
   // shared first-screen bundle; the packaging run measured 272,805 (272,808 in
   // this worktree), so the ceiling moves to the next 256-byte boundary.
-  const FIRST_SCREEN_GZIP_BUDGET = 272896;
+  // task-726 split ADB into USB direct / wireless debugging / network direct:
+  // the Task hardware panel, the takeover form and the short en-US labels in
+  // the shared bundle gained the per-form labels, endpoints and fields (+480
+  // JS, +41 CSS over main's 272,829); measured 273,350, next 256-byte boundary.
+  // ADB 三模式新增 UI，实测 273,350，owner 侧分身上限 +512 字节（272,896 → 273,408）。
+  const FIRST_SCREEN_GZIP_BUDGET = 273408;
   // The version query parameter is a content hash and its hex digits change
   // how the same code gzips: main already sits exactly on the budget, so a
   // hash-only change could trip it without growing the payload. Mask the
~~~~
