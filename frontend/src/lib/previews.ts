import { createContext } from 'react';
import { fetchJSON, postJSON } from './api';

/** Owner (`remoteId`) of the conversation whose links are previewed. */
export const PreviewOwnerContext = createContext('local');

/** Mirrors internal/linkpreview.State. */
export type PreviewState =
  | 'ok' | 'connect' | 'expired' | 'denied' | 'not_found' | 'rate_limited' | 'ambiguous' | 'error';

/** One provider resource (internal/linkpreview.Ref). */
export interface PreviewRef {
  provider: string;
  workspace?: string;
  kind: string;
  id: string;
  /** Canonical resource URL; absent for an unresolved ticket identifier. */
  url?: string;
}

/** Normalized preview result (internal/linkpreview.Preview). */
export interface PreviewResult extends PreviewRef {
  title?: string;
  status?: string;
  /** Bootstrap icon class, e.g. `bi-kanban`. */
  icon?: string;
  meta?: string[];
  updatedAt?: string;
  state: PreviewState;
  /** Cached data served while the provider is rate limited. */
  stale?: boolean;
}

/** One grant: display names only (internal/previewauth.Connection). */
export interface PreviewConnection {
  workspaceId: string;
  workspaceName: string;
  accountName: string;
  sites: { id: string; name: string }[];
  state: 'connected' | 'expired';
}

/** A configured provider and this viewer's connections. */
export interface PreviewProvider {
  id: string;
  name: string;
  /** The access a grant gives, shown before consent. */
  notice?: string;
  connections: PreviewConnection[];
}

/** Fired after connect/disconnect: drop everything viewer-private and reload. */
export const PREVIEW_AUTH_EVENT = 'ocman:preview-auth-changed';

const q = (remoteId: string) => `?remoteId=${encodeURIComponent(remoteId)}`;

// In-memory only: viewer-private names and choices never touch storage.
const providerCache = new Map<string, Promise<PreviewProvider[]>>();
const workspaceChoice = new Map<string, string>();

/** Forgets viewer-private state and tells mounted previews to reload. */
export function previewAuthChanged(): void {
  providerCache.clear();
  workspaceChoice.clear();
  window.dispatchEvent(new Event(PREVIEW_AUTH_EVENT));
}

export function loadPreviewProviders(remoteId = 'local'): Promise<PreviewProvider[]> {
  let p = providerCache.get(remoteId);
  if (!p) {
    p = fetchJSON<{ providers: PreviewProvider[] }>(`/api/previews/providers${q(remoteId)}`).then((r) => r.providers ?? []);
    p.catch(() => providerCache.delete(remoteId));
    providerCache.set(remoteId, p);
  }
  return p;
}

/** Starts consent in this tab; the provider redirects back to `returnTo`. */
export async function connectPreviewProvider(provider: string, remoteId = 'local'): Promise<void> {
  const returnTo = window.location.pathname + window.location.search;
  const { authorizeUrl } = await postJSON<{ authorizeUrl: string }>(`/api/previews/connect${q(remoteId)}`, { provider, returnTo });
  window.location.assign(authorizeUrl);
}

/** Deletes the grant; the server clears its cached previews immediately. */
export async function disconnectPreviewProvider(provider: string, workspaceId?: string, remoteId = 'local'): Promise<void> {
  await postJSON(`/api/previews/disconnect${q(remoteId)}`, { provider, workspaceId });
  previewAuthChanged();
}

/** Picks the workspace for a provider's ambiguous resources. */
export function chooseWorkspace(provider: string, workspaceId: string, remoteId = 'local'): void {
  workspaceChoice.set(`${remoteId}\u0000${provider}`, workspaceId);
  window.dispatchEvent(new Event(PREVIEW_AUTH_EVENT));
}

/**
 * Discovers and resolves previews in `text` for this browser's viewer.
 * The server owns discovery, provider hosts and credentials.
 */
export async function resolvePreviews(text: string, remoteId = 'local', signal?: AbortSignal): Promise<PreviewResult[]> {
  const workspaces: Record<string, string> = {};
  for (const [key, id] of workspaceChoice) {
    const [owner, provider] = key.split('\u0000');
    if (owner === remoteId) workspaces[provider] = id;
  }
  const { previews } = await postJSON<{ previews: PreviewResult[] }>(
    `/api/previews/resolve${q(remoteId)}`,
    Object.keys(workspaces).length ? { text, workspaces } : { text },
    { signal },
  );
  return previews;
}
