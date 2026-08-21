// @vitest-environment jsdom
//
// #529: when the URL session id changes there is a one-commit window
// before useSession replaces the session state. During that window the
// `session` prop still holds the PREVIOUS session, so a send fired in
// the window would be delivered to the old session. handleSend and
// handleRetrySend must fail closed when the session prop doesn't match
// the route id.

import { describe, it, expect, beforeEach, vi } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { createRef } from 'react';
import type { MutableRefObject } from 'react';
import { useSessionActions, type UseSessionActionsOptions } from './useSessionActions';

const { sendMessage, runShell } = vi.hoisted(() => ({
  sendMessage: vi.fn().mockResolvedValue(undefined),
  runShell: vi.fn().mockResolvedValue(undefined),
}));

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
vi.mock('../../lib/api', () => ({
  api: { runShell },
  BackendUnavailableError: class BackendUnavailableError extends Error {},
}));
vi.mock('../../lib/remoteLog', () => ({ remoteLog: { error: vi.fn() } }));

const begin = vi.fn().mockReturnValue('entry-1');

function makeOptions(overrides?: Partial<UseSessionActionsOptions>): UseSessionActionsOptions {
  return {
    session: { id: 'sess-old', platform: 'opencode', directory: '/p', timeUpdated: 0 },
    routeSessionId: 'sess-old',
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
    isRunningRef: { current: false },
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
    ...overrides,
  };
}

beforeEach(() => {
  sendMessage.mockClear();
  runShell.mockClear();
  begin.mockClear();
});

describe('useSessionActions — stale route window (#529)', () => {
  it('drops a send when the session prop lags the route id', async () => {
    // Route already points at sess-new; session state still sess-old.
    const options = makeOptions({ routeSessionId: 'sess-new' });
    const { result } = renderHook(() => useSessionActions(options));

    await act(async () => {
      await result.current.handleSend('meant for sess-new');
    });

    expect(sendMessage).not.toHaveBeenCalled();
    expect(begin).not.toHaveBeenCalled();
  });

  it('drops a queued send (queue=true) when the session prop lags the route id', async () => {
    const options = makeOptions({ routeSessionId: 'sess-new' });
    const { result } = renderHook(() => useSessionActions(options));

    await act(async () => {
      await result.current.handleSend('queued for sess-new', undefined, true);
    });

    expect(sendMessage).not.toHaveBeenCalled();
  });

  it('drops a retry when the session prop lags the route id', async () => {
    const options = makeOptions({
      routeSessionId: 'sess-new',
      failedSends: [{ id: 'entry-1', text: 'stale retry', error: 'boom', failedAt: 1 }],
    });
    const { result } = renderHook(() => useSessionActions(options));

    act(() => {
      result.current.handleRetrySend('entry-1');
    });

    expect(sendMessage).not.toHaveBeenCalled();
  });

  it('drops a shell command when the session prop lags the route id', async () => {
    const options = makeOptions({ routeSessionId: 'sess-new' });
    const { result } = renderHook(() => useSessionActions(options));

    await act(async () => {
      await result.current.handleShell('rm -rf build');
    });

    expect(runShell).not.toHaveBeenCalled();
  });

  it('sends normally when session and route agree', async () => {
    const { result } = renderHook(() => useSessionActions(makeOptions()));

    await act(async () => {
      await result.current.handleSend('hello');
    });

    expect(sendMessage).toHaveBeenCalledWith(
      'sess-old', 'hello', undefined, 'anthropic/x', 'build', undefined, 'opencode',
    );
  });
});
