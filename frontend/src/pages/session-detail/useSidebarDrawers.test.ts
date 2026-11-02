// @vitest-environment jsdom
import { act, renderHook } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { Session } from '../../lib/api';
import { checkoutKey } from '../../lib/projectIdentity';
import { runningSidebarDrawers, useSidebarDrawers } from './useSidebarDrawers';

const session = (id: string, patch: Partial<Session> = {}) => ({
  id, platform: 'local', title: id, directory: '/repo', status: 'busy', timeCreated: 1, ...patch,
}) as Session;

describe('running sidebar drawers', () => {
  it('keeps only busy sessions and assigns overlapping categories once, with origin first', () => {
    const archive = session('archive', { archived: true });
    const result = runningSidebarDrawers([
      archive, archive, session('plain'), session('done', { status: 'done', archived: true, backgroundTaskCount: 1 }),
      session('waiting', { status: 'waiting', archived: true, pendingPermission: true }),
      session('routine', { archived: true, routineId: 'r' }), session('plugin', { archived: true, origin: 'plugin' }),
    ], '', {});
    expect(result.map((drawer) => [drawer.id, drawer.sessions.map((row) => row.id)])).toEqual([
      ['archived', ['archive']], ['routines', ['routine']], ['plugins', ['plugin']],
    ]);
  });

  it('inherits origins across descendants and keeps identities separate by owner', () => {
    const result = runningSidebarDrawers([
      session('parent', { origin: 'plugin', status: 'done' }), session('child', { parentId: 'parent' }),
      session('grandchild', { parentId: 'child' }), session('parent', { platform: 'remote', archived: true }),
      session('routine', { routineId: 'r', status: 'done' }), session('run-child', { parentId: 'routine' }),
    ], '', {});
    expect(result.map((drawer) => [drawer.id, drawer.sessions.map((row) => row.id)])).toEqual([
      ['archived', ['parent']], ['routines', ['run-child']], ['plugins', ['child', 'grandchild']],
    ]);
  });

  it('shares title, directory and owner-qualified branch search without pin or prompt bypasses', () => {
    const rows = [session('archive', { archived: true, title: 'Deploy fix', pinned: true, pendingQuestion: true }),
      session('plugin', { origin: 'plugin', directory: '/other', remoteId: 'box', branch: 'stored' })];
    const infos = { [checkoutKey('/other', 'box')]: { branch: 'feature/search', ahead: 0, behind: 0, dirty: false } };
    expect(runningSidebarDrawers(rows, 'Deploy', {}).flatMap((drawer) => drawer.sessions).map((row) => row.id)).toEqual(['archive']);
    expect(runningSidebarDrawers(rows, '/other', {}).map((drawer) => drawer.id)).toEqual(['plugins']);
    expect(runningSidebarDrawers(rows, 'feature/search', infos).map((drawer) => drawer.id)).toEqual(['plugins']);
    expect(runningSidebarDrawers(rows, 'stored', {}).map((drawer) => drawer.id)).toEqual(['plugins']);
    expect(runningSidebarDrawers(rows, 'absent', {})).toEqual([]);
  });

  it('uses the active session live status and drops a drawer as its final session finishes', () => {
    const rows = [session('archive', { archived: true })];
    expect(runningSidebarDrawers(rows, '', {}, 'archive', 'done')).toEqual([]);
    expect(runningSidebarDrawers([{ ...rows[0], status: 'done' }], '', {}, 'archive', 'busy')).toHaveLength(1);
    const { result, rerender } = renderHook(({ rows }) => useSidebarDrawers(rows, '', {}), { initialProps: { rows } });
    expect(result.current.sections).toEqual([]);
    act(() => result.current.toggle('archived'));
    expect(result.current.sections).toEqual([rows]);
    rerender({ rows: [{ ...rows[0], status: 'done' }] });
    expect(result.current.drawers).toEqual([]);
    expect(result.current.sections).toEqual([]);
    rerender({ rows });
    expect(result.current.sections).toEqual([rows]);
    act(() => result.current.toggle('archived'));
    expect(result.current.sections).toEqual([]);
  });

  it('puts snoozed sessions in their own drawer regardless of status or origin', () => {
    const rows = [session('snoozed', { archived: true, status: 'done', snoozedUntil: 9999999999999, pinned: true }),
      session('plugin', { origin: 'plugin', snoozedUntil: 9999999999999 }),
      session('routine', { routineId: 'r', status: 'waiting', snoozedUntil: 9999999999999 })];
    const drawers = runningSidebarDrawers(rows, '', {});
    expect(drawers.map((drawer) => drawer.id)).toEqual(['snoozed']);
    expect(drawers[0].sessions.map((row) => row.id)).toEqual(['plugin', 'routine', 'snoozed']);
    expect(runningSidebarDrawers(rows, 'snoozed', {})[0].sessions.map((row) => row.id)).toEqual(['snoozed']);
    expect(runningSidebarDrawers(rows, 'missing', {})).toEqual([]);
  });
});
