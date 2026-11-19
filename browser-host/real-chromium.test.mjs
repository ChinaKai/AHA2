// Opt-in real-browser regressions for the P1 review findings (task-595).
//
// The default `npm test` run skips these because a Chromium binary and the
// Playwright package are not repository dependencies. Set:
//   AHA2_PLAYWRIGHT_MODULE=/abs/path/to/playwright/index.mjs
//   AHA2_CHROMIUM_EXECUTABLE=/abs/path/to/chrome-headless-shell
// to execute them. They use a local test server only; no account or real
// profile is touched.

import test from "node:test";
import assert from "node:assert/strict";
import {createServer} from "node:http";
import {access, mkdtemp, readdir} from "node:fs/promises";
import {tmpdir} from "node:os";
import {join} from "node:path";
import {createHostServer} from "./server.mjs";

const playwrightModule = process.env.AHA2_PLAYWRIGHT_MODULE || "";
const chromiumExecutable = process.env.AHA2_CHROMIUM_EXECUTABLE || "";
const enabled = Boolean(playwrightModule && chromiumExecutable);
const chromium = enabled ? (await import(playwrightModule)).chromium : null;
const PROFILE = "browser_profile_real";
const TOKEN = "real-chromium-token";

const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));

function localServer() {
  return createServer((request, response) => {
    const path = request.url || "/";
    if (path.startsWith("/f/")) {
      response.writeHead(200, {
        "Content-Type": "application/octet-stream",
        "Content-Disposition": `attachment; filename="file-${path.slice(3)}.bin"`,
      });
      response.end(Buffer.alloc(1024, 0x61));
      return;
    }
    if (path === "/slow") {
      response.writeHead(200, {
        "Content-Type": "application/octet-stream",
        "Content-Disposition": 'attachment; filename="slow.bin"',
      });
      const timer = setInterval(() => {
        if (response.destroyed) {
          clearInterval(timer);
          return;
        }
        response.write(Buffer.alloc(64 * 1024, 0x62));
      }, 50);
      request.on("close", () => clearInterval(timer));
      return;
    }
    if (path === "/blank") {
      response.writeHead(200, {"Content-Type": "text/html; charset=utf-8"});
      response.end("<!doctype html><html><body></body></html>");
      return;
    }
    if (path === "/huge") {
      response.writeHead(200, {
        "Content-Type": "application/octet-stream",
        "Content-Disposition": 'attachment; filename="huge.bin"',
      });
      let sent = 0;
      const timer = setInterval(() => {
        if (response.destroyed || sent >= 40 * 1024 * 1024) {
          clearInterval(timer);
          if (!response.destroyed) response.end();
          return;
        }
        response.write(Buffer.alloc(64 * 1024, 0x63));
        sent += 64 * 1024;
      }, 10);
      request.on("close", () => clearInterval(timer));
      return;
    }
    if (path.startsWith("/size/")) {
      const megabytes = Number(path.slice(6));
      if (!Number.isInteger(megabytes) || megabytes < 1 || megabytes > 20) {
        response.writeHead(400, {"Content-Type": "text/plain"});
        response.end("invalid size");
        return;
      }
      response.writeHead(200, {
        "Content-Type": "application/octet-stream",
        "Content-Disposition": `attachment; filename="size-${megabytes}.bin"`,
      });
      response.end(Buffer.alloc(megabytes * 1024 * 1024, 0x64));
      return;
    }
    if (path === "/upload") {
      response.writeHead(200, {"Content-Type": "text/html; charset=utf-8"});
      response.end(`<!doctype html><html><body>
        <button id="upload">Upload</button>
        <input id="file" type="file" style="display:none">
        <script>document.getElementById("upload").addEventListener("click", () => document.getElementById("file").click())</script>
      </body></html>`);
      return;
    }
    if (path === "/two") {
      response.writeHead(200, {"Content-Type": "text/html; charset=utf-8"});
      response.end("<!doctype html><html><body><input id=\"alpha\"><input id=\"beta\"></body></html>");
      return;
    }
    if (path === "/slow-page") {
      response.writeHead(200, {"Content-Type": "text/html; charset=utf-8"});
      response.end('<!doctype html><html><body><a id="slow" href="/slow" download>slow</a></body></html>');
      return;
    }
    if (path === "/downloads") {
      const links = Array.from({length: 30}, (_, index) =>
        `<a id="d${index + 1}" href="/f/${index + 1}" download>d${index + 1}</a>`).join("");
      response.writeHead(200, {"Content-Type": "text/html; charset=utf-8"});
      response.end(`<!doctype html><html><body>${links}</body></html>`);
      return;
    }
    response.writeHead(404, {"Content-Type": "text/plain"});
    response.end("not found");
  });
}

