// @vitest-environment jsdom

import { fireEvent, render, screen } from '@testing-library/react';
import { beforeAll, describe, expect, it, vi } from 'vitest';
import type { Message, Part } from '../../lib/api';
import { ForkPicker } from './ForkPicker';

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

describe('ForkPicker', () => {
  it('lists full session and user messages only', () => {
    render(<ForkPicker open messages={messages} parts={parts} onSelect={vi.fn()} onClose={vi.fn()} />);

    expect(screen.getByText('Full session')).toBeInTheDocument();
    expect(screen.getByText('First prompt')).toBeInTheDocument();
    expect(screen.getByText('Latest prompt')).toBeInTheDocument();
    expect(screen.queryByText('Assistant reply')).not.toBeInTheDocument();
  });

  it('selects the full session or a specific user message', () => {
    const onSelect = vi.fn();
    const { rerender } = render(
      <ForkPicker open messages={messages} parts={parts} onSelect={onSelect} onClose={vi.fn()} />,
    );

    fireEvent.click(screen.getByText('Full session'));
    expect(onSelect).toHaveBeenLastCalledWith(undefined);

    rerender(<ForkPicker open messages={messages} parts={parts} onSelect={onSelect} onClose={vi.fn()} />);
    fireEvent.click(screen.getByText('Latest prompt'));
    expect(onSelect).toHaveBeenLastCalledWith('msg-user-2');
  });

  it('filters prompts by text', () => {
    render(<ForkPicker open messages={messages} parts={parts} onSelect={vi.fn()} onClose={vi.fn()} />);

    fireEvent.change(screen.getByPlaceholderText('Search user messages'), { target: { value: 'latest' } });
    expect(screen.getByText('Latest prompt')).toBeInTheDocument();
    expect(screen.queryByText('First prompt')).not.toBeInTheDocument();
  });
});
