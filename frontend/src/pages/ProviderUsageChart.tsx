import { Line } from 'react-chartjs-2';
import './dashboard/chartSetup';
import './SubscriptionUsage.css';
import { CHART_COLORS, LINE_INTERACTION } from '../lib/chartConfig';
import type { SubscriptionHistorySeries } from '../lib/subscriptionHistorySeries';

export function ProviderUsageChart({ name, series }: {
  name: string;
  series: SubscriptionHistorySeries[];
}) {
  return <div className="subscription-history-chart">
    <Line role="img" aria-label={`${name} subscription usage history`} data={{
      datasets: series.map((item, index) => ({
        label: item.label, data: item.data, borderColor: CHART_COLORS[index % CHART_COLORS.length],
        pointRadius: 2, spanGaps: false,
      })),
    }} options={{
      interaction: LINE_INTERACTION,
      scales: {
        x: { type: 'linear', grid: { display: false }, ticks: { maxTicksLimit: 5, callback: value => new Date(Number(value)).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }) } },
        y: { min: 0, max: 100, ticks: { callback: value => `${value}%` } },
      },
      plugins: { legend: { position: 'bottom' }, tooltip: { callbacks: {
        title: items => items.length ? new Date(Number(items[0].parsed.x)).toLocaleString() : '',
        label: item => `${item.dataset.label}: ${item.parsed.y}% used`,
      } } },
    }} />
  </div>;
}
