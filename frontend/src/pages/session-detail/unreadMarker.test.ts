import { describe, it, expect } from 'vitest';
import { findFirstUnreadMessageId, countUnreadMessages } from './unreadMarker';
import type { Message } from '../../lib/api';

function msg(id: string, role: string, timeCreated: number): Message {
  return {
    id,
    sessionId: 's1',
    timeCreated,
    data: { role },
  };
}

describe('findFirstUnreadMessageId', () => {
  const messages: Message[] = [
    msg('u1', 'user', 100),
    msg('a1', 'assistant', 150),
    msg('u2', 'user', 200),
    msg('a2', 'assistant', 250),
    msg('a3', 'assistant', 300),
  ];

  it('returns null when cutoff is zero (never seen)', () => {
    expect(findFirstUnreadMessageId(messages, 0)).toBeNull();
  });

  it('returns null when cutoff is negative', () => {
    expect(findFirstUnreadMessageId(messages, -1)).toBeNull();
  });

  it('returns null when no message is newer than the cutoff', () => {
    expect(findFirstUnreadMessageId(messages, 999)).toBeNull();
  });

  it('returns the oldest non-user message past the cutoff', () => {
    // Cutoff 175 → next assistant is a2@250. a1@150 is before cutoff,
    // u2@200 is skipped (role=user).
    expect(findFirstUnreadMessageId(messages, 175)).toBe('a2');
  });

  it('skips user messages even when they are strictly newer than the cutoff', () => {
    // Cutoff 100: u2@200 is the oldest message strictly newer, but
    // because it's a user message we skip to a1@150 — except a1 is
    // older. So the answer is a2@250 (oldest non-user after cutoff).
    // Note: a1@150 IS > 100, so it should be returned first.
    expect(findFirstUnreadMessageId(messages, 100)).toBe('a1');
  });

  it('returns null when all newer messages are user messages', () => {
    const onlyUser: Message[] = [
      msg('a1', 'assistant', 100),
      msg('u1', 'user', 200),
      msg('u2', 'user', 300),
    ];
    expect(findFirstUnreadMessageId(onlyUser, 150)).toBeNull();
  });

  it('uses strict > comparison (cutoff equal to message time is treated as seen)', () => {
    const msgs: Message[] = [msg('a1', 'assistant', 100)];
    expect(findFirstUnreadMessageId(msgs, 100)).toBeNull();
  });
});

describe('countUnreadMessages', () => {
  const messages: Message[] = [
    msg('u1', 'user', 100),
    msg('a1', 'assistant', 150),
    msg('u2', 'user', 200),
    msg('a2', 'assistant', 250),
    msg('a3', 'assistant', 300),
  ];

  it('returns 0 when cutoff is zero', () => {
    expect(countUnreadMessages(messages, 0)).toBe(0);
  });

  it('counts only non-user messages past the cutoff', () => {
    // 175 → a2 and a3, skipping u2.
    expect(countUnreadMessages(messages, 175)).toBe(2);
  });

  it('returns 0 when all newer messages are from the user', () => {
    const onlyUser: Message[] = [
      msg('a1', 'assistant', 100),
      msg('u1', 'user', 200),
    ];
    expect(countUnreadMessages(onlyUser, 150)).toBe(0);
  });
});
