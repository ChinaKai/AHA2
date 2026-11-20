import assert from "node:assert/strict";
import {createServer} from "node:http";
import {readFile} from "node:fs/promises";
import {resolve, extname} from "node:path";
import {pathToFileURL} from "node:url";

// Real-browser check for the "无形象" character. The page is reloaded between
// character values to reproduce "保存后刷新页面即生效" without restarting the
// service. The fixture serves the same web/build output the Go binary embeds.
const root = resolve(import.meta.dirname, "../..");
const tools = resolve(root, ".tools/web-validation");
const {chromium} = await import(pathToFileURL(process.env.AHA_PLAYWRIGHT_MODULE || resolve(tools, "node_modules/playwright/index.mjs")));
const binary = process.env.AHA_BROWSER_EXECUTABLE
  ? {executablePath: async () => process.env.AHA_BROWSER_EXECUTABLE, args: []}
  : (await import(pathToFileURL(resolve(tools, "node_modules/@sparticuz/chromium/build/index.js")))).default;

let character = "gugugaga";
const now = new Date().toISOString();

function fixture() {
  return `
const nativeFetch=window.fetch.bind(window);
let character=${JSON.stringify(character)};
window.__petStateReads=0;
const json=(value,status=200)=>new Response(JSON.stringify(value),{status,headers:{"Content-Type":"application/json"}});
const assistant={id:"assistant-1",name:"小A",model_id:"model-1",character,pet_enabled:true,brain_model_id:"model-1",
 pet_actions:["walk","jump","happy","angry"],pet_identity:"",pet_principles:"",pet_idle_minutes:10,
 pet_quiet_enabled:false,pet_quiet_start:23,pet_quiet_end:8,pet_hourly_limit:6,pet_task_id:"",
 pet_allow_view_tasks:true,pet_allow_create_tasks:true,pet_allow_create_workspaces:false,
 pet_allow_message_tasks:true,pet_allow_manage_models:false,pet_allow_manage_knowledge:false,
 pet_allow_manage_channels:false,pet_allow_manage_system:false,created_at:${JSON.stringify(now)},updated_at:${JSON.stringify(now)}};
window.fetch=async(input,init={})=>{
 const url=new URL(typeof input==="string"?input:input.url,location.origin);
 const path=url.pathname; const method=(init.method||"GET").toUpperCase();
 if(path==="/api/v1/auth/status")return json({authenticated:true,csrf_token:"csrf",owner:{id:"owner-1",username:"owner"}});
 if(path==="/api/v1/settings/general")return json({ok:true,general:{language:"zh-CN"}});
 if(path==="/api/v1/assistant"&&method==="GET")return json({assistant:{...assistant,character},messages:[],models:[{id:"model-1",display_name:"Model",wire_api:"chat"}],model_name:"Model",brain_model_name:"Model",tools:[]});
 if(path==="/api/v1/assistant"&&method==="PUT"){
  const body=JSON.parse(init.body||"{}");
  if(body.character)character=body.character;
  await nativeFetch("/__character",{method:"PUT",headers:{"Content-Type":"application/json"},body:JSON.stringify({character})});
  return json({ok:true,assistant:{...assistant,character},models:[{id:"model-1",display_name:"Model",wire_api:"chat"}]});
 }
 if(path==="/api/v1/pet"){
  window.__petStateReads++;
  return json({ok:true,state:{id:"owner_pet",character,mood:"neutral",action:"idle",zone:"desk",x:0,y:0,facing:1,sleeping:false,quiet:false,version:1,updated_at:new Date().toISOString()}});
 }
 if(path==="/api/v1/assistant/hints")return json({hints:[]});
 if(path==="/api/v1/events")return json({events:[],cursor:0});
 if(path==="/api/v1/tasks")return json({tasks:[],has_more:false});
 if(path==="/api/v1/projects")return json({projects:[],has_more:false});
 if(path==="/api/v1/workspaces")return json({workspaces:[]});
 if(path==="/api/v1/models")return json({models:[]});
 if(path==="/api/v1/skills")return json({skills:[]});
 if(path==="/api/v1/codex/accounts")return json({accounts:[]});
 if(path.startsWith("/api/v1/"))return json({});
 return json({});
};
window.EventSource=class extends EventTarget{constructor(url){super();this.url=url;}close(){}};
`;
}

