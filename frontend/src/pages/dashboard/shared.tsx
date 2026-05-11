/**
 * Shared sub-components and constants used by multiple Dashboard tab components.
 */
import type { ReactNode } from 'react';

// ---------------------------------------------------------------------------
// MetricCard
// ---------------------------------------------------------------------------

export function MetricCard({ label, value, subvalue, tone }: { label: string; value: string; subvalue?: string; tone: 'blue' | 'green' | 'purple' | 'orange' }) {
  return (
    <div className="stat-card">
      <div className="label">{label}</div>
      <div className={`value ${tone}`}>{value}</div>
      {subvalue ? <div className="metrics-subvalue">{subvalue}</div> : null}
    </div>
  );
}

// ---------------------------------------------------------------------------
// ChartCard
// ---------------------------------------------------------------------------

export function ChartCard({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="chart-card metrics-chart-card">
      <h3>{title}</h3>
      <div className="metrics-chart-body">{children}</div>
    </div>
  );
}


