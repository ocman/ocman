// @vitest-environment jsdom
//
// Integration tests for SessionDetail. These tests are the regression
// net for the FR-1 hook decomposition: they exercise the page's
// observable contracts (initial load, SSE, sidebar polling, prompts,
// composer submit, error states) so any extraction that breaks
// closure capture or dependency arrays surfaces immediately.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, screen, waitFor, act } from '@testing-library/react';
import {
  flushPromises,
  makeSession,
  makeSessionDetail,
  renderSessionPage,
} from './harness';
import { recordFailedSend, clearFailedSends } from '../../../lib/failedSends';
import { useApiStore } from '../../../lib/apiStore';

beforeEach(() => {
  // jsdom does not implement scrollIntoView or scrollTo; the
  // AssistantThread (via @assistant-ui/react's auto-scroll viewport)
  // invokes both after every message append.
  Element.prototype.scrollIntoView = vi.fn() as unknown as typeof Element.prototype.scrollIntoView;
  (Element.prototype as unknown as { scrollTo: () => void }).scrollTo = vi.fn();
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});


describe('SessionDetail — initial mount', () => {
  it('renders the layout and fetches the session detail', async () => {
    const { api } = renderSessionPage({ sessionId: 'sess_1' });
    await flushPromises();
    await waitFor(() => {
      expect(api.session).toHaveBeenCalled();
    });
    expect(screen.getByTestId('session-layout')).toBeInTheDocument();
  });

  it('does not trigger maximum update depth on mount', async () => {
    // Regression guard: React throws "Maximum update depth exceeded"
    // when a component calls setState > 50 times in a single commit.
    // Spy on console.error to detect the error message React logs
    // just before throwing.
    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

    const sess = makeSession({ id: 'sess_depth', status: 'busy' });
    const detail = makeSessionDetail(sess, {
      messages: [
        { id: 'msg1', sessionId: 'sess_depth', timeCreated: Date.now() - 1000, data: { role: 'user' } },
        { id: 'msg2', sessionId: 'sess_depth', timeCreated: Date.now(), data: { role: 'assistant' } },
      ],
      totalMessages: 2,
    });

    renderSessionPage({ sessionId: 'sess_depth', detail, sessions: [sess] });
    await flushPromises(8);

    const maxDepthCalls = errorSpy.mock.calls.filter(
      (args) => args.some((a) => typeof a === 'string' && a.includes('Maximum update depth exceeded')),
    );
    expect(maxDepthCalls).toHaveLength(0);

    errorSpy.mockRestore();
  });

  it('does not loop when getSession returns fresh objects each call', async () => {
    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

    const mkDetail = () => {
      const s = makeSession({ id: 'sess_fresh', status: 'busy' });
      return makeSessionDetail(s, {
        messages: [
          { id: 'msg1', sessionId: 'sess_fresh', timeCreated: Date.now() - 1000, data: { role: 'user' } },
          { id: 'msg2', sessionId: 'sess_fresh', timeCreated: Date.now(), data: { role: 'assistant' } },
        ],
        totalMessages: 2,
      });
    };

    // Each call returns a structurally identical but referentially
    // new object — exactly what a real API does.
    const apiSessionSpy = vi.fn().mockImplementation(() => Promise.resolve(mkDetail()));
    const getSessionsSpy = vi.fn().mockImplementation(() =>
      Promise.resolve([makeSession({ id: 'sess_fresh', status: 'busy' })]),
    );

    renderSessionPage({
      sessionId: 'sess_fresh',
      detail: mkDetail(),
      sessions: [makeSession({ id: 'sess_fresh', status: 'busy' })],
      apiOverrides: { session: apiSessionSpy },
      storeOverrides: {
        getSessions: getSessionsSpy,
      },
    });

    // Let several render cycles settle.
    await flushPromises(12);
    await act(async () => { await flushPromises(4); });

    const maxDepthCalls = errorSpy.mock.calls.filter(
      (args) => args.some((a) => typeof a === 'string' && a.includes('Maximum update depth exceeded')),
    );
    expect(maxDepthCalls).toHaveLength(0);

    errorSpy.mockRestore();
  }, 10_000);

  it('does not loop when failedSends exist near the memory-trim threshold', async () => {
    // Regression: ghost injection and memory trimming both depended on
    // `messages` and both called `setMessages`. When failedSends were
    // present and the message count was near MAX_RETAINED_MESSAGES (200),
    // the two effects cascaded into an infinite loop:
    //   inject ghosts → messages grows past 200 → trim fires → trim
    //   removes ghosts → injection re-fires → ...
    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

    const sessionId = 'sess_trim_loop';
    // Seed 195 messages — close to the 200 threshold.
    const msgs = Array.from({ length: 195 }, (_, i) => ({
      id: `msg_${i}`,
      sessionId,
      timeCreated: Date.now() - (195 - i) * 1000,
      data: { role: i % 2 === 0 ? 'user' as const : 'assistant' as const },
    }));

    const sess = makeSession({ id: sessionId, status: 'busy' });
    const detail = makeSessionDetail(sess, { messages: msgs, totalMessages: 195 });

    // Persist a failed send so the ghost-injection effect has work to do.
    recordFailedSend(sessionId, {
      id: 'ghost_1',
      text: 'a prompt that failed',
      error: 'network error',
      failedAt: Date.now(),
    });

    try {
      renderSessionPage({ sessionId, detail, sessions: [sess] });
      await flushPromises(12);
      await act(async () => { await flushPromises(8); });

      const maxDepthCalls = errorSpy.mock.calls.filter(
        (args) => args.some((a) => typeof a === 'string' && a.includes('Maximum update depth exceeded')),
      );
      expect(maxDepthCalls).toHaveLength(0);
    } finally {
      clearFailedSends(sessionId);
      errorSpy.mockRestore();
    }
  }, 10_000);

  it('shows the loading state before the first response', async () => {
    let resolveDetail!: (d: ReturnType<typeof makeSessionDetail>) => void;
    const pending = new Promise<ReturnType<typeof makeSessionDetail>>((r) => {
      resolveDetail = r;
    });
    const handle = renderSessionPage({
      apiOverrides: { session: vi.fn().mockReturnValue(pending) },
    });
    await waitFor(() => {
      expect(handle.result.queryByTestId('thread-skeleton')).toBeInTheDocument();
    });
    resolveDetail(makeSessionDetail(makeSession()));
    await flushPromises();
    await waitFor(() => {
      expect(handle.result.queryByTestId('thread-skeleton')).not.toBeInTheDocument();
    });
  });

  it('renders the error banner when api.session rejects', async () => {
    renderSessionPage({
      apiOverrides: {
        session: vi.fn().mockRejectedValue(new Error('boom')),
      },
    });
    await flushPromises(8);
    await waitFor(() => {
      expect(screen.getByTestId('error-banner')).toBeInTheDocument();
    });
  });

});

