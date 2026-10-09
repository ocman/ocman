// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { expect, it, vi } from 'vitest';
import { api, type FactoryRecoveryGate } from '../lib/api';
import { FactoryRecoveryActions } from './FactoryRecoveryActions';

vi.mock('../lib/api', () => ({ api: { resolveFactoryRecoveryGate: vi.fn() } }));

const open: FactoryRecoveryGate = { issueId: 'ship.3', epicId: 'ship', attemptId: 'attempt', workId: 'ship.1', question: 'Which API?', reason: 'Both supported', choices: ['A', 'B'], resolution: 'open' };

it('lets a gate change seen while the resume request is pending win once it succeeds', async () => {
  let finish!: () => void;
  vi.mocked(api.resolveFactoryRecoveryGate).mockImplementation(() => new Promise((resolve) => { finish = () => resolve({ ...open, response: 'A' }); }));
  const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
  const view = (gate: FactoryRecoveryGate) => <QueryClientProvider client={client}><MemoryRouter><FactoryRecoveryActions gate={gate} /></MemoryRouter></QueryClientProvider>;
  const { rerender } = render(view(open));
  fireEvent.click(screen.getByRole('button', { name: 'Resume work' }));
  await screen.findByText('Saving recovery decision…');
  // The queued resume was delivered, failed, and polling saw resume_pending first.
  rerender(view({ ...open, response: 'A', resolution: 'resume_pending' }));
  await act(async () => finish());
  await waitFor(() => expect(screen.getByRole('button', { name: 'Resume work' })).toBeEnabled());
  expect(screen.queryByText(/Resume queued/)).not.toBeInTheDocument();
});
