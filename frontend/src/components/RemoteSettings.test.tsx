// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { render, screen, fireEvent, waitFor, act, within } from '@testing-library/react';
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
    render(<RemoteSettings />);
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
    render(<RemoteSettings />);
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
    render(<RemoteSettings />);
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
    render(<RemoteSettings />);
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
    render(<RemoteSettings />);
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
    render(<RemoteSettings />);
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
    render(<RemoteSettings />);
    expect(await screen.findByText('This machine')).toBeInTheDocument();
    expect(screen.getByText(/inst123/)).toBeInTheDocument();
    expect(screen.getByText(/listening on 0.0.0.0:8230/)).toBeInTheDocument();
  });

  it('reveals the token only via the explicit action', async () => {
    seed();
    m.revealRemoteToken.mockResolvedValue({ token: 'super-secret' });
    render(<RemoteSettings />);

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
    render(<RemoteSettings />);
    expect(await screen.findByText('Box')).toBeInTheDocument();
    expect(screen.getByText('connected')).toBeInTheDocument();
  });

  it('adds a remote via the form and refreshes the list', async () => {
    seed();
    m.addRemote.mockResolvedValue({ localId: 9 });
    render(<RemoteSettings />);
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
    render(<RemoteSettings />);
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
    render(<RemoteSettings />);
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
    render(<RemoteSettings />);
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
});
