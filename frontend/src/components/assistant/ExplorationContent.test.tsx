// @vitest-environment jsdom
import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { ExplorationMessage, ExplorationPart } from '../../lib/exploration';
import { useUiStore } from '../../lib/uiStore';
import { PrintCollapseContext } from '../../lib/printCollapseContext';
import { ExplorationContent } from './ExplorationContent';

const state = vi.hoisted(() => ({ current: {} as { message: ExplorationMessage; thread: { messages: ExplorationMessage[] } } }));
vi.mock('@assistant-ui/react', () => ({
  useAuiState: (selector: (value: typeof state.current) => unknown) => selector(state.current),
  MessagePrimitive: {
    Content: () => <div>Normal content</div>,
    PartByIndex: ({ index }: { index: number }) => <div>Part {index}</div>,
  },
}));

const tool = (label: string, status = 'completed'): ExplorationPart => ({
  type: 'tool-call', toolCallId: label, toolName: '__read__', argsText: label, args: {},
  artifact: { explorationStatus: status },
});
function setMessages(content: ExplorationMessage['content'], following: ExplorationMessage['content'] = []) {
  const messages: ExplorationMessage[] = [{ id: 'a', role: 'assistant', content, metadata: { custom: {} } }];
  if (following.length) messages.push({ id: 'b', role: 'assistant', content: following, metadata: { custom: {} } });
  state.current = { message: messages[0], thread: { messages } };
}

beforeEach(() => {
  useUiStore.setState({ compactToolCalls: true });
  setMessages([tool('Grep Session'), tool('Read http.go'), tool('Read native.go')], [tool('Grep Status'), tool('Read activity.go'), tool('Read client.go')]);
});
afterEach(() => useUiStore.setState({ compactToolCalls: false }));

describe('compact exploration content', () => {
  it('collapses the example into one line and expands child rows', () => {
    const { container } = render(<ExplorationContent />);
    const summary = screen.getByText('Explored: 2 searches 4 files');
    const details = summary.closest('details')!;
    expect(details.open).toBe(false);
    fireEvent.click(summary);
    expect(details.open).toBe(true);
    expect(screen.getByText('Read client.go')).toBeVisible();
    expect(container.querySelectorAll('.oc-exploration-children .oc-read-arrow')).toHaveLength(6);
    expect(container.querySelector('[data-message-id="b"] [data-tool-call-id="Read client.go"]')).not.toBeNull();
    fireEvent.click(summary);
    expect(details.open).toBe(false);
  });

  it('updates the running label and counts without closing an expanded group', () => {
    setMessages([tool('Read a.go', 'pending')]);
    const { rerender } = render(<ExplorationContent />);
    const summary = screen.getByText('Exploring: 0 searches 1 file');
    fireEvent.click(summary);
    setMessages([tool('Read a.go'), tool('Grep b', 'running')]);
    rerender(<ExplorationContent />);
    expect(screen.getByText('Exploring: 1 search 1 file').closest('details')).toHaveAttribute('open');
    setMessages([tool('Read a.go'), tool('Grep b')]);
    rerender(<ExplorationContent />);
    expect(screen.getByText('Explored: 1 search 1 file').closest('details')).toHaveAttribute('open');
  });

  it('does not repeat a group in its following message', () => {
    state.current.message = state.current.thread.messages[1];
    const { container } = render(<ExplorationContent />);
    expect(container).toBeEmptyDOMElement();
  });

  it('keeps every tool from a grouped message reachable through its message anchor', () => {
    setMessages([tool('Grep A')], [tool('Read first.go'), tool('Read second.go')]);
    const { container } = render(<ExplorationContent />);
    const sourceMessage = container.querySelector('[data-message-id="b"]');
    expect(sourceMessage?.querySelector('[data-tool-call-id="Read second.go"]')).not.toBeNull();
  });

  it('keeps full mode unchanged', () => {
    useUiStore.setState({ compactToolCalls: false });
    render(<ExplorationContent />);
    expect(screen.getByText('Normal content')).toBeInTheDocument();
    expect(screen.queryByText(/Explored/)).not.toBeInTheDocument();
  });

  it('groups runs inside mixed messages without swallowing text, images, or other tools', () => {
    setMessages([
      { type: 'text', text: 'Checking' }, tool('Grep A'), tool('Read a.go'),
      { type: 'image', image: 'https://example.com/a.png' }, tool('Read b.go'),
      { ...tool('command'), toolName: 'bash' },
    ]);
    render(<ExplorationContent />);
    expect(screen.getByText('Part 0')).toBeInTheDocument();
    expect(screen.getByText('Part 3')).toBeInTheDocument();
    expect(screen.getByText('Part 5')).toBeInTheDocument();
    expect(screen.getByText('Explored: 1 search 1 file')).toBeInTheDocument();
    expect(screen.getByText('Explored: 0 searches 1 file')).toBeInTheDocument();
  });

  it('expands for printing unless print collapse is requested', () => {
    const { rerender } = render(<ExplorationContent />);
    act(() => window.dispatchEvent(new Event('beforeprint')));
    expect(screen.getByText(/Explored/).closest('details')).toHaveAttribute('open');
    rerender(<PrintCollapseContext.Provider value={true}><ExplorationContent /></PrintCollapseContext.Provider>);
    expect(screen.getByText(/Explored/).closest('details')).not.toHaveAttribute('open');
  });
});
