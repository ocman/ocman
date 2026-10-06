// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import type { Session } from '../../lib/api';
import type { SidebarProjectGroup as Group } from './SessionSidebar';
import { SidebarProjectGroup } from './SidebarProjectGroup';

it('keeps the main checkout first and worktrees ordered by completion, not streaming activity', () => {
  const newer = { id: 'newer', platform: 'opencode', directory: '/parent/.worktrees/repo/newer',
    timeCreated: 1, timeUpdated: 20, lastTurnCompletedAt: 20 } as Session;
  const older = { ...newer, id: 'older', directory: '/parent/.worktrees/repo/older',
    timeUpdated: 999, lastTurnCompletedAt: 10 };
  const main = { ...newer, id: 'main', directory: '/parent/repo', lastTurnCompletedAt: 1 };
  const group: Group = { directory: '/parent/repo', sessions: [newer, older, main],
    lastUpdated: 999, aggregate: { kind: 'none' } };
  const props = { group, collapsed: false, siblingGitInfos: {}, toggleCollapsedProject: vi.fn(),
    onNewSessionInDirectory: vi.fn(), onArchiveProject: vi.fn(),
    renderRow: (session: Session) => <div key={session.id} data-testid="session-row">{session.id}</div> };
  const { rerender } = render(<SidebarProjectGroup {...props} />);
  const order = () => screen.getAllByTestId('session-row').map(row => row.textContent);
  expect(order()).toEqual(['main', 'newer', 'older']);
  rerender(<SidebarProjectGroup {...props} group={{ ...group,
    sessions: [newer, { ...older, timeUpdated: 2000 }, main] }} />);
  expect(order()).toEqual(['main', 'newer', 'older']);
  rerender(<SidebarProjectGroup {...props} group={{ ...group,
    sessions: [{ ...older, lastTurnCompletedAt: 30 }, newer, main] }} />);
  expect(order()).toEqual(['main', 'older', 'newer']);
});
