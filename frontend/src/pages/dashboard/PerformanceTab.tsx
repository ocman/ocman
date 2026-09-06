import { useState } from 'react';
import { renderModel } from '../../lib/format';
import { useMetrics } from '../../lib/queries';
import { ModelLogo } from '../../components/ModelLogo';
import { AnalyticsFilters } from './AnalyticsFilters';
import { useDashboard } from './context';
import { ChartSkeletons } from './shared';
import { PerformanceCharts, PerformanceSummaryCards } from './StatsLogTables';

export function PerformanceTab() {
  const { dirScope } = useDashboard();
  const [agent, setAgent] = useState('');
  const [model, setModel] = useState('');
  const [days, setDays] = useState(30);
  const metricsQ = useMetrics({ agent: agent || undefined, model: model || undefined, days, dir: dirScope || undefined });
  const metrics = metricsQ.data;
  const agentOptions = [{ value: '', label: 'All agents' }, ...(metrics?.availableAgents ?? []).map((value) => ({ value, label: value }))];
  const modelOptions = [{ value: '', label: 'All models' }, ...(metrics?.availableModels ?? []).map((value) => ({ value, label: renderModel(value), icon: <ModelLogo model={value} /> }))];

  return (
    <div className="metrics-page">
      <AnalyticsFilters days={days} onDaysChange={setDays} agent={agent} onAgentChange={setAgent} agentOptions={agentOptions} model={model} onModelChange={setModel} modelOptions={modelOptions} />
      {metricsQ.error instanceof Error && <div className="oc-error-banner">{metricsQ.error.message}</div>}
      {metricsQ.isLoading && !metrics && <ChartSkeletons labels={['Loading throughput', 'Loading request latency', 'Loading error rate', 'Loading cache efficiency', 'Loading stop reasons']} />}
      {metrics && <><PerformanceSummaryCards metrics={metrics} /><PerformanceCharts metrics={metrics} /></>}
    </div>
  );
}
