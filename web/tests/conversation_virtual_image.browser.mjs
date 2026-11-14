// Regression test for delayed images above the viewport while the reader is
// following the bottom. A growing image in the overscan must be re-measured:
// the viewport stays at the bottom, the first visible row keeps its offset and
// the height cache absorbs the new image height. A second delayed image is
// released after scrolling up to prove the same growth does not move that
// anchor.
import assert from "node:assert/strict";
import {createServer} from "node:http";
import {readFile, mkdir} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";
import {deflateSync} from "node:zlib";

const root = resolve(import.meta.dirname, "../..");
const tools = resolve(root, ".tools/web-validation");
const {chromium} = await import(pathToFileURL(
  process.env.AHA_PLAYWRIGHT_MODULE || resolve(tools, "node_modules/playwright/index.mjs"),
));
const useInstalledBrowser = Boolean(process.env.AHA_BROWSER_EXECUTABLE);
const binary = useInstalledBrowser
  ? {executablePath: async () => process.env.AHA_BROWSER_EXECUTABLE, args: []}
  : (await import(pathToFileURL(resolve(tools, "node_modules/@sparticuz/chromium/build/index.js")))).default;
const scratch = resolve(root, ".aha2-context", "task_1b8aebee53ad2b363881", "scratch", "main", "virtual-image");
await mkdir(scratch, {recursive: true});
const csp = (await readFile(resolve(root, "internal/httpapi/server.go"), "utf8"))
  .match(/Set\("Content-Security-Policy", "([^"]+)"\)/)[1];

// A 1000x800 PNG. The attachment CSS caps it at 420px tall, so the image adds
// exactly the 420px growth the review reported once its bytes arrive.
function png(width, height, [red, green, blue]) {
  const crcTable = Array.from({length: 256}, (_, index) => {
    let value = index;
    for (let bit = 0; bit < 8; bit += 1) value = value & 1 ? 0xedb88320 ^ (value >>> 1) : value >>> 1;
    return value >>> 0;
  });
  const crc = buffer => {
    let value = 0xffffffff;
    for (const byte of buffer) value = crcTable[(value ^ byte) & 0xff] ^ (value >>> 8);
    return (value ^ 0xffffffff) >>> 0;
  };
  const chunk = (type, data) => {
    const length = Buffer.alloc(4);
    length.writeUInt32BE(data.length);
    const body = Buffer.concat([Buffer.from(type, "ascii"), data]);
    const checksum = Buffer.alloc(4);
    checksum.writeUInt32BE(crc(body));
    return Buffer.concat([length, body, checksum]);
  };
  const header = Buffer.alloc(13);
  header.writeUInt32BE(width, 0);
  header.writeUInt32BE(height, 4);
  header[8] = 8;
  header[9] = 2;
  const row = Buffer.alloc(1 + width * 3);
  for (let offset = 1; offset < row.length; offset += 3) {
    row[offset] = red;
    row[offset + 1] = green;
    row[offset + 2] = blue;
  }
  const raw = Buffer.concat(Array.from({length: height}, () => row));
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk("IHDR", header),
    chunk("IDAT", deflateSync(raw)),
    chunk("IEND", Buffer.alloc(0)),
  ]);
}
const IMAGE = png(1000, 800, [46, 116, 181]);

const TASK = {
  id: "t-image", code: "task-951", title: "Delayed image", status: "active",
  project_id: "project-1", workspace_id: "workspace-1", collaboration_mode: "single", max_agents: 1,
  original_request: "chat", current_goal: "chat", task_branch: "",
  runtime_config_snapshot_id: "runtime-1", total_tokens: 0,
  created_at: "2026-09-27T00:00:00Z", updated_at: "2026-09-27T00:00:00Z",
};

