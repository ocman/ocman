// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
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
