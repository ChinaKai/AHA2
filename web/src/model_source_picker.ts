import {tr} from "./i18n.js";
import type {ClaudeModelOption, CodexAccount, Model, Workspace} from "./types.js";

// This module is lazy-loaded on the pet/channel surfaces. The Chinese copy
// stays here so the first-screen catalog does not grow for two secondary pages;
// tr() supplies the English pairing from i18n.ts, so there is one catalog
// rather than a second hardcoded translation table in this module.
const modelSourceCopy = {
  label: "模型来源", model: "模型 / 账号",
  provider: "Provider 模型", official: "Codex 官方账号", claudeNative: "Claude native",
  unavailable: "暂不可用", auto: "自动选择可用模型",
  noProvider: "没有可用的 Provider 模型", noAccount: "未配置 Codex 官方账号",
  noCredential: "尚未登录（未配置凭据）", accountStatus: "账号状态：{status}",
  accountMissing: "所选 Codex 账号已不存在", noAccountModels: "账号暂无可用模型，请先刷新账号",
  modelNotInCatalog: "当前配置已不在可选列表中", savedUnavailable: "当前配置不可用：{reason}",
  noCLI: "当前 Workspace 未检测到 Claude Code CLI",
  notLoggedIn: "尚未登录 Claude Code，请先执行 claude auth login",
  authUnknown: "尚未完成 Claude Code 登录状态检测",
  authUnknownWithReason: "登录状态检测失败（{reason}）", noModels: "未能获取官方模型列表",
  modelsReady: "已检测到 {count} 个官方模型", modelsErrorWithReason: "未能获取官方模型列表（{reason}）",
  helpProvider: "使用 Provider 的凭据与 Env Group。",
  helpOfficial: "使用所选 Codex 官方账号的额度；账号与模型必须同时可用。",
  helpClaudeNative: "使用当前 Workspace 的 Claude Code 登录，不读取也不保存凭据。",
} as const;

type CopyKey = keyof typeof modelSourceCopy;

function modelSourceText(key: CopyKey, values: Record<string, string> = {}): string {
  return tr(modelSourceCopy[key])
    .replace(/\{(\w+)\}/g, (_, name: string) => String(values[name] ?? `{${name}}`));
}

// One source-first picker for channel settings and pet settings. Both surfaces
// must agree on what is selectable, why an entry is disabled, and what the
// persisted selection means, so the catalog/selection logic lives here only.
export type ModelSource = "provider" | "official" | "claude_native";

export type ClaudeOfficialAvailability = "ready" | "loading" | "not_logged_in" | "unknown" | "unavailable";

export type ClaudeOfficialCatalog = {
  availability: ClaudeOfficialAvailability;
  models: ClaudeModelOption[];
  message?: string;
};

export type SourceModelLike = Pick<Model, "id" | "display_name" | "provider_name" | "source" | "wire_model" | "codex_account_id" | "backend">;

export type ModelSourceSelection = {
  source?: string;
  modelID?: string;
  wireModel?: string;
  accountID?: string;
};

export type SourceModelEntry = {
  // provider: catalog model ID; official/native: wire model.
  value: string;
  label: string;
  accountID: string;
  disabled: boolean;
  reason: string;
};

export type SourceModelOption = {
  source: ModelSource;
  label: string;
  disabled: boolean;
  reason: string;
  entries: SourceModelEntry[];
};

export function normalizeModelSource(value: unknown): ModelSource {
  const text = String(value ?? "").trim().toLowerCase();
  if (text === "official" || text === "codex_official") return "official";
  if (text === "claude_native") return "claude_native";
  return "provider";
}

