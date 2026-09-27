import { postJSON } from './api';

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

/**
 * Discovers and resolves previews in `text` for this browser's viewer.
 * The server owns discovery, provider hosts and credentials.
 */
export async function resolvePreviews(text: string, remoteId = 'local', signal?: AbortSignal): Promise<PreviewResult[]> {
  const { previews } = await postJSON<{ previews: PreviewResult[] }>(
    `/api/previews/resolve?remoteId=${encodeURIComponent(remoteId)}`,
    { text },
    { signal },
  );
  return previews;
}
