import { memo, useEffect, useId, useLayoutEffect, useRef, useState, type MouseEvent } from 'react';
import type { ToolApproval, parseToolTime } from '../../lib/threadHelpers';
import { Button } from '../Control';
import { ApprovalLock } from './ApprovalLock';
import { HighlightedCode } from './HighlightedCode';
import { AnsiText, BashPrompt, ToolDuration } from './toolRenderers';
import { shellOutputIsLong, shellOutputPreview } from './toolOutputFormat';

// Keep the last painted text (and its scroll height) when the log leaves view.
const LiveOutputText = memo(function LiveOutputText({ output }: { output: string; paused: boolean }) {
  return output ? <AnsiText text={output} /> : 'Waiting for output…';
}, (previous, next) => next.paused || previous.output === next.output);

function isRowClick({ target }: MouseEvent) {
  return target instanceof Element && !target.closest('button, a') && window.getSelection()?.isCollapsed !== false;
}

function shellOutputView(output: string, running: boolean, expanded: boolean) {
  if (running) return { preview: '', bounded: false, toggleLabel: expanded ? 'Collapse output' : 'Show live output' };
  if (!shellOutputIsLong(output)) return { preview: output, bounded: false, toggleLabel: '' };
  return expanded
    ? { preview: output, bounded: false, toggleLabel: 'Collapse output' }
    : { preview: shellOutputPreview(output), bounded: true, toggleLabel: 'Show full output' };
}

function LiveShellOutput({ id, output }: { id: string; output: string }) {
  const ref = useRef<HTMLSpanElement>(null);
  const follow = useRef(true);
  const [visible, setVisible] = useState(true);

  useEffect(() => {
    const node = ref.current;
    if (!node || typeof IntersectionObserver === 'undefined') return;
    // The browser viewport also accounts for clipping by the thread scroller.
    const observer = new IntersectionObserver((entries) => setVisible(entries.at(-1)?.isIntersecting ?? false));
    observer.observe(node);
    return () => observer.disconnect();
  }, []);

  useLayoutEffect(() => {
    const node = ref.current;
    if (visible && follow.current && node) node.scrollTop = node.scrollHeight;
  }, [output, visible]);

  return (
    <span ref={ref} id={id} className="oc-shell-live-output" role="region" aria-label="Live command output" tabIndex={0}
      onScroll={({ currentTarget: node }) => {
        follow.current = node.scrollHeight - node.scrollTop - node.clientHeight <= 2;
      }}>
      <LiveOutputText output={output} paused={!visible} />
    </span>
  );
}

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
  const outputId = useId();
  const running = status === 'running';
  // Commands run directly by the user already show their output by default.
  const [quickShellOpen, setQuickShellOpen] = useState(true);
  const quickShell = running && userExecutedTool;
  const showOutput = quickShell ? quickShellOpen : expanded;
  const toggle = quickShell ? () => setQuickShellOpen(!quickShellOpen) : onToggle;
  const { preview, bounded, toggleLabel } = shellOutputView(output, running, showOutput);
  const clickToExpand = Boolean(toggleLabel) && !showOutput;
  const liveOpen = running && showOutput;
  return (
    <div className={`oc-tool oc-tool-shell ${bounded ? '' : 'oc-tool-expanded'}`}>
      {!forcePrintCollapse && (
        <div className="oc-tool-content" style={clickToExpand ? { cursor: 'pointer' } : undefined}
          onClick={clickToExpand ? (event) => {
            if (isRowClick(event)) toggle();
          } : undefined}>
          <pre className="oc-shell-block" data-testid="shell-output-block">
            {description && <><span className="oc-shell-description"># {description}</span>{'\n\n'}</>}
            {command && <>
              <BashPrompt running={running} />{' '}
              <HighlightedCode className="oc-shell-cmd" code={command} language="bash" />
              {timeInfo && <> <ToolDuration startedAt={timeInfo.startedAt} completedAt={timeInfo.completedAt} isRunning={running} /></>}
              {approvals.length > 0 && <> <ApprovalLock approvals={approvals} /></>}
              {preview ? '\n' : ''}
            </>}
            {preview && <AnsiText text={preview} />}
            {toggleLabel && (
              <Button variant="ghost" size="compact" type="button" className="oc-tool-expand oc-shell-output-toggle" aria-expanded={showOutput}
                aria-controls={liveOpen ? outputId : undefined}
                onClick={(event) => { event.stopPropagation(); toggle(); }}>
                {toggleLabel}
              </Button>
            )}
            {liveOpen && <LiveShellOutput id={outputId} output={output} />}
          </pre>
          {userExecutedTool && <div className="oc-shell-attribution">The following tool was executed by the user</div>}
        </div>
      )}
    </div>
  );
}
