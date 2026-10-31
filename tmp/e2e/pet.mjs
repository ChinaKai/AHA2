import { createRequire } from "node:module";
const require = createRequire("/home/kaikai/kk-workspace/my_project/AHA2/.tools/web-validation/package.json");
const { chromium } = require("playwright");

const BASE = "http://127.0.0.1:18766";
const CHROME = "/home/kaikai/.cache/ms-playwright/chromium-1234/chrome-linux64/chrome";

const browser = await chromium.launch({ headless: true, executablePath: CHROME });
const context = await browser.newContext();
const page = await context.newPage();
const streams = [];
page.on("request", r => { if (r.url().includes("/api/v1/pet/events") || r.url().endsWith("/api/v1/events")) streams.push(r.url()); });
await page.goto(BASE, { waitUntil: "domcontentloaded" });
await page.waitForSelector("#auth-form");
await page.fill('#auth-form input[name="username"]', "owner");
await page.fill('#auth-form input[name="password"]', "e2e-password-12345");
await page.click('#auth-form button[type="submit"]');
await page.waitForSelector("#assistant-pet", { state: "attached", timeout: 15000 });
await new Promise(r => setTimeout(r, 1500));
const before = await page.locator("#assistant-pet").getAttribute("data-mood");
await new Promise(r => setTimeout(r, 6000));
const after = await page.locator("#assistant-pet").getAttribute("data-mood");
console.log(JSON.stringify({ before, after, petStreams: streams.filter(u => u.includes("pet/events")).length, globalStreams: streams.filter(u => u.endsWith("/api/v1/events")).length }));
await browser.close();
