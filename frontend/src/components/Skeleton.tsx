import './Skeleton.css';
import { DataTable } from './DataTable';

// ---------------------------------------------------------------------------
// Skeleton — base shimmer block.
//
// Use via SkeletonTable / SkeletonLines below, or directly:
//   <Skeleton className="oc-skeleton-line" style={{ width: '60%' }} />
// ---------------------------------------------------------------------------

interface SkeletonProps {
  className?: string;
  style?: React.CSSProperties;
  'aria-hidden'?: boolean;
}

export function Skeleton({ className = '', style, 'aria-hidden': ariaHidden = true }: SkeletonProps) {
  return (
    <span
      className={`oc-skeleton ${className}`.trim()}
      style={style}
      aria-hidden={ariaHidden}
    />
  );
}

// Stable, varied widths so placeholder rows don't look uniform.
const WIDTHS = [55, 75, 40, 65, 35, 70, 50, 60];

const lineWidth = (i: number) => `${WIDTHS[i % WIDTHS.length]}%`;

/** Placeholder table with the real table's column headers. */
export function SkeletonTable({ columns, rows = 5, label }: { columns: string[]; rows?: number; label: string }) {
  return (
    <DataTable aria-busy="true" aria-label={label}>
      <thead>
        <tr>{columns.map((c, i) => <th key={i}>{c}</th>)}</tr>
      </thead>
      <tbody className="oc-skeleton-tbody">
        {Array.from({ length: rows }, (_, r) => (
          <tr key={r}>
            {columns.map((c, i) => (
              <td key={i}>{c && <Skeleton className="oc-skeleton-line" style={{ width: lineWidth(r + i) }} />}</td>
            ))}
          </tr>
        ))}
      </tbody>
    </DataTable>
  );
}

/** Vertical stack of placeholder text lines for sidebars, threads and panes. */
export function SkeletonLines({ rows = 6, label, testId }: { rows?: number; label: string; testId?: string }) {
  return (
    <div className="oc-skeleton-lines" aria-busy="true" aria-label={label} data-testid={testId}>
      {Array.from({ length: rows }, (_, i) => (
        <Skeleton key={i} className="oc-skeleton-line" style={{ width: lineWidth(i) }} />
      ))}
    </div>
  );
}
