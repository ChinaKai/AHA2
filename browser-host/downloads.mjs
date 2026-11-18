// Per-session download capture for the AHA2 browser host.
//
// Downloads are written under `<profile-root>/.downloads/<profile>/<session>/`
// using an opaque id as the file name. The suggested file name is metadata
// only and never becomes a path. AHA addresses downloads by that id; a host
// path is never returned.
//
// Limits are enforced while the stream is consumed, not after a full
// saveAs(): a download larger than the per-file or cumulative budget is
// canceled and its Playwright artifact is deleted. The in-flight count is
// reserved synchronously so parallel download events cannot exceed the
// declared file count. Entries expire on a timer, and clear()/close() cancel
// in-flight downloads and delete every artifact.

import {randomBytes} from "node:crypto";
import {promises as fs} from "node:fs";
import {join} from "node:path";

export const MAX_DOWNLOAD_BYTES = 10 * 1024 * 1024;
export const MAX_DOWNLOAD_FILES = 20;
export const MAX_DOWNLOAD_TOTAL_BYTES = 50 * 1024 * 1024;
export const DOWNLOAD_TTL_MS = 30 * 60 * 1000;

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
  const maxBytes = Number.isInteger(options.maxBytes) && options.maxBytes > 0
    ? options.maxBytes : MAX_DOWNLOAD_BYTES;
  const maxFiles = Number.isInteger(options.maxFiles) && options.maxFiles > 0
    ? options.maxFiles : MAX_DOWNLOAD_FILES;
  const maxTotalBytes = Number.isInteger(options.maxTotalBytes) && options.maxTotalBytes > 0
    ? options.maxTotalBytes : MAX_DOWNLOAD_TOTAL_BYTES;
  const ttlMs = Number.isInteger(options.ttlMs) && options.ttlMs > 0
    ? options.ttlMs : DOWNLOAD_TTL_MS;
  const rawDir = typeof options.rawDir === "string" && options.rawDir ? options.rawDir : "";
  const rawPollMs = Number.isInteger(options.rawPollMs) && options.rawPollMs > 0
    ? options.rawPollMs : 100;
  const directory = join(root, ".downloads", profileID, sessionID);
  const entries = new Map();
  const inFlight = new Set();
  let totalBytes = 0;
  let pendingBytes = 0;
  let rejected = 0;
  let failed = 0;
  let rawMaxBytes = 0;
  let rawTotalBytes = 0;
  let rawFiles = 0;
  let rawCancels = 0;
  let ready = false;
  let closed = false;
  // Every clear() advances the generation. A capture that started in an older
  // generation must never publish into the reset budget or release its bytes
  // from the new generation's pending total.
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
    entries.delete(entry.id);
    if (entry.state === "ready" && entry.size > 0) {
      totalBytes = Math.max(0, totalBytes - entry.size);
    }
    await fs.rm(entry.path, {force: true}).catch(() => {});
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

  const prune = async () => {
    const cutoff = Date.now() - ttlMs;
    for (const entry of [...entries.values()]) {
      if (entry.createdAt >= cutoff) continue;
      if (entry.state === "saving" && entry.download) {
        entry.state = "expired";
        await discard(entry.download);
      }
      await removeEntry(entry);
    }
  };

  // Synchronous slot reservation: several download events can arrive in the
  // same tick, so the size check and map insertion must not be separated by an
  // await. Older ready entries are evicted; a full set of in-flight entries
  // rejects the newcomer instead of allowing an unbounded 21st file.
  const reserve = (download, fence) => {
    const evicted = [];
    while (entries.size >= maxFiles) {
      const oldest = [...entries.values()]
        .filter(entry => entry.state !== "saving")
        .sort((left, right) => left.createdAt - right.createdAt)[0];
      if (!oldest) {
        rejected += 1;
        return null;
      }
      entries.delete(oldest.id);
      if (oldest.state === "ready" && oldest.size > 0) {
        totalBytes = Math.max(0, totalBytes - oldest.size);
      }
      evicted.push(oldest);
    }
    const id = `download-${randomBytes(12).toString("hex")}`;
    const entry = {
      id,
      name: safeDownloadName(typeof download.suggestedFilename === "function" ? download.suggestedFilename() : ""),
      size: 0,
      createdAt: Date.now(),
      path: join(directory, id),
      // Downloads belong to the lease fence that was installed when the page
      // activity happened. A later holder cannot list or fetch them.
      fence: String(fence || ""),
      state: "saving",
      download,
    };
    entries.set(id, entry);
    return {entry, evicted};
  };

  // createReadStream() only resolves after Chromium finished writing its raw
  // artifact, so a stream-only limit is too late: a 30 MiB response would
  // already sit on disk. Poll the controlled downloadsPath while a download is
  // active and cancel every in-flight transfer as soon as one raw artifact (or
  // the raw total) crosses the declared budget.
  const checkRaw = async () => {
    if (!rawDir) return;
    let names = [];
    try {
      names = await fs.readdir(rawDir);
    } catch {
      return;
    }
    let maximum = 0;
    let total = 0;
    let files = 0;
    for (const name of names) {
      const stat = await fs.stat(join(rawDir, name)).catch(() => null);
      if (!stat || !stat.isFile()) continue;
      files += 1;
      total += stat.size;
      if (stat.size > maximum) maximum = stat.size;
    }
    rawFiles = files;
    rawTotalBytes = total;
    if (maximum > rawMaxBytes) rawMaxBytes = maximum;
    if (inFlight.size === 0) return;
    if (maximum > maxBytes || total > maxTotalBytes) {
      rawCancels += 1;
      for (const download of [...inFlight]) {
        try {
          await download.cancel?.();
        } catch {
          // A finished download cannot be canceled; its capture decides.
        }
      }
    }
  };

  const rawTimer = rawDir
    ? setInterval(() => { void checkRaw().catch(() => {}); }, rawPollMs)
    : null;
  rawTimer?.unref?.();

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
    const reservation = reserve(download, fence);
    if (!reservation) {
      await discard(download);
      throw downloadFailure("too many pending downloads", "browser_download_too_many");
    }
    const {entry, evicted} = reservation;
    for (const old of evicted) {
      await fs.rm(old.path, {force: true}).catch(() => {});
    }
    if (captureGeneration !== generation) {
      entries.delete(entry.id);
      await fs.rm(entry.path, {force: true}).catch(() => {});
      await discard(download);
      throw downloadFailure("download store was cleared", "browser_download_canceled");
    }
    inFlight.add(download);
    let stream = null;
    let written = 0;
    let charged = 0;
    try {
      await ensure();
      stream = await download.createReadStream();
      if (!stream) throw downloadFailure("download has no readable stream");
      const handle = await fs.open(entry.path, "w", 0o600);
      try {
        for await (const chunk of stream) {
          if (captureGeneration !== generation) {
            throw downloadFailure("download store was cleared", "browser_download_canceled");
          }
          const buffer = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk);
          written += buffer.length;
          if (written > maxBytes) {
            throw downloadFailure("download exceeds the size limit", "browser_download_too_large");
          }
          if (totalBytes + pendingBytes + buffer.length > maxTotalBytes) {
            throw downloadFailure("download budget is exhausted", "browser_download_too_large");
          }
          pendingBytes += buffer.length;
          charged += buffer.length;
          await handle.write(buffer);
        }
      } finally {
        await handle.close();
      }
      if (captureGeneration !== generation) {
        throw downloadFailure("download store was cleared", "browser_download_canceled");
      }
      entry.size = written;
      entry.state = "ready";
      totalBytes += written;
      return entry;
    } catch (error) {
      failed += 1;
      entry.state = "failed";
      entries.delete(entry.id);
      await fs.rm(entry.path, {force: true}).catch(() => {});
      await discard(download);
      throw error;
    } finally {
      // Release only the bytes this download actually reserved. A chunk that
      // failed the per-file or cumulative check never entered the budget and
      // must not be subtracted (that would over-release budget for peers).
      // A capture from a cleared generation must not touch the new budget at
      // all: clear() already reset pendingBytes after waiting for it (or timed
      // out), and subtracting here would steal a new download's reservation.
      if (captureGeneration === generation) {
        pendingBytes = Math.max(0, pendingBytes - charged);
      }
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

  const maintenance = setInterval(() => {
    void prune().catch(() => {});
  }, Math.max(50, Math.min(60_000, ttlMs)));
  maintenance.unref?.();

  const clearAll = async (keepRawDirectory = true) => {
    if (clearing) return clearing;
    generation += 1;
    const capturesAtClear = [...activeCaptures];
    const work = (async () => {
      // Cancel first so streams end; then wait for the old captures to finish
      // their per-download cleanup. The generation check above/below is the
      // backstop if a filesystem write cannot be interrupted within the grace
      // period: a stale capture can no longer publish or release budget.
      for (const download of [...inFlight]) await discard(download);
      await Promise.race([
        Promise.allSettled(capturesAtClear),
        sleep(3000),
      ]);
      const pending = [...entries.values()];
      entries.clear();
      totalBytes = 0;
      pendingBytes = 0;
      for (const entry of pending) {
        await fs.rm(entry.path, {force: true}).catch(() => {});
      }
      inFlight.clear();
      ready = false;
      rawFiles = 0;
      rawTotalBytes = 0;
      await fs.rm(directory, {recursive: true, force: true}).catch(() => {});
      // Chromium keeps writing future downloads to the same downloadsPath;
      // keep the controlled directory present after a release-time clear.
      if (rawDir && keepRawDirectory) {
        await fs.mkdir(rawDir, {recursive: true, mode: 0o700}).catch(() => {});
      }
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
      return {
        entries: entries.size, inFlight: inFlight.size, totalBytes, pendingBytes,
        rejected, failed, rawMaxBytes, rawTotalBytes, rawFiles, rawCancels,
      };
    },

    async list(fence = "") {
      await prune();
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
      await prune();
      if (!/^download-[a-f0-9]{24}$/.test(String(id || ""))) return null;
      const entry = entries.get(id);
      if (!entry || entry.state !== "ready" || entry.fence !== String(fence || "")) return null;
      const stat = await fs.stat(entry.path).catch(() => null);
      if (!stat || !stat.isFile() || stat.size > maxBytes) {
        if (stat) await removeEntry(entry);
        return null;
      }
      return {entry, data: await fs.readFile(entry.path)};
    },

    // Release path: drop this holder's artifacts but keep the store usable for
    // a later lease on the same browser session.
    async clear() {
      await clearAll(true);
    },

    // Session close: cancel timers and pending downloads permanently.
    async close() {
      closed = true;
      clearInterval(maintenance);
      if (rawTimer) clearInterval(rawTimer);
      await clearAll(false);
    },
  };
}
