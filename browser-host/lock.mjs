// Per-profile directory locks for the AHA2 browser host.
//
// Two layers matter here:
//   1. acquire/release are serialized per profile inside this process;
//   2. the lock file carries a random owner token, and reclaiming a stale lock
//      goes through a separate `.reclaim` file so two host processes cannot
//      both unlink a live lock. Release only removes a file whose token still
//      matches, so a slow owner cannot delete a newer lock.

import {promises as fs} from "node:fs";
import {join, resolve, dirname} from "node:path";
import {randomBytes} from "node:crypto";

const LOCK_NAME = ".aha2-host.lock";
const INSTANCE_NAME = ".aha2-host.instance";

export function safeProfileID(value) {
  if (typeof value !== "string" || value === "." || value === "..") return false;
  return /^[A-Za-z0-9_.:-]{1,160}$/.test(value);
}

// The host, not AHA, owns the real user-data directory. AHA only sends the
// logical profile ID; this validates it and keeps the resolved directory a
// direct child of the configured root.
export function profileDir(root, profileID) {
  if (!safeProfileID(profileID)) return "";
  const base = resolve(root);
  const target = resolve(base, profileID);
  if (dirname(target) !== base) return "";
  return target;
}

function lockedError() {
  return Object.assign(new Error("profile directory is locked by another browser process"),
    {code: "browser_profile_locked"});
}

async function ownerAlive(path) {
  let raw;
  try {
    raw = await fs.readFile(path, "utf8");
  } catch (error) {
    if (error && error.code === "ENOENT") return false;
    // Unreadable for another reason: fail closed.
    return true;
  }
  let pid = 0;
  try {
    pid = Number(JSON.parse(raw).pid);
  } catch {
    // A lock file is created (wx) before its JSON is written. Treat a just
    // created, still-empty file as live so another process cannot unlink it in
    // that window; only a clearly old malformed file is reclaimable.
    try {
      const info = await fs.stat(path);
      return Date.now() - info.mtimeMs < 60_000;
    } catch {
      return true;
    }
  }
  if (!Number.isInteger(pid) || pid <= 0) return false;
  try {
    process.kill(pid, 0);
    return true;
  } catch (error) {
    // EPERM means the process exists but belongs to another account. ESRCH
    // means the recorded owner is gone.
    return Boolean(error && error.code === "EPERM");
  }
}

async function createOwned(path, token, kind) {
  const handle = await fs.open(path, "wx", 0o600);
  try {
    await handle.writeFile(JSON.stringify({
      pid: process.pid,
      token,
      kind,
      started_at: new Date().toISOString(),
    }));
  } catch (error) {
    await handle.close().catch(() => {});
    await fs.unlink(path).catch(() => {});
    throw error;
  }
  await handle.close();
}

async function releaseOwned(path, token) {
  try {
    const raw = await fs.readFile(path, "utf8");
    if (JSON.parse(raw).token === token) await fs.unlink(path);
  } catch {
    // Already released or replaced by a newer owner; never delete a lock whose
    // token does not match.
  }
}

// claimExclusive builds a lock at path. A stale file is reclaimed through
// path.reclaim, so only one process can remove it; every other process fails
// closed without ever unlinking a lock it did not observe as stale.
async function claimExclusive(path, token, kind, conflictError) {
  try {
    await createOwned(path, token, kind);
    return;
  } catch (error) {
    if (!error || error.code !== "EEXIST") throw error;
  }
  if (await ownerAlive(path)) throw conflictError();
  const reclaimPath = `${path}.reclaim`;
  const reclaimToken = randomBytes(16).toString("hex");
  try {
    await createOwned(reclaimPath, reclaimToken, "reclaim");
  } catch (error) {
    if (error && error.code === "EEXIST" && !(await ownerAlive(reclaimPath))) {
      await fs.unlink(reclaimPath).catch(() => {});
      try {
        await createOwned(reclaimPath, reclaimToken, "reclaim");
      } catch {
        throw conflictError();
      }
    } else {
      throw conflictError();
    }
  }
  try {
    // Re-check after winning the reclaim slot: a new owner may have replaced
    // the stale file while this process was waiting.
    if (await ownerAlive(path)) throw conflictError();
    await fs.unlink(path).catch(() => {});
    try {
      await createOwned(path, token, kind);
    } catch {
      throw conflictError();
    }
  } finally {
    await releaseOwned(reclaimPath, reclaimToken);
  }
}

export function createProfileLocks(root) {
  const queues = new Map();
  const serial = (key, fn) => {
    const previous = queues.get(key) || Promise.resolve();
    const result = previous.then(fn, fn);
    const tail = result.catch(() => {});
    queues.set(key, tail);
    void tail.finally(() => {
      if (queues.get(key) === tail) queues.delete(key);
    });
    return result;
  };

  const acquire = profileID => serial(profileID, async () => {
    const directory = profileDir(root, profileID);
    if (!directory) {
      throw Object.assign(new Error("invalid profile id"), {code: "browser_profile_invalid"});
    }
    await fs.mkdir(directory, {recursive: true, mode: 0o700});
    const lockPath = join(directory, LOCK_NAME);
    const token = randomBytes(16).toString("hex");
    await claimExclusive(lockPath, token, "profile", lockedError);
    return {profileID, lockPath, token};
  });

  const release = lock => {
    if (!lock) return Promise.resolve();
    return serial(lock.profileID, () => releaseOwned(lock.lockPath, lock.token));
  };

  return {acquire, release};
}

// One host per profile root. This is the startup layer that prevents two host
// processes from racing on the same user-data directories even before a
// profile lock exists.
export async function createInstanceLock(root) {
  const path = join(resolve(root), INSTANCE_NAME);
  const token = randomBytes(16).toString("hex");
  await claimExclusive(path, token, "instance", () => Object.assign(
    new Error("another AHA2 browser host is already using this profile root"),
    {code: "browser_host_already_running"}));
  return {path, token, release: () => releaseOwned(path, token)};
}
