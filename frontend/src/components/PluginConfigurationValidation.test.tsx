// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { api } from '../lib/api';
import type { PluginRegistration } from '../lib/plugins';
import { renderWithQuery } from '../test/renderWithQuery';
import { PluginSettings } from './PluginSettings';
import { MemoryRouter } from 'react-router-dom';
import { ConfirmProvider } from './ConfirmDialog';

let plugin: PluginRegistration;
let posts: { path: string; body: unknown }[];
let mutationStatus: number;
let onMutation: (() => void) | undefined;
beforeEach(() => {
  posts = [];
  mutationStatus = 200;
  onMutation = undefined;
  plugin = {
    ownerId: 'local', approval: 'approved', checksum: 'fixture', enabled: false, removed: false, grants: [],
    description: { id: 'org.example.output', name: 'Output', version: '1', scope: 'owner', settings: [
      { key: 'sourcePassword', label: 'ocman password', type: 'string', secret: true, required: true },
      { key: 'count', label: 'Count', type: 'integer' },
      { key: 'mode', label: 'Mode', type: 'string', enum: ['serial', 'http'] },
    ] },
    configuration: { values: { count: 1, mode: 'serial' }, secrets: { sourcePassword: false } },
    health: { status: 'disabled', restartCount: 0 },
  };
  vi.spyOn(api, 'capabilities').mockResolvedValue({ platforms: [], hosts: [{
    remoteId: 'local', remoteName: 'local', pluginManagement: true,
    capabilities: { gitDiff: false, worktrees: false, tmux: false, projects: false, sessionLaunch: false, judgeModel: false, mcpConfig: false },
  }] });
  vi.stubGlobal('fetch', vi.fn(async (input: string, init?: RequestInit) => {
    const path = new URL(input, 'http://fixture').pathname;
    if (init?.method === 'POST') {
      posts.push({ path, body: JSON.parse(String(init.body)) });
      onMutation?.();
      if (mutationStatus !== 200) return new Response('private-raw-server-diagnostic', { status: mutationStatus });
      return Response.json({});
    }
    if (path === '/api/plugins') return Response.json([plugin]);
    if (path === '/api/plugins/discovery') return Response.json([]);
    throw new Error(`Unexpected fixture request: ${path}`);
  }));
});
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

const click = (name: string) => {
  if (name === 'Configure') {
    const tab = screen.getByRole('tab', { name: 'Settings' });
    if (tab.getAttribute('aria-selected') !== 'true') fireEvent.mouseDown(tab, { button: 0, ctrlKey: false });
  } else if (name === 'Enable') fireEvent.click(screen.getByRole('checkbox', { name: 'Enable Output' }));
  else fireEvent.click(screen.getByRole('button', { name }));
};
async function open() {
  renderWithQuery(<MemoryRouter initialEntries={[`/settings?tab=plugins&plugin=${plugin.description.id}&pluginTab=permissions`]}><ConfirmProvider><PluginSettings /></ConfirmProvider></MemoryRouter>);
  await screen.findByRole('region', { name: 'Output plugin' });
}

it('opens and focuses the missing required secret instead of attempting enable or blaming health', async () => {
  await open();
  click('Enable');
  const password = screen.getByLabelText('ocman password');
  expect(password).toHaveAttribute('aria-invalid', 'true');
  expect(password).toHaveAccessibleDescription('ocman password is required and is not configured.');
  expect(password).toHaveFocus();
  expect(screen.getByRole('form')).toHaveTextContent('Fix the highlighted settings');
  expect(screen.getByRole('button', { name: 'Save configuration' })).toBeDisabled();
  expect(screen.queryByText(/Check its health/)).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'Approve grants and enable' })).not.toBeInTheDocument();
  expect(posts).toEqual([]);
  click('Refresh health');
  await waitFor(() => expect(screen.getByRole('button', { name: 'Refresh health' })).toBeEnabled());
  expect(password).toHaveAttribute('aria-invalid', 'true');
  click('Configure');
  click('Configure');
  expect(screen.getByLabelText('ocman password')).toHaveFocus();
  expect(screen.getByLabelText('ocman password')).toHaveAccessibleDescription('ocman password is required and is not configured.');
});