async function startRealHost({downloadOptions = null} = {}) {
  const server = localServer();
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  const origin = `http://127.0.0.1:${server.address().port}`;
  const root = await mkdtemp(join(tmpdir(), "aha2-real-chromium-"));
  const host = createHostServer({
    root, token: TOKEN, hostID: "real", chromium,
    edgeExecutable: () => chromiumExecutable,
    headless: true,
    downloadOptions,
  });
  await new Promise(resolve => host.server.listen(0, "127.0.0.1", resolve));
  const base = `http://127.0.0.1:${host.server.address().port}`;
  const call = async (method, path, body) => {
    const response = await fetch(base + path, {
      method,
      headers: {Authorization: `Bearer ${TOKEN}`, "Content-Type": "application/json"},
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    const payload = await response.json();
    return {status: response.status, body: payload};
  };
  const close = async () => {
    await host.shutdown();
    await new Promise(resolve => host.server.close(resolve));
    await new Promise(resolve => server.close(resolve));
  };
  return {host, root, origin, call, close};
}

async function openTab(host, url, fence = "") {
  const opened = await host.call("POST", `/v1/profiles/${PROFILE}/session`, {browser_kind: "edge"});
  assert.equal(opened.status, 200);
  if (fence) {
    const fenced = await host.call("POST", `/v1/profiles/${PROFILE}/fence`,
      {expected_fence: "", fence});
    assert.equal(fenced.status, 200);
  }
  const tab = await host.call("POST", `/v1/profiles/${PROFILE}/tabs`, fence ? {url, fence} : {url});
  assert.equal(tab.status, 201);
  return tab.body.tab.id;
}

function pageFor(host, tabID) {
  return host.host.state.get(PROFILE).tabs.get(tabID).page;
}

function downloadStats(host) {
  return host.host.state.get(PROFILE).downloads.stats();
}

async function waitFor(predicate, timeoutMs = 5000, label = "condition") {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (await predicate()) return;
    await sleep(25);
  }
  throw new Error(`timed out waiting for ${label}`);
}

async function slowServer() {
  const server = createServer((request, response) => {
    response.writeHead(200, {
      "Content-Type": "application/octet-stream",
      "Content-Disposition": 'attachment; filename="slow.bin"',
    });
    const timer = setInterval(() => {
      if (response.destroyed) {
        clearInterval(timer);
        return;
      }
      response.write(Buffer.alloc(64 * 1024, 0x62));
    }, 50);
    request.on("close", () => clearInterval(timer));
  });
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  return server;
}

test("real chromium: disabled upload target fails cleanly and host stays alive", {
  skip: !enabled, timeout: 40_000,
}, async () => {
  const host = await startRealHost();
  try {
    const fence = `grant-real-1@${Date.now() + 60_000}`;
    const tabID = await openTab(host, `${host.origin}/upload`, fence);
    const snapshot = await host.call("GET", `/v1/profiles/${PROFILE}/tabs/${tabID}/snapshot`);
    assert.equal(snapshot.status, 200);
    const upload = snapshot.body.snapshot.elements.find(element => element.name === "Upload");
    assert.ok(upload, "upload button was not snapshotted");
    const page = pageFor(host, tabID);
    // Snapshot first, then disable the button. A click now waits for the
    // element to become enabled and must not leave the file-chooser timer as an
    // unhandled rejection.
    await page.evaluate(() => { document.getElementById("upload").disabled = true; });
    const started = Date.now();
    const result = await host.call("POST", `/v1/profiles/${PROFILE}/tabs/${tabID}/uploads`, {
      fence, ref: upload.ref,
      files: [{filename: "note.txt", content_base64: Buffer.from("hello").toString("base64")}],
    });
    const elapsed = Date.now() - started;
    assert.equal(result.status, 502);
    assert.equal(result.body.error.code, "browser_upload_failed");
    assert.ok(elapsed >= 4000, `click/chooser timeout was not exercised (${elapsed}ms)`);
    const alive = await host.call("GET", `/v1/profiles/${PROFILE}/tabs`);
    assert.equal(alive.status, 200);
  } finally {
    await host.close();
  }
});

test("real chromium: concurrent typing is serialized to one focused field", {
  skip: !enabled, timeout: 40_000,
}, async () => {
  const host = await startRealHost();
  try {
    const fence = `grant-real-2@${Date.now() + 60_000}`;
    const tabID = await openTab(host, `${host.origin}/two`, fence);
    const snapshot = await host.call("GET", `/v1/profiles/${PROFILE}/tabs/${tabID}/snapshot`);
    const inputs = snapshot.body.snapshot.elements.filter(element => element.role === "textbox");
    assert.equal(inputs.length, 2);
    const page = pageFor(host, tabID);
    for (let round = 0; round < 5; round++) {
      await page.evaluate(() => {
        document.getElementById("alpha").value = "";
        document.getElementById("beta").value = "";
      });
      const sensitiveA = `alpha-secret-${round}`;
      const sensitiveB = `beta-secret-${round}`;
      const [left, right] = await Promise.all([
        host.call("POST", `/v1/profiles/${PROFILE}/tabs/${tabID}/actions`,
          {fence, kind: "type", ref: inputs[0].ref, text: sensitiveA}),
        host.call("POST", `/v1/profiles/${PROFILE}/tabs/${tabID}/actions`,
          {fence, kind: "type", ref: inputs[1].ref, text: sensitiveB}),
      ]);
      assert.equal(left.status, 200);
      assert.equal(right.status, 200);
      const values = await page.evaluate(() => ({
        alpha: document.getElementById("alpha").value,
        beta: document.getElementById("beta").value,
      }));
      assert.equal(values.alpha, sensitiveA, `round ${round}: alpha=${values.alpha}`);
      assert.equal(values.beta, sensitiveB, `round ${round}: beta=${values.beta}`);
    }
  } finally {
    await host.close();
  }
});

test("real chromium: 25 downloads exceed the old 20-file cap and clear removes them", {
  skip: !enabled, timeout: 90_000,
}, async () => {
  const host = await startRealHost();
  try {
    const tabID = await openTab(host, `${host.origin}/downloads`);
    const page = pageFor(host, tabID);
    const downloads = [];
    page.on("download", download => downloads.push(download));

    // Trigger more downloads than the removed 20-file cap. Every download
    // must be accepted and retained.
    for (let index = 1; index <= 25; index++) {
      await page.click(`#d${index}`);
    }
    let listed = [];
    for (let attempt = 0; attempt < 200; attempt++) {
      listed = (await host.call("GET", `/v1/profiles/${PROFILE}/downloads`)).body.downloads;
      if (listed.length >= 25) break;
      await sleep(50);
    }
    assert.equal(listed.length, 25, `download list length: ${listed.length}`);
    const session = await host.call("GET", `/v1/profiles/${PROFILE}/session`);
    const directory = join(host.root, ".downloads", PROFILE, session.body.session.id);
    const files = await readdir(directory).catch(() => []);
    assert.equal(files.filter(name => name !== "raw").length, 25, `download directory: ${files.length}`);

    // The Playwright artifact must be gone after the controlled copy exists.
    const first = downloads[0];
    assert.ok(first, "no download event observed");
    await sleep(300);
    const artifact = await first.path().catch(() => "");
    if (artifact) await assert.rejects(access(artifact), "raw download artifact still exists");

    // Clear (the release path) removes copies and cancels in-flight work.
    const cleared = await host.call("DELETE", `/v1/profiles/${PROFILE}/downloads`);
    assert.equal(cleared.status, 200);
    const afterClear = await readdir(directory).catch(() => []);
    assert.deepEqual(afterClear.filter(name => name !== "raw"), []);
    assert.equal((await host.call("GET", `/v1/profiles/${PROFILE}/downloads`)).body.downloads.length, 0);
    assert.equal((await host.call("GET", "/v1/health")).status, 200);
  } finally {
    await host.close();
  }
});

test("real chromium: a download larger than the old 10 MiB cap succeeds", {
  skip: !enabled, timeout: 90_000,
}, async () => {
  const host = await startRealHost();
  try {
    const tabID = await openTab(host, `${host.origin}/blank`);
    const page = pageFor(host, tabID);
    await page.evaluate(() => {
      const anchor = document.createElement("a");
      anchor.id = "huge";
      anchor.href = "/huge";
      anchor.download = "huge.bin";
      anchor.textContent = "huge";
      document.body.appendChild(anchor);
    });
    await page.click("#huge");
    await waitFor(() => {
      const current = downloadStats(host);
      return current.entries === 1 && current.totalBytes >= 40 * 1024 * 1024 && current.inFlight === 0;
    }, 30_000, "large download retained");
    const stats = downloadStats(host);
    assert.ok(stats.totalBytes >= 40 * 1024 * 1024, JSON.stringify(stats));
    const listed = (await host.call("GET", `/v1/profiles/${PROFILE}/downloads`)).body.downloads;
    assert.equal(listed.length, 1, JSON.stringify(listed));
    assert.ok(listed[0].size >= 40 * 1024 * 1024, JSON.stringify(listed));
    const rawDirectory = join(host.root, ".downloads", PROFILE,
      (await host.call("GET", `/v1/profiles/${PROFILE}/session`)).body.session.id, "raw");
    await waitFor(async () => (await readdir(rawDirectory).catch(() => [])).length === 0,
      5_000, "raw artifact cleanup");
    const cleared = await host.call("DELETE", `/v1/profiles/${PROFILE}/downloads`);
    assert.equal(cleared.status, 200);
    await waitFor(() => downloadStats(host).entries === 0, 5_000, "clear");
    assert.equal((await host.call("GET", "/v1/health")).status, 200);
  } finally {
    await host.close();
  }
});

test("real chromium: 25 concurrent downloads are all accepted", {
  skip: !enabled, timeout: 90_000,
}, async () => {
  const slowServers = [];
  for (let index = 0; index < 25; index++) slowServers.push(await slowServer());
  const host = await startRealHost();
  try {
    const tabID = await openTab(host, `${host.origin}/blank`);
    const page = pageFor(host, tabID);
    const ports = slowServers.map(server => server.address().port);
    await page.evaluate(ports => {
      document.body.innerHTML = ports.map((port, index) =>
        `<a id="s${index}" href="http://127.0.0.1:${port}/slow" download>s${index}</a>`).join("");
    }, ports);
    for (let index = 0; index < 25; index++) await page.click(`#s${index}`);
    await waitFor(() => {
      const current = downloadStats(host);
      return current.entries === 25 && current.inFlight === 25;
    }, 15_000, "all 25 concurrent downloads accepted");
    const stats = downloadStats(host);
    console.log("concurrent download stats:", JSON.stringify(stats));
    assert.equal(stats.entries, 25, JSON.stringify(stats));
    assert.equal(stats.inFlight, 25, JSON.stringify(stats));
    const session = await host.call("GET", `/v1/profiles/${PROFILE}/session`);
    const rawDirectory = join(host.root, ".downloads", PROFILE, session.body.session.id, "raw");
    const cleared = await host.call("DELETE", `/v1/profiles/${PROFILE}/downloads`);
    assert.equal(cleared.status, 200);
    await waitFor(() => downloadStats(host).entries === 0 && downloadStats(host).inFlight === 0,
      5_000, "clear");
    await waitFor(async () => (await readdir(rawDirectory).catch(() => [])).length === 0,
      5_000, "raw directory cleanup");
    assert.deepEqual(await readdir(rawDirectory).catch(() => []), [], "raw directory is not empty");
    assert.equal((await host.call("GET", "/v1/health")).status, 200);
  } finally {
    await host.close();
    for (const server of slowServers) {
      server.closeAllConnections?.();
      await new Promise(resolve => server.close(resolve));
    }
  }
});

test("real chromium: downloads stay available until release clear", {
  skip: !enabled, timeout: 40_000,
}, async () => {
  const host = await startRealHost();
  try {
    const tabID = await openTab(host, `${host.origin}/downloads`);
    const page = pageFor(host, tabID);
    await page.click("#d1");
    let listed = [];
    for (let attempt = 0; attempt < 100; attempt++) {
      listed = (await host.call("GET", `/v1/profiles/${PROFILE}/downloads`)).body.downloads;
      if (listed.length > 0) break;
      await sleep(20);
    }
    assert.ok(listed.length > 0, "download did not arrive");
    await sleep(1500);
    const retained = await host.call("GET", `/v1/profiles/${PROFILE}/downloads`);
    assert.equal(retained.body.downloads.length, 1, "a download must not expire on a timer");
    const cleared = await host.call("DELETE", `/v1/profiles/${PROFILE}/downloads`);
    assert.equal(cleared.status, 200);
    assert.deepEqual((await host.call("GET", `/v1/profiles/${PROFILE}/downloads`)).body.downloads, []);
    assert.equal((await host.call("GET", "/v1/health")).status, 200);
  } finally {
    await host.close();
  }
});
