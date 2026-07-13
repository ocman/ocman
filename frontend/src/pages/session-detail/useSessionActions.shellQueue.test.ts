// @vitest-environment jsdom
//
// Tests for the `!`-prefixed shell-command queue in useSessionActions.
//
// OpenCode rejects POST /session/{id}/shell while the session is
// streaming an assistant response. Rather than fire-and-fail silently,
// handleShell queues the command when `isRunningRef.current` is true
// and SessionDetail flushes it once the turn finishes. These tests pin
// that behaviour at the hook level.

import { describe, it, expect, beforeEach, vi } from 'vitest';
import { renderHook, act, waitFor } from '@testing-library/react';
import { createRef } from 'react';
import type { MutableRefObject } from 'react';
import { useSessionActions, type UseSessionActionsOptions } from './useSessionActions';
import { api } from '../../lib/api';

// The hook reads a few zustand actions; stub them to no-ops.
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
  api: { runShell: vi.fn().mockResolvedValue(undefined) },
}));

const runShell = vi.mocked(api.runShell);

function makeOptions(
  isRunningRef: MutableRefObject<boolean>,
): UseSessionActionsOptions {
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
    isRunningRef,
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

beforeEach(() => {
  runShell.mockClear();
});

describe('useSessionActions — shell command queue', () => {
  it('runs the shell command immediately when the session is idle', async () => {
    const isRunningRef = { current: false };
    const { result } = renderHook(() => useSessionActions(makeOptions(isRunningRef)));

    await act(async () => {
      await result.current.handleShell('ls -la');
    });

    expect(runShell).toHaveBeenCalledWith('sess-1', 'ls -la', 'build');
    expect(result.current.queuedShellCommand).toBe(null);
  });

  it('queues the command (does not run it) while the session is streaming', async () => {
    const isRunningRef = { current: true };
    const { result } = renderHook(() => useSessionActions(makeOptions(isRunningRef)));

    await act(async () => {
      await result.current.handleShell('git status');
    });

    expect(runShell).not.toHaveBeenCalled();
    expect(result.current.queuedShellCommand).toBe('git status');
  });

  it('flushes the queued command once the session goes idle', async () => {
    const isRunningRef = { current: true };
    const { result } = renderHook(() => useSessionActions(makeOptions(isRunningRef)));

    await act(async () => {
      await result.current.handleShell('npm test');
    });
    expect(runShell).not.toHaveBeenCalled();

    // Simulate the SessionDetail isRunning true -> false transition.
    isRunningRef.current = false;
    act(() => {
      result.current.flushQueuedShell();
    });

    await waitFor(() => expect(runShell).toHaveBeenCalledWith('sess-1', 'npm test', 'build'));
    expect(result.current.queuedShellCommand).toBe(null);
  });

  it('does not flush while still running', async () => {
    const isRunningRef = { current: true };
    const { result } = renderHook(() => useSessionActions(makeOptions(isRunningRef)));

    await act(async () => {
      await result.current.handleShell('echo hi');
    });

    act(() => {
      result.current.flushQueuedShell(); // still running
    });

    expect(runShell).not.toHaveBeenCalled();
    expect(result.current.queuedShellCommand).toBe('echo hi');
  });

  it('cancelQueuedShell drops the command without running it', async () => {
    const isRunningRef = { current: true };
    const { result } = renderHook(() => useSessionActions(makeOptions(isRunningRef)));

    await act(async () => {
      await result.current.handleShell('rm -rf nope');
    });
    expect(result.current.queuedShellCommand).toBe('rm -rf nope');

    act(() => {
      result.current.cancelQueuedShell();
    });
    expect(result.current.queuedShellCommand).toBe(null);

    // Flushing after cancel must be a no-op.
    isRunningRef.current = false;
    act(() => {
      result.current.flushQueuedShell();
    });
    expect(runShell).not.toHaveBeenCalled();
  });

  it('replaces an already-queued command when a new one is submitted', async () => {
    const isRunningRef = { current: true };
    const { result } = renderHook(() => useSessionActions(makeOptions(isRunningRef)));

    await act(async () => {
      await result.current.handleShell('first');
    });
    await act(async () => {
      await result.current.handleShell('second');
    });
    expect(result.current.queuedShellCommand).toBe('second');

    isRunningRef.current = false;
    act(() => {
      result.current.flushQueuedShell();
    });
    await waitFor(() => expect(runShell).toHaveBeenCalledTimes(1));
    expect(runShell).toHaveBeenCalledWith('sess-1', 'second', 'build');
  });
});
