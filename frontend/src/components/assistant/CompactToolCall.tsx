import type { ReactNode } from 'react';
import { extractPatchPayload, parseJsonObject, parsePatchSections, splitToolArgs, type parseToolTime } from '../../lib/threadHelpers';
import { relativizePath } from '../../lib/convertMessages';
import { BashPrompt, ToolDuration } from './toolRenderers';
import { Button } from '../Control';
import './CompactToolCall.css';

interface Props {
  toolName: string;
  args: string;
  description?: string;
  projectDirectory?: string;
  status: string;
  expanded: boolean;
  onToggle: () => void;
  timeInfo: ReturnType<typeof parseToolTime>;
  children: ReactNode;
}

function compactFilePaths(name: string, args: string, projectDirectory: string): string[] {
  if (name === 'bash') return [];
  if (name === 'apply_patch') {
    const sections = parsePatchSections(extractPatchPayload(args).patchText || '');
    return [...new Set(sections.map(section => relativizePath(section.path, projectDirectory)))];
  }
  const { title, detail } = splitToolArgs(name, args);
  const input = parseJsonObject(args) || parseJsonObject(detail);
  const path = input?.filePath || input?.file_path || input?.path;
  const displayPath = typeof path === 'string' ? path : title.replace(/^(?:Write|Edit) /, '').trim();
  return displayPath ? [relativizePath(displayPath, projectDirectory)] : [];
}

function compactSummary(name: string, args: string, paths: string[], description?: string) {
  if (name === 'bash') return description?.trim() ? `Bash: ${description.trim()}` : `$ ${args.trim()}`;
  const action = name === 'write' ? 'Writing' : 'Patching';
  return paths.length ? `${action} ${paths.length} ${paths.length === 1 ? 'file' : 'files'}` : `${action} files`;
}

export function CompactToolCall({ toolName, args, description, projectDirectory = '', status, expanded, onToggle, timeInfo, children }: Props) {
  const name = toolName.toLowerCase().replace(/^mcp_/, '');
  const describedShell = name === 'bash' && !!description?.trim();
  const paths = compactFilePaths(name, args, projectDirectory);
  const summary = compactSummary(name, args, paths, description);
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
        <span className="oc-read-arrow" aria-hidden="true" title={describedShell && running ? undefined : statusTitle}>{failed ? '\u2717' : running && !describedShell ? '\u223C' : '\u2192'}</span>
        <span className="oc-tool-compact-content">
          <span className="oc-tool-compact-heading">
            <span className="oc-tool-compact-summary">{summary}</span>
            {timeInfo && <>{' '}<ToolDuration startedAt={timeInfo.startedAt} completedAt={timeInfo.completedAt} isRunning={running} /></>}
          </span>
          {describedShell && <span className="oc-tool-compact-command"><span aria-hidden="true">└</span><BashPrompt running={running} /> <span>{args.trim()}</span></span>}
          {paths.map(path => <span className="oc-tool-compact-file" key={path}><span aria-hidden="true">└</span><span title={path}>{path}</span></span>)}
        </span>
      </Button>
      {expanded && <div className="oc-tool-compact-body">{children}</div>}
    </div>
  );
}
