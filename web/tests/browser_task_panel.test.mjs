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

test("only bookmarks sitting on the bar root count as bookmark-bar level", () => {
  const bookmark = folder => ({title: "t", url: "https://x.example/", folder});
  for (const folder of ["", "收藏夹栏", "收藏夹", "Favorites", "favourites", "favourites/", " 收藏夹栏 ／ "]) {
    assert.equal(panel.isBookmarkBarLevel(bookmark(folder)), true, `"${folder}" is the bar itself`);
  }
  // Anything inside a first-level folder, or deeper, belongs to that folder.
  for (const folder of ["收藏夹栏/Work", "收藏夹/Work/网易邮箱", "收藏夹栏/Docs/HUALAI", "Bar", "收藏夹栏/Work/"]) {
    assert.equal(panel.isBookmarkBarLevel(bookmark(folder)), false, `"${folder}" is not on the bar`);
  }
});

test("the overflow menu lists hidden first-level folders plus bar-level urls only", () => {
  const bookmark = (folder, title) => ({title, url: `https://${title}.example/`, folder});
  const bookmarks = [
    bookmark("收藏夹栏", "根目录直达"),
    bookmark("收藏夹/", "另一根"),
    bookmark("收藏夹栏/Work", "Work 门户"),
    bookmark("收藏夹栏/Work/网易邮箱", "网易邮箱"),
    bookmark("收藏夹栏/Tools", "工具"),
  ];
  const hidden = panel.bookmarkOverflowEntries(bookmarks, ["Work"]);
  assert.deepEqual(hidden.folders.map(([folder, group]) => [folder, group.map(item => item.title)]), [
    ["Work", ["Work 门户", "网易邮箱"]],
  ], "a hidden first-level folder carries its whole subtree");
  assert.deepEqual(hidden.urls.map(item => item.title), ["根目录直达", "另一根"],
    "folder bookmarks never leak into the bar-level url list");

  // A visible folder must not be repeated in the overflow menu.
  const withVisible = panel.bookmarkOverflowEntries(bookmarks, ["Work"]);
  assert.equal(withVisible.folders.some(([folder]) => folder === "Tools"), false);

  // No hidden folder means no folder section at all - never a fallback that
  // turns the chevron into a second copy of the whole tree.
  const noneHidden = panel.bookmarkOverflowEntries(bookmarks, []);
  assert.deepEqual(noneHidden.folders, []);
  assert.deepEqual(noneHidden.urls.map(item => item.title), ["根目录直达", "另一根"]);

  assert.deepEqual(panel.bookmarkOverflowEntries([], []), {folders: [], urls: []});
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

test("one focused page wins over several active pages from other windows", () => {
  const tabs = [
    {id: "a", active: true, focused: false},
    {id: "b", active: true, focused: true},
  ];
  assert.equal(panel.browserFollowTabID(tabs, "a"), "b", "the foreground window wins");
  assert.equal(panel.browserFollowTabID(tabs, ""), "b");
  assert.equal(panel.browserFollowTabID(tabs, "a", false), "a", "manual selection is respected briefly");
});

test("a focused change moves the selection and the preview stream key", () => {
  const before = [
    {id: "a", active: true, focused: true},
    {id: "b", active: true, focused: false},
  ];
  const after = before.map(tab => ({...tab, focused: tab.id === "b"}));
  const next = panel.browserFollowTabID(after, "a");
  assert.equal(panel.browserFollowTabID(before, "a"), "a");
  assert.equal(next, "b", "selection follows the new foreground window");
  assert.notEqual(panel.browserPreviewStreamKey("browser_grant_1", "a"),
    panel.browserPreviewStreamKey("browser_grant_1", next),
    "a new tab id must start a new preview stream");
});

test("without a focused page the single active page still wins, ambiguity never guesses", () => {
  assert.equal(panel.browserFollowTabID([{id: "a", active: true}, {id: "b", active: false}], "b"), "a");
  assert.equal(panel.browserFollowTabID([{id: "a", active: true}, {id: "b", active: true}], "b"), "b",
    "several active pages without focus stay ambiguous");
});

test("several focused pages still fall back to the single active page", () => {
  // Review case: a and b are both focused, only b is active -> b wins.
  assert.equal(panel.browserFollowTabID([
    {id: "a", focused: true},
    {id: "b", focused: true, active: true},
  ], "a"), "b");
  // Several focused and several active is still ambiguous: keep the selection.
  assert.equal(panel.browserFollowTabID([
    {id: "a", focused: true, active: true},
    {id: "b", focused: true, active: true},
  ], "a"), "a");
  // Several focused with no active page cannot pick a window either.
  assert.equal(panel.browserFollowTabID([{id: "a", focused: true}, {id: "b", focused: true}], "b"), "b");
});

test("clicks map through the letterboxed image and the frame's CSS viewport", () => {
  // A 1280x800 page shown in a 640x480 stage: scale .5, content 640x400,
  // letterboxed 40px at the top and bottom.
  const rect = {left: 100, top: 200, width: 640, height: 480};
  const center = panel.browserViewportPoint(420, 440, rect, 1280, 800, 1280, 800);
  assert.deepEqual(center, {x: 640, y: 400});
  // The letterbox bars are not part of the page.
  assert.equal(panel.browserViewportPoint(420, 210, rect, 1280, 800, 1280, 800), null);
  assert.equal(panel.browserViewportPoint(90, 440, rect, 1280, 800, 1280, 800), null);
  // The far content corner clamps to the last page pixel, never one past it.
  assert.deepEqual(panel.browserViewportPoint(740, 640, rect, 1280, 800, 1280, 800), {x: 1279, y: 799});
});

test("a device scale factor cancels out because only the image/viewport ratio is used", () => {
  // A 2560x1600 screenshot of a 1280x800 CSS viewport displayed at 640x400.
  const rect = {left: 0, top: 0, width: 640, height: 400};
  assert.deepEqual(panel.browserViewportPoint(320, 200, rect, 2560, 1600, 1280, 800), {x: 640, y: 400});
  assert.deepEqual(panel.browserViewportPoint(160, 100, rect, 2560, 1600, 1280, 800), {x: 320, y: 200});
  // Missing viewport metadata falls back to the image's own pixel size.
  assert.deepEqual(panel.browserViewportPoint(160, 100, rect, 1280, 800, 0, 0), {x: 320, y: 200});
  assert.equal(panel.browserViewportPoint(0, 0, {left: 0, top: 0, width: 0, height: 0}, 1280, 800, 1280, 800), null);
});

test("keyboard mapping keeps printable input as text and forwards real shortcuts", () => {
  assert.deepEqual(panel.browserKeyCommand({key: "a"}), {kind: "text", text: "a"});
  assert.deepEqual(panel.browserKeyCommand({key: "你"}), {kind: "text", text: "你"});
  assert.deepEqual(panel.browserKeyCommand({key: "A", shiftKey: true}), {kind: "text", text: "A"});
  assert.deepEqual(panel.browserKeyCommand({key: " "}), {kind: "text", text: " "});
  assert.deepEqual(panel.browserKeyCommand({key: "Enter"}), {kind: "key", keys: ["Enter"]});
  assert.deepEqual(panel.browserKeyCommand({key: "Tab", shiftKey: true}), {kind: "key", keys: ["Shift", "Tab"]});
  assert.deepEqual(panel.browserKeyCommand({key: "ArrowLeft", altKey: true}), {kind: "key", keys: ["Alt", "ArrowLeft"]});
  assert.deepEqual(panel.browserKeyCommand({key: "l", ctrlKey: true, shiftKey: true}),
    {kind: "key", keys: ["Control", "Shift", "l"]});
  assert.deepEqual(panel.browserKeyCommand({key: "/", ctrlKey: true}), {kind: "key", keys: ["Control", "/"]});
  assert.deepEqual(panel.browserKeyCommand({key: "F5"}), {kind: "key", keys: ["F5"]});
  // IME composing events never leak as raw key presses.
  assert.equal(panel.browserKeyCommand({key: "Enter", isComposing: true}), null);
  assert.equal(panel.browserKeyCommand({key: "Process"}), null);
  // Win/Meta and the OS secure attention sequence are never forwarded.
  assert.equal(panel.browserKeyCommand({key: "l", metaKey: true}), null);
  assert.equal(panel.browserKeyCommand({key: "Delete", ctrlKey: true, altKey: true}), null);
  assert.equal(panel.browserKeyCommand({key: "Control"}), null);
});

test("text is chunked on UTF-8 boundaries and newlines become Enter presses", () => {
  assert.equal(panel.browserTextByteLength("你好"), 6);
  assert.deepEqual(panel.browserTextActions("你好\nworld"),
    [{kind: "text", text: "你好"}, {kind: "key", keys: ["Enter"]}, {kind: "text", text: "world"}]);
  assert.deepEqual(panel.browserTextActions("a\n"),
    [{kind: "text", text: "a"}, {kind: "key", keys: ["Enter"]}]);
  assert.deepEqual(panel.browserTextActions(""), []);
  const chunks = panel.splitBrowserText("你".repeat(3000), 4096);
  assert.equal(chunks.length, 3);
  for (const chunk of chunks) assert.ok(panel.browserTextByteLength(chunk) <= 4096);
  assert.equal(chunks.join(""), "你".repeat(3000));
  assert.deepEqual(panel.browserTextActions("x", 4096), [{kind: "text", text: "x"}]);
});

test("scroll steps stay inside the host's per-action budget without dropping distance", () => {
  assert.equal(panel.browserScrollStep(120), 120);
  assert.equal(panel.browserScrollStep(9000), 4000);
  assert.equal(panel.browserScrollStep(-9000), -4000);
  assert.equal(panel.browserScrollStep(Number.NaN), 0);
  assert.equal(panel.browserScrollStep(12.9), 12);
});
