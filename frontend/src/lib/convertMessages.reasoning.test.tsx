// @vitest-environment jsdom
import { render } from '@testing-library/react';
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
  expect(quote).toHaveTextContent('Last paragraph. · 11s');
  expect(quote).not.toHaveTextContent('Actual answer.');
  expect(container).toHaveTextContent('Actual answer.');
});
