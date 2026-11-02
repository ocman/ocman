import { describe, it, expect } from 'vitest';
import { normalizeQuestionItems, parseQuestions } from './questions';

describe('normalizeQuestionItems', () => {
  it('parses JSON strings', () => {
    const out = normalizeQuestionItems(JSON.stringify([{ question: 'q', options: [] }]));
    expect(out).toHaveLength(1);
  });

  it('returns [] for unparsable strings', () => {
    expect(normalizeQuestionItems('{not json')).toEqual([]);
  });

  it('unwraps `{ questions: [...] }` envelopes', () => {
    expect(normalizeQuestionItems({ questions: [{ question: 'q', options: [] }] })).toHaveLength(1);
  });

  it('drops malformed entries', () => {
    const out = normalizeQuestionItems([
      { question: 'ok', options: [] },
      { question: 123, options: [] }, // bad question type
      { question: 'no options' }, // missing options
      null,
      { options: [] }, // missing question
    ]);
    expect(out).toHaveLength(1);
    expect(out[0].question).toBe('ok');
  });

  it('returns [] for empty/non-array values', () => {
    expect(normalizeQuestionItems([])).toEqual([]);
    expect(normalizeQuestionItems(undefined)).toEqual([]);
    expect(normalizeQuestionItems(null)).toEqual([]);
  });
});

describe('parseQuestions', () => {
  it('parses a `{ questions: [...] }` envelope', () => {
    const argsText = `pending\n${JSON.stringify({
      questions: [
        { header: 'h', question: 'q1', options: [] },
      ],
    })}`;
    expect(parseQuestions(argsText)).toEqual([
      { header: 'h', question: 'q1', options: [] },
    ]);
  });

  it('parses a bare array', () => {
    const argsText = `running\n${JSON.stringify([
      { header: 'h', question: 'q', options: [] },
    ])}`;
    expect(parseQuestions(argsText)).toEqual([
      { header: 'h', question: 'q', options: [] },
    ]);
  });

  it('returns null when there is no payload after the status line', () => {
    expect(parseQuestions('only one line')).toBeNull();
  });

  it('returns null for malformed JSON', () => {
    expect(parseQuestions('status\n{not json')).toBeNull();
  });

  it('returns null when the payload does not look like questions', () => {
    expect(parseQuestions('status\n[]')).toBeNull();
    expect(parseQuestions('status\n[{}]')).toBeNull();
  });
});
