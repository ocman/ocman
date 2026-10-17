// @vitest-environment jsdom

import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Session } from '../../lib/api';
import type { PendingQuestion } from '../../components/session/QuestionPrompt';

const patchRecentSession = vi.hoisted(() => vi.fn());
vi.mock('../../lib/sessionsCache', () => ({ patchSessionLists: patchRecentSession }));
const listPermissions = vi.fn<(sid?: string, platform?: string) => Promise<unknown[]>>(async () => []);
const listQuestions = vi.fn<(sid?: string, platform?: string) => Promise<unknown[]>>(async () => []);
const refreshPermissions = vi.fn<(sid?: string) => Promise<unknown[]>>(async () => []);

vi.mock('../../lib/apiStore', () => ({
  useApiStore: (selector: (s: Record<string, unknown>) => unknown) =>
    selector({ listPermissions, listQuestions, refreshPermissions }),
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
    session: { id: 'sess-1', platform: 'r-box:opencode' } as Session,
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
    listPermissions.mockReset().mockResolvedValue([]);
    listQuestions.mockReset().mockResolvedValue([]);
    refreshPermissions.mockReset().mockResolvedValue([]);
    sessionStorage.clear();
  });
  afterEach(() => vi.useRealTimers());

  it('mirrors prompt flags into the sidebar row', () => {
    renderHook(() => usePromptSync(opts({ pendingQuestion: pendingQ })));
    // Owner-qualified: another machine's session with this id keeps its badge.
    expect(patchRecentSession).toHaveBeenCalledWith('sess-1', { pendingPermission: false, pendingQuestion: true }, 'r-box:opencode');
  });

  it('discovers prompts only on the viewed owner when session IDs collide', async () => {
    listPermissions.mockImplementation(async (_id, platform) => [{ ...rawPermission, id: `perm-${platform}` }]);
    listQuestions.mockImplementation(async (_id, platform) => [{ ...rawQuestion, id: `question-${platform}` }]);
    const o = opts({ promptSessionIds: ['sess-1'] });
    const { rerender } = renderHook(value => usePromptSync(value), { initialProps: o });
    await waitFor(() => expect(o.setPendingPermission).toHaveBeenCalledWith(
      expect.objectContaining({ permissionId: 'perm-r-box:opencode' }), ['sess-1']));
    expect(o.setPendingQuestion).toHaveBeenCalledWith(expect.objectContaining({ requestId: 'question-r-box:opencode' }));
    rerender({ ...o, session: { id: 'sess-1', platform: 'r-other:opencode' } as Session });
    await waitFor(() => expect(o.setPendingQuestion).toHaveBeenCalledWith(expect.objectContaining({ requestId: 'question-r-other:opencode' })));
    expect(listPermissions).toHaveBeenLastCalledWith('sess-1', 'r-other:opencode');
    expect(listQuestions).toHaveBeenLastCalledWith('sess-1', 'r-other:opencode');
  });

  it('waits for the loaded owner even when sidebar flags already report prompts', () => {
    renderHook(() => usePromptSync(opts({ session: null, recentSessions: [
      { id: 'sess-1', platform: 'opencode', pendingPermission: true, pendingQuestion: true } as Session,
    ] })));
    expect(listPermissions).not.toHaveBeenCalled();
    expect(listQuestions).not.toHaveBeenCalled();
  });

  it('recovers a live question without REST or sidebar prompt flags', async () => {
    listQuestions.mockResolvedValue([rawQuestion]);
    const o = opts();
    const { rerender } = renderHook((value) => usePromptSync(value), { initialProps: o });
    await waitFor(() => expect(o.setPendingQuestion).toHaveBeenCalledWith(expect.objectContaining({ requestId: 'q-1' })));
    expect(loadPendingQuestion('sess-1')?.requestId).toBe('q-1');
    rerender({ ...o, pendingQuestion: pendingQ });
    rerender({ ...o, pendingQuestion: null });
    // A stale server list must not reopen the question immediately after replying.
    expect(listQuestions).toHaveBeenCalledTimes(1);
  });

  it('recovers permissions across the active session tree without loading the sidebar', async () => {
    listPermissions.mockImplementation(async (id) => id === 'child-1' ? [{ ...rawPermission, sessionID: 'child-1' }] : []);
    const o = opts();
    renderHook(() => usePromptSync(o));
    await waitFor(() => expect(o.setPendingPermission).toHaveBeenCalledWith(
      expect.objectContaining({ permissionId: 'perm-1', sessionId: 'child-1' }), ['sess-1', 'child-1']));
  });

  it('does not cancel question recovery when an unrelated permission arrives', async () => {
    let finish!: (value: unknown[]) => void;
    listQuestions.mockReturnValue(new Promise(resolve => { finish = resolve; }));
    const o = opts();
    const { rerender } = renderHook((value) => usePromptSync(value), { initialProps: o });
    rerender({ ...o, pendingPermission: { permissionId: 'perm-1', permission: 'bash', patterns: [], sessionId: 'sess-1', askedAt: 0 } });
    await act(async () => { finish([rawQuestion]); });
    expect(o.setPendingQuestion).toHaveBeenCalledWith(expect.objectContaining({ requestId: 'q-1' }));
  });

  it.each([
    { id: 'next', platform: 'r-box:opencode' },
    { id: 'sess-1', platform: 'r-other:opencode' },
  ])('ignores recovery responses after changing session identity to %j', async (next) => {
    let finish!: (value: unknown[]) => void;
    listQuestions.mockReturnValueOnce(new Promise(resolve => { finish = resolve; })).mockResolvedValue([]);
    const o = opts();
    const { rerender } = renderHook((value) => usePromptSync(value), { initialProps: o });
    rerender({ ...o, id: next.id, session: next as Session, promptSessionIds: [next.id] });
    await act(async () => { finish([rawQuestion]); });
    expect(o.setPendingQuestion).not.toHaveBeenCalled();
  });

  it.each([
    { portAvailable: false },
    { session: null },
    { session: { id: 'previous', platform: 'opencode' } as Session },
  ])('does not bootstrap prompts before the requested session is live: %j', async (overrides) => {
    renderHook(() => usePromptSync(opts(overrides)));
    expect(listQuestions).not.toHaveBeenCalled();
    expect(listPermissions).not.toHaveBeenCalled();
  });

  it('fetches a permission across the session tree when REST says one is pending', async () => {
    listPermissions.mockImplementation(async (sid?: string) => (sid === 'child-1' ? [rawPermission] : []));
    const o = opts({ session: { id: 'sess-1', platform: 'r-box:opencode', pendingPermission: true } as Session });
    renderHook(() => usePromptSync(o));
    await waitFor(() => expect(o.setPendingPermission).toHaveBeenCalled());
    expect(listPermissions).toHaveBeenCalledTimes(2);
    expect(o.setPendingPermission).toHaveBeenCalledWith(
      expect.objectContaining({ permissionId: 'perm-1' }), ['sess-1', 'child-1']);
    expect(o.setPermissionError).toHaveBeenCalledWith(null);
  });

  it('fetches and stores a question when the sidebar row flags one', async () => {
    listQuestions.mockResolvedValue([rawQuestion]);
    const o = opts({ recentSessions: [{ id: 'sess-1', platform: 'r-box:opencode', pendingQuestion: true } as Session] });
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

  // A reply that lands while the stream is down (laptop asleep, judge
  // auto-approves, answered in another tab) is never replayed, and a
  // reconnect's reconcile keeps the in-memory prompt.
  it('dismisses a pending permission once OpenCode no longer lists it', async () => {
    vi.useFakeTimers();
    refreshPermissions.mockResolvedValue([]);
    const perm = { permissionId: 'perm-1', permission: 'Run shell', patterns: [], sessionId: 'child-1', askedAt: 0 };
    const o = opts({ pendingPermission: perm });
    renderHook(() => usePromptSync(o));
    // One miss may be the observed-prompt cache trailing the stream.
    await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
    expect(refreshPermissions).toHaveBeenCalledWith('child-1', 'r-box:opencode');
    expect(o.clearPrompt).not.toHaveBeenCalled();
    await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
    expect(o.clearPrompt).toHaveBeenCalledWith('permission', 'perm-1');
  });

  // The session stream can deliver a prompt the observed cache missed;
  // an empty cached list must never dismiss it.
  it('keeps the permission while the live list has it, even if the cache is empty', async () => {
    vi.useFakeTimers();
    listPermissions.mockResolvedValue([]);
    refreshPermissions.mockResolvedValue([{ ...rawPermission, sessionID: 'child-1' }]);
    const perm = { permissionId: 'perm-1', permission: 'Run shell', patterns: [], sessionId: 'child-1', askedAt: 0 };
    const o = opts({ pendingPermission: perm });
    renderHook(() => usePromptSync(o));
    await act(async () => { await vi.advanceTimersByTimeAsync(9000); });
    expect(o.clearPrompt).not.toHaveBeenCalled();
  });

  // A bare id can resolve to another machine with the same session id,
  // whose empty list would wrongly dismiss the prompt.
  it('pins the refresh to the session owner and skips it when unknown', async () => {
    vi.useFakeTimers();
    const perm = { permissionId: 'perm-1', permission: 'Run shell', patterns: [], sessionId: 'child-1', askedAt: 0 };
    const o = opts({ pendingPermission: perm, session: { id: 'sess-1' } as Session });
    renderHook(() => usePromptSync(o));
    await act(async () => { await vi.advanceTimersByTimeAsync(9000); });
    expect(refreshPermissions).not.toHaveBeenCalled();
    expect(o.clearPrompt).not.toHaveBeenCalled();
  });

  // AD-7a: a push-only owner's stream delivers every reply; nothing polls.
  it('never polls prompts for a push-only owner', async () => {
    vi.useFakeTimers();
    const perm = { permissionId: 'perm-1', permission: 'Run shell', patterns: [], sessionId: 'child-1', askedAt: 0 };
    const o = opts({ pendingPermission: perm, pendingQuestion: pendingQ, pushPrompts: true });
    renderHook(() => usePromptSync(o));
    await act(async () => { await vi.advanceTimersByTimeAsync(9000); });
    expect(refreshPermissions).not.toHaveBeenCalled();
    expect(listQuestions).not.toHaveBeenCalled();
    expect(o.clearPrompt).not.toHaveBeenCalled();
  });

  it('keeps the permission when the authoritative refresh fails', async () => {
    vi.useFakeTimers();
    refreshPermissions.mockRejectedValue(new Error('unreachable'));
    const perm = { permissionId: 'perm-1', permission: 'Run shell', patterns: [], sessionId: 'child-1', askedAt: 0 };
    const o = opts({ pendingPermission: perm });
    renderHook(() => usePromptSync(o));
    await act(async () => { await vi.advanceTimersByTimeAsync(9000); });
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
