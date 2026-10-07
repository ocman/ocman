// @vitest-environment jsdom

import { act, render } from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { Message, Part, SessionStatus } from '../lib/api';
import { useTurnStats } from '../lib/turnStats';

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
  useApiStore: (selector: (state: { sendMessage: ReturnType<typeof vi.fn> }) => unknown) => selector({ sendMessage: vi.fn() }),
}));

vi.mock('../lib/uiStore', () => ({
  useUiStore: (selector: (state: { showReasoning: boolean }) => unknown) => selector({ showReasoning: true }),
}));

import { OcmanRuntimeProvider } from './OcmanRuntimeProvider';

afterEach(() => {
  vi.useRealTimers();
});

describe('OcmanRuntimeProvider turn lifecycle', () => {
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
