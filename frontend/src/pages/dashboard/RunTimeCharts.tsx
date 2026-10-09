import { Bar } from 'react-chartjs-2';
import { InlineAlert } from '../../components/InlineAlert';
import { BAR_OPTIONS_HOURLY, CHART_COLORS } from '../../lib/chartConfig';
import { useAgentRunHours, useUIUsage } from '../../lib/queries';
import { ChartCard, ChartSlot } from './shared';

export function RunTimeCharts({ days, dir }: { days: number; dir?: string }) {
  const usage = useUIUsage(days);
  const agents = useAgentRunHours({ days: days || undefined, dir });
  const totalHours = (usage.data ?? []).reduce((sum, day) => sum + day.activeSeconds / 3600, 0);
  const average = totalHours / (usage.data?.length || 1);
  return <>
    {[usage.error, agents.error].filter((error): error is Error => error instanceof Error)
      .map((error, index) => <InlineAlert key={index}>{error.message}</InlineAlert>)}
    <p className="analytics-scope-note">{totalHours.toFixed(1)} hours in ocman · {average.toFixed(1)} hours per day</p>
    <ChartSlot isLoading={usage.isLoading} label="Loading active time in ocman">
      <ChartCard title="Active Time in Ocman per Day">
        <Bar aria-label="Active hours in ocman per day" role="img" data={{
          labels: usage.data?.map((day) => day.date) ?? [],
          datasets: [{ label: 'Hours', data: usage.data?.map((day) => day.activeSeconds / 3600) ?? [], backgroundColor: CHART_COLORS[0] }],
        }} options={{ ...BAR_OPTIONS_HOURLY, scales: {
          ...BAR_OPTIONS_HOURLY.scales,
          y: { beginAtZero: true, title: { display: true, text: 'Hours' } },
        } }} />
      </ChartCard>
    </ChartSlot>
    <p className="analytics-scope-note">Foreground use, excluding inactivity longer than five minutes. UTC days; all browsers and devices on this installation count once. Not project-scoped. Tracking starts with this version.</p>
    <ChartSlot isLoading={agents.isLoading} label="Loading agent run minutes">
      <ChartCard title="Agent Run Minutes per Hour">
        <Bar aria-label="Agent run minutes in each chronological hour" role="img" data={{
          datasets: [{ label: 'Agent minutes', data: agents.data?.map((point) => ({ x: point.timestamp, y: point.minutes })) ?? [], backgroundColor: CHART_COLORS[1] }],
        }} options={{ ...BAR_OPTIONS_HOURLY, scales: {
          x: { type: 'linear', grid: { display: false }, ticks: { maxTicksLimit: 10, callback: (value) => new Date(Number(value)).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', timeZone: 'UTC' }) } },
          y: { beginAtZero: true, title: { display: true, text: 'Agent minutes' } },
        }, plugins: { legend: { display: false }, tooltip: { callbacks: {
          title: (items) => items.length ? `${new Date(Number(items[0].parsed.x)).toISOString().slice(0, 13)}:00 UTC` : '',
          label: (item) => `${Number(item.parsed.y).toFixed(1)} agent minutes`,
        } } } }} />
      </ChartCard>
    </ChartSlot>
    <p className="analytics-scope-note">UTC hours on this machine, from completed assistant timings. Includes tools and subagents; excludes gaps, recorded human waits and question-tool waits. Parallel agents add together, so an hour can exceed 60 minutes. Unrecorded historical permission waits cannot be excluded.</p>
  </>;
}
