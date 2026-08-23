// @vitest-environment jsdom
//
// #58: queueing is an explicit user gesture (Ctrl/Cmd+Enter), not an
// inference from the running state. handleSend(text, images, queue=true)
// must POST with queue=true and NOT create an optimistic thread bubble
// (pending.begin) — the compact queue list under the composer owns it.
// Without the flag the normal optimistic-bubble send runs, mid-turn
// included, so the running turn picks the prompt up instead of the user
// waiting for the whole turn to end.

import { describe, it, expect, beforeEach, vi } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { createRef } from 'react';
import type { MutableRefObject } from 'react';
import { useSessionActions, type UseSessionActionsOptions } from './useSessionActions';
import { BackendUnavailableError } from '../../lib/api';

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
vi.mock('../../lib/api', () => ({
  api: { revertSession, unrevertSession },
  BackendUnavailableError: class BackendUnavailableError extends Error {},
}));
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
  it('queue=true: POSTs with the queue flag and shows no optimistic bubble', async () => {
    const { result } = renderHook(() => useSessionActions(makeOptions({ current: true })));

    await act(async () => {
      await result.current.handleSend('follow up', undefined, true);
    });

    expect(begin).not.toHaveBeenCalled(); // no thread bubble; it's held
    // queue=true (last arg) tells the server to hold it for the next idle edge.
    expect(sendMessage).toHaveBeenCalledWith(
      'sess-1', 'follow up', undefined, 'anthropic/x', 'build', undefined, 'opencode', true,
    );
  });

  // Regression: a plain Enter send mid-turn used to be force-queued,
  // so it only reached the agent after the whole turn ended. It must go
  // out immediately (queue flag falsy) and get an optimistic bubble.
  it('mid-turn without the queue flag: sends now, optimistic bubble and all', async () => {
    const { result } = renderHook(() => useSessionActions(makeOptions({ current: true })));

    await act(async () => {
      await result.current.handleSend('interleave me');
    });

    expect(begin).toHaveBeenCalledTimes(1);
    // performSend omits the queue flag entirely — the server sends now.
    expect(sendMessage).toHaveBeenCalledWith(
      'sess-1', 'interleave me', undefined, 'anthropic/x', 'build', undefined, 'opencode',
    );
  });

  it('idle: uses the optimistic-bubble path', async () => {
    const { result } = renderHook(() => useSessionActions(makeOptions({ current: false })));

    await act(async () => {
      await result.current.handleSend('first message');
    });

    expect(begin).toHaveBeenCalledTimes(1);
    // performSend still POSTs the message.
    expect(sendMessage).toHaveBeenCalled();
  });

  it('lets the composer retry backend outages instead of recording a failed send', async () => {
    sendMessage.mockRejectedValueOnce(new BackendUnavailableError());
    const options = makeOptions({ current: false });
    const { result } = renderHook(() => useSessionActions(options));

    await expect(act(async () => result.current.handleSend('keep trying'))).rejects.toBeInstanceOf(BackendUnavailableError);

    expect(options.pending.fail).not.toHaveBeenCalled();
    expect(options.setFailedSends).not.toHaveBeenCalled();
  });

  it('propagates queue failures so the composer keeps the message', async () => {
    const failure = new Error('failed to queue');
    sendMessage.mockRejectedValueOnce(failure);
    const { result } = renderHook(() => useSessionActions(makeOptions({ current: true })));

    await expect(act(async () => result.current.handleSend('keep queued', undefined, true))).rejects.toBe(failure);
  });

  it('records and propagates immediate failures so the composer does not clear', async () => {
    const failure = new Error('send rejected');
    sendMessage.mockRejectedValueOnce(failure);
    const options = makeOptions({ current: false });
    const { result } = renderHook(() => useSessionActions(options));

    await expect(act(async () => result.current.handleSend('keep this'))).rejects.toBe(failure);
    expect(options.pending.fail).toHaveBeenCalledWith('send rejected');
    expect(options.setFailedSends).toHaveBeenCalled();
  });
});
