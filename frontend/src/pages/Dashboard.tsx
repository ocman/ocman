import { useState, useEffect, useCallback, useContext, useMemo, createContext, type ReactNode } from 'react';
import './Dashboard.css';
import { useNavigate, NavLink, Outlet, useSearchParams, useLocation } from 'react-router-dom';
import { Chart as ChartJS, CategoryScale, LinearScale, BarElement, ArcElement, Tooltip, Legend, PointElement, LineElement } from 'chart.js';
import { Bar, Doughnut, Line } from 'react-chartjs-2';
import type { ActivityDay, HourlyTokensByModel, Project, ProjectLogEntry, Session } from '../lib/api';
import {
  cleanTitle,
  formatCompactNumber,
  formatCurrency,
  formatDateTimeShort,
  formatNumber,
  formatPercent,
  formatSeconds,
  formatTokenCache,
  relativeTime,
  renderModel,
  shortPath,
  shortSessionID,
} from '../lib/format';
import {
  BAR_OPTIONS_TOKS,
  BAR_OPTIONS_DURATION,
  BAR_OPTIONS_STACKED,
  BAR_OPTIONS_HOURLY,
  BAR_OPTIONS_HOURLY_TOKENS,
  BAR_OPTIONS_SESSIONS,
  BAR_OPTIONS_TOKENS_BY_MODEL,
  LINE_OPTIONS_COST,
  LINE_OPTIONS_CACHE,
  DOUGHNUT_OPTIONS,
  CHART_COLORS,
  STOP_REASON_COLORS,
} from '../lib/chartConfig';
import { usePageTitle } from '../lib/headerContext';
import { SessionTable } from '../components/SessionTable';
import { ErrorBoundary } from '../components/ErrorBoundary';
import { ProjectScopePicker } from '../components/ProjectScopePicker';
import { matchesScope } from '../lib/projectTree';

import { useUiStore } from '../lib/uiStore';
import { useAuthStore } from '../lib/authStore';
import { usePwaInstall } from '../lib/usePwaInstall';
import {
  notificationsSupported,
  requestNotificationPermission,
} from '../lib/useNotificationNotify';
import { useSessions as useTQSessions, useProjects as useTQProjects, useActivity, useModels, useHourly, useHourlyTokens, useMetrics } from '../lib/queries';

ChartJS.register(CategoryScale, LinearScale, BarElement, ArcElement, PointElement, LineElement, Tooltip, Legend);

// Stable empty arrays so `data ?? []` doesn't create a new reference on
// every render while the query is still loading.
const EMPTY_SESSIONS: Session[] = [];
const EMPTY_PROJECTS: Project[] = [];

const METRICS_RANGE_OPTIONS = [
  { label: '24 hours', value: 1 },
  { label: '7 days', value: 7 },
  { label: '30 days', value: 30 },
  { label: '90 days', value: 90 },
  { label: 'All time', value: 0 },
];

// ---------------------------------------------------------------------------
// Shared dashboard context — sessions + projects are fetched once in the
// layout and shared across all three tab routes.
// ---------------------------------------------------------------------------

interface DashboardCtx {
  sessions: Session[];
  projects: Project[];
  sessionsLoading: boolean;
  sessionsError: string | null;
  projectsLoading: boolean;
  loadSessions: () => void;
  timeRange: number;
  setTimeRange: (v: number) => void;
  showArchived: boolean;
  setShowArchived: (v: boolean) => void;
  /**
   * Active project-prefix scope, persisted in the URL as `?dir=`. Empty
   * string means "all projects". Shared across the Stats / Usage /
   * Projects tabs so a chosen scope survives tab switches.
   * See spec/stats-project-filter/architecture.md (AD-3, AD-5).
   */
  dirScope: string;
  setDirScope: (v: string) => void;
}

const DashboardContext = createContext<DashboardCtx | null>(null);

function useDashboard(): DashboardCtx {
  const ctx = useContext(DashboardContext);
  if (!ctx) throw new Error('useDashboard must be used inside DashboardLayout');
  return ctx;
}

// ---------------------------------------------------------------------------
// Layout — renders the tab bar + <Outlet> for the active tab route.
// ---------------------------------------------------------------------------

