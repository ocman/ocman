// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
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

function renderProject() {
  render(<QueryClientProvider client={queryClient}>
    <MemoryRouter initialEntries={['/project/%2Frepo']}>
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
  expect(screen.getByText('No sessions found')).toBeInTheDocument();
});

it('fills in totals without replacing compact status, titles or archive flags', async () => {
  let finish!: (rows: Session[]) => void;
  vi.mocked(api.sessions).mockImplementation(params => params?.view === 'sidebar'
    ? Promise.resolve([session()]) : new Promise(resolve => { finish = resolve; }));
  renderProject();
  await screen.findByRole('link', { name: /Ready session/ });
  await act(async () => finish([session({ title: 'Old title', status: 'done', archived: true, messageCount: 42, durationMs: 60000 })]));
  expect(await screen.findByText(/42 msgs/)).toHaveTextContent('42 msgs · 1m');
  expect(screen.getByRole('link', { name: /Ready session/ })).toBeInTheDocument();
  expect(screen.queryByText('Old title')).not.toBeInTheDocument();
  expect(screen.getByLabelText('Session status: Busy')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Exclude archived' }));
  expect(screen.getByRole('link', { name: /Ready session/ })).toBeInTheDocument();
});

it('does not show outdated totals or borrow totals from another owner with the same session ID', async () => {
  vi.mocked(api.sessions).mockImplementation(params => Promise.resolve(params?.view === 'sidebar'
    ? [session(), session({ id: 'second' }), session({ id: 'third' })]
    : [session({ timeUpdated: 1000, messageCount: 10 }), session({ id: 'second', platform: 'remote:opencode', messageCount: 20 }),
      session({ id: 'third', messageCount: 30 })]));
  renderProject();
  await screen.findByText(/30 msgs/);
  for (const id of ['session', 'second']) {
    const row = screen.getByText(id).closest('tr')!;
    expect(within(row).getByText('Loading activity…')).toBeInTheDocument();
  }
  expect(screen.queryByText(/10 msgs|20 msgs/)).not.toBeInTheDocument();
});

it('shows an empty project without waiting for accounting', async () => {
  vi.mocked(api.sessions).mockImplementation(params => params?.view === 'sidebar'
    ? Promise.resolve([]) : new Promise(() => {}));
  renderProject();
  expect(await screen.findByText('No sessions found')).toBeInTheDocument();
});
