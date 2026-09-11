import { useState } from 'react';
import { Bar, Doughnut } from 'react-chartjs-2';
import { BAR_OPTIONS_COST_BY_MODEL, BAR_OPTIONS_HOURLY_TOKENS, BAR_OPTIONS_TOKENS_BY_MODEL, CHART_COLORS, COST_DOUGHNUT_OPTIONS, DOUGHNUT_OPTIONS } from '../../lib/chartConfig';
import { formatCompactNumber, formatCurrency, formatNumber, formatPercent, renderModel } from '../../lib/format';
import { useHourlyTokens, useMetrics, useModels } from '../../lib/queries';
import { ModelLogo } from '../../components/ModelLogo';
import { AnalyticsFilters } from './AnalyticsFilters';
import { useDashboard } from './context';
import { ChartCard, ChartSlot, ChartSkeletons } from './shared';
import { buildCostByModelDatasets } from './metricsChartData';

export function ModelsTab() {
  const { dirScope } = useDashboard();
  const [days, setDays] = useState(30);
  const [model, setModel] = useState('');
  const params = { days: days || undefined, dir: dirScope || undefined };
  const modelsQ = useModels(params);
  const hourlyQ = useHourlyTokens({ ...params, model: model || undefined });
  const metricsQ = useMetrics({ ...params, model: model || undefined });
  const models = [...(modelsQ.data ?? [])].sort((a, b) => b.count - a.count);
  const selectedModels = model ? models.filter((item) => `${item.provider}/${item.model}` === model) : models;
  const top = selectedModels.slice(0, 8);
  const modelCosts = metricsQ.data?.costByModel;
  const costByType = metricsQ.data?.summary.estimatedCostByType;
  const errors = queryErrors(modelsQ.error, hourlyQ.error, metricsQ.error);
  const modelOptions = [{ value: '', label: 'All models' }, ...models.map((item) => ({ value: `${item.provider}/${item.model}`, label: item.model, icon: <ModelLogo model={`${item.provider}/${item.model}`} /> }))];

  return (
    <div className="metrics-page">
      <AnalyticsFilters days={days} onDaysChange={setDays} model={model} onModelChange={setModel} modelOptions={modelOptions} />
      {errors.map((error) => <div key={error.message} className="oc-error-banner">{error.message}</div>)}
      <div className="analytics-chart-pair">
        <ChartSlot isLoading={modelsQ.isLoading} label="Loading model usage"><ChartCard title="Model Usage">
              <Doughnut data={{ labels: top.map((item) => item.model), datasets: [{ data: top.map((item) => item.count), backgroundColor: CHART_COLORS, borderWidth: 0 }] }} options={DOUGHNUT_OPTIONS} />
            </ChartCard></ChartSlot>
        <ChartSlot isLoading={modelsQ.isLoading} label="Loading tokens by model"><ChartCard title="Tokens by Model">
              <Bar data={{ labels: top.map((item) => item.model), datasets: [
                { label: 'Input', data: top.map((item) => item.tokensIn), backgroundColor: 'rgba(137, 180, 250, 0.6)' },
                { label: 'Output', data: top.map((item) => item.tokensOut), backgroundColor: 'rgba(203, 166, 247, 0.6)' },
                { label: 'Cache Read', data: top.map((item) => item.cacheRead ?? 0), backgroundColor: 'rgba(166, 227, 161, 0.6)' },
              ] }} options={BAR_OPTIONS_TOKENS_BY_MODEL} />
            </ChartCard></ChartSlot>
      </div>
      {metricsQ.isLoading && !metricsQ.data && <ChartSkeletons labels={['Loading effective cost', 'Loading agent breakdown']} />}
      {metricsQ.data && <>
            <div className="analytics-chart-pair">
              <ChartCard title="Cost Distribution by Model"><Doughnut data={{ labels: modelCosts?.models.map(renderModel), datasets: [{ data: modelCosts?.series.at(-1)?.costs ?? [], backgroundColor: CHART_COLORS, borderWidth: 0 }] }} options={COST_DOUGHNUT_OPTIONS} /></ChartCard>
              <ChartCard title="Estimated Cost Distribution by Type"><Doughnut data={{ labels: ['Input', 'Output', 'Cache read', 'Cache write'], datasets: [{ data: [costByType?.input ?? 0, costByType?.output ?? 0, costByType?.cacheRead ?? 0, costByType?.cacheWrite ?? 0], backgroundColor: CHART_COLORS.slice(0, 4), borderWidth: 0 }] }} options={COST_DOUGHNUT_OPTIONS} /></ChartCard>
            </div>
            <div className="metrics-chart-grid"><ChartCard title="Effective Cost per Day by Model (USD)"><Bar data={{ labels: metricsQ.data.dailyEffectiveCostByModel.series.map((point) => point.label), datasets: buildCostByModelDatasets(metricsQ.data) }} options={BAR_OPTIONS_COST_BY_MODEL} /></ChartCard></div>
            <div className="chart-card">
              <h3>Agent breakdown</h3>
              <div className="metrics-table-wrap"><table><thead><tr><th>Agent</th><th>Requests</th><th>Errors</th><th>Tokens</th><th>Effective cost</th></tr></thead><tbody>
                {metricsQ.data.agents.map((agent) => <tr key={agent.agent}><td>{agent.agent || 'Unknown'}</td><td>{formatNumber(agent.requests)}</td><td>{formatPercent(agent.errorRate)}</td><td>{formatCompactNumber(agent.totalTokens)}</td><td>{formatCurrency(agent.effectiveCost)}</td></tr>)}
                {metricsQ.data.agents.length === 0 && <tr><td colSpan={5}>No agents matched the current filters</td></tr>}
              </tbody></table></div>
            </div>
      </>}
      {hourlyQ.isLoading && !hourlyQ.data && <ChartSkeletons labels={['Loading hourly model tokens']} />}
      {(hourlyQ.data?.length ?? 0) > 0 && <HourlyModelTokens data={hourlyQ.data ?? []} days={days || 7} />}
    </div>
  );
}

