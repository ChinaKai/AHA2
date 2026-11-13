#!/usr/bin/env node
import {createHash} from "node:crypto";
import {createReadStream} from "node:fs";
import {stat, writeFile} from "node:fs/promises";
import {isAbsolute, resolve, sep} from "node:path";
import {fileURLToPath} from "node:url";

// The release manifest describes a package directory: which files were built,
// their sizes and SHA256 digests, and how the build was invoked. It deliberately
// carries no deployment instructions -- stopping, backing up, replacing and
// rolling back belong to the installer on the target machine.
export const MANIFEST_SCHEMA_VERSION = 1;

function sha256File(path) {
  return new Promise((resolvePromise, reject) => {
    const hash = createHash("sha256");
    const stream = createReadStream(path);
    stream.on("error", reject);
    stream.on("data", (chunk) => hash.update(chunk));
    stream.on("end", () => resolvePromise(hash.digest("hex")));
  });
}

function normalizeRelativePath(path) {
  if (typeof path !== "string" || path.trim() === "") {
    throw new Error("manifest input path must be a non-empty string");
  }
  if (isAbsolute(path) || /^[A-Za-z]:/.test(path)) {
    throw new Error(`manifest input path must be relative to the package root: ${path}`);
  }
  const normalized = path.split(/[\\/]+/).filter((part) => part !== "").join("/");
  if (normalized === "" || normalized === ".." || normalized.startsWith("../") || normalized.split("/").includes("..")) {
    throw new Error(`manifest input path escapes the package root: ${path}`);
  }
  return normalized;
}

function normalizeVersion(version) {
  if (typeof version !== "string" || version.trim() === "") {
    throw new Error("manifest version must be a non-empty string");
  }
  return version.trim().replace(/^v/i, "");
}

function requireNonEmpty(name, value) {
  if (typeof value !== "string" || value.trim() === "") {
    throw new Error(`manifest ${name} must be a non-empty string`);
  }
  return value.trim();
}

function normalizeGenerator(generator = {}) {
  if (generator === null || typeof generator !== "object" || Array.isArray(generator)) {
    throw new Error("manifest generator must be an object");
  }
  const result = {};
  for (const key of ["script", "command", "web_version", "go_version", "node_version"]) {
    const value = generator[key];
    if (value === undefined || value === null || value === "") continue;
    result[key] = requireNonEmpty(`generator.${key}`, value);
  }
  if (!result.script) {
    throw new Error("manifest generator.script must be a non-empty string");
  }
  return result;
}

export async function buildManifest({root, version, commit, builtAt, generator, files} = {}) {
  const rootPath = resolve(requireNonEmpty("root", root));
  const normalizedVersion = normalizeVersion(version);
  const normalizedCommit = requireNonEmpty("commit", commit);
  if (!Array.isArray(files) || files.length === 0) {
    throw new Error("manifest files must list at least one package file");
  }
  const builtAtValue = builtAt === undefined ? new Date().toISOString() : builtAt;
  if (Number.isNaN(Date.parse(builtAtValue))) {
    throw new Error(`manifest built_at is not an RFC3339 timestamp: ${builtAtValue}`);
  }

  const seen = new Set();
  const entries = [];
  for (const file of files) {
    const relativePath = normalizeRelativePath(file);
    if (seen.has(relativePath)) {
      throw new Error(`manifest input is listed twice: ${relativePath}`);
    }
    seen.add(relativePath);
    const absolutePath = resolve(rootPath, relativePath);
    if (absolutePath !== rootPath && !absolutePath.startsWith(`${rootPath}${sep}`)) {
      throw new Error(`manifest input path escapes the package root: ${relativePath}`);
    }
    let info;
    try {
      info = await stat(absolutePath);
    } catch {
      throw new Error(`manifest input is missing: ${relativePath}`);
    }
    if (!info.isFile()) {
      throw new Error(`manifest input is not a regular file: ${relativePath}`);
    }
    entries.push({path: relativePath, size: info.size, sha256: await sha256File(absolutePath)});
  }
  entries.sort((left, right) => (left.path < right.path ? -1 : left.path > right.path ? 1 : 0));

  return {
    schema_version: MANIFEST_SCHEMA_VERSION,
    product: "AHA2",
    version: normalizedVersion,
    commit: normalizedCommit,
    built_at: new Date(builtAtValue).toISOString(),
    generator: normalizeGenerator(generator),
    files: entries,
  };
}

export async function writeManifest(options) {
  const manifest = await buildManifest(options);
  const output = resolve(requireNonEmpty("output", options.output));
  await writeFile(output, `${JSON.stringify(manifest, null, 2)}\n`, "utf8");
  return manifest;
}

function parseArgs(argv) {
  const options = {files: []};
  const valueFlags = {
    "--root": "root",
    "--version": "version",
    "--commit": "commit",
    "--built-at": "builtAt",
    "--output": "output",
    "--generator-script": "generatorScript",
    "--generator-command": "generatorCommand",
    "--web-version": "webVersion",
    "--go-version": "goVersion",
    "--node-version": "nodeVersion",
  };
  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    if (arg === "-h" || arg === "--help") {
      options.help = true;
      continue;
    }
    if (arg.startsWith("--")) {
      const key = valueFlags[arg];
      if (!key) throw new Error(`unknown option: ${arg}`);
      if (index + 1 >= argv.length) throw new Error(`missing value for ${arg}`);
      options[key] = argv[index + 1];
      index += 1;
      continue;
    }
    options.files.push(arg);
  }
  return options;
}

const usage = `Usage: node scripts/release-manifest.mjs \\
  --root <package-dir> --version <x.y.z> --commit <sha> --output <manifest.json> \\
  --generator-script <path> [--generator-command <text>] \\
  [--web-version <v>] [--go-version <go>] [--node-version <v>] [--built-at <RFC3339>] \\
  <file>...`;

async function main(argv) {
  const options = parseArgs(argv);
  if (options.help) {
    console.log(usage);
    return;
  }
  const manifest = await writeManifest({
    root: options.root,
    version: options.version,
    commit: options.commit,
    builtAt: options.builtAt,
    output: options.output,
    generator: {
      script: options.generatorScript,
      command: options.generatorCommand,
      web_version: options.webVersion,
      go_version: options.goVersion,
      node_version: options.nodeVersion,
    },
    files: options.files,
  });
  console.log(`Wrote ${resolve(options.output)} (${manifest.files.length} files, version ${manifest.version})`);
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2)).catch((error) => {
    console.error(`release-manifest: ${error.message}`);
    process.exitCode = 1;
  });
}
