// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { expect, it, vi } from 'vitest';
import type { GitInfo, Session } from '../../lib/api';
import { SidebarSessionRow } from './SidebarSessionRow';

vi.mock('../../lib/useCapabilities', () => ({ useMultiHost: () => false }));

const clean = { branch: 'feature', dirty: false, ahead: 0, behind: 0 };

function renderRow(gitInfo?: GitInfo, stale = false, flat = true) {
  const session = { id: 'git-session', platform: 'opencode', directory: '/repo',
    title: 'Review sidebar', status: 'done', timeCreated: 1, timeUpdated: 1, stale } as Session;
  const navigate = vi.fn();
  render(<MemoryRouter><SidebarSessionRow session={session} flat={flat} inGroup={!flat}
    active={false} activeId={undefined} activeDisplayStatus="done" archiving={false}
    draft={false} debugMode={false} gitInfo={gitInfo} onNavigateToSession={navigate}
    onArchiveSession={vi.fn()} onPinSession={vi.fn()} /></MemoryRouter>);
  return navigate;
}

it('shows both Git reasons next to the branch in one keyboard-accessible tooltip', async () => {
  const navigate = renderRow({ ...clean, dirty: true, ahead: 3 });
  const label = 'Uncommitted changes · 3 unpushed commits';
  const indicator = screen.getByRole('img', { name: label });
  expect(indicator.previousElementSibling).toHaveTextContent('feature');
  expect(indicator).toHaveAttribute('tabindex', '0');
  fireEvent.focus(indicator);
  expect(await screen.findByRole('tooltip')).toHaveTextContent(label);
  fireEvent.keyDown(indicator, { key: 'Enter' });
  expect(navigate).not.toHaveBeenCalled();
});

it.each([
  [{ ...clean, dirty: true }, false, 'Uncommitted changes'],
  [{ ...clean, ahead: 1 }, false, '1 unpushed commit'],
  [{ ...clean, dirty: true }, true, 'Last known: Uncommitted changes'],
] as const)('labels a single Git reason (%j, stale=%s) as %s', (info, stale, label) => {
  renderRow(info, stale);
  expect(screen.getByRole('img', { name: label })).toBeInTheDocument();
});

it.each([
  ['clean', clean, true],
  ['unknown', undefined, true],
  ['non-Git', { ...clean, branch: '' }, true],
  ['grouped', { ...clean, dirty: true, ahead: 3 }, false],
] as const)('does not show an indicator for %s sessions', (_name, info, flat) => {
  renderRow(info, false, flat);
  expect(screen.queryByRole('img', { name: /uncommitted|unpushed/i })).not.toBeInTheDocument();
});
