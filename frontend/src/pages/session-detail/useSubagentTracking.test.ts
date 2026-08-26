// @vitest-environment jsdom
//
// useSubagentTracking owns a bounded per-message token map. The cap
// (MAX_SUBAGENT_TOKEN_ENTRIES) protects against unbounded growth
// during long subagent runs.

import { afterEach, describe, expect, it, vi } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import { useSubagentTracking } from './useSubagentTracking';
import { api, type Part } from '../../lib/api';

afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
});

describe('useSubagentTracking', () => {
  it('exposes a stable setSubagentTokens identity across renders', () => {
    // Regression: a previous version returned a fresh function
    // every render. Listing the setter in `useSessionStatus`'s TPS
    // effect deps then caused a render loop ("Maximum update depth
    // exceeded") during active streaming.
    const { result, rerender } = renderHook(() => useSubagentTracking([], 's1'));
    const first = result.current.setSubagentTokens;
    rerender();
    expect(result.current.setSubagentTokens).toBe(first);
    rerender();
    expect(result.current.setSubagentTokens).toBe(first);
  });

  it('trims subagent token entries past the cap', () => {
    const { result } = renderHook(() => useSubagentTracking([], 's1'));

    // Push more entries than the cap (256) and confirm the cap is
    // enforced. We use the functional updater so the cap helper is
    // exercised end-to-end. The state update is wrapped in act() so
    // React commits before we read back.
    const N = 300;
    act(() => {
      result.current.setSubagentTokens(() => {
        const map = new Map<string, { output: number; created: number }>();
        for (let i = 0; i < N; i++) {
          map.set(`m${i}`, { output: i, created: Date.now() + i });
        }
        return map;
      });
    });

    // The trimmer keeps the most recently inserted entries
    // (insertion order = chronological in this test), so the
    // lowest-id entries should be evicted.
    expect(result.current.subagentTokens.size).toBeLessThanOrEqual(256);
    expect(result.current.subagentTokens.has('m0')).toBe(false);
    expect(result.current.subagentTokens.has(`m${N - 1}`)).toBe(true);
  });

  it('does not treat retired new_session calls as native tasks', async () => {
    const request = vi.spyOn(api, 'sessionTasks').mockResolvedValue({ tasks: {} });
    const part = {
      id: 'tool-1', messageId: 'message-1', sessionId: 'parent-1', timeCreated: 1000,
      data: { type: 'tool', tool: 'new_session', state: { status: 'running' } },
    } as Part;

    renderHook(() => useSubagentTracking([part], 'parent-1'));
    await act(async () => { await Promise.resolve(); });
    expect(request).not.toHaveBeenCalled();
  });

  it('does not poll running tasks while hidden', async () => {
    vi.useFakeTimers();
    Object.defineProperty(document, 'hidden', { configurable: true, value: true });
    const request = vi.spyOn(api, 'sessionTasks').mockResolvedValue({ tasks: {} });
    const part = {
      id: 'tool-1',
      messageId: 'message-1',
      sessionId: 'parent-1',
      timeCreated: 1000,
      data: {
        type: 'tool',
        tool: 'task',
        state: { status: 'running', metadata: { taskId: 'ses_child' } },
      },
    } as Part;

    renderHook(() => useSubagentTracking([part], 'parent-1'));
    await act(async () => { await vi.advanceTimersByTimeAsync(4000); });
    expect(request).not.toHaveBeenCalled();

    Object.defineProperty(document, 'hidden', { configurable: true, value: false });
  });

  it('does not overlap slow running-task polls', async () => {
    vi.useFakeTimers();
    Object.defineProperty(document, 'hidden', { configurable: true, value: false });
    const request = vi.spyOn(api, 'sessionTasks').mockImplementation(() => new Promise(() => {}));
    const part = {
      id: 'tool-1',
      messageId: 'message-1',
      sessionId: 'parent-1',
      timeCreated: 1000,
      data: {
        type: 'tool',
        tool: 'task',
        state: { status: 'running', metadata: { taskId: 'ses_child' } },
      },
    } as Part;

    renderHook(() => useSubagentTracking([part], 'parent-1'));
    await act(async () => { await vi.advanceTimersByTimeAsync(6000); });
    expect(request).toHaveBeenCalledTimes(1);
  });

  it('restarts polling when a running task is replaced', async () => {
    Object.defineProperty(document, 'hidden', { configurable: true, value: false });
    const request = vi.spyOn(api, 'sessionTasks').mockResolvedValue({ tasks: {} });
    const part = (taskId: string) => ({
      id: `tool-${taskId}`, messageId: 'message-1', sessionId: 'parent-1', timeCreated: 1000,
      data: { type: 'tool', tool: 'task', state: { status: 'running', metadata: { taskId } } },
    }) as Part;
    const { rerender } = renderHook(
      ({ taskId }) => useSubagentTracking([part(taskId)], 'parent-1'),
      { initialProps: { taskId: 'first' } },
    );
    await act(async () => { await Promise.resolve(); });

    rerender({ taskId: 'second' });
    await act(async () => { await Promise.resolve(); });

    expect(request.mock.calls.at(-1)?.[1]).toEqual(['second']);
  });

  it('clears task state when the session changes', async () => {
    const { result, rerender } = renderHook(
      ({ sessionId }) => useSubagentTracking([], sessionId),
      { initialProps: { sessionId: 'first' } },
    );
    act(() => {
      result.current.setSubagentTokens(new Map([['message', { output: 1, created: 1 }]]));
      result.current.setTaskLiveOutput({ task: { messages: [], parts: [] } });
    });

    rerender({ sessionId: 'second' });
    await act(async () => { await Promise.resolve(); });

    expect(result.current.subagentTokens.size).toBe(0);
    expect(result.current.taskLiveOutput).toEqual({});
  });
});
