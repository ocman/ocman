// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { MemoryRouter } from 'react-router-dom';
import { BackgroundTaskCounter } from './BackgroundTaskCounter';

const count = vi.hoisted(() => vi.fn());
let visible = true;
vi.mock('../lib/backgroundTasks', () => ({ backgroundTaskCount: count }));
vi.mock('../lib/usePanelVisible', () => ({ useDocumentVisible: () => visible }));

let client: QueryClient;
beforeEach(() => {
  visible = true;
  count.mockReset().mockResolvedValue({ count: 3 });
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
});
afterEach(() => client.clear());

function view(enabled = true) {
  return <QueryClientProvider client={client}><MemoryRouter><BackgroundTaskCounter enabled={enabled} /></MemoryRouter></QueryClientProvider>;
}

it('shows a compact active count linking to the existing task page', async () => {
  render(view());
  const link = await screen.findByRole('link', { name: 'Active background tasks: 3' });
  expect(link).toHaveAttribute('href', '/background-tasks');
  expect(link).toHaveTextContent('bgt: 3');
  expect(count).toHaveBeenCalledWith(expect.any(AbortSignal));
});

it('keeps the link available when idle, loading or unavailable', async () => {
  count.mockRejectedValue(new Error('restarting'));
  render(view());
  expect(screen.getByRole('link', { name: 'Background tasks: count unavailable' })).toHaveAttribute('href', '/background-tasks');
  await waitFor(() => expect(client.getQueryState(['background-task-count'])?.status).toBe('error'));
  expect(screen.getByRole('link')).toHaveTextContent('bgt: —');
});

it('does not fetch while disabled or hidden, and retains the last count', async () => {
  const { rerender } = render(view(false));
  expect(count).not.toHaveBeenCalled();
  visible = false;
  rerender(view());
  expect(count).not.toHaveBeenCalled();
  visible = true;
  count.mockResolvedValue({ count: 0 });
  rerender(view());
  await screen.findByRole('link', { name: 'Active background tasks: 0' });
  rerender(view(false));
  expect(screen.getByRole('link')).toHaveTextContent('bgt: 0');
});
