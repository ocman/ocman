/**
 * The three log tables (session / project / request) rendered inside the
 * Stats tab's log card. Extracted from StatsTab so that component stays
 * within the size budget; the session and project tables share the same
 * 13-column layout via the MetricsRowCells helper.
 */
import { useNavigate } from 'react-router-dom';
import { Bar, Doughnut, Line } from 'react-chartjs-2';
import type { MetricsDashboard, ProjectLogEntry } from '../../lib/api';
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
  LINE_OPTIONS_COST_STACKED,
  LINE_OPTIONS_CACHE,
  DOUGHNUT_OPTIONS,
  CHART_COLORS,
  STOP_REASON_COLORS,
} from '../../lib/chartConfig';
import { MetricCard, ChartCard } from './shared';

const dim = { color: 'var(--text-dim)' } as const;
const dash = <span style={dim}>—</span>;

/**
 * Convert a hex colour (e.g. "#89b4fa") to an rgba string with the
 * given alpha. Used to give each model stack a translucent fill while
 * keeping its border at full opacity.
 */
function hexToRgba(hex: string, alpha: number): string {
  const m = /^#?([a-f\d]{2})([a-f\d]{2})([a-f\d]{2})$/i.exec(hex);
  if (!m) return hex;
  const r = parseInt(m[1], 16);
  const g = parseInt(m[2], 16);
  const b = parseInt(m[3], 16);
  return `rgba(${r}, ${g}, ${b}, ${alpha})`;
}

/**
 * Build the Chart.js datasets for the stacked cumulative cost chart.
 * Falls back to a single "Cost" line when the backend reports no
 * per-model breakdown (empty database, or a window where every row
 * has zero cost — the legacy chart's behaviour).
 */
function buildCostByModelDatasets(metrics: MetricsDashboard) {
  const cbm = metrics.costByModel;
  const series = metrics.series ?? [];
  const models = cbm?.models ?? [];
  const cbmSeries = cbm?.series ?? [];
  if (models.length === 0) {
    return [
      {
        label: 'Cost',
        data: series.map((p) => p.cumulativeEffectiveCost),
        borderColor: '#a6e3a1',
        backgroundColor: 'rgba(166, 227, 161, 0.18)',
        fill: 'origin' as const,
        tension: 0.2,
        pointRadius: 0,
      },
    ];
  }
  return models.map((model, idx) => {
    const colour = CHART_COLORS[idx % CHART_COLORS.length];
    return {
      label: renderModel(model),
      data: cbmSeries.map((pt) => pt.costs?.[idx] ?? 0),
      borderColor: colour,
      backgroundColor: hexToRgba(colour, 0.35),
      fill: true,
      stack: 'cost',
      tension: 0.2,
      pointRadius: 0,
      borderWidth: 1,
    };
  });
}

/**
 * Summary metric cards + the six overview charts shown above the log
 * tables. Pure presentation of a resolved MetricsDashboard.
 */
