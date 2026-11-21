// Page-level interaction helpers for the AHA2 browser host.
//
// Everything here is deliberately reference- or coordinate-based. The host
// never accepts a CSS selector, a script or a CDP call from AHA. Element refs
// carry the snapshot generation so a ref from an older snapshot cannot address
// a new element that happens to occupy the same slot.

export const MAX_ELEMENTS = 200;
export const MAX_ACTION_TEXT_BYTES = 4096;
export const MAX_ACTION_KEYS = 4;
export const MAX_ACTION_KEY_LENGTH = 32;
export const MAX_SCROLL_DELTA = 4000;
export const MAX_COORDINATE = 100000;
export const REF_TTL_MS = 120_000;
export const MAX_UPLOAD_FILES = 5;
export const MAX_UPLOAD_FILE_BYTES = 10 * 1024 * 1024;
export const MAX_UPLOAD_TOTAL_BYTES = 20 * 1024 * 1024;
export const MAX_UPLOAD_BODY_BYTES = 32 * 1024 * 1024;
export const MAX_FILENAME_CHARS = 255;

const KEY_ALIASES = new Map([
  ["CTRL", "Control"], ["CONTROL", "Control"],
  ["ALT", "Alt"], ["SHIFT", "Shift"], ["META", "Meta"], ["WIN", "Meta"], ["CMD", "Meta"],
  ["ESC", "Escape"], ["ESCAPE", "Escape"], ["RETURN", "Enter"], ["ENTER", "Enter"],
  ["TAB", "Tab"], ["SPACE", "Space"], ["BACKSPACE", "Backspace"], ["DELETE", "Delete"],
  ["UP", "ArrowUp"], ["DOWN", "ArrowDown"], ["LEFT", "ArrowLeft"], ["RIGHT", "ArrowRight"],
  ["HOME", "Home"], ["END", "End"], ["PAGEUP", "PageUp"], ["PAGEDOWN", "PageDown"],
  ["INSERT", "Insert"], ["CONTEXTMENU", "ContextMenu"],
]);

const DRAG_STEPS = 8;

function invalid(message = "invalid action", code = "browser_action_invalid") {
  return Object.assign(new Error(message), {code});
}

function stale(message = "element ref is stale") {
  return Object.assign(new Error(message), {code: "browser_element_stale"});
}

function notFound(message = "element ref not found") {
  return Object.assign(new Error(message), {code: "browser_element_not_found"});
}

export function isElementRef(value) {
  return /^e[0-9]{1,9}-[0-9]{1,4}$/.test(String(value || ""));
}

