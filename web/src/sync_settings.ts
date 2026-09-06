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

const syncDomains = [
  {id: "project", icon: "projects", label: "项目", detail: "Project / Workspace 元数据"},
  {id: "task", icon: "tasks", label: "任务", detail: "Task / Round / Turn / Conversation / Memory"},
  {id: "knowledge", icon: "knowledge", label: "知识库", detail: "index 文档树 / Skills"},
  {id: "model", icon: "model", label: "模型", detail: "Provider / Model / Env / Account"},
  {id: "agent", icon: "bot", label: "代理", detail: "Agent Prompt / Capabilities"},
] as const;

function syncDomainForType(value: string): string {
  if (["project", "product_line", "workspace"].includes(value)) return "项目";
  if (["task", "task_agent", "round", "turn", "conversation", "task_memory", "attachment"].includes(value)) return "任务";
  if (["knowledge", "skill"].includes(value)) return "知识库";
  if (["provider", "model", "env_group", "codex_account", "secret_bundle"].includes(value)) return "模型";
  if (["prompt_override", "agent_profile"].includes(value)) return "代理";
  return "其他";
}

const phaseLabels: Record<string, string> = {preparing: "分析差异", pulling: "拉取远端变化", replaying: "回放远端历史", pushing: "上传本机变化", finalizing: "确认最终状态", complete: "同步完成", failed: "同步失败"};

function syncProgressHTML(run: SyncRunProgress): string {
  const total = Math.max(0, Number(run.total || 0));
  const completed = Math.min(total, Math.max(0, Number(run.completed || 0)));
  return `<div id="sync-run-progress" class="sync-run-progress ${run.running ? "running" : ""}" ${run.running ? "" : "hidden"}><div><strong data-sync-phase>${escapeHTML(phaseLabels[run.phase] || run.phase || "准备同步")}</strong><span data-sync-count>${completed} / ${total}</span></div><progress data-sync-progress max="${Math.max(1, total)}" value="${completed}"></progress></div>`;
}

function updateSyncProgress(run: SyncRunProgress): void {
  const root = document.querySelector<HTMLElement>("#sync-run-progress");
  if (!root) return;
  root.hidden = !run.running;
  root.classList.toggle("running", run.running);
  const total = Math.max(0, Number(run.total || 0));
  const completed = Math.min(total, Math.max(0, Number(run.completed || 0)));
  const phase = root.querySelector<HTMLElement>("[data-sync-phase]");
  const count = root.querySelector<HTMLElement>("[data-sync-count]");
  const progress = root.querySelector<HTMLProgressElement>("[data-sync-progress]");
  if (phase) phase.textContent = phaseLabels[run.phase] || run.phase || "准备同步";
  if (count) count.textContent = `${completed} / ${total}`;
  if (progress) { progress.max = Math.max(1, total); progress.value = completed; }
}

export function renderSyncSettings(settings: SyncSettings, state: SyncState, pending: number, conflicts: SyncConflict[], preview: SyncPreview, run: SyncRunProgress): string {
  return `<section class="page sync-page">
    <header class="page-head"><div><h1>同步</h1><p>配置本机与 AHA 中心的通用对象同步。认证口令只保存在本机 Secret Store。</p></div><div class="actions"><button id="run-sync" class="primary">${icon("refresh")}立即同步</button></div></header>
    <nav class="sync-domain-grid" aria-label="同步分类">${syncDomains.map((domain, index) => `<article><span>${index + 1}</span>${icon(domain.icon)}<div><strong>${domain.label}</strong><small>${domain.detail}</small></div></article>`).join("")}</nav>
    <p class="sync-dependency-note">界面按项目 → 任务 → 知识库 → 模型 → 代理分类；底层依据对象依赖自动排序。</p>
    <div class="sync-layout">
      <form id="sync-settings-form" class="panel sync-settings-panel">
        <div class="panel-head"><strong>连接设置</strong><span>${settings.token_configured ? "设备已注册" : "设备尚未注册"}</span></div>
        <div class="sync-settings-body">
          <label class="proxy-toggle"><input name="enabled" type="checkbox" ${settings.enabled ? "checked" : ""}>启用定时同步</label>
          <label>中心地址<input name="endpoint" type="url" value="${escapeHTML(settings.endpoint)}" placeholder="https://sync.example.com" required></label>
          <label>设备名称<input name="device_name" value="${escapeHTML(settings.device_name || settings.device_id)}" required></label>
          ${settings.device_id ? `<label>设备 ID<input value="${escapeHTML(settings.device_id)}" readonly></label>` : ""}
          <label>同步间隔（秒）<input name="interval_seconds" type="number" min="10" max="86400" value="${settings.interval_seconds || 300}" required></label>
          ${settings.token_configured ? "" : '<label>一次性注册码<input name="registration_code" type="password" autocomplete="one-time-code" placeholder="首次设备注册时填写"></label>'}
          <label>同步加密口令<input name="passphrase" type="password" minlength="12" autocomplete="new-password" placeholder="${settings.passphrase_configured ? "留空以保留现有加密口令" : "至少 12 位，各设备必须一致"}"></label>
          ${settings.passphrase_configured ? '<label class="proxy-toggle"><input name="clear_passphrase" type="checkbox">清除已保存加密口令</label>' : ""}
          <div class="field-help">Provider、Env Secret 与 Codex 账号凭据会自动全部加密同步。SSH/硬件凭据仅作为远端只读镜像加密保存，显式接管后才能启用；本机路径、串口需重新绑定，设备 Sync Token 永不跨设备。</div>
        </div>
        <div class="dialog-actions sync-actions"><button class="primary" type="submit">${icon("save")}保存设置</button></div>
      </form>
      <section class="panel sync-status-panel"><div class="panel-head"><strong>同步状态</strong><span>${pending} 项待推送</span></div><div class="sync-diff-summary"><span>本机待上传 <strong>${preview.upserts || 0}</strong></span><span>本机待删除 <strong>${preview.deletes || 0}</strong></span><span>远端待更新 <strong>${preview.remote_upserts || 0}</strong></span><span>远端待删除 <strong>${preview.remote_deletes || 0}</strong></span></div>${syncProgressHTML(run)}<div class="sync-domain-status">${syncDomains.map(domain => `<span>${icon(domain.icon)}${domain.label}</span>`).join("")}</div><dl class="sync-status-grid">
        <div><dt>游标</dt><dd>${escapeHTML(state.cursor || "—")}</dd></div><div><dt>最近推送</dt><dd>${time(state.last_push_at)}</dd></div><div><dt>最近拉取</dt><dd>${time(state.last_pull_at)}</dd></div><div><dt>最近错误</dt><dd class="${state.last_error ? "bad" : ""}">${escapeHTML(state.last_error || "无")}</dd></div>
      </dl></section>
    </div>
    <section class="panel sync-conflicts"><div class="panel-head"><strong>待处理冲突</strong><span>${conflicts.length} 项</span></div>${conflicts.length ? `<div class="table-wrap"><table><thead><tr><th>分类</th><th>对象类型</th><th>对象 ID</th><th>本地版本</th><th>远端版本</th><th>时间</th></tr></thead><tbody>${conflicts.map(item => `<tr><td><span class="sync-domain-badge">${syncDomainForType(item.object_type)}</span></td><td>${escapeHTML(item.object_type)}</td><td>${escapeHTML(item.object_id)}</td><td>${escapeHTML(item.local_version || "—")}</td><td>${escapeHTML(item.remote_version || "—")}</td><td>${time(item.created_at)}</td></tr>`).join("")}</tbody></table></div>` : '<div class="empty-state">当前没有同步冲突</div>'}</section>
  </section>`;
}

