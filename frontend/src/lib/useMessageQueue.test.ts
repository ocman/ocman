// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderHook, waitFor, act } from '@testing-library/react';
import { useMessageQueue } from './useMessageQueue';
import { api, type QueuedMessage } from './api';

vi.mock('./api', () => ({
  api: {
    queuedMessages: vi.fn(),
    deleteQueuedMessage: vi.fn().mockResolvedValue(undefined),
    moveQueuedMessage: vi.fn().mockResolvedValue(undefined),
  },
}));

// Capture the registered listeners so we can drive them.
let listener: ((sid: string, messages?: QueuedMessage[]) => void) | null = null;
let connectListener: (() => void) | null = null;
vi.mock('./useGlobalEvents', () => ({
  onQueueUpdated: vi.fn((cb: (sid: string, messages?: QueuedMessage[]) => void) => {
    listener = cb;
    return () => { listener = null; };
  }),
  onSseConnect: vi.fn((cb: () => void) => {
    connectListener = cb;
    return () => { connectListener = null; };
  }),
}));

vi.mock('./remoteLog', () => ({ remoteLog: { error: vi.fn() } }));

const mkMsg = (id: string, text: string): QueuedMessage => ({
  id, text, hasImages: false, createdAt: 1,
});

afterEach(() => {
  vi.clearAllMocks();
  listener = null;
  connectListener = null;
});