it('blocks invalid public values at the save caller, explains them without echoing values and clears errors on repair', async () => {
  plugin.configuration.secrets = { sourcePassword: true };
  plugin.configuration.values = { count: 1.5, mode: 'private-raw-invalid-value' };
  await open();
  click('Configure');
  expect(screen.getByLabelText('Count')).toHaveAttribute('aria-invalid', 'true');
  expect(screen.getByLabelText('Count')).toHaveAccessibleDescription('Enter a whole number.');
  expect(screen.getByLabelText('Count')).toHaveFocus();
  expect(screen.getByLabelText('Mode')).toHaveAccessibleDescription('Choose one of the declared options.');
  expect(screen.getByLabelText('Mode')).toHaveAttribute('aria-invalid', 'true');
  expect(screen.getByRole('button', { name: 'Save configuration' })).toBeDisabled();
  fireEvent.submit(screen.getByRole('form'));
  expect(posts).toEqual([]);
  expect(document.body).not.toHaveTextContent('private-raw-invalid-value');
  fireEvent.change(screen.getByLabelText('Count'), { target: { value: '2' } });
  fireEvent.change(screen.getByLabelText('Mode'), { target: { value: 'http' } });
  expect(screen.getByLabelText('Count')).not.toHaveAttribute('aria-invalid', 'true');
  expect(screen.getByLabelText('Mode')).not.toHaveAttribute('aria-describedby');
  click('Save configuration');
  await waitFor(() => expect(posts).toHaveLength(1));
  expect(posts[0].body).toEqual({ values: { count: 2, mode: 'http' }, secrets: {} });
});

it('accepts blank for an already configured required secret on save and enable', async () => {
  plugin.configuration.secrets = { sourcePassword: true };
  await open();
  click('Configure');
  expect(screen.getByLabelText('ocman password')).toHaveValue('');
  expect(screen.getByLabelText('ocman password')).not.toHaveAttribute('aria-invalid', 'true');
  click('Save configuration');
  await screen.findByText('Configuration saved.');
  expect(posts[0].body).toEqual({ values: { count: 1, mode: 'serial' }, secrets: {} });
  click('Enable');
  click('Approve grants and enable');
  await waitFor(() => expect(posts).toHaveLength(2));
  expect(posts[1]).toEqual({ path: '/api/plugins/org.example.output/enable', body: { approval: 'approved', grants: [] } });
});

it('retains missing-secret guidance through a changed configuration remount without copying a draft password', async () => {
  await open();
  click('Enable');
  const field = screen.getByLabelText('ocman password');
  fireEvent.change(field, { target: { value: 'fixture-draft-only' } });
  expect(field).not.toHaveAttribute('aria-invalid', 'true');
  plugin.configuration.values = { count: 2, mode: 'http' };
  click('Refresh health');
  await waitFor(() => expect(screen.getByLabelText('Count')).toHaveValue(2));
  expect(screen.getByLabelText('ocman password')).toHaveValue('');
  expect(screen.getByLabelText('ocman password')).toHaveAttribute('aria-invalid', 'true');
  expect(screen.getByLabelText('ocman password')).toHaveFocus();
  expect(screen.getByLabelText('ocman password')).toHaveAccessibleDescription('ocman password is required and is not configured.');
  expect(document.body).not.toHaveTextContent('fixture-draft-only');
  expect(posts).toEqual([]);
});

it('rechecks saved settings at approval after an async refresh makes them invalid', async () => {
  plugin.configuration.secrets = { sourcePassword: true };
  await open();
  click('Enable');
  plugin.configuration.secrets = { sourcePassword: false };
  click('Refresh health');
  await waitFor(() => expect(screen.getByRole('button', { name: 'Refresh health' })).toBeEnabled());
  click('Approve grants and enable');
  expect(screen.getByLabelText('ocman password')).toHaveAttribute('aria-invalid', 'true');
  expect(screen.getByLabelText('ocman password')).toHaveFocus();
  expect(posts).toEqual([]);
});

