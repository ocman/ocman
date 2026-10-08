import { type ShareLinkLike, type useShareLinks } from '../lib/useShareLinks';
import { TextField } from './Control';
import { EmptyState } from './EmptyState';
import { InlineAlert } from './InlineAlert';
import { LoadingState } from './LoadingState';
import { ShareLinkActions } from './ShareLinkActions';
import styles from './ShareLinkList.module.css';

interface ShareLinkListProps<T extends ShareLinkLike> {
  state: ReturnType<typeof useShareLinks<T>>;
  emptyText: string;
  urlLabel: string;
  copyLabel?: string;
}

/** ShareLinkList renders the error, empty state, and link rows for a share-link list. */
export function ShareLinkList<T extends ShareLinkLike>({
  state, emptyText, urlLabel, copyLabel = 'Copy',
}: ShareLinkListProps<T>) {
  const { links, loaded, error } = state;
  return (
    <>
      {error && (
        <InlineAlert>{error}</InlineAlert>
      )}
      {loaded && links.length === 0 && (
        <EmptyState>{emptyText}</EmptyState>
      )}
      {!loaded && !error && <LoadingState>Loading share links...</LoadingState>}
      {links.length > 0 && (
        <ul className={styles.links}>
          {links.map((link) => (
            <li key={link.token} className={styles.link}>
              <TextField
                type="text"
                readOnly
                value={link.url}
                className={styles.url}
                aria-label={urlLabel}
                onFocus={(e) => e.currentTarget.select()}
              />
              <ShareLinkActions state={state} link={link} copyLabel={copyLabel} />
            </li>
          ))}
        </ul>
      )}
    </>
  );
}
