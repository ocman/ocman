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

beforeEach(() => vi.mocked(useIsPhone).mockReturnValue(true));

describe('session status filters', () => {
  it('shows all sessions until a phone filter is selected', () => {
    mount();
    expect(within(screen.getByRole('region', { name: 'Results' })).getAllByRole('paragraph')).toHaveLength(6);
    fireEvent.click(screen.getByRole('radio', { name: 'Running' }));
    expect(screen.getByText('Running work')).toBeInTheDocument();
    expect(screen.getByText('Permission work')).toBeInTheDocument();
    expect(screen.queryByText('Finished work')).not.toBeInTheDocument();
  });

  it('includes pending permissions and questions in Waiting', () => {
    mount();
    fireEvent.click(screen.getByRole('radio', { name: 'Waiting' }));
    for (const title of ['Waiting work', 'Permission work', 'Question work']) expect(screen.getByText(title)).toBeInTheDocument();
    expect(screen.queryByText('Running work')).not.toBeInTheDocument();
    expect(screen.queryByText('Finished work')).not.toBeInTheDocument();
    fireEvent.click(within(screen.getByRole('radiogroup', { name: 'Session status' })).getByRole('radio', { name: 'All' }));
    expect(screen.getByText('Finished work')).toBeInTheDocument();
  });

  it('never lets the phone status filter remove desktop rows after resizing', () => {
    const { rerender } = mount();
    fireEvent.click(screen.getByRole('radio', { name: 'Running' }));
    expect(screen.queryByText('Finished work')).not.toBeInTheDocument();
    vi.mocked(useIsPhone).mockReturnValue(false);
    rerender(<MemoryRouter><SessionsTab /></MemoryRouter>);
    expect(screen.getByText('Finished work')).toBeInTheDocument();
    expect(screen.getByText('Waiting work')).toBeInTheDocument();
  });

  it('combines search and directory scope with the phone status filter', () => {
    mount({ dirScope: '/repo' });
    expect(screen.queryByText('Other project')).not.toBeInTheDocument();
    fireEvent.change(screen.getByRole('textbox', { name: 'Search sessions' }), { target: { value: 'Running' } });
    expect(screen.getByText('Running work')).toBeInTheDocument();
    expect(screen.queryByText('Finished work')).not.toBeInTheDocument();
  });

  it('retains loading, archive, retry and time-range controls', () => {
    const { value } = mount({ sessions: [], sessionsLoading: true, sessionsError: 'Unable to load sessions' });
    expect(screen.getByRole('region', { name: 'Results' })).toHaveAttribute('data-loading', 'true');
    expect(screen.getByRole('alert')).toHaveTextContent('Unable to load sessions');
    expect(screen.getByRole('button', { name: 'Retry' })).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: 'Include archived' }));
    expect(value.setShowArchived).toHaveBeenCalledWith(true);
    fireEvent.click(screen.getByRole('radio', { name: '7d' }));
    expect(value.setTimeRange).toHaveBeenCalledWith(168);
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
