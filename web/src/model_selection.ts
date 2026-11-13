// Model-page pagination and multi-select helpers. Kept free of DOM state so the
// paging, grouping, and select-all rules can be tested as behavior; main.ts
// only renders the returned page/groups and mutates the selected-id set.
import type { Model } from "./types.js";
import { paginateItems, type PageSlice } from "./ui_helpers.js";

export const MODEL_PAGE_SIZE = 20;

export interface ModelProviderGroup {
  providerId: string;
  providerName: string;
  /** Models of this provider on the current page. */
  items: Model[];
  /** Models of this provider in the whole filtered list. */
  total: number;
}

export interface ModelPageState {
  slice: PageSlice<Model>;
  groups: ModelProviderGroup[];
  pageIDs: string[];
}

export function buildModelPage(models: Model[], requestedPage: number, pageSize = MODEL_PAGE_SIZE): ModelPageState {
  const slice = paginateItems(models, requestedPage, pageSize);
  return {
    slice,
    groups: groupModelsByProvider(slice.items, models),
    pageIDs: slice.items.map(item => item.id),
  };
}

// Groups the current page's models by Provider in first-seen order. The group
// count is the provider's total in the filtered list, while `items` stays the
// page slice the checkboxes can select.
export function groupModelsByProvider(pageItems: Model[], allItems: Model[]): ModelProviderGroup[] {
  const order: string[] = [];
  const groups = new Map<string, ModelProviderGroup>();
  for (const item of pageItems) {
    const providerId = item.provider_id || "";
    let group = groups.get(providerId);
    if (!group) {
      group = {providerId, providerName: item.provider_name || item.provider_id || "未命名 Provider", items: [], total: 0};
      groups.set(providerId, group);
      order.push(providerId);
    }
    group.items.push(item);
  }
  for (const item of allItems) {
    const group = groups.get(item.provider_id || "");
    if (group) group.total += 1;
  }
  return order.map(providerId => groups.get(providerId)!);
}

export type ModelSelectionState = "none" | "some" | "all";

export function modelSelectionState(selected: Set<string>, ids: string[]): ModelSelectionState {
  if (!ids.length) return "none";
  let picked = 0;
  for (const id of ids) {
    if (selected.has(id)) picked += 1;
  }
  if (picked === 0) return "none";
  return picked === ids.length ? "all" : "some";
}

export function toggleModelSelection(selected: Set<string>, id: string, checked: boolean): void {
  if (!id) return;
  if (checked) selected.add(id);
  else selected.delete(id);
}

export function selectModelIDs(selected: Set<string>, ids: string[], checked: boolean): void {
  for (const id of ids) {
    if (!id) continue;
    if (checked) selected.add(id);
    else selected.delete(id);
  }
}

// Drops ids that no longer exist after a list refresh so a stale selection
// cannot be submitted to a batch action.
export function pruneModelSelection(selected: Set<string>, models: Model[]): void {
  const live = new Set(models.map(item => item.id));
  for (const id of [...selected]) {
    if (!live.has(id)) selected.delete(id);
  }
}

export function modelBatchBarHTML(count: number): string {
  if (count <= 0) return "";
  return `<div class="model-batch-bar" role="region" aria-label="模型批量操作">
    <strong>已选 ${count} 个模型</strong>
    <div class="model-batch-actions">
      <button type="button" data-batch-check-models>批量检测</button>
      <button type="button" data-batch-delete-models class="danger">批量删除</button>
      <button type="button" data-clear-model-selection>取消选择</button>
    </div>
  </div>`;
}
