// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { api } from '../lib/api';
import { plugins, type PluginRegistration } from '../lib/plugins';
import type { HostCapabilityEntry } from '../lib/api.types';
import { PluginSettings } from './PluginSettings';

vi.mock('../lib/api', () => ({ api: { capabilities: vi.fn(), projects: vi.fn() } }));
vi.mock('../lib/plugins', () => ({
  plugins: { list: vi.fn(), discovery: vi.fn(), rescan: vi.fn(), mutate: vi.fn(), stderr: vi.fn(), backlog: vi.fn(), projectCatalog: vi.fn() },
  // The example plugin serves no conversation, so its delivery backlog is not
  // part of the card; PluginDeliveryBacklog.test.tsx covers that on its own.
  hasConversationCapability: () => false,
}));

const host = (remoteId: string, pluginManagement = true): HostCapabilityEntry => ({
  remoteId, remoteName: remoteId, pluginManagement,
  capabilities: { gitDiff: false, worktrees: false, tmux: false, projects: false, whisper: false, opencodeLaunch: false },
});
let plugin: PluginRegistration;
beforeEach(() => {
  vi.resetAllMocks();
  plugin = {
    ownerId: 'local', approval: 'reviewed-registration', checksum: 'abc123', enabled: false, removed: false, grants: [],
    description: { id: 'org.example.test', name: 'Example', version: '1.2.3', scope: 'owner',
      capabilities: [{ name: 'action', version: { major: 1, minor: 0 } }], requestedGrants: ['context.project'],
      settings: [
        { key: 'mode', type: 'string', label: 'Mode', enum: ['good', 'bad'], default: 'good' },
        { key: 'token', type: 'string', label: 'Token', secret: true },
        { key: 'name', type: 'string', label: 'Name' },
        { key: 'active', type: 'boolean', label: 'Active' },
        { key: 'count', type: 'integer', label: 'Count' },
        { key: 'ratio', type: 'number', label: 'Ratio' },
      ] },
    configuration: { values: { mode: 'good' }, secrets: { token: true } },
    health: { status: 'disabled', restartCount: 0 },
  };
  vi.mocked(api.capabilities).mockResolvedValue({ platforms: [], hosts: [host('local'), host('remote'), host('old', false)] });
  vi.mocked(api.projects).mockResolvedValue([]);
  vi.mocked(plugins.list).mockImplementation(async () => [structuredClone(plugin)]);
  vi.mocked(plugins.discovery).mockResolvedValue([]);
  vi.mocked(plugins.rescan).mockResolvedValue([]);
  vi.mocked(plugins.mutate).mockResolvedValue({});
  vi.mocked(plugins.stderr).mockResolvedValue({ stderr: '' });
});

async function open() {
  render(<PluginSettings />);
  return screen.findByRole('region', { name: 'Example plugin' });
}
const click = (name: string) => fireEvent.click(screen.getByRole('button', { name }));

it('shows rejected discovery diagnostics for the selected owner and clears them after rescan', async () => {
  vi.mocked(plugins.discovery).mockResolvedValue([{ filename: 'ocman-plugin-broken', error: 'invalid plugin message' }]);
  await open();
  expect(screen.getByRole('alert')).toHaveTextContent('ocman-plugin-broken: invalid plugin message');
  expect(plugins.discovery).toHaveBeenCalledWith('local', expect.any(AbortSignal));
  vi.mocked(plugins.discovery).mockResolvedValue([]);
  click('Rescan plugins');
  await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument());
});

it('shows owner and catalog loading, an empty state, and rescan results', async () => {
  let resolve!: (p: PluginRegistration[]) => void;
  vi.mocked(plugins.list).mockReturnValue(new Promise((r) => { resolve = r; }));
  render(<PluginSettings />);
  expect(screen.getByRole('status')).toHaveTextContent('Loading plugin owners');
  expect(await screen.findByText('Loading plugins…')).toBeVisible();
  expect(screen.getByRole('button', { name: 'Rescan plugins' })).toBeDisabled();
  await act(async () => resolve([]));
  expect(screen.getByRole('status')).toHaveTextContent('No plugins discovered');
  vi.mocked(plugins.rescan).mockResolvedValue([plugin]);
  click('Rescan plugins');
  expect(await screen.findByRole('region', { name: 'Example plugin' })).toBeVisible();
  expect(plugins.rescan).toHaveBeenCalledWith('local');
});

