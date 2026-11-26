import assert from "node:assert/strict";
import {createServer} from "node:http";
import {readFile} from "node:fs/promises";
import {resolve, extname} from "node:path";
import {pathToFileURL} from "node:url";
const root = resolve(import.meta.dirname, "../..");
const tools = resolve(root, ".tools/web-validation");
const {chromium} = await import(pathToFileURL(process.env.AHA_PLAYWRIGHT_MODULE || resolve(tools, "node_modules/playwright/index.mjs")));
const binary = process.env.AHA_BROWSER_EXECUTABLE ? {executablePath: async () => process.env.AHA_BROWSER_EXECUTABLE, args: []} : (await import(pathToFileURL(resolve(tools, "node_modules/@sparticuz/chromium/build/index.js")))).default;
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
const watches = [
  {task_id:"ignored-task", code:"task-801", title:"Ignored task", status:"waiting_user", watched:true, ignored:true, created_at:now, updated_at:now},
  {task_id:"unfollowed-task", code:"task-520", title:"Unfollowed task", status:"waiting_user", watched:false, ignored:false, created_at:now, updated_at:now},
];
const bootstrap = `
const json=(v,s=200)=>new Response(JSON.stringify(v),{status:s,headers:{"Content-Type":"application/json"}});
const assistant=${JSON.stringify(assistant)};
const approvals=${JSON.stringify(approvals)};
const watches=${JSON.stringify(watches)};
window.pendingCount=0;
window.petPendingReads=0;
const nativeSetInterval=window.setInterval.bind(window);
window.setInterval=(callback,ms,...args)=>{
 if(ms===60000){window.pollPetPendingCount=callback;return 98765;}
 return nativeSetInterval(callback,ms,...args);
};
window.fetch=async(input,init={})=>{
 const u=new URL(typeof input==="string"?input:input.url,location.origin); const path=u.pathname; const method=(init.method||"GET").toUpperCase();
 if(path==="/api/v1/auth/status")return json({authenticated:true,csrf_token:"csrf",owner:{id:"owner-1",username:"owner"}});
 if(path==="/api/v1/settings/general")return json({ok:true,general:{language:new URLSearchParams(location.search).get("language")||"en-US"}});
 if(path==="/api/v1/assistant"&&method==="GET")return json({assistant,messages:[],models:[{id:"model-1",display_name:"Model",wire_api:"chat"}],model_name:"Model",brain_model_name:"Model",tools:[],pet_last_brain_at:new Date(Date.now()-600000).toISOString(),pet_next_brain_at:new Date(Date.now()+600000).toISOString(),pet_brain_runs:2});
 if(path==="/api/v1/assistant/hints")return json({hints:[]});
 if(path==="/api/v1/pet")return json({state:{id:"pet",mood:"neutral",action:"idle",zone:"desk"}});
 if(path==="/api/v1/pet/activity")return json({events:[]});
 if(path==="/api/v1/pet/approvals/pending-count"){window.petPendingReads++;return json({ok:true,pending:window.pendingCount});}
 if(path.startsWith("/api/v1/pet/approvals/")&&path.endsWith("/resolve")&&method==="POST"){
  const id=path.split("/")[5]; const item=approvals.find(a=>a.id===id);
  if(item?.status==="pending"){item.status=JSON.parse(init.body).decision;window.pendingCount--;}
  return json({ok:true,approval:item});
 }
 if(path.startsWith("/api/v1/pet/watches/")&&method==="PATCH"){
  const watch=watches.find(w=>path.endsWith("/"+w.task_id));
  watch.ignored=JSON.parse(init.body).ignored;
  return json({ok:true,watch});
 }
 if(path==="/api/v1/pet/workbench")return json({core:[],worklog:[],approvals,watches,updated_at:new Date().toISOString(),inspection:{health:"degraded",version:"9.9.9",web_version:"web-1",uptime_minutes:125,checked_at:new Date().toISOString(),pending_approvals:3},channels:[{id:"c-1",name:"Mail",status:"degraded",pending:2,degraded:true}],channel_total:1});
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
window.EventSource=class extends EventTarget{
 constructor(url){super();if(url==="/api/v1/events")window.petGlobalEvents=this;}
 close(){}
};
window.confirm=()=>true;
sessionStorage.setItem("aha2.navigation", JSON.stringify({view:"pet-diary"}));
`;
const html=`<!doctype html><html lang="en-US"><head><meta charset="utf-8"><title>AHA2</title><link rel="stylesheet" href="/styles.css"></head><body><main id="app"></main><script src="/fixture.js"></script><script type="module" src="/app.js"></script></body></html>`;
const mime={".js":"text/javascript",".css":"text/css",".svg":"image/svg+xml",".png":"image/png",".html":"text/html"};
const server=createServer(async(req,res)=>{const name=new URL(req.url,"http://localhost").pathname;try{if(name==="/"||name==="/index.html"){res.setHeader("Content-Type","text/html");res.end(html);return;}if(name==="/fixture.js"){res.setHeader("Content-Type","text/javascript");res.end(bootstrap);return;}const data=await readFile(resolve(root,"web/build",name.slice(1)));res.setHeader("Content-Type",mime[extname(name)]||"application/octet-stream");res.end(data);}catch{res.writeHead(404).end();}});
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
   const skip=[".markdown-body",".message-text",".message-bubble","pre","code","textarea","input","[contenteditable='true']",".pet-doc-entry pre",".pet-approval-card strong",".pet-approval-row-main strong",".pet-approval-card p",".pet-approval-card small",".pet-overview-activity strong",".pet-watch-row strong","[data-i18n-ignore]"].join(",");
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
 await scan("pet workbench overview");
 assert.equal(await page.locator('[data-pet-workbench-tab="overview"]').getAttribute("class"), "active");
 // The overview no longer carries the inspection snapshot or channel-health
 // cards below Recent activity, even though the response contains both.
 const workbenchBody = page.locator("#pet-workbench-body");
 assert.equal(await workbenchBody.locator(".pet-overview-inspection").count(), 0);
 assert.equal(await workbenchBody.locator(".pet-overview-channels").count(), 0);
 assert.equal(await workbenchBody.locator(".pet-inspection-summary").count(), 0);
 assert.equal(await workbenchBody.locator(".pet-channel-card").count(), 0);
 assert.doesNotMatch(await workbenchBody.innerText(), /Inspection snapshot|Channel health/);
 // The status row, numeric cards, watch lists and recent activity still render.
 assert.equal(await workbenchBody.locator(".pet-overview-status").count(), 1);
 assert.equal(await workbenchBody.locator(".pet-overview-cards").count(), 1);
 assert.equal(await workbenchBody.locator(".pet-overview-follows").count(), 1);
 assert.equal(await workbenchBody.locator(".pet-overview-recent").count(), 1);
 assert.match(await workbenchBody.locator(".pet-overview-status").innerText(), /Version: 9\.9\.9/);
 assert.match(await workbenchBody.locator(".pet-overview-status").innerText(), /Uptime: 2 h 5 min/);
 // Recent activity is the last overview section: no hollow grid row or spare
 // gap is left behind at either viewport width.
 const assertOverviewLayout = async label => {
  const layout = await page.evaluate(() => {
   const overview = document.querySelector("#pet-workbench-body .pet-overview");
   const recent = document.querySelector("#pet-workbench-body .pet-overview-recent");
   return {
    sections: [...overview.children].map(el => ({className: el.className, width: Math.round(el.getBoundingClientRect().width), height: Math.round(el.getBoundingClientRect().height)})),
    overviewBottom: Math.round(overview.getBoundingClientRect().bottom),
    recentBottom: Math.round(recent.getBoundingClientRect().bottom),
   };
  });
  assert.deepEqual(layout.sections.map(section => section.className), ["pet-overview-status", "pet-overview-cards", "pet-overview-follows pet-follow-list", "pet-overview-other pet-follow-list", "pet-overview-other pet-follow-list", "pet-overview-recent"], label);
  assert.ok(layout.sections.every(section => section.width > 0 && section.height > 0), label + " has an empty section");
  assert.equal(layout.recentBottom, layout.overviewBottom, label + " leaves space below Recent activity");
 };
 await assertOverviewLayout("desktop overview");
 await page.setViewportSize({width:390,height:844});
 await assertOverviewLayout("mobile overview");
 await page.setViewportSize({width:1400,height:1000});
 const ignored = workbenchBody.locator("#pet-overview-ignored");
 const unfollowed = workbenchBody.locator("#pet-overview-unfollowed");
 const task520 = '[data-pet-watch-task="unfollowed-task"]';
 // The follow lists are always open (no summary to click): the buttons are
 // usable right away and the titles count the rows.
 assert.equal(await ignored.locator(".pet-follow-title").innerText(), "Ignored tasks (1)");
 assert.equal(await ignored.locator(task520).count(), 0);
 await unfollowed.locator(task520 + '[data-pet-watch-ignore="true"]').click();
 await ignored.locator(task520 + '[data-pet-watch-ignore="false"]').waitFor({state:"attached"});
 assert.equal(await ignored.locator(".pet-follow-title").innerText(), "Ignored tasks (2)");
 await ignored.locator(task520 + '[data-pet-watch-ignore="false"]').click();
 await unfollowed.locator(task520 + '[data-pet-watch-ignore="true"]').waitFor({state:"attached"});
 assert.equal(await ignored.locator(".pet-follow-title").innerText(), "Ignored tasks (1)");
 assert.equal(await page.locator('[data-pet-workbench-tab="diary"]').count(), 0);
 assert.doesNotMatch(await page.locator("#app").innerText(), /日记|diary/i);
 const nav = page.locator('button[data-view="pet-workbench"]');
 assert.equal(await nav.count(), 2, "desktop sidebar and mobile bottom navigation");
 const setCount=async value=>{
  await page.evaluate(value=>{window.pendingCount=value;window.pollPetPendingCount();},value);
  await page.waitForFunction(value=>{
   const buttons=[...document.querySelectorAll('button[data-view="pet-workbench"]')];
   return buttons.length===2&&buttons.every(b=>value===0?!b.classList.contains("has-pending")&&!b.querySelector(".task-tool-badge"):b.querySelector(".task-tool-badge")?.textContent===(value>99?"99+":String(value)));
  },value);
 };
 await setCount(0);
 for(const value of [7,105]){
  await setCount(value);
  for(const button of await nav.all()){
   assert.match(await button.getAttribute("title"), new RegExp(value+" pending approvals"));
   assert.match(await button.getAttribute("aria-label"), new RegExp(value+" pending approvals"));
  }
 }
 // The workbench tab badge went with the approvals tab (task-813); only the
 // navigation badge counts pending approvals.
 assert.equal(await page.locator("#pet-approval-count").count(),0);
 await setCount(3);
 // Exercise the real global SSE listeners away from the workbench: neither
 // a page reload nor the disabled interval can refresh the badge for this test.
 await page.locator('.sidebar [data-view="projects"]').click();
 await page.waitForSelector(".pet-workbench-page",{state:"detached"});
 const eventCount=async(pending,eventType)=>{
  const previousReads=await page.evaluate(()=>window.petPendingReads);
  await page.evaluate(({pending,eventType})=>{
   window.pendingCount=pending;
   const event=eventType==="pet_approval"
    ?new MessageEvent("update",{data:JSON.stringify({type:"pet_approval",data:{status:pending===4?"pending":"approved"}})})
    :new Event("open");
   window.petGlobalEvents.dispatchEvent(event);
  },{pending,eventType});
  await page.waitForFunction(({pending,previousReads})=>{
   const buttons=[...document.querySelectorAll('button[data-view="pet-workbench"]')];
   return window.petPendingReads>previousReads&&buttons.length===2&&buttons.every(button=>button.querySelector(".task-tool-badge")?.textContent===String(pending));
  },{pending,previousReads},{timeout:3000});
 };
 await eventCount(4,"pet_approval"); // New approval: 3 -> 4.
 await eventCount(3,"pet_approval"); // Resolved elsewhere: 4 -> 3.
 await eventCount(5,"open"); // Reconnect catches up to missed approvals.
 await page.locator('.sidebar [data-view="pet-workbench"]').click();
 await page.waitForSelector('[data-pet-workbench-tab="overview"].active');
 assert.equal(await nav.first().locator(".task-tool-badge").innerText(),"5");
 await setCount(3);
 await page.locator('[data-pet-workbench-tab="records"]').click();
 await page.waitForSelector('#pet-record-approvals [data-pet-approval-open="approval-1"]');
 for(const [id,decision,want] of [["approval-1","approve",2],["approval-2","reject",1],["idea-1","cancel",0]]){
  await page.locator('[data-pet-approval-open="'+id+'"]').click();
  await page.waitForSelector("dialog.pet-approval-detail-dialog[open]");
  const handle=async dialog=>dialog.accept("test reason");
  if(decision==="reject")page.once("dialog",handle);
  await page.locator('dialog.pet-approval-detail-dialog [data-pet-approval-decision="'+decision+'"]').click();
  await page.waitForFunction(want=>{const badge=document.querySelector('.sidebar [data-view="pet-workbench"] .task-tool-badge');return want?badge?.textContent===String(want):!badge;},want);
  if(decision==="reject")page.removeListener("dialog",handle);
  await page.waitForSelector("dialog.pet-approval-detail-dialog",{state:"detached"});
  assert.equal(await nav.locator(".task-tool-badge").count(),want?2:0);
 }
 await page.setViewportSize({width:390,height:844});
 await setCount(105);
 assert.equal(await page.locator('.bottom-nav [data-view="pet-workbench"] .task-tool-badge').isVisible(),true);
 await page.setViewportSize({width:1400,height:1000});
 await page.goto(url+"/?legacy=1#pet-diary",{waitUntil:"networkidle"});
 assert.equal(new URL(page.url()).hash,"#pet-workbench");
 await page.waitForSelector('[data-pet-workbench-tab="overview"].active');
 assert.equal(await page.locator('[data-pet-workbench-tab="approvals"]').count(),0,"no approvals tab");
 await page.evaluate(()=>document.querySelector('[data-pet-workbench-tab="records"]')?.click());await page.waitForSelector("#pet-record-approvals [data-pet-approval-filters-open]");await page.waitForTimeout(300);await scan("pet approval history");
 await page.locator("[data-pet-approval-filters-open]").click();
 await page.waitForSelector("dialog.pet-approval-filter-dialog[open]");
 await page.evaluate(()=>document.querySelector('[data-pet-approval-filter="idea"]')?.click());await page.waitForTimeout(200);await scan("pet ideas filter");
 await page.evaluate(()=>document.querySelector('[data-pet-approval-status-filter="pending"]')?.click());await page.waitForTimeout(200);await scan("pet approval status filter");
 await page.locator("dialog.pet-approval-filter-dialog [data-pet-approval-filters-close]").click();
 await page.waitForSelector("dialog.pet-approval-filter-dialog",{state:"detached"});
 await page.evaluate(()=>document.querySelector('[data-pet-workbench-tab="settings"]')?.click());await page.waitForTimeout(600);await scan("pet settings");
 await page.evaluate(()=>document.querySelector("#assistant-pet")?.click());await page.waitForTimeout(300);await scan("assistant panel");
 await page.goto(url+"/?language=zh-CN#pet-workbench",{waitUntil:"networkidle"});
 await page.waitForSelector('[data-pet-workbench-tab="overview"].active');
 await setCount(7);
 for(const button of await nav.all()){
  assert.match(await button.getAttribute("title"),/7 条待审批/);
  assert.match(await button.getAttribute("aria-label"),/7 条待审批/);
 }
 assert.doesNotMatch(await page.locator("#app").innerText(), /日记|diary/i);
 assert.deepEqual(errors,[]);
}finally{await browser?.close();await new Promise(r=>server.close(r));}
console.log("pet i18n browser check passed");
