import type {CodexAccount, CodexModelOption, Model} from "./types.js";

type RuntimeSelection = {
  backend?: string;
  model_source?: string;
  model_id?: string;
  codex_account_id?: string;
  wire_model?: string;
};

function escapeHTML(value: unknown): string {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

function accountName(account: CodexAccount): string {
  const name = account.label || account.email || account.account_id || account.id;
  const codex = (account.usage?.rate_limits || []).find(limit => limit.id === "codex");
  const weekly = [codex?.primary_window, codex?.secondary_window].find(window => {
    const days = Number(window?.limit_window_seconds || 0) / (24 * 60 * 60);
    return days >= 6 && days <= 8;
  });
  return weekly ? `${name} · 周额度已用 ${Number(weekly.used_percent || 0)}%` : name;
}

function accountModels(accounts: CodexAccount[], accountID: string): CodexModelOption[] {
  return accounts.find(account => account.id === accountID)?.available_models || [];
}

function option(value: string, label: string, selected: boolean): string {
  return `<option value="${escapeHTML(value)}" ${selected ? "selected" : ""}>${escapeHTML(label)}</option>`;
}

function envModelOptions(models: Model[], selectedID: string): string {
  const grouped = new Map<string, Model[]>();
  for (const model of models) {
    const provider = model.provider_name || model.provider_id || "未分组";
    grouped.set(provider, [...(grouped.get(provider) || []), model]);
  }
  return [...grouped.entries()].map(([provider, items]) =>
    `<optgroup label="${escapeHTML(provider)}">${items.map(model => option(model.id, model.display_name, model.id === selectedID)).join("")}</optgroup>`
  ).join("");
}

export function runtimeFieldsHTML(
  prefix: string,
  models: Model[],
  accounts: CodexAccount[],
  selection: RuntimeSelection = {},
  className = "",
): string {
  const source = selection.model_source === "official" ? "official" : "env";
  const backends = [...new Set([
    ...models.filter(model => model.source !== "official").map(model => model.backend),
    ...(accounts.length ? ["codex"] : []),
  ])];
  const backend = selection.backend || backends[0] || "";
  const accountID = selection.codex_account_id || accounts[0]?.id || "";
  const wireModels = accountModels(accounts, accountID);
  const runtimeClass = className ? ` ${className}` : "";
  const envModels = models.filter(model => model.source !== "official" && model.backend === backend);
  return `<div class="two runtime-picker${runtimeClass}">
    <label>Backend<select name="backend" id="${prefix}-backend">${backends.map(item => option(item, item === "codex" ? "Codex" : "Claude Code", item === backend)).join("")}</select></label>
    <label id="${prefix}-model-source-field">模型使用方式<select name="model_source" id="${prefix}-model-source"><option value="env" ${source === "env" ? "selected" : ""}>Env</option><option value="official" ${source === "official" ? "selected" : ""}>Official</option></select></label>
  </div>
  <label id="${prefix}-env-model-field" class="runtime-picker${runtimeClass}">Env 模型<select name="model_id" id="${prefix}-model">${envModelOptions(envModels, selection.model_id || "")}</select></label>
  <div id="${prefix}-official-fields" class="two runtime-picker${runtimeClass}" hidden>
    <label>Codex 账号<select name="codex_account_id" id="${prefix}-codex-account">${accounts.map(account => option(account.id, accountName(account), account.id === accountID)).join("")}</select></label>
    <label>官方模型<select name="wire_model" id="${prefix}-wire-model">${wireModels.map(model => option(model.wire_model, model.display_name || model.wire_model, model.wire_model === selection.wire_model)).join("")}</select></label>
  </div>`;
}

export function setRuntimeBackends(prefix: string, backends: string[]): void {
  const select = document.querySelector<HTMLSelectElement>(`#${prefix}-backend`);
  if (!select) return;
  const previous = select.value;
  select.innerHTML = backends.length
    ? backends.map(item => option(item, item === "codex" ? "Codex" : "Claude Code", item === previous)).join("")
    : '<option value="">该 Workspace 无可用的 Backend（请先检测）</option>';
  select.value = backends.includes(previous) ? previous : (backends[0] || "");
}

export function syncRuntimeFields(prefix: string, models: Model[], accounts: CodexAccount[]): void {
  const backend = document.querySelector<HTMLSelectElement>(`#${prefix}-backend`)?.value || "";
  const sourceField = document.querySelector<HTMLElement>(`#${prefix}-model-source-field`);
  const sourceSelect = document.querySelector<HTMLSelectElement>(`#${prefix}-model-source`);
  const envField = document.querySelector<HTMLElement>(`#${prefix}-env-model-field`);
  const modelSelect = document.querySelector<HTMLSelectElement>(`#${prefix}-model`);
  const officialFields = document.querySelector<HTMLElement>(`#${prefix}-official-fields`);
  const accountSelect = document.querySelector<HTMLSelectElement>(`#${prefix}-codex-account`);
  const wireSelect = document.querySelector<HTMLSelectElement>(`#${prefix}-wire-model`);
  if (!sourceField || !sourceSelect || !envField || !modelSelect || !officialFields || !accountSelect || !wireSelect) return;

  const supportsOfficial = backend === "codex";
  sourceField.hidden = !supportsOfficial;
  sourceSelect.disabled = !supportsOfficial;
  if (!supportsOfficial) sourceSelect.value = "env";
  const official = supportsOfficial && sourceSelect.value === "official";
  envField.hidden = official;
  modelSelect.disabled = official;
  officialFields.hidden = !official;
  accountSelect.disabled = !official;
  wireSelect.disabled = !official;

  if (official) {
    const accountID = accountSelect.value || accounts[0]?.id || "";
    if (accountID) accountSelect.value = accountID;
    const previousWire = wireSelect.value;
    const available = accountModels(accounts, accountID);
    wireSelect.innerHTML = available.length
      ? available.map(model => option(model.wire_model, model.display_name || model.wire_model, model.wire_model === previousWire)).join("")
      : '<option value="">该账号暂无可用模型，请先刷新账号</option>';
    if (!available.some(model => model.wire_model === previousWire)) wireSelect.value = available[0]?.wire_model || "";
  } else {
    const previousModel = modelSelect.value;
    const available = models.filter(model => model.source !== "official" && model.backend === backend);
    modelSelect.innerHTML = available.length
      ? envModelOptions(available, previousModel)
      : `<option value="">${backend ? "该 Backend 下暂无 Env 模型" : "请先选择 Backend"}</option>`;
    if (!available.some(model => model.id === previousModel)) modelSelect.value = available[0]?.id || "";
  }

  const effort = document.querySelector<HTMLSelectElement>(`#${prefix}-effort`);
  if (!effort) return;
  const previousEffort = effort.value;
  const effortLevels = backend === "claude"
    ? ["low", "medium", "high", "xhigh", "max"]
    : ["low", "medium", "high", "xhigh"];
  effort.innerHTML = effortLevels.map(level => option(level, level, level === previousEffort)).join("");
  const selectedModel = official
    ? accountModels(accounts, accountSelect.value).find(model => model.wire_model === wireSelect.value)
    : models.find(model => model.id === modelSelect.value);
  const desired = selectedModel?.default_reasoning_effort || "";
  effort.value = effortLevels.includes(desired)
    ? desired
    : (effortLevels.includes(previousEffort) ? previousEffort : "medium");
}

export function bindRuntimeFields(
  prefix: string,
  models: Model[],
  accounts: CodexAccount[],
  onBackendChange?: () => void,
): void {
  document.querySelector(`#${prefix}-backend`)?.addEventListener("change", () => {
    syncRuntimeFields(prefix, models, accounts);
    onBackendChange?.();
  });
  for (const suffix of ["model-source", "model", "codex-account", "wire-model"]) {
    document.querySelector(`#${prefix}-${suffix}`)?.addEventListener("change", () => syncRuntimeFields(prefix, models, accounts));
  }
  syncRuntimeFields(prefix, models, accounts);
}
