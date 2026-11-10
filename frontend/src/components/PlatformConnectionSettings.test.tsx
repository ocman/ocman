// @vitest-environment jsdom
import { act, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { queryClient } from '../lib/queryClient';
import { readConnectionSetup, writeConnectionSetup } from '../lib/connectionSetupReceipt';
import { PlatformConnectionSettings } from './PlatformConnectionSettings';
import { ConfirmProvider } from './ConfirmDialog';

const inventory = { supported: true, onboardingComplete: true, connections: [], platforms: [{ id: 'codex', label: 'Codex', existingDirectory: '/owner/default', supportsIsolation: true, isolationNote: 'Separate config' }] };
const receipt = { kind: 'codex', name: 'Work', configuration: 'isolated' as const, directory: '', enabled: true, reservationId: 'stp_original' };
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
function mount(owner = 'local') { return render(<MemoryRouter initialEntries={[`/settings?remoteId=${owner}`]}><ConfirmProvider><PlatformConnectionSettings /></ConfirmProvider></MemoryRouter>); }

beforeEach(() => { queryClient.clear(); localStorage.clear(); });
afterEach(() => { vi.unstubAllGlobals(); queryClient.clear(); });

describe('owner connection settings', () => {
  it('renames, sets the default, toggles intent and removes only the explicitly selected account', async () => {
    const connection = { id: 'cnx_work', kind: 'codex', name: 'Work', configuration: 'existing', directory: '/owner/default', enabled: true, isDefault: false, status: 'ready' };
    let rows = [connection];
    let rejectRemoval = true;
    const fetch = vi.fn(async (url: string, init?: RequestInit) => {
      if (init?.method === 'PATCH') {
        expect(url).toBe('/api/platform-connections/cnx_work?remoteId=local');
        const body = JSON.parse(String(init.body));
        rows = rows.map(row => ({ ...row, ...body, isDefault: body.default ?? row.isDefault }));
        return json({ saved: true });
      }
      if (init?.method === 'DELETE') {
        if (rejectRemoval) return new Response('Connection still in use', { status: 409 });
        rows = [];
        return json({ removed: true });
      }
      return json({ ...inventory, connections: rows });
    });
    vi.stubGlobal('fetch', fetch);
    mount();
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'Edit Work' }));
    const dialog = within(screen.getByRole('dialog', { name: 'Edit platform' }));
    await user.clear(dialog.getByRole('textbox', { name: 'Name' }));
    await user.type(dialog.getByRole('textbox', { name: 'Name' }), 'Work renamed');
    await user.click(dialog.getByRole('button', { name: 'Save changes' }));
    await user.click(await screen.findByRole('button', { name: 'Set default' }));
    await screen.findByText('Work renamed is now the default.');
    await user.click(screen.getByRole('button', { name: 'Disable Work renamed' }));
    await screen.findByText('Work renamed disabled.');
    await user.click(screen.getByRole('button', { name: 'Enable Work renamed' }));
    await screen.findByText('Work renamed enabled.');
    async function confirmRemove() {
      await user.click(screen.getByRole('button', { name: 'Remove Work renamed' }));
      await user.click(within(await screen.findByRole('dialog', { name: 'Remove Work renamed?' })).getByRole('button', { name: 'Remove connection' }));
    }
    await confirmRemove();
    expect(await screen.findByText('Connection still in use')).toBeVisible();
    expect(screen.getByRole('button', { name: 'Work renamed' })).toBeVisible();
    rejectRemoval = false;
    await confirmRemove();
    expect(await screen.findByText('No platforms connected')).toBeVisible();
    expect(fetch.mock.calls.filter(([, init]) => init?.method === 'PATCH').map(([, init]) => JSON.parse(String(init?.body)))).toEqual([
      { name: 'Work renamed' }, { enabled: true }, { default: true }, { enabled: false }, { enabled: true },
    ]);
  });
  it('keeps explicit remote ownership for resumed checks and saving', async () => {
    writeConnectionSetup('box', receipt);
    const fetch = vi.fn(async (url: string, init?: RequestInit) => {
      expect(url).toContain('remoteId=box');
      if (url.includes('/check')) {
        expect(JSON.parse(String(init?.body))).toMatchObject(receipt);
        return json({ status: 'ready', reservationId: receipt.reservationId, checks: [] });
      }
      return json(inventory);
    });
    vi.stubGlobal('fetch', fetch);
    mount('box');
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'Resume setup' }));
    expect(screen.getByRole('textbox', { name: 'Name' })).toHaveValue('Work');
    await user.click(screen.getByRole('button', { name: 'Check setup' }));
    await user.click(await within(screen.getByRole('dialog')).findByRole('button', { name: 'Add platform' }));
    await waitFor(() => expect(fetch.mock.calls.some(([, init]) => init?.method === 'POST' && !String(init.body).includes('kind'))).toBe(true));
    expect(readConnectionSetup('box')).toBeUndefined();
  });
  it('retains owner receipts when a failed setup check is returned', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string) => url.includes('/check') ? json({ reservationId: 'stp_failed', error: 'probe failed' }, 409) : json(inventory)));
    mount();
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'Add platform' }));
    await user.type(screen.getByRole('textbox', { name: 'Name' }), 'Work');
    await user.click(screen.getByRole('button', { name: 'Check setup' }));
    expect(await screen.findByText(/Your reservation is kept/)).toBeVisible();
    expect(readConnectionSetup('local')?.reservationId).toBe('stp_failed');
  });
  it('cancels the owner receipt before dismissing resumed setup', async () => {
    writeConnectionSetup('local', receipt);
    const fetch = vi.fn(async () => json(inventory));
    vi.stubGlobal('fetch', fetch);
    mount();
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'Resume setup' }));
    await user.click(screen.getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(fetch).toHaveBeenCalledWith('/api/platform-connections/setup/stp_original?remoteId=local', expect.objectContaining({ method: 'DELETE' }));
    expect(readConnectionSetup('local')).toBeUndefined();
  });
  it('does not borrow local setup when a remote is unsupported', async () => {
    const fetch = vi.fn(async () => json({ ...inventory, supported: false }));
    vi.stubGlobal('fetch', fetch);
    await act(async () => { mount('disconnected'); });
    expect(await screen.findByText('This owner does not support named platform connections.')).toBeVisible();
    expect(screen.queryByRole('button', { name: 'Add platform' })).not.toBeInTheDocument();
    expect(fetch.mock.calls).toHaveLength(1);
  });
});
