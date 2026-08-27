import { useState, useCallback, useEffect } from 'react';
import { renderModel } from '../../lib/format';
import { usePageTitle } from '../../lib/headerContext';
import { ProjectScopePicker } from '../../components/ProjectScopePicker';
import { SearchSelect } from '../../components/SearchSelect';
import { useMetrics, usePermissionStats } from '../../lib/queries';
import { useDashboard } from './context';
import { MetricsPagination } from './shared';
import { SessionLogTable, ProjectLogTable, RequestLogTable, StatsSummaryCharts, LogRange } from './StatsLogTables';
import { METRICS_RANGE_OPTIONS } from './constants';
import { ModelLogo } from '../../components/ModelLogo';
import { PermissionStatsSection } from './PermissionStatsSection';

export function StatsTab() {
  usePageTitle('Stats');
  const { projects, dirScope, setDirScope } = useDashboard();

  const [selectedAgent, setSelectedAgentRaw] = useState('');
  const [selectedModel, setSelectedModelRaw] = useState('');
  const [metricsDays, setMetricsDaysRaw] = useState(30);
  const [logTab, setLogTab] = useState<'session' | 'request' | 'project'>('project');
  const [logPage, setLogPage] = useState(0);
  const [sessionLogPage, setSessionLogPage] = useState(0);
  const [projectLogPage, setProjectLogPage] = useState(0);
  const LOG_PAGE_SIZE = 20;
  const SESSION_LOG_PAGE_SIZE = 20;
  const PROJECT_LOG_PAGE_SIZE = 20;

  // Reset pagination when filters change. Done in the setter wrappers
  // rather than a useEffect to avoid cascading renders.
  const resetPages = useCallback(() => {
    setLogPage(0);
    setSessionLogPage(0);
    setProjectLogPage(0);
  }, []);
  const setSelectedAgent = useCallback((v: string) => { setSelectedAgentRaw(v); resetPages(); }, [resetPages]);
  const setSelectedModel = useCallback((v: string) => { setSelectedModelRaw(v); resetPages(); }, [resetPages]);
  const setMetricsDays = useCallback((v: number) => { setMetricsDaysRaw(v); resetPages(); }, [resetPages]);

  // Also reset pages when dirScope changes (comes from the URL / ProjectScopePicker).
  // This is a well-known React pattern for resetting derived state when a
  // prop/context value changes. The lint rule flags it because it's a
  // cascading render, but the alternative (key-based remount) would lose
  // all local filter state.
  // eslint-disable-next-line react-hooks/set-state-in-effect
  useEffect(() => { resetPages(); }, [dirScope, resetPages]);

  // TanStack Query handles cancellation, dedup, and stale-while-revalidate
  // automatically (Wave 3 / P4+P5 fix).
  const metricsQ = useMetrics({
    agent: selectedAgent || undefined,
    model: selectedModel || undefined,
    days: metricsDays,
    limit: LOG_PAGE_SIZE,
    offset: logPage * LOG_PAGE_SIZE,
    sessionLimit: SESSION_LOG_PAGE_SIZE,
    sessionOffset: sessionLogPage * SESSION_LOG_PAGE_SIZE,
    projectLimit: PROJECT_LOG_PAGE_SIZE,
    projectOffset: projectLogPage * PROJECT_LOG_PAGE_SIZE,
    dir: dirScope || undefined,
  });
  const permissionStatsQ = usePermissionStats({
    days: metricsDays,
    dir: dirScope || undefined,
  });

  const metrics = metricsQ.data ?? null;
  const metricsLoading = metricsQ.isLoading;
  const metricsError = metricsQ.error instanceof Error ? metricsQ.error.message : null;
  const permissionStatsError = permissionStatsQ.error instanceof Error ? permissionStatsQ.error.message : null;
  const agentOptions = [{ value: '', label: 'All agents' }, ...(metrics?.availableAgents ?? []).map((agent) => ({ value: agent, label: agent }))];
  const modelOptions = [{ value: '', label: 'All models' }, ...(metrics?.availableModels ?? []).map((model) => ({ value: model, label: renderModel(model), icon: <ModelLogo model={model} /> }))];

  return (
    <div className="metrics-page">
      <div className="metrics-filters">
        <ProjectScopePicker projects={projects} value={dirScope} onChange={setDirScope} showLabel />
        <label className="metrics-filter">
          <span>Agent</span>
          <SearchSelect value={selectedAgent} ariaLabel="Agent" placeholder="All agents" searchLabel="Search agents" onChange={setSelectedAgent} options={agentOptions} />
        </label>
        <label className="metrics-filter">
          <span>Model</span>
          <SearchSelect value={selectedModel} ariaLabel="Model" placeholder="All models" searchLabel="Search models" onChange={setSelectedModel} options={modelOptions} />
        </label>
        <label className="metrics-filter metrics-filter-small">
          <span>Last</span>
          <SearchSelect value={String(metricsDays)} ariaLabel="Last" placeholder="Select range" searchLabel="Search ranges" onChange={(value) => setMetricsDays(Number(value))} options={METRICS_RANGE_OPTIONS.map((option) => ({ value: String(option.value), label: option.label }))} />
        </label>
      </div>

      {metricsError && (
        <div className="oc-error-banner">
          {metricsError}
        </div>
      )}

      {permissionStatsError && (
        <div className="oc-error-banner">
          Permission stats: {permissionStatsError}
        </div>
      )}

      {metricsLoading && !metrics && (
        <div className="oc-list-loading">
          <div className="oc-spinner" />
          Loading metrics...
        </div>
      )}

      {metrics && <StatsSummaryCharts metrics={metrics} />}
      {permissionStatsQ.data && <PermissionStatsSection stats={permissionStatsQ.data} />}

      {metrics && (
        <div className="chart-card">
            <div className="metrics-log-header">
              <div className="nav-tabs metrics-log-tabs">
                <button
                  className={`nav-tab${logTab === 'project' ? ' active' : ''}`}
                  onClick={() => setLogTab('project')}
                >Project Log</button>
                <button
                  className={`nav-tab${logTab === 'session' ? ' active' : ''}`}
                  onClick={() => setLogTab('session')}
                >Session Log</button>
                <button
                  className={`nav-tab${logTab === 'request' ? ' active' : ''}`}
                  onClick={() => setLogTab('request')}
                >Request Log</button>
              </div>
              <span style={{ fontWeight: 400, fontSize: 12, color: 'var(--text-dim)' }}>
                {logTab === 'session' && <LogRange page={sessionLogPage} pageSize={SESSION_LOG_PAGE_SIZE} total={metrics.totalSessions} />}
                {logTab === 'project' && <LogRange page={projectLogPage} pageSize={PROJECT_LOG_PAGE_SIZE} total={metrics.totalProjects} />}
                {logTab === 'request' && <LogRange page={logPage} pageSize={LOG_PAGE_SIZE} total={metrics.totalRequests} />}
              </span>
            </div>

            {logTab === 'session' && (
              <>
                <SessionLogTable metrics={metrics} pageOffset={sessionLogPage * SESSION_LOG_PAGE_SIZE} />
                <MetricsPagination
                  page={sessionLogPage}
                  pageSize={SESSION_LOG_PAGE_SIZE}
                  total={metrics.totalSessions}
                  onChange={setSessionLogPage}
                />
              </>
            )}

            {logTab === 'project' && (
              <>
                <ProjectLogTable metrics={metrics} pageOffset={projectLogPage * PROJECT_LOG_PAGE_SIZE} />
                <MetricsPagination
                  page={projectLogPage}
                  pageSize={PROJECT_LOG_PAGE_SIZE}
                  total={metrics.totalProjects}
                  onChange={setProjectLogPage}
                />
              </>
            )}

            {logTab === 'request' && (
              <>
                <RequestLogTable metrics={metrics} pageOffset={logPage * LOG_PAGE_SIZE} />
                <MetricsPagination
                  page={logPage}
                  pageSize={LOG_PAGE_SIZE}
                  total={metrics.totalRequests}
                  onChange={setLogPage}
                />
              </>
            )}
        </div>
      )}
    </div>
  );
}
