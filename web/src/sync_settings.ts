import {api} from "./api.js";
import {icon} from "./icons.js";
import type {SyncConflict, SyncPreview, SyncRunProgress, SyncSettings, SyncState} from "./types.js";

function escapeHTML(value: unknown): string {
  return String(value ?? "").replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;").replaceAll('"', "&quot;").replaceAll("'", "&#039;");
}

function time(value?: string): string {
  if (!value) return "尚未同步";
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? "尚未同步" : parsed.toLocaleString();
}

function elapsedText(startedAt?: string): string {
  if (!startedAt) return "";
  const started = new Date(startedAt).getTime();
  if (Number.isNaN(started)) return "";
  const seconds = Math.max(0, Math.round((Date.now() - started) / 1000));
  if (seconds < 60) return `${seconds} 秒`;
  return `${Math.floor(seconds / 60)} 分 ${seconds % 60} 秒`;
}

function latestTime(...values: Array<string | undefined>): string {
  return values.filter((value): value is string => Boolean(value)).sort().pop() || "";
}

function syncDomainForType(value: string): string {
  if (["project", "product_line", "workspace"].includes(value)) return "项目";
  if (["task", "task_agent", "round", "turn", "conversation", "task_memory", "attachment"].includes(value)) return "任务";
  if (["knowledge", "knowledge_proposal", "knowledge_binding", "skill"].includes(value)) return "知识库";
  if (["provider", "model", "env_group", "codex_account", "secret_bundle"].includes(value)) return "模型";
  if (["prompt_override", "agent_profile"].includes(value)) return "代理";
  return "其他";
}

const phaseLabels: Record<string, string> = {
  preparing: "分析差异", pulling: "拉取远端变化", replaying: "回放远端历史",
  pushing: "上传本机变化", finalizing: "确认最终状态", complete: "同步完成", failed: "同步失败",
};

function syncStatusHeadline(settings: SyncSettings, state: SyncState, run: SyncRunProgress): {text: string; level: string} {
  if (run.running) return {text: "正在同步", level: "running"};
  if (!settings.endpoint) return {text: "未配置", level: "off"};
  if (!settings.token_configured) return {text: "设备未注册", level: "off"};
  if (state.last_error) return {text: "上次同步失败", level: "error"};
  if (!settings.enabled) return {text: "定时同步已关闭", level: "off"};
  if (!state.last_push_at && !state.last_pull_at) return {text: "尚未同步", level: "off"};
  return {text: "已同步", level: "ok"};
}

function syncErrorHint(message: string): string {
  if (!message) return "";
  if (message.includes("deadline") || message.includes("timeout")) {
    return "同步超时；进度已保存，再次同步会从最后成功的位置继续。";
  }
  if (message.includes("429") || message.includes("503")) {
    return "中心暂时限流，稍后会自动重试，不需要手动处理。";
  }
  if (message.includes("token")) {
    return "设备凭据无效，请重新填写注册码完成设备注册。";
  }
  if (message.includes("dependency")) {
    return "有对象依赖缺失，部分内容暂时无法应用；原始错误见高级详情。";
  }
  return "可以稍后重试；若重复失败，展开高级详情查看原始错误。";
}

function syncProgressHTML(run: SyncRunProgress): string {
  const total = Math.max(0, Number(run.total || 0));
  const completed = Math.max(0, Number(run.completed || 0));
  const detail = run.detail || phaseLabels[run.phase] || "准备同步";
  const count = total > 0 ? `${Math.min(total, completed)} / ${total}` : completed > 0 ? `已处理 ${completed}` : "";
  return `<div id="sync-run-progress" class="sync-run-progress ${run.running ? "running" : ""}" ${run.running ? "" : "hidden"}><div class="sync-run-head"><strong data-sync-phase>${escapeHTML(phaseLabels[run.phase] || run.phase || "准备同步")}</strong><span data-sync-detail>${escapeHTML(detail)}</span><span data-sync-count>${escapeHTML(count)}</span><span data-sync-elapsed>${escapeHTML(elapsedText(run.started_at))}</span></div><progress data-sync-progress ${total > 0 ? `max="${total}" value="${Math.min(total, completed)}"` : ""}></progress></div>`;
}

