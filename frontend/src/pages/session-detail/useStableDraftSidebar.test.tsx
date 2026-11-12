// @vitest-environment jsdom
import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { useStableDraftSidebar } from './useStableDraftSidebar';
import { useBackendConversationDrafts } from '../../lib/backendConversationDrafts';
import { useNewConversationDrafts } from '../../lib/newConversationDrafts';
import type { Session } from '../../lib/api.types';
import type { SidebarProjectGroup } from './SessionSidebar';
import { fetchJSON, postJSON } from '../../lib/api.requests';

vi.mock('../../lib/api.requests', () => ({ fetchJSON: vi.fn(async () => []), postJSON: vi.fn(async () => undefined) }));
const { project } = vi.hoisted(() => ({ project: () => ({ key: 'project', directory: '/repo', remoteId: 'local' }) }));
vi.mock('../../lib/useProjectIdentityIndex', () => ({ useProjectIdentityIndex: () => project }));
const id = 'ocm_01234567-89ab-4cde-8fab-0123456789ab';
const meta = { draftId: id, directory: '/repo', platform: 'opencode', remoteId: 'local', createdAt: 100 };
const ready = { [id]: { id, metadata: meta, phase: 'ready' as const, createdAt: 100, steps: {} } };
const group: SidebarProjectGroup = { key: 'project', directory: '/repo', sessions: [], lastUpdated: 0, aggregate: { kind: 'none' } };

beforeEach(() => {
  vi.mocked(postJSON).mockReset().mockResolvedValue(undefined);
  vi.mocked(fetchJSON).mockReset().mockResolvedValue([]);
  useNewConversationDrafts.setState({ drafts: [meta] });
  useBackendConversationDrafts.setState({ drafts: {} });
});

it('does not duplicate pending cleanup or leave a newer selection when it commits', async () => {
  let release!: () => void;
  vi.mocked(postJSON).mockReturnValue(new Promise<void>((resolve) => { release = resolve; }));
  const navigate = vi.fn();
  const { result, rerender } = renderHook(({ active }) => useStableDraftSidebar([], [group], navigate, active), { initialProps: { active: id } });
  await act(async () => {});
  act(() => {
    expect(result.current.discard(id)).toBe(true);
    expect(result.current.discard(id)).toBe(true);
  });
  expect(postJSON).toHaveBeenCalledTimes(1);
  expect(result.current.discarding.has(id)).toBe(true);
  rerender({ active: 'other' });
  await act(async () => { release(); });
  expect(navigate).not.toHaveBeenCalled();
  await waitFor(() => expect(result.current.discarding.has(id)).toBe(false));
  expect(navigate).not.toHaveBeenCalled();
});

afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); });

it('archives accepted setup without deleting its submitted prompt', async () => {
  const starting = { id, metadata: meta, phase: 'starting' as const, createdAt: 100, steps: {} };
  vi.mocked(fetchJSON).mockResolvedValue([starting]);
  useBackendConversationDrafts.setState({ drafts: { [id]: starting } });
  const navigate = vi.fn();
  const { result } = renderHook(() => useStableDraftSidebar([], [group], navigate, id));
  await act(async () => {});
  await act(async () => { expect(result.current.discard(id)).toBe(true); });
  expect(postJSON).toHaveBeenCalledWith(`/api/session-drafts/${id}/archive`, undefined, { parseJSON: false });
  expect(useNewConversationDrafts.getState().drafts).toEqual([meta]);
  expect(result.current.recent).toEqual([]);
  expect(navigate).toHaveBeenCalled();
});

it('restores archive state on reload, exposes it in the archive view and resurfaces setup errors', async () => {
  const starting = { id, metadata: meta, phase: 'starting' as const, archived: true, createdAt: 100, steps: {} };
  vi.mocked(fetchJSON).mockResolvedValue([starting]);
  const { result, rerender } = renderHook(({ archived }) => useStableDraftSidebar([], [group], vi.fn(), undefined, archived), { initialProps: { archived: false } });
  await act(async () => {});
  expect(result.current.recent).toEqual([]);
  rerender({ archived: true });
  expect(result.current.recent[0]).toMatchObject({ id, archived: true });
  rerender({ archived: false });
  act(() => useBackendConversationDrafts.setState({ drafts: { [id]: { ...starting, phase: 'failed', error: 'setup failed' } } }));
  expect(result.current.recent[0]).toMatchObject({ id, archived: false, status: 'error' });
});

it('retains setup and offers retry when archiving fails', async () => {
  const starting = { id, metadata: meta, phase: 'starting' as const, createdAt: 100, steps: {} };
  vi.mocked(fetchJSON).mockResolvedValue([starting]);
  vi.mocked(postJSON).mockRejectedValueOnce(new Error('offline'));
  const navigate = vi.fn();
  const { result } = renderHook(() => useStableDraftSidebar([], [group], navigate, id));
  await act(async () => {});
  await act(async () => { result.current.discard(id); });
  expect(result.current.discardError).toContain('Could not archive the session: offline');
  expect(result.current.recent).toHaveLength(1);
  expect(navigate).not.toHaveBeenCalled();
  await act(async () => { result.current.retryDiscard(); });
  expect(result.current.recent).toEqual([]);
});

