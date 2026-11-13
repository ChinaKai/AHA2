import test from "node:test";
import assert from "node:assert/strict";
import {chunkErrorHTML, chunkLoadingHTML, createLazyPage, retryLazyPage} from "../build/lazy_page.js";

test("a route chunk loads once and repeat entry reuses the module", async () => {
  let calls = 0;
  let resolve;
  const pending = new Promise(done => { resolve = done; });
  const page = createLazyPage("test-repeat", () => {
    calls++;
    return pending;
  });

  const first = page.load();
  const second = page.load();
  assert.equal(calls, 1, "a pending chunk must not start a second request");
  assert.equal(page.status(), "loading");
  assert.equal(page.loaded(), null);

  resolve({title: "knowledge"});
  assert.deepEqual(await first, {title: "knowledge"});
  assert.equal(await second, await first);
  assert.equal(page.status(), "ready");
  assert.deepEqual(page.loaded(), {title: "knowledge"});

  const third = page.load();
  assert.equal(await third, page.loaded());
  assert.equal(calls, 1, "entering the page again must use the cached chunk");
});

test("a failed chunk reports an error, does not retry from render, and retries on demand", async () => {
  let calls = 0;
  const page = createLazyPage("test-failure", async () => {
    calls++;
    if (calls === 1) throw new Error("network down");
    return {ok: true};
  });

  assert.equal(await page.load(), null);
  assert.equal(page.status(), "failed");
  assert.equal(page.error(), "network down");
  assert.equal(await page.load(), null, "a render-driven load must not loop on failure");
  assert.equal(calls, 1);

  assert.deepEqual(await page.retry(), {ok: true});
  assert.equal(page.status(), "ready");
  assert.equal(page.error(), "");
  assert.equal(calls, 2);
});

test("only a failed chunk can be retried by key", async () => {
  const page = createLazyPage("test-retry-key", async () => ({ready: true}));
  await page.load();
  assert.equal(retryLazyPage("test-retry-key"), null, "a loaded chunk is never refetched");

  let attempts = 0;
  const failing = createLazyPage("test-retry-failed", async () => {
    attempts++;
    if (attempts === 1) throw new Error("boom");
    return {attempts};
  });
  await failing.load();
  assert.ok(retryLazyPage("test-retry-failed") instanceof Promise);
  await new Promise(resolve => setTimeout(resolve, 0));
  assert.equal(failing.status(), "ready");
  assert.equal(attempts, 2);
  assert.equal(retryLazyPage("missing-page"), null);
});

test("chunk loading and failure markup is explicit and escaped", () => {
  assert.match(chunkLoadingHTML("正在加载页面…"), /role="status"[^>]*aria-busy="true"/);
  assert.match(chunkLoadingHTML("正在加载页面…"), /boot-spinner/);

  const html = chunkErrorHTML("mcp", '页面 <失败>', 'bad "chunk" & network', "重试");
  assert.match(html, /role="alert"/);
  assert.match(html, /data-chunk-retry="mcp"/);
  assert.match(html, /页面 &lt;失败&gt;/);
  assert.match(html, /bad &quot;chunk&quot; &amp; network/);
  assert.match(html, />重试</);
  assert.doesNotMatch(chunkErrorHTML("mcp", "页面加载失败", "", "重试"), /<span><\/span>/);
});