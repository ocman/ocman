// @vitest-environment jsdom

import { renderHook } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { Message } from '../../lib/api';
import type { SessionMetadata } from '../../lib/sessionReducer';
import { useUnreadMarker } from './useUnreadMarker';

const msg = (id: string, role: string, t: number): Message =>
  ({ id, sessionId: 's1', timeCreated: t, data: { role } }) as Message;
const messages = [msg('u1', 'user', 5), msg('a1', 'assistant', 10), msg('a2', 'assistant', 20)];

describe('useUnreadMarker', () => {
  it('snapshots the cutoff once per session and keeps it after seen advances', () => {
    const s1 = { id: 's1', seenTimeUpdated: 7 } as SessionMetadata;
    const { result, rerender } = renderHook(
      ({ session }: { session: SessionMetadata | null }) => useUnreadMarker(session, messages),
      { initialProps: { session: s1 as SessionMetadata | null } },
    );
    expect(result.current).toEqual({ firstUnreadMessageId: 'a1', unreadMessageCount: 2 });

    rerender({ session: { ...s1, seenTimeUpdated: 99 } });
    expect(result.current.firstUnreadMessageId).toBe('a1');

    rerender({ session: { id: 's2', seenTimeUpdated: 15 } as SessionMetadata });
    expect(result.current).toEqual({ firstUnreadMessageId: 'a2', unreadMessageCount: 1 });

    rerender({ session: null });
    expect(result.current.firstUnreadMessageId).toBeNull();
  });
});
