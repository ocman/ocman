import { useCallback, useContext, useEffect, useRef, useState } from 'react';
import type { FC } from 'react';
import { PreviewOwnerContext, PreviewSessionContext, fetchPreviewChecks, fetchPreviewPRStatus } from '../lib/previews';
import { usePRMergeability } from '../lib/usePRMergeability';
import { getCachedPRChecks } from '../lib/prChecksCache';
import { CI_LABEL, usePRChecks } from '../lib/usePRChecks';
import { previewChecksKeys, previewPRContext, useSharedPreview } from '../lib/sharedPreviews';
import { hasRichPreview } from '../lib/useProviderPreviews';
import type { PreviewProvider, PreviewResult } from '../lib/previews';
import { RelativeTime } from './RelativeTime';
import { PreviewRefreshButton } from './PreviewRefreshButton';

// A page ID is opaque; its title alone labels it.
const label = (p: PreviewResult) => !p.title ? p.id : p.kind === 'page' ? p.title : `${p.id} ${p.title}`;

const FallbackLink: FC<{ preview: PreviewResult }> = ({ preview }) => preview.url
  ? <a className="gh-preview__title" href={preview.url} target="_blank" rel="noopener noreferrer">{preview.id}</a>
  : <span className="gh-preview__title">{preview.id}</span>;

// Forge statuses keep their old card colours.
const STATUS_CLASS: Record<string, string> = { Open: 'open', Draft: 'open', Merged: 'merged', Closed: 'closed' };

const RichCard: FC<{ preview: PreviewResult; refreshChecks?: boolean; onReappear?: () => void }> = ({ preview, onReappear }) => {
  const session = useContext(PreviewSessionContext);
  const owner = useContext(PreviewOwnerContext);
  const statusClass = STATUS_CLASS[preview.status ?? ''];
  const cls = `gh-preview gh-preview--${statusClass ?? 'commit'}`;
  const body = <>
    <span className="gh-preview__icon"><i className={`bi ${preview.icon || 'bi-link-45deg'}`} aria-hidden="true" /></span>
    <span className="gh-preview__body">
      <span className="gh-preview__title">{label(preview)}</span>
      {preview.image && <img className="gh-preview__image" src={`/api/previews/image?url=${encodeURIComponent(preview.image)}`} alt={preview.imageAlt ?? ''} onError={(event) => { event.currentTarget.hidden = true; }} />}
      {preview.description && <span className="gh-preview__description">{preview.description}</span>}
      <span className="gh-preview__meta">
        {statusClass ? <span className={`gh-preview__state gh-preview__state--${statusClass}`}>{preview.status}</span> : preview.status}
        {preview.status && preview.meta?.some(Boolean) && ' · '}
        {preview.meta?.filter(Boolean).join(' · ')}
        {preview.updatedAt && <> · <RelativeTime iso={preview.updatedAt} /></>}
        {preview.stale && ' · cached'}
      </span>
      {preview.kind === 'pr' && preview.url && <PreviewCI key={session} preview={preview} onReappear={onReappear} />}
    </span>
  </>;
  return preview.url ? <div className="gh-preview-frame">
    <a className={`${cls} gh-preview--refreshable`} href={preview.url} target="_blank" rel="noopener noreferrer" data-testid="provider-preview-card">{body}</a>
    <PreviewRefreshButton key={JSON.stringify([owner, preview.url])} preview={preview} owner={owner} />
  </div> : <div className={cls} data-testid="provider-preview-card">{body}</div>;
};

