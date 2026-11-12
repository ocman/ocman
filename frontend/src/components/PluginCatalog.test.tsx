// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, useLocation, useNavigate } from 'react-router-dom';
import { beforeEach, expect, it, vi } from 'vitest';
import { renderWithQuery } from '../test/renderWithQuery';
import { api } from '../lib/api';
import { plugins, type PluginRegistration } from '../lib/plugins';
import { ConfirmProvider } from './ConfirmDialog';
import { PluginSettings } from './PluginSettings';

vi.mock('../lib/api', () => ({ api: { capabilities: vi.fn(), projects: vi.fn() } }));
vi.mock('../lib/plugins', () => ({
  plugins: { list: vi.fn(), discovery: vi.fn(), rescan: vi.fn(), mutate: vi.fn(), stderr: vi.fn() },
  hasConversationCapability: () => false, hasAgentTools: () => false,
}));

function registration(name: string, enabled = true, status = 'ready'): PluginRegistration {
  return { ownerId: 'local', approval: 'approval', checksum: 'checksum', enabled, removed: false, grants: [],
    description: { id: `org.example.${name.toLowerCase()}`, name, version: '1.0', scope: 'owner',
      settings: [{ key: 'url', label: 'Server URL', type: 'string' }] },
    configuration: { values: { url: 'https://example.com' }, secrets: {} }, health: { status, restartCount: 0 } };
}
function Location() {
  const location = useLocation();
  const navigate = useNavigate();
  return <><output aria-label="Current URL">{location.search}</output><button onClick={() => navigate(-1)}>Back</button></>;
}
function renderCatalog(query = '') {
  return renderWithQuery(<MemoryRouter initialEntries={[`/settings?group=plugins${query}`]}>
    <ConfirmProvider><PluginSettings /><Location /></ConfirmProvider>
  </MemoryRouter>);
}
beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(api.capabilities).mockResolvedValue({ platforms: [], hosts: ['local', 'remote'].map((remoteId) => ({
    remoteId, remoteName: remoteId, pluginManagement: true,
    capabilities: { gitDiff: false, worktrees: false, tmux: false, projects: false, sessionLaunch: false, judgeModel: false, mcpConfig: false },
  })) });
  vi.mocked(plugins.list).mockResolvedValue([registration('Forgejo'), registration('Slack', false, 'disabled'), registration('Sentry', true, 'unhealthy')]);
  vi.mocked(plugins.discovery).mockResolvedValue([]);
  vi.mocked(plugins.mutate).mockResolvedValue({});
  vi.mocked(plugins.stderr).mockResolvedValue({ stderr: '' });
});

it('shows compact rows instead of every plugin configuration', async () => {
  renderCatalog();
  expect(await screen.findByRole('link', { name: 'Forgejo' })).toBeVisible();
  expect(screen.getByRole('searchbox', { name: 'Search plugins' })).toBeVisible();
  expect(screen.queryByText('checksum')).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'Restart' })).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('link', { name: 'Forgejo' }));
  expect(await screen.findByRole('dialog', { name: 'Forgejo' })).toBeVisible();
  expect(screen.getByLabelText('Server URL')).toBeVisible();
  expect(screen.getByText(/Host: local/)).toBeVisible();
});

it('searches names and IDs, filters status and clears an empty search without losing host scope', async () => {
  renderCatalog('&ownerId=remote');
  await screen.findByRole('link', { name: 'Forgejo' });
  fireEvent.change(screen.getByRole('searchbox'), { target: { value: 'org.example.slack' } });
  expect(screen.getByRole('link', { name: 'Slack' })).toBeVisible();
  expect(screen.queryByRole('link', { name: 'Forgejo' })).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Needs attention 1' }));
  expect(screen.getByText('No matching plugins')).toBeVisible();
  fireEvent.click(screen.getByRole('button', { name: 'Clear filters' }));
  expect(screen.getByRole('link', { name: 'Forgejo' })).toBeVisible();
  expect(screen.getByLabelText('Current URL')).toHaveTextContent('ownerId=remote');
  expect(screen.getByLabelText('Current URL')).toHaveTextContent('group=plugins');
});

it('restores linked plugin tabs and closes with browser Back', async () => {
  renderCatalog('&ownerId=remote&plugin=org.example.sentry&pluginTab=diagnostics');
  expect(await screen.findByRole('dialog', { name: 'Sentry' })).toBeVisible();
  expect(screen.getByRole('tab', { name: 'Diagnostics' })).toHaveAttribute('aria-selected', 'true');
  expect(screen.getByRole('button', { name: 'Restart' })).toBeVisible();
  fireEvent.click(screen.getByRole('button', { name: 'Close plugin details' }));
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
  fireEvent.click(screen.getByRole('link', { name: 'Forgejo' }));
  await screen.findByRole('dialog', { name: 'Forgejo' });
  fireEvent.click(screen.getByRole('button', { name: 'Back' }));
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
});

