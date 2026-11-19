import type { ToolApproval, parseToolTime } from '../../lib/threadHelpers';
import { Button } from '../Control';
import { ApprovalLock } from './ApprovalLock';
import { HighlightedCode } from './HighlightedCode';
import { AnsiText, BashPrompt, ToolDuration } from './toolRenderers';
import { shellOutputIsLong, shellOutputPreview } from './toolOutputFormat';

export function ShellToolCall({ command, description, output, status, expanded, onToggle, forcePrintCollapse, userExecutedTool, timeInfo, approvals }: {
  command: string;
  description: string;
  output: string;
  status: string;
  expanded: boolean;
  onToggle: () => void;
  forcePrintCollapse: boolean;
  userExecutedTool: boolean;
  timeInfo: ReturnType<typeof parseToolTime>;
  approvals: ToolApproval[];
}) {
  const isLong = shellOutputIsLong(output);
  const outputExpanded = expanded || !isLong;
  const outputDisplay = outputExpanded ? output : shellOutputPreview(output);
  return (
    <div className={`oc-tool oc-tool-shell ${outputExpanded ? 'oc-tool-expanded' : ''}`}>
      {!forcePrintCollapse && (
        <div className="oc-tool-content" onClick={() => !outputExpanded && onToggle()} style={!outputExpanded ? { cursor: 'pointer' } : undefined}>
          <pre className="oc-shell-block" data-testid="shell-output-block">
            {description && <><span className="oc-shell-description"># {description}</span>{'\n\n'}</>}
            {command && <>
              <BashPrompt running={status === 'running'} />{' '}
              <HighlightedCode className="oc-shell-cmd" code={command} language="bash" />
              {timeInfo && <> <ToolDuration startedAt={timeInfo.startedAt} completedAt={timeInfo.completedAt} isRunning={status === 'running'} /></>}
              {approvals.length > 0 && <> <ApprovalLock approvals={approvals} /></>}
              {outputDisplay ? '\n' : ''}
            </>}
            {outputDisplay && <AnsiText text={outputDisplay} />}
            {isLong && (
              <Button variant="ghost" size="compact" type="button" className="oc-tool-expand oc-shell-output-toggle" aria-expanded={expanded}
                onClick={(event) => { event.stopPropagation(); onToggle(); }}>
                {expanded ? 'Collapse output' : 'Show full output'}
              </Button>
            )}
          </pre>
          {userExecutedTool && <div className="oc-shell-attribution">The following tool was executed by the user</div>}
        </div>
      )}
    </div>
  );
}
