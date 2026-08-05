import { describe, it, expect } from 'vitest';
import type { Session } from './api';
import { computeSidebarHash, filterInactiveChildren, pickNextSessionAfterArchive, mergeSidebarSessions, resolveOpenSession, rollupGroupStatus } from './sidebarHelpers';
import { vi } from 'vitest';

function makeSession(overrides: Partial<Session> = {}): Session {
  return {
    id: 's',
    platform: 'opencode',
    projectId: 'p',
    title: 't',
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
    ...overrides,
  };
}

describe('filterInactiveChildren', () => {
  it('drops a child whose parent is not in the list', () => {
    const sessions = [
      makeSession({ id: 'top' }),
      makeSession({ id: 'orphan', parentId: 'missing-parent' }),
    ];
    expect(filterInactiveChildren(sessions).map((s) => s.id)).toEqual(['top']);
  });

  it('drops a completed child even when its parent is present', () => {
    const sessions = [
      makeSession({ id: 'parent' }),
      makeSession({ id: 'child', parentId: 'parent' }),
    ];
    expect(filterInactiveChildren(sessions).map((s) => s.id)).toEqual(['parent']);
  });

  it('keeps the currently-open session even if it is an orphan child', () => {
    const sessions = [
      makeSession({ id: 'top' }),
      makeSession({ id: 'open', parentId: 'missing-parent' }),
    ];
    expect(filterInactiveChildren(sessions, 'open').map((s) => s.id)).toEqual(['top', 'open']);
  });

  it('keeps plain top-level sessions', () => {
    const sessions = [makeSession({ id: 'a' }), makeSession({ id: 'b' })];
    expect(filterInactiveChildren(sessions).map((s) => s.id)).toEqual(['a', 'b']);
  });

  it('keeps an orphan child while it is active, drops it once done', () => {
    const active = [
      makeSession({ id: 'top' }),
      makeSession({ id: 'busy', parentId: 'missing', status: 'busy' }),
      makeSession({ id: 'prompt', parentId: 'missing', status: 'done', pendingPermission: true }),
      makeSession({ id: 'gone', parentId: 'missing', status: 'done' }),
    ];
    expect(filterInactiveChildren(active).map((s) => s.id)).toEqual(['top', 'busy', 'prompt']);
  });
});

describe('computeSidebarHash', () => {
  it('returns the empty string for an empty array', () => {
    expect(computeSidebarHash([])).toBe('');
  });

  it('encodes id, status, timeUpdated and pending flags', () => {
    const sessions = [
      makeSession({ id: 'a', status: 'busy', timeUpdated: 100 }),
      makeSession({ id: 'b', status: 'done', timeUpdated: 200, pendingPermission: true }),
    ];
    expect(computeSidebarHash(sessions)).toBe('a|busy|100|,b|done|200|p');
  });

  it('marks both pending flags when set', () => {
    const session = makeSession({
      id: 'a',
      status: 'waiting',
      timeUpdated: 50,
      pendingPermission: true,
      pendingQuestion: true,
    });
    expect(computeSidebarHash([session])).toBe('a|waiting|50|pq');
  });

  it('produces stable output across calls with identical input', () => {
    const sessions = [makeSession({ id: 'a', status: 'busy', timeUpdated: 1 })];
    expect(computeSidebarHash(sessions)).toBe(computeSidebarHash(sessions));
  });

  it('changes when timeUpdated changes (so the page invalidates correctly)', () => {
    const a = computeSidebarHash([makeSession({ id: 'a', timeUpdated: 1 })]);
    const b = computeSidebarHash([makeSession({ id: 'a', timeUpdated: 2 })]);
    expect(a).not.toBe(b);
  });

  it('changes when a notice appears or changes', () => {
    const without = computeSidebarHash([makeSession({ id: 'a', status: 'error', timeUpdated: 1 })]);
    const withNotice = computeSidebarHash([makeSession({
      id: 'a', status: 'error', timeUpdated: 1,
      notice: { kind: 'rate_limit', message: 'rate limited', retryAt: 999, attempt: 1 },
    })]);
    expect(without).not.toBe(withNotice);
  });

  it('is stable when notice is identical across calls', () => {
    const notice = { kind: 'rate_limit' as const, message: 'rate limited', retryAt: 999, attempt: 1 };
    const a = computeSidebarHash([makeSession({ id: 'a', status: 'error', timeUpdated: 1, notice })]);
    const b = computeSidebarHash([makeSession({ id: 'a', status: 'error', timeUpdated: 1, notice })]);
    expect(a).toBe(b);
  });
});

