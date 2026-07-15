// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render } from '@testing-library/react';
import type { ReactNode } from 'react';

vi.hoisted(() => {
  const mem = new Map<string, string>();
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: {
      getItem: (key: string) => mem.get(key) ?? null,
      setItem: (key: string, value: string) => void mem.set(key, value),
      removeItem: (key: string) => void mem.delete(key),
    },
  });
});

const { message } = vi.hoisted(() => ({
  message: {
    id: 'assistant-1',
    content: [{ type: 'text', text: 'Reply' }],
    createdAt: new Date('2026-07-16T12:00:00Z'),
    status: { type: 'complete' },
    metadata: { custom: { model: 'openai/gpt-5', time: { created: 1000, completed: 2000 }, tokens: { output: 10 } } },
  },
}));

vi.mock('@assistant-ui/react', async () => {
  const React = await import('react');
  const Root = ({ children, className }: { children: ReactNode; className?: string }) => <div className={className}>{children}</div>;
  const Viewport = React.forwardRef<HTMLDivElement, { children: ReactNode; className?: string }>(
    ({ children, className }, ref) => <div ref={ref} className={className}>{children}</div>,
  );
  return {
    ThreadPrimitive: {
      Root,
      Viewport,
      Empty: ({ children }: { children: ReactNode }) => <>{children}</>,
      Messages: ({ components }: { components: { AssistantMessage: React.ComponentType } }) => <components.AssistantMessage />,
      ViewportFooter: React.forwardRef<HTMLDivElement, { children: ReactNode; className?: string }>(
        ({ children, className }, ref) => <div ref={ref} className={className}>{children}</div>,
      ),
      ScrollToBottom: ({ children }: { children: ReactNode }) => <button type="button">{children}</button>,
    },
    MessagePrimitive: {
      Root,
      Content: () => <div>Reply</div>,
    },
    useMessage: (selector: (value: typeof message) => unknown) => selector(message),
  };
});

vi.mock('../lib/turnStats', () => ({
  useModelLabel: (model: string) => model,
  useTurnStats: () => ({
    wallClockMs: 1000,
    tokensOut: 10,
    tokensIn: 5,
    cost: 0,
    toolCalls: 0,
    tps: 10,
    isLive: false,
    startedAt: Date.parse('2026-07-16T12:00:00Z'),
    model: 'openai/gpt-5',
    isSummaryAnchor: true,
  }),
}));

import { AssistantThread } from './AssistantThread';
import { useUiStore } from '../lib/uiStore';

class StubResizeObserver {
  observe() {}
  disconnect() {}
}

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', StubResizeObserver);
  useUiStore.getState().setShowMessageMetadata(false);
});
afterEach(() => vi.unstubAllGlobals());

describe('AssistantThread pagination', () => {
  it('does not load older messages when a thread first mounts at its tail', () => {
    const onLoadMore = vi.fn();

    render(<AssistantThread hasMore onLoadMore={onLoadMore} />);

    expect(onLoadMore).not.toHaveBeenCalled();
  });
});

describe('AssistantThread message metadata', () => {
  it('hides per-message metadata by default but keeps the between-turn summary', () => {
    const { container } = render(<AssistantThread />);

    expect(container.querySelector('.oc-msg-meta')).toBeNull();
    expect(container.querySelector('.oc-turn-stats')).not.toBeNull();
  });

  it('shows per-message metadata when enabled', () => {
    useUiStore.getState().setShowMessageMetadata(true);

    const { container } = render(<AssistantThread />);

    expect(container.querySelector('.oc-msg-meta')).not.toBeNull();
  });
});
