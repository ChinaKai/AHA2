import test from "node:test";
import assert from "node:assert/strict";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const helpers = await import(pathToFileURL(resolve(import.meta.dirname, "..", "dist", "ui_helpers.js")));

test("HTTP 400 is an ambiguous rejection, not a missing protocol endpoint", () => {
  // An unknown model or bad parameter also returns 400. The row must not
  // claim the gateway lacks the protocol endpoint.
  const failures = helpers.protocolProbeFailures(
    {responses: "rejected", chat_completions: "supported"},
    {responses: "https://gateway.example/v1/responses: HTTP 400"},
  );
  assert.equal(failures.length, 1);
  assert.equal(failures[0].message, "Responses 请求被拒绝（HTTP 400）");
  assert.equal(failures[0].tone, "bad");
  assert.match(failures[0].hint, /可能不支持该协议，也可能是模型、参数或鉴权配置错误/);
  assert.doesNotMatch(failures[0].message, /不可用|传输错误/);
  assert.doesNotMatch(failures[0].hint, /没有提供这个协议端点/);

  const html = helpers.renderProtocolProbeStatus(
    {responses: "rejected", chat_completions: "supported"},
    {responses: "https://gateway.example/v1/responses: HTTP 400"},
  );
  assert.match(html, /Responses 请求被拒绝（HTTP 400）/);
  assert.doesNotMatch(html, /没有提供这个协议端点/);
  assert.doesNotMatch(html, /传输错误/);
  assert.match(html, /查看详情/);
  assert.match(html, /https:\/\/gateway\.example\/v1\/responses: HTTP 400/);
  // A protocol the gateway does support stays out of the failure block.
  assert.doesNotMatch(html, /Chat 不可用/);
});

test("HTTP 404/405/501 read as protocol unavailable, not transport failure", () => {
  const failures = helpers.protocolProbeFailures(
    {responses: "unsupported", anthropic_messages: "unsupported"},
    {
      responses: "https://gateway.example/v1/responses: HTTP 404",
      anthropic_messages: "https://gateway.example/anthropic/v1/messages: HTTP 405",
    },
  );
  assert.deepEqual(failures.map(failure => failure.message), [
    "Responses 不可用（HTTP 404）",
    "Messages 不可用（HTTP 405）",
  ]);
  assert.equal(failures[0].tone, "muted");
  assert.match(failures[0].hint, /不是网络故障/);
  for (const failure of failures) {
    assert.doesNotMatch(failure.message, /传输错误|请求被拒绝/);
    assert.doesNotMatch(failure.hint, /检查网络连接/);
  }
});

test("HTTP 422 inconclusive results stay unconfirmed and keep the HTTP status", () => {
  const failures = helpers.protocolProbeFailures(
    {responses: "inconclusive"},
    {responses: "https://gateway.example/v1/responses: HTTP 422"},
  );
  assert.equal(failures[0].message, "Responses 未确认（HTTP 422）");
  assert.match(failures[0].hint, /网关拒绝了这次探测/);
  const html = helpers.renderProtocolProbeStatus(
    {responses: "inconclusive"},
    {responses: "https://gateway.example/v1/responses: HTTP 422"},
  );
  assert.match(html, /Responses 未确认（HTTP 422）/);
  assert.match(html, /HTTP 422/);
  assert.doesNotMatch(html, /传输错误|不可用|鉴权失败/);
});

test("timeouts and connection failures keep the transport-error wording", () => {
  const failures = helpers.protocolProbeFailures(
    {responses: "unavailable"},
    {responses: 'Post "https://gateway.example/v1/responses": context deadline exceeded'},
  );
  assert.equal(failures.length, 1);
  assert.equal(failures[0].message, "Responses 传输错误");
  assert.equal(failures[0].tone, "bad");
  assert.match(failures[0].hint, /检查网络连接、代理或 Base URL/);
  const html = helpers.renderProtocolProbeStatus(
    {responses: "unavailable"},
    {responses: 'Post "https://gateway.example/v1/responses": context deadline exceeded'},
  );
  assert.match(html, /Responses 传输错误/);
  assert.match(html, /检查网络连接/);
});

