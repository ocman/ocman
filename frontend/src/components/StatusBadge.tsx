import './StatusBadge.css';

const labels: Record<string, string> = { waiting: 'Waiting', busy: 'Busy', done: 'Done', error: 'Error' };

export function StatusBadge({ status, compact, seen }: { status: string; compact?: boolean; seen?: boolean }) {
  if (compact) {
    if (status === 'error') {
      return (
        <span
          className={`status-icon-compact status-error${seen ? ' status-seen' : ''}`}
          title={labels[status] || status}
        >
          <svg width="10" height="10" viewBox="0 0 16 16" fill="none" aria-hidden="true">
            <circle cx="8" cy="8" r="7" stroke="currentColor" strokeWidth="1.5" />
            <line x1="8" y1="4.5" x2="8" y2="9" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
            <circle cx="8" cy="11.5" r="0.75" fill="currentColor" />
          </svg>
        </span>
      );
    }
    return (
      <span
        className={`status-dot-compact status-${status}${seen ? ' status-seen' : ''}`}
        title={labels[status] || status}
      />
    );
  }
  return (
    <span className={`status-indicator status-${status}`}>
      <span className="status-dot" />
      {labels[status] || status}
    </span>
  );
}