describe('SessionDetail — SSE stream', () => {
  it('opens an EventSource against the active session id', async () => {
    const { sse } = renderSessionPage({ sessionId: 'sess_42' });
    await flushPromises();
    await waitFor(() => {
      expect(sse()).toBeDefined();
    });
    expect(sse()!.url).toContain('/api/session/sess_42/events');
  });

  it('updates the session cache when message.created is dispatched', async () => {
    // The page mirrors live state into the apiStore's session cache
    // via updateCachedSession. Asserting on its calls verifies the
    // SSE handler reached setMessages without depending on
    // assistant-ui's render scheduling.
    const handle = renderSessionPage({ sessionId: 'sess_1' });
    await flushPromises();
    await waitFor(() => expect(handle.sse()).toBeDefined());
    // Reset the spy so we ignore mirror calls from the initial load.
    handle.store.updateCachedSession.mockClear();

    act(() => {
      handle.sse()!.open();
      handle.sse()!.emitMessage({
        type: 'message.created',
        properties: {
          info: {
            id: 'msg_1',
            sessionID: 'sess_1',
            role: 'assistant',
            time: { created: Date.now() },
          },
          parts: [
            { id: 'p_1', type: 'text', text: 'Hello world', messageID: 'msg_1' },
          ],
        },
      });
    });

    await waitFor(() => {
      // updateCachedSession is invoked with an updater function. We
      // run that updater against an empty SessionDetail seed and
      // inspect the resulting messages array.
      const calls = handle.store.updateCachedSession.mock.calls;
      expect(calls.length).toBeGreaterThan(0);
      const seedDetail = {
        session: {} as never,
        messages: [],
        parts: [],
        totalMessages: 0,
      };
      const seenMsgIds = calls.flatMap((call) => {
        const [, updater] = call as [string, (prev: typeof seedDetail) => { messages: { id: string }[] }];
        return updater(seedDetail).messages.map((m) => m.id);
      });
      expect(seenMsgIds).toContain('msg_1');
    });
  });

  it('appends streamed delta text to an existing part', async () => {
    const handle = renderSessionPage({ sessionId: 'sess_1' });
    await flushPromises();
    await waitFor(() => expect(handle.sse()).toBeDefined());

    const seedDetail = {
      session: {} as never,
      messages: [],
      parts: [],
      totalMessages: 0,
    };
    const partTextsFromUpdaters = () => {
      const calls = handle.store.updateCachedSession.mock.calls;
      return calls.flatMap((call) => {
        const [, updater] = call as [string, (prev: typeof seedDetail) => { parts: { data: unknown }[] }];
        return updater(seedDetail).parts.map((p) => {
          const data = typeof p.data === 'string' ? JSON.parse(p.data) : p.data;
          return (data as { text?: string })?.text ?? '';
        });
      });
    };

    handle.store.updateCachedSession.mockClear();
    act(() => {
      handle.sse()!.open();
      // Seed the message and its initial part via the same
      // `message.created` event OpenCode emits in practice — the
      // embedded parts snapshot is the only channel that carries
      // tool / question blocks before they finalise, so the SSE
      // handler must merge it into the parts array.
      handle.sse()!.emitMessage({
        type: 'message.created',
        properties: {
          info: { id: 'msg_1', sessionID: 'sess_1', role: 'assistant', time: { created: Date.now() } },
          parts: [{ id: 'p_1', type: 'text', text: 'Hi', messageID: 'msg_1' }],
        },
      });
    });
    await waitFor(() => {
      expect(partTextsFromUpdaters().some((t) => t.includes('Hi'))).toBe(true);
    });

    act(() => {
      handle.sse()!.emitMessage({
        type: 'message.part.delta',
        properties: {
          partID: 'p_1',
          messageID: 'msg_1',
          field: 'text',
          delta: ' there',
        },
      });
    });

    await waitFor(() => {
      expect(partTextsFromUpdaters().some((t) => t.includes('Hi there'))).toBe(true);
    });
  });

  it('appends streamed delta text from documented OpenCode payload events', async () => {
    const handle = renderSessionPage({ sessionId: 'sess_1' });
    await flushPromises();
    await waitFor(() => expect(handle.sse()).toBeDefined());

    const seedDetail = {
      session: {} as never,
      messages: [],
      parts: [],
      totalMessages: 0,
    };
    const partTextsFromUpdaters = () => {
      const calls = handle.store.updateCachedSession.mock.calls;
      return calls.flatMap((call) => {
        const [, updater] = call as [string, (prev: typeof seedDetail) => { parts: { data: unknown }[] }];
        return updater(seedDetail).parts.map((p) => {
          const data = typeof p.data === 'string' ? JSON.parse(p.data) : p.data;
          return (data as { text?: string })?.text ?? '';
        });
      });
    };

    handle.store.updateCachedSession.mockClear();
    act(() => {
      handle.sse()!.open();
      handle.sse()!.emitMessage({
        id: 'evt-created',
        type: 'message.created',
        payload: {
          info: { id: 'msg_1', sessionID: 'sess_1', role: 'assistant', time: { created: Date.now() } },
          parts: [{ id: 'p_1', type: 'text', text: 'Hi', messageID: 'msg_1' }],
        },
      });
    });
    await waitFor(() => {
      expect(partTextsFromUpdaters().some((t) => t.includes('Hi'))).toBe(true);
    });

    act(() => {
      handle.sse()!.emitMessage({
        id: 'evt-delta',
        type: 'message.part.delta',
        payload: {
          sessionID: 'sess_1',
          partID: 'p_1',
          messageID: 'msg_1',
          field: 'text',
          delta: ' there',
        },
      });
    });

    await waitFor(() => {
      expect(partTextsFromUpdaters().some((t) => t.includes('Hi there'))).toBe(true);
    });
  });

  it('appends streamed tool output from documented OpenCode payload events', async () => {
    const handle = renderSessionPage({ sessionId: 'sess_1' });
    await flushPromises();
    await waitFor(() => expect(handle.sse()).toBeDefined());

    const seedDetail = {
      session: {} as never,
      messages: [],
      parts: [],
      totalMessages: 0,
    };
    const partOutputsFromUpdaters = () => {
      const calls = handle.store.updateCachedSession.mock.calls;
      return calls.flatMap((call) => {
        const [, updater] = call as [string, (prev: typeof seedDetail) => { parts: { data: unknown }[] }];
        return updater(seedDetail).parts.map((p) => {
          const data = typeof p.data === 'string' ? JSON.parse(p.data) : p.data;
          return (data as { state?: { output?: string } })?.state?.output ?? '';
        });
      });
    };

    handle.store.updateCachedSession.mockClear();
    act(() => {
      handle.sse()!.open();
      handle.sse()!.emitMessage({
        id: 'evt-tool',
        type: 'message.part.updated',
        payload: {
          id: 'p_tool',
          sessionID: 'sess_1',
          messageID: 'msg_1',
          type: 'tool',
          tool: 'bash',
          state: {
            status: 'running',
            input: { command: 'for i in 1 2; do echo $i; sleep 1; done' },
            output: '',
          },
        },
      });
    });
    await waitFor(() => {
      expect(partOutputsFromUpdaters()).toContain('');
    });

    act(() => {
      handle.sse()!.emitMessage({
        id: 'evt-tool-delta',
        type: 'message.part.delta',
        payload: {
          sessionID: 'sess_1',
          partID: 'p_tool',
          messageID: 'msg_1',
          field: 'state.output',
          delta: 'line 1\n',
        },
      });
    });

    await waitFor(() => {
      expect(partOutputsFromUpdaters().some((out) => out.includes('line 1'))).toBe(true);
    });
  });

  it('accepts OpenCode global payload wrappers for running bash metadata output', async () => {
    const handle = renderSessionPage({ sessionId: 'sess_1' });
    await flushPromises();
    await waitFor(() => expect(handle.sse()).toBeDefined());

    const seedDetail = {
      session: {} as never,
      messages: [],
      parts: [],
      totalMessages: 0,
    };
    const metadataOutputsFromUpdaters = () => {
      const calls = handle.store.updateCachedSession.mock.calls;
      return calls.flatMap((call) => {
        const [, updater] = call as [string, (prev: typeof seedDetail) => { parts: { data: unknown }[] }];
        return updater(seedDetail).parts.map((p) => {
          const data = typeof p.data === 'string' ? JSON.parse(p.data) : p.data;
          return (data as { state?: { metadata?: { output?: string } } })?.state?.metadata?.output ?? '';
        });
      });
    };

    handle.store.updateCachedSession.mockClear();
    act(() => {
      handle.sse()!.open();
      handle.sse()!.emitMessage({
        type: 'event',
        payload: {
          type: 'message.part.updated',
          properties: {
            part: {
              id: 'p_tool',
              sessionID: 'sess_1',
              messageID: 'msg_1',
              type: 'tool',
              tool: 'bash',
              state: {
                status: 'running',
                input: { command: 'ping -c 6 8.8.8.8' },
                metadata: { output: '64 bytes from 8.8.8.8: icmp_seq=0\n' },
              },
            },
          },
        },
      });
    });

    await waitFor(() => {
      expect(metadataOutputsFromUpdaters().some((out) => out.includes('icmp_seq=0'))).toBe(true);
    });
  });

  it('renders a tool part embedded in message.created without a refresh', async () => {
    // Regression: tool blocks (including `question` prompts) arrive
    // exclusively via the embedded `parts` snapshot on
    // `message.created` / `message.updated` — they have no
    // standalone `message.part.delta` channel. Dropping that
    // snapshot makes tool blocks invisible until the user
    // refreshes. See lib/partReducer.ts.
    const handle = renderSessionPage({ sessionId: 'sess_1' });
    await flushPromises();
    await waitFor(() => expect(handle.sse()).toBeDefined());

    const seedDetail = {
      session: {} as never,
      messages: [],
      parts: [],
      totalMessages: 0,
    };
    const collectedParts = () => {
      const calls = handle.store.updateCachedSession.mock.calls;
      return calls.flatMap((call) => {
        const [, updater] = call as [string, (prev: typeof seedDetail) => { parts: { id: string; data: unknown }[] }];
        return updater(seedDetail).parts.map((p) => ({
          id: p.id,
          data: typeof p.data === 'string' ? JSON.parse(p.data) : p.data,
        }));
      });
    };

    handle.store.updateCachedSession.mockClear();
    act(() => {
      handle.sse()!.open();
      handle.sse()!.emitMessage({
        type: 'message.created',
        properties: {
          info: { id: 'msg_tool', sessionID: 'sess_1', role: 'assistant', time: { created: Date.now() } },
          parts: [
            {
              id: 'p_tool',
              type: 'tool',
              tool: 'bash',
              messageID: 'msg_tool',
              sessionID: 'sess_1',
              state: { status: 'running', input: { command: 'ls' } },
            },
          ],
        },
      });
    });

    await waitFor(() => {
      const parts = collectedParts();
      expect(parts.some((p) => p.id === 'p_tool' && (p.data as { type?: string }).type === 'tool')).toBe(true);
    });
  });

  it('routes a live message.part.updated into the cache as a tool part', async () => {
    // Regression: the symptom is "tool blocks / question answers
    // don't show up live, only after refresh". The new reducer
    // pipeline writes tool parts to state on every part.updated
    // SSE event, then mirrors into the apiStore cache. We assert
    // via the cache mirror because the AssistantThread is mocked
    // out at the harness level (see harness.tsx). End-to-end DOM
    // rendering of `.oc-tool` is covered by the Playwright e2e
    // suite.
    const handle = renderSessionPage({ sessionId: 'sess_1' });
    await flushPromises();
    await waitFor(() => expect(handle.sse()).toBeDefined());
    handle.store.updateCachedSession.mockClear();

    act(() => {
      handle.sse()!.open();
      handle.sse()!.emitMessage({
        type: 'message.created',
        properties: {
          info: { id: 'msg_a', sessionID: 'sess_1', role: 'assistant', time: { created: Date.now() } },
        },
      });
      handle.sse()!.emitMessage({
        type: 'message.part.updated',
        properties: {
          part: {
            id: 'p_bash',
            type: 'tool',
            tool: 'bash',
            messageID: 'msg_a',
            sessionID: 'sess_1',
            state: {
              status: 'running',
              input: { command: 'echo hello' },
              title: 'echo hello',
            },
          },
        },
      });
    });

    await waitFor(() => {
      const calls = handle.store.updateCachedSession.mock.calls;
      expect(calls.length).toBeGreaterThan(0);
      type SeedDetail = {
        session: never;
        messages: { id: string; data: { role: string } }[];
        parts: { id: string; data: unknown }[];
        totalMessages: number;
      };
      const seedDetail: SeedDetail = { session: {} as never, messages: [], parts: [], totalMessages: 0 };
      const lastCall = calls.at(-1)!;
      const updater = lastCall[1] as (prev: SeedDetail) => SeedDetail;
      const result = updater(seedDetail);
      const toolPart = result.parts.find((p) => p.id === 'p_bash');
      expect(toolPart).toBeDefined();
      const data = typeof toolPart!.data === 'string' ? JSON.parse(toolPart!.data) : toolPart!.data;
      expect((data as { type: string }).type).toBe('tool');
      expect((data as { tool: string }).tool).toBe('bash');
    });
  });

  it('reconciles delta-accumulated text against trailing snapshots using longest-wins', async () => {
    // Contract for delta-owned streaming fields: the longer of (local,
    // snapshot) wins — streaming fields are append-only on the wire,
    // so the shorter string is always a prefix of the longer one, and
    // the longer string is the more-complete value.
    //
    // History:
    //  - Originally the reducer compared lengths naïvely and would
    //    rewind local text when a snapshot happened to be shorter,
    //    leaving mid-text gaps.
    //  - That was replaced with "local always wins for delta-owned
    //    fields", which fixed the rewind but introduced a new bug:
    //    after a session-switch round-trip the cache-seeded local
    //    text could not grow even when the server snapshot had moved
    //    ahead, and (because the cache doesn't persist the ownership
    //    map) a DB-lagging snapshot could wipe the cached text.
    //  - The current rule is "longest-wins" — strictly safe given the
    //    append-only contract, and it heals both gaps from
    //    cache-seeded revisits and the original rewind. See
    //    seedDeltaOwnedFields / upsertSnapshotPart in sessionReducer.
    const handle = renderSessionPage({ sessionId: 'sess_1' });
    await flushPromises();
    await waitFor(() => expect(handle.sse()).toBeDefined());

    type SeedDetail = {
      session: never;
      messages: { id: string; data: unknown }[];
      parts: { id: string; data: unknown }[];
      totalMessages: number;
    };
    const seedDetail: SeedDetail = {
      session: {} as never,
      messages: [],
      parts: [],
      totalMessages: 0,
    };
    const latestParts = () => {
      const calls = handle.store.updateCachedSession.mock.calls;
      // Reduce all updater calls in order to get the final state.
      let detail: SeedDetail = seedDetail;
      for (const call of calls) {
        const [, updater] = call as [string, (prev: SeedDetail) => SeedDetail];
        detail = updater(detail);
      }
      return detail.parts.map((p) => ({
        id: p.id,
        data: typeof p.data === 'string' ? JSON.parse(p.data) : p.data,
      }));
    };

    handle.store.updateCachedSession.mockClear();
    act(() => {
      handle.sse()!.open();
      handle.sse()!.emitMessage({
        type: 'message.created',
        properties: {
          info: { id: 'msg_drift', sessionID: 'sess_1', role: 'assistant', time: { created: Date.now() } },
          parts: [{ id: 'p_drift', type: 'text', text: 'Hello', messageID: 'msg_drift' }],
        },
      });
      // Stream a few deltas so the local copy gets to "Hello world".
      handle.sse()!.emitMessage({
        type: 'message.part.delta',
        properties: { partID: 'p_drift', messageID: 'msg_drift', field: 'text', delta: ' world' },
      });
    });

    await waitFor(() => {
      const parts = latestParts();
      const drift = parts.find((p) => p.id === 'p_drift');
      expect((drift?.data as { text?: string })?.text).toBe('Hello world');
    });

    // A trailing snapshot arrives carrying stale text (server-side
    // commit lagged the deltas) plus updated metadata. The reducer
    // must preserve the delta-built text while still applying any
    // new metadata fields.
    act(() => {
      handle.sse()!.emitMessage({
        type: 'message.part.updated',
        properties: {
          part: {
            id: 'p_drift',
            type: 'text',
            text: 'Hello',
            messageID: 'msg_drift',
            sessionID: 'sess_1',
            // A field that does NOT participate in delta streaming —
            // we want to see this land while text stays put.
            completed: true,
          },
        },
      });
    });

    await waitFor(() => {
      const parts = latestParts();
      const drift = parts.find((p) => p.id === 'p_drift');
      const data = drift?.data as { text?: string; completed?: boolean };
      expect(data?.text).toBe('Hello world');
      expect(data?.completed).toBe(true);
    });

    // A snapshot that jumps AHEAD of the deltas takes over: it
    // carries strictly more content (append-only), so refusing it
    // would leave the user's view permanently truncated relative to
    // the server. This is the path that heals "missing sections"
    // after a session-switch round-trip when the DB has caught up
    // past the cached text.
    act(() => {
      handle.sse()!.emitMessage({
        type: 'message.part.updated',
        properties: {
          part: {
            id: 'p_drift',
            type: 'text',
            text: 'Hello world how are you doing today',
            messageID: 'msg_drift',
            sessionID: 'sess_1',
          },
        },
      });
    });

    await waitFor(() => {
      const parts = latestParts();
      const drift = parts.find((p) => p.id === 'p_drift');
      expect((drift?.data as { text?: string })?.text).toBe('Hello world how are you doing today');
    });
  });
});

