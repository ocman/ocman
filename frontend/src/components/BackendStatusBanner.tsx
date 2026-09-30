import * as Toast from '@radix-ui/react-toast';
import { api } from '../lib/api';
import { useBackendStatus } from '../lib/backendStatus';
import './PromptToastNotify.css';

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
 * Persistent app-root toast shown while the backend is unreachable. It
 * cannot be dismissed; it closes itself once the backend answers again.
 */
export function BackendStatusBanner({ force = false, onRetry = probe }: Props) {
  const unreachable = useBackendStatus((s) => s.unreachable);
  const error = useBackendStatus((s) => s.error);
  if (!force && !unreachable) return null;
  return (
    <Toast.Provider swipeDirection="right" duration={Infinity}>
      <Toast.Root className="oc-prompt-toast" data-kind="error" data-testid="backend-status-banner" open duration={Infinity}>
        <Toast.Title className="oc-prompt-toast-heading">Backend unreachable</Toast.Title>
        <Toast.Description className="oc-prompt-toast-body">
          Backend is not responding.
          {error && !error.startsWith('Backend is not responding') && <span> ({error})</span>}
        </Toast.Description>
        <div className="oc-prompt-toast-actions">
          <Toast.Action asChild altText="Retry connecting to the backend" onClick={(e) => { e.preventDefault(); onRetry(); }}>
            <button type="button" className="oc-prompt-toast-open">Retry</button>
          </Toast.Action>
        </div>
      </Toast.Root>
      <Toast.Viewport className="oc-prompt-toast-viewport" />
    </Toast.Provider>
  );
}
