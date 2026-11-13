// Global command palette: index/filter/keyboard pieces plus the storage of
// recently opened Tasks. main.ts owns the DOM shell and the navigation each
// item triggers, so this module stays dependency-free and testable in Node.

export type CommandKind = "task" | "project" | "workspace" | "page" | "action";
export type CommandSectionID = CommandKind | "recent";

export interface CommandItem {
  /** Unique across all sections, e.g. "task:abc" or "page:models". */
  id: string;
  kind: CommandKind;
  /** Primary line, usually the Task title or the page name. */
  label: string;
  /** Secondary line with code/project/status. Also searchable. */
  detail: string;
  /** Extra searchable terms (code without prefix, status label, English name). */
  keywords: string[];
  /** Task ID, Project ID, view name or action ID, interpreted by main.ts. */
  target: string;
}

export interface CommandSection {
  id: CommandSectionID;
  label: string;
  items: CommandItem[];
}

export const COMMAND_GROUP_LABELS: Record<CommandSectionID, string> = {
  tasks: "任务",
  projects: "项目",
  workspaces: "Workspace",
  pages: "页面",
  actions: "动作",
  recent: "最近打开",
};

// Search results show the most specific matches first; empty input shows the
// stable entry points instead.
const SEARCH_SECTION_ORDER: CommandSectionID[] = ["tasks", "projects", "workspaces", "pages", "actions"];

export interface CommandSectionsInput {
  query: string;
  items: CommandItem[];
  recent?: CommandItem[];
  taskLimit?: number;
  otherLimit?: number;
  /** Localized group headings; falls back to the Chinese defaults. */
  labels?: Partial<Record<CommandSectionID, string>>;
}

export function buildCommandSections(input: CommandSectionsInput): CommandSection[] {
  const needle = input.query.trim().toLowerCase();
  if (!needle) {
    return [
      makeSection("pages", input.items.filter(item => item.kind === "page"), input.labels),
      makeSection("actions", input.items.filter(item => item.kind === "action"), input.labels),
      makeSection("recent", (input.recent || []).slice(0, 5), input.labels),
    ].filter(section => section.items.length > 0);
  }
  const taskLimit = Math.max(1, input.taskLimit ?? 8);
  const otherLimit = Math.max(1, input.otherLimit ?? 5);
  const ranked = input.items
    .map(item => ({item, score: commandMatchScore(item, needle)}))
    .filter(entry => entry.score > 0)
    .sort((a, b) => b.score - a.score);
  return SEARCH_SECTION_ORDER.map(id => {
    const kind = id.slice(0, -1) as CommandKind;
    const limit = kind === "task" ? taskLimit : otherLimit;
    return makeSection(id, ranked.filter(entry => entry.item.kind === kind).slice(0, limit).map(entry => entry.item), input.labels);
  }).filter(section => section.items.length > 0);
}

function makeSection(id: CommandSectionID, items: CommandItem[], labels?: Partial<Record<CommandSectionID, string>>): CommandSection {
  return {id, label: labels?.[id] || COMMAND_GROUP_LABELS[id], items};
}

// Exact matches beat prefixes, prefixes beat substrings. An empty query only
// needs the score to be non-zero, so callers never treat it as "no match".
export function commandMatchScore(item: CommandItem, query: string): number {
  const needle = query.trim().toLowerCase();
  if (!needle) return 1;
  const fields = [item.label, item.detail, ...(item.keywords || [])];
  let best = 0;
  for (const field of fields) {
    const value = String(field || "").toLowerCase();
    if (!value) continue;
    if (value === needle) return 100;
    if (value.startsWith(needle)) best = Math.max(best, 80);
    else if (value.includes(needle)) best = Math.max(best, 40);
  }
  return best;
}

export function flattenCommandSections(sections: CommandSection[]): CommandItem[] {
  const items: CommandItem[] = [];
  for (const section of sections) items.push(...section.items);
  return items;
}

export function activeCommandItem(sections: CommandSection[], activeIndex: number): CommandItem | null {
  const items = flattenCommandSections(sections);
  if (!items.length) return null;
  const index = ((activeIndex % items.length) + items.length) % items.length;
  return items[index];
}

export interface CommandKeyEvent {
  key: string;
  isComposing?: boolean;
  keyCode?: number;
  ctrlKey?: boolean;
  metaKey?: boolean;
  altKey?: boolean;
  shiftKey?: boolean;
}

export interface CommandKeyContext {
  count: number;
  activeIndex: number;
  composing?: boolean;
  /**
   * WebKit commits a composition by firing compositionend before the Enter
   * keydown, so that keydown has isComposing=false and keyCode=13. Treat an
   * Enter within the post-composition guard window as part of the commit.
   */
  justComposed?: boolean;
}

export interface CommandKeyResult {
  handled: boolean;
  action?: "up" | "down" | "select" | "close" | "none";
  activeIndex?: number;
}

