import { afterEach, beforeEach, describe, it, expect, vi } from 'vitest';
import { SESSION_CACHE_MAX, resetWhisperStatusCache, useApiStore } from './apiStore';
import { api, type SessionDetail, type Session } from './api';
import { SIDEBAR_SESSIONS_KEY, getSidebarSessions, patchSessionLists, setSidebarSessions } from './sessionsCache';
import { queryClient } from './queryClient';

it('scopes lifecycle patches to the owning platform', () => {
  const local = makeSessionDetail('same').session;
  const remote = { ...local, platform: 'r-A:opencode' };
  queryClient.setQueryData(SIDEBAR_SESSIONS_KEY, [local, remote]);
  patchSessionLists('same', { status: 'limited', resetsAt: 12345 }, remote.platform);
  expect(getSidebarSessions().find(s => s.platform === local.platform)?.status).toBe('done');
  expect(getSidebarSessions().find(s => s.platform === remote.platform)).toMatchObject({ status: 'limited', resetsAt: 12345 });
});

function makeSessionDetail(id: string, overrides: Partial<SessionDetail> = {}): SessionDetail {
  const session: Session = {
    id,
    platform: 'opencode',
    projectId: 'proj',
    title: `Session ${id}`,
    directory: '/tmp',
    timeCreated: 0,
    timeUpdated: 0,
    summaryAdditions: null,
    summaryDeletions: null,
    summaryFiles: null,
    shareUrl: null,
    messageCount: 0,
    durationMs: 0,
    activeDurationMs: 0,
    totalInputTokens: 0,
    totalOutputTokens: 0,
    totalCost: 0,
    status: 'done',
    liveConnection: false,
    pendingPermission: false,
    pendingQuestion: false,
    archived: false,
    seen: true,
    pinned: false,
    pinnedAt: 0,
    seenTimeUpdated: 0,
    unreadCount: 0,
  };
  return {
    session,
    messages: [],
    parts: [],
    totalMessages: 0,
    ...overrides,
  };
}

function resetCache() {
  useApiStore.setState({ sessionCache: new Map(), sessionCacheOrder: [] });
}

