import test from "node:test";
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const webRoot = resolve(import.meta.dirname, "..");
const palette = await import(pathToFileURL(resolve(webRoot, "build", "command_palette.js")).href);

function commandItem(kind, id, label, detail, target, keywords = []) {
  return {id: `${kind}:${id}`, kind, label, detail, target, keywords};
}

function sampleItems() {
  return [
    commandItem("task", "task-352", "AHA2 Web：全局搜索/命令面板", "task-352 · AHA2 · AHA2-BUILDER-SERVER · 执行中", "task-352", ["执行中", "active"]),
    commandItem("task", "task-101", "模型健康检测", "task-101 · AHA2 · AHA2-BUILDER-SERVER · 已完成", "task-101", ["已完成"]),
    commandItem("project", "project-aha2", "AHA2", "项目", "project-aha2", ["project"]),
    commandItem("workspace", "ws-builder", "AHA2-BUILDER-SERVER", "Workspace · AHA2 · /srv/build", "project-aha2", ["workspace"]),
    commandItem("page", "models", "模型", "模型列表与健康检测", "models", ["model"]),
    commandItem("action", "new-task", "新建任务", "打开创建任务面板", "new-task", ["create"]),
  ];
}

test("executing an item drops composer focus before navigation starts", async () => {
  const calls = [];
  let releaseNavigation;
  const navigation = new Promise(resolve => {
    releaseNavigation = resolve;
  });
  const running = palette.runPaletteSelection({
    closePalette(restorePreviousFocus) {
      calls.push(`close:${restorePreviousFocus}`);
    },
    focusTrigger() {
      calls.push("focus");
    },
    navigate() {
      calls.push("navigate");
      return navigation;
    },
  });
  // This is the regression: previously the palette closed with focus restored
  // to the old Task composer, and only then openTask changed state.
  assert.deepEqual(calls, ["close:false", "focus", "navigate"]);
  releaseNavigation();
  await running;
  assert.deepEqual(calls, ["close:false", "focus", "navigate"]);
});

test("Ctrl+K and Cmd+K open the palette while plain K does not", () => {
  assert.equal(palette.isPaletteShortcut({key: "k", ctrlKey: true}), true);
  assert.equal(palette.isPaletteShortcut({key: "K", metaKey: true}), true);
  assert.equal(palette.isPaletteShortcut({key: "k"}), false);
  assert.equal(palette.isPaletteShortcut({key: "k", ctrlKey: true, shiftKey: true}), false);
  assert.equal(palette.isPaletteShortcut({key: "k", metaKey: true, altKey: true}), false);
});

test("Alt+P opens the pet workbench by physical key and stays out of IME and other modifiers", () => {
  const shortcut = palette.isPetWorkbenchShortcut;
  assert.deepEqual(palette.PET_WORKBENCH_SHORTCUT, {code: "KeyP", label: "Alt+P", macLabel: "⌥P"});
  assert.equal(shortcut({key: "p", code: "KeyP", altKey: true}), true);
  // macOS Option+P types "π": the physical key still matches.
  assert.equal(shortcut({key: "π", code: "KeyP", altKey: true}), true);
  // A different layout puts another letter on the physical P key; the
  // shortcut follows the key, like Ctrl+K follows "k".
  assert.equal(shortcut({key: "p", code: "KeyP"}), false, "no modifier");
  assert.equal(shortcut({key: "p", code: "KeyP", altKey: true, shiftKey: true}), false);
  assert.equal(shortcut({key: "p", code: "KeyP", altKey: true, ctrlKey: true}), false, "AltGr reports Ctrl+Alt on Windows");
  assert.equal(shortcut({key: "p", code: "KeyP", altKey: true, metaKey: true}), false);
  assert.equal(shortcut({key: "o", code: "KeyO", altKey: true}), false);
  assert.equal(shortcut({key: "p", code: "KeyP", altKey: true, isComposing: true}), false, "IME composing");
  assert.equal(shortcut({key: "Process", code: "KeyP", altKey: true, keyCode: 229}), false, "IME keyCode 229");
  assert.equal(palette.petWorkbenchShortcutLabel("Win32"), "Alt+P");
  assert.equal(palette.petWorkbenchShortcutLabel("MacIntel"), "⌥P");
});

