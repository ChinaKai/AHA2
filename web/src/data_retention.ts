import {formatLocaleDateTime, formatLocaleNumber, t} from "./i18n.js";
import type {
  ConversationPurgePlan,
  ConversationPurgeResult,
  ConversationRetentionSettings,
  DatabaseCategory,
  DatabasePurgeProgress,
  DatabaseSafety,
  DatabaseStats,
  DatabaseTableStat,
  HardwareIOFileStats,
  HardwareIOPurgePlan,
  HardwareIOPurgeResult,
  HardwareIORetentionSettings,
  OfflineCompactionInfo,
  OfflineCompactionResult,
} from "./types.js";

// Limits mirror internal/domain/data_retention.go. The server re-validates;
// these only drive the form state.
export const RETENTION_MIN_DAYS = 1;
export const RETENTION_MAX_DAYS = 3650;
// Hardware IO no longer asks for a batch size in the UI. The server still
// enforces its own bounds, so the client sends the stored (or default) cap.
export const PURGE_DEFAULT_MAX_ROWS = 200000;

export interface DataRetentionViewState {
  database: DatabaseStats | null;
  loading: boolean;
  error: string;
  plan: HardwareIOPurgePlan | null;
  result: HardwareIOPurgeResult | null;
  busy: "" | "save" | "preview" | "purge";
  // Last server-side failure for the C dialog. Kept in state so it survives
  // the in-place body refresh instead of only living in the global banner.
  actionError: string;
  progress: DatabasePurgeProgress | null;
  conversationPlan: ConversationPurgePlan | null;
  conversationResult: ConversationPurgeResult | null;
  conversationBusy: "" | "save" | "preview" | "purge";
  conversationActionError: string;
  conversationProgress: DatabasePurgeProgress | null;
  offlineCompaction: OfflineCompactionInfo | null;
  offlineCompactionLoading: boolean;
  offlineCompactionError: string;
  openDialog: string;
}

function escapeHTML(value: unknown): string {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  return `${value >= 100 || unit === 0 ? Math.round(value) : value.toFixed(1)} ${units[unit]}`;
}

function formatRows(value: number, exact: boolean): string {
  const text = formatLocaleNumber(Math.max(0, value || 0));
  return exact ? text : `≈ ${text}`;
}

function formatTime(value?: string): string {
  return value ? formatLocaleDateTime(value) : "—";
}

// retentionFormError returns the message for an invalid form, or "" when the
// values can be sent. Empty, fractional and out-of-range values are rejected.
export function retentionFormError(days: string): string {
  const dayValue = Number(days);
  if (!days.trim() || !Number.isInteger(dayValue) || dayValue < RETENTION_MIN_DAYS || dayValue > RETENTION_MAX_DAYS) {
    return t("data.db.invalidDays", {min: RETENTION_MIN_DAYS, max: RETENTION_MAX_DAYS});
  }
  return "";
}

// conversationFormError validates the B-category form: a valid retention
// period and at least one of tool/update selected.
export function conversationFormError(days: string, tool: boolean, update: boolean): string {
  const dayValue = Number(days);
  if (!days.trim() || !Number.isInteger(dayValue) || dayValue < RETENTION_MIN_DAYS || dayValue > RETENTION_MAX_DAYS) {
    return t("data.db.invalidDays", {min: RETENTION_MIN_DAYS, max: RETENTION_MAX_DAYS});
  }
  if (!tool && !update) return t("data.db.conversationInvalid");
  return "";
}

export function skipReasonText(reason: string): string {
  if (!reason) return "";
  const key = `data.db.skip.${reason}`;
  const text = t(key);
  return text === key ? reason : text;
}

function tableRow(table: DatabaseTableStat): string {
  const size = table.sample_rows > 0
    ? `≈ ${formatBytes(table.estimated_bytes)}<small class="field-help">${t("data.db.sampleNote", {rows: formatLocaleNumber(table.sample_rows), avg: formatBytes(table.avg_row_bytes)})}</small>`
    : "—";
  return `<tr data-db-table="${escapeHTML(table.name)}">
    <td><code>${escapeHTML(table.name)}</code></td>
    <td class="data-size">${formatRows(table.rows, table.rows_exact)}</td>
    <td class="data-size data-db-bytes">${size}</td>
  </tr>`;
}

