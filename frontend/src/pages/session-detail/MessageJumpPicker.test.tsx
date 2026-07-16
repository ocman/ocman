// @vitest-environment jsdom

import { fireEvent, render, screen } from '@testing-library/react';
import { beforeAll, describe, expect, it, vi } from 'vitest';
import type { Message, Part } from '../../lib/api';
import { MessageJumpPicker } from './MessageJumpPicker';

const messages: Message[] = [
  { id: 'user-1', sessionId: 's1', timeCreated: 1_700_000_000_000, data: { role: 'user' } },
  { id: 'assistant-1', sessionId: 's1', timeCreated: 1_700_000_001_000, data: { role: 'assistant' } },
  { id: 'user-2', sessionId: 's1', timeCreated: 1_700_000_002_000, data: { role: 'user' } },
];

const parts: Part[] = [
  { id: 'p1', messageId: 'user-1', sessionId: 's1', data: { type: 'text', text: 'First prompt' } },
  { id: 'p2', messageId: 'assistant-1', sessionId: 's1', data: { type: 'text', text: 'Assistant reply' } },
  { id: 'p3', messageId: 'user-2', sessionId: 's1', data: { type: 'image', mime: 'image/png', url: 'data:image/png;base64,abc' } },
];

beforeAll(() => {
  Element.prototype.scrollIntoView = vi.fn();
});

describe('MessageJumpPicker', () => {
  it('lists every user message and selects its message ID', () => {
    const onSelect = vi.fn();
    render(
      <MessageJumpPicker open messages={messages} parts={parts} onSelect={onSelect} onClose={vi.fn()} />,
    );

    expect(screen.getByText('First prompt')).toBeInTheDocument();
    expect(screen.getByText('Message without text')).toBeInTheDocument();
    expect(screen.queryByText('Assistant reply')).not.toBeInTheDocument();

    fireEvent.click(screen.getByText('First prompt'));
    expect(onSelect).toHaveBeenCalledWith('user-1');
  });
});
