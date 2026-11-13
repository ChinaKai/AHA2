import test from "node:test";
import assert from "node:assert/strict";
import {readFileSync} from "node:fs";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const modulesDir = resolve(import.meta.dirname, "..", "build");
const cards = await import(pathToFileURL(resolve(modulesDir, "mail_cards.js")));
const resources = await import(pathToFileURL(resolve(modulesDir, "resources.js")));
const mailResources = await import(pathToFileURL(resolve(modulesDir, "mail_resources.js")));
const mailTask = await import(pathToFileURL(resolve(modulesDir, "mail_task.js")));
const mailEditor = await import(pathToFileURL(resolve(modulesDir, "mail_editor.js")));
// The build rewrites shared imports to "./api.js?v=<build>"; importing the
// unversioned URL would create a second APIClient singleton. Reuse the exact
// specifier mail_task.js resolved so the stub lands on the same object.
const mailTaskSource = await readFile(resolve(modulesDir, "mail_task.js"), "utf8");
const apiVersion = mailTaskSource.match(/from "\.\/api\.js(\?v=[^"]+)"/)?.[1] || "";
const apiModule = await import(pathToFileURL(resolve(modulesDir, "api.js")).href + apiVersion);

const SANITIZER_TAGS = new Set([
  "h1", "h2", "h3", "h4", "h5", "h6", "p", "br", "hr",
  "strong", "b", "em", "i", "u", "s", "ul", "ol", "li",
  "blockquote", "code", "pre", "table", "thead", "tbody", "tfoot",
  "tr", "th", "td", "caption", "a", "img",
]);
const SANITIZER_ATTRS = {
  a: new Set(["href", "title"]),
  img: new Set(["src", "alt", "width", "height"]),
  table: new Set(["style"]),
  tr: new Set(["style"]),
  td: new Set(["colspan", "rowspan", "style"]),
  th: new Set(["colspan", "rowspan", "style"]),
};
const SANITIZER_STYLE_PROPERTIES = new Set([
  "border", "border-collapse", "border-spacing", "border-color", "border-style",
  "border-width", "border-top", "border-right", "border-bottom", "border-left",
  "padding", "padding-top", "padding-right", "padding-bottom", "padding-left",
  "text-align", "vertical-align", "width", "min-width", "max-width",
  "height", "min-height", "max-height", "background-color", "color",
  "font-weight", "font-size", "line-height", "white-space",
]);

