/**
 * Shared sub-components and constants used by multiple Dashboard tab components.
 */
import type { CSSProperties, ReactNode } from 'react';
import { Skeleton } from '../../components/Skeleton';

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

export function ChartCard({ title, children, style }: { title: string; children: ReactNode; style?: CSSProperties }) {
  return (
    <div className="chart-card metrics-chart-card" style={style}>
      <h3>{title}</h3>
      <div className="metrics-chart-body">{children}</div>
    </div>
  );
}

export function ChartSkeletons({ cards = 2 }: { cards?: number }) {
  return (
    <div className="metrics-chart-grid" role="status" aria-label="Loading charts">
      {Array.from({ length: cards }, (_, index) => (
        <div key={index} className="chart-card metrics-chart-card">
          <Skeleton className="oc-skeleton-line-lg" style={{ width: `${35 + index * 8}%`, marginBottom: 20 }} />
          <Skeleton style={{ width: '100%', height: 260 }} />
        </div>
      ))}
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
