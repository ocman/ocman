import { useMemo, useRef } from 'react';
import type { Project } from './api';
import { postJSON } from './api.requests';
import { ONE_OFF_PROJECT, isOneOffDirectory } from './oneOffProject';

/** Virtual choices are only for session creation, never filesystem/API targets. */
export function useOneOffProjects(data: Project[] | undefined, owner: string,
  onError: (message: string | null) => void) {
  const locked = useRef(false);
  const projects = useMemo(() => {
    if (!data) return data;
    const owners = new Map<string, Project>();
    const regular = data.filter((project) => {
      if (!isOneOffDirectory(project.directory)) return true;
      const id = project.remoteId || 'local';
      const existing = owners.get(id);
      owners.set(id, { ...project, directory: ONE_OFF_PROJECT, projectKey: `one-off:${id}`,
        sessionCount: (existing?.sessionCount ?? 0) + project.sessionCount,
        lastUsed: Math.max(existing?.lastUsed ?? 0, project.lastUsed) });
      return false;
    });
    if (!owners.has(owner)) owners.set(owner, {
      directory: ONE_OFF_PROJECT, projectKey: `one-off:${owner}`, remoteId: owner === 'local' ? undefined : owner,
      sessionCount: 0, messageCount: 0, totalTokensIn: 0, totalTokensOut: 0, lastUsed: 0,
    });
    return [...regular, ...owners.values()];
  }, [data, owner]);

  async function start(project: Project, open: (directory: string, project: Project) => void) {
    if (locked.current) return;
    if (project.directory !== ONE_OFF_PROJECT) { open(project.directory, project); return; }
    locked.current = true;
    onError(null);
    try {
      const { directory } = await postJSON<{ directory: string }>('/api/sessions/one-off', { remoteId: project.remoteId || 'local' });
      open(directory, project);
    } catch (error) {
      onError(error instanceof Error ? error.message : String(error));
    } finally {
      locked.current = false;
    }
  }

  return { projects, start };
}
