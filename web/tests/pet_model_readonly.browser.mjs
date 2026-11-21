// Browser-level check for the unified pet model settings:
// - the chat model field is a read-only mirror, not a chooser;
// - saving never sends an independent chat_model_id;
// - after save the read-only field and the source picker show the same model.
import assert from "node:assert/strict";
import {createServer} from "node:http";
import {readFile} from "node:fs/promises";
import {resolve, extname} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "../..");
const playwrightModule = process.env.AHA_PLAYWRIGHT_MODULE;
const {chromium} = await import(pathToFileURL(playwrightModule || resolve(root, ".tools/web-validation/node_modules/playwright/index.mjs")).href);
const executable = process.env.AHA_BROWSER_EXECUTABLE
  ? {executablePath: async () => process.env.AHA_BROWSER_EXECUTABLE, args: []}
  : (await import(pathToFileURL(resolve(root, ".tools/web-validation/node_modules/@sparticuz/chromium/build/index.js")))).default;

const now = new Date().toISOString();
const providerModel = {
  id: "model-provider", display_name: "Provider Chat", provider_name: "Local Provider",
  source: "provider", backend: "codex", wire_model: "provider-chat", wire_api: "chat",
  codex_account_id: "",
};
const officialModel = {
  id: "model-official", display_name: "GPT Official", provider_name: "official-codex",
  source: "official", backend: "codex", wire_model: "gpt-official", wire_api: "responses",
  codex_account_id: "account-official",
};
const assistant = {
  id: "assistant-1", name: "小A", model_id: providerModel.id, character: "gugugaga", pet_enabled: true,
  brain_model_id: providerModel.id, pet_actions: ["walk", "jump", "happy", "angry"], pet_identity: "", pet_principles: "",
  pet_idle_minutes: 10, pet_quiet_enabled: false, pet_quiet_start: 23, pet_quiet_end: 8, pet_hourly_limit: 6,
  pet_free_daily_turns: 3, pet_free_used: 0, pet_task_id: "task_pet",
  pet_allow_view_tasks: true, pet_allow_create_tasks: false, pet_allow_create_workspaces: false,
  pet_allow_message_tasks: false, pet_allow_manage_models: false, pet_allow_manage_knowledge: false,
  pet_allow_manage_channels: false, pet_allow_manage_system: false, created_at: now, updated_at: now,
};
const readback = (source, model, label) => ({
  assistant: {...assistant, model_id: model.id, brain_model_id: model.id},
  messages: [], models: [providerModel], model_name: model.display_name, brain_model_name: model.display_name,
  pet_model_id: model.id, pet_model_source: source, pet_wire_model: model.wire_model,
  pet_codex_account_id: model.codex_account_id || "", pet_model_name: model.display_name,
  pet_account_name: source === "official" ? "Plus Account" : "",
  pet_model_label: label, pet_model_available: true, pet_model_unavailable_reason: "",
  pet_workspace_id: "workspace-pet", tools: [],
});
let current = readback("provider", providerModel, "Provider 模型 · Local Provider / Provider Chat");
let savedBody = null;

