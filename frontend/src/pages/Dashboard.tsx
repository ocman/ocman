import { startTransition, useState, useEffect, useCallback, type ReactNode } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { Chart as ChartJS, CategoryScale, LinearScale, BarElement, ArcElement, Tooltip, Legend, PointElement, LineElement } from 'chart.js';
import { Bar, Doughnut, Line } from 'react-chartjs-2';
import { api } from '../lib/api';
import type { MetricsDashboard, Project, Session } from '../lib/api';
import {
  formatCompactNumber,
  formatCurrency,
  formatDateTimeShort,
  formatNumber,
  formatPercent,
  formatSeconds,
  formatTokenCache,
  relativeTime,
  shortPath,
} from '../lib/format';
import { usePageTitle } from '../lib/headerContext';
import { SessionTable } from '../components/SessionTable';
import { useTmux } from '../lib/useTmux';
import { useApiStore, useApiRequest } from '../lib/apiStore';

ChartJS.register(CategoryScale, LinearScale, BarElement, ArcElement, PointElement, LineElement, Tooltip, Legend);

const STOP_REASON_COLORS = ['#f38ba8', '#a6e3a1', '#f9e2af', '#89b4fa', '#cba6f7', '#fab387', '#94e2d5'];

const METRICS_RANGE_OPTIONS = [
  { label: '24 hours', value: 1 },
  { label: '7 days', value: 7 },
  { label: '30 days', value: 30 },
  { label: '90 days', value: 90 },
  { label: 'All time', value: 0 },
];

