// @vitest-environment jsdom
import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import type { SessionStatus } from '../../lib/api';
import { convertToolPart, type ToolPartContext } from '../../lib/convertToolPart';
import { TaskToolCard } from './TaskToolCard';

describe('subagent lifecycle status', () => {
  it.each<[SessionStatus, string]>([
    ['busy', 'Running'], ['waiting', 'Completed'], ['done', 'Completed'],
    ['error', 'Error'], ['interrupted', 'Error'], ['limited', 'Error'],
  ])('shows child %s status instead of the completed launch call', (status, label) => {
    const context: ToolPartContext = {
      messageId: 'message', msgParts: [], msgPartsRaw: [], msgCompleted: 1,
      projectDirectory: '', pendingUserToolExecutionNotice: false,
      taskLiveOutput: { ses_child: { messages: [], parts: [], status } },
    };
    const { toolCall } = convertToolPart(context, {
      type: 'tool', tool: 'task', state: {
        status: 'completed', input: { description: 'Review' },
        output: 'task_id: ses_child\nAgent launched in background.',
      },
    }, 0, 'call');
    render(<MemoryRouter><TaskToolCard argsText={toolCall.argsText} result={toolCall.result} timeInfo={null} /></MemoryRouter>);
    expect(screen.getByText(label)).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Open detailed subagent session' })).toHaveAttribute('href', '/session/ses_child');
    if (status === 'busy') expect(screen.queryByText('Completed')).toBeNull();
  });
});
