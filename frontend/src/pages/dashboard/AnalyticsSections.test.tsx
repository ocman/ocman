// @vitest-environment jsdom
import { fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('react-chartjs-2', () => ({
  Bar: ({ data }: { data: unknown }) => <div data-testid="bar-chart" data-chart={JSON.stringify(data)} />,
  Doughnut: ({ data }: { data: unknown }) => <div data-testid="doughnut-chart" data-chart={JSON.stringify(data)} />,
  Line: () => <div data-testid="line-chart" />,
}));
vi.mock('../../components/ProjectScopePicker', () => ({ ProjectScopePicker: () => <div>project scope</div> }));
vi.mock('./context', () => ({ useDashboard: () => ({ projects: [], dirScope: '/repo', setDirScope: vi.fn() }) }));

const useActivity = vi.fn();
const useHourly = vi.fn();
const useHourlyTokens = vi.fn();
const useMetrics = vi.fn();
const useAnalyticsOverview = vi.fn();
const useModels = vi.fn();
const usePermissionStats = vi.fn();
const useMetricLogs = vi.fn();
vi.mock('../../lib/queries', () => ({
  useActivity: (...args: unknown[]) => useActivity(...args),
  useHourly: (...args: unknown[]) => useHourly(...args),
  useHourlyTokens: (...args: unknown[]) => useHourlyTokens(...args),
  useMetrics: (...args: unknown[]) => useMetrics(...args),
  useAnalyticsOverview: (...args: unknown[]) => useAnalyticsOverview(...args),
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
  summary: { requests: 1, completedRequests: 1, successfulRequests: 1, errorRequests: 0, errorRate: 0, totalTokens: 15, inputTokens: 10, outputTokens: 5, avgTokensPerSec: 5, avgDurationMs: 1000, p50DurationMs: 900, p95DurationMs: 1200, totalDurationMs: 1000, cacheHitRate: 0, cacheReadTokens: 0, cacheWriteTokens: 0, totalCost: 0.1, totalCalcCost: 0.1, totalEffectiveCost: 0.1, costPerSuccessfulRequest: 0.1 },
  series: [{ label: 'Sep 1', avgOutputTokensSec: 5, inputTokens: 10, outputTokens: 5, cacheReadTokens: 0, avgDurationMs: 1000, p50DurationMs: 900, p95DurationMs: 1200, avgCacheEfficiency: 0, errorRate: 0 }],
  stopReasons: [{ reason: 'end_turn', count: 1 }],
  dailyEstimatedCostByModel: { models: [], series: [] }, dailyEffectiveCostByModel: { models: [], series: [] }, costByModel: { models: [], series: [] },
  agents: [{ agent: 'build', requests: 1, successfulRequests: 1, errorRequests: 0, errorRate: 0, inputTokens: 10, outputTokens: 5, totalTokens: 15, totalDurationMs: 1000, effectiveCost: 0.1 }],
};

function renderTab(component: React.ReactNode) {
  return render(<MemoryRouter>{component}</MemoryRouter>);
}

describe('analytics sections', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useActivity.mockReturnValue(query([{ date: '2026-09-01', messages: 2, userMessages: 1, sessions: 1 }]));
    useHourly.mockReturnValue(query([{ hour: 12, sessions: 1 }]));
    useHourlyTokens.mockReturnValue(query([]));
    useModels.mockReturnValue(query([{ provider: 'provider', model: 'model', count: 2, tokensIn: 10, tokensOut: 5 }]));
    useMetrics.mockReturnValue(query(metrics));
    useAnalyticsOverview.mockReturnValue(query({ inventoryScope: 'local', totalSessions: 10, totalProjects: 2, totalRoutines: 1, routineRunsByStatus: { done: 3 }, factoryEpicsByStatus: { active: 1 }, factoryIssuesByStatus: { done: 4 }, factoryAttemptsByPhase: { terminal: 5 }, factoryAttemptsByTerminalOutcome: { successful: 4 } }));
    usePermissionStats.mockReturnValue(query({ eligibleRequests: 1, autoApprovedRate: 1, manualPreemptions: 0, manualPreemptionRate: 0, medianJudgmentDurationMs: 10, medianManualResponseDurationMs: 20, userDecisionCount: 1, userDecisionRate: 1, affectedSessions: 1, unresolvedEligibleRequests: 0, observedUserWaitMs: 5000, p50UserWaitMs: 5000, p95UserWaitMs: 5000, daily: [] }));
    useMetricLogs.mockImplementation(({ kind }: { kind: string }) => query({ kind, total: 0, availableAgents: [], availableModels: [], [`${kind}s`]: [] }));
  });

  it('keeps overview limited to summary data', () => {
    renderTab(<OverviewTab />);
    const inventory = screen.getByRole('region', { name: 'All-time inventory' });
    const activity = screen.getByRole('region', { name: 'Request activity' });
    expect(within(inventory).getByText('Factory attempts')).toBeInTheDocument();
    expect(within(inventory).queryByRole('combobox')).not.toBeInTheDocument();
    expect(within(activity).getByText('Total Cost')).toBeInTheDocument();
    expect(within(activity).getByRole('combobox', { name: 'Last' })).toBeInTheDocument();
    expect(useMetrics).toHaveBeenCalledWith({ days: 30, dir: '/repo' });
  });

  it('shows activity without a misleading model filter', () => {
    renderTab(<ActivityTab />);
    expect(screen.getByText('Activity over the last 12 months')).toBeInTheDocument();
    expect(screen.getByText('2 messages in the last 12 months')).toBeInTheDocument();
    expect(screen.getByText('Less')).toBeInTheDocument();
    expect(useActivity).toHaveBeenCalledWith({ days: 365, dir: '/repo' });
    expect(screen.queryByRole('combobox', { name: 'Model' })).not.toBeInTheDocument();
  });

  it('shows partial activity query failures', () => {
    useActivity.mockReturnValueOnce(query([])).mockReturnValueOnce({ ...query([]), error: new Error('daily failed') });
    renderTab(<ActivityTab />);
    expect(screen.getByText('daily failed')).toBeInTheDocument();
  });

  it('owns model and cost filtering', () => {
    renderTab(<ModelsTab />);
    expect(screen.getByText('Effective Cost per Day by Model (USD)')).toBeInTheDocument();
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
    const chart = JSON.parse(screen.getByTestId('doughnut-chart').getAttribute('data-chart') ?? '{}');
    expect(chart.labels).toEqual(['two']);
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

  it('uses chart skeletons while graph data loads', () => {
    useMetrics.mockReturnValue({ data: undefined, isLoading: true, error: null });
    renderTab(<PerformanceTab />);
    expect(screen.getByRole('status', { name: 'Loading charts' })).toBeInTheDocument();
    expect(document.querySelector('.oc-spinner')).not.toBeInTheDocument();
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
    fireEvent.click(screen.getByRole('button', { name: 'Request Log' }));
    expect(useMetricLogs).toHaveBeenLastCalledWith(expect.objectContaining({ kind: 'request', limit: 20 }));
    expect(screen.getByRole('table')).toBeInTheDocument();
  });
});
