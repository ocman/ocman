import { useQuery } from '@tanstack/react-query';
import { useMemo } from 'react';
import { api, type Project } from './api';
import { checkoutKey, projectIdentityIndex } from './projectIdentity';
import { projectRootForDirectory } from './worktrees';
import { queryClient } from './queryClient';

type Checkout = { directory: string; remoteId?: string };

export function useProjectIdentityIndex(projects: Array<Pick<Project, 'directory' | 'remoteId' | 'remoteName' | 'platform' | 'projectKey'>>, checkouts: Checkout[], enabled = true) {
  const missing = useMemo(() => {
    const known = new Set(projects.map((project) => checkoutKey(project.directory, project.remoteId)));
    return [...new Map(checkouts.filter((checkout) => checkout.directory).map((checkout) => {
      const root = { directory: projectRootForDirectory(checkout.directory), remoteId: checkout.remoteId };
      return [checkoutKey(root.directory, root.remoteId), root] as const;
    })).entries()].filter(([key]) => !known.has(key)).sort(([a], [b]) => a.localeCompare(b)).map(([, checkout]) => checkout);
  }, [projects, checkouts]);
  const catalogKey = useMemo(() => projects.map((project) => [project.projectKey, project.directory, project.remoteId]), [projects]);
  const resolved = useQuery({
    queryKey: ['project-identities', catalogKey, missing],
    queryFn: ({ signal }) => api.resolveProjectIdentities(missing, signal),
    enabled: enabled && missing.length > 0,
    staleTime: 30_000,
  }, queryClient);
  return useMemo(() => projectIdentityIndex(projects, (resolved.data ?? []).map((project, index) => ({ checkout: missing[index], project }))), [projects, resolved.data, missing]);
}
