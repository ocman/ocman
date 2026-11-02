import './HostBadge.css';
import { useMultiHost } from '../lib/useCapabilities';

interface HostBadgeProps {
  /** Display label for the owning machine (Session.remoteName). */
  remoteName?: string;
  /** Owning machine id; 'local' is the hub's own machine (Session.remoteId). */
  remoteId?: string;
  /** True when the session is last-known data from an offline remote. */
  stale?: boolean;
  /** Project headers show the owner even on a single-host install. */
  alwaysShow?: boolean;
  /** Replace the status dot with a machine icon. */
  showIcon?: boolean;
  className?: string;
}

/**
 * Renders a host badge showing which machine owns a session
 * (multi-remote support, AD-7). Hidden on single-host installs where it
 * adds no information. Display-only: it never branches behaviour on the
 * host identity, it just shows the server-provided label.
 */
export function HostBadge({ remoteName, remoteId, stale, alwaysShow = false, showIcon = false, className = '' }: HostBadgeProps) {
  const multi = useMultiHost();
  // Session rows omit the local owner; project headers always show it.
  // An offline remote may arrive without a name — still flag it as remote.
  if (!alwaysShow && (!multi || !remoteId || remoteId === 'local')) return null;
  const label = remoteName || 'Remote';
  const classes = ['host-badge', className];
  if (showIcon) classes.push('host-badge--icon');
  if (stale) classes.push('stale');
  const title = stale ? `${label} (offline — last known)` : label;
  return (
    <span className={classes.join(' ').trim()} title={title} aria-label={title}>
      {showIcon && <i className="bi bi-hdd-network" aria-hidden="true" />}
      {showIcon ? <span className="host-badge-label">{label}{stale ? ' (offline)' : ''}</span>
        : <>{label}{stale ? ' (offline)' : ''}</>}
    </span>
  );
}