describe('pickNextSessionAfterArchive', () => {
  describe('recent (flat) view', () => {
    it('picks the row directly below the archived session', () => {
      const sessions = [
        makeSession({ id: 'a' }),
        makeSession({ id: 'b' }),
        makeSession({ id: 'c' }),
      ];
      expect(pickNextSessionAfterArchive(sessions, 'b', 'recent')?.id).toBe('c');
    });

    it('falls back to the row above when the archived session is last', () => {
      const sessions = [
        makeSession({ id: 'a' }),
        makeSession({ id: 'b' }),
        makeSession({ id: 'c' }),
      ];
      expect(pickNextSessionAfterArchive(sessions, 'c', 'recent')?.id).toBe('b');
    });

    it('returns undefined when the archived session is the only one', () => {
      const sessions = [makeSession({ id: 'a' })];
      expect(pickNextSessionAfterArchive(sessions, 'a', 'recent')).toBeUndefined();
    });

    it('returns undefined when the target is not present', () => {
      const sessions = [makeSession({ id: 'a' })];
      expect(pickNextSessionAfterArchive(sessions, 'missing', 'recent')).toBeUndefined();
    });
  });

  describe('projects (grouped) view', () => {
    it('picks the most recent remaining session in the same project', () => {
      const sessions = [
        makeSession({ id: 'cur', directory: '/src/foo', timeUpdated: 500 }),
        makeSession({ id: 'foo-old', directory: '/src/foo', timeUpdated: 100 }),
        makeSession({ id: 'foo-new', directory: '/src/foo', timeUpdated: 300 }),
        makeSession({ id: 'bar', directory: '/src/bar', timeUpdated: 999 }),
      ];
      // Even though 'bar' is the most recent overall, we stay in /src/foo
      // and pick its newest remaining sibling.
      expect(pickNextSessionAfterArchive(sessions, 'cur', 'projects')?.id).toBe('foo-new');
    });

    it('treats worktrees as part of the same project', () => {
      const sessions = [
        makeSession({ id: 'cur', directory: '/src/foo', timeUpdated: 500 }),
        makeSession({
          id: 'wt',
          directory: '/src/.worktrees/foo/feature-a',
          timeUpdated: 300,
        }),
        makeSession({ id: 'bar', directory: '/src/bar', timeUpdated: 999 }),
      ];
      expect(pickNextSessionAfterArchive(sessions, 'cur', 'projects')?.id).toBe('wt');
    });

    it('falls back to the newest remaining session when the project has no other sessions', () => {
      const sessions = [
        makeSession({ id: 'cur', directory: '/src/foo', timeUpdated: 500 }),
        makeSession({ id: 'bar-old', directory: '/src/bar', timeUpdated: 100 }),
        makeSession({ id: 'bar-new', directory: '/src/bar', timeUpdated: 300 }),
      ];
      expect(pickNextSessionAfterArchive(sessions, 'cur', 'projects')?.id).toBe('bar-new');
    });

    it('returns undefined when the target is not present', () => {
      const sessions = [makeSession({ id: 'a', directory: '/src/foo' })];
      expect(pickNextSessionAfterArchive(sessions, 'missing', 'projects')).toBeUndefined();
    });
  });
});

describe('rollupGroupStatus', () => {
  it('returns { kind: "none" } for an empty group', () => {
    expect(rollupGroupStatus([])).toEqual({ kind: 'none' });
  });

  it('elevates pending above every other state', () => {
    const sessions = [
      makeSession({ status: 'busy' }),
      makeSession({ status: 'error', seen: false }),
      makeSession({ pendingPermission: true }),
    ];
    expect(rollupGroupStatus(sessions)).toEqual({ kind: 'pending', count: 1 });
  });

  it('elevates error above busy/waiting when seen=false', () => {
    const sessions = [
      makeSession({ status: 'busy' }),
      makeSession({ status: 'error', seen: false }),
    ];
    expect(rollupGroupStatus(sessions)).toEqual({ kind: 'error', count: 1 });
  });

  it('ignores already-seen errors', () => {
    const sessions = [
      makeSession({ status: 'busy' }),
      makeSession({ status: 'error', seen: true }),
    ];
    expect(rollupGroupStatus(sessions)).toEqual({ kind: 'busy', count: 1 });
  });

  it('reports busy ahead of waiting', () => {
    const sessions = [
      makeSession({ status: 'busy' }),
      makeSession({ status: 'waiting', seen: false }),
    ];
    expect(rollupGroupStatus(sessions)).toEqual({ kind: 'busy', count: 1 });
  });

  it('reports waiting only when sessions are unseen', () => {
    const seen = [makeSession({ status: 'waiting', seen: true })];
    expect(rollupGroupStatus(seen)).toEqual({ kind: 'none' });
    const unseen = [makeSession({ status: 'waiting', seen: false })];
    expect(rollupGroupStatus(unseen)).toEqual({ kind: 'waiting', count: 1 });
  });

  it('counts every session contributing to the chosen kind', () => {
    const sessions = [
      makeSession({ pendingQuestion: true }),
      makeSession({ pendingPermission: true }),
      makeSession({ status: 'busy' }),
    ];
    expect(rollupGroupStatus(sessions)).toEqual({ kind: 'pending', count: 2 });
  });

  it('uses effectiveStatusOf to override the recorded status', () => {
    const target = makeSession({ id: 'x', status: 'done' });
    const other = makeSession({ id: 'y', status: 'done' });
    const overridden = rollupGroupStatus([target, other], (s) =>
      s.id === 'x' ? 'busy' : s.status,
    );
    expect(overridden).toEqual({ kind: 'busy', count: 1 });
  });

  it('does not override pending — pending check is independent of status', () => {
    // Even when effectiveStatusOf forces 'busy', the pending flag still wins.
    const session = makeSession({ status: 'done', pendingQuestion: true });
    expect(rollupGroupStatus([session], () => 'busy')).toEqual({ kind: 'pending', count: 1 });
  });
});

