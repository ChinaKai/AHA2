// Model health badges answer "can this account call the model right now".
// These tests pin the wording so a 429 cannot read as a missing protocol and a
// quota failure cannot read as "可用".
import test from "node:test";
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "..");
const helpers = await import(pathToFileURL(resolve(root, "build", "ui_helpers.js")));

test("each health status has a distinct Chinese label and tone", () => {
  const expectations = [
    ["ok", "可用", "good"],
    ["rate_limited", "限流", "warn"],
    ["unauthorized", "鉴权失败", "bad"],
    ["quota_exhausted", "余额不足", "bad"],
    ["model_not_found", "模型不存在", "bad"],
    ["rejected", "请求被拒绝", "warn"],
    ["unavailable", "不可用", "bad"],
    ["inconclusive", "未确认", "muted"],
  ];
  for (const [status, label, tone] of expectations) {
    const display = helpers.modelHealthDisplay({status});
    assert.equal(display.label, label, `${status} label`);
    assert.equal(display.tone, tone, `${status} tone`);
    assert.ok(display.hint.length > 0, `${status} hint`);
  }
  // 429 is throttling, not an absent protocol or an unavailable account.
  const limited = helpers.modelHealthDisplay({status: "rate_limited"});
  assert.doesNotMatch(limited.label, /不可用|不支持/);
  // A 400 rejection must not be reported as a balance problem.
  const rejected = helpers.modelHealthDisplay({status: "rejected"});
  assert.doesNotMatch(rejected.label, /余额|限流|鉴权/);
});

test("rate limited shows the Retry-After wait when present", () => {
  const withRetry = helpers.renderModelHealthBadge({status: "rate_limited", retry_after_seconds: 30});
  assert.match(withRetry, /限流 · 30 秒后重试/);
  assert.match(withRetry, /class="status warn"/);
  const withoutRetry = helpers.renderModelHealthBadge({status: "rate_limited"});
  assert.match(withoutRetry, />限流</);
  assert.doesNotMatch(withoutRetry, /秒后重试/);
});

test("quota exhaustion and ok never share wording", () => {
  const quota = helpers.renderModelHealthBadge({status: "quota_exhausted"});
  assert.match(quota, /余额不足/);
  assert.doesNotMatch(quota, />可用</);
  const ok = helpers.renderModelHealthBadge({status: "ok"});
  assert.match(ok, />可用</);
  assert.doesNotMatch(ok, /余额|限流|鉴权/);
});

test("an unknown or not-yet-checked model renders 未检测 with an enabled button", () => {
  const cell = helpers.renderModelHealthCell("model-1", undefined);
  assert.match(cell, />未检测</);
  assert.match(cell, /data-check-model="model-1"/);
  assert.match(cell, />检测</);
  assert.doesNotMatch(cell, /disabled/);
});

test("checking disables only that row's button and shows progress", () => {
  const cell = helpers.renderModelHealthCell("model-1", {status: "checking"});
  assert.match(cell, /检测中…/);
  assert.match(cell, /<button[^>]*disabled/);
  assert.doesNotMatch(cell, /<details/);
  // Another row stays clickable while this one is in flight.
  const other = helpers.renderModelHealthCell("model-2", undefined);
  assert.doesNotMatch(other, /disabled/);
});

test("the badge tooltip exposes HTTP status, endpoint and body without markup injection", () => {
  const html = helpers.renderModelHealthBadge({
    status: "rejected",
    http_status: 400,
    endpoint: "https://gateway.example/v1/responses",
    detail: '<script>alert("x")</script> invalid max_tokens',
  });
  assert.match(html, /title="/);
  assert.match(html, /HTTP 400/);
  assert.match(html, /https:\/\/gateway\.example\/v1\/responses/);
  assert.doesNotMatch(html, /<script>/);
  assert.match(html, /&lt;script&gt;/);
  // A healthy or in-flight row keeps the plain hint; nothing extra to show.
  const ok = helpers.renderModelHealthBadge({status: "ok"});
  assert.doesNotMatch(ok, /HTTP|https?:/);
  const checking = helpers.renderModelHealthBadge({status: "checking"});
  assert.match(checking, /检测中/);
  assert.doesNotMatch(checking, /HTTP|https?:/);
});

test("editing a model or provider clears stale health results", async () => {
  const app = await readFile(resolve(root, "build", "app.js"), "utf8");
  assert.match(app, /function forgetModelHealth/, "no per-model health reset exists");
  assert.match(app, /function forgetAllModelHealth/, "no provider-wide health reset exists");
  assert.match(app, /forgetModelHealth\(id\);/, "model edits/deletes do not clear the old health result");
  assert.match(app, /forgetAllModelHealth\(\);/, "provider edits do not clear old health results");
  // A response that was in flight when the result was cleared must not win.
  // That generation guard lives in the extracted state module.
  const stateModule = await readFile(resolve(root, "build", "model_health.js"), "utf8");
  assert.match(stateModule, /state\.modelHealthSeq\[modelID\] !== seq/, "an old health response can overwrite cleared state");
  assert.match(app, /if \(force\) forgetAllModelHealth\(\);/, "a forced refresh keeps stale health results");
});

test("the model page wires the per-row check without a global error path", async () => {
  const app = await readFile(resolve(root, "build", "app.js"), "utf8");
  assert.match(app, /api\.checkModelHealth\(modelID\)/, "the row action no longer calls the health endpoint");
  assert.match(app, /runModelHealthCheck\(state, id/, "the row action no longer uses the health state machine");
  assert.match(app, /data-check-model/, "the row no longer renders a check button");
  assert.match(app, /target\.closest\("\[data-check-model\]"\)/, "the delegated click handler lost the check action");
  const stateModule = await readFile(resolve(root, "build", "model_health.js"), "utf8");
  assert.match(stateModule, /state\.modelHealth\[modelID\]\?\.status === "checking"\) return;/, "a second click while checking is not blocked");
  const api = await readFile(resolve(root, "build", "api.js"), "utf8");
  assert.match(api, /\/api\/v1\/models\/\$\{encodeURIComponent\(id\)\}\/health-check/);
});