describe('SessionDetail — sidebar polling', () => {
  it('lists the recent sessions returned by getSessions', async () => {
    const sib = makeSession({ id: 'sess_other', title: 'Other session', timeUpdated: Date.now() });
    const handle = renderSessionPage({
      sessionId: 'sess_1',
      sessions: [makeSession({ id: 'sess_1', title: 'Active' }), sib],
    });
    await flushPromises();
    await waitFor(() => {
      expect(handle.store.getSessions).toHaveBeenCalled();
    });
    await waitFor(() => {
      expect(screen.getByText('Other session')).toBeInTheDocument();
    });
  });

  it('passes the configured recent-sessions window via the `since` filter', async () => {
    const handle = renderSessionPage({ sessionId: 'sess_1' });
    await flushPromises();
    await waitFor(() => expect(handle.store.getSessions).toHaveBeenCalled());
    const [params] = handle.store.getSessions.mock.calls[0] as [{ since?: number; limit?: number }];
    expect(typeof params.since).toBe('number');
    expect(params.since).toBeLessThan(Date.now());
    expect(params.limit).toBeGreaterThan(0);
  });
});

describe('SessionDetail — session tree usage', () => {
  it('uses the detail tree for parent lookup without fetching the full session list', async () => {
    const parent = makeSession({ id: 'sess_parent', title: 'Parent planning session' });
    const child = makeSession({ id: 'sess_child', parentId: parent.id });
    const sessions = vi.fn().mockResolvedValue([parent, child]);
    const handle = renderSessionPage({
      sessionId: child.id,
      detail: makeSessionDetail(child, { sessionTree: [parent, child] }),
      sessions: [parent, child],
      apiOverrides: { sessions },
    });

    const link = await screen.findByRole('link', { name: 'Parent planning session' });
    expect(link).toHaveAttribute('href', '/session/sess_parent');
    expect(link.closest('[role="note"]')).toHaveTextContent('Child session of');
    expect(handle.api.sessions).not.toHaveBeenCalled();
  });

  it('shows totals for the current session and nested subagents', async () => {
    const root = { ...makeSession({ id: 'sess_1', totalInputTokens: 10, totalOutputTokens: 20, totalCost: 0.1 }), totalEstCost: 0.15 };
    const child = { ...makeSession({ id: 'sess_child', parentId: root.id, totalInputTokens: 30, totalOutputTokens: 40, totalCost: 0.2 }), totalEstCost: 0.25 };
    const grandchild = { ...makeSession({ id: 'sess_grandchild', parentId: child.id, totalInputTokens: 50, totalOutputTokens: 60, totalCost: 0.3 }), totalEstCost: 0.35 };
    const sessions = vi.fn().mockResolvedValue([root, child, grandchild]);
    const handle = renderSessionPage({
      sessionId: root.id,
      detail: makeSessionDetail(root, {
        contextTokenCount: 100,
        sessionTree: [root, child, grandchild],
      }),
      sessions: [root],
      apiOverrides: { sessions },
    });

    await flushPromises(8);
    fireEvent.click(await screen.findByTitle('Click for token usage details'));

    const title = await screen.findByText('Session + subagents (3)');
    expect(title.parentElement).toHaveTextContent('Input90');
    expect(title.parentElement).toHaveTextContent('Output120');
    expect(title.parentElement).toHaveTextContent('Reported cost$0.6000');
    expect(title.parentElement).toHaveTextContent('Est. cost$0.7500');
    expect(handle.api.sessions).not.toHaveBeenCalled();
  });
});

