// @vitest-environment jsdom
//
// Regression: a session switch fetched /api/session/{id}/models twice,
// once from the page's per-session effect and once from
// useSessionCapabilities when the live connection was known.

import { describe, it, expect, vi } from 'vitest';
import { act, waitFor } from '@testing-library/react';
import { renderSessionPage, makeSession, makeSessionDetail, flushPromises } from './harness';

describe('SessionDetail — model catalog fetch', () => {
  it('fetches the session models once per switch', async () => {
    const detailA = makeSessionDetail(makeSession({ id: 'sess_a', directory: '/tmp/proj-a' }));
    const detailB = makeSessionDetail(makeSession({ id: 'sess_b', directory: '/tmp/proj-b' }));
    const handle = renderSessionPage({
      sessionId: 'sess_a',
      detail: detailA,
      sessions: [detailA.session, detailB.session],
      apiOverrides: {
        session: vi.fn(async (id: string) => (id === 'sess_a' ? detailA : detailB)),
      },
    });
    const callsFor = (id: string) =>
      handle.api.sessionModels.mock.calls.filter((c: unknown[]) => c[0] === id).length;

    await flushPromises();
    await waitFor(() => expect(handle.sse()).toBeDefined());
    act(() => { handle.sse()!.open(); });
    await flushPromises(8);
    expect(callsFor('sess_a')).toBe(1);

    act(() => { handle.navigate('/session/sess_b'); });
    await flushPromises();
    await waitFor(() => expect(handle.sse()).toBeDefined());
    act(() => { handle.sse()!.open(); });
    await flushPromises(8);
    expect(callsFor('sess_b')).toBe(1);
  });
});
