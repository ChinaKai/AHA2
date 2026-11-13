// Static contract for the phone model list. Phones now reuse the desktop
// compact row (checkbox, name, health + actions) instead of stacking labelled
// cards, so these checks pin the shared DOM hooks, the no-overflow flex layout,
// the 36px touch targets, and the pet/bottom-nav clearance.
import test from "node:test";
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";

const root = resolve(import.meta.dirname, "..");

function mediaBlocks(css, query) {
  const marker = `@media (${query})`;
  const blocks = [];
  let from = 0;
  for (;;) {
    const start = css.indexOf(marker, from);
    if (start < 0) return blocks;
    const open = css.indexOf("{", start);
    let depth = 0;
    for (let index = open; index < css.length; index += 1) {
      if (css[index] === "{") depth += 1;
      else if (css[index] === "}") {
        depth -= 1;
        if (depth === 0) {
          blocks.push(css.slice(open + 1, index));
          from = index + 1;
          break;
        }
      }
    }
  }
}

function ruleSelectors(block) {
  const clean = block.replace(/\/\*[\s\S]*?\*\//g, "");
  const selectors = [];
  for (const rule of clean.split("}")) {
    const open = rule.indexOf("{");
    if (open < 0) continue;
    for (const part of rule.slice(0, open).split(",")) {
      selectors.push(part.replace(/\s+/g, " ").trim());
    }
  }
  return selectors;
}

test("model layout rules cannot leak to other .models-table consumers", async () => {
  const css = await readFile(resolve(root, "src", "styles.css"), "utf8");
  for (const query of ["max-width: 760px", "max-width: 640px", "max-width: 400px", "max-width: 360px"]) {
    for (const block of mediaBlocks(css, query)) {
      for (const selector of ruleSelectors(block)) {
        if (!/\.models-table(?![\w-])/.test(selector)) continue;
        assert.match(
          selector,
          /^\.models-table-wrap \.models-table(?![\w-])/,
          `${query} block contains an unscoped model-table selector: ${selector}`,
        );
      }
    }
  }
});

test("model rows keep the shared hooks for selection, grouping and row actions", async () => {
  const main = await readFile(resolve(root, "src", "main.ts"), "utf8");
  const row = main.match(/function modelRowHTML[\s\S]*?\n}/)?.[0] || "";
  assert.notEqual(row, "", "modelRowHTML must exist");
  assert.match(row, /<tr class="model-row" data-model-card="\$\{escapeHTML\(item\.id\)\}">/);
  assert.match(row, /class="model-select-cell"/);
  assert.match(row, /data-select-model="\$\{escapeHTML\(item\.id\)\}"/);
  assert.match(row, /class="model-name-cell"/);
  assert.match(row, /class="model-actions-cell"/);
  assert.match(row, /class="model-health-cell"/);
  assert.match(row, /data-edit-model="\$\{item\.id\}"/);
  assert.match(row, /data-delete-model="\$\{item\.id\}"/);
  // The phone card labels are gone; nothing uses data-label anymore.
  assert.doesNotMatch(row, /data-label=/);

  const group = main.match(/function modelGroupRowsHTML[\s\S]*?\n}/)?.[0] || "";
  assert.match(group, /<tr class="model-group-row" data-model-group=/);
  assert.match(group, /data-select-model-group="\$\{escapeHTML\(group\.providerId\)\}"/);
  assert.match(group, /class="model-group-select-label">全选本页</);
  assert.match(group, /共 \$\{group\.total\} 个 · 本页 \$\{group\.items\.length\} 个/);

  const results = main.match(/function modelResultsHTML[\s\S]*?\n}/)?.[0] || "";
  assert.match(results, /class="table-wrap models-table-wrap"/);
  assert.match(results, /data-select-models-page/);
  assert.match(results, /class="model-page-select-label">全选本页</);
});

test("the model page opts into compact rows and safe space for the pet", async () => {
  const main = await readFile(resolve(root, "src", "main.ts"), "utf8");
  assert.match(main, /<section class="page models-page">/);
  assert.match(main, /<section class="panel model-panel">/);
});

