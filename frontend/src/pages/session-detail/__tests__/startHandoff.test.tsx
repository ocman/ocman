// @vitest-environment jsdom
import { act, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useApiStore } from '../../../lib/apiStore';
import { startHandoffs } from '../startHandoffs';
import { fullCaps, makeSession, makeSessionDetail, renderSessionPage } from './harness';

afterEach(() => startHandoffs.clear());
beforeEach(() => { HTMLElement.prototype.scrollTo = vi.fn(); });

describe('new-session handoff', () => {
  it('keeps a launched session writable while the first detail fetch is pending', async () => {
    useApiStore.getState().seedNewSession('child', '/repo', 'opencode');
    const cached = useApiStore.getState().sessionCache.get('child')!;
    startHandoffs.set('child', { prompt: 'Fix login', steps: { opencode: 'done', session: 'done', prompt: 'done' } });
    renderSessionPage({
      sessionId: 'child',
      realAssistantThread: true,
      tmuxAvailable: true,
      caps: { ...fullCaps(), liveConnectionHint: 'Launch the agent to continue.' },
      apiOverrides: { session: vi.fn(() => new Promise(() => {})) },
      storeOverrides: { getCachedSession: () => cached },
    });
    expect(await screen.findByText('Fix login')).toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole('textbox')).not.toBeDisabled());
    expect(screen.queryByRole('button', { name: /launch/i })).not.toBeInTheDocument();
  });

  it('honours a disconnected detail response after the optimistic launch seed', async () => {
    useApiStore.getState().seedNewSession('child', '/repo', 'opencode');
    const cached = useApiStore.getState().sessionCache.get('child')!;
    const disconnected = makeSessionDetail(makeSession({ id: 'child', liveConnection: false }));
    let resolve!: (value: typeof disconnected) => void;
    const response = new Promise<typeof disconnected>((done) => { resolve = done; });
    renderSessionPage({
      sessionId: 'child', realAssistantThread: true, tmuxAvailable: true,
      caps: { ...fullCaps(), liveConnectionHint: 'Launch the agent to continue.' },
      apiOverrides: { session: vi.fn(() => response) },
      storeOverrides: { getCachedSession: () => cached },
    });
    await waitFor(() => expect(screen.getByRole('textbox')).not.toBeDisabled());
    await act(async () => resolve(disconnected));
    await waitFor(() => expect(screen.getByRole('textbox')).toBeDisabled());
    expect(screen.getByRole('button', { name: 'Launch session' })).toBeInTheDocument();
  });

  it('keeps the prompt until its text part arrives, not just its message header', async () => {
    const session = makeSession({ id: 'child' });
    const message = { id: 'msg-first', sessionId: 'child', timeCreated: Date.now(), data: { role: 'user' } };
    startHandoffs.set('child', { prompt: 'Fix login', steps: { prompt: 'done' } });
    const page = renderSessionPage({ sessionId: 'child', realAssistantThread: true, detail: makeSessionDetail(session, { messages: [message] }) });
    await screen.findByRole('textbox');
    expect(screen.getByText('Fix login')).toBeInTheDocument();
    await waitFor(() => expect(page.sse()).toBeDefined());
    await act(async () => page.sse()!.emitMessage({ type: 'message.part.updated', properties: { part: {
      id: 'part-first', messageID: message.id, sessionID: 'child', type: 'text', text: 'Fix login',
    } } }));
    await waitFor(() => expect(screen.queryByTestId('start-progress')).not.toBeInTheDocument());
    expect(screen.getAllByText('Fix login')).toHaveLength(1);
  });
});
