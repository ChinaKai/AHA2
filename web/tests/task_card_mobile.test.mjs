// Source-level guard for the mobile Task card layout: the status badge leaves
// the title row for the action column on narrow screens, while desktop keeps
// its current markup and the read-only rows keep exactly one state badge.
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import test from "node:test";

const root = resolve(import.meta.dirname, "..");
const read = name => readFile(resolve(root, "src", name), "utf8");

test("mobile task cards move the status badge above the row actions", async () => {
  const main = await read("main.ts");
  const styles = await read("styles.css");

  // The title keeps only the task code and heading; its status node is marked
  // so the mobile breakpoint can drop it without touching the heading.
  assert.match(
    main,
    /<div class="task-card-title"><span class="task-code">[^`]*<h3 data-task-title="\$\{task\.id\}">[^`]*<span class="status task-card-title-status /,
    "the title status is not distinguishable from the action-column status",
  );
  assert.match(
    main,
    /const readonlyBadge = archived \|\| task\.read_only \|\| task\.status === "completed";/,
    "the read-only badge rule changed",
  );
  // Read-only rows already show 已完成只读 / 归档只读 / 只读 in the action column,
  // so they must not get a second mobile status copy.
  assert.match(
    main,
    /class="task-card-actions\$\{readonlyBadge \? "" : " has-status"\}"/,
    "the action column does not mark the mobile status layout",
  );
  assert.match(
    main,
    /readonlyBadge \? "" : `<span class="status task-card-actions-status \$\{statusClass\(task\.status\)\}">\$\{statusLabel\(task\.status\)\}<\/span>`/,
    "the mobile action-column status is rendered for read-only rows too",
  );

  // One visible badge per breakpoint: hidden on desktop, swapped on mobile.
  assert.match(styles, /\.task-card-actions-status \{ display: none; \}/);
  assert.match(
    styles,
    /@media \(max-width: 760px\)[\s\S]*?\.task-card-title \.task-card-title-status \{ display: none; \}/,
  );
  assert.match(
    styles,
    /@media \(max-width: 760px\)[\s\S]*?\.task-card-actions\.has-status \{[^}]*display: grid;[^}]*grid-template-columns: auto auto;[^}]*justify-items: end;/,
  );
  assert.match(
    styles,
    /@media \(max-width: 760px\)[\s\S]*?\.task-card-actions \.task-card-actions-status \{[^}]*display: inline-flex;[^}]*grid-column: 1 \/ -1;/,
  );
  // The action buttons must keep their normal hit area on mobile.
  assert.doesNotMatch(
    styles,
    /@media \(max-width: 760px\)[\s\S]*?\.task-card-actions[^}]*button[^}]*\{[^}]*min-height:/,
    "the mobile layout shrinks the row action buttons",
  );
});