// @vitest-environment jsdom
//
// Regression: switching sessions must not carry the previously-viewed
// session's model into the composer.
//
// The page is not remounted on navigation (see ../index.tsx), so for a
// render or two after `id` flips the `session`/`messages` in state still
// belong to the *old* session. The composer's model is seeded exactly
// once per session id; if that seed runs during the stale window it
// latches the old session's model and the correct one is then blocked
// forever — so the next message silently goes out on the wrong model.

import { describe, it, expect, vi } from 'vitest';
import { act, waitFor } from '@testing-library/react';
import {
  renderSessionPage,
  makeSession,
  makeSessionDetail,
  flushPromises,
} from './harness';
import { saveProjectModel } from '../../../lib/projectModel';

const MODEL_A = 'anthropic/claude-opus-4';
const MODEL_B = 'openai/gpt-5';

async function typeAndSend(container: HTMLElement, text: string) {
  let composer: HTMLTextAreaElement | null = null;
  await waitFor(() => {
    composer = container.querySelector('textarea');
    expect(composer).not.toBeNull();
  }, { timeout: 4000 });
  composer!.focus();
  await act(async () => {
    composer!.value = text;
    composer!.dispatchEvent(new Event('input', { bubbles: true }));
    composer!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
    await flushPromises();
  });
}

describe('SessionDetail — composer model on session switch', () => {
  it('keeps the conversation model when the initial page starts mid-turn', async () => {
    const directory = '/tmp/long-turn-project';
    saveProjectModel(directory, MODEL_B);
    const session = makeSession({ id: 'sess_long', directory });
    const detail = makeSessionDetail(session, {
      defaultModel: MODEL_A,
      totalMessages: 31,
      messages: Array.from({ length: 30 }, (_, index) => ({
        id: `assistant_${index}`,
        sessionId: session.id,
        timeCreated: index + 2,
        data: {
          role: 'assistant' as const,
          providerID: 'anthropic',
          modelID: 'claude-opus-4',
          finish: 'tool-calls',
        },
      })),
    });

    const handle = renderSessionPage({ sessionId: session.id, detail });
    await flushPromises();
    await waitFor(() => expect(handle.sse()).toBeDefined());
    act(() => { handle.sse()!.open(); });

    await typeAndSend(handle.result.container, 'continue');

    await waitFor(() => expect(handle.store.sendMessage).toHaveBeenCalled());
    const call = handle.store.sendMessage.mock.calls[0] as unknown[];
    expect(call[3]).toBe(MODEL_A);
  });

  it('sends with the newly-opened session\'s model, not the previous one', async () => {
    const detailA = makeSessionDetail(
      makeSession({ id: 'sess_a', directory: '/tmp/proj-a' }),
      { defaultModel: MODEL_A },
    );
    const detailB = makeSessionDetail(
      makeSession({ id: 'sess_b', directory: '/tmp/proj-b' }),
      { defaultModel: MODEL_B },
    );

    const handle = renderSessionPage({
      sessionId: 'sess_a',
      detail: detailA,
      sessions: [detailA.session, detailB.session],
      apiOverrides: {
        session: vi.fn(async (id: string) => (id === 'sess_a' ? detailA : detailB)),
      },
    });

    // Session A fully settles, so its model is seeded into the composer.
    await flushPromises();
    await waitFor(() => expect(handle.sse()).toBeDefined());
    act(() => { handle.sse()!.open(); });
    await waitFor(() => {
      expect(handle.result.container.querySelector('textarea')).not.toBeNull();
    }, { timeout: 4000 });

    // Now switch sessions the way the sidebar does — no remount.
    act(() => { handle.navigate('/session/sess_b'); });
    await flushPromises();
    await waitFor(() => expect(handle.sse()).toBeDefined());
    act(() => { handle.sse()!.open(); });
    await flushPromises(8);

    await typeAndSend(handle.result.container, 'hello from b');

    await waitFor(() => {
      expect(handle.store.sendMessage).toHaveBeenCalled();
    }, { timeout: 4000 });
    const call = handle.store.sendMessage.mock.calls[0] as unknown[];
    expect(call[0]).toBe('sess_b');
    expect(call[3]).toBe(MODEL_B);
  });
});
