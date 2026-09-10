import { useState } from 'react';
import { Bar } from 'react-chartjs-2';
import { BAR_OPTIONS_STACKED } from '../../lib/chartConfig';
import { formatNumber } from '../../lib/format';
import { useAnalyticsOverview, useMetrics } from '../../lib/queries';
import { AnalyticsFilters } from './AnalyticsFilters';
import { useDashboard } from './context';
import { ChartCard, ChartSkeletons, MetricCard, MetricCardsSkeleton } from './shared';
import { MetricsSummaryCards } from './StatsLogTables';

export function OverviewTab() {
  const { dirScope } = useDashboard();
  const [days, setDays] = useState(30);
  const metricsQ = useMetrics({ days: days || undefined, dir: dirScope || undefined });
  const overviewQ = useAnalyticsOverview();
  const metrics = metricsQ.data;
  const overview = overviewQ.data;
  const total = (counts: Record<string, number>) => Object.values(counts).reduce((sum, count) => sum + count, 0);
  const breakdown = (counts: Record<string, number>) => Object.entries(counts).map(([status, count]) => `${status}: ${count}`).join(' / ');

  return (
    <div className="metrics-page">
      <section aria-labelledby="inventory-heading">
        <h2 id="inventory-heading" className="analytics-section-heading">All-time inventory</h2>
        {overviewQ.error instanceof Error && <div className="oc-error-banner">{overviewQ.error.message}</div>}
        {overviewQ.isLoading && !overview && <MetricCardsSkeleton cards={7} label="Loading inventory" />}
        {overview && (
          <>
          <div className="analytics-scope-note">Inventory totals are scoped to this {overview.inventoryScope} ocman instance.</div>
          <div className="metrics-summary-grid">
            <MetricCard
              label="Sessions"
              value={formatNumber(overview.totalSessions - overview.subagentSessions)}
              tone="blue"
              subvalue={`${formatNumber(overview.subagentSessions)} subagent session${overview.subagentSessions === 1 ? '' : 's'}`}
            />
            <MetricCard label="Projects" value={formatNumber(overview.totalProjects)} tone="blue" />
            <MetricCard label="Routines" value={formatNumber(overview.totalRoutines)} tone="green" />
            <MetricCard label="Routine runs" value={formatNumber(total(overview.routineRunsByStatus))} tone="purple" subvalue={breakdown(overview.routineRunsByStatus)} />
            <MetricCard label="Factory epics" value={formatNumber(total(overview.factoryEpicsByStatus))} tone="orange" subvalue={breakdown(overview.factoryEpicsByStatus)} />
            <MetricCard label="Factory issues" value={formatNumber(total(overview.factoryIssuesByStatus))} tone="orange" subvalue={breakdown(overview.factoryIssuesByStatus)} />
            <MetricCard label="Factory attempts" value={formatNumber(total(overview.factoryAttemptsByPhase))} tone="purple" subvalue={breakdown(overview.factoryAttemptsByTerminalOutcome)} />
          </div>
          </>
        )}
      </section>
      <section aria-labelledby="activity-heading">
        <h2 id="activity-heading" className="analytics-section-heading">Request activity</h2>
        <AnalyticsFilters days={days} onDaysChange={setDays} />
        {metricsQ.error instanceof Error && <div className="oc-error-banner">{metricsQ.error.message}</div>}
        {metricsQ.isLoading && !metrics && <>
          <MetricCardsSkeleton cards={8} label="Loading request summary" />
          <ChartSkeletons labels={['Loading token usage']} />
        </>}
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
      </section>
    </div>
  );
}
