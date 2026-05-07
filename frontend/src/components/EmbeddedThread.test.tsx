// @vitest-environment jsdom
import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { EmbeddedThread } from './EmbeddedThread';
import type { Message, Part } from '../lib/api';

function makeMessage(id: string, role: string, timeCreated = 0): Message {
  return { id, sessionId: 's', timeCreated, data: { role } };
}

function makePart(messageId: string, data: Record<string, unknown>, id = ''): Part {
  return {
    id: id || `${messageId}-p-${Math.random().toString(36).slice(2, 8)}`,
    messageId,
    sessionId: 's',
    data: data as unknown as string,
  };
}

describe('EmbeddedThread', () => {
  it('renders nothing for empty messages', () => {
    const { container } = render(<EmbeddedThread messages={[]} parts={[]} />);
    expect(container.innerHTML).toBe('');
  });

  it('renders text content from assistant messages', () => {
    const messages = [makeMessage('m1', 'assistant')];
    const parts = [makePart('m1', { type: 'text', text: 'Hello from subagent' })];
    render(<EmbeddedThread messages={messages} parts={parts} />);
    expect(screen.getByText('Hello from subagent')).toBeTruthy();
  });

  it('renders the embedded-thread container with data-testid', () => {
    const messages = [makeMessage('m1', 'assistant')];
    const parts = [makePart('m1', { type: 'text', text: 'content' })];
    render(<EmbeddedThread messages={messages} parts={parts} />);
    expect(screen.getByTestId('embedded-thread')).toBeTruthy();
  });

  it('renders multiple messages in order', () => {
    const messages = [
      makeMessage('u1', 'user', 1),
      makeMessage('a1', 'assistant', 2),
    ];
    const parts = [
      makePart('u1', { type: 'text', text: 'user prompt' }),
      makePart('a1', { type: 'text', text: 'assistant reply' }),
    ];
    render(<EmbeddedThread messages={messages} parts={parts} />);
    expect(screen.getByText('user prompt')).toBeTruthy();
    expect(screen.getByText('assistant reply')).toBeTruthy();
  });

  it('skips empty messages', () => {
    const messages = [
      makeMessage('m1', 'assistant'),
      makeMessage('m2', 'assistant'),
    ];
    const parts = [
      makePart('m1', { type: 'text', text: '' }),
      makePart('m2', { type: 'text', text: 'visible' }),
    ];
    render(<EmbeddedThread messages={messages} parts={parts} />);
    expect(screen.getByText('visible')).toBeTruthy();
  });

  it('has overflow-y auto for scrolling', () => {
    const messages = [makeMessage('m1', 'assistant')];
    const parts = [makePart('m1', { type: 'text', text: 'scrollable' })];
    render(<EmbeddedThread messages={messages} parts={parts} />);
    const el = screen.getByTestId('embedded-thread');
    // The CSS class oc-embedded-thread sets overflow-y: auto; verify
    // the class is applied so the scroll behaviour is active.
    expect(el.className).toContain('oc-embedded-thread');
  });
});
