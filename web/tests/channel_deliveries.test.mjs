import test from "node:test";
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "..");
const channels = await import(pathToFileURL(resolve(root, "dist", "channels.js")).href);

const delivery = (id, state, extra = {}) => ({
  id, instance_id: "channel-1", stream_sequence: 1, replay_generation: 0, semantic_payload: {}, state,
  attempts: 5, created_at: "2026-11-01T02:03:04Z", ...extra,
});

test("delivery issues list only dead letters with id, time, attempts, and error", () => {
  const html = channels.renderChannelDeliveryIssues("channel-1", [
    delivery("delivery-dead", "dead_letter", {last_error_code: "rate_limited"}),
    delivery("delivery-ok", "delivered"),
    delivery("delivery-skipped", "skipped"),
    delivery("delivery-dead-2", "dead_letter", {attempts: 2}),
  ]);
  assert.match(html, /投递异常/);
  assert.match(html, /死信 2/);
  assert.match(html, /<code>delivery-dead<\/code>/);
  assert.match(html, /尝试 5 次 · 错误 rate_limited/);
  assert.match(html, /尝试 2 次 · 错误 —/);
  assert.match(html, /创建于 /);
  assert.doesNotMatch(html, /delivery-ok|delivery-skipped/);
  assert.match(html, /data-channel-delivery-action="replay" data-channel-delivery-id="delivery-dead" data-channel-instance-id="channel-1">重放</);
  assert.match(html, /data-channel-delivery-action="skip" data-channel-delivery-id="delivery-dead" data-channel-instance-id="channel-1">跳过</);
  assert.equal(channels.renderChannelDeliveryIssues("channel-1", [delivery("delivery-ok", "delivered")]), "");
  assert.equal(channels.renderChannelDeliveryIssues("channel-1", []), "");
});

test("delivery issue fields are escaped", () => {
  const html = channels.renderChannelDeliveryIssues("c\"1", [delivery("<img src=x>", "dead_letter", {last_error_code: "<script>"})]);
  assert.doesNotMatch(html, /<img|<script>/);
  assert.match(html, /&lt;img src=x&gt;/);
  assert.match(html, /data-channel-instance-id="c&quot;1"/);
});

test("confirm copy names the real-channel resend and the archive-without-send", () => {
  assert.match(channels.channelDeliveryConfirmMessages.replay, /重新发送到真实渠道/);
  assert.match(channels.channelDeliveryConfirmMessages.skip, /归档不再发送/);
});

test("active channel cards host the issues list; retired cards do not", () => {
  const instance = {id: "channel-1", plugin_id: "feishu", name: "团队飞书", status: "ready", effective_availability: "available", revision: 1, credential_configured: true, owner_bound: true, host_project_id: "p", host_workspace_id: "w"};
  const context = {models: [], accounts: [], projects: [], workspaces: [], knowledgeIndexes: [], libraries: []};
  assert.match(channels.renderChannels([], [instance], context), /data-channel-deliveries="channel-1" hidden/);
  assert.doesNotMatch(channels.renderChannels([], [{...instance, status: "retired"}], context), /data-channel-deliveries=/);
});

test("replay and skip require confirmation, disable buttons, and refresh after success", async () => {
  const source = await readFile(resolve(root, "src", "channels.ts"), "utf8");
  const binder = source.slice(source.indexOf("const bindDeliveryActions"));
  assert.ok(binder.indexOf("window.confirm(channelDeliveryConfirmMessages[action])") < binder.indexOf("api.replayChannelDelivery(deliveryID)"));
  assert.ok(binder.indexOf("item.disabled = true") < binder.indexOf("api.replayChannelDelivery(deliveryID)"));
  assert.match(binder, /api\.skipChannelDelivery\(deliveryID\)/);
  assert.match(binder, /deliveryIssueCache\.delete\(instanceID\);[\s\S]{0,240}await refresh\(\)/);
  assert.match(binder, /\.catch\(error => \{\s*buttons\.forEach\(item => \{ item\.disabled = false; \}\)/);
  assert.doesNotMatch(source, /channel_delivery_[0-9a-f]{20}/);
  // The archive export keeps reading the same deliveries endpoint.
  assert.match(source, /api\.channelHandoffs\(instanceID\), api\.channelDeliveries\(instanceID\)/);
});
