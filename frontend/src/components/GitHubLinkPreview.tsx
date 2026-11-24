import { useState, useEffect } from 'react';
import type { FC } from 'react';
import { extractCustomLinks, loadLinkPreviewRules } from '../lib/linkPreviewRules';
import type { LinkPreviewRule } from '../lib/linkPreviewRules';
import { ProviderPreview } from './ProviderLinkPreview';
import { Skeleton } from './Skeleton';
import { hasRichPreview, needsCard, useProviderPreviews } from '../lib/useProviderPreviews';
import './GitHubLinkPreview.css';

/**
 * Renders a card below the text block for each previewable resource the
 * server finds in `text` (GitHub/Forgejo links and connected providers),
 * plus custom link-rule cards. Renders nothing when there are none.
 */
export const LinkPreviewStrip: FC<{ text: string }> = ({ text }) => {
  const [customRules, setCustomRules] = useState<LinkPreviewRule[]>([]);

  useEffect(() => {
    let active = true;
    const reload = () => { loadLinkPreviewRules().then((rules) => {
      if (active) setCustomRules(rules);
    }).catch(() => {}); };
    reload();
    window.addEventListener('ocman:link-preview-rules-changed', reload);
    return () => { active = false; window.removeEventListener('ocman:link-preview-rules-changed', reload); };
  }, []);

  const provider = useProviderPreviews(text);
  // A resolved provider card replaces its custom rule's plain fallback card.
  const richIds = new Set(provider.previews.filter(hasRichPreview).map((p) => p.id));
  const allCustom = extractCustomLinks(text, customRules);
  const customLinks = allCustom.filter(({ label }) => !richIds.has(label));
  // A plain provider fallback is redundant where a custom rule card exists.
  const customLabels = new Set(allCustom.map(({ label }) => label));
  const providerPreviews = provider.previews.filter((p) => needsCard(p) || !customLabels.has(p.id));

  // A custom fallback already takes up its card's space while the provider loads.
  const fallbackKeys = new Set(customLinks.flatMap(({ label, url }) => [label, url.split('#')[0]]));
  const skeletons = provider.pendingCandidates.filter((key) => !fallbackKeys.has(key));
  if (customLinks.length === 0 && providerPreviews.length === 0 && skeletons.length === 0) return null;
  return (
    <div className="gh-preview-strip" data-testid="gh-preview-strip" aria-busy={provider.pendingCandidates.length > 0 || undefined}>
      {providerPreviews.map((p) => (
        <ProviderPreview key={[p.provider, p.workspace, p.kind, p.id].join('\u0000')} preview={p} providers={provider.providers} refreshChecks={provider.refreshChecks} onReappear={provider.refresh} />
      ))}
      {skeletons.map((key) => (
        <div key={key} className="gh-preview gh-preview--skeleton" data-testid="link-preview-skeleton" aria-hidden="true">
          <Skeleton className="gh-preview__skeleton-icon" />
          <span className="gh-preview__body">
            <span className="gh-preview__skeleton-row"><Skeleton className="oc-skeleton-line" style={{ width: '55%' }} /></span>
            <span className="gh-preview__skeleton-row gh-preview__meta"><Skeleton className="oc-skeleton-line" style={{ width: '30%' }} /></span>
          </span>
        </div>
      ))}
      {customLinks.map(({ url, label }) => (
        <a className="gh-preview gh-preview--commit" key={url} href={url} target="_blank" rel="noopener noreferrer">
          <span className="gh-preview__icon"><i className="bi bi-link-45deg" aria-hidden="true" /></span>
          <span className="gh-preview__body">
            <span className="gh-preview__title">{label}</span>
            <span className="gh-preview__meta">{new URL(url).hostname}</span>
          </span>
          <span className="gh-preview__external" aria-hidden="true"><i className="bi bi-arrow-up-right" /></span>
        </a>
      ))}
    </div>
  );
};
