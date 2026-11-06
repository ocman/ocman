// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import type { NotifyEntry, Session } from '../../lib/api';
import { useNotifySnapshot } from '../../lib/useNotifyData';
import { useUiStore } from '../../lib/uiStore';
import { SessionSidebar, type SessionSidebarProps } from './SessionSidebar';
import { seedView } from '../../test/oceFixtures';
import { useBetaFeatures } from '../../lib/betaFeatures';

beforeEach(() => useBetaFeatures.setState({ factory: true }));

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

function renderSidebar(view: 'recent' | 'projects', rows: Session[] = [], route = '/', activeId = 'other', collapsedProjectSet = new Set(['/old-repo'])) {
  const props: SessionSidebarProps = {
    activeId, sidebarWidth: 300, sidebarView: view, setSidebarView: vi.fn(),
    showArchivedRecent: false, setShowArchivedRecent: vi.fn(), loadingRecentSessions: false,
    recentSessions: rows, sidebarProjectGroups: rows.length ? [{
      directory: '/old-repo', sessions: rows, lastUpdated: 1, aggregate: { kind: 'none' },
    }] : [],
    onReorderProjects: vi.fn(), archivingSessionIds: new Set(), collapsedProjectSet,
    toggleCollapsedProject: vi.fn(), siblingGitInfos: {}, activeDisplayStatus: 'busy', debugMode: false,
    pendingTmuxSession: null, pickerPos: null, pickerRef: { current: null },
    tmux: { available: false, isLocal: true, sessions: [], clients: [], switchSession: vi.fn(), findSession: vi.fn(), launchOpencode: vi.fn() },
    onNavigateToSession: vi.fn(), onArchiveSession: vi.fn(), onPinSession: vi.fn(), onNewSession: vi.fn(),
    onClientSelect: vi.fn(), onNewSessionInDirectory: vi.fn(), onArchiveProject: vi.fn(),
  };
  const tree = () => <MemoryRouter initialEntries={[route]}><SessionSidebar {...props} /><Location /></MemoryRouter>;
  const page = render(tree());
  return { ...page, refresh: () => page.rerender(tree()), navigateToSession: props.onNavigateToSession };
}

beforeEach(() => {
  localStorage.clear();
  useUiStore.setState({ sidebarRecentHours: 72 });
  snapshot([entry]);
});

it('reveals a badge-counted Factory interruption absent from grouped history', () => {
  renderSidebar('projects');
  const attention = screen.getByRole('region', { name: 'Needs attention' });
  expect(within(attention).getByText(entry.title!)).toBeInTheDocument();
  expect(within(attention).getByLabelText(/Session status: Interrupted/)).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Filter sessions' }));
  expect(screen.queryByRole('checkbox', { name: 'Show factory' })).not.toBeInTheDocument();
  fireEvent.change(screen.getByRole('searchbox', { name: 'Search sessions' }), { target: { value: 'no match' } });
  expect(within(attention).getByText(entry.title!)).toBeInTheDocument();
});

it('removes acknowledged items from attention without adding old history to the recent list', () => {
  const page = renderSidebar('projects');
  expect(screen.getByText(entry.title!)).toBeInTheDocument();
  snapshot([]);
  page.refresh();
  expect(screen.queryByRole('region', { name: 'Needs attention' })).not.toBeInTheDocument();
});

it('navigates to the explicit remote owner with keyboard activation', () => {
  snapshot([{ ...entry, platform: 'r-box:opencode' }]);
  renderSidebar('projects');
  const row = within(screen.getByRole('region', { name: 'Needs attention' })).getByRole('button');
  fireEvent.keyDown(row, { key: 'Enter' });
  expect(screen.getByLabelText('Location')).toHaveTextContent('/session/old-factory?platform=r-box%3Aopencode');
});

