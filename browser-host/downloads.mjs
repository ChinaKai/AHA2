// Per-session download capture for the AHA2 browser host.
//
// Downloads are written under `<profile-root>/.downloads/<profile>/<session>/`
// using an opaque id as the file name. The suggested file name is metadata
// only and never becomes a path. AHA addresses downloads by that id; a host
// path is never returned.
//
// There is no business quota on downloads: a file of any size and any number
// of files are accepted while the session lives. Real resource failures (for
// example a full disk) still surface as capture errors and leave no artifact.
// Captures are never truncated or reported as successful when the write
// failed. Artifacts are removed only by an explicit release clear() or by
// session close(); there is no age-based expiry.

import {randomBytes} from "node:crypto";
import {promises as fs} from "node:fs";
import {join} from "node:path";

export function safeDownloadName(raw) {
  const cleaned = String(raw || "")
    .replace(/[\\/]/g, "_")
    .replace(/[\u0000-\u001f\u007f]/g, "")
    .replace(/^\.+/, "")
    .trim();
  const name = Array.from(cleaned).slice(0, 200).join("");
  return name || "download.bin";
}

function downloadFailure(message, code = "browser_download_failed") {
  return Object.assign(new Error(message), {code});
}

export function createDownloadStore(root, profileID, sessionID, options = {}) {
  const rawDir = typeof options.rawDir === "string" && options.rawDir ? options.rawDir : "";
  const clearGraceMs = Number.isInteger(options.clearGraceMs) && options.clearGraceMs > 0
    ? options.clearGraceMs : 3000;
  const directory = join(root, ".downloads", profileID, sessionID);
  const entries = new Map();
  const inFlight = new Set();
  let totalBytes = 0;
  let failed = 0;
  let ready = false;
  let closed = false;
  // Every clear() advances the generation. A capture that started in an older
  // generation must never publish into the reset store or leave an artifact
  // that a later holder could address.
  let generation = 0;
  let clearing = null;
  const activeCaptures = new Set();

  const ensure = async () => {
    if (ready) return;
    await fs.mkdir(directory, {recursive: true, mode: 0o700});
    if (rawDir) await fs.mkdir(rawDir, {recursive: true, mode: 0o700});
    ready = true;
  };

  const removeEntry = async entry => {
    // Identity check: a stale snapshot entry is not the row currently in the
    // map (clear() already removed it), so it must not delete or account
    // anything.
    if (entries.get(entry.id) !== entry) return;
    entries.delete(entry.id);
    if (entry.accounted) {
      entry.accounted = false;
      totalBytes = Math.max(0, totalBytes - entry.size);
    }
    await fs.rm(entry.path, {force: true}).catch(() => {});
  };

  // A capture may finish fs.open() after clear()/close() already dropped its
  // row; removeEntry() then no-ops on the identity guard and the empty
  // artifact would survive. Delete the artifact this capture created unless a
  // live entry owns the same path (paths are per-entry random ids, so this is
  // only a safety net against stealing a newer generation's file).
  const removeOrphanArtifact = async path => {
    for (const current of entries.values()) {
      if (current.path === path) return;
    }
    await fs.rm(path, {force: true}).catch(() => {});
  };

  const discard = async download => {
    try {
      await download.cancel?.();
    } catch {
      // A finished or already-canceled download is not an error.
    }
    try {
      await download.delete?.();
    } catch {
      // The context close path also removes artifacts.
    }
  };

  const reserve = (download, fence) => {
    const id = `download-${randomBytes(12).toString("hex")}`;
    const entry = {
      id,
      name: safeDownloadName(typeof download.suggestedFilename === "function" ? download.suggestedFilename() : ""),
      size: 0,
      createdAt: Date.now(),
      path: join(directory, id),
      // Downloads belong to the fence that was installed when the page
      // activity happened. A later holder cannot list or fetch them.
      fence: String(fence || ""),
      generation: 0,
      accounted: false,
      state: "saving",
      download,
    };
    entries.set(id, entry);
    return entry;
  };

  const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));

  const publish = async (download, fence, captureGeneration) => {
    if (closed) {
      await discard(download);
      throw downloadFailure("download store is closed");
    }
    if (captureGeneration !== generation) {
      await discard(download);
      throw downloadFailure("download store was cleared", "browser_download_canceled");
    }
    const entry = reserve(download, fence);
    entry.generation = captureGeneration;
    if (captureGeneration !== generation) {
      entries.delete(entry.id);
      await fs.rm(entry.path, {force: true}).catch(() => {});
      await discard(download);
      throw downloadFailure("download store was cleared", "browser_download_canceled");
    }
    inFlight.add(download);
    let stream = null;
    let written = 0;
    let opened = false;
    try {
      await ensure();
      stream = await download.createReadStream();
      if (!stream) throw downloadFailure("download has no readable stream");
      const handle = await fs.open(entry.path, "w", 0o600);
      // fs.open() creates the file, so from here on the artifact exists even if
      // this capture is already stale.
      opened = true;
      try {
        if (captureGeneration !== generation || entries.get(entry.id) !== entry ||
            entry.state !== "saving") {
          // The row was cleared while open() was pending. Do not consume or
          // write into a generation that no longer exists; detach from the
          // browser stream so a later error has no owner.
          stream.destroy?.();
          throw downloadFailure("download entry was invalidated", "browser_download_canceled");
        }
        for await (const chunk of stream) {
          if (captureGeneration !== generation) {
            throw downloadFailure("download store was cleared", "browser_download_canceled");
          }
          const buffer = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk);
          written += buffer.length;
          await handle.write(buffer);
        }
      } finally {
        await handle.close();
      }
      if (captureGeneration !== generation || entries.get(entry.id) !== entry ||
          entry.state !== "saving") {
        // A newer clear invalidated this row while the stream was still
        // running. It must neither reappear in the list nor keep its bytes.
        throw downloadFailure("download entry was invalidated", "browser_download_canceled");
      }
      entry.size = written;
      entry.state = "ready";
      entry.accounted = true;
      totalBytes += written;
      return entry;
    } catch (error) {
      if (captureGeneration === generation) failed += 1;
      if (entry.state === "saving") entry.state = "failed";
      await removeEntry(entry);
      // removeEntry() is a no-op once the row is gone (clear/close may have run
      // while open() was pending), so clean the artifact this capture created
      // explicitly. New-generation files are protected by the path check.
      if (opened) await removeOrphanArtifact(entry.path);
      await discard(download);
      throw error;
    } finally {
      inFlight.delete(download);
      delete entry.download;
      // Playwright keeps the raw browser artifact until it is deleted. Always
      // remove it, including on failure/cancel, so a rejected download does
      // not linger outside the controlled store.
      try {
        await download.delete();
      } catch {
        // Context close is the final fallback.
      }
      stream?.destroy?.();
    }
  };

  const clearAll = async (keepRawDirectory = true) => {
    if (clearing) return clearing;
    generation += 1;
    const capturesAtClear = [...activeCaptures];
    const work = (async () => {
      // Cancel and wait under one overall deadline. A hanging cancel/delete
      // must not block clear/close or later captures forever; the generation
      // guard makes every late completion harmless.
      const settle = (async () => {
        await Promise.allSettled([...inFlight].map(download => discard(download)));
        await Promise.allSettled(capturesAtClear);
      })();
      await Promise.race([settle, sleep(clearGraceMs)]);
      const pending = [...entries.values()];
      entries.clear();
      totalBytes = 0;
      for (const entry of pending) {
        await fs.rm(entry.path, {force: true}).catch(() => {});
      }
      inFlight.clear();
      ready = false;
      await fs.rm(directory, {recursive: true, force: true}).catch(() => {});
      // The raw downloadsPath may be configured outside the session directory
      // in tests; remove it explicitly so no old artifact survives a clear.
      if (rawDir) {
        await fs.rm(rawDir, {recursive: true, force: true}).catch(() => {});
        // Chromium keeps writing future downloads to the same downloadsPath;
        // keep the controlled directory present after a release-time clear.
        if (keepRawDirectory) {
          await fs.mkdir(rawDir, {recursive: true, mode: 0o700}).catch(() => {});
        }
      }
      // Captures that outlived the bounded grace stay isolated by generation;
      // do not keep waiting on them in every later clear.
      for (const promise of capturesAtClear) activeCaptures.delete(promise);
    })();
    clearing = work.finally(() => {
      if (clearing === wrapped) clearing = null;
    });
    const wrapped = clearing;
    return clearing;
  };

  return {
    async capture(download, fence = "") {
      // A clear() in progress blocks new captures so its reset cannot race a
      // fresh download. Once admitted, the capture owns the current generation
      // until it settles.
      if (clearing) await clearing;
      if (closed) {
        await discard(download);
        throw downloadFailure("download store is closed");
      }
      const promise = publish(download, String(fence || ""), generation);
      activeCaptures.add(promise);
      const drop = () => activeCaptures.delete(promise);
      promise.then(drop, drop);
      return promise;
    },

    // Diagnostics for tests and the opt-in real-browser regression harness.
    // Contains counts only; no file name, fence or path.
    stats() {
      return {entries: entries.size, inFlight: inFlight.size, totalBytes, failed};
    },

    async list(fence = "") {
      return [...entries.values()]
        .filter(entry => entry.state === "ready" && entry.fence === String(fence || ""))
        .sort((left, right) => right.createdAt - left.createdAt)
        .map(entry => ({
          id: entry.id,
          filename: entry.name,
          size: entry.size,
          created_at: new Date(entry.createdAt).toISOString(),
        }));
    },

    async read(id, fence = "") {
      if (!/^download-[a-f0-9]{24}$/.test(String(id || ""))) return null;
      const entry = entries.get(id);
      if (!entry || entry.state !== "ready" || entry.fence !== String(fence || "")) return null;
      const stat = await fs.stat(entry.path).catch(() => null);
      if (!stat || !stat.isFile()) {
        if (stat) await removeEntry(entry);
        return null;
      }
      return {entry, data: await fs.readFile(entry.path)};
    },

    // Release path: drop this holder's artifacts but keep the store usable for
    // a later owner switch on the same browser session.
    async clear() {
      await clearAll(true);
    },

    // Session close: cancel pending downloads permanently.
    async close() {
      closed = true;
      await clearAll(false);
      // If a release-time clear was already in progress, it recreated the raw
      // directory for future downloads. Session close must remove it again.
      await fs.rm(directory, {recursive: true, force: true}).catch(() => {});
      if (rawDir) await fs.rm(rawDir, {recursive: true, force: true}).catch(() => {});
    },
  };
}
