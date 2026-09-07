const allowedLinkProtocols = new Set(["http:", "https:", "mailto:", "tel:"]);
const allowedImageProtocols = new Set(["http:", "https:"]);
const allowedDataImageTypes = new Set(["image/png", "image/jpeg", "image/gif", "image/webp", "image/svg+xml"]);

function safeDataImageURL(value: string): boolean {
  if (!value.startsWith("data:")) return false;
  const marker = ";base64,";
  const markerIndex = value.indexOf(marker);
  if (markerIndex < 5) return false;
  const mediaType = value.slice(5, markerIndex).toLowerCase();
  const payload = value.slice(markerIndex + marker.length);
  const valid = allowedDataImageTypes.has(mediaType)
    && payload.length > 0
    && payload.length <= 3 * 1024 * 1024
    && payload.length % 4 === 0
    && /^[A-Za-z0-9+/]*={0,2}$/.test(payload);
  if (!valid) return false;
  if (mediaType === "image/svg+xml") {
    try {
      const svg = atob(payload);
      if (/<(?:script|foreignobject|iframe|object|embed)\b|on[a-z]+\s*=|(?:href|xlink:href)\s*=\s*["']?\s*(?:javascript:|https?:|data:)/i.test(svg)) return false;
    } catch {
      return false;
    }
  }
  return true;
}

function escapeHTML(value: unknown): string {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

export function sanitizeMarkdownURL(value: string, image = false): string | null {
  const url = String(value || "").trim();
  if (!url || /[\u0000-\u0020\u007f]/.test(url) || url.startsWith("\\")) return null;
  if (image && safeDataImageURL(url)) return url;
  if (/^(?:#|\?|\/|\.\/|\.\.\/)/.test(url)) return url;
  const scheme = url.match(/^([a-z][a-z0-9+.-]*:)/i)?.[1].toLowerCase();
  if (!scheme) return url;
  return (image ? allowedImageProtocols : allowedLinkProtocols).has(scheme) ? url : null;
}

interface LinkTarget {
  destination: string;
  title: string;
  end: number;
}

function closingBracket(source: string, start: number): number {
  for (let index = start + 1; index < source.length; index += 1) {
    if (source[index] === "\\") {
      index += 1;
      continue;
    }
    if (source[index] === "]") return index;
  }
  return -1;
}

function linkTarget(source: string, open: number): LinkTarget | null {
  let index = open + 1;
  while (index < source.length && /[ \t]/.test(source[index])) index += 1;
  let destination = "";
  if (source[index] === "<") {
    const end = source.indexOf(">", index + 1);
    if (end < 0 || source.slice(index + 1, end).includes("\n")) return null;
    destination = source.slice(index + 1, end);
    index = end + 1;
  } else {
    let depth = 0;
    const start = index;
    for (; index < source.length; index += 1) {
      const character = source[index];
      if (character === "\\" && index + 1 < source.length) {
        index += 1;
        continue;
      }
      if (character === "(") {
        depth += 1;
        continue;
      }
      if (character === ")") {
        if (depth === 0) break;
        depth -= 1;
        continue;
      }
      if (depth === 0 && /[ \t\n]/.test(character)) break;
    }
    destination = source.slice(start, index);
  }
  if (!destination) return null;
  while (index < source.length && /[ \t]/.test(source[index])) index += 1;
  let title = "";
  if (source[index] === '"' || source[index] === "'") {
    const quote = source[index];
    const start = ++index;
    while (index < source.length && source[index] !== quote) {
      if (source[index] === "\\") index += 1;
      index += 1;
    }
    if (source[index] !== quote) return null;
    title = source.slice(start, index);
    index += 1;
    while (index < source.length && /[ \t]/.test(source[index])) index += 1;
  }
  if (source[index] !== ")") return null;
  return {
    destination: destination.replace(/\\([\\()[\]<>])/g, "$1"),
    title: title.replace(/\\([\\"'])/g, "$1"),
    end: index + 1,
  };
}

function plainLabel(value: string): string {
  return value.replace(/\\([\\`*_[\]{}()#+.!~-])/g, "$1").replace(/[`*_~]/g, "");
}

function renderInline(source: string, depth = 0): string {
  if (depth > 24) return escapeHTML(source).replaceAll("\n", "<br>");
  let output = "";
  let index = 0;
  while (index < source.length) {
    const image = source.startsWith("![", index);
    if (image || source[index] === "[") {
      const labelOpen = image ? index + 1 : index;
      const labelEnd = closingBracket(source, labelOpen);
      if (labelEnd >= 0 && source[labelEnd + 1] === "(") {
        const target = linkTarget(source, labelEnd + 1);
        if (target) {
          const label = source.slice(labelOpen + 1, labelEnd);
          const safeURL = sanitizeMarkdownURL(target.destination, image);
          if (image) {
            output += safeURL
              ? `<img src="${escapeHTML(safeURL)}" alt="${escapeHTML(plainLabel(label))}"${target.title ? ` title="${escapeHTML(target.title)}"` : ""} loading="lazy" decoding="async" referrerpolicy="no-referrer">`
              : escapeHTML(plainLabel(label));
          } else {
            const renderedLabel = renderInline(label, depth + 1);
            output += safeURL
              ? `<a href="${escapeHTML(safeURL)}"${target.title ? ` title="${escapeHTML(target.title)}"` : ""} target="_blank" rel="noopener noreferrer">${renderedLabel}</a>`
              : renderedLabel;
          }
          index = target.end;
          continue;
        }
      }
    }
    if (source[index] === "`") {
      let ticks = 1;
      while (source[index + ticks] === "`") ticks += 1;
      const marker = "`".repeat(ticks);
      const end = source.indexOf(marker, index + ticks);
      if (end >= 0) {
        let code = source.slice(index + ticks, end).replace(/\n/g, " ");
        if (/^ .* $/.test(code) && !/^ +$/.test(code)) code = code.slice(1, -1);
        output += `<code>${escapeHTML(code)}</code>`;
        index = end + ticks;
        continue;
      }
    }
    const emphasis = ([
      ["**", "strong"],
      ["__", "strong"],
      ["~~", "del"],
      ["*", "em"],
      ["_", "em"],
    ] as const).find(([marker]) => source.startsWith(marker, index));
    if (emphasis) {
      const [marker, tag] = emphasis;
      const end = source.indexOf(marker, index + marker.length);
      if (end > index + marker.length) {
        output += `<${tag}>${renderInline(source.slice(index + marker.length, end), depth + 1)}</${tag}>`;
        index = end + marker.length;
        continue;
      }
    }
    if (source[index] === "\\" && index + 1 < source.length && /[\\`*_[\]{}()#+.!|>~-]/.test(source[index + 1])) {
      output += escapeHTML(source[index + 1]);
      index += 2;
      continue;
    }
    if (source[index] === "\n") {
      output += "<br>";
      index += 1;
      continue;
    }
    output += escapeHTML(source[index]);
    index += 1;
  }
  return output;
}

function splitTableRow(line: string): string[] {
  const source = line.trim().replace(/^\|/, "").replace(/\|$/, "");
  const cells: string[] = [];
  let cell = "";
  let ticks = 0;
  for (let index = 0; index < source.length; index += 1) {
    const character = source[index];
    if (character === "\\" && source[index + 1] === "|") {
      cell += "|";
      index += 1;
      continue;
    }
    if (character === "`") ticks = ticks ? 0 : 1;
    if (character === "|" && !ticks) {
      cells.push(cell.trim());
      cell = "";
      continue;
    }
    cell += character;
  }
  cells.push(cell.trim());
  return cells;
}

function tableAlignments(line: string): Array<"left" | "center" | "right" | ""> | null {
  const cells = splitTableRow(line);
  if (!cells.length || cells.some(cell => !/^:?-{3,}:?$/.test(cell))) return null;
  return cells.map(cell => cell.startsWith(":") && cell.endsWith(":") ? "center" : cell.endsWith(":") ? "right" : cell.startsWith(":") ? "left" : "");
}

function isFence(line: string): RegExpMatchArray | null {
  return line.match(/^ {0,3}(`{3,}|~{3,})\s*([^`]*)$/);
}

function isList(line: string): RegExpMatchArray | null {
  return line.match(/^ {0,3}(?:(\d+)[.)]|([-+*]))\s+(.+)$/);
}

function startsBlock(lines: string[], index: number): boolean {
  const line = lines[index] || "";
  return Boolean(
    isFence(line)
    || /^ {0,3}#{1,6}(?:\s+|$)/.test(line)
    || /^ {0,3}>/.test(line)
    || isList(line)
    || (line.includes("|") && index + 1 < lines.length && tableAlignments(lines[index + 1])),
  );
}

function renderBlocks(lines: string[], depth = 0): string {
  const blocks: string[] = [];
  let index = 0;
  while (index < lines.length) {
    if (!lines[index].trim()) {
      index += 1;
      continue;
    }
    const fence = isFence(lines[index]);
    if (fence) {
      const marker = fence[1];
      const language = fence[2].trim().split(/\s+/)[0].replace(/[^a-z0-9_-]/gi, "");
      const code: string[] = [];
      index += 1;
      const closing = new RegExp(`^ {0,3}${marker[0]}{${marker.length},}\\s*$`);
      while (index < lines.length && !closing.test(lines[index])) {
        code.push(lines[index]);
        index += 1;
      }
      if (index < lines.length) index += 1;
      blocks.push(`<pre><code${language ? ` class="language-${escapeHTML(language)}"` : ""}>${escapeHTML(code.join("\n"))}</code></pre>`);
      continue;
    }
    const heading = lines[index].match(/^ {0,3}(#{1,6})(?:\s+|$)(.*)$/);
    if (heading) {
      const level = heading[1].length;
      blocks.push(`<h${level}>${renderInline(heading[2].replace(/\s+#+\s*$/, ""))}</h${level}>`);
      index += 1;
      continue;
    }
    if (/^ {0,3}>/.test(lines[index])) {
      const quote: string[] = [];
      while (index < lines.length && (/^ {0,3}>/.test(lines[index]) || !lines[index].trim())) {
        quote.push(lines[index].replace(/^ {0,3}> ?/, ""));
        index += 1;
      }
      const content = depth < 24 ? renderBlocks(quote, depth + 1) : `<p>${renderInline(quote.join("\n"))}</p>`;
      blocks.push(`<blockquote>${content}</blockquote>`);
      continue;
    }
    const firstListItem = isList(lines[index]);
    if (firstListItem) {
      const ordered = Boolean(firstListItem[1]);
      const items: string[] = [];
      const start = firstListItem[1] ? Number(firstListItem[1]) : 1;
      while (index < lines.length) {
        const item = isList(lines[index]);
        if (!item || Boolean(item[1]) !== ordered) break;
        const content = [item[3]];
        index += 1;
        while (index < lines.length && /^ {2,}\S/.test(lines[index]) && !isList(lines[index])) {
          content.push(lines[index].trim());
          index += 1;
        }
        items.push(`<li>${renderInline(content.join("\n"))}</li>`);
      }
      const tag = ordered ? "ol" : "ul";
      const startAttribute = ordered && start !== 1 ? ` start="${start}"` : "";
      blocks.push(`<${tag}${startAttribute}>${items.join("")}</${tag}>`);
      continue;
    }
    if (lines[index].includes("|") && index + 1 < lines.length) {
      const alignments = tableAlignments(lines[index + 1]);
      if (alignments) {
        const headers = splitTableRow(lines[index]);
        index += 2;
        const rows: string[][] = [];
        while (index < lines.length && lines[index].trim() && lines[index].includes("|") && !startsBlock(lines, index)) {
          rows.push(splitTableRow(lines[index]));
          index += 1;
        }
        const cell = (value: string, position: number, tag: "th" | "td") => {
          const alignment = alignments[position] || "";
          return `<${tag}${alignment ? ` style="text-align:${alignment}"` : ""}>${renderInline(value || "")}</${tag}>`;
        };
        const head = `<thead><tr>${headers.map((value, position) => cell(value, position, "th")).join("")}</tr></thead>`;
        const body = rows.length ? `<tbody>${rows.map(row => `<tr>${headers.map((_, position) => cell(row[position] || "", position, "td")).join("")}</tr>`).join("")}</tbody>` : "";
        blocks.push(`<div class="markdown-table-wrap"><table>${head}${body}</table></div>`);
        continue;
      }
    }
    const paragraph: string[] = [];
    while (index < lines.length && lines[index].trim() && (!paragraph.length || !startsBlock(lines, index))) {
      paragraph.push(lines[index]);
      index += 1;
    }
    blocks.push(`<p>${renderInline(paragraph.join("\n"))}</p>`);
  }
  return blocks.join("");
}

export function renderMarkdown(value: unknown): string {
  const source = String(value ?? "").replace(/\r\n?/g, "\n");
  return renderBlocks(source.split("\n"));
}
