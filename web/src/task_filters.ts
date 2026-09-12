import type {Task} from "./types.js";

export const LOCAL_TASK_DEVICE_FILTER = "local";

export interface TaskDeviceFilterOption {
  value: string;
  label: string;
  count: number;
}

export function taskDeviceFilterKey(task: Task): string {
  if (!task.read_only) return LOCAL_TASK_DEVICE_FILTER;
  return `remote:${task.owner_device_id?.trim() || "unknown"}`;
}

export function taskDeviceFilterOptions(tasks: Task[]): TaskDeviceFilterOption[] {
  const counts = new Map<string, number>();
  for (const task of tasks) {
    const key = taskDeviceFilterKey(task);
    counts.set(key, (counts.get(key) || 0) + 1);
  }
  const remote = [...counts.entries()]
    .filter(([value]) => value !== LOCAL_TASK_DEVICE_FILTER)
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([value, count]) => ({
      value,
      label: value === "remote:unknown" ? "其他设备（未知）" : value.slice("remote:".length),
      count,
    }));
  return [
    {value: LOCAL_TASK_DEVICE_FILTER, label: "本机", count: counts.get(LOCAL_TASK_DEVICE_FILTER) || 0},
    ...remote,
  ];
}

export function taskMatchesFilters(
  task: Task,
  projectIDs: ReadonlySet<string>,
  statuses: ReadonlySet<string>,
  deviceIDs: ReadonlySet<string>,
): boolean {
  return (projectIDs.size === 0 || projectIDs.has(task.project_id))
    && (statuses.size === 0 || statuses.has(task.status))
    && (deviceIDs.size === 0 || deviceIDs.has(taskDeviceFilterKey(task)));
}