it('focuses and describes invalid list and multiline fields without exposing invalid values in messages', async () => {
  plugin.configuration.secrets = { sourcePassword: true };
  plugin.description.settings = [
    { key: 'hosts', label: 'Hosts', type: 'string', list: true },
    { key: 'notes', label: 'Notes', type: 'string', multiline: true, required: true },
  ];
  plugin.configuration.values = { hosts: 'invalid-list-fixture' };
  await open();
  click('Configure');
  const hosts = screen.getByRole('group', { name: 'Hosts' });
  expect(hosts).toHaveAttribute('aria-invalid', 'true');
  expect(hosts).toHaveAccessibleDescription('Enter a list of text values.');
  expect(hosts).toHaveFocus();
  expect(screen.getByLabelText('Notes')).toHaveAccessibleDescription('Notes is required.');
  expect(document.body).not.toHaveTextContent('invalid-list-fixture');
  click('Add Hosts');
  fireEvent.change(screen.getByLabelText('Notes'), { target: { value: 'notes' } });
  expect(hosts).not.toHaveAttribute('aria-invalid', 'true');
  expect(screen.getByRole('button', { name: 'Save configuration' })).toBeEnabled();
});

it('propagates validation and focus to the shared searchable project field', async () => {
  plugin.description.id = 'org.ocman.slack';
  plugin.description.settings = [{ key: 'project', label: 'Project', type: 'string', required: true }];
  plugin.configuration.values = {};
  vi.spyOn(api, 'projects').mockResolvedValue([]);
  await open();
  click('Enable');
  const project = screen.getByRole('combobox', { name: 'Project' });
  expect(project).toHaveAttribute('aria-invalid', 'true');
  expect(project).toHaveAccessibleDescription('Project is required.');
  expect(project).toHaveFocus();
  expect(posts).toEqual([]);
});

it('does not move focus away while a user repairs an invalid field', async () => {
  plugin.configuration.values = { count: 1.5, mode: 'serial' };
  await open();
  click('Configure');
  const password = screen.getByLabelText('ocman password');
  expect(password).toHaveFocus();
  fireEvent.change(password, { target: { value: 'fixture-draft' } });
  expect(password).toHaveFocus();
  fireEvent.submit(screen.getByRole('form'));
  expect(screen.getByLabelText('Count')).toHaveFocus();
  expect(posts).toEqual([]);
});

it('opens configuration after an owner rejects enable validation and focuses missing fields from refresh', async () => {
  plugin.configuration.secrets = { sourcePassword: true };
  mutationStatus = 400;
  onMutation = () => { plugin.configuration.secrets = { sourcePassword: false }; };
  await open();
  click('Enable');
  click('Approve grants and enable');
  const field = await screen.findByLabelText('ocman password');
  await waitFor(() => expect(field).toHaveFocus());
  expect(field).toHaveAccessibleDescription('ocman password is required and is not configured.');
  expect(screen.getByRole('alert')).toHaveTextContent('configuration or grants');
  expect(screen.queryByText(/Check its health|private-raw-server-diagnostic/)).not.toBeInTheDocument();
});

it('does not describe a server validation rejection as an activation rollback', async () => {
  mutationStatus = 400;
  await open();
  click('Configure');
  fireEvent.change(screen.getByLabelText('ocman password'), { target: { value: 'fixture-only-candidate' } });
  click('Save configuration');
  expect(await screen.findByRole('alert')).toHaveTextContent('Configuration was rejected');
  expect(screen.getByRole('alert')).not.toHaveTextContent(/activation|restore|private-raw/);
  expect(screen.getByLabelText('ocman password')).toHaveValue('');
  await waitFor(() => expect(screen.getByLabelText('ocman password')).toHaveFocus());
  expect(screen.queryByDisplayValue('fixture-only-candidate')).not.toBeInTheDocument();
});
