import { useState, useCallback, useEffect } from 'react';
import { useNavigate } from 'react-router-dom';
import { Bar, Doughnut, Line } from 'react-chartjs-2';
import type { ProjectLogEntry } from '../../lib/api';
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
} from '../../lib/format';
import {
  BAR_OPTIONS_TOKS,
  BAR_OPTIONS_DURATION,
  BAR_OPTIONS_STACKED,
  LINE_OPTIONS_COST,
  LINE_OPTIONS_CACHE,
  DOUGHNUT_OPTIONS,
  STOP_REASON_COLORS,
} from '../../lib/chartConfig';
import { usePageTitle } from '../../lib/headerContext';
import { ProjectScopePicker } from '../../components/ProjectScopePicker';
import { useMetrics } from '../../lib/queries';
import { useDashboard } from './context';
import { MetricCard, ChartCard } from './shared';
import { METRICS_RANGE_OPTIONS } from './constants';

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
