// @vitest-environment jsdom
//
// useSubagentTracking owns a bounded per-message token map. The cap
// (MAX_SUBAGENT_TOKEN_ENTRIES) protects against unbounded growth
// during long subagent runs.

import { afterEach, describe, expect, it, vi } from 'vitest';
import { act, renderHook, waitFor } from '@testing-library/react';
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

  it('loads child session references while new_session is running', async () => {
    const part = {
      id: 'tool-1',
      messageId: 'message-1',
      sessionId: 'parent-1',
      timeCreated: 1000,
      data: { type: 'tool', tool: 'new_session', state: { status: 'running', input: { intent: 'Explain' } } },
    } as Part;
    vi.spyOn(api, 'sessionTasks').mockResolvedValue({
      tasks: {},
      children: [{ id: 'child-1', intent: 'Explain', status: 'running', createdAt: 1100 }],
    });

    const { result } = renderHook(() => useSubagentTracking([part], 'parent-1'));

    await waitFor(() => expect(result.current.childSessions[0]?.id).toBe('child-1'));
  });

  it('retries quickly when the child record races the first lookup', async () => {
    vi.useFakeTimers();
    const part = {
      id: 'tool-1',
      messageId: 'message-1',
      sessionId: 'parent-1',
      timeCreated: 1000,
      data: { type: 'tool', tool: 'new_session', state: { status: 'running', input: { intent: 'Explain' } } },
    } as Part;
    const request = vi.spyOn(api, 'sessionTasks')
      .mockResolvedValueOnce({ tasks: {}, children: [] })
      .mockResolvedValue({
        tasks: {},
        children: [{ id: 'child-1', intent: 'Explain', status: 'running', createdAt: 1100 }],
      });

    const { result } = renderHook(() => useSubagentTracking([part], 'parent-1'));
    await act(async () => { await Promise.resolve(); });
    expect(request).toHaveBeenCalledTimes(1);

    await act(async () => { await vi.advanceTimersByTimeAsync(250); });
    expect(request).toHaveBeenCalledTimes(2);
    expect(result.current.childSessions[0]?.id).toBe('child-1');
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

  it('pauses child-link retries while hidden and resumes when visible', async () => {
    vi.useFakeTimers();
    Object.defineProperty(document, 'hidden', { configurable: true, value: true });
    const request = vi.spyOn(api, 'sessionTasks').mockResolvedValue({ tasks: {}, children: [] });
    const part = {
      id: 'tool-1',
      messageId: 'message-1',
      sessionId: 'parent-1',
      timeCreated: 1000,
      data: { type: 'tool', tool: 'new_session', state: { status: 'running', input: { intent: 'Explain' } } },
    } as Part;

    renderHook(() => useSubagentTracking([part], 'parent-1'));
    await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
    expect(request).not.toHaveBeenCalled();

    Object.defineProperty(document, 'hidden', { configurable: true, value: false });
    document.dispatchEvent(new Event('visibilitychange'));
    await act(async () => { await Promise.resolve(); });
    expect(request).toHaveBeenCalledTimes(1);
  });

  it('does not fork child-link polls on repeated visibility events', async () => {
    Object.defineProperty(document, 'hidden', { configurable: true, value: true });
    const request = vi.spyOn(api, 'sessionTasks').mockImplementation(() => new Promise(() => {}));
    const part = {
      id: 'tool-1',
      messageId: 'message-1',
      sessionId: 'parent-1',
      timeCreated: 1000,
      data: { type: 'tool', tool: 'new_session', state: { status: 'running', input: { intent: 'Explain' } } },
    } as Part;

    renderHook(() => useSubagentTracking([part], 'parent-1'));
    Object.defineProperty(document, 'hidden', { configurable: true, value: false });
    document.dispatchEvent(new Event('visibilitychange'));
    document.dispatchEvent(new Event('visibilitychange'));
    await act(async () => { await Promise.resolve(); });
    expect(request).toHaveBeenCalledTimes(1);
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
});
