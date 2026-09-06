import { useEffect, useState } from 'react';
import type { MetricsLogKind } from '../../lib/api';
import { renderModel } from '../../lib/format';
import { useMetricLogs } from '../../lib/queries';
import { ModelLogo } from '../../components/ModelLogo';
import { AnalyticsFilters } from './AnalyticsFilters';
import { useDashboard } from './context';
import { MetricsPagination } from './shared';
import { LogRange, ProjectLogTable, RequestLogTable, SessionLogTable } from './StatsLogTables';

const PAGE_SIZE = 20;

export function LogsTab() {
  const { dirScope } = useDashboard();
  const [kind, setKind] = useState<MetricsLogKind>('project');
  const [agent, setAgent] = useState('');
  const [model, setModel] = useState('');
  const [days, setDays] = useState(30);
  const [page, setPage] = useState(0);

  // eslint-disable-next-line react-hooks/set-state-in-effect
  useEffect(() => setPage(0), [kind, agent, model, days, dirScope]);

  const logsQ = useMetricLogs({
    kind,
    agent: agent || undefined,
    model: model || undefined,
    days,
    dir: dirScope || undefined,
    limit: kind === 'request' ? PAGE_SIZE : undefined,
    offset: kind === 'request' ? page * PAGE_SIZE : undefined,
    sessionLimit: kind === 'session' ? PAGE_SIZE : undefined,
    sessionOffset: kind === 'session' ? page * PAGE_SIZE : undefined,
    projectLimit: kind === 'project' ? PAGE_SIZE : undefined,
    projectOffset: kind === 'project' ? page * PAGE_SIZE : undefined,
  });
  const logs = logsQ.data;
  const agentOptions = [{ value: '', label: 'All agents' }, ...(logs?.availableAgents ?? []).map((value) => ({ value, label: value }))];
  const modelOptions = [{ value: '', label: 'All models' }, ...(logs?.availableModels ?? []).map((value) => ({ value, label: renderModel(value), icon: <ModelLogo model={value} /> }))];

  return (
    <div className="metrics-page">
      <AnalyticsFilters days={days} onDaysChange={setDays} agent={agent} onAgentChange={setAgent} agentOptions={agentOptions} model={model} onModelChange={setModel} modelOptions={modelOptions} />
      {logsQ.error instanceof Error && <div className="oc-error-banner">{logsQ.error.message}</div>}
      <div className="chart-card">
        <div className="metrics-log-header">
          <div className="nav-tabs metrics-log-tabs">
            {(['project', 'session', 'request'] as const).map((value) => <button key={value} className={`nav-tab${kind === value ? ' active' : ''}`} onClick={() => setKind(value)}>{value[0].toUpperCase() + value.slice(1)} Log</button>)}
          </div>
          {logs && <span className="metrics-log-range"><LogRange page={page} pageSize={PAGE_SIZE} total={logs.total} /></span>}
        </div>
        {logsQ.isLoading && !logs && <div className="oc-list-loading"><div className="oc-spinner" />Loading logs...</div>}
        {logs && kind === 'project' && <ProjectLogTable projects={logs.projects ?? []} pageOffset={page * PAGE_SIZE} />}
        {logs && kind === 'session' && <SessionLogTable sessions={logs.sessions ?? []} pageOffset={page * PAGE_SIZE} />}
        {logs && kind === 'request' && <RequestLogTable requests={logs.requests ?? []} pageOffset={page * PAGE_SIZE} />}
        {logs && <MetricsPagination page={page} pageSize={PAGE_SIZE} total={logs.total} onChange={setPage} />}
      </div>
    </div>
  );
}
