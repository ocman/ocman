// @vitest-environment jsdom

import { fireEvent, render, screen } from '@testing-library/react';
import { beforeAll, describe, expect, it, vi } from 'vitest';
import type { Message, Part } from '../../lib/api';
import { UserMessagePicker } from './UserMessagePicker';

const messages: Message[] = [
  { id: 'msg-user-1', sessionId: 's1', timeCreated: 1_700_000_000_000, data: { role: 'user' } },
  { id: 'msg-assistant', sessionId: 's1', timeCreated: 1_700_000_001_000, data: { role: 'assistant' } },
  { id: 'msg-user-2', sessionId: 's1', timeCreated: 1_700_000_002_000, data: { role: 'user' } },
];

const parts: Part[] = [
  { id: 'p1', messageId: 'msg-user-1', sessionId: 's1', data: { type: 'text', text: 'First prompt' } },
  { id: 'p2', messageId: 'msg-assistant', sessionId: 's1', data: { type: 'text', text: 'Assistant reply' } },
  { id: 'p3', messageId: 'msg-user-2', sessionId: 's1', data: JSON.stringify({ type: 'text', text: 'Latest prompt' }) },
];

beforeAll(() => {
  Element.prototype.scrollIntoView = vi.fn();
});

describe('UserMessagePicker fork mode', () => {
  it('lists full session and user messages only', () => {
    render(<UserMessagePicker open mode="fork" messages={messages} parts={parts} onSelect={vi.fn()} onClose={vi.fn()} />);

    expect(screen.getByText('Full session')).toBeInTheDocument();
    expect(screen.getByText('First prompt')).toBeInTheDocument();
    expect(screen.getByText('Latest prompt')).toBeInTheDocument();
    expect(screen.queryByText('Assistant reply')).not.toBeInTheDocument();
  });

  it('selects the full session or a specific user message', () => {
    const onSelect = vi.fn();
    const { rerender } = render(
      <UserMessagePicker open mode="fork" messages={messages} parts={parts} onSelect={onSelect} onClose={vi.fn()} />,
    );

    fireEvent.click(screen.getByText('Full session'));
    expect(onSelect).toHaveBeenLastCalledWith(undefined);

    rerender(<UserMessagePicker open mode="fork" messages={messages} parts={parts} onSelect={onSelect} onClose={vi.fn()} />);
    fireEvent.click(screen.getByText('Latest prompt'));
    expect(onSelect).toHaveBeenLastCalledWith('msg-user-2');
  });

  it('offers only the full session when the platform cannot fork at a message', () => {
    const onSelect = vi.fn();
    render(<UserMessagePicker open mode="fork" fullSessionOnly messages={messages} parts={parts} onSelect={onSelect} onClose={vi.fn()} />);

    expect(screen.queryByText('First prompt')).not.toBeInTheDocument();
    fireEvent.click(screen.getByText('Full session'));
    expect(onSelect).toHaveBeenLastCalledWith(undefined);
  });

  it('filters prompts by text', () => {
    render(<UserMessagePicker open mode="fork" messages={messages} parts={parts} onSelect={vi.fn()} onClose={vi.fn()} />);

    fireEvent.change(screen.getByPlaceholderText('Search user messages'), { target: { value: 'latest' } });
    expect(screen.getByText('Latest prompt')).toBeInTheDocument();
    expect(screen.queryByText('First prompt')).not.toBeInTheDocument();
  });
});

describe('UserMessagePicker jump mode', () => {
  const jumpMessages: Message[] = [
    { id: 'user-1', sessionId: 's1', timeCreated: 1_700_000_000_000, data: { role: 'user' } },
    { id: 'assistant-1', sessionId: 's1', timeCreated: 1_700_000_001_000, data: { role: 'assistant' } },
    { id: 'user-2', sessionId: 's1', timeCreated: 1_700_000_002_000, data: { role: 'user' } },
  ];
  const jumpParts: Part[] = [
    { id: 'p1', messageId: 'user-1', sessionId: 's1', data: { type: 'text', text: 'First prompt' } },
    { id: 'p2', messageId: 'assistant-1', sessionId: 's1', data: { type: 'text', text: 'Assistant reply' } },
    { id: 'p3', messageId: 'user-2', sessionId: 's1', data: { type: 'image', mime: 'image/png', url: 'data:image/png;base64,abc' } },
  ];

  it('lists every user message and selects its message ID', () => {
    const onSelect = vi.fn();
    render(
      <UserMessagePicker open mode="jump" messages={jumpMessages} parts={jumpParts} onSelect={onSelect} onClose={vi.fn()} />,
    );

    expect(screen.getByText('First prompt')).toBeInTheDocument();
    expect(screen.getByText('Message without text')).toBeInTheDocument();
    expect(screen.queryByText('Assistant reply')).not.toBeInTheDocument();
    expect(screen.queryByText('Full session')).not.toBeInTheDocument();
    expect(screen.getByPlaceholderText('Search 2 user messages')).toBeInTheDocument();

    fireEvent.click(screen.getByText('First prompt'));
    expect(onSelect).toHaveBeenCalledWith('user-1');
  });
});
