import { createRequire } from "node:module";
const require = createRequire("/home/kaikai/kk-workspace/my_project/AHA2/.tools/web-validation/package.json");
const { chromium } = require("playwright");

const BASE = "http://127.0.0.1:18766";
const CHROME = "/home/kaikai/.cache/ms-playwright/chromium-1234/chrome-linux64/chrome";
const TABS = Number(process.env.TABS || 3);

const browser = await chromium.launch({ headless: true, executablePath: CHROME });
const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
const pages = [];
const events = [];
const streams = [];
for (let i = 0; i < TABS; i++) {
  const page = await context.newPage();
  page.on("request", r => {
    const url = r.url();
    if (url.includes("/events")) streams.push(url);
    if (url.includes("detect-models") || url.includes("model-probes")) events.push("REQ " + r.method() + " " + url.replace(BASE, ""));
  });
  page.on("requestfailed", r => {
    if (r.url().includes("detect-models") || r.url().includes("model-probes")) events.push("FAIL " + r.url().replace(BASE, ""));
  });
  await page.goto(BASE, { waitUntil: "domcontentloaded" });
  const loginForm = page.locator("#auth-form");
  const navButton = page.locator('.sidebar nav button[data-view="models"]');
  await Promise.race([
    loginForm.waitFor({ state: "visible", timeout: 15000 }).catch(() => {}),
    navButton.waitFor({ state: "visible", timeout: 15000 }).catch(() => {}),
  ]);
  if (await loginForm.isVisible()) {
    await page.fill('#auth-form input[name="username"]', "owner");
    await page.fill('#auth-form input[name="password"]', "e2e-password-12345");
    await page.click('#auth-form button[type="submit"]');
  }
  await navButton.waitFor({ state: "visible", timeout: 15000 });
  pages.push(page);
}
await new Promise(r => setTimeout(r, 2000));
console.log(`opened ${TABS} tabs`);
if (process.env.CLOSE_LAST === "1" && pages.length > 1) {
  await pages.pop().close();
  await new Promise(r => setTimeout(r, 1000));
  console.log("closed last tab");
}

const page = pages[0];
page.on("console", m => { if (m.type() === "error") events.push("CONSOLE " + m.text()); });
page.on("pageerror", e => events.push("PAGEERROR " + e.message));
await page.click('.sidebar nav button[data-view="models"]');
await page.waitForSelector('button[data-dialog="model"]', { timeout: 15000 });
await page.click('button[data-dialog="model"]');
await page.waitForSelector("#model-dialog[open]");
const waitMs = Number(process.env.WAIT_MS || 12000);
const started = Date.now();
const clickStart = Date.now();
await page.click("#detect-models");
console.log("click ms", Date.now() - clickStart);
let rows = 0;
try {
  await page.waitForSelector(".detected-item", { timeout: waitMs });
  rows = await page.locator(".detected-item").count();
} catch { /* timed out */ }
if (!rows && process.env.WAIT_LONG === "1") {
  const deadline = Date.now() + 25000;
  while (Date.now() < deadline) {
    const text = await page.locator("#model-detect-progress").textContent().catch(() => "");
    if (text && (text.includes("失败") || text.includes("超时"))) break;
    await new Promise(r => setTimeout(r, 1000));
  }
}
const status = await page.locator("#model-detect-progress").textContent().catch(() => "");
const dom = await page.evaluate(() => ({dialogOpen: Boolean(document.querySelector("#model-dialog")?.open), results: (document.querySelector("#model-detect-results")?.innerHTML || "").slice(0, 260), button: document.querySelector("#detect-models")?.innerHTML?.slice(0,80), visibility: document.visibilityState}));
console.log("dom", JSON.stringify(dom));
const petStreams = streams.filter(url => url.includes("pet/events"));
const globalStreams = streams.filter(url => url.includes("/api/v1/events"));
console.log(JSON.stringify({ tabs: TABS, rows, elapsedMs: Date.now() - started, status, petStreams: petStreams.length, globalStreams: globalStreams.length, events }));
await page.screenshot({ path: `/home/kaikai/kk-workspace/my_project/AHA2/tmp/e2e/tabs-${TABS}.png`, fullPage: true });
await browser.close();
