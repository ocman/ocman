import { useState } from 'react';
import type { Scalar } from '../../lib/a2ui';
import { Button } from '../Control';
import { DataTable } from '../DataTable';
import styles from './A2UIResult.module.css';

export function ResultTable({ columns, rows }: { columns: string[]; rows: Scalar[][] }) {
  const [sort, setSort] = useState<{ column: number; descending: boolean } | null>(null);
  const ordered = sort ? [...rows].sort((a, b) => {
    const left = a[sort.column], right = b[sort.column];
    const comparison = typeof left === 'number' && typeof right === 'number'
      ? left - right : String(left ?? '').localeCompare(String(right ?? ''));
    return sort.descending ? -comparison : comparison;
  }) : rows;
  return <DataTable framed>
    <thead><tr>{columns.map((column, i) => <th key={i} scope="col" aria-sort={sort?.column === i ? (sort.descending ? 'descending' : 'ascending') : 'none'}>
      <Button type="button" variant="ghost" size="compact" onClick={() => setSort({ column: i, descending: sort?.column === i && !sort.descending })}>{column}</Button>
    </th>)}</tr></thead>
    <tbody>{ordered.map((row, i) => <tr key={i}>{columns.map((_, j) => <td key={j}>{String(row[j] ?? '')}</td>)}</tr>)}</tbody>
  </DataTable>;
}

export function ResultBarChart({ label, data }: { label: string; data: { label: string; value: number }[] }) {
  const max = Math.max(1, ...data.map(item => item.value));
  return <figure className={styles.chart}>
    <figcaption>{label}</figcaption>
    <ul>{data.map((item, i) => <li key={i}>
      <span>{item.label}</span>
      <span className={styles.track} aria-hidden="true"><span className={styles.bar} style={{ width: `${item.value / max * 100}%` }} /></span>
      <span>{item.value.toLocaleString()}</span>
    </li>)}</ul>
  </figure>;
}
