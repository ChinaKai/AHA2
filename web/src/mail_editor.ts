// Mail template WYSIWYG helpers.
//
// The normalizer below intentionally mirrors only the safe subset enforced by
// internal/mail/sanitize.go. It is an editing convenience: the server sanitizer
// remains the final authority and must never be relaxed to match this code.
//
// Keeping it DOM-free means the exact same transformation runs in Node tests,
// the source-mode textarea and the contenteditable save path.

type EditorToken =
  | {kind: "text"; value: string}
  | {kind: "start"; tag: string; attrs: Array<[string, string]>; selfClosing: boolean}
  | {kind: "end"; tag: string};

type EditorNode =
  | {kind: "text"; value: string}
  | {kind: "element"; tag: string; attrs: Array<[string, string]>; children: EditorNode[]};

const ALLOWED_TAGS = new Set([
  "h1", "h2", "h3", "h4", "h5", "h6",
  "p", "br", "hr",
  "strong", "b", "em", "i", "u", "s",
  "ul", "ol", "li",
  "blockquote", "code", "pre",
  "table", "thead", "tbody", "tfoot", "tr", "th", "td", "caption",
  "a", "img",
]);

const VOID_TAGS = new Set(["br", "hr", "img"]);

// Elements whose entire subtree is discarded. Pasted markup is intercepted,
// but contenteditable can still synthesize some of these.
const DROP_WITH_CONTENT = new Set([
  "script", "style", "iframe", "object", "embed", "svg", "math", "template",
  "noscript", "form", "input", "button", "select", "option", "textarea",
  "title", "head", "meta", "link", "base", "video", "audio", "canvas",
  "source", "track", "map", "area",
]);

// Block-level tags: a wrapper (div/p) containing one must be unwrapped instead
// of nesting blocks inside a paragraph.
const BLOCK_TAGS = new Set([
  "h1", "h2", "h3", "h4", "h5", "h6", "p", "hr",
  "ul", "ol", "li", "blockquote", "pre",
  "table", "thead", "tbody", "tfoot", "tr", "th", "td", "caption",
]);

// Whitespace-only text directly inside these containers is layout noise, not
// meaningful inline spacing.
const DROP_WHITESPACE_PARENTS = new Set(["", "table", "thead", "tbody", "tfoot", "tr", "ul", "ol"]);

// contenteditable vendors (and pasted markup) introduce these; they are
// unwrapped so only sanitizer-allowed tags survive.
const UNWRAP_TAGS = new Set([
  "span", "font", "label", "small", "big", "mark", "sub", "sup", "ins",
  "tt", "kbd", "samp", "var", "abbr", "cite", "q", "time", "address",
  "figure", "figcaption", "center", "nobr", "section", "article", "header",
  "footer", "main", "aside", "nav", "details", "summary", "fieldset", "legend",
]);

const STYLE_PROPERTIES = new Set([
  "border", "border-collapse", "border-spacing",
  "border-color", "border-style", "border-width",
  "border-top", "border-right", "border-bottom", "border-left",
  "padding", "padding-top", "padding-right", "padding-bottom", "padding-left",
  "text-align", "vertical-align",
  "width", "min-width", "max-width",
  "height", "min-height", "max-height",
  "background-color", "color",
  "font-weight", "font-size", "line-height", "white-space",
]);

// Mirror of internal/mail/sanitize.go tableStyleForbiddenValueFragments.
const STYLE_FORBIDDEN_FRAGMENTS = [
  "url(", "expression", "@import", "!important", "javascript:", "behavior",
  "-moz-binding", "position", "display",
];

const CID_PATTERN = /^cid:[A-Za-z0-9][A-Za-z0-9_.@-]{0,127}$/i;
const PLACEHOLDER_PATTERN = /\{\{([a-zA-Z0-9_.-]+)\}\}/g;
const PLACEHOLDER_ONLY_PATTERN = /^\{\{[a-zA-Z0-9_.-]+\}\}$/;

// Tags that are dropped together with their content but have no closing tag.
// Everything else in DROP_WITH_CONTENT is treated as a raw container even when
// the source writes `<script/>`, matching the HTML parser instead of tokenizer
// self-closing rules.
const DROP_VOID_TAGS = new Set(["input", "meta", "link", "base", "source", "track", "area", "embed"]);

function escapeHTML(value: unknown): string {
  return String(value ?? "")
    .replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;").replaceAll("'", "&#039;");
}

function escapeText(value: string): string {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll("\u00a0", "&nbsp;");
}

function escapeAttr(value: string): string {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
}

