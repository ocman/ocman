// @vitest-environment jsdom
import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { useStableDraftSidebar } from './useStableDraftSidebar';
import { useBackendConversationDrafts } from '../../lib/backendConversationDrafts';
import { useNewConversationDrafts } from '../../lib/newConversationDrafts';
import type { Session } from '../../lib/api.types';
import type { SidebarProjectGroup } from './SessionSidebar';
import { postJSON } from '../../lib/api.requests';

vi.mock('../../lib/api.requests', () => ({ fetchJSON: vi.fn(async () => []), postJSON: vi.fn(async () => undefined) }));
vi.mock('../../lib/useProjectIdentityIndex', () => ({ useProjectIdentityIndex: () => () => ({ key: 'project', directory: '/repo', remoteId: 'local' }) }));
const id = 'ocm_01234567-89ab-4cde-8fab-0123456789ab';
const meta = { draftId: id, directory: '/repo', platform: 'opencode', remoteId: 'local', createdAt: 100 };
const group: SidebarProjectGroup = { key: 'project', directory: '/repo', sessions: [], lastUpdated: 0, aggregate: { kind: 'none' } };

beforeEach(() => {
  vi.mocked(postJSON).mockReset().mockResolvedValue(undefined);
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

it('keeps a completed background draft visible until the actual list row arrives', async () => {
  const { result } = renderHook(() => useStableDraftSidebar([], [group], vi.fn()));
  await act(async () => {});
  act(() => useBackendConversationDrafts.setState({ drafts: { [id]: { id, metadata: meta, phase: 'ready', createdAt: 100, steps: {} } } }));
  expect(result.current.recent.map((row) => row.id)).toEqual([id]);
  expect(result.current.groups[0].sessions.map((row) => row.id)).toEqual([id]);
});
