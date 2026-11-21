# task-728 review packet diff 2：Web 源码与测试

- 基线 12b9be1 → b285d54
- 内容（按完整 diff 的路径顺序）：`web/src`、`web/tests`
- 本卷是 `git diff 12b9be1 b285d54 -- web/src web/tests` 的原样输出，sha256 `4ce62fffbc5a2ab772fd00eaf8dbe52cec21246895710bf8f1290f20dac3d355`，29558 字节

~~~~diff
diff --git a/web/src/api.ts b/web/src/api.ts
index 160758a..911d369 100644
--- a/web/src/api.ts
+++ b/web/src/api.ts
@@ -1441,7 +1441,7 @@ class APIClient {
     return this.request(`/api/v1/pet/activity?limit=${limit}`);
   }
 
-  updatePetSettings(payload: {enabled: boolean; character: string; chat_model_id: string; brain_model_id?: string; pet_model_source?: string; pet_model_id?: string; pet_wire_model?: string; pet_codex_account_id?: string; actions: string[]; identity?: string; principles?: string; idle_minutes: number; quiet_start: number; quiet_end: number; hourly_limit: number; free_daily_turns?: number; allow_view_tasks?: boolean; allow_create_tasks?: boolean; allow_create_workspaces?: boolean; allow_message_tasks?: boolean; allow_manage_models?: boolean; allow_manage_knowledge?: boolean; allow_manage_channels?: boolean; allow_manage_system?: boolean}): Promise<{ok: boolean; assistant: OwnerAssistant; models: AssistantModelOption[]; model_name?: string; brain_model_name?: string; pet_model_id?: string; pet_model_source?: string; pet_wire_model?: string; pet_codex_account_id?: string; pet_model_name?: string; pet_workspace_id?: string}> {
+  updatePetSettings(payload: {enabled: boolean; character: string; chat_model_id?: string; brain_model_id?: string; pet_model_source?: string; pet_model_id?: string; pet_wire_model?: string; pet_codex_account_id?: string; actions: string[]; identity?: string; principles?: string; idle_minutes: number; quiet_start: number; quiet_end: number; hourly_limit: number; free_daily_turns?: number; allow_view_tasks?: boolean; allow_create_tasks?: boolean; allow_create_workspaces?: boolean; allow_message_tasks?: boolean; allow_manage_models?: boolean; allow_manage_knowledge?: boolean; allow_manage_channels?: boolean; allow_manage_system?: boolean}): Promise<{ok: boolean; assistant: OwnerAssistant; models: AssistantModelOption[]; model_name?: string; brain_model_name?: string; pet_model_id?: string; pet_model_source?: string; pet_wire_model?: string; pet_codex_account_id?: string; pet_model_name?: string; pet_account_name?: string; pet_model_label?: string; pet_model_available?: boolean; pet_model_unavailable_reason?: string; pet_workspace_id?: string}> {
     return this.request("/api/v1/pet/settings", {method: "PUT", body: JSON.stringify(payload)});
   }
 
diff --git a/web/src/assistant.ts b/web/src/assistant.ts
index ee7d178..b2fdceb 100644
--- a/web/src/assistant.ts
+++ b/web/src/assistant.ts
@@ -76,7 +76,6 @@ let trailLayer: HTMLElement;
 let bubbleTitle: HTMLElement;
 let bubbleBody: HTMLElement;
 let panel: HTMLElement;
-let modelSelect: HTMLSelectElement;
 let modelLabel: HTMLElement;
 let view: OwnerAssistantView | null = null;
 let petModelContext: {models: Model[]; accounts: CodexAccount[]; workspace?: Workspace} | null = null;
@@ -140,7 +139,7 @@ let settingsQuietStart: HTMLInputElement;
 let settingsQuietEnabled: HTMLInputElement;
 let settingsQuietEnd: HTMLInputElement;
 let settingsCharacters: HTMLElement;
-let settingsChatModel: HTMLSelectElement;
+let settingsChatModel: HTMLInputElement;
 let settingsPetModelHost: HTMLElement;
 let settingsBrainStatus: HTMLElement;
 let settingsError: HTMLElement;
@@ -194,7 +193,6 @@ export function mountAssistant(): void {
             <span id="assistant-character-mini" class="assistant-character-mini" aria-hidden="true"></span>
             <span class="assistant-character-badge" aria-hidden="true">✦</span>
           </button>
-          <select id="assistant-model" aria-label="助手模型"></select>
           <button id="assistant-close" class="icon-button" type="button" aria-label="收起小A">×</button>
         </div>
       </header>
@@ -257,9 +255,9 @@ export function mountAssistant(): void {
             <p class="assistant-settings-note">权限变更立即生效，不重置会话；无权限的工具调用会被服务端拒绝。删除 Task/项目/Workspace 会先创建审批，owner 批准后才执行。</p>
           </section>
           <section><h3>模型</h3>
-            <label class="assistant-settings-field">助手聊天模型（Provider）<select id="pet-chat-model"></select></label>
+            <label class="assistant-settings-field">助手聊天模型<input id="pet-chat-model" type="text" readonly aria-describedby="pet-chat-model-help"></label>
             <div id="pet-model-source-host"></div>
-            <p class="assistant-settings-note">聊天面板使用 Provider 模型；自主行为在宠物 Task 中运行，可选择 Provider、Claude native 或 Codex 官方账号。不可用来源不会回退。</p>
+            <p class="assistant-settings-note" id="pet-chat-model-help">聊天和自主行为使用同一个模型；下面的「模型来源 / 模型账号」是唯一修改入口。不可用来源不会回退。</p>
           </section>
           <section class="assistant-settings-danger"><h3>恢复出厂设置</h3>
             <p class="assistant-settings-note">只清理本机宠物 Workspace、Task、工作记录/身份设定、审批与活动记录、聊天记录，并停用本机宠物。宠物项目、同步镜像和其他设备的宠物不受影响；模型、形象和权限设置会保留。</p>
@@ -319,7 +317,7 @@ export function mountAssistant(): void {
   settingsQuietStart = layer.querySelector<HTMLInputElement>("#pet-quiet-start")!;
   settingsQuietEnd = layer.querySelector<HTMLInputElement>("#pet-quiet-end")!;
   settingsCharacters = layer.querySelector<HTMLElement>("#pet-character-options")!;
-  settingsChatModel = layer.querySelector<HTMLSelectElement>("#pet-chat-model")!;
+  settingsChatModel = layer.querySelector<HTMLInputElement>("#pet-chat-model")!;
   settingsPetModelHost = layer.querySelector<HTMLElement>("#pet-model-source-host")!;
   settingsBrainStatus = layer.querySelector<HTMLElement>("#pet-brain-status")!;
   settingsError = layer.querySelector<HTMLElement>("#pet-settings-error")!;
@@ -347,7 +345,6 @@ export function mountAssistant(): void {
   bubbleBody = layer.querySelector<HTMLElement>("#assistant-bubble-body")!;
   panel = layer.querySelector<HTMLElement>("#assistant-panel")!;
   panel.style.zIndex = "4";
-  modelSelect = layer.querySelector<HTMLSelectElement>("#assistant-model")!;
   modelLabel = layer.querySelector<HTMLElement>("#assistant-model-name")!;
   cachedCharacter = readCachedCharacter();
   characterReady = Boolean(cachedCharacter);
@@ -507,16 +504,6 @@ function bindEvents(): void {
       setPanelOpen(true);
     }
   });
-  modelSelect.addEventListener("change", () => {
-    const modelID = modelSelect.value;
-    if (!modelID || !view) return;
-    void api.updateOwnerAssistant({model_id: modelID}).then(result => {
-      if (view) {
-        view = {...view, assistant: result.assistant, models: result.models, model_name: result.model_name};
-        renderModel();
-      }
-    }).catch(error => appendLocalError(error instanceof Error ? error.message : String(error)));
-  });
   characterButton.addEventListener("click", event => {
     event.stopPropagation();
     void openPetSettings();
@@ -609,7 +596,13 @@ function bindEvents(): void {
 async function refreshAssistant(): Promise<void> {
   try {
     const result = await api.ownerAssistant();
-    view = {assistant: result.assistant, messages: result.messages || [], models: result.models || [], model_name: result.model_name, brain_model_name: result.brain_model_name, tools: result.tools || []};
+    view = {assistant: result.assistant, messages: result.messages || [], models: result.models || [], model_name: result.model_name, brain_model_name: result.brain_model_name, tools: result.tools || [],
+      pet_model_id: result.pet_model_id, pet_model_source: result.pet_model_source,
+      pet_wire_model: result.pet_wire_model, pet_codex_account_id: result.pet_codex_account_id,
+      pet_model_name: result.pet_model_name, pet_account_name: result.pet_account_name,
+      pet_model_label: result.pet_model_label, pet_model_available: result.pet_model_available,
+      pet_model_unavailable_reason: result.pet_model_unavailable_reason,
+      pet_workspace_id: result.pet_workspace_id};
     rememberCharacter(view.assistant.character);
     renderCharacter();
     renderCharacterMenu();
@@ -774,7 +767,14 @@ function renderPetSettings(): void {
     <span class="assistant-character-preview">${characterMarkup(character)}</span>
     <strong>${escapeHTML(isPetNoCharacter(character.id) ? character.name : character.nickname)}</strong>
   </button>`).join("");
-  settingsChatModel.innerHTML = view.models.map(model => `<option value="${escapeHTML(model.id)}" ${model.id === view!.assistant.model_id ? "selected" : ""}>${escapeHTML([model.provider_name, model.display_name].filter(Boolean).join(" / ") || model.id)}</option>`).join("");
+  // Read-only mirror of the unified model. It is rendered from the same
+  // pet_model_* fields that seed the source picker below, so the two cannot
+  // show different values.
+  settingsChatModel.value = String(view.pet_model_label || "未配置模型");
+  settingsChatModel.title = view.pet_model_available === false
+    ? `当前配置不可用：${view.pet_model_unavailable_reason || "请重新选择"}`
+    : "聊天和自主行为使用同一个模型；请用下面的「模型来源 / 模型账号」修改";
+  settingsChatModel.classList.toggle("is-unavailable", view.pet_model_available === false);
   renderPetModelPicker();
   settingsIdleMinutes.value = String(view.assistant.pet_idle_minutes || 5);
   settingsHourlyLimit.value = String(view.assistant.pet_hourly_limit ?? 6);
@@ -871,7 +871,7 @@ async function savePetSettings(): Promise<void> {
   settingsError.hidden = true;
   try {
     const result = await api.updatePetSettings({
-      enabled, character, chat_model_id: settingsChatModel.value,
+      enabled, character,
       pet_model_source: petRuntime.model_source, pet_model_id: petRuntime.model_id,
       pet_wire_model: petRuntime.wire_model, pet_codex_account_id: petRuntime.codex_account_id,
       actions,
@@ -895,7 +895,11 @@ async function savePetSettings(): Promise<void> {
       model_name: result.model_name ?? view.model_name, brain_model_name: result.brain_model_name ?? view.brain_model_name,
       pet_model_id: result.pet_model_id ?? view.pet_model_id, pet_model_source: result.pet_model_source ?? view.pet_model_source,
       pet_wire_model: result.pet_wire_model ?? view.pet_wire_model, pet_codex_account_id: result.pet_codex_account_id ?? view.pet_codex_account_id,
-      pet_model_name: result.pet_model_name ?? view.pet_model_name, pet_workspace_id: result.pet_workspace_id ?? view.pet_workspace_id,
+      pet_model_name: result.pet_model_name ?? view.pet_model_name, pet_account_name: result.pet_account_name ?? view.pet_account_name,
+      pet_model_label: result.pet_model_label ?? view.pet_model_label,
+      pet_model_available: result.pet_model_available ?? view.pet_model_available,
+      pet_model_unavailable_reason: result.pet_model_unavailable_reason ?? view.pet_model_unavailable_reason,
+      pet_workspace_id: result.pet_workspace_id ?? view.pet_workspace_id,
     };
     rememberCharacter(view.assistant.character);
     renderCharacter();
@@ -1304,13 +1308,14 @@ function schedulePetSync(patch: PetStatePatch): void {
 
 function renderModel(): void {
   if (!view) return;
-  modelLabel.textContent = view.model_name ? "模型 · " + view.model_name : "未配置模型";
-  const options = view.models.map(model => {
-    const label = [model.provider_name, model.display_name].filter(Boolean).join(" / ");
-    return `<option value="${escapeHTML(model.id)}" ${model.id === view!.assistant.model_id ? "selected" : ""}>${escapeHTML(label || model.id)}</option>`;
-  }).join("");
-  modelSelect.innerHTML = options || `<option value="">未配置模型</option>`;
-  modelSelect.disabled = view.models.length <= 1;
+  // One model for chat and autonomy: the panel shows the unified selection and
+  // offers no independent chooser. The source picker in pet settings is the
+  // only place that writes it.
+  const label = String(view.pet_model_label || view.model_name || "").trim();
+  modelLabel.textContent = label || "未配置模型";
+  modelLabel.title = view.pet_model_available === false
+    ? `当前配置不可用：${view.pet_model_unavailable_reason || "请重新选择"}`
+    : label;
 }
 
 function nickname(): string {
diff --git a/web/src/i18n.ts b/web/src/i18n.ts
index a52bf95..87d557b 100644
--- a/web/src/i18n.ts
+++ b/web/src/i18n.ts
@@ -1855,6 +1855,9 @@ const legacyEnglish: Record<string, string> = {
   "关闭后任何时间都可能自主互动": "When off, autonomous interaction can happen at any time",
   "宠物模型（聊天 + 自主行为）": "Pet model (chat + autonomy)",
   "助手聊天模型（Provider）": "Assistant chat model (Provider)",
+  "助手聊天模型": "Assistant chat model",
+  "聊天和自主行为使用同一个模型；下面的「模型来源 / 模型账号」是唯一修改入口。不可用来源不会回退。": "Chat and autonomy use the same model. Model source / model account below is the only place to change it. An unavailable source never falls back.",
+  "聊天和自主行为使用同一个模型；请用下面的「模型来源 / 模型账号」修改": "Chat and autonomy use the same model; change it with Model source / model account below",
   "聊天面板使用 Provider 模型；自主行为在宠物 Task 中运行，可选择 Provider、Claude native 或 Codex 官方账号。不可用来源不会回退。": "The chat panel uses a Provider model; autonomy runs in the Pet Task and can use a Provider, Claude native, or Codex official account. Unavailable sources never fall back.",
   "立即触发一次自主行为": "Trigger autonomy now",
   "已请求一次自主行为，等待 Task 执行…": "Autonomy requested; waiting for the Task",
diff --git a/web/src/styles.css b/web/src/styles.css
index d53bd4c..ccf4917 100644
--- a/web/src/styles.css
+++ b/web/src/styles.css
@@ -2650,6 +2650,8 @@ dialog.agent-config-dialog { width: min(760px,calc(100vw - 32px)); }
 .pet-action-option { display: flex; align-items: center; gap: 5px; padding: 7px 10px; border: 1px solid #dbe8f7; border-radius: 9px; background: #f8fbff; font-size: 12px; }
 .assistant-settings-field { display: grid; gap: 4px; margin-top: 7px; color: #5c7187; font-size: 11px; }
 .assistant-settings-field select { min-height: 32px; padding: 0 8px; border: 1px solid #d7e2f0; border-radius: 8px; background: #fff; color: #17324d; }
+.assistant-settings-field input[readonly] { min-height: 32px; padding: 0 8px; border: 1px solid #d7e2f0; border-radius: 8px; background: #f6f9fd; color: #52677d; }
+.assistant-settings-field input[readonly].is-unavailable { border-color: #e7b4b4; background: #fff7f7; color: #a33a3a; }
 .assistant-settings-error { margin: 0; color: #b42318; font-size: 11px; }
 .assistant-settings-foot { display: flex; justify-content: flex-end; gap: 7px; padding: 10px 14px; border-top: 1px solid #e7eef7; background: #fbfdff; }
 .assistant-settings-foot button { min-height: 32px; padding: 0 14px; border: 1px solid #d7e2f0; border-radius: 8px; background: #fff; color: #25517d; font-size: 12px; }
diff --git a/web/src/types.ts b/web/src/types.ts
index e6b85a1..426584b 100644
--- a/web/src/types.ts
+++ b/web/src/types.ts
@@ -1714,6 +1714,11 @@ export interface OwnerAssistantView {
   pet_wire_model?: string;
   pet_codex_account_id?: string;
   pet_model_name?: string;
+  pet_account_name?: string;
+  /** Readable "source · model/account" description for the read-only chat field. */
+  pet_model_label?: string;
+  pet_model_available?: boolean;
+  pet_model_unavailable_reason?: string;
   pet_workspace_id?: string;
   tools?: PetToolInfo[];
   pet_last_brain_at?: string;
diff --git a/web/tests/assistant.test.mjs b/web/tests/assistant.test.mjs
index dea6e43..251de6b 100644
--- a/web/tests/assistant.test.mjs
+++ b/web/tests/assistant.test.mjs
@@ -54,6 +54,20 @@ test("pet uses the cached character before the first API response", async () =>
   assert.match(styles, /\.assistant-pet\.character-loading \{ visibility: hidden; \}/);
 });
 
+test("chat model is a read-only mirror of the unified pet model", async () => {
+  const assistant = await readFile(resolve(root, "build", "assistant.js"), "utf8");
+  // The chat field is a readonly input, not a chooser, and the old panel-level
+  // model switch is gone: the source picker is the only edit entry.
+  assert.match(assistant, /id="pet-chat-model" type="text" readonly/);
+  assert.doesNotMatch(assistant, /id="pet-chat-model"><\/select>/);
+  assert.doesNotMatch(assistant, /id="assistant-model"/);
+  // Saving must not send the legacy independent chat field at all.
+  assert.doesNotMatch(assistant, /chat_model_id/);
+  // The read-only box and the picker are both fed by the server's one label.
+  assert.match(assistant, /settingsChatModel\.value = String\(view\.pet_model_label/);
+  assert.match(assistant, /modelLabel\.textContent = label/);
+});
+
 test("built assistant ships the no-character choice without a pet kill switch", async () => {
   const assistant = await readFile(resolve(root, "build", "assistant.js"), "utf8");
   const character = await readFile(resolve(root, "build", "pet_character.js"), "utf8");
diff --git a/web/tests/build.test.mjs b/web/tests/build.test.mjs
index 1ead8bb..8c8fcd7 100644
--- a/web/tests/build.test.mjs
+++ b/web/tests/build.test.mjs
@@ -3893,7 +3893,10 @@ test("production web splits route chunks and keeps the first-screen budget", asy
   // the shared bundle gained the per-form labels, endpoints and fields (+480
   // JS, +41 CSS over main's 272,829); measured 273,350, next 256-byte boundary.
   // ADB 三模式新增 UI，实测 273,350，owner 侧分身上限 +512 字节（272,896 → 273,408）。
-  const FIRST_SCREEN_GZIP_BUDGET = 273408;
+  // task-728 unified pet model: the readonly chat field, its shared Chinese/English
+  // copy and the unavailable-state style add 136 bytes over 273,408; measured
+  // 273,544, next 256-byte boundary.
+  const FIRST_SCREEN_GZIP_BUDGET = 273664;
   // The version query parameter is a content hash and its hex digits change
   // how the same code gzips: main already sits exactly on the budget, so a
   // hash-only change could trip it without growing the payload. Mask the
diff --git a/web/tests/pet_model_readonly.browser.mjs b/web/tests/pet_model_readonly.browser.mjs
new file mode 100644
index 0000000..38e4cba
--- /dev/null
+++ b/web/tests/pet_model_readonly.browser.mjs
@@ -0,0 +1,176 @@
+// Browser-level check for the unified pet model settings:
+// - the chat model field is a read-only mirror, not a chooser;
+// - saving never sends an independent chat_model_id;
+// - after save the read-only field and the source picker show the same model.
+import assert from "node:assert/strict";
+import {createServer} from "node:http";
+import {readFile} from "node:fs/promises";
+import {resolve, extname} from "node:path";
+import {pathToFileURL} from "node:url";
+
+const root = resolve(import.meta.dirname, "../..");
+const playwrightModule = process.env.AHA_PLAYWRIGHT_MODULE;
+const {chromium} = await import(pathToFileURL(playwrightModule || resolve(root, ".tools/web-validation/node_modules/playwright/index.mjs")).href);
+const executable = process.env.AHA_BROWSER_EXECUTABLE
+  ? {executablePath: async () => process.env.AHA_BROWSER_EXECUTABLE, args: []}
+  : (await import(pathToFileURL(resolve(root, ".tools/web-validation/node_modules/@sparticuz/chromium/build/index.js")))).default;
+
+const now = new Date().toISOString();
+const providerModel = {
+  id: "model-provider", display_name: "Provider Chat", provider_name: "Local Provider",
+  source: "provider", backend: "codex", wire_model: "provider-chat", wire_api: "chat",
+  codex_account_id: "",
+};
+const officialModel = {
+  id: "model-official", display_name: "GPT Official", provider_name: "official-codex",
+  source: "official", backend: "codex", wire_model: "gpt-official", wire_api: "responses",
+  codex_account_id: "account-official",
+};
+const assistant = {
+  id: "assistant-1", name: "小A", model_id: providerModel.id, character: "gugugaga", pet_enabled: true,
+  brain_model_id: providerModel.id, pet_actions: ["walk", "jump", "happy", "angry"], pet_identity: "", pet_principles: "",
+  pet_idle_minutes: 10, pet_quiet_enabled: false, pet_quiet_start: 23, pet_quiet_end: 8, pet_hourly_limit: 6,
+  pet_free_daily_turns: 3, pet_free_used: 0, pet_task_id: "task_pet",
+  pet_allow_view_tasks: true, pet_allow_create_tasks: false, pet_allow_create_workspaces: false,
+  pet_allow_message_tasks: false, pet_allow_manage_models: false, pet_allow_manage_knowledge: false,
+  pet_allow_manage_channels: false, pet_allow_manage_system: false, created_at: now, updated_at: now,
+};
+const readback = (source, model, label) => ({
+  assistant: {...assistant, model_id: model.id, brain_model_id: model.id},
+  messages: [], models: [providerModel], model_name: model.display_name, brain_model_name: model.display_name,
+  pet_model_id: model.id, pet_model_source: source, pet_wire_model: model.wire_model,
+  pet_codex_account_id: model.codex_account_id || "", pet_model_name: model.display_name,
+  pet_account_name: source === "official" ? "Plus Account" : "",
+  pet_model_label: label, pet_model_available: true, pet_model_unavailable_reason: "",
+  pet_workspace_id: "workspace-pet", tools: [],
+});
+let current = readback("provider", providerModel, "Provider 模型 · Local Provider / Provider Chat");
+let savedBody = null;
+
+const bootstrap = `
+const json=(v,s=200)=>new Response(JSON.stringify(v),{status:s,headers:{"Content-Type":"application/json"}});
+const providerModel=${JSON.stringify(providerModel)};
+const officialModel=${JSON.stringify(officialModel)};
+let current=${JSON.stringify(current)};
+window.__savedBody=null;
+window.fetch=async(input,init={})=>{
+ const u=new URL(typeof input==="string"?input:input.url,location.origin); const path=u.pathname; const method=(init.method||"GET").toUpperCase();
+ if(path==="/api/v1/auth/status")return json({authenticated:true,csrf_token:"csrf",owner:{id:"owner-1",username:"owner"}});
+ if(path==="/api/v1/settings/general")return json({ok:true,general:{language:"zh-CN"}});
+ if(path==="/api/v1/assistant"&&method==="GET")return json(current);
+ if(path==="/api/v1/assistant/hints")return json({hints:[]});
+ if(path==="/api/v1/pet"&&method==="GET")return json({state:{id:"pet",mood:"neutral",action:"idle",zone:"desk",character:"gugugaga"}});
+ if(path==="/api/v1/pet/activity")return json({events:[]});
+ if(path==="/api/v1/pet/approvals/pending-count")return json({ok:true,pending:0});
+ if(path==="/api/v1/pet/workbench")return json({core:[],worklog:[],approvals:[],inspection:{health:"ok",checked_at:new Date().toISOString(),pending_approvals:0},channels:[],channel_total:0});
+ if(path==="/api/v1/models")return json({models:[providerModel,officialModel]});
+ if(path==="/api/v1/codex-accounts")return json({accounts:[{id:"account-official",label:"Plus Account",status:"ready",credential_configured:true,available_models:[{wire_model:"gpt-official",display_name:"GPT Official"}]}]});
+ if(path==="/api/v1/workspaces")return json({workspaces:[{id:"workspace-pet",name:"pet-workspace",locality:"local",transport:"native",health:"ready",capabilities:{claude:{status:"ready",auth_status:"logged_in",models:[{wire_model:"claude-sonnet",display_name:"Claude Sonnet"}]}}}]});
+ if(path==="/api/v1/pet/settings"&&method==="PUT"){
+  const body=JSON.parse(init.body||"{}");
+  window.__savedBody=body;
+  if(body.pet_model_source==="official"){
+   current=${JSON.stringify(readback("official", officialModel, "Codex 官方账号 · Plus Account / GPT Official"))};
+   return json({ok:true,...current});
+  }
+  current=${JSON.stringify(readback("provider", providerModel, "Provider 模型 · Local Provider / Provider Chat"))};
+  return json({ok:true,...current});
+ }
+ if(path==="/api/v1/tasks")return json({tasks:[],has_more:false});
+ if(path==="/api/v1/projects")return json({projects:[],has_more:false});
+ if(path==="/api/v1/skills")return json({skills:[]});
+ if(path==="/api/v1/events")return json({events:[],cursor:0});
+ if(path.startsWith("/api/v1/"))return json({});
+ return json({});
+};
+window.EventSource=class extends EventTarget{constructor(){super();}close(){}};
+window.confirm=()=>true;
+`;
+const html = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><title>AHA2</title><link rel="stylesheet" href="/styles.css"></head><body><main id="app"></main><script src="/fixture.js"></script><script type="module" src="/app.js"></script></body></html>`;
+const mime = {".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png", ".html": "text/html"};
+const server = createServer(async (req, res) => {
+  const name = new URL(req.url, "http://localhost").pathname;
+  try {
+    if (name === "/" || name === "/index.html") {
+      res.setHeader("Content-Type", "text/html");
+      res.end(html);
+      return;
+    }
+    if (name === "/fixture.js") {
+      res.setHeader("Content-Type", "text/javascript");
+      res.end(bootstrap);
+      return;
+    }
+    const data = await readFile(resolve(root, "web/build", name.slice(1)));
+    res.setHeader("Content-Type", mime[extname(name)] || "application/octet-stream");
+    res.end(data);
+  } catch {
+    res.writeHead(404).end();
+  }
+});
+await new Promise(r => server.listen(0, "127.0.0.1", r));
+const url = `http://127.0.0.1:${server.address().port}`;
+let browser;
+try {
+  browser = await chromium.launch({executablePath: await executable.executablePath(), args: await executable.args || [], headless: true});
+  const page = await browser.newPage({viewport: {width: 1400, height: 1000}, locale: "zh-CN"});
+  const errors = [];
+  page.on("pageerror", error => errors.push(error.message));
+  page.on("console", message => { if (message.type() === "error") errors.push("console: " + message.text()); });
+  await page.goto(url, {waitUntil: "networkidle"});
+  await page.evaluate(() => window.dispatchEvent(new CustomEvent("aha:open-pet-settings")));
+  await page.waitForSelector("#pet-chat-model", {timeout: 15000});
+
+  // The panel-level chooser is gone; the settings chat field is readonly.
+  assert.equal(await page.locator("#assistant-model").count(), 0, "panel still has a model chooser");
+  assert.equal(await page.locator("#pet-chat-model").getAttribute("readonly"), "", "chat model is editable");
+  assert.equal(await page.locator("#pet-chat-model").inputValue(), "Provider 模型 · Local Provider / Provider Chat");
+
+  // Pick the Codex official account as the only edit entry.
+  await page.selectOption("#pet-model-source", "official");
+  try {
+    await page.waitForFunction(() => document.querySelector("#pet-model-model")?.value === "gpt-official", null, {timeout: 5000});
+  } catch (error) {
+    const picker = await page.evaluate(() => ({
+      sourceOptions: [...document.querySelectorAll("#pet-model-source option")].map(o => ({value: o.value, disabled: o.disabled, text: o.textContent})),
+      modelOptions: [...document.querySelectorAll("#pet-model-model option")].map(o => ({value: o.value, disabled: o.disabled, text: o.textContent})),
+      help: document.querySelector("#pet-model-help")?.textContent,
+      modelHTML: document.querySelector("#pet-model-model")?.outerHTML,
+      hostHTML: document.querySelector("#pet-model-source-host")?.innerHTML,
+    }));
+    throw new Error(error.message + " " + JSON.stringify(picker));
+  }
+  await page.locator("#pet-settings-save").click();
+  try {
+    await page.waitForFunction(() => window.__savedBody !== null, null, {timeout: 5000});
+  } catch (error) {
+    const diagnostics = await page.evaluate(() => ({
+      saved: window.__savedBody,
+      error: document.querySelector("#pet-settings-error")?.textContent,
+      hidden: document.querySelector("#pet-settings-error")?.hidden,
+      saveDisabled: document.querySelector("#pet-settings-save")?.disabled,
+      source: document.querySelector("#pet-model-source")?.value,
+      model: document.querySelector("#pet-model-model")?.value,
+    }));
+    errors.push(JSON.stringify(diagnostics));
+    throw new Error(error.message + " " + JSON.stringify(diagnostics));
+  }
+
+  const body = await page.evaluate(() => window.__savedBody);
+  assert.equal(Object.hasOwn(body, "chat_model_id"), false, "save still sent chat_model_id");
+  assert.equal(body.pet_model_source, "official");
+  assert.equal(body.pet_wire_model, "gpt-official");
+  assert.equal(body.pet_codex_account_id, "account-official");
+
+  // After save both surfaces show the same unified model.
+  await page.waitForFunction(() => document.querySelector("#pet-chat-model")?.value === "Codex 官方账号 · Plus Account / GPT Official");
+  assert.equal(await page.locator("#pet-chat-model").inputValue(), "Codex 官方账号 · Plus Account / GPT Official");
+  assert.equal(await page.locator("#pet-model-source").inputValue(), "official");
+  assert.equal(await page.locator("#pet-model-model").inputValue(), "gpt-official");
+  assert.equal(await page.locator("#pet-chat-model").getAttribute("readonly"), "");
+  assert.deepEqual(errors, [], "page errors");
+  console.log("pet model readonly browser check passed");
+} finally {
+  if (browser) await browser.close();
+  server.close();
+}
~~~~
