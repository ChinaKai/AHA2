// Loopback HTTP route layer for the AHA2 browser host. host.mjs is the CLI
// entry point; this module is injectable so route failures can be tested with a
// fake Playwright implementation and no real Edge install.

import {createServer} from "node:http";
import {timingSafeEqual} from "node:crypto";
import {promises as fs} from "node:fs";
import {createProfileLocks, profileDir, safeProfileID} from "./lock.mjs";
import {navigatePage, openTabPage} from "./tabs.mjs";

export const VERSION = "0.1.0";
export const LOOPBACK = "127.0.0.1";
const MAX_BODY = 64 * 1024;
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

async function readJSON(request) {
  const chunks = [];
  let size = 0;
  for await (const chunk of request) {
    size += chunk.length;
    if (size > MAX_BODY) throw new Error("request body too large");
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

function sessionPayload(session, reused = false) {
  return {
    session: {
      id: session.id,
      profile_id: session.profileID,
      state: session.state,
      error: session.error || "",
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
  const {root, token, hostID = "local", chromium, edgeExecutable, onShutdown} = options;
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
      try {
        body = await readJSON(request);
      } catch {
        return json(response, 400, fail(400, "invalid_json", "invalid request body").body);
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
            headless: false,
            viewport: null,
            args: ["--disable-features=msEdgeSidebar"],
          });
          const session = {
            id: `host-session-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`,
            profileID,
            state: "ready",
            context,
            tabs: new Map(),
            lock,
          };
          context.on("close", () => {
            if (state.get(profileID) === session) {
              state.sessions.delete(profileID);
              void state.locks.release(lock);
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

    // /v1/profiles/{id}/tabs and /v1/profiles/{id}/tabs/{tab}[/...]
    if (parts[3] === "tabs") {
      const session = state.get(profileID);
      if (!session) return json(response, 404, fail(404, "browser_session_not_found", "no active session").body);
      const suppliedSession = String(body.session_id || url.searchParams.get("session_id") || "");
      if (suppliedSession && suppliedSession !== session.id) {
        return json(response, 409, fail(409, "browser_session_changed", "session id mismatch").body);
      }
      if (parts.length === 4 && request.method === "POST") {
        const target = validateURL(body.url);
        if (!target) return json(response, 400, fail(400, "browser_url_invalid", "only http/https URLs are accepted").body);
        try {
          const id = `host-tab-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
          const tab = await openTabPage(session.context, target, id);
          session.tabs.set(tab.id, tab);
          tab.page.on("close", () => session.tabs.delete(tab.id));
          return json(response, 201, tabPayload(tab));
        } catch (error) {
          // Do not reference Edge here: this path must work even when the
          // failure happened outside a launch, and the variable does not exist
          // in this scope.
          console.error(`tab open failed profile=${profileID} diagnostic=${diagnostics(error, root, "")}`);
          return json(response, 502, fail(502, "browser_navigation_failed",
            "页面打开失败或超时").body);
        }
      }
      const tab = parts[4] ? session.tabs.get(parts[4]) : null;
      if (!tab) return json(response, 404, fail(404, "browser_tab_not_found", "tab not found").body);
      if (parts.length === 5 && request.method === "DELETE") {
        await tab.page.close().catch(() => {});
        session.tabs.delete(tab.id);
        return json(response, 200, {ok: true});
      }
      if (parts.length === 6 && parts[5] === "navigate" && request.method === "POST") {
        const target = validateURL(body.url);
        if (!target) return json(response, 400, fail(400, "browser_url_invalid", "only http/https URLs are accepted").body);
        try {
          const navigated = await navigatePage(tab.page, target);
          tab.url = navigated.url;
          tab.title = navigated.title;
          tab.frame = null;
          tab.framePending = null;
          return json(response, 200, tabPayload(tab));
        } catch (error) {
          console.error(`tab navigation failed profile=${profileID} diagnostic=${diagnostics(error, root, "")}`);
          return json(response, 502, fail(502, "browser_navigation_failed",
            "页面打开失败或超时").body);
        }
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
          // Structured text only: no cookies, storage, HTML or CDP URL leave
          // the host.
          const text = await tab.page.evaluate(limit => {
            const body = document.body ? document.body.innerText : "";
            return String(body || "").slice(0, limit);
          }, MAX_SNAPSHOT_TEXT);
          return json(response, 200, {snapshot: {...current, text}});
        } catch (error) {
          console.error(`tab snapshot failed profile=${profileID} diagnostic=${diagnostics(error, root, "")}`);
          return json(response, 502, fail(502, "browser_snapshot_failed",
            "读取页面失败").body);
        }
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
