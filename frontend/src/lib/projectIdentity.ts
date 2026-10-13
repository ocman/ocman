import type { Project } from './api';
import { projectRootForDirectory } from './worktrees';

type Checkout = Pick<Project, 'directory' | 'remoteId' | 'remoteName' | 'platform' | 'projectKey' | 'mergedIntoDirectory' | 'mergedIntoRemoteId'>;
export type ProjectIdentity = Checkout & { key: string };

export const checkoutKey = (directory: string, remoteId?: string) =>
  JSON.stringify([remoteId || 'local', directory]);

// Keep the display/action checkout separate from the grouping key.
export function projectIdentityIndex(projects: Checkout[]) {
  const byCheckout = new Map<string, string>();
  const representatives = new Map<string, ProjectIdentity>();
  for (const project of projects) {
    const directory = projectRootForDirectory(project.directory);
    const key = project.projectKey || (project.remoteId && project.remoteId !== 'local'
      ? checkoutKey(directory, project.remoteId) : directory);
    byCheckout.set(checkoutKey(directory, project.remoteId), key);
    const previous = representatives.get(key);
    if (!previous || (previous.remoteId && previous.remoteId !== 'local' && (!project.remoteId || project.remoteId === 'local'))) {
      representatives.set(key, { directory, remoteId: project.remoteId,
        remoteName: project.remoteName, platform: project.platform, projectKey: project.projectKey, key });
    }
  }
  for (const project of projects) {
    if (!project.mergedIntoDirectory) continue;
    const key = byCheckout.get(checkoutKey(projectRootForDirectory(project.directory), project.remoteId))!;
    byCheckout.set(checkoutKey(projectRootForDirectory(project.mergedIntoDirectory), project.mergedIntoRemoteId), key);
    const target = projects.find(p => p.directory === project.mergedIntoDirectory
      && (p.remoteId || 'local') === (project.mergedIntoRemoteId || 'local'));
    representatives.set(key, { directory: project.mergedIntoDirectory,
      remoteId: project.mergedIntoRemoteId, remoteName: target?.remoteName,
      platform: target?.platform, projectKey: key, key });
  }
  return (directory: string, remoteId?: string): ProjectIdentity => {
    const root = projectRootForDirectory(directory);
    const key = byCheckout.get(checkoutKey(root, remoteId));
    return key !== undefined ? representatives.get(key)! : {
      directory: root, remoteId,
      key: remoteId && remoteId !== 'local' ? checkoutKey(root, remoteId) : root,
    };
  };
}

export function groupProjects(projects: Project[], identity = projectIdentityIndex(projects)): Project[] {
  const groups = new Map<string, Project>();
  for (const project of projects) {
    const target = identity(project.directory, project.remoteId);
    const group = groups.get(target.key);
    if (!group) {
      groups.set(target.key, { ...project, ...target });
      continue;
    }
    group.sessionCount += project.sessionCount;
    group.messageCount += project.messageCount;
    group.totalTokensIn += project.totalTokensIn;
    group.totalTokensOut += project.totalTokensOut;
    group.lastUsed = Math.max(group.lastUsed, project.lastUsed);
    group.archived = !!group.archived && !!project.archived;
  }
  return [...groups.values()].sort((a, b) => b.lastUsed - a.lastUsed);
}
