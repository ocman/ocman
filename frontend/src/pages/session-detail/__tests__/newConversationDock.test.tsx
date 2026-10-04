// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { renderSessionPage } from './harness';

vi.mock('../NewConversation', () => ({ NewConversation: () => <div data-testid="new-conversation" /> }));
vi.mock('../../../components/TerminalPane', () => ({
  TerminalPane: ({ window, remoteId }: { window: string; remoteId?: string }) => <div data-testid="terminal-pane">{window}@{remoteId}</div>,
}));

function terminalApi() {
  let finishCreate!: (value: { window: string }) => void;
  return {
    finishCreate: (window: string) => finishCreate({ window }),
    term: {
      listWindows: vi.fn(async (_dir: string, remoteId?: string) => ({
        windows: remoteId === 'm2' ? [{ name: 'ocman-x-7', title: 'm2 shell' }] : [],
      })),
      createWindow: vi.fn(() => new Promise<{ window: string }>((resolve) => { finishCreate = resolve; })),
      killWindow: vi.fn(),
    },
  };
}

it('docks a terminal below a new conversation, on its directory and machine', async () => {
  const { term } = terminalApi();
  const page = renderSessionPage({ tmuxAvailable: true, apiOverrides: { term } as never });
  await act(async () => page.navigate('/session/new?dir=%2Frepo&remoteId=box'));
  expect(await screen.findByTestId('new-conversation')).toBeInTheDocument();
  expect(screen.getByTestId('terminal-dock')).toBeInTheDocument();
  await waitFor(() => expect(term.listWindows).toHaveBeenCalledWith('/repo', 'box'));
});

it('drops a pending terminal from the previous machine after a machine switch', async () => {
  const api = terminalApi();
  const page = renderSessionPage({ tmuxAvailable: true, apiOverrides: { term: api.term } as never });
  await act(async () => page.navigate('/session/new?dir=%2Frepo&remoteId=m1'));
  fireEvent.click(await screen.findByRole('button', { name: 'New terminal' }));
  expect(api.term.createWindow).toHaveBeenCalledWith('/repo', 'm1');
  // Same path on both machines: the stale name would address an m2 shell.
  await act(async () => page.navigate('/session/new?dir=%2Frepo&remoteId=m2'));
  expect(await screen.findByRole('tab', { name: /m2 shell/ })).toBeInTheDocument();
  // The dock and NewConversation are siblings; colliding keys duplicate the thread.
  expect(screen.getAllByTestId('new-conversation')).toHaveLength(1);
  await act(async () => api.finishCreate('ocman-x-1'));
  const terminals = within(screen.getByRole('tablist', { name: 'Terminals' }));
  expect(terminals.getAllByRole('tab').map((tab) => tab.textContent)).toEqual(['m2 shell']);
  expect(screen.queryByTestId('terminal-pane')).not.toBeInTheDocument();
});