const MESSAGES = Array.from({length: 1200}, (_, index) => {
  const number = index + 1;
  const last = number === 1200;
  const image = number === 1197 || number === 1196;
  return {
    sequence: number,
    id: `ci-${number}`,
    task_id: TASK.id,
    agent_id: "main",
    category: "chat",
    kind: "agent_message",
    summary: last
      ? `Long message ${number}: ` + "the expanded body keeps several lines of prose so the row is tall. ".repeat(80)
      : `Line ${String(number).padStart(4, "0")}: the quick brown fox jumps over the lazy dog.`,
    payload: image
      ? {attachments: [{
        id: `slow-image-${number}`,
        name: `slow-${number}.png`,
        media_type: "image/png",
        size: IMAGE.length,
      }]}
      : {},
    created_at: "2026-09-27T00:00:00Z",
  };
});

const bootstrap = `
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
const TASK = ${JSON.stringify(TASK)};
const MESSAGES = ${JSON.stringify(MESSAGES)};
const lang = new URLSearchParams(location.search).get("lang") || "zh-CN";
const detail = () => ({
  task: TASK, turns: [],
  agents: [{agent_id: "main", status: "idle", title: "Main", runtime_config_valid: true, unread_count: 0}],
  latest_round: null, hardware: [], memory: {},
});
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  const method = (init.method || "GET").toUpperCase();
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: lang}});
  if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", nickname: "AHA", enabled: false}, messages: [], models: [], tools: []});
  if (path === "/api/v1/projects") return json({projects: [{id: "project-1", name: "Project", project_type: "folder", channel_retired: false}], has_more: false});
  if (path === "/api/v1/workspaces") return json({workspaces: [{id: "workspace-1", project_id: "project-1", name: "W", root_path: "/tmp", transport: "native", health: "ready"}]});
  if (path === "/api/v1/tasks/t-image" && method === "GET") return json(detail());
  if (path === "/api/v1/tasks/t-image/channel-routes") return json({ok: true, destinations: [], route: null, contacts: [], members: []});
  if (path === "/api/v1/tasks/t-image/agents") return json({agents: detail().agents});
  if (path === "/api/v1/tasks") return json({tasks: [TASK], has_more: false});
  if (path.includes("/conversation")) {
    const after = url.searchParams.has("after");
    return json({conversation: {
      items: after ? [] : MESSAGES, has_more: false, next_before: 0, latest_sequence: 1200,
    }});
  }
  if (path.includes("/context")) return json({context: {}});
  if (path.endsWith("/events")) return json({events: [], cursor: 0});
  if (path === "/api/v1/pet/approvals/pending-count") return json({ok: true, pending: 0});
  if (path.startsWith("/api/v1/")) return json({ok: true});
  return json({});
};
Object.defineProperty(navigator, "clipboard", {configurable: true, value: {writeText: async () => { window.__copied = true; }}});
window.EventSource = class { constructor() {} close() {} addEventListener() {} };
sessionStorage.setItem("aha2.navigation", JSON.stringify({view: "tasks", taskID: TASK.id, task: TASK, agentID: "main"}));
`;

const html = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><link rel="stylesheet" href="/styles.css"></head><body><main id="app"></main><script src="/fixture.js"></script><script type="module" src="/app.js"></script></body></html>`;

const pending = new Map(); // attachment id -> ServerResponse waiting for release
const server = createServer(async (request, response) => {
  const requestURL = new URL(request.url, "http://localhost");
  const name = requestURL.pathname;
  try {
    response.setHeader("Content-Security-Policy", csp);
    if (name === "/" || name === "/index.html") {
      response.setHeader("Content-Type", "text/html");
      response.end(html);
    } else if (name === "/fixture.js") {
      response.setHeader("Content-Type", "text/javascript");
      response.end(bootstrap);
    } else if (name === "/__pending-image") {
      response.setHeader("Content-Type", "application/json");
      response.end(JSON.stringify({pending: pending.has(requestURL.searchParams.get("id") || "")}));
    } else if (name === "/__release-image") {
      const id = requestURL.searchParams.get("id") || "";
      const held = pending.get(id);
      if (!held) { response.writeHead(409).end(); return; }
      pending.delete(id);
      held.setHeader("Content-Type", "image/png");
      held.setHeader("Content-Length", String(IMAGE.length));
      held.end(IMAGE);
      response.setHeader("Content-Type", "application/json");
      response.end(JSON.stringify({released: id}));
    } else if (name.startsWith("/api/v1/tasks/t-image/attachments/slow-image-")) {
      pending.set(name.split("/").at(-1), response);
    } else if (name.startsWith("/assets/")) {
      const type = name.endsWith(".svg") ? "image/svg+xml" : name.endsWith(".png") ? "image/png"
        : name.endsWith(".woff2") ? "font/woff2" : "application/octet-stream";
      response.setHeader("Content-Type", type);
      response.end(await readFile(resolve(root, "web/build", name.replace(/^\//, ""))));
    } else {
      if (!/^\/(?:vendor\/)?[a-z0-9_]+\.(?:js|css|png)$/.test(name)) { response.writeHead(404).end(); return; }
      const base = name.startsWith("/vendor/") ? resolve(root, "web") : resolve(root, "web/build");
      response.setHeader("Content-Type", name.endsWith(".css") ? "text/css" : name.endsWith(".png") ? "image/png" : "text/javascript");
      response.end(await readFile(resolve(base, name.replace(/^\/(vendor\/)?/, ""))));
    }
  } catch { response.writeHead(404).end(); }
});
await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
const url = `http://127.0.0.1:${server.address().port}`;

