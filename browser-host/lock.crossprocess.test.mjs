import test from "node:test";
import assert from "node:assert/strict";
import {mkdtemp, mkdir, writeFile} from "node:fs/promises";
import {tmpdir} from "node:os";
import {join} from "node:path";
import {spawn} from "node:child_process";

const worker = new URL("./lock-worker.mjs", import.meta.url);

function runWorker(root) {
  return new Promise((resolve, reject) => {
    const child = spawn(process.execPath, [worker.pathname, root, "browser_profile_1"], {
      stdio: ["pipe", "pipe", "pipe"],
    });
    let stdout = "";
    let stderr = "";
    child.stdout.on("data", chunk => { stdout += chunk; });
    child.stderr.on("data", chunk => { stderr += chunk; });
    child.on("error", reject);
    child.on("exit", code => {
      if (code !== 0) reject(new Error(`worker exited ${code}: ${stderr}`));
      else resolve(stdout.trim());
    });
    child.stdin.write("go\n");
  });
}

test("two real node processes reclaiming the same stale lock never both acquire", async () => {
  for (let round = 0; round < 12; round++) {
    const root = await mkdtemp(join(tmpdir(), "aha2-browser-xproc-"));
    const dir = join(root, "browser_profile_1");
    await mkdir(dir, {recursive: true});
    await writeFile(join(dir, ".aha2-host.lock"),
      JSON.stringify({pid: 2147483647, token: "stale", started_at: new Date(0).toISOString()}));
    const [first, second] = await Promise.all([runWorker(root), runWorker(root)]);
    const results = [first, second];
    const acquired = results.filter(value => value.startsWith("ACQUIRED"));
    const locked = results.filter(value => value.startsWith("LOCKED"));
    assert.equal(acquired.length, 1, `round ${round}: ${results.join(" | ")}`);
    assert.equal(locked.length, 1, `round ${round}: ${results.join(" | ")}`);
  }
});
