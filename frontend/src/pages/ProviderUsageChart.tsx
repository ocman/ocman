import { useState } from 'react';
import { Line } from 'react-chartjs-2';
import './dashboard/chartSetup';
import './SubscriptionUsage.css';
import { CHART_COLORS, LINE_INTERACTION } from '../lib/chartConfig';
import type { SubscriptionHistorySeries } from '../lib/subscriptionHistorySeries';

export function ProviderUsageChart({ name, series, days = 7, updatedAt }: {
  name: string;
  series: SubscriptionHistorySeries[];
  days?: number;
  updatedAt?: number;
}) {
  const [mountedAt] = useState(Date.now);
  const now = updatedAt || mountedAt;
  return <div className="subscription-history-chart">
    <Line role="img" aria-label={`${name} subscription usage history`} data={{
      datasets: series.map((item, index) => ({
        label: item.label, data: item.data, borderColor: CHART_COLORS[index % CHART_COLORS.length],
        pointRadius: 0, spanGaps: false,
      })),
    }} options={{
      interaction: LINE_INTERACTION,
      scales: {
        x: { type: 'linear', bounds: 'data', min: days > 0 ? now - days * 86400000 : undefined, max: now, grid: { display: false }, ticks: { maxTicksLimit: 5, callback: value => new Date(Number(value)).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }) } },
        y: { min: 0, max: 100, ticks: { callback: value => `${value}%` } },
      },
      plugins: { legend: { position: 'bottom' }, tooltip: { callbacks: {
        title: items => items.length ? new Date(Number(items[0].parsed.x)).toLocaleString() : '',
        label: item => `${item.dataset.label}: ${item.parsed.y}% used`,
      } } },
    }} />
  </div>;
}
