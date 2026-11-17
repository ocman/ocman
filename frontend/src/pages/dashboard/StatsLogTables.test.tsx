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

import { LogRange, ProjectLogTable, RequestLogTable, SessionLogTable } from './StatsLogTables';

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
    renderTable(<SessionLogTable sessions={makeMetrics().sessions} pageOffset={0} />);

    const link = screen.getByRole('link', { name: /Example session/ });
    expect(link).toHaveAttribute('href', '/session/sess-1');

    await user.tab();
    expect(link).toHaveFocus();
    await user.keyboard('{Enter}');
    expect(screen.getByTestId('location')).toHaveTextContent('/session/sess-1');
  });

  it('links the project row to the project', async () => {
    const user = userEvent.setup();
    renderTable(<ProjectLogTable projects={makeMetrics().projects} pageOffset={0} />);

    const link = screen.getByRole('link', { name: /proj/ });
    expect(link).toHaveAttribute('href', `/project/${encodeURIComponent('/home/u/proj')}`);

    await user.tab();
    expect(link).toHaveFocus();
    await user.keyboard('{Enter}');
    expect(screen.getByTestId('location')).toHaveTextContent('/project/');
  });

  it('links the request row to its session', async () => {
    const user = userEvent.setup();
    renderTable(<RequestLogTable requests={makeMetrics().requests} pageOffset={0} />);

    const link = screen.getByRole('link');
    expect(link).toHaveAttribute('href', '/session/sess-1');

    await user.tab();
    expect(link).toHaveFocus();
    await user.keyboard('{Enter}');
    expect(screen.getByTestId('location')).toHaveTextContent('/session/sess-1');
  });
});

describe('Subscription-backed cost', () => {
  it('labels the token estimate API-equivalent when the platform reports $0', () => {
    const metrics = makeMetrics();
    const subscription = { ...metrics.sessions[0], id: 'claude-1', title: 'Claude session', cost: 0, calcCost: 1.25, effectiveCost: 1.25 };
    renderTable(<SessionLogTable sessions={[metrics.sessions[0], subscription]} pageOffset={0} />);

    const rows = screen.getAllByRole('row');
    const billed = rows.find((row) => row.textContent?.includes('Example session'))!;
    const claude = rows.find((row) => row.textContent?.includes('Claude session'))!;
    expect(claude.querySelector('[title^="API-equivalent cost"]')).toHaveTextContent('≈$1.25');
    expect(billed).toHaveTextContent('$0.80');
    expect(billed).not.toHaveTextContent('≈');
    expect(screen.queryByText(/Reported/)).not.toBeInTheDocument();
  });

  it('labels subscription requests too', () => {
    const request = { ...makeMetrics().requests[0], cost: 0, calcCost: 0.3, effectiveCost: 0.3 };
    renderTable(<RequestLogTable requests={[request]} pageOffset={0} />);
    expect(screen.getByTitle(/^API-equivalent cost/)).toHaveTextContent('≈$0.30');
  });

  it('notes billed amounts on rows that mix billed and estimated cost', () => {
    const request = { ...makeMetrics().requests[0], cost: 0.1, calcCost: 0.3, effectiveCost: 0.3 };
    renderTable(<RequestLogTable requests={[request]} pageOffset={0} />);
    expect(screen.getByTitle(/\(\$0\.10 billed\)$/)).toHaveTextContent('≈$0.30');
  });

  it('labels each cell for phone card rows and keeps the rank in the sticky cell', () => {
    const project = { ...makeMetrics().projects[0], cost: 0, calcCost: 0, effectiveCost: 0, errorCount: 2 };
    renderTable(<ProjectLogTable projects={[project]} pageOffset={20} />);
    const row = screen.getAllByRole('row')[1];
    expect(row.querySelector('.metrics-sticky')).toHaveTextContent('21');
    expect(row.querySelector('[data-label="Cache read"]')).toHaveAttribute('title', '500 read · 200 written');
    expect(row.querySelector('[data-label="Cost"]')).toHaveTextContent('—');
    expect(row.querySelector('[data-label="Errors"]')).toHaveTextContent('2');
  });
});

it('keeps shared native message IDs distinct across sessions and reorders', () => {
  const error = vi.spyOn(console, 'error').mockImplementation(() => {});
  try {
    const base = makeMetrics().requests[0];
    const requests = ['parent', 'child-a', 'child-b'].map((sessionId) => ({ ...base, sessionId }));
    const { rerender } = renderTable(<RequestLogTable requests={requests} pageOffset={0} />);
    const links = screen.getAllByRole('link');
    expect(links.map((link) => link.getAttribute('href'))).toEqual(requests.map((r) => `/session/${r.sessionId}`));
    rerender(<MemoryRouter><RequestLogTable requests={[requests[2], requests[0]]} pageOffset={0} /><Location /></MemoryRouter>);
    const reordered = screen.getAllByRole('link');
    expect(reordered).toHaveLength(2);
    expect(reordered[0]).toBe(links[2]);
    expect(reordered[1]).toBe(links[0]);
    expect(error).not.toHaveBeenCalled();
  } finally {
    error.mockRestore();
  }
});

it('keeps each session model list attached to its row when sessions reorder', () => {
  const base = makeMetrics().sessions[0];
  const sessions = [
    { ...base, id: 'parent', title: 'Parent', models: ['anthropic/claude', 'openai/gpt-test'] },
    { ...base, id: 'child', title: 'Child', models: ['anthropic/claude'] },
  ];
  const { rerender } = renderTable(<SessionLogTable sessions={sessions} pageOffset={0} />);
  const tooltip = screen.getByRole('tooltip');
  expect(tooltip).toHaveTextContent('claude');
  expect(tooltip).toHaveTextContent('gpt-test');
  expect(tooltip.closest('tr')).toHaveTextContent('Parent');
  rerender(<MemoryRouter><SessionLogTable sessions={[sessions[1], sessions[0]]} pageOffset={0} /><Location /></MemoryRouter>);
  expect(screen.getByRole('tooltip')).toBe(tooltip);
  expect(tooltip.closest('tr')).toHaveTextContent('Parent');
});

describe('Missing timestamps', () => {
  it('shows a dash instead of the Unix epoch when a row has no recorded time', () => {
    const metrics = makeMetrics();
    renderTable(<SessionLogTable sessions={[{ ...metrics.sessions[0], lastRequestTime: 0 }]} pageOffset={0} />);
    expect(screen.getByRole('row', { name: /Example session/ })).not.toHaveTextContent(/1970/);
  });

  it('shows a dash for a request without a creation time', () => {
    renderTable(<RequestLogTable requests={[{ ...makeMetrics().requests[0], timeCreated: 0 }]} pageOffset={0} />);
    expect(screen.getAllByRole('row')[1]).not.toHaveTextContent(/01\/01/);
  });
});

it('shows the visible log range and nothing for an empty log', () => {
  const { container, rerender } = render(<LogRange page={1} pageSize={20} total={27} />);
  expect(container).toHaveTextContent('21–27 of 27');
  rerender(<LogRange page={0} pageSize={20} total={0} />);
  expect(container).toBeEmptyDOMElement();
});
