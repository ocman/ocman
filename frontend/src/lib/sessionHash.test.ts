import { describe, it, expect } from 'vitest';
import { hashSession, hashMessagesAndParts } from './sessionHash';
import type { Session, Message, Part } from './api';

function makeSession(overrides: Partial<Session & {
  contextTokenCount?: number;
  defaultAgent?: string;
  defaultModel?: string;
}> = {}): Session & {
  contextTokenCount?: number;
  defaultAgent?: string;
  defaultModel?: string;
} {
  return {
    id: 'sess-1',
    platform: 'opencode',
    projectId: 'proj-1',
    title: 'A session',
    directory: '/tmp/project',
    timeCreated: 1_700_000_000_000,
    timeUpdated: 1_700_000_000_500,
    summaryAdditions: null,
    summaryDeletions: null,
    summaryFiles: null,
    shareUrl: null,
    messageCount: 0,
    durationMs: 500,
    totalInputTokens: 0,
    totalOutputTokens: 0,
    totalCost: 0,
    status: 'done',
    liveConnection: false,
    pendingPermission: false,
    pendingQuestion: false,
    archived: false,
    seen: true,
    ...overrides,
  };
}

function makeMessage(overrides: Partial<Message> = {}): Message {
  return {
    id: 'msg-1',
    sessionId: 'sess-1',
    timeCreated: 1_700_000_000_000,
    data: { role: 'user' },
    ...overrides,
  };
}

function makePart(overrides: Partial<Part> = {}): Part {
  return {
    id: 'part-1',
    messageId: 'msg-1',
    sessionId: 'sess-1',
    data: 'hello',
    ...overrides,
  };
}

describe('hashSession', () => {
  it('returns the same hash for identical session data', () => {
    const a = makeSession();
    const b = makeSession();
    expect(hashSession(a)).toBe(hashSession(b));
  });

  it('changes when the status changes', () => {
    const a = makeSession({ status: 'done' });
    const b = makeSession({ status: 'busy' });
    expect(hashSession(a)).not.toBe(hashSession(b));
  });

  it('changes when the title changes', () => {
    const a = makeSession({ title: 'Original' });
    const b = makeSession({ title: 'Renamed' });
    expect(hashSession(a)).not.toBe(hashSession(b));
  });

  it('changes when contextTokenCount changes', () => {
    const a = makeSession({ contextTokenCount: 100 });
    const b = makeSession({ contextTokenCount: 200 });
    expect(hashSession(a)).not.toBe(hashSession(b));
  });

  it('changes when defaultAgent changes', () => {
    const a = makeSession({ defaultAgent: 'build' });
    const b = makeSession({ defaultAgent: 'plan' });
    expect(hashSession(a)).not.toBe(hashSession(b));
  });

  it('changes when defaultModel changes', () => {
    const a = makeSession({ defaultModel: 'anthropic/claude-opus-4' });
    const b = makeSession({ defaultModel: 'anthropic/claude-sonnet-4' });
    expect(hashSession(a)).not.toBe(hashSession(b));
  });

  it('does not change for fields outside the hashed set', () => {
    const a = makeSession({ timeUpdated: 1 });
    const b = makeSession({ timeUpdated: 99_999 });
    expect(hashSession(a)).toBe(hashSession(b));
  });
});

describe('hashMessagesAndParts', () => {
  it('returns the same hash for identical inputs', () => {
    const msgs = [makeMessage()];
    const parts = [makePart()];
    expect(hashMessagesAndParts(msgs, parts)).toBe(hashMessagesAndParts(msgs, parts));
  });

  it('changes when a message is added', () => {
    const base = hashMessagesAndParts([makeMessage({ id: 'msg-1' })], []);
    const added = hashMessagesAndParts(
      [makeMessage({ id: 'msg-1' }), makeMessage({ id: 'msg-2' })],
      [],
    );
    expect(base).not.toBe(added);
  });

  it('changes when a message timeCreated changes', () => {
    const a = hashMessagesAndParts([makeMessage({ timeCreated: 1 })], []);
    const b = hashMessagesAndParts([makeMessage({ timeCreated: 2 })], []);
    expect(a).not.toBe(b);
  });

  it('changes when part data changes', () => {
    const a = hashMessagesAndParts([], [makePart({ data: 'hello' })]);
    const b = hashMessagesAndParts([], [makePart({ data: 'hello world' })]);
    expect(a).not.toBe(b);
  });

  it('changes when a part is added', () => {
    const base = hashMessagesAndParts([], [makePart({ id: 'part-1' })]);
    const added = hashMessagesAndParts(
      [],
      [makePart({ id: 'part-1' }), makePart({ id: 'part-2' })],
    );
    expect(base).not.toBe(added);
  });

  it('is order-sensitive for messages', () => {
    const a = hashMessagesAndParts(
      [makeMessage({ id: 'a', timeCreated: 1 }), makeMessage({ id: 'b', timeCreated: 2 })],
      [],
    );
    const b = hashMessagesAndParts(
      [makeMessage({ id: 'b', timeCreated: 2 }), makeMessage({ id: 'a', timeCreated: 1 })],
      [],
    );
    expect(a).not.toBe(b);
  });

  it('produces a stable hash for empty inputs', () => {
    expect(hashMessagesAndParts([], [])).toBe(hashMessagesAndParts([], []));
  });

  it('handles object part data', () => {
    const partA = makePart({ data: { type: 'text', text: 'hi' } as unknown as string });
    const partB = makePart({ data: { type: 'text', text: 'bye' } as unknown as string });
    expect(hashMessagesAndParts([], [partA])).not.toBe(hashMessagesAndParts([], [partB]));
  });
});
