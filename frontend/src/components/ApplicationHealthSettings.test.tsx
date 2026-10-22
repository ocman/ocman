// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { renderWithQuery } from '../test/renderWithQuery';
import { fetchJSON } from '../lib/api.requests';
import { useBackendStatus } from '../lib/backendStatus';
import { useDocumentVisible } from '../lib/usePanelVisible';
import { ApplicationHealthSettings } from './ApplicationHealthSettings';

vi.mock('../lib/api.requests', () => ({ fetchJSON: vi.fn() }));
vi.mock('../lib/usePanelVisible', () => ({ useDocumentVisible: vi.fn(() => true) }));

const backendHealth = vi.fn();

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(useDocumentVisible).mockReturnValue(true);
  useBackendStatus.setState({ unreachable: false, since: null, error: null });
  backendHealth.mockReset().mockResolvedValue(new Response(JSON.stringify({ state: 'ready', phase: 'Ready' })));
  vi.stubGlobal('fetch', backendHealth);
});
afterEach(() => vi.unstubAllGlobals());

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
  backendHealth.mockResolvedValue(new Response(JSON.stringify({ state, phase: 'Loading history' })));
  renderHealth();
  expect(await screen.findByText(label)).toBeVisible();
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
  expect(backendHealth).not.toHaveBeenCalled();
  hidden.unmount();
  vi.mocked(useDocumentVisible).mockReturnValue(true);
  vi.mocked(fetchJSON).mockReturnValue(new Promise(() => {}));
  backendHealth.mockReturnValue(new Promise(() => {}));
  const view = renderHealth();
  expect(screen.getAllByText('Checking…')).toHaveLength(2);
  await waitFor(() => expect(fetchJSON).toHaveBeenCalledOnce());
  const signal = vi.mocked(fetchJSON).mock.calls[0][1];
  const backendSignal = backendHealth.mock.calls[0][1].signal;
  view.unmount();
  expect(signal?.aborted).toBe(true);
  expect(backendSignal.aborted).toBe(true);
});

it('handles standalone servers without a startup endpoint', async () => {
  backendHealth.mockResolvedValue(new Response('', { status: 404 }));
  vi.mocked(fetchJSON).mockResolvedValue({ status: 'ready', sources: 1 });
  renderHealth();
  expect(await screen.findByText('Connected')).toBeVisible();
});

it.each([
  [503, { state: 'starting', phase: 'Unavailable' }],
  [200, { state: 'unknown', phase: 'Unknown' }],
  [200, { state: 'ready' }],
])('rejects unavailable or invalid backend health: %s %j', async (status, health) => {
  backendHealth.mockResolvedValue(new Response(JSON.stringify(health), { status }));
  vi.mocked(fetchJSON).mockResolvedValue({ status: 'ready', sources: 1 });
  renderHealth();
  expect(await screen.findByText('Reconnecting')).toBeVisible();
});
