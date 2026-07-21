import * as Toast from '@radix-ui/react-toast';
import type { SessionWarning } from '../lib/api';

interface SessionWarningBannerProps {
  warning: SessionWarning;
  onDismiss: () => void;
}

function warningTitle(kind: string): string {
  if (kind === 'duplicate_opencode_servers') {
    return 'Multiple OpenCode servers';
  }
  return 'Session warning';
}

// Session warnings surface as auto-hiding toasts in the shared toast
// viewport (bottom-right) rather than banners over the thread. Closing
// (auto-hide, swipe, or the dismiss button) calls onDismiss, which the
// parent persists so the same warning doesn't re-surface.
export function SessionWarningBanner({ warning, onDismiss }: SessionWarningBannerProps) {
  const ports = warning.ports?.filter(Boolean) ?? [];

  return (
    <Toast.Root
      className="oc-toast-root warning"
      open
      onOpenChange={(open) => { if (!open) onDismiss(); }}
      duration={10000}
    >
      <Toast.Description className="oc-toast-description" data-testid={`session-warning-${warning.kind}`}>
        <i className="bi bi-exclamation-triangle" aria-hidden="true" />
        {' '}
        <strong>{warningTitle(warning.kind)}</strong>
        {' — '}
        {warning.message}
        {ports.length > 0 && (
          <span className="oc-session-warning-detail">
            {' · '}ports {ports.join(', ')}
          </span>
        )}
      </Toast.Description>
      <Toast.Close className="oc-toast-close" aria-label="Dismiss session warning">
        <i className="bi bi-x-lg" aria-hidden="true" />
      </Toast.Close>
    </Toast.Root>
  );
}
