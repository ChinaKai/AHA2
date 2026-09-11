import {api} from "./api.js";
import {icon} from "./icons.js";
import type {ManagedProxyProfile, ManagedProxyView, ProxySettings} from "./types.js";

function escapeHTML(value: unknown): string {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

function option(value: string, label: string, selected: boolean): string {
  return `<option value="${escapeHTML(value)}"${selected ? " selected" : ""}>${escapeHTML(label)}</option>`;
}

function protocolLabel(protocol: "hysteria2" | "vless"): string {
  return protocol === "vless" ? "VLESS Reality" : "Hysteria2";
}

function formatUpdatedAt(value?: string): string {
  if (!value) return "尚未更新";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "更新时间未知" : date.toLocaleString();
}

function formSettings(base: ProxySettings): ProxySettings {
  const mode = document.querySelector<HTMLSelectElement>("#proxy-mode")?.value || "external";
  return {
    ...base,
    mode: mode as ProxySettings["mode"],
    http_proxy: document.querySelector<HTMLInputElement>("#proxy-http")?.value.trim() || "",
    https_proxy: document.querySelector<HTMLInputElement>("#proxy-https")?.value.trim() || "",
    no_proxy: document.querySelector<HTMLInputElement>("#proxy-bypass")?.value.trim() || "",
  };
}

async function pendingSubscription(): Promise<{name?: string; subscription_url?: string; subscription_yaml?: string; refresh_interval_minutes: number} | null> {
  const sourceURL = document.querySelector<HTMLInputElement>("#proxy-subscription-url")?.value.trim() || "";
  const file = document.querySelector<HTMLInputElement>("#proxy-subscription-file")?.files?.[0];
  if (!sourceURL && !file) return null;
  return {
    name: document.querySelector<HTMLInputElement>("#proxy-profile-name")?.value.trim() || undefined,
    subscription_url: sourceURL || undefined,
    subscription_yaml: file ? await file.text() : undefined,
    refresh_interval_minutes: Number(document.querySelector<HTMLInputElement>("#proxy-new-refresh-interval")?.value || 1440),
  };
}

async function saveProxySettings(base: ProxySettings): Promise<{proxy: ProxySettings; managed?: ManagedProxyView}> {
  const saved = await api.updateProxySettings(formSettings(base));
  const view = await api.proxySettings();
  return {proxy: saved.proxy, managed: view.managed};
}

function profileCards(profiles: ManagedProxyProfile[]): string {
  if (!profiles.length) return '<p class="field-help">尚未添加代理配置。</p>';
  return profiles.map(profile => {
    const selectedDiffers = Boolean(profile.active && profile.selected_node_id && profile.selected_node_id !== profile.active_node_id);
    const needsSelection = profile.needs_selection || (profile.nodes.length > 0 && !profile.selected_node_id);
    const activeNodeMissing = Boolean(profile.active && profile.active_node_id && !profile.nodes.some(node => node.id === profile.active_node_id));
    const actionHint = needsSelection
      ? '<span class="field-help bad">需要重新选择节点</span>'
      : profile.active && !selectedDiffers
        ? '<span class="field-help">当前配置正在使用</span>'
        : "";
    return `<article class="managed-profile-card${profile.active ? " active" : ""}" data-profile-card="${escapeHTML(profile.id)}">
      <header><div><strong>${escapeHTML(profile.name)}</strong>${profile.active ? '<span class="status-pill good">使用中</span>' : ""}</div>
        <small>${profile.url_configured ? "URL 订阅" : "本地 YAML"} · ${profile.nodes.length} 个可用节点 · 更新于 ${escapeHTML(formatUpdatedAt(profile.subscription_at))}</small></header>
      <div class="managed-profile-meta">
        <label>刷新间隔（分钟）<input type="number" min="15" max="10080" value="${profile.refresh_interval_minutes}" data-profile-interval="${escapeHTML(profile.id)}"></label>
        <button type="button" data-save-proxy-profile="${escapeHTML(profile.id)}">保存配置</button>
      </div>
      ${profile.unsupported_count ? `<p class="field-help">另有 ${profile.unsupported_count} 个不支持的节点（${escapeHTML(profile.unsupported_types.join("、"))}）</p>` : ""}
      ${activeNodeMissing ? '<p class="field-help bad">当前节点已从最新订阅中移除；现有线路不会自动切换，请重新选择并应用节点。</p>' : ""}
      <details class="managed-profile-nodes"${profile.active ? " open" : ""}><summary>节点列表（${profile.nodes.length}）</summary>
        <div>${profile.nodes.length ? profile.nodes.map(node => `<div class="managed-profile-node">
          <label class="managed-profile-node-choice">
            <input type="radio" name="profile-node-${escapeHTML(profile.id)}" value="${escapeHTML(node.id)}" data-select-proxy-node data-profile-id="${escapeHTML(profile.id)}"${node.id === profile.selected_node_id ? " checked" : ""}>
            <span><strong>${escapeHTML(node.name)}</strong><small>${protocolLabel(node.protocol)}${node.id === profile.active_node_id ? " · 当前线路" : ""}</small></span>
          </label>
          <button type="button" data-test-proxy-node data-profile-id="${escapeHTML(profile.id)}" data-node-id="${escapeHTML(node.id)}">测试</button>
        </div>`).join("") : '<p class="field-help">当前配置没有支持的节点。</p>'}</div>
      </details>
      <footer class="managed-profile-card-actions">
        ${actionHint}
        ${profile.active && !selectedDiffers && !needsSelection ? "" : `<button type="button" class="primary" data-activate-proxy-profile="${escapeHTML(profile.id)}"${profile.selected_node_id ? "" : " disabled"}>${profile.active ? "应用节点" : "使用配置"}</button>`}
        <button type="button" data-refresh-proxy-profile="${escapeHTML(profile.id)}"${profile.url_configured ? "" : " disabled"}>${icon("refresh")}刷新</button>
        <button type="button" class="danger" data-delete-proxy-profile="${escapeHTML(profile.id)}">删除</button>
      </footer>
    </article>`;
  }).join("");
}

export function renderProxySettings(settings: ProxySettings, managed: ManagedProxyView): string {
  const mode = settings.mode || "external";
  const profiles = managed.profiles || [];
  const activeProfile = profiles.find(profile => profile.active);
  const activeNode = activeProfile?.nodes.find(node => node.id === activeProfile.active_node_id);
  return `<section class="page proxy-page">
    <header class="page-head"><div><h1>代理</h1><p>添加配置、选择节点和切换线路相互独立；任何测试都不会改变当前流量。</p></div></header>
    <form id="proxy-settings-form" class="panel proxy-settings-panel">
      <div class="panel-head"><strong>全局出站</strong><span>${activeProfile ? `当前：${escapeHTML(activeProfile.name)}${activeNode ? ` · ${escapeHTML(activeNode.name)}` : ""}` : "当前未使用内置配置"}</span></div>
      <div class="proxy-settings-body proxy-settings-grid">
        <label>代理模式<select id="proxy-mode" name="mode">
          ${option("off", "关闭代理", mode === "off")}
          ${option("external", "现有 HTTP / HTTPS 代理", mode === "external")}
          ${option("managed_hysteria2", "AHA 内置代理", mode === "managed_hysteria2")}
        </select></label>
        <div id="proxy-external-fields" class="proxy-mode-fields${mode === "external" ? "" : " hidden"}">
          <label>HTTP_PROXY<input id="proxy-http" value="${escapeHTML(settings.http_proxy)}" placeholder="http://127.0.0.1:7897"></label>
          <label>HTTPS_PROXY<input id="proxy-https" value="${escapeHTML(settings.https_proxy)}" placeholder="http://127.0.0.1:7897"></label>
        </div>
        <label>直连地址（NO_PROXY）<input id="proxy-bypass" value="${escapeHTML(settings.no_proxy)}" placeholder="localhost,127.0.0.1,::1"></label>
        <div id="proxy-test-status" class="detect-status${managed.last_error ? " bad" : ""}">${escapeHTML(managed.last_error || "")}</div>
      </div>
      <div class="dialog-actions proxy-actions"><button type="button" id="test-proxy">${icon("refresh")}保存并测试当前线路</button><button class="primary" type="submit">${icon("save")}保存全局设置</button></div>
    </form>
    <section class="panel managed-profiles-panel">
      <div class="panel-head"><strong>代理配置</strong><span>已保存 ${profiles.length} / 20</span><button type="button" class="primary" id="open-proxy-add">添加配置</button></div>
      <div class="managed-profile-list">${profileCards(profiles)}</div>
    </section>
    <dialog id="proxy-add-dialog">
      <div class="dialog-head"><strong>添加代理配置</strong><button type="button" id="close-proxy-add" class="icon-button" title="关闭">×</button></div>
      <div class="dialog-body proxy-add-body">
        <label>配置名称<input id="proxy-profile-name" maxlength="100" placeholder="例如：备用订阅"></label>
        <label>HTTPS 订阅地址<input id="proxy-subscription-url" type="password" autocomplete="new-password" placeholder="https://..."></label>
        <label>或上传 Clash YAML<input id="proxy-subscription-file" type="file" accept=".yaml,.yml,text/yaml,text/plain"></label>
        <label>刷新间隔（分钟）<input id="proxy-new-refresh-interval" type="number" min="15" max="10080" value="1440"></label>
        <p class="field-help">添加后不会切换当前线路，需要在配置卡片中明确点击“使用配置”。</p>
      </div>
      <div class="dialog-actions"><button type="button" id="cancel-proxy-add">取消</button><button type="button" class="primary" id="import-proxy-subscription">${icon("save")}添加配置</button></div>
    </dialog>
  </section>`;
}

export function bindProxySettings(options: {
  settings: ProxySettings;
  managed: ManagedProxyView;
  onChanged: (settings: ProxySettings, managed?: ManagedProxyView) => void;
  setMessage: (kind: "error" | "notice", message: string) => void;
}): void {
  const status = (message: string, bad = false): void => {
    const node = document.querySelector<HTMLElement>("#proxy-test-status");
    if (!node) return;
    node.textContent = message;
    node.classList.toggle("bad", bad);
  };
  const finish = (response: {proxy: ProxySettings; managed: ManagedProxyView}, message: string): void => {
    options.setMessage("notice", message);
    options.onChanged(response.proxy, response.managed);
  };
  const addDialog = document.querySelector<HTMLDialogElement>("#proxy-add-dialog");
  document.querySelector<HTMLButtonElement>("#open-proxy-add")?.addEventListener("click", () => addDialog?.showModal());
  for (const id of ["close-proxy-add", "cancel-proxy-add"]) {
    document.querySelector<HTMLButtonElement>(`#${id}`)?.addEventListener("click", () => addDialog?.close());
  }
  document.querySelector<HTMLSelectElement>("#proxy-mode")?.addEventListener("change", event => {
    document.querySelector("#proxy-external-fields")?.classList.toggle("hidden", event.currentTarget.value !== "external");
  });
  document.querySelector<HTMLFormElement>("#proxy-settings-form")?.addEventListener("submit", event => {
    event.preventDefault();
    const button = event.currentTarget.querySelector<HTMLButtonElement>('button[type="submit"]');
    if (button) button.disabled = true;
    status("正在保存全局代理设置…");
    void saveProxySettings(options.settings).then(response => {
      if (response.managed) finish(response as {proxy: ProxySettings; managed: ManagedProxyView}, "全局代理设置已保存");
      else options.onChanged(response.proxy);
    }).catch(error => status(error instanceof Error ? error.message : String(error), true)).finally(() => {
      if (button) button.disabled = false;
    });
  });
  document.querySelector<HTMLButtonElement>("#import-proxy-subscription")?.addEventListener("click", event => {
    const button = event.currentTarget;
    button.disabled = true;
    status("正在读取并校验订阅…");
    void pendingSubscription().then(subscription => {
      if (!subscription) throw new Error("请输入订阅地址或选择 YAML 文件");
      return api.importProxySubscription(subscription);
    }).then(response => {
      addDialog?.close();
      const added = response.managed.profiles[response.managed.profiles.length - 1];
      finish(response, added && added.nodes.length === 0 ? "配置已保存但未切换：没有当前支持的节点" : "代理配置已添加，当前线路未切换");
    }).catch(error => status(error instanceof Error ? error.message : String(error), true)).finally(() => {
      button.disabled = false;
    });
  });
  document.querySelectorAll<HTMLInputElement>("[data-select-proxy-node]").forEach(input => input.addEventListener("change", () => {
    const profileID = input.dataset.profileId;
    if (!profileID || !input.checked) return;
    input.disabled = true;
    void api.updateProxyProfile(profileID, {selected_node_id: input.value})
      .then(response => finish(response, "节点已选中，当前线路未切换"))
      .catch(error => status(error instanceof Error ? error.message : String(error), true))
      .finally(() => { input.disabled = false; });
  }));
  document.querySelectorAll<HTMLButtonElement>("[data-save-proxy-profile]").forEach(button => button.addEventListener("click", () => {
    const profileID = button.dataset.saveProxyProfile;
    const interval = document.querySelector<HTMLInputElement>(`[data-profile-interval="${profileID}"]`);
    if (!profileID || !interval) return;
    button.disabled = true;
    void api.updateProxyProfile(profileID, {refresh_interval_minutes: Number(interval.value)})
      .then(response => finish(response, "配置设置已保存"))
      .catch(error => status(error instanceof Error ? error.message : String(error), true))
      .finally(() => { button.disabled = false; });
  }));
  document.querySelectorAll<HTMLButtonElement>("[data-activate-proxy-profile]").forEach(button => button.addEventListener("click", () => {
    const profileID = button.dataset.activateProxyProfile;
    if (!profileID) return;
    button.disabled = true;
    void api.activateProxyProfile(profileID).then(response => finish(response, "代理线路已切换"))
      .catch(error => status(error instanceof Error ? error.message : String(error), true))
      .finally(() => { button.disabled = false; });
  }));
  document.querySelectorAll<HTMLButtonElement>("[data-refresh-proxy-profile]").forEach(button => button.addEventListener("click", () => {
    const profileID = button.dataset.refreshProxyProfile;
    if (!profileID) return;
    button.disabled = true;
    status("正在刷新代理配置…");
    void api.refreshProxyProfile(profileID).then(response => finish(response, "代理配置已刷新"))
      .catch(error => status(error instanceof Error ? error.message : String(error), true))
      .finally(() => { button.disabled = false; });
  }));
  document.querySelectorAll<HTMLButtonElement>("[data-delete-proxy-profile]").forEach(button => button.addEventListener("click", () => {
    const profileID = button.dataset.deleteProxyProfile;
    if (!profileID || !window.confirm("确定删除这个代理配置？订阅地址和节点凭据会同时删除。")) return;
    button.disabled = true;
    void api.deleteProxyProfile(profileID).then(response => finish(response, "代理配置已删除"))
      .catch(error => status(error instanceof Error ? error.message : String(error), true))
      .finally(() => { button.disabled = false; });
  }));
  document.querySelectorAll<HTMLButtonElement>("[data-test-proxy-node]").forEach(button => button.addEventListener("click", event => {
    event.preventDefault();
    event.stopPropagation();
    const profileID = button.dataset.profileId;
    const nodeID = button.dataset.nodeId;
    if (!profileID || !nodeID) return;
    button.disabled = true;
    const original = button.textContent || "测试";
    button.textContent = "测试中…";
    void api.testProxyNode(profileID, nodeID).then(response => {
      button.textContent = `可用 · ${response.elapsed_ms} ms`;
      button.classList.add("good");
    }).catch(error => {
      button.textContent = "失败 · 重试";
      button.classList.add("bad");
      status(error instanceof Error ? error.message : String(error), true);
    }).finally(() => {
      button.disabled = false;
      window.setTimeout(() => {
        if (!button.isConnected) return;
        button.textContent = original;
        button.classList.remove("good", "bad");
      }, 5000);
    });
  }));
  document.querySelector<HTMLButtonElement>("#test-proxy")?.addEventListener("click", event => {
    const button = event.currentTarget;
    button.disabled = true;
    status("正在测试代理线路…");
    void api.testProxySettings(formSettings(options.settings)).then(response => {
      status(`代理可用：OpenAI HTTP ${response.status_code}，${response.elapsed_ms} ms`);
    }).catch(error => status(error instanceof Error ? error.message : String(error), true)).finally(() => {
      button.disabled = false;
    });
  });
}
