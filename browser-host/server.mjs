// Loopback HTTP route layer for the AHA2 browser host. host.mjs is the CLI
// entry point; this module is injectable so route failures can be tested with a
// fake Playwright implementation and no real Edge install.

import {createServer} from "node:http";
import {timingSafeEqual} from "node:crypto";
import {constants as fsConstants, promises as fs} from "node:fs";
import {basename, dirname, join} from "node:path";
import {createProfileLocks, profileDir, safeProfileID} from "./lock.mjs";
import {navigatePage, openTabPage} from "./tabs.mjs";
import {
  captureElements, performAction, performUpload, normalizeAction, MAX_UPLOAD_BODY_BYTES,
} from "./interact.mjs";
import {createDownloadStore} from "./downloads.mjs";
import {
  cookieDomainMatches, exportCookie, normalizeCookie, normalizeCookieDomain,
  toPlaywrightCookie, MAX_COOKIES,
} from "./cookies.mjs";

export const VERSION = "0.1.0";
export const LOOPBACK = "127.0.0.1";
const MAX_BODY = 64 * 1024;
const MAX_COOKIE_BODY = 2 * 1024 * 1024;
const MAX_SNAPSHOT_TEXT = 200_000;
const MAX_FRAME_BYTES = 4 * 1024 * 1024;
const FRAME_MIN_INTERVAL_MS = 200;
const FRAME_QUALITY = 55;

function fail(status, code, message) {
  return {status, body: {error: {code, message}}};
}

function json(response, status, body) {
  const payload = JSON.stringify(body);
  response.writeHead(status, {
    "Content-Type": "application/json; charset=utf-8",
    "Cache-Control": "no-store",
    "Content-Length": Buffer.byteLength(payload),
  });
  response.end(payload);
}

async function readJSON(request, limit = MAX_BODY) {
  const chunks = [];
  let size = 0;
  for await (const chunk of request) {
    size += chunk.length;
    if (size > limit) throw new Error("request body too large");
    chunks.push(chunk);
  }
  if (!chunks.length) return {};
  return JSON.parse(Buffer.concat(chunks).toString("utf8"));
}