const bootstrap = `
const json=(v,s=200)=>new Response(JSON.stringify(v),{status:s,headers:{"Content-Type":"application/json"}});
const providerModel=${JSON.stringify(providerModel)};
const officialModel=${JSON.stringify(officialModel)};
let current=${JSON.stringify(current)};
window.__savedBody=null;
window.fetch=async(input,init={})=>{
 const u=new URL(typeof input==="string"?input:input.url,location.origin); const path=u.pathname; const method=(init.method||"GET").toUpperCase();
 if(path==="/api/v1/auth/status")return json({authenticated:true,csrf_token:"csrf",owner:{id:"owner-1",username:"owner"}});
 if(path==="/api/v1/settings/general")return json({ok:true,general:{language:"zh-CN"}});
 if(path==="/api/v1/assistant"&&method==="GET")return json(current);
 if(path==="/api/v1/assistant/hints")return json({hints:[]});
 if(path==="/api/v1/pet"&&method==="GET")return json({state:{id:"pet",mood:"neutral",action:"idle",zone:"desk",character:"gugugaga"}});
 if(path==="/api/v1/pet/activity")return json({events:[]});
 if(path==="/api/v1/pet/approvals/pending-count")return json({ok:true,pending:0});
 if(path==="/api/v1/pet/workbench")return json({core:[],worklog:[],approvals:[],inspection:{health:"ok",checked_at:new Date().toISOString(),pending_approvals:0},channels:[],channel_total:0});
 if(path==="/api/v1/models")return json({models:[providerModel,officialModel]});
 if(path==="/api/v1/codex-accounts")return json({accounts:[{id:"account-official",label:"Plus Account",status:"ready",credential_configured:true,available_models:[{wire_model:"gpt-official",display_name:"GPT Official"}]}]});
 if(path==="/api/v1/workspaces")return json({workspaces:[{id:"workspace-pet",name:"pet-workspace",locality:"local",transport:"native",health:"ready",capabilities:{claude:{status:"ready",auth_status:"logged_in",models:[{wire_model:"claude-sonnet",display_name:"Claude Sonnet"}]}}}]});
 if(path==="/api/v1/pet/settings"&&method==="PUT"){
  const body=JSON.parse(init.body||"{}");
  window.__savedBody=body;
  if(body.pet_model_source==="official"){
   current=${JSON.stringify(readback("official", officialModel, "Codex 官方账号 · Plus Account / GPT Official"))};
   return json({ok:true,...current});
  }
  current=${JSON.stringify(readback("provider", providerModel, "Provider 模型 · Local Provider / Provider Chat"))};
  return json({ok:true,...current});
 }
 if(path==="/api/v1/tasks")return json({tasks:[],has_more:false});
 if(path==="/api/v1/projects")return json({projects:[],has_more:false});
 if(path==="/api/v1/skills")return json({skills:[]});
 if(path==="/api/v1/events")return json({events:[],cursor:0});
 if(path.startsWith("/api/v1/"))return json({});
 return json({});
};
window.EventSource=class extends EventTarget{constructor(){super();}close(){}};
window.confirm=()=>true;
`;
const html = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><title>AHA2</title><link rel="stylesheet" href="/styles.css"></head><body><main id="app"></main><script src="/fixture.js"></script><script type="module" src="/app.js"></script></body></html>`;
const mime = {".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png", ".html": "text/html"};
const server = createServer(async (req, res) => {
  const name = new URL(req.url, "http://localhost").pathname;
  try {
    if (name === "/" || name === "/index.html") {
      res.setHeader("Content-Type", "text/html");
      res.end(html);
      return;
    }
    if (name === "/fixture.js") {
      res.setHeader("Content-Type", "text/javascript");
      res.end(bootstrap);
      return;
    }
    const data = await readFile(resolve(root, "web/build", name.slice(1)));
    res.setHeader("Content-Type", mime[extname(name)] || "application/octet-stream");
    res.end(data);
  } catch {
    res.writeHead(404).end();
  }
});
await new Promise(r => server.listen(0, "127.0.0.1", r));
const url = `http://127.0.0.1:${server.address().port}`;
let browser;
try {
  browser = await chromium.launch({executablePath: await executable.executablePath(), args: await executable.args || [], headless: true});
  const page = await browser.newPage({viewport: {width: 1400, height: 1000}, locale: "zh-CN"});
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  page.on("console", message => { if (message.type() === "error") errors.push("console: " + message.text()); });
  await page.goto(url, {waitUntil: "networkidle"});
  await page.evaluate(() => window.dispatchEvent(new CustomEvent("aha:open-pet-settings")));
  await page.waitForSelector("#pet-chat-model", {timeout: 15000});

  // The panel-level chooser is gone; the settings chat field is readonly.
  assert.equal(await page.locator("#assistant-model").count(), 0, "panel still has a model chooser");
  assert.equal(await page.locator("#pet-chat-model").getAttribute("readonly"), "", "chat model is editable");
  assert.equal(await page.locator("#pet-chat-model").inputValue(), "Provider 模型 · Local Provider / Provider Chat");

  // Pick the Codex official account as the only edit entry.
  await page.selectOption("#pet-model-source", "official");
  try {
    await page.waitForFunction(() => document.querySelector("#pet-model-model")?.value === "gpt-official", null, {timeout: 5000});
  } catch (error) {
    const picker = await page.evaluate(() => ({
      sourceOptions: [...document.querySelectorAll("#pet-model-source option")].map(o => ({value: o.value, disabled: o.disabled, text: o.textContent})),
      modelOptions: [...document.querySelectorAll("#pet-model-model option")].map(o => ({value: o.value, disabled: o.disabled, text: o.textContent})),
      help: document.querySelector("#pet-model-help")?.textContent,
      modelHTML: document.querySelector("#pet-model-model")?.outerHTML,
      hostHTML: document.querySelector("#pet-model-source-host")?.innerHTML,
    }));
    throw new Error(error.message + " " + JSON.stringify(picker));
  }
  await page.locator("#pet-settings-save").click();
  try {
    await page.waitForFunction(() => window.__savedBody !== null, null, {timeout: 5000});
  } catch (error) {
    const diagnostics = await page.evaluate(() => ({
      saved: window.__savedBody,
      error: document.querySelector("#pet-settings-error")?.textContent,
      hidden: document.querySelector("#pet-settings-error")?.hidden,
      saveDisabled: document.querySelector("#pet-settings-save")?.disabled,
      source: document.querySelector("#pet-model-source")?.value,
      model: document.querySelector("#pet-model-model")?.value,
    }));
    errors.push(JSON.stringify(diagnostics));
    throw new Error(error.message + " " + JSON.stringify(diagnostics));
  }

  const body = await page.evaluate(() => window.__savedBody);
  assert.equal(Object.hasOwn(body, "chat_model_id"), false, "save still sent chat_model_id");
  assert.equal(body.pet_model_source, "official");
  assert.equal(body.pet_wire_model, "gpt-official");
  assert.equal(body.pet_codex_account_id, "account-official");

  // After save both surfaces show the same unified model.
  await page.waitForFunction(() => document.querySelector("#pet-chat-model")?.value === "Codex 官方账号 · Plus Account / GPT Official");
  assert.equal(await page.locator("#pet-chat-model").inputValue(), "Codex 官方账号 · Plus Account / GPT Official");
  assert.equal(await page.locator("#pet-model-source").inputValue(), "official");
  assert.equal(await page.locator("#pet-model-model").inputValue(), "gpt-official");
  assert.equal(await page.locator("#pet-chat-model").getAttribute("readonly"), "");
  assert.deepEqual(errors, [], "page errors");
  console.log("pet model readonly browser check passed");
} finally {
  if (browser) await browser.close();
  server.close();
}
