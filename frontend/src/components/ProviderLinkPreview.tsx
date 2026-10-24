import { useCallback, useContext, useEffect, useRef, useState } from 'react';
import type { FC } from 'react';
import { PreviewOwnerContext, PreviewSessionContext, fetchPreviewChecks, fetchPreviewPRStatus } from '../lib/previews';
import { usePRMergeability } from '../lib/usePRMergeability';
import { prChecksCacheKey } from '../lib/prChecksCache';
import { CI_LABEL, usePRChecks } from '../lib/usePRChecks';
import { hasRichPreview } from '../lib/useProviderPreviews';
import type { PreviewProvider, PreviewResult } from '../lib/previews';
import { RelativeTime } from './RelativeTime';

// A page ID is opaque; its title alone labels it.
const label = (p: PreviewResult) => !p.title ? p.id : p.kind === 'page' ? p.title : `${p.id} ${p.title}`;

const FallbackLink: FC<{ preview: PreviewResult }> = ({ preview }) => preview.url
  ? <a className="gh-preview__title" href={preview.url} target="_blank" rel="noopener noreferrer">{preview.id}</a>
  : <span className="gh-preview__title">{preview.id}</span>;

// Forge statuses keep their old card colours.
const STATUS_CLASS: Record<string, string> = { Open: 'open', Draft: 'open', Merged: 'merged', Closed: 'closed' };

const RichCard: FC<{ preview: PreviewResult; refreshChecks?: boolean; onReappear?: () => void }> = ({ preview, onReappear }) => {
  const session = useContext(PreviewSessionContext);
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
  return preview.url
    ? <a className={cls} href={preview.url} target="_blank" rel="noopener noreferrer" data-testid="provider-preview-card">
        {body}<span className="gh-preview__external" aria-hidden="true"><i className="bi bi-arrow-up-right" /></span>
      </a>
    : <div className={cls} data-testid="provider-preview-card">{body}</div>;
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
        if (entry.isIntersecting && seen && !wasVisible) onReappear?.();
        seen ||= entry.isIntersecting;
        wasVisible = entry.isIntersecting;
        setVisible(entry.isIntersecting);
      }
    });
    observer.observe(ref.current);
    return () => observer.disconnect();
  }, [onReappear]);
  const owner = useContext(PreviewOwnerContext);
  const url = preview.url!;
  const sha = preview.headSha ?? '';
  const repo = preview.id.split('#')[0];
  const key = prChecksCacheKey(new URL(url).host, repo, sha);
  const loadChecks = useCallback((signal: AbortSignal, refresh: boolean) => fetchPreviewChecks(url, sha, owner, signal, refresh), [url, sha, owner]);
  const checks = usePRChecks(key, `${owner}\0${url}\0${sha}`, visible && !!sha, loadChecks, true);
  const label = checks.error ? 'Failed to load checks' : checks.loading && !checks.loaded ? 'Loading checks…' : CI_LABEL[checks.state];
  return <span className="gh-preview__meta"><span ref={ref} className="gh-preview__meta" aria-label={label} data-ci-state={checks.error ? 'unknown' : checks.state}>
    <i className={`bi ${checks.error ? 'bi-exclamation-circle' : checks.state === 'success' ? 'bi-check-circle' : checks.state === 'failure' ? 'bi-x-circle' : checks.state === 'pending' ? 'bi-hourglass-split' : 'bi-question-circle'}`} aria-hidden="true" /> {label}
  </span><PreviewPRStatus preview={preview} owner={owner} visible={visible} /></span>;
}

function PreviewPRStatus({ preview, owner, visible }: { preview: PreviewResult; owner: string; visible: boolean }) {
  const url = preview.url!;
  const loadStatus = (signal: AbortSignal, refresh: boolean) => fetchPreviewPRStatus(url, owner, signal, refresh);
  const status = preview.status === 'Merged' ? 'merged' : preview.status === 'Closed' ? 'closed' : preview.status === 'Draft' ? 'draft' : 'open';
  const supported = preview.provider === 'github' || preview.provider.startsWith('forgejo:');
  const { mergeable, approved } = usePRMergeability({ host: new URL(url).host, repo: preview.id.split('#')[0], number: Number(preview.id.split('#')[1]), headSha: preview.headSha, updatedAt: preview.updatedAt ?? '', status }, '', owner, '', visible && supported, loadStatus, true);
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
export const ProviderPreview: FC<{ preview: PreviewResult; providers: PreviewProvider[]; refreshChecks?: boolean; onReappear?: () => void }> = ({ preview, providers, refreshChecks, onReappear }) => {
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
