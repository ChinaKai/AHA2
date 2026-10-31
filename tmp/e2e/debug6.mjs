import { createRequire } from "node:module";
const require = createRequire("/home/kaikai/kk-workspace/my_project/AHA2/.tools/web-validation/package.json");
const { chromium } = require("playwright");

const BASE = "http://127.0.0.1:18766";
const CHROME = "/home/kaikai/.cache/ms-playwright/chromium-1234/chrome-linux64/chrome";
const TABS = Number(process.env.TABS || 6);

const browser = await chromium.launch({ headless: true, executablePath: CHROME });
const context = await browser.newContext();
const pages = [];
for (let i = 0; i < TABS; i++) {
  const page = await context.newPage();
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
const page = pages[0];
await page.click('.sidebar nav button[data-view="models"]');
await page.waitForSelector('button[data-dialog="model"]');
await page.click('button[data-dialog="model"]');
await page.waitForSelector("#model-dialog[open]");
await page.evaluate(() => {
  const button = document.querySelector("#detect-models");
  window.__btn = button;
  window.__clicked = false;
  button?.addEventListener("click", () => { window.__clicked = true; });
});
await page.click("#detect-models");
await new Promise(r => setTimeout(r, 1500));
const state = await page.evaluate(() => ({
  sameButton: document.querySelector("#detect-models") === window.__btn,
  markerClicked: window.__clicked,
  disabled: document.querySelector("#detect-models")?.disabled,
  results: (document.querySelector("#model-detect-results")?.innerHTML || "").slice(0, 200),
  dialogOpen: Boolean(document.querySelector("#model-dialog")?.open),
  provider: document.querySelector("#model-provider")?.value,
}));
console.log(JSON.stringify(state));
await browser.close();