// safetyLabel and categoryNote come from the server's fixed safety value, so
// an unknown value falls back to read-only rather than implying deletion.
function safetyKey(safety: DatabaseSafety): DatabaseSafety {
  return (["keep", "read_only", "preview_purge", "retire"] as const).includes(safety) ? safety : "read_only";
}

function categoryName(category: DatabaseCategory): string {
  return category.id === "other"
    ? t("data.db.cat.other")
    : `${escapeHTML(category.id)}. ${t(`data.db.cat.${category.key}`)}`;
}

function categoryNote(category: DatabaseCategory, safety: DatabaseSafety): string {
  if (category.cleanup === "conversation") return t("data.db.catNote.conversation");
  if (category.cleanup === "hardware_io") return t("data.db.catNote.hardware");
  return safety === "read_only" || safety === "preview_purge"
    ? t("data.db.catNote.readOnly")
    : t("data.db.catNote.protected");
}

function categoryCard(category: DatabaseCategory): string {
  const safety = safetyKey(category.safety);
  const note = categoryNote(category, safety);
  // Only categories with an executable cleanup kind (B conversation, C
  // hardware_io) get the button. A/H/K and every read-only category still get
  // their dialog from the server payload, but there is nothing to launch.
  const cleanup = category.cleanup
    ? `<button type="button" class="data-db-cleanup-button" data-db-cleanup="${escapeHTML(category.id)}" aria-label="${t("data.db.cleanup")} ${categoryName(category)}">${t("data.db.cleanup")}</button>`
    : "";
  const tables = category.tables.length
    ? `<div class="table-wrap"><table class="data-table data-db-table"><thead><tr><th>${t("data.db.table")}</th><th>${t("data.db.rows")}</th><th>${t("data.db.size")}</th></tr></thead>
        <tbody>${category.tables.map(tableRow).join("")}</tbody></table></div>`
    : `<p class="field-help data-cleanup-note">${t("data.db.categoryEmpty")}</p>`;
  return `<details class="data-db-category" data-db-category="${escapeHTML(category.id)}" data-db-safety="${safety}">
    <summary>
      <strong>${categoryName(category)}</strong>
      <span class="data-db-badge data-db-badge-${safety}">${t(`data.db.safety.${safety}`)}</span>
      <span class="data-db-category-stats field-help">${t("data.db.categoryStats", {tables: category.tables.length, rows: formatRows(category.rows, category.rows_exact), size: formatBytes(category.estimated_bytes)})}</span>
      ${cleanup}
    </summary>
    <p class="field-help data-cleanup-note" data-db-category-note>${note}</p>
    ${tables}
  </details>`;
}

function hardwarePlanSummary(plan: HardwareIOPurgePlan): string {
  const skip = plan.skip_reason ? `<p class="field-help" data-db-plan-skip>${escapeHTML(skipReasonText(plan.skip_reason))}</p>` : "";
  return `<div class="data-db-plan" data-db-plan>
    <p>${t("data.db.planCutoff", {cutoff: escapeHTML(formatTime(plan.cutoff)), days: plan.retention_days})}</p>
    <p><strong>${t("data.db.planRun", {rows: formatLocaleNumber(plan.run_rows), size: formatBytes(plan.run_bytes_estimate)})}</strong></p>
    <p class="field-help">${t("data.db.planEligible", {rows: formatLocaleNumber(plan.eligible_rows_estimate), size: formatBytes(plan.eligible_bytes_estimate), remaining: formatLocaleNumber(plan.remaining_eligible_estimate)})}</p>
    ${skip}
  </div>`;
}

