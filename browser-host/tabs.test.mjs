import test from "node:test";
import assert from "node:assert/strict";
import {NAVIGATION_TIMEOUT_MS, navigatePage, openTabPage} from "./tabs.mjs";

function fakePage(overrides = {}) {
  const page = {
    closed: 0,
    url: () => "https://example.com/final",
    title: async () => "Example",
    goto: async () => {},
    close: async () => { page.closed++; },
    ...overrides,
  };
  return page;
}

test("a successful open returns the page and does not close it", async () => {
  let options = null;
  const page = fakePage({goto: async (url, opts) => { options = opts; }});
  const context = {newPage: async () => page};
  const tab = await openTabPage(context, "https://example.com", "tab-1");
  assert.equal(options.timeout, 30000);
  assert.equal(tab.id, "tab-1");
  assert.equal(tab.url, "https://example.com/final");
  assert.equal(tab.title, "Example");
  assert.equal(page.closed, 0);
  assert.equal(NAVIGATION_TIMEOUT_MS, 30000);
});

test("a failed navigation closes the orphan page before propagating", async () => {
  const failure = new Error("Navigation timeout of 30000 ms exceeded");
  const page = fakePage({goto: async () => { throw failure; }});
  const context = {newPage: async () => page};
  await assert.rejects(openTabPage(context, "https://slow.example", "tab-2"), failure);
  assert.equal(page.closed, 1);
});

test("close failure during cleanup still propagates the navigation error", async () => {
  const failure = new Error("net::ERR_CONNECTION_REFUSED");
  const page = fakePage({
    goto: async () => { throw failure; },
    close: async () => { throw new Error("target already closed"); },
  });
  const context = {newPage: async () => page};
  await assert.rejects(openTabPage(context, "https://down.example", "tab-3"), failure);
});

test("navigatePage reports the final URL and title", async () => {
  const page = fakePage();
  const result = await navigatePage(page, "https://example.com/next");
  assert.deepEqual(result, {url: "https://example.com/final", title: "Example"});
});
