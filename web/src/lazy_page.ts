// Route chunks are fetched on first use. Keep the state machine here so every
// page reports loading, failure, and retry the same way and a page entered
// twice never fires a second request.
export type LazyPageStatus = "idle" | "loading" | "ready" | "failed";

export interface LazyPage<T> {
  key: string;
  status(): LazyPageStatus;
  loaded(): T | null;
  error(): string;
  load(): Promise<T | null>;
  retry(): Promise<T | null>;
}

export interface LazyPageOptions {
  onChange?: () => void;
}

const registry = new Map<string, LazyPage<unknown>>();

export function createLazyPage<T>(key: string, loader: () => Promise<T>, options: LazyPageOptions = {}): LazyPage<T> {
  let state: LazyPageStatus = "idle";
  let module: T | null = null;
  let failure = "";
  let pending: Promise<T | null> | null = null;

  const finish = (value: T | null) => {
    pending = null;
    options.onChange?.();
    return value;
  };

  const load = (): Promise<T | null> => {
    if (state === "ready") return Promise.resolve(module);
    // A failed chunk stays failed until the owner retries; auto-loading from a
    // render would loop render → failure → render.
    if (state === "failed") return Promise.resolve(null);
    if (pending) return pending;
    state = "loading";
    failure = "";
    pending = loader()
      .then(value => {
        module = value;
        state = "ready";
        return finish(value);
      })
      .catch(reason => {
        state = "failed";
        failure = reason instanceof Error ? reason.message : String(reason);
        return finish(null);
      });
    return pending;
  };

  const page: LazyPage<T> = {
    key,
    status: () => state,
    loaded: () => module,
    error: () => failure,
    load,
    retry: () => {
      if (state !== "failed") return load();
      state = "idle";
      return load();
    },
  };
  if (!registry.has(key)) registry.set(key, page as LazyPage<unknown>);
  return page;
}

export function retryLazyPage(key: string): Promise<unknown> | null {
  const page = registry.get(key);
  if (!page || page.status() !== "failed") return null;
  return page.retry();
}

function escapeChunkText(value: unknown): string {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

export function chunkLoadingHTML(label: string): string {
  return `<div class="chunk-state" role="status" aria-busy="true"><span class="boot-spinner" aria-hidden="true"></span><strong>${escapeChunkText(label)}</strong></div>`;
}

export function chunkErrorHTML(key: string, label: string, message: string, retryLabel: string): string {
  const detail = message ? `<span>${escapeChunkText(message)}</span>` : "";
  return `<div class="chunk-state chunk-state-error" role="alert"><strong>${escapeChunkText(label)}</strong>${detail}<button type="button" data-chunk-retry="${escapeChunkText(key)}">${escapeChunkText(retryLabel)}</button></div>`;
}