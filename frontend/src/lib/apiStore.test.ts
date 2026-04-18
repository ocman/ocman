import { beforeEach, describe, it, expect } from 'vitest';
import { SESSION_CACHE_MAX, useApiStore } from './apiStore';
import type { SessionDetail, Session } from './api';

function makeSessionDetail(id: string, overrides: Partial<SessionDetail> = {}): SessionDetail {
  const session: Session = {
    id,
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
    totalInputTokens: 0,
    totalOutputTokens: 0,
    totalCost: 0,
    status: 'done',
    hasPort: false,
    pendingPermission: false,
    pendingQuestion: false,
    archived: false,
    seen: true,
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
