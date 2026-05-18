// @vitest-environment jsdom
//
// Tests for `usePendingSend` — the small state slot that holds an
// optimistic user send (text + images) outside the SessionView's
// `messages[]`. Architecture §"The composer / optimistic send".
//
// Replaces the legacy `temp-<ts>` id system + reparentTempParts +
// id-prefix filtering with one rule: the bubble is visible while
// `pending` is set, and cleared when a server `message.created`
// lands for a user message we don't yet have.

import { describe, it, expect, afterEach, vi } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import type { Message, Part } from '../../lib/api';
import {
  usePendingSend,
  materializePending,
  type PendingSend,
} from './usePendingSend';

const SID = 'sess-1';

afterEach(() => {
  vi.useRealTimers();
});

function makeUserMessage(id: string, timeCreated: number): Message {
  return { id, sessionId: SID, timeCreated, data: { role: 'user' } };
}

describe('usePendingSend — basic lifecycle', () => {
  it('starts empty', () => {
    const { result } = renderHook(() => usePendingSend(SID));
    expect(result.current.pending).toBe(null);
  });

  it('begin() sets a pending entry with text + images', () => {
    const { result } = renderHook(() => usePendingSend(SID));
    act(() => {
      result.current.begin('hello', [{ url: 'data:image/png;base64,AAA', mime: 'image/png' }]);
    });
    expect(result.current.pending?.text).toBe('hello');
    expect(result.current.pending?.images).toHaveLength(1);
    expect(result.current.pending?.error).toBeUndefined();
    expect(typeof result.current.pending?.startedAt).toBe('number');
  });

  it('clear() resets to null', () => {
    const { result } = renderHook(() => usePendingSend(SID));
    act(() => {
      result.current.begin('hello');
    });
    act(() => {
      result.current.clear();
    });
    expect(result.current.pending).toBe(null);
  });

  it('fail() preserves text/images and adds an error', () => {
    const { result } = renderHook(() => usePendingSend(SID));
    act(() => {
      result.current.begin('hello', [{ url: 'data:image/png;base64,AAA', mime: 'image/png' }]);
    });
    act(() => {
      result.current.fail('Network down');
    });
    expect(result.current.pending?.error).toBe('Network down');
    expect(result.current.pending?.text).toBe('hello');
    expect(result.current.pending?.images).toHaveLength(1);
  });
});

describe('usePendingSend — server-confirmed clear', () => {
  it('clears when a new user message arrives that was not in prev messages', () => {
    const { result, rerender } = renderHook(
      ({ messages }: { messages: Message[] }) => {
        const hook = usePendingSend(SID);
        // Page mirrors observe-on-render: invoke the helper to
        // detect the new user message and clear pending.
        hook.observeMessages(messages);
        return hook;
      },
      { initialProps: { messages: [] as Message[] } },
    );
    act(() => {
      result.current.begin('hello');
    });
    expect(result.current.pending?.text).toBe('hello');

    // Server delivers the real user message — pending must clear.
    rerender({ messages: [makeUserMessage('m-real', 100)] });
    expect(result.current.pending).toBe(null);
  });

  it('does NOT clear pending when only the assistant replies (no user message)', () => {
    const { result, rerender } = renderHook(
      ({ messages }: { messages: Message[] }) => {
        const hook = usePendingSend(SID);
        hook.observeMessages(messages);
        return hook;
      },
      { initialProps: { messages: [] as Message[] } },
    );
    act(() => {
      result.current.begin('hello');
    });
    // Assistant message lands (e.g. streaming kicked off before the
    // user message was acked). Pending must stay until the user
    // message itself confirms.
    rerender({
      messages: [
        { id: 'm-assist', sessionId: SID, timeCreated: 5, data: { role: 'assistant' } },
      ],
    });
    expect(result.current.pending?.text).toBe('hello');
  });

  it('does NOT clear pending when the user message id matches one already present', () => {
    // Edge case: we initialised with a user message already in
    // messages (cold cache hit). A subsequent `message.updated` for
    // the same id must not be mistaken for a new send.
    const { result, rerender } = renderHook(
      ({ messages }: { messages: Message[] }) => {
        const hook = usePendingSend(SID);
        hook.observeMessages(messages);
        return hook;
      },
      { initialProps: { messages: [makeUserMessage('m-pre', 10)] as Message[] } },
    );
    act(() => {
      result.current.begin('hello');
    });
    rerender({ messages: [makeUserMessage('m-pre', 10)] });
    expect(result.current.pending?.text).toBe('hello');
  });
});

describe('usePendingSend — session reset', () => {
  it('clears pending when the session id changes', () => {
    const { result, rerender } = renderHook(
      ({ id }: { id: string }) => usePendingSend(id),
      { initialProps: { id: 'sess-a' } },
    );
    act(() => {
      result.current.begin('hello');
    });
    rerender({ id: 'sess-b' });
    expect(result.current.pending).toBe(null);
  });
});

describe('materializePending — concatenation helper', () => {
  it('appends a user message + parts after the last real message', () => {
    const pending: PendingSend = {
      id: 'pending-1',
      text: 'hi there',
      startedAt: 1_000,
    };
    const messages: Message[] = [makeUserMessage('m-a', 1)];
    const parts: Part[] = [];
    const { messages: mergedMessages, parts: mergedParts } = materializePending(
      SID,
      pending,
      messages,
      parts,
    );
    expect(mergedMessages).toHaveLength(2);
    expect(mergedMessages[1].id).toBe('pending-1');
    expect(mergedMessages[1].data.role).toBe('user');
    expect(mergedParts.find((p) => p.messageId === 'pending-1')).toBeDefined();
  });

  it('emits one part per image plus the text part', () => {
    const pending: PendingSend = {
      id: 'pending-1',
      text: 'caption',
      images: [
        { url: 'data:image/png;base64,AAA', mime: 'image/png' },
        { url: 'data:image/png;base64,BBB', mime: 'image/png' },
      ],
      startedAt: 1_000,
    };
    const { parts } = materializePending(SID, pending, [], []);
    expect(parts.filter((p) => p.messageId === 'pending-1')).toHaveLength(3);
  });

  it('returns the inputs unchanged when pending is null', () => {
    const messages: Message[] = [];
    const parts: Part[] = [];
    const result = materializePending(SID, null, messages, parts);
    expect(result.messages).toBe(messages);
    expect(result.parts).toBe(parts);
  });

  it('regression: user message bubble is visible immediately', () => {
    // Requirement: when the user types a message and presses send,
    // the bubble is visible immediately ("pending" affordance optional).
    const pending: PendingSend = {
      id: 'pending-1',
      text: 'my prompt',
      startedAt: 1_000,
    };
    const { messages } = materializePending(SID, pending, [], []);
    expect(messages).toHaveLength(1);
    expect(messages[0].data.role).toBe('user');
  });
});