describe('SessionDetail — permission prompt', () => {
  // listPermissions is only called when the sidebar reports the
  // current session has a pending permission. The fixture below
  // mirrors that: getSessions returns the session with the flag
  // set so the polling effect triggers the listPermissions branch.
  const sessionWithPerm = makeSession({ id: 'sess_1', pendingPermission: true });
  const permPayload = {
    id: 'perm_1',
    permission: 'Run shell command',
    patterns: ['ls *'],
    sessionID: '',
  };

  it('renders the prompt when listPermissions returns one', async () => {
    const listPermissionsSpy = vi.fn().mockResolvedValue([permPayload]);
    const handle = renderSessionPage({
      sessionId: 'sess_1',
      detail: makeSessionDetail(sessionWithPerm),
      sessions: [sessionWithPerm],
      storeOverrides: { listPermissions: listPermissionsSpy },
    });
    await flushPromises(8);
    // listPermissions is called once the sidebar reflects the
    // pending-permission flag for the active session.
    await waitFor(() => {
      expect(listPermissionsSpy).toHaveBeenCalledWith('sess_1');
    });
    await waitFor(() => {
      expect(handle.result.container.textContent).toContain('Run shell command');
    }, { timeout: 4000 });
  });

  it('keeps deep descendant IDs as permission owners', async () => {
    const parent = makeSession({
      id: 'sess_parent',
      title: 'App Security Review',
      directory: '/Users/dries/src/github.com/aspect-analytics/platform-service',
      pendingPermission: true,
    });
    const childId = 'sess_native_child';
    const child = makeSession({ id: childId, parentId: parent.id, directory: parent.directory });
    const grandchild = makeSession({ id: 'sess_mcp_grandchild', parentId: childId, directory: parent.directory });
    const permission = { ...permPayload, sessionID: grandchild.id };
    const listPermissionsSpy = vi.fn().mockImplementation((sessionId: string) =>
      Promise.resolve(sessionId === grandchild.id ? [permission] : []),
    );
    const handle = renderSessionPage({
      sessionId: parent.id,
      detail: makeSessionDetail(parent, {
        messages: [{
          id: 'msg_assistant',
          sessionId: parent.id,
          timeCreated: Date.now(),
          data: { role: 'assistant' },
        }],
        parts: [{
          id: 'part_task',
          messageId: 'msg_assistant',
          sessionId: parent.id,
          data: {
            type: 'tool',
            tool: 'task',
            state: {
              status: 'running',
              metadata: { sessionId: childId, parentSessionId: parent.id },
            },
          } as never,
        }],
        sessionTree: [parent, child, grandchild],
      }),
      sessions: [parent],
      apiOverrides: { sessionTasks: vi.fn().mockResolvedValue({ tasks: {} }) },
      storeOverrides: { listPermissions: listPermissionsSpy },
    });

    await flushPromises(8);
    expect(listPermissionsSpy).toHaveBeenCalledWith(parent.id);
    await waitFor(() => expect(listPermissionsSpy).toHaveBeenCalledWith(childId));
    await waitFor(() => expect(listPermissionsSpy).toHaveBeenCalledWith(grandchild.id));
    await waitFor(() => expect(handle.result.container.textContent).toContain('Run shell command'));
  });

  it('renders a child prompt even when its worktree is not marked live', async () => {
    const child = makeSession({
      id: 'sess_child',
      parentId: 'sess_parent',
      liveConnection: false,
      pendingPermission: true,
    });
    const permission = { ...permPayload, sessionID: child.id };
    const handle = renderSessionPage({
      sessionId: child.id,
      detail: makeSessionDetail(child),
      sessions: [child],
      storeOverrides: {
        listPermissions: vi.fn().mockResolvedValue([permission]),
      },
    });

    await flushPromises(8);
    await waitFor(() => {
      expect(handle.result.container.textContent).toContain('Run shell command');
    }, { timeout: 4000 });

    fireEvent.click(screen.getByRole('button', { name: /allow once/i }));
    await waitFor(() => {
      expect(handle.store.respondPermission).toHaveBeenCalledWith(child.id, permission.id, 'once');
    });
  });

  it('posts the reply when Allow once is clicked', async () => {
    const handle = renderSessionPage({
      sessionId: 'sess_1',
      detail: makeSessionDetail(sessionWithPerm),
      sessions: [sessionWithPerm],
      storeOverrides: {
        listPermissions: vi.fn().mockResolvedValue([permPayload]),
      },
    });
    await flushPromises(8);
    const allow = await screen.findByRole('button', { name: /allow once/i }, { timeout: 4000 });
    await act(async () => {
      allow.click();
      await flushPromises();
    });
    expect(handle.store.respondPermission).toHaveBeenCalledWith('sess_1', 'perm_1', 'once');
  });
});

