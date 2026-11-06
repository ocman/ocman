import { describe, expect, it, vi } from 'vitest';
import type { Message, Part } from './api';
import * as parsing from './parsePart';
import { createSpeechAnswers } from './speechAnswers';
import { computeTurnStats } from './turnStats';

const user: Message = { id: 'u', sessionId: 's', timeCreated: 1, data: { role: 'user' } };
const answer: Message = { ...user, id: 'a', data: { role: 'assistant', finish: 'stop' } };
const text: Part = { id: 'p', messageId: 'a', sessionId: 's', data: { type: 'text', text: 'Completed **answer**' } };

describe('speech answer index', () => {
  it('indexes once per snapshot and does not reparse unchanged completed parts', () => {
    const select = createSpeechAnswers();
    const messages = [user, answer];
    const parts = [text];
    const turns = computeTurnStats(messages, parts);
    const parse = vi.spyOn(parsing, 'parsePart');
    try {
      const initial = select(messages, parts, turns);
      expect(parse).toHaveBeenCalledTimes(1);
      expect(select([...messages], [...parts], turns)).toBe(initial);
      const unrelated = { ...text, id: 'stream', messageId: 'other' };
      expect(select(messages, [...parts, unrelated], turns)).toBe(initial);
      expect(parse).toHaveBeenCalledTimes(1);
      expect(select(messages, [...parts, unrelated], turns, 'a').size).toBe(0);
      expect(parse).toHaveBeenCalledTimes(1);
      expect(select(messages, parts, turns).get('a')).toBe('Completed answer');
      expect(parse).toHaveBeenCalledTimes(1);
    } finally { parse.mockRestore(); }
  });

  it('invalidates edited/deleted parts, eligibility changes, and removed messages', () => {
    const select = createSpeechAnswers();
    const messages = [user, answer];
    const turns = computeTurnStats(messages, [text]);
    expect(select(messages, [text], turns).get('a')).toBe('Completed answer');
    const edited = { ...text, data: JSON.stringify({ type: 'text', text: 'Updated' }) };
    expect(select(messages, [edited], turns).get('a')).toBe('Updated');
    const extra = { ...text, id: 'extra', data: { type: 'text', text: 'Second paragraph' } };
    expect(select(messages, [edited, extra], turns).get('a')).toBe('Updated Second paragraph');
    expect(select(messages, [], turns).size).toBe(0);
    expect(select(messages, [text], new Map()).size).toBe(0);
    expect(select(messages, [text], turns).get('a')).toBe('Completed answer');
    const failed = { ...answer, data: { ...answer.data, error: { name: 'Error' } } };
    expect(select([user, failed], [text], turns).size).toBe(0);
    expect(select([], [text], turns).size).toBe(0);
  });
});
