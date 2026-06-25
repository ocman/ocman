import { beforeEach, describe, it, expect } from 'vitest';
import { SESSION_CACHE_MAX, useApiStore } from './apiStore';
import type { SessionDetail, Session } from './api';

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

describe('runRequest AbortError handling', () => {
  beforeEach(() => {
    useApiStore.setState({ requests: {} });
  });

  it('does not write an error state for AbortError', async () => {
    const store = useApiStore.getState();
    const abortError = new DOMException('The operation was aborted.', 'AbortError');

    await expect(
      store.runRequest('test:abort', () => Promise.reject(abortError)),
    ).rejects.toThrow(abortError);

    // The request should still be in loading state (not error) because
    // AbortError is not a real failure.
    const req = useApiStore.getState().requests['test:abort'];
    expect(req).toBeDefined();
    expect(req.error).toBeNull();
  });

  it('writes an error state for non-abort errors', async () => {
    const store = useApiStore.getState();
    const error = new Error('Network failure');

    await expect(
      store.runRequest('test:fail', () => Promise.reject(error)),
    ).rejects.toThrow(error);

    const req = useApiStore.getState().requests['test:fail'];
    expect(req).toBeDefined();
    expect(req.error).toBe('Network failure');
    expect(req.loading).toBe(false);
  });

  it('writes success state for resolved tasks', async () => {
    const store = useApiStore.getState();
    const result = await store.runRequest('test:ok', () => Promise.resolve(42));
    expect(result).toBe(42);

    const req = useApiStore.getState().requests['test:ok'];
    expect(req).toBeDefined();
    expect(req.loading).toBe(false);
    expect(req.error).toBeNull();
  });
});

describe('seedNewSession', () => {
  beforeEach(() => {
    useApiStore.setState({
      sessionCache: new Map(),
      sessionCacheOrder: [],
      cachedSessions: null,
      recentSessions: [],
      recentSessionsHash: '',
    });
  });

  it('prepends the stub to recentSessions so the sidebar shows it instantly', () => {
    const existing = makeSessionDetail('existing').session;
    useApiStore.setState({ recentSessions: [existing] });

    useApiStore.getState().seedNewSession('new-1', '/repo', 'opencode', 'pr #7');

    const recent = useApiStore.getState().recentSessions;
    expect(recent[0].id).toBe('new-1');
    expect(recent[0].title).toBe('pr #7');
    expect(recent.map((s) => s.id)).toEqual(['new-1', 'existing']);
    // Hash is recomputed so the next poll's dedup check stays accurate.
    expect(useApiStore.getState().recentSessionsHash).not.toBe('');
  });

  it('does not duplicate a session already in recentSessions', () => {
    useApiStore.getState().seedNewSession('dup', '/repo', 'opencode');
    useApiStore.getState().seedNewSession('dup', '/repo', 'opencode');
    const recent = useApiStore.getState().recentSessions;
    expect(recent.filter((s) => s.id === 'dup')).toHaveLength(1);
  });
});
