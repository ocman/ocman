// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import { useSubagentTracking } from './useSubagentTracking';
import { api, type Part } from '../../lib/api';

afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
});

describe('useSubagentTracking', () => {
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

  it('keeps the taskLiveOutput reference when a poll returns unchanged data', async () => {
    // taskLiveOutput is part of convertMessages' per-message cache key,
    // so a fresh object every 2s poll re-converts every message.
    vi.useFakeTimers();
    Object.defineProperty(document, 'hidden', { configurable: true, value: false });
    vi.spyOn(api, 'sessionTasks').mockImplementation(async () => ({
      tasks: { ses_child: { messages: [], parts: [] } },
    }));
    const part = {
      id: 'tool-1', messageId: 'message-1', sessionId: 'parent-1', timeCreated: 1000,
      data: { type: 'tool', tool: 'task', state: { status: 'running', metadata: { taskId: 'ses_child' } } },
    } as Part;

    const { result } = renderHook(() => useSubagentTracking([part], 'parent-1'));
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    const first = result.current.taskLiveOutput;
    expect(first.ses_child).toBeDefined();

    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    expect(result.current.taskLiveOutput).toBe(first);
  });

  it('clears task state when the session changes', async () => {
    const { result, rerender } = renderHook(
      ({ sessionId }) => useSubagentTracking([], sessionId),
      { initialProps: { sessionId: 'first' } },
    );
    act(() => {
      result.current.setTaskLiveOutput({ task: { messages: [], parts: [] } });
    });

    rerender({ sessionId: 'second' });
    await act(async () => { await Promise.resolve(); });

    expect(result.current.taskLiveOutput).toEqual({});
  });
});
