import { Bar } from 'react-chartjs-2';
import type { PermissionStats } from '../../lib/api';
import { BAR_OPTIONS_STACKED, CHART_COLORS } from '../../lib/chartConfig';
import { formatNumber, formatPercent, formatSeconds } from '../../lib/format';
import { ChartCard, MetricCard } from './shared';

const evaluationDatasets = [
  ['safe', 'Safe'],
  ['unsafe', 'Unsafe'],
  ['cache-safe', 'Cache-safe'],
  ['denylisted', 'Denylisted'],
  ['error', 'Error'],
] as const;

export function PermissionStatsSection({ stats }: { stats: PermissionStats }) {
  return (
    <>
      <div className="metrics-summary-grid">
        <MetricCard label="Eligible requests" value={formatNumber(stats.eligibleRequests)} tone="blue" />
        <MetricCard label="Auto-approved rate" value={formatPercent(stats.autoApprovedRate)} tone="green" />
        <MetricCard label="Manual preemptions" value={formatNumber(stats.manualPreemptions)} tone="orange" />
        <MetricCard label="Preemption rate" value={formatPercent(stats.manualPreemptionRate)} tone="orange" />
        <MetricCard label="Median judgment time" value={formatSeconds(stats.medianJudgmentDurationMs / 1000)} tone="purple" />
        <MetricCard label="Median manual response time" value={formatSeconds(stats.medianManualResponseDurationMs / 1000)} tone="purple" />
      </div>
      <div className="metrics-chart-grid">
        <ChartCard title="Permission approvals per day">
          <Bar
            data={{
              labels: stats.daily.map((day) => day.date),
              datasets: [
                ...evaluationDatasets.map(([key, label], index) => ({
                  label,
                  data: stats.daily.map((day) => day.evaluationResults[key] ?? 0),
                  backgroundColor: CHART_COLORS[index],
                  stack: 'permissions',
                })),
                {
                  label: 'Manual preemptions',
                  data: stats.daily.map((day) => day.manualPreemptions),
                  backgroundColor: CHART_COLORS[5],
                  stack: 'preemptions',
                },
              ],
            }}
            options={BAR_OPTIONS_STACKED}
          />
        </ChartCard>
      </div>
    </>
  );
}
