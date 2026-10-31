import { createRequire } from "node:module";
const require = createRequire("/home/kaikai/kk-workspace/my_project/AHA2/.tools/web-validation/package.json");
const { chromium } = require("playwright");

const BASE = "http://127.0.0.1:18766";
const CHROME = "/home/kaikai/.cache/ms-playwright/chromium-1234/chrome-linux64/chrome";
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));

const browser = await chromium.launch({ headless: true, executablePath: CHROME });
const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
const page = await context.newPage();
page.on("pageerror", e => console.log("pageerror", e.message));

async function delayState(route) {
  await sleep(900);
  await route.continue();
}
await page.route("**/api/v1/assistant", delayState);
await page.route("**/api/v1/pet", delayState);

async function petState() {
  return page.evaluate(() => {
    const pet = document.querySelector("#assistant-pet");
    if (!pet) return null;
    const img = pet.querySelector(".pet-img");
    return {
      classes: pet.className,
      visibility: getComputedStyle(pet).visibility,
      character: pet.dataset.character || "",
      image: img ? img.getAttribute("src") : "",
    };
  });
}

await page.goto(BASE, { waitUntil: "domcontentloaded" });
await page.waitForSelector("#auth-form");
await page.fill('#auth-form input[name="username"]', "owner");
await page.fill('#auth-form input[name="password"]', "e2e-password-12345");
await page.click('#auth-form button[type="submit"]');
await page.waitForSelector("#assistant-pet", { state: "attached", timeout: 15000 });
await sleep(250);
const firstLoading = await petState();

await page.waitForFunction(() => {
  const pet = document.querySelector("#assistant-pet");
  return pet && getComputedStyle(pet).visibility !== "hidden";
}, null, { timeout: 8000 });
const firstVisible = await petState();
const cached = await page.evaluate(() => localStorage.getItem("aha2.pet.character.v1"));

await page.reload({ waitUntil: "domcontentloaded" });
await page.waitForSelector("#assistant-pet", { state: "attached", timeout: 15000 });
await sleep(150);
const reloadEarly = await petState();
await sleep(1500);
const reloadSettled = await petState();

console.log(JSON.stringify({ firstLoading, firstVisible, cached, reloadEarly, reloadSettled }, null, 1));
await browser.close();
