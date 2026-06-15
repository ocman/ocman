import { describe, it, expect } from 'vitest';
import type { Part } from './api';
import {
  extractMessageFromEvent,
  extractPartFromEvent,
  isSessionStatusIdle,
  extractPendingPermission,
  extractPendingQuestion,
  normalizeQuestionItems,
  hasQuestionOutput,
  extractPendingQuestionFromPart,
  extractPendingQuestionFromParts,
  hasPendingQuestionInParts,
  answeredQuestionRequestId,
  truncateSseData,
} from './sseHelpers';

const SID = 'sess_123';

function makePart(data: unknown, id = 'p1', messageId = 'm1'): Part {
  return { id, messageId, sessionId: SID, data: data as string };
}

describe('extractMessageFromEvent', () => {
  it('returns null when there is no info object', () => {
    expect(extractMessageFromEvent({ properties: {} }, SID)).toBeNull();
  });

  it('returns null when info.id is missing', () => {
    expect(extractMessageFromEvent({ properties: { info: { role: 'assistant' } } }, SID)).toBeNull();
  });

  it('extracts a minimal message and falls back to role=assistant', () => {
    const out = extractMessageFromEvent({ properties: { info: { id: 'm1' } } }, SID);
    expect(out).not.toBeNull();
    expect(out!.message.id).toBe('m1');
    expect(out!.message.sessionId).toBe(SID);
    expect(out!.message.data.role).toBe('assistant');
    expect(out!.parts).toEqual([]);
  });

  it('uses the timeCreated from info.time when present', () => {
    const out = extractMessageFromEvent({
      properties: { info: { id: 'm1', time: { created: 12345 } } },
    }, SID);
    expect(out!.message.timeCreated).toBe(12345);
  });

  it('reads payload at the top level when there is no `properties` envelope', () => {
    const out = extractMessageFromEvent({ info: { id: 'm-top', role: 'user' } }, SID);
    expect(out).not.toBeNull();
    expect(out!.message.data.role).toBe('user');
  });

  it('skips lifecycle parts (step-start / step-finish / snapshot)', () => {
    const out = extractMessageFromEvent({
      properties: {
        info: { id: 'm1' },
        parts: [
          { id: 'p1', type: 'step-start' },
          { id: 'p2', type: 'step-finish' },
          { id: 'p3', type: 'snapshot' },
          { id: 'p4', type: 'text', text: 'hi' },
        ],
      },
    }, SID);
    expect(out!.parts.map((p) => p.id)).toEqual(['p4']);
  });

  it('synthesises ids and falls back to messageID/sessionID when missing', () => {
    const out = extractMessageFromEvent({
      properties: {
        info: { id: 'm1' },
        parts: [{ type: 'text', text: 'hello' }],
      },
    }, SID);
    expect(out!.parts).toHaveLength(1);
    expect(out!.parts[0].id).toBe('sse-part-m1-0');
    expect(out!.parts[0].messageId).toBe('m1');
    expect(out!.parts[0].sessionId).toBe(SID);
  });

  it('truncates oversized text/output strings', () => {
    const huge = 'x'.repeat(300_000);
    const out = extractMessageFromEvent({
      properties: {
        info: { id: 'm1' },
        parts: [{ id: 'p1', type: 'text', text: huge }],
      },
    }, SID);
    const part = out!.parts[0].data as unknown as { text: string };
    expect(part.text.length).toBeLessThan(huge.length);
    expect(part.text.endsWith('\n... (truncated)')).toBe(true);
  });
});

describe('extractPartFromEvent', () => {
  it('returns null without a partId/messageId', () => {
    expect(extractPartFromEvent({ properties: {} }, SID)).toBeNull();
    expect(extractPartFromEvent({ properties: { id: 'p1' } }, SID)).toBeNull();
  });

  it('returns null for lifecycle part types', () => {
    expect(extractPartFromEvent({
      properties: { id: 'p1', messageID: 'm1', type: 'step-start' },
    }, SID)).toBeNull();
  });

  it('returns a Part when the payload is well-formed', () => {
    const out = extractPartFromEvent({
      properties: { id: 'p1', messageID: 'm1', type: 'text', text: 'ok' },
    }, SID);
    expect(out).not.toBeNull();
    expect(out!.id).toBe('p1');
    expect(out!.messageId).toBe('m1');
  });

  it('falls back to top-level payload when no `properties` envelope', () => {
    const out = extractPartFromEvent({ id: 'p1', messageID: 'm1', type: 'text' }, SID);
    expect(out).not.toBeNull();
  });
});