function decodeEntities(value: string): string {
  const named: Record<string, string> = {
    amp: "&", lt: "<", gt: ">", quot: '"', apos: "'", nbsp: "\u00a0",
  };
  return value.replace(/&(#x[0-9a-f]+|#[0-9]+|[a-z][a-z0-9]*);/gi, (match, body: string) => {
    const lower = body.toLowerCase();
    if (lower.startsWith("#x")) {
      const code = Number.parseInt(body.slice(2), 16);
      return validCodePoint(code) ? String.fromCodePoint(code) : match;
    }
    if (lower.startsWith("#")) {
      const code = Number.parseInt(body.slice(1), 10);
      return validCodePoint(code) ? String.fromCodePoint(code) : match;
    }
    return named[lower] ?? match;
  });
}

function validCodePoint(code: number): boolean {
  return Number.isInteger(code) && code > 0 && code <= 0x10ffff && !(code >= 0xd800 && code <= 0xdfff);
}

function parseAttributes(raw: string): Array<[string, string]> {
  const attrs: Array<[string, string]> = [];
  const pattern = /([:@a-zA-Z_][-:@a-zA-Z0-9_.]*)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>`]+)))?/g;
  let match: RegExpExecArray | null;
  while ((match = pattern.exec(raw))) {
    attrs.push([match[1].toLowerCase(), decodeEntities(match[2] ?? match[3] ?? match[4] ?? "")]);
  }
  return attrs;
}

function tokenizeHTML(input: string): EditorToken[] {
  const tokens: EditorToken[] = [];
  let index = 0;
  while (index < input.length) {
    if (input[index] !== "<") {
      const next = input.indexOf("<", index);
      const end = next < 0 ? input.length : next;
      tokens.push({kind: "text", value: input.slice(index, end)});
      index = end;
      continue;
    }
    if (input.startsWith("<!--", index)) {
      const end = input.indexOf("-->", index + 4);
      index = end < 0 ? input.length : end + 3;
      continue;
    }
    if (/^<![^>]*>/.test(input.slice(index))) {
      const end = input.indexOf(">", index + 2);
      index = end < 0 ? input.length : end + 1;
      continue;
    }
    let cursor = index + 1;
    let closing = false;
    if (input[cursor] === "/") { closing = true; cursor++; }
    while (cursor < input.length && /\s/.test(input[cursor])) cursor++;
    const nameStart = cursor;
    while (cursor < input.length && /[A-Za-z0-9:-]/.test(input[cursor])) cursor++;
    const tag = input.slice(nameStart, cursor).toLowerCase();
    if (!tag) {
      tokens.push({kind: "text", value: "<"});
      index += 1;
      continue;
    }
    // Find the closing > while respecting quoted attribute values.
    let quote = "";
    let end = cursor;
    for (; end < input.length; end++) {
      const character = input[end];
      if (quote) {
        if (character === quote) quote = "";
        continue;
      }
      if (character === '"' || character === "'") { quote = character; continue; }
      if (character === ">") break;
    }
    if (end >= input.length) {
      tokens.push({kind: "text", value: input.slice(index)});
      break;
    }
    const rawAttrs = input.slice(cursor, end);
    const selfClosing = /\/\s*$/.test(rawAttrs);
    if (closing) {
      tokens.push({kind: "end", tag});
    } else {
      tokens.push({
        kind: "start",
        tag,
        attrs: parseAttributes(rawAttrs.replace(/\/\s*$/, "")),
        selfClosing,
      });
    }
    index = end + 1;
  }
  return tokens;
}

function buildTree(tokens: EditorToken[]): EditorNode[] {
  const root: EditorNode[] = [];
  const stack: Array<{tag: string; children: EditorNode[]}> = [{tag: "", children: root}];
  let skipTag = "";
  let skipDepth = 0;
  for (const token of tokens) {
    if (skipTag) {
      if (token.kind === "start" && token.tag === skipTag && !token.selfClosing) skipDepth++;
      else if (token.kind === "end" && token.tag === skipTag && --skipDepth <= 0) skipTag = "";
      continue;
    }
    if (token.kind === "start" && DROP_WITH_CONTENT.has(token.tag)) {
      if (!DROP_VOID_TAGS.has(token.tag)) { skipTag = token.tag; skipDepth = 1; }
      continue;
    }
    const parent = stack[stack.length - 1];
    if (token.kind === "text") {
      parent.children.push({kind: "text", value: decodeEntities(token.value)});
    } else if (token.kind === "start") {
      const seen = new Set<string>();
      const attrs = token.attrs.filter(([name]) => !seen.has(name) && Boolean(seen.add(name)));
      const node: EditorNode = {kind: "element", tag: token.tag, attrs, children: []};
      parent.children.push(node);
      if (!VOID_TAGS.has(token.tag) && !token.selfClosing) {
        stack.push({tag: token.tag, children: node.children});
      }
    } else {
      // Close the nearest matching element; stray end tags are ignored.
      for (let depth = stack.length - 1; depth > 0; depth--) {
        if (stack[depth].tag === token.tag) {
          stack.length = depth;
          break;
        }
      }
    }
  }
  return root;
}

function normalizeText(value: string, parentTag: string): string {
  const decoded = value.replaceAll("\u00a0", " ");
  if (parentTag === "pre") return decoded.replace(/\r\n?/g, "\n");
  return decoded.replace(/[ \t\r\n\f]+/g, " ");
}

function elementNode(tag: string, children: EditorNode[] = [], attrs: Array<[string, string]> = []): EditorNode {
  return {kind: "element", tag, attrs, children};
}

function isEmptyContent(nodes: EditorNode[]): boolean {
  return nodes.every(node => node.kind === "text" && node.value.trim() === "");
}

function isBlockNode(node: EditorNode): boolean {
  return node.kind === "element" && BLOCK_TAGS.has(node.tag);
}

function mergeText(nodes: EditorNode[]): EditorNode[] {
  const merged: EditorNode[] = [];
  for (const node of nodes) {
    const previous = merged[merged.length - 1];
    if (node.kind === "text" && previous?.kind === "text") {
      merged[merged.length - 1] = {kind: "text", value: previous.value + node.value};
    } else {
      merged.push(node);
    }
  }
  return merged;
}

function trimEdges(nodes: EditorNode[]): EditorNode[] {
  const trimmed = nodes.map(node => node.kind === "text" ? {kind: "text" as const, value: node.value} : node);
  const first = trimmed[0];
  if (first?.kind === "text") first.value = first.value.replace(/^ /, "");
  const last = trimmed[trimmed.length - 1];
  if (last?.kind === "text") last.value = last.value.replace(/ $/, "");
  return trimmed.filter(node => node.kind !== "text" || node.value !== "");
}

function normalizeChildren(children: EditorNode[], parentTag: string): EditorNode[] {
  const normalized: EditorNode[] = [];
  for (const child of children) {
    if (child.kind === "text") {
      const text = normalizeText(child.value, parentTag);
      if (text === "") continue;
      if (text.trim() === "" && DROP_WHITESPACE_PARENTS.has(parentTag)) continue;
      normalized.push({kind: "text", value: text});
    } else {
      normalized.push(...normalizeElement(child, parentTag));
    }
  }
  return mergeText(normalized);
}

function cleanStyle(value: string): string {
  const declarations: string[] = [];
  for (const rawDeclaration of String(value || "").split(";")) {
    const declaration = rawDeclaration.trim();
    if (!declaration) continue;
    const separator = declaration.indexOf(":");
    if (separator < 0) continue;
    const property = declaration.slice(0, separator).trim().toLowerCase();
    const rawValue = declaration.slice(separator + 1).trim();
    if (!STYLE_PROPERTIES.has(property) || !rawValue) continue;
    if (!/^[\x20-\x7e]+$/.test(rawValue)) continue;
    const lowered = rawValue.toLowerCase();
    if (STYLE_FORBIDDEN_FRAGMENTS.some(fragment => lowered.includes(fragment))) continue;
    // Same punctuation rejection as the Go validator.
    if (/[!()\\"'<>{};/:]/.test(rawValue)) continue;
    declarations.push(`${property}: ${rawValue}`);
  }
  return declarations.join("; ");
}

function attribute(node: EditorNode & {kind: "element"}, name: string): string {
  return node.attrs.find(([key]) => key === name)?.[1] ?? "";
}

function safeLinkHref(value: string): string {
  if (!value || value !== value.trim()) return "";
  // A bare {{task.url}} is validated by the server after substitution, and
  // placeholders inside https/mailto links are allowed there too.
  const substituted = value.replace(PLACEHOLDER_PATTERN, "x");
  if (substituted.includes("{{") || substituted.includes("}}")) return "";
  if (PLACEHOLDER_ONLY_PATTERN.test(value)) return value;
  if (!/^(https:\/\/|mailto:)/i.test(substituted)) return "";
  if (/[\s<>"'`\\]/.test(substituted)) return "";
  if (substituted.toLowerCase().includes("javascript:")) return "";
  return value;
}