function updateSyncProgress(run: SyncRunProgress): void {
  const root = document.querySelector<HTMLElement>("#sync-run-progress");
  if (!root) return;
  root.hidden = !run.running;
  root.classList.toggle("running", run.running);
  const total = Math.max(0, Number(run.total || 0));
  const completed = Math.max(0, Number(run.completed || 0));
  const phase = root.querySelector<HTMLElement>("[data-sync-phase]");
  const detail = root.querySelector<HTMLElement>("[data-sync-detail]");
  const count = root.querySelector<HTMLElement>("[data-sync-count]");
  const elapsed = root.querySelector<HTMLElement>("[data-sync-elapsed]");
  const progress = root.querySelector<HTMLProgressElement>("[data-sync-progress]");
  if (phase) phase.textContent = phaseLabels[run.phase] || run.phase || "准备同步";
  if (detail) detail.textContent = run.detail || phaseLabels[run.phase] || "准备同步";
  if (count) count.textContent = total > 0 ? `${Math.min(total, completed)} / ${total}` : completed > 0 ? `已处理 ${completed}` : "";
  if (elapsed) elapsed.textContent = elapsedText(run.started_at);
  if (progress) {
    if (total > 0) {
      progress.max = total;
      progress.value = Math.min(total, completed);
    } else {
      progress.removeAttribute("max");
      progress.removeAttribute("value");
    }
  }
  const runButton = document.querySelector<HTMLButtonElement>("#run-sync");
  if (runButton) {
    runButton.disabled = run.running;
    runButton.innerHTML = run.running ? `${icon("spinner", true)}同步中…` : `${icon("refresh")}立即同步`;
  }
}

export function renderSyncSettings(settings: SyncSettings, state: SyncState, pending: number, conflicts: SyncConflict[], run: SyncRunProgress, settingsReady = true, nextRunAt = ""): string {
  const settingsDisabled = settingsReady ? "" : "disabled";
  const headline = syncStatusHeadline(settings, state, run);
  const lastSuccess = latestTime(state.last_push_at, state.last_pull_at);
  const errorHint = state.last_error ? syncErrorHint(state.last_error) : "";
  const statusPanel = `<section class="panel sync-status-panel"><div class="panel-head"><strong>同步状态</strong><span class="sync-status-pill ${headline.level}">${headline.text}</span></div><div class="sync-summary-grid">
        <div><small>上次成功</small><strong>${time(lastSuccess)}</strong></div>
        <div><small>下次自动同步</small><strong>${settings.enabled ? (nextRunAt ? time(nextRunAt) : "等待计划") : "已关闭"}</strong></div>
        <div><small>待推送</small><strong>${pending}</strong></div>
        <div><small>待处理冲突</small><strong>${conflicts.length}</strong></div>
      </div>${syncProgressHTML(run)}${state.last_error ? `<div class="sync-error-hint"><strong>上次失败</strong><span>${escapeHTML(errorHint)}</span></div>` : ""}
      <details class="sync-advanced"><summary>高级详情</summary><dl class="sync-status-grid">
        <div><dt>游标</dt><dd>${escapeHTML(state.cursor || "—")}</dd></div><div><dt>最近推送</dt><dd>${time(state.last_push_at)}</dd></div><div><dt>最近拉取</dt><dd>${time(state.last_pull_at)}</dd></div><div><dt>原始错误</dt><dd class="${state.last_error ? "bad" : ""}">${escapeHTML(state.last_error || "无")}</dd></div>
      </dl></details></section>`;
  return `<section class="page sync-page">
    <header class="page-head"><div><h1>同步</h1><p>本机与 AHA 中心的对象同步。认证口令只保存在本机 Secret Store。</p></div><div class="actions"><button id="preview-sync" ${run.running ? "disabled" : ""}>${icon("filter")}查看差异</button><button id="run-sync" class="primary" ${run.running ? "disabled" : ""}>${run.running ? `${icon("spinner", true)}同步中…` : `${icon("refresh")}立即同步`}</button></div></header>
    ${statusPanel}
      <form id="sync-settings-form" class="panel sync-settings-panel">
        <div class="panel-head"><strong>连接设置</strong><span>${settingsReady ? (settings.token_configured ? "设备已注册" : "设备尚未注册") : "正在读取本地设置"}</span></div>
        <div class="sync-settings-body">
          <label class="proxy-toggle"><input name="enabled" type="checkbox" ${settings.enabled ? "checked" : ""} ${settingsDisabled}>启用定时同步</label>
          <label>中心地址<input name="endpoint" type="url" value="${escapeHTML(settings.endpoint)}" placeholder="https://sync.example.com" required ${settingsDisabled}></label>
          <label>设备名称<input name="device_name" value="${escapeHTML(settings.device_name?.trim() || settings.device_id)}" required ${settingsDisabled}></label>
          ${settings.device_id ? `<label>设备 ID<input value="${escapeHTML(settings.device_id)}" readonly></label>` : ""}
          <label>同步间隔（秒）<input name="interval_seconds" type="number" min="10" max="86400" value="${settings.interval_seconds || 300}" required ${settingsDisabled}></label>
          ${settings.token_configured ? "" : `<label>一次性注册码<input name="registration_code" type="password" autocomplete="one-time-code" placeholder="首次设备注册时填写" ${settingsDisabled}></label>`}
          <label>同步加密口令<input name="passphrase" type="password" minlength="12" autocomplete="new-password" placeholder="${settings.passphrase_configured ? "留空以保留现有加密口令" : "至少 12 位，各设备必须一致"}" ${settingsDisabled}></label>
          ${settings.passphrase_configured ? `<label class="proxy-toggle"><input name="clear_passphrase" type="checkbox" ${settingsDisabled}>清除已保存加密口令</label>` : ""}
          <div class="field-help">Provider、Env Secret 与 Codex 账号凭据会自动全部加密同步。SSH/硬件凭据仅作为远端只读镜像加密保存，显式接管后才能启用；本机路径、串口需重新绑定，设备 Sync Token 永不跨设备。</div>
        </div>
        <div class="dialog-actions sync-actions"><button class="primary" type="submit" ${settingsDisabled}>${icon("save")}保存设置</button></div>
      </form>
    <section class="panel sync-conflicts"><div class="panel-head"><strong>待处理冲突</strong><span>${conflicts.length} 项</span></div>${conflicts.length ? `<div class="table-wrap"><table class="sync-conflicts-table"><thead><tr><th>分类</th><th>对象类型</th><th>对象 ID</th><th>本地版本</th><th>远端版本</th><th>时间</th></tr></thead><tbody>${conflicts.map(item => `<tr><td><span class="sync-domain-badge">${syncDomainForType(item.object_type)}</span></td><td>${escapeHTML(item.object_type)}</td><td>${escapeHTML(item.object_id)}</td><td>${escapeHTML(item.local_version || "—")}</td><td>${escapeHTML(item.remote_version || "—")}</td><td>${time(item.created_at)}</td></tr>`).join("")}</tbody></table></div>` : '<div class="empty-state">当前没有同步冲突</div>'}</section>
  </section>`;
}

