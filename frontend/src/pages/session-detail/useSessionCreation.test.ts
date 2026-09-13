// @vitest-environment jsdom

import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { SessionMetadata } from '../../lib/sessionReducer';

vi.mock('../../lib/api', () => ({ api: { compactSession: vi.fn().mockResolvedValue(undefined) } }));
vi.mock('../../lib/remoteLog', () => ({ remoteLog: { error: vi.fn() } }));
const createSessionWithLaunch = vi.fn();
vi.mock('../../lib/createSessionWithLaunch', () => ({
  createSessionWithLaunch: (...args: unknown[]) => createSessionWithLaunch(...args),
}));
const createSession = vi.fn();
const launchOpencodeInTmux = vi.fn();
const seedNewSession = vi.fn();
vi.mock('../../lib/apiStore', () => ({
  useApiStore: (selector: (s: Record<string, unknown>) => unknown) =>
    selector({ createSession, launchOpencodeInTmux, seedNewSession }),
}));

import { api } from '../../lib/api';
import { useSessionCreation, type UseSessionCreationOptions } from './useSessionCreation';

const session = { id: 's1', directory: '/repo/a', platform: 'r-x:opencode', remoteId: 'r-x' } as SessionMetadata;

function opts(over: Partial<UseSessionCreationOptions> = {}): UseSessionCreationOptions {
  return {
    session,
    portAvailable: true,
    caps: { compact: true },
    tmuxAvailable: true,
    selectedModel: '',
    activeModel: 'prov/model',
    selectedAgent: '',
    activeAgent: 'build',
    setSelectedAgent: vi.fn(),
    navigateToSession: vi.fn(),
    onCreateError: vi.fn(),
    ...over,
  };
}

describe('useSessionCreation', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    createSessionWithLaunch.mockResolvedValue({ id: 'new-1', directory: '/repo/a/wt' });
  });

  it('inherits the open session platform only for the same project', async () => {
    const o = opts();
    const { result } = renderHook(() => useSessionCreation(o));

    await act(() => result.current.handleNewSessionInDirectory('/repo/.worktrees/a/feat'));
    expect(createSessionWithLaunch).toHaveBeenLastCalledWith(
      { createSession, launchOpencodeInTmux, tmuxAvailable: true },
      expect.objectContaining({ directory: '/repo/.worktrees/a/feat', platform: 'r-x:opencode' }),
    );
    expect(seedNewSession).toHaveBeenCalledWith('new-1', '/repo/a/wt', 'r-x:opencode', undefined, undefined);
    expect(o.navigateToSession).toHaveBeenCalledWith('new-1');

    await act(() => result.current.handleNewSessionInDirectory('/repo/other'));
    expect(createSessionWithLaunch).toHaveBeenLastCalledWith(
      expect.anything(),
      expect.objectContaining({ directory: '/repo/other', platform: undefined }),
    );
  });

  it('reports create failures through onCreateError', async () => {
    createSessionWithLaunch.mockRejectedValueOnce(new Error('nope'));
    const o = opts();
    const { result } = renderHook(() => useSessionCreation(o));
    await act(() => result.current.handleNewSession('title'));
    expect(o.onCreateError).toHaveBeenCalled();
    expect(o.navigateToSession).not.toHaveBeenCalled();
  });

  it('compacts with the selected model and restores the agent', async () => {
    const o = opts({ selectedModel: 'anthropic/claude', selectedAgent: 'plan' });
    const { result } = renderHook(() => useSessionCreation(o));
    await act(() => result.current.handleCompact());
    expect(api.compactSession).toHaveBeenCalledWith('s1', 'anthropic', 'claude');
    expect(o.setSelectedAgent).toHaveBeenCalledWith('plan');

    const gated = opts({ caps: { compact: false } });
    const { result: r2 } = renderHook(() => useSessionCreation(gated));
    await act(() => r2.current.handleCompact());
    expect(api.compactSession).toHaveBeenCalledTimes(1);
  });
});
