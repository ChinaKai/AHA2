import {formatLocaleDateTime, formatLocaleNumber, t} from "./i18n.js";
import type {DatabaseStats, DatabaseTableStat, HardwareIOPurgePlan, HardwareIOPurgeResult, HardwareIORetentionSettings} from "./types.js";

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

export function skipReasonText(reason: string): string {
  if (!reason) return "";
  const key = `data.db.skip.${reason}`;
  const text = t(key);
  return text === key ? reason : text;
}

function tableRow(table: DatabaseTableStat): string {
  if (!table.exists) {
    return `<tr><td><code>${escapeHTML(table.name)}</code></td><td colspan="2" class="field-help">${t("data.db.tableMissing")}</td></tr>`;
  }
  const size = table.sample_rows > 0
    ? `≈ ${formatBytes(table.estimated_bytes)}<small class="field-help">${t("data.db.sampleNote", {rows: formatLocaleNumber(table.sample_rows), avg: formatBytes(table.avg_row_bytes)})}</small>`
    : "—";
  return `<tr data-db-table="${escapeHTML(table.name)}">
    <td><code>${escapeHTML(table.name)}</code></td>
    <td class="data-size">${formatRows(table.rows, table.rows_exact)}</td>
    <td class="data-size data-db-bytes">${size}</td>
  </tr>`;
}

function planSummary(plan: HardwareIOPurgePlan): string {
  const skip = plan.skip_reason ? `<p class="field-help" data-db-plan-skip>${escapeHTML(skipReasonText(plan.skip_reason))}</p>` : "";
  return `<div class="data-db-plan" data-db-plan>
    <p>${t("data.db.planCutoff", {cutoff: escapeHTML(formatTime(plan.cutoff)), days: plan.retention_days})}</p>
    <p><strong>${t("data.db.planRun", {rows: formatLocaleNumber(plan.run_rows), size: formatBytes(plan.run_bytes_estimate)})}</strong></p>
    <p class="field-help">${t("data.db.planEligible", {rows: formatLocaleNumber(plan.eligible_rows_estimate), size: formatBytes(plan.eligible_bytes_estimate), remaining: formatLocaleNumber(plan.remaining_eligible_estimate)})}</p>
    ${skip}
  </div>`;
}

function resultSummary(result: HardwareIOPurgeResult): string {
  const skip = result.skip_reason ? ` ${escapeHTML(skipReasonText(result.skip_reason))}` : "";
  const partial = result.partial ? ` ${t("data.db.resultPartial")}` : "";
  return `<p class="data-db-result" data-db-result>${t("data.db.resultDeleted", {rows: formatLocaleNumber(result.deleted_rows), batches: result.batches})}${partial}${skip}</p>`;
}

// purgeEnabled is true only for a preview that would delete rows and matches
// the values currently in the form, so the confirmed run is the one previewed.
export function purgeEnabled(state: DataRetentionViewState, days: number, rows: number): boolean {
  const plan = state.plan;
  return Boolean(plan && !state.busy && !plan.skip_reason && plan.run_rows > 0
    && plan.retention_days === days && plan.max_delete_rows === rows);
}

export function renderDatabasePanel(state: DataRetentionViewState): string {
  const stats = state.database;
  const error = state.error ? `<div class="security-warning active" data-db-error>${escapeHTML(state.error)}</div>` : "";
  if (!stats) {
    return `<section class="panel spaced data-db-panel">
      <div class="panel-head"><strong>${t("data.db.title")}</strong></div>
      ${error}
      <p class="field-help data-cleanup-note">${state.loading ? t("data.db.reading") : t("data.db.unavailable")}</p>
    </section>`;
  }
  const retention: HardwareIORetentionSettings = stats.retention;
  const range = stats.hardware_io;
  const busy = Boolean(state.busy);
  const days = retention.retention_days;
  const rows = retention.max_delete_rows;
  const canPurge = purgeEnabled(state, days, rows);
  return `<section class="panel spaced data-db-panel">
    <div class="panel-head"><strong>${t("data.db.title")}</strong><small class="field-help">${t("data.db.collected", {ms: stats.duration_ms})}</small></div>
    ${error}
    <div class="data-db-files">
      <div><span class="field-help">${t("data.db.file")}</span><strong>${formatBytes(stats.file_bytes)}</strong></div>
      <div><span class="field-help">WAL</span><strong>${formatBytes(stats.wal_bytes)}</strong></div>
      <div><span class="field-help">SHM</span><strong>${formatBytes(stats.shm_bytes)}</strong></div>
      <div><span class="field-help">${t("data.db.used")}</span><strong>${formatBytes(stats.used_bytes)}</strong></div>
      <div><span class="field-help">${t("data.db.free")}</span><strong>${formatBytes(stats.free_bytes)}</strong></div>
    </div>
    <div class="table-wrap"><table class="data-table data-db-table"><thead><tr><th>${t("data.db.table")}</th><th>${t("data.db.rows")}</th><th>${t("data.db.size")}</th></tr></thead>
      <tbody>${stats.tables.map(tableRow).join("")}</tbody></table></div>
    <p class="field-help data-cleanup-note">${t("data.db.estimateNote")}</p>
    <div class="data-db-retention">
      <div class="panel-head"><strong>${t("data.db.hardwareTitle")}</strong></div>
      <p class="data-cleanup-note" data-db-range>${range.max_sequence
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
      ${state.plan ? planSummary(state.plan) : ""}
      ${state.result ? resultSummary(state.result) : ""}
      <p class="field-help data-cleanup-note">${t("data.db.vacuumNote")}</p>
    </div>
  </section>`;
}
