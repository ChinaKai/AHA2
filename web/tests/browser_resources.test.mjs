import test from "node:test";
import assert from "node:assert/strict";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "..");
const moduleURL = pathToFileURL(resolve(root, "build/browser_resources.js"));
const browser = await import(moduleURL);

const profile = overrides => ({
  id: "browser_profile_1", name: "工作浏览器", browser_kind: "edge", host_id: "local",
  state: "idle", host_available: true, created_at: "2026-11-17T00:00:00Z",
  updated_at: "2026-11-17T00:00:00Z", ...overrides,
});
const session = overrides => ({
  id: "browser_session_1", profile_id: "browser_profile_1", host_id: "local",
  state: "ready", started_at: "2026-11-17T00:00:00Z", created_at: "2026-11-17T00:00:00Z",
  updated_at: "2026-11-17T00:00:00Z", ...overrides,
});

test("browser profile states use the owner-facing vocabulary", () => {
  assert.equal(browser.browserProfileStateLabel("idle"), "未初始化");
  assert.equal(browser.browserProfileStateLabel("auth_required"), "等待登录");
  assert.equal(browser.browserProfileStateLabel("error"), "异常");
  assert.equal(browser.browserSessionStateLabel("busy"), "使用中");
  assert.equal(browser.browserSessionStateLabel("closed"), "已关闭");
});

test("resource rows offer login while idle and close while a session is active", () => {
  const idle = browser.renderBrowserResources({profiles: [profile()], host: null, loading: false, error: ""});
  assert.match(idle, /data-browser-profile-login="browser_profile_1"/);
  assert.doesNotMatch(idle, /data-browser-profile-close=/);
  assert.match(idle, /Microsoft Edge · 宿主 local/);
  assert.match(idle, /未初始化/);

  const active = browser.renderBrowserResources({
    profiles: [profile({state: "ready", session: session()})],
    host: {available: true, host_id: "local", edge_available: true, version: "0.1.0", platform: "win32"},
    loading: false, error: "",
  });
  assert.match(active, /data-browser-profile-close="browser_profile_1"/);
  assert.doesNotMatch(active, /data-browser-profile-login=/);
  assert.match(active, /会话 可用/);
  assert.match(active, /宿主 local · Edge 可用/);
  // The real profile directory is host-owned and must never appear in the UI.
  assert.doesNotMatch(active, /User Data|AppData|ProgramData|C:\\\\Users/);
});

test("host failures are explicit and never suggest a browser fallback", () => {
  const unavailable = browser.renderBrowserHostBanner({
    available: false, host_id: "local", edge_available: false, code: "browser_host_unavailable",
    error: "浏览器宿主不可达",
  });
  assert.match(unavailable, /浏览器宿主不可用/);
  assert.match(unavailable, /不会换用其他浏览器/);

  const noEdge = browser.renderBrowserHostBanner({available: true, host_id: "local", edge_available: false});
  assert.match(noEdge, /未找到 Microsoft Edge/);
  assert.match(noEdge, /不会静默换用 Chrome\/Chromium/);

  const ready = browser.renderBrowserHostBanner({available: true, host_id: "local", edge_available: true, version: "0.1.0"});
  assert.match(ready, /Edge 可用/);
  assert.doesNotMatch(ready, /error/);
});

test("profile options and session status explain the selected profile", () => {
  const options = browser.browserProfileOptions([
    profile(),
    profile({id: "browser_profile_2", name: "日志平台", state: "ready"}),
  ], "browser_profile_2");
  assert.match(options, /value="browser_profile_2" selected/);
  assert.match(options, /工作浏览器 · 未初始化/);
  assert.match(options, /日志平台 · 可用/);

  assert.equal(browser.browserSessionStatusText(null), "尚未附加会话");
  assert.equal(browser.browserSessionStatusText(session({host_reachable: false})), "会话 可用 · 宿主不可达");
  assert.equal(browser.browserSessionStatusText(null, {available: true, host_id: "local", edge_available: false}),
    "浏览器宿主在线，但未找到 Microsoft Edge");
});

test("deleting from the resource sheet asks for explicit confirmation", async () => {
  const {readFile} = await import("node:fs/promises");
  const source = await readFile(resolve(root, "src/browser_resources.ts"), "utf8");
  assert.match(source, /if \(!window\.confirm\(`删除浏览器 Profile/);
  assert.match(source, /服务端会拒绝删除；请先关闭会话/);
  assert.match(source, /真实用户数据目录不会被 AHA 删除/);
});

test("shared control switches to the built-in browser and hides the display picker", async () => {
  const {readFile} = await import("node:fs/promises");
  const panel = await readFile(resolve(root, "src/desktop_panel.ts"), "utf8");
  assert.match(panel, /id="desktop-control-mode"/);
  assert.match(panel, /<option value="browser">内置浏览器<\/option>/);
  // Browser mode hides the whole display target block, including the monitor
  // dropdown, because a browser instance is not bound to a physical display.
  assert.match(panel, /const displayTarget = root\.querySelector<HTMLElement>\("#desktop-display-target"\)/);
  assert.match(panel, /displayTarget\.hidden = state\.browserMode/);
  assert.match(panel, /import\("\.\/browser_task_panel\.js"\)/);
  const styles = await readFile(resolve(root, "src/styles.css"), "utf8");
  assert.match(styles, /\.desktop-tool\.browser-mode #desktop-picture-controls/);
});
