// @vitest-environment jsdom
import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { convertToolPart, type ToolPartContext } from '../../lib/convertToolPart';
import { TaskToolCard } from './TaskToolCard';

const footer = `\nagentId: a000000000000000d (use SendMessage with to: 'a000000000000000d', summary: '<5-10 word recap>' to continue this agent)
<usage>subagent_tokens: 15619
tool_uses: 1
duration_ms: 3349</usage>`;

function renderResult(output: string) {
  const context: ToolPartContext = { messageId: 'message', msgParts: [], msgPartsRaw: [], msgCompleted: 1, projectDirectory: '', pendingUserToolExecutionNotice: false };
  const { toolCall } = convertToolPart(context, { type: 'tool', tool: 'task', state: { status: 'completed', input: { description: 'Review' }, output } }, 0, 'call');
  return { ...render(<MemoryRouter><TaskToolCard argsText={toolCall.argsText} result={toolCall.result} timeInfo={null} /></MemoryRouter>), converted: JSON.parse(toolCall.result!) };
}

describe('Claude Agent result details', () => {
  it('separates the recorded Agent footer from the Markdown answer', async () => {
    renderResult('**Done**' + footer);
    const result = screen.getByRole('button', { name: 'Expand subagent task result' });
    expect(result.textContent).toBe('Done');
    expect(screen.getByText('Done').tagName).toBe('STRONG');
    const details = screen.getByText('Task details').closest('details');
    expect(details).not.toHaveAttribute('open');
    await userEvent.click(screen.getByText('Task details'));
    expect(details).toHaveAttribute('open');
    expect(details).toHaveTextContent('a000000000000000d');
    expect(details).toHaveTextContent('SendMessage');
    expect(details).toHaveTextContent('15619');
    expect(details).toHaveTextContent('3349');
    expect(screen.queryByRole('link', { name: 'Open detailed subagent session' })).toBeNull();
  });

  it('retains metadata even when a long answer is truncated', () => {
    renderResult('x'.repeat(6000) + footer);
    expect(screen.getByText('Task details').closest('details')).toHaveTextContent('15619');
    expect(screen.getByRole('button', { name: 'Expand subagent task result' })).not.toHaveTextContent('agentId');
  });

  it.each(['Ordinary answer', 'Explain <usage> tags', 'Answer\nagentId: example', 'Answer' + footer + '\nMore answer'])('preserves ordinary and incomplete footer text', (text) => {
    const { converted } = renderResult(text);
    expect(converted.taskOutput).toBe(text);
    expect(screen.queryByText('Task details')).toBeNull();
  });
});

describe('Claude tool-result image attachments', () => {
  it.each(['read', 'task', 'mcp__browser__screenshot'])('preserves images from %s results', (tool) => {
    const context: ToolPartContext = { messageId: 'message', msgParts: [], msgPartsRaw: [], msgCompleted: 1, projectDirectory: '', pendingUserToolExecutionNotice: false };
    const { images } = convertToolPart(context, { type: 'tool', tool, state: { status: 'completed', input: {}, attachments: [{ type: 'file', mime: 'image/png', url: 'data:image/png;base64,aGVsbG8=' }] } }, 0, 'call');
    expect(images).toEqual([{ type: 'image', image: 'data:image/png;base64,aGVsbG8=' }]);
  });
});
