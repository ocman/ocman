// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import type { NotifyEntry, Session } from '../../lib/api';
import { useNotifySnapshot } from '../../lib/useNotifyData';
import { useUiStore } from '../../lib/uiStore';
import { SessionSidebar, type SessionSidebarProps } from './SessionSidebar';
import { seedView } from '../../test/oceFixtures';

vi.mock('../../lib/useNotifyData', () => ({ useNotifySnapshot: vi.fn() }));
vi.mock('../../components/BackendStats', () => ({ BackendStats: () => null }));
vi.mock('../../lib/queries', () => ({ useWorkEpics: () => ({ data: [] }) }));
vi.mock('../../lib/useCapabilities', async (original) => ({
  ...await original<typeof import('../../lib/useCapabilities')>(), useMultiHost: () => false,
}));

const entry: NotifyEntry = {
  id: 'old-factory', platform: 'opencode', title: 'Old Factory verification', directory: '/old-repo',
  status: 'interrupted', seen: false, pendingPermission: false, pendingQuestion: false,
};

function snapshot(entries: NotifyEntry[]) {
  vi.mocked(useNotifySnapshot).mockReturnValue({ entries, fetchedAt: Date.now() });
}

function Location() {
  const location = useLocation();
  return <output aria-label="Location">{location.pathname}{location.search}</output>;
}

function renderSidebar(view: 'recent' | 'projects', rows: Session[] = [], route = '/', activeId = 'other') {
  const props: SessionSidebarProps = {
    activeId, sidebarWidth: 300, sidebarView: view, setSidebarView: vi.fn(),
    showArchivedRecent: false, setShowArchivedRecent: vi.fn(), loadingRecentSessions: false,
    recentSessions: rows, sidebarProjectGroups: rows.length ? [{
      directory: '/old-repo', sessions: rows, lastUpdated: 1, aggregate: { kind: 'none' },
    }] : [],
    onReorderProjects: vi.fn(), archivingSessionIds: new Set(), collapsedProjectSet: new Set(['/old-repo']),
    toggleCollapsedProject: vi.fn(), siblingGitInfos: {}, activeDisplayStatus: 'busy', debugMode: false,
    pendingTmuxSession: null, pickerPos: null, pickerRef: { current: null },
    tmux: { available: false, isLocal: true, sessions: [], clients: [], switchSession: vi.fn(), findSession: vi.fn(), launchOpencode: vi.fn() },
    onNavigateToSession: vi.fn(), onArchiveSession: vi.fn(), onPinSession: vi.fn(), onNewSession: vi.fn(),
    onClientSelect: vi.fn(), onNewSessionInDirectory: vi.fn(), onArchiveProject: vi.fn(),
  };
  const tree = () => <MemoryRouter initialEntries={[route]}><SessionSidebar {...props} /><Location /></MemoryRouter>;
  const page = render(tree());
  return { ...page, refresh: () => page.rerender(tree()) };
}

beforeEach(() => {
  localStorage.clear();
  useUiStore.setState({ sidebarRecentHours: 72 });
  snapshot([entry]);
});

it.each(['recent', 'projects'] as const)('reveals a badge-counted Factory interruption absent from the 72h list in %s', (view) => {
  renderSidebar(view);
  const attention = screen.getByRole('region', { name: 'Needs attention' });
  expect(within(attention).getByText(entry.title!)).toBeInTheDocument();
  expect(within(attention).getByLabelText(/Session status: Interrupted/)).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Filter sessions' }));
  expect(screen.getByRole('checkbox', { name: 'Show factory' })).not.toBeChecked();
  fireEvent.change(screen.getByRole('searchbox', { name: 'Search sessions' }), { target: { value: 'no match' } });
  expect(within(attention).getByText(entry.title!)).toBeInTheDocument();
});

it('removes acknowledged items from attention without adding old history to the recent list', () => {
  const page = renderSidebar('recent');
  expect(screen.getByText(entry.title!)).toBeInTheDocument();
  snapshot([]);
  page.refresh();
  expect(screen.queryByRole('region', { name: 'Needs attention' })).not.toBeInTheDocument();
});

it('navigates to the explicit remote owner with keyboard activation', () => {
  snapshot([{ ...entry, platform: 'r-box:opencode' }]);
  renderSidebar('recent');
  const row = within(screen.getByRole('region', { name: 'Needs attention' })).getByRole('button');
  fireEvent.keyDown(row, { key: 'Enter' });
  expect(screen.getByLabelText('Location')).toHaveTextContent('/session/old-factory?platform=r-box%3Aopencode');
});

