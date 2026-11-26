// Browser verification for the pet desk overview (task-813, design doc
// section 10, items 1, 4, 5, 10, 12, 13 and 15).
//
// Checks against the real built Web bundle with a stubbed API:
//   - with `desk` the overview renders the desk blocks in order, the waiting
//     and running columns sit side by side on a wide screen and stack on a
//     phone, and the English UI shows no Chinese interface copy;
//   - a task row opens the Task, an approval row and an idea open the existing
//     approval detail (also when the approval is older than the loaded page),
//     a channel row jumps to the channel cards and "see all" opens Records;
//   - the desk buttons still work after the body is repainted;
//   - without `desk` the overview falls back to the pre-desk layout.
//
// Dependencies stay in .tools/web-validation; a machine without that directory
// can point AHA_PLAYWRIGHT_MODULE / AHA_BROWSER_EXECUTABLE at a local install.
import assert from "node:assert/strict";
import {createServer} from "node:http";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "../..");
const tools = resolve(root, ".tools/web-validation");
const playwrightModule = process.env.AHA_PLAYWRIGHT_MODULE || resolve(tools, "node_modules/playwright/index.mjs");
const {chromium} = await import(pathToFileURL(playwrightModule));
const launchOptions = process.env.AHA_BROWSER_EXECUTABLE
  ? {executablePath: process.env.AHA_BROWSER_EXECUTABLE, args: ["--no-sandbox"]}
  : await (async () => {
      const {default: binary} = await import(pathToFileURL(resolve(tools, "node_modules/@sparticuz/chromium/build/index.js")));
      return {executablePath: await binary.executablePath(), args: binary.args};
    })();
const csp = (await readFile(resolve(root, "internal/httpapi/server.go"), "utf8")).match(/Set\("Content-Security-Policy", "([^"]+)"\)/)[1];

const minutesAgo = minutes => new Date(Date.now() - minutes * 60_000).toISOString();
// The newest approval page holds only the idea; the desk approval a-old is
// older than that page, so opening it must read the wider approval list.
const pageApprovals = [
  {id: "idea-1", task_id: "pet-task", kind: "idea", title: "整理渠道联系人", summary: "想法说明", risk: "low", status: "pending", created_at: minutesAgo(10)},
];
const allApprovals = [
  ...pageApprovals,
  {id: "a-old", task_id: "pet-task", kind: "owner_decision", title: "是否合并 task-801 的候选", summary: "宠物的理由摘要", risk: "high", status: "pending", created_at: minutesAgo(60)},
];
const desk = {
  needs_owner: [
    {kind: "blocked", task_id: "t-790", task_code: "task-790", title: "需要一个权限才能继续", detail: "缺少权限", since: minutesAgo(90)},
    {kind: "approval", approval_id: "a-old", approval_kind: "owner_decision", risk: "high", task_id: "t-801", task_code: "task-801", title: "是否合并 task-801 的候选", detail: "宠物的理由摘要", since: minutesAgo(60)},
    {kind: "question", task_id: "t-813", task_code: "task-813", title: "宠物工作台改造计划", questions: ["先只做第一步？", "只出计划？"], since: minutesAgo(45)},
  ],
  needs_owner_total: 3,
  later: [
    {kind: "idea", approval_id: "idea-1", title: "整理渠道联系人", since: minutesAgo(10)},
    {kind: "next_step", task_id: "t-3", task_code: "task-3", title: "做完一轮的任务", detail: "等评审意见", since: minutesAgo(200)},
  ],
  later_total: 2,
  waiting_others: [
    {kind: "review", task_id: "t-805", task_code: "task-805", title: "评审中的任务", on: "task-806", since: minutesAgo(50)},
    {kind: "channel", title: "飞书群 A", on: "c-1", count: 3, since: minutesAgo(4)},
  ],
  waiting_others_total: 2,
  running: [
    {task_id: "t-811", task_code: "task-811", title: "跑过夜稳定性对比", status: "active", turn_status: "running", detail: "正在对比", hardware: ["调试板 1"], since: minutesAgo(12)},
  ],
  running_total: 1,
  corrections: [{at: minutesAgo(300), approval_id: "a-9", kind: "delete_workspace", title: "删除工作区 X", owner_note: "这个还要留着对比"}],
  open_loops: ["跟进合作方答复"], open_loops_total: 5,
  agenda: ["整理渠道"], agenda_total: 3,
};
const watches = [
  {task_id: "t-w", code: "task-9", title: "关注的任务", status: "active", watched: true, ignored: false, reason: "manual", created_at: minutesAgo(600), updated_at: minutesAgo(30)},
];

