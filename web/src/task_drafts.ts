import type {Attachment} from "./types.js";

// Drafts are saved per Task *and* per agent. The separator cannot appear in a
// Task or agent identifier, so a Task can never read another Task's draft even
// though both keep an agent called "main".
export const TASK_DRAFT_SEPARATOR = "\u0000";
export const TASK_DRAFT_STORAGE_KEY = "aha2.task-drafts";

export interface TaskDraft {
  text: string;
  attachments: Attachment[];
}

export type TaskDraftStore = Record<string, TaskDraft>;

export function taskDraftKey(taskID: string, agentID: string): string {
  return `${taskID}${TASK_DRAFT_SEPARATOR}${agentID || "main"}`;
}

function validAttachment(value: unknown): value is Attachment {
  const item = value as Partial<Attachment> | null;
  return Boolean(
    item
    && typeof item.id === "string"
    && typeof item.task_id === "string"
    && typeof item.name === "string",
  );
}

export function normalizeTaskDraft(value: unknown): TaskDraft {
  const draft = value as Partial<TaskDraft> | null;
  return {
    text: typeof draft?.text === "string" ? draft.text : "",
    attachments: Array.isArray(draft?.attachments) ? draft!.attachments.filter(validAttachment) : [],
  };
}

export function draftHasContent(draft: TaskDraft): boolean {
  return Boolean(draft.text) || draft.attachments.length > 0;
}

export function readTaskDraft(store: TaskDraftStore, taskID: string, agentID: string): TaskDraft {
  return normalizeTaskDraft(store[taskDraftKey(taskID, agentID)]);
}

export function writeTaskDraft(
  store: TaskDraftStore,
  taskID: string,
  agentID: string,
  draft: TaskDraft,
): TaskDraftStore {
  const key = taskDraftKey(taskID, agentID);
  const normalized = normalizeTaskDraft(draft);
  const next = {...store};
  if (draftHasContent(normalized)) next[key] = normalized;
  else delete next[key];
  return next;
}

export function clearTaskDraft(store: TaskDraftStore, taskID: string, agentID: string): TaskDraftStore {
  const key = taskDraftKey(taskID, agentID);
  if (!(key in store)) return store;
  const next = {...store};
  delete next[key];
  return next;
}

export function parseTaskDraftStore(raw: string | null): TaskDraftStore {
  if (!raw) return {};
  try {
    const parsed = JSON.parse(raw) as Record<string, unknown> | null;
    const store: TaskDraftStore = {};
    for (const [key, value] of Object.entries(parsed || {})) {
      if (!key.includes(TASK_DRAFT_SEPARATOR)) continue;
      const draft = normalizeTaskDraft(value);
      if (draftHasContent(draft)) store[key] = draft;
    }
    return store;
  } catch {
    return {};
  }
}

export function loadTaskDraftStore(
  storage: Pick<Storage, "getItem"> | null = null,
): TaskDraftStore {
  try {
    const target = storage || (typeof localStorage === "undefined" ? null : localStorage);
    return parseTaskDraftStore(target ? target.getItem(TASK_DRAFT_STORAGE_KEY) : null);
  } catch {
    return {};
  }
}

export function saveTaskDraftStore(
  store: TaskDraftStore,
  storage: Pick<Storage, "setItem"> | null = null,
): void {
  try {
    const target = storage || (typeof localStorage === "undefined" ? null : localStorage);
    if (target) target.setItem(TASK_DRAFT_STORAGE_KEY, JSON.stringify(store));
  } catch {
    // Draft persistence is best-effort in restricted WebViews/quota errors.
  }
}
