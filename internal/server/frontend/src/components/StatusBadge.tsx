const labels: Record<string, string> = { waiting: 'Waiting', busy: 'Busy', done: 'Done' };

export function StatusBadge({ status }: { status: string }) {
  return (
    <span className={`status-indicator status-${status}`}>
      <span className="status-dot" />
      {labels[status] || status}
    </span>
  );
}
