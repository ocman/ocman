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
    useAuiState: (selector: (value: { message: typeof message; thread: { messages: typeof message[] } }) => unknown) => selector({ message, thread: { messages: [message] } }),
  };
});

vi.mock('../lib/turnStats', () => ({
  useModelLabel: (model: string) => model,
  useTurnStats: () => turnStats,
}));

import { AssistantThread, ImageDisplay } from './AssistantThread';
import { formatTimelineMarker } from '../lib/conversationTimeline';
import { formatFullDateTime } from '../lib/format';
import { useUiStore } from '../lib/uiStore';
import { TurnSpeechContext } from '../lib/turnSpeech';

it.each([
  ['shell', '<shell id="sh_1" state="completed" command="echo "hello"">\nhello\n</shell>', 'hello'],
  ['user shell', 'The following shell command was executed by the user:\n<shell id="sh_1" state="completed" command="pwd">\n/workspace\n</shell>', '/workspace'],
  ['subagent', '<subagent sessionID="ses_child" state="completed" description="Review "changes"">\n**All clear**\n</subagent>', 'All clear'],
])('renders v2 %s notifications without raw tags', (name, text, result) => {
  threadState.renderUser = true;
  message.content = [{ type: 'text', text }];
  const { container } = render(<AssistantThread />);
  expect(container.textContent).toContain(result);
  expect(container.textContent).not.toContain('<shell');
  expect(container.textContent).not.toContain('<subagent');
  expect(container.querySelector('.oc-msg-system')).toBeInTheDocument();
  expect(container.querySelector('.oc-msg-system')?.classList.contains('oc-msg-tools')).toBe(name !== 'subagent');
});

class StubResizeObserver {
  observe() {}
  disconnect() {}
}

beforeEach(() => {
  Element.prototype.scrollTo = vi.fn();
  vi.stubGlobal('ResizeObserver', StubResizeObserver);
  useUiStore.getState().setShowMessageMetadata(false);
  useUiStore.setState({ showToolDetails: true, compactToolCalls: false });
  threadState.renderUser = false;
  message.content = [{ type: 'text', text: 'Reply' }];
  turnStats.promptCacheRebuilt = false;
  turnStats.isLive = false;
  turnStats.isSummaryAnchor = true;
  message.metadata.custom = { model: 'openai/gpt-5', time: { created: 1000, completed: 2000 }, tokens: { output: 10 } };
});
afterEach(() => vi.unstubAllGlobals());

describe('tool-only message spacing', () => {
  it.each(['__skill__', 'bash', 'some_mcp_tool'])('marks %s messages for compact spacing', (toolName) => {
    message.content = [{ type: 'tool-call', toolName, toolCallId: 'call-1', argsText: 'completed\necho hello', result: 'Done' }];
    const { container } = render(<AssistantThread />);
    expect(container.querySelector('[data-message-id="assistant-1"]')).toHaveClass('oc-msg-tools');
  });

  it('keeps normal spacing for prose mixed with tools', () => {
    message.content.push({ type: 'tool-call', toolName: '__skill__', toolCallId: 'call-1', argsText: 'Skill "example"' });
    const { container } = render(<AssistantThread />);
    expect(container.querySelector('[data-message-id="assistant-1"]')).not.toHaveClass('oc-msg-tools');
  });
});

