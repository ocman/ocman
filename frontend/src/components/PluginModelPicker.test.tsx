// @vitest-environment jsdom
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { beforeEach, expect, it, vi } from 'vitest';
import { PluginPageFields } from './PluginPageFields';
import type { CapabilitiesResponse } from '../lib/api';

const mocks = vi.hoisted(() => ({ fetch: vi.fn(), capabilities: null as CapabilitiesResponse | null }));
vi.mock('../lib/api', async (original) => ({ ...await original<typeof import('../lib/api')>(), fetchJSON: mocks.fetch }));
vi.mock('../lib/useCapabilities', () => ({ useCapabilities: () => mocks.capabilities }));
vi.mock('./ModelLogo', () => ({ ModelLabel: ({ children }: { children: React.ReactNode }) => children }));

beforeEach(() => {
  mocks.fetch.mockReset();
  mocks.capabilities = { platforms: [
    { id: 'opencode', displayName: 'Local', available: true, capabilities: { modelCatalog: true } },
    { id: 'r-box:claudecode', remoteId: 'box', displayName: 'Claude Code', available: true, capabilities: { modelCatalog: true } },
    { id: 'r-box:opencode', remoteId: 'box', displayName: 'OpenCode', available: true, capabilities: { modelCatalog: true } },
    { id: 'r-box:unsupported', remoteId: 'box', available: true, capabilities: {} },
    { id: 'r-box:offline', remoteId: 'box', available: false, capabilities: { modelCatalog: true } },
  ] } as CapabilitiesResponse;
  mocks.fetch.mockImplementation(async (url: string) => ({ models: [{ provider: 'anthropic', model: url.includes('claudecode') ? 'sonnet' : 'other', isAvailable: true }] }));
});

function mount(value = '', ownerId = 'box') {
  const onChange = vi.fn();
  const view = render(<QueryClientProvider client={new QueryClient()}><PluginPageFields
    fields={[{ key: 'model', label: 'Review model', type: 'model' }]} values={{ model: value }} ownerId={ownerId} onChange={onChange}
  /></QueryClientProvider>);
  return { ...view, onChange };
}

it('uses only owner-local platform catalogs and saves a platform-qualified selection', async () => {
  const user = userEvent.setup();
  const { onChange } = mount();
  await waitFor(() => expect(screen.queryByText('Loading models…')).not.toBeInTheDocument());
  expect(mocks.fetch).toHaveBeenCalledTimes(2);
  expect(mocks.fetch).toHaveBeenCalledWith('/api/plugins/model-catalog?ownerId=box&platform=claudecode', expect.any(AbortSignal));
  expect(mocks.fetch).toHaveBeenCalledWith('/api/plugins/model-catalog?ownerId=box&platform=opencode', expect.any(AbortSignal));
  await user.click(screen.getByRole('combobox', { name: 'Review model' }));
  await user.click(screen.getByRole('option', { name: /anthropic\/sonnet.*Claude Code/ }));
  expect(onChange).toHaveBeenCalledWith({ model: 'claudecode:anthropic/sonnet' });
});

it('preserves a saved model when a catalog fails and lets the user retry or choose the default', async () => {
  mocks.fetch.mockRejectedValue(new Error('offline'));
  const user = userEvent.setup();
  const { onChange } = mount('old/custom');
  await screen.findByRole('alert');
  expect(screen.getByRole('combobox', { name: 'Review model' })).toHaveTextContent('old/custom');
  mocks.fetch.mockResolvedValue({ models: [] });
  await user.click(screen.getByRole('button', { name: 'Retry models' }));
  await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument());
  await user.click(screen.getByRole('combobox', { name: 'Review model' }));
  await user.click(screen.getByRole('option', { name: 'Use default model' }));
  expect(onChange).toHaveBeenCalledWith({ model: '' });
});

it('does not borrow another owner catalog when the requested owner is absent', async () => {
  mount('', 'missing');
  expect(screen.getByRole('status')).toHaveTextContent('No models available on this owner');
  expect(mocks.fetch).not.toHaveBeenCalled();
});

it('shows loading while capabilities are unavailable and cancels active catalog reads on unmount', async () => {
  const capabilities = mocks.capabilities;
  mocks.capabilities = null;
  const loading = mount();
  expect(screen.getByRole('status')).toHaveTextContent('Loading models');
  loading.unmount();
  mocks.capabilities = capabilities;
  mocks.fetch.mockImplementation((_url, signal: AbortSignal) => new Promise((_resolve, reject) => {
    signal.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')));
  }));
  const view = mount();
  await waitFor(() => expect(mocks.fetch).toHaveBeenCalledTimes(2));
  const signal = mocks.fetch.mock.calls[0][1] as AbortSignal;
  view.unmount();
  expect(signal.aborted).toBe(true);
});
