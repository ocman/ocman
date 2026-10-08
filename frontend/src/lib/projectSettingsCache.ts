import { useSyncExternalStore } from 'react';
import { fetchJSON } from './api';
import { projectRootForDirectory } from './worktrees';

export interface CachedProjectSettings {
  models: string[];
  off: boolean;
  defaultAgent: string;
  defaults?: ProjectDefaults;
}

export interface ProjectDefaults {
  model: string;
  agent: string;
  worktree: '' | 'worktree' | 'current';
  permissionMode?: '' | 'default' | 'plan' | 'auto-edit' | 'yolo';
}

const cache = new Map<string, Promise<CachedProjectSettings>>();
const listeners = new Set<() => void>();
let revision = 0;

export function clearSettingsCache(): void {
  cache.clear();
  revision++;
  for (const listener of listeners) listener();
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
}

export function useSettingsRevision(): number {
  return useSyncExternalStore(subscribe, () => revision);
}

export function loadProjectSettings(directory: string, remoteId = 'local'): Promise<CachedProjectSettings> {
  const root = projectRootForDirectory(directory);
  const key = JSON.stringify([remoteId, root]);
  const cached = cache.get(key);
  if (cached) return cached;
  const request: Promise<CachedProjectSettings> = fetchJSON<CachedProjectSettings>(`/api/project/settings?dir=${encodeURIComponent(root)}&remoteId=${encodeURIComponent(remoteId)}`)
    .then((settings) => cache.get(key) === request ? settings : loadProjectSettings(directory, remoteId))
    .catch((error) => {
      if (cache.get(key) === request) cache.delete(key);
      throw error;
    });
  cache.set(key, request);
  return request;
}
