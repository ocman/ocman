// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { plugins, type PluginBacklog, type PluginRegistration } from '../lib/plugins';
import { PluginDeliveryBacklog } from './PluginDeliveryBacklog';

vi.mock('../lib/plugins', () => ({ plugins: { backlog: vi.fn(), mutate: vi.fn() } }));

const plugin = {
  ownerId: 'local', approval: 'a', checksum: 'c', enabled: true, removed: false, grants: ['conversation.session'],
  description: {
    id: 'org.example.chatops', name: 'Chat Ops', version: '1', scope: 'owner' as const,
    capabilities: [{ name: 'conversation', version: { major: 1, minor: 0 } }],
  },
  configuration: { values: null, secrets: null },
  health: { status: 'ready', restartCount: 0 },
} satisfies PluginRegistration;

const backlog = (overrides: Partial<PluginBacklog> = {}): PluginBacklog => ({
  pending: 2, dead: 0, bytes: 2048, retrying: 1, paused: false,
  maxRows: 500, maxBytes: 8 << 20, oldestUnsent: Date.now() - 90_000, deadLetters: [], ...overrides,
});

const deadLetter = {
  id: 7, accountId: 'T0WORKSPACE', threadId: 'C1:1700000000.000100',
  attempts: 6, lastError: 'unavailable', updatedAt: Date.now(), bytes: 12,
};

beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(plugins.backlog).mockResolvedValue(backlog());
  vi.mocked(plugins.mutate).mockResolvedValue(backlog({ dead: 0, pending: 1 }));
});

function open() {
  render(<PluginDeliveryBacklog plugin={plugin} owner="local" />);
}
const click = (name: string) => fireEvent.click(screen.getByRole('button', { name }));

it('shows what is still owed and the limits that would pause new work', async () => {
  open();
  expect(await screen.findByText(/2 waiting · 1 retrying · 0 needing a decision/)).toBeVisible();
  expect(screen.getByText(/2 of 500 replies/)).toBeVisible();
  expect(screen.getByText(/oldest 2m/)).toBeVisible();
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  expect(plugins.backlog).toHaveBeenCalledWith('local', 'org.example.chatops', expect.any(AbortSignal));
});

it('warns that new conversation work is paused instead of silently dropping replies', async () => {
  vi.mocked(plugins.backlog).mockResolvedValue(backlog({ pending: 500, paused: true }));
  open();
  expect(await screen.findByRole('alert')).toHaveTextContent('New conversation messages are paused');
});

it('offers an explicit retry and discard for a reply that gave up', async () => {
  vi.mocked(plugins.backlog).mockResolvedValue(backlog({ dead: 1, deadLetters: [deadLetter] }));
  open();
  expect(await screen.findByText('C1:1700000000.000100')).toBeVisible();
  expect(screen.getByText(/6 attempts · unavailable/)).toBeVisible();

  click('Retry delivery');
  await waitFor(() => expect(plugins.mutate).toHaveBeenCalledWith('local', 'org.example.chatops', 'conversations/retry', { deliveryId: 7 }));
  // The control's response is the fresh backlog, so the list clears without a refetch.
  await waitFor(() => expect(screen.queryByText('C1:1700000000.000100')).not.toBeInTheDocument());
  expect(plugins.backlog).toHaveBeenCalledTimes(1);

  vi.mocked(plugins.backlog).mockResolvedValue(backlog({ dead: 1, deadLetters: [deadLetter] }));
  click('Reload status');
  await screen.findByText('C1:1700000000.000100');
  click('Discard reply');
  await waitFor(() => expect(plugins.mutate).toHaveBeenCalledWith('local', 'org.example.chatops', 'conversations/discard', { deliveryId: 7 }));
});

it('reports a failed control without pretending the decision was applied', async () => {
  vi.mocked(plugins.backlog).mockResolvedValue(backlog({ dead: 1, deadLetters: [deadLetter] }));
  vi.mocked(plugins.mutate).mockRejectedValue(new Error('forbidden'));
  open();
  await screen.findByText('C1:1700000000.000100');
  click('Discard reply');
  expect(await screen.findByRole('alert')).toHaveTextContent('Could not discard that reply');
  // The reload after the failure keeps the dead letter visible and actionable.
  expect(screen.getByText('C1:1700000000.000100')).toBeVisible();
});

it('surfaces a status read failure', async () => {
  vi.mocked(plugins.backlog).mockRejectedValue(new Error('offline'));
  open();
  expect(await screen.findByRole('alert')).toHaveTextContent('Could not load reply delivery status');
});
