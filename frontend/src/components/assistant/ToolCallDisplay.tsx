// Per-tool-call rendering: the ToolCallDisplay dispatcher. Output
// renderers live in toolRenderers.tsx, subagent tasks in TaskToolCard.tsx,
// and approval footnotes in ApprovalFootnote.tsx.
import React, { useState } from 'react';
import { type ToolCallMessagePartProps } from '@assistant-ui/react';
import { parseTodos } from '../../lib/todos';
import { TodoList } from '../TodoList';
import { isMutedLineTool } from '../../lib/mutedTools';
import { useIsPrinting } from '../../lib/useIsPrinting';
import { usePrintCollapse } from '../../lib/printCollapseContext';
import {
  highlightDiffCode,
  toolOutputLanguage,
  extractPatchPayload,
  splitToolArgs,
  summarizeToolArgs,
  summarizePatch,
  parseQuestionAnswers,
  parseQuestions,
  parseToolTime,
  parseShellDescription,
  type ToolApproval,
} from '../../lib/threadHelpers';
import type { FC } from 'react';
import { ArtifactToolCard } from './ArtifactToolCard';
import { parseCreatedArtifact } from '../../lib/artifactsApi';
import { ApprovalFootnote } from './ApprovalFootnote';
import { TaskToolCard } from './TaskToolCard';
import { FactoryMarkerCard } from '../FactoryMarkerCard';
import { factoryCardFromToolResult } from '../factoryCards';
import { AnsiText, AnsweredQuestionBlock, BashPrompt, ToolDuration } from './toolRenderers';
import { LazyInlineDiff } from './LazyInlineDiff';
import { parseDiffPayload, renderOutput, renderPatch, shellOutputIsLong, shellOutputPreview, toolOutputPreview } from './toolOutputFormat';
/**
 * Tool-call renderer. Validated Ocman approval artifacts render below the
 * tool body; argsText remains entirely user-visible and untrusted.
 */
export const ToolCallDisplay: FC<ToolCallMessagePartProps> = (props) => {
  const approvals = toolApprovals(props.artifact);
  let body: React.ReactNode;
  if (props.toolName === 'ocman:auto-approved') {
    const aiApprovals = approvals.filter((approval) => approval.approvedBy === 'ai');
    body = aiApprovals.length > 0
      ? <ApprovalFootnote approvals={aiApprovals} />
      : <ToolCallBody {...props} />;
  } else if (approvals.length === 0) {
    body = <ToolCallBody {...props} />;
  } else {
    body = (
      <div className="oc-tool-with-footnote">
        <ToolCallBody {...props} />
        <ApprovalFootnote approvals={approvals} />
      </div>
    );
  }
  return (
    <div className="oc-tool-source" data-perf="tool-call" data-tool-call-id={props.toolCallId}>
      {body}
    </div>
  );
};


function toolApprovals(artifact: unknown): ToolApproval[] {
  if (!artifact || typeof artifact !== 'object' || Array.isArray(artifact)) return [];
  const approvals = (artifact as { ocmanApprovals?: unknown }).ocmanApprovals;
  if (!Array.isArray(approvals)) return [];
  return approvals.filter((approval): approval is ToolApproval => {
    if (!approval || typeof approval !== 'object' || Array.isArray(approval)) return false;
    const value = approval as Partial<ToolApproval>;
    return typeof value.permission === 'string'
      && Array.isArray(value.patterns)
      && value.patterns.every((pattern) => typeof pattern === 'string')
      && typeof value.reasoning === 'string'
      && (value.approvedBy === 'user' || value.approvedBy === 'ai');
  });
}

