// @vitest-environment jsdom
//
// #58: when the session is mid-turn, handleSend must NOT create an
// optimistic thread bubble (pending.begin) — that renders as a big
// "QUEUED" message in the thread, duplicating the compact queue list
// under the composer. It should POST directly (which enqueues
// server-side) and let the queue list surface it. When idle, the normal
// optimistic-bubble path runs.

import { describe, it, expect, beforeEach, vi } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { createRef } from 'react';
import type { MutableRefObject } from 'react';
import { useSessionActions, type UseSessionActionsOptions } from './useSessionActions';

const sendMessage = vi.fn().mockResolvedValue(undefined);

vi.mock('../../lib/apiStore', () => ({
  useApiStore: Object.assign(
    (selector: (s: Record<string, unknown>) => unknown) =>
      selector({
        sendMessage,
        abortSession: vi.fn().mockResolvedValue(undefined),
        archiveSession: vi.fn().mockResolvedValue(undefined),
        createSession: vi.fn().mockResolvedValue(undefined),
        launchOpencodeInTmux: vi.fn().mockResolvedValue(undefined),
        seedNewSession: vi.fn(),
      }),
    { getState: () => ({ pushClosedSession: vi.fn() }) },
  ),
}));

const { revertSession, unrevertSession } = vi.hoisted(() => ({
  revertSession: vi.fn().mockResolvedValue(undefined),
  unrevertSession: vi.fn().mockResolvedValue(undefined),
}));
vi.mock('../../lib/api', () => ({ api: { revertSession, unrevertSession } }));
vi.mock('../../lib/remoteLog', () => ({ remoteLog: { error: vi.fn() } }));

const begin = vi.fn().mockReturnValue('entry-1');

function makeOptions(isRunningRef: MutableRefObject<boolean>): UseSessionActionsOptions {
  return {
    session: { id: 'sess-1', platform: 'opencode', directory: '/p', timeUpdated: 0 },
    portAvailable: true,
    caps: {} as UseSessionActionsOptions['caps'],
    pendingPermission: null,
    pendingQuestion: null,
    selectedModel: 'anthropic/x',
    selectedAgent: 'build',
    selectedReasoning: '',
    activeAgent: 'build',
    recentSessionsRef: createRef<Array<{ id: string }>>() as MutableRefObject<Array<{ id: string }>>,
    messagesRef: { current: [] },
    partsRef: { current: [] },
    isRunningRef,
    tmuxAvailable: false,
    failedSends: [],
    setFailedSends: vi.fn(),
    pending: {
      pending: null,
      begin,
      fail: vi.fn(),
      clear: vi.fn(),
    } as unknown as UseSessionActionsOptions['pending'],
    navigate: vi.fn(),
    navigateToSession: vi.fn(),
    openWorktreeForm: vi.fn(),
    handleCompact: vi.fn().mockResolvedValue(undefined),
    handleNewSession: vi.fn().mockResolvedValue(undefined),
    handleTmuxShortcut: vi.fn(),
    setShowRenameModal: vi.fn(),
    setShowForkPicker: vi.fn(),
    setShowMovePicker: vi.fn(),
    setShowRenameToast: vi.fn(),
    setShowDisconnectedToast: vi.fn(),
    setRestartToastMessage: vi.fn(),
    setCopyToastMessage: vi.fn(),
  };
}

beforeEach(() => {
  sendMessage.mockClear();
  begin.mockClear();
  revertSession.mockClear();
  unrevertSession.mockClear();
});

describe('useSessionActions — undo and redo (#293)', () => {
  it('shows the disconnected notice instead of calling OpenCode when it is not live', async () => {
    const options = makeOptions({ current: false });
    options.portAvailable = false;
    const { result } = renderHook(() => useSessionActions(options));

    await act(async () => { await result.current.handleCommand('redo', ''); });

    expect(unrevertSession).not.toHaveBeenCalled();
    expect(options.setShowDisconnectedToast).toHaveBeenCalledWith(true);
  });

  it('reverts the last message then reloads the thread', async () => {
    const options = makeOptions({ current: false });
    options.messagesRef.current = [{ id: 'msg-1' } as never];
    options.refreshThread = vi.fn().mockResolvedValue(undefined);
    const { result } = renderHook(() => useSessionActions(options));

    await act(async () => { await result.current.handleCommand('undo', ''); });

    expect(revertSession).toHaveBeenCalledWith('sess-1', 'msg-1');
    expect(options.refreshThread).toHaveBeenCalledOnce();
  });

  it('restores reverted messages then reloads the thread', async () => {
    const options = makeOptions({ current: false });
    options.refreshThread = vi.fn().mockResolvedValue(undefined);
    const { result } = renderHook(() => useSessionActions(options));

    await act(async () => { await result.current.handleCommand('redo', ''); });

    expect(unrevertSession).toHaveBeenCalledWith('sess-1');
    expect(options.refreshThread).toHaveBeenCalledOnce();
  });
});

describe('useSessionActions — handleSend queue behaviour (#58)', () => {
  it('mid-turn: POSTs directly and does NOT create an optimistic bubble', async () => {
    const isRunningRef = { current: true };
    const { result } = renderHook(() => useSessionActions(makeOptions(isRunningRef)));

    await act(async () => {
      await result.current.handleSend('follow up');
    });

    expect(begin).not.toHaveBeenCalled(); // no "QUEUED" thread bubble
    // queue=true (last arg) tells the server to hold it, not drain it.
    expect(sendMessage).toHaveBeenCalledWith(
      'sess-1', 'follow up', undefined, 'anthropic/x', 'build', undefined, 'opencode', true,
    );
  });

  it('idle: uses the optimistic-bubble path', async () => {
    const isRunningRef = { current: false };
    const { result } = renderHook(() => useSessionActions(makeOptions(isRunningRef)));

    await act(async () => {
      await result.current.handleSend('first message');
    });

    expect(begin).toHaveBeenCalledTimes(1);
    // performSend still POSTs the message.
    expect(sendMessage).toHaveBeenCalled();
  });
});
