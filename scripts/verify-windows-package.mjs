#!/usr/bin/env node
import {createHash} from "node:crypto";
import {createReadStream, lstatSync, readdirSync, readFileSync, readlinkSync} from "node:fs";
import {resolve} from "node:path";
import {fileURLToPath} from "node:url";
import {assertPackageManifest} from "./release-manifest.mjs";

// verify-windows-package checks the bytes the installer is about to copy
// against release-manifest.json. The comparison is bidirectional: every
// manifest entry must have an installer input, and every file the installer
// copies (including the recursively copied browser-host tree) must be listed.
// The installed manifest is the deploy verification contract, so a file that
// is installed without a hash would silently weaken it.

function sha256File(path) {
  return new Promise((resolvePromise, reject) => {
    const hash = createHash("sha256");
    const stream = createReadStream(path);
    stream.on("error", reject);
    stream.on("data", (chunk) => hash.update(chunk));
    stream.on("end", () => resolvePromise(hash.digest("hex")));
  });
}

// lstat plus readlink catches both POSIX symlinks and Windows junctions or
// other reparse points. Following one would let the installer copy bytes that
// are not hashed by the manifest, so both are rejected before any file is read.
function assertPlainEntry(path, label) {
  let info;
  try {
    info = lstatSync(path);
  } catch {
    throw new Error(`${label} is missing: ${path}`);
  }
  if (info.isSymbolicLink()) {
    throw new Error(`${label} is a symlink, which the installer would copy without a manifest hash: ${path}`);
  }
  let isReparsePoint = false;
  try {
    readlinkSync(path);
    isReparsePoint = true;
  } catch (error) {
    if (!error || error.code !== "EINVAL") throw error;
  }
  if (isReparsePoint) {
    throw new Error(`${label} is a reparse point, which the installer would copy without a manifest hash: ${path}`);
  }
  return info;
}

function addBrowserHost(inputs, browserHost) {
  const root = resolve(browserHost);
  const rootInfo = assertPlainEntry(root, "browser host root");
  if (!rootInfo.isDirectory()) {
    throw new Error(`browser host root is not a directory: ${root}`);
  }
  function walk(directory, relativeDir) {
    for (const name of readdirSync(directory)) {
      const relative = `${relativeDir}/${name}`;
      const absolute = resolve(directory, name);
      const info = assertPlainEntry(absolute, `installer input ${relative}`);
      if (info.isDirectory()) {
        walk(absolute, relative);
      } else if (info.isFile()) {
        inputs.set(relative, absolute);
      } else {
        throw new Error(`installer input ${relative} is not a regular file: ${absolute}`);
      }
    }
  }
  walk(root, "browser-host");
}

export function collectInstallInputs({
  server,
  tray,
  browserHost,
  nodeExe,
  nodeLicense,
  feishuPlugin,
  feishuManifest,
} = {}) {
  for (const [name, value] of Object.entries({server, tray, browserHost, nodeExe, nodeLicense})) {
    if (typeof value !== "string" || value === "") {
      throw new Error(`installer input ${name} is required`);
    }
  }
  if (Boolean(feishuPlugin) !== Boolean(feishuManifest)) {
    throw new Error("Feishu plugin executable and manifest must be provided together.");
  }
  const inputs = new Map([
    ["aha2-windows-amd64.exe", resolve(server)],
    ["aha2-tray-windows-amd64.exe", resolve(tray)],
    ["node/node.exe", resolve(nodeExe)],
    ["node/LICENSE", resolve(nodeLicense)],
  ]);
  addBrowserHost(inputs, browserHost);
  if (feishuPlugin) {
    inputs.set("plugins/feishu/windows-amd64/aha2-channel-feishu.exe", resolve(feishuPlugin));
    inputs.set("plugins/feishu/windows-amd64/plugin.json", resolve(feishuManifest));
  }
  return inputs;
}

