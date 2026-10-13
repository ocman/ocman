import type { ReactNode } from 'react';
import { formatCurrency } from '../lib/format';

export const API_EQUIVALENT_TITLE = 'API-equivalent cost: the platform reported $0 (subscription), so the tokens are priced at public API rates';

/**
 * A cost figure. When the platform reported nothing (subscription plans,
 * Claude Code), the figure is the token estimate and is labelled
 * API-equivalent rather than passed off as a bill.
 */
export function ApiEquivalentCost({ reported, effective, fallback = null }: { reported: number; effective: number; fallback?: ReactNode }) {
  if (!(effective > 0)) return <>{fallback}</>;
  if (reported > 0) return <span title="Platform-reported (billed)">{formatCurrency(effective)}</span>;
  return (
    <span title={API_EQUIVALENT_TITLE}>
      {formatCurrency(effective)} <span style={{ color: 'var(--text-dim)', fontSize: '0.85em' }}>API-equivalent</span>
    </span>
  );
}
