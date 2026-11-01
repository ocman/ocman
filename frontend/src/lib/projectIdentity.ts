import type { Project, ProjectIdentity } from './api';
import { projectRootForDirectory } from './worktrees';

type Checkout = Pick<Project, 'directory' | 'remoteId' | 'remoteName' | 'platform' | 'projectKey'>;
export type { ProjectIdentity } from './api';
export type ProjectResolution = { checkout: Pick<Checkout, 'directory' | 'remoteId'>; project: ProjectIdentity };

export const checkoutKey = (directory: string, remoteId?: string) =>
  JSON.stringify([remoteId || 'local', directory]);

// Index final API records and explicit resolutions, never reconstruct merges.
export function projectIdentityIndex(projects: Checkout[], resolutions: ProjectResolution[] = []) {
  const byCheckout = new Map<string, ProjectIdentity>();
  for (const project of projects) {
    const directory = projectRootForDirectory(project.directory);
    const key = project.projectKey || (project.remoteId && project.remoteId !== 'local'
      ? checkoutKey(directory, project.remoteId) : directory);
    byCheckout.set(checkoutKey(directory, project.remoteId), { ...project, directory, key });
  }
  for (const { checkout, project } of resolutions) {
    byCheckout.set(checkoutKey(projectRootForDirectory(checkout.directory), checkout.remoteId), project);
  }
  return (directory: string, remoteId?: string): ProjectIdentity => {
    const root = projectRootForDirectory(directory);
    return byCheckout.get(checkoutKey(root, remoteId)) ?? {
      directory: root, remoteId,
      key: remoteId && remoteId !== 'local' ? checkoutKey(root, remoteId) : root,
    };
  };
}
