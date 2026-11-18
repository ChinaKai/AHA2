#!/usr/bin/env node
import {createHash} from "node:crypto";
import {createReadStream, readFileSync} from "node:fs";
import {stat, writeFile} from "node:fs/promises";
import {isAbsolute, resolve, sep} from "node:path";
import {fileURLToPath} from "node:url";

// The release manifest describes a package directory: which files were built,
// their sizes and SHA256 digests, and how the build was invoked. It deliberately
// carries no deployment instructions -- stopping, backing up, replacing and
// rolling back belong to the installer on the target machine.
export const MANIFEST_SCHEMA_VERSION = 1;

// The scope file is shared with the installer verifier and the Release archive
// checker so the three code paths cannot disagree about which package files
// the official installer copies.
export const PACKAGE_SCOPE = JSON.parse(
  readFileSync(new URL("./windows-package-scope.json", import.meta.url), "utf8"),
);

// The official installer places these exact paths under the install directory
// (or, for the Feishu plugin, under the selected data directory). Anything else
// in the manifest would describe a file the installer never writes, so package
// validation rejects it. Keep this list in sync with installer/windows/AHA2.iss
// and the fixed mapping used by the host deploy script.
export const REQUIRED_PACKAGE_FILES = PACKAGE_SCOPE.required;

const EXACT_PACKAGE_FILES = new Set(PACKAGE_SCOPE.exact);
const PACKAGE_FILE_PREFIXES = PACKAGE_SCOPE.prefixes;

export function packagePathInScope(path) {
  return EXACT_PACKAGE_FILES.has(path) || PACKAGE_FILE_PREFIXES.some((prefix) => path.startsWith(prefix));
}

// assertPackageManifest is the release-side gate: it re-checks a manifest that
// buildManifest produced before the installer is built. It covers identity,
// path safety, the required install scope and the absence of files the
// installer would not copy.
export function assertPackageManifest(manifest, expected = {}) {
  if (manifest === null || typeof manifest !== "object" || Array.isArray(manifest)) {
    throw new Error("manifest must be a JSON object");
  }
  if (manifest.schema_version !== MANIFEST_SCHEMA_VERSION) {
    throw new Error(`manifest schema_version ${manifest.schema_version} != ${MANIFEST_SCHEMA_VERSION}`);
  }
  if (manifest.product !== "AHA2") {
    throw new Error(`manifest product ${manifest.product} != AHA2`);
  }
  const version = normalizeVersion(manifest.version);
  if (expected.version !== undefined && version !== normalizeVersion(expected.version)) {
    throw new Error(`manifest version ${version} != ${normalizeVersion(expected.version)}`);
  }
  const commit = requireNonEmpty("commit", manifest.commit);
  if (!/^[0-9a-fA-F]{12,40}$/.test(commit)) {
    throw new Error(`manifest commit is not 12 to 40 hexadecimal characters: ${commit}`);
  }
  if (expected.commit !== undefined && commit.toLowerCase() !== String(expected.commit).toLowerCase()) {
    throw new Error(`manifest commit ${commit} != ${expected.commit}`);
  }
  const builtAt = requireNonEmpty("built_at", manifest.built_at);
  if (Number.isNaN(Date.parse(builtAt))) {
    throw new Error(`manifest built_at is not an RFC3339 timestamp: ${builtAt}`);
  }
  const generator = normalizeGenerator(manifest.generator);
  const webVersion = requireNonEmpty("generator.web_version", generator.web_version);
  const webVersionPattern = new RegExp(`^v${escapeRegExp(version)}\\.(\\d{8})\\.([0-9a-f]{12})(\\.dirty)?$`);
  const webVersionMatch = webVersionPattern.exec(webVersion);
  if (!webVersionMatch) {
    throw new Error(
      `manifest generator.web_version ${webVersion} must look like v${version}.<yyyymmdd>.<commit12>`,
    );
  }
  const buildDate = webVersionMatch[1];
  const parsedDate = new Date(`${buildDate.slice(0, 4)}-${buildDate.slice(4, 6)}-${buildDate.slice(6, 8)}T00:00:00Z`);
  if (Number.isNaN(parsedDate.getTime()) || parsedDate.toISOString().slice(0, 10) !== `${buildDate.slice(0, 4)}-${buildDate.slice(4, 6)}-${buildDate.slice(6, 8)}`) {
    throw new Error(`manifest generator.web_version ${webVersion} carries an invalid build date`);
  }
  const webVersionCommit = webVersionMatch[2];
  if (webVersionCommit !== commit.slice(0, 12).toLowerCase()) {
    throw new Error(
      `manifest generator.web_version commit ${webVersionCommit} does not match manifest commit ${commit}`,
    );
  }
  if (expected.webVersion !== undefined && webVersion !== expected.webVersion) {
    throw new Error(`manifest generator.web_version ${webVersion} != ${expected.webVersion}`);
  }
  if (expected.generatorScript !== undefined && generator.script !== expected.generatorScript) {
    throw new Error(`manifest generator.script ${generator.script} != ${expected.generatorScript}`);
  }
  if (!Array.isArray(manifest.files) || manifest.files.length === 0) {
    throw new Error("manifest files must list at least one package file");
  }
  const seen = new Set();
  for (const entry of manifest.files) {
    if (entry === null || typeof entry !== "object" || Array.isArray(entry)) {
      throw new Error("manifest file entry must be an object");
    }
    const normalized = normalizeRelativePath(entry.path);
    if (normalized !== entry.path) {
      throw new Error(`manifest path is not normalized: ${entry.path}`);
    }
    if (seen.has(normalized)) {
      throw new Error(`manifest file is listed twice: ${normalized}`);
    }
    seen.add(normalized);
    if (normalized === "release-manifest.json") {
      throw new Error("manifest must not list itself");
    }
    if (!Number.isInteger(entry.size) || entry.size < 0) {
      throw new Error(`manifest size is invalid for ${normalized}`);
    }
    if (!/^[0-9a-f]{64}$/.test(entry.sha256)) {
      throw new Error(`manifest has a malformed sha256 for ${normalized}`);
    }
    if (!packagePathInScope(normalized)) {
      throw new Error(`manifest file is outside the package install scope: ${normalized}`);
    }
  }
  for (const required of REQUIRED_PACKAGE_FILES) {
    if (!seen.has(required)) {
      throw new Error(`manifest is missing required package file: ${required}`);
    }
  }
  return manifest;
}

