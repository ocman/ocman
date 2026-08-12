// @vitest-environment jsdom
//
// The three Stats log tables navigate on a row-wide onClick, which is
// mouse-only. Each row's primary cell must carry a real link so the
// destination is focusable, announced and openable with the keyboard.
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import type { MetricsDashboard } from '../../lib/api';

vi.mock('react-chartjs-2', () => ({
  Bar: () => <div data-testid="chart-bar" />,
  Line: () => <div data-testid="chart-line" />,
  Doughnut: () => <div data-testid="chart-doughnut" />,
}));

import { ProjectLogTable, RequestLogTable, SessionLogTable } from './StatsLogTables';

const NUMBERS = {
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
  models: ['anthropic/claude'],
  errorCount: 0,
};

function makeMetrics(): MetricsDashboard {
  return {
    sessions: [{
      id: 'sess-1',
      title: 'Example session',
      directory: '/home/u/proj',
      firstRequestTime: 1000,
      lastRequestTime: 2000,
      agents: ['build'],
      ...NUMBERS,
    }],
    projects: [{
      directory: '/home/u/proj',
      sessions: 2,
      lastRequestTime: 2000,
      ...NUMBERS,
    }],
    requests: [{
      id: 'req-1',
      sessionId: 'sess-1',
      timeCreated: 2000,
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
    }],
  } as unknown as MetricsDashboard;
}

function Location() {
  return <div data-testid="location">{useLocation().pathname}</div>;
}

function renderTable(node: React.ReactNode) {
  return render(
    <MemoryRouter>
      {node}
      <Location />
    </MemoryRouter>,
  );
}

describe('Stats log table rows are keyboard reachable', () => {
  it('links the session row title to the session', async () => {
    const user = userEvent.setup();
    renderTable(<SessionLogTable metrics={makeMetrics()} pageOffset={0} />);

    const link = screen.getByRole('link', { name: /Example session/ });
    expect(link).toHaveAttribute('href', '/session/sess-1');

    await user.tab();
    expect(link).toHaveFocus();
    await user.keyboard('{Enter}');
    expect(screen.getByTestId('location')).toHaveTextContent('/session/sess-1');
  });

  it('links the project row to the project', async () => {
    const user = userEvent.setup();
    renderTable(<ProjectLogTable metrics={makeMetrics()} pageOffset={0} />);

    const link = screen.getByRole('link', { name: /proj/ });
    expect(link).toHaveAttribute('href', `/project/${encodeURIComponent('/home/u/proj')}`);

    await user.tab();
    expect(link).toHaveFocus();
    await user.keyboard('{Enter}');
    expect(screen.getByTestId('location')).toHaveTextContent('/project/');
  });

  it('links the request row to its session', async () => {
    const user = userEvent.setup();
    renderTable(<RequestLogTable metrics={makeMetrics()} pageOffset={0} />);

    const link = screen.getByRole('link');
    expect(link).toHaveAttribute('href', '/session/sess-1');

    await user.tab();
    expect(link).toHaveFocus();
    await user.keyboard('{Enter}');
    expect(screen.getByTestId('location')).toHaveTextContent('/session/sess-1');
  });
});
