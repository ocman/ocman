// @vitest-environment jsdom
import { act, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, expect, it, vi } from 'vitest';
import type { Session } from '../../lib/api';
import { queryClient } from '../../lib/queryClient';
import { getSidebarSessions, setSidebarSessions, SIDEBAR_SESSIONS_KEY, useSidebarSessionList } from '../../lib/sessionsCache';
import { SidebarSessionRow, type SidebarSessionRowProps } from './SidebarSessionRow';

const session = {
  id: 's', platform: 'opencode', directory: '/repo', title: 'Background work',
  status: 'done', seen: true, timeUpdated: 1, timeCreated: 1,
  backgroundTaskCount: 3, backgroundServiceCount: 1,
} as Session;

function row(overrides: Partial<SidebarSessionRowProps> = {}) {
  return <MemoryRouter><SidebarSessionRow
    session={session} inGroup={false} active={false} activeId="other"
    activeDisplayStatus="done" archiving={false} draft={false} debugMode={false}
    gitInfo={undefined} onNavigateToSession={vi.fn()} onArchiveSession={vi.fn()}
    onPinSession={vi.fn()} {...overrides}
  /></MemoryRouter>;
}

beforeEach(() => { queryClient.removeQueries({ queryKey: SIDEBAR_SESSIONS_KEY }); });

it.each([false, true])('shows a slow spinner for finite tasks on a seen session with flat=%s', (flat) => {
  render(row({ flat, inGroup: !flat }));
  expect(screen.getByLabelText('Session status: Waiting for 2 background tasks')).toHaveAttribute('data-state', 'background');
});

it('uses the active foreground status, then stops the background spinner when tasks finish', () => {
  const { rerender } = render(row({ active: true, activeDisplayStatus: 'busy' }));
  expect(screen.getByLabelText('Session status: Busy')).toHaveAttribute('data-state', 'busy');
  rerender(row({ active: true }));
  expect(screen.getByLabelText('Session status: Waiting for 2 background tasks')).toHaveAttribute('data-state', 'background');
  rerender(row({ active: true, session: { ...session, backgroundTaskCount: 1 } }));
  expect(screen.getByLabelText('Session status: Done')).toHaveAttribute('data-state', 'viewed');
});

it('labels a single task and supports waiting foreground status', () => {
  render(row({ session: { ...session, status: 'waiting', backgroundTaskCount: 2 } }));
  expect(screen.getByLabelText('Session status: Waiting for 1 background task')).toHaveAttribute('data-state', 'background');
});

it.each([
  { backgroundTaskCount: undefined, backgroundServiceCount: undefined },
  { backgroundTaskCount: 0, backgroundServiceCount: 0 },
  { backgroundTaskCount: 2, backgroundServiceCount: 2 },
  { backgroundTaskCount: 1, backgroundServiceCount: 2 },
])('does not spin for absent tasks or services only: %j', (counts) => {
  render(row({ session: { ...session, ...counts } }));
  expect(screen.getByLabelText('Session status: Done')).toHaveAttribute('data-state', 'viewed');
});

it.each([
  ['busy', 'busy'], ['error', 'error'], ['interrupted', 'viewed'], ['limited', 'limited'],
] as const)('does not hide foreground %s behind background work', (status, state) => {
  render(row({ session: { ...session, status } }));
  expect(screen.getByLabelText(/^Session status:/)).toHaveAttribute('data-state', state);
});

it.each(['pendingPermission', 'pendingQuestion'] as const)('keeps %s actionable', (pending) => {
  render(row({ session: { ...session, [pending]: true } }));
  expect(screen.getByLabelText('Session status: Waiting for your response')).toHaveAttribute('data-state', 'permission');
});

it('preserves the unsent draft indicator', () => {
  render(row({ draft: true }));
  expect(screen.getByLabelText('Session status: Unsent draft')).toHaveAttribute('data-state', 'draft');
});

it('renders count-only refreshes without changing foreground status or activity', () => {
  function CachedRow() {
    const [session] = useSidebarSessionList();
    return row({ session });
  }
  setSidebarSessions([{ ...session, backgroundTaskCount: 0, backgroundServiceCount: 0 }]);
  render(<CachedRow />);
  expect(screen.getByLabelText('Session status: Done')).toHaveAttribute('data-state', 'viewed');
  act(() => setSidebarSessions([{ ...session, backgroundTaskCount: 2, backgroundServiceCount: 0 }]));
  expect(screen.getByLabelText('Session status: Waiting for 2 background tasks')).toHaveAttribute('data-state', 'background');
  act(() => setSidebarSessions([{ ...session, backgroundTaskCount: 2, backgroundServiceCount: 1 }]));
  expect(screen.getByLabelText('Session status: Waiting for 1 background task')).toHaveAttribute('data-state', 'background');
  act(() => setSidebarSessions([{ ...session, backgroundTaskCount: 2, backgroundServiceCount: 2 }]));
  expect(screen.getByLabelText('Session status: Done')).toHaveAttribute('data-state', 'viewed');
  expect(getSidebarSessions()[0]).toMatchObject({ status: 'done', timeUpdated: 1 });
});
