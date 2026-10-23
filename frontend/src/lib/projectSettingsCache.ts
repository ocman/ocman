import { useSyncExternalStore } from 'react';
import { fetchJSON } from './api';
import { projectRootForDirectory } from './worktrees';
import { queryClient } from './queryClient';

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

const revisionKey = ['settings-revision'];
queryClient.setQueryDefaults(revisionKey, { gcTime: Infinity });
const revision = () => queryClient.getQueryData<number>(revisionKey) ?? 0;

export function clearSettingsCache(): void {
  void queryClient.invalidateQueries({ queryKey: ['project-settings'], refetchType: 'none' });
  queryClient.setQueryData(revisionKey, revision() + 1);
}

export function useSettingsRevision(): number {
  return useSyncExternalStore((notify) => queryClient.getQueryCache().subscribe(notify), revision);
}

export function loadProjectSettings(directory: string, remoteId = 'local'): Promise<CachedProjectSettings> {
  const root = projectRootForDirectory(directory);
  const generation = revision();
  return queryClient.fetchQuery({
    queryKey: ['project-settings', remoteId, root, generation],
    staleTime: Infinity,
    retry: false,
    queryFn: () => fetchJSON<CachedProjectSettings>(`/api/project/settings?dir=${encodeURIComponent(root)}&remoteId=${encodeURIComponent(remoteId)}`)
      .then((settings) => generation === revision() ? settings : loadProjectSettings(directory, remoteId)),
  });
}
