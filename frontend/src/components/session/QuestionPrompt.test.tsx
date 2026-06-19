// @vitest-environment jsdom

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
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

  describe('multiple-choice questions', () => {
    const multiQuestion: PendingQuestion = {
      requestId: 'q-multi',
      sessionID: 'sess-1',
      questions: [
        {
          question: 'Pick languages',
          header: 'Step 1',
          options: [
            { label: 'Go', description: '' },
            { label: 'TypeScript', description: '' },
            { label: 'Rust', description: '' },
          ],
          multiple: true,
        },
      ],
    };

    it('renders options as checkboxes and shows the multi-select hint', () => {
      render(<QuestionPrompt question={multiQuestion} onReply={vi.fn()} onReject={vi.fn()} />);
      expect(screen.getAllByRole('checkbox')).toHaveLength(3);
      expect(screen.getByText(/select all that apply/i)).toBeTruthy();
    });

    it('lets the user select multiple options without auto-submitting', async () => {
      const user = userEvent.setup();
      const onReply = vi.fn();
      render(<QuestionPrompt question={multiQuestion} onReply={onReply} onReject={vi.fn()} />);

      await user.click(screen.getByRole('checkbox', { name: /Go/ }));
      await user.click(screen.getByRole('checkbox', { name: /Rust/ }));

      // Clicking options must NOT submit on its own.
      expect(onReply).not.toHaveBeenCalled();

      const go = screen.getByRole('checkbox', { name: /Go/ });
      const rust = screen.getByRole('checkbox', { name: /Rust/ });
      expect(go.getAttribute('aria-checked')).toBe('true');
      expect(rust.getAttribute('aria-checked')).toBe('true');

      await user.click(screen.getByRole('button', { name: 'Submit' }));
      expect(onReply).toHaveBeenCalledWith([['Go', 'Rust']]);
    });

    it('toggles an option off when clicked twice', async () => {
      const user = userEvent.setup();
      render(<QuestionPrompt question={multiQuestion} onReply={vi.fn()} onReject={vi.fn()} />);

      const ts = screen.getByRole('checkbox', { name: /TypeScript/ });
      await user.click(ts);
      expect(screen.getByRole('checkbox', { name: /TypeScript/ }).getAttribute('aria-checked')).toBe('true');
      await user.click(screen.getByRole('checkbox', { name: /TypeScript/ }));
      expect(screen.getByRole('checkbox', { name: /TypeScript/ }).getAttribute('aria-checked')).toBe('false');
    });

    it('keeps Submit disabled until at least one option is selected', async () => {
      const user = userEvent.setup();
      render(<QuestionPrompt question={multiQuestion} onReply={vi.fn()} onReject={vi.fn()} />);

      const submit = screen.getByRole('button', { name: 'Submit' });
      expect(submit.hasAttribute('disabled')).toBe(true);
      await user.click(screen.getByRole('checkbox', { name: /Go/ }));
      expect(screen.getByRole('button', { name: 'Submit' }).hasAttribute('disabled')).toBe(false);
    });
  });
});
