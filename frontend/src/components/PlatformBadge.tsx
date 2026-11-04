import './PlatformBadge.css';
import { basePlatform } from '../lib/modelRef';
import { useMultiPlatform } from '../lib/useCapabilities';
import { Tooltip } from './Tooltip';

/**
 * Human-facing labels + short aliases for known platforms. A platform
 * ID we don't recognise falls through to a generic badge using the ID
 * itself — new adapters just need to register in the backend, and the
 * frontend renders something sensible automatically.
 */
/* eslint-disable no-restricted-syntax -- icons are bundled frontend assets keyed by platform id */
const META: Record<string, { label: string; short: string; icon: string }> = {
  opencode: { label: 'OpenCode', short: 'OC', icon: '/platform-opencode.svg' },
  claudecode: { label: 'Claude Code', short: 'CC', icon: '/platform-claudecode.svg' },
  codex: { label: 'Codex', short: 'CX', icon: '/platform-codex.svg' },
};
/* eslint-enable no-restricted-syntax */

export function PlatformLogo({ platform }: { platform: string }) {
  const meta = META[basePlatform(platform)];
  return meta ? <img src={meta.icon} width={16} height={16} alt="" /> : null;
}

interface PlatformBadgeProps {
  platform: string;
  alwaysShow?: boolean;
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
  variant?: 'compact' | 'plain' | 'icon';
}

/**
 * Renders a platform badge (pill or inline label). When only a single
 * platform is registered the badge is hidden — it adds no information
 * when every session comes from the same platform.
 */
export function PlatformBadge({ platform, variant, alwaysShow }: PlatformBadgeProps) {
  const multi = useMultiPlatform();
  if (!multi && !alwaysShow) return null;

  // A remote session's platform is the compound "r-<remoteId>:<base>"
  // key (multi-remote support). Strip the prefix for display so the
  // badge shows the base platform ("OpenCode"), not the routing id.
  // This is opaque string handling, not behaviour branching.
  const base = basePlatform(platform);
  const meta = META[base];
  const label = meta?.label ?? base;
  const short = meta?.short ?? base.slice(0, 2).toUpperCase();
  const classes = ['platform-badge', `platform-${base}`];
  // A routed id means another machine owns the session; say so up front.
  const remote = base !== platform;
  if (variant === 'icon') return (
    <Tooltip content={`This session runs on ${label}${remote ? ' on a remote machine' : ''}.`}>
      <span className="platform-icon" tabIndex={0} aria-label={remote ? `${label} (remote machine)` : label}>
        <span className="platform-icon-separator" aria-hidden="true"> · </span>
        {meta ? <img src={meta.icon} width={16} height={16} alt="" /> : short}
      </span>
    </Tooltip>
  );
  if (variant) classes.push(variant);
  return (
    <span className={classes.join(' ')} title={label} aria-label={label}>
      {variant === undefined ? label : short}
    </span>
  );
}