it('displays metadata, conflicts and removed executables without allowing enable', async () => {
  plugin.health = { status: 'conflict', restartCount: 3, lastError: 'Duplicate immutable ID' };
  plugin.removed = true;
  const card = await open();
  for (const text of ['org.example.test', '1.2.3', 'abc123', 'action v1.0', 'owner', 'Duplicate immutable ID', 'Configured']) {
    expect(within(card).getByText(text)).toBeVisible();
  }
  expect(within(card).getByText(/Disabled · conflict · Executable removed/)).toBeVisible();
  expect(screen.getByRole('button', { name: 'Enable' })).toBeDisabled();
  expect(screen.getByRole('button', { name: 'Restart' })).toBeDisabled();
});

it('requires explicit grant and checksum review before enabling', async () => {
  await open();
  click('Enable');
  expect(plugins.mutate).not.toHaveBeenCalled();
  expect(screen.getByText(/Approve checksum/)).toHaveTextContent('abc123');
  click('Cancel approval');
  expect(screen.queryByRole('button', { name: 'Approve grants and enable' })).not.toBeInTheDocument();
  click('Enable');
  plugin.enabled = true;
  plugin.grants = ['context.project'];
  plugin.health.status = 'ready';
  click('Approve grants and enable');
  await waitFor(() => expect(plugins.mutate).toHaveBeenCalledWith('local', plugin.description.id, 'enable', { approval: 'reviewed-registration', grants: ['context.project'] }));
  expect(await screen.findByText('Enabled · ready')).toBeVisible();
});

it('supports enabling without grants and settings', async () => {
  plugin.description.requestedGrants = undefined;
  plugin.description.capabilities = undefined;
  plugin.description.settings = undefined;
  plugin.configuration = { values: null, secrets: null };
  await open();
  click('Configure');
  expect(screen.getByText('No configuration settings.')).toBeVisible();
  click('Enable');
  expect(screen.getByText('No grants requested.')).toBeVisible();
  click('Approve grants and enable');
  await waitFor(() => expect(plugins.mutate).toHaveBeenCalledWith('local', plugin.description.id, 'enable', { approval: 'reviewed-registration', grants: [] }));
});

it.each(['Retry', 'Restart', 'Disable', 'Revoke grants'])('routes %s to the selected owner', async (action) => {
  plugin.enabled = true;
  plugin.grants = ['context.project'];
  plugin.health = { status: 'unhealthy', restartCount: 4, lastError: 'Readiness failed' };
  await open();
  fireEvent.change(screen.getByRole('combobox', { name: 'Plugin owner' }), { target: { value: 'remote' } });
  await screen.findByRole('region', { name: 'Example plugin' });
  expect(screen.getByText('Enabled · unhealthy')).toBeVisible();
  click(action);
  await waitFor(() => expect(plugins.mutate).toHaveBeenCalledWith('remote', plugin.description.id,
    action === 'Revoke grants' ? 'grants' : action.toLowerCase(), action === 'Revoke grants' ? { grants: [] } : undefined));
});

it('keeps secrets write-only, omits unchanged secrets, and sends typed configuration', async () => {
  await open();
  click('Configure');
  expect(screen.getByLabelText('Token')).toHaveAttribute('type', 'password');
  expect(screen.getByLabelText('Token')).toHaveValue('');
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'example' } });
  fireEvent.change(screen.getByLabelText('Active'), { target: { value: 'false' } });
  fireEvent.change(screen.getByLabelText('Count'), { target: { value: '2' } });
  fireEvent.change(screen.getByLabelText('Ratio'), { target: { value: '1.5' } });
  click('Save configuration');
  await waitFor(() => expect(plugins.mutate).toHaveBeenCalledWith('local', plugin.description.id, 'configuration', {
    values: { mode: 'good', name: 'example', active: false, count: 2, ratio: 1.5 }, secrets: {},
  }));
  await waitFor(() => expect(screen.queryByRole('form')).not.toBeInTheDocument());
  click('Configure');
  fireEvent.change(screen.getByLabelText('Token'), { target: { value: 'write-only-value' } });
  click('Save configuration');
  await waitFor(() => expect(plugins.mutate).toHaveBeenLastCalledWith('local', plugin.description.id, 'configuration', { values: { mode: 'good' }, secrets: { token: 'write-only-value' } }));
  expect(screen.queryByDisplayValue('write-only-value')).not.toBeInTheDocument();
});

