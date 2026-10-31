import { createRequire } from "node:module";
const require = createRequire("/home/kaikai/kk-workspace/my_project/AHA2/.tools/web-validation/package.json");
const { chromium } = require("playwright");

const BASE = "http://127.0.0.1:18766";
const CHROME = "/home/kaikai/.cache/ms-playwright/chromium-1234/chrome-linux64/chrome";
const TABS = Number(process.env.TABS || 3);

const browser = await chromium.launch({ headless: true, executablePath: CHROME });
const context = await browser.newContext();
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
}
console.log(`holding ${TABS} tabs`);
await new Promise(r => setTimeout(r, 25000));
await browser.close();
