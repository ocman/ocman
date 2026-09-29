// Subagent task tool call: header, live activity, and a markdown preview
// of the final result.
import { useState } from 'react';
import { Link } from 'react-router-dom';
import type { FC } from 'react';
import { useIsPrinting } from '../../lib/useIsPrinting';
import { usePrintCollapse } from '../../lib/printCollapseContext';
import type { Part, PartData } from '../../lib/api';
import { MarkdownText } from './MarkdownText';
import { ToolDuration } from './toolRenderers';

function taskActivity(
  liveTools: { toolName: string; summary?: string }[],
  parts: Part[],
) {
  const liveTool = liveTools.at(-1);
  if (liveTool) return `${liveTool.toolName}${liveTool.summary ? `: ${liveTool.summary}` : ''}`;

  for (let i = parts.length - 1; i >= 0; i--) {
    const data = parts[i].data;
    const part = typeof data === 'string'
      ? (() => { try { return JSON.parse(data) as PartData; } catch { return null; } })()
      : data;
    if (!part || part.type === 'reasoning') continue;
    if (part.type === 'tool' && part.tool) {
      return part.state?.title ? `${part.tool}: ${part.state.title}` : `Using ${part.tool}`;
    }
    const text = part.text;
    if (text?.trim()) return text.replace(/\s+/g, ' ').trim();
  }
  return 'Waiting for activity...';
}

export const TaskToolCard: FC<{ argsText: string; result: unknown; timeInfo: { startedAt: number; completedAt: number } | null }> = ({ argsText, result, timeInfo }) => {
  const [taskExpandedState, setTaskExpanded] = useState(false);
  const isPrinting = useIsPrinting();
  const printCollapse = usePrintCollapse();
  const taskExpanded = taskExpandedState || (isPrinting && !printCollapse);
  const lines = (argsText || '').split('\n');
  const taskStatus = lines[0] || 'running';
  const label = lines.slice(1).join(' ').trim() || 'Subagent task';

  let sessionId = '';
  let taskOutput = '';
  type LiveTool = { toolName: string; summary?: string; subagentId?: string; startedAt?: string };
  let liveTools: LiveTool[] = [];
  let subParts: Part[] = [];
  try {
    const parsed = JSON.parse(typeof result === 'string' ? result : '{}');
    sessionId = parsed.taskId || '';
    const rawTaskOutput = parsed.taskOutput || '';
    taskOutput = (rawTaskOutput.match(/<task_result(?:\s[^>]*)?>([\s\S]*?)(?:<\/task_result>|$)/i)?.[1] ?? rawTaskOutput).trim();
    if (Array.isArray(parsed.liveTools)) liveTools = parsed.liveTools as LiveTool[];
    if (parsed.subSession) {
      const sub = parsed.subSession as { parts?: unknown[] };
      if (Array.isArray(sub.parts)) subParts = sub.parts as Part[];
    }
  } catch { /* ignore */ }

  let statusClass = 'oc-tool-running';
  let statusTitle = 'Running';
  if (taskStatus === 'completed') { statusClass = 'oc-tool-done'; statusTitle = 'Completed'; }
  else if (taskStatus === 'error') { statusClass = 'oc-tool-error'; statusTitle = 'Error'; }

  const activity = taskActivity(liveTools, subParts);

  return (
    <div className={`oc-tool oc-tool-task ${statusClass} ${taskExpanded ? 'oc-tool-expanded' : ''}`}>
      <div className="oc-tool-header">
        <span className="oc-tool-label">{label}</span>
        <span className={`oc-task-status ${statusClass}`}>{statusTitle}</span>
        {timeInfo && <ToolDuration startedAt={timeInfo.startedAt} completedAt={timeInfo.completedAt} isRunning={taskStatus === 'running'} />}
        {sessionId && <Link className="oc-task-link" to={`/session/${encodeURIComponent(sessionId)}`} aria-label="Open detailed subagent session">{'\u2197'}</Link>}
      </div>
      {taskOutput ? (
        <div
          className={`oc-task-result oc-md ${taskExpanded ? 'oc-task-result-expanded' : ''}`}
          role="button"
          tabIndex={0}
          aria-expanded={taskExpanded}
          aria-label={`${taskExpanded ? 'Collapse' : 'Expand'} subagent task result`}
          onClick={() => setTaskExpanded((expanded) => !expanded)}
          onKeyDown={(event) => {
            if (event.key !== 'Enter' && event.key !== ' ') return;
            event.preventDefault();
            setTaskExpanded((expanded) => !expanded);
          }}
        >
          <MarkdownText text={taskOutput} />
        </div>
      ) : (
        <div className="oc-task-activity" data-testid="subagent-activity" title={activity} aria-live="polite">{activity}</div>
      )}
    </div>
  );
};
