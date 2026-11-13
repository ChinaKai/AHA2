// Static contract for the compact single-line model list. Every width shares
// one row shape - checkbox, name, then health badge + check + edit + delete in
// a single actions cell - so these checks pin the three-column table, the
// ellipsis hooks, the 48px row, and the absence of the old Provider/Backend/
// Context cells that used to push the action column off the panel.
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

function rules(block) {
  const clean = block.replace(/\/\*[\s\S]*?\*\//g, "");
  const found = [];
  for (const rule of clean.split("}")) {
    const open = rule.indexOf("{");
    if (open < 0) continue;
    found.push({
      selector: rule.slice(0, open).replace(/\s+/g, " ").trim(),
      body: rule.slice(open + 1).trim(),
    });
  }
  return found;
}

function ruleFor(block, selector) {
  const match = rules(block).find(rule => rule.selector === selector);
  assert.ok(match, `missing rule for ${selector}`);
  return match.body;
}

function modelFunction(main, name) {
  const body = main.match(new RegExp(`function ${name}\\([\\s\\S]*?\\n}`))?.[0] || "";
  assert.notEqual(body, "", `${name} must exist`);
  return body;
}

test("each model row has exactly three cells: select, name, actions", async () => {
  const main = await readFile(resolve(root, "src", "main.ts"), "utf8");
  const row = modelFunction(main, "modelRowHTML");
  // Strip the explanatory comments so only the rendered markup is inspected.
  const markup = row.replace(/\/\/.*$/gm, "");
  assert.equal((markup.match(/<td /g) || []).length, 3, "the row must keep only three cells");
  assert.match(markup, /<td class="model-select-cell">/);
  assert.match(markup, /<td class="model-name-cell">/);
  assert.match(markup, /<td class="model-actions-cell">/);
  // Provider, Backend/协议 and Context used to be their own cells; the compact
  // row removes them and the phone data-label fallbacks with them.
  assert.doesNotMatch(markup, /data-label=/);
  assert.doesNotMatch(markup, /Provider|Backend\/协议|Context|auto_compact_window|max_output_tokens|context_window/);
  assert.doesNotMatch(markup, /provider_name|provider_id|backendProtocolLabel/);
  // wire_model is no longer a second small line; it rides in the title.
  assert.doesNotMatch(row, /<small/);
  assert.match(row, /const title = item\.wire_model \? `\$\{item\.display_name\} · \$\{item\.wire_model\}` : item\.display_name;/);
  assert.match(row, /<strong title="\$\{escapeHTML\(title\)\}">/);
  assert.match(row, /class="model-name-text"/);
  assert.match(row, /capability-badge/);
});

test("the header and group header collapse to the same three columns", async () => {
  const main = await readFile(resolve(root, "src", "main.ts"), "utf8");
  const results = modelFunction(main, "modelResultsHTML");
  assert.equal((results.match(/<th[ >]/g) || []).length, 3, "the table header must have three columns");
  assert.match(results, /<th>模型<\/th>/);
  assert.match(results, /<th class="model-actions-head">操作<\/th>/);
  assert.doesNotMatch(results, />Provider<|>Backend<|>Context<|可用性/);
  assert.match(results, /<tr><td colspan="3">/);

  const group = modelFunction(main, "modelGroupRowsHTML");
  assert.match(group, /<td colspan="2" class="model-group-title">/);
  assert.match(group, /escapeHTML\(group\.providerName\)/);
  assert.match(group, /class="model-group-count"/);
  assert.match(group, /data-select-model-group=/);
});

test("the health badge, check, edit and delete share one action cell", async () => {
  const main = await readFile(resolve(root, "src", "main.ts"), "utf8");
  const row = modelFunction(main, "modelRowHTML");
  const health = row.indexOf('class="model-health-cell"');
  const actions = row.indexOf('class="row-actions"');
  assert.ok(health > 0, "the health cell must be rendered");
  assert.ok(actions > health, "edit/delete follow the health badge and check button");
  assert.match(row, /data-check-model|renderModelHealthCell\(item\.id/);
  assert.match(row, /data-edit-model="\$\{item\.id\}"/);
  assert.match(row, /data-delete-model="\$\{item\.id\}"/);
});

test("the table is fixed-layout with an ellipsized name and a bounded action column", async () => {
  const css = await readFile(resolve(root, "src", "styles.css"), "utf8");
  assert.match(css, /\.models-table-wrap \.models-table \{ table-layout: fixed; \}/);
  const first = ruleFor(
    css,
    ".models-table-wrap .models-table th:nth-child(1), .models-table-wrap .models-table td:nth-child(1)",
  );
  assert.match(first, /width: 34px;/);
  const actions = ruleFor(
    css,
    ".models-table-wrap .models-table th:nth-child(3), .models-table-wrap .models-table td:nth-child(3)",
  );
  assert.match(actions, /width: 272px;/);
  assert.match(actions, /text-align: right;/);

  assert.match(ruleFor(css, ".models-table-wrap .models-table .model-row"), /height: 48px;/);
  assert.match(ruleFor(css, ".models-table-wrap .models-table .model-name-cell"), /overflow: hidden;/);
  const name = ruleFor(css, ".models-table-wrap .models-table .model-name-text");
  assert.match(name, /min-width: 0;/);
  assert.match(name, /overflow: hidden;/);
  assert.match(name, /text-overflow: ellipsis;/);
  assert.match(name, /white-space: nowrap;/);
  assert.match(
    ruleFor(css, ".models-table-wrap .models-table .model-name-cell strong"),
    /display: flex;/,
  );
});

test("the action group never wraps and keeps 36px touch targets", async () => {
  const css = await readFile(resolve(root, "src", "styles.css"), "utf8");
  const group = ruleFor(css, ".model-row-actions");
  assert.match(group, /display: flex;/);
  assert.match(group, /align-items: center;/);
  assert.match(group, /justify-content: flex-end;/);
  assert.match(ruleFor(css, ".model-row-actions .model-health"), /flex-wrap: nowrap;/);
  assert.match(ruleFor(css, ".model-row-actions .icon-button"), /min-height: 36px;/);
  assert.match(ruleFor(css, ".model-row-actions .icon-button"), /min-width: 36px;/);
  assert.match(ruleFor(css, ".model-row-actions .health-check-button"), /min-height: 36px;/);
  // The evidence panel opens out of flow so the row cannot grow past 52px.
  assert.match(css, /\.model-health-detail \{ position: relative;[^}]*margin-top: 0;/);
  assert.match(css, /\.model-health-detail small \{[^}]*position: absolute;/);
});

test("the old seven-column desktop contract is gone", async () => {
  const css = await readFile(resolve(root, "src", "styles.css"), "utf8");
  assert.doesNotMatch(css, /models-table th:nth-child\(4\)/);
  assert.doesNotMatch(css, /models-table th:nth-child\(5\)/);
  assert.doesNotMatch(css, /models-table th:nth-child\(6\)/);
  assert.doesNotMatch(css, /models-table td:nth-child\(5\)/);
  assert.doesNotMatch(css, /@media \(max-width: 1500px\)/);
  assert.doesNotMatch(css, /grid-template-columns: minmax\(260px,1fr\) minmax\(760px/);
});

test("phone widths keep the same single row instead of stacking labelled cards", async () => {
  const css = await readFile(resolve(root, "src", "styles.css"), "utf8");
  const [block = ""] = mediaBlocks(css, "max-width: 640px");
  assert.notEqual(block, "", "a max-width: 640px block must exist");
  const row = ruleFor(block, ".models-table-wrap .models-table .model-row");
  assert.match(row, /display: flex;/);
  assert.match(row, /align-items: center;/);
  assert.match(row, /min-height: 48px;/);
  assert.match(row, /height: auto;/);
  assert.match(
    ruleFor(block, ".models-table-wrap .models-table .model-row > td.model-name-cell"),
    /flex: 1 1 auto;[^}]*min-width: 0;/,
  );
  assert.match(
    ruleFor(block, ".models-table-wrap .models-table .model-row > td.model-actions-cell"),
    /flex: 0 0 auto;/,
  );
  // No data-label stacking remains anywhere in the phone layout.
  assert.doesNotMatch(block, /data-label/);
  assert.doesNotMatch(block, /content: attr\(data-label\)/);
  assert.doesNotMatch(block, /grid-template-columns: 20px/);
  // Narrow breakpoints only tighten spacing; the 36px targets stay.
  for (const query of ["max-width: 400px", "max-width: 360px"]) {
    for (const part of mediaBlocks(css, query)) {
      if (!part.includes(".model-row")) continue;
      assert.doesNotMatch(part, /min-height:\s*(?:3[0-5]|[12]?\d)px/);
    }
  }
});
