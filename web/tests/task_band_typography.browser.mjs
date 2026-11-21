// Verifies the task band typography change at the three widths the owner
// cares about: normal desktop, the 272px narrow overlay, and a 390px phone.
import assert from "node:assert/strict";
import {createServer} from "node:http";
import {readFile, mkdir} from "node:fs/promises";
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
const output = process.env.AHA_BROWSER_SCREENSHOT_DIR || resolve(tools, "screenshots");
await mkdir(output, {recursive: true});

const now = "2026-11-21T09:00:00Z";
const task = {
  id: "t-band", code: "task-736", title: "任务带字体收敛与渠道页合并权限申请之后标题仍然保持两行截断",
  status: "active", project_id: "project-1", workspace_id: "workspace-1",
  project_name: "一个名字很长很长的项目用于验证省略号", workspace_name: "一个名字很长时间很长的 Workspace 用于验证省略号", created_at: now,
};
// A short title exposes the bug where the grid track was centered inside the
// card instead of starting at the left padding.
const shortTask = {
  id: "t-band-short", code: "task-737", title: "短标题",
  status: "active", project_id: "project-1", workspace_id: "workspace-1", created_at: now,
};
const bootstrap = `
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
const task = ${JSON.stringify(task)};
const shortTask = ${JSON.stringify(shortTask)};
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  const method = (init.method || "GET").toUpperCase();
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: "zh-CN"}});
  if (path === "/api/v1/system") return json({system: {web_version: "v0.0.0.test", version: "v0.0.0.test", started_at: new Date().toISOString()}});
  if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", nickname: "AHA", enabled: false}, messages: [], models: [], tools: []});
  if (path === "/api/v1/tasks" && method === "GET") return json({tasks: [task, shortTask], has_more: false, counts: {status: {active: 2}, project: {"project-1": 2}, device: {local: 2}}});
  if (path === "/api/v1/projects") return json({projects: [{id: "project-1", name: "一个名字很长很长的项目用于验证省略号", project_type: "folder", channel_retired: false}]});
  if (path === "/api/v1/workspaces") return json({workspaces: [{id: "workspace-1", project_id: "project-1", name: "一个名字很长时间很长的 Workspace 用于验证省略号", root_path: "/tmp/ws", transport: "native", health: "ready"}]});
  if (path === "/api/v1/skills" || path === "/api/v1/models" || path === "/api/v1/providers" || path === "/api/v1/env-groups") return json({skills: [], models: [], providers: [], env_groups: []});
  if (path === "/api/v1/codex-accounts") return json({accounts: []});
  if (path === "/api/v1/mcp-servers") return json({servers: []});
  if (path === "/api/v1/tasks/t-band" && method === "GET") return json({task: {...task, collaboration_mode: "auto", max_agents: 3}, turns: [], agents: [{task_id: "t-band", agent_id: "main", role: "main", status: "idle", title: "Main", runtime_config_valid: true, unread_count: 0}], latest_round: null, hardware: [], memory: {}, permission_requests: []});
  if (path.includes("/conversation")) return json({conversation: {items: [], has_more: false, next_before: 0, latest_sequence: 0}});
  if (path.includes("/channel-routes")) return json({destinations: [], contacts: [], members: []});
  if (path.startsWith("/api/v1/")) return json({});
  return json({});
};
window.EventSource = class { constructor() {} close() {} addEventListener() {} };
sessionStorage.setItem("aha2.navigation", JSON.stringify({view: "tasks"}));
`;

const html = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><link rel="stylesheet" href="/styles.css"></head><body><main id="app"></main><script type="module" src="/fixture.js"></script><script type="module" src="/app.js"></script></body></html>`;
const server = createServer(async (request, response) => {
  const name = new URL(request.url, "http://localhost").pathname;
  try {
    if (name === "/" || name === "/index.html") {
      response.setHeader("Content-Type", "text/html");
      response.end(html);
    } else if (name === "/fixture.js") {
      response.setHeader("Content-Type", "text/javascript");
      response.end(bootstrap);
    } else {
      if (!/^\/[a-z0-9_]+\.(?:js|css)$/.test(name)) { response.writeHead(404).end(); return; }
      response.setHeader("Content-Type", name.endsWith(".css") ? "text/css" : "text/javascript");
      response.end(await readFile(resolve(root, "web/build", name.slice(1))));
    }
  } catch { response.writeHead(404).end(); }
});
await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
const url = `http://127.0.0.1:${server.address().port}`;

const bandMetrics = page => page.evaluate(() => {
  const card = document.querySelector(".task-band-card");
  const head = document.querySelector(".task-band-head small");
  const meta = card.querySelector(".task-band-card-meta");
  const spans = [...meta.querySelectorAll(":scope > span")];
  const rect = element => element.getBoundingClientRect();
  const icons = [...meta.querySelectorAll(".icon")];
  return {
    bandWidth: Math.round(rect(document.querySelector(".task-band")).width),
    headFont: getComputedStyle(head).fontSize,
    headWhiteSpace: getComputedStyle(head).whiteSpace,
    headLines: Math.round(rect(head).height / parseFloat(getComputedStyle(head).lineHeight)),
    metaFont: getComputedStyle(meta).fontSize,
    metaRows: spans.map(span => Math.round(rect(span).top)),
    metaLeft: spans.map(span => Math.round(rect(span).left)),
    iconWidths: icons.map(icon => Math.round(rect(icon).width)),
    cardOverflow: card.scrollWidth - card.clientWidth,
    docOverflow: document.documentElement.scrollWidth - window.innerWidth,
    projectTitle: spans[0].getAttribute("title"),
    workspaceTitle: spans[1].getAttribute("title"),
    titleClamp: getComputedStyle(card.querySelector(".task-band-card-title")).webkitLineClamp,
  };
});

