// @vitest-environment jsdom
//
// Tests for the `/thinking` command in useSessionActions (#290). The
// command is a display-only toggle over ocman's own reasoning-block
// visibility (useUiStore.showReasoning) — it never touches the agent,
// so it must work regardless of portAvailable and never call the API.
//
// useUiStore is mocked here so the persist middleware (which binds a
// jsdom-broken localStorage at import time) stays out of the way; the
// store's own behaviour is covered in uiStore.test.ts. What we verify
// here is the command wiring: that handleCommand dispatches the right
// setter for each arg and short-circuits before the API call.

import { describe, it, expect, beforeEach, vi } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { createRef } from 'react';
import type { MutableRefObject } from 'react';
import { useSessionActions, type UseSessionActionsOptions } from './useSessionActions';
import { api } from '../../lib/api';

const setShowReasoning = vi.fn();
const toggleShowReasoning = vi.fn();

vi.mock('../../lib/uiStore', () => ({
  useUiStore: { getState: () => ({ setShowReasoning, toggleShowReasoning }) },
}));

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
    { getState: () => ({ pushClosedSession: vi.fn(), patchRecentSession: vi.fn() }) },
  ),
}));

vi.mock('../../lib/api', () => ({
  api: { executeCommand: vi.fn().mockResolvedValue(undefined) },
}));

const executeCommand = vi.mocked(api.executeCommand);

function makeOptions(portAvailable = true): UseSessionActionsOptions {
  return {
    session: { id: 'sess-1', platform: 'opencode', directory: '/p', timeUpdated: 0 },
    portAvailable,
    caps: {} as UseSessionActionsOptions['caps'],
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
    setShowRenameToast: vi.fn(),
    setShowDisconnectedToast: vi.fn(),
    setRestartToastMessage: vi.fn(),
    setCopyToastMessage: vi.fn(),
  };
}

// jsdom in this project ships a localStorage without a working
// setItem, which the persist middleware calls on every write. Plant a
// minimal in-memory stub so store mutations don't throw.
beforeEach(() => {
  executeCommand.mockClear();
  setShowReasoning.mockClear();
  toggleShowReasoning.mockClear();
});

describe('useSessionActions — /thinking command (#290)', () => {
  it('toggles on a bare command', async () => {
    const { result } = renderHook(() => useSessionActions(makeOptions()));

    await act(async () => {
      await result.current.handleCommand('thinking', '');
    });

    expect(toggleShowReasoning).toHaveBeenCalledTimes(1);
    expect(setShowReasoning).not.toHaveBeenCalled();
  });

  it('sets an explicit state with on/off args (case-insensitive)', async () => {
    const { result } = renderHook(() => useSessionActions(makeOptions()));

    await act(async () => {
      await result.current.handleCommand('thinking', 'off');
    });
    await act(async () => {
      await result.current.handleCommand('thinking', ' ON ');
    });

    expect(setShowReasoning.mock.calls).toEqual([[false], [true]]);
    expect(toggleShowReasoning).not.toHaveBeenCalled();
  });

  it('works when the port is unavailable and never calls the agent', async () => {
    const { result } = renderHook(() => useSessionActions(makeOptions(false)));

    await act(async () => {
      await result.current.handleCommand('thinking', '');
    });

    expect(toggleShowReasoning).toHaveBeenCalledTimes(1);
    expect(executeCommand).not.toHaveBeenCalled();
  });
});
