// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import type { Loop, LoopDetail } from '../lib/api.types';
import { LoopHistoryView } from './LoopHistoryView';

const getMock = vi.fn();
vi.mock('../lib/api', () => ({
  api: { loops: { get: (...a: unknown[]) => getMock(...a) } },
}));

function makeLoop(overrides: Partial<Loop> = {}): Loop {
  return {
    id: 'loop_1',
    platform: 'opencode',
    rootSessionID: 's1',
    directory: '/src/ocman',
    projectName: 'ocman',
    title: 'Check PRs mergeable',
    currentTask: '',
    pattern: 'heartbeat',
    triggerType: 'schedule',
    actionType: 'prompt_root',
    actionTemplate: 'check',
    sessionMode: 'fresh',
    state: 'active',
    iteration: 2,
    errorStreak: 0,
    tokensUsed: 0,
    costUSD: 0.5,
    lastFiredAt: Date.now(),
    createdAt: Date.now(),
    updatedAt: Date.now(),
    completedAt: 0,
    lastSummary: '',
    triggerConfig: { interval_seconds: 1800 },
    stopConditions: { max_iterations: 48, max_cost_usd: 5 },
    ...overrides,
  };
}

beforeEach(() => getMock.mockReset());

function renderView(loop = makeLoop()) {
  return render(
    <MemoryRouter>
      <LoopHistoryView loop={loop} />
    </MemoryRouter>,
  );
}

describe('LoopHistoryView', () => {
  it('shows budget and next-run summary', async () => {
    getMock.mockResolvedValueOnce({ ...makeLoop(), iterations: [], children: [], subLoops: [] } as LoopDetail);
    renderView();
    expect(screen.getByTestId('loop-budget')).toHaveTextContent('2/48 iters');
    expect(screen.getByTestId('loop-budget')).toHaveTextContent('$0.50/$5.00');
    expect(screen.getByTestId('loop-next-run')).toHaveTextContent(/next run: in /);
    await waitFor(() => expect(getMock).toHaveBeenCalledWith('loop_1'));
  });

  it('renders the iteration table with session state, duration, and link', async () => {
    const now = Date.now();
    const detail: LoopDetail = {
      ...makeLoop(),
      iterations: [
        {
          id: 1, seq: 1, firedAt: now - 30000, startedAt: now - 30000, completedAt: now - 29000,
          triggerDetail: 'scheduled', renderedPrompt: 'check PRs', targetSessionID: '',
          childSessionID: 'sess_a', outcome: 'ok', summary: 'spawned sess_a',
        },
      ],
      children: [{ id: 'sess_a', status: 'completed', createdAt: now - 30000, completedAt: now - 5000 }],
      subLoops: [],
    };
    getMock.mockResolvedValueOnce(detail);
    renderView();

    await waitFor(() => expect(screen.getByRole('table')).toBeInTheDocument());
    expect(screen.getByRole('cell', { name: '1' })).toBeInTheDocument();
    const stateBadge = screen.getByTestId('loop-history-session-state');
    expect(stateBadge).toHaveTextContent('done');
    expect(screen.getByTestId('loop-history-duration')).toHaveTextContent('25s');
    expect(screen.getByTestId('loop-history-session-link')).toHaveAttribute('href', '/session/sess_a');
  });

  it('renders sub-loops', async () => {
    const detail: LoopDetail = {
      ...makeLoop(),
      iterations: [],
      children: [],
      subLoops: [makeLoop({ id: 'sub1', title: 'Sub A', state: 'active' })],
    };
    getMock.mockResolvedValueOnce(detail);
    renderView();
    await waitFor(() => expect(screen.getByTestId('loop-subloops')).toBeInTheDocument());
    expect(screen.getByText('Sub A')).toBeInTheDocument();
  });
});
