import { describe, it, expect } from 'vitest';
import type { Part } from './api';
import {
  permissionFromRequest,
  questionFromRequest,
  normalizeQuestionItems,
  hasQuestionOutput,
  extractPendingQuestionFromPart,
  extractPendingQuestionFromParts,
  hasPendingQuestionInParts,
  truncateSseData,
} from './sseHelpers';

const SID = 'sess_123';

function makePart(data: unknown, id = 'p1', messageId = 'm1'): Part {
  return { id, messageId, sessionId: SID, data: data as string };
}

const request = (value: unknown) => value as never;

describe('permissionFromRequest', () => {
  it('returns null for non-object requests', () => {
    expect(permissionFromRequest(request(null))).toBeNull();
    expect(permissionFromRequest(request('not an object'))).toBeNull();
    expect(permissionFromRequest(request([]))).toBeNull();
  });

  it('returns null when no id is present', () => {
    expect(permissionFromRequest(request({}))).toBeNull();
  });

  it('extracts an id, falling back to requestID', () => {
    expect(permissionFromRequest(request({ requestID: 'req_1' }))?.permissionId).toBe('req_1');
  });

  it('defaults permission text and patterns when missing', () => {
    const out = permissionFromRequest({ id: 'p1' });
    expect(out?.permission).toBe('Permission required');
    expect(out?.patterns).toEqual([]);
    expect(out?.sessionId).toBe('');
  });

  it('captures sessionID and permission patterns when present', () => {
    const out = permissionFromRequest(request({
      id: 'p1',
      permission: 'Run shell command?',
      patterns: ['ls *', 1, 'git status'],
      always: ['ls *', 1, 'git *'],
      metadata: { command: 'git status' },
      sessionID: 'sub_1',
    }));
    expect(out?.permission).toBe('Run shell command?');
    expect(out?.patterns).toEqual(['ls *', 'git status']); // non-string filtered
    expect(out?.always).toEqual(['ls *', 'git *']);
    expect(out?.metadata).toEqual({ command: 'git status' });
    expect(out?.sessionId).toBe('sub_1');
  });
});

describe('questionFromRequest', () => {
  const validQ = { question: 'q?', options: [] };

  it('returns null for non-object requests', () => {
    expect(questionFromRequest(request(null))).toBeNull();
  });

  it('returns null without a usable id', () => {
    expect(questionFromRequest(request({ questions: [validQ] }))).toBeNull();
  });

  it('returns null when no questions parse', () => {
    expect(questionFromRequest({ id: 'q1' })).toBeNull();
  });

  it('extracts a well-formed question request', () => {
    const out = questionFromRequest({ id: 'q1', sessionID: 'sub_1', questions: [validQ] });
    expect(out?.requestId).toBe('q1');
    expect(out?.sessionID).toBe('sub_1');
    expect(out?.questions).toHaveLength(1);
  });
});

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

describe('hasQuestionOutput', () => {
  it('treats null/undefined/empty/quoted-empty/empty-array as no answer', () => {
    expect(hasQuestionOutput(null)).toBe(false);
    expect(hasQuestionOutput(undefined)).toBe(false);
    expect(hasQuestionOutput('')).toBe(false);
    expect(hasQuestionOutput('""')).toBe(false);
    expect(hasQuestionOutput('[]')).toBe(false);
  });

  it('treats anything else as an answer', () => {
    expect(hasQuestionOutput('answer')).toBe(true);
    expect(hasQuestionOutput(0)).toBe(true);
    expect(hasQuestionOutput([])).toBe(true); // array literal — not the string '[]'
  });
});

