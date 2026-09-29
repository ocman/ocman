// Small components shared by ToolCallDisplay: inline diffs, ANSI shell
// output, answered questions, and durations.
import React, { useState, useEffect, Suspense, type FC } from 'react';
import { MultiFileDiff } from '@pierre/diffs/react';
import { DIFF_OPTIONS } from '../diffOptions';
import { parseAnsi, hasAnsi, hasStyle, type AnsiSegment } from '../../lib/ansi';
import { formatToolDuration, type QuestionData } from '../../lib/threadHelpers';
import type { DiffPayload } from './toolOutputFormat';

export const ToolDuration: FC<{ startedAt: number; completedAt: number; isRunning: boolean; label?: string }> = ({
  startedAt,
  completedAt,
  isRunning,
  label,
}) => {
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    if (!isRunning) return;
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [isRunning]);

  const elapsed = isRunning
    ? now - startedAt
    : completedAt > startedAt
      ? completedAt - startedAt
      : 0;
  if (label) {
    const duration = elapsed > 0 ? ` (${formatToolDuration(elapsed)})` : '';
    return <span className="oc-tool-label">{label}{duration}</span>;
  }
  if (elapsed <= 0) return null;
  return <span className="oc-tool-duration">{formatToolDuration(elapsed)}</span>;
};

const bashSpinnerFrames = ['⣾', '⣽', '⣻', '⢿', '⡿', '⣟', '⣯', '⣷'];

export const BashPrompt: FC<{ running: boolean }> = ({ running }) => {
  const [frame, setFrame] = useState(0);
  useEffect(() => {
    if (!running) return;
    const id = setInterval(() => setFrame((current) => (current + 1) % bashSpinnerFrames.length), 80);
    return () => clearInterval(id);
  }, [running]);

  return <span className="oc-shell-prompt" data-testid={running ? 'bash-spinner' : undefined} title={running ? 'Running' : undefined}>{running ? bashSpinnerFrames[frame] : '$'}</span>;
};

// Renders a before/after diff using @pierre/diffs.
export function InlineDiff({ payload }: { payload: DiffPayload }) {
  const name = payload.filePath || 'file';
  return (
    <Suspense fallback={null}>
      <MultiFileDiff
        oldFile={{ name, contents: payload.before }}
        newFile={{ name, contents: payload.after }}
        options={DIFF_OPTIONS}
        disableWorkerPool
      />
    </Suspense>
  );
}

export function AnsweredQuestionBlock({ questions, answers }: { questions: QuestionData[]; answers: string[] }) {
  return (
    <div className="oc-question-list">
      {questions.map((q, index) => (
        <div key={index} className="oc-question-card oc-question-answered-card">
          <div className="oc-question-text">{q.question}</div>
          <div className="oc-question-answer">{answers[index] || ''}</div>
        </div>
      ))}
    </div>
  );
}

// Renders shell output that may contain ANSI escape sequences. Falls
// back to a plain text node when no escapes are present so we don't
// pay any DOM-overhead cost on uncolored output (the common case for
// successful commands).
export function AnsiText({ text }: { text: string }) {
  if (!hasAnsi(text)) return <>{text}</>;
  const segments = parseAnsi(text);
  return (
    <>
      {segments.map((seg, i) => {
        if (!hasStyle(seg)) return <React.Fragment key={i}>{seg.text}</React.Fragment>;
        return (
          <span key={i} className={ansiClassNames(seg)}>{seg.text}</span>
        );
      })}
    </>
  );
}

function ansiClassNames(seg: AnsiSegment): string {
  const classes: string[] = ['oc-ansi'];
  if (seg.fg) classes.push(`oc-ansi-fg-${seg.fg}`);
  if (seg.bg) classes.push(`oc-ansi-bg-${seg.bg}`);
  if (seg.bold) classes.push('oc-ansi-bold');
  if (seg.dim) classes.push('oc-ansi-dim');
  if (seg.italic) classes.push('oc-ansi-italic');
  if (seg.underline) classes.push('oc-ansi-underline');
  return classes.join(' ');
}
