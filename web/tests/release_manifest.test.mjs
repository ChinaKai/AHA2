import test from "node:test";
import assert from "node:assert/strict";
import {createHash} from "node:crypto";
import {mkdir, mkdtemp, readFile, writeFile} from "node:fs/promises";
import {tmpdir} from "node:os";
import {join, resolve} from "node:path";
import {spawnSync} from "node:child_process";
import {
  assertPackageManifest,
  buildManifest,
  MANIFEST_SCHEMA_VERSION,
  packagePathInScope,
  REQUIRED_PACKAGE_FILES,
} from "../../scripts/release-manifest.mjs";

const generatorScript = resolve(import.meta.dirname, "..", "..", "scripts", "release-manifest.mjs");

function sha256(data) {
  return createHash("sha256").update(data).digest("hex");
}

async function packageDir() {
  const root = await mkdtemp(join(tmpdir(), "aha2-manifest-"));
  await mkdir(join(root, "plugins", "feishu", "windows-amd64"), {recursive: true});
  await writeFile(join(root, "aha2-windows-amd64.exe"), "server-bytes");
  await writeFile(join(root, "plugins", "feishu", "windows-amd64", "plugin.json"), "{\"plugin_id\":\"feishu\"}\n");
  return root;
}

test("release manifest records digest, size, version, commit and generator", async () => {
  const root = await packageDir();
  const manifest = await buildManifest({
    root,
    version: "v1.2.3",
    commit: "abcdef0123456789abcdef0123456789abcdef01",
    builtAt: "2026-11-12T00:00:00Z",
    generator: {script: "scripts/build-windows-package.sh", command: "bash scripts/build-windows-package.sh"},
    files: ["plugins/feishu/windows-amd64/plugin.json", "aha2-windows-amd64.exe"],
  });

  assert.equal(manifest.schema_version, MANIFEST_SCHEMA_VERSION);
  assert.equal(manifest.product, "AHA2");
  assert.equal(manifest.version, "1.2.3");
  assert.equal(manifest.commit, "abcdef0123456789abcdef0123456789abcdef01");
  assert.equal(manifest.built_at, "2026-11-12T00:00:00.000Z");
  assert.deepEqual(manifest.generator, {script: "scripts/build-windows-package.sh", command: "bash scripts/build-windows-package.sh"});
  assert.deepEqual(manifest.files.map((file) => file.path), ["aha2-windows-amd64.exe", "plugins/feishu/windows-amd64/plugin.json"]);
  for (const entry of manifest.files) {
    const data = await readFile(join(root, entry.path));
    assert.equal(entry.size, data.length);
    assert.equal(entry.sha256, sha256(data));
  }
});

test("release manifest refuses missing inputs instead of writing a partial package", async () => {
  const root = await packageDir();
  await assert.rejects(
    buildManifest({
      root,
      version: "1.2.3",
      commit: "abc",
      generator: {script: "scripts/build-windows-package.sh"},
      files: ["aha2-windows-amd64.exe", "missing.exe"],
    }),
    /manifest input is missing: missing\.exe/,
  );
});

test("release manifest rejects paths outside the package root", async () => {
  const root = await packageDir();
  for (const path of ["../outside.exe", "/etc/passwd", "C:\\outside.exe", "plugins/../../outside.exe"]) {
    await assert.rejects(
      buildManifest({root, version: "1.2.3", commit: "abc", generator: {script: "s.sh"}, files: [path]}),
      /escapes the package root|must be relative/,
      `path ${path} should be rejected`,
    );
  }
});

test("release manifest rejects empty file lists, duplicate entries and missing identity", async () => {
  const root = await packageDir();
  await assert.rejects(
    buildManifest({root, version: "1.2.3", commit: "abc", generator: {script: "s.sh"}, files: []}),
    /at least one package file/,
  );
  await assert.rejects(
    buildManifest({root, version: "1.2.3", commit: "abc", generator: {script: "s.sh"}, files: ["aha2-windows-amd64.exe", "aha2-windows-amd64.exe"]}),
    /listed twice/,
  );
  await assert.rejects(
    buildManifest({root, version: "", commit: "abc", generator: {script: "s.sh"}, files: ["aha2-windows-amd64.exe"]}),
    /version/,
  );
  await assert.rejects(
    buildManifest({root, version: "v", commit: "abc", generator: {script: "s.sh"}, files: ["aha2-windows-amd64.exe"]}),
    /version/,
    "a version that is only a v prefix must be rejected, not stored as an empty string",
  );
  await assert.rejects(
    buildManifest({root, version: "1.2.3", commit: "", generator: {script: "s.sh"}, files: ["aha2-windows-amd64.exe"]}),
    /commit/,
  );
  await assert.rejects(
    buildManifest({root, version: "1.2.3", commit: "abc", generator: {}, files: ["aha2-windows-amd64.exe"]}),
    /generator\.script/,
  );
});

function validPackageManifest(overrides = {}) {
  return {
    schema_version: MANIFEST_SCHEMA_VERSION,
    product: "AHA2",
    version: "1.2.3",
    commit: "abcdef0123456789abcdef0123456789abcdef01",
    built_at: "2026-11-12T00:00:00.000Z",
    generator: {
      script: "scripts/build-windows-package.sh",
      command: "bash scripts/build-windows-package.sh --version 1.2.3",
      web_version: "v1.2.3.20261112.abcdef012345",
      go_version: "go1.27.4",
      node_version: "v24.21.0",
    },
    files: REQUIRED_PACKAGE_FILES.map((path) => ({path, size: 1, sha256: "a".repeat(64)})),
    ...overrides,
  };
}

