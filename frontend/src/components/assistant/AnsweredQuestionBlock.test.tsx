// @vitest-environment jsdom
import { describe, expect, it } from 'vitest';
import { render, screen, within } from '@testing-library/react';
import type { QuestionItem } from '../../lib/questions';
import { AnsweredQuestionBlock } from './AnsweredQuestionBlock';

const question: QuestionItem = {
  question: 'Which checks?', header: 'Checks', multiple: true,
  options: [
    { label: 'Unit', description: 'Run unit tests.' },
    { label: 'Lint', description: 'Check code style.' },
    { label: 'Unit, Lint', description: 'Use the combined preset.' },
  ],
};

describe('answered question cards', () => {
  it('keeps the selected description with its question and hides unselected choices', () => {
    render(<AnsweredQuestionBlock questions={[question]} answers={['Unit']} />);
    const card = within(screen.getByRole('article', { name: question.question }));
    expect(card.getByText('Checks')).toBeVisible();
    expect(card.queryByText(/Question \d/)).not.toBeInTheDocument();
    expect(card.getByText('Unit')).toBeVisible();
    expect(card.getByText('Run unit tests.')).toBeVisible();
    expect(card.queryByText('Check code style.')).not.toBeInTheDocument();
    expect(screen.getByRole('region', { name: 'Answered questions' })).toBeVisible();
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });

  it('prefers a whole option label containing commas over separate choices', () => {
    render(<AnsweredQuestionBlock questions={[question]} answers={['Unit, Lint']} />);
    expect(screen.getByText('Unit, Lint')).toBeVisible();
    expect(screen.getByText('Use the combined preset.')).toBeVisible();
    expect(screen.queryByText('Run unit tests.')).not.toBeInTheDocument();
  });

  it('shows each selected description in answer order, including comma-bearing labels', () => {
    render(<AnsweredQuestionBlock questions={[question]} answers={['Unit, Lint, Unit']} />);
    expect(screen.getByText('Use the combined preset.')).toBeVisible();
    expect(screen.getByText('Run unit tests.')).toBeVisible();
    expect(screen.getAllByText(/preset\.|tests\./).map((element) => element.textContent))
      .toEqual(['Use the combined preset.', 'Run unit tests.']);
  });

  it.each(['Custom response\nwith another line', 'Unit, Custom response', 'Unit tests', 'Unit, '])('preserves custom answer %s without guessing descriptions', (answer) => {
    render(<AnsweredQuestionBlock questions={[question]} answers={[answer]} />);
    expect(screen.getByText(answer, { collapseWhitespace: false, trim: false })).toBeVisible();
    expect(screen.queryByText('Run unit tests.')).not.toBeInTheDocument();
    expect(screen.queryByText('Check code style.')).not.toBeInTheDocument();
  });

  it('supports legacy options without descriptions and questions without headers', () => {
    const legacy = { question: 'Continue?', options: [{ label: 'Yes' }] } as QuestionItem;
    render(<AnsweredQuestionBlock questions={[legacy]} answers={['Yes']} />);
    expect(screen.queryByText(/Question \d/)).not.toBeInTheDocument();
    expect(screen.getByText('Yes')).toBeVisible();
  });

  it('does not invent a selected answer when history has fewer answers than questions', () => {
    const empty = { ...question, options: [{ label: '', description: 'Not selected.' }, ...question.options] };
    render(<AnsweredQuestionBlock questions={[empty]} answers={[]} />);
    expect(screen.getByText('No answer recorded')).toBeVisible();
    expect(screen.queryByText('Unit')).not.toBeInTheDocument();
    expect(screen.queryByText('Not selected.')).not.toBeInTheDocument();
  });

  it('ignores empty option labels when matching multi-select answers', () => {
    const empty = { ...question, options: [{ label: '', description: 'Not selected.' }, ...question.options] };
    render(<AnsweredQuestionBlock questions={[empty]} answers={['Lint, Unit']} />);
    expect(screen.getByText('Check code style.')).toBeVisible();
    expect(screen.getByText('Run unit tests.')).toBeVisible();
    expect(screen.queryByText('Not selected.')).not.toBeInTheDocument();
  });
});