export function DashboardLayout() {
  const location = useLocation();
  const [searchParams, setSearchParams] = useSearchParams();

  const isOnDashboard = location.pathname === '/' || location.pathname === '/projects' || location.pathname === '/stats' || location.pathname === '/usage' || location.pathname === '/settings';

  const timeRange = parseInt(searchParams.get('t') || '12', 10);
  const showArchived = searchParams.get('a') === '1';
  const dirScope = searchParams.get('dir') ?? '';

  const setTimeRange = useCallback((v: number) => {
    setSearchParams((p) => { p.set('t', String(v)); return p; }, { replace: true });
  }, [setSearchParams]);

  const setShowArchived = useCallback((v: boolean) => {
    setSearchParams((p) => {
      if (v) p.set('a', '1');
      else p.delete('a');
      return p;
    }, { replace: true });
  }, [setSearchParams]);

  // setDirScope writes the chosen scope to the URL so it survives refresh
  // and is preserved when the user switches between the Stats/Usage/Projects
  // tabs. Empty value clears the param entirely so the URL stays clean
  // ('?t=24' rather than '?t=24&dir=').
  const setDirScope = useCallback((v: string) => {
    setSearchParams((p) => {
      if (v) p.set('dir', v);
      else p.delete('dir');
      return p;
    }, { replace: true });
  }, [setSearchParams]);

  // TanStack Query handles dedup, cancellation, stale-while-revalidate,
  // and visibility pausing automatically (Wave 3 / P4+P5 fix).
  // sinceHours produces a stable query key; the actual timestamp is
  // computed inside the queryFn at fetch time.
  const sinceHours = timeRange > 0 ? timeRange : 30 * 24;
  const sessionsQ = useTQSessions(
    { sinceHours },
    { refetchInterval: isOnDashboard ? 5000 : undefined },
  );
  const projectsQ = useTQProjects();

  // Module-level constants avoid creating a new [] on every render when
  // the query hasn't resolved yet (undefined ?? [] would be a fresh ref).
  const sessions = sessionsQ.data ?? EMPTY_SESSIONS;
  const projects = projectsQ.data ?? EMPTY_PROJECTS;

  const sessionsError = sessionsQ.error instanceof Error ? sessionsQ.error.message : null;
  const { isLoading: sessionsLoading, refetch: refetchSessions } = sessionsQ;
  const { isLoading: projectsLoading } = projectsQ;

  const loadSessions = useCallback(() => { void refetchSessions(); }, [refetchSessions]);

  const ctx: DashboardCtx = useMemo(() => ({
    sessions,
    projects,
    sessionsLoading,
    sessionsError,
    projectsLoading,
    loadSessions,
    timeRange,
    setTimeRange,
    showArchived,
    setShowArchived,
    dirScope,
    setDirScope,
  }), [
    sessions,
    projects,
    sessionsLoading,
    sessionsError,
    projectsLoading,
    loadSessions,
    timeRange,
    setTimeRange,
    showArchived,
    setShowArchived,
    dirScope,
    setDirScope,
  ]);

  return (
    <DashboardContext.Provider value={ctx}>
      <div style={{ flex: 1, overflowY: 'auto', minHeight: 0 }}>
        <div className="nav-tabs">
          <NavLink to="/" end className={({ isActive }) => `nav-tab${isActive ? ' active' : ''}`}>Sessions</NavLink>
          <NavLink to="/projects" className={({ isActive }) => `nav-tab${isActive ? ' active' : ''}`}>Projects</NavLink>
          <NavLink to="/stats" className={({ isActive }) => `nav-tab${isActive ? ' active' : ''}`}>Stats</NavLink>
          <NavLink to="/usage" className={({ isActive }) => `nav-tab${isActive ? ' active' : ''}`}>Usage</NavLink>
          <NavLink to="/settings" className={({ isActive }) => `nav-tab${isActive ? ' active' : ''}`}>Settings</NavLink>
        </div>
        {/* Per-tab boundary so a crash inside Stats / Usage / etc.
            (chart.js render error, malformed metrics payload) doesn't
            blank the tab bar above. resetKey on pathname auto-clears
            when the user switches to a different tab. */}
        <ErrorBoundary name={`dashboard:${location.pathname}`} resetKey={location.pathname}>
          <Outlet />
        </ErrorBoundary>
      </div>
    </DashboardContext.Provider>
  );
}

// ---------------------------------------------------------------------------
// Sessions tab
// ---------------------------------------------------------------------------

export function SessionsTab() {
  usePageTitle('Sessions');
  const { sessions, sessionsLoading, sessionsError, loadSessions, timeRange, setTimeRange, showArchived, setShowArchived } = useDashboard();

  return (
    <>
      {sessionsError && (
        <div className="oc-error-banner">
          {sessionsError}
          <button onClick={() => loadSessions()}>Retry</button>
        </div>
      )}
      <div className="oc-time-range">
        {[{label: '12h', value: 12}, {label: '24h', value: 24}, {label: '7d', value: 168}, {label: '30d', value: 720}, {label: 'All', value: 0}].map((opt) => (
          <button
            key={opt.value}
            className={`oc-time-range-btn${timeRange === opt.value ? ' active' : ''}`}
            onClick={() => setTimeRange(opt.value)}
          >{opt.label}</button>
        ))}
        <button
          className={`oc-time-range-btn${showArchived ? ' active' : ''}`}
          onClick={() => setShowArchived(!showArchived)}
        >Exclude archived</button>
      </div>
      <SessionTable sessions={sessions} showProject loading={sessionsLoading && sessions.length === 0} includeArchived={!showArchived} />
    </>
  );
}

// ---------------------------------------------------------------------------
// Projects tab
// ---------------------------------------------------------------------------

