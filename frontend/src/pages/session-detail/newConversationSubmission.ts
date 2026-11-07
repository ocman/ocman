import type { PermissionRule, StartSessionRequest } from '../../lib/api';
import type { SessionTarget } from '../../components/assistant/ComposerSelectorRow';
import type { DraftCatalog } from './draftCatalog';

export interface Ready { catalog: DraftCatalog; canWorktree: boolean; worktrees: { path: string }[]; platform?: string; model: string }
export interface Submission {
  model: string;
  send?: StartSessionRequest['send'];
  execute?: (sessionId: string, platform: string) => Promise<void>;
  command?: { command: string; arguments: string };
  shell?: { command: string };
  files?: File[];
}
export type DurableStartRequest = StartSessionRequest & Pick<Submission, 'command' | 'shell'>;
export function resolveTarget(target: SessionTarget, canWorktree: boolean, worktrees: { path: string }[]): SessionTarget {
  return !canWorktree || target.startsWith('dir:') && !worktrees.some((tree) => `dir:${tree.path}` === target) ? 'current' : target;
}

export function durableStartRequest(r: Ready, submission: Submission, params: {
  directory: string; remoteId: string; title?: string; target?: SessionTarget; cloneURL?: string; permissionRules?: PermissionRule[];
  platformForModel: (model: string) => string | undefined;
}, prompt: string): DurableStartRequest {
  const target = resolveTarget(params.target || r.catalog.defaults?.worktree || 'worktree', r.canWorktree, r.worktrees);
  return { directory: target.startsWith('dir:') ? target.slice(4) : params.directory,
    platform: (submission.model && params.platformForModel(submission.model)) || r.platform,
    remoteId: params.remoteId, title: params.title, prompt, send: submission.send,
    worktree: target === 'worktree', cloneUrl: params.cloneURL, command: submission.command, shell: submission.shell,
    ...(params.permissionRules ? { permissionRules: params.permissionRules } : {}) };
}