const settle = page => page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));

async function state(page) {
  return page.evaluate(() => {
    const list = document.querySelector("#conversation-list");
    const listTop = list.getBoundingClientRect().top;
    const rows = [...list.querySelectorAll("[data-message-id]")];
    const first = rows.find(node => node.getBoundingClientRect().bottom > listTop + 1);
    const image = document.querySelector('[data-message-id="ci-1197"] img');
    return {
      scrollTop: Math.round(list.scrollTop * 100) / 100,
      scrollHeight: Math.round(list.scrollHeight * 100) / 100,
      clientHeight: list.clientHeight,
      distance: Math.round((list.scrollHeight - list.scrollTop - list.clientHeight) * 100) / 100,
      first: first?.dataset.messageId || "",
      offset: first ? Math.round((first.getBoundingClientRect().top - listTop) * 100) / 100 : 0,
      image1197: Boolean(image),
      imageHeight: image ? Math.round(image.getBoundingClientRect().height) : null,
      imageComplete: image?.complete === true,
      imageY: image ? Math.round((image.getBoundingClientRect().top - listTop) * 100) / 100 : null,
      ids: rows.map(node => node.dataset.messageId),
      window: [list.dataset.windowStart, list.dataset.windowEnd],
    };
  });
}

async function release(id) {
  const result = await fetch(`${url}/__release-image?id=${encodeURIComponent(id)}`);
  assert.equal(result.status, 200, `release ${id} was not pending`);
}