function escapeHTML(value: unknown): string {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

function option(value: string, label: string, selected: boolean, disabled = false, dataset: Record<string, string> = {}): string {
  const attrs = Object.entries(dataset)
    .filter(([, item]) => item !== "")
    .map(([key, item]) => ` data-${key}="${escapeHTML(item)}"`)
    .join("");
  return `<option value="${escapeHTML(value)}"${attrs} ${selected ? "selected" : ""} ${disabled ? "disabled" : ""}>${escapeHTML(label)}</option>`;
}

export function claudeCatalogFromWorkspace(workspace?: Workspace): ClaudeOfficialCatalog {
  const probe = (workspace?.capabilities || {}).claude as {
    status?: string;
    auth_status?: string;
    models?: ClaudeModelOption[];
    models_error?: string;
    auth_error?: string;
  } | undefined;
  if (!probe || probe.status !== "ready") {
    return {availability: "unavailable", models: [], message: modelSourceText("noCLI")};
  }
  if (probe.auth_status === "not_logged_in") {
    return {availability: "not_logged_in", models: [], message: modelSourceText("notLoggedIn")};
  }
  if (probe.auth_status !== "logged_in") {
    const reason = typeof probe.auth_error === "string" ? probe.auth_error.trim() : "";
    return {
      availability: "unknown",
      models: [],
      message: reason ? modelSourceText("authUnknownWithReason", {reason}) : modelSourceText("authUnknown"),
    };
  }
  const models = Array.isArray(probe.models) ? probe.models : [];
  if (models.length > 0) {
    return {availability: "ready", models, message: modelSourceText("modelsReady", {count: String(models.length)})};
  }
  return {
    availability: "unavailable",
    models: [],
    message: probe.models_error ? modelSourceText("modelsErrorWithReason", {reason: probe.models_error}) : modelSourceText("noModels"),
  };
}

function providerModels(models: SourceModelLike[]): SourceModelLike[] {
  return models.filter(model => {
    const source = String(model.source || "provider");
    return source === "provider";
  });
}

function providerEntries(models: SourceModelLike[], saved: ModelSourceSelection): SourceModelEntry[] {
  const entries = providerModels(models).map(model => ({
    value: model.id,
    label: [model.provider_name, model.display_name].filter(Boolean).join(" · ") || model.id,
    accountID: "",
    disabled: false,
    reason: "",
  })).sort((left, right) => left.label.localeCompare(right.label));
  const savedID = String(saved.modelID || "");
  if (savedID !== "" && !entries.some(entry => entry.value === savedID)) {
    entries.unshift({
      value: savedID, label: savedID, accountID: "",
      disabled: true, reason: modelSourceText("modelNotInCatalog"),
    });
  }
  return entries;
}

function officialEntries(accounts: CodexAccount[], saved: ModelSourceSelection): {entries: SourceModelEntry[]; reason: string} {
  if (accounts.length === 0) {
    return {entries: [], reason: modelSourceText("noAccount")};
  }
  const entries: SourceModelEntry[] = [];
  for (const account of accounts) {
    let reason = "";
    if (!account.credential_configured) {
      reason = modelSourceText("noCredential");
    } else if (account.status !== "ready") {
      reason = modelSourceText("accountStatus", {status: account.status || "unknown"});
    }
    const options = account.available_models || [];
    if (options.length === 0) {
      entries.push({
        value: "", label: account.label || account.id, accountID: account.id,
        disabled: true,
        reason: reason || modelSourceText("noAccountModels"),
      });
      continue;
    }
    for (const model of options) {
      entries.push({
        value: model.wire_model,
        label: `${account.label || account.id} · ${model.display_name || model.wire_model}`,
        accountID: account.id,
        disabled: Boolean(reason),
        reason,
      });
    }
  }
  const savedWire = String(saved.wireModel || "");
  const savedAccount = String(saved.accountID || "");
  const hasSaved = savedWire !== "" && entries.some(entry => entry.value === savedWire && (!savedAccount || entry.accountID === savedAccount));
  if ((savedWire !== "" || savedAccount !== "") && !hasSaved) {
    const account = accounts.find(item => item.id === savedAccount);
    const reason = !account
      ? modelSourceText("accountMissing")
      : !account.credential_configured
        ? modelSourceText("noCredential")
        : account.status !== "ready"
          ? modelSourceText("accountStatus", {status: account.status || "unknown"})
          : modelSourceText("modelNotInCatalog");
    entries.unshift({
      value: savedWire, label: `${account?.label || savedAccount || modelSourceText("official")} · ${savedWire}`,
      accountID: savedAccount, disabled: true, reason,
    });
  }
  return {entries, reason: ""};
}

function nativeEntries(catalog: ClaudeOfficialCatalog | undefined, saved: ModelSourceSelection): {entries: SourceModelEntry[]; reason: string} {
  const source = catalog || {availability: "unavailable" as const, models: []};
  const reason = source.availability === "ready" ? "" : (source.message || modelSourceText("noModels"));
  if (source.availability === "ready" && source.models.length > 0) {
    return {
      entries: source.models.map(model => ({
        value: model.wire_model,
        label: model.display_name || model.wire_model,
        accountID: "",
        disabled: false,
        reason: "",
      })),
      reason: "",
    };
  }
  const savedWire = String(saved.wireModel || "");
  const entries: SourceModelEntry[] = [];
  if (savedWire !== "") {
    entries.push({
      value: savedWire, label: savedWire, accountID: "",
      disabled: true, reason: reason || modelSourceText("modelNotInCatalog"),
    });
  }
  return {entries, reason: reason || modelSourceText("noModels")};
}

// buildModelSourceOptions always returns all three sources. Availability is
// data, not a filter: an unavailable saved source stays visible (and selected)
// with a written reason instead of silently becoming Provider.
export function buildModelSourceOptions(
  models: SourceModelLike[],
  accounts: CodexAccount[],
  claudeCatalog: ClaudeOfficialCatalog | undefined,
  selection: ModelSourceSelection = {},
  options: {allowAuto?: boolean} = {},
): SourceModelOption[] {
  const savedSource = normalizeModelSource(selection.source);
  const providers = providerEntries(models, selection);
  if (options.allowAuto && providers.length > 0) {
    providers.unshift({value: "", label: modelSourceText("auto"), accountID: "", disabled: false, reason: ""});
  }
  const official = officialEntries(accounts || [], selection);
  const native = nativeEntries(claudeCatalog, selection);
  return [
    {
      source: "provider",
      label: modelSourceText("provider"),
      disabled: providers.length === 0,
      reason: providers.length === 0 ? modelSourceText("noProvider") : "",
      entries: providers,
    },
    {
      source: "official",
      label: modelSourceText("official"),
      disabled: official.entries.length === 0,
      reason: official.reason,
      entries: official.entries,
    },
    {
      source: "claude_native",
      label: modelSourceText("claudeNative"),
      disabled: native.entries.length === 0,
      reason: native.reason,
      entries: native.entries,
    },
  ].map(item => item.source === savedSource ? {...item, disabled: item.entries.length === 0} : item);
}

function savedEntryIndex(source: SourceModelOption, selection: ModelSourceSelection, sourceChanged: boolean): number {
  if (sourceChanged) return source.entries.findIndex(entry => !entry.disabled);
  const selectionSource = normalizeModelSource(selection.source);
  if (source.source !== selectionSource) return source.entries.findIndex(entry => !entry.disabled);
  if (source.source === "provider") {
    return source.entries.findIndex(entry => entry.value === String(selection.modelID || ""));
  }
  if (source.source === "claude_native") {
    return source.entries.findIndex(entry => entry.value === String(selection.wireModel || ""));
  }
  return source.entries.findIndex(entry =>
    entry.value === String(selection.wireModel || "") &&
    (!selection.accountID || entry.accountID === String(selection.accountID)));
}

function modelOptionsHTML(source: SourceModelOption, selection: ModelSourceSelection, sourceChanged: boolean): string {
  const selectedIndex = savedEntryIndex(source, selection, sourceChanged);
  const fallback = selectedIndex >= 0 ? selectedIndex : source.entries.findIndex(entry => !entry.disabled);
  if (source.entries.length === 0) {
    return `<option value="" disabled selected>${escapeHTML(source.reason || modelSourceText("unavailable"))}</option>`;
  }
  return source.entries.map((entry, index) => option(
    entry.value,
    entry.disabled && entry.reason ? `${entry.label} · ${entry.reason}` : entry.label,
    index === fallback,
    entry.disabled,
    {"account-id": entry.accountID},
  )).join("");
}

function helpText(source: SourceModelOption, selection: ModelSourceSelection, sourceChanged: boolean): string {
  const selectedIndex = savedEntryIndex(source, selection, sourceChanged);
  const selected = selectedIndex >= 0 ? source.entries[selectedIndex] : undefined;
  if (selected?.disabled && selected.reason) return modelSourceText("savedUnavailable", {reason: selected.reason});
  if (source.disabled && source.reason) return source.reason;
  if (source.source === "provider") return modelSourceText("helpProvider");
  if (source.source === "official") return modelSourceText("helpOfficial");
  return modelSourceText("helpClaudeNative");
}

export function modelSourceFieldsHTML(prefix: string, options: SourceModelOption[], selection: ModelSourceSelection = {}): string {
  const source = options.find(item => item.source === normalizeModelSource(selection.source)) || options[0];
  const sourceOptions = options.map(item => {
    const label = item.disabled && item.reason ? `${item.label} · ${item.reason}` : item.label;
    return option(item.source, label, item.source === source?.source, item.disabled);
  }).join("");
  return `<div class="model-source-picker">
    <label>${escapeHTML(modelSourceText("label"))}<select id="${prefix}-source" name="model_source">${sourceOptions}</select></label>
    <label>${escapeHTML(modelSourceText("model"))}<select id="${prefix}-model" name="model_id">${source ? modelOptionsHTML(source, selection, false) : ""}</select></label>
    <small id="${prefix}-help" class="field-help">${escapeHTML(source ? helpText(source, selection, false) : "")}</small>
  </div>`;
}

function selectedSource(options: SourceModelOption[], prefix: string): SourceModelOption | undefined {
  const value = document.querySelector<HTMLSelectElement>(`#${prefix}-source`)?.value;
  return options.find(item => item.source === value) || options[0];
}

export function syncModelSourceFields(prefix: string, options: SourceModelOption[], selection: SourceModelSelection = {}, sourceChanged = false): void {
  const sourceSelect = document.querySelector<HTMLSelectElement>(`#${prefix}-source`);
  const modelSelect = document.querySelector<HTMLSelectElement>(`#${prefix}-model`);
  const help = document.querySelector<HTMLElement>(`#${prefix}-help`);
  if (!sourceSelect || !modelSelect) return;
  const source = selectedSource(options, prefix);
  if (!source) return;
  modelSelect.innerHTML = modelOptionsHTML(source, selection, sourceChanged);
  modelSelect.disabled = source.disabled || source.entries.every(entry => entry.disabled);
  if (help) help.textContent = helpText(source, selection, sourceChanged);
}

export function bindModelSourceFields(prefix: string, options: SourceModelOption[], onChange?: () => void): void {
  const sourceSelect = document.querySelector<HTMLSelectElement>(`#${prefix}-source`);
  sourceSelect?.addEventListener("change", () => {
    syncModelSourceFields(prefix, options, {}, true);
    onChange?.();
  });
  document.querySelector<HTMLSelectElement>(`#${prefix}-model`)?.addEventListener("change", () => onChange?.());
}

export type ModelSourcePick = {
  model_source: ModelSource;
  model_id: string;
  wire_model: string;
  codex_account_id: string;
};

export function readModelSourceSelection(prefix: string): ModelSourcePick {
  const source = normalizeModelSource(document.querySelector<HTMLSelectElement>(`#${prefix}-source`)?.value);
  const model = document.querySelector<HTMLSelectElement>(`#${prefix}-model`);
  const selected = model?.selectedOptions?.[0];
  const value = selected?.value || "";
  if (source === "official") {
    return {model_source: "official", model_id: "", wire_model: value, codex_account_id: selected?.dataset.accountId || ""};
  }
  if (source === "claude_native") {
    return {model_source: "claude_native", model_id: "", wire_model: value, codex_account_id: ""};
  }
  return {model_source: "provider", model_id: value, wire_model: "", codex_account_id: ""};
}
