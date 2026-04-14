const labels: Record<string, string> = { waiting: 'Waiting', busy: 'Busy', done: 'Done' };

export function StatusBadge({ status, compact }: { status: string; compact?: boolean }) {
  if (compact) {
    return (
      <span
        className={`status-dot-compact status-${status}`}
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
