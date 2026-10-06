import { Button } from '../../components/Control';

export interface SessionSyncIndicatorProps {
  refreshing: boolean;
  refreshError: string | null;
  onRetry: () => void;
}

/**
 * Footer note for a conversation that may be stale: "checking" while a
 * cached render waits for its refresh, and the failure (with a retry) when a
 * refresh did not land. Renders nothing once the view is current.
 */
export function SessionSyncIndicator({ refreshing, refreshError, onRetry }: SessionSyncIndicatorProps) {
  if (refreshing) {
    return (
      <div className="oc-sse-indicator oc-sse-indicator-reconnecting" role="status" data-testid="session-syncing">
        <span className="oc-sse-indicator-dot" aria-hidden="true" />
        <span title="Checking for updates…">Checking for updates…</span>
      </div>
    );
  }
  if (!refreshError) return null;
  const message = `Couldn't refresh, this conversation may be out of date (${refreshError})`;
  return (
    <div className="oc-sse-indicator oc-sse-indicator-reconnecting oc-sync-indicator-failed" role="alert" data-testid="session-sync-failed">
      <span title={message}>{message}</span>
      <Button variant="link" size="compact" onClick={onRetry}>Retry</Button>
    </div>
  );
}
