import './PlatformBadge.css';
import { useMultiPlatform } from '../lib/useCapabilities';

/**
 * Human-facing labels + short aliases for known platforms. A platform
 * ID we don't recognise falls through to a generic badge using the ID
 * itself — new adapters just need to register in the backend, and the
 * frontend renders something sensible automatically.
 */
const META: Record<string, { label: string; short: string }> = {
  opencode: { label: 'OpenCode', short: 'OC' },
};

/**
 * Strips the remote-routing prefix from a possibly-compound platform id
 * ("r-<remoteId>:<base>" -> "<base>"). A bare id is returned unchanged.
 * Pure string handling for display — not platform-identity branching.
 */
function basePlatform(platform: string): string {
  if (platform.startsWith('r-')) {
    const sep = platform.indexOf(':');
    if (sep >= 0) return platform.slice(sep + 1);
  }
  return platform;
}

interface PlatformBadgeProps {
  platform: string;
  /**
   * Visual treatment:
   *   - undefined (default): full pill with the platform's accent
   *     colour, rendered with the human label ("OpenCode"). Used in
   *     headers and other prominent spots.
   *   - 'compact': two-letter short code on the platform accent
   *     (background + border). A smaller version of the default pill.
   *   - 'plain': short code only. No background, no border, inherits
   *     the surrounding text colour. Designed to sit inline in the
   *     same monospace secondary row as a session id or project path.
   */
  variant?: 'compact' | 'plain';
}

/**
 * Renders a platform badge (pill or inline label). When only a single
 * platform is registered the badge is hidden — it adds no information
 * when every session comes from the same platform.
 */
export function PlatformBadge({ platform, variant }: PlatformBadgeProps) {
  const multi = useMultiPlatform();
  if (!multi) return null;

  // A remote session's platform is the compound "r-<remoteId>:<base>"
  // key (multi-remote support). Strip the prefix for display so the
  // badge shows the base platform ("OpenCode"), not the routing id.
  // This is opaque string handling, not behaviour branching.
  const base = basePlatform(platform);
  const meta = META[base];
  const label = meta?.label ?? base;
  const short = meta?.short ?? base.slice(0, 2).toUpperCase();
  const classes = ['platform-badge', `platform-${base}`];
  if (variant) classes.push(variant);
  return (
    <span className={classes.join(' ')} title={label} aria-label={label}>
      {variant === undefined ? label : short}
    </span>
  );
}
