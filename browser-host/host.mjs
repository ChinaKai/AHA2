#!/usr/bin/env node
// AHA2 shared-browser host CLI.
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

import {promises as fs, readFileSync, existsSync} from "node:fs";
import {join, resolve} from "node:path";
import {createHash} from "node:crypto";
import {createHostServer, LOOPBACK, VERSION} from "./server.mjs";
import {createInstanceLock} from "./lock.mjs";

function parseArgs(argv) {
  const options = {port: 8765, dataDir: process.env.AHA2_BROWSER_PROFILE_ROOT || "", tokenFile: "", parentPID: 0};
  for (let index = 0; index < argv.length; index++) {
    const value = argv[index];
    if (value === "--port") options.port = Number(argv[++index]);
    else if (value === "--data-dir") options.dataDir = argv[++index];
    else if (value === "--token-file") options.tokenFile = argv[++index];
    else if (value === "--parent-pid") options.parentPID = Number(argv[++index]);
    else if (value === "--help" || value === "-h") options.help = true;
  }
  return options;
}

// AHA starts the host as a child; if AHA is killed without running its shutdown
// path, the host must not keep an invisible Edge session alive forever.
function watchParent(parentPID, onGone) {
  if (!Number.isInteger(parentPID) || parentPID <= 0) return;
  setInterval(() => {
    try {
      process.kill(parentPID, 0);
    } catch {
      console.error("parent process is gone; shutting down");
      onGone();
    }
  }, 3000).unref();
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

export function edgeExecutable() {
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

async function main() {
  const options = parseArgs(process.argv.slice(2));
  if (options.help) {
    console.log("usage: node host.mjs --port 8765 --data-dir <profile-root> [--parent-pid <pid>]");
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
  let instanceLock;
  try {
    instanceLock = await createInstanceLock(root);
  } catch (error) {
    console.error("another AHA2 browser host is already using this profile root");
    process.exit(3);
  }
  const {chromium} = await import("playwright");
  let shuttingDown = false;
  const host = createHostServer({
    root,
    token,
    hostID: process.env.AHA2_BROWSER_HOST_ID || "local",
    chromium,
    edgeExecutable,
    onShutdown: () => void shutdown("http"),
  });
  const shutdown = async signal => {
    if (shuttingDown) return;
    shuttingDown = true;
    console.log(`shutting down (${signal})`);
    await host.shutdown();
    await instanceLock.release();
    host.server.close(() => process.exit(0));
    setTimeout(() => process.exit(0), 3000).unref();
  };
  process.on("SIGINT", () => void shutdown("SIGINT"));
  process.on("SIGTERM", () => void shutdown("SIGTERM"));
  watchParent(options.parentPID, () => void shutdown("parent-exit"));

  host.server.on("error", async error => {
    console.error("browser host listen failed", String(error && error.message || error));
    await instanceLock.release();
    process.exit(1);
  });
  // Loopback only. There is deliberately no --listen override.
  host.server.listen(options.port, LOOPBACK, () => {
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