const ToolCallBody: FC<ToolCallMessagePartProps> = ({ toolName, argsText: rawArgsText, artifact, result }) => {
  const [expandedState, setExpanded] = useState(false);
  // While printing / saving to PDF, force every block open so the
  // exported transcript is complete. CSS lifts the max-height caps
  // (see the @media print block in tokens.css); this additionally
  // reveals the few bodies that are gated on React state and would
  // otherwise be absent from the DOM. Length caps (toolOutputPreview)
  // still apply, so a single huge log can't balloon the PDF.
  //
  // The shared-conversation page can opt out via PrintCollapseContext so
  // the reader controls how verbose the PDF is: when collapse is
  // requested, only individually-expanded blocks print expanded.
  const isPrinting = useIsPrinting();
  const printCollapse = usePrintCollapse();
  const forcePrintExpand = isPrinting && !printCollapse;
  const expanded = expandedState || forcePrintExpand;

  // Extract timing data from the @time: line, if present.
  const timeInfo = parseToolTime(rawArgsText || '');
  const argsTextWithMeta = timeInfo ? timeInfo.strippedArgs : (rawArgsText || '');
  const { description: shellDescription, strippedArgs: argsTextNoDesc } = parseShellDescription(argsTextWithMeta);
  const argsLines = argsTextNoDesc.split('\n');
  const userExecutedTool = argsLines.includes('@user-executed-tool');
  const argsText = argsLines.filter((line) => line !== '@user-executed-tool').join('\n');

  if (toolName === 'ocman:auto-approved') {
    let approval: Partial<ToolApproval> = toolApprovals(artifact)[0] || {};
    if (!approval.permission) {
      try { approval = JSON.parse(argsText) as Partial<ToolApproval>; } catch { /* legacy notice */ }
    }
    const approvedBy = approval.approvedBy === 'user' ? 'user' : 'AI';
    return (
      <div className="oc-read-line oc-approval-notice">
        <span className="oc-read-arrow" aria-hidden="true">{`\u2713`}</span>
        <span>{`Permission approved by ${approvedBy}`}</span>
        {approval.permission && <span>{approval.permission}</span>}
      </div>
    );
  }

  // File reads/greps and Skill loads render as a muted inline line
  // with an arrow icon. Skill is here (rather than in its own branch)
  // because its renderer is byte-for-byte identical to a read: a one
  // line label, no collapsible body, no input JSON. The provider
  // builds an argsText like `Skill "create-commit"` so this branch
  // just displays whatever it gets.
  if (isMutedLineTool(toolName)) {
    return (
      <div className="oc-read-line">
        <span className="oc-read-arrow">{'\u2192'}</span>
        <span>{argsText || 'Read'}</span>
      </div>
    );
  }

  // Subagent tasks render their final result as a short markdown preview.
  if (toolName === '__task__') return <TaskToolCard argsText={argsText} result={result} timeInfo={timeInfo} />;

  // Questions are answered in the composer slot. While pending, show a muted
  // summary. Once answered, repeat the question and answer in a compact block.
  if (toolName === '__question__') {
    const questions = parseQuestions(argsText || '');
    if (questions) {
      const answers = parseQuestionAnswers(result);
      if (answers) {
        return <AnsweredQuestionBlock questions={questions} answers={answers} />;
      }
      const count = questions.length;
      return (
        <div className="oc-read-line">
          <span className="oc-read-arrow">{'\u2192'}</span>
          <span>{`Asked ${count} question${count === 1 ? '' : 's'}`}</span>
        </div>
      );
    }
  }

  // First line of argsText is the tool's own status (completed/running/error)
  const lines = (argsText || '').split('\n');
  const toolStatus = lines[0] || 'running';
  const remainingArgs = lines.slice(1).join('\n');
  const createdArtifact = toolStatus === 'completed' ? parseCreatedArtifact(toolName, result) : null;
  if (createdArtifact) return <ArtifactToolCard artifact={createdArtifact} />;

  // Show tool calls that have content, are completed, or are actively running.
  // Only hide if there's truly nothing to show (no args, no result, no
  // meaningful status). "pending" and "running" states should remain visible
  // so the user can see operations in progress (e.g. "preparing to write").
  const hasArgs = remainingArgs.trim() && remainingArgs.trim() !== '{}';
  const hasResult = result && String(result).trim() && String(result).trim() !== '{}';
  const isActive = toolStatus === 'running' || toolStatus === 'pending';
  if (!hasArgs && !hasResult && !isActive && toolStatus !== 'completed') return null;

  // Empty tool calls that are still running render as a muted preparing indicator
  if (!hasArgs && !hasResult && isActive) {
    const lowerName = toolName.toLowerCase().replace(/^mcp_/, '');
    const preparingLabel =
      lowerName === 'edit' ? 'Preparing edit…' :
      lowerName === 'write' ? 'Preparing write…' :
      lowerName === 'bash' ? 'Preparing command…' :
      lowerName === 'read' ? 'Preparing read…' :
      lowerName === 'grep' ? 'Preparing search…' :
      lowerName === 'glob' ? 'Preparing file search…' :
      lowerName === 'task' ? 'Preparing task…' :
      lowerName === 'todowrite' ? 'Updating tasks…' :
      lowerName === 'webfetch' ? 'Preparing fetch…' :
      lowerName === 'question' ? 'Preparing question…' :
      `Preparing ${lowerName}…`;
    return (
      <div className="oc-read-line">
        <span className="oc-read-arrow">{'\u223C'}</span>
        <span>{preparingLabel}</span>
      </div>
    );
  }

  let statusIcon = '\u2022';
  let statusClass = 'oc-tool-running';
  let statusTitle = 'Running';
  if (toolStatus === 'completed') { statusIcon = '\u2713'; statusClass = 'oc-tool-done'; statusTitle = 'Completed'; }
  else if (toolStatus === 'error') { statusIcon = '\u2717'; statusClass = 'oc-tool-error'; statusTitle = 'Error'; }

  let outputDisplay = '';
  if (typeof result === 'string') outputDisplay = result;
  else if (result != null) outputDisplay = JSON.stringify(result, null, 2);

  const { title: parsedTitle, detail } = splitToolArgs(toolName, remainingArgs);
  const title = parsedTitle || toolName;

  const isLong = outputDisplay.length > 500 || (detail && detail.length > 300);
  // Truncation honors the *real* expand state (not the print-forced
  // one) so the 5000-char cap still applies in PDFs — printing reveals
  // collapsed blocks but does not lift the length cap on huge outputs.
  const outputPreview = toolOutputPreview(outputDisplay, expandedState);

  // Detect TodoWrite tool calls and render as a checklist
  const isTodo = toolName === 'mcp_todowrite' || toolName === 'todowrite' || toolName === 'TodoWrite';
  const todos = isTodo ? parseTodos(detail, result) : null;

  if (todos) {
    return (
      <div className={`oc-tool ${statusClass}`}>
        {/* The task list is always rendered, so this header has nothing
            to expand — it carries no toggle at all. */}
        <div className="oc-tool-header">
          <i className={`bi bi-check2-square oc-tool-icon ${statusClass}`} title={statusTitle} aria-hidden="true" />
          <span className="oc-tool-label">{title && title !== toolName ? title : 'Task list'}</span>
          {timeInfo && <ToolDuration startedAt={timeInfo.startedAt} completedAt={timeInfo.completedAt} isRunning={toolStatus === 'running'} />}
        </div>
        <div className="oc-tool-content">
          <TodoList todos={todos} />
        </div>
      </div>
    );
  }

  const isApplyPatch = toolName === 'apply_patch';
  if (isApplyPatch) {
    const patchSource = remainingArgs.trim();
    const { patchText } = extractPatchPayload(patchSource || detail);
    const patchSummary = patchText ? summarizePatch(patchText) : 'Apply patch';
    const patchBody = patchText || '';

    return (
      <div className={`oc-tool oc-tool-patch ${statusClass} ${expanded ? 'oc-tool-expanded' : ''}`}>
        <button
          type="button"
          className="oc-tool-header"
          aria-expanded={expanded}
          onClick={() => setExpanded(!expanded)}
        >
          <span className={`oc-tool-icon ${statusClass}`} title={statusTitle}>{statusIcon}</span>
          <span className="oc-tool-label">{patchSummary}</span>
          {timeInfo && <ToolDuration startedAt={timeInfo.startedAt} completedAt={timeInfo.completedAt} isRunning={toolStatus === 'running'} />}
        </button>
        {expanded && patchBody && (
          <div className="oc-tool-content">
            {renderPatch(patchBody)}
          </div>
        )}
      </div>
    );
  }

  // Edit / Write tools get a diff-style rendering
  const isEditTool = toolName === 'edit' || toolName === 'mcp_edit' || toolName === 'mcp_Edit';
  const isWriteTool = toolName === 'write' || toolName === 'mcp_write' || toolName === 'mcp_Write';
  if (isEditTool || isWriteTool) {
    const diffPayload = parseDiffPayload(result as string | null | undefined);
    return (
      <div className={`oc-tool oc-tool-edit ${statusClass} ${expanded || diffPayload ? 'oc-tool-expanded' : ''}`}>
        <button
          type="button"
          className="oc-tool-header"
          aria-expanded={expanded}
          onClick={() => setExpanded(!expanded)}
        >
          <i className={`bi bi-pencil-fill oc-tool-icon ${statusClass}`} title={statusTitle} aria-hidden="true" />
          <span className="oc-tool-label">{title || toolName}</span>
          {timeInfo && <ToolDuration startedAt={timeInfo.startedAt} completedAt={timeInfo.completedAt} isRunning={toolStatus === 'running'} />}
        </button>
        {(diffPayload || outputDisplay) && (
          <div className="oc-tool-content" onClick={() => !expanded && !diffPayload && setExpanded(true)} style={!expanded && !diffPayload ? { cursor: 'pointer' } : undefined}>
            {diffPayload
              ? <LazyInlineDiff payload={diffPayload} />
              : <pre className="oc-tool-pre oc-tool-output">{outputPreview}</pre>
            }
            {!expanded && !diffPayload && isLong && (
              <div className="oc-tool-expand">Click to expand</div>
            )}
          </div>
        )}
      </div>
    );
  }

  // Shell commands get a terminal-style rendering
  const isBash = toolName === 'bash' || toolName === 'mcp_bash';
  if (isBash) {
    // The command is whatever is left after the status/meta lines, kept
    // verbatim — multi-line commands (heredocs) render as typed.
    const command = remainingArgs.trim();
    const description = shellDescription;
    const bashOutput = outputDisplay;
    const bashIsLong = shellOutputIsLong(bashOutput);
    const forcePrintCollapse = isPrinting && printCollapse;
    const bashExpanded = expanded || !bashIsLong;
    const bashOutputDisplay = bashExpanded ? bashOutput : shellOutputPreview(bashOutput);
    const toggleLabel = expanded ? 'Collapse output' : 'Show full output';
    return (
      <div className={`oc-tool oc-tool-shell ${bashExpanded ? 'oc-tool-expanded' : ''}`}>
        {!forcePrintCollapse && (
          <div className="oc-tool-content" onClick={() => !bashExpanded && setExpanded(true)} style={!bashExpanded ? { cursor: 'pointer' } : undefined}>
            <pre className="oc-shell-block" data-testid="shell-output-block">
{description && <><span className="oc-shell-description"># {description}</span>{'\n\n'}</>}{command && <><BashPrompt running={toolStatus === 'running'} /> <span className="oc-shell-cmd" dangerouslySetInnerHTML={{ __html: highlightDiffCode(command, 'bash') }} />{bashOutputDisplay ? '\n' : ''}</>}{bashOutputDisplay && <AnsiText text={bashOutputDisplay} />}
              {bashIsLong && (
                <button
                  type="button"
                  className="oc-tool-expand oc-shell-output-toggle"
                  aria-expanded={expanded}
                  onClick={(event) => {
                    event.stopPropagation();
                    setExpanded(!expanded);
                  }}
                >
                  {toggleLabel}
                </button>
              )}
            </pre>
            {userExecutedTool && (
              <div className="oc-shell-attribution">The following tool was executed by the user</div>
            )}
          </div>
        )}
      </div>
    );
  }

  // Generic / MCP tool calls render as a single muted line by default,
  // matching the look of read/grep/glob lines. Click to expand into
  // an inline panel showing the raw args and result. Errors get a red
  // accent but stay collapsed — same compact treatment as success so
  // the conversation stays scannable.
  const summary = parsedTitle || summarizeToolArgs(remainingArgs);
  const arrowIcon = toolStatus === 'running' ? '\u223C' : toolStatus === 'error' ? '\u2717' : '\u2192';
  const compactClass = [
    'oc-tool-compact',
    toolStatus === 'error' ? 'oc-tool-compact-error' : '',
    toolStatus === 'running' ? 'oc-tool-compact-running' : '',
    expanded ? 'oc-tool-compact-expanded' : '',
  ].filter(Boolean).join(' ');
  const hasBody = !!(detail || outputDisplay);
  const factoryCard = factoryCardFromToolResult(toolName, outputDisplay);
  const compactLine = (
    <>
      <span className="oc-read-arrow" aria-hidden="true" title={statusTitle}>{arrowIcon}</span>
      <span className="oc-tool-compact-name">{toolName}</span>
      {summary && <span className="oc-tool-compact-summary">{summary}</span>}
      {timeInfo && <ToolDuration startedAt={timeInfo.startedAt} completedAt={timeInfo.completedAt} isRunning={toolStatus === 'running'} />}
    </>
  );

  const row = (
    <div className={compactClass}>
      {/* A button only when there is something to reveal; an inert line
          otherwise, so nothing focusable does nothing. */}
      {hasBody ? (
        <button
          type="button"
          className="oc-tool-compact-line"
          aria-expanded={expanded}
          style={{ cursor: 'pointer' }}
          onClick={() => setExpanded(!expanded)}
        >
          {compactLine}
        </button>
      ) : (
        <div className="oc-tool-compact-line">{compactLine}</div>
      )}
      {expanded && hasBody && (
        <div className="oc-tool-compact-body">
          {detail && <pre className="oc-tool-pre">{detail}</pre>}
          {outputDisplay && (
            <pre className="oc-tool-pre oc-tool-output">{renderOutput(outputPreview, toolOutputLanguage(parsedTitle, detail || ''))}</pre>
          )}
        </div>
      )}
    </div>
  );
  // Outside the compact row so a denied call's error colour doesn't tint the card.
  if (!factoryCard) return row;
  return <>{row}<div><FactoryMarkerCard epicID={factoryCard.epicID} issueID={factoryCard.issueID} action={factoryCard.action} /></div></>;
};
