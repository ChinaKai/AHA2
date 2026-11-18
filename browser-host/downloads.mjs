// Per-session download capture for the AHA2 browser host.
//
// Downloads are written under `<profile-root>/.downloads/<profile>/<session>/`
// using an opaque id as the file name. The suggested file name is metadata
// only and never becomes a path. AHA addresses downloads by that id; a host
// path is never returned. Entries expire, are capped, and are removed when the
// session closes or the lease is released.

import {randomBytes} from "node:crypto";
import {promises as fs} from "node:fs";
import {join} from "node:path";

export const MAX_DOWNLOAD_BYTES = 10 * 1024 * 1024;
export const MAX_DOWNLOAD_FILES = 20;
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

export function createDownloadStore(root, profileID, sessionID) {
  const directory = join(root, ".downloads", profileID, sessionID);
  const entries = new Map();
  let ready = false;

  const ensure = async () => {
    if (ready) return;
    await fs.mkdir(directory, {recursive: true, mode: 0o700});
    ready = true;
  };

  const removeEntry = async entry => {
    entries.delete(entry.id);
    await fs.rm(entry.path, {force: true}).catch(() => {});
  };

  const prune = async () => {
    const cutoff = Date.now() - DOWNLOAD_TTL_MS;
    for (const entry of [...entries.values()]) {
      if (entry.createdAt < cutoff) await removeEntry(entry);
    }
  };

  const enforceCount = async () => {
    while (entries.size > MAX_DOWNLOAD_FILES) {
      const oldest = [...entries.values()]
        .filter(entry => entry.state !== "saving")
        .sort((left, right) => left.createdAt - right.createdAt)[0];
      if (!oldest) return;
      await removeEntry(oldest);
    }
  };

  return {
    async capture(download, fence = "") {
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
      };
      entries.set(id, entry);
      try {
        await ensure();
        await download.saveAs(entry.path);
        const stat = await fs.stat(entry.path);
        if (!stat.isFile() || stat.size > MAX_DOWNLOAD_BYTES) {
          throw new Error("download is too large");
        }
        entry.size = stat.size;
        entry.state = "ready";
        await enforceCount();
      } catch {
        entry.state = "failed";
        await fs.rm(entry.path, {force: true}).catch(() => {});
        entries.delete(id);
      }
      return entry;
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
      const entry = entries.get(String(id || ""));
      if (!entry || entry.state !== "ready" || entry.fence !== String(fence || "")) return null;
      const stat = await fs.stat(entry.path).catch(() => null);
      if (!stat || !stat.isFile() || stat.size > MAX_DOWNLOAD_BYTES) {
        if (stat) await removeEntry(entry);
        return null;
      }
      return {entry, data: await fs.readFile(entry.path)};
    },

    async clear() {
      entries.clear();
      ready = false;
      await fs.rm(directory, {recursive: true, force: true}).catch(() => {});
    },
  };
}
