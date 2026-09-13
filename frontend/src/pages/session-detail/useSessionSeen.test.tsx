// @vitest-environment jsdom

import { renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ReactNode } from 'react';

vi.hoisted(() => {
  const mem = new Map<string, string>();
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: {
      getItem: (key: string) => mem.get(key) ?? null,
      setItem: (key: string, value: string) => void mem.set(key, value),
      removeItem: (key: string) => void mem.delete(key),
    },
  });
});

const recheckFaviconNotify = vi.fn();
vi.mock('../../lib/useFaviconNotify', () => ({ recheckFaviconNotify: () => recheckFaviconNotify() }));

import { useApiStore } from '../../lib/apiStore';
import { useUiStore } from '../../lib/uiStore';
import { HeaderContext, type HeaderInfo } from '../../lib/headerContext';
import type { SessionMetadata } from '../../lib/sessionReducer';
import { useSessionSeen } from './useSessionSeen';

const session = {
  id: 's1', platform: 'opencode', directory: '/home/u/repo', title: 'Fix bug', timeUpdated: 42, remoteId: 'r1',
} as SessionMetadata;

describe('useSessionSeen', () => {
  const markSessionSeen = vi.fn(async () => ({ ok: true }));
  const patchRecentSession = vi.fn();
  const setInfo = vi.fn();
  const wrapper = ({ children }: { children: ReactNode }) => (
    <HeaderContext.Provider value={{ info: {}, setInfo }}>{children}</HeaderContext.Provider>
  );

  beforeEach(() => {
    vi.clearAllMocks();
    useApiStore.setState({ markSessionSeen, patchRecentSession });
    useUiStore.setState({ lastOpenedSessionId: undefined });
  });

  it('marks seen everywhere, records the open, and publishes header info', async () => {
    const patchSession = vi.fn();
    const { unmount } = renderHook(() => useSessionSeen({ session, patchSession }), { wrapper });

    expect(patchSession).toHaveBeenCalledWith({ seen: true, archived: false });
    expect(patchRecentSession).toHaveBeenCalledWith('s1', { seen: true, archived: false });
    expect(markSessionSeen).toHaveBeenCalledWith('opencode', 's1', 42);
    await waitFor(() => expect(recheckFaviconNotify).toHaveBeenCalled());
    expect(useUiStore.getState().lastOpenedSessionId).toBe('s1');
    expect(document.title).toBe('Fix bug - ocman');
    expect(setInfo).toHaveBeenCalledWith(expect.objectContaining<HeaderInfo>({
      sessionId: 's1', sessionTitle: 'Fix bug', sessionRemoteId: 'r1', sessionProjectFull: '/home/u/repo',
    }));

    unmount();
    expect(setInfo).toHaveBeenLastCalledWith({});
  });

  it('does nothing until the session has loaded', () => {
    renderHook(() => useSessionSeen({ session: null, patchSession: vi.fn() }), { wrapper });
    expect(markSessionSeen).not.toHaveBeenCalled();
    expect(setInfo).not.toHaveBeenCalled();
    expect(document.title).toBe('Session - ocman');
  });
});
