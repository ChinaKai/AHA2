import {formatLocaleDateTime, formatLocaleNumber, t} from "./i18n.js";
import type {
  ConversationPurgePlan,
  ConversationPurgeResult,
  ConversationRetentionSettings,
  DatabaseCategory,
  DatabaseCompactionPlan,
  DatabaseCompactionResult,
  DatabaseSafety,
  DatabaseStats,
  DatabaseTableStat,
  HardwareIOPurgePlan,
  HardwareIOPurgeResult,
  HardwareIORetentionSettings,
} from "./types.js";

// Limits mirror internal/domain/data_retention.go. The server re-validates;
// these only drive the form state.
export const RETENTION_MIN_DAYS = 1;
export const RETENTION_MAX_DAYS = 3650;
export const PURGE_MIN_ROWS = 1000;
export const PURGE_MAX_ROWS = 1000000;

export interface DataRetentionViewState {
  database: DatabaseStats | null;
  loading: boolean;
  error: string;
  plan: HardwareIOPurgePlan | null;
  result: HardwareIOPurgeResult | null;
  busy: "" | "save" | "preview" | "purge";
  conversationPlan: ConversationPurgePlan | null;
  conversationResult: ConversationPurgeResult | null;
  conversationBusy: "" | "save" | "preview" | "purge";
  compactionPlan: DatabaseCompactionPlan | null;
  compactionResult: DatabaseCompactionResult | null;
  compactionBusy: boolean;
  compactionError: string;
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
export function retentionFormError(days: string, rows: string): string {
  const dayValue = Number(days);
  if (!days.trim() || !Number.isInteger(dayValue) || dayValue < RETENTION_MIN_DAYS || dayValue > RETENTION_MAX_DAYS) {
    return t("data.db.invalidDays", {min: RETENTION_MIN_DAYS, max: RETENTION_MAX_DAYS});
  }
  const rowValue = Number(rows);
  if (!rows.trim() || !Number.isInteger(rowValue) || rowValue < PURGE_MIN_ROWS || rowValue > PURGE_MAX_ROWS) {
    return t("data.db.invalidRows", {min: formatLocaleNumber(PURGE_MIN_ROWS), max: formatLocaleNumber(PURGE_MAX_ROWS)});
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

export function compactionReasonText(reason: string): string {
  const key = `data.db.compactReason.${reason}`;
  const text = t(key);
  return text === key ? reason : text;
}

export function compactionBlockedText(plan: DatabaseCompactionPlan): string {
  return plan.blocking_reasons.map(compactionReasonText).join("；");
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

function hardwareDialogBody(state: DataRetentionViewState, category: DatabaseCategory): string {
  const retention: HardwareIORetentionSettings = state.database?.retention ?? {retention_days: 30, max_delete_rows: 200000};
  const range = state.database?.hardware_io;
  const days = retention.retention_days;
  const rows = retention.max_delete_rows;
  const busy = Boolean(state.busy);
  const canPurge = purgeEnabled(state, days, rows);
  return `<p class="data-cleanup-note">${categoryNote(category, safetyKey(category.safety))}</p>
    <p class="data-cleanup-note" data-db-range>${range?.max_sequence
      ? t("data.db.range", {oldest: escapeHTML(formatTime(range.oldest_at)), newest: escapeHTML(formatTime(range.newest_at)), rows: formatLocaleNumber(range.estimated_rows)})
      : t("data.db.rangeEmpty")}</p>
    <div class="data-cleanup-actions data-db-form">
      <label>${t("data.db.retentionDays")} <input id="data-hardware-io-days" type="number" min="${RETENTION_MIN_DAYS}" max="${RETENTION_MAX_DAYS}" step="1" value="${days}" ${busy ? "disabled" : ""}></label>
      <label>${t("data.db.maxRows")} <input id="data-hardware-io-rows" type="number" min="${PURGE_MIN_ROWS}" max="${PURGE_MAX_ROWS}" step="1000" value="${rows}" ${busy ? "disabled" : ""}></label>
      <button type="button" data-hardware-io-save ${busy ? "disabled" : ""}>${t("data.db.save")}</button>
      <button type="button" data-hardware-io-preview ${busy ? "disabled" : ""}>${t("data.db.preview")}</button>
      <button type="button" class="danger" data-hardware-io-purge ${canPurge ? "" : "disabled"}>${t("data.db.purge")}</button>
    </div>
    <p class="form-error" data-hardware-io-form-error hidden></p>
    <p class="field-help data-cleanup-note">${t("data.db.purgeHelp")}</p>
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
      <button type="button" class="danger" data-conversation-purge ${canPurge ? "" : "disabled"}>${t("data.db.purge")}</button>
    </div>
    <p class="form-error" data-conversation-form-error hidden></p>
    <p class="field-help data-cleanup-note">${t("data.db.conversationHelp")}</p>
    ${state.conversationPlan ? conversationPlanSummary(state.conversationPlan) : ""}
    ${state.conversationResult ? conversationResultSummary(state.conversationResult) : ""}
    <p class="field-help data-cleanup-note">${t("data.db.vacuumNote")}</p>`;
}

function readOnlyDialogBody(category: DatabaseCategory): string {
  return `<p class="data-cleanup-note">${categoryNote(category, safetyKey(category.safety))}</p>
    <p class="data-cleanup-note">${t("data.db.readOnlyBody")}</p>
    <p class="field-help data-cleanup-note">${t("data.db.readOnlyProtect")}</p>`;
}

function categoryDialog(state: DataRetentionViewState, category: DatabaseCategory): string {
  let body = readOnlyDialogBody(category);
  let title = categoryName(category);
  if (category.cleanup === "hardware_io") {
    body = hardwareDialogBody(state, category);
    title = `${categoryName(category)} · ${t("data.db.hardwareTitle")}`;
  } else if (category.cleanup === "conversation") {
    body = conversationDialogBody(state, category);
    title = `${categoryName(category)} · ${t("data.db.conversationTitle")}`;
  }
  return dialogShell(category.id, title, body);
}

function compactionResultSummary(result: DatabaseCompactionResult): string {
  if (result.error) {
    const restored = result.restored
      ? `<p class="field-help" data-compact-restored>${t("data.db.compactRestored")}</p>`
      : result.restore_error
        ? `<p class="form-error" data-compact-restore-error>${escapeHTML(t("data.db.compactRestoreFailed", {path: result.backup_path || "—"}))}: ${escapeHTML(result.restore_error)}</p>`
        : "";
    return `<div class="data-db-result" data-compact-result>
      <p class="form-error">${escapeHTML(t("data.db.compactFailed", {message: result.error}))}</p>
      ${result.backup_path ? `<p class="field-help">${escapeHTML(t("data.db.compactBackup", {path: result.backup_path}))}</p>` : ""}
      ${restored}
    </div>`;
  }
  return `<div class="data-db-result" data-compact-result>
    <p>${t("data.db.compactResult", {before: formatBytes(result.before_bytes), after: formatBytes(result.after_bytes), freed: formatBytes(result.freed_bytes), integrity: escapeHTML(result.integrity_check || "—")})}</p>
    <p class="field-help">${t("data.db.compactResultCounts")}</p>
    ${result.backup_path ? `<p class="field-help">${escapeHTML(t("data.db.compactBackup", {path: result.backup_path}))}</p>` : ""}
  </div>`;
}

function compactionDialog(state: DataRetentionViewState): string {
  const plan = state.compactionPlan;
  const busy = state.compactionBusy;
  let body: string;
  if (!plan && state.compactionError) {
    // The plan request is independent of the stats panel; when the DB read
    // fails we surface the error here instead of an endless "reading" line.
    body = `<p class="form-error" data-compact-error>${escapeHTML(t("data.db.compactLoadFailed", {message: state.compactionError}))}</p>
      <div class="data-cleanup-actions">
        <button type="button" data-compact-refresh ${busy ? "disabled" : ""}>${t("common.retry")}</button>
      </div>`;
  } else if (!plan) {
    body = `<p class="field-help">${t("data.db.compactReading")}</p>`;
  } else {
    const blocked = plan.blocking_reasons.length
      ? `<p class="form-error" data-compact-blocked>${escapeHTML(t("data.db.compactBlocked", {reasons: compactionBlockedText(plan)}))}</p>`
      : `<p class="field-help" data-compact-ready>${t("data.db.compactReady")}</p>`;
    body = `<p class="data-cleanup-note">${t("data.db.compactIntro")}</p>
      <div class="data-db-files data-db-compact-plan">
        <div><span class="field-help">${t("data.db.compactFile")}</span><strong>${formatBytes(plan.file_bytes)}</strong></div>
        <div><span class="field-help">${t("data.db.compactFree")}</span><strong>${t("data.db.compactFreeValue", {pages: formatLocaleNumber(plan.freelist_count), size: formatBytes(plan.free_bytes)})}</strong></div>
        <div><span class="field-help">${t("data.db.compactReclaim")}</span><strong>${formatBytes(plan.estimated_reclaimed_bytes)}</strong></div>
        <div><span class="field-help">${t("data.db.compactRequired")}</span><strong>${formatBytes(plan.required_temp_bytes)}</strong></div>
        <div><span class="field-help">${t("data.db.compactDisk")}</span><strong>${formatBytes(plan.disk_free_bytes)}</strong></div>
        <div><span class="field-help">${t("data.db.compactWriters")}</span><strong>${t("data.db.compactActive", {tasks: plan.active_tasks, turns: plan.active_turns, approvals: plan.pending_approvals + plan.pet_approvals})}</strong></div>
      </div>
      <p class="field-help data-cleanup-note">${t("data.db.compactStop")}</p>
      ${blocked}
      <div class="data-cleanup-actions">
        <button type="button" data-compact-refresh ${busy ? "disabled" : ""}>${t("common.refresh")}</button>
        <button type="button" class="danger" data-compact-run ${plan.can_compact && !busy ? "" : "disabled"}>${t("data.db.compactRun")}</button>
      </div>
      <p class="field-help data-cleanup-note">${t("data.db.compactHelp")}</p>
      ${state.compactionResult ? compactionResultSummary(state.compactionResult) : ""}`;
  }
  return `<dialog class="data-db-dialog" data-compact-dialog aria-labelledby="data-compact-title">
    <header class="data-db-dialog-head">
      <strong id="data-compact-title">${t("data.db.compactTitle")}</strong>
      <button type="button" class="icon-button" data-compact-close aria-label="${t("data.db.close")}">×</button>
    </header>
    <div class="data-db-dialog-body">${body}</div>
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
      ${compactionDialog(state)}
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
    ${compactionDialog(state)}
  </section>`;
}