function queryErrors(...errors: unknown[]) {
  return errors.filter((error): error is Error => error instanceof Error);
}

function HourlyModelTokens({ data, days }: { data: Array<{ datetime: string; provider: string; model: string; tokensIn: number; tokensOut: number }>; days: number }) {
  const end = new Date(`${data.at(-1)!.datetime.replace(' ', 'T')}:00:00`).getTime();
  const labels = Array.from({ length: days * 24 }, (_, index) => {
    const date = new Date(end - (days * 24 - index - 1) * 3_600_000);
    return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')} ${String(date.getHours()).padStart(2, '0')}`;
  });
  const totals = new Map<string, number>();
  const values = new Map<string, Map<string, number>>();
  for (const point of data) {
    const key = `${point.provider}/${point.model}`;
    const tokens = point.tokensIn + point.tokensOut;
    totals.set(key, (totals.get(key) ?? 0) + tokens);
    if (!values.has(key)) values.set(key, new Map());
    values.get(key)!.set(point.datetime, (values.get(key)!.get(point.datetime) ?? 0) + tokens);
  }
  const models = [...totals].sort((a, b) => b[1] - a[1]).slice(0, 8).map(([key]) => key);
  return <ChartCard title="Tokens per Hour by Model" style={{ marginTop: 24 }}><Bar data={{ labels, datasets: models.map((key, index) => ({ label: key.split('/').pop(), data: labels.map((label) => values.get(key)?.get(label) ?? 0), backgroundColor: CHART_COLORS[index % CHART_COLORS.length] })) }} options={{ ...BAR_OPTIONS_HOURLY_TOKENS, plugins: { ...BAR_OPTIONS_HOURLY_TOKENS.plugins, tooltip: { callbacks: { ...BAR_OPTIONS_HOURLY_TOKENS.plugins.tooltip.callbacks, label: (context) => `${context.dataset.label}: ${formatCompactNumber(context.raw as number)} tokens` } } } }} /></ChartCard>;
}
