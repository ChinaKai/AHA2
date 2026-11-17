import test from "node:test";
import assert from "node:assert/strict";
import {mkdtemp, mkdir, readFile, writeFile, access} from "node:fs/promises";
import {tmpdir} from "node:os";
import {join} from "node:path";
import {createProfileLocks, profileDir, safeProfileID} from "./lock.mjs";

const root = () => mkdtemp(join(tmpdir(), "aha2-browser-lock-"));

test("profile ids cannot escape the host-owned root", () => {
  assert.equal(safeProfileID("browser_profile_1"), true);
  assert.equal(safeProfileID("."), false);
  assert.equal(safeProfileID(".."), false);
  assert.equal(safeProfileID("../escape"), false);
  assert.equal(safeProfileID("a/b"), false);
  assert.equal(profileDir("/srv/profiles", ".."), "");
  assert.equal(profileDir("/srv/profiles", "../escape"), "");
  assert.equal(profileDir("/srv/profiles", "browser_profile_1"), "/srv/profiles/browser_profile_1");
});

test("concurrent opens of one profile produce one lock, not a stolen lock", async () => {
  const base = await root();
  const locks = createProfileLocks(base);
  const results = await Promise.allSettled([
    locks.acquire("browser_profile_1"),
    locks.acquire("browser_profile_1"),
  ]);
  const fulfilled = results.filter(result => result.status === "fulfilled");
  const rejected = results.filter(result => result.status === "rejected");
  assert.equal(fulfilled.length, 1);
  assert.equal(rejected.length, 1);
  assert.equal(rejected[0].reason.code, "browser_profile_locked");
  const lock = fulfilled[0].value;
  assert.equal(await readFile(lock.lockPath, "utf8").then(raw => JSON.parse(raw).token), lock.token);
  await locks.release(lock);
  await assert.rejects(access(lock.lockPath));
});

test("a stale lock from a dead pid is reclaimed", async () => {
  const base = await root();
  const dir = join(base, "browser_profile_1");
  await mkdir(dir, {recursive: true});
  await writeFile(join(dir, ".aha2-host.lock"),
    JSON.stringify({pid: 2147483647, token: "stale", started_at: new Date(0).toISOString()}));
  const locks = createProfileLocks(base);
  const lock = await locks.acquire("browser_profile_1");
  assert.notEqual(lock.token, "stale");
  await locks.release(lock);
  await assert.rejects(access(lock.lockPath));
});

test("release never deletes a lock owned by a newer token", async () => {
  const base = await root();
  const locks = createProfileLocks(base);
  const lock = await locks.acquire("browser_profile_1");
  await locks.release({...lock, token: "someone-else"});
  const current = JSON.parse(await readFile(lock.lockPath, "utf8"));
  assert.equal(current.token, lock.token);
  await locks.release(lock);
  await assert.rejects(access(lock.lockPath));
});
