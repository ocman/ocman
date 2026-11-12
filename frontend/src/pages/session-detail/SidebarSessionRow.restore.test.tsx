// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { api, type Session } from '../../lib/api';
import { SidebarSessionRow } from './SidebarSessionRow';

beforeEach(() => vi.restoreAllMocks());

function setup(snoozedUntil = 0) {
  const navigate = vi.fn();
  const archive = vi.fn();
  const session = { id: 's1', platform: 'r-box:work', title: 'Hidden work', directory: '/repo',
    timeUpdated: 42, status: 'busy', archived: true, snoozedUntil } as Session;
  render(<MemoryRouter><SidebarSessionRow session={session} flat inGroup={false}
    active={false} activeId="other" activeDisplayStatus="busy" archiving={false} draft={false} debugMode={false}
    gitInfo={undefined} onNavigateToSession={navigate} onArchiveSession={archive} onPinSession={vi.fn()} /></MemoryRouter>);
  return { navigate, archive };
}

it.each([0, 9999999999999])('restores archived/snoozed rows explicitly without opening them, deadline=%s', async (until) => {
  let finish!: (value: { ok: boolean }) => void;
  const restore = vi.spyOn(api, 'archiveSession').mockImplementation(() => new Promise(resolve => { finish = resolve; }));
  const { navigate, archive } = setup(until);
  const button = screen.getByRole('button', { name: until ? 'Unsnooze session' : 'Unarchive session' });
  fireEvent.keyDown(button, { key: 'Enter' });
  expect(navigate).not.toHaveBeenCalled();
  fireEvent.click(button);
  expect(restore).toHaveBeenCalledExactlyOnceWith('r-box:work', 's1', 42, false);
  expect(button).toBeDisabled();
  expect(archive).not.toHaveBeenCalled();
  expect(navigate).not.toHaveBeenCalled();
  await act(async () => finish({ ok: true }));
  expect(button).not.toBeDisabled();
  fireEvent.click(screen.getByText('Hidden work'));
  expect(navigate).toHaveBeenCalledWith('s1', 'r-box:work');
});

it('shows restore failures and allows retry without navigating', async () => {
  const restore = vi.spyOn(api, 'archiveSession').mockRejectedValueOnce(new Error('Offline')).mockResolvedValue({ ok: true });
  const { navigate } = setup();
  fireEvent.click(screen.getByRole('button', { name: 'Unarchive session' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('Offline');
  fireEvent.click(screen.getByRole('button', { name: 'Unarchive session' }));
  await act(async () => {});
  expect(restore).toHaveBeenCalledTimes(2);
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  expect(navigate).not.toHaveBeenCalled();
});