it('selects only the active owner when attention contains same-id sessions on different machines', () => {
  snapshot([entry, { ...entry, platform: 'r-box:opencode', title: 'Remote verification' }]);
  renderSidebar('projects', [], '/session/old-factory?platform=r-box%3Aopencode', entry.id);
  const attention = within(screen.getByRole('region', { name: 'Needs attention' }));
  expect(attention.getByRole('button', { name: /Old Factory verification/ })).toHaveAttribute('aria-selected', 'false');
  expect(attention.getByRole('button', { name: /Remote verification/ })).toHaveAttribute('aria-selected', 'true');
});

it.each(['recent', 'projects'] as const)('deduplicates attention against %s history without hiding another owner', (view) => {
  const row = { ...seedView(entry.id).session!, ...entry, status: 'interrupted' as const };
  snapshot([entry, entry]);
  renderSidebar(view, [row, { ...row, platform: 'r-other:opencode', title: 'Other owner', factoryAttemptId: undefined, pinned: true }]);
  expect(screen.getAllByText(entry.title!)).toHaveLength(1);
  expect(screen.getByText('Other owner')).toBeInTheDocument();
});

it('does not repeat an attention item already visible in normal history', () => {
  const row = { ...seedView(entry.id).session!, ...entry, status: 'interrupted' as const };
  const page = renderSidebar('projects', [row], '/', 'other', new Set());
  expect(screen.getAllByText(entry.title!)).toHaveLength(1);
  expect(screen.queryByRole('region', { name: 'Needs attention' })).not.toBeInTheDocument();
  fireEvent.change(screen.getByRole('searchbox', { name: 'Search sessions' }), { target: { value: 'no match' } });
  expect(within(screen.getByRole('region', { name: 'Needs attention' })).getByText(entry.title!)).toBeInTheDocument();
  page.refresh();
  expect(screen.getAllByText(entry.title!)).toHaveLength(1);
});

it.each(['recent', 'projects'] as const)('keeps same-id rows owner-routed in %s history without an attention fallback', (view) => {
  const remote = { ...entry, platform: 'r-box:opencode', title: 'Remote verification' };
  snapshot([entry, remote]);
  const row = { ...seedView(entry.id).session!, ...entry, status: 'interrupted' as const };
  const page = renderSidebar(view, [row, { ...row, ...remote, status: 'interrupted' }], '/', 'other', new Set());
  expect(screen.getAllByText(entry.title!)).toHaveLength(1);
  expect(screen.getAllByText(remote.title)).toHaveLength(1);
  expect(screen.queryByRole('region', { name: 'Needs attention' })).not.toBeInTheDocument();
  fireEvent.click(screen.getByText(remote.title));
  expect(page.navigateToSession).toHaveBeenCalledWith(remote.id, remote.platform);
});

it('keeps hidden Factory descendants hidden when their parent moves to attention', () => {
  const parent = { ...seedView(entry.id).session!, ...entry, status: 'interrupted' as const, factoryAttemptId: 'attempt' };
  const child = { ...seedView('child').session!, title: 'Hidden Factory child', parentId: entry.id };
  renderSidebar('projects', [parent, child]);
  expect(screen.getByText(entry.title!)).toBeInTheDocument();
  expect(screen.queryByText('Hidden Factory child')).not.toBeInTheDocument();
});

it('hides acknowledged settled Factory sessions outside attention', () => {
  const row = { ...seedView(entry.id).session!, ...entry, status: 'interrupted' as const, factoryAttemptId: 'attempt' };
  const page = renderSidebar('projects', [row]);
  snapshot([]);
  page.refresh();
  expect(screen.queryByText(entry.title!)).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Filter sessions' }));
  expect(screen.queryByRole('checkbox', { name: 'Show factory' })).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /Factory \d/ })).not.toBeInTheDocument();
});

it('renders missing metadata safely and supports a legacy ownerless entry', () => {
  snapshot([{ id: 'legacy', status: 'waiting', seen: true, pendingQuestion: true }]);
  renderSidebar('projects');
  fireEvent.click(screen.getByText('Untitled'));
  expect(screen.getByLabelText('Location').textContent).toBe('/session/legacy');
});

