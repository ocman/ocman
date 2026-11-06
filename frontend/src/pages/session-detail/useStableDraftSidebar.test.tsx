// @vitest-environment jsdom
import { act, renderHook } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { useStableDraftSidebar } from './useStableDraftSidebar';
import { useBackendConversationDrafts } from '../../lib/backendConversationDrafts';
import { useNewConversationDrafts } from '../../lib/newConversationDrafts';
import type { Session } from '../../lib/api.types';
import type { SidebarProjectGroup } from './SessionSidebar';

vi.mock('../../lib/api.requests', () => ({ fetchJSON: vi.fn(async () => []), postJSON: vi.fn(async () => undefined) }));
vi.mock('../../lib/useProjectIdentityIndex', () => ({ useProjectIdentityIndex: () => () => ({ key: 'project', directory: '/repo', remoteId: 'local' }) }));
const id = 'ocm_01234567-89ab-4cde-8fab-0123456789ab';
const meta = { draftId: id, directory: '/repo', platform: 'opencode', remoteId: 'local', createdAt: 100 };
const group: SidebarProjectGroup = { key: 'project', directory: '/repo', sessions: [], lastUpdated: 0, aggregate: { kind: 'none' } };

beforeEach(() => {
  useNewConversationDrafts.setState({ drafts: [meta] });
  useBackendConversationDrafts.setState({ drafts: {} });
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
