// @vitest-environment jsdom

import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Session } from '../../lib/api';
import { useApiStore } from '../../lib/apiStore';

let sessionChanged: ((sessionId: string, session?: Session, patch?: Partial<Session>) => void) | undefined;
let sseConnect: (() => void) | undefined;

vi.mock('../../lib/useGlobalEvents', () => ({
  onSessionChanged: (cb: (sessionId: string, session?: Session, patch?: Partial<Session>) => void) => {
    sessionChanged = cb;
    return () => { sessionChanged = undefined; };
  },
  onSseConnect: (cb: () => void) => {
    sseConnect = cb;
    return () => { sseConnect = undefined; };
  },
}));

import { useSidebarSessions } from './useSidebarSessions';

describe('useSidebarSessions live refresh', () => {
  const getSessions = vi.fn().mockResolvedValue([]);

  beforeEach(() => {
    getSessions.mockClear();
    sessionChanged = undefined;
    sseConnect = undefined;
    useApiStore.setState({
      getSessions,
      recentSessions: [{ id: 'session-1', status: 'done' } as Session],
      recentSessionsHash: '',
    });
  });

  it('refreshes on session changes and SSE reconnects', async () => {
    const abortController = new AbortController();
    renderHook(() => useSidebarSessions({
      id: undefined,
      sessionId: 'session-1',
      collapsedProjects: [],
      sidebarView: 'recent',
      abortSignalRef: { current: abortController },
      navigate: vi.fn(),
    }));

    await waitFor(() => expect(getSessions).toHaveBeenCalledTimes(1));

    act(() => sessionChanged?.('session-1', undefined, { status: 'busy' }));
    expect(useApiStore.getState().recentSessions[0].status).toBe('busy');
    expect(getSessions).toHaveBeenCalledTimes(1);

    act(() => sessionChanged?.('session-1'));
    await waitFor(() => expect(getSessions).toHaveBeenCalledTimes(2));

    act(() => sseConnect?.());
    await waitFor(() => expect(getSessions).toHaveBeenCalledTimes(3));
  });
});
