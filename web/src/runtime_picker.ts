import type {ClaudeModelOption, CodexAccount, CodexModelOption, Model} from "./types.js";

type RuntimeSelection = {
  backend?: string;
  model_source?: string;
  model_id?: string;
  codex_account_id?: string;
  wire_model?: string;
  stream_idle_timeout_ms?: number;
  stream_max_retries?: number;
};

export type ClaudeOfficialAvailability = "ready" | "loading" | "not_logged_in" | "unknown" | "unavailable";

export type ClaudeOfficialCatalog = {
  availability: ClaudeOfficialAvailability;
  models: ClaudeModelOption[];
  message?: string;
};

const CLAUDE_OFFICIAL_LABELS: Record<ClaudeOfficialAvailability, string> = {
  ready: "Official",
  loading: "Official（检测中）",
  not_logged_in: "Official（暂不可用）",
  unknown: "Official（待检测）",
  unavailable: "Official（暂不可用）",
};

const claudeOfficialCatalogs = new Map<string, ClaudeOfficialCatalog>();

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

export function resolveReasoningEffort(
  effortLevels: string[],
  savedEffort: string,
  modelDefault: string,
  preferModelDefault: boolean,
): string {
  if (preferModelDefault && effortLevels.includes(modelDefault)) return modelDefault;
  if (effortLevels.includes(savedEffort)) return savedEffort;
  if (effortLevels.includes(modelDefault)) return modelDefault;
  return effortLevels.includes("medium") ? "medium" : (effortLevels[0] || "");
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

function officialModelOptions(catalog: ClaudeOfficialCatalog | undefined, selected: string): string {
  const models = catalog?.models || [];
  if (models.length === 0) {
    const label = catalog?.message || "暂不可用";
    const selectedOption = selected ? option(selected, `${selected}（暂不可用）`, true) : "";
    return `${selectedOption}${option("", label, !selectedOption)}`;
  }
  return models.map(model => option(
    model.wire_model,
    model.display_name || model.wire_model,
    model.wire_model === selected,
  )).join("");
}

export function runtimeFieldsHTML(
  prefix: string,
  models: Model[],
  accounts: CodexAccount[],
  selection: RuntimeSelection = {},
  className = "",
): string {
  const source = selection.model_source === "official"
    ? "official"
    : selection.model_source === "claude_native" ? "claude_native" : "env";
  const backends = [...new Set([
    ...(selection.backend ? [selection.backend] : []),
    ...models.filter(model => model.source !== "official" && model.source !== "claude_native").map(model => model.backend),
    ...(accounts.length ? ["codex"] : []),
  ])];
  const backend = selection.backend || backends[0] || "";
  const accountID = selection.codex_account_id || accounts[0]?.id || "";
  const wireModels = accountModels(accounts, accountID);
  const claudeCatalog = claudeOfficialCatalogs.get(prefix);
  const runtimeClass = className ? ` ${className}` : "";
  const envModels = models.filter(model => model.source !== "official" && model.source !== "claude_native" && model.backend === backend);
  const official = backend === "codex" && source === "official";
  const native = backend === "claude" && source === "claude_native";
  const existingSelection = Object.keys(selection).length > 0;
  const streamIdleTimeoutMS = Number(selection.stream_idle_timeout_ms || 0) ||
    (existingSelection ? 300000 : 120000);
  const configuredStreamMaxRetries = Number(selection.stream_max_retries || 0);
  const streamMaxRetries = configuredStreamMaxRetries > 0
    ? configuredStreamMaxRetries
    : (existingSelection ? 5 : 2);
  return `<div class="two runtime-picker${runtimeClass}">
    <label>Backend<select name="backend" id="${prefix}-backend" required>${backends.map(item => option(item, item === "codex" ? "Codex" : "Claude Code", item === backend)).join("")}</select></label>
    <label id="${prefix}-model-source-field">模型使用方式<select name="model_source" id="${prefix}-model-source" ${existingSelection && source === "claude_native" ? 'data-initial-official="true"' : ""} required>${backend === "claude"
      ? `${option("env", "Env Provider", source === "env")}${option("claude_native", "Official", source === "claude_native")}`
      : `${option("env", "Env", source === "env")}${option("official", "Official", source === "official")}`}</select></label>
  </div>
  <label id="${prefix}-env-model-field" class="runtime-picker${runtimeClass}" ${official || native ? "hidden" : ""}>Env 模型<select name="model_id" id="${prefix}-model" ${official || native ? "disabled" : "required"}>${envModelOptions(envModels, selection.model_id || "")}</select></label>
  <div id="${prefix}-official-fields" class="two runtime-picker${runtimeClass}" ${official ? "" : "hidden"}>
    <label>Codex 账号<select name="codex_account_id" id="${prefix}-codex-account" required>${accounts.map(account => option(account.id, accountName(account), account.id === accountID)).join("")}</select></label>
    <label>官方模型<select name="wire_model" id="${prefix}-wire-model" required>${wireModels.map(model => option(model.wire_model, model.display_name || model.wire_model, model.wire_model === selection.wire_model)).join("")}</select></label>
  </div>
  <div id="${prefix}-claude-native-fields" class="two runtime-picker${runtimeClass}" ${native ? "" : "hidden"}>
    <label>官方模型<select name="wire_model" id="${prefix}-claude-native-model" ${native && claudeCatalog?.availability === "ready" ? "required" : "disabled"}>${officialModelOptions(claudeCatalog, selection.wire_model || "default")}</select></label>
    <div class="field-help" id="${prefix}-claude-native-help">${escapeHTML(claudeCatalog?.message || "选择 Official 后检测当前 Workspace 的 Claude Code 登录状态和模型。")}</div>
  </div>
  <div id="${prefix}-codex-stream-settings" class="two runtime-picker${runtimeClass}" ${backend === "codex" ? "" : "hidden"}>
    <label>SSE 无数据超时（秒）<input name="stream_idle_timeout_seconds" type="number" min="30" max="1800" step="1" value="${Math.round(streamIdleTimeoutMS / 1000)}" required></label>
    <label>流中断重试次数<input name="stream_max_retries" type="number" min="1" max="10" step="1" value="${streamMaxRetries}" required></label>
    <div class="field-help" id="${prefix}-codex-stream-official-help" hidden>Codex 官方账号使用 CLI 默认的流超时与重试参数；两项设置仅适用于自定义 Env Provider。</div>
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

export function syncRuntimeFields(prefix: string, models: Model[], accounts: CodexAccount[], preferModelDefault = true): void {
  const backend = document.querySelector<HTMLSelectElement>(`#${prefix}-backend`)?.value || "";
  const sourceField = document.querySelector<HTMLElement>(`#${prefix}-model-source-field`);
  const sourceSelect = document.querySelector<HTMLSelectElement>(`#${prefix}-model-source`);
  const envField = document.querySelector<HTMLElement>(`#${prefix}-env-model-field`);
  const modelSelect = document.querySelector<HTMLSelectElement>(`#${prefix}-model`);
  const officialFields = document.querySelector<HTMLElement>(`#${prefix}-official-fields`);
  const accountSelect = document.querySelector<HTMLSelectElement>(`#${prefix}-codex-account`);
  const wireSelect = document.querySelector<HTMLSelectElement>(`#${prefix}-wire-model`);
  const nativeFields = document.querySelector<HTMLElement>(`#${prefix}-claude-native-fields`);
  const nativeSelect = document.querySelector<HTMLSelectElement>(`#${prefix}-claude-native-model`);
  const streamSettings = document.querySelector<HTMLElement>(`#${prefix}-codex-stream-settings`);
  const streamOfficialHelp = document.querySelector<HTMLElement>(`#${prefix}-codex-stream-official-help`);
  if (!sourceField || !sourceSelect || !envField || !modelSelect || !officialFields || !accountSelect || !wireSelect || !nativeFields || !nativeSelect) return;

  const supportsOfficial = backend === "codex";
  const supportsNative = backend === "claude";
  const expectedSourceValues = supportsOfficial ? "env,official" : supportsNative ? "env,claude_native" : "env";
  const currentSourceValues = [...sourceSelect.options].map(item => item.value).join(",");
  const desiredOptions = supportsOfficial
    ? `${option("env", "Env", sourceSelect.value === "env")}${option("official", "Official", sourceSelect.value === "official")}`
    : supportsNative
      ? `${option("env", "Env Provider", sourceSelect.value === "env")}${option("claude_native", "Official", sourceSelect.value === "claude_native")}`
      : option("env", "Env", true);
  if (currentSourceValues !== expectedSourceValues) {
    const current = sourceSelect.value;
    sourceSelect.innerHTML = desiredOptions;
    restoreSourceValue(sourceSelect, current, supportsOfficial, supportsNative);
  }
  sourceField.hidden = !supportsOfficial && !supportsNative;
  sourceSelect.disabled = !supportsOfficial && !supportsNative;
  if (!supportsOfficial && sourceSelect.value === "official") sourceSelect.value = "env";
  if (!supportsNative && sourceSelect.value === "claude_native") sourceSelect.value = "env";
  const nativeOption = sourceSelect.querySelector<HTMLOptionElement>('option[value="claude_native"]');
  if (nativeOption) {
    const availability = claudeOfficialCatalogs.get(prefix)?.availability || "unknown";
    nativeOption.textContent = CLAUDE_OFFICIAL_LABELS[availability];
  }
  const official = supportsOfficial && sourceSelect.value === "official";
  const native = supportsNative && sourceSelect.value === "claude_native";

  if (streamSettings) {
    streamSettings.hidden = !supportsOfficial;
    streamSettings.querySelectorAll<HTMLInputElement>("input").forEach(input => { input.disabled = !supportsOfficial || official; });
    if (streamOfficialHelp) streamOfficialHelp.hidden = !official;
  }
  envField.hidden = official || native;
  modelSelect.disabled = official || native;
  modelSelect.required = !official && !native;
  officialFields.hidden = !official;
  accountSelect.disabled = !official;
  wireSelect.disabled = !official;
  nativeFields.hidden = !native;
  const claudeCatalog = claudeOfficialCatalogs.get(prefix);
  const preserveUnavailable = sourceSelect.dataset.initialOfficial === "true" && claudeCatalog?.availability !== "ready";
  nativeSelect.disabled = !native || (claudeCatalog?.availability !== "ready" && !preserveUnavailable);
  nativeSelect.required = native && claudeCatalog?.availability === "ready";
  nativeSelect.dataset.ready = claudeCatalog?.availability === "ready" ? "true" : "false";

  if (official) {
    const accountID = accountSelect.value || accounts[0]?.id || "";
    if (accountID) accountSelect.value = accountID;
    const previousWire = wireSelect.value;
    const available = accountModels(accounts, accountID);
    wireSelect.innerHTML = available.length
      ? available.map(model => option(model.wire_model, model.display_name || model.wire_model, model.wire_model === previousWire)).join("")
      : '<option value="">该账号暂无可用模型，请先刷新账号</option>';
    if (!available.some(model => model.wire_model === previousWire)) wireSelect.value = available[0]?.wire_model || "";
  } else if (native) {
    const previousNative = nativeSelect.value || "default";
    nativeSelect.innerHTML = officialModelOptions(claudeCatalog, previousNative);
  } else {
    const previousModel = modelSelect.value;
    const available = models.filter(model => model.source !== "official" && model.source !== "claude_native" && model.backend === backend);
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
    : native
      ? undefined
      : models.find(model => model.id === modelSelect.value);
  effort.value = resolveReasoningEffort(
    effortLevels, previousEffort, selectedModel?.default_reasoning_effort || "", preferModelDefault,
  );
}

function restoreSourceValue(
  select: HTMLSelectElement,
  previous: string,
  supportsOfficial: boolean,
  supportsNative: boolean,
): void {
  if (previous === "official" && supportsOfficial) {
    select.value = "official";
  } else if (previous === "claude_native" && supportsNative) {
    select.value = "claude_native";
  } else {
    select.value = "env";
  }
}

export function setClaudeOfficialCatalog(prefix: string, catalog: ClaudeOfficialCatalog): void {
  claudeOfficialCatalogs.set(prefix, catalog);
  syncClaudeOfficialFields(prefix);
}

export function setClaudeOfficialLoading(prefix: string): void {
  setClaudeOfficialCatalog(prefix, {availability: "loading", models: [], message: "正在检测当前 Workspace 的 Claude Code 官方账号..."});
}

function syncClaudeOfficialFields(prefix: string): void {
  const sourceSelect = document.querySelector<HTMLSelectElement>(`#${prefix}-model-source`);
  const catalog = claudeOfficialCatalogs.get(prefix);
  if (!sourceSelect || !catalog) return;
  const officialOption = sourceSelect.querySelector<HTMLOptionElement>('option[value="claude_native"]');
  const help = document.querySelector<HTMLElement>(`#${prefix}-claude-native-help`);
  const modelSelect = document.querySelector<HTMLSelectElement>(`#${prefix}-claude-native-model`);
  if (officialOption) {
    officialOption.textContent = CLAUDE_OFFICIAL_LABELS[catalog.availability];
  }
  if (help) help.textContent = catalog.message || (catalog.availability === "ready"
    ? `已检测到 ${catalog.models.length} 个可用模型。`
    : "Official 暂不可用。");
  if (modelSelect) {
    const previous = modelSelect.value || "default";
    const preserveUnavailable = sourceSelect.dataset.initialOfficial === "true" && catalog.availability !== "ready";
    modelSelect.innerHTML = officialModelOptions(catalog, previous);
    modelSelect.disabled = sourceSelect.value !== "claude_native" || (catalog.availability !== "ready" && !preserveUnavailable);
    modelSelect.required = sourceSelect.value === "claude_native" && catalog.availability === "ready";
    modelSelect.dataset.ready = catalog.availability === "ready" ? "true" : "false";
  }
}

export function bindRuntimeFields(
  prefix: string,
  models: Model[],
  accounts: CodexAccount[],
  onBackendChange?: () => void,
  preserveInitialEffort = false,
): void {
  document.querySelector(`#${prefix}-backend`)?.addEventListener("change", () => {
    syncRuntimeFields(prefix, models, accounts, true);
    onBackendChange?.();
  });
  for (const suffix of ["model-source", "model", "codex-account", "wire-model"]) {
    document.querySelector(`#${prefix}-${suffix}`)?.addEventListener("change", () => syncRuntimeFields(prefix, models, accounts, true));
  }
  syncRuntimeFields(prefix, models, accounts, !preserveInitialEffort);
}