export function ProjectsTab() {
  usePageTitle('Projects');
  const { projects, projectsLoading, dirScope, setDirScope } = useDashboard();
  const navigate = useNavigate();

  // The picker is sourced from the full project list (so the user can
  // navigate up/down the tree); the table itself is filtered to the
  // active scope. matchesScope mirrors the SQL predicate used by the
  // backend (see spec/stats-project-filter/architecture.md, AD-7).
  const visibleProjects = projects
    .filter((p) => p.sessionCount > 0)
    .filter((p) => matchesScope(p.directory, dirScope));

  return projectsLoading && projects.length === 0 ? (
    <div className="oc-list-loading">
      <div className="oc-spinner" />
      Loading projects...
    </div>
  ) : (
    <div className="metrics-page" style={{ padding: 0 }}>
      <div className="metrics-filters">
        <ProjectScopePicker projects={projects} value={dirScope} onChange={setDirScope} />
      </div>
    <table>
      <thead>
        <tr>
          <th>Project</th>
          <th>Sessions</th>
          <th>Messages</th>
          <th>Tokens (in/out)</th>
          <th>Last Active</th>
        </tr>
      </thead>
      <tbody>
        {visibleProjects.length === 0 ? (
          <tr>
            <td colSpan={5} style={{ textAlign: 'center', color: 'var(--text-dim)', padding: 24 }}>
              No projects found
            </td>
          </tr>
        ) : visibleProjects.map((p) => (
          <tr key={p.directory} onClick={() => navigate(`/project/${encodeURIComponent(p.directory)}`)}>
            <td>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <span style={{ color: 'var(--accent)', fontWeight: 500 }}>{shortPath(p.directory)}</span>
                <a
                  href={`vscode://file${p.directory}`}
                  className="vscode-btn"
                  title="Open in VS Code"
                  onClick={(e) => e.stopPropagation()}
                >VS Code</a>
              </div>
              <div className="mono">{p.directory}</div>
            </td>
            <td>{p.sessionCount}</td>
            <td>{p.messageCount}</td>
            <td className="mono">{formatNumber(p.totalTokensIn)} / {formatNumber(p.totalTokensOut)}</td>
            <td>{relativeTime(p.lastUsed)}</td>
          </tr>
        ))}
      </tbody>
    </table>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Stats tab
// ---------------------------------------------------------------------------

export function StatsTab() {
  usePageTitle('Stats');
  const navigate = useNavigate();
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

  const metrics = metricsQ.data ?? null;
  const metricsLoading = metricsQ.isLoading;
  const metricsError = metricsQ.error instanceof Error ? metricsQ.error.message : null;

  const metricLabels = metrics?.series.map((point) => point.label) ?? [];

  return (
    <div className="metrics-page">
      <div className="metrics-filters">
        <ProjectScopePicker projects={projects} value={dirScope} onChange={setDirScope} />
        <label className="metrics-filter">
          <span>Agent</span>
          <select value={selectedAgent} onChange={(e) => setSelectedAgent(e.target.value)}>
            <option value="">All agents</option>
            {(metrics?.availableAgents ?? []).map((agent) => (
              <option key={agent} value={agent}>{agent}</option>
            ))}
          </select>
        </label>
        <label className="metrics-filter">
          <span>Model</span>
          <select value={selectedModel} onChange={(e) => setSelectedModel(e.target.value)}>
            <option value="">All models</option>
            {(metrics?.availableModels ?? []).map((model) => (
              <option key={model} value={model}>{renderModel(model)}</option>
            ))}
          </select>
        </label>
        <label className="metrics-filter metrics-filter-small">
          <span>Last</span>
          <select value={metricsDays} onChange={(e) => setMetricsDays(Number(e.target.value))}>
            {METRICS_RANGE_OPTIONS.map((option) => (
              <option key={option.value} value={option.value}>{option.label}</option>
            ))}
          </select>
        </label>
      </div>

      {metricsError && (
        <div className="oc-error-banner">
          {metricsError}
        </div>
      )}

      {metricsLoading && !metrics ? (
        <div className="oc-list-loading">
          <div className="oc-spinner" />
          Loading metrics...
        </div>
      ) : metrics && (
        <>
          <div className="metrics-summary-grid">
            <MetricCard label="Requests" value={formatNumber(metrics.summary.requests)} tone="blue" />
            <MetricCard label="Total Tokens" value={formatCompactNumber(metrics.summary.totalTokens)} tone="blue" subvalue={`${formatCompactNumber(metrics.summary.inputTokens)} in / ${formatCompactNumber(metrics.summary.outputTokens)} out`} />
            <MetricCard label="Avg Tok/s" value={metrics.summary.avgTokensPerSec.toFixed(1)} tone="orange" />
            <MetricCard label="Avg Duration" value={formatSeconds(metrics.summary.avgDurationMs / 1000)} tone="blue" />
            <MetricCard label="Total Wall Clock" value={formatSeconds(metrics.summary.totalDurationMs / 1000)} tone="blue" subvalue="sum of response times" />
            <MetricCard label="Cache Hit Rate" value={formatPercent(metrics.summary.cacheHitRate)} tone="green" subvalue={formatTokenCache(metrics.summary.cacheReadTokens, metrics.summary.cacheWriteTokens)} />
            <MetricCard label="Total Cost" value={formatCurrency(metrics.summary.totalCost)} tone="green" subvalue="reported by platform" />
            <MetricCard label="Est. API Cost" value={formatCurrency(metrics.summary.totalCalcCost)} tone="orange" subvalue="calculated from tokens" />
          </div>

          <div className="metrics-chart-grid metrics-chart-grid-top">
            <ChartCard title="Avg Output Tokens/Second">
              <Bar data={{
                labels: metricLabels,
                datasets: [{ label: 'Tok/s', data: metrics.series.map((point) => point.avgOutputTokensSec), backgroundColor: 'rgba(249, 226, 175, 0.7)', borderRadius: 2 }],
              }} options={BAR_OPTIONS_TOKS} />
            </ChartCard>

            <ChartCard title="Cumulative Cost (USD)">
              <Line data={{
                labels: metricLabels,
                datasets: [
                  { label: 'Cost', data: metrics.series.map((point) => point.cumulativeCost), borderColor: '#a6e3a1', backgroundColor: 'rgba(166, 227, 161, 0.18)', fill: true, tension: 0.2, pointRadius: 0 },
                  { label: 'Est. Cost', data: metrics.series.map((point) => point.cumulativeCalcCost), borderColor: '#fab387', backgroundColor: 'rgba(250, 179, 135, 0.18)', fill: true, tension: 0.2, pointRadius: 0 },
                ],
              }} options={LINE_OPTIONS_COST} />
            </ChartCard>

            <ChartCard title="Token Usage per Bucket">
              <Bar data={{
                labels: metricLabels,
                datasets: [
                  { label: 'Input', data: metrics.series.map((point) => point.inputTokens), backgroundColor: 'rgba(137, 180, 250, 0.72)', stack: 'tokens' },
                  { label: 'Cache Read', data: metrics.series.map((point) => point.cacheReadTokens), backgroundColor: 'rgba(148, 226, 213, 0.72)', stack: 'tokens' },
                  { label: 'Output', data: metrics.series.map((point) => point.outputTokens), backgroundColor: 'rgba(166, 227, 161, 0.72)', stack: 'tokens' },
                ],
              }} options={BAR_OPTIONS_STACKED} />
            </ChartCard>

            <ChartCard title="Avg Request Duration (s)">
              <Bar data={{
                labels: metricLabels,
                datasets: [{ label: 'Duration', data: metrics.series.map((point) => point.avgDurationMs / 1000), backgroundColor: 'rgba(203, 166, 247, 0.45)', borderRadius: 2 }],
              }} options={BAR_OPTIONS_DURATION} />
            </ChartCard>
          </div>

          <div className="metrics-chart-grid metrics-chart-grid-bottom">
            <ChartCard title="Cache Efficiency">
              <Line data={{
                labels: metricLabels,
                datasets: [{ label: 'Efficiency', data: metrics.series.map((point) => point.avgCacheEfficiency * 100), borderColor: '#94e2d5', backgroundColor: 'rgba(148, 226, 213, 0.15)', fill: true, tension: 0.25, pointRadius: 0 }],
              }} options={LINE_OPTIONS_CACHE} />
            </ChartCard>

            <ChartCard title="Stop Reason Distribution">
              <Doughnut data={{
                labels: metrics.stopReasons.map((item) => item.reason),
                datasets: [{ data: metrics.stopReasons.map((item) => item.count), backgroundColor: metrics.stopReasons.map((_, idx) => STOP_REASON_COLORS[idx % STOP_REASON_COLORS.length]), borderWidth: 0 }],
              }} options={DOUGHNUT_OPTIONS} />
            </ChartCard>
          </div>

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
                {logTab === 'session' && metrics.totalSessions > 0 && (
                  <>{sessionLogPage * SESSION_LOG_PAGE_SIZE + 1}–{Math.min((sessionLogPage + 1) * SESSION_LOG_PAGE_SIZE, metrics.totalSessions)} of {metrics.totalSessions}</>
                )}
                {logTab === 'project' && metrics.totalProjects > 0 && (
                  <>{projectLogPage * PROJECT_LOG_PAGE_SIZE + 1}–{Math.min((projectLogPage + 1) * PROJECT_LOG_PAGE_SIZE, metrics.totalProjects)} of {metrics.totalProjects}</>
                )}
                {logTab === 'request' && metrics.totalRequests > 0 && (
                  <>{logPage * LOG_PAGE_SIZE + 1}–{Math.min((logPage + 1) * LOG_PAGE_SIZE, metrics.totalRequests)} of {metrics.totalRequests}</>
                )}
              </span>
            </div>

            {logTab === 'session' && (
              <>
                <div className="metrics-table-wrap">
                  <table>
                    <thead>
                      <tr>
                        <th>#</th>
                        <th>Title</th>
                        <th>Last Activity</th>
                        <th>Requests</th>
                        <th>Input</th>
                        <th>Output</th>
                        <th>Cache</th>
                        <th>Tok/s</th>
                        <th>Duration</th>
                        <th>Cost</th>
                        <th title="Calculated from token counts using public API pricing">Est. Cost</th>
                        <th>Models</th>
                        <th title="Assistant messages that finished with an error">Errors</th>
                      </tr>
                    </thead>
                    <tbody>
                      {metrics.sessions.length === 0 ? (
                        <tr>
                          <td colSpan={13} style={{ textAlign: 'center', color: 'var(--text-dim)', padding: 24 }}>
                            No sessions matched the current filters
                          </td>
                        </tr>
                      ) : metrics.sessions.map((session, idx) => (
                        <tr key={session.id} onClick={() => navigate(`/session/${encodeURIComponent(session.id)}`)}>
                          <td>{sessionLogPage * SESSION_LOG_PAGE_SIZE + idx + 1}</td>
                          <td title={cleanTitle(session.title)}>
                            <div>{cleanTitle(session.title) || <span style={{ color: 'var(--text-dim)' }}>untitled</span>}</div>
                            {session.directory && <div className="metrics-session-project">{shortPath(session.directory)}</div>}
                          </td>
                          <td title={formatDateTimeShort(session.lastRequestTime)}>{relativeTime(session.lastRequestTime)}</td>
                          <td>{formatNumber(session.requests)}</td>
                          <td>{formatNumber(session.inputTokens)}</td>
                          <td>{formatNumber(session.outputTokens)}</td>
                          <td className="mono">{formatTokenCache(session.cacheReadTokens, session.cacheWriteTokens)}</td>
                          <td>{session.avgTokensPerSec > 0 ? session.avgTokensPerSec.toFixed(1) : '-'}</td>
                          <td>{session.totalDurationMs > 0 ? formatSeconds(session.totalDurationMs / 1000) : '-'}</td>
                          <td>{session.cost > 0 ? formatCurrency(session.cost) : <span style={{ color: 'var(--text-dim)' }}>—</span>}</td>
                          <td>{session.calcCost > 0 ? <span style={{ color: 'var(--accent4)' }}>{formatCurrency(session.calcCost)}</span> : <span style={{ color: 'var(--text-dim)' }}>—</span>}</td>
                          <td title={session.models.join(', ')}>
                            {session.models.length === 0
                              ? <span style={{ color: 'var(--text-dim)' }}>—</span>
                              : session.models.length === 1
                                ? renderModel(session.models[0])
                                : `${renderModel(session.models[0])} +${session.models.length - 1}`}
                          </td>
                          <td>{session.errorCount > 0
                            ? <span style={{ color: 'var(--accent3, #f38ba8)' }}>{session.errorCount}</span>
                            : <span style={{ color: 'var(--text-dim)' }}>—</span>}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
                {metrics.totalSessions > SESSION_LOG_PAGE_SIZE && (
                  <div className="metrics-pagination">
                    <button
                      className="oc-time-range-btn"
                      disabled={sessionLogPage === 0}
                      onClick={() => setSessionLogPage((p) => p - 1)}
                    >Prev</button>
                    <span className="metrics-pagination-info">
                      Page {sessionLogPage + 1} / {Math.ceil(metrics.totalSessions / SESSION_LOG_PAGE_SIZE)}
                    </span>
                    <button
                      className="oc-time-range-btn"
                      disabled={(sessionLogPage + 1) * SESSION_LOG_PAGE_SIZE >= metrics.totalSessions}
                      onClick={() => setSessionLogPage((p) => p + 1)}
                    >Next</button>
                  </div>
                )}
              </>
            )}

            {logTab === 'project' && (
              <>
                <div className="metrics-table-wrap">
                  <table>
                    <thead>
                      <tr>
                        <th>#</th>
                        <th>Project</th>
                        <th>Sessions</th>
                        <th>Requests</th>
                        <th>Input</th>
                        <th>Output</th>
                        <th>Cache</th>
                        <th>Tok/s</th>
                        <th>Duration</th>
                        <th>Cost</th>
                        <th title="Calculated from token counts using public API pricing">Est. Cost</th>
                        <th>Models</th>
                        <th title="Assistant messages that finished with an error">Errors</th>
                      </tr>
                    </thead>
                    <tbody>
                      {metrics.projects.length === 0 ? (
                        <tr>
                          <td colSpan={13} style={{ textAlign: 'center', color: 'var(--text-dim)', padding: 24 }}>
                            No projects matched the current filters
                          </td>
                        </tr>
                      ) : metrics.projects.map((project: ProjectLogEntry, idx: number) => (
                        <tr key={project.directory} onClick={() => navigate(`/project/${encodeURIComponent(project.directory)}`)}>
                          <td>{projectLogPage * PROJECT_LOG_PAGE_SIZE + idx + 1}</td>
                          <td title={project.directory}>
                            <span style={{ color: 'var(--accent)', fontWeight: 500 }}>{shortPath(project.directory)}</span>
                          </td>
                          <td>{formatNumber(project.sessions)}</td>
                          <td>{formatNumber(project.requests)}</td>
                          <td>{formatNumber(project.inputTokens)}</td>
                          <td>{formatNumber(project.outputTokens)}</td>
                          <td className="mono">{formatTokenCache(project.cacheReadTokens, project.cacheWriteTokens)}</td>
                          <td>{project.avgTokensPerSec > 0 ? project.avgTokensPerSec.toFixed(1) : '-'}</td>
                          <td>{project.totalDurationMs > 0 ? formatSeconds(project.totalDurationMs / 1000) : '-'}</td>
                          <td>{project.cost > 0 ? formatCurrency(project.cost) : <span style={{ color: 'var(--text-dim)' }}>—</span>}</td>
                          <td>{project.calcCost > 0 ? <span style={{ color: 'var(--accent4)' }}>{formatCurrency(project.calcCost)}</span> : <span style={{ color: 'var(--text-dim)' }}>—</span>}</td>
                          <td title={project.models.join(', ')}>
                            {project.models.length === 0
                              ? <span style={{ color: 'var(--text-dim)' }}>—</span>
                              : project.models.length === 1
                                ? renderModel(project.models[0])
                                : `${renderModel(project.models[0])} +${project.models.length - 1}`}
                          </td>
                          <td>{project.errorCount > 0
                            ? <span style={{ color: 'var(--accent3, #f38ba8)' }}>{project.errorCount}</span>
                            : <span style={{ color: 'var(--text-dim)' }}>—</span>}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
                {metrics.totalProjects > PROJECT_LOG_PAGE_SIZE && (
                  <div className="metrics-pagination">
                    <button
                      className="oc-time-range-btn"
                      disabled={projectLogPage === 0}
                      onClick={() => setProjectLogPage((p) => p - 1)}
                    >Prev</button>
                    <span className="metrics-pagination-info">
                      Page {projectLogPage + 1} / {Math.ceil(metrics.totalProjects / PROJECT_LOG_PAGE_SIZE)}
                    </span>
                    <button
                      className="oc-time-range-btn"
                      disabled={(projectLogPage + 1) * PROJECT_LOG_PAGE_SIZE >= metrics.totalProjects}
                      onClick={() => setProjectLogPage((p) => p + 1)}
                    >Next</button>
                  </div>
                )}
              </>
            )}

            {logTab === 'request' && (
              <>
                <div className="metrics-table-wrap">
                  <table>
                    <thead>
                      <tr>
                        <th>#</th>
                        <th>Time</th>
                        <th>Session</th>
                        <th>Model</th>
                        <th>Input</th>
                        <th>Output</th>
                        <th>Cache</th>
                        <th>Tok/s</th>
                        <th>Duration</th>
                        <th>Cost</th>
                        <th title="Calculated from token counts using public API pricing">Est. Cost</th>
                        <th>Stop</th>
                      </tr>
                    </thead>
                    <tbody>
                      {metrics.requests.length === 0 ? (
                        <tr>
                          <td colSpan={12} style={{ textAlign: 'center', color: 'var(--text-dim)', padding: 24 }}>
                            No requests matched the current filters
                          </td>
                        </tr>
                      ) : metrics.requests.map((request, idx) => (
                        <tr key={request.id} onClick={() => navigate(`/session/${encodeURIComponent(request.sessionId)}`)}>
                          <td>{logPage * LOG_PAGE_SIZE + idx + 1}</td>
                          <td>{formatDateTimeShort(request.timeCreated)}</td>
                          <td className="mono">{shortSessionID(request.sessionId)}</td>
                          <td>{renderModel(request.model)}</td>
                          <td>{formatNumber(request.inputTokens)}</td>
                          <td>{formatNumber(request.outputTokens)}</td>
                          <td className="mono">{formatTokenCache(request.cacheReadTokens, request.cacheWriteTokens)}</td>
                          <td>{request.tokensPerSecond > 0 ? request.tokensPerSecond.toFixed(1) : '-'}</td>
                          <td>{request.durationMs > 0 ? formatSeconds(request.durationMs / 1000) : '-'}</td>
                          <td>{request.cost > 0 ? formatCurrency(request.cost) : <span style={{ color: 'var(--text-dim)' }}>—</span>}</td>
                          <td>{request.calcCost > 0 ? <span style={{ color: 'var(--accent4)' }}>{formatCurrency(request.calcCost)}</span> : <span style={{ color: 'var(--text-dim)' }}>—</span>}</td>
                          <td><span className="metrics-stop-pill">{request.stopReason}</span></td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
                {metrics.totalRequests > LOG_PAGE_SIZE && (
                  <div className="metrics-pagination">
                    <button
                      className="oc-time-range-btn"
                      disabled={logPage === 0}
                      onClick={() => setLogPage((p) => p - 1)}
                    >Prev</button>
                    <span className="metrics-pagination-info">
                      Page {logPage + 1} / {Math.ceil(metrics.totalRequests / LOG_PAGE_SIZE)}
                    </span>
                    <button
                      className="oc-time-range-btn"
                      disabled={(logPage + 1) * LOG_PAGE_SIZE >= metrics.totalRequests}
                      onClick={() => setLogPage((p) => p + 1)}
                    >Next</button>
                  </div>
                )}
              </>
            )}
          </div>
        </>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Shared sub-components
// ---------------------------------------------------------------------------

function MetricCard({ label, value, subvalue, tone }: { label: string; value: string; subvalue?: string; tone: 'blue' | 'green' | 'purple' | 'orange' }) {
  return (
    <div className="stat-card">
      <div className="label">{label}</div>
      <div className={`value ${tone}`}>{value}</div>
      {subvalue ? <div className="metrics-subvalue">{subvalue}</div> : null}
    </div>
  );
}

function ChartCard({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="chart-card metrics-chart-card">
      <h3>{title}</h3>
      <div className="metrics-chart-body">{children}</div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Usage charts (restored): heatmap + hourly/daily/model breakdowns
// ---------------------------------------------------------------------------

const USAGE_RANGE_OPTIONS = [
  { label: '7 days', value: 7 },
  { label: '30 days', value: 30 },
  { label: '90 days', value: 90 },
  { label: 'All time', value: 0 },
];

export function UsageTab() {
  usePageTitle('Usage');
  const { projects, dirScope, setDirScope } = useDashboard();
  const [selectedModel, setSelectedModel] = useState('');
  const [usageDays, setUsageDays] = useState(30);

  const dir = dirScope || undefined;
  const daysParam = usageDays || undefined;

  // TanStack Query handles cancellation, dedup, and stale-while-revalidate
  // automatically — no manual AbortController needed (Wave 3 / P4+P5 fix).
  // Activity heatmap always shows the full year regardless of the days filter.
  const activityQ = useActivity({ model: selectedModel || undefined, dir });
  const modelsQ = useModels({ days: daysParam, dir });
  const hourlyQ = useHourly({ days: daysParam, dir });
  const hourlyTokensQ = useHourlyTokens({ days: daysParam, model: selectedModel || undefined, dir });

  const activity = activityQ.data ?? [];
  const models = modelsQ.data ?? [];
  const hourly = hourlyQ.data ?? [];
  const hourlyTokens = hourlyTokensQ.data ?? [];
  const loading = activityQ.isLoading || modelsQ.isLoading || hourlyQ.isLoading || hourlyTokensQ.isLoading;

  // Derive available models for the filter dropdown from loaded model usage data.
  const allModels = [...models].sort((a, b) => b.count - a.count);
  const sortedModels = allModels.slice(0, 8);

  return (
    <div className="metrics-page">
      <div className="metrics-filters">
        <ProjectScopePicker projects={projects} value={dirScope} onChange={setDirScope} />
        <label className="metrics-filter">
          <span>Model</span>
          <select value={selectedModel} onChange={(e) => setSelectedModel(e.target.value)}>
            <option value="">All models</option>
            {allModels.map((m) => {
              const key = `${m.provider}/${m.model}`;
              return <option key={key} value={key}>{m.model}</option>;
            })}
          </select>
        </label>
        <label className="metrics-filter metrics-filter-small">
          <span>Last</span>
          <select value={usageDays} onChange={(e) => setUsageDays(Number(e.target.value))}>
            {USAGE_RANGE_OPTIONS.map((opt) => (
              <option key={opt.value} value={opt.value}>{opt.label}</option>
            ))}
          </select>
        </label>
      </div>

      {activity.length > 0 && <HeatmapChart activity={activity} />}

      {loading ? (
        <div className="oc-list-loading">
          <div className="oc-spinner" />
          Loading usage charts...
        </div>
      ) : (
        <>
      <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr', gap: 16, marginBottom: 24 }}>
        <div className="chart-card metrics-chart-card">
          <h3>Daily Messages</h3>
          <div className="metrics-chart-body">
            <Bar data={{
              labels: activity.map((d) => d.date.slice(5)),
              datasets: [
                { label: 'User Prompts', data: activity.map((d) => d.userMessages), backgroundColor: 'rgba(166, 227, 161, 0.6)', borderRadius: 2 },
                { label: 'Assistant Turns', data: activity.map((d) => d.messages), backgroundColor: 'rgba(137, 180, 250, 0.6)', borderRadius: 2 },
              ],
            }} options={BAR_OPTIONS_SESSIONS} />
          </div>
        </div>
        <div className="chart-card metrics-chart-card">
          <h3>Model Usage</h3>
          <div className="metrics-chart-body">
            <Doughnut data={{
              labels: sortedModels.map((m) => m.model),
              datasets: [{ data: sortedModels.map((m) => m.count), backgroundColor: CHART_COLORS, borderWidth: 0 }],
            }} options={{ responsive: true, maintainAspectRatio: false, animation: false, plugins: { legend: { position: 'bottom', labels: { color: '#bac2de', boxWidth: 12, padding: 8, font: { size: 11 } } } } }} />
          </div>
        </div>
      </div>

      {hourlyTokens.length > 0 && <HourlyTokensChart data={hourlyTokens} />}

      <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr', gap: 16, marginBottom: 32 }}>
        <div className="chart-card metrics-chart-card">
          <h3>Sessions by Hour of Day</h3>
          <div className="metrics-chart-body">
            <Bar data={{
              labels: hourly.map((h) => h.hour + ':00'),
              datasets: [{ label: 'Sessions', data: hourly.map((h) => h.sessions), backgroundColor: hourly.map((h) => h.sessions > 0 ? 'rgba(166, 227, 161, 0.6)' : 'rgba(166, 227, 161, 0.1)'), borderRadius: 2 }],
            }} options={BAR_OPTIONS_HOURLY} />
          </div>
        </div>
        <div className="chart-card metrics-chart-card">
          <h3>Tokens by Model</h3>
          <div className="metrics-chart-body">
            <Bar data={{
              labels: sortedModels.map((m) => m.model.length > 20 ? m.model.slice(0, 20) + '...' : m.model),
              datasets: [
                { label: 'Input', data: sortedModels.map((m) => m.tokensIn), backgroundColor: 'rgba(137, 180, 250, 0.6)', borderRadius: 2 },
                { label: 'Output', data: sortedModels.map((m) => m.tokensOut), backgroundColor: 'rgba(203, 166, 247, 0.6)', borderRadius: 2 },
              ],
            }} options={BAR_OPTIONS_TOKENS_BY_MODEL} />
          </div>
        </div>
      </div>
        </>
      )}
    </div>
  );
}

function HourlyTokensChart({ data }: { data: HourlyTokensByModel[] }) {
  const slots: string[] = [];
  const now = new Date();
  for (let i = 7 * 24 - 1; i >= 0; i--) {
    const d = new Date(now.getTime() - i * 3_600_000);
    slots.push(`${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')} ${String(d.getHours()).padStart(2, '0')}`);
  }

  const modelTotals = new Map<string, number>();
  for (const d of data) {
    const key = `${d.provider}/${d.model}`;
    modelTotals.set(key, (modelTotals.get(key) ?? 0) + d.tokensIn + d.tokensOut);
  }
  const topModels = [...modelTotals.entries()].sort((a, b) => b[1] - a[1]).slice(0, 8).map(([k]) => k);

  const lookup = new Map<string, Map<string, number>>();
  for (const d of data) {
    const key = `${d.provider}/${d.model}`;
    if (!topModels.includes(key)) continue;
    if (!lookup.has(key)) lookup.set(key, new Map());
    const dtMap = lookup.get(key)!;
    dtMap.set(d.datetime, (dtMap.get(d.datetime) ?? 0) + d.tokensIn + d.tokensOut);
  }

  const datasets = topModels.map((key, idx) => {
    const dtMap = lookup.get(key) ?? new Map();
    const label = key.includes('/') ? key.split('/').pop()! : key;
    return {
      label: label.length > 25 ? label.slice(0, 25) + '...' : label,
      data: slots.map((s) => dtMap.get(s) ?? 0),
      backgroundColor: CHART_COLORS[idx % CHART_COLORS.length],
      borderRadius: 1,
    };
  });

  const labels = slots.map((s) => {
    const hour = s.slice(11, 13);
    if (hour === '00') {
      const d = new Date(s.replace(' ', 'T') + ':00:00');
      return `${['Sun','Mon','Tue','Wed','Thu','Fri','Sat'][d.getDay()]} ${d.getDate()}`;
    }
    return '';
  });

  return (
    <div className="chart-card metrics-chart-card" style={{ marginBottom: 24 }}>
      <h3>Tokens per Hour by Model (last 7 days)</h3>
      <div className="metrics-chart-body">
        <Bar data={{ labels, datasets }} options={{
          ...BAR_OPTIONS_HOURLY_TOKENS,
          plugins: {
            ...BAR_OPTIONS_HOURLY_TOKENS.plugins,
            tooltip: {
              callbacks: {
                title: (items) => { const idx = items[0]?.dataIndex; return idx != null ? `${slots[idx].slice(0, 10)} ${slots[idx].slice(11)}:00` : ''; },
                label: (ctx) => `${ctx.dataset.label}: ${formatCompactNumber(ctx.raw as number)} tokens`,
              },
            },
          },
          scales: {
            ...BAR_OPTIONS_HOURLY_TOKENS.scales,
            x: { ...BAR_OPTIONS_HOURLY_TOKENS.scales.x, ticks: { maxRotation: 0, autoSkip: false, callback: (_: unknown, idx: number) => labels[idx] || null } },
          },
        }} />
      </div>
    </div>
  );
}

const CELL = 13;
const GAP = 2;
const DOW_W = 36; // width reserved for Mon/Wed/Fri labels

function HeatmapChart({ activity }: { activity: ActivityDay[] }) {
  const [tooltip, setTooltip] = useState<{ text: string; x: number; y: number } | null>(null);

  const maxMessages = Math.max(...activity.map((d) => d.messages), 1);
  const totalMessages = activity.reduce((s, d) => s + d.messages, 0);

  // Build week columns: each column is Sun→Sat (index 0–6).
  // The first day of the data determines how many leading nulls to pad.
  const weeks: (ActivityDay | null)[][] = [];
  if (activity.length > 0) {
    const first = new Date(activity[0].date + 'T00:00:00');
    let col: (ActivityDay | null)[] = Array(first.getDay()).fill(null);
    for (const day of activity) {
      col.push(day);
      if (col.length === 7) { weeks.push(col); col = []; }
    }
    if (col.length > 0) {
      while (col.length < 7) col.push(null);
      weeks.push(col);
    }
  }

  // Month labels: emit a label at the first week-column that starts a new month.
  const monthLabels: { wi: number; label: string }[] = [];
  let lastMonth = -1;
  weeks.forEach((week, wi) => {
    const firstReal = week.find((d) => d !== null);
    if (firstReal) {
      const m = new Date(firstReal.date + 'T00:00:00').getMonth();
      if (m !== lastMonth) {
        monthLabels.push({ wi, label: new Date(firstReal.date + 'T00:00:00').toLocaleString('en-US', { month: 'short' }) });
        lastMonth = m;
      }
    }
  });

  const level = (day: ActivityDay) => {
    if (day.messages === 0) return 0;
    return Math.min(4, Math.ceil(day.messages / maxMessages * 4));
  };

  const LEGEND_COLORS = [
    'var(--heatmap-1)',
    'var(--heatmap-2)',
    'var(--heatmap-3)',
    'var(--heatmap-4)',
  ];

  return (
    <div className="chart-card heatmap-card">
      {/* Month labels row */}
      <div className="heatmap-months" style={{ paddingLeft: DOW_W }}>
        {monthLabels.map(({ wi, label }) => (
          <div key={wi} className="heatmap-month-label" style={{ left: wi * (CELL + GAP) }}>
            {label}
          </div>
        ))}
      </div>

      {/* Grid */}
      <div style={{ display: 'flex', gap: 0 }}>
        {/* Day-of-week labels: Mon, Wed, Fri */}
        <div className="heatmap-dow" style={{ width: DOW_W }}>
          {['Sun','Mon','Tue','Wed','Thu','Fri','Sat'].map((d, i) => (
            <div key={i} className="heatmap-dow-label" style={{ height: CELL, marginBottom: i < 6 ? GAP : 0 }}>
              {(i === 1 || i === 3 || i === 5) ? d : ''}
            </div>
          ))}
        </div>

        {/* Week columns */}
        <div style={{ display: 'flex', gap: GAP, overflow: 'hidden' }}>
          {weeks.map((week, wi) => (
            <div key={wi} style={{ display: 'flex', flexDirection: 'column', gap: GAP }}>
              {week.map((day, di) =>
                day === null ? (
                  <div key={di} style={{ width: CELL, height: CELL }} />
                ) : (
                  <div
                    key={di}
                    className="heatmap-day"
                    data-level={level(day)}
                    style={{ width: CELL, height: CELL }}
                    onMouseEnter={(e) => setTooltip({
                      text: `${day.date}: ${day.messages} messages, ${day.sessions} sessions`,
                      x: e.clientX + 12,
                      y: e.clientY - 36,
                    })}
                    onMouseLeave={() => setTooltip(null)}
                  />
                )
              )}
            </div>
          ))}
        </div>
      </div>

      {/* Footer: summary + legend */}
      <div className="heatmap-footer">
        <span className="heatmap-summary">
          {totalMessages.toLocaleString()} messages in the last 12 months
        </span>
        <span className="heatmap-legend">
          <span className="heatmap-legend-label">Less</span>
          {LEGEND_COLORS.map((c, i) => (
            <span key={i} className="heatmap-legend-cell" style={{ background: c }} />
          ))}
          <span className="heatmap-legend-label">More</span>
        </span>
      </div>

      {tooltip && (
        <div className="heatmap-tooltip" style={{ left: tooltip.x, top: tooltip.y }}>
          {tooltip.text}
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Settings tab
// ---------------------------------------------------------------------------

export function SettingsTab() {
  usePageTitle('Settings');
  const bellEnabled = useUiStore((s) => s.bellEnabled);
  const setBellEnabled = useUiStore((s) => s.setBellEnabled);
  const notificationsEnabled = useUiStore((s) => s.notificationsEnabled);
  const setNotificationsEnabled = useUiStore((s) => s.setNotificationsEnabled);
  const authRequired = useAuthStore((s) => s.authRequired);
  const logout = useAuthStore((s) => s.logout);
  const { canInstall, installed, promptInstall } = usePwaInstall();

  // System notification state. Tracked locally so we can re-render
  // when permission changes (the browser API doesn't give us an event
  // for that, so we read it on each render and update after a request).
  const [notifPermission, setNotifPermission] = useState<NotificationPermission | 'unsupported'>(
    () => {
      if (!notificationsSupported()) return 'unsupported';
      return Notification.permission;
    },
  );

  const notifSupported = notifPermission !== 'unsupported';
  const notifBlocked = notifPermission === 'denied';

  async function handleNotificationsToggle(want: boolean) {
    if (!want) {
      setNotificationsEnabled(false);
      return;
    }
    // Turning on: ensure permission is granted first. If the user
    // previously denied it, the browser won't re-prompt — surface that
    // explicitly so the toggle doesn't silently fail.
    if (notifPermission === 'granted') {
      setNotificationsEnabled(true);
      return;
    }
    const result = await requestNotificationPermission();
    setNotifPermission(result);
    setNotificationsEnabled(result === 'granted');
  }

  // The "App" section only renders when there's something actionable
  // to show: an install button (Chromium, not yet installed) or an
  // "already installed" confirmation. On Safari/Firefox or before the
  // browser has decided the page is installable the section is hidden
  // entirely, keeping the settings page tidy.
  const showAppSection = canInstall || installed;

  return (
    <div className="settings-page">
      <div className="settings-section">
        <h2 className="settings-section-title">Notifications</h2>
        {notifSupported && (
          <div className="settings-row">
            <div className="settings-row-info">
              <div className="settings-row-label">System notifications</div>
              <div className="settings-row-desc">
                {notifBlocked
                  ? 'Notifications are blocked by your browser. Allow them in your browser\u2019s site settings to enable this option.'
                  : 'Show a desktop notification when a session finishes or needs your input. Works best after installing ocman as an app.'}
              </div>
            </div>
            <label className="settings-toggle">
              <input
                type="checkbox"
                checked={notificationsEnabled && notifPermission === 'granted'}
                disabled={notifBlocked}
                onChange={(e) => { void handleNotificationsToggle(e.target.checked); }}
              />
              <span className="settings-toggle-track" />
            </label>
          </div>
        )}
        <div className="settings-row">
          <div className="settings-row-info">
            <div className="settings-row-label">Bell sound</div>
            <div className="settings-row-desc">
              Play a bell sound when the app is not in focus and a session
              finishes or asks a question.
            </div>
          </div>
          <label className="settings-toggle">
            <input
              type="checkbox"
              checked={bellEnabled}
              onChange={(e) => setBellEnabled(e.target.checked)}
            />
            <span className="settings-toggle-track" />
          </label>
        </div>
      </div>

      {showAppSection && (
        <div className="settings-section">
          <h2 className="settings-section-title">App</h2>
          <div className="settings-row">
            <div className="settings-row-info">
              <div className="settings-row-label">Install ocman</div>
              <div className="settings-row-desc">
                {installed
                  ? 'ocman is installed as an app on this device. Launch it from your dock or app launcher to use it in its own window.'
                  : 'Install ocman as a standalone app with its own window and dock icon. The web version keeps working in any browser tab.'}
              </div>
            </div>
            <button
              type="button"
              className="vscode-btn"
              disabled={installed || !canInstall}
              onClick={() => { void promptInstall(); }}
            >
              {installed ? 'Installed' : 'Install'}
            </button>
          </div>
        </div>
      )}

      {authRequired && (
        <div className="settings-section">
          <h2 className="settings-section-title">Account</h2>
          <div className="settings-row">
            <div className="settings-row-info">
              <div className="settings-row-label">Session</div>
              <div className="settings-row-desc">Sign out of the current session.</div>
            </div>
            <button
              type="button"
              className="vscode-btn"
              onClick={() => { void logout(); }}
            >
              Sign out
            </button>
          </div>
        </div>
      )}
    </div>
  );
}


