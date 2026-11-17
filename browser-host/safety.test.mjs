import test from "node:test";
import assert from "node:assert/strict";
import {mkdtemp} from "node:fs/promises";
import {tmpdir} from "node:os";
import {join} from "node:path";
import {createHostServer} from "./server.mjs";

async function startHost({launchFailure = false, tabGoto = null} = {}) {
  const root = await mkdtemp(join(tmpdir(), "aha2-browser-host-"));
  const page = {
    closed: 0,
    url: () => "https://example.com/final",
    title: async () => "Example",
    goto: tabGoto || (async () => {}),
    close: async () => { page.closed++; },
    evaluate: async () => "page text",
    on: () => {},
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
  return {root, host, base, call, page, close};
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
