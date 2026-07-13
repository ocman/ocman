// @vitest-environment jsdom
//
// Tests for the `/copy` command in useSessionActions: it serializes the
// session transcript, writes it to the clipboard, and reports success or
// failure via the copy toast. Copy is client-only — it works even when
// OpenCode is not running (portAvailable false).

import { describe, it, expect, beforeEach, vi } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { createRef } from 'react';
import type { MutableRefObject } from 'react';
import { useSessionActions, type UseSessionActionsOptions } from './useSessionActions';
import { copyTextToClipboard } from '../../lib/clipboard';
import type { Message, Part } from '../../lib/api';

vi.mock('../../lib/apiStore', () => ({
  useApiStore: Object.assign(
    (selector: (s: Record<string, unknown>) => unknown) =>
      selector({
        sendMessage: vi.fn(),
        abortSession: vi.fn(),
        archiveSession: vi.fn(),
        createSession: vi.fn(),
        launchOpencodeInTmux: vi.fn(),
        seedNewSession: vi.fn(),
      }),
    { getState: () => ({ pushClosedSession: vi.fn(), patchRecentSession: vi.fn() }) },
  ),
}));

vi.mock('../../lib/clipboard', () => ({ copyTextToClipboard: vi.fn() }));

const copyMock = vi.mocked(copyTextToClipboard);

const messages: Message[] = [{ id: 'm1', sessionId: 's', timeCreated: 1, data: { role: 'user' } }];
const parts: Part[] = [{ id: 'p1', messageId: 'm1', sessionId: 's', data: { type: 'text', text: 'hello' } }];

function makeOptions(over: Partial<UseSessionActionsOptions> = {}): UseSessionActionsOptions {
  const pending = { pending: null, begin: vi.fn(), fail: vi.fn(), clear: vi.fn() };
  return {
    session: { id: 'sess-1', platform: 'opencode', directory: '/p', title: 'My Session', timeUpdated: 0 },
    portAvailable: false,
    caps: {} as UseSessionActionsOptions['caps'],
    pendingPermission: null,
    pendingQuestion: null,
    selectedModel: '',
    selectedAgent: '',
    selectedReasoning: '',
    activeAgent: '',
    recentSessionsRef: createRef<Array<{ id: string }>>() as MutableRefObject<Array<{ id: string }>>,
    messagesRef: { current: messages },
    partsRef: { current: parts },
    isRunningRef: { current: false },
    tmuxAvailable: true,
    failedSends: [],
    setFailedSends: vi.fn(),
    pending: pending as unknown as UseSessionActionsOptions['pending'],
    navigate: vi.fn(),
    navigateToSession: vi.fn(),
    openWorktreeForm: vi.fn(),
    handleCompact: vi.fn(),
    handleNewSession: vi.fn(),
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

beforeEach(() => copyMock.mockReset());

describe('useSessionActions — /copy', () => {
  it('copies the transcript and shows a success toast', async () => {
    copyMock.mockResolvedValue(true);
    const setCopyToastMessage = vi.fn();
    const { result } = renderHook(() => useSessionActions(makeOptions({ setCopyToastMessage })));
    await act(async () => { await result.current.handleCommand('copy', ''); });
    expect(copyMock).toHaveBeenCalledTimes(1);
    expect(copyMock.mock.calls[0][0]).toContain('# My Session');
    expect(copyMock.mock.calls[0][0]).toContain('hello');
    expect(setCopyToastMessage).toHaveBeenCalledWith('Transcript copied');
  });

  it('shows an error toast when the clipboard write fails', async () => {
    copyMock.mockResolvedValue(false);
    const setCopyToastMessage = vi.fn();
    const { result } = renderHook(() => useSessionActions(makeOptions({ setCopyToastMessage })));
    await act(async () => { await result.current.handleCommand('copy', ''); });
    expect(setCopyToastMessage).toHaveBeenCalledWith('Copy failed — clipboard unavailable');
  });
});