// captureElements clears the previous snapshot's markers, walks a bounded list
// of interactive elements, and assigns a generation-scoped short ref. It never
// reads input values (a password field's value must not leave the page).
export async function captureElements(page, prefix) {
  return page.evaluate(({max, prefix}) => {
    for (const node of document.querySelectorAll("[data-aha2-ref]")) {
      node.removeAttribute("data-aha2-ref");
    }
    const selector = [
      "a[href]", "button", "input:not([type=hidden])", "select", "textarea",
      "[role=button]", "[role=link]", "[role=textbox]", "[role=checkbox]",
      "[role=radio]", "[role=combobox]", "[role=tab]", "[role=menuitem]",
      "[contenteditable=true]", "summary",
    ].join(",");
    const nameOf = element => {
      const values = [
        element.getAttribute("aria-label"),
        element.getAttribute("alt"),
        element.getAttribute("title"),
        element.getAttribute("placeholder"),
      ];
      const labelledBy = element.getAttribute("aria-labelledby");
      if (labelledBy) {
        for (const id of labelledBy.split(/\s+/)) {
          const node = document.getElementById(id);
          if (node) values.push(node.innerText);
        }
      }
      if (element.labels && element.labels.length) values.push(element.labels[0].innerText);
      values.push(element.innerText || element.textContent);
      for (const value of values) {
        const text = String(value || "").replace(/\s+/g, " ").trim();
        if (text) return text.slice(0, 120);
      }
      return "";
    };
    const roleOf = element => {
      const explicit = String(element.getAttribute("role") || "").trim().toLowerCase();
      if (explicit) return explicit;
      const tag = element.tagName.toLowerCase();
      if (tag === "a") return "link";
      if (tag === "button" || tag === "summary") return "button";
      if (tag === "select") return "combobox";
      if (tag === "textarea") return "textbox";
      if (tag === "input") {
        const type = String(element.getAttribute("type") || "text").toLowerCase();
        if (type === "checkbox") return "checkbox";
        if (type === "radio") return "radio";
        if (type === "button" || type === "submit" || type === "reset" || type === "file") return "button";
        return "textbox";
      }
      if (element.isContentEditable) return "textbox";
      return "button";
    };
    const result = [];
    const seen = new Set();
    for (const element of document.querySelectorAll(selector)) {
      if (result.length >= max) break;
      if (seen.has(element)) continue;
      seen.add(element);
      if (element.disabled || element.getAttribute("aria-hidden") === "true") continue;
      const style = window.getComputedStyle(element);
      if (style.display === "none" || style.visibility === "hidden" || Number(style.opacity) === 0) continue;
      const rect = element.getBoundingClientRect();
      if (rect.width < 2 || rect.height < 2) continue;
      if (rect.bottom < 0 || rect.right < 0 || rect.top > window.innerHeight || rect.left > window.innerWidth) continue;
      const role = roleOf(element);
      const actions = ["click"];
      if (role === "textbox" || role === "combobox") actions.push("type");
      else actions.push("double_click");
      const ref = `${prefix}${result.length + 1}`;
      element.setAttribute("data-aha2-ref", ref);
      result.push({
        ref, role, name: nameOf(element),
        bounds: {
          x: Math.round(rect.x), y: Math.round(rect.y),
          width: Math.round(rect.width), height: Math.round(rect.height),
        },
        actions,
      });
    }
    return result;
  }, {max: MAX_ELEMENTS, prefix});
}

function normalizeKeyToken(token) {
  const raw = String(token || "").trim();
  if (!raw || raw.length > MAX_ACTION_KEY_LENGTH) throw invalid("invalid key name");
  const upper = raw.toUpperCase();
  if (KEY_ALIASES.has(upper)) return KEY_ALIASES.get(upper);
  if (/^[A-Za-z0-9]$/.test(raw)) return raw;
  // A single printable ASCII character is a valid Playwright key name. Owner
  // keyboard input needs combinations such as Control+/; element refs never
  // reach this path, so no selector or script can be smuggled in here.
  if (/^[!-~]$/.test(raw)) return raw;
  if (/^F([1-9]|1[0-2])$/.test(upper)) return upper;
  throw invalid("invalid key name");
}

