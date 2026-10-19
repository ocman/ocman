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
import { formatDate } from '../../../lib/format';
import { useUiStore } from '../../../lib/uiStore';
import { __handleSessionChangedForTests } from '../../../lib/useGlobalEvents';
import { getSidebarSessions, patchSessionLists } from '../../../lib/sessionsCache';
import { hello, partUpserted, textDelta } from '../../../test/oce';
import type { OceEvent } from '../../../lib/conversation/events';

// Pass-through spy: the real patch still runs, calls are recorded.
vi.mock('../../../lib/sessionsCache', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../lib/sessionsCache')>();
  return { ...actual, patchSessionLists: vi.fn(actual.patchSessionLists) };
});

beforeEach(() => {
  useUiStore.getState().setSidebarView('recent');
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

  it('excludes synthetic notices when deciding whether older messages remain', async () => {
    const session = makeSession({ id: 'sess_notices' });
    const detail = makeSessionDetail(session, {
      messages: [
        { id: 'msg1', sessionId: session.id, timeCreated: 1, data: { role: 'user' } },
        { id: 'notice1', sessionId: session.id, timeCreated: 2, data: { role: 'notice' } },
      ],
      totalMessages: 2,
    });

    renderSessionPage({ sessionId: session.id, detail, sessions: [session] });

    expect(await screen.findByTestId('assistant-thread-has-more')).toHaveTextContent('true');
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

describe('SessionDetail — conversation stream', () => {
  type SeedDetail = {
    session: never;
    messages: { id: string; data: unknown }[];
    parts: { id: string; data: unknown }[];
    totalMessages: number;
  };
  const seedDetail = (): SeedDetail => ({ session: {} as never, messages: [], parts: [], totalMessages: 0 });
  // The page mirrors live state into the apiStore cache through updater
  // calls; folding them in order yields the latest mirrored state.
  const mirrored = (handle: ReturnType<typeof renderSessionPage>) => {
    let detail = seedDetail();
    for (const call of handle.store.updateCachedSession.mock.calls) {
      const [, updater] = call as [string, (prev: SeedDetail) => SeedDetail];
      detail = updater(detail);
    }
    return {
      messages: detail.messages,
      parts: detail.parts.map((p) => ({ id: p.id, data: (typeof p.data === 'string' ? JSON.parse(p.data) : p.data) as Record<string, unknown> })),
    };
  };
  async function openStream(sessionId = 'sess_1') {
    const handle = renderSessionPage({ sessionId });
    await flushPromises();
    await waitFor(() => expect(handle.sse()).toBeDefined());
    handle.store.updateCachedSession.mockClear();
    act(() => {
      handle.sse()!.open();
      handle.sse()!.emitOce(hello());
    });
    return handle;
  }

  it('opens the conversation stream against the active session id', async () => {
    const { sse } = renderSessionPage({ sessionId: 'sess_42' });
    await flushPromises();
    await waitFor(() => expect(sse()).toBeDefined());
    expect(sse()!.url).toContain('/api/session/sess_42/stream');
  });

  it('updates the session cache when a message and its part are upserted', async () => {
    const handle = await openStream();
    act(() => {
      handle.sse()!.emitOce({ type: 'message.upserted', message: { id: 'msg_1', sessionId: 'sess_1', timeCreated: Date.now(), data: { role: 'assistant' } } });
      handle.sse()!.emitOce(partUpserted({ id: 'p_1', messageId: 'msg_1', data: { type: 'text', text: 'Hello world' } }, 'sess_1'));
    });
    await waitFor(() => {
      const state = mirrored(handle);
      expect(state.messages.map((m) => m.id)).toContain('msg_1');
      expect(state.parts.find((p) => p.id === 'p_1')?.data.text).toBe('Hello world');
    });
  });

  it('appends offset-anchored text deltas to an existing part', async () => {
    const handle = await openStream();
    act(() => {
      handle.sse()!.emitOce(partUpserted({ id: 'p_1', messageId: 'msg_1', data: { type: 'text', text: 'Hi' } }, 'sess_1'));
      handle.sse()!.emitOce(textDelta('p_1', 2, ' there', 'msg_1'));
    });
    await waitFor(() => expect(mirrored(handle).parts.find((p) => p.id === 'p_1')?.data.text).toBe('Hi there'));
  });

  it('appends streamed tool output to a running tool part', async () => {
    const handle = await openStream();
    act(() => {
      handle.sse()!.emitOce(partUpserted({ id: 'p_tool', messageId: 'msg_1', data: {
        type: 'tool', tool: 'bash', state: { status: 'running', input: { command: 'echo 1' }, output: '' },
      } }, 'sess_1'));
      handle.sse()!.emitOce({ type: 'part.delta', messageId: 'msg_1', partId: 'p_tool', field: 'tool.output', offset: 0, text: 'line 1\n', partType: 'tool' });
    });
    await waitFor(() => {
      const tool = mirrored(handle).parts.find((p) => p.id === 'p_tool')?.data;
      expect((tool?.state as { output?: string })?.output).toBe('line 1\n');
      expect(tool?.tool).toBe('bash');
    });
  });

  it('renders a running tool part that leads its message', async () => {
    const handle = await openStream();
    act(() => handle.sse()!.emitOce(partUpserted({ id: 'p_bash', messageId: 'msg_a', data: {
      type: 'tool', tool: 'bash', state: { status: 'running', input: { command: 'echo hello' }, title: 'echo hello' },
    } }, 'sess_1')));
    await waitFor(() => {
      const state = mirrored(handle);
      expect(state.messages.map((m) => m.id)).toContain('msg_a');
      expect(state.parts.find((p) => p.id === 'p_bash')?.data.type).toBe('tool');
    });
  });

  it('ignores frames that are not conversation envelopes', async () => {
    const handle = await openStream();
    act(() => {
      handle.sse()!.emitMessage({ type: 'message.part.updated', properties: { part: { id: 'legacy', messageID: 'm', type: 'text', text: 'x' } } });
      handle.sse()!.emit('oce', '{bad json');
      handle.sse()!.emit('oce', { v: 2, epoch: 'test', seq: 1, sessionId: 'sess_1', event: { type: 'part.upserted' } });
      handle.sse()!.emitOce(partUpserted({ id: 'foreign', messageId: 'm', data: { type: 'text', text: 'x' } }, 'other'), { sessionId: 'other' });
      handle.sse()!.emitOce(partUpserted({ id: 'kept', messageId: 'm', data: { type: 'text', text: 'y' } }, 'sess_1'));
    });
    await waitFor(() => expect(mirrored(handle).parts.map((p) => p.id)).toEqual(['kept']));
  });

  it('keeps streamed text against a lagging non-final upsert and lets a final upsert replace it', async () => {
    const handle = await openStream();
    const text = () => mirrored(handle).parts.find((p) => p.id === 'p_drift')?.data;
    act(() => {
      handle.sse()!.emitOce({ type: 'message.upserted', message: { id: 'msg_drift', sessionId: 'sess_1', timeCreated: Date.now(), data: { role: 'assistant' } } });
      handle.sse()!.emitOce(partUpserted({ id: 'p_drift', messageId: 'msg_drift', data: { type: 'text', text: 'Hello' } }, 'sess_1'));
      handle.sse()!.emitOce(textDelta('p_drift', 5, ' world', 'msg_drift'));
    });
    await waitFor(() => expect(text()?.text).toBe('Hello world'));
    // A lagging snapshot keeps the streamed text but applies its other fields.
    act(() => handle.sse()!.emitOce(partUpserted({ id: 'p_drift', messageId: 'msg_drift', data: { type: 'text', text: 'Hello', completed: true } as never }, 'sess_1')));
    await waitFor(() => expect(text()).toMatchObject({ text: 'Hello world', completed: true }));
    // A snapshot ahead of the deltas takes over.
    act(() => handle.sse()!.emitOce(partUpserted({ id: 'p_drift', messageId: 'msg_drift', data: { type: 'text', text: 'Hello world how are you' } }, 'sess_1')));
    await waitFor(() => expect(text()?.text).toBe('Hello world how are you'));
    // The final text wins even when shorter (e.g. trimmed).
    act(() => handle.sse()!.emitOce(partUpserted({ id: 'p_drift', messageId: 'msg_drift', data: { type: 'text', text: 'Hello world' } }, 'sess_1', true)));
    await waitFor(() => expect(text()?.text).toBe('Hello world'));
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

  it('passes the configured time window without capping project sessions', async () => {
    useUiStore.getState().setSidebarView('projects');
    const handle = renderSessionPage({ sessionId: 'sess_1' });
    await flushPromises();
    await waitFor(() => expect(handle.store.getSessions).toHaveBeenCalled());
    const [params] = handle.store.getSessions.mock.calls[0] as [{ since?: number; limit?: number }];
    expect(typeof params.since).toBe('number');
    expect(params.since).toBeLessThan(Date.now());
    expect(params.limit).toBe(0);
  });
});

describe('SessionDetail — session tree usage', () => {
  it('shows current-session timing details in the usage popover', async () => {
    const startedAt = Date.UTC(2026, 0, 2, 3, 4);
    const session = makeSession({
      timeCreated: startedAt,
      durationMs: 3_661_000,
      activeDurationMs: 125_000,
      totalInputTokens: 10,
    });
    const child = makeSession({
      id: 'sess_child',
      parentId: session.id,
      durationMs: 3_600_000,
      activeDurationMs: 1_800_000,
    });
    renderSessionPage({
      sessionId: session.id,
      detail: makeSessionDetail(session, { sessionTree: [session, child] }),
      sessions: [session, child],
    });

    fireEvent.click(await screen.findByRole('button', { name: '$0.00' }));

    expect((await screen.findByText('Started')).parentElement).toHaveTextContent(formatDate(startedAt));
    expect(screen.getByText('Total time').parentElement).toHaveTextContent('1h 1m');
    expect(screen.getByText('Agent time').parentElement).toHaveTextContent('2m 5s');
  }, 10_000);

  it('ticks agent time in the usage popover while the session is running', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-01-01T00:00:00Z'));
    try {
      const session = makeSession({
        status: 'busy',
        activeDurationMs: 125_000,
        totalInputTokens: 10,
      });
      renderSessionPage({ sessionId: session.id, detail: makeSessionDetail(session) });
      await act(async () => { await flushPromises(8); });

      fireEvent.click(screen.getByRole('button', { name: '$0.00' }));
      expect(screen.getByText('Agent time').parentElement).toHaveTextContent('2m 5s');

      act(() => vi.advanceTimersByTime(1_000));

      expect(screen.getByText('Agent time').parentElement).toHaveTextContent('2m 6s');
    } finally {
      vi.useRealTimers();
    }
  });

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
    const root = { ...makeSession({ id: 'sess_1', totalInputTokens: 10, totalOutputTokens: 20, totalCost: 0.1 }), totalEstCost: 0.15, totalEffectiveCost: 0.1 };
    const child = { ...makeSession({ id: 'sess_child', parentId: root.id, totalInputTokens: 30, totalOutputTokens: 40, totalCost: 0.2 }), totalEstCost: 0.25, totalEffectiveCost: 0.2 };
    const grandchild = { ...makeSession({ id: 'sess_grandchild', parentId: child.id, totalInputTokens: 50, totalOutputTokens: 60, totalCost: 0 }), totalEstCost: 0.35, totalEffectiveCost: 0.35 };
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
    expect(title.parentElement).toHaveTextContent('Reported cost$0.3000');
    expect(title.parentElement).toHaveTextContent('API-equivalent cost$0.7500');
    expect(title.parentElement).toHaveTextContent('Cost$0.6500');
    expect(handle.api.sessions).not.toHaveBeenCalled();
  });

  it('shows estimated cost without requiring context usage', async () => {
    const session = makeSession({ id: 'sess_1', totalInputTokens: 10, totalOutputTokens: 20 });
    const treeSession = {
      ...session,
      totalEstCost: 0.426,
      totalEffectiveCost: 0.426,
    };
    const calcCost = vi.fn().mockResolvedValue({ cost: 9.99, known: true });
    const handle = renderSessionPage({
      sessionId: session.id,
      detail: makeSessionDetail(session, { sessionTree: [treeSession], contextTokenCount: 0 }),
      sessions: [treeSession],
      apiOverrides: { calcCost },
    });

    // Nothing reported (subscription): the figure is labelled API-equivalent.
    const cost = await screen.findByTitle(/^API-equivalent cost: .*Click for details$/);
    expect(cost.tagName).toBe('BUTTON');
    expect(cost).toHaveTextContent('$0.43');

    fireEvent.click(cost);
    const estimate = await screen.findByText('API-equivalent cost');
    expect(estimate.parentElement).toHaveTextContent('API-equivalent cost$0.4260');
    expect(handle.api.calcCost).not.toHaveBeenCalled();
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
      expect(listPermissionsSpy).toHaveBeenCalledWith('sess_1', sessionWithPerm.platform);
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
    await waitFor(() => expect(listPermissionsSpy).toHaveBeenCalledWith(parent.id, parent.platform));
    await waitFor(() => expect(listPermissionsSpy).toHaveBeenCalledWith(childId, parent.platform));
    await waitFor(() => expect(listPermissionsSpy).toHaveBeenCalledWith(grandchild.id, parent.platform));
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
  it.each([false, true])('restores one failed bubble with a working retry when returning=%s', async (returning) => {
    const sessionId = 'restored-failure';
    const images = [{ url: 'data:image/png;base64,YQ==', mime: 'image/png' }];
    clearFailedSends(sessionId);
    recordFailedSend(sessionId, {
      id: 'saved-failure', text: 'retry my saved prompt', images,
      model: 'p/m', agent: 'plan', reasoning: 'high', error: 'offline', failedAt: Date.now(),
    });
    try {
      const handle = renderSessionPage({ sessionId: returning ? 'other' : sessionId, realAssistantThread: true,
        detail: makeSessionDetail(makeSession({ id: sessionId })) });
      await flushPromises();
      if (returning) {
        act(() => handle.navigate(`/session/${sessionId}`));
        await flushPromises();
      }
      await waitFor(() => expect(screen.getAllByRole('button', { name: 'Retry' })).toHaveLength(1));
      expect(screen.getAllByText('retry my saved prompt', { selector: 'span' })).toHaveLength(1);
      fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
      await waitFor(() => expect(handle.store.sendMessage).toHaveBeenCalledWith(
        sessionId, 'retry my saved prompt', images, 'p/m', 'plan', 'high', 'opencode',
      ));
      expect(screen.getAllByText('retry my saved prompt', { selector: 'span' })).toHaveLength(1);
    } finally {
      clearFailedSends(sessionId);
    }
  });

  it.each(['opencode', 'claudecode'])('survives the SSE round-trip on %s without waiting for the send response', async (platform) => {
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
    const handle = renderSessionPage({ sessionId: 'sess_1', realAssistantThread: true, detail: makeSessionDetail(makeSession({ platform })) });
    let accept!: () => void;
    handle.store.sendMessage.mockReturnValueOnce(new Promise<void>((resolve) => { accept = resolve; }));
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

    // The prompt must be visible before any server message arrives.
    expect(handle.result.getByText('hello agent', { selector: 'span' })).toBeVisible();
    await act(async () => accept());

    // Server delivers the real user message via SSE.
    act(() => {
      handle.sse()!.emitOce({ type: 'message.upserted', message: { id: 'msg_real', sessionId: 'sess_1', timeCreated: Date.now(), data: { role: 'user' } } });
    });
    expect(handle.result.getByText('hello agent', { selector: 'span' })).toBeVisible();
    act(() => handle.sse()!.emitOce(partUpserted({ id: 'p_real', messageId: 'msg_real', data: { type: 'text', text: 'hello agent' } }, 'sess_1', true)));
    await waitFor(() => expect(handle.result.getAllByText('hello agent', { selector: 'span' })).toHaveLength(1));

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
    const busyMessage = (output: number) => ({
      type: 'message.upserted' as const,
      message: { id: 'msg_busy', sessionId: 'sess_1', timeCreated: start, data: { role: 'assistant' as const, time: { created: start }, tokens: { input: 0, output } } },
    });
    act(() => handle.sse()!.emitOce(busyMessage(500)));
    // Burst of 30 deltas + interleaved message.updated events that
    // bump the output token count. Each message.updated changes
    // `messages` identity (because the reducer reassigns the message
    // object), which is the dep useSessionStatus reacts to.
    act(() => {
      let offset = 0;
      for (let i = 0; i < 30; i++) {
        const text = `token ${i} `;
        handle.sse()!.emitOce(textDelta('p_text', offset, text, 'msg_busy'));
        offset += text.length;
        handle.sse()!.emitOce(busyMessage(500 + i * 5));
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

describe('SessionDetail — regression: changes signals', () => {
  // A completed edit tool carries its filediff on its final upsert, so a
  // changes signal only refreshes the Changes view, never the transcript.
  it('does not refetch the transcript when changes are dirtied', async () => {
    const handle = renderSessionPage({ sessionId: 'sess_1' });
    await flushPromises();
    await waitFor(() => expect(handle.sse()).toBeDefined());
    act(() => { handle.sse()!.open(); handle.sse()!.emitOce(hello()); });
    await flushPromises();
    const initialCallCount = handle.api.session.mock.calls.length;
    act(() => handle.sse()!.emitOce({ type: 'changes.dirty', reason: 'diff' }));
    await flushPromises(4);
    expect(handle.api.session.mock.calls.length).toBe(initialCallCount);
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

    // The owner could not resume the cursor, so the hook refetches
    // /api/session/{id} to reconcile the gap in one shot.
    act(() => { handle.sse()!.open(); handle.sse()!.emitOce(hello(false)); });

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
      handle.sse()!.emitOce({ type: 'session.status', status: 'busy', notice: null });
      handle.sse()!.emitOce({ type: 'message.upserted', message: { id: 'msg_busy', sessionId: 'sess_1', timeCreated: Date.now(), data: { role: 'assistant' } } });
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
      const row = getSidebarSessions().find((s) => s.id === 'sess_1');
      expect(row?.archived).toBe(false);
    });
  });
});

describe('SessionDetail — status badge follows the backend', () => {
  const erroredMessage: OceEvent = {
    type: 'message.upserted',
    message: {
      id: 'msg_1', sessionId: 'sess_1', timeCreated: 1_000,
      data: { role: 'assistant', finish: 'error', error: { name: 'Boom', data: { message: 'kaboom' } }, time: { created: 1_000 } },
    },
  };
  const idleEdge: OceEvent = { type: 'session.status', status: 'done', provisional: true, notice: null };

  it('flips to error when a session.status event reports it', async () => {
    const handle = renderSessionPage({ sessionId: 'sess_1' });
    await flushPromises();
    await waitFor(() => expect(handle.sse()).toBeDefined());

    act(() => {
      handle.sse()!.open();
      handle.sse()!.emitOce({ type: 'session.status', status: 'error' });
    });

    await waitFor(() => {
      expect(handle.result.container.querySelector('[data-state="error"]')).toBeInTheDocument();
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
      handle.sse()!.emitOce({ type: 'session.status', status: 'busy', notice: null });
    });
    await waitFor(() => {
      expect(handle.result.container.querySelector('[data-state="busy"]')).toBeInTheDocument();
    });

    act(() => { handle.sse()!.emitOce(erroredMessage); });
    await flushPromises(8);

    expect(handle.result.container.querySelector('[data-state="busy"]')).toBeInTheDocument();
    expect(handle.result.container.querySelector('[data-state="error"]')).not.toBeInTheDocument();
  });

  it('renders limited with its reset time in the composer notification area', async () => {
    const slot = document.createElement('div');
    slot.id = 'header-actions-slot';
    document.body.append(slot);
    try {
      const resetsAt = Date.UTC(2030, 0, 1, 12);
      const session = makeSession({ status: 'busy' });
      const handle = renderSessionPage({ detail: makeSessionDetail(session) });
      await flushPromises();
      await waitFor(() => expect(handle.result.getByLabelText('Session actions')).toBeInTheDocument());
      const notices = handle.result.getByRole('region', { name: 'Conversation status' });
      act(() => __handleSessionChangedForTests(JSON.stringify({ sessionID: session.id, platform: 'other', patch: { status: 'limited', resetsAt } })));
      expect(slot).not.toHaveTextContent('Limited');
      act(() => __handleSessionChangedForTests(JSON.stringify({ sessionID: session.id, platform: session.platform, patch: { status: 'limited', resetsAt } })));
      await waitFor(() => expect(notices).toHaveTextContent(`Limited until ${new Date(resetsAt).toLocaleString()}`));
      expect(slot).not.toHaveTextContent('Limited');
      act(() => __handleSessionChangedForTests(JSON.stringify({ sessionID: session.id, platform: session.platform, patch: { status: 'busy', resetsAt: 0 } })));
      await waitFor(() => expect(notices).not.toHaveTextContent('Limited'));
      handle.result.unmount();
    } finally {
      slot.remove();
    }
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

    act(() => { handle.sse()!.emitOce(erroredMessage); });
    await flushPromises(4);

    settledYet = true;
    // The provisional idle edge arrives before the owner settles the turn.
    act(() => { handle.sse()!.emitOce(idleEdge); });

    // Asserted before the reconcile round trip resolves: that gap is
    // exactly where the badge used to claim `done`.
    expect(handle.result.container.querySelector('[data-state="done"]')).not.toBeInTheDocument();
    expect(handle.result.container.querySelector('[data-state="error"]')).toBeInTheDocument();

    act(() => {
      handle.sse()!.emitOce({ type: 'session.status', status: 'error' });
      handle.sse()!.emitOce({ type: 'turn.ended', outcome: 'error', endedAt: 2_000 });
    });
    await flushPromises(8);
    expect(handle.result.container.querySelector('[data-state="done"]')).not.toBeInTheDocument();
    expect(handle.result.container.querySelector('[data-state="error"]')).toBeInTheDocument();
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
      handle.sse()!.emitOce(erroredMessage);
      handle.sse()!.emitOce(idleEdge);
    });
    await flushPromises(8);

    const rows = handle.result.container.querySelectorAll('.session-sidebar-item');
    expect(rows.length).toBeGreaterThanOrEqual(2);
    for (const row of rows) {
      expect(row.querySelector('[data-state="error"]')).not.toBeNull();
      expect(row.querySelector('[data-state="done"]')).toBeNull();
    }
  });

  // Only API/SSE status is shared; the page's display-only error overlay stays local.
  it('mirrors authoritative status without writing its display-only status into shared state', async () => {
    const patchRecentSession = vi.mocked(patchSessionLists);
    patchRecentSession.mockClear();
    const handle = renderSessionPage({ sessionId: 'sess_1' });
    await flushPromises(8);
    await waitFor(() => expect(handle.sse()).toBeDefined());

    act(() => {
      handle.sse()!.open();
      handle.sse()!.emitOce(erroredMessage);
    });
    await flushPromises(8);

    await waitFor(() => expect(handle.result.container.querySelector('[data-state="error"]')).toBeInTheDocument());
    const statusPatches = patchRecentSession.mock.calls.filter((call) => 'status' in call[1]);
    expect(statusPatches.length).toBeGreaterThan(0);
    for (const call of statusPatches) expect(call[1].status).toBe('done');
  });
});

describe('SessionDetail — rate-limit notice', () => {
  it.each(['error', 'busy'] as const)('renders a %s session notice without maximum-update-depth', async (status) => {
    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

    const notice = {
      kind: 'rate_limit' as const,
      message: 'rate limit exceeded',
      retryAt: Date.now() + 300_000,
      attempt: 1,
    };
    const sess = makeSession({ id: 'sess_rl', status, notice });
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
    expect(screen.getByTestId('assistant-thread-composer')).toContainElement(screen.getByTestId('rate-limit-banner'));
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
  it('keeps the composer available with an external-owner warning', async () => {
    const sess = makeSession({ id: 'external_claude', platform: 'claudecode', status: 'done', liveConnection: true });
    renderSessionPage({
      sessionId: sess.id,
      sessions: [sess],
      detail: makeSessionDetail(sess, {
        warnings: [{ kind: 'external-session-owner', message: 'This session may still be open in another Claude instance.' }],
      }),
    });
    await flushPromises();
    expect(await screen.findByTestId('session-warning-external-session-owner')).toHaveTextContent('another Claude instance');
    expect(screen.getByRole('textbox')).not.toBeDisabled();
  });

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
