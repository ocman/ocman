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

// ---------------------------------------------------------------------------
// MetricsPagination
// ---------------------------------------------------------------------------

/**
 * Prev / "Page N / M" / Next footer for the Stats log tables. Renders
 * nothing when everything fits on one page. `page` is 0-based.
 */
export function MetricsPagination({
  page,
  pageSize,
  total,
  onChange,
}: {
  page: number;
  pageSize: number;
  total: number;
  onChange: (page: number) => void;
}) {
  if (total <= pageSize) return null;
  const lastPage = Math.ceil(total / pageSize);
  return (
    <div className="metrics-pagination">
      <button
        className="oc-time-range-btn"
        disabled={page === 0}
        onClick={() => onChange(page - 1)}
      >Prev</button>
      <span className="metrics-pagination-info">
        Page {page + 1} / {lastPage}
      </span>
      <button
        className="oc-time-range-btn"
        disabled={(page + 1) * pageSize >= total}
        onClick={() => onChange(page + 1)}
      >Next</button>
    </div>
  );
}


