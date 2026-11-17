/**
 * Shared analytics charts and the three log-table presentations.
 */
import { Link, useNavigate } from 'react-router-dom';
import { Bar, Doughnut, Line } from 'react-chartjs-2';
import './chartSetup';
import './StatsLogTables.css';
import type { MetricsPerformance, ProjectLogEntry, RequestMetricsRow, SessionLogEntry } from '../../lib/api';
import {
  cleanTitle,
  formatCompactNumber,
  formatCost,
  formatDateTimeShort,
  formatNumber,
  formatPercent,
  formatSeconds,
  formatTokensPerSecond,
  relativeTime,
  renderModel,
  shortSessionID,
} from '../../lib/format';
import { ProjectLabel } from '../../components/ProjectLabel';
import {
  BAR_OPTIONS_TOKS,
  BAR_OPTIONS_DURATION,
  BAR_OPTIONS_WAIT_TIME,
  CHART_COLORS,
  LINE_OPTIONS_CACHE,
  DOUGHNUT_OPTIONS,
  STOP_REASON_COLORS,
} from '../../lib/chartConfig';
import { ChartCard } from './shared';
import { KpiStrip } from './KpiStrip';
import { COST_HELP, cacheBreakdown, costBasis, costNote, errorDetail, tokenBreakdown, tokensProcessed } from './metricsSummary';
import { ModelLabel } from '../../components/ModelLogo';
import { DataTable } from '../../components/DataTable';
import { API_EQUIVALENT_TITLE } from '../../components/ApiEquivalentCost';
import { EmptyState } from '../../components/EmptyState';

const dim = { color: 'var(--text-dim)' } as const;
const dash = <span style={dim}>—</span>;

/** Headline request figures on the analytics overview. */
export function MetricsSummaryCards({ metrics }: { metrics: MetricsPerformance }) {
  const s = metrics.summary;
  return <KpiStrip label="Request summary" items={[
    { label: 'Requests', value: formatNumber(s.requests), detail: errorDetail(s.errorRequests, s.completedRequests) },
    { label: 'Tokens', value: formatCompactNumber(tokensProcessed(s)), detail: tokenBreakdown(s), help: 'All tokens the model processed, including cache reads and writes.' },
    { label: 'Cache hit rate', value: formatPercent(s.cacheHitRate), detail: cacheBreakdown(s.cacheReadTokens, s.cacheWriteTokens), help: 'Share of input tokens served from the prompt cache.' },
    { label: 'Cost', value: formatCost(s.totalEffectiveCost), detail: costNote(s.totalCost, s.totalEffectiveCost), help: COST_HELP },
    { label: 'Avg response', value: formatSeconds(s.avgDurationMs / 1000), detail: `${formatTokensPerSecond(s.avgTokensPerSec)} tok/s output` },
    { label: 'Response time', value: formatSeconds(s.totalDurationMs / 1000), detail: 'Sum of response times', help: 'Total time spent answering requests. Parallel requests add together, so this can exceed elapsed time.' },
  ]} />;
}

export function PerformanceSummaryCards({ metrics }: { metrics: MetricsPerformance }) {
  const s = metrics.summary;
  return <KpiStrip label="Performance summary" items={[
    { label: 'Completed', value: formatNumber(s.completedRequests), detail: `${formatNumber(s.successfulRequests)} succeeded` },
    { label: 'Error rate', value: formatPercent(s.errorRate), detail: errorDetail(s.errorRequests, s.completedRequests) },
    { label: 'P50 latency', value: formatSeconds(s.p50DurationMs / 1000), detail: 'Median response' },
    { label: 'P95 latency', value: formatSeconds(s.p95DurationMs / 1000), detail: 'Slowest 5% start here' },
    { label: 'Cost per success', value: formatCost(s.costPerSuccessfulRequest), detail: costNote(s.totalCost, s.totalEffectiveCost), help: COST_HELP },
  ]} />;
}

