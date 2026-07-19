// @vitest-environment jsdom

import { act, render } from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { Message, Part } from '../lib/api';

let runtimeMessages: Array<{ content: unknown }> = [];

vi.mock('@assistant-ui/react', () => ({
  AssistantRuntimeProvider: ({ children }: { children: ReactNode }) => children,
  useExternalStoreRuntime: (store: { messages: Array<{ content: unknown }> }) => {
    runtimeMessages = store.messages;
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
