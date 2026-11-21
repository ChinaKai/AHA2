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

// One description for the default scope, shared by the task list and the task
// band so the visible label can never drift from the predicate below.
export const DEFAULT_TASK_SCOPE_LABEL = "未完成与异常 · 本机 · 全部项目";

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

export interface HiddenTaskStatusGroup {
  group: TaskStatusGroup;
  label: string;
  count: number;
}

// Groups the status filter leaves out that still hold tasks, from the server
// totals. Without this line the default view reads as "only one task exists"
// when every completed task is merely filtered out.
export function hiddenTaskStatusGroups(
  grouped: Record<TaskStatusGroup, number>,
  statusGroups: ReadonlySet<string>,
): HiddenTaskStatusGroup[] {
  if (statusGroups.size === 0) return [];
  return (Object.keys(TASK_STATUS_GROUP_LABELS) as TaskStatusGroup[])
    .filter(group => !statusGroups.has(group) && grouped[group] > 0)
    .map(group => ({group, label: TASK_STATUS_GROUP_LABELS[group], count: grouped[group]}));
}

export function hiddenTaskStatusText(hidden: HiddenTaskStatusGroup[], cancelledCount = 0): string {
  const parts = hidden.map(item => `${item.count} 个${item.label}`);
  // The default view hides cancelled Tasks even though their raw status is
  // still counted in the 异常 bucket; say so instead of under-reporting what
  // the filter removed.
  if (cancelledCount > 0) parts.push(`${cancelledCount} 个已取消`);
  if (parts.length === 0) return "";
  return `已隐藏 ${parts.join("、")}任务`;
}

export function taskMatchesFilters(
  task: Task,
  projectIDs: ReadonlySet<string>,
  statusGroups: ReadonlySet<string>,
  deviceIDs: ReadonlySet<string>,
): boolean {
  const matches = (projectIDs.size === 0 || projectIDs.has(task.project_id))
    && (statusGroups.size === 0 || statusGroups.has(taskStatusGroup(task.status)))
    && (deviceIDs.size === 0 || deviceIDs.has(taskDeviceFilterKey(task)));
  // "取消" is grouped with errors for manual filtering, but the untouched
  // default view means "work that still needs attention", so a cancelled Task
  // is hidden there until the owner chooses a filter by hand.
  return matches && !(task.status === "cancelled" && isDefaultTaskFilterSelection(projectIDs, statusGroups, deviceIDs));
}

// True only for the exact default selection: every project, the open+error
// buckets, local device. A hand-picked subset keeps the older bucket semantics
// (including cancelled under 异常).
export function isDefaultTaskFilterSelection(
  projectIDs: ReadonlySet<string>,
  statusGroups: ReadonlySet<string>,
  deviceIDs: ReadonlySet<string>,
): boolean {
  return projectIDs.size === 0
    && statusGroups.size === DEFAULT_TASK_STATUS_FILTERS.length
    && DEFAULT_TASK_STATUS_FILTERS.every(group => statusGroups.has(group))
    && deviceIDs.size === 1
    && deviceIDs.has(LOCAL_TASK_DEVICE_FILTER);
}

// The task band always shows the default scope, independent of whatever
// manual filters the task list currently has. It calls the same predicate as
// the list instead of restating the status/device rules.
const DEFAULT_TASK_PROJECT_FILTERS: ReadonlySet<string> = new Set();
const DEFAULT_TASK_STATUS_FILTER_SET: ReadonlySet<string> = new Set(DEFAULT_TASK_STATUS_FILTERS);
const DEFAULT_TASK_DEVICE_FILTERS: ReadonlySet<string> = new Set([LOCAL_TASK_DEVICE_FILTER]);

export function taskInDefaultTaskScope(task: Task): boolean {
  return taskMatchesFilters(
    task,
    DEFAULT_TASK_PROJECT_FILTERS,
    DEFAULT_TASK_STATUS_FILTER_SET,
    DEFAULT_TASK_DEVICE_FILTERS,
  );
}