it('pauses hidden reads, reconciles on return and stops after unmount', async () => {
  vi.useFakeTimers();
  let hidden = true;
  vi.spyOn(document, 'hidden', 'get').mockImplementation(() => hidden);
  const view = renderHook(() => useStableDraftSidebar([], [group], vi.fn()));
  await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
  expect(fetchJSON).not.toHaveBeenCalled();
  hidden = false;
  await act(async () => { document.dispatchEvent(new Event('visibilitychange')); });
  expect(fetchJSON).toHaveBeenCalledTimes(1);
  hidden = true;
  await act(async () => { document.dispatchEvent(new Event('visibilitychange')); await vi.advanceTimersByTimeAsync(30_000); });
  expect(fetchJSON).toHaveBeenCalledTimes(1);
  view.unmount();
  hidden = false;
  await act(async () => { document.dispatchEvent(new Event('visibilitychange')); window.dispatchEvent(new Event('online')); await vi.advanceTimersByTimeAsync(30_000); });
  expect(fetchJSON).toHaveBeenCalledTimes(1);
});

it('uses one row ID through draft, setup and actual session publication', async () => {
  const navigate = vi.fn();
  const { result, rerender } = renderHook(({ sessions }: { sessions: Session[] }) => useStableDraftSidebar(sessions,
    [{ ...group, sessions }], navigate), { initialProps: { sessions: [] as Session[] } });
  await act(async () => {});
  expect(result.current.recent.map((row) => row.id)).toEqual([id]);
  expect(result.current.groups[0].sessions.map((row) => row.id)).toEqual([id]);
  expect(result.current.open(id)).toBe(true);
  expect(navigate).toHaveBeenLastCalledWith(expect.stringContaining(`/session/${id}?`));
  act(() => useBackendConversationDrafts.setState({ drafts: { [id]: { id, metadata: meta, phase: 'starting', createdAt: 100, steps: { platform: 'active' } } } }));
  const actual = { ...result.current.recent[0], directory: '/worktree', title: 'Named session', status: 'busy' as const, messageCount: 1 };
  rerender({ sessions: [actual] });
  expect(result.current.recent).toHaveLength(1);
  expect(result.current.groups.flatMap((entry) => entry.sessions)).toHaveLength(1);
  act(() => useBackendConversationDrafts.setState({ drafts: { [id]: { id, metadata: meta, phase: 'ready', createdAt: 100, steps: {} } } }));
  expect(result.current.recent).toEqual([actual]);
  expect(result.current.ids.has(id)).toBe(false);
  expect(result.current.open(id)).toBe(false);
  expect(result.current.discard(id)).toBe(false);
});

it('does not resurface a completed background draft after its session row is archived', async () => {
  vi.mocked(fetchJSON).mockResolvedValue(Object.values(ready));
  useBackendConversationDrafts.setState({ drafts: ready });
  const actual = { id, directory: '/repo', timeUpdated: 200 } as Session;
  const { result, rerender } = renderHook(({ sessions }: { sessions: Session[] }) => useStableDraftSidebar(sessions,
    [{ ...group, sessions }], vi.fn()), { initialProps: { sessions: [actual] } });
  await act(async () => {});
  rerender({ sessions: [] });
  await waitFor(() => expect(result.current.recent).toEqual([]));
  expect(result.current.groups.flatMap((entry) => entry.sessions)).toEqual([]);
});

it('leaves retiring the open draft to its conversation page', async () => {
  vi.mocked(fetchJSON).mockResolvedValue(Object.values(ready));
  useBackendConversationDrafts.setState({ drafts: ready });
  renderHook(() => useStableDraftSidebar([{ id } as Session], [group], vi.fn(), id));
  await act(async () => {});
  expect(useNewConversationDrafts.getState().drafts).toEqual([meta]);
});

it('keeps a completed background draft visible until the actual list row arrives', async () => {
  const { result } = renderHook(() => useStableDraftSidebar([], [group], vi.fn()));
  await act(async () => {});
  act(() => useBackendConversationDrafts.setState({ drafts: { [id]: { id, metadata: meta, phase: 'ready', createdAt: 100, steps: {} } } }));
  expect(result.current.recent.map((row) => row.id)).toEqual([id]);
  expect(result.current.groups[0].sessions.map((row) => row.id)).toEqual([id]);
});

it('reuses rows, groups, membership and actions across transcript-only renders', async () => {
  const recent: Session[] = [];
  const groups = [group];
  const navigate = vi.fn();
  const { result, rerender } = renderHook(() => useStableDraftSidebar(recent, groups, navigate));
  await act(async () => {});
  const before = result.current;
  for (let i = 0; i < 50; i++) rerender();
  expect(result.current.recent).toBe(before.recent);
  expect(result.current.recent[0]).toBe(before.recent[0]);
  expect(result.current.groups).toBe(before.groups);
  expect(result.current.ids).toBe(before.ids);
  expect(result.current.open).toBe(before.open);
  expect(result.current.discard).toBe(before.discard);
  act(() => useBackendConversationDrafts.setState({ drafts: { [id]: { id, metadata: meta, phase: 'failed', error: 'failed', createdAt: 100, steps: {} } } }));
  expect(result.current.recent[0].status).toBe('error');
  expect(result.current.recent[0]).not.toBe(before.recent[0]);
});