it('warns before discarding configuration on close or tab change', async () => {
  renderCatalog('&plugin=org.example.forgejo');
  await screen.findByLabelText('Server URL');
  fireEvent.change(screen.getByLabelText('Server URL'), { target: { value: 'https://changed.com' } });
  fireEvent.click(screen.getByRole('button', { name: 'Close plugin details' }));
  expect(await screen.findByRole('dialog', { name: 'Discard unsaved changes?' })).toBeVisible();
  fireEvent.click(screen.getByRole('button', { name: 'Keep editing' }));
  expect(screen.getByLabelText('Server URL')).toHaveValue('https://changed.com');
  fireEvent.mouseDown(screen.getByRole('tab', { name: 'Diagnostics' }), { button: 0, ctrlKey: false });
  fireEvent.click(await screen.findByRole('button', { name: 'Discard changes' }));
  await waitFor(() => expect(screen.getByRole('tab', { name: 'Diagnostics' })).toHaveAttribute('aria-selected', 'true'));
});

it('paginates large lists and resets pagination when searching', async () => {
  vi.mocked(plugins.list).mockResolvedValue(Array.from({ length: 45 }, (_, index) => registration(`Plugin ${String(index).padStart(2, '0')}`)));
  renderCatalog();
  await screen.findByRole('link', { name: 'Plugin 00' });
  expect(screen.getAllByRole('link')).toHaveLength(20);
  expect(screen.getByRole('button', { name: 'Previous' })).toBeDisabled();
  fireEvent.click(screen.getByRole('button', { name: 'Next' }));
  expect(screen.getByRole('link', { name: 'Plugin 20' })).toBeVisible();
  expect(screen.getByLabelText('Current URL')).toHaveTextContent('pluginPage=2');
  fireEvent.click(screen.getByRole('button', { name: 'Next' }));
  expect(screen.getAllByRole('link')).toHaveLength(5);
  expect(screen.getByRole('button', { name: 'Next' })).toBeDisabled();
  fireEvent.click(screen.getByRole('button', { name: 'Previous' }));
  fireEvent.change(screen.getByRole('searchbox'), { target: { value: 'Plugin 01' } });
  expect(screen.getByRole('link', { name: 'Plugin 01' })).toBeVisible();
  expect(screen.queryByRole('button', { name: 'Next' })).not.toBeInTheDocument();
});

it('filters enabled and disabled registrations and flags missing grants or executables', async () => {
  const removed = registration('Removed', false, 'disabled'); removed.removed = true;
  const grants = registration('Grants'); grants.description.requestedGrants = ['ui.app'];
  vi.mocked(plugins.list).mockResolvedValue([removed, grants, registration('Starting', true, 'starting'), registration('Conflict', false, 'conflict')]);
  renderCatalog();
  await screen.findByRole('link', { name: 'Removed' });
  fireEvent.click(screen.getByRole('button', { name: 'Needs attention 3' }));
  expect(screen.getAllByRole('link')).toHaveLength(3);
  expect(screen.queryByRole('link', { name: 'Starting' })).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Disabled 2' }));
  expect(screen.getAllByRole('link')).toHaveLength(2);
  fireEvent.click(screen.getByRole('button', { name: 'Enabled 2' }));
  expect(screen.getByRole('link', { name: 'Starting' })).toBeVisible();
});

it('opens rows except while selecting text and restores the list when dismissed', async () => {
  renderCatalog();
  const link = await screen.findByRole('link', { name: 'Forgejo' });
  const selection = vi.spyOn(window, 'getSelection').mockReturnValue({ toString: () => 'Forgejo' } as Selection);
  fireEvent.click(link.closest('tr')!);
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  selection.mockRestore();
  fireEvent.click(link.closest('tr')!);
  expect(screen.getByRole('dialog', { name: 'Forgejo' })).toBeVisible();
  fireEvent.keyDown(window, { key: 'Escape' });
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
});

it('handles stale plugin links and invalid tabs without choosing a different plugin', async () => {
  renderCatalog('&plugin=missing');
  expect(await screen.findByText('Plugin is no longer available')).toBeVisible();
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
  expect(screen.getByLabelText('Current URL')).not.toHaveTextContent('plugin=');
});

it('uses settings for unknown detail tabs', async () => {
  renderCatalog('&plugin=org.example.forgejo&pluginTab=unknown');
  expect(await screen.findByLabelText('Server URL')).toBeVisible();
  expect(screen.getByRole('tab', { name: 'Settings' })).toHaveAttribute('aria-selected', 'true');
});

it('preserves unsaved secrets outside the URL and closes after explicit discard', async () => {
  const plugin = registration('Secret');
  plugin.description.settings = [{ key: 'token', label: 'Token', type: 'string', secret: true }];
  vi.mocked(plugins.list).mockResolvedValue([plugin]);
  renderCatalog('&plugin=org.example.secret');
  await screen.findByLabelText('Token');
  fireEvent.change(screen.getByLabelText('Token'), { target: { value: 'never-in-url' } });
  expect(screen.getByLabelText('Current URL')).not.toHaveTextContent('never-in-url');
  fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
  fireEvent.click(await screen.findByRole('button', { name: 'Discard changes' }));
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
});