function hardwareResultSummary(result: HardwareIOPurgeResult): string {
  const skip = result.skip_reason ? ` ${escapeHTML(skipReasonText(result.skip_reason))}` : "";
  const partial = result.partial ? ` ${t("data.db.resultPartial")}` : "";
  const backup = result.backup_path ? `<p class="field-help">${escapeHTML(t("data.db.resultBackup", {path: result.backup_path}))}</p>` : "";
  return `<div class="data-db-result" data-db-result>
    <p>${t("data.db.resultDeleted", {rows: formatLocaleNumber(result.deleted_rows), batches: result.batches})}${partial}${skip}</p>
    ${backup}
  </div>`;
}

function conversationPlanSummary(plan: ConversationPurgePlan): string {
  const skip = plan.skip_reason ? `<p class="field-help" data-conversation-plan-skip>${escapeHTML(skipReasonText(plan.skip_reason))}</p>` : "";
  const categories = plan.categories.map(category => t(`data.db.conversationCategory.${category}`)).join(" + ");
  return `<div class="data-db-plan" data-conversation-plan>
    <p>${t("data.db.conversationPlanScope", {days: plan.retention_days, cutoff: escapeHTML(formatTime(plan.cutoff)), categories})}</p>
    <p><strong>${t("data.db.conversationPlanRun", {rows: formatLocaleNumber(plan.run_rows), tasks: formatLocaleNumber(plan.tasks_affected), size: formatBytes(plan.run_bytes_estimate)})}</strong></p>
    ${skip}
  </div>`;
}

function conversationResultSummary(result: ConversationPurgeResult): string {
  const skip = result.skip_reason ? ` ${escapeHTML(skipReasonText(result.skip_reason))}` : "";
  const partial = result.partial ? ` ${t("data.db.resultPartial")}` : "";
  const backup = result.backup_path ? `<p class="field-help">${escapeHTML(t("data.db.resultBackup", {path: result.backup_path}))}</p>` : "";
  return `<div class="data-db-result" data-conversation-result>
    <p>${t("data.db.resultDeleted", {rows: formatLocaleNumber(result.deleted_rows), batches: result.batches})}${partial}${skip}</p>
    ${backup}
  </div>`;
}

// purgeProgressSummary mirrors the server's live purge phase. It is rendered
// only while the cleanup request is in flight: the phase, deleted rows, batch
// count and elapsed seconds keep changing instead of freezing on "reading".
function purgePhaseText(phase: string): string {
  const fallback = "preparing";
  const key = `data.db.phase.${phase || fallback}`;
  const text = t(key);
  return text === key ? t(`data.db.phase.${fallback}`) : text;
}

function purgeProgressSummary(progress: DatabasePurgeProgress | null): string {
  const rows = Math.max(0, progress?.deleted_rows || 0);
  const batches = Math.max(0, progress?.batches || 0);
  const seconds = Math.max(0, Math.floor((progress?.duration_ms || 0) / 1000));
  const planned = Math.max(0, progress?.planned_rows || 0);
  const plannedLine = planned > 0
    ? `<p class="field-help">${t("data.db.purgePlanned", {rows: formatLocaleNumber(planned)})}</p>`
    : "";
  return `<div class="data-db-progress" data-db-progress>
    <p><strong>${escapeHTML(purgePhaseText(progress?.phase || "preparing"))}</strong></p>
    <p>${t("data.db.purgeProgress", {rows: formatLocaleNumber(rows), batches, seconds})}</p>
    ${plannedLine}
  </div>`;
}

// purgeEnabled is true only for a preview that would delete rows and matches
// the values currently in the form, so the confirmed run is the one previewed.
export function purgeEnabled(state: DataRetentionViewState, days: number, rows: number): boolean {
  const plan = state.plan;
  return Boolean(plan && !state.busy && !plan.skip_reason && plan.run_rows > 0
    && plan.retention_days === days && plan.max_delete_rows === rows);
}

export function conversationPurgeEnabled(state: DataRetentionViewState, days: number, tool: boolean, update: boolean): boolean {
  const plan = state.conversationPlan;
  return Boolean(plan && !state.conversationBusy && !plan.skip_reason && plan.run_rows > 0
    && plan.retention_days === days && plan.purge_tool === tool && plan.purge_update === update);
}