describe('useMessageQueue', () => {
  it('fetches the queue on mount', async () => {
    vi.mocked(api.queuedMessages).mockResolvedValue([mkMsg('a', 'one')]);
    const { result } = renderHook(() => useMessageQueue('s1', 'opencode'));
    await waitFor(() => expect(result.current.queue).toHaveLength(1));
    expect(result.current.queue[0].text).toBe('one');
    expect(api.queuedMessages).toHaveBeenCalledWith('s1', 'opencode');
  });

  it('keeps an enqueued item when the effect re-runs (platform resolves)', async () => {
    // Mount with platform still undefined (session not fully loaded yet).
    vi.mocked(api.queuedMessages).mockResolvedValue([]);
    const { result, rerender } = renderHook(
      ({ p }: { p?: string }) => useMessageQueue('s1', p),
      { initialProps: { p: undefined as string | undefined } },
    );
    await waitFor(() => expect(result.current.queue).toHaveLength(0));

    // A mid-turn enqueue broadcast arrives and shows the item.
    act(() => { listener?.('s1', [mkMsg('a', 'one')]); });
    expect(result.current.queue).toHaveLength(1);

    // The session finishes loading → platform resolves → effect re-runs.
    // The item MUST survive (not be cleared by a re-mount reset).
    vi.mocked(api.queuedMessages).mockResolvedValue([mkMsg('a', 'one')]);
    rerender({ p: 'opencode' });
    await act(async () => { await Promise.resolve(); });
    expect(result.current.queue.map((m) => m.id)).toEqual(['a']);
  });

  it('applies queue messages carried by the broadcast without a refetch', async () => {
    vi.mocked(api.queuedMessages).mockResolvedValue([mkMsg('a', 'one')]);
    const { result } = renderHook(() => useMessageQueue('s1', 'opencode'));
    await waitFor(() => expect(result.current.queue).toHaveLength(1));

    const callsBefore = vi.mocked(api.queuedMessages).mock.calls.length;
    // Broadcast for THIS session carries the full queue — applied directly.
    act(() => { listener?.('s1', [mkMsg('a', 'one'), mkMsg('b', 'two')]); });
    expect(result.current.queue.map((m) => m.id)).toEqual(['a', 'b']);
    // No refetch: the payload was authoritative.
    expect(vi.mocked(api.queuedMessages).mock.calls.length).toBe(callsBefore);

    // A broadcast for a different session is ignored.
    act(() => { listener?.('other', [mkMsg('z', 'zz')]); });
    expect(result.current.queue.map((m) => m.id)).toEqual(['a', 'b']);
  });

  it('ignores a broadcast that omits messages (no refetch)', async () => {
    vi.mocked(api.queuedMessages).mockResolvedValue([mkMsg('a', 'one')]);
    const { result } = renderHook(() => useMessageQueue('s1', 'opencode'));
    await waitFor(() => expect(result.current.queue).toHaveLength(1));

    // The backend always carries the full list; a message-less payload is
    // ignored (no racy refetch). State is unchanged.
    const calls = vi.mocked(api.queuedMessages).mock.calls.length;
    act(() => { listener?.('s1'); });
    expect(result.current.queue.map((m) => m.id)).toEqual(['a']);
    expect(vi.mocked(api.queuedMessages).mock.calls.length).toBe(calls);
  });

  it('optimistically removes then calls the api', async () => {
    vi.mocked(api.queuedMessages).mockResolvedValue([mkMsg('a', 'one'), mkMsg('b', 'two')]);
    const { result } = renderHook(() => useMessageQueue('s1', 'opencode'));
    await waitFor(() => expect(result.current.queue).toHaveLength(2));

    act(() => { result.current.remove('a'); });
    // Optimistic: 'a' gone immediately.
    expect(result.current.queue.map((m) => m.id)).toEqual(['b']);
    expect(api.deleteQueuedMessage).toHaveBeenCalledWith('s1', 'a', 'opencode');
  });

  it('move calls the api with the direction', async () => {
    vi.mocked(api.queuedMessages).mockResolvedValue([mkMsg('a', 'one'), mkMsg('b', 'two')]);
    const { result } = renderHook(() => useMessageQueue('s1', 'opencode'));
    await waitFor(() => expect(result.current.queue).toHaveLength(2));

    act(() => { result.current.move('b', -1); });
    expect(api.moveQueuedMessage).toHaveBeenCalledWith('s1', 'b', -1, 'opencode');
  });

  it('a broadcast during the in-flight mount load wins (load does not clobber it)', async () => {
    // The mount load resolves SLOWLY and returns a pre-drain row.
    let resolveMount!: (v: QueuedMessage[]) => void;
    vi.mocked(api.queuedMessages).mockReturnValue(
      new Promise<QueuedMessage[]>((res) => { resolveMount = res; }),
    );
    const { result } = renderHook(() => useMessageQueue('s1', 'opencode'));
    // Let the mount microtask run so the load is issued and in flight.
    await act(async () => { await Promise.resolve(); });

    // A live broadcast lands while the load is in flight — empty list.
    act(() => { listener?.('s1', []); });
    expect(result.current.queue).toHaveLength(0);

    // The slow load resolves with the stale row — it must be DROPPED
    // (a newer update already applied), so the item does not appear.
    await act(async () => { resolveMount([mkMsg('a', 'one')]); await Promise.resolve(); });
    expect(result.current.queue).toHaveLength(0);
  });

  it('keeps the queue an array when the endpoint returns a non-array', async () => {
    // A misrouted /queue (e.g. proxy returns the session object or HTML)
    // must not crash the render — the list stays an empty array.
    vi.mocked(api.queuedMessages).mockResolvedValue(
      { session: { id: 's1' } } as unknown as QueuedMessage[],
    );
    const { result } = renderHook(() => useMessageQueue('s1', 'opencode'));
    await waitFor(() => expect(Array.isArray(result.current.queue)).toBe(true));
    expect(result.current.queue).toHaveLength(0);
  });

  it('reloads from the endpoint on SSE reconnect (missed-event recovery)', async () => {
    vi.mocked(api.queuedMessages).mockResolvedValue([mkMsg('a', 'one')]);
    const { result } = renderHook(() => useMessageQueue('s1', 'opencode'));
    await waitFor(() => expect(result.current.queue).toHaveLength(1));

    // While disconnected, the drain's queue.updated was missed and the item
    // is stuck. On reconnect the hook reloads → server truth (empty).
    vi.mocked(api.queuedMessages).mockResolvedValue([]);
    act(() => { connectListener?.(); });
    await waitFor(() => expect(result.current.queue).toHaveLength(0));
  });

  it('a broadcast during a reconnect reload wins (reload does not clobber it)', async () => {
    vi.mocked(api.queuedMessages).mockResolvedValue([mkMsg('a', 'one')]);
    const { result } = renderHook(() => useMessageQueue('s1', 'opencode'));
    await waitFor(() => expect(result.current.queue).toHaveLength(1));

    // Reconnect triggers a reload that resolves SLOWLY with the stale row.
    let resolveReload!: (v: QueuedMessage[]) => void;
    vi.mocked(api.queuedMessages).mockReturnValueOnce(
      new Promise<QueuedMessage[]>((res) => { resolveReload = res; }),
    );
    act(() => { connectListener?.(); });

    // A fresher broadcast (empty) lands first.
    act(() => { listener?.('s1', []); });
    expect(result.current.queue).toHaveLength(0);

    // The slow reload resolves stale — it must be dropped.
    await act(async () => { resolveReload([mkMsg('a', 'one')]); await Promise.resolve(); });
    expect(result.current.queue).toHaveLength(0);
  });

  it('is inert without a session id', async () => {
    const { result } = renderHook(() => useMessageQueue(undefined));
    await waitFor(() => expect(result.current.queue).toHaveLength(0));
    expect(api.queuedMessages).not.toHaveBeenCalled();
  });
});
