import { api } from '../lib/api';
import { useBackendStatus } from '../lib/backendStatus';

// Probe that clears the flag on success (apiFetch does the clearing).
function probe() {
  void api.authMe().catch(() => {});
}

type Props = {
  /** Render regardless of the store (AuthGate's boot timeout). */
  force?: boolean;
  onRetry?: () => void;
};

/**
 * Persistent (not a toast) app-root banner shown while the backend is
 * unreachable. Renders nothing when the backend is healthy.
 */
export function BackendStatusBanner({ force = false, onRetry = probe }: Props) {
  const unreachable = useBackendStatus((s) => s.unreachable);
  const error = useBackendStatus((s) => s.error);
  if (!force && !unreachable) return null;
  return (
    <div className="oc-error-banner" role="alert" data-testid="backend-status-banner">
      Backend is not responding.
      {error && !error.startsWith('Backend is not responding') && <span> ({error})</span>}
      <button type="button" onClick={onRetry}>Retry</button>
    </div>
  );
}
