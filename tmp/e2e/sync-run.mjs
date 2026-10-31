import { createRequire } from "node:module";
const require = createRequire("/home/kaikai/kk-workspace/my_project/AHA2/.tools/web-validation/package.json");
const { chromium } = require("playwright");

const BASE = "http://127.0.0.1:18766";
const CHROME = "/home/kaikai/.cache/ms-playwright/chromium-1234/chrome-linux64/chrome";

const browser = await chromium.launch({ headless: true, executablePath: CHROME });
const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
const page = await context.newPage();
let previews = 0;
page.on("request", r => { if (r.url().includes("/api/v1/settings/sync/preview")) previews++; });
page.on("pageerror", e => console.log("pageerror", e.message));

async function login() {
  await page.goto(BASE, { waitUntil: "domcontentloaded" });
  const form = page.locator("#auth-form");
  const nav = page.locator('#owner-avatar');
  await Promise.race([
    form.waitFor({ state: "visible", timeout: 15000 }).catch(() => {}),
    nav.waitFor({ state: "visible", timeout: 15000 }).catch(() => {}),
  ]);
  if (await form.isVisible()) {
    await page.fill('#auth-form input[name="username"]', "owner");
    await page.fill('#auth-form input[name="password"]', "e2e-password-12345");
    await page.click('#auth-form button[type="submit"]');
  }
  await nav.waitFor({ state: "visible", timeout: 15000 });
}

async function openSync() {
  for (let i = 0; i < 5; i++) await page.click("#owner-avatar");
  await page.waitForSelector('button[data-view="sync"]', { timeout: 15000 });
  await page.click('button[data-view="sync"]');
  await page.waitForSelector("#run-sync", { timeout: 15000 });
}

await login();
await openSync();
const initialHeadline = await page.locator(".sync-status-pill").textContent();
const previewsOnLoad = previews;
const nextRunText = await page.locator(".sync-summary-grid > div").nth(1).textContent();

await page.click("#run-sync");
await page.waitForSelector("#sync-run-progress:not([hidden])", { timeout: 10000 });
await page.waitForFunction(() => (document.querySelector("[data-sync-phase]")?.textContent || "").length > 0, null, { timeout: 10000 });
const runningPhase = await page.locator("[data-sync-phase]").textContent();
const runningDetail = await page.locator("[data-sync-detail]").textContent();

// Reload while the run is still in progress: it must continue on the server and
// the page must reattach to the same run.
await page.reload({ waitUntil: "domcontentloaded" });
await page.waitForSelector('#owner-avatar', { timeout: 15000 }).catch(async () => {
  await login();
});
await openSync();
await page.waitForSelector("#sync-run-progress", { timeout: 10000 });
const reattached = await page.locator("#sync-run-progress").isVisible();
const reattachedPhase = await page.locator("[data-sync-phase]").textContent().catch(() => "");

await page.waitForFunction(() => {
  const pill = document.querySelector(".sync-status-pill");
  return pill && (pill.textContent || "").includes("已同步");
}, null, { timeout: 30000 }).catch(() => {});
const finalHeadline = await page.locator(".sync-status-pill").textContent();
const lastSuccess = await page.locator(".sync-summary-grid > div").first().textContent();
const progressHidden = await page.locator("#sync-run-progress").isHidden().catch(() => true);

const previewsBefore = previews;
await page.click("#preview-sync");
await new Promise(r => setTimeout(r, 1500));
const previewsAfter = previews;

console.log(JSON.stringify({
  initialHeadline, previewsOnLoad, nextRunText,
  runningPhase, runningDetail, reattached, reattachedPhase,
  finalHeadline, lastSuccess, progressHidden,
  previewsBefore, previewsAfter,
}, null, 1));
await page.screenshot({ path: "/home/kaikai/kk-workspace/my_project/AHA2/tmp/e2e/sync-page.png", fullPage: true });
await browser.close();
