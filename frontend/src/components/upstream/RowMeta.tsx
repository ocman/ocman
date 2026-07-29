import type { Label, ForgeUser } from '../../lib/upstreamApi';
import { styleForLabel } from './labelStyle';
import { relativeTimeISO } from '../../lib/format';

interface RowMetaProps {
  author: string;
  updatedAt: string;
  labels?: Label[] | null;
  assignees?: ForgeUser[] | null;
}

/**
 * RowMeta renders the subtitle line of a PR/Issue row: author,
 * relative timestamp, label chips, and assignee initials. Kept
 * separate from the row components so PR and Issue share the same
 * pixel layout without code duplication.
 */
export function RowMeta({ author, updatedAt, labels, assignees }: RowMetaProps) {
  return (
    <div className="oc-upstream-row-meta">
      <span className="oc-upstream-row-author">by {author}</span>
      {labels && labels.length > 0 ? (
        <span className="oc-upstream-row-labels">
          {labels.map((l) => (
            <span
              key={l.name}
              className="oc-upstream-row-label"
              style={styleForLabel(l.color)}
              title={l.name}
            >
              {l.name}
            </span>
          ))}
        </span>
      ) : null}
      <span className="oc-upstream-row-time">{relativeTimeISO(updatedAt)}</span>
      {assignees && assignees.length > 0 ? (
        <span className="oc-upstream-row-assignees">
          {assignees.slice(0, 3).map((u) => (
            <span key={u.login} className="oc-upstream-row-assignee" title={u.login}>
              {initials(u.login)}
            </span>
          ))}
          {assignees.length > 3 && (
            <span className="oc-upstream-row-assignee-more">+{assignees.length - 3}</span>
          )}
        </span>
      ) : null}
    </div>
  );
}

function initials(login: string): string {
  if (!login) return '?';
  const cleaned = login.replace(/[^a-zA-Z0-9]/g, '');
  if (cleaned.length <= 2) return cleaned.toUpperCase();
  return (cleaned[0] + cleaned[cleaned.length - 1]).toUpperCase();
}
