import './StatusBadge.css';
import { SessionStatusIndicator, type SessionStatusIndicatorState } from './SessionStatusIndicator';

const labels: Record<string, string> = {
  waiting: 'Waiting',
  busy: 'Busy',
  done: 'Done',
  error: 'Error',
  interrupted: 'Interrupted',
  limited: 'Limited',
};

/** Tooltip for states whose one-word label doesn't explain itself. */
const hints: Record<string, string> = {
  interrupted: 'Interrupted — the agent process stopped before the turn finished',
};

interface StatusBadgeProps {
  status: string;
  resetsAt?: number;
  compact?: boolean;
  /** Running finite background tasks, excluding declared services. */
  backgroundTaskCount?: number;
  seen?: boolean;
  /** A pending permission/question prompt needs the user's attention. */
  pending?: boolean;
  /** An unsent composer draft is parked on this session. */
  draft?: boolean;
  /** Override the default tooltip text (e.g. to surface a rate-limit notice). */
  titleOverride?: string;
}

function indicatorState(status: string, seen?: boolean, pending?: boolean, draft?: boolean, backgroundTaskCount = 0): SessionStatusIndicatorState {
  if (pending) return 'permission';
  if (status === 'error') return 'error';
  if (status === 'limited') return 'limited';
  // A draft on a running turn keeps the spinner, in the draft colour.
  if (draft) return status === 'busy' ? 'draft-busy' : 'draft';
  if (backgroundTaskCount > 0 && (status === 'done' || status === 'waiting')) return 'background';
  if (seen) return 'viewed';
  if (status === 'busy' || status === 'waiting' || status === 'interrupted') return status;
  return 'done';
}

export function StatusBadge({ status, resetsAt, compact, backgroundTaskCount = 0, seen, pending, draft, titleOverride }: StatusBadgeProps) {
  const state = indicatorState(status, seen, pending, draft, compact ? backgroundTaskCount : 0);
  const label = status === 'limited' && resetsAt ? `Limited until ${new Date(resetsAt).toLocaleString()}` : labels[status] || status;
  const title = state === 'background' ? `Waiting for ${backgroundTaskCount} background task${backgroundTaskCount === 1 ? '' : 's'}` : status === 'limited' ? label : pending
    ? 'Waiting for your response'
    : draft
      ? status === 'busy' ? 'Unsent draft — still working' : 'Unsent draft'
      : titleOverride || hints[status] || label;
  if (compact) {
    return (
      <SessionStatusIndicator
        state={state}
        compact
        aria-label={`Session status: ${title}`}
        title={title}
      />
    );
  }
  return (
    <span className={`status-indicator status-${pending ? 'pending' : status}`} title={pending || hints[status] ? title : undefined}>
      <SessionStatusIndicator state={state} />
      {pending ? 'Prompt' : label}
    </span>
  );
}