const bootstrap = `
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
const params = new URLSearchParams(location.search);
window.__desk = params.get("desk") === "0" ? undefined : ${JSON.stringify(desk)};
window.__workbenchQueries = [];
window.__taskReads = [];
const pageApprovals = ${JSON.stringify(pageApprovals)};
const allApprovals = ${JSON.stringify(allApprovals)};
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: params.get("language") || "en-US"}});
  if (path === "/api/v1/system") return json({system: {web_version: "test", version: "test", started_at: new Date().toISOString()}});
  if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", nickname: "AHA", enabled: false}, messages: [], models: [], tools: []});
  if (path === "/api/v1/pet/approvals/pending-count") return json({ok: true, pending: 2});
  if (path === "/api/v1/pet/workbench") {
    const limit = Number(url.searchParams.get("approval_limit") || 50);
    window.__workbenchQueries.push(limit);
    const approvals = limit >= 200 ? allApprovals : pageApprovals;
    return json({
      ok: true, core: [{name: "core/loops.md", content: "loops"}], worklog: [], approvals, approval_total: allApprovals.length,
      approval_counts: [{kind: "idea", status: "pending", count: 1}, {kind: "owner_decision", status: "pending", count: 1}],
      watches: ${JSON.stringify(watches)}, updated_at: new Date().toISOString(),
      inspection: {health: "ok", version: "9.9.9", web_version: "web-1", uptime_minutes: 125, checked_at: new Date().toISOString(), pending_approvals: 2},
      channels: [{id: "c-1", name: "Mail", status: "degraded", pending: 3, degraded: true}], channel_total: 1,
      ...(window.__desk ? {desk: window.__desk} : {}),
    });
  }
  if (path.startsWith("/api/v1/tasks/")) {
    window.__taskReads.push(path.split("/")[4]);
    return json({ok: false, error: "task_stub"}, 404);
  }
  if (path === "/api/v1/tasks") return json({tasks: [], has_more: false});
  if (path === "/api/v1/projects") return json({projects: [], has_more: false});
  if (path === "/api/v1/workspaces") return json({workspaces: []});
  if (path === "/api/v1/models") return json({models: []});
  if (path === "/api/v1/skills") return json({skills: []});
  if (path === "/api/v1/codex-accounts") return json({accounts: []});
  if (path.startsWith("/api/v1/")) return json({ok: true});
  return json({});
};
window.EventSource = class {
  constructor(url) { this.url = String(url); }
  close() {}
  addEventListener() {}
};
sessionStorage.setItem("aha2.navigation", JSON.stringify({view: "pet-workbench"}));
`;

const html = `<!doctype html><html lang="en-US"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><link rel="stylesheet" href="/styles.css"></head><body><div id="app"></div><div id="app-status" class="visually-hidden" role="status" aria-live="polite" aria-atomic="true"></div><script src="/fixture.js"></script><script type="module" src="/app.js"></script></body></html>`;