describe('turn actions', () => {
  it.each([false, true])('keeps metadata visible on an empty summary anchor with live=%s', (isLive) => {
    message.content = [];
    turnStats.isLive = isLive;
    const { container } = render(<AssistantThread />);
    expect(container.querySelector('.oc-turn-stats')).toBeInTheDocument();
    expect(screen.getByText('15 tok')).toBeInTheDocument();
    expect(container.querySelector('[data-message-id="assistant-1"]')).toBeInTheDocument();
  });

  it('shows the live turn line under a prompt that has no reply yet', () => {
    threadState.renderUser = true;
    message.content = [{ type: 'text', text: 'Do it' }];
    turnStats.isLive = true;
    const { container, rerender } = render(<AssistantThread />);
    expect(container.querySelector('.oc-turn-stats')).toBeInTheDocument();
    expect(screen.getByTitle('Turn in progress')).toBeInTheDocument();
    turnStats.isLive = false;
    rerender(<AssistantThread />);
    expect(container.querySelector('.oc-turn-stats')).not.toBeInTheDocument();
  });

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

describe('Claude local commands', () => {
  it.each([
    ['<command-name>/model</command-name>\n <command-message>model</command-message>\n <command-args>opus</command-args>', '/model opus', 'Local command'],
    ['<command-name>/model</command-name><command-message>model</command-message><command-args></command-args>', '/model', 'Local command'],
    ['<command-message>review-pr</command-message>\n<command-name>/review-pr</command-name>', '/review-pr', 'Local command'],
    ['<command-message>review-pr</command-message>\n<command-name>/review-pr</command-name>\n<command-args>1062</command-args>', '/review-pr 1062', 'Local command'],
    ['<local-command-stdout>Set model to `Opus 5.5`</local-command-stdout>', 'Set model to Opus 5.5', 'Command output'],
    ['<local-command-stderr>Unknown model</local-command-stderr>', 'Unknown model', 'Command error'],
  ])('renders %s without transcript markup', (text, expected, label) => {
    threadState.renderUser = true;
    message.content = [{ type: 'text', text }];
    render(<AssistantThread />);
    const entry = screen.getByLabelText(label);
    expect(entry).toHaveTextContent(expected);
    expect(entry.textContent).not.toContain('<');
    expect(entry.closest('[data-message-id]')).toHaveClass('oc-msg-local-command');
  });

  it.each([
    'Explain <command-name>/model</command-name>',
    '<local-command-stdout>unfinished',
    '<local-command-stdout>output</local-command-stdout> extra text',
    '<command-name>/model</command-name><command-message>model</command-message><command-args></command-args> trailing',
    '<command-name>/model</command-name>',
    '<command-message>model</command-message><command-name>/a</command-name><command-name>/b</command-name>',
  ])('preserves ordinary text and incomplete wrappers: %s', (text) => {
    threadState.renderUser = true;
    message.content = [{ type: 'text', text }];
    const { container } = render(<AssistantThread />);
    expect(screen.getByText(text)).toBeInTheDocument();
    expect(container.querySelector('[data-message-id]')).not.toHaveClass('oc-msg-local-command');
  });
});

describe('Claude task notifications', () => {
  const notification = `<task-notification>
<task-id>a8c55d66aa2b920c7</task-id>
<tool-use-id>toolu_01NAYXoxPMCm4xD6ACK2qS2U</tool-use-id>
<output-file>/private/tmp/claude/tasks/a8c55d66aa2b920c7.output</output-file>
<status>completed</status>
<summary>Agent "Review backend composition &amp; boundaries" finished</summary>
<note>The agent can be resumed.</note>
<result>## Findings

**Eight findings**, ranked by impact. Raw A & B stay intact.</result>
<usage><subagent_tokens>86431</subagent_tokens><tool_uses>24</tool_uses><duration_ms>170733</duration_ms></usage>
</task-notification>`;

  it.each([1, 2])('renders %i notification parts as system entries without a user bubble', (count) => {
    threadState.renderUser = true;
    message.metadata.custom = { agent: 'build' };
    message.content = Array.from({ length: count }, () => ({ type: 'text', text: notification }));
    render(<AssistantThread onToggleMessageBookmark={vi.fn()} />);
    const tasks = screen.getAllByLabelText('Task notification');
    expect(tasks).toHaveLength(count);
    for (const task of tasks) {
      const root = task.closest('[data-message-id]');
      expect(root).not.toHaveClass('oc-msg-user');
      expect(root).toHaveClass('oc-msg-system');
      expect(root).not.toHaveClass('oc-msg-tools');
      expect(root).not.toHaveAttribute('style');
    }
    expect(screen.queryByRole('button', { name: 'Bookmark message' })).toBeNull();
  });

  it('keeps user content alongside a notification in a user message', () => {
    threadState.renderUser = true;
    message.content = [{ type: 'text', text: 'Please inspect this task' }, { type: 'text', text: notification }];
    render(<AssistantThread />);
    expect(screen.getByText('Please inspect this task').closest('[data-message-id]')).toHaveClass('oc-msg-user');
  });

  it('renders a task summary and Markdown result instead of raw XML', async () => {
    threadState.renderUser = true;
    message.content = [{ type: 'text', text: notification }];
    render(<AssistantThread />);
    const task = screen.getByLabelText('Task notification');
    expect(task).toHaveTextContent('Agent "Review backend composition & boundaries" finished');
    expect(task).toHaveTextContent('Completed');
    expect(screen.getByRole('heading', { name: 'Findings' })).toBeInTheDocument();
    expect(screen.getByText('Eight findings').tagName).toBe('STRONG');
    expect(task).toHaveTextContent('Raw A & B stay intact.');
    expect(task.textContent).not.toContain('<task-notification>');
    const details = screen.getByText('Task details').closest('details');
    expect(details).not.toHaveAttribute('open');
    await userEvent.click(screen.getByText('Task details'));
    expect(details).toHaveAttribute('open');
    expect(details).toHaveTextContent('a8c55d66aa2b920c7');
    expect(details).toHaveTextContent('toolu_01NAYXoxPMCm4xD6ACK2qS2U');
    expect(details).toHaveTextContent('/private/tmp/claude/tasks/a8c55d66aa2b920c7.output');
    expect(details).toHaveTextContent('The agent can be resumed.');
    expect(details).toHaveTextContent('86431');
    expect(details).toHaveTextContent('24');
    expect(details).toHaveTextContent('170733');
    expect(screen.queryByRole('link', { name: 'Open detailed subagent session' })).toBeNull();
  });

  it.each([
    `Explain this: ${notification}`,
    notification.replace('</task-notification>', ''),
    notification + '\nMore text',
    notification + notification,
    notification.replace('</result>', ''),
    '<task-notification><result>Missing summary and status</result></task-notification>',
    '<task-notification><summary>Missing status and event</summary></task-notification>',
  ])('preserves ordinary text and incomplete notifications', (text) => {
    threadState.renderUser = true;
    message.content = [{ type: 'text', text }];
    const { container } = render(<AssistantThread />);
    expect(container.textContent).toContain(text);
    expect(screen.queryByLabelText('Task notification')).toBeNull();
  });

  it.each(['failed', 'error', 'running', 'stopped'])('renders %s notifications without optional fields', (status) => {
    threadState.renderUser = true;
    message.content = [{ type: 'text', text: `<task-notification><status>${status}</status><summary>Background task</summary></task-notification>` }];
    render(<AssistantThread />);
    const task = screen.getByLabelText('Task notification');
    expect(task).toHaveTextContent('Background task');
    expect(task).toHaveTextContent(status.charAt(0).toUpperCase() + status.slice(1));
    if (status === 'failed' || status === 'error') expect(task).toHaveClass('oc-tool-error');
    else if (status === 'running') expect(task).toHaveClass('oc-tool-running');
    else expect(task).not.toHaveClass('oc-tool-running');
    expect(screen.queryByText('Task details')).toBeNull();
  });

  it('renders Monitor events, which carry an event instead of a status', () => {
    threadState.renderUser = true;
    message.content = [{ type: 'text', text: `<task-notification>
<task-id>baqp6boor</task-id>
<summary>Monitor event: "PR #1062 CI checks"</summary>
<event>CI / Backend (pull_request): success</event>
</task-notification>` }];
    render(<AssistantThread />);
    const task = screen.getByLabelText('Task notification');
    expect(task.closest('[data-message-id]')).toHaveClass('oc-msg-system');
    expect(task).toHaveTextContent('Monitor event: "PR #1062 CI checks"');
    expect(task).toHaveTextContent('CI / Backend (pull_request): success');
    expect(task.querySelector('.oc-task-status')).toBeNull();
    expect(task.textContent).not.toContain('<event>');
  });

  it('decodes XML entities once and treats metadata as text', () => {
    threadState.renderUser = true;
    message.content = [{ type: 'text', text: `<task-notification><summary>&lt;script&gt; &quot;task&quot; &apos;done&apos; &amp;amp;</summary><status>completed</status><result>**Result** &lt;img src=x onerror=alert(1)&gt;</result><note>&lt;img src=x onerror=alert(1)&gt;</note></task-notification>` }];
    const { container } = render(<AssistantThread />);
    expect(screen.getByText('<script> "task" \'done\' &amp;')).toBeInTheDocument();
    expect(screen.getByText('Result').tagName).toBe('STRONG');
    expect(container.querySelector('script, img')).toBeNull();
  });
});

describe('AssistantThread slash-command skills', () => {
  const instructions = '# Review Pull Request\n\nReview the changes carefully.\n\nBase directory for this skill: /home/user/.config/opencode/skills/review-pr\nRelative paths in this skill (e.g., scripts/, references/) are relative to this base directory.';

  it.each([
    '/Users/user/.claude/skills/devtoys',
    'C:\\Users\\user\\.claude\\skills\\devtoys',
  ])('collapses Claude skill content from %s without losing the instructions', async (directory) => {
    const user = userEvent.setup();
    threadState.renderUser = true;
    const text = `Base directory for this skill: ${directory}\r\n\r\n# devtoys\r\n\r\nUse the CLI directly.\r\n\r\n## Introspection\r\nRun --help first.`;
    message.content = [{ type: 'text', text }];
    render(<AssistantThread />);

    const toggle = screen.getByRole('button', { name: '/devtoys' });
    expect(toggle).toHaveTextContent('→ Skill "devtoys"');
    expect(toggle).toHaveClass('oc-read-line');
    expect(toggle.closest('[data-message-id]')).not.toHaveClass('oc-msg-user');
    expect(toggle.closest('[data-message-id]')).toHaveClass('oc-msg-tools');
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    expect(screen.queryByText(/Use the CLI directly/)).not.toBeInTheDocument();
    await user.click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByText(/Use the CLI directly/).textContent).toBe(text);
    await user.click(toggle);
    expect(screen.queryByText(/Use the CLI directly/)).not.toBeInTheDocument();
  });

  it('keeps a Claude skill header quoted inside a user prompt as plain text', () => {
    threadState.renderUser = true;
    const text = 'Explain this:\nBase directory for this skill: /Users/user/.claude/skills/devtoys\n\n# devtoys\nUse the CLI directly.';
    message.content = [{ type: 'text', text }];
    const { container } = render(<AssistantThread />);
    expect(screen.queryByRole('button', { name: '/devtoys' })).not.toBeInTheDocument();
    expect(container.textContent).toContain(text);
  });

  it('collapses skill instructions and lets the user expand them', async () => {
    const user = userEvent.setup();
    threadState.renderUser = true;
    message.content = [{ type: 'text', text: instructions + '\n\nReview PR 42' }];
    render(<AssistantThread />);

    const toggle = screen.getByRole('button', { name: '/review-pr' });
    const body = () => document.querySelector('.oc-skill-called-body');
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    expect(body()).toBeNull();
    // The arguments stay inline next to the command, not inside the toggle.
    expect(screen.getByText('Review PR 42')).toBeInTheDocument();
    expect(toggle.closest('[data-message-id]')).toHaveClass('oc-msg-user');
    await user.click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    expect(body()?.textContent).toContain(instructions);
    await user.click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
  });

  it('keeps ordinary user text and partial footer mentions as plain text', () => {
    threadState.renderUser = true;
    const text = 'Explain this line:\nBase directory for this skill: /home/user/skills/review-pr';
    message.content = [{ type: 'text', text }];
    const { container } = render(<AssistantThread />);
    expect(container.querySelector('.oc-skill-called')).toBeNull();
    expect(container.textContent).toContain(text);
  });

  it('collapses a skill without arguments and with CRLF line endings', () => {
    threadState.renderUser = true;
    message.content = [{ type: 'text', text: instructions.replaceAll('\n', '\r\n') }];
    render(<AssistantThread />);
    expect(screen.getByRole('button', { name: '/review-pr' })).toHaveAttribute('aria-expanded', 'false');
    expect(screen.getByRole('button', { name: '/review-pr' }).closest('[data-message-id]')).not.toHaveClass('oc-msg-user');
  });

  it('does not collapse skill text quoted by the assistant', () => {
    message.content = [{ type: 'text', text: instructions }];
    render(<AssistantThread />);
    expect(screen.queryByRole('button', { name: '/review-pr' })).toBeNull();
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
  it('opens an attached image in the preview modal from the keyboard', async () => {
    const user = userEvent.setup();
    render(<ImageDisplay image="data:image/png;base64,AA" filename="shot.png" />);

    const trigger = screen.getByRole('button', { name: 'Expand shot.png' });
    await user.tab();
    expect(trigger).toHaveFocus();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();

    await user.keyboard('{Enter}');
    expect(screen.getByRole('dialog', { name: 'shot.png' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Zoom in' })).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Close image' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('falls back to a generic label when the attachment has no filename', () => {
    render(<ImageDisplay image="data:image/png;base64,AA" />);
    expect(screen.getByRole('button', { name: 'Expand Image' })).toBeInTheDocument();
  });
});

describe('AssistantThread pagination', () => {
  it('loads earlier history without scrolling when the oldest message is visible', () => {
    const onLoadMore = vi.fn();
    vi.stubGlobal('IntersectionObserver', class {
      callback: IntersectionObserverCallback;
      constructor(callback: IntersectionObserverCallback) { this.callback = callback; }
      observe(target: Element) {
        this.callback([{ target, isIntersecting: true } as IntersectionObserverEntry], this as unknown as IntersectionObserver);
      }
      disconnect() {}
    });

    render(<AssistantThread hasMore onLoadMore={onLoadMore} />);

    expect(onLoadMore).toHaveBeenCalledTimes(1);
  });

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

  it('opens an exploration group when jumping to a child tool', async () => {
    useUiStore.setState({ compactToolCalls: true });
    message.content = [
      { type: 'tool-call', toolCallId: 'read-1', toolName: '__read__', argsText: 'Read a.go' },
      { type: 'tool-call', toolCallId: 'read-2', toolName: '__read__', argsText: 'Read b.go' },
    ];
    const { container } = render(<AssistantThread scrollToToolCall={{ messageId: 'assistant-1', toolCallId: 'read-2', tick: 1 }} />);
    expect(container.querySelector('details')).toHaveAttribute('open');
    await vi.waitFor(() => expect(screen.getByText('Read b.go').parentElement).toHaveClass('oc-msg-scroll-highlight'));
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
  it('renders adjusted throughput while keeping elapsed duration', () => {
    useUiStore.getState().setShowMessageMetadata(true);
    message.metadata.custom = {
      time: { created: 1000, completed: 11000 }, tokens: { output: 200 }, tokensPerSecond: 100,
    };
    render(<AssistantThread />);
    expect(screen.getByText('100 tok/s')).toBeInTheDocument();
    expect(screen.queryByText('20 tok/s')).not.toBeInTheDocument();
    expect(screen.getByText('10s')).toBeInTheDocument();
  });
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

  it('stamps the turn summary with the time the turn ended', async () => {
    const started = turnStats.startedAt;
    turnStats.wallClockMs = 3 * 60 * 60 * 1000;
    try {
      const { container } = render(<AssistantThread />);
      const ended = started + turnStats.wallClockMs;
      const fmt = (ms: number) => new Date(ms).toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
      const stats = container.querySelector('.oc-turn-stats')!;
      expect(stats).toHaveTextContent(fmt(ended));
      expect(stats).not.toHaveTextContent(fmt(started));
      await userEvent.hover(screen.getByText(fmt(ended)));
      expect(await screen.findByRole('tooltip')).toHaveTextContent(formatFullDateTime(ended));
    } finally {
      turnStats.wallClockMs = 1000;
    }
  });

  it('shows a prompt cache rebuild warning once in the turn summary', () => {
    turnStats.promptCacheRebuilt = true;

    render(<AssistantThread />);

    expect(screen.getAllByText('Prompt cache rebuilt')).toHaveLength(1);
  });
});

describe('composer footer observer', () => {
  it('disconnects the footer ResizeObserver on unmount', () => {
    const footerObservers: { disconnected: boolean }[] = [];
    class TrackingResizeObserver {
      disconnected = false;
      observe(el: Element) {
        if (el.getAttribute('data-testid') === 'conversation-composer') footerObservers.push(this);
      }
      disconnect() { this.disconnected = true; }
    }
    vi.stubGlobal('ResizeObserver', TrackingResizeObserver);
    const { unmount } = render(<AssistantThread />);
    expect(footerObservers.length).toBeGreaterThan(0);
    unmount();
    expect(footerObservers.every((o) => o.disconnected)).toBe(true);
  });
});

describe('empty thread', () => {
  it('shows the default placeholder unless the caller supplies one', () => {
    const { rerender } = render(<AssistantThread />);
    expect(screen.getByText('No messages yet.')).toBeInTheDocument();
    rerender(<AssistantThread empty={<span>Starting session</span>} />);
    expect(screen.queryByText('No messages yet.')).toBeNull();
    expect(screen.getByText('Starting session')).toBeInTheDocument();
  });
});
