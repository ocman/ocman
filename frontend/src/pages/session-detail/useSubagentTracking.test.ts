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
  it('keeps polling a resumed task until its child becomes busy', async () => {
    vi.useFakeTimers();
    Object.defineProperty(document, 'hidden', { configurable: true, value: false });
    const request = vi.spyOn(api, 'sessionTasks')
      .mockResolvedValueOnce({ tasks: { ses_child: { messages: [], parts: [], status: 'waiting' } } })
      .mockResolvedValueOnce({ tasks: { ses_child: { messages: [], parts: [], status: 'waiting' } } })
      .mockResolvedValue({ tasks: { ses_child: { messages: [], parts: [], status: 'busy' } } });
    const part = {
      id: 'tool', messageId: 'message', sessionId: 'parent', timeCreated: 1000,
      data: { type: 'tool', tool: 'task', state: { status: 'running', metadata: { taskId: 'ses_child' } } },
    } as Part;
    const { result } = renderHook(() => useSubagentTracking([part], 'parent'));
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    await act(async () => { await vi.advanceTimersByTimeAsync(4000); });
    expect(request.mock.calls.length).toBeGreaterThanOrEqual(3);
    expect(result.current.taskLiveOutput.ses_child.status).toBe('busy');
  });

  it('continues tracking a busy child after the launch tool completes', async () => {
    vi.useFakeTimers();
    Object.defineProperty(document, 'hidden', { configurable: true, value: false });
    const request = vi.spyOn(api, 'sessionTasks').mockResolvedValue({
      tasks: { ses_child: { messages: [], parts: [], status: 'busy' } },
    });
    const part = {
      id: 'tool', messageId: 'message', sessionId: 'parent', timeCreated: 1000,
      data: { type: 'tool', tool: 'task', state: { status: 'completed', metadata: { taskId: 'ses_child' } } },
    } as Part;
    const { result } = renderHook(() => useSubagentTracking([part], 'parent'));
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    expect(result.current.runningTaskIds).toEqual([{ taskId: 'ses_child', status: 'running' }]);
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    expect(request.mock.calls.length).toBeGreaterThan(1);
    request.mockResolvedValue({ tasks: { ses_child: { messages: [], parts: [], status: 'waiting' } } });
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    expect(result.current.runningTaskIds).toEqual([]);
    const settledCalls = request.mock.calls.length;
    await act(async () => { await vi.advanceTimersByTimeAsync(4000); });
    expect(request).toHaveBeenCalledTimes(settledCalls);
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
