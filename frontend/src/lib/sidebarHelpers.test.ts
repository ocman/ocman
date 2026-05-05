import { describe, it, expect } from 'vitest';
import type { Session } from './api';
import { computeSidebarHash, rollupGroupStatus } from './sidebarHelpers';

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
    ...overrides,
  };
}

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