test("<=640px keeps the same single-line row and cannot overflow the viewport", async () => {
  const css = await readFile(resolve(root, "src", "styles.css"), "utf8");
  const [block = ""] = mediaBlocks(css, "max-width: 640px");
  assert.notEqual(block, "", "a max-width: 640px block must exist");

  // The table stack becomes block so the row can be a flex line.
  assert.match(block, /\.models-table-wrap \{ overflow: visible; \}/);
  assert.match(block, /\.models-table-wrap \.models-table,\s*\.models-table-wrap \.models-table thead,\s*\.models-table-wrap \.models-table tbody \{ display: block; width: 100%; \}/);
  assert.match(block, /\.models-table-wrap \.models-table \.model-row \{ display: flex; align-items: center;/);
  assert.match(block, /\.models-table-wrap \.models-table \.model-row > td\.model-name-cell \{ flex: 1 1 auto; width: auto; min-width: 0; overflow: hidden; \}/);
  assert.match(block, /\.models-table-wrap \.models-table \.model-row > td\.model-actions-cell \{ flex: 0 0 auto; width: auto; padding: 0; \}/);

  // The name ellipsizes instead of wrapping; no labelled detail rows remain.
  assert.doesNotMatch(block, /td\[data-label\]/);
  assert.doesNotMatch(block, /content: attr\(data-label\)/);
  assert.doesNotMatch(block, /grid-template-columns: 20px/);

  // Provider group header keeps its checkbox/indeterminate hook and count.
  assert.match(block, /\.models-table-wrap \.models-table \.model-group-row \{ display: flex;/);
  assert.match(block, /\.models-page \.model-group-select-label \{ display: inline;/);

  // Touch targets stay usable (declared once in the shared row rules); the
  // batch bar stays in normal document flow.
  assert.match(css, /\.model-row-actions \.model-health \{ flex-wrap: nowrap; \}/);
  assert.match(css, /\.model-row-actions \.icon-button \{[^}]*min-height: 36px;/);
  assert.match(css, /\.model-row-actions \.health-check-button \{[^}]*min-height: 36px;/);
  assert.doesNotMatch(block, /\.model-batch-bar \{[^}]*position:\s*(?:sticky|fixed)/);
  assert.match(block, /\.models-page \.model-batch-actions button \{[^}]*min-height: 36px;/);

  // Reserve room under the list so the pet and bottom nav cannot hide the
  // last row's edit/delete/check controls.
  assert.match(block, /\.models-page \{ padding-bottom: calc\(96px \+ env\(safe-area-inset-bottom\)\); \}/);
});

test("narrow phone breakpoints only tighten spacing and keep 36px targets", async () => {
  const css = await readFile(resolve(root, "src", "styles.css"), "utf8");
  const blocks400 = mediaBlocks(css, "max-width: 400px");
  const blocks360 = mediaBlocks(css, "max-width: 360px");
  assert.ok(blocks400.some(block => /\.models-table-wrap \.models-table \.model-row \{ gap: 6px; padding: 4px 8px; \}/.test(block)));
  assert.ok(blocks360.some(block => /\.models-table-wrap \.models-table \.model-row \{ gap: 5px; padding: 4px 6px; \}/.test(block)));
  assert.ok(blocks360.some(block => /\.model-actions-cell \.health-check-button \{ padding: 0 7px; font-size: 10px; \}/.test(block)));
  // The compact rules must not drop below the 36px primary action target.
  for (const block of [...blocks400, ...blocks360]) {
    if (!/model-row/.test(block)) continue;
    assert.doesNotMatch(block, /icon-button \{[^}]*min-height: (?:3[0-5]|[12]?\d)px/);
  }
});

test("the compact desktop table no longer hides or narrows the action column", async () => {
  const css = await readFile(resolve(root, "src", "styles.css"), "utf8");
  // The old 760px rule hid the Provider/Context columns by nth-child; with
  // three columns that would hide the action cell, so it must be gone.
  const tablet = mediaBlocks(css, "max-width: 760px").join("\n");
  assert.doesNotMatch(tablet, /models-table[^{]*nth-child\(3\)[^{]*\{[^}]*display: none/);
  assert.doesNotMatch(css, /models-table th:nth-child\(5\)/);
  assert.match(css, /\.models-table-wrap \.models-table \{ table-layout: fixed; \}/);
});

test("the pet docks above the phone bottom navigation", async () => {
  const assistant = await readFile(resolve(root, "src", "assistant.ts"), "utf8");
  assert.match(assistant, /const nav = document\.querySelector<HTMLElement>\("\.bottom-nav"\)/);
  assert.match(assistant, /window\.innerHeight - navHeight - PET_SIZE - 12/);
});
