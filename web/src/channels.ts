import {api} from "./api.js";
import {icon} from "./icons.js";
import {t} from "./i18n.js";
import {bindModelSourceFields, buildModelSourceOptions, claudeCatalogFromWorkspace, modelSourceFieldsHTML, readModelSourceSelection} from "./model_source_picker.js";
import type {ModelSourceSelection} from "./model_source_picker.js";
import type {ChannelConversationSummary, ChannelDelivery, ChannelInstance, ChannelOnboardingOptions, ChannelOnboardingSession, ChannelPlugin, CodexAccount, Knowledge, KnowledgeLibrary, KnowledgeVaultLibrary, Model, Project, Workspace} from "./types.js";

interface ChannelUIContext {
	models: Model[];
	accounts: CodexAccount[];
	projects: Project[];
	workspaces: Workspace[];
	// One verified knowledge root per project, from /knowledge/indexes. Not a page.
	knowledgeIndexes: Knowledge[];
	libraries: KnowledgeLibrary[];
	// True once the owner switched reads to the file vault. The legacy index and
	// library arrays above then stay unused by the channel page.
	knowledgeVaultEnabled: boolean;
	vaultLibraries: KnowledgeVaultLibrary[];
}

function escapeHTML(value: unknown): string {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

function pluginState(plugin: ChannelPlugin): string {
  if (plugin.install_state !== "installed") return plugin.install_state === "missing" ? "文件缺失" : plugin.install_state === "incompatible" ? "协议不兼容" : "安装无效";
  return plugin.enabled ? "可用" : "已停用";
}

function instanceState(instance: ChannelInstance): string {
  if (instance.status === "retired") return "已归档";
  if (instance.effective_availability === "unavailable") return "提供方不可用";
  const labels: Record<string, string> = {draft: "待绑定", onboarding: "绑定中", ready: "已就绪", degraded: "运行异常", disabled: "已停用", error: "错误", retired: "已归档"};
  return labels[instance.status] || instance.status;
}

function objectValue(value: unknown): Record<string, unknown> {
	return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : {};
}

function stringList(config: Record<string, unknown>, key: string, defaults: string[]): string[] {
	return Array.isArray(config[key]) ? (config[key] as unknown[]).map(String) : defaults;
}

function channelErrorMessage(error: unknown): string {
	const typed = error as Error & {code?: string};
	if (typed?.code === "channel_revision_conflict") return "渠道设置已被其他更新修改，页面已刷新，请重新操作。";
	return error instanceof Error ? error.message : String(error);
}

function refreshAfterChannelConflict(error: unknown, refresh: () => Promise<void>): void {
	if ((error as {code?: string})?.code === "channel_revision_conflict") void refresh().catch(() => undefined);
}

export function channelPurgePreviewMessage(preview: {name: string; tasks: number; conversations: number; messages: number; attachments: number}): string {
	return `将永久删除“${preview.name}”及其本地归档：${preview.tasks} 个任务、${preview.conversations} 个会话、${preview.messages} 条消息、${preview.attachments} 个附件。此操作不可恢复，飞书后台应用不会自动删除。`;
}

export async function purgeRetiredChannelInstance(instanceID: string, revision = 0): Promise<"cancelled" | "name_mismatch" | "purged"> {
	const [instanceResult, previewResult] = await Promise.all([
		revision > 0 ? Promise.resolve(null) : api.channelInstance(instanceID),
		api.channelInstancePurgePreview(instanceID),
	]);
	const preview = previewResult.preview;
	if (!window.confirm(channelPurgePreviewMessage(preview))) return "cancelled";
	const confirmationName = window.prompt(`请输入实例名“${preview.name}”确认永久删除：`);
	if (confirmationName === null) return "cancelled";
	if (confirmationName !== preview.name) return "name_mismatch";
	await api.purgeChannelInstance(instanceID, confirmationName, revision > 0 ? revision : Number(instanceResult?.instance.revision || 0));
	return "purged";
}

async function completeConversation(taskID: string): Promise<unknown[]> {
	const items: unknown[] = [];
	let before: number | undefined;
	for (;;) {
		const response = await api.conversation(taskID, {before, limit: 500});
		items.push(...response.conversation.items);
		if (!response.conversation.has_more || response.conversation.next_before === undefined || response.conversation.next_before === before) break;
		before = response.conversation.next_before;
	}
	return items;
}

export async function exportRetiredChannelInstance(instanceID: string): Promise<void> {
	const instanceResult = await api.channelInstance(instanceID);
	const instance = instanceResult.instance;
	const [projects, workspaces, tasks, handoffs, deliveries, policies] = await Promise.all([
		api.projects(), api.workspaces(instance.host_project_id), api.tasks(instance.host_project_id), api.channelHandoffs(instanceID), api.channelDeliveries(instanceID), api.channelKnowledgePolicies(instanceID),
	]);
	const taskHistory = await Promise.all(tasks.tasks.map(async task => ({
		detail: await api.task(task.id),
		conversation: await completeConversation(task.id),
	})));
	const content = {
		exported_at: new Date().toISOString(),
		instance,
		endpoints: instanceResult.endpoints,
		project: projects.projects.find(project => project.id === instance.host_project_id) || null,
		workspaces: workspaces.workspaces,
		tasks: taskHistory,
		handoffs: handoffs.handoffs,
		deliveries: deliveries.deliveries,
		knowledge_policies: policies.policies,
	};
	const blobURL = URL.createObjectURL(new Blob([JSON.stringify(content, null, 2)], {type: "application/json"}));
	const link = document.createElement("a");
	link.href = blobURL;
	link.download = `${instance.name.replace(/[\\/:*?"<>|]+/g, "-") || "channel"}-archive.json`;
	link.click();
	window.setTimeout(() => URL.revokeObjectURL(blobURL), 0);
}

export function channelRuntimeSelection(config: Record<string, unknown>, context: ChannelUIContext): ModelSourceSelection {
  const runtime = objectValue(config.runtime_default);
  const modelID = String(runtime.model_id || "");
  const model = context.models.find(item => item.id === modelID);
  const accountID = String(runtime.codex_account_id || model?.codex_account_id || "");
  // Old saves stored only model_id + account. The catalog response hides
  // managed models, so an account is the remaining signal that the saved row
  // was an official selection. Never infer Provider in that case.
  const source = String(runtime.model_source || model?.source || (accountID ? "official" : ""));
  return {
    source,
    modelID,
    wireModel: String(runtime.wire_model || model?.wire_model || ""),
    accountID,
  };
}

export function channelRuntimeOptionsFor(config: Record<string, unknown>, context: ChannelUIContext, instance?: ChannelInstance) {
  const selection = channelRuntimeSelection(config, context);
  const workspace = (context.workspaces || []).find(item => item.id === (instance?.host_workspace_id || ""));
  const options = buildModelSourceOptions(context.models, context.accounts, claudeCatalogFromWorkspace(workspace), selection, {allowAuto: true});
  return {selection, options};
}

export function channelRuntimeEditor(config: Record<string, unknown>, context: ChannelUIContext, instance?: ChannelInstance): string {
  const {selection, options} = channelRuntimeOptionsFor(config, context, instance);
  return `<fieldset class="channel-runtime-editor"><legend>运行模型</legend>${modelSourceFieldsHTML("channel-runtime", options, selection)}<small>私聊和群聊共用。官方来源会同时确定账号和模型；不可用的账号或模型不可选。</small></fieldset>`;
}

export interface ChannelKnowledgeSource {
	label: string;
	root: string;
	kind: "legacy" | "vault";
	selectable: boolean;
	// Vault sources carry the project space ID used by knowledge_bindings.
	projectId?: string;
}

export interface ChannelGroupBindingPayload {
	space_project_id: string;
	access_mode: string;
	scope_mode: string;
}

export interface ChannelGroupKnowledgeSelectionPayload {
	include_global: boolean;
	bindings: ChannelGroupBindingPayload[];
}

export const channelEmptyGroupKnowledgeWarning = "群聊将没有可读知识；保存后会清空当前选择。";

// An empty vault group selection is a legal save that grants the group nothing.
// The editor warns about it inline instead of blocking the save, and the same
// check drives the live update as the owner toggles spaces and global knowledge.
export function channelGroupKnowledgeIsEmpty(selectedProjectSpaces: number, includeGlobal: boolean): boolean {
	return selectedProjectSpaces <= 0 && !includeGlobal;
}

// The group picker saves in one atomic request: the server writes the endpoint
// selection and removes the instance defaults in the same transaction, so an
// empty group choice cannot fall back to a space list the owner just revoked.
export function channelGroupKnowledgeSelectionPayload(spaces: ChannelGroupBindingPayload[], includeGlobal: boolean): ChannelGroupKnowledgeSelectionPayload {
  return {include_global: includeGlobal, bindings: spaces};
}

type ChannelKnowledgePolicy = {
	revision: number;
	scope_mode: "all" | "selected" | string;
	grants?: Array<{knowledge_entry_id: string; grant_scope: string}>;
};

// The channel scope picker reads the source that is live. With the vault on it
// lists the vault project roots (root_id, not the picker key) and never touches
// /knowledge/indexes or /knowledge/libraries. These roots only scope the group
// digital human: the private chat has no implicit or explicit restriction. With
// the vault off the legacy list is unchanged.
export function channelKnowledgeSources(context: ChannelUIContext): ChannelKnowledgeSource[] {
  if (context.knowledgeVaultEnabled === true) {
    return (context.vaultLibraries || [])
      .filter(library => library.kind === "project" && Boolean(library.root_id))
      .map(library => ({label: `vault 知识库 · ${library.name}`, root: String(library.root_id), kind: "vault" as const, selectable: true, projectId: library.project_id}));
  }
  const rootByProject = new Map(context.knowledgeIndexes.map(entry => [entry.project_id, entry.id]));
  const projectSources = context.projects
    .filter(project => !["channel", "system", "knowledge"].includes(project.project_type || ""))
    .map(project => ({label: `项目知识 · ${project.name}`, root: rootByProject.get(project.id) || ""}))
    .filter(item => item.root);
  const librarySources = context.libraries
    .map(library => ({label: `知识库 · ${library.name}`, root: rootByProject.get(library.container_project_id) || ""}))
    .filter(item => item.root);
  return [...projectSources, ...librarySources].map(item => ({...item, kind: "legacy" as const, selectable: true}));
}

function channelKnowledgePolicyEditor(sources: ChannelKnowledgeSource[], policy: ChannelKnowledgePolicy | undefined, hint = "", includeGlobal?: boolean): string {
  const granted = new Set((policy?.grants || []).map(grant => grant.knowledge_entry_id));
  const scopeMode = policy?.scope_mode === "all" ? "all" : "selected";
  const revision = policy?.revision || 0;
  const choices = sources.map(source => `<label class="channel-check"><input type="checkbox" name="knowledge_entry_id" value="${escapeHTML(source.root)}" ${granted.has(source.root) ? "checked" : ""}${source.selectable ? "" : " disabled"}>${escapeHTML(source.label)}</label>`).join("");
  const vaultMode = includeGlobal !== undefined;
  const globalChoice = vaultMode ? `<label class="channel-check"><input type="checkbox" name="include_global" ${includeGlobal ? "checked" : ""}>全局知识（跨项目共享）</label>` : "";
  // The vault selection is exactly the checked global and project spaces, so
  // the legacy all/selected mode switch would be misleading there.
  const scopeEditor = vaultMode
    ? ""
    : `<label>范围模式<select name="knowledge_scope_mode"><option value="all" ${scopeMode === "all" ? "selected" : ""}>全部项目知识与知识库</option><option value="selected" ${scopeMode === "selected" ? "selected" : ""}>限制到所选知识源</option></select></label>`;
  const selectedHidden = !vaultMode && scopeMode === "all" ? "hidden" : "";
  const footer = vaultMode ? "" : "<small>固定渠道索引始终可读。</small>";
  // The warning follows the rendered checkboxes, not the stored policy: the
  // owner sees it the moment the last project space is cleared.
  const checkedSpaces = sources.filter(source => granted.has(source.root)).length;
  const emptyWarning = vaultMode
    ? `<p class="channel-knowledge-empty-warning" data-channel-knowledge-empty-warning ${channelGroupKnowledgeIsEmpty(checkedSpaces, includeGlobal === true) ? "" : "hidden"}>${channelEmptyGroupKnowledgeWarning}</p>`
    : "";
  return `<div data-channel-settings-policy data-revision="${revision}">${hint}${globalChoice}${scopeEditor}${emptyWarning}<div data-channel-knowledge-selected ${selectedHidden}><div class="channel-batch-toolbar"><button type="button" data-channel-policy-select-all>全选</button><button type="button" data-channel-policy-clear>清空</button></div><div class="channel-checkbox-list">${choices || "<small>暂无可选知识源</small>"}</div></div>${footer}</div>`;
}

export function channelVaultKnowledgeSection(context: ChannelUIContext, bindings: Array<{space_project_id: string}>, includeGlobal: boolean): string {
  const sources = channelKnowledgeSources(context);
  const bound = new Set(bindings.map(binding => binding.space_project_id));
  const policy: ChannelKnowledgePolicy = {
    revision: 1,
    scope_mode: "selected",
    grants: sources
      .filter(source => source.projectId && bound.has(source.projectId))
      .map(source => ({knowledge_entry_id: source.root, grant_scope: "subtree"})),
  };
  const notice = bindings.length > 0 || includeGlobal
    ? `<p><strong>文件知识库（vault）已启用</strong></p><p>群聊只读取勾选的知识：全局知识与项目知识库均可选；私聊不受限制，始终读取全部知识。</p>`
    : `<p><strong>该实例尚未选择群聊知识</strong></p><p>请勾选要授予群聊的全局知识和项目空间；都不勾选时群聊知识为空，私聊仍读取全部知识。</p>`;
  return `<div data-channel-vault-knowledge>${channelKnowledgePolicyEditor(sources, policy, notice, includeGlobal)}</div>`;
}

function formatChannelTime(value?: string): string {
  if (!value) return "—";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

export function bindChannelKnowledgeScope(container: HTMLElement): void {
  const mode = container.querySelector<HTMLSelectElement>('select[name="knowledge_scope_mode"]');
  const selected = container.querySelector<HTMLElement>("[data-channel-knowledge-selected]");
  const emptyWarning = container.querySelector<HTMLElement>("[data-channel-knowledge-empty-warning]");
  const projectInputs = () => [...container.querySelectorAll<HTMLInputElement>('input[name="knowledge_entry_id"]')];
  // The vault editor shows an inline warning instead of blocking an empty
  // save; legacy has no warning element and this stays a no-op there.
  const syncWarning = () => {
    if (!emptyWarning) return;
    const checkedSpaces = projectInputs().filter(input => input.checked).length;
    const includeGlobal = Boolean(container.querySelector<HTMLInputElement>('input[name="include_global"]')?.checked);
    emptyWarning.hidden = !channelGroupKnowledgeIsEmpty(checkedSpaces, includeGlobal);
  };
  const setAll = (checked: boolean) => {
    container.querySelectorAll<HTMLInputElement>('input[name="knowledge_entry_id"], input[name="include_global"]').forEach(input => { input.checked = checked; });
    syncWarning();
  };
  container.querySelectorAll<HTMLInputElement>('input[name="knowledge_entry_id"], input[name="include_global"]').forEach(input => input.addEventListener("change", syncWarning));
  container.querySelector<HTMLElement>("[data-channel-policy-select-all]")?.addEventListener("click", () => setAll(true));
  container.querySelector<HTMLElement>("[data-channel-policy-clear]")?.addEventListener("click", () => setAll(false));
  // The vault picker has no scope select: its checkbox list must stay visible.
  // Only the legacy editor hides the list when the mode is "all".
  const syncMode = () => { if (selected && mode) selected.hidden = mode.value !== "selected"; };
  mode?.addEventListener("change", syncMode);
  syncMode();
  syncWarning();
}

function openChannelSettings(instance: ChannelInstance, context: ChannelUIContext, uiOptions: {refresh: () => Promise<void>; setMessage: (kind: "error" | "success", message: string) => void}): void {
  document.querySelector("#channel-settings-dialog")?.remove();
  const config = objectValue(instance.config);
  const dialog = document.createElement("dialog");
  dialog.id = "channel-settings-dialog";
  dialog.className = "channel-settings-dialog";
  dialog.innerHTML = `<div class="dialog-head"><div><h2>渠道设置</h2><p>${escapeHTML(instance.name)}</p></div><button type="button" class="icon-button" data-close aria-label="${t("common.closeDialog")}">${icon("close")}</button></div>
    <div class="dialog-body">
      ${channelRuntimeEditor(config, context, instance)}
      <fieldset><legend>消息通知</legend><label class="channel-check"><input type="checkbox" name="notify_task_status" ${config.notify_task_status === true ? "checked" : ""}>普通 Task 状态变更推送到飞书私聊助手</label><small>仅推送等待处理、完成、失败或中断等状态；不会推送普通消息、过程输出或渠道宿主 Task。</small></fieldset>
      <section class="channel-settings-knowledge"><div class="panel-head"><strong>Knowledge 访问范围</strong><span>私聊不限制；群聊可选全局知识 + 项目知识库</span></div><div data-channel-settings-knowledge><small>加载中…</small></div></section>
    </div>
    <div class="dialog-actions"><button type="button" data-close>取消</button><button type="button" class="primary" data-save>保存设置</button></div>`;
  document.body.append(dialog);
  dialog.showModal();
  const runtimePicker = channelRuntimeOptionsFor(config, context, instance);
  bindModelSourceFields("channel-runtime", runtimePicker.options);
  const close = () => { dialog.close(); dialog.remove(); };
  dialog.querySelectorAll("[data-close]").forEach(button => button.addEventListener("click", close));
  dialog.addEventListener("cancel", event => { event.preventDefault(); close(); });
  const knowledgeHost = dialog.querySelector<HTMLElement>("[data-channel-settings-knowledge]");
  const saveButton = dialog.querySelector<HTMLButtonElement>("[data-save]");
  // Saving before the group selection has loaded would skip the knowledge
  // write while still reporting success. A failed load keeps it disabled.
  if (saveButton) saveButton.disabled = true;
  if (context.knowledgeVaultEnabled === true) {
    // The vault picker is editable: the saved rows are project space IDs plus
    // the global choice. A missing selection is a real empty state, not an
    // implicit host-space grant.
    void api.channelKnowledgeBindings(instance.id, "group_digital_human").then(result => {
      if (!dialog.isConnected || !knowledgeHost) return;
      knowledgeHost.innerHTML = channelVaultKnowledgeSection(context, result.bindings || [], result.include_global === true);
      const policyContainer = knowledgeHost.querySelector<HTMLElement>("[data-channel-settings-policy]");
      if (policyContainer) bindChannelKnowledgeScope(policyContainer);
      if (saveButton) saveButton.disabled = false;
    }).catch(error => {
      if (knowledgeHost) knowledgeHost.textContent = error instanceof Error ? error.message : String(error);
    });
  } else {
    void api.channelKnowledgePolicies(instance.id).then(policies => {
      if (!dialog.isConnected || !knowledgeHost) return;
      const groupPolicy = policies.policies.find(policy => policy.endpoint === "group_digital_human");
      if (!groupPolicy) {
        knowledgeHost.innerHTML = `<small>暂无群聊 Knowledge 策略。</small>`;
        if (saveButton) saveButton.disabled = false;
        return;
      }
      const sources = channelKnowledgeSources(context);
      knowledgeHost.innerHTML = channelKnowledgePolicyEditor(sources, groupPolicy);
      const policyContainer = knowledgeHost.querySelector<HTMLElement>("[data-channel-settings-policy]");
      if (policyContainer) bindChannelKnowledgeScope(policyContainer);
      if (saveButton) saveButton.disabled = false;
    }).catch(error => {
      if (knowledgeHost) knowledgeHost.textContent = error instanceof Error ? error.message : String(error);
    });
  }
  saveButton?.addEventListener("click", () => {
    void (async () => {
      const runtimeSelection = readModelSourceSelection("channel-runtime");
      const configUpdate = {
        runtime_default: runtimeSelection,
        runtime_assistant_dm: {inherit: true},
        runtime_group_digital_human: {inherit: true},
        notify_task_status: Boolean(dialog.querySelector<HTMLInputElement>('input[name="notify_task_status"]')?.checked),
      };
      if (saveButton) saveButton.disabled = true;
      try {
        await api.updateChannelInstance(instance.id, {config: configUpdate}, instance.revision);
        const policy = dialog.querySelector<HTMLElement>("[data-channel-settings-policy]");
        if (policy && context.knowledgeVaultEnabled === true) {
          const sourceByRoot = new Map(channelKnowledgeSources(context).map(source => [source.root, source]));
          const spaces = [...policy.querySelectorAll<HTMLInputElement>('input[name="knowledge_entry_id"]:checked')]
            .map(input => sourceByRoot.get(input.value)?.projectId)
            .filter((projectID): projectID is string => Boolean(projectID))
            .map(projectID => ({space_project_id: projectID, access_mode: "read", scope_mode: "all"}));
          const includeGlobal = Boolean(policy.querySelector<HTMLInputElement>('input[name="include_global"]')?.checked);
          // One atomic request writes the group endpoint selection and clears
          // the instance defaults, so revoking every space fails closed.
          const selection = channelGroupKnowledgeSelectionPayload(spaces, includeGlobal);
          await api.replaceChannelGroupKnowledgeBindings(instance.id, selection.bindings, selection.include_global);
        } else if (policy) {
          const mode = policy.querySelector<HTMLSelectElement>('select[name="knowledge_scope_mode"]')?.value === "selected" ? "selected" : "all";
          const grants = [...policy.querySelectorAll<HTMLInputElement>('input[name="knowledge_entry_id"]:checked')].map(input => ({knowledge_entry_id: input.value, grant_scope: "subtree"}));
          await api.updateChannelKnowledgePolicy(instance.id, "group_digital_human", mode, Number(policy.dataset.revision || 0), grants);
        }
        close();
        uiOptions.setMessage("success", "渠道设置已保存。");
        await uiOptions.refresh();
      } catch (error) {
        uiOptions.setMessage("error", channelErrorMessage(error));
        refreshAfterChannelConflict(error, uiOptions.refresh);
        if (saveButton) saveButton.disabled = false;
      }
    })();
  });
}

export const channelDeliveryConfirmMessages = {
	replay: "重放会把这条旧消息重新发送到真实渠道，对方会再次收到。确定重放？",
	skip: "跳过后这条消息将归档不再发送。确定跳过？",
} as const;

// Only dead letters need an Owner decision; delivered/replayed/skipped rows are history.
export function renderChannelDeliveryIssues(instanceID: string, deliveries: ChannelDelivery[]): string {
	const deadLetters = deliveries.filter(item => item.state === "dead_letter");
	if (!deadLetters.length) return "";
	const rows = deadLetters.map(item => `<li class="channel-delivery-issue">
      <div><code>${escapeHTML(item.id)}</code><small>创建于 ${escapeHTML(formatChannelTime(item.created_at))} · 尝试 ${escapeHTML(item.attempts)} 次 · 错误 ${escapeHTML(item.last_error_code || "—")}</small></div>
      <span><button type="button" data-channel-delivery-action="replay" data-channel-delivery-id="${escapeHTML(item.id)}" data-channel-instance-id="${escapeHTML(instanceID)}">重放</button><button type="button" data-channel-delivery-action="skip" data-channel-delivery-id="${escapeHTML(item.id)}" data-channel-instance-id="${escapeHTML(instanceID)}">跳过</button></span>
    </li>`).join("");
	return `<section class="channel-delivery-issues"><header><strong>投递异常</strong><span class="status bad">死信 ${deadLetters.length}</span></header><ul>${rows}</ul><small>不会自动重放或跳过；请逐条确认处理。</small></section>`;
}

// Page re-renders rebind the channel page; a short cache keeps unrelated
// re-renders from refetching every instance's outbox.
const deliveryIssueCache = new Map<string, {at: number; deliveries: ChannelDelivery[]}>();
const deliveryIssueCacheMs = 5000;

async function loadChannelDeliveryIssues(host: HTMLElement, instanceID: string, bindActions: (host: HTMLElement) => void): Promise<void> {
	const cached = deliveryIssueCache.get(instanceID);
	const show = (deliveries: ChannelDelivery[]) => {
		if (!host.isConnected) return;
		host.innerHTML = renderChannelDeliveryIssues(instanceID, deliveries);
		host.hidden = !host.innerHTML;
		bindActions(host);
	};
	if (cached) show(cached.deliveries);
	if (cached && Date.now() - cached.at < deliveryIssueCacheMs) return;
	try {
		const result = await api.channelDeliveries(instanceID);
		deliveryIssueCache.set(instanceID, {at: Date.now(), deliveries: result.deliveries || []});
		show(result.deliveries || []);
	} catch (error) {
		if (!host.isConnected) return;
		host.hidden = false;
		host.innerHTML = `<section class="channel-delivery-issues"><header><strong>投递异常</strong></header><small>投递列表加载失败：${escapeHTML(channelErrorMessage(error))}</small></section>`;
	}
}

function conversationBoundTask(item: ChannelConversationSummary): string {
  if (!item.bound_task_id) return "未绑定";
  return [item.bound_task_code, item.bound_task_title].filter(Boolean).join(" · ") || item.bound_task_id;
}

function topicWakeHint(enabled: boolean, mode: string): string {
  if (!enabled) return "请先开启「接收全量消息」。";
  return mode === "mention" ? "话题内也要 @ 机器人。" : "机器人发过消息的话题可免 @ 唤醒。";
}

export function conversationCard(item: ChannelConversationSummary): string {
  const kind = item.endpoint_kind === "group_digital_human" ? "群聊" : "私聊";
  const inbound = item.last_inbound_state
    ? `${item.last_inbound_state}${item.last_inbound_outcome ? ` · ${item.last_inbound_outcome}` : ""} · ${formatChannelTime(item.last_inbound_at)}`
    : "暂无";
  const outbound = item.last_outbound_state
    ? `${item.last_outbound_state}${item.last_outbound_error ? ` · ${item.last_outbound_error}` : ""} · ${formatChannelTime(item.last_outbound_at)}`
    : "暂无";
  const group = item.group;
  const topicWakeMode = group?.topic_wake_mode === "mention" ? "mention" : "full";
  const formatInbound = (entry: ChannelConversationSummary["last_mention_inbound"]) => entry
    ? `${entry.state}${entry.outcome ? ` · ${entry.outcome}` : ""} · ${formatChannelTime(entry.at)}`
    : "暂无";
  const groupReception = group ? `<label class="channel-check">
    <span><strong>接收全量消息</strong><br><small>接收非 @ 消息。${item.bound_task_id ? "@ 仍唤醒绑定任务" : "@ 由群聊电子人接"}</small>${item.not_in_bot_group_list ? "<br><small>不在机器人群列表</small>" : ""}</span>
    <span class="kv-skill-auto"><input type="checkbox" role="switch" aria-label="接收全量消息" data-group-reception="${escapeHTML(group.chat_id)}" ${group.receive_all_messages ? "checked" : ""}><span class="kv-skill-auto-track" aria-hidden="true"></span></span>
    <small data-group-reception-state>${group.receive_all_messages ? "已开启" : "仅 @"}</small>
  </label>
  <label class="channel-check">
    <span><strong>话题唤醒方式</strong><br><small data-topic-wake-hint>${topicWakeHint(group.receive_all_messages, topicWakeMode)}</small></span>
    <select aria-label="话题唤醒方式" data-group-topic-wake="${topicWakeMode}" ${group.receive_all_messages ? "" : "disabled"}>
      <option value="full" ${topicWakeMode === "full" ? "selected" : ""}>全量唤醒</option>
      <option value="mention" ${topicWakeMode === "mention" ? "selected" : ""}>需 @</option>
    </select>
  </label>` : "";
  return `<article class="channel-conversation-card">
    <header><strong>${escapeHTML(item.display_name || `${kind}会话`)}</strong><span class="status ${item.status === "active" ? "good" : "warn"}">${kind}</span></header>
    <div class="channel-conversation-bound"><span>绑定任务</span><strong>${escapeHTML(conversationBoundTask(item))}</strong></div>
    ${groupReception}
    <div class="channel-conversation-counts"><span>入站 <b>${item.inbound_count}</b></span><span>出站 <b>${item.outbound_count}</b></span></div>
    ${group ? `<small>最近入站（@）：${escapeHTML(formatInbound(item.last_mention_inbound))}</small><small>最近入站（非 @）：${escapeHTML(formatInbound(item.last_non_mention_inbound))}</small>` : `<small>最近入站：${escapeHTML(inbound)}</small>`}
    <small>最近出站：${escapeHTML(outbound)}</small>
  </article>`;
}

function openChannelConversations(instance: ChannelInstance, uiOptions: {setMessage: (kind: "error" | "success", message: string) => void}): void {
  document.querySelector("#channel-conversations-dialog")?.remove();
  const dialog = document.createElement("dialog");
  dialog.id = "channel-conversations-dialog";
  dialog.className = "channel-conversations-dialog";
  const feishu = instance.provider_key === "feishu" && instance.status !== "retired";
  const canRefresh = feishu && ["ready", "degraded"].includes(instance.status);
  const needsPermission = feishu && !stringList(objectValue(instance.config), "channel_permission_ids", []).includes("group_message_read");
  dialog.innerHTML = `<div class="dialog-head"><div><h2>会话</h2><p>${escapeHTML(instance.name)} · 私聊与群聊</p></div><button type="button" class="icon-button" data-close aria-label="${t("common.closeDialog")}">${icon("close")}</button></div><div class="dialog-body">${needsPermission ? "<small>群开关在「群消息全量接收」扫码授权后生效。</small>" : ""}<div data-conversations-body><small>加载中…</small></div><small data-conversations-status role="status"></small></div><div class="dialog-actions">${canRefresh ? `<button type="button" data-refresh-groups disabled>${icon("refresh")}刷新群聊</button>` : ""}<button type="button" data-close>关闭</button></div>`;
  document.body.append(dialog);
  dialog.showModal();
  const close = () => { dialog.close(); dialog.remove(); };
  dialog.querySelectorAll("[data-close]").forEach(button => button.addEventListener("click", close));
  dialog.addEventListener("cancel", event => { event.preventDefault(); close(); });
  const body = dialog.querySelector<HTMLElement>("[data-conversations-body]");
  const status = dialog.querySelector<HTMLElement>("[data-conversations-status]");
  const refreshButton = dialog.querySelector<HTMLButtonElement>("[data-refresh-groups]");
  if (!body || !status) return;
  const render = (result: {conversations: ChannelConversationSummary[]}) => {
    if (!dialog.isConnected) return;
    const direct = result.conversations.filter(item => item.endpoint_kind === "assistant_dm");
    const groups = result.conversations.filter(item => item.endpoint_kind === "group_digital_human");
    const section = (title: string, items: ChannelConversationSummary[]) => `<section class="channel-conversation-section"><header><strong>${title}</strong><span>${items.length}</span></header><div class="channel-conversation-grid">${items.map(conversationCard).join("") || `<small>暂无${title}</small>`}</div></section>`;
    body.innerHTML = section("私聊", direct) + section("群聊", groups);
  };
  const refreshGroups = async () => {
    if (!refreshButton) return;
    refreshButton.disabled = true;
    const controls = body.querySelectorAll<HTMLInputElement | HTMLSelectElement>("[data-group-reception], [data-group-topic-wake]");
    const disabled = Array.from(controls, control => control.disabled);
    controls.forEach(control => { control.disabled = true; });
    status.textContent = "正在刷新群聊…";
    try {
      render(await api.request(`/api/v1/channel-instances/${encodeURIComponent(instance.id)}/conversations/refresh`, {method: "POST", body: "{}"}));
      status.textContent = "";
    } catch (error) {
      status.textContent = channelErrorMessage(error);
    } finally {
      controls.forEach((control, index) => { control.disabled = disabled[index]; });
      refreshButton.disabled = false;
    }
  };
  refreshButton?.addEventListener("click", () => { void refreshGroups(); });
  let pendingSaves = 0;
  body.addEventListener("change", event => {
    const control = event.target;
    const isReception = control instanceof HTMLInputElement && control.hasAttribute("data-group-reception");
    const isMode = control instanceof HTMLSelectElement && control.hasAttribute("data-group-topic-wake");
    if (!isReception && !isMode) return;
    const card = control.closest(".channel-conversation-card");
    const reception = card?.querySelector<HTMLInputElement>("[data-group-reception]");
    const mode = card?.querySelector<HTMLSelectElement>("[data-group-topic-wake]");
    if (!reception?.dataset.groupReception || !mode) return;
    const previousReception = isReception ? !reception.checked : reception.checked;
    const previousMode = mode.dataset.groupTopicWake || "full";
    const chatID = reception.dataset.groupReception;
    reception.disabled = true;
    mode.disabled = true;
    pendingSaves++;
    if (refreshButton) refreshButton.disabled = true;
    const save = isReception ? api.request<{group: NonNullable<ChannelConversationSummary["group"]>}>(
      `/api/v1/channel-instances/${encodeURIComponent(instance.id)}/conversations/groups/${encodeURIComponent(chatID)}/reception`,
      {method: "PUT", body: JSON.stringify({receive_all_messages: reception.checked})},
    ) : api.request<{group: NonNullable<ChannelConversationSummary["group"]>}>(
      `/api/v1/channel-instances/${encodeURIComponent(instance.id)}/conversations/groups/${encodeURIComponent(chatID)}/topic-wake`,
      {method: "PUT", body: JSON.stringify({topic_wake_mode: mode.value})},
    );
    void save.then(({group}) => {
      reception.checked = group.receive_all_messages;
      mode.value = mode.dataset.groupTopicWake = group.topic_wake_mode === "mention" ? "mention" : "full";
      const receptionState = card?.querySelector("[data-group-reception-state]");
      if (receptionState) receptionState.textContent = reception.checked ? "已开启" : "仅 @";
      const hint = card?.querySelector("[data-topic-wake-hint]");
      if (hint) hint.textContent = topicWakeHint(reception.checked, mode.value);
      status.textContent = "";
    }).catch(error => {
      reception.checked = previousReception;
      mode.value = previousMode;
      status.textContent = `保存${isReception ? "群开关" : "话题唤醒方式"}失败：${channelErrorMessage(error)}`;
    }).finally(() => {
      reception.disabled = false;
      mode.disabled = !reception.checked;
      pendingSaves--;
      if (refreshButton && pendingSaves === 0) refreshButton.disabled = false;
    });
  });
  void api.channelConversations(instance.id).then(result => {
    render(result);
    if (dialog.isConnected) void refreshGroups();
  }).catch(error => {
    if (refreshButton) refreshButton.disabled = false;
    body.textContent = error instanceof Error ? error.message : String(error);
    uiOptions.setMessage("error", "会话列表加载失败。");
  });
}

export function renderChannels(providers: ChannelPlugin[], instances: ChannelInstance[], context: ChannelUIContext): string {
  const available = providers.filter(item => item.available);
  const providerRows = providers.length ? providers.map(plugin => `<article class="channel-provider-card ${plugin.available ? "available" : "unavailable"}">
    <div class="channel-provider-main"><span class="square-icon">${icon("channel")}</span><div><strong>${escapeHTML(plugin.display_name)}</strong><small>${escapeHTML(plugin.provider_key)} · ${escapeHTML(plugin.package_version)} · ${pluginState(plugin)}</small></div></div>
    <div class="channel-provider-actions">${plugin.install_state === "installed" ? `<button type="button" data-channel-plugin-toggle="${escapeHTML(plugin.id)}" data-enabled="${plugin.enabled}" data-revision="${plugin.revision}">${plugin.enabled ? "停用" : "启用"}</button>` : ""}</div>
    ${plugin.last_error ? `<p>${escapeHTML(plugin.last_error)}</p>` : ""}
  </article>`).join("") : `<div class="empty channel-empty"><strong>无可用渠道提供方</strong><p>未安装渠道插件时，项目、任务和知识库仍可正常使用。</p></div>`;
  const instanceRows = instances.length ? instances.map(instance => {
	const retired = instance.status === "retired";
	if (retired) return `<article class="channel-instance-card retired">
    <header><div><strong>${escapeHTML(instance.name)}</strong><small>${escapeHTML(instance.provider_key || instance.plugin_id)}</small></div><span class="status warn">${instanceState(instance)}</span></header>
    <p class="channel-retired-note">实例已归档，运行能力与本地凭据已移除；历史记录保持只读，飞书后台应用不会自动删除。</p>
    <div class="channel-retired-actions"><button type="button" data-channel-view="${escapeHTML(instance.id)}" data-project-id="${escapeHTML(instance.host_project_id)}">查看</button><button type="button" data-channel-export="${escapeHTML(instance.id)}">导出</button><button type="button" class="danger" data-channel-purge="${escapeHTML(instance.id)}" data-revision="${instance.revision}">永久删除</button></div>
  </article>`;
    const config = objectValue(instance.config);
    const fullFeishuGroups = instance.provider_key === "feishu" && stringList(config, "channel_permission_ids", []).includes("group_message_read");
    const lastBotTopic = typeof config.runtime_feishu_non_mention_bot_topic_at === "string" ? config.runtime_feishu_non_mention_bot_topic_at : "";
	return `<article class="channel-instance-card">
    <header><div><strong>${escapeHTML(instance.name)}</strong><small>${escapeHTML(instance.provider_key || instance.plugin_id)}</small></div><span><span class="status ${instance.status === "ready" ? "good" : instance.status === "error" || instance.status === "degraded" ? "bad" : "warn"}">${instanceState(instance)}</span><button type="button" data-channel-instance-toggle="${escapeHTML(instance.id)}" data-enabled="${instance.status !== "disabled"}" data-revision="${instance.revision}">${instance.status === "disabled" ? "启用" : "停用"}</button></span></header>
    <dl><div><dt>凭据</dt><dd>${instance.credential_configured ? "已安全保存" : "未配置"}</dd></div><div><dt>App ID</dt><dd>${instance.app_id ? escapeHTML(instance.app_id) : "尚未创建"}</dd></div></dl>
    <div class="channel-instance-actions">
      <button type="button" class="primary" data-channel-onboard="${escapeHTML(instance.id)}">${!instance.owner_bound ? (instance.credential_configured ? "扫码确认唯一 Owner" : "扫码创建并绑定") : "更新权限"}</button>
      <button type="button" data-channel-settings-open="${escapeHTML(instance.id)}">设置</button>
      <button type="button" data-channel-conversations-open="${escapeHTML(instance.id)}">会话</button>
      <button type="button" data-channel-reset-binding="${escapeHTML(instance.id)}" data-revision="${instance.revision}">重置绑定</button>
      <button type="button" class="danger" data-channel-archive="${escapeHTML(instance.id)}" data-revision="${instance.revision}">归档实例</button>
    </div>
    ${instance.owner_bound ? `<small class="channel-instance-hint">权限更新仅做增量授权；AHA 运行时菜单可独立勾选，不修改飞书后台已有菜单。</small>` : ""}
    ${fullFeishuGroups ? `<small class="channel-instance-hint">${lastBotTopic ? `最近接收机器人非 @ 话题消息 ${escapeHTML(lastBotTopic)}` : "机器人非 @ 话题推送待测试群验证。"} </small>` : ""}
    <div data-channel-deliveries="${escapeHTML(instance.id)}" hidden></div>
  </article>`;
  }).join("") : `<div class="empty"><strong>尚未创建渠道实例</strong><p>每个实例独立绑定一个 Owner，并包含私聊助手与群聊电子人。</p></div>`;
  return `<section class="page channels-page">
    <header class="page-head"><div><h1>渠道</h1><p>可选的外部消息渠道；插件缺失或停用不会影响 AHA2 核心功能。</p></div><button id="refresh-channels" type="button">${icon("refresh")}刷新</button></header>
    <section class="panel"><div class="section-head"><div><h2>渠道提供方</h2><p>仅加载受管目录中 manifest 与可执行文件校验通过的插件。</p></div></div><div class="channel-provider-grid">${providerRows}</div></section>
    <section class="panel"><div class="section-head"><div><h2>渠道实例</h2><p>系统宿主 Project/Workspace 在普通项目列表中保持可见。</p></div></div>
      ${available.length ? `<form id="channel-instance-form" class="channel-create-form"><label>提供方<select name="plugin_id">${available.map(item => `<option value="${escapeHTML(item.id)}">${escapeHTML(item.display_name)}</option>`).join("")}</select></label><label>实例名称<input name="name" placeholder="例如：团队飞书" required></label><button class="primary" type="submit">创建实例</button></form>` : ""}
      <div class="channel-instance-grid">${instanceRows}</div>
    </section>
  </section>`;
}

export function bindChannels(options: {refresh: () => Promise<void>; setMessage: (kind: "error" | "success", message: string) => void; context: ChannelUIContext; instances: ChannelInstance[]; openProject?: (projectID: string) => void}): void {
  const refresh = async () => {
    deliveryIssueCache.clear();
    await options.refresh();
  };
  document.querySelector("#refresh-channels")?.addEventListener("click", () => void refresh().catch(error => options.setMessage("error", String(error))));
  document.querySelector<HTMLFormElement>("#channel-instance-form")?.addEventListener("submit", event => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    void api.createChannelInstance(String(form.get("plugin_id") || ""), String(form.get("name") || "")).then(async () => {
      options.setMessage("success", "渠道实例已创建，请继续扫码绑定。");
      await refresh();
    }).catch(error => options.setMessage("error", error instanceof Error ? error.message : String(error)));
  });
  document.querySelectorAll<HTMLButtonElement>("[data-channel-plugin-toggle]").forEach(button => button.addEventListener("click", () => {
    const plugin = button.dataset.channelPluginToggle || "";
    const current = button.dataset.enabled === "true";
    const revision = Number(button.dataset.revision || 0);
    void api.updateChannelPlugin(plugin, !current, revision).then(refresh).catch(error => options.setMessage("error", error instanceof Error ? error.message : String(error)));
  }));
  document.querySelectorAll<HTMLButtonElement>("[data-channel-onboard]").forEach(button => button.addEventListener("click", () => {
    const instance = options.instances.find(item => item.id === button.dataset.channelOnboard);
    if (!instance) return;
    button.disabled = true;
    void api.channelOnboardingOptions(instance.id).then(result => {
      button.disabled = false;
      showOnboardingSetup(instance, result.options, options);
    }).catch(error => {
      button.disabled = false;
      options.setMessage("error", error instanceof Error ? error.message : String(error));
    });
  }));
  document.querySelectorAll<HTMLButtonElement>("[data-channel-instance-toggle]").forEach(button => button.addEventListener("click", () => {
    void api.setChannelInstanceEnabled(button.dataset.channelInstanceToggle || "", button.dataset.enabled !== "true", Number(button.dataset.revision || 0)).then(refresh).catch(error => options.setMessage("error", String(error)));
  }));
	document.querySelectorAll<HTMLButtonElement>("[data-channel-reset-binding]").forEach(button => button.addEventListener("click", () => {
		if (!window.confirm("重置绑定会保留实例设置与宿主历史，清理当前凭据并回到待绑定状态。确定继续？")) return;
		button.disabled = true;
		void api.resetChannelBinding(button.dataset.channelResetBinding || "", Number(button.dataset.revision || 0)).then(async () => {
			options.setMessage("success", "渠道绑定已重置，可以重新扫码绑定。");
			await refresh();
		}).catch(error => {
			button.disabled = false;
			options.setMessage("error", channelErrorMessage(error));
			refreshAfterChannelConflict(error, refresh);
		});
	}));
	document.querySelectorAll<HTMLButtonElement>("[data-channel-archive]").forEach(button => button.addEventListener("click", () => {
		if (!window.confirm("归档后实例及宿主内容保持只读，运行能力与本地凭据会被移除。飞书后台应用不会自动删除。确定归档？")) return;
		button.disabled = true;
		void api.archiveChannelInstance(button.dataset.channelArchive || "", Number(button.dataset.revision || 0)).then(async () => {
			options.setMessage("success", "渠道实例已归档，历史记录保持只读。");
			await refresh();
		}).catch(error => {
			button.disabled = false;
			options.setMessage("error", channelErrorMessage(error));
			refreshAfterChannelConflict(error, refresh);
		});
	}));
	document.querySelectorAll<HTMLButtonElement>("[data-channel-view]").forEach(button => button.addEventListener("click", () => {
		const projectID = button.dataset.projectId || "";
		if (options.openProject && projectID) options.openProject(projectID);
	}));
	document.querySelectorAll<HTMLButtonElement>("[data-channel-export]").forEach(button => button.addEventListener("click", () => {
		button.disabled = true;
		void exportRetiredChannelInstance(button.dataset.channelExport || "").then(() => {
			options.setMessage("success", "归档 JSON 已生成并开始下载。");
		}).catch(error => options.setMessage("error", error instanceof Error ? error.message : String(error))).finally(() => {
			button.disabled = false;
		});
	}));
	document.querySelectorAll<HTMLButtonElement>("[data-channel-purge]").forEach(button => button.addEventListener("click", () => {
		button.disabled = true;
		void purgeRetiredChannelInstance(button.dataset.channelPurge || "", Number(button.dataset.revision || 0)).then(async result => {
			if (result === "name_mismatch") {
				options.setMessage("error", "输入的实例名不匹配，未执行永久删除。");
				return;
			}
			if (result === "purged") {
				options.setMessage("success", "渠道归档及其本地宿主资源已永久删除。");
				await refresh();
			}
		}).catch(error => {
			options.setMessage("error", channelErrorMessage(error));
			refreshAfterChannelConflict(error, refresh);
		}).finally(() => {
			button.disabled = false;
		});
	}));
	document.querySelectorAll<HTMLButtonElement>("[data-channel-settings-open]").forEach(button => button.addEventListener("click", () => {
		const instance = options.instances.find(item => item.id === button.dataset.channelSettingsOpen);
		if (instance) openChannelSettings(instance, options.context, {refresh, setMessage: options.setMessage});
	}));
	document.querySelectorAll<HTMLButtonElement>("[data-channel-conversations-open]").forEach(button => button.addEventListener("click", () => {
		const instance = options.instances.find(item => item.id === button.dataset.channelConversationsOpen);
		if (instance) openChannelConversations(instance, {setMessage: options.setMessage});
	}));
	const bindDeliveryActions = (host: HTMLElement) => host.querySelectorAll<HTMLButtonElement>("[data-channel-delivery-action]").forEach(button => button.addEventListener("click", () => {
		const action = button.dataset.channelDeliveryAction === "replay" ? "replay" : "skip";
		const deliveryID = button.dataset.channelDeliveryId || "";
		const instanceID = button.dataset.channelInstanceId || "";
		if (!deliveryID || !window.confirm(channelDeliveryConfirmMessages[action])) return;
		const buttons = button.parentElement?.querySelectorAll<HTMLButtonElement>("button") || [];
		buttons.forEach(item => { item.disabled = true; });
		const request = action === "replay" ? api.replayChannelDelivery(deliveryID) : api.skipChannelDelivery(deliveryID);
		void request.then(async () => {
			deliveryIssueCache.delete(instanceID);
			options.setMessage("success", action === "replay" ? `已重放投递 ${deliveryID}，旧消息将重新发送。` : `已跳过投递 ${deliveryID}，不再发送。`);
			await refresh();
		}).catch(error => {
			buttons.forEach(item => { item.disabled = false; });
			options.setMessage("error", `${action === "replay" ? "重放" : "跳过"}失败：${channelErrorMessage(error)}`);
			// The page message only shows on the next render, and a failed action
			// often means the row changed elsewhere, so reload the list either way.
			void refresh().catch(() => undefined);
		});
	}));
	document.querySelectorAll<HTMLElement>("[data-channel-deliveries]").forEach(host => {
		const instanceID = host.dataset.channelDeliveries || "";
		if (instanceID) void loadChannelDeliveryIssues(host, instanceID, bindDeliveryActions);
	});
}

let onboardingPoll = 0;
let channelReadinessGeneration = 0;

function permissionOptionsHTML(options: ChannelOnboardingOptions): string {
  return options.permissions.map(item => {
    const locked = item.required || (options.mode === "existing_app" && item.enabled);
    const status = item.required ? "核心权限" : item.enabled ? "已开启" : "可新增";
    return `<label class="channel-onboarding-option ${locked ? "locked" : ""}">
      <input type="checkbox" name="channel_permission_id" value="${escapeHTML(item.id)}" ${item.enabled ? "checked" : ""} ${locked ? "disabled" : ""}>
      <span><strong>${escapeHTML(item.name)}${item.sensitive ? "（敏感权限）" : ""}</strong><small>${escapeHTML(item.description)}</small></span>
      <b>${status}</b>
    </label>`;
  }).join("");
}

function selectedPermissionIDs(dialog: HTMLDialogElement): string[] {
  return [...dialog.querySelectorAll<HTMLInputElement>('input[name="channel_permission_id"]:checked')].map(input => input.value);
}

function showOnboardingSetup(instance: ChannelInstance, options: ChannelOnboardingOptions, uiOptions: {refresh: () => Promise<void>; setMessage: (kind: "error" | "success", message: string) => void}): void {
  document.querySelector("#channel-onboarding-setup")?.remove();
  const dialog = document.createElement("dialog");
  dialog.id = "channel-onboarding-setup";
  dialog.className = "channel-onboarding-setup";
  const existingApp = options.mode === "existing_app";
  dialog.innerHTML = `<form method="dialog"><div class="dialog-head"><div><h2>${existingApp ? "更新飞书权限与发布" : "配置飞书扫码权限"}</h2><small>${existingApp ? "每次确认都提交应用发布；已开启权限不能在扫码中撤销。" : "核心权限固定启用，其他权限按需勾选。"}</small></div><button type="button" class="icon-button" data-setup-close aria-label="${t("common.closeDialog")}">${icon("close")}</button></div>
    <div class="dialog-body channel-onboarding-setup-body">
      <fieldset><legend>飞书权限</legend><div class="channel-onboarding-option-list">${permissionOptionsHTML(options) || "<small>当前提供方没有可选权限。</small>"}</div><p class="field-help">扫码只增补权限。已开启权限需要到飞书开发者后台撤销。</p></fieldset>
      <fieldset><legend>飞书机器人菜单</legend><div class="channel-onboarding-menu-reset"><span><strong>重置为 AHA 默认菜单</strong><small>重新初始化项目、Workspace、任务查询和创建任务菜单，可能提交应用发布。</small></span><button type="button" data-reset-runtime-menus>重置菜单</button></div></fieldset>
      <div class="channel-onboarding-summary" data-onboarding-summary></div>
    </div>
    <div class="dialog-actions"><button type="button" data-setup-close>取消</button><button type="submit" class="primary" data-onboarding-confirm>${existingApp ? "扫码并发布" : "扫码创建并绑定"}</button></div></form>`;
  document.body.append(dialog);
  const close = () => { dialog.close(); dialog.remove(); };
  dialog.querySelectorAll("[data-setup-close]").forEach(button => button.addEventListener("click", close));
  dialog.addEventListener("cancel", event => { event.preventDefault(); close(); });
  const summary = dialog.querySelector<HTMLElement>("[data-onboarding-summary]");
  const confirm = dialog.querySelector<HTMLButtonElement>("[data-onboarding-confirm]");
  const sync = () => {
    const selectedPermissions = selectedPermissionIDs(dialog);
    const additions = options.permissions.filter(item => !item.enabled && selectedPermissions.includes(item.id));
    const requested = existingApp ? additions : options.permissions.filter(item => selectedPermissions.includes(item.id));
    if (summary) {
      const names = requested.map(item => item.name).join("、");
      summary.innerHTML = requested.length
        ? `<strong>将启动飞书扫码</strong><span>本次请求：${escapeHTML(names)}；菜单初始化由独立按钮执行。</span>`
        : `<strong>没有权限变更</strong><span>扫码成功后仍会提交应用版本发布。</span>`;
    }
    if (confirm) confirm.disabled = false;
  };
  dialog.addEventListener("change", sync);
  sync();
  dialog.querySelector<HTMLButtonElement>("[data-reset-runtime-menus]")?.addEventListener("click", event => {
    if (!window.confirm("重置菜单会在飞书后台重新初始化 AHA 默认菜单，并可能提交应用发布。确定重新初始化机器人菜单？")) return;
    const button = event.currentTarget as HTMLButtonElement;
    button.disabled = true;
    void api.resetChannelRuntimeMenus(instance.id, instance.revision).then(async () => {
      close();
      uiOptions.setMessage("success", "已提交飞书菜单初始化，完成状态请查看渠道运行记录。");
      await uiOptions.refresh();
    }).catch(error => {
      button.disabled = false;
      uiOptions.setMessage("error", error instanceof Error ? error.message : String(error));
    });
  });
  dialog.addEventListener("submit", event => {
    event.preventDefault();
    if (!confirm || confirm.disabled) return;
    confirm.disabled = true;
    void api.startChannelOnboarding(instance.id, {
      permission_ids: selectedPermissionIDs(dialog),
    }).then(result => {
      close();
      showOnboarding(result.onboarding, uiOptions);
    }).catch(error => {
      confirm.disabled = false;
      uiOptions.setMessage("error", error instanceof Error ? error.message : String(error));
    });
  });
  dialog.showModal();
}

export async function waitForChannelReadiness(instanceID: string, options: {refresh: () => Promise<void>; setMessage: (kind: "error" | "success", message: string) => void}): Promise<void> {
  const generation = ++channelReadinessGeneration;
  const deadline = Date.now() + 60_000;
  let previousStatus = "";
  while (generation === channelReadinessGeneration && Date.now() < deadline) {
    try {
      const result = await api.channelInstance(instanceID);
      const status = result.instance.status;
      if (status !== previousStatus) {
        previousStatus = status;
        await options.refresh();
      }
      if (status === "ready") {
        options.setMessage("success", "渠道已就绪，可以开始收发消息。");
        return;
      }
      if (["disabled", "retired"].includes(status)) return;
    } catch {
      // The service can be briefly unavailable while the plugin process starts.
      // Keep polling until the bounded deadline; the final state remains visible.
    }
    await new Promise(resolve => window.setTimeout(resolve, 1000));
  }
  if (generation === channelReadinessGeneration) {
    await options.refresh().catch(() => undefined);
    options.setMessage("error", "授权已完成，但渠道尚未就绪；请检查渠道运行状态。");
  }
}

function showOnboarding(initial: ChannelOnboardingSession, options: {refresh: () => Promise<void>; setMessage: (kind: "error" | "success", message: string) => void}): void {
  if (onboardingPoll) window.clearTimeout(onboardingPoll);
  document.querySelector("#channel-onboarding-dialog")?.remove();
  const dialog = document.createElement("dialog");
  dialog.id = "channel-onboarding-dialog";
  dialog.className = "channel-onboarding-dialog";
  const existingApp = initial.mode === "existing_app";
  const title = existingApp ? "扫码更新飞书权限" : "扫码创建飞书应用";
  const notice = existingApp ? "仅增补权限，不修改已有菜单或其他应用配置。即使没有新增权限，扫码确认后也会自动提交应用版本发布；审批结果以飞书后台为准。" : "请在飞书官方页面核对应用名称与最小权限后确认。新应用会执行一次 AHA 菜单初始化并提交发布。";
  dialog.innerHTML = `<div class="dialog-body"><header class="dialog-head"><div><h2>${title}</h2><p>${notice}</p></div><button type="button" class="icon-button" data-onboarding-close aria-label="${t("common.closeDialog")}">${icon("close")}</button></header><div data-onboarding-state class="channel-onboarding-state"></div><div class="dialog-actions"><button type="button" data-onboarding-cancel>取消绑定</button></div></div>`;
  document.body.append(dialog);
  const close = () => { if (onboardingPoll) window.clearTimeout(onboardingPoll); onboardingPoll = 0; dialog.close(); dialog.remove(); };
  dialog.querySelector("[data-onboarding-close]")?.addEventListener("click", () => { close(); void options.refresh(); });
  dialog.querySelector("[data-onboarding-cancel]")?.addEventListener("click", () => void api.cancelChannelOnboarding(initial.id).then(async () => { close(); await options.refresh(); }).catch(error => options.setMessage("error", String(error))));
  dialog.addEventListener("cancel", event => { event.preventDefault(); });
  dialog.showModal();
  const update = (item: ChannelOnboardingSession) => {
    const state = dialog.querySelector<HTMLElement>("[data-onboarding-state]");
    if (!state) return;
    if (item.status === "qr_ready" && item.verification_url) {
      state.innerHTML = `<img src="/api/v1/channel-onboarding-sessions/${encodeURIComponent(item.id)}/qr" alt="飞书授权二维码"><p>二维码将在 ${escapeHTML(new Date(item.expires_at).toLocaleTimeString())} 前有效。</p><a class="primary" target="_blank" rel="noopener noreferrer" href="${escapeHTML(item.verification_url)}">在当前设备打开飞书官方确认页</a>`;
    } else {
      state.innerHTML = `<span class="spinner"></span><strong>${item.status === "succeeded" ? "应用已创建，正在启动渠道" : "正在向飞书申请一次性二维码…"}</strong><small>${escapeHTML(item.step || item.status)}</small>`;
    }
  };
  const poll = async () => {
    try {
      const result = await api.channelOnboarding(initial.id);
      update(result.onboarding);
      if (result.onboarding.status === "succeeded") {
        options.setMessage("success", existingApp ? "飞书增量授权已完成并已提交应用发布，正在等待渠道重新就绪。" : "飞书应用已创建并完成 Owner 绑定，正在等待渠道就绪。");
        window.setTimeout(() => {
          close();
          void options.refresh().finally(() => {
            void waitForChannelReadiness(initial.instance_id, options);
          });
        }, 300);
        return;
      }
      if (["failed", "cancelled", "expired"].includes(result.onboarding.status)) {
        options.setMessage("error", `飞书绑定未完成：${result.onboarding.status}`);
        return;
      }
    } catch (error) {
      options.setMessage("error", error instanceof Error ? error.message : String(error));
    }
    onboardingPoll = window.setTimeout(() => void poll(), 1000);
  };
  update(initial);
  void poll();
}