function safeImageSrc(value: string): boolean {
  const substituted = value.replace(PLACEHOLDER_PATTERN, "x");
  if (substituted.includes("{{") || substituted.includes("}}")) return false;
  return CID_PATTERN.test(substituted);
}

function normalizeElement(node: EditorNode & {kind: "element"}, parentTag: string): EditorNode[] {
  const tag = node.tag;
  if (tag === "strike" || tag === "del" || tag === "st") {
    return [elementNode("s", trimEdges(normalizeChildren(node.children, "s")))];
  }
  if (tag === "div") {
    const children = trimEdges(normalizeChildren(node.children, "div"));
    if (!children.length || isEmptyContent(children)) return [elementNode("p", [elementNode("br")])];
    if (children.some(isBlockNode)) return children;
    return [elementNode("p", children)];
  }
  if (tag === "p") {
    const children = trimEdges(normalizeChildren(node.children, "p"));
    if (!children.length || isEmptyContent(children)) return [elementNode("p", [elementNode("br")])];
    if (children.some(isBlockNode)) return children;
    return [elementNode("p", children)];
  }
  if (UNWRAP_TAGS.has(tag)) {
    return normalizeChildren(node.children, parentTag);
  }
  if (!ALLOWED_TAGS.has(tag)) {
    return normalizeChildren(node.children, parentTag === "" ? "" : tag);
  }
  if (VOID_TAGS.has(tag)) {
    if (tag === "img") {
      const src = attribute(node, "src");
      if (!safeImageSrc(src)) return [];
      const attrs: Array<[string, string]> = [["src", src]];
      const alt = attribute(node, "alt");
      // The server rejects placeholders in every attribute except href/src.
      if (alt && !alt.includes("{{")) attrs.push(["alt", alt]);
      for (const dimension of ["width", "height"]) {
        const value = attribute(node, dimension).trim();
        if (/^[0-9]{1,5}$/.test(value)) attrs.push([dimension, value]);
      }
      return [elementNode("img", [], attrs)];
    }
    return [elementNode(tag)];
  }
  const children = normalizeChildren(node.children, tag);
  if (tag === "table" || tag === "tr") {
    const style = cleanStyle(attribute(node, "style"));
    return [elementNode(tag, trimEdges(children), style ? [["style", style]] : [])];
  }
  if (tag === "td" || tag === "th") {
    const attrs: Array<[string, string]> = [];
    for (const dimension of ["colspan", "rowspan"]) {
      const value = attribute(node, dimension).trim();
      if (/^[1-9][0-9]{0,2}$/.test(value)) attrs.push([dimension, value]);
    }
    const style = cleanStyle(attribute(node, "style"));
    if (style) attrs.push(["style", style]);
    const content = trimEdges(children);
    if (!content.length || isEmptyContent(content)) {
      // Keep one non-breaking space so an empty cell keeps its visible box.
      return [elementNode(tag, [{kind: "text", value: "\u00a0"}], attrs)];
    }
    return [elementNode(tag, content, attrs)];
  }
  if (tag === "a") {
    const attrs: Array<[string, string]> = [];
    const href = safeLinkHref(attribute(node, "href"));
    if (href) attrs.push(["href", href]);
    const title = attribute(node, "title");
    if (title && !title.includes("{{")) attrs.push(["title", title]);
    return [elementNode("a", normalizeChildren(node.children, "a"), attrs)];
  }
  if (tag === "li" || tag === "blockquote") {
    const content = trimEdges(children);
    if (!content.length || isEmptyContent(content)) return [elementNode(tag, [elementNode("br")])];
    return [elementNode(tag, content)];
  }
  if (tag === "pre") {
    // Preformatted text keeps its exact leading/trailing whitespace.
    return [elementNode("pre", children)];
  }
  if (/^h[1-6]$/.test(tag)) {
    const content = trimEdges(children);
    return content.length && !isEmptyContent(content) ? [elementNode(tag, content)] : [];
  }
  return [elementNode(tag, trimEdges(children))];
}

