// Per-model health check state, separated from main.ts so the concurrency and
// clearing rules can be tested as behavior instead of source text.
//
// A check writes "checking" immediately, then the final result only if its
// request generation is still current. Editing a model or Provider clears
// results and bumps the generation, so a response that was already in flight
// cannot resurrect a stale "可用" badge.
import type { ModelHealthView } from "./ui_helpers.js";

export interface ModelHealthState {
  modelHealth: Record<string, ModelHealthView>;
  modelHealthSeq: Record<string, number>;
}

export function createModelHealthState(): ModelHealthState {
  return {modelHealth: {}, modelHealthSeq: {}};
}

export async function runModelHealthCheck(
  state: ModelHealthState,
  modelID: string,
  fetchHealth: (modelID: string) => Promise<ModelHealthView>,
  onChange?: () => void,
): Promise<void> {
  if (!modelID || state.modelHealth[modelID]?.status === "checking") return;
  const seq = (state.modelHealthSeq[modelID] || 0) + 1;
  state.modelHealthSeq[modelID] = seq;
  state.modelHealth[modelID] = {status: "checking"};
  onChange?.();
  let next: ModelHealthView;
  try {
    next = await fetchHealth(modelID);
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error || "检测请求失败");
    next = {status: "error", detail: message};
  }
  if (state.modelHealthSeq[modelID] !== seq) return;
  state.modelHealth[modelID] = next;
  onChange?.();
}

export function forgetModelHealth(state: ModelHealthState, modelID: string): void {
  delete state.modelHealth[modelID];
  state.modelHealthSeq[modelID] = (state.modelHealthSeq[modelID] || 0) + 1;
}

export function forgetAllModelHealth(state: ModelHealthState): void {
  state.modelHealth = {};
  for (const modelID of Object.keys(state.modelHealthSeq)) {
    state.modelHealthSeq[modelID] += 1;
  }
}
