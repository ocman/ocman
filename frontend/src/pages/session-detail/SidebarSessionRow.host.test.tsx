// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { expect, it, vi } from 'vitest';
import type { Session } from '../../lib/api';
import { SidebarSessionRow } from './SidebarSessionRow';

vi.mock('../../lib/useCapabilities', () => ({ useMultiHost: () => false }));

function renderRow(remoteId: string, stale = false, onNavigateToSession = vi.fn()) {
  const session = { id: 'remote-session', platform: 'opencode', directory: '/projects/repo',
    title: 'Remote work', status: 'done', timeCreated: 1, timeUpdated: 1,
    remoteId, remoteName: 'Build host', stale } as Session;
  return render(<MemoryRouter><SidebarSessionRow session={session} flat inGroup={false}
    active={false} activeId={undefined} activeDisplayStatus="done" archiving={false}
    draft={false} debugMode={false} gitInfo={undefined} onNavigateToSession={onNavigateToSession}
    onArchiveSession={vi.fn()} onPinSession={vi.fn()} /></MemoryRouter>);
}

it.each([false, true])('shows the remote host next to the flat row project with stale=%s', (stale) => {
  renderRow('r1', stale);
  const badge = screen.getByLabelText(stale ? 'Build host (offline — last known)' : 'Build host');
  expect(badge.parentElement).toHaveClass('session-sidebar-project');
  expect(badge.previousElementSibling).toHaveClass('session-sidebar-project-path');
  expect(badge.querySelector('.bi-hdd-network')).toHaveAttribute('aria-hidden', 'true');
});

it('omits the remote badge from local flat rows', () => {
  renderRow('local');
  expect(screen.queryByLabelText('Build host')).not.toBeInTheDocument();
});

it('keeps the host pill display-only and preserves mouse and keyboard row navigation', () => {
  const navigate = vi.fn();
  renderRow('r1', false, navigate);
  fireEvent.click(screen.getByLabelText('Build host'));
  fireEvent.keyDown(screen.getByRole('button', { name: /Remote work/ }), { key: 'Enter' });
  expect(navigate).toHaveBeenNthCalledWith(1, 'remote-session');
  expect(navigate).toHaveBeenNthCalledWith(2, 'remote-session');
});
