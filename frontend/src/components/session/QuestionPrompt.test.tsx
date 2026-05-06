// @vitest-environment jsdom

import { render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { QuestionPrompt, type PendingQuestion } from './QuestionPrompt';

const question: PendingQuestion = {
  requestId: 'q-1',
  sessionID: 'sess-1',
  questions: [
    {
      question: 'Which approach?',
      header: 'Step 1',
      options: [
        { label: 'Option A', description: 'First choice' },
        { label: 'Option B', description: 'Second choice' },
      ],
      multiple: false,
      custom: true,
    },
  ],
};

describe('QuestionPrompt', () => {
  it('handles number-key selection even when the dialog is not focused', async () => {
    const onReply = vi.fn();
    const onReject = vi.fn();
    render(<QuestionPrompt question={question} onReply={onReply} onReject={onReject} />);

    screen.getByRole('dialog', { name: 'Pending question' });
    document.body.focus();
    window.dispatchEvent(new KeyboardEvent('keydown', { key: '1', bubbles: true }));

    await waitFor(() => expect(onReply).toHaveBeenCalledWith([['Option A']]));
    expect(onReject).not.toHaveBeenCalled();
  });

  it('handles Escape dismissal even when the dialog is not focused', async () => {
    const onReply = vi.fn();
    const onReject = vi.fn();
    render(<QuestionPrompt question={question} onReply={onReply} onReject={onReject} />);

    screen.getByRole('dialog', { name: 'Pending question' });
    document.body.focus();
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));

    await waitFor(() => expect(onReject).toHaveBeenCalledTimes(1));
    expect(onReply).not.toHaveBeenCalled();
  });
});
