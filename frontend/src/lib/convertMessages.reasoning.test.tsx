// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { expect, it } from 'vitest';
import type { Message, Part } from './api';
import { createConvertMessages } from './convertMessages';
import { MarkdownContent } from '../components/assistant/MarkdownText';

it.each([undefined, 11000])('keeps multiline reasoning inside its quote with end=%s', (end) => {
  const messages: Message[] = [{ id: 'm', sessionId: 's', timeCreated: 0, data: { role: 'assistant' } }];
  const parts: Part[] = [
    { id: 'reasoning', messageId: 'm', sessionId: 's', data: JSON.stringify({
      type: 'reasoning',
      text: 'First paragraph.\n\nSecond paragraph.\n\n- Reasoning item\n\n```text\nreasoning code\n```\n\nLast paragraph.',
      time: { start: 0, end },
    }) },
    { id: 'answer', messageId: 'm', sessionId: 's', data: JSON.stringify({
      type: 'text', text: 'Actual answer.',
    }) },
  ];
  const converted = createConvertMessages()(messages, parts, undefined, undefined, undefined, undefined, true, 11000);
  const content = converted[0].content;
  const text = typeof content === 'string' ? content : content
    .filter(part => part.type === 'text').map(part => part.text).join('\n');
  const { container } = render(<MarkdownContent text={text} />);
  const quote = container.querySelector('blockquote');
  expect(container.querySelectorAll('blockquote')).toHaveLength(1);
  expect(quote).toHaveTextContent(`${end === undefined ? 'Thinking' : 'Thought'}:`);
  expect(quote).toHaveTextContent('Second paragraph.');
  expect(quote).toContainElement(container.querySelector('li'));
  expect(quote).toContainElement(container.querySelector('pre'));
  expect(quote).toHaveTextContent('Last paragraph.');
  expect(quote).not.toHaveTextContent('Actual answer.');
  expect(container).toHaveTextContent('Actual answer.');
  const details = quote!.querySelector('details')!;
  const summary = details.querySelector('summary')!;
  expect(details).not.toHaveAttribute('open');
  expect(summary).toHaveTextContent('First paragraph. · 11s');
  expect(quote!.textContent?.match(/· 11s/g)).toHaveLength(1);
  expect(summary).not.toHaveTextContent('Second paragraph.');
  expect(summary).not.toHaveTextContent('Last paragraph.');
  fireEvent.click(summary);
  expect(details).toHaveAttribute('open');
  fireEvent.click(summary);
  expect(details).not.toHaveAttribute('open');
});

it.each(['0s', '7.8s', '1m 5s', '2h 3m', '1d 2h'])(
  'keeps duration %s after the visible preview with formatted reasoning', (duration) => {
    const { container } = render(<MarkdownContent text={`> **Thought:** First **paragraph**.\n> Another line.\n>\n> Last *paragraph*. · ${duration}`} />);
    expect(container.querySelector('summary')).toHaveTextContent(`Thought: First paragraph. Another line. · ${duration}`);
    expect(container.querySelector('summary strong:last-child')).toHaveTextContent('paragraph');
    expect(container.querySelector('details > p')).toHaveTextContent('Last paragraph.');
    expect(container.querySelector('details > p')).not.toHaveTextContent(`· ${duration}`);
  },
);

it('keeps the timer visible when a list interrupts the preview without a blank line', () => {
  const { container } = render(<MarkdownContent text={'> **Thought:** Preview.\n> - First item\n> - Last *item*. · 11s'} />);
  expect(container.querySelector('summary')).toHaveTextContent('Thought: Preview. · 11s');
  expect(container.querySelector('li:last-child')).toHaveTextContent('Last item.');
  expect(container.querySelector('li:last-child')).not.toHaveTextContent('· 11s');
});

it('preserves literal reasoning examples in fenced code', () => {
  const code = '> **Thought:** Preview.\n>\n> Hidden reasoning. · 11s';
  const { container } = render(<MarkdownContent text={`\`\`\`text\n${code}\n\`\`\``} />);
  expect(container.querySelector('pre code')?.textContent).toBe(`${code}\n`);
});

it.each(['**Note:** First paragraph.', 'First paragraph.', '# Heading'])('leaves ordinary blockquotes expanded: %s', (first) => {
  const { container } = render(<MarkdownContent text={`> ${first}\n>\n> Second paragraph.`} />);
  expect(container.querySelector('details')).not.toBeInTheDocument();
  expect(screen.getByText('Second paragraph.')).toBeVisible();
});

it('keeps a single-paragraph preview collapsed as more reasoning arrives', () => {
  const { container, rerender } = render(<MarkdownContent text={'> **Thinking:** Working.'} />);
  expect(container.querySelector('summary')).toHaveTextContent('Thinking: Working.');
  expect(container.querySelector('details')).not.toHaveAttribute('open');
  rerender(<MarkdownContent text={'> **Thinking:** Working.\n>\n> More reasoning.'} />);
  expect(container.querySelector('summary')).not.toHaveTextContent('More reasoning.');
  expect(container.querySelector('details')).not.toHaveAttribute('open');
});

it('keeps expanded reasoning open while it streams and finishes', () => {
  const { container, rerender } = render(<MarkdownContent text={'> **Thinking:** First paragraph.\n>\n> Still working.'} />);
  const summary = container.querySelector('summary')!;
  fireEvent.click(summary);
  rerender(<MarkdownContent text={'> **Thinking:** First paragraph.\n>\n> Still working. More text.'} />);
  expect(container.querySelector('details')).toHaveAttribute('open');
  rerender(<MarkdownContent text={'> **Thought:** First paragraph.\n>\n> Finished. · 11s'} />);
  expect(container.querySelector('details')).toHaveAttribute('open');
  expect(container.querySelector('summary')).toHaveTextContent('Thought: First paragraph.');
});
