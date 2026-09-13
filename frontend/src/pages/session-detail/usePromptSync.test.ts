// @vitest-environment jsdom

import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Session } from '../../lib/api';
import type { PendingQuestion } from '../../components/session/QuestionPrompt';

const patchRecentSession = vi.fn();
const listPermissions = vi.fn<(sid?: string) => Promise<unknown[]>>(async () => []);
const listQuestions = vi.fn(async (): Promise<unknown[]> => []);

vi.mock('../../lib/apiStore', () => ({
  useApiStore: (selector: (s: Record<string, unknown>) => unknown) =>
    selector({ patchRecentSession, listPermissions, listQuestions }),
}));

import { usePromptSync, type UsePromptSyncOptions } from './usePromptSync';
import { loadPendingQuestion, storePendingQuestion } from './usePromptHandlers';

const rawQuestion = {
  id: 'q-1',
  sessionID: 'sess-1',
  questions: [{ question: 'pick one', header: 'h', options: [{ label: 'a' }] }],
};
const rawPermission = { id: 'perm-1', sessionID: 'sess-1', permission: 'Run shell' };
const pendingQ: PendingQuestion = {
  requestId: 'q-1',
  sessionID: 'sess-1',
  questions: [{ question: 'pick one', header: 'h', options: [{ label: 'a' }] }],
} as unknown as PendingQuestion;

function opts(over: Partial<UsePromptSyncOptions> = {}): UsePromptSyncOptions {
  return {
    id: 'sess-1',
    session: { id: 'sess-1' } as Session,
    parts: [],
    recentSessions: [],
    portAvailable: true,
    promptSessionIds: ['sess-1', 'child-1'],
    pendingPermission: null,
    pendingQuestion: null,
    clearPrompt: vi.fn(),
    setPendingPermission: vi.fn(),
    setPendingQuestion: vi.fn(),
    setPermissionError: vi.fn(),
    ...over,
  };
}

describe('usePromptSync', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sessionStorage.clear();
  });
  afterEach(() => vi.useRealTimers());

  it('mirrors prompt flags into the sidebar row', () => {
    renderHook(() => usePromptSync(opts({ pendingQuestion: pendingQ })));
    expect(patchRecentSession).toHaveBeenCalledWith('sess-1', { pendingPermission: false, pendingQuestion: true });
  });

  it('fetches a permission across the session tree when REST says one is pending', async () => {
    listPermissions.mockImplementation(async (sid?: string) => (sid === 'child-1' ? [rawPermission] : []));
    const o = opts({ session: { id: 'sess-1', pendingPermission: true } as Session });
    renderHook(() => usePromptSync(o));
    await waitFor(() => expect(o.setPendingPermission).toHaveBeenCalled());
    expect(listPermissions).toHaveBeenCalledTimes(2);
    expect(o.setPendingPermission).toHaveBeenCalledWith(
      expect.objectContaining({ permissionId: 'perm-1' }), ['sess-1', 'child-1']);
    expect(o.setPermissionError).toHaveBeenCalledWith(null);
  });

  it('fetches and stores a question when the sidebar row flags one', async () => {
    listQuestions.mockResolvedValue([rawQuestion]);
    const o = opts({ recentSessions: [{ id: 'sess-1', pendingQuestion: true } as Session] });
    renderHook(() => usePromptSync(o));
    await waitFor(() => expect(o.setPendingQuestion).toHaveBeenCalledWith(expect.objectContaining({ requestId: 'q-1' })));
    expect(loadPendingQuestion('sess-1')?.requestId).toBe('q-1');
  });

  it('dismisses a pending question once OpenCode no longer lists it', async () => {
    vi.useFakeTimers();
    listQuestions.mockResolvedValue([]);
    storePendingQuestion('sess-1', pendingQ);
    const o = opts({ pendingQuestion: pendingQ });
    renderHook(() => usePromptSync(o));
    await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
    expect(o.clearPrompt).toHaveBeenCalledWith('question', 'q-1');
    expect(loadPendingQuestion('sess-1')).toBeNull();
  });

  it('keeps the question while OpenCode still lists it', async () => {
    vi.useFakeTimers();
    listQuestions.mockResolvedValue([rawQuestion]);
    const o = opts({ pendingQuestion: pendingQ });
    renderHook(() => usePromptSync(o));
    await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
    expect(o.clearPrompt).not.toHaveBeenCalled();
  });

  it('restores a stored question when parts still show a pending question tool', () => {
    storePendingQuestion('sess-1', pendingQ);
    const parts = [{
      id: 'p1', messageId: 'm1', sessionId: 'sess-1',
      data: { type: 'tool', tool: 'question', state: { status: 'running', input: {} } },
    }] as unknown as UsePromptSyncOptions['parts'];
    const o = opts({ parts });
    renderHook(() => usePromptSync(o));
    expect(o.setPendingQuestion).toHaveBeenCalledWith(expect.objectContaining({ requestId: 'q-1' }));
  });
});
