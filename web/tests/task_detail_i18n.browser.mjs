// Optional browser verification for the Task detail screen in English mode.
// The Round card and the message composer are rendered from TypeScript, so a
// missing phrase or a composite text node can leave Chinese in the UI even when
// the rest of the page is translated.
import assert from "node:assert/strict";
import {createServer} from "node:http";
import {readFile} from "node:fs/promises";
import {resolve, extname} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "../..");
const tools = resolve(root, ".tools/web-validation");
const {chromium} = await import(pathToFileURL(resolve(tools, "node_modules/playwright/index.mjs")));
const configuredBrowser = process.env.AHA_BROWSER_EXECUTABLE;
const {default: binary} = configuredBrowser ? {default: null} : await import(pathToFileURL(resolve(tools, "node_modules/@sparticuz/chromium/build/index.js")));
const csp = (await readFile(resolve(root, "internal/httpapi/server.go"), "utf8")).match(/Set\("Content-Security-Policy", "([^"]+)"\)/)[1];

const bootstrap = `
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
const language = new URLSearchParams(location.search).get("lang") === "zh-CN" ? "zh-CN" : "en-US";
const now = new Date().toISOString();
const started = new Date(Date.now() - 20000).toISOString();
const task = {
  id: "task-1", code: "task-001", title: "Round example", status: "waiting_user",
  project_id: "project-1", workspace_id: "workspace-1", collaboration_mode: "auto", max_agents: 3,
  original_request: "do the thing", current_goal: "do the thing", task_branch: "",
  runtime_config_snapshot_id: "runtime-1", total_tokens: 0,
  created_at: now, updated_at: now,
};
const turn = {
  id: "turn-1", round_id: "round-1", agent_id: "main", status: "succeeded", generation: 1, attempt: 1,
  usage: {input_tokens: 1200, output_tokens: 300, context_tokens: 5000}, context_window: 128000,
  elapsed_ms: 20000, started_at: started, finished_at: now,
};
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  const method = (init.method || "GET").toUpperCase();
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
  if (path === "/api/v1/settings/general") return json({ok: true, general: {language}});
  if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", nickname: "AHA", enabled: true}, messages: [], models: [], tools: []});
  if (path === "/api/v1/tasks/task-1" && method === "GET") return json({
    task, turns: [turn],
    agents: [
      {agent_id: "main", status: "idle", title: "Main", runtime_config_valid: true, unread_count: 0},
      {agent_id: "worker", status: "failed", title: "Worker", runtime_config_valid: true, unread_count: 2},
      {agent_id: "runner", status: "running", title: "Runner", runtime_config_valid: true, unread_count: 0},
    ],
    latest_round: {id: "round-1", sequence: 3, status: "completed", started_at: started, finished_at: now, elapsed_ms: 20000},
    hardware: [], memory: {current_goal: "do the thing"},
  });
  if (path === "/api/v1/projects") return json({projects: [{id: "project-1", name: "Project", project_type: "folder", channel_retired: false}], has_more: false});
  if (path === "/api/v1/workspaces") return json({workspaces: [{id: "workspace-1", project_id: "project-1", name: "Workspace", root_path: "/tmp/workspace", transport: "native", health: "ready"}]});
  if (path === "/api/v1/tasks") return json({tasks: [task], has_more: false});
  if (path === "/api/v1/tasks/task-1/channel-routes") return json({ok: true, destinations: [], route: null, contacts: [], members: []});
  if (path === "/api/v1/tasks/task-1/agents") return json({agents: [
    {agent_id: "main", status: "idle", title: "Main", runtime_config_valid: true, unread_count: 0},
    {agent_id: "worker", status: "failed", title: "Worker", runtime_config_valid: true, unread_count: 2},
    {agent_id: "runner", status: "running", title: "Runner", runtime_config_valid: true, unread_count: 0},
  ]});
  if (path === "/api/v1/models") return json({models: []});
  if (path === "/api/v1/skills") return json({skills: []});
  if (path === "/api/v1/codex/accounts") return json({accounts: []});
  if (path.includes("/conversation")) return json({conversation: {items: [], has_more: false, next_before: 0, latest_sequence: 0}});
  if (path.includes("/context")) return json({context: {}});
  if (path.endsWith("/events")) return json({events: [], cursor: 0});
  if (path.startsWith("/api/v1/")) return json({});
  return json({});
};
window.EventSource = class { constructor() {} close() {} addEventListener() {} };
sessionStorage.setItem("aha2.navigation", JSON.stringify({view: "tasks", taskID: "task-1", task, agentID: "main"}));
`;

