import {api} from "./api.js";
import {formatLocaleDateTime, t} from "./i18n.js";
import {icon} from "./icons.js";
import type {MCPServer} from "./types.js";

// The MCP page is one list at every width. >=1041px renders it as a compact
// table-like row (name, connection, status, actions); <=1040px turns the same
// article into a three-line card, because the visible 228px sidebar leaves the
// fixed action columns no room below that. All hooks use .mcp-* so the model
// page's .models-table responsive rules never apply here.

export interface MCPDraft {
  name: string;
  transport: "http" | "stdio";
  url: string;
  command: string;
  args: string;
  description: string;
  headerKeys: string;
  envKeys: string;
  enabled: boolean;
}

export type MCPDraftError = "" | "nameRequired" | "urlRequired" | "commandRequired" | "duplicateName" | "duplicateURL" | "duplicateCommand";

export interface MCPPageOptions {
  servers: MCPServer[];
  reload: () => Promise<unknown>;
  render: () => void;
  setMessage: (type: "error" | "notice", value: string) => void;
}

function escapeHTML(value: unknown): string {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

function transportLabel(server: MCPServer): string {
  const transport = (server.transport || "").toLowerCase();
  if (transport === "builtin") return t("mcp.transport.builtin");
  if (transport === "stdio") return t("mcp.transport.stdio");
  return t("mcp.transport.http");
}

function healthClass(server: MCPServer): string {
  if (!server.enabled) return "off";
  const health = (server.health || "").toLowerCase();
  if (health === "ok" || health === "good" || health === "healthy") return "ok";
  if (health === "error" || health === "failed" || health === "bad" || health === "unhealthy") return "bad";
  return "warn";
}

function toolCount(server: MCPServer): number {
  return (server.tools || []).length;
}

function connectionAddress(server: MCPServer): string {
  if (server.auto_inject) return t("mcp.detail.dynamic");
  if (server.transport === "stdio") {
    return [server.command || "", ...(server.args || [])].filter(Boolean).join(" ");
  }
  if (server.url) return server.url;
  return server.builtin ? t("mcp.detail.builtinAddress") : "";
}

export function parseKeyNames(value: string): string[] {
  const seen = new Set<string>();
  const keys: string[] = [];
  for (const part of String(value || "").split(/[\n,]+/)) {
    const key = part.trim();
    if (!key || seen.has(key)) continue;
    seen.add(key);
    keys.push(key);
  }
  return keys;
}

export function mergeKeyMap(keys: string[], existing?: Record<string, string>): Record<string, string> {
  const map: Record<string, string> = {};
  for (const key of keys) map[key] = existing?.[key] ?? "";
  return map;
}

function normalized(value: string): string {
  return String(value || "").trim().toLowerCase();
}

// mcpDraftError keeps every validation rule in one pure function so the dialog
// and the tests agree on what "empty" and "duplicate" mean. editingID is the
// server being edited; it must not collide with itself.
export function mcpDraftError(draft: MCPDraft, servers: MCPServer[], editingID = ""): MCPDraftError {
  const name = draft.name.trim();
  if (!name) return "nameRequired";
  if (draft.transport === "http") {
    if (!/^https?:\/\/\S+/i.test(draft.url.trim())) return "urlRequired";
  } else if (!draft.command.trim()) {
    return "commandRequired";
  }
  const others = servers.filter(server => server.id !== editingID);
  if (others.some(server => normalized(server.name) === normalized(name))) return "duplicateName";
  if (draft.transport === "http") {
    const url = normalized(draft.url);
    if (others.some(server => server.transport === "http" && normalized(server.url || "") === url)) return "duplicateURL";
  } else {
    const command = draft.command.trim();
    if (others.some(server => server.transport === "stdio" && (server.command || "").trim() === command)) return "duplicateCommand";
  }
  return "";
}

export function mcpDraftErrorMessage(error: MCPDraftError): string {
  return error ? t(`mcp.error.${error}`) : "";
}

function badgeHTML(text: string): string {
  return `<span class="capability-badge">${escapeHTML(text)}</span>`;
}

export function renderMCPRow(server: MCPServer): string {
  const builtin = Boolean(server.builtin);
  const badges = [
    builtin ? badgeHTML(t("mcp.badge.builtin")) : "",
    server.auto_inject ? badgeHTML(t("mcp.badge.autoInject")) : "",
  ].join("");
  const id = escapeHTML(server.id);
  return `<article class="mcp-row${builtin ? " mcp-row-builtin" : ""}" data-mcp-row="${id}" role="listitem">
    <div class="mcp-name"><strong title="${escapeHTML(server.name)}">${escapeHTML(server.name)}</strong>${badges ? `<span class="mcp-badges">${badges}</span>` : ""}</div>
    <div class="mcp-conn"><span class="mcp-mobile-label">${t("mcp.col.transport")}</span><span class="mcp-conn-value">${escapeHTML(transportLabel(server))}</span></div>
    <div class="mcp-status"><span class="mcp-status-dot mcp-status-dot-${healthClass(server)}" aria-hidden="true"></span><span class="mcp-status-text">${server.enabled ? t("mcp.enabled") : t("mcp.disabled")}</span><span class="mcp-tool-count">${t("mcp.toolCount", {count: toolCount(server)})}</span></div>
    <div class="mcp-actions">
      <button type="button" data-mcp-detail="${id}">${t("mcp.actions.detail")}</button>
      ${builtin ? "" : `<button type="button" data-mcp-edit="${id}">${t("mcp.actions.edit")}</button><button type="button" class="mcp-action-danger" data-mcp-delete="${id}">${t("mcp.actions.delete")}</button>`}
    </div>
  </article>`;
}

export function renderMCPPage(servers: MCPServer[]): string {
  const rows = servers.map(renderMCPRow).join("");
  return `<section class="page mcp-page">
    <header class="page-head"><div><h1>${t("mcp.title")}</h1><p>${t("mcp.description")}</p></div><div class="actions"><button type="button" id="mcp-add" class="primary">${icon("plus")}${t("mcp.add")}</button><button type="button" id="mcp-refresh" class="icon-button" title="${t("mcp.refresh")}" aria-label="${t("mcp.refresh")}">${icon("refresh")}</button></div></header>
    <section class="panel mcp-panel"><div class="panel-head"><strong>${t("mcp.listTitle")}</strong><span>${t("mcp.count", {count: servers.length})}</span></div>
      <div class="mcp-list" role="list">
        <div class="mcp-list-head" aria-hidden="true"><span>${t("mcp.col.name")}</span><span>${t("mcp.col.transport")}</span><span>${t("mcp.col.status")}</span><span>${t("mcp.col.actions")}</span></div>
        ${rows || `<div class="mcp-empty">${t("mcp.empty")}</div>`}
      </div>
    </section>
    ${mcpEditDialogHTML()}${mcpDetailDialogHTML()}${mcpDeleteDialogHTML()}
  </section>`;
}

function mcpEditDialogHTML(): string {
  return `<dialog id="mcp-edit-dialog" class="mcp-dialog" aria-labelledby="mcp-edit-title"><form id="mcp-edit-form" novalidate>
    <div class="dialog-head"><h2 id="mcp-edit-title">${t("mcp.edit.addTitle")}</h2><button type="button" data-mcp-close class="icon-button" aria-label="${t("common.closeDialog")}">${icon("close")}</button></div>
    <div class="dialog-body">
      <label>${t("mcp.field.name")}<input name="name" autocomplete="off" placeholder="filesystem"></label>
      <label>${t("mcp.field.transport")}<select name="transport"><option value="http">HTTP</option><option value="stdio">stdio</option></select></label>
      <div class="mcp-fields" data-mcp-fields="http">
        <label>${t("mcp.field.url")}<input name="url" inputmode="url" autocomplete="off" placeholder="https://example.com/mcp"></label>
        <label>${t("mcp.field.headerKeys")}<textarea name="header_keys" rows="3" placeholder="Authorization"></textarea><span class="field-help">${t("mcp.help.keys")}</span></label>
        <p class="field-help">${t("mcp.help.http")}</p>
      </div>
      <div class="mcp-fields" data-mcp-fields="stdio" hidden>
        <label>${t("mcp.field.command")}<input name="command" autocomplete="off" placeholder="npx"></label>
        <label>${t("mcp.field.args")}<input name="args" autocomplete="off" placeholder="-y @modelcontextprotocol/server-filesystem /path"></label>
        <label>${t("mcp.field.envKeys")}<textarea name="env_keys" rows="3" placeholder="BOARD_TOKEN"></textarea><span class="field-help">${t("mcp.help.keys")}</span></label>
        <p class="field-help">${t("mcp.help.stdio")}</p>
      </div>
      <label>${t("mcp.field.description")}<textarea name="description" rows="2"></textarea></label>
      <label class="mcp-enabled-toggle"><input type="checkbox" name="enabled" checked>${t("mcp.field.enabled")}</label>
      <p class="form-error mcp-form-error" id="mcp-edit-error" hidden></p>
      <div class="dialog-actions"><button type="button" data-mcp-close>${t("mcp.cancel")}</button><button type="submit" class="primary">${t("mcp.save")}</button></div>
    </div>
  </form></dialog>`;
}

function mcpDetailDialogHTML(): string {
  return `<dialog id="mcp-detail-dialog" class="mcp-dialog" aria-labelledby="mcp-detail-name">
    <div class="dialog-head"><h2 id="mcp-detail-name">${t("mcp.detail.title")}</h2><button type="button" data-mcp-close class="icon-button" aria-label="${t("common.closeDialog")}">${icon("close")}</button></div>
    <div class="dialog-body" id="mcp-detail-body"></div>
    <div class="dialog-actions"><button type="button" id="mcp-detail-test">${t("mcp.detail.test")}</button><button type="button" class="primary" data-mcp-close>${t("mcp.detail.close")}</button></div>
  </dialog>`;
}

function mcpDeleteDialogHTML(): string {
  return `<dialog id="mcp-delete-dialog" class="mcp-dialog mcp-confirm-dialog" aria-labelledby="mcp-delete-title">
    <div class="dialog-head"><h2 id="mcp-delete-title">${t("mcp.delete.title")}</h2><button type="button" data-mcp-close class="icon-button" aria-label="${t("common.closeDialog")}">${icon("close")}</button></div>
    <div class="dialog-body"><p id="mcp-delete-message"></p></div>
    <div class="dialog-actions"><button type="button" data-mcp-close>${t("mcp.cancel")}</button><button type="button" class="danger" id="mcp-delete-confirm">${t("mcp.delete.confirm")}</button></div>
  </dialog>`;
}

export function renderMCPDetail(server: MCPServer): string {
  const address = connectionAddress(server);
  const health = server.health || "unknown";
  const checkedAt = server.last_checked_at ? formatLocaleDateTime(server.last_checked_at) : t("mcp.detail.neverChecked");
  const disabled = new Set(server.disabled_tools || []);
  const tools = (server.tools || []).map(tool => server.builtin
    ? `<li><div class="mcp-tool-row"><span><strong>${escapeHTML(tool.name)}</strong><small>${escapeHTML(tool.description || "")}</small></span></div></li>`
    : `<li><label class="mcp-tool-row"><input type="checkbox" data-mcp-tool="${escapeHTML(tool.name)}" ${disabled.has(tool.name) ? "" : "checked"}><span><strong>${escapeHTML(tool.name)}</strong><small>${escapeHTML(tool.description || "")}</small></span></label></li>`).join("");
  return `<p class="mcp-detail-description">${escapeHTML(server.description || t("mcp.detail.noDescription"))}</p>
    <dl class="mcp-detail-meta">
      <div><dt>${t("mcp.detail.transport")}</dt><dd>${escapeHTML(transportLabel(server))}</dd></div>
      <div><dt>${t("mcp.detail.address")}</dt><dd class="mcp-detail-address">${escapeHTML(address || "—")}</dd></div>
      <div><dt>${t("mcp.detail.tools")}</dt><dd>${toolCount(server)}</dd></div>
      <div><dt>${t("mcp.detail.health")}</dt><dd><span class="mcp-status-dot mcp-status-dot-${healthClass(server)}" aria-hidden="true"></span>${escapeHTML(health)}</dd></div>
      <div><dt>${t("mcp.detail.lastChecked")}</dt><dd>${escapeHTML(checkedAt)}</dd></div>
    </dl>
    <h3>${t("mcp.detail.toolsList", {count: toolCount(server)})}</h3>
    <ul class="mcp-tool-list">${tools || `<li>${t("mcp.detail.noTools")}</li>`}</ul>
    ${server.last_error ? `<p class="field-help">${escapeHTML(server.last_error)}</p>` : ""}
    ${server.builtin ? "" : `<div class="mcp-detail-actions"><button type="button" data-mcp-detail-edit>${t("mcp.actions.edit")}</button><button type="button" class="danger" data-mcp-detail-delete>${t("mcp.actions.delete")}</button></div>`}`;
}

// ---------------------------------------------------------------- binding

const focusReturn = new WeakMap<HTMLDialogElement, HTMLElement | null>();

function rememberFocus(dialog: HTMLDialogElement, returnTo?: HTMLElement | null): void {
  const active = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  focusReturn.set(dialog, returnTo ?? active);
}

function bindDialogDismissal(dialog: HTMLDialogElement): void {
  dialog.querySelectorAll<HTMLElement>("[data-mcp-close]").forEach(element => {
    element.onclick = () => dialog.close();
  });
  // A modal dialog's backdrop is the dialog element itself; clicks inside the
  // form/body stop at their own elements. Native showModal keeps the rest of
  // the page inert, so background controls are not tabbable.
  dialog.onclick = event => {
    if (event.target === dialog) dialog.close();
  };
  dialog.onclose = () => {
    const target = focusReturn.get(dialog);
    focusReturn.delete(dialog);
    if (target?.isConnected) target.focus();
  };
}

function attributeValue(value: string): string {
  return value.replaceAll("\\", "\\\\").replaceAll('"', '\\"');
}

function findRowButton(id: string, action: "detail" | "edit" | "delete"): HTMLElement | null {
  if (!id) return null;
  return document.querySelector<HTMLElement>(`[data-mcp-${action}="${attributeValue(id)}"]`);
}

function focusRowButton(id: string, action: "detail" | "add" = "detail"): void {
  window.setTimeout(() => {
    const target = action === "add" ? document.querySelector<HTMLElement>("#mcp-add") : findRowButton(id, "detail");
    target?.focus();
  }, 0);
}

function setFormError(form: HTMLFormElement, message: string): void {
  const box = form.querySelector<HTMLElement>("#mcp-edit-error");
  if (!box) return;
  box.textContent = message;
  box.hidden = !message;
}

function fieldValue(form: HTMLFormElement, name: string): string {
  const element = form.elements.namedItem(name);
  return element instanceof HTMLInputElement || element instanceof HTMLTextAreaElement || element instanceof HTMLSelectElement
    ? element.value
    : "";
}

function readDraft(form: HTMLFormElement, editing: MCPServer | null): MCPDraft {
  const transport = fieldValue(form, "transport") === "stdio" ? "stdio" : "http";
  return {
    name: fieldValue(form, "name"),
    transport,
    url: transport === "http" ? fieldValue(form, "url") : (editing?.url || ""),
    command: transport === "stdio" ? fieldValue(form, "command") : (editing?.command || ""),
    args: transport === "stdio" ? fieldValue(form, "args") : (editing?.args || []).join(" "),
    description: fieldValue(form, "description"),
    headerKeys: transport === "http" ? fieldValue(form, "header_keys") : "",
    envKeys: transport === "stdio" ? fieldValue(form, "env_keys") : "",
    enabled: form.elements.namedItem("enabled") instanceof HTMLInputElement
      ? (form.elements.namedItem("enabled") as HTMLInputElement).checked
      : true,
  };
}

function syncTransportFields(form: HTMLFormElement, transport: string): void {
  form.querySelectorAll<HTMLElement>("[data-mcp-fields]").forEach(section => {
    section.hidden = section.dataset.mcpFields !== transport;
  });
}

function openMCPEditDialog(options: MCPPageOptions, server: MCPServer | null, returnTo?: HTMLElement | null): void {
  const dialog = document.querySelector<HTMLDialogElement>("#mcp-edit-dialog");
  const form = dialog?.querySelector<HTMLFormElement>("#mcp-edit-form");
  if (!dialog || !form) return;
  const title = dialog.querySelector<HTMLElement>("#mcp-edit-title");
  if (title) title.textContent = server ? t("mcp.edit.editTitle") : t("mcp.edit.addTitle");
  form.reset();
  setFormError(form, "");
  form.dataset.mcpEditingId = server?.id || "";
  const transport = server?.transport === "stdio" ? "stdio" : "http";
  const transportSelect = form.elements.namedItem("transport");
  if (transportSelect instanceof HTMLSelectElement) transportSelect.value = transport;
  syncTransportFields(form, transport);
  const set = (name: string, value: string) => {
    const element = form.elements.namedItem(name);
    if (element instanceof HTMLInputElement || element instanceof HTMLTextAreaElement || element instanceof HTMLSelectElement) element.value = value;
  };
  set("name", server?.name || "");
  set("url", server?.url || "");
  set("command", server?.command || "");
  set("args", (server?.args || []).join(" "));
  set("description", server?.description || "");
  set("header_keys", Object.keys(server?.headers || {}).join("\n"));
  set("env_keys", Object.keys(server?.env || {}).join("\n"));
  const enabled = form.elements.namedItem("enabled");
  if (enabled instanceof HTMLInputElement) enabled.checked = server ? server.enabled : true;
  rememberFocus(dialog, returnTo);
  bindDialogDismissal(dialog);
  if (transportSelect instanceof HTMLSelectElement) {
    transportSelect.onchange = () => syncTransportFields(form, transportSelect.value);
  }
  form.oninput = () => setFormError(form, "");
  form.onsubmit = event => {
    event.preventDefault();
    void submitMCPForm(options, dialog, form, server);
  };
  dialog.showModal();
  window.setTimeout(() => form.querySelector<HTMLInputElement>('[name="name"]')?.focus(), 0);
}

async function submitMCPForm(options: MCPPageOptions, dialog: HTMLDialogElement, form: HTMLFormElement, editing: MCPServer | null): Promise<void> {
  const draft = readDraft(form, editing);
  const error = mcpDraftError(draft, options.servers, editing?.id || "");
  if (error) {
    setFormError(form, mcpDraftErrorMessage(error));
    return;
  }
  const submit = form.querySelector<HTMLButtonElement>('button[type="submit"]');
  if (submit) submit.disabled = true;
  try {
    const payload: Record<string, unknown> = {
      ...(editing || {}),
      name: draft.name.trim(),
      transport: draft.transport,
      url: draft.transport === "http" ? draft.url.trim() : "",
      command: draft.transport === "stdio" ? draft.command.trim() : "",
      args: draft.transport === "stdio" ? draft.args.trim().split(/\s+/).filter(Boolean) : [],
      description: draft.description.trim(),
      enabled: draft.enabled,
      headers: draft.transport === "http" ? mergeKeyMap(parseKeyNames(draft.headerKeys), editing?.headers) : (editing?.headers || {}),
      env: draft.transport === "stdio" ? mergeKeyMap(parseKeyNames(draft.envKeys), editing?.env) : (editing?.env || {}),
    };
    if (editing) {
      payload.id = editing.id;
      payload.created_at = editing.created_at;
    } else {
      delete payload.id;
    }
    const result = await api.saveMCPServer(payload);
    await options.reload();
    dialog.close();
    options.setMessage("notice", editing ? t("mcp.notice.saved") : t("mcp.notice.added"));
    options.render();
    focusRowButton(result.server?.id || editing?.id || "", "detail");
  } catch (error) {
    setFormError(form, error instanceof Error ? error.message : String(error));
  } finally {
    if (submit?.isConnected) submit.disabled = false;
  }
}

function openMCPDeleteDialog(options: MCPPageOptions, server: MCPServer, returnTo?: HTMLElement | null): void {
  const dialog = document.querySelector<HTMLDialogElement>("#mcp-delete-dialog");
  const message = dialog?.querySelector<HTMLElement>("#mcp-delete-message");
  const confirm = dialog?.querySelector<HTMLButtonElement>("#mcp-delete-confirm");
  if (!dialog || !message || !confirm) return;
  message.textContent = t("mcp.delete.message", {name: server.name});
  confirm.disabled = false;
  confirm.onclick = () => {
    confirm.disabled = true;
    void api.deleteMCPServer(server.id)
      .then(() => options.reload())
      .then(() => {
        dialog.close();
        options.setMessage("notice", t("mcp.notice.deleted"));
        options.render();
        focusRowButton("", "add");
      })
      .catch((error: unknown) => {
        confirm.disabled = false;
        options.setMessage("error", error instanceof Error ? error.message : String(error));
      });
  };
  rememberFocus(dialog, returnTo);
  bindDialogDismissal(dialog);
  dialog.showModal();
}

async function openMCPDetail(options: MCPPageOptions, id: string, returnTo?: HTMLElement | null): Promise<void> {
  const dialog = document.querySelector<HTMLDialogElement>("#mcp-detail-dialog");
  const body = dialog?.querySelector<HTMLElement>("#mcp-detail-body");
  const name = dialog?.querySelector<HTMLElement>("#mcp-detail-name");
  const test = dialog?.querySelector<HTMLButtonElement>("#mcp-detail-test");
  if (!dialog || !body || !name || !test) return;
  rememberFocus(dialog, returnTo);
  bindDialogDismissal(dialog);
  body.innerHTML = `<p>${t("common.loading")}</p>`;
  test.onclick = null;
  dialog.showModal();
  try {
    const result = await api.mcpServer(id);
    if (!dialog.open) return;
    const renderServer = (server: MCPServer) => {
      name.textContent = server.name;
      body.innerHTML = renderMCPDetail(server);
      body.querySelector<HTMLElement>("[data-mcp-detail-edit]")?.addEventListener("click", () => {
        dialog.close();
        openMCPEditDialog(options, server, findRowButton(server.id, "edit"));
      });
      body.querySelector<HTMLElement>("[data-mcp-detail-delete]")?.addEventListener("click", () => {
        dialog.close();
        openMCPDeleteDialog(options, server, findRowButton(server.id, "delete"));
      });
      // Tool toggles stay inside the detail dialog: the list only shows the
      // count, so saving here does not need to rebuild the page behind it.
      body.querySelectorAll<HTMLInputElement>("[data-mcp-tool]").forEach(input => {
        input.addEventListener("change", () => {
          const disabledTools = [...body.querySelectorAll<HTMLInputElement>("[data-mcp-tool]")]
            .filter(item => !item.checked)
            .map(item => item.dataset.mcpTool || "");
          input.disabled = true;
          void api.saveMCPServer({...server, disabled_tools: disabledTools})
            .then(updated => {
              const index = options.servers.findIndex(item => item.id === updated.server.id);
              if (index >= 0) options.servers[index] = updated.server;
              options.setMessage("notice", t("mcp.notice.saved"));
              renderServer(updated.server);
            })
            .catch((error: unknown) => {
              input.checked = !input.checked;
              input.disabled = false;
              options.setMessage("error", error instanceof Error ? error.message : String(error));
            });
        });
      });
      test.onclick = () => {
        test.disabled = true;
        void api.testMCPServer(server.id)
          .then(updated => {
            renderServer(updated.server);
            options.setMessage("notice", t("mcp.notice.toolsRefreshed"));
            void options.reload().then(() => options.render()).catch(() => undefined);
          })
          .catch((error: unknown) => options.setMessage("error", error instanceof Error ? error.message : String(error)))
          .finally(() => { test.disabled = false; });
      };
    };
    renderServer(result.server);
  } catch (error) {
    body.innerHTML = `<p class="field-help">${escapeHTML(error instanceof Error ? error.message : String(error))}</p>`;
  }
}

export function bindMCPPage(options: MCPPageOptions): void {
  const add = document.querySelector<HTMLButtonElement>("#mcp-add");
  add?.addEventListener("click", () => openMCPEditDialog(options, null, add));
  document.querySelector("#mcp-refresh")?.addEventListener("click", () => {
    void options.reload().then(() => options.render()).catch((error: unknown) => options.setMessage("error", error instanceof Error ? error.message : String(error)));
  });
  document.querySelectorAll<HTMLButtonElement>("[data-mcp-detail]").forEach(button => {
    button.addEventListener("click", () => { void openMCPDetail(options, button.dataset.mcpDetail || "", button); });
  });
  document.querySelectorAll<HTMLButtonElement>("[data-mcp-edit]").forEach(button => {
    button.addEventListener("click", () => {
      const server = options.servers.find(item => item.id === button.dataset.mcpEdit);
      if (server) openMCPEditDialog(options, server, button);
    });
  });
  document.querySelectorAll<HTMLButtonElement>("[data-mcp-delete]").forEach(button => {
    button.addEventListener("click", () => {
      const server = options.servers.find(item => item.id === button.dataset.mcpDelete);
      if (server) openMCPDeleteDialog(options, server, button);
    });
  });
  const editDialog = document.querySelector<HTMLDialogElement>("#mcp-edit-dialog");
  const detailDialog = document.querySelector<HTMLDialogElement>("#mcp-detail-dialog");
  const deleteDialog = document.querySelector<HTMLDialogElement>("#mcp-delete-dialog");
  if (editDialog) bindDialogDismissal(editDialog);
  if (detailDialog) bindDialogDismissal(detailDialog);
  if (deleteDialog) bindDialogDismissal(deleteDialog);
}