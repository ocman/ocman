import { useState } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import type { Issue } from '../../lib/upstreamApi';
import { LaunchSplitButton } from './LaunchSplitButton';
import { OpenInBrowser } from './OpenInBrowser';
import { RowMeta } from './RowMeta';

interface IssueRowProps {
  issue: Issue;
  directory: string;
  remote: string;
}

export function IssueRow({ issue, directory, remote }: IssueRowProps) {
  const [expanded, setExpanded] = useState(false);

  return (
    <li
      className={`oc-upstream-row oc-upstream-row-issue${expanded ? ' expanded' : ''}`}
      data-testid={`issue-row-${issue.number}`}
    >
      <div className="oc-upstream-row-head">
        <button
          type="button"
          className="oc-upstream-row-summary"
          onClick={() => setExpanded((e) => !e)}
          aria-expanded={expanded}
        >
          <span className="oc-upstream-row-number">#{issue.number}</span>
          <span className="oc-upstream-row-title">{issue.title}</span>
          <span className={`oc-upstream-status oc-upstream-status-${issue.status}`}>
            {issue.status}
          </span>
        </button>
        <OpenInBrowser url={issue.url} host={issue.host} testId={`issue-row-${issue.number}-open`} />
      </div>
      <RowMeta
        author={issue.author}
        updatedAt={issue.updatedAt}
        labels={issue.labels}
        assignees={issue.assignees}
      />
      {expanded && (
        <div className="oc-upstream-row-detail" data-testid={`issue-detail-${issue.number}`}>
          <a
            className="oc-upstream-row-link"
            href={issue.url}
            target="_blank"
            rel="noreferrer noopener"
          >
            View on {issue.host}
          </a>
          <div className="oc-upstream-row-body">
            {issue.body ? (
              <ReactMarkdown remarkPlugins={[remarkGfm]}>{issue.body}</ReactMarkdown>
            ) : (
              <em>No description.</em>
            )}
          </div>
          <LaunchSplitButton
            directory={directory}
            remote={remote}
            type="issue"
            number={issue.number}
            crossFork={false}
          />
        </div>
      )}
    </li>
  );
}
