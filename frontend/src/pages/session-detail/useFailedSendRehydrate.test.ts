// @vitest-environment jsdom

import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Message, Part } from '../../lib/api';
import { recordFailedSend, type FailedSend } from '../../lib/failedSends';
import type { UsePendingSendResult } from './usePendingSend';
import { useFailedSendRehydrate, type UseFailedSendRehydrateOptions } from './useFailedSendRehydrate';

const entry = (id: string, text: string): FailedSend => ({ id, text, error: 'boom', failedAt: 1, model: 'p/m' });

function pendingMock(pendingValue: UsePendingSendResult['pending'] = null): UsePendingSendResult {
  return { pending: pendingValue, begin: vi.fn(), fail: vi.fn(), clear: vi.fn(), observeMessages: vi.fn() };
}

function opts(over: Partial<UseFailedSendRehydrateOptions> = {}): UseFailedSendRehydrateOptions {
  return { id: 's1', sessionLoaded: true, messages: [], parts: [], pending: pendingMock(), ...over };
}

describe('useFailedSendRehydrate', () => {
  beforeEach(() => localStorage.clear());

  it('loads the persisted list for the route id and replays the first ghost once', () => {
    recordFailedSend('s1', entry('f1', 'hello'));
    const o = opts();
    const { result, rerender } = renderHook((p: UseFailedSendRehydrateOptions) => useFailedSendRehydrate(p), { initialProps: o });
    expect(result.current.failedSends.map((e) => e.id)).toEqual(['f1']);
    expect(o.pending.begin).toHaveBeenCalledWith('hello', undefined, { model: 'p/m', agent: undefined, reasoning: undefined });
    expect(o.pending.fail).toHaveBeenCalledWith('boom');

    rerender({ ...o, messages: [] });
    expect(o.pending.begin).toHaveBeenCalledTimes(1);
  });

  it('skips entries whose text already landed as a real user message', () => {
    recordFailedSend('s1', entry('f1', 'hello'));
    const messages = [{ id: 'm1', sessionId: 's1', timeCreated: 1, data: { role: 'user' } }] as Message[];
    const parts = [{ id: 'p1', messageId: 'm1', sessionId: 's1', data: { type: 'text', text: 'hello' } }] as Part[];
    const o = opts({ messages, parts });
    renderHook(() => useFailedSendRehydrate(o));
    expect(o.pending.begin).not.toHaveBeenCalled();
  });

  it('waits while a send is in flight and while the session is loading', () => {
    recordFailedSend('s1', entry('f1', 'hello'));
    const inFlight = opts({ pending: pendingMock({ id: 'x', text: 't', startedAt: 0 } as UsePendingSendResult['pending']) });
    renderHook(() => useFailedSendRehydrate(inFlight));
    expect(inFlight.pending.begin).not.toHaveBeenCalled();

    const loading = opts({ sessionLoaded: false });
    renderHook(() => useFailedSendRehydrate(loading));
    expect(loading.pending.begin).not.toHaveBeenCalled();
  });

  it('swaps the list on route change and supports functional updates', () => {
    recordFailedSend('s1', entry('f1', 'one'));
    recordFailedSend('s2', entry('f2', 'two'));
    const { result, rerender } = renderHook((p: UseFailedSendRehydrateOptions) => useFailedSendRehydrate(p), { initialProps: opts() });
    expect(result.current.failedSends.map((e) => e.id)).toEqual(['f1']);
    rerender(opts({ id: 's2' }));
    expect(result.current.failedSends.map((e) => e.id)).toEqual(['f2']);
    act(() => result.current.setFailedSends((prev) => prev.filter((e) => e.id !== 'f2')));
    expect(result.current.failedSends).toEqual([]);
  });
});
