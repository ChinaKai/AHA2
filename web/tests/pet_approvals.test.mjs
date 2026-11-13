import test from "node:test";
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "..");
const {parsePetApprovalPayload, petApprovalOptions, petApprovalOptionLabel, petApprovalModelBatch, renderPetApprovalModelBatch, renderPetApprovalOptionPicker, selectedPetApprovalOption} = await import(
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

test("batch model delete approvals show the snapshotted count and names", () => {
  const approval = {
    kind: "delete_models",
    status: "pending",
    payload: JSON.stringify({model_ids: ["m-1", "m-2"], model_names: ["Kimi A", "Kimi B"], count: 2}),
  };
  assert.deepEqual(petApprovalModelBatch(approval), {count: 2, names: ["Kimi A", "Kimi B"]});
  const html = renderPetApprovalModelBatch(approval);
  assert.match(html, /2 个/);
  assert.match(html, /Kimi A、Kimi B/);
  assert.equal(petApprovalModelBatch({kind: "delete_model", payload: approval.payload}), null);
  assert.equal(renderPetApprovalModelBatch({kind: "delete_model", payload: approval.payload}), "");
  // A malformed payload falls back to whatever is still readable instead of
  // throwing inside the approval list.
  assert.deepEqual(petApprovalModelBatch({kind: "delete_models", payload: "{not json"}), null);
  assert.deepEqual(petApprovalModelBatch({kind: "delete_models", payload: {model_names: ["Only"]}}), {count: 1, names: ["Only"]});
});

test("heavy approval cards show the executing state and refresh until it finishes", async () => {
  const main = await readFile(resolve(root, "src", "main.ts"), "utf8");
  const approvals = await readFile(resolve(root, "src", "pet_approvals.ts"), "utf8");
  assert.match(approvals, /const executing = status === "executing"/);
  assert.match(approvals, /petWorkbench\.approvalExecutingHint/);
  assert.match(main, /const buttons = \[\.\.\.dialog\.querySelectorAll<HTMLButtonElement>\("\[data-pet-approval-decision\]"\)\]/);
  assert.match(main, /buttons\.forEach\(item => \{ item\.disabled = true; \}\)/);
  assert.match(main, /if \(result\.status === "executing"\) scheduleApprovalExecutionPoll\(\)/);
  assert.match(main, /function scheduleApprovalExecutionPoll\(\): void/);
  assert.match(main, /some\(item => item\.status === "executing"\)/);
});

test("approval cards wire the picker into approve and keep reject/cancel paths", async () => {
  const main = await readFile(resolve(root, "src", "main.ts"), "utf8");
  const approvals = await readFile(resolve(root, "src", "pet_approvals.ts"), "utf8");
  // The detail dialog is rendered by the shared approval module; main only
  // binds the decisions.
  assert.match(main, /renderPetApprovalDetail\(approval, \{[\s\S]{0,120}translate: petApprovalText/);
  assert.match(approvals, /renderPetApprovalOptionPicker\(item\.id, petApprovalOptions\(item\), translate\)/);
  assert.match(main, /if \(decision === "approve"\) \{[\s\S]{0,700}selectedPetApprovalOption\(options, select\?\.value\)[\s\S]{0,300}if \(!decisionReason\) \{[\s\S]{0,300}return;/);
  assert.match(main, /if \(decision === "reject"\) \{\s*decisionReason = \(window\.prompt\(t\("petApproval\.rejectReasonPrompt"\)\)/);
  assert.match(main, /api\.resolvePetApproval\(approvalID, decision, decisionReason\)/);
  // A failed resolve must show the server message inside the open dialog
  // instead of silently re-enabling the buttons.
  assert.match(main, /const showDialogError = \(dialog: HTMLDialogElement, message: string\): void => \{/);
  assert.match(approvals, /data-pet-approval-error/);
  assert.match(main, /showDialogError\(dialog, error instanceof Error \? error\.message : String\(error\)\)/);
  const app = await readFile(resolve(root, "build", "app.js"), "utf8");
  assert.match(app, /\.\/pet_approvals\.js\?v=[a-f0-9]{12}/);
});