it('leaves the normal list intact while notification data is loading', () => {
  vi.mocked(useNotifySnapshot).mockReturnValue(undefined);
  renderSidebar('recent', [{ ...seedView('normal').session!, title: 'Normal history' }]);
  expect(screen.queryByRole('region', { name: 'Needs attention' })).not.toBeInTheDocument();
  expect(screen.getByText('Normal history')).toBeInTheDocument();
});

it('keeps ordinary unarchived sessions visible after acknowledgement without leaking settled origins', () => {
  const rows: Session[] = [
    { ...seedView('normal').session!, title: 'Normal result' },
    { ...seedView(entry.id).session!, ...entry, status: 'interrupted' as const, factoryAttemptId: 'attempt' },
    { ...seedView('routine').session!, title: 'Routine result', routineId: 'run' },
    { ...seedView('plugin').session!, title: 'Plugin result', origin: 'plugin' },
  ];
  const page = renderSidebar('recent', rows);
  expect(screen.getByText('Normal result')).toBeInTheDocument();
  for (const row of rows.slice(1)) expect(screen.queryByText(row.title)).not.toBeInTheDocument();
  expect(screen.queryByRole('region', { name: 'Needs attention' })).not.toBeInTheDocument();
  snapshot([]);
  page.refresh();
  expect(screen.getByText('Normal result')).toBeInTheDocument();
  for (const row of rows.slice(1)) expect(screen.queryByText(row.title)).not.toBeInTheDocument();
});

it('does not inject stale notification-only rows into the flat list or bypass its search', () => {
  const row = { ...seedView('normal').session!, title: 'Normal history' };
  renderSidebar('recent', [row]);
  expect(screen.queryByText(entry.title!)).not.toBeInTheDocument();
  snapshot([{ ...entry, id: row.id, title: row.title }]);
  fireEvent.change(screen.getByRole('searchbox', { name: 'Search sessions' }), { target: { value: 'no match' } });
  expect(screen.queryByText(row.title)).not.toBeInTheDocument();
  expect(screen.queryByRole('region', { name: 'Needs attention' })).not.toBeInTheDocument();
});

it('keeps archived and snoozed sessions hidden in the flat list', () => {
  renderSidebar('recent', [
    { ...seedView('archived').session!, title: 'Archived result', archived: true },
    { ...seedView('snoozed').session!, title: 'Snoozed result', snoozedUntil: Date.now() + 60_000 },
  ]);
  expect(screen.queryByText('Archived result')).not.toBeInTheDocument();
  expect(screen.queryByText('Snoozed result')).not.toBeInTheDocument();
  expect(screen.queryByRole('region', { name: 'Needs attention' })).not.toBeInTheDocument();
});

it('keeps running origins, archived and snoozed work in drawers rather than the flat list', () => {
  renderSidebar('recent', [
    { ...seedView('routine').session!, title: 'Running routine', status: 'busy', routineId: 'run' },
    { ...seedView('archived').session!, title: 'Archived work', status: 'busy', archived: true },
    { ...seedView('snoozed').session!, title: 'Snoozed work', status: 'busy', snoozedUntil: Date.now() + 60_000 },
  ]);
  expect(screen.queryByText('Running routine')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Routines 1' }));
  expect(within(screen.getByRole('region', { name: 'Routines 1' })).getByText('Running routine')).toBeInTheDocument();
  expect(screen.queryByText('Archived work')).not.toBeInTheDocument();
  expect(screen.queryByText('Snoozed work')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Background 1' }));
  fireEvent.click(screen.getByRole('button', { name: 'Snoozed 1' }));
  expect(screen.getByText('Archived work')).toBeInTheDocument();
  expect(screen.getByText('Snoozed work')).toBeInTheDocument();
});
