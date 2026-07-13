// @vitest-environment jsdom
//
// Tests for the /sessions command (aliases /resume, /continue) in
// useSessionActions. It's a thin shim that opens the existing command
// palette (whose default mode is the session switcher) — no backend,
// no new picker (#292).

import { describe, it, expect, beforeEach, vi } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { createRef } from 'react';
import type { MutableRefObject } from 'react';
import { useSessionActions, type UseSessionActionsOptions } from './useSessionActions';

const openCommandPalette = vi.fn();

vi.mock('../../lib/uiStore', () => ({
  useUiStore: Object.assign((selector: (s: Record<string, unknown>) => unknown) => selector({}), {
    getState: () => ({ openCommandPalette }),
  }),
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

function makeOptions(overrides: Partial<UseSessionActionsOptions> = {}): UseSessionActionsOptions {
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
    ...overrides,
  };
}

beforeEach(() => {
  openCommandPalette.mockClear();
});

describe('useSessionActions — /sessions switcher shim (#292)', () => {
  it.each(['sessions', 'resume', 'continue'])(
    '/%s opens the command palette (session switcher)',
    async (command) => {
      const { result } = renderHook(() => useSessionActions(makeOptions()));

      await act(async () => {
        await result.current.handleCommand(command, '');
      });

      expect(openCommandPalette).toHaveBeenCalledTimes(1);
    },
  );

  it('opens the switcher even when the OpenCode port is unavailable', async () => {
    const { result } = renderHook(() =>
      useSessionActions(makeOptions({ portAvailable: false })),
    );

    await act(async () => {
      await result.current.handleCommand('sessions', '');
    });

    expect(openCommandPalette).toHaveBeenCalledTimes(1);
  });
});
