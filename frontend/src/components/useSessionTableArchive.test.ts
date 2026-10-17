// @vitest-environment jsdom
import { act, renderHook } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import type { Session } from '../lib/api';
import { useSessionTableArchive } from './useSessionTableArchive';

const archiveSession = vi.hoisted(() => vi.fn());
vi.mock('../lib/apiStore', () => ({ useApiStore: (select: (state: { archiveSession: typeof archiveSession }) => unknown) => select({ archiveSession }) }));
const session = { id: 'session', platform: 'r-box:opencode', timeUpdated: 123 } as Session;
const event = () => ({ stopPropagation: vi.fn() }) as never;
beforeEach(() => archiveSession.mockReset());

it('marks the archive pending synchronously, deduplicates it and commits after success', async () => {
  let finish!: (value: { ok: boolean }) => void;
  archiveSession.mockReturnValue(new Promise(resolve => { finish = resolve; }));
  const { result } = renderHook(useSessionTableArchive);
  let pending!: Promise<void>;
  act(() => {
    pending = result.current.handleArchiveSession(event(), session);
    void result.current.handleArchiveSession(event(), session);
  });
  expect(result.current.archivingSessionIds.has(session.id)).toBe(true);
  expect(archiveSession).toHaveBeenCalledExactlyOnceWith('r-box:opencode', 'session', 123, true);
  await act(async () => { finish({ ok: true }); await pending; });
  expect(result.current.archivingSessionIds.size).toBe(0);
  expect(result.current.locallyArchivedSessionIds.has(session.id)).toBe(true);
});

it('restores the pending row on failure and clears the error when retried', async () => {
  archiveSession.mockRejectedValueOnce(new Error('Offline')).mockResolvedValueOnce({ ok: true });
  const { result } = renderHook(useSessionTableArchive);
  await act(async () => { await result.current.handleArchiveSession(event(), session); });
  expect(result.current.archivingSessionIds.size).toBe(0);
  expect(result.current.locallyArchivedSessionIds.size).toBe(0);
  expect(result.current.archiveError).toMatch(/Could not archive session/);
  await act(async () => { await result.current.handleArchiveSession(event(), session); });
  expect(result.current.archiveError).toBeNull();
  expect(result.current.locallyArchivedSessionIds.has(session.id)).toBe(true);
});
