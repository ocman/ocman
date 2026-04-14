const labels: Record<string, string> = { waiting: 'Waiting', busy: 'Busy', done: 'Done', error: 'Error' };

export function StatusBadge({ status, compact, seen }: { status: string; compact?: boolean; seen?: boolean }) {
  if (compact) {
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