it('shows plugin branding and a header toggle that still reviews enabling permissions', async () => {
  const plugin = registration('Forgejo', false, 'disabled');
  plugin.description.icon = { bootstrap: 'git' };
  plugin.description.requestedGrants = ['context.project'];
  vi.mocked(plugins.list).mockResolvedValue([plugin]);
  renderCatalog('&plugin=org.example.forgejo');
  const dialog = await screen.findByRole('dialog', { name: 'Forgejo' });
  expect(dialog.querySelector('.bi-git')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('checkbox', { name: 'Enable Forgejo' }));
  await waitFor(() => expect(screen.getByRole('tab', { name: 'Permissions' })).toHaveAttribute('aria-selected', 'true'));
  expect(screen.getByText(/Approve checksum/)).toBeVisible();
  expect(plugins.mutate).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole('button', { name: 'Approve grants and enable' }));
  await waitFor(() => expect(plugins.mutate).toHaveBeenCalledWith('local', plugin.description.id, 'enable', {
    approval: 'approval', grants: ['context.project'],
  }));
});

it('disables immediately from the drawer header and waits for persistence', async () => {
  let resolve!: (value: unknown) => void;
  vi.mocked(plugins.mutate).mockReturnValue(new Promise((done) => { resolve = done; }));
  renderCatalog('&plugin=org.example.forgejo');
  const toggle = await screen.findByRole('checkbox', { name: 'Enable Forgejo' });
  expect(toggle).toBeChecked();
  fireEvent.click(toggle);
  expect(plugins.mutate).toHaveBeenCalledWith('local', 'org.example.forgejo', 'disable', undefined);
  expect(toggle).toBeDisabled();
  expect(toggle).toBeChecked();
  vi.mocked(plugins.list).mockResolvedValue([registration('Forgejo', false, 'disabled')]);
  resolve({});
  await waitFor(() => expect(toggle).not.toBeChecked());
});

it('opens a dedicated logs panel straight from the drawer header', async () => {
  vi.mocked(plugins.stderr).mockResolvedValue({ stderr: '10:42 Connected to server' });
  renderCatalog('&ownerId=remote&plugin=org.example.forgejo');
  fireEvent.click(await screen.findByRole('button', { name: 'View logs' }));
  await waitFor(() => expect(screen.getByLabelText('Recent stderr')).toHaveTextContent('10:42 Connected to server'));
  expect(screen.getByRole('button', { name: 'Refresh logs' })).toBeVisible();
  expect(screen.getByRole('button', { name: 'Copy logs' })).toBeEnabled();
  expect(plugins.stderr).toHaveBeenCalledWith('remote', 'org.example.forgejo');
});

it('keeps edits and the toggle off when enabling review is cancelled', async () => {
  vi.mocked(plugins.list).mockResolvedValue([registration('Forgejo', false, 'disabled')]);
  renderCatalog('&plugin=org.example.forgejo');
  await screen.findByLabelText('Server URL');
  fireEvent.change(screen.getByLabelText('Server URL'), { target: { value: 'https://unsaved.example.com' } });
  fireEvent.click(screen.getByRole('checkbox', { name: 'Enable Forgejo' }));
  fireEvent.click(await screen.findByRole('button', { name: 'Keep editing' }));
  expect(screen.getByLabelText('Server URL')).toHaveValue('https://unsaved.example.com');
  expect(screen.getByRole('checkbox', { name: 'Enable Forgejo' })).not.toBeChecked();
  expect(screen.queryByText(/Approve checksum/)).not.toBeInTheDocument();
  expect(plugins.mutate).not.toHaveBeenCalled();
});

it('keeps a failed disable visible without claiming the plugin is off', async () => {
  vi.mocked(plugins.mutate).mockRejectedValue(new Error('private detail'));
  renderCatalog('&plugin=org.example.forgejo');
  fireEvent.click(await screen.findByRole('checkbox', { name: 'Enable Forgejo' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('Could not disable plugin');
  expect(screen.getByRole('checkbox', { name: 'Enable Forgejo' })).toBeChecked();
  expect(screen.queryByText('private detail')).not.toBeInTheDocument();
});

it.each(['conflict', 'removed'])('blocks the header enable toggle for a %s registration', async (status) => {
  const plugin = registration('Forgejo', false, status);
  plugin.removed = status === 'removed';
  vi.mocked(plugins.list).mockResolvedValue([plugin]);
  renderCatalog('&plugin=org.example.forgejo');
  expect(await screen.findByRole('checkbox', { name: 'Enable Forgejo' })).toBeDisabled();
});
