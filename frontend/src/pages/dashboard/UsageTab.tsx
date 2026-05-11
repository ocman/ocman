import { useState } from 'react';
import { Bar, Doughnut } from 'react-chartjs-2';
import type { ActivityDay, HourlyTokensByModel } from '../../lib/api';
import { formatCompactNumber } from '../../lib/format';
import {
  BAR_OPTIONS_SESSIONS,
  BAR_OPTIONS_HOURLY,
  BAR_OPTIONS_HOURLY_TOKENS,
  BAR_OPTIONS_TOKENS_BY_MODEL,
  CHART_COLORS,
} from '../../lib/chartConfig';
import { usePageTitle } from '../../lib/headerContext';
import { ProjectScopePicker } from '../../components/ProjectScopePicker';
import { useActivity, useModels, useHourly, useHourlyTokens } from '../../lib/queries';
import { useDashboard } from './context';

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

// ---------------------------------------------------------------------------
// HourlyTokensChart
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// HeatmapChart
// ---------------------------------------------------------------------------

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