it('uses project-scoped selectors for Slack configuration', async () => {
  plugin.description.id = 'org.ocman.slack';
  plugin.description.settings = [
    { key: 'project', type: 'string', label: 'Project directory', required: true },
    { key: 'agent', type: 'string', label: 'Agent (optional)' },
    { key: 'model', type: 'string', label: 'Model (optional provider/model)' },
  ];
  vi.mocked(api.projects).mockResolvedValue([{ directory: '/repo', sessionCount: 0, messageCount: 0, totalTokensIn: 0, totalTokensOut: 0, lastUsed: 0 }]);
  vi.mocked(plugins.projectCatalog).mockResolvedValue({ agents: ['build'], models: ['openai/gpt-5'] });
  await open();
  click('Configure');
  await waitFor(() => expect(api.projects).toHaveBeenCalled());
  fireEvent.click(screen.getByRole('combobox', { name: 'Project directory' }));
  fireEvent.click(await screen.findByRole('option', { name: '/repo' }));
  await waitFor(() => expect(plugins.projectCatalog).toHaveBeenCalledWith('local', '/repo', expect.any(AbortSignal)));
  fireEvent.click(screen.getByRole('combobox', { name: 'Agent (optional)' }));
  fireEvent.click(screen.getByRole('option', { name: 'build' }));
  fireEvent.click(screen.getByRole('combobox', { name: 'Model (optional provider/model)' }));
  fireEvent.click(screen.getByRole('option', { name: 'openai/gpt-5' }));
  click('Save configuration');
  await waitFor(() => expect(plugins.mutate).toHaveBeenCalledWith('local', 'org.ocman.slack', 'configuration', {
    values: { mode: 'good', project: '/repo', agent: 'build', model: 'openai/gpt-5' }, secrets: {},
  }));
});

it('clears optional secrets explicitly and can undo clearing or editing', async () => {
  await open();
  click('Configure');
  const token = screen.getByLabelText('Token');
  fireEvent.change(token, { target: { value: 'temporary' } });
  fireEvent.change(token, { target: { value: '' } });
  fireEvent.click(screen.getByLabelText('Clear Token'));
  fireEvent.click(screen.getByLabelText('Clear Token'));
  fireEvent.click(screen.getByLabelText('Clear Token'));
  click('Save configuration');
  await waitFor(() => expect(plugins.mutate).toHaveBeenCalledWith('local', plugin.description.id, 'configuration', { values: { mode: 'good' }, secrets: { token: '' } }));
});

it('omits an optional enum after clearing its selection', async () => {
  plugin.description.settings![0].default = undefined;
  await open();
  click('Configure');
  fireEvent.change(screen.getByLabelText('Mode'), { target: { value: '' } });
  click('Save configuration');
  await waitFor(() => expect(plugins.mutate).toHaveBeenCalledWith('local', plugin.description.id, 'configuration', { values: {}, secrets: {} }));
});

it('shows configuration activation/rollback errors, reloads persisted values, and erases submitted secrets', async () => {
  vi.mocked(plugins.mutate).mockRejectedValue(new Error('private-candidate'));
  await open();
  click('Configure');
  fireEvent.change(screen.getByLabelText('Mode'), { target: { value: 'bad' } });
  fireEvent.change(screen.getByLabelText('Token'), { target: { value: 'private-candidate' } });
  click('Save configuration');
  expect(await screen.findByRole('alert')).toHaveTextContent('Configuration was not activated');
  await waitFor(() => expect(screen.getByRole('button', { name: 'Configure' })).toBeEnabled());
  expect(screen.queryByText(/private-candidate/)).not.toBeInTheDocument();
  click('Configure');
  expect(screen.getByLabelText('Mode')).toHaveValue('good');
  expect(screen.getByLabelText('Token')).toHaveValue('');
});

it('renders defaults, required secrets and optional boolean/numeric omissions', async () => {
  plugin.configuration = { values: {}, secrets: {} };
  plugin.description.settings![1].required = true;
  await open();
  expect(screen.getByText('Not configured')).toBeVisible();
  click('Configure');
  expect(screen.getByLabelText('Mode')).toHaveValue('good');
  expect(screen.getByLabelText('Token')).toBeRequired();
  expect(screen.queryByLabelText('Clear Token')).not.toBeInTheDocument();
  fireEvent.change(screen.getByLabelText('Token'), { target: { value: 'new-secret' } });
  for (const [label, value] of [['Active', 'true'], ['Count', '1']]) {
    fireEvent.change(screen.getByLabelText(label), { target: { value } });
    fireEvent.change(screen.getByLabelText(label), { target: { value: '' } });
  }
  click('Save configuration');
  await waitFor(() => expect(plugins.mutate).toHaveBeenCalledWith('local', plugin.description.id, 'configuration', { values: { mode: 'good' }, secrets: { token: 'new-secret' } }));
});