export function PerformanceCharts({ metrics }: { metrics: MetricsPerformance }) {
  const metricLabels = metrics.series.map((point) => point.label);
  return (
    <>
      <div className="metrics-chart-grid">
        <ChartCard title="Waiting Time by Agent" bodyHeight={Math.max(140, 56 * metrics.agents.length + 72)}>
          {metrics.agents.some((agent) => agent.totalDurationMs > 0) ? <Bar
            role="img"
            aria-label="Total waiting time by agent, split into agent response, tools, and unknown timing"
            data={{
              labels: metrics.agents.map((agent) => agent.agent || 'Unknown agent'),
              datasets: [
                { label: 'Agent response', data: metrics.agents.map((agent) => agent.agentDurationMs / 1000), backgroundColor: CHART_COLORS[0] },
                { label: 'Tools', data: metrics.agents.map((agent) => agent.toolDurationMs / 1000), backgroundColor: CHART_COLORS[1] },
                { label: 'Unknown timing', data: metrics.agents.map((agent) => agent.unknownDurationMs / 1000), backgroundColor: CHART_COLORS[3] },
              ],
            }}
            options={BAR_OPTIONS_WAIT_TIME}
          /> : <EmptyState>No recorded waiting time for these filters.</EmptyState>}
        </ChartCard>
      </div>
      <div className="analytics-scope-note">Summed request time, not elapsed session time. Parallel tools count once per request. Tool time includes permission and question waits; incomplete timings remain unknown.</div>
      <div className="metrics-chart-grid metrics-chart-grid-top">
        <ChartCard title="Avg Output Tokens/Second">
          <Bar data={{
            labels: metricLabels,
            datasets: [{ label: 'Tok/s', data: metrics.series.map((point) => point.avgOutputTokensSec), backgroundColor: 'rgba(249, 226, 175, 0.7)', borderRadius: 2 }],
          }} options={BAR_OPTIONS_TOKS} />
        </ChartCard>

        <ChartCard title="Request Latency (s)">
          <Bar data={{
            labels: metricLabels,
            datasets: [
              { label: 'P50', data: metrics.series.map((point) => point.p50DurationMs / 1000), backgroundColor: 'rgba(137, 180, 250, 0.65)', borderRadius: 2 },
              { label: 'P95', data: metrics.series.map((point) => point.p95DurationMs / 1000), backgroundColor: 'rgba(203, 166, 247, 0.55)', borderRadius: 2 },
            ],
          }} options={BAR_OPTIONS_DURATION} />
        </ChartCard>

        <ChartCard title="Error Rate">
          <Line data={{
            labels: metricLabels,
            datasets: [{ label: 'Errors', data: metrics.series.map((point) => point.errorRate * 100), borderColor: '#f38ba8', backgroundColor: 'rgba(243, 139, 168, 0.15)', fill: true, tension: 0.25, pointRadius: 0 }],
          }} options={LINE_OPTIONS_CACHE} />
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
    </>
  );
}

// Shared numeric/cost/models cells common to the session and project
// rows (everything except the leading identity columns and the trailing
// error count, which differ per entity).
function MetricsRowCells({ e }: { e: SessionOrProject }) {
  return (
    <>
      <td className="num" data-label="Requests">{formatNumber(e.requests)}</td>
      <td className="num metrics-secondary" data-label="Input">{formatNumber(e.inputTokens)}</td>
      <td className="num" data-label="Output">{formatNumber(e.outputTokens)}</td>
      <td className="num metrics-secondary" data-label="Cache read" title={cacheBreakdown(e.cacheReadTokens, e.cacheWriteTokens)}>{formatCompactNumber(e.cacheReadTokens)}</td>
      <td className="num metrics-secondary" data-label="Tok/s">{e.avgTokensPerSec > 0 ? e.avgTokensPerSec.toFixed(1) : dash}</td>
      <td className="num" data-label="Duration">{e.totalDurationMs > 0 ? formatSeconds(e.totalDurationMs / 1000) : dash}</td>
      <td className="num" data-label="Cost"><CostCell reported={e.cost} effective={e.effectiveCost} /></td>
      <td className="metrics-models" data-label="Models">{renderModels(e.models)}</td>
      <td className="num" data-label="Errors">{e.errorCount > 0
        ? <span style={{ color: 'var(--danger)' }}>{e.errorCount}</span>
        : dash}</td>
    </>
  );
}

/**
 * One cost figure per row; "≈" marks an API-equivalent estimate (the
 * platform billed $0), explained in the column header and hover text.
 */
