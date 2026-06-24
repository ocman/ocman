import type { Loop } from './api.types';

/** Human-readable trigger label, e.g. "every 30m", "PR #42". */
export function loopTriggerLabel(loop: Loop): string {
  switch (loop.triggerType) {
    case 'pr_event':
      return loop.triggerConfig?.pr_number ? `PR #${loop.triggerConfig.pr_number}` : 'PR events';
    case 'schedule':
      return loop.triggerConfig?.interval_seconds
        ? `every ${formatGoDuration(loop.triggerConfig.interval_seconds)}`
        : 'schedule';
    case 'child_complete':
      return 'on child complete';
    case 'turn_complete':
      return 'on turn complete';
    default:
      return loop.triggerType;
  }
}

/** "2/48 · $0.50/$5.00" budget summary (iterations, cost, error streak). */
export function loopBudgetLabel(loop: Loop): string {
  const parts = [`${loop.iteration}/${loop.stopConditions?.max_iterations ?? '?'}`];
  if (loop.stopConditions?.max_cost_usd) {
    parts.push(`$${loop.costUSD.toFixed(2)}/$${loop.stopConditions.max_cost_usd.toFixed(2)}`);
  }
  if (loop.errorStreak > 0) parts.push(`${loop.errorStreak} err`);
  return parts.join(' · ');
}

// MIN_SCHEDULE_INTERVAL_S mirrors the backend's 60s floor on schedule
// interval (internal/loops MinScheduleInterval) so the displayed next-run
// matches when the loop will actually fire.
export const MIN_SCHEDULE_INTERVAL_S = 60;

/** formatDurationMs renders a millisecond span as a short human string. */
export function formatDurationMs(ms: number): string {
  if (ms < 0) ms = 0;
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  const rem = s % 60;
  if (m < 60) return rem ? `${m}m ${rem}s` : `${m}m`;
  const h = Math.floor(m / 60);
  return `${h}h ${m % 60}m`;
}

/**
 * nextRunLabel returns the expected next-fire time for an active schedule
 * loop, or "" when not applicable (non-schedule, not active, no interval).
 * Never-fired or overdue loops read as "due now". Interval-based only —
 * the backend has no cron parser.
 */
export function nextRunLabel(loop: Loop): string {
  if (loop.triggerType !== 'schedule' || loop.state !== 'active') return '';
  const intervalS = Math.max(loop.triggerConfig?.interval_seconds ?? 0, MIN_SCHEDULE_INTERVAL_S);
  if (loop.lastFiredAt === 0) return 'due now';
  const nextAt = loop.lastFiredAt + intervalS * 1000;
  const deltaMs = nextAt - Date.now();
  if (deltaMs <= 0) return 'due now';
  return `in ${formatDurationMs(deltaMs)}`;
}

// Go duration units we accept on the interval input, in seconds. Sub-second
// units (ms/us/ns) are intentionally omitted — the schedule floor is 60s.
const DURATION_UNIT_SECONDS: Record<string, number> = {
  s: 1,
  m: 60,
  h: 3600,
  d: 86400, // not real Go syntax, but a convenient extension for schedules
};

/**
 * parseGoDuration parses a Go-style duration string ("30m", "1h30m",
 * "90s", "1.5h") into whole seconds. Returns null on empty/invalid input.
 * A bare number (no unit) is treated as seconds for backwards-compat with
 * the old raw-seconds input.
 */
export function parseGoDuration(input: string): number | null {
  // Strip all whitespace so "1h 30m" is accepted (Go itself disallows
  // spaces, but the input is more forgiving).
  const s = input.trim().toLowerCase().replace(/\s+/g, '');
  if (s === '') return null;
  // Bare number => seconds (legacy / forgiving).
  if (/^\d+(\.\d+)?$/.test(s)) return Math.round(Number(s));

  const re = /(\d+(?:\.\d+)?)(d|h|m|s)/g;
  let total = 0;
  let matched = false;
  let lastIndex = 0;
  let m: RegExpExecArray | null;
  while ((m = re.exec(s)) !== null) {
    // Reject stray characters between tokens (e.g. "1h x 2m").
    if (m.index !== lastIndex) return null;
    total += Number(m[1]) * DURATION_UNIT_SECONDS[m[2]];
    lastIndex = re.lastIndex;
    matched = true;
  }
  if (!matched || lastIndex !== s.length) return null;
  return Math.round(total);
}

/**
 * formatGoDuration renders whole seconds as a compact Go-style duration
 * ("90s" -> "1m30s", "3600" -> "1h"), the inverse of parseGoDuration for
 * display in the interval input.
 */
export function formatGoDuration(totalSeconds: number): string {
  if (totalSeconds <= 0) return '0s';
  let rem = Math.round(totalSeconds);
  const h = Math.floor(rem / 3600);
  rem %= 3600;
  const m = Math.floor(rem / 60);
  const s = rem % 60;
  let out = '';
  if (h) out += `${h}h`;
  if (m) out += `${m}m`;
  if (s || out === '') out += `${s}s`;
  return out;
}
