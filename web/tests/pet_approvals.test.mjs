import test from "node:test";
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "..");
const {parsePetApprovalPayload, petApprovalOptions, petApprovalOptionLabel, renderPetApprovalOptionPicker, selectedPetApprovalOption} = await import(
  pathToFileURL(resolve(root, "build", "pet_approvals.js")).href
);

const pending = (payload, extra = {}) => ({kind: "owner_decision", status: "pending", payload, ...extra});

test("owner_decision payload options become selectable choices", () => {
  assert.deepEqual(petApprovalOptions(pending('{"options":["replay","skip"],"delivery_id":"d-1"}')), ["replay", "skip"]);
  assert.deepEqual(petApprovalOptions(pending({options: ["replay", "skip"]})), ["replay", "skip"]);
  assert.deepEqual(petApprovalOptions(pending('{"options":["replay","", 3, null, "replay","custom"]}')), ["replay", "custom"]);
  assert.equal(petApprovalOptionLabel("replay"), "重放");
  assert.equal(petApprovalOptionLabel("skip"), "跳过");
  assert.equal(petApprovalOptionLabel("keep_waiting"), "keep_waiting");
});

test("invalid, empty, or missing payloads keep the plain approval actions", () => {
  for (const payload of [undefined, "", "   ", "{not json", "null", "[]", '"text"', "42"]) {
    assert.deepEqual(parsePetApprovalPayload(payload), {}, `payload ${String(payload)}`);
    assert.deepEqual(petApprovalOptions(pending(payload)), [], `payload ${String(payload)}`);
  }
  assert.deepEqual(petApprovalOptions(pending("{}")), []);
  assert.deepEqual(petApprovalOptions(pending('{"options":[]}')), []);
  assert.deepEqual(petApprovalOptions(pending('{"options":"replay"}')), []);
  assert.deepEqual(petApprovalOptions(pending('{"options":[1,2]}')), []);
  assert.equal(renderPetApprovalOptionPicker("approval-1", []), "");
});

test("options only apply to pending owner_decision approvals", () => {
  const payload = '{"options":["replay","skip"]}';
  assert.deepEqual(petApprovalOptions(pending(payload, {kind: "delete_task"})), []);
  assert.deepEqual(petApprovalOptions(pending(payload, {status: "approved"})), []);
  assert.deepEqual(petApprovalOptions(pending(payload, {status: "cancelled"})), []);
});

test("picker escapes values and approve note must be an offered option", () => {
  const html = renderPetApprovalOptionPicker('a"1', ["skip", '<b>"x"</b>']);
  assert.match(html, /选择方案/);
  assert.match(html, /data-pet-approval-option="a&quot;1"/);
  assert.match(html, /<option value="">请选择方案<\/option>/);
  assert.match(html, /<option value="skip">跳过<\/option>/);
  assert.match(html, /value="&lt;b&gt;&quot;x&quot;&lt;\/b&gt;"/);
  assert.doesNotMatch(html, /<b>/);
  assert.equal(selectedPetApprovalOption(["replay", "skip"], "skip"), "skip");
  assert.equal(selectedPetApprovalOption(["replay", "skip"], ""), "");
  assert.equal(selectedPetApprovalOption(["replay", "skip"], "other"), "");
  assert.equal(selectedPetApprovalOption(["replay", "skip"], undefined), "");
});

test("heavy approval cards show the executing state and refresh until it finishes", async () => {
  const main = await readFile(resolve(root, "src", "main.ts"), "utf8");
  assert.match(main, /const status = executing \? tr\("执行中"\)/);
  assert.match(main, /petWorkbench\.approvalExecutingHint/);
  assert.match(main, /const cardButtons = card[\s\S]{0,200}\.disabled = true/);
  assert.match(main, /if \(result\.status === "executing"\) scheduleApprovalExecutionPoll\(\)/);
  assert.match(main, /function scheduleApprovalExecutionPoll\(\): void/);
  assert.match(main, /some\(item => item\.status === "executing"\)/);
});

test("approval cards wire the picker into approve and keep reject/cancel paths", async () => {
  const main = await readFile(resolve(root, "src", "main.ts"), "utf8");
  assert.match(main, /renderPetApprovalOptionPicker\(item\.id, petApprovalOptions\(item\), tr\)/);
  assert.match(main, /if \(decision === "approve"\) \{[\s\S]{0,700}selectedPetApprovalOption\(options, select\?\.value\)[\s\S]{0,200}if \(!decisionReason\) \{[\s\S]{0,300}return;/);
  assert.match(main, /if \(decision === "reject"\) \{\s*decisionReason = \(window\.prompt\(t\("petApproval\.rejectReasonPrompt"\)\)/);
  assert.match(main, /api\.resolvePetApproval\(id, decision, decisionReason\)/);
  // A failed resolve must show the server message on the clicked card instead
  // of silently re-enabling the button.
  assert.match(main, /const showApprovalError = \(button: HTMLButtonElement, message: string\) => \{/);
  assert.match(main, /data-pet-approval-error/);
  assert.match(main, /showApprovalError\(button, error instanceof Error \? error\.message : String\(error\)\)/);
  const app = await readFile(resolve(root, "build", "app.js"), "utf8");
  assert.match(app, /\.\/pet_approvals\.js\?v=[a-f0-9]{12}/);
});
