import test from "node:test";
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {renderTaskReviewDecision} from "../build/task_agents.js";
import {applyLocale, tr} from "../build/i18n.js";

test("historical Tasks have no fabricated review decision", () => {
  assert.equal(renderTaskReviewDecision({}), "");
  assert.equal(renderTaskReviewDecision({review_decision: ""}), "");
});

test("skipped review shows reason and escapes untrusted audit fields", () => {
  const html = renderTaskReviewDecision({review_decision: "skipped", review_reason: '<script>"reason"</script>', review_decided_by: 'pet" onclick="bad', review_decided_at: "2026-11-09T00:00:00Z"});
  assert.match(html, /已跳过评审/);
  assert.match(html, /&lt;script&gt;&quot;reason&quot;&lt;\/script&gt;/);
  assert.match(html, /pet&quot; onclick=&quot;bad/);
  assert.match(html, /class="task-review-detail" data-i18n-ignore/);
  assert.doesNotMatch(html, /<script>|title="pet"/);
});

test("required review shows the associated review Task", () => {
  const html = renderTaskReviewDecision({review_decision: "required", review_task_id: 'task-review<1>'});
  assert.match(html, /需要评审/);
  assert.match(html, /task-review&lt;1&gt;/);
  assert.doesNotMatch(html, /已跳过评审/);
});

test("review decision labels support both locales without translating reasons", () => {
  const reason = "原始理由";
  for (const [locale, skipped, required] of [["zh-CN", "已跳过评审", "需要评审"], ["en-US", "Review skipped", "Review required"]]) {
    applyLocale(locale);
    const html = renderTaskReviewDecision({review_decision: "skipped", review_reason: reason}, tr);
    assert.ok(html.includes(skipped));
    assert.ok(html.includes(reason));
    assert.ok(renderTaskReviewDecision({review_decision: "required", review_task_id: "review-1"}, tr).includes(required));
  }
  applyLocale("zh-CN");
});

test("Task detail renders and refreshes review decisions", async () => {
  const main = await readFile(new URL("../src/main.ts", import.meta.url), "utf8");
  assert.match(main, /id="task-review-slot">\$\{renderTaskReviewDecision\(detail.task, tr\)\}/);
  assert.match(main, /replaceRegionHTML\(reviewSlot, renderTaskReviewDecision\(detail.task, tr\)\)/);
});
