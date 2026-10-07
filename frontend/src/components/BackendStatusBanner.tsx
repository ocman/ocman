import * as Toast from '@radix-ui/react-toast';
import { api } from '../lib/api';
import { useBackendStatus } from '../lib/backendStatus';
import { Button } from './Control';
import { PromptToast, PromptToastHeading, PromptToastBody, PromptToastActions, PromptToastViewport } from './PromptToast';

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
      <PromptToast data-kind="error" data-testid="backend-status-banner" open duration={Infinity}>
        <PromptToastHeading>Backend unreachable</PromptToastHeading>
        <PromptToastBody>
          Backend is not responding.
          {error && !error.startsWith('Backend is not responding') && <span> ({error})</span>}
        </PromptToastBody>
        <PromptToastActions label="Backend connection actions">
          <Toast.Action asChild altText="Retry connecting to the backend" onClick={(e) => { e.preventDefault(); onRetry(); }}>
            <Button type="button" size="small" variant="accent">Retry</Button>
          </Toast.Action>
        </PromptToastActions>
      </PromptToast>
      <PromptToastViewport />
    </Toast.Provider>
  );
}