const html = `<!doctype html><html lang="en-US"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>AHA2</title><link rel="stylesheet" href="/styles.css"></head><body><main id="app"></main><script src="/fixture.js"></script><script type="module" src="/app.js"></script></body></html>`;
const mime = {".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png", ".html": "text/html"};
const server = createServer(async (request, response) => {
  const name = new URL(request.url, "http://localhost").pathname;
  try {
    response.setHeader("Content-Security-Policy", csp);
    if (name === "/" || name === "/index.html") {
      response.setHeader("Content-Type", "text/html");
      response.end(html);
      return;
    }
    if (name === "/fixture.js") {
      response.setHeader("Content-Type", "text/javascript");
      response.end(bootstrap);
      return;
    }
    const data = await readFile(resolve(root, "web/build", name.slice(1)));
    response.setHeader("Content-Type", mime[extname(name)] || "application/octet-stream");
    response.end(data);
  } catch {
    response.writeHead(404).end();
  }
});
await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
const url = `http://127.0.0.1:${server.address().port}`;

let browser;
try {
  const launchOptions = configuredBrowser
    ? {executablePath: configuredBrowser, args: ["--no-sandbox"]}
    : {executablePath: await binary.executablePath(), args: binary.args};
  browser = await chromium.launch({...launchOptions, headless: true});
  const page = await browser.newPage({viewport: {width: 1280, height: 900}, locale: "en-US"});
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto(url, {waitUntil: "networkidle"});
  await page.waitForSelector("#message-form", {timeout: 15000});

  const placeholder = await page.locator("#message-form textarea").getAttribute("placeholder");
  assert.equal(placeholder, "Send to main; type / for commands");
  const agentOptions = await page.locator("#composer-agent option").allTextContents();
  assert.ok(agentOptions.some(text => text === "● main · Idle"), `idle agent status is not translated: ${agentOptions.join(" | ")}`);
  assert.ok(agentOptions.some(text => text === "○ worker · Failed · 2 Unread"), `failed agent status is not translated: ${agentOptions.join(" | ")}`);
  assert.ok(agentOptions.some(text => text === "● runner · Running"), `running agent status is not translated: ${agentOptions.join(" | ")}`);

  const scan = async (label) => {
    const missing = await page.evaluate(() => {
      const root = document.querySelector("#app");
      const skip = [".markdown-body", ".message-text", ".message-bubble", ".task-title-text", "[data-task-title]", ".task-card-title h3", ".item-title strong", ".knowledge-tree-item-title", "pre", "code", "textarea", "input", "[contenteditable='true']", "[data-i18n-ignore]"].join(",");
      const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
      const out = [];
      while (walker.nextNode()) {
        const el = walker.currentNode.parentElement;
        if (!el || el.closest(skip)) continue;
        const text = (walker.currentNode.nodeValue || "").trim();
        if (text && /[一-鿿]/.test(text)) out.push(text.slice(0, 120));
      }
      for (const el of root.querySelectorAll("[placeholder],[title],[aria-label]")) {
        if (el.closest(skip)) continue;
        for (const name of ["placeholder", "title", "aria-label"]) {
          const value = el.getAttribute(name);
          if (value && /[一-鿿]/.test(value)) out.push(`${name}=${value.slice(0, 120)}`);
        }
      }
      return [...new Set(out)];
    });
    assert.deepEqual(missing, [], `${label} left untranslated Chinese in Task detail`);
  };
  // The permission, mail and attachment toolbar controls are icon-only, so
  // their title and aria-label are the only visible or screen-reader copy.
  const assertTaskToolLabels = async (target, expectations) => {
    for (const [selector, expected] of Object.entries(expectations)) {
      const element = target.locator(selector);
      assert.equal(await element.getAttribute("title"), expected.title, `${selector} title`);
      assert.equal(await element.getAttribute("aria-label"), expected.label, `${selector} aria-label`);
    }
  };
  await scan("closed Round card");
  await assertTaskToolLabels(page, {
    '[data-task-tool="permissions"]': {title: "Permission requests", label: "Permission requests"},
    '[data-task-tool="mail"]': {title: "Mail", label: "Mail"},
    "#message-attachment-pick": {
      title: "Add attachments or paste images (up to 8 per message, 200MiB per file)",
      label: "Add attachments (up to 8 per message, 200MiB per file)",
    },
  });
  assert.match(await page.locator("#agent-turn-slot summary").innerText(), /Round 3 · Completed/);

  await page.evaluate(() => document.querySelector("#agent-turn-slot summary")?.click());
  await page.waitForTimeout(200);
  await scan("open Round card");

  // The other composer states are not reachable from this fixture; probe the
  // same translation layer for their dynamic placeholders and card labels.
  await page.evaluate(async () => {
    const mod = await import("/i18n.js");
    const probe = document.createElement("div");
    probe.id = "task-i18n-probe";
    probe.innerHTML = '<input placeholder="main 正在执行，发送后将排队"><input placeholder="启动任务后可发送消息"><input placeholder="只读 Task"><span title="编辑 main 配置">Context 准备</span><span>等待输入</span><span>正在打开任务</span><span>加载最近 20 条消息…</span>';
    document.body.appendChild(probe);
    mod.applyLocale("en-US");
    mod.translateUI(probe);
  });
  assert.equal(await page.locator("#task-i18n-probe input").nth(0).getAttribute("placeholder"), "main is running; messages will be queued");
  assert.equal(await page.locator("#task-i18n-probe input").nth(1).getAttribute("placeholder"), "Start the Task to send messages");
  assert.equal(await page.locator("#task-i18n-probe input").nth(2).getAttribute("placeholder"), "Read-only Task");
  assert.equal(await page.locator("#task-i18n-probe span[title]").getAttribute("title"), "Edit main configuration");
  assert.equal(await page.locator("#task-i18n-probe span[title]").innerText(), "Context prep");
  assert.equal(await page.locator("#task-i18n-probe span").nth(1).innerText(), "Waiting for input");
  assert.equal(await page.locator("#task-i18n-probe span").nth(2).innerText(), "Opening Task");
  assert.equal(await page.locator("#task-i18n-probe span").nth(3).innerText(), "Loading the latest 20 messages…");

  // The same three controls must stay Chinese in zh-CN; an English fallback
  // there is just as much a translation gap as Chinese leaking into en-US.
  const zhPage = await browser.newPage({viewport: {width: 1280, height: 900}, locale: "zh-CN"});
  const zhErrors = [];
  zhPage.on("pageerror", error => zhErrors.push(error.message));
  await zhPage.goto(`${url}/?lang=zh-CN`, {waitUntil: "networkidle"});
  await zhPage.waitForSelector("#message-form", {timeout: 15000});
  await assertTaskToolLabels(zhPage, {
    '[data-task-tool="permissions"]': {title: "权限申请", label: "权限申请"},
    '[data-task-tool="mail"]': {title: "邮件", label: "邮件"},
    "#message-attachment-pick": {
      title: "添加附件或直接粘贴图片（每条消息最多 8 个，单文件 ≤200MiB）",
      label: "添加附件（每条消息最多 8 个，单文件 ≤200MiB）",
    },
  });
  assert.deepEqual(zhErrors, []);
  await zhPage.close();

  // Phone geometry: the global fixed search FAB used to sit on the composer.
  // Task view now exposes an in-stream search entry that ends above the
  // composer, leaves the attachment/send controls clear, and still opens the
  // palette with focus returning to it after Escape.
  for (const [width, height] of [[360, 800], [390, 844]]) {
    await page.setViewportSize({width, height});
    await page.waitForTimeout(120);
    const geometry = await page.evaluate(() => {
      const rect = selector => {
        const box = document.querySelector(selector).getBoundingClientRect();
        return {top: box.top, right: box.right, bottom: box.bottom, left: box.left, width: box.width, height: box.height};
      };
      return {
        taskViewActive: document.body.classList.contains("task-view-active"),
        globalFabDisplay: getComputedStyle(document.querySelector(".app-shell > .command-palette-fab")).display,
        fab: rect(".task-search-fab"),
        composer: rect("#message-form"),
        attachment: rect("#message-attachment-pick"),
        send: rect("#message-send"),
        overflow: document.documentElement.scrollWidth - window.innerWidth,
      };
    });
    assert.equal(geometry.taskViewActive, true, `${width}px is the Task detail route`);
    assert.equal(geometry.globalFabDisplay, "none", `${width}px hides the global FAB on the Task route`);
    assert.ok(geometry.fab.width >= 40 && geometry.fab.height >= 40, `${width}px search entry is a 40px touch target`);
    assert.ok(geometry.fab.bottom <= geometry.composer.top - 1, `${width}px search entry sits above the composer`);
    const overlaps = (a, b) => a.left < b.right && a.right > b.left && a.top < b.bottom && a.bottom > b.top;
    assert.equal(overlaps(geometry.fab, geometry.composer), false, `${width}px search entry does not cover the composer`);
    assert.equal(overlaps(geometry.fab, geometry.attachment), false, `${width}px search entry does not cover the attachment button`);
    assert.equal(overlaps(geometry.fab, geometry.send), false, `${width}px search entry does not cover send`);
    assert.ok(geometry.attachment.bottom <= height && geometry.send.bottom <= height, `${width}px composer controls stay on screen`);
    assert.ok(geometry.overflow <= 1, `${width}px Task detail has no horizontal overflow`);

    await page.locator(".task-search-fab").click();
    await page.waitForSelector("#command-palette", {state: "visible", timeout: 5000});
    await page.keyboard.press("Escape");
    await page.waitForFunction(() => {
      const palette = document.querySelector("#command-palette");
      return !palette || palette.open === false || getComputedStyle(palette).display === "none";
    });
    assert.equal(await page.evaluate(() => document.activeElement?.classList.contains("task-search-fab")), true, `${width}px focus returns to the search entry`);
  }
  await page.setViewportSize({width: 1280, height: 900});
  assert.deepEqual(errors, []);
} finally {
  await browser?.close();
  await new Promise(resolve => server.close(resolve));
}
console.log("task detail i18n browser check passed");
