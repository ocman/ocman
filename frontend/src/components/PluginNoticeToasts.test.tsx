// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import * as Toast from '@radix-ui/react-toast';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import type { PluginActionRequest } from '../lib/plugins';
import { renderWithQuery } from '../test/renderWithQuery';
import { PluginNoticeToasts } from './PluginNoticeToasts';

vi.mock('../lib/settingsQueries', () => ({ useHostCapabilities: () => ({ data: [
  { remoteId: 'local', pluginManagement: true }, { remoteId: 'machine', pluginManagement: true }, { remoteId: 'viewer', pluginManagement: false },
] }) }));

const recorder = 'be.nousefreak.recorder';
const recording = { id: 'recording', tone: 'warning', title: 'Recording meeting', body: 'Audio is being captured.', startedAt: Date.now() - 65_000,
  actions: [{ actionId: 'stop-recording', label: 'Stop recording', placement: 'global' }] };
let snapshots: Record<string, unknown>;
let offline: Set<string>;
let invocations: PluginActionRequest[];
const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });

beforeEach(() => {
  snapshots = {
    local: { [recorder]: { pluginLabel: 'Meeting recorder', notices: [recording] } },
    machine: { 'org.example.scan': { pluginLabel: 'Scanner', notices: [{ id: 'scan', tone: 'critical', title: 'Scan failed', actions: [] }] } },
  };
  offline = new Set();
  invocations = [];
  vi.stubGlobal('fetch', vi.fn<typeof fetch>(async (input, init) => {
    const url = new URL(String(input), 'http://localhost');
    if (url.pathname === '/api/plugins/notices') {
      const owner = url.searchParams.get('ownerId')!;
      if (owner === 'viewer') throw new Error('viewer must not be polled');
      return offline.has(owner) ? json({ error: 'offline' }, 503) : json(snapshots[owner] ?? {});
    }
    if (url.pathname === '/api/plugins/actions/invoke') {
      const request = JSON.parse(String(init?.body)) as PluginActionRequest;
      invocations.push(request);
      return request.confirmationToken
        ? json({ results: [{ kind: 'notice', text: 'Recording stopped.' }] })
        : json({ confirmation: { text: 'Stop the recording?', token: 'token-1', expiresAt: Date.now() + 60_000 } }, 409);
    }
    throw new Error(`unexpected ${url}`);
  }));
});

afterEach(() => vi.unstubAllGlobals());

function mount() {
  const result = renderWithQuery(<MemoryRouter><Toast.Provider duration={Infinity}><PluginNoticeToasts /><Toast.Viewport /></Toast.Provider></MemoryRouter>);
  const refresh = () => act(async () => { await result.client.refetchQueries({ queryKey: ['plugin-notices'] }); });
  return { ...result, refresh };
}

it('shows each owner’s notices as plain text with declared actions and elapsed time', async () => {
  mount();
  const toast = (await screen.findByText('Recording meeting')).closest('li')!;
  expect(toast).toHaveAttribute('data-tone', 'warning');
  expect(within(toast).getByText('Meeting recorder')).toBeInTheDocument();
  expect(within(toast).getByText('Audio is being captured.')).toBeInTheDocument();
  expect(within(toast).getByText(/^1m \ds$/)).toBeInTheDocument();
  expect(within(toast).getByRole('button', { name: 'Stop recording' })).toBeInTheDocument();
  const scan = (await screen.findByText('Scan failed')).closest('li')!;
  expect(scan).toHaveAttribute('data-tone', 'critical');
  expect(within(scan).queryByRole('group')).not.toBeInTheDocument();
  expect(within(scan).queryByText(/Elapsed/)).not.toBeInTheDocument();
});

it('runs a toast action through the confirmation dialog without dismissing the notice', async () => {
  const { client } = mount();
  const invalidate = vi.spyOn(client, 'invalidateQueries');
  fireEvent.click(await screen.findByRole('button', { name: 'Stop recording' }));
  const dialog = await screen.findByRole('dialog', { name: 'Stop recording' });
  expect(await within(dialog).findByText('Stop the recording?')).toBeInTheDocument();
  expect(invocations[0]).toEqual({ ownerId: 'local', pluginId: recorder, actionId: 'stop-recording', operationId: expect.any(String),
    placement: 'global', surface: 'command-palette', context: { ownerId: 'local' } });

  // Cancelling keeps the notice: a declined confirmation must not hide it.
  fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
  expect(screen.getByText('Recording meeting')).toBeInTheDocument();

  fireEvent.click(screen.getByRole('button', { name: 'Stop recording' }));
  const second = await screen.findByRole('dialog', { name: 'Stop recording' });
  fireEvent.click(await within(second).findByRole('button', { name: 'Confirm' }));
  expect(await within(second).findByText('Recording stopped.')).toBeInTheDocument();
  expect(invocations.at(-1)).toMatchObject({ operationId: invocations[1].operationId, confirmationToken: 'token-1' });
  expect(invocations[1].operationId).not.toBe(invocations[0].operationId);
  fireEvent.click(within(second).getByRole('button', { name: 'Close' }));
  expect(invalidate).toHaveBeenCalledWith({ queryKey: ['plugin-notices'] });
});

it('keeps a dismissal while the notice ID is active and shows it again once it returns', async () => {
  const { refresh } = mount();
  fireEvent.click(await screen.findByRole('button', { name: 'Dismiss Recording meeting' }));
  await waitFor(() => expect(screen.queryByText('Recording meeting')).not.toBeInTheDocument());
  await refresh();
  expect(screen.queryByText('Recording meeting')).not.toBeInTheDocument();

  // Another owner reporting nothing must not reset this owner's dismissal.
  snapshots.machine = {};
  await refresh();
  expect(screen.queryByText('Recording meeting')).not.toBeInTheDocument();

  snapshots.local = {};
  await refresh();
  snapshots.local = { [recorder]: { pluginLabel: 'Meeting recorder', notices: [recording] } };
  await refresh();
  expect(await screen.findByText('Recording meeting')).toBeInTheDocument();
});

it('hides an owner’s notices while it is unreachable', async () => {
  const { refresh } = mount();
  expect(await screen.findByText('Scan failed')).toBeInTheDocument();
  offline.add('machine');
  await refresh();
  await waitFor(() => expect(screen.queryByText('Scan failed')).not.toBeInTheDocument());
  expect(screen.getByText('Recording meeting')).toBeInTheDocument();
  offline.delete('machine');
  await refresh();
  expect(await screen.findByText('Scan failed')).toBeInTheDocument();
});

it('ignores a malformed owner response', async () => {
  snapshots.local = ['not', 'a', 'map'];
  snapshots.machine = { 'org.example.scan': { pluginLabel: 'Scanner', notices: null } };
  const { refresh } = mount();
  await refresh();
  expect(screen.queryByRole('status')).not.toBeInTheDocument();
  expect(document.querySelectorAll('li')).toHaveLength(0);
});

it('renders a notice with an unrepresentable start time without crashing', async () => {
  snapshots.local = { [recorder]: { pluginLabel: 'Meeting recorder', notices: [{ ...recording, startedAt: 8640000000000001 }] } };
  mount();
  const toast = (await screen.findByText('Recording meeting')).closest('li')!;
  expect(within(toast).getByRole('button', { name: 'Stop recording' })).toBeInTheDocument();
  expect(toast.querySelector('time')).toBeNull();
});