test("HTTP 5xx probe results read as a gateway service error, not a network fault", () => {
  const failures = helpers.protocolProbeFailures(
    {responses: "unavailable"},
    {responses: "https://gateway.example/v1/responses: HTTP 502"},
  );
  assert.equal(failures[0].message, "Responses 服务异常（HTTP 502）");
  assert.match(failures[0].hint, /服务端错误/);
  assert.doesNotMatch(failures[0].message, /传输错误/);
});

test("HTTP 401/403 probe results read as auth failures", () => {
  const failures = helpers.protocolProbeFailures(
    {anthropic_messages: "unauthorized"},
    {anthropic_messages: "https://gateway.example/anthropic/v1/messages: HTTP 401"},
  );
  assert.equal(failures[0].message, "Messages 鉴权失败（HTTP 401）");
  assert.match(failures[0].hint, /API Key 或鉴权方式/);
  assert.doesNotMatch(failures[0].message, /传输错误|不可用/);
});

test("supported and rate-limited protocols render no failure text", () => {
  const html = helpers.renderProtocolProbeStatus(
    {responses: "supported", chat_completions: "rate_limited", anthropic_messages: "supported"},
    {},
  );
  assert.equal(html, "");
  assert.deepEqual(helpers.protocolProbeFailures({responses: "supported"}, {}), []);
  // 429 is a usable protocol: it still gets the normal protocol badge.
  assert.match(helpers.protoBadges({id: "model", capabilities: {chat_completions: "rate_limited"}}), /<span class="proto codex">Chat<\/span>/);
});

test("badge row only says 不支持 when every failure is a confirmed missing endpoint", () => {
  const row = (caps, errors = {}) =>
    helpers.protoBadges({id: "model", capabilities: caps}) + helpers.renderProtocolProbeStatus(caps, errors);
  const unsupportedBadge = /<span class="proto none">不支持<\/span>/;
  const unconfirmedBadge = /<span class="proto none">未确认<\/span>/;
  const rejectedErrors = {
    responses: "https://gateway.example/v1/responses: HTTP 400",
    chat_completions: "https://gateway.example/v1/chat/completions: HTTP 400",
    anthropic_messages: "https://gateway.example/v1/messages: HTTP 400",
  };

  const allMissing = row({responses: "unsupported", chat_completions: "unsupported", anthropic_messages: "unsupported"});
  assert.match(allMissing, unsupportedBadge);
  assert.doesNotMatch(allMissing, unconfirmedBadge);

  const allRejected = row({responses: "rejected", chat_completions: "rejected", anthropic_messages: "rejected"}, rejectedErrors);
  assert.match(allRejected, unconfirmedBadge);
  assert.doesNotMatch(allRejected, unsupportedBadge);

  const mixed = row({responses: "unsupported", chat_completions: "rejected", anthropic_messages: "unsupported"}, rejectedErrors);
  assert.match(mixed, unconfirmedBadge);
  assert.doesNotMatch(mixed, unsupportedBadge);

  for (const status of ["unauthorized", "unavailable", "inconclusive"]) {
    const state = row({responses: status, chat_completions: status, anthropic_messages: status});
    assert.match(state, unconfirmedBadge, `${status} must stay unconfirmed`);
    assert.doesNotMatch(state, unsupportedBadge, `${status} must not say 不支持`);
  }

  // A supported protocol still gets its normal badge; the failure block
  // describes the rejected protocol without claiming the model is unsupported.
  const partial = row({responses: "rejected", chat_completions: "supported", anthropic_messages: "unsupported"}, rejectedErrors);
  assert.match(partial, /<span class="proto codex">Chat<\/span>/);
  assert.doesNotMatch(partial, unsupportedBadge);
  assert.doesNotMatch(partial, unconfirmedBadge);

  assert.match(row({}), unconfirmedBadge);
});