function validateURL(raw) {
  let parsed;
  try {
    parsed = new URL(String(raw || "").trim());
  } catch {
    return "";
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") return "";
  if (!parsed.hostname) return "";
  return parsed.toString();
}

function diagnostics(error, root, edge) {
  let text = String(error && error.message || error);
  for (const [value, label] of [[root, "[profile-root]"], [edge, "[edge]"]]) {
    if (value) text = text.split(value).join(label);
  }
  return text.slice(0, 500);
}

class HostState {
  constructor(root) {
    this.root = root;
    this.sessions = new Map();
    this.locks = createProfileLocks(root);
  }

  get(profileID) {
    return this.sessions.get(profileID) || null;
  }
}

const PAGE_METADATA_TIMEOUT_MS = 1500;

// A page can be wedged by a dialog or a crashed renderer. Never let one page's
// title/visibility probe hold up the whole Owner-facing page list.
function settleWithin(promise, timeoutMs, fallback) {
  return new Promise(resolve => {
    let done = false;
    const finish = value => {
      if (done) return;
      done = true;
      clearTimeout(timer);
      resolve(value);
    };
    const timer = setTimeout(() => finish(fallback), timeoutMs);
    Promise.resolve(promise).then(finish).catch(() => finish(fallback));
  });
}

function newTabID() {
  return `host-tab-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
}

// Owner-opened pages live only in the Playwright context. Give every page a
// stable host tab id the first time it is seen so AHA can bind and address it
// without ever receiving a CDP endpoint or a profile path.
function attachPage(session, page, id = "") {
  for (const tab of session.tabs.values()) {
    if (tab.page === page) return tab;
  }
  const tab = {
    id: id || newTabID(), page, url: "", title: "", frame: null, framePending: null,
    // Structured-snapshot refs carry this generation; navigation and a newer
    // snapshot invalidate every earlier ref.
    refGeneration: 0, refsAt: 0,
  };
  try {
    tab.url = page.url();
  } catch {
    // A page that is already gone is listed only if its URL can still be read.
  }
  session.tabs.set(tab.id, tab);
  try {
    page.on("close", () => {
      const current = session.tabs.get(tab.id);
      if (current && current.page === page) session.tabs.delete(tab.id);
    });
  } catch {
    // A page that cannot accept listeners is still addressable until it closes.
  }
  try {
    // Downloads are captured into the per-session controlled directory. The
    // suggested name is metadata only; the file on disk uses an opaque id.
    page.on("download", download => {
      session.downloads.capture(download, session.fence).catch(error => {
        // Do not log the suggested name or any path: a download name can carry
        // task content. Only the stable error code belongs in the host log.
        console.error(`download capture failed profile=${session.profileID} code=${String(error && error.code || "error")}`);
      });
    });
  } catch {
    // Fakes and unusual pages may not support download events.
  }
  return tab;
}

// The list is the Owner-visible page picker. Only ordinary web pages are
// returned: devtools://, edge://, chrome:// and about: pages must never appear
// in AHA or the Web UI. Every page is enumerated (no silent truncation), with
// bounded concurrency so one wedged page does not serialize the whole list.
async function describePage(tab) {
  let page;
  try {
    page = tab.page;
    if (typeof page.isClosed === "function" && page.isClosed()) return null;
  } catch {
    return null;
  }
  let url = "";
  try {
    url = page.url();
  } catch {
    return null;
  }
  if (!/^https?:\/\//i.test(url)) return null;
  const title = await settleWithin(page.title(), PAGE_METADATA_TIMEOUT_MS, "");
  const active = (await settleWithin(
    page.evaluate(() => document.visibilityState),
    PAGE_METADATA_TIMEOUT_MS,
    "",
  )) === "visible";
  return {id: tab.id, url, title, active};
}

async function listSessionPages(session) {
  const tabs = [...session.tabs.values()];
  const result = new Array(tabs.length);
  let next = 0;
  const worker = async () => {
    while (true) {
      const index = next++;
      if (index >= tabs.length) return;
      result[index] = await describePage(tabs[index]);
    }
  };
  const workers = Math.min(8, Math.max(1, tabs.length));
  await Promise.all(Array.from({length: workers}, worker));
  return result.filter(Boolean);
}

const FENCE_WAIT_TIMEOUT_MS = 5000;
const OPERATION_WAIT_TIMEOUT_MS = 30_000;

function gateError(code, message) {
  return Object.assign(new Error(message), {code});
}

// Per-session reader/writer gate. Mutating page operations hold a read slot
// until the Playwright call finishes; fence switches take the write slot and
// wait for all in-flight page operations. A write that cannot be isolated in
// time fails closed without changing the fence.
function createSessionGate() {
  let readers = 0;
  let writer = false;
  const readQueue = [];
  const writeQueue = [];
  const pump = () => {
    while (!writer && readQueue.length) {
      const request = readQueue.shift();
      readers++;
      clearTimeout(request.timer);
      request.resolve(() => { readers--; pump(); });
    }
    if (!writer && readers === 0 && writeQueue.length) {
      const request = writeQueue.shift();
      writer = true;
      clearTimeout(request.timer);
      request.resolve(() => { writer = false; pump(); });
    }
  };
  const acquire = (type, timeoutMs) => new Promise((resolve, reject) => {
    const request = {type, resolve};
    if (timeoutMs > 0) {
      request.timer = setTimeout(() => {
        const queue = type === "write" ? writeQueue : readQueue;
        const index = queue.indexOf(request);
        if (index < 0) return;
        queue.splice(index, 1);
        reject(gateError(type === "write" ? "browser_fence_timeout" : "browser_operation_busy",
          "timed out waiting for in-flight browser operations"));
      }, timeoutMs);
    }
    (type === "write" ? writeQueue : readQueue).push(request);
    pump();
  });
  return {acquire};
}

async function withSessionGate(session, type, timeoutMs, fn) {
  const release = await session.gate.acquire(type, timeoutMs);
  try {
    return await fn();
  } finally {
    release();
  }
}

// The session gate isolates fence switches; it deliberately allows concurrent
// readers. Page input, however, is a compound gesture (focus/click then type)
// that must not interleave with another input. This slot serializes whole
// actions/uploads for one browser session.
function createSerialSlot() {
  let tail = Promise.resolve();
  return {
    run(fn) {
      const next = tail.then(() => fn(), () => fn());
      tail = next.then(() => {}, () => {});
      return next;
    },
  };
}

const MAX_BOOKMARKS = 500;
const MAX_BOOKMARK_DEPTH = 8;
const MAX_BOOKMARK_FILE = 16 * 1024 * 1024;

function bookmarkPathError(message) {
  return Object.assign(new Error(message), {code: "browser_bookmarks_failed"});
}

// Edge stores bookmarks in <profile>/Default/Bookmarks. Only title, URL and
// folder names leave the host; the file path never appears in a response.
// Symlinked profiles/Default/Bookmarks are rejected so Profile A cannot read
// Profile B's file through a link or a .. traversal.
async function readProfileBookmarks(root, profileID, directory, beforeBookmarkOpen) {
  let rootReal;
  try {
    rootReal = await fs.realpath(root);
  } catch (error) {
    if (error && error.code === "ENOENT") return [];
    throw error;
  }
  let profileReal;
  try {
    profileReal = await fs.realpath(directory);
  } catch (error) {
    if (error && error.code === "ENOENT") return [];
    throw error;
  }
  if (dirname(profileReal) !== rootReal || basename(profileReal) !== profileID) {
    throw bookmarkPathError("profile directory resolves outside the profile root");
  }
  const defaultDir = join(profileReal, "Default");
  const file = join(defaultDir, "Bookmarks");
  let defaultStat;
  try {
    defaultStat = await fs.lstat(defaultDir);
  } catch (error) {
    if (error && error.code === "ENOENT") return [];
    throw error;
  }
  if (defaultStat.isSymbolicLink() || !defaultStat.isDirectory()) {
    throw bookmarkPathError("bookmark directory must be a real directory");
  }
  const defaultReal = await fs.realpath(defaultDir);
  if (defaultReal !== join(profileReal, "Default")) {
    throw bookmarkPathError("bookmark directory resolves outside the profile");
  }
  let fileStat;
  try {
    fileStat = await fs.lstat(file);
  } catch (error) {
    if (error && error.code === "ENOENT") return [];
    throw error;
  }
  if (fileStat.isSymbolicLink() || !fileStat.isFile()) {
    throw bookmarkPathError("bookmark file must be a real file");
  }
  if (fileStat.size > MAX_BOOKMARK_FILE) throw bookmarkPathError("bookmark file is too large");
  const realFile = await fs.realpath(file);
  if (realFile !== join(profileReal, "Default", "Bookmarks")) {
    throw bookmarkPathError("bookmark file resolves outside the profile");
  }
  if (typeof beforeBookmarkOpen === "function") {
    await beforeBookmarkOpen({profileID, directory});
  }
  const handle = await fs.open(file, fsConstants.O_RDONLY | fsConstants.O_NOFOLLOW);
  let raw;
  try {
    // Re-validate the actual opened object, not just the path. A parent
    // directory replaced between lstat and open changes the opened dev/ino and
    // the real path; fail closed instead of returning another Profile's data.
    const openedStat = await handle.stat();
    const sameFile = String(openedStat.dev) === String(fileStat.dev) &&
      String(openedStat.ino) === String(fileStat.ino) &&
      openedStat.size === fileStat.size &&
      openedStat.mtimeMs === fileStat.mtimeMs;
    if (!sameFile) throw bookmarkPathError("bookmark file identity changed during open");
    const defaultAfter = await fs.lstat(defaultDir);
    if (defaultAfter.isSymbolicLink() || !defaultAfter.isDirectory() ||
        String(defaultAfter.dev) !== String(defaultStat.dev) ||
        String(defaultAfter.ino) !== String(defaultStat.ino)) {
      throw bookmarkPathError("bookmark directory changed during open");
    }
    const realAfter = await fs.realpath(file);
    if (realAfter !== join(profileReal, "Default", "Bookmarks")) {
      throw bookmarkPathError("bookmark file moved outside the profile during open");
    }
    raw = await handle.readFile("utf8");
  } finally {
    await handle.close();
  }
  const parsed = JSON.parse(raw);
  const result = [];
  const walk = (node, folder, depth) => {
    if (!node || depth > MAX_BOOKMARK_DEPTH || result.length >= MAX_BOOKMARKS) return;
    if (node.type === "url") {
      const target = validateURL(node.url);
      if (!target) return;
      result.push({
        title: String(node.name || "").slice(0, 300),
        url: target,
        folder: String(folder || "").slice(0, 300),
      });
      return;
    }
    if (!Array.isArray(node.children)) return;
    const next = node.name ? (folder ? `${folder}/${String(node.name).slice(0, 120)}` : String(node.name).slice(0, 120)) : folder;
    for (const child of node.children) walk(child, next, depth + 1);
  };
  const roots = parsed && parsed.roots ? parsed.roots : {};
  for (const key of ["bookmark_bar", "other", "synced"]) {
    walk(roots[key], "", 0);
  }
  return result;
}

const OPERATION_DEADLINE_MAX_MS = 5 * 60 * 1000;
const OPERATION_CLOCK_SKEW_MS = 30 * 1000;

// Parse the per-operation fence without ever treating a malformed deadline as
// unlimited authority. Persistent session fences hold only the lease identity.
function parseOperationFence(value) {
  const raw = String(value || "");
  if (!raw) return null;
  const pipe = raw.indexOf("|");
  if (pipe >= 0) {
    if (pipe === 0 || raw.indexOf("|", pipe + 1) >= 0) return null;
    const identity = raw.slice(0, pipe);
    const deadlineText = raw.slice(pipe + 1);
    if (!identity || identity.includes("@") || !/^[0-9]+$/.test(deadlineText)) return null;
    const expiresAt = Number(deadlineText);
    if (!Number.isSafeInteger(expiresAt) || expiresAt <= 0) return null;
    return {identity, expiresAt};
  }
  // Legacy "<leaseID>@<expiry>" is accepted only with a strict numeric
  // deadline; a bare lease ID is not a valid mutation token.
  const at = raw.indexOf("@");
  if (at <= 0 || raw.indexOf("@", at + 1) >= 0) return null;
  const identity = raw.slice(0, at);
  const deadlineText = raw.slice(at + 1);
  if (!identity || identity.includes("|") || !/^[0-9]+$/.test(deadlineText)) return null;
  const expiresAt = Number(deadlineText);
  if (!Number.isSafeInteger(expiresAt) || expiresAt <= 0) return null;
  return {identity, expiresAt};
}

function fenceIdentity(fence) {
  const parsed = parseOperationFence(fence);
  if (parsed) return parsed.identity;
  const raw = String(fence || "");
  const pipe = raw.indexOf("|");
  if (pipe > 0) return raw.slice(0, pipe);
  const at = raw.lastIndexOf("@");
  return at > 0 ? raw.slice(0, at) : raw;
}

// A mutation holds its already-admitted lease for the duration of the
// Playwright call. The original fence token may expire while a
// close/navigate/open is in flight; the operation that was admitted while the
// token was live must still finish, so its expiry is suspended until fn()
// returns. The hold is local to this operation, so revocation/release still
// proceed through the session gate and are not blocked indefinitely.
function beginOperationHold(session, failHold) {
  if (!session.fence) return {fence: "", release() {}};
  if (typeof failHold === "function" ? failHold() : failHold) return null;
  return {fence: session.fence, release() {}};
}

// Returns "" when the call may proceed, otherwise the fence error code. The
// caller-provided deadline is a hint with a hard upper bound, never an
// unlimited lease: malformed, oversized or expired values fail closed.
function fenceStatus(session, supplied, hold = null) {
  if (!session.fence) return "";
  const parsed = parseOperationFence(supplied);
  if (!parsed) return "browser_fence_invalid";
  if (parsed.identity !== fenceIdentity(session.fence)) return "browser_fence_stale";
  const now = Date.now();
  if (parsed.expiresAt > now + OPERATION_DEADLINE_MAX_MS + OPERATION_CLOCK_SKEW_MS) {
    return "browser_fence_invalid";
  }
  const held = Boolean(hold && hold.fence === session.fence);
  if (parsed.expiresAt <= now && !held) return "browser_fence_expired";
  return "";
}

function fenceFailure(response, code) {
  if (code === "browser_fence_invalid") {
    return json(response, 400, fail(400, code, "operation fence deadline is invalid").body);
  }
  const message = code === "browser_fence_expired"
    ? "browser control lease expired"
    : code === "browser_lease_hold_failed"
      ? "browser lease hold could not be established"
      : "browser control lease changed";
  return json(response, 409, fail(409, code, message).body);
}

async function runMutation(response, session, suppliedFence, beforeMutation, failLeaseHold, fn) {
  try {
    return await withSessionGate(session, "read", OPERATION_WAIT_TIMEOUT_MS, async () => {
      let code = fenceStatus(session, suppliedFence);
      if (code) return fenceFailure(response, code);
      const hold = beginOperationHold(session, failLeaseHold);
      if (!hold) return fenceFailure(response, "browser_lease_hold_failed");
      try {
        if (typeof beforeMutation === "function") {
          await beforeMutation({session, fence: suppliedFence});
        }
        code = fenceStatus(session, suppliedFence, hold);
        if (code) return fenceFailure(response, code);
        // The callback receives the hold so a serialized operation can re-run
        // the fence check inside the execution slot.
        return await fn(hold);
      } finally {
        hold.release();
      }
    });
  } catch (error) {
    if (error && error.code === "browser_operation_busy") {
      return json(response, 409, fail(409, "browser_operation_busy", "browser is busy").body);
    }
    throw error;
  }
}

function sessionPayload(session, reused = false) {
  return {
    session: {
      id: session.id,
      profile_id: session.profileID,
      state: session.state,
      error: session.error || "",
      fence: session.fence || "",
      reused,
    },
  };
}

function tabPayload(tab) {
  return {tab: {id: tab.id, url: tab.url, title: tab.title}};
}

function sendFrame(response, buffer, cached) {
  response.writeHead(200, {
    "Content-Type": "image/jpeg",
    "Cache-Control": "no-store",
    "Content-Length": buffer.length,
    "X-AHA2-Frame-Cached": cached ? "1" : "0",
  });
  response.end(buffer);
}

// options: root, token, hostID, chromium (Playwright namespace), edgeExecutable
export function createHostServer(options) {
  const {root, token, hostID = "local", chromium, edgeExecutable, onShutdown, fenceWaitTimeoutMs = FENCE_WAIT_TIMEOUT_MS, beforeBookmarkOpen, beforeMutation, failLeaseHold, headless = false, downloadOptions = null} = options;
  if (!chromium || typeof chromium.launchPersistentContext !== "function") {
    throw new Error("createHostServer requires a Playwright-compatible chromium");
  }
  if (typeof edgeExecutable !== "function") {
    throw new Error("createHostServer requires edgeExecutable()");
  }
  const state = new HostState(root);
  const authorized = request => {
    const header = String(request.headers.authorization || "");
    if (!header.startsWith("Bearer ")) return false;
    const supplied = Buffer.from(header.slice(7));
    const expected = Buffer.from(token);
    if (supplied.length !== expected.length) return false;
    return timingSafeEqual(supplied, expected);
  };

  const closeSession = async (profileID, reason) => {
    const session = state.get(profileID);
    if (!session) return false;
    session.state = "closed";
    state.sessions.delete(profileID);
    try {
      await session.context.close();
    } catch {
      // The browser may already be gone; the lock still has to be released.
    }
    // Download artifacts are scoped to this browser session. Remove the
    // directory so a later session cannot address an old Task's files.
    try {
      await session.downloads.close();
    } catch {
      // Cleanup is best-effort; the directory is outside every profile.
    }
    await state.locks.release(session.lock);
    console.log(`session closed profile=${profileID} reason=${reason}`);
    return true;
  };

  const route = async (request, response) => {
    if (request.url === "/v1/health" && request.method === "GET") {
      if (!authorized(request)) return json(response, 401, fail(401, "host_unauthorized", "invalid host token").body);
      const edge = edgeExecutable();
      return json(response, 200, {
        host_id: hostID,
        version: VERSION,
        platform: process.platform,
        edge_available: Boolean(edge),
        active_profiles: [...state.sessions.keys()],
      });
    }
    if (!authorized(request)) {
      return json(response, 401, fail(401, "host_unauthorized", "invalid host token").body);
    }
    // AHA's supervisor asks the host to close every browser session and exit
    // before it falls back to killing the process tree.
    if (request.url === "/v1/shutdown" && request.method === "POST") {
      await shutdown();
      json(response, 202, {ok: true});
      if (typeof onShutdown === "function") {
        setTimeout(() => {
          try {
            onShutdown();
          } catch {
            // The supervisor still has the process kill as a fallback.
          }
        }, 10);
      }
      return;
    }
    const url = new URL(request.url, `http://${LOOPBACK}`);
    const parts = url.pathname.split("/").filter(Boolean);
    if (parts[0] !== "v1" || parts[1] !== "profiles" || !safeProfileID(parts[2])) {
      return json(response, 404, fail(404, "not_found", "unknown route").body);
    }
    const profileID = parts[2];
    const directory = profileDir(root, profileID);
    if (!directory) {
      return json(response, 400, fail(400, "browser_profile_invalid", "invalid profile id").body);
    }
    let body = {};
    if (request.method === "POST") {
      const isUpload = parts[3] === "tabs" && parts.length === 6 && parts[5] === "uploads";
      const isCookieImport = parts[3] === "cookies" && parts.length === 4;
      const limit = isUpload ? MAX_UPLOAD_BODY_BYTES : isCookieImport ? MAX_COOKIE_BODY : MAX_BODY;
      try {
        body = await readJSON(request, limit);
      } catch {
        const code = isUpload ? "browser_upload_invalid" : isCookieImport ? "browser_cookie_invalid" : "invalid_json";
        return json(response, 400, fail(400, code, "invalid request body").body);
      }
    }

    // /v1/profiles/{id}/session
    if (parts[3] === "session" && parts.length === 4) {
      if (request.method === "GET") {
        const session = state.get(profileID);
        if (!session) return json(response, 404, fail(404, "browser_session_not_found", "no active session").body);
        return json(response, 200, sessionPayload(session));
      }
      if (request.method === "POST") {
        const existing = state.get(profileID);
        if (existing) {
          return json(response, 200, sessionPayload(existing, true));
        }
        const browserKind = String(body.browser_kind || "edge");
        if (browserKind !== "edge") {
          return json(response, 400, fail(400, "browser_kind_unsupported", "this host only starts Edge profiles").body);
        }
        const edge = edgeExecutable();
        if (!edge) {
          return json(response, 503, fail(503, "browser_edge_unavailable",
            "Microsoft Edge was not found; set AHA2_EDGE_PATH. Refusing to fall back to Chrome/Chromium.").body);
        }
        let lock = null;
        try {
          lock = await state.locks.acquire(profileID);
        } catch (error) {
          const code = error && error.code === "browser_profile_locked" ? "browser_profile_locked" : "browser_profile_unavailable";
          return json(response, 409, fail(409, code,
            "该 Profile 的用户数据目录已被另一个浏览器进程占用").body);
        }
        try {
          const context = await chromium.launchPersistentContext(directory, {
            executablePath: edge,
            // Production is always visible. The override exists only for the
            // opt-in real-Chromium regression harness.
            headless,
            viewport: null,
            args: ["--disable-features=msEdgeSidebar"],
          });
          const sessionID = `host-session-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
          const session = {
            id: sessionID,
            profileID,
            state: "ready",
            context,
            tabs: new Map(),
            downloads: createDownloadStore(root, profileID, sessionID, downloadOptions || {}),
            lock,
            fence: "",
            gate: createSessionGate(),
            inputSlot: createSerialSlot(),
          };
          // A persistent context starts with an initial page and the Owner can
          // open more at any time. Register them now and on every new page so a
          // later Task binding can address the page the Owner is looking at.
          try {
            for (const page of context.pages()) attachPage(session, page);
          } catch {
            // Older test doubles may not implement pages(); tab open still works.
          }
          try {
            context.on("page", page => attachPage(session, page));
          } catch {
            // Listener support is optional for injected fakes.
          }
          context.on("close", () => {
            if (state.get(profileID) === session) {
              state.sessions.delete(profileID);
              void state.locks.release(lock);
              void session.downloads.close().catch(() => {});
              console.log(`browser window closed profile=${profileID}`);
            }
          });
          state.sessions.set(profileID, session);
          console.log(`session opened profile=${profileID}`);
          return json(response, 200, sessionPayload(session));
        } catch (error) {
          await state.locks.release(lock);
          // Raw Playwright text can contain the user-data directory or Edge
          // executable path; keep it in the host-local log only, sanitized.
          console.error(`browser launch failed profile=${profileID} diagnostic=${diagnostics(error, root, edge)}`);
          return json(response, 500, fail(500, "browser_launch_failed",
            "浏览器启动失败，请查看浏览器宿主本地日志").body);
        }
      }
      if (request.method === "DELETE") {
        const closed = await closeSession(profileID, "owner_closed");
        if (!closed) return json(response, 404, fail(404, "browser_session_not_found", "no active session").body);
        return json(response, 200, {ok: true});
      }
    }

    // /v1/profiles/{id}/fence
    if (parts[3] === "fence" && parts.length === 4 && request.method === "POST") {
      const session = state.get(profileID);
      if (!session) return json(response, 404, fail(404, "browser_session_not_found", "no active session").body);
      const suppliedSession = String(body.session_id || "");
      if (suppliedSession && suppliedSession !== session.id) {
        return json(response, 409, fail(409, "browser_session_changed", "session id mismatch").body);
      }
      const expected = String(body.expected_fence || "");
      const nextFence = String(body.fence || "");
      if ((session.fence || "") !== expected) return fenceFailure(response, "browser_fence_stale");
      // Invalidate the old fence immediately so paused operations abort at
      // their side-effect boundary, then wait for in-flight operations. New
      // readers may still run (and fail if their fence is stale), which lets
      // the next holder take a snapshot while an old operation is paused.
      session.fence = nextFence;
      try {
        const release = await session.gate.acquire("write", fenceWaitTimeoutMs);
        release();
        return json(response, 200, {ok: true});
      } catch (error) {
        if (error && error.code === "browser_fence_timeout") {
          if (session.fence === nextFence) session.fence = expected;
          return json(response, 409, fail(409, "browser_fence_timeout", "timed out waiting for in-flight browser operations").body);
        }
        throw error;
      }
    }

    // /v1/profiles/{id}/bookmarks
    if (parts[3] === "bookmarks" && parts.length === 4 && request.method === "GET") {
      const session = state.get(profileID);
      if (!session) return json(response, 404, fail(404, "browser_session_not_found", "no active session").body);
      const suppliedSession = String(url.searchParams.get("session_id") || "");
      if (suppliedSession && suppliedSession !== session.id) {
        return json(response, 409, fail(409, "browser_session_changed", "session id mismatch").body);
      }
      try {
        return json(response, 200, {bookmarks: await readProfileBookmarks(root, profileID, directory, beforeBookmarkOpen)});
      } catch (error) {
        console.error(`bookmarks failed profile=${profileID} diagnostic=${diagnostics(error, root, "")}`);
        return json(response, 502, fail(502, "browser_bookmarks_failed", "读取收藏夹失败").body);
      }
    }

    // /v1/profiles/{id}/downloads[/{download}]
    if (parts[3] === "downloads" && parts.length >= 4 && parts.length <= 5) {
      const session = state.get(profileID);
      if (!session) return json(response, 404, fail(404, "browser_session_not_found", "no active session").body);
      const suppliedSession = String(url.searchParams.get("session_id") || "");
      if (suppliedSession && suppliedSession !== session.id) {
        return json(response, 409, fail(409, "browser_session_changed", "session id mismatch").body);
      }
      if (parts.length === 4 && request.method === "GET") {
        try {
          return json(response, 200, {downloads: await session.downloads.list(session.fence)});
        } catch {
          return json(response, 502, fail(502, "browser_download_failed", "读取下载列表失败").body);
        }
      }
      if (parts.length === 4 && request.method === "DELETE") {
        await session.downloads.clear();
        return json(response, 200, {ok: true});
      }
      if (parts.length === 5 && request.method === "GET") {
        let item = null;
        try {
          item = await session.downloads.read(parts[4], session.fence);
        } catch {
          item = null;
        }
        if (!item) {
          return json(response, 404, fail(404, "browser_download_not_found", "download not found").body);
        }
        response.writeHead(200, {
          "Content-Type": "application/octet-stream",
          "Content-Length": item.data.length,
          "Cache-Control": "no-store",
          "X-Content-Type-Options": "nosniff",
          "X-AHA2-Download-Filename": encodeURIComponent(item.entry.name),
        });
        response.end(item.data);
        return;
      }
    }

    // /v1/profiles/{id}/cookies
    if (parts[3] === "cookies" && parts.length === 4) {
      const session = state.get(profileID);
      if (!session) return json(response, 404, fail(404, "browser_session_not_found", "no active session").body);
      const suppliedSession = String(body.session_id || url.searchParams.get("session_id") || "");
      if (suppliedSession && suppliedSession !== session.id) {
        return json(response, 409, fail(409, "browser_session_changed", "session id mismatch").body);
      }
      if (request.method === "GET") {
        const targetURL = validateURL(url.searchParams.get("url") || "");
        const rawDomain = String(url.searchParams.get("domain") || "").trim();
        if (!targetURL && !rawDomain) {
          return json(response, 400, fail(400, "browser_cookie_invalid", "url or domain is required").body);
        }
        let domain = "";
        try {
          if (rawDomain) domain = normalizeCookieDomain(rawDomain);
        } catch {
          return json(response, 400, fail(400, "browser_cookie_invalid", "invalid cookie domain").body);
        }
        let cookies;
        try {
          const raw = targetURL
            ? await session.context.cookies([targetURL])
            : await session.context.cookies();
          cookies = (Array.isArray(raw) ? raw : []).map(exportCookie);
        } catch {
          return json(response, 502, fail(502, "browser_cookie_export_failed", "读取 Cookie 失败").body);
        }
        if (domain) cookies = cookies.filter(cookie => cookieDomainMatches(cookie.domain, domain));
        let truncated = false;
        if (cookies.length > MAX_COOKIES) {
          cookies = cookies.slice(0, MAX_COOKIES);
          truncated = true;
        }
        return json(response, 200, {cookies, truncated});
      }
      if (request.method === "POST") {
        let cookies;
        try {
          if (!Array.isArray(body.cookies) || body.cookies.length < 1 || body.cookies.length > MAX_COOKIES) {
            throw new Error("invalid cookie count");
          }
          cookies = body.cookies.map(cookie => normalizeCookie(cookie));
        } catch {
          return json(response, 400, fail(400, "browser_cookie_invalid", "invalid cookie body").body);
        }
        return runMutation(response, session, body.fence, beforeMutation, failLeaseHold, async () => {
          try {
            if (typeof session.context.addCookies !== "function") throw new Error("cookies unavailable");
            await session.context.addCookies(cookies.map(toPlaywrightCookie));
            return json(response, 200, {count: cookies.length});
          } catch {
            // Never include a cookie value in the response or a log line.
            return json(response, 502, fail(502, "browser_cookie_import_failed", "写入 Cookie 失败").body);
          }
        });
      }
    }

    // /v1/profiles/{id}/tabs and /v1/profiles/{id}/tabs/{tab}[/...]
    if (parts[3] === "tabs") {
      const session = state.get(profileID);
      if (!session) return json(response, 404, fail(404, "browser_session_not_found", "no active session").body);
      const suppliedSession = String(body.session_id || url.searchParams.get("session_id") || "");
      if (suppliedSession && suppliedSession !== session.id) {
        return json(response, 409, fail(409, "browser_session_changed", "session id mismatch").body);
      }
      if (parts.length === 4 && request.method === "GET") {
        try {
          return json(response, 200, {tabs: await listSessionPages(session)});
        } catch (error) {
          console.error(`tab list failed profile=${profileID} diagnostic=${diagnostics(error, root, "")}`);
          return json(response, 502, fail(502, "browser_tab_list_failed", "读取当前页面失败").body);
        }
      }
      if (parts.length === 4 && request.method === "POST") {
        const target = validateURL(body.url);
        if (!target) return json(response, 400, fail(400, "browser_url_invalid", "only http/https URLs are accepted").body);
        return runMutation(response, session, body.fence, beforeMutation, failLeaseHold, async () => {
          try {
            const id = newTabID();
            const opened = await openTabPage(session.context, target, id);
            const tab = attachPage(session, opened.page, id);
            tab.url = opened.url;
            tab.title = opened.title;
            return json(response, 201, tabPayload(tab));
          } catch (error) {
            // Do not reference Edge here: this path must work even when the
            // failure happened outside a launch, and the variable does not exist
            // in this scope.
            console.error(`tab open failed profile=${profileID} diagnostic=${diagnostics(error, root, "")}`);
            return json(response, 502, fail(502, "browser_navigation_failed",
              "页面打开失败或超时").body);
          }
        });
      }
      const tab = parts[4] ? session.tabs.get(parts[4]) : null;
      if (!tab) return json(response, 404, fail(404, "browser_tab_not_found", "tab not found").body);
      if (parts.length === 5 && request.method === "DELETE") {
        return runMutation(response, session, url.searchParams.get("fence") || "", beforeMutation, failLeaseHold, async () => {
          await tab.page.close().catch(() => {});
          session.tabs.delete(tab.id);
          return json(response, 200, {ok: true});
        });
      }
      if (parts.length === 6 && parts[5] === "navigate" && request.method === "POST") {
        const target = validateURL(body.url);
        if (!target) return json(response, 400, fail(400, "browser_url_invalid", "only http/https URLs are accepted").body);
        return runMutation(response, session, body.fence, beforeMutation, failLeaseHold, async () => {
          try {
            const navigated = await navigatePage(tab.page, target);
            tab.url = navigated.url;
            tab.title = navigated.title;
            tab.frame = null;
            tab.framePending = null;
            // A document change invalidates every element ref from the old page.
            tab.refGeneration += 1;
            tab.refsAt = 0;
            return json(response, 200, tabPayload(tab));
          } catch (error) {
            console.error(`tab navigation failed profile=${profileID} diagnostic=${diagnostics(error, root, "")}`);
            return json(response, 502, fail(502, "browser_navigation_failed",
              "页面打开失败或超时").body);
          }
        });
      }
      if (parts.length === 6 && parts[5] === "activate" && request.method === "POST") {
        return runMutation(response, session, body.fence, beforeMutation, failLeaseHold, async () => {
          try {
            await tab.page.bringToFront();
            return json(response, 200, tabPayload(tab));
          } catch (error) {
            console.error(`tab activate failed profile=${profileID} diagnostic=${diagnostics(error, root, "")}`);
            return json(response, 502, fail(502, "browser_activation_failed", "切换浏览器页面失败").body);
          }
        });
      }
      if (parts.length === 6 && parts[5] === "frame" && request.method === "GET") {
        // Read-only preview: one JPEG of the page viewport. Frames are throttled
        // to FRAME_MIN_INTERVAL_MS and reused while the tab is unchanged, so a
        // fast client cannot turn into an unbounded screenshot loop.
        const now = Date.now();
        if (tab.frame && now - tab.frame.at < FRAME_MIN_INTERVAL_MS) {
          return sendFrame(response, tab.frame.buffer, true);
        }
        try {
          if (!tab.framePending) {
            const capture = (async () => {
              const buffer = await tab.page.screenshot({
                type: "jpeg", quality: FRAME_QUALITY, timeout: 5000,
              });
              if (!Buffer.isBuffer(buffer) || buffer.length === 0) {
                throw new Error("browser screenshot returned no data");
              }
              if (buffer.length > MAX_FRAME_BYTES) {
                throw new Error("browser screenshot exceeded the frame size limit");
              }
              return buffer;
            })();
            tab.framePending = capture;
            capture.finally(() => {
              if (tab.framePending === capture) tab.framePending = null;
            }).catch(() => {});
          }
          const buffer = await tab.framePending;
          tab.frame = {at: Date.now(), buffer};
          return sendFrame(response, buffer, false);
        } catch (error) {
          console.error(`tab frame failed profile=${profileID} diagnostic=${diagnostics(error, root, "")}`);
          return json(response, 502, fail(502, "browser_frame_failed", "读取页面画面失败").body);
        }
      }
      if (parts.length === 6 && parts[5] === "snapshot" && request.method === "GET") {
        try {
          const current = {url: tab.page.url(), title: await tab.page.title()};
          // Structured text and a bounded element list only: no cookies,
          // storage, input values, HTML or CDP URL leave the host.
          const text = await tab.page.evaluate(limit => {
            const body = document.body ? document.body.innerText : "";
            return String(body || "").slice(0, limit);
          }, MAX_SNAPSHOT_TEXT);
          let elements = [];
          try {
            tab.refGeneration += 1;
            tab.refsAt = Date.now();
            elements = await captureElements(tab.page, `e${tab.refGeneration}-`);
          } catch {
            // A page that cannot be introspected still yields its text; every
            // earlier ref is stale because the generation advanced.
            elements = [];
          }
          return json(response, 200, {snapshot: {...current, text, elements}});
        } catch (error) {
          console.error(`tab snapshot failed profile=${profileID} diagnostic=${diagnostics(error, root, "")}`);
          return json(response, 502, fail(502, "browser_snapshot_failed",
            "读取页面失败").body);
        }
      }
      if (parts.length === 6 && parts[5] === "actions" && request.method === "POST") {
        let action = null;
        try {
          action = normalizeAction(body);
        } catch (error) {
          const code = error && error.code ? error.code : "browser_action_invalid";
          return json(response, 400, fail(400, code, "invalid page action").body);
        }
        return runMutation(response, session, body.fence, beforeMutation, failLeaseHold, async hold => {
          // Serialize the whole focused-click/type sequence. The session gate
          // admits concurrent readers for fence isolation, so it cannot order
          // two input gestures by itself.
          return session.inputSlot.run(async () => {
            // The fence may have changed while this request waited in the
            // slot; re-check before touching the page.
            const fenceCode = fenceStatus(session, body.fence, hold);
            if (fenceCode) return fenceFailure(response, fenceCode);
            try {
              const result = await performAction(tab.page, tab, action);
              tab.frame = null;
              return json(response, 200, {action: result});
            } catch (error) {
              const code = error && error.code ? error.code : "browser_action_failed";
              const status = code === "browser_element_not_found" ? 404
                : code === "browser_element_stale" ? 409
                  : code === "browser_action_invalid" ? 400 : 502;
              // Never echo Playwright's diagnostic: it can contain typed text
              // or page-derived selectors.
              return json(response, status, fail(status, code, "page action failed").body);
            }
          });
        });
      }
      if (parts.length === 6 && parts[5] === "uploads" && request.method === "POST") {
        return runMutation(response, session, body.fence, beforeMutation, failLeaseHold, async hold => {
          return session.inputSlot.run(async () => {
            const fenceCode = fenceStatus(session, body.fence, hold);
            if (fenceCode) return fenceFailure(response, fenceCode);
            try {
              const result = await performUpload(tab.page, tab, body);
              tab.frame = null;
              return json(response, 200, {upload: result});
            } catch (error) {
              const code = error && error.code ? error.code : "browser_upload_failed";
              const status = code === "browser_element_not_found" ? 404
                : code === "browser_element_stale" ? 409
                  : code === "browser_upload_invalid" ? 400 : 502;
              return json(response, status, fail(status, code, "file upload failed").body);
            }
          });
        });
      }
    }
    return json(response, 404, fail(404, "not_found", "unknown route").body);
  };

  const server = createServer((request, response) => {
    route(request, response).catch(error => {
      console.error("host request failed", diagnostics(error, root, ""));
      if (!response.headersSent) {
        json(response, 500, fail(500, "host_internal_error", "host request failed").body);
      }
    });
  });

  const shutdown = async () => {
    for (const profileID of [...state.sessions.keys()]) {
      await closeSession(profileID, "host_shutdown");
    }
  };

  return {server, state, shutdown, closeSession};
}
