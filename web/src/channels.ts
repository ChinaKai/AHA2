import {api} from "./api.js";
import {icon} from "./icons.js";
import type {ChannelInstance, ChannelOnboardingSession, ChannelPlugin} from "./types.js";

function escapeHTML(value: unknown): string {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

function pluginState(plugin: ChannelPlugin): string {
  if (plugin.install_state !== "installed") return plugin.install_state === "missing" ? "文件缺失" : plugin.install_state === "incompatible" ? "协议不兼容" : "安装无效";
  return plugin.enabled ? "可用" : "已停用";
}

function instanceState(instance: ChannelInstance): string {
  if (instance.effective_availability === "unavailable") return "提供方不可用";
  const labels: Record<string, string> = {draft: "待绑定", onboarding: "绑定中", ready: "已就绪", degraded: "运行异常", disabled: "已停用", error: "错误"};
  return labels[instance.status] || instance.status;
}

export function renderChannels(providers: ChannelPlugin[], instances: ChannelInstance[]): string {
  const available = providers.filter(item => item.available);
  const providerRows = providers.length ? providers.map(plugin => `<article class="channel-provider-card ${plugin.available ? "available" : "unavailable"}">
    <div class="channel-provider-main"><span class="square-icon">${icon("bot")}</span><div><strong>${escapeHTML(plugin.display_name)}</strong><small>${escapeHTML(plugin.provider_key)} · ${escapeHTML(plugin.package_version)} · ${pluginState(plugin)}</small></div></div>
    <div class="channel-provider-actions">${plugin.install_state === "installed" ? `<button type="button" data-channel-plugin-toggle="${escapeHTML(plugin.id)}" data-enabled="${plugin.enabled}" data-revision="${plugin.revision}">${plugin.enabled ? "停用" : "启用"}</button>` : ""}</div>
    ${plugin.last_error ? `<p>${escapeHTML(plugin.last_error)}</p>` : ""}
  </article>`).join("") : `<div class="empty channel-empty"><strong>无可用渠道提供方</strong><p>未安装渠道插件时，项目、任务和知识库仍可正常使用。</p></div>`;
  const instanceRows = instances.length ? instances.map(instance => `<article class="channel-instance-card">
    <header><div><strong>${escapeHTML(instance.name)}</strong><small>${escapeHTML(instance.provider_key || instance.plugin_id)}</small></div><span><span class="status ${instance.status === "ready" ? "good" : instance.status === "error" || instance.status === "degraded" ? "bad" : "warn"}">${instanceState(instance)}</span><button type="button" data-channel-instance-toggle="${escapeHTML(instance.id)}" data-enabled="${instance.status !== "disabled"}" data-revision="${instance.revision}">${instance.status === "disabled" ? "启用" : "停用"}</button></span></header>
    <dl><div><dt>私聊助手</dt><dd>唯一 Owner</dd></div><div><dt>群聊电子人</dt><dd>群 + 提问人隔离</dd></div><div><dt>凭据</dt><dd>${instance.credential_configured ? "已安全保存" : "未配置"}</dd></div></dl>
    ${!instance.owner_bound ? `<button type="button" class="primary full" data-channel-onboard="${escapeHTML(instance.id)}">${instance.credential_configured ? "扫码确认唯一 Owner" : "扫码创建并绑定飞书应用"}</button>` : instance.status !== "ready" ? `<small>唯一 Owner 已绑定，渠道进程正在验活…</small>` : ""}
    <details><summary>兼容方式：绑定已有应用</summary><form data-channel-credentials="${escapeHTML(instance.id)}" data-revision="${instance.revision}"><label>App ID<input name="app_id" value="${escapeHTML(instance.app_id || "")}" required></label><label>App Secret<input name="app_secret" type="password" autocomplete="new-password" required></label><button class="primary" type="submit">保存到 Secret Store</button></form></details>
    <details data-channel-activity="${escapeHTML(instance.id)}"><summary>Owner 收件箱与投递</summary><div class="channel-activity"><small>展开后加载</small></div></details>
  </article>`).join("") : `<div class="empty"><strong>尚未创建渠道实例</strong><p>每个实例独立绑定一个 Owner，并包含私聊助手与群聊电子人。</p></div>`;
  return `<section class="page channels-page">
    <header class="page-head"><div><h1>渠道</h1><p>可选的外部消息渠道；插件缺失或停用不会影响 AHA2 核心功能。</p></div><button id="refresh-channels" type="button">${icon("refresh")}刷新</button></header>
    <section class="panel"><div class="section-head"><div><h2>渠道提供方</h2><p>仅加载受管目录中 manifest 与可执行文件校验通过的插件。</p></div></div><div class="channel-provider-grid">${providerRows}</div></section>
    <section class="panel"><div class="section-head"><div><h2>渠道实例</h2><p>系统宿主 Project/Workspace 在普通项目列表中保持可见。</p></div></div>
      ${available.length ? `<form id="channel-instance-form" class="channel-create-form"><label>提供方<select name="plugin_id">${available.map(item => `<option value="${escapeHTML(item.id)}">${escapeHTML(item.display_name)}</option>`).join("")}</select></label><label>实例名称<input name="name" placeholder="例如：团队飞书" required></label><button class="primary" type="submit">创建实例</button></form>` : ""}
      <div class="channel-instance-grid">${instanceRows}</div>
    </section>
  </section>`;
}

export function bindChannels(options: {refresh: () => Promise<void>; setMessage: (kind: "error" | "success", message: string) => void}): void {
  const refresh = async () => {
    await options.refresh();
  };
  document.querySelector("#refresh-channels")?.addEventListener("click", () => void refresh().catch(error => options.setMessage("error", String(error))));
  document.querySelector<HTMLFormElement>("#channel-instance-form")?.addEventListener("submit", event => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    void api.createChannelInstance(String(form.get("plugin_id") || ""), String(form.get("name") || "")).then(async () => {
      options.setMessage("success", "渠道实例已创建，请继续扫码绑定。");
      await refresh();
    }).catch(error => options.setMessage("error", error instanceof Error ? error.message : String(error)));
  });
  document.querySelectorAll<HTMLButtonElement>("[data-channel-plugin-toggle]").forEach(button => button.addEventListener("click", () => {
    const plugin = button.dataset.channelPluginToggle || "";
    const current = button.dataset.enabled === "true";
    const revision = Number(button.dataset.revision || 0);
    void api.updateChannelPlugin(plugin, !current, revision).then(refresh).catch(error => options.setMessage("error", error instanceof Error ? error.message : String(error)));
  }));
  document.querySelectorAll<HTMLFormElement>("[data-channel-credentials]").forEach(form => form.addEventListener("submit", event => {
    event.preventDefault();
    const values = new FormData(form);
    const id = form.dataset.channelCredentials || "";
    const revision = Number(form.dataset.revision || 0);
    void api.updateChannelCredentials(id, String(values.get("app_id") || ""), String(values.get("app_secret") || ""), revision).then(async () => {
      form.reset();
      options.setMessage("success", "凭据已写入 Secret Store，响应未回显 Secret。");
      await refresh();
    }).catch(error => options.setMessage("error", error instanceof Error ? error.message : String(error)));
  }));
  document.querySelectorAll<HTMLButtonElement>("[data-channel-onboard]").forEach(button => button.addEventListener("click", () => {
    button.disabled = true;
    void api.startChannelOnboarding(button.dataset.channelOnboard || "").then(result => showOnboarding(result.onboarding, options)).catch(error => {
      button.disabled = false;
      options.setMessage("error", error instanceof Error ? error.message : String(error));
    });
  }));
  document.querySelectorAll<HTMLButtonElement>("[data-channel-instance-toggle]").forEach(button => button.addEventListener("click", () => {
    void api.setChannelInstanceEnabled(button.dataset.channelInstanceToggle || "", button.dataset.enabled !== "true", Number(button.dataset.revision || 0)).then(refresh).catch(error => options.setMessage("error", String(error)));
  }));
  document.querySelectorAll<HTMLDetailsElement>("[data-channel-activity]").forEach(details => details.addEventListener("toggle", () => {
    if (!details.open || details.dataset.loaded === "true") return;
    const instanceID = details.dataset.channelActivity || "";
    const body = details.querySelector<HTMLElement>(".channel-activity");
    if (!body) return;
    body.innerHTML = `<small>加载中…</small>`;
    void Promise.all([api.channelHandoffs(instanceID), api.channelDeliveries(instanceID), api.channelKnowledgePolicies(instanceID), api.channelKnowledgeRecords(instanceID)]).then(([handoffs, deliveries, policies, records]) => {
      const handoffRows = handoffs.handoffs.length ? handoffs.handoffs.map(item => `<li><strong>${escapeHTML(item.summary)}</strong><small>${escapeHTML(item.state)}${item.created_task_id ? ` · Task ${escapeHTML(item.created_task_id)}` : ""}</small></li>`).join("") : `<li><small>暂无群聊转单</small></li>`;
      const failed = deliveries.deliveries.filter(item => item.state === "dead_letter");
      const deliveryRows = failed.length ? failed.map(item => `<li><strong>#${item.stream_sequence} · ${escapeHTML(item.last_error_code || "投递失败")}</strong><small>${item.attempts} 次 · ${escapeHTML(item.outcome_certainty || "unknown")}</small><span><button type="button" data-delivery-replay="${escapeHTML(item.id)}">重放</button><button type="button" data-delivery-skip="${escapeHTML(item.id)}">跳过</button></span></li>`).join("") : `<li><small>暂无死信投递</small></li>`;
      const policyRows = policies.policies.map(policy => `<form data-channel-policy="${escapeHTML(instanceID)}" data-endpoint="${escapeHTML(policy.endpoint)}" data-revision="${policy.revision}"><strong>${escapeHTML(policy.endpoint)}</strong><small>固定索引 ${escapeHTML(policy.fixed_index_entry_id)}</small><label>额外 Knowledge 节点 ID（每行一个；追加 :subtree 可授权子树）<textarea name="grants">${escapeHTML(policy.grants.map(grant => `${grant.knowledge_entry_id}${grant.grant_scope === "subtree" ? ":subtree" : ""}`).join("\n"))}</textarea></label><button type="submit">保存 allowlist</button></form>`).join("");
      const recordRows = records.records.filter(record => record.authority_status !== "verified").map(record => `<details><summary>${escapeHTML(record.question)}</summary><form data-channel-record-promote="${escapeHTML(record.id)}"><label>整理后的标题<input name="title" value="${escapeHTML(record.question.slice(0, 120))}" required></label><label>整理后的正文<textarea name="body" required>${escapeHTML(record.answer)}</textarea></label><button type="submit">人工整理并共享</button></form></details>`).join("") || `<small>暂无待整理渠道问答</small>`;
      body.innerHTML = `<h4>Handoff</h4><ul>${handoffRows}</ul><h4>Dead letter</h4><ul>${deliveryRows}</ul><h4>Knowledge allowlist</h4><div class="channel-policy-list">${policyRows}</div><h4>待人工整理问答</h4><div class="channel-record-list">${recordRows}</div>`;
      details.dataset.loaded = "true";
      body.querySelectorAll<HTMLButtonElement>("[data-delivery-replay]").forEach(button => button.addEventListener("click", () => void api.replayChannelDelivery(button.dataset.deliveryReplay || "").then(() => { details.dataset.loaded = "false"; details.open = false; details.open = true; }).catch(error => options.setMessage("error", String(error)))));
      body.querySelectorAll<HTMLButtonElement>("[data-delivery-skip]").forEach(button => button.addEventListener("click", () => void api.skipChannelDelivery(button.dataset.deliverySkip || "").then(() => { details.dataset.loaded = "false"; details.open = false; details.open = true; }).catch(error => options.setMessage("error", String(error)))));
      body.querySelectorAll<HTMLFormElement>("[data-channel-policy]").forEach(form => form.addEventListener("submit", event => {
        event.preventDefault();
        const values = new FormData(form);
        const grants = String(values.get("grants") || "").split(/\r?\n/).map(value => value.trim()).filter(Boolean).map(value => ({knowledge_entry_id: value.replace(/:subtree$/, ""), grant_scope: value.endsWith(":subtree") ? "subtree" : "node"}));
        void api.updateChannelKnowledgePolicy(form.dataset.channelPolicy || "", form.dataset.endpoint || "", Number(form.dataset.revision || 0), grants).then(() => { options.setMessage("success", "Knowledge allowlist 已更新。"); details.dataset.loaded = "false"; }).catch(error => options.setMessage("error", String(error)));
      }));
      body.querySelectorAll<HTMLFormElement>("[data-channel-record-promote]").forEach(form => form.addEventListener("submit", event => {
        event.preventDefault();
        const values = new FormData(form);
        void api.promoteChannelKnowledgeRecord(form.dataset.channelRecordPromote || "", String(values.get("title") || ""), String(values.get("body") || "")).then(() => { options.setMessage("success", "问答已人工整理为实例共享知识。"); form.closest("details")?.remove(); }).catch(error => options.setMessage("error", String(error)));
      }));
    }).catch(error => { body.textContent = error instanceof Error ? error.message : String(error); });
  }));
}