export function offlineCompactionReasonText(reason: string): string {
  if (!reason) return "";
  const key = `data.db.offlineReason.${reason}`;
  const text = t(key);
  return text === key ? reason : text;
}

function offlineCompactionStatusText(status: string): string {
  const key = `data.db.offlineResultStatus.${status}`;
  const text = t(key);
  return text === key ? status : text;
}

function dialogShell(id: string, title: string, body: string): string {
  return `<dialog class="data-db-dialog" data-db-dialog="${escapeHTML(id)}" aria-labelledby="data-db-dialog-${escapeHTML(id)}-title">
    <header class="data-db-dialog-head">
      <strong id="data-db-dialog-${escapeHTML(id)}-title">${title}</strong>
      <button type="button" class="icon-button" data-db-dialog-close aria-label="${t("data.db.close")}">×</button>
    </header>
    <div class="data-db-dialog-body">${body}</div>
  </dialog>`;
}

// hardwareFilesSummary describes where new terminal output lives. The
// SQLite range below it only covers rows from before the switch, which the
// service clears at startup.
function hardwareFilesSummary(files: HardwareIOFileStats | undefined, days: number): string {
  if (!files) return "";
  const usage = t("data.db.hardwareFiles", {
    size: formatBytes(files.bytes), max: formatBytes(files.max_bytes), records: formatLocaleNumber(files.records),
    oldest: escapeHTML(formatTime(files.oldest_at)), newest: escapeHTML(formatTime(files.newest_at)),
  });
  return `<p class="data-cleanup-note" data-db-hardware-files>${usage}</p>
    <p class="field-help data-cleanup-note" data-db-hardware-files-policy>${t("data.db.hardwareFilesPolicy", {days})}</p>`;
}

function hardwareDialogBody(state: DataRetentionViewState, category: DatabaseCategory): string {
  const retention: HardwareIORetentionSettings = state.database?.retention ?? {retention_days: 30, max_delete_rows: PURGE_DEFAULT_MAX_ROWS};
  const range = state.database?.hardware_io;
  const days = retention.retention_days;
  // The batch cap stays an internal safety value; the dialog only lets the
  // Owner choose the retention window.
  const rows = retention.max_delete_rows || PURGE_DEFAULT_MAX_ROWS;
  const busy = Boolean(state.busy);
  const canPurge = purgeEnabled(state, days, rows);
  return `<p class="data-cleanup-note">${categoryNote(category, safetyKey(category.safety))}</p>
    ${hardwareFilesSummary(state.database?.hardware_io_files, days)}
    <p class="data-cleanup-note" data-db-range>${range?.max_sequence
      ? t("data.db.range", {oldest: escapeHTML(formatTime(range.oldest_at)), newest: escapeHTML(formatTime(range.newest_at)), rows: formatLocaleNumber(range.estimated_rows)})
      : t("data.db.rangeEmpty")}</p>
    <div class="data-cleanup-actions data-db-form">
      <label>${t("data.db.retentionDays")} <input id="data-hardware-io-days" type="number" min="${RETENTION_MIN_DAYS}" max="${RETENTION_MAX_DAYS}" step="1" value="${days}" ${busy ? "disabled" : ""}></label>
    </div>
    <div class="data-cleanup-actions">
      <button type="button" data-hardware-io-save ${busy ? "disabled" : ""}>${t("data.db.save")}</button>
      <button type="button" data-hardware-io-preview ${busy ? "disabled" : ""}>${t("data.db.preview")}</button>
      <button type="button" class="danger" data-hardware-io-purge ${canPurge ? "" : "disabled"}>${state.busy === "purge" ? t("data.db.purging") : t("data.db.purge")}</button>
    </div>
    <p class="form-error" data-hardware-io-form-error hidden></p>
    ${state.actionError ? `<p class="form-error" data-hardware-io-action-error>${escapeHTML(state.actionError)}</p>` : ""}
    <p class="field-help data-cleanup-note">${t("data.db.purgeHelp")}</p>
    ${state.busy === "purge" ? purgeProgressSummary(state.progress) : ""}
    ${state.plan ? hardwarePlanSummary(state.plan) : ""}
    ${state.result ? hardwareResultSummary(state.result) : ""}
    <p class="field-help data-cleanup-note">${t("data.db.vacuumNote")}</p>`;
}

