// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import type { CapabilitiesResponse } from './api';

// A remote that connects after the page loaded its capabilities must
// become usable without a browser reload, for consumers mounted before
// *and* after the cache warmed.

const capabilities = vi.fn<() => Promise<CapabilitiesResponse>>();
vi.mock('./api', () => ({ api: { capabilities: () => capabilities() } }));

const hostCaps = { gitDiff: true, worktrees: true, tmux: true, projects: true, whisper: true, opencodeLaunch: true };
const resp = (...remotes: string[]): CapabilitiesResponse => ({
  platforms: [],
  worktreeSessions: true,
  hosts: [
    { remoteId: 'local', remoteName: 'This machine', capabilities: hostCaps },
    ...remotes.map((id) => ({ remoteId: id, remoteName: id, capabilities: hostCaps })),
  ],
});

const flush = () => act(async () => { await vi.advanceTimersByTimeAsync(0); });

describe('useOpencodeLaunch with a late-connecting owner', () => {
  beforeEach(() => {
    vi.resetModules();
    vi.useFakeTimers();
    capabilities.mockReset();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('turns on once the owner connects, and updates warm-cache consumers', async () => {
    // Initial load and the immediate miss refetch: B not connected yet.
    capabilities.mockResolvedValueOnce(resp()).mockResolvedValueOnce(resp()).mockResolvedValue(resp('B'));
    const mod = await import('./useCapabilities');

    const early = renderHook(() => mod.useOpencodeLaunch('B'));
    await flush();
    expect(early.result.current).toBe(false);
    expect(capabilities).toHaveBeenCalledTimes(2);

    // Mounted against the warm (B-less) cache: it must still hear updates.
    const warm = renderHook(() => mod.useCapabilities());
    expect(warm.result.current?.hosts?.map((h) => h.remoteId)).toEqual(['local']);

    await act(async () => { await vi.advanceTimersByTimeAsync(10_000); });
    expect(early.result.current).toBe(true);
    expect(warm.result.current?.hosts?.map((h) => h.remoteId)).toEqual(['local', 'B']);

    // Owner present: polling stops.
    const calls = capabilities.mock.calls.length;
    await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
    expect(capabilities).toHaveBeenCalledTimes(calls);
  });

  it('stops re-asking once no consumer waits on the absent owner', async () => {
    capabilities.mockResolvedValue(resp());
    const mod = await import('./useCapabilities');
    const view = renderHook(() => mod.useOpencodeLaunch('gone'));
    await flush();
    expect(view.result.current).toBe(false);
    view.unmount();
    const calls = capabilities.mock.calls.length;
    await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
    expect(capabilities).toHaveBeenCalledTimes(calls);
  });

  it('never polls for this machine', async () => {
    capabilities.mockResolvedValue(resp());
    const mod = await import('./useCapabilities');
    const view = renderHook(() => mod.useOpencodeLaunch('local'));
    await flush();
    await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
    expect(view.result.current).toBe(true);
    expect(capabilities).toHaveBeenCalledTimes(1);
  });
});
