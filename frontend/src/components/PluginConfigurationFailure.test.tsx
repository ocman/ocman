// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { api } from '../lib/api';
import type { PluginRegistration } from '../lib/plugins';
import { renderWithQuery } from '../test/renderWithQuery';
import { PluginSettings } from './PluginSettings';

const saved: PluginRegistration = {
  ownerId: 'local', approval: 'approved', checksum: 'fixture', enabled: true, removed: false, grants: [],
  description: {
    id: 'org.example.output', name: 'Output', version: '1', scope: 'owner',
    settings: [
      { key: 'serialEnabled', label: 'Serial enabled', type: 'boolean' },
      { key: 'password', label: 'Source password', type: 'string', secret: true },
    ],
  },
  configuration: { values: { serialEnabled: false }, secrets: { password: false } },
  health: { status: 'ready', restartCount: 0 },
};
let refreshFails: boolean;
let rejectSave: () => void;
let resolveSave: () => void;
let requests: { path: string; body: unknown }[];

beforeEach(() => {
  refreshFails = false;
  requests = [];
  vi.spyOn(api, 'capabilities').mockResolvedValue({ platforms: [], hosts: ['local', 'remote'].map((remoteId) => ({
    remoteId, remoteName: remoteId, pluginManagement: true,
    capabilities: { gitDiff: false, worktrees: false, tmux: false, projects: false, sessionLaunch: false, judgeModel: false, mcpConfig: false },
  })) });
  vi.stubGlobal('fetch', vi.fn(async (input: string, init?: RequestInit) => {
    const path = new URL(input, 'http://fixture').pathname;
    if (init?.method === 'POST') {
      requests.push({ path, body: JSON.parse(String(init.body)) });
      return new Promise<Response>((resolve) => {
        rejectSave = () => resolve(new Response('private-candidate must never be displayed', { status: 503 }));
        resolveSave = () => resolve(Response.json({}));
      });
    }
    if (refreshFails) return new Response('private refresh diagnostic', { status: 503 });
    if (path === '/api/plugins') return Response.json([saved]);
    if (path === '/api/plugins/discovery') return Response.json([]);
    throw new Error(`Unexpected fixture request: ${path}`);
  }));
});
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

async function submit() {
  renderWithQuery(<PluginSettings />);
  await screen.findByRole('region', { name: 'Output plugin' });
  fireEvent.click(screen.getByRole('button', { name: 'Configure' }));
  fireEvent.change(screen.getByLabelText('Serial enabled'), { target: { value: 'true' } });
  fireEvent.change(screen.getByLabelText('Source password'), { target: { value: 'private-candidate' } });
  fireEvent.click(screen.getByRole('button', { name: 'Save configuration' }));
  await waitFor(() => expect(requests).toHaveLength(1));
  expect(requests[0]).toEqual({
    path: '/api/plugins/org.example.output/configuration',
    body: { values: { serialEnabled: true }, secrets: { password: 'private-candidate' } },
  });
}

it('keeps a rejected activation visible beside Save after refreshing rolled-back ready state', async () => {
  await submit();
  await act(async () => rejectSave());
  const form = screen.getByRole('form', { name: 'Plugin configuration' });
  expect(within(form).getByRole('alert')).toHaveTextContent('Could not confirm configuration was saved and activated');
  expect(within(form).getByRole('alert')).toHaveTextContent(/restore.*last working configuration/i);
  expect(screen.getByLabelText('Serial enabled')).toHaveValue('false');
  expect(screen.getByLabelText('Source password')).toHaveValue('');
  expect(screen.getByText('Enabled · ready')).toBeVisible();
  expect(screen.getByRole('button', { name: 'Save configuration' })).toBeEnabled();
  expect(within(form).getByRole('alert')).toHaveFocus();
  expect(document.body).not.toHaveTextContent('private-candidate');
  expect(screen.queryByDisplayValue('private-candidate')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Refresh health' }));
  await waitFor(() => expect(screen.getByRole('button', { name: 'Refresh health' })).toBeEnabled());
  expect(within(form).getByRole('alert')).toBeVisible();
});

it('retains the failed-save error and form through refresh failure and recovery', async () => {
  await submit();
  refreshFails = true;
  await act(async () => rejectSave());
  expect(screen.getByRole('region', { name: 'Output plugin' })).toBeVisible();
  expect(within(screen.getByRole('form')).getByRole('alert')).toBeVisible();
  expect(screen.getByText(/Could not load plugins for this owner/)).toBeVisible();
  expect(document.body).not.toHaveTextContent('private refresh diagnostic');
  refreshFails = false;
  fireEvent.click(screen.getByRole('button', { name: 'Refresh health' }));
  await waitFor(() => expect(screen.queryByText(/Could not load plugins for this owner/)).not.toBeInTheDocument());
  expect(within(screen.getByRole('form')).getByRole('alert')).toBeVisible();
});

it('shows pending until the response, blocks duplicate saves, and only then reports success', async () => {
  await submit();
  const form = screen.getByRole('form');
  expect(form).toHaveAttribute('aria-busy', 'true');
  expect(screen.getByRole('button', { name: 'Saving configuration…' })).toBeDisabled();
  expect(screen.queryByText('Configuration saved.')).not.toBeInTheDocument();
  fireEvent.submit(form);
  expect(requests).toHaveLength(1);
  await act(async () => rejectSave());
  expect(within(screen.getByRole('form')).getByRole('alert')).toBeVisible();
  fireEvent.click(screen.getByRole('button', { name: 'Save configuration' }));
  await waitFor(() => expect(requests).toHaveLength(2));
  expect(requests[1].body).toEqual({ values: { serialEnabled: false }, secrets: {} });
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  expect(screen.queryByText('Configuration saved.')).not.toBeInTheDocument();
  await act(async () => resolveSave());
  expect(screen.getByRole('status')).toHaveTextContent('Configuration saved.');
  expect(screen.queryByRole('form')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Configure' }));
  expect(screen.queryByText('Configuration saved.')).not.toBeInTheDocument();
});

it('keeps the rejection visible when closing and reopening configuration', async () => {
  await submit();
  await act(async () => rejectSave());
  fireEvent.click(screen.getByRole('button', { name: 'Configure' }));
  expect(screen.queryByRole('form')).not.toBeInTheDocument();
  expect(screen.getByRole('alert')).toBeVisible();
  fireEvent.click(screen.getByRole('button', { name: 'Configure' }));
  expect(within(screen.getByRole('form')).getByRole('alert')).toHaveFocus();
  expect(screen.getByLabelText('Serial enabled')).toHaveValue('false');
  expect(screen.getByLabelText('Source password')).toHaveValue('');
});

it('does not attach a late rejection from the previous owner to the newly selected owner', async () => {
  await submit();
  fireEvent.change(screen.getByLabelText('Plugin owner'), { target: { value: 'remote' } });
  await screen.findByRole('region', { name: 'Output plugin' });
  await act(async () => rejectSave());
  expect(screen.getByLabelText('Plugin owner')).toHaveValue('remote');
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  expect(screen.queryByRole('form')).not.toBeInTheDocument();
  expect(requests).toHaveLength(1);
});

it('does not turn an acknowledged save into an activation failure when health refresh fails', async () => {
  await submit();
  refreshFails = true;
  await act(async () => resolveSave());
  expect(screen.getByText('Configuration saved.')).toBeVisible();
  expect(screen.getByRole('alert')).toHaveTextContent('Could not load plugins for this owner');
  expect(screen.getByRole('region', { name: 'Output plugin' })).toBeVisible();
  expect(screen.queryByRole('form')).not.toBeInTheDocument();
});