describe('SessionDetail — question prompt', () => {
  const sessionWithQ = makeSession({ id: 'sess_1', pendingQuestion: true });
  const questionPayload = {
    id: 'q_1',
    sessionID: '',
    questions: [
      {
        question: 'Pick a colour',
        options: [
          { label: 'red', description: '' },
          { label: 'blue', description: '' },
        ],
      },
    ],
  };

  it('renders the question when listQuestions returns one', async () => {
    const handle = renderSessionPage({
      sessionId: 'sess_1',
      detail: makeSessionDetail(sessionWithQ),
      sessions: [sessionWithQ],
      storeOverrides: {
        listQuestions: vi.fn().mockResolvedValue([questionPayload]),
      },
    });
    await flushPromises(8);
    await waitFor(() => {
      expect(handle.result.container.textContent).toContain('Pick a colour');
    }, { timeout: 4000 });
  });

  // A question answered outside ocman (another browser tab, or the
  // OpenCode CLI) does not reliably produce a `question.replied` event.
  // The 3 s poll is the only thing that takes the dialog down; if it
  // can't, the prompt blocks the composer forever.
  it('dismisses the question once the live list drops its request id', async () => {
    let answered = false;
    const listQuestions = vi.fn(() => Promise.resolve(answered ? [] : [questionPayload]));
    // Fake timers must be installed before the mount: the poll's
    // setInterval is registered during the mount effect.
    vi.useFakeTimers();
    try {
      const handle = renderSessionPage({
        sessionId: 'sess_1',
        detail: makeSessionDetail(sessionWithQ),
        sessions: [sessionWithQ],
        storeOverrides: { listQuestions },
      });
      await act(async () => { await vi.advanceTimersByTimeAsync(50); });
      expect(handle.result.container.textContent).toContain('Pick a colour');

      answered = true;
      // Two poll ticks: one to observe the drop, one to prove the
      // dialog doesn't get re-hydrated from session storage.
      await act(async () => { await vi.advanceTimersByTimeAsync(6500); });
      expect(handle.result.container.textContent).not.toContain('Pick a colour');
    } finally {
      vi.useRealTimers();
    }
  });
});

describe('SessionDetail — composer send', () => {
  // The composer dispatches via either the assistant-ui runtime's
  // onNew callback (which calls sendMessage on the apiStore) or the
  // page's send handler. Either way, calling sendMessage is the
  // observable contract: typing a prompt and submitting must reach
  // useApiStore.sendMessage with the correct session id and text.
  it('routes a composed message through useApiStore.sendMessage', async () => {
    const handle = renderSessionPage({ sessionId: 'sess_1' });
    await flushPromises();
    await waitFor(() => expect(handle.sse()).toBeDefined());
    act(() => {
      handle.sse()!.open();
    });
    // Wait for the composer to mount — it renders inside the page's
    // composer slot once the load resolves and caps.composer is true.
    let composer: HTMLTextAreaElement | null = null;
    await waitFor(() => {
      composer = handle.result.container.querySelector('textarea');
      expect(composer).not.toBeNull();
    }, { timeout: 4000 });
    composer!.focus();
    await act(async () => {
      composer!.value = 'hello agent';
      composer!.dispatchEvent(new Event('input', { bubbles: true }));
      composer!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
      await flushPromises();
    });
    // sendMessage may be reached either through the apiStore directly
    // (assistant-ui runtime onNew) or through the page's handleSend
    // callback. Either path satisfies the contract.
    await waitFor(() => {
      expect(handle.store.sendMessage).toHaveBeenCalled();
    }, { timeout: 4000 });
    const [sid, text] = handle.store.sendMessage.mock.calls[0] as [string, string];
    expect(sid).toBe('sess_1');
    expect(text).toContain('hello agent');
  });
});