let browser;
try {
  browser = await chromium.launch({
    executablePath: await binary.executablePath(),
    args: useInstalledBrowser ? ["--no-sandbox"] : binary.args,
    headless: true,
  });
  const page = await browser.newPage({viewport: {width: 1280, height: 800}});
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  page.on("console", message => { if (message.type() === "error") errors.push(message.text()); });
  await page.goto(url, {waitUntil: "domcontentloaded"});
  await page.waitForSelector("#conversation-list .message", {timeout: 20000});
  await page.waitForFunction(() => {
    const list = document.querySelector("#conversation-list");
    return list && list.scrollHeight - list.scrollTop - list.clientHeight < 80;
  }, null, {timeout: 20000});
  await page.waitForFunction(() => Boolean(document.querySelector('[data-message-id="ci-1197"]')), null, {timeout: 5000});

  // The image request must already be in flight before the row can grow.
  await page.waitForFunction(() => !document.querySelector('[data-message-id="ci-1197"] img')?.complete, null, {timeout: 5000});
  const pending1197 = await fetch(`${url}/__pending-image?id=slow-image-1197`).then(response => response.json());
  assert.equal(pending1197.pending, true, "the delayed image was never requested");

  await page.click('[data-message-id="ci-1200"] [data-toggle-message]');
  await settle(page);
  // Return to the very bottom with the expanded row above; ci-1197 is now in
  // the overscan above the viewport.
  await page.evaluate(() => {
    const list = document.querySelector("#conversation-list");
    list.scrollTop = list.scrollHeight;
  });
  await settle(page);
  await settle(page);
  const before = await state(page);
  assert.equal(before.image1197, true, "ci-1197 left the overscan window after expanding the last message");
  assert.ok(before.imageY !== null && before.imageY < 1, `ci-1197 is not above the viewport: ${before.imageY}`);
  assert.equal(before.imageHeight, 0);
  assert.ok(before.distance < 80, `not following the bottom before the image loads: ${JSON.stringify(before)}`);

  await release("slow-image-1197");
  await page.waitForFunction(() => {
    const image = document.querySelector('[data-message-id="ci-1197"] img');
    return image && image.complete && image.naturalHeight > 0;
  }, null, {timeout: 10000});
  await settle(page);
  await settle(page);
  const after = await state(page);
  const growth = after.scrollHeight - before.scrollHeight;
  assert.ok(Math.abs(growth - 420) <= 4, `the measured height did not absorb the image: grew ${growth}px`);
  assert.ok(after.distance < 2, `the reader was left ${after.distance}px above the bottom`);
  assert.equal(after.first, before.first, `the anchor jumped from ${before.first} to ${after.first}`);
  assert.ok(Math.abs(after.offset - before.offset) <= 2,
    `the anchor moved ${before.offset} -> ${after.offset}px`);

  // Non-bottom: scrolling up must keep the reader's row fixed when the second
  // delayed image loads above it. It is released only after the scroll settles.
  const pending1196 = await fetch(`${url}/__pending-image?id=slow-image-1196`).then(response => response.json());
  if (!pending1196.pending) {
    await page.waitForFunction(() => Boolean(document.querySelector('[data-message-id="ci-1196"] img')), null, {timeout: 5000});
    await page.waitForFunction(() => !document.querySelector('[data-message-id="ci-1196"] img')?.complete, null, {timeout: 5000});
  }
  await page.evaluate(() => {
    const list = document.querySelector("#conversation-list");
    list.dispatchEvent(new WheelEvent("wheel", {deltaY: -800, bubbles: true, cancelable: true}));
    list.scrollTop = Math.max(0, list.scrollTop - 900);
  });
  await settle(page);
  await settle(page);
  const upBefore = await state(page);
  assert.ok(upBefore.image1197, "ci-1197 is not rendered after scrolling up");
  assert.equal(upBefore.distance >= 80, true, "expected to be away from the bottom");
  const pending1196Again = await fetch(`${url}/__pending-image?id=slow-image-1196`).then(response => response.json());
  assert.equal(pending1196Again.pending, true, "the second delayed image was not in flight");
  await release("slow-image-1196");
  await page.waitForFunction(() => {
    const image = document.querySelector('[data-message-id="ci-1196"] img');
    return image && image.complete && image.naturalHeight > 0;
  }, null, {timeout: 10000});
  await settle(page);
  await settle(page);
  const upAfter = await state(page);
  assert.equal(upAfter.first, upBefore.first, `a delayed image moved the scrolled-up anchor: ${upBefore.first} -> ${upAfter.first}`);
  assert.ok(Math.abs(upAfter.offset - upBefore.offset) <= 2,
    `a delayed image moved the scrolled-up row ${upBefore.offset} -> ${upAfter.offset}px`);
  assert.ok(upAfter.distance >= 80, "a delayed image yanked the reader back to the bottom");

  assert.deepEqual(errors, [], "the delayed-image conversation threw");
  console.log("delayed image:", JSON.stringify({
    before: {distance: before.distance, first: before.first, offset: before.offset, height: before.scrollHeight},
    after: {distance: after.distance, first: after.first, offset: after.offset, height: after.scrollHeight, growth},
    up: {before: [upBefore.first, upBefore.offset], after: [upAfter.first, upAfter.offset], distance: upAfter.distance},
  }));
} finally {
  if (browser) await browser.close();
  server.close();
}