it('shows bounded text-only stderr, empty logs, refresh and errors', async () => {
  await open();
  click('Load recent stderr');
  expect(await screen.findByLabelText('Recent stderr')).toHaveTextContent('No recent stderr.');
  vi.mocked(plugins.stderr).mockResolvedValue({ stderr: 'old'.repeat(50000) + '<script>recent</script>' });
  click('Load recent stderr');
  await waitFor(() => expect(screen.getByLabelText('Recent stderr')).toHaveTextContent('<script>recent</script>'));
  expect(screen.getByLabelText('Recent stderr').textContent).toHaveLength(128 * 1024);
  expect(screen.getByLabelText('Recent stderr').querySelector('script')).toBeNull();
  vi.mocked(plugins.stderr).mockRejectedValue(new Error('offline'));
  click('Load recent stderr');
  expect(await screen.findByRole('alert')).toHaveTextContent('Could not load recent stderr');
});

it('requires disabled state and explicit confirmation to permanently remove data', async () => {
  plugin.enabled = true;
  await open();
  expect(screen.getByRole('button', { name: 'Remove data' })).toBeDisabled();
  plugin.enabled = false;
  click('Refresh health');
  await waitFor(() => expect(screen.getByRole('button', { name: 'Remove data' })).toBeEnabled());
  click('Remove data');
  click('Cancel removal');
  expect(plugins.mutate).not.toHaveBeenCalled();
  click('Remove data');
  expect(screen.getByText(/This cannot be undone/)).toBeVisible();
  vi.mocked(plugins.list).mockResolvedValue([]);
  click('Confirm permanent removal');
  expect(await screen.findByText('No plugins discovered.')).toBeVisible();
  expect(plugins.mutate).toHaveBeenCalledWith('local', plugin.description.id, 'remove-data', undefined);
});

it('gates unsupported owners, clears old data on switching and ignores stale responses', async () => {
  let resolve!: (p: PluginRegistration[]) => void;
  vi.mocked(plugins.list).mockImplementation((owner) => owner === 'local' ? new Promise((r) => { resolve = r; }) : Promise.resolve([]));
  render(<PluginSettings />);
  await screen.findByText('Loading plugins…');
  fireEvent.change(screen.getByLabelText('Plugin owner'), { target: { value: 'remote' } });
  expect(await screen.findByText('No plugins discovered.')).toBeVisible();
  await act(async () => resolve([plugin]));
  expect(screen.queryByRole('region')).not.toBeInTheDocument();
  fireEvent.change(screen.getByLabelText('Plugin owner'), { target: { value: 'old' } });
  expect(screen.getByRole('status')).toHaveTextContent('unavailable');
  expect(plugins.list).not.toHaveBeenCalledWith('old', expect.anything());
});

it('handles owner disconnect and capability refresh without falling back to local', async () => {
  await open();
  fireEvent.change(screen.getByLabelText('Plugin owner'), { target: { value: 'remote' } });
  await screen.findByRole('region');
  vi.mocked(plugins.list).mockRejectedValue(new Error('offline'));
  click('Refresh health');
  expect(await screen.findByRole('alert')).toHaveTextContent('Could not load plugins for this owner');
  expect(screen.queryByRole('region')).not.toBeInTheDocument();
  vi.mocked(api.capabilities).mockResolvedValue({ platforms: [], hosts: [host('local')] });
  click('Refresh owners');
  expect(await screen.findByText('Plugin management is unavailable on this owner.')).toBeVisible();
  expect(screen.getByLabelText('Plugin owner')).toHaveValue('remote');
});

it('supports retrying owner and catalog failures and sanitizes mutation errors', async () => {
  vi.mocked(api.capabilities).mockRejectedValueOnce(new Error('offline'));
  render(<PluginSettings />);
  expect(await screen.findByRole('alert')).toHaveTextContent('Could not load plugin owners');
  vi.mocked(plugins.list).mockRejectedValueOnce(new Error('offline'));
  click('Refresh owners');
  expect(await screen.findByText(/Could not load plugins for this owner/)).toBeVisible();
  click('Refresh health');
  await screen.findByRole('region');
  vi.mocked(plugins.mutate).mockRejectedValue(new Error('sensitive error body'));
  click('Enable'); click('Approve grants and enable');
  expect(await screen.findByRole('alert')).toHaveTextContent('Could not enable plugin');
  expect(screen.queryByText(/sensitive error/)).not.toBeInTheDocument();
});