it('selects only the active owner when attention contains same-id sessions on different machines', () => {
  snapshot([entry, { ...entry, platform: 'r-box:opencode', title: 'Remote verification' }]);
  renderSidebar('recent', [], '/session/old-factory?platform=r-box%3Aopencode', entry.id);
  const attention = within(screen.getByRole('region', { name: 'Needs attention' }));
  expect(attention.getByRole('button', { name: /Old Factory verification/ })).toHaveAttribute('aria-selected', 'false');
  expect(attention.getByRole('button', { name: /Remote verification/ })).toHaveAttribute('aria-selected', 'true');
});

it.each(['recent', 'projects'] as const)('deduplicates attention against %s history without hiding another owner', (view) => {
  const row = { ...seedView(entry.id).session!, ...entry, status: 'interrupted' as const, factoryAttemptId: 'attempt' };
  snapshot([entry, entry]);
  renderSidebar(view, [row, { ...row, platform: 'r-other:opencode', title: 'Other owner', factoryAttemptId: undefined, pinned: true }]);
  expect(screen.getAllByText(entry.title!)).toHaveLength(1);
  expect(screen.getByText('Other owner')).toBeInTheDocument();
});

it('does not repeat an attention item already visible in normal history', () => {
  const row = { ...seedView(entry.id).session!, ...entry, status: 'interrupted' as const };
  const page = renderSidebar('recent', [row]);
  expect(screen.getAllByText(entry.title!)).toHaveLength(1);
  expect(screen.queryByRole('region', { name: 'Needs attention' })).not.toBeInTheDocument();
  fireEvent.change(screen.getByRole('searchbox', { name: 'Search sessions' }), { target: { value: 'no match' } });
  expect(within(screen.getByRole('region', { name: 'Needs attention' })).getByText(entry.title!)).toBeInTheDocument();
  page.refresh();
  expect(screen.getAllByText(entry.title!)).toHaveLength(1);
});

it('reveals the other owner when normal nesting drops one of two unpinned same-id rows', () => {
  const remote = { ...entry, platform: 'r-box:opencode', title: 'Remote verification' };
  snapshot([entry, remote]);
  const row = { ...seedView(entry.id).session!, ...entry, status: 'interrupted' as const };
  renderSidebar('recent', [row, { ...row, ...remote, status: 'interrupted' }]);
  expect(screen.getAllByText(entry.title!)).toHaveLength(1);
  expect(screen.getAllByText(remote.title)).toHaveLength(1);
  const attentionRows = within(screen.getByRole('region', { name: 'Needs attention' })).getAllByRole('button');
  expect(attentionRows).toHaveLength(1);
  fireEvent.click(attentionRows[0]);
  expect(screen.getByLabelText('Location')).toHaveTextContent('/session/old-factory?platform=r-box%3Aopencode');
});

it('keeps hidden Factory descendants hidden when their parent moves to attention', () => {
  const parent = { ...seedView(entry.id).session!, ...entry, status: 'interrupted' as const, factoryAttemptId: 'attempt' };
  const child = { ...seedView('child').session!, title: 'Hidden Factory child', parentId: entry.id };
  renderSidebar('recent', [parent, child]);
  expect(screen.getByText(entry.title!)).toBeInTheDocument();
  expect(screen.queryByText('Hidden Factory child')).not.toBeInTheDocument();
});

it('returns acknowledged sessions to ordinary history filters', () => {
  const row = { ...seedView(entry.id).session!, ...entry, status: 'interrupted' as const, factoryAttemptId: 'attempt' };
  const page = renderSidebar('recent', [row]);
  snapshot([]);
  page.refresh();
  expect(screen.queryByText(entry.title!)).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Filter sessions' }));
  fireEvent.click(screen.getByRole('checkbox', { name: 'Show factory' }));
  expect(screen.getByText(entry.title!)).toBeInTheDocument();
});

it('renders missing metadata safely and supports a legacy ownerless entry', () => {
  snapshot([{ id: 'legacy', status: 'waiting', seen: true, pendingQuestion: true }]);
  renderSidebar('recent');
  fireEvent.click(screen.getByText('Untitled'));
  expect(screen.getByLabelText('Location').textContent).toBe('/session/legacy');
});

it('leaves the normal list intact while notification data is loading', () => {
  vi.mocked(useNotifySnapshot).mockReturnValue(undefined);
  renderSidebar('recent', [{ ...seedView('normal').session!, title: 'Normal history' }]);
  expect(screen.queryByRole('region', { name: 'Needs attention' })).not.toBeInTheDocument();
  expect(screen.getByText('Normal history')).toBeInTheDocument();
});
