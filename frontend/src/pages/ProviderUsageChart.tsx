import { useId, useMemo, useState } from 'react';
import { Line } from 'react-chartjs-2';
import './dashboard/chartSetup';
import './SubscriptionUsage.css';
import { Button, ButtonGroup } from '../components/Control';
import { DataTable } from '../components/DataTable';
import { CHART_COLORS, LINE_INTERACTION } from '../lib/chartConfig';
import { formatFullDateTime } from '../lib/format';
import type { SubscriptionHistorySeries } from '../lib/subscriptionHistorySeries';

const ROWS_PER_PAGE = 25;

function ProviderUsageChartData({ name, series, min, max }: {
  name: string;
  series: SubscriptionHistorySeries[];
  min?: number;
  max: number;
}) {
  const [page, setPage] = useState(0);
  const rows = useMemo(() => series.flatMap(({ key, label, data }) => data
    .filter(({ x, lineBreak }) => !lineBreak && (min === undefined || x >= min) && x <= max)
    .map(({ x, y }) => ({ key, label, x, y })))
    .sort((a, b) => b.x - a.x), [series, min, max]);
  if (!rows.length) return <p>No readings in this period.</p>;

  const lastPage = Math.ceil(rows.length / ROWS_PER_PAGE) - 1;
  const currentPage = Math.min(page, lastPage);
  const start = currentPage * ROWS_PER_PAGE;
  const end = Math.min(start + ROWS_PER_PAGE, rows.length);
  return <>
    {lastPage > 0 && <ButtonGroup className="subscription-history-pagination" label="Chart data pages">
      <Button type="button" size="small" aria-disabled={currentPage === 0}
        onClick={() => { if (currentPage > 0) setPage(currentPage - 1); }}>Newer readings</Button>
      <Button type="button" size="small" aria-disabled={currentPage === lastPage}
        onClick={() => { if (currentPage < lastPage) setPage(currentPage + 1); }}>Older readings</Button>
      <span role="status">Rows {start + 1}–{end} of {rows.length}</span>
    </ButtonGroup>}
    <DataTable className="subscription-history-table" framed aria-label={`${name} data`}>
      <thead><tr><th scope="col">Timestamp</th><th scope="col">Quota window or budget</th><th scope="col">Used</th></tr></thead>
      <tbody>{rows.slice(start, end).map(({ key, label, x, y }) => <tr key={`${key}:${x}`}>
        <td><time dateTime={new Date(x).toISOString()}>{formatFullDateTime(x)}</time></td>
        <td>{label}</td>
        <td>{y === null ? 'No reading' : `${y}%`}</td>
      </tr>)}</tbody>
    </DataTable>
  </>;
}

export function ProviderUsageChart({ name, series, days = 7, updatedAt }: {
  name: string;
  series: SubscriptionHistorySeries[];
  days?: number;
  updatedAt?: number;
}) {
  const [mountedAt] = useState(Date.now);
  const [showData, setShowData] = useState(false);
  const dataId = useId();
  const now = updatedAt || mountedAt;
  const min = days > 0 ? now - days * 86400000 : undefined;
  const chartName = `${name} subscription usage history`;
  return <div>
    <div className="subscription-history-chart">
      <Line role="img" aria-label={chartName} data={{
        datasets: series.map((item, index) => ({
          label: item.label, data: item.data, borderColor: CHART_COLORS[index % CHART_COLORS.length],
          pointRadius: 0, spanGaps: false,
        })),
      }} options={{
        interaction: LINE_INTERACTION,
        scales: {
          x: { type: 'linear', bounds: 'data', min, max: now, grid: { display: false }, ticks: { maxTicksLimit: 5, callback: value => new Date(Number(value)).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }) } },
          y: { min: 0, max: 100, ticks: { callback: value => `${Math.round(Number(value))}%` } },
        },
        plugins: { legend: { position: 'bottom' }, tooltip: { callbacks: {
          title: items => items.length ? new Date(Number(items[0].parsed.x)).toLocaleString() : '',
          label: item => `${item.dataset.label}: ${Math.round(Number(item.parsed.y))}% used`,
        } } },
      }} />
    </div>
    <Button type="button" size="small" aria-expanded={showData} aria-controls={dataId}
      onClick={() => setShowData(open => !open)}>View chart data</Button>
    <div id={dataId} className="subscription-history-data" hidden={!showData}>
      {showData && <ProviderUsageChartData key={days} name={chartName} series={series} min={min} max={now} />}
    </div>
  </div>;
}