function serializeNodes(nodes: EditorNode[]): string {
  return nodes.map(node => {
    if (node.kind === "text") return escapeText(node.value);
    const attrs = node.attrs.map(([name, value]) => ` ${name}="${escapeAttr(value)}"`).join("");
    if (VOID_TAGS.has(node.tag)) return `<${node.tag}${attrs}>`;
    return `<${node.tag}${attrs}>${serializeNodes(node.children)}</${node.tag}>`;
  }).join("");
}

function isEmptyParagraph(node: EditorNode): boolean {
  if (node.kind !== "element" || node.tag !== "p") return false;
  return node.children.length === 1 && node.children[0].kind === "element" && node.children[0].tag === "br";
}

// Bare inline runs at the fragment root (typing into an empty editor before
// the first Enter) are wrapped in a paragraph so the saved email has a block
// structure instead of an anonymous inline run next to the tables.
function wrapRootInline(nodes: EditorNode[]): EditorNode[] {
  const wrapped: EditorNode[] = [];
  let inline: EditorNode[] = [];
  const flush = () => {
    if (!inline.length) return;
    wrapped.push(elementNode("p", trimEdges(inline)));
    inline = [];
  };
  for (const node of nodes) {
    if (isBlockNode(node)) {
      flush();
      wrapped.push(node);
    } else {
      inline.push(node);
    }
  }
  flush();
  return wrapped;
}

