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
  // An offline remote may arrive without a name — still flag it as remote.
  if (!multi || !remoteId || remoteId === 'local') return null;
  const label = remoteName || 'Remote';
  const classes = ['host-badge'];
  if (stale) classes.push('stale');
  const title = stale ? `${label} (offline — last known)` : label;
  return (
    <span className={classes.join(' ')} title={title} aria-label={title}>
      {label}
      {stale ? ' (offline)' : ''}
    </span>
  );
}
