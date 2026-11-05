// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import type { PluginAgentTools as AgentToolsReport, PluginRegistration, PluginToolConfirmation } from '../lib/plugins';

const api = vi.hoisted(() => ({ agentTools: vi.fn(), decideToolConfirmation: vi.fn() }));
vi.mock('../lib/plugins', async (orig) => {
  const actual = await orig<typeof import('../lib/plugins')>();
  return { ...actual, plugins: { ...actual.plugins, ...api } };
});

import { PluginToolConfirmationCard } from './PluginToolConfirmation';
import { PluginAgentTools } from './PluginAgentTools';
import { PluginSettingsCard } from './PluginSettingsCard';

const request = (status: PluginToolConfirmation['status']): PluginToolConfirmation => ({
  id: 'tc_A', pluginId: 'org.example.notes', tool: 'notes_delete', key: '', text: 'Delete a saved note?',
  arguments: { id: 'n1', password: '(hidden)' }, status, createdAt: 1, expiresAt: 2,
});
const report = (status: PluginToolConfirmation['status']): AgentToolsReport => ({
  tools: [
    { name: 'notes_search', description: 'Search', status: 'available' },
    { name: 'notes_delete', title: 'Delete', description: 'Delete', status: 'not-granted', requiredGrants: ['notes.write'], confirmation: 'Delete a saved note?' },
  ],
  confirmations: [request(status)],
});
const plugin: PluginRegistration = {
  ownerId: 'local', approval: 'fp', checksum: 'abc', enabled: false, removed: false, grants: [],
  description: { id: 'org.example.notes', name: 'Notes', version: '1', scope: 'owner', requestedGrants: ['tool.agent'],
    tools: [{ name: 'notes_search', description: 'Search' }, { name: 'notes_delete', description: 'Delete' }] },
  configuration: { values: {}, secrets: {} }, health: { status: 'disabled', restartCount: 0 },
};

let client: QueryClient;
beforeEach(() => {
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  api.agentTools.mockReset().mockResolvedValue(report('pending'));
  api.decideToolConfirmation.mockReset().mockImplementation(async () => {
    api.agentTools.mockResolvedValue(report('approved'));
    return report('approved');
  });
});
afterEach(() => client.clear());

const wrap = (node: React.ReactNode) => render(<QueryClientProvider client={client}>{node}</QueryClientProvider>);

it('lets the user approve an agent request under its tool call', async () => {
  wrap(<PluginToolConfirmationCard owner="local" pluginId="org.example.notes" confirmationId="tc_A" />);
  expect(await screen.findByText('Delete a saved note?')).toBeInTheDocument();
  expect(screen.getByLabelText('Requested arguments')).toHaveTextContent('(hidden)');
  fireEvent.click(screen.getByRole('button', { name: 'Approve' }));
  await waitFor(() => expect(api.decideToolConfirmation).toHaveBeenCalledWith('local', 'org.example.notes', 'tc_A', 'approve'));
  expect(await screen.findByRole('status')).toHaveTextContent('Approved. The agent can now repeat the call once.');
});

it('reports a refused decision and denial', async () => {
  api.decideToolConfirmation.mockRejectedValue(new Error('409'));
  wrap(<PluginToolConfirmationCard owner="box" pluginId="org.example.notes" confirmationId="tc_A" />);
  fireEvent.click(await screen.findByRole('button', { name: 'Deny' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('Could not record the decision.');
  expect(api.decideToolConfirmation).toHaveBeenCalledWith('box', 'org.example.notes', 'tc_A', 'deny');
  expect(screen.getByText('box')).toBeInTheDocument();
});

it('explains expired, unknown and unloadable requests', async () => {
  api.agentTools.mockResolvedValue({ tools: [], confirmations: [] });
  wrap(<PluginToolConfirmationCard owner="local" pluginId="org.example.notes" confirmationId="tc_A" />);
  expect(await screen.findByRole('status')).toHaveTextContent('no longer available');
  client.clear();
  api.agentTools.mockRejectedValue(new Error('offline'));
  wrap(<PluginToolConfirmationCard owner="local" pluginId="org.example.other" confirmationId="tc_B" />);
  expect(await screen.findByRole('alert')).toHaveTextContent('Settings → Plugins');
});

it('lists declared tools with their availability and pending requests in Settings', async () => {
  wrap(<PluginAgentTools plugin={plugin} owner="local" />);
  expect(await screen.findByText(/Available to agents/)).toBeInTheDocument();
  expect(screen.getByText(/Grant not approved · needs notes.write · always asks you first/)).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Approve' })).toBeInTheDocument();
});

it('names agent tools in the grant review and omits the section without tools', () => {
  wrap(<PluginSettingsCard plugin={plugin} owner="local" refresh={vi.fn()} />);
  fireEvent.click(screen.getByRole('button', { name: 'Enable' }));
  expect(screen.getByText(/can call these tools: notes_search, notes_delete/)).toBeInTheDocument();
  const { container } = render(<PluginAgentTools plugin={{ ...plugin, description: { ...plugin.description, tools: [] } }} owner="local" />);
  expect(container).toBeEmptyDOMElement();
});

it('shows a request that arrives while Settings stays open', async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  try {
    api.agentTools.mockResolvedValueOnce({ tools: report('pending').tools, confirmations: [] });
    wrap(<PluginAgentTools plugin={plugin} owner="local" />);
    expect(await screen.findByText(/Available to agents/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Approve' })).toBeNull();
    await vi.advanceTimersByTimeAsync(10_000);
    expect(await screen.findByRole('button', { name: 'Approve' })).toBeInTheDocument();
  } finally {
    vi.useRealTimers();
  }
});
