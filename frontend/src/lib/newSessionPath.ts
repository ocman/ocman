// Drafts allocate their final Ocman route before submission. Saving metadata
// launches nothing; the first accepted prompt fixes the native machine/target.
import { randomId } from './randomId';

export interface NewSessionParams {
  directory: string;
  /** Owning machine; omitted or 'local' = this machine. */
  remoteId?: string;
  /** Platform id (compound for remotes); empty = auto. */
  platform?: string;
  title?: string;
  draftId?: string;
}

export const NEW_SESSION_ID = 'new';

export function newConversationId(): string {
  const raw = randomId().replaceAll('-', '');
  return `ocm_${raw.slice(0, 8)}-${raw.slice(8, 12)}-${raw.slice(12, 16)}-${raw.slice(16, 20)}-${raw.slice(20)}`;
}

export function newSessionPath({ directory, remoteId, platform, title, draftId = newConversationId() }: NewSessionParams): string {
  const query = new URLSearchParams({ dir: directory });
  if (remoteId && remoteId !== 'local') query.set('remoteId', remoteId);
  if (platform) query.set('platform', platform);
  if (title) query.set('title', title);
  query.set('draftId', draftId);
  return `/session/${draftId.startsWith('ocm_') ? draftId : NEW_SESSION_ID}?${query}`;
}

export function parseNewSessionParams(search: URLSearchParams): NewSessionParams | null {
  const directory = search.get('dir');
  if (!directory) return null;
  return {
    directory,
    remoteId: search.get('remoteId') || 'local',
    platform: search.get('platform') || undefined,
    title: search.get('title') || undefined,
    ...(search.get('draftId') ? { draftId: search.get('draftId')! } : {}),
  };
}
