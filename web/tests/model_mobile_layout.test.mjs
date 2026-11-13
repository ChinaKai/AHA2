// Static contract for the phone model page. The <=640px layout reuses the
// desktop table rows as cards, so the markup keeps its data hooks and only
// gains labels; these checks make sure the desktop table stays untouched and
// the narrow-screen overrides cannot drift out of their media blocks.
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

test("model card rules cannot leak to other .models-table consumers", async () => {
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

test("model rows expose labels and card markers for the phone layout", async () => {
  const main = await readFile(resolve(root, "src", "main.ts"), "utf8");
  const row = main.match(/function modelRowHTML[\s\S]*?\n}/)?.[0] || "";
  assert.notEqual(row, "", "modelRowHTML must exist");
  assert.match(row, /<tr class="model-row" data-model-card="\$\{escapeHTML\(item\.id\)\}">/);
  assert.match(row, /class="model-name-cell"/);
  assert.match(row, /data-label="Provider"/);
  assert.match(row, /data-label="Backend\/协议"/);
  assert.match(row, /data-label="Context"/);
  assert.match(row, /class="model-health-cell" data-label="可用性"/);
  assert.match(row, /data-edit-model="\$\{item\.id\}"/);
  assert.match(row, /data-delete-model="\$\{item\.id\}"/);

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

test("the model page opts into card layout and safe space for the pet", async () => {
  const main = await readFile(resolve(root, "src", "main.ts"), "utf8");
  assert.match(main, /<section class="page models-page">/);
  assert.match(main, /<section class="panel model-panel">/);
});

test("<=640px turns model rows into Provider-grouped cards", async () => {
  const css = await readFile(resolve(root, "src", "styles.css"), "utf8");
  const [block = ""] = mediaBlocks(css, "max-width: 640px");
  assert.notEqual(block, "", "a max-width: 640px block must exist");

  // The horizontal scroller and the desktop min-width are both disabled.
  assert.match(block, /\.models-table-wrap \{ overflow: visible; \}/);
  assert.match(block, /\.models-table-wrap \.models-table,\s*\.models-table-wrap \.models-table thead,\s*\.models-table-wrap \.models-table tbody \{ display: block; width: 100%; \}/);
  assert.match(block, /\.models-table-wrap \.models-table td:nth-child\(3\),\s*\.models-table-wrap \.models-table td:nth-child\(5\) \{ display: block;/);
  assert.match(block, /\.models-table-wrap \.models-table td\.model-health-cell \{ min-width: 0; \}/);

  // Each row is a card with a header row and labelled detail rows.
  assert.match(block, /\.models-table-wrap \.models-table \.model-row \{ display: grid; grid-template-columns: 20px minmax\(0,1fr\) auto;/);
  assert.match(block, /\.models-table-wrap \.models-table \.model-row > td\[data-label\] \{ grid-column: 1 \/ -1;/);
  assert.match(block, /content: attr\(data-label\)/);
  assert.match(block, /\.models-table-wrap \.models-table \.model-row > td\.row-actions \{ grid-column: 3; grid-row: 1;/);

  // Provider group header keeps its checkbox/indeterminate hook and count.
  assert.match(block, /\.models-table-wrap \.models-table \.model-group-row \{ display: flex;/);
  assert.match(block, /\.models-page \.model-group-select-label \{ display: inline;/);

  // Touch targets stay usable; the batch bar stays in normal document flow.
  assert.match(block, /\.models-table-wrap \.models-table \.model-row \.row-actions \.icon-button \{[^}]*min-height: 36px;/);
  assert.match(block, /\.models-table-wrap \.models-table \.model-row \.health-check-button \{[^}]*min-height: 36px;/);
  assert.doesNotMatch(block, /\.model-batch-bar \{[^}]*position:\s*(?:sticky|fixed)/);
  assert.match(block, /\.models-page \.model-batch-actions button \{[^}]*min-height: 36px;/);

  // Reserve room under the list so the pet and bottom nav cannot hide the
  // last card's edit/delete/check controls.
  assert.match(block, /\.models-page \{ padding-bottom: calc\(96px \+ env\(safe-area-inset-bottom\)\); \}/);
});

test("narrow phone breakpoints tighten the card layout without shrinking targets", async () => {
  const css = await readFile(resolve(root, "src", "styles.css"), "utf8");
  const blocks400 = mediaBlocks(css, "max-width: 400px");
  const blocks360 = mediaBlocks(css, "max-width: 360px");
  assert.ok(blocks400.some(block => /\.models-table-wrap \.models-table \.model-row \{ grid-template-columns: 18px minmax\(0,1fr\) auto;/.test(block)));
  assert.ok(blocks360.some(block => /\.models-table-wrap \.models-table \.model-row \{ padding: 9px 8px; \}/.test(block)));
  // The compact rules must not drop below the 36px primary action target.
  assert.ok(blocks360.every(block => !/row-actions \.icon-button \{[^}]*min-height: (?:3[0-5]|[12]?\d)px/.test(block)));
});

test("the desktop model table keeps its existing appearance", async () => {
  const css = await readFile(resolve(root, "src", "styles.css"), "utf8");
  // Desktop rules survive outside the phone block.
  assert.match(css, /\.model-health-cell \{ min-width: 128px; \}/);
  assert.match(css, /\.models-table input\[type="checkbox"\] \{ width: 15px; height: 15px;/);
  // The old 760px column-hiding rules stay in place for >640px widths and are
  // scoped to the model page so the MCP table keeps its status column.
  assert.match(css, /@media \(max-width: 760px\)[\s\S]*?\.models-table-wrap \.models-table th:nth-child\(3\), \.models-table-wrap \.models-table td:nth-child\(3\)/);
  // Card-only rules must not leak outside the phone/media blocks.
  const gridRule = css.indexOf(".models-table .model-row { display: grid;");
  const first640 = css.indexOf("@media (max-width: 640px)");
  assert.ok(gridRule > first640, "card grid must only be declared inside the phone block");
});

test("the pet docks above the phone bottom navigation", async () => {
  const assistant = await readFile(resolve(root, "src", "assistant.ts"), "utf8");
  assert.match(assistant, /const nav = document\.querySelector<HTMLElement>\("\.bottom-nav"\)/);
  assert.match(assistant, /window\.innerHeight - navHeight - PET_SIZE - 12/);
});
