import test from "node:test";
import assert from "node:assert/strict";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const helpers = await import(pathToFileURL(resolve(import.meta.dirname, "..", "dist", "ui_helpers.js")));

test("HTTP 400/404 probe results read as protocol unavailable, not transport failure", () => {
  const failures = helpers.protocolProbeFailures(
    {responses: "unsupported", chat_completions: "supported", anthropic_messages: "unsupported"},
    {
      responses: "https://gateway.example/v1/responses: HTTP 400",
      anthropic_messages: "https://gateway.example/anthropic/v1/messages: HTTP 404",
    },
  );
  assert.deepEqual(failures.map(failure => failure.wire), ["responses", "anthropic_messages"]);
  assert.deepEqual(failures.map(failure => failure.message), [
    "Responses 不可用（HTTP 400）",
    "Messages 不可用（HTTP 404）",
  ]);
  assert.equal(failures[0].tone, "muted");
  assert.match(failures[0].hint, /不是网络故障/);
  for (const failure of failures) {
    assert.doesNotMatch(failure.message, /传输错误/);
    assert.doesNotMatch(failure.hint, /检查网络连接/);
  }

  const html = helpers.renderProtocolProbeStatus(
    {responses: "unsupported", chat_completions: "supported", anthropic_messages: "unsupported"},
    {
      responses: "https://gateway.example/v1/responses: HTTP 400",
      anthropic_messages: "https://gateway.example/anthropic/v1/messages: HTTP 404",
    },
  );
  assert.match(html, /Responses 不可用（HTTP 400）/);
  assert.match(html, /Messages 不可用（HTTP 404）/);
  assert.match(html, /不是网络故障/);
  assert.match(html, /查看详情/);
  assert.match(html, /https:\/\/gateway\.example\/v1\/responses: HTTP 400/);
  assert.doesNotMatch(html, /传输错误/);
  // A protocol the gateway does support stays out of the failure block.
  assert.doesNotMatch(html, /Chat 不可用/);
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
});
