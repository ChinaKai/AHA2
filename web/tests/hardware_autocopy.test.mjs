import test from "node:test";
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";

const root = resolve("/home/kaikai/kk-workspace/my_project/AHA2", "web");

test("hardware terminal offers selection copy and cleans it up", async () => {
  const source = await readFile(resolve(root, "src", "hardware_terminal.ts"), "utf8");
  assert.match(source, /onSelectionChange\?\(listener: \(\) => void\)/);
  assert.match(source, /onSelectionCopy\?\(text: string\)/);
  assert.match(source, /options\.container\.addEventListener\("pointerup", handlePointerUp\)/);
  assert.match(source, /options\.container\.removeEventListener\("pointerup", handlePointerUp\)/);
  assert.match(source, /text === lastCopied/);
});

test("hardware panel exposes the auto-copy toggle and copies on selection", async () => {
  const source = await readFile(resolve(root, "src", "hardware_panel.ts"), "utf8");
  assert.match(source, /id="hardware-auto-copy"/);
  assert.match(source, /hardwareAutoCopyEnabled\(\)/);
  assert.match(source, /onSelectionCopy\(text\) \{/);
  assert.match(source, /setHardwareAutoCopy\(/);
});
