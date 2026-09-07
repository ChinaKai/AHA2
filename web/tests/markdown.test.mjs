import test from "node:test";
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "..");

async function markdownModule() {
  return import(pathToFileURL(resolve(root, "dist", "markdown.js")));
}

test("markdown renders the supported block and inline syntax", async () => {
  const {renderMarkdown} = await markdownModule();
  const html = renderMarkdown(`# Heading

- one
- two with **bold** and \`inline\`

> quoted

\`\`\`js
const answer = 42 < 50;
\`\`\`

| Name | Value |
| :--- | ---: |
| alpha | [docs](https://example.com/docs) |

![diagram](/assets/diagram.png)`);
  assert.match(html, /<h1>Heading<\/h1>/);
  assert.match(html, /<ul><li>one<\/li><li>two with <strong>bold<\/strong> and <code>inline<\/code><\/li><\/ul>/);
  assert.match(html, /<blockquote><p>quoted<\/p><\/blockquote>/);
  assert.match(html, /<pre><code class="language-js">const answer = 42 &lt; 50;<\/code><\/pre>/);
  assert.match(html, /<table>/);
  assert.match(html, /text-align:left/);
  assert.match(html, /text-align:right/);
  assert.match(html, /href="https:\/\/example\.com\/docs"[^>]*rel="noopener noreferrer"/);
  assert.match(html, /<img src="\/assets\/diagram\.png" alt="diagram"[^>]*loading="lazy"/);
});

test("markdown escapes raw HTML and rejects active URL schemes", async () => {
  const {renderMarkdown, sanitizeMarkdownURL} = await markdownModule();
  const html = renderMarkdown(`<img src=x onerror=alert(1)>

[script](javascript:alert(1))
[data](data:text/html,boom)
[control](java\tscript:alert(1))
![payload](data:image/svg+xml,boom)
[safe](/tasks/1)`);
  assert.match(html, /&lt;img src=x onerror=alert\(1\)&gt;/);
  assert.doesNotMatch(html, /href="(?:javascript|data):|src="data:/i);
  assert.match(html, /<a href="\/tasks\/1"/);
  assert.equal(sanitizeMarkdownURL("HTTPS://example.com"), "HTTPS://example.com");
  assert.equal(sanitizeMarkdownURL("mailto:hello@example.com"), "mailto:hello@example.com");
  assert.equal(sanitizeMarkdownURL("javascript:alert(1)"), null);
  assert.equal(sanitizeMarkdownURL("data:text/html,boom"), null);
  assert.equal(sanitizeMarkdownURL("java\nscript:alert(1)"), null);
  assert.equal(sanitizeMarkdownURL("mailto:hello@example.com", true), null);
  assert.equal(sanitizeMarkdownURL("data:image/png;base64,iVBORw0KGgo=", true), "data:image/png;base64,iVBORw0KGgo=");
  assert.equal(sanitizeMarkdownURL("data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=", true), "data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=");
  assert.equal(sanitizeMarkdownURL("data:image/svg+xml;base64,PHN2ZyBvbmxvYWQ9ImFsZXJ0KDEpIj48L3N2Zz4=", true), null);
  assert.equal(sanitizeMarkdownURL("data:text/html;base64,PGgxPmJvb208L2gxPg==", true), null);
  assert.equal(sanitizeMarkdownURL("data:image/svg+xml,%3Csvg%3E", true), null);
  assert.match(renderMarkdown("![embedded](data:image/png;base64,iVBORw0KGgo=)"), /<img src="data:image\/png;base64,iVBORw0KGgo=" alt="embedded"/);
});

test("conversation preview and full message share the safe markdown renderer", async () => {
  const {renderConversationList} = await import(pathToFileURL(resolve(root, "dist", "conversation_ui.js")));
  const summary = `# Preview\n\n[unsafe](javascript:alert(1)) ${"long ".repeat(190)}`;
  const html = renderConversationList([{
    id: "message-1",
    task_id: "task-1",
    agent_id: "main",
    stream_agent_id: "main",
    category: "chat",
    kind: "agent_message",
    summary,
    created_at: "2026-09-05T00:00:00Z",
  }], null, false);
  assert.equal((html.match(/class="message-text markdown-body/g) || []).length, 2);
  assert.equal((html.match(/<h1>Preview/g) || []).length, 2);
  assert.match(html, /class="message-text markdown-body message-preview"/);
  assert.match(html, /class="message-text markdown-body message-full-text" hidden/);
  assert.match(html, /data-copy-message-source="# Preview\n\n\[unsafe\]\(javascript:alert\(1\)\)/);
  assert.doesNotMatch(html, /href="javascript:/i);
  assert.match(html, /data-toggle-message/);
});

test("built conversation module uses the versioned markdown module", async () => {
  const conversation = await readFile(resolve(root, "dist", "conversation_ui.js"), "utf8");
  const markdown = await readFile(resolve(root, "dist", "markdown.js"), "utf8");
  assert.match(conversation, /\.\/markdown\.js\?v=[a-f0-9]{12}/);
  assert.match(markdown, /function renderMarkdown/);
});