// The host accepts a closed action set. The first six kinds are the Agent set;
// context_click, text and drag are Owner-only additions (AHA enforces the
// boundary before the host call; the host still validates every field).
export function normalizeAction(input) {
  const kind = String(input && input.kind || "").trim();
  if (!["click", "double_click", "hover", "type", "key", "scroll",
    "context_click", "text", "drag"].includes(kind)) {
    throw invalid("unsupported action");
  }
  const hasRef = typeof input.ref === "string" && input.ref.trim() !== "";
  const hasX = Number.isInteger(input.x);
  const hasY = Number.isInteger(input.y);
  if (hasX !== hasY) throw invalid("x and y must be supplied together");
  const hasCoords = hasX && hasY;
  const ref = hasRef ? String(input.ref).trim() : "";
  if (hasRef && (!isElementRef(ref) || ref.length > 64)) throw invalid("invalid element ref");
  if (hasRef && hasCoords) throw invalid("ref and coordinates are mutually exclusive");
  if (hasCoords && (input.x < 0 || input.y < 0 || input.x > MAX_COORDINATE || input.y > MAX_COORDINATE)) {
    throw invalid("coordinates are out of range");
  }
  const hasToX = Number.isInteger(input.to_x);
  const hasToY = Number.isInteger(input.to_y);
  if (hasToX !== hasToY) throw invalid("to_x and to_y must be supplied together");
  if (hasToX && (input.to_x < 0 || input.to_y < 0 ||
    input.to_x > MAX_COORDINATE || input.to_y > MAX_COORDINATE)) {
    throw invalid("drag destination is out of range");
  }
  // Only drag accepts a destination; every other kind rejects it so a
  // malformed call cannot silently ignore part of its payload.
  if (kind !== "drag" && (hasToX || hasToY)) throw invalid("drag destination is not accepted");
  const action = {
    kind, ref, x: hasCoords ? input.x : null, y: hasCoords ? input.y : null,
    toX: hasToX ? input.to_x : null, toY: hasToY ? input.to_y : null,
    text: "", keys: [], deltaX: 0, deltaY: 0,
  };
  const hasExtraPayload = () => String(input.text || "") !== "" ||
    (Array.isArray(input.keys) && input.keys.length > 0) ||
    (Number.isInteger(input.delta_x) && input.delta_x !== 0) ||
    (Number.isInteger(input.delta_y) && input.delta_y !== 0);
  if (kind === "click" || kind === "double_click" || kind === "hover" || kind === "context_click") {
    if (!hasRef && !hasCoords) throw invalid("element ref or coordinates are required");
    if (hasExtraPayload()) throw invalid("this action does not accept input content");
  } else if (kind === "text") {
    if (hasRef || hasCoords) throw invalid("text action does not accept a target");
    const text = String(input.text || "");
    if (!text) throw invalid("text is required");
    if (Buffer.byteLength(text, "utf8") > MAX_ACTION_TEXT_BYTES) throw invalid("text is too long");
    if (/[\u0000\r\n\u007f]/.test(text)) throw invalid("text contains control characters");
    if ((Array.isArray(input.keys) && input.keys.length > 0) ||
        (Number.isInteger(input.delta_x) && input.delta_x !== 0) ||
        (Number.isInteger(input.delta_y) && input.delta_y !== 0)) {
      throw invalid("text action does not accept keys or scroll parameters");
    }
    action.text = text;
  } else if (kind === "drag") {
    if (hasRef) throw invalid("drag does not accept an element ref");
    if (!hasCoords || !hasToX) throw invalid("drag requires start and end coordinates");
    if (hasExtraPayload()) throw invalid("drag does not accept input content");
  } else if (kind === "type") {
    if (!hasRef && !hasCoords) throw invalid("element ref or coordinates are required");
    const text = String(input.text || "");
    if (!text) throw invalid("text is required");
    if (Buffer.byteLength(text, "utf8") > MAX_ACTION_TEXT_BYTES) throw invalid("text is too long");
    if (/[\u0000\r\n\u007f]/.test(text)) throw invalid("text contains control characters");
    action.text = text;
  } else if (kind === "key") {
    if (hasRef || hasCoords) throw invalid("key action does not accept a target");
    const keys = Array.isArray(input.keys) ? input.keys : [];
    if (keys.length < 1 || keys.length > MAX_ACTION_KEYS) throw invalid("invalid key count");
    action.keys = keys.map(normalizeKeyToken);
  } else if (kind === "scroll") {
    if (hasRef) throw invalid("scroll action does not accept an element ref");
    const deltaX = Number.isInteger(input.delta_x) ? input.delta_x : 0;
    const deltaY = Number.isInteger(input.delta_y) ? input.delta_y : 0;
    if (deltaX === 0 && deltaY === 0) throw invalid("scroll distance is zero");
    if (Math.abs(deltaX) > MAX_SCROLL_DELTA || Math.abs(deltaY) > MAX_SCROLL_DELTA) {
      throw invalid("scroll distance is too large");
    }
    action.deltaX = deltaX;
    action.deltaY = deltaY;
  }
  return action;
}

function checkRefFresh(tab, ref) {
  const match = /^e([0-9]+)-([0-9]+)$/.exec(ref);
  if (!match || Number(match[1]) !== tab.refGeneration || !tab.refsAt ||
      Date.now() - tab.refsAt > REF_TTL_MS) {
    throw stale();
  }
}

