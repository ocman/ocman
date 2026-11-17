import type { MetricsSummary } from '../../lib/api';
import { formatCompactNumber, formatCost, formatNumber } from '../../lib/format';

/** Every token the model processed, including cache reads and writes. */
export function tokensProcessed(s: Pick<MetricsSummary, 'inputTokens' | 'outputTokens' | 'cacheReadTokens' | 'cacheWriteTokens'>): number {
  return s.inputTokens + s.outputTokens + s.cacheReadTokens + s.cacheWriteTokens;
}

/** "964 in · 303.3K out · 99.3M cache read · 1.9M cache write", omitting zero parts. */
export function tokenBreakdown(s: Pick<MetricsSummary, 'inputTokens' | 'outputTokens' | 'cacheReadTokens' | 'cacheWriteTokens'>): string {
  return [
    [s.inputTokens, 'in'], [s.outputTokens, 'out'], [s.cacheReadTokens, 'cache read'], [s.cacheWriteTokens, 'cache write'],
  ].filter(([count]) => Number(count) > 0).map(([count, label]) => `${formatCompactNumber(Number(count))} ${label}`).join(' · ') || 'No tokens';
}

/** "99.3M read · 1.9M written" for cache columns and details. */
export function cacheBreakdown(read: number, write: number): string {
  return `${formatCompactNumber(read)} read · ${formatCompactNumber(write)} written`;
}

export type CostBasis = 'billed' | 'estimate' | 'mixed' | 'none';

/**
 * How a headline cost was obtained. `effective` uses the billed amount per
 * request when the platform reported one, and the API-equivalent token
 * estimate otherwise (subscription plans report $0).
 */
export function costBasis(reported: number, effective: number): CostBasis {
  if (!(effective > 0)) return 'none';
  if (!(reported > 0)) return 'estimate';
  return effective - reported >= 0.005 ? 'mixed' : 'billed';
}

export function costNote(reported: number, effective: number): string {
  switch (costBasis(reported, effective)) {
    case 'estimate': return 'API-equivalent estimate · $0 billed';
    case 'mixed': return `${formatCost(reported)} billed · rest API-equivalent`;
    case 'billed': return 'Billed by the provider';
    default: return 'No cost recorded';
  }
}

export const COST_HELP = 'Billed cost where the provider reports one. Subscription plans report $0, so those requests are priced at public API rates (API-equivalent) instead.';

/** "1 of 449 failed" rather than a bare error count. */
export function errorDetail(errors: number, completed: number): string {
  return errors > 0 ? `${formatNumber(errors)} of ${formatNumber(completed)} failed` : `None of ${formatNumber(completed)} failed`;
}