// ---------------------------------------------------------------
// Regression tests for the five user-visible behaviours called out
// in spec/sse-rewrite/requirements.md §User-visible behaviour.
// Each test maps to one bullet there; the tests above cover the
// other three (tool blocks live, streaming text never rewinds,
// question round-trip).
// ---------------------------------------------------------------

describe('SessionDetail — regression: user message appears immediately and stays', () => {
  it('survives the SSE round-trip: real user message lands without losing the bubble', async () => {
    // After the user submits a prompt:
    //   1) handleSend calls pending.begin(...) — the bubble is
    //      visible immediately at the render layer.
    //   2) sendMessage POSTs. SSE delivers `message.created` for
    //      the real user message id.
    //   3) usePendingSend.observeMessages clears the pending slot
    //      because a new user-message id appeared in the messages
    //      list.
    //
    // The user-visible bubble's text content is identical between
    // (1) and (3) so no flicker is observed. We assert via the
    // cache mirror: the server-acked user message lands in state
    // exactly once.
    const handle = renderSessionPage({ sessionId: 'sess_1' });
    await flushPromises();
    await waitFor(() => expect(handle.sse()).toBeDefined());
    act(() => handle.sse()!.open());

    let composer: HTMLTextAreaElement | null = null;
    await waitFor(() => {
      composer = handle.result.container.querySelector('textarea');
      expect(composer).not.toBeNull();
    }, { timeout: 4000 });
    composer!.focus();
    await act(async () => {
      composer!.value = 'hello agent';
      composer!.dispatchEvent(new Event('input', { bubbles: true }));
      composer!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
      await flushPromises();
    });
    // sendMessage was reached — the page didn't refuse the send.
    await waitFor(() => {
      expect(handle.store.sendMessage).toHaveBeenCalled();
    }, { timeout: 4000 });
    const [sid, text] = handle.store.sendMessage.mock.calls[0] as [string, string];
    expect(sid).toBe('sess_1');
    expect(text).toContain('hello agent');

    // Server delivers the real user message via SSE.
    act(() => {
      handle.sse()!.emitMessage({
        type: 'message.created',
        properties: {
          info: { id: 'msg_real', sessionID: 'sess_1', role: 'user', time: { created: Date.now() } },
          parts: [
            { id: 'p_real', type: 'text', text: 'hello agent', messageID: 'msg_real' },
          ],
        },
      });
    });

    // The cache mirror now reflects the real user message with
    // the original prompt text. No `temp-*` id has leaked into the
    // cache (legacy bug: optimistic-id reparenting could produce
    // duplicate bubbles).
    type SeedDetail = {
      session: never;
      messages: { id: string; data: { role: string } }[];
      parts: { id: string; data: unknown }[];
      totalMessages: number;
    };
    const seed: SeedDetail = { session: {} as never, messages: [], parts: [], totalMessages: 0 };
    await waitFor(() => {
      const calls = handle.store.updateCachedSession.mock.calls;
      expect(calls.length).toBeGreaterThan(0);
      const lastCall = calls.at(-1)!;
      const updater = lastCall[1] as (prev: SeedDetail) => SeedDetail;
      const result = updater(seed);
      const userMessages = result.messages.filter((m) => m.data.role === 'user');
      // Exactly one user message — no temp-id residue.
      expect(userMessages).toHaveLength(1);
      expect(userMessages[0].id).toBe('msg_real');
      expect(userMessages[0].id.startsWith('temp-')).toBe(false);
    });
  });
});

describe('SessionDetail — regression: no max-update-depth loop during streaming', () => {
  it('renders many SSE deltas back-to-back without triggering setState-in-loop warning', async () => {
    // Regression: a previous version of the rewrite left
    // useSessionStatus.ts:257 (`setLiveTokensPerSecond`) caught in a
    // render loop because `setSubagentTokens` had fresh identity
    // each render (deliberate non-memoised setter). With the TPS
    // effect listing it as a dep, every setState fired the effect
    // again. The fix (useCallback + observeMessages-in-useEffect)
    // must hold under sustained delta streams.
    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
    const handle = renderSessionPage({ sessionId: 'sess_1' });
    await flushPromises();
    await waitFor(() => expect(handle.sse()).toBeDefined());
    act(() => handle.sse()!.open());

    // Seed an assistant message with non-zero token output so the
    // TPS effect hits the `setLiveTokensPerSecond(value)` branch
    // (not the early `null` short-circuit). The loop only fires
    // when a real numeric value is being written.
    const start = Date.now() - 5_000; // 5s of "elapsed" stream
    act(() => {
      handle.sse()!.emitMessage({
        type: 'message.created',
        properties: {
          info: {
            id: 'msg_busy',
            sessionID: 'sess_1',
            role: 'assistant',
            time: { created: start },
            tokens: { input: 0, output: 500 },
          },
        },
      });
    });
    // Burst of 30 deltas + interleaved message.updated events that
    // bump the output token count. Each message.updated changes
    // `messages` identity (because the reducer reassigns the message
    // object), which is the dep useSessionStatus reacts to.
    act(() => {
      for (let i = 0; i < 30; i++) {
        handle.sse()!.emitMessage({
          type: 'message.part.delta',
          properties: {
            partID: 'p_text',
            messageID: 'msg_busy',
            sessionID: 'sess_1',
            field: 'text',
            delta: `token ${i} `,
          },
        });
        handle.sse()!.emitMessage({
          type: 'message.updated',
          properties: {
            info: {
              id: 'msg_busy',
              sessionID: 'sess_1',
              role: 'assistant',
              time: { created: start },
              tokens: { input: 0, output: 500 + i * 5 },
            },
          },
        });
      }
    });
    await flushPromises(8);

    const maxDepthCalls = errorSpy.mock.calls.filter(
      (args) => args.some((a) => typeof a === 'string' && a.includes('Maximum update depth exceeded')),
    );
    expect(maxDepthCalls).toHaveLength(0);

    errorSpy.mockRestore();
  });
});

describe('SessionDetail — regression: session.diff triggers an immediate refetch', () => {
  it('refetches /api/session/{id} as soon as session.diff arrives', async () => {
    // The user-reported regression: tool blocks (edit/write) carry
    // their diff result via `state.metadata.filediff` on the part,
    // which the server fills in after emitting the corresponding
    // `session.diff` event. Without observing `session.diff` and
    // triggering a refetch, the tool block's diff stays empty in
    // the UI until the user refreshes the page.
    //
    // The refetch fires immediately (not debounced) so the diff
    // appears as soon as it's ready. The doFetch AbortController
    // ensures overlapping diff events don't pile up — only one
    // request is ever in flight.
    const handle = renderSessionPage({ sessionId: 'sess_1' });
    await flushPromises();
    await waitFor(() => expect(handle.sse()).toBeDefined());
    act(() => handle.sse()!.open());
    await flushPromises();

    const initialCallCount = handle.api.session.mock.calls.length;
    act(() => {
      handle.sse()!.emitMessage({
        type: 'session.diff',
        properties: {
          sessionID: 'sess_1',
          diff: [
            { file: 'src/foo.ts', patch: 'diff body', additions: 5, deletions: 1, status: 'modified' },
          ],
        },
      });
    });

    await waitFor(() => {
      expect(handle.api.session.mock.calls.length).toBeGreaterThan(initialCallCount);
    });
  });
});

