import test from "node:test";
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {createPetApprovalCounter, petPendingApprovalBadge} from "../dist/pet_approvals.js";

const root = resolve(import.meta.dirname, "../..");

test("pending badges hide zero and display counts capped at 99+", () => {
  assert.equal(petPendingApprovalBadge(0), "");
  assert.match(petPendingApprovalBadge(7), />7<\/span>/);
  assert.match(petPendingApprovalBadge(99), />99<\/span>/);
  assert.match(petPendingApprovalBadge(100), />99\+<\/span>/);
  assert.match(petPendingApprovalBadge(105), /class="task-tool-badge"/);
});

test("approve/reject/cancel refresh from the same authoritative counter", async () => {
  let pending = 3;
  const seen = [];
  const counter = createPetApprovalCounter(async () => ({pending}), () => seen.push(counter.pending));
  await counter.refresh();
  for (const decision of ["approve", "reject", "cancel"]) {
    pending--;
    await counter.refresh(true);
    assert.equal(counter.pending, pending, decision);
  }
  assert.deepEqual(seen, [3, 2, 1, 0]);
});

test("mutation during an in-flight count discards stale results and refreshes once", async () => {
  const reads = [];
  const seen = [];
  const counter = createPetApprovalCounter(() => new Promise(resolve => reads.push(resolve)), () => seen.push(counter.pending));
  const first = counter.refresh();
  assert.equal(counter.refresh(), first);
  assert.equal(counter.refresh(true), first);
  reads.shift()({pending: 9});
  await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(seen, []);
  assert.equal(reads.length, 1);
  reads.shift()({pending: 2});
  await first;
  assert.deepEqual(seen, [2]);
});

test("failed count refresh retains the last count and allows the next poll", async () => {
  let fail = false;
  const counter = createPetApprovalCounter(async () => {
    if (fail) throw new Error("offline");
    return {pending: 5};
  }, () => {});
  await counter.refresh();
  fail = true;
  await assert.rejects(counter.refresh(), /offline/);
  assert.equal(counter.pending, 5);
  fail = false;
  await counter.refresh();
  assert.equal(counter.pending, 5);
});

test("workbench defaults to core and removes journal UI/API while retaining old bookmarks", async () => {
  const main = await readFile(resolve(root, "web/src/main.ts"), "utf8");
  const api = await readFile(resolve(root, "web/src/api.ts"), "utf8");
  const assistant = await readFile(resolve(root, "web/src/assistant.ts"), "utf8");
  const i18n = await readFile(resolve(root, "web/src/i18n.ts"), "utf8");
  assert.match(main, /let petWorkbenchTab = "core"/);
  assert.match(main, /class="active" data-pet-workbench-tab="core"/);
  assert.doesNotMatch(main, /data-pet-workbench-tab="diary"|view\.diary|diaryLimit|petDiary/);
  assert.doesNotMatch(api, /petDiary|diary_limit|diaryLimit/);
  assert.doesNotMatch(assistant + i18n, /日记|diary/i);
  assert.match(main, /value === "pet-diary"\) return "pet-workbench"/);
  assert.match(main, /setInterval\(\(\) => void refreshPetPendingApprovalCount\(\), 60_000\)/);
  assert.match(main, /await api.resolvePetApproval[\s\S]*?await refreshPetPendingApprovalCount\(true\)/);
  assert.doesNotMatch(main, /view\.approvals\.filter\(item => item\.status === "pending"\)\.length/);
});