async function elementLocator(page, tab, ref) {
  checkRefFresh(tab, ref);
  if (typeof page.locator !== "function") throw notFound();
  const locator = page.locator(`[data-aha2-ref="${ref}"]`).first();
  try {
    await locator.waitFor({state: "attached", timeout: 2000});
  } catch {
    throw notFound();
  }
  return locator;
}

async function checkViewport(page, x, y) {
  if (typeof page.evaluate !== "function") return;
  let size;
  try {
    size = await page.evaluate(() => ({width: window.innerWidth, height: window.innerHeight}));
  } catch {
    return;
  }
  if (!size || typeof size.width !== "number" || typeof size.height !== "number") return;
  if (x > size.width || y > size.height) throw invalid("coordinates are outside the viewport");
}

export async function performAction(page, tab, action) {
  const target = action.ref ? await elementLocator(page, tab, action.ref) : null;
  if (!target && action.x !== null) await checkViewport(page, action.x, action.y);
  switch (action.kind) {
    case "click":
      if (target) await target.click({timeout: 5000});
      else await page.mouse.click(action.x, action.y);
      break;
    case "double_click":
      if (target) await target.dblclick({timeout: 5000});
      else await page.mouse.dblclick(action.x, action.y);
      break;
    case "hover":
      if (target) await target.hover({timeout: 5000});
      else await page.mouse.move(action.x, action.y);
      break;
    case "context_click":
      // A real right click, like the Owner pressing the right mouse button.
      // Pages may show their own menu; Chromium may also show its native one.
      if (target) await target.click({timeout: 5000, button: "right"});
      else await page.mouse.click(action.x, action.y, {button: "right"});
      break;
    case "type":
      if (target) await target.click({timeout: 5000});
      else await page.mouse.click(action.x, action.y);
      await page.keyboard.type(action.text);
      break;
    case "text":
      // Type into the page's current focus. The Owner clicks the target first
      // (a click action), so continuous typing does not re-click or move focus.
      await page.keyboard.type(action.text);
      break;
    case "key":
      await page.keyboard.press(action.keys.join("+"));
      break;
    case "scroll":
      if (action.x !== null) await page.mouse.move(action.x, action.y);
      await page.mouse.wheel(action.deltaX, action.deltaY);
      break;
    case "drag":
      await checkViewport(page, action.toX, action.toY);
      await page.mouse.move(action.x, action.y);
      await page.mouse.down();
      try {
        await page.mouse.move(action.toX, action.toY, {steps: DRAG_STEPS});
      } finally {
        await page.mouse.up();
      }
      break;
    default:
      throw invalid("unsupported action");
  }
  return {
    kind: action.kind, ref: action.ref || undefined,
    x: action.x === null ? undefined : action.x, y: action.y === null ? undefined : action.y,
    to_x: action.toX === null ? undefined : action.toX, to_y: action.toY === null ? undefined : action.toY,
  };
}

function decodeUploadFile(file) {
  const name = String(file && file.filename || "").trim();
  if (!name || [...name].length > MAX_FILENAME_CHARS || name === "." || name === ".." ||
      /[\\/]/.test(name) || /[\u0000\r\n\u007f]/.test(name)) {
    throw Object.assign(new Error("invalid upload filename"), {code: "browser_upload_invalid"});
  }
  const base64 = String(file && file.content_base64 || "").trim();
  if (!base64 || base64.length % 4 !== 0 || !/^[A-Za-z0-9+/]+={0,2}$/.test(base64)) {
    throw Object.assign(new Error("invalid upload payload"), {code: "browser_upload_invalid"});
  }
  const buffer = Buffer.from(base64, "base64");
  if (!buffer.length || buffer.length > MAX_UPLOAD_FILE_BYTES) {
    throw Object.assign(new Error("upload file is too large"), {code: "browser_upload_invalid"});
  }
  return {
    name,
    mimeType: "application/octet-stream",
    buffer,
  };
}

export const FILE_CHOOSER_TIMEOUT_MS = 5000;

function uploadFailure(message) {
  return Object.assign(new Error(message), {code: "browser_upload_failed"});
}