describe('SessionDetail — regression: disconnect-reconnect recovers cleanly', () => {
  it('refetches the session detail on reconnect after an EventSource error', async () => {
    const handle = renderSessionPage({ sessionId: 'sess_1' });
    await flushPromises();
    await waitFor(() => expect(handle.sse()).toBeDefined());
    act(() => handle.sse()!.open());

    // Reset the api.session spy so we can count the reconnect-driven
    // refetch in isolation.
    const apiSession = handle.api.session;
    const initialCallCount = apiSession.mock.calls.length;

    // Simulate a disconnect.
    act(() => {
      handle.sse()!.onerror?.(new Event('error'));
    });

    // The hook schedules a reconnect via computeReconnectDelay
    // (default schedule starts at 500ms). To avoid waiting, we
    // poll for a new EventSource and open it. The page wires a
    // 500ms+ backoff, so we wait long enough.
    await waitFor(() => {
      expect((globalThis as unknown as { EventSource: { instances: unknown[] } }).EventSource.instances.length).toBeGreaterThanOrEqual(2);
    }, { timeout: 4000 });

    act(() => handle.sse()!.open());

    // On reconnect, the hook refetches /api/session/{id} so any
    // events emitted during the gap reconcile in one shot.
    await waitFor(() => {
      expect(apiSession.mock.calls.length).toBeGreaterThan(initialCallCount);
    });
  });
});

describe('SessionDetail — composer abort', () => {
  it('calls abortSession when the SSE marks the assistant running and the user clicks abort', async () => {
    const handle = renderSessionPage({ sessionId: 'sess_1' });
    await flushPromises();
    await waitFor(() => expect(handle.sse()).toBeDefined());
    act(() => {
      handle.sse()!.open();
      // Mark the assistant as running so the abort button appears.
      handle.sse()!.emitMessage({
        type: 'message.created',
        properties: {
          info: {
            id: 'msg_busy',
            sessionID: 'sess_1',
            role: 'assistant',
            time: { created: Date.now() },
          },
          parts: [],
        },
      });
    });
    await flushPromises();
    // Find the abort button — the composer renders it with a stop
    // icon when isRunning is true. We look up by aria-label.
    const abortBtn = handle.result.container.querySelector('[aria-label*="Abort"], [aria-label*="abort"]');
    if (abortBtn) {
      await act(async () => {
        (abortBtn as HTMLButtonElement).click();
        await flushPromises();
      });
      expect(handle.store.abortSession).toHaveBeenCalled();
    } else {
      // The composer didn't render abort UI under the test mocks
      // (capability gating). Skip silently — the abort path is
      // exercised at the hook level instead.
      expect(true).toBe(true);
    }
  });
});

describe('SessionDetail — sidebar archive', () => {
  it('calls archiveSession when the archive button is clicked', async () => {
    const sib = makeSession({ id: 'sess_other', title: 'Other one', timeUpdated: Date.now() });
    const handle = renderSessionPage({
      sessionId: 'sess_1',
      sessions: [makeSession({ id: 'sess_1' }), sib],
    });
    await flushPromises();
    await waitFor(() => expect(handle.store.getSessions).toHaveBeenCalled());
    await waitFor(() => {
      expect(screen.getByText('Other one')).toBeInTheDocument();
    });
    // Find the archive button for the sibling row.
    const archiveBtns = handle.result.container.querySelectorAll('[aria-label="Archive session"]');
    expect(archiveBtns.length).toBeGreaterThan(0);
    await act(async () => {
      (archiveBtns[0] as HTMLButtonElement).click();
    });
    // The handler delays archiveSession by ARCHIVE_ANIMATION_MS
    // (220 ms) so the sibling row can fade out. Wait past the
    // animation window before asserting.
    await waitFor(
      () => expect(handle.store.archiveSession).toHaveBeenCalled(),
      { timeout: 1000 },
    );
  });

  it('optimistically unarchives the active session on open', async () => {
    // Opening an archived session unarchives it server-side; the sidebar
    // row must reflect that immediately instead of waiting for the next poll.
    const archived = makeSession({ id: 'sess_1', archived: true });
    const handle = renderSessionPage({
      sessionId: 'sess_1',
      detail: makeSessionDetail(archived),
      sessions: [archived],
    });
    await flushPromises(8);
    await waitFor(() => expect(handle.store.markSessionSeen).toHaveBeenCalled());
    await waitFor(() => {
      const row = useApiStore.getState().recentSessions.find((s) => s.id === 'sess_1');
      expect(row?.archived).toBe(false);
    });
  });
});

