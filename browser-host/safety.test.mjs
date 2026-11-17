import test from "node:test";
import assert from "node:assert/strict";
import {mkdtemp} from "node:fs/promises";
import {tmpdir} from "node:os";
import {join} from "node:path";
import {createHostServer} from "./server.mjs";

async function startHost({launchFailure = false, tabGoto = null, screenshotFailure = false} = {}) {
  const root = await mkdtemp(join(tmpdir(), "aha2-browser-host-"));
  let screenshots = 0;
  let shutdownCalls = 0;
  const page = {
    closed: 0,
    url: () => "https://example.com/final",
    title: async () => "Example",
    goto: tabGoto || (async () => {}),
    close: async () => { page.closed++; },
    evaluate: async () => "page text",
    on: () => {},
    screenshot: async () => {
      if (screenshotFailure) throw new Error(`screenshot failed under ${root}`);
      screenshots++;
      return Buffer.from([0xff, 0xd8, 0xff, 0x01, 0x02, 0xff, 0xd9]);
    },
  };
  const context = {
    on: () => {},
    close: async () => {},
    newPage: async () => page,
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
    root, host, base, call, raw, page, close,
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
    assert.equal(host.page.closed, 1);
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
