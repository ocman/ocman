// @vitest-environment jsdom
//
// Tests for the `/rename` command in useSessionActions. Renaming must
// optimistically patch the sidebar store (patchRecentSession) so the new
// title shows immediately instead of waiting for the 3s recent-sessions poll.

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
  api: { renameSession: vi.fn().mockResolvedValue(undefined) },
}));

const renameSession = vi.mocked(api.renameSession);

function makeOptions(): UseSessionActionsOptions {
  return {
    session: { id: 'sess-1', platform: 'opencode', directory: '/p', timeUpdated: 0 },
    portAvailable: true,
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
    setShowMovePicker: vi.fn(),
    setShowRenameToast: vi.fn(),
    setShowDisconnectedToast: vi.fn(),
    setRestartToastMessage: vi.fn(),
    setCopyToastMessage: vi.fn(),
  };
}

beforeEach(() => {
  renameSession.mockClear();
  patchRecentSession.mockClear();
});

describe('useSessionActions — /rename command', () => {
  it('renames the session and optimistically patches the sidebar store', async () => {
    const { result } = renderHook(() => useSessionActions(makeOptions()));

    await act(async () => {
      await result.current.handleCommand('rename', '  New Title  ');
    });

    expect(renameSession).toHaveBeenCalledWith('sess-1', 'New Title');
    expect(patchRecentSession).toHaveBeenCalledWith('sess-1', { title: 'New Title' });
  });

  it('is a no-op when the title is blank', async () => {
    const { result } = renderHook(() => useSessionActions(makeOptions()));

    await act(async () => {
      await result.current.handleCommand('rename', '   ');
    });

    expect(renameSession).not.toHaveBeenCalled();
    expect(patchRecentSession).not.toHaveBeenCalled();
  });
});
