// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import type { Loop } from '../lib/api.types';
import { LoopTableRow } from './LoopTableRow';

const storeState = {
  resume: vi.fn(),
  pause: vi.fn(),
  trigger: vi.fn(),
  remove: vi.fn(),
  update: vi.fn(),
};
vi.mock('../lib/loopsStore', () => ({
  useLoopsStore: (selector: (s: typeof storeState) => unknown) => selector(storeState),
}));

// The history modal has its own test (and needs router + api); stub it
// here so the row test stays focused on the row.
vi.mock('./LoopHistoryModal', () => ({
  LoopHistoryModal: ({ loop }: { loop: Loop }) => (
    <div data-testid="loop-history-backdrop">history for {loop.id}</div>
  ),
}));

function makeLoop(overrides: Partial<Loop> = {}): Loop {
  return {
    id: 'loop_1',
    platform: 'opencode',
    rootSessionID: 's1',
    directory: '/src/ocman',
    projectName: 'ocman',
    title: 'Check PRs',
    currentTask: '',
    pattern: '',
    triggerType: 'schedule',
    actionType: 'prompt_root',
    actionTemplate: '',
    sessionMode: 'fresh',
    state: 'active',
    iteration: 2,
    errorStreak: 0,
    tokensUsed: 0,
    costUSD: 0.5,
    lastFiredAt: 0,
    createdAt: 0,
    updatedAt: 0,
    completedAt: 0,
    lastSummary: '',
    triggerConfig: { interval_seconds: 10000 },
    stopConditions: { max_iterations: 48, max_cost_usd: 5 },
    ...overrides,
  };
}

// Render inside a table so the <tr> is valid.
function renderRow(loop: Loop) {
  return render(
    <table><tbody><LoopTableRow loop={loop} /></tbody></table>,
  );
}

beforeEach(() => {
  Object.values(storeState).forEach((fn) => fn.mockReset());
});

describe('LoopTableRow', () => {
  it('renders a row with state, formatted trigger, and budget', () => {
    renderRow(makeLoop());
    expect(screen.getByTestId('loop-state')).toHaveTextContent('active');
    expect(screen.getByText('every 2h46m40s')).toBeInTheDocument();
    expect(screen.getByTestId('loop-budget')).toHaveTextContent('2/48');
    expect(screen.getByTestId('loop-budget')).toHaveTextContent('$0.50/$5.00');
  });

  it('shows Edit for an active loop, hides it for a terminal one', () => {
    const { rerender } = renderRow(makeLoop({ state: 'active' }));
    expect(screen.getByRole('button', { name: 'Edit loop' })).toBeInTheDocument();
    rerender(<table><tbody><LoopTableRow loop={makeLoop({ state: 'deleted' })} /></tbody></table>);
    expect(screen.queryByRole('button', { name: 'Edit loop' })).not.toBeInTheDocument();
  });

  it('opens the edit modal from the Edit button', () => {
    renderRow(makeLoop());
    fireEvent.click(screen.getByRole('button', { name: 'Edit loop' }));
    expect(screen.getByTestId('loop-edit-form')).toBeInTheDocument();
  });

  it('opens the history modal from the History button (even when terminal)', () => {
    renderRow(makeLoop({ state: 'deleted' }));
    // History is available regardless of state; Edit is not.
    fireEvent.click(screen.getByRole('button', { name: 'Loop history' }));
    expect(screen.getByTestId('loop-history-backdrop')).toBeInTheDocument();
  });
});
