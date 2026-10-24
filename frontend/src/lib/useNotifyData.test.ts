// @vitest-environment jsdom
import { act, renderHook } from '@testing-library/react';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  __resetForTests, NOTIFY_RECHECK_DELAY_MS, NOTIFY_TIMEOUT_MS, notifyQueryKey, recheckNotifyData,
  useNotifyData, useNotifySnapshot, type NotifySnapshot,
} from './useNotifyData';
import { api, type Session } from './api';
import { queryClient } from './queryClient';
import { patchSessionLists, seedSessionLists } from './sessionsCache';
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

let hidden = false;
function setHidden(value: boolean) {
  hidden = value;
  document.dispatchEvent(new Event('visibilitychange', { bubbles: true }));
}

const snapshot = () => queryClient.getQueryData<NotifySnapshot>(notifyQueryKey);
const data = () => snapshot()?.entries ?? null;
const recheck = recheckNotifyData;
const mounts: (() => void)[] = [];
function subscribe() {
  mounts.push(renderHook(() => useNotifySnapshot()).unmount);
}
function unsubscribe() {
  act(() => mounts.pop()?.());
}

beforeAll(() => queryClient.mount());
afterAll(() => queryClient.unmount());

describe('useNotifySnapshot', () => {
  beforeEach(() => {
    __resetForTests();
    hidden = false;
    vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => hidden ? 'hidden' : 'visible');
    vi.spyOn(document, 'hidden', 'get').mockImplementation(() => hidden);
    vi.useFakeTimers();
  });

  afterEach(() => {
    while (mounts.length) unsubscribe();
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it('starts with null data', () => {
    const { result, unmount } = renderHook(() => useNotifyData());
    expect(result.current).toBeNull();
    unmount();
  });

  it('shares one request between consumers', async () => {
    const notify = vi.mocked(api.sessionsNotify);
    notify.mockClear();
    subscribe();
    subscribe();
    await vi.advanceTimersByTimeAsync(0);
    expect(notify).toHaveBeenCalledTimes(1);
  });

  it('polls only while visible and refetches when shown again', async () => {
    const notify = vi.mocked(api.sessionsNotify);
    subscribe();
    await vi.advanceTimersByTimeAsync(0);
    notify.mockClear();
    await vi.advanceTimersByTimeAsync(10_000);
    expect(notify).toHaveBeenCalledTimes(1);
    act(() => setHidden(true));
    await vi.advanceTimersByTimeAsync(30_000);
    expect(notify).toHaveBeenCalledTimes(1);
    act(() => setHidden(false));
    await vi.advanceTimersByTimeAsync(0);
    expect(notify).toHaveBeenCalledTimes(2);
  });

  it('stays out of the session list caches', async () => {
    subscribe();
    await vi.advanceTimersByTimeAsync(0);
    const before = snapshot();
    seedSessionLists({ id: 'new', platform: 'opencode', directory: '/repo' } as Session);
    patchSessionLists('s1', { seen: true });
    expect(snapshot()).toBe(before);
  });

  it('stops polling once the last consumer leaves', async () => {
    const notify = vi.mocked(api.sessionsNotify);
    subscribe();
    await vi.advanceTimersByTimeAsync(0);
    unsubscribe();
    notify.mockClear();
    await vi.advanceTimersByTimeAsync(30_000);
    expect(notify).not.toHaveBeenCalled();
  });

  it('fetches data after first subscribe', async () => {
    subscribe();
    // Let the async fetch resolve
    await vi.advanceTimersByTimeAsync(0);
    expect(data()).toHaveLength(2);
    expect(snapshot()?.fetchedAt).toBeGreaterThan(0);
    // Cleanup
    unsubscribe();
  });

  it('recheck triggers a fetch when consumers are active', async () => {
    subscribe();
    await vi.advanceTimersByTimeAsync(0);
    const firstFetch = snapshot()!.fetchedAt;

    // Advance a bit so lastFetched changes
    vi.advanceTimersByTime(100);
    recheck();
    await vi.advanceTimersByTimeAsync(0);
    expect(snapshot()!.fetchedAt).toBeGreaterThanOrEqual(firstFetch);

    // Cleanup
    unsubscribe();
  });

  it('recheck is a no-op when no consumers are active', async () => {
    recheck();
    await vi.advanceTimersByTimeAsync(0);
    expect(data()).toBeNull();
  });

  it('folds a burst of rechecks into one request and never aborts one in flight', async () => {
    const notify = vi.mocked(api.sessionsNotify);
    subscribe();
    await vi.advanceTimersByTimeAsync(0);
    notify.mockClear();
    let release!: () => void;
    const signals: (AbortSignal | undefined)[] = [];
    notify.mockImplementation((_params, signal) => {
      signals.push(signal);
      return new Promise((resolve) => { release = () => resolve([]); });
    });

    for (let i = 0; i < 5; i++) recheck();
    await vi.advanceTimersByTimeAsync(NOTIFY_RECHECK_DELAY_MS);
    expect(notify).toHaveBeenCalledTimes(1);

    // Events during the fetch queue one follow-up instead of cancelling it.
    for (let i = 0; i < 5; i++) recheck();
    await vi.advanceTimersByTimeAsync(NOTIFY_RECHECK_DELAY_MS * 2);
    expect(notify).toHaveBeenCalledTimes(1);
    expect(signals.some((signal) => signal?.aborted)).toBe(false);
    release();
    await vi.advanceTimersByTimeAsync(NOTIFY_RECHECK_DELAY_MS);
    expect(notify).toHaveBeenCalledTimes(2);
    release();
    await vi.advanceTimersByTimeAsync(NOTIFY_RECHECK_DELAY_MS);
    expect(notify).toHaveBeenCalledTimes(2);

    unsubscribe();
    notify.mockReset();
    notify.mockResolvedValue([]);
  });

  it('recovers when a request stalls', async () => {
    const notify = vi.mocked(api.sessionsNotify);
    notify.mockImplementationOnce((_params, signal) => new Promise((_resolve, reject) => {
      signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
    }));
    subscribe();
    await vi.advanceTimersByTimeAsync(0);
    recheck();
    await vi.advanceTimersByTimeAsync(NOTIFY_TIMEOUT_MS + NOTIFY_RECHECK_DELAY_MS);
    expect(notify).toHaveBeenCalledTimes(2);
    expect(data()).not.toBeNull();
    unsubscribe();
  });

  it('drops a queued follow-up once the last consumer leaves', async () => {
    const notify = vi.mocked(api.sessionsNotify);
    notify.mockClear();
    let release!: () => void;
    notify.mockImplementationOnce(() => new Promise((resolve) => { release = () => resolve([]); }));
    subscribe();
    await vi.advanceTimersByTimeAsync(0);
    recheck();
    unsubscribe();
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
    subscribe();
    await vi.advanceTimersByTimeAsync(0);
    recheck({ ...prompt, kind: 'permission' });
    releaseStale();
    await vi.advanceTimersByTimeAsync(0);
    expect(data()).toEqual([
      { id: 's1', status: 'waiting', seen: false, pendingPermission: false, permissions: [] },
      stale[1],
    ]);
    unsubscribe();
  });

  it('keeps publishing while events arrive during every request', async () => {
    const notify = vi.mocked(api.sessionsNotify);
    notify.mockClear();
    let n = 0;
    notify.mockImplementation(() => new Promise((resolve) => {
      const id = `s${++n}`;
      setTimeout(() => resolve([{ id, status: 'waiting', seen: false, pendingPermission: true }] as never), 100);
    }));
    subscribe();
    for (let i = 0; i < 20; i++) {
      recheck({ platform: 'opencode', sessionId: 'unrelated', requestId: `resolved-${i}`, kind: 'permission' });
      await vi.advanceTimersByTimeAsync(50);
    }
    expect(notify.mock.calls.length).toBeGreaterThan(2);
    expect(data()?.[0].pendingPermission).toBe(true);
    unsubscribe();
    notify.mockReset();
    notify.mockResolvedValue([]);
  });

  it('still runs event rechecks while the tab is hidden', async () => {
    const notify = vi.mocked(api.sessionsNotify);
    subscribe();
    await vi.advanceTimersByTimeAsync(0);
    notify.mockClear();
    hidden = true;
    recheck();
    await vi.advanceTimersByTimeAsync(NOTIFY_RECHECK_DELAY_MS);
    expect(notify).toHaveBeenCalledTimes(1);
    unsubscribe();
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
    subscribe();
    recheck({ ...child, kind: 'permission' });
    release();
    await vi.advanceTimersByTimeAsync(0);
    const [remote, local] = data()!;
    expect(remote.permissions).toEqual([other]);
    expect(remote.pendingPermission).toBe(true);
    expect(remote.questions).toEqual([question]);
    expect(remote.pendingQuestion).toBe(true);
    expect(local.pendingPermission).toBe(true);
    unsubscribe();
  });

  it('clears the ancestor prompt flag when its last child request resolves', async () => {
    const child = { platform: 'opencode', sessionId: 'child', requestId: 'q1' };
    let release!: () => void;
    vi.mocked(api.sessionsNotify).mockImplementationOnce(() => new Promise((resolve) => { release = () => resolve([
      { id: 'parent', status: 'waiting', seen: false, pendingQuestion: true, questions: [child] },
    ]); }));
    subscribe();
    recheck({ ...child, kind: 'question' });
    release();
    await vi.advanceTimersByTimeAsync(0);
    expect(data()![0].pendingQuestion).toBe(false);
    unsubscribe();
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
    subscribe();
    recheck({ ...child, kind: 'permission' });
    release();
    await vi.advanceTimersByTimeAsync(0);
    // Bell/favicon/completion consumers receive only genuinely eligible rows.
    expect(data()?.map((entry) => entry.id)).toEqual(['error']);
    unsubscribe();
  });

  it('treats an unidentified resolution as refresh-only across owners and prompt kinds', async () => {
    const rows = ['opencode', 'r-box:opencode'].map((platform) => ({
      id: 'same', status: 'busy', seen: true, pendingPermission: true, pendingQuestion: true,
      permissions: [{ platform, sessionId: 'same', requestId: 'p1' }],
      questions: [{ platform, sessionId: 'same', requestId: 'q1' }],
    }));
    let release!: () => void;
    vi.mocked(api.sessionsNotify).mockImplementationOnce(() => new Promise((resolve) => { release = () => resolve(rows); }));
    subscribe();
    __handleResolvedForTests(JSON.stringify({ sessionID: 'same', platform: 'opencode' }));
    release();
    await vi.advanceTimersByTimeAsync(0);
    expect(data()).toEqual(rows);
    unsubscribe();
  });
});
