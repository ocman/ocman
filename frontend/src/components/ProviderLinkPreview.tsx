import { useContext, useState } from 'react';
import type { FC } from 'react';
import { PreviewOwnerContext, chooseWorkspace, connectPreviewProvider } from '../lib/previews';
import { hasRichPreview } from '../lib/useProviderPreviews';
import type { PreviewProvider, PreviewResult } from '../lib/previews';
import { Button, SelectField } from './Control';
import { RelativeTime } from './RelativeTime';

const label = (p: PreviewResult) => p.title ? `${p.id} ${p.title}` : p.id;

const FallbackLink: FC<{ preview: PreviewResult }> = ({ preview }) => preview.url
  ? <a className="gh-preview__title" href={preview.url} target="_blank" rel="noopener noreferrer">{preview.id}</a>
  : <span className="gh-preview__title">{preview.id}</span>;

const RichCard: FC<{ preview: PreviewResult }> = ({ preview }) => {
  const body = <>
    <span className="gh-preview__icon"><i className={`bi ${preview.icon || 'bi-link-45deg'}`} aria-hidden="true" /></span>
    <span className="gh-preview__body">
      <span className="gh-preview__title">{label(preview)}</span>
      <span className="gh-preview__meta">
        {[preview.status, ...(preview.meta ?? [])].filter(Boolean).join(' · ')}
        {preview.updatedAt && <> · <RelativeTime iso={preview.updatedAt} /></>}
        {preview.stale && ' · cached'}
      </span>
    </span>
  </>;
  return preview.url
    ? <a className="gh-preview gh-preview--commit" href={preview.url} target="_blank" rel="noopener noreferrer" data-testid="provider-preview-card">
        {body}<span className="gh-preview__external" aria-hidden="true"><i className="bi bi-arrow-up-right" /></span>
      </a>
    : <div className="gh-preview gh-preview--commit" data-testid="provider-preview-card">{body}</div>;
};

const NOTICE: Partial<Record<PreviewResult['state'], string>> = {
  connect: 'Connect to preview',
  expired: 'Connection expired',
  denied: 'Your account cannot view this',
  ambiguous: 'Choose which workspace to preview from',
};

/** One provider preview: a rich card, or a plain link with its connect affordance. */
export const ProviderPreview: FC<{ preview: PreviewResult; providers: PreviewProvider[] }> = ({ preview, providers }) => {
  const owner = useContext(PreviewOwnerContext);
  const [error, setError] = useState('');
  if (hasRichPreview(preview)) return <RichCard preview={preview} />;
  const notice = NOTICE[preview.state];
  // not_found / error / rate limited: the plain link is the safe fallback.
  if (!notice) return preview.url ? <div className="gh-preview"><span className="gh-preview__body"><FallbackLink preview={preview} /></span></div> : null;
  const provider = providers.find((p) => p.id === preview.provider);
  const name = provider?.name ?? preview.provider;
  const connect = () => { setError(''); connectPreviewProvider(preview.provider, owner).catch((err: unknown) => setError(String(err))); };
  const verb = preview.state === 'connect' ? 'Connect' : 'Reconnect';
  return (
    <div className="gh-preview" data-testid="provider-preview-notice">
      <span className="gh-preview__icon"><i className="bi bi-lock" aria-hidden="true" /></span>
      <span className="gh-preview__body">
        <FallbackLink preview={preview} />
        <span className="gh-preview__meta">{name}: {notice}</span>
        {error && <span role="alert">{error}</span>}
      </span>
      {preview.state === 'ambiguous'
        ? <SelectField aria-label={`Workspace for ${preview.id}`} value="" onChange={(e) => chooseWorkspace(preview.provider, e.target.value, owner)}>
            <option value="" disabled>Choose workspace…</option>
            {provider?.connections.map((c) => <option key={c.workspaceId} value={c.workspaceId}>{c.workspaceName || c.workspaceId}</option>)}
          </SelectField>
        : <Button type="button" size="small" onClick={connect} aria-label={`${verb} ${name} for ${preview.id}`}>{verb}</Button>}
    </div>
  );
};