test("package manifest gate accepts the exact installer install scope", () => {
  const manifest = validPackageManifest();
  assert.equal(assertPackageManifest(manifest, {
    version: "v1.2.3",
    commit: "abcdef0123456789abcdef0123456789abcdef01",
    webVersion: "v1.2.3.20261112.abcdef012345",
    generatorScript: "scripts/build-windows-package.sh",
  }), manifest);
  for (const path of REQUIRED_PACKAGE_FILES) {
    assert.ok(packagePathInScope(path), `${path} must be in the installer install scope`);
  }
  for (const path of ["secret.txt", "plugins/other/x.exe", "browser-host", "node-modules/x", "/absolute.exe", "C:/x.exe"]) {
    assert.ok(!packagePathInScope(path), `${path} must be outside the installer install scope`);
  }
});

test("package manifest gate rejects identity mismatches", () => {
  assert.throws(() => assertPackageManifest(validPackageManifest(), {version: "1.2.4"}), /manifest version/);
  assert.throws(() => assertPackageManifest(validPackageManifest(), {commit: "0000000000000000000000000000000000000000"}), /manifest commit/);
  assert.throws(() => assertPackageManifest(validPackageManifest(), {webVersion: "v1.2.3.20261113.abcdef012345"}), /generator\.web_version/);
  assert.throws(() => assertPackageManifest(validPackageManifest(), {generatorScript: "local_build.sh"}), /generator\.script/);
  assert.ok(assertPackageManifest(validPackageManifest({version: "v1.2.3"}), {version: "1.2.3"}),
    "the manifest version and expected.version are both normalized before comparison");
});

test("package manifest gate rejects unsafe, duplicate and out-of-scope entries", () => {
  assert.throws(() => assertPackageManifest(validPackageManifest({files: []})), /at least one package file/);
  assert.throws(
    () => assertPackageManifest(validPackageManifest({files: [
      {path: "browser-host//host.mjs", size: 1, sha256: "a".repeat(64)},
    ]})),
    /not normalized/,
  );
  assert.throws(
    () => assertPackageManifest(validPackageManifest({files: [
      {path: "aha2-windows-amd64.exe", size: 1, sha256: "a".repeat(64)},
      {path: "aha2-windows-amd64.exe", size: 2, sha256: "b".repeat(64)},
    ]})),
    /listed twice/,
  );
  assert.throws(
    () => assertPackageManifest(validPackageManifest({files: [
      ...validPackageManifest().files,
      {path: "operator-deploy.ps1", size: 1, sha256: "a".repeat(64)},
    ]})),
    /outside the package install scope/,
  );
  assert.throws(
    () => assertPackageManifest(validPackageManifest({files: [
      ...validPackageManifest().files,
      {path: "release-manifest.json", size: 1, sha256: "a".repeat(64)},
    ]})),
    /must not list itself/,
  );
  assert.throws(
    () => assertPackageManifest(validPackageManifest({files: validPackageManifest().files.slice(1)})),
    /missing required package file/,
  );
  assert.throws(
    () => assertPackageManifest(validPackageManifest({files: [
      ...validPackageManifest().files,
      {path: "node/extra.exe", size: 1, sha256: "xyz"},
    ]})),
    /malformed sha256/,
  );
});

test("release manifest CLI writes the manifest and fails without required arguments", async () => {
  const root = await packageDir();
  const output = join(root, "release-manifest.json");
  const ok = spawnSync(process.execPath, [
    generatorScript,
    "--root", root,
    "--version", "0.5.23",
    "--commit", "0123456789abcdef0123456789abcdef01234567",
    "--built-at", "2026-11-12T01:02:03Z",
    "--output", output,
    "--generator-script", "scripts/build-windows-package.sh",
    "--generator-command", "bash scripts/build-windows-package.sh --version 0.5.23",
    "--web-version", "v0.5.23.20261112.0123456789ab",
    "--go-version", "go1.27.4",
    "--node-version", "v24.21.0",
    "aha2-windows-amd64.exe",
    "plugins/feishu/windows-amd64/plugin.json",
  ], {encoding: "utf8"});
  assert.equal(ok.status, 0, ok.stderr);
  const manifest = JSON.parse(await readFile(output, "utf8"));
  assert.equal(manifest.version, "0.5.23");
  assert.equal(manifest.generator.web_version, "v0.5.23.20261112.0123456789ab");
  assert.equal(manifest.files.length, 2);
  assert.ok(!manifest.files.some((file) => file.path === "release-manifest.json"));
  for (const file of manifest.files) {
    assert.match(file.sha256, /^[0-9a-f]{64}$/, `CLI wrote a malformed sha256 for ${file.path}`);
    assert.ok(file.size > 0, `CLI wrote an empty size for ${file.path}`);
  }

  const bad = spawnSync(process.execPath, [
    generatorScript, "--root", root, "--version", "0.5.23", "--output", output,
    "--generator-script", "scripts/build-windows-package.sh", "aha2-windows-amd64.exe",
  ], {encoding: "utf8"});
  assert.notEqual(bad.status, 0);
  assert.match(bad.stderr, /commit/);
});
