// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { memo, startTransition, Suspense, useLayoutEffect, useState } from 'react';
import { act, render, screen } from '@testing-library/react';
import type { Message, Part, TaskSessionData } from './api';
import { createConvertMessages } from './convertMessages';
import { computeTurnStats, createComputeTurnStats, useTurnStats, type TurnStatsMap } from './turnStats';
import { ThreadPrimitive, useAuiState } from '@assistant-ui/react';
import { OcmanRuntimeProvider } from '../components/OcmanRuntimeProvider';
import * as throughput from './throughput';
import type { FailedSend } from './failedSends';

const msg = (id: string, role: 'user' | 'assistant'): Message => ({
  id, sessionId: 's', timeCreated: 1000,
  data: { role, ...(role === 'assistant' ? { finish: 'stop', tokens: { input: 10, output: 20 } } : {}) },
});
const part = (messageId: string, data: object): Part => ({ id: messageId, messageId, sessionId: 's', data: JSON.stringify(data) });

describe('fine-grained conversation caches', () => {
  it('does not publish a suspended or subsequently aborted transition to committed subscribers', async () => {
    const compute = createComputeTurnStats();
    const messages = [msg('u', 'user'), msg('a', 'assistant')];
    const initial = compute(messages, [], true);
    const speculative = compute([messages[0], { ...messages[1], data: { ...messages[1].data, tokens: { input: 10, output: 99 } } }], [], false);
    let release!: () => void;
    const pending = new Promise<void>((resolve) => { release = resolve; });
    let update!: (value: TurnStatsMap) => void;
    const attempts = vi.fn();
    const notifications = vi.fn();
    function Probe() {
      const stats = useTurnStats('a');
      useLayoutEffect(() => {
        notifications(stats);
      }, [stats]);
      return <span data-testid="committed-stats">{stats?.tokensOut}:{String(stats?.isLive)}</span>;
    }
    function Gate({ value }: { value: TurnStatsMap }) {
      if (value === speculative) {
        attempts();
        throw pending;
      }
      return null;
    }
    function Harness() {
      const [value, setValue] = useState(initial);
      update = setValue;
      const currentMessages = value === initial ? messages : [messages[0], { ...messages[1], data: { ...messages[1].data, tokens: { input: 10, output: 99 } } }];
      return <Suspense fallback={<span>Suspended</span>}><OcmanRuntimeProvider messages={currentMessages} parts={[]} sessionId="s" canSend={false} sessionStatus={value === initial ? 'busy' : 'done'}><Probe /><Gate value={value} /></OcmanRuntimeProvider></Suspense>;
    }
    render(<Harness />);
    notifications.mockClear();
    await act(async () => { startTransition(() => update(speculative)); });
    expect(attempts).toHaveBeenCalled();
    expect(screen.queryByText('Suspended')).not.toBeInTheDocument();
    expect(screen.getByTestId('committed-stats')).toHaveTextContent('20:true');
    expect(notifications).not.toHaveBeenCalled();

    // An urgent update abandons the speculative render before its promise resolves.
    await act(async () => { update(initial); });
    await act(async () => { release(); await pending; });
    expect(screen.getByTestId('committed-stats')).toHaveTextContent('20:true');
    expect(notifications).not.toHaveBeenCalled();
  });

  it('keeps anchors, accounting and lifecycle consistent with real runtime messages in every child layout effect', () => {
    const compute = createComputeTurnStats();
    const messages = [msg('u', 'user'), msg('a', 'assistant')];
    messages[1].data.cost = 0.01;
    const tool = part('a', { type: 'tool', tool: 'bash', state: { status: 'completed' } });
    const first = compute(messages, [tool], true);
    const nextMessage = msg('b', 'assistant');
    nextMessage.data.cost = 0.02;
    nextMessage.data.time = { created: 2000, completed: 4000 };
    const updated = [...messages, nextMessage];
    const transferred = compute(updated, [tool], true);
    const completed = compute(updated, [tool], false);
    const observations: Array<{ expected: TurnStatsMap; actual: TurnStatsMap }> = [];
    function Probe() {
      const runtimeMessages = useAuiState((state) => state.thread.messages);
      const runtimeRunning = useAuiState((state) => state.thread.isRunning);
      const a = useTurnStats('a');
      const b = useTurnStats('b');
      useLayoutEffect(() => {
        // The runtime publishes metadata and message content together. Its
        // adapter may still be on the previous snapshot during a parent commit.
        const expected = runtimeMessages.some((message) => message.id === 'b')
          ? (runtimeRunning ? transferred : completed) : first;
        observations.push({ expected, actual: new Map([
          ...(a ? [['a', a] as const] : []), ...(b ? [['b', b] as const] : []),
        ]) });
      }, [runtimeMessages, runtimeRunning, a, b]);
      return <span data-testid="anchor">{b?.isSummaryAnchor ? 'b' : a?.isSummaryAnchor ? 'a' : 'none'}:{b?.tokensOut ?? a?.tokensOut}:{String(b?.isLive ?? a?.isLive)}</span>;
    }
    const tree = (value: TurnStatsMap) => <OcmanRuntimeProvider messages={value === first ? messages : updated} parts={[tool]} sessionId="s" canSend={false} sessionStatus={value === completed ? 'done' : 'busy'}><Probe /></OcmanRuntimeProvider>;
    const { rerender } = render(tree(first));
    rerender(tree(transferred));
    expect(screen.getByTestId('anchor')).toHaveTextContent('b:40:true');
    expect(transferred.get('a')).toMatchObject({ isSummaryAnchor: false, tokensOut: 40, tokensIn: 20, toolCalls: 1, cost: 0.03, isLive: true, wallClockMs: null });
    expect(transferred.get('b')).toMatchObject({ isSummaryAnchor: true, tokensOut: 40, tokensIn: 20, toolCalls: 1, cost: 0.03, isLive: true, wallClockMs: null });
    rerender(tree(completed));
    expect(screen.getByTestId('anchor')).toHaveTextContent('b:40:false');
    expect(completed.get('b')).toMatchObject({ isSummaryAnchor: true, wallClockMs: 3000, isLive: false });
    expect(completed).toEqual(computeTurnStats(updated, [tool], false));
    expect(observations.some(({ actual }) => actual.get('b')?.isLive === false)).toBe(true);
    for (const observation of observations) expect(observation.actual).toEqual(observation.expected);
  });

  it('recomputes only the active turn across 50 updates to a 500-turn history', () => {
    const sample = vi.spyOn(throughput, 'throughputSample');
    const compute = createComputeTurnStats();
    const messages = Array.from({ length: 500 }, (_, i) => [msg(`u${i}`, 'user'), msg(`a${i}`, 'assistant')]).flat();
    const first = compute(messages, [], true);
    sample.mockClear();
    let latest = first;
    for (let i = 0; i < 50; i++) {
      messages[999] = { ...messages[999], data: { ...messages[999].data, tokens: { input: 10, output: 30 + i } } };
      latest = compute([...messages], [], true);
    }
    expect(sample).toHaveBeenCalledTimes(50);
    expect([...first].filter(([id, stats]) => latest.get(id) === stats)).toHaveLength(499);
    sample.mockRestore();
  });

  it('shares parsed parts between conversion and accounting', () => {
    const messages = [msg('u', 'user'), msg('a', 'assistant')];
    const parts = [part('a', { type: 'tool', tool: 'bash', state: { status: 'completed' } })];
    const parse = vi.spyOn(JSON, 'parse');
    createConvertMessages()(messages, parts);
    computeTurnStats(messages, parts);
    expect(parse.mock.calls.filter(([text]) => text === parts[0].data)).toHaveLength(1);
    parse.mockRestore();
  });

  it('rechecks downstream prompt-cache context and session lifecycle transitions', () => {
    const compute = createComputeTurnStats();
    const messages = [msg('u', 'user'), msg('a', 'assistant'), msg('v', 'user'), msg('b', 'assistant')];
    messages[1].data.tokens = { input: 10, output: 20, cache: { read: 100 } };
    messages[3].data.tokens = { input: 10, output: 20, cache: { write: 100 } };
    expect(compute(messages, [], true).get('b')?.promptCacheRebuilt).toBe(false);
    expect(compute(messages, [], false).get('b')?.promptCacheRebuilt).toBe(true);
    const updated = [...messages];
    updated[1] = { ...messages[1], data: { ...messages[1].data, tokens: { input: 10, output: 20 } } };
    expect(compute(updated, [], false)).toEqual(computeTurnStats(updated, [], false));
    expect(compute(updated, [], false).get('b')?.promptCacheRebuilt).toBe(false);
    expect(createComputeTurnStats()(messages, [], false).get('a')).not.toBe(compute(messages, [], false).get('a'));
  });

  it('invalidates only messages referencing a changed task, not pending agent or unrelated failures', () => {
    const convert = createConvertMessages();
    const messages = [msg('u', 'user'), msg('a', 'assistant'), msg('b', 'assistant')];
    messages[1].data.agent = 'build';
    const parts = [part('a', { type: 'tool', tool: 'task', state: { input: { task_id: 'child' }, status: 'running' } }), part('b', { type: 'text', text: 'done' })];
    const child = { messages: [], parts: [], status: 'busy' } as unknown as TaskSessionData;
    const first = convert(messages, parts, 'plan', { child });
    const same = convert(messages, parts, 'other', { child, unrelated: child }, '', {});
    expect(same).toBe(first);
    const next = convert(messages, parts, 'other', { child: { ...child, status: 'done' } }, '', {});
    expect(next[0]).toBe(first[0]);
    expect(next[1]).not.toBe(first[1]);
    expect(next[2]).toBe(first[2]);
    expect(next[1]).toEqual(createConvertMessages()(messages, parts, 'other', { child: { ...child, status: 'done' } }, '', {} )[1]);
  });

  it('preserves completed turn identities while a later turn streams, with uncached parity', () => {
    const compute = createComputeTurnStats();
    const messages = [msg('u', 'user'), msg('a', 'assistant'), msg('v', 'user'), msg('b', 'assistant')];
    const first = compute(messages, [], true);
    const updated = [...messages.slice(0, 3), { ...messages[3], data: { ...messages[3].data, tokens: { input: 10, output: 30 } } }];
    const next = compute(updated, [], true);
    expect(next.get('a')).toBe(first.get('a'));
    expect(next.get('b')).not.toBe(first.get('b'));
    expect(next).toEqual(computeTurnStats(updated, [], true));
    expect(compute(updated, [], true)).toBe(next);
  });

  it('keys converted messages on their own aggregate rather than the whole stats map', () => {
    const compute = createComputeTurnStats();
    const convert = createConvertMessages();
    const messages = [msg('u', 'user'), msg('a', 'assistant'), msg('v', 'user'), msg('b', 'assistant')];
    const firstStats = compute(messages, [], true);
    const first = convert(messages, [], undefined, undefined, undefined, undefined, true, 0, firstStats);
    const updated = [...messages.slice(0, 3), { ...messages[3], data: { ...messages[3].data, tokens: { input: 10, output: 30 } } }];
    const nextStats = compute(updated, [], true);
    const next = convert(updated, [], undefined, undefined, undefined, undefined, true, 0, nextStats);
    expect(next.slice(0, 3)).toEqual(first.slice(0, 3));
    for (let i = 0; i < 3; i++) expect(next[i]).toBe(first[i]);
    expect(next[3]).not.toBe(first[3]);
    expect(next[1].metadata?.custom?.turnStats).toBe(firstStats.get('a'));
    expect(next[3].metadata?.custom?.turnStats).toBe(nextStats.get('b'));
  });

  it('selects aggregates through real assistant-ui message scopes during anchor transfer', () => {
    const observations: Array<{ id: string; anchor: boolean; tokens: number; live: boolean }> = [];
    function Probe() {
      const id = useAuiState((state) => state.message.id);
      const stats = useTurnStats();
      useLayoutEffect(() => {
        if (stats) observations.push({ id, anchor: stats.isSummaryAnchor, tokens: stats.tokensOut, live: stats.isLive });
      }, [id, stats]);
      return <output data-testid={`scoped-${id}`}>{String(stats?.isSummaryAnchor)}:{stats?.tokensOut}:{String(stats?.isLive)}</output>;
    }
    const messages = [msg('u', 'user'), msg('a', 'assistant')];
    const children = <ThreadPrimitive.Messages components={{ UserMessage: () => null, AssistantMessage: Probe }} />;
    const tree = (current: Message[], running: boolean) => <OcmanRuntimeProvider messages={current} parts={[]} sessionId="s" canSend={false} sessionStatus={running ? 'busy' : 'done'}>{children}</OcmanRuntimeProvider>;
    const { rerender } = render(tree(messages, true));
    expect(screen.getByTestId('scoped-a')).toHaveTextContent('true:20:true');
    const updated = [...messages, msg('b', 'assistant')];
    rerender(tree(updated, true));
    expect(screen.getByTestId('scoped-a')).toHaveTextContent('false:40:true');
    expect(screen.getByTestId('scoped-b')).toHaveTextContent('true:40:true');
    rerender(tree(updated, false));
    expect(screen.getByTestId('scoped-b')).toHaveTextContent('true:40:false');
    expect(observations.filter(({ id }) => id === 'b')).toEqual([
      { id: 'b', anchor: true, tokens: 40, live: true },
      { id: 'b', anchor: true, tokens: 40, live: false },
    ]);
  });

  it('invalidates only the failed user and keeps converter contexts independent', () => {
    const convert = createConvertMessages();
    const other = createConvertMessages();
    const messages = [msg('u', 'user'), msg('a', 'assistant')];
    const failure = { id: 'u', error: 'offline', imagesDropped: true } as FailedSend;
    const first = convert(messages, [], 'plan');
    const failed = convert(messages, [], 'plan', undefined, undefined, { u: failure });
    expect(failed[0]).not.toBe(first[0]);
    expect(failed[1]).toBe(first[1]);
    expect(failed[0].metadata?.custom?.failed).toEqual({ error: 'offline', imagesDropped: true });
    other(messages, [], 'build');
    expect(convert(messages, [], 'plan', undefined, undefined, { u: { ...failure } })).toBe(failed);
    expect(convert(messages, [], 'plan')[0].metadata?.custom?.failed).toBeUndefined();
  });

  it('does not render a completed-turn subscriber when only another turn changes', () => {
    const renders = vi.fn();
    const Probe = memo(({ id }: { id: string }) => {
      renders(id);
      return <span>{id}:{useTurnStats(id)?.tokensOut}</span>;
    });
    const messages = [msg('u', 'user'), msg('a', 'assistant'), msg('v', 'user'), msg('b', 'assistant')];
    const children = <><Probe id="a" /><Probe id="b" /></>;
    const tree = (current: Message[]) => <OcmanRuntimeProvider messages={current} parts={[]} sessionId="s" canSend={false} sessionStatus="busy">{children}</OcmanRuntimeProvider>;
    const { rerender } = render(tree(messages));
    renders.mockClear();
    const updated = [...messages.slice(0, 3), { ...messages[3], data: { ...messages[3].data, tokens: { input: 10, output: 30 } } }];
    rerender(tree(updated));
    expect(screen.getByText('b:30')).toBeInTheDocument();
    expect(renders.mock.calls).toEqual([['b']]);
  });
});
