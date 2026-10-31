import { createRequire } from "node:module";
const require = createRequire("/home/kaikai/kk-workspace/my_project/AHA2/.tools/web-validation/package.json");
const { chromium } = require("playwright");

const BASE = "http://127.0.0.1:18766";
const CHROME = "/home/kaikai/.cache/ms-playwright/chromium-1234/chrome-linux64/chrome";

const browser = await chromium.launch({ headless: true, executablePath: CHROME });
const context = await browser.newContext();
const page = await context.newPage();
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
await page.waitForFunction(() => (document.querySelector("#model-provider")?.value || "").length > 0, null, { timeout: 10000 });

// Never answer the catalog request: the client-side deadline must surface an error.
await page.route("**/api/v1/providers/detect-models", () => {});
const started = Date.now();
await page.click("#detect-models");
await page.waitForFunction(
  () => (document.querySelector("#model-detect-progress")?.textContent || "").includes("失败"),
  null,
  { timeout: 30000 },
);
const status = await page.locator("#model-detect-progress").textContent();
const enabled = await page.locator("#detect-models").isEnabled();
console.log(JSON.stringify({ elapsedMs: Date.now() - started, status, enabled }));
await browser.close();