export function StatsSummaryCharts({ metrics }: { metrics: MetricsDashboard }) {
  const metricLabels = metrics.series.map((point) => point.label);
  return (
    <>
      <div className="metrics-summary-grid">
        <MetricCard label="Requests" value={formatNumber(metrics.summary.requests)} tone="blue" />
        <MetricCard label="Total Tokens" value={formatCompactNumber(metrics.summary.totalTokens)} tone="blue" subvalue={`${formatCompactNumber(metrics.summary.inputTokens)} in / ${formatCompactNumber(metrics.summary.outputTokens)} out`} />
        <MetricCard label="Avg Tok/s" value={metrics.summary.avgTokensPerSec.toFixed(1)} tone="orange" />
        <MetricCard label="Avg Duration" value={formatSeconds(metrics.summary.avgDurationMs / 1000)} tone="blue" />
        <MetricCard label="Total Wall Clock" value={formatSeconds(metrics.summary.totalDurationMs / 1000)} tone="blue" subvalue="sum of response times" />
        <MetricCard label="Cache Hit Rate" value={formatPercent(metrics.summary.cacheHitRate)} tone="green" subvalue={formatTokenCache(metrics.summary.cacheReadTokens, metrics.summary.cacheWriteTokens)} />
        <MetricCard label="Total Cost" value={formatCurrency(metrics.summary.totalEffectiveCost)} tone="green" subvalue="billed, est. when plan reports $0" />
        <MetricCard label="Reported / Est." value={`${formatCurrency(metrics.summary.totalCost)} / ${formatCurrency(metrics.summary.totalCalcCost)}`} tone="orange" subvalue="platform-billed / token estimate" />
      </div>

      <div className="metrics-chart-grid metrics-chart-grid-top">
        <ChartCard title="Avg Output Tokens/Second">
          <Bar data={{
            labels: metricLabels,
            datasets: [{ label: 'Tok/s', data: metrics.series.map((point) => point.avgOutputTokensSec), backgroundColor: 'rgba(249, 226, 175, 0.7)', borderRadius: 2 }],
          }} options={BAR_OPTIONS_TOKS} />
        </ChartCard>

        <ChartCard title="Cumulative Cost by Model (USD)">
          <Line data={{
            labels: metricLabels,
            datasets: buildCostByModelDatasets(metrics),
          }} options={LINE_OPTIONS_COST_STACKED} />
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
    </>
  );
}

// Shared numeric/cost/models cells common to the session and project
// rows (everything except the leading identity columns and the trailing
// error count, which differ per entity).
function MetricsRowCells({ e }: { e: SessionOrProject }) {
  return (
    <>
      <td>{formatNumber(e.requests)}</td>
      <td>{formatNumber(e.inputTokens)}</td>
      <td>{formatNumber(e.outputTokens)}</td>
      <td className="mono">{formatTokenCache(e.cacheReadTokens, e.cacheWriteTokens)}</td>
      <td>{e.avgTokensPerSec > 0 ? e.avgTokensPerSec.toFixed(1) : '-'}</td>
      <td>{e.totalDurationMs > 0 ? formatSeconds(e.totalDurationMs / 1000) : '-'}</td>
      <td>{e.effectiveCost > 0 ? formatCurrency(e.effectiveCost) : dash}</td>
      <td className="metrics-cost-detail">
        <span title="Platform-reported (billed)">{e.cost > 0 ? formatCurrency(e.cost) : dash}</span>
        <span style={dim}> / </span>
        <span title="Token-based estimate" style={{ color: 'var(--accent4)' }}>{e.calcCost > 0 ? formatCurrency(e.calcCost) : '—'}</span>
      </td>
      <td title={e.models.join(', ')}>{renderModels(e.models)}</td>
      <td>{e.errorCount > 0
        ? <span style={{ color: 'var(--accent3, #f38ba8)' }}>{e.errorCount}</span>
        : dash}</td>
    </>
  );
}

