import { useState } from 'react';
import { Bar, Line } from 'react-chartjs-2';
import type { DatabaseSizeSample } from '../../lib/api';
import { BAR_OPTIONS_STACKED, LINE_OPTIONS_DATABASE_SIZE } from '../../lib/chartConfig';
import { formatNumber } from '../../lib/format';
import { useAnalyticsOverview, useDatabaseSizes, useMetrics } from '../../lib/queries';
import { AnalyticsFilters } from './AnalyticsFilters';
import { useDashboard } from './context';
import { ChartCard, ChartSkeleton, ChartSkeletons, MetricCard, MetricCardsSkeleton } from './shared';
import { MetricsSummaryCards } from './StatsLogTables';

const formatDatabaseSizeTime = (timestamp: number) => new Date(timestamp).toLocaleString('en-US', {
  year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false,
});

function DatabaseSizeChart({ samples, isLoading }: { samples?: DatabaseSizeSample[]; isLoading: boolean }) {
  const timestamps = [...new Set(samples?.map((sample) => sample.sampledAt) ?? [])];
  const sizeByKey = new Map(samples?.map((sample) => [`${sample.database}:${sample.sampledAt}`, sample.sizeBytes / 1024 / 1024]) ?? []);
  const opencodeSizes = timestamps.map((timestamp) => sizeByKey.get(`opencode:${timestamp}`) ?? null);
  const ocmanSizes = timestamps.map((timestamp) => sizeByKey.get(`ocman:${timestamp}`) ?? null);
  return <>
    <div className="metrics-chart-grid">
      {isLoading && !samples ? <ChartSkeleton label="Loading database sizes" /> : (
        <ChartCard title="Database Size (log scale)">
          {timestamps.length ? <Line data={{
            labels: timestamps.map(formatDatabaseSizeTime),
            datasets: [
              { label: 'OpenCode (MiB)', data: opencodeSizes, borderColor: '#89b4fa', backgroundColor: 'rgba(137, 180, 250, 0.15)', tension: 0.25, pointRadius: 1 },
              { label: 'ocman (MiB)', data: ocmanSizes, borderColor: '#a6e3a1', backgroundColor: 'rgba(166, 227, 161, 0.15)', tension: 0.25, pointRadius: 1 },
            ],
          }} options={LINE_OPTIONS_DATABASE_SIZE} /> : <p className="oc-empty">No database size samples yet.</p>}
        </ChartCard>
      )}
    </div>
    <div className="analytics-scope-note">Database sizes cover this local instance and are not project-scoped.</div>
  </>;
}

export function OverviewTab() {
  const { dirScope } = useDashboard();
  const [days, setDays] = useState(30);
  const metricsQ = useMetrics({ days: days || undefined, dir: dirScope || undefined });
  const overviewQ = useAnalyticsOverview();
  const databaseSizesQ = useDatabaseSizes({ days: days || undefined });
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
        {databaseSizesQ.error instanceof Error && <div className="oc-error-banner">{databaseSizesQ.error.message}</div>}
        <DatabaseSizeChart samples={databaseSizesQ.data} isLoading={databaseSizesQ.isLoading} />
      </section>
    </div>
  );
}
