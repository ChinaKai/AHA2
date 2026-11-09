// Owner-decision approvals may carry the choices the pet is asking about in
// payload.options (for example ["replay","skip"]). The chosen value is sent
// back verbatim as the approval note, so labels are display-only.

const optionLabels: Record<string, string> = {
  replay: "重放",
  skip: "跳过",
};

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