function PreviewCI({ preview, onReappear }: { preview: PreviewResult; onReappear?: () => void }) {
  const ref = useRef<HTMLSpanElement>(null);
  const [visible, setVisible] = useState(() => typeof IntersectionObserver === 'undefined');
  useEffect(() => {
    if (!ref.current) return;
    if (typeof IntersectionObserver === 'undefined') return;
    let seen = false;
    let wasVisible = false;
    const observer = new IntersectionObserver((entries) => {
      const entry = entries[entries.length - 1];
      if (entry) {
        if (entry.isIntersecting && seen && !wasVisible && preview.status !== 'Merged') onReappear?.();
        seen ||= entry.isIntersecting;
        wasVisible = entry.isIntersecting;
        setVisible(entry.isIntersecting);
      }
    });
    observer.observe(ref.current);
    return () => observer.disconnect();
  }, [onReappear, preview.status]);
  const owner = useContext(PreviewOwnerContext);
  const url = preview.url!;
  const sha = preview.headSha ?? '';
  const [key, requestKey] = previewChecksKeys(preview, url, owner);
  const loadChecks = useCallback((signal: AbortSignal, refresh: boolean) => fetchPreviewChecks(url, sha, owner, signal, refresh), [url, sha, owner]);
  const merged = preview.status === 'Merged';
  const checks = usePRChecks(key, requestKey, visible && !!sha && !merged, loadChecks, true);
  const saved = merged ? preview.checks ?? (sha ? getCachedPRChecks(key) : undefined) : undefined;
  const state = saved?.state ?? checks.state;
  const failed = !saved && checks.error;
  const label = failed ? 'Failed to load checks' : !saved && checks.loading && !checks.loaded ? 'Loading checks…' : CI_LABEL[state];
  return <span className="gh-preview__meta"><span ref={ref} className="gh-preview__meta" aria-label={label} data-ci-state={failed ? 'unknown' : state}>
    <i className={`bi ${failed ? 'bi-exclamation-circle' : state === 'success' ? 'bi-check-circle' : state === 'failure' ? 'bi-x-circle' : state === 'pending' ? 'bi-hourglass-split' : 'bi-question-circle'}`} aria-hidden="true" /> {label}
  </span><PreviewPRStatus preview={preview} owner={owner} visible={visible} /></span>;
}

function PreviewPRStatus({ preview, owner, visible }: { preview: PreviewResult; owner: string; visible: boolean }) {
  const url = preview.url!;
  const loadStatus = (signal: AbortSignal, refresh: boolean) => fetchPreviewPRStatus(url, owner, signal, refresh);
  const pr = previewPRContext(preview, url);
  const { status } = pr;
  const supported = preview.provider === 'github' || preview.provider.startsWith('forgejo:');
  const loaded = usePRMergeability(pr, '', owner, '', visible && supported && status !== 'merged', loadStatus, true);
  const { mergeable, approved } = status === 'merged' ? preview.prStatus ?? loaded : loaded;
  const mergeLabel = status === 'open' || status === 'draft' ? status === 'draft' || mergeable === false ? 'Not mergeable' : mergeable === true ? 'Mergeable' : undefined : undefined;
  return <span className="gh-preview__meta">
    {(mergeLabel || approved === true) && <span aria-hidden="true">·</span>}
    {mergeLabel && <span>{mergeLabel}</span>}
    {mergeLabel && approved === true && ' · '}
    {approved === true && <span><i className="bi bi-check-lg" aria-hidden="true" /> Approved</span>}
  </span>;
}

const NOTICE: Partial<Record<PreviewResult['state'], string>> = {
  connect: 'Private. Add a token under Settings → Link previews to preview it.',
  expired: 'The saved token expired. Replace it under Settings → Link previews.',
  denied: 'The saved token cannot view this.',
};

/** One provider preview: a rich card, or a plain link saying why there is none. */
export const ProviderPreview: FC<{ preview: PreviewResult; providers: PreviewProvider[]; refreshChecks?: boolean; onReappear?: () => void }> = ({ preview: initial, providers, refreshChecks, onReappear }) => {
  const owner = useContext(PreviewOwnerContext);
  const preview = useSharedPreview(owner, initial);
  if (hasRichPreview(preview)) return <RichCard preview={preview} refreshChecks={refreshChecks} onReappear={onReappear} />;
  if (preview.choices?.length) return (
    <div className="gh-preview" data-testid="provider-preview-choices">
      <span className="gh-preview__icon"><i className="bi bi-files" aria-hidden="true" /></span>
      <span className="gh-preview__body">
        <span className="gh-preview__title">{preview.id}</span>
        <span className="gh-preview__meta">Several pages match:</span>
        {preview.choices.map((c) => <a key={c.url} className="gh-preview__meta" href={c.url} target="_blank" rel="noopener noreferrer">{c.title}</a>)}
      </span>
    </div>
  );
  const notice = NOTICE[preview.state];
  // not_found / error / rate limited: the plain link is the safe fallback.
  if (!notice) return preview.url ? <div className="gh-preview"><span className="gh-preview__body"><FallbackLink preview={preview} /></span></div> : null;
  const name = providers.find((p) => p.id === preview.provider)?.name ?? preview.provider;
  return (
    <div className="gh-preview" data-testid="provider-preview-notice">
      <span className="gh-preview__icon"><i className="bi bi-lock" aria-hidden="true" /></span>
      <span className="gh-preview__body">
        <FallbackLink preview={preview} />
        <span className="gh-preview__meta">{name}: {notice}</span>
      </span>
    </div>
  );
};
