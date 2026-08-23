// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act, renderHook } from '@testing-library/react';

// Spy on the collaborators before importing the module under test so
// the module captures the mocked references.
const notifyPromptDismissed = vi.fn();
const recheckNotifyData = vi.fn();

vi.mock('./useToastNotify', () => ({
  notifyPromptDismissed: (...args: unknown[]) => notifyPromptDismissed(...args),
}));
vi.mock('./useNotifyData', () => ({
  recheckNotifyData: (...args: unknown[]) => recheckNotifyData(...args),
}));

import {
  __handleResolvedForTests,
  __handleSurfaceForTests,
  __handleSessionChangedForTests,
  __handleQueueUpdatedForTests,
  __resetForTests,
  onSessionChanged,
  onQueueUpdated,
  useGlobalEvents,
} from './useGlobalEvents';

class FakeEventSource {
  static instances: FakeEventSource[] = [];

  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  url: string;

  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }

  addEventListener() {}
  close() {}
  error() { this.onerror?.(); }
}

(globalThis as unknown as { EventSource: typeof FakeEventSource }).EventSource = FakeEventSource;

describe('useGlobalEvents connection', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    FakeEventSource.instances = [];
    __resetForTests();
  });

  afterEach(() => {
    __resetForTests();
    vi.useRealTimers();
  });

  it('opens a fresh stream after a hard connection failure', async () => {
    const { unmount } = renderHook(() => useGlobalEvents());
    expect(FakeEventSource.instances).toHaveLength(1);

    act(() => FakeEventSource.instances[0].error());
    await act(() => vi.runOnlyPendingTimersAsync());

    expect(FakeEventSource.instances).toHaveLength(2);
    expect(FakeEventSource.instances[1].url).toBe('/api/events');
    unmount();
  });

  it('cancels a pending reconnect when the last consumer unmounts', async () => {
    const { unmount } = renderHook(() => useGlobalEvents());
    act(() => FakeEventSource.instances[0].error());

    unmount();
    await act(() => vi.runOnlyPendingTimersAsync());

    expect(FakeEventSource.instances).toHaveLength(1);
  });
});

describe('useGlobalEvents resolved handler', () => {
  beforeEach(() => {
    notifyPromptDismissed.mockClear();
    recheckNotifyData.mockClear();
  });

  it('dismisses the toast and rechecks notify on a valid payload', () => {
    __handleResolvedForTests(
      JSON.stringify({ sessionID: 'sess-1', permissionId: 'p1', reason: 'auto-approved' }),
    );
    expect(notifyPromptDismissed).toHaveBeenCalledWith('sess-1');
    expect(recheckNotifyData).toHaveBeenCalledTimes(1);
  });

  it('dismisses the toast for a resolved question payload', () => {
    __handleResolvedForTests(
      JSON.stringify({ sessionID: 'sess-q', requestId: 'r1', reason: 'rejected' }),
    );
    expect(notifyPromptDismissed).toHaveBeenCalledWith('sess-q');
    expect(recheckNotifyData).toHaveBeenCalledTimes(1);
  });

  it('ignores malformed JSON', () => {
    __handleResolvedForTests('not json');
    expect(notifyPromptDismissed).not.toHaveBeenCalled();
    expect(recheckNotifyData).not.toHaveBeenCalled();
  });

  it('ignores payloads without a sessionID', () => {
    __handleResolvedForTests(JSON.stringify({ permissionId: 'p1' }));
    expect(notifyPromptDismissed).not.toHaveBeenCalled();
    expect(recheckNotifyData).not.toHaveBeenCalled();
  });
});

describe('useGlobalEvents surface handler', () => {
  beforeEach(() => {
    notifyPromptDismissed.mockClear();
    recheckNotifyData.mockClear();
  });

  it('rechecks notify without dismissing a toast (flagged/idle)', () => {
    __handleSurfaceForTests(JSON.stringify({ sessionID: 'sess-2', reason: 'flagged' }));
    expect(recheckNotifyData).toHaveBeenCalledTimes(1);
    expect(notifyPromptDismissed).not.toHaveBeenCalled();
  });

  it('ignores malformed or session-less payloads', () => {
    __handleSurfaceForTests('nope');
    __handleSurfaceForTests(JSON.stringify({ reason: 'flagged' }));
    expect(recheckNotifyData).not.toHaveBeenCalled();
  });
});

describe('useGlobalEvents session.changed handler', () => {
  it('notifies registered listeners with the session id', () => {
    const cb = vi.fn();
    const unsub = onSessionChanged(cb);
    __handleSessionChangedForTests(JSON.stringify({ sessionID: 'sess-new' }));
    expect(cb).toHaveBeenCalledWith('sess-new', undefined, undefined);
    unsub();
    __handleSessionChangedForTests(JSON.stringify({ sessionID: 'sess-2' }));
    expect(cb).toHaveBeenCalledTimes(1); // not called after unsubscribe
  });

  it('forwards a provisional session row when present', () => {
    const cb = vi.fn();
    const unsub = onSessionChanged(cb);
    const session = { id: 'sess-new', directory: '/repo/a' };
    __handleSessionChangedForTests(JSON.stringify({ sessionID: 'sess-new', session }));
    expect(cb).toHaveBeenCalledWith('sess-new', session, undefined);
    unsub();
  });

  it('forwards a session patch when present', () => {
    const cb = vi.fn();
    const unsub = onSessionChanged(cb);
    const patch = { status: 'busy' };
    __handleSessionChangedForTests(JSON.stringify({ sessionID: 'sess-live', patch }));
    expect(cb).toHaveBeenCalledWith('sess-live', undefined, patch);
    unsub();
  });

  it('ignores malformed or session-less payloads', () => {
    const cb = vi.fn();
    const unsub = onSessionChanged(cb);
    __handleSessionChangedForTests('nope');
    __handleSessionChangedForTests(JSON.stringify({ reason: 'x' }));
    expect(cb).not.toHaveBeenCalled();
    unsub();
  });
});

describe('useGlobalEvents queue.updated handler', () => {
  it('notifies registered listeners with the session id and messages', () => {
    const cb = vi.fn();
    const unsub = onQueueUpdated(cb);
    // No messages key → undefined (listener falls back to a refetch).
    __handleQueueUpdatedForTests(JSON.stringify({ sessionID: 'sess-q' }));
    expect(cb).toHaveBeenCalledWith('sess-q', undefined);
    // With messages → forwarded so the listener applies them directly.
    const msgs = [{ id: 'a', text: 'hi', hasImages: false, createdAt: 1 }];
    __handleQueueUpdatedForTests(JSON.stringify({ sessionID: 'sess-q', messages: msgs }));
    expect(cb).toHaveBeenCalledWith('sess-q', msgs);
    unsub();
    __handleQueueUpdatedForTests(JSON.stringify({ sessionID: 'sess-q2' }));
    expect(cb).toHaveBeenCalledTimes(2); // not called after unsubscribe
  });

  it('ignores malformed or session-less payloads', () => {
    const cb = vi.fn();
    const unsub = onQueueUpdated(cb);
    __handleQueueUpdatedForTests('nope');
    __handleQueueUpdatedForTests(JSON.stringify({ reason: 'x' }));
    expect(cb).not.toHaveBeenCalled();
    unsub();
  });
});
