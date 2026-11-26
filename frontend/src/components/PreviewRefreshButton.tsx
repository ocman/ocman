import { useEffect, useRef, useState } from 'react';
import { fetchPreviewChecks, fetchPreviewPRStatus, reloadPreview } from '../lib/previews';
import type { PreviewResult } from '../lib/previews';
import { beginPreviewRead, previewChecksKeys, previewPRContext, previewReadIsCurrent, publishPreview } from '../lib/sharedPreviews';
import { seedPRChecks } from '../lib/usePRChecks';
import { seedPRMergeability } from '../lib/usePRMergeability';
import { SubmitButton } from './Control';

async function refreshPRDetails(preview: PreviewResult, url: string, owner: string, signal: AbortSignal) {
  const [checks, status] = await Promise.allSettled([
    preview.headSha ? fetchPreviewChecks(url, preview.headSha, owner, signal, true) : undefined,
    fetchPreviewPRStatus(url, owner, signal, true),
  ]);
  return {
    preview: { ...preview, checks: checks.status === 'fulfilled' ? checks.value : undefined, prStatus: status.status === 'fulfilled' ? status.value : undefined },
    failed: checks.status === 'rejected' || status.status === 'rejected',
  };
}

export function PreviewRefreshButton({ preview, owner }: { preview: PreviewResult; owner: string }) {
  const active = useRef<AbortController>(null);
  const [failed, setFailed] = useState(false);
  useEffect(() => () => active.current?.abort(), []);

  const refresh = async () => {
    if (!preview.url) return;
    const request = new AbortController();
    active.current = request;
    const { signal } = request;
    const read = beginPreviewRead();
    setFailed(false);
    try {
      const results = await reloadPreview(preview.url, owner, signal, preview.provider === 'opengraph');
      const next = results.find((p) => p.provider === preview.provider && p.kind === preview.kind && p.id === preview.id);
      if (!next || next.stale || next.state === 'error' || next.state === 'rate_limited') throw new Error('Preview unavailable');
      if (signal.aborted || !previewReadIsCurrent(read)) return;
      const url = next.url ?? preview.url;
      if (next.state === 'ok' && next.workspace !== preview.workspace) throw new Error('Preview workspace changed');
      const snapshot = { ...next, url: preview.url, workspace: preview.workspace };
      publishPreview(owner, snapshot, read);
      if (next.state === 'ok' && next.kind === 'pr' && (next.provider === 'github' || next.provider.startsWith('forgejo:'))) {
        const details = await refreshPRDetails(snapshot, url, owner, signal);
        if (signal.aborted || !previewReadIsCurrent(read)) return;
        setFailed(details.failed);
        if (next.status === 'Merged') {
          publishPreview(owner, details.preview, read);
        } else {
          const cardURL = preview.url;
          if (details.preview.checks && next.headSha) seedPRChecks(...previewChecksKeys(next, cardURL, owner), details.preview.checks);
          if (details.preview.prStatus) seedPRMergeability(previewPRContext(next, cardURL), owner, details.preview.prStatus);
        }
      }
    } catch {
      if (!signal.aborted && previewReadIsCurrent(read)) setFailed(true);
    }
  };

  return <SubmitButton className="gh-preview__refresh" variant="ghost" size="compact"
    aria-label="Refresh preview" title={failed ? 'Refresh failed. Try again.' : 'Refresh preview'} onClick={refresh}>
    <i className={`bi ${failed ? 'bi-exclamation-circle' : 'bi-arrow-clockwise'}`} aria-hidden="true" />
    {failed && <span className="gh-preview__refresh-status" role="status">Preview refresh failed</span>}
  </SubmitButton>;
}
