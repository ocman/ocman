// @vitest-environment jsdom

import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import { beforeAll, describe, expect, it, vi } from 'vitest';
import { useShortcutRegistry } from '../../../lib/shortcutRegistry';
import { makeSession, makeSessionDetail, renderSessionPage } from './harness';

beforeAll(() => {
  Element.prototype.scrollIntoView = vi.fn();
});

describe('SessionDetail message jump', () => {
  it('opens with Alt+G and sends the selected message to the thread scroll target', async () => {
    const session = makeSession();
    const initialDetail = makeSessionDetail(session, {
      messages: [
        { id: 'user-1', sessionId: session.id, timeCreated: 1_700_000_000_000, data: { role: 'user' } },
      ],
      parts: [
        { id: 'part-1', messageId: 'user-1', sessionId: session.id, data: { type: 'text', text: 'Jump here' } },
      ],
      totalMessages: 1,
    });
    const fullDetail = makeSessionDetail(session, {
      messages: [
        { id: 'user-older', sessionId: session.id, timeCreated: 1_600_000_000_000, data: { role: 'user' } },
        ...initialDetail.messages,
      ],
      parts: [
        { id: 'part-older', messageId: 'user-older', sessionId: session.id, data: { type: 'text', text: 'Older prompt' } },
        ...initialDetail.parts,
      ],
      totalMessages: 2,
    });
    const fetchSession = vi.fn()
      .mockResolvedValueOnce(initialDetail)
      .mockResolvedValue(fullDetail);
    const { api } = renderSessionPage({
      detail: initialDetail,
      apiOverrides: { session: fetchSession },
    });

    await waitFor(() => expect(screen.getByTestId('assistant-thread')).toBeInTheDocument());
    const shortcut = useShortcutRegistry.getState().shortcuts.get('session.jump-to-message');
    act(() => shortcut?.handler(new KeyboardEvent('keydown')));
    await waitFor(() => expect(api.session).toHaveBeenCalledWith(
      session.id,
      2_147_483_647,
      0,
      undefined,
      session.platform,
    ));
    expect(await screen.findByText('Older prompt')).toBeInTheDocument();
    fireEvent.click(await screen.findByText('Older prompt'));

    expect(screen.getByTestId('assistant-thread-scroll-target')).toHaveTextContent('user-older');
  });
});
