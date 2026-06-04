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
  /**
   * The branch currently checked out in the project's working tree.
   * When this matches the PR's source branch (and the PR isn't from
   * a fork), the row is highlighted so the user can quickly spot the
   * PR that corresponds to whatever they're working on locally.
   * Undefined when git info is still loading or unavailable.
   */
  currentBranch?: string;
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
export function PRRow({ pr, directory, remote, currentBranch }: PRRowProps) {
  const [expanded, setExpanded] = useState(false);

  // Cross-fork PRs share their head branch name with the user's
  // local tree by coincidence at best (different repo entirely), so
  // skip the match in that case to avoid false positives.
  const isCurrentBranch =
    !pr.crossFork && !!currentBranch && pr.branch === currentBranch;

  return (
    <li
      className={
        `oc-upstream-row oc-upstream-row-pr` +
        (expanded ? ' expanded' : '') +
        (isCurrentBranch ? ' current-branch' : '')
      }
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
        {isCurrentBranch && (
          <span
            className="oc-upstream-row-current-branch"
            title={`Matches your current branch: ${pr.branch}`}
            data-testid={`pr-row-${pr.number}-current-branch`}
          >
            current
          </span>
        )}
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
