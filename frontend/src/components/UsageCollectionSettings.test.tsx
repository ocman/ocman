// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { UsageCollectionSettings } from './UsageCollectionSettings';
import { fetchJSON, postJSON } from '../lib/api.requests';

vi.mock('../lib/api.requests', () => ({ fetchJSON: vi.fn(), postJSON: vi.fn() }));
const view = { enabled: true, relayUrl: 'https://relay.example.com/v1/usage', version: '0.275.0' };
beforeEach(() => vi.resetAllMocks());
function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  return render(<QueryClientProvider client={client}><UsageCollectionSettings /></QueryClientProvider>);
}

it('disables the control until the setting is loaded', () => {
  vi.mocked(fetchJSON).mockReturnValue(new Promise(() => {}));
  mount();
  expect(screen.getByRole('checkbox', { name: 'Send usage reports' })).toBeDisabled();
});

it('documents collection and saves an instance-wide opt-out', async () => {
  vi.mocked(fetchJSON).mockResolvedValue(view);
  vi.mocked(postJSON).mockResolvedValue({ ...view, enabled: false });
  mount();
  const toggle = screen.getByRole('checkbox', { name: 'Send usage reports' });
  await waitFor(() => expect(toggle).toBeChecked());
  expect(screen.getByText(/persistent random installation ID/)).toBeVisible();
  expect(screen.getByText(/source-IP security events/)).toBeVisible();
  expect(screen.getByText('0.275.0')).toBeVisible();
  fireEvent.click(toggle);
  await waitFor(() => expect(postJSON).toHaveBeenCalledWith('/api/settings/usage-collection', { enabled: false }));
  await waitFor(() => expect(toggle).not.toBeChecked());
});

it('keeps the saved state after a failed write', async () => {
  vi.mocked(fetchJSON).mockResolvedValue(view);
  vi.mocked(postJSON).mockRejectedValue(new Error('offline'));
  mount();
  const toggle = screen.getByRole('checkbox', { name: 'Send usage reports' });
  await waitFor(() => expect(toggle).toBeEnabled());
  fireEvent.click(toggle);
  await waitFor(() => expect(postJSON).toHaveBeenCalled());
  await waitFor(() => expect(toggle).toBeEnabled());
  expect(toggle).toBeChecked();
});

it('offers retry for failed loads and explains an unconfigured relay', async () => {
  vi.mocked(fetchJSON).mockRejectedValueOnce(new Error('offline')).mockResolvedValue({ ...view, relayUrl: '' });
  mount();
  fireEvent.click(await screen.findByRole('button', { name: 'Retry' }));
  expect(await screen.findByText('No relay configured. Usage reports are not sent.')).toBeVisible();
  expect(screen.getByRole('checkbox', { name: 'Send usage reports' })).toBeChecked();
});