describe('interruption read acknowledgement', () => {
  afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); useApiStore.setState({ pendingInterruptionReads: {} }); });

  it('publishes a confirmed cold read before dropping its owner-qualified marker', async () => {
    vi.spyOn(api, 'markSessionSeen').mockResolvedValue({ ok: true });
    queryClient.setQueryData(SIDEBAR_SESSIONS_KEY, []);
    useApiStore.setState({ pendingInterruptionReads: {} });
    await useApiStore.getState().markSessionSeen('r-box:opencode', 's', 100, true);
    const local = { ...makeSessionDetail('s').session, status: 'interrupted' as const, timeUpdated: 100, seen: false };
    const remote = { ...local, platform: 'r-box:opencode' };
    setSidebarSessions([local]);
    expect(getSidebarSessions()[0].seen).toBe(false);
    expect(useApiStore.getState().pendingInterruptionReads['r-box:opencode:s']).toMatchObject({ confirmed: true });
    setSidebarSessions([local, remote]);
    expect(getSidebarSessions()[1]).toMatchObject({ seen: true, seenTimeUpdated: 100 });
    expect(useApiStore.getState().pendingInterruptionReads).toEqual({});
  });

  it.each([
    ['interrupted', 100, true],
    ['interrupted', 200, false],
    ['busy', 100, false],
  ] as const)('reconciles a successful cold read only for the same interruption: %s/%i', async (status, updated, seen) => {
    let finish!: (value: { ok: boolean }) => void;
    vi.spyOn(api, 'markSessionSeen').mockImplementation(() => new Promise(resolve => { finish = resolve; }));
    queryClient.setQueryData(SIDEBAR_SESSIONS_KEY, []);
    useApiStore.setState({ pendingInterruptionReads: {} });
    const reading = useApiStore.getState().markSessionSeen('r-box:opencode', 's', 100, true);
    const row = { ...makeSessionDetail('s').session, platform: 'r-box:opencode', status, timeUpdated: updated, seenTimeUpdated: 0, seen: false };
    const local = { ...row, platform: 'opencode' };
    queryClient.setQueryData(SIDEBAR_SESSIONS_KEY, [local, row]);
    finish({ ok: true });
    await reading;
    expect(getSidebarSessions().find(s => s.platform === row.platform)?.seen).toBe(seen);
    expect(getSidebarSessions().find(s => s.platform === local.platform)?.seen).toBe(false);
  });

  it('keeps newer and other-owner reads pending when an older request completes', async () => {
    const row = { ...makeSessionDetail('s').session, status: 'interrupted' as const, timeUpdated: 100 };
    queryClient.setQueryData(SIDEBAR_SESSIONS_KEY, [row, { ...row, platform: 'r-box:opencode' }]);
    useApiStore.setState({ pendingInterruptionReads: {} });
    let finishOld!: (value: { ok: boolean }) => void;
    let finishNew!: (value: { ok: boolean }) => void;
    let failOther!: (error: Error) => void;
    vi.spyOn(api, 'markSessionSeen')
      .mockImplementationOnce(() => new Promise(resolve => { finishOld = resolve; }))
      .mockImplementationOnce(() => new Promise(resolve => { finishNew = resolve; }))
      .mockImplementationOnce(() => new Promise((_resolve, reject) => { failOther = reject; }));
    const old = useApiStore.getState().markSessionSeen('opencode', 's', 100, true);
    const newer = useApiStore.getState().markSessionSeen('opencode', 's', 100, true);
    const other = useApiStore.getState().markSessionSeen('r-box:opencode', 's', 100, true);
    const rejected = expect(other).rejects.toThrow('offline');
    finishOld({ ok: true });
    await old;
    expect(Object.keys(useApiStore.getState().pendingInterruptionReads)).toEqual(['opencode:s', 'r-box:opencode:s']);
    finishNew({ ok: true });
    await newer;
    expect(Object.keys(useApiStore.getState().pendingInterruptionReads)).toEqual(['r-box:opencode:s']);
    failOther(new Error('offline'));
    await rejected;
    expect(useApiStore.getState().pendingInterruptionReads).toEqual({});
  });

  it.each([true, false])('sends only the interruption state actually viewed: %s', async (interrupted) => {
    const fetch = vi.fn().mockResolvedValue(new Response('{"ok":true}', { status: 200 }));
    vi.stubGlobal('fetch', fetch);
    await useApiStore.getState().markSessionSeen('r-owner:opencode', 's', 100, interrupted);
    expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({
      platform: 'r-owner:opencode', sessionId: 's', timeUpdated: 100,
      ...(interrupted ? { interrupted: true } : {}),
    });
  });
});

describe('session cache', () => {
  beforeEach(resetCache);

  it('returns null for a missing session', () => {
    expect(useApiStore.getState().getCachedSession('nope')).toBeNull();
  });

  it('returns cached data after setCachedSession', () => {
    const detail = makeSessionDetail('a');
    useApiStore.getState().setCachedSession('a', detail);
    expect(useApiStore.getState().getCachedSession('a')).toEqual(detail);
  });

  it('evicts the oldest entry when exceeding SESSION_CACHE_MAX', () => {
    const store = useApiStore.getState();
    for (let i = 0; i < SESSION_CACHE_MAX + 1; i++) {
      store.setCachedSession(`s${i}`, makeSessionDetail(`s${i}`));
    }
    // s0 (oldest) should be evicted; s1..sN remain
    expect(useApiStore.getState().getCachedSession('s0')).toBeNull();
    for (let i = 1; i <= SESSION_CACHE_MAX; i++) {
      expect(useApiStore.getState().getCachedSession(`s${i}`)).not.toBeNull();
    }
  });

  it('keeps the ten most recently viewed sessions', () => {
    const store = useApiStore.getState();
    for (let i = 0; i < 11; i++) store.setCachedSession(`s${i}`, makeSessionDetail(`s${i}`));
    expect(useApiStore.getState().getCachedSession('s0')).toBeNull();
    expect(useApiStore.getState().getCachedSession('s1')).not.toBeNull();
    expect(useApiStore.getState().sessionCacheOrder).toHaveLength(10);
  });

  it('promotes an entry to most-recent on setCachedSession', () => {
    const store = useApiStore.getState();
    for (let i = 0; i < SESSION_CACHE_MAX; i++) {
      store.setCachedSession(`s${i}`, makeSessionDetail(`s${i}`));
    }
    // Re-set s0 (promoting it to most-recent)
    store.setCachedSession('s0', makeSessionDetail('s0'));
    // Now add one more; the oldest should now be s1 (not s0)
    store.setCachedSession('new', makeSessionDetail('new'));
    expect(useApiStore.getState().getCachedSession('s0')).not.toBeNull();
    expect(useApiStore.getState().getCachedSession('s1')).toBeNull();
  });

  it('overwrites existing cache entry without growing order list', () => {
    const store = useApiStore.getState();
    store.setCachedSession('a', makeSessionDetail('a', { totalMessages: 1 }));
    store.setCachedSession('a', makeSessionDetail('a', { totalMessages: 2 }));
    expect(useApiStore.getState().getCachedSession('a')?.totalMessages).toBe(2);
    expect(useApiStore.getState().sessionCacheOrder).toEqual(['a']);
  });

  it('updateCachedSession mutates an existing entry', () => {
    const store = useApiStore.getState();
    store.setCachedSession('a', makeSessionDetail('a', { totalMessages: 1 }));
    store.updateCachedSession('a', (prev) => ({ ...prev, totalMessages: 42 }));
    expect(useApiStore.getState().getCachedSession('a')?.totalMessages).toBe(42);
  });

  it('updateCachedSession is a no-op for a missing entry', () => {
    const store = useApiStore.getState();
    let called = false;
    store.updateCachedSession('missing', (prev) => {
      called = true;
      return prev;
    });
    expect(called).toBe(false);
    expect(useApiStore.getState().getCachedSession('missing')).toBeNull();
  });

  it('clearCachedSession removes an entry and its order slot', () => {
    const store = useApiStore.getState();
    store.setCachedSession('a', makeSessionDetail('a'));
    store.setCachedSession('b', makeSessionDetail('b'));
    store.clearCachedSession('a');
    expect(useApiStore.getState().getCachedSession('a')).toBeNull();
    expect(useApiStore.getState().getCachedSession('b')).not.toBeNull();
    expect(useApiStore.getState().sessionCacheOrder).toEqual(['b']);
  });

  it('clearCachedSession is a no-op for a missing entry', () => {
    const store = useApiStore.getState();
    store.setCachedSession('a', makeSessionDetail('a'));
    store.clearCachedSession('nope');
    expect(useApiStore.getState().getCachedSession('a')).not.toBeNull();
  });
});

