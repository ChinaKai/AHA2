import { createRequire } from "node:module";
const require = createRequire("/home/kaikai/kk-workspace/my_project/AHA2/.tools/web-validation/package.json");
const { chromium } = require("playwright");

const BASE = "http://127.0.0.1:18766";
const CHROME = "/home/kaikai/.cache/ms-playwright/chromium-1234/chrome-linux64/chrome";

const browser = await chromium.launch({ headless: true, executablePath: CHROME });
const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
const page = await context.newPage();
const requests = [];
const probeTimings = [];
page.on("request", r => { if (r.url().includes("model-probes")) probeTimings.push({phase: "request", at: Date.now()}); });
page.on("response", r => { if (r.url().includes("model-probes")) probeTimings.push({phase: "response", status: r.status(), at: Date.now()}); });
page.on("requestfinished", async r => { if (r.url().includes("model-probes")) probeTimings.push({phase: "finished", timing: await r.timing()}); });
const consoleLines = [];
page.on("console", m => consoleLines.push(`${m.type()}: ${m.text()}`));
page.on("pageerror", e => consoleLines.push(`pageerror: ${e.message}`));
page.on("request", r => { if (r.url().includes("model-detection")) requests.push(r.method() + " " + r.url()); });
page.on("requestfailed", r => { if (r.url().includes("model-detection")) requests.push("FAILED " + r.url() + " " + r.failure()?.errorText); });

await page.goto(BASE, { waitUntil: "networkidle" });
await page.waitForSelector("#auth-form");
await page.fill('#auth-form input[name="username"]', "owner");
await page.fill('#auth-form input[name="password"]', "e2e-password-12345");
await page.click('#auth-form button[type="submit"]');
await page.waitForSelector('.sidebar nav button[data-view="models"]', { timeout: 15000 });
await page.click('.sidebar nav button[data-view="models"]');
await page.waitForSelector('button[data-dialog="model"]', { timeout: 15000 });
await page.click('button[data-dialog="model"]');
await page.waitForSelector("#model-dialog[open]");
const providerOptions = await page.$$eval("#model-provider option", os => os.map(o => o.value + ":" + o.textContent));
console.log("providers:", providerOptions);
const started = Date.now();
await page.click("#detect-models");
let rows = 0;
try {
  await page.waitForSelector(".detected-item", { timeout: 20000 });
  rows = await page.locator(".detected-item").count();
} catch (e) {
  console.log("no rows after 20s");
}
const status = await page.locator("#model-detect-progress").textContent().catch(() => "");
const stopPresent = await page.locator("#stop-model-detection").count();
const detectButtonHTML = await page.locator("#detect-models").innerHTML().catch(() => "");
console.log(JSON.stringify({ rows, elapsedMs: Date.now() - started, status, stopPresent, detectButtonHTML }));
await page.screenshot({ path: "/home/kaikai/kk-workspace/my_project/AHA2/tmp/e2e/detection-catalog.png", fullPage: true });

if (rows > 0) {
  const target = page.locator('.detected-item[data-model="gpt-6.1-terra"]');
  const count = await target.count();
  console.log("target rows:", count);
  const row = count ? target.first() : page.locator(".detected-item").first();
  const startedProbe = Date.now();
  await row.locator("[data-probe-model]").click();
  console.log("probe click ms", Date.now() - startedProbe);
  await page.waitForFunction(
    () => {
      const el = document.querySelector('.detected-item[data-model="gpt-6.1-terra"]') || document.querySelector(".detected-item");
      return el && el.querySelector(".proto-badges, .proto.none");
    },
    null,
    { timeout: 30000 },
  ).catch(() => console.log("probe did not render badges within 30s"));
  const badge = await row.locator(".proto-badges, .proto.none").first().textContent().catch(() => "");
  const title = await row.locator(".proto.none").first().getAttribute("title").catch(() => "");
  const probeButtonText = await row.locator("[data-probe-model]").textContent().catch(() => "");
  console.log(JSON.stringify({ probeElapsedMs: Date.now() - startedProbe, badge, title, probeButtonText, probeTimings }));
  await page.screenshot({ path: "/home/kaikai/kk-workspace/my_project/AHA2/tmp/e2e/detection-probe.png", fullPage: true });
}

console.log("requests:", requests);
console.log("console:", consoleLines.slice(-20));
await browser.close();
