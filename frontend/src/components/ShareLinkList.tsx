import type { ReactNode } from 'react';
import { type ShareLinkLike, type useShareLinks } from '../lib/useShareLinks';

interface ShareLinkListProps<T extends ShareLinkLike> {
  state: ReturnType<typeof useShareLinks<T>>;
  emptyText: string;
  urlLabel: string;
  copyLabel?: string;
  /** Rendered before the Copy button (e.g. an Inspect link). */
  renderLeading?: (link: T) => ReactNode;
  /** Rendered under the action row (e.g. link age). */
  renderFooter?: (link: T) => ReactNode;
}

/** ShareLinkList renders the error, empty state, and link rows for a share-link list. */
export function ShareLinkList<T extends ShareLinkLike>({
  state, emptyText, urlLabel, copyLabel = 'Copy', renderLeading, renderFooter,
}: ShareLinkListProps<T>) {
  const { links, loaded, busy, error, copied, copy, revoke } = state;
  return (
    <>
      {error && (
        <div className="oc-share-menu-error" role="alert">{error}</div>
      )}
      {loaded && links.length === 0 && (
        <div className="oc-share-menu-empty">{emptyText}</div>
      )}
      {links.length > 0 && (
        <ul className="oc-share-menu-links">
          {links.map((link) => (
            <li key={link.token} className="oc-share-menu-link">
              <input
                type="text"
                readOnly
                value={link.url}
                className="oc-share-menu-url"
                aria-label={urlLabel}
                onFocus={(e) => e.currentTarget.select()}
              />
              <div className="oc-share-menu-link-actions">
                {renderLeading?.(link)}
                <button type="button" onClick={() => void copy(link)} data-testid="share-copy-link">
                  {copied === link.token ? 'Copied!' : copyLabel}
                </button>
                <button
                  type="button"
                  className="oc-share-menu-revoke"
                  onClick={() => void revoke(link)}
                  disabled={busy}
                  data-testid="share-revoke-link"
                >
                  Revoke
                </button>
              </div>
              {renderFooter?.(link)}
            </li>
          ))}
        </ul>
      )}
    </>
  );
}
