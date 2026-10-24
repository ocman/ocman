import type { PrepareSessionResponse } from './api';
import { readStored, writeStorage } from './storage';

const STORAGE_KEY = 'ocman.newSessionCatalogs.v1';

function load(): Map<string, PrepareSessionResponse> {
  return readStored(STORAGE_KEY, new Map<string, PrepareSessionResponse>(), (raw) => new Map(JSON.parse(raw)));
}

export function getNewSessionCatalog(key: string): PrepareSessionResponse | undefined {
  const catalog = load().get(key);
  if (!catalog || typeof catalog.platform !== 'string' ||
    !Array.isArray(catalog.agents) || !catalog.agents.every((agent) => typeof agent?.name === 'string') ||
    !Array.isArray(catalog.commands) || !catalog.commands.every((command) => typeof command?.name === 'string') ||
    !Array.isArray(catalog.models?.models) || !catalog.models.models.every((model) =>
      typeof model?.provider === 'string' && typeof model?.model === 'string')) return undefined;
  return catalog;
}

export function cacheNewSessionCatalog(key: string, catalog: PrepareSessionResponse): void {
  const entries = load();
  entries.delete(key);
  entries.set(key, catalog);
  // ponytail: keep the last 20 targets, always refresh before submission.
  // Disabled storage or quota exhaustion must not prevent preparation.
  writeStorage(STORAGE_KEY, JSON.stringify([...entries].slice(-20)));
}