test("IME composition swallows Enter and arrows until the composition is committed", () => {
  const context = {count: 3, activeIndex: 1};
  assert.deepEqual(
    palette.paletteKeyAction({key: "Enter", isComposing: true}, context),
    {handled: false},
  );
  assert.deepEqual(
    palette.paletteKeyAction({key: "ArrowDown", keyCode: 229}, context),
    {handled: false},
  );
  assert.deepEqual(
    palette.paletteKeyAction({key: "Enter", isComposing: false}, {count: 3, activeIndex: 1, composing: true}),
    {handled: false},
  );
  assert.deepEqual(
    palette.paletteKeyAction({key: "Enter"}, {count: 3, activeIndex: 1}),
    {handled: true, action: "select", activeIndex: 1},
  );
});

test("WebKit's post-composition Enter is ignored even with isComposing=false", () => {
  assert.equal(palette.COMMAND_PALETTE_COMPOSITION_GUARD_MS, 50);
  assert.deepEqual(
    palette.paletteKeyAction({key: "Enter", keyCode: 13, isComposing: false}, {count: 2, activeIndex: 0, justComposed: true}),
    {handled: false},
    "Safari fires compositionend before the committing Enter keydown",
  );
  assert.equal(
    palette.paletteKeyAction({key: "Enter"}, {count: 2, activeIndex: 0, justComposed: false}).action,
    "select",
    "the guard must expire so a later Enter still selects",
  );
  assert.equal(
    palette.paletteKeyAction({key: "ArrowDown"}, {count: 2, activeIndex: 0, justComposed: true}).action,
    "down",
    "only Enter belongs to the composition commit",
  );
});

test("arrow keys move the selection and wrap at both ends", () => {
  assert.equal(palette.paletteKeyAction({key: "ArrowDown"}, {count: 3, activeIndex: 2}).activeIndex, 0);
  assert.equal(palette.paletteKeyAction({key: "ArrowUp"}, {count: 3, activeIndex: 0}).activeIndex, 2);
  assert.equal(palette.paletteKeyAction({key: "ArrowDown"}, {count: 3, activeIndex: 0}).activeIndex, 1);
  assert.deepEqual(palette.paletteKeyAction({key: "Escape"}, {count: 3, activeIndex: 0}), {handled: true, action: "close"});
  // An empty result list must not select anything.
  assert.deepEqual(palette.paletteKeyAction({key: "Enter"}, {count: 0, activeIndex: 0}), {handled: true, action: "none"});
});

test("the task code, project name, workspace name and status all filter", () => {
  const items = sampleItems();
  const byCode = palette.buildCommandSections({query: "352", items, recent: []});
  assert.deepEqual(byCode.map(section => section.id), ["tasks"]);
  assert.equal(byCode[0].items[0].target, "task-352");

  const byProject = palette.buildCommandSections({query: "AHA2", items, recent: []});
  assert.equal(byProject[0].id, "tasks");
  // Project and workspace rows are found by the same term and stay in their
  // own groups; Task rows sort above them.
  assert.ok(byProject.some(section => section.id === "projects" && section.items.some(item => item.target === "project-aha2")));
  assert.ok(byProject.some(section => section.id === "workspaces" && section.items.some(item => item.label === "AHA2-BUILDER-SERVER")));

  const byStatus = palette.buildCommandSections({query: "执行中", items, recent: []});
  assert.deepEqual(byStatus[0].items.map(item => item.target), ["task-352"]);

  const byPage = palette.buildCommandSections({query: "模型", items, recent: []});
  assert.ok(byPage.some(section => section.id === "pages" && section.items.some(item => item.target === "models")));

  const byAction = palette.buildCommandSections({query: "新建", items, recent: []});
  assert.ok(byAction.some(section => section.id === "actions" && section.items.some(item => item.target === "new-task")));

  assert.deepEqual(palette.buildCommandSections({query: "没有这个任务", items, recent: []}), []);
});

test("empty input shows page entries, actions and at most five recent Tasks", () => {
  const items = sampleItems();
  const recent = [1, 2, 3, 4, 5, 6].map(index => commandItem("task", `recent-${index}`, `最近任务 ${index}`, "", `recent-${index}`));
  const sections = palette.buildCommandSections({query: "   ", items, recent});
  assert.deepEqual(sections.map(section => section.id), ["pages", "actions", "recent"]);
  assert.deepEqual(sections.map(section => section.label), ["页面", "动作", "最近打开"]);
  assert.equal(sections[2].items.length, 5);
  assert.ok(!sections.some(section => section.id === "tasks"), "an empty query must not dump every Task");
});

