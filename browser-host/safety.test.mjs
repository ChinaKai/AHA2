import test from "node:test";
import assert from "node:assert/strict";
import {mkdtemp, mkdir, writeFile, symlink, rm} from "node:fs/promises";
import {tmpdir} from "node:os";
import {join} from "node:path";
import {createHostServer} from "./server.mjs";

const futureFence = id => `${id}@${Date.now() + 60_000}`;

async function startHost({launchFailure = false, tabGoto = null, pageClose = null, screenshotFailure = false, initialURL = "https://example.com/final", initialURLs = null, pageStates = null, beforeMutation = null, fenceWaitTimeoutMs, beforeBookmarkOpen = null, failLeaseHold = false} = {}) {
  const root = await mkdtemp(join(tmpdir(), "aha2-browser-host-"));
  let screenshots = 0;
  let shutdownCalls = 0;
  const makePage = (initialURL = "https://example.com/final", visible = true, focused = false, focusThrows = false, hang = false) => {
    let currentURL = initialURL;
    const page = {
      closed: 0,
      url: () => currentURL,
      title: async () => hang ? new Promise(() => {}) : "Example",
      goto: async target => {
        if (tabGoto) await tabGoto(target);
        currentURL = target;
      },
      close: async () => {
        if (pageClose) await pageClose(page);
        page.closed++;
      },
      bringToFront: async () => { page.broughtToFront = (page.broughtToFront || 0) + 1; },
      // Snapshot passes a numeric limit; the visibility and focus probes pass
      // no args. A page can be visible in its window without owning the
      // foreground focus, which is exactly the multi-window case.
      evaluate: async (fn, arg) => {
        if (hang) return new Promise(() => {});
        if (typeof arg === "number") return "page text";
        if (String(fn).includes("hasFocus")) {
          if (focusThrows) throw new Error("hasFocus unavailable");
          return focused;
        }
        return visible ? "visible" : "hidden";
      },
      isClosed: () => false,
      on: () => {},
      screenshot: async () => {
        if (screenshotFailure) throw new Error(`screenshot failed under ${root}`);
        screenshots++;
        return Buffer.from([0xff, 0xd8, 0xff, 0x01, 0x02, 0xff, 0xd9]);
      },
    };
    return page;
  };
  // A persistent Edge context starts with one page; the Owner may then open
  // more. Both are listed as bindable pages by the host.
  const initialPages = pageStates
    ? pageStates.map(state => makePage(state.url, state.visible !== false, state.focused === true, state.focusThrows === true, state.hang === true))
    : (initialURLs || [initialURL]).map((url, index) => makePage(url, index === 0));
  const page = initialPages[0];
  let lastPage = page;
  const context = {
    on: () => {},
    close: async () => {},
    pages: () => initialPages,
    newPage: async () => {
      lastPage = makePage("about:blank");
      initialPages.push(lastPage);
      return lastPage;
    },
  };
  const chromium = {
    launchPersistentContext: async () => {
      if (launchFailure) throw new Error(`launch failed at ${root}\\browser_profile_1`);
      return context;
    },
  };
  const host = createHostServer({
    root, token: "test-token", hostID: "test-host", chromium,
    edgeExecutable: () => "C:\\Program Files\\msedge.exe",
    onShutdown: () => { shutdownCalls++; },
    beforeMutation,
    beforeBookmarkOpen,
    failLeaseHold,
    ...(fenceWaitTimeoutMs ? {fenceWaitTimeoutMs} : {}),
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
    return {status: response.status, body: payload};
  };
  const close = async () => {
    await host.shutdown();
    await new Promise(resolve => host.server.close(resolve));
  };
  const raw = async (method, path) => {
    const response = await fetch(base + path, {method, headers: {"Authorization": "Bearer test-token"}});
    return {status: response.status, headers: response.headers, body: Buffer.from(await response.arrayBuffer())};
  };
  return {
    root, host, base, call, raw, page: () => lastPage, close,
    screenshots: () => screenshots, shutdownCalls: () => shutdownCalls,
  };
}

test("tab navigation failure returns the declared 502, not host_internal_error", async () => {
  let first = true;
  const host = await startHost({
    tabGoto: async () => {
      if (!first) throw new Error("navigation failed inside profile root");
      first = false;
    },
  });
  try {
    let result = await host.call("POST", "/v1/profiles/browser_profile_1/session", {browser_kind: "edge"});
    assert.equal(result.status, 200);
    result = await host.call("POST", "/v1/profiles/browser_profile_1/tabs", {url: "https://example.com"});
    assert.equal(result.status, 201);
    const tabID = result.body.tab.id;
    result = await host.call("POST", `/v1/profiles/browser_profile_1/tabs/${tabID}/navigate`, {url: "https://example.com/next"});
    assert.equal(result.status, 502);
    assert.equal(result.body.error.code, "browser_navigation_failed");
    assert.doesNotMatch(JSON.stringify(result.body), /profile root|browser_profile_1/);
  } finally {
    await host.close();
  }
});

test("tab open failure closes the page and returns 502 without diagnostic paths", async () => {
  const host = await startHost({
    tabGoto: async () => { throw new Error(`goto failed at ${"/tmp/secret"}`); },
  });
  try {
    let result = await host.call("POST", "/v1/profiles/browser_profile_1/session", {browser_kind: "edge"});
    assert.equal(result.status, 200);
    result = await host.call("POST", "/v1/profiles/browser_profile_1/tabs", {url: "https://example.com"});
    assert.equal(result.status, 502);
    assert.equal(result.body.error.code, "browser_navigation_failed");
    assert.equal(host.page().closed, 1);
    assert.doesNotMatch(JSON.stringify(result.body), /secret/);
  } finally {
    await host.close();
  }
});

test("launch failure returns a safe message and never the profile root", async () => {
  const host = await startHost({launchFailure: true});
  try {
    const result = await host.call("POST", "/v1/profiles/browser_profile_1/session", {browser_kind: "edge"});
    assert.equal(result.status, 500);
    assert.equal(result.body.error.code, "browser_launch_failed");
    assert.doesNotMatch(JSON.stringify(result.body), /browser_profile_1|msedge\.exe|aha2-browser-host-/);
  } finally {
    await host.close();
  }
});

test("health reports the injected Edge availability", async () => {
  const host = await startHost();
  try {
    const result = await host.call("GET", "/v1/health");
    assert.equal(result.status, 200);
    assert.equal(result.body.host_id, "test-host");
    assert.equal(result.body.edge_available, true);
  } finally {
    await host.close();
  }
});

test("lists the page the Owner already has open, including its active state", async () => {
  const host = await startHost();
  try {
    const opened = await host.call("POST", "/v1/profiles/browser_profile_1/session", {browser_kind: "edge"});
    assert.equal(opened.status, 200);
    const listed = await host.call("GET", "/v1/profiles/browser_profile_1/tabs");
    assert.equal(listed.status, 200);
    assert.equal(listed.body.tabs.length, 1);
    assert.match(listed.body.tabs[0].id, /^host-tab-/);
    assert.equal(listed.body.tabs[0].url, "https://example.com/final");
    assert.equal(listed.body.tabs[0].active, true);

    // AHA-created tabs are pages too and stay addressable by their host id.
    const created = await host.call("POST", "/v1/profiles/browser_profile_1/tabs", {url: "https://example.com/created"});
    assert.equal(created.status, 201);
    const again = await host.call("GET", "/v1/profiles/browser_profile_1/tabs");
    assert.equal(again.body.tabs.length, 2);
    assert.ok(again.body.tabs.some(tab => tab.id === created.body.tab.id && tab.url === "https://example.com/created"));
  } finally {
    await host.close();
  }
});

test("reports foreground focus separately from visibility across browser windows", async () => {
  // Two Edge windows: each has one visible page, only the foreground window's
  // page has focus. Both stay active; focused identifies the Owner's window.
  const host = await startHost({pageStates: [
    {url: "https://example.com/notes", visible: true, focused: true},
    {url: "https://example.com/other", visible: true, focused: false},
  ]});
  try {
    const opened = await host.call("POST", "/v1/profiles/browser_profile_1/session", {browser_kind: "edge"});
    assert.equal(opened.status, 200);
    const listed = await host.call("GET", "/v1/profiles/browser_profile_1/tabs");
    assert.equal(listed.status, 200);
    assert.equal(listed.body.tabs.length, 2);
    const byURL = Object.fromEntries(listed.body.tabs.map(tab => [tab.url, tab]));
    assert.equal(byURL["https://example.com/notes"].active, true);
    assert.equal(byURL["https://example.com/notes"].focused, true);
    assert.equal(byURL["https://example.com/other"].active, true);
    assert.equal(byURL["https://example.com/other"].focused, false);
  } finally {
    await host.close();
  }
});

test("a page whose hasFocus probe throws is listed without focus", async () => {
  const host = await startHost({pageStates: [
    {url: "https://example.com/broken-focus", visible: true, focusThrows: true},
  ]});
  try {
    const opened = await host.call("POST", "/v1/profiles/browser_profile_1/session", {browser_kind: "edge"});
    assert.equal(opened.status, 200);
    const listed = await host.call("GET", "/v1/profiles/browser_profile_1/tabs");
    assert.equal(listed.status, 200);
    assert.equal(listed.body.tabs.length, 1);
    assert.equal(listed.body.tabs[0].active, true);
    assert.equal(listed.body.tabs[0].focused, false);
  } finally {
    await host.close();
  }
});

test("hung pages keep one wait budget instead of serializing probes", async () => {
  // 16 pages that never answer title/visibility. With 8 list workers the old
  // serial title+visibility+focus chain cost ~9s; one shared budget per page
  // costs two waves of PAGE_METADATA_TIMEOUT_MS (~3s).
  const pageStates = Array.from({length: 16}, (_, index) => ({
    url: `https://example.com/hung-${index}`, hang: true,
  }));
  const host = await startHost({pageStates});
  try {
    const opened = await host.call("POST", "/v1/profiles/browser_profile_1/session", {browser_kind: "edge"});
    assert.equal(opened.status, 200);
    const startedAt = Date.now();
    const listed = await host.call("GET", "/v1/profiles/browser_profile_1/tabs");
    const elapsed = Date.now() - startedAt;
    assert.equal(listed.status, 200);
    assert.equal(listed.body.tabs.length, 16);
    assert.ok(listed.body.tabs.every(tab => tab.active === false && tab.focused === false),
      "a hung page must fall back to active=false, focused=false");
    console.log(`16 hung pages listed in ${elapsed}ms`);
    assert.ok(elapsed < 6000, `hung pages must not serialize probes: ${elapsed}ms`);
  } finally {
    await host.close();
  }
});

test("does not list about:blank or internal browser surfaces as bindable pages", async () => {
  const host = await startHost({initialURL: "about:blank"});
  try {
    const opened = await host.call("POST", "/v1/profiles/browser_profile_1/session", {browser_kind: "edge"});
    assert.equal(opened.status, 200);
    const listed = await host.call("GET", "/v1/profiles/browser_profile_1/tabs");
    assert.equal(listed.status, 200);
    assert.deepEqual(listed.body.tabs, []);
  } finally {
    await host.close();
  }
});

test("tab frame returns JPEG bytes and reuses the last frame inside the throttle window", async () => {
  const host = await startHost();
  try {
    let result = await host.call("POST", "/v1/profiles/browser_profile_1/session", {browser_kind: "edge"});
    assert.equal(result.status, 200);
    result = await host.call("POST", "/v1/profiles/browser_profile_1/tabs", {url: "https://example.com"});
    assert.equal(result.status, 201);
    const tabID = result.body.tab.id;

    const first = await host.raw("GET", `/v1/profiles/browser_profile_1/tabs/${tabID}/frame`);
    assert.equal(first.status, 200);
    assert.equal(first.headers.get("content-type"), "image/jpeg");
    assert.equal(first.headers.get("cache-control"), "no-store");
    assert.equal(first.body[0], 0xff);
    assert.equal(host.screenshots(), 1);

    const second = await host.raw("GET", `/v1/profiles/browser_profile_1/tabs/${tabID}/frame`);
    assert.equal(second.status, 200);
    assert.equal(host.screenshots(), 1, "frames inside the throttle window must be reused");
    assert.equal(second.headers.get("x-aha2-frame-cached"), "1");

    const unauthorized = await fetch(`${host.base}/v1/profiles/browser_profile_1/tabs/${tabID}/frame`);
    assert.equal(unauthorized.status, 401);
  } finally {
    await host.close();
  }
});

test("frame capture failure is a declared 502 without the profile root", async () => {
  const host = await startHost({screenshotFailure: true});
  try {
    let result = await host.call("POST", "/v1/profiles/browser_profile_1/session", {browser_kind: "edge"});
    assert.equal(result.status, 200);
    result = await host.call("POST", "/v1/profiles/browser_profile_1/tabs", {url: "https://example.com"});
    const tabID = result.body.tab.id;
    const frame = await host.raw("GET", `/v1/profiles/browser_profile_1/tabs/${tabID}/frame`);
    assert.equal(frame.status, 502);
    const payload = JSON.parse(frame.body.toString("utf8"));
    assert.equal(payload.error.code, "browser_frame_failed");
    assert.doesNotMatch(JSON.stringify(payload), /aha2-browser-host-|profile root/);
  } finally {
    await host.close();
  }
});

test("shutdown endpoint closes sessions and invokes the supervisor callback", async () => {
  const host = await startHost();
  try {
    let result = await host.call("POST", "/v1/profiles/browser_profile_1/session", {browser_kind: "edge"});
    assert.equal(result.status, 200);
    const shutdown = await host.call("POST", "/v1/shutdown");
    assert.equal(shutdown.status, 202);
    assert.equal(host.host.state.sessions.size, 0);
    await new Promise(resolve => setTimeout(resolve, 60));
    assert.equal(host.shutdownCalls(), 1);
  } finally {
    await host.close();
  }
});

test("tab activation brings the page to front", async () => {
  const host = await startHost();
  try {
    let result = await host.call("POST", "/v1/profiles/browser_profile_1/session", {browser_kind: "edge"});
    assert.equal(result.status, 200);
    result = await host.call("POST", "/v1/profiles/browser_profile_1/tabs", {url: "https://example.com"});
    const tabID = result.body.tab.id;
    result = await host.call("POST", `/v1/profiles/browser_profile_1/tabs/${tabID}/activate`, {});
    assert.equal(result.status, 200);
    assert.equal(host.page().broughtToFront, 1);
  } finally {
    await host.close();
  }
});

test("bookmarks return title, url and folder without the profile path", async () => {
  const host = await startHost();
  try {
    await host.call("POST", "/v1/profiles/browser_profile_1/session", {browser_kind: "edge"});
    const defaultDir = `${host.root}/browser_profile_1/Default`;
    await mkdir(defaultDir, {recursive: true});
    await writeFile(`${defaultDir}/Bookmarks`, JSON.stringify({
      roots: {
        bookmark_bar: {type: "folder", name: "Bar", children: [
          {type: "url", name: "Mail", url: "https://mail.example/"},
          {type: "url", name: "Unsafe", url: "javascript:alert(1)"},
          {type: "folder", name: "Work", children: [
            {type: "url", name: "Docs", url: "https://docs.example/"},
          ]},
        ]},
      },
    }));
    const result = await host.call("GET", "/v1/profiles/browser_profile_1/bookmarks");
    assert.equal(result.status, 200);
    assert.equal(result.body.bookmarks.length, 2);
    assert.deepEqual(result.body.bookmarks[0], {title: "Mail", url: "https://mail.example/", folder: "Bar"});
    assert.equal(result.body.bookmarks[1].folder, "Bar/Work");
    assert.doesNotMatch(JSON.stringify(result.body), /browser_profile_1|aha2-browser-host-/);
  } finally {
    await host.close();
  }
});

test("missing bookmark file is an empty list", async () => {
  const host = await startHost();
  try {
    await host.call("POST", "/v1/profiles/browser_profile_1/session", {browser_kind: "edge"});
    const result = await host.call("GET", "/v1/profiles/browser_profile_1/bookmarks");
    assert.equal(result.status, 200);
    assert.deepEqual(result.body.bookmarks, []);
  } finally {
    await host.close();
  }
});

test("enumerates every http(s) page without truncation", async () => {
  const urls = Array.from({length: 60}, (_, index) => `https://example.com/page-${index}`);
  const host = await startHost({initialURLs: urls});
  try {
    let result = await host.call("POST", "/v1/profiles/browser_profile_1/session", {browser_kind: "edge"});
    assert.equal(result.status, 200);
    result = await host.call("GET", "/v1/profiles/browser_profile_1/tabs");
    assert.equal(result.status, 200);
    assert.equal(result.body.tabs.length, 60);
    assert.ok(result.body.tabs.some(tab => tab.url === "https://example.com/page-59"));
    assert.equal(result.body.tabs[0].active, true);
  } finally {
    await host.close();
  }
});

async function writeBookmarkFile(host, profile, title) {
  const dir = `${host.root}/${profile}/Default`;
  await mkdir(dir, {recursive: true});
  await writeFile(`${dir}/Bookmarks`, JSON.stringify({
    roots: {bookmark_bar: {type: "folder", name: "Bar", children: [
      {type: "url", name: title, url: "https://secret.example/"},
    ]}},
  }));
}

test("rejects a Bookmarks file symlink to another profile", async () => {
  const host = await startHost();
  try {
    await host.call("POST", "/v1/profiles/browser_profile_1/session", {browser_kind: "edge"});
    await writeBookmarkFile(host, "browser_profile_2", "Secret B");
    await mkdir(`${host.root}/browser_profile_1/Default`, {recursive: true});
    await symlink(`${host.root}/browser_profile_2/Default/Bookmarks`, `${host.root}/browser_profile_1/Default/Bookmarks`);
    const result = await host.call("GET", "/v1/profiles/browser_profile_1/bookmarks");
    assert.notEqual(result.status, 200);
    assert.doesNotMatch(JSON.stringify(result.body), /Secret B|secret\.example/);
  } finally {
    await host.close();
  }
});

test("rejects a Default directory symlink to another profile", async () => {
  const host = await startHost();
  try {
    await host.call("POST", "/v1/profiles/browser_profile_1/session", {browser_kind: "edge"});
    await writeBookmarkFile(host, "browser_profile_2", "Secret B");
    await mkdir(`${host.root}/browser_profile_1`, {recursive: true});
    await symlink(`${host.root}/browser_profile_2/Default`, `${host.root}/browser_profile_1/Default`);
    const result = await host.call("GET", "/v1/profiles/browser_profile_1/bookmarks");
    assert.notEqual(result.status, 200);
    assert.doesNotMatch(JSON.stringify(result.body), /Secret B|secret\.example/);
  } finally {
    await host.close();
  }
});

test("rejects a relative .. Bookmarks symlink", async () => {
  const host = await startHost();
  try {
    await host.call("POST", "/v1/profiles/browser_profile_1/session", {browser_kind: "edge"});
    await writeBookmarkFile(host, "browser_profile_2", "Secret B");
    await mkdir(`${host.root}/browser_profile_1/Default`, {recursive: true});
    await symlink("../../browser_profile_2/Default/Bookmarks", `${host.root}/browser_profile_1/Default/Bookmarks`);
    const result = await host.call("GET", "/v1/profiles/browser_profile_1/bookmarks");
    assert.notEqual(result.status, 200);
    assert.doesNotMatch(JSON.stringify(result.body), /Secret B|secret\.example/);
  } finally {
    await host.close();
  }
});

test("malformed operation deadlines fail closed without opening a page", async () => {
  const host = await startHost();
  try {
    let result = await host.call("POST", "/v1/profiles/browser_profile_1/session", {browser_kind: "edge"});
    const sessionID = result.body.session.id;
    await host.call("POST", "/v1/profiles/browser_profile_1/fence", {
      session_id: sessionID, expected_fence: "", fence: "grant-1",
    });
    const now = Date.now();
    const bad = [
      "grant-1",
      "|123",
      "grant-1|",
      "grant-1|1.5",
      "grant-1| 123",
      "grant-1|NaN",
      "grant-1|100|200",
      "grant-1|Infinity",
      "grant-1|-1",
      "grant-1|0",
      "grant-1|99999999999999999999",
      `grant-1|${now + 6 * 60 * 1000}`,
      "grant-1@NaN",
      "grant-1@Infinity",
      "grant-1@-1",
      "grant-1@1@2",
    ];
    for (const fence of bad) {
      result = await host.call("POST", "/v1/profiles/browser_profile_1/tabs", {
        url: `https://example.com/late-${encodeURIComponent(fence)}`, fence,
      });
      assert.ok(result.status >= 400 && result.status < 500, `${fence} returned ${result.status}`);
      assert.equal(result.body.error.code, "browser_fence_invalid", fence);
    }
    result = await host.call("POST", "/v1/profiles/browser_profile_1/tabs", {
      url: "https://example.com/past", fence: `grant-1|${now - 1}`,
    });
    assert.equal(result.status, 409);
    assert.equal(result.body.error.code, "browser_fence_expired");
    const after = await host.call("GET", "/v1/profiles/browser_profile_1/tabs");
    assert.ok(!after.body.tabs.some(tab => tab.url.includes("/late-") || tab.url.endsWith("/past")));

    result = await host.call("POST", "/v1/profiles/browser_profile_1/tabs", {
      url: "https://example.com/valid", fence: `grant-1|${now + 60_000}`,
    });
    assert.equal(result.status, 201);
  } finally {
    await host.close();
  }
});

test("stable lease identity is separate from the per-operation deadline", async () => {
  const host = await startHost();
  try {
    let result = await host.call("POST", "/v1/profiles/browser_profile_1/session", {browser_kind: "edge"});
    const sessionID = result.body.session.id;
    await host.call("POST", "/v1/profiles/browser_profile_1/fence", {
      session_id: sessionID, expected_fence: "", fence: "grant-stable",
    });
    result = await host.call("POST", "/v1/profiles/browser_profile_1/tabs", {
      url: "https://example.com/stable", fence: `grant-stable|${Date.now() + 60_000}`,
    });
    assert.equal(result.status, 201);
    const firstID = result.body.tab.id;
    result = await host.call("POST", `/v1/profiles/browser_profile_1/tabs/${firstID}/navigate`, {
      url: "https://example.com/next", fence: `grant-stable|${Date.now() + 60_000}`,
    });
    assert.equal(result.status, 200);
    result = await host.call("POST", "/v1/profiles/browser_profile_1/tabs", {
      url: "https://example.com/expired", fence: `grant-stable|${Date.now() - 1}`,
    });
    assert.equal(result.status, 409);
    assert.equal(result.body.error.code, "browser_fence_expired");
    result = await host.call("POST", "/v1/profiles/browser_profile_1/tabs", {
      url: "https://example.com/stale", fence: `other-grant|${Date.now() + 60_000}`,
    });
    assert.equal(result.status, 409);
    assert.equal(result.body.error.code, "browser_fence_stale");
  } finally {
    await host.close();
  }
});

test("mutating calls require the current lease fence", async () => {
  const host = await startHost();
  try {
    let result = await host.call("POST", "/v1/profiles/browser_profile_1/session", {browser_kind: "edge"});
    assert.equal(result.status, 200);
    result = await host.call("POST", "/v1/profiles/browser_profile_1/tabs", {url: "https://example.com"});
    const tabID = result.body.tab.id;
    const fence = futureFence("grant-1");
    result = await host.call("POST", "/v1/profiles/browser_profile_1/fence", {
      session_id: host.host.state.sessions.get("browser_profile_1").id,
      expected_fence: "",
      fence,
    });
    assert.equal(result.status, 200);
    const stale = futureFence("old");
    result = await host.call("DELETE", `/v1/profiles/browser_profile_1/tabs/${tabID}?fence=${encodeURIComponent(stale)}`);
    assert.equal(result.status, 409);
    assert.equal(result.body.error.code, "browser_fence_stale");
    result = await host.call("DELETE", `/v1/profiles/browser_profile_1/tabs/${tabID}?fence=${encodeURIComponent(fence)}`);
    assert.equal(result.status, 200);
  } finally {
    await host.close();
  }
});

test("fence switch invalidates a paused old mutation before new holder reads", async () => {
  let pause = false;
  let enteredResolve;
  let releaseMutation;
  const host = await startHost({
    beforeMutation: async () => {
      if (!pause) return;
      pause = false;
      enteredResolve();
      await new Promise(resolve => { releaseMutation = resolve; });
    },
  });
  try {
    let result = await host.call("POST", "/v1/profiles/browser_profile_1/session", {browser_kind: "edge"});
    const sessionID = result.body.session.id;
    result = await host.call("POST", "/v1/profiles/browser_profile_1/tabs", {url: "https://example.com"});
    const tabID = result.body.tab.id;
    const fence1 = futureFence("grant-1");
    const fence2 = futureFence("grant-2");
    result = await host.call("POST", "/v1/profiles/browser_profile_1/fence", {
      session_id: sessionID, expected_fence: "", fence: fence1,
    });
    assert.equal(result.status, 200);

    let entered = new Promise(resolve => { enteredResolve = resolve; });
    pause = true;
    const closePromise = host.call("DELETE", `/v1/profiles/browser_profile_1/tabs/${tabID}?fence=${encodeURIComponent(fence1)}`);
    await entered;
    // New holder takes the fence while the old close is paused. The switch
    // waits for the old operation, but reader calls remain available.
    const switchPromise = host.call("POST", "/v1/profiles/browser_profile_1/fence", {
      session_id: sessionID, expected_fence: fence1, fence: fence2,
    });
    const tabs = await host.call("GET", "/v1/profiles/browser_profile_1/tabs");
    assert.equal(tabs.status, 200);
    assert.ok(tabs.body.tabs.some(tab => tab.id === tabID));
    releaseMutation();
    const closed = await closePromise;
    const switched = await switchPromise;
    assert.equal(closed.status, 409);
    assert.equal(closed.body.error.code, "browser_fence_stale");
    assert.equal(switched.status, 200);
    const after = await host.call("GET", "/v1/profiles/browser_profile_1/tabs");
    const restored = after.body.tabs.find(tab => tab.id === tabID);
    assert.ok(restored, "stale close must not remove the page");
    assert.equal(restored.url, "https://example.com/", "the restored page must keep its URL");
    const session = await host.call("GET", "/v1/profiles/browser_profile_1/session");
    assert.equal(session.body.session.fence, fence2);
  } finally {
    await host.close();
  }
});

test("fence switch times out without changing the old fence", async () => {
  let pause = false;
  let enteredResolve;
  let releaseMutation;
  const host = await startHost({
    fenceWaitTimeoutMs: 50,
    beforeMutation: async () => {
      if (!pause) return;
      pause = false;
      enteredResolve();
      await new Promise(resolve => { releaseMutation = resolve; });
    },
  });
  try {
    let result = await host.call("POST", "/v1/profiles/browser_profile_1/session", {browser_kind: "edge"});
    const sessionID = result.body.session.id;
    result = await host.call("POST", "/v1/profiles/browser_profile_1/tabs", {url: "https://example.com"});
    const tabID = result.body.tab.id;
    const fence1 = futureFence("grant-1");
    const fence2 = futureFence("grant-2");
    await host.call("POST", "/v1/profiles/browser_profile_1/fence", {
      session_id: sessionID, expected_fence: "", fence: fence1,
    });
    const entered = new Promise(resolve => { enteredResolve = resolve; });
    pause = true;
    const closePromise = host.call("DELETE", `/v1/profiles/browser_profile_1/tabs/${tabID}?fence=${encodeURIComponent(fence1)}`);
    await entered;
    const switched = await host.call("POST", "/v1/profiles/browser_profile_1/fence", {
      session_id: sessionID, expected_fence: fence1, fence: fence2,
    });
    assert.equal(switched.status, 409);
    assert.equal(switched.body.error.code, "browser_fence_timeout");
    const session = await host.call("GET", "/v1/profiles/browser_profile_1/session");
    assert.equal(session.body.session.fence, fence1);
    releaseMutation();
    assert.equal((await closePromise).status, 200);
  } finally {
    await host.close();
  }
});

test("lease hold lets a paused mutation finish after the original expiry, then expires", async () => {
  let pause = false;
  let enteredResolve;
  let releaseMutation;
  const host = await startHost({
    beforeMutation: async () => {
      if (!pause) return;
      pause = false;
      enteredResolve();
      await new Promise(resolve => { releaseMutation = resolve; });
    },
  });
  try {
    let result = await host.call("POST", "/v1/profiles/browser_profile_1/session", {browser_kind: "edge"});
    const sessionID = result.body.session.id;
    result = await host.call("POST", "/v1/profiles/browser_profile_1/tabs", {url: "https://example.com"});
    const tabID = result.body.tab.id;
    const expiring = `grant-1@${Date.now() + 250}`;
    result = await host.call("POST", "/v1/profiles/browser_profile_1/fence", {
      session_id: sessionID, expected_fence: "", fence: expiring,
    });
    assert.equal(result.status, 200);
    const entered = new Promise(resolve => { enteredResolve = resolve; });
    pause = true;
    const closePromise = host.call("DELETE", `/v1/profiles/browser_profile_1/tabs/${tabID}?fence=${encodeURIComponent(expiring)}`);
    await entered;
    await new Promise(resolve => setTimeout(resolve, 300));
    releaseMutation();
    const closed = await closePromise;
    assert.equal(closed.status, 200, "an operation admitted while the lease was live may finish");
    const after = await host.call("GET", "/v1/profiles/browser_profile_1/tabs");
    assert.ok(!after.body.tabs.some(tab => tab.id === tabID));
    const late = await host.call("POST", "/v1/profiles/browser_profile_1/tabs", {
      url: "https://example.com/late", fence: expiring,
    });
    assert.equal(late.status, 409);
    assert.equal(late.body.error.code, "browser_fence_expired", "the hold must end with the operation");
  } finally {
    await host.close();
  }
});

test("a fence switch waits for an admitted page.close and closes exactly once", async () => {
  let enteredResolve;
  let releaseClose;
  let pause = false;
  const host = await startHost({
    pageClose: async () => {
      if (!pause) return;
      pause = false;
      enteredResolve();
      await new Promise(resolve => { releaseClose = resolve; });
    },
  });
  try {
    let result = await host.call("POST", "/v1/profiles/browser_profile_1/session", {browser_kind: "edge"});
    const sessionID = result.body.session.id;
    result = await host.call("POST", "/v1/profiles/browser_profile_1/tabs", {url: "https://example.com"});
    const tabID = result.body.tab.id;
    const fence = `grant-1@${Date.now() + 60_000}`;
    await host.call("POST", "/v1/profiles/browser_profile_1/fence", {
      session_id: sessionID, expected_fence: "", fence,
    });
    const entered = new Promise(resolve => { enteredResolve = resolve; });
    pause = true;
    const closePromise = host.call("DELETE", `/v1/profiles/browser_profile_1/tabs/${tabID}?fence=${encodeURIComponent(fence)}`);
    await entered;
    const switchPromise = host.call("POST", "/v1/profiles/browser_profile_1/fence", {
      session_id: sessionID, expected_fence: fence, fence: `grant-2@${Date.now() + 60_000}`,
    });
    const sessionDuring = await host.call("GET", "/v1/profiles/browser_profile_1/session");
    assert.match(sessionDuring.body.session.fence, /^grant-2@/, "the new fence is visible before the writer takes over");
    releaseClose();
    const closed = await closePromise;
    const switched = await switchPromise;
    assert.equal(closed.status, 200, "the admitted close must not be invalidated by the switch");
    assert.equal(switched.status, 200);
    assert.equal(host.page().closed, 1);
    const after = await host.call("GET", "/v1/profiles/browser_profile_1/tabs");
    assert.ok(!after.body.tabs.some(tab => tab.id === tabID));
  } finally {
    await host.close();
  }
});

test("lease hold keeps page.close valid past the original expiry", async () => {
  let enteredResolve;
  let releaseClose;
  let pause = false;
  const host = await startHost({
    pageClose: async () => {
      if (!pause) return;
      pause = false;
      enteredResolve();
      await new Promise(resolve => { releaseClose = resolve; });
    },
  });
  try {
    let result = await host.call("POST", "/v1/profiles/browser_profile_1/session", {browser_kind: "edge"});
    const sessionID = result.body.session.id;
    result = await host.call("POST", "/v1/profiles/browser_profile_1/tabs", {url: "https://example.com"});
    const tabID = result.body.tab.id;
    const expiring = `grant-1@${Date.now() + 250}`;
    await host.call("POST", "/v1/profiles/browser_profile_1/fence", {
      session_id: sessionID, expected_fence: "", fence: expiring,
    });
    const entered = new Promise(resolve => { enteredResolve = resolve; });
    pause = true;
    const closePromise = host.call("DELETE", `/v1/profiles/browser_profile_1/tabs/${tabID}?fence=${encodeURIComponent(expiring)}`);
    await entered;
    await new Promise(resolve => setTimeout(resolve, 300));
    releaseClose();
    const closed = await closePromise;
    assert.equal(closed.status, 200);
    assert.equal(host.page().closed, 1);
    const after = await host.call("GET", "/v1/profiles/browser_profile_1/tabs");
    assert.ok(!after.body.tabs.some(tab => tab.id === tabID));
    const late = await host.call("POST", "/v1/profiles/browser_profile_1/tabs", {
      url: "https://example.com/late", fence: expiring,
    });
    assert.equal(late.status, 409);
    assert.equal(late.body.error.code, "browser_fence_expired");
  } finally {
    await host.close();
  }
});

test("lease hold keeps a slow tab open valid past the original expiry", async () => {
  let enteredResolve;
  let releaseGoto;
  let pause = false;
  const host = await startHost({
    tabGoto: async () => {
      if (!pause) return;
      pause = false;
      enteredResolve();
      await new Promise(resolve => { releaseGoto = resolve; });
    },
  });
  try {
    let result = await host.call("POST", "/v1/profiles/browser_profile_1/session", {browser_kind: "edge"});
    const sessionID = result.body.session.id;
    const expiring = `grant-1@${Date.now() + 250}`;
    await host.call("POST", "/v1/profiles/browser_profile_1/fence", {
      session_id: sessionID, expected_fence: "", fence: expiring,
    });
    const entered = new Promise(resolve => { enteredResolve = resolve; });
    pause = true;
    const openedPromise = host.call("POST", "/v1/profiles/browser_profile_1/tabs", {
      url: "https://example.com/late", fence: expiring,
    });
    await entered;
    await new Promise(resolve => setTimeout(resolve, 300));
    releaseGoto();
    const opened = await openedPromise;
    assert.equal(opened.status, 201);
    assert.equal(host.page().closed, 0);
    const after = await host.call("GET", "/v1/profiles/browser_profile_1/tabs");
    assert.ok(after.body.tabs.some(tab => tab.url === "https://example.com/late"));
    assert.equal(new Set(after.body.tabs.map(tab => tab.id)).size, after.body.tabs.length,
      "one real page must map to exactly one host tab id");
    const late = await host.call("POST", "/v1/profiles/browser_profile_1/tabs", {
      url: "https://example.com/later", fence: expiring,
    });
    assert.equal(late.status, 409);
    assert.equal(late.body.error.code, "browser_fence_expired");
  } finally {
    await host.close();
  }
});

test("lease hold keeps a slow navigation valid past the original expiry", async () => {
  let enteredResolve;
  let releaseGoto;
  let pause = false;
  const host = await startHost({
    tabGoto: async () => {
      if (!pause) return;
      pause = false;
      enteredResolve();
      await new Promise(resolve => { releaseGoto = resolve; });
    },
  });
  try {
    let result = await host.call("POST", "/v1/profiles/browser_profile_1/session", {browser_kind: "edge"});
    const sessionID = result.body.session.id;
    result = await host.call("POST", "/v1/profiles/browser_profile_1/tabs", {url: "https://example.com/original"});
    const tabID = result.body.tab.id;
    const expiring = `grant-1@${Date.now() + 250}`;
    await host.call("POST", "/v1/profiles/browser_profile_1/fence", {
      session_id: sessionID, expected_fence: "", fence: expiring,
    });
    const entered = new Promise(resolve => { enteredResolve = resolve; });
    pause = true;
    const navigatePromise = host.call("POST", `/v1/profiles/browser_profile_1/tabs/${tabID}/navigate`, {
      url: "https://example.com/late", fence: expiring,
    });
    await entered;
    await new Promise(resolve => setTimeout(resolve, 300));
    releaseGoto();
    const navigated = await navigatePromise;
    assert.equal(navigated.status, 200);
    const after = await host.call("GET", "/v1/profiles/browser_profile_1/tabs");
    const tab = after.body.tabs.find(candidate => candidate.id === tabID);
    assert.ok(tab);
    assert.equal(tab.url, "https://example.com/late");
  } finally {
    await host.close();
  }
});

test("a mutation is rejected before page side effects when the lease hold cannot be established", async () => {
  const host = await startHost({failLeaseHold: true});
  try {
    let result = await host.call("POST", "/v1/profiles/browser_profile_1/session", {browser_kind: "edge"});
    const sessionID = result.body.session.id;
    result = await host.call("POST", "/v1/profiles/browser_profile_1/tabs", {url: "https://example.com"});
    const tabID = result.body.tab.id;
    const fence = `grant-1@${Date.now() + 60_000}`;
    await host.call("POST", "/v1/profiles/browser_profile_1/fence", {
      session_id: sessionID, expected_fence: "", fence,
    });
    result = await host.call("DELETE", `/v1/profiles/browser_profile_1/tabs/${tabID}?fence=${encodeURIComponent(fence)}`);
    assert.equal(result.status, 409);
    assert.equal(result.body.error.code, "browser_lease_hold_failed");
    assert.equal(host.page().closed, 0);
    const after = await host.call("GET", "/v1/profiles/browser_profile_1/tabs");
    assert.ok(after.body.tabs.some(tab => tab.id === tabID));
  } finally {
    await host.close();
  }
});
test("an already-expired fence is rejected before opening a page", async () => {
  const host = await startHost();
  try {
    let result = await host.call("POST", "/v1/profiles/browser_profile_1/session", {browser_kind: "edge"});
    const sessionID = result.body.session.id;
    const expired = `grant-1@${Date.now() - 1000}`;
    await host.call("POST", "/v1/profiles/browser_profile_1/fence", {
      session_id: sessionID, expected_fence: "", fence: expired,
    });
    result = await host.call("POST", "/v1/profiles/browser_profile_1/tabs", {
      url: "https://example.com/late", fence: expired,
    });
    assert.equal(result.status, 409);
    assert.equal(result.body.error.code, "browser_fence_expired");
    const after = await host.call("GET", "/v1/profiles/browser_profile_1/tabs");
    assert.ok(!after.body.tabs.some(tab => tab.url === "https://example.com/late"));
  } finally {
    await host.close();
  }
});

test("rejects a parent directory swapped after the bookmark path check", async () => {
  let swapped = false;
  const host = await startHost({
    beforeBookmarkOpen: async ({directory}) => {
      if (swapped) return;
      swapped = true;
      await rm(`${directory}/Default`, {recursive: true, force: true});
      await symlink(`${host.root}/browser_profile_2/Default`, `${directory}/Default`);
    },
  });
  try {
    await host.call("POST", "/v1/profiles/browser_profile_1/session", {browser_kind: "edge"});
    await writeBookmarkFile(host, "browser_profile_1", "A bookmarks");
    await writeBookmarkFile(host, "browser_profile_2", "Secret B");
    const result = await host.call("GET", "/v1/profiles/browser_profile_1/bookmarks");
    assert.notEqual(result.status, 200);
    assert.doesNotMatch(JSON.stringify(result.body), /Secret B|secret\.example/);
    assert.equal(swapped, true);
  } finally {
    await host.close();
  }
});