// Structural mirror of internal/mail/sanitize.go's allowlist. This is not a
// replacement for the Go tests; it keeps the client normalizer from emitting
// tags, attributes or styles the server would reject.
function assertSanitizerSubset(html) {
  for (const match of html.matchAll(/<([a-zA-Z0-9]+)(\s[^<>]*?)?\/?>/g)) {
    const tag = match[1].toLowerCase();
    assert.ok(SANITIZER_TAGS.has(tag), `tag <${tag}> is allowlisted`);
    const allowed = SANITIZER_ATTRS[tag] || new Set();
    for (const attribute of (match[2] || "").matchAll(/([a-zA-Z-]+)(?:="([^"]*)")?/g)) {
      const name = attribute[1].toLowerCase();
      assert.ok(allowed.has(name), `attribute ${name} is allowlisted on <${tag}>`);
      if (name !== "style") continue;
      assert.ok(["table", "tr", "td", "th"].includes(tag), `style is only allowed on table elements, not <${tag}>`);
      for (const declaration of (attribute[2] || "").split(";")) {
        const trimmed = declaration.trim();
        if (!trimmed) continue;
        const [property, ...valueParts] = trimmed.split(":");
        assert.ok(SANITIZER_STYLE_PROPERTIES.has(property.trim().toLowerCase()), `style property ${property} is allowlisted`);
        const value = valueParts.join(":").trim();
        assert.ok(value, "style value is not empty");
        assert.match(value, /^[\x20-\x7e]+$/, "style value is ASCII");
        assert.doesNotMatch(value, /url\(|expression|@import|!important|javascript:|behavior|-moz-binding|position|display/i);
        assert.doesNotMatch(value, /[!()\\"'<>{};/:]/, "style value has no unsafe punctuation");
      }
    }
  }
  assert.doesNotMatch(html, /<(?:div|span|font)\b/i, "contenteditable residue is removed");
}

function message(overrides = {}) {
  return {
    id: "mail_message_1",
    task_id: "task_1",
    account_id: "mail_account_1",
    source: "agent",
    idempotency_key: "key-1",
    from_address: "noreply@example.com",
    to: ["qa@example.com"],
    cc: [],
    bcc: [],
    subject: "提测通知",
    body_text: "正文",
    template_version: 2,
    content_revision: 1,
    approval_digest: "sha256:abcdef1234567890",
    status: "pending_approval",
    created_at: "2026-11-06T09:00:00Z",
    updated_at: "2026-11-06T09:00:00Z",
    ...overrides,
  };
}

test("mail cancel follows the phase-one state rules", () => {
  for (const status of ["pending_approval", "queued", "failed", "dead_letter"]) {
    assert.equal(cards.mailCanCancel(status), true, status);
  }
  for (const status of ["sending", "sent", "rejected", "cancelled", "draft"]) {
    assert.equal(cards.mailCanCancel(status), false, status);
  }
  assert.equal(cards.mailCanRetry("failed"), true);
  assert.equal(cards.mailCanRetry("dead_letter"), true);
  assert.equal(cards.mailCanRetry("queued"), false);
});

test("pending card exposes approve/reject and never renders sending cancel", () => {
  const pending = cards.mailCardHtml({message: message(), attachments: [], deliveries: []});
  assert.match(pending, /data-mail-action="approve"/);
  assert.match(pending, /data-mail-action="reject"/);
  assert.match(pending, /data-mail-action="cancel"/);
  assert.match(pending, /待审批/);
  assert.match(pending, /abcdef123456/);

  const sending = cards.mailCardHtml({
    message: message({status: "sending"}),
    attachments: [],
    deliveries: [{state: "sending", attempts: 1, outcome_certainty: "unknown", updated_at: "2026-11-06T09:00:00Z"}],
  });
  assert.doesNotMatch(sending, /data-mail-action="cancel"/);
  assert.match(sending, /发送中，不可取消/);
});

test("conversation timeline exports no mail card renderer", () => {
  assert.equal(cards.mailConversationCardHtml, undefined);
});

test("older pending mail stays visible and actionable beyond the recent-five cut", () => {
  // Six non-pending messages plus one older pending message: the recent list
  // may cap at five, but the pending list must still expose the only approval.
  const cards = Array.from({length: 7}, (_, index) => ({
    message: message({id: `mail_message_${index + 1}`, subject: `邮件 ${index + 1}`, status: "sent"}),
    attachments: [],
    deliveries: [],
  }));
  cards[6] = {
    message: message({id: "mail_pending_old", subject: "较早的待审批", status: "pending_approval"}),
    attachments: [],
    deliveries: [],
  };
  const detail = {task: {id: "task_1", status: "active"}};
  const html = mailTask.renderTaskMailPanel(detail, {
    accounts: [],
    templates: [],
    messages: cards,
    attachments: [],
    build_artifacts: [],
  }, [], false);

  assert.match(html, /待审批（1）/);
  assert.match(html, /data-mail-card="mail_pending_old"/);
  const pendingArticle = html.match(/<article[^>]*data-mail-card="mail_pending_old"[\s\S]*?<\/article>/)?.[0] || "";
  assert.equal((pendingArticle.match(/data-mail-action="review"/g) || []).length, 1, "the pending row keeps one approval entry");
  assert.doesNotMatch(pendingArticle, /data-mail-action="approve"|data-mail-action="reject"/);
  assert.ok(html.indexOf("待审批（1）") < html.indexOf("最近邮件"), "pending list is rendered before the recent list");
  assert.match(html, /data-mail-card="mail_message_5"/);
  assert.doesNotMatch(html, /data-mail-card="mail_message_6"/, "non-pending recent mail is still capped at five");
});

test("task mail compact rows expose one primary entry and hide rare actions behind more", () => {
  const pending = {
    message: message({id: "mail_pending", subject: "待审批主题", status: "pending_approval"}),
    attachments: [{id: "att", message_id: "mail_pending", position: 0, source_type: "task_attachment", source_id: "a", file_name: "log.txt", media_type: "text/plain", size_bytes: 3, sha256: "abc", delivery_mode: "direct", disposition: "attachment", created_at: ""}],
    deliveries: [],
  };
  const sent = {
    message: message({id: "mail_sent", subject: "已发送主题", status: "sent", to: ["a@example.com", "b@example.com"]}),
    attachments: [],
    deliveries: [],
  };
  const failed = {
    message: message({id: "mail_failed", subject: "发送失败主题", status: "failed"}),
    attachments: [],
    deliveries: [],
  };
  const detail = {task: {id: "task_1", status: "active"}};
  const html = mailTask.renderTaskMailPanel(detail, {
    accounts: [], templates: [], messages: [pending, sent, failed], attachments: [], build_artifacts: [],
  }, [], false);

  assert.equal((html.match(/class="mail-resource-row mail-compact-row/g) || []).length, 3, "both sections use the shared compact row");
  assert.doesNotMatch(html, /class="mail-card /, "the full mail-card no longer renders in the Task panel");
  assert.doesNotMatch(html, /mail-card-meta/, "approval digests and attachment details stay out of the compact list");
  assert.match(html, /data-mail-card="mail_pending"/);
  assert.match(html, /data-mail-card="mail_sent"/);
  assert.match(html, /待审批/);
  assert.match(html, /已发送/);
  assert.match(html, /a@example\.com, b@example\.com/, "recipients stay visible");
  assert.match(html, /附件 1/, "attachment count replaces the expanded details");
  assert.doesNotMatch(html, /data-mail-action="approve"|data-mail-action="reject"/, "decision buttons never sit in a row");

  const pendingRow = html.slice(html.indexOf('data-mail-card="mail_pending"'), html.indexOf('data-mail-card="mail_sent"'));
  assert.equal((pendingRow.match(/data-mail-action="review"/g) || []).length, 1, "pending row has one primary entry");
  assert.match(pendingRow, /data-mail-action="review"[^>]*data-mail-message="mail_pending"[^>]*>审批</, "pending primary label is 审批");
  assert.match(pendingRow, /<details class="mail-row-more mail-compact-more">/, "rare actions live in 更多");
  assert.match(pendingRow, /data-mail-action="cancel"[^>]*data-mail-message="mail_pending"[^>]*>取消</);

  const sentRow = html.slice(html.indexOf('data-mail-card="mail_sent"'), html.indexOf('data-mail-card="mail_failed"'));
  assert.equal((sentRow.match(/data-mail-action="review"/g) || []).length, 1, "terminal row has one primary entry");
  assert.match(sentRow, /data-mail-action="review"[^>]*data-mail-message="mail_sent"[^>]*>查看</, "terminal primary label is 查看");
  assert.doesNotMatch(sentRow, /mail-row-more/, "sent row has no rare actions");

  const failedRow = html.slice(html.indexOf('data-mail-card="mail_failed"'));
  assert.match(failedRow, /data-mail-action="review"[^>]*data-mail-message="mail_failed"[^>]*>查看</);
  assert.match(failedRow, /mail-row-more/, "failed row keeps retry/cancel accessible");
  assert.match(failedRow, /data-mail-action="retry"/);
  assert.match(failedRow, /data-mail-action="cancel"/);
});

test("review preview keeps decision buttons only for pending messages", () => {
  const actions = {reload: async () => {}, notify: () => {}};
  const terminal = ["sent", "failed", "dead_letter", "rejected", "cancelled"];
  for (const status of terminal) {
    const data = mailTask.mailReviewPreviewData({
      message: message({id: `mail_${status}`, status, body_text: "终态正文"}),
      attachments: [],
      deliveries: [],
    }, actions);
    assert.equal(data.onApprove, undefined, `${status}: no approve callback`);
    assert.equal(data.onReject, undefined, `${status}: no reject callback`);
    assert.equal(data.title, "邮件预览", `${status}: terminal preview is read-only`);
    const html = cards.mailPreviewDialogHtml(data);
    assert.doesNotMatch(html, /data-mail-preview-decision=/, `${status}: no approve/reject buttons`);
    assert.match(html, /data-close>关闭</, `${status}: only the close footer remains`);
  }

  const pending = mailTask.mailReviewPreviewData({
    message: message({id: "mail_pending", status: "pending_approval"}),
    attachments: [],
    deliveries: [],
  }, actions);
  assert.equal(typeof pending.onApprove, "function");
  assert.equal(typeof pending.onReject, "function");
  const html = cards.mailPreviewDialogHtml(pending);
  assert.match(html, /data-mail-preview-decision="approve"/);
  assert.match(html, /data-mail-preview-decision="reject"/);
});

test("template WYSIWYG normalizes contenteditable output into the sanitizer subset", () => {
  const normalize = mailEditor.normalizeMailTemplateHtml;
  assert.equal(
    normalize("<div>第一行</div><div>第二行</div>"),
    "<p>第一行</p><p>第二行</p>",
    "Enter-created divs become paragraphs",
  );
  assert.equal(
    normalize('<p><span style="color:red">红</span> <font color="x">字</font></p>'),
    "<p>红 字</p>",
    "span/font wrappers and non-table styles are removed",
  );
  assert.equal(normalize("<p>&nbsp;</p><p>正文</p>"), "<p>正文</p>", "edge blank paragraphs and nbsp are cleaned");
  assert.equal(normalize("<p>前</p><p>&nbsp;</p><p>后</p>"), "<p>前</p><p><br></p><p>后</p>", "intentional internal blank lines survive");
  assert.equal(normalize('<script>alert(1)</script><p onclick="x">安全</p>'), "<p>安全</p>", "scripts and event attributes never survive");
  assert.equal(normalize("<strike>删除</strike><b>粗</b><i>斜</i><u>下划</u><s>删除线</s>"), "<p><s>删除</s><b>粗</b><i>斜</i><u>下划</u><s>删除线</s></p>");
  assert.equal(normalize("版本 {{build.version}}"), "<p>版本 {{build.version}}</p>", "bare typed text is wrapped as a paragraph and keeps the variable literal");
  assert.equal(normalize("<p>{{build.version}}</p>"), "<p>{{build.version}}</p>", "template variables remain literal text");
  assert.equal(normalize("a &amp; b &lt; c"), "<p>a &amp; b &lt; c</p>", "entities are neither lost nor double-escaped");
  assert.equal(normalize("<div><p>嵌套</p></div>"), "<p>嵌套</p>", "block wrappers are unwrapped without nesting p in p");
  assert.equal(normalize('<a href="javascript:alert(1)">bad</a><a href="https://example.com/x?a=1&amp;b=2">ok</a>'), '<p><a>bad</a><a href="https://example.com/x?a=1&amp;b=2">ok</a></p>');
  assert.equal(normalize('<img src="https://tracker.example/p.png" onerror="x"><img src="cid:att_1">'), '<p><img src="cid:att_1"></p>', "only cid images and safe attributes survive");
  assert.equal(normalize('<a href="{{task.url}}">打开任务</a>'), '<p><a href="{{task.url}}">打开任务</a></p>', "server-validated URL variables stay in href");
  assert.equal(normalize('<img src="cid:{{attachment.id}}">'), '<p><img src="cid:{{attachment.id}}"></p>', "cid placeholders stay in img src");
  assert.equal(normalize('<img src="cid:att_1" alt="{{task.title}}">'), '<p><img src="cid:att_1"></p>', "placeholders are dropped from attributes the server rejects");

  const filtered = normalize('<table style="border-collapse: collapse; position: absolute; width: 100%"><tbody><tr><td style="border: 1px solid #d0d7de; color: #333; display: block">&nbsp;</td></tr></tbody></table>');
  assert.match(filtered, /<table style="border-collapse: collapse; width: 100%">/);
  assert.match(filtered, /<td style="border: 1px solid #d0d7de; color: #333">&nbsp;<\/td>/);
  assert.doesNotMatch(filtered, /position|display|span|font/, "unsafe table styles are filtered");

  const skeleton = mailResources.mailTemplateTableSkeleton(3, 3);
  const normalizedSkeleton = normalize(skeleton);
  assert.match(normalizedSkeleton, /<table style="border-collapse: collapse; border-spacing: 0; width: 100%">/);
  assert.equal((normalizedSkeleton.match(/<th /g) || []).length, 3);
  assert.equal((normalizedSkeleton.match(/<td /g) || []).length, 9);
  assert.equal((normalizedSkeleton.match(/<td style="[^"]*">&nbsp;<\/td>/g) || []).length, 9, "empty cells keep a visible placeholder");
});

test("character references round-trip without double escaping", () => {
  const normalize = mailEditor.normalizeMailTemplateHtml;
  assert.equal(normalize("<p>&copy; 2026</p>"), "<p>&copy; 2026</p>", "named entities are not escaped again");
  assert.equal(normalize("<p>&amp;copy; 2026</p>"), "<p>&amp;copy; 2026</p>", "an escaped literal ampersand stays literal");
  assert.equal(normalize("<p>&#169; &#xA9; &euro;</p>"), "<p>&#169; &#xA9; &euro;</p>", "numeric and other named references survive");
  assert.equal(normalize("<p>a & b</p>"), "<p>a &amp; b</p>", "a bare ampersand is still escaped");
  assert.equal(normalize('<img src="cid:att_1" alt="&copy;">'), '<p><img src="cid:att_1" alt="&copy;"></p>', "attributes keep references verbatim");
  assert.equal(normalize('<a href="https://example.com/?a=1&amp;b=2">x</a>'), '<p><a href="https://example.com/?a=1&amp;b=2">x</a></p>');
});

test("pre/code keeps newlines and indentation through load and save", () => {
  const normalize = mailEditor.normalizeMailTemplateHtml;
  const pre = "<pre><code>line1\n    indented\n\nlast</code></pre>";
  assert.equal(normalize(pre), pre, "code directly under pre is preformatted");
  const nested = "<pre><code>if (a &lt; b) {\n\t  return  a;\n}</code></pre>";
  assert.equal(normalize(nested), nested, "entities and tabs inside pre are preserved");
  const withInline = "<pre>  <b>bold</b>\n    next</pre>";
  assert.equal(normalize(withInline), withInline, "inline children inherit the preformatted context");
  assert.equal(normalize("<code>a   b\nc</code>"), "<p><code>a b c</code></p>", "code outside pre still collapses ordinary whitespace");
});

test("normalization is idempotent after the first pass", () => {
  const normalize = mailEditor.normalizeMailTemplateHtml;
  const cases = [
    "<p>&copy; 2026</p>",
    "<p>&amp;copy; 2026</p>",
    "<pre><code>  one\n\n    two</code></pre>",
    "<p>a</p><p><br></p><p>b</p>",
    "<p>a & b</p>",
    "<div>first</div><div><span style=\"color:red\">second</span></div>",
    mailResources.mailTemplateTableSkeleton(3, 3),
    "<table><tbody><tr><td>outer<table><tbody><tr><td>inner</td></tr></tbody></table>tail</td></tr></tbody></table>",
    "<table><thead><tr><th>head<table><tbody><tr><td>inner</td></tr></tbody></table>tail</th></tr></thead></table>",
  ];
  for (const input of cases) {
    let current = normalize(input);
    for (let pass = 0; pass < 5; pass++) {
      const next = normalize(current);
      assert.equal(next, current, `stable after the first pass for ${JSON.stringify(input)}`);
      const blankBlocks = (current.match(/<p><br><\/p>/g) || []).length;
      const nextBlankBlocks = (next.match(/<p><br><\/p>/g) || []).length;
      assert.equal(nextBlankBlocks, blankBlocks, "blank paragraphs never accumulate");
      current = next;
    }
    assertSanitizerSubset(current);
  }
});

test("nested tables are hoisted after the outer table for td and th", () => {
  const normalize = mailEditor.normalizeMailTemplateHtml;
  const td = normalize("<table><tbody><tr><td>outer<table><tbody><tr><td>inner</td></tr></tbody></table>tail</td></tr></tbody></table>");
  const th = normalize("<table><thead><tr><th>head<table><tbody><tr><td>inner</td></tr></tbody></table>tail</th></tr></thead></table>");
  for (const [label, html, outerCell] of [["td", td, "outertail"], ["th", th, "headtail"]]) {
    assert.equal((html.match(/<table\b/g) || []).length, 2, `${label}: both tables survive`);
    const outerEnd = html.indexOf("</table>");
    assert.ok(outerEnd >= 0, `${label}: outer table closes`);
    assert.match(html.slice(outerEnd + "</table>".length), /^<table\b/, `${label}: the second table is a sibling after the outer table`);
    assert.match(html.slice(0, outerEnd), new RegExp(`<${label}>${outerCell}</${label}>`), `${label}: the nested table is removed from the cell`);
    assert.doesNotMatch(html, new RegExp(`<${label}[^>]*>[^<]*<table`), `${label}: no table remains inside a cell`);
    assert.equal(normalize(html), html, `${label}: hoisting is idempotent`);
    assertSanitizerSubset(html);
  }
});

test("table insertion inside a td or th goes after the enclosing table and focuses the new cell", () => {
  for (const cellTag of ["TD", "TH"]) {
    const events = [];
    class FakeNode {
      constructor(tagName) {
        this.tagName = tagName;
        this.nodeType = 1;
        this.parentNode = null;
        this.children = [];
        this.textContent = "";
        this.innerHTML = "";
        this.nextSibling = null;
        this._query = new Map();
      }
      contains(node) {
        for (let current = node; current; current = current.parentNode) if (current === this) return true;
        return false;
      }
      focus() { events.push("focus"); }
      dispatchEvent(event) { events.push(event.type); }
      insertBefore(fragment, reference) { this.inserted = {fragment, reference}; events.push("insertBefore"); }
      querySelector(selector) { return this._query.get(selector) ?? null; }
    }
    const editor = new FakeNode("DIV");
    const outerTable = new FakeNode("TABLE");
    const tbody = new FakeNode("TBODY");
    const tr = new FakeNode("TR");
    const cell = new FakeNode(cellTag);
    const nextSibling = new FakeNode("P");
    editor.appendChild = child => { child.parentNode = editor; editor.children.push(child); return child; };
    editor.appendChild(outerTable);
    tbody.parentNode = outerTable;
    tr.parentNode = tbody;
    cell.parentNode = tr;
    outerTable.nextSibling = nextSibling;
    const newTable = new FakeNode("TABLE");
    const firstCell = new FakeNode("TD");
    newTable._query.set("tbody td", firstCell);
    newTable._query.set("td", firstCell);
    const fragment = {
      childNodes: [newTable], lastChild: newTable,
      querySelector: selector => selector === "table" ? newTable : null,
    };
    const ranges = [];
    globalThis.document = {
      createElement(tag) { assert.equal(tag, "template"); return {content: {}}; },
      importNode() { return fragment; },
      createRange() {
        const range = {
          selected: null,
          setStartAfter() {}, collapse() {}, deleteContents() {}, insertNode() {},
          selectNodeContents(node) { range.selected = node; },
        };
        ranges.push(range);
        return range;
      },
    };
    const selection = {
      rangeCount: 1,
      start: null,
      getRangeAt: () => ({startContainer: cell, commonAncestorContainer: cell}),
      removeAllRanges() {},
      addRange(range) { selection.added = range; },
    };
    globalThis.window = {getSelection: () => selection};
    try {
      const result = mailEditor.insertMailEditorTable(editor, "<table><tbody><tr><td>x</td></tr></tbody></table>");
      assert.deepEqual(result, {inserted: true, afterTable: true}, `${cellTag}: insertion is hoisted`);
      assert.equal(editor.inserted.fragment, fragment, `${cellTag}: inserted at the table's parent`);
      assert.equal(editor.inserted.reference, nextSibling, `${cellTag}: inserted directly after the outer table`);
      assert.equal(cell.children.length, 0, `${cellTag}: nothing is nested into the cell`);
      assert.equal(selection.added?.selected, firstCell, `${cellTag}: focus moves to the new first data cell`);
      assert.ok(events.includes("insertBefore"));
      assert.ok(events.includes("input"), `${cellTag}: input event notifies the form`);
    } finally {
      delete globalThis.document;
      delete globalThis.window;
    }
  }
});

test("plain-text paste is escaped and never interpreted as markup", () => {
  const html = mailEditor.plainTextToEditorHtml("第一段\n第二行\n\n第三段 <script>alert(1)</script> & 结束");
  assert.equal(html, "<p>第一段<br>第二行</p><p>第三段 &lt;script&gt;alert(1)&lt;/script&gt; &amp; 结束</p>");
  assert.equal(mailEditor.plainTextToEditorHtml("   "), "", "blank paste is ignored");
  assert.match(mailEditor.plainTextToEditorHtml("a b"), /<p>a b<\/p>/, "nbsp collapses to a normal space");
});

test("preview envelope uses an inline empty attachment state and compact baseline CSS", async () => {
  const html = cards.mailPreviewDialogHtml({subject: "无附件", bodyHtml: "<p>x</p>"});
  assert.match(html, /<dt>附件<\/dt><dd><span class="field-help">无附件<\/span><\/dd>/, "empty attachment state is inline");
  assert.doesNotMatch(html, /<dd><p class="field-help">无附件<\/p><\/dd>/, "no paragraph margin can offset the attachment row");
  assert.match(html, /<dl class="mail-preview-envelope">/);

  const css = await readFile(resolve(modulesDir, "styles.css"), "utf8");
  const envelopeRule = css.match(/\.mail-preview-envelope\s*\{([^}]*)\}/);
  assert.ok(envelopeRule, "envelope rule exists");
  assert.match(envelopeRule[1], /align-items:\s*baseline/);
  assert.match(envelopeRule[1], /gap:\s*4px 10px/);
  assert.match(envelopeRule[1], /line-height:\s*1\.45/);
  const resetRule = css.match(/\.mail-preview-envelope dd > p,\s*\.mail-preview-envelope dd \.field-help\s*\{([^}]*)\}/);
  assert.ok(resetRule, "envelope paragraph margin reset exists");
  assert.match(resetRule[1], /margin:\s*0/);
  const toolbarRule = css.match(/\.mail-preview-toolbar\s*\{([^}]*)\}/);
  assert.match(toolbarRule[1], /flex-wrap:\s*wrap/);
  assert.match(toolbarRule[1], /min-width:\s*0/);
  const stageRule = css.match(/\.mail-preview-stage\s*\{([^}]*)\}/);
  assert.match(stageRule[1], /max-width:\s*100%/);
  assert.match(stageRule[1], /min-width:\s*0/);
});

test("template editor insert-table skeleton is sanitizer-safe and editable", () => {
  const html = mailResources.mailTemplateTableSkeleton(2, 3);
  assert.match(html, /<table style="border-collapse: collapse; border-spacing: 0; width: 100%;">/);
  assert.match(html, /<thead>/);
  assert.match(html, /<tbody>/);
  assert.equal((html.match(/<th /g) || []).length, 3, "one th per column");
  assert.equal((html.match(/<td /g) || []).length, 6, "rows x columns td cells");
  for (const match of html.matchAll(/style="([^"]*)"/g)) {
    for (const declaration of match[1].split(";").map(value => value.trim()).filter(Boolean)) {
      const [property, ...value] = declaration.split(":");
      assert.ok(["border", "border-collapse", "border-spacing", "padding", "text-align", "vertical-align", "width", "background-color", "font-weight"].includes(property.trim()), property);
      assert.doesNotMatch(value.join(":"), /url\(|expression|@import|!important|position|display|calc\(/i);
    }
  }
  // Skeleton clamps to the same limits the editor validates.
  assert.equal((mailResources.mailTemplateTableSkeleton(0, 0).match(/<td /g) || []).length, 1);
  assert.equal((mailResources.mailTemplateTableSkeleton(999, 99).match(/<td /g) || []).length, 500);
});

test("send preview uses the selected account and renders server HTML in a sandbox", () => {
  const data = mailTask.taskMailPreviewData({
    subject: "构建完成",
    to: ["qa@example.com"],
    cc: ["lead@example.com"],
    body_text: "纯文本回退",
    body_html: "<p>HTML 正文</p>",
    warnings: ["示例警告"],
    attachments: [{
      source_type: "task_attachment", source_id: "att_1", file_name: "log.txt",
      media_type: "text/plain", size_bytes: 12, sha256: "abcdef",
      content_id: "att_1", delivery_mode: "direct",
    }],
  }, {display_name: "AHA 构建通知", from_address: "noreply@example.com"});
  assert.equal(data.title, "发送预览");
  assert.equal(data.fromName, "AHA 构建通知");
  assert.equal(data.fromAddress, "noreply@example.com");
  assert.equal(data.bodyHtml, "<p>HTML 正文</p>");
  assert.equal(data.bodyText, "纯文本回退");
  assert.equal(data.width, 600);
  assert.deepEqual(data.warnings, ["示例警告"]);

  const html = cards.mailPreviewDialogHtml(data);
  assert.match(html, /sandbox=""/, "HTML preview is sandboxed");
  assert.match(html, /srcdoc="[^"]*&lt;p&gt;HTML 正文&lt;\/p&gt;"/, "srcdoc carries the server HTML");
  assert.match(html, /纯文本回退/);
  assert.match(html, /AHA 构建通知/);
  assert.match(html, /noreply@example\.com/);
  assert.match(html, /qa@example\.com/);
  assert.match(html, /log\.txt/);
});

test("message detail preview renders the stored HTML snapshot in a sandbox", () => {
  const snapshot = message({
    from_name: "AHA 构建通知",
    body_text: "审批纯文本",
    body_html: "<p>审批 HTML 正文</p>",
  });
  const data = mailResources.messagePreviewData(snapshot, [{
    id: "mail_attachment_1", message_id: snapshot.id, position: 0,
    source_type: "task_attachment", source_id: "att_1", file_name: "log.txt",
    media_type: "text/plain", size_bytes: 12, sha256: "abcdef",
    delivery_mode: "direct", disposition: "attachment", created_at: "2026-11-06T09:00:00Z",
  }]);
  assert.equal(data.subject, snapshot.subject);
  assert.equal(data.fromAddress, snapshot.from_address);
  assert.equal(data.bodyHtml, snapshot.body_html);
  assert.equal(data.width, 600);

  const html = cards.mailPreviewDialogHtml(data);
  assert.match(html, /sandbox=""/);
  assert.match(html, /srcdoc="[^"]*&lt;p&gt;审批 HTML 正文&lt;\/p&gt;"/);
  assert.match(html, /审批纯文本/);
  assert.match(html, /log\.txt/);
});

test("task compose offers no-template direct fields and builds a direct payload", async () => {
  const source = await readFile(resolve(modulesDir, "mail_task.js"), "utf8");
  assert.match(source, /不使用模板/);
  assert.match(source, /mail-compose-direct/);
  assert.match(source, /正文（HTML 主编辑区）/);

  const common = ["attachment_1"];
  const artifacts = ["artifact_1"];
  const direct = mailTask.mailComposePayload({
    account_id: "mail_account_1", template_id: "  ", subject: "  直发主题  ",
    body_html: "<p>直发正文</p>", to: "qa@example.com, lead@example.com", cc: "", bcc: "",
  }, "task_1", {}, common, artifacts);
  assert.equal(direct.template_id, "");
  assert.equal(direct.subject, "直发主题");
  assert.equal(direct.body_html, "<p>直发正文</p>");
  assert.equal(direct.body_text, "");
  assert.deepEqual(direct.to, ["qa@example.com", "lead@example.com"]);
  assert.deepEqual(direct.attachment_ids, common);
  assert.deepEqual(direct.build_artifact_ids, artifacts);

  const templated = mailTask.mailComposePayload({
    account_id: "mail_account_1", template_id: "mail_template_1",
    subject: "ignored", body_html: "<p>ignored</p>", to: "qa@example.com",
  }, "task_1", {build_version: "1"}, [], []);
  assert.equal(templated.template_id, "mail_template_1");
  assert.ok(!("subject" in templated), "render template path never sends direct subject");
  assert.ok(!("body_html" in templated), "render template path never sends direct body");

  const guide = mailTask.mailComposePayload({
    account_id: "mail_account_1", template_id: "mail_template_guide",
    subject: "  最终主题  ", body_html: "<p>最终正文</p>", to: "qa@example.com",
  }, "task_1", {build_version: "ignored"}, [], [], "guide");
  assert.equal(guide.template_id, "mail_template_guide", "guide template id is recorded as reference");
  assert.equal(guide.subject, "最终主题");
  assert.equal(guide.body_html, "<p>最终正文</p>");
  assert.deepEqual(guide.variables, {}, "guide mode ignores caller variables");
});

test("template editor derives required variables and has no scope or project field", () => {
  const variables = cards.mailTemplateVariables(
    "{{build.version}} {{extra.build_no}}",
    "{{task.title}} {{test.scope}}",
    "<p>{{release.date}}</p>",
  );
  assert.deepEqual(variables, ["build.version", "extra.build_no", "release.date", "test.scope"]);

  const payload = mailResources.mailTemplateSubmitPayload({
    name: "提测通知",
    mode: "render",
    subject_template: "{{task.title}}",
    body_text_template: "{{test.scope}} 旧的自定义纯文本",
    body_html_template: "<p>{{build.version}}</p>",
    default_to: "qa@example.com, lead@example.com",
    default_cc: "",
    required_variables: "release.date",
  });
  assert.equal(payload.scope, "global");
  assert.equal(payload.name, "提测通知");
  assert.equal(payload.mode, "render");
  assert.equal(payload.body_text_template, "", "saving always clears the legacy custom text; the backend derives it from HTML");
  assert.deepEqual(payload.required_variables, ["build.version"], "variables in removed legacy text no longer count");
  assert.deepEqual(payload.default_to, ["qa@example.com", "lead@example.com"]);
  assert.ok(!("project_id" in payload), "the editor no longer submits a project");
  assert.equal(payload.description, "", "the description is submitted for guide instructions");

  const guidePayload = mailResources.mailTemplateSubmitPayload({
    name: "Guide 提测",
    subject_template: "[AHA] {{task.title}} 提测通知（示例）",
    body_html_template: "<p>{{task.title}} 示例正文</p>",
    description: "核对仓库列表与版本",
  });
  assert.equal(guidePayload.mode, "guide", "new templates default to guide");
  assert.deepEqual(guidePayload.required_variables, [], "guide mode never requires variables");
  assert.equal(guidePayload.description, "核对仓库列表与版本");

  const form = mailResources.templateForm();
  assert.doesNotMatch(form, /name="scope"/, "scope is not a user-editable field");
  assert.doesNotMatch(form, /name="project_id"/, "project is not a user-editable field");
  assert.doesNotMatch(form, /name="required_variables"/, "required variables are scanned, not typed");
  assert.match(form, /name="body_html_template"/, "the form still submits body_html_template");
  assert.doesNotMatch(form, /name="body_text_template"/, "custom plain text field was removed");
  assert.doesNotMatch(form, /高级：默认 CC 与自定义纯文本/, "advanced section is gone");
  assert.doesNotMatch(form, /mail-template-advanced/, "no collapsed advanced container remains");
  assert.match(form, /name="default_cc"/, "default CC lives in the main form");
  assert.match(form, /data-mail-template-preview-form/);
  assert.match(form, /contenteditable="true"/, "the primary editor is contenteditable");
  assert.match(form, /data-mail-template-editor/, "WYSIWYG editor hook exists");
  assert.doesNotMatch(form, /data-mail-template-live/, "the iframe live preview was replaced");
  assert.doesNotMatch(form, /mail-template-html-editor/, "the HTML textarea is no longer the main editor");
  assert.match(form, /data-mail-template-source-toggle/, "source mode is optional");
  assert.match(form, /name="mode"/, "guide/render mode is selectable");
  assert.match(form, /<option value="guide" selected>/, "new templates default to guide");
  assert.match(form, /<option value="render"/, "render remains available for legacy templates");
  assert.match(form, /name="description"/, "guide mode exposes filling instructions");
  assert.match(form, /data-mail-render-only[^>]*hidden/, "guide mode hides the variable bar");
  assert.match(form, /data-mail-guide-only/, "guide instructions are mode-scoped");
  assert.match(form, /value="3"[^>]*data-mail-table-rows/);
  assert.match(form, /value="3"[^>]*data-mail-table-columns/);
  assert.match(form, /data-mail-template-source[^>]*hidden/, "source textarea starts hidden behind the WYSIWYG editor");

  const legacy = mailResources.templateForm({
    id: "mail_template_legacy", scope: "project", project_id: "project_1", name: "旧模板",
    subject_template: "s", body_text_template: "", body_html_template: "", default_to: [], default_cc: [],
    required_variables: [], current_version: 1, status: "active", created_at: "", updated_at: "",
  });
  assert.match(legacy, /旧项目模板/);
  assert.doesNotMatch(legacy, /name="scope"/);
});

test("guide template preview shows the literal sample without variable substitution", async () => {
  const source = await readFile(resolve(modulesDir, "mail_resources.js"), "utf8");
  assert.match(source, /template\.mode === "guide"/, "guide templates use the literal preview path");
  assert.match(source, /preview_example: true/, "editor preview asks the server for the example");
  assert.match(source, /不做变量替换/, "the preview help states there is no substitution");
});

test("preview samples cover documented variables and the dialog exposes width, tabs and envelope", () => {
  const names = ["task.id", "task.title", "task.url", "project.name", "workspace.name", "build.version", "test.scope", "release.date", "extra.note"];
  const samples = cards.mailPreviewSamples(names);
  for (const name of names) assert.ok(samples[name], `${name} has a sample value`);
  assert.equal(samples["extra.note"], "示例-extra.note");

  const payload = cards.mailPreviewVariablePayload(names, samples);
  assert.equal(payload.sample_variables["task.title"], samples["task.title"]);
  assert.equal(payload.sample_variables["workspace.name"], samples["workspace.name"]);
  assert.equal(payload.variables["build.version"], samples["build.version"]);
  assert.equal(payload.variables["release.date"], samples["release.date"]);
  assert.deepEqual(payload.warnings, [], "built-in covered names need no warning");

  const unknown = cards.mailPreviewVariablePayload(["extra.unknown"]);
  assert.deepEqual(unknown.warnings, ["缺少示例变量：extra.unknown"]);
  assert.equal(unknown.variables["extra.unknown"], "示例-extra.unknown");

  const html = cards.mailPreviewDialogHtml({
    subject: "构建完成",
    fromName: "AHA",
    fromAddress: "noreply@example.com",
    to: ["qa@example.com"],
    cc: ["cc@example.com"],
    bodyText: "纯文本正文",
    bodyHtml: "<p>HTML 正文</p>",
    attachments: [],
    warnings: ["缺少示例变量：extra.unknown"],
    samples,
    onSampleRefresh: async () => ({subject: "x"}),
  });
  assert.match(html, /data-mail-preview-width="600"/);
  assert.match(html, /data-mail-preview-width="375"/);
  assert.match(html, /data-mail-preview-tab="html"/);
  assert.match(html, /data-mail-preview-tab="text"/);
  assert.match(html, /sandbox=""/, "HTML preview is sandboxed");
  assert.match(html, /noreply@example\.com/);
  assert.match(html, /qa@example\.com/);
  assert.match(html, /cc@example\.com/);
  assert.match(html, /缺少示例变量：extra\.unknown/);
  assert.match(html, /Outlook/);
  assert.match(html, /纯文本正文/);
  assert.match(html, /data-mail-sample-input="task\.id"/);
  assert.match(html, /data-mail-preview-refresh/);

  const empty = cards.mailPreviewDialogHtml({subject: "空正文", bodyText: "", bodyHtml: ""});
  assert.match(empty, /邮件没有 HTML 正文/);
  assert.match(empty, /邮件没有纯文本正文/);
});

test("pending card opens an approval preview with the snapshot and decision buttons", () => {
  const card = {
    message: message({body_text: "审批纯文本", body_html: "<p>审批 HTML 正文</p>", bcc: ["secret@example.com"]}),
    attachments: [{id: "mail_attachment_1", message_id: "mail_message_1", position: 0, source_type: "task_attachment", source_id: "att_1", file_name: "log.txt", media_type: "text/plain", size_bytes: 12, sha256: "abcdef", delivery_mode: "direct", disposition: "attachment", created_at: "2026-11-06T09:00:00Z"}],
    deliveries: [],
  };
  const cardHTML = cards.mailCardHtml(card);
  assert.match(cardHTML, /data-mail-action="review"/);
  assert.match(cardHTML, /查看并审批/);

  const dialog = cards.mailPreviewDialogHtml({
    title: "查看并审批",
    subject: card.message.subject,
    fromName: card.message.from_name,
    fromAddress: card.message.from_address,
    to: card.message.to,
    cc: card.message.cc,
    bcc: card.message.bcc,
    bodyText: card.message.body_text,
    bodyHtml: card.message.body_html,
    attachments: card.attachments,
    onApprove: async () => true,
    onReject: async () => true,
  });
  assert.match(dialog, /审批 HTML 正文/);
  assert.match(dialog, /审批纯文本/);
  assert.match(dialog, /data-mail-preview-decision="approve"/);
  assert.match(dialog, /data-mail-preview-decision="reject"/);
  assert.match(dialog, /log\.txt/);
  assert.match(dialog, /BCC/);
  assert.match(dialog, /sandbox=""/);
});

test("binding form grid keeps labels, selects and button on one baseline", async () => {
  const css = await readFile(resolve(modulesDir, "styles.css"), "utf8");
  const formRule = css.match(/\.task-mail-binding-form\s*\{([^}]*)\}/);
  assert.ok(formRule, "binding form rule exists");
  assert.match(formRule[1], /grid-template-columns:\s*minmax\(0,\s*1fr\)\s+minmax\(0,\s*1fr\)\s+auto/);
  assert.match(formRule[1], /min-width:\s*0/);
  const labelRule = css.match(/\.task-mail-binding-form label\s*\{([^}]*)\}/);
  assert.match(labelRule[1], /min-width:\s*0/);
  // Regression for the screenshot: the global label rule has margin-bottom:
  // 13px; without resetting it align-items:end pushed the button below the
  // selects instead of keeping one baseline.
  assert.match(labelRule[1], /margin:\s*0/);
  assert.doesNotMatch(labelRule[1], /margin-bottom/);
  const selectRule = css.match(/\.task-mail-binding-form select\s*\{([^}]*)\}/);
  assert.match(selectRule[1], /min-width:\s*0/);
  assert.match(selectRule[1], /height:\s*38px/, "select height is explicit");
  assert.match(selectRule[1], /text-overflow:\s*ellipsis/);
  const buttonRule = css.match(/\.task-mail-binding-form button\s*\{([^}]*)\}/);
  assert.match(buttonRule[1], /white-space:\s*nowrap/);
  assert.match(buttonRule[1], /height:\s*38px/, "button matches select height");
  assert.match(buttonRule[1], /min-height:\s*38px/);

  const container = css.match(/@container \(max-width: 520px\) \{([\s\S]*?)\n\}/);
  assert.ok(container, "narrow-container query exists (not only viewport media queries)");
  assert.match(container[1], /\.task-mail-binding-form\s*\{\s*grid-template-columns:\s*minmax\(0,\s*1fr\)/);
  assert.match(container[1], /\.task-mail-binding-form button\s*\{\s*width:\s*100%/);

  const narrow = css.match(/@media \(max-width: 560px\) \{([\s\S]*?)\n\}/);
  assert.ok(narrow, "560px viewport fallback exists");
  assert.match(narrow[1], /\.task-mail-binding-form\s*\{\s*grid-template-columns:\s*minmax\(0,\s*1fr\)/);
  assert.match(narrow[1], /\.task-mail-binding-form button\s*\{\s*width:\s*100%/);
});

test("template list hides archived templates until the toggle and badges legacy project scope", () => {
  const template = (overrides = {}) => ({
    id: "mail_template_active", scope: "global", project_id: "", name: "启用模板",
    subject_template: "s", body_text_template: "t", body_html_template: "", default_to: [], default_cc: [],
    required_variables: ["build.version"], current_version: 1, status: "active",
    created_at: "2026-11-06T09:00:00Z", updated_at: "2026-11-06T09:00:00Z", ...overrides,
  });
  const data = showArchived => ({
    subTab: "templates", accounts: [], messages: [], projects: [{id: "project_1", name: "AHA2"}],
    templates: [
      template(),
      template({id: "mail_template_legacy", scope: "project", project_id: "project_1", name: "旧项目", required_variables: []}),
      template({id: "mail_template_archived", name: "归档模板", status: "archived"}),
    ],
    filterStatus: "", filterAccount: "", showArchived, loading: false, error: "",
  });
  const daily = mailResources.renderMailResources(data(false));
  assert.match(daily, /显示归档模板/);
  assert.match(daily, /data-mail-template="mail_template_active"/);
  assert.match(daily, /data-mail-template="mail_template_legacy"/);
  assert.match(daily, /旧项目模板/);
  assert.doesNotMatch(daily, /data-mail-template="mail_template_archived"/, "archived templates stay out of the daily list");
  assert.doesNotMatch(daily, /已归档模板（/);

  const withArchived = mailResources.renderMailResources(data(true));
  assert.match(withArchived, /已归档模板（1）/);
  assert.match(withArchived, /data-mail-template="mail_template_archived"/);
  assert.ok(withArchived.indexOf("已归档模板（1）") > withArchived.indexOf("data-mail-template=\"mail_template_active\""), "archived section is separate and after active templates");
  assert.match(withArchived, /data-mail-template-purge="mail_template_active"/);
  assert.match(withArchived, /data-mail-template-purge="mail_template_archived"/);
});

test("template row keeps edit/preview visible and moves archive/purge into more", () => {
  const template = {
    id: "mail_template_active", scope: "global", project_id: "", name: "启用模板",
    subject_template: "s", body_text_template: "t", body_html_template: "", default_to: [], default_cc: [],
    required_variables: [], current_version: 1, status: "active",
    created_at: "2026-11-06T09:00:00Z", updated_at: "2026-11-06T09:00:00Z",
  };
  const html = mailResources.renderMailResources({
    subTab: "templates", accounts: [], messages: [], projects: [], templates: [template],
    filterStatus: "", filterAccount: "", showArchived: false, loading: false, error: "",
  });
  const rowStart = html.indexOf('data-mail-template="mail_template_active"');
  const row = html.slice(rowStart);
  const menuStart = row.indexOf('<details class="mail-row-more"');
  assert.ok(menuStart > 0, "low-frequency actions are in a more menu");
  const primary = row.slice(0, menuStart);
  const menu = row.slice(menuStart);
  for (const action of ["edit", "preview", "versions"]) {
    assert.match(primary, new RegExp(`data-mail-template-${action}="mail_template_active"`), `${action} stays a primary action`);
  }
  assert.doesNotMatch(primary, /data-mail-template-(?:archive|purge)=/, "archive/purge leave the primary bar");
  assert.match(menu, /data-mail-template-archive="mail_template_active"[^>]*>归档</);
  assert.match(menu, /data-mail-template-purge="mail_template_active"/);
  assert.match(menu, /mail-row-more-menu/);
});

test("mobile resource rows use a horizontal compact action bar", async () => {
  const css = await readFile(resolve(modulesDir, "styles.css"), "utf8");
  assert.doesNotMatch(css, /\.mail-resource-actions[^{]*\{[^}]*flex-direction:\s*column/, "resource actions never stack vertically");
  const mobile = [...css.matchAll(/@media \(max-width: 760px\) \{([\s\S]*?)\n\}/g)]
    .map(match => match[1])
    .filter(block => block.includes(".mail-resource-actions"))
    .at(-1);
  assert.ok(mobile, "a 760px media block owns the mail resource actions");
  const actionsRule = mobile.match(/\.mail-resource-actions\s*\{([^}]*)\}/);
  assert.ok(actionsRule, "mobile resource action rule exists");
  assert.match(actionsRule[1], /flex-direction:\s*row/);
  assert.match(actionsRule[1], /flex-wrap:\s*nowrap/);
  assert.match(actionsRule[1], /justify-content:\s*flex-end/);
  assert.match(actionsRule[1], /gap:\s*4px/);
  const compactButton = mobile.match(/\.mail-resource-actions > button\s*\{([^}]*)\}/);
  assert.match(compactButton[1], /min-height:\s*28px/);
  const iconButton = mobile.match(/\.mail-resource-actions \.icon-button\s*\{([^}]*)\}/);
  assert.match(iconButton[1], /width:\s*28px/);
  assert.match(css, /\.mail-row-more-menu\s*\{[^}]*position:\s*absolute/, "more menu does not widen the row");
});

test("opening the preview dialog mounts envelope markup before showModal", () => {
  // Minimal DOM stub: the regression was that openMailPreviewDialog called
  // bindPreviewDialog/showModal on an empty <dialog>. This asserts the initial
  // markup is actually assigned first, instead of matching source strings.
  const events = [];
  class FakeDialog {
    constructor() { this.className = ""; this._html = ""; this.open = false; }
    set innerHTML(value) { this._html = value; events.push("innerHTML"); }
    get innerHTML() { return this._html; }
    addEventListener() {}
    querySelectorAll() { return []; }
    querySelector() { return null; }
    close() {}
    remove() {}
    showModal() { events.push("showModal"); this.open = true; }
  }
  const appended = [];
  globalThis.document = {
    createElement(tag) { assert.equal(tag, "dialog"); return new FakeDialog(); },
    body: { appendChild(element) { appended.push(element); events.push("appendChild"); } },
  };
  try {
    const dialog = cards.openMailPreviewDialog({
      title: "发送预览",
      subject: "构建完成",
      fromName: "AHA",
      fromAddress: "noreply@example.com",
      to: ["qa@example.com"],
      bodyText: "纯文本回退",
      bodyHtml: "<p>HTML 正文</p>",
      attachments: [],
    });
    assert.equal(appended.length, 1, "dialog is attached to the body");
    assert.deepEqual(events.slice(-3), ["innerHTML", "appendChild", "showModal"], "initial markup is mounted before the dialog is shown");
    assert.equal(dialog.open, true);
    assert.match(dialog.innerHTML, /mail-preview-envelope/);
    assert.match(dialog.innerHTML, /构建完成/);
    assert.match(dialog.innerHTML, /qa@example\.com/);
    assert.match(dialog.innerHTML, /sandbox=""/);
    assert.match(dialog.innerHTML, /data-mail-preview-width="600"/);
    assert.match(dialog.innerHTML, /data-mail-preview-tab="text"/);
    assert.match(dialog.innerHTML, /HTML 正文/);
  } finally {
    delete globalThis.document;
  }
});

test("compose default template is exactly the account binding template", () => {
  const templates = [
    {id: "mail_template_first", name: "第一个模板"},
    {id: "mail_template_bound", name: "绑定模板"},
  ];
  assert.equal(
    mailTask.mailComposeTemplateSelection({account_id: "a1", default_template_id: "mail_template_bound"}, templates),
    "mail_template_bound",
    "uses the binding default even when it is not the first template",
  );
  assert.equal(
    mailTask.mailComposeTemplateSelection({account_id: "a1", default_template_id: ""}, templates),
    "",
    "an unset binding selects 不使用模板 instead of the first template",
  );
  assert.equal(
    mailTask.mailComposeTemplateSelection({account_id: "a1"}, templates),
    "",
    "a missing default_template_id also selects 不使用模板",
  );
  assert.equal(
    mailTask.mailComposeTemplateSelection({account_id: "a1", default_template_id: "mail_template_missing"}, templates),
    "",
    "a stale binding id never selects an unrelated template",
  );
  assert.equal(
    mailTask.mailComposeTemplateSelection(undefined, templates),
    "",
    "no selected account falls back to 不使用模板",
  );
});

test("attachment picker selection maps to the submitted payload ids", () => {
  const selected = mailTask.mailComposeAttachments(
    [{value: "att_1", checked: true}, {value: "att_2", checked: false}, {value: "att_3", checked: true}],
    [{value: "artifact_1", checked: false}, {value: "artifact_2", checked: true}],
  );
  assert.deepEqual(selected.attachment_ids, ["att_1", "att_3"], "unchecked Task attachments never enter the payload");
  assert.deepEqual(selected.build_artifact_ids, ["artifact_2"], "build artifacts stay in their own payload field");

  const payload = mailTask.mailComposePayload(
    {account_id: "a1", template_id: "t1", to: "qa@example.com"},
    "task_1", {}, selected.attachment_ids, selected.build_artifact_ids,
  );
  assert.deepEqual(payload.attachment_ids, ["att_1", "att_3"]);
  assert.deepEqual(payload.build_artifact_ids, ["artifact_2"]);

  const composeSource = readFileSync(resolve(modulesDir, "mail_task.js"), "utf8");
  assert.match(composeSource, /data-mail-attachment-chips/, "selected attachments render as chips");
  assert.match(composeSource, /data-mail-attachment-picker/, "one compact picker replaces the two stacked lists");
  assert.match(composeSource, /data-mail-picker-pane="task"/);
  assert.match(composeSource, /data-mail-picker-pane="build"/);
  assert.match(composeSource, /构建产物只来自当前 Task 已登记/, "the build-artifact source and selection rule is explained");
  assert.match(composeSource, /data-mail-selected-total/, "selected count stays visible");
});

test("compose dialog close, cancel and successful submit all close the dialog", async () => {
  // The compose rewrite once dropped its local close()/listener block, letting
  // `close` resolve to window.close. This DOM stub drives the real dialog
  // bindings: X/cancel must call dialog.close+remove, and a successful submit
  // must close the dialog instead of leaving it open for a duplicate submit.
  class FakeElement {
    constructor() {
      this.value = "";
      this.textContent = "";
      this.innerHTML = "";
      this.hidden = false;
      this.disabled = false;
      this.required = false;
      this.checked = false;
      this.isConnected = true;
      this.dataset = {};
      this.selectedOptions = [{value: "mail_account_1", dataset: {}}];
      this.classList = {toggle() {}};
      this._listeners = {};
      this._query = new Map();
      this._queryAll = new Map();
    }
    addEventListener(type, handler) { (this._listeners[type] ||= []).push(handler); }
    dispatch(type, event = {}) {
      for (const handler of this._listeners[type] || []) {
        handler({target: this, currentTarget: this, preventDefault() {}, ...event});
      }
    }
    setAttribute() {}
    removeAttribute() {}
    replaceChildren() {}
    append() {}
    appendChild(child) { return child; }
    querySelector(selector) { return this._query.get(selector) ?? new FakeElement(); }
    querySelectorAll(selector) { return this._queryAll.get(selector) ?? []; }
    closest() { return null; }
    focus() {}
    close() { this.closeCalls = (this.closeCalls || 0) + 1; this.open = false; }
    remove() { this.removeCalls = (this.removeCalls || 0) + 1; this.isConnected = false; }
    showModal() { this.open = true; }
  }

  const makeTree = () => {
    const form = new FakeElement();
    const elements = {
      templateSelect: new FakeElement(), accountSelect: new FakeElement(),
      variablesBox: new FakeElement(), directBox: new FakeElement(),
      picker: new FakeElement(), pickerToggle: new FakeElement(),
      chips: new FakeElement(), total: new FakeElement(), hint: new FakeElement(),
      to: new FakeElement(), cc: new FakeElement(), submit: new FakeElement(),
      preview: new FakeElement(),
    };
    form._query.set('select[name="template_id"]', elements.templateSelect);
    form._query.set('select[name="account_id"]', elements.accountSelect);
    form._query.set("#mail-compose-variables", elements.variablesBox);
    form._query.set("#mail-compose-direct", elements.directBox);
    form._query.set("[data-mail-attachment-picker]", elements.picker);
    form._query.set("#mail-compose-attachment-toggle", elements.pickerToggle);
    form._query.set("[data-mail-attachment-chips]", elements.chips);
    form._query.set("[data-mail-selected-total]", elements.total);
    form._query.set("[data-mail-template-hint]", elements.hint);
    form._query.set('input[name="to"]', elements.to);
    form._query.set('input[name="cc"]', elements.cc);
    form._query.set('button[type="submit"]', elements.submit);
    form._query.set("#mail-compose-preview-button", elements.preview);
    form._queryAll.set('input[name="attachment_ids"]', []);
    form._queryAll.set('input[name="build_artifact_ids"]', []);
    form._queryAll.set("[data-mail-variable-input]", []);
    form._queryAll.set("[data-mail-picker-tab]", []);
    form._queryAll.set("[data-mail-picker-pane]", []);
    const dialog = new FakeElement();
    dialog.closeButtons = [new FakeElement(), new FakeElement()];
    dialog._query.set("#task-mail-compose-form", form);
    dialog._queryAll.set("[data-close]", dialog.closeButtons);
    return {dialog, form, elements};
  };

  const dialogs = [];
  let lastTree;
  globalThis.document = {
    querySelector(selector) {
      if (selector !== "#task-mail-compose-dialog") return null;
      const last = dialogs[dialogs.length - 1];
      return last && last.isConnected ? last : null;
    },
    createElement(tag) {
      if (tag === "dialog") {
        lastTree = makeTree();
        dialogs.push(lastTree.dialog);
        return lastTree.dialog;
      }
      return new FakeElement();
    },
    body: {appendChild() {}},
  };
  const savedFormData = globalThis.FormData;
  const savedInput = globalThis.HTMLInputElement;
  const savedWindow = globalThis.window;
  globalThis.FormData = class {
    get(name) {
      return {
        account_id: "mail_account_1", template_id: "mail_template_1", to: "qa@example.com",
        cc: "", bcc: "", subject: "", body_html: "",
      }[name] ?? "";
    }
    getAll() { return []; }
  };
  globalThis.HTMLInputElement = class {};
  globalThis.window = {confirm: () => true, prompt: () => null};
  const api = apiModule.api;
  const originalCreateMailMessage = api.createMailMessage;
  const submitted = [];
  api.createMailMessage = async payload => { submitted.push(payload); return {ok: true}; };
  const detail = {task: {id: "task_1", status: "active"}};
  const options = {
    ok: true, task_id: "task_1",
    accounts: [{
      account_id: "mail_account_1", binding_id: "mail_binding_1", binding_scope: "task",
      from_address: "noreply@example.com", display_name: "AHA", aliases: [],
      credential_configured: true, status: "ok", default_template_id: "mail_template_1",
    }],
    templates: [{
      id: "mail_template_1", name: "提测模板", scope: "global", subject_template: "提测",
      body_text_template: "", body_html_template: "<p>正文</p>", default_to: ["qa@example.com"],
      default_cc: [], required_variables: [], current_version: 1, status: "active",
      created_at: "", updated_at: "",
    }],
    attachments: [], build_artifacts: [], messages: [],
  };
  const actions = {reload: async () => {}, notify: () => {}};
  try {
    // X / 取消 button.
    await mailTask.openTaskMailCompose(detail, options, actions);
    const first = lastTree;
    assert.equal(first.dialog.open, true, "compose dialog is shown");
    first.dialog.closeButtons[0].dispatch("click");
    assert.equal(first.dialog.closeCalls, 1, "data-close click calls dialog.close");
    assert.equal(first.dialog.removeCalls, 1, "data-close click removes the dialog");

    // Escape / cancel event.
    await mailTask.openTaskMailCompose(detail, options, actions);
    const second = lastTree;
    assert.equal(typeof second.dialog._listeners.cancel?.[0], "function", "cancel handler is bound");
    second.dialog.dispatch("cancel");
    assert.equal(second.dialog.closeCalls, 1, "cancel calls dialog.close");
    assert.equal(second.dialog.removeCalls, 1, "cancel removes the dialog");

    // Successful submit.
    await mailTask.openTaskMailCompose(detail, options, actions);
    const third = lastTree;
    third.form.dispatch("submit");
    await new Promise(resolvePromise => setImmediate(resolvePromise));
    assert.equal(submitted.length, 1, "submit creates exactly one mail message");
    assert.equal(submitted[0].submit, true);
    assert.equal(third.dialog.closeCalls, 1, "successful submit closes the dialog");
    assert.equal(third.dialog.removeCalls, 1, "successful submit removes the dialog");
  } finally {
    api.createMailMessage = originalCreateMailMessage;
    globalThis.FormData = savedFormData;
    globalThis.HTMLInputElement = savedInput;
    globalThis.window = savedWindow;
    delete globalThis.document;
  }
});

test("resources page keeps hardware and mail in one eight-item navigation shell", () => {
  const hardware = resources.renderResourcesPage({
    tab: "hardware",
    hardware: [],
    mail: {subTab: "accounts", accounts: [], templates: [], messages: [], projects: [], filterStatus: "", filterAccount: "", showArchived: false, loading: false, error: ""},
  });
  assert.match(hardware, /class="resources-view"/);
  assert.match(hardware, /data-resources-tab="hardware"/);
  assert.match(hardware, /data-resources-tab="mail"/);
  assert.match(hardware, /hardware-resources-view/);
  assert.match(hardware, /资源/);

  const mail = resources.renderResourcesPage({
    tab: "mail",
    hardware: [],
    mail: {subTab: "accounts", accounts: [], templates: [], messages: [], projects: [], filterStatus: "", filterAccount: "", showArchived: false, loading: false, error: ""},
  });
  assert.match(mail, /mail-resources-view/);
  assert.match(mail, /data-mail-subtab="accounts"/);
  assert.match(mail, /添加账号/);
  assert.doesNotMatch(mail, /credential_ref/);
});
