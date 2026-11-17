// Per-profile directory locks for the AHA2 browser host.
//
// Two layers matter here:
//   1. acquire/release are serialized per profile inside this process, so two
//      concurrent open requests cannot delete each other's freshly created lock;
//   2. the lock file carries a random owner token, and release only removes the
//      file when the token still matches. A crashed process leaves a stale lock
//      that is reclaimed only when the recorded pid is gone.

import {promises as fs} from "node:fs";
import {join, resolve, dirname} from "node:path";
import {randomBytes} from "node:crypto";

const LOCK_NAME = ".aha2-host.lock";

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

async function lockOwnerAlive(lockPath) {
  try {
    const raw = await fs.readFile(lockPath, "utf8");
    const pid = Number(JSON.parse(raw).pid);
    if (!Number.isInteger(pid) || pid <= 0) return false;
    process.kill(pid, 0);
    return true;
  } catch (error) {
    // EPERM means the process exists but belongs to another account. Any parse
    // or ESRCH failure means the recorded owner is gone.
    return Boolean(error && error.code === "EPERM");
  }
}

function lockedError() {
  return Object.assign(new Error("profile directory is locked by another browser process"),
    {code: "browser_profile_locked"});
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

  const create = async (lockPath, token) => {
    const handle = await fs.open(lockPath, "wx", 0o600);
    try {
      await handle.writeFile(JSON.stringify({
        pid: process.pid,
        token,
        started_at: new Date().toISOString(),
      }));
    } catch (error) {
      await handle.close().catch(() => {});
      await fs.unlink(lockPath).catch(() => {});
      throw error;
    }
    await handle.close();
  };

  const acquire = profileID => serial(profileID, async () => {
    const directory = profileDir(root, profileID);
    if (!directory) {
      throw Object.assign(new Error("invalid profile id"), {code: "browser_profile_invalid"});
    }
    await fs.mkdir(directory, {recursive: true, mode: 0o700});
    const lockPath = join(directory, LOCK_NAME);
    const token = randomBytes(16).toString("hex");
    try {
      await create(lockPath, token);
    } catch (error) {
      if (!error || error.code !== "EEXIST") throw error;
      // Serialized in-process, so the only remaining live owner is a different
      // process or a stale file from a crash.
      if (await lockOwnerAlive(lockPath)) throw lockedError();
      await fs.unlink(lockPath).catch(() => {});
      try {
        await create(lockPath, token);
      } catch {
        throw lockedError();
      }
    }
    return {profileID, lockPath, token};
  });

  const release = lock => {
    if (!lock) return Promise.resolve();
    return serial(lock.profileID, async () => {
      try {
        const raw = await fs.readFile(lock.lockPath, "utf8");
        if (JSON.parse(raw).token === lock.token) {
          await fs.unlink(lock.lockPath);
        }
      } catch {
        // Already released or replaced by a newer owner; never delete a lock
        // whose token does not match.
      }
    });
  };

  return {acquire, release};
}