function conversationDialogBody(state: DataRetentionViewState, category: DatabaseCategory): string {
  const retention: ConversationRetentionSettings = state.database?.conversation_retention ?? {retention_days: 30, purge_tool: true, purge_update: false};
  const days = retention.retention_days;
  const busy = Boolean(state.conversationBusy);
  const canPurge = conversationPurgeEnabled(state, days, retention.purge_tool, retention.purge_update);
  return `<p class="data-cleanup-note">${t("data.db.conversationIntro")}</p>
    <div class="data-cleanup-actions data-db-form">
      <label>${t("data.db.retentionDays")} <input id="data-conversation-days" type="number" min="${RETENTION_MIN_DAYS}" max="${RETENTION_MAX_DAYS}" step="1" value="${days}" ${busy ? "disabled" : ""}></label>
    </div>
    <div class="data-cleanup-note data-db-checks">
      <label><input id="data-conversation-tool" type="checkbox" ${retention.purge_tool ? "checked" : ""} ${busy ? "disabled" : ""}> ${t("data.db.conversationTool")}</label>
      <label><input id="data-conversation-update" type="checkbox" ${retention.purge_update ? "checked" : ""} ${busy ? "disabled" : ""}> ${t("data.db.conversationUpdate")}</label>
    </div>
    <div class="data-cleanup-actions">
      <button type="button" data-conversation-save ${busy ? "disabled" : ""}>${t("data.db.save")}</button>
      <button type="button" data-conversation-preview ${busy ? "disabled" : ""}>${t("data.db.preview")}</button>
      <button type="button" class="danger" data-conversation-purge ${canPurge ? "" : "disabled"}>${state.conversationBusy === "purge" ? t("data.db.purging") : t("data.db.purge")}</button>
    </div>
    <p class="form-error" data-conversation-form-error hidden></p>
    ${state.conversationActionError ? `<p class="form-error" data-conversation-action-error>${escapeHTML(state.conversationActionError)}</p>` : ""}
    <p class="field-help data-cleanup-note">${t("data.db.conversationHelp")}</p>
    ${state.conversationBusy === "purge" ? purgeProgressSummary(state.conversationProgress) : ""}
    ${state.conversationPlan ? conversationPlanSummary(state.conversationPlan) : ""}
    ${state.conversationResult ? conversationResultSummary(state.conversationResult) : ""}
    <p class="field-help data-cleanup-note">${t("data.db.vacuumNote")}</p>`;
}

function readOnlyDialogBody(category: DatabaseCategory): string {
  return `<p class="data-cleanup-note">${categoryNote(category, safetyKey(category.safety))}</p>
    <p class="data-cleanup-note">${t("data.db.readOnlyBody")}</p>
    <p class="field-help data-cleanup-note">${t("data.db.readOnlyProtect")}</p>`;
}

// categoryDialogBody is the inner body shared by the full panel render and the
// in-place dialog refresh. main.ts replaces only `.data-db-dialog-body` while a
// modal is open, because the global render deliberately defers then.
export function categoryDialogBody(state: DataRetentionViewState, category: DatabaseCategory): string {
  if (category.cleanup === "hardware_io") return hardwareDialogBody(state, category);
  if (category.cleanup === "conversation") return conversationDialogBody(state, category);
  return readOnlyDialogBody(category);
}

function categoryDialog(state: DataRetentionViewState, category: DatabaseCategory): string {
  let title = categoryName(category);
  if (category.cleanup === "hardware_io") {
    title = `${categoryName(category)} · ${t("data.db.hardwareTitle")}`;
  } else if (category.cleanup === "conversation") {
    title = `${categoryName(category)} · ${t("data.db.conversationTitle")}`;
  }
  return dialogShell(category.id, title, categoryDialogBody(state, category));
}

