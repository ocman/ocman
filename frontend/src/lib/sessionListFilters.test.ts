import { describe, expect, it } from 'vitest';
import type { Session } from './api';
import { SESSION_FILTER_DEFAULTS, hiddenSessionOrigins, matchesSessionListFilters, type SessionListFilters } from './sessionListFilters';

const row = (value: Partial<Session> = {}) => ({ id: 'session', platform: 'local', ...value }) as Session;

describe('shared session filters', () => {
  it('has the desktop sidebar defaults', () => {
    expect(SESSION_FILTER_DEFAULTS).toEqual({ alwaysShowPrompts: true, showArchived: false, showSnoozed: false,
      showChildren: true, showFactory: false, showRoutines: false, showPlugins: false });
  });

  it('hides origin descendants without crossing owners and terminates on cycles', () => {
    const sessions = [row({ id: 'root', routineId: 'routine', parentId: 'grandchild' }), row({ id: 'child', parentId: 'root' }),
      row({ id: 'grandchild', parentId: 'child' }), row({ id: 'root', platform: 'remote' }), row({ id: 'child', platform: 'remote', parentId: 'root' }),
      row({ id: 'factory', factoryAttemptId: 'attempt' })];
    const show = (showFactory: boolean, showRoutines: boolean) => ({ showFactory, showRoutines, showPlugins: true });
    expect([...hiddenSessionOrigins(sessions, show(false, false))].sort()).toEqual(['local\0child', 'local\0factory', 'local\0grandchild', 'local\0root']);
    expect([...hiddenSessionOrigins(sessions, show(true, true))]).toEqual([]);
    expect([...hiddenSessionOrigins(sessions, show(false, true))]).toEqual(['local\0factory']);
    expect(hiddenSessionOrigins(sessions, show(true, false)).has('local\0factory')).toBe(false);
  });

  it('hides plugin sessions and their children unless shown', () => {
    const sessions = [row({ id: 'background', origin: 'plugin', originId: 'org.example.background' }), row({ id: 'sub', parentId: 'background' }), row({ id: 'user', origin: 'user' })];
    expect([...hiddenSessionOrigins(sessions, { showFactory: true, showRoutines: true, showPlugins: false })].sort()).toEqual(['local\0background', 'local\0sub']);
    expect([...hiddenSessionOrigins(sessions, { showFactory: true, showRoutines: true, showPlugins: true })]).toEqual([]);
  });

  it.each<[{ session?: Partial<Session>; filters?: Partial<SessionListFilters>; search?: boolean; active?: string; hidden?: boolean }, boolean]>([
    [{}, true],
    [{ session: { archived: true } }, false],
    [{ session: { archived: true }, filters: { showArchived: true } }, true],
    [{ session: { parentId: 'parent' } }, true],
    [{ session: { parentId: 'parent' }, filters: { showChildren: false } }, false],
    [{ search: false }, false],
    [{ hidden: true }, false],
    [{ session: { pinned: true }, hidden: true, search: false }, true],
    [{ active: 'session', hidden: true, search: false }, true],
    [{ session: { pendingQuestion: true, archived: true, parentId: 'parent' }, hidden: true, search: false, filters: { showChildren: false } }, true],
    [{ session: { pendingPermission: true, archived: true }, hidden: true }, true],
    [{ session: { pendingQuestion: true, archived: true }, filters: { alwaysShowPrompts: false } }, false],
    [{ session: { snoozedUntil: 123, archived: true, pinned: true, pendingQuestion: true }, active: 'session' }, false],
    [{ session: { snoozedUntil: 123, archived: true }, filters: { showSnoozed: true } }, true],
    [{ session: { archived: true }, filters: { showSnoozed: true } }, false],
  ])('applies the shared visibility rules for %j', (testCase, expected) => {
    expect(matchesSessionListFilters(row(testCase.session), { ...SESSION_FILTER_DEFAULTS, ...testCase.filters },
      new Set(testCase.hidden ? ['local\0session'] : []), testCase.search ?? true, testCase.active)).toBe(expected);
  });
});
