// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
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

const { message, threadState, turnStats } = vi.hoisted(() => ({
  message: {
    id: 'assistant-1',
    content: [{ type: 'text', text: 'Reply' }],
    createdAt: new Date('2026-07-16T12:00:00Z'),
    status: { type: 'complete' },
    metadata: { custom: { model: 'openai/gpt-5', time: { created: 1000, completed: 2000 }, tokens: { output: 10 } } as Record<string, unknown> },
  },
  threadState: { renderUser: false },
  turnStats: {
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
    promptCacheRebuilt: false,
  },
}));

vi.mock('@assistant-ui/react', async () => {
  const React = await import('react');
  const Root = ({ children, className, ...props }: React.HTMLAttributes<HTMLDivElement>) => <div className={className} {...props}>{children}</div>;
  const Viewport = React.forwardRef<
    HTMLDivElement,
    {
      children: ReactNode;
      className?: string;
      autoScroll?: boolean;
      scrollToBottomOnRunStart?: boolean;
    }
  >(
    ({ children, className, autoScroll, scrollToBottomOnRunStart }, ref) => (
      <div
        ref={ref}
        className={className}
        data-auto-scroll={String(autoScroll)}
        data-scroll-on-run-start={String(scrollToBottomOnRunStart)}
      >
        {children}
      </div>
    ),
  );
  return {
    ThreadPrimitive: {
      Root,
      Viewport,
      Empty: ({ children }: { children: ReactNode }) => <>{children}</>,
      Messages: ({ components }: { components: { UserMessage: React.ComponentType; AssistantMessage: React.ComponentType } }) => (
        threadState.renderUser ? <components.UserMessage /> : <components.AssistantMessage />
      ),
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
  useTurnStats: () => turnStats,
}));

import { AssistantThread, ImageDisplay } from './AssistantThread';
import { useUiStore } from '../lib/uiStore';

class StubResizeObserver {
  observe() {}
  disconnect() {}
}

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', StubResizeObserver);
  useUiStore.getState().setShowMessageMetadata(false);
  threadState.renderUser = false;
  turnStats.promptCacheRebuilt = false;
  message.metadata.custom = { model: 'openai/gpt-5', time: { created: 1000, completed: 2000 }, tokens: { output: 10 } };
});
afterEach(() => vi.unstubAllGlobals());

describe('AssistantThread attached images', () => {
  it('expands an attached image from the keyboard', async () => {
    const user = userEvent.setup();
    render(<ImageDisplay image="data:image/png;base64,AA" filename="shot.png" />);

    // Same pattern as MarkdownText's MarkdownImage: the <img> lives inside
    // a real button, so it is focusable and announced as a toggle.
    const toggle = screen.getByRole('button', { name: 'Expand shot.png' });
    await user.tab();
    expect(toggle).toHaveFocus();
    expect(toggle).toHaveAttribute('aria-expanded', 'false');

    await user.keyboard('{Enter}');
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByRole('button', { name: 'Collapse shot.png' })).toBe(toggle);
    expect(screen.getByAltText('shot.png').className).toContain('oc-image-expanded');
  });

  it('falls back to a generic label when the attachment has no filename', () => {
    render(<ImageDisplay image="data:image/png;base64,AA" />);
    expect(screen.getByRole('button', { name: 'Expand Image' })).toBeInTheDocument();
  });
});

describe('AssistantThread pagination', () => {
  it('does not load older messages when a thread first mounts at its tail', () => {
    const onLoadMore = vi.fn();

    render(<AssistantThread hasMore onLoadMore={onLoadMore} />);

    expect(onLoadMore).not.toHaveBeenCalled();
  });
});

describe('AssistantThread message jumps', () => {
  it('disables assistant-ui auto-scroll so it cannot override a jump', () => {
    const { container } = render(<AssistantThread />);

    expect(container.querySelector('.oc-thread-viewport')).toHaveAttribute('data-auto-scroll', 'false');
  });

  it('disables the library scroll-to-bottom-on-run-start latch', () => {
    // `autoScroll={false}` does not cover this one: the library checks it
    // independently, and it latches a re-scroll that its content-resize
    // handler re-applies on every later resize. The latch is only cleared
    // by a scroll landing at the bottom, so a scroll-up never clears it —
    // one run start and every streaming chunk drags the reader down.
    const { container } = render(<AssistantThread />);

    expect(container.querySelector('.oc-thread-viewport')).toHaveAttribute(
      'data-scroll-on-run-start',
      'false',
    );
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

  it('shows a prompt cache rebuild warning once in the turn summary', () => {
    turnStats.promptCacheRebuilt = true;

    render(<AssistantThread />);

    expect(screen.getAllByText('Prompt cache rebuilt')).toHaveLength(1);
  });
});

describe('AssistantThread agent updates', () => {
  it('labels child-to-parent messages separately from user messages', () => {
    threadState.renderUser = true;
    message.metadata.custom = {
      childMessage: {
        kind: 'direct_message',
        childSessionId: 'child-1',
        intent: 'Inspect the failing test',
        status: 'running',
      },
    };

    const { getByText, getByTestId } = render(<AssistantThread />);

    expect(getByText('Agent update')).toBeInTheDocument();
    expect(getByText('Inspect the failing test')).toBeInTheDocument();
    expect(getByTestId('agent-update-message')).toBeInTheDocument();
  });

  it('labels parent-to-child messages separately from user messages', () => {
    threadState.renderUser = true;
    message.metadata.custom = {
      parentMessage: { parentSessionId: 'parent-1' },
    };

    const { getByText, getByTestId } = render(<AssistantThread />);

    expect(getByText('Parent update')).toBeInTheDocument();
    expect(getByTestId('agent-update-message')).toHaveAttribute('class', expect.stringContaining('oc-msg-agent-update'));
  });
});
