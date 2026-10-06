import { useState, useEffect } from 'react';
import type { FC } from 'react';
import { extractCustomLinks, loadLinkPreviewRules } from '../lib/linkPreviewRules';
import type { LinkPreviewRule } from '../lib/linkPreviewRules';
import { ProviderPreview } from './ProviderLinkPreview';
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

  // Only announce loading where a link or rule match could become a card.
  const loading = provider.loading && (customLinks.length > 0 || /https?:\/\//.test(text));
  if (customLinks.length === 0 && providerPreviews.length === 0 && !loading) return null;
  return (
    <div className="gh-preview-strip" data-testid="gh-preview-strip">
      {providerPreviews.map((p) => (
        <ProviderPreview key={[p.provider, p.workspace, p.kind, p.id].join('\u0000')} preview={p} providers={provider.providers} refreshChecks={provider.refreshChecks} />
      ))}
      {loading && <span className="gh-preview__meta" role="status">Loading previews…</span>}
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