const server = createServer(async (request, response) => {
  const name = new URL(request.url, "http://localhost").pathname;
  try {
    response.setHeader("Content-Security-Policy", csp);
    if (name === "/" || name === "/index.html") {
      response.setHeader("Content-Type", "text/html");
      response.end(html);
    } else if (name === "/fixture.js") {
      response.setHeader("Content-Type", "text/javascript");
      response.end(bootstrap);
    } else if (name === "/favicon.ico") {
      response.writeHead(204).end();
    } else if (name.startsWith("/assets/")) {
      response.setHeader("Content-Type", name.endsWith(".svg") ? "image/svg+xml" : "image/png");
      response.end(await readFile(resolve(root, "web/build", name.replace(/^\//, ""))));
    } else {
      if (!/^\/[a-z0-9_]+\.(?:js|css)$/.test(name)) { response.writeHead(404).end(); return; }
      response.setHeader("Content-Type", name.endsWith(".css") ? "text/css" : "text/javascript");
      response.end(await readFile(resolve(root, "web/build", name.slice(1))));
    }
  } catch { response.writeHead(404).end(); }
});
await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
const url = `http://127.0.0.1:${server.address().port}`;

// Same scan as pet_i18n.browser.mjs: interface copy must be English; server
// content is marked data-i18n-ignore by the desk renderer.
const untranslated = page => page.evaluate(() => {
  const body = document.querySelector("#pet-workbench-body");
  const skip = "[data-i18n-ignore],.pet-watch-row strong,pre,code,textarea,input";
  const out = [];
  const walker = document.createTreeWalker(body, NodeFilter.SHOW_TEXT);
  while (walker.nextNode()) {
    const el = walker.currentNode.parentElement;
    if (!el || el.closest(skip)) continue;
    const text = (walker.currentNode.nodeValue || "").trim();
    if (text && /[一-鿿]/.test(text)) out.push(text);
  }
  for (const el of body.querySelectorAll("[title],[aria-label]")) {
    if (el.closest(skip)) continue;
    for (const name of ["title", "aria-label"]) {
      const value = el.getAttribute(name);
      if (value && /[一-鿿]/.test(value)) out.push(`${name}=${value}`);
    }
  }
  return out;
});

const openOverview = async (page, query = "") => {
  await page.goto(`${url}/${query}`, {waitUntil: "domcontentloaded"});
  await page.waitForSelector("#pet-workbench-body .pet-overview", {timeout: 20000});
};
const backToOverview = async page => {
  await page.click('[data-pet-workbench-tab="overview"]');
  await page.waitForSelector("#pet-workbench-body .pet-desk", {timeout: 15000});
};
const closeDialog = async page => {
  await page.click("dialog.pet-approval-detail-dialog [data-pet-approval-detail-close]");
  await page.waitForSelector("dialog.pet-approval-detail-dialog", {state: "detached"});
};

let browser;
try {
  browser = await chromium.launch({...launchOptions, headless: true});
  const page = await browser.newPage({viewport: {width: 1400, height: 1000}, locale: "en-US"});
  const errors = [];
  // The Task stub answers 404 on purpose: this test only proves the click asks
  // for the right Task. The task page's own handling of that 404 is not under
  // test here, so its stub error is not a desk failure.
  page.on("pageerror", error => { if (error.message !== "task_stub") errors.push(error.message); });

  await openOverview(page);
  const body = page.locator("#pet-workbench-body");
  await body.locator(".pet-desk").waitFor();

  // Item 1: block order; cards and recent activity are gone.
  const blocks = await page.evaluate(() => [...document.querySelector("#pet-workbench-body .pet-desk").children].map(el => el.className));
  assert.deepEqual(blocks, ["pet-desk-status", "pet-desk-section pet-desk-needs", "pet-desk-pair", "pet-desk-section pet-desk-pet", "pet-desk-follows"]);
  assert.equal(await body.locator(".pet-overview-cards, .pet-overview-recent").count(), 0);
  assert.equal(await body.locator("#pet-overview-follows, #pet-overview-ignored").count(), 2);
  // Wide screen: waiting and running side by side; phone: stacked.
  const columns = () => page.evaluate(() => {
    const box = id => document.getElementById(id).getBoundingClientRect();
    const waiting = box("pet-desk-waiting");
    const running = box("pet-desk-running");
    return {sameRow: Math.abs(waiting.top - running.top) < 2 && running.left > waiting.right - 1, stacked: running.top >= waiting.bottom - 1};
  });
  assert.equal((await columns()).sameRow, true, "wide screen puts the two columns side by side");
  await page.setViewportSize({width: 390, height: 844});
  assert.equal((await columns()).stacked, true, "a phone stacks the two columns");
  const overflow = await page.evaluate(() => document.querySelector("#pet-workbench-body").scrollWidth - document.querySelector("#pet-workbench-body").clientWidth);
  assert.ok(overflow <= 1, `the desk must not scroll sideways on a phone (${overflow}px)`);
  await page.setViewportSize({width: 1400, height: 1000});

  // Item 13: the English desk has no Chinese interface copy.
  await body.locator("#pet-desk-later summary").click();
  assert.deepEqual(await untranslated(page), [], "English desk left untranslated Chinese");
  const status = await body.locator(".pet-desk-status").innerText();
  assert.match(status, /3 for you/);
  assert.match(status, /2 waiting on others/);
  assert.match(status, /1 running/);
  assert.match(await body.locator(".pet-desk-status").getAttribute("title"), /Version: 9\.9\.9/);
  // Item 3: numbering from ① in response order.
  assert.deepEqual(await body.locator("#pet-desk-needs .pet-desk-index").allInnerTexts(), ["①", "②", "③"]);
  // Item 5: the later list starts folded on every fresh render.
  await page.click('[data-pet-workbench-tab="records"]');
  await backToOverview(page);
  assert.equal(await body.locator("#pet-desk-later").getAttribute("open"), null, "later starts collapsed");

  // Item 4: an approval older than the loaded approval page still opens its
  // detail; the page reads the wider list once.
  const queriesBefore = await page.evaluate(() => window.__workbenchQueries.length);
  await body.locator('#pet-desk-needs [data-pet-approval-open="a-old"].pet-desk-item-main').click();
  await page.waitForSelector("dialog.pet-approval-detail-dialog[open]", {timeout: 15000});
  assert.match(await page.locator("dialog.pet-approval-detail-dialog").innerText(), /是否合并 task-801 的候选/);
  assert.ok((await page.evaluate(() => window.__workbenchQueries.slice(-1)[0])) >= 200, "the wide read uses the server cap");
  await closeDialog(page);
  assert.equal(await page.locator('[data-pet-workbench-tab="overview"]').getAttribute("class"), "active", "still on the overview");
  // An idea from the folded later list opens from the loaded page directly.
  const queriesBeforeIdea = await page.evaluate(() => window.__workbenchQueries.length);
  assert.ok(queriesBeforeIdea > queriesBefore);
  await body.locator("#pet-desk-later summary").click();
  await body.locator('#pet-desk-later [data-pet-approval-open="idea-1"].pet-desk-item-main').click();
  await page.waitForSelector("dialog.pet-approval-detail-dialog[open]");
  assert.match(await page.locator("dialog.pet-approval-detail-dialog").innerText(), /整理渠道联系人/);
  assert.equal(await page.evaluate(() => window.__workbenchQueries.length), queriesBeforeIdea, "a loaded approval needs no extra read");
  await closeDialog(page);
  // No approve/reject buttons on the desk itself.
  assert.equal(await body.locator("[data-pet-approval-decision]").count(), 0);

  // Item 12: after a repaint (tab round trip) the desk buttons still work.
  await page.click('[data-pet-workbench-tab="approvals"]');
  await page.waitForSelector("[data-pet-approval-filters-open]");
  await backToOverview(page);
  await body.locator("#pet-desk-pet [data-pet-overview-jump=\"records\"]").click();
  await page.waitForSelector('[data-pet-workbench-tab="records"].active');
  await body.locator(".pet-records").waitFor();
  await backToOverview(page);

  // A channel row jumps to the channel cards.
  await body.locator('#pet-desk-waiting [data-pet-overview-jump="channels"]').click();
  await page.waitForSelector(".pet-workbench-page", {state: "detached", timeout: 15000});
  await page.waitForSelector('.sidebar [data-view="channels"].active', {timeout: 15000});
  await page.locator('.sidebar [data-view="pet-workbench"]').click();
  await page.waitForSelector("#pet-workbench-body .pet-desk", {timeout: 20000});

  // A task row opens the Task.
  await page.locator('#pet-desk-needs .pet-desk-item-main[data-pet-overview-task="t-790"]').click();
  await page.waitForFunction(() => window.__taskReads.includes("t-790"), null, {timeout: 15000});
  await page.waitForSelector(".pet-workbench-page", {state: "detached"});

  // Item 10: without desk the pre-desk overview comes back.
  await openOverview(page, "?desk=0");
  assert.equal(await body.locator(".pet-desk").count(), 0);
  assert.equal(await body.locator(".pet-overview-cards").count(), 1);
  assert.equal(await body.locator(".pet-overview-recent").count(), 1);
  assert.deepEqual(await untranslated(page).then(items => items.filter(text => !/整理渠道联系人|关注的任务/.test(text))), []);

  // The Chinese desk renders the same structure.
  await openOverview(page, "?language=zh-CN");
  await body.locator(".pet-desk").waitFor();
  assert.match(await body.locator("#pet-desk-needs h3").innerText(), /要你定（3）/);
  assert.match(await body.locator("#pet-desk-later summary").innerText(), /不急的还有 2 件/);

  assert.deepEqual(errors, [], `page errors: ${errors.join(" | ")}`);
  console.log("pet desk browser checks passed");
} finally {
  await browser?.close();
  server.close();
}