describe('isSessionStatusIdle', () => {
  it('returns false without properties', () => {
    expect(isSessionStatusIdle({})).toBe(false);
  });

  it('returns true for string status === "idle"', () => {
    expect(isSessionStatusIdle({ properties: { status: 'idle' } })).toBe(true);
  });

  it('returns false for other string statuses', () => {
    expect(isSessionStatusIdle({ properties: { status: 'busy' } })).toBe(false);
  });

  it('handles object-shaped status with a type field', () => {
    expect(isSessionStatusIdle({ properties: { status: { type: 'idle' } } })).toBe(true);
    expect(isSessionStatusIdle({ properties: { status: { type: 'busy' } } })).toBe(false);
  });

  it('returns false for arrays and missing fields', () => {
    expect(isSessionStatusIdle({ properties: { status: [] } })).toBe(false);
    expect(isSessionStatusIdle({ properties: { status: null } })).toBe(false);
  });
});

describe('extractPendingPermission', () => {
  it('returns null for non-permission events', () => {
    expect(extractPendingPermission(null)).toBeNull();
    expect(extractPendingPermission('not an object')).toBeNull();
    expect(extractPendingPermission({ type: 'other' })).toBeNull();
    expect(extractPendingPermission([])).toBeNull();
  });

  it('returns null when no id is present', () => {
    expect(extractPendingPermission({
      type: 'permission.asked',
      properties: {},
    })).toBeNull();
  });

  it('extracts an id, falling back to requestID', () => {
    expect(extractPendingPermission({
      type: 'permission.asked',
      properties: { requestID: 'req_1' },
    })?.permissionId).toBe('req_1');
  });

  it('defaults permission text and patterns when missing', () => {
    const out = extractPendingPermission({
      type: 'permission.asked',
      properties: { id: 'p1' },
    });
    expect(out?.permission).toBe('Permission required');
    expect(out?.patterns).toEqual([]);
    expect(out?.sessionId).toBe('');
  });

  it('captures sessionID and patterns when present', () => {
    const out = extractPendingPermission({
      type: 'permission.asked',
      properties: {
        id: 'p1',
        permission: 'Run shell command?',
        patterns: ['ls *', 1, 'git status'],
        metadata: { command: 'git status' },
        sessionID: 'sub_1',
      },
    });
    expect(out?.permission).toBe('Run shell command?');
    expect(out?.patterns).toEqual(['ls *', 'git status']); // non-string filtered
    expect(out?.metadata).toEqual({ command: 'git status' });
    expect(out?.sessionId).toBe('sub_1');
  });
});

describe('extractPendingQuestion', () => {
  const validQ = { question: 'q?', options: [] };

  it('returns null for non-question events', () => {
    expect(extractPendingQuestion({ type: 'other' })).toBeNull();
  });

  it('returns null without a usable id', () => {
    expect(extractPendingQuestion({
      type: 'question.asked',
      properties: { questions: [validQ] },
    })).toBeNull();
  });

  it('returns null when no questions parse', () => {
    expect(extractPendingQuestion({
      type: 'question.asked',
      properties: { id: 'q1' },
    })).toBeNull();
  });

  it('extracts a well-formed question event', () => {
    const out = extractPendingQuestion({
      type: 'question.asked',
      properties: { id: 'q1', sessionID: 'sub_1', questions: [validQ] },
    });
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

describe('answeredQuestionRequestId', () => {
  it('returns the requestId for an answered question tool part', () => {
    const part = makePart({
      type: 'tool',
      tool: 'question',
      state: { status: 'completed', output: 'yes', input: { requestId: 'req_1' } },
    });
    expect(answeredQuestionRequestId(part)).toBe('req_1');
  });

  it('returns null while the question is still running', () => {
    const part = makePart({
      type: 'tool',
      tool: 'question',
      state: { status: 'running', output: '', input: { requestId: 'req_1' } },
    });
    expect(answeredQuestionRequestId(part)).toBeNull();
  });

  it('returns null when completed but output is empty', () => {
    const part = makePart({
      type: 'tool',
      tool: 'question',
      state: { status: 'completed', output: '', input: { requestId: 'req_1' } },
    });
    expect(answeredQuestionRequestId(part)).toBeNull();
  });

  it('falls back to metadata for the requestId', () => {
    const part = makePart({
      type: 'tool',
      tool: 'mcp_Question',
      state: { status: 'completed', output: 'x', input: {}, metadata: { id: 'meta_id' } },
    });
    expect(answeredQuestionRequestId(part)).toBe('meta_id');
  });

  it('returns null for non-question tool parts', () => {
    const part = makePart({
      type: 'tool',
      tool: 'bash',
      state: { status: 'completed', output: 'x', input: { requestId: 'req_1' } },
    });
    expect(answeredQuestionRequestId(part)).toBeNull();
  });

  it('returns null for malformed JSON-string data', () => {
    expect(answeredQuestionRequestId(makePart('{bad json' as unknown))).toBeNull();
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
