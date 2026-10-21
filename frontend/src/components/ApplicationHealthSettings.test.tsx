// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { renderWithQuery } from '../test/renderWithQuery';
import { fetchJSON } from '../lib/api.requests';
import { useBackendStatus } from '../lib/backendStatus';
import { useStartupStatus } from '../lib/startupStatus';
import { useDocumentVisible } from '../lib/usePanelVisible';
import { ApplicationHealthSettings } from './ApplicationHealthSettings';

vi.mock('../lib/api.requests', () => ({ fetchJSON: vi.fn() }));
vi.mock('../lib/usePanelVisible', () => ({ useDocumentVisible: vi.fn(() => true) }));

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(useDocumentVisible).mockReturnValue(true);
  useBackendStatus.setState({ unreachable: false, since: null, error: null });
  useStartupStatus.setState({ status: null, unreachable: false });
});

const renderHealth = () => renderWithQuery(<MemoryRouter><ApplicationHealthSettings /></MemoryRouter>);

it.each([
  ['ready', 'Up to date'], ['syncing', 'Syncing in background'], ['unavailable', 'Unavailable'],
])('shows %s history health without a toast', async (status, label) => {
  vi.mocked(fetchJSON).mockResolvedValue({ status, sources: 1, lastCheckedAt: '2026-10-22T10:00:00Z' });
  renderHealth();
  expect(await screen.findByText(label)).toBeVisible();
  expect(screen.getByText(/Last successful check/)).toBeVisible();
  expect(screen.getByRole('link', { name: 'Background tasks' })).toHaveAttribute('href', '/background-tasks');
  expect(screen.queryByText('History import needs attention')).not.toBeInTheDocument();
  expect(screen.getByText(/Analytics uses already-imported data/)).toBeVisible();
});

it.each([
  ['starting', 'Starting'], ['failed', 'Startup failed'],
] as const)('shows backend %s progress in health settings', async (state, label) => {
  vi.mocked(fetchJSON).mockResolvedValue({ status: 'syncing', sources: 1 });
  useStartupStatus.setState({ status: { state, phase: 'Loading history', completed: 0, total: 1, elapsedSeconds: 2 } });
  renderHealth();
  expect(screen.getByText(label)).toBeVisible();
  expect(screen.getByText('Loading history')).toBeVisible();
  await screen.findByText('Syncing in background');
});

it('reports connection and health read failures without asserting the app is healthy', async () => {
  vi.mocked(fetchJSON).mockRejectedValue(new Error('offline'));
  useBackendStatus.setState({ unreachable: true, error: 'Connection lost' });
  renderHealth();
  expect(screen.getByText('Reconnecting')).toBeVisible();
  expect(await screen.findByText('Health check unavailable')).toBeVisible();
  expect(screen.getByText('Connection lost')).toBeVisible();
});

it('does not poll hidden settings and cancels its read on unmount', async () => {
  vi.mocked(useDocumentVisible).mockReturnValue(false);
  const hidden = renderHealth();
  expect(fetchJSON).not.toHaveBeenCalled();
  hidden.unmount();
  vi.mocked(useDocumentVisible).mockReturnValue(true);
  vi.mocked(fetchJSON).mockReturnValue(new Promise(() => {}));
  const view = renderHealth();
  expect(screen.getByText('Checking…')).toBeVisible();
  await waitFor(() => expect(fetchJSON).toHaveBeenCalledOnce());
  const signal = vi.mocked(fetchJSON).mock.calls[0][1];
  view.unmount();
  expect(signal?.aborted).toBe(true);
});
