// @vitest-environment jsdom
//
// Tests for the `/fork` and `/move` commands in useSessionActions.
// Fork navigates to the new session; move (first cut) takes the target
// directory as its argument and optimistically patches the sidebar store.

import { describe, it, expect, beforeEach, vi } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { createRef } from 'react';
import type { MutableRefObject } from 'react';
import { useSessionActions, type UseSessionActionsOptions } from './useSessionActions';
import { api } from '../../lib/api';

const patchRecentSession = vi.fn();

vi.mock('../../lib/apiStore', () => ({
  useApiStore: Object.assign(
    (selector: (s: Record<string, unknown>) => unknown) =>
      selector({
        sendMessage: vi.fn().mockResolvedValue(undefined),
        abortSession: vi.fn().mockResolvedValue(undefined),
        archiveSession: vi.fn().mockResolvedValue(undefined),
        createSession: vi.fn().mockResolvedValue(undefined),
        launchOpencodeInTmux: vi.fn().mockResolvedValue(undefined),
        seedNewSession: vi.fn(),
      }),
    { getState: () => ({ pushClosedSession: vi.fn(), patchRecentSession }) },
  ),
}));

vi.mock('../../lib/api', () => ({
  api: {
    forkSession: vi.fn().mockResolvedValue({ id: 'sess-forked' }),
    moveSession: vi.fn().mockResolvedValue(undefined),
  },
}));

const forkSession = vi.mocked(api.forkSession);
const moveSession = vi.mocked(api.moveSession);
const navigateToSession = vi.fn();
const failFn = vi.fn();
const setShowForkPicker = vi.fn();

function makeOptions(caps: Partial<UseSessionActionsOptions['caps']> = {}): UseSessionActionsOptions {
  return {
    session: { id: 'sess-1', platform: 'opencode', directory: '/p', timeUpdated: 0 },
    portAvailable: true,
    caps: { fork: true, move: true, ...caps } as UseSessionActionsOptions['caps'],
    pendingPermission: null,
    pendingQuestion: null,
    selectedModel: '',
    selectedAgent: '',
    selectedReasoning: '',
    activeAgent: '',
    recentSessionsRef: createRef<Array<{ id: string }>>() as MutableRefObject<Array<{ id: string }>>,
    messagesRef: { current: [] },
    partsRef: { current: [] },
    isRunningRef: { current: false },
    tmuxAvailable: false,
    failedSends: [],
    setFailedSends: vi.fn(),
    pending: {
      pending: null,
      begin: vi.fn(),
      fail: failFn,
      clear: vi.fn(),
    } as unknown as UseSessionActionsOptions['pending'],
    navigate: vi.fn(),
    navigateToSession,
    openWorktreeForm: vi.fn(),
    handleCompact: vi.fn().mockResolvedValue(undefined),
    handleNewSession: vi.fn().mockResolvedValue(undefined),
    handleTmuxShortcut: vi.fn(),
    setShowRenameModal: vi.fn(),
    setShowForkPicker,
    setShowRenameToast: vi.fn(),
    setShowDisconnectedToast: vi.fn(),
    setRestartToastMessage: vi.fn(),
    setCopyToastMessage: vi.fn(),
  };
}

beforeEach(() => {
  forkSession.mockClear();
  moveSession.mockClear();
  navigateToSession.mockClear();
  patchRecentSession.mockClear();
  failFn.mockClear();
  setShowForkPicker.mockClear();
});

describe('useSessionActions — /fork command', () => {
  it('opens the fork-point picker', async () => {
    const { result } = renderHook(() => useSessionActions(makeOptions()));

    await act(async () => {
      await result.current.handleCommand('fork', '');
    });

    expect(setShowForkPicker).toHaveBeenCalledWith(true);
    expect(forkSession).not.toHaveBeenCalled();
  });

  it('is a no-op when the platform lacks the fork capability', async () => {
    const { result } = renderHook(() => useSessionActions(makeOptions({ fork: false })));

    await act(async () => {
      await result.current.handleCommand('fork', '');
    });

    expect(forkSession).not.toHaveBeenCalled();
  });
});

describe('useSessionActions — /move command', () => {
  it('moves the session to the given directory and patches the sidebar', async () => {
    const { result } = renderHook(() => useSessionActions(makeOptions()));

    await act(async () => {
      await result.current.handleCommand('move', '  /tmp/dst  ');
    });

    expect(moveSession).toHaveBeenCalledWith('sess-1', '/tmp/dst');
    expect(patchRecentSession).toHaveBeenCalledWith('sess-1', { directory: '/tmp/dst' });
  });

  it('reports usage and does not call the API when no directory is given', async () => {
    const { result } = renderHook(() => useSessionActions(makeOptions()));

    await act(async () => {
      await result.current.handleCommand('move', '   ');
    });

    expect(moveSession).not.toHaveBeenCalled();
    expect(failFn).toHaveBeenCalled();
  });
});
