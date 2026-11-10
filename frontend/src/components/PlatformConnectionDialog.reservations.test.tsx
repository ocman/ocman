// @vitest-environment jsdom
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import type { ConnectionCheck, ConnectionPlatform } from '../lib/platformConnections';
import { ConfirmProvider } from './ConfirmDialog';
import { PlatformConnectionDialog } from './PlatformConnectionDialog';

const platforms: ConnectionPlatform[] = [
  { id: 'codex', label: 'Codex', existingDirectory: '/existing', supportsIsolation: true, isolationNote: 'Separate configuration' },
  { id: 'claudecode', label: 'Claude Code', existingDirectory: '/other', supportsIsolation: true, isolationNote: 'Separate configuration' },
];
const checked = { status: 'ready' as const, reservationId: 'stp_original', directory: '/owner/reserved/config', checks: [] };
const input = { kind: 'codex', name: 'Work', configuration: 'existing', directory: '', enabled: true };

function mount(onCheck = vi.fn().mockResolvedValue(checked), onSave = vi.fn().mockResolvedValue(undefined)) {
  render(<MemoryRouter><ConfirmProvider><PlatformConnectionDialog platforms={platforms} connections={[]} onCheck={onCheck} onSave={onSave} onClose={vi.fn()} /></ConfirmProvider></MemoryRouter>);
  return { user: userEvent.setup(), onCheck, onSave };
}

async function checkInitial(user: ReturnType<typeof userEvent.setup>) {
  await user.type(screen.getByRole('textbox', { name: 'Name' }), 'Work');
  await user.click(screen.getByRole('button', { name: 'Check setup' }));
  expect(await screen.findByText(checked.directory)).toBeVisible();
}

describe('platform setup receipts', () => {
  it('passes the owner receipt through Check again and Save, without replacing input with observed paths', async () => {
    const { user, onCheck, onSave } = mount();
    await checkInitial(user);
    expect(onCheck).toHaveBeenNthCalledWith(1, input);
    await user.click(screen.getByRole('button', { name: 'Check again' }));
    expect(onCheck).toHaveBeenNthCalledWith(2, { ...input, reservationId: checked.reservationId });
    await user.click(screen.getByRole('button', { name: 'Add platform' }));
    expect(onSave).toHaveBeenCalledExactlyOnceWith({ ...input, reservationId: checked.reservationId }, undefined);
  });

  it('retains the receipt through Back, a rename, failed rechecks and failed saves', async () => {
    const onCheck = vi.fn().mockResolvedValueOnce(checked).mockRejectedValueOnce(new Error('Owner offline')).mockResolvedValue(checked);
    const onSave = vi.fn().mockRejectedValueOnce(new Error('Save response lost')).mockResolvedValue(undefined);
    const { user } = mount(onCheck, onSave);
    await checkInitial(user);
    await user.click(screen.getByRole('button', { name: 'Back' }));
    await user.clear(screen.getByRole('textbox', { name: 'Name' }));
    await user.type(screen.getByRole('textbox', { name: 'Name' }), 'Renamed');
    await user.click(screen.getByRole('button', { name: 'Check setup' }));
    expect(await screen.findByText('Owner offline')).toBeVisible();
    await user.click(screen.getByRole('button', { name: 'Check setup' }));
    const expected = { ...input, name: 'Renamed', reservationId: checked.reservationId };
    expect(onCheck).toHaveBeenNthCalledWith(2, expected);
    expect(onCheck).toHaveBeenNthCalledWith(3, expected);
    await user.click(screen.getByRole('button', { name: 'Add platform' }));
    expect(await screen.findByText('Save response lost')).toBeVisible();
    await user.click(screen.getByRole('button', { name: 'Add platform' }));
    expect(onSave).toHaveBeenNthCalledWith(1, expected, undefined);
    expect(onSave).toHaveBeenNthCalledWith(2, expected, undefined);
  });

  it.each(['kind', 'configuration', 'directory'])('starts a new setup only when the %s changes', async field => {
    const replacement = { ...checked, reservationId: 'stp_replacement' };
    const onCheck = vi.fn().mockResolvedValueOnce(checked).mockResolvedValue(replacement);
    const { user, onSave } = mount(onCheck);
    await checkInitial(user);
    await user.click(screen.getByRole('button', { name: 'Back' }));
    if (field === 'kind') await user.selectOptions(screen.getByRole('combobox', { name: 'Platform' }), 'claudecode');
    if (field === 'configuration') await user.selectOptions(screen.getByRole('combobox', { name: 'Configuration' }), 'isolated');
    if (field === 'directory') await user.type(screen.getByRole('textbox', { name: /Configuration directory/ }), '/custom');
    await user.click(screen.getByRole('button', { name: 'Check setup' }));
    expect(onCheck.mock.calls[1][0]).not.toHaveProperty('reservationId');
    await user.click(screen.getByRole('button', { name: 'Add platform' }));
    expect(onSave.mock.calls[0][0]).toHaveProperty('reservationId', replacement.reservationId);
  });

  it.each(['stp_another_account', undefined])('refuses an owner response that changes or drops the receipt: %s', async reservationId => {
    const onCheck = vi.fn().mockResolvedValueOnce(checked).mockResolvedValueOnce({ ...checked, reservationId }).mockResolvedValue(checked);
    const { user, onSave } = mount(onCheck);
    await checkInitial(user);
    await user.click(screen.getByRole('button', { name: 'Check again' }));
    expect(await screen.findByText(/The setup reservation changed/)).toBeVisible();
    expect(within(screen.getByRole('dialog')).queryByRole('button', { name: 'Add platform' })).not.toBeInTheDocument();
    expect(onSave).not.toHaveBeenCalled();
    await user.click(screen.getByRole('button', { name: 'Check setup' }));
    expect(onCheck).toHaveBeenNthCalledWith(3, { ...input, reservationId: checked.reservationId });
    expect(await screen.findByText(checked.directory)).toBeVisible();
  });

  it('keeps the receipt for whitespace-only directory edits and saves incomplete sign-in for later', async () => {
    const check: ConnectionCheck & { reservationId: string } = { ...checked, status: 'sign-in-required' };
    const { user, onCheck, onSave } = mount(vi.fn().mockResolvedValue(check));
    await checkInitial(user);
    await user.click(screen.getByRole('button', { name: 'Back' }));
    await user.type(screen.getByRole('textbox', { name: /Configuration directory/ }), '  ');
    await user.click(screen.getByRole('button', { name: 'Check setup' }));
    expect(onCheck).toHaveBeenNthCalledWith(2, { ...input, reservationId: checked.reservationId });
    await user.click(screen.getByRole('button', { name: 'Save for later' }));
    expect(onSave).toHaveBeenCalledExactlyOnceWith({ ...input, reservationId: checked.reservationId }, undefined);
  });
});
