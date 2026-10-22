// @vitest-environment jsdom
import { act, fireEvent, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { ActiveSessions } from './ActiveSessions';
import { BackendStats } from './BackendStats';
vi.mock('./BackgroundTaskCounter', () => ({ BackgroundTaskCounter: () => null }));
import { useUiStore } from '../lib/uiStore';
import { TOOLTIP_DELAY_MS } from './Tooltip';

const { useSessions, useProjects, refetch } = vi.hoisted(() => ({ useSessions: vi.fn(), useProjects: vi.fn(), refetch: vi.fn() }));
vi.mock('../lib/queries', () => ({ useSessions, useProjects, useRunningSessionCount: () => ({ data: { count: 3 } }) }));

function Location() {
  const location = useLocation();
  return <output>{location.pathname}{location.search}</output>;
}

function mount(enabled = true) {
  return render(<MemoryRouter><ActiveSessions count={3} enabled={enabled} /><Location /></MemoryRouter>);
}

function advance(ms: number) { act(() => vi.advanceTimersByTime(ms)); }

beforeEach(() => {
  vi.useFakeTimers();
  useProjects.mockReturnValue({ data: [
    { directory: '/repo/alpha', projectKey: 'alpha' },
    { directory: '/checkout/alpha', projectKey: 'alpha', remoteId: 'box', remoteName: 'Box' },
  ] });
  useSessions.mockReturnValue({ data: [
    { id: 'local', title: 'Local task', directory: '/repo/alpha', platform: 'opencode', status: 'busy' },
    { id: 'remote/id', title: 'Remote task', directory: '/checkout/alpha', remoteId: 'box', platform: 'r-box:opencode', status: 'busy', parentId: 'parent', archived: true },
    { id: 'other', title: 'Other task', directory: '/repo/beta', platform: 'claudecode', status: 'busy' },
    { id: 'done', title: 'Finished task', directory: '/repo/beta', platform: 'opencode', status: 'done' },
  ], isPending: false, isError: false, refetch });
});
afterEach(() => { vi.useRealTimers(); vi.clearAllMocks(); });

it('delays hover, groups every busy session by project, and closes on navigation', () => {
  mount();
  const trigger = screen.getByRole('button', { name: 'Total active sessions: 3' });
  expect(trigger).toHaveTextContent('tas: 3');
  expect(useSessions).toHaveBeenLastCalledWith({ limit: 0 }, { enabled: false, poll: true });
  fireEvent.mouseEnter(trigger);
  advance(TOOLTIP_DELAY_MS - 1);
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  advance(1);
  const panel = screen.getByRole('dialog', { name: 'Active sessions' });
  expect(panel).not.toHaveFocus();
  expect(useSessions).toHaveBeenLastCalledWith({ limit: 0 }, { enabled: true, poll: true });
  expect(within(panel).getByRole('heading', { level: 2, name: 'Total active sessions' })).toBeVisible();
  expect(within(panel).getAllByRole('heading', { level: 3 })).toHaveLength(2);
  const alpha = within(panel).getByRole('heading', { name: 'repo/alpha' }).closest('section')!;
  expect(within(alpha).getAllByRole('link')).toHaveLength(2);
  expect(screen.queryByText('Finished task')).not.toBeInTheDocument();
  fireEvent.mouseLeave(trigger);
  advance(50);
  fireEvent.mouseEnter(within(panel).getByText('Remote task').parentElement!);
  advance(150);
  expect(panel).toBeInTheDocument();
  fireEvent.click(screen.getByRole('link', { name: 'Remote task' }));
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  expect(screen.getByRole('status')).toHaveTextContent('/session/remote%2Fid?platform=r-box%3Aopencode');
});

it('cancels short hovers and closes after leaving the popover', () => {
  mount();
  const trigger = screen.getByRole('button', { name: 'Total active sessions: 3' });
  fireEvent.mouseEnter(trigger);
  fireEvent.mouseLeave(trigger);
  advance(TOOLTIP_DELAY_MS);
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  fireEvent.click(trigger);
  const panel = screen.getByRole('dialog');
  fireEvent.mouseLeave(within(panel).getByText('Local task').closest('section')!.parentElement!);
  advance(150);
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
});

it('supports keyboard focus and Escape, and cleans up a pending hover on unmount', () => {
  const { unmount } = mount();
  const trigger = screen.getByRole('button', { name: 'Total active sessions: 3' });
  fireEvent.focus(trigger);
  expect(screen.getByRole('dialog')).toBeInTheDocument();
  fireEvent.keyDown(document, { key: 'Escape' });
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  fireEvent.mouseEnter(trigger);
  unmount();
  advance(TOOLTIP_DELAY_MS);
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
});

it.each([
  [{ isPending: true }, 'Loading active sessions…'],
  [{ isError: true }, 'Could not load active sessions.'],
  [{ data: [] }, 'No active sessions'],
])('shows list state %j', (state, text) => {
  useSessions.mockReturnValue({ data: [], refetch, ...state });
  mount();
  fireEvent.click(screen.getByRole('button', { name: 'Total active sessions: 3' }));
  expect(screen.getByText(text, { exact: false })).toBeInTheDocument();
  if ('isError' in state && state.isError) {
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(refetch).toHaveBeenCalled();
  }
});

it('does not open when disabled', () => {
  mount(false);
  expect(screen.getByRole('button', { name: 'Total active sessions: 3' })).toBeDisabled();
  expect(useProjects).toHaveBeenLastCalledWith({ enabled: false });
});

it('keeps keyboard navigation inside the panel and closes when focus leaves', () => {
  mount();
  const trigger = screen.getByRole('button', { name: 'Total active sessions: 3' });
  fireEvent.focus(trigger);
  const link = screen.getByRole('link', { name: 'Local task' });
  fireEvent.blur(trigger, { relatedTarget: link });
  expect(screen.getByRole('dialog')).toBeInTheDocument();
  fireEvent.blur(link, { relatedTarget: screen.getByRole('link', { name: 'Other task' }) });
  expect(screen.getByRole('dialog')).toBeInTheDocument();
  fireEvent.blur(link, { relatedTarget: document.body });
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  fireEvent.focus(trigger);
  fireEvent.blur(trigger, { relatedTarget: document.body });
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
});

it('uses the session id when its title is empty and closes on outside click', () => {
  useSessions.mockReturnValue({ data: [{ id: 'untitled', title: '', directory: '/repo', platform: 'opencode', status: 'busy' }] });
  mount();
  fireEvent.click(screen.getByRole('button', { name: 'Total active sessions: 3' }));
  expect(screen.getByRole('link', { name: 'untitled' })).toBeInTheDocument();
  fireEvent.mouseDown(document.body);
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
});

it('tabs from the real footer counter into the popover before the adjacent stats button', async () => {
  vi.useRealTimers();
  useUiStore.setState({ sidebarStatsExpanded: false });
  const user = userEvent.setup();
  render(<MemoryRouter><BackendStats /></MemoryRouter>);
  await user.tab();
  expect(screen.getByRole('button', { name: 'Total active sessions: 3' })).toHaveFocus();
  await user.tab();
  expect(screen.getByRole('dialog', { name: 'Active sessions' })).toHaveFocus();
  await user.tab();
  expect(screen.getByRole('link', { name: 'Local task' })).toHaveFocus();
  await user.keyboard('{Escape}');
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Total active sessions: 3' })).toHaveFocus();
  await user.tab({ shift: true });
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
});
