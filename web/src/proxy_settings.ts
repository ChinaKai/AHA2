import {api} from "./api.js";
import {icon} from "./icons.js";
import type {ProxySettings} from "./types.js";

function escapeHTML(value: unknown): string {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

function formSettings(): ProxySettings {
  return {
    http_proxy: document.querySelector<HTMLInputElement>("#proxy-http")?.value.trim() || "",
    https_proxy: document.querySelector<HTMLInputElement>("#proxy-https")?.value.trim() || "",
    no_proxy: document.querySelector<HTMLInputElement>("#proxy-bypass")?.value.trim() || "",
  };
}

export function renderProxySettings(settings: ProxySettings): string {
  return `<section class="page proxy-page">
    <header class="page-head"><div><h1>代理</h1><p>共享代理地址由 Task Backend 和 Codex 账号登录按各自开关使用。</p></div></header>
    <form id="proxy-settings-form" class="panel proxy-settings-panel">
      <div class="panel-head"><strong>共享代理</strong><span>一期支持 HTTP / HTTPS 代理</span></div>
      <div class="proxy-settings-body">
        <label>HTTP_PROXY<input id="proxy-http" name="http_proxy" value="${escapeHTML(settings.http_proxy)}" placeholder="http://127.0.0.1:7897"></label>
        <label>HTTPS_PROXY<input id="proxy-https" name="https_proxy" value="${escapeHTML(settings.https_proxy)}" placeholder="http://127.0.0.1:7897"></label>
        <label>NO_PROXY<input id="proxy-bypass" name="no_proxy" value="${escapeHTML(settings.no_proxy)}" placeholder="localhost,127.0.0.1,::1"></label>
        <div id="proxy-test-status" class="detect-status"></div>
      </div>
      <div class="dialog-actions proxy-actions"><button type="button" id="test-proxy">${icon("refresh")}测试代理</button><button class="primary" type="submit">${icon("save")}保存</button></div>
    </form>
  </section>`;
}

export function bindProxySettings(options: {
  onChanged: (settings: ProxySettings) => void;
  setMessage: (kind: "error" | "notice", message: string) => void;
}): void {
  const status = (message: string, bad = false): void => {
    const node = document.querySelector<HTMLElement>("#proxy-test-status");
    if (!node) return;
    node.textContent = message;
    node.classList.toggle("bad", bad);
  };
  document.querySelector<HTMLFormElement>("#proxy-settings-form")?.addEventListener("submit", event => {
    event.preventDefault();
    const button = event.currentTarget.querySelector<HTMLButtonElement>('button[type="submit"]');
    if (button) button.disabled = true;
    void api.updateProxySettings(formSettings()).then(response => {
      options.onChanged(response.proxy);
      options.setMessage("notice", "代理设置已保存");
      status("设置已保存，新的登录和 Backend Turn 将使用更新后的地址");
    }).catch(error => status(error instanceof Error ? error.message : String(error), true)).finally(() => {
      if (button) button.disabled = false;
    });
  });
  document.querySelector<HTMLButtonElement>("#test-proxy")?.addEventListener("click", event => {
    const button = event.currentTarget;
    button.disabled = true;
    status("正在通过代理连接 OpenAI...");
    void api.testProxySettings(formSettings()).then(response => {
      status(`代理可用，OpenAI HTTP ${response.status_code}，${response.elapsed_ms} ms`);
    }).catch(error => status(error instanceof Error ? error.message : String(error), true)).finally(() => {
      button.disabled = false;
    });
  });
}
