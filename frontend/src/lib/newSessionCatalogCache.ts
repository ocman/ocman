import type { PrepareSessionResponse } from './api';

const STORAGE_KEY = 'ocman.newSessionCatalogs.v1';

function load(): Map<string, PrepareSessionResponse> {
  try {
    return new Map(JSON.parse(window.localStorage.getItem(STORAGE_KEY) ?? '[]'));
  } catch {
    return new Map();
  }
}

export function getNewSessionCatalog(key: string): PrepareSessionResponse | undefined {
  return load().get(key);
}

export function cacheNewSessionCatalog(key: string, catalog: PrepareSessionResponse): void {
  const entries = load();
  entries.delete(key);
  entries.set(key, catalog);
  // ponytail: keep the last 20 targets, always refresh before submission.
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify([...entries].slice(-20)));
  } catch {
    // Disabled storage or quota exhaustion must not prevent preparation.
  }
}