describe('resolveOpenSession', () => {
  it('returns the session from the fetched list without fetching', async () => {
    const open = makeSession({ id: 'open' });
    const fetchById = vi.fn();
    const res = await resolveOpenSession({
      id: 'open',
      fetched: [makeSession({ id: 'other' }), open],
      cached: null,
      fetchById,
    });
    expect(res.session).toBe(open);
    expect(res.cache).toBeNull();
    expect(fetchById).not.toHaveBeenCalled();
  });

  it('fetches by id when the open session is missing from the list (the bug)', async () => {
    // Regression: an open session older than the recent window / past the
    // backend limit is absent from `fetched`, so it must be fetched by id.
    const recovered = makeSession({ id: 'old' });
    const fetchById = vi.fn().mockResolvedValue(recovered);
    const res = await resolveOpenSession({
      id: 'old',
      fetched: [makeSession({ id: 'recent' })],
      cached: null,
      fetchById,
    });
    expect(fetchById).toHaveBeenCalledOnce();
    expect(fetchById).toHaveBeenCalledWith('old');
    expect(res.session).toBe(recovered);
    expect(res.cache).toBe(recovered); // cached for next poll
  });

  it('reuses the cache instead of re-fetching on subsequent polls', async () => {
    const cached = makeSession({ id: 'old' });
    const fetchById = vi.fn();
    const res = await resolveOpenSession({
      id: 'old',
      fetched: [makeSession({ id: 'recent' })],
      cached,
      fetchById,
    });
    expect(fetchById).not.toHaveBeenCalled();
    expect(res.session).toBe(cached);
    expect(res.cache).toBe(cached);
  });

  it('prefers the fresh list entry over a stale cache', async () => {
    const cached = makeSession({ id: 'open', status: 'done' });
    const fresh = makeSession({ id: 'open', status: 'busy' });
    const res = await resolveOpenSession({
      id: 'open',
      fetched: [fresh],
      cached,
      fetchById: vi.fn(),
    });
    expect(res.session).toBe(fresh);
  });

  it('is a no-op with no active id', async () => {
    const fetchById = vi.fn();
    const res = await resolveOpenSession({
      id: undefined,
      fetched: [makeSession({ id: 'x' })],
      cached: null,
      fetchById,
    });
    expect(res.session).toBeUndefined();
    expect(fetchById).not.toHaveBeenCalled();
  });

  it('is non-fatal when the fetch fails: no session, cache untouched, onError called', async () => {
    const err = new Error('network');
    const onError = vi.fn();
    const res = await resolveOpenSession({
      id: 'old',
      fetched: [],
      cached: null,
      fetchById: vi.fn().mockRejectedValue(err),
      onError,
    });
    expect(res.session).toBeUndefined();
    expect(res.cache).toBeNull();
    expect(onError).toHaveBeenCalledWith(err);
  });
});

describe('mergeSidebarSessions', () => {
  it('clears a pending permission flag once the server reports it answered', () => {
    const current = [makeSession({ id: 'a', pendingPermission: true, pendingQuestion: true })];
    const next = [makeSession({ id: 'a', pendingPermission: false, pendingQuestion: false })];
    const merged = mergeSidebarSessions(next, current, 'a');
    expect(merged[0].pendingPermission).toBe(false);
    expect(merged[0].pendingQuestion).toBe(false);
  });

  it('lets the poll status win over a store busy, and keeps seen sticky', () => {
    const current = [makeSession({ id: 'a', status: 'busy', seen: true })];
    const next = [makeSession({ id: 'a', status: 'done', seen: false })];
    const merged = mergeSidebarSessions(next, current, undefined);
    expect(merged[0].status).toBe('done');
    expect(merged[0].seen).toBe(true);
  });

  it('forces the active session unarchived even with no store row', () => {
    const next = [makeSession({ id: 'a', archived: true }), makeSession({ id: 'b', archived: true })];
    const merged = mergeSidebarSessions(next, [], 'a');
    expect(merged[0].archived).toBe(false);
    expect(merged[1].archived).toBe(true);
  });
});
