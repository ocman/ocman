// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import type { Session } from '../../lib/api';
import { SidebarSessionRow } from './SidebarSessionRow';

function renderChild(sourceChild: boolean, flat = true, depth = 1) {
  const navigate = vi.fn();
  const archive = vi.fn((event: React.MouseEvent) => event.stopPropagation());
  const session = {
    id: 'child', platform: 'test', parentId: 'parent', title: 'Build login UI', directory: '/web-app',
    branch: 'feature/login', timeUpdated: 1, status: 'busy', remoteId: 'remote', remoteName: 'Build host',
    sourceChild,
  } as Session;
  render(<MemoryRouter><SidebarSessionRow session={session} flat={flat} inGroup={false} depth={depth}
    active activeId="child" activeDisplayStatus="busy" archiving={false} draft={false} debugMode={false}
    gitInfo={undefined} onNavigateToSession={navigate} onArchiveSession={archive} onPinSession={vi.fn()} /></MemoryRouter>);
  const row = screen.getByText('Build login UI').closest('[data-session-key]')!;
  return { row: row as HTMLElement, navigate, archive };
}

describe('nested sidebar session cards', () => {
  it.each([1, 2])('uses the full flat card for a subsession at depth %s with selection on the whole row', (depth) => {
    const { row, navigate, archive } = renderChild(false, true, depth);
    expect(row).toHaveClass('flat', 'active', 'session-sidebar-item-child');
    expect(within(row).getByText('web-app')).toBeInTheDocument();
    expect(within(row).getByText('feature/login')).toBeInTheDocument();
    expect(within(row).getByText('Build host')).toBeInTheDocument();
    expect(within(row).getByText('└─')).toHaveStyle({ '--depth': String(depth) });
    expect(within(row).getByText('└─').parentElement).toBe(row);
    expect(row).toHaveAttribute('aria-selected', 'true');
    expect(within(row).getByTitle(new Date(1).toLocaleString())).toBeInTheDocument();
    fireEvent.click(within(row).getByText('└─'));
    expect(navigate).toHaveBeenCalledWith('child');
    fireEvent.click(within(row).getByRole('button', { name: 'Archive session' }));
    expect(archive).toHaveBeenCalledOnce();
    expect(navigate).toHaveBeenCalledOnce();
    fireEvent.keyDown(row, { key: 'Enter' });
    expect(navigate).toHaveBeenCalledTimes(2);
  });

  it('keeps native agents compact even in the flat view', () => {
    const { row } = renderChild(true);
    expect(row).not.toHaveClass('flat');
    expect(row).toHaveClass('session-sidebar-item-child');
    expect(within(row).queryByText('web-app')).not.toBeInTheDocument();
    expect(within(row).queryByText('feature/login')).not.toBeInTheDocument();
  });

  it('does not introduce full cards in grouped rows', () => {
    const { row } = renderChild(false, false);
    expect(row).not.toHaveClass('flat');
    expect(within(row).queryByText('web-app')).not.toBeInTheDocument();
  });
});
