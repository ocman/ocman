import type { HTMLAttributes } from 'react';
import './SessionStatusIndicator.css';

export type SessionStatusIndicatorState =
  | 'busy'
  | 'waiting'
  | 'done'
  | 'viewed'
  | 'permission'
  | 'error'
  | 'interrupted'
  | 'draft'
  /** An unsent draft on a session whose turn is still running. */
  | 'draft-busy';

interface SessionStatusIndicatorProps extends HTMLAttributes<HTMLSpanElement> {
  state: SessionStatusIndicatorState;
  compact?: boolean;
}

function ExclamationIcon() {
  return (
    <svg width="10" height="10" viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <circle cx="8" cy="8" r="7" stroke="currentColor" strokeWidth="1.5" />
      <line x1="8" y1="4.5" x2="8" y2="9" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
      <circle cx="8" cy="11.5" r="0.75" fill="currentColor" />
    </svg>
  );
}

export function SessionStatusIndicator({ state, compact, className, ...props }: SessionStatusIndicatorProps) {
  const labelled = props['aria-label'] || props['aria-labelledby'];
  const classes = `session-status-indicator${compact ? ' session-status-indicator--compact' : ''}${className ? ` ${className}` : ''}`;
  return (
    <span aria-hidden={labelled ? undefined : true} {...props} className={classes} data-state={state}>
      {(state === 'permission' || state === 'error') && <ExclamationIcon />}
    </span>
  );
}
