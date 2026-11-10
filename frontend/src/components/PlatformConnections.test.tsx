// @vitest-environment jsdom
import { render, screen, within, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import type { ConnectionCheck, ConnectionPlatform, PlatformConnection } from '../lib/platformConnections';
import { ConfirmProvider } from './ConfirmDialog';
import { PlatformConnections, type PlatformConnectionsProps } from './PlatformConnections';

const platforms: ConnectionPlatform[] = [
  { id: 'opencode', label: 'OpenCode', existingDirectory: '/home/user/.config/opencode', supportsIsolation: true, isolationNote: 'Separate OpenCode config, credentials, database and server.' },
  { id: 'claudecode', label: 'Claude Code', existingDirectory: '/home/user/.claude', supportsIsolation: true, isolationNote: 'A separate Claude configuration.' },
  { id: 'codex', label: 'Codex', existingDirectory: '/home/user/.codex', supportsIsolation: true, isolationNote: 'A separate Codex home.' },
];
const personal: PlatformConnection = { id: 'pc-personal', kind: 'claudecode', name: 'Claude Personal', enabled: true, isDefault: true, configuration: 'existing', directory: '/home/user/.claude', status: 'ready', account: 'personal@example.com' };
const work: PlatformConnection = { ...personal, id: 'pc-work', name: 'Claude Work', isDefault: false, configuration: 'isolated', directory: '/data/connections/work', account: 'work@example.com' };
const ready: ConnectionCheck = { status: 'ready', account: 'verified@example.com', checks: [{ label: 'Credentials', ok: true, detail: 'Signed in.' }] };

function mount(overrides: Partial<PlatformConnectionsProps> = {}) {
  const props: PlatformConnectionsProps = { connections: [personal, work], platforms, machineName: 'This machine', onRetry: vi.fn(), onSave: vi.fn().mockResolvedValue(undefined), onCheck: vi.fn().mockResolvedValue(ready), onRemove: vi.fn().mockResolvedValue(undefined), onDefault: vi.fn().mockResolvedValue(undefined), onEnabledChange: vi.fn().mockResolvedValue(undefined), ...overrides };
  const user = userEvent.setup();
  render(<MemoryRouter><ConfirmProvider><PlatformConnections {...props} /></ConfirmProvider></MemoryRouter>);
  return { props, user };
}

describe('PlatformConnections', () => {
  it('shows two separately named connections for one platform with a stable default', () => {
    mount();
    expect(screen.getByRole('button', { name: 'Claude Personal' })).toBeVisible();
    expect(screen.getByRole('button', { name: 'Claude Work' })).toBeVisible();
    expect(screen.getByText('personal@example.com')).toBeVisible();
    expect(screen.getByText('work@example.com')).toBeVisible();
    expect(screen.getByText('Default')).toBeVisible();
    expect(screen.getAllByRole('listitem')).toHaveLength(2);
  });

  it('distinguishes loading, initial failure, and successful empty onboarding', async () => {
    const { user, props } = mount({ connections: null, error: 'Owner disconnected' });
    expect(screen.getByRole('alert')).toHaveTextContent('Platforms unavailable');
    expect(screen.queryByText('Connect your first platform')).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Retry' }));
    expect(props.onRetry).toHaveBeenCalledOnce();
  });

  it('shows a loading state without creation controls', () => {
    mount({ connections: null });
    expect(screen.getByRole('status')).toHaveTextContent('Loading platform connections');
    expect(screen.queryByRole('button', { name: 'Add platform' })).not.toBeInTheDocument();
  });

  it('uses the same add dialog in onboarding and supports setup later', async () => {
    const onSkip = vi.fn();
    const onContinue = vi.fn();
    const { user } = mount({ connections: [], onboarding: true, onSkip, onContinue });
    expect(screen.getByText('Connect your first platform')).toBeVisible();
    expect(screen.getByRole('button', { name: 'Choose a project' })).toBeDisabled();
    await user.click(screen.getByRole('button', { name: 'Set up later' }));
    expect(onSkip).toHaveBeenCalledOnce();
    await user.click(screen.getByRole('button', { name: 'Add platform' }));
    expect(screen.getByRole('dialog', { name: 'Add platform' })).toBeVisible();
  });

  it('only enables the project step for an enabled ready connection', async () => {
    const onContinue = vi.fn();
    const { user } = mount({ onboarding: true, onContinue });
    await user.click(screen.getByRole('button', { name: 'Choose a project' }));
    expect(onContinue).toHaveBeenCalledOnce();
  });

  it('requires an explicit default before the onboarding project step', () => {
    mount({ connections: [work], onboarding: true, onContinue: vi.fn() });
    expect(screen.getByRole('button', { name: 'Choose a project' })).toBeDisabled();
  });

  it('creates an isolated OpenCode connection without trusting client account observations', async () => {
    const { user, props } = mount();
    await user.click(screen.getByRole('button', { name: 'Add platform' }));
    await user.type(screen.getByRole('textbox', { name: 'Name' }), 'OpenCode Work');
    await user.selectOptions(screen.getByRole('combobox', { name: 'Configuration' }), 'isolated');
    expect(screen.getByText(/Separate OpenCode config/)).toBeVisible();
    expect(screen.queryByRole('textbox', { name: /Configuration directory/ })).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Check setup' }));
    expect(props.onCheck).toHaveBeenCalledWith({ kind: 'opencode', name: 'OpenCode Work', configuration: 'isolated', directory: '', enabled: true });
    expect(await screen.findByText('Account: verified@example.com')).toBeVisible();
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Add platform' }));
    expect(props.onSave).toHaveBeenCalledWith({ kind: 'opencode', name: 'OpenCode Work', configuration: 'isolated', directory: '', enabled: true }, undefined);
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
  });

  it('checks an existing Codex configuration and trims user input', async () => {
    const { user, props } = mount();
    await user.click(screen.getByRole('button', { name: 'Add platform' }));
    await user.selectOptions(screen.getByRole('combobox', { name: 'Platform' }), 'codex');
    await user.type(screen.getByRole('textbox', { name: 'Name' }), ' Codex Work ');
    await user.type(screen.getByRole('textbox', { name: /Configuration directory/ }), ' /accounts/codex ');
    await user.click(screen.getByRole('button', { name: 'Check setup' }));
    expect(props.onCheck).toHaveBeenCalledWith({ kind: 'codex', name: 'Codex Work', configuration: 'existing', directory: '/accounts/codex', enabled: true });
    await user.click(screen.getByRole('button', { name: 'Back' }));
    expect(screen.getByRole('textbox', { name: 'Name' })).toHaveValue(' Codex Work ');
  });

  it('rejects duplicate names across platforms ignoring whitespace and case', async () => {
    const { user, props } = mount();
    await user.click(screen.getByRole('button', { name: 'Add platform' }));
    await user.type(screen.getByRole('textbox', { name: 'Name' }), ' claude WORK ');
    expect(screen.getByText('A connection with this name already exists.')).toBeVisible();
    expect(screen.getByRole('button', { name: 'Check setup' })).toBeDisabled();
    expect(props.onCheck).not.toHaveBeenCalled();
  });

  it('keeps the stable connection ID and source when renaming or disabling', async () => {
    const { user, props } = mount();
    await user.click(screen.getByRole('button', { name: 'Edit Claude Work' }));
    expect(screen.getByRole('combobox', { name: 'Platform' })).toBeDisabled();
    expect(screen.getByRole('combobox', { name: 'Configuration' })).toBeDisabled();
    await user.clear(screen.getByRole('textbox', { name: 'Name' }));
    await user.type(screen.getByRole('textbox', { name: 'Name' }), 'Company Claude');
    await user.click(screen.getByRole('checkbox', { name: 'Available for new sessions' }));
    await user.click(screen.getByRole('button', { name: 'Save changes' }));
    expect(props.onSave).toHaveBeenCalledWith({ kind: 'claudecode', name: 'Company Claude', configuration: 'isolated', directory: work.directory, enabled: false }, 'pc-work');
  });

  it('requires confirmation before discarding dirty edits', async () => {
    const { user } = mount();
    await user.click(screen.getByRole('button', { name: 'Add platform' }));
    await user.type(screen.getByRole('textbox', { name: 'Name' }), 'Unsaved');
    await user.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.getByRole('dialog', { name: 'Discard unsaved changes?' })).toBeVisible();
    await user.click(screen.getByRole('button', { name: 'Keep editing' }));
    expect(screen.getByRole('textbox', { name: 'Name' })).toHaveValue('Unsaved');
    await user.click(screen.getByRole('button', { name: 'Cancel' }));
    await user.click(screen.getByRole('button', { name: 'Discard changes' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('closes a pristine dialog without discard confirmation', async () => {
    const { user } = mount();
    await user.click(screen.getByRole('button', { name: 'Add platform' }));
    await user.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('preserves input on a check failure and retries', async () => {
    const onCheck = vi.fn().mockRejectedValueOnce(new Error('Account probe failed')).mockResolvedValue(ready);
    const { user } = mount({ onCheck });
    await user.click(screen.getByRole('button', { name: 'Add platform' }));
    await user.type(screen.getByRole('textbox', { name: 'Name' }), 'Retry account');
    await user.click(screen.getByRole('button', { name: 'Check setup' }));
    expect(await screen.findByText('Account probe failed')).toBeVisible();
    expect(screen.getByRole('textbox', { name: 'Name' })).toHaveValue('Retry account');
    await user.click(screen.getByRole('button', { name: 'Check setup' }));
    expect(await screen.findByText('Account: verified@example.com')).toBeVisible();
  });

  it('shows sign-in instructions, can recheck, and saves incomplete setup explicitly', async () => {
    const check: ConnectionCheck = { status: 'sign-in-required', directory: '/isolated/codex', instructions: 'Run this command on this machine, then check again.', command: 'CODEX_HOME=/isolated/codex codex login', checks: [{ label: 'Account', ok: false, detail: 'Not signed in.' }] };
    const { user, props } = mount({ onCheck: vi.fn().mockResolvedValue(check) });
    await user.click(screen.getByRole('button', { name: 'Add platform' }));
    await user.type(screen.getByRole('textbox', { name: 'Name' }), 'Codex Personal');
    await user.click(screen.getByRole('button', { name: 'Check setup' }));
    expect(await screen.findByText('Sign-in required')).toBeVisible();
    expect(screen.getByText(check.command!)).toBeVisible();
    await user.click(screen.getByRole('button', { name: 'Check again' }));
    expect(props.onCheck).toHaveBeenCalledTimes(2);
    await user.click(screen.getByRole('button', { name: 'Save for later' }));
    expect(props.onSave).toHaveBeenCalledOnce();
  });

  it('keeps the dialog and check result when persistence fails', async () => {
    const { user } = mount({ onSave: vi.fn().mockRejectedValue(new Error('Disk full')) });
    await user.click(screen.getByRole('button', { name: 'Edit Claude Work' }));
    await user.click(screen.getByRole('button', { name: 'Check setup' }));
    expect(await screen.findByText('Account: verified@example.com')).toBeVisible();
    await user.click(screen.getByRole('button', { name: 'Save changes' }));
    expect(await screen.findByText('Disk full')).toBeVisible();
    expect(screen.getByRole('dialog')).toBeVisible();
  });

  it('prevents duplicate submissions and closing during save', async () => {
    let finish!: () => void;
    const onSave = vi.fn(() => new Promise<void>(resolve => { finish = resolve; }));
    const { user } = mount({ onSave });
    await user.click(screen.getByRole('button', { name: 'Edit Claude Work' }));
    await user.click(screen.getByRole('button', { name: 'Save changes' }));
    expect(screen.getByRole('button', { name: 'Saving…' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled();
    await user.keyboard('{Escape}');
    expect(screen.getByRole('dialog')).toBeVisible();
    expect(onSave).toHaveBeenCalledOnce();
    finish();
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
  });

  it('sets the default by ID and reports completion only after persistence', async () => {
    const { user, props } = mount();
    await user.click(screen.getByRole('button', { name: 'Set default' }));
    expect(props.onDefault).toHaveBeenCalledWith('pc-work');
    expect(await screen.findByRole('status')).toHaveTextContent('Claude Work is now the default.');
  });

  it('does not offer unready or disabled connections as defaults', () => {
    mount({ connections: [{ ...work, status: 'sign-in-required' }, { ...personal, enabled: false, isDefault: false }] });
    expect(screen.getByText('Disabled')).toBeVisible();
    for (const button of screen.getAllByRole('button', { name: 'Set default' })) expect(button).toBeDisabled();
  });

  it('keeps history and credentials in the removal confirmation and can cancel', async () => {
    const { user, props } = mount();
    await user.click(screen.getByRole('button', { name: 'Remove Claude Work' }));
    expect(screen.getByRole('dialog', { name: 'Remove Claude Work?' })).toHaveTextContent('Session history and native credentials are kept');
    await user.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(props.onRemove).not.toHaveBeenCalled();
    await user.click(screen.getByRole('button', { name: 'Remove Claude Work' }));
    await user.click(screen.getByRole('button', { name: 'Remove connection' }));
    expect(props.onRemove).toHaveBeenCalledWith('pc-work');
    expect(await screen.findByRole('status')).toHaveTextContent('Claude Work removed.');
  });

  it('shows mutation failure without optimistic success or losing rows', async () => {
    const { user } = mount({ onDefault: vi.fn().mockRejectedValue(new Error('Owner offline')) });
    await user.click(screen.getByRole('button', { name: 'Set default' }));
    expect(await screen.findByText('Owner offline')).toBeVisible();
    expect(screen.getByRole('button', { name: 'Claude Work' })).toBeVisible();
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
  });

  it('keeps last successful content on refresh failure and supports read-only hosts', async () => {
    const { user, props } = mount({ error: 'Refresh failed', readOnlyReason: 'Manage connections on the owning machine.' });
    expect(screen.getByRole('button', { name: 'Edit Claude Work' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Add platform' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Claude Work' })).toBeVisible();
    await user.click(screen.getByRole('button', { name: 'Retry' }));
    expect(props.onRetry).toHaveBeenCalledOnce();
  });

  it('disables creation until platform metadata is available', () => {
    mount({ platforms: [], connections: [] });
    expect(screen.getByRole('button', { name: 'Add platform' })).toBeDisabled();
  });

  it('disables only the selected named connection by ID', async () => {
    const onEnabledChange = vi.fn().mockResolvedValue(undefined);
    const { user } = mount({ onEnabledChange });
    await user.click(screen.getByRole('button', { name: 'Disable Claude Work' }));
    expect(onEnabledChange).toHaveBeenCalledExactlyOnceWith('pc-work', false);
    expect(await screen.findByRole('status')).toHaveTextContent('Claude Work disabled.');
  });

  it('can re-enable a disabled default without choosing another account', async () => {
    const onEnabledChange = vi.fn().mockResolvedValue(undefined);
    const { user, props } = mount({ connections: [{ ...personal, enabled: false }, work], onEnabledChange });
    expect(screen.getByText('Default (disabled)')).toBeVisible();
    await user.click(screen.getByRole('button', { name: 'Enable Claude Personal' }));
    expect(onEnabledChange).toHaveBeenCalledExactlyOnceWith('pc-personal', true);
    expect(props.onDefault).not.toHaveBeenCalled();
    expect(await screen.findByRole('status')).toHaveTextContent('Claude Personal enabled.');
  });

  it('keeps the connection unchanged when disabling fails', async () => {
    const { user } = mount({ onEnabledChange: vi.fn().mockRejectedValue(new Error('Could not disable connection')) });
    await user.click(screen.getByRole('button', { name: 'Disable Claude Work' }));
    expect(await screen.findByText('Could not disable connection')).toBeVisible();
    expect(screen.getByRole('button', { name: 'Disable Claude Work' })).toBeEnabled();
    expect(screen.queryByText('Claude Work disabled.')).not.toBeInTheDocument();
  });
});
