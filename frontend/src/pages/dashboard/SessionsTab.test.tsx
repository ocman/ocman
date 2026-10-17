// @vitest-environment jsdom
import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Session } from '../../lib/api';
import { useIsPhone } from '../../lib/useIsPhone';
import { useUiStore } from '../../lib/uiStore';
import type { DashboardCtx } from './context';
import { SessionsTab } from './SessionsTab';

const dashboard = vi.fn();
vi.mock('./context', () => ({ useDashboard: () => dashboard() }));
vi.mock('../../lib/useIsPhone', () => ({ useIsPhone: vi.fn() }));
vi.mock('../../lib/headerContext', () => ({ usePageTitle: vi.fn() }));
vi.mock('../../components/SessionTable', () => ({
  SessionTable: ({ sessions, loading, includeArchived }: { sessions: Session[]; loading: boolean; includeArchived: boolean }) =>
    <section aria-label="Results" data-loading={loading} data-archived={includeArchived}>{sessions.map(s => <p key={s.id}>{s.title}</p>)}</section>,
}));
vi.mock('./DashboardToolbar', () => ({
  DashboardToolbar: ({ search, setSearch, onAction }: { search: string; setSearch: (s: string) => void; onAction: () => void }) =>
    <><input aria-label="Search sessions" value={search} onChange={e => setSearch(e.target.value)} /><button onClick={onAction}>New session</button></>,
}));

const sessions = [
  { id: 'busy', title: 'Running work', status: 'busy', directory: '/repo' },
  { id: 'waiting', title: 'Waiting work', status: 'waiting', directory: '/repo' },
  { id: 'permission', title: 'Permission work', status: 'busy', pendingPermission: true, directory: '/repo' },
  { id: 'question', title: 'Question work', status: 'done', pendingQuestion: true, directory: '/repo' },
  { id: 'done', title: 'Finished work', status: 'done', directory: '/repo' },
  { id: 'other', title: 'Other project', status: 'busy', directory: '/other' },
] as unknown as Session[];

const ctx = (overrides: Partial<DashboardCtx> = {}) => ({
  sessions, projects: [], sessionsLoading: false, sessionsError: null,
  loadSessions: vi.fn(), timeRange: 24, setTimeRange: vi.fn(),
  showArchived: false, setShowArchived: vi.fn(), dirScope: '', setDirScope: vi.fn(), ...overrides,
});

function mount(overrides: Partial<DashboardCtx> = {}) {
  const value = ctx(overrides);
  dashboard.mockReturnValue(value);
  const view = render(<MemoryRouter><SessionsTab /></MemoryRouter>);
  return { ...view, value };
}

beforeEach(() => { localStorage.clear(); vi.mocked(useIsPhone).mockReturnValue(true); });

