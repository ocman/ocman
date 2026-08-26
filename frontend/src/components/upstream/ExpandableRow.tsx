import { useState, type ReactNode } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import type { ForgeUser, Label } from '../../lib/upstreamApi';
import { LaunchSplitButton } from './LaunchSplitButton';
import { OpenInBrowser } from './OpenInBrowser';
import { RowMeta } from './RowMeta';

interface ExpandableRowProps {
  type: 'pr' | 'issue';
  number: number;
  title: string;
  body: string;
  author: string;
  status: string;
  updatedAt: string;
  labels?: Label[] | null;
  assignees?: ForgeUser[] | null;
  url: string;
  host: string;
  directory: string;
  remoteId?: string;
  remote: string;
  crossFork: boolean;
  className?: string;
  onToggle?: () => void;
  onMouseEnter?: () => void;
  summaryPrefix?: ReactNode;
  summarySuffix?: ReactNode;
  detailBeforeBody?: ReactNode;
  detailAfterBody?: ReactNode;
}

export function ExpandableRow({
  type,
  number,
  title,
  body,
  author,
  status,
  updatedAt,
  labels,
  assignees,
  url,
  host,
  directory,
  remoteId = 'local',
  remote,
  crossFork,
  className = '',
  onToggle,
  onMouseEnter,
  summaryPrefix,
  summarySuffix,
  detailBeforeBody,
  detailAfterBody,
}: ExpandableRowProps) {
  const [expanded, setExpanded] = useState(false);
  const rowId = `${type}-row-${number}`;

  return (
    <li
      className={`oc-upstream-row oc-upstream-row-${type}${expanded ? ' expanded' : ''}${className ? ` ${className}` : ''}`}
      data-testid={rowId}
      onMouseEnter={onMouseEnter}
    >
      <div className="oc-upstream-row-head">
        <button
          type="button"
          className="oc-upstream-row-summary"
          onClick={() => {
            setExpanded((value) => !value);
            onToggle?.();
          }}
          aria-expanded={expanded}
        >
          {summaryPrefix}
          <span className="oc-upstream-row-number">#{number}</span>
          <span className="oc-upstream-row-title">{title}</span>
          {summarySuffix}
          <span className={`oc-upstream-status oc-upstream-status-${status}`}>{status}</span>
        </button>
        <OpenInBrowser url={url} host={host} testId={`${rowId}-open`} />
      </div>
      <RowMeta author={author} updatedAt={updatedAt} labels={labels} assignees={assignees} />
      {expanded && (
        <div className="oc-upstream-row-detail" data-testid={`${type}-detail-${number}`}>
          <a className="oc-upstream-row-link" href={url} target="_blank" rel="noreferrer noopener">
            View on {host}
          </a>
          {detailBeforeBody}
          <div className="oc-upstream-row-body">
            {body ? (
              <ReactMarkdown remarkPlugins={[remarkGfm]}>{body}</ReactMarkdown>
            ) : (
              <em>No description.</em>
            )}
          </div>
          {detailAfterBody}
          <LaunchSplitButton
            directory={directory}
            remoteId={remoteId}
            remote={remote}
            type={type}
            number={number}
            crossFork={crossFork}
          />
        </div>
      )}
    </li>
  );
}