test("selecting a result keeps the routing target for Tasks, Projects and pages", () => {
  const items = sampleItems();
  const sections = palette.buildCommandSections({query: "", items, recent: []});
  const flattened = palette.flattenCommandSections(sections);
  assert.equal(palette.activeCommandItem(sections, 0).target, "models");
  assert.equal(flattened[1].target, "new-task");
  assert.equal(palette.activeCommandItem(sections, -1).target, "new-task", "negative indexes wrap");

  const taskSections = palette.buildCommandSections({query: "352", items, recent: []});
  const selected = palette.activeCommandItem(taskSections, 0);
  const action = palette.paletteKeyAction({key: "Enter"}, {count: palette.flattenCommandSections(taskSections).length, activeIndex: 0});
  assert.equal(action.action, "select");
  assert.equal(selected.target, "task-352");
});

test("the filter debounces by 100ms and only runs the latest query", () => {
  const calls = [];
  const handles = [];
  const timers = {
    setTimeout(callback, delayMs) {
      const handle = {callback, delayMs, cancelled: false};
      handles.push(handle);
      return handle;
    },
    clearTimeout(handle) {
      handle.cancelled = true;
    },
  };
  const debouncer = palette.createDebouncer(100, value => calls.push(value), timers);
  debouncer.schedule("gl");
  debouncer.schedule("glob");
  assert.equal(handles.length, 2);
  assert.equal(handles[0].cancelled, true);
  assert.equal(handles[1].cancelled, false);
  assert.equal(handles[1].delayMs, 100);
  assert.equal(debouncer.pending(), true);
  handles[1].callback();
  assert.deepEqual(calls, ["glob"]);
  assert.equal(debouncer.pending(), false);
  debouncer.schedule("stale");
  debouncer.cancel();
  assert.equal(handles[2].cancelled, true);
  assert.deepEqual(calls, ["glob"]);
});

test("Enter flushes the pending debounced query instead of using stale results", () => {
  const calls = [];
  let pending = null;
  const timers = {
    setTimeout(callback) {
      pending = callback;
      return 1;
    },
    clearTimeout() {
      pending = null;
    },
  };
  const debouncer = palette.createDebouncer(100, value => calls.push(value), timers);
  debouncer.schedule("352");
  assert.deepEqual(calls, []);
  assert.equal(debouncer.flush(), true);
  assert.deepEqual(calls, ["352"]);
  assert.equal(debouncer.pending(), false);
  assert.equal(debouncer.flush(), false, "nothing is left to flush");
  assert.equal(pending, null, "the timer was cleared, not run twice");
});

test("recent Tasks are de-duplicated, kept newest-first and capped at five", () => {
  const values = new Map();
  const storage = {
    getItem(key) { return values.get(key) ?? null; },
    setItem(key, value) { values.set(key, value); },
  };
  for (const id of ["t1", "t2", "t3", "t4", "t5", "t6"]) palette.rememberRecentTaskID(storage, id);
  assert.deepEqual(palette.readRecentTaskIDs(storage), ["t6", "t5", "t4", "t3", "t2"]);
  palette.rememberRecentTaskID(storage, "t4");
  assert.deepEqual(palette.readRecentTaskIDs(storage), ["t4", "t6", "t5", "t3", "t2"]);
  assert.equal(palette.readRecentTaskIDs({getItem: () => "not json", setItem() {}}).length, 0);
  assert.equal(palette.rememberRecentTaskID(storage, "t4", 2).length, 2);
});

