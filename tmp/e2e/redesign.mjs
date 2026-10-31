import { createRequire } from "node:module";
const require = createRequire("/home/kaikai/kk-workspace/my_project/AHA2/.tools/web-validation/package.json");
const { chromium } = require("playwright");

const BASE = "http://127.0.0.1:18766";
const CHROME = "/home/kaikai/.cache/ms-playwright/chromium-1234/chrome-linux64/chrome";

const browser = await chromium.launch({ headless: true, executablePath: CHROME });
const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
const page = await context.newPage();
const probes = [];
page.on("request", r => { if (r.url().includes("model-probes")) probes.push(r.url()); });
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
const detectLabel = await page.locator("#detect-models").textContent();
await page.click("#detect-models");
await page.waitForSelector(".detected-item", { timeout: 20000 });
const total = await page.locator(".detected-item").count();

// Fuzzy search: both tokens, any position, and counts.
await page.fill("#detect-search", "gpt terra");
await new Promise(r => setTimeout(r, 200));
const matchCount = await page.locator("#detect-match-count").textContent();
const visible = await page.locator('.detected-item:visible').count();
const visibleIds = await page.$$eval('.detected-item:visible', rows => rows.map(row => row.dataset.model));
const fuzzyOk = visibleIds.every(id => id.toLowerCase().includes("gpt") && id.toLowerCase().includes("terra"));
await page.fill("#detect-search", "");
await new Promise(r => setTimeout(r, 200));

// Multi-select an unprobed row and open the add dialog.
const firstRow = page.locator('.detected-item[data-model="codex-gpt-5.6-terra"]');
await firstRow.locator("strong").click();
const checked = await firstRow.locator(".detected-model").isChecked();
const addDisabled = await page.locator("#open-model-add").isDisabled();
const addLabel = await page.locator("#open-model-add").textContent();
await page.locator('.detected-item[data-model="claude-kimi-k3"] strong').click();
const selectedCount = await page.locator("#detect-selected-count").textContent();
await page.click("#open-model-add");
await page.waitForSelector("#model-add-dialog[open]");
const backendValue = await page.locator("#model-add-backend").inputValue();
const dshWires = await page.$$eval("#model-add-wire option", os => os.map(o => o.value));
await page.selectOption("#model-add-backend", "claude");
const claudeWires = await page.$$eval("#model-add-wire option", os => os.map(o => o.value));
await page.selectOption("#model-add-backend", "dsh");
await page.selectOption("#model-add-wire", "responses");
const summaryBefore = await page.locator("#model-add-summary").textContent();
await page.click("#confirm-model-add");
await page.waitForFunction(() => !document.querySelector("#model-dialog")?.open, null, { timeout: 60000 }).catch(() => {});
await new Promise(r => setTimeout(r, 500));
const notice = await page.locator(".banner.success").textContent().catch(() => "");
const models = await page.evaluate(async () => {
  const response = await fetch("/api/v1/models");
  return (await response.json()).models.map(m => ({name: m.display_name, backend: m.backend, wire: m.wire_api, context_window: m.context_window}));
});

console.log(JSON.stringify({
  detectLabel, total, matchCount, visible, fuzzyOk, checked, addDisabled, addLabel,
  selectedCount, backendValue, dshWires, claudeWires, summaryBefore, notice,
  probes: probes.length,
  added: models.filter(m => m.name === "codex-gpt-5.6-terra" || m.name === "claude-kimi-k3"),
  contextOk: models.some(m => m.name === "claude-kimi-k3" && (m.context_window || 0) >= 1000000),
}, null, 1));
await page.screenshot({ path: "/home/kaikai/kk-workspace/my_project/AHA2/tmp/e2e/redesign.png", fullPage: true });
await browser.close();