function CostCell({ reported, effective }: { reported: number; effective: number }) {
  const basis = costBasis(reported, effective);
  if (basis === 'none') return dash;
  if (basis === 'billed') return <span title="Billed by the provider">{formatCost(effective)}</span>;
  return <span title={`${API_EQUIVALENT_TITLE}${basis === 'mixed' ? ` (${formatCost(reported)} billed)` : ''}`}><span className="metrics-approx" aria-label="approximately">≈</span>{formatCost(effective)}</span>;
}

const COST_HEADER = <th className="num" title="Billed cost; ≈ marks an API-equivalent estimate where the platform billed $0 (subscription)">Cost</th>;

function renderModels(models: string[]) {
  if (models.length === 0) return dash;
  if (models.length === 1) return <ModelLabel model={models[0]}>{renderModel(models[0])}</ModelLabel>;
  return (
    <span className="metrics-model-list" tabIndex={0}>
      <ModelLabel model={models[0]}>{renderModel(models[0])} +{models.length - 1}</ModelLabel>
      <span className="metrics-model-list-tooltip" role="tooltip">
        {models.map((model) => <ModelLabel key={model} model={model}>{renderModel(model)}</ModelLabel>)}
      </span>
    </span>
  );
}

// Common shape of the numeric fields shared by session and project rows.
type SessionOrProject = {
  requests: number;
  inputTokens: number;
  outputTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  avgTokensPerSec: number;
  totalDurationMs: number;
  effectiveCost: number;
  cost: number;
  calcCost: number;
  models: string[];
  errorCount: number;
};

const ENTITY_HEADERS = (
  <>
    <th className="num">Requests</th>
    <th className="num metrics-secondary">Input</th>
    <th className="num">Output</th>
    <th className="num metrics-secondary" title="Tokens read from the prompt cache; hover a value for cache writes">Cache read</th>
    <th className="num metrics-secondary" title="Estimated output tokens per model-request second, excluding recorded tool and approval waits; includes startup latency.">Tok/s</th>
    <th className="num">Duration</th>
    {COST_HEADER}
    <th>Models</th>
    <th className="num" title="Assistant messages that finished with an error">Errors</th>
  </>
);

// "1–20 of 137" range indicator for the active log tab. Renders
// nothing when the tab has no rows.
export function LogRange({ page, pageSize, total }: { page: number; pageSize: number; total: number }) {
  if (total <= 0) return null;
  return <>{page * pageSize + 1}–{Math.min((page + 1) * pageSize, total)} of {total}</>;
}

function EmptyRow({ colSpan, label }: { colSpan: number; label: string }) {
  return (
    <tr>
      <td colSpan={colSpan} style={{ textAlign: 'center', color: 'var(--text-dim)', padding: 24 }}>
        {label}
      </td>
    </tr>
  );
}

export function SessionLogTable({
  sessions,
  pageOffset,
}: {
  sessions: SessionLogEntry[];
  pageOffset: number;
}) {
  const navigate = useNavigate();
  return (
    <div className="metrics-table-wrap">
      <DataTable className="metrics-log-table">
        <thead>
          <tr>
            <th className="metrics-sticky">Title</th>
            <th>Last Activity</th>
            {ENTITY_HEADERS}
          </tr>
        </thead>
        <tbody>
          {sessions.length === 0 ? (
            <EmptyRow colSpan={11} label="No sessions matched the current filters" />
          ) : sessions.map((session, idx) => (
            <tr key={session.id} onClick={() => navigate(`/session/${encodeURIComponent(session.id)}`)}>
              <td className="metrics-sticky" title={cleanTitle(session.title)}>
                <span className="metrics-rank">{pageOffset + idx + 1}</span>
                {/* The row-wide onClick is mouse-only; this link is the
                    keyboard/AT path into the session. */}
                <Link className="metrics-row-link" to={`/session/${encodeURIComponent(session.id)}`}>
                  <div>{cleanTitle(session.title) || <span style={dim}>untitled</span>}</div>
                  {session.directory && <ProjectLabel className="metrics-session-project" path={session.directory} />}
                </Link>
              </td>
              {session.lastRequestTime > 0
                ? <td data-label="Last activity" title={formatDateTimeShort(session.lastRequestTime)}>{relativeTime(session.lastRequestTime)}</td>
                : <td data-label="Last activity">{dash}</td>}
              <MetricsRowCells e={session} />
            </tr>
          ))}
        </tbody>
      </DataTable>
    </div>
  );
}

