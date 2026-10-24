// @vitest-environment jsdom
import { screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fullCaps, makeSession, makeSessionDetail, renderSessionPage } from './harness';

beforeEach(() => { HTMLElement.prototype.scrollTo = vi.fn(); });

const hint = 'Launch the agent to continue.';

function renderDisconnected({ directory = '/repo', tmuxAvailable = true, liveConnectionHint = hint } = {}) {
  renderSessionPage({
    sessionId: 'child', realAssistantThread: true, tmuxAvailable,
    caps: { ...fullCaps(), liveConnectionHint },
    detail: makeSessionDetail(makeSession({ id: 'child', liveConnection: false, directory })),
  });
}

describe('launch session affordance', () => {
  it('is offered for a disconnected session', async () => {
    renderDisconnected();
    expect(await screen.findByRole('button', { name: 'Launch session' })).toBeInTheDocument();
  });

  it.each([
    ['without a directory (dead-button guard)', { directory: '' }],
    ['without tmux', { tmuxAvailable: false }],
    ['without a live-connection hint', { liveConnectionHint: '' }],
  ])('is not offered %s', async (_name, overrides) => {
    renderDisconnected(overrides);
    await waitFor(() => expect(screen.getByRole('textbox')).toBeDisabled());
    expect(screen.queryByRole('button', { name: 'Launch session' })).not.toBeInTheDocument();
  });
});