export function syncSettingsPayload(settings: SyncSettings, form: Pick<FormData, "get" | "getAll">): Record<string, unknown> {
  return {enabled: form.get("enabled") === "on", endpoint: String(form.get("endpoint") || ""), device_id: settings.device_id, device_name: String(form.get("device_name") || ""), interval_seconds: Number(form.get("interval_seconds") || 300), registration_code: String(form.get("registration_code") || ""), passphrase: String(form.get("passphrase") || ""), clear_passphrase: form.get("clear_passphrase") === "on"};
}

export function syncPreviewMessage(preview: SyncPreview, completed = false): string {
  if (completed) return `同步完成：上传 ${preview.upserts}，本机删除 ${preview.deletes}，拉取更新 ${preview.remote_upserts || 0}，远端删除 ${preview.remote_deletes || 0}，剩余 ${preview.pending}，冲突 ${preview.conflicts}`;
  return `本机将上传 ${preview.upserts} 项、删除 ${preview.deletes} 项；远端将更新本机 ${preview.remote_upserts || 0} 项、删除 ${preview.remote_deletes || 0} 项；当前队列 ${preview.pending} 项、冲突 ${preview.conflicts} 项。`;
}

function syncRunResultMessage(run: SyncRunProgress): string {
  if (run.error) return `同步失败：${run.error}`;
  const summary = run.summary;
  if (!summary) return "同步完成";
  return `同步完成：上传 ${summary.upserts}，本机删除 ${summary.deletes}，拉取更新 ${summary.remote_upserts || 0}，远端删除 ${summary.remote_deletes || 0}`;
}

