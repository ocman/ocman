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
    content: [{ type: 'text', text: 'Reply' }] as Array<{ type: string; text?: string; [key: string]: unknown }>,
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
      Content: ({ components }: { components: { Text: React.ComponentType<{ text: string }>; tools?: { Fallback: React.ComponentType<Record<string, unknown>> } } }) => (
        <>{message.content.map((part, index) => part.type === 'tool-call' && components.tools
          ? <components.tools.Fallback key={index} {...part} />
          : <components.Text key={index} text={part.text ?? ''} />)}</>
      ),
    },
    useMessage: (selector: (value: typeof message) => unknown) => selector(message),
  };
});

vi.mock('../lib/turnStats', () => ({
  useModelLabel: (model: string) => model,
  useTurnStats: () => turnStats,
}));

import { AssistantThread, ImageDisplay } from './AssistantThread';
import { formatTimelineMarker } from '../lib/conversationTimeline';
import { useUiStore } from '../lib/uiStore';
import { TurnSpeechContext } from '../lib/turnSpeech';

class StubResizeObserver {
  observe() {}
  disconnect() {}
}

beforeEach(() => {
  Element.prototype.scrollTo = vi.fn();
  vi.stubGlobal('ResizeObserver', StubResizeObserver);
  useUiStore.getState().setShowMessageMetadata(false);
  useUiStore.setState({ showToolDetails: true });
  threadState.renderUser = false;
  message.content = [{ type: 'text', text: 'Reply' }];
  turnStats.promptCacheRebuilt = false;
  turnStats.isLive = false;
  turnStats.isSummaryAnchor = true;
  message.metadata.custom = { model: 'openai/gpt-5', time: { created: 1000, completed: 2000 }, tokens: { output: 10 } };
});
afterEach(() => vi.unstubAllGlobals());

describe('turn actions', () => {
  it('groups speaker and bookmark at the turn end and switches the speaker to stop', async () => {
    const toggle = vi.fn();
    const bookmark = vi.fn();
    const speech = { answers: new Map([['assistant-1', 'Reply']]), speakingId: null as string | null, error: null, toggle };
    const view = (value = speech) => <TurnSpeechContext.Provider value={value}>
      <AssistantThread onToggleMessageBookmark={bookmark} />
    </TurnSpeechContext.Provider>;
    const { rerender } = render(view());
    const group = screen.getByRole('group', { name: 'Turn actions' });
    expect(group).toContainElement(screen.getByRole('button', { name: 'Read aloud' }));
    expect(group).toContainElement(screen.getByRole('button', { name: 'Bookmark message' }));
    await userEvent.click(screen.getByRole('button', { name: 'Read aloud' }));
    expect(toggle).toHaveBeenCalledWith('assistant-1');
    await userEvent.click(screen.getByRole('button', { name: 'Bookmark message' }));
    expect(bookmark).toHaveBeenCalledWith('assistant-1');
    rerender(view({ ...speech, speakingId: 'assistant-1' }));
    expect(screen.getByRole('button', { name: 'Stop reading' })).toBeInTheDocument();
    turnStats.isSummaryAnchor = false;
    rerender(view());
    expect(screen.queryByRole('group', { name: 'Turn actions' })).not.toBeInTheDocument();
  });
  it('keeps live turns free of actions and shows playback errors accessibly', () => {
    turnStats.isLive = true;
    render(<TurnSpeechContext.Provider value={{ answers: new Map(), speakingId: null, error: 'Playback blocked', toggle: vi.fn() }}>
      <AssistantThread onToggleMessageBookmark={vi.fn()} />
    </TurnSpeechContext.Provider>);
    expect(screen.queryByRole('group', { name: 'Turn actions' })).not.toBeInTheDocument();
    expect(screen.getByRole('status')).toHaveTextContent('Playback blocked');
  });
});

describe('AssistantThread slash-command skills', () => {
  const instructions = '# Review Pull Request\n\nReview the changes carefully.\n\nBase directory for this skill: /home/user/.config/opencode/skills/review-pr\nRelative paths in this skill (e.g., scripts/, references/) are relative to this base directory.';

  it('collapses skill instructions and lets the user expand them', async () => {
    const user = userEvent.setup();
    threadState.renderUser = true;
    message.content = [{ type: 'text', text: instructions + '\n\nReview PR 42' }];
    render(<AssistantThread />);

    const summary = screen.getByText('Skill called: /review-pr');
    const details = summary.closest('details');
    expect(details).not.toHaveAttribute('open');
    expect(details?.textContent).toContain(instructions);
    expect(screen.getByText('Review PR 42').closest('details')).toBeNull();
    await user.click(summary);
    expect(details).toHaveAttribute('open');
    await user.click(summary);
    expect(details).not.toHaveAttribute('open');
  });

  it('keeps ordinary user text and partial footer mentions as plain text', () => {
    threadState.renderUser = true;
    const text = 'Explain this line:\nBase directory for this skill: /home/user/skills/review-pr';
    message.content = [{ type: 'text', text }];
    const { container } = render(<AssistantThread />);
    expect(container.querySelector('details')).toBeNull();
    expect(container.textContent).toContain(text);
  });

  it('collapses a skill without arguments and with CRLF line endings', () => {
    threadState.renderUser = true;
    message.content = [{ type: 'text', text: instructions.replaceAll('\n', '\r\n') }];
    render(<AssistantThread />);
    expect(screen.getByText('Skill called: /review-pr').closest('details')).not.toHaveAttribute('open');
  });

  it('does not collapse skill text quoted by the assistant', () => {
    message.content = [{ type: 'text', text: instructions }];
    render(<AssistantThread />);
    expect(screen.queryByText('Skill called: /review-pr')).toBeNull();
    expect(screen.getByRole('heading', { name: 'Review Pull Request' })).toBeInTheDocument();
  });
});

