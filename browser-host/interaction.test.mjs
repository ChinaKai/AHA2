import test from "node:test";
import assert from "node:assert/strict";
import {mkdtemp} from "node:fs/promises";
import {tmpdir} from "node:os";
import {join} from "node:path";
import {createHostServer} from "./server.mjs";
import {createDownloadStore} from "./downloads.mjs";
import {MAX_ACTION_TEXT_BYTES} from "./interact.mjs";

const futureFence = id => `${id}@${Date.now() + 60_000}`;

async function startHost({elements = [], cookieList = [], downloadBody = "download-body", downloadOptions = null} = {}) {
  const root = await mkdtemp(join(tmpdir(), "aha2-browser-interaction-"));
  const calls = {actions: [], uploads: [], addedCookies: [], cookieURLs: null, chooserFiles: null};
  const listeners = {};
  let currentURL = "https://example.com/login";
  const fixtureElements = elements.length
    ? elements
    : [
      {role: "textbox", name: "Email", bounds: {x: 10, y: 20, width: 200, height: 30}, actions: ["click", "type"], fileInput: false},
      {role: "textbox", name: "Password", bounds: {x: 10, y: 60, width: 200, height: 30}, actions: ["click", "type"], fileInput: false},
      {role: "button", name: "Sign in", bounds: {x: 10, y: 100, width: 90, height: 30}, actions: ["click", "double_click"], fileInput: false},
      {role: "button", name: "Upload", bounds: {x: 10, y: 140, width: 90, height: 30}, actions: ["click", "double_click"], fileInput: true},
    ];
  const entryFor = ref => fixtureElements[Number(String(ref).split("-")[1]) - 1] || null;
  const locatorFor = selector => {
    const ref = /data-aha2-ref="([^"]+)"/.exec(String(selector))?.[1] || "";
    const entry = entryFor(ref);
    const locator = {
      first() { return locator; },
      waitFor: async () => { if (!entry) throw new Error("not attached"); },
      count: async () => (entry ? 1 : 0),
      evaluate: async () => Boolean(entry && entry.fileInput),
      click: async () => {
        calls.actions.push({kind: "click", ref});
        if (entry && entry.clickError) throw entry.clickError;
        if (entry && entry.opensChooser && typeof listeners.filechooser === "function") {
          listeners.filechooser(chooser);
        }
      },
      dblclick: async () => { calls.actions.push({kind: "double_click", ref}); },
      hover: async () => { calls.actions.push({kind: "hover", ref}); },
      setInputFiles: async files => { calls.uploads.push({ref, files}); },
    };
    return locator;
  };
  const chooser = {
    setFiles: async files => { calls.chooserFiles = files; },
  };
  const page = {
    closed: 0,
    url: () => currentURL,
    title: async () => "Login",
    goto: async target => { currentURL = target; },
    close: async () => { page.closed++; },
    bringToFront: async () => {},
    isClosed: () => false,
    on: (event, fn) => { listeners[event] = fn; },
    once: (event, fn) => { listeners[event] = fn; },
    removeListener: event => { delete listeners[event]; },
    screenshot: async () => Buffer.from([0xff, 0xd8, 0xff, 0xd9]),
    evaluate: async (fn, arg) => {
      if (typeof arg === "number") return "page text";
      if (arg && typeof arg === "object" && typeof arg.max === "number") {
        return fixtureElements.map((element, index) => ({
          ref: `${arg.prefix}${index + 1}`,
          role: element.role, name: element.name, bounds: element.bounds, actions: element.actions,
        }));
      }
      if (String(fn).includes("visibilityState")) return "visible";
      return {width: 1280, height: 800};
    },
    locator: locatorFor,
    mouse: {
      click: async (x, y) => { calls.actions.push({kind: "mouse-click", x, y}); },
      dblclick: async (x, y) => { calls.actions.push({kind: "mouse-double-click", x, y}); },
      move: async (x, y) => { calls.actions.push({kind: "mouse-move", x, y}); },
      wheel: async (deltaX, deltaY) => { calls.actions.push({kind: "wheel", deltaX, deltaY}); },
    },
    keyboard: {
      type: async text => { calls.actions.push({kind: "type", text}); },
      press: async keys => { calls.actions.push({kind: "press", keys}); },
    },
    waitForEvent: async event => {
      if (event !== "filechooser") throw new Error(`unexpected event ${event}`);
      return new Promise(resolve => {
        listeners.filechooser = resolve;
      });
    },
  };
  const context = {
    on: () => {},
    pages: () => [page],
    newPage: async () => page,
    close: async () => {},
    cookies: async urls => { calls.cookieURLs = urls || null; return cookieList; },
    addCookies: async cookies => { calls.addedCookies.push(...cookies); },
  };
  const chromium = {launchPersistentContext: async () => context};
  const host = createHostServer({
    root, token: "test-token", hostID: "test-host", chromium,
    edgeExecutable: () => "C:\\Program Files\\msedge.exe",
    downloadOptions,
  });
  await new Promise(resolve => host.server.listen(0, "127.0.0.1", resolve));
  const base = `http://127.0.0.1:${host.server.address().port}`;
  const call = async (method, path, body) => {
    const response = await fetch(base + path, {
      method,
      headers: {"Authorization": "Bearer test-token", "Content-Type": "application/json"},
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    const payload = await response.json();
    return {status: response.status, body: payload, headers: response.headers};
  };
  const raw = async path => {
    const response = await fetch(base + path, {headers: {"Authorization": "Bearer test-token"}});
    return {status: response.status, headers: response.headers, body: Buffer.from(await response.arrayBuffer())};
  };
  const close = async () => {
    await host.shutdown();
    await new Promise(resolve => host.server.close(resolve));
  };
  return {root, calls, listeners, downloadBody, page, fixtureElements, chooser, call, raw, close};
}

async function openSession(host, fence = "") {
  const opened = await host.call("POST", "/v1/profiles/browser_profile_1/session", {browser_kind: "edge"});
  assert.equal(opened.status, 200);
  if (fence) {
    const fenced = await host.call("POST", "/v1/profiles/browser_profile_1/fence", {expected_fence: "", fence});
    assert.equal(fenced.status, 200);
  }
  const tabs = await host.call("GET", "/v1/profiles/browser_profile_1/tabs");
  assert.equal(tabs.status, 200);
  return tabs.body.tabs[0].id;
}

test("structured snapshot returns bounded element refs without values", async () => {
  const host = await startHost();
  try {
    const tabID = await openSession(host);
    const snapshot = await host.call("GET", `/v1/profiles/browser_profile_1/tabs/${tabID}/snapshot`);
    assert.equal(snapshot.status, 200);
    assert.equal(snapshot.body.snapshot.text, "page text");
    assert.equal(snapshot.body.snapshot.elements.length, 4);
    assert.equal(snapshot.body.snapshot.elements[0].ref, "e1-1");
    assert.equal(snapshot.body.snapshot.elements[0].role, "textbox");
    assert.equal(snapshot.body.snapshot.elements[0].name, "Email");
    // No input value, cookie or HTML source is part of an element.
    assert.deepEqual(Object.keys(snapshot.body.snapshot.elements[0]).sort(),
      ["actions", "bounds", "name", "ref", "role"]);
    const again = await host.call("GET", `/v1/profiles/browser_profile_1/tabs/${tabID}/snapshot`);
    assert.equal(again.body.snapshot.elements[0].ref, "e2-1");
  } finally {
    await host.close();
  }
});

test("page actions run through the fence and reject stale refs", async () => {
  const host = await startHost();
  try {
    const fence = futureFence("grant-1");
    const tabID = await openSession(host, fence);
    await host.call("GET", `/v1/profiles/browser_profile_1/tabs/${tabID}/snapshot`);

    let result = await host.call("POST", `/v1/profiles/browser_profile_1/tabs/${tabID}/actions`,
      {session_id: "", fence, kind: "click", ref: "e1-1"});
    assert.equal(result.status, 200);
    assert.equal(result.body.action.kind, "click");
    result = await host.call("POST", `/v1/profiles/browser_profile_1/tabs/${tabID}/actions`,
      {fence, kind: "type", ref: "e1-2", text: "owner-secret"});
    assert.equal(result.status, 200);
    result = await host.call("POST", `/v1/profiles/browser_profile_1/tabs/${tabID}/actions`,
      {fence, kind: "key", keys: ["CTRL", "L"]});
    assert.equal(result.status, 200);
    result = await host.call("POST", `/v1/profiles/browser_profile_1/tabs/${tabID}/actions`,
      {fence, kind: "scroll", delta_y: 400});
    assert.equal(result.status, 200);
    assert.ok(host.calls.actions.some(call => call.kind === "type" && call.text === "owner-secret"));
    assert.ok(host.calls.actions.some(call => call.kind === "press" && call.keys === "Control+L"));
    assert.ok(host.calls.actions.some(call => call.kind === "wheel" && call.deltaY === 400));

    // A newer snapshot advances the generation; the old ref is stale.
    await host.call("GET", `/v1/profiles/browser_profile_1/tabs/${tabID}/snapshot`);
    result = await host.call("POST", `/v1/profiles/browser_profile_1/tabs/${tabID}/actions`,
      {fence, kind: "click", ref: "e1-1"});
    assert.equal(result.status, 409);
    assert.equal(result.body.error.code, "browser_element_stale");

    result = await host.call("POST", `/v1/profiles/browser_profile_1/tabs/${tabID}/actions`,
      {fence, kind: "click", ref: "e2-99"});
    assert.equal(result.status, 404);
    assert.equal(result.body.error.code, "browser_element_not_found");

    result = await host.call("POST", `/v1/profiles/browser_profile_1/tabs/${tabID}/actions`,
      {fence, kind: "type", ref: "e2-1", text: "x".repeat(MAX_ACTION_TEXT_BYTES + 1)});
    assert.equal(result.status, 400);
    assert.equal(result.body.error.code, "browser_action_invalid");
    // The typed payload never appears in an error body.
    result = await host.call("POST", `/v1/profiles/browser_profile_1/tabs/${tabID}/actions`,
      {fence: "stale@1", kind: "type", ref: "e2-1", text: "must-not-leak"});
    assert.equal(result.status, 409);
    assert.doesNotMatch(JSON.stringify(result.body), /must-not-leak/);
  } finally {
    await host.close();
  }
});

test("upload sends bytes to a file input or a file chooser without a host path", async () => {
  const host = await startHost();
  try {
    const fence = futureFence("grant-1");
    const tabID = await openSession(host, fence);
    await host.call("GET", `/v1/profiles/browser_profile_1/tabs/${tabID}/snapshot`);

    let result = await host.call("POST", `/v1/profiles/browser_profile_1/tabs/${tabID}/uploads`,
      {fence, ref: "e1-4", files: [{filename: "note.txt", content_base64: Buffer.from("hello").toString("base64")}]});
    assert.equal(result.status, 200);
    assert.equal(result.body.upload.file_count, 1);
    assert.equal(host.calls.uploads.length, 1);
    assert.equal(host.calls.uploads[0].files[0].name, "note.txt");
    assert.equal(host.calls.uploads[0].files[0].buffer.toString(), "hello");

    result = await host.call("POST", `/v1/profiles/browser_profile_1/tabs/${tabID}/uploads`,
      {fence, ref: "e1-4", files: [{filename: "../escape.txt", content_base64: "YQ=="}]});
    assert.equal(result.status, 400);
    assert.equal(result.body.error.code, "browser_upload_invalid");

    result = await host.call("POST", `/v1/profiles/browser_profile_1/tabs/${tabID}/uploads`,
      {fence, files: [{filename: "note.txt", content_base64: "YQ=="}]});
    assert.equal(result.status, 400);
    assert.equal(result.body.error.code, "browser_upload_invalid");
  } finally {
    await host.close();
  }
});

test("captured downloads list, fetch raw bytes, reject traversal and clear", async () => {
  const host = await startHost({downloadBody: "download-body"});
  try {
    const tabID = await openSession(host);
    let deleted = 0;
    let canceled = 0;
    const download = {
      suggestedFilename: () => "report.csv",
      createReadStream: async () => {
        const {Readable} = await import("node:stream");
        return Readable.from([Buffer.from(host.downloadBody)]);
      },
      cancel: async () => { canceled++; },
      delete: async () => { deleted++; },
    };
    host.listeners.download(download);
    let listed = {body: {downloads: []}};
    for (let attempt = 0; attempt < 50; attempt++) {
      listed = await host.call("GET", "/v1/profiles/browser_profile_1/downloads");
      if (listed.body.downloads.length) break;
      await new Promise(resolve => setTimeout(resolve, 10));
    }
    assert.equal(listed.status, 200);
    assert.equal(listed.body.downloads.length, 1);
    assert.equal(listed.body.downloads[0].filename, "report.csv");
    const id = listed.body.downloads[0].id;
    assert.match(id, /^download-[a-f0-9]{24}$/);

    const fetched = await host.raw(`/v1/profiles/browser_profile_1/downloads/${id}`);
    assert.equal(fetched.status, 200);
    assert.equal(fetched.body.toString(), "download-body");
    assert.equal(decodeURIComponent(fetched.headers.get("x-aha2-download-filename")), "report.csv");
    // The raw Playwright artifact is removed after the controlled copy exists.
    assert.ok(deleted >= 1, "download artifact was not deleted");
    assert.equal(canceled, 0);

    // A new lease fence cannot see the previous holder's download.
    const fenced = await host.call("POST", "/v1/profiles/browser_profile_1/fence",
      {expected_fence: "", fence: futureFence("grant-next")});
    assert.equal(fenced.status, 200);
    const hidden = await host.call("GET", "/v1/profiles/browser_profile_1/downloads");
    assert.deepEqual(hidden.body.downloads, []);
    const hiddenFetch = await host.raw(`/v1/profiles/browser_profile_1/downloads/${id}`);
    assert.equal(hiddenFetch.status, 404);

    for (const bad of ["../../etc/passwd", "download/../../x", "not-a-download"]) {
      const denied = await host.raw(`/v1/profiles/browser_profile_1/downloads/${encodeURIComponent(bad)}`);
      assert.equal(denied.status, 404);
    }
    const cleared = await host.call("DELETE", "/v1/profiles/browser_profile_1/downloads");
    assert.equal(cleared.status, 200);
    const after = await host.call("GET", "/v1/profiles/browser_profile_1/downloads");
    assert.deepEqual(after.body.downloads, []);
    const directory = join(host.root, ".downloads", "browser_profile_1");
    const {readdir} = await import("node:fs/promises");
    for (const session of await readdir(directory).catch(() => [])) {
      const entries = await readdir(join(directory, session)).catch(() => []);
      assert.deepEqual(entries.filter(name => name !== "raw"), []);
      assert.deepEqual(await readdir(join(directory, session, "raw")).catch(() => []), []);
    }
  } finally {
    await host.close();
  }
});

test("cookie export is scoped and cookie import never echoes values", async () => {
  const cookies = [
    {name: "session", value: "cookie-secret", domain: ".example.com", path: "/", expires: -1, httpOnly: true, secure: true, sameSite: "Lax"},
    {name: "other", value: "other-secret", domain: "other.example", path: "/", expires: -1, httpOnly: false, secure: false, sameSite: "Lax"},
  ];
  const host = await startHost({cookieList: cookies});
  try {
    const fence = futureFence("grant-1");
    await openSession(host, fence);
    let result = await host.call("GET", "/v1/profiles/browser_profile_1/cookies?domain=example.com");
    assert.equal(result.status, 200);
    assert.equal(result.body.cookies.length, 1, "only matching-domain cookies are exported");
    assert.equal(result.body.cookies[0].http_only, true);
    assert.equal(result.body.cookies[0].same_site, "Lax");
    assert.equal(result.body.cookies[0].value, "cookie-secret");

    result = await host.call("GET", "/v1/profiles/browser_profile_1/cookies");
    assert.equal(result.status, 400);
    assert.equal(result.body.error.code, "browser_cookie_invalid");

    result = await host.call("POST", "/v1/profiles/browser_profile_1/cookies", {
      fence,
      cookies: [{name: "imported", value: "import-secret", domain: "example.com", path: "/", expires: -1,
        http_only: true, secure: true, same_site: "None"}],
    });
    assert.equal(result.status, 200);
    assert.equal(result.body.count, 1);
    assert.equal(host.calls.addedCookies[0].httpOnly, true);
    assert.equal(host.calls.addedCookies[0].sameSite, "None");
    assert.doesNotMatch(JSON.stringify(result.body), /import-secret/);

    result = await host.call("POST", "/v1/profiles/browser_profile_1/cookies", {
      fence,
      cookies: [{name: "bad", value: "bad-secret", domain: "example.com", path: "/", expires: -1,
        secure: false, same_site: "None"}],
    });
    assert.equal(result.status, 400);
    assert.equal(result.body.error.code, "browser_cookie_invalid");
    assert.doesNotMatch(JSON.stringify(result.body), /bad-secret/);
  } finally {
    await host.close();
  }
});

test("file chooser upload succeeds and a failed click leaves the host alive", async () => {
  const host = await startHost();
  try {
    const fence = futureFence("grant-1");
    const tabID = await openSession(host, fence);
    await host.call("GET", `/v1/profiles/browser_profile_1/tabs/${tabID}/snapshot`);
    const button = host.fixtureElements[2];
    button.opensChooser = true;

    let result = await host.call("POST", `/v1/profiles/browser_profile_1/tabs/${tabID}/uploads`,
      {fence, ref: "e1-3", files: [{filename: "note.txt", content_base64: "aGVsbG8="}]});
    assert.equal(result.status, 200);
    assert.equal(host.calls.chooserFiles[0].name, "note.txt");

    // A disabled target makes Playwright's click fail. The chooser waiter must
    // be canceled instead of surfacing an unhandled TimeoutError.
    button.clickError = new Error("element is not enabled");
    result = await host.call("POST", `/v1/profiles/browser_profile_1/tabs/${tabID}/uploads`,
      {fence, ref: "e1-3", files: [{filename: "note.txt", content_base64: "aGVsbG8="}]});
    assert.equal(result.status, 502);
    assert.equal(result.body.error.code, "browser_upload_failed");
    const alive = await host.call("GET", "/v1/profiles/browser_profile_1/tabs");
    assert.equal(alive.status, 200);
  } finally {
    await host.close();
  }
});

test("download store caps in-flight files and rejects beyond the declared count", async () => {
  const host = await startHost({downloadOptions: {maxFiles: 2}});
  const {Readable} = await import("node:stream");
  try {
    await openSession(host);
    const gates = [];
    const deleted = [];
    const makeDownload = index => {
      let release;
      const gate = new Promise(resolve => { release = resolve; });
      return {
        release,
        suggestedFilename: () => `f${index}.txt`,
        createReadStream: async () => {
          await gate;
          return Readable.from([Buffer.from(`body-${index}`)]);
        },
        cancel: async () => {},
        delete: async () => { deleted.push(index); },
      };
    };
    const downloads = [1, 2, 3].map(makeDownload);
    for (const download of downloads) host.listeners.download(download);
    // Reservation is synchronous, so the third event must be rejected while
    // the first two are still saving.
    await new Promise(resolve => setTimeout(resolve, 20));
    downloads[0].release();
    downloads[1].release();
    let listed = [];
    for (let attempt = 0; attempt < 50; attempt++) {
      listed = (await host.call("GET", "/v1/profiles/browser_profile_1/downloads")).body.downloads;
      if (listed.length === 2) break;
      await new Promise(resolve => setTimeout(resolve, 10));
    }
    assert.equal(listed.length, 2, "in-flight count must stay within maxFiles");
    assert.ok(deleted.includes(3), "rejected download artifact must be deleted");
    assert.equal((await host.call("DELETE", "/v1/profiles/browser_profile_1/downloads")).status, 200);
  } finally {
    await host.close();
  }
});

test("download TTL timer removes entries and files without a read", async () => {
  const host = await startHost({downloadOptions: {ttlMs: 100}});
  const {Readable} = await import("node:stream");
  try {
    await openSession(host);
    const download = {
      suggestedFilename: () => "ttl.txt",
      createReadStream: async () => Readable.from([Buffer.from("ttl-body")]),
      cancel: async () => {},
      delete: async () => {},
    };
    host.listeners.download(download);
    let listed = [];
    for (let attempt = 0; attempt < 50; attempt++) {
      listed = (await host.call("GET", "/v1/profiles/browser_profile_1/downloads")).body.downloads;
      if (listed.length === 1) break;
      await new Promise(resolve => setTimeout(resolve, 10));
    }
    assert.equal(listed.length, 1);
    await new Promise(resolve => setTimeout(resolve, 400));
    const after = await host.call("GET", "/v1/profiles/browser_profile_1/downloads");
    assert.deepEqual(after.body.downloads, []);
    const directory = join(host.root, ".downloads", "browser_profile_1");
    const {readdir} = await import("node:fs/promises");
    const sessions = await readdir(directory).catch(() => []);
    for (const session of sessions) {
      const entries = await readdir(join(directory, session)).catch(() => []);
      assert.deepEqual(entries.filter(name => name !== "raw"), []);
      assert.deepEqual(await readdir(join(directory, session, "raw")).catch(() => []), []);
    }
  } finally {
    await host.close();
  }
});

test("clearing downloads cancels an in-flight stream and deletes its artifact", async () => {
  const host = await startHost({downloadOptions: {maxFiles: 5}});
  const {PassThrough} = await import("node:stream");
  try {
    await openSession(host);
    let stream = null;
    let canceled = 0;
    let deleted = 0;
    const download = {
      suggestedFilename: () => "slow.bin",
      createReadStream: async () => {
        stream = new PassThrough();
        return stream;
      },
      cancel: async () => {
        canceled++;
        stream?.destroy(new Error("canceled by clear"));
      },
      delete: async () => { deleted++; },
    };
    host.listeners.download(download);
    for (let attempt = 0; attempt < 50 && !stream; attempt++) {
      await new Promise(resolve => setTimeout(resolve, 10));
    }
    assert.ok(stream, "stream did not start");
    const cleared = await host.call("DELETE", "/v1/profiles/browser_profile_1/downloads");
    assert.equal(cleared.status, 200);
    assert.ok(canceled >= 1 && deleted >= 1);
    assert.equal((await host.call("GET", "/v1/profiles/browser_profile_1/tabs")).status, 200);
  } finally {
    await host.close();
  }
});


test("failed chunks release only the budget they reserved", async () => {
  const root = await mkdtemp(join(tmpdir(), "aha2-download-budget-"));
  const {Readable} = await import("node:stream");
  const store = createDownloadStore(root, "profile", "session", {
    maxBytes: 10 * 1024 * 1024,
    maxTotalBytes: 8 * 1024 * 1024,
    ttlMs: 60_000,
  });
  let releaseA;
  const gateA = new Promise(resolve => { releaseA = resolve; });
  const makeDownload = (chunks, gate = null) => ({
    suggestedFilename: () => "f.bin",
    createReadStream: async () => Readable.from((async function* () {
      for (const chunk of chunks) yield Buffer.alloc(chunk, 0x61);
      if (gate) await gate;
    })()),
    cancel: async () => {},
    delete: async () => {},
  });
  const captureA = store.capture(makeDownload([6 * 1024 * 1024], gateA), "f");
  await new Promise(resolve => setTimeout(resolve, 20));
  // A holds 6 MiB. B gets one 2 MiB chunk (pending 8) and then exceeds the
  // budget; only its charged 2 MiB may be returned, not the rejected chunk.
  await assert.rejects(store.capture(makeDownload([2 * 1024 * 1024, 4 * 1024 * 1024]), "f"));
  const afterB = store.stats();
  assert.equal(afterB.pendingBytes, 6 * 1024 * 1024, JSON.stringify(afterB));
  // If B had over-released, C's 4 MiB would now fit (6+4>8 must reject).
  await assert.rejects(store.capture(makeDownload([4 * 1024 * 1024]), "f"));
  releaseA();
  await captureA;
  const final = store.stats();
  assert.equal(final.totalBytes, 6 * 1024 * 1024, JSON.stringify(final));
  assert.equal(final.pendingBytes, 0, JSON.stringify(final));
  await store.close();
});

test("clear waits out an old capture so it cannot publish or release into the new budget", async () => {
  const root = await mkdtemp(join(tmpdir(), "aha2-download-clear-"));
  const {Readable} = await import("node:stream");
  const store = createDownloadStore(root, "profile", "session", {
    maxBytes: 10 * 1024 * 1024,
    maxTotalBytes: 50 * 1024 * 1024,
    ttlMs: 60_000,
  });
  let release;
  const gate = new Promise(resolve => { release = resolve; });
  let canceled = 0;
  let settled = false;
  const oldDownload = {
    suggestedFilename: () => "old.bin",
    createReadStream: async () => Readable.from((async function* () {
      yield Buffer.alloc(1024 * 1024, 0x61);
      await gate;
    })()),
    cancel: async () => { canceled += 1; },
    delete: async () => {},
  };
  const oldCapture = store.capture(oldDownload, "f").catch(() => { settled = true; });
  for (let attempt = 0; attempt < 100 && store.stats().pendingBytes === 0; attempt++) {
    await new Promise(resolve => setTimeout(resolve, 10));
  }
  assert.equal(store.stats().pendingBytes, 1024 * 1024);
  const clearPromise = store.clear();
  await new Promise(resolve => setTimeout(resolve, 50));
  assert.equal(settled, false, "clear returned before the old capture exited");
  release();
  await oldCapture;
  await clearPromise;
  assert.ok(canceled >= 1);
  const cleared = store.stats();
  assert.deepEqual(
    {entries: cleared.entries, totalBytes: cleared.totalBytes, pendingBytes: cleared.pendingBytes},
    {entries: 0, totalBytes: 0, pendingBytes: 0});
  assert.deepEqual(await store.list("f"), []);

  // The stale capture's finally must not subtract its old 1 MiB from the new
  // generation's pending budget: a fresh 1 MiB must remain exactly 1 MiB.
  const fresh = {
    suggestedFilename: () => "fresh.bin",
    createReadStream: async () => Readable.from([Buffer.alloc(1024 * 1024, 0x62)]),
    cancel: async () => {},
    delete: async () => {},
  };
  await store.capture(fresh, "f");
  const after = store.stats();
  assert.equal(after.totalBytes, 1024 * 1024, JSON.stringify(after));
  assert.equal(after.pendingBytes, 0, JSON.stringify(after));
  await store.close();
});

test("clear during concurrent fills resets all counters and accepts a new-generation budget", async () => {
  const root = await mkdtemp(join(tmpdir(), "aha2-download-clear-stress-"));
  const {PassThrough} = await import("node:stream");
  const store = createDownloadStore(root, "profile", "session", {
    maxBytes: 10 * 1024 * 1024,
    maxTotalBytes: 50 * 1024 * 1024,
    ttlMs: 60_000,
  });
  const streams = [];
  const captures = [];
  for (let index = 0; index < 10; index++) {
    const stream = new PassThrough();
    streams.push(stream);
    const download = {
      suggestedFilename: () => `stress-${index}.bin`,
      createReadStream: async () => stream,
      cancel: async () => { stream.destroy(new Error("canceled by clear")); },
      delete: async () => {},
    };
    captures.push(store.capture(download, "f").catch(() => {}));
  }
  for (const stream of streams) {
    for (let chunk = 0; chunk < 6; chunk++) stream.write(Buffer.alloc(1024 * 1024, 0x63));
  }
  await new Promise(resolve => setTimeout(resolve, 50));
  assert.ok(store.stats().totalBytes + store.stats().pendingBytes > 0);
  await store.clear();
  await Promise.allSettled(captures);
  const cleared = store.stats();
  assert.equal(cleared.entries, 0, JSON.stringify(cleared));
  assert.equal(cleared.totalBytes, 0, JSON.stringify(cleared));
  assert.equal(cleared.pendingBytes, 0, JSON.stringify(cleared));
  assert.deepEqual(await store.list("f"), []);

  const fresh = {
    suggestedFilename: () => "fresh.bin",
    createReadStream: async () => {
      const {Readable} = await import("node:stream");
      return Readable.from([Buffer.alloc(4 * 1024 * 1024, 0x64)]);
    },
    cancel: async () => {},
    delete: async () => {},
  };
  await store.capture(fresh, "f");
  assert.equal(store.stats().totalBytes, 4 * 1024 * 1024, JSON.stringify(store.stats()));
  assert.equal(store.stats().pendingBytes, 0);
  await store.close();
});
