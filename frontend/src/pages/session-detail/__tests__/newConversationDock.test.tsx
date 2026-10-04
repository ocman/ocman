// @vitest-environment jsdom
import { act, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { renderSessionPage } from './harness';

vi.mock('../../../components/SessionTerminalDock', () => ({
  SessionTerminalDock: ({ directory, remoteId }: { directory?: string; remoteId?: string }) => (
    <div data-testid="terminal-dock">{directory}@{remoteId}</div>
  ),
}));
vi.mock('../NewConversation', () => ({ NewConversation: () => <div data-testid="new-conversation" /> }));

it('docks a terminal below a new conversation, on its directory and machine', async () => {
  const page = renderSessionPage();
  await act(async () => page.navigate('/session/new?dir=%2Frepo&remoteId=box'));
  expect(await screen.findByTestId('new-conversation')).toBeInTheDocument();
  expect(screen.getByTestId('terminal-dock')).toHaveTextContent('/repo@box');
});
