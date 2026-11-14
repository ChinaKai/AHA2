// Pure tests for the conversation window math and its rendered markup.
// The browser test (conversation_virtual.browser.mjs) covers measuring,
// scroll anchoring and recycled-row UI state against the real app.
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";
import test from "node:test";

const root = resolve(import.meta.dirname, "..");
const {conversationWindow, renderConversationList} = await import(pathToFileURL(resolve(root, "build", "conversation_ui.js")));

const keys = count => Array.from({length: count}, (_, index) => `m-${index}`);
const heights = values => new Map(values.map((height, index) => [`m-${index}`, height]));

test("an empty conversation has no window and no spacers", () => {
  const window = conversationWindow([], {heights: new Map(), scrollTop: 0, clientHeight: 500});
  assert.deepEqual(window, {start: 0, end: 0, topHeight: 0, bottomHeight: 0, totalHeight: 0});
});

test("the window covers the viewport plus overscan and spacers cover the rest", () => {
  const window = conversationWindow(keys(10), {
    heights: heights(Array(10).fill(100)),
    scrollTop: 500,
    clientHeight: 300,
    headerHeight: 40,
    paddingTop: 10,
    overscanPx: 100,
  });
  // Content coordinate 500-50=450 to 750; grow one overscan on each side.
  assert.equal(window.start, 3);
  assert.equal(window.end, 9);
  assert.equal(window.totalHeight, 1000);
  assert.equal(window.topHeight, 300);
  assert.equal(window.bottomHeight, 100);
  // The rendered rows plus both spacers add up to the full content height.
  assert.equal(window.topHeight + (window.end - window.start) * 100 + window.bottomHeight, window.totalHeight);
});

test("unmeasured rows use the estimate while measured rows keep their height", () => {
  const window = conversationWindow(keys(6), {
    heights: new Map([["m-0", 300]]),
    scrollTop: 290,
    clientHeight: 100,
    overscanPx: 0,
    estimateHeight: 50,
  });
  // Measured 300, then five estimated 50s = total 550; viewport is inside row 0.
  assert.equal(window.totalHeight, 550);
  assert.equal(window.start, 0);
  assert.ok(window.end >= 1);
  assert.equal(window.topHeight, 0);
});

test("at bottom the newest rows render and the bottom spacer disappears", () => {
  const window = conversationWindow(keys(100), {
    heights: heights(Array(100).fill(80)),
    scrollTop: 0,
    clientHeight: 500,
    overscanPx: 100,
    atBottom: true,
  });
  assert.equal(window.end, 100);
  assert.equal(window.bottomHeight, 0);
  assert.ok(window.start > 0);
  assert.equal(window.start, 92);
});

test("the anchor row is kept rendered when the scrollbar lands in a spacer", () => {
  const window = conversationWindow(keys(200), {
    heights: heights(Array(200).fill(80)),
    scrollTop: 8000,
    clientHeight: 400,
    overscanPx: 0,
    anchorKey: "m-20",
  });
  assert.equal(window.start, 20);
  assert.equal(window.topHeight, 1600);
  assert.ok(window.end > 20);
});

test("the anchor is not allowed to explode the DOM bound", () => {
  const window = conversationWindow(keys(1000), {
    heights: heights(Array(1000).fill(80)),
    scrollTop: 40000,
    clientHeight: 400,
    overscanPx: 0,
    anchorKey: "m-0",
  });
  // Far outside the viewport, so the normal window wins.
  assert.ok(window.start > 0);
  assert.ok(window.end - window.start <= 10);
});

test("focus pinning spans the viewport and the focused row", () => {
  const window = conversationWindow(keys(100), {
    heights: heights(Array(100).fill(80)),
    scrollTop: 4000,
    clientHeight: 400,
    overscanPx: 0,
    pinnedKey: "m-99",
  });
  assert.equal(window.end, 100);
  assert.ok(window.end - window.start <= 240);
});

test("rendering a window emits inert spacers and only the selected rows", () => {
  const items = Array.from({length: 30}, (_, index) => ({
    sequence: index + 1,
    id: `m-${index}`,
    task_id: "task-1",
    agent_id: "main",
    category: "chat",
    kind: "agent_message",
    summary: `Message ${index + 1}`,
    created_at: "2026-09-27T00:00:00Z",
  }));
  const html = renderConversationList(items, null, true, {
    heights: heights(Array(30).fill(100)),
    scrollTop: 1000,
    clientHeight: 300,
    headerHeight: 40,
    paddingTop: 10,
    overscanPx: 100,
  });
  const rendered = [...html.matchAll(/data-message-id="(m-\d+)"/g)].map(match => match[1]);
  assert.deepEqual(rendered, ["m-8", "m-9", "m-10", "m-11", "m-12", "m-13"]);
  assert.match(html, /data-ui-key="conversation-spacer-top"[^>]*style="height:800px"/);
  assert.match(html, /data-ui-key="conversation-spacer-bottom"[^>]*style="height:1600px"/);
  assert.match(html, /aria-hidden="true"/);
  assert.match(html, /id="load-older-conversation"/);
});

test("a conversation that fits renders exactly as before, with no spacers", () => {
  const items = Array.from({length: 3}, (_, index) => ({
    sequence: index + 1,
    id: `m-${index}`,
    task_id: "task-1",
    agent_id: "main",
    category: "chat",
    kind: "agent_message",
    summary: `Message ${index + 1}`,
    created_at: "2026-09-27T00:00:00Z",
  }));
  const html = renderConversationList(items, null, false, {
    heights: new Map(),
    scrollTop: 0,
    clientHeight: 800,
  });
  assert.equal((html.match(/data-message-id=/g) || []).length, 3);
  assert.doesNotMatch(html, /conversation-spacer/);
  assert.doesNotMatch(html, /id="load-older-conversation"/);
});

test("a delivered channel reply hidden by its status card is not counted as a row", async () => {
  const items = [{
    sequence: 1,
    id: "reply-1",
    task_id: "task-1",
    agent_id: "main",
    category: "update",
    kind: "agent_channel_reply",
    summary: "reply",
    payload: {inbound_receipt_id: "receipt-1"},
    created_at: "2026-09-27T00:00:00Z",
  }, {
    sequence: 2,
    id: "status-1",
    task_id: "task-1",
    agent_id: "main",
    category: "update",
    kind: "agent_channel_route",
    summary: "status",
    payload: {status_card: true, message_status: "replied", inbound_receipt_id: "receipt-1"},
    created_at: "2026-09-27T00:00:00Z",
  }];
  const html = renderConversationList(items, null, false, {
    heights: new Map(),
    scrollTop: 0,
    clientHeight: 800,
  });
  assert.equal((html.match(/data-message-id="reply-1"/g) || []).length, 0);
  assert.equal((html.match(/data-message-id="status-1"/g) || []).length, 1);
  const {conversationItemKeys} = await import(pathToFileURL(resolve(root, "build", "conversation_ui.js")));
  assert.deepEqual(conversationItemKeys(items), ["status-1"]);
});

test("the virtual list reports item heights and bounds from the same keys", async () => {
  // Guards against a window that measures one list and renders another.
  const source = await readFile(resolve(root, "src", "conversation_ui.ts"), "utf8");
  assert.match(source, /export function conversationRenderableItems/);
  assert.match(source, /export function conversationWindow/);
  assert.match(source, /const renderable = conversationRenderableItems\(items\)/);
  assert.match(source, /const visible = window \? renderable\.slice\(window\.start, window\.end\) : renderable/);
});