export function Dashboard() {
  usePageTitle('Dashboard');
  const navigate = useNavigate();
  const tmux = useTmux();
  const [searchParams, setSearchParams] = useSearchParams();
  const tabParam = searchParams.get('tab');
  const tab = (tabParam === 'projects' || tabParam === 'stats') ? tabParam : 'sessions';
  const setTab = (t: 'sessions' | 'projects' | 'stats') => {
    setSearchParams(t === 'sessions' ? {} : { tab: t }, { replace: true });
  };

  const [sessions, setSessions] = useState<Session[]>([]);
  const [projects, setProjects] = useState<Project[]>([]);
  const [metrics, setMetrics] = useState<MetricsDashboard | null>(null);
  const [timeRange, setTimeRange] = useState(24);
  const [showArchived, setShowArchived] = useState(false);
  const [selectedAgent, setSelectedAgent] = useState('');
  const [selectedModel, setSelectedModel] = useState('');
  const [metricsDays, setMetricsDays] = useState(30);
  const [logPage, setLogPage] = useState(0);
  const LOG_PAGE_SIZE = 20;

  const getSessions = useApiStore((state) => state.getSessions);
  const getProjects = useApiStore((state) => state.getProjects);

  const [metricsLoading, setMetricsLoading] = useState(false);
  const [metricsError, setMetricsError] = useState<string | null>(null);

  const projectsRequest = useApiRequest('projects:get');
  const sessionsRequest = useApiRequest('sessions:get');

  const loadSessions = useCallback(async () => {
    try {
      const since = timeRange > 0 ? Date.now() - timeRange * 60 * 60 * 1000 : undefined;
      setSessions(await getSessions(since ? { since } : {}));
    } catch {
      // error is tracked by useApiRequest
    }
  }, [getSessions, timeRange]);

  useEffect(() => {
    let cancelled = false;

    async function loadInitialData() {
      try {
        const [nextProjects] = await Promise.all([
          getProjects(),
          loadSessions(),
        ]);
        if (cancelled) return;
        setProjects(nextProjects);
      } catch {
        // errors tracked by useApiRequest
      }
    }

    void loadInitialData();
    return () => {
      cancelled = true;
    };
  }, [getProjects, loadSessions]);

  useEffect(() => {
    const id = setInterval(loadSessions, 5000);
    return () => clearInterval(id);
  }, [loadSessions]);

  // Reset to page 0 whenever filters or date range change.
  useEffect(() => {
    setLogPage(0);
  }, [selectedAgent, selectedModel, metricsDays]);

  useEffect(() => {
    if (tab !== 'stats') return;

    let cancelled = false;
    const params = {
      agent: selectedAgent || undefined,
      model: selectedModel || undefined,
      days: metricsDays,
      limit: LOG_PAGE_SIZE,
      offset: logPage * LOG_PAGE_SIZE,
    };

    void (async () => {
      startTransition(() => {
        setMetricsLoading(true);
        setMetricsError(null);
      });
      try {
        const nextMetrics = await api.metrics(params);
        if (cancelled) return;
        startTransition(() => {
          setMetrics(nextMetrics);
          setMetricsLoading(false);
        });
      } catch (err) {
        if (cancelled) return;
        startTransition(() => {
          setMetricsError(err instanceof Error ? err.message : 'Failed to load metrics');
          setMetricsLoading(false);
        });
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [metricsDays, selectedAgent, selectedModel, tab, logPage, LOG_PAGE_SIZE]);

  const metricLabels = metrics?.series.map((point) => point.label) ?? [];

  return (
    <div style={{ flex: 1, overflowY: 'auto', minHeight: 0 }}>
      <div className="nav-tabs">
        <button className={`nav-tab ${tab === 'sessions' ? 'active' : ''}`} onClick={() => setTab('sessions')}>Sessions</button>
        <button className={`nav-tab ${tab === 'projects' ? 'active' : ''}`} onClick={() => setTab('projects')}>Projects</button>
        <button className={`nav-tab ${tab === 'stats' ? 'active' : ''}`} onClick={() => setTab('stats')}>Stats</button>
      </div>

      {sessionsRequest.error && (
        <div className="oc-error-banner">
          {sessionsRequest.error}
          <button onClick={() => loadSessions()}>Retry</button>
        </div>
      )}

      {tab === 'sessions' && (
        <>
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
              onClick={() => setShowArchived((current) => !current)}
            >Include archived</button>
          </div>
          <SessionTable sessions={sessions} showProject loading={sessionsRequest.loading && sessions.length === 0} tmux={tmux} includeArchived={showArchived} />
        </>
      )}

      {tab === 'projects' && (
        projectsRequest.loading && projects.length === 0 ? (
          <div className="oc-list-loading">
            <div className="oc-spinner" />
            Loading projects...
          </div>
        ) : (
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
              {projects.filter((p) => p.sessionCount > 0).length === 0 ? (
                <tr>
                  <td colSpan={5} style={{ textAlign: 'center', color: 'var(--text-dim)', padding: 24 }}>
                    No projects found
                  </td>
                </tr>
              ) : projects.filter((p) => p.sessionCount > 0).map((p) => (
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
        )
      )}

      {tab === 'stats' && (
        <>
          <div className="metrics-filters">
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
                <MetricCard label="Cache Hit Rate" value={formatPercent(metrics.summary.cacheHitRate)} tone="green" subvalue={formatTokenCache(metrics.summary.cacheReadTokens, metrics.summary.cacheWriteTokens)} />
                <MetricCard label="Total Cost" value={formatCurrency(metrics.summary.totalCost)} tone="green" subvalue="stored by OpenCode" />
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
                    datasets: [{ label: 'Cost', data: metrics.series.map((point) => point.cumulativeCost), borderColor: '#a6e3a1', backgroundColor: 'rgba(166, 227, 161, 0.18)', fill: true, tension: 0.2, pointRadius: 0 }],
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
                <h3 style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
                  <span>Request Log</span>
                  <span style={{ fontWeight: 400, fontSize: 12, color: 'var(--text-dim)', textTransform: 'none', letterSpacing: 0 }}>
                    {metrics.totalRequests > 0 && (
                      <>
                        {logPage * LOG_PAGE_SIZE + 1}–{Math.min((logPage + 1) * LOG_PAGE_SIZE, metrics.totalRequests)} of {metrics.totalRequests}
                      </>
                    )}
                  </span>
                </h3>
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
              </div>
            </>
          )}
        </>
      )}
    </div>
  );
}

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

const CHART_X_TICKS = { maxTicksLimit: 10, maxRotation: 45, minRotation: 45 };

const BAR_OPTIONS_TOKS = {
  responsive: true,
  maintainAspectRatio: false,
  animation: false as const,
  plugins: { legend: { display: false } },
  scales: {
    x: { grid: { display: false }, ticks: CHART_X_TICKS },
    y: { beginAtZero: true, ticks: { callback: (v: string | number) => `${v}Tok/s` } },
  },
} as const;

const BAR_OPTIONS_DURATION = {
  responsive: true,
  maintainAspectRatio: false,
  animation: false as const,
  plugins: { legend: { display: false } },
  scales: {
    x: { grid: { display: false }, ticks: CHART_X_TICKS },
    y: { beginAtZero: true, ticks: { callback: (v: string | number) => `${v}s` } },
  },
} as const;

const BAR_OPTIONS_STACKED = {
  responsive: true,
  maintainAspectRatio: false,
  animation: false as const,
  plugins: { legend: { position: 'top' as const, labels: { color: '#bac2de', boxWidth: 12, padding: 12 } } },
  scales: {
    x: { stacked: true, grid: { display: false }, ticks: CHART_X_TICKS },
    y: { stacked: true, beginAtZero: true, ticks: { callback: (v: string | number) => formatCompactNumber(Number(v)) } },
  },
} as const;

const LINE_OPTIONS_COST = {
  responsive: true,
  maintainAspectRatio: false,
  animation: false as const,
  plugins: { legend: { display: false } },
  scales: {
    x: { grid: { display: false }, ticks: CHART_X_TICKS },
    y: { beginAtZero: true, ticks: { callback: (v: string | number) => formatCurrency(Number(v), 2) } },
  },
} as const;

const LINE_OPTIONS_CACHE = {
  responsive: true,
  maintainAspectRatio: false,
  animation: false as const,
  plugins: { legend: { display: false } },
  scales: {
    x: { grid: { display: false }, ticks: CHART_X_TICKS },
    y: { beginAtZero: true, max: 100, ticks: { callback: (v: string | number) => `${Number(v).toFixed(0)}%` } },
  },
} as const;

const DOUGHNUT_OPTIONS = {
  responsive: true,
  maintainAspectRatio: false,
  animation: false as const,
  cutout: '62%',
  plugins: { legend: { position: 'right' as const, labels: { color: '#bac2de', boxWidth: 12, padding: 12 } } },
} as const;

function renderModel(model: string): string {
  if (!model) return 'unknown';
  return model.includes('/') ? model.split('/').slice(-1)[0] : model;
}

function shortSessionID(sessionID: string): string {
  if (sessionID.length <= 12) return sessionID;
  return `${sessionID.slice(0, 4)}...${sessionID.slice(-8)}`;
}