// A file-chooser wait and its click are two asynchronous operations. Either
// can fail (disabled target, navigation, timeout) and whichever loses must not
// leave an unhandled rejection: in a long-lived host that would terminate the
// Node process. This waiter owns the listener and timer, and can be canceled
// as soon as the click fails.
export function createFileChooserWaiter(page, timeoutMs = FILE_CHOOSER_TIMEOUT_MS) {
  let settled = false;
  let timer = null;
  let resolvePromise = null;
  let rejectPromise = null;
  const promise = new Promise((resolve, reject) => {
    resolvePromise = resolve;
    rejectPromise = reject;
  });
  const cleanup = () => {
    if (timer) {
      clearTimeout(timer);
      timer = null;
    }
    if (typeof page.removeListener === "function") page.removeListener("filechooser", listener);
  };
  const listener = chooser => {
    if (settled) return;
    settled = true;
    cleanup();
    resolvePromise(chooser);
  };
  timer = setTimeout(() => {
    if (settled) return;
    settled = true;
    cleanup();
    rejectPromise(uploadFailure("file chooser did not open"));
  }, timeoutMs);
  timer.unref?.();
  if (typeof page.once === "function") page.once("filechooser", listener);
  else if (typeof page.on === "function") page.on("filechooser", listener);
  else {
    cleanup();
    rejectPromise(uploadFailure("file chooser is unavailable"));
  }
  // Keep the rejection handled even when the caller cancels after a click
  // failure and never awaits it.
  promise.catch(() => {});
  return {
    promise,
    cancel(message = "upload target could not be clicked") {
      if (settled) return;
      settled = true;
      cleanup();
      rejectPromise(uploadFailure(message));
    },
  };
}

async function clickAndWaitForChooser(page, clickFn, timeoutMs = FILE_CHOOSER_TIMEOUT_MS) {
  const waiter = createFileChooserWaiter(page, timeoutMs);
  try {
    await clickFn();
  } catch {
    waiter.cancel();
    throw uploadFailure("upload target could not be clicked");
  }
  try {
    return await waiter.promise;
  } catch {
    throw uploadFailure("file chooser did not open");
  }
}

export async function performUpload(page, tab, upload) {
  const files = Array.isArray(upload && upload.files) ? upload.files : [];
  if (files.length < 1 || files.length > MAX_UPLOAD_FILES) {
    throw Object.assign(new Error("invalid upload file count"), {code: "browser_upload_invalid"});
  }
  const payloads = files.map(decodeUploadFile);
  const total = payloads.reduce((sum, file) => sum + file.buffer.length, 0);
  if (total > MAX_UPLOAD_TOTAL_BYTES) {
    throw Object.assign(new Error("upload is too large"), {code: "browser_upload_invalid"});
  }
  const hasRef = typeof upload.ref === "string" && upload.ref.trim() !== "";
  const hasX = Number.isInteger(upload.x);
  const hasY = Number.isInteger(upload.y);
  if (hasX !== hasY || hasRef === (hasX && hasY)) {
    throw Object.assign(new Error("invalid upload target"), {code: "browser_upload_invalid"});
  }
  if (hasRef) {
    const locator = await elementLocator(page, tab, String(upload.ref).trim());
    let fileInput = false;
    try {
      fileInput = await locator.evaluate(element => element.matches('input[type="file"]'));
    } catch {
      fileInput = false;
    }
    if (fileInput) {
      await locator.setInputFiles(payloads, {timeout: FILE_CHOOSER_TIMEOUT_MS});
    } else {
      const chooser = await clickAndWaitForChooser(page,
        () => locator.click({timeout: FILE_CHOOSER_TIMEOUT_MS}));
      await chooser.setFiles(payloads);
    }
    return {ref: String(upload.ref).trim(), file_count: payloads.length, total_bytes: total};
  }
  if (hasX) await checkViewport(page, upload.x, upload.y);
  const chooser = await clickAndWaitForChooser(page,
    () => page.mouse.click(upload.x, upload.y));
  await chooser.setFiles(payloads);
  return {file_count: payloads.length, total_bytes: total};
}
