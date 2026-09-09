import {api} from "./api.js";
import {icon} from "./icons.js";
import type {CodexAccount, CodexUsageWindow} from "./types.js";
const refreshingAccounts = new Set<string>();

function escapeHTML(value: unknown): string {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

function accountIdentity(account: CodexAccount): string {
  return account.label || account.email || account.account_id || account.id;
}

function resetLabel(timestamp?: number): string {
  if (!timestamp) return "";
  return new Date(timestamp * 1000).toLocaleString("zh-CN", {
    month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit",
  });
}

function quotaWindow(account: CodexAccount): CodexUsageWindow | undefined {
  const codex = (account.usage?.rate_limits || []).find(limit => limit.id === "codex");
  if (!codex) return undefined;
  return [codex.primary_window, codex.secondary_window]
    .filter((window): window is CodexUsageWindow => Boolean(window))
    .sort((left, right) => right.limit_window_seconds - left.limit_window_seconds)[0];
}

function quotaLabel(seconds: number): string {
  const days = seconds / (24 * 60 * 60);
  if (days >= 6 && days <= 8) return "周额度";
  if (days >= 28 && days <= 31) return "月额度";
  if (seconds >= 24 * 60 * 60 && Number.isInteger(days)) return `${days} 天额度`;
  return `${Math.round(seconds / (60 * 60))} 小时额度`;
}

function usageHTML(account: CodexAccount): string {
  const quota = quotaWindow(account);
  const error = account.usage_error
    ? `<div class="codex-account-error">${escapeHTML(account.usage_error)}</div>`
    : "";
  if (!quota) {
    return `${!error ? '<div class="codex-limit-row empty"><span>额度</span><small>暂不可用</small></div>' : ""}${error}`;
  }
  const label = quotaLabel(quota.limit_window_seconds);
  const remaining = Math.max(0, Math.min(100, 100 - Number(quota.used_percent || 0)));
  const tone = remaining <= 10 ? "bad" : remaining <= 30 ? "warn" : "good";
  const reset = resetLabel(quota.reset_at);
  return `<div class="codex-limit-row ${tone}">
    <span>${label}</span><progress max="100" value="${remaining}" title="${label}剩余 ${remaining}%"></progress>
    <strong>${remaining}% 剩余</strong>${reset ? `<small>${escapeHTML(reset)} 重置</small>` : ""}
  </div>${error}`;
}

function renderCodexAccountsBase(accounts: CodexAccount[]): string {
  const rows = accounts.map(account => {
    const resetCredits = Number(account.usage?.reset_credits_available || 0);
    const displayName = account.email || accountIdentity(account);
    const subtitle = account.label && account.label !== displayName
      ? `<small>${escapeHTML(account.label)}</small>` : "";
    const updated = account.usage_updated_at
      ? `，额度更新于 ${new Date(account.usage_updated_at).toLocaleString("zh-CN")}` : "";
    return `<article class="codex-account-card">
      <div class="codex-account-head">
        <div class="item-title">${icon("model")}<div><strong>${escapeHTML(displayName)}</strong>${subtitle}</div></div>
        <div class="codex-account-meta">
          <span class="status ${account.credential_configured ? "good" : "warn"}">${escapeHTML(account.plan_type || (account.credential_configured ? "Ready" : "No Token"))}</span>
          ${resetCredits > 0 ? `<span class="codex-reset-credit">重置券 ${resetCredits}</span>` : ""}
          <span class="row-actions"><button type="button" data-refresh-codex-account="${account.id}" class="icon-button" title="刷新额度和模型${escapeHTML(updated)}">${icon("refresh")}</button><button type="button" data-delete-codex-account="${account.id}" class="icon-button" title="删除账号">${icon("close")}</button></span>
        </div>
      </div>
      ${usageHTML(account)}
      ${account.models_error ? `<div class="codex-account-error">模型目录：${escapeHTML(account.models_error)}</div>` : ""}
    </article>`;
  }).join("");
  return `<section class="panel codex-account-panel">
    <div class="panel-head"><strong>Codex 官方账号</strong><button type="button" id="add-codex-account">${icon("plus")}添加账号</button></div>
    ${rows || `<div class="empty">添加 OpenAI Codex 账号后，可为每个模型选择独立账号。</div>`}
  </section>
  <dialog id="codex-account-dialog" class="wide"><div class="dialog-head"><h2>添加 Codex 账号</h2><button type="button" data-close class="icon-button">${icon("close")}</button></div><div class="dialog-body">
    <label>账号备注<input id="codex-account-label" placeholder="可选，例如 工作账号"></label>
    <label>授权链接<div class="codex-auth-link"><textarea id="codex-auth-url" readonly rows="4" placeholder="正在生成..."></textarea><button type="button" id="copy-codex-auth-url" class="icon-button" title="复制授权链接">${icon("copy")}</button></div></label>
    <div class="field-help">复制链接到浏览器完成登录。跳转失败页面出现后，复制浏览器地址栏中的完整 Callback URL。</div>
    <label>Callback URL<textarea id="codex-callback-url" rows="4" placeholder="http://localhost:1455/auth/callback?code=...&state=..."></textarea></label>
    <div id="codex-login-status" class="detect-status"></div>
    <div class="dialog-actions"><button type="button" data-close>取消</button><button type="button" id="import-local-codex-account">导入本机登录</button><button type="button" id="submit-codex-callback" class="primary">添加账号</button></div>
  </div></dialog>`;
}

export function renderCodexAccounts(accounts: CodexAccount[]): string {
  return renderCodexAccountsBase(accounts).replace(
    "<label>Callback URL",
    '<label class="proxy-toggle"><input id="codex-login-proxy" type="checkbox" checked>OAuth Token 交换使用共享代理</label><label>Callback URL',
  );
}

export function bindCodexAccounts(options: {
  accounts: CodexAccount[];
  onChanged: () => Promise<void>;
  setMessage: (kind: "error" | "notice", message: string) => void;
}): void {
  let loginID = "";
  const status = (message: string, bad = false): void => {
    const node = document.querySelector<HTMLElement>("#codex-login-status");
    if (node) {
      node.textContent = message;
      node.classList.toggle("bad", bad);
    }
  };
  const busy = async (
    button: HTMLElement | null,
    label: string,
    action: () => Promise<void>,
    onError?: (message: string) => void,
    iconOnly = false,
  ): Promise<void> => {
    if (!button) return;
    const original = button.innerHTML;
    const originalTitle = button.getAttribute("title");
    button.setAttribute("disabled", "true");
    if (iconOnly) {
      button.innerHTML = icon("spinner", true);
      button.setAttribute("title", label);
    } else {
      button.textContent = label;
    }
    try {
      await action();
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      options.setMessage("error", message);
      onError?.(message);
    } finally {
      button.removeAttribute("disabled");
      button.innerHTML = original;
      if (originalTitle === null) button.removeAttribute("title");
      else button.setAttribute("title", originalTitle);
    }
  };

  const refreshAccount = async (id: string, notify: boolean): Promise<void> => {
    if (!id || refreshingAccounts.has(id)) return;
    refreshingAccounts.add(id);
    try {
      const result = await api.refreshCodexAccount(id);
      if (notify) {
        options.setMessage(result.warnings?.length ? "error" : "notice",
          result.warnings?.join("；") || "账号额度和模型目录已刷新");
      }
    } finally {
      refreshingAccounts.delete(id);
    }
  };

  document.querySelector<HTMLElement>("#add-codex-account")?.addEventListener("click", () => {
    const dialog = document.querySelector<HTMLDialogElement>("#codex-account-dialog");
    const authURL = document.querySelector<HTMLTextAreaElement>("#codex-auth-url");
    const callback = document.querySelector<HTMLTextAreaElement>("#codex-callback-url");
    loginID = "";
    if (authURL) authURL.value = "";
    if (callback) callback.value = "";
    status("正在生成一次性授权链接...");
    dialog?.showModal();
    const proxyEnabled = Boolean(document.querySelector<HTMLInputElement>("#codex-login-proxy")?.checked);
    void api.startCodexLogin(proxyEnabled).then(response => {
      loginID = response.login.id;
      if (authURL) authURL.value = response.login.auth_url;
      status("授权链接已生成");
    }).catch(error => status(error instanceof Error ? error.message : String(error), true));
  });
  document.querySelector<HTMLElement>("#copy-codex-auth-url")?.addEventListener("click", () => {
    const button = document.querySelector<HTMLElement>("#copy-codex-auth-url");
    const value = document.querySelector<HTMLTextAreaElement>("#codex-auth-url")?.value || "";
    if (!value) return;
    void navigator.clipboard.writeText(value).then(() => {
      if (button) button.classList.add("copied");
      status("授权链接已复制");
    }).catch(() => status("复制失败，请手动选择链接复制", true));
  });
  document.querySelector<HTMLElement>("#import-local-codex-account")?.addEventListener("click", () => {
    const button = document.querySelector<HTMLElement>("#import-local-codex-account");
    status("正在读取本机 Codex 登录并刷新模型...");
    void busy(button, "导入中", async () => {
      const imported = await api.importLocalCodexAccount();
      let refreshError = "";
      try {
        await api.refreshCodexAccount(imported.account.id);
      } catch (error) {
        refreshError = error instanceof Error ? error.message : String(error);
      }
      document.querySelector<HTMLDialogElement>("#codex-account-dialog")?.close();
      options.setMessage(refreshError ? "error" : "notice", refreshError
        ? `Codex 账号已导入，但模型刷新失败：${refreshError}`
        : "本机 Codex 登录已导入");
      await options.onChanged();
    }, message => status(message, true));
  });
  document.querySelector<HTMLElement>("#submit-codex-callback")?.addEventListener("click", () => {
    const button = document.querySelector<HTMLElement>("#submit-codex-callback");
    const callbackURL = document.querySelector<HTMLTextAreaElement>("#codex-callback-url")?.value.trim() || "";
    const label = document.querySelector<HTMLInputElement>("#codex-account-label")?.value.trim() || "";
    if (!loginID || !callbackURL) {
      status("请先生成授权链接并填写完整 Callback URL", true);
      return;
    }
    status("正在校验 Callback 并添加账号...");
    void busy(button, "添加中", async () => {
      await api.submitCodexCallback(loginID, callbackURL, label);
      document.querySelector<HTMLDialogElement>("#codex-account-dialog")?.close();
      options.setMessage("notice", "Codex 账号已添加");
      await options.onChanged();
    }, message => status(message, true));
  });
  document.querySelectorAll<HTMLElement>("[data-delete-codex-account]").forEach(button => button.addEventListener("click", () => {
    const id = button.dataset.deleteCodexAccount || "";
    if (!id || !window.confirm("删除该 Codex 账号？已被模型或历史 Session 使用时不能删除。")) return;
    void busy(button, "删除中", async () => {
      await api.deleteCodexAccount(id);
      options.setMessage("notice", "Codex 账号已删除");
      await options.onChanged();
    });
  }));
  document.querySelectorAll<HTMLElement>("[data-refresh-codex-account]").forEach(button => button.addEventListener("click", () => {
    const id = button.dataset.refreshCodexAccount || "";
    void busy(button, "刷新中", async () => {
      await refreshAccount(id, true);
      await options.onChanged();
    }, undefined, true);
  }));
  const stale = options.accounts.filter(account => {
    const checked = [account.usage_updated_at, account.models_updated_at]
      .map(value => Date.parse(value || ""));
    return checked.some(value => !Number.isFinite(value) || Date.now() - value > 5 * 60 * 1000);
  });
  if (stale.length) {
    void (async () => {
      for (const account of stale) {
        await refreshAccount(account.id, false);
      }
      await options.onChanged();
    })().catch(error => options.setMessage("error", error instanceof Error ? error.message : String(error)));
  }
}