// Normalizes contenteditable/pasted HTML into the server sanitizer subset.
// Returns a compact fragment using only allowed tags, attributes and table
// styles, with div/span/font wrappers removed.
export function normalizeMailTemplateHtml(html: string): string {
  const tree = buildTree(tokenizeHTML(String(html || "")));
  const nodes = wrapRootInline(trimEdges(normalizeChildren(tree, "")));
  while (nodes.length && isEmptyParagraph(nodes[0])) nodes.shift();
  while (nodes.length && isEmptyParagraph(nodes[nodes.length - 1])) nodes.pop();
  return serializeNodes(nodes);
}

// Plain-text paste fallback: escaped text, one <p> per paragraph and <br> for
// intra-paragraph line breaks. Nothing from the clipboard is interpreted.
export function plainTextToEditorHtml(value: string): string {
  const text = String(value || "").replace(/\r\n?/g, "\n").replaceAll("\u00a0", " ");
  if (!text.trim()) return "";
  return text.split(/\n{2,}/)
    .filter(block => block.trim() !== "")
    .map(block => `<p>${block.split("\n").map(line => escapeHTML(line)).join("<br>")}</p>`)
    .join("");
}

function rangeInside(editor: HTMLElement): Range | null {
  const selection = window.getSelection?.();
  if (!selection || !selection.rangeCount) return null;
  const range = selection.getRangeAt(0);
  return editor.contains(range.commonAncestorContainer) ? range : null;
}

function appendRange(editor: HTMLElement): Range {
  const range = document.createRange();
  range.selectNodeContents(editor);
  range.collapse(false);
  return range;
}

function placeCaret(range: Range): void {
  const selection = window.getSelection?.();
  if (!selection) return;
  selection.removeAllRanges();
  selection.addRange(range);
}

function insertAtSelection(editor: HTMLElement, content: Node): boolean {
  const range = rangeInside(editor) || appendRange(editor);
  range.deleteContents();
  range.insertNode(content);
  const caret = document.createRange();
  caret.setStartAfter(content);
  caret.collapse(true);
  placeCaret(caret);
  editor.focus();
  editor.dispatchEvent(new Event("input", {bubbles: true}));
  return true;
}

// Inserts a plain text token (for example {{build.version}}) at the caret so
// contenteditable never treats the braces as markup.
export function insertMailEditorText(editor: HTMLElement, text: string): boolean {
  if (!editor) return false;
  return insertAtSelection(editor, document.createTextNode(text));
}

// Inserts an HTML fragment at the caret. Callers pass already-normalized
// skeletons; the save path normalizes again.
export function insertMailEditorHtml(editor: HTMLElement, html: string): boolean {
  if (!editor) return false;
  const holder = document.createElement("template");
  holder.innerHTML = html;
  const fragment = document.importNode(holder.content, true);
  const last = fragment.lastChild;
  if (!last) return false;
  const range = rangeInside(editor) || appendRange(editor);
  range.deleteContents();
  range.insertNode(fragment);
  const caret = document.createRange();
  caret.setStartAfter(last);
  caret.collapse(true);
  placeCaret(caret);
  editor.focus();
  editor.dispatchEvent(new Event("input", {bubbles: true}));
  return true;
}

// Puts the caret in the first data cell after inserting a table skeleton and
// clears its placeholder nbsp so the first typed character lands there.
export function focusFirstMailEditorCell(editor: HTMLElement): boolean {
  if (!editor) return false;
  const cell = editor.querySelector("tbody td") || editor.querySelector("td");
  if (!cell) return false;
  if (!(cell.textContent || "").trim()) cell.innerHTML = "";
  editor.focus();
  const range = document.createRange();
  range.selectNodeContents(cell);
  range.collapse(true);
  placeCaret(range);
  return true;
}
