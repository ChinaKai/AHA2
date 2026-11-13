import test from "node:test";
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";

const repo = resolve(import.meta.dirname, "..", "..");
const css = await readFile(resolve(repo, "web", "src", "styles.css"), "utf8");

// The cascade checks model five fixed layout widths plus two declared
// container widths. This is a deliberately small CSS cascade model, not a
// browser: it covers the selector and at-rule surface used by the assertions
// below. See docs/web-design-tokens.md for the documented limits.
//
// Supported selector surface: type/class/id/attribute selectors, descendant
// and child combinators, :not/:is/:where/:root, ::pseudo-elements and the UI
// state pseudo-classes used by these targets. Sibling combinators and :has
// are not modeled because no assertion below depends on them.
const VIEWPORTS = [360, 375, 768, 1280, 1440];
const DEFAULT_VIEWPORT = 1280;
const CONTAINER_WIDTHS = [360, 900];
const DEFAULT_CONTAINER_WIDTH = 900;
const KNOWN_CONTAINERS = new Set(["shared-desktop"]);

/* ------------------------------------------------------------------ *
 * CSS parser
 * ------------------------------------------------------------------ */

function stripComments(source) {
  return source.replace(/\/\*[\s\S]*?\*\//g, "");
}

function splitTopLevel(text, delimiter) {
  const parts = [];
  let depth = 0;
  let quote = null;
  let start = 0;
  for (let index = 0; index < text.length; index += 1) {
    const char = text[index];
    if (quote) {
      if (char === "\\") index += 1;
      else if (char === quote) quote = null;
      continue;
    }
    if (char === '"' || char === "'") {
      quote = char;
      continue;
    }
    if (char === "(" || char === "[") {
      depth += 1;
      continue;
    }
    if (char === ")" || char === "]") {
      depth -= 1;
      continue;
    }
    if (depth === 0 && char === delimiter) {
      parts.push(text.slice(start, index));
      start = index + 1;
    }
  }
  parts.push(text.slice(start));
  return parts;
}

function indexOfTopLevel(text, target) {
  let depth = 0;
  let quote = null;
  for (let index = 0; index < text.length; index += 1) {
    const char = text[index];
    if (quote) {
      if (char === "\\") index += 1;
      else if (char === quote) quote = null;
      continue;
    }
    if (char === '"' || char === "'") {
      quote = char;
      continue;
    }
    if (char === "(" || char === "[") depth += 1;
    else if (char === ")" || char === "]") depth -= 1;
    else if (depth === 0 && char === target) return index;
  }
  return -1;
}

function findMatchingBrace(text, openIndex) {
  let depth = 0;
  let quote = null;
  for (let index = openIndex; index < text.length; index += 1) {
    const char = text[index];
    if (quote) {
      if (char === "\\") index += 1;
      else if (char === quote) quote = null;
      continue;
    }
    if (char === '"' || char === "'") {
      quote = char;
      continue;
    }
    if (char === "{") depth += 1;
    else if (char === "}") {
      depth -= 1;
      if (depth === 0) return index;
    }
  }
  return text.length;
}

function parseDeclarations(block) {
  const declarations = [];
  for (const part of splitTopLevel(block, ";")) {
    const text = part.trim();
    if (!text) continue;
    const colon = indexOfTopLevel(text, ":");
    if (colon < 0) continue;
    const rawProperty = text.slice(0, colon).trim();
    const property = rawProperty.startsWith("--") ? rawProperty : rawProperty.toLowerCase();
    let value = text.slice(colon + 1).trim();
    let important = false;
    const importantMatch = value.match(/\s*!important\s*$/i);
    if (importantMatch) {
      important = true;
      value = value.slice(0, importantMatch.index).trim();
    }
    if (property && value) declarations.push({property, value, important});
  }
  return declarations;
}

function evaluateMedia(condition, viewportWidth) {
  return splitTopLevel(condition, ",").some(query => {
    const normalized = query.trim().toLowerCase();
    if (!normalized || normalized === "all" || normalized === "screen") return true;
    if (normalized === "print") return false;
    return normalized.split(/\s+and\s+/).every(feature => {
      const item = feature.trim();
      if (item === "all" || item === "screen") return true;
      if (item === "print") return false;
      const minWidth = item.match(/^\(min-width:\s*(\d+(?:\.\d+)?)px\)$/);
      if (minWidth) return viewportWidth >= Number(minWidth[1]);
      const maxWidth = item.match(/^\(max-width:\s*(\d+(?:\.\d+)?)px\)$/);
      if (maxWidth) return viewportWidth <= Number(maxWidth[1]);
      if (item.includes("prefers-reduced-motion")) return false;
      if (item.includes("prefers-color-scheme")) return false;
      // Unknown features do not match the modeled viewport.
      return false;
    });
  });
}

const ASSERTED_PROPERTY_PATTERN = /(?:^|[;{])\s*(?:color|background(?:-color)?|border(?:-(?:top|right|bottom|left|color|radius))?(?:-color)?|outline(?:-color)?|box-shadow|font-size|display|padding|--color-[\w-]+|--button-[\w-]+|--focus-[\w-]+|--radius-[\w-]+|--shadow-[\w-]+|--swatch-color)\s*:/i;

function containsAssertedProperty(block) {
  return ASSERTED_PROPERTY_PATTERN.test(block);
}

function evaluateContainer(condition, containerWidth, block) {
  const firstParen = condition.indexOf("(");
  const name = firstParen > 0 ? condition.slice(0, firstParen).trim() : "";
  if (name && !KNOWN_CONTAINERS.has(name)) {
    if (containsAssertedProperty(block)) {
      throw new Error(`unmodeled container ${name} affects asserted properties; add the container name or remove the rule`);
    }
    return false;
  }
  const features = firstParen >= 0 ? condition.slice(firstParen) : condition;
  const queries = splitTopLevel(features, ",")
    .map(query => query.trim().toLowerCase())
    .filter(Boolean);
  if (queries.length === 0) {
    if (containsAssertedProperty(block)) {
      throw new Error(`unmodeled container condition "${condition}" affects asserted properties`);
    }
    return false;
  }
  let recognized = false;
  const result = queries.some(query => query.split(/\s+and\s+/).every(feature => {
    const item = feature.trim();
    const minWidth = item.match(/^\(min-width:\s*(\d+(?:\.\d+)?)px\)$/);
    if (minWidth) {
      recognized = true;
      return containerWidth >= Number(minWidth[1]);
    }
    const maxWidth = item.match(/^\(max-width:\s*(\d+(?:\.\d+)?)px\)$/);
    if (maxWidth) {
      recognized = true;
      return containerWidth <= Number(maxWidth[1]);
    }
    return false;
  }));
  if (!recognized && containsAssertedProperty(block)) {
    throw new Error(`unmodeled container condition "${condition}" affects asserted properties; only width features are modeled`);
  }
  return result;
}

function splitTopLevelKeyword(text, keyword) {
  const lowerKeyword = keyword.toLowerCase();
  const parts = [];
  let depth = 0;
  let quote = null;
  let start = 0;
  for (let index = 0; index < text.length; index += 1) {
    const char = text[index];
    if (quote) {
      if (char === "\\") index += 1;
      else if (char === quote) quote = null;
      continue;
    }
    if (char === '"' || char === "'") {
      quote = char;
      continue;
    }
    if (char === "(" || char === "[") {
      depth += 1;
      continue;
    }
    if (char === ")" || char === "]") {
      depth -= 1;
      continue;
    }
    if (depth === 0 && text.slice(index, index + keyword.length).toLowerCase() === lowerKeyword) {
      const before = text[index - 1] ?? " ";
      const after = text[index + keyword.length] ?? " ";
      if (/\s/.test(before) && /\s/.test(after)) {
        parts.push(text.slice(start, index).trim());
        start = index + keyword.length;
        index += keyword.length - 1;
      }
    }
  }
  parts.push(text.slice(start).trim());
  return parts.filter(part => part.length > 0);
}

const SUPPORTED_PROPERTIES = new Set([
  "accent-color", "animation", "aspect-ratio", "backdrop-filter", "background",
  "background-color", "border", "border-color", "border-radius", "box-shadow",
  "color", "color-scheme", "container-name", "container-type", "content", "display",
  "flex", "flex-direction", "flex-wrap", "font", "font-family", "font-size",
  "font-weight", "gap", "grid", "grid-template-columns", "height", "inset",
  "justify-content", "line-height", "margin", "max-height", "max-width", "min-height",
  "min-width", "opacity", "outline", "overflow", "padding", "position", "transform",
  "transition", "width", "z-index",
]);

const DISPLAY_VALUES = new Set([
  "block", "contents", "flex", "flow-root", "grid", "inline", "inline-block",
  "inline-flex", "inline-grid", "list-item", "none", "ruby", "table", "table-cell",
  "table-row",
]);

function supportsDeclaration(property, value) {
  if (!property || !value) return false;
  if (property.startsWith("--")) return true;
  if (!SUPPORTED_PROPERTIES.has(property)) return false;
  if (/unsupported|invalid|unknown/i.test(value)) return false;
  if (property === "display") return DISPLAY_VALUES.has(value);
  return true;
}

function evaluateSupports(condition) {
  const text = condition.trim();
  if (!text) return false;
  if (/^not\s+/i.test(text)) return !evaluateSupports(text.replace(/^not\s+/i, "").trim());
  const orParts = splitTopLevelKeyword(text, "or");
  if (orParts.length > 1) return orParts.some(evaluateSupports);
  const andParts = splitTopLevelKeyword(text, "and");
  if (andParts.length > 1) return andParts.every(evaluateSupports);
  if (!text.startsWith("(") || !text.endsWith(")")) return false;
  const inner = text.slice(1, -1).trim();
  if (!inner) return false;
  if (inner.startsWith("(") || /^not\s+/i.test(inner)
    || splitTopLevelKeyword(inner, "or").length > 1
    || splitTopLevelKeyword(inner, "and").length > 1) {
    return evaluateSupports(inner);
  }
  const colon = indexOfTopLevel(inner, ":");
  if (colon < 0) return false;
  const property = inner.slice(0, colon).trim().toLowerCase();
  const value = inner.slice(colon + 1).trim().toLowerCase();
  return supportsDeclaration(property, value);
}

function parseStylesheet(source, viewportWidth = DEFAULT_VIEWPORT, containerWidth = DEFAULT_CONTAINER_WIDTH) {
  const clean = stripComments(source);
  const rules = [];
  let order = 0;
  const layerOrder = [];
  const layerRanks = new Map();
  let anonymousLayer = 0;

  const registerLayer = name => {
    if (!layerRanks.has(name)) {
      layerRanks.set(name, layerOrder.length);
      layerOrder.push(name);
    }
    return layerRanks.get(name);
  };

  const scopedLayer = (parent, name) => parent ? `${parent}.${name}` : name;

  function parseBlock(text, applies, layer = null) {
    let index = 0;
    while (index < text.length) {
      while (index < text.length && /\s/.test(text[index])) index += 1;
      if (index >= text.length) break;

      const start = index;
      let depth = 0;
      let quote = null;
      while (index < text.length) {
        const char = text[index];
        if (quote) {
          if (char === "\\") index += 2;
          else {
            if (char === quote) quote = null;
            index += 1;
          }
          continue;
        }
        if (char === '"' || char === "'") {
          quote = char;
          index += 1;
          continue;
        }
        if (char === "(" || char === "[") {
          depth += 1;
          index += 1;
          continue;
        }
        if (char === ")" || char === "]") {
          depth -= 1;
          index += 1;
          continue;
        }
        if (depth === 0 && (char === "{" || char === ";")) break;
        index += 1;
      }
      if (index >= text.length) break;

      const prelude = text.slice(start, index).trim();
      if (text[index] === ";") {
        if (/^@layer\b/i.test(prelude)) {
          for (const name of prelude.slice(6).split(",").map(item => item.trim()).filter(Boolean)) {
            registerLayer(scopedLayer(layer, name));
          }
        }
        index += 1;
        continue;
      }

      const blockEnd = findMatchingBrace(text, index);
      const block = text.slice(index + 1, blockEnd);
      index = blockEnd + 1;

      if (prelude.startsWith("@")) {
        const atMatch = prelude.match(/^@([\w-]+)/);
        const atName = atMatch?.[1]?.toLowerCase() ?? "";
        const condition = atMatch ? prelude.slice(atMatch[0].length).trim() : "";
        if (atName === "media") {
          parseBlock(block, applies && evaluateMedia(condition, viewportWidth), layer);
        } else if (atName === "supports") {
          parseBlock(block, applies && evaluateSupports(condition), layer);
        } else if (atName === "container") {
          parseBlock(block, applies && evaluateContainer(condition, containerWidth, block), layer);
        } else if (atName === "layer") {
          const names = condition.split(",").map(item => item.trim()).filter(Boolean);
          if (names.length === 0) names.push(`__anonymous_${anonymousLayer++}`);
          const chosen = names[0];
          const nestedLayer = scopedLayer(layer, chosen);
          registerLayer(nestedLayer);
          parseBlock(block, applies, nestedLayer);
        } else if (atName === "keyframes" || atName === "font-face" || atName === "page") {
          // Known non-element at-rules.
        } else if (containsAssertedProperty(block)) {
          throw new Error(`unmodeled at-rule @${atName || "(anonymous)"} contains asserted properties; extend the cascade model or remove the rule`);
        }
        continue;
      }

      if (!applies) continue;
      const selectors = splitTopLevel(prelude, ",")
        .map(selector => selector.replace(/\s+/g, " ").trim())
        .filter(Boolean);
      const declarations = parseDeclarations(block);
      for (const declaration of declarations) declaration.order = order++;
      if (selectors.length && declarations.length) {
        rules.push({
          selectors,
          declarations,
          layer,
          layerRank: layer ? registerLayer(layer) : null,
        });
      }
    }
  }

  parseBlock(clean, true, null);
  return {rules};
}

const sheetCache = new Map();
function sheet(source, viewportWidth = DEFAULT_VIEWPORT, containerWidth = DEFAULT_CONTAINER_WIDTH) {
  let byViewport = sheetCache.get(source);
  if (!byViewport) {
    byViewport = new Map();
    sheetCache.set(source, byViewport);
  }
  const key = `${viewportWidth}/${containerWidth}`;
  if (!byViewport.has(key)) {
    byViewport.set(key, parseStylesheet(source, viewportWidth, containerWidth));
  }
  return byViewport.get(key);
}

/* ------------------------------------------------------------------ *
 * Selector matching and specificity
 * ------------------------------------------------------------------ */

function readIdent(text, index) {
  let value = "";
  while (index < text.length) {
    const char = text[index];
    if (/[a-zA-Z0-9_-]/.test(char)) {
      value += char;
      index += 1;
    } else if (char === "\\" && index + 1 < text.length) {
      value += text[index + 1];
      index += 2;
    } else break;
  }
  return {value, next: index};
}

function skipParen(text, index, open, close) {
  let depth = 0;
  let quote = null;
  for (let cursor = index; cursor < text.length; cursor += 1) {
    const char = text[cursor];
    if (quote) {
      if (char === "\\") cursor += 1;
      else if (char === quote) quote = null;
      continue;
    }
    if (char === '"' || char === "'") {
      quote = char;
      continue;
    }
    if (char === open) depth += 1;
    else if (char === close) {
      depth -= 1;
      if (depth === 0) return cursor;
    }
  }
  return text.length - 1;
}

function compareSpecificity(left, right) {
  for (let index = 0; index < 3; index += 1) {
    if (left[index] !== right[index]) return left[index] - right[index];
  }
  return 0;
}

function maxSpecificityOfSelectorList(selectorList) {
  let max = [0, 0, 0];
  for (const part of splitTopLevel(selectorList, ",")) {
    const candidate = specificity(part.trim());
    if (compareSpecificity(candidate, max) > 0) max = candidate;
  }
  return max;
}

const specificityCache = new Map();
function specificity(selector) {
  if (specificityCache.has(selector)) return specificityCache.get(selector);
  let ids = 0;
  let classes = 0;
  let elements = 0;
  let index = 0;
  while (index < selector.length) {
    const char = selector[index];
    if (char === "#") {
      ids += 1;
      index = readIdent(selector, index + 1).next;
    } else if (char === ".") {
      classes += 1;
      index = readIdent(selector, index + 1).next;
    } else if (char === "[") {
      classes += 1;
      index = skipParen(selector, index, "[", "]") + 1;
    } else if (char === ":") {
      if (selector[index + 1] === ":") {
        elements += 1;
        index = readIdent(selector, index + 2).next;
        if (selector[index] === "(") index = skipParen(selector, index, "(", ")") + 1;
      } else {
        const ident = readIdent(selector, index + 1);
        const name = ident.value.toLowerCase();
        index = ident.next;
        if (selector[index] === "(") {
          const close = skipParen(selector, index, "(", ")");
          const args = selector.slice(index + 1, close);
          if (name !== "where") {
            if (name === "not" || name === "is" || name === "has") {
              const nested = maxSpecificityOfSelectorList(args);
              ids += nested[0];
              classes += nested[1];
              elements += nested[2];
            } else {
              classes += 1;
            }
          }
          index = close + 1;
        } else {
          classes += 1;
        }
      }
    } else if (char === "*") {
      index += 1;
    } else if (char === "|") {
      index += 1;
    } else if (/[a-zA-Z_-]/.test(char)) {
      elements += 1;
      index = readIdent(selector, index).next;
    } else {
      index += 1;
    }
  }
  const result = [ids, classes, elements];
  specificityCache.set(selector, result);
  return result;
}

const selectorCache = new Map();
function parsedSelector(selector) {
  if (selectorCache.has(selector)) return selectorCache.get(selector);
  const tokens = [];
  let buffer = "";
  let depth = 0;
  let quote = null;
  const flush = () => {
    if (buffer.trim()) tokens.push({type: "compound", value: buffer.trim()});
    buffer = "";
  };
  for (let index = 0; index < selector.length; index += 1) {
    const char = selector[index];
    if (quote) {
      buffer += char;
      if (char === "\\") {
        buffer += selector[index + 1] ?? "";
        index += 1;
      } else if (char === quote) quote = null;
      continue;
    }
    if (char === '"' || char === "'") {
      quote = char;
      buffer += char;
      continue;
    }
    if (char === "(" || char === "[") {
      depth += 1;
      buffer += char;
      continue;
    }
    if (char === ")" || char === "]") {
      depth -= 1;
      buffer += char;
      continue;
    }
    if (depth === 0 && /\s/.test(char)) {
      flush();
      continue;
    }
    if (depth === 0 && (char === ">" || char === "+" || char === "~")) {
      flush();
      tokens.push({type: "combinator", value: char});
      continue;
    }
    buffer += char;
  }
  flush();

  const compounds = [];
  const combinators = [];
  let pendingCombinator = null;
  for (const token of tokens) {
    if (token.type === "compound") {
      if (compounds.length > 0) combinators.push(pendingCombinator ?? " ");
      compounds.push(token.value);
      pendingCombinator = null;
    } else {
      pendingCombinator = token.value;
    }
  }
  const result = {compounds, combinators};
  selectorCache.set(selector, result);
  return result;
}

const compoundCache = new Map();
function parsedCompound(compound) {
  if (compoundCache.has(compound)) return compoundCache.get(compound);
  const result = {type: null, id: null, classes: [], attrs: [], pseudos: [], hasPseudoElement: false};
  let index = 0;
  while (index < compound.length) {
    const char = compound[index];
    if (char === "#") {
      const ident = readIdent(compound, index + 1);
      result.id = ident.value;
      index = ident.next;
    } else if (char === ".") {
      const ident = readIdent(compound, index + 1);
      result.classes.push(ident.value);
      index = ident.next;
    } else if (char === "[") {
      const close = skipParen(compound, index, "[", "]");
      result.attrs.push(parseAttributeSelector(compound.slice(index + 1, close)));
      index = close + 1;
    } else if (char === ":") {
      if (compound[index + 1] === ":") {
        result.hasPseudoElement = true;
        const ident = readIdent(compound, index + 2);
        index = ident.next;
        if (compound[index] === "(") index = skipParen(compound, index, "(", ")") + 1;
      } else {
        const ident = readIdent(compound, index + 1);
        let args = null;
        index = ident.next;
        if (compound[index] === "(") {
          const close = skipParen(compound, index, "(", ")");
          args = compound.slice(index + 1, close);
          index = close + 1;
        }
        result.pseudos.push({name: ident.value.toLowerCase(), args});
      }
    } else if (char === "*") {
      result.type = "*";
      index += 1;
    } else if (char === "|") {
      index += 1;
    } else if (/[a-zA-Z_-]/.test(char)) {
      const ident = readIdent(compound, index);
      result.type = ident.value.toLowerCase();
      index = ident.next;
    } else {
      index += 1;
    }
  }
  compoundCache.set(compound, result);
  return result;
}

function parseAttributeSelector(text) {
  const match = text.trim().match(/^([^\s~|^$*=\]]+)\s*(?:([~|^$*]?=)\s*(?:"([^"]*)"|'([^']*)'|([^\s\]]+)))?(?:\s+([iIsS]))?$/);
  if (!match) return {name: text.trim().toLowerCase(), operator: null, value: null, insensitive: false};
  return {
    name: match[1].toLowerCase(),
    operator: match[2] ?? null,
    value: match[3] ?? match[4] ?? match[5] ?? null,
    insensitive: Boolean(match[6]),
  };
}

function matchesAttribute(selector, element) {
  if (!element.attrs.has(selector.name)) return false;
  if (!selector.operator) return true;
  let left = String(element.attrs.get(selector.name));
  let right = selector.value ?? "";
  if (selector.insensitive) {
    left = left.toLowerCase();
    right = right.toLowerCase();
  }
  switch (selector.operator) {
    case "=": return left === right;
    case "~=": return left.split(/\s+/).includes(right);
    case "|=": return left === right || left.startsWith(`${right}-`);
    case "^=": return left.startsWith(right);
    case "$=": return left.endsWith(right);
    case "*=": return left.includes(right);
    default: return false;
  }
}

function matchesPseudo(pseudo, element) {
  if (pseudo.name === "not") {
    return !splitTopLevel(pseudo.args ?? "", ",").some(selector => matchesSelector(selector.trim(), element));
  }
  if (pseudo.name === "is" || pseudo.name === "where") {
    return splitTopLevel(pseudo.args ?? "", ",").some(selector => matchesSelector(selector.trim(), element));
  }
  if (pseudo.name === "has") return false;
  if (pseudo.name === "root") return element.tag === "html";
  return element.states.has(pseudo.name);
}

function matchesCompound(compound, element) {
  const parsed = parsedCompound(compound);
  if (parsed.hasPseudoElement) return false;
  if (parsed.type && parsed.type !== "*" && parsed.type !== element.tag) return false;
  if (parsed.id && parsed.id !== element.id) return false;
  for (const className of parsed.classes) if (!element.classes.has(className)) return false;
  for (const attr of parsed.attrs) if (!matchesAttribute(attr, element)) return false;
  for (const pseudo of parsed.pseudos) if (!matchesPseudo(pseudo, element)) return false;
  return true;
}

function matchesSelector(selector, element) {
  const parsed = parsedSelector(selector);
  const matchAt = (index, node) => {
    if (!node) return false;
    if (!matchesCompound(parsed.compounds[index], node)) return false;
    if (index === 0) return true;
    const combinator = parsed.combinators[index - 1];
    if (combinator === ">") return matchAt(index - 1, node.parent);
    if (combinator === " ") {
      for (let ancestor = node.parent; ancestor; ancestor = ancestor.parent) {
        if (matchAt(index - 1, ancestor)) return true;
      }
      return false;
    }
    return false;
  };
  return matchAt(parsed.compounds.length - 1, element);
}

function element(tag, {classes = [], states = [], attrs = {}, id = null, type = null, parent = null} = {}) {
  const attributeMap = new Map(
    Object.entries(attrs).map(([name, value]) => [name.toLowerCase(), String(value)]),
  );
  const normalizedClasses = classes.map(String);
  if (normalizedClasses.length > 0) attributeMap.set("class", normalizedClasses.join(" "));
  if (id !== null) attributeMap.set("id", String(id));
  if (type !== null) attributeMap.set("type", String(type));
  return {
    tag: tag.toLowerCase(),
    id,
    classes: new Set(normalizedClasses),
    states: new Set(states),
    attrs: attributeMap,
    parent,
    matchCache: new Map(),
  };
}

/* ------------------------------------------------------------------ *
 * Cascade resolution
 * ------------------------------------------------------------------ */

function colorFromShorthand(value) {
  const matches = [...value.matchAll(/#[0-9a-fA-F]{3,8}\b|var\(--[\w-]+\)|rgba?\([^)]*\)|hsla?\([^)]*\)|color\([^)]*\)|\b(?:transparent|currentColor|white|black)\b/g)];
  return matches.at(-1)?.[0];
}

function valueForProperty(property, value, requested) {
  if (property === requested) return value;
  if (property.startsWith("--") || requested.startsWith("--")) return null;
  if (requested === "outline-color" && property === "outline") return colorFromShorthand(value);
  if (requested === "border-color" && property === "border") return colorFromShorthand(value);
  if (requested === "background-color" && property === "background") return colorFromShorthand(value);
  if (requested === "background" && property === "background-color") return value;
  return null;
}

function isBetter(candidate, winner) {
  if (!winner) return true;
  if (candidate.important !== winner.important) return candidate.important;
  if (candidate.layer !== winner.layer) {
    if (!candidate.important) {
      // Normal declarations: unlayered wins over layered; later layer wins
      // over earlier.
      if (candidate.layer === null) return true;
      if (winner.layer === null) return false;
      return candidate.layerRank > winner.layerRank;
    }
    // Important declarations reverse layer priority: layered wins over
    // unlayered; earlier layer wins over later.
    if (candidate.layer === null) return false;
    if (winner.layer === null) return true;
    return candidate.layerRank < winner.layerRank;
  }
  const comparison = compareSpecificity(candidate.specificity, winner.specificity);
  if (comparison !== 0) return comparison > 0;
  return candidate.order > winner.order;
}

function matchesSelectorCached(selector, element) {
  if (!element.matchCache.has(selector)) {
    element.matchCache.set(selector, matchesSelector(selector, element));
  }
  return element.matchCache.get(selector);
}

function resolveProperty(source, element, property, viewportWidth = DEFAULT_VIEWPORT, containerWidth = DEFAULT_CONTAINER_WIDTH) {
  const {rules} = sheet(source, viewportWidth, containerWidth);
  let winner = null;
  for (const rule of rules) {
    let bestSpecificity = null;
    for (const selector of rule.selectors) {
      if (!matchesSelectorCached(selector, element)) continue;
      const candidate = specificity(selector);
      if (!bestSpecificity || compareSpecificity(candidate, bestSpecificity) > 0) {
        bestSpecificity = candidate;
      }
    }
    if (!bestSpecificity) continue;
    for (const declaration of rule.declarations) {
      const value = valueForProperty(declaration.property, declaration.value, property);
      if (value === undefined || value === null) continue;
      const candidate = {
        value,
        important: declaration.important,
        specificity: bestSpecificity,
        order: declaration.order,
        layer: rule.layer,
        layerRank: rule.layerRank,
      };
      if (isBetter(candidate, winner)) winner = candidate;
    }
  }
  return winner?.value;
}

const tokenCache = new Map();
function rootTokens(source, viewportWidth = DEFAULT_VIEWPORT, containerWidth = DEFAULT_CONTAINER_WIDTH) {
  let byViewport = tokenCache.get(source);
  if (!byViewport) {
    byViewport = new Map();
    tokenCache.set(source, byViewport);
  }
  const key = `${viewportWidth}/${containerWidth}`;
  if (byViewport.has(key)) return byViewport.get(key);
  const {rules} = sheet(source, viewportWidth, containerWidth);
  const root = element("html", {states: ["root"]});
  const names = new Set();
  for (const rule of rules) {
    if (!rule.selectors.some(selector => matchesSelectorCached(selector, root))) continue;
    for (const declaration of rule.declarations) {
      if (declaration.property.startsWith("--")) names.add(declaration.property);
    }
  }
  const tokens = new Map();
  for (const name of names) {
    const value = resolveProperty(source, root, name, viewportWidth, containerWidth);
    if (value !== undefined) tokens.set(name, value);
  }
  byViewport.set(key, tokens);
  return tokens;
}

/* ------------------------------------------------------------------ *
 * Contrast
 * ------------------------------------------------------------------ */

function token(value, name, viewportWidth = DEFAULT_VIEWPORT, containerWidth = DEFAULT_CONTAINER_WIDTH) {
  const resolved = rootTokens(value, viewportWidth, containerWidth).get(name);
  assert.ok(resolved, `${name} is defined by the final :root cascade at ${viewportWidth}px / ${containerWidth}px`);
  return resolved;
}

function hexToken(value, name, viewportWidth = DEFAULT_VIEWPORT, containerWidth = DEFAULT_CONTAINER_WIDTH) {
  const resolved = token(value, name, viewportWidth, containerWidth);
  assert.match(resolved, /^#[0-9a-f]{6}$/i, `${name} must resolve to a direct hex value, got ${resolved}`);
  return resolved;
}

function resolveVars(value, source, viewportWidth) {
  const tokens = rootTokens(source, viewportWidth);
  let resolved = value;
  for (let pass = 0; pass < 10; pass += 1) {
    const next = resolved.replace(/var\((--[\w-]+)(?:,\s*([^)]*))?\)/g, (_, name, fallback) => {
      const tokenValue = tokens.get(name);
      return tokenValue ?? fallback?.trim() ?? "";
    });
    if (next === resolved) break;
    resolved = next;
  }
  return resolved.trim();
}

function luminance(hex) {
  const channels = [1, 3, 5].map(index => parseInt(hex.slice(index, index + 2), 16) / 255);
  const linear = channels.map(channel => channel <= 0.04045
    ? channel / 12.92
    : Math.pow((channel + 0.055) / 1.055, 2.4));
  return 0.2126 * linear[0] + 0.7152 * linear[1] + 0.0722 * linear[2];
}

function contrast(foreground, background) {
  const first = luminance(foreground);
  const second = luminance(background);
  const lighter = Math.max(first, second);
  const darker = Math.min(first, second);
  return (lighter + 0.05) / (darker + 0.05);
}

function assertContrastAtLeast(foreground, background, minimum, label) {
  const ratio = contrast(foreground, background);
  assert.ok(ratio >= minimum, `${label} is ${ratio.toFixed(2)}:1, expected >= ${minimum}:1`);
}

/* ------------------------------------------------------------------ *
 * Elements under test
 * ------------------------------------------------------------------ */

const BUTTON = element("button", {type: "button"});
const PRIMARY = element("button", {classes: ["primary"], type: "button"});
const SECONDARY = element("button", {classes: ["secondary"], type: "button"});
const GHOST = element("button", {classes: ["ghost"], type: "button"});
const DANGER = element("button", {classes: ["danger"], type: "button"});
const DEFAULT_HOVER = element("button", {states: ["hover"], type: "button"});
const INPUT = element("input", {type: "text"});
const SELECT = element("select", {type: "select-one"});
const TABLE_HEADER = element("th");
const PANEL = element("section", {classes: ["panel"]});
const STATUS = element("span", {classes: ["status"]});
const STATUS_GOOD = element("span", {classes: ["status", "good"]});
const BANNER = element("div", {classes: ["banner"]});
const BANNER_ERROR = element("div", {classes: ["banner", "error"]});
const BANNER_SUCCESS = element("div", {classes: ["banner", "success"]});
const DIALOG = element("dialog");
const SIDEBAR = element("aside", {classes: ["sidebar"]});
const LOGIN_BRAND = element("div", {classes: ["login-brand"]});

const SWATCH_HOVER = element("button", {
  classes: ["sidebar-memo-swatch"],
  states: ["hover"],
  type: "button",
  parent: element("div", {classes: ["sidebar-memo"], parent: SIDEBAR}),
});
const HARDWARE_ACTIVE = element("button", {
  classes: ["hardware-toggle", "active"],
  states: ["hover"],
  type: "button",
});
const CHARACTER_HOVER = element("button", {
  classes: ["assistant-character-button"],
  states: ["hover"],
  type: "button",
  parent: element("div", {classes: ["assistant-panel"]}),
});
const COMPOSER_BUTTON = element("button", {
  states: ["hover"],
  type: "button",
  parent: element("div", {classes: ["assistant-composer"]}),
});

const SIDEBAR_NAV_FOCUS = element("button", {
  states: ["focus-visible"],
  type: "button",
  parent: element("nav", {parent: SIDEBAR}),
});
const BOTTOM_NAV_FOCUS = element("button", {
  states: ["focus-visible"],
  type: "button",
  parent: element("nav", {classes: ["bottom-nav"]}),
});

const SIDEBAR_PENDING = element("button", {
  classes: ["has-pending"],
  type: "button",
  parent: element("nav", {parent: SIDEBAR}),
});
const SIDEBAR_PENDING_ACTIVE = element("button", {
  classes: ["has-pending", "active"],
  type: "button",
  parent: element("nav", {parent: SIDEBAR}),
});
const BOTTOM_PENDING = element("button", {
  classes: ["has-pending"],
  type: "button",
  parent: element("nav", {classes: ["bottom-nav"]}),
});
const BOTTOM_PENDING_ACTIVE = element("button", {
  classes: ["has-pending", "active"],
  type: "button",
  parent: element("nav", {classes: ["bottom-nav"]}),
});
const BOTTOM_BADGE = element("span", {
  classes: ["task-tool-badge"],
  parent: BOTTOM_PENDING,
});
const SIDEBAR_BADGE = element("span", {
  classes: ["task-tool-badge"],
  parent: SIDEBAR_PENDING,
});

function style(source, subject, property, viewportWidth = DEFAULT_VIEWPORT, containerWidth = DEFAULT_CONTAINER_WIDTH) {
  return resolveProperty(source, subject, property, viewportWidth, containerWidth);
}

/* ------------------------------------------------------------------ *
 * Reusable health checks. The mutation test runs these against tampered
 * stylesheets and asserts that each one throws.
 * ------------------------------------------------------------------ */

function forEachViewport(check) {
  for (const viewport of VIEWPORTS) check(viewport);
}

function assertPrimaryHealthy(source = css) {
  forEachViewport(viewport => {
    assert.equal(style(source, PRIMARY, "background", viewport), "var(--color-accent)", `primary background at ${viewport}px`);
    assert.equal(style(source, PRIMARY, "color", viewport), "var(--color-text-inverse)", `primary color at ${viewport}px`);
    assertContrastAtLeast(
      resolveVars(style(source, PRIMARY, "color", viewport), source, viewport),
      resolveVars(style(source, PRIMARY, "background", viewport), source, viewport),
      4.5,
      `primary text at ${viewport}px`,
    );
  });
}

function assertNavFocusHealthy(source = css) {
  forEachViewport(viewport => {
    assert.equal(style(source, SIDEBAR_NAV_FOCUS, "outline-color", viewport), "var(--focus-ring-nav-color)", `sidebar focus at ${viewport}px`);
    assert.equal(style(source, BOTTOM_NAV_FOCUS, "outline-color", viewport), "var(--focus-ring-color)", `bottom-nav focus at ${viewport}px`);
    const sidebarFocus = resolveVars(style(source, SIDEBAR_NAV_FOCUS, "outline-color", viewport), source, viewport);
    assertContrastAtLeast(sidebarFocus, token(source, "--color-nav", viewport), 3, `sidebar focus on nav at ${viewport}px`);
    assertContrastAtLeast(sidebarFocus, token(source, "--color-nav-active", viewport), 3, `sidebar focus on active nav at ${viewport}px`);
    const bottomFocus = resolveVars(style(source, BOTTOM_NAV_FOCUS, "outline-color", viewport), source, viewport);
    assertContrastAtLeast(bottomFocus, token(source, "--color-surface", viewport), 3, `bottom-nav focus at ${viewport}px`);
  });
}

function assertPendingHealthy(source = css) {
  forEachViewport(viewport => {
    assert.equal(style(source, BOTTOM_BADGE, "background", viewport), "var(--color-warning)", `badge background at ${viewport}px`);
    assert.equal(style(source, BOTTOM_BADGE, "color", viewport), "var(--color-text-inverse)", `badge text at ${viewport}px`);
    assert.equal(style(source, SIDEBAR_BADGE, "box-shadow", viewport), "0 0 0 1px var(--color-warning-bright)", `sidebar badge ring at ${viewport}px`);
    assert.equal(style(source, BOTTOM_PENDING, "color", viewport), "var(--color-warning-ink)", `bottom pending at ${viewport}px`);
    assert.equal(style(source, BOTTOM_PENDING_ACTIVE, "color", viewport), "var(--color-warning-ink)", `bottom pending active at ${viewport}px`);
    assert.equal(style(source, SIDEBAR_PENDING, "color", viewport), "var(--color-warning-bright)", `sidebar pending at ${viewport}px`);
    assert.equal(style(source, SIDEBAR_PENDING_ACTIVE, "color", viewport), "var(--color-warning-bright)", `sidebar pending active at ${viewport}px`);
    assert.equal(style(source, SIDEBAR_BADGE, "background", viewport), "var(--color-warning)", `sidebar badge background at ${viewport}px`);

    const surface = token(source, "--color-surface", viewport);
    const nav = token(source, "--color-nav", viewport);
    const navActive = token(source, "--color-nav-active", viewport);
    assertContrastAtLeast(resolveVars(style(source, BOTTOM_PENDING, "color", viewport), source, viewport), surface, 4.5, `bottom pending at ${viewport}px`);
    assertContrastAtLeast(resolveVars(style(source, BOTTOM_PENDING_ACTIVE, "color", viewport), source, viewport), surface, 4.5, `bottom pending active at ${viewport}px`);
    assertContrastAtLeast(resolveVars(style(source, BOTTOM_BADGE, "color", viewport), source, viewport), resolveVars(style(source, BOTTOM_BADGE, "background", viewport), source, viewport), 4.5, `badge text at ${viewport}px`);
    assertContrastAtLeast(resolveVars(style(source, SIDEBAR_PENDING, "color", viewport), source, viewport), nav, 4.5, `sidebar pending at ${viewport}px`);
    assertContrastAtLeast(resolveVars(style(source, SIDEBAR_PENDING_ACTIVE, "color", viewport), source, viewport), navActive, 4.5, `sidebar pending active at ${viewport}px`);
    const badgeRing = colorFromShorthand(resolveVars(style(source, SIDEBAR_BADGE, "box-shadow", viewport), source, viewport));
    assert.ok(badgeRing, `sidebar badge ring color at ${viewport}px`);
    assertContrastAtLeast(badgeRing, nav, 3, `sidebar badge ring on nav at ${viewport}px`);
    assertContrastAtLeast(badgeRing, navActive, 3, `sidebar badge ring on active nav at ${viewport}px`);
  });
}

function assertComponentStatesHealthy(source = css) {
  forEachViewport(viewport => {
    assert.equal(style(source, SWATCH_HOVER, "background", viewport), "var(--swatch-color)", `swatch background at ${viewport}px`);
    assert.equal(style(source, HARDWARE_ACTIVE, "background", viewport), "#dce8ff", `hardware background at ${viewport}px`);
    assert.equal(style(source, HARDWARE_ACTIVE, "color", viewport), "#1d4ed8", `hardware color at ${viewport}px`);
    assert.equal(style(source, CHARACTER_HOVER, "background", viewport), "#eaf4ff", `character background at ${viewport}px`);
    assert.equal(style(source, CHARACTER_HOVER, "border-color", viewport), "#9fc4ee", `character border at ${viewport}px`);
    assert.equal(style(source, COMPOSER_BUTTON, "background", viewport), "#2563eb", `composer background at ${viewport}px`);
    assert.equal(style(source, COMPOSER_BUTTON, "color", viewport), "#fff", `composer color at ${viewport}px`);
    assert.equal(style(source, DEFAULT_HOVER, "--button-bg", viewport), "var(--color-accent-soft)", `default hover background at ${viewport}px`);
    assert.equal(style(source, DEFAULT_HOVER, "--button-border", viewport), "var(--color-accent)", `default hover border at ${viewport}px`);
    assert.equal(style(source, DEFAULT_HOVER, "--button-fg", viewport), "var(--color-accent-ink)", `default hover text at ${viewport}px`);
    const hoverBackground = resolveVars(style(source, DEFAULT_HOVER, "--button-bg", viewport), source, viewport);
    const hoverForeground = resolveVars(style(source, DEFAULT_HOVER, "--button-fg", viewport), source, viewport);
    assertContrastAtLeast(hoverForeground, hoverBackground, 4.5, `default hover text at ${viewport}px`);
  });
}

function assertContrastHealthy(source = css) {
  forEachViewport(viewport => {
    const ratio = contrast(
      hexToken(source, "--color-text-inverse", viewport),
      hexToken(source, "--color-accent", viewport),
    );
    assert.ok(ratio >= 4.5, `inverse/accent contrast at ${viewport}px is ${ratio.toFixed(2)}:1`);
  });
}

/* ------------------------------------------------------------------ *
 * Token assertions
 * ------------------------------------------------------------------ */

test("core colour, type, spacing, radius, elevation and motion tokens exist", () => {
  for (const name of [
    "--color-text", "--color-text-muted", "--color-text-subtle", "--color-text-inverse",
    "--color-canvas", "--color-surface", "--color-surface-raised", "--color-surface-muted",
    "--color-border", "--color-border-strong", "--color-border-subtle", "--color-overlay",
    "--color-accent", "--color-accent-hover", "--color-accent-active", "--color-accent-soft", "--color-accent-ink",
    "--color-success", "--color-success-soft", "--color-success-ink", "--color-success-bright",
    "--color-warning", "--color-warning-soft", "--color-warning-ink", "--color-warning-bright",
    "--color-danger", "--color-danger-soft", "--color-danger-ink",
    "--color-info", "--color-info-soft", "--color-info-ink",
    "--color-nav", "--color-nav-text", "--color-nav-muted", "--color-nav-active", "--color-nav-border",
  ]) {
    token(css, name);
  }

  const fontSize = {
    "--font-size-11": "11px", "--font-size-12": "12px", "--font-size-13": "13px",
    "--font-size-14": "14px", "--font-size-16": "16px", "--font-size-20": "20px", "--font-size-26": "26px",
  };
  for (const [name, value] of Object.entries(fontSize)) assert.equal(token(css, name), value);

  const spacing = {
    "--space-1": "4px", "--space-2": "8px", "--space-3": "12px", "--space-4": "16px",
    "--space-5": "24px", "--space-6": "32px", "--space-7": "48px",
  };
  for (const [name, value] of Object.entries(spacing)) assert.equal(token(css, name), value);

  assert.equal(token(css, "--radius-control"), "6px");
  assert.equal(token(css, "--radius-card"), "10px");
  assert.equal(token(css, "--radius-popover"), "14px");
  assert.equal(token(css, "--radius-pill"), "999px");

  assert.match(token(css, "--shadow-xs"), /^0 1px 2px /);
  assert.match(token(css, "--shadow-popover"), /^0 12px 32px /);
  assert.match(token(css, "--shadow-dialog"), /^0 24px 64px /);

  assert.match(token(css, "--focus-ring-color"), /^#[0-9a-f]{6}$/i);
  assert.match(token(css, "--focus-ring"), /^0 0 0 3px /);
  assert.match(token(css, "--focus-ring-nav-color"), /^#[0-9a-f]{6}$/i);
  assert.match(token(css, "--focus-ring-nav"), /^0 0 0 3px /);
  assert.equal(token(css, "--duration-fast"), "120ms");
  assert.equal(token(css, "--duration-base"), "160ms");
  assert.equal(token(css, "--duration-slow"), "180ms");
  assert.match(token(css, "--font-mono"), /ui-monospace/);
  assert.doesNotMatch(css, /prefers-color-scheme:\s*dark/, "dark mode stays out of phase 2");
});

/* ------------------------------------------------------------------ *
 * Component consumption
 * ------------------------------------------------------------------ */

test("base components consume tokens instead of hard-coded colours", () => {
  assert.equal(style(css, BUTTON, "--button-bg"), "var(--color-surface)");
  assert.equal(style(css, BUTTON, "--button-border"), "var(--color-border-strong)");
  assert.equal(style(css, BUTTON, "--button-fg"), "var(--color-text)");
  assert.equal(style(css, BUTTON, "border"), "1px solid var(--button-border)");
  assert.equal(style(css, BUTTON, "border-radius"), "var(--radius-control)");
  assert.equal(style(css, BUTTON, "background"), "var(--button-bg)");
  assert.equal(style(css, BUTTON, "color"), "var(--button-fg)");

  for (const [subject, property, expected] of [
    [SECONDARY, "border-color", "var(--color-border-strong)"],
    [GHOST, "color", "var(--color-text-muted)"],
    [DANGER, "color", "var(--color-danger)"],
  ]) {
    assert.equal(style(css, subject, property), expected);
  }
  assertPrimaryHealthy();

  assert.equal(style(css, INPUT, "border"), "1px solid var(--color-border-strong)");
  assert.equal(style(css, INPUT, "border-radius"), "var(--radius-control)");
  assert.equal(style(css, INPUT, "background"), "var(--color-surface)");
  assert.equal(style(css, SELECT, "padding"), "0 32px 0 4px");
  assert.match(css, /input:focus-visible, select:focus-visible, textarea:focus-visible \{[^}]*box-shadow: var\(--focus-ring\)/);

  assert.equal(style(css, TABLE_HEADER, "border-bottom"), "1px solid var(--color-border)");
  assert.equal(style(css, PANEL, "border-radius"), "var(--radius-card)");
  assert.equal(style(css, PANEL, "background"), "var(--color-surface)");
  assert.equal(style(css, STATUS, "border-radius"), "var(--radius-pill)");
  assert.equal(style(css, STATUS_GOOD, "background"), "var(--color-success-soft)");
  assert.equal(style(css, BANNER_ERROR, "background"), "var(--color-danger-soft)");
  assert.equal(style(css, BANNER_SUCCESS, "background"), "var(--color-success-soft)");
  assert.equal(style(css, DIALOG, "border-radius"), "var(--radius-popover)");
  assert.equal(style(css, DIALOG, "box-shadow"), "var(--shadow-dialog)");
  assert.equal(style(css, SIDEBAR, "background"), "var(--color-nav)");
  assert.equal(style(css, LOGIN_BRAND, "background"), "var(--color-nav)");
});

test("base hover cannot steal component hover and active states", () => {
  assertComponentStatesHealthy();
  assertNavFocusHealthy();
  assertPendingHealthy();
});

/* ------------------------------------------------------------------ *
 * Cascade regression checks
 * ------------------------------------------------------------------ */

test("winning declarations cover primary buttons, nav focus and pending badges", () => {
  assertPrimaryHealthy();
  assertNavFocusHealthy();
  assertPendingHealthy();
  assertContrastHealthy();
});

test("the cascade model catches known override patterns", () => {
  const primaryOverride = source => assert.equal(style(source, PRIMARY, "background"), "#fff");
  const mutations = [
    ["higher specificity", "button.primary.primary { background: #fff; color: #fff; }", assertPrimaryHealthy, primaryOverride],
    ["multi-line selector list", "button.primary,\n.primary { background: #fff; color: #fff; }", assertPrimaryHealthy, primaryOverride],
    ["nested at-rule", "@media all { button.primary, .primary { background: #fff; color: #fff; } }", assertPrimaryHealthy, primaryOverride],
    ["important declaration", ".primary { background: #fff !important; color: #fff !important; }", assertPrimaryHealthy, primaryOverride],
    ["higher-specificity nav focus", ".sidebar nav button:focus-visible { outline-color: #fff; }", assertNavFocusHealthy, source => {
      assert.equal(style(source, SIDEBAR_NAV_FOCUS, "outline-color"), "#fff");
    }],
    ["higher-specificity pending badge", ".task-tool-badge.task-tool-badge { background: #fff; color: #fff; }", assertPendingHealthy, source => {
      assert.equal(style(source, BOTTOM_BADGE, "background"), "#fff");
    }],
    ["later :root token override", ":root { --color-accent: #fff; --color-text-inverse: #fff; }", assertContrastHealthy, source => {
      assert.equal(rootTokens(source).get("--color-accent"), "#fff");
      assert.equal(rootTokens(source).get("--color-text-inverse"), "#fff");
    }],
  ];
  for (const [name, mutation, check, verifyOverride] of mutations) {
    const tampered = `${css}\n${mutation}\n`;
    verifyOverride(tampered);
    assert.throws(() => check(tampered), Error, `${name} must fail the cascade check`);
  }

  const oldBaseHover = "button:hover:not(:disabled) { background: #fff; color: #fff; border-color: #fff; }";
  const tampered = `${css}\n${oldBaseHover}\n`;
  for (const subject of [SWATCH_HOVER, HARDWARE_ACTIVE, CHARACTER_HOVER, COMPOSER_BUTTON]) {
    assert.equal(style(tampered, subject, "background"), "#fff", "the injected base hover must win before the fix");
  }
  assert.throws(
    () => assertComponentStatesHealthy(tampered),
    Error,
    "the old direct-property base hover must fail the component state check",
  );
});

test("viewport-scoped overrides are evaluated at 360/375/768/1280/1440", () => {
  const pending360 = `${css}\n@media (max-width:360px){ .bottom-nav button.has-pending{color:#fff} }\n`;
  assert.equal(style(pending360, BOTTOM_PENDING, "color", 360), "#fff");
  for (const viewport of [375, 768, 1280, 1440]) {
    assert.equal(style(pending360, BOTTOM_PENDING, "color", viewport), "var(--color-warning-ink)", `pending 360 mutation at ${viewport}px`);
  }
  assert.throws(() => assertPendingHealthy(pending360), Error, "pending override must fail at 360px");

  const pending = `${css}\n@media (max-width:760px){ .bottom-nav button.has-pending{color:#fff} }\n`;
  for (const viewport of [360, 375]) {
    assert.equal(style(pending, BOTTOM_PENDING, "color", viewport), "#fff", `pending mutation at ${viewport}px`);
  }
  for (const viewport of [768, 1280, 1440]) {
    assert.equal(style(pending, BOTTOM_PENDING, "color", viewport), "var(--color-warning-ink)", `pending at ${viewport}px`);
  }
  assert.throws(() => assertPendingHealthy(pending), Error, "pending override must fail at 360/375px");

  const primary = `${css}\n@media (min-width:1281px){ button.primary{background:#fff;color:#fff} }\n`;
  assert.equal(style(primary, PRIMARY, "background", 1440), "#fff");
  for (const viewport of [360, 375, 768, 1280]) {
    assert.equal(style(primary, PRIMARY, "background", viewport), "var(--color-accent)", `primary at ${viewport}px`);
  }
  assert.throws(() => assertPrimaryHealthy(primary), Error, "primary override must fail at 1440px");

  const focus = `${css}\n@media (max-width:760px){ .bottom-nav button:focus-visible{outline-color:#fff} }\n`;
  for (const viewport of [360, 375]) {
    assert.equal(style(focus, BOTTOM_NAV_FOCUS, "outline-color", viewport), "#fff", `focus mutation at ${viewport}px`);
  }
  for (const viewport of [768, 1280, 1440]) {
    assert.equal(style(focus, BOTTOM_NAV_FOCUS, "outline-color", viewport), "var(--focus-ring-color)", `focus at ${viewport}px`);
  }
  assert.throws(() => assertNavFocusHealthy(focus), Error, "focus override must fail at 360/375px");

  const badge = `${css}\n@media (min-width:1281px){ .task-tool-badge{background:#fff;color:#fff} }\n`;
  assert.equal(style(badge, BOTTOM_BADGE, "background", 1440), "#fff");
  for (const viewport of [360, 375, 768, 1280]) {
    assert.equal(style(badge, BOTTOM_BADGE, "background", viewport), "var(--color-warning)", `badge at ${viewport}px`);
  }
  assert.throws(() => assertPendingHealthy(badge), Error, "badge override must fail at 1440px");
});

test("attribute selectors match only attributes present on the fixture", () => {
  const classMutation = `${css}\nbutton[class]{background:#fff;color:#fff}\n`;
  assert.equal(style(classMutation, PRIMARY, "background"), "#fff");
  assert.equal(style(classMutation, BUTTON, "background"), "var(--button-bg)");
  assert.throws(() => assertPrimaryHealthy(classMutation), Error);

  const dataTaskAbsent = `${css}\nbutton[data-task]{background:#fff;color:#fff}\n`;
  assert.equal(style(dataTaskAbsent, PRIMARY, "background"), "var(--color-accent)");
  assertPrimaryHealthy(dataTaskAbsent);

  const withDataTask = element("button", {
    classes: ["primary"],
    type: "button",
    attrs: {"data-task": "task-1"},
  });
  assert.equal(style(dataTaskAbsent, withDataTask, "background"), "#fff");

  const ariaButton = element("button", {type: "button", attrs: {"aria-current": "page"}});
  const ariaMutation = `${css}\nbutton[aria-current="page"]{color:#fff}\n`;
  assert.equal(style(ariaMutation, ariaButton, "color"), "#fff");
  assert.equal(style(ariaMutation, BUTTON, "color"), "var(--button-fg)");
});

test("@layer follows layer order, unlayered priority and important reversal", () => {
  const layeredNormal = `${css}\n@layer base { button.primary { background:#fff; color:#fff; } }\n`;
  assert.equal(style(layeredNormal, PRIMARY, "background"), "var(--color-accent)");
  assertPrimaryHealthy(layeredNormal);

  const layeredImportant = `${css}\n@layer base { button.primary { background:#fff !important; color:#fff !important; } }\n`;
  assert.equal(style(layeredImportant, PRIMARY, "background"), "#fff");
  assert.throws(() => assertPrimaryHealthy(layeredImportant), Error);

  const laterLayer = `${css}\n@layer first, second;\n@layer second { .primary { outline-color:#fff; } }\n@layer first { .primary { outline-color:#eee; } }\n`;
  assert.equal(style(laterLayer, PRIMARY, "outline-color"), "#fff");

  const importantLayerReversal = `${css}\n@layer first, second;\n@layer second { .primary { outline-color:#fff !important; } }\n@layer first { .primary { outline-color:#eee !important; } }\n`;
  assert.equal(style(importantLayerReversal, PRIMARY, "outline-color"), "#eee");
});

test("@supports evaluates the modeled declaration conditions", () => {
  const supported = `${css}\n@supports (display: grid) { button.primary { background:#fff; color:#fff; } }\n`;
  assert.equal(style(supported, PRIMARY, "background"), "#fff");
  assert.throws(() => assertPrimaryHealthy(supported), Error);

  const unsupportedProperty = `${css}\n@supports (not-a-real-property: 1) { button.primary { background:#fff; color:#fff; } }\n`;
  assert.equal(style(unsupportedProperty, PRIMARY, "background"), "var(--color-accent)");
  assertPrimaryHealthy(unsupportedProperty);

  const unsupportedValue = `${css}\n@supports (display: unsupported-value) { button.primary { background:#fff; color:#fff; } }\n`;
  assert.equal(style(unsupportedValue, PRIMARY, "background"), "var(--color-accent)");

  const negated = `${css}\n@supports not (display: grid) { button.primary { background:#fff; color:#fff; } }\n`;
  assert.equal(style(negated, PRIMARY, "background"), "var(--color-accent)");
});

test("@container evaluates known names at 360 and 900 container widths", () => {
  const applies = `${css}\n@container (min-width: 500px) { button.primary { background:#fff; color:#fff; } }\n`;
  assert.equal(style(applies, PRIMARY, "background", 1280, 900), "#fff");
  assert.equal(style(applies, PRIMARY, "background", 1280, 360), "var(--color-accent)");
  assert.throws(() => assertPrimaryHealthy(applies), Error);

  const doesNotApply = `${css}\n@container (min-width: 1000px) { button.primary { background:#fff; color:#fff; } }\n`;
  assert.equal(style(doesNotApply, PRIMARY, "background", 1280, 900), "var(--color-accent)");
  assert.equal(style(doesNotApply, PRIMARY, "background", 1280, 360), "var(--color-accent)");
  assertPrimaryHealthy(doesNotApply);

  const namedNarrow = `${css}\n@container shared-desktop (max-width: 360px) { button.primary { background:#fff; color:#fff; } }\n`;
  assert.equal(style(namedNarrow, PRIMARY, "background", 1280, 360), "#fff");
  assert.equal(style(namedNarrow, PRIMARY, "background", 1280, 900), "var(--color-accent)");

  const namedWide = `${css}\n@container shared-desktop (min-width: 500px) { button.primary { background:#fff; color:#fff; } }\n`;
  assert.equal(style(namedWide, PRIMARY, "background", 1280, 360), "var(--color-accent)");
  assert.equal(style(namedWide, PRIMARY, "background", 1280, 900), "#fff");

  const unknownName = `${css}\n@container card (max-width: 1000px) { button.primary { background:#fff; color:#fff; } }\n`;
  assert.throws(() => style(unknownName, PRIMARY, "background", 1280, 900), /unmodeled container card/);
  assert.throws(() => style(unknownName, PRIMARY, "background", 1280, 360), /unmodeled container card/);

  const unknownNameLayoutOnly = `${css}\n@container card (max-width: 1000px) { .unrelated { margin:0; width:10px; } }\n`;
  assert.equal(style(unknownNameLayoutOnly, PRIMARY, "background", 1280, 900), "var(--color-accent)");

  assert.equal(style(css, PRIMARY, "background", 1280, 360), "var(--color-accent)");
  assert.equal(style(css, PRIMARY, "background", 1280, 900), "var(--color-accent)");
});

test("unmodeled at-rules fail when their block asserts a tracked property", () => {
  for (const rule of [
    "button.primary { background:#fff; }",
    ".panel { background:#fff; }",
    "input { color:#fff; }",
    ".sidebar-memo-swatch { background:#fff; }",
    ".unrelated { font-size:12px; }",
    ".unrelated { --color-accent:#fff; }",
  ]) {
    const unknown = `${css}\n@scope (.app) { ${rule} }\n`;
    assert.throws(() => style(unknown, PRIMARY, "background"), /unmodeled at-rule @scope/, rule);
  }

  const layoutOnly = `${css}\n@scope (.app) { .unrelated { margin:0; width:10px; } }\n`;
  assert.equal(style(layoutOnly, PRIMARY, "background"), "var(--color-accent)");

  const unrelatedAtRule = `${css}\n@starting-style { .unrelated { opacity: 0; } }\n`;
  assert.equal(style(unrelatedAtRule, PRIMARY, "background"), "var(--color-accent)");
});

/* ------------------------------------------------------------------ *
 * Contrast
 * ------------------------------------------------------------------ */

test("text, navigation and status pairs meet WCAG AA contrast", () => {
  const pairs = [
    ["--color-text", "--color-canvas", 4.5],
    ["--color-text", "--color-surface", 4.5],
    ["--color-text-muted", "--color-canvas", 4.5],
    ["--color-text-muted", "--color-surface", 4.5],
    ["--color-text-subtle", "--color-canvas", 4.5],
    ["--color-text-inverse", "--color-accent", 4.5],
    ["--color-accent-ink", "--color-accent-soft", 4.5],
    ["--color-success-ink", "--color-success-soft", 4.5],
    ["--color-warning-ink", "--color-warning-soft", 4.5],
    ["--color-danger-ink", "--color-danger-soft", 4.5],
    ["--color-info-ink", "--color-info-soft", 4.5],
    ["--color-amber-ink", "--color-amber-soft", 4.5],
    ["--color-nav-text", "--color-nav", 4.5],
    ["--color-nav-muted", "--color-nav", 4.5],
    ["--color-success-bright", "--color-nav", 4.5],
    ["--color-warning-bright", "--color-nav", 4.5],
    ["--color-warning-bright", "--color-nav-active", 4.5],
    ["--color-warning-ink", "--color-surface", 4.5],
    ["--color-text-inverse", "--color-warning", 4.5],
    ["--focus-ring-color", "--color-surface", 3],
    ["--focus-ring-nav-color", "--color-nav", 3],
    ["--focus-ring-nav-color", "--color-nav-active", 3],
  ];
  for (const viewport of VIEWPORTS) {
    for (const [foreground, background, minimum] of pairs) {
      const ratio = contrast(hexToken(css, foreground, viewport), hexToken(css, background, viewport));
      assert.ok(ratio >= minimum, `${foreground} on ${background} at ${viewport}px is ${ratio.toFixed(2)}:1, expected >= ${minimum}:1`);
    }
  }
});

/* ------------------------------------------------------------------ *
 * Focus, motion and non-colour status cues
 * ------------------------------------------------------------------ */

test("focus and reduced-motion affordances are present", () => {
  assert.match(css, /:where\([^)]*\):focus-visible \{[^}]*outline: 2px solid var\(--focus-ring-color\)/);
  assert.match(css, /\.sidebar :focus-visible, \.login-brand :focus-visible \{ outline-color: var\(--focus-ring-nav-color\)/);
  assert.match(css, /\.bottom-nav button:focus-visible \{ outline-color: var\(--focus-ring-color\)/);
  const reduced = css.match(/@media \(prefers-reduced-motion: reduce\) \{([\s\S]*?)\n\}/);
  assert.ok(reduced, "prefers-reduced-motion block exists");
  assert.match(reduced[1], /animation-duration: 1ms !important/);
  assert.match(reduced[1], /transition-duration: 1ms !important/);
  assert.match(reduced[1], /scroll-behavior: auto !important/);
});

test("status is not conveyed by colour alone", () => {
  assert.equal(style(css, STATUS, "display"), "inline-flex");
  assert.equal(style(css, BANNER, "box-shadow"), "inset 3px 0 0 currentColor");
  assert.match(css, /\.status::before \{[^}]*width: 6px/);
  assert.match(css, /\.status::before \{[^}]*background: currentColor/);
});
