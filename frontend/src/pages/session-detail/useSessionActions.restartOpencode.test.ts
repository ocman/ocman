// @vitest-environment jsdom
//
// Tests for the `/restart-opencode` command path in useSessionActions.
// On success it walks the toast through "Restarting..." -> "Restarted"
// and clears the pending indicator; on failure it hides the toast and
// reports the error via pending.fail.

import { describe, it, expect, beforeEach, vi } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { createRef } from 'react';
import type { MutableRefObject } from 'react';
import { useSessionActions, type UseSessionActionsOptions } from './useSessionActions';
import { api } from '../../lib/api';

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
  api: { restartOpencode: vi.fn(), debugLog: vi.fn().mockResolvedValue(undefined) },
}));

const restartOpencode = vi.mocked(api.restartOpencode);

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
    setShowRenameToast: vi.fn(),
    setShowDisconnectedToast: vi.fn(),
    setRestartToastMessage: vi.fn(),
    ...over,
  };
}

beforeEach(() => {
  restartOpencode.mockReset();
});

describe('useSessionActions — /restart-opencode', () => {
  it('shows progress then success toast on a successful restart', async () => {
    restartOpencode.mockResolvedValue({ target: 'repo:oc' });
    const setRestartToastMessage = vi.fn();
    const pending = { pending: null, begin: vi.fn(), fail: vi.fn(), clear: vi.fn() };
    const opts = makeOptions({
      setRestartToastMessage,
      pending: pending as unknown as UseSessionActionsOptions['pending'],
    });
    const { result } = renderHook(() => useSessionActions(opts));

    await act(async () => {
      await result.current.handleCommand('restart-opencode', '');
    });

    expect(restartOpencode).toHaveBeenCalledWith('sess-1');
    expect(setRestartToastMessage.mock.calls).toEqual([
      ['Restarting OpenCode...'],
      ['Restarted OpenCode'],
    ]);
    expect(pending.clear).toHaveBeenCalled();
    expect(pending.fail).not.toHaveBeenCalled();
  });

  it('hides the toast and reports the error when the restart fails', async () => {
    restartOpencode.mockRejectedValue(new Error('no pane'));
    const setRestartToastMessage = vi.fn();
    const pending = { pending: null, begin: vi.fn(), fail: vi.fn(), clear: vi.fn() };
    const opts = makeOptions({
      setRestartToastMessage,
      pending: pending as unknown as UseSessionActionsOptions['pending'],
    });
    const { result } = renderHook(() => useSessionActions(opts));

    await act(async () => {
      await result.current.handleCommand('restart-opencode', '');
    });

    expect(setRestartToastMessage.mock.calls).toEqual([
      ['Restarting OpenCode...'],
      [null],
    ]);
    expect(pending.fail).toHaveBeenCalledWith('no pane');
    expect(pending.clear).not.toHaveBeenCalled();
  });
});
