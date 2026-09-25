import { useEffect, useState } from 'react';
import type { MetricsLogKind } from '../../lib/api';
import { renderModel } from '../../lib/format';
import { useMetricLogs } from '../../lib/queries';
import { ModelLogo } from '../../components/ModelLogo';
import { Tabs, TabsList, TabsTrigger, TabsContent } from '../../components/Tabs';
import { AnalyticsFilters } from './AnalyticsFilters';
import { useDashboard } from './context';
import { MetricsPagination } from './shared';
import { LogRange, ProjectLogTable, RequestLogTable, SessionLogTable } from './StatsLogTables';

const PAGE_SIZE = 20;
const kinds = ['project', 'session', 'request'] as const;

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
      <Tabs className="chart-card" value={kind} onValueChange={(value) => setKind(value as MetricsLogKind)}>
        <div className="metrics-log-header">
          <TabsList aria-label="Log views" className="metrics-log-tabs">
            {kinds.map((value) => <TabsTrigger key={value} value={value}>{value[0].toUpperCase() + value.slice(1)} Log</TabsTrigger>)}
          </TabsList>
          {logs && <span className="metrics-log-range"><LogRange page={page} pageSize={PAGE_SIZE} total={logs.total} /></span>}
        </div>
        {kinds.map((value) => <TabsContent key={value} value={value}>
          {logsQ.isLoading && !logs && <div className="oc-list-loading"><div className="oc-spinner" />Loading logs...</div>}
          {logs && value === 'project' && <ProjectLogTable projects={logs.projects ?? []} pageOffset={page * PAGE_SIZE} />}
          {logs && value === 'session' && <SessionLogTable sessions={logs.sessions ?? []} pageOffset={page * PAGE_SIZE} />}
          {logs && value === 'request' && <RequestLogTable requests={logs.requests ?? []} pageOffset={page * PAGE_SIZE} />}
          {logs && <MetricsPagination page={page} pageSize={PAGE_SIZE} total={logs.total} onChange={setPage} />}
        </TabsContent>)}
      </Tabs>
    </div>
  );
}
