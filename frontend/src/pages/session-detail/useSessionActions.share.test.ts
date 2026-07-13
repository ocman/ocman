// @vitest-environment jsdom
//
// Tests for the `/share` command path in useSessionActions (issue #294).
// It copies THIS ocman instance's session URL to the clipboard and
// surfaces a toast with the reachability caveat — no OpenCode cloud
// share involved.

import { describe, it, expect, beforeEach, vi } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { createRef } from 'react';
import type { MutableRefObject } from 'react';
import { useSessionActions, type UseSessionActionsOptions } from './useSessionActions';
import { copyToClipboard } from '../../lib/clipboard';

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
  api: { debugLog: vi.fn().mockResolvedValue(undefined) },
}));

vi.mock('../../lib/clipboard', () => ({ copyToClipboard: vi.fn() }));

const copyMock = vi.mocked(copyToClipboard);

function makeOptions(over: Partial<UseSessionActionsOptions> = {}): UseSessionActionsOptions {
  const pending = { pending: null, begin: vi.fn(), fail: vi.fn(), clear: vi.fn() };
  return {
    session: { id: 'sess 1', platform: 'opencode', directory: '/p', timeUpdated: 0 },
    portAvailable: false, // share must work even when the platform is offline
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
    setShowMovePicker: vi.fn(),
    setShowRenameToast: vi.fn(),
    setShowDisconnectedToast: vi.fn(),
    setRestartToastMessage: vi.fn(),
    setCopyToastMessage: vi.fn(),
    ...over,
  };
}

beforeEach(() => {
  copyMock.mockReset();
});

describe('useSessionActions — /share', () => {
  it('copies the origin-based session URL and toasts success', async () => {
    copyMock.mockResolvedValue(true);
    const setRestartToastMessage = vi.fn();
    const opts = makeOptions({ setRestartToastMessage });
    const { result } = renderHook(() => useSessionActions(opts));

    await act(async () => {
      await result.current.handleCommand('share', '');
    });

    // origin comes from jsdom (http://localhost); id is URL-encoded.
    expect(copyMock).toHaveBeenCalledWith(`${window.location.origin}/session/sess%201`);
    expect(setRestartToastMessage).toHaveBeenCalledWith(
      'Session link copied (reachable only where this ocman instance is)',
    );
  });

  it('toasts a failure message when the clipboard write fails', async () => {
    copyMock.mockResolvedValue(false);
    const setRestartToastMessage = vi.fn();
    const opts = makeOptions({ setRestartToastMessage });
    const { result } = renderHook(() => useSessionActions(opts));

    await act(async () => {
      await result.current.handleCommand('share', '');
    });

    expect(setRestartToastMessage).toHaveBeenCalledWith('Could not copy link to clipboard');
  });
});
