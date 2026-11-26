// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { queryClient } from '../lib/queryClient';
import { api, type Session } from '../lib/api';
import { ProjectDetail } from './ProjectDetail';

vi.mock('../lib/api', () => ({ api: {
  projects: vi.fn().mockResolvedValue([{ directory: '/repo', projectKey: 'repo' }]),
  sessions: vi.fn(),
} }));
vi.mock('../lib/useIsPhone', () => ({ useIsPhone: () => false }));
vi.mock('../lib/useCapabilities', () => ({ useMultiHost: () => false }));

const session = (over: Partial<Session> = {}) => ({
  id: 'session', platform: 'opencode', title: 'Ready session', directory: '/repo',
  status: 'busy', timeCreated: 1000, timeUpdated: 2000, messageCount: 0, durationMs: 0,
  ...over,
} as Session);

afterEach(() => {
  cleanup();
  queryClient.clear();
  vi.clearAllMocks();
});

function CurrentSearch() {
  return <output aria-label="Current URL query">{useLocation().search}</output>;
}

function renderProject(search = '') {
  render(<QueryClientProvider client={queryClient}>
    <MemoryRouter initialEntries={[`/project/%2Frepo${search}`]}>
      <CurrentSearch />
      <Routes><Route path="/project/:dir" element={<ProjectDetail />} /></Routes>
    </MemoryRouter>
  </QueryClientProvider>);
}

it('shows project sessions without waiting for slow accounting reads', async () => {
  vi.mocked(api.sessions).mockImplementation(params => params?.view === 'sidebar'
    ? Promise.resolve([session()]) : new Promise(() => {}));
  renderProject();
  expect(await screen.findByRole('link', { name: /Ready session/ })).toBeInTheDocument();
  expect(screen.queryByLabelText('Loading sessions')).not.toBeInTheDocument();
  expect(screen.getByText('Loading activity…')).toBeInTheDocument();
  fireEvent.change(screen.getByRole('searchbox', { name: 'Search sessions' }), { target: { value: 'missing' } });
  expect(screen.getByRole('heading', { name: 'No matching sessions' })).toBeInTheDocument();
});

it('clears session filters without losing the explicit owner or other URL context', async () => {
  vi.mocked(api.sessions).mockResolvedValue([session()]);
  renderProject('?q=missing&t=0&a=1&remoteId=local&context=keep');
  await screen.findByRole('heading', { name: 'No matching sessions' });
  fireEvent.click(screen.getByRole('button', { name: 'Clear filters' }));
  expect(screen.getByLabelText('Current URL query')).toHaveTextContent(/^\?remoteId=local&context=keep$/);
  expect(await screen.findByRole('link', { name: /Ready session/ })).toBeInTheDocument();
});

it('offers retry instead of an empty result when session inventory is initially unavailable', async () => {
  vi.mocked(api.sessions).mockRejectedValue(new Error('HTTP 503: unavailable'));
  renderProject();
  const alert = await screen.findByRole('alert', {}, { timeout: 4000 });
  expect(alert).toHaveTextContent('Sessions are unavailable');
  expect(screen.queryByText('No active sessions found')).not.toBeInTheDocument();
  vi.mocked(api.sessions).mockResolvedValue([session()]);
  fireEvent.click(within(alert).getByRole('button', { name: 'Retry' }));
  expect(await screen.findByRole('link', { name: /Ready session/ })).toBeInTheDocument();
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

it.each([true, false])('keeps the last successful result (populated: %s) after a failed refresh', async populated => {
  vi.mocked(api.sessions).mockResolvedValue(populated ? [session()] : []);
  renderProject();
  const result = () => populated
    ? screen.findByRole('link', { name: /Ready session/ })
    : screen.findByText('No active sessions found');
  await result();
  vi.mocked(api.sessions).mockRejectedValue(new Error('HTTP 503: unavailable'));
  await act(() => queryClient.invalidateQueries({ queryKey: ['sessions', { limit: 0 }] }));
  const warning = (await screen.findByText('Could not refresh sessions. Showing the last loaded results.')).closest<HTMLElement>('[role="status"]')!;
  expect(await result()).toBeInTheDocument();
  vi.mocked(api.sessions).mockResolvedValue([session({ title: 'Fresh session' })]);
  fireEvent.click(within(warning).getByRole('button', { name: 'Retry' }));
  expect(await screen.findByRole('link', { name: /Fresh session/ })).toBeInTheDocument();
  expect(warning).not.toBeInTheDocument();
});

it('fills in totals without replacing compact status, titles or archive flags', async () => {
  let finish!: (rows: Session[]) => void;
  vi.mocked(api.sessions).mockImplementation(params => params?.view === 'sidebar'
    ? Promise.resolve([session()]) : new Promise(resolve => { finish = resolve; }));
  renderProject();
  await screen.findByRole('link', { name: /Ready session/ });
  await act(async () => finish([session({ title: 'Old title', status: 'done', archived: true, messageCount: 42, durationMs: 60000 })]));
  expect(await screen.findByTitle('42 prompts')).toHaveTextContent('42');
  expect(screen.getByRole('link', { name: /Ready session/ })).toBeInTheDocument();
  expect(screen.queryByText('Old title')).not.toBeInTheDocument();
  expect(screen.getByLabelText('Session status: Busy')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Show archived' }));
  expect(screen.getByRole('link', { name: /Ready session/ })).toBeInTheDocument();
});

it('does not show outdated totals or borrow totals from another owner with the same session ID', async () => {
  vi.mocked(api.sessions).mockImplementation(params => Promise.resolve(params?.view === 'sidebar'
    ? [session(), session({ id: 'second' }), session({ id: 'third' })]
    : [session({ timeUpdated: 1000, messageCount: 10 }), session({ id: 'second', platform: 'remote:opencode', messageCount: 20 }),
      session({ id: 'third', messageCount: 30 })]));
  renderProject();
  await screen.findByTitle('30 prompts');
  for (const id of ['session', 'second']) {
    const row = screen.getByText(id).closest('tr')!;
    expect(within(row).getByText('Loading activity…')).toBeInTheDocument();
  }
  expect(screen.queryByTitle(/10 prompts|20 prompts/)).not.toBeInTheDocument();
});

it('shows an empty project without waiting for accounting', async () => {
  vi.mocked(api.sessions).mockImplementation(params => params?.view === 'sidebar'
    ? Promise.resolve([]) : new Promise(() => {}));
  renderProject();
  expect(await screen.findByText('No active sessions found')).toBeInTheDocument();
});

it('keeps project sessions visible and offers Retry when activity totals fail', async () => {
  vi.mocked(api.sessions).mockImplementation(params => params?.view === 'sidebar'
    ? Promise.resolve([session()]) : Promise.reject(new Error('accounting unavailable')));
  renderProject();
  expect(await screen.findByRole('link', { name: /Ready session/ })).toBeInTheDocument();
  const alert = await screen.findByRole('alert', {}, { timeout: 9000 });
  expect(alert).toHaveTextContent('Could not load session activity: accounting unavailable');
  expect(within(alert).getByRole('button', { name: 'Retry' })).toBeInTheDocument();
}, 15000);
