import test from "node:test";
import assert from "node:assert/strict";
import {appendFile, cp, mkdir, mkdtemp, readFile, readdir, rm} from "node:fs/promises";
import {execFile} from "node:child_process";
import {tmpdir} from "node:os";
import {join, resolve} from "node:path";
import {promisify} from "node:util";

const execFileAsync = promisify(execFile);
const repo = resolve(import.meta.dirname, "..", "..");
const web = resolve(repo, "web");

function appAsset(index) {
  const urls = [...index.matchAll(/<(?:script|link)\b[^>]*\b(?:src|href)="([^"]*\/app\.[a-f0-9]{12}\.js[^"]*)"/g)].map(match => match[1]);
  assert.equal(new Set(urls).size, 1, "preload and script must agree on the app URL");
  const url = new URL(urls[0], "http://local");
  return {file: url.pathname.slice(1), version: url.searchParams.get("v")};
}

function stylesheet(dist) {
  return dist.find(name => /^styles\.[a-f0-9]{12}\.css$/.test(name));
}

test("vendor-only change bumps the production vendor URL but not the app hash", async () => {
  // Build a throwaway source tree so mutating a vendor file cannot race other
  // tests that read the repository's web/dist.
  const workDir = await mkdtemp(join(tmpdir(), "aha2-web-vendor-"));
  try {
    await mkdir(join(workDir, "scripts"), {recursive: true});
    await cp(join(repo, "scripts", "build-web.mjs"), join(workDir, "scripts", "build-web.mjs"));
    await cp(join(repo, "assets"), join(workDir, "assets"), {recursive: true});
    await mkdir(join(workDir, "web"), {recursive: true});
    for (const name of ["src", "vendor", "package.json", "index.html"]) {
      await cp(join(web, name), join(workDir, "web", name), {recursive: true});
    }

    const build = () => execFileAsync(process.execPath, [join(workDir, "scripts", "build-web.mjs")], {
      cwd: workDir,
      env: {...process.env, NODE_PATH: join(web, "node_modules")},
      timeout: 120_000,
    });
    const dist = join(workDir, "web", "dist");

    await build();
    const first = appAsset(await readFile(join(dist, "index.html"), "utf8"));
    const firstStyles = stylesheet(await readdir(dist));

    // Change only the vendored terminal. The app bundle and stylesheet inputs
    // stay untouched, so their content hashes must stay put.
    await appendFile(join(workDir, "web", "vendor", "xterm.js"), "\n/* vendor-only regression probe */\n");
    await build();
    const index = await readFile(join(dist, "index.html"), "utf8");
    const second = appAsset(index);
    const secondStyles = stylesheet(await readdir(dist));

    assert.equal(second.file, first.file, "app bundle content did not change");
    assert.equal(secondStyles, firstStyles, "stylesheet content did not change");
    assert.notEqual(second.version, first.version, "vendor bytes must feed the build-wide ?v=");
    assert.ok(index.includes(`/app.${first.file.slice(4)}?v=${second.version}`), "index must carry the new vendor version");

    // hardware_panel copies the script query onto the vendor URLs, which is the
    // path that was stuck on the old xterm before this regression test existed.
    const panel = await readFile(join(workDir, "web", "build", "hardware_panel.js"), "utf8");
    assert.match(panel, /script\[type="module"\]\[src\*="\/app"\]/);
    assert.match(panel, /`\/vendor\/xterm\.js\$\{version\}`/);
  } finally {
    await rm(workDir, {recursive: true, force: true});
  }
});
