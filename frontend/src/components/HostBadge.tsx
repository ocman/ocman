import './HostBadge.css';
import { useMultiHost } from '../lib/useCapabilities';

interface HostBadgeProps {
  /** Display label for the owning machine (Session.remoteName). */
  remoteName?: string;
  /** Owning machine id; 'local' is the hub's own machine (Session.remoteId). */
  remoteId?: string;
  /** True when the session is last-known data from an offline remote. */
  stale?: boolean;
}

/**
 * Renders a host badge showing which machine owns a session
 * (multi-remote support, AD-7). Hidden on single-host installs where it
 * adds no information. Display-only: it never branches behaviour on the
 * host identity, it just shows the server-provided label.
 */
export function HostBadge({ remoteName, remoteId, stale }: HostBadgeProps) {
  const multi = useMultiHost();
  // Only prefix remote-owned sessions; the local machine needs no label.
  if (!multi || !remoteName || remoteId === 'local') return null;
  const classes = ['host-badge'];
  if (stale) classes.push('stale');
  const title = stale ? `${remoteName} (offline — last known)` : remoteName;
  return (
    <span className={classes.join(' ')} title={title} aria-label={title}>
      {remoteName}
      {stale ? ' (offline)' : ''}
    </span>
  );
}
