import { useSyncExternalStore } from 'react';

/**
 * A heavy module (e.g. a syntax highlighter) that loads on first use rather
 * than with the entry chunk. `get()` returns undefined until it has loaded,
 * so callers render a plain fallback first; components that read it through
 * `useLazyModule` start the load and re-render once it resolves. A failed
 * load is retried on the next subscription.
 */
export interface LazyModule<T> {
  get(): T | undefined;
  load(): Promise<T>;
  subscribe(listener: () => void): () => void;
}

export function lazyModule<T>(importer: () => Promise<T>): LazyModule<T> {
  let value: T | undefined;
  let pending: Promise<T> | undefined;
  const listeners = new Set<() => void>();
  function load() {
    pending ??= importer().then(
      (loaded) => {
        value = loaded;
        for (const listener of listeners) listener();
        return loaded;
      },
      (err: unknown) => {
        pending = undefined;
        throw err;
      },
    );
    return pending;
  }
  return {
    get: () => value,
    load,
    subscribe(listener) {
      listeners.add(listener);
      if (value === undefined) load().catch(() => { /* keep the fallback */ });
      return () => { listeners.delete(listener); };
    },
  };
}

/** The loaded module, or undefined while it loads (or after a failed load). */
export function useLazyModule<T>(module: LazyModule<T>): T | undefined {
  return useSyncExternalStore(module.subscribe, module.get, module.get);
}
