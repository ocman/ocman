// A new conversation is a client-only route until its first prompt: the
// session is created where the composer's machine/target selectors point
// at that moment, so nothing is created, moved or archived beforehand.

export interface NewSessionParams {
  directory: string;
  /** Owning machine; omitted or 'local' = this machine. */
  remoteId?: string;
  /** Platform id (compound for remotes); empty = auto. */
  platform?: string;
  title?: string;
}

export const NEW_SESSION_ID = 'new';

export function newSessionPath({ directory, remoteId, platform, title }: NewSessionParams): string {
  const query = new URLSearchParams({ dir: directory });
  if (remoteId && remoteId !== 'local') query.set('remoteId', remoteId);
  if (platform) query.set('platform', platform);
  if (title) query.set('title', title);
  return `/session/${NEW_SESSION_ID}?${query}`;
}

export function parseNewSessionParams(search: URLSearchParams): NewSessionParams | null {
  const directory = search.get('dir');
  if (!directory) return null;
  return {
    directory,
    remoteId: search.get('remoteId') || 'local',
    platform: search.get('platform') || undefined,
    title: search.get('title') || undefined,
  };
}