describe('conversation timeline markers', () => {
  it('formats markers relative to the current day', () => {
    const now = new Date(2026, 8, 14, 9);

    expect(formatTimelineMarker(new Date(2026, 8, 14, 22, 23).getTime(), now)).toBe('Today 22:23');
    expect(formatTimelineMarker(new Date(2026, 8, 13, 22, 23).getTime(), now)).toBe('Yesterday 22:23');
    expect(formatTimelineMarker(new Date(2026, 8, 11, 22, 23).getTime(), now)).toBe('Friday September 11th, 22:23');
  });

  it('renders the marker attached to a conversation message', () => {
    threadState.renderUser = true;
    message.metadata.custom.timelineAt = new Date().setSeconds(0, 0);

    render(<AssistantThread />);

    expect(screen.getByText(/^Today \d{2}:\d{2}$/).closest('time')).toHaveAttribute('datetime');
  });
});

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

  it('reveals and highlights only the requested tool call', async () => {
    message.content = [
      { type: 'tool-call', toolCallId: 'call-1', toolName: 'bash', argsText: 'completed\necho first', result: 'first' },
      { type: 'tool-call', toolCallId: 'call-2', toolName: 'bash', argsText: 'completed\ngit commit', result: Array.from({ length: 13 }, (_, i) => `line ${i}`).join('\n') },
    ];
    const { container } = render(<AssistantThread scrollToToolCall={{ messageId: 'assistant-1', toolCallId: 'call-2', tick: 1 }} />);

    expect(screen.getByRole('button', { name: 'Collapse output' })).toHaveAttribute('aria-expanded', 'true');
    await vi.waitFor(() => expect(container.querySelector('[data-tool-call-id="call-2"]')?.firstElementChild).toHaveClass('oc-msg-scroll-highlight'));
    expect(container.querySelector('[data-tool-call-id="call-1"]')?.firstElementChild).not.toHaveClass('oc-msg-scroll-highlight');
    expect(container.querySelector('[data-tool-call-id="call-2"]')?.firstElementChild).toHaveFocus();
    expect(Element.prototype.scrollTo).toHaveBeenCalled();
  });

  it('reveals the requested call while tool details are hidden', async () => {
    useUiStore.setState({ showToolDetails: false });
    message.content = [
      { type: 'tool-call', toolCallId: 'call-1', toolName: 'bash', argsText: 'completed\ngit commit', result: 'done' },
    ];
    const { container } = render(<AssistantThread scrollToToolCall={{ messageId: 'assistant-1', toolCallId: 'call-1', tick: 1 }} />);

    await vi.waitFor(() => expect(container.querySelector('[data-tool-call-id="call-1"]')).toHaveClass('oc-tool-source-revealed'));
  });

  it('only keeps the latest requested call revealed', async () => {
    useUiStore.setState({ showToolDetails: false });
    message.content = [
      { type: 'tool-call', toolCallId: 'call-1', toolName: 'bash', argsText: 'completed\nfirst', result: 'done' },
      { type: 'tool-call', toolCallId: 'call-2', toolName: 'bash', argsText: 'completed\nsecond', result: 'done' },
    ];
    const { container, rerender } = render(<AssistantThread scrollToToolCall={{ messageId: 'assistant-1', toolCallId: 'call-1', tick: 1 }} />);
    await vi.waitFor(() => expect(container.querySelector('[data-tool-call-id="call-1"]')).toHaveClass('oc-tool-source-revealed'));

    rerender(<AssistantThread scrollToToolCall={{ messageId: 'assistant-1', toolCallId: 'call-2', tick: 2 }} />);

    await vi.waitFor(() => expect(container.querySelector('[data-tool-call-id="call-2"]')).toHaveClass('oc-tool-source-revealed'));
    expect(container.querySelector('[data-tool-call-id="call-1"]')).not.toHaveClass('oc-tool-source-revealed');
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
