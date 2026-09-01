// @vitest-environment jsdom
//
// #58 regression: the compact queued-message list must update LIVE from
// the ocman.queue.updated broadcast — enqueue/drain/reorder — without a
// page refresh.

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { act, fireEvent, waitFor, within } from '@testing-library/react';
import {
  renderSessionPage,
  makeSessionDetail,
  makeSession,
  flushPromises,
} from './harness';
import { __handleQueueUpdatedForTests, __resetForTests } from '../../../lib/useGlobalEvents';
import type { QueuedMessage } from '../../../lib/api';

const SID = 'sess_q';

function q(id: string, text: string): QueuedMessage {
  return { id, text, hasImages: false, createdAt: 1 };
}

beforeEach(() => __resetForTests());
afterEach(() => __resetForTests());

describe('queued-message list live updates (#58)', () => {
  it('shows a Cmd+Enter submission without waiting for an SSE echo', async () => {
    const detail = makeSessionDetail(makeSession({ id: SID, status: 'busy' }));
    const queuedMessages = vi.fn()
      .mockResolvedValueOnce([])
      .mockResolvedValue([q('a', 'follow up')]);
    const handle = renderSessionPage({
      sessionId: SID,
      detail,
      apiOverrides: { queuedMessages },
    });
    await flushPromises();

    const input = await waitFor(() => within(handle.result.container).getByRole('textbox'));
    fireEvent.input(input, { target: { value: 'follow up' } });
    fireEvent.keyDown(input, { key: 'Enter', metaKey: true });

    await waitFor(() => {
      expect(within(handle.result.container).getByText('follow up')).toBeTruthy();
    });
    expect(handle.store.sendMessage).toHaveBeenCalledWith(
      SID, 'follow up', undefined, expect.anything(), expect.anything(), undefined, 'opencode', true,
    );
  });

  it('reflects enqueue and drain from the broadcast without a refresh', async () => {
    const detail = makeSessionDetail(makeSession({ id: SID }));
    // Start with one queued message.
    const queuedMessages = vi.fn().mockResolvedValue([q('a', 'first queued')]);

    const handle = renderSessionPage({
      sessionId: SID,
      detail,
      apiOverrides: { queuedMessages },
    });

    await flushPromises();

    // The compact list shows the first item.
    const list = await waitFor(() => {
      const el = handle.result.container.querySelector('[data-testid="queued-messages"]');
      expect(el).not.toBeNull();
      return el as HTMLElement;
    });
    expect(within(list).getByText('first queued')).toBeTruthy();

    // Server side: a second message is enqueued. The broadcast carries the
    // full list.
    await act(async () => {
      __handleQueueUpdatedForTests(JSON.stringify({
        sessionID: SID,
        messages: [q('a', 'first queued'), q('b', 'second queued')],
      }));
      await flushPromises();
    });

    // Both items now show — driven purely by the broadcast payload.
    await waitFor(() => {
      expect(within(list).getByText('first queued')).toBeTruthy();
      expect(within(list).getByText('second queued')).toBeTruthy();
    });

    // Server side: the head drains (sent). Broadcast carries the remainder.
    await act(async () => {
      __handleQueueUpdatedForTests(JSON.stringify({
        sessionID: SID,
        messages: [q('b', 'second queued')],
      }));
      await flushPromises();
    });

    // The drained item is gone live; only the remaining one shows.
    await waitFor(() => {
      expect(within(list).queryByText('first queued')).toBeNull();
      expect(within(list).getByText('second queued')).toBeTruthy();
    });
  });

  it('applies messages carried by the broadcast without a refetch', async () => {
    const detail = makeSessionDetail(makeSession({ id: SID }));
    const queuedMessages = vi.fn().mockResolvedValue([q('a', 'first queued')]);
    const handle = renderSessionPage({ sessionId: SID, detail, apiOverrides: { queuedMessages } });
    await flushPromises();

    const list = await waitFor(() => {
      const el = handle.result.container.querySelector('[data-testid="queued-messages"]');
      expect(el).not.toBeNull();
      return el as HTMLElement;
    });
    const callsBefore = queuedMessages.mock.calls.length;

    // Broadcast carries the full queue — the list updates with no refetch.
    await act(async () => {
      __handleQueueUpdatedForTests(
        JSON.stringify({ sessionID: SID, messages: [q('a', 'first queued'), q('b', 'second queued')] }),
      );
      await flushPromises();
    });

    await waitFor(() => {
      expect(within(list).getByText('first queued')).toBeTruthy();
      expect(within(list).getByText('second queued')).toBeTruthy();
    });
    expect(queuedMessages.mock.calls.length).toBe(callsBefore);
  });

  it('ignores broadcasts for a different session', async () => {
    const detail = makeSessionDetail(makeSession({ id: SID }));
    const queuedMessages = vi.fn().mockResolvedValue([q('a', 'mine')]);
    const handle = renderSessionPage({ sessionId: SID, detail, apiOverrides: { queuedMessages } });
    await flushPromises();

    await waitFor(() =>
      expect(handle.result.container.querySelector('[data-testid="queued-messages"]')).not.toBeNull(),
    );
    const callsBefore = queuedMessages.mock.calls.length;

    await act(async () => {
      __handleQueueUpdatedForTests(JSON.stringify({ sessionID: 'other-session' }));
      await flushPromises();
    });

    // No refetch triggered for an unrelated session.
    expect(queuedMessages.mock.calls.length).toBe(callsBefore);
  });
});
