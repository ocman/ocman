// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterEach, expect, it, vi } from 'vitest';
import { api, type Session } from '../../lib/api';
import { queryClient } from '../../lib/queryClient';
import { DashboardLayout } from '../Dashboard';
import { SessionsTab } from './SessionsTab';

vi.mock('../../lib/api', async () => {
  const real = await vi.importActual<typeof import('../../lib/api')>('../../lib/api');
  return { ...real, api: { ...real.api, projects: vi.fn().mockResolvedValue([]), sessions: vi.fn(), resolveProjectIdentities: vi.fn().mockResolvedValue([]) } };
});
vi.mock('../../lib/useIsPhone', () => ({ useIsPhone: () => false }));
vi.mock('../../lib/headerContext', () => ({ usePageTitle: vi.fn() }));

const row = (over: Partial<Session> = {}) => ({
  id: 'ses_a', platform: 'claudecode', title: 'Diagnose CPU', directory: '/repo',
  status: 'done', seen: true, timeCreated: 1000, timeUpdated: 2000, messageCount: 0, durationMs: 0,
  ...over,
} as Session);

afterEach(() => { cleanup(); queryClient.clear(); vi.clearAllMocks(); });

function renderSessions(url = '/sessions') {
  render(<QueryClientProvider client={queryClient}><MemoryRouter initialEntries={[url]}>
    <Routes><Route element={<DashboardLayout />}><Route path="/sessions" element={<SessionsTab />} /></Route></Routes>
  </MemoryRouter></QueryClientProvider>);
}

it('distinguishes an empty recent window from first use', async () => {
  vi.mocked(api.sessions).mockImplementation(async params => params?.sinceHours ? [] : [row()]);
  renderSessions('/sessions?t=12');
  const empty = await screen.findByRole('region', { name: 'No matching sessions' });
  fireEvent.click(within(empty).getByRole('button', { name: 'Clear filters' }));
  expect(await screen.findByRole('link', { name: /Diagnose CPU/ })).toBeInTheDocument();
});

it('retries a failed all-time check before claiming first use', async () => {
  let fail = true;
  vi.mocked(api.sessions).mockImplementation(async params => {
    if (params?.limit === 1 && fail) throw new Error('HTTP 400: Session history unavailable');
    return [];
  });
  renderSessions();
  const error = await screen.findByRole('alert', { name: 'Could not load sessions' });
  expect(error).toHaveTextContent('Session history unavailable');
  expect(screen.queryByRole('region', { name: 'No sessions yet' })).not.toBeInTheDocument();
  fail = false;
  fireEvent.click(within(error).getByRole('button', { name: 'Retry' }));
  expect(await screen.findByRole('region', { name: 'No sessions yet' })).toBeInTheDocument();
});

it('keeps a successfully empty list on screen when a refresh fails', async () => {
  let fail = false;
  vi.mocked(api.sessions).mockImplementation(async () => {
    if (fail) throw new Error('HTTP 400: Refresh failed');
    return [];
  });
  renderSessions('/sessions?t=0');
  const empty = await screen.findByRole('region', { name: 'No sessions yet' });
  fail = true;
  await act(async () => { await queryClient.invalidateQueries({ queryKey: ['sessions'] }); });
  const warning = await screen.findByRole('status');
  expect(warning).toHaveTextContent('Refresh failed');
  expect(empty).toBeVisible();
  fail = false;
  fireEvent.click(within(warning).getByRole('button', { name: 'Retry' }));
  await waitFor(() => expect(screen.queryByText(/Refresh failed/)).not.toBeInTheDocument());
  expect(empty).toBeVisible();
});

it('fills the global list activity from accounting instead of the compact zero placeholders', async () => {
  let finish!: (rows: Session[]) => void;
  vi.mocked(api.sessions).mockImplementation(params => params?.view === 'sidebar'
    ? Promise.resolve([row()]) : new Promise(resolve => { finish = resolve; }));
  renderSessions();
  const link = await screen.findByRole('link', { name: /Diagnose CPU/ });
  const tr = link.closest('tr')!;
  // Compact rows carry no accounting; never present them as real zeros.
  expect(within(tr).queryByText(/^0 /)).not.toBeInTheDocument();
  expect(within(tr).queryByText(/< 1s/)).not.toBeInTheDocument();
  await act(async () => finish([row({ messageCount: 14, durationMs: 9_420_000 })]));
  expect(await within(tr).findByTitle('14 prompts')).toHaveTextContent('14');
  expect(within(tr).getByText('2h 37m')).toBeInTheDocument();
});

it('reads totals for older unarchived rows that the compact list keeps outside the time window', async () => {
  const old = Date.now() - 30 * 24 * 3_600_000;
  vi.mocked(api.sessions).mockImplementation(params => params?.view === 'sidebar'
    ? Promise.resolve([row({ timeCreated: old, timeUpdated: old })])
    // Like the backend, accounting reads drop rows updated before `since`.
    : Promise.resolve([row({ timeCreated: old, timeUpdated: old, messageCount: 5, durationMs: 4000 })]
      .filter(r => !params?.since || r.timeUpdated >= params.since)));
  renderSessions();
  const tr = (await screen.findByRole('link', { name: /Diagnose CPU/ })).closest('tr')!;
  expect(await within(tr).findByTitle('5 prompts')).toHaveTextContent('5');
});

it('keeps the list and offers Retry when activity totals fail to load', async () => {
  let fail = true;
  vi.mocked(api.sessions).mockImplementation(params => params?.view === 'sidebar'
    ? Promise.resolve([row()])
    : fail ? Promise.reject(new Error('accounting unavailable')) : Promise.resolve([row({ messageCount: 3 })]));
  renderSessions();
  expect(await screen.findByRole('link', { name: /Diagnose CPU/ })).toBeInTheDocument();
  const alert = await screen.findByRole('alert', {}, { timeout: 8000 });
  expect(alert).toHaveTextContent('Could not load session activity: accounting unavailable');
  fail = false;
  fireEvent.click(within(alert).getByRole('button', { name: 'Retry' }));
  expect(await screen.findByTitle('3 prompts')).toBeInTheDocument();
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
}, 15000);
