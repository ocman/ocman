import { useState } from 'react';
import type { Component, Scalar, ValueHint } from '../../lib/a2ui';
import { formatValue } from '../../lib/a2uiCharts';
import { Button } from '../Control';
import { DataTable } from '../DataTable';
import styles from './A2UIResult.module.css';

/** Sorts on raw cell values; formatCell only changes what is displayed. */
export function ResultTable({ columns, rows, formatCell }: { columns: string[]; rows: Scalar[][]; formatCell?: (value: Scalar, column: number) => string }) {
  const [sort, setSort] = useState<{ column: number; descending: boolean } | null>(null);
  const ordered = sort ? [...rows].sort((a, b) => {
    const left = a[sort.column], right = b[sort.column];
    const comparison = typeof left === 'number' && typeof right === 'number'
      ? left - right : String(left ?? '').localeCompare(String(right ?? ''));
    return sort.descending ? -comparison : comparison;
  }) : rows;
  return <DataTable framed className={styles.table}>
    <thead><tr>{columns.map((column, i) => <th key={i} scope="col" aria-sort={sort?.column === i ? (sort.descending ? 'descending' : 'ascending') : 'none'}>
      <Button type="button" variant="ghost" size="compact" className={styles.sortButton}
        title={sort?.column === i && !sort.descending ? 'Sort descending' : 'Sort ascending'}
        onClick={() => setSort({ column: i, descending: sort?.column === i && !sort.descending })}>
        <span>{column}</span>
        <span className={styles.sortIndicator} aria-hidden="true">{sort?.column === i ? (sort.descending ? '↓' : '↑') : '↕'}</span>
      </Button>
    </th>)}</tr></thead>
    <tbody>{ordered.map((row, i) => <tr key={i}>{columns.map((_, j) => <td key={j}>{formatCell && row[j] != null ? formatCell(row[j], j) : String(row[j] ?? '')}</td>)}</tr>)}</tbody>
  </DataTable>;
}

export function ResultBarChart({ label, data, ...hint }: { label: string; data: { label: string; value: number }[] } & ValueHint) {
  const max = Math.max(1, ...data.map(item => item.value));
  return <figure className={styles.chart}>
    <figcaption>{label}</figcaption>
    <ul className={styles.bars}>{data.map((item, i) => <li key={i}>
      <span>{item.label}</span>
      <span className={styles.track} aria-hidden="true"><span className={styles.bar} style={{ width: `${item.value / max * 100}%` }} /></span>
      <span>{formatValue(item.value, hint)}</span>
    </li>)}</ul>
  </figure>;
}

const ARROWS = { up: '▲', down: '▼', flat: '–' } as const;

export function ResultStat({ stat }: { stat: Extract<Component, { component: 'Stat' }> }) {
  const { label, value, delta, deltaLabel, better = 'up', unit, format } = stat;
  const direction = !delta ? 'flat' : delta > 0 ? 'up' : 'down';
  const tone = direction === 'flat' || better === 'neither' ? undefined : direction === better ? 'better' : 'worse';
  return <figure className={styles.stat}>
    <figcaption>{label}</figcaption>
    <p className={styles.statValue}>{formatValue(value, { unit, format })}</p>
    {delta !== undefined && <p className={tone ? styles[tone] : styles.caption}>
      <span aria-hidden="true">{ARROWS[direction]} </span>
      {delta > 0 ? '+' : delta < 0 ? '-' : '±'}{formatValue(Math.abs(delta), { unit, format })}
      {deltaLabel && ` ${deltaLabel}`}
      {tone && <span className={styles.srOnly}> ({tone})</span>}
    </p>}
  </figure>;
}
