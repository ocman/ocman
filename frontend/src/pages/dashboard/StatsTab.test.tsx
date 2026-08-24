// @vitest-environment jsdom
//
// Render smoke test for the Stats tab. It pins the "effective cost"
// reconciliation UI introduced alongside the per-session estimate fix:
// the headline Total Cost card reads summary.totalEffectiveCost, and
// each log table shows an effective Cost column plus a "Reported / Est."
// detail. The heavy leaf dependencies (charts, the project-scope
// picker, the metrics query, the dashboard context) are mocked so the
// test stays fast and deterministic while still exercising the summary
// cards and all three log tables.

import { describe, expect, it, vi } from 'vitest';
import { render, screen, fireEvent, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import type { MetricsDashboard } from '../../lib/api';

vi.mock('react-chartjs-2', () => ({
  Bar: ({ data }: { data: unknown }) => <div data-testid="chart-bar" data-chart={JSON.stringify(data)} />,
  Line: () => <div data-testid="chart-line" />,
  Doughnut: () => <div data-testid="chart-doughnut" />,
}));

vi.mock('../../components/ProjectScopePicker', () => ({
  ProjectScopePicker: () => <div data-testid="project-scope-picker" />,
}));

vi.mock('./context', () => ({
  useDashboard: () => ({ projects: [], dirScope: '', setDirScope: vi.fn() }),
}));

const useMetrics = vi.fn();
vi.mock('../../lib/queries', () => ({
  useMetrics: (...args: unknown[]) => useMetrics(...args),
}));

// Imported after the mocks so StatsTab binds to them.
import { StatsTab } from './StatsTab';

function makeMetrics(): MetricsDashboard {
  return {
    availableAgents: ['build'],
    availableModels: ['anthropic/claude'],
    summary: {
      requests: 42,
      totalTokens: 100_000,
      inputTokens: 70_000,
      outputTokens: 30_000,
      avgTokensPerSec: 150,
      avgDurationMs: 5_000,
      totalDurationMs: 210_000,
      cacheHitRate: 0.35,
      cacheReadTokens: 25_000,
      cacheWriteTokens: 10_000,
      totalCost: 1.23,
      totalCalcCost: 1.1,
      totalEffectiveCost: 1.23,
    },
    series: [
      {
        label: 'Mon',
        avgOutputTokensSec: 100,
        cumulativeCost: 1.23,
        cumulativeCalcCost: 1.1,
        cumulativeEffectiveCost: 1.23,
        inputTokens: 70_000,
        cacheReadTokens: 25_000,
        outputTokens: 30_000,
        avgDurationMs: 5_000,
        avgCacheEfficiency: 0.35,
      } as MetricsDashboard['series'][number],
    ],
    costByModel: {
      models: ['anthropic/claude'],
      series: [{ label: '2026-08-24', costs: [0.75] }],
    },
    stopReasons: [{ reason: 'end_turn', count: 40 }],
    requests: [
      {
        id: 'req-1',
        sessionId: 'sess-1',
        timeCreated: Date.now(),
        agent: 'build',
        model: 'anthropic/claude',
        inputTokens: 1000,
        outputTokens: 500,
        cacheReadTokens: 100,
        cacheWriteTokens: 50,
        tokensPerSecond: 120,
        durationMs: 4000,
        cost: 0.5,
        calcCost: 0.45,
        effectiveCost: 0.5,
        stopReason: 'end_turn',
      },
    ],
    totalRequests: 1,
    sessions: [
      {
        id: 'sess-1',
        title: 'Example session',
        directory: '/home/u/proj',
        firstRequestTime: Date.now() - 10_000,
        lastRequestTime: Date.now(),
        requests: 5,
        inputTokens: 5000,
        outputTokens: 2000,
        cacheReadTokens: 500,
        cacheWriteTokens: 200,
        totalTokens: 7000,
        totalDurationMs: 20_000,
        avgTokensPerSec: 130,
        cost: 0.8,
        calcCost: 0.75,
        effectiveCost: 0.8,
        agents: ['build'],
        models: ['anthropic/claude'],
        errorCount: 0,
      },
    ],
    totalSessions: 1,
    projects: [
      {
        directory: '/home/u/proj',
        sessions: 2,
        requests: 10,
        inputTokens: 10_000,
        outputTokens: 4000,
        cacheReadTokens: 1000,
        cacheWriteTokens: 400,
        totalTokens: 14_000,
        totalDurationMs: 40_000,
        avgTokensPerSec: 125,
        cost: 1.0,
        calcCost: 0.9,
        effectiveCost: 1.0,
        models: ['anthropic/claude'],
        errorCount: 0,
        lastRequestTime: Date.now(),
      },
    ],
    totalProjects: 1,
  };
}

function renderStats(over: Partial<ReturnType<typeof useMetrics>> = {}) {
  useMetrics.mockReturnValue({ data: makeMetrics(), isLoading: false, error: null, ...over });
  return render(
    <MemoryRouter>
      <StatsTab />
    </MemoryRouter>,
  );
}

describe('StatsTab effective-cost UI', () => {
  it('shows estimated cost per day grouped by model as a bar chart', () => {
    renderStats();
    expect(screen.getByText('Estimated Cost per Day by Model (USD)')).toBeInTheDocument();
    expect(screen.getAllByTestId('chart-bar')).toHaveLength(4);
    expect(screen.getAllByTestId('chart-line')).toHaveLength(1);
    const costChart = screen.getAllByTestId('chart-bar')
      .map((chart) => JSON.parse(chart.getAttribute('data-chart') ?? '{}'))
      .find((data) => data.labels?.[0] === '2026-08-24');
    expect(costChart.datasets[0].data).toEqual([0.75]);
  });

  it('shows the effective Total Cost headline and the Reported / Est. detail card', () => {
    renderStats();
    // Headline card uses totalEffectiveCost ($1.23) with the new subtitle.
    expect(screen.getByText('Total Cost')).toBeInTheDocument();
    expect(screen.getByText('billed, est. when plan reports $0')).toBeInTheDocument();
    // Secondary detail card pairs reported / estimate. The "Reported /
    // Est." string also appears as a table column header, so anchor the
    // assertion on the card-only subtitle.
    expect(screen.getByText('platform-billed / token estimate')).toBeInTheDocument();
  });

  it('renders the Reported / Est. column header in the default project table', () => {
    renderStats();
    expect(screen.getByRole('columnheader', { name: 'Reported / Est.' })).toBeInTheDocument();
  });

  it('filters metrics through the shared agent select', () => {
    renderStats();
    fireEvent.click(screen.getByRole('combobox', { name: 'Agent' }));
    fireEvent.click(screen.getByRole('option', { name: 'build' }));
    expect(useMetrics).toHaveBeenLastCalledWith(expect.objectContaining({ agent: 'build' }));
  });

  it('switches to the session and request tables which also carry effective cost columns', () => {
    renderStats();

    fireEvent.click(screen.getByRole('button', { name: 'Session Log' }));
    expect(screen.getByText('Example session')).toBeInTheDocument();
    expect(screen.getAllByRole('columnheader', { name: 'Reported / Est.' }).length).toBeGreaterThan(0);

    fireEvent.click(screen.getByRole('button', { name: 'Request Log' }));
    const table = screen.getByRole('table');
    expect(within(table).getByRole('columnheader', { name: 'Reported / Est.' })).toBeInTheDocument();
  });

  it('renders a loading state before metrics arrive', () => {
    renderStats({ data: null, isLoading: true });
    expect(screen.getByText('Loading metrics...')).toBeInTheDocument();
  });

  it('surfaces a query error', () => {
    renderStats({ data: null, isLoading: false, error: new Error('boom') });
    expect(screen.getByText('boom')).toBeInTheDocument();
  });
});
