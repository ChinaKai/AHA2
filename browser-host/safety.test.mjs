import test from "node:test";
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";

const source = await readFile(new URL("./host.mjs", import.meta.url), "utf8");

test("host responses never embed Playwright diagnostic text", () => {
  // Raw Playwright messages can contain the user-data directory or Edge path.
  assert.doesNotMatch(source, /browser_launch_failed[\s\S]{0,80}Edge 启动失败：\$\{/);
  assert.doesNotMatch(source, /browser_navigation_failed[\s\S]{0,80}页面(打开|导航)失败：\$\{/);
  assert.doesNotMatch(source, /browser_snapshot_failed[\s\S]{0,80}读取页面失败：\$\{/);
  assert.match(source, /浏览器启动失败，请查看浏览器宿主本地日志/);
  assert.match(source, /diagnostics\(error, root, edge\)/);
});

test("host launch uses the double-checked profile directory", () => {
  assert.match(source, /const directory = profileDir\(root, profileID\)/);
  assert.match(source, /chromium\.launchPersistentContext\(directory,/);
  assert.match(source, /if \(!directory\) \{/);
});
