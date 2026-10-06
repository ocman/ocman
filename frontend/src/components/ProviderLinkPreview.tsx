import { useCallback, useContext, useEffect, useRef, useState } from 'react';
import type { FC } from 'react';
import { PreviewOwnerContext, fetchPreviewChecks } from '../lib/previews';
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
const STATUS_CLASS: Record<string, string> = { Open: 'open', Merged: 'merged', Closed: 'closed' };

const RichCard: FC<{ preview: PreviewResult }> = ({ preview }) => {
  const cls = `gh-preview gh-preview--${STATUS_CLASS[preview.status ?? ''] ?? 'commit'}`;
  const body = <>
    <span className="gh-preview__icon"><i className={`bi ${preview.icon || 'bi-link-45deg'}`} aria-hidden="true" /></span>
    <span className="gh-preview__body">
      <span className="gh-preview__title">{label(preview)}</span>
      <span className="gh-preview__meta">
        {[preview.status, ...(preview.meta ?? [])].filter(Boolean).join(' · ')}
        {preview.updatedAt && <> · <RelativeTime iso={preview.updatedAt} /></>}
        {preview.stale && ' · cached'}
      </span>
      {preview.kind === 'pr' && preview.headSha && preview.url && <PreviewCI preview={preview} />}
    </span>
  </>;
  return preview.url
    ? <a className={cls} href={preview.url} target="_blank" rel="noopener noreferrer" data-testid="provider-preview-card">
        {body}<span className="gh-preview__external" aria-hidden="true"><i className="bi bi-arrow-up-right" /></span>
      </a>
    : <div className={cls} data-testid="provider-preview-card">{body}</div>;
};

function PreviewCI({ preview }: { preview: PreviewResult }) {
  const ref = useRef<HTMLSpanElement>(null);
  const [visible, setVisible] = useState(() => typeof IntersectionObserver === 'undefined');
  useEffect(() => {
    if (!ref.current) return;
    if (typeof IntersectionObserver === 'undefined') return;
    const observer = new IntersectionObserver((entries) => {
      const entry = entries[entries.length - 1];
      if (entry) setVisible(entry.isIntersecting);
    });
    observer.observe(ref.current);
    return () => observer.disconnect();
  }, []);
  const owner = useContext(PreviewOwnerContext);
  const url = preview.url!;
  const sha = preview.headSha!;
  const repo = preview.id.split('#')[0];
  const key = prChecksCacheKey(new URL(url).host, repo, sha);
  const loadChecks = useCallback((signal: AbortSignal, refresh: boolean) => fetchPreviewChecks(url, sha, owner, signal, refresh), [url, sha, owner]);
  const checks = usePRChecks(key, `${owner}\0${url}\0${sha}`, visible, loadChecks);
  const label = checks.error ? 'Failed to load checks' : checks.loading && !checks.loaded ? 'Loading checks…' : CI_LABEL[checks.state];
  return <span ref={ref} className="gh-preview__meta" aria-label={label}>
    <i className={`bi ${checks.error ? 'bi-exclamation-circle' : checks.state === 'success' ? 'bi-check-circle' : checks.state === 'failure' ? 'bi-x-circle' : checks.state === 'pending' ? 'bi-hourglass-split' : 'bi-question-circle'}`} aria-hidden="true" /> {label}
  </span>;
}

const NOTICE: Partial<Record<PreviewResult['state'], string>> = {
  connect: 'Private. Add a token under Settings → Link previews to preview it.',
  expired: 'The saved token expired. Replace it under Settings → Link previews.',
  denied: 'The saved token cannot view this.',
};

/** One provider preview: a rich card, or a plain link saying why there is none. */
export const ProviderPreview: FC<{ preview: PreviewResult; providers: PreviewProvider[] }> = ({ preview, providers }) => {
  if (hasRichPreview(preview)) return <RichCard preview={preview} />;
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