export function isPaletteShortcut(event: CommandKeyEvent): boolean {
  if (event.altKey || event.shiftKey) return false;
  if (!event.ctrlKey && !event.metaKey) return false;
  return String(event.key || "").toLowerCase() === "k";
}

// Chinese IME composition keydowns report keyCode 229 in Chrome and set
// isComposing in every modern browser. Neither Enter nor the arrow keys may act
// until the composition has been committed.
export function paletteKeyAction(event: CommandKeyEvent, context: CommandKeyContext): CommandKeyResult {
  if (context.composing || event.isComposing || event.keyCode === 229) return {handled: false};
  if (context.justComposed && event.key === "Enter") return {handled: false};
  const count = Math.max(0, context.count);
  if (event.key === "Escape") return {handled: true, action: "close"};
  if (event.key === "ArrowDown" || event.key === "ArrowUp") {
    if (!count) return {handled: true, action: "none"};
    const delta = event.key === "ArrowDown" ? 1 : -1;
    const current = ((context.activeIndex % count) + count) % count;
    return {handled: true, action: delta > 0 ? "down" : "up", activeIndex: (current + delta + count) % count};
  }
  if (event.key === "Enter") {
    if (!count) return {handled: true, action: "none"};
    return {handled: true, action: "select", activeIndex: ((context.activeIndex % count) + count) % count};
  }
  return {handled: false};
}

export interface DebouncerTimers {
  setTimeout(callback: () => void, delayMs: number): unknown;
  clearTimeout(handle: unknown): void;
}

export interface Debouncer {
  schedule(value: string): void;
  /** Runs a scheduled filter now; Enter must see the text already typed. */
  flush(): boolean;
  cancel(): void;
  pending(): boolean;
}

export const COMMAND_PALETTE_DEBOUNCE_MS = 100;
// Safari/WebKit fires compositionend before the Enter keydown that commits the
// text. Ignore an Enter this close to compositionend; a later Enter works.
export const COMMAND_PALETTE_COMPOSITION_GUARD_MS = 50;

export function createDebouncer(delayMs: number, run: (value: string) => void, timers?: DebouncerTimers): Debouncer {
  const clock = timers || {
    setTimeout: (callback, delay) => setTimeout(callback, delay),
    clearTimeout: handle => clearTimeout(handle as ReturnType<typeof setTimeout>),
  };
  let handle: unknown = null;
  let pendingValue: string | null = null;
  return {
    schedule(value) {
      if (handle !== null) clock.clearTimeout(handle);
      pendingValue = value;
      handle = clock.setTimeout(() => {
        handle = null;
        const next = pendingValue;
        pendingValue = null;
        if (next !== null) run(next);
      }, delayMs);
    },
    flush() {
      if (handle === null) return false;
      clock.clearTimeout(handle);
      handle = null;
      const next = pendingValue;
      pendingValue = null;
      if (next !== null) run(next);
      return true;
    },
    cancel() {
      if (handle !== null) clock.clearTimeout(handle);
      handle = null;
      pendingValue = null;
    },
    pending() {
      return handle !== null;
    },
  };
}

export interface PaletteSelectionHooks {
  /** When true, focus returns to whatever opened the palette. */
  closePalette(restorePreviousFocus: boolean): void;
  focusTrigger(): void;
  navigate(): void | Promise<void>;
}

// Selecting an item navigates away. The old editor (often a Task composer)
// must lose focus before navigation starts, and must not get it back: main.ts
// defers renders while a #message-form textarea is focused, which would leave
// the previous Task on screen while the new one is already selected in state.
export async function runPaletteSelection(hooks: PaletteSelectionHooks): Promise<void> {
  hooks.closePalette(false);
  hooks.focusTrigger();
  await hooks.navigate();
}

export interface StorageLike {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}

export const RECENT_TASKS_STORAGE_KEY = "aha2.command-palette.recent-tasks";
export const RECENT_TASKS_LIMIT = 5;

export function readRecentTaskIDs(storage: StorageLike | null | undefined, limit = RECENT_TASKS_LIMIT): string[] {
  if (!storage) return [];
  try {
    const parsed = JSON.parse(storage.getItem(RECENT_TASKS_STORAGE_KEY) || "[]");
    if (!Array.isArray(parsed)) return [];
    const ids: string[] = [];
    for (const value of parsed) {
      if (typeof value !== "string" || !value || ids.includes(value)) continue;
      ids.push(value);
      if (ids.length >= limit) break;
    }
    return ids;
  } catch {
    return [];
  }
}

export function rememberRecentTaskID(storage: StorageLike | null | undefined, taskID: string, limit = RECENT_TASKS_LIMIT): string[] {
  if (!storage) return [];
  const previous = readRecentTaskIDs(storage, Number.MAX_SAFE_INTEGER).filter(id => id !== taskID);
  const ids = taskID ? [taskID, ...previous].slice(0, limit) : previous.slice(0, limit);
  try {
    storage.setItem(RECENT_TASKS_STORAGE_KEY, JSON.stringify(ids));
  } catch {
    // A full or disabled localStorage must not break opening a Task.
  }
  return ids;
}
