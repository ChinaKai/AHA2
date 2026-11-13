import test from "node:test";
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";

const repo = resolve(import.meta.dirname, "..", "..");
const css = await readFile(resolve(repo, "web", "src", "styles.css"), "utf8");

// The cascade checks model a 1280px desktop viewport. Conditional media
// queries that do not match that viewport are parsed but do not contribute a
// declaration, so the result is the same cascade the desktop UI sees.
//
// Supported selector surface: type/class/id/attribute selectors, descendant
// and child combinators, :not/:is/:where/:root, ::pseudo-elements and the UI
// state pseudo-classes used by these targets. Sibling combinators and :has
// are not modeled because no assertion below depends on them.
const VIEWPORT_WIDTH = 1280;

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

function evaluateMedia(condition) {
  return splitTopLevel(condition, ",").some(query => {
    const normalized = query.trim().toLowerCase();
    if (!normalized || normalized === "all" || normalized === "screen") return true;
    if (normalized === "print") return false;
    return normalized.split(/\s+and\s+/).every(feature => {
      const item = feature.trim();
      if (item === "all" || item === "screen") return true;
      if (item === "print") return false;
      const minWidth = item.match(/^\(min-width:\s*(\d+(?:\.\d+)?)px\)$/);
      if (minWidth) return VIEWPORT_WIDTH >= Number(minWidth[1]);
      const maxWidth = item.match(/^\(max-width:\s*(\d+(?:\.\d+)?)px\)$/);
      if (maxWidth) return VIEWPORT_WIDTH <= Number(maxWidth[1]);
      if (item.includes("prefers-reduced-motion")) return false;
      if (item.includes("prefers-color-scheme")) return false;
      // Unknown features do not match the modeled desktop baseline.
      return false;
    });
  });
}

function parseStylesheet(source) {
  const clean = stripComments(source);
  const rules = [];
  let order = 0;

  function parseBlock(text, applies) {
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
        index += 1;
        continue;
      }

      const blockEnd = findMatchingBrace(text, index);
      const block = text.slice(index + 1, blockEnd);
      index = blockEnd + 1;

      if (prelude.startsWith("@")) {
        const atMatch = prelude.match(/^@([\w-]+)/);
        const atName = atMatch?.[1]?.toLowerCase() ?? "";
        if (atName === "media" || atName === "supports" || atName === "layer") {
          const condition = prelude.slice(atMatch[0].length).trim();
          const nestedApplies = applies && (atName !== "media" || evaluateMedia(condition));
          parseBlock(block, nestedApplies);
        }
        // @keyframes, @font-face, @page and other at-rules do not contribute
        // element declarations, so their blocks are skipped.
        continue;
      }

      if (!applies) continue;
      const selectors = splitTopLevel(prelude, ",")
        .map(selector => selector.replace(/\s+/g, " ").trim())
        .filter(Boolean);
      const declarations = parseDeclarations(block);
      for (const declaration of declarations) declaration.order = order++;
      if (selectors.length && declarations.length) rules.push({selectors, declarations});
    }
  }

  parseBlock(clean, true);
  return {rules};
}

const sheetCache = new Map();
function sheet(source) {
  if (!sheetCache.has(source)) sheetCache.set(source, parseStylesheet(source));
  return sheetCache.get(source);
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

function element(tag, {classes = [], states = [], attrs = {}, id = null, parent = null} = {}) {
  return {
    tag: tag.toLowerCase(),
    id,
    classes: new Set(classes),
    states: new Set(states),
    attrs: new Map(Object.entries(attrs).map(([name, value]) => [name.toLowerCase(), value])),
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

function resolveProperty(source, element, property) {
  const {rules} = sheet(source);
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
      };
      if (isBetter(candidate, winner)) winner = candidate;
    }
  }
  return winner?.value;
}

const tokenCache = new Map();
function rootTokens(source) {
  if (tokenCache.has(source)) return tokenCache.get(source);
  const {rules} = sheet(source);
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
    const value = resolveProperty(source, root, name);
    if (value !== undefined) tokens.set(name, value);
  }
  tokenCache.set(source, tokens);
  return tokens;
}

/* ------------------------------------------------------------------ *
 * Contrast
 * ------------------------------------------------------------------ */

function token(value, name) {
  const resolved = rootTokens(value).get(name);
  assert.ok(resolved, `${name} is defined by the final :root cascade`);
  return resolved;
}

function hexToken(value, name) {
  const resolved = token(value, name);
  assert.match(resolved, /^#[0-9a-f]{6}$/i, `${name} must resolve to a direct hex value, got ${resolved}`);
  return resolved;
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

/* ------------------------------------------------------------------ *
 * Elements under test
 * ------------------------------------------------------------------ */

const PRIMARY = element("button", {classes: ["primary"]});
const SECONDARY = element("button", {classes: ["secondary"]});
const GHOST = element("button", {classes: ["ghost"]});
const DANGER = element("button", {classes: ["danger"]});
const DEFAULT_HOVER = element("button", {states: ["hover"]});
const INPUT = element("input");
const SELECT = element("select");
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
  parent: element("div", {classes: ["sidebar-memo"], parent: SIDEBAR}),
});
const HARDWARE_ACTIVE = element("button", {
  classes: ["hardware-toggle", "active"],
  states: ["hover"],
});
const CHARACTER_HOVER = element("button", {
  classes: ["assistant-character-button"],
  states: ["hover"],
  parent: element("div", {classes: ["assistant-panel"]}),
});
const COMPOSER_BUTTON = element("button", {
  states: ["hover"],
  parent: element("div", {classes: ["assistant-composer"]}),
});

