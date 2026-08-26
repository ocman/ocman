import type { Issue } from '../../lib/upstreamApi';
import { ExpandableRow } from './ExpandableRow';

interface IssueRowProps {
  issue: Issue;
  directory: string;
  remoteId?: string;
  remote: string;
}

export function IssueRow({ issue, directory, remoteId = 'local', remote }: IssueRowProps) {
  return (
    <ExpandableRow
      type="issue"
      number={issue.number}
      title={issue.title}
      body={issue.body}
      author={issue.author}
      status={issue.status}
      updatedAt={issue.updatedAt}
      labels={issue.labels}
      assignees={issue.assignees}
      url={issue.url}
      host={issue.host}
      directory={directory}
      remoteId={remoteId}
      remote={remote}
      crossFork={false}
    />
  );
}