const html = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><title>AHA2</title><link rel="stylesheet" href="/styles.css"></head><body><main id="app"></main><script src="/fixture.js"></script><script type="module" src="/app.js"></script></body></html>`;
const mime = {".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png", ".html": "text/html"};
const server = createServer(async (request, response) => {
  const name = new URL(request.url, "http://localhost").pathname;
  if (name === "/__character") {
    if (request.method === "PUT") {
      let body = "";
      for await (const chunk of request) body += chunk;
      const parsed = JSON.parse(body || "{}");
      if (typeof parsed.character === "string" && parsed.character) character = parsed.character;
    }
    response.setHeader("Content-Type", "application/json");
    response.end(JSON.stringify({ok: true, character}));
    return;
  }
  try {
    if (name === "/" || name === "/index.html") {
      response.setHeader("Cache-Control", "no-store");
      response.setHeader("Content-Type", "text/html");
      response.end(html);
      return;
    }
    if (name === "/fixture.js") {
      response.setHeader("Cache-Control", "no-store");
      response.setHeader("Content-Type", "text/javascript");
      response.end(fixture());
      return;
    }
    const data = await readFile(resolve(root, "web/build", name.slice(1)));
    response.setHeader("Content-Type", mime[extname(name)] || "application/octet-stream");
    response.end(data);
  } catch {
    response.writeHead(404).end();
  }
});
await new Promise(resolveListen => server.listen(0, "127.0.0.1", resolveListen));
const url = `http://127.0.0.1:${server.address().port}`;
let browser;
try {
  browser = await chromium.launch({executablePath: await binary.executablePath(), args: binary.args, headless: true});
  const page = await browser.newPage({viewport: {width: 1280, height: 900}, locale: "zh-CN"});
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  const petVisible = () => page.waitForFunction(() => {
    const pet = document.querySelector("#assistant-pet");
    return Boolean(pet) && !pet.hidden && Boolean(pet.querySelector(".assistant-pet-art img"));
  }, {timeout: 15000});
  const petHidden = () => page.waitForFunction(() => {
    const pet = document.querySelector("#assistant-pet");
    return Boolean(pet) && pet.hidden === true && pet.dataset.character === "none";
  }, {timeout: 15000});

  // Default user keeps the current appearance after the change ships.
  await page.goto(url, {waitUntil: "networkidle"});
  await petVisible();
  assert.equal(await page.locator("#assistant-bubble").isVisible(), false);
  assert.equal(await page.locator("[data-assistant-character='none']").count(), 1);
  assert.match(await page.locator("[data-assistant-character='none']").textContent(), /无形象/);

  // Switch to 无形象 through the persisted setting, then refresh.
  character = "none";
  await page.reload({waitUntil: "networkidle"});
  await petHidden();
  assert.equal(await page.locator("#assistant-pet").isVisible(), false);
  assert.equal(await page.locator("#assistant-pet-art img").count(), 0);
  assert.equal(await page.locator("#assistant-thinking").isVisible(), false);
  assert.equal(await page.locator("#assistant-bubble").isVisible(), false);
  // The avatar is hidden, but the pet state endpoint still answers and the
  // assistant layer is still mounted for approvals/panel/settings.
  assert.ok(await page.evaluate(() => window.__petStateReads > 0));
  assert.equal(await page.locator("#assistant-panel").count(), 1);
  // Hiding an absolutely positioned avatar must not collapse the app shell.
  assert.ok((await page.locator("#app").boundingBox()).height > 100);

  // Switch back to an appearance, refresh, and the avatar renders again.
  character = "gugugaga";
  await page.reload({waitUntil: "networkidle"});
  await petVisible();
  assert.equal(await page.locator("#assistant-pet").isVisible(), true);
  assert.ok((await page.locator("#assistant-pet .assistant-pet-art img").count()) >= 1);

  assert.deepEqual(errors, []);
  console.log("pet_character_none.browser: ok");
} finally {
  if (browser) await browser.close();
  server.close();
}