// Every content row must start at the card's left padding, and the right-side
// task code must stay at the right padding for both short and long titles.
const cardAlignment = (page, id) => page.locator(`.task-band-card[data-task-band-open="${id}"]`).evaluate(card => {
  const style = getComputedStyle(card);
  const rect = selector => card.querySelector(selector).getBoundingClientRect();
  const cardRect = card.getBoundingClientRect();
  const contentLeft = Math.round(cardRect.left + parseFloat(style.borderLeftWidth) + parseFloat(style.paddingLeft));
  const contentRight = Math.round(cardRect.right - parseFloat(style.borderRightWidth) - parseFloat(style.paddingRight));
  return {
    justify: style.justifyContent,
    contentLeft,
    contentRight,
    rowLefts: [".task-band-card-top", ".task-band-card-title", ".task-band-card-meta"].map(selector => Math.round(rect(selector).left)),
    statusLeft: Math.round(rect(".status").left),
    codeRight: Math.round(rect(".task-code").right),
  };
});

let browser;
try {
  browser = await chromium.launch({...launchOptions, headless: true});
  const page = await browser.newPage({viewport: {width: 1280, height: 900}});
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto(url, {waitUntil: "networkidle"});
  await page.waitForSelector('.task-card[data-task="t-band"]', {timeout: 15000});
  await page.locator('.task-card[data-task="t-band"] [data-task-open]').click();
  await page.waitForSelector(".task-band-card[data-task-band-open=\"t-band\"]", {timeout: 15000});

  const normal = await bandMetrics(page);
  assert.equal(normal.headFont, "9px");
  assert.equal(normal.headWhiteSpace, "nowrap");
  assert.equal(normal.headLines, 1);
  assert.equal(normal.metaFont, "9px");
  assert.equal(normal.metaRows.length, 2);
  assert.ok(normal.metaRows[1] - normal.metaRows[0] >= 8, `meta rows did not stack: ${normal.metaRows}`);
  assert.equal(normal.metaLeft[0], normal.metaLeft[1], "meta rows are not left aligned");
  assert.deepEqual(normal.iconWidths, [9, 9]);
  assert.equal(normal.projectTitle, "所属项目");
  assert.equal(normal.workspaceTitle, "所属 Workspace");
  assert.equal(normal.titleClamp, "2");
  assert.ok(normal.cardOverflow <= 1, `card overflows at 1280 (${normal.cardOverflow}px)`);
  assert.equal(normal.docOverflow <= 0, true);
  for (const id of ["t-band", "t-band-short"]) {
    const align = await cardAlignment(page, id);
    assert.equal(align.justify, "start", `${id} grid track is not left aligned (${align.justify})`);
    assert.deepEqual(align.rowLefts, [align.contentLeft, align.contentLeft, align.contentLeft], `${id} content rows start at ${align.rowLefts}, expected ${align.contentLeft}`);
    assert.equal(align.statusLeft, align.contentLeft, `${id} status row starts at ${align.statusLeft}, expected ${align.contentLeft}`);
    assert.equal(align.codeRight, align.contentRight, `${id} task code moved away from the right padding (${align.codeRight} vs ${align.contentRight})`);
  }
  await page.screenshot({path: resolve(output, "task-band-1280.png")});

  // 1000px: the band collapses to the rail; opening it overlays at 272px.
  await page.setViewportSize({width: 1000, height: 800});
  await page.waitForFunction(() => document.querySelector(".task-band")?.classList.contains("collapsed"), null, {timeout: 10000});
  await page.locator(".task-band-rail[data-task-band-toggle]").click();
  await page.waitForSelector(".task-band.narrow-expanded");
  const narrow = await bandMetrics(page);
  assert.ok(Math.abs(narrow.bandWidth - 272) <= 2, `narrow band is ${narrow.bandWidth}px`);
  assert.ok(narrow.cardOverflow <= 1, `card overflows at 272px (${narrow.cardOverflow}px)`);
  assert.equal(narrow.headLines, 1);
  assert.ok(narrow.metaRows[1] > narrow.metaRows[0]);
  assert.equal(narrow.docOverflow <= 0, true);
  await page.screenshot({path: resolve(output, "task-band-272.png")});
  await page.keyboard.press("Escape");

  // 390px: the band is hidden and the detail must not overflow.
  await page.setViewportSize({width: 390, height: 844});
  await page.waitForTimeout(150);
  assert.equal(await page.locator(".task-band").isVisible(), false);
  const phone = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  assert.ok(phone <= 0, `phone page overflows (${phone}px)`);
  await page.screenshot({path: resolve(output, "task-band-390.png")});
  assert.deepEqual(errors, [], "the band typography change threw");
  console.log("task band typography verified at 1280 / 272 / 390");
} finally {
  await browser?.close();
  server.close();
}
