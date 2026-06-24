// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import type { Loop, LoopDetail } from '../lib/api.types';
import { LoopsPane } from './LoopsPane';

// LoopsPane renders <Link> elements, which need a router context.
function renderPane(dir = '/src/ocman') {
  return render(
    <MemoryRouter>
      <LoopsPane directory={dir} />
    </MemoryRouter>,
  );
}

// --- mock the store: expose a controllable state object ---
const state: {
  loops: Loop[];
  loading: boolean;
  error: string | null;
  load: ReturnType<typeof vi.fn>;
  remove: ReturnType<typeof vi.fn>;
  pause: ReturnType<typeof vi.fn>;
  resume: ReturnType<typeof vi.fn>;
  trigger: ReturnType<typeof vi.fn>;
  update: ReturnType<typeof vi.fn>;
} = {
  loops: [],
  loading: false,
  error: null,
  load: vi.fn(),
  remove: vi.fn(),
  pause: vi.fn(),
  resume: vi.fn(),
  trigger: vi.fn(),
  update: vi.fn(),
};

vi.mock('../lib/loopsStore', () => ({
  // The component calls useLoopsStore(selector); run the selector
  // against our controllable state object.
  useLoopsStore: (selector: (s: typeof state) => unknown) => selector(state),
}));

// --- mock the api for lazy history fetch ---
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
    lastSummary: 'prompted root session',
    triggerConfig: { interval_seconds: 1800 },
    stopConditions: { max_iterations: 48, max_cost_usd: 5 },
    ...overrides,
  };
}

beforeEach(() => {
  state.loops = [];
  state.loading = false;
  state.error = null;
  state.load = vi.fn();
  state.remove = vi.fn();
  state.pause = vi.fn();
  state.resume = vi.fn();
  state.trigger = vi.fn();
  state.update = vi.fn().mockResolvedValue(undefined);
  getMock.mockReset();
});

describe('LoopsPane', () => {
  it('shows an empty message when there are no loops', () => {
    renderPane();
    expect(screen.getByTestId('loops-pane-empty')).toBeInTheDocument();
  });

  it('lists loops with title and state (compact row)', () => {
    state.loops = [makeLoop()];
    renderPane();
    expect(screen.getByText('Check PRs mergeable')).toBeInTheDocument();
    expect(screen.getByTestId('loop-state')).toHaveTextContent('active');
    expect(screen.getByText('prompted root session')).toBeInTheDocument();
  });

  it('loads the directory-scoped list on mount', () => {
    renderPane();
    expect(state.load).toHaveBeenCalledWith({ dir: '/src/ocman' });
  });

  it('expands history inline from the History button', async () => {
    state.loops = [makeLoop()];
    const detail: LoopDetail = { ...makeLoop(), iterations: [], children: [], subLoops: [] };
    getMock.mockResolvedValueOnce(detail);
    renderPane();
    fireEvent.click(screen.getByRole('button', { name: 'History' }));
    await waitFor(() => expect(screen.getByTestId('loop-history')).toBeInTheDocument());
    expect(getMock).toHaveBeenCalledWith('loop_1');
    // Toggling again collapses it.
    fireEvent.click(screen.getByRole('button', { name: 'Hide history' }));
    expect(screen.queryByTestId('loop-history')).not.toBeInTheDocument();
  });

  it('keeps the row compact: only History and Edit, no lifecycle controls', () => {
    state.loops = [makeLoop({ state: 'active' })];
    renderPane();
    expect(screen.getByRole('button', { name: 'History' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Edit' })).toBeInTheDocument();
    // Lifecycle controls live in the Edit modal now, not on the row.
    expect(screen.queryByRole('button', { name: 'Pause' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Trigger now' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Stop' })).not.toBeInTheDocument();
  });

  it('hides Edit on a terminal (deleted) loop', () => {
    state.loops = [makeLoop({ state: 'deleted' })];
    renderPane();
    expect(screen.queryByRole('button', { name: 'Edit' })).not.toBeInTheDocument();
    // History is still available to inspect a deleted loop.
    expect(screen.getByRole('button', { name: 'History' })).toBeInTheDocument();
  });

  it('edits settings and saves via update', async () => {
    state.loops = [makeLoop({ state: 'active' })];
    renderPane();

    fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    const form = await screen.findByTestId('loop-edit-form');
    expect(form).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText('Max cost (USD)'), { target: { value: '9' } });
    fireEvent.change(screen.getByLabelText('Session per iteration'), { target: { value: 'reuse' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() => expect(state.update).toHaveBeenCalled());
    const [id, req] = state.update.mock.calls[0];
    expect(id).toBe('loop_1');
    expect(req.session_mode).toBe('reuse');
    expect(req.stop_conditions.max_cost_usd).toBe(9);
  });

  it('blocks saving when the budget is removed', async () => {
    state.loops = [makeLoop({ state: 'active' })];
    renderPane();
    fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    await screen.findByTestId('loop-edit-form');
    fireEvent.change(screen.getByLabelText('Max cost (USD)'), { target: { value: '' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(state.update).not.toHaveBeenCalled();
    expect(screen.getByText(/budget is required/i)).toBeInTheDocument();
  });
});