describe('extractPendingQuestionFromPart', () => {
  it('returns null for non-tool parts', () => {
    expect(extractPendingQuestionFromPart(makePart({ type: 'text' }), SID)).toBeNull();
  });

  it('returns null for non-question tools', () => {
    expect(
      extractPendingQuestionFromPart(makePart({ type: 'tool', tool: 'read', state: {} }), SID),
    ).toBeNull();
  });

  it('returns null when status is not running and there is an answer', () => {
    expect(
      extractPendingQuestionFromPart(
        makePart({
          type: 'tool',
          tool: 'question',
          state: { status: 'completed', output: 'answered' },
        }),
        SID,
      ),
    ).toBeNull();
  });

  it('returns a PendingQuestion when running with parsable input', () => {
    const out = extractPendingQuestionFromPart(
      makePart({
        type: 'tool',
        tool: 'question',
        state: {
          status: 'running',
          input: {
            requestId: 'req_1',
            sessionID: 'sub_1',
            questions: [{ question: 'q?', options: [] }],
          },
        },
      }),
      SID,
    );
    expect(out?.requestId).toBe('req_1');
    expect(out?.sessionID).toBe('sub_1');
  });

  it('falls back to metadata for the requestId', () => {
    const out = extractPendingQuestionFromPart(
      makePart({
        type: 'tool',
        tool: 'mcp_question',
        state: {
          status: 'running',
          input: { questions: [{ question: 'q', options: [] }] },
          metadata: { requestId: 'req_meta' },
        },
      }),
      SID,
    );
    expect(out?.requestId).toBe('req_meta');
  });

  it('falls back to the page sessionId when none in input', () => {
    const out = extractPendingQuestionFromPart(
      makePart({
        type: 'tool',
        tool: 'question',
        state: {
          status: 'running',
          input: { id: 'r1', questions: [{ question: 'q', options: [] }] },
        },
      }),
      SID,
    );
    expect(out?.sessionID).toBe(SID);
  });

  it('handles JSON-string part data', () => {
    const part = makePart(
      JSON.stringify({
        type: 'tool',
        tool: 'question',
        state: { status: 'running', input: { id: 'r', questions: [{ question: 'q', options: [] }] } },
      }),
    );
    expect(extractPendingQuestionFromPart(part, SID)?.requestId).toBe('r');
  });

  it('returns null for malformed JSON-string data', () => {
    expect(extractPendingQuestionFromPart(makePart('{bad json' as unknown), SID)).toBeNull();
  });
});

describe('extractPendingQuestionFromParts', () => {
  const validPart = makePart(
    {
      type: 'tool',
      tool: 'question',
      state: { status: 'running', input: { id: 'q1', questions: [{ question: 'q', options: [] }] } },
    },
    'p_q',
  );

  it('walks newest-first and returns the first match', () => {
    expect(extractPendingQuestionFromParts([makePart({ type: 'text' }), validPart], SID)?.requestId).toBe('q1');
  });

  it('returns null when no part matches', () => {
    expect(extractPendingQuestionFromParts([makePart({ type: 'text' })], SID)).toBeNull();
    expect(extractPendingQuestionFromParts([], SID)).toBeNull();
  });
});

describe('hasPendingQuestionInParts', () => {
  it('returns true when extractPendingQuestionFromParts succeeds', () => {
    const part = makePart({
      type: 'tool',
      tool: 'question',
      state: { status: 'running', input: { id: 'r', questions: [{ question: 'q', options: [] }] } },
    });
    expect(hasPendingQuestionInParts([part], SID)).toBe(true);
  });

  it('returns true when a question tool has no answer (running or empty output)', () => {
    // No usable id, but state.status === 'running' should still be detected.
    const part = makePart({
      type: 'tool',
      tool: 'question',
      state: { status: 'running', input: {} },
    });
    expect(hasPendingQuestionInParts([part], SID)).toBe(true);
  });

  it('returns false when only answered question parts exist', () => {
    const part = makePart({
      type: 'tool',
      tool: 'question',
      state: { status: 'completed', output: '"answered"', input: {} },
    });
    expect(hasPendingQuestionInParts([part], SID)).toBe(false);
  });

  it('skips non-tool / non-question parts', () => {
    expect(hasPendingQuestionInParts([makePart({ type: 'text' })], SID)).toBe(false);
  });
});

describe('truncateSseData', () => {
  it('returns short strings unchanged', () => {
    expect(truncateSseData('short')).toBe('short');
  });

  it('appends ... and clips at max', () => {
    const long = 'a'.repeat(600);
    const out = truncateSseData(long);
    expect(out.length).toBe(503); // 500 + '...'
    expect(out.endsWith('...')).toBe(true);
  });

  it('respects a custom max', () => {
    expect(truncateSseData('abcdef', 3)).toBe('abc...');
  });
});