export async function verifyWindowsPackage({
  manifestPath,
  root,
  version,
  expectedCommit,
  expectedWebVersion,
  inputs,
}) {
  if (typeof root !== "string" || root === "") {
    throw new Error("package root is required");
  }
  assertPlainEntry(resolve(manifestPath), "release manifest");
  const manifest = JSON.parse(readFileSync(manifestPath, "utf8"));
  assertPackageManifest(manifest, {
    version,
    commit: expectedCommit,
    webVersion: expectedWebVersion,
  });

  const entries = new Map(manifest.files.map((file) => [file.path, file]));
  const actual = inputs instanceof Map ? inputs : collectInstallInputs(inputs);

  for (const path of entries.keys()) {
    if (!actual.has(path)) {
      throw new Error(`manifest lists ${path} but the installer does not copy it`);
    }
  }
  for (const path of actual.keys()) {
    if (!entries.has(path)) {
      throw new Error(`installer would copy ${path} but the manifest does not list it`);
    }
  }

  for (const [path, absolutePath] of actual) {
    const info = assertPlainEntry(absolutePath, `installer input ${path}`);
    if (!info.isFile()) {
      throw new Error(`installer input ${path} is not a regular file: ${absolutePath}`);
    }
    const entry = entries.get(path);
    if (entry.size !== info.size) {
      throw new Error(`manifest size ${entry.size} for ${path} does not match installer input size ${info.size}`);
    }
    const digest = await sha256File(absolutePath);
    if (digest !== entry.sha256) {
      throw new Error(`manifest SHA256 for ${path} does not match the installer input`);
    }
  }

  // The manifest itself is installed next to the package, not listed in it.
  if (entries.has("release-manifest.json")) {
    throw new Error("manifest must not list itself");
  }
  return manifest;
}

function parseArgs(argv) {
  const options = {};
  const valueFlags = {
    "--manifest": "manifestPath",
    "--root": "root",
    "--version": "version",
    "--expected-commit": "expectedCommit",
    "--expected-web-version": "expectedWebVersion",
    "--server": "server",
    "--tray": "tray",
    "--browser-host": "browserHost",
    "--node-exe": "nodeExe",
    "--node-license": "nodeLicense",
    "--feishu-plugin": "feishuPlugin",
    "--feishu-manifest": "feishuManifest",
  };
  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    if (arg === "-h" || arg === "--help") {
      options.help = true;
      continue;
    }
    const key = valueFlags[arg];
    if (!key) throw new Error(`unknown option: ${arg}`);
    if (index + 1 >= argv.length) throw new Error(`missing value for ${arg}`);
    options[key] = argv[index + 1];
    index += 1;
  }
  return options;
}

const usage = `Usage: node scripts/verify-windows-package.mjs \\
  --manifest <release-manifest.json> --root <package-dir> --version <x.y.z> \\
  --expected-commit <sha> [--expected-web-version <v>] \\
  [--server <path>] [--tray <path>] [--browser-host <dir>] \\
  [--node-exe <path>] [--node-license <path>] \\
  [--feishu-plugin <path> --feishu-manifest <path>]`;

async function main(argv) {
  const options = parseArgs(argv);
  if (options.help) {
    console.log(usage);
    return;
  }
  for (const required of ["manifestPath", "root", "version", "expectedCommit"]) {
    if (!options[required]) throw new Error(`missing required option: ${required}`);
  }
  const root = resolve(options.root);
  const manifest = await verifyWindowsPackage({
    manifestPath: options.manifestPath,
    root,
    version: options.version,
    expectedCommit: options.expectedCommit,
    expectedWebVersion: options.expectedWebVersion,
    inputs: {
      server: options.server ?? resolve(root, "aha2-windows-amd64.exe"),
      tray: options.tray ?? resolve(root, "aha2-tray-windows-amd64.exe"),
      browserHost: options.browserHost ?? resolve(root, "browser-host"),
      nodeExe: options.nodeExe ?? resolve(root, "node", "node.exe"),
      nodeLicense: options.nodeLicense ?? resolve(root, "node", "LICENSE"),
      feishuPlugin: options.feishuPlugin,
      feishuManifest: options.feishuManifest,
    },
  });
  console.log(`Windows package verified against release-manifest.json (${manifest.files.length} files)`);
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2)).catch((error) => {
    console.error(`verify-windows-package: ${error.message}`);
    process.exitCode = 1;
  });
}
