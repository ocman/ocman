// @vitest-environment jsdom

import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { SessionMetadata } from '../../lib/sessionReducer';

const listWorktrees = vi.fn();
vi.mock('../../lib/api', () => ({
  api: { compactSession: vi.fn().mockResolvedValue(undefined), worktree: { list: (...args: unknown[]) => listWorktrees(...args) } },
}));
vi.mock('../../lib/remoteLog', () => ({ remoteLog: { error: vi.fn(), warn: vi.fn() } }));

import { api } from '../../lib/api';
import { useSessionCreation, type UseSessionCreationOptions } from './useSessionCreation';

const session = { id: 's1', directory: '/repo/a', platform: 'r-x:opencode', remoteId: 'r-x' } as SessionMetadata;

function opts(over: Partial<UseSessionCreationOptions> = {}): UseSessionCreationOptions {
  return {
    session,
    portAvailable: true,
    caps: { compact: true },
    selectedModel: '',
    activeModel: 'prov/model',
    selectedAgent: '',
    activeAgent: 'build',
    setSelectedAgent: vi.fn(),
    navigate: vi.fn(),
    ...over,
  };
}

describe('useSessionCreation', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    listWorktrees.mockRejectedValue(new Error('not a git repository'));
  });

  // A new conversation is a route: no session exists until the first prompt.
  it('opens the new-conversation route, inheriting the platform only for the same project', () => {
    const o = opts();
    const { result } = renderHook(() => useSessionCreation(o));

    act(() => result.current.handleNewSessionInDirectory('/repo/.worktrees/a/feat'));
    expect(o.navigate).toHaveBeenLastCalledWith('/session/new?dir=%2Frepo%2F.worktrees%2Fa%2Ffeat&platform=r-x%3Aopencode');

    act(() => result.current.handleNewSessionInDirectory('/repo/other'));
    expect(o.navigate).toHaveBeenLastCalledWith('/session/new?dir=%2Frepo%2Fother');

    act(() => result.current.handleNewSessionInDirectory('/remote/repo', 'box', 'r-box:opencode'));
    expect(o.navigate).toHaveBeenLastCalledWith('/session/new?dir=%2Fremote%2Frepo&remoteId=box&platform=r-box%3Aopencode');
  });

  it('does not inherit a remote platform for an explicitly local checkout at the same path', async () => {
    const o = opts();
    const { result } = renderHook(() => useSessionCreation(o));
    await act(() => result.current.handleNewSessionInDirectory('/repo/a', 'local'));
    expect(o.navigate).toHaveBeenLastCalledWith('/session/new?dir=%2Frepo%2Fa');
  });

  it('resolves the main checkout from the owner for a worktree outside the managed layout', async () => {
    listWorktrees.mockResolvedValue({ worktrees: [
      { path: '/src/repo', branch: 'main', main: true, bare: false },
      { path: '/src/repo-feature', branch: 'feature', main: false, bare: false },
    ] });
    const o = opts({ session: { ...session, directory: '/src/repo-feature' } });
    const { result } = renderHook(() => useSessionCreation(o));
    await act(() => result.current.handleNewSession());
    expect(listWorktrees).toHaveBeenCalledWith('/src/repo-feature', 'r-x');
    expect(o.navigate).toHaveBeenLastCalledWith('/session/new?dir=%2Fsrc%2Frepo&remoteId=r-x&platform=r-x%3Aopencode');
  });

  it('falls back to the directory layout when the worktree lookup fails', async () => {
    const o = opts({ session: { ...session, directory: '/src/.worktrees/repo/feat' } });
    const { result } = renderHook(() => useSessionCreation(o));
    await act(() => result.current.handleNewSession());
    expect(o.navigate).toHaveBeenLastCalledWith('/session/new?dir=%2Fsrc%2Frepo&remoteId=r-x&platform=r-x%3Aopencode');
  });

  it('starts a new conversation beside the open session, with an optional title', async () => {
    const o = opts();
    const { result } = renderHook(() => useSessionCreation(o));
    await act(() => result.current.handleNewSession('Fix login'));
    expect(o.navigate).toHaveBeenCalledWith('/session/new?dir=%2Frepo%2Fa&remoteId=r-x&platform=r-x%3Aopencode&title=Fix+login');

    const none = opts({ session: null });
    const { result: r2 } = renderHook(() => useSessionCreation(none));
    await act(() => r2.current.handleNewSession());
    expect(none.navigate).not.toHaveBeenCalled();
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
