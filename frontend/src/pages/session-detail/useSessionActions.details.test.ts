// @vitest-environment jsdom
//
// Tests for the `/details` command path in useSessionActions. It is a
// pure client-side UI toggle over the persisted uiStore flag
// (showToolDetails), so it must flip the store and must work even when
// the live OpenCode port is unavailable.

import { describe, it, expect, beforeEach, vi } from 'vitest';

// jsdom's localStorage is only partially implemented in this setup; the
// zustand persist middleware calls setItem on every write. Install a
// minimal in-memory stub before any module (uiStore) is imported —
// vi.hoisted runs ahead of the hoisted import statements.
vi.hoisted(() => {
  const mem = new Map<string, string>();
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: {
      getItem: (k: string) => mem.get(k) ?? null,
      setItem: (k: string, v: string) => void mem.set(k, v),
      removeItem: (k: string) => void mem.delete(k),
      clear: () => mem.clear(),
    },
  });
});

import { renderHook, act } from '@testing-library/react';
import { createRef } from 'react';
import type { MutableRefObject } from 'react';
import { useSessionActions, type UseSessionActionsOptions } from './useSessionActions';
import { useUiStore } from '../../lib/uiStore';

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
    { getState: () => ({ pushClosedSession: vi.fn() }) },
  ),
}));

vi.mock('../../lib/api', () => ({
  api: { executeCommand: vi.fn(), debugLog: vi.fn().mockResolvedValue(undefined) },
}));

function makeOptions(over: Partial<UseSessionActionsOptions> = {}): UseSessionActionsOptions {
  const pending = { pending: null, begin: vi.fn(), fail: vi.fn(), clear: vi.fn() };
  return {
    session: { id: 'sess-1', platform: 'opencode', directory: '/p', timeUpdated: 0 },
    portAvailable: true,
    caps: { shellExec: true } as UseSessionActionsOptions['caps'],
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
    tmuxAvailable: true,
    failedSends: [],
    setFailedSends: vi.fn(),
    pending: pending as unknown as UseSessionActionsOptions['pending'],
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
    ...over,
  };
}

beforeEach(() => {
  useUiStore.setState({ showToolDetails: true });
});

describe('useSessionActions — /details', () => {
  it('toggles the persisted tool-detail visibility flag', async () => {
    const opts = makeOptions();
    const { result } = renderHook(() => useSessionActions(opts));

    await act(async () => {
      await result.current.handleCommand('details', '');
    });
    expect(useUiStore.getState().showToolDetails).toBe(false);

    await act(async () => {
      await result.current.handleCommand('details', '');
    });
    expect(useUiStore.getState().showToolDetails).toBe(true);
  });

  it('works even when the live port is unavailable', async () => {
    const opts = makeOptions({ portAvailable: false });
    const { result } = renderHook(() => useSessionActions(opts));

    await act(async () => {
      await result.current.handleCommand('details', '');
    });
    expect(useUiStore.getState().showToolDetails).toBe(false);
  });
});
