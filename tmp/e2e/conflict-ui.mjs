import { createRequire } from "node:module";
const require = createRequire("/home/kaikai/kk-workspace/my_project/AHA2/.tools/web-validation/package.json");
const { chromium } = require("playwright");

const BASE = "http://127.0.0.1:18766";
const CHROME = "/home/kaikai/.cache/ms-playwright/chromium-1234/chrome-linux64/chrome";

const browser = await chromium.launch({ headless: true, executablePath: CHROME });
const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
const page = await context.newPage();
page.on("pageerror", e => console.log("pageerror", e.message));

await page.goto(BASE, { waitUntil: "domcontentloaded" });
await page.waitForSelector("#auth-form");
await page.fill('#auth-form input[name="username"]', "owner");
await page.fill('#auth-form input[name="password"]', "e2e-password-12345");
await page.click('#auth-form button[type="submit"]');
await page.waitForSelector('#owner-avatar', { timeout: 15000 });
for (let i = 0; i < 5; i++) await page.click("#owner-avatar");
await page.waitForSelector('button[data-view="sync"]', { timeout: 15000 });
await page.click('button[data-view="sync"]');
await page.waitForSelector("#run-sync", { timeout: 15000 });
await page.waitForSelector(".sync-conflict-card", { timeout: 15000 });
const initialCards = await page.locator(".sync-conflict-card").count();
console.log("cards", JSON.stringify(await page.locator(".sync-conflict-card").allTextContents()));

const projectCard = page.locator(".sync-conflict-card", { hasText: "Remote Name" });
await projectCard.locator("[data-sync-conflict]").click();
await page.waitForSelector("#sync-conflict-dialog[open]");
const projectDetail = await page.locator("#sync-conflict-detail").textContent();
const localEnabled = await page.locator('[data-sync-resolution="local"]').isEnabled();
const remoteEnabled = await page.locator('[data-sync-resolution="remote"]').isEnabled();
await page.click('[data-sync-resolution="local"]');
await page.waitForFunction(() => !document.querySelector("#sync-conflict-dialog")?.open, null, { timeout: 20000 });
await page.waitForFunction(() => document.querySelectorAll(".sync-conflict-card").length === 1, null, { timeout: 20000 });
const afterLocal = await page.locator(".sync-conflict-card").count();
const localNotice = await page.locator(".banner.success").textContent().catch(() => "");

const knowledgeCard = page.locator(".sync-conflict-card", { hasText: "远端知识" });
await knowledgeCard.locator("[data-sync-conflict]").click();
await page.waitForSelector("#sync-conflict-dialog[open]");
const knowledgeDetail = await page.locator("#sync-conflict-detail").textContent();
const knowledgeLocalEnabled = await page.locator('[data-sync-resolution="local"]').isEnabled();
const knowledgeRemoteEnabled = await page.locator('[data-sync-resolution="remote"]').isEnabled();
await page.click('[data-sync-resolution="ignore"]');
await page.waitForFunction(() => document.querySelectorAll(".sync-conflict-card").length === 0, null, { timeout: 20000 });
const remaining = await page.evaluate(async () => {
  const response = await fetch("/api/v1/settings/sync/conflicts");
  const body = await response.json();
  return (body.conflicts || []).length;
});

console.log(JSON.stringify({
  initialCards, projectDetail: projectDetail.slice(0, 300), localEnabled, remoteEnabled,
  afterLocal, localNotice, knowledgeDetail: knowledgeDetail.slice(0, 300),
  knowledgeLocalEnabled, knowledgeRemoteEnabled, remaining,
}, null, 1));
await page.screenshot({ path: "/home/kaikai/kk-workspace/my_project/AHA2/tmp/e2e/conflict-ui.png", fullPage: true });
await browser.close();
