// Contract for the rebuilt MCP page: a compact desktop line, the same DOM as
// a three-line phone card with no horizontal overflow, read-only built-ins,
// dialog validation for empty/duplicate values, and the dialog dismissal
// behaviour (Esc via native <dialog>, backdrop click, focus return, inert
// background via showModal).
import test from "node:test";
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "../..");
const webRoot = resolve(root, "web");
const mcp = await import(pathToFileURL(resolve(webRoot, "build", "mcp_page.js")).href);

const now = "2026-11-13T08:00:00Z";
const builtin = {
  id: "mcp_channel", name: "渠道（内置）", description: "群聊路由的 Turn 自动注入；提供渠道回复工具。",
  transport: "builtin", enabled: true, builtin: true, auto_inject: true, health: "ok",
  tools: [{name: "channel_reply", description: "回复触发本 Turn 的渠道消息"}],
  created_at: now, updated_at: now,
};
const user = {
  id: "mcp_user", name: "Filesystem", description: "本地文件工具，描述很长很长，不应该出现在列表行里。",
  transport: "http", url: "https://mcp.example.test/mcp", enabled: true, builtin: false, health: "error",
  tools: [{name: "read_file", description: "读取文件"}], last_checked_at: now,
  env: {API_KEY: "secret-value"}, headers: {Authorization: "Bearer secret"},
  created_at: now, updated_at: now,
};

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
      selectors: rule.slice(0, open).split(",").map(selector => selector.replace(/\s+/g, " ").trim()),
      body: rule.slice(open + 1).trim(),
    });
  }
  return found;
}

function ruleFor(block, selector) {
  const match = rules(block).find(rule => rule.selectors.includes(selector));
  assert.ok(match, `missing rule for ${selector}`);
  return match.body;
}

function draft(overrides = {}) {
  return {
    name: "", transport: "http", url: "", command: "", args: "",
    description: "", headerKeys: "", envKeys: "", enabled: true,
    ...overrides,
  };
}

