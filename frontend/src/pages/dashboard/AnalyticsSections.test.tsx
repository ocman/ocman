// @vitest-environment jsdom
import { fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('react-chartjs-2', () => ({
  Bar: ({ data }: { data: unknown }) => <div data-testid="bar-chart" data-chart={JSON.stringify(data)} />,
  Doughnut: () => <div data-testid="doughnut-chart" />,
  Line: () => <div data-testid="line-chart" />,
}));
vi.mock('../../components/ProjectScopePicker', () => ({ ProjectScopePicker: () => <div>project scope</div> }));
vi.mock('./context', () => ({ useDashboard: () => ({ projects: [], dirScope: '/repo', setDirScope: vi.fn() }) }));

const useActivity = vi.fn();
const useHourly = vi.fn();
const useHourlyTokens = vi.fn();
const useMetrics = vi.fn();
const useModels = vi.fn();
const usePermissionStats = vi.fn();
const useMetricLogs = vi.fn();
vi.mock('../../lib/queries', () => ({
  useActivity: (...args: unknown[]) => useActivity(...args),
  useHourly: (...args: unknown[]) => useHourly(...args),
  useHourlyTokens: (...args: unknown[]) => useHourlyTokens(...args),
  useMetrics: (...args: unknown[]) => useMetrics(...args),
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
  summary: { requests: 1, totalTokens: 15, inputTokens: 10, outputTokens: 5, avgTokensPerSec: 5, avgDurationMs: 1000, totalDurationMs: 1000, cacheHitRate: 0, cacheReadTokens: 0, cacheWriteTokens: 0, totalCost: 0.1, totalCalcCost: 0.1, totalEffectiveCost: 0.1 },
  series: [{ label: 'Sep 1', avgOutputTokensSec: 5, inputTokens: 10, outputTokens: 5, cacheReadTokens: 0, avgDurationMs: 1000, avgCacheEfficiency: 0 }],
  stopReasons: [{ reason: 'end_turn', count: 1 }],
  dailyEstimatedCostByModel: { models: [], series: [] }, costByModel: { models: [], series: [] },
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
    usePermissionStats.mockReturnValue(query({ eligibleRequests: 1, autoApprovedRate: 1, manualPreemptions: 0, manualPreemptionRate: 0, medianJudgmentDurationMs: 10, medianManualResponseDurationMs: 20, daily: [] }));
    useMetricLogs.mockImplementation(({ kind }: { kind: string }) => query({ kind, total: 0, availableAgents: [], availableModels: [], [`${kind}s`]: [] }));
  });

  it('keeps overview limited to summary data', () => {
    renderTab(<OverviewTab />);
    expect(screen.getByText('Total Cost')).toBeInTheDocument();
    expect(useMetrics).toHaveBeenCalledWith({ days: 30, dir: '/repo' });
  });

  it('shows activity without a misleading model filter', () => {
    renderTab(<ActivityTab />);
    expect(screen.getByText('Activity over the last 12 months')).toBeInTheDocument();
    expect(screen.queryByRole('combobox', { name: 'Model' })).not.toBeInTheDocument();
  });

  it('shows partial activity query failures', () => {
    useActivity.mockReturnValueOnce(query([])).mockReturnValueOnce({ ...query([]), error: new Error('daily failed') });
    renderTab(<ActivityTab />);
    expect(screen.getByText('daily failed')).toBeInTheDocument();
  });

  it('owns model and cost filtering', () => {
    renderTab(<ModelsTab />);
    expect(screen.getByText('Estimated Cost per Day by Model (USD)')).toBeInTheDocument();
    expect(screen.getByRole('combobox', { name: 'Model' })).toBeInTheDocument();
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
    expect(screen.getByRole('combobox', { name: 'Agent' })).toBeInTheDocument();
    expect(screen.getByRole('combobox', { name: 'Model' })).toBeInTheDocument();
  });

  it('queries permission data independently', () => {
    renderTab(<PermissionsTab />);
    expect(usePermissionStats).toHaveBeenCalledWith({ days: 30, dir: '/repo' });
    expect(screen.getByText('Eligible requests')).toBeInTheDocument();
  });

  it('fetches only the selected log grain', () => {
    renderTab(<LogsTab />);
    expect(useMetricLogs).toHaveBeenLastCalledWith(expect.objectContaining({ kind: 'project', projectLimit: 20 }));
    fireEvent.click(screen.getByRole('button', { name: 'Request Log' }));
    expect(useMetricLogs).toHaveBeenLastCalledWith(expect.objectContaining({ kind: 'request', limit: 20 }));
    expect(screen.getByRole('table')).toBeInTheDocument();
  });
});
