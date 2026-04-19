import './PlatformBadge.css';

/**
 * Human-facing labels + short aliases for known platforms. A platform
 * ID we don't recognise falls through to a generic badge using the ID
 * itself — new adapters just need to register in the backend, and the
 * frontend renders something sensible automatically.
 */
const META: Record<string, { label: string; short: string }> = {
  opencode: { label: 'OpenCode', short: 'OC' },
  'claude-code': { label: 'Claude Code', short: 'CC' },
};

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

export function PlatformBadge({ platform, variant }: PlatformBadgeProps) {
  const meta = META[platform];
  const label = meta?.label ?? platform;
  const short = meta?.short ?? platform.slice(0, 2).toUpperCase();
  const classes = ['platform-badge', `platform-${platform}`];
  if (variant) classes.push(variant);
  return (
    <span className={classes.join(' ')} title={label} aria-label={label}>
      {variant === undefined ? label : short}
    </span>
  );
}
