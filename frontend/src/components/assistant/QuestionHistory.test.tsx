// @vitest-environment jsdom
import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import type { ComponentProps } from 'react';
import type { Message, Part, PartData } from '../../lib/api';
import { createConvertMessages } from '../../lib/convertMessages';
import { ToolCallDisplay } from './ToolCallDisplay';

function renderQuestion(tool: string, completed: boolean) {
  const messages: Message[] = [{
    id: 'message', sessionId: 'session', timeCreated: 1000,
    data: { role: 'assistant' },
  }];
  const data: PartData = {
    type: 'tool', tool, callID: 'question-call',
    state: {
      status: completed ? 'completed' : 'running',
      input: { questions: [
        { question: 'Which marker?', header: 'Marker', options: [{ label: 'Alpha' }], multiSelect: false },
        { question: 'Which checks?', header: 'Checks', options: [{ label: 'Unit' }, { label: 'Lint' }], multiSelect: true },
      ] },
      output: completed
        ? 'User has answered your questions: "Which marker?"="Alpha", "Which checks?"="Unit, Lint". You can now continue with the user\'s answers in mind.'
        : undefined,
    },
  };
  const parts: Part[] = [{
    id: 'part', messageId: 'message', sessionId: 'session', timeCreated: 1000,
    data: JSON.stringify(data),
  }];
  const converted = createConvertMessages()(messages, parts);
  const content = converted[0].content as ComponentProps<typeof ToolCallDisplay>[];
  render(<ToolCallDisplay {...content[0]} />);
}

describe('question history', () => {
  // Claude's converter lowercases AskUserQuestion in both live and stored parts.
  it.each(['askuserquestion', 'AskUserQuestion', 'question'])('shows %s questions and answers without expanding a tool call', (tool) => {
    renderQuestion(tool, true);
    expect(screen.getByText('Which marker?')).toBeVisible();
    expect(screen.getByText('Alpha')).toBeVisible();
    expect(screen.getByText('Which checks?')).toBeVisible();
    expect(screen.getByText('Unit, Lint')).toBeVisible();
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });

  it.each(['askuserquestion', 'AskUserQuestion', 'question'])('shows a pending summary for %s without inventing an answer', (tool) => {
    renderQuestion(tool, false);
    expect(screen.getByText('Asked 2 questions')).toBeVisible();
    expect(screen.queryByText('Alpha')).not.toBeInTheDocument();
  });
});
