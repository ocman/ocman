import { useQuery } from '@tanstack/react-query';
import { api, fetchJSON, postJSON } from './api';

export type ArtifactItem = {
  kind: 'file' | 'link';
  name?: string;
  mime?: string;
  size?: number;
  sha256?: string;
  url?: string;
  label?: string;
};

export type Artifact = {
  id: string;
  title: string;
  description?: string;
  directory: string;
  platform?: string;
  sessionId?: string;
  remoteId: string;
  createdAt: string;
  items: ArtifactItem[];
};

export type ArtifactPage = { artifacts: Artifact[]; nextCursor: string };
export type ArtifactStats = { count: number; totalBytes: number };
export type ArtifactListParams = { directory?: string; q?: string; cursor?: string; limit?: number; sessionId?: string; platform?: string; includeDescendants?: boolean };

const path = (id: string) => `/api/artifacts/${encodeURIComponent(id)}`;

// ponytail: kept out of api.ts (already >1000 lines), same as beadsApi.ts.
export const artifactsApi = {
  list: (params: ArtifactListParams = {}, signal?: AbortSignal) => {
    const q = new URLSearchParams();
    for (const [k, v] of Object.entries(params)) {
      if (v === undefined || v === '' || v === false) continue;
      q.set(k, v === true ? '1' : String(v));
    }
    const qs = q.toString();
    return fetchJSON<ArtifactPage>(`/api/artifacts${qs ? `?${qs}` : ''}`, signal);
  },
  get: (id: string, signal?: AbortSignal) => fetchJSON<Artifact>(path(id), signal),
  remove: (id: string) => postJSON<void, undefined>(path(id), undefined, { method: 'DELETE', parseJSON: false }),
  stats: (signal?: AbortSignal) => fetchJSON<ArtifactStats>('/api/artifacts/stats', signal),
};

export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  const units = ['KiB', 'MiB', 'GiB', 'TiB'];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i += 1; }
  return `${v.toFixed(v < 10 ? 1 : 0)} ${units[i]}`;
}

export const artifactBytes = (a: Artifact) => a.items.reduce((sum, it) => sum + (it.size ?? 0), 0);

/** Ids of every session ocman still knows; undefined while loading. */
export function useKnownSessionIds(): Set<string> | undefined {
  return useQuery({
    queryKey: ['artifact-known-sessions'],
    queryFn: async ({ signal }) => new Set((await api.sessions({ limit: 0 }, signal)).map((s) => s.id)),
    staleTime: 30_000,
  }).data;
}

const MAX_TEXT_PREVIEW = 1024 * 1024;
const TEXT_MIMES = /^(text\/|application\/(json|xml|javascript|x-yaml|yaml|x-sh|toml|sql)\b)/;
const TEXT_EXT = /\.(txt|log|json|ya?ml|toml|xml|csv|sh|go|ts|tsx|js|jsx|py|rs|rb|java|c|h|cpp|css|html|sql|diff|patch)$/i;

export type PreviewKind = 'image' | 'markdown' | 'text' | 'none';

export function previewKind(item: ArtifactItem): PreviewKind {
  const mime = (item.mime ?? '').split(';')[0].trim().toLowerCase();
  const name = item.name ?? '';
  if (mime.startsWith('image/')) return 'image';
  if ((item.size ?? 0) > MAX_TEXT_PREVIEW) return 'none';
  if (mime === 'text/markdown' || /\.(md|markdown)$/i.test(name)) return 'markdown';
  if (TEXT_MIMES.test(mime) || TEXT_EXT.test(name)) return 'text';
  return 'none';
}

export type CreatedArtifact = { id: string; title: string; items: number };

/**
 * Reads the output of a completed ocman `artifacts` action=create call.
 * Only create returns `markdown` (whose first line is `[title](url)`), so
 * the output shape identifies the action without re-parsing the args.
 */
export function parseCreatedArtifact(toolName: string, result: unknown): CreatedArtifact | null {
  if (!/(^|_)ocman_artifacts$/i.test(toolName) || typeof result !== 'string') return null;
  let out: { id?: unknown; url?: unknown; items?: unknown; markdown?: unknown };
  try { out = JSON.parse(result); } catch { return null; }
  if (!out || typeof out.id !== 'string' || !out.id || typeof out.markdown !== 'string') return null;
  const first = out.markdown.split('\n')[0];
  const suffix = typeof out.url === 'string' ? `](${out.url})` : '';
  const title = suffix && first.startsWith('[') && first.endsWith(suffix) ? first.slice(1, -suffix.length) : out.id;
  return { id: out.id, title, items: Array.isArray(out.items) ? out.items.length : 0 };
}
