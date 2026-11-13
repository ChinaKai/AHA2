import test from "node:test";
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";

const repo = resolve(import.meta.dirname, "..", "..");
const css = await readFile(resolve(repo, "web", "src", "styles.css"), "utf8");

function rootBlock() {
  const match = css.match(/:root\s*\{([\s\S]*?)\n\}/);
  assert.ok(match, ":root token block exists");
  return match[1];
}

const root = rootBlock();
const tokens = new Map();
for (const match of root.matchAll(/(--[a-z0-9-]+)\s*:\s*([^;]+);/gi)) {
  tokens.set(match[1], match[2].trim());
}

function token(name) {
  const value = tokens.get(name);
  assert.ok(value, `${name} is defined in :root`);
  return value;
}

function hexToken(name) {
  const value = token(name);
  assert.match(value, /^#[0-9a-f]{6}$/i, `${name} must be a direct hex value, got ${value}`);
  return value;
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

function rule(selector) {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const match = css.match(new RegExp(`(?:^|\\n)${escaped}\\s*\\{([^}]*)\\}`));
  assert.ok(match, `${selector} rule exists`);
  return match[1];
}

test("core colour, type, spacing, radius, elevation and motion tokens exist", () => {
  for (const name of [
    "--color-text", "--color-text-muted", "--color-text-subtle", "--color-text-inverse",
    "--color-canvas", "--color-surface", "--color-surface-raised", "--color-surface-muted",
    "--color-border", "--color-border-strong", "--color-border-subtle", "--color-overlay",
    "--color-accent", "--color-accent-hover", "--color-accent-active", "--color-accent-soft", "--color-accent-ink",
    "--color-success", "--color-success-soft", "--color-success-ink", "--color-success-bright",
    "--color-warning", "--color-warning-soft", "--color-warning-ink",
    "--color-danger", "--color-danger-soft", "--color-danger-ink",
    "--color-info", "--color-info-soft", "--color-info-ink",
    "--color-nav", "--color-nav-text", "--color-nav-muted", "--color-nav-active", "--color-nav-border",
  ]) {
    token(name);
  }

  const fontSize = {
    "--font-size-11": "11px", "--font-size-12": "12px", "--font-size-13": "13px",
    "--font-size-14": "14px", "--font-size-16": "16px", "--font-size-20": "20px", "--font-size-26": "26px",
  };
  for (const [name, value] of Object.entries(fontSize)) assert.equal(token(name), value);

  const spacing = {
    "--space-1": "4px", "--space-2": "8px", "--space-3": "12px", "--space-4": "16px",
    "--space-5": "24px", "--space-6": "32px", "--space-7": "48px",
  };
  for (const [name, value] of Object.entries(spacing)) assert.equal(token(name), value);

  assert.equal(token("--radius-control"), "6px");
  assert.equal(token("--radius-card"), "10px");
  assert.equal(token("--radius-popover"), "14px");
  assert.equal(token("--radius-pill"), "999px");

  assert.match(token("--shadow-xs"), /^0 1px 2px /);
  assert.match(token("--shadow-popover"), /^0 12px 32px /);
  assert.match(token("--shadow-dialog"), /^0 24px 64px /);

  assert.match(token("--focus-ring-color"), /^#[0-9a-f]{6}$/i);
  assert.match(token("--focus-ring"), /^0 0 0 3px /);
  assert.equal(token("--duration-fast"), "120ms");
  assert.equal(token("--duration-base"), "160ms");
  assert.equal(token("--duration-slow"), "180ms");
  assert.match(token("--font-mono"), /ui-monospace/);
  assert.doesNotMatch(css, /prefers-color-scheme:\s*dark/, "dark mode stays out of phase 2");
});

test("base components consume tokens instead of hard-coded colours", () => {
  const button = rule("button");
  assert.match(button, /border: 1px solid var\(--color-border-strong\)/);
  assert.match(button, /border-radius: var\(--radius-control\)/);
  assert.match(button, /background: var\(--color-surface\)/);
  assert.match(button, /color: var\(--color-text\)/);

  for (const selector of ["button.primary, .primary", "button.secondary", "button.ghost", "button.danger"]) {
    rule(selector);
  }
  assert.match(rule("button.primary, .primary"), /background: var\(--color-accent\)/);
  assert.match(rule("button.primary, .primary"), /color: var\(--color-text-inverse\)/);
  assert.match(rule("button.danger"), /color: var\(--color-danger\)/);

  const inputs = rule("input, select, textarea");
  assert.match(inputs, /border: 1px solid var\(--color-border-strong\)/);
  assert.match(inputs, /border-radius: var\(--radius-control\)/);
  assert.match(inputs, /background: var\(--color-surface\)/);
  assert.match(rule("select"), /padding: 0 32px 0 4px/);
  assert.match(css, /input:focus-visible, select:focus-visible, textarea:focus-visible \{[^}]*box-shadow: var\(--focus-ring\)/);

  assert.match(rule("th, td"), /border-bottom: 1px solid var\(--color-border\)/);
  assert.match(rule(".panel"), /border-radius: var\(--radius-card\)/);
  assert.match(rule(".panel"), /background: var\(--color-surface\)/);
  assert.match(rule(".status"), /border-radius: var\(--radius-pill\)/);
  assert.match(rule(".status.good"), /var\(--color-success-soft\)/);
  assert.match(rule(".status.bad"), /var\(--color-danger-soft\)/);
  assert.match(rule(".banner.error"), /var\(--color-danger-soft\)/);
  assert.match(rule(".banner.success"), /var\(--color-success-soft\)/);
  assert.match(rule("dialog"), /border-radius: var\(--radius-popover\)/);
  assert.match(rule("dialog"), /box-shadow: var\(--shadow-dialog\)/);
  assert.match(rule(".sidebar"), /background: var\(--color-nav\)/);
  assert.match(rule(".login-brand"), /background: var\(--color-nav\)/);
});

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
  ];
  for (const [foreground, background, minimum] of pairs) {
    const ratio = contrast(hexToken(foreground), hexToken(background));
    assert.ok(ratio >= minimum, `${foreground} on ${background} is ${ratio.toFixed(2)}:1, expected >= ${minimum}:1`);
  }
});

test("focus and reduced-motion affordances are present", () => {
  assert.match(css, /:where\([^)]*\):focus-visible \{[^}]*outline: 2px solid var\(--focus-ring-color\)/);
  const reduced = css.match(/@media \(prefers-reduced-motion: reduce\) \{([\s\S]*?)\n\}/);
  assert.ok(reduced, "prefers-reduced-motion block exists");
  assert.match(reduced[1], /animation-duration: 1ms !important/);
  assert.match(reduced[1], /transition-duration: 1ms !important/);
  assert.match(reduced[1], /scroll-behavior: auto !important/);
});

test("status is not conveyed by colour alone", () => {
  assert.match(rule(".status"), /display: inline-flex/);
  assert.match(rule(".status::before"), /width: 6px/);
  assert.match(rule(".status::before"), /background: currentColor/);
  assert.match(rule(".banner"), /box-shadow: inset 3px 0 0 currentColor/);
});
