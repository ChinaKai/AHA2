#!/usr/bin/env node
// AHA2 shared-browser host.
//
// Runs in the owner's interactive desktop session (never Windows Session 0).
// It owns the persistent Edge user-data directories and exposes only a
// loopback, token-authenticated control API to AHA2. It never opens a CDP
// endpoint to AHA or the network, and it never logs the token or page content.
//
// Usage:
//   AHA2_BROWSER_HOST_TOKEN=... \
//   AHA2_BROWSER_PROFILE_ROOT="D:\\ProgramData\\AHA2\\browser-profiles" \
//   node host.mjs --port 8765
//
// See README.md for the full contract and manual acceptance steps.

import {createServer} from "node:http";
import {createHash, timingSafeEqual} from "node:crypto";
import {promises as fs, readFileSync, existsSync} from "node:fs";
import {join, resolve} from "node:path";
import {chromium} from "playwright";
import {createProfileLocks, profileDir, safeProfileID} from "./lock.mjs";
import {navigatePage, openTabPage} from "./tabs.mjs";

const VERSION = "0.1.0";
const LOOPBACK = "127.0.0.1";
const MAX_BODY = 64 * 1024;
const MAX_SNAPSHOT_TEXT = 200_000;

function parseArgs(argv) {
  const options = {port: 8765, dataDir: process.env.AHA2_BROWSER_PROFILE_ROOT || "", tokenFile: ""};
  for (let index = 0; index < argv.length; index++) {
    const value = argv[index];
    if (value === "--port") options.port = Number(argv[++index]);
    else if (value === "--data-dir") options.dataDir = argv[++index];
    else if (value === "--token-file") options.tokenFile = argv[++index];
    else if (value === "--help" || value === "-h") options.help = true;
  }
  return options;
}

function readToken(options) {
  const fromEnv = String(process.env.AHA2_BROWSER_HOST_TOKEN || "").trim();
  if (fromEnv) return fromEnv;
  if (options.tokenFile) {
    try {
      return readFileSync(options.tokenFile, "utf8").trim();
    } catch {
      return "";
    }
  }
  return "";
}

function edgeExecutable() {
  const configured = String(process.env.AHA2_EDGE_PATH || "").trim();
  const candidates = [configured];
  if (process.platform === "win32") {
    const programFiles = process.env.ProgramFiles || "C:\\Program Files";
    const programFilesX86 = process.env["ProgramFiles(x86)"] || "C:\\Program Files (x86)";
    const localAppData = process.env.LOCALAPPDATA || "";
    candidates.push(
      join(programFilesX86, "Microsoft", "Edge", "Application", "msedge.exe"),
      join(programFiles, "Microsoft", "Edge", "Application", "msedge.exe"),
      join(localAppData, "Microsoft", "Edge", "Application", "msedge.exe"),
    );
  }
  for (const candidate of candidates) {
    if (candidate && existsSync(candidate)) return candidate;
  }
  return "";
}

const safeID = safeProfileID;

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
  for (const [value, label] of [[root, "[profile-root]"], [edge, "[edge]"]] ) {
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

async function main() {
  const options = parseArgs(process.argv.slice(2));
  if (options.help) {
    console.log("usage: node host.mjs --port 8765 --data-dir <profile-root>");
    console.log("token: AHA2_BROWSER_HOST_TOKEN (required)");
    return;
  }
  const token = readToken(options);
  if (!token) {
    console.error("AHA2_BROWSER_HOST_TOKEN is required; refusing to start without a local token");
    process.exit(2);
  }
  const root = resolve(options.dataDir || join(process.cwd(), ".data", "browser-profiles"));
  if (!options.dataDir) {
    console.error(`AHA2_BROWSER_PROFILE_ROOT not set; using ${root}`);
  }
  await fs.mkdir(root, {recursive: true, mode: 0o700});
  if (!Number.isInteger(options.port) || options.port < 1 || options.port > 65535) {
    console.error("invalid --port");
    process.exit(2);
  }
  const state = new HostState(root);
  let shuttingDown = false;

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
        host_id: process.env.AHA2_BROWSER_HOST_ID || "local",
        version: VERSION,
        platform: process.platform,
        edge_available: Boolean(edge),
        active_profiles: [...state.sessions.keys()],
      });
    }
    if (!authorized(request)) {
      return json(response, 401, fail(401, "host_unauthorized", "invalid host token").body);
    }
    const url = new URL(request.url, `http://${LOOPBACK}`);
    const parts = url.pathname.split("/").filter(Boolean);
    if (parts[0] !== "v1" || parts[1] !== "profiles" || !safeID(parts[2])) {
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
          console.error(`tab open failed profile=${profileID} diagnostic=${diagnostics(error, root, edge)}`);
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
          return json(response, 200, tabPayload(tab));
        } catch (error) {
          console.error(`tab navigation failed profile=${profileID} diagnostic=${diagnostics(error, root, edge)}`);
          return json(response, 502, fail(502, "browser_navigation_failed",
            "页面打开失败或超时").body);
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
          console.error(`tab snapshot failed profile=${profileID} diagnostic=${diagnostics(error, root, edge)}`);
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

  const shutdown = async signal => {
    if (shuttingDown) return;
    shuttingDown = true;
    console.log(`shutting down (${signal})`);
    for (const profileID of [...state.sessions.keys()]) {
      await closeSession(profileID, "host_shutdown");
    }
    server.close(() => process.exit(0));
    setTimeout(() => process.exit(0), 3000).unref();
  };
  process.on("SIGINT", () => void shutdown("SIGINT"));
  process.on("SIGTERM", () => void shutdown("SIGTERM"));

  // Loopback only. There is deliberately no --listen override.
  server.listen(options.port, LOOPBACK, () => {
    console.log(`AHA2 browser host ${VERSION} listening on http://${LOOPBACK}:${options.port}`);
    console.log(`profile root: ${root}`);
  });
}

// Keep the fingerprint helper honest about the token never being logged: this
// function exists only for diagnostics that opt in explicitly.
export function tokenFingerprint(token) {
  return createHash("sha256").update(String(token)).digest("hex").slice(0, 8);
}

main().catch(error => {
  console.error("browser host failed to start", String(error && error.message || error));
  process.exit(1);
});
