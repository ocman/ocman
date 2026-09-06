import { useState } from 'react';
import { Bar } from 'react-chartjs-2';
import { BAR_OPTIONS_STACKED } from '../../lib/chartConfig';
import { useMetrics } from '../../lib/queries';
import { AnalyticsFilters } from './AnalyticsFilters';
import { useDashboard } from './context';
import { ChartCard } from './shared';
import { MetricsSummaryCards } from './StatsLogTables';

export function OverviewTab() {
  const { dirScope } = useDashboard();
  const [days, setDays] = useState(30);
  const metricsQ = useMetrics({ days: days || undefined, dir: dirScope || undefined });
  const metrics = metricsQ.data;

  return (
    <div className="metrics-page">
      <AnalyticsFilters days={days} onDaysChange={setDays} />
      {metricsQ.error instanceof Error && <div className="oc-error-banner">{metricsQ.error.message}</div>}
      {metricsQ.isLoading && !metrics && <div className="oc-list-loading"><div className="oc-spinner" />Loading overview...</div>}
      {metrics && (
        <>
          <MetricsSummaryCards metrics={metrics} />
          <div className="metrics-chart-grid">
            <ChartCard title="Token Usage">
              <Bar data={{
                labels: metrics.series.map((point) => point.label),
                datasets: [
                  { label: 'Input', data: metrics.series.map((point) => point.inputTokens), backgroundColor: 'rgba(137, 180, 250, 0.72)', stack: 'tokens' },
                  { label: 'Cache Read', data: metrics.series.map((point) => point.cacheReadTokens), backgroundColor: 'rgba(148, 226, 213, 0.72)', stack: 'tokens' },
                  { label: 'Output', data: metrics.series.map((point) => point.outputTokens), backgroundColor: 'rgba(166, 227, 161, 0.72)', stack: 'tokens' },
                ],
              }} options={BAR_OPTIONS_STACKED} />
            </ChartCard>
          </div>
        </>
      )}
    </div>
  );
}
