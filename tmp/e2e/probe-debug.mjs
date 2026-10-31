import { createRequire } from "node:module";
const require = createRequire("/home/kaikai/kk-workspace/my_project/AHA2/.tools/web-validation/package.json");
const { chromium } = require("playwright");

const BASE = "http://127.0.0.1:18766";
const CHROME = "/home/kaikai/.cache/ms-playwright/chromium-1234/chrome-linux64/chrome";

const browser = await chromium.launch({ headless: true, executablePath: CHROME });
const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
const page = await context.newPage();
page.on("console", m => console.log("console", m.type(), m.text()));
page.on("pageerror", e => console.log("pageerror", e.message));
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
await page.waitForSelector(".detected-item", { timeout: 20000 });
await page.evaluate(() => {
  window.__events = [];
  const results = document.querySelector("#model-detect-results");
  new MutationObserver(records => {
    window.__events.push(["mutation", Date.now(), records.length, records[0]?.type || "", records[0]?.target?.className || records[0]?.target?.nodeName || ""]);
  }).observe(results, { childList: true, subtree: true, attributes: true, attributeFilter: ["disabled"] });
  const original = window.fetch;
  window.fetch = async (...args) => {
    const url = String(args[0]);
    window.__events.push(["fetch-start", Date.now(), url]);
    const response = await original(...args);
    window.__events.push(["fetch-response", Date.now(), url, response.status]);
    return response;
  };
});
const row = page.locator('.detected-item[data-model="gpt-6.1-terra"]');
await row.locator("[data-probe-model]").click();
const started = Date.now();
await new Promise(r => setTimeout(r, 15000));
const info = await page.evaluate(() => ({
  events: window.__events,
  rowHTML: (document.querySelector('.detected-item[data-model="gpt-6.1-terra"]')?.outerHTML || "").slice(0, 500),
}));
console.log("elapsed", Date.now() - started);
console.log(JSON.stringify(info, null, 1));
await browser.close();
