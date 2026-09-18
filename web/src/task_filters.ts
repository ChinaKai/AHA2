import type {Task} from "./types.js";

export const LOCAL_TASK_DEVICE_FILTER = "local";

// The owner-facing status filter is three buckets over the nine raw task
// statuses. "blocked" sits with the errors because it needs the owner to step
// in, while every status that is still moving toward a result stays open.
export type TaskStatusGroup = "open" | "done" | "error";

export const TASK_STATUS_GROUP_LABELS: Record<TaskStatusGroup, string> = {
  open: "未完成",
  done: "已完成",
  error: "异常",
};

const TASK_STATUS_GROUPS: Record<string, TaskStatusGroup> = {
  draft: "open",
  preparing: "open",
  active: "open",
  waiting_user: "open",
  completed: "done",
  failed: "error",
  blocked: "error",
  cancelled: "error",
};

export function taskStatusGroup(status: string): TaskStatusGroup {
  return TASK_STATUS_GROUPS[status] || "open";
}

// Selected by default so the list opens on the work that still needs attention.
export const DEFAULT_TASK_STATUS_FILTERS: TaskStatusGroup[] = ["open", "error"];

export interface TaskDeviceFilterOption {
  value: string;
  label: string;
  count: number;
}

export function taskDeviceFilterKey(task: Task): string {
  if (!task.read_only) return LOCAL_TASK_DEVICE_FILTER;
  return `remote:${task.owner_device_id?.trim() || "unknown"}`;
}

// The device options come from the server totals rather than the loaded tasks,
// because the list is paged: deriving them from one page drops every device that
// happens not to appear on it. Keys match store.taskDeviceFilterKey.
export function taskDeviceFilterOptions(counts: Record<string, number>): TaskDeviceFilterOption[] {
  const remote = Object.keys(counts)
    .filter(value => value !== LOCAL_TASK_DEVICE_FILTER)
    .sort((left, right) => left.localeCompare(right))
    .map(value => ({
      value,
      label: value === "remote:unknown" ? "其他设备（未知）" : value.slice("remote:".length),
      count: counts[value] || 0,
    }));
  return [
    {value: LOCAL_TASK_DEVICE_FILTER, label: "本机", count: counts[LOCAL_TASK_DEVICE_FILTER] || 0},
    ...remote,
  ];
}

// Bucket totals add up the raw statuses they cover, so a group with no tasks
// still reports 0 instead of being missing.
export function taskStatusGroupCounts(counts: Record<string, number>): Record<TaskStatusGroup, number> {
  const totals: Record<TaskStatusGroup, number> = {open: 0, done: 0, error: 0};
  for (const [status, count] of Object.entries(counts || {})) {
    totals[taskStatusGroup(status)] += count;
  }
  return totals;
}

export function taskMatchesFilters(
  task: Task,
  projectIDs: ReadonlySet<string>,
  statusGroups: ReadonlySet<string>,
  deviceIDs: ReadonlySet<string>,
): boolean {
  return (projectIDs.size === 0 || projectIDs.has(task.project_id))
    && (statusGroups.size === 0 || statusGroups.has(taskStatusGroup(task.status)))
    && (deviceIDs.size === 0 || deviceIDs.has(taskDeviceFilterKey(task)));
}