const SIDEBAR_NAV_FOCUS = element("button", {
  states: ["focus-visible"],
  parent: element("nav", {parent: SIDEBAR}),
});
const BOTTOM_NAV_FOCUS = element("button", {
  states: ["focus-visible"],
  parent: element("nav", {classes: ["bottom-nav"]}),
});

const SIDEBAR_PENDING = element("button", {
  classes: ["has-pending"],
  parent: element("nav", {parent: SIDEBAR}),
});
const SIDEBAR_PENDING_ACTIVE = element("button", {
  classes: ["has-pending", "active"],
  parent: element("nav", {parent: SIDEBAR}),
});
const BOTTOM_PENDING = element("button", {
  classes: ["has-pending"],
  parent: element("nav", {classes: ["bottom-nav"]}),
});
const BOTTOM_PENDING_ACTIVE = element("button", {
  classes: ["has-pending", "active"],
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

function style(source, subject, property) {
  return resolveProperty(source, subject, property);
}

/* ------------------------------------------------------------------ *
 * Reusable health checks. The mutation test runs these against tampered
 * stylesheets and asserts that each one throws.
 * ------------------------------------------------------------------ */

function assertPrimaryHealthy(source = css) {
  assert.equal(style(source, PRIMARY, "background"), "var(--color-accent)");
  assert.equal(style(source, PRIMARY, "color"), "var(--color-text-inverse)");
}

function assertNavFocusHealthy(source = css) {
  assert.equal(style(source, SIDEBAR_NAV_FOCUS, "outline-color"), "var(--focus-ring-nav-color)");
  assert.equal(style(source, BOTTOM_NAV_FOCUS, "outline-color"), "var(--focus-ring-color)");
}

function assertPendingHealthy(source = css) {
  assert.equal(style(source, BOTTOM_BADGE, "background"), "var(--color-warning)");
  assert.equal(style(source, BOTTOM_BADGE, "color"), "var(--color-text-inverse)");
  assert.equal(style(source, SIDEBAR_BADGE, "box-shadow"), "0 0 0 1px var(--color-warning-bright)");
  assert.equal(style(source, BOTTOM_PENDING, "color"), "var(--color-warning-ink)");
  assert.equal(style(source, BOTTOM_PENDING_ACTIVE, "color"), "var(--color-warning-ink)");
  assert.equal(style(source, SIDEBAR_PENDING, "color"), "var(--color-warning-bright)");
  assert.equal(style(source, SIDEBAR_PENDING_ACTIVE, "color"), "var(--color-warning-bright)");
  assert.equal(style(source, SIDEBAR_BADGE, "background"), "var(--color-warning)");
}

function assertComponentStatesHealthy(source = css) {
  assert.equal(style(source, SWATCH_HOVER, "background"), "var(--swatch-color)");
  assert.equal(style(source, HARDWARE_ACTIVE, "background"), "#dce8ff");
  assert.equal(style(source, HARDWARE_ACTIVE, "color"), "#1d4ed8");
  assert.equal(style(source, CHARACTER_HOVER, "background"), "#eaf4ff");
  assert.equal(style(source, CHARACTER_HOVER, "border-color"), "#9fc4ee");
  assert.equal(style(source, COMPOSER_BUTTON, "background"), "#2563eb");
  assert.equal(style(source, COMPOSER_BUTTON, "color"), "#fff");
  assert.equal(style(source, DEFAULT_HOVER, "--button-bg"), "var(--color-accent-soft)");
  assert.equal(style(source, DEFAULT_HOVER, "--button-border"), "var(--color-accent)");
  assert.equal(style(source, DEFAULT_HOVER, "--button-fg"), "var(--color-accent-ink)");
}

function assertContrastHealthy(source = css) {
  const ratio = contrast(
    hexToken(source, "--color-text-inverse"),
    hexToken(source, "--color-accent"),
  );
  assert.ok(ratio >= 4.5, `inverse/accent contrast is ${ratio.toFixed(2)}:1`);
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
  assert.equal(style(css, element("button"), "--button-bg"), "var(--color-surface)");
  assert.equal(style(css, element("button"), "--button-border"), "var(--color-border-strong)");
  assert.equal(style(css, element("button"), "--button-fg"), "var(--color-text)");
  assert.equal(style(css, element("button"), "border"), "1px solid var(--button-border)");
  assert.equal(style(css, element("button"), "border-radius"), "var(--radius-control)");
  assert.equal(style(css, element("button"), "background"), "var(--button-bg)");
  assert.equal(style(css, element("button"), "color"), "var(--button-fg)");

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
  for (const [foreground, background, minimum] of pairs) {
    const ratio = contrast(hexToken(css, foreground), hexToken(css, background));
    assert.ok(ratio >= minimum, `${foreground} on ${background} is ${ratio.toFixed(2)}:1, expected >= ${minimum}:1`);
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
