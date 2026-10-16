import type { ReactNode } from 'react';
import { extractPatchPayload, splitToolArgs, summarizePatch, summarizeToolArgs, type parseToolTime } from '../../lib/threadHelpers';
import { ToolDuration } from './toolRenderers';
import { Button } from '../Control';

interface Props {
  toolName: string;
  args: string;
  status: string;
  expanded: boolean;
  onToggle: () => void;
  timeInfo: ReturnType<typeof parseToolTime>;
  children: ReactNode;
}

export function CompactToolCall({ toolName, args, status, expanded, onToggle, timeInfo, children }: Props) {
  const name = toolName.toLowerCase().replace(/^mcp_/, '');
  const { title } = splitToolArgs(toolName, args);
  const summary = name === 'bash' ? `$ ${args.trim()}`
    : name === 'apply_patch' ? summarizePatch(extractPatchPayload(args).patchText || '')
    : `${name === 'write' ? 'Create' : 'Edit'} ${title || summarizeToolArgs(args)}`;
  const running = status === 'running' || status === 'pending';
  const failed = status === 'error';
  const statusTitle = failed ? 'Error' : running ? 'Running' : 'Completed';
  const className = [
    'oc-tool-compact',
    running ? 'oc-tool-compact-running' : '',
    failed ? 'oc-tool-compact-error' : '',
    expanded ? 'oc-tool-compact-expanded' : '',
  ].filter(Boolean).join(' ');

  return (
    <div className={className}>
      <Button variant="ghost" type="button" className="oc-tool-compact-line" aria-expanded={expanded} onClick={onToggle}>
        <span className="oc-read-arrow" aria-hidden="true" title={statusTitle}>{failed ? '\u2717' : running ? '\u223C' : '\u2192'}</span>
        <span className="oc-tool-compact-summary">{summary}</span>
        {timeInfo && <ToolDuration startedAt={timeInfo.startedAt} completedAt={timeInfo.completedAt} isRunning={running} />}
      </Button>
      {expanded && <div className="oc-tool-compact-body">{children}</div>}
    </div>
  );
}