interface SyncSettingsFormState {
  dataset?: {syncDirty?: string};
  contains?: (node: Node | null) => boolean;
}

export function isSyncSettingsFormEditing(form: SyncSettingsFormState | null, activeElement: Node | null): boolean {
  if (!form) return false;
  return form.dataset?.syncDirty === "true" || Boolean(activeElement && form.contains?.(activeElement));
}

let syncPollTimer = 0;

export function bindSyncSettings(options: {settings: SyncSettings; refresh: () => Promise<void>; pollStatus?: () => Promise<SyncRunProgress>; setMessage: (kind: "error" | "notice", message: string) => void; updateSettings?: (payload: Record<string, unknown>) => Promise<unknown>; flushDeferredRender?: () => void; initialRun?: SyncRunProgress}): void {
  const stopPolling = () => {
    if (syncPollTimer) window.clearInterval(syncPollTimer);
    syncPollTimer = 0;
  };
  const poll = async () => {
    if (!document.querySelector("#sync-run-progress")) {
      stopPolling();
      return;
    }
    try {
      const run = await options.pollStatus?.();
      if (!run) return;
      updateSyncProgress(run);
      if (run.running) return;
      stopPolling();
      options.setMessage(run.error ? "error" : "notice", syncRunResultMessage(run));
      await options.refresh();
    } catch {
      stopPolling();
    }
  };
  const startPolling = () => {
    stopPolling();
    if (!options.pollStatus) return;
    syncPollTimer = window.setInterval(() => void poll(), 800);
    void poll();
  };
  if (options.initialRun?.running) startPolling();

  const settingsForm = document.querySelector<HTMLFormElement>("#sync-settings-form");
  settingsForm?.addEventListener("input", () => { settingsForm.dataset.syncDirty = "true"; });
  settingsForm?.addEventListener("change", () => { settingsForm.dataset.syncDirty = "true"; });
  settingsForm?.addEventListener("focusout", () => {
    window.setTimeout(() => {
      if (!isSyncSettingsFormEditing(settingsForm, document.activeElement)) options.flushDeferredRender?.();
    }, 0);
  });
  settingsForm?.addEventListener("submit", event => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const button = event.currentTarget.querySelector<HTMLButtonElement>('button[type="submit"]');
    if (button) button.disabled = true;
    const payload = syncSettingsPayload(options.settings, form);
    const updateSettings = options.updateSettings || (value => api.updateSyncSettings(value));
    void updateSettings(payload).then(async () => {
      settingsForm.dataset.syncDirty = "false";
      const activeElement = document.activeElement as HTMLElement | null;
      if (activeElement && settingsForm.contains(activeElement)) activeElement.blur();
      options.setMessage("notice", "同步设置已保存");
      await options.refresh();
    }).catch(error => options.setMessage("error", error instanceof Error ? error.message : String(error))).finally(() => { if (button) button.disabled = false; });
  });
  document.querySelector<HTMLButtonElement>("#preview-sync")?.addEventListener("click", async event => {
    const button = event.currentTarget;
    const originalHTML = button.innerHTML;
    button.disabled = true;
    button.innerHTML = `${icon("spinner", true)}分析差异…`;
    try {
      const {preview} = await api.syncPreview();
      options.setMessage("notice", syncPreviewMessage(preview));
    } catch (error) {
      options.setMessage("error", error instanceof Error ? error.message : String(error));
    } finally {
      button.disabled = false;
      button.innerHTML = originalHTML;
    }
  });
  document.querySelector<HTMLButtonElement>("#run-sync")?.addEventListener("click", async event => {
    const button = event.currentTarget;
    button.disabled = true;
    button.innerHTML = `${icon("spinner", true)}同步中…`;
    updateSyncProgress({running: true, phase: "preparing", detail: "分析差异", completed: 0, total: 0});
    try {
      await api.runSync();
      options.setMessage("notice", "同步已在后台开始，可以离开此页面");
      startPolling();
    } catch (error) {
      const code = (error as {code?: string}).code || "";
      if (code === "sync_already_running") {
        options.setMessage("notice", "同步已在进行中");
        startPolling();
        return;
      }
      button.disabled = false;
      button.innerHTML = `${icon("refresh")}立即同步`;
      options.setMessage("error", error instanceof Error ? error.message : String(error));
    }
  });
}