export function syncSettingsPayload(settings: SyncSettings, form: Pick<FormData, "get" | "getAll">): Record<string, unknown> {
  return {enabled: form.get("enabled") === "on", endpoint: String(form.get("endpoint") || ""), device_id: settings.device_id, device_name: String(form.get("device_name") || ""), interval_seconds: Number(form.get("interval_seconds") || 300), registration_code: String(form.get("registration_code") || ""), passphrase: String(form.get("passphrase") || ""), clear_passphrase: form.get("clear_passphrase") === "on"};
}

export function syncPreviewMessage(preview: SyncPreview, completed = false): string {
  if (completed) return `同步完成：上传 ${preview.upserts}，本机删除 ${preview.deletes}，拉取更新 ${preview.remote_upserts || 0}，远端删除 ${preview.remote_deletes || 0}，剩余 ${preview.pending}，冲突 ${preview.conflicts}`;
  return `本机将上传 ${preview.upserts} 项、删除 ${preview.deletes} 项；远端将更新本机 ${preview.remote_upserts || 0} 项、删除 ${preview.remote_deletes || 0} 项；当前队列 ${preview.pending} 项、冲突 ${preview.conflicts} 项。是否继续？`;
}

interface SyncSettingsFormState {
  dataset?: {syncDirty?: string};
  contains?: (node: Node | null) => boolean;
}

export function isSyncSettingsFormEditing(form: SyncSettingsFormState | null, activeElement: Node | null): boolean {
  if (!form) return false;
  return form.dataset?.syncDirty === "true" || Boolean(activeElement && form.contains?.(activeElement));
}

export function bindSyncSettings(options: {settings: SyncSettings; refresh: () => Promise<void>; pollStatus?: () => Promise<SyncRunProgress>; setMessage: (kind: "error" | "notice", message: string) => void; updateSettings?: (payload: Record<string, unknown>) => Promise<unknown>; flushDeferredRender?: () => void}): void {
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
  document.querySelector<HTMLButtonElement>("#run-sync")?.addEventListener("click", async event => {
    const button = event.currentTarget;
    button.disabled = true;
    try {
      const {preview} = await api.syncPreview();
      if ((preview.upserts || preview.deletes || preview.remote_upserts || preview.remote_deletes || preview.pending || preview.conflicts) && !window.confirm(syncPreviewMessage(preview))) return;
      updateSyncProgress({running: true, phase: "preparing", completed: 0, total: preview.upserts + preview.deletes + (preview.remote_upserts || 0) + (preview.remote_deletes || 0)});
      const request = api.runSync();
      const timer = window.setInterval(() => {
        void options.pollStatus?.().then(updateSyncProgress).catch(() => {});
      }, 400);
      let result;
      try {
        result = await request;
      } finally {
        window.clearInterval(timer);
		if (options.pollStatus) {
			try { updateSyncProgress(await options.pollStatus()); } catch { /* final refresh reports errors */ }
		}
      }
      options.setMessage("notice", syncPreviewMessage(result.summary, true));
      await options.refresh();
    } catch (error) {
      options.setMessage("error", error instanceof Error ? error.message : String(error));
    } finally {
      button.disabled = false;
    }
  });
}