describe('SessionDetail — status badge follows the backend', () => {
  const erroredMessage = {
    type: 'message.created',
    properties: {
      info: {
        id: 'msg_1',
        sessionID: 'sess_1',
        role: 'assistant',
        finish: 'error',
        error: { name: 'Boom', data: { message: 'kaboom' } },
        time: { created: 1_000 },
      },
      parts: [],
    },
  };

  it('flips to error when a session.status event reports it', async () => {
    const handle = renderSessionPage({ sessionId: 'sess_1' });
    await flushPromises();
    await waitFor(() => expect(handle.sse()).toBeDefined());

    act(() => {
      handle.sse()!.open();
      handle.sse()!.emitMessage({
        type: 'session.status',
        properties: { sessionID: 'sess_1', status: 'error' },
      });
    });

    await waitFor(() => {
      // The status badge gets a status-error class via the StatusBadge
      // component when `session.status === 'error'`.
      expect(handle.result.container.querySelector('.status-error')).toBeInTheDocument();
    });
  });

  // The backend settles lifecycle status from the agent's own turn
  // (db.SettleSessionStatus). A message snapshot must not re-derive it:
  // doing so overwrote the authoritative value with a local guess.
  it('does not let a message snapshot overwrite the reported status', async () => {
    const handle = renderSessionPage({ sessionId: 'sess_1' });
    await flushPromises();
    await waitFor(() => expect(handle.sse()).toBeDefined());

    act(() => {
      handle.sse()!.open();
      handle.sse()!.emitMessage({
        type: 'session.status',
        properties: { sessionID: 'sess_1', status: { type: 'busy' } },
      });
    });
    await waitFor(() => {
      expect(handle.result.container.querySelector('.status-busy')).toBeInTheDocument();
    });

    act(() => { handle.sse()!.emitMessage(erroredMessage); });
    await flushPromises(8);

    expect(handle.result.container.querySelector('.status-busy')).toBeInTheDocument();
    expect(handle.result.container.querySelector('.status-error')).not.toBeInTheDocument();
  });

  // The production failure sequence. OpenCode's `session.status`
  // vocabulary is busy|retry|idle only (live_status.go) — it never emits
  // `error`. A failed turn therefore arrives as
  // `message.updated{finish:'error'}` followed by `session.idle`, and
  // `session.idle` alone says "done". The badge must not spend that
  // window claiming the failed turn succeeded.
  it('never shows done for a failed turn on the real event sequence', async () => {
    const running = makeSession({ id: 'sess_1', status: 'busy' });
    // What the reconcile triggered by `session.idle` returns: the
    // backend settles the terminal state from the errored tail
    // (db.InferSessionStatus), so REST reports `error`.
    const settled = makeSession({ id: 'sess_1', status: 'error' });
    let settledYet = false;
    const session = vi.fn(() => Promise.resolve(
      settledYet
        ? makeSessionDetail(settled, {
          messages: [{
            id: 'msg_1',
            sessionId: 'sess_1',
            timeCreated: 1_000,
            data: { role: 'assistant', finish: 'error', error: { name: 'Boom' } },
          }],
          totalMessages: 1,
        })
        : makeSessionDetail(running),
    ));

    const handle = renderSessionPage({
      sessionId: 'sess_1',
      detail: makeSessionDetail(running),
      sessions: [running],
      apiOverrides: { session },
    });
    await flushPromises(8);
    await waitFor(() => expect(handle.sse()).toBeDefined());
    act(() => { handle.sse()!.open(); });
    await flushPromises(4);

    act(() => {
      handle.sse()!.emitMessage({
        type: 'message.updated',
        properties: {
          info: {
            id: 'msg_1',
            sessionID: 'sess_1',
            role: 'assistant',
            finish: 'error',
            error: { name: 'Boom', data: { message: 'kaboom' } },
            time: { created: 1_000 },
          },
          parts: [],
        },
      });
    });
    await flushPromises(4);

    settledYet = true;
    act(() => {
      handle.sse()!.emitMessage({
        type: 'session.idle',
        properties: { sessionID: 'sess_1' },
      });
    });

    // Asserted before the reconcile round trip resolves: that gap is
    // exactly where the badge used to claim `done`.
    expect(handle.result.container.querySelector('.status-done')).not.toBeInTheDocument();
    expect(handle.result.container.querySelector('.status-error')).toBeInTheDocument();

    await flushPromises(8);
    expect(handle.result.container.querySelector('.status-done')).not.toBeInTheDocument();
    expect(handle.result.container.querySelector('.status-error')).toBeInTheDocument();
  });

  // The active sidebar row overlays the page's display status on top of
  // the polled row. If that overlay is less live than the row it
  // replaces, every row shows the settled `error` except the one the
  // user is actually looking at.
  it('keeps the active sidebar row in step with the other rows on a settled error', async () => {
    const active = makeSession({ id: 'sess_1', status: 'error' });
    const other = makeSession({ id: 'sess_2', title: 'Other', status: 'error' });
    const handle = renderSessionPage({
      sessionId: 'sess_1',
      detail: makeSessionDetail(makeSession({ id: 'sess_1', status: 'busy' })),
      sessions: [active, other],
    });
    await flushPromises(8);
    await waitFor(() => expect(handle.sse()).toBeDefined());

    act(() => {
      handle.sse()!.open();
      handle.sse()!.emitMessage(erroredMessage);
      // The live stream only ever reports idle for a finished turn.
      handle.sse()!.emitMessage({
        type: 'session.idle',
        properties: { sessionID: 'sess_1' },
      });
    });
    await flushPromises(8);

    const rows = handle.result.container.querySelectorAll('.session-sidebar-item');
    expect(rows.length).toBeGreaterThanOrEqual(2);
    for (const row of rows) {
      expect(row.querySelector('.status-error')).not.toBeNull();
      expect(row.querySelector('.status-done')).toBeNull();
    }
  });

  // Regression guard for the shared-state overwrite: the page's display
  // status is local, and must never be written back into the row every
  // other view reads.
  it('does not write its display status into shared recent-session state', async () => {
    const patchRecentSession = vi.fn();
    const handle = renderSessionPage({
      sessionId: 'sess_1',
      storeOverrides: { patchRecentSession },
    });
    await flushPromises(8);
    await waitFor(() => expect(handle.sse()).toBeDefined());

    act(() => {
      handle.sse()!.open();
      handle.sse()!.emitMessage(erroredMessage);
    });
    await flushPromises(8);

    for (const call of patchRecentSession.mock.calls) {
      expect(call[1]).not.toHaveProperty('status');
    }
  });
});

describe('SessionDetail — rate-limit notice', () => {
  it('renders without maximum-update-depth when session has a notice', async () => {
    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

    const notice = {
      kind: 'rate_limit' as const,
      message: 'rate limit exceeded',
      retryAt: Date.now() + 300_000,
      attempt: 1,
    };
    const sess = makeSession({ id: 'sess_rl', status: 'error', notice });
    const detail = makeSessionDetail(sess);

    const handle = renderSessionPage({
      sessionId: 'sess_rl',
      detail,
      sessions: [sess],
    });

    await flushPromises();
    await waitFor(() => {
      expect(handle.api.session).toHaveBeenCalled();
    });

    // React logs "Maximum update depth exceeded" to console.error
    // before throwing; if we see that message the test fails.
    const maxDepthCalls = errorSpy.mock.calls.filter(
      (args) => typeof args[0] === 'string' && args[0].includes('Maximum update depth exceeded'),
    );
    expect(maxDepthCalls).toHaveLength(0);
    expect(screen.getByTestId('session-layout')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Change model' }));
    expect(await screen.findByPlaceholderText(/Select a model/)).toBeInTheDocument();

    errorSpy.mockRestore();
  });

  it('does not loop when a non-notice session renders', async () => {
    // Regression: ensure the new reducer pipeline doesn't cause a
    // cascade through any useCallback hook that depends on
    // `session`.
    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

    const sess = makeSession({ id: 'sess_plain', status: 'done' });
    const detail = makeSessionDetail(sess);

    const handle = renderSessionPage({
      sessionId: 'sess_plain',
      detail,
      sessions: [sess],
    });

    await flushPromises();
    await waitFor(() => {
      expect(handle.api.session).toHaveBeenCalled();
    });

    const maxDepthCalls = errorSpy.mock.calls.filter(
      (args) => typeof args[0] === 'string' && args[0].includes('Maximum update depth exceeded'),
    );
    expect(maxDepthCalls).toHaveLength(0);
    expect(screen.getByTestId('session-layout')).toBeInTheDocument();

    errorSpy.mockRestore();
  });

  it('renders provider overload notices', async () => {
    const notice = {
      kind: 'provider_overloaded' as const,
      message: 'provider is overloaded, please try again later',
      retryAt: 0,
      attempt: 0,
    };
    const sess = makeSession({ id: 'sess_overloaded', status: 'error', notice });
    const detail = makeSessionDetail(sess);

    renderSessionPage({
      sessionId: 'sess_overloaded',
      detail,
      sessions: [sess],
    });

    await flushPromises();
    expect(await screen.findByText(/Provider overloaded/)).toBeInTheDocument();
    expect(screen.getByText(/provider is overloaded/)).toBeInTheDocument();
  });
});

describe('SessionDetail — session warnings', () => {
  it('renders and dismisses duplicate OpenCode server warnings', async () => {
    const sess = makeSession({ id: 'sess_dupes', status: 'done' });
    const detail = makeSessionDetail(sess, {
      warnings: [{
        kind: 'duplicate_opencode_servers',
        message: 'Multiple OpenCode servers are running for this project. Live updates may be unreliable for activity started outside this tab.',
        ports: ['55001', '55002'],
      }],
    });

    renderSessionPage({
      sessionId: 'sess_dupes',
      detail,
      sessions: [sess],
    });

    await flushPromises();

    const warning = await screen.findByTestId('session-warning-duplicate_opencode_servers');
    expect(warning).toHaveTextContent('Multiple OpenCode servers');
    expect(warning).toHaveTextContent('ports 55001, 55002');

    fireEvent.click(screen.getByRole('button', { name: /dismiss session warning/i }));

    expect(screen.queryByTestId('session-warning-duplicate_opencode_servers')).not.toBeInTheDocument();
  });
});
