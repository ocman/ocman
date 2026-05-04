import { describe, it, expect } from 'vitest';
import type { Message, Part } from './api';
import {
  MAX_OUTPUT_LEN,
  truncatePartField,
  insertMessageByTime,
  mergeParts,
  upsertPart,
  inferStatusFromMessage,
} from './sseMessageHelpers';

function makeMessage(id: string, timeCreated: number, overrides: Partial<Message['data']> = {}): Message {
  return {
    id,
    sessionId: 'sess',
    timeCreated,
    data: { role: 'assistant', ...overrides },
  };
}

function makePart(id: string, messageId = 'm1', data: unknown = '{}'): Part {
  return { id, messageId, sessionId: 'sess', data: data as string };
}

describe('truncatePartField', () => {
  it('returns short strings unchanged', () => {
    expect(truncatePartField('hello')).toBe('hello');
  });

  it('truncates long strings and appends the marker', () => {
    const long = 'x'.repeat(MAX_OUTPUT_LEN + 50);
    const result = truncatePartField(long);
    expect(typeof result).toBe('string');
    expect((result as string).length).toBe(MAX_OUTPUT_LEN + '\n... (truncated)'.length);
    expect((result as string).endsWith('\n... (truncated)')).toBe(true);
  });

  it('returns non-strings unchanged', () => {
    expect(truncatePartField(42)).toBe(42);
    expect(truncatePartField(null)).toBe(null);
    expect(truncatePartField(undefined)).toBe(undefined);
    const obj = { foo: 'bar' };
    expect(truncatePartField(obj)).toBe(obj);
  });

  it('does not truncate at exactly MAX_OUTPUT_LEN', () => {
    const exact = 'x'.repeat(MAX_OUTPUT_LEN);
    expect(truncatePartField(exact)).toBe(exact);
  });
});

describe('insertMessageByTime', () => {
  it('appends a message later than every existing message', () => {
    const prev = [makeMessage('a', 100), makeMessage('b', 200)];
    const next = makeMessage('c', 300);
    expect(insertMessageByTime(prev, next).map((m) => m.id)).toEqual(['a', 'b', 'c']);
  });

  it('inserts a message at the correct sorted position', () => {
    const prev = [makeMessage('a', 100), makeMessage('c', 300)];
    const next = makeMessage('b', 200);
    expect(insertMessageByTime(prev, next).map((m) => m.id)).toEqual(['a', 'b', 'c']);
  });

  it('replaces a message with the same id', () => {
    const prev = [makeMessage('a', 100), makeMessage('b', 200, { finish: 'old' })];
    const next = makeMessage('b', 200, { finish: 'new' });
    const result = insertMessageByTime(prev, next);
    expect(result).toHaveLength(2);
    expect(result.find((m) => m.id === 'b')?.data.finish).toBe('new');
  });

  it('drops temp- and error- placeholders', () => {
    const prev = [
      makeMessage('temp-1', 50, { role: 'user' }),
      makeMessage('error-1', 60, { role: 'assistant' }),
      makeMessage('a', 100),
    ];
    const next = makeMessage('b', 200);
    const result = insertMessageByTime(prev, next);
    expect(result.map((m) => m.id)).toEqual(['a', 'b']);
  });

  it('inserts before an existing same-time message (stable to insertion order)', () => {
    const prev = [makeMessage('a', 100), makeMessage('b', 200)];
    const next = makeMessage('aa', 100);
    // findIndex picks the first message with timeCreated > newMsg.time;
    // 'b' (200) is the first such — so 'aa' lands between 'a' and 'b'.
    expect(insertMessageByTime(prev, next).map((m) => m.id)).toEqual(['a', 'aa', 'b']);
  });
});

describe('mergeParts', () => {
  it('appends new parts when none of their ids overlap', () => {
    const prev = [makePart('p1')];
    const next = [makePart('p2'), makePart('p3')];
    expect(mergeParts(prev, next).map((p) => p.id)).toEqual(['p1', 'p2', 'p3']);
  });

  it('replaces parts with overlapping ids', () => {
    const prev = [makePart('p1', 'm1', 'old'), makePart('p2')];
    const next = [makePart('p1', 'm1', 'new')];
    const result = mergeParts(prev, next);
    expect(result).toHaveLength(2);
    expect(result.find((p) => p.id === 'p1')?.data).toBe('new');
  });

  it('drops part-temp- and part-error- placeholders', () => {
    const prev = [makePart('part-temp-1'), makePart('part-error-2'), makePart('p1')];
    const next = [makePart('p2')];
    expect(mergeParts(prev, next).map((p) => p.id)).toEqual(['p1', 'p2']);
  });

  it('returns the previous array unchanged when newParts is empty', () => {
    const prev = [makePart('p1')];
    expect(mergeParts(prev, [])).toBe(prev);
  });
});

describe('upsertPart', () => {
  it('appends when the id is not present', () => {
    const prev = [makePart('p1')];
    const result = upsertPart(prev, makePart('p2'));
    expect(result.map((p) => p.id)).toEqual(['p1', 'p2']);
  });

  it('replaces in place when the id matches', () => {
    const prev = [makePart('p1', 'm1', 'old'), makePart('p2')];
    const result = upsertPart(prev, makePart('p1', 'm1', 'new'));
    expect(result).toHaveLength(2);
    expect(result[0].data).toBe('new');
    expect(result[1].id).toBe('p2');
  });

  it('does not mutate the original array', () => {
    const prev = [makePart('p1', 'm1', 'old')];
    const result = upsertPart(prev, makePart('p1', 'm1', 'new'));
    expect(prev[0].data).toBe('old');
    expect(result).not.toBe(prev);
  });
});

describe('inferStatusFromMessage', () => {
  it('returns done for user messages', () => {
    expect(inferStatusFromMessage(makeMessage('a', 0, { role: 'user' }))).toBe('done');
  });

  it('returns error when finish === "error"', () => {
    expect(inferStatusFromMessage(makeMessage('a', 0, { role: 'assistant', finish: 'error' }))).toBe('error');
  });

  it('returns error when data.error is set', () => {
    expect(
      inferStatusFromMessage(makeMessage('a', 0, { role: 'assistant', error: { name: 'boom' } })),
    ).toBe('error');
  });

  it('returns waiting for assistant messages with a non-error finish reason', () => {
    expect(
      inferStatusFromMessage(makeMessage('a', 0, { role: 'assistant', finish: 'stop' })),
    ).toBe('waiting');
  });

  it('returns busy for assistant messages without a finish reason', () => {
    expect(inferStatusFromMessage(makeMessage('a', 0, { role: 'assistant' }))).toBe('busy');
  });
});
