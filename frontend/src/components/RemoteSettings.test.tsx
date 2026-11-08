// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { screen, fireEvent, waitFor, act, within } from '@testing-library/react';
import { renderWithQuery } from '../test/renderWithQuery';
import { RemoteSettings } from './RemoteSettings';

vi.mock('../lib/api', () => ({
  api: {
    remoteAccess: vi.fn(),
    revealRemoteToken: vi.fn(),
    listRemotes: vi.fn(),
    addRemote: vi.fn(),
    updateRemote: vi.fn(),
    removeRemote: vi.fn(),
    reconnectRemote: vi.fn(),
  },
}));
import { api } from '../lib/api';

const m = api as unknown as Record<string, ReturnType<typeof vi.fn>>;

afterEach(() => {
  vi.clearAllMocks();
});

function seed(remotes: unknown[] = []) {
  m.remoteAccess.mockResolvedValue({
    instanceId: 'inst123', listening: true, listenAddr: '0.0.0.0:8230', tls: false, tokenSet: true,
  });
  m.listRemotes.mockResolvedValue(remotes);
}

describe('RemoteSettings', () => {
  it('lists machines in a shared table and edits in a drawer without replacing the row', async () => {
    seed([{ localId: 1, displayName: 'Box', address: 'ws:8230', enabled: true, health: 'connected' }]);
    renderWithQuery(<RemoteSettings />);
    await screen.findByText('Box');
    expect(screen.getByRole('table', { name: 'Machines' })).toHaveClass('oc-data-table');
    expect(screen.getAllByRole('columnheader').map((header) => header.textContent)).toEqual(['Machine', 'Connection', 'Status', 'Actions']);
    expect(screen.getByText('Box').closest('tr')).toHaveTextContent('ws:8230');
    expect(screen.getByText('This machine').closest('tr')).toHaveTextContent('0.0.0.0:8230');
    fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    const drawer = screen.getByRole('dialog', { name: 'Edit remote' });
    expect(within(drawer).getByDisplayValue('Box')).toBeInTheDocument();
    expect(screen.getByText('Box').closest('tr')).toHaveTextContent('connected');
    expect(screen.getByDisplayValue('Box').closest('td')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.getByText('Box').closest('tr')).toHaveTextContent('connected');
  });

  it('keeps the edit drawer and values after a failed save, then allows retry', async () => {
    seed([{ localId: 1, displayName: 'Box', address: 'ws:8230', enabled: true, health: 'connected' }]);
    m.updateRemote.mockRejectedValueOnce(new Error('connection failed')).mockResolvedValueOnce({ ok: true });
    renderWithQuery(<RemoteSettings />);
    fireEvent.click(await screen.findByRole('button', { name: 'Edit' }));
    fireEvent.change(screen.getByDisplayValue('Box'), { target: { value: 'Renamed' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Failed to save remote.');
    expect(screen.getByDisplayValue('Renamed')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(m.updateRemote).toHaveBeenCalledTimes(2);
  });

  it('blocks drawer dismissal and repeat edits while saving', async () => {
    seed([{ localId: 1, displayName: 'Box', address: 'ws:8230', enabled: true, health: 'connected' }]);
    let resolve!: (value: unknown) => void;
    m.updateRemote.mockImplementation(() => new Promise((done) => { resolve = done; }));
    renderWithQuery(<RemoteSettings />);
    fireEvent.click(await screen.findByRole('button', { name: 'Edit' }));
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(screen.getByDisplayValue('Box')).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Close Edit remote' })).toBeDisabled();
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(screen.getByRole('dialog')).toBeInTheDocument();
    await act(async () => { resolve({ ok: true }); });
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('uses shared action controls and fields throughout the add and edit forms', async () => {
    seed([{ localId: 1, displayName: 'Box', address: 'ws:8230', enabled: true, health: 'connected' }]);
    renderWithQuery(<RemoteSettings />);
    await screen.findByText('Box');
    for (const button of screen.getAllByRole('button')) expect(button).toHaveClass('oc-button');
    for (const field of screen.getAllByRole('textbox')) expect(field).toHaveClass('oc-field');
    fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    for (const button of screen.getAllByRole('button')) expect(button).toHaveClass('oc-button');
    for (const field of screen.getAllByRole('textbox')) expect(field).toHaveClass('oc-field');
    expect(screen.getByRole('checkbox', { name: 'Enabled' })).toBeChecked();
  });

  it.each([true, false])('toggles an enabled=%s remote without replacing its connection settings', async (enabled) => {
    const remote = {
      localId: 1, remoteId: 'abc', displayName: 'Box', address: 'grpc://ws:8230',
      enabled, health: 'connected', hostname: 'ws.host', protocolVersion: 1,
      lastSeen: 0, sessionCount: 2,
    };
    seed([remote]);
    m.updateRemote.mockImplementation(async () => {
      m.listRemotes.mockResolvedValue([{ ...remote, enabled: !enabled }]);
      return { ok: true };
    });
    renderWithQuery(<RemoteSettings />);
    const toggle = await screen.findByRole('button', { name: enabled ? 'Disable' : 'Enable' });
    fireEvent.click(toggle);
    await waitFor(() => expect(m.updateRemote).toHaveBeenCalledWith(1, {
      address: remote.address, displayName: remote.displayName, enabled: !enabled,
    }));
    await screen.findByRole('button', { name: enabled ? 'Enable' : 'Disable' });
    expect(screen.getByText('Box')).toBeInTheDocument();
    expect(m.removeRemote).not.toHaveBeenCalled();
    if (enabled) {
      expect(screen.getByText('disabled')).toBeInTheDocument();
      expect(screen.queryByText('connected')).not.toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'Reconnect' })).toBeDisabled();
    }
  });

  it.each([
    [new Error('Remote update failed'), 'Remote update failed'],
    ['failure', 'Failed to update remote.'],
  ])('keeps the toggle busy during a failed update and shows %s', async (failure, message) => {
    seed([{
      localId: 1, displayName: 'Box', address: 'grpc://ws:8230', enabled: true, health: 'connected',
    }]);
    let reject!: (error: unknown) => void;
    m.updateRemote.mockImplementation(() => new Promise((_, fail) => { reject = fail; }));
    renderWithQuery(<RemoteSettings />);
    const toggle = await screen.findByRole('button', { name: 'Disable' });
    fireEvent.click(toggle);
    expect(toggle).toBeDisabled();
    await act(async () => { reject(failure); });
    expect(screen.getByRole('alert')).toHaveTextContent(message);
    expect(toggle).toBeEnabled();
    expect(m.listRemotes).toHaveBeenCalledOnce();
    expect(screen.getByText('connected')).toBeInTheDocument();
  });

  it('shows the non-removable "This machine" entry with listen status', async () => {
    seed();
    renderWithQuery(<RemoteSettings />);
    expect(await screen.findByText('This machine')).toBeInTheDocument();
    expect(screen.getByText(/inst123/)).toBeInTheDocument();
    expect(screen.getByText(/listening on 0.0.0.0:8230/)).toBeInTheDocument();
  });

  it('reveals the token only via the explicit action', async () => {
    seed();
    m.revealRemoteToken.mockResolvedValue({ token: 'super-secret' });
    renderWithQuery(<RemoteSettings />);

    // Token is not present until revealed.
    await screen.findByText('This machine');
    expect(screen.queryByDisplayValue('super-secret')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Reveal token' }));
    expect(await screen.findByDisplayValue('super-secret')).toBeInTheDocument();
    expect(m.revealRemoteToken).toHaveBeenCalledOnce();
  });

  it('lists configured remotes with health', async () => {
    seed([
      {
        localId: 1, remoteId: 'abc', displayName: 'Box', address: 'ws:8230',
        enabled: true, health: 'connected', hostname: 'ws.host', protocolVersion: 1,
        lastSeen: 0, sessionCount: 2,
      },
    ]);
    renderWithQuery(<RemoteSettings />);
    expect(await screen.findByText('Box')).toBeInTheDocument();
    expect(screen.getByText('connected')).toBeInTheDocument();
  });

  it('adds a remote via the form and refreshes the list', async () => {
    seed();
    m.addRemote.mockResolvedValue({ localId: 9 });
    renderWithQuery(<RemoteSettings />);
    await screen.findByText('This machine');

    fireEvent.change(screen.getByLabelText('Remote address'), { target: { value: 'ws:8230' } });
    fireEvent.change(screen.getByLabelText('Remote-access token'), { target: { value: 'tok' } });
    fireEvent.change(screen.getByLabelText('Display name'), { target: { value: 'NewBox' } });

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Add remote' }));
    });

    await waitFor(() => expect(m.addRemote).toHaveBeenCalledWith({
      address: 'ws:8230', token: 'tok', displayName: 'NewBox',
    }));
    // listRemotes is called on mount and again after add.
    expect(m.listRemotes.mock.calls.length).toBeGreaterThanOrEqual(2);
  });

  it('edits a remote name/address via the edit form', async () => {
    seed([
      {
        localId: 1, remoteId: 'abc', displayName: 'Box', address: 'ws:8230',
        enabled: true, health: 'connected', hostname: 'ws.host', protocolVersion: 1,
        lastSeen: 0, sessionCount: 0,
      },
    ]);
    m.updateRemote.mockResolvedValue({ ok: true });
    renderWithQuery(<RemoteSettings />);
    await screen.findByText('Box');

    fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    // The edit form's display-name input is pre-filled with the current
    // name; target it by value to disambiguate from the add form's.
    const nameInput = await screen.findByDisplayValue('Box');
    fireEvent.change(nameInput, { target: { value: 'Renamed' } });
    fireEvent.click(screen.getByLabelText('Enabled')); // toggle to disabled

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    });

    await waitFor(() => expect(m.updateRemote).toHaveBeenCalledWith(1, expect.objectContaining({
      displayName: 'Renamed',
      enabled: false,
    })));
  });

  it('cancels the edit form without saving', async () => {
    seed([
      {
        localId: 1, remoteId: 'abc', displayName: 'Box', address: 'ws:8230',
        enabled: true, health: 'connected', hostname: '', protocolVersion: 1,
        lastSeen: 0, sessionCount: 0,
      },
    ]);
    renderWithQuery(<RemoteSettings />);
    await screen.findByText('Box');
    fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Cancel' }));
    // Back to the row view (Reconnect button reappears).
    expect(await screen.findByRole('button', { name: 'Reconnect' })).toBeInTheDocument();
    expect(m.updateRemote).not.toHaveBeenCalled();
  });

  it('reconnects and removes a remote', async () => {
    seed([
      {
        localId: 1, remoteId: 'abc', displayName: 'Box', address: 'ws:8230',
        enabled: true, health: 'offline', hostname: '', protocolVersion: 1,
        lastSeen: 0, sessionCount: 0,
      },
    ]);
    m.reconnectRemote.mockResolvedValue({ ok: true });
    m.removeRemote.mockResolvedValue({ ok: true });
    renderWithQuery(<RemoteSettings />);
    await screen.findByText('Box');

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Reconnect' }));
    });
    expect(m.reconnectRemote).toHaveBeenCalledWith(1);

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Remove' }));
    });
    expect(m.removeRemote).toHaveBeenCalledWith(1);
  });

  it('shows whether each remote uses this hub\'s settings', async () => {
    seed([
      { localId: 1, displayName: 'Managed', address: 'a:8230', enabled: true, health: 'connected', managedSettings: 'applied' },
      { localId: 2, displayName: 'Plain', address: 'b:8230', enabled: true, health: 'connected', managedSettings: 'not-managed' },
      { localId: 3, displayName: 'Old', address: 'c:8230', enabled: true, health: 'connected', managedSettings: 'unsupported' },
      { localId: 4, displayName: 'Broken', address: 'd:8230', enabled: true, health: 'connected', managedSettings: 'failed' },
      { localId: 5, displayName: 'Offline', address: 'e:8230', enabled: true, health: 'offline' },
    ]);
    renderWithQuery(<RemoteSettings />);
    const row = async (name: string) => (await screen.findByText(name)).closest('tr')!;
    expect(await row('Managed')).toHaveTextContent("Uses this hub's auto-approve settings");
    expect(await row('Plain')).toHaveTextContent('start it with -remote-managed');
    expect(await row('Old')).toHaveTextContent('update ocman on the remote');
    expect(await row('Broken')).toHaveTextContent('Could not send settings');
    expect(within(await row('Offline')).queryByTestId('managed-settings')).toBeNull();
  });

  it('shows which hub manages this machine', async () => {
    seed();
    m.remoteAccess.mockResolvedValueOnce({
      instanceId: 'inst123', listening: true, listenAddr: '0.0.0.0:8230', tls: true, tokenSet: true, managed: {},
    });
    const { unmount } = renderWithQuery(<RemoteSettings />);
    expect(await screen.findByTestId('managed-by')).toHaveTextContent('using local settings until a hub connects');
    unmount();
    m.remoteAccess.mockResolvedValueOnce({
      instanceId: 'inst123', listening: true, listenAddr: '0.0.0.0:8230', tls: true, tokenSet: true, managed: { hub: 'hub-1', appliedAt: 1 },
    });
    renderWithQuery(<RemoteSettings />);
    expect(await screen.findByTestId('managed-by')).toHaveTextContent('managed by hub hub-1');
  });
});