test("main.ts wires the shortcut, dialog semantics, IME guard and routing", async () => {
  const main = await readFile(resolve(webRoot, "src", "main.ts"), "utf8");
  const buildScript = await readFile(resolve(webRoot, "..", "scripts", "build-web.mjs"), "utf8");

  // Shortcut entry points.
  assert.match(main, /if \(event\.defaultPrevented \|\| !isPaletteShortcut\(event\)\) return;/);
  assert.match(main, /event\.preventDefault\(\);\s*if \(commandPaletteRoot\?\.open\) closeCommandPalette\(\);\s*else openCommandPalette\(\);/);
  assert.match(main, /data-command-palette-open/);
  assert.match(main, /command-palette-trigger/);
  assert.match(main, /command-palette-fab/);

  // Combobox/listbox semantics and the live result count.
  assert.match(main, /role="combobox"/);
  assert.match(main, /role="listbox"/);
  assert.match(main, /aria-activedescendant/);
  assert.match(main, /aria-selected="false"/);
  assert.match(main, /role="status" aria-live="polite"/);

  // IME composition and the 100ms debounce.
  assert.match(main, /compositionstart/);
  assert.match(main, /compositionend/);
  assert.match(main, /commandPaletteComposing = true/);
  assert.match(main, /createDebouncer\(COMMAND_PALETTE_DEBOUNCE_MS/);
  const paletteSource = await readFile(resolve(webRoot, "src", "command_palette.ts"), "utf8");
  assert.match(paletteSource, /COMMAND_PALETTE_DEBOUNCE_MS = 100/);

  // The index is rebuilt from the live stores whenever the panel opens.
  assert.match(main, /commandPaletteItems = commandPaletteAllItems\(\);/);
  assert.match(main, /commandPaletteRecent = commandPaletteRecentItems\(commandPaletteItems\);/);
  // An SSE list refresh while the panel is open rebuilds from the new store.
  assert.match(main, /function rebuildOpenCommandPaletteIndex\(\)/);
  assert.match(main, /rebuildOpenCommandPaletteIndex\(\);/);
  // Recents are recorded on every Task open, not just palette selections.
  assert.match(main, /rememberCommandPaletteTask\(taskID\);/);

  // Routing reuses the existing pages and Task detail flow.
  assert.match(main, /await openTask\(item\.target\)/);
  assert.match(main, /state\.selectedProject = project;/);
  assert.match(main, /switchView\(view\)/);
  assert.match(main, /await openTaskCreateDialogFromPalette\(\)/);
  // The approvals tab is gone (task-813): "Open approvals" and the workbench
  // page entry mean handle what is pending (the overview's "for you"); the
  // approval history entry opens the records tab on that group.
  assert.match(main, /openPetWorkbenchFor\("handle-pending"\)/);
  assert.match(main, /openPetWorkbenchFor\("approval-history"\)/);
  assert.doesNotMatch(main, /petWorkbenchTab = "approvals";/);

  // Focus returns to the trigger after closing and after a route change...
  assert.match(main, /function closeCommandPalette\(restoreFocus = true\): void \{[\s\S]{0,600}?if \(!restoreFocus\) return;[\s\S]{0,240}?previous\?\.isConnected\) previous\.focus\(\)/);
  assert.match(main, /function focusCommandPaletteTrigger\(\)/);
  // ...but executing an item must drop the old composer focus before openTask.
  // The ordering lives in runPaletteSelection above; here we pin that the
  // execution path really uses it.
  assert.match(main, /await runPaletteSelection\(\{/);
  assert.match(main, /navigate: \(\) => navigateCommandPaletteItem\(item\)/);
  assert.match(main, /async function navigateCommandPaletteItem\(item: CommandItem\): Promise<void> \{[\s\S]{0,400}?await openTask\(item\.target\)/);
  const executeStart = main.indexOf("async function executeCommandPaletteItem");
  const executeBody = main.slice(executeStart, main.indexOf('document.addEventListener("keydown"', executeStart));
  assert.doesNotMatch(executeBody, /closeCommandPalette\(\);/, "execution must not restore the old composer focus");

  // Safari IME: compositionend timestamps the keydown guard window.
  assert.match(main, /commandPaletteCompositionEndedAt = performance\.now\(\)/);
  assert.match(main, /justComposed/);
  assert.match(main, /COMMAND_PALETTE_COMPOSITION_GUARD_MS/);

  assert.match(buildScript, /"command_palette", "main"/);

  const styles = await readFile(resolve(webRoot, "src", "styles.css"), "utf8");
  assert.match(styles, /#command-palette \{ width: min\(680px/);
  assert.match(styles, /@media \(max-width: 760px\)[\s\S]*?#command-palette \{ position: fixed;[\s\S]*?height: var\(--visual-viewport-height\)/);
  assert.match(styles, /env\(safe-area-inset-bottom\)/);
});
