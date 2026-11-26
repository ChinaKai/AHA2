// Owner-decision approvals may carry the choices the pet is asking about in
// payload.options (for example ["replay","skip"]). The chosen value is sent
// back verbatim as the approval note, so labels are display-only.

const optionLabels: Record<string, string> = {
  replay: "重放",
  skip: "跳过",
};

export type PetApprovalTranslate = (key: string, values?: Record<string, string | number>) => string;

// Shape shared with the API row and the test fixtures. Timestamps stay
// optional because old responses may omit resolved_at.
export interface PetApprovalLike {
  id: string;
  task_id?: string;
  kind?: string;
  title?: string;
  summary?: string;
  reason?: string;
  risk?: string;
  payload?: unknown;
  status?: string;
  resolution?: string;
  decision_reason?: string;
  created_at?: string;
  resolved_at?: string;
}

export interface PetApprovalRenderOptions {
  translate: PetApprovalTranslate;
  /** Reference clock for the waiting duration; defaults to Date.now(). */
  now?: number;
  statusLabel?: (status: string) => string;
  /** Replaces the default empty hint, e.g. "no pending approvals". */
  empty?: string;
}

function escapeHTML(value: unknown): string {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

export function parsePetApprovalPayload(payload: unknown): Record<string, unknown> {
  let value = payload;
  if (typeof value === "string") {
    if (!value.trim()) return {};
    try {
      value = JSON.parse(value);
    } catch {
      return {};
    }
  }
  return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : {};
}

// Returns the selectable options, or [] when the approval should keep the
// plain approve/reject behaviour (other kinds, resolved, missing or bad payload).
export function petApprovalOptions(approval: {kind?: string; status?: string; payload?: unknown}): string[] {
  if (approval.kind !== "owner_decision" || approval.status !== "pending") return [];
  const options = parsePetApprovalPayload(approval.payload).options;
  if (!Array.isArray(options)) return [];
  const result: string[] = [];
  for (const option of options) {
    if (typeof option !== "string" || !option.trim() || result.includes(option)) continue;
    result.push(option);
  }
  return result;
}

export function petApprovalOptionLabel(value: string): string {
  return optionLabels[value] ?? value;
}

export function renderPetApprovalOptionPicker(approvalID: string, options: string[], translate: (text: string) => string = text => text): string {
  if (!options.length) return "";
  const choices = options.map(option => `<option value="${escapeHTML(option)}">${escapeHTML(translate(petApprovalOptionLabel(option)))}</option>`).join("");
  return `<label class="pet-approval-choice">${escapeHTML(translate("选择方案"))}<select data-pet-approval-option="${escapeHTML(approvalID)}"><option value="">${escapeHTML(translate("请选择方案"))}</option>${choices}</select></label>`;
}

// The approve note must be one of the offered options; "" means not chosen.
export function selectedPetApprovalOption(options: string[], selected: string | undefined): string {
  return selected && options.includes(selected) ? selected : "";
}

// Batch model deletes snapshot the affected names and count in the approval
// payload so the approval center can show them even after the models are gone.
export function petApprovalModelBatch(approval: {kind?: string; payload?: unknown}): {count: number; names: string[]} | null {
  if (approval.kind !== "delete_models") return null;
  const payload = parsePetApprovalPayload(approval.payload);
  const names = Array.isArray(payload.model_names)
    ? payload.model_names.filter((name): name is string => typeof name === "string" && name.trim() !== "")
    : [];
  const rawCount = Number(payload.count);
  const count = Number.isFinite(rawCount) && rawCount > 0 ? Math.trunc(rawCount) : names.length;
  if (count <= 0 && !names.length) return null;
  return {count, names};
}

export function renderPetApprovalModelBatch(
  approval: {kind?: string; payload?: unknown},
  translate: (text: string) => string = text => text,
): string {
  const batch = petApprovalModelBatch(approval);
  if (!batch) return "";
  const names = batch.names.length ? ` · ${batch.names.join("、")}` : "";
  return `<p class="pet-approval-models"><strong>${escapeHTML(translate("模型"))}:</strong> ${escapeHTML(String(batch.count))} 个${escapeHTML(names)}</p>`;
}

/* Compact approval rows ---------------------------------------------------- */

const kindIconPaths: Record<string, string> = {
  idea: '<path d="M9 18h6M10 21h4M12 3a6 6 0 0 0-3.5 10.9c.6.4 1 1.1 1 1.9V16h5v-.2c0-.8.4-1.5 1-1.9A6 6 0 0 0 12 3Z"/><path d="M12 17v-5"/>',
  decision: '<path d="M12 3 4 6v6c0 4.5 3.4 7.6 8 9 4.6-1.4 8-4.5 8-9V6Z"/><path d="m9 12 2 2 4-4"/>',
  delete: '<path d="M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13"/><path d="M10 11v6M14 11v6"/>',
  approval: '<path d="M8 4h8a2 2 0 0 1 2 2v14l-6-3-6 3V6a2 2 0 0 1 2-2Z"/><path d="M9 8h6M9 12h6"/>',
};

// The row icon is inline SVG so pet_approvals stays free of the page icon
// module; tests can assert the marker without a DOM.
export function petApprovalKindIcon(kind: string): string {
  const path = kind === "idea"
    ? kindIconPaths.idea
    : kind === "owner_decision"
      ? kindIconPaths.decision
      : kind.startsWith("delete")
        ? kindIconPaths.delete
        : kindIconPaths.approval;
  return `<svg class="pet-approval-kind-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${path}</svg>`;
}

export function petApprovalKindLabel(kind: string, translate: PetApprovalTranslate): string {
  return kind === "idea" ? translate("petWorkbench.ideas") : translate("petWorkbench.approvals");
}

export function petApprovalStatusLabel(status: string, translate: PetApprovalTranslate): string {
  const normalized = status || "pending";
  const key = `petWorkbench.approvalStatus.${normalized}`;
  const label = translate(key);
  return label === key ? normalized : label;
}

export function petApprovalRiskLabel(risk: string, translate: PetApprovalTranslate): string {
  const normalized = String(risk || "").trim().toLowerCase();
  const key = normalized === "low" || normalized === "medium" || normalized === "high"
    ? `petWorkbench.risk${normalized[0].toUpperCase()}${normalized.slice(1)}`
    : "petWorkbench.riskUnknown";
  const label = translate(key);
  return label === key ? (normalized || translate("petWorkbench.riskUnknown")) : label;
}

// The filter button badge counts the two filter groups, not the result rows:
// the owner needs to know how many conditions narrowed the list.
export function petApprovalFilterCount(kind: string, status: string): number {
  return (kind && kind !== "all" ? 1 : 0) + (status && status !== "all" ? 1 : 0);
}

// A whole hour or day drops the zero part: "2 小时", not "2 小时 0 分钟".
// Under an hour the minutes are always shown, including "0 分钟".
export function petApprovalDurationText(minutes: number, translate: PetApprovalTranslate): string {
  const value = Number.isFinite(minutes) && minutes > 0 ? Math.trunc(minutes) : 0;
  if (value < 60) return translate("petWorkbench.waitMinutes", {minutes: String(value)});
  const hours = Math.floor(value / 60);
  if (hours < 24) {
    const rest = value % 60;
    return rest
      ? translate("petWorkbench.waitHours", {hours: String(hours), minutes: String(rest)})
      : translate("petWorkbench.waitHoursOnly", {hours: String(hours)});
  }
  const days = Math.floor(hours / 24);
  const restHours = hours % 24;
  return restHours
    ? translate("petWorkbench.waitDays", {days: String(days), hours: String(restHours)})
    : translate("petWorkbench.waitDaysOnly", {days: String(days)});
}

// Waiting time is measured from creation until resolution; a pending row keeps
// growing with the reference clock. Clock skew or a bad timestamp renders as
// zero instead of NaN.
export function petApprovalWaitingMinutes(item: PetApprovalLike, now = Date.now()): number | null {
  const start = Date.parse(String(item.created_at || ""));
  if (!Number.isFinite(start)) return null;
  const end = item.resolved_at ? Date.parse(String(item.resolved_at)) : now;
  if (!Number.isFinite(end)) return null;
  return Math.max(0, Math.floor((end - start) / 60_000));
}

export function petApprovalWaitingText(item: PetApprovalLike, now: number, translate: PetApprovalTranslate): string {
  const minutes = petApprovalWaitingMinutes(item, now);
  if (minutes === null) return "";
  return translate("petWorkbench.approvalWaiting", {duration: petApprovalDurationText(minutes, translate)});
}

// Pending and executing rows come first, longest wait first so the oldest
// decision is at the top. Resolved rows follow newest-first. The input array is
// left untouched because the tab keeps the server response for counts.
export function sortPetApprovals<T extends PetApprovalLike>(items: T[]): T[] {
  const rank = (status: string | undefined): number => {
    if (status === "pending") return 0;
    if (status === "executing") return 1;
    return 2;
  };
  const time = (value: string | undefined): number => {
    const parsed = Date.parse(String(value || ""));
    return Number.isFinite(parsed) ? parsed : 0;
  };
  return [...items].sort((left, right) => {
    const leftRank = rank(left.status);
    const rightRank = rank(right.status);
    if (leftRank !== rightRank) return leftRank - rightRank;
    if (leftRank < 2) {
      return time(left.created_at) - time(right.created_at);
    }
    return time(right.resolved_at || right.created_at) - time(left.resolved_at || left.created_at);
  });
}

export function renderPetApprovalRow(item: PetApprovalLike, options: PetApprovalRenderOptions): string {
  const translate = options.translate;
  const status = item.status || "pending";
  const kindLabel = petApprovalKindLabel(String(item.kind || ""), translate);
  const statusLabel = options.statusLabel
    ? options.statusLabel(status)
    : petApprovalStatusLabel(status, translate);
  const waiting = petApprovalWaitingText(item, options.now ?? Date.now(), translate);
  const risk = String(item.risk || "").trim();
  const riskLabel = risk ? petApprovalRiskLabel(risk, translate) : "";
  return `<button type="button" class="pet-approval-row ${status}" data-pet-approval-open="${escapeHTML(item.id)}">
    <span class="pet-approval-kind">${petApprovalKindIcon(String(item.kind || ""))}</span>
    <span class="pet-approval-row-main"><strong>${escapeHTML(item.title || "")}</strong><small>${escapeHTML(kindLabel)} · ${escapeHTML(statusLabel)}</small></span>
    ${riskLabel ? `<span class="pet-approval-risk risk-${escapeHTML(risk.toLowerCase())}">${escapeHTML(riskLabel)}</span>` : ""}
    ${waiting ? `<span class="pet-approval-wait">${escapeHTML(waiting)}</span>` : ""}
  </button>`;
}

export function renderPetApprovalList(items: PetApprovalLike[], options: PetApprovalRenderOptions): string {
  if (items.length === 0) {
    return `<div class="field-help">${escapeHTML(options.empty || options.translate("petWorkbench.emptyFiltered"))}</div>`;
  }
  return `<div class="pet-approval-rows">${sortPetApprovals(items).map(item => renderPetApprovalRow(item, options)).join("")}</div>`;
}

// The two filter rows live behind one button; this panel is rendered into a
// dialog (full screen on phones) and applies each click immediately.
export function renderPetApprovalFilters(kind: string, status: string, translate: PetApprovalTranslate): string {
  const kindFilters: Array<[string, string]> = [
    ["all", translate("petWorkbench.filterAll")],
    ["idea", translate("petWorkbench.ideas")],
    ["decision", translate("petWorkbench.approvals")],
  ];
  const statusFilters: Array<[string, string]> = [
    ["all", translate("petWorkbench.filterAll")],
    ["pending", translate("petWorkbench.filterPending")],
    ["approved", translate("petWorkbench.filterApproved")],
    ["rejected", translate("petWorkbench.filterRejected")],
    ["cancelled", translate("petWorkbench.filterCancelled")],
  ];
  const count = petApprovalFilterCount(kind, status);
  // The tab opens on pending because that is the owner's queue; a non-pending
  // status is an explicit lookup, and this hint keeps it from looking broken.
  const pendingHint = status !== "pending"
    ? `<p class="field-help" data-pet-approval-pending-hint>${escapeHTML(translate("petWorkbench.pendingFirstHint"))}</p>`
    : "";
  return `<div class="pet-approval-filter-panel">
    <p class="pet-approval-filter-summary">${escapeHTML(translate("petWorkbench.filterActive", {count: String(count)}))}</p>
    ${pendingHint}
    <section><h3>${escapeHTML(translate("petWorkbench.filterKinds"))}</h3><div class="pet-approval-filters">${kindFilters.map(([id, label]) => `<button type="button" class="${kind === id ? "active" : ""}" data-pet-approval-filter="${id}">${escapeHTML(label)}</button>`).join("")}</div></section>
    <section><h3>${escapeHTML(translate("petWorkbench.filterStatuses"))}</h3><div class="pet-approval-filters">${statusFilters.map(([id, label]) => `<button type="button" class="${status === id ? "active" : ""}" data-pet-approval-status-filter="${id}">${escapeHTML(label)}</button>`).join("")}</div></section>
    <div class="pet-approval-filter-actions"><button type="button" class="primary" data-pet-approval-filters-close>${escapeHTML(translate("petWorkbench.filterDone"))}</button></div>
  </div>`;
}

export function renderPetApprovalDetail(item: PetApprovalLike, options: PetApprovalRenderOptions): string {
  const translate = options.translate;
  const status = item.status || "pending";
  const executing = status === "executing";
  const pending = status === "pending";
  const kindLabel = petApprovalKindLabel(String(item.kind || ""), translate);
  const statusLabel = options.statusLabel
    ? options.statusLabel(status)
    : petApprovalStatusLabel(status, translate);
  const risk = String(item.risk || "").trim();
  const riskLabel = risk ? petApprovalRiskLabel(risk, translate) : "";
  const waiting = petApprovalWaitingText(item, options.now ?? Date.now(), translate);
  const picker = pending ? renderPetApprovalOptionPicker(item.id, petApprovalOptions(item), translate) : "";
  const actions = pending
    ? `${picker}<div class="pet-approval-actions"><button type="button" class="primary" data-pet-approval-decision="approve" data-pet-approval-id="${escapeHTML(item.id)}">${escapeHTML(translate("批准"))}</button><button type="button" data-pet-approval-decision="reject" data-pet-approval-id="${escapeHTML(item.id)}">${escapeHTML(translate("拒绝"))}</button><button type="button" data-pet-approval-decision="cancel" data-pet-approval-id="${escapeHTML(item.id)}">${escapeHTML(translate("取消"))}</button></div>`
    : "";
  const meta = [kindLabel, statusLabel, riskLabel, waiting].filter(Boolean).join(" · ");
  return `<article class="pet-approval-card ${pending ? "pending" : ""} ${executing ? "executing" : ""}">
    <header class="pet-approval-detail-head">
      <span class="pet-approval-kind">${petApprovalKindIcon(String(item.kind || ""))}</span>
      <div><strong>${escapeHTML(item.title || "")}</strong><small>${escapeHTML(meta)}</small></div>
      <button type="button" class="icon-button" data-pet-approval-detail-close aria-label="${escapeHTML(translate("petWorkbench.close"))}">×</button>
    </header>
    ${item.reason ? `<p class="pet-approval-reason"><strong>${escapeHTML(translate("petApproval.reason"))}:</strong> ${escapeHTML(item.reason)}</p>` : ""}
    <p>${escapeHTML(item.summary || "")}</p>
    ${renderPetApprovalModelBatch(item, translate)}
    ${executing ? `<p class="field-help">${escapeHTML(translate("petWorkbench.approvalExecutingHint"))}</p>` : ""}
    ${item.decision_reason ? `<small>${escapeHTML(translate("petApproval.decisionReason"))}: ${escapeHTML(item.decision_reason)}</small>` : item.resolution ? `<small>${escapeHTML(item.resolution)}</small>` : ""}
    ${actions}
    <div data-pet-approval-error hidden></div>
  </article>`;
}

export function petPendingApprovalBadge(pending: number): string {
  return pending > 0 ? `<span class="task-tool-badge" aria-hidden="true">${pending > 99 ? "99+" : pending}</span>` : "";
}

export function createPetApprovalCounter(read: () => Promise<{pending: number}>, changed: () => void) {
  let pending = 0;
  let inFlight: Promise<void> | null = null;
  let rerun = false;
  return {
    get pending() { return pending; },
    refresh(afterMutation = false): Promise<void> {
      if (inFlight) {
        if (afterMutation) rerun = true;
        return inFlight;
      }
      inFlight = (async () => {
        do {
          rerun = false;
          const result = await read();
          if (!rerun) {
            pending = Math.max(0, Math.trunc(Number(result.pending) || 0));
            changed();
          }
        } while (rerun);
      })().finally(() => { inFlight = null; });
      return inFlight;
    },
  };
}
