import test from "node:test";
import assert from "node:assert/strict";
import {renderConversationList} from "../build/conversation_ui.js";
import {planKeyedNodes, stableTextSignature} from "../build/ui_helpers.js";

test("an unchanged task list replaces no rows", () => {
  const state = [
    {key: "task-a", signature: "a1"},
    {key: "task-b", signature: "b1"},
    {key: "task-c", signature: "c1"},
  ];
  const plan = planKeyedNodes(state, state.map(item => ({...item})));
  assert.deepEqual(plan.order, ["task-a", "task-b", "task-c"]);
  assert.deepEqual(plan.replace, []);
  assert.deepEqual(plan.insert, []);
  assert.deepEqual(plan.remove, []);
});

test("a refresh replaces only rows whose content changed", () => {
  const plan = planKeyedNodes(
    [
      {key: "task-a", signature: "running-1"},
      {key: "task-b", signature: "done-1"},
      {key: "task-c", signature: "running-1"},
    ],
    [
      {key: "task-a", signature: "running-2"},
      {key: "task-b", signature: "done-1"},
      {key: "task-c", signature: "running-1"},
    ],
  );
  assert.deepEqual(plan.replace, ["task-a"]);
  assert.deepEqual(plan.remove, []);
  assert.deepEqual(plan.insert, []);
});

test("new, removed, and reordered rows keep the surviving rows intact", () => {
  const plan = planKeyedNodes(
    [
      {key: "task-a", signature: "a1"},
      {key: "task-b", signature: "b1"},
      {key: "task-c", signature: "c1"},
    ],
    [
      {key: "task-d", signature: "d1"},
      {key: "task-b", signature: "b1"},
      {key: "task-a", signature: "a1"},
    ],
  );
  assert.deepEqual(plan.order, ["task-d", "task-b", "task-a"]);
  assert.deepEqual(plan.insert, ["task-d"]);
  assert.deepEqual(plan.remove, ["task-c"]);
  assert.deepEqual(plan.replace, []);
});

test("row signatures are stable for identical data and change with visible fields", () => {
  const task = {id: "task-1", title: "Build", status: "running", total_tokens: 10};
  const same = stableTextSignature([task, "Project", "/work"]);
  assert.equal(stableTextSignature([{...task}, "Project", "/work"]), same);
  assert.notEqual(stableTextSignature([{...task, status: "completed"}, "Project", "/work"]), same);
  assert.notEqual(stableTextSignature([{...task, total_tokens: 11}, "Project", "/work"]), same);
  assert.notEqual(stableTextSignature([task, "Renamed project", "/work"]), same);
});

test("a conversation message keeps its content signature until the message itself changes", () => {
  const item = {
    id: "message-1",
    task_id: "task-1",
    sequence: 1,
    kind: "agent_message",
    category: "assistant",
    summary: "first line",
    created_at: "2026-11-13T10:00:00Z",
    payload: {},
  };
  const signatureOf = value => renderConversationList([value], null, false).match(/data-message-signature="([^"]+)"/)?.[1];
  const original = signatureOf(item);
  assert.ok(original);
  assert.equal(signatureOf({...item, payload: {...item.payload}}), original);
  assert.notEqual(signatureOf({...item, summary: "second line"}), original);
});