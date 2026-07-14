import { beforeEach, describe, it, expect, vi } from 'vitest';
import type { Loop } from './api.types';

// Mock the api module before importing the store.
const listMock = vi.fn();
const deleteMock = vi.fn();
const restartMock = vi.fn();
vi.mock('./api', () => ({
  api: {
    loops: {
      list: (...args: unknown[]) => listMock(...args),
      delete: (...args: unknown[]) => deleteMock(...args),
      pause: vi.fn(),
      resume: vi.fn(),
      restart: (...args: unknown[]) => restartMock(...args),
      step: vi.fn(),
      trigger: vi.fn(),
      update: vi.fn(),
    },
  },
}));
// useGlobalEvents registers a module-level listener; stub it.
vi.mock('./useGlobalEvents', () => ({
  onLoopUpdated: () => () => {},
  onWorkflowRunUpdated: () => () => {},
  onWorkflowTriggerUpdated: () => () => {},
}));

import { useLoopsStore } from './loopsStore';

function makeLoop(id: string, overrides: Partial<Loop> = {}): Loop {
  return {
    id,
    platform: 'opencode',
    rootSessionID: 's1',
    directory: '/src/ocman',
    projectName: 'ocman',
    title: `Loop ${id}`,
    currentTask: '',
    pattern: '',
    triggerType: 'schedule',
    actionType: 'prompt_root',
    actionTemplate: '',
    sessionMode: 'fresh',
    state: 'active',
    iteration: 0,
    errorStreak: 0,
    tokensUsed: 0,
    costUSD: 0,
    lastFiredAt: 0,
    createdAt: 0,
    updatedAt: 0,
    completedAt: 0,
    lastSummary: '',
    triggerConfig: { interval_seconds: 60 },
    stopConditions: { max_iterations: 10, max_cost_usd: 2 },
    ...overrides,
  };
}

beforeEach(() => {
  listMock.mockReset();
  deleteMock.mockReset();
  restartMock.mockReset();
  useLoopsStore.setState({ loops: [], loading: false, error: null, filter: {} });
});

describe('loopsStore', () => {
  it('loads loops with a filter and stores them', async () => {
    listMock.mockResolvedValueOnce([makeLoop('a'), makeLoop('b')]);
    await useLoopsStore.getState().load({ dir: '/src/ocman' });
    const s = useLoopsStore.getState();
    expect(s.loops).toHaveLength(2);
    expect(s.filter).toEqual({ dir: '/src/ocman' });
    expect(listMock).toHaveBeenCalledWith({ dir: '/src/ocman' });
  });

  it('records an error on load failure', async () => {
    listMock.mockRejectedValueOnce(new Error('boom'));
    await useLoopsStore.getState().load();
    expect(useLoopsStore.getState().error).toBe('boom');
    expect(useLoopsStore.getState().loading).toBe(false);
  });

  it('remove calls the delete api then refreshes with the current filter', async () => {
    useLoopsStore.setState({ filter: { session: 's1' } });
    deleteMock.mockResolvedValueOnce({ ok: true });
    listMock.mockResolvedValueOnce([]);
    await useLoopsStore.getState().remove('a');
    expect(deleteMock).toHaveBeenCalledWith('a');
    expect(listMock).toHaveBeenCalledWith({ session: 's1' });
    expect(useLoopsStore.getState().loops).toHaveLength(0);
  });

  it('restart calls the restart api then refreshes with the current filter', async () => {
    useLoopsStore.setState({ filter: { dir: '/src/ocman' } });
    restartMock.mockResolvedValueOnce(makeLoop('a', { state: 'active' }));
    listMock.mockResolvedValueOnce([makeLoop('a', { state: 'active' })]);
    await useLoopsStore.getState().restart('a');
    expect(restartMock).toHaveBeenCalledWith('a');
    expect(listMock).toHaveBeenCalledWith({ dir: '/src/ocman' });
    expect(useLoopsStore.getState().loops[0].state).toBe('active');
  });
});