let onboardingPoll = 0;

function showOnboarding(initial: ChannelOnboardingSession, options: {refresh: () => Promise<void>; setMessage: (kind: "error" | "success", message: string) => void}): void {
  if (onboardingPoll) window.clearTimeout(onboardingPoll);
  document.querySelector("#channel-onboarding-dialog")?.remove();
  const dialog = document.createElement("dialog");
  dialog.id = "channel-onboarding-dialog";
  dialog.className = "channel-onboarding-dialog";
  dialog.innerHTML = `<div class="dialog-body"><header class="dialog-head"><div><h2>扫码创建飞书应用</h2><p>请在飞书官方页面核对应用名称与最小权限后确认。</p></div><button type="button" class="icon-button" data-onboarding-close>${icon("close")}</button></header><div data-onboarding-state class="channel-onboarding-state"></div><div class="dialog-actions"><button type="button" data-onboarding-cancel>取消绑定</button></div></div>`;
  document.body.append(dialog);
  const close = () => { if (onboardingPoll) window.clearTimeout(onboardingPoll); onboardingPoll = 0; dialog.close(); dialog.remove(); };
  dialog.querySelector("[data-onboarding-close]")?.addEventListener("click", () => { close(); void options.refresh(); });
  dialog.querySelector("[data-onboarding-cancel]")?.addEventListener("click", () => void api.cancelChannelOnboarding(initial.id).then(async () => { close(); await options.refresh(); }).catch(error => options.setMessage("error", String(error))));
  dialog.addEventListener("cancel", event => { event.preventDefault(); });
  dialog.showModal();
  const update = (item: ChannelOnboardingSession) => {
    const state = dialog.querySelector<HTMLElement>("[data-onboarding-state]");
    if (!state) return;
    if (item.status === "qr_ready" && item.verification_url) {
      state.innerHTML = `<img src="/api/v1/channel-onboarding-sessions/${encodeURIComponent(item.id)}/qr" alt="飞书授权二维码"><p>二维码将在 ${escapeHTML(new Date(item.expires_at).toLocaleTimeString())} 前有效。</p><a class="primary" target="_blank" rel="noopener noreferrer" href="${escapeHTML(item.verification_url)}">在当前设备打开飞书官方确认页</a>`;
    } else {
      state.innerHTML = `<span class="spinner"></span><strong>${item.status === "succeeded" ? "应用已创建，正在启动渠道" : "正在向飞书申请一次性二维码…"}</strong><small>${escapeHTML(item.step || item.status)}</small>`;
    }
  };
  const poll = async () => {
    try {
      const result = await api.channelOnboarding(initial.id);
      update(result.onboarding);
      if (result.onboarding.status === "succeeded") {
        options.setMessage("success", "飞书应用已创建，扫码用户已绑定为唯一 Owner。");
        window.setTimeout(() => { close(); void options.refresh(); }, 700);
        return;
      }
      if (["failed", "cancelled", "expired"].includes(result.onboarding.status)) {
        options.setMessage("error", `飞书绑定未完成：${result.onboarding.status}`);
        return;
      }
    } catch (error) {
      options.setMessage("error", error instanceof Error ? error.message : String(error));
    }
    onboardingPoll = window.setTimeout(() => void poll(), 1000);
  };
  update(initial);
  void poll();
}
