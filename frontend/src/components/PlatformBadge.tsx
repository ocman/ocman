import './PlatformBadge.css';
import { basePlatform } from '../lib/modelRef';
import { useCapabilities, useMultiPlatform } from '../lib/useCapabilities';
import { Tooltip } from './Tooltip';

/**
 * Bundled labels, short aliases and icons for native tools. Named
 * connections resolve their labels from owner-qualified capability metadata
 * instead of displaying internal routing keys.
 */
/* eslint-disable no-restricted-syntax -- icons are bundled frontend assets keyed by platform id */
const META: Record<string, { label: string; short: string; icon: string }> = {
  opencode: { label: 'OpenCode', short: 'OC', icon: '/platform-opencode.svg' },
  claudecode: { label: 'Claude Code', short: 'CC', icon: '/platform-claudecode.svg' },
  codex: { label: 'Codex', short: 'CX', icon: '/platform-codex.svg' },
};
/* eslint-enable no-restricted-syntax */

export function PlatformLogo({ platform }: { platform: string }) {
  if (basePlatform(platform).startsWith('cnx_')) return <NamedPlatformLogo platform={platform} />;
  const meta = META[basePlatform(platform)];
  return meta ? <img src={meta.icon} width={16} height={16} alt="" /> : null;
}

function NamedPlatformLogo({ platform }: { platform: string }) {
  const entry = useCapabilities()?.platforms.find(entry => entry.id === platform);
  const meta = META[entry?.kind ?? ''];
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
  if (basePlatform(platform).startsWith('cnx_')) return <NamedPlatformBadge platform={platform} variant={variant} />;
  const base = basePlatform(platform);
  return <PlatformBadgeLabel platform={platform} kind={base} label={META[base]?.label ?? base} variant={variant} />;
}

function NamedPlatformBadge({ platform, variant }: PlatformBadgeProps) {
  // Match the full owner-qualified key. A same-named account on another
  // machine must never supply this session's label or native tool icon.
  const entry = useCapabilities()?.platforms.find(entry => entry.id === platform);
  return <PlatformBadgeLabel platform={platform} kind={entry?.kind ?? 'unknown'} label={entry?.displayName || 'Platform'} variant={variant} />;
}

function PlatformBadgeLabel({ platform, kind, label, variant }: PlatformBadgeProps & { kind: string; label: string }) {
  const meta = META[kind];
  const short = meta?.short ?? label.slice(0, 2).toUpperCase();
  const classes = ['platform-badge', `platform-${kind}`];
  // A routed id means another machine owns the session; say so up front.
  const remote = basePlatform(platform) !== platform;
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
