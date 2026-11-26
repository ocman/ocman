import { skipToken, useQuery } from '@tanstack/react-query';
import { PREVIEW_AUTH_EVENT } from './previews';
import type { PreviewResult } from './previews';
import { queryClient } from './queryClient';
import { prChecksCacheKey } from './prChecksCache';

export function previewPRContext(preview: PreviewResult, url: string) {
  const [repo, number] = preview.id.split('#');
  const status = preview.status === 'Merged' ? 'merged' : preview.status === 'Closed' ? 'closed' : preview.status === 'Draft' ? 'draft' : 'open';
  return { host: new URL(url).host, repo, number: Number(number), headSha: preview.headSha, updatedAt: preview.updatedAt ?? '', status } as const;
}

export function previewChecksKeys(preview: PreviewResult, url: string, owner: string) {
  const { host, repo, headSha = '' } = previewPRContext(preview, url);
  return [prChecksCacheKey(host, repo, headSha), `${owner}\0${url}\0${headSha}`] as const;
}

const prefix = ['link-preview'] as const;
queryClient.setQueryDefaults(prefix, { gcTime: 60_000 });

interface Snapshot {
  order: number;
  preview: PreviewResult;
}

let order = 0;
let authChange = 0;

if (typeof window !== 'undefined') {
  window.addEventListener(PREVIEW_AUTH_EVENT, () => {
    authChange = ++order;
    queryClient.removeQueries({ queryKey: prefix });
  });
}

export const beginPreviewRead = () => ++order;
export const previewReadIsCurrent = (read: number) => read > authChange;

function previewKey(owner: string, preview: PreviewResult) {
  return [...prefix, owner, preview.provider, preview.workspace, preview.kind, preview.id, preview.url] as const;
}

export function publishPreview(owner: string, preview: PreviewResult, read: number) {
  if (!preview.url || !previewReadIsCurrent(read)) return;
  queryClient.setQueryData<Snapshot>(previewKey(owner, preview), (previous) => {
    if (previous && (previous.order > read || preview.state === 'error' || preview.state === 'rate_limited')) return undefined;
    // Keep explicitly refreshed checks and approval on identical metadata reads.
    const old = previous?.preview;
    if (old?.state === 'ok' && preview.state === 'ok' && preview.kind === 'pr' &&
      ((old.status === 'Merged' && preview.status !== 'Merged') || Date.parse(old.updatedAt ?? '') > Date.parse(preview.updatedAt ?? ''))) return undefined;
    if (old && preview.state === 'ok' && old.state === 'ok' && old.headSha === preview.headSha && old.updatedAt === preview.updatedAt && old.status === preview.status) {
      preview = { ...preview, checks: preview.checks ?? old.checks, prStatus: preview.prStatus ?? old.prStatus };
    }
    return { order: read, preview };
  });
}

export function useSharedPreview(owner: string, preview: PreviewResult) {
  const { data } = useQuery<Snapshot>({ queryKey: previewKey(owner, preview), queryFn: skipToken, subscribed: !!preview.url }, queryClient);
  return data?.preview ?? preview;
}