function renderModels(models: string[]) {
  if (models.length === 0) return dash;
  if (models.length === 1) return renderModel(models[0]);
  return `${renderModel(models[0])} +${models.length - 1}`;
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
    <th>Requests</th>
    <th>Input</th>
    <th>Output</th>
    <th>Cache</th>
    <th>Tok/s</th>
    <th>Duration</th>
    <th title="Platform-billed cost; falls back to the token-based estimate when the plan reports $0">Cost</th>
    <th title="Platform-reported (billed) / token-based estimate">Reported / Est.</th>
    <th>Models</th>
    <th title="Assistant messages that finished with an error">Errors</th>
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
  metrics,
  pageOffset,
}: {
  metrics: MetricsDashboard;
  pageOffset: number;
}) {
  const navigate = useNavigate();
  return (
    <div className="metrics-table-wrap">
      <table>
        <thead>
          <tr>
            <th>#</th>
            <th>Title</th>
            <th>Last Activity</th>
            {ENTITY_HEADERS}
          </tr>
        </thead>
        <tbody>
          {metrics.sessions.length === 0 ? (
            <EmptyRow colSpan={13} label="No sessions matched the current filters" />
          ) : metrics.sessions.map((session, idx) => (
            <tr key={session.id} onClick={() => navigate(`/session/${encodeURIComponent(session.id)}`)}>
              <td>{pageOffset + idx + 1}</td>
              <td title={cleanTitle(session.title)}>
                <div>{cleanTitle(session.title) || <span style={dim}>untitled</span>}</div>
                {session.directory && <div className="metrics-session-project">{shortPath(session.directory)}</div>}
              </td>
              <td title={formatDateTimeShort(session.lastRequestTime)}>{relativeTime(session.lastRequestTime)}</td>
              <MetricsRowCells e={session} />
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export function ProjectLogTable({
  metrics,
  pageOffset,
}: {
  metrics: MetricsDashboard;
  pageOffset: number;
}) {
  const navigate = useNavigate();
  return (
    <div className="metrics-table-wrap">
      <table>
        <thead>
          <tr>
            <th>#</th>
            <th>Project</th>
            <th>Sessions</th>
            {ENTITY_HEADERS}
          </tr>
        </thead>
        <tbody>
          {metrics.projects.length === 0 ? (
            <EmptyRow colSpan={13} label="No projects matched the current filters" />
          ) : metrics.projects.map((project: ProjectLogEntry, idx: number) => (
            <tr key={project.directory} onClick={() => navigate(`/project/${encodeURIComponent(project.directory)}`)}>
              <td>{pageOffset + idx + 1}</td>
              <td title={project.directory}>
                <span style={{ color: 'var(--accent)', fontWeight: 500 }}>{shortPath(project.directory)}</span>
              </td>
              <td>{formatNumber(project.sessions)}</td>
              <MetricsRowCells e={project} />
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export function RequestLogTable({
  metrics,
  pageOffset,
}: {
  metrics: MetricsDashboard;
  pageOffset: number;
}) {
  const navigate = useNavigate();
  return (
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
            <th title="Platform-billed cost; falls back to the token-based estimate when the plan reports $0">Cost</th>
            <th title="Platform-reported (billed) / token-based estimate">Reported / Est.</th>
            <th>Stop</th>
          </tr>
        </thead>
        <tbody>
          {metrics.requests.length === 0 ? (
            <EmptyRow colSpan={12} label="No requests matched the current filters" />
          ) : metrics.requests.map((request, idx) => (
            <tr key={request.id} onClick={() => navigate(`/session/${encodeURIComponent(request.sessionId)}`)}>
              <td>{pageOffset + idx + 1}</td>
              <td>{formatDateTimeShort(request.timeCreated)}</td>
              <td className="mono">{shortSessionID(request.sessionId)}</td>
              <td>{renderModel(request.model)}</td>
              <td>{formatNumber(request.inputTokens)}</td>
              <td>{formatNumber(request.outputTokens)}</td>
              <td className="mono">{formatTokenCache(request.cacheReadTokens, request.cacheWriteTokens)}</td>
              <td>{request.tokensPerSecond > 0 ? request.tokensPerSecond.toFixed(1) : '-'}</td>
              <td>{request.durationMs > 0 ? formatSeconds(request.durationMs / 1000) : '-'}</td>
              <td>{request.effectiveCost > 0 ? formatCurrency(request.effectiveCost) : dash}</td>
              <td className="metrics-cost-detail">
                <span title="Platform-reported (billed)">{request.cost > 0 ? formatCurrency(request.cost) : dash}</span>
                <span style={dim}> / </span>
                <span title="Token-based estimate" style={{ color: 'var(--accent4)' }}>{request.calcCost > 0 ? formatCurrency(request.calcCost) : '—'}</span>
              </td>
              <td><span className="metrics-stop-pill">{request.stopReason}</span></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
