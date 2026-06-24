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

export function SessionWarningBanner({ warning, onDismiss }: SessionWarningBannerProps) {
  const ports = warning.ports?.filter(Boolean) ?? [];

  return (
    <div className="oc-session-warning-banner" role="status" data-testid={`session-warning-${warning.kind}`}>
      <i className="bi bi-exclamation-triangle" aria-hidden="true" />
      <span className="oc-session-warning-body">
        <strong>{warningTitle(warning.kind)}</strong>
        {' — '}
        {warning.message}
        {ports.length > 0 && (
          <span className="oc-session-warning-detail">
            {' · '}ports {ports.join(', ')}
          </span>
        )}
      </span>
      <button
        type="button"
        className="oc-session-warning-dismiss"
        aria-label="Dismiss session warning"
        onClick={onDismiss}
      >
        <i className="bi bi-x-lg" aria-hidden="true" />
      </button>
    </div>
  );
}
