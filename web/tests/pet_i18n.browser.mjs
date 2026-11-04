import assert from "node:assert/strict";
import {createServer} from "node:http";
import {readFile} from "node:fs/promises";
import {resolve, extname} from "node:path";
import {pathToFileURL} from "node:url";
const root = resolve(import.meta.dirname, "../..");
const tools = resolve(root, ".tools/web-validation");
const {chromium} = await import(pathToFileURL(resolve(tools, "node_modules/playwright/index.mjs")));
const {default: binary} = await import(pathToFileURL(resolve(tools, "node_modules/@sparticuz/chromium/build/index.js")));
const now = new Date().toISOString();
const assistant = {
  id: "assistant-1", name: "AHA", model_id: "model-1", character: "gugugaga", pet_enabled: true,
  brain_model_id: "model-1", pet_actions: ["walk","jump","happy","angry"], pet_identity: "", pet_principles: "",
  pet_idle_minutes: 10, pet_quiet_enabled: false, pet_quiet_start: 23, pet_quiet_end: 8, pet_hourly_limit: 6,
  pet_task_id: "pet-task", pet_allow_view_tasks: true, pet_allow_create_tasks: true, pet_allow_create_workspaces: false,
  pet_allow_message_tasks: true, pet_allow_manage_models: false, pet_allow_manage_knowledge: false,
  pet_allow_manage_channels: false, pet_allow_manage_system: false, created_at: now, updated_at: now,
};
const approvals = [
  {id:"approval-1", task_id:"pet-task", kind:"delete_task", title:"删除 Task「示例任务」", summary:"宠物请求删除这个 Task，需要主人确认。", reason:"重复任务不再需要", risk:"high", status:"pending", created_at:now},
  {id:"approval-2", task_id:"pet-task", kind:"permission_change", title:"开启宠物权限：管理渠道", summary:"宠物申请开启权限「管理渠道」。\n原因：需要巡检渠道。", risk:"medium", status:"pending", created_at:now},
  {id:"idea-1", task_id:"pet-task", kind:"idea", title:"自动整理渠道联系人", summary:"想法说明\n\n计划：\n先巡检。\n\n预期收益：\n减少重复。", risk:"low", status:"pending", created_at:now},
];
const bootstrap = `
const json=(v,s=200)=>new Response(JSON.stringify(v),{status:s,headers:{"Content-Type":"application/json"}});
const assistant=${JSON.stringify(assistant)};
const approvals=${JSON.stringify(approvals)};
window.fetch=async(input,init={})=>{
 const u=new URL(typeof input==="string"?input:input.url,location.origin); const path=u.pathname; const method=(init.method||"GET").toUpperCase();
 if(path==="/api/v1/auth/status")return json({authenticated:true,csrf_token:"csrf",owner:{id:"owner-1",username:"owner"}});
 if(path==="/api/v1/settings/general")return json({ok:true,general:{language:"en-US"}});
 if(path==="/api/v1/assistant"&&method==="GET")return json({assistant,messages:[],models:[{id:"model-1",display_name:"Model",wire_api:"chat"}],model_name:"Model",brain_model_name:"Model",tools:[],pet_last_brain_at:new Date(Date.now()-600000).toISOString(),pet_next_brain_at:new Date(Date.now()+600000).toISOString(),pet_brain_runs:2});
 if(path==="/api/v1/assistant/hints")return json({hints:[]});
 if(path==="/api/v1/pet")return json({state:{id:"pet",mood:"neutral",action:"idle",zone:"desk"}});
 if(path==="/api/v1/pet/activity")return json({events:[]});
 if(path==="/api/v1/pet/diary")return json({entries:[]});
 if(path==="/api/v1/pet/workbench")return json({diary:[{date:"2026-10-31",content:"Diary content stays user-authored."}],core:[],worklog:[],approvals,updated_at:new Date().toISOString()});
 if(path==="/api/v1/tasks")return json({tasks:[],has_more:false});
 if(path==="/api/v1/projects")return json({projects:[],has_more:false});
 if(path==="/api/v1/workspaces")return json({workspaces:[]});
 if(path==="/api/v1/models")return json({models:[]});
 if(path==="/api/v1/skills")return json({skills:[]});
 if(path==="/api/v1/codex/accounts")return json({accounts:[]});
 if(path==="/api/v1/events")return json({events:[],cursor:0});
 if(path.startsWith("/api/v1/"))return json({});
 return json({});
};
window.EventSource=class{constructor(){}close(){}addEventListener(){}};
window.confirm=()=>true;
sessionStorage.setItem("aha2.navigation", JSON.stringify({view:"pet-diary"}));
`;
const html=`<!doctype html><html lang="en-US"><head><meta charset="utf-8"><title>AHA2</title><link rel="stylesheet" href="/styles.css"></head><body><main id="app"></main><script src="/fixture.js"></script><script type="module" src="/app.js"></script></body></html>`;
const mime={".js":"text/javascript",".css":"text/css",".svg":"image/svg+xml",".png":"image/png",".html":"text/html"};
const server=createServer(async(req,res)=>{const name=new URL(req.url,"http://localhost").pathname;try{if(name==="/"||name==="/index.html"){res.setHeader("Content-Type","text/html");res.end(html);return;}if(name==="/fixture.js"){res.setHeader("Content-Type","text/javascript");res.end(bootstrap);return;}const data=await readFile(resolve(root,"web/dist",name.slice(1)));res.setHeader("Content-Type",mime[extname(name)]||"application/octet-stream");res.end(data);}catch{res.writeHead(404).end();}});
await new Promise(r=>server.listen(0,"127.0.0.1",r));
const url=`http://127.0.0.1:${server.address().port}`;
let browser;
try{
 browser=await chromium.launch({executablePath:await binary.executablePath(),args:binary.args,headless:true});
 const page=await browser.newPage({viewport:{width:1400,height:1000},locale:"en-US"});
 const errors=[];page.on("pageerror",e=>errors.push(e.message));
 await page.goto(url,{waitUntil:"networkidle"});
 await page.waitForSelector("[data-pet-workbench-tab]",{timeout:15000});
 const scan=async(label)=>{
  const missing=await page.evaluate(()=>{
   const roots=[document.querySelector("#app"),document.querySelector("#assistant-layer")].filter(Boolean);
   const skip=[".markdown-body",".message-text",".message-bubble","pre","code","textarea","input","[contenteditable='true']",".pet-diary-entry pre",".pet-approval-card strong",".pet-approval-card p",".pet-approval-card small","[data-i18n-ignore]"].join(",");
   const out=[];
   for(const root of roots){
    const walker=document.createTreeWalker(root,NodeFilter.SHOW_TEXT);
    while(walker.nextNode()){const el=walker.currentNode.parentElement;if(!el||el.closest(skip))continue;const t=(walker.currentNode.nodeValue||"").trim();if(t&&/[一-鿿]/.test(t))out.push(t.slice(0,140));}
    for(const el of root.querySelectorAll("[placeholder],[title],[aria-label]")){if(el.closest(skip))continue;for(const name of ["placeholder","title","aria-label"]){const v=el.getAttribute(name);if(v&&/[一-鿿]/.test(v))out.push(name+"="+v.slice(0,140));}}
   }
   return [...new Set(out)];
  });
  assert.deepEqual(missing, [], label + " left untranslated Chinese");
 };
 await scan("pet workbench diary");
 await page.evaluate(()=>document.querySelector('[data-pet-workbench-tab="approvals"]')?.click());await page.waitForTimeout(300);await scan("pet approvals");
 await page.evaluate(()=>document.querySelector('[data-pet-approval-filter="idea"]')?.click());await page.waitForTimeout(200);await scan("pet ideas filter");
 await page.evaluate(()=>document.querySelector('[data-pet-approval-status-filter="pending"]')?.click());await page.waitForTimeout(200);await scan("pet approval status filter");
 await page.evaluate(()=>document.querySelector('[data-pet-workbench-tab="settings"]')?.click());await page.waitForTimeout(600);await scan("pet settings");
 await page.evaluate(()=>document.querySelector("#assistant-pet")?.click());await page.waitForTimeout(300);await scan("assistant panel");
 assert.deepEqual(errors,[]);
}finally{await browser?.close();await new Promise(r=>server.close(r));}
console.log("pet i18n browser check passed");