export function ProjectLogTable({
  projects,
  pageOffset,
}: {
  projects: ProjectLogEntry[];
  pageOffset: number;
}) {
  const navigate = useNavigate();
  return (
    <div className="metrics-table-wrap">
      <DataTable className="metrics-log-table">
        <thead>
          <tr>
            <th className="metrics-sticky">Project</th>
            <th className="num">Sessions</th>
            {ENTITY_HEADERS}
          </tr>
        </thead>
        <tbody>
          {projects.length === 0 ? (
            <EmptyRow colSpan={11} label="No projects matched the current filters" />
          ) : projects.map((project: ProjectLogEntry, idx: number) => (
            <tr key={project.directory} onClick={() => navigate(`/project/${encodeURIComponent(project.directory)}`)}>
              <td className="metrics-sticky" title={project.directory}>
                <span className="metrics-rank">{pageOffset + idx + 1}</span>
                <Link className="metrics-row-link" to={`/project/${encodeURIComponent(project.directory)}`}>
                  <ProjectLabel path={project.directory} style={{ color: 'var(--accent)', fontWeight: 500 }} />
                </Link>
              </td>
              <td className="num" data-label="Sessions">{formatNumber(project.sessions)}</td>
              <MetricsRowCells e={project} />
            </tr>
          ))}
        </tbody>
      </DataTable>
    </div>
  );
}

export function RequestLogTable({
  requests,
  pageOffset,
}: {
  requests: RequestMetricsRow[];
  pageOffset: number;
}) {
  const navigate = useNavigate();
  return (
    <div className="metrics-table-wrap">
      <DataTable className="metrics-log-table">
        <thead>
          <tr>
            <th className="metrics-sticky">Time</th>
            <th>Session</th>
            <th>Model</th>
            <th className="num metrics-secondary">Input</th>
            <th className="num">Output</th>
            <th className="num metrics-secondary" title="Tokens read from the prompt cache; hover a value for cache writes">Cache read</th>
            <th className="num metrics-secondary" title="Estimated output tokens per model-request second, excluding recorded tool and approval waits; includes startup latency.">Tok/s</th>
            <th className="num">Duration</th>
            {COST_HEADER}
            <th>Stop</th>
          </tr>
        </thead>
        <tbody>
          {requests.length === 0 ? (
            <EmptyRow colSpan={10} label="No requests matched the current filters" />
          ) : requests.map((request, idx) => (
            <tr key={JSON.stringify([request.sessionId, request.id])} onClick={() => navigate(`/session/${encodeURIComponent(request.sessionId)}`)}>
              <td className="metrics-sticky"><span className="metrics-rank">{pageOffset + idx + 1}</span>{request.timeCreated > 0 ? formatDateTimeShort(request.timeCreated) : dash}</td>
              <td className="mono" data-label="Session">
                <Link className="metrics-row-link" to={`/session/${encodeURIComponent(request.sessionId)}`}>
                  {shortSessionID(request.sessionId)}
                </Link>
              </td>
              <td data-label="Model"><ModelLabel model={request.model}>{renderModel(request.model)}</ModelLabel></td>
              <td className="num metrics-secondary" data-label="Input">{formatNumber(request.inputTokens)}</td>
              <td className="num" data-label="Output">{formatNumber(request.outputTokens)}</td>
              <td className="num metrics-secondary" data-label="Cache read" title={cacheBreakdown(request.cacheReadTokens, request.cacheWriteTokens)}>{formatCompactNumber(request.cacheReadTokens)}</td>
              <td className="num metrics-secondary" data-label="Tok/s">{request.tokensPerSecond > 0 ? request.tokensPerSecond.toFixed(1) : dash}</td>
              <td className="num" data-label="Duration">{request.durationMs > 0 ? formatSeconds(request.durationMs / 1000) : dash}</td>
              <td className="num" data-label="Cost"><CostCell reported={request.cost} effective={request.effectiveCost} /></td>
              <td data-label="Stop"><span className="metrics-stop-pill">{request.stopReason}</span></td>
            </tr>
          ))}
        </tbody>
      </DataTable>
    </div>
  );
}
