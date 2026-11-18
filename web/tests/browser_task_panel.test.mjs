import test from "node:test";
import assert from "node:assert/strict";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "..");
const moduleURL = pathToFileURL(resolve(root, "build/browser_task_panel.js"));
const panel = await import(moduleURL);

test("frame signatures distinguish changed previews and tolerate short frames", () => {
  const first = panel.browserFrameSignature(new Uint8Array([1, 2, 3, 4]));
  const second = panel.browserFrameSignature(new Uint8Array([1, 2, 3, 5]));
  assert.notEqual(first, second);
  assert.equal(panel.browserFrameSignature(new Uint8Array([1, 2, 3, 4])), first);
  assert.equal(panel.browserFrameSignature(new Uint8Array()), "0");
});

test("unchanged pages back off the frame rate without exceeding the cap", () => {
  let delay = panel.BROWSER_FRAME_MIN_DELAY_MS;
  const same = "10:1:2:3:4:5:6";
  for (let index = 0; index < 10; index++) {
    delay = panel.nextBrowserFrameDelay(same, same, delay);
  }
  assert.equal(delay, panel.BROWSER_FRAME_MAX_DELAY_MS);
  assert.ok(delay >= 1000, "an unchanged page should drop below 1 fps");
});

test("a changed page returns to the live frame rate immediately", () => {
  const delay = panel.nextBrowserFrameDelay("10:1:2:3:4:5:6", "10:1:2:9:4:5:6", panel.BROWSER_FRAME_MAX_DELAY_MS);
  assert.equal(delay, panel.BROWSER_FRAME_MIN_DELAY_MS);
  assert.equal(panel.nextBrowserFrameDelay("", "10:1:2:3:4:5:6", panel.BROWSER_FRAME_MAX_DELAY_MS), panel.BROWSER_FRAME_MIN_DELAY_MS);
});

test("bookmark folder labels keep only the first level below the Edge bar root", () => {
  assert.equal(panel.browserBookmarkFolderLabel("收藏夹栏/Docs"), "Docs");
  assert.equal(panel.browserBookmarkFolderLabel("收藏夹栏/Docs/HUALAI"), "Docs");
  assert.equal(panel.browserBookmarkFolderLabel("收藏夹栏/Docs/蓝牙"), "Docs");
  assert.equal(panel.browserBookmarkFolderLabel("收藏夹/Bar"), "Bar");
  assert.equal(panel.browserBookmarkFolderLabel("收藏夹/工作/日报"), "工作");
  assert.equal(panel.browserBookmarkFolderLabel("Favorites/Work"), "Work");
  assert.equal(panel.browserBookmarkFolderLabel("favourites/work"), "work");
  assert.equal(panel.browserBookmarkFolderLabel(" 收藏夹栏 ／ Docs / HUALAI "), "Docs");
  assert.equal(panel.browserBookmarkFolderLabel("Bar"), "Bar");
  assert.equal(panel.browserBookmarkFolderLabel(""), "");
  // A bare root path is the bar itself, not a folder.
  assert.equal(panel.browserBookmarkFolderLabel("收藏夹栏"), "");
  assert.equal(panel.browserBookmarkFolderLabel("收藏夹"), "");
  assert.equal(panel.browserBookmarkFolderLabel("Favorites"), "");
  assert.equal(panel.browserBookmarkFolderLabel("favourites/"), "");
});

test("bookmarks group under one first-level folder without losing subfolder entries", () => {
  const grouped = panel.groupBookmarks([
    {title: "文档中心", url: "https://docs.example/", folder: "收藏夹栏/Docs"},
    {title: "HUALAI", url: "https://hualai.example/", folder: "收藏夹栏/Docs/HUALAI"},
    {title: "蓝牙", url: "https://bluetooth.example/", folder: "收藏夹栏/Docs/蓝牙"},
    {title: "Bar", url: "https://bar.example/", folder: "收藏夹/Bar"},
    {title: "未归档", url: "https://unfiled.example/", folder: ""},
    {title: "根目录直达", url: "https://root.example/", folder: "收藏夹栏"},
  ]);
  assert.deepEqual([...grouped].map(([folder, items]) => [folder, items.map(bookmark => bookmark.title)]), [
    ["Docs", ["文档中心", "HUALAI", "蓝牙"]],
    ["Bar", ["Bar"]],
    ["", ["未归档", "根目录直达"]],
  ]);
});

test("tab selection follows a single real-window switch and leaves ambiguous windows alone", () => {
  const tabs = [
    {id: "a", active: false}, {id: "b", active: true},
  ];
  assert.equal(panel.browserFollowTabID(tabs, ""), "b");
  assert.equal(panel.browserFollowTabID(tabs, "a"), "b", "one active page wins");
  assert.equal(panel.browserFollowTabID(tabs, "a", false), "a", "manual selection is respected briefly");
  assert.equal(panel.browserFollowTabID([{id: "a", active: true}, {id: "b", active: true}], "a"), "a",
    "several active pages mean several windows; do not guess");
  assert.equal(panel.browserFollowTabID([{id: "a"}, {id: "b"}], "missing"), "a");
  assert.equal(panel.browserFollowTabID([], "a"), "");
});