function offlineCompactionResultSummary(result: OfflineCompactionResult): string {
  const reason = result.reason
    ? `<p class="field-help" data-offline-result-reason>${escapeHTML(offlineCompactionReasonText(result.reason))}</p>`
    : "";
  const sizes = result.before_total_bytes > 0
    ? `<p data-offline-result-size>${t("data.db.offlineResultSize", {
      before: formatBytes(result.before_total_bytes),
      after: formatBytes(result.after_total_bytes),
      freed: formatBytes(result.freed_bytes),
    })}</p>`
    : "";
  const integrity = result.integrity_check
    ? `<p class="field-help" data-offline-result-integrity>${escapeHTML(t("data.db.offlineResultIntegrity", {integrity: result.integrity_check}))}</p>`
    : "";
  const rows = result.row_counts_ok
    ? `<p class="field-help" data-offline-result-rows>${t("data.db.offlineResultRows")}</p>`
    : "";
  const backup = result.backup_path
    ? `<p class="field-help" data-offline-result-backup>${escapeHTML(result.backup_kept
      ? t("data.db.offlineResultBackup", {path: result.backup_path})
      : t("data.db.offlineResultBackupDeleted", {path: result.backup_path}))}</p>`
    : "";
  const failure = result.error
    ? `<p class="form-error" data-offline-result-error>${escapeHTML(t("data.db.offlineResultError", {message: result.error}))}</p>`
    : "";
  return `<div class="data-db-result" data-offline-result data-offline-result-status="${escapeHTML(result.status)}">
    <p data-offline-result-status-text><strong>${escapeHTML(t("data.db.offlineResult", {status: offlineCompactionStatusText(result.status)}))}</strong></p>
    <p class="field-help" data-offline-result-time>${escapeHTML(t("data.db.offlineResultTime", {time: formatTime(result.finished_at)}))}</p>
    ${reason}${sizes}${integrity}${rows}${backup}${failure}
  </div>`;
}

export function offlineCompactionDialogBody(state: DataRetentionViewState): string {
  const info = state.offlineCompaction;
  if (!info && state.offlineCompactionError) {
    return `<p class="form-error" data-offline-compaction-error>${escapeHTML(t("data.db.offlineLoadFailed", {message: state.offlineCompactionError}))}</p>
      <div class="data-cleanup-actions">
        <button type="button" data-offline-compaction-refresh ${state.offlineCompactionLoading ? "disabled" : ""}>${t("common.retry")}</button>
      </div>`;
  }
  if (!info) {
    return `<p class="field-help" data-offline-compaction-reading>${t("data.db.offlineReading")}</p>`;
  }
  const latest = info.latest_result
    ? offlineCompactionResultSummary(info.latest_result)
    : `<p class="field-help" data-offline-no-result>${t("data.db.offlineNoResult")}</p>`;
  return `<p class="data-cleanup-note">${t("data.db.offlineIntro")}</p>
    <div class="data-db-files data-db-offline-plan">
      <div><span class="field-help">${t("data.db.offlineFile")}</span><strong>${formatBytes(info.file_bytes)}</strong></div>
      <div><span class="field-help">${t("data.db.offlineWAL")}</span><strong>${formatBytes(info.wal_bytes)}</strong></div>
      <div><span class="field-help">${t("data.db.offlineSHM")}</span><strong>${formatBytes(info.shm_bytes)}</strong></div>
      <div><span class="field-help">${t("data.db.offlineRequired")}</span><strong>${formatBytes(info.required_bytes)}</strong></div>
      <div><span class="field-help">${t("data.db.offlineDisk")}</span><strong>${formatBytes(info.disk_free_bytes)}</strong></div>
      <div class="data-db-path"><span class="field-help">${t("data.db.offlineDataDir")}</span><code>${escapeHTML(info.data_dir)}</code></div>
      <div class="data-db-path"><span class="field-help">${t("data.db.offlineBackupDir")}</span><code>${escapeHTML(info.backup_dir)}</code></div>
    </div>
    <h4 class="data-offline-steps-title">${t("data.db.offlineStepsTitle")}</h4>
    <ol class="data-offline-steps">
      <li>${t("data.db.offlineStep1")}</li>
      <li>${t("data.db.offlineStep2")}</li>
      <li>${t("data.db.offlineStep3")}</li>
      <li>${t("data.db.offlineStep4")}</li>
    </ol>
    <div class="data-offline-command">
      <span class="field-help">${t("data.db.offlineCommand")}</span>
      <pre><code data-offline-command>${escapeHTML(info.command)}</code></pre>
      <button type="button" data-offline-copy>${t("data.db.offlineCopy")}</button>
    </div>
    <p class="field-help data-cleanup-note">${t("data.db.offlineHelp")}</p>
    <div class="data-cleanup-actions">
      <button type="button" data-offline-compaction-refresh ${state.offlineCompactionLoading ? "disabled" : ""}>${state.offlineCompactionLoading ? t("common.loading") : t("common.refresh")}</button>
    </div>
    <section class="data-offline-latest" data-offline-latest>
      <strong>${t("data.db.offlineLatest")}</strong>
      ${latest}
    </section>`;
}

