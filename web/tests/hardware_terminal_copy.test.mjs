// The hardware console copy runs against a real xterm buffer, because the
// behaviour worth pinning down is a property of that buffer rather than of our
// loop: translateToString(true) trims the padding xterm adds out to the terminal
// width, so a line whose text ends in genuine spaces keeps them. Stripping
// trailing whitespace ourselves would look tidier and would corrupt the copy.
import test from "node:test";
import assert from "node:assert/strict";
import {readFileSync} from "node:fs";
import {fileURLToPath} from "node:url";
import {dirname, join} from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const vendor = readFileSync(join(here, "..", "vendor", "xterm.js"), "utf8");

function loadTerminal() {
  const previousWindow = globalThis.window;
  const previousDocument = globalThis.document;
  globalThis.window = globalThis;
  globalThis.document = {
    createElement: () => ({
      style: {}, classList: {add() {}, remove() {}}, setAttribute() {},
      appendChild() {}, remove() {}, addEventListener() {}, removeEventListener() {},
    }),
  };
  const module = {exports: {}};
  new Function("module", "exports", vendor)(module, module.exports);
  const Terminal = globalThis.Terminal || module.exports.Terminal;
  return {
    Terminal,
    restore() {
      globalThis.window = previousWindow;
      globalThis.document = previousDocument;
    },
  };
}

// Mirrors bufferText() in web/src/hardware_terminal.ts.
function bufferText(terminal) {
  const buffer = terminal.buffer.active;
  const lines = [];
  for (let index = 0; index < buffer.length; index++) {
    lines.push(buffer.getLine(index)?.translateToString(true) ?? "");
  }
  while (lines.length > 0 && lines[lines.length - 1] === "") lines.pop();
  return lines.join("\n");
}

test("terminal buffer copy keeps real spacing and drops width padding", async () => {
  const {Terminal, restore} = loadTerminal();
  try {
    const terminal = new Terminal({cols: 20, rows: 4});
    await new Promise(resolve => terminal.write("AT+GMR\r\n  busy   \r\nOK\r\n", resolve));
    const copied = bufferText(terminal);

    assert.equal(copied, "AT+GMR\n  busy   \nOK");
    // Padding to the 20-column width must not survive.
    assert.ok(!copied.includes("AT+GMR "), "width padding leaked into the copy");
    // Interior spacing is content, not padding, and must survive.
    assert.ok(copied.includes("  busy   "), "interior spacing was stripped from the copy");
    assert.ok(!copied.endsWith("\n"), "copy ends with an empty line");
    terminal.dispose();
  } finally {
    restore();
  }
});

test("terminal buffer copy is empty for an untouched terminal", async () => {
  const {Terminal, restore} = loadTerminal();
  try {
    const terminal = new Terminal({cols: 20, rows: 4});
    assert.equal(bufferText(terminal), "");
    terminal.dispose();
  } finally {
    restore();
  }
});
