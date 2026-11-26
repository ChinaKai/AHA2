import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";
import test from "node:test";

const build = resolve(import.meta.dirname, "../build");
const {renderTaskMemory} = await import(pathToFileURL(resolve(build, "task_agents.js")));
const memory = {task_id: "task-1", current_goal: "Verify the release"};

test("state card omits the workstream section when no lines are recorded", () => {
  for (const card of [undefined, memory, {...memory, lines: []}]) {
    assert.doesNotMatch(renderTaskMemory(card), /memory-lines/);
  }
});

test("state card shows open lines first, escapes server text and folds closed lines", async () => {
  const moduleText = await readFile(resolve(build, "task_agents.js"), "utf8");
  const i18nImport = moduleText.match(/from "\.\/(i18n\.js\?v=[^"]+)"/)?.[1];
  assert.ok(i18nImport, "task_agents must use the versioned i18n singleton");
  const {applyLocale} = await import(new URL(i18nImport, pathToFileURL(resolve(build, "task_agents.js"))));
  const stamp = "2026-11-26T09:20:00Z";
  const card = {...memory, lines: [
    {id: "docs", title: "Finish docs", owner: "self", state: "closed", updated_at: stamp},
    {id: "device", title: "Validate <b>", owner: "other", who: 'Lab & "QA"', state: "blocked",
      step: "Build <sent>", blocker: "<img src=x>", updated_at: stamp},
  ]};
  try {
    applyLocale("zh-CN");
    const zh = renderTaskMemory(card);
    assert.match(zh, /<strong>分线<\/strong>/);
    assert.match(zh, /<details><summary>已闭环 1 条<\/summary>/);
    assert.match(zh, /负责方.+Lab &amp; &quot;QA&quot;/);
    assert.match(zh, /<time datetime="2026-11-26T09:20:00Z">[^<]+<\/time>/);
    const activeAt = zh.indexOf("Validate &lt;b&gt;");
    const summaryAt = zh.indexOf("<summary>已闭环 1 条");
    const closedAt = zh.indexOf("Finish docs");
    for (const index of [activeAt, summaryAt, closedAt]) assert.ok(index >= 0, "workstream markup is missing");
    assert.ok(activeAt < summaryAt && summaryAt < closedAt, "closed work must be inside the fold after active work");
    assert.match(zh, /进展.+Build &lt;sent&gt;/);
    assert.match(zh, /卡点.+&lt;img src=x&gt;/);
    assert.doesNotMatch(zh, /<img src=x>|Validate <b>|Build <sent>/);

    applyLocale("en-US");
    const en = renderTaskMemory(card);
    for (const word of ["Workstreams", "Blocked", "Responsible", "Progress", "Blocker", "Closed workstreams (1)", "The Task"]) {
      assert.ok(en.includes(word), `missing English label: ${word}`);
    }
    assert.doesNotMatch(en, /\p{Script=Han}/u);
  } finally {
    applyLocale("zh-CN");
  }
});
