// @vitest-environment jsdom
import { act, cleanup, render, screen, within } from '@testing-library/react';
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

function renderSessions() {
  render(<QueryClientProvider client={queryClient}><MemoryRouter initialEntries={['/sessions']}>
    <Routes><Route element={<DashboardLayout />}><Route path="/sessions" element={<SessionsTab />} /></Route></Routes>
  </MemoryRouter></QueryClientProvider>);
}

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
  expect(await within(tr).findByText(/14 prompts/)).toHaveTextContent('14 prompts · 2h 37m');
});