function sha256File(path) {
  return new Promise((resolvePromise, reject) => {
    const hash = createHash("sha256");
    const stream = createReadStream(path);
    stream.on("error", reject);
    stream.on("data", (chunk) => hash.update(chunk));
    stream.on("end", () => resolvePromise(hash.digest("hex")));
  });
}

function escapeRegExp(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

const WINDOWS_RESERVED_NAMES = new Set([
  "con", "prn", "aux", "nul",
  ...Array.from({length: 9}, (_, index) => `com${index + 1}`),
  ...Array.from({length: 9}, (_, index) => `lpt${index + 1}`),
]);
const WINDOWS_INVALID_NAME = /[<>:"|?*\u0000-\u001f]/;

function windowsNameProblem(part) {
  if (part.endsWith(".") || part.endsWith(" ")) return "ends with a dot or space";
  if (WINDOWS_INVALID_NAME.test(part)) return "contains an invalid character";
  if (WINDOWS_RESERVED_NAMES.has(part.split(".", 1)[0].toLowerCase())) return "is a reserved Windows device name";
  return null;
}

function normalizeRelativePath(path) {
  if (typeof path !== "string" || path.trim() === "") {
    throw new Error("manifest input path must be a non-empty string");
  }
  if (isAbsolute(path) || /^[A-Za-z]:/.test(path)) {
    throw new Error(`manifest input path must be relative to the package root: ${path}`);
  }
  const parts = path.split(/[\\/]+/).filter((part) => part !== "");
  const normalized = parts.join("/");
  if (normalized === "" || parts.includes(".") || parts.includes("..")) {
    throw new Error(`manifest input path escapes the package root: ${path}`);
  }
  for (const part of parts) {
    const problem = windowsNameProblem(part);
    if (problem) {
      throw new Error(`manifest path segment ${part} ${problem}: ${path}`);
    }
  }
  return normalized;
}

function normalizeVersion(version) {
  if (typeof version !== "string" || version.trim() === "") {
    throw new Error("manifest version must be a non-empty string");
  }
  const normalized = version.trim().replace(/^v/i, "");
  if (normalized === "") {
    throw new Error("manifest version must name a version after the v prefix");
  }
  return normalized;
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
