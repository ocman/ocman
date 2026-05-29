import { useState } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import type { PR } from '../../lib/upstreamApi';
import { LaunchSplitButton } from './LaunchSplitButton';
import { RowMeta } from './RowMeta';

interface PRRowProps {
  pr: PR;
  directory: string;
  remote: string;
}

/**
 * PRRow renders a single PR row with an inline-expand affordance.
 *
 * Click anywhere on the collapsed row (except the link) to expand.
 * Click the title/header again to collapse. Only one row is expanded
 * at a time within a list — collapse is opt-in here, the parent
 * doesn't enforce single-expansion in v1 (matches the "best-effort"
 * note in FR-7; can be tightened later).
 */
export function PRRow({ pr, directory, remote }: PRRowProps) {
  const [expanded, setExpanded] = useState(false);

  return (
    <li
      className={`oc-upstream-row oc-upstream-row-pr${expanded ? ' expanded' : ''}`}
      data-testid={`pr-row-${pr.number}`}
    >
      <button
        type="button"
        className="oc-upstream-row-summary"
        onClick={() => setExpanded((e) => !e)}
        aria-expanded={expanded}
      >
        <span className="oc-upstream-row-number">#{pr.number}</span>
        <span className="oc-upstream-row-title">{pr.title}</span>
        <StatusBadge status={pr.status} />
      </button>
      <RowMeta
        author={pr.author}
        updatedAt={pr.updatedAt}
        labels={pr.labels}
        assignees={pr.assignees}
      />
      {expanded && (
        <div className="oc-upstream-row-detail" data-testid={`pr-detail-${pr.number}`}>
          <a className="oc-upstream-row-link" href={pr.url} target="_blank" rel="noreferrer noopener">
            View on {pr.host}
          </a>
          {pr.crossFork && (
            <div className="oc-upstream-row-fork-note">
              Cross-fork PR — worktree launch will fetch the PR ref.
            </div>
          )}
          <div className="oc-upstream-row-body">
            {pr.body ? (
              <ReactMarkdown remarkPlugins={[remarkGfm]}>{pr.body}</ReactMarkdown>
            ) : (
              <em>No description.</em>
            )}
          </div>
          <LaunchSplitButton
            directory={directory}
            remote={remote}
            type="pr"
            number={pr.number}
            crossFork={pr.crossFork}
          />
        </div>
      )}
    </li>
  );
}

function StatusBadge({ status }: { status: PR['status'] }) {
  return (
    <span className={`oc-upstream-status oc-upstream-status-${status}`}>{status}</span>
  );
}
