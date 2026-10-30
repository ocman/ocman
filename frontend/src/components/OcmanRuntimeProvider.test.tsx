// @vitest-environment jsdom

import { act, render } from '@testing-library/react';
import { useContext, type ReactNode } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { Message, Part, SessionStatus } from '../lib/api';
import { useTurnStats } from '../lib/turnStats';
import { A2UIActionContext } from '../lib/a2uiActionContext';
import type { A2UIAction } from '../lib/a2ui';

const { sendMessage } = vi.hoisted(() => ({ sendMessage: vi.fn().mockResolvedValue(undefined) }));

let runtimeMessages: Array<{ content: unknown }> = [];
let runtimeRunning = false;

vi.mock('@assistant-ui/react', () => ({
  AssistantRuntimeProvider: ({ children }: { children: ReactNode }) => children,
  useExternalStoreRuntime: (store: { messages: Array<{ content: unknown }>; isRunning: boolean }) => {
    runtimeMessages = store.messages;
    runtimeRunning = store.isRunning;
    return {};
  },
}));

vi.mock('../lib/apiStore', () => ({
  useApiStore: (selector: (state: { sendMessage: ReturnType<typeof vi.fn> }) => unknown) => selector({ sendMessage }),
}));

vi.mock('../lib/uiStore', () => ({
  useUiStore: (selector: (state: { showReasoning: boolean }) => unknown) => selector({ showReasoning: true }),
}));

import { OcmanRuntimeProvider } from './OcmanRuntimeProvider';

afterEach(() => {
  vi.useRealTimers();
  sendMessage.mockClear();
});

describe('A2UI action delivery', () => {
  it('uses the owning compound platform and session, and disables delivery when disconnected', async () => {
    let context!: React.ContextType<typeof A2UIActionContext>;
    function Probe() { context = useContext(A2UIActionContext); return null; }
    const view = (canSend: boolean, sessionId = 'first') => <OcmanRuntimeProvider messages={[]} parts={[]} sessionId={sessionId} platformId="r-machine:agent" canSend={canSend}><Probe /></OcmanRuntimeProvider>;
    const action: A2UIAction = { action: { name: 'inspect', surfaceId: 'result', sourceComponentId: 'button', timestamp: '2026-10-30T00:00:00Z', context: { job: 'Backend' } } };
    const { rerender } = render(view(true));
    expect(context.scope).toBe('r-machine:agent:first');
    await act(async () => { await context.send!(action); });
    expect(sendMessage).toHaveBeenCalledExactlyOnceWith('first', `A2UI user response:\n${JSON.stringify(action)}`, undefined, undefined, undefined, undefined, 'r-machine:agent');
    rerender(view(false));
    expect(context.send).toBeUndefined();
    rerender(view(true, 'second'));
    expect(context.scope).toBe('r-machine:agent:second');
    await act(async () => { await context.send!(action); });
    expect(sendMessage).toHaveBeenLastCalledWith('second', expect.any(String), undefined, undefined, undefined, undefined, 'r-machine:agent');
  });

  it('does not infer an owner when the platform is missing', () => {
    function Probe() { expect(useContext(A2UIActionContext).send).toBeUndefined(); return null; }
    render(<OcmanRuntimeProvider messages={[]} parts={[]} sessionId="s" canSend><Probe /></OcmanRuntimeProvider>);
  });
});

describe('OcmanRuntimeProvider turn lifecycle', () => {
  it('renders pending text and images, without duplicating its failed-send entry', () => {
    const pending = { id: 'pending', text: 'hello', images: [{ url: 'data:image/png;base64,a', mime: 'image/png' }], agent: 'plan', startedAt: 1000 };
    const view = (error?: string) => (
      <OcmanRuntimeProvider messages={[]} parts={[]} sessionId="s" canSend pendingSend={{ ...pending, error }}
        failedSends={error ? [{ ...pending, failedAt: 1000, error }] : []}>
        <div />
      </OcmanRuntimeProvider>
    );
    const { rerender } = render(view());
    expect(runtimeMessages).toEqual([expect.objectContaining({
      id: 'pending', role: 'user',
      content: [{ type: 'text', text: 'hello' }, { type: 'image', image: 'data:image/png;base64,a' }],
      metadata: { custom: { agent: 'plan' } },
    })]);
    rerender(view('offline'));
    expect(runtimeMessages).toHaveLength(1);
    expect(runtimeMessages[0]).toMatchObject({ metadata: { custom: { failed: { error: 'offline' } } } });
  });

  it.each<SessionStatus>(['done', 'error', 'interrupted'])('stops the conversation and timer when the session becomes %s without a finish event', (status) => {
    const messages: Message[] = [
      { id: 'u', sessionId: 's', timeCreated: 1000, data: { role: 'user' } },
      { id: 'a', sessionId: 's', timeCreated: 5000, data: { role: 'assistant' } },
    ];
    function Probe() {
      const stats = useTurnStats('a');
      return <output data-testid="turn-stats">{JSON.stringify({ live: stats?.isLive, duration: stats?.wallClockMs })}</output>;
    }
    const view = (sessionStatus: SessionStatus) => (
      <OcmanRuntimeProvider messages={messages} parts={[]} sessionId="s" sessionStatus={sessionStatus} canSend={false}>
        <Probe />
      </OcmanRuntimeProvider>
    );
    const { rerender, getByTestId } = render(view('busy'));
    expect(runtimeRunning).toBe(true);
    expect(getByTestId('turn-stats').textContent).toBe('{"live":true,"duration":null}');

    rerender(view(status));
    expect(runtimeRunning).toBe(false);
    expect(getByTestId('turn-stats').textContent).toBe('{"live":false,"duration":4000}');
  });

  it('keeps the conversation running between tool steps while the session is busy', () => {
    const messages: Message[] = [
      { id: 'u', sessionId: 's', timeCreated: 1000, data: { role: 'user' } },
      { id: 'a', sessionId: 's', timeCreated: 5000, data: { role: 'assistant', finish: 'tool-calls' } },
    ];
    function Probe() {
      expect(useTurnStats('a')?.isLive).toBe(true);
      return null;
    }
    render(<OcmanRuntimeProvider messages={messages} parts={[]} sessionId="s" sessionStatus="busy" canSend={false}><Probe /></OcmanRuntimeProvider>);
    expect(runtimeRunning).toBe(true);
  });
});

describe('OcmanRuntimeProvider reasoning timer', () => {
  it('updates active reasoning elapsed time without SSE events', () => {
    vi.useFakeTimers();
    vi.setSystemTime(8800);
    const messages: Message[] = [{
      id: 'm1',
      sessionId: 's1',
      timeCreated: 1000,
      data: { role: 'assistant' },
    }];
    const parts: Part[] = [{
      id: 'p1',
      messageId: 'm1',
      sessionId: 's1',
      data: { type: 'reasoning', text: 'working', time: { start: 1000 } },
    }];

    render(
      <OcmanRuntimeProvider messages={messages} parts={parts} sessionId="s1" canSend={false}>
        <div />
      </OcmanRuntimeProvider>,
    );
    expect(runtimeMessages[0].content).toBe('> **Thinking:** working · 7.8s');

    act(() => vi.advanceTimersByTime(1000));
    expect(runtimeMessages[0].content).toBe('> **Thinking:** working · 8.8s');
  });
});