describe('seedNewSession', () => {
  beforeEach(() => {
    queryClient.setQueryData(SIDEBAR_SESSIONS_KEY, []);
    useApiStore.setState({ sessionCache: new Map(), sessionCacheOrder: [] });
  });

  it('prepends the stub to recentSessions so the sidebar shows it instantly', () => {
    const existing = makeSessionDetail('existing').session;
    queryClient.setQueryData(SIDEBAR_SESSIONS_KEY, [existing]);

    useApiStore.getState().seedNewSession('new-1', '/repo', 'opencode', 'pr #7', 'box');

    const recent = getSidebarSessions();
    expect(recent[0].id).toBe('new-1');
    expect(recent[0].title).toBe('pr #7');
    expect(recent[0].remoteId).toBe('box');
    expect(recent[0].liveConnection).toBe(true);
    expect(useApiStore.getState().sessionCache.get('new-1')?.session.liveConnection).toBe(true);
    expect(recent.map((s) => s.id)).toEqual(['new-1', 'existing']);
  });

  it('does not duplicate a session already in recentSessions', () => {
    useApiStore.getState().seedNewSession('dup', '/repo', 'opencode');
    useApiStore.getState().seedNewSession('dup', '/repo', 'opencode');
    const recent = getSidebarSessions();
    expect(recent.filter((s) => s.id === 'dup')).toHaveLength(1);
  });
});

describe('getWhisperStatus', () => {
  afterEach(() => vi.restoreAllMocks());

  it('fetches the machine whisper status once per app load', async () => {
    resetWhisperStatusCache();
    const request = vi.spyOn(api, 'whisperStatus').mockResolvedValue({ available: true });
    await expect(useApiStore.getState().getWhisperStatus()).resolves.toEqual({ available: true });
    await expect(useApiStore.getState().getWhisperStatus()).resolves.toEqual({ available: true });
    expect(request).toHaveBeenCalledTimes(1);
  });

  it('retries after a failed status read', async () => {
    resetWhisperStatusCache();
    const request = vi.spyOn(api, 'whisperStatus')
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValue({ available: false });
    await expect(useApiStore.getState().getWhisperStatus()).rejects.toThrow('offline');
    await expect(useApiStore.getState().getWhisperStatus()).resolves.toEqual({ available: false });
    expect(request).toHaveBeenCalledTimes(2);
  });
});
