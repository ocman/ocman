// @vitest-environment jsdom
import { act, fireEvent, render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { beforeEach, expect, it, vi } from 'vitest';
import { PluginConfigurations } from './PluginConfigurations';

const mocks = vi.hoisted(() => ({ sessions: vi.fn(), read: vi.fn() }));
vi.mock('../lib/api', () => ({ api: { sessions: mocks.sessions } }));
vi.mock('../lib/pluginPages', () => ({ callPluginPage: mocks.read }));
const page = { pluginId: 'org.example.plugin', ownerId: 'machine', page: { id: 'workspace', label: 'Workspace' } };

beforeEach(() => {
  vi.resetAllMocks();
  mocks.sessions.mockImplementation(({ dir }: { dir: string }) => Promise.resolve([{ directory: dir, remoteId: 'machine', projectId: dir.slice(1) }]));
  mocks.read.mockResolvedValue({ configured: true, values: { selected_target: 'staging', targets: [{ name: 'staging' }] }, tree: {} });
});

function mount(directories: string[]) {
  const onOpen = vi.fn();
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(<QueryClientProvider client={client}><PluginConfigurations page={page} directories={directories} onOpen={onOpen} /></QueryClientProvider>);
  return { ...view, onOpen };
}

it('reads configurations serially to respect plugins with one call slot', async () => {
  let finish!: (value: unknown) => void;
  mocks.read.mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
  const { onOpen } = mount(['/one', '/two']);
  await vi.waitFor(() => expect(mocks.read).toHaveBeenCalledTimes(1));
  expect(mocks.sessions).toHaveBeenCalledTimes(1);
  await act(async () => { finish({ configured: true, values: {}, tree: {} }); });
  fireEvent.click(await screen.findByRole('button', { name: 'Open configuration for /two' }));
  expect(onOpen).toHaveBeenCalledWith(expect.objectContaining({ projectId: 'two', directory: '/two' }));
  expect(screen.getByText('Target to scan: staging')).toBeInTheDocument();
});

it('keeps readable configurations visible when another project identity cannot be resolved', async () => {
  mocks.sessions.mockResolvedValueOnce([]);
  mount(['/missing', '/readable']);
  await screen.findByRole('button', { name: 'Open configuration for /readable' });
  expect(screen.getByRole('alert')).toHaveTextContent('Some configurations could not be loaded');
  expect(screen.queryByText(/No saved configurations/)).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Retry configurations' }));
  await screen.findByRole('button', { name: 'Open configuration for /missing' });
});

it('never substitutes another owner identity and aborts in-flight reads on unmount', async () => {
  mocks.sessions.mockResolvedValueOnce([{ directory: '/one', remoteId: 'other', projectId: 'wrong' }]);
  const view = mount(['/one']);
  await screen.findByRole('alert');
  expect(mocks.read).not.toHaveBeenCalled();
  view.unmount();
  mocks.sessions.mockImplementation(() => new Promise(() => {}));
  const second = mount(['/pending']);
  await vi.waitFor(() => expect(mocks.sessions).toHaveBeenCalledTimes(2));
  const signal = mocks.sessions.mock.calls[1][1] as AbortSignal;
  second.unmount();
  expect(signal.aborted).toBe(true);
});
