import { useEffect } from 'react';

const counts = new Map<string, number>();
const listeners = new Set<() => void>();

export function activityScopeSnapshot(): string[] {
  return Array.from(counts.keys()).sort();
}

export function subscribeActivityScopes(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function acquireActivityScope(scope: string): () => void {
  if (!scope) return () => {};
  const count = counts.get(scope) ?? 0;
  counts.set(scope, count + 1);
  if (count === 0) listeners.forEach((listener) => listener());

  let released = false;
  return () => {
    if (released) return;
    released = true;
    const next = (counts.get(scope) ?? 1) - 1;
    if (next > 0) {
      counts.set(scope, next);
      return;
    }
    counts.delete(scope);
    listeners.forEach((listener) => listener());
  };
}

export function useActivityScope(scope: string | undefined): void {
  useEffect(() => {
    if (!scope) return;
    return acquireActivityScope(scope);
  }, [scope]);
}

export function __resetActivityScopesForTests(): void {
  counts.clear();
  listeners.clear();
}