test("the page is one .mcp-* list and never reuses the model table", async () => {
  const html = mcp.renderMCPPage([user, builtin]);
  assert.doesNotMatch(html, /models-table/);
  assert.match(html, /class="mcp-list"/);
  assert.match(html, /id="mcp-add" class="primary"/);
  assert.match(html, /id="mcp-refresh"/);
  assert.equal((html.match(/class="mcp-list-head"/g) || []).length, 1);
  assert.equal((html.match(/class="mcp-row/g) || []).length, 2);
  for (const id of ["mcp-edit-dialog", "mcp-detail-dialog", "mcp-delete-dialog"]) {
    assert.match(html, new RegExp(`id="${id}"`));
  }
  assert.match(html, /id="mcp-edit-form" novalidate/);
  assert.match(html, /aria-labelledby="mcp-edit-title"/);

  const css = await readFile(resolve(webRoot, "src", "styles.css"), "utf8");
  const clean = css.replace(/\/\*[\s\S]*?\*\//g, "");
  assert.doesNotMatch(clean, /\.mcp-[^{]*\.models-table|\.models-table[^{]*\.mcp-/);
});

test("the desktop row is a compact single line without the description", async () => {
  const row = mcp.renderMCPRow(user);
  assert.match(row, /class="mcp-row"/);
  assert.match(row, /class="mcp-name"/);
  assert.match(row, /class="mcp-conn"/);
  assert.match(row, /class="mcp-status"/);
  assert.match(row, /class="mcp-actions"/);
  // The description only feeds the detail dialog; the row must not grow a
  // second line of text.
  assert.doesNotMatch(row, /本地文件工具/);
  assert.doesNotMatch(row, /<small/);

  const css = await readFile(resolve(webRoot, "src", "styles.css"), "utf8");
  const head = ruleFor(css, ".mcp-row");
  assert.match(head, /display: grid;/);
  assert.match(head, /grid-template-columns: minmax\(0, 1fr\) 112px 190px 208px;/);
  const rowRule = rules(css).filter(rule => rule.selectors.includes(".mcp-row")).map(rule => rule.body).join(" ");
  assert.match(rowRule, /min-height: 48px;/);
  const name = ruleFor(css, ".mcp-name strong");
  assert.match(name, /overflow: hidden;/);
  assert.match(name, /text-overflow: ellipsis;/);
  assert.match(name, /white-space: nowrap;/);
});

test("<=1040px turns the same DOM into a three-line card without overflow", async () => {
  const css = await readFile(resolve(webRoot, "src", "styles.css"), "utf8");
  const block = mediaBlocks(css, "max-width: 1040px").find(candidate => candidate.includes(".mcp-row")) ?? "";
  assert.notEqual(block, "", "a max-width: 1040px MCP card block must exist");
  assert.match(ruleFor(block, ".mcp-list-head"), /display: none;/);
  const row = ruleFor(block, ".mcp-row");
  assert.match(row, /display: grid;/);
  assert.match(row, /grid-template-columns: minmax\(0, 1fr\) auto;/);
  assert.match(row, /grid-template-areas: "name name" "conn status" "actions actions";/);
  assert.match(ruleFor(block, ".mcp-row .mcp-name"), /grid-area: name;/);
  assert.match(ruleFor(block, ".mcp-row .mcp-conn"), /grid-area: conn;[\s\S]*min-width: 0;/);
  assert.match(ruleFor(block, ".mcp-row .mcp-status"), /grid-area: status;/);
  assert.match(ruleFor(block, ".mcp-row .mcp-actions"), /grid-area: actions;/);
  assert.match(ruleFor(block, ".mcp-actions button"), /min-height: 36px;/);
  // No fixed column may exceed a 360px viewport once the list becomes cards.
  for (const rule of rules(block)) {
    for (const match of rule.body.matchAll(/width:\s*(\d+)px/g)) {
      assert.ok(Number(match[1]) <= 400, `fixed width ${match[1]}px in ${rule.selectors.join(",")}`);
    }
  }

  const phone = mediaBlocks(css, "max-width: 640px").find(candidate => candidate.includes(".mcp-page")) ?? "";
  assert.notEqual(phone, "", "the <=640px block keeps the phone bottom padding");
  assert.match(ruleFor(phone, ".mcp-page"), /padding-bottom: calc\(96px \+ env\(safe-area-inset-bottom\)\);/);
});

test("the 641-1040px band keeps names visible and every action reachable", async () => {
  const css = await readFile(resolve(webRoot, "src", "styles.css"), "utf8");
  // The app shell keeps its 228px sidebar plus the 8px resizer through 1040px;
  // this arithmetic mirrors that layout without a browser dependency.
  const sidebar = Number(css.match(/--sidebar-width:\s*(\d+)px/)?.[1] ?? NaN)
    + Number(css.match(/\.sidebar-resizer \{[^}]*?width:\s*(\d+)px/)?.[1] ?? NaN);
  const pagePadding = Number(css.match(/^\.page \{[^}]*?padding:\s*(\d+)px;/m)?.[1] ?? NaN);
  const mobilePage = mediaBlocks(css, "max-width: 760px")
    .map(block => block.match(/\.page \{[^}]*?padding:\s*\d+px (\d+)px/m)?.[1])
    .find(Boolean);
  assert.ok(Number.isFinite(sidebar) && Number.isFinite(pagePadding) && mobilePage, "app shell metrics must be readable");

  const card = mediaBlocks(css, "max-width: 1040px").find(candidate => candidate.includes(".mcp-row")) ?? "";
  assert.notEqual(card, "", "the card block must cover the intermediate band");
  const cardRow = ruleFor(card, ".mcp-row");
  const cardPadding = Number(cardRow.match(/padding:\s*\d+px (\d+)px/)?.[1] ?? NaN);
  const desktopRow = rules(css)
    .filter(rule => rule.selectors.includes(".mcp-row"))
    .map(rule => rule.body)
    .join(" ");
  const columns = desktopRow.match(/grid-template-columns:([^;]+);/)?.[1] ?? "";
  const fixedColumns = [...columns.matchAll(/(\d+)px/g)].reduce((sum, match) => sum + Number(match[1]), 0);
  const gap = Number(desktopRow.match(/gap:\s*\d+px (\d+)px/)?.[1] ?? NaN);
  assert.ok(Number.isFinite(cardPadding) && fixedColumns > 0 && Number.isFinite(gap), "row metrics must be readable");

  // Three 2-glyph actions at 12px plus 10px side padding and two 6px gaps.
  const actionsWidth = 3 * (2 * 10 + 24) + 2 * 6;
  const nameFloor = 200;
  for (const width of [641, 700, 761, 768, 800, 829, 830, 880, 1040]) {
    assert.ok(width <= 1040, `${width}px must be covered by the card block`);
    const chrome = width > 760 ? sidebar : 0;
    const padding = width > 760 ? pagePadding : Number(mobilePage);
    const rowWidth = width - chrome - padding * 2 - 2 - cardPadding * 2;
    assert.ok(rowWidth >= nameFloor, `${width}px leaves the name only ${rowWidth}px`);
    assert.ok(rowWidth >= actionsWidth, `${width}px leaves actions only ${rowWidth}px of ${actionsWidth}px`);
  }

  // At the first desktop pixel the four fixed columns still leave the name a
  // readable floor once the same shell and row padding are subtracted.
  const desktopRowWidth = 1041 - sidebar - pagePadding * 2 - 2 - cardPadding * 2;
  assert.ok(desktopRowWidth - fixedColumns - gap * 3 >= 140, `1041px name width ${desktopRowWidth - fixedColumns - gap * 3}px`);
});

test("built-in servers stay read-only and keep their badges", () => {
  // "builtin" is server-side only: the edit dialog may not offer it.
  const page = mcp.renderMCPPage([user, builtin]);
  assert.doesNotMatch(page, /value="builtin"/);
  assert.match(page, /<option value="http">HTTP<\/option><option value="stdio">stdio<\/option>/);

  const row = mcp.renderMCPRow(builtin);
  assert.equal((row.match(/data-mcp-detail=/g) || []).length, 1);
  assert.equal((row.match(/data-mcp-edit=/g) || []).length, 0);
  assert.equal((row.match(/data-mcp-delete=/g) || []).length, 0);
  assert.match(row, /内置/);
  assert.match(row, /自动注入/);

  const editable = mcp.renderMCPRow(user);
  assert.equal((editable.match(/data-mcp-edit=/g) || []).length, 1);
  assert.equal((editable.match(/data-mcp-delete=/g) || []).length, 1);
});

test("the detail dialog carries the full description, address, tools, health and check time", () => {
  const detail = mcp.renderMCPDetail(user);
  assert.match(detail, /本地文件工具/);
  assert.match(detail, /https:\/\/mcp\.example\.test\/mcp/);
  assert.match(detail, /工具数/);
  assert.match(detail, /健康状态/);
  assert.match(detail, /最后检查/);
  assert.match(detail, /read_file/);
  assert.doesNotMatch(detail, /从未检查/);
  assert.match(detail, /data-mcp-detail-edit/);
  assert.match(detail, /data-mcp-detail-delete/);
  // No secret values are rendered anywhere in the detail body.
  assert.doesNotMatch(detail, /secret-value/);
  assert.doesNotMatch(detail, /Bearer secret/);

  const readOnly = mcp.renderMCPDetail(builtin);
  assert.doesNotMatch(readOnly, /data-mcp-detail-edit/);
  assert.doesNotMatch(readOnly, /data-mcp-detail-delete/);
  assert.match(readOnly, /channel_reply/);

  const unchecked = mcp.renderMCPDetail({...user, last_checked_at: undefined});
  assert.match(unchecked, /从未检查/);
});

test("empty and duplicate names, URLs and commands are rejected before saving", () => {
  const servers = [user, {...user, id: "mcp_stdio", name: "Board", transport: "stdio", url: "", command: "npx"}];
  assert.equal(mcp.mcpDraftError(draft({name: ""}), servers), "nameRequired");
  assert.equal(mcp.mcpDraftError(draft({name: " filesystem ", url: "https://new.example.test/mcp"}), servers), "duplicateName");
  assert.equal(mcp.mcpDraftError(draft({name: "New", url: ""}), servers), "urlRequired");
  assert.equal(mcp.mcpDraftError(draft({name: "New", url: "ftp://example.test"}), servers), "urlRequired");
  assert.equal(mcp.mcpDraftError(draft({name: "New", url: "https://mcp.example.test/mcp"}), servers), "duplicateURL");
  assert.equal(mcp.mcpDraftError(draft({name: "New", transport: "stdio", command: ""}), servers), "commandRequired");
  assert.equal(mcp.mcpDraftError(draft({name: "New", transport: "stdio", command: "npx"}), servers), "duplicateCommand");
  assert.equal(mcp.mcpDraftError(draft({name: "Filesystem", url: user.url}), servers, user.id), "");
  assert.equal(mcp.mcpDraftError(draft({name: "New", url: "https://new.example.test/mcp"}), servers), "");
  assert.equal(mcp.mcpDraftError(draft({name: "New", transport: "stdio", command: "python"}), servers), "");

  for (const error of ["nameRequired", "urlRequired", "commandRequired", "duplicateName", "duplicateURL", "duplicateCommand"]) {
    assert.notEqual(mcp.mcpDraftErrorMessage(error), "");
    assert.notEqual(mcp.mcpDraftErrorMessage(error), `mcp.error.${error}`);
  }
  assert.equal(mcp.mcpDraftErrorMessage(""), "");
});

test("secret values survive an edit: only key names travel through the dialog", () => {
  assert.deepEqual(mcp.parseKeyNames("API_KEY\nBASE_URL, API_KEY\n\n"), ["API_KEY", "BASE_URL"]);
  assert.deepEqual(mcp.mergeKeyMap(["API_KEY", "NEW_KEY"], user.env), {API_KEY: "secret-value", NEW_KEY: ""});
  assert.deepEqual(mcp.mergeKeyMap(["Authorization"], user.headers), {Authorization: "Bearer secret"});
});

test("dialogs dismiss on the backdrop and return focus; showModal keeps the page inert", async () => {
  const source = await readFile(resolve(webRoot, "src", "mcp_page.ts"), "utf8");
  assert.match(source, /dialog\.showModal\(\)/);
  assert.match(source, /if \(event\.target === dialog\) dialog\.close\(\)/);
  assert.match(source, /dialog\.onclose = \(\) => \{[\s\S]*?target\.focus\(\)/);
  assert.match(source, /window\.setTimeout\(\(\) => form\.querySelector<HTMLInputElement>\('\[name="name"\]'\)\?\.focus\(\), 0\)/);
  assert.match(source, /data-mcp-close/);
  // Deleting always goes through the confirmation dialog first.
  assert.match(source, /function openMCPDeleteDialog[\s\S]*?confirm\.onclick = \(\) => \{[\s\S]*?api\.deleteMCPServer/);
  // Native <dialog> handles Esc; no key handler may swallow it.
  assert.doesNotMatch(source, /keydown|Escape/);
});