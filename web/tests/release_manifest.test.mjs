import test from "node:test";
import assert from "node:assert/strict";
import {createHash} from "node:crypto";
import {mkdir, mkdtemp, readFile, writeFile} from "node:fs/promises";
import {tmpdir} from "node:os";
import {join, resolve} from "node:path";
import {spawnSync} from "node:child_process";
import {buildManifest, MANIFEST_SCHEMA_VERSION} from "../../scripts/release-manifest.mjs";

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
    buildManifest({root, version: "1.2.3", commit: "", generator: {script: "s.sh"}, files: ["aha2-windows-amd64.exe"]}),
    /commit/,
  );
  await assert.rejects(
    buildManifest({root, version: "1.2.3", commit: "abc", generator: {}, files: ["aha2-windows-amd64.exe"]}),
    /generator\.script/,
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
