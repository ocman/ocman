// @vitest-environment jsdom
import { fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const lineOptions = vi.hoisted(() => vi.fn());

vi.mock('react-chartjs-2', () => ({
  Bar: ({ data }: { data: unknown }) => <div data-testid="bar-chart" data-chart={JSON.stringify(data)} />,
  Doughnut: ({ data, options }: { data: unknown; options: unknown }) => <div data-testid="doughnut-chart" data-chart={JSON.stringify(data)} data-options={JSON.stringify(options)} />,
  Line: ({ data, options }: { data: unknown; options: unknown }) => {
    lineOptions(options);
    return <div data-testid="line-chart" data-chart={JSON.stringify(data)} data-options={JSON.stringify(options)} />;
  },
}));
vi.mock('../../components/ProjectScopePicker', () => ({ ProjectScopePicker: () => <div>project scope</div> }));
vi.mock('./context', () => ({ useDashboard: () => ({ projects: [], dirScope: '/repo', setDirScope: vi.fn() }) }));

const useActivity = vi.fn();
const useHourly = vi.fn();
const useSessionConcurrency = vi.fn();
const useUIUsage = vi.fn();
const useAgentRunHours = vi.fn();
const useHourlyTokens = vi.fn();
const useMetrics = vi.fn();
const useAnalyticsOverview = vi.fn();
const useDatabaseSizes = vi.fn();
const useModels = vi.fn();
const usePermissionStats = vi.fn();
const useMetricLogs = vi.fn();
vi.mock('../../lib/queries', () => ({
  useActivity: (...args: unknown[]) => useActivity(...args),
  useHourly: (...args: unknown[]) => useHourly(...args),
  useSessionConcurrency: (...args: unknown[]) => useSessionConcurrency(...args),
  useUIUsage: (...args: unknown[]) => useUIUsage(...args),
  useAgentRunHours: (...args: unknown[]) => useAgentRunHours(...args),
  useHourlyTokens: (...args: unknown[]) => useHourlyTokens(...args),
  useMetrics: (...args: unknown[]) => useMetrics(...args),
  useAnalyticsOverview: (...args: unknown[]) => useAnalyticsOverview(...args),
  useDatabaseSizes: (...args: unknown[]) => useDatabaseSizes(...args),
  useModels: (...args: unknown[]) => useModels(...args),
  usePermissionStats: (...args: unknown[]) => usePermissionStats(...args),
  useMetricLogs: (...args: unknown[]) => useMetricLogs(...args),
}));

import { ActivityTab } from './ActivityTab';
import { LogsTab } from './LogsTab';
import { ModelsTab } from './ModelsTab';
import { OverviewTab } from './OverviewTab';
import { PerformanceTab } from './PerformanceTab';
import { PermissionsTab } from './PermissionsTab';

const query = (data: unknown) => ({ data, isLoading: false, error: null });
const metrics = {
  availableAgents: ['build'], availableModels: ['provider/model'],
  summary: { requests: 1, completedRequests: 1, successfulRequests: 1, errorRequests: 0, errorRate: 0, totalTokens: 15, inputTokens: 10, outputTokens: 5, avgTokensPerSec: 5, avgDurationMs: 1000, p50DurationMs: 900, p95DurationMs: 1200, totalDurationMs: 1000, cacheHitRate: 0, cacheReadTokens: 0, cacheWriteTokens: 0, totalCost: 0.1, totalCalcCost: 0.1, totalEffectiveCost: 0.1, estimatedCostByType: { input: 0.01, output: 0.04, cacheRead: 0.02, cacheWrite: 0.03 }, costPerSuccessfulRequest: 0.1 },
  series: [{ label: 'Sep 1', avgOutputTokensSec: 5, inputTokens: 10, outputTokens: 5, cacheReadTokens: 0, avgDurationMs: 1000, p50DurationMs: 900, p95DurationMs: 1200, avgCacheEfficiency: 0, errorRate: 0 }],
  stopReasons: [{ reason: 'end_turn', count: 1 }],
  dailyEstimatedCostByModel: { models: [], series: [] }, dailyEffectiveCostByModel: { models: [], series: [] }, costByModel: { models: [], series: [] },
  agents: [{ agent: 'build', requests: 1, successfulRequests: 1, errorRequests: 0, errorRate: 0, inputTokens: 10, outputTokens: 5, totalTokens: 15, totalDurationMs: 1000, agentDurationMs: 1000, toolDurationMs: 0, unknownDurationMs: 0, effectiveCost: 0.1 }],
};

function renderTab(component: React.ReactNode) {
  return render(<MemoryRouter>{component}</MemoryRouter>);
}

describe('analytics sections', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useActivity.mockReturnValue(query([{ date: '2026-09-01', messages: 2, userMessages: 1, sessions: 1 }]));
    useHourly.mockReturnValue(query([{ hour: 12, sessions: 1 }]));
    useSessionConcurrency.mockReturnValue(query({ bucketMs: 3_600_000, series: [{ timestamp: 1000, sessions: 2 }, { timestamp: 3_601_000, sessions: 0 }] }));
    useUIUsage.mockReturnValue(query([{ date: '2026-09-01', activeSeconds: 7200 }, { date: '2026-09-02', activeSeconds: 0 }]));
    useAgentRunHours.mockReturnValue(query([{ timestamp: 3_600_000, minutes: 90 }, { timestamp: 7_200_000, minutes: 0 }]));
    useHourlyTokens.mockReturnValue(query([]));
    useModels.mockReturnValue(query([{ provider: 'provider', model: 'model', count: 2, tokensIn: 10, tokensOut: 5 }]));
    useMetrics.mockReturnValue(query(metrics));
    useAnalyticsOverview.mockReturnValue(query({ inventoryScope: 'local', totalSessions: 10, subagentSessions: 2, totalProjects: 2, totalRoutines: 1, routineRunsByStatus: { done: 3 }, factoryEpicsByStatus: { active: 1 }, factoryIssuesByStatus: { done: 4 }, factoryAttemptsByPhase: { terminal: 5 }, factoryAttemptsByTerminalOutcome: { successful: 4 } }));
    useDatabaseSizes.mockReturnValue(query([
      { database: 'ocman', sampledAt: 1_757_500_000_000, sizeBytes: 10 * 1024 * 1024 },
      { database: 'opencode', sampledAt: 1_757_500_000_000, sizeBytes: 100 * 1024 * 1024 },
      { database: 'opencode', sampledAt: 1_757_503_600_000, sizeBytes: 120 * 1024 * 1024 },
    ]));
    usePermissionStats.mockReturnValue(query({ eligibleRequests: 1, autoApprovedRate: 1, manualPreemptions: 0, manualPreemptionRate: 0, medianJudgmentDurationMs: 10, medianManualResponseDurationMs: 20, userDecisionCount: 1, userDecisionRate: 1, affectedSessions: 1, unresolvedEligibleRequests: 0, observedUserWaitMs: 5000, p50UserWaitMs: 5000, p95UserWaitMs: 5000, daily: [] }));
    useMetricLogs.mockImplementation(({ kind }: { kind: string }) => query({ kind, total: 0, availableAgents: [], availableModels: [], [`${kind}s`]: [] }));
  });

  it('keeps overview limited to summary data', () => {
    renderTab(<OverviewTab />);
    const inventory = screen.getByRole('region', { name: 'All-time inventory' });
    const activity = screen.getByRole('region', { name: 'Request activity' });
    expect(within(inventory).getByText('Factory attempts')).toBeInTheDocument();
    expect(within(inventory).getByText('8')).toBeInTheDocument();
    expect(within(inventory).getByText('2 subagent sessions')).toBeInTheDocument();
    expect(within(inventory).queryByRole('combobox')).not.toBeInTheDocument();
    expect(within(activity).getByText('Total Cost')).toBeInTheDocument();
    expect(within(activity).getByRole('combobox', { name: 'Last' })).toBeInTheDocument();
    expect(useMetrics).toHaveBeenCalledWith({ days: 30, dir: '/repo' });
    expect(useDatabaseSizes).toHaveBeenCalledWith({ days: 30 });
    const storage = screen.getByText('Database Size (log scale)').closest('.chart-card') as HTMLElement;
    const chart = JSON.parse(within(storage).getByTestId('line-chart').getAttribute('data-chart') ?? '{}');
    expect(chart.datasets.map((dataset: { label: string }) => dataset.label)).toEqual(['OpenCode (MiB)', 'ocman (MiB)']);
    expect(chart.datasets[1].data).toEqual([10, null]);
    expect(chart.datasets.map((dataset: { pointRadius: number }) => dataset.pointRadius)).toEqual([1, 1]);
    const options = JSON.parse(within(storage).getByTestId('line-chart').getAttribute('data-options') ?? '{}');
    expect(Object.keys(options.scales)).toEqual(['x', 'y']);
    expect(options.scales.y.type).toBe('logarithmic');
    expect(screen.getByText('Database sizes cover this local instance and are not project-scoped.')).toBeInTheDocument();
  });

  it('shows activity without a misleading model filter', () => {
    renderTab(<ActivityTab />);
    expect(screen.getByText('Activity over the last 12 months')).toBeInTheDocument();
    expect(screen.getByText('2 messages in the last 12 months')).toBeInTheDocument();
    expect(screen.getByText('Less')).toBeInTheDocument();
    expect(useActivity).toHaveBeenCalledWith({ days: 365, dir: '/repo' });
    expect(screen.queryByRole('combobox', { name: 'Model' })).not.toBeInTheDocument();
  });

  it('plots installation-wide daily hours and project-scoped chronological agent minutes', () => {
    renderTab(<ActivityTab />);
    expect(useUIUsage).toHaveBeenCalledWith(30);
    expect(useAgentRunHours).toHaveBeenCalledWith({ days: 30, dir: '/repo' });
    expect(screen.getByText('2.0 hours in ocman · 1.0 hours per day')).toBeInTheDocument();
    const usageCard = screen.getByText('Active Time in Ocman per Day').closest('.chart-card') as HTMLElement;
    const usage = JSON.parse(within(usageCard).getByTestId('bar-chart').getAttribute('data-chart') ?? '{}');
    expect(usage.datasets[0].data).toEqual([2, 0]);
    const agentCard = screen.getByText('Agent Run Minutes per Hour').closest('.chart-card') as HTMLElement;
    const agents = JSON.parse(within(agentCard).getByTestId('bar-chart').getAttribute('data-chart') ?? '{}');
    expect(agents.datasets[0].data).toEqual([{ x: 3_600_000, y: 90 }, { x: 7_200_000, y: 0 }]);
  });

  it('plots only the selected daily activity range', () => {
    useActivity.mockReturnValueOnce(query([])).mockReturnValueOnce(query(Array.from({ length: 366 }, (_, index) => ({
      date: `day-${index}`,
      messages: index,
      userMessages: index,
      sessions: 0,
    }))));
    renderTab(<ActivityTab />);
    const card = screen.getByText('Daily Messages').closest('.chart-card') as HTMLElement;
    const chart = JSON.parse(within(card).getByTestId('bar-chart').getAttribute('data-chart') ?? '{}');
    expect(chart.labels).toHaveLength(30);
    expect(chart.datasets[0].data[0]).toBe(336);
  });

  it('plots concurrent sessions on a timestamp axis with the activity filters', () => {
    renderTab(<ActivityTab />);
    expect(useSessionConcurrency).toHaveBeenCalledWith({ days: 30, dir: '/repo' });
    const card = screen.getByText('Active Parallel Sessions').closest('.chart-card') as HTMLElement;
    const chart = JSON.parse(within(card).getByTestId('line-chart').getAttribute('data-chart') ?? '{}');
    expect(chart.datasets[0].data).toEqual([{ x: 1000, y: 2 }, { x: 3_601_000, y: 0 }]);
    const options = JSON.parse(within(card).getByTestId('line-chart').getAttribute('data-options') ?? '{}');
    expect(options.scales.x.type).toBe('linear');
    expect(options.scales.y.beginAtZero).toBe(true);
    expect(options.scales.y.ticks.precision).toBe(0);
    const callbacks = lineOptions.mock.lastCall?.[0];
    expect(callbacks.scales.x.ticks.callback(1000)).toBe(new Date(1000).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit' }));
    expect(callbacks.plugins.tooltip.callbacks.title([{ parsed: { x: 1000 } }])).toBe(new Date(1000).toLocaleString());
    expect(callbacks.plugins.tooltip.callbacks.title([])).toBe('');
    expect(screen.getByText(/excludes idle gaps and unfinished messages/)).toBeInTheDocument();
  });

  it('loads concurrency independently and shows its errors', () => {
    useSessionConcurrency.mockReturnValue({ data: undefined, isLoading: true, error: new Error('concurrency failed') });
    renderTab(<ActivityTab />);
    expect(screen.getByRole('status', { name: 'Loading parallel sessions' })).toBeInTheDocument();
    expect(screen.getByText('concurrency failed')).toBeInTheDocument();
    expect(screen.getByText('Daily Messages')).toBeInTheDocument();
  });

  it('handles absent concurrency history without inventing active sessions', () => {
    useSessionConcurrency.mockReturnValue(query(undefined));
    renderTab(<ActivityTab />);
    const card = screen.getByText('Active Parallel Sessions').closest('.chart-card') as HTMLElement;
    const chart = JSON.parse(within(card).getByTestId('line-chart').getAttribute('data-chart') ?? '{}');
    expect(chart.datasets[0].data).toEqual([]);
    expect(screen.getByText(/Peak per 1-hour bucket/)).toBeInTheDocument();
  });

  it('shows a visible marker for a single concurrency bucket', () => {
    useSessionConcurrency.mockReturnValue(query({ bucketMs: 3_600_000, series: [{ timestamp: 1000, sessions: 2 }] }));
    renderTab(<ActivityTab />);
    const card = screen.getByText('Active Parallel Sessions').closest('.chart-card') as HTMLElement;
    const chart = JSON.parse(within(card).getByTestId('line-chart').getAttribute('data-chart') ?? '{}');
    expect(chart.datasets[0].data).toEqual([{ x: 1000, y: 2 }]);
    expect(chart.datasets[0].pointRadius).toBeGreaterThan(0);
  });

  it('shows partial activity query failures', () => {
    useActivity.mockReturnValueOnce(query([])).mockReturnValueOnce({ ...query([]), error: new Error('daily failed') });
    renderTab(<ActivityTab />);
    expect(screen.getByText('daily failed')).toBeInTheDocument();
  });

  it('loads each activity graph independently', () => {
    useActivity.mockReturnValueOnce({ data: undefined, isLoading: true, error: null }).mockReturnValueOnce(query([{ date: '2026-09-01', messages: 2, userMessages: 1, sessions: 1 }]));
    renderTab(<ActivityTab />);
    expect(screen.getByRole('status', { name: 'Loading activity heatmap' })).toBeInTheDocument();
    expect(screen.getByText('Daily Messages')).toBeInTheDocument();
    expect(screen.getByText('Sessions by Hour of Day')).toBeInTheDocument();
  });

  it('owns model and cost filtering', () => {
    renderTab(<ModelsTab />);
    expect(screen.getByText('Effective Cost per Day by Model (USD)')).toBeInTheDocument();
    expect(screen.getByText('Cost Distribution by Model')).toBeInTheDocument();
    expect(screen.getByText('Estimated Cost Distribution by Type')).toBeInTheDocument();
    expect(screen.getByText('Agent breakdown')).toBeInTheDocument();
    expect(screen.getByRole('combobox', { name: 'Model' })).toBeInTheDocument();
  });

  it('applies the model filter to usage charts', () => {
    useModels.mockReturnValue(query([
      { provider: 'provider', model: 'one', count: 2, tokensIn: 10, tokensOut: 5 },
      { provider: 'provider', model: 'two', count: 1, tokensIn: 4, tokensOut: 2 },
    ]));
    renderTab(<ModelsTab />);
    fireEvent.click(screen.getByRole('combobox', { name: 'Model' }));
    fireEvent.click(screen.getByRole('option', { name: 'two' }));
    const card = screen.getByText('Model Usage').closest('.chart-card') as HTMLElement;
    const chart = JSON.parse(within(card).getByTestId('doughnut-chart').getAttribute('data-chart') ?? '{}');
    expect(chart.labels).toEqual(['two']);
    const options = JSON.parse(within(card).getByTestId('doughnut-chart').getAttribute('data-options') ?? '{}');
    expect(options.plugins.legend.position).toBe('right');
  });

  it('plots model and type cost distributions', () => {
    useMetrics.mockReturnValue(query({
      ...metrics,
      costByModel: { models: ['provider/one', 'provider/two'], series: [{ label: 'Sep 1', costs: [0.6, 0.4] }] },
    }));
    renderTab(<ModelsTab />);

    const modelCard = screen.getByText('Cost Distribution by Model').closest('.chart-card') as HTMLElement;
    const modelChart = JSON.parse(within(modelCard).getByTestId('doughnut-chart').getAttribute('data-chart') ?? '{}');
    expect(modelChart.labels).toEqual(['one', 'two']);
    expect(modelChart.datasets[0].data).toEqual([0.6, 0.4]);

    const typeCard = screen.getByText('Estimated Cost Distribution by Type').closest('.chart-card') as HTMLElement;
    const typeChart = JSON.parse(within(typeCard).getByTestId('doughnut-chart').getAttribute('data-chart') ?? '{}');
    expect(typeChart.labels).toEqual(['Input', 'Output', 'Cache read', 'Cache write']);
    expect(typeChart.datasets[0].data).toEqual([0.01, 0.04, 0.02, 0.03]);
  });

  it('ranks hourly models by token volume', () => {
    useHourlyTokens.mockReturnValue(query(Array.from({ length: 9 }, (_, index) => ({
      datetime: '2026-09-01 12', provider: 'p', model: `m${index}`,
      tokensIn: index === 8 ? 100 : 9 - index, tokensOut: 0,
    }))));
    renderTab(<ModelsTab />);
    const card = screen.getByText('Tokens per Hour by Model').closest('.chart-card') as HTMLElement;
    const chart = JSON.parse(within(card).getByTestId('bar-chart').getAttribute('data-chart') ?? '{}');
    expect(chart.datasets[0].label).toBe('m8');
    expect(chart.datasets).toHaveLength(8);
    expect(chart.labels).toHaveLength(30 * 24);
    expect(chart.datasets[0].data).toContain(0);
  });

  it('shows partial model query failures', () => {
    useMetrics.mockReturnValue({ ...query(undefined), error: new Error('cost failed') });
    renderTab(<ModelsTab />);
    expect(screen.getByText('cost failed')).toBeInTheDocument();
  });

  it('keeps agent and model filters on performance', () => {
    renderTab(<PerformanceTab />);
    expect(screen.getByText('Cache Efficiency')).toBeInTheDocument();
    expect(screen.getByText('P95 latency')).toBeInTheDocument();
    expect(screen.getByText('Error Rate')).toBeInTheDocument();
    expect(screen.getByRole('combobox', { name: 'Agent' })).toBeInTheDocument();
    expect(screen.getByRole('combobox', { name: 'Model' })).toBeInTheDocument();
  });

  it('stacks total waiting time by agent and tools in seconds', () => {
    useMetrics.mockReturnValue(query({ ...metrics, agents: [
      { ...metrics.agents[0], agent: 'build', agentDurationMs: 2000, toolDurationMs: 8000, unknownDurationMs: 1000 },
      { ...metrics.agents[0], agent: '', agentDurationMs: 500, toolDurationMs: 0, unknownDurationMs: 0 },
    ] }));
    renderTab(<PerformanceTab />);
    const card = screen.getByText('Waiting Time by Agent (s)').closest('.chart-card') as HTMLElement;
    const chart = JSON.parse(within(card).getByTestId('bar-chart').getAttribute('data-chart') ?? '{}');
    expect(chart.labels).toEqual(['build', 'Unknown agent']);
    expect(chart.datasets.map((dataset: { label: string; data: number[] }) => [dataset.label, dataset.data])).toEqual([
      ['Agent response', [2, 0.5]], ['Tools', [8, 0]], ['Unknown timing', [1, 0]],
    ]);
    expect(screen.getByText(/Parallel tools count once/)).toBeInTheDocument();
  });

  it('shows an empty waiting-time graph when no requests match', () => {
    useMetrics.mockReturnValue(query({ ...metrics, agents: [] }));
    renderTab(<PerformanceTab />);
    expect(screen.getByText('No recorded waiting time for these filters.')).toBeInTheDocument();
  });

  it('uses chart skeletons while graph data loads', () => {
    useMetrics.mockReturnValue({ data: undefined, isLoading: true, error: null });
    renderTab(<PerformanceTab />);
    expect(screen.getByRole('status', { name: 'Loading request latency' })).toBeInTheDocument();
    expect(screen.getByRole('status', { name: 'Loading waiting time' })).toBeInTheDocument();
    expect(document.querySelector('.oc-spinner')).not.toBeInTheDocument();
  });

  it('keeps separate overview loaders in their eventual slots', () => {
    useAnalyticsOverview.mockReturnValue({ data: undefined, isLoading: true, error: null });
    useMetrics.mockReturnValue({ data: undefined, isLoading: true, error: null });
    useDatabaseSizes.mockReturnValue({ data: undefined, isLoading: true, error: null });
    renderTab(<OverviewTab />);
    expect(screen.getByRole('status', { name: 'Loading inventory' })).toBeInTheDocument();
    expect(screen.getByRole('status', { name: 'Loading request summary' })).toBeInTheDocument();
    expect(screen.getByRole('status', { name: 'Loading token usage' })).toBeInTheDocument();
    expect(screen.getByRole('status', { name: 'Loading database sizes' })).toBeInTheDocument();
  });

  it('renders database sizes independently of request metrics', () => {
    useMetrics.mockReturnValue({ data: undefined, isLoading: false, error: new Error('metrics failed') });
    renderTab(<OverviewTab />);
    expect(screen.getByText('metrics failed')).toBeInTheDocument();
    expect(screen.getByText('Database Size (log scale)')).toBeInTheDocument();
  });

  it('shows a point for the first database size sample', () => {
    useDatabaseSizes.mockReturnValue(query([{ database: 'ocman', sampledAt: 1_757_500_000_000, sizeBytes: 10 * 1024 * 1024 }]));
    renderTab(<OverviewTab />);
    const storage = screen.getByText('Database Size (log scale)').closest('.chart-card') as HTMLElement;
    const chart = JSON.parse(within(storage).getByTestId('line-chart').getAttribute('data-chart') ?? '{}');
    expect(chart.datasets.map((dataset: { pointRadius: number }) => dataset.pointRadius)).toEqual([1, 1]);
    expect(chart.labels[0]).toContain('2025');
  });

  it('shows database size errors and empty history', () => {
    useDatabaseSizes.mockReturnValue({ data: [], isLoading: false, error: new Error('sizes failed') });
    renderTab(<OverviewTab />);
    expect(screen.getByText('sizes failed')).toBeInTheDocument();
    expect(screen.getByText('No database size samples yet.')).toBeInTheDocument();
  });

  it('queries permission data independently', () => {
    renderTab(<PermissionsTab />);
    expect(usePermissionStats).toHaveBeenCalledWith({ days: 30, dir: '/repo' });
    expect(screen.getByText('Eligible requests')).toBeInTheDocument();
    expect(screen.getByText('Observed user wait')).toBeInTheDocument();
  });

  it('fetches only the selected log grain', () => {
    renderTab(<LogsTab />);
    expect(useMetricLogs).toHaveBeenLastCalledWith(expect.objectContaining({ kind: 'project', projectLimit: 20 }));
    fireEvent.keyDown(screen.getByRole('tab', { name: 'Request Log' }), { key: 'Enter' });
    expect(useMetricLogs).toHaveBeenLastCalledWith(expect.objectContaining({ kind: 'request', limit: 20 }));
    expect(screen.getByRole('table')).toBeInTheDocument();
  });

  it('announces only the active log loader and retains stale tables during a failed refresh', () => {
    useMetricLogs.mockReturnValue({ data: undefined, isLoading: true, error: null });
    const { rerender } = renderTab(<LogsTab />);
    expect(screen.getAllByRole('status')).toHaveLength(1);
    expect(screen.getByRole('status')).toHaveTextContent('Loading logs...');
    expect(screen.getByRole('status').firstElementChild).toHaveAttribute('aria-hidden', 'true');
    expect(screen.queryByRole('table')).not.toBeInTheDocument();

    useMetricLogs.mockReturnValue({
      data: { kind: 'project', total: 0, availableAgents: [], availableModels: [], projects: [] },
      isLoading: true,
      error: new Error('Refresh failed'),
    });
    rerender(<MemoryRouter><LogsTab /></MemoryRouter>);
    expect(screen.queryByText('Loading logs...')).not.toBeInTheDocument();
    expect(screen.getByRole('table')).toBeInTheDocument();
    expect(screen.getByRole('alert')).toHaveTextContent('Refresh failed');

    useMetricLogs.mockReturnValue({ data: undefined, isLoading: false, error: new Error('Logs failed') });
    rerender(<MemoryRouter><LogsTab /></MemoryRouter>);
    expect(screen.queryByText('Loading logs...')).not.toBeInTheDocument();
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
    expect(screen.getByRole('alert')).toHaveTextContent('Logs failed');
  });
});
