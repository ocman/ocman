import { useState } from 'react';
import { Bar } from 'react-chartjs-2';
import type { ActivityDay } from '../../lib/api';
import { BAR_OPTIONS_HOURLY, BAR_OPTIONS_SESSIONS } from '../../lib/chartConfig';
import { useActivity, useHourly } from '../../lib/queries';
import { AnalyticsFilters } from './AnalyticsFilters';
import { useDashboard } from './context';
import { ChartCard, ChartSlot, ChartSkeletons } from './shared';

export function ActivityTab() {
  const { dirScope } = useDashboard();
  const [days, setDays] = useState(30);
  const dir = dirScope || undefined;
  const activityQ = useActivity({ days: 365, dir });
  const dailyQ = useActivity({ days: days || undefined, dir });
  const hourlyQ = useHourly({ days: days || undefined, dir });
  const daily = dailyQ.data?.slice(-days);
  const errors = queryErrors(activityQ.error, dailyQ.error, hourlyQ.error);

  return (
    <div className="metrics-page">
      <AnalyticsFilters days={days} onDaysChange={setDays} />
      {errors.map((error) => <div key={error.message} className="oc-error-banner">{error.message}</div>)}
      {activityQ.isLoading && !activityQ.data && <ChartSkeletons labels={['Loading activity heatmap']} />}
      {(activityQ.data?.length ?? 0) > 0 && <HeatmapChart activity={activityQ.data ?? []} />}
      <div className="analytics-chart-pair">
        <ChartSlot isLoading={dailyQ.isLoading} label="Loading daily messages"><ChartCard title="Daily Messages">
            <Bar data={{ labels: daily?.map((day) => day.date.slice(5)) ?? [], datasets: [
              { label: 'User Prompts', data: daily?.map((day) => day.userMessages) ?? [], backgroundColor: 'rgba(166, 227, 161, 0.6)', borderRadius: 2 },
              { label: 'Assistant Turns', data: daily?.map((day) => day.messages) ?? [], backgroundColor: 'rgba(137, 180, 250, 0.6)', borderRadius: 2 },
            ] }} options={BAR_OPTIONS_SESSIONS} />
          </ChartCard></ChartSlot>
        <ChartSlot isLoading={hourlyQ.isLoading} label="Loading sessions by hour"><ChartCard title="Sessions by Hour of Day">
            <Bar data={{ labels: hourlyQ.data?.map((hour) => `${hour.hour}:00`) ?? [], datasets: [{ label: 'Sessions', data: hourlyQ.data?.map((hour) => hour.sessions) ?? [], backgroundColor: 'rgba(166, 227, 161, 0.6)', borderRadius: 2 }] }} options={BAR_OPTIONS_HOURLY} />
          </ChartCard></ChartSlot>
      </div>
    </div>
  );
}

function queryErrors(...errors: unknown[]) {
  return errors.filter((error): error is Error => error instanceof Error);
}

const CELL = 13;
const GAP = 2;
const DOW_WIDTH = 36;

function HeatmapChart({ activity }: { activity: ActivityDay[] }) {
  const [tooltip, setTooltip] = useState<{ text: string; x: number; y: number } | null>(null);
  const maxMessages = Math.max(...activity.map((day) => day.messages), 1);
  const totalMessages = activity.reduce((sum, day) => sum + day.messages, 0);
  const weeks: (ActivityDay | null)[][] = [];
  let week: (ActivityDay | null)[] = Array(new Date(`${activity[0].date}T00:00:00`).getDay()).fill(null);
  for (const day of activity) {
    week.push(day);
    if (week.length === 7) { weeks.push(week); week = []; }
  }
  while (week.length > 0 && week.length < 7) week.push(null);
  if (week.length > 0) weeks.push(week);
  const monthLabels: { week: number; label: string }[] = [];
  let lastMonth = -1;
  weeks.forEach((days, weekIndex) => {
    const first = days.find(Boolean);
    if (!first) return;
    const month = new Date(`${first.date}T00:00:00`).getMonth();
    if (month !== lastMonth) {
      monthLabels.push({ week: weekIndex, label: new Date(`${first.date}T00:00:00`).toLocaleString('en-US', { month: 'short' }) });
      lastMonth = month;
    }
  });

  return (
    <div className="chart-card heatmap-card">
      <div className="heatmap-title">Activity over the last 12 months</div>
      <div className="heatmap-months" style={{ paddingLeft: DOW_WIDTH }}>
        {monthLabels.map(({ week: weekIndex, label }) => <span key={weekIndex} className="heatmap-month-label" style={{ left: weekIndex * (CELL + GAP) }}>{label}</span>)}
      </div>
      <div style={{ display: 'flex' }}>
        <div className="heatmap-dow" style={{ width: DOW_WIDTH }}>
          {['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'].map((day, index) => <div key={day} className="heatmap-dow-label" style={{ height: CELL, marginBottom: index < 6 ? GAP : 0 }}>{index % 2 === 1 ? day : ''}</div>)}
        </div>
        <div className="heatmap-grid">
          {weeks.map((days, weekIndex) => <div key={weekIndex} className="heatmap-week">
            {days.map((day, dayIndex) => day ? <div key={day.date} className="heatmap-day" data-level={day.messages === 0 ? 0 : Math.min(4, Math.ceil(day.messages / maxMessages * 4))} style={{ width: CELL, height: CELL }} onMouseEnter={(event) => setTooltip({ text: `${day.date}: ${day.messages} messages, ${day.sessions} sessions`, x: event.clientX + 12, y: event.clientY - 36 })} onMouseLeave={() => setTooltip(null)} /> : <div key={dayIndex} style={{ width: CELL, height: CELL }} />)}
          </div>
          )}
        </div>
      </div>
      <div className="heatmap-footer">
        <span className="heatmap-summary">{totalMessages.toLocaleString()} messages in the last 12 months</span>
        <span className="heatmap-legend"><span className="heatmap-legend-label">Less</span>{[0, 1, 2, 3, 4].map((level) => <span key={level} className="heatmap-legend-cell heatmap-day" data-level={level} />)}<span className="heatmap-legend-label">More</span></span>
      </div>
      {tooltip && <div className="heatmap-tooltip" style={{ left: tooltip.x, top: tooltip.y }}>{tooltip.text}</div>}
    </div>
  );
}
