import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useNotifyStore, __resetForTests, NOTIFY_RECHECK_DELAY_MS, NOTIFY_TIMEOUT_MS } from './useNotifyData';
import { api } from './api';
import { __handleResolvedForTests } from './useGlobalEvents';

// Mock the api module so we don't make real HTTP requests.
vi.mock('./api', () => ({
  api: {
    sessionsNotify: vi.fn().mockResolvedValue([
      { id: 's1', status: 'waiting', seen: false },
      { id: 's2', status: 'error', seen: false, pendingPermission: true },
    ]),
  },
}));

// Provide a minimal document stub for the visibility API used by the store.
const originalDocument = globalThis.document;
beforeEach(() => {
  // @ts-expect-error -- minimal stub for tests
  globalThis.document = {
    hidden: false,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  };
});
afterEach(() => {
  globalThis.document = originalDocument;
});

describe('useNotifyStore', () => {
  beforeEach(() => {
    __resetForTests();
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('starts with refCount 0 and null data', () => {
    const state = useNotifyStore.getState();
    expect(state.refCount).toBe(0);
    expect(state.data).toBeNull();
  });

  it('increments refCount on subscribe', () => {
    useNotifyStore.getState().subscribe();
    expect(useNotifyStore.getState().refCount).toBe(1);
    useNotifyStore.getState().subscribe();
    expect(useNotifyStore.getState().refCount).toBe(2);
    // Cleanup
    useNotifyStore.getState().unsubscribe();
    useNotifyStore.getState().unsubscribe();
  });

  it('decrements refCount on unsubscribe, never below 0', () => {
    useNotifyStore.getState().subscribe();
    useNotifyStore.getState().subscribe();
    useNotifyStore.getState().unsubscribe();
    expect(useNotifyStore.getState().refCount).toBe(1);
    useNotifyStore.getState().unsubscribe();
    expect(useNotifyStore.getState().refCount).toBe(0);
    // Extra unsubscribe should not go negative
    useNotifyStore.getState().unsubscribe();
    expect(useNotifyStore.getState().refCount).toBe(0);
  });

  it('fetches data after first subscribe', async () => {
    useNotifyStore.getState().subscribe();
    // Let the async fetch resolve
    await vi.advanceTimersByTimeAsync(0);
    const state = useNotifyStore.getState();
    expect(state.data).not.toBeNull();
    expect(state.data).toHaveLength(2);
    expect(state.lastFetched).toBeGreaterThan(0);
    // Cleanup
    useNotifyStore.getState().unsubscribe();
  });

  it('recheck triggers a fetch when consumers are active', async () => {
    useNotifyStore.getState().subscribe();
    await vi.advanceTimersByTimeAsync(0);
    const firstFetch = useNotifyStore.getState().lastFetched;

    // Advance a bit so lastFetched changes
    vi.advanceTimersByTime(100);
    useNotifyStore.getState().recheck();
    await vi.advanceTimersByTimeAsync(0);
    expect(useNotifyStore.getState().lastFetched).toBeGreaterThanOrEqual(firstFetch);

    // Cleanup
    useNotifyStore.getState().unsubscribe();
  });

  it('recheck is a no-op when no consumers are active', async () => {
    useNotifyStore.getState().recheck();
    await vi.advanceTimersByTimeAsync(0);
    expect(useNotifyStore.getState().data).toBeNull();
  });

  it('folds a burst of rechecks into one request and never aborts one in flight', async () => {
    const notify = vi.mocked(api.sessionsNotify);
    useNotifyStore.getState().subscribe();
    await vi.advanceTimersByTimeAsync(0);
    notify.mockClear();
    let release!: () => void;
    const signals: (AbortSignal | undefined)[] = [];
    notify.mockImplementation((_params, signal) => {
      signals.push(signal);
      return new Promise((resolve) => { release = () => resolve([]); });
    });

    for (let i = 0; i < 5; i++) useNotifyStore.getState().recheck();
    await vi.advanceTimersByTimeAsync(NOTIFY_RECHECK_DELAY_MS);
    expect(notify).toHaveBeenCalledTimes(1);

    // Events during the fetch queue one follow-up instead of cancelling it.
    for (let i = 0; i < 5; i++) useNotifyStore.getState().recheck();
    await vi.advanceTimersByTimeAsync(NOTIFY_RECHECK_DELAY_MS * 2);
    expect(notify).toHaveBeenCalledTimes(1);
    expect(signals.some((signal) => signal?.aborted)).toBe(false);
    release();
    await vi.advanceTimersByTimeAsync(NOTIFY_RECHECK_DELAY_MS);
    expect(notify).toHaveBeenCalledTimes(2);
    release();
    await vi.advanceTimersByTimeAsync(NOTIFY_RECHECK_DELAY_MS);
    expect(notify).toHaveBeenCalledTimes(2);

    useNotifyStore.getState().unsubscribe();
    notify.mockReset();
    notify.mockResolvedValue([]);
  });

  it('recovers when a request stalls', async () => {
    const notify = vi.mocked(api.sessionsNotify);
    notify.mockImplementationOnce((_params, signal) => new Promise((_resolve, reject) => {
      signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
    }));
    useNotifyStore.getState().subscribe();
    await vi.advanceTimersByTimeAsync(0);
    useNotifyStore.getState().recheck();
    await vi.advanceTimersByTimeAsync(NOTIFY_TIMEOUT_MS + NOTIFY_RECHECK_DELAY_MS);
    expect(notify).toHaveBeenCalledTimes(2);
    expect(useNotifyStore.getState().data).not.toBeNull();
    useNotifyStore.getState().unsubscribe();
  });

  it('drops a queued follow-up once the last consumer leaves', async () => {
    const notify = vi.mocked(api.sessionsNotify);
    notify.mockClear();
    let release!: () => void;
    notify.mockImplementationOnce(() => new Promise((resolve) => { release = () => resolve([]); }));
    useNotifyStore.getState().subscribe();
    await vi.advanceTimersByTimeAsync(0);
    useNotifyStore.getState().recheck();
    useNotifyStore.getState().unsubscribe();
    release();
    await vi.advanceTimersByTimeAsync(NOTIFY_RECHECK_DELAY_MS * 2);
    expect(notify).toHaveBeenCalledTimes(1);
  });

  it('masks a prompt resolved mid-request without withholding the snapshot', async () => {
    const notify = vi.mocked(api.sessionsNotify);
    const prompt = { platform: 'opencode', sessionId: 's1', requestId: 'p1' };
    const stale = [
      { id: 's1', status: 'waiting', seen: false, pendingPermission: true, permissions: [prompt] },
      { id: 's2', status: 'waiting', seen: false, pendingQuestion: true },
    ];
    let releaseStale!: () => void;
    notify.mockImplementationOnce(() => new Promise((resolve) => { releaseStale = () => resolve(stale as never); }));
    notify.mockResolvedValueOnce([]);
    useNotifyStore.getState().subscribe();
    await vi.advanceTimersByTimeAsync(0);
    useNotifyStore.getState().recheck({ ...prompt, kind: 'permission' });
    releaseStale();
    await vi.advanceTimersByTimeAsync(0);
    expect(useNotifyStore.getState().data).toEqual([
      { id: 's1', status: 'waiting', seen: false, pendingPermission: false, permissions: [] },
      stale[1],
    ]);
    useNotifyStore.getState().unsubscribe();
  });

  it('keeps publishing while events arrive during every request', async () => {
    const notify = vi.mocked(api.sessionsNotify);
    notify.mockClear();
    let n = 0;
    notify.mockImplementation(() => new Promise((resolve) => {
      const id = `s${++n}`;
      setTimeout(() => resolve([{ id, status: 'waiting', seen: false, pendingPermission: true }] as never), 100);
    }));
    useNotifyStore.getState().subscribe();
    for (let i = 0; i < 20; i++) {
      useNotifyStore.getState().recheck({ platform: 'opencode', sessionId: 'unrelated', requestId: `resolved-${i}`, kind: 'permission' });
      await vi.advanceTimersByTimeAsync(50);
    }
    expect(notify.mock.calls.length).toBeGreaterThan(2);
    expect(useNotifyStore.getState().data?.[0].pendingPermission).toBe(true);
    useNotifyStore.getState().unsubscribe();
    notify.mockReset();
    notify.mockResolvedValue([]);
  });

  it('still runs event rechecks while the tab is hidden', async () => {
    const notify = vi.mocked(api.sessionsNotify);
    useNotifyStore.getState().subscribe();
    await vi.advanceTimersByTimeAsync(0);
    notify.mockClear();
    (document as { hidden: boolean }).hidden = true;
    useNotifyStore.getState().recheck();
    await vi.advanceTimersByTimeAsync(NOTIFY_RECHECK_DELAY_MS);
    expect(notify).toHaveBeenCalledTimes(1);
    useNotifyStore.getState().unsubscribe();
  });

  it('reconciles a child resolution by owner and request without hiding other prompts', async () => {
    const notify = vi.mocked(api.sessionsNotify);
    const child = { platform: 'r-laptop:opencode', sessionId: 'child', requestId: 'p1' };
    const other = { ...child, requestId: 'p2' };
    const question = { ...child, requestId: 'q1' };
    let release!: () => void;
    notify.mockImplementationOnce(() => new Promise((resolve) => { release = () => resolve([
      { id: 'parent', status: 'waiting', seen: false, pendingPermission: true, pendingQuestion: true,
        permissions: [child, other], questions: [question] },
      { id: 'parent', status: 'waiting', seen: false, pendingPermission: true,
        permissions: [{ ...child, platform: 'opencode' }] },
    ]); }));
    useNotifyStore.getState().subscribe();
    useNotifyStore.getState().recheck({ ...child, kind: 'permission' });
    release();
    await vi.advanceTimersByTimeAsync(0);
    const [remote, local] = useNotifyStore.getState().data!;
    expect(remote.permissions).toEqual([other]);
    expect(remote.pendingPermission).toBe(true);
    expect(remote.questions).toEqual([question]);
    expect(remote.pendingQuestion).toBe(true);
    expect(local.pendingPermission).toBe(true);
    useNotifyStore.getState().unsubscribe();
  });

  it('clears the ancestor prompt flag when its last child request resolves', async () => {
    const child = { platform: 'opencode', sessionId: 'child', requestId: 'q1' };
    let release!: () => void;
    vi.mocked(api.sessionsNotify).mockImplementationOnce(() => new Promise((resolve) => { release = () => resolve([
      { id: 'parent', status: 'waiting', seen: false, pendingQuestion: true, questions: [child] },
    ]); }));
    useNotifyStore.getState().subscribe();
    useNotifyStore.getState().recheck({ ...child, kind: 'question' });
    release();
    await vi.advanceTimersByTimeAsync(0);
    expect(useNotifyStore.getState().data![0].pendingQuestion).toBe(false);
    useNotifyStore.getState().unsubscribe();
  });

  it('drops a busy or seen row after its final prompt resolves', async () => {
    const child = { platform: 'opencode', sessionId: 'child', requestId: 'p1' };
    let release!: () => void;
    vi.mocked(api.sessionsNotify).mockImplementationOnce(() => new Promise((resolve) => { release = () => resolve([
      { id: 'busy', status: 'busy', seen: false, pendingPermission: true, permissions: [child] },
      { id: 'seen', status: 'waiting', seen: true, pendingPermission: true, permissions: [child] },
      { id: 'error', status: 'error', seen: false, pendingPermission: true, permissions: [child] },
      { id: 'deferred', status: 'waiting', seen: false, pendingPermission: true, permissions: [child], suppressTerminal: true },
    ]); }));
    useNotifyStore.getState().subscribe();
    useNotifyStore.getState().recheck({ ...child, kind: 'permission' });
    release();
    await vi.advanceTimersByTimeAsync(0);
    // Bell/favicon/completion consumers receive only genuinely eligible rows.
    expect(useNotifyStore.getState().data?.map((entry) => entry.id)).toEqual(['error']);
    useNotifyStore.getState().unsubscribe();
  });

  it('treats an unidentified resolution as refresh-only across owners and prompt kinds', async () => {
    const rows = ['opencode', 'r-box:opencode'].map((platform) => ({
      id: 'same', status: 'busy', seen: true, pendingPermission: true, pendingQuestion: true,
      permissions: [{ platform, sessionId: 'same', requestId: 'p1' }],
      questions: [{ platform, sessionId: 'same', requestId: 'q1' }],
    }));
    let release!: () => void;
    vi.mocked(api.sessionsNotify).mockImplementationOnce(() => new Promise((resolve) => { release = () => resolve(rows); }));
    useNotifyStore.getState().subscribe();
    __handleResolvedForTests(JSON.stringify({ sessionID: 'same', platform: 'opencode' }));
    release();
    await vi.advanceTimersByTimeAsync(0);
    expect(useNotifyStore.getState().data).toEqual(rows);
    useNotifyStore.getState().unsubscribe();
  });
});