function offlineCompactionDialog(state: DataRetentionViewState): string {
  return `<dialog class="data-db-dialog" data-offline-compact-dialog aria-labelledby="data-offline-compact-title">
    <header class="data-db-dialog-head">
      <strong id="data-offline-compact-title">${t("data.db.offlineTitle")}</strong>
      <button type="button" class="icon-button" data-offline-compact-close aria-label="${t("data.db.close")}">×</button>
    </header>
    <div class="data-db-dialog-body">${offlineCompactionDialogBody(state)}</div>
  </dialog>`;
}

export function renderDatabasePanel(state: DataRetentionViewState): string {
  const stats = state.database;
  const error = state.error ? `<div class="security-warning active" data-db-error>${escapeHTML(state.error)}</div>` : "";
  if (!stats) {
    return `<section class="panel spaced data-db-panel">
      <div class="panel-head"><strong>${t("data.db.title")}</strong></div>
      ${error}
      <p class="field-help data-cleanup-note">${state.loading ? t("data.db.reading") : t("data.db.unavailable")}</p>
      ${offlineCompactionDialog(state)}
    </section>`;
  }
  const collected = t(stats.cached ? "data.db.collectedCached" : "data.db.collected", {ms: stats.duration_ms, time: escapeHTML(formatTime(stats.collected_at))});
  return `<section class="panel spaced data-db-panel">
    <div class="panel-head"><strong>${t("data.db.title")}</strong><small class="field-help" data-db-collected>${collected}</small></div>
    ${error}
    <div class="data-db-files">
      <div class="data-db-file-row">
        <div><span class="field-help">${t("data.db.file")}</span><strong>${formatBytes(stats.file_bytes)}</strong></div>
      </div>
      <div><span class="field-help">WAL</span><strong>${formatBytes(stats.wal_bytes)}</strong></div>
      <div><span class="field-help">SHM</span><strong>${formatBytes(stats.shm_bytes)}</strong></div>
      <div><span class="field-help">${t("data.db.used")}</span><strong>${formatBytes(stats.used_bytes)}</strong></div>
      <div><span class="field-help">${t("data.db.free")}</span><strong>${formatBytes(stats.free_bytes)}</strong></div>
    </div>
    <p class="field-help data-cleanup-note">${t("data.db.categoriesIntro", {tables: stats.table_count})}</p>
    <div class="data-db-categories">${stats.categories.map(categoryCard).join("")}</div>
    <p class="field-help data-cleanup-note">${t("data.db.estimateNote")}</p>
    <p class="field-help data-cleanup-note">${t("data.db.vacuumNote")}</p>
    ${stats.categories.map(category => categoryDialog(state, category)).join("")}
    ${offlineCompactionDialog(state)}
  </section>`;
}
