import './StatusBadge.css';

const labels: Record<string, string> = { waiting: 'Waiting', busy: 'Busy', done: 'Done', error: 'Error' };

interface StatusBadgeProps {
  status: string;
  compact?: boolean;
  seen?: boolean;
  /** A pending permission/question prompt needs the user's attention. */
  pending?: boolean;
  /** An unsent composer draft is parked on this session. */
  draft?: boolean;
  /** Override the default tooltip text (e.g. to surface a rate-limit notice). */
  titleOverride?: string;
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

export function StatusBadge({ status, compact, seen, pending, draft, titleOverride }: StatusBadgeProps) {
  if (compact) {
    // A pending permission/question outranks the normal status because it
    // requires user action — show a pulsing "!" icon in the attention color.
    if (pending) {
      return (
        <span
          className="status-icon-compact status-pending"
          title="Waiting for your response"
        >
          <ExclamationIcon />
        </span>
      );
    }
    if (status === 'error') {
      return (
        <span
          className={`status-icon-compact status-error${seen ? ' status-seen' : ''}`}
          title={titleOverride || labels[status] || status}
        >
          <ExclamationIcon />
        </span>
      );
    }
    // An unsent draft turns the dot into a hollow ring. ponytail: only the
    // dot branch — pending/error already render their own attention icon.
    return (
      <span
        className={`status-dot-compact status-${status}${seen ? ' status-seen' : ''}${draft ? ' has-draft' : ''}`}
        title={draft ? 'Unsent draft' : titleOverride || labels[status] || status}
      />
    );
  }
  if (pending) {
    return (
      <span className="status-indicator status-pending" title="Waiting for your response">
        <span className="status-pending-icon">
          <ExclamationIcon />
        </span>
        Prompt
      </span>
    );
  }
  return (
    <span className={`status-indicator status-${status}`}>
      <span className="status-dot" />
      {labels[status] || status}
    </span>
  );
}
