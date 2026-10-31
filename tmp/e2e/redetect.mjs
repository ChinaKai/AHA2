import { createRequire } from "node:module";
const require = createRequire("/home/kaikai/kk-workspace/my_project/AHA2/.tools/web-validation/package.json");
const { chromium } = require("playwright");

const BASE = "http://127.0.0.1:18766";
const CHROME = "/home/kaikai/.cache/ms-playwright/chromium-1234/chrome-linux64/chrome";

const browser = await chromium.launch({ headless: true, executablePath: CHROME });
const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
const page = await context.newPage();
let detects = 0;
let probes = 0;
page.on("request", r => {
  if (r.url().includes("/providers/detect-models")) detects++;
  if (r.url().includes("/model-probes")) probes++;
});
await page.goto(BASE, { waitUntil: "domcontentloaded" });
await page.waitForSelector("#auth-form");
await page.fill('#auth-form input[name="username"]', "owner");
await page.fill('#auth-form input[name="password"]', "e2e-password-12345");
await page.click('#auth-form button[type="submit"]');
await page.waitForSelector('.sidebar nav button[data-view="models"]', { timeout: 15000 });
await page.click('.sidebar nav button[data-view="models"]');
await page.waitForSelector('button[data-dialog="model"]');
await page.click('button[data-dialog="model"]');
await page.waitForSelector("#model-dialog[open]");
await page.click("#detect-models");
await page.waitForSelector(".detected-item", { timeout: 15000 });
const first = await page.locator(".detected-item").count();
await page.click("#detect-models");
await new Promise(r => setTimeout(r, 1500));
const second = await page.locator(".detected-item").count();
const status = await page.locator("#model-detect-progress").textContent();
await page.locator('.detected-item[data-model="gpt-6.1-terra"] [data-probe-model]').click();
await page.waitForFunction(
  () => Boolean(document.querySelector('.detected-item[data-model="gpt-6.1-terra"] .proto-badges, .detected-item[data-model="gpt-6.1-terra"] .proto.none')),
  null,
  { timeout: 30000, polling: 200 },
);
console.log(JSON.stringify({ first, second, status, detects, probes }));
await browser.close();