describe('session status filters', () => {
  it('hides routine and Factory sessions by default just like the desktop sidebar', () => {
    mount({ sessions: [
      ...sessions,
      { ...sessions[0], id: 'routine', title: 'Routine session', routineId: 'routine-1' },
      { ...sessions[0], id: 'factory', title: 'Factory session', factoryAttemptId: 'attempt-1' },
    ] });
    expect(screen.queryByText('Routine session')).not.toBeInTheDocument();
    expect(screen.queryByText('Factory session')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Filter sessions' }));
    expect(screen.getByRole('checkbox', { name: 'Show routines' })).not.toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'Show factory' })).not.toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'Show children' })).toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'Always show prompts' })).toBeChecked();
  });
  it('keeps phone filters collapsed until requested', () => {
    mount();
    expect(screen.queryByRole('radiogroup', { name: 'Time range' })).not.toBeInTheDocument();
    expect(screen.queryByRole('radiogroup', { name: 'Session status' })).not.toBeInTheDocument();
    expect(screen.getByRole('searchbox', { name: 'Search sessions' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Choose project' })).toBeInTheDocument();
    const filters = screen.getByRole('button', { name: 'Filter sessions' });
    expect(filters).toHaveAttribute('aria-expanded', 'false');
    fireEvent.click(filters);
    expect(screen.getByRole('dialog', { name: 'Session filters' })).toBeInTheDocument();
    expect(screen.getByRole('checkbox', { name: 'Show routines' })).toBeInTheDocument();
    fireEvent.click(filters);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('keeps selected filters applied when the popover closes', () => {
    mount({ sessions: [...sessions, { ...sessions[0], id: 'routine', title: 'Routine session', routineId: 'r1' }] });
    const toggle = screen.getByRole('button', { name: 'Filter sessions' });
    fireEvent.click(toggle);
    fireEvent.click(screen.getByRole('checkbox', { name: 'Show routines' }));
    fireEvent.click(toggle);
    expect(screen.getByText('Routine session')).toBeInTheDocument();
    expect(toggle).toHaveClass('oc-button--accent');
    fireEvent.click(toggle);
    expect(screen.getByRole('checkbox', { name: 'Show routines' })).toBeChecked();
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(toggle).toHaveFocus();
  });

  it('chooses a project from its icon and returns focus to the toolbar', () => {
    const { value } = mount({ projects: [{ directory: '/repo' }] as DashboardCtx['projects'] });
    const project = screen.getByRole('button', { name: 'Choose project' });
    fireEvent.click(project);
    fireEvent.click(screen.getByRole('combobox', { name: 'Project scope' }));
    fireEvent.click(screen.getByRole('option', { name: '/repo' }));
    expect(value.setDirScope).toHaveBeenCalledWith('/repo');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(project).toHaveFocus();
  });

  it('highlights a selected scope and archive filter and switches between popovers', () => {
    mount({ dirScope: '/repo', showArchived: true });
    const project = screen.getByRole('button', { name: 'Choose project' });
    const filters = screen.getByRole('button', { name: 'Filter sessions' });
    expect(project).toHaveClass('oc-button--accent');
    expect(filters).toHaveClass('oc-button--accent');
    fireEvent.click(project);
    expect(screen.getByRole('dialog', { name: 'Choose project' })).toBeInTheDocument();
    fireEvent.click(filters);
    expect(screen.getAllByRole('dialog')).toHaveLength(1);
    expect(screen.getByRole('dialog', { name: 'Session filters' })).toBeInTheDocument();
  });

  it('shows child sessions until Show children is disabled', () => {
    mount({ sessions: sessions.map(session => session.id === 'busy' ? { ...session, parentId: 'parent' } : session) });
    expect(within(screen.getByRole('region', { name: 'Results' })).getAllByRole('paragraph')).toHaveLength(6);
    fireEvent.click(screen.getByRole('button', { name: 'Filter sessions' }));
    fireEvent.click(screen.getByRole('checkbox', { name: 'Show children' }));
    expect(screen.queryByText('Running work')).not.toBeInTheDocument();
    expect(screen.getByText('Permission work')).toBeInTheDocument();
    expect(screen.getByText('Finished work')).toBeInTheDocument();
  });

  it('keeps pending permissions and questions visible through search until disabled', () => {
    mount();
    fireEvent.click(screen.getByRole('button', { name: 'Filter sessions' }));
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search sessions' }), { target: { value: 'no match' } });
    for (const title of ['Permission work', 'Question work']) expect(screen.getByText(title)).toBeInTheDocument();
    expect(screen.queryByText('Running work')).not.toBeInTheDocument();
    expect(screen.queryByText('Finished work')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Always show prompts' }));
    expect(screen.queryByText('Permission work')).not.toBeInTheDocument();
    expect(screen.queryByText('Question work')).not.toBeInTheDocument();
  });

  it('keeps the desktop dashboard filters unchanged after resizing', () => {
    const { rerender } = mount({ sessions: sessions.map(session => session.id === 'busy' ? { ...session, parentId: 'parent' } : session) });
    fireEvent.click(screen.getByRole('button', { name: 'Filter sessions' }));
    fireEvent.click(screen.getByRole('checkbox', { name: 'Show children' }));
    expect(screen.queryByText('Running work')).not.toBeInTheDocument();
    vi.mocked(useIsPhone).mockReturnValue(false);
    rerender(<MemoryRouter><SessionsTab /></MemoryRouter>);
    expect(screen.getByText('Running work')).toBeInTheDocument();
    expect(screen.getByText('Waiting work')).toBeInTheDocument();
  });

  it('combines search and directory scope with the shared filters', () => {
    mount({ dirScope: '/repo' });
    expect(screen.queryByText('Other project')).not.toBeInTheDocument();
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search sessions' }), { target: { value: 'Running' } });
    expect(screen.getByText('Running work')).toBeInTheDocument();
    expect(screen.queryByText('Finished work')).not.toBeInTheDocument();
  });

  it('retains mobile loading, archive and retry controls', () => {
    const { value } = mount({ sessions: [], sessionsLoading: true, sessionsError: 'Unable to load sessions' });
    expect(screen.getByRole('region', { name: 'Results' })).toHaveAttribute('data-loading', 'true');
    expect(screen.getByRole('alert')).toHaveTextContent('Unable to load sessions');
    expect(screen.getByRole('button', { name: 'Retry' })).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: 'Filter sessions' }));
    fireEvent.click(screen.getByRole('checkbox', { name: 'Show archived' }));
    expect(value.setShowArchived).toHaveBeenCalledWith(true);
    expect(localStorage.getItem('ocman:sidebar-filter:archived')).toBe('true');
  });

  it('retains desktop time-range controls', () => {
    vi.mocked(useIsPhone).mockReturnValue(false);
    const { value } = mount();
    fireEvent.click(screen.getByRole('radio', { name: '7d' }));
    expect(value.setTimeRange).toHaveBeenCalledWith(168);
  });

  it('honours desktop saved preferences on mobile', () => {
    localStorage.setItem('ocman:sidebar-filter:routines', 'true');
    localStorage.setItem('ocman:sidebar-filter:children', 'false');
    localStorage.setItem('ocman:sidebar-filter:archived', 'true');
    mount({ sessions: [...sessions, { ...sessions[0], id: 'routine', title: 'Routine session', routineId: 'r1' }] });
    expect(screen.getByText('Routine session')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Filter sessions' }));
    expect(screen.getByRole('checkbox', { name: 'Show routines' })).toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'Show children' })).not.toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'Show archived' })).toBeChecked();
  });

  it('retries failures and opens the project session picker', () => {
    const open = vi.fn();
    const previous = useUiStore.getState().openProjectSessionPalette;
    useUiStore.setState({ openProjectSessionPalette: open });
    const { value } = mount({ sessionsError: 'Try again' });
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(value.loadSessions).toHaveBeenCalled();
    act(() => fireEvent.click(screen.getByRole('button', { name: 'New session' })));
    expect(open).toHaveBeenCalled();
    useUiStore.setState({ openProjectSessionPalette: previous });
  });
});
