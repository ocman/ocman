// @vitest-environment jsdom

import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { SessionMetadata } from '../../lib/sessionReducer';
import { useSessionCreation, type UseSessionCreationOptions } from './useSessionCreation';

const list = vi.hoisted(() => vi.fn());
vi.mock('../../lib/api', () => ({ api: { worktree: { list } } }));
vi.mock('../../lib/remoteLog', () => ({ remoteLog: { warn: vi.fn() } }));

const session = { id: 's1', directory: '/src/repo-feature', remoteId: 'box', platform: 'r-box:opencode' } as SessionMetadata;
const trees = { worktrees: [{ path: '/src/repo', main: true, bare: false }] };
function options(): UseSessionCreationOptions {
  return { session, portAvailable: true, caps: { compact: false }, selectedModel: '', activeModel: '',
    selectedAgent: '', activeAgent: '', setSelectedAgent: vi.fn(), navigate: vi.fn() };
}

describe('new-session checkout prefetch', () => {
  beforeEach(() => { list.mockReset(); });

  it('prefetches once and opens repeated drafts without another network round trip', async () => {
    list.mockResolvedValue(trees);
    const opts = options();
    const { result, rerender } = renderHook(({ session }) => useSessionCreation({ ...opts, session }), { initialProps: { session } });
    await act(async () => {});
    expect(list).toHaveBeenCalledTimes(1);
    expect(list).toHaveBeenCalledWith(session.directory, 'box', expect.any(AbortSignal));
    list.mockImplementation(() => new Promise(() => {}));
    rerender({ session: { ...session, title: 'A streamed title' } });
    await act(async () => {
      void result.current.handleNewSession();
      await Promise.resolve();
    });
    expect(opts.navigate).toHaveBeenCalledWith(expect.stringContaining('dir=%2Fsrc%2Frepo&'));
    await act(async () => {
      void result.current.handleNewSession('Another draft');
      await Promise.resolve();
    });
    expect(opts.navigate).toHaveBeenCalledTimes(2);
    expect(list).toHaveBeenCalledTimes(1);
  });

  it('shares an in-flight lookup with an early shortcut press', async () => {
    let resolve!: (value: typeof trees) => void;
    list.mockReturnValue(new Promise((done) => { resolve = done; }));
    const opts = options();
    const { result } = renderHook(() => useSessionCreation(opts));
    let opening!: Promise<void>;
    act(() => { opening = result.current.handleNewSession(); });
    expect(list).toHaveBeenCalledTimes(1);
    expect(opts.navigate).not.toHaveBeenCalled();
    await act(async () => { resolve(trees); await opening; });
    expect(opts.navigate).toHaveBeenCalledOnce();
  });

  it.each(['owner', 'directory', 'unmount'] as const)('cancels stale navigation on %s changes', async (change) => {
    let resolve!: (value: typeof trees) => void;
    list.mockReturnValueOnce(new Promise((done) => { resolve = done; })).mockResolvedValue(trees);
    const opts = options();
    const { result, rerender, unmount } = renderHook(({ session }) => useSessionCreation({ ...opts, session }), { initialProps: { session } });
    let opening!: Promise<void>;
    act(() => { opening = result.current.handleNewSession(); });
    const signal = list.mock.calls[0][2] as AbortSignal;
    if (change === 'unmount') unmount();
    else rerender({ session: change === 'owner' ? { ...session, remoteId: 'other' } : { ...session, directory: '/other/repo' } });
    expect(signal.aborted).toBe(true);
    await act(async () => { resolve(trees); await opening; });
    expect(opts.navigate).not.toHaveBeenCalled();
    if (change !== 'unmount') {
      await act(() => result.current.handleNewSession());
      expect(list).toHaveBeenCalledTimes(2);
      expect(opts.navigate).toHaveBeenCalledOnce();
    }
  });

  it('does not read worktrees without a session', () => {
    renderHook(() => useSessionCreation({ ...options(), session: null }));
    expect(list).not.toHaveBeenCalled();
  });

  it('ignores cancellation errors from an abandoned prefetch', async () => {
    list.mockImplementation((_directory, _owner, signal: AbortSignal) => new Promise((_resolve, reject) => {
      signal.addEventListener('abort', () => reject(new Error('aborted')));
    }));
    const { unmount } = renderHook(() => useSessionCreation(options()));
    await act(async () => { unmount(); });
    const { remoteLog } = await import('../../lib/remoteLog');
    expect(remoteLog.warn).not.toHaveBeenCalled();
  });

  it('uses the layout fallback when no usable main worktree is returned', async () => {
    list.mockResolvedValue({ worktrees: [{ path: '/bare', main: true, bare: true }] });
    const opts = { ...options(), session: { ...session, directory: '/src/.worktrees/repo/feature' } };
    const { result } = renderHook(() => useSessionCreation(opts));
    await act(() => result.current.handleNewSession());
    expect(opts.navigate).toHaveBeenCalledWith(expect.stringContaining('dir=%2Fsrc%2Frepo&'));
    expect(list).toHaveBeenCalledTimes(1);
  });
